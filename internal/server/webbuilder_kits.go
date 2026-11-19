package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
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
	wbKitMaxTotal   = 256 << 20 // a WordPress copy's used images alone are 140 MB (Decoland)
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
	// Root (unix nanoseconds of the last site import of the kit, 0: never) serves the kit's files at
	// the site's root as well (wbKitRootAsset): the site import maps the design's folders to the
	// site's paths, so the URLs its scripts and stylesheets build keep working.
	Root int64 `json:"root,omitempty"`
	// Breakpoints are the tablet and mobile thresholds of local styles (element editor, Style
	// panel): set on commit (the request's, else suggested from the kit's stylesheets).
	Breakpoints *webbuilder.Breakpoints `json:"breakpoints,omitempty"`
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
		"updated": k.Updated, "url": s.links(c).HomeURL(wbKitURLPrefix + k.Slug + "/"), "breakpoints": s.wbKitBreakpoints(k.Slug)}
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
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".wb-kit-") { // a write in progress (wbKitWriteFile)
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
	s.wbRegisterKitOverrideREST(reg)
}

// wbKitUser: reading a kit's override needs edit_posts (the editor's frame shows it on every page
// using the kit); writing needs unfiltered_html and edit_theme_options.
func wbKitUser(c *gin.Context, write bool) bool {
	if !write {
		return wbUser(c, "edit_posts") != nil
	}
	u := wbUser(c, webbuilder.TemplateCap)
	if u == nil {
		return false
	}
	if !wbUnfiltered(c, u) {
		wbForbidden(c, u)
		return false
	}
	return true
}

// wbKitReader: the kits themselves (their list, files and sizes) are for who may use them in a code
// section, an unfiltered_html author.
func wbKitReader(c *gin.Context) bool {
	return wbUser(c, "unfiltered_html") != nil
}

func (s *Server) wbRESTKits(c *gin.Context, r *restRequest) {
	if !wbKitReader(c) {
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
	if !wbKitReader(c) {
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
		if err := wbKitWriteFile(dst, f.body); err != nil {
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
		restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, fmt.Sprintf("A design kit holds at most %d files and %d MB.", wbKitMaxFiles, wbKitMaxTotal>>20))
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
		Name        string                  `json:"name"`
		Breakpoints *webbuilder.Breakpoints `json:"breakpoints"`
	}
	if raw, ok := wbBody(c, r, 4<<10); !ok {
		return
	} else if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	k, status, code, msg := s.wbKitCommitStage(c, slug, in.Name, in.Breakpoints)
	if k == nil {
		restErr(c, status, code, msg)
		return
	}
	c.JSON(http.StatusOK, s.wbKitView(c, k))
}

// wbKitCommitStage swaps the kit's staged files in for its live ones (the caller holds wbKitsMu),
// bumps the version and saves the kit's entry with the given local-style breakpoints (nil:
// suggested from the kit's stylesheets). On failure it returns a nil kit and the REST error.
func (s *Server) wbKitCommitStage(c *gin.Context, slug, name string, breakpoints *webbuilder.Breakpoints) (k *wbKit, status int, code, msg string) {
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
		// A kit deleted and uploaded again goes on from the old one's version: browsers keep the old
		// files under their immutable ?v= URLs.
		k = &wbKit{Slug: slug, Name: slug, Version: s.wbKitGoneVersions()[slug]}
		kits[slug] = k
	}
	if name = strings.TrimSpace(name); name != "" {
		k.Name = truncRunes(name, 120)
	}
	k.Version++
	k.Files, k.Bytes, k.Updated = len(files), total, wbTime(time.Now())
	k.Origins = wbKitOrigins(live, files)
	if breakpoints != nil {
		bp := breakpoints.Normalize()
		k.Breakpoints = &bp
	} else {
		bp := webbuilder.SuggestLocalBreakpoints(wbKitCSS(live, files))
		k.Breakpoints = &bp
	}
	if err := s.settings.Set(c, map[string]any{wbKitsOption: kits}); err != nil {
		return nil, http.StatusInternalServerError, "db_error", "Could not save the kit."
	}
	wbKitVersions.Delete("v")
	s.purgeAll(c)
	return k, http.StatusOK, "", ""
}

// wbKitRootAsset serves a file of a site-imported kit (wbKit.Root) at the site's root path: what
// a static host does for the design's folder. The site import maps index.html to / and a/b.html to
// /a/b/, so the URLs the design builds at run time — root-relative paths in its stylesheets
// (url(/assets/fonts/x.woff2)) and scripts, an import map's "/assets/vendor/three.module.js", an
// inline module's import "./js/engine.js" on /post/, a data-video="../assets/film.mp4" a script
// reads on /identity/ — reach the kit's files. It reports whether it answered.
//
// site() calls it after everything the site itself answers at a path of its own (Pursae SEO's
// redirections, robots.txt, sitemaps and IndexNow key, the shop's and the forum's paths, the
// built-in robots.txt, favicon.ico and sitemaps), so a design's file never stands in for one of
// them; wbKitRootReserved keeps the conventional root files out even when nothing answers them.
// Only GET and HEAD of a path that is a valid kit file (web asset extensions, no HTML, no dot
// segments) outside /wp-*, of a kit with a published page or template of its import (a draft
// import serves nothing at the root); when several kits have the file, the most recently imported
// wins.
//
// The files are served as data on the site's own origin (the uploads route's policy, see
// wbKitRootHeaders): a type from the extension, never sniffed; a sandbox policy for a file opened as
// a document (an SVG, a JSON or .map file holding markup); no service worker. A response's policy
// applies to it only as a document: the design's pages load the same scripts, modules, stylesheets,
// fonts and images as subresources, as they do from /wp-content/uploads/.
func (s *Server) wbKitRootAsset(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	p := path.Clean(c.Request.URL.Path)
	if strings.HasPrefix(p, "/wp-") || wbKitRootReserved(p) || !wbKitExts[strings.ToLower(path.Ext(p))] {
		return false
	}
	rel, ok := wbKitRel(strings.TrimPrefix(p, "/"))
	if !ok {
		return false
	}
	var best *wbKit
	full := ""
	for _, k := range s.wbKitList() {
		if k.Root <= 0 || (best != nil && k.Root <= best.Root) {
			continue
		}
		f := filepath.Join(s.wbKitRoot(), k.Slug, filepath.FromSlash(rel))
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() && s.wbKitRootLive(c, k.Slug) {
			best, full = k, f
		}
	}
	if best == nil {
		return false
	}
	wbKitRootHeaders(c.Writer.Header(), rel)
	if c.GetHeader("Service-Worker") != "" {
		// A design may not register a service worker over the site's root (it would control every
		// page, the admin included).
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.Header("Cache-Control", "no-store")
		c.String(http.StatusForbidden, "A design's script cannot be a service worker.")
		return true
	}
	c.Header("Cache-Control", wbKitCacheShort)
	http.ServeFile(c.Writer, c.Request, full)
	return true
}

// wbKitRootTypes are the Content-Types of the kit files served at the site's root; any other file
// is application/octet-stream (never sniffed as HTML).
var wbKitRootTypes = map[string]string{
	".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".map": "application/json", ".json": "application/json", ".txt": "text/plain; charset=utf-8",
	".woff2": "font/woff2", ".woff": "font/woff", ".ttf": "font/ttf", ".otf": "font/otf", ".eot": "application/vnd.ms-fontobject",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif",
	".svg": "image/svg+xml", ".ico": "image/x-icon",
	".mp4": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav",
	".glb": "model/gltf-binary", ".gltf": "model/gltf+json", ".bin": "application/octet-stream", ".wasm": "application/wasm",
	".pdf": "application/pdf",
}

// wbKitRootHeaders are the headers of a kit file served at the site's root: the type of the
// extension, nosniff, and the uploads route's sandbox policy (wbKitFilePolicy).
func wbKitRootHeaders(h http.Header, rel string) {
	ct := wbKitRootTypes[strings.ToLower(path.Ext(rel))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	wbKitFilePolicy(h, rel)
	h.Del("Service-Worker-Allowed")
}

// wbKitFilePolicy sets the policy of a kit file response (at the root and under /wp-content/uploads/):
// the uploads route's sandbox for every file a browser could open as a document (an SVG, a JSON
// file holding markup), none for a script. A response's policy only matters where the response is
// the document: a page loads the design's scripts, modules, stylesheets, fonts and images the same
// with or without it. A script's own response is the document of a Web Worker, though: under the
// sandbox the worker runs with an opaque origin and default-src 'none', so its importScripts and
// fetches fail (checked in Chromium). A script with nosniff and its text/javascript type is never
// run as a page, so it gets no policy, as on a static host.
func wbKitFilePolicy(h http.Header, rel string) {
	h.Del("Content-Security-Policy-Report-Only")
	switch strings.ToLower(path.Ext(rel)) {
	case ".js", ".mjs":
		h.Del("Content-Security-Policy")
	default:
		h.Set("Content-Security-Policy", wbKitSandboxPolicy)
	}
}

// wbKitSandboxPolicy is the uploads route's policy (server.go).
const wbKitSandboxPolicy = "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; media-src 'self'"

// wbKitRootReserved: root files a site answers by convention (robots.txt, favicon.ico, ads.txt,
// security.txt …, sitemaps) are never a design's, even when nothing on the site answers them.
func wbKitRootReserved(p string) bool {
	name := strings.ToLower(strings.TrimPrefix(p, "/"))
	if strings.Contains(name, "/") {
		return false
	}
	switch name {
	case "robots.txt", "favicon.ico", "ads.txt", "app-ads.txt", "sellers.json", "security.txt", "humans.txt", "llms.txt", "llms-full.txt":
		return true
	}
	return strings.HasPrefix(name, "sitemap") || strings.Contains(name, "-sitemap")
}

// wbKitRootLiveCache: slug → whether a page or template of the kit's import is published (true
// for wbKitRootLiveTTL, false for wbKitRootDeadTTL, so a publish shows at once).
var wbKitRootLiveCache sync.Map

type wbKitRootLiveEntry struct {
	at   time.Time
	live bool
}

const (
	wbKitRootLiveTTL = 30 * time.Second
	wbKitRootDeadTTL = 3 * time.Second
)

// wbKitRootLive reports whether the kit's site import has a published page or template.
func (s *Server) wbKitRootLive(ctx context.Context, slug string) bool {
	key := fmt.Sprintf("%p/%s", s, slug)
	if v, ok := wbKitRootLiveCache.Load(key); ok {
		e := v.(wbKitRootLiveEntry)
		ttl := wbKitRootDeadTTL
		if e.live {
			ttl = wbKitRootLiveTTL
		}
		if time.Since(e.at) < ttl {
			return e.live
		}
	}
	var live bool
	if err := s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE status = 'publish' AND meta->>$1 = $2)`, wbSiteMetaKit, slug).Scan(&live); err != nil {
		return false
	}
	wbKitRootLiveCache.Store(key, wbKitRootLiveEntry{at: time.Now(), live: live})
	return live
}

// wbKitRootLiveReset forgets the published state of the kits (an import, its removal, a publish).
func wbKitRootLiveReset() {
	wbKitRootLiveCache.Range(func(k, _ any) bool {
		wbKitRootLiveCache.Delete(k)
		return true
	})
}

// wbKitSetRoot marks a kit as served at the site's root (on, the time of the site import) or not.
func (s *Server) wbKitSetRoot(c *gin.Context, slug string, on bool) error {
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	kits := s.wbKitList()
	k := kits[slug]
	if k == nil {
		return nil
	}
	if on {
		k.Root = time.Now().UnixNano()
	} else if k.Root == 0 {
		return nil
	} else {
		k.Root = 0
	}
	if err := s.settings.Set(c, map[string]any{wbKitsOption: kits}); err != nil {
		return err
	}
	wbKitRootLiveReset()
	s.purgeAll(c)
	return nil
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

// wbKitGoneOption keeps the last version of every deleted kit (slug → version), so a kit uploaded
// again under the same slug never reuses a version (wbKitCommitStage).
const wbKitGoneOption = "wb_design_kits_gone"

func (s *Server) wbKitGoneVersions() map[string]int {
	out := map[string]int{}
	if raw := s.settings.Get(wbKitGoneOption); raw != nil {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &out)
		}
	}
	return out
}

// wbKitRemove deletes a kit and its files (the caller holds wbKitsMu); code is "" on success.
func (s *Server) wbKitRemove(c *gin.Context, slug string) (status int, code, msg string) {
	kits := s.wbKitList()
	if kits[slug] == nil {
		return http.StatusNotFound, "wb_kit_not_found", "No design kit has that slug."
	}
	gone := s.wbKitGoneVersions()
	if v := kits[slug].Version; v > gone[slug] {
		gone[slug] = v
		if err := s.settings.Set(c, map[string]any{wbKitGoneOption: gone}); err != nil {
			return http.StatusInternalServerError, "db_error", "Could not delete the kit."
		}
	}
	delete(kits, slug)
	if err := s.settings.Set(c, map[string]any{wbKitsOption: kits}); err != nil {
		return http.StatusInternalServerError, "db_error", "Could not delete the kit."
	}
	_ = os.RemoveAll(filepath.Join(s.wbKitRoot(), slug))
	s.wbKitOverrideDrop(c, slug)
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

// wbKitCSS is the text of the kit's stylesheets (each at most 4 MB, 16 MB in all).
func wbKitCSS(dir string, files map[string]int64) []string {
	var out []string
	var total int64
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		n := files[rel]
		if strings.ToLower(path.Ext(rel)) != ".css" || n > 4<<20 || total+n > 16<<20 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		total += n
		out = append(out, string(b))
	}
	return out
}

// wbKitBPCache: breakpoints suggested for kits committed before kits had them, by slug@version.
var wbKitBPCache sync.Map

// wbKitBreakpoints are the local-style breakpoints of a kit: its own, else suggested from its
// stylesheets (kits committed before breakpoints existed; cached per version), else the defaults.
func (s *Server) wbKitBreakpoints(slug string) webbuilder.Breakpoints {
	if slug == "" || !wbKitSlugRE.MatchString(slug) {
		return webbuilder.DefaultBreakpoints
	}
	k := s.wbKitList()[slug]
	if k == nil {
		return webbuilder.DefaultBreakpoints
	}
	if k.Breakpoints != nil {
		return k.Breakpoints.Normalize()
	}
	key := slug + "@" + strconv.Itoa(k.Version)
	if v, ok := wbKitBPCache.Load(key); ok {
		return v.(webbuilder.Breakpoints)
	}
	dir := filepath.Join(s.wbKitRoot(), slug)
	files, _ := wbKitFiles(dir)
	bp := webbuilder.SuggestLocalBreakpoints(wbKitCSS(dir, files))
	wbKitBPCache.Store(key, bp)
	return bp
}

// wbLocalBreakpoints fills in the breakpoints of code sections' kits (FillCode prints their local
// styles with them).
func (s *Server) wbLocalBreakpoints(list []webbuilder.CodeContent) {
	for i := range list {
		if len(list[i].Local) > 0 {
			list[i].Breakpoints = s.wbKitBreakpoints(list[i].Kit)
		}
	}
}

// wbLocalFontNeeds are the font needs (FontNeed JSON) whose family the local stylesheet names.
func wbLocalFontNeeds(raw json.RawMessage, local string) json.RawMessage {
	var needs []webbuilder.FontNeed
	if len(raw) == 0 || json.Unmarshal(raw, &needs) != nil {
		return nil
	}
	var out []webbuilder.FontNeed
	for _, n := range needs {
		if n.Family != "" && strings.Contains(local, n.Family) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	b, _ := json.Marshal(out)
	return b
}

// wbKitWriteFile writes a staged file through a temporary file renamed over it. The staging folder
// starts as hard links to the live files (wbKitCopy): writing into the existing name would change
// the live file (served as immutable for a year) before the commit; the rename only replaces the
// staging folder's link.
func wbKitWriteFile(dst string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".wb-kit-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
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
)

// wbCodeSourceCache: post id, rev and update time → the doc's code markup (wbCodeSource).
var wbCodeSourceCache sync.Map

// wbCodeSource is the stored markup of a doc's shown code sections (head and html, as their
// unfiltered_html author published them), for wbCodeCSP; "" without code sections.
func wbCodeSource(d *store.WebBuilderDoc) string {
	if d == nil || !webbuilder.HasCode(d.Doc) {
		return ""
	}
	key := wbDocKey(d)
	if v, ok := wbCodeSourceCache.Load(key); ok {
		return v.(string)
	}
	list, _ := webbuilder.CodeSections(d.Doc)
	var b strings.Builder
	for _, sec := range list {
		if sec.Hidden {
			continue
		}
		b.WriteString(sec.Head)
		b.WriteByte('\n')
		b.WriteString(sec.HTML)
		b.WriteByte('\n')
	}
	out := b.String()
	wbCodeSourceLen.Add(1)
	if wbCodeSourceLen.Load() > 512 { // a bound, not an LRU: the next requests fill it again
		wbCodeSourceCache.Range(func(k, _ any) bool { wbCodeSourceCache.Delete(k); return true })
		wbCodeSourceLen.Store(0)
	}
	wbCodeSourceCache.Store(key, out)
	return out
}

var wbCodeSourceLen atomic.Int64

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
// csp_trusted.go). code is the stored markup of the page's code sections (wbCodeSource) and of
// other trusted markup (a forum site template): never the filled page, whose comments, posts and
// field values anyone may have written, so a visitor's url(https://…) adds nothing.
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
// Pages read the origins of their stored docs once per revision (wbCodeCSPDocs).
func (s *Server) wbCodeCSP(c *gin.Context, code string) {
	if c == nil {
		return
	}
	s.wbCodeCSPApply(c, wbCodeOriginsOf(code))
}

// wbCodeOrigins is what wbCodeCSP reads from a code markup (wbCodeOriginsOf).
type wbCodeOrigins struct {
	kits                           []string // the slugs of the kits the markup loads from, in order
	scripts, styles, fonts, frames []string
}

// wbCodeOriginsOf reads the origins of a code markup; nil for blank markup.
func wbCodeOriginsOf(code string) *wbCodeOrigins {
	if strings.TrimSpace(code) == "" {
		return nil
	}
	src := code
	o := &wbCodeOrigins{}
	if strings.Contains(src, wbKitURLPrefix) {
		wbKitRefs(src, func(_, _ int, _, slug, _ string) {
			if !cspHas(o.kits, slug) {
				o.kits = append(o.kits, slug)
			}
		})
	}
	if !strings.Contains(src, "https://") {
		return o
	}
	add := func(list *[]string, o string) {
		if o != "" && len(*list) < wbCodeOriginMax && !cspHas(*list, o) {
			*list = append(*list, o)
		}
	}
	style := func(origin string) {
		add(&o.styles, origin)
		add(&o.fonts, origin)
		if origin == wbGoogleFontsCSS {
			add(&o.fonts, wbGoogleFontsFiles)
		}
	}
	for _, m := range wbCodeScriptRE.FindAllStringSubmatch(src, -1) {
		add(&o.scripts, wbHTTPSOrigin(html.UnescapeString(m[1]+m[2]+m[3])))
	}
	for _, m := range wbCodeLinkRE.FindAllStringSubmatch(src, -1) {
		style(wbHTTPSOrigin(html.UnescapeString(m[1] + m[2] + m[3])))
	}
	wbCSSURLs(src, func(u string) { style(wbHTTPSOrigin(u)) })
	for _, m := range wbCodeFrameRE.FindAllStringSubmatch(src, -1) {
		add(&o.frames, wbHTTPSOrigin(html.UnescapeString(m[1]+m[2]+m[3])))
	}
	return o
}

// wbCodeCSPApply adds the origins of a code markup to this response's policy (wbCodeCSP).
func (s *Server) wbCodeCSPApply(c *gin.Context, o *wbCodeOrigins) {
	if c == nil || o == nil {
		return
	}
	var kits map[string]*wbKit
	if len(o.kits) > 0 {
		kits = s.wbKitList()
	}
	wbCodeOriginsAllow(c, o, kits)
}

// wbCodeOriginsAllow is wbCodeCSPApply with the site's kits.
func wbCodeOriginsAllow(c *gin.Context, o *wbCodeOrigins, kits map[string]*wbKit) {
	// Code sections: images and media the design's scripts make in the page (a canvas exported with toBlob, a
	// video from a fetched file): blob: URLs are the page's own content.
	cspAllow(c, "img-src", "blob:")
	cspAllow(c, "media-src", "blob:")
	// The kits the markup loads from: the origins their files name.
	for _, slug := range o.kits {
		if k := kits[slug]; k != nil {
			for _, dir := range []string{"script-src", "connect-src", "style-src", "font-src"} {
				cspAllow(c, dir, k.Origins...)
			}
			if cspHas(k.Origins, wbGoogleFontsCSS) {
				cspAllow(c, "font-src", wbGoogleFontsFiles)
			}
		}
	}
	cspAllow(c, "script-src", o.scripts...)
	cspAllow(c, "connect-src", o.scripts...)
	cspAllow(c, "style-src", o.styles...)
	cspAllow(c, "font-src", o.fonts...)
	cspAllow(c, "frame-src", o.frames...)
}

// wbCodeOriginsCache: the docs of a page (their post ids, revisions and update times, as
// wbCodeSource keys them) → the origins of their code markup (*wbCodeOriginsEntry).
var (
	wbCodeOriginsCache sync.Map
	wbCodeOriginsN     atomic.Int64
)

type wbCodeOriginsEntry struct{ o *wbCodeOrigins }

// wbCodeOriginsMax bounds wbCodeOriginsCache (a bound, not an LRU: the next requests fill it again).
const wbCodeOriginsMax = 512

// wbCodeCSPDocs is wbCodeCSP for the stored code sections of docs, in order, followed by extra
// trusted markup (the forum's site templates). A stored doc does not change without a new
// revision or update time, so the origins of a set of docs are read once (wbCodeOriginsCache) and
// only the kits' origins, which change with the kits, are looked up per response. With extra
// markup, or a doc that was never stored, the origins are read for the response.
func (s *Server) wbCodeCSPDocs(c *gin.Context, docs []*store.WebBuilderDoc, extra string) {
	if c == nil {
		return
	}
	code := func() string {
		var b strings.Builder
		for _, d := range docs {
			b.WriteString(wbCodeSource(d))
		}
		b.WriteString(extra)
		return b.String()
	}
	keyed := extra == ""
	for _, d := range docs {
		keyed = keyed && (d == nil || wbDocKeyed(d))
	}
	if !keyed {
		s.wbCodeCSP(c, code())
		return
	}
	var key strings.Builder
	for _, d := range docs {
		if d != nil {
			key.WriteString(wbDocKey(d))
		}
		key.WriteByte(';')
	}
	if v, ok := wbCodeOriginsCache.Load(key.String()); ok {
		s.wbCodeCSPApply(c, v.(*wbCodeOriginsEntry).o)
		return
	}
	o := wbCodeOriginsOf(code())
	if wbCodeOriginsN.Add(1) > wbCodeOriginsMax {
		wbCodeOriginsCache.Clear()
		wbCodeOriginsN.Store(0)
	}
	wbCodeOriginsCache.Store(key.String(), &wbCodeOriginsEntry{o})
	s.wbCodeCSPApply(c, o)
}

// wbDocKeyed reports whether d is a stored doc (a revision and an update time), which wbDocKey
// identifies.
func wbDocKeyed(d *store.WebBuilderDoc) bool { return d.Rev > 0 && !d.UpdatedAt.IsZero() }

// wbDocKey identifies a stored doc's revision: post id, revision and update time (a publish bumps
// the revision; the time keeps two databases of one process, as in tests, apart).
func wbDocKey(d *store.WebBuilderDoc) string {
	return strconv.FormatInt(d.PostID, 10) + ":" + strconv.Itoa(d.Rev) + ":" + strconv.FormatInt(d.UpdatedAt.UnixNano(), 10)
}
