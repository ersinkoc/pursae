package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Google Fonts served from this site ("Load Google Fonts Locally", a port of Elementor's
// Core\Files\Fonts\Google_Font, on by default). Visitors' browsers never contact
// Google: the server downloads a family's stylesheet from fonts.googleapis.com and its woff2 files
// from fonts.gstatic.com once, stores them under uploads/web-builder/google-fonts/{css,fonts}/,
// rewrites the URLs and the page links the local stylesheet (font-display: swap, plus a preload of
// the regular latin face). With the option off, Google Fonts are not loaded at all.
//
// Other code calls s.gweGoogleFontsHead(c, families) to get the <link> tags for a list of families
// (only families from the Google Fonts catalogue are handled; others are ignored).

const (
	gweFontsBase         = gweUploadsDir + "/google-fonts" // under the uploads dir
	gweFontsUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36"
	gweFontsWait         = 4 * time.Second // how long a page waits for a first download
	gweFontsDownloadTime = 3 * time.Minute
	gweFontsFetchTime    = 60 * time.Second // one request
)

var gweFontWeights = "100,100italic,200,200italic,300,300italic,400,400italic,500,500italic,600,600italic,700,700italic,800,800italic,900,900italic"

// gweFontFetch downloads one URL from Google Fonts. Tests replace it; the default refuses every
// URL but https:// on fonts.googleapis.com and fonts.gstatic.com, never follows redirects and caps
// the body at 8 MB.
var gweFontFetch = func(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("Invalid API URL.")
	}
	switch strings.ToLower(u.Hostname()) {
	case "fonts.googleapis.com", "fonts.gstatic.com":
	default:
		return nil, errors.New("API host not allowed.")
	}
	ctx, cancel := context.WithTimeout(ctx, gweFontsFetchTime)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", gweFontsUserAgent)
	client := &http.Client{Timeout: gweFontsFetchTime, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return b, nil
}

var gweGoogleFontsOnce = sync.OnceValue(func() map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(gweGoogleFontList, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if strings.HasPrefix(line, "*") {
			m[line[1:]] = "earlyaccess"
		} else {
			m[line] = "googlefonts"
		}
	}
	return m
})

// gweGoogleFontType is "googlefonts", "earlyaccess" or "" (not a Google font).
func gweGoogleFontType(family string) string { return gweGoogleFontsOnce()[strings.TrimSpace(family)] }

// gweFontKey is sanitize_key( family ).
func gweFontKey(family string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(family) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// gweGoogleFontsEnabled is the "Load Google Fonts Locally" option (default on).
func (s *Server) gweGoogleFontsEnabled() bool {
	return s.settings.String("gwe_local_google_fonts") != "0"
}

func (s *Server) gweFontsDir(sub string) string {
	return filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(gweFontsBase), sub)
}

// gweFontJobs dedupes concurrent downloads per uploads dir + family.
var gweFontJobs sync.Map // string -> chan struct{} (closed when done)

// gweEnsureGoogleFont downloads a family once; it returns true when the local stylesheet exists.
// It waits at most wait for a running download (which carries on in the background).
func (s *Server) gweEnsureGoogleFont(family string, wait time.Duration) bool {
	typ := gweGoogleFontType(family)
	key := gweFontKey(family)
	if typ == "" || key == "" {
		return false
	}
	cssPath := filepath.Join(s.gweFontsDir("css"), key+".css")
	if _, err := os.Stat(cssPath); err == nil {
		return true
	}
	job := s.cfg.UploadsDir + "\x00" + key
	if t, ok := gweFontFailures.Load(job); ok && time.Since(t.(time.Time)) < 10*time.Minute {
		return false // a recent failure: don't retry on every page view
	}
	ch := make(chan struct{})
	if prev, loaded := gweFontJobs.LoadOrStore(job, ch); loaded {
		ch = prev.(chan struct{})
	} else {
		go func() {
			defer func() {
				close(ch)
				gweFontJobs.Delete(job)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), gweFontsDownloadTime)
			defer cancel()
			if err := s.gweDownloadGoogleFont(ctx, family, typ); err != nil {
				gweFontFailures.Store(job, time.Now())
			}
		}()
	}
	select {
	case <-ch:
	case <-time.After(wait):
		return false
	}
	_, err := os.Stat(cssPath)
	return err == nil
}

var gweFontFailures sync.Map

// gweGoogleFontURL is the stylesheet Google serves for a family (get_stable_google_fonts_url).
func gweGoogleFontURL(family, typ string) string {
	if typ == "earlyaccess" {
		return "https://fonts.googleapis.com/earlyaccess/" + strings.ToLower(strings.ReplaceAll(family, " ", "")) + ".css"
	}
	return "https://fonts.googleapis.com/css?family=" + strings.ReplaceAll(family, " ", "+") + ":" + gweFontWeights + "&display=swap"
}

var (
	gweCSSURLRe      = regexp.MustCompile(`url\(([^)]+)\)`)
	gweFontFaceRe    = regexp.MustCompile(`(?s)@font-face\s*\{[^}]*\}`)
	gweFontDisplayRe = regexp.MustCompile(`font-display\s*:[^;}]*`)
)

// gweDownloadGoogleFont fetches a family's CSS and font files and writes the local stylesheet.
func (s *Server) gweDownloadGoogleFont(ctx context.Context, family, typ string) error {
	key := gweFontKey(family)
	css, err := gweFontFetch(ctx, gweGoogleFontURL(family, typ))
	if err != nil {
		return err
	}
	text := string(css)
	if !strings.Contains(text, "@font-face") {
		return errors.New("no @font-face in the Google Fonts stylesheet")
	}
	fontsDir, cssDir := s.gweFontsDir("fonts"), s.gweFontsDir("css")
	if err := os.MkdirAll(fontsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(cssDir, 0o755); err != nil {
		return err
	}
	// Unique font URLs, downloaded a few at a time.
	seen := map[string]string{}
	var urls []string
	for _, m := range gweCSSURLRe.FindAllStringSubmatch(text, -1) {
		orig := strings.Trim(strings.TrimSpace(m[1]), `'"`)
		if _, ok := seen[orig]; !ok {
			seen[orig] = ""
			urls = append(urls, orig)
		}
	}
	type result struct {
		orig, local string
		err         error
	}
	sem := make(chan struct{}, 6)
	out := make(chan result, len(urls))
	for _, orig := range urls {
		go func(orig string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			clean := orig
			if strings.HasPrefix(clean, "//") {
				clean = "https:" + clean
			}
			clean = strings.Replace(clean, "http://", "https://", 1)
			if i := strings.IndexAny(clean, "?#"); i >= 0 {
				clean = clean[:i]
			}
			ext := strings.ToLower(strings.TrimPrefix(path.Ext(clean), "."))
			switch ext {
			case "woff2", "woff", "ttf", "otf", "eot", "svg":
			default:
				out <- result{orig: orig, err: fmt.Errorf("unexpected font file %q", clean)}
				return
			}
			b, err := gweFontFetch(ctx, clean)
			if err != nil {
				out <- result{orig: orig, err: err}
				return
			}
			sum := sha1.Sum([]byte(clean))
			name := key + "-" + hex.EncodeToString(sum[:])[:8] + "." + ext
			if err := gweWriteFileAtomic(filepath.Join(fontsDir, name), b); err != nil {
				out <- result{orig: orig, err: err}
				return
			}
			out <- result{orig: orig, local: "../fonts/" + name}
		}(orig)
	}
	for range urls {
		r := <-out
		if r.err != nil {
			return r.err
		}
		seen[r.orig] = r.local
	}
	text = gweCSSURLRe.ReplaceAllStringFunc(text, func(m string) string {
		orig := strings.Trim(strings.TrimSpace(gweCSSURLRe.FindStringSubmatch(m)[1]), `'"`)
		if local := seen[orig]; local != "" {
			return "url(" + local + ")"
		}
		return m
	})
	// font-display: swap on every face (the early access stylesheets have none).
	text = gweFontFaceRe.ReplaceAllStringFunc(text, func(face string) string {
		if gweFontDisplayRe.MatchString(face) {
			return gweFontDisplayRe.ReplaceAllString(face, "font-display: swap")
		}
		return strings.TrimSuffix(strings.TrimRight(face, " \n\t}"), ";") + ";\n  font-display: swap;\n}"
	})
	header := "/* " + family + " — Google Fonts served locally by Pursae Web Builder (SIL Open Font License / Apache License, see fonts.google.com) */\n"
	return gweWriteFileAtomic(filepath.Join(cssDir, key+".css"), []byte(header+text))
}

func gweWriteFileAtomic(p string, b []byte) error {
	tmp := p + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// gwePreloadCache remembers each stylesheet's preload file by modification time.
var gwePreloadCache sync.Map // path -> [2]string{mtime, file}

var (
	gweFaceCommentRe = regexp.MustCompile(`(?s)/\*\s*([a-z0-9-]+)\s*\*/\s*(@font-face\s*\{[^}]*\})`)
	gweFaceWeightRe  = regexp.MustCompile(`font-weight\s*:\s*(\d+|normal)`)
	gweFaceStyleRe   = regexp.MustCompile(`font-style\s*:\s*(\w+)`)
	gweFaceWoff2Re   = regexp.MustCompile(`url\(\.\./fonts/([^)]+\.woff2)\)`)
)

// gweFontPreloadFile picks the face to preload: regular (400 normal), latin subset when marked.
func gweFontPreloadFile(css string) string {
	type face struct{ subset, body string }
	var faces []face
	if m := gweFaceCommentRe.FindAllStringSubmatch(css, -1); len(m) > 0 {
		for _, x := range m {
			faces = append(faces, face{x[1], x[2]})
		}
	} else {
		for _, x := range gweFontFaceRe.FindAllString(css, -1) {
			faces = append(faces, face{"", x})
		}
	}
	score := func(f face) int {
		n := 0
		if w := gweFaceWeightRe.FindStringSubmatch(f.body); w != nil && (w[1] == "400" || w[1] == "normal") {
			n += 4
		}
		if st := gweFaceStyleRe.FindStringSubmatch(f.body); st == nil || st[1] == "normal" {
			n += 2
		}
		if f.subset == "latin" {
			n++
		}
		return n
	}
	best, bestScore := "", -1
	for _, f := range faces {
		m := gweFaceWoff2Re.FindStringSubmatch(f.body)
		if m == nil {
			continue
		}
		if sc := score(f); sc > bestScore {
			best, bestScore = m[1], sc
		}
	}
	return best
}

// gweLocalFontLinks returns the preload and stylesheet tags of a downloaded family ("" if missing).
func (s *Server) gweLocalFontLinks(c *gin.Context, family string) string {
	key := gweFontKey(family)
	cssPath := filepath.Join(s.gweFontsDir("css"), key+".css")
	st, err := os.Stat(cssPath)
	if err != nil {
		return ""
	}
	ver := strconv.FormatInt(st.ModTime().Unix(), 10)
	preload := ""
	if v, ok := gwePreloadCache.Load(cssPath); ok && v.([2]string)[0] == ver {
		preload = v.([2]string)[1]
	} else if b, err := os.ReadFile(cssPath); err == nil {
		preload = gweFontPreloadFile(string(b))
		gwePreloadCache.Store(cssPath, [2]string{ver, preload})
	}
	lb := s.links(c)
	base := "/wp-content/uploads/" + gweFontsBase
	var sb strings.Builder
	if preload != "" {
		fmt.Fprintf(&sb, "<link rel=\"preload\" href=\"%s\" as=\"font\" type=\"font/woff2\" crossorigin>\n", html.EscapeString(lb.HomeURL(base+"/fonts/"+preload)))
	}
	fmt.Fprintf(&sb, "<link rel='stylesheet' id='%s-css' href='%s' media='all' />\n", "gwe-gf-local-"+key, html.EscapeString(lb.HomeURL(base+"/css/"+key+".css?ver="+ver)))
	return sb.String()
}

// gweGoogleFontsHead returns the <link> tags that load the given families from this site,
// downloading them first when needed. It is the entry point for other code (classic widgets, the
// kit, atomic elements): families outside the Google Fonts catalogue are skipped, duplicates are
// printed once per request.
func (s *Server) gweGoogleFontsHead(c *gin.Context, families []string) string {
	if !s.gweGoogleFontsEnabled() {
		return ""
	}
	done := map[string]bool{}
	if c != nil {
		if v, ok := c.Get("gwe.fonts.printed"); ok {
			done = v.(map[string]bool)
		}
	}
	var sb strings.Builder
	for _, f := range families {
		f = strings.TrimSpace(strings.Trim(strings.TrimSpace(f), `"'`))
		key := gweFontKey(f)
		if done[key] || gweGoogleFontType(f) == "" {
			continue
		}
		done[key] = true
		if s.gweEnsureGoogleFont(f, gweFontsWait) {
			sb.WriteString(s.gweLocalFontLinks(c, f))
		}
	}
	if c != nil {
		c.Set("gwe.fonts.printed", done)
	}
	return sb.String()
}

// --- settings ------------------------------------------------------------------------------------------

func (s *Server) gweFontsSettingsSection(c *gin.Context) string {
	on, off := " selected", ""
	if !s.gweGoogleFontsEnabled() {
		on, off = "", " selected"
	}
	n := 0
	if entries, err := os.ReadDir(s.gweFontsDir("css")); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".css") {
				n++
			}
		}
	}
	return `<h2 id="fonts">Fonts</h2><table class="form-table" role="presentation"><tbody>` +
		`<tr><th scope="row"><label for="gwe_local_google_fonts">Load Google Fonts Locally</label></th><td><select id="gwe_local_google_fonts" name="gwe_local_google_fonts">` +
		`<option value="1"` + on + `>Enable</option><option value="0"` + off + `>Disable</option></select>` +
		`<p class="description">Google Fonts used on the site are downloaded once by the server and served from this site (font-display: swap), so visitors' browsers never contact Google. Disable to not load Google Fonts at all; pages then use the fallback fonts.</p></td></tr>` +
		`<tr><th scope="row">Downloaded families</th><td>` + strconv.Itoa(n) + ` <label style="margin-left:12px"><input type="checkbox" name="gwe_google_fonts_clear" value="1"/> Delete the downloaded files (they are fetched again when needed)</label></td></tr>` +
		`</tbody></table>`
}

func (s *Server) gweFontsSaveSettings(c *gin.Context, vals map[string]any) {
	v := c.PostForm("gwe_local_google_fonts")
	if v == "0" || v == "1" {
		vals["gwe_local_google_fonts"] = v
	}
	if c.PostForm("gwe_google_fonts_clear") == "1" {
		_ = os.RemoveAll(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(gweFontsBase)))
		gwePreloadCache.Range(func(k, _ any) bool { gwePreloadCache.Delete(k); return true })
		gweFontFailures.Range(func(k, _ any) bool { gweFontFailures.Delete(k); return true })
	}
}
