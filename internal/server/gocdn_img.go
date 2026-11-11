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
	"image/gif"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"wpgo/internal/gocdn"
	"wpgo/internal/media"
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

// gocdnAnimKey adds the frame budget to the cache key of animations (it decides animated or flat).
func gocdnAnimKey(anim bool, st gocdnSettings) string {
	if !anim {
		return ""
	}
	return "|f" + strconv.Itoa(st.ImgMaxFrames) + "|p" + strconv.Itoa(st.ImgMaxSource)
}

// gocdnAnimMemo remembers whether a GIF source version is animated.
var gocdnAnimMemo sync.Map

// gocdnGIFAnimated reports whether GIF data has more than one image, walking the block structure
// without decoding pixels.
func gocdnGIFAnimated(b []byte) bool { return gocdnGIFFrames(b, 2) > 1 }

// gocdnGIFFrames counts the images of GIF data, walking the block structure without decoding
// pixels; it stops counting at limit. gif.DecodeAll allocates every frame before the animation
// budget can be checked, so the count is checked first: LZW packs a uniform 50-megapixel frame
// into a few kilobytes, and a small file with thousands of them would decode to many gigabytes.
func gocdnGIFFrames(b []byte, limit int) int {
	if len(b) < 13 || string(b[:3]) != "GIF" {
		return 0
	}
	i := 13
	if b[10]&0x80 != 0 { // global colour table
		i += 3 << (int(b[10]&7) + 1)
	}
	skipSub := func() bool {
		for i < len(b) {
			n := int(b[i])
			i++
			if n == 0 {
				return true
			}
			i += n
		}
		return false
	}
	frames := 0
	for i < len(b) {
		switch b[i] {
		case 0x21: // extension
			i += 2
			if !skipSub() {
				return frames
			}
		case 0x2C: // image descriptor
			frames++
			if frames >= limit {
				return frames
			}
			if i+10 > len(b) {
				return frames
			}
			flags := b[i+9]
			i += 10
			if flags&0x80 != 0 {
				i += 3 << (int(flags&7) + 1)
			}
			i++ // LZW minimum code size
			if !skipSub() {
				return frames
			}
		default: // 0x3B trailer or garbage
			return frames
		}
	}
	return frames
}

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
	if p.Fit == "cover" && !p.Focal {
		// The attachment's focal point (media_replace.go) decides the crop unless the URL names one.
		if fi, ok := s.mediaIdx.lookup(rel); ok && fi.Focal != nil && !fi.Focal.Centered() {
			p.Focal, p.FX, p.FY = true, fi.Focal.X, fi.Focal.Y
		}
	}
	limits := gocdn.Limits{MaxW: st.ImgMaxW, MaxH: st.ImgMaxH, MaxSourcePix: int64(st.ImgMaxSource) * 1_000_000}
	if err := p.Check(limits); err != nil {
		gocdnImgError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The source and its version (for the cache key).
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
	} else {
		gocdnImgError(w, http.StatusNotFound, "not found")
		return
	}

	srcFormat := strings.TrimPrefix(strings.ToLower(path.Ext(rel)), ".")
	switch srcFormat {
	case "jpg", "jpe":
		srcFormat = "jpeg"
	}
	var src []byte
	readSrc := func(ctx context.Context) ([]byte, error) {
		if src != nil {
			return src, nil
		}
		var err error
		if local != "" {
			src, err = os.ReadFile(local)
		} else {
			src, err = s.gocdnFetchSource(ctx, st, rel)
		}
		return src, err
	}
	// The output format depends on Accept only with fm=auto; PNG/GIF sources may have alpha, which
	// only matters when no explicit format is asked and WebP is not used.
	alpha := srcFormat == "png" || srcFormat == "gif" || srcFormat == "webp"
	out := gocdn.Negotiate(p.Format, r.Header.Get("Accept"), srcFormat, st.ImgWebP, st.ImgAVIF, alpha)
	// Animated GIFs stay animations (animated WebP/AVIF where negotiated, else GIF) unless a still
	// JPEG/PNG is asked for explicitly. Whether a GIF is animated is remembered per source version, so only the first request reads it here.
	anim := false
	if srcFormat == "gif" && p.Format != "jpeg" && p.Format != "png" {
		akey := rel + "|" + version
		if v, ok := gocdnAnimMemo.Load(akey); ok {
			anim = v.(bool)
		} else if data, err := readSrc(r.Context()); err == nil {
			anim = gocdnGIFAnimated(data)
			gocdnAnimMemo.Store(akey, anim)
		}
		if anim && out != "webp" && out != "avif" {
			out = "gif"
		}
	}
	h := w.Header()
	if p.Format == "auto" || p.Format == "avif" {
		h.Add("Vary", "Accept")
	}
	sum := sha256.Sum256([]byte(rel + "|" + version + "|" + p.Canonical() + "|q" + strconv.Itoa(p.Q) + "|" + out + "|anim" + strconv.FormatBool(anim) + gocdnAnimKey(anim, st) + "|exif"))
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
		gocdnTouch(cacheFile)
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
					s.gocdnMaybePrune(st)
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
	if _, err := readSrc(r.Context()); err != nil {
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
	var buf bytes.Buffer
	animDone := false
	if anim {
		al := gocdn.AnimLimits{MaxFrames: st.ImgMaxFrames, MaxPixels: 3 * limits.MaxSourcePix}
		n := gocdnGIFFrames(src, al.MaxFrames+1)
		if n > al.MaxFrames || int64(n)*int64(cfg.Width)*int64(cfg.Height) > al.MaxPixels {
			// over the budget before decoding: flattened below
		} else if g, err := gif.DecodeAll(bytes.NewReader(src)); err == nil {
			if out == "gif" {
				if ag, err := gocdn.TransformGIF(g, p, al); err == nil {
					animDone = gif.EncodeAll(&buf, ag) == nil
				}
			} else {
				animDone = gocdn.TransformGIFTo(&buf, g, p, al, out, p.Q) == nil
			}
		}
		if !animDone { // over the frame/pixel budget: the first frame, in the negotiated format
			buf.Reset()
			h.Set("X-GoCDN-Animation", "flattened")
		}
	}
	if !animDone {
		img, _, err := image.Decode(bytes.NewReader(src))
		if err != nil {
			gocdnImgError(w, http.StatusUnsupportedMediaType, "the source is not a readable image")
			return
		}
		if srcFormat == "jpeg" { // upright like the original shows in the browser (EXIF orientation)
			img = media.Orient(img, media.JPEGOrientation(bytes.NewReader(src)))
		}
		img = gocdn.Transform(img, p)
		if err := gocdn.Encode(&buf, img, out, p.Q); err != nil {
			gocdnImgError(w, http.StatusInternalServerError, "encoding failed")
			return
		}
	}
	data := buf.Bytes()
	gocdnImgStore(cacheFile, data)
	s.gocdnMaybePrune(st)
	if cl != nil {
		go func(data []byte) {
			o := gocdnPutOptions(st, "variant"+gocdn.Ext(out))
			o.ContentType = gocdn.ContentType(out)
			_ = cl.Put(context.Background(), bucketKey, data, o)
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

// gocdnImgStore caches a variant. Concurrent misses for one variant each write their own temp file:
// a shared one could be truncated by the second writer after the first renamed it into place, and
// a cache hit is served as immutable.
func gocdnImgStore(file string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return
	}
	if err := gocdnWriteFileAtomic(file, bytes.NewReader(data), -1); err != nil {
		slog.Warn("gocdn: caching an image variant", "file", file, "err", err)
	}
}

// gocdnWriteFileAtomic writes r to a temp file unique to this call next to file and renames it into
// place; with limit >= 0 at most limit bytes are read.
func gocdnWriteFileAtomic(file string, r io.Reader, limit int64) error {
	f, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".tmp*")
	if err != nil {
		return err
	}
	if limit >= 0 {
		r = io.LimitReader(r, limit)
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), file)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
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
