package server

import (
	"context"
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

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Pursae Flipbook: PDFs from the Media Library shown as page-turning books. Every flipbook gets a
// [pursae_flipbook id="N"] shortcode and a full-screen page at /flipbook/N. The PDF is rendered in
// the visitor's browser (pdf.js, Apache-2.0) and turned with StPageFlip (MIT), both vendored under
// web/flipbook/vendor, so the server needs no Ghostscript or ImageMagick and stores nothing but the
// list below. While the plugin is off, the shortcode is left as text and every route answers 404.

const (
	flipbookSlug    = "pursae-flipbook"
	flipbookSetting = "native_pursae_flipbook" // the plugin is active
	flipbookOptions = "pursae_flipbooks"       // flipbookStore as one JSON object
	flipbookVersion = "1.0.0"
	flipbookAssets  = "/wp-content/plugins/pursae-flipbook/assets"
	flipbookTag     = "pursae_flipbook"
	flipbookNonce   = "pursae-flipbook"
)

// flipbook is one book: a PDF of the Media Library and how it is shown.
type flipbook struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	AttachmentID int64  `json:"attachment_id"`
	Author       int64  `json:"author,omitempty"` // the user who made it (0: made before authors were kept)
	PDFURL       string `json:"pdf_url"`
	FileName     string `json:"file_name"`
	Height       int    `json:"height"`     // px of the embed; the full-screen page uses the viewport
	Background   string `json:"background"` // #rrggbb behind the pages
	Download     bool   `json:"download"`   // the toolbar offers the PDF
	Share        bool   `json:"share"`      // the toolbar copies the full-screen link
	Created      string `json:"created"`    // RFC 3339
	Modified     string `json:"modified"`
}

type flipbookStore struct {
	NextID int        `json:"next_id"`
	Items  []flipbook `json:"items"`
}

const (
	flipbookDefaultHeight = 640
	flipbookDefaultBG     = "#1a1a1a"
)

var flipbookMu sync.Mutex // serializes read-modify-write of the option

func (s *Server) flipbookOn() bool { return s.settings.Bool(flipbookSetting) }

func (s *Server) flipbookOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.flipbookOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// flipbookLoad returns the saved books, newest first.
func (s *Server) flipbookLoad() flipbookStore {
	st := flipbookStore{NextID: 1}
	if v := s.settings.Get(flipbookOptions); v != nil {
		if raw, err := json.Marshal(v); err == nil {
			_ = json.Unmarshal(raw, &st)
		}
	}
	if st.NextID < 1 {
		st.NextID = 1
	}
	for i := range st.Items {
		st.Items[i] = flipbookSanitize(st.Items[i])
		if st.Items[i].ID >= st.NextID {
			st.NextID = st.Items[i].ID + 1
		}
	}
	return st
}

func (s *Server) flipbookSave(ctx context.Context, st flipbookStore) error {
	if err := s.settings.Set(ctx, map[string]any{flipbookOptions: st}); err != nil {
		return err
	}
	s.purgeAll(ctx) // pages that embed a book show its new settings
	return nil
}

// flipbookGet returns the book with id.
func (s *Server) flipbookGet(id int) (flipbook, bool) {
	for _, b := range s.flipbookLoad().Items {
		if b.ID == id {
			return b, true
		}
	}
	return flipbook{}, false
}

// flipbookUpdate applies fn to the stored list under the lock and saves it.
func (s *Server) flipbookUpdate(ctx context.Context, fn func(*flipbookStore) error) error {
	flipbookMu.Lock()
	defer flipbookMu.Unlock()
	st := s.flipbookLoad()
	if err := fn(&st); err != nil {
		return err
	}
	return s.flipbookSave(ctx, st)
}

var flipbookColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// flipbookSanitize repairs a book read from the option or an import.
func flipbookSanitize(b flipbook) flipbook {
	b.Title = strings.TrimSpace(b.Title)
	if r := []rune(b.Title); len(r) > 200 { // characters, like the form's maxlength
		b.Title = string(r[:200])
	}
	b.PDFURL = maintMediaURL(strings.TrimSpace(b.PDFURL))
	if b.Height < 240 || b.Height > 2000 {
		b.Height = flipbookDefaultHeight
	}
	if !flipbookColorRe.MatchString(b.Background) {
		b.Background = flipbookDefaultBG
	}
	return b
}

func flipbookNew(title, pdfURL, fileName string, attachmentID, author int64) flipbook {
	now := time.Now().UTC().Format(time.RFC3339)
	return flipbookSanitize(flipbook{Title: title, PDFURL: pdfURL, FileName: fileName, AttachmentID: attachmentID, Author: author,
		Height: flipbookDefaultHeight, Background: flipbookDefaultBG, Download: true, Share: true, Created: now, Modified: now})
}

// flipbookShortcodeText is the shortcode an editor pastes for b.
func flipbookShortcodeText(id int) string {
	return `[` + flipbookTag + ` id="` + strconv.Itoa(id) + `"]`
}

// flipbookPageURL is the book's full-screen page.
func flipbookPageURL(id int) string { return "/flipbook/" + strconv.Itoa(id) }

// --- Plugins screen, menu, routes ----------------------------------------------------------------

func (s *Server) flipbookPluginRow() pluginRow {
	r := pluginRow{Slug: flipbookSlug, Name: "Pursae Flipbook", Version: builtinPluginVersion, Native: true, Active: s.flipbookOn(),
		Description: "Turn PDFs from the Media Library into page-turning flipbooks. Each flipbook gets a shortcode to place it in any post or page and a full-screen link to share. Double pages on wide screens, single pages on phones, keyboard and swipe navigation, thumbnails, zoom and download. PDFs render in the browser, nothing to install on the server.",
		Author:      "Pursae"}
	if r.Active {
		r.SettingsURL = "/wp-admin/admin?page=" + flipbookSlug
	}
	return r
}

func (s *Server) flipbookPluginAction(c *gin.Context, action string) {
	if err := s.settings.Set(c, map[string]any{flipbookSetting: action == "activate"}); err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+template.URLQueryEscaper(err.Error()))
		return
	}
	s.purgeAll(c)
	if action == "activate" {
		c.Redirect(http.StatusFound, "/wp-admin/admin?page="+flipbookSlug+"&activated=1")
		return
	}
	c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
}

// flipbookMenu adds the Flipbook top-level menu under Media while the plugin is active.
func (s *Server) flipbookMenu(menu []*menuTop, u *store.User, screen string) []*menuTop {
	if !s.flipbookOn() || u == nil || !u.Can("upload_files", nil) {
		return menu
	}
	slug := "admin?page=" + flipbookSlug
	top := &menuTop{Slug: slug, Title: "Flipbook", ID: "toplevel_page_" + flipbookSlug, Icon: "dashicons-book-alt",
		Current: screen == slug || strings.HasPrefix(screen, slug+"&")}
	top.Sub = []*menuSub{
		{Slug: slug, Title: "All Flipbooks", First: true, Current: screen == slug},
		{Slug: slug + "&action=new", Title: "Add New", Current: screen == slug+"&action=new"},
	}
	at := len(menu)
	for i, m := range menu {
		if m.ID == "menu-media" {
			at = i + 1
			break
		}
	}
	out := append([]*menuTop{}, menu[:at]...)
	out = append(out, top)
	return append(out, menu[at:]...)
}

// registerFlipbook wires the assets (404 while the plugin is off); the full-screen page is flipbookRoute.
func (s *Server) registerFlipbook(r *gin.Engine) {
	s.static(r, flipbookAssets, "flipbook", s.flipbookOnly())
}

var flipbookPathRe = regexp.MustCompile(`^/flipbook/([1-9][0-9]{0,9})/?$`)

// flipbookRoute serves /flipbook/N from the site's fallback chain (before the site itself), so any
// other address under /flipbook/ (a page or post of that name) is still the site's.
func (s *Server) flipbookRoute(c *gin.Context) {
	m := flipbookPathRe.FindStringSubmatch(c.Request.URL.Path)
	if m == nil || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) || !s.flipbookOn() {
		c.Next()
		return
	}
	id, _ := strconv.Atoi(m[1])
	b, ok := s.flipbookGet(id)
	if !ok || b.PDFURL == "" {
		c.Next()
		return
	}
	s.flipbookFullPage(c, b)
	c.Abort()
}

// --- Front end -----------------------------------------------------------------------------------

var flipbookParaRe = regexp.MustCompile(`<p>\s*(\[` + flipbookTag + `\b[^\]]*\])\s*</p>`)

// flipbookContent expands [pursae_flipbook id="N"] in rendered content.
func (s *Server) flipbookContent(ctx context.Context, content string) string {
	if !s.flipbookOn() || !strings.Contains(content, "["+flipbookTag) {
		return content
	}
	content = flipbookParaRe.ReplaceAllString(content, "$1") // wpautop wraps a lone shortcode in <p>
	content = gfShortcodeQuotesFix(content, flipbookTag)
	var c *gin.Context
	if rc := renderFrom(ctx); rc != nil {
		c = rc.c
	}
	return wp.DoShortcode(content, []string{flipbookTag}, func(_ string, attrs map[string]string, _ string) string {
		id, _ := strconv.Atoi(firstNonEmpty(attrs["id"], attrs["0"]))
		b, ok := s.flipbookGet(id)
		if !ok || b.PDFURL == "" {
			return "<!-- pursae_flipbook: no flipbook with id " + html.EscapeString(strconv.Itoa(id)) + " -->"
		}
		if h, err := strconv.Atoi(attrs["height"]); err == nil && h >= 240 && h <= 2000 {
			b.Height = h
		}
		flipbookAllowPDF(c, b.PDFURL)
		return flipbookAssetsHTML() + flipbookEmbedHTML(b, false)
	})
}

// gfShortcodeQuotesFix straightens the curled quotes wptexturize left in tag's attributes.
func gfShortcodeQuotesFix(content, tag string) string {
	re := regexp.MustCompile(`\[` + regexp.QuoteMeta(tag) + `\s[^\]]*\]`)
	return re.ReplaceAllStringFunc(content, func(m string) string { return gfQuoteFix.Replace(m) })
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// flipbookAllowPDF lets the viewer fetch a PDF kept on another host (an offloaded upload) and run
// pdf.js's worker from this site (without worker-src, strict nonce policies would block it).
func flipbookAllowPDF(c *gin.Context, pdf string) {
	if c == nil {
		return
	}
	cspAllow(c, "worker-src", "'self'")
	if strings.HasPrefix(pdf, "/") {
		return
	}
	if src := cspRedirectSource(pdf); src != "" {
		cspAllow(c, "connect-src", src)
	}
}

// flipbookAssetsHTML loads the viewer. A module script runs once per page however many books it has.
func flipbookAssetsHTML() string {
	return `<link rel="stylesheet" href="` + flipbookAssets + `/flipbook.css?ver=` + flipbookVersion + `" media="all" />` +
		`<script type="module"` + cspNonceAttr() + ` src="` + flipbookAssets + `/flipbook.js?ver=` + flipbookVersion + `"></script>`
}

// flipbookEmbedHTML is the viewer's mount point; flipbook.js builds the toolbar and pages in it.
func flipbookEmbedHTML(b flipbook, full bool) string {
	e := html.EscapeString
	cls := "pfb alignwide" // block themes give a book the wide width, not the text column's
	if full {
		cls = "pfb pfb--full"
	}
	var sb strings.Builder
	sb.WriteString(`<div class="` + cls + `" data-pfb data-pdf="` + e(b.PDFURL) + `" data-title="` + e(b.Title) + `"`)
	sb.WriteString(` data-worker="` + flipbookAssets + `/vendor/pdf.worker.min.mjs?ver=` + flipbookVersion + `"`)
	if b.Download {
		sb.WriteString(` data-download="1"`)
	}
	if b.Share {
		sb.WriteString(` data-share="` + e(flipbookPageURL(b.ID)) + `"`)
	}
	if !full {
		sb.WriteString(` data-full="` + e(flipbookPageURL(b.ID)) + `"`)
	}
	sb.WriteString(` style="--pfb-bg:` + b.Background + `;`)
	if !full {
		sb.WriteString(`--pfb-height:` + strconv.Itoa(b.Height) + `px;`)
	}
	sb.WriteString(`">`)
	// The loading screen: a small open book whose right page keeps turning, the title under it. It
	// covers the stage until the first spread is drawn, then fades out (flipbook.js).
	sb.WriteString(`<div class="pfb-loading" role="status"><div class="pfb-loader" aria-hidden="true"><i></i><i></i><i></i></div>` +
		`<p class="pfb-loading-text">` + e(b.Title) + `</p></div>`)
	sb.WriteString(`<noscript><a href="` + e(b.PDFURL) + `">` + e(b.Title) + ` (PDF)</a></noscript></div>`)
	return sb.String()
}

// flipbookFullPage serves /flipbook/:id, the book alone on a full-screen page (the share link).
func (s *Server) flipbookFullPage(c *gin.Context, b flipbook) {
	flipbookAllowPDF(c, b.PDFURL)
	e := html.EscapeString
	site := s.settings.String("blogname")
	title := b.Title
	if site != "" {
		title += " – " + site
	}
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="` + e(s.htmlLang()) + `"><head><meta charset="utf-8">`)
	sb.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">`)
	sb.WriteString(`<title>` + e(title) + `</title>`)
	sb.WriteString(`<meta property="og:title" content="` + e(b.Title) + `"><meta property="og:type" content="article">`)
	sb.WriteString(`<meta name="robots" content="noindex">`)
	if c.Query("embed") == "1" {
		sb.WriteString(`<style>html,body{background:transparent}</style>`)
	}
	sb.WriteString(flipbookAssetsHTML())
	sb.WriteString(`</head><body class="pfb-page">`)
	sb.WriteString(flipbookEmbedHTML(b, true))
	sb.WriteString(`</body></html>`)
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(sb.String()))
}

// htmlLang is the <html lang> of front pages, from the site language (en-US by default).
func (s *Server) htmlLang() string {
	l := s.settings.String("WPLANG")
	if l == "" {
		return "en-US"
	}
	return strings.ReplaceAll(l, "_", "-")
}

// flipbookAbsURL makes a stored URL absolute for the share box.
func flipbookAbsURL(home, u string) string {
	if strings.HasPrefix(u, "/") {
		if b, err := url.Parse(home); err == nil {
			b.Path, b.RawQuery, b.Fragment = u, "", ""
			return b.String()
		}
	}
	return u
}
