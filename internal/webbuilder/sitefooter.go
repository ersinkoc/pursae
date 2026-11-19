package webbuilder

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Site-level help for ConvertSite (docs/developer/web-builder-code-sections.md, "Importing a site"):
//
//   - siteFooterHints: a design without a <footer> element (WordPress's Salient theme draws its
//     footer as <div id="footer-outer"> inside the content's wrapper) still ends every page with
//     the same block. Walking down from <body> along the wrappers that hold the site header, then
//     into the content's wrapper, the first level where every page's element children end with a
//     run of the same elements after content that differs is where the footer starts.
//   - Volatile bits: copies of a header or footer that differ only in values a page generates on
//     each request — a page builder's random row id (WPBakery's id="fws_6af66492d500e") that
//     nothing else on the page names, a cache-buster (?ver=19958), a nonce in an inline script or a
//     hidden input (WordPress's 10-hex-digit nonces) — are one part (normVolatile); the template
//     keeps the first page's copy of them.

// volatileIDRE: an id value that reads as generated (letters, then 8 or more hex digits with
// digits among them).
var volatileIDRE = regexp.MustCompile(`\sid="([A-Za-z][A-Za-z_-]{0,40}[_-]?[0-9a-f]{8,40})"`)

// volatileVerRE: a cache-buster query value.
var volatileVerRE = regexp.MustCompile(`([?&](?:ver|v|version)=)[^"&'\s<>#\\]+`)

// volatileHexRE: a nonce-like token (8–64 hex digits, digits and letters both).
var volatileHexRE = regexp.MustCompile(`\b(?:[0-9]+[a-f]|[a-f]+[0-9])[0-9a-f]{6,62}\b`)

var (
	volatileScriptRE = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	volatileHiddenRE = regexp.MustCompile(`(?i)<input\b[^>]*\btype="hidden"[^>]*>`)
)

// volatileIDs are the generated-looking id values of a page that nothing else on it names (the
// value occurs only as an id: a form printed twice repeats its button's id).
func volatileIDs(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range volatileIDRE.FindAllStringSubmatch(src, -1) {
		v := m[1]
		digits := 0
		for _, r := range v {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits >= 3 && strings.Count(src, v) == strings.Count(src, `id="`+v+`"`) {
			out[v] = true
		}
	}
	return out
}

// normVolatile is markup with its volatile bits (see the file comment) replaced by "#", for
// comparing copies of a part; ids lists the page's generated ids.
func normVolatile(s string, ids map[string]bool) string {
	if len(ids) > 0 {
		s = volatileIDRE.ReplaceAllStringFunc(s, func(m string) string {
			if v := volatileIDRE.FindStringSubmatch(m)[1]; ids[v] {
				return ` id="#"`
			}
			return m
		})
	}
	s = volatileVerRE.ReplaceAllString(s, "${1}#")
	hex := func(m string) string { return volatileHexRE.ReplaceAllString(m, "#") }
	s = volatileScriptRE.ReplaceAllStringFunc(s, hex)
	return volatileHiddenRE.ReplaceAllStringFunc(s, hex)
}

// footerKey is how a block compares across pages: its skeleton (tags, text, attributes but class
// and state attributes, as the active-link learner aligns copies) with volatile bits normalized.
func footerKey(n *html.Node, ids map[string]bool, o CodeImportOptions) string {
	// As converted: the pages' relative URLs differ with their depth (../../wp-content/…).
	c := cloneNode(n)
	cv := newCodeConv(o)
	cv.rewrite(c)
	cv.rewriteAll(c)
	var b strings.Builder
	_ = html.Render(&b, c)
	nodes, err := parseFragment(normVolatile(StripCodeIDs(b.String()), ids))
	if err != nil {
		return b.String()
	}
	_, sk := activeElements(nodes)
	return sk
}

func footerKids(n *html.Node) []*html.Node {
	var out []*html.Node
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == html.ElementNode && !isScripty(k) {
			out = append(out, k)
		}
	}
	return out
}

// siteFooterHints finds the footer of pages that have none (see the file comment): for each such
// page its path from <body> (codeNodeAt), keyed by the page's index. Fewer than two such pages,
// or no common run, give none.
func siteFooterHints(pages []SitePage, ids []map[string]bool, opts func(i int) CodeImportOptions) map[int][]int {
	type pg struct {
		i      int
		body   *html.Node
		header *html.Node
	}
	var list []pg
	for i, p := range pages {
		doc, err := html.Parse(strings.NewReader(p.HTML))
		if err != nil {
			continue
		}
		body := findBody(doc)
		if body == nil {
			continue
		}
		sh := findShell(body, nil)
		if sh.footer != nil || sh.main != nil {
			continue // a footer of its own, or a main (a footer after it would be a <footer>)
		}
		list = append(list, pg{i, body, sh.header})
	}
	if len(list) < 2 {
		return nil
	}
	cur := make([]*html.Node, len(list))
	for j, p := range list {
		cur[j] = p.body
	}
	for depth := 0; depth < 16; depth++ {
		kids := make([][]*html.Node, len(list))
		holdsHeader := true
		for j, p := range list {
			kids[j] = footerKids(cur[j])
			holdsHeader = holdsHeader && p.header != nil && codeContains(cur[j], p.header) && cur[j] != p.header
		}
		// Where the content starts at this level: after the child holding the header.
		start := make([]int, len(list))
		if holdsHeader {
			for j, p := range list {
				start[j] = -1
				for k, n := range kids[j] {
					if codeContains(n, p.header) {
						start[j] = k + 1
					}
				}
			}
		}
		// The run of equal elements every page ends with (after start).
		run := 0
		for {
			ok := true
			var key string
			for j := range list {
				k := len(kids[j]) - 1 - run
				if k < start[j] {
					ok = false
					break
				}
				fk := footerKey(kids[j][k], ids[list[j].i], opts(list[j].i))
				if j == 0 {
					key = fk
				} else if fk != key {
					ok = false
				}
				if !ok {
					break
				}
			}
			if !ok {
				break
			}
			run++
		}
		varying := true
		for j := range list {
			varying = varying && len(kids[j])-run > start[j]
		}
		if run > 0 && varying {
			out := map[int][]int{}
			for j, p := range list {
				if path := codePathOf(p.body, kids[j][len(kids[j])-run]); path != nil {
					out[p.i] = path
				}
			}
			return out
		}
		// Go down into the element that holds the content: after the child holding the header,
		// the largest of the elements before the run (the content's wrapper, not a drawer);
		// the child holding the header itself when nothing comes after it (the header and the
		// content share a wrapper).
		next := make([]*html.Node, len(list))
		for j := range list {
			lo, hi := start[j], len(kids[j])-run
			if holdsHeader && lo >= hi {
				lo = start[j] - 1
			}
			if lo < 0 || lo >= hi {
				return nil
			}
			best, size := -1, -1
			for k := lo; k < hi; k++ {
				if n := codeSize(kids[j][k]); n > size {
					best, size = k, n
				}
			}
			next[j] = kids[j][best]
		}
		for j := range next {
			if next[j].Data != next[0].Data || attr(next[j], "id") != attr(next[0], "id") {
				return nil
			}
		}
		cur = next
	}
	return nil
}

// codeSize is the size of n's markup (text and attributes), roughly.
func codeSize(n *html.Node) int {
	t := 0
	var walk func(x *html.Node)
	walk = func(x *html.Node) {
		t += len(x.Data)
		for _, a := range x.Attr {
			t += len(a.Val)
		}
		for k := x.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	walk(n)
	return t
}

func findBody(doc *html.Node) *html.Node {
	var walk func(n *html.Node) *html.Node
	walk = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && n.Data == "body" {
			return n
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			if b := walk(k); b != nil {
				return b
			}
		}
		return nil
	}
	return walk(doc)
}
