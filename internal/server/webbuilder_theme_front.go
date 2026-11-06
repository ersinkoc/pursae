package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// The Theme Builder front (contract §11.9): while the plugin is on and Hello Pursae renders the
// request (active or previewed), wbRenderPage hands the view to wbRenderTheme. The header, main
// and footer parts are picked (wbPickTemplate, webbuilder_templates.go), filled over the view
// (webbuilder_slots.go) and given to Hello Pursae's own skeleton in v.Data["wb"]
// (wbThemePartsView, theme_hellopursae.go); the theme prints its minimal defaults for every
// location without a part.
//
//   - Main: the singular post's own builder doc (layout canvas: no header and footer part), else
//     the main template that matched, else none.
//   - Assets: the builder's base CSS, the merged fonts and one <style id="wb-css-<postID>"> (or
//     stylesheet link) per part are added to the head; the .wb-js class script goes first and the
//     runtime is loaded once, deferred, before </body>, when some part needs it.
//   - Cache: wb-theme on every page with a part, wb-loop when a part runs a post loop (purged when
//     any viewable post is saved, wbPurgeLoops).
//   - Admin bar: Edit Header / Edit Template / Edit Footer for the template parts in use.

// wbPart is one filled location.
type wbPart struct {
	loc      string // webbuilder.LocationHeader, LocationMain, LocationFooter
	id       int64  // the post id of the doc (template or page)
	template bool   // a Theme Builder template (else the page's own doc)
	doc      *store.WebBuilderDoc
	filled   wbFilled
	// draftNewer: a preview of the page by a user whose autosave is newer than the published doc.
	draftNewer bool
}

// wbThemeParts is what wbRenderTheme prints.
type wbThemeParts struct {
	view   *wbThemePartsView
	parts  []*wbPart // in document order: header, main, footer
	canvas bool      // the page's own doc with layout canvas: no header and footer part
}

// wbThemeParts picks and fills the parts of the request. nil when no location has one.
func (s *Server) wbThemeParts(c *gin.Context, v *siteView) *wbThemeParts {
	q := v.Q
	out := &wbThemeParts{view: &wbThemePartsView{}}
	var main *wbPart
	// A main template previewed by ?wb_template_preview=<id> wins even over the post's own doc.
	if q.Store == "" && s.wbTemplatePreviewing(c) {
		if d, id, ok := s.wbTemplatePreview(c, webbuilder.LocationMain); ok {
			main = &wbPart{loc: webbuilder.LocationMain, id: id, template: true, doc: d}
		}
	}
	// The singular post's own builder doc.
	if main == nil && q.Post != nil && (v.Template == "page" || v.Template == "single") {
		if fp := s.wbFrontFor(c, q, q.Post); fp != nil {
			main = &wbPart{loc: webbuilder.LocationMain, id: q.Post.ID, doc: fp.doc, draftNewer: fp.preview && fp.draftNewer}
			var ds webbuilder.DocState
			var ps webbuilder.PageSettings
			if len(fp.doc.Doc) > 0 {
				_ = json.Unmarshal(fp.doc.Doc, &ds)
			}
			if len(fp.doc.Page) > 0 {
				_ = json.Unmarshal(fp.doc.Page, &ps)
			}
			out.canvas = webbuilder.EffectivePageLayout(ps, &ds) == webbuilder.PageLayoutCanvas
		}
	}
	if main == nil && q.Store == "" {
		if d, id := s.wbPickTemplate(c, webbuilder.LocationMain, q); d != nil {
			main = &wbPart{loc: webbuilder.LocationMain, id: id, template: true, doc: d}
		}
	}
	var header, footer *wbPart
	if out.canvas && s.wbTemplatePreviewing(c) {
		// A header or footer template previewed on a canvas page (its preview object is often the
		// front page): show it, or the preview would be empty.
		if d, id, ok := s.wbTemplatePreview(c, webbuilder.LocationHeader); ok {
			header, out.canvas = &wbPart{loc: webbuilder.LocationHeader, id: id, template: true, doc: d}, false
		} else if d, id, ok := s.wbTemplatePreview(c, webbuilder.LocationFooter); ok {
			footer, out.canvas = &wbPart{loc: webbuilder.LocationFooter, id: id, template: true, doc: d}, false
		}
	}
	if !out.canvas {
		if d, id := s.wbPickTemplate(c, webbuilder.LocationHeader, q); d != nil && header == nil {
			header = &wbPart{loc: webbuilder.LocationHeader, id: id, template: true, doc: d}
		}
		if d, id := s.wbPickTemplate(c, webbuilder.LocationFooter, q); d != nil && footer == nil {
			footer = &wbPart{loc: webbuilder.LocationFooter, id: id, template: true, doc: d}
		}
	}
	for _, p := range []*wbPart{header, main, footer} {
		if p == nil {
			continue
		}
		p.filled = s.wbFill(c, v, p.doc, !p.template)
		out.parts = append(out.parts, p)
		h := template.HTML(p.filled.html)
		switch p.loc {
		case webbuilder.LocationHeader:
			out.view.Header = h
		case webbuilder.LocationFooter:
			out.view.Footer = h
		default:
			if p.draftNewer {
				h = template.HTML(wbDraftNotice(s, c)) + h
			}
			out.view.Main = h
			out.view.HasMain = wbHasMainLandmark(p.filled.html)
		}
	}
	if len(out.parts) == 0 {
		return nil
	}
	out.view.Canvas = out.canvas // Hello Pursae then prints no header or footer at all
	return out
}

// wbRenderTheme renders the request with Hello Pursae and its Theme Builder parts. false (nothing
// written) when no location has a part: the theme renders the view as usual.
func (s *Server) wbRenderTheme(c *gin.Context, v *siteView, status int) bool {
	tp := s.wbThemeParts(c, v)
	if tp == nil {
		return false
	}
	v.Data[wbThemePartsKey] = tp.view
	v.BodyClass = strings.TrimSpace(v.BodyClass + " wb-site")

	var fonts []webbuilder.FontNeed
	feats := map[string]bool{}
	var css strings.Builder
	loops := false
	var bodies []string
	for _, p := range tp.parts {
		var needs []webbuilder.FontNeed
		if len(p.doc.Fonts) > 0 && json.Unmarshal(p.doc.Fonts, &needs) == nil {
			fonts = append(fonts, needs...)
		}
		for _, f := range p.doc.Features {
			feats[f] = true
		}
		css.WriteString(s.wbPartCSS(c, p.doc, "wb-css-"+itoa64(p.id), func() bool {
			if !p.template && v.Q.Post != nil && v.Q.Post.ID == p.id {
				return v.Q.Post.Status == "publish" && v.Q.Post.Password == ""
			}
			post, err := s.store.GetPost(c.Request.Context(), p.id)
			return err == nil && post.Status == "publish" && post.Password == ""
		}))
		loops = loops || p.filled.loops
		bodies = append(bodies, p.filled.html)
	}
	site, err := s.wbSiteAssets()
	if err != nil {
		log.Printf("web builder: %v", err)
	}
	runtime := len(feats) > 0 && site.Runtime != ""
	lb := s.links(c)

	var head strings.Builder
	if runtime { // the runtime's hiding rules apply only when it will run (content stays readable without JS)
		head.WriteString("<script>document.documentElement.classList.add('" + webbuilder.ClassJS + "')</script>\n")
	}
	head.WriteString(string(v.Head))
	if site.CSS != "" {
		fmt.Fprintf(&head, "<link rel='stylesheet' id='web-builder-css' href='%s' media='all' />\n",
			html.EscapeString(lb.HomeURL(webbuilder.SiteAssetsPath+site.CSS)))
	}
	if len(fonts) > 0 {
		if raw, err := json.Marshal(fonts); err == nil {
			head.WriteString(s.wbFontLinks(c, raw))
		}
	}
	head.WriteString(css.String())
	v.Head = template.HTML(head.String())
	if runtime {
		v.Footer += template.HTML(fmt.Sprintf("<script defer src=\"%s\" id=\"web-builder-runtime-js\"></script>\n",
			html.EscapeString(lb.HomeURL(webbuilder.SiteAssetsPath+site.Runtime))))
	}
	if v.AdminBar != "" {
		v.AdminBar = template.HTML(wbInsertAdminBarNodes(string(v.AdminBar), s.wbThemeAdminBarNodes(c, tp)))
	}
	wbEmbedCSP(c, strings.Join(bodies, "\n"))
	wbAddCacheTags(c, true, loops)
	s.renderSiteTemplate(c, v, status)
	return true
}

// wbAddCacheTags adds wb-theme / wb-loop to the response's cache tags (cacheTags already ran).
func wbAddCacheTags(c *gin.Context, theme, loops bool) {
	var add []string
	if theme {
		add = append(add, webbuilder.CacheTagTheme)
	}
	if loops {
		add = append(add, webbuilder.CacheTagLoop)
	}
	if len(add) == 0 {
		return
	}
	cur := c.Writer.Header().Get(cache.HeaderTags)
	if cur != "" {
		cur += ","
	}
	c.Header(cache.HeaderTags, cur+strings.Join(add, ","))
}

// wbPurgeLoops drops the cached pages whose parts run a post loop when a post that loops can show
// is saved (any status change of a viewable type: published, unpublished, trashed, edited).
func (s *Server) wbPurgeLoops(_ context.Context, p *store.Post) {
	if p == nil || wbIsTemplate(p) || !siteViewableType(p.Type) || p.Status == "auto-draft" {
		return
	}
	s.purgeTags(webbuilder.CacheTagLoop)
}

// wbAdminBarSecondary is where the toolbar's primary group ends (frontAdminBar, site_head.go).
const wbAdminBarSecondary = `</ul><ul role='menu' id='wp-admin-bar-top-secondary'`

// wbInsertAdminBarNodes adds nodes at the end of the toolbar's primary group.
func wbInsertAdminBarNodes(bar, nodes string) string {
	if nodes == "" {
		return bar
	}
	i := strings.Index(bar, wbAdminBarSecondary)
	if i < 0 {
		return bar
	}
	return bar[:i] + nodes + bar[i:]
}

// wbThemeAdminBarNodes: Edit Header, Edit Template, Edit Footer for the template parts of the
// page, for users who may edit templates.
func (s *Server) wbThemeAdminBarNodes(c *gin.Context, tp *wbThemeParts) string {
	u := currentUser(c)
	if u == nil || !u.Can(webbuilder.TemplateCap, nil) {
		return ""
	}
	var sb strings.Builder
	for _, p := range tp.parts {
		if !p.template {
			continue
		}
		id, label := "web_builder_edit_template", "Edit Template"
		switch p.loc {
		case webbuilder.LocationHeader:
			id, label = "web_builder_edit_header", "Edit Header"
		case webbuilder.LocationFooter:
			id, label = "web_builder_edit_footer", "Edit Footer"
		}
		sb.WriteString(`<li role='group' id='wp-admin-bar-` + id + `'><a class='ab-item' role="menuitem" href='` +
			html.EscapeString(wbEditURL(p.id)) + `'><span class="ab-icon dashicons-layout" aria-hidden="true"></span><span class="ab-label">` +
			html.EscapeString(s.__(c, label)) + `</span></a></li>`)
	}
	return sb.String()
}
