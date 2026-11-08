package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Compressed static text assets: stylesheets, scripts, SVG and JSON under the static directories
// (wp-includes, wp-admin assets, bundled themes) go out gzipped to clients that accept it, the way
// a web server's mod_deflate would serve WordPress' files. The compressed bytes and the ETag are
// computed once per file (on every request in development, where files are read from WPGO_WEB_DIR
// and change).

var gzipStaticExt = map[string]bool{".css": true, ".js": true, ".svg": true, ".json": true, ".txt": true, ".map": true}

// gzipStaticMin is the size below which compressing is not worth it.
const gzipStaticMin = 1024

var gzipStaticCache sync.Map // dir/name -> *staticFile

// staticFile is what serveGzipped computes once per embedded file: its gzipped bytes (nil when not
// worth compressing) and a strong ETag from the content hash.
type staticFile struct {
	gz   []byte
	etag string // quoted; the gzipped representation adds "-gzip"
}

// staticInfo reads and prepares a static file (cached unless files are read live from WPGO_WEB_DIR).
func (s *Server) staticInfo(fsys fs.FS, dir, name string) *staticFile {
	key := dir + "/" + name
	if v, ok := gzipStaticCache.Load(key); ok && s.cfg.WebDir == "" {
		return v.(*staticFile)
	}
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(raw)
	f := &staticFile{etag: `"` + hex.EncodeToString(sum[:12]) + `"`}
	if gzipStaticExt[path.Ext(name)] && len(raw) >= gzipStaticMin {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(raw)
		_ = zw.Close()
		f.gz = buf.Bytes()
	}
	if s.cfg.WebDir == "" {
		gzipStaticCache.Store(key, f)
	}
	return f
}

// etagMatch reports whether an If-None-Match value lists etag (weak comparison, as for GET).
func etagMatch(inm, etag string) bool {
	for _, t := range strings.Split(inm, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// serveGzipped writes name from fsys gzipped when the client accepts gzip; it reports whether it did.
// Every embedded file gets a content-hash ETag: a matching If-None-Match is answered 304 here for the
// gzipped representation and by http.FileServer (which honours a preset ETag) for the plain one.
func (s *Server) serveGzipped(c *gin.Context, fsys fs.FS, dir, name string) bool {
	f := s.staticInfo(fsys, dir, name)
	if f == nil {
		return false
	}
	ext := path.Ext(name)
	h := c.Writer.Header()
	if f.gz == nil || c.GetHeader("Range") != "" || !acceptsGzip(c.GetHeader("Accept-Encoding")) {
		h.Set("ETag", f.etag)
		if f.gz != nil {
			h.Add("Vary", "Accept-Encoding")
		}
		return false
	}
	etag := strings.TrimSuffix(f.etag, `"`) + `-gzip"`
	h.Set("ETag", etag)
	h.Add("Vary", "Accept-Encoding")
	if etagMatch(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return true
	}
	ct := mime.TypeByExtension(ext)
	if ext == ".js" {
		ct = "text/javascript; charset=utf-8"
	}
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("Content-Encoding", "gzip")
	h.Set("Content-Length", strconv.Itoa(len(f.gz)))
	c.Status(http.StatusOK)
	if c.Request.Method != http.MethodHead {
		_, _ = c.Writer.Write(f.gz)
	}
	return true
}
