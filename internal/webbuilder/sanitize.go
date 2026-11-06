package webbuilder

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Snapshot HTML sanitizer (contract §8.2). The snapshot is rendered in the browser and is
// untrusted. It is cleaned in two passes:
//
//  1. A tree pass over golang.org/x/net/html that applies the contract allowlist with the rules a
//     plain allowlist cannot express: aria-* / data-wb-* prefixes, style through the CSS sanitizer,
//     URL schemes per attribute, a[target] → rel, the form action, iframe sources, input types,
//     same-document SVG references and the single root element.
//  2. A bluemonday policy built from the same contract lists, as defence in depth. On the output
//     of pass 1 it is a no-op apart from serialisation details.

// Errors of SanitizeSnapshot (both answer 422 wb_invalid_doc).
var (
	ErrSnapshotTooLarge = errors.New("webbuilder: snapshot html too large")
	ErrSnapshotRoot     = errors.New("webbuilder: snapshot must be exactly one div.wb-page[data-wb-root]")
)

// SnapshotOptions tune SanitizeSnapshot.
type SnapshotOptions struct {
	// FormActionPrefix is the absolute URL of this post's form endpoint up to the section id,
	// e.g. "https://example.com/wp-json/web-builder/v2/forms/42/". A form action is kept only when
	// it is this prefix followed by an id matching IDPattern. Empty drops every action.
	FormActionPrefix string
	// EmbedHTML allows sandbox="allow-scripts" srcdoc iframes (unfiltered_html authors).
	EmbedHTML bool
	// SearchAction is the site's home URL with a trailing slash. A form[data-wb-search] (the
	// Theme Builder search form, contract §11.5) is kept with method="get", this action, one
	// input[type=search][name=s] and its submit button; every other control is removed.
	// Empty means "/".
	SearchAction string
}

// SanitizeSnapshot cleans the snapshot HTML of a publish. changed reports that the markup was
// altered beyond serialisation (the publish answer then lists StrippedMarkup).
func SanitizeSnapshot(src string, opts SnapshotOptions) (out string, changed bool, err error) {
	if len(src) > MaxSnapshotHTML {
		return "", false, ErrSnapshotTooLarge
	}
	nodes, err := parseFragment(src)
	if err != nil {
		return "", false, ErrSnapshotRoot
	}
	var root *html.Node
	for _, n := range nodes {
		switch n.Type {
		case html.TextNode:
			if strings.TrimSpace(n.Data) != "" {
				return "", false, ErrSnapshotRoot
			}
		case html.CommentNode:
		case html.ElementNode:
			if root != nil {
				return "", false, ErrSnapshotRoot
			}
			root = n
		default:
			return "", false, ErrSnapshotRoot
		}
	}
	if root == nil || root.Namespace != "" || root.DataAtom != atom.Div || !hasClass(root, "wb-page") || !hasAttr(root, AttrRoot) {
		return "", false, ErrSnapshotRoot
	}
	s := &treeSanitizer{opts: opts}
	s.element(root, false)
	out, err = renderNodes([]*html.Node{root})
	if err != nil {
		return "", false, err
	}
	final, err := pass2(out)
	if err != nil {
		return "", false, err
	}
	if final != out {
		// Pass 2 changed something pass 1 let through; treat it as a change, keep pass 2's output.
		s.changed = true
	}
	return final, s.changed, nil
}

// SanitizeSVG cleans an SVG fragment (BgSettings.DecorSVG, pasted icons) to the SVG subset. The
// result is "" unless the input is a single <svg> element.
func SanitizeSVG(src string) string {
	if strings.TrimSpace(src) == "" || len(src) > MaxStringBytes {
		return ""
	}
	nodes, err := parseFragment(src)
	if err != nil {
		return ""
	}
	var root *html.Node
	for _, n := range nodes {
		if n.Type == html.ElementNode {
			if root != nil {
				return ""
			}
			root = n
		} else if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" {
			return ""
		}
	}
	if root == nil || root.Namespace != "svg" || root.Data != "svg" {
		return ""
	}
	s := &treeSanitizer{svgOnly: true}
	s.element(root, false)
	out, err := renderNodes([]*html.Node{root})
	if err != nil {
		return ""
	}
	final, err := pass2(out)
	if err != nil {
		return ""
	}
	return final
}

// pass2 runs the bluemonday policy and re-serializes through the HTML parser, so the output has
// the browser's view of the markup (adjusted SVG names such as viewBox) and compares equal to the
// pass 1 output when the policy removed nothing.
func pass2(src string) (string, error) {
	nodes, err := parseFragment(snapshotPolicy().Sanitize(src))
	if err != nil {
		return "", err
	}
	return renderNodes(nodes)
}

// ---------------------------------------------------------------------------------------------
// URL rules shared by the doc validator, the sanitizers and the fallback generator.

var (
	schemeRE   = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*):`)
	dataImgRE  = regexp.MustCompile(`^(?i)data:(image/[a-z0-9.+-]+)[;,]`)
	urlCtrlSet = func(r rune) bool { return r <= 0x20 || (r >= 0x7f && r <= 0x9f) }
)

func stripURLControl(s string) string {
	return strings.Map(func(r rune) rune {
		if urlCtrlSet(r) {
			return -1
		}
		return r
	}, s)
}

// SafeHref mirrors upstream rich.ts safeHref: http(s), mailto, tel, "#anchor" or a relative path.
// It returns the trimmed value and false for anything else (javascript:, data:, //host, \host).
func SafeHref(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false
	}
	probe := stripURLControl(v)
	if m := schemeRE.FindStringSubmatch(probe); m != nil {
		switch strings.ToLower(m[1]) {
		case "http", "https", "mailto", "tel":
			return v, true
		}
		return "", false
	}
	if strings.HasPrefix(probe, "//") || strings.HasPrefix(probe, `\`) || strings.HasPrefix(probe, `/\`) {
		return "", false
	}
	if strings.ContainsAny(probe, `\`) && strings.HasPrefix(probe, "/") {
		return "", false
	}
	return v, true
}

// ImageURLOK reports whether raw is a usable image (or video) source: http, https, a relative
// path or, with allowData, a data:image/* URI (DataImageTypes) of at most MaxDataURIBytes.
func ImageURLOK(raw string, allowData bool) bool {
	v := strings.TrimSpace(raw)
	if v == "" {
		return false
	}
	probe := stripURLControl(v)
	if m := schemeRE.FindStringSubmatch(probe); m != nil {
		switch strings.ToLower(m[1]) {
		case "http", "https":
			return true
		case "data":
			if !allowData || len(v) > MaxDataURIBytes {
				return false
			}
			dm := dataImgRE.FindStringSubmatch(probe)
			if dm == nil {
				return false
			}
			mt := strings.ToLower(dm[1])
			for _, t := range DataImageTypes {
				if t == mt {
					return true
				}
			}
			return false
		}
		return false
	}
	if strings.HasPrefix(probe, "//") || strings.HasPrefix(probe, `\`) || strings.HasPrefix(probe, `/\`) {
		return false
	}
	return true
}

// videoEmbedOK: https on a VideoEmbedHosts host with an /embed/… or /video/… path.
func videoEmbedOK(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	ok := false
	for _, h := range VideoEmbedHosts {
		if h == host {
			ok = true
		}
	}
	if !ok {
		return false
	}
	return strings.HasPrefix(u.Path, "/embed/") || strings.HasPrefix(u.Path, "/video/")
}

// ---------------------------------------------------------------------------------------------
// Pass 1: tree sanitizer.

var (
	elementSet    = toSet(AllowedElements)
	svgElementSet = toSet(AllowedSVGElements)
	forbiddenSet  = toSetFold(ForbiddenElements)
	globalSet     = toSet(GlobalAttrs)
	svgAttrSet    = toSet(SVGAttrs)
	inputTypeSet  = toSet(InputTypes)
	elementAttrs  = func() map[string]map[string]bool {
		m := map[string]map[string]bool{}
		for el, attrs := range ElementAttrs {
			m[el] = toSet(attrs)
		}
		return m
	}()
	ariaSet = toSet(ariaAttrs)
	// Form control names (contract §3.5) plus the honeypot and timing fields.
	formNameSet  = toSet(append(append([]string{}, FormFields...), FormHoneypot, FormTiming))
	idPatternRE  = regexp.MustCompile(IDPattern)
	dataWBRE     = regexp.MustCompile(`^data-wb-[a-z0-9-]{1,40}$`)
	htmlIDRE     = regexp.MustCompile(`^[^\s"'<>&\x60]{1,128}$`)
	srcsetDescRE = regexp.MustCompile(`^[0-9.]+[wx]$`)
	sameDocRefRE = regexp.MustCompile(`^#[A-Za-z0-9_:.-]{1,128}$`)
	urlRefRE     = regexp.MustCompile(`(?i)url\(`)
	sameDocURLRE = regexp.MustCompile(`(?i)^\s*url\(\s*['"]?#[A-Za-z0-9_:.-]{1,128}['"]?\s*\)\s*$`)
	numberishRE  = regexp.MustCompile(`^[0-9.]{1,10}(%|px)?$`)
	allowTokens  = toSet([]string{"accelerometer", "autoplay", "clipboard-write", "encrypted-media", "gyroscope", "picture-in-picture", "fullscreen", "web-share"})
	relTokens    = toSet([]string{"noopener", "noreferrer", "nofollow", "ugc", "sponsored", "external"})
)

// ariaAttrs is WAI-ARIA 1.2 (aria-* attributes outside it are dropped).
var ariaAttrs = []string{
	"aria-activedescendant", "aria-atomic", "aria-autocomplete", "aria-braillelabel",
	"aria-brailleroledescription", "aria-busy", "aria-checked", "aria-colcount", "aria-colindex",
	"aria-colindextext", "aria-colspan", "aria-controls", "aria-current", "aria-describedby",
	"aria-description", "aria-details", "aria-disabled", "aria-dropeffect", "aria-errormessage",
	"aria-expanded", "aria-flowto", "aria-grabbed", "aria-haspopup", "aria-hidden", "aria-invalid",
	"aria-keyshortcuts", "aria-label", "aria-labelledby", "aria-level", "aria-live", "aria-modal",
	"aria-multiline", "aria-multiselectable", "aria-orientation", "aria-owns", "aria-placeholder",
	"aria-posinset", "aria-pressed", "aria-readonly", "aria-relevant", "aria-required",
	"aria-roledescription", "aria-rowcount", "aria-rowindex", "aria-rowindextext", "aria-rowspan",
	"aria-selected", "aria-setsize", "aria-sort", "aria-valuemax", "aria-valuemin", "aria-valuenow",
	"aria-valuetext",
}

func toSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, v := range list {
		m[v] = true
	}
	return m
}

func toSetFold(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, v := range list {
		m[strings.ToLower(v)] = true
	}
	return m
}

type treeSanitizer struct {
	opts     SnapshotOptions
	svgOnly  bool
	changed  bool
	inSearch bool // below a form[data-wb-search]
}

func parseFragment(src string) ([]*html.Node, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	return html.ParseFragment(strings.NewReader(src), ctx)
}

func renderNodes(nodes []*html.Node) (string, error) {
	var b strings.Builder
	for _, n := range nodes {
		if err := html.Render(&b, n); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return true
		}
	}
	return false
}

func getAttr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// children processes the children of n; inSVG is true below an <svg>.
func (s *treeSanitizer) children(n *html.Node, inSVG bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.TextNode:
		case html.ElementNode:
			s.element(c, inSVG)
		default: // comments, doctype
			n.RemoveChild(c)
			s.changed = true
		}
		c = next
	}
}

// element sanitizes n in place: it is removed, unwrapped or kept with clean attributes.
func (s *treeSanitizer) element(n *html.Node, inSVG bool) {
	name := n.Data
	lname := strings.ToLower(name)
	if forbiddenSet[lname] || (n.Namespace != "" && n.Namespace != "svg") {
		s.remove(n)
		return
	}
	var ok bool
	if n.Namespace == "svg" {
		ok = svgElementSet[name]
	} else {
		ok = elementSet[name] && !s.svgOnly
	}
	if !ok {
		s.unwrap(n, inSVG)
		return
	}
	isSVG := n.Namespace == "svg"
	if !isSVG && name == "form" && hasAttr(n, AttrSearch) && !s.inSearch {
		s.searchForm(n)
		return
	}
	s.attrs(n, isSVG)
	// Element-specific rules that may remove the element.
	switch {
	case !isSVG && name == "iframe":
		if !s.iframe(n) {
			s.remove(n)
			return
		}
	case !isSVG && name == "a":
		s.link(n)
	case !isSVG && name == "form":
		s.form(n)
	}
	if !isSVG && (name == "iframe" || name == "textarea") {
		// iframe content is never rendered; textarea content is text only.
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type != html.TextNode || name == "iframe" {
				n.RemoveChild(c)
				s.changed = true
			}
			c = next
		}
		return
	}
	s.children(n, inSVG || isSVG)
}

func (s *treeSanitizer) remove(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	} else {
		// The root itself: empty it.
		for n.FirstChild != nil {
			n.RemoveChild(n.FirstChild)
		}
		n.Attr = nil
	}
	s.changed = true
}

// unwrap replaces n by its (sanitized) children.
func (s *treeSanitizer) unwrap(n *html.Node, inSVG bool) {
	s.changed = true
	p := n.Parent
	if p == nil {
		for n.FirstChild != nil {
			n.RemoveChild(n.FirstChild)
		}
		n.Attr = nil
		return
	}
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}
	for _, c := range kids {
		n.RemoveChild(c)
		p.InsertBefore(c, n)
	}
	p.RemoveChild(n)
	for _, c := range kids {
		switch c.Type {
		case html.ElementNode:
			s.element(c, inSVG)
		case html.TextNode:
		default:
			p.RemoveChild(c)
		}
	}
}

func (s *treeSanitizer) attrs(n *html.Node, isSVG bool) {
	name := n.Data
	kept := n.Attr[:0:0]
	for _, a := range n.Attr {
		if v, ok := s.attr(name, isSVG, a); ok {
			a.Val = v
			kept = append(kept, a)
		} else {
			s.changed = true
		}
	}
	n.Attr = kept
}

// attr decides one attribute; it returns the (possibly rewritten) value.
func (s *treeSanitizer) attr(el string, isSVG bool, a html.Attribute) (string, bool) {
	key := a.Key
	if a.Namespace != "" {
		// Only xlink:href on SVG, same-document references only.
		if isSVG && a.Namespace == "xlink" && key == "href" && sameDocRefRE.MatchString(strings.TrimSpace(a.Val)) {
			return strings.TrimSpace(a.Val), true
		}
		return "", false
	}
	lkey := strings.ToLower(key)
	if strings.HasPrefix(lkey, "on") || strings.Contains(lkey, ":") {
		return "", false
	}
	val := a.Val
	switch {
	case lkey == "style":
		if val == "" {
			return "", false
		}
		out, changed := SanitizeStyleAttr(val)
		if changed {
			s.changed = true
		}
		if strings.TrimSpace(out) == "" {
			return "", false
		}
		return out, true
	case lkey == "id":
		return val, htmlIDRE.MatchString(val)
	case lkey == "tabindex":
		return val, val == "0" || val == "-1"
	case globalSet[lkey]:
		return val, true
	case strings.HasPrefix(lkey, "aria-"):
		return val, ariaSet[lkey]
	case strings.HasPrefix(lkey, "data-"):
		return val, dataWBRE.MatchString(lkey) && len(val) <= 256
	}
	if isSVG {
		allowed := svgAttrSet[key] || (el == "svg" && elementAttrs["svg"][key])
		if !allowed {
			return "", false
		}
		switch key {
		case "href":
			v := strings.TrimSpace(val)
			return v, sameDocRefRE.MatchString(v)
		case "clip-path", "mask", "fill", "stroke":
			if urlRefRE.MatchString(val) {
				return val, sameDocURLRE.MatchString(val)
			}
			return val, CSSValueOK(val)
		}
		if urlRefRE.MatchString(val) || !CSSValueOK(val) {
			return "", false
		}
		return val, true
	}
	if !elementAttrs[el][key] {
		return "", false
	}
	switch key {
	case "href":
		return SafeHref(val)
	case "src":
		switch el {
		case "iframe":
			return strings.TrimSpace(val), videoEmbedOK(val)
		case "video":
			return strings.TrimSpace(val), ImageURLOK(val, false)
		}
		return strings.TrimSpace(val), ImageURLOK(val, true)
	case "poster":
		return strings.TrimSpace(val), ImageURLOK(val, true)
	case "srcset":
		return srcsetOK(val)
	case "sizes", "media":
		return val, !urlRefRE.MatchString(val) && CSSValueOK(val)
	case "target":
		return "_blank", strings.EqualFold(strings.TrimSpace(val), "_blank")
	case "rel":
		var toks []string
		for _, t := range strings.Fields(strings.ToLower(val)) {
			if relTokens[t] {
				toks = append(toks, t)
			}
		}
		return strings.Join(toks, " "), len(toks) > 0
	case "type":
		v := strings.ToLower(strings.TrimSpace(val))
		switch el {
		case "input":
			return v, inputTypeSet[v] || (s.inSearch && v == SearchInputType)
		case "button":
			return v, v == "submit" || v == "button" || v == "reset"
		case "source":
			return v, !strings.Contains(v, "<")
		}
	case "name":
		return val, formNameSet[val] || (s.inSearch && el == "input" && val == SearchInputName)
	case "method":
		return "post", true
	case "action":
		return s.formAction(val)
	case "sandbox":
		return EmbedSandbox, strings.TrimSpace(val) == EmbedSandbox
	case "srcdoc":
		return val, s.opts.EmbedHTML
	case "allow":
		var toks []string
		for _, t := range strings.Split(val, ";") {
			t = strings.TrimSpace(strings.ToLower(t))
			if allowTokens[t] {
				toks = append(toks, t)
			}
		}
		return strings.Join(toks, "; "), len(toks) > 0
	case "loading":
		v := strings.ToLower(val)
		return v, v == "lazy" || v == "eager"
	case "decoding":
		v := strings.ToLower(val)
		return v, v == "async" || v == "sync" || v == "auto"
	case "fetchpriority":
		v := strings.ToLower(val)
		return v, v == "high" || v == "low" || v == "auto"
	case "referrerpolicy":
		v := strings.ToLower(val)
		switch v {
		case "no-referrer", "strict-origin-when-cross-origin", "strict-origin", "origin", "same-origin", "origin-when-cross-origin", "no-referrer-when-downgrade":
			return v, true
		}
		return "", false
	case "width", "height":
		return val, numberishRE.MatchString(strings.TrimSpace(val))
	case "for":
		return val, htmlIDRE.MatchString(val)
	case "preload":
		v := strings.ToLower(val)
		return v, v == "none" || v == "metadata" || v == "auto"
	}
	return val, true
}

func srcsetOK(val string) (string, bool) {
	parts := strings.Split(val, ",")
	var out []string
	for _, p := range parts {
		f := strings.Fields(p)
		if len(f) == 0 {
			continue
		}
		if len(f) > 2 || !ImageURLOK(f[0], false) {
			return "", false
		}
		if len(f) == 2 && !srcsetDescRE.MatchString(f[1]) {
			return "", false
		}
		out = append(out, strings.Join(f, " "))
	}
	return strings.Join(out, ", "), len(out) > 0
}

// link: target=_blank forces rel="noopener noreferrer".
func (s *treeSanitizer) link(n *html.Node) {
	if _, ok := getAttr(n, "target"); !ok {
		return
	}
	rel, _ := getAttr(n, "rel")
	toks := strings.Fields(rel)
	has := toSet(toks)
	for _, t := range []string{"noopener", "noreferrer"} {
		if !has[t] {
			toks = append(toks, t)
		}
	}
	v := strings.Join(toks, " ")
	for i := range n.Attr {
		if n.Attr[i].Key == "rel" && n.Attr[i].Namespace == "" {
			if n.Attr[i].Val != v {
				s.changed = true
			}
			n.Attr[i].Val = v
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: "rel", Val: v})
	s.changed = true
}

func (s *treeSanitizer) formAction(val string) (string, bool) {
	v := strings.TrimSpace(val)
	p := s.opts.FormActionPrefix
	if p == "" || !strings.HasPrefix(v, p) {
		return "", false
	}
	return v, idPatternRE.MatchString(v[len(p):])
}

// form: method is always post.
func (s *treeSanitizer) form(n *html.Node) {
	for i := range n.Attr {
		if n.Attr[i].Key == "method" {
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: "method", Val: "post"})
}

// searchForm sanitizes a form[data-wb-search]: method get, the home URL as action, the first
// search (or text) input becomes input[type=search][name=s], submit inputs and buttons stay,
// every other control goes (a GET form must not carry other query variables).
func (s *treeSanitizer) searchForm(n *html.Node) {
	origMethod, _ := getAttr(n, "method")
	origAction, _ := getAttr(n, "action")
	if hasAttr(n, AttrForm) {
		s.changed = true // a search form is never a builder form
	}
	delAttr(n, "method")
	delAttr(n, "action")
	delAttr(n, AttrForm)
	s.inSearch = true
	s.attrs(n, false)
	s.children(n, false)
	s.inSearch = false
	action := s.opts.SearchAction
	if v, ok := SafeHref(action); ok {
		action = v
	} else {
		action = "/"
	}
	delAttr(n, "method")
	delAttr(n, "action")
	n.Attr = append(n.Attr, html.Attribute{Key: "method", Val: "get"}, html.Attribute{Key: "action", Val: action})
	if strings.ToLower(strings.TrimSpace(origMethod)) != "get" || strings.TrimSpace(origAction) != action {
		s.changed = true
	}
	found := false
	var walk func(p *html.Node)
	walk = func(p *html.Node) {
		for c := p.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode && c.Namespace == "" {
				switch c.Data {
				case "input":
					ty, _ := getAttr(c, "type")
					switch {
					case ty == "submit":
						delAttr(c, "name")
					case !found && (ty == "" || ty == "text" || ty == SearchInputType):
						found = true
						if name, _ := getAttr(c, "name"); ty != SearchInputType || name != SearchInputName {
							s.changed = true
						}
						delAttr(c, "type")
						delAttr(c, "name")
						c.Attr = append(c.Attr, html.Attribute{Key: "type", Val: SearchInputType}, html.Attribute{Key: "name", Val: SearchInputName})
					default:
						p.RemoveChild(c)
						s.changed = true
					}
				case "select", "textarea":
					p.RemoveChild(c)
					s.changed = true
				default:
					walk(c)
				}
			}
			c = next
		}
	}
	walk(n)
}

func delAttr(n *html.Node, key string) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			continue
		}
		kept = append(kept, a)
	}
	n.Attr = kept
}

// iframe keeps either a video embed (src) or, for EmbedHTML, a sandboxed srcdoc.
func (s *treeSanitizer) iframe(n *html.Node) bool {
	_, hasSrc := getAttr(n, "src")
	_, hasDoc := getAttr(n, "srcdoc")
	sandbox, hasSandbox := getAttr(n, "sandbox")
	switch {
	case hasDoc && s.opts.EmbedHTML:
		// srcdoc wins; drop src and the permissions policy, force the sandbox.
		kept := n.Attr[:0:0]
		for _, a := range n.Attr {
			if a.Key == "src" || a.Key == "allow" || a.Key == "sandbox" {
				continue
			}
			kept = append(kept, a)
		}
		if hasSrc || !hasSandbox || sandbox != EmbedSandbox {
			s.changed = true
		}
		n.Attr = append(kept, html.Attribute{Key: "sandbox", Val: EmbedSandbox})
		return true
	case hasSrc:
		kept := n.Attr[:0:0]
		for _, a := range n.Attr {
			if a.Key == "sandbox" || a.Key == "srcdoc" {
				s.changed = true
				continue
			}
			kept = append(kept, a)
		}
		n.Attr = kept
		return true
	}
	return false
}

// ---------------------------------------------------------------------------------------------
// Pass 2: bluemonday policy from the contract lists.

var (
	policyOnce sync.Once
	policy     *bluemonday.Policy
)

func snapshotPolicy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.NewPolicy()
		var all []string
		for _, e := range AllowedElements {
			all = append(all, e)
		}
		for _, e := range AllowedSVGElements {
			all = append(all, strings.ToLower(e))
		}
		p.AllowElements(all...)
		p.AllowNoAttrs().OnElements(all...)
		p.AllowAttrs(GlobalAttrs...).Globally()
		p.AllowAttrs(ariaAttrs...).Globally()
		p.AllowDataAttributes() // pass 1 keeps data-wb-* only
		re := func(s string) *regexp.Regexp { return regexp.MustCompile(s) }
		constrained := map[string]*regexp.Regexp{
			"target":  re(`^_blank$`),
			"sandbox": re(`^` + regexp.QuoteMeta(EmbedSandbox) + `$`),
			"method":  re(`^(post|get)$`),
		}
		for el, attrs := range ElementAttrs {
			for _, a := range attrs {
				la := strings.ToLower(a)
				if r, ok := constrained[la]; ok {
					p.AllowAttrs(la).Matching(r).OnElements(el)
					continue
				}
				if el == "input" && la == "type" {
					p.AllowAttrs("type").Matching(re(`^(` + strings.Join(append(append([]string{}, InputTypes...), SearchInputType), "|") + `)$`)).OnElements("input")
					continue
				}
				p.AllowAttrs(la).OnElements(el)
			}
		}
		var svgEls []string
		for _, e := range AllowedSVGElements {
			svgEls = append(svgEls, strings.ToLower(e))
		}
		for _, a := range SVGAttrs {
			if a == "href" {
				p.AllowAttrs("href").Matching(sameDocRefRE).OnElements(svgEls...)
				continue
			}
			p.AllowAttrs(strings.ToLower(a)).OnElements(svgEls...)
		}
		p.AllowAttrs("xlink:href").Matching(sameDocRefRE).OnElements(svgEls...)
		p.RequireParseableURLs(true)
		p.AllowRelativeURLs(true)
		p.AllowURLSchemes("http", "https", "mailto", "tel")
		p.AllowURLSchemeWithCustomPolicy("data", func(u *url.URL) bool {
			return ImageURLOK(u.String(), true)
		})
		p.SkipElementsContent(ForbiddenElements...)
		p.SkipElementsContent("foreignobject", "animatemotion", "animatetransform")
		policy = p
	})
	return policy
}
