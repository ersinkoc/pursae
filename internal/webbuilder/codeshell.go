package webbuilder

import (
	"encoding/json"
	"strings"

	"golang.org/x/net/html"
)

// The front end of code parts: what a part's code sections say about the document (CodeInfo), the
// shell the header template, the page and the footer template share (ShareShell), and the design's
// <html> and <body> attributes (MergeDocAttrs). See docs/developer/web-builder-code-sections.md,
// "Shell" and "Active links".

// CodeDocInfo is what the front end needs of a part made of code sections.
type CodeDocInfo struct {
	Lang, HTMLAttrs, BodyAttrs string
	// Active: the rule of the part's sections (the header's), nil when none.
	Active *ActiveRule
	// First and Last are the wrapper chains of the first and last shown code sections; Common is
	// what all of them share.
	First, Last, Common []string
}

// CodeInfo reads a document's code sections for the front end (nil without code sections).
func CodeInfo(doc json.RawMessage) *CodeDocInfo {
	if !HasCode(doc) {
		return nil
	}
	list, _ := CodeSections(doc)
	var info *CodeDocInfo
	for _, s := range list {
		if s.Hidden {
			continue
		}
		chain := wrapChain(s.Wrap)
		if info == nil {
			info = &CodeDocInfo{First: chain, Common: chain}
		}
		info.Last = chain
		k := 0
		for k < len(info.Common) && k < len(chain) && info.Common[k] == chain[k] {
			k++
		}
		info.Common = info.Common[:k]
		if info.Lang == "" {
			info.Lang = s.Lang
		}
		if info.HTMLAttrs == "" {
			info.HTMLAttrs = s.HTMLAttrs
		}
		if info.BodyAttrs == "" {
			info.BodyAttrs = s.BodyAttrs
		}
		if info.Active == nil {
			info.Active = ParseActiveRule(s.Active)
		}
	}
	return info
}

// ShellLen is how many wrappers a header, a page and a footer share: the header's last chain, the
// footer's first and (main != nil) all of the page's sections. 0 = none.
func ShellLen(header, main, footer []string, mainCode bool) int {
	k := 0
	for k < len(header) && k < len(footer) && header[k] == footer[k] {
		k++
	}
	if mainCode {
		j := 0
		for j < k && j < len(main) && main[j] == header[j] {
			j++
		}
		k = j
	}
	return k
}

// shellEl is an element of a part's published markup with its offsets.
type shellEl struct {
	open             string // the raw opening tag
	name             string
	start, openEnd   int
	closeStart, end  int
	parent           *shellEl
	kids             []*shellEl
	closed, selfOpen bool
}

// shellTree reads the element tree of markup with offsets (nil when it is not well formed: an
// element left open, an end tag that closes nothing).
func shellTree(src string) []*shellEl {
	z := html.NewTokenizer(strings.NewReader(src))
	var top []*shellEl
	var cur *shellEl
	pos := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := z.Raw()
		start, end := pos, pos+len(raw)
		pos = end
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			e := &shellEl{open: string(raw), name: string(name), start: start, openEnd: end, parent: cur}
			if cur == nil {
				top = append(top, e)
			} else {
				cur.kids = append(cur.kids, e)
			}
			if tt == html.SelfClosingTagToken || voidElements[string(name)] {
				e.closeStart, e.end, e.closed, e.selfOpen = end, end, true, true
				continue
			}
			cur = e
		case html.EndTagToken:
			if cur == nil {
				return nil
			}
			name, _ := z.TagName()
			if cur.name != string(name) {
				return nil
			}
			cur.closeStart, cur.end, cur.closed = start, end, true
			cur = cur.parent
		}
	}
	if cur != nil {
		return nil
	}
	return top
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// onlyEl is the single element of els whose surroundings in src (from lo to hi) are blank.
func onlyEl(src string, els []*shellEl, lo, hi int) *shellEl {
	if len(els) != 1 {
		return nil
	}
	e := els[0]
	if !blank(src[lo:e.start]) || !blank(src[e.end:hi]) {
		return nil
	}
	return e
}

// ShareShell prints the wrappers that a header, a page and a footer share once around all three:
// the header keeps the opening tags of its last k wrappers and loses their closing tags, the
// footer loses the opening tags of its first k wrappers and keeps their closing tags, and the page
// (main != "" — a page of code sections in the same wrappers) loses both. chain is the shared
// wrappers as published. ok is false (and nothing changed) when the markup does not have them
// where expected.
func ShareShell(header, main, footer string, chain []string, mainCode bool) (h, m, f string, ok bool) {
	k := len(chain)
	if k == 0 {
		return header, main, footer, false
	}
	// The header: its last element (blank after it) is chain[0], whose last element is chain[1] …
	ht := shellTree(header)
	if len(ht) == 0 {
		return header, main, footer, false
	}
	els, hi := ht, len(header)
	cut := -1
	for i := 0; i < k; i++ {
		e := els[len(els)-1]
		if e.open != chain[i] || !e.closed || e.selfOpen || !blank(header[e.end:hi]) {
			return header, main, footer, false
		}
		cut, els, hi = e.closeStart, e.kids, e.closeStart
		if i < k-1 && len(els) == 0 {
			return header, main, footer, false
		}
	}
	h = strings.TrimRight(header[:cut], " \t\r\n")
	// The footer: it starts (blank before) with chain[0], whose first element is chain[1] …
	ft := shellTree(footer)
	if len(ft) == 0 {
		return header, main, footer, false
	}
	els, lo := ft, 0
	fcut := -1
	for i := 0; i < k; i++ {
		e := els[0]
		if e.open != chain[i] || e.selfOpen || !blank(footer[lo:e.start]) {
			return header, main, footer, false
		}
		fcut, els, lo = e.openEnd, e.kids, e.openEnd
		if i < k-1 && len(els) == 0 {
			return header, main, footer, false
		}
	}
	f = strings.TrimLeft(footer[fcut:], " \t\r\n")
	m = main
	if mainCode {
		mt := shellTree(main)
		els, lo, hi := mt, 0, len(main)
		var e *shellEl
		for i := 0; i < k; i++ {
			e = onlyEl(main, els, lo, hi)
			if e == nil || e.open != chain[i] || e.selfOpen {
				return header, main, footer, false
			}
			els, lo, hi = e.kids, e.openEnd, e.closeStart
		}
		m = main[e.openEnd:e.closeStart]
	}
	return h, m, f, true
}

// MergeDocAttrs joins attribute strings (`class="a" data-x="1"`): the first value of each name
// wins, classes are joined, inline event handlers are dropped. Each is re-escaped, so the result is
// safe in an opening tag; skip names attributes to leave out (e.g. "lang").
func MergeDocAttrs(skip []string, list ...string) (attrs string, class string) {
	seen := map[string]bool{}
	for _, s := range skip {
		seen[strings.ToLower(s)] = true
	}
	var classes []string
	have := map[string]bool{}
	var out []html.Attribute
	for _, s := range list {
		if strings.TrimSpace(s) == "" {
			continue
		}
		z := html.NewTokenizer(strings.NewReader("<x " + s + ">"))
		if z.Next() != html.StartTagToken {
			continue
		}
		for _, a := range z.Token().Attr {
			key := strings.ToLower(a.Key)
			switch {
			case key == "class":
				for _, c := range strings.Fields(a.Val) {
					if !have[c] {
						have[c] = true
						classes = append(classes, c)
					}
				}
			case seen[key] || isEventHandler(key) || !attrNameOK(key):
			default:
				seen[key] = true
				out = append(out, html.Attribute{Key: key, Val: a.Val})
			}
		}
	}
	var b strings.Builder
	for _, a := range out {
		b.WriteString(" " + a.Key + `="` + html.EscapeString(a.Val) + `"`)
	}
	return strings.TrimSpace(b.String()), strings.Join(classes, " ")
}

func attrNameOK(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == ':' || r == '.') {
			return false
		}
	}
	return true
}
