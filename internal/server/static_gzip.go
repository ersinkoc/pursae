package server

import (
	"bytes"
	"compress/gzip"
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
// a web server's mod_deflate would serve WordPress' files. The compressed bytes are computed once
// per file (on every request in development, where files are read from WPGO_WEB_DIR and change).

var gzipStaticExt = map[string]bool{".css": true, ".js": true, ".svg": true, ".json": true, ".txt": true, ".map": true}

// gzipStaticMin is the size below which compressing is not worth it.
const gzipStaticMin = 1024

var gzipStaticCache sync.Map // dir/name -> []byte (nil: not worth compressing)

// serveGzipped writes name from fsys gzipped when the client accepts gzip; it reports whether it did.
func (s *Server) serveGzipped(c *gin.Context, fsys fs.FS, dir, name string) bool {
	ext := path.Ext(name)
	if !gzipStaticExt[ext] || c.GetHeader("Range") != "" || !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
		return false
	}
	key := dir + "/" + name
	var body []byte
	if v, ok := gzipStaticCache.Load(key); ok && s.cfg.WebDir == "" {
		body, _ = v.([]byte)
	} else {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return false
		}
		if len(raw) >= gzipStaticMin {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = zw.Write(raw)
			_ = zw.Close()
			body = buf.Bytes()
		}
		if s.cfg.WebDir == "" {
			gzipStaticCache.Store(key, body)
		}
	}
	if body == nil {
		return false
	}
	h := c.Writer.Header()
	ct := mime.TypeByExtension(ext)
	if ext == ".js" {
		ct = "text/javascript; charset=utf-8"
	}
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	c.Status(http.StatusOK)
	if c.Request.Method != http.MethodHead {
		_, _ = c.Writer.Write(body)
	}
	return true
}
