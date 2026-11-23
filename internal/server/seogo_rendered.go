package server

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"wpgo/internal/store"
)

// Pursae SEO's content analysis scores what a visitor sees, not only the stored editor markup: a
// page whose text and images come from dynamic blocks (latest posts, synced patterns, query loops)
// or from its theme template (a front-page or page-<name> template that prints whole sections)
// would otherwise score as nearly empty.
//
//   - Pages are rendered the way the front end renders them (buildSiteView, then Pursae Web
//     Builder's canvas or the theme's template) and the analysis reads the page's <main>.
//   - Posts and custom post type entries read their rendered content (the_content: blocks rendered,
//     shortcodes run): their single template adds only the title, meta, navigation and comments.
//   - Products, password-protected posts and anything that fails to render keep the stored content.
//
// In both cases the site chrome the template may put inside <main> (navigation, sidebars, comments,
// the post title heading) and the markup Pursae SEO itself injects (table of contents, breadcrumbs)
// are left out, so nothing is counted twice. The rendering runs as a logged-out visitor in the
// post's language, with the in-memory post, so unsaved editor content is rendered as well.

type seogoRenderingKey struct{}

// seogoRenderTimeout bounds one render: an analysis must never hang a save.
const seogoRenderTimeout = 10 * time.Second

// seogoVisibleContent is the HTML the analysis scores for p, and whether the rendered page had a
// Pursae SEO table of contents (it is removed from the returned HTML).
func (s *Server) seogoVisibleContent(ctx context.Context, p *store.Post) (out string, toc bool) {
	if p == nil {
		return "", false
	}
	if ctx.Value(seogoRenderingKey{}) != nil || p.Password != "" || (p.Type != "page" && p.Type != "post" && siteCPT(p.Type) == nil) {
		return p.Content, false
	}
	defer func() {
		if r := recover(); r != nil { // a theme template or block renderer failed: keep the stored content
			log.Printf("seogo: rendering post %d for the analysis: %v", p.ID, r)
			out, toc = p.Content, false
		}
	}()

	// A fresh context: the admin request's language filter, render state and view memo must not leak
	// into the front-end render. Only the request origin (local sites' links) carries over.
	base := context.WithValue(context.Background(), seogoRenderingKey{}, true)
	if o := seogoRequestOrigin(ctx); o != "" {
		base = context.WithValue(base, originKey{}, o)
	}
	var lang *pllReq
	if s.golylangOn() && s.pllTranslatedType(p.Type) && p.ID > 0 {
		if l := s.pllLang(ctx, s.pllObjLang(ctx, pllPost, p.ID)); l != nil {
			lang = &pllReq{srv: s, Lang: l, Filter: true, Front: true}
			base = context.WithValue(base, pllCtxKey{}, lang)
		}
	}
	base, cancel := context.WithTimeout(base, seogoRenderTimeout)
	defer cancel()

	lb := s.links(base)
	cp := *p
	if cp.Status != "publish" {
		cp.Status = "publish" // the address it will have; drafts render all the same
	}
	uri := "/"
	if u, err := url.Parse(lb.Permalink(&cp)); err == nil && u.Path != "" {
		uri = u.RequestURI()
	}
	req, err := http.NewRequestWithContext(base, http.MethodGet, uri, nil)
	if err != nil {
		return p.Content, false
	}
	if hu, err := url.Parse(lb.HomeURL("/")); err == nil && hu.Host != "" {
		req.Host = hu.Host
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	if lang != nil {
		lang.Path = strings.Trim(req.URL.Path, "/")
		pllSet(c, lang)
	}
	q := s.seogoQueryForPost(p)
	c.Set(ctxSiteQuery, q)

	if p.Type != "page" {
		rc := &renderCtx{s: s, lb: lb, c: c, q: q, reqURI: uri, page: true, mainLoop: true}
		content := s.theContent(withRender(c.Request.Context(), rc), c, p, true, lb)
		return seogoCleanContent(content, "", false)
	}

	v, err := s.buildSiteView(c, q)
	if err != nil || v == nil {
		return p.Content, false
	}
	rendered := ""
	if v.Post != nil {
		rendered = string(v.Post.Content)
	}
	if !s.wbRenderPage(c, v, http.StatusOK) {
		s.renderSiteTemplate(c, v, http.StatusOK)
	}
	if rec.Code < 300 {
		if main, ok := seogoMainHTML(rec.Body.String()); ok {
			mc, mtoc := seogoCleanContent(main, p.Title, true)
			if rc, _ := seogoCleanContent(rendered, "", false); len(words(plainText(mc))) >= len(words(plainText(rc))) {
				return mc, mtoc
			}
		}
	}
	return seogoCleanContent(rendered, "", false)
}

// seogoRequestOrigin is the request origin (scheme://host) a context carries, if any.
func seogoRequestOrigin(ctx context.Context) string {
	if v, ok := ctx.Value(originKey{}).(string); ok {
		return v
	}
	if gc, ok := ctx.(*gin.Context); ok && gc.Request != nil {
		v, _ := gc.Request.Context().Value(originKey{}).(string)
		return v
	}
	return ""
}

// seogoMainHTML is the inner HTML of a page's main landmark (<main>, else role="main").
func seogoMainHTML(page string) (string, bool) {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return "", false
	}
	var found *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for ch := n.FirstChild; ch != nil && found == nil; ch = ch.NextSibling {
			if ch.Type == html.ElementNode && (ch.DataAtom == atom.Main || seogoAttr(ch, "role") == "main") {
				found = ch
				return
			}
			walk(ch)
		}
	}
	walk(doc)
	if found == nil {
		return "", false
	}
	var b strings.Builder
	for ch := found.FirstChild; ch != nil; ch = ch.NextSibling {
		_ = html.Render(&b, ch)
	}
	return b.String(), true
}

// seogoChromeClasses mark the parts of a rendered page the analysis leaves out: Pursae SEO's own
// output and the comments, post navigation and edit links a template prints next to the content.
var seogoChromeClasses = []string{"seogo-toc", "seogo-breadcrumb", "seogo-schema-graph", "comments-area", "comment-respond",
	"wp-block-comments", "wp-block-post-comments-form", "wp-block-comments-query-loop", "post-navigation",
	"wp-block-post-navigation-link", "post-edit-link", "edit-link", "screen-reader-text", "skip-link"}

// seogoCleanContent removes from rendered HTML what is not the content: navigation, sidebars,
// scripts, comments, Pursae SEO's table of contents and breadcrumbs and, with dropTitle, an <h1>
// that is the post title (the analysis treats the title on its own, like the stored content does).
// It reports whether a table of contents was removed.
func seogoCleanContent(content, title string, dropTitle bool) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return content, false
	}
	nodes, err := html.ParseFragment(strings.NewReader(content), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return content, false
	}
	title = strings.ToLower(strings.Join(strings.Fields(html.UnescapeString(title)), " "))
	toc := false
	var clean func(n *html.Node)
	clean = func(n *html.Node) {
		for ch := n.FirstChild; ch != nil; {
			next := ch.NextSibling
			if seogoIsChrome(ch, title, dropTitle, &toc) {
				n.RemoveChild(ch)
			} else {
				clean(ch)
			}
			ch = next
		}
	}
	var b strings.Builder
	for _, n := range nodes {
		if seogoIsChrome(n, title, dropTitle, &toc) {
			continue
		}
		clean(n)
		_ = html.Render(&b, n)
	}
	return b.String(), toc
}

func seogoIsChrome(n *html.Node, title string, dropTitle bool, toc *bool) bool {
	switch n.Type {
	case html.CommentNode:
		return true
	case html.ElementNode:
	default:
		return false
	}
	switch n.DataAtom {
	case atom.Nav, atom.Aside, atom.Script, atom.Style, atom.Noscript, atom.Template:
		if strings.Contains(seogoAttr(n, "class"), "seogo-toc") {
			*toc = true
		}
		return true
	case atom.H1:
		if dropTitle && title != "" && strings.ToLower(strings.Join(strings.Fields(seogoNodeText(n)), " ")) == title {
			return true
		}
	}
	switch seogoAttr(n, "role") {
	case "navigation", "complementary", "search":
		return true
	}
	if id := seogoAttr(n, "id"); id == "comments" || id == "respond" {
		return true
	}
	for _, cl := range strings.Fields(seogoAttr(n, "class")) {
		for _, chrome := range seogoChromeClasses {
			if cl == chrome || (chrome == "seogo-breadcrumb" && strings.HasPrefix(cl, chrome)) {
				if chrome == "seogo-toc" {
					*toc = true
				}
				return true
			}
		}
	}
	return false
}

func seogoAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func seogoNodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return b.String()
}
