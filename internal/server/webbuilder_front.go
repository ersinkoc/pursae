package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Pursae Web Builder on the site (contract §10, docs/developer/web-builder-contract.md). A page
// published from the builder is served as a canvas document: no theme stylesheet, theme scripts,
// header or footer, and no React. The body is the snapshot the editor rendered at publish time
// (web_builder_docs.html, sanitized by the REST publish), its images passed through
// filterContentTags for width/height, srcset and the loading attributes. The head keeps everything
// that is not the theme's (SEO, Gooike consent and analytics, plugin heads, the admin bar styles)
// and adds the builder's base CSS, the page CSS (inline up to webbuilder.InlineCSSMax, a file under
// uploads/web-builder/css above it) and the localized fonts. The vanilla data-wb-* runtime is
// loaded with defer only when the snapshot needs one of its features.
//
// The canvas applies when the plugin is on, the request is the singular view of a post with a
// web_builder_docs row and that row is not detached (posts.content was edited outside the
// builder). Otherwise the theme renders posts.content, the semantic fallback the publish writes,
// so pages of the old builder (no row) are untouched. Two hooks lead here: theContent
// (site_view.go) returns the snapshot as the main post's content, renderSite (site.go) prints the
// canvas instead of the theme template.
//
// A preview (?preview=true by a user who may edit the post) shows the last published snapshot:
// snapshots are rendered in the browser, a draft has none (§3.1). When the previewing user has an
// unpublished autosave newer than it, a notice says so.

// wbFrontOn is the plugin switch (the same option as webbuilder_plugin.go's wbOn).
func (s *Server) wbFrontOn() bool { return s.settings.Bool(webbuilder.PluginOption) }

// ctxWBFront caches the per-request decision: post id → *wbFrontPage (nil = theme path).
const ctxWBFront = "wb.front"

// wbFrontPage is a post the canvas renders in this request.
type wbFrontPage struct {
	doc        *store.WebBuilderDoc
	preview    bool // ?preview=true by a user who may edit the post
	draftNewer bool // the previewing user has an autosave newer than the published doc
	body       string
	bodyDone   bool
}

// wbFrontFor decides once per request and post whether the canvas renders p as the main query's
// singular post (q). nil means the theme path.
func (s *Server) wbFrontFor(c *gin.Context, q *siteQuery, p *store.Post) *wbFrontPage {
	if c == nil || p == nil || !s.wbFrontOn() {
		return nil
	}
	cache, _ := c.Get(ctxWBFront)
	m, _ := cache.(map[int64]*wbFrontPage)
	if m == nil {
		m = map[int64]*wbFrontPage{}
		c.Set(ctxWBFront, m)
	}
	if fp, ok := m[p.ID]; ok {
		return fp
	}
	fp := s.wbFrontDecide(c, q, p)
	m[p.ID] = fp
	return fp
}

func (s *Server) wbFrontDecide(c *gin.Context, q *siteQuery, p *store.Post) *wbFrontPage {
	if (p.Type != "page" && p.Type != "post") || p.Status == "trash" {
		return nil
	}
	if s.passwordRequired(c, p) { // the theme prints the password form
		return nil
	}
	ctx := c.Request.Context()
	wb := s.store.WebBuilder()
	doc, err := wb.Doc(ctx, p.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("web builder: front doc %d: %v", p.ID, err)
		}
		return nil
	}
	if doc.Detached || strings.TrimSpace(doc.HTML) == "" {
		return nil
	}
	fp := &wbFrontPage{doc: doc}
	if u := currentUser(c); q != nil && q.Preview && u != nil && u.Can("edit_post", p) {
		fp.preview = true
		if d, err := wb.Draft(ctx, p.ID, u.ID); err == nil && d.UpdatedAt.After(doc.UpdatedAt) {
			fp.draftNewer = true
		}
	}
	return fp
}

// wbContent is theContent's hook: the snapshot is the content of the main query's singular post
// when the canvas renders it. Feeds, archives and other posts keep posts.content.
func (s *Server) wbContent(ctx context.Context, c *gin.Context, p *store.Post, singular bool) (string, bool) {
	rc := renderFrom(ctx)
	if !singular || c == nil || rc == nil || rc.feed || rc.q == nil || rc.q.Post == nil || rc.q.Post.ID != p.ID {
		return "", false
	}
	fp := s.wbFrontFor(c, rc.q, p)
	if fp == nil {
		return "", false
	}
	return s.wbBody(ctx, fp), true
}

// wbBody is the stored snapshot with wp_filter_content_tags applied (once per request).
func (s *Server) wbBody(ctx context.Context, fp *wbFrontPage) string {
	if !fp.bodyDone {
		fp.body, fp.bodyDone = s.filterContentTags(ctx, fp.doc.HTML), true
	}
	return fp.body
}

// wbRenderPage is renderSite's hook: it writes the canvas document and reports true, or false
// when the theme renders the view.
func (s *Server) wbRenderPage(c *gin.Context, v *siteView, status int) bool {
	if v == nil || v.Q == nil || v.Q.Post == nil || (v.Template != "page" && v.Template != "single") {
		return false
	}
	fp := s.wbFrontFor(c, v.Q, v.Q.Post)
	if fp == nil {
		return false
	}
	ctx := v.ctx
	if ctx == nil {
		ctx = c.Request.Context()
	}
	out := s.wbCanvas(c, v, fp, s.wbBody(ctx, fp))
	c.Data(status, "text/html; charset=UTF-8", s.asStripVersion(c, s.golylangHTML(c, s.gooikeFilterPage(c, []byte(out)))))
	return true
}

// wbCanvas assembles the canvas document.
func (s *Server) wbCanvas(c *gin.Context, v *siteView, fp *wbFrontPage, body string) string {
	d := fp.doc
	lb := s.links(c)
	adminBar := v.AdminBar != ""
	site, err := s.wbSiteAssets()
	if err != nil {
		log.Printf("web builder: %v", err)
	}
	runtime := len(d.Features) > 0 && site.Runtime != ""

	// wp_head without the theme: a copy of the view whose theme brings no head, no block styles
	// and no Additional CSS (the Customizer's CSS belongs to the theme).
	bare := *v
	bare.Theme = &Theme{Slug: v.Theme.Slug, Name: v.Theme.Name, BlockStyles: []string{}}
	bare.CustomCSS = ""
	head := strings.Replace(string(s.wpHead(c, v.Q, lb, adminBar, &bare)), blockStylesMarker, "", 1)

	loc := s.siteLocale()
	var sb strings.Builder
	sb.Grow(len(body) + len(head) + len(d.CSS) + 4096)
	sb.WriteString("<!DOCTYPE html>\n<html lang=\"" + html.EscapeString(strings.ReplaceAll(loc, "_", "-")) + "\"")
	if i18n.IsRTL(loc) {
		sb.WriteString(` dir="rtl"`)
	}
	sb.WriteString(">\n<head>\n<meta charset=\"UTF-8\" />\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\" />\n")
	if runtime { // the runtime's hiding rules apply only when it will run (content stays readable without JS)
		sb.WriteString("<script>document.documentElement.classList.add('" + webbuilder.ClassJS + "')</script>\n")
	}
	sb.WriteString("<title>" + string(v.DocumentTitle) + "</title>\n")
	sb.WriteString(head)
	if site.CSS != "" {
		fmt.Fprintf(&sb, "<link rel='stylesheet' id='web-builder-css' href='%s' media='all' />\n",
			html.EscapeString(lb.HomeURL(webbuilder.SiteAssetsPath+site.CSS)))
	}
	sb.WriteString(s.wbFontLinks(c, d.Fonts))
	sb.WriteString(s.wbPageCSS(c, d))
	sb.WriteString("</head>\n")
	sb.WriteString("<body class=\"" + html.EscapeString(strings.TrimSpace(v.BodyClass+" web-builder-page")) + "\">\n")
	sb.WriteString(string(v.BodyOpen))
	if fp.preview && fp.draftNewer {
		sb.WriteString(`<div class="wb-preview-notice" role="status" style="position:relative;z-index:99998;margin:0;padding:10px 16px;background:#fcf9e8;border-bottom:1px solid #dba617;color:#1d2327;font:14px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif">`)
		sb.WriteString(html.EscapeString(s.__(c, "This preview shows the last published version of the page. Publish your changes in Pursae Web Builder to see them here.")))
		sb.WriteString("</div>\n")
	}
	// The main landmark: the snapshot is one div.wb-page of sections, and wb.css only uses
	// descendant selectors under .wb-page, so a wrapper changes no styling. A snapshot that brings
	// its own main (an HTML section) keeps it as the only one.
	ownMain := wbHasMainLandmark(body)
	if !ownMain {
		sb.WriteString(`<main id="wb-main">`)
	}
	sb.WriteString(body)
	if !ownMain {
		sb.WriteString("</main>")
	}
	sb.WriteString("\n")
	if runtime {
		fmt.Fprintf(&sb, "<script defer src=\"%s\" id=\"web-builder-runtime-js\"></script>\n",
			html.EscapeString(lb.HomeURL(webbuilder.SiteAssetsPath+site.Runtime)))
	}
	sb.WriteString(string(v.AdminBar))
	sb.WriteString(string(s.wpFooter(c, v.Q, lb, adminBar))) // wp_footer without the theme's footer scripts
	sb.WriteString("\n</body>\n</html>\n")
	wbEmbedCSP(c, body)
	return sb.String()
}

// wbMainRe finds a main element or role="main" in a snapshot.
var wbMainRe = regexp.MustCompile(`(?i)<main[\s>]|\srole\s*=\s*["']?main\b`)

// wbHasMainLandmark reports whether the snapshot already has a main landmark.
func wbHasMainLandmark(body string) bool { return wbMainRe.MatchString(body) }

// --- site assets -------------------------------------------------------------------------------

var wbSiteAssetsCache struct {
	once sync.Once
	m    webbuilder.SiteManifest
	err  error
}

// wbSiteAssets names the public runtime and base CSS from web/web-builder/manifest.json: read once
// from the embedded files, on every call in development (WPGO_WEB_DIR).
func (s *Server) wbSiteAssets() (webbuilder.SiteManifest, error) {
	if s.cfg.WebDir != "" {
		return wbReadSiteAssets(s.web)
	}
	wbSiteAssetsCache.once.Do(func() { wbSiteAssetsCache.m, wbSiteAssetsCache.err = wbReadSiteAssets(s.web) })
	return wbSiteAssetsCache.m, wbSiteAssetsCache.err
}

// wbSiteAssetRe is a plain hashed file name inside the site bundle (nothing that leaves it or
// breaks out of an attribute).
var wbSiteAssetRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:js|css)$`)

func wbReadSiteAssets(fsys fs.FS) (webbuilder.SiteManifest, error) {
	name := webbuilder.EmbedDir + "/" + webbuilder.ManifestFile
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return webbuilder.SiteManifest{}, fmt.Errorf("web builder: %s: %w", name, err)
	}
	var m webbuilder.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return webbuilder.SiteManifest{}, fmt.Errorf("web builder: %s: %w", name, err)
	}
	for _, p := range []*string{&m.Site.Runtime, &m.Site.CSS} {
		if *p != "" && !wbSiteAssetRe.MatchString(*p) {
			return webbuilder.SiteManifest{}, fmt.Errorf("web builder: %s: bad site asset %q", name, *p)
		}
	}
	return m.Site, nil
}

// --- page CSS ----------------------------------------------------------------------------------

// wbPageCSSWritten remembers the content hash of each page CSS file this process wrote or checked.
var wbPageCSSWritten sync.Map // path → hash

// wbPageCSS is the page's CSS (utility + section CSS, sanitized at publish): inline in
// <style id="wb-css"> up to InlineCSSMax, otherwise a stylesheet link to
// uploads/web-builder/css/post-<id>-<rev>.css (written atomically on first use; ?ver= is the
// content hash, since uploads are served as immutable).
func (s *Server) wbPageCSS(c *gin.Context, d *store.WebBuilderDoc) string {
	css := strings.TrimSpace(d.CSS)
	if css == "" || strings.Contains(strings.ToLower(css), "</style") { // the sanitizer refuses </style; never trust it blindly
		return ""
	}
	inline := "<style id=\"wb-css\">\n" + css + "\n</style>\n"
	if len(css) <= webbuilder.InlineCSSMax || s.cfg.UploadsDir == "" {
		return inline
	}
	sum := sha256.Sum256([]byte(css))
	hash := hex.EncodeToString(sum[:])
	rel := webbuilder.PageCSSDir + "/post-" + itoa64(d.PostID) + "-" + strconv.Itoa(d.Rev) + ".css"
	if err := s.wbEnsurePageCSS(d.PostID, d.Rev, filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(rel)), css, hash); err != nil {
		log.Printf("web builder: page css %d: %v", d.PostID, err)
		return inline
	}
	return fmt.Sprintf("<link rel='stylesheet' id='wb-css' href='%s' media='all' />\n",
		html.EscapeString(s.uploadURL(c, rel)+"?ver="+hash[:12]))
}

func (s *Server) wbEnsurePageCSS(postID int64, rev int, path, css, hash string) error {
	if h, ok := wbPageCSSWritten.Load(path); ok && h.(string) == hash {
		return nil
	}
	if b, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) == hash {
			wbPageCSSWritten.Store(path, hash)
			return nil
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wb-css-*")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(css)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	wbPageCSSWritten.Store(path, hash)
	wbPrunePageCSS(dir, postID, rev)
	return nil
}

// wbPrunePageCSS removes this post's CSS files older than the previous rev (a page cached by a
// proxy just before the publish may still ask for the previous one).
func wbPrunePageCSS(dir string, postID int64, rev int) {
	prefix := "post-" + itoa64(postID) + "-"
	matches, _ := filepath.Glob(filepath.Join(dir, prefix+"*.css"))
	for _, m := range matches {
		r, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), prefix), ".css"))
		if err == nil && r < rev-1 {
			_ = os.Remove(m)
			wbPageCSSWritten.Delete(m)
		}
	}
}

// --- fonts -------------------------------------------------------------------------------------

// wbFontLinks links the page's library fonts that are already localized (gfonts.go, downloaded by
// the editor's fonts/css2 requests): one stylesheet per family and a preload of the first family's
// regular face. A visitor's request never starts a download.
func (s *Server) wbFontLinks(c *gin.Context, raw json.RawMessage) string {
	var needs []webbuilder.FontNeed
	if len(raw) == 0 || json.Unmarshal(raw, &needs) != nil || len(needs) == 0 {
		return ""
	}
	lb := s.links(c)
	base := "/wp-content/uploads/" + gfontsBase
	var sb strings.Builder
	seen := map[string]bool{}
	preloaded := false
	for _, n := range needs {
		f, ok := webbuilder.LookupFont(n.Family)
		if !ok {
			continue
		}
		key := gfontKey(f.Family)
		if seen[key] {
			continue
		}
		seen[key] = true
		cssPath := filepath.Join(s.gfontsDir("css"), key+".css")
		st, err := os.Stat(cssPath)
		if err != nil {
			continue
		}
		ver := strconv.FormatInt(st.ModTime().Unix(), 10)
		if !preloaded {
			preloaded = true
			if file := s.wbFontPreload(cssPath, ver); file != "" {
				fmt.Fprintf(&sb, "<link rel=\"preload\" href=\"%s\" as=\"font\" type=\"font/woff2\" crossorigin />\n",
					html.EscapeString(lb.HomeURL(base+"/fonts/"+file)))
			}
		}
		fmt.Fprintf(&sb, "<link rel='stylesheet' id='wb-font-%s-css' href='%s' media='all' />\n",
			html.EscapeString(key), html.EscapeString(lb.HomeURL(base+"/css/"+key+".css?ver="+ver)))
	}
	return sb.String()
}

// wbFontPreload is the woff2 file of a localized family's regular face (cached per file version).
func (s *Server) wbFontPreload(cssPath, ver string) string {
	if v, ok := gfontPreloadCache.Load(cssPath); ok && v.([2]string)[0] == ver {
		return v.([2]string)[1]
	}
	b, err := os.ReadFile(cssPath)
	if err != nil {
		return ""
	}
	file := gfontPreloadFile(string(b))
	gfontPreloadCache.Store(cssPath, [2]string{ver, file})
	return file
}

// --- embeds and the Content-Security-Policy ------------------------------------------------------

var (
	wbIframeRe  = regexp.MustCompile(`(?is)<iframe\b[^>]*>`)
	wbAttrSrcRe = regexp.MustCompile(`(?is)\ssrc\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	wbSrcdocRe  = regexp.MustCompile(`(?is)\ssrcdoc\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	wbURLAttrRe = regexp.MustCompile(`(?is)\s(?:src|href)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))`)
)

// wbEmbedCSP adds what the snapshot's iframes load to this response's policy: video players by
// their known hosts, and for html sections (sandboxed srcdoc iframes, which inherit the page's
// policy; only unfiltered_html authors can publish them) the https origins their markup names.
func wbEmbedCSP(c *gin.Context, body string) {
	if c == nil || !strings.Contains(body, "<iframe") {
		return
	}
	var needs embedNeeds
	var extra []string
	for _, tag := range wbIframeRe.FindAllString(body, -1) {
		if m := wbSrcdocRe.FindStringSubmatch(tag); m != nil {
			doc := html.UnescapeString(m[1] + m[2])
			for _, u := range wbURLAttrRe.FindAllStringSubmatch(doc, -1) {
				if o := wbHTTPSOrigin(u[1] + u[2] + u[3]); o != "" && !cspHas(extra, o) {
					extra = append(extra, o)
				}
			}
			continue
		}
		if m := wbAttrSrcRe.FindStringSubmatch(tag); m != nil {
			needs.video(html.UnescapeString(m[1]+m[2]), false, false)
		}
	}
	needs.allow(c)
	for _, dir := range []string{"script-src", "style-src", "frame-src", "connect-src", "img-src", "font-src"} {
		cspAllow(c, dir, extra...)
	}
}

// wbHTTPSOrigin is the https origin of an absolute URL ("" for anything else).
func wbHTTPSOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil ||
		strings.ContainsAny(u.Host, " ;,'\"*") {
		return ""
	}
	return "https://" + strings.ToLower(u.Host)
}
