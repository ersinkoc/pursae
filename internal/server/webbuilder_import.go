package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Exact HTML import (1:1): the page is drawn by a real browser, never guessed from the source.
//
//  1. MCP web_builder_import_html stores the HTML in an import job and answers with its import URL
//     (/web-builder-import/<token>, a one-time token, two hours).
//  2. Whoever has a browser opens it — the agent's own browser tool, or the user. The page draws the
//     HTML in two sandboxed frames (allow-scripts only: an opaque origin with no access to this site,
//     its cookies or the page around it) at 1440 and 390 px, runs webbuilder.ExtractorJS in each and
//     posts both results back to the same URL.
//  3. MCP web_builder_import_finish (same user) converts them (webbuilder.ConvertExtract) into free-form
//     sections and publishes the page through the ordinary build path, with the page's title,
//     description, robots and Open Graph fields mapped to the page and Pursae SEO.
//
// Jobs live in memory: a restart or a second instance loses them, and the agent starts again.

const (
	wbImportTTL      = 2 * time.Hour
	wbImportMaxJobs  = 200
	wbImportUserJobs = 5        // open jobs of one user
	wbImportMaxBytes = 64 << 20 // the HTML and the extractions of every open job, in memory
	wbImportMaxHTML  = 2 << 20
	wbImportMaxData  = 12 << 20
	wbImportDeskW    = 1440
	wbImportMobileW  = 390
	wbImportRoute    = "/web-builder-import/"
	wbImportDoneText = "Pursae import: done"
)

type wbImportJob struct {
	token   string
	userID  int64
	html    string
	args    map[string]any
	created time.Time
	desk    *webbuilder.Extract
	mobile  *webbuilder.Extract
	done    chan struct{}
	used    bool
	data    int // bytes of the browser's extraction (wbImportReceive)
}

// size is what the job holds in memory (the caller holds the store's lock).
func (j *wbImportJob) size() int { return len(j.html) + j.data }

type wbImportStore struct {
	mu   sync.Mutex
	jobs map[string]*wbImportJob
}

var wbImports = &wbImportStore{jobs: map[string]*wbImportJob{}}

func (st *wbImportStore) create(userID int64, html string, args map[string]any) (*wbImportJob, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	mine, total := 0, len(html)
	for k, j := range st.jobs {
		if now.Sub(j.created) > wbImportTTL {
			delete(st.jobs, k)
			continue
		}
		if j.userID == userID {
			mine++
		}
		total += j.size()
	}
	if mine >= wbImportUserJobs {
		return nil, mcpErrf("you have %d HTML imports waiting for a browser; finish them (web_builder_import_finish) or wait until they expire (two hours)", mine)
	}
	if len(st.jobs) >= wbImportMaxJobs || total > wbImportMaxBytes {
		return nil, mcpErrf("too many HTML imports are waiting; finish or wait for the open ones")
	}
	j := &wbImportJob{token: hex.EncodeToString(b), userID: userID, html: html, args: args, created: now, done: make(chan struct{})}
	st.jobs[j.token] = j
	return j, nil
}

func (st *wbImportStore) get(token string) *wbImportJob {
	st.mu.Lock()
	defer st.mu.Unlock()
	j := st.jobs[token]
	if j == nil || time.Since(j.created) > wbImportTTL {
		return nil
	}
	return j
}

func (st *wbImportStore) remove(token string) {
	st.mu.Lock()
	delete(st.jobs, token)
	st.mu.Unlock()
}

func (s *Server) wbRegisterImport(r *gin.Engine) {
	r.GET(wbImportRoute+":token", s.wbOnly(), s.wbImportPage)
	r.POST(wbImportRoute+":token", s.wbOnly(), s.wbImportReceive)
}

var wbImportTokenRE = func(t string) bool {
	if len(t) != 48 {
		return false
	}
	_, err := hex.DecodeString(t)
	return err == nil
}

// wbImportPage serves the page that draws the HTML in the browser and sends the extraction back.
func (s *Server) wbImportPage(c *gin.Context) {
	token := c.Param("token")
	j := (*wbImportJob)(nil)
	if wbImportTokenRE(token) {
		j = wbImports.get(token)
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Header("Referrer-Policy", "no-referrer")
	// The design's frames inherit this page's policy (srcdoc), and a design loads what it likes: CDN
	// scripts (Tailwind), web fonts, remote images. That is safe because the frames are sandboxed
	// (scripts only, opaque origin): they reach neither this page nor the site's cookies. The page
	// itself only runs its own inline script and posts to its own URL.
	c.Header("Content-Security-Policy", "default-src * data: blob:; script-src * 'unsafe-inline' 'unsafe-eval' data: blob:; "+
		"style-src * 'unsafe-inline' data:; img-src * data: blob:; font-src * data:; connect-src *; frame-src 'self' about: data: blob:; "+
		"frame-ancestors 'none'; base-uri http: https:; form-action 'none'; object-src 'none'")
	c.Header("X-Frame-Options", "DENY")
	if j == nil {
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte(`<!doctype html><meta charset="utf-8"><title>Pursae import: expired</title><p>This import link is unknown or has expired. Ask the AI agent to start the import again.</p>`))
		return
	}
	// The HTML and the extractor travel as JSON inside a script tag; json.Marshal escapes <, > and &.
	payload, _ := json.Marshal(map[string]any{"html": j.html, "extractor": webbuilder.ExtractorJS, "widths": []int{wbImportDeskW, wbImportMobileW}})
	page := strings.Replace(wbImportPageHTML, "{{DATA}}", string(payload), 1)
	page = strings.Replace(page, "{{DONE}}", template.JSEscapeString(wbImportDoneText), 1)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

// wbImportReceive stores the browser's extraction (the token is the credential).
func (s *Server) wbImportReceive(c *gin.Context) {
	token := c.Param("token")
	if !wbImportTokenRE(token) {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown import"})
		return
	}
	j := wbImports.get(token)
	if j == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown or expired import"})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, wbImportMaxData))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "the page is too large to import"})
		return
	}
	var body struct {
		Desktop *webbuilder.Extract `json:"desktop"`
		Mobile  *webbuilder.Extract `json:"mobile"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Desktop == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid extraction"})
		return
	}
	wbImports.mu.Lock()
	total := len(raw) - j.data
	for _, o := range wbImports.jobs {
		total += o.size()
	}
	if total > wbImportMaxBytes {
		wbImports.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "the site holds too many imports right now; try again later"})
		return
	}
	first := j.desk == nil
	j.desk, j.mobile, j.data = body.Desktop, body.Mobile, len(raw)
	wbImports.mu.Unlock()
	if first {
		close(j.done)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// mcpWBImportHTMLExact starts an exact import and answers with the URL to open in a browser.
func (s *Server) mcpWBImportHTMLExact(c *gin.Context, args map[string]any) (any, error) {
	html, _ := args["html"].(string)
	if len(html) > wbImportMaxHTML {
		return nil, mcpErrf("html is larger than %d MB", wbImportMaxHTML>>20)
	}
	if argInt(args, "post_id") <= 0 && argStr(args, "title") == "" {
		return nil, mcpErrf("give post_id (an existing page) or title (a new page)")
	}
	// base_url: where the design's relative CSS, scripts, fonts and images live while it is measured.
	if b := argStr(args, "base_url"); b != "" {
		if !strings.HasPrefix(b, "http://") && !strings.HasPrefix(b, "https://") || strings.ContainsAny(b, "\"'<> ") {
			return nil, mcpErrf("base_url must be an http(s) URL")
		}
		if !strings.HasSuffix(b, "/") {
			b += "/"
		}
		tag := `<base href="` + b + `">`
		if i := strings.Index(strings.ToLower(html), "<head>"); i >= 0 {
			html = html[:i+6] + tag + html[i+6:]
		} else {
			html = tag + html
		}
	}
	u := restCurrent(c)
	keep := map[string]any{}
	for _, k := range []string{"post_id", "title", "post_type", "slug", "parent", "mode", "layout", "status", "front_page", "site", "font_map"} {
		if v, ok := args[k]; ok {
			keep[k] = v
		}
	}
	j, err := wbImports.create(u.ID, html, keep)
	if err != nil {
		return nil, err
	}
	url := s.links(c).HomeURL(wbImportRoute + j.token)
	return map[string]any{
		"job":        j.token,
		"import_url": url,
		"status":     "waiting_for_browser",
		"next":       "Open import_url in a web browser (your browser tool, or give the link to the user to open). The page draws the design at desktop and phone width and sends what it measured back; its title becomes \"" + wbImportDoneText + "\" when it is done (about 5–20 seconds). Then call web_builder_import_finish {job}. The link works for two hours.",
	}, nil
}

// mcpWBImportFinish converts a browser extraction and publishes the page.
func mcpWBImportFinish(s *Server, c *gin.Context, args map[string]any) (any, error) {
	if err := s.mcpNeedBuilder(); err != nil {
		return nil, err
	}
	token := argStr(args, "job")
	j := (*wbImportJob)(nil)
	if wbImportTokenRE(token) {
		j = wbImports.get(token)
	}
	if j == nil {
		return nil, mcpErrf("unknown or expired import job; start again with web_builder_import_html")
	}
	if u := restCurrent(c); u == nil || u.ID != j.userID {
		return nil, mcpErrf("this import job belongs to another user")
	}
	wait := argInt(args, "wait")
	if wait <= 0 {
		wait = 45
	}
	if wait > 110 {
		wait = 110
	}
	select {
	case <-j.done:
	case <-time.After(time.Duration(wait) * time.Second):
		return map[string]any{"job": token, "status": "waiting_for_browser", "import_url": s.links(c).HomeURL(wbImportRoute + token),
			"next": "The import page has not reported yet. Make sure import_url is open in a browser and call web_builder_import_finish again."}, nil
	case <-c.Request.Context().Done():
		return nil, mcpErrf("cancelled")
	}
	wbImports.mu.Lock()
	desk, mobile := j.desk, j.mobile
	wbImports.mu.Unlock()
	fm := map[string]string{}
	for k, v := range argObj(j.args, "font_map") {
		if sv, ok := v.(string); ok {
			fm[k] = sv
		}
	}
	for k, v := range argObj(args, "font_map") {
		if sv, ok := v.(string); ok {
			fm[k] = sv
		}
	}
	res, err := webbuilder.ConvertExtract(desk, mobile, webbuilder.ExtractOptions{FontMap: fm})
	if err != nil {
		return nil, mcpErrf("%v", err)
	}
	build := map[string]any{}
	for k, v := range j.args {
		if k != "font_map" {
			build[k] = v
		}
	}
	if override, ok := args["status"]; ok {
		build["status"] = override
	}
	specs := make([]any, len(res.Sections))
	for i, sp := range res.Sections {
		specs[i] = sp
	}
	build["sections"] = specs
	if build["layout"] == nil {
		build["layout"] = "canvas" // the design brings its own header and footer
	}
	// Page settings from the page's own metadata.
	page := map[string]any{}
	if t := strings.TrimSpace(argStr(build, "title")); t == "" && res.SEO.Title != "" && argInt(build, "post_id") == 0 {
		build["title"] = res.SEO.Title
	}
	if d := strings.TrimSpace(res.SEO.Description); d != "" {
		page["description"] = truncRunes(d, 300)
	}
	if strings.Contains(strings.ToLower(res.SEO.Robots), "noindex") {
		page["indexable"] = false
	}
	if len(page) > 0 {
		build["page"] = page
	}
	site, _ := build["site"].(map[string]any)
	if site == nil {
		site = map[string]any{}
	}
	if site["brand"] == nil && res.Brand != "" {
		site["brand"] = res.Brand
	}
	build["site"] = site
	out, err := mcpWBBuildPage(s, c, build)
	if err != nil {
		return nil, err
	}
	m, _ := out.(map[string]any)
	if m == nil {
		return out, nil
	}
	warnings := asStrings(m["warnings"])
	warnings = append(warnings, res.Warnings...)
	warnings = append(warnings, wbImportSEOChecks(res.SEO)...)
	for _, f := range res.Fonts {
		if _, ok := webbuilder.LookupFont(f); !ok && !wbWebSafeFonts[strings.ToLower(f)] {
			warnings = append(warnings, fmt.Sprintf("font %q is not in the builder's font library; a similar system font is used", f))
		}
	}
	// Social and search snippets in Pursae SEO (when it is on).
	if id, ok := m["post_id"].(int64); ok {
		if fields, warn := s.wbImportSEO(c, id, res.SEO); warn != "" {
			warnings = append(warnings, warn)
		} else if len(fields) > 0 {
			m["seo"] = fields
		}
	}
	if len(warnings) > 0 {
		m["warnings"] = warnings
	}
	m["nodes"] = res.Nodes
	m["method"] = "exact (measured in a browser)"
	wbImports.remove(token)
	return m, nil
}

// wbWebSafeFonts are installed on practically every visitor's system, so the editor's fallback stack
// ('<name>', Inter, sans-serif) still shows them.
var wbWebSafeFonts = map[string]bool{"arial": true, "helvetica": true, "helvetica neue": true, "georgia": true, "times new roman": true, "times": true,
	"verdana": true, "tahoma": true, "trebuchet ms": true, "courier new": true, "segoe ui": true, "roboto": true}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// wbImportSEOChecks reports what search engines would miss on the imported page.
func wbImportSEOChecks(seo webbuilder.ExtractSEO) []string {
	var w []string
	switch len(seo.H1) {
	case 0:
		w = append(w, "SEO: the page has no h1 heading")
	case 1:
	default:
		w = append(w, fmt.Sprintf("SEO: the page has %d h1 headings; keep one", len(seo.H1)))
	}
	prev := 0
	for _, h := range seo.Headings {
		lvl := int(h[1] - '0')
		if prev > 0 && lvl > prev+1 {
			w = append(w, fmt.Sprintf("SEO: heading levels skip from h%d to h%d", prev, lvl))
			break
		}
		prev = lvl
	}
	if seo.Description == "" {
		w = append(w, "SEO: the page has no meta description; set one with seo_update_meta")
	}
	if seo.ImagesWithoutAlt > 0 {
		w = append(w, fmt.Sprintf("SEO: %d image(s) have no alt text", seo.ImagesWithoutAlt))
	}
	return w
}

// wbImportPageHTML is the import page. The design is drawn in sandboxed frames (scripts only, opaque
// origin); the extractor in each frame posts its result here, and this page posts both to the server.
const wbImportPageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow"><title>Pursae import: reading the design…</title>
<style>
body{margin:0;font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#f0f0f1;color:#1d2327}
.bar{padding:16px 24px;background:#fff;border-bottom:1px solid #dcdcde}
.bar strong{font-size:16px}.st{margin-top:4px;color:#50575e}.ok{color:#00a32a}.err{color:#d63638}
.stage{display:flex;gap:24px;padding:24px;align-items:flex-start;overflow:hidden}
.fw{flex:none;overflow:hidden;background:#fff;box-shadow:0 1px 3px rgba(0,0,0,.15)}
.fw iframe{border:0;transform-origin:0 0;display:block}
.cap{font-size:12px;color:#50575e;margin:0 0 6px}
</style></head><body>
<div class="bar"><strong>Pursae Web Builder &middot; HTML import</strong><div class="st" id="st">Drawing the design at desktop and phone width&hellip;</div></div>
<div class="stage" id="stage"></div>
<script id="pursae-import-data" type="application/json">{{DATA}}</script>
<script>
(function(){
  var D = JSON.parse(document.getElementById('pursae-import-data').textContent)
  var st = document.getElementById('st'), stage = document.getElementById('stage')
  var results = {}, frames = {}, sent = false
  function status(t, cls){ st.textContent = t; st.className = 'st ' + (cls || '') }
  function doc(){
    var h = D.html
    var inject = '<scr' + 'ipt>' + D.extractor.replace(/<\/script/gi, '<\\/script') + '</scr' + 'ipt>'
    if (/<\/body>/i.test(h)) return h.replace(/<\/body>(?![\s\S]*<\/body>)/i, inject + '</body>')
    return h + inject
  }
  var src = doc()
  D.widths.forEach(function(w){
    var scale = w > 400 ? 0.42 : 0.6
    var box = document.createElement('div')
    var cap = document.createElement('p'); cap.className = 'cap'; cap.textContent = w + ' px'
    var fw = document.createElement('div'); fw.className = 'fw'; fw.style.width = Math.round(w * scale) + 'px'; fw.style.height = Math.round(900 * scale) + 'px'
    var f = document.createElement('iframe')
    f.setAttribute('sandbox', 'allow-scripts')
    f.setAttribute('referrerpolicy', 'no-referrer')
    f.setAttribute('loading', 'eager')
    f.style.width = w + 'px'; f.style.height = '900px'; f.style.transform = 'scale(' + scale + ')'
    f.srcdoc = src
    fw.appendChild(f); box.appendChild(cap); box.appendChild(fw); stage.appendChild(box)
    frames[w] = f
  })
  window.addEventListener('message', function(e){
    if (!e.data || e.data.pursaeExtract !== 1) return
    var w = 0
    for (var k in frames) if (frames[k].contentWindow === e.source) w = +k
    if (!w) return
    results[w] = e.data.data
    if (results[D.widths[0]] && results[D.widths[1]] && !sent) send()
  })
  function send(){
    sent = true
    status('Sending the measurements to the site…')
    fetch(location.pathname, { method: 'POST', headers: { 'Content-Type': 'application/json' }, credentials: 'omit',
      body: JSON.stringify({ desktop: results[D.widths[0]], mobile: results[D.widths[1]] }) })
      .then(function(r){ return r.json().then(function(j){ return { ok: r.ok, j: j } }) })
      .then(function(x){
        if (!x.ok) throw new Error(x.j && x.j.error || 'upload failed')
        var n = results[D.widths[0]] && results[D.widths[0]].nodes
        status('Done: ' + n + ' elements measured. The AI agent can finish the import now; you can close this tab.', 'ok')
        document.title = '{{DONE}}'
      })
      .catch(function(err){ status('Could not send the result: ' + err.message, 'err'); document.title = 'Pursae import: failed' })
  }
  setTimeout(function(){ if (!sent) { status('The design is taking long to load (slow images or fonts?). Still waiting…') } }, 30000)
})()
</script></body></html>`
