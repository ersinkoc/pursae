package webbuilder

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTML import: a static page (an agent's mock-up, an exported landing page) becomes Web Builder
// sections the editor can change by drag and drop. Each top-level part of the page (header, section,
// footer, …) is one free-form section (kind custom): a tree of box, heading, text, button, link,
// image, icon, input and divider nodes (Item.node / Item.parent), with the look of every node in
// els["items.<id>.title"]. Styles come from style attributes and the page's <style> rules; the
// matcher is deliberately small (type, class, id and attribute-free compound selectors, the last
// compound of a descendant chain, :root custom properties), so utility-class frameworks loaded from
// a CDN are not resolved. Scripts, iframes and anything the snapshot sanitizer would drop are left
// out.

// ImportOptions tune ImportHTML.
type ImportOptions struct {
	// MaxSections caps the sections made (default 60); MaxNodes caps the nodes per section (default 400).
	MaxSections, MaxNodes int
}

// ImportResult is what ImportHTML found.
type ImportResult struct {
	// Sections are section specs for the bundle's __wbCreate (kind custom, items, els, style, name).
	Sections []map[string]any
	Title    string
	// Brand is the most used accent color of buttons ("" = none found).
	Brand    string
	Warnings []string
}

// ImportHTML converts a page or fragment.
func ImportHTML(src string, o ImportOptions) (*ImportResult, error) {
	if o.MaxSections <= 0 {
		o.MaxSections = 60
	}
	if o.MaxNodes <= 0 {
		o.MaxNodes = 400
	}
	root, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil, err
	}
	im := &importer{o: o, vars: map[string]string{}, brands: map[string]int{}}
	im.collectStyles(root)
	body := findTag(root, atom.Body)
	if body == nil {
		body = root
	}
	res := &ImportResult{}
	if t := findTag(root, atom.Title); t != nil {
		res.Title = strings.TrimSpace(textOf(t))
	}
	parts := im.sectionsOf(body)
	if len(parts) == 0 {
		return nil, fmt.Errorf("the HTML has no visible content")
	}
	for i, p := range parts {
		if i >= o.MaxSections {
			im.warn(fmt.Sprintf("only the first %d parts of the page were imported", o.MaxSections))
			break
		}
		if s := im.section(p, i); s != nil {
			res.Sections = append(res.Sections, s)
		}
	}
	if len(res.Sections) == 0 {
		return nil, fmt.Errorf("the HTML has no visible content")
	}
	best := 0
	for c, n := range im.brands {
		if n > best || (n == best && c < res.Brand) {
			best, res.Brand = n, c
		}
	}
	res.Warnings = im.warnings
	return res, nil
}

type cssRule struct {
	sel   compound
	spec  int
	order int
	decls map[string]string
}

type compound struct {
	tag     string
	id      string
	classes []string
}

type importer struct {
	o        ImportOptions
	rules    []cssRule
	vars     map[string]string
	brands   map[string]int
	warnings []string
	nodes    int
	seq      int
}

func (im *importer) warn(w string) {
	for _, x := range im.warnings {
		if x == w {
			return
		}
	}
	im.warnings = append(im.warnings, w)
}

func (im *importer) id(prefix string) string {
	im.seq++
	return fmt.Sprintf("%s_imp%d", prefix, im.seq)
}

// --- CSS -------------------------------------------------------------------------------------

var (
	cssCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssVarRE     = regexp.MustCompile(`var\(\s*(--[A-Za-z0-9_-]+)\s*(?:,\s*([^()]*(?:\([^()]*\))?[^()]*))?\)`)
	cssCompRE    = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9-]*|\*)?((?:[.#][A-Za-z0-9_-]+)*)$`)
)

func (im *importer) collectStyles(n *html.Node) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Style {
			im.parseCSS(ownText(n), 0)
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Script {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
}

// parseCSS reads rules; @media blocks are skipped except a plain "screen", nested blocks of other
// at-rules too.
func (im *importer) parseCSS(css string, depth int) {
	css = cssCommentRE.ReplaceAllString(css, "")
	for len(css) > 0 && depth < 4 {
		open := strings.IndexByte(css, '{')
		if open < 0 {
			return
		}
		head := strings.TrimSpace(css[:open])
		end := impMatchBrace(css, open)
		if end < 0 {
			return
		}
		block := css[open+1 : end]
		css = css[end+1:]
		if strings.HasPrefix(head, "@") {
			if strings.HasPrefix(head, "@media") && strings.Contains(head, "screen") && !strings.Contains(head, "width") {
				im.parseCSS(block, depth+1)
			}
			continue
		}
		decls := parseDecls(block)
		for _, sel := range strings.Split(head, ",") {
			sel = strings.TrimSpace(sel)
			if sel == ":root" || sel == "html" || sel == "body" || sel == "*" {
				for k, v := range decls {
					if strings.HasPrefix(k, "--") {
						im.vars[k] = v
					}
				}
				if sel == ":root" {
					continue
				}
			}
			if strings.ContainsAny(sel, ":[") {
				continue // pseudo-classes, pseudo-elements and attribute selectors are not matched
			}
			fields := strings.FieldsFunc(sel, func(r rune) bool { return r == ' ' || r == '>' || r == '+' || r == '~' })
			if len(fields) == 0 {
				continue
			}
			m := cssCompRE.FindStringSubmatch(fields[len(fields)-1])
			if m == nil {
				continue
			}
			c := compound{tag: strings.ToLower(strings.TrimPrefix(m[1], "*"))}
			spec := 0
			if c.tag != "" {
				spec = 1
			}
			for _, part := range regexp.MustCompile(`[.#][A-Za-z0-9_-]+`).FindAllString(m[2], -1) {
				if part[0] == '#' {
					c.id = part[1:]
					spec += 100
				} else {
					c.classes = append(c.classes, part[1:])
					spec += 10
				}
			}
			// Ancestors in the selector add specificity but are not checked.
			spec += (len(fields) - 1)
			im.rules = append(im.rules, cssRule{sel: c, spec: spec, order: len(im.rules), decls: decls})
		}
	}
}

func impMatchBrace(s string, open int) int {
	d := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			d++
		case '}':
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}

func parseDecls(block string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(block, ";") {
		k, v, ok := strings.Cut(d, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important"))
		if k != "" && v != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// style is the declared style of an element: matching rules by specificity, then the style attribute.
func (im *importer) style(n *html.Node) map[string]string {
	cls := strings.Fields(attr(n, "class"))
	id := attr(n, "id")
	var hits []cssRule
	for _, r := range im.rules {
		if r.sel.tag != "" && r.sel.tag != n.Data {
			continue
		}
		if r.sel.id != "" && r.sel.id != id {
			continue
		}
		ok := true
		for _, c := range r.sel.classes {
			if !containsString(cls, c) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, r)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].spec != hits[j].spec {
			return hits[i].spec < hits[j].spec
		}
		return hits[i].order < hits[j].order
	})
	out := map[string]string{}
	for _, r := range hits {
		for k, v := range r.decls {
			out[k] = v
		}
	}
	for k, v := range parseDecls(attr(n, "style")) {
		out[k] = v
	}
	for k, v := range out {
		out[k] = im.resolveVars(v, 0)
	}
	expandShorthands(out)
	return out
}

func (im *importer) resolveVars(v string, depth int) string {
	if depth > 8 || !strings.Contains(v, "var(") {
		return v
	}
	return im.resolveVars(cssVarRE.ReplaceAllStringFunc(v, func(m string) string {
		p := cssVarRE.FindStringSubmatch(m)
		if x, ok := im.vars[p[1]]; ok {
			return x
		}
		return strings.TrimSpace(p[2])
	}), depth+1)
}

func expandShorthands(st map[string]string) {
	if v, ok := st["background"]; ok {
		if _, has := st["background-color"]; !has {
			if c := firstColor(v); c != "" {
				st["background-color"] = c
			}
		}
		if _, has := st["background-image"]; !has && strings.Contains(v, "url(") {
			st["background-image"] = v[strings.Index(v, "url("):]
		}
		if strings.Contains(v, "gradient(") {
			st["background-image"] = v
		}
	}
	if v, ok := st["flex-flow"]; ok {
		for _, w := range strings.Fields(v) {
			switch w {
			case "row", "column", "row-reverse", "column-reverse":
				st["flex-direction"] = w
			case "wrap":
				st["flex-wrap"] = w
			}
		}
	}
}

// --- structure -------------------------------------------------------------------------------

var (
	skipTags   = map[string]bool{"script": true, "style": true, "link": true, "meta": true, "noscript": true, "template": true, "iframe": true, "object": true, "embed": true, "head": true, "title": true, "base": true, "canvas": true, "audio": true, "video": true, "source": true, "track": true, "map": true, "dialog": true}
	inlineTags = map[string]bool{"a": true, "b": true, "strong": true, "i": true, "em": true, "u": true, "s": true, "del": true, "span": true, "br": true, "code": true, "small": true, "mark": true, "sub": true, "sup": true, "abbr": true, "time": true, "q": true, "cite": true, "kbd": true, "label": true}
	wrapTags   = map[string]bool{"div": true, "main": true, "article": true, "body": true}
	partTags   = map[string]bool{"header": true, "nav": true, "section": true, "footer": true, "aside": true, "article": true, "form": true, "figure": true}
	headingTag = regexp.MustCompile(`^h[1-6]$`)
)

func (im *importer) hidden(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if skipTags[n.Data] || impHasAttr(n, "hidden") || attr(n, "aria-hidden") == "true" && n.Data != "svg" {
		return true
	}
	st := im.style(n)
	return st["display"] == "none" || st["visibility"] == "hidden"
}

func elementKids(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

func ownText(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return strings.TrimSpace(b.String())
}

// sectionsOf splits the page into its top-level parts: single wrappers are opened, main is
// flattened, and loose inline content is grouped into one part.
func (im *importer) sectionsOf(body *html.Node) []*html.Node {
	cur := body
	for {
		kids := visibleKids(im, cur)
		if len(kids) == 1 && wrapTags[kids[0].Data] && ownText(cur) == "" && !im.hasLook(kids[0]) {
			cur = kids[0]
			continue
		}
		break
	}
	var out []*html.Node
	for _, k := range visibleKids(im, cur) {
		if k.Data == "main" || (wrapTags[k.Data] && !partTags[k.Data] && len(visibleKids(im, k)) > 1 && allParts(im, k)) {
			out = append(out, visibleKids(im, k)...)
			continue
		}
		out = append(out, k)
	}
	if len(out) == 0 && strings.TrimSpace(textOf(cur)) != "" {
		out = append(out, cur)
	}
	return out
}

func visibleKids(im *importer, n *html.Node) []*html.Node {
	var out []*html.Node
	for _, k := range elementKids(n) {
		if !im.hidden(k) {
			out = append(out, k)
		}
	}
	return out
}

// allParts reports whether every child of n is itself a page part (section, header, …).
func allParts(im *importer, n *html.Node) bool {
	for _, k := range visibleKids(im, n) {
		if !partTags[k.Data] {
			return false
		}
	}
	return true
}

// hasLook reports whether an element has its own background, border or padding (a wrapper with a
// look is kept as a box).
func (im *importer) hasLook(n *html.Node) bool {
	st := im.style(n)
	for _, k := range []string{"background-color", "background-image", "border", "padding", "box-shadow"} {
		if v := st[k]; v != "" && v != "none" && v != "0" && v != "transparent" {
			return true
		}
	}
	return false
}

var partNames = map[string]string{"header": "Header", "nav": "Menu", "footer": "Footer", "form": "Form", "aside": "Sidebar"}

func (im *importer) section(n *html.Node, i int) map[string]any {
	im.nodes = 0
	items := []map[string]any{}
	els := map[string]any{}
	st := im.style(n)
	root := im.node(n, "", "column", "", &items, els, 0, true)
	if root == "" || len(items) == 0 {
		return nil
	}
	bg := normColor(st["background-color"])
	if bg == "" || !hexColorRE.MatchString(bg) {
		bg = "#ffffff"
	}
	palette := "light"
	if luminance(bg) < 0.45 {
		palette = "dark"
	}
	name := partNames[n.Data]
	if name == "" {
		if h := firstHeading(n); h != "" {
			name = h
		} else {
			name = fmt.Sprintf("Section %d", i+1)
		}
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	spec := map[string]any{
		"kind":  KindCustom,
		"name":  name,
		"items": items,
		"els":   els,
		"style": map[string]any{"palette": palette, "bg": bg, "padding": 0},
	}
	if id := attr(n, "id"); id != "" && regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`).MatchString(id) {
		spec["style"].(map[string]any)["cssId"] = id
	}
	return spec
}

// node adds n (and its subtree) and returns its item id ("" = nothing visible).
// talign is the text-align inherited from the ancestors (CSS inherits it; the editor's boxes do not).
func (im *importer) node(n *html.Node, parent, parentDir, talign string, items *[]map[string]any, els map[string]any, depth int, top bool) string {
	if n.Type != html.ElementNode || im.hidden(n) || depth > 24 {
		return ""
	}
	if im.nodes >= im.o.MaxNodes {
		im.warn(fmt.Sprintf("long parts were cut at %d elements", im.o.MaxNodes))
		return ""
	}
	st := im.style(n)
	switch st["text-align"] {
	case "left", "center", "right":
		talign = st["text-align"]
	case "start":
		talign = "left"
	case "end":
		talign = "right"
	}
	add := func(kind, title, text string, extra map[string]any) string {
		im.nodes++
		id := im.id("n")
		it := map[string]any{"id": id, "title": title, "text": text, "node": kind}
		if parent != "" {
			it["parent"] = parent
		}
		for k, v := range extra {
			it[k] = v
		}
		*items = append(*items, it)
		// Inline things (buttons, links) keep their own width in a column instead of stretching, placed
		// where the inherited text-align puts them.
		if (kind == "button" || kind == "link") && parentDir == "column" {
			self := map[string]string{"center": "center", "right": "end"}[talign]
			if self == "" {
				self = "start"
			}
			lay, _ := it["lay"].(map[string]any)
			if lay == nil {
				lay = map[string]any{}
			}
			lay["self"] = self
			it["lay"] = lay
		}
		es := im.elStyle(kind, st, n, parentDir)
		if _, has := es["align"]; !has && talign != "" && talign != "left" && kind != "box" && kind != "image" && kind != "icon" {
			es["align"] = talign
		}
		if len(es) > 0 {
			els["items."+id+".title"] = es
		}
		return id
	}
	tag := n.Data
	switch {
	case tag == "img":
		src := strings.TrimSpace(attr(n, "src"))
		if !okImageURL(src) {
			im.warn("images with unsupported sources were skipped")
			return ""
		}
		return add("image", src, attr(n, "alt"), nil)
	case tag == "svg":
		var b strings.Builder
		_ = html.Render(&b, n)
		if b.Len() > 24*1024 {
			im.warn("large inline SVGs were skipped")
			return ""
		}
		return add("icon", svgDataURL(b.String()), "", nil)
	case tag == "hr":
		return add("divider", "", "", nil)
	case tag == "input" || tag == "textarea" || tag == "select":
		t := strings.ToLower(attr(n, "type"))
		if t == "hidden" {
			return ""
		}
		if t == "submit" || t == "button" {
			return add("button", attr(n, "value"), "", nil)
		}
		ph := attr(n, "placeholder")
		if ph == "" {
			ph = attr(n, "name")
		}
		return add("input", ph, "", nil)
	case tag == "button":
		return add("button", strings.TrimSpace(textOf(n)), "", nil)
	case headingTag.MatchString(tag):
		return add("heading", im.rich(n, false), "", map[string]any{"tag": tag})
	case tag == "ul" || tag == "ol":
		if inlineOnlyList(n) {
			var b strings.Builder
			_ = html.Render(&b, n)
			return add("text", SanitizeRich(b.String()), "", nil)
		}
	case tag == "a" && !top:
		if inlineOnly(n) {
			label := strings.TrimSpace(textOf(n))
			if label == "" {
				// an image or icon link: keep the picture, linked
				if img := findTag(n, atom.Img); img != nil && okImageURL(attr(img, "src")) {
					return add("image", attr(img, "src"), attr(img, "alt"), hrefOf(n))
				}
				return ""
			}
			kind := "link"
			if im.looksLikeButton(n, st) {
				kind = "button"
				if c := normColor(st["background-color"]); hexColorRE.MatchString(c) {
					im.brands[c]++
				}
			}
			return add(kind, label, "", hrefOf(n))
		}
	}
	if !top && (tag == "p" || tag == "blockquote" || tag == "figcaption" || tag == "label" || tag == "span" || tag == "small" || tag == "strong" || tag == "em" || tag == "li" || tag == "dt" || tag == "dd" || tag == "address" || tag == "pre" || tag == "code" || wrapTags[tag] || partTags[tag] || tag == "div") && inlineOnly(n) {
		if strings.TrimSpace(textOf(n)) == "" {
			if !im.hasLook(n) {
				return ""
			}
		} else {
			return add("text", im.rich(n, true), "", nil)
		}
	}
	// A container: a box with its children.
	kids := visibleKids(im, n)
	if !top && len(kids) == 1 && !im.hasLook(n) && ownText(n) == "" && wrapTags[tag] {
		return im.node(kids[0], parent, parentDir, talign, items, els, depth+1, false)
	}
	lay, dir := im.layout(st, kids)
	id := add("box", "", "", map[string]any{"lay": lay})
	if t := strings.TrimSpace(ownText(n)); t != "" && len(kids) > 0 {
		// loose text next to elements: keep it as a text node first
		im.nodes++
		tid := im.id("n")
		*items = append(*items, map[string]any{"id": tid, "title": t, "text": "", "node": "text", "parent": id})
	}
	for _, k := range kids {
		im.node(k, id, dir, talign, items, els, depth+1, false)
	}
	if !top && len(kids) == 0 && !im.hasLook(n) {
		// nothing inside and no look: drop the empty box
		*items = (*items)[:len(*items)-1]
		delete(els, "items."+id+".title")
		return ""
	}
	return id
}

func hrefOf(n *html.Node) map[string]any {
	h := strings.TrimSpace(attr(n, "href"))
	if h == "" || !okHref(h) {
		return nil
	}
	return map[string]any{"href": h}
}

var okHrefRE = regexp.MustCompile(`(?i)^(https?:|mailto:|tel:|#|/|\./|\.\./|[a-z0-9_-]+(\.[a-z0-9]+)?([/?#].*)?$)`)

func okHref(h string) bool {
	return okHrefRE.MatchString(h) && !strings.HasPrefix(strings.ToLower(h), "javascript:")
}

func okImageURL(src string) bool {
	l := strings.ToLower(src)
	switch {
	case strings.HasPrefix(l, "https://"), strings.HasPrefix(l, "http://"), strings.HasPrefix(l, "/"):
		return true
	case strings.HasPrefix(l, "data:image/"):
		return len(src) <= 32*1024 && !strings.HasPrefix(l, "data:image/svg")
	}
	return src != "" && !strings.Contains(l, ":") && !strings.HasPrefix(l, "//")
}

func svgDataURL(svg string) string {
	r := strings.NewReplacer("%", "%25", "#", "%23", "<", "%3C", ">", "%3E", "\"", "'", "\n", " ")
	return "data:image/svg+xml," + r.Replace(svg)
}

func (im *importer) looksLikeButton(n *html.Node, st map[string]string) bool {
	cls := strings.ToLower(attr(n, "class"))
	for _, w := range []string{"btn", "button", "cta"} {
		if strings.Contains(cls, w) {
			return true
		}
	}
	if attr(n, "role") == "button" {
		return true
	}
	bg := normColor(st["background-color"])
	return bg != "" && bg != "transparent" && st["padding"] != ""
}

func inlineOnly(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (!inlineTags[c.Data] || !inlineOnly(c)) {
			return false
		}
	}
	return true
}

func inlineOnlyList(n *html.Node) bool {
	for _, li := range elementKids(n) {
		if li.Data != "li" || !inlineOnly(li) {
			return false
		}
	}
	return true
}

// rich is the inline content of n in the editor's rich-text subset (plain text when it has no
// markup worth keeping).
func (im *importer) rich(n *html.Node, keepLinks bool) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	src := b.String()
	src = strings.NewReplacer("<strong", "<b", "</strong>", "</b>", "<em", "<i", "</em>", "</i>", "<del", "<s", "</del>", "</s>").Replace(src)
	if !keepLinks {
		src = regexp.MustCompile(`(?i)</?a\b[^>]*>`).ReplaceAllString(src, "")
	}
	out := strings.TrimSpace(SanitizeRich(src))
	if !IsRich(out) {
		return collapseSpace(html.UnescapeString(out))
	}
	return out
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// layout reads how a container lays out its children: NodeLayout and its direction.
func (im *importer) layout(st map[string]string, kids []*html.Node) (map[string]any, string) {
	lay := map[string]any{}
	dir := "column"
	switch st["display"] {
	case "flex", "inline-flex":
		dir = "row"
		if strings.HasPrefix(st["flex-direction"], "column") {
			dir = "column"
		}
		if st["flex-wrap"] == "wrap" {
			lay["wrap"] = true
		}
	case "grid", "inline-grid":
		dir = "grid"
		if cols := gridCols(st["grid-template-columns"]); cols > 0 {
			lay["cols"] = cols
		} else {
			lay["cols"] = 1
		}
	}
	lay["dir"] = dir
	if g := firstLen(st["gap"]); g > 0 {
		lay["gap"] = g
	} else if g := firstLen(st["column-gap"]); g > 0 {
		lay["gap"] = g
	}
	if g := firstLen(st["row-gap"]); g > 0 {
		lay["gapY"] = g
	} else if parts := strings.Fields(st["gap"]); len(parts) == 2 {
		if g := firstLen(parts[1]); g > 0 && dir != "column" {
			lay["gap"] = g
			lay["gapY"] = firstLen(parts[0])
		}
	}
	if dir != "grid" {
		switch st["align-items"] {
		case "center":
			lay["align"] = "center"
		case "flex-end", "end":
			lay["align"] = "end"
		case "flex-start", "start":
			lay["align"] = "start"
		}
		switch st["justify-content"] {
		case "center":
			lay["justify"] = "center"
		case "flex-end", "end":
			lay["justify"] = "end"
		case "space-between", "space-around", "space-evenly":
			lay["justify"] = "space-between"
		}
		if dir == "column" && st["text-align"] == "center" && lay["align"] == nil {
			lay["align"] = "center"
		}
	}
	if dir != "column" && len(kids) > 1 {
		lay["stack"] = true // rows and grids stack on phones, as most responsive pages do
	}
	return lay, dir
}

func gridCols(v string) int {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" {
		return 0
	}
	if m := regexp.MustCompile(`repeat\(\s*(\d+)\s*,`).FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		return clampInt(n, 1, 12)
	}
	if strings.Contains(v, "auto-fit") || strings.Contains(v, "auto-fill") {
		return 3
	}
	depth, n, in := 0, 0, false
	for _, r := range v {
		switch {
		case r == '(':
			depth++
		case r == ')':
			depth--
		case r == ' ' && depth == 0:
			in = false
			continue
		}
		if !in && depth >= 0 && r != ' ' {
			if depth == 0 || r == '(' {
				n++
			}
			in = true
		}
	}
	return clampInt(n, 1, 12)
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// elStyle maps the declared style of a node to the editor's element style.
func (im *importer) elStyle(kind string, st map[string]string, n *html.Node, parentDir string) map[string]any {
	es := map[string]any{}
	if c := normColor(st["color"]); c != "" {
		es["color"] = c
	}
	if c := normColor(st["background-color"]); c != "" && c != "transparent" {
		es["bg"] = c
	}
	if bi := st["background-image"]; bi != "" && bi != "none" && cssSafeURLs(bi) {
		es["bgImage"] = bi
		if v := st["background-size"]; v != "" {
			es["bgSize"] = v
		} else {
			es["bgSize"] = "cover"
		}
		if v := st["background-position"]; v != "" {
			es["bgPos"] = v
		}
	}
	if v := st["border"]; v != "" && v != "none" && v != "0" {
		es["border"] = v
	}
	if r := firstLen(st["border-radius"]); r > 0 {
		es["radius"] = r
	}
	if v := st["box-shadow"]; v != "" && v != "none" {
		es["shadow"] = v
	}
	if p := box4(st["padding"], st); p != nil {
		es["padding"] = p
	}
	if kind != "box" {
		if f := fontFamily(st["font-family"]); f != "" {
			es["font"] = f
		}
		if v := fontSize(st["font-size"]); v > 0 {
			es["fontSize"] = v
		}
		if v := fontWeight(st["font-weight"]); v > 0 {
			es["fontWeight"] = v
		}
		if v := lineHeight(st["line-height"], st["font-size"]); v > 0 {
			es["lineHeight"] = v
		}
		if v := firstLen(st["letter-spacing"]); v != 0 {
			es["letterSpacing"] = v
		}
		switch st["text-align"] {
		case "left", "center", "right":
			es["align"] = st["text-align"]
		}
	} else if f := fontFamily(st["font-family"]); f != "" {
		es["font"] = f
	}
	switch st["text-transform"] {
	case "uppercase", "lowercase", "capitalize":
		es["transform"] = st["text-transform"]
	}
	if st["font-style"] == "italic" {
		es["italic"] = true
	}
	if strings.Contains(st["text-decoration"], "underline") {
		es["underline"] = true
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(st["opacity"]), 64); err == nil && v >= 0 && v < 1 {
		es["opacity"] = v
	}
	if v := st["object-fit"]; v == "cover" || v == "contain" {
		es["fit"] = v
	}
	if mw, unit := lenUnit(st["max-width"]); mw > 0 {
		es["maxWidth"] = mw
		if unit == "%" {
			es["maxWidthUnit"] = "%"
		}
	}
	if kind == "image" || kind == "icon" {
		if w, unit := lenUnit(st["width"]); w > 0 {
			es["width"] = w
			if unit == "%" || unit == "vw" {
				es["widthUnit"] = unit
			}
		} else if w, err := strconv.ParseFloat(attr(n, "width"), 64); err == nil && w > 0 {
			es["width"] = w
		}
	}
	if h, unit := lenUnit(st["min-height"]); h > 0 {
		es["height"] = h
		if unit == "vh" {
			es["heightUnit"] = "vh"
		}
	} else if kind == "image" {
		if h, unit := lenUnit(st["height"]); h > 0 && unit != "%" {
			es["height"] = h
		}
	}
	if m := firstLen(st["margin-top"]); m > 0 {
		es["mt"] = m
	}
	if parentDir == "column" && strings.Contains(st["margin"], "auto") && kind == "box" {
		// a centered column: keep it centered
		es["margin"] = map[string]any{"t": 0, "r": 0, "b": 0, "l": 0}
	}
	return es
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

var namedColors = map[string]string{"white": "#ffffff", "black": "#000000", "red": "#ff0000", "blue": "#0000ff", "green": "#008000", "gray": "#808080", "grey": "#808080", "orange": "#ffa500", "yellow": "#ffff00", "purple": "#800080", "navy": "#000080", "teal": "#008080", "silver": "#c0c0c0", "transparent": "transparent"}

var rgbRE = regexp.MustCompile(`^rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)\s*(?:[,/]\s*([\d.]+%?))?\s*\)$`)

// normColor returns #rrggbb, an rgba()/hsl()/color-mix() value unchanged, "transparent" or "".
func normColor(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || v == "inherit" || v == "initial" || v == "currentcolor" || v == "unset" {
		return ""
	}
	if c, ok := namedColors[v]; ok {
		return c
	}
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		switch len(h) {
		case 3:
			return "#" + string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		case 6:
			return v
		case 8:
			if h[6:] == "00" {
				return "transparent"
			}
			return v
		}
		return ""
	}
	if m := rgbRE.FindStringSubmatch(v); m != nil {
		if m[4] != "" {
			a := strings.TrimSuffix(m[4], "%")
			if f, _ := strconv.ParseFloat(a, 64); f == 0 {
				return "transparent"
			} else if (strings.HasSuffix(m[4], "%") && f < 100) || (!strings.HasSuffix(m[4], "%") && f < 1) {
				return v
			}
		}
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		return fmt.Sprintf("#%02x%02x%02x", clampInt(r, 0, 255), clampInt(g, 0, 255), clampInt(b, 0, 255))
	}
	if strings.HasPrefix(v, "hsl") || strings.HasPrefix(v, "color-mix(") || strings.HasPrefix(v, "oklch(") {
		if strings.ContainsAny(v, ";{}<>") || strings.Contains(v, "url(") {
			return ""
		}
		return v
	}
	return ""
}

func firstColor(v string) string {
	for _, tok := range regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\([^)]*\)|hsla?\([^)]*\)|\b[a-z]+\b`).FindAllString(v, -1) {
		if c := normColor(tok); c != "" {
			return c
		}
	}
	return ""
}

func luminance(hex string) float64 {
	if !hexColorRE.MatchString(hex) {
		return 1
	}
	n, _ := strconv.ParseUint(hex[1:], 16, 32)
	return (0.299*float64((n>>16)&255) + 0.587*float64((n>>8)&255) + 0.114*float64(n&255)) / 255
}

var cssURLRE = regexp.MustCompile(`url\(\s*['"]?([^'")]*)['"]?\s*\)`)

func cssSafeURLs(v string) bool {
	if strings.ContainsAny(v, ";{}<>") {
		return false
	}
	for _, m := range cssURLRE.FindAllStringSubmatch(v, -1) {
		if !okImageURL(m[1]) {
			return false
		}
	}
	return true
}

var lenRE = regexp.MustCompile(`^(-?[\d.]+)(px|rem|em|%|vh|vw)?$`)

func lenUnit(v string) (float64, string) {
	v = strings.TrimSpace(strings.ToLower(v))
	if strings.HasPrefix(v, "min(") || strings.HasPrefix(v, "clamp(") || strings.HasPrefix(v, "max(") {
		// take the largest absolute length inside
		best := 0.0
		for _, m := range regexp.MustCompile(`([\d.]+)(px|rem|em)`).FindAllStringSubmatch(v, -1) {
			if f, u := lenUnit(m[0]); u == "px" && f > best {
				best = f
			}
		}
		return best, "px"
	}
	m := lenRE.FindStringSubmatch(v)
	if m == nil {
		return 0, ""
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsNaN(f) {
		return 0, ""
	}
	switch m[2] {
	case "rem", "em":
		return math.Round(f*16*100) / 100, "px"
	case "", "px":
		return f, "px"
	}
	return f, m[2]
}

func firstLen(v string) float64 {
	f := strings.Fields(v)
	if len(f) == 0 {
		return 0
	}
	n, u := lenUnit(f[0])
	if u != "px" {
		return 0
	}
	return n
}

func box4(v string, st map[string]string) map[string]any {
	parts := strings.Fields(v)
	vals := make([]float64, 0, 4)
	for _, p := range parts {
		n, u := lenUnit(p)
		if u != "px" && n != 0 {
			n = 0
		}
		vals = append(vals, n)
	}
	var t, r, b, l float64
	switch len(vals) {
	case 1:
		t, r, b, l = vals[0], vals[0], vals[0], vals[0]
	case 2:
		t, b, r, l = vals[0], vals[0], vals[1], vals[1]
	case 3:
		t, r, l, b = vals[0], vals[1], vals[1], vals[2]
	case 4:
		t, r, b, l = vals[0], vals[1], vals[2], vals[3]
	}
	for k, p := range map[string]*float64{"padding-top": &t, "padding-right": &r, "padding-bottom": &b, "padding-left": &l} {
		if x := firstLen(st[k]); x > 0 {
			*p = x
		}
	}
	if t == 0 && r == 0 && b == 0 && l == 0 {
		return nil
	}
	return map[string]any{"t": t, "r": r, "b": b, "l": l}
}

func fontFamily(v string) string {
	for _, f := range strings.Split(v, ",") {
		f = strings.Trim(strings.TrimSpace(f), `'"`)
		switch strings.ToLower(f) {
		case "", "inherit", "sans-serif", "serif", "monospace", "system-ui", "-apple-system", "blinkmacsystemfont", "ui-sans-serif", "ui-serif":
			continue
		}
		if regexp.MustCompile(`^[A-Za-z0-9 -]{1,48}$`).MatchString(f) {
			return f
		}
	}
	return ""
}

func fontSize(v string) float64 {
	n, u := lenUnit(v)
	if u != "px" || n <= 0 || n > 400 {
		return 0
	}
	return n
}

func fontWeight(v string) float64 {
	switch strings.TrimSpace(v) {
	case "bold":
		return 700
	case "normal":
		return 400
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || n < 100 || n > 900 {
		return 0
	}
	return n
}

func lineHeight(v, size string) float64 {
	v = strings.TrimSpace(v)
	if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 && n < 4 {
		return n
	}
	if n, u := lenUnit(v); u == "px" && n > 0 {
		if fs := fontSize(size); fs > 0 {
			return math.Round(n/fs*100) / 100
		}
	}
	return 0
}

// --- helpers ---------------------------------------------------------------------------------

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func impHasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func findTag(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findTag(c, a); f != nil {
			return f
		}
	}
	return nil
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func firstHeading(n *html.Node) string {
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && headingTag.MatchString(n.Data) {
			found = collapseSpace(textOf(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
