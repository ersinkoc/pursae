package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
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

	"wpgo/internal/webbuilder"
)

// Google Fonts served from this site. Visitors' browsers never contact Google: the server downloads
// a family's stylesheet from fonts.googleapis.com and its woff2 files from fonts.gstatic.com once,
// stores them under uploads/web-builder/google-fonts/{css,fonts}/, rewrites the URLs (font-display:
// swap) and the page links the local stylesheet plus a preload of the regular latin face
// (gfontPreloadFile). Only the families of the Web Builder's font library (webbuilder.LookupFont,
// all of them Google Fonts families) are handled, with the weights the library lists for them.

const (
	gfontsBase = builderUploadsDir + "/google-fonts" // under the uploads dir
	// A current desktop browser's user agent: Google Fonts picks the font format (woff2) by it.
	gfontUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36"
	gfontDownloadTime = 3 * time.Minute
	gfontFetchTime    = 60 * time.Second // one request
)

// gfontFetch downloads one URL from Google Fonts. Tests replace it; the default refuses every
// URL but https:// on fonts.googleapis.com and fonts.gstatic.com, never follows redirects and caps
// the body at 8 MB.
var gfontFetch = func(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("Invalid API URL.")
	}
	switch strings.ToLower(u.Hostname()) {
	case "fonts.googleapis.com", "fonts.gstatic.com":
	default:
		return nil, errors.New("API host not allowed.")
	}
	ctx, cancel := context.WithTimeout(ctx, gfontFetchTime)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", gfontUserAgent)
	client := &http.Client{Timeout: gfontFetchTime, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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

// gfontFamily is the library font of that exact family name (false: not one the localizer serves).
func gfontFamily(family string) (webbuilder.FontFamily, bool) {
	f, ok := webbuilder.LookupFont(strings.TrimSpace(family))
	return f, ok && f.Family == strings.TrimSpace(family)
}

// gfontKey is sanitize_key( family ).
func gfontKey(family string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(family) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Server) gfontsDir(sub string) string {
	return filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(gfontsBase), sub)
}

// gfontJobs dedupes concurrent downloads per uploads dir + family.
var gfontJobs sync.Map // string -> chan struct{} (closed when done)

// gfontEnsure downloads a family once; it returns true when the local stylesheet exists.
// It waits at most wait for a running download (which carries on in the background).
func (s *Server) gfontEnsure(family string, wait time.Duration) bool {
	f, ok := gfontFamily(family)
	key := gfontKey(family)
	if !ok || key == "" {
		return false
	}
	cssPath := filepath.Join(s.gfontsDir("css"), key+".css")
	if _, err := os.Stat(cssPath); err == nil {
		return true
	}
	job := s.cfg.UploadsDir + "\x00" + key
	if t, ok := gfontFailures.Load(job); ok && time.Since(t.(time.Time)) < 10*time.Minute {
		return false // a recent failure: don't retry on every page view
	}
	ch := make(chan struct{})
	if prev, loaded := gfontJobs.LoadOrStore(job, ch); loaded {
		ch = prev.(chan struct{})
	} else {
		safeGo("gfonts.gfontEnsure", func() {
			defer func() {
				close(ch)
				gfontJobs.Delete(job)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), gfontDownloadTime)
			defer cancel()
			if err := s.gfontDownload(ctx, f); err != nil {
				gfontFailures.Store(job, time.Now())
			}
		})
	}
	select {
	case <-ch:
	case <-time.After(wait):
		return false
	}
	_, err := os.Stat(cssPath)
	return err == nil
}

var gfontFailures sync.Map

// gfontURL is the Google Fonts stylesheet of a family with every weight and italic the library
// lists for it, e.g. css?family=Inter:400,700,400italic&display=swap (400 when it lists none).
func gfontURL(f webbuilder.FontFamily) string {
	var axes []string
	for _, w := range f.Weights {
		axes = append(axes, strconv.Itoa(w))
	}
	for _, w := range f.Italics {
		axes = append(axes, strconv.Itoa(w)+"italic")
	}
	if len(axes) == 0 {
		axes = []string{"400"}
	}
	return "https://fonts.googleapis.com/css?family=" + strings.ReplaceAll(f.Family, " ", "+") + ":" + strings.Join(axes, ",") + "&display=swap"
}

var (
	gfontCSSURLRe  = regexp.MustCompile(`url\(([^)]+)\)`)
	gfontFaceRe    = regexp.MustCompile(`(?s)@font-face\s*\{[^}]*\}`)
	gfontDisplayRe = regexp.MustCompile(`font-display\s*:[^;}]*`)
)

// gfontDownload fetches a family's CSS and font files and writes the local stylesheet.
func (s *Server) gfontDownload(ctx context.Context, f webbuilder.FontFamily) error {
	family := f.Family
	key := gfontKey(family)
	css, err := gfontFetch(ctx, gfontURL(f))
	if err != nil {
		return err
	}
	text := string(css)
	if !strings.Contains(text, "@font-face") {
		return errors.New("no @font-face in the Google Fonts stylesheet")
	}
	fontsDir, cssDir := s.gfontsDir("fonts"), s.gfontsDir("css")
	if err := os.MkdirAll(fontsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(cssDir, 0o755); err != nil {
		return err
	}
	// Unique font URLs, downloaded a few at a time.
	seen := map[string]string{}
	var urls []string
	for _, m := range gfontCSSURLRe.FindAllStringSubmatch(text, -1) {
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
		safeGo("gfonts.gfontDownload", func() {
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
			b, err := gfontFetch(ctx, clean)
			if err != nil {
				out <- result{orig: orig, err: err}
				return
			}
			sum := sha1.Sum([]byte(clean))
			name := key + "-" + hex.EncodeToString(sum[:])[:8] + "." + ext
			if err := gfontWriteFileAtomic(filepath.Join(fontsDir, name), b); err != nil {
				out <- result{orig: orig, err: err}
				return
			}
			out <- result{orig: orig, local: "../fonts/" + name}
		})
	}
	for range urls {
		r := <-out
		if r.err != nil {
			return r.err
		}
		seen[r.orig] = r.local
	}
	text = gfontCSSURLRe.ReplaceAllStringFunc(text, func(m string) string {
		orig := strings.Trim(strings.TrimSpace(gfontCSSURLRe.FindStringSubmatch(m)[1]), `'"`)
		if local := seen[orig]; local != "" {
			return "url(" + local + ")"
		}
		return m
	})
	// font-display: swap on every face, added where a face has none.
	text = gfontFaceRe.ReplaceAllStringFunc(text, func(face string) string {
		if gfontDisplayRe.MatchString(face) {
			return gfontDisplayRe.ReplaceAllString(face, "font-display: swap")
		}
		return strings.TrimSuffix(strings.TrimRight(face, " \n\t}"), ";") + ";\n  font-display: swap;\n}"
	})
	header := "/* " + family + " — Google Fonts served locally by Pursae Web Builder (SIL Open Font License / Apache License, see fonts.google.com) */\n"
	return gfontWriteFileAtomic(filepath.Join(cssDir, key+".css"), []byte(header+text))
}

func gfontWriteFileAtomic(p string, b []byte) error {
	tmp := p + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// gfontPreloadCache remembers each stylesheet's preload file by modification time.
var gfontPreloadCache sync.Map // path -> [2]string{mtime, file}

var (
	gfontFaceCommentRe = regexp.MustCompile(`(?s)/\*\s*([a-z0-9-]+)\s*\*/\s*(@font-face\s*\{[^}]*\})`)
	gfontFaceWeightRe  = regexp.MustCompile(`font-weight\s*:\s*(\d+|normal)`)
	gfontFaceStyleRe   = regexp.MustCompile(`font-style\s*:\s*(\w+)`)
	gfontFaceWoff2Re   = regexp.MustCompile(`url\(\.\./fonts/([^)]+\.woff2)\)`)
)

// gfontPreloadFile picks the face to preload: regular (400 normal), latin subset when marked.
func gfontPreloadFile(css string) string {
	type face struct{ subset, body string }
	var faces []face
	if m := gfontFaceCommentRe.FindAllStringSubmatch(css, -1); len(m) > 0 {
		for _, x := range m {
			faces = append(faces, face{x[1], x[2]})
		}
	} else {
		for _, x := range gfontFaceRe.FindAllString(css, -1) {
			faces = append(faces, face{"", x})
		}
	}
	score := func(f face) int {
		n := 0
		if w := gfontFaceWeightRe.FindStringSubmatch(f.body); w != nil && (w[1] == "400" || w[1] == "normal") {
			n += 4
		}
		if st := gfontFaceStyleRe.FindStringSubmatch(f.body); st == nil || st[1] == "normal" {
			n += 2
		}
		if f.subset == "latin" {
			n++
		}
		return n
	}
	best, bestScore := "", -1
	for _, f := range faces {
		m := gfontFaceWoff2Re.FindStringSubmatch(f.body)
		if m == nil {
			continue
		}
		if sc := score(f); sc > bestScore {
			best, bestScore = m[1], sc
		}
	}
	return best
}
