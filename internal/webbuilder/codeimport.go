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
	// Remap gives files of the design that are not in the kit a URL of their own (a file too large
	// for a kit put into the Media Library): kit path → URL.
	Remap map[string]string
	// OldHosts are the domain(s) the design was saved from (DetectOldHosts): their URLs are paths
	// of this site (oldsite.go). KeepOldLinks leaves links, sources and other attributes pointing
	// there; form actions and the URLs in inline scripts point at this site (SiteURL, the site's
	// home URL; "" makes them root-relative) either way.
	OldHosts     []string
	KeepOldLinks bool
	SiteURL      string
	// FooterAt (Split) points at the page's site footer when it has no <footer> element: the
	// element-child indexes from <body> down to it (ConvertSite finds the block every page ends
	// with, Salient's #footer-outer).
	FooterAt []int
}

// CodePart is one code section of an import.
type CodePart struct {
	Name string `json:"name"`
	HTML string `json:"html"`
	Wrap string `json:"wrap,omitempty"`
	// Tail: the page's scripts after its shell (body-end scripts, with Split and a footer): the
	// front end prints them after the footer template, where the design had them (content.tail).
	Tail bool `json:"tail,omitempty"`
	// Binds are the part's content bindings (content.binds, codebind.go) a site import made.
	Binds string `json:"binds,omitempty"`
}

// CodeImport is the result of ConvertCode.
type CodeImport struct {
	Head      string `json:"head"`
	Lang      string `json:"lang"`
	BodyClass string `json:"bodyClass"`
	// HTMLAttrs and BodyAttrs are the other attributes of <html> (lang aside) and <body> (class
	// included), as written in an opening tag (`class="js" data-theme="dark"`); the front end
	// prints them (CodeDocAttrs).
	HTMLAttrs string `json:"htmlAttrs,omitempty"`
	BodyAttrs string `json:"bodyAttrs,omitempty"`
	// Shell is the wrappers between the body and the element holding the site header, main and
	// footer (opening tags, outermost first, one per line): every part's wrap starts with it.
	Shell  string    `json:"shell,omitempty"`
	Header *CodePart `json:"header,omitempty"`
	Footer *CodePart `json:"footer,omitempty"`
	// HeaderBefore and FooterAfter (Split): elements beside the shell's wrappers (an icon sprite
	// before it, a modal after it), parts of the header and footer templates before and after
	// Header and Footer.
	HeaderBefore []CodePart `json:"headerBefore,omitempty"`
	FooterAfter  []CodePart `json:"footerAfter,omitempty"`
	// FooterInPage: the footer sat inside main, where the design's CSS expects it, so it also stays
	// on this page (the footer template is for the site's other pages).
	FooterInPage bool       `json:"footerInPage,omitempty"`
	Sections     []CodePart `json:"sections"`
	SEO          ExtractSEO `json:"seo"`
	Editable     int        `json:"editable"`
	Warnings     []string   `json:"warnings,omitempty"`
	Unmapped     []string   `json:"unmappedLinks,omitempty"`
	Missing      []string   `json:"missingFiles,omitempty"`
	// OldURLs are the URLs of the design's original site (OldHosts) the page held, as written (in
	// scripts: their origin); OldForms the forms whose action pointed there; OldInScripts how many
	// script URLs now point at this site; Trackers the third-party tracking the page loads.
	OldURLs      []string `json:"oldUrls,omitempty"`
	OldForms     int      `json:"oldForms,omitempty"`
	OldInScripts int      `json:"oldInScripts,omitempty"`
	Trackers     []string `json:"trackers,omitempty"`
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
	handlers map[string]int // inline event handler attributes: "onclick on <button>" → count
	// The design's original site (oldsite.go).
	oldHost      map[string]bool
	oldRE        *regexp.Regexp
	oldURLs      map[string]bool
	oldForms     int
	oldInScripts int
	tracking     map[string]bool
}

// newCodeConv is a converter with o's defaults applied.
func newCodeConv(o CodeImportOptions) *codeConv {
	if o.KitURL != "" && !strings.HasSuffix(o.KitURL, "/") {
		o.KitURL += "/"
	}
	o.Base = strings.Trim(path.Clean("/"+o.Base), "/")
	cv := &codeConv{o: o, out: &CodeImport{}, unmapped: map[string]bool{}, missing: map[string]bool{}, handlers: map[string]int{},
		oldHost: map[string]bool{}, oldURLs: map[string]bool{}, tracking: map[string]bool{}}
	for _, h := range o.OldHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			cv.oldHost[h] = true
		}
	}
	if len(cv.oldHost) > 0 {
		hosts := make([]string, 0, len(cv.oldHost))
		for h := range cv.oldHost {
			hosts = append(hosts, h)
		}
		cv.oldRE = oldScriptRE(hosts)
	}
	return cv
}

// ConvertCode converts a full HTML document (see the comment above).
func ConvertCode(src string, o CodeImportOptions) (*CodeImport, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil, err
	}
	cv := newCodeConv(o)
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
	cv.out.HTMLAttrs = attrString(root, "lang")
	cv.out.BodyAttrs = attrString(body)
	cv.body(body)
	for u := range cv.unmapped {
		cv.out.Unmapped = append(cv.out.Unmapped, u)
	}
	for u := range cv.missing {
		cv.out.Missing = append(cv.out.Missing, u)
	}
	sort.Strings(cv.out.Unmapped)
	sort.Strings(cv.out.Missing)
	for u := range cv.oldURLs {
		cv.out.OldURLs = append(cv.out.OldURLs, u)
	}
	sort.Strings(cv.out.OldURLs)
	cv.out.OldForms, cv.out.OldInScripts = cv.oldForms, cv.oldInScripts
	for t := range cv.tracking {
		cv.out.Trackers = append(cv.out.Trackers, t)
	}
	sort.Strings(cv.out.Trackers)
	if w := handlerWarning(cv.handlers); w != "" {
		cv.out.Warnings = append(cv.out.Warnings, w)
	}
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
	sh := findShell(b, codeNodeAt(b, cv.o.FooterAt))
	var chain []string
	for _, n := range sh.chain {
		chain = append(chain, openTag(n))
	}
	cv.out.Shell = strings.Join(chain, "\n")
	shell := b
	if len(sh.chain) > 0 {
		shell = sh.chain[len(sh.chain)-1]
	}
	var kids []*html.Node
	for n := shell.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode {
			kids = append(kids, n)
		}
	}
	// main: a child of the shell, or inside one through wrappers that hold nothing else.
	var main *html.Node
	mainIdx := -1
	mainWrap := append([]string{}, chain...)
	if sh.main != nil {
		m0 := childOf(shell, sh.main)
		if extra, ok := plainPath(m0, sh.main); ok {
			main = sh.main
			mainWrap = append(mainWrap, extra...)
			for i, k := range kids {
				if k == m0 {
					mainIdx = i
				}
			}
		}
	}
	if mainIdx < 0 {
		main = nil
	}
	var h0, f0 *html.Node
	if sh.header != nil {
		h0 = childOf(shell, sh.header)
	}
	if sh.footer != nil {
		f0 = childOf(shell, sh.footer)
	}
	var pre, header, footer []*html.Node
	var pieces []*codePiece
	var after []*html.Node
	lead := kids
	if main != nil {
		lead, after = kids[:mainIdx], kids[mainIdx+1:]
	}
	// What comes before the content: with Split, everything before the site's header (skip links,
	// icon sprites, an announcement bar) and the header form the header part; the rest is content.
	var rest []*html.Node
	hi := -1
	if cv.o.Split && h0 != nil {
		for i, n := range lead {
			if n == h0 {
				hi = i
			}
		}
	}
	if hi >= 0 {
		pre, header = append(pre, lead[:hi]...), []*html.Node{h0}
		for _, n := range lead[hi+1:] {
			if main != nil && len(rest) == 0 && isPreamble(n) {
				pre = append(pre, n)
				continue
			}
			rest = append(rest, n)
		}
	} else {
		rest = lead
	}
	// A page without main whose site footer is a block the content's wrapper also holds (Salient:
	// #ajax-content-wrap > [.container-wrap, …, #footer-outer, the off-canvas menu]): the wrappers
	// down to the footer are opened, what precedes the footer in them is content (in those
	// wrappers), the footer and what follows it in its wrapper the footer part, and what follows
	// the wrappers the footer template's further parts.
	var footerMore []*codePiece
	footerWrap := chain
	fi := -1
	if cv.o.Split && main == nil && f0 != nil && sh.footer != nil {
		for i, n := range rest {
			if n == f0 {
				fi = i
			}
		}
	}
	if fi >= 0 {
		pieces = cv.split(holder(rest[:fi]), chain, true)
		wrap := append([]string{}, chain...)
		cur := f0
		var levels [][]*html.Node // what follows the path at each level, outermost first
		levels = append(levels, rest[fi+1:])
		for cur != sh.footer {
			next := childOf(cur, sh.footer)
			wrap = append(wrap, openTag(cur))
			var before, after []*html.Node
			seen := false
			for k := cur.FirstChild; k != nil; k = k.NextSibling {
				switch {
				case k.Type != html.ElementNode:
				case k == next:
					seen = true
				case seen:
					after = append(after, k)
				default:
					before = append(before, k)
				}
			}
			if len(before) > 0 {
				pieces = append(pieces, cv.split(holder(before), append([]string{}, wrap...), false)...)
			}
			levels = append(levels, after)
			cur = next
		}
		footer = []*html.Node{sh.footer}
		footer = append(footer, levels[len(levels)-1]...)
		footerWrap = wrap
		for li := len(levels) - 2; li >= 0; li-- {
			w := append([]string{}, wrap[:len(chain)+li]...)
			for _, n := range levels[li] {
				footerMore = appendPiece(footerMore, n, w)
			}
		}
	} else {
		pieces = cv.split(holder(rest), chain, true)
	}
	if main != nil {
		pieces = append(pieces, cv.split(main, append(append([]string{}, mainWrap...), openTag(main)), false)...)
	}
	lastPiece := func() *codePiece {
		if len(pieces) == 0 {
			pieces = append(pieces, &codePiece{wrap: chain})
		}
		return pieces[len(pieces)-1]
	}
	// Parts after main: scripts go with the last part, the site's footer is the footer, what follows
	// the footer (a to-top button, modal and lightbox containers) goes with it; the rest are parts.
	var tail []*html.Node
	for _, n := range after {
		switch {
		case isScripty(n):
			last := lastPiece()
			last.nodes = append(last.nodes, n)
		case cv.o.Split && footer == nil && (n == f0 || n.DataAtom == atom.Footer):
			footer = append(footer, n)
		case cv.o.Split && footer != nil:
			tail = append(tail, n)
		default:
			pieces = append(pieces, &codePiece{nodes: []*html.Node{n}, wrap: chain, tag: n.DataAtom, name: partName(n, len(pieces))})
		}
	}
	// A footer that is the last part (inside main, or of a page without main), kept with its
	// wrappers minus main itself.
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
				inMain := main != nil && len(p.wrap) > len(mainWrap) && strings.HasPrefix(p.wrap[len(mainWrap)], "<main")
				footerWrap = nil
				for j, w := range p.wrap {
					if !inMain || j != len(mainWrap) {
						footerWrap = append(footerWrap, w)
					}
				}
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
	// Elements beside the shell's wrappers (in the body, or in an outer wrapper): with Split, those
	// before go with the header template and those after with the footer template, each part
	// keeping the wrappers it sat in; scripts after the shell go with the last part.
	var before, beyond, tailParts []*codePiece
	beyond = footerMore
	for i, c := range sh.chain {
		w := append([]string{}, chain[:i]...)
		for n := c.Parent.FirstChild; n != nil && n != c; n = n.NextSibling {
			if n.Type == html.ElementNode {
				before = appendPiece(before, n, w)
			}
		}
	}
	for i := len(sh.chain) - 1; i >= 0; i-- {
		w := append([]string{}, chain[:i]...)
		for n := sh.chain[i].NextSibling; n != nil; n = n.NextSibling {
			if n.Type != html.ElementNode {
				continue
			}
			if isScripty(n) {
				if cv.o.Split && len(footer) > 0 {
					// After the footer, as in the design: printed after the footer template.
					tailParts = appendPiece(tailParts, n, nil)
					continue
				}
				last := lastPiece()
				last.nodes = append(last.nodes, n)
				continue
			}
			beyond = appendPiece(beyond, n, w)
		}
	}
	// A page's structured data in its header or footer (JSON-LD: the page's own SEO data) goes to
	// the page's head, so the templates shared by the site's pages do not carry one page's data.
	if cv.o.Split && (len(header) > 0 || len(footer) > 0) {
		var lifted []string
		lift := func(nodes []*html.Node) {
			for _, n := range nodes {
				for _, sc := range codeFindAll(n, func(x *html.Node) bool {
					return x.DataAtom == atom.Script && strings.EqualFold(strings.TrimSpace(attr(x, "type")), "application/ld+json")
				}) {
					if sc == n || sc.Parent == nil {
						continue // a top-level script is handled below
					}
					sc.Parent.RemoveChild(sc)
					cv.rewrite(sc)
					lifted = append(lifted, render(sc))
				}
			}
		}
		keepTop := func(nodes []*html.Node) []*html.Node {
			out := nodes[:0]
			for _, n := range nodes {
				if n.DataAtom == atom.Script && strings.EqualFold(strings.TrimSpace(attr(n, "type")), "application/ld+json") {
					cv.rewrite(n)
					lifted = append(lifted, render(n))
					continue
				}
				out = append(out, n)
			}
			return out
		}
		if len(header) > 0 {
			lift(pre)
			lift(header)
			pre = keepTop(pre)
			for _, p := range before {
				lift(p.nodes)
				p.nodes = keepTop(p.nodes)
			}
		}
		if len(footer) > 0 {
			lift(footer)
			footer = keepTop(footer)
			if len(footer) == 0 {
				footer = nil
			}
		}
		if len(lifted) > 0 {
			if cv.out.Head != "" {
				cv.out.Head += "\n"
			}
			cv.out.Head += strings.Join(lifted, "\n")
		}
		var keep []*codePiece
		for _, p := range before {
			if len(p.nodes) > 0 {
				keep = append(keep, p)
			}
		}
		before = keep
	}
	if len(header) > 0 {
		for _, p := range before {
			cv.out.HeaderBefore = append(cv.out.HeaderBefore, CodePart{Name: partName(p.nodes[0], 0), HTML: cv.markup(p.nodes), Wrap: strings.Join(p.wrap, "\n")})
		}
		cv.out.Header = &CodePart{Name: "Header", HTML: cv.markup(append(pre, header...)), Wrap: strings.Join(chain, "\n")}
	} else {
		pieces = append(before, pieces...)
	}
	if len(footer) > 0 {
		cv.out.Footer = &CodePart{Name: "Footer", HTML: cv.markup(append(footer, tail...)), Wrap: strings.Join(footerWrap, "\n")}
		for _, p := range beyond {
			cv.out.FooterAfter = append(cv.out.FooterAfter, CodePart{Name: partName(p.nodes[0], 0), HTML: cv.markup(p.nodes), Wrap: strings.Join(p.wrap, "\n")})
		}
	} else {
		pieces = append(pieces, beyond...)
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
	if len(cv.out.Sections) > 0 || cv.out.Footer == nil {
		for _, p := range tailParts {
			cv.out.Sections = append(cv.out.Sections, CodePart{Name: "Scripts", HTML: cv.markup(p.nodes), Tail: true})
		}
	} else if len(tailParts) > 0 { // a page of a header and footer only: its scripts go with the footer
		for _, p := range tailParts {
			cv.out.FooterAfter = append(cv.out.FooterAfter, CodePart{Name: "Scripts", HTML: cv.markup(p.nodes)})
		}
	}
}

// appendPiece adds n to the last piece when it has the same wrappers, else starts a piece.
func appendPiece(list []*codePiece, n *html.Node, wrap []string) []*codePiece {
	if k := len(list); k > 0 && strings.Join(list[k-1].wrap, "\n") == strings.Join(wrap, "\n") {
		list[k-1].nodes = append(list[k-1].nodes, n)
		return list
	}
	return append(list, &codePiece{nodes: []*html.Node{n}, wrap: wrap, tag: n.DataAtom, name: partName(n, len(list))})
}

// codeShell is where a page's site header, main and footer sit (findShell).
type codeShell struct {
	// chain: the wrappers from the body's child down to the element holding header, main and
	// footer (empty when that is the body).
	chain                []*html.Node
	main, header, footer *html.Node
}

// codeSectioning are the elements whose header and footer belong to them, not to the site.
var codeSectioning = map[atom.Atom]bool{atom.Article: true, atom.Section: true, atom.Aside: true, atom.Nav: true,
	atom.Main: true, atom.Header: true, atom.Footer: true, atom.Dialog: true, atom.Form: true}

// findShell finds the page's main (the first <main>), its site header (the first <header> before
// main that no sectioning element holds) and footer (the first such <footer> after main; the last
// one of a page without main) at any depth, and the nearest element that holds all of them: the
// wrappers from the body down to it are the shell chain (a design's
// <div class="flex min-h-screen flex-col"> around its header, main and footer).
func findShell(b, footerHint *html.Node) codeShell {
	var sh codeShell
	var footers []*html.Node
	afterMain := map[*html.Node]bool{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			if k.Type != html.ElementNode || codeOpaque[k.DataAtom] {
				continue
			}
			switch k.DataAtom {
			case atom.Main:
				if sh.main == nil {
					sh.main = k
				}
				continue
			case atom.Header:
				if sh.header == nil && sh.main == nil {
					sh.header = k
				}
				continue
			case atom.Footer:
				footers = append(footers, k)
				afterMain[k] = sh.main != nil
				continue
			}
			if codeSectioning[k.DataAtom] {
				continue
			}
			walk(k)
		}
	}
	walk(b)
	for _, f := range footers {
		if sh.main != nil && afterMain[f] {
			sh.footer = f
			break
		}
	}
	if sh.main == nil && len(footers) > 0 {
		sh.footer = footers[len(footers)-1]
	}
	// A design without a <footer> element: the block every page of the site ends with.
	if sh.footer == nil && footerHint != nil && (sh.header == nil || !codeContains(sh.header, footerHint) && !codeContains(footerHint, sh.header)) &&
		(sh.main == nil || !codeContains(sh.main, footerHint) && !codeContains(footerHint, sh.main)) {
		sh.footer = footerHint
	}
	var paths [][]*html.Node
	for _, n := range []*html.Node{sh.main, sh.header, sh.footer} {
		if n == nil {
			continue
		}
		var up []*html.Node
		for p := n.Parent; p != nil && p != b; p = p.Parent {
			up = append([]*html.Node{p}, up...)
		}
		paths = append(paths, up)
	}
	if len(paths) == 0 {
		return sh
	}
	common := paths[0]
	for _, p := range paths[1:] {
		k := 0
		for k < len(common) && k < len(p) && common[k] == p[k] {
			k++
		}
		common = common[:k]
	}
	sh.chain = common
	return sh
}

// codeFindAll lists the elements under n (n included) that match.
func codeFindAll(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(x *html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && match(x) {
			out = append(out, x)
		}
		for k := x.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	walk(n)
	return out
}

// codeContains reports whether n is a or inside a.
func codeContains(a, n *html.Node) bool {
	for ; n != nil; n = n.Parent {
		if n == a {
			return true
		}
	}
	return false
}

// codeNodeAt is the element at path (element-child indexes from b), nil when there is none.
func codeNodeAt(b *html.Node, path []int) *html.Node {
	if b == nil || len(path) == 0 {
		return nil
	}
	n := b
	for _, i := range path {
		k := 0
		var next *html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if k == i {
				next = c
				break
			}
			k++
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// codePathOf is the path of n from b (codeNodeAt's), nil when n is not inside b.
func codePathOf(b, n *html.Node) []int {
	var out []int
	for ; n != nil && n != b; n = n.Parent {
		if n.Parent == nil {
			return nil
		}
		k := 0
		for c := n.Parent.FirstChild; c != nil && c != n; c = c.NextSibling {
			if c.Type == html.ElementNode {
				k++
			}
		}
		out = append([]int{k}, out...)
	}
	if n != b {
		return nil
	}
	return out
}

// childOf is the child of parent that is or holds n.
func childOf(parent, n *html.Node) *html.Node {
	for n != nil && n.Parent != parent {
		n = n.Parent
	}
	return n
}

// plainPath reports whether main sits in top through wrappers that hold nothing else, and gives
// their opening tags (outermost first; none when top is main).
func plainPath(top, main *html.Node) ([]string, bool) {
	var out []string
	for n := main; n != top; n = n.Parent {
		p := n.Parent
		for k := p.FirstChild; k != nil; k = k.NextSibling {
			if k.Type == html.ElementNode && k != n {
				return nil, false
			}
		}
		out = append([]string{openTag(p)}, out...)
	}
	return out, true
}

// attrString is an element's attributes as written in an opening tag (` a="b" c="d"`), without
// the ones named in skip.
func attrString(n *html.Node, skip ...string) string {
	var b strings.Builder
	for _, a := range n.Attr {
		key := a.Key
		if a.Namespace != "" {
			key = a.Namespace + ":" + a.Key
		}
		drop := false
		for _, s := range skip {
			drop = drop || strings.EqualFold(key, s)
		}
		if !drop {
			b.WriteString(" " + key + `="` + html.EscapeString(a.Val) + `"`)
		}
	}
	return strings.TrimSpace(b.String())
}

// split cuts a container into parts: each element child is a part; scripts and styles go with the
// part before them; a plain wrapper around a run of sections is opened (kept as a wrap).
func (cv *codeConv) split(container *html.Node, wrap []string, top bool) []*codePiece {
	var out []*codePiece
	var pending []*html.Node
	for n := container.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != html.ElementNode {
			continue
		}
		if top && isPreamble(n) && !isScripty(n) && n.NextSibling != nil {
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
			inner := cv.split(n, append(append([]string{}, wrap...), openTag(n)), false)
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
	if n.DataAtom == atom.Video || n.DataAtom == atom.Audio {
		// Not edited inside, but its <source> and <track> elements name the design's files.
		cv.rewriteAll(n)
		return
	}
	if codeOpaque[n.DataAtom] && n.DataAtom != atom.Svg {
		return
	}
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		cv.walk(k, svg)
	}
}

// rewriteAll rewrites the URL attributes of n's descendants (no editor ids).
func (cv *codeConv) rewriteAll(n *html.Node) {
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == html.ElementNode {
			cv.rewrite(k)
			cv.rewriteAll(k)
		}
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
		if isEventHandler(key) {
			cv.handlers[strings.ToLower(key)+" on <"+n.Data+">"]++
		}
		switch {
		case n.DataAtom == atom.Form && key == "action":
			// A form posting to the original site posts to this site's same path (never there).
			val := a.Val
			if l, ok := cv.oldLocal(val); ok {
				val = l
				cv.oldForms++
			}
			n.Attr[i].Val = cv.urlWith(val, true, false)
		case codeURLAttrs[key]:
			page := (n.DataAtom == atom.A || n.DataAtom == atom.Area || n.DataAtom == atom.Form) && key != "src"
			n.Attr[i].Val = cv.url(a.Val, page)
		case codeSrcsetAttrs[key]:
			n.Attr[i].Val = cv.srcset(a.Val)
		case key == "style" && strings.Contains(a.Val, "url("):
			n.Attr[i].Val = cv.css(a.Val)
		case strings.HasPrefix(key, "data-"):
			// The design's own data attributes naming its files (data-wide="../assets/film.mp4",
			// data-poster, a lazy loader's data-nectar-img-srcset list): its scripts read them and
			// resolve them against the page's URL, which is not the kit's folder.
			n.Attr[i].Val = cv.dataValue(a.Val)
		}
	}
	cv.trackers(n)
	if n.DataAtom == atom.Script {
		cv.oldScript(n)
	}
	if n.DataAtom == atom.Script && strings.EqualFold(strings.TrimSpace(attr(n, "type")), "importmap") {
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			if k.Type == html.TextNode {
				k.Data = cv.importMap(k.Data)
			}
		}
	}
}

// codeImportMapRE: the string values of an import map ("three": "/assets/vendor/three.module.js").
var codeImportMapRE = regexp.MustCompile(`(:\s*")([^"\\]+)(")`)

// importMap points the URLs an import map maps to (its values, never its specifier keys) at the kit
// when they name kit files: a root-relative "/assets/vendor/three.module.js" resolves against the
// site's root, a relative one against the page's URL, neither of which is the kit's folder.
func (cv *codeConv) importMap(src string) string {
	return codeImportMapRE.ReplaceAllStringFunc(src, func(m string) string {
		g := codeImportMapRE.FindStringSubmatch(m)
		if v := g[2]; strings.HasSuffix(v, "/") && !strings.HasPrefix(v, "//") && !codeSchemeRE.MatchString(v) && cv.o.KitURL != "" {
			// A folder mapping ("three/addons/": "/assets/vendor/addons/"): when the kit has files in it.
			dir := strings.TrimPrefix(path.Clean("/"+v), "/")
			if !strings.HasPrefix(v, "/") {
				dir = strings.TrimPrefix(path.Clean("/"+path.Join(cv.o.Base, v)), "/")
			}
			for f := range cv.o.KitFiles {
				if dir != "" && strings.HasPrefix(f, dir+"/") {
					return g[1] + cv.o.KitURL + dir + "/" + g[3]
				}
			}
			return m
		}
		return g[1] + cv.kitFile(g[2]) + g[3]
	})
}

// codeAssetExts are the extensions of files a design references (a data-* value with one of them
// that names no kit file is reported as a missing file).
var codeAssetExts = map[string]bool{".css": true, ".js": true, ".mjs": true, ".json": true, ".woff2": true, ".woff": true, ".ttf": true,
	".otf": true, ".eot": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true,
	".ico": true, ".mp4": true, ".webm": true, ".mp3": true, ".ogg": true, ".wav": true, ".glb": true, ".gltf": true, ".wasm": true, ".pdf": true}

// codeSrcsetDescRE is a srcset candidate's descriptor (300w, 2x, 1.5x).
var codeSrcsetDescRE = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?[wx]$`)

// codeJSONStringRE: the string values of a JSON list or object in a data-* attribute.
var codeJSONStringRE = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// dataValue maps the value of a data-* attribute: the path of a kit file (or of a remapped file)
// → its URL; a srcset-like list ("a.png 1489w, b.png 300w", a lazy loader's data-*-srcset) of
// which a candidate names a kit file → every candidate mapped (the others reported missing); a
// JSON list or object → its string values that name kit files; a lone path with an asset
// extension that names no file → kept and reported missing. Anything else is left alone (data-*
// values need not be URLs).
func (cv *codeConv) dataValue(raw string) string {
	if v := cv.kitFile(raw); v != raw {
		return v
	}
	u := strings.TrimSpace(raw)
	if u == "" || cv.o.KitURL == "" && len(cv.o.Remap) == 0 {
		return raw
	}
	if (u[0] == '[' || u[0] == '{') && len(u) <= 1<<16 {
		return codeJSONStringRE.ReplaceAllStringFunc(raw, func(m string) string {
			inner := m[1 : len(m)-1]
			if strings.Contains(inner, "\\") {
				unesc := strings.ReplaceAll(inner, `\/`, "/")
				if v := cv.kitFile(unesc); v != unesc {
					return `"` + strings.ReplaceAll(v, "/", `\/`) + `"`
				}
				return m
			}
			return `"` + cv.kitFile(inner) + `"`
		})
	}
	if cands, ok := codeSrcsetList(u); ok {
		hit := false
		for _, c := range cands {
			if cv.kitFile(c) != c {
				hit = true
				break
			}
		}
		if hit || codeAssetPath(cands[0]) {
			return cv.srcset(raw)
		}
		return raw
	}
	if codeAssetPath(u) {
		cv.url(u, false) // reports it missing
	}
	return raw
}

// codeSrcsetList reads a srcset-like list: two or more candidates, or one with a descriptor, each
// a URL without spaces and at most one w/x descriptor. ok is false for anything else.
func codeSrcsetList(v string) ([]string, bool) {
	if len(v) > 1<<16 || strings.HasPrefix(v, "data:") {
		return nil, false
	}
	parts := strings.Split(v, ",")
	var urls []string
	desc := false
	for _, p := range parts {
		f := strings.Fields(p)
		switch len(f) {
		case 1:
		case 2:
			if !codeSrcsetDescRE.MatchString(f[1]) {
				return nil, false
			}
			desc = true
		default:
			return nil, false
		}
		urls = append(urls, f[0])
	}
	if len(urls) < 2 && !desc {
		return nil, false
	}
	for _, u := range urls {
		lu := strings.ToLower(u)
		web := strings.HasPrefix(lu, "http://") || strings.HasPrefix(lu, "https://") || strings.HasPrefix(u, "//")
		if strings.HasPrefix(u, "#") || !web && codeSchemeRE.MatchString(u) || path.Ext(strings.SplitN(u, "?", 2)[0]) == "" {
			return nil, false
		}
	}
	return urls, true
}

// codeAssetPath reports whether a value reads as a relative or root-relative path of a design
// file: no scheme, no spaces, an asset extension.
func codeAssetPath(u string) bool {
	if u == "" || len(u) > 1024 || strings.ContainsAny(u, " \t\n<>\"'(){},;") || strings.HasPrefix(u, "#") || strings.HasPrefix(u, "//") || codeSchemeRE.MatchString(u) {
		return false
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return codeAssetExts[strings.ToLower(path.Ext(u))]
}

// kitFile maps a value that is the path of a kit file (relative to the page's folder, or
// root-relative) to its kit URL, query and fragment kept; any other value is returned as it is,
// unreported (it may not be a URL at all).
func (cv *codeConv) kitFile(raw string) string {
	u := strings.TrimSpace(raw)
	if l, ok := cv.oldLocal(u); ok && !cv.o.KeepOldLinks {
		if v := cv.kitFile(l); v != l {
			return v
		}
		return raw
	}
	if cv.o.KitURL == "" && len(cv.o.Remap) == 0 || u == "" || len(u) > 1024 || strings.ContainsAny(u, " \t\n<>\"'(){}") || strings.HasPrefix(u, "#") ||
		strings.HasPrefix(u, "//") || codeSchemeRE.MatchString(u) {
		return raw
	}
	rest := ""
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u, rest = u[:i], u[i:]
	}
	if path.Ext(u) == "" {
		return raw
	}
	var rel string
	if strings.HasPrefix(u, "/") {
		rel = strings.TrimPrefix(path.Clean(u), "/")
	} else {
		rel = strings.TrimPrefix(path.Clean("/"+path.Join(cv.o.Base, u)), "/")
	}
	if v, ok := cv.o.Remap[rel]; ok {
		return v + rest
	}
	if !cv.o.KitFiles[rel] {
		return raw
	}
	return cv.o.KitURL + rel + rest
}

// isEventHandler reports whether an attribute is an inline event handler (onclick, onload, …).
func isEventHandler(key string) bool {
	key = strings.ToLower(key)
	if len(key) < 3 || !strings.HasPrefix(key, "on") {
		return false
	}
	for _, r := range key[2:] {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// handlerWarning tells the importer about the design's inline event handlers: the site's nonce
// Content Security Policy (csp_mode enforce) blocks them (only nonced <script> elements run; no
// 'unsafe-inline' or 'unsafe-hashes' is added), and report mode reports them.
func handlerWarning(handlers map[string]int) string {
	if len(handlers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(handlers))
	total := 0
	for k, n := range handlers {
		keys = append(keys, k)
		total += n
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for i, k := range keys {
		if i == 12 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%d× %s", handlers[k], k))
	}
	return fmt.Sprintf("%d inline event handler attribute(s) (%s) do not run under the site's nonce Content Security Policy "+
		"(csp_mode enforce; report mode reports them): move them into the design's scripts (addEventListener)",
		total, strings.Join(parts, ", "))
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
	return cv.urlWith(raw, page, cv.o.KeepOldLinks)
}

// urlWith is url; keepOld leaves a URL of the design's original site as it is.
func (cv *codeConv) urlWith(raw string, page, keepOld bool) string {
	u := strings.TrimSpace(raw)
	if l, ok := cv.oldLocal(u); ok {
		if keepOld {
			return raw
		}
		u, raw = l, l
	}
	if u == "" || strings.HasPrefix(u, "#") || strings.HasPrefix(u, "//") || codeSchemeRE.MatchString(u) {
		return raw
	}
	whole := u
	frag, query := "", ""
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u, frag = u[:i], u[i:]
	}
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u, query = u[:i], u[i:]
	}
	// The query string stays on the mapped URL (a page's ?tab=2, a file's ?v=3).
	withQuery := func(v string) string {
		if query == "" {
			return v + frag
		}
		if strings.Contains(v, "?") {
			return v + "&" + query[1:] + frag
		}
		return v + query + frag
	}
	var rel string
	if strings.HasPrefix(u, "/") {
		rel = strings.TrimPrefix(path.Clean(u), "/")
	} else {
		rel = strings.TrimPrefix(path.Clean("/"+path.Join(cv.o.Base, u)), "/")
	}
	// The link map: by the path resolved inside the kit first (unambiguous), then as written.
	if v, ok := cv.o.LinkMap[rel]; ok && u != "" {
		return withQuery(v)
	}
	if v, ok := cv.o.LinkMap[whole]; ok {
		return v
	}
	if v, ok := cv.o.LinkMap[u]; ok && u != "" {
		return withQuery(v)
	}
	if v, ok := cv.o.Remap[rel]; ok {
		return withQuery(v)
	}
	if cv.o.KitURL != "" && cv.o.KitFiles[rel] {
		return withQuery(cv.o.KitURL + rel)
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
