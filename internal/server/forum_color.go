package server

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
)

// Forum colours (v0.2.6). A forum's _forum_color holds a pastel key (sky, mint, peach, lilac, grey:
// the theme maps them to its own colours) or a custom "#rrggbb". For a custom colour the theme data
// carries Color "custom", ColorHex (the background) and ColorInk: a text colour of the same hue the
// core picks so the pair reads at a contrast of at least 4.5:1 (WCAG AA): a dark ink on a light
// colour, a light ink on a dark one.

// forumColorCustom is forumForum.Color for a custom colour.
const forumColorCustom = "custom"

// forumColorValue normalizes a typed colour: a pastel key, "#rrggbb" (from #rgb or #rrggbb, any
// case), or "" for anything else.
func forumColorValue(raw string) string {
	c := strings.ToLower(strings.TrimSpace(raw))
	if forumColors[c] {
		return c
	}
	if !forumColorRe.MatchString(c) {
		return ""
	}
	if len(c) == 4 {
		c = "#" + strings.Repeat(c[1:2], 2) + strings.Repeat(c[2:3], 2) + strings.Repeat(c[3:4], 2)
	}
	return c
}

// forumColorParts maps a stored colour to the theme fields: the key ("custom" for a hex), the hex
// and its ink (custom colours only) and the inline style carrying both (--pf-c / --pf-ci).
func forumColorParts(stored string) (key, hex, ink string, style template.CSS) {
	v := forumColorValue(stored)
	switch {
	case v == "":
		return "", "", "", ""
	case forumColors[v]:
		return v, "", "", ""
	}
	ink = forumInkFor(v)
	return forumColorCustom, v, ink, template.CSS("--pf-c:" + v + ";--pf-ci:" + ink)
}

// forumRGB reads "#rrggbb".
func forumRGB(hex string) (r, g, b float64) {
	n, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return float64(n>>16&0xff) / 255, float64(n>>8&0xff) / 255, float64(n&0xff) / 255
}

// forumLuminance is the WCAG relative luminance of a colour (components 0..1).
func forumLuminance(r, g, b float64) float64 {
	lin := func(c float64) float64 {
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// forumContrast is the WCAG contrast ratio of two "#rrggbb" colours.
func forumContrast(a, b string) float64 {
	la := forumLuminance(forumRGB(a))
	lb := forumLuminance(forumRGB(b))
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// forumHSL converts RGB (0..1) to HSL (h in degrees, s and l 0..1).
func forumHSL(r, g, b float64) (h, s, l float64) {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

// forumHex converts HSL to "#rrggbb".
func forumHex(h, s, l float64) string {
	f := func(n float64) float64 {
		k := math.Mod(n+h/30, 12)
		a := s * math.Min(l, 1-l)
		return l - a*math.Max(-1, math.Min(math.Min(k-3, 9-k), 1))
	}
	c := func(v float64) int { return int(math.Round(math.Max(0, math.Min(1, v)) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", c(f(0)), c(f(8)), c(f(4)))
}

// forumInkFor is a readable text colour on bg ("#rrggbb"): the same hue, darkened on a light
// colour (lightened on a dark one) step by step until the contrast reaches 4.5:1; black or white
// when no tone of the hue gets there.
func forumInkFor(bg string) string {
	h, s, l := forumHSL(forumRGB(bg))
	s = math.Min(math.Max(s, 0.35), 0.9) // a recognisable tint of the hue, never neon
	if s == 0.35 && l > 0.97 {
		s = 0.2 // near-white backgrounds get a quiet grey ink
	}
	dark := forumLuminance(forumRGB(bg)) > 0.18
	for i := 0; i <= 40; i++ {
		var tl float64
		if dark {
			tl = math.Min(l, 0.45) - float64(i)*0.0125
		} else {
			tl = math.Max(l, 0.6) + float64(i)*0.01
		}
		if tl < 0 || tl > 1 {
			break
		}
		if ink := forumHex(h, s, tl); forumContrast(ink, bg) >= 4.5 {
			return ink
		}
	}
	if forumContrast("#000000", bg) >= forumContrast("#ffffff", bg) {
		return "#000000"
	}
	return "#ffffff"
}
