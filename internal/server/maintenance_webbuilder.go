package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"

	"wpgo/internal/webbuilder"
)

// Pursae Web Builder as the maintenance page: the published, static snapshot of a Theme Builder
// template replaces the built-in page. Only a template without dynamic widgets qualifies (the gate
// runs before the router, so there is no request to fill a post loop or a cart widget for), which
// is also what keeps the page cheap: nothing but the stored HTML and CSS is printed.

// maintWBChoice is one template of the Maintenance settings' select.
type maintWBChoice struct {
	ID    int64
	Title string
}

// maintWBSnapshot returns the template's snapshot (body HTML, CSS); ok is false when the post is not
// a published, public wb_template with a static snapshot.
func (s *Server) maintWBSnapshot(ctx context.Context, id int64) (body, css string, ok bool) {
	if id <= 0 {
		return "", "", false
	}
	p, err := s.store.GetPost(ctx, id)
	if err != nil || p.Type != webbuilder.TemplatePostType || p.Status != "publish" || p.Password != "" {
		return "", "", false
	}
	d, err := s.store.WebBuilder().Doc(ctx, id)
	if err != nil || strings.TrimSpace(d.HTML) == "" {
		return "", "", false
	}
	prog, err := wbPrograms.Compile(d.PostID, d.Rev, d.HTML)
	if err != nil {
		return "", "", false
	}
	static, isStatic := prog.Static()
	if !isStatic {
		return "", "", false
	}
	css = strings.TrimSpace(d.CSS)
	if strings.Contains(strings.ToLower(css), "</style") { // the sanitizer refuses it; never trust it blindly
		css = ""
	}
	return s.filterContentTags(ctx, static), css, true
}

// maintWBChoices lists the templates that can be the maintenance page.
func (s *Server) maintWBChoices(ctx context.Context) []maintWBChoice {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, title FROM posts WHERE type = $1 AND status = 'publish' AND coalesce(password, '') = '' ORDER BY title, id LIMIT 200`, webbuilder.TemplatePostType)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []maintWBChoice
	for rows.Next() {
		var c maintWBChoice
		if rows.Scan(&c.ID, &c.Title) == nil {
			if _, _, ok := s.maintWBSnapshot(ctx, c.ID); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

// maintRenderWB writes the Web Builder page; false (nothing written) when the chosen template is gone
// or no longer qualifies, and the built-in page is used instead.
func (s *Server) maintRenderWB(w http.ResponseWriter, r *http.Request, st maintSettings, status int, lang string) bool {
	if st.WBTemplate <= 0 {
		return false
	}
	body, css, ok := s.maintWBSnapshot(r.Context(), st.WBTemplate)
	if !ok {
		return false
	}
	var head strings.Builder
	head.WriteString("<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	head.WriteString("<title>" + html.EscapeString(s.maintVars(st.Title)) + "</title>\n")
	if d := s.maintVars(st.MetaDescription); d != "" {
		head.WriteString("<meta name=\"description\" content=\"" + html.EscapeString(d) + "\">\n")
	}
	if st.NoIndex {
		head.WriteString("<meta name=\"robots\" content=\"noindex, nofollow\">\n")
	}
	if site, err := s.wbSiteAssets(); err == nil && site.CSS != "" {
		fmt.Fprintf(&head, "<link rel='stylesheet' id='web-builder-css' href='%s' media='all' />\n",
			html.EscapeString(s.links(r.Context()).HomeURL(webbuilder.SiteAssetsPath+site.CSS)))
	}
	if css != "" {
		head.WriteString("<style id=\"wb-css-" + itoa64(st.WBTemplate) + "\">\n" + css + "\n</style>\n")
	}
	if lang == "" {
		lang = "en-US"
	}
	page := "<!doctype html>\n<html lang=\"" + html.EscapeString(lang) + "\">\n<head>\n" + head.String() + "</head>\n<body class=\"wb-site wb-maintenance\">\n" + body + "\n</body>\n</html>\n"
	s.maintHeaders(w, st, "text/html; charset=utf-8")
	s.maintStatus(w, st, status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(page))
	}
	return true
}
