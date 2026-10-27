package server

import (
	"bytes"
	"html/template"
	"log"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
)

// gweCommentsHTML is comments_template() for the Post Comments widget: the comment list and
// comment_form() of a post, printed through the active theme's own comments template (its
// "comments" block, or Twenty Twenty's renderer), so the widget looks like the theme's comments in
// theme-builder single templates too. GoWebEditor themes use Hello GoWebEditor's template.
func (s *Server) gweCommentsHTML(c *gin.Context, p *store.Post, lb *links.Builder) string {
	if c == nil || p == nil {
		return ""
	}
	u := currentUser(c)
	var view *siteView
	if v, ok := c.Get(ctxGweView); ok {
		view, _ = v.(*siteView)
	}
	theme := s.activeTheme()
	if view != nil && view.Theme != nil {
		theme = view.Theme
	}
	shell := view != nil && view.GWE != nil
	if theme != nil && theme.Slug == "twentytwenty" && !shell {
		return string(s.twentyTwentyComments(c, p, lb, u))
	}
	cv := s.commentsView(c, p, lb, u)
	if cv == nil {
		return ""
	}
	var set *template.Template
	switch {
	case shell || theme == nil || theme.Custom:
		set = s.tmpl.gweShell()
	default:
		set = s.tmpl.getTheme(true, theme.Slug)
	}
	data := &siteView{Comments: cv, Data: map[string]any{"Comments": cv}, Theme: theme}
	if view != nil {
		cp := *view
		cp.Comments = cv
		cp.Data = map[string]any{}
		for k, v := range view.Data {
			cp.Data[k] = v
		}
		cp.Data["Comments"] = cv
		data = &cp
	}
	if set != nil && set.Lookup("comments") != nil {
		var buf bytes.Buffer
		if err := set.ExecuteTemplate(&buf, "comments", data); err == nil {
			return buf.String()
		} else {
			log.Printf("gowebeditor: post comments: %v", err)
		}
	}
	// No theme template: core's default comments markup.
	var buf bytes.Buffer
	buf.WriteString(`<div id="comments" class="comments-area">`)
	if cv.Count > 0 {
		buf.WriteString(`<h2 class="comments-title">` + string(cv.Title) + `</h2><ol class="comment-list">` + string(cv.List) + `</ol>`)
	}
	if cv.ClosedNotice {
		buf.WriteString(`<p class="no-comments">Comments are closed.</p>`)
	}
	buf.WriteString(string(cv.Form) + `</div>`)
	return buf.String()
}
