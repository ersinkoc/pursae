package server

import (
	"bytes"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/gocdn"
)

// URL rewriting. The front end's HTML, feeds and the REST API's JSON pass through gocdnRewrite: the
// URLs of offloaded uploads (/wp-content/uploads/…, relative or on the site's own host, in src,
// srcset, href, CSS url() and JSON alike) become their bucket / CDN URLs, content images optionally
// go through the image service (WebP), and theme, plugin and core assets move to the assets CDN.
// wp-admin, the login screen, the REST API's edit context (what the editors load and save back),
// static files and anything that is not HTML/XML/JSON are never touched. The page cache stores the
// rewritten page, and every settings change purges it.

// gocdnRewrite is the gin middleware (registered in routes()).
func (s *Server) gocdnRewrite() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.gocdnOn() || s.sandbox != nil || !gocdnRewritable(c.Request) {
			c.Next()
			return
		}
		st := s.gocdnLoad()
		media := st.ServeFromCDN && gocdnPublicBase(st) != ""
		assets := st.Assets && st.AssetsURL != "" && !strings.HasPrefix(c.Request.URL.Path, "/wp-json")
		if !media && !assets && !st.ImgRewrite {
			c.Next()
			return
		}
		orig := c.Writer
		w := &gocdnWriter{ResponseWriter: orig}
		c.Writer = w
		c.Next()
		c.Writer = orig
		if !w.capture {
			return
		}
		body := w.buf.Bytes()
		if len(body) > 0 {
			body = s.gocdnRewriteBody(c, st, body, media, assets, strings.Contains(orig.Header().Get("Content-Type"), "html"))
		}
		if orig.Header().Get("Content-Length") != "" {
			orig.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		orig.WriteHeader(w.status())
		orig.WriteHeaderNow()
		if len(body) > 0 {
			_, _ = orig.Write(body)
		}
	}
}

// gocdnRewritable reports whether a request's response may be rewritten.
func gocdnRewritable(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		return false
	}
	p := r.URL.Path
	for _, pre := range []string{"/wp-admin", "/wp-login", "/wp-content/", "/wp-includes/", gocdnImgPath, "/wp-cron", "/xmlrpc"} {
		if strings.HasPrefix(p, pre) {
			return false
		}
	}
	if strings.HasPrefix(p, "/wp-json") || r.URL.Query().Has("rest_route") {
		return r.Method == http.MethodGet && r.URL.Query().Get("context") != "edit"
	}
	return r.Method != http.MethodPost
}

// gocdnWriter buffers HTML, XML and JSON responses; everything else streams through.
type gocdnWriter struct {
	gin.ResponseWriter
	buf      bytes.Buffer
	code     int
	decided  bool
	capture  bool
	captured int
}

func (w *gocdnWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	h := w.ResponseWriter.Header()
	ct := strings.ToLower(h.Get("Content-Type"))
	if h.Get("Content-Encoding") != "" {
		return
	}
	for _, t := range []string{"text/html", "application/json", "+json", "application/xml", "text/xml", "+xml"} {
		if strings.Contains(ct, t) {
			w.capture = true
			return
		}
	}
}

func (w *gocdnWriter) status() int {
	if w.code != 0 {
		return w.code
	}
	return w.ResponseWriter.Status()
}

func (w *gocdnWriter) WriteHeader(code int) {
	if w.decided && !w.capture {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if code > 0 {
		w.code = code
	}
}

func (w *gocdnWriter) WriteHeaderNow() {
	w.decide()
	if !w.capture {
		if w.code != 0 {
			w.ResponseWriter.WriteHeader(w.code)
		}
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *gocdnWriter) Write(b []byte) (int, error) {
	w.decide()
	if w.capture {
		w.captured += len(b)
		return w.buf.Write(b)
	}
	if w.code != 0 {
		w.ResponseWriter.WriteHeader(w.code)
		w.code = 0
	}
	return w.ResponseWriter.Write(b)
}

func (w *gocdnWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *gocdnWriter) Status() int { return w.status() }

func (w *gocdnWriter) Written() bool {
	if w.capture {
		return w.captured > 0
	}
	return w.ResponseWriter.Written()
}

func (w *gocdnWriter) Size() int {
	if w.capture {
		return w.captured
	}
	return w.ResponseWriter.Size()
}

func (w *gocdnWriter) Flush() {
	w.decide()
	if !w.capture {
		w.ResponseWriter.Flush()
	}
}

// gocdnUploadsURL finds upload URLs: an optional scheme and host, then /wp-content/uploads/<path>.
var gocdnUploadsURL = regexp.MustCompile(`(?:https?:)?(?://([A-Za-z0-9.\-]+(?::\d+)?))?/wp-content/uploads/([^\s"'<>()\\?#,&;]+)`)

// gocdnHosts are the host names that count as this site.
func (s *Server) gocdnHosts(c *gin.Context) map[string]bool {
	hosts := map[string]bool{}
	for _, raw := range []string{s.settings.String("home"), s.settings.String("siteurl"), s.cfg.SiteURL, localOrigin(c, s.settings.String("home"))} {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			hosts[strings.ToLower(u.Host)] = true
		}
	}
	if c.Request.Host != "" && isLocalHost(strings.Split(c.Request.Host, ":")[0]) {
		hosts[strings.ToLower(c.Request.Host)] = true
	}
	return hosts
}

// gocdnBoundary reports whether a match starts a URL (not the middle of another path or word).
func gocdnBoundary(body []byte, i int) bool {
	if i == 0 {
		return true
	}
	switch body[i-1] {
	case '"', '\'', '=', ' ', '\t', '\n', '\r', '(', ',', '>', ';':
		return true
	}
	return false
}

var gocdnImageExt = map[string]bool{".jpg": true, ".jpeg": true, ".jpe": true, ".png": true, ".gif": true, ".webp": true}

// gocdnRewriteBody rewrites one response body.
func (s *Server) gocdnRewriteBody(c *gin.Context, st gocdnSettings, body []byte, media, assets, isHTML bool) []byte {
	hosts := s.gocdnHosts(c)
	out := body
	if media || st.ImgRewrite {
		out = s.gocdnRewriteMedia(c, st, out, hosts, media)
	}
	if assets && isHTML {
		out = gocdnRewriteAssets(st, out, hosts)
		if origin := gocdnOrigin(st.AssetsURL); origin != "" {
			for _, d := range []string{"script-src", "style-src", "font-src", "img-src", "media-src", "connect-src"} {
				cspAllow(c, d, origin)
			}
		}
	}
	if media && isHTML {
		if origin := gocdnOrigin(gocdnPublicBase(st)); origin != "" {
			cspAllow(c, "img-src", origin)
			cspAllow(c, "media-src", origin)
		}
	}
	return out
}

func gocdnOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (s *Server) gocdnRewriteMedia(c *gin.Context, st gocdnSettings, body []byte, hosts map[string]bool, media bool) []byte {
	matches := gocdnUploadsURL.FindAllSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return body
	}
	type hit struct {
		start, end int
		rel        string
	}
	var hits []hit
	var paths []string
	seen := map[string]bool{}
	for _, m := range matches {
		if !gocdnBoundary(body, m[0]) {
			continue
		}
		if m[2] >= 0 && !hosts[strings.ToLower(string(body[m[2]:m[3]]))] {
			continue // another site's uploads
		}
		rel, err := url.PathUnescape(string(body[m[4]:m[5]]))
		if err != nil {
			continue
		}
		hits = append(hits, hit{m[0], m[5], rel})
		if !seen[rel] {
			seen[rel] = true
			paths = append(paths, rel)
		}
	}
	if len(hits) == 0 {
		return body
	}
	items := map[string]*gocdnItem{}
	if media {
		var err error
		if items, err = s.gocdnItemsByPaths(c, paths); err != nil {
			return body
		}
	}
	var key []byte
	if st.ImgRewrite {
		key = s.gocdnImgKey(st)
	}
	home := strings.TrimRight(localOrigin(c, s.settings.String("home")), "/")
	if home == "" {
		home = strings.TrimRight(s.cfg.SiteURL, "/")
	}
	var b bytes.Buffer
	last := 0
	for _, h := range hits {
		var repl string
		hasQuery := h.end < len(body) && body[h.end] == '?'
		if st.ImgRewrite && !hasQuery && gocdnImageExt[strings.ToLower(path.Ext(h.rel))] {
			p := gocdn.ImageParams{Q: st.ImgRewriteQ, Format: "auto"}
			repl = home + gocdnSignedPath(key, h.rel, p, st.ImgSign)
		} else if it := items[h.rel]; it != nil {
			repl = gocdnItemURL(st, it)
		}
		if repl == "" {
			continue
		}
		b.Write(body[last:h.start])
		b.WriteString(repl)
		last = h.end
	}
	if last == 0 {
		return body
	}
	b.Write(body[last:])
	return b.Bytes()
}

// gocdnAssetDirs are the asset folders, as path prefixes.
func gocdnAssetDirs(st gocdnSettings) []string {
	var out []string
	for _, l := range lines(st.AssetsDirs) {
		l = "/" + strings.Trim(l, "/") + "/"
		switch {
		case l == "//", strings.HasPrefix(l, "/wp-content/uploads/"): // media has its own rewriting
			continue
		case strings.HasPrefix(l, "/wp-admin/") && !strings.HasPrefix(l, "/wp-admin/assets/") && !strings.HasPrefix(l, "/wp-admin/css/") &&
			!strings.HasPrefix(l, "/wp-admin/js/") && !strings.HasPrefix(l, "/wp-admin/images/"):
			continue // only wp-admin's static folders
		}
		out = append(out, l)
	}
	return out
}

func gocdnAssetExts(st gocdnSettings) map[string]bool {
	out := map[string]bool{}
	for _, e := range strings.FieldsFunc(st.AssetsExt, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		out["."+strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e)), ".")] = true
	}
	return out
}

// gocdnAssetRegexps caches the asset URL pattern of a folder list.
var gocdnAssetRegexps sync.Map // string → *regexp.Regexp

func gocdnAssetRegexp(dirs []string) *regexp.Regexp {
	k := strings.Join(dirs, "\n")
	if v, ok := gocdnAssetRegexps.Load(k); ok {
		return v.(*regexp.Regexp)
	}
	alts := make([]string, len(dirs))
	for i, d := range dirs {
		alts[i] = regexp.QuoteMeta(d)
	}
	re := regexp.MustCompile(`(?:https?:)?(?://([A-Za-z0-9.\-]+(?::\d+)?))?((?:` + strings.Join(alts, "|") + `)[A-Za-z0-9._~%!$*+=:@/\-]*)`)
	gocdnAssetRegexps.Store(k, re)
	return re
}

// gocdnRewriteAssets moves asset URLs of the configured folders and extensions to the assets CDN.
func gocdnRewriteAssets(st gocdnSettings, body []byte, hosts map[string]bool) []byte {
	dirs := gocdnAssetDirs(st)
	exts := gocdnAssetExts(st)
	excludes := lines(st.AssetsExclude)
	if len(dirs) == 0 {
		return body
	}
	var b bytes.Buffer
	last := 0
	for _, m := range gocdnAssetRegexp(dirs).FindAllSubmatchIndex(body, -1) {
		if !gocdnBoundary(body, m[0]) {
			continue
		}
		if m[2] >= 0 && !hosts[strings.ToLower(string(body[m[2]:m[3]]))] {
			continue
		}
		if m[2] < 0 && m[0] != m[4] { // a scheme with no host
			continue
		}
		p := string(body[m[4]:m[5]])
		if !exts[strings.ToLower(path.Ext(p))] {
			continue
		}
		excluded := false
		for _, x := range excludes {
			if strings.Contains(p, x) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		b.Write(body[last:m[0]])
		b.WriteString(st.AssetsURL + p)
		last = m[5]
	}
	if last == 0 {
		return body
	}
	b.Write(body[last:])
	return b.Bytes()
}
