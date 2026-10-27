package builder

// Icons_Manager: Font Awesome 5 / eicons are printed as inline SVG (e_font_icon_svg), shape
// dividers are printed from upstream's assets/shapes.

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"sync"
)

//go:embed data/icons.json.gz
var iconsGz []byte

//go:embed data/shapes.json.gz
var shapesGz []byte

var (
	iconsOnce sync.Once
	iconData  map[string]map[string][3]any // library -> name -> [width, height, path]
	shapeOnce sync.Once
	shapeData map[string]string
)

func gunzipJSON(gz []byte, v any) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		panic("builder: " + err.Error())
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		panic("builder: " + err.Error())
	}
	if err := json.Unmarshal(raw, v); err != nil {
		panic("builder: " + err.Error())
	}
}

func icons() map[string]map[string][3]any {
	iconsOnce.Do(func() { gunzipJSON(iconsGz, &iconData) })
	return iconData
}

var faPrefixRe = regexp.MustCompile(`fa(.*) fa-`)

// fontIconSVG mirrors Icons_Manager::get_font_icon_svg ("" when the icon is unknown).
func fontIconSVG(icon map[string]any, a *attrs) string {
	library := phpString(icon["library"])
	value := phpString(icon["value"])
	var key, name, file string
	switch {
	case strings.HasPrefix(library, "fa-"):
		m := faPrefixRe.FindString(value)
		if m == "" {
			return ""
		}
		name = strings.Replace(value, m, "", -1)
		key = strings.ReplaceAll(value, " fa-", "-")
		file = library
	case library == "eicons":
		key = value
		name = strings.ReplaceAll(value, "eicon-", "")
		file = "eicons"
	default:
		return ""
	}
	lib := icons()[file]
	d, ok := lib[name]
	if !ok || phpString(d[2]) == "" {
		return ""
	}
	if a == nil {
		a = newAttrs()
	}
	a.add("class", "e-font-icon-svg", "e-"+key)
	a.set("viewBox", "0 0 "+phpString(d[0])+" "+phpString(d[1]))
	a.set("xmlns", "http://www.w3.org/2000/svg")
	return "<svg " + a.String() + "><path d=\"" + escAttr(phpString(d[2])) + "\"></path></svg>"
}

// renderIcon mirrors Icons_Manager::render_icon / try_get_icon_html. It returns "" when the icon
// has no library. attributes are applied in order (e.g. aria-hidden first).
func renderIcon(icon any, a *attrs, tag string) string {
	im := asMap(icon)
	if im == nil || isEmpty(im["library"]) {
		return ""
	}
	if a == nil {
		a = newAttrs()
	}
	if tag == "" {
		tag = "i"
	}
	if phpString(im["library"]) == "svg" {
		return uploadedSVG(im["value"])
	}
	family := ""
	lib := phpString(im["library"])
	if strings.HasPrefix(lib, "fa-") {
		family = "font-awesome"
	} else if lib == "eicons" {
		family = "eicons"
	}
	if family != "" {
		cp := cloneAttrs(a)
		if svg := fontIconSVG(im, cp); svg != "" {
			return svg
		}
	}
	a.add("class", phpString(im["value"]))
	return "<" + tag + " " + a.String() + "></" + tag + ">"
}

func cloneAttrs(a *attrs) *attrs {
	n := newAttrs()
	for _, k := range a.keys {
		n.add(k, a.vals[k]...)
	}
	return n
}

// SVGLoader returns the markup of an uploaded SVG attachment (Svg::get_inline_svg). The server may
// set it; when nil uploaded SVG icons render nothing.
var SVGLoader func(id int64) string

func uploadedSVG(v any) string {
	m := asMap(v)
	if m == nil {
		return ""
	}
	if _, ok := m["id"]; !ok {
		return ""
	}
	if SVGLoader == nil {
		return ""
	}
	return SVGLoader(int64(toInt(m["id"])))
}

func shapeSVG(shape string, negative bool) string {
	shapeOnce.Do(func() { gunzipJSON(shapesGz, &shapeData) })
	if shape == "" {
		return ""
	}
	name := shape
	if negative {
		name += "-negative"
	}
	return shapeData[name]
}
