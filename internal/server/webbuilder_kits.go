package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Pursae Web Builder design kits (docs/developer/web-builder-code-sections.md): the files a design
// brings (stylesheets, scripts, fonts, images) kept with their folder structure under
// uploads/web-builder/kits/<slug>/, so the design's relative references keep working. Code
// sections point at them with absolute URLs; the front end adds the kit's version (?v=) so the
// files can be cached as immutable, and requests without the current version revalidate.
//
// Writing a kit is writing site-wide CSS and JS: it needs unfiltered_html and edit_theme_options.
// Files are staged (POST kits/{slug}/files, any number of calls) and go live together (POST
// kits/{slug}/commit).

const (
	wbKitsOption    = "wb_design_kits"
	wbKitsDir       = "web-builder/kits"
	wbKitStageDir   = ".next"
	wbKitMaxFile    = 15 << 20
	wbKitMaxTotal   = 100 << 20
	wbKitMaxFiles   = 2000
	wbKitBodyMax    = 24 << 20 // one files request (base64 included)
	wbKitURLPrefix  = "/wp-content/uploads/" + wbKitsDir + "/"
	wbKitCacheShort = "public, no-cache"
)

var (
	wbKitSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	wbKitSegRE  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._@+-]{0,127}$`)
	// What a kit may hold: web assets only (no HTML documents, no server-side code).
	wbKitExts = map[string]bool{
		".css": true, ".js": true, ".mjs": true, ".map": true, ".json": true, ".txt": true,
		".woff2": true, ".woff": true, ".ttf": true, ".otf": true, ".eot": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true, ".ico": true,
		".mp4": true, ".webm": true, ".mp3": true, ".ogg": true, ".wav": true, ".glb": true, ".gltf": true, ".bin": true, ".wasm": true,
		".pdf": true,
	}
	wbKitsMu sync.Mutex // kit writes (staging and commit)
)

// wbKit is one kit in the wb_design_kits option.
type wbKit struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	Updated string `json:"updated"`
	// Origins are the https origins the kit's scripts and stylesheets name (CDN modules, APIs):
	// pages using the kit allow them in their Content Security Policy.
	Origins []string `json:"origins,omitempty"`
}

func (s *Server) wbKitList() map[string]*wbKit {
	out := map[string]*wbKit{}
	raw := s.settings.Get(wbKitsOption)
	if raw == nil {
		return out
	}
	if b, err := json.Marshal(raw); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	for slug, k := range out {
		if k == nil || !wbKitSlugRE.MatchString(slug) {
			delete(out, slug)
			continue
		}
		k.Slug = slug
	}
	return out
}

// wbKitRoot is the kits folder on disk.
func (s *Server) wbKitRoot() string {
	return filepath.Join(filepath.Clean(s.cfg.UploadsDir), filepath.FromSlash(wbKitsDir))
}

// wbKitRel checks a file path inside a kit: relative, slash-separated, plain segments, a web asset
// extension. It returns the cleaned path.
func wbKitRel(p string) (string, bool) {
	p = strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(p, "\\", "/")), "./")
	if p == "" || strings.HasPrefix(p, "/") || len(p) > 512 {
		return "", false
	}
	segs := strings.Split(p, "/")
	if len(segs) > 8 {
		return "", false
	}
	for _, seg := range segs {
		if !wbKitSegRE.MatchString(seg) || seg == wbKitStageDir {
			return "", false
		}
	}
	if !wbKitExts[strings.ToLower(path.Ext(p))] {
		return "", false
	}
	return p, true
}

// wbKitView is a kit as the REST API shows it.
func (s *Server) wbKitView(c *gin.Context, k *wbKit) map[string]any {
	return map[string]any{"slug": k.Slug, "name": k.Name, "version": k.Version, "files": k.Files, "bytes": k.Bytes,
		"updated": k.Updated, "url": s.links(c).HomeURL(wbKitURLPrefix + k.Slug + "/")}
}

// wbKitFiles lists the files under dir (slash paths relative to it) with their sizes.
func wbKitFiles(dir string) (map[string]int64, error) {
	out := map[string]int64{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == wbKitStageDir && p != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if info, err := d.Info(); err == nil {
			out[filepath.ToSlash(rel)] = info.Size()
		}
		return nil
	})
	return out, err
}

func (s *Server) wbRegisterKitREST(reg func(pattern string, methods map[string]restHandler, handshake bool)) {
	const kit = `/web-builder/v2/kits/(?P<slug>[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)`
	reg(`/web-builder/v2/kits`, map[string]restHandler{http.MethodGet: s.wbRESTKits}, true)
	reg(kit, map[string]restHandler{http.MethodGet: s.wbRESTKit, http.MethodDelete: s.wbRESTKitDelete}, true)
	reg(kit+`/files`, map[string]restHandler{http.MethodPost: s.wbRESTKitFiles}, true)
	reg(kit+`/commit`, map[string]restHandler{http.MethodPost: s.wbRESTKitCommit}, true)
}

// wbKitUser: reading kits needs edit_posts (the editor shows them); writing needs unfiltered_html
// and edit_theme_options.
func wbKitUser(c *gin.Context, write bool) bool {
	if !write {
		return wbUser(c, "edit_posts") != nil
	}
	u := wbUser(c, webbuilder.TemplateCap)
	if u == nil {
		return false
	}
	if !u.Can("unfiltered_html", nil) {
		wbForbidden(c, u)
		return false
	}
	return true
}

func (s *Server) wbRESTKits(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, false) {
		return
	}
	kits := s.wbKitList()
	slugs := make([]string, 0, len(kits))
	for slug := range kits {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	out := make([]map[string]any, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, s.wbKitView(c, kits[slug]))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (s *Server) wbRESTKit(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, false) {
		return
	}
	slug := r.Params["slug"]
	k := s.wbKitList()[slug]
	if k == nil {
		restErr(c, http.StatusNotFound, "wb_kit_not_found", "No design kit has that slug.")
		return
	}
	files, _ := wbKitFiles(filepath.Join(s.wbKitRoot(), slug))
	list := make([]map[string]any, 0, len(files))
	for p, n := range files {
		list = append(list, map[string]any{"path": p, "bytes": n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i]["path"].(string) < list[j]["path"].(string) })
	v := s.wbKitView(c, k)
	v["fileList"] = list
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, v)
}

// wbKitFileIn is one file of a POST kits/{slug}/files: data is base64, text is UTF-8 (one of them).
type wbKitFileIn struct {
	Path string  `json:"path"`
	Data *string `json:"data"`
	Text *string `json:"text"`
}

// wbRESTKitFiles is POST kits/{slug}/files {name?, fresh?, files: [{path, data|text}], delete?: [path]}:
// writes into the kit's staging folder (created from the live files, or empty with fresh: true).
func (s *Server) wbRESTKitFiles(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, true) {
		return
	}
	slug := r.Params["slug"]
	raw, ok := wbBody(c, r, wbKitBodyMax)
	if !ok {
		return
	}
	var in struct {
		Fresh  bool          `json:"fresh"`
		Files  []wbKitFileIn `json:"files"`
		Delete []string      `json:"delete"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_json", "The body must be {files: [{path, data|text}]}.")
		return
	}
	type file struct {
		rel  string
		body []byte
	}
	files := make([]file, 0, len(in.Files))
	for i, f := range in.Files {
		rel, ok := wbKitRel(f.Path)
		if !ok {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "files["+strconv.Itoa(i)+"].path is not allowed: "+truncRunes(f.Path, 120)+
				" (relative path, plain names, web asset extensions only).", map[string]any{"key": "files"})
			return
		}
		var body []byte
		switch {
		case f.Data != nil:
			b, err := base64.StdEncoding.DecodeString(*f.Data)
			if err != nil {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "files["+strconv.Itoa(i)+"].data is not base64.", map[string]any{"key": "files"})
				return
			}
			body = b
		case f.Text != nil:
			body = []byte(*f.Text)
		default:
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "files["+strconv.Itoa(i)+"] needs data or text.", map[string]any{"key": "files"})
			return
		}
		if len(body) > wbKitMaxFile {
			restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, rel+" is larger than 15 MB.")
			return
		}
		files = append(files, file{rel, body})
	}
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	live := filepath.Join(s.wbKitRoot(), slug)
	stage := filepath.Join(live, wbKitStageDir)
	if _, err := os.Stat(stage); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(stage, 0o755); err != nil {
			restErr(c, http.StatusInternalServerError, "wb_kit_io", "Could not write the kit.")
			return
		}
		if !in.Fresh {
			if err := wbKitCopy(live, stage); err != nil {
				restErr(c, http.StatusInternalServerError, "wb_kit_io", "Could not write the kit.")
				return
			}
		}
	} else if in.Fresh {
		_ = os.RemoveAll(stage)
		_ = os.MkdirAll(stage, 0o755)
	}
	for _, d := range in.Delete {
		if rel, ok := wbKitRel(d); ok {
			_ = os.Remove(filepath.Join(stage, filepath.FromSlash(rel)))
		}
	}
	for _, f := range files {
		dst := filepath.Join(stage, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			restErr(c, http.StatusInternalServerError, "wb_kit_io", "Could not write the kit.")
			return
		}
		if err := os.WriteFile(dst, f.body, 0o644); err != nil {
			restErr(c, http.StatusInternalServerError, "wb_kit_io", "Could not write the kit.")
			return
		}
	}
	staged, _ := wbKitFiles(stage)
	var total int64
	for _, n := range staged {
		total += n
	}
	if len(staged) > wbKitMaxFiles || total > wbKitMaxTotal {
		restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, "A design kit holds at most 2000 files and 100 MB.")
		return
	}
	c.JSON(http.StatusOK, map[string]any{"slug": slug, "staged": len(staged), "bytes": total, "written": len(files)})
}

// wbRESTKitCommit is POST kits/{slug}/commit {name?}: the staged files replace the live ones.
func (s *Server) wbRESTKitCommit(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, true) {
		return
	}
	slug := r.Params["slug"]
	var in struct {
		Name string `json:"name"`
	}
	if raw, ok := wbBody(c, r, 4<<10); !ok {
		return
	} else if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	k, status, code, msg := s.wbKitCommitStage(c, slug, in.Name)
	if k == nil {
		restErr(c, status, code, msg)
		return
	}
	c.JSON(http.StatusOK, s.wbKitView(c, k))
}

// wbKitCommitStage swaps the kit's staged files in for its live ones (the caller holds wbKitsMu),
// bumps the version and saves the kit's entry. On failure it returns a nil kit and the REST error.
func (s *Server) wbKitCommitStage(c *gin.Context, slug, name string) (k *wbKit, status int, code, msg string) {
	live := filepath.Join(s.wbKitRoot(), slug)
	stage := filepath.Join(live, wbKitStageDir)
	if _, err := os.Stat(stage); err != nil {
		return nil, http.StatusConflict, "wb_kit_nothing_staged", "Nothing is staged for this kit: send files first."
	}
	// Swap: the staged folder becomes the kit (the old files go, the staging folder with them).
	tmp := live + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	parent := filepath.Dir(live)
	next := filepath.Join(parent, "."+slug+".next")
	_ = os.RemoveAll(next)
	if err := os.Rename(stage, next); err != nil {
		return nil, http.StatusInternalServerError, "wb_kit_io", "Could not publish the kit."
	}
	if err := os.Rename(live, tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Rename(next, stage)
		return nil, http.StatusInternalServerError, "wb_kit_io", "Could not publish the kit."
	}
	if err := os.Rename(next, live); err != nil {
		_ = os.Rename(tmp, live)
		return nil, http.StatusInternalServerError, "wb_kit_io", "Could not publish the kit."
	}
	_ = os.RemoveAll(tmp)
	files, _ := wbKitFiles(live)
	var total int64
	for _, n := range files {
		total += n
	}
	kits := s.wbKitList()
	k = kits[slug]
	if k == nil {
		k = &wbKit{Slug: slug, Name: slug}
		kits[slug] = k
	}
	if name = strings.TrimSpace(name); name != "" {
		k.Name = truncRunes(name, 120)
	}
	k.Version++
	k.Files, k.Bytes, k.Updated = len(files), total, wbTime(time.Now())
	k.Origins = wbKitOrigins(live, files)
	if err := s.settings.Set(c, map[string]any{wbKitsOption: kits}); err != nil {
		return nil, http.StatusInternalServerError, "db_error", "Could not save the kit."
	}
	wbKitVersions.Delete("v")
	s.purgeAll(c)
	return k, http.StatusOK, "", ""
}

func (s *Server) wbRESTKitDelete(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, true) {
		return
	}
	slug := r.Params["slug"]
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	if status, code, msg := s.wbKitRemove(c, slug); code != "" {
		restErr(c, status, code, msg)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"deleted": true, "slug": slug})
}

// wbKitRemove deletes a kit and its files (the caller holds wbKitsMu); code is "" on success.
func (s *Server) wbKitRemove(c *gin.Context, slug string) (status int, code, msg string) {
	kits := s.wbKitList()
	if kits[slug] == nil {
		return http.StatusNotFound, "wb_kit_not_found", "No design kit has that slug."
	}
	delete(kits, slug)
	if err := s.settings.Set(c, map[string]any{wbKitsOption: kits}); err != nil {
		return http.StatusInternalServerError, "db_error", "Could not delete the kit."
	}
	_ = os.RemoveAll(filepath.Join(s.wbKitRoot(), slug))
	wbKitVersions.Delete("v")
	s.purgeAll(c)
	return http.StatusOK, "", ""
}

var wbKitOriginRE = regexp.MustCompile(`['"\x60(]https://([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+(?::\d{1,5})?)[/'"\x60)]`)

// wbKitOrigins lists the https origins named in the kit's scripts and stylesheets (at most 24).
func wbKitOrigins(dir string, files map[string]int64) []string {
	seen := map[string]bool{}
	var out []string
	for rel, n := range files {
		ext := strings.ToLower(path.Ext(rel))
		if (ext != ".js" && ext != ".mjs" && ext != ".css") || n > 4<<20 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, m := range wbKitOriginRE.FindAllSubmatch(b, -1) {
			o := "https://" + strings.ToLower(string(m[1]))
			if !seen[o] && len(out) < 24 {
				seen[o] = true
				out = append(out, o)
			}
		}
	}
	sort.Strings(out)
	return out
}

// wbKitCopy copies the live kit files into the staging folder.
func wbKitCopy(src, dst string) error {
	files, err := wbKitFiles(src)
	if err != nil {
		return err
	}
	for rel := range files {
		from, to := filepath.Join(src, filepath.FromSlash(rel)), filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Link(from, to); err == nil { // the live file is replaced, never written in place
			continue
		}
		b, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.WriteFile(to, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// --- front end ---------------------------------------------------------------------------------

// wbKitVersions caches slug → version (reset when a kit changes).
var wbKitVersions sync.Map // "v" → *map[string]int

func (s *Server) wbKitVersionMap() map[string]int {
	if p, ok := wbKitVersions.Load("v"); ok {
		if m, _ := p.(*map[string]int); m != nil {
			return *m
		}
	}
	m := map[string]int{}
	for slug, k := range s.wbKitList() {
		m[slug] = k.Version
	}
	wbKitVersions.Store("v", &m)
	return m
}

var wbKitRefRE = regexp.MustCompile(`(/wp-content/uploads/web-builder/kits/([a-z0-9-]{1,64})/[A-Za-z0-9_./@+-]{1,512}?\.[A-Za-z0-9]{1,8})(\?v=\d+)?(["'\s)>,])`)

// wbKitVersioned adds ?v=<kit version> to the kit file URLs of code-section markup, so the files
// are served as immutable and a changed kit gets new URLs.
func (s *Server) wbKitVersioned(src string) string {
	if !strings.Contains(src, wbKitURLPrefix) {
		return src
	}
	vers := s.wbKitVersionMap()
	return wbKitRefRE.ReplaceAllStringFunc(src, func(m string) string {
		g := wbKitRefRE.FindStringSubmatch(m)
		v, ok := vers[g[2]]
		if !ok {
			return m
		}
		return g[1] + "?v=" + strconv.Itoa(v) + g[4]
	})
}

// wbKitCacheControl is the Cache-Control of a kit file: immutable when the request names the
// kit's current version, else revalidated (CSS url() references, links from elsewhere).
func (s *Server) wbKitCacheControl(c *gin.Context, rel string) (string, bool) {
	if !strings.HasPrefix(rel, wbKitsDir+"/") {
		return "", false
	}
	slug, _, _ := strings.Cut(strings.TrimPrefix(rel, wbKitsDir+"/"), "/")
	if v, ok := s.wbKitVersionMap()[slug]; ok && c.Query("v") == strconv.Itoa(v) {
		return "public, max-age=31536000, immutable", true
	}
	return wbKitCacheShort, true
}

var (
	wbCodeScriptRE = regexp.MustCompile(`(?is)<script\b[^>]*\ssrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))`)
	wbCodeLinkRE   = regexp.MustCompile(`(?is)<link\b[^>]*\shref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))`)
	wbCodeFrameRE  = regexp.MustCompile(`(?is)<iframe\b[^>]*\ssrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))`)
	// url(https://…) and @import "https://…" in the markup's <style> elements and style attributes.
	wbCodeCSSURLRE = regexp.MustCompile(`(?i)(?:url\(\s*|@import\s+)(?:&#39;|&quot;|['"])?(https://[^'"\s)&;]+)`)
)

// wbCodeOriginMax bounds the origins one directive takes from the code sections' markup (the kit
// origins have the same cap, wbKitOrigins).
const wbCodeOriginMax = 24

// wbGoogleFonts: a stylesheet from fonts.googleapis.com loads its font files from fonts.gstatic.com.
const (
	wbGoogleFontsCSS   = "https://fonts.googleapis.com"
	wbGoogleFontsFiles = "https://fonts.gstatic.com"
)

// wbCodeCSP adds what the code sections load from other origins to this response's policy (only
// unfiltered_html authors can publish code sections; their scripts carry the response's nonce,
// csp_trusted.go):
//
//   - the kits the markup loads from: the https origins their scripts and stylesheets name, in
//     script-src, connect-src, style-src and font-src;
//   - <script src> origins in script-src and connect-src. Under the enforced nonce policy
//     ('strict-dynamic') browsers ignore host lists: the nonce on the tag is what lets the script
//     load; the hosts are for browsers without 'strict-dynamic' and for the report-only policy;
//   - <link href> origins and url() / @import origins of the markup's CSS in style-src and font-src
//     (fonts.googleapis.com brings fonts.gstatic.com);
//   - <iframe src> origins (a map, a video) in frame-src.
//
// Each list takes at most wbCodeOriginMax origins from the markup. Kit files are same-origin.
func (s *Server) wbCodeCSP(c *gin.Context, head, body string) {
	if c == nil {
		return
	}
	src := head + body
	// The kits the markup loads from: the origins their files name.
	if strings.Contains(src, wbKitURLPrefix) {
		kits := s.wbKitList()
		done := map[string]bool{}
		for _, m := range wbKitRefRE.FindAllStringSubmatch(src, -1) {
			if k := kits[m[2]]; k != nil && !done[m[2]] {
				done[m[2]] = true
				for _, dir := range []string{"script-src", "connect-src", "style-src", "font-src"} {
					cspAllow(c, dir, k.Origins...)
				}
				if cspHas(k.Origins, wbGoogleFontsCSS) {
					cspAllow(c, "font-src", wbGoogleFontsFiles)
				}
			}
		}
	}
	if !strings.Contains(src, "https://") {
		return
	}
	var scripts, styles, fonts, frames []string
	add := func(list *[]string, o string) {
		if o != "" && len(*list) < wbCodeOriginMax && !cspHas(*list, o) {
			*list = append(*list, o)
		}
	}
	style := func(o string) {
		add(&styles, o)
		add(&fonts, o)
		if o == wbGoogleFontsCSS {
			add(&fonts, wbGoogleFontsFiles)
		}
	}
	for _, m := range wbCodeScriptRE.FindAllStringSubmatch(src, -1) {
		add(&scripts, wbHTTPSOrigin(html.UnescapeString(m[1]+m[2]+m[3])))
	}
	for _, m := range wbCodeLinkRE.FindAllStringSubmatch(src, -1) {
		style(wbHTTPSOrigin(html.UnescapeString(m[1] + m[2] + m[3])))
	}
	for _, m := range wbCodeCSSURLRE.FindAllStringSubmatch(src, -1) {
		style(wbHTTPSOrigin(m[1]))
	}
	for _, m := range wbCodeFrameRE.FindAllStringSubmatch(src, -1) {
		add(&frames, wbHTTPSOrigin(html.UnescapeString(m[1]+m[2]+m[3])))
	}
	cspAllow(c, "script-src", scripts...)
	cspAllow(c, "connect-src", scripts...)
	cspAllow(c, "style-src", styles...)
	cspAllow(c, "font-src", fonts...)
	cspAllow(c, "frame-src", frames...)
}
