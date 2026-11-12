package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Tools › AI Access is the admin side of the MCP server (mcp.go): how to connect an AI tool, every
// OAuth grant on the site with Revoke (each user also sees their own on the profile), and the
// activity log of tool calls that wrote (mcp_audit.go), filterable by tool and client.

type aiActivityRow struct {
	ID       int64
	At       string
	User     string
	Client   string
	Tool     string
	Method   string
	Route    string
	Status   int
	Duration int
	IP       string
}

type aiGrantRow struct {
	ID                          int64
	User, Client, Created, Used string
	Scopes                      string
}

const aiActivityPerPage = 50

func (s *Server) aiAccessScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to manage AI access on this site.")
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "ai-access") {
			s.denyNonce(c)
			return
		}
		var n int64
		switch c.PostForm("do") {
		case "revoke":
			tag, _ := s.store.Pool.Exec(c, `DELETE FROM oauth_tokens WHERE id::text = $1`, c.PostForm("grant"))
			n = tag.RowsAffected()
		case "clear-log":
			tag, _ := s.store.Pool.Exec(c, `DELETE FROM mcp_activity`)
			n = tag.RowsAffected()
		}
		c.Redirect(http.StatusSeeOther, "/wp-admin/ai-access?"+url.Values{"done": {c.PostForm("do")}, "n": {strconv.FormatInt(n, 10)}}.Encode())
		return
	}

	tool, client := c.Query("tool"), c.Query("client")
	page, _ := strconv.Atoi(c.Query("paged"))
	if page < 1 {
		page = 1
	}
	loc := s.loc()
	var total int
	_ = s.store.Pool.QueryRow(c, `SELECT count(*) FROM mcp_activity WHERE ($1 = '' OR tool = $1) AND ($2 = '' OR client ILIKE '%' || $2 || '%')`,
		tool, client).Scan(&total)
	var activity []aiActivityRow
	if rows, err := s.store.Pool.Query(c, `SELECT a.id, a.created_at, coalesce(u.login, ''), a.client, a.tool, a.method, a.route, a.status, a.duration_ms, a.ip
		FROM mcp_activity a LEFT JOIN users u ON u.id = a.user_id
		WHERE ($1 = '' OR a.tool = $1) AND ($2 = '' OR a.client ILIKE '%' || $2 || '%') ORDER BY a.id DESC LIMIT $3 OFFSET $4`,
		tool, client, aiActivityPerPage, (page-1)*aiActivityPerPage); err == nil {
		for rows.Next() {
			var r aiActivityRow
			var at time.Time
			if rows.Scan(&r.ID, &at, &r.User, &r.Client, &r.Tool, &r.Method, &r.Route, &r.Status, &r.Duration, &r.IP) == nil {
				r.At = at.In(loc).Format("2006-01-02 15:04:05")
				activity = append(activity, r)
			}
		}
		rows.Close()
	}
	var tools []string
	if rows, err := s.store.Pool.Query(c, `SELECT DISTINCT tool FROM mcp_activity ORDER BY tool`); err == nil {
		for rows.Next() {
			var t string
			if rows.Scan(&t) == nil {
				tools = append(tools, t)
			}
		}
		rows.Close()
	}
	var grants []aiGrantRow
	if rows, err := s.store.Pool.Query(c, `SELECT t.id, coalesce(u.login, ''), cl.name, t.created_at, t.last_used_at, t.scopes FROM oauth_tokens t
		JOIN oauth_clients cl ON cl.client_id = t.client_id LEFT JOIN users u ON u.id = t.user_id ORDER BY t.id DESC`); err == nil {
		for rows.Next() {
			var g aiGrantRow
			var created time.Time
			var used *time.Time
			var scopes []string
			if rows.Scan(&g.ID, &g.User, &g.Client, &created, &used, &scopes) != nil {
				continue
			}
			g.Created, g.Used, g.Scopes = created.In(loc).Format("January 2, 2006"), "—", scopeLabels(scopes)
			if used != nil {
				g.Used = used.In(loc).Format("January 2, 2006")
			}
			grants = append(grants, g)
		}
		rows.Close()
	}

	endpoint := s.oauthIssuer(c) + oauthMCPPath
	remote, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"pursae": map[string]any{"type": "http", "url": endpoint}}}, "", "  ")
	bridge, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"pursae": map[string]any{"command": "wpgo", "args": []string{"mcp", "--url", s.oauthIssuer(c)}}}}, "", "  ")
	pages := (total + aiActivityPerPage - 1) / aiActivityPerPage
	pageURL := func(p int) string {
		q := url.Values{}
		if tool != "" {
			q.Set("tool", tool)
		}
		if client != "" {
			q.Set("client", client)
		}
		if p > 1 {
			q.Set("paged", strconv.Itoa(p))
		}
		if len(q) == 0 {
			return "/wp-admin/ai-access"
		}
		return "/wp-admin/ai-access?" + q.Encode()
	}
	d := map[string]any{
		"Nonce": s.nonce(c, "ai-access"), "Endpoint": endpoint, "SiteURL": s.oauthIssuer(c),
		"RemoteConfig": string(remote), "BridgeConfig": string(bridge),
		"Grants": grants, "Activity": activity, "Tools": tools, "Tool": tool, "Client": client,
		"Total": total, "Page": page, "Pages": pages, "PrevURL": pageURL(page - 1), "NextURL": pageURL(page + 1),
		"KeepDays": int(mcpActivityKeep.Hours() / 24),
	}
	var notices []notice
	switch c.Query("done") {
	case "revoke":
		notices = append(notices, notice{Type: "success", Message: "Access revoked. The application has to be authorized again.", Dismissible: true})
	case "clear-log":
		notices = append(notices, notice{Type: "success", Message: template.HTML("Activity log cleared (" + template.HTMLEscapeString(c.Query("n")) + " entries)."), Dismissible: true})
	}
	s.adminPage(c, "ai-access", "AI Access", "tools_page_ai-access", "admin/ai-access", d, notices...)
}
