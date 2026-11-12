package server

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"wpgo/internal/webbuilder"
)

// Translation units of an HTML fragment (Pursae Multi Language: Web Builder snapshots and the rich
// text values of a builder document).
//
// A unit is the smallest piece a translator sees as one sentence:
//
//   - the inner HTML of an element whose children are text and inline formatting only
//     (<h2>Fast <b>and</b> simple</h2> is one unit, "Fast <b>and</b> simple");
//   - a text node standing next to block children (plain text);
//   - the alt, title, placeholder and aria-label attributes, and the value of a submit button.
//
// Scripts, styles, SVG, templates, elements filled per request (data-wb-slot) and anything marked
// translate="no" or class="notranslate" are left alone.
//
// Applying a translation never trusts its markup: only the inline tags below survive, and every
// tag gets the attributes of the matching tag of the original (the n-th <a> keeps the n-th
// original href); a tag the original did not have is unwrapped to its text.

// pllInlineTags are the tags a unit may hold.
var pllInlineTags = map[string]bool{"b": true, "strong": true, "i": true, "em": true, "u": true, "s": true, "code": true,
	"span": true, "a": true, "br": true, "small": true, "sub": true, "sup": true, "mark": true, "ins": true, "del": true}

// pllUnitAttrs are the translated attributes.
var pllUnitAttrs = []string{"alt", "title", "placeholder", "aria-label"}

type pllHTMLUnit struct {
	Text string // the source: inner HTML (HTML) or plain text
	HTML bool
	Key  string // normalized visible text, the dictionary key (pllUnitKey)

	node *html.Node // the host element (HTML), the text node (plain) or the element of an attribute
	attr string     // attribute name, "" for content
}

type pllHTMLDoc struct {
	root  *html.Node // a synthetic <body> holding the fragment
	Units []pllHTMLUnit
}

// pllUnitKey normalizes visible text: entities decoded by the caller, whitespace collapsed.
func pllUnitKey(text string) string { return strings.Join(strings.Fields(text), " ") }

// pllNodeText is the visible text of a node; <br> counts as a line break.
func pllNodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			if n.DataAtom == atom.Br {
				b.WriteByte('\n')
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

func pllInnerHTML(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	return b.String()
}

func pllAttr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// pllUnitSkipped reports whether an element and everything below it stay untranslated.
func pllUnitSkipped(n *html.Node) bool {
	if n.Namespace != "" {
		return true // svg, math
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Template, atom.Noscript, atom.Textarea, atom.Code, atom.Pre, atom.Iframe:
		return true
	}
	if _, ok := pllAttr(n, webbuilder.AttrSlot); ok {
		return true // filled per request
	}
	if v, _ := pllAttr(n, "translate"); strings.EqualFold(v, "no") {
		return true
	}
	if v, _ := pllAttr(n, "class"); v != "" {
		for _, c := range strings.Fields(v) {
			if c == "notranslate" {
				return true
			}
		}
	}
	return false
}

// pllInlineOnly reports whether every element below n is plain inline formatting.
func pllInlineOnly(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.Namespace != "" || !pllInlineTags[c.Data] || pllUnitSkipped(c) || !pllInlineOnly(c) {
			return false
		}
	}
	return true
}

// pllHTMLParse parses an HTML fragment and lists its translation units in document order.
func pllHTMLParse(src string) (*pllHTMLDoc, error) {
	root := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(src), root)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	d := &pllHTMLDoc{root: root}
	d.walk(root, true)
	return d, nil
}

func (d *pllHTMLDoc) walk(n *html.Node, isRoot bool) {
	if !isRoot {
		if pllUnitSkipped(n) {
			return
		}
		for _, k := range pllUnitAttrs {
			if v, ok := pllAttr(n, k); ok && hasLetter(v) {
				d.Units = append(d.Units, pllHTMLUnit{Text: v, Key: pllUnitKey(v), node: n, attr: k})
			}
		}
		if n.DataAtom == atom.Input {
			if t, _ := pllAttr(n, "type"); t == "submit" || t == "button" {
				if v, ok := pllAttr(n, "value"); ok && hasLetter(v) {
					d.Units = append(d.Units, pllHTMLUnit{Text: v, Key: pllUnitKey(v), node: n, attr: "value"})
				}
			}
		}
	}
	direct := false
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && hasLetter(c.Data) {
			direct = true
		}
	}
	if direct && pllInlineOnly(n) {
		hasEl := false
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			hasEl = hasEl || c.Type == html.ElementNode
		}
		if !hasEl && n.FirstChild == n.LastChild {
			// One text node: a plain unit (no markup for the translator to break).
			d.Units = append(d.Units, pllHTMLUnit{Text: n.FirstChild.Data, Key: pllUnitKey(n.FirstChild.Data), node: n.FirstChild})
		} else {
			d.Units = append(d.Units, pllHTMLUnit{Text: pllInnerHTML(n), HTML: true, Key: pllUnitKey(pllNodeText(n)), node: n})
		}
		// Attributes of the inline children (a title on a link) are still listed.
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				d.attrsBelow(c)
			}
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if hasLetter(c.Data) {
				d.Units = append(d.Units, pllHTMLUnit{Text: c.Data, Key: pllUnitKey(c.Data), node: c})
			}
		case html.ElementNode:
			d.walk(c, false)
		}
	}
}

func (d *pllHTMLDoc) attrsBelow(n *html.Node) {
	for _, k := range pllUnitAttrs {
		if v, ok := pllAttr(n, k); ok && hasLetter(v) {
			d.Units = append(d.Units, pllHTMLUnit{Text: v, Key: pllUnitKey(v), node: n, attr: k})
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			d.attrsBelow(c)
		}
	}
}

// Set replaces unit i with its translation. An HTML translation is rebuilt from safe inline tags
// carrying the original attributes; a blank translation keeps the source.
func (d *pllHTMLDoc) Set(i int, tr string) {
	if i < 0 || i >= len(d.Units) || strings.TrimSpace(tr) == "" {
		return
	}
	u := &d.Units[i]
	switch {
	case u.attr != "":
		for k := range u.node.Attr {
			if u.node.Attr[k].Namespace == "" && u.node.Attr[k].Key == u.attr {
				u.node.Attr[k].Val = pllPlainTr(u.Text, tr)
			}
		}
	case !u.HTML:
		u.node.Data = pllPlainTr(u.Text, tr)
	default:
		// The original inline elements, by tag, in order: the source of every attribute.
		orig := map[string][]*html.Node{}
		var collect func(*html.Node)
		collect = func(n *html.Node) {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode {
					orig[c.Data] = append(orig[c.Data], c)
					collect(c)
				}
			}
		}
		collect(u.node)
		ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
		nodes, err := html.ParseFragment(strings.NewReader(tr), ctx)
		if err != nil {
			return
		}
		used := map[string]int{}
		box := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
		var rebuild func(src *html.Node, dst *html.Node)
		rebuild = func(src, dst *html.Node) {
			switch src.Type {
			case html.TextNode:
				dst.AppendChild(&html.Node{Type: html.TextNode, Data: src.Data})
			case html.ElementNode:
				tag := src.Data
				if src.Namespace == "" && pllInlineTags[tag] && used[tag] < len(orig[tag]) {
					o := orig[tag][used[tag]]
					used[tag]++
					el := &html.Node{Type: html.ElementNode, Data: o.Data, DataAtom: o.DataAtom, Attr: append([]html.Attribute(nil), o.Attr...)}
					dst.AppendChild(el)
					dst = el
				} else if src.Namespace != "" || src.DataAtom == atom.Script || src.DataAtom == atom.Style {
					return // never the text of a script, a style or foreign content
				}
				for c := src.FirstChild; c != nil; c = c.NextSibling {
					rebuild(c, dst)
				}
			}
		}
		for _, n := range nodes {
			rebuild(n, box)
		}
		if !hasLetter(pllNodeText(box)) {
			return
		}
		for u.node.FirstChild != nil {
			u.node.RemoveChild(u.node.FirstChild)
		}
		for box.FirstChild != nil {
			c := box.FirstChild
			box.RemoveChild(c)
			u.node.AppendChild(c)
		}
	}
}

// Render serializes the fragment.
func (d *pllHTMLDoc) Render() string { return pllInnerHTML(d.root) }

// pllPlainOf is the plain text of a translation that may carry markup (a provider answering HTML
// for a plain text): tags removed, entities decoded.
func pllPlainOf(tr string) string {
	if !strings.ContainsAny(tr, "<&") {
		return tr
	}
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(tr), ctx)
	if err != nil {
		return tr
	}
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			continue
		}
		b.WriteString(pllNodeText(n))
	}
	return b.String()
}

// pllPlainTr is the translation of a plain text: markup a provider added to a source that had
// none is removed; the text is escaped when the fragment is rendered.
func pllPlainTr(src, tr string) string {
	if strings.Contains(src, "<") {
		return tr
	}
	return pllPlainOf(tr)
}
