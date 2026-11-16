package webbuilder

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ConvertCode turns an HTML document into code sections (KindCode) without a browser: the design's
// markup is kept as written, split into top-level parts the editor can reorder; every text, link
// and image gets a data-wbc id the editor edits in place; references to the design's files are
// pointed at its design kit (internal/server/webbuilder_kits.go); the head's stylesheets and scripts
// become the sections' head markup; title, description and social tags come back for the page's
// SEO settings.
//
// Wrappers the parts share (main, a div around a run of sections) are kept as each part's wrap
// chain (content.wrap, outermost first, one opening tag per line): FillCode wraps consecutive parts
// with the same chain in one copy of each wrapper, so the published page has the design's structure.

// CodeImportOptions configures ConvertCode.
type CodeImportOptions struct {
	// KitURL is the design kit's base URL with a trailing slash ("" = no kit).
	KitURL string
	// KitFiles are the kit's files (slash paths relative to the kit).
	KitFiles map[string]bool
	// Base is the folder of the HTML file inside the kit ("" for the root, "forum").
	Base string
	// LinkMap maps the design's own links (as written, or resolved inside the kit) to site URLs.
	LinkMap map[string]string
	// Split moves the page's header and footer out into their own parts (Theme Builder templates).
	Split bool
}

// CodePart is one code section of an import.
type CodePart struct {
	Name string `json:"name"`
	HTML string `json:"html"`
	Wrap string `json:"wrap,omitempty"`
}

// CodeImport is the result of ConvertCode.
type CodeImport struct {
	Head      string    `json:"head"`
	Lang      string    `json:"lang"`
	BodyClass string    `json:"bodyClass"`
	Header    *CodePart `json:"header,omitempty"`
	Footer    *CodePart `json:"footer,omitempty"`
	// FooterInPage: the footer sat inside main, where the design's CSS expects it, so it also stays
	// on this page (the footer template is for the site's other pages).
	FooterInPage bool       `json:"footerInPage,omitempty"`
	Sections     []CodePart `json:"sections"`
	SEO          ExtractSEO `json:"seo"`
	Editable     int        `json:"editable"`
	Warnings     []string   `json:"warnings,omitempty"`
	Unmapped     []string   `json:"unmappedLinks,omitempty"`
	Missing      []string   `json:"missingFiles,omitempty"`
}

var (
	codeCSSURLRE    = regexp.MustCompile(`url\(\s*(['"]?)([^'")]+)(['"]?)\s*\)`)
	codeCSSImportRE = regexp.MustCompile(`@import\s+(['"])([^'"]+)(['"])`)
	codeSchemeRE    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// codeOpaque are elements whose insides are never edited (or are not markup).
var codeOpaque = map[atom.Atom]bool{atom.Script: true, atom.Style: true, atom.Svg: true, atom.Template: true, atom.Noscript: true,
	atom.Textarea: true, atom.Select: true, atom.Iframe: true, atom.Canvas: true, atom.Video: true, atom.Audio: true, atom.Math: true}

type codeConv struct {
	o        CodeImportOptions
	out      *CodeImport
	next     int
	unmapped map[string]bool
	missing  map[string]bool
}

// ConvertCode converts a full HTML document (see the comment above).
func ConvertCode(src string, o CodeImportOptions) (*CodeImport, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil, err
	}
	if o.KitURL != "" && !strings.HasSuffix(o.KitURL, "/") {
		o.KitURL += "/"
	}
	o.Base = strings.Trim(path.Clean("/"+o.Base), "/")
	cv := &codeConv{o: o, out: &CodeImport{}, unmapped: map[string]bool{}, missing: map[string]bool{}}
	var root, head, body *html.Node
	for n := doc.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && n.DataAtom == atom.Html {
			root = n
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no html element")
	}
	cv.out.Lang = attr(root, "lang")
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		switch {
		case n.Type == html.ElementNode && n.DataAtom == atom.Head:
			head = n
		case n.Type == html.ElementNode && n.DataAtom == atom.Body:
			body = n
		}
	}
	cv.out.SEO.Lang = cv.out.Lang
	if head != nil {
		cv.out.Head = cv.head(head)
	}
	if body == nil {
		return nil, fmt.Errorf("no body element")
	}
	cv.out.BodyClass = attr(body, "class")
	cv.body(body)
	for u := range cv.unmapped {
		cv.out.Unmapped = append(cv.out.Unmapped, u)
	}
	for u := range cv.missing {
		cv.out.Missing = append(cv.out.Missing, u)
	}
	sort.Strings(cv.out.Unmapped)
	sort.Strings(cv.out.Missing)
	cv.out.Editable = cv.next
	if len(cv.out.Sections) == 0 && cv.out.Header == nil && cv.out.Footer == nil {
		return nil, fmt.Errorf("the body has no content")
	}
	return cv.out, nil
}

// head keeps what the design's head loads (stylesheets, scripts, preloads, inline styles) and takes
// the page's metadata for SEO.
func (cv *codeConv) head(h *html.Node) string {
	var lines []string
	for n := h.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != html.ElementNode {
			continue
		}
		switch n.DataAtom {
		case atom.Title:
			cv.out.SEO.Title = strings.TrimSpace(textOf(n))
		case atom.Meta:
			name, prop, content := strings.ToLower(attr(n, "name")), strings.ToLower(attr(n, "property")), attr(n, "content")
			switch {
			case name == "description":
				cv.out.SEO.Description = content
			case name == "robots":
				cv.out.SEO.Robots = content
			case prop == "og:title":
				cv.out.SEO.OGTitle = content
			case prop == "og:description":
				cv.out.SEO.OGDescription = content
			case prop == "og:image":
				cv.out.SEO.OGImage = cv.url(content, false)
			case name == "theme-color":
				lines = append(lines, render(n))
			}
		case atom.Link:
			rel := strings.ToLower(attr(n, "rel"))
			switch {
			case strings.Contains(rel, "canonical"):
				cv.out.SEO.Canonical = attr(n, "href")
			case strings.Contains(rel, "icon"), strings.Contains(rel, "alternate"), strings.Contains(rel, "manifest"):
				// the site's own (site icon, languages, feeds)
			default:
				cv.rewrite(n)
				lines = append(lines, render(n))
			}
		case atom.Style:
			cv.styleText(n)
			lines = append(lines, render(n))
		case atom.Script, atom.Noscript:
			cv.rewrite(n)
			lines = append(lines, render(n))
		case atom.Base:
			cv.out.Warnings = append(cv.out.Warnings, "the design's <base> element was dropped; its relative links are resolved against the kit instead")
		}
	}
	return strings.Join(lines, "\n")
}

// part is one piece of the body on its way to a section.
type codePiece struct {
	nodes []*html.Node
	wrap  []string
	name  string
	tag   atom.Atom
}

func (cv *codeConv) body(b *html.Node) {
	var kids []*html.Node
	for n := b.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode {
			kids = append(kids, n)
		}
	}
	mainIdx := -1
	for i, n := range kids {
		if n.DataAtom == atom.Main {
			mainIdx = i
			break
		}
	}
	var pre, header, footer []*html.Node
	var pieces []*codePiece
	var after []*html.Node
	lead := kids
	var main *html.Node
	if mainIdx >= 0 {
		lead, main, after = kids[:mainIdx], kids[mainIdx], kids[mainIdx+1:]
	}
	// What comes before the content: with Split, the preamble (sprites, skip links) and the first
	// header form the header part; the rest is content.
	var rest []*html.Node
	for i, n := range lead {
		if cv.o.Split && header == nil && len(rest) == 0 {
			if n.DataAtom == atom.Header {
				header = append(header, n)
				continue
			}
			if isPreamble(n) {
				pre = append(pre, n)
				continue
			}
		}
		if cv.o.Split && header != nil && main != nil && len(rest) == 0 && isPreamble(n) {
			pre = append(pre, lead[i])
			continue
		}
		rest = append(rest, n)
	}
	if cv.o.Split && header == nil {
		rest, pre = append(pre, rest...), nil // no header: nothing to move out
	}
	pieces = cv.split(holder(rest), nil)
	if main != nil {
		pieces = append(pieces, cv.split(main, []string{openTag(main)})...)
	}
	// Body-level parts after main: scripts go with the last part, a footer is the footer, the rest
	// are parts of their own.
	for _, n := range after {
		switch {
		case isScripty(n):
			if len(pieces) == 0 {
				pieces = append(pieces, &codePiece{})
			}
			last := pieces[len(pieces)-1]
			last.nodes = append(last.nodes, n)
		case cv.o.Split && n.DataAtom == atom.Footer && footer == nil:
			footer = append(footer, n)
		default:
			pieces = append(pieces, &codePiece{nodes: []*html.Node{n}, tag: n.DataAtom, name: partName(n, len(pieces))})
		}
	}
	// A footer that is the last part inside main (kept with its wrappers, minus main itself).
	var footerWrap []string
	if cv.o.Split && footer == nil {
		for i := len(pieces) - 1; i >= 0; i-- {
			p := pieces[i]
			if p.tag == atom.Footer {
				// The footer leaves; scripts that came with it (the page's) stay on the page.
				var stay []*html.Node
				for _, n := range p.nodes {
					if n.DataAtom == atom.Footer && footer == nil {
						footer = []*html.Node{n}
					} else if footer != nil && isScripty(n) {
						stay = append(stay, n)
					} else {
						footer = append(footer, n)
					}
				}
				for _, w := range p.wrap {
					if !strings.HasPrefix(w, "<main") {
						footerWrap = append(footerWrap, w)
					}
				}
				inMain := len(p.wrap) > 0 && strings.HasPrefix(p.wrap[0], "<main")
				if inMain {
					// It stays where the design put it (a copy goes to the template for other pages).
					cv.out.FooterInPage = true
					keep := &codePiece{nodes: footer, wrap: p.wrap, tag: atom.Footer, name: "Footer"}
					pieces = append(pieces[:i], append([]*codePiece{keep}, pieces[i+1:]...)...)
					footer = cloneNodes(footer)
				} else {
					pieces = append(pieces[:i], pieces[i+1:]...)
				}
				if len(stay) > 0 {
					if len(pieces) > 0 {
						last := pieces[len(pieces)-1]
						last.nodes = append(last.nodes, stay...)
					} else {
						pieces = append(pieces, &codePiece{nodes: stay, wrap: p.wrap})
					}
				}
				break
			}
			if p.tag != 0 { // only scripts may follow the footer
				break
			}
		}
	}
	if len(header) > 0 {
		cv.out.Header = &CodePart{Name: "Header", HTML: cv.markup(append(pre, header...))}
	}
	if len(footer) > 0 {
		cv.out.Footer = &CodePart{Name: "Footer", HTML: cv.markup(footer), Wrap: strings.Join(footerWrap, "\n")}
	}
	for i, p := range pieces {
		if len(p.nodes) == 0 {
			continue
		}
		name := p.name
		if name == "" {
			name = "Part " + strconv.Itoa(i+1)
		}
		cv.out.Sections = append(cv.out.Sections, CodePart{Name: name, HTML: cv.markup(p.nodes), Wrap: strings.Join(p.wrap, "\n")})
	}
}

// split cuts a container into parts: each element child is a part; scripts and styles go with the
// part before them; a plain wrapper around a run of sections is opened (kept as a wrap).
func (cv *codeConv) split(container *html.Node, wrap []string) []*codePiece {
	var out []*codePiece
	var pending []*html.Node
	for n := container.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != html.ElementNode {
			continue
		}
		if len(wrap) == 0 && isPreamble(n) && !isScripty(n) && n.NextSibling != nil {
			pending = append(pending, n) // sprites and skip links go with the part they lead into
			continue
		}
		if isScripty(n) {
			if len(out) == 0 {
				pending = append(pending, n)
			} else {
				out[len(out)-1].nodes = append(out[len(out)-1].nodes, n)
			}
			continue
		}
		if isRunWrapper(n) {
			inner := cv.split(n, append(append([]string{}, wrap...), openTag(n)))
			if len(pending) > 0 && len(inner) > 0 {
				inner[0].nodes = append(pending, inner[0].nodes...)
				pending = nil
			}
			out = append(out, inner...)
			continue
		}
		p := &codePiece{nodes: append(pending, n), wrap: wrap, tag: n.DataAtom, name: partName(n, len(out))}
		pending = nil
		out = append(out, p)
	}
	if len(pending) > 0 {
		out = append(out, &codePiece{nodes: pending, wrap: wrap})
	}
	return out
}

// isRunWrapper: a div (or other plain container) holding two or more sections (section, article,
// header, footer, aside, or elements with an id), opened so each section is a part of its own.
func isRunWrapper(n *html.Node) bool {
	if n.DataAtom != atom.Div {
		return false
	}
	secs := 0
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type != html.ElementNode || isScripty(k) {
			continue
		}
		switch k.DataAtom {
		case atom.Section, atom.Article, atom.Header, atom.Footer, atom.Aside, atom.Nav:
			secs++
		default:
			if attr(k, "id") != "" {
				secs++
			} else {
				return false
			}
		}
	}
	return secs >= 2
}

// cloneNodes deep-copies nodes (one tree is rendered for the page, the copy for a template).
func cloneNodes(nodes []*html.Node) []*html.Node {
	out := make([]*html.Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, cloneNode(n))
	}
	return out
}

func cloneNode(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace, Attr: append([]html.Attribute{}, n.Attr...)}
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		c.AppendChild(cloneNode(k))
	}
	return c
}

// holder is a detached container of nodes (moved out of their parent) for split.
func holder(nodes []*html.Node) *html.Node {
	h := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range nodes {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
		h.AppendChild(n)
	}
	return h
}

func isScripty(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Link, atom.Template, atom.Noscript:
		return true
	}
	return false
}

// isPreamble: things a page starts with before its header (icon sprites, skip links).
func isPreamble(n *html.Node) bool {
	if n.DataAtom == atom.Svg {
		return true
	}
	if n.DataAtom == atom.A && strings.HasPrefix(attr(n, "href"), "#") {
		return true
	}
	return isScripty(n)
}

func partName(n *html.Node, i int) string {
	switch n.DataAtom {
	case atom.Header:
		return "Header"
	case atom.Footer:
		return "Footer"
	}
	if v := strings.TrimSpace(attr(n, "aria-label")); v != "" {
		return truncName(v)
	}
	var heading string
	var find func(*html.Node)
	find = func(x *html.Node) {
		for k := x.FirstChild; k != nil && heading == ""; k = k.NextSibling {
			if k.Type != html.ElementNode {
				continue
			}
			switch k.DataAtom {
			case atom.H1, atom.H2, atom.H3:
				heading = strings.Join(strings.Fields(textOf(k)), " ")
			default:
				if !codeOpaque[k.DataAtom] {
					find(k)
				}
			}
		}
	}
	find(n)
	if heading != "" {
		return truncName(heading)
	}
	if id := attr(n, "id"); id != "" {
		return "#" + id
	}
	if cls := strings.Fields(attr(n, "class")); len(cls) > 0 {
		return n.Data + "." + cls[0]
	}
	return n.Data + " " + strconv.Itoa(i+1)
}

func truncName(v string) string {
	r := []rune(v)
	if len(r) > 48 {
		return string(r[:47]) + "…"
	}
	return v
}

// markup renders nodes after giving their editable elements ids and pointing their files at the kit.
func (cv *codeConv) markup(nodes []*html.Node) string {
	var b strings.Builder
	for i, n := range nodes {
		cv.walk(n, false)
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(render(n))
	}
	return b.String()
}

func (cv *codeConv) walk(n *html.Node, inSVG bool) {
	if n.Type != html.ElementNode {
		return
	}
	cv.rewrite(n)
	if n.DataAtom == atom.Style {
		cv.styleText(n)
	}
	// Every element gets an id (the svg itself, not its drawing): the editor selects, edits, moves,
	// duplicates and styles elements as blocks by it.
	if !inSVG && codeTaggable(n) {
		cv.next++
		n.Attr = append(n.Attr, html.Attribute{Key: AttrCodeID, Val: strconv.Itoa(cv.next)})
	}
	svg := inSVG || n.DataAtom == atom.Svg
	if codeOpaque[n.DataAtom] && n.DataAtom != atom.Svg {
		return
	}
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		cv.walk(k, svg)
	}
}

var codeURLAttrs = map[string]bool{"href": true, "src": true, "poster": true, "data-src": true, "data-bg": true, "data-background": true, "action": true, "xlink:href": true}
var codeSrcsetAttrs = map[string]bool{"srcset": true, "data-srcset": true, "imagesrcset": true}

// rewrite points an element's URL attributes at the kit (or the link map) and its style attribute's
// url() references too.
func (cv *codeConv) rewrite(n *html.Node) {
	for i, a := range n.Attr {
		key := a.Key
		if a.Namespace != "" {
			key = a.Namespace + ":" + a.Key
		}
		switch {
		case codeURLAttrs[key]:
			page := (n.DataAtom == atom.A || n.DataAtom == atom.Area || n.DataAtom == atom.Form) && key != "src"
			n.Attr[i].Val = cv.url(a.Val, page)
		case codeSrcsetAttrs[key]:
			n.Attr[i].Val = cv.srcset(a.Val)
		case key == "style" && strings.Contains(a.Val, "url("):
			n.Attr[i].Val = cv.css(a.Val)
		}
	}
}

func (cv *codeConv) styleText(n *html.Node) {
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == html.TextNode {
			k.Data = cv.css(k.Data)
		}
	}
}

func (cv *codeConv) css(src string) string {
	src = codeCSSURLRE.ReplaceAllStringFunc(src, func(m string) string {
		g := codeCSSURLRE.FindStringSubmatch(m)
		return "url(" + g[1] + cv.url(g[2], false) + g[3] + ")"
	})
	return codeCSSImportRE.ReplaceAllStringFunc(src, func(m string) string {
		g := codeCSSImportRE.FindStringSubmatch(m)
		return "@import " + g[1] + cv.url(g[2], false) + g[3]
	})
}

func (cv *codeConv) srcset(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		f := strings.Fields(p)
		if len(f) == 0 {
			continue
		}
		f[0] = cv.url(f[0], false)
		parts[i] = strings.Join(f, " ")
	}
	return strings.Join(parts, ", ")
}

// url maps one reference: a file of the kit → its kit URL; a link of the link map → its site URL;
// anything else is kept (and reported when it is a relative page link or a missing file).
func (cv *codeConv) url(raw string, page bool) string {
	u := strings.TrimSpace(raw)
	if u == "" || strings.HasPrefix(u, "#") || strings.HasPrefix(u, "//") || codeSchemeRE.MatchString(u) {
		return raw
	}
	whole := u
	frag := ""
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u, frag = u[:i], u[i:]
	}
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	var rel string
	if strings.HasPrefix(u, "/") {
		rel = strings.TrimPrefix(path.Clean(u), "/")
	} else {
		rel = strings.TrimPrefix(path.Clean("/"+path.Join(cv.o.Base, u)), "/")
	}
	// The link map: by the path resolved inside the kit first (unambiguous), then as written.
	if v, ok := cv.o.LinkMap[rel]; ok && u != "" {
		return v + frag
	}
	if v, ok := cv.o.LinkMap[whole]; ok {
		return v
	}
	if v, ok := cv.o.LinkMap[u]; ok && u != "" {
		return v + frag
	}
	if cv.o.KitURL != "" && cv.o.KitFiles[rel] {
		return cv.o.KitURL + rel + frag
	}
	ext := strings.ToLower(path.Ext(rel))
	switch {
	case page || ext == ".html" || ext == ".htm" || ext == "" || ext == ".php":
		if !strings.HasPrefix(u, "/") || ext == ".html" || ext == ".htm" {
			cv.unmapped[raw] = true
		}
	default:
		cv.missing[rel] = true
	}
	return raw
}

func openTag(n *html.Node) string {
	var b strings.Builder
	b.WriteString("<" + n.Data)
	for _, a := range n.Attr {
		key := a.Key
		if a.Namespace != "" {
			key = a.Namespace + ":" + a.Key
		}
		b.WriteString(" " + key + `="` + html.EscapeString(a.Val) + `"`)
	}
	b.WriteString(">")
	return b.String()
}

func render(n *html.Node) string {
	var b strings.Builder
	_ = html.Render(&b, n)
	return b.String()
}
