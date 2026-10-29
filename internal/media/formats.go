package media

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"image"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	_ "golang.org/x/image/bmp"  // decode BMP uploads for their sizes
	_ "golang.org/x/image/tiff" // decode TIFF uploads for their sizes
)

// Formats beyond JPEG/PNG/GIF. Images the standard library can decode but not write (WebP, BMP,
// TIFF) get their sub-sizes as JPEG, or PNG when they have transparency, like WordPress'
// image_editor_output_format mapping (HEIC → JPEG). AVIF and HEIC cannot be decoded here: their
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
	i := bytes.Index(head, []byte("ispe"))
	if i < 0 || i+16 > len(head) {
		return 0, 0, false
	}
	// box: size(4) "ispe" version+flags(4) width(4) height(4)
	w := binary.BigEndian.Uint32(head[i+8 : i+12])
	h := binary.BigEndian.Uint32(head[i+12 : i+16])
	if w == 0 || h == 0 || w > 1<<16 || h > 1<<16 {
		return 0, 0, false
	}
	return int(w), int(h), true
}

// svgLength parses an SVG length ("120", "120px", "12.5"); other units are not measurable.
func svgLength(v string) (float64, bool) {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil && f > 0
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
				vw, e1 := strconv.ParseFloat(p[2], 64)
				vh, e2 := strconv.ParseFloat(p[3], 64)
				if e1 == nil && e2 == nil && vw > 0 && vh > 0 {
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
		if okW && okH {
			return int(w + 0.5), int(h + 0.5)
		}
		return 0, 0
	}
}

// svgDropElements are removed with everything inside them.
var svgDropElements = map[string]bool{"script": true, "foreignobject": true, "iframe": true, "embed": true,
	"object": true, "audio": true, "video": true, "handler": true, "listener": true}

var svgScriptURL = regexp.MustCompile(`(?i)^\s*(javascript|vbscript|livescript):`)
var svgSafeData = regexp.MustCompile(`(?i)^\s*data:image/(png|jpe?g|gif|webp|avif);`)
var svgBadCSS = regexp.MustCompile(`(?i)(@import|expression\s*\(|javascript:|url\s*\(\s*['"]?\s*(?:javascript|https?:|//))`)

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
			if skip > 0 || svgDropElements[name] {
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
					lv := strings.ToLower(v)
					if svgScriptURL.MatchString(v) || (strings.HasPrefix(lv, "data:") && !svgSafeData.MatchString(v)) {
						continue
					}
					if name == "use" && !strings.HasPrefix(v, "#") {
						continue
					}
					if (strings.HasPrefix(lv, "http") || strings.HasPrefix(lv, "//")) && name != "a" {
						continue // no external resources inside the image
					}
				case an == "style" && svgBadCSS.MatchString(a.Value):
					continue
				case svgScriptURL.MatchString(v):
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
			if svgBadCSS.Match(t) {
				continue // a <style> body with imports or script URLs
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
