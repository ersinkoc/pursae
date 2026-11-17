package webbuilder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Content bindings of code sections (docs/developer/web-builder-code-sections.md, "Binding to
// content"). An imported design keeps its own markup; a binding names an element of it (by its
// data-wbc id) and what fills it on the site:
//
//   - menu: a list whose element children are menu item templates; on the site the list holds one
//     item per item of a Pursae menu (a theme location or a menu id). Every child keeps its markup
//     as a template: an item picks the template it was imported from (Items), else the first child
//     with a submenu ("parent") when it has sub-items, else the first child without one. The link
//     (the first <a>) gets the item's URL and target, the label (the first element with text of
//     its own) the item's title; a submenu binding inside a template repeats that list's children
//     per sub-item (one level).
//   - loop: a container whose first element child is the card template, repeated per post of a
//     query (LoopSpec keys, source query by default); its other children are the design's sample
//     cards and are left out on the site.
//   - field: an element filled from the post of the nearest loop card, else the post being viewed
//     (a Theme Builder single template).
//   - form: a design's <form> that posts to the Pursae form endpoint (CodeFormSchema).
//
// The bindings are stored as JSON in the section's content.binds ({"<data-wbc>": CodeBind}) so the
// design's markup stays as written. On publish CompileBinds turns them into the dynamic engine's
// placeholder attributes (data-wb-menu-loc, data-wb-loop, data-wb-slot, data-wb-text, …) on the
// bound elements only, splicing the source: everything else stays byte for byte.

// ContentBinds is the code section content key holding the bindings.
const ContentBinds = "binds"

// TrustedHTMLField prefixes the protected post meta key ("_wb_html_<name>") under which a site
// import keeps a page's varying design markup for a custom html field: protected meta is written
// only by trusted code (never by the custom fields box or the REST API), so post.field-html prints
// it as stored (the design's SVG icons, data attributes); the public field <name> is sanitized.
const TrustedHTMLField = "_wb_html_"

// Binding kinds.
const (
	BindMenu    = "menu"
	BindSubmenu = "submenu"
	BindLoop    = "loop"
	BindField   = "field"
	BindForm    = "form"
)

// Limits.
const (
	MaxCodeBinds      = 200
	MaxCodeBindItems  = 200 // menu item → template entries
	MaxCodeFormFields = 40
)

var (
	BindKinds = []string{BindMenu, BindSubmenu, BindLoop, BindField, BindForm}
	// BindFields are what a field binding shows.
	// BindFields are what a field binding shows ("link": a link to the post with its title as text).
	BindFields = []string{"title", "excerpt", "content", "image", "url", "link", "date", "modified", "author", "terms", "custom", "pagination"}
	// BindCustomAttrs: how a custom field fills its element: "" its text, "html" its content (the
	// value sanitized like post content), "url" its href (src on an image), "attrs" the attributes
	// href, src, srcset, alt, … from the fields <name>_href, <name>_src, … (and its text from <name>
	// when Text is set).
	BindCustomAttrs = []string{"", "html", "url", "attrs"}

	bindKindSet   = toSet(BindKinds)
	bindFieldSet  = toSet(BindFields)
	bindCustomSet = toSet(BindCustomAttrs)
	wbcIDRE       = regexp.MustCompile(`^[0-9]{1,9}$`)
	// FormFieldPattern: the generic Pursae form field name (field_<name>) besides FormFields.
	FormFieldPattern = `^field_[a-z0-9_]{1,40}$`
	formFieldRE      = regexp.MustCompile(FormFieldPattern)
)

// CodeBind is one binding (see the file comment). Every field is optional except Kind.
type CodeBind struct {
	Kind string `json:"kind"`

	// menu: a menu location of the active theme, or a menu id; Items: menu item id → the data-wbc
	// of the template it uses (a child of the list or of a submenu list).
	Location string            `json:"location,omitempty"`
	Menu     int64             `json:"menu,omitempty"`
	Items    map[string]string `json:"items,omitempty"`

	// loop: LoopSpec content keys (source, postType, perPage, orderBy, order, terms, author,
	// offset, parent, exclude); source defaults to query.
	Query map[string]string `json:"query,omitempty"`

	// field
	Field  string `json:"field,omitempty"`  // BindFields
	Name   string `json:"name,omitempty"`   // custom: the field name; image: a URL field used when the post has no featured image
	Attr   string `json:"attr,omitempty"`   // custom: BindCustomAttrs
	Text   bool   `json:"text,omitempty"`   // custom attrs: the element's text from <name> too
	Format string `json:"format,omitempty"` // date, modified
	Tax    string `json:"tax,omitempty"`    // terms
	Sep    string `json:"sep,omitempty"`    // terms
	Links  bool   `json:"links,omitempty"`  // terms: links to the term archives (else names)
	Words  int    `json:"words,omitempty"`  // excerpt
	Size   string `json:"size,omitempty"`   // image with Srcset: DynImageSizes
	Srcset bool   `json:"srcset,omitempty"` // image: srcset, sizes, width and height of the attachment (else src and alt only)

	// form: input name → Pursae field (FormFields, field_<name>; "-" leaves the input out);
	// inputs not named here map to themselves when they are FormFields, else to field_<name>.
	Fields   map[string]string `json:"fields,omitempty"`
	Required []string          `json:"required,omitempty"` // input names that must be filled (besides required inputs)
	Done     string            `json:"done,omitempty"`     // data-wbc of the design's success element
	Error    string            `json:"error,omitempty"`    // data-wbc of the design's error element
	Title    string            `json:"title,omitempty"`    // mail subject (else the form's heading or the page title)
	To       string            `json:"to,omitempty"`
	Cc       string            `json:"cc,omitempty"`
	Success  string            `json:"success,omitempty"`
	Failure  string            `json:"failure,omitempty"`
	Redirect string            `json:"redirect,omitempty"`
}

// CodeBinds are a section's bindings by data-wbc.
type CodeBinds map[string]*CodeBind

// ParseCodeBinds reads content.binds leniently: entries that do not check are dropped.
func ParseCodeBinds(s string) CodeBinds {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var raw map[string]*CodeBind
	if json.Unmarshal([]byte(s), &raw) != nil {
		return nil
	}
	out := CodeBinds{}
	for id, b := range raw {
		if b == nil || !wbcIDRE.MatchString(id) || len(checkBind(b)) > 0 {
			continue
		}
		out[id] = b
	}
	return out
}

// String is the bindings as stored.
func (b CodeBinds) String() string {
	if len(b) == 0 {
		return ""
	}
	out, _ := json.Marshal(map[string]*CodeBind(b))
	return string(out)
}

// CheckCodeBinds validates content.binds for a save; it returns one message per problem.
func CheckCodeBinds(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var raw map[string]*CodeBind
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return []string{"binds must be a JSON object of bindings by element id"}
	}
	if len(raw) > MaxCodeBinds {
		return []string{fmt.Sprintf("more than %d bindings", MaxCodeBinds)}
	}
	var out []string
	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !wbcIDRE.MatchString(id) {
			out = append(out, fmt.Sprintf("binding %q: not an element id", trunc(id, 20)))
			continue
		}
		if raw[id] == nil {
			out = append(out, "binding "+id+": empty")
			continue
		}
		for _, m := range checkBind(raw[id]) {
			out = append(out, "binding "+id+": "+m)
		}
	}
	return out
}

func checkBind(b *CodeBind) []string {
	var out []string
	bad := func(f string, a ...any) { out = append(out, fmt.Sprintf(f, a...)) }
	if !bindKindSet[b.Kind] {
		bad("unknown kind %q", trunc(b.Kind, 20))
		return out
	}
	short := func(name, v string, max int) {
		if utf8.RuneCountInString(v) > max || !utf8.ValidString(v) {
			bad("%s is too long", name)
		}
	}
	switch b.Kind {
	case BindMenu:
		if b.Location == "" && b.Menu <= 0 {
			bad("a menu needs a location or a menu id")
		}
		if b.Location != "" && !menuLocRE.MatchString(b.Location) {
			bad("bad menu location")
		}
		if len(b.Items) > MaxCodeBindItems {
			bad("more than %d menu items", MaxCodeBindItems)
		}
		for k, v := range b.Items {
			if _, ok := parseID(k); !ok || !wbcIDRE.MatchString(v) {
				bad("bad menu item template %q", trunc(k, 20))
				break
			}
		}
	case BindLoop:
		if len(b.Query) > 16 {
			bad("too many query keys")
		}
		for k, v := range b.Query {
			switch k {
			case LoopSource, LoopPostType, LoopPerPage, LoopOrderBy, LoopOrder, LoopTerms, LoopAuthor, LoopExclude, LoopOffset, LoopParent, LoopPagination:
				short("query "+k, v, 400)
			default:
				bad("unknown query key %q", trunc(k, 20))
			}
		}
	case BindField:
		if !bindFieldSet[b.Field] {
			bad("unknown field %q", trunc(b.Field, 20))
		}
		if b.Name != "" && !fieldRE.MatchString(b.Name) {
			bad("bad field name")
		}
		if b.Field == "custom" && b.Name == "" {
			bad("a custom field needs a name")
		}
		if !bindCustomSet[b.Attr] {
			bad("bad attr %q", trunc(b.Attr, 20))
		}
		if b.Format != "" && !dynFormatRE.MatchString(b.Format) {
			bad("bad date format")
		}
		if b.Tax != "" && !slugRE.MatchString(b.Tax) {
			bad("bad taxonomy")
		}
		if utf8.RuneCountInString(b.Sep) > MaxDynSepRunes {
			bad("separator too long")
		}
		if b.Words != 0 && (b.Words < MinDynWords || b.Words > MaxDynWords) {
			bad("words out of range")
		}
		if b.Size != "" && !dynSizeSet[b.Size] {
			bad("bad image size")
		}
	case BindForm:
		if len(b.Fields) > MaxCodeFormFields {
			bad("more than %d fields", MaxCodeFormFields)
		}
		for in, f := range b.Fields {
			if !formInputRE.MatchString(in) || (f != "-" && !FormFieldOK(f)) {
				bad("bad field mapping %q", trunc(in, 30))
				break
			}
		}
		for _, in := range b.Required {
			if !formInputRE.MatchString(in) {
				bad("bad required input")
				break
			}
		}
		for _, id := range []string{b.Done, b.Error} {
			if id != "" && !wbcIDRE.MatchString(id) {
				bad("bad message element")
			}
		}
		short("title", b.Title, 200)
		short("to", b.To, 1000)
		short("cc", b.Cc, 1000)
		short("success", b.Success, 500)
		short("failure", b.Failure, 500)
		short("redirect", b.Redirect, 2000)
	}
	return out
}

var formInputRE = regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]{1,64}$`)

// FormFieldOK reports whether name is a Pursae form field name: one of FormFields or field_<name>.
func FormFieldOK(name string) bool {
	for _, f := range FormFields {
		if f == name {
			return true
		}
	}
	return formFieldRE.MatchString(name)
}

// BindKey is the id of a bound element in the snapshot and on the routes (a loop's data-wb-loop,
// a form's {section}): "<section id>-w<data-wbc>".
func BindKey(section, wbc string) string { return section + "-w" + wbc }

// SplitBindKey is BindKey's inverse.
func SplitBindKey(key string) (section, wbc string, ok bool) {
	i := strings.LastIndex(key, "-w")
	if i <= 0 || !wbcIDRE.MatchString(key[i+2:]) {
		return "", "", false
	}
	return key[:i], key[i+2:], true
}

// MenuLoc is the data-wb-menu-loc value of a menu binding: the location, or "menu-<id>".
func (b *CodeBind) MenuLoc() string {
	if b.Location != "" {
		return b.Location
	}
	return "menu-" + strconv.FormatInt(b.Menu, 10)
}

// MenuIDOfLoc reads a "menu-<id>" location (0: a theme location).
func MenuIDOfLoc(loc string) int64 {
	if !strings.HasPrefix(loc, "menu-") {
		return 0
	}
	id, ok := parseID(loc[len("menu-"):])
	if !ok {
		return 0
	}
	return id
}

// CodeLoopSpec is the query of a loop binding (source query unless the binding says otherwise).
func (b *CodeBind) CodeLoopSpec() LoopSpec {
	q := map[string]string{LoopSource: LoopSourceQuery}
	for k, v := range b.Query {
		q[k] = v
	}
	return ParseLoopSpec(q)
}

// ---------------------------------------------------------------------------------------------
// Source tree: a light parse that keeps every element's byte range, so bindings splice the
// markup instead of re-rendering it.

type srcNode struct {
	tag        string // "" for a text node
	attrs      []html.Attribute
	start      int // the start tag (or the text) begins
	openEnd    int // the start tag ends
	closeStart int // the end tag begins (= end when there is none)
	end        int // the element ends
	selfClose  bool
	raw        string // text nodes: the source text
	kids       []*srcNode
	parent     *srcNode
}

func (n *srcNode) attr(k string) (string, bool) {
	for _, a := range n.attrs {
		if a.Key == k {
			return a.Val, true
		}
	}
	return "", false
}

func (n *srcNode) wbc() string { v, _ := n.attr(AttrCodeID); return v }

// elems are the element children.
func (n *srcNode) elems() []*srcNode {
	var out []*srcNode
	for _, k := range n.kids {
		if k.tag != "" {
			out = append(out, k)
		}
	}
	return out
}

// ownText: the element has a direct text child that is not blank.
func (n *srcNode) ownText() bool {
	for _, k := range n.kids {
		if k.tag == "" && strings.TrimSpace(html.UnescapeString(k.raw)) != "" {
			return true
		}
	}
	return false
}

func (n *srcNode) hasElems() bool {
	for _, k := range n.kids {
		if k.tag != "" {
			return true
		}
	}
	return false
}

// parseSource parses markup into srcNodes (a root holding the top-level nodes).
func parseSource(src string) *srcNode {
	root := &srcNode{tag: "#root", end: len(src), closeStart: len(src)}
	stack := []*srcNode{root}
	z := html.NewTokenizer(strings.NewReader(src))
	pos := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		start := pos
		pos += len(z.Raw())
		top := stack[len(stack)-1]
		switch tt {
		case html.TextToken:
			top.kids = append(top.kids, &srcNode{start: start, end: pos, raw: src[start:pos], parent: top})
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			n := &srcNode{tag: string(name), start: start, openEnd: pos, parent: top}
			for more {
				var k, v []byte
				k, v, more = z.TagAttr()
				n.attrs = append(n.attrs, html.Attribute{Key: string(k), Val: string(v)})
			}
			top.kids = append(top.kids, n)
			if tt == html.SelfClosingTagToken || voidElements[n.tag] {
				n.end, n.closeStart, n.selfClose = pos, pos, true
				continue
			}
			stack = append(stack, n)
		case html.EndTagToken:
			name, _ := z.TagName()
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].tag == string(name) {
					for j := len(stack) - 1; j > i; j-- {
						stack[j].end, stack[j].closeStart = start, start
					}
					stack[i].closeStart, stack[i].end = start, pos
					stack = stack[:i]
					break
				}
			}
		}
	}
	for _, n := range stack[1:] {
		n.end, n.closeStart = len(src), len(src)
	}
	return root
}

// index maps data-wbc ids to their elements.
func (n *srcNode) index(m map[string]*srcNode) {
	if n.tag != "" && n.tag != "#root" {
		if id := n.wbc(); id != "" {
			if _, dup := m[id]; !dup {
				m[id] = n
			}
		}
	}
	for _, k := range n.kids {
		k.index(m)
	}
}

func (n *srcNode) within(a *srcNode) bool {
	for x := n; x != nil; x = x.parent {
		if x == a {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------
// Compiling.

// CodeFillOptions are what FillCode needs beyond the sections.
type CodeFillOptions struct {
	// FormActionPrefix is the form endpoint of the page ("https://…/wp-json/web-builder/v2/forms/42/");
	// "" leaves form bindings out.
	FormActionPrefix string
}

type spliceEdit struct {
	at, end int
	text    string
}

type bindCompiler struct {
	src     string
	sec     string
	opts    CodeFillOptions
	byID    map[string]*srcNode
	adds    map[*srcNode][]html.Attribute // attributes inserted before the start tag's end
	sets    map[*srcNode]map[string]string
	drops   []*srcNode
	inserts []spliceEdit
	subs    map[*srcNode]bool // elements bound as submenus
	binds   CodeBinds
}

// CompileBinds turns a code section's bindings into placeholder attributes (see the file
// comment). Bindings whose element is gone are skipped.
func CompileBinds(src, section string, binds CodeBinds, o CodeFillOptions) string {
	if len(binds) == 0 || src == "" {
		return src
	}
	root := parseSource(src)
	c := &bindCompiler{src: src, sec: section, opts: o, byID: map[string]*srcNode{}, adds: map[*srcNode][]html.Attribute{},
		sets: map[*srcNode]map[string]string{}, subs: map[*srcNode]bool{}, binds: binds}
	root.index(c.byID)
	ids := make([]string, 0, len(binds))
	for id := range binds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { a, _ := strconv.Atoi(ids[i]); b, _ := strconv.Atoi(ids[j]); return a < b })
	for _, id := range ids {
		if b := binds[id]; b.Kind == BindSubmenu {
			if n := c.byID[id]; n != nil {
				c.subs[n] = true
			}
		}
	}
	for _, id := range ids {
		n := c.byID[id]
		if n == nil {
			continue
		}
		b := binds[id]
		switch b.Kind {
		case BindMenu:
			c.menu(n, b, id)
		case BindLoop:
			c.loop(n, id)
		case BindField:
			c.field(n, b)
		case BindForm:
			c.form(n, b, id)
		}
	}
	return c.apply()
}

func (c *bindCompiler) add(n *srcNode, k, v string) {
	for i, a := range c.adds[n] {
		if a.Key == k {
			c.adds[n][i].Val = v
			return
		}
	}
	if _, has := n.attr(k); has {
		if c.sets[n] == nil {
			c.sets[n] = map[string]string{}
		}
		c.sets[n][k] = v
		return
	}
	c.adds[n] = append(c.adds[n], html.Attribute{Key: k, Val: v})
}

func (c *bindCompiler) set(n *srcNode, k, v string) {
	if c.sets[n] == nil {
		c.sets[n] = map[string]string{}
	}
	c.sets[n][k] = v
}

// menu: the list's children are templates; see the file comment.
func (c *bindCompiler) menu(n *srcNode, b *CodeBind, id string) {
	c.add(n, AttrMenuLoc, b.MenuLoc())
	c.add(n, AttrMenuTree, "")
	c.templates(n, b)
}

// templates marks the element children of a menu list (or submenu list) as item templates.
func (c *bindCompiler) templates(list *srcNode, b *CodeBind) {
	byTpl := map[string][]string{}
	for item, tpl := range b.Items {
		byTpl[tpl] = append(byTpl[tpl], "k:"+item)
	}
	def, parent := false, false
	for _, t := range list.elems() {
		var toks []string
		sub := c.subIn(t)
		switch {
		case sub == nil && !def:
			toks, def = append(toks, "default"), true
		case sub != nil && !parent:
			toks, parent = append(toks, "parent"), true
		}
		ks := byTpl[t.wbc()]
		sort.Strings(ks)
		toks = append(toks, ks...)
		if len(toks) == 0 {
			toks = []string{"t"}
		}
		c.add(t, AttrLoopItem, strings.Join(toks, " "))
		c.menuItem(t, sub)
		if sub != nil {
			c.add(sub, AttrMenuSub, "")
			c.templates(sub, b)
		}
	}
}

// subIn finds the submenu list bound inside a template (not inside a nested one).
func (c *bindCompiler) subIn(t *srcNode) *srcNode {
	var found *srcNode
	var walk func(n *srcNode)
	walk = func(n *srcNode) {
		for _, k := range n.kids {
			if found != nil || k.tag == "" {
				continue
			}
			if c.subs[k] {
				found = k
				return
			}
			walk(k)
		}
	}
	walk(t)
	return found
}

// menuItem binds the link and the label of one item template (outside its submenu).
func (c *bindCompiler) menuItem(t, sub *srcNode) {
	var link, label, firstText *srcNode
	var walk func(n *srcNode)
	walk = func(n *srcNode) {
		if n == sub || n.tag == "svg" || n.tag == "script" || n.tag == "style" || n.tag == "template" {
			return
		}
		if link == nil && n.tag == "a" {
			link = n
		}
		if firstText == nil && n.ownText() {
			firstText = n
		}
		for _, k := range n.kids {
			if k.tag != "" {
				walk(k)
			}
		}
	}
	walk(t)
	switch {
	case link != nil && link.ownText():
		label = link
	case link != nil:
		// The label is inside the link (<a><span>Label</span></a>) when the link has text at all.
		var inner func(n *srcNode)
		inner = func(n *srcNode) {
			for _, k := range n.kids {
				if label != nil || k.tag == "" || k.tag == "svg" {
					continue
				}
				if k.ownText() {
					label = k
					return
				}
				inner(k)
			}
		}
		inner(link)
		if label == nil {
			label = firstText
		}
	default:
		label = firstText
	}
	if link != nil {
		c.add(link, AttrSlot, string(SlotMenuLink))
	}
	if label != nil {
		c.add(label, AttrText, string(SlotMenuLabel))
	}
}

func (c *bindCompiler) loop(n *srcNode, id string) {
	key := BindKey(c.sec, id)
	if !idPatternRE.MatchString(key) {
		return
	}
	c.add(n, AttrLoop, key)
	seen := false
	for i, k := range n.kids {
		if k.tag == "" {
			continue
		}
		if !seen {
			seen = true
			c.add(k, AttrLoopItem, "")
			continue
		}
		// A sample card goes with the blank text before it (the engine repeats the template with
		// the blank text that follows it).
		d := &srcNode{tag: k.tag, start: k.start, end: k.end}
		if i > 0 && n.kids[i-1].tag == "" && strings.TrimSpace(n.kids[i-1].raw) == "" {
			d.start = n.kids[i-1].start
		}
		c.drops = append(c.drops, d)
	}
}

// textSlot fills n's text: its own text when it has elements too (an icon beside a date), else
// its whole content.
func (c *bindCompiler) textSlot(n *srcNode, slot SlotName) {
	if n.hasElems() && n.ownText() {
		c.add(n, AttrText, string(slot))
		return
	}
	c.add(n, AttrSlot, string(slot))
}

func (c *bindCompiler) field(n *srcNode, b *CodeBind) {
	switch b.Field {
	case "title":
		c.textSlot(n, SlotPostTitleText)
	case "excerpt":
		c.add(n, AttrSlot, string(SlotPostExcerpt))
		if b.Words > 0 {
			c.add(n, AttrWords, strconv.Itoa(b.Words))
		}
	case "content":
		c.add(n, AttrSlot, string(SlotPostContent))
		c.add(n, AttrRaw, "")
	case "image":
		img := n
		if n.tag != "img" {
			img = nil
			var find func(x *srcNode)
			find = func(x *srcNode) {
				for _, k := range x.kids {
					if img == nil && k.tag == "img" {
						img = k
					}
					if img == nil && k.tag != "" {
						find(k)
					}
				}
			}
			find(n)
			if img == nil {
				return
			}
		}
		c.add(img, AttrSlot, string(SlotPostImage))
		if b.Srcset {
			if b.Size != "" {
				c.add(img, AttrSize, b.Size)
			}
		} else {
			c.add(img, AttrRaw, "")
		}
		if b.Name != "" {
			c.add(img, AttrField, b.Name)
			c.add(img, AttrMeta, "")
		}
	case "url":
		c.add(n, AttrSlot, string(SlotPostURL))
	case "link":
		c.add(n, AttrSlot, string(SlotPostURL))
		c.add(n, AttrText, string(SlotPostTitleText))
	case "date", "modified":
		slot := SlotPostDate
		if b.Field == "modified" {
			slot = SlotPostModified
		}
		c.textSlot(n, slot)
		if b.Format != "" {
			c.add(n, AttrFormat, b.Format)
		}
	case "author":
		c.textSlot(n, SlotPostAuthor)
	case "terms":
		if b.Links {
			c.add(n, AttrSlot, string(SlotPostTerms))
		} else {
			c.textSlot(n, SlotPostTermNames)
		}
		if b.Tax != "" {
			c.add(n, AttrTax, b.Tax)
		}
		if b.Sep != "" {
			c.add(n, AttrSep, b.Sep)
		}
	case "pagination":
		c.add(n, AttrSlot, string(SlotLoopPagination))
	case "custom":
		c.add(n, AttrField, b.Name)
		c.add(n, AttrMeta, "")
		switch b.Attr {
		case "html":
			c.add(n, AttrSlot, string(SlotPostFieldHTML))
		case "url":
			c.add(n, AttrSlot, string(SlotPostFieldURL))
		case "attrs":
			c.add(n, AttrSlot, string(SlotPostFieldAttrs))
			if b.Text {
				c.add(n, AttrText, string(SlotPostField))
			}
		default:
			c.textSlot(n, SlotPostField)
		}
	}
}

func (c *bindCompiler) form(n *srcNode, b *CodeBind, id string) {
	if n.tag != "form" || c.opts.FormActionPrefix == "" {
		return
	}
	key := BindKey(c.sec, id)
	if !idPatternRE.MatchString(key) {
		return
	}
	c.set(n, "action", c.opts.FormActionPrefix+key)
	c.set(n, "method", "post")
	c.add(n, AttrForm, "")
	c.add(n, AttrCodeForm, key)
	if d := c.byID[b.Done]; d != nil && b.Done != "" {
		c.add(d, AttrFormDone, key)
	}
	if e := c.byID[b.Error]; e != nil && b.Error != "" {
		c.add(e, AttrFormError, key)
	}
	if !n.selfClose {
		c.inserts = append(c.inserts, spliceEdit{at: n.closeStart, end: n.closeStart, text: `<input type="hidden" name="` + FormTiming + `" value="">` +
			`<input type="text" name="` + FormHoneypot + `" value="" tabindex="-1" autocomplete="off" aria-hidden="true" style="position:absolute;left:-9999px;width:1px;height:1px;opacity:0">`})
	}
}

// apply writes the edits into the source.
func (c *bindCompiler) apply() string {
	var edits []spliceEdit
	dropped := func(at int) bool {
		for _, d := range c.drops {
			if at >= d.start && at < d.end {
				return true
			}
		}
		return false
	}
	for _, d := range c.drops {
		edits = append(edits, spliceEdit{at: d.start, end: d.end})
	}
	tagEdit := func(n *srcNode) {
		if dropped(n.start) {
			return
		}
		sets, adds := c.sets[n], c.adds[n]
		if len(sets) == 0 {
			var b strings.Builder
			for _, a := range adds {
				writeAttr(&b, a.Key, a.Val)
			}
			raw := c.src[n.start:n.openEnd]
			at := n.openEnd - 1
			if strings.HasSuffix(raw, "/>") {
				at = n.openEnd - 2
			}
			edits = append(edits, spliceEdit{at: at, end: at, text: b.String()})
			return
		}
		var b strings.Builder
		b.WriteString("<" + n.tag)
		seen := map[string]bool{}
		for _, a := range n.attrs {
			if seen[a.Key] {
				continue
			}
			seen[a.Key] = true
			v := a.Val
			if sv, ok := sets[a.Key]; ok {
				v = sv
			}
			writeAttr(&b, a.Key, v)
		}
		keys := make([]string, 0, len(sets))
		for k := range sets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				writeAttr(&b, k, sets[k])
			}
		}
		for _, a := range adds {
			if !seen[a.Key] {
				writeAttr(&b, a.Key, a.Val)
			}
		}
		if strings.HasSuffix(c.src[n.start:n.openEnd], "/>") {
			b.WriteString("/")
		}
		b.WriteString(">")
		edits = append(edits, spliceEdit{at: n.start, end: n.openEnd, text: b.String()})
	}
	done := map[*srcNode]bool{}
	for n := range c.adds {
		done[n] = true
		tagEdit(n)
	}
	for n := range c.sets {
		if !done[n] {
			tagEdit(n)
		}
	}
	for _, e := range c.inserts {
		if !dropped(e.at) {
			edits = append(edits, e)
		}
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].at != edits[j].at {
			return edits[i].at > edits[j].at
		}
		return edits[i].end > edits[j].end
	})
	out := c.src
	last := len(out) + 1
	for _, e := range edits {
		if e.end > last {
			continue // overlaps an edit already applied
		}
		out = out[:e.at] + e.text + out[e.end:]
		last = e.at
	}
	return out
}

func writeAttr(b *strings.Builder, k, v string) {
	b.WriteString(" " + k)
	if v == "" {
		b.WriteString(`=""`)
		return
	}
	b.WriteString(`="`)
	b.WriteString(strings.NewReplacer(`&`, "&amp;", `"`, "&#34;", `<`, "&lt;", `>`, "&gt;").Replace(v))
	b.WriteString(`"`)
}

// ---------------------------------------------------------------------------------------------
// Forms.

// codeFormSchemas lists the forms bound in a code section.
func codeFormSchemas(sectionID, src string, binds CodeBinds) []FormSchema {
	var out []FormSchema
	var ids []string
	for id, b := range binds {
		if b.Kind == BindForm {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	nodes, err := parseFragment(src)
	if err != nil {
		return nil
	}
	byID := map[string]*html.Node{}
	var index func(n *html.Node)
	index = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if v, ok := getAttr(n, AttrCodeID); ok {
				if _, dup := byID[v]; !dup {
					byID[v] = n
				}
			}
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			index(k)
		}
	}
	for _, n := range nodes {
		index(n)
	}
	for _, id := range ids {
		f := byID[id]
		if f == nil || f.Data != "form" {
			continue
		}
		out = append(out, CodeFormSchema(f, BindKey(sectionID, id), binds[id], byID))
	}
	return out
}

// CodeFormSchema derives a bound form's schema from its markup: every named input, select and
// textarea (not buttons, files or hidden inputs), mapped by the binding's Fields, labelled by its
// <label>, required when the input says so or the binding lists it.
func CodeFormSchema(form *html.Node, key string, b *CodeBind, byID map[string]*html.Node) FormSchema {
	fs := FormSchema{SectionID: key, Variant: "code", Title: strings.TrimSpace(b.Title), To: strings.TrimSpace(b.To), Cc: strings.TrimSpace(b.Cc),
		Success: strings.TrimSpace(b.Success), Failure: strings.TrimSpace(b.Failure), Redirect: strings.TrimSpace(b.Redirect)}
	labels := map[string]string{}
	var heading string
	var inputs []*html.Node
	var walk func(n *html.Node, label *html.Node)
	walk = func(n *html.Node, label *html.Node) {
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			if k.Type != html.ElementNode {
				continue
			}
			switch k.Data {
			case "label":
				if f, ok := getAttr(k, "for"); ok {
					labels["#"+f] = labelText(k)
				}
				walk(k, k)
				continue
			case "input", "select", "textarea":
				inputs = append(inputs, k)
				if label != nil {
					labels["@"+fmt.Sprintf("%p", k)] = labelText(label)
				}
				continue
			case "h1", "h2", "h3", "h4", "h5", "h6", "legend":
				if heading == "" {
					heading = strings.Join(strings.Fields(textOf(k)), " ")
				}
			}
			walk(k, label)
		}
	}
	walk(form, nil)
	if fs.Title == "" {
		fs.Title = heading
	}
	required := toSet(b.Required)
	seen := map[string]int{} // input name → index in fs.Fields + 1
	for _, in := range inputs {
		name, _ := getAttr(in, "name")
		typ, _ := getAttr(in, "type")
		typ = strings.ToLower(typ)
		if i := seen[name]; i > 0 && in.Data == "input" && (typ == "radio" || typ == "checkbox") && fs.Fields[i-1].Type == typ {
			// Another choice of a radio group (or checkboxes sharing a name): one more value.
			if v := choiceValue(in); !slices.Contains(fs.Fields[i-1].Options, v) {
				fs.Fields[i-1].Options = append(fs.Fields[i-1].Options, v)
			}
			if hasAttr(in, "required") {
				fs.Fields[i-1].Required = true
			}
			continue
		}
		if name == "" || seen[name] > 0 || name == FormHoneypot || name == FormTiming || name == FormNonce || strings.HasPrefix(name, "wb_") ||
			name == "cf-turnstile-response" || name == "g-recaptcha-response" || !formInputRE.MatchString(name) {
			continue
		}
		if in.Data == "input" {
			switch typ {
			case "submit", "button", "reset", "image", "file", "hidden", "password":
				continue
			}
		}
		target := b.Fields[name]
		if target == "-" {
			continue
		}
		if target == "" {
			target = defaultFormField(name)
		}
		if !FormFieldOK(target) {
			continue
		}
		f := FormField{Name: target, Input: name, Type: "text", Max: FormNameMax}
		switch {
		case in.Data == "textarea" || target == "message":
			f.Type, f.Max = "textarea", FormMessageMax
		case in.Data == "select":
			f.Type, f.Max = "select", FormOtherMax
			for o := in.FirstChild; o != nil; o = o.NextSibling {
				if o.Type == html.ElementNode && o.Data == "option" {
					if v, ok := getAttr(o, "value"); ok && v != "" {
						f.Options = append(f.Options, v)
					}
				}
			}
		case typ == "email" || target == "email":
			f.Type, f.Max = "email", FormEmailMax
		case typ == "tel" || target == "phone":
			f.Type, f.Max = "tel", FormPhoneMax
		case typ == "checkbox" || typ == "radio":
			// The values the markup declares, like a select's options (the other inputs of the
			// group add theirs above).
			f.Type, f.Max, f.Options = typ, FormOtherMax, []string{choiceValue(in)}
		}
		if id, ok := getAttr(in, "id"); ok && labels["#"+id] != "" {
			f.Label = labels["#"+id]
		} else if l := labels["@"+fmt.Sprintf("%p", in)]; l != "" {
			f.Label = l
		} else if p, ok := getAttr(in, "placeholder"); ok && strings.TrimSpace(p) != "" {
			f.Label = strings.TrimSpace(p)
		} else {
			f.Label = name
		}
		f.Label = truncRunes(f.Label, 120)
		if hasAttr(in, "required") || required[name] {
			f.Required = true
		}
		if v, ok := getAttr(in, "aria-required"); ok && v == "true" {
			f.Required = true
		}
		fs.Fields = append(fs.Fields, f)
		seen[name] = len(fs.Fields)
		if len(fs.Fields) >= MaxCodeFormFields {
			break
		}
	}
	return fs
}

// choiceValue is the value a checked radio or checkbox submits ("on" without a value attribute).
func choiceValue(in *html.Node) string {
	if v, ok := getAttr(in, "value"); ok {
		return strings.TrimSpace(v)
	}
	return "on"
}

// defaultFormField maps an input name to a Pursae field: itself when it is one, else field_<name>.
func defaultFormField(name string) string {
	low := strings.ToLower(name)
	for _, f := range FormFields {
		if f == low {
			return f
		}
	}
	var b strings.Builder
	for _, r := range low {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_':
			b.WriteRune(r)
		case r == '-' || r == '.' || r == '[' || r == ']':
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), "_")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return ""
	}
	return "field_" + s
}

func labelText(n *html.Node) string {
	var b strings.Builder
	var walk func(x *html.Node)
	walk = func(x *html.Node) {
		for k := x.FirstChild; k != nil; k = k.NextSibling {
			switch k.Type {
			case html.TextNode:
				b.WriteString(k.Data)
			case html.ElementNode:
				switch k.Data {
				case "input", "select", "textarea", "svg", "script", "style":
					continue
				}
				walk(k)
			}
		}
	}
	walk(n)
	t := strings.Join(strings.Fields(b.String()), " ")
	return strings.TrimSpace(strings.TrimRight(t, "*: "))
}

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// FindCodeElement finds an element of a code section's markup by its data-wbc id (wbc) or, when
// that is empty, by its id attribute; it answers the element's data-wbc id.
func FindCodeElement(src, wbc, htmlID string) (string, bool) {
	root := parseSource(src)
	var found string
	var walk func(n *srcNode)
	walk = func(n *srcNode) {
		for _, k := range n.kids {
			if found != "" || k.tag == "" {
				continue
			}
			id := k.wbc()
			if id != "" && ((wbc != "" && id == wbc) || (wbc == "" && htmlID != "" && func() bool { v, _ := k.attr("id"); return v == htmlID }())) {
				found = id
				return
			}
			walk(k)
		}
	}
	walk(root)
	return found, found != ""
}

// CodeLoops lists the loop bindings of a doc's code sections by BindKey.
func CodeLoops(d *DocState) map[string]LoopSpec {
	out := map[string]LoopSpec{}
	for i := range d.Sections {
		s := &d.Sections[i]
		if s.Kind != KindCode || s.Content[ContentBinds] == "" {
			continue
		}
		for id, b := range ParseCodeBinds(s.Content[ContentBinds]) {
			if b.Kind == BindLoop {
				out[BindKey(s.ID, id)] = b.CodeLoopSpec()
			}
		}
	}
	return out
}
