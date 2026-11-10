package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// mcpActivityKeep is how long the activity log is kept.
const mcpActivityKeep = 90 * 24 * time.Hour

// mcpClientName names the credential a request used: the OAuth client of the grant, or the
// Application Password's name.
func (s *Server) mcpClientName(c *gin.Context) string {
	var name string
	if id, ok := c.Get(restOAuthKey); ok {
		_ = s.store.Pool.QueryRow(c, `SELECT cl.name FROM oauth_tokens t JOIN oauth_clients cl ON cl.client_id = t.client_id WHERE t.id = $1`, id).Scan(&name)
		return "OAuth: " + name
	}
	if id, ok := c.Get(restAppPasswordKey); ok {
		_ = s.store.Pool.QueryRow(c, `SELECT name FROM app_passwords WHERE id = $1`, id).Scan(&name)
		return "Application Password: " + name
	}
	return "Session"
}

// mcpLogActivity records a tool call that is not a plain read. The route is logged without its query
// string and body, so no content or secret lands in the log.
func (s *Server) mcpLogActivity(c *gin.Context, tool, method, route string, status int, took time.Duration) {
	if method == http.MethodGet || method == http.MethodHead || tool == "seo_analyze" {
		return
	}
	var uid int64
	if u := restCurrent(c); u != nil {
		uid = u.ID
	}
	if i := strings.IndexByte(route, '?'); i >= 0 {
		route = route[:i]
	}
	if _, err := s.store.Pool.Exec(c, `INSERT INTO mcp_activity (user_id, client, tool, method, route, status, duration_ms, ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, uid, s.mcpClientName(c), tool, method, route, status, int(took.Milliseconds()), clientIP(c)); err == nil {
		s.asyncExec(`DELETE FROM mcp_activity WHERE created_at < $1`, time.Now().Add(-mcpActivityKeep))
	}
}

// pursaeMCPActivity is GET /pursae/v1/mcp/activity: the newest tool calls that wrote, filterable by
// tool and client.
func (s *Server) pursaeMCPActivity(c *gin.Context, _ *restRequest) {
	per, _ := strconv.Atoi(c.Query("per_page"))
	if per < 1 || per > 100 {
		per = 25
	}
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	rows, err := s.store.Pool.Query(c, `SELECT id, created_at, user_id, client, tool, method, route, status, duration_ms, ip FROM mcp_activity
		WHERE ($1 = '' OR tool = $1) AND ($2 = '' OR client ILIKE '%' || $2 || '%') ORDER BY id DESC LIMIT $3 OFFSET $4`,
		c.Query("tool"), c.Query("client"), per, (page-1)*per)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_activity_failed", "Could not read the activity log.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, uid int64
		var at time.Time
		var client, tool, method, route, ip string
		var status, ms int
		if rows.Scan(&id, &at, &uid, &client, &tool, &method, &route, &status, &ms, &ip) != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "date_gmt": at.UTC().Format("2006-01-02T15:04:05"), "user": uid, "client": client,
			"tool": tool, "method": method, "route": route, "status": status, "duration_ms": ms, "ip": ip})
	}
	restJSON(c, http.StatusOK, out)
}
