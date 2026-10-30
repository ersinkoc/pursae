package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"wpgo/internal/gocdn"
)

// The image service: /cdn-img/<path in uploads>?w=&h=&fit=&q=&fm=&s= (and the same parameters on
// /wp-content/uploads/<path>) resizes, crops and converts an uploaded image, local or offloaded.
// fm=auto (the default) sends WebP to browsers that accept it, with Vary: Accept. With URL signing
// on, s must be the HMAC of the path and the canonical parameters under the site's image key;
// unsigned or tampered URLs get 403. Sizes are capped, sources over the pixel limit are refused and
// at most one transformation per CPU runs at a time. Variants are cached on disk
// (<data>/gocdn-cache) and optionally in the bucket (gocdn-cache/…), and served with an ETag and
// an immutable Cache-Control.

var gocdnImgSem = make(chan struct{}, max(2, runtime.NumCPU()))

const gocdnImgMaxSourceBytes = 64 << 20

// gocdnImgSig is the signature of a path and canonical parameter string.
func gocdnImgSig(key []byte, rel, canonical string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(rel + "?" + canonical))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))[:22]
}

// gocdnSignedPath is the service path of an uploads path with parameters (signed when sign).
func gocdnSignedPath(key []byte, rel string, p gocdn.ImageParams, sign bool) string {
	canon := p.Canonical()
	q := canon
	if sign {
		if q != "" {
			q += "&"
		}
		q += "s=" + gocdnImgSig(key, rel, canon)
	}
	out := gocdnImgPath + strings.TrimPrefix(gocdn.EscapePath("/"+rel), "/")
	if q != "" {
		out += "?" + q
	}
	return out
}

// gocdnImageURL is the absolute image service URL of an uploads path, for themes and templates.
func (s *Server) gocdnImageURL(ctx context.Context, rel string, p gocdn.ImageParams) string {
	st := s.gocdnLoad()
	return strings.TrimRight(s.links(ctx).HomeURL("/"), "/") + gocdnSignedPath(s.gocdnImgKey(st), rel, p, st.ImgSign)
}

func gocdnImgError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg+"\n")
}

// gocdnCacheDir is where processed variants are kept.
func (s *Server) gocdnCacheDir() string {
	return filepath.Join(filepath.Dir(filepath.Clean(s.cfg.UploadsDir)), "gocdn-cache")
}

// gocdnImage serves one image service request.
func (s *Server) gocdnImage(w http.ResponseWriter, r *http.Request, st gocdnSettings, rel string) {
	clean := path.Clean("/" + rel)
	if clean == "/" || strings.Contains(clean, "..") || s.gocdnLocalPath(rel) == "" {
		gocdnImgError(w, http.StatusBadRequest, "bad path")
		return
	}
	rel = clean[1:]
	if !gocdnImageExt[strings.ToLower(path.Ext(rel))] {
		gocdnImgError(w, http.StatusBadRequest, "the image service handles JPEG, PNG, GIF and WebP files")
		return
	}
	q := r.URL.Query()
	p, err := gocdn.ParseImageParams(q)
	if err != nil {
		gocdnImgError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.Q == 0 {
		p.Q = st.ImgQuality
	}
	canon, _ := gocdn.ParseImageParams(q) // what was signed: the parameters as given
	if st.ImgSign {
		sig := q.Get("s")
		if sig == "" || !hmac.Equal([]byte(sig), []byte(gocdnImgSig(s.gocdnImgKey(st), rel, canon.Canonical()))) {
			gocdnImgError(w, http.StatusForbidden, "invalid or missing signature")
			return
		}
	}
	limits := gocdn.Limits{MaxW: st.ImgMaxW, MaxH: st.ImgMaxH, MaxSourcePix: int64(st.ImgMaxSource) * 1_000_000}
	if err := p.Check(limits); err != nil {
		gocdnImgError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The source and its version (for the cache key).
	var src []byte
	var version string
	local := s.gocdnLocalPath(rel)
	if fi, err := os.Stat(local); err == nil && fi.Mode().IsRegular() {
		if fi.Size() > gocdnImgMaxSourceBytes {
			gocdnImgError(w, http.StatusRequestEntityTooLarge, "source file too large")
			return
		}
		version = "l" + strconv.FormatInt(fi.Size(), 10) + "-" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	} else if it := s.gocdnItemByPath(r.Context(), rel); it != nil {
		version = "r" + strconv.FormatInt(it.ID, 10) + "-" + strconv.FormatInt(it.Size, 10) + "-" + it.Key
		local = ""
		_ = it
	} else {
		gocdnImgError(w, http.StatusNotFound, "not found")
		return
	}

	srcFormat := strings.TrimPrefix(strings.ToLower(path.Ext(rel)), ".")
	switch srcFormat {
	case "jpg", "jpe":
		srcFormat = "jpeg"
	}
	// The output format depends on Accept only with fm=auto; PNG/GIF sources may have alpha, which
	// only matters when no explicit format is asked and WebP is not used.
	alpha := srcFormat == "png" || srcFormat == "gif" || srcFormat == "webp"
	out := gocdn.Negotiate(p.Format, r.Header.Get("Accept"), srcFormat, st.ImgWebP, alpha)
	h := w.Header()
	if p.Format == "auto" || p.Format == "avif" {
		h.Add("Vary", "Accept")
	}
	sum := sha256.Sum256([]byte(rel + "|" + version + "|" + p.Canonical() + "|q" + strconv.Itoa(p.Q) + "|" + out))
	key := hex.EncodeToString(sum[:16])
	etag := `"` + key + `"`
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if inm := r.Header.Get("If-None-Match"); inm != "" && (inm == etag || inm == "W/"+etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	cacheFile := filepath.Join(s.gocdnCacheDir(), key[:2], key+gocdn.Ext(out))
	if data, err := os.ReadFile(cacheFile); err == nil {
		gocdnImgWrite(w, r, out, data, "disk")
		return
	}
	bucketKey := "gocdn-cache/" + key[:2] + "/" + key + gocdn.Ext(out)
	var cl *gocdn.Client
	if st.ImgBucket {
		if c, err := s.gocdnClient(st); err == nil {
			cl = c
			if rc, _, err := c.Get(r.Context(), bucketKey); err == nil {
				data, rerr := io.ReadAll(io.LimitReader(rc, gocdnImgMaxSourceBytes))
				rc.Close()
				if rerr == nil {
					gocdnImgStore(cacheFile, data)
					gocdnImgWrite(w, r, out, data, "bucket")
					return
				}
			}
		}
	}

	select {
	case gocdnImgSem <- struct{}{}:
		defer func() { <-gocdnImgSem }()
	case <-r.Context().Done():
		return
	}
	if local != "" {
		src, err = os.ReadFile(local)
	} else {
		src, err = s.gocdnFetchSource(r.Context(), st, rel)
	}
	if err != nil {
		gocdnImgError(w, http.StatusBadGateway, "the source image cannot be read")
		return
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		gocdnImgError(w, http.StatusUnsupportedMediaType, "the source is not a readable image")
		return
	}
	if err := limits.CheckSource(cfg); err != nil {
		gocdnImgError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		gocdnImgError(w, http.StatusUnsupportedMediaType, "the source is not a readable image")
		return
	}
	img = gocdn.Transform(img, p)
	var buf bytes.Buffer
	if err := gocdn.Encode(&buf, img, out, p.Q); err != nil {
		gocdnImgError(w, http.StatusInternalServerError, "encoding failed")
		return
	}
	data := buf.Bytes()
	gocdnImgStore(cacheFile, data)
	if cl != nil {
		go func(data []byte) {
			_ = cl.Put(context.Background(), bucketKey, data, gocdn.PutOptions{ContentType: gocdn.ContentType(out), CacheControl: "public, max-age=31536000, immutable"})
		}(append([]byte(nil), data...))
	}
	gocdnImgWrite(w, r, out, data, "miss")
}

// gocdnFetchSource reads an offloaded source image from the bucket.
func (s *Server) gocdnFetchSource(ctx context.Context, st gocdnSettings, rel string) ([]byte, error) {
	it := s.gocdnItemByPath(ctx, rel)
	if it == nil {
		return nil, os.ErrNotExist
	}
	cl, err := s.gocdnItemClient(st, it)
	if err != nil {
		return nil, err
	}
	rc, _, err := cl.Get(ctx, it.Key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, gocdnImgMaxSourceBytes+1))
	if err == nil && len(data) > gocdnImgMaxSourceBytes {
		return nil, errors.New("source file too large")
	}
	return data, err
}

func gocdnImgStore(file string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return
	}
	tmp := file + ".tmp" + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, file)
	}
}

func gocdnImgWrite(w http.ResponseWriter, r *http.Request, format string, data []byte, cache string) {
	h := w.Header()
	h.Set("Content-Type", gocdn.ContentType(format))
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("X-GoCDN-Cache", cache)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// gocdnPurgeImageCache empties the local variant cache.
func (s *Server) gocdnPurgeImageCache() error {
	return os.RemoveAll(s.gocdnCacheDir())
}
