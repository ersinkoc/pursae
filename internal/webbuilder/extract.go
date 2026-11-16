package webbuilder

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"strings"
)

// Exact HTML import: extract.js runs in a browser (the import page's sandboxed frame), reads the
// computed styles and boxes of the rendered page and describes it as free-form nodes; ConvertExtract
// turns that description into section specs for the server bundle's __wbCreate. Everything the browser
// sends is untrusted: node types, layout and element-style keys are whitelisted, numbers clamped,
// strings checked, rich text sanitized and URLs filtered here, and the doc validation and snapshot
// sanitizer run again on publish.

// ExtractorJS is the script the import page injects into the frame.
//
//go:embed extract.js
var ExtractorJS string

// Extract is what extract.js posts for one viewport width.
type Extract struct {
	V        int              `json:"v"`
	Width    int              `json:"width"`
	Error    string           `json:"error"`
	SEO      ExtractSEO       `json:"seo"`
	BodyBg   string           `json:"bodyBg"`
	Sections []ExtractSection `json:"sections"`
	Hidden   []string         `json:"hidden"`
	Layouts  map[string][]any `json:"layouts"`
	Metrics  map[string][]any `json:"metrics"`
	Warnings []string         `json:"warnings"`
	Nodes    int              `json:"nodes"`
}

// ExtractSEO is the page's search metadata.
type ExtractSEO struct {
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Lang             string   `json:"lang"`
	Robots           string   `json:"robots"`
	OGTitle          string   `json:"ogTitle"`
	OGDescription    string   `json:"ogDescription"`
	OGImage          string   `json:"ogImage"`
	Canonical        string   `json:"canonical"`
	H1               []string `json:"h1"`
	Headings         []string `json:"headings"`
	ImagesWithoutAlt int      `json:"imagesWithoutAlt"`
}

// ExtractSection is one top-level part of the page.
type ExtractSection struct {
	Tag    string       `json:"tag"`
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Bg     string       `json:"bg"`
	Sticky int          `json:"sticky"`
	Root   *ExtractNode `json:"root"`
}

// ExtractNode is one free-form node.
type ExtractNode struct {
	N      string          `json:"n"`
	P      string          `json:"p"`
	Tag    string          `json:"tag"`
	T      string          `json:"t"`
	X      string          `json:"x"`
	Href   string          `json:"href"`
	Lay    map[string]any  `json:"lay"`
	Es     map[string]any  `json:"es"`
	K      []*ExtractNode  `json:"k"`
	Sticky int             `json:"sticky"`
	Extra  json.RawMessage `json:"-"`
}

// ExtractResult is ConvertExtract's answer.
type ExtractResult struct {
	Sections []map[string]any
	SEO      ExtractSEO
	Brand    string
	Fonts    []string
	Warnings []string
	Nodes    int
}

const (
	maxSectionNodes    = 480 // under the doc validator's 500 items per section
	maxExtractSections = 80
	maxExtractNodes    = 4000
	maxExtractDepth    = 40
)

// ExtractOptions tune ConvertExtract.
type ExtractOptions struct {
	// FontMap replaces font families the builder's library lacks with library fonts, e.g.
	// {"Mona": "Geist"}; DefaultFontMap fills in common proprietary fonts.
	FontMap map[string]string
}

// DefaultFontMap: system and brand fonts that are not in the builder's font library, mapped to the
// closest library font (same proportions), so text keeps its length and lines break where they did.
var DefaultFontMap = map[string]string{
	"mona": "Geist", "mona sans": "Geist", "hubot sans": "Geist", "sf pro": "Inter", "sf pro display": "Inter", "sf pro text": "Inter",
	"helvetica neue": "Inter", "segoe ui": "Inter", "system-ui": "Inter", "sf mono": "Geist Mono", "menlo": "Geist Mono",
	"monaco": "Geist Mono", "consolas": "JetBrains Mono", "circular": "DM Sans", "gt walsheim": "DM Sans", "graphik": "Inter",
	"proxima nova": "Montserrat", "avenir": "Nunito Sans", "avenir next": "Nunito Sans", "futura": "Jost",
}

// ConvertExtract builds section specs from the desktop extraction; mobile (optional) is the same page
// at a phone width, used for what the page hides there (menus, parts).
func ConvertExtract(desk *Extract, mobile *Extract, opts ...ExtractOptions) (*ExtractResult, error) {
	if desk == nil || desk.V != 1 {
		return nil, fmt.Errorf("unknown extraction format")
	}
	if desk.Error != "" {
		return nil, fmt.Errorf("the browser could not read the page: %s", desk.Error)
	}
	c := &extractConv{hidden: map[string]bool{}, brands: map[string]int{}, fonts: map[string]int{}, fontMap: map[string]string{}, parentP: map[*ExtractNode]string{}}
	for k, v := range DefaultFontMap {
		c.fontMap[k] = v
	}
	for _, o := range opts {
		for k, v := range o.FontMap {
			c.fontMap[strings.ToLower(strings.TrimSpace(k))] = v
		}
	}
	if mobile != nil && mobile.Error == "" {
		c.mobileLay = mobile.Layouts
		c.mobileMet = mobile.Metrics
		deskHidden := map[string]bool{}
		for _, p := range desk.Hidden {
			deskHidden[p] = true
		}
		for _, p := range mobile.Hidden {
			if !deskHidden[p] {
				c.hidden[p] = true
			}
		}
	}
	res := &ExtractResult{SEO: desk.SEO}
	for i, s := range desk.Sections {
		if i >= maxExtractSections {
			c.warn(fmt.Sprintf("only the first %d parts of the page were imported", maxExtractSections))
			break
		}
		if s.Root == nil {
			continue
		}
		// The editor allows MaxItems nodes per section: a bigger part becomes consecutive sections.
		pieces := splitExtract(s.Root, maxSectionNodes)
		for pi, root := range pieces {
			part := s
			part.Root = root
			if pi > 0 {
				part.ID, part.Sticky = "", 0
			}
			if spec := c.section(part, i); spec != nil {
				res.Sections = append(res.Sections, spec)
			}
		}
	}
	if len(res.Sections) == 0 {
		return nil, fmt.Errorf("the page has no visible content")
	}
	for _, w := range desk.Warnings {
		if len(w) < 200 {
			c.warn(w)
		}
	}
	best := 0
	for col, n := range c.brands {
		if n > best || (n == best && col < res.Brand) {
			best, res.Brand = n, col
		}
	}
	for f := range c.fonts {
		res.Fonts = append(res.Fonts, f)
	}
	res.Warnings, res.Nodes = c.warnings, c.nodes
	return res, nil
}

type extractConv struct {
	parentP   map[*ExtractNode]string // extract path of each node's parent box
	fontMap   map[string]string
	mobileLay map[string][]any
	mobileMet map[string][]any
	hidden    map[string]bool
	brands    map[string]int
	fonts     map[string]int
	warnings  []string
	nodes     int
	seq       int
}

func (c *extractConv) warn(w string) {
	for _, x := range c.warnings {
		if x == w {
			return
		}
	}
	c.warnings = append(c.warnings, w)
}

var extractIDRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func (c *extractConv) section(s ExtractSection, i int) map[string]any {
	items := []map[string]any{}
	els := map[string]any{}
	if c.node(s.Root, "", "column", &items, els, 0) == "" || len(items) == 0 {
		return nil
	}
	bg := strings.ToLower(strings.TrimSpace(s.Bg))
	if !hexColorRE.MatchString(bg) {
		bg = "#ffffff"
	}
	palette := "light"
	if luminance(bg) < 0.45 {
		palette = "dark"
	}
	name := strings.TrimSpace(s.Name)
	if n := partNames[s.Tag]; n != "" {
		name = n
	}
	if name == "" {
		name = fmt.Sprintf("Section %d", i+1)
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	style := map[string]any{"palette": palette, "bg": bg, "padding": 0}
	if extractIDRE.MatchString(s.ID) {
		style["cssId"] = s.ID
	}
	if s.Sticky == 1 {
		style["motion"] = map[string]any{"sticky": true}
	}
	if s.Root != nil && c.hidden[s.Root.P] {
		style["hideOn"] = []string{"mobile"}
	}
	return map[string]any{"kind": KindCustom, "name": name, "items": items, "els": els, "style": style}
}

var (
	extractKinds = map[string]bool{"box": true, "heading": true, "text": true, "button": true, "link": true, "image": true, "icon": true, "input": true, "divider": true}
	extractEnums = map[string]map[string]bool{
		"dir":     {"row": true, "column": true, "grid": true},
		"align":   {"start": true, "center": true, "end": true, "stretch": true},
		"justify": {"start": true, "center": true, "end": true, "space-between": true},
		"self":    {"start": true, "center": true, "end": true, "stretch": true},
	}
	cssValueRE    = regexp.MustCompile(`^[A-Za-z0-9#%(),./\s:;'"_+-]*$`)
	cssImageRE    = regexp.MustCompile(`^[A-Za-z0-9#%(),./\s:'"_+?&=~-]*$`)
	extractFontRE = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,48}$`)
	listStyleRE   = regexp.MustCompile(`^[a-z-]{1,24}$`)
	colorValRE    = regexp.MustCompile(`^(#[0-9a-f]{6}|rgba\(\d{1,3},\d{1,3},\d{1,3},(0|1|0?\.\d+)\))$`)
)

// node adds n and its subtree to items; it returns the new item id ("" = left out).
func (c *extractConv) node(n *ExtractNode, parent, parentDir string, items *[]map[string]any, els map[string]any, depth int) string {
	if n == nil || depth > maxExtractDepth || !extractKinds[n.N] {
		return ""
	}
	if c.nodes >= maxExtractNodes {
		c.warn(fmt.Sprintf("the page was cut at %d elements", maxExtractNodes))
		return ""
	}
	it := map[string]any{"node": n.N, "title": "", "text": ""}
	switch n.N {
	case "heading", "text", "link":
		t := strings.TrimSpace(n.T)
		if strings.ContainsAny(t, "<>&") {
			t = strings.TrimSpace(SanitizeRich(t))
			if !IsRich(t) {
				t = strings.TrimSpace(html.UnescapeString(t))
			}
		}
		if t == "" || len(t) > 60000 {
			return ""
		}
		it["title"] = t
		if n.N == "heading" && headingTag.MatchString(n.Tag) {
			it["tag"] = n.Tag
		}
	case "button", "input":
		t := collapseSpace(n.T)
		if len(t) > 500 {
			t = t[:500]
		}
		if t == "" && n.N == "button" {
			return ""
		}
		it["title"] = t
		if n.N == "input" && n.Tag == "textarea" {
			it["tag"] = "textarea"
		}
	case "image", "icon":
		src := strings.TrimSpace(n.T)
		ok := okImageURL(src)
		if !ok && n.N == "icon" && strings.HasPrefix(strings.ToLower(src), "data:image/svg+xml,") && len(src) <= 32*1024 {
			// An inline SVG icon: through the builder's SVG sanitizer, then encoded again.
			if raw, err := url.PathUnescape(src[len("data:image/svg+xml,"):]); err == nil {
				if clean := SanitizeSVG(raw); clean != "" {
					src, ok = "data:image/svg+xml,"+url.PathEscape(clean), true
				}
			}
		}
		if !ok {
			c.warn("images with unsupported sources were left out")
			return ""
		}
		it["title"] = src
		if n.N == "image" {
			alt := collapseSpace(n.X)
			if len(alt) > 300 {
				alt = alt[:300]
			}
			it["text"] = alt
		}
	}
	if (n.N == "link" || n.N == "button") && n.Href != "" && okHref(n.Href) && len(n.Href) <= 2000 {
		it["href"] = n.Href
	}
	c.nodes++
	c.seq++
	id := fmt.Sprintf("n_x%d", c.seq)
	it["id"] = id
	if parent != "" {
		it["parent"] = parent
	}
	lay := c.lay(n.Lay, n.N, parentDir)
	if n.N == "box" && c.hidden[n.P] && lay["dir"] == "row" && linkKids(n) >= 2 {
		lay["menu"] = true // links hidden on phones: a hamburger there, like the source
	}
	if n.N == "box" && lay["dir"] == "row" && c.mobileLay != nil {
		// Rows stack on phones only where the design stacks them.
		if m, ok := c.mobileLay[n.P]; ok && len(m) > 0 {
			if d, _ := m[0].(string); d == "row" {
				delete(lay, "stack")
			} else {
				lay["stack"] = true
			}
		}
	}
	if n.N == "box" && lay["dir"] == "grid" && c.mobileLay != nil {
		if m, ok := c.mobileLay[n.P]; ok && len(m) > 1 {
			if f, ok := num(m[1]); ok && f >= 1 {
				lay["colsM"] = math.Round(clampF(f, 1, 6))
			}
		}
	}
	if n.N == "box" && lay["dir"] == "row" && len(n.K) > 0 {
		// A row of short leaves (a menu, a pair of buttons, a list of links): the editor keeps it on
		// one line on wide screens and wraps it only on small ones, so font metrics never break a word.
		leaves := true
		for _, k := range n.K {
			if k == nil || (k.N == "box" && !chipBox(k)) {
				leaves = false
				break
			}
		}
		if leaves {
			lay["wrap"] = true
		}
	}
	if n.N == "box" && chipBox(n) && lay["dir"] == "row" {
		lay["wrap"] = true // an icon + label link (a pill, a "GitHub ★ 79" badge) never breaks its label
	}
	es := c.style(n.Es, n.N)
	c.mobile(n, es, lay)
	if c.hidden[n.P] && lay["menu"] != true && parent != "" {
		// hidden on phones in the design (and not a menu, which becomes a hamburger)
		m, _ := es["m"].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		m["hide"] = true
		es["m"] = m
	}
	if len(lay) > 0 {
		it["lay"] = lay
	}
	*items = append(*items, it)
	if len(es) > 0 {
		els["items."+id+".title"] = es
		if n.N == "button" {
			if bg, _ := es["bg"].(string); hexColorRE.MatchString(bg) {
				c.brands[bg]++
			}
		}
		if f, _ := es["font"].(string); f != "" {
			c.fonts[f]++
		}
	}
	if n.N == "box" {
		dir, _ := lay["dir"].(string)
		if dir == "" {
			dir = "column"
		}
		for _, k := range n.K {
			c.parentP[k] = n.P
			c.node(k, id, dir, items, els, depth+1)
		}
	}
	return id
}

// linkKids counts the links among a box's children (a menu has several).
func linkKids(n *ExtractNode) int {
	c := 0
	for _, k := range n.K {
		if k != nil && (k.N == "link" || (k.N == "button" && k.Href != "")) {
			c++
		}
	}
	return c
}

// chipBox is a small inline box: a few leaves (icon, label, count) and no boxes, like a link with
// an icon or a badge. In a row it behaves like a word.
func chipBox(n *ExtractNode) bool {
	if n == nil || n.N != "box" || len(n.K) == 0 || len(n.K) > 4 {
		return false
	}
	for _, k := range n.K {
		if k == nil || k.N == "box" || (len(k.T) > 60 && k.N != "icon" && k.N != "image") {
			return false
		}
	}
	return true
}

func countExtract(n *ExtractNode) int {
	if n == nil {
		return 0
	}
	c := 1
	for _, k := range n.K {
		c += countExtract(k)
	}
	return c
}

// splitExtract cuts a part into pieces of at most max nodes, each a copy of the root box with a run
// of its children (recursively when one child alone is too big). Only the first piece keeps the top
// padding and only the last the bottom padding, so the pieces stack like the original.
func splitExtract(n *ExtractNode, max int) []*ExtractNode {
	if n == nil || countExtract(n) <= max || len(n.K) == 0 {
		return []*ExtractNode{n}
	}
	var groups [][]*ExtractNode
	var cur []*ExtractNode
	size := 1
	for _, k := range n.K {
		kc := countExtract(k)
		if kc+1 > max {
			if len(cur) > 0 {
				groups, cur, size = append(groups, cur), nil, 1
			}
			for _, sub := range splitExtract(k, max-1) {
				groups = append(groups, []*ExtractNode{sub})
			}
			continue
		}
		if size+kc > max && len(cur) > 0 {
			groups, cur, size = append(groups, cur), nil, 1
		}
		cur = append(cur, k)
		size += kc
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	out := make([]*ExtractNode, 0, len(groups))
	for i, g := range groups {
		cp := *n
		cp.K = g
		cp.P = fmt.Sprintf("%s.part%d", n.P, i)
		es := map[string]any{}
		for k, v := range n.Es {
			es[k] = v
		}
		if pad, ok := es["padding"].(map[string]any); ok {
			np := map[string]any{"t": pad["t"], "r": pad["r"], "b": pad["b"], "l": pad["l"]}
			if i > 0 {
				np["t"] = 0.0
			}
			if i < len(groups)-1 {
				np["b"] = 0.0
			}
			es["padding"] = np
		}
		if i > 0 {
			delete(es, "height")
		}
		cp.Es = es
		out = append(out, &cp)
	}
	return out
}

// mobile adds the node's phone sizes where they differ from what the editor would derive.
func (c *extractConv) mobile(n *ExtractNode, es, lay map[string]any) {
	m, ok := c.mobileMet[n.P]
	if !ok || len(m) < 7 {
		return
	}
	get := func(i int) float64 { f, _ := num(m[i]); return f }
	mob := map[string]any{}
	textual := n.N == "heading" || n.N == "text" || n.N == "link" || n.N == "button"
	if textual {
		if fs := get(0); fs >= 6 && fs <= 400 {
			if d, _ := num(es["fontSize"]); d != fs || d > 30 {
				mob["fontSize"] = math.Round(fs*10) / 10
			}
		}
		if lh := get(1); lh >= 0.5 && lh <= 4 {
			if d, _ := num(es["lineHeight"]); math.Abs(d-lh) > 0.02 {
				mob["lineHeight"] = math.Round(lh*100) / 100
			}
		}
	}
	if n.N == "box" || textual {
		p := map[string]any{"t": math.Round(clampF(get(2), 0, 2000)), "r": math.Round(clampF(get(3), 0, 2000)), "b": math.Round(clampF(get(4), 0, 2000)), "l": math.Round(clampF(get(5), 0, 2000))}
		dp, _ := es["padding"].(map[string]any)
		differs := false
		for _, k := range []string{"t", "r", "b", "l"} {
			var d float64
			if dp != nil {
				d, _ = num(dp[k])
			}
			pv, _ := num(p[k])
			if math.Abs(d-pv) > 1 {
				differs = true
			}
		}
		if differs || (dp != nil && (num1(dp["l"]) > 28 || num1(dp["r"]) > 28 || num1(dp["t"]) > 64 || num1(dp["b"]) > 64)) {
			mob["padding"] = p
		}
	}
	if len(mob) > 0 {
		es["m"] = mob
	}
	// A share of a row that stays a row on phones: its phone share, measured there.
	stillRow := false
	if pl, ok := c.mobileLay[c.parentP[n]]; ok && len(pl) > 0 {
		d, _ := pl[0].(string)
		stillRow = d == "row"
	}
	if _, ok := lay["basis"]; ok && stillRow && len(m) >= 8 {
		if pm, ok := c.mobileMet[c.parentP[n]]; ok && len(pm) >= 9 {
			if pw, _ := num(pm[8]); pw > 0 {
				if w := get(7); w > 0 {
					lay["basisM"] = math.Round(clampF(w/pw*100, 1, 100)*10) / 10
				}
			}
		}
	}
	if (n.N == "image" || n.N == "icon") && get(6) > 0.05 && get(6) < 20 {
		if d, _ := num(lay["ratio"]); d == 0 || math.Abs(d-get(6))/get(6) > 0.03 {
			lay["ratioM"] = math.Round(get(6)*1000) / 1000
		}
	}
}

func num1(v any) float64 { f, _ := num(v); return f }

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func clampF(f, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, f)) }

func (c *extractConv) lay(in map[string]any, kind, parentDir string) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		switch k {
		case "dir", "align", "justify", "self":
			if s, _ := v.(string); extractEnums[k][s] {
				if k == "dir" && kind != "box" {
					continue
				}
				out[k] = s
			}
		case "cols":
			if f, ok := num(v); ok && kind == "box" {
				out[k] = math.Round(clampF(f, 1, 12))
			}
		case "gap", "gapY":
			if f, ok := num(v); ok && kind == "box" {
				out[k] = math.Round(clampF(f, 0, 400))
			}
		case "basis":
			if f, ok := num(v); ok && parentDir == "row" {
				out[k] = math.Round(clampF(f, 1, 100)*10) / 10
			}
		case "ratio":
			if f, ok := num(v); ok && f > 0.05 && f < 20 {
				out[k] = math.Round(f*1000) / 1000
			}
		case "wrap", "stack", "clip":
			if b, _ := v.(bool); b && kind == "box" {
				out[k] = true
			}
		}
	}
	if self, ok := out["self"]; ok && parentDir != "column" {
		delete(out, "self")
		_ = self
	}
	return out
}

func (c *extractConv) style(in map[string]any, kind string) map[string]any {
	out := map[string]any{}
	box4 := func(v any, lo float64) map[string]any {
		m, _ := v.(map[string]any)
		if m == nil {
			return nil
		}
		b := map[string]any{}
		any := false
		for _, s := range []string{"t", "r", "b", "l"} {
			f, _ := num(m[s])
			f = math.Round(clampF(f, lo, 20000))
			b[s] = f
			if f != 0 {
				any = true
			}
		}
		if !any {
			return nil
		}
		return b
	}
	for k, v := range in {
		switch k {
		case "color", "bg":
			if s, _ := v.(string); colorValRE.MatchString(strings.ReplaceAll(strings.ToLower(s), " ", "")) {
				out[k] = strings.ReplaceAll(strings.ToLower(s), " ", "")
			}
		case "bgImage":
			if s, _ := v.(string); len(s) <= 4000 && cssImageRE.MatchString(s) && cssSafeURLs(s) && !strings.Contains(strings.ToLower(s), "expression") {
				out[k] = s
			}
		case "bgSize", "bgPos":
			if s, _ := v.(string); len(s) <= 80 && cssValueRE.MatchString(s) && !strings.ContainsAny(s, ";()") {
				out[k] = s
			}
		case "border", "shadow":
			if s, _ := v.(string); len(s) <= 600 && cssValueRE.MatchString(s) && !strings.Contains(strings.ToLower(s), "url(") {
				if k == "shadow" && strings.Contains(s, ";") {
					continue
				}
				out[k] = s
			}
		case "radius":
			if f, ok := num(v); ok {
				out[k] = math.Round(clampF(f, 0, 2000))
			}
		case "padding":
			if b := box4(v, 0); b != nil {
				out[k] = b
			}
		case "margin":
			if b := box4(v, -20000); b != nil { // overlapping layers use negative margins
				out[k] = b
			}
		case "mt":
			if f, ok := num(v); ok && f != 0 {
				out[k] = math.Round(clampF(f, -2000, 2000))
			}
		case "fontSize":
			if f, ok := num(v); ok && kind != "box" {
				out[k] = math.Round(clampF(f, 6, 400)*10) / 10
			}
		case "fontWeight":
			if f, ok := num(v); ok && kind != "box" {
				out[k] = math.Round(clampF(f, 100, 900)/100) * 100
			}
		case "lineHeight":
			if f, ok := num(v); ok && kind != "box" {
				out[k] = math.Round(clampF(f, 0.5, 4)*100) / 100
			}
		case "letterSpacing":
			if f, ok := num(v); ok && kind != "box" {
				out[k] = math.Round(clampF(f, -0.5, 2)*1000) / 1000
			}
		case "align":
			if s, _ := v.(string); s == "left" || s == "center" || s == "right" {
				out[k] = s
			}
		case "transform":
			if s, _ := v.(string); s == "uppercase" || s == "lowercase" || s == "capitalize" {
				out[k] = s
			}
		case "italic", "underline":
			if b, _ := v.(bool); b {
				out[k] = true
			}
		case "opacity":
			if f, ok := num(v); ok && f >= 0 && f < 1 {
				out[k] = math.Round(f*100) / 100
			}
		case "fit":
			if s, _ := v.(string); s == "cover" || s == "contain" {
				out[k] = s
			}
		case "font":
			if s, _ := v.(string); extractFontRE.MatchString(s) {
				if _, known := LookupFont(s); !known {
					if m := c.fontMap[strings.ToLower(s)]; m != "" {
						if _, ok := LookupFont(m); ok {
							s = m
						}
					}
				}
				out[k] = s
			}
		case "list":
			if s, _ := v.(string); listStyleRE.MatchString(s) {
				out[k] = s
			}
		case "width", "maxWidth", "height":
			if f, ok := num(v); ok && f > 0 {
				out[k] = math.Round(clampF(f, 1, 4000))
			}
		}
	}
	// Every CSS string must pass the doc validator's own check, value by value.
	for _, k := range []string{"bgImage", "bgSize", "bgPos", "border", "shadow", "color", "bg"} {
		if v, ok := out[k].(string); ok && !CSSValueOK(v) && !(k == "border" && BorderSidesOK(v)) {
			if k == "border" && strings.Contains(v, ";") {
				// per-side borders: keep the sides that pass
				var keep []string
				for _, part := range strings.Split(v, ";") {
					if bits := strings.SplitN(part, ":", 2); len(bits) == 2 && CSSValueOK(strings.TrimSpace(bits[1])) {
						keep = append(keep, part)
					}
				}
				if j := strings.Join(keep, ";"); j != "" && BorderSidesOK(j) {
					out[k] = j
					continue
				}
			}
			delete(out, k)
		}
	}
	return out
}
