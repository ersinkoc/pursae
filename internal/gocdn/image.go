package gocdn

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
)

// The image service's transformations: resize (w, h), fit (inside, cover, fill), quality (q) and
// output format (fm: auto, avif, webp, jpeg, png, gif). WebP is encoded by libwebp transpiled to
// pure Go (github.com/gen2brain/webp, no cgo); AVIF by libavif + aom compiled to WebAssembly and
// run by wazero (github.com/gen2brain/avif, no cgo). Both use a system library instead when one is
// installed (purego, still no cgo). AVIF encoding is several times slower than WebP, so it is a
// separate setting; animated GIFs stay animated GIFs (see TransformGIF).

// ImageParams are the parameters of one image service request.
type ImageParams struct {
	W, H   int
	Fit    string // inside (default), cover, fill
	Q      int    // 1–100, 0 = the site's default
	Format string // auto (default), webp, jpeg, png, gif
}

// Limits keep the service from being used to burn CPU or memory.
type Limits struct {
	MaxW, MaxH   int   // largest output size
	MaxSourcePix int64 // largest source image (width × height) it will decode
}

// DefaultLimits are used when the settings leave a limit at 0.
var DefaultLimits = Limits{MaxW: 4096, MaxH: 4096, MaxSourcePix: 60_000_000}

var (
	ErrBadParam = errors.New("invalid image parameter")
	ErrTooLarge = errors.New("requested image is too large")
)

var fitAliases = map[string]string{"": "inside", "inside": "inside", "contain": "inside", "scale-down": "inside", "max": "inside",
	"cover": "cover", "crop": "cover", "fill": "fill", "scale": "fill"}

var formatAliases = map[string]string{"": "auto", "auto": "auto", "webp": "webp", "jpeg": "jpeg", "jpg": "jpeg", "png": "png", "gif": "gif",
	"avif": "avif"}

// ParseImageParams reads w, h, fit, q and fm (width/height/quality/format are accepted too).
func ParseImageParams(q url.Values) (ImageParams, error) {
	get := func(names ...string) string {
		for _, n := range names {
			if v := q.Get(n); v != "" {
				return v
			}
		}
		return ""
	}
	var p ImageParams
	num := func(v string, lo, hi int) (int, error) {
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			return 0, fmt.Errorf("%w: %q", ErrBadParam, v)
		}
		return n, nil
	}
	var err error
	if p.W, err = num(get("w", "width"), 1, 1<<16); err != nil {
		return p, err
	}
	if p.H, err = num(get("h", "height"), 1, 1<<16); err != nil {
		return p, err
	}
	if p.Q, err = num(get("q", "quality"), 1, 100); err != nil {
		return p, err
	}
	fit, ok := fitAliases[strings.ToLower(get("fit"))]
	if !ok {
		return p, fmt.Errorf("%w: fit", ErrBadParam)
	}
	p.Fit = fit
	fm, ok := formatAliases[strings.ToLower(get("fm", "format", "f"))]
	if !ok {
		return p, fmt.Errorf("%w: fm", ErrBadParam)
	}
	p.Format = fm
	return p, nil
}

// HasImageParams reports whether a query asks for a transformation.
func HasImageParams(q url.Values) bool {
	for _, k := range []string{"w", "h", "fit", "q", "fm", "f", "width", "height", "quality", "format"} {
		if q.Has(k) {
			return true
		}
	}
	return false
}

// Canonical is the parameters in a fixed order, the part of the URL that is signed and the
// variant cache key.
func (p ImageParams) Canonical() string {
	var parts []string
	if p.W > 0 {
		parts = append(parts, "w="+strconv.Itoa(p.W))
	}
	if p.H > 0 {
		parts = append(parts, "h="+strconv.Itoa(p.H))
	}
	if p.Fit != "" && p.Fit != "inside" {
		parts = append(parts, "fit="+p.Fit)
	}
	if p.Q > 0 {
		parts = append(parts, "q="+strconv.Itoa(p.Q))
	}
	if p.Format != "" && p.Format != "auto" {
		parts = append(parts, "fm="+p.Format)
	}
	return strings.Join(parts, "&")
}

// Check applies the limits to the requested size.
func (p ImageParams) Check(l Limits) error {
	if l.MaxW <= 0 {
		l.MaxW = DefaultLimits.MaxW
	}
	if l.MaxH <= 0 {
		l.MaxH = DefaultLimits.MaxH
	}
	if p.W > l.MaxW || p.H > l.MaxH {
		return fmt.Errorf("%w: at most %d×%d", ErrTooLarge, l.MaxW, l.MaxH)
	}
	return nil
}

// CheckSource refuses sources that would take too much memory to decode.
func (l Limits) CheckSource(cfg image.Config) error {
	max := l.MaxSourcePix
	if max <= 0 {
		max = DefaultLimits.MaxSourcePix
	}
	if int64(cfg.Width)*int64(cfg.Height) > max {
		return fmt.Errorf("%w: the source image has more than %d pixels", ErrTooLarge, max)
	}
	return nil
}

// WebPAvailable reports whether WebP output can be produced (always, with the transpiled libwebp).
func WebPAvailable() bool { return true }

// AVIFAvailable reports whether AVIF output can be produced (always: the WebAssembly libavif is
// embedded).
func AVIFAvailable() bool { return true }

// AVIFSpeed is the aom encoder speed used (0 slowest/smallest … 10 fastest). 8 keeps a 1000 px
// photo well under a second per CPU while still beating WebP's size.
var AVIFSpeed = 8

// Negotiate picks the output format: an explicit fm wins (avif falls back to webp, then the
// source format, when AVIF is not allowed), auto uses the browser's Accept header (AVIF > WebP >
// source format) for the formats allowed; otherwise the source format (other source formats become
// jpeg, or png with alpha).
func Negotiate(fm, accept, srcFormat string, allowWebP, allowAVIF bool, hasAlpha bool) string {
	src := srcFormat
	switch src {
	case "jpeg", "png", "gif":
	default:
		if hasAlpha {
			src = "png"
		} else {
			src = "jpeg"
		}
	}
	switch fm {
	case "webp":
		return "webp"
	case "jpeg", "png", "gif":
		return fm
	case "avif":
		if allowAVIF && AVIFAvailable() {
			return "avif"
		}
		if allowWebP && WebPAvailable() {
			return "webp"
		}
		return src
	}
	if allowAVIF && AVIFAvailable() && AcceptsType(accept, "image/avif") {
		return "avif"
	}
	if allowWebP && WebPAvailable() && AcceptsType(accept, "image/webp") {
		return "webp"
	}
	return src
}

// AcceptsType reports whether an Accept header lists a type with q > 0 (wildcards do not count:
// browsers send */* even when they cannot show the format).
func AcceptsType(accept, typ string) bool {
	for _, part := range strings.Split(accept, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), typ) {
			continue
		}
		if k, v, ok := strings.Cut(strings.TrimSpace(params), "="); ok && strings.TrimSpace(k) == "q" {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// ContentType is the MIME type of an output format.
func ContentType(format string) string {
	switch format {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "avif":
		return "image/avif"
	}
	return "application/octet-stream"
}

// Ext is the file extension of an output format.
func Ext(format string) string {
	if format == "jpeg" {
		return ".jpg"
	}
	return "." + format
}

// OutputSize computes the output dimensions and the source rectangle to read for p.
func OutputSize(sw, sh int, p ImageParams) (w, h int, src image.Rectangle) {
	src = image.Rect(0, 0, sw, sh)
	fit := p.Fit
	if p.W == 0 || p.H == 0 {
		fit = "inside"
	}
	switch fit {
	case "fill":
		w, h = min(p.W, sw), min(p.H, sh)
		return max(1, w), max(1, h), src
	case "cover":
		w, h = p.W, p.H
		if w > sw || h > sh { // never upscale: shrink the box to fit the source, keeping its ratio
			r := min(float64(sw)/float64(w), float64(sh)/float64(h))
			w, h = max(1, int(float64(w)*r)), max(1, int(float64(h)*r))
		}
		target := float64(w) / float64(h)
		if float64(sw)/float64(sh) > target {
			cw := int(float64(sh)*target + 0.5)
			x := (sw - cw) / 2
			src = image.Rect(x, 0, x+cw, sh)
		} else {
			ch := int(float64(sw)/target + 0.5)
			y := (sh - ch) / 2
			src = image.Rect(0, y, sw, y+ch)
		}
		return w, h, src
	}
	mw, mh := p.W, p.H
	if mw == 0 {
		mw = sw
	}
	if mh == 0 {
		mh = sh
	}
	if sw <= mw && sh <= mh {
		return sw, sh, src
	}
	r := min(float64(mw)/float64(sw), float64(mh)/float64(sh))
	return max(1, int(float64(sw)*r+0.5)), max(1, int(float64(sh)*r+0.5)), src
}

// Transform resizes/crops img for p (a no-op when the size does not change).
func Transform(img image.Image, p ImageParams) image.Image {
	b := img.Bounds()
	w, h, src := OutputSize(b.Dx(), b.Dy(), p)
	src = src.Add(b.Min)
	if w == b.Dx() && h == b.Dy() && src == b {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, src, draw.Over, nil)
	return dst
}

// HasAlpha reports whether an image may have transparent pixels.
func HasAlpha(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	return true
}

// Encode writes img in format at quality q (1–100; jpeg, webp and avif).
func Encode(w io.Writer, img image.Image, format string, q int) error {
	if q <= 0 || q > 100 {
		q = 82
	}
	switch format {
	case "webp":
		return webp.Encode(w, img, webp.Options{Quality: q, Method: 4})
	case "avif":
		// AVIF quality runs lower than JPEG's for the same look: map 82 → ~62.
		aq := max(1, min(100, q*3/4))
		return avif.Encode(w, img, avif.Options{Quality: aq, QualityAlpha: aq, Speed: AVIFSpeed, ChromaSubsampling: image.YCbCrSubsampleRatio420})
	case "jpeg":
		if HasAlpha(img) { // JPEG has no alpha: flatten on white like the browser would show it
			b := img.Bounds()
			flat := image.NewRGBA(b)
			draw.Draw(flat, b, image.NewUniform(color.White), image.Point{}, draw.Src)
			draw.Draw(flat, b, img, b.Min, draw.Over)
			img = flat
		}
		return jpeg.Encode(w, img, &jpeg.Options{Quality: q})
	case "gif":
		return gif.Encode(w, img, nil)
	case "png":
		return (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(w, img)
	}
	return fmt.Errorf("unsupported output format %q", format)
}
