package server

import (
	"context"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// The front end of imported designs (docs/developer/web-builder-code-sections.md): the parts of a
// request made of code sections share their shell (the design's wrapper around its header, main and
// footer, printed once), the header marks the current page's link (its ActiveRule), and the design's
// <html> lang and attributes and <body> attributes are printed.

// wbLangRE is a language tag a design may give its <html> (tr, en-US, zh-Hant-TW).
var wbLangRE = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8}){0,3}$`)

var (
	wbCodeInfoCache sync.Map // "post:rev:updated" → *wbCodeInfoEntry
	wbCodeInfoN     atomic.Int64
)

type wbCodeInfoEntry struct{ info *webbuilder.CodeDocInfo }

// wbContentTags is wp_filter_content_tags over a filled snapshot, except a page of code sections:
// the design's images are printed as written (no loading="lazy", decoding or added dimensions, which
// change what the design loads and when).
func (s *Server) wbContentTags(ctx context.Context, src string) string {
	if webbuilder.IsCodePage(src) {
		return src
	}
	return s.filterContentTags(ctx, src)
}

// wbCodeInfo is what a published doc's code sections say about the document (nil without any),
// read once per doc revision.
func (s *Server) wbCodeInfo(d *store.WebBuilderDoc) *webbuilder.CodeDocInfo {
	if d == nil || !webbuilder.HasCode(d.Doc) {
		return nil
	}
	key := strconv.FormatInt(d.PostID, 10) + ":" + strconv.Itoa(d.Rev) + ":" + strconv.FormatInt(d.UpdatedAt.UnixNano(), 36)
	if v, ok := wbCodeInfoCache.Load(key); ok {
		return v.(*wbCodeInfoEntry).info
	}
	info := webbuilder.CodeInfo(d.Doc)
	if wbCodeInfoN.Add(1) > 512 {
		wbCodeInfoCache.Clear()
		wbCodeInfoN.Store(0)
	}
	wbCodeInfoCache.Store(key, &wbCodeInfoEntry{info})
	return info
}

// wbCodeParts finishes the code parts of a Hello Pursae request: the header's and footer's active
// links for the requested path, the shared shell, and the document attributes (lang, <html> and
// <body> attributes) of the parts, main first.
func (s *Server) wbCodeParts(c *gin.Context, tp *wbThemeParts, header, main, footer *wbPart) {
	info := func(p *wbPart) *webbuilder.CodeDocInfo {
		if p == nil || !p.codePage {
			return nil
		}
		return s.wbCodeInfo(p.doc)
	}
	hi, mi, fi := info(header), info(main), info(footer)
	current := c.Request.URL.Path
	if hi != nil && hi.Active != nil {
		tp.view.Header = template.HTML(webbuilder.ApplyActive(string(tp.view.Header), hi.Active, current))
	}
	if fi != nil && fi.Active != nil {
		tp.view.Footer = template.HTML(webbuilder.ApplyActive(string(tp.view.Footer), fi.Active, current))
	}
	// One shell around header, main and footer: a code main must sit in the same wrappers; any
	// other main (a theme view, builder sections, a forum view) is printed inside them. A page
	// without a footer (or header) shares it with the header (or footer) alone (ShellPlan).
	mainCode := mi != nil && tp.view.Main != ""
	var hc, fc []string
	if hi != nil && tp.view.Header != "" {
		hc = hi.Last
	}
	if fi != nil && tp.view.Footer != "" {
		fc = fi.First
	}
	mInfo := mi
	if !mainCode {
		mInfo = nil
	}
	if k, withH, withF := webbuilder.ShellPlan(hc, fc, mInfo); k > 0 {
		from := hc
		if !withH {
			from = fc
		}
		chain := make([]string, k)
		for i, w := range from[:k] {
			chain[i] = s.wbKitVersioned(w)
		}
		hv, fv := string(tp.view.Header), string(tp.view.Footer)
		if !withH {
			hv = ""
		}
		if !withF {
			fv = ""
		}
		if h, m, f, ok := webbuilder.ShareShell(hv, string(tp.view.Main), fv, chain, mainCode); ok {
			if withH {
				tp.view.Header = template.HTML(h)
			}
			if withF {
				tp.view.Footer = template.HTML(f)
			}
			tp.view.Main = template.HTML(m)
		}
	}
	var langs, htmlAttrs, bodyAttrs []string
	for _, x := range []*webbuilder.CodeDocInfo{mi, hi, fi} {
		if x == nil {
			continue
		}
		langs = append(langs, x.Lang)
		htmlAttrs = append(htmlAttrs, x.HTMLAttrs)
		bodyAttrs = append(bodyAttrs, x.BodyAttrs)
	}
	for _, l := range langs {
		if wbLangRE.MatchString(l) {
			tp.view.Lang = l
			break
		}
	}
	a, cls := webbuilder.MergeDocAttrs([]string{"lang"}, htmlAttrs...)
	if cls != "" {
		a = strings.TrimSpace(`class="` + template.HTMLEscapeString(cls) + `" ` + a)
	}
	if a != "" {
		tp.view.HTMLAttrs = template.HTMLAttr(" " + a)
	}
	a, cls = webbuilder.MergeDocAttrs([]string{"lang"}, bodyAttrs...)
	if a != "" {
		tp.view.BodyAttrs = template.HTMLAttr(" " + a)
	}
	tp.bodyClass = cls
}

// wbCodeDocAttrs is the document attributes of a canvas page of code sections: its lang and the
// <html> and <body> attribute strings (body class apart).
func (s *Server) wbCodeDocAttrs(d *store.WebBuilderDoc) (lang, htmlAttrs, bodyAttrs, bodyClass string) {
	info := s.wbCodeInfo(d)
	if info == nil {
		return "", "", "", ""
	}
	if wbLangRE.MatchString(info.Lang) {
		lang = info.Lang
	}
	a, cls := webbuilder.MergeDocAttrs([]string{"lang"}, info.HTMLAttrs)
	if cls != "" {
		a = strings.TrimSpace(`class="` + template.HTMLEscapeString(cls) + `" ` + a)
	}
	htmlAttrs = a
	bodyAttrs, bodyClass = webbuilder.MergeDocAttrs([]string{"lang"}, info.BodyAttrs)
	return
}
