package webbuilder

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Code sections (KindCode, docs/developer/web-builder-code-sections.md).
//
// The renderer draws a code section as an empty placeholder element carrying data-wb-code="{id}";
// the snapshot sanitizer keeps it (a data-wb-* attribute). After the snapshot is sanitized, FillCode
// puts the section's own markup (content.html, written by an unfiltered_html author) in place of the
// placeholder, inside a display:contents wrapper so the design's layout sees no extra box. The
// sections' head markup (content.head) travels in the stored snapshot as the root's first child,
// <template data-wb-code-head>, which the front end lifts into <head> (CodeHead).
//
// A page whose top-level sections are all code sections is a "code page": its root gets
// data-wb-code-page and the front end prints neither the builder's site stylesheet (a global reset
// and a size container that would change the design) nor the canvas wrapper's main landmark when
// the design brings its own.

const (
	// AttrCode marks a code section's placeholder / wrapper.
	AttrCode = "data-wb-code"
	// AttrCodePage marks the snapshot root of a page made only of code sections.
	AttrCodePage = "data-wb-code-page"
	// AttrCodeID is the stable id the importer gives each editable element of a code section; the
	// editor maps an edit back to the source with it. It is removed from published markup.
	AttrCodeID = "data-wbc"

	codeHeadOpen  = `<template data-wb-code-head>`
	codeHeadClose = `</template>`
)

var (
	codePlaceholderRE = regexp.MustCompile(`<(section|div)\b[^>]*\sdata-wb-code="([A-Za-z0-9_-]{1,64})"[^>]*>\s*</(?:section|div)>`)
	codeIDAttrRE      = regexp.MustCompile(`\sdata-wbc="[^"]*"`)
	rootOpenRE        = regexp.MustCompile(`^\s*<div\b[^>]*>`)
	rootPostRE        = regexp.MustCompile(`\sdata-wb-post="(\d{1,20})"`)
)

// CodeContent is what FillCode needs of a code section.
type CodeContent struct {
	ID   string
	HTML string
	Head string
	// Wrap is the section's wrapper chain: opening tags, outermost first, one per line.
	Wrap string
	// Lang, HTMLAttrs and BodyAttrs: the design's <html> lang and attributes and its <body>
	// attributes (content.lang, content.htmlAttrs, content.bodyAttrs), printed by the front end.
	Lang, HTMLAttrs, BodyAttrs string
	// Active is the rule that marks the current page's link (content.active, ActiveRule JSON).
	Active string
	// Binds are the section's content bindings (content.binds, codebind.go).
	Binds  string
	Hidden bool
	// Kit is the design kit's slug (content.kit); Local the section's local styles and Breakpoints
	// the kit's devices (zero: DefaultBreakpoints), which the caller fills in (FillCode prints the
	// local styles of the sections it fills, see local.go).
	Kit         string
	Local       LocalStyles
	Breakpoints Breakpoints
}

// CodeSections lists the top-level code sections of a document (DocState JSON) and reports
// whether every top-level section is one.
func CodeSections(doc json.RawMessage) (list []CodeContent, only bool) {
	var d struct {
		Sections []struct {
			ID      string            `json:"id"`
			Kind    string            `json:"kind"`
			Hidden  bool              `json:"hidden"`
			Content map[string]string `json:"content"`
			Local   json.RawMessage   `json:"local"`
		} `json:"sections"`
	}
	if len(doc) == 0 || json.Unmarshal(doc, &d) != nil {
		return nil, false
	}
	only = len(d.Sections) > 0
	for _, s := range d.Sections {
		if s.Kind != KindCode {
			only = false
			continue
		}
		local, _ := ParseLocal(s.Local)
		list = append(list, CodeContent{ID: s.ID, HTML: s.Content["html"], Head: s.Content["head"], Wrap: s.Content["wrap"],
			Lang: s.Content["lang"], HTMLAttrs: s.Content["htmlAttrs"], BodyAttrs: s.Content["bodyAttrs"], Active: s.Content["active"], Binds: s.Content[ContentBinds], Hidden: s.Hidden,
			Kit: s.Content["kit"], Local: local})
	}
	return list, only && len(list) > 0
}

// HasCode reports whether a document has a code section (a cheap check before CodeSections).
func HasCode(doc json.RawMessage) bool {
	return len(doc) > 0 && strings.Contains(string(doc), `"`+KindCode+`"`)
}

// FillCode puts the code sections' markup into a sanitized snapshot (see the file comment). Each
// placeholder becomes the section's own markup, with no element of its own; consecutive sections
// with the same wrapper chain share one copy of each wrapper. The local styles of the sections it
// fills give their elements a class and the head part a stylesheet (local.go). A snapshot without
// placeholders comes back unchanged.
func FillCode(snapshot string, sections []CodeContent, only bool) string {
	return FillCodeWith(snapshot, sections, only, CodeFillOptions{})
}

// FillCodeWith is FillCode with the options the sections' content bindings need (CompileBinds).
func FillCodeWith(snapshot string, sections []CodeContent, only bool, o CodeFillOptions) string {
	if len(sections) == 0 || !strings.Contains(snapshot, AttrCode+`="`) {
		return snapshot
	}
	post := ""
	if loc := rootOpenRE.FindStringIndex(snapshot); loc != nil {
		if m := rootPostRE.FindStringSubmatch(snapshot[:loc[1]]); m != nil {
			post = m[1]
		}
	}
	var localCSS strings.Builder
	byID := make(map[string]CodeContent, len(sections))
	for _, s := range sections {
		byID[s.ID] = s
	}
	var b strings.Builder
	b.Grow(len(snapshot) + 64<<10)
	var stack []string // open wrappers
	closeTo := func(k int) {
		for len(stack) > k {
			b.WriteString(closeTag(stack[len(stack)-1]))
			stack = stack[:len(stack)-1]
		}
	}
	last := 0
	for _, m := range codePlaceholderRE.FindAllStringSubmatchIndex(snapshot, -1) {
		between := snapshot[last:m[0]]
		if strings.TrimSpace(between) != "" {
			closeTo(0)
		}
		b.WriteString(between)
		last = m[1]
		s, ok := byID[snapshot[m[4]:m[5]]]
		if !ok {
			continue
		}
		chain := wrapChain(s.Wrap)
		k := 0
		for k < len(stack) && k < len(chain) && stack[k] == chain[k] {
			k++
		}
		closeTo(k)
		for _, w := range chain[k:] {
			b.WriteString(w)
			stack = append(stack, w)
		}
		src := s.HTML
		if s.Binds != "" {
			src = CompileBinds(src, s.ID, ParseCodeBinds(s.Binds), o)
		}
		if ids := styledIDs(s.Local); len(ids) > 0 {
			classes := make(map[string]string, len(ids))
			for id := range ids {
				classes[id] = LocalClass(post, id)
			}
			src = AddLocalClasses(src, classes)
			localCSS.WriteString(LocalCSS(s.Local, s.Breakpoints, LocalSelector(post)))
		}
		b.WriteString(StripCodeIDs(src))
	}
	closeTo(0)
	b.WriteString(snapshot[last:])
	out := b.String()
	head := CodeHeads(sections)
	if localCSS.Len() > 0 {
		head += `<style data-wb-local="">` + "\n" + localCSS.String() + `</style>` + "\n"
	}
	if loc := rootOpenRE.FindStringIndex(out); loc != nil {
		open := out[:loc[1]]
		if only {
			// No builder look on a page of code sections: the root keeps its identity, not its classes
			// and page variables, and draws no box of its own (the front end drops it: UnwrapCodePage).
			post := ""
			if m := rootPostRE.FindStringSubmatch(open); m != nil {
				post = ` data-wb-post="` + m[1] + `"`
			}
			open = `<div class="wb-page" data-wb-root=""` + post + ` ` + AttrCodePage + `="" style="display:contents">`
		}
		if head != "" {
			open += codeHeadOpen + head + codeHeadClose
		}
		out = open + out[loc[1]:]
	}
	return out
}

var (
	wrapTagRE  = regexp.MustCompile(`^<([a-zA-Z][a-zA-Z0-9-]{0,30})(?:\s[^<>]*)?>$`)
	closeTagRE = regexp.MustCompile(`^<([a-zA-Z][a-zA-Z0-9-]{0,30})`)
)

// wrapChain is a section's wrappers: well-formed opening tags only (one per line).
func wrapChain(wrap string) []string {
	var out []string
	for _, line := range strings.Split(wrap, "\n") {
		if line = strings.TrimSpace(line); wrapTagRE.MatchString(line) {
			out = append(out, line)
		}
	}
	return out
}

func closeTag(open string) string {
	if m := closeTagRE.FindStringSubmatch(open); m != nil {
		return "</" + m[1] + ">"
	}
	return ""
}

// UnwrapCodePage drops the root element of a page of code sections, so the design's elements are
// the page's own (selectors such as body > header keep matching).
func UnwrapCodePage(snapshot string) string {
	loc := rootOpenRE.FindStringIndex(snapshot)
	if loc == nil || !strings.Contains(snapshot[:loc[1]], AttrCodePage) {
		return snapshot
	}
	inner := snapshot[loc[1]:]
	if i := strings.LastIndex(inner, "</div>"); i >= 0 {
		inner = inner[:i]
	}
	return inner
}

// CodeHeads joins the sections' head markup (see MergeHeads).
func CodeHeads(sections []CodeContent) string {
	heads := make([]string, 0, len(sections))
	for _, s := range sections {
		if !strings.Contains(strings.ToLower(s.Head), "</template") {
			heads = append(heads, s.Head)
		}
	}
	return MergeHeads(heads...)
}

// MergeHeads joins head markup tag by tag, each distinct top-level element once, in order, so
// sections and template parts of one design share their stylesheets and scripts.
func MergeHeads(heads ...string) string {
	key := strings.Join(heads, "\x00")
	if strings.TrimSpace(key) == "" {
		return ""
	}
	if v, ok := mergeHeadsCache.Load(key); ok {
		return v.(string)
	}
	seen := map[string]bool{}
	var b strings.Builder
	ctx := &html.Node{Type: html.ElementNode, Data: "head", DataAtom: atom.Head}
	for _, h := range heads {
		if strings.TrimSpace(h) == "" {
			continue
		}
		nodes, err := html.ParseFragment(strings.NewReader(h), ctx)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			if n.Type != html.ElementNode {
				continue
			}
			var one strings.Builder
			if html.Render(&one, n) != nil {
				continue
			}
			tag := one.String()
			if seen[tag] {
				continue
			}
			seen[tag] = true
			b.WriteString(tag)
			b.WriteByte('\n')
		}
	}
	out := b.String()
	if mergeHeadsN.Add(1) > 256 { // a small cache: the same few heads come back on every request
		mergeHeadsCache.Clear()
		mergeHeadsN.Store(0)
	}
	mergeHeadsCache.Store(key, out)
	return out
}

var (
	mergeHeadsCache sync.Map
	mergeHeadsN     atomic.Int64
)

// CodeHead lifts the head markup out of a filled snapshot: the head part and the body without it.
func CodeHead(body string) (head, rest string) {
	i := strings.Index(body, codeHeadOpen)
	if i < 0 {
		return "", body
	}
	j := strings.Index(body[i:], codeHeadClose)
	if j < 0 {
		return "", body
	}
	return body[i+len(codeHeadOpen) : i+j], body[:i] + body[i+j+len(codeHeadClose):]
}

// IsCodePage reports whether a filled snapshot is a page of code sections only.
func IsCodePage(snapshot string) bool {
	loc := rootOpenRE.FindStringIndex(snapshot)
	return loc != nil && strings.Contains(snapshot[:loc[1]], AttrCodePage)
}

// StripCodeIDs removes the editor's data-wbc ids.
func StripCodeIDs(src string) string {
	if !strings.Contains(src, AttrCodeID+`="`) {
		return src
	}
	return codeIDAttrRE.ReplaceAllString(src, "")
}

// CodeFallback is a code section's markup for posts.content (feeds, search, excerpts): the markup
// without scripts, styles, templates and the editor ids.
func CodeFallback(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(src), ctx)
	if err != nil {
		return ""
	}
	var drop func(n *html.Node)
	drop = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.CommentNode || (c.Type == html.ElementNode && (c.DataAtom == atom.Script || c.DataAtom == atom.Style ||
				c.DataAtom == atom.Template || c.DataAtom == atom.Link || c.DataAtom == atom.Noscript)) {
				n.RemoveChild(c)
			} else {
				drop(c)
			}
			c = next
		}
	}
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == html.CommentNode || (n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style ||
			n.DataAtom == atom.Template || n.DataAtom == atom.Link || n.DataAtom == atom.Noscript)) {
			continue
		}
		drop(n)
		_ = html.Render(&b, n)
	}
	return StripCodeIDs(b.String()) + "\n"
}

// codeUntagged are elements the editor never selects: they draw nothing of their own.
var codeUntagged = map[atom.Atom]bool{atom.Script: true, atom.Style: true, atom.Template: true, atom.Noscript: true,
	atom.Link: true, atom.Meta: true, atom.Base: true, atom.Br: true, atom.Wbr: true, atom.Source: true,
	atom.Track: true, atom.Param: true, atom.Title: true}

// codeTaggable: an element the editor addresses as a block.
func codeTaggable(n *html.Node) bool {
	return n.Type == html.ElementNode && !codeUntagged[n.DataAtom] && attr(n, AttrCodeID) == ""
}

// TagCodeDoc gives every element of the document's code sections that has none a data-wbc id (a
// number above the highest one in use), so the editor can treat each as a block; sections imported
// before every element was tagged get theirs when the editor opens them. The ids never reach the
// published page (StripCodeIDs). The document comes back unchanged when nothing was missing or it
// does not parse.
func TagCodeDoc(doc json.RawMessage) json.RawMessage {
	if len(doc) == 0 || !strings.Contains(string(doc), `"code"`) {
		return doc
	}
	var d map[string]json.RawMessage
	if json.Unmarshal(doc, &d) != nil {
		return doc
	}
	var sections []map[string]json.RawMessage
	if json.Unmarshal(d["sections"], &sections) != nil {
		return doc
	}
	type part struct {
		content map[string]json.RawMessage
		nodes   []*html.Node
	}
	var parts []part
	maxID := 0
	var each func(n *html.Node, inSVG bool, fn func(*html.Node, bool))
	each = func(n *html.Node, inSVG bool, fn func(*html.Node, bool)) {
		if n.Type != html.ElementNode {
			return
		}
		fn(n, inSVG)
		if codeUntagged[n.DataAtom] {
			return
		}
		svg := inSVG || n.DataAtom == atom.Svg
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			each(k, svg, fn)
		}
	}
	missing := false
	for i, s := range sections {
		var kind string
		if json.Unmarshal(s["kind"], &kind) != nil || kind != "code" {
			continue
		}
		var content map[string]json.RawMessage
		if json.Unmarshal(s["content"], &content) != nil {
			continue
		}
		var src string
		if json.Unmarshal(content["html"], &src) != nil || strings.TrimSpace(src) == "" {
			continue
		}
		ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
		nodes, err := html.ParseFragment(strings.NewReader(src), ctx)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			each(n, false, func(e *html.Node, inSVG bool) {
				if v, err := strconv.Atoi(attr(e, AttrCodeID)); err == nil && v > maxID {
					maxID = v
				}
				if !inSVG && codeTaggable(e) {
					missing = true
				}
			})
		}
		parts = append(parts, part{content, nodes})
		sections[i]["content"] = nil // marked: rewritten below
	}
	if !missing {
		return doc
	}
	pi := 0
	for i := range sections {
		if sections[i]["content"] != nil || pi >= len(parts) {
			continue
		}
		p := parts[pi]
		pi++
		var b strings.Builder
		for _, n := range p.nodes {
			each(n, false, func(e *html.Node, inSVG bool) {
				if !inSVG && codeTaggable(e) {
					maxID++
					e.Attr = append(e.Attr, html.Attribute{Key: AttrCodeID, Val: strconv.Itoa(maxID)})
				}
			})
			_ = html.Render(&b, n)
		}
		raw, _ := json.Marshal(b.String())
		p.content["html"] = raw
		c, _ := json.Marshal(p.content)
		sections[i]["content"] = c
	}
	raw, err := json.Marshal(sections)
	if err != nil {
		return doc
	}
	d["sections"] = raw
	out, err := json.Marshal(d)
	if err != nil {
		return doc
	}
	return out
}
