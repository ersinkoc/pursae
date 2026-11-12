package webbuilder

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Fonts a doc needs (port of upstream fonts.ts docFontNeeds): the site heading and body fonts
// plus per-element overrides, limited to library (Google) fonts and snapped to the weights each
// family really ships. System fonts and unknown names need nothing.

const (
	// SiteHeadingFont / SiteBodyFont are element font values that follow the site settings.
	SiteHeadingFont = "@heading"
	SiteBodyFont    = "@body"
	DefaultBodyFont = "Inter"
)

type pairFont struct {
	family string
	weight int
	italic bool
}

// pairFonts mirrors PAIR_FONT (the heading font of each font pair preset).
var pairFonts = map[string]pairFont{
	"elegant": {"Instrument Serif", 400, true},
	"modern":  {"Space Grotesk", 600, false},
	"plain":   {"Inter", 700, false},
}

// FontFamily is a library font.
type FontFamily struct {
	Family  string
	Weights []int
	Italics []int
}

var fontByLower = func() map[string]FontFamily {
	m := make(map[string]FontFamily, len(fontCatalog))
	for fam, w := range fontCatalog {
		m[strings.ToLower(fam)] = FontFamily{Family: fam, Weights: hundreds(w[0]), Italics: hundreds(w[1])}
	}
	return m
}()

func hundreds(s string) []int {
	out := make([]int, 0, len(s))
	for _, d := range s {
		out = append(out, int(d-'0')*100)
	}
	return out
}

func unquoteFont(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "'")
	s = strings.TrimPrefix(s, `"`)
	s = strings.TrimSuffix(s, "'")
	return strings.TrimSuffix(s, `"`)
}

// LookupFont returns the library font of that name (case-insensitive).
func LookupFont(name string) (FontFamily, bool) {
	if name == "" {
		return FontFamily{}, false
	}
	f, ok := fontByLower[strings.ToLower(unquoteFont(name))]
	if ok {
		f.Weights = append([]int(nil), f.Weights...)
		f.Italics = append([]int(nil), f.Italics...)
	}
	return f, ok
}

func snapWeight(list []int, w int) int {
	if len(list) == 0 {
		return w
	}
	best := list[0]
	for _, x := range list[1:] {
		if abs(x-w) < abs(best-w) {
			best = x
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

type siteFontInfo struct {
	heading, body             string
	headingWeight, bodyWeight int
	italicAccent              bool
}

func siteFontsOf(s SiteSettings) siteFontInfo {
	pair, ok := pairFonts[s.FontPair]
	if !ok {
		pair = pairFonts["elegant"]
	}
	f := siteFontInfo{heading: s.HeadingFont, headingWeight: pair.weight, body: s.BodyFont, bodyWeight: 400}
	if f.heading == "" {
		f.heading = pair.family
	}
	if s.HeadingWeight != nil {
		f.headingWeight = int(*s.HeadingWeight)
	}
	if f.body == "" {
		f.body = DefaultBodyFont
	}
	if s.BodyWeight != nil {
		f.bodyWeight = int(*s.BodyWeight)
	}
	if hf, ok := LookupFont(f.heading); ok && pair.italic && len(hf.Italics) > 0 {
		f.italicAccent = true
	}
	return f
}

// DocFonts lists the library fonts the doc uses, sorted by family; weights are snapped to the
// family's weights (italic weights included in Weights when Italic is set).
func DocFonts(d *DocState) []FontNeed {
	site := siteFontsOf(d.Site)
	type need struct{ w, i map[int]bool }
	needs := map[string]*need{}
	add := func(name string, w []int, it []int) {
		switch name {
		case SiteHeadingFont:
			name = site.heading
		case SiteBodyFont:
			name = site.body
		}
		f, ok := LookupFont(name)
		if !ok {
			return
		}
		n := needs[f.Family]
		if n == nil {
			n = &need{w: map[int]bool{400: true}, i: map[int]bool{}}
			needs[f.Family] = n
		}
		for _, x := range w {
			n.w[x] = true
		}
		for _, x := range it {
			n.i[x] = true
		}
	}
	var accent []int
	if site.italicAccent {
		accent = []int{site.headingWeight}
	}
	add(site.heading, []int{site.headingWeight}, accent)
	add(site.body, []int{400, 500, 600, 700, site.bodyWeight}, []int{400})
	var walk func(list []Section, depth int)
	walk = func(list []Section, depth int) {
		if depth > MaxDepth+1 {
			return
		}
		for i := range list {
			s := &list[i]
			if s.Style.TitleWeight != nil {
				add(site.heading, []int{int(*s.Style.TitleWeight)}, nil)
			}
			keys := make([]string, 0, len(s.Els))
			for k := range s.Els {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				es := s.Els[k]
				switch {
				case es.Font != "":
					w := []int{400, 600, 700}
					if es.FontWeight != nil {
						w = []int{int(*es.FontWeight)}
					}
					var it []int
					if es.Italic != nil && *es.Italic {
						it = w
					}
					add(es.Font, w, it)
				case es.FontWeight != nil:
					add(site.body, []int{int(*es.FontWeight)}, nil)
					add(site.heading, []int{int(*es.FontWeight)}, nil)
				}
			}
			for j := range s.Items {
				if s.Items[j].Sub != nil {
					walk([]Section{*s.Items[j].Sub}, depth+1)
				}
			}
		}
	}
	walk(d.Sections, 0)

	out := make([]FontNeed, 0, len(needs))
	for fam, n := range needs {
		f, _ := LookupFont(fam)
		ws := map[int]bool{}
		for w := range n.w {
			ws[snapWeight(f.Weights, w)] = true
		}
		italic := false
		if len(f.Italics) > 0 && len(n.i) > 0 {
			italic = true
			for w := range n.i {
				ws[snapWeight(f.Italics, w)] = true
			}
		}
		fn := FontNeed{Family: fam, Italic: italic}
		for w := range ws {
			fn.Weights = append(fn.Weights, w)
		}
		sort.Ints(fn.Weights)
		out = append(out, fn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Family < out[j].Family })
	return out
}

// FontCSS2Query is the Google Fonts css2 query of one need ("family=Inter:wght@400;700&display=swap"),
// as RouteFontsCSS takes it.
func FontCSS2Query(n FontNeed) string {
	fam := strings.ReplaceAll(url.QueryEscape(n.Family), "%20", "+")
	ws := make([]string, len(n.Weights))
	for i, w := range n.Weights {
		ws[i] = strconv.Itoa(w)
	}
	var axis string
	if n.Italic {
		var tuples []string
		for _, w := range ws {
			tuples = append(tuples, "0,"+w)
		}
		for _, w := range ws {
			tuples = append(tuples, "1,"+w)
		}
		axis = "ital,wght@" + strings.Join(tuples, ";")
	} else {
		axis = "wght@" + strings.Join(ws, ";")
	}
	return "family=" + fam + ":" + axis + "&display=swap"
}
