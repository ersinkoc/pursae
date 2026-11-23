package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
	"wpgo/internal/webbuilder"
)

// The element editor's live frame (builder/host/elements/live.ts): the canvas with the design's
// scripts running must be the published page, so the frame is not a srcdoc document (whose
// location is about:srcdoc, whose CSP is the editor's and whose relative URLs need a <base>) but a
// same-origin document at the page's own address:
//
//  1. POST web-builder/v2/documents/{id}/live-frame with the editor's code sections (the unsaved
//     page, the locked header and footer parts) renders them like the front end prints them
//     (wbLiveRender: FillCode per part, the shared shell, the parts' <html>/<body> attributes and
//     active links (wbCodePartsOf), the heads merged page first (MergeHeads), the tail after the
//     footer, Hello Pursae's or the canvas's main landmark), keeps the editor's data-wbc ids, adds
//     the editor's three stylesheets at the end of the head, and keeps the result for its user
//     under a random token (wbLiveFrames, a few minutes);
//  2. the frame loads <the page's path>?wb-live-frame=<token>; wbLiveFrameServe answers it before
//     any route: only to the user who made it, only as a frame of this origin (Sec-Fetch-Dest), with
//     the front end's CSP for that path, the design's origins (wbCodeCSP) and — when stored
//     administrator scripts get the nonce (cspTrustOn) — the nonce on every script, as the
//     published page gives the code sections' scripts (cspTrustScripts).
//
// Only users with unfiltered_html (who may publish the scripts anyway) and edit rights on the post
// get a frame; the markup is theirs and is shown to them only. Nothing is stored in the database.

const (
	wbLiveQuery    = "wb-live-frame"
	wbLiveTTL      = 15 * time.Minute
	wbLiveMax      = 64 // frames kept in all (oldest dropped)
	wbLivePerUser  = 6  // frames kept per user
	wbLiveMaxBytes = 8 << 20
)

type wbLiveEntry struct {
	user, post int64
	path, html string
	at         time.Time
}

var wbLiveFrames = struct {
	sync.Mutex
	m     map[string]*wbLiveEntry
	order []string
}{m: map[string]*wbLiveEntry{}}

// wbLivePut keeps e under a new token.
func wbLivePut(e *wbLiveEntry) string {
	var b [18]byte
	_, _ = rand.Read(b[:])
	tok := hex.EncodeToString(b[:])
	f := &wbLiveFrames
	f.Lock()
	defer f.Unlock()
	now := time.Now()
	mine := 0
	keep := f.order[:0]
	for i := len(f.order) - 1; i >= 0; i-- { // newest first: count this user's frames
		x := f.m[f.order[i]]
		if x != nil && x.user == e.user {
			mine++
			if mine >= wbLivePerUser {
				delete(f.m, f.order[i])
			}
		}
	}
	for _, k := range f.order {
		if x := f.m[k]; x != nil && now.Sub(x.at) < wbLiveTTL {
			keep = append(keep, k)
		} else {
			delete(f.m, k)
		}
	}
	f.order = keep
	for len(f.order) >= wbLiveMax {
		delete(f.m, f.order[0])
		f.order = f.order[1:]
	}
	e.at = now
	f.m[tok] = e
	f.order = append(f.order, tok)
	return tok
}

func wbLiveGet(tok string) *wbLiveEntry {
	f := &wbLiveFrames
	f.Lock()
	defer f.Unlock()
	e := f.m[tok]
	if e == nil || time.Since(e.at) >= wbLiveTTL {
		return nil
	}
	return e
}

// registerWebBuilderLiveFrame adds the middleware that serves live frames. It must run before the
// routes are registered (gin applies Use only to later routes; NoRoute gets it either way).
func (s *Server) registerWebBuilderLiveFrame(r *gin.Engine) {
	r.Use(s.wbLiveFrameServe)
}

// registerWebBuilderLiveFrameREST adds POST documents/{id}/live-frame (after s.rest exists).
func (s *Server) registerWebBuilderLiveFrameREST() {
	s.rest.register(webbuilder.RESTNamespace, `/web-builder/v2/documents/(?P<id>[\d]+)/live-frame`,
		map[string]restHandler{http.MethodPost: s.wbREST(s.wbRESTLiveFrame, true)}, nil)
}

var wbLiveTokenRE = regexp.MustCompile(`^[0-9a-f]{36}$`)

// wbLiveFrameServe answers GET <path>?wb-live-frame=<token> with the frame its user made.
func (s *Server) wbLiveFrameServe(c *gin.Context) {
	tok, ok := c.GetQuery(wbLiveQuery)
	if !ok {
		c.Next()
		return
	}
	defer c.Abort()
	r := c.Request
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	notFound := func() {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("This live frame has expired. Reload the editor's canvas."))
	}
	e := (*wbLiveEntry)(nil)
	if wbLiveTokenRE.MatchString(tok) {
		e = wbLiveGet(tok)
	}
	u := currentUser(c)
	// Only its user, only while they may still run scripts, only at the page's path, only as a frame.
	if e == nil || u == nil || u.ID != e.user || !u.Can("unfiltered_html", nil) || r.URL.Path != e.path ||
		(r.Header.Get("Sec-Fetch-Dest") != "" && r.Header.Get("Sec-Fetch-Dest") != "iframe") ||
		(r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
		notFound()
		return
	}
	out := e.html
	if s.cspTrustOn() { // the published page gives the design's scripts the nonce (csp_trusted.go)
		out = cspTrustScripts(out)
	}
	s.wbCodeCSP(c, e.html)
	wbEmbedCSP(c, e.html)
	c.Header("Cache-Control", "no-store, private")
	c.Header("X-Robots-Tag", "noindex")
	c.Data(http.StatusOK, "text/html; charset=UTF-8", []byte(out))
}

// wbLiveBody is the POST body: the editor's sections and stylesheets.
type wbLiveBody struct {
	// URL is the page's address on the site (the frame is served at its path).
	URL   string `json:"url"`
	Title string `json:"title"`
	// Shell: "theme" (Hello Pursae with parts), "canvas" (a canvas page) or "part" (a template alone).
	Shell  string            `json:"shell"`
	Page   []json.RawMessage `json:"page"`
	Header []json.RawMessage `json:"header"`
	Footer []json.RawMessage `json:"footer"`
	CSS    struct {
		Kit, Local, Edit string
	} `json:"css"`
	// Hook is the editor's first head script (live.ts GL_HOOK: it records the design's WebGL
	// contexts so a reload can release them, then removes itself).
	Hook string `json:"hook"`
}

// wbRESTLiveFrame is POST documents/{id}/live-frame → {url}.
func (s *Server) wbRESTLiveFrame(c *gin.Context, r *restRequest) {
	u, p := s.wbPost(c, r)
	if p == nil {
		return
	}
	if !u.Can("unfiltered_html", nil) {
		wbForbidden(c, u)
		return
	}
	raw, ok := wbBody(c, r, wbLiveMaxBytes)
	if !ok {
		return
	}
	var body wbLiveBody
	if !wbDecode(c, raw, &body) {
		return
	}
	path, query := wbLivePath(body.URL)
	doc := s.wbLiveRender(c, p.ID, path, &body)
	tok := wbLivePut(&wbLiveEntry{user: u.ID, post: p.ID, path: path, html: doc})
	q := url.Values{}
	if query != "" {
		q, _ = url.ParseQuery(query)
	}
	q.Del(wbLiveQuery)
	q.Set(wbLiveQuery, tok)
	c.JSON(http.StatusOK, map[string]any{"url": path + "?" + q.Encode(), "path": path})
}

// wbLivePath is the path and query of a page address (the site's own: only its path counts).
func wbLivePath(addr string) (path, query string) {
	pu, err := url.Parse(strings.TrimSpace(addr))
	if err != nil || pu.Path == "" || !strings.HasPrefix(pu.Path, "/") || strings.HasPrefix(pu.Path, "//") {
		return "/", ""
	}
	return pu.EscapedPath(), pu.RawQuery
}

// wbLiveKeepID / wbLiveID: FillCode removes the editor's data-wbc ids (StripCodeIDs); the live
// frame needs them, so they travel under another name through it.
const wbLiveKeepID = ` data-wb-live-keep="`

func wbLiveHideIDs(s string) string {
	return strings.ReplaceAll(s, " "+webbuilder.AttrCodeID+`="`, wbLiveKeepID)
}

func wbLiveShowIDs(s string) string {
	return strings.ReplaceAll(s, wbLiveKeepID, " "+webbuilder.AttrCodeID+`="`)
}

// wbLivePart is one part filled like the front end fills it (wbFill → CodeHead → UnwrapCodePage).
type wbLivePart struct {
	html, head string
	info       *webbuilder.CodeDocInfo
}

func wbLiveFill(postID int64, secs []json.RawMessage) *wbLivePart {
	if len(secs) == 0 {
		return nil
	}
	doc, err := json.Marshal(map[string]any{"sections": secs})
	if err != nil {
		return nil
	}
	list, _ := webbuilder.CodeSections(doc)
	var snap strings.Builder
	snap.WriteString(`<div class="wb-page" data-wb-root="" data-wb-post="` + strconv.FormatInt(postID, 10) + `">`)
	n := 0
	for i := range list {
		if list[i].Hidden {
			continue
		}
		list[i].HTML = wbLiveHideIDs(list[i].HTML)
		list[i].Local = nil // the editor's local styles come as one stylesheet (data-wb-local)
		list[i].Binds = ""  // the markup as stored (the published page fills bound content)
		snap.WriteString(`<div ` + webbuilder.AttrCode + `="` + html.EscapeString(list[i].ID) + `"></div>`)
		n++
	}
	if n == 0 {
		return nil
	}
	snap.WriteString(`</div>`)
	filled := webbuilder.FillCode(snap.String(), list, true)
	head, body := webbuilder.CodeHead(filled)
	body = webbuilder.UnwrapCodePage(body)
	return &wbLivePart{html: wbLiveShowIDs(body), head: head, info: webbuilder.CodeInfo(doc)}
}

// wbLiveCSS is an editor stylesheet's text, safe inside <style>.
func wbLiveCSS(css string) string {
	return strings.ReplaceAll(strings.ReplaceAll(css, "</", `<\/`), "<!--", `<\!--`)
}

// wbLiveRender is the frame's document: what the front end prints for the page (Hello Pursae's
// header.html / pages.html around the parts, or wbCanvas), without wp_head's and wp_footer's
// site-wide additions and without the admin bar.
func (s *Server) wbLiveRender(c *gin.Context, postID int64, path string, b *wbLiveBody) string {
	main := wbLiveFill(postID, b.Page)
	var header, footer *wbLivePart
	if b.Shell == "theme" {
		header, footer = wbLiveFill(0, b.Header), wbLiveFill(0, b.Footer)
	}
	var heads []string
	for _, p := range []*wbLivePart{main, header, footer} { // the page's own head first (wbRenderTheme)
		if p != nil {
			heads = append(heads, p.head)
		}
	}
	codeHead := s.wbKitVersioned(webbuilder.MergeHeads(heads...))
	str := func(p *wbLivePart) string {
		if p == nil {
			return ""
		}
		return s.wbKitVersioned(p.html)
	}
	mainHTML, headerHTML, footerHTML, tail := str(main), str(header), str(footer), ""
	lang, htmlAttrs, bodyAttrs, bodyClass := "", "", "", ""
	switch b.Shell {
	case "theme":
		mainHTML, tail = webbuilder.SplitCodeTail(mainHTML)
		headerHTML, footerHTML = webbuilder.StripCodeTail(headerHTML), webbuilder.StripCodeTail(footerHTML)
		info := func(p *wbLivePart) *webbuilder.CodeDocInfo {
			if p == nil {
				return nil
			}
			return p.info
		}
		out := wbCodePartsOf(wbCodePartsIn{header: headerHTML, main: mainHTML, footer: footerHTML, current: path,
			hi: info(header), mi: info(main), fi: info(footer), kits: s.wbKitVersionRef()})
		headerHTML, mainHTML, footerHTML = out.header, out.main, out.footer
		lang, htmlAttrs, bodyAttrs, bodyClass = out.lang, strings.TrimSpace(out.htmlAttrs), strings.TrimSpace(out.bodyAttrs), out.bodyClass
		if lang == "" {
			lang = "en-US"
		}
	default:
		mainHTML = webbuilder.StripCodeTail(mainHTML)
		if main != nil && main.info != nil {
			info := main.info
			if wbLangRE.MatchString(info.Lang) {
				lang = info.Lang
			}
			a, cls := webbuilder.MergeDocAttrs([]string{"lang"}, info.HTMLAttrs)
			if cls != "" {
				a = strings.TrimSpace(`class="` + template.HTMLEscapeString(cls) + `" ` + a)
			}
			htmlAttrs = a
			bodyAttrs, bodyClass = webbuilder.MergeDocAttrs([]string{"lang"}, info.BodyAttrs)
		}
		if lang == "" {
			if b.Shell == "canvas" {
				lang = strings.ReplaceAll(s.siteLocale(), "_", "-")
			} else {
				lang = "en-US"
			}
		}
	}

	var sb strings.Builder
	sb.Grow(len(mainHTML) + len(headerHTML) + len(footerHTML) + len(codeHead) + 4096)
	sb.WriteString("<!DOCTYPE html>\n<html lang=\"" + html.EscapeString(lang) + "\"")
	if b.Shell == "canvas" && i18n.IsRTL(s.siteLocale()) && !strings.Contains(htmlAttrs, "dir=") {
		sb.WriteString(` dir="rtl"`)
	}
	if htmlAttrs != "" {
		sb.WriteString(" " + htmlAttrs)
	}
	sb.WriteString(">\n<head>\n")
	if h := strings.TrimSpace(b.Hook); h != "" && !strings.Contains(strings.ToLower(h), "</script") && len(h) < 4096 {
		sb.WriteString("<script>" + h + "</script>")
	}
	sb.WriteString("<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	if b.Shell == "theme" {
		sb.WriteString("<link rel=\"profile\" href=\"https://gmpg.org/xfn/11\">\n")
	}
	sb.WriteString("<title>" + html.EscapeString(b.Title) + "</title>\n")
	sb.WriteString(codeHead)
	// The editor's stylesheets, last (kitstyle.ts, local.ts, the editor's own rules).
	sb.WriteString("<style data-wb-kit>" + wbLiveCSS(b.CSS.Kit) + "</style><style data-wb-local>" + wbLiveCSS(b.CSS.Local) +
		"</style><style data-wb-edit>" + wbLiveCSS(b.CSS.Edit) + "</style>\n")
	sb.WriteString("</head>\n")
	cls := bodyClass
	if b.Shell == "canvas" {
		cls = strings.TrimSpace("web-builder-page " + bodyClass)
	}
	sb.WriteString("<body class=\"" + html.EscapeString(cls) + "\"")
	if bodyAttrs != "" {
		sb.WriteString(" " + bodyAttrs)
	}
	sb.WriteString(">\n")
	ownMain := wbHasMainLandmark(mainHTML)
	switch b.Shell {
	case "theme": // header.html, pages.html (theme/page), main-open / main-close, byte for byte
		sb.WriteString("\n" + headerHTML + "\n\n")
		if !ownMain {
			sb.WriteString(`<main id="content" class="site-main wb-main-part">`)
		}
		sb.WriteString("\n" + mainHTML + "\n")
		if !ownMain {
			sb.WriteString("</main>")
		}
		sb.WriteString("\n" + footerHTML + tail + "\n\n")
	case "canvas": // wbCanvas
		if !ownMain {
			sb.WriteString(`<main id="wb-main">`)
		}
		sb.WriteString(mainHTML)
		if !ownMain {
			sb.WriteString("</main>")
		}
		sb.WriteString("\n")
	default: // a template alone
		sb.WriteString(mainHTML + "\n")
	}
	sb.WriteString("</body>\n</html>\n")
	return sb.String()
}
