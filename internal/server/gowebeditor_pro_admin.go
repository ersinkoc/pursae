package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Golementor Pro admin screens (/wp-admin/web-builder?tab=...): Popups, Form Submissions,
// Custom Code, Custom Fonts, Role Manager and Settings (reCAPTCHA). They replace the upstream
// plugin's admin pages; there is no license, Connect or upgrade screen.

const gweProAdminPath = "/wp-admin/web-builder"

// gweProTabs are the screens in menu order.
var gweProTabs = []struct{ Tab, Title, Cap string }{
	{"popups", "Popups", "edit_posts"},
	{"loops", "Loop Items", "edit_posts"}, // gap-loops: gowebeditor_loop.go
	{"submissions", "Form Submissions", "manage_options"},
	{"code", "Custom Code", "unfiltered_html"},
	{"fonts", "Custom Fonts", "manage_options"},
	{"roles", "Role Manager", "manage_options"},
	{"settings", "Settings", "manage_options"},
}

// gweProAdminData is the data of the admin/gowebeditor-pro template.
type gweProAdminData struct {
	Title string
	Tabs  []gweProTabLink
	Body  template.HTML
}

type gweProTabLink struct {
	URL, Title string
	Active     bool
}

func (s *Server) registerGoWebEditorProAdmin(admin *gin.RouterGroup) {
	admin.GET("/web-builder", s.golementorOnly(), s.gweProAdmin)
	admin.POST("/web-builder", s.golementorOnly(), s.gweProAdmin)
	admin.Match([]string{http.MethodGet, http.MethodPost}, "/web-builder-icons", s.golementorOnly(), s.gweIconsAdmin) // gap-widgets: Custom Icons (gwe_custom_icons.go)
}

func (s *Server) gweProAdmin(c *gin.Context) {
	u := currentUser(c)
	tab := c.DefaultQuery("tab", "popups")
	title, capName := "", ""
	for _, t := range gweProTabs {
		if t.Tab == tab {
			title, capName = t.Title, t.Cap
		}
	}
	if title == "" {
		c.String(http.StatusNotFound, "Unknown screen.")
		return
	}
	if !u.Can(capName, nil) && !(capName == "unfiltered_html" && u.Can("manage_options", nil)) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "gwe-pro-"+tab) {
			s.denyNonce(c)
			return
		}
		msg, err := s.gweProAdminPost(c, tab)
		q := "?tab=" + tab
		if err != nil {
			q += "&error=" + url.QueryEscape(err.Error())
		} else if msg != "" {
			q += "&message=" + url.QueryEscape(msg)
		}
		if id := c.PostForm("redirect_id"); id != "" && err == nil {
			q += "&id=" + url.QueryEscape(id)
		}
		c.Redirect(http.StatusFound, gweProAdminPath+q)
		return
	}
	if tab == "submissions" && c.Query("export") == "csv" {
		s.gweProSubmissionsCSV(c)
		return
	}
	var body string
	switch tab {
	case "popups":
		body = s.gweProPopupsScreen(c)
	case "loops":
		body = s.gweLoopItemsScreen(c) // gap-loops
	case "submissions":
		body = s.gweProSubmissionsScreen(c)
	case "code":
		body = s.gweProCodeScreen(c)
	case "fonts":
		body = s.gweProFontsScreen(c)
	case "roles":
		body = s.gweProRolesScreen(c)
	case "settings":
		body = s.gweProSettingsScreen(c)
	}
	d := gweProAdminData{Title: title, Body: template.HTML(body)}
	for _, t := range gweProTabs {
		if u.Can(t.Cap, nil) || (t.Cap == "unfiltered_html" && u.Can("manage_options", nil)) {
			d.Tabs = append(d.Tabs, gweProTabLink{URL: gweProAdminPath + "?tab=" + t.Tab, Title: t.Title, Active: t.Tab == tab})
		}
	}
	var notices []notice
	if m := c.Query("message"); m != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML(html.EscapeString(m)), Dismissible: true})
	}
	if e := c.Query("error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(html.EscapeString(e)), Dismissible: true})
	}
	if tab == "fonts" {
		s.enqueueMedia(c, 0)
	}
	s.adminPage(c, "web-builder?tab="+tab, title+" ‹ Pursae Web Builder", "toplevel_page_gowebeditor-pro", "admin/gowebeditor-pro", d, notices...)
}

func esc(s string) string { return html.EscapeString(s) }

func (s *Server) gweProNonceField(c *gin.Context, tab string) string {
	return `<input type="hidden" name="_wpnonce" value="` + esc(s.nonce(c, "gwe-pro-"+tab)) + `" />`
}

// --- popups --------------------------------------------------------------------------------------

func (s *Server) gweProLibraryPosts(c *gin.Context, tplType string) []*store.Post {
	rows, err := s.store.Pool.Query(c, `SELECT id FROM posts WHERE type = $1 AND meta->>'_elementor_template_type' = $2
		AND status IN ('publish','draft','pending','private') ORDER BY modified_at DESC`, gweLibrary, tplType)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []*store.Post
	for _, id := range ids {
		if p, err := s.store.GetPost(c, id); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) gweProPopupsScreen(c *gin.Context) string {
	newURL := "/wp-admin/edit?action=elementor_new_post&post_type=" + gweLibrary + "&template_type=popup&_wpnonce=" + s.nonce(c, "elementor_action_new_post")
	var b strings.Builder
	b.WriteString(`<p><a class="button button-primary" href="` + esc(newURL) + `">Add New Popup</a></p>`)
	b.WriteString(`<p class="description">Popups open on the pages their display conditions match (set them when you publish the popup in Pursae Web Builder) and from any link that uses the Popup dynamic tag or a form's Popup action. Triggers (page load, scroll, click, exit intent, inactivity…) and advanced rules are under the editor's Settings › Popup.</p>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th>Title</th><th>Display Conditions</th><th>Status</th><th>Last Modified</th></tr></thead><tbody>`)
	list := s.gweProLibraryPosts(c, "popup")
	for _, p := range list {
		var conds []string
		for _, cd := range gweConditionsOf(p) {
			conds = append(conds, esc(s.gweConditionLabel(c, cd)))
		}
		cond := strings.Join(conds, ", ")
		if cond == "" {
			cond = `<span class="description">Only opened by links and forms</span>`
		}
		fmt.Fprintf(&b, `<tr><td><strong><a class="row-title" href="%s">%s</a></strong><div class="row-actions"><a href="%s">Edit with Pursae Web Builder</a></div></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			esc(gweEditURL(p.ID)), esc(p.Title), esc(gweEditURL(p.ID)), cond, esc(gweStatusLabels[p.Status]), esc(wp.FormatDate(p.ModifiedAt.In(s.loc()), "Y/m/d g:i a")))
	}
	if len(list) == 0 {
		b.WriteString(`<tr><td colspan="4">No popups yet.</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// --- submissions -----------------------------------------------------------------------------------

type gweSubmission struct {
	ID                           int64
	PostID                       int64
	ElementID, FormName, Referer string
	RefererTitle, UserIP, Agent  string
	Fields                       []gweFormField
	Meta                         map[string]string
	Actions                      []map[string]any
	Read                         bool
	Created                      time.Time
}

func (s *Server) gweProSubmissions(c *gin.Context, where string, args ...any) []*gweSubmission {
	rows, err := s.store.Pool.Query(c, `SELECT id, coalesce(post_id,0), element_id, form_name, referer, referer_title, user_ip, user_agent,
		fields, meta, actions, is_read, created_at FROM gwe_form_submissions WHERE status = 'new' `+where+` ORDER BY created_at DESC LIMIT 500`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*gweSubmission
	for rows.Next() {
		sub := &gweSubmission{}
		var fj, mj, aj []byte
		if rows.Scan(&sub.ID, &sub.PostID, &sub.ElementID, &sub.FormName, &sub.Referer, &sub.RefererTitle, &sub.UserIP, &sub.Agent,
			&fj, &mj, &aj, &sub.Read, &sub.Created) != nil {
			continue
		}
		_ = json.Unmarshal(fj, &sub.Fields)
		_ = json.Unmarshal(mj, &sub.Meta)
		_ = json.Unmarshal(aj, &sub.Actions)
		out = append(out, sub)
	}
	return out
}

func (s *Server) gweProSubmissionsScreen(c *gin.Context) string {
	var b strings.Builder
	if id := gweInt(c.Query("id")); id > 0 {
		list := s.gweProSubmissions(c, "AND id = $1", id)
		if len(list) == 0 {
			return `<p>Submission not found.</p>`
		}
		sub := list[0]
		_, _ = s.store.Pool.Exec(c, `UPDATE gwe_form_submissions SET is_read = true WHERE id = $1`, id)
		fmt.Fprintf(&b, `<p><a href="%s?tab=submissions">&larr; All submissions</a></p><h2>Submission #%d</h2>`, gweProAdminPath, sub.ID)
		b.WriteString(`<table class="widefat striped"><tbody>`)
		for _, f := range sub.Fields {
			fmt.Fprintf(&b, `<tr><th style="width:220px">%s</th><td>%s</td></tr>`, esc(f.Title), strings.ReplaceAll(esc(f.Value), "\n", "<br>"))
		}
		b.WriteString(`</tbody></table><h3>Details</h3><table class="widefat striped"><tbody>`)
		fmt.Fprintf(&b, `<tr><th style="width:220px">Form</th><td>%s (%s)</td></tr>`, esc(sub.FormName), esc(sub.ElementID))
		if gweSameSite(sub.Referer, s.links(c).Home) {
			fmt.Fprintf(&b, `<tr><th>Page</th><td><a href="%s">%s</a></td></tr>`, esc(sub.Referer), esc(sub.RefererTitle+" "+sub.Referer))
		} else { // stored before referrers were checked: text, never a link
			fmt.Fprintf(&b, `<tr><th>Page</th><td>%s</td></tr>`, esc(sub.RefererTitle+" "+sub.Referer))
		}
		fmt.Fprintf(&b, `<tr><th>Submitted</th><td>%s</td></tr>`, esc(wp.FormatDate(sub.Created.In(s.loc()), "F j, Y g:i a")))
		fmt.Fprintf(&b, `<tr><th>IP / User agent</th><td>%s<br>%s</td></tr>`, esc(sub.UserIP), esc(sub.Agent))
		for _, a := range sub.Actions {
			fmt.Fprintf(&b, `<tr><th>Action: %s</th><td>%s %s</td></tr>`, esc(gweStr(a["label"])), esc(gweStr(a["status"])), esc(gweStr(a["log"])))
		}
		b.WriteString(`</tbody></table>`)
		fmt.Fprintf(&b, `<form method="post" style="margin-top:16px">%s<input type="hidden" name="action" value="trash"/><input type="hidden" name="ids" value="%d"/><button class="button-link submitdelete" type="submit">Move to Trash</button></form>`,
			s.gweProNonceField(c, "submissions"), sub.ID)
		return b.String()
	}
	list := s.gweProSubmissions(c, "")
	fmt.Fprintf(&b, `<p><a class="button" href="%s?tab=submissions&export=csv">Export CSV</a></p>`, gweProAdminPath)
	b.WriteString(`<form method="post">` + s.gweProNonceField(c, "submissions") + `<input type="hidden" name="action" value="trash"/>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><td class="manage-column check-column"></td><th>Main</th><th>Form</th><th>Page</th><th>Date</th></tr></thead><tbody>`)
	for _, sub := range list {
		main := ""
		for _, f := range sub.Fields {
			if f.Value != "" {
				main = f.Value
				break
			}
		}
		weight := ""
		if !sub.Read {
			weight = ` style="font-weight:600"`
		}
		fmt.Fprintf(&b, `<tr%s><th class="check-column"><input type="checkbox" name="ids" value="%d"/></th><td><a href="%s?tab=submissions&id=%d">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			weight, sub.ID, gweProAdminPath, sub.ID, esc(trimTo(main, 80)), esc(sub.FormName), esc(sub.RefererTitle), esc(wp.FormatDate(sub.Created.In(s.loc()), "Y/m/d g:i a")))
	}
	if len(list) == 0 {
		b.WriteString(`<tr><td colspan="5">No submissions yet. Add the Collect Submissions action to a form to store its entries here.</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	if len(list) > 0 {
		b.WriteString(`<p><button class="button" type="submit">Move selected to Trash</button></p>`)
	}
	b.WriteString(`</form>`)
	return b.String()
}

func trimTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (s *Server) gweProSubmissionsCSV(c *gin.Context) {
	list := s.gweProSubmissions(c, "")
	cols := []string{}
	seen := map[string]bool{}
	for _, sub := range list {
		for _, f := range sub.Fields {
			if !seen[f.Title] {
				seen[f.Title] = true
				cols = append(cols, f.Title)
			}
		}
	}
	c.Header("Content-Disposition", `attachment; filename="form-submissions.csv"`)
	c.Header("Content-Type", "text/csv; charset=UTF-8")
	w := csv.NewWriter(c.Writer)
	_ = w.Write(append(append([]string{"ID", "Form", "Page", "Date"}, cols...), "IP"))
	for _, sub := range list {
		row := []string{strconv.FormatInt(sub.ID, 10), csvSafe(sub.FormName), csvSafe(sub.Referer), sub.Created.In(s.loc()).Format("2006-01-02 15:04:05")}
		vals := map[string]string{}
		for _, f := range sub.Fields {
			vals[f.Title] = f.Value
		}
		for _, col := range cols {
			row = append(row, csvSafe(vals[col])) // keep spreadsheet formulas out
		}
		_ = w.Write(append(row, csvSafe(sub.UserIP)))
	}
	w.Flush()
}

func (s *Server) gweProSubmissionsREST(c *gin.Context) {
	list := s.gweProSubmissions(c, "")
	out := []map[string]any{}
	for _, sub := range list {
		out = append(out, map[string]any{"id": sub.ID, "post_id": sub.PostID, "element_id": sub.ElementID, "form_name": sub.FormName,
			"referer": sub.Referer, "is_read": sub.Read, "created_at": sub.Created.UTC().Format(time.RFC3339), "values": sub.Fields})
	}
	c.JSON(http.StatusOK, map[string]any{"data": out, "meta": map[string]any{"total": len(out)}})
}

// --- custom code -----------------------------------------------------------------------------------

var gweProCodeLocations = [][2]string{{"head", "<head>"}, {"body_start", "<body> - Start"}, {"body_end", "<body> - End"}}

func (s *Server) gweProCodeScreen(c *gin.Context) string {
	var b strings.Builder
	if c.Query("edit") != "" {
		var p *store.Post
		if id := gweInt(c.Query("edit")); id > 0 {
			if got, err := s.store.GetPost(c, id); err == nil && gweDocType(got) == "code_snippet" {
				p = got
			}
		}
		if p == nil {
			p = &store.Post{Status: "draft", Meta: map[string]any{}}
		}
		code, _ := p.Meta["_gwe_code"].(string)
		loc, _ := p.Meta["_gwe_code_location"].(string)
		prio := gweInt(p.Meta["_gwe_code_priority"])
		if prio == 0 {
			prio = 5
		}
		conds := gweConditionsOf(p)
		if len(conds) == 0 {
			conds = []string{"include/general"}
		}
		b.WriteString(`<form method="post">` + s.gweProNonceField(c, "code") + `<input type="hidden" name="action" value="save"/>`)
		fmt.Fprintf(&b, `<input type="hidden" name="id" value="%d"/>`, p.ID)
		b.WriteString(`<table class="form-table"><tbody>`)
		fmt.Fprintf(&b, `<tr><th><label for="gwe-code-title">Title</label></th><td><input id="gwe-code-title" name="title" class="regular-text" value="%s" required/></td></tr>`, esc(p.Title))
		b.WriteString(`<tr><th><label for="gwe-code-loc">Location</label></th><td><select id="gwe-code-loc" name="location">`)
		for _, l := range gweProCodeLocations {
			sel := ""
			if l[0] == loc || (loc == "" && l[0] == "head") {
				sel = " selected"
			}
			fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, l[0], sel, esc(l[1]))
		}
		b.WriteString(`</select></td></tr>`)
		fmt.Fprintf(&b, `<tr><th><label for="gwe-code-prio">Priority</label></th><td><input id="gwe-code-prio" name="priority" type="number" min="1" max="10" value="%d"/></td></tr>`, prio)
		fmt.Fprintf(&b, `<tr><th><label for="gwe-code">Code</label></th><td><textarea id="gwe-code" name="code" rows="14" class="large-text code">%s</textarea><p class="description">Printed as is (HTML: &lt;script&gt;, &lt;style&gt;, &lt;meta&gt;…).</p></td></tr>`, esc(code))
		fmt.Fprintf(&b, `<tr><th><label for="gwe-code-conds">Display conditions</label></th><td><textarea id="gwe-code-conds" name="conditions" rows="4" class="large-text code">%s</textarea><p class="description">One rule per line, the Theme Builder's rule format: <code>include/general</code>, <code>include/singular/page/12</code>, <code>exclude/archive/category/3</code>…</p></td></tr>`, esc(strings.Join(conds, "\n")))
		pub := ""
		if p.Status == "publish" {
			pub = " checked"
		}
		fmt.Fprintf(&b, `<tr><th>Status</th><td><label><input type="checkbox" name="publish" value="1"%s/> Published</label></td></tr>`, pub)
		b.WriteString(`</tbody></table><p class="submit"><button class="button button-primary" type="submit">Save</button> <a class="button" href="` + gweProAdminPath + `?tab=code">Cancel</a></p></form>`)
		return b.String()
	}
	fmt.Fprintf(&b, `<p><a class="button button-primary" href="%s?tab=code&edit=new">Add New Code</a></p>`, gweProAdminPath)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th>Title</th><th>Location</th><th>Priority</th><th>Conditions</th><th>Status</th></tr></thead><tbody>`)
	list := s.gweProLibraryPosts(c, "code_snippet")
	for _, p := range list {
		loc, _ := p.Meta["_gwe_code_location"].(string)
		label := "<head>"
		for _, l := range gweProCodeLocations {
			if l[0] == loc {
				label = l[1]
			}
		}
		var conds []string
		for _, cd := range gweConditionsOf(p) {
			conds = append(conds, esc(s.gweConditionLabel(c, cd)))
		}
		fmt.Fprintf(&b, `<tr><td><strong><a class="row-title" href="%s?tab=code&edit=%d">%s</a></strong><div class="row-actions"><form method="post" style="display:inline">%s<input type="hidden" name="action" value="delete"/><input type="hidden" name="id" value="%d"/><button type="submit" class="button-link submitdelete">Delete</button></form></div></td><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>`,
			gweProAdminPath, p.ID, esc(p.Title), s.gweProNonceField(c, "code"), p.ID, esc(label), gweInt(p.Meta["_gwe_code_priority"]), strings.Join(conds, ", "), esc(gweStatusLabels[p.Status]))
	}
	if len(list) == 0 {
		b.WriteString(`<tr><td colspan="5">No custom code yet. Add tracking pixels, meta tags or scripts to the site's head or body.</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// --- custom fonts ----------------------------------------------------------------------------------

// gweProFont is one custom font family with its files (Custom Fonts).
type gweProFont struct {
	Family     string             `json:"family"`
	Variations []gweProFontVarian `json:"variations"`
}

type gweProFontVarian struct {
	Weight string `json:"weight"`
	Style  string `json:"style"`
	WOFF2  string `json:"woff2,omitempty"`
	WOFF   string `json:"woff,omitempty"`
	TTF    string `json:"ttf,omitempty"`
}

func (s *Server) gweProFonts() []gweProFont {
	var out []gweProFont
	switch v := s.settings.Get("gwe_custom_fonts").(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &out)
	default:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// gweProFontFaceCSS is the @font-face CSS of the custom fonts.
func (s *Server) gweProFontFaceCSS() string {
	var b strings.Builder
	for _, f := range s.gweProFonts() {
		for _, v := range f.Variations {
			var srcs []string
			if v.WOFF2 != "" {
				srcs = append(srcs, "url('"+v.WOFF2+"') format('woff2')")
			}
			if v.WOFF != "" {
				srcs = append(srcs, "url('"+v.WOFF+"') format('woff')")
			}
			if v.TTF != "" {
				srcs = append(srcs, "url('"+v.TTF+"') format('truetype')")
			}
			if len(srcs) == 0 {
				continue
			}
			fmt.Fprintf(&b, "@font-face{font-family:'%s';font-style:%s;font-weight:%s;font-display:auto;src:%s;}\n",
				strings.ReplaceAll(f.Family, "'", ""), v.Style, v.Weight, strings.Join(srcs, ","))
		}
	}
	return b.String()
}

func (s *Server) gweProFontsScreen(c *gin.Context) string {
	var b strings.Builder
	b.WriteString(`<p class="description">Upload font files (WOFF2, WOFF or TTF) once; the family then appears under "Custom Fonts" in every typography control and is served from this site.</p>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th>Font family</th><th>Variations</th><th></th></tr></thead><tbody>`)
	fonts := s.gweProFonts()
	for i, f := range fonts {
		var vs []string
		for _, v := range f.Variations {
			vs = append(vs, esc(v.Weight+" "+v.Style))
		}
		fmt.Fprintf(&b, `<tr><td><strong>%s</strong></td><td>%s</td><td><form method="post">%s<input type="hidden" name="action" value="delete"/><input type="hidden" name="index" value="%d"/><button class="button-link submitdelete" type="submit">Delete</button></form></td></tr>`,
			esc(f.Family), strings.Join(vs, ", "), s.gweProNonceField(c, "fonts"), i)
	}
	if len(fonts) == 0 {
		b.WriteString(`<tr><td colspan="3">No custom fonts yet.</td></tr>`)
	}
	b.WriteString(`</tbody></table><h2>Add a font variation</h2><form method="post" enctype="multipart/form-data">` + s.gweProNonceField(c, "fonts") + `<input type="hidden" name="action" value="add"/>`)
	b.WriteString(`<table class="form-table"><tbody><tr><th><label for="gwe-font-family">Font family</label></th><td><input id="gwe-font-family" name="family" class="regular-text" required/></td></tr>`)
	b.WriteString(`<tr><th><label for="gwe-font-weight">Weight</label></th><td><select id="gwe-font-weight" name="weight">`)
	for _, w := range []string{"normal", "bold", "100", "200", "300", "400", "500", "600", "700", "800", "900"} {
		fmt.Fprintf(&b, `<option value="%s">%s</option>`, w, w)
	}
	b.WriteString(`</select></td></tr><tr><th><label for="gwe-font-style">Style</label></th><td><select id="gwe-font-style" name="style"><option>normal</option><option>italic</option><option>oblique</option></select></td></tr>`)
	b.WriteString(`<tr><th><label for="gwe-font-file">Font files</label></th><td><input id="gwe-font-file" type="file" name="files" accept=".woff2,.woff,.ttf" multiple required/></td></tr></tbody></table>`)
	b.WriteString(`<p class="submit"><button class="button button-primary" type="submit">Add Font</button></p></form>`)
	return b.String()
}

// --- role manager ----------------------------------------------------------------------------------

func (s *Server) gweProRolesScreen(c *gin.Context) string {
	rows, err := s.store.Pool.Query(c, `SELECT slug, name FROM roles WHERE slug <> 'administrator' ORDER BY id`)
	if err != nil {
		return "<p>" + esc(err.Error()) + "</p>"
	}
	type role struct{ slug, name string }
	var roles []role
	for rows.Next() {
		var r role
		if rows.Scan(&r.slug, &r.name) == nil {
			roles = append(roles, r)
		}
	}
	rows.Close()
	excluded := map[string]bool{}
	if l, ok := s.settings.Get("gwe_exclude_user_roles").([]any); ok {
		for _, x := range l {
			excluded[gweStr(x)] = true
		}
	}
	rm, _ := s.settings.Get("gwe_role_manager").(map[string]any)
	var b strings.Builder
	b.WriteString(`<p class="description">Choose what each role may do in Pursae Web Builder. Administrators always have full access.</p><form method="post">` + s.gweProNonceField(c, "roles") + `<input type="hidden" name="action" value="save"/>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th>Role</th><th>No access to the editor</th><th>Access to edit content only</th></tr></thead><tbody>`)
	for _, r := range roles {
		ex, co := "", ""
		if excluded[r.slug] {
			ex = " checked"
		}
		if m, ok := rm[r.slug].(map[string]any); ok && truthyAny(m["content_only"]) {
			co = " checked"
		}
		fmt.Fprintf(&b, `<tr><td><strong>%s</strong></td><td><input type="checkbox" name="exclude" value="%s"%s/></td><td><input type="checkbox" name="content_only" value="%s"%s/></td></tr>`,
			esc(r.name), esc(r.slug), ex, esc(r.slug), co)
	}
	b.WriteString(`</tbody></table><p class="submit"><button class="button button-primary" type="submit">Save Changes</button></p></form>`)
	return b.String()
}

// --- settings --------------------------------------------------------------------------------------

func (s *Server) gweProSettingsScreen(c *gin.Context) string {
	field := func(key, label, typ string) string {
		v := s.settings.String(key)
		if typ == "password" { // secrets are never printed: a mask that keeps the key when submitted unchanged
			v, typ = gweMaskSecret(v), "text"
		}
		return fmt.Sprintf(`<tr><th><label for="%s">%s</label></th><td><input id="%s" name="%s" type="%s" class="regular-text" value="%s" autocomplete="off"/></td></tr>`,
			key, esc(label), key, key, typ, esc(v))
	}
	var b strings.Builder
	b.WriteString(`<form method="post">` + s.gweProNonceField(c, "settings") + `<input type="hidden" name="action" value="save"/>`)
	b.WriteString(`<h2>reCAPTCHA</h2><p class="description">reCAPTCHA protects forms from spam. Get the keys from Google's reCAPTCHA admin console; visitors' browsers then load Google's script on pages with a reCAPTCHA field.</p><table class="form-table"><tbody>`)
	b.WriteString(field("gwe_recaptcha_site_key", "Site Key (v2)", "text") + field("gwe_recaptcha_secret_key", "Secret Key (v2)", "password"))
	b.WriteString(`</tbody></table><h2>reCAPTCHA V3</h2><table class="form-table"><tbody>`)
	b.WriteString(field("gwe_recaptcha_v3_site_key", "Site Key (v3)", "text") + field("gwe_recaptcha_v3_secret_key", "Secret Key (v3)", "password"))
	b.WriteString(field("gwe_recaptcha_v3_threshold", "Score Threshold (0–1)", "number"))
	b.WriteString(`</tbody></table>` + s.gweIntegrationsSettingsSections(c) + `<p class="submit"><button class="button button-primary" type="submit">Save Changes</button></p></form>`)
	return b.String()
}

// --- POST handlers ---------------------------------------------------------------------------------

func (s *Server) gweProAdminPost(c *gin.Context, tab string) (string, error) {
	u := currentUser(c)
	action := c.PostForm("action")
	switch tab + "/" + action {
	case "submissions/trash":
		var ids []int64
		for _, raw := range c.PostFormArray("ids") {
			if id := gweInt(raw); id > 0 {
				ids = append(ids, id)
			}
		}
		_, err := s.store.Pool.Exec(c, `UPDATE gwe_form_submissions SET status = 'trash', updated_at = now() WHERE id = ANY($1)`, ids)
		return "Submissions moved to the trash.", err
	case "code/save":
		if err := s.gweEnsureLibraryType(c); err != nil {
			return "", err
		}
		var p *store.Post
		if id := gweInt(c.PostForm("id")); id > 0 {
			got, err := s.store.GetPost(c, id)
			if err != nil || gweDocType(got) != "code_snippet" {
				return "", fmt.Errorf("Snippet not found.")
			}
			p = got
		} else {
			p = &store.Post{Type: gweLibrary, AuthorID: u.ID, Meta: map[string]any{"_elementor_template_type": "code_snippet"}}
		}
		p.Title = strings.TrimSpace(c.PostForm("title"))
		if p.Title == "" {
			p.Title = "Custom Code"
		}
		loc := c.PostForm("location")
		if loc != "head" && loc != "body_start" && loc != "body_end" {
			loc = "head"
		}
		prio, _ := strconv.Atoi(c.PostForm("priority"))
		if prio < 1 || prio > 10 {
			prio = 5
		}
		var conds []any
		for _, line := range strings.Split(c.PostForm("conditions"), "\n") {
			if line = strings.TrimSpace(line); line != "" && gweValidRule(line) {
				conds = append(conds, line)
			}
		}
		p.Meta["_gwe_code"] = c.PostForm("code")
		p.Meta["_gwe_code_location"] = loc
		p.Meta["_gwe_code_priority"] = prio
		p.Meta["_gwe_conditions"] = conds
		p.Status = "draft"
		if c.PostForm("publish") == "1" {
			p.Status = "publish"
		}
		var err error
		if p.ID == 0 {
			err = s.store.InsertPost(c, p)
		} else {
			err = s.store.UpdatePost(c, p, u.ID)
		}
		s.purgeAll(c)
		return "Custom code saved.", err
	case "code/delete":
		p, err := s.store.GetPost(c, gweInt(c.PostForm("id")))
		if err != nil || gweDocType(p) != "code_snippet" {
			return "", fmt.Errorf("Snippet not found.")
		}
		err = s.store.DeletePost(c, p.ID)
		s.purgeAll(c)
		return "Custom code deleted.", err
	case "fonts/add":
		family := strings.TrimSpace(c.PostForm("family"))
		if family == "" {
			return "", fmt.Errorf("Enter the font family name.")
		}
		form, err := c.MultipartForm()
		if err != nil || len(form.File["files"]) == 0 {
			return "", fmt.Errorf("Choose the font files.")
		}
		v := gweProFontVarian{Weight: c.PostForm("weight"), Style: c.PostForm("style")}
		dir := filepath.Join(s.cfg.UploadsDir, builderUploadsDir, "fonts")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		for _, fh := range form.File["files"] {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fh.Filename), "."))
			if ext != "woff2" && ext != "woff" && ext != "ttf" {
				return "", fmt.Errorf("Only WOFF2, WOFF and TTF files are allowed.")
			}
			if fh.Size > 10<<20 {
				return "", fmt.Errorf("Font files may be at most 10 MB.")
			}
			name := strings.Trim(strings.Map(func(r rune) rune {
				if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
					return r
				}
				return '-'
			}, strings.ToLower(family)), "-") + "-" + v.Weight + "-" + v.Style + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "." + ext
			src, err := fh.Open()
			if err != nil {
				return "", err
			}
			dst, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				src.Close()
				return "", err
			}
			_, err = io.Copy(dst, src)
			src.Close()
			dst.Close()
			if err != nil {
				return "", err
			}
			u := s.links(c).HomeURL("/wp-content/uploads/web-builder/fonts/" + name)
			switch ext {
			case "woff2":
				v.WOFF2 = u
			case "woff":
				v.WOFF = u
			default:
				v.TTF = u
			}
		}
		fonts := s.gweProFonts()
		found := false
		for i := range fonts {
			if strings.EqualFold(fonts[i].Family, family) {
				fonts[i].Variations = append(fonts[i].Variations, v)
				found = true
			}
		}
		if !found {
			fonts = append(fonts, gweProFont{Family: family, Variations: []gweProFontVarian{v}})
		}
		sort.SliceStable(fonts, func(i, j int) bool { return fonts[i].Family < fonts[j].Family })
		return "Font saved.", s.gweProSaveFonts(c, fonts)
	case "fonts/delete":
		fonts := s.gweProFonts()
		i := int(gweInt(c.PostForm("index")))
		if i < 0 || i >= len(fonts) {
			return "", fmt.Errorf("Font not found.")
		}
		fonts = append(fonts[:i], fonts[i+1:]...)
		return "Font deleted.", s.gweProSaveFonts(c, fonts)
	case "roles/save":
		excl := []any{}
		for _, r := range c.PostFormArray("exclude") {
			excl = append(excl, r)
		}
		rm := map[string]any{}
		for _, r := range c.PostFormArray("content_only") {
			rm[r] = map[string]any{"content_only": true}
		}
		return "Role Manager saved.", s.settings.Set(c, map[string]any{"gwe_exclude_user_roles": excl, "gwe_role_manager": rm})
	case "settings/save":
		vals := map[string]any{}
		for _, k := range []string{"gwe_recaptcha_site_key", "gwe_recaptcha_secret_key", "gwe_recaptcha_v3_site_key", "gwe_recaptcha_v3_secret_key", "gwe_recaptcha_v3_threshold"} {
			vals[k] = strings.TrimSpace(c.PostForm(k))
			if cur := s.settings.String(k); strings.HasSuffix(k, "_secret_key") && cur != "" && vals[k] == gweMaskSecret(cur) {
				vals[k] = cur // the mask was submitted unchanged
			}
		}
		s.gweIntegrationsSaveSettings(c, vals) // gap-integrations: API keys, Google Fonts option
		gweWidgetsCache.Range(func(k, _ any) bool { gweWidgetsCache.Delete(k); return true })
		return "Settings saved.", s.settings.Set(c, vals)
	}
	return "", fmt.Errorf("Unknown action.")
}

func (s *Server) gweProSaveFonts(c *gin.Context, fonts []gweProFont) error {
	b, _ := json.Marshal(fonts)
	var v any
	_ = json.Unmarshal(b, &v)
	if v == nil {
		v = []any{}
	}
	return s.settings.Set(c, map[string]any{"gwe_custom_fonts": v})
}
