package media

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"image"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	_ "golang.org/x/image/bmp"  // decode BMP uploads for their sizes
	_ "golang.org/x/image/tiff" // decode TIFF uploads for their sizes
)

// Formats beyond JPEG/PNG/GIF. Images decoded here but not written by default (WebP, BMP, TIFF)
// get their sub-sizes as JPEG, or PNG when they have transparency, like WordPress'
// image_editor_output_format mapping (HEIC → JPEG). WebP can be written: Options.OutputFormat
// (the site's image output format setting) keeps WebP uploads in WebP or converts JPEG and PNG
// uploads to WebP. AVIF and HEIC cannot be decoded here: their
// width and height are read from the ISO-BMFF "ispe" box and no sub-sizes are made, so the
// original is used everywhere. SVG is sanitized on upload and measured from its root element.

// ErrSVG is returned for an SVG that could not be parsed or made safe.
var ErrSVG = errors.New("Sorry, this SVG file could not be read or is not safe to upload.")

// outputFormat is the format sub-sizes of a decoded image are written in.
func outputFormat(format string, img image.Image) string {
	switch format {
	case "jpeg", "png", "gif":
		return format
	}
	if hasAlpha(img) {
		return "png"
	}
	return "jpeg"
}

func hasAlpha(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	return true
}

// writable reports whether images can be saved in a format.
func writable(format string) bool {
	switch format {
	case "jpeg", "png", "gif", "webp":
		return true
	}
	return false
}

// formatExt is the file extension for an output format.
func formatExt(format string) string {
	switch format {
	case "jpeg":
		return ".jpg"
	case "png":
		return ".png"
	case "gif":
		return ".gif"
	}
	return "." + format
}

// MaxSVGBytes is the largest SVG accepted on upload or on a remote-storage link.
const MaxSVGBytes = 8 << 20

// sniffImage checks the first bytes of an uploaded raster image (JPEG, PNG, GIF, WebP, BMP, TIFF)
// against the type of its extension: ok is false when they are not an image of any of those
// types, real is the MIME type the content has when it differs from mime ("" when it matches, or
// for other types, which are not checked).
func sniffImage(path, mime string) (real string, ok bool) {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/tiff":
	default:
		return "", true
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	return sniffBytes(head[:n], mime)
}

// SniffUpload is sniffImage for bytes already in memory (a remote object that cannot be renamed).
// real is another raster MIME when the content does not match the extension; ok is false when the
// bytes are not one of the checked image types.
func SniffUpload(filename string, head []byte) (real string, ok bool) {
	return sniffBytes(head, MimeFor(filename))
}

func sniffBytes(head []byte, mime string) (real string, ok bool) {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/tiff":
	default:
		return "", true
	}
	if len(head) == 0 {
		return "", true // empty: the callers refuse it with their own message
	}
	var got string
	switch {
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		got = "image/jpeg"
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		got = "image/png"
	case bytes.HasPrefix(head, []byte("GIF87a")), bytes.HasPrefix(head, []byte("GIF89a")):
		got = "image/gif"
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		got = "image/webp"
	case bytes.HasPrefix(head, []byte("BM")):
		got = "image/bmp"
	case bytes.HasPrefix(head, []byte("II*\x00")), bytes.HasPrefix(head, []byte("MM\x00*")):
		got = "image/tiff"
	default:
		return "", false
	}
	if got == mime {
		return "", true
	}
	return got, true
}

// isoDimensions reads the width and height of an AVIF/HEIC file from its first "ispe" box.
func isoDimensions(path string) (int, int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	head := make([]byte, 256<<10) // the meta box sits near the start
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	for start := 0; start < len(head); {
		found := bytes.Index(head[start:], []byte("ispe"))
		if found < 0 {
			break
		}
		i := start + found
		start = i + len("ispe")
		if i < 4 || i+16 > len(head) {
			continue
		}
		if size := binary.BigEndian.Uint32(head[i-4 : i]); size != 0 && size < 20 {
			continue
		}
		// box: size(4) "ispe" version+flags(4) width(4) height(4)
		w := binary.BigEndian.Uint32(head[i+8 : i+12])
		h := binary.BigEndian.Uint32(head[i+12 : i+16])
		if w > 0 && h > 0 && w <= 1<<16 && h <= 1<<16 {
			return int(w), int(h), true
		}
	}
	return 0, 0, false
}

// svgLength parses an SVG length ("120", "120px", "12.5"); other units are not measurable.
func svgLength(v string) (float64, bool) {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil && f > 0 && f+0.5 < float64(int(^uint(0)>>1))
}

// svgDimensions reads width/height (or the viewBox) of a sanitized SVG.
func svgDimensions(data []byte) (int, int) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err != nil {
			return 0, 0
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var w, h float64
		var okW, okH bool
		var vb string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "width":
				w, okW = svgLength(a.Value)
			case "height":
				h, okH = svgLength(a.Value)
			case "viewBox":
				vb = a.Value
			}
		}
		if (!okW || !okH) && vb != "" {
			if p := strings.Fields(strings.ReplaceAll(vb, ",", " ")); len(p) == 4 {
				vx, ex := strconv.ParseFloat(p[0], 64)
				vy, ey := strconv.ParseFloat(p[1], 64)
				vw, e1 := strconv.ParseFloat(p[2], 64)
				vh, e2 := strconv.ParseFloat(p[3], 64)
				if ex == nil && ey == nil && !math.IsNaN(vx) && !math.IsNaN(vy) && !math.IsInf(vx, 0) && !math.IsInf(vy, 0) &&
					e1 == nil && e2 == nil && vw > 0 && vh > 0 && !math.IsInf(vw, 0) && !math.IsInf(vh, 0) {
					switch {
					case okW:
						h, okH = w*vh/vw, true
					case okH:
						w, okW = h*vw/vh, true
					default:
						w, h, okW, okH = vw, vh, true, true
					}
				}
			}
		}
		if okW && okH && w+0.5 < float64(int(^uint(0)>>1)) && h+0.5 < float64(int(^uint(0)>>1)) {
			return int(w + 0.5), int(h + 0.5)
		}
		return 0, 0
	}
}

// svgDropElements are removed with everything inside them.
var svgDropElements = map[string]bool{"script": true, "foreignobject": true, "iframe": true, "embed": true,
	"object": true, "audio": true, "video": true, "handler": true, "listener": true}

// urlAsParsed is v the way a browser reads it as a URL: ASCII tab, CR, LF and the other control characters
// are deleted wherever they occur, backslashes become slashes (so "\\evil" is a protocol-relative URL),
// then surrounding spaces are trimmed. "java&#9;script:" is a script URL.
func urlAsParsed(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	v = strings.ReplaceAll(v, `\`, "/")
	return strings.TrimSpace(v)
}

// urlSchemeFlat collapses slashes inside the scheme. After "\" is normalized to "/", "java\script:"
// is "java/script:" and a browser that ignores the slash still treats it as a script URL.
func urlSchemeFlat(v string) string {
	v = urlAsParsed(v)
	i := strings.IndexByte(v, ':')
	if i < 0 {
		return v
	}
	return strings.ReplaceAll(v[:i], "/", "") + v[i:]
}

var svgScriptURL = regexp.MustCompile(`(?i)^\s*(javascript|vbscript|livescript):`)
var svgSafeData = regexp.MustCompile(`(?i)^\s*data:image/(png|jpe?g|gif|webp|avif);`)
var svgBadCSS = regexp.MustCompile(`(?i)(@import|expression\s*\(|javascript:|url\s*\(\s*['"]?\s*(?:javascript|https?:|//))`)

// svgPaintAttrs are presentation attributes that take a paint server, so url() is a resource load.
var svgPaintAttrs = map[string]bool{
	"fill": true, "stroke": true, "clip-path": true, "mask": true, "filter": true,
	"marker": true, "marker-start": true, "marker-mid": true, "marker-end": true,
	"cursor": true, "color": true, "stop-color": true, "flood-color": true, "lighting-color": true,
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// svgDecodeCSSEscapes decodes CSS escapes (\6a, \6a , \n, \x) so a script URL hidden as
// "\6a avascript:" is visible to the checks below.
func svgDecodeCSSEscapes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		if isHex(s[i+1]) {
			hex, n, j := 0, 0, i+1
			for n < 6 && j < len(s) && isHex(s[j]) {
				hex = hex*16 + hexVal(s[j])
				j++
				n++
			}
			if hex > 0 && hex <= 0x10FFFF {
				b.WriteRune(rune(hex))
			}
			if j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			i = j - 1
			continue
		}
		if s[i+1] == '\n' {
			i++
			continue
		}
		if s[i+1] == '\r' {
			i++
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			continue
		}
		b.WriteByte(s[i+1])
		i++
	}
	return b.String()
}

// svgExternalURLFunc reports a url() that is not a local fragment (#id).
func svgExternalURLFunc(v string) bool {
	lower := strings.ToLower(v)
	for i := 0; i < len(lower); {
		j := strings.Index(lower[i:], "url(")
		if j < 0 {
			return false
		}
		rest := strings.TrimLeft(lower[i+j+4:], " \t\"'")
		if !strings.HasPrefix(rest, "#") {
			return true
		}
		i = i + j + 4
	}
	return false
}

// svgCSSUnsafe reports CSS (a style attribute, a presentation attribute or a style element) that
// loads a script, an external resource, or a url() hidden by CSS escapes.
func svgCSSUnsafe(v string) bool {
	flat := strings.ReplaceAll(svgDecodeCSSEscapes(v), `\`, "/")
	if svgBadCSS.MatchString(flat) || svgScriptURL.MatchString(urlSchemeFlat(flat)) {
		return true
	}
	return svgExternalURLFunc(flat)
}

// svgAnimatesURL reports an animation element (set, animate…) that changes a link or other URL
// attribute: <set attributeName="href" to="javascript:…"> or values="x;javascript:…" would turn a
// safe link into a script URL after the attribute checks below ran.
func svgAnimatesURL(name string, attrs []xml.Attr) bool {
	switch name {
	case "set", "animate", "animatemotion", "animatetransform", "animatecolor", "discard":
	default:
		return false
	}
	for _, a := range attrs {
		if strings.ToLower(a.Name.Local) != "attributename" {
			continue
		}
		v := strings.ToLower(urlAsParsed(a.Value))
		if i := strings.LastIndexByte(v, ':'); i >= 0 {
			v = v[i+1:] // xlink:href
		}
		switch v {
		case "href", "src", "action", "formaction", "style":
			return true
		case "fill", "stroke", "clip-path", "mask", "filter", "cursor", "marker", "marker-start", "marker-mid", "marker-end":
			for _, b := range attrs {
				if svgCSSUnsafe(b.Value) {
					return true
				}
			}
			return false
		}
		return strings.HasPrefix(v, "on")
	}
	return false
}

func svgName(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

func svgEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// SanitizeSVG returns the SVG without scripts, event handlers, external references, embedded
// documents and DOCTYPE/entity declarations (the protections of the usual "safe SVG" sanitizers).
// It is serialized by hand from raw tokens so namespace prefixes (xlink:href, …) stay as written.
func SanitizeSVG(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrSVG
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	d.Entity = map[string]string{} // unknown entities fail instead of expanding
	var out strings.Builder
	skip, depth := 0, 0
	sawRoot := false
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrSVG
		}
		switch t := tok.(type) {
		case xml.Directive, xml.ProcInst, xml.Comment:
			continue // DOCTYPE, ENTITY, processing instructions, comments
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if skip > 0 || svgDropElements[name] || svgAnimatesURL(name, t.Attr) {
				skip++
				continue
			}
			if !sawRoot {
				if name != "svg" {
					return nil, ErrSVG
				}
				sawRoot = true
			}
			depth++
			out.WriteString("<" + svgName(t.Name))
			for _, a := range t.Attr {
				an := strings.ToLower(a.Name.Local)
				v := strings.TrimSpace(a.Value)
				switch {
				case strings.HasPrefix(an, "on"):
					continue
				case an == "href" || an == "src" || an == "action" || an == "formaction":
					v = urlAsParsed(v)
					flat := urlSchemeFlat(v)
					lv := strings.ToLower(v)
					if svgScriptURL.MatchString(flat) || (strings.HasPrefix(lv, "data:") && !svgSafeData.MatchString(v)) {
						continue
					}
					if name == "use" && !strings.HasPrefix(v, "#") {
						continue
					}
					if name != "a" && !strings.HasPrefix(v, "#") && !svgSafeData.MatchString(v) {
						continue // no external or relative resources inside the image
					}
				case an == "style" && svgCSSUnsafe(a.Value):
					continue
				case svgPaintAttrs[an] && svgCSSUnsafe(a.Value):
					continue
				case svgScriptURL.MatchString(urlSchemeFlat(v)):
					continue
				}
				out.WriteString(" " + svgName(a.Name) + `="` + strings.ReplaceAll(svgEscape(a.Value), `"`, "&#34;") + `"`)
			}
			out.WriteString(">")
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			depth--
			out.WriteString("</" + svgName(t.Name) + ">")
		case xml.CharData:
			if skip > 0 || depth == 0 {
				continue
			}
			if svgCSSUnsafe(string(t)) {
				continue // a <style> body with imports, script URLs or CSS-escaped url()
			}
			out.WriteString(svgEscape(string(t)))
		}
	}
	if !sawRoot || depth != 0 {
		return nil, ErrSVG
	}
	return []byte(out.String()), nil
}

// sanitizeSVGFile rewrites an uploaded SVG in place.
func sanitizeSVGFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	clean, err := SanitizeSVG(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, clean, 0o644)
}
