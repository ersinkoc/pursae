// Package media stores uploads the way WordPress does: year/month folders, unique
// file names, the registered image sizes and the "-scaled" big image handling.
package media

import (
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"wpgo/internal/wp"
)

// MimeTypes mirrors wp_get_mime_types() for the types allowed to regular users
// (get_allowed_mime_types() without unfiltered_upload).
var MimeTypes = map[string]string{
	"jpg|jpeg|jpe": "image/jpeg", "gif": "image/gif", "png": "image/png", "bmp": "image/bmp", "tiff|tif": "image/tiff",
	"webp": "image/webp", "avif": "image/avif", "ico": "image/x-icon", "heic": "image/heic", "heif": "image/heif",
	"svg":     "image/svg+xml", // sanitized on upload (formats.go)
	"asf|asx": "video/x-ms-asf", "wmv": "video/x-ms-wmv", "wmx": "video/x-ms-wmx", "wm": "video/x-ms-wm", "avi": "video/avi",
	"divx": "video/divx", "flv": "video/x-flv", "mov|qt": "video/quicktime", "mpeg|mpg|mpe": "video/mpeg", "mp4|m4v": "video/mp4",
	"ogv": "video/ogg", "webm": "video/webm", "mkv": "video/x-matroska", "3gp|3gpp": "video/3gpp", "3g2|3gp2": "video/3gpp2",
	"txt|asc|c|cc|h|srt": "text/plain", "csv": "text/csv", "tsv": "text/tab-separated-values", "ics": "text/calendar",
	"rtx": "text/richtext", "css": "text/css", "vtt": "text/vtt", "dfxp": "application/ttaf+xml",
	"mp3|m4a|m4b": "audio/mpeg", "aac": "audio/aac", "ra|ram": "audio/x-realaudio", "wav|x-wav": "audio/wav", "ogg|oga": "audio/ogg",
	"flac": "audio/flac", "mid|midi": "audio/midi", "wma": "audio/x-ms-wma", "wax": "audio/x-ms-wax", "mka": "audio/x-matroska",
	"rtf": "application/rtf", "pdf": "application/pdf", "class": "application/java", "tar": "application/x-tar",
	"zip": "application/zip", "gz|gzip": "application/x-gzip", "rar": "application/rar", "7z": "application/x-7z-compressed",
	"psd": "application/octet-stream", "xcf": "application/octet-stream",
	"doc": "application/msword", "pot|pps|ppt": "application/vnd.ms-powerpoint", "wri": "application/vnd.ms-write",
	"xla|xls|xlt|xlw": "application/vnd.ms-excel", "mdb": "application/vnd.ms-access", "mpp": "application/vnd.ms-project",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"docm": "application/vnd.ms-word.document.macroEnabled.12",
	"dotx": "application/vnd.openxmlformats-officedocument.wordprocessingml.template",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"odt":  "application/vnd.oasis.opendocument.text", "odp": "application/vnd.oasis.opendocument.presentation",
	"ods": "application/vnd.oasis.opendocument.spreadsheet", "odg": "application/vnd.oasis.opendocument.graphics",
	"key": "application/vnd.apple.keynote", "numbers": "application/vnd.apple.numbers", "pages": "application/vnd.apple.pages",
}

// MimeFor returns the allowed MIME type for a file name, or "" when the extension is not allowed.
func MimeFor(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return ""
	}
	for exts, mime := range MimeTypes {
		for _, e := range strings.Split(exts, "|") {
			if e == ext {
				return mime
			}
		}
	}
	return ""
}

var ErrType = errors.New("Sorry, you are not allowed to upload this file type.")

// SanitizeFileName is sanitize_file_name(): lowercase-safe characters, dashes, one extension.
func SanitizeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	base := strings.TrimSuffix(name, filepath.Ext(name))
	base = wp.RemoveAccents(base)
	var sb strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			sb.WriteRune(r)
		case r == ' ' || r == '+' || r == '%':
			sb.WriteRune('-')
		}
	}
	base = strings.Trim(strings.ReplaceAll(sb.String(), "--", "-"), "-._")
	if base == "" {
		base = "unnamed-file"
	}
	return base + ext
}

// Stored describes a saved upload.
type Stored struct {
	RelPath  string // e.g. 2026/10/photo.jpg, relative to the uploads dir
	Size     int64
	Mime     string
	Original string // name before sanitizing, used for the title
}

// Save writes r into the uploads directory under year/month (when dated) with a unique name.
func Save(uploadsDir string, dated bool, now time.Time, filename string, r io.Reader) (*Stored, error) {
	mime := MimeFor(filename)
	if mime == "" {
		return nil, ErrType
	}
	sub := ""
	if dated {
		sub = fmt.Sprintf("%04d/%02d", now.Year(), int(now.Month()))
	}
	dir := filepath.Join(uploadsDir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := uniqueName(dir, SanitizeFileName(filename))
	dst := filepath.Join(dir, name)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && mime == "image/svg+xml" && n <= 8<<20 {
		err = sanitizeSVGFile(dst)
		if st, serr := os.Stat(dst); err == nil && serr == nil {
			n = st.Size()
		}
	} else if err == nil && mime == "image/svg+xml" {
		err = ErrSVG
	}
	if err != nil {
		os.Remove(dst)
		return nil, err
	}
	return &Stored{RelPath: filepath.ToSlash(filepath.Join(sub, name)), Size: n, Mime: mime, Original: filename}, nil
}

// uniqueName is wp_unique_filename(): name.ext, name-1.ext, name-2.ext …, also avoiding
// collisions with generated size variants of other files.
func uniqueName(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	cand := name
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, cand)); os.IsNotExist(err) {
			return cand
		}
		cand = base + "-" + strconv.Itoa(i) + ext
	}
}

// Size is a registered image size (add_image_size()).
type Size struct {
	Name          string
	Width, Height int
	Crop          bool
}

// Variant is one generated sub-size.
type Variant struct {
	Name          string
	File          string // file name only, in the same folder as the original
	Width, Height int
	Mime          string
	Size          int64
}

// ImageInfo is the result of processing an uploaded image (wp_create_image_subsizes()).
type ImageInfo struct {
	Width, Height int
	RelPath       string // may change to the -scaled file
	OriginalFile  string // set when a -scaled copy replaced the original as the main file
	Variants      []Variant
	FileSize      int64
}

// BigImageThreshold is the big_image_size_threshold default.
const BigImageThreshold = 2560

// ProcessImage reads an uploaded image, creates the -scaled main file when needed and every
// registered size that is smaller than the original. Formats Go can't encode (WebP, AVIF, …)
// keep their original file only.
func ProcessImage(uploadsDir, relPath string, sizes []Size) (*ImageInfo, error) {
	full := filepath.Join(uploadsDir, filepath.FromSlash(relPath))
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	img, format, err := image.Decode(f)
	f.Close()
	if err != nil {
		// Formats that cannot be decoded here are still measured: SVG from its root element,
		// AVIF/HEIC from their "ispe" box. They get no sub-sizes.
		info := &ImageInfo{RelPath: relPath}
		if st, serr := os.Stat(full); serr == nil {
			info.FileSize = st.Size()
		}
		switch strings.ToLower(filepath.Ext(full)) {
		case ".svg":
			if data, rerr := os.ReadFile(full); rerr == nil {
				info.Width, info.Height = svgDimensions(data)
			}
			return info, nil
		case ".avif", ".heic", ".heif":
			if w, h, ok := isoDimensions(full); ok {
				info.Width, info.Height = w, h
			}
			return info, nil
		}
		return nil, err
	}
	b := img.Bounds()
	info := &ImageInfo{Width: b.Dx(), Height: b.Dy(), RelPath: relPath}
	if st, err := os.Stat(full); err == nil {
		info.FileSize = st.Size()
	}
	dir := filepath.Dir(full)
	ext := filepath.Ext(full)
	base := strings.TrimSuffix(filepath.Base(full), ext)
	// Sub-sizes keep the format WordPress Go can write: WebP/BMP/TIFF become JPEG (PNG with
	// transparency) and the original stays as uploaded, with no "-scaled" copy.
	out := outputFormat(format, img)
	native := out == format
	outExt := ext
	if !native {
		outExt = formatExt(out)
	}

	src := img
	if native && (info.Width > BigImageThreshold || info.Height > BigImageThreshold) {
		w, h := fitWithin(info.Width, info.Height, BigImageThreshold, BigImageThreshold)
		scaled := resize(img, w, h, false)
		name := base + "-scaled" + ext
		if err := encode(filepath.Join(dir, name), scaled, format); err == nil {
			info.OriginalFile = filepath.Base(full)
			info.RelPath = filepath.ToSlash(filepath.Join(filepath.Dir(relPath), name))
			info.Width, info.Height = w, h
			if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
				info.FileSize = st.Size()
			}
			src = scaled
		}
	}

	for _, sz := range sizes {
		w, h, ok := resizeDims(info.Width, info.Height, sz.Width, sz.Height, sz.Crop)
		if !ok {
			continue
		}
		resized := resize(src, w, h, sz.Crop)
		name := fmt.Sprintf("%s-%dx%d%s", base, w, h, outExt)
		path := filepath.Join(dir, name)
		if err := encode(path, resized, out); err != nil {
			continue
		}
		v := Variant{Name: sz.Name, File: name, Width: w, Height: h, Mime: "image/" + out}
		if st, err := os.Stat(path); err == nil {
			v.Size = st.Size()
		}
		info.Variants = append(info.Variants, v)
	}
	return info, nil
}

// resizeDims is image_resize_dimensions(): no upscaling, proportional fit or center crop.
func resizeDims(origW, origH, maxW, maxH int, crop bool) (int, int, bool) {
	if maxW <= 0 && maxH <= 0 {
		return 0, 0, false
	}
	if crop {
		w, h := maxW, maxH
		if w <= 0 {
			w = origW
		}
		if h <= 0 {
			h = origH
		}
		if w > origW {
			w = origW
		}
		if h > origH {
			h = origH
		}
		if w == origW && h == origH {
			return 0, 0, false
		}
		return w, h, true
	}
	if maxW <= 0 {
		maxW = origW
	}
	if maxH <= 0 {
		maxH = origH
	}
	w, h := fitWithin(origW, origH, maxW, maxH)
	if w >= origW && h >= origH {
		return 0, 0, false
	}
	return w, h, true
}

// fitWithin is wp_constrain_dimensions().
func fitWithin(w, h, maxW, maxH int) (int, int) {
	if w <= maxW && h <= maxH {
		return w, h
	}
	rw := float64(maxW) / float64(w)
	rh := float64(maxH) / float64(h)
	r := rw
	if rh < rw {
		r = rh
	}
	nw := int(float64(w)*r + 0.5)
	nh := int(float64(h)*r + 0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}

func resize(src image.Image, w, h int, crop bool) image.Image {
	sb := src.Bounds()
	srcRect := sb
	if crop {
		// Center crop to the target aspect ratio first.
		sw, sh := sb.Dx(), sb.Dy()
		targetRatio := float64(w) / float64(h)
		if float64(sw)/float64(sh) > targetRatio {
			cw := int(float64(sh) * targetRatio)
			x := sb.Min.X + (sw-cw)/2
			srcRect = image.Rect(x, sb.Min.Y, x+cw, sb.Max.Y)
		} else {
			ch := int(float64(sw) / targetRatio)
			y := sb.Min.Y + (sh-ch)/2
			srcRect = image.Rect(sb.Min.X, y, sb.Max.X, y+ch)
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, srcRect, draw.Over, nil)
	return dst
}

func encode(path string, img image.Image, format string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	switch format {
	case "jpeg":
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: 82}) // WordPress' default JPEG quality
	case "gif":
		err = gif.Encode(f, img, nil)
	default:
		err = png.Encode(f, img)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// HumanSize is size_format().
func HumanSize(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	s := strconv.FormatFloat(f, 'f', 0, 64)
	if f < 10 {
		s = strings.TrimSuffix(strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), "0"), ".")
	}
	return s + " " + units[i]
}

// Edit operations of the image editor (image_edit_apply_changes()).

// Op is one step of the editor history: Rotate (degrees, counter-clockwise), Flip (bit 1 = along the
// horizontal axis, bit 2 = along the vertical axis) or Crop (in preview coordinates; Ratio converts
// them to image pixels, 0 means use the preview ratio of the current image).
type Op struct {
	Rotate            int
	Flip              int
	Crop              bool
	X, Y, W, H, Ratio float64
}

// PreviewRatio is _image_get_preview_ratio().
func PreviewRatio(w, h int) float64 {
	m := w
	if h > m {
		m = h
	}
	if m > 600 {
		return 600 / float64(m)
	}
	return 1
}

// ApplyOps combines consecutive rotations and flips like WordPress, then applies the history.
func ApplyOps(img image.Image, ops []Op) image.Image {
	var merged []Op
	for _, op := range ops {
		if n := len(merged); n > 0 && !op.Crop && !merged[n-1].Crop {
			last := &merged[n-1]
			if op.Flip == 0 && last.Flip == 0 {
				last.Rotate += op.Rotate
				continue
			}
			if op.Rotate == 0 && last.Rotate == 0 {
				last.Flip ^= op.Flip
				continue
			}
		}
		merged = append(merged, op)
	}
	for _, op := range merged {
		switch {
		case op.Crop:
			b := img.Bounds()
			scale := op.Ratio
			if scale == 0 {
				scale = 1 / PreviewRatio(b.Dx(), b.Dy())
			}
			r := image.Rect(int(op.X*scale), int(op.Y*scale), int(op.X*scale)+int(op.W*scale), int(op.Y*scale)+int(op.H*scale)).
				Add(b.Min).Intersect(b)
			if r.Empty() {
				continue
			}
			dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
			draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
			img = dst
		case op.Flip != 0:
			img = flip(img, op.Flip&1 != 0, op.Flip&2 != 0)
		default:
			img = rotate(img, op.Rotate)
		}
	}
	return img
}

func rotate(src image.Image, angle int) image.Image {
	angle = ((angle % 360) + 360) % 360
	if angle == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	if angle == 180 {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			switch angle {
			case 90: // counter-clockwise, like imagerotate()
				dst.Set(y, w-1-x, c)
			case 180:
				dst.Set(w-1-x, h-1-y, c)
			case 270:
				dst.Set(h-1-y, x, c)
			}
		}
	}
	return dst
}

func flip(src image.Image, vertical, horizontal bool) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			if horizontal {
				dx = w - 1 - x
			}
			if vertical {
				dy = h - 1 - y
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// Decode opens an image file under uploadsDir.
func Decode(uploadsDir, relPath string) (image.Image, string, error) {
	f, err := os.Open(filepath.Join(uploadsDir, filepath.FromSlash(relPath)))
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	return image.Decode(f)
}

// Resize scales an image to exactly w×h.
func Resize(img image.Image, w, h int) image.Image { return resize(img, w, h, false) }

// Encode writes img to w in the given format (jpeg, png or gif).
func EncodeTo(w io.Writer, img image.Image, format string) error {
	switch format {
	case "jpeg":
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 82})
	case "gif":
		return gif.Encode(w, img, nil)
	}
	return png.Encode(w, img)
}

// WriteFile encodes img to a new file under uploadsDir.
func WriteFile(uploadsDir, relPath string, img image.Image, format string) error {
	return encode(filepath.Join(uploadsDir, filepath.FromSlash(relPath)), img, format)
}
