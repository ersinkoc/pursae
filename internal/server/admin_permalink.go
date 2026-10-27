package server

import (
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// samplePermalinkHTML is get_sample_permalink_html(): the "Permalink: …/slug/ [Edit]" line under
// the title of the classic Edit Post screen. post.js turns the Edit button into the inline slug
// editor and posts the new slug to admin-ajax "sample-permalink", which answers with this markup.
func (s *Server) samplePermalinkHTML(c *gin.Context, p *store.Post, title, slug string) template.HTML {
	lb := s.links(c)
	q := *p
	if slug == "" {
		slug = p.Slug
	}
	if slug == "" {
		slug = wp.SanitizeTitle(title)
	}
	if slug == "" {
		slug = itoa64(p.ID)
	}
	if u, err := s.store.UniqueSlug(c, p.Type, p.ParentID, slug, p.ID); err == nil {
		slug = u
	}
	q.Slug = slug

	view := lb.Permalink(&q)
	if q.Status != "publish" && q.Status != "private" {
		view += previewSep(view) + "preview=true"
	}
	// The structure with the slug left out: pages keep their parents' path (…/parent/%pagename%/).
	tpl := lb.PermalinkTemplate(&q)
	if p.Type == "page" {
		tpl = lb.Permalink(&q)
		if i := strings.LastIndex(tpl, "/"+slug); i >= 0 {
			tpl = tpl[:i+1] + "%pagename%" + tpl[i+1+len(slug):]
		}
	}
	marker := "%postname%"
	if p.Type == "page" {
		marker = "%pagename%"
	}
	before, after, editable := strings.Cut(tpl, marker)
	if !editable { // plain permalinks (?p=123): WordPress shows the link and no Edit button
		return template.HTML(`<strong>Permalink:</strong> <span id="sample-permalink"><a href="` + html.EscapeString(view) + `">` +
			html.EscapeString(lb.Permalink(&q)) + `</a></span>`)
	}
	short := slug
	if utf8.RuneCountInString(short) > 34 {
		r := []rune(short)
		short = string(r[:16]) + "…" + string(r[len(r)-16:])
	}
	return template.HTML(`<strong>Permalink:</strong>` + "\n" +
		`<span id="sample-permalink"><a href="` + html.EscapeString(view) + `">` + html.EscapeString(before) +
		`<span id="editable-post-name">` + html.EscapeString(short) + `</span>` + html.EscapeString(after) + `</a></span>` + "\n" +
		`&lrm;<span id="edit-slug-buttons"><button type="button" class="edit-slug button button-small hide-if-no-js" aria-label="Edit permalink">Edit</button></span>` + "\n" +
		`<span id="editable-post-name-full">` + html.EscapeString(slug) + `</span>`)
}

// samplePermalinkAjax is wp_ajax_sample_permalink: it previews a slug without saving it.
func (s *Server) samplePermalinkAjax(c *gin.Context) {
	if !s.checkNonceValue(c, c.PostForm("samplepermalinknonce"), "samplepermalink") {
		c.String(http.StatusForbidden, "-1")
		return
	}
	id, _ := strconv.ParseInt(c.PostForm("post_id"), 10, 64)
	p, err := s.store.GetPost(c, id)
	if err != nil || !currentUser(c).Can("edit_post", p) {
		c.String(http.StatusOK, "-1")
		return
	}
	slug := wp.SanitizeTitle(c.PostForm("new_slug"))
	c.String(http.StatusOK, string(s.samplePermalinkHTML(c, p, c.PostForm("new_title"), slug)))
}
