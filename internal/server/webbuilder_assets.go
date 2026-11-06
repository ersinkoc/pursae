package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// The built editor and the public site runtime of Pursae Web Builder (web/web-builder, committed
// output of `make builder`). Every file in the bundle directories carries a content hash in its
// name (Vite), so those files are cached for a year as immutable; manifest.json names the entry
// files. Both prefixes answer 404 while the plugin is off.
//
//	/wp-content/plugins/web-builder/dist/editor/        web/web-builder/editor/
//	/wp-content/plugins/web-builder/dist/editor-noai/   web/web-builder/editor-noai/ (when built)
//	/wp-content/web-builder/site/                       web/web-builder/site/ (runtime + base CSS)
//
// None of them overlaps the old builder's /wp-content/plugins/web-builder/assets/ or the
// /wp-content/plugins/:slug/assets/ and /wp-content/uploads/ catch-alls.

func (s *Server) wbRegisterAssets(r *gin.Engine) {
	for _, a := range []struct{ prefix, dir string }{
		{webbuilder.EditorAssetsPath, webbuilder.BundleEditor},
		{webbuilder.EditorNoAIAssetsPath, webbuilder.BundleEditorNoAI},
		{webbuilder.SiteAssetsPath, webbuilder.BundleSite},
	} {
		s.static(r, strings.TrimSuffix(a.prefix, "/"), webbuilder.EmbedDir+"/"+a.dir, s.wbOnly(), wbAssetHeaders)
	}
}

// wbHashedName matches Vite's hashed file names: "index-r47ukO3c.js", "runtime.BUPc1PCe.js",
// "inter-latin-400-normal-C38fXH4l.woff2", "wb.4d16e08a.css".
var wbHashedName = regexp.MustCompile(`[.-][A-Za-z0-9_-]{8}\.(?:js|mjs|css|woff2?|ttf|otf|svg|png|jpe?g|gif|webp|avif|map|json)$`)

// wbImmutable is the Cache-Control of hashed bundle files.
const wbImmutable = "public, max-age=31536000, immutable"

// wbFontTypes are content types Go's built-in table lacks (minimal containers have no
// /etc/mime.types): without them the editor's fonts would go out as application/octet-stream.
var wbFontTypes = map[string]string{".woff2": "font/woff2", ".woff": "font/woff", ".ttf": "font/ttf", ".otf": "font/otf"}

// wbAssetHeaders runs before s.static's handler: it sets the font content types and makes a
// successful answer for a hashed file immutable (s.static's own week-long Cache-Control is
// replaced when the status is written).
func wbAssetHeaders(c *gin.Context) {
	name := path.Base(c.Param("filepath"))
	if ct, ok := wbFontTypes[strings.ToLower(path.Ext(name))]; ok {
		c.Header("Content-Type", ct)
	}
	if wbHashedName.MatchString(name) {
		c.Writer = &wbCacheWriter{ResponseWriter: c.Writer, cacheControl: wbImmutable}
	}
	c.Next()
}

// wbCacheWriter sets Cache-Control when a 200, 206 or 304 status is written.
type wbCacheWriter struct {
	gin.ResponseWriter
	cacheControl string
	done         bool
}

func (w *wbCacheWriter) apply(code int) {
	if w.done {
		return
	}
	w.done = true
	if code == http.StatusOK || code == http.StatusPartialContent || code == http.StatusNotModified {
		w.Header().Set("Cache-Control", w.cacheControl)
	}
}

func (w *wbCacheWriter) WriteHeader(code int) {
	w.apply(code)
	w.ResponseWriter.WriteHeader(code)
}

func (w *wbCacheWriter) WriteHeaderNow() {
	w.apply(w.ResponseWriter.Status())
	w.ResponseWriter.WriteHeaderNow()
}

func (w *wbCacheWriter) Write(b []byte) (int, error) {
	w.apply(w.ResponseWriter.Status())
	return w.ResponseWriter.Write(b)
}

func (w *wbCacheWriter) WriteString(str string) (int, error) {
	w.apply(w.ResponseWriter.Status())
	return w.ResponseWriter.WriteString(str)
}

// --- manifest ----------------------------------------------------------------------------------

var wbManifestCache struct {
	once sync.Once
	m    *webbuilder.Manifest
	err  error
}

// wbManifest is web/web-builder/manifest.json: read once from the embedded files, or on every call
// in development (WPGO_WEB_DIR), where `make builder-watch` rewrites it.
func (s *Server) wbManifest() (*webbuilder.Manifest, error) {
	if s.cfg.WebDir != "" {
		return wbReadManifest(s.web)
	}
	wbManifestCache.once.Do(func() {
		wbManifestCache.m, wbManifestCache.err = wbReadManifest(s.web)
	})
	return wbManifestCache.m, wbManifestCache.err
}

func wbReadManifest(fsys fs.FS) (*webbuilder.Manifest, error) {
	name := webbuilder.EmbedDir + "/" + webbuilder.ManifestFile
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("web builder: %s: %w", name, err)
	}
	m := &webbuilder.Manifest{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("web builder: %s: %w", name, err)
	}
	if m.Editor.JS == "" && m.EditorNoAI.JS == "" {
		return nil, fmt.Errorf("web builder: %s names no editor bundle", name)
	}
	for _, p := range append(append([]string{m.Editor.JS, m.EditorNoAI.JS, m.Site.Runtime, m.Site.CSS}, m.Editor.CSS...), m.EditorNoAI.CSS...) {
		if p != "" && !wbSafeAssetPath(p) {
			return nil, fmt.Errorf("web builder: %s: bad asset path %q", name, p)
		}
	}
	return m, nil
}

// wbSafeAssetPath accepts a relative path inside a bundle directory, nothing that could leave it
// or break out of an HTML attribute.
func wbSafeAssetPath(p string) bool {
	if !fs.ValidPath(p) || strings.ContainsAny(p, "\"'<>&?#\\ ") {
		return false
	}
	return true
}

// errWBNoBundle: the manifest names no editor bundle the server may serve.
var errWBNoBundle = errors.New("web builder: no editor bundle")

// wbEditorBundle picks the editor bundle and its URL prefix. AI is out of scope for now, so the
// no-AI build wins when it is built; the single editor/ build of today has its AI modules stubbed
// (builder/vite/noai.ts) and reports AI disabled to the host.
func wbEditorBundle(m *webbuilder.Manifest) (prefix string, entry webbuilder.ManifestEntry, err error) {
	switch {
	case m.EditorNoAI.JS != "":
		return webbuilder.EditorNoAIAssetsPath, m.EditorNoAI, nil
	case m.Editor.JS != "":
		return webbuilder.EditorAssetsPath, m.Editor, nil
	}
	return "", webbuilder.ManifestEntry{}, errWBNoBundle
}
