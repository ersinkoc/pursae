package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	pathpkg "path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
	"wpgo/internal/store"
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
// The frame's path is the server's: the post's permalink (a draft's ?p= / ?page_id= address, a
// template's preview address), never one the request names; a path under /wp-admin, /wp-login.php,
// /wp-json, /wp-content, /wp-includes or /xmlrpc.php is refused (wbLiveAddr). The middleware answers
// the token at that path whatever route would otherwise serve it.
//
// Only users with unfiltered_html (who may publish the scripts anyway; with an Application Password
// the site-code scope too, wbUnfiltered) and edit rights on the post get a frame; the markup is
// theirs and is shown to them only. Nothing is stored in the database: the frames are kept in
// memory, at most wbLiveMaxTotal bytes in all and wbLiveUserBytes per user (the oldest go first),
// each for wbLiveTTL. The response says Referrer-Policy: no-referrer, so the token in its address
// is not sent on with the design's requests (GL_HOOK also takes it out of the address before any
// design script runs).

const (
	wbLiveQuery     = "wb-live-frame"
	wbLiveTTL       = 15 * time.Minute
	wbLiveMax       = 64       // frames kept in all (oldest dropped)
	wbLivePerUser   = 6        // frames kept per user
	wbLiveMaxBytes  = 8 << 20  // one request body
	wbLiveMaxTotal  = 64 << 20 // bytes of all frames kept (oldest dropped)
	wbLiveUserBytes = 24 << 20 // bytes of one user's frames kept (their oldest dropped)
)

type wbLiveEntry struct {
	user, post int64
	path, html string
	at         time.Time
}

var wbLiveFrames = struct {
	sync.Mutex
	m     map[string]*wbLiveEntry
	order []string // oldest first
	bytes int      // the html of every frame kept
}{m: map[string]*wbLiveEntry{}}

// wbLiveSize is what a kept frame counts against the caps.
func wbLiveSize(e *wbLiveEntry) int { return len(e.html) + len(e.path) + 64 }

// wbLivePrune drops expired frames, then the oldest of e's user beyond wbLivePerUser - 1 frames or
// wbLiveUserBytes - size bytes, then the oldest of all beyond wbLiveMax - 1 frames or
// wbLiveMaxTotal - size bytes, making room for a frame of size bytes (size 0, e nil: expiry only).
// The caller holds the lock.
func wbLivePrune(now time.Time, e *wbLiveEntry, size int) {
	f := &wbLiveFrames
	drop := map[string]bool{}
	for _, k := range f.order {
		if x := f.m[k]; x == nil || now.Sub(x.at) >= wbLiveTTL {
			drop[k] = true
		}
	}
	if e != nil {
		n, b := 0, 0
		for i := len(f.order) - 1; i >= 0; i-- { // newest first: what the user keeps
			k := f.order[i]
			x := f.m[k]
			if drop[k] || x == nil || x.user != e.user {
				continue
			}
			n++
			b += wbLiveSize(x)
			if n >= wbLivePerUser || b+size > wbLiveUserBytes {
				drop[k] = true
			}
		}
	}
	keep := f.order[:0]
	f.bytes = 0
	for _, k := range f.order {
		if drop[k] {
			delete(f.m, k)
			continue
		}
		keep = append(keep, k)
		f.bytes += wbLiveSize(f.m[k])
	}
	f.order = keep
	if e == nil {
		return
	}
	for len(f.order) > 0 && (len(f.order) >= wbLiveMax || f.bytes+size > wbLiveMaxTotal) {
		k := f.order[0]
		f.bytes -= wbLiveSize(f.m[k])
		delete(f.m, k)
		f.order = f.order[1:]
	}
}

// wbLivePut keeps e under a new token ("" when e alone is larger than a user may keep).
func wbLivePut(e *wbLiveEntry) string {
	size := wbLiveSize(e)
	if size > wbLiveUserBytes || size > wbLiveMaxTotal {
		return ""
	}
	var b [18]byte
	_, _ = rand.Read(b[:])
	tok := hex.EncodeToString(b[:])
	f := &wbLiveFrames
	f.Lock()
	defer f.Unlock()
	now := time.Now()
	wbLivePrune(now, e, size)
	e.at = now
	f.m[tok] = e
	f.order = append(f.order, tok)
	f.bytes += size
	return tok
}

// wbLiveGet is the frame kept under tok (nil when it expired); expired frames are dropped.
func wbLiveGet(tok string) *wbLiveEntry {
	f := &wbLiveFrames
	f.Lock()
	defer f.Unlock()
	now := time.Now()
	wbLivePrune(now, nil, 0)
	e := f.m[tok]
	if e == nil || now.Sub(e.at) >= wbLiveTTL {
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
	if e == nil || u == nil || u.ID != e.user || !wbUnfiltered(c, u) || r.URL.Path != e.path ||
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
	// The token is in the frame's address: no Referer carries it to the design's requests.
	c.Header("Referrer-Policy", "no-referrer")
	c.Data(http.StatusOK, "text/html; charset=UTF-8", []byte(out))
}

// wbLiveBody is the POST body: the editor's sections and stylesheets.
type wbLiveBody struct {
	// URL is ignored (older editors send the page's address): the server builds the frame's path
	// from the post (wbLiveAddr).
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

// wbRESTLiveFrame is POST documents/{id}/live-frame → {url, path}.
func (s *Server) wbRESTLiveFrame(c *gin.Context, r *restRequest) {
	u, p := s.wbPost(c, r)
	if p == nil {
		return
	}
	if !wbUnfiltered(c, u) {
		wbForbidden(c, u)
		return
	}
	path, query, ok := wbLivePath(s.wbLiveAddr(c, p), s.links(c).HomeURL("/"))
	if !ok {
		restErr(c, http.StatusConflict, "wb_live_path", "This item has no front-end address the live canvas can use.")
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
	doc := s.wbLiveRender(c, p.ID, path, &body)
	tok := wbLivePut(&wbLiveEntry{user: u.ID, post: p.ID, path: path, html: doc})
	if tok == "" {
		restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, "The page is too large for the live canvas.", map[string]any{"limit": wbLiveUserBytes})
		return
	}
	q := url.Values{}
	if query != "" {
		q, _ = url.ParseQuery(query)
	}
	q.Del(wbLiveQuery)
	q.Set(wbLiveQuery, tok)
	c.JSON(http.StatusOK, map[string]any{"url": path + "?" + q.Encode(), "path": path})
}

// wbLiveAddr is the front-end address of p as the editor's links name it: its permalink (a draft's
// or a pending item's is its ?p= / ?page_id= address, which the token is answered at like any
// other), a Theme Builder template's preview address.
func (s *Server) wbLiveAddr(c *gin.Context, p *store.Post) string {
	if wbIsTemplate(p) {
		if t, err := s.store.WebBuilder().Template(c, p.ID); err == nil {
			if info, _ := s.wbTemplatePreviewInfo(c, t); info.URL != "" {
				return info.URL
			}
		}
		return s.links(c).HomeURL("/")
	}
	return s.links(c).Permalink(p)
}

// wbLiveReserved are the site's own non-front-end paths (relative to the home path; a folder with
// everything under it): a live frame is never served there.
var wbLiveReserved = []string{"/wp-admin", "/wp-login.php", "/wp-json", "/wp-content", "/wp-includes", "/wp-cron.php", "/wp-signup.php", "/wp-activate.php", "/xmlrpc.php"}

// wbLivePath is the path and query of a front-end address of the site (home: the site's home
// URL); ok is false for an address that is not a front-end page of the site (another host, a
// relative or malformed address, a wbLiveReserved path, a REST route in the query).
func wbLivePath(addr, home string) (path, query string, ok bool) {
	pu, err := url.Parse(strings.TrimSpace(addr))
	hu, herr := url.Parse(home)
	if err != nil || herr != nil || pu.Path == "" || !strings.HasPrefix(pu.Path, "/") || strings.HasPrefix(pu.Path, "//") ||
		strings.Contains(pu.Path, "\\") || strings.HasSuffix(strings.ToLower(pu.Path), ".php") || pu.Opaque != "" || pu.User != nil {
		return "", "", false
	}
	if pu.Host != "" && (!strings.EqualFold(pu.Host, hu.Host) || pu.Scheme != hu.Scheme) {
		return "", "", false
	}
	if clean := pathpkg.Clean(pu.Path); clean != pu.Path && clean+"/" != pu.Path {
		return "", "", false // dot segments, doubled slashes
	}
	rel := pu.Path
	if base := strings.TrimSuffix(hu.Path, "/"); base != "" {
		if rel != base && !strings.HasPrefix(rel, base+"/") {
			return "", "", false
		}
		rel = strings.TrimPrefix(rel, base)
		if rel == "" {
			rel = "/"
		}
	}
	low := strings.ToLower(rel)
	for _, r := range wbLiveReserved {
		if low == r || strings.HasPrefix(low, r+"/") {
			return "", "", false
		}
	}
	if v, _ := url.ParseQuery(pu.RawQuery); v.Has("rest_route") {
		return "", "", false
	}
	return pu.EscapedPath(), pu.RawQuery, true
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
