package webbuilder

import (
	"fmt"
	"html"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Binding an imported site to content (docs/developer/web-builder-code-sections.md, "Binding to
// content"): after ConvertSite, BindSite finds what of a static design is really content the site
// should manage, and binds it (codebind.go) only where the bound markup reproduces the design byte
// for byte (every result is rendered with the planned content and compared with the design):
//
//   - Menu: the header's navigation list becomes a Pursae menu (one item per list item, each item
//     keeping its own markup as its template; a dropdown list inside an item gives sub-items).
//   - Groups: the pages of a folder that share one skeleton (hizmet/*.html) become one Theme Builder
//     single template; each page keeps its URL (it stays a page under the folder's parent page)
//     and holds its own content: its title (the varying <h1>), its content (the largest varying
//     region) and custom fields for the other varying texts, links and images. Elements that
//     differ only in their classes and state attributes (the page's own link in a side menu) are
//     marked by an active-link rule, as in headers. Pages whose skeletons split into a few
//     variants (two categories with their own side menus) make one template per variant.
//   - Listings: a list on another page whose items link to the pages of such a folder (a news
//     page's cards) becomes a loop over those pages (ordered by menu_order, which the import sets
//     from the list), its cards' varying parts bound to the pages' title, link, excerpt, image and
//     custom fields.

// SiteBindOptions selects what BindSite binds.
type SiteBindOptions struct {
	// Menus: bind the header's navigation to a menu (location MenuLocation).
	Menus        bool
	MenuLocation string
	// Groups: the folders whose pages may become one single template ("*": every folder).
	Groups map[string]bool
	// FolderParents: the id of each folder's parent page ("hizmet" → "41"), for listing queries.
	FolderParents map[string]string
	// PostSets: the sets of leaf pages (SiteLeafSets: "*", "*:single-post") whose pages become
	// posts — the caller's choice, when the site's post permalinks (/%postname%/) give them their
	// design URLs; their template is the posts' single template and their lists loop over posts.
	PostSets map[string]bool
}

// SiteMenuItem is one planned menu item (Tpls: the data-wbc of its template in each bound list).
type SiteMenuItem struct {
	Label    string         `json:"label"`
	URL      string         `json:"url"`
	Target   string         `json:"target,omitempty"`
	Tpls     []string       `json:"-"`
	Children []SiteMenuItem `json:"children,omitempty"`
}

// SiteMenu is the planned header menu: the lists of header template 0's section Section that it
// fills (data-wbc), their submenu lists, and the items.
type SiteMenu struct {
	Section int            `json:"section"`
	Lists   []string       `json:"lists"`
	Subs    []string       `json:"subs,omitempty"`
	Items   []SiteMenuItem `json:"items"`
}

// Binds is the header section's content.binds once the items have ids (ids[i] for the i-th item
// in depth-first order: an item, then its children).
func (m *SiteMenu) Binds(existing string, location string, ids []int64) string {
	binds := ParseCodeBinds(existing)
	if binds == nil {
		binds = CodeBinds{}
	}
	for li, list := range m.Lists {
		b := &CodeBind{Kind: BindMenu, Location: location, Items: map[string]string{}}
		k := 0
		var walk func(items []SiteMenuItem)
		walk = func(items []SiteMenuItem) {
			for _, it := range items {
				if k < len(ids) && li < len(it.Tpls) && it.Tpls[li] != "" {
					b.Items[strconv.FormatInt(ids[k], 10)] = it.Tpls[li]
				}
				k++
				walk(it.Children)
			}
		}
		walk(m.Items)
		binds[list] = b
	}
	for _, s := range m.Subs {
		binds[s] = &CodeBind{Kind: BindSubmenu}
	}
	return binds.String()
}

// SiteGroupPost is the content of one page of a group.
type SiteGroupPost struct {
	Page    int               `json:"page"` // index into SiteImport.Pages
	Title   string            `json:"title"`
	Content string            `json:"-"`
	Fields  map[string]string `json:"-"`
}

// SiteGroup is a single template made from a folder's pages.
type SiteGroup struct {
	Folder   string          `json:"folder"`
	Sections []CodePart      `json:"-"` // the template's sections, content.binds set
	Head     string          `json:"-"`
	Lang     string          `json:"-"`
	Attrs    [2]string       `json:"-"`
	Rule     *ActiveRule     `json:"-"`
	Posts    []SiteGroupPost `json:"posts"`
	// Fields lists the custom fields the template reads (beyond title and content).
	Fields []string `json:"fields,omitempty"`
	// AsPosts: the pages become posts (a set of SiteBindOptions.PostSets).
	AsPosts bool `json:"asPosts,omitempty"`
}

// SiteListing is a list bound as a loop over a folder's pages.
type SiteListing struct {
	Page    int    `json:"page"`    // index into SiteImport.Pages (the list's page)
	Section int    `json:"section"` // its section
	Folder  string `json:"folder"`
	// Members: the folder's pages (indexes into Pages) in the list's order.
	Members []int `json:"members"`
	Offset  int   `json:"offset,omitempty"`
}

// SitePagePlan is what the import sets on a page that a group or listing binds: its title (the
// page's <h1>), excerpt, menu_order and custom fields.
type SitePagePlan struct {
	Title     string
	Excerpt   string
	MenuOrder int
	HasOrder  bool
	Fields    map[string]string
}

// SiteBinding is what BindSite found.
type SiteBinding struct {
	Menu     *SiteMenu             `json:"menu,omitempty"`
	Groups   []SiteGroup           `json:"groups,omitempty"`
	Listings []SiteListing         `json:"listings,omitempty"`
	Plans    map[int]*SitePagePlan `json:"-"` // by page index
	// Candidates: folders whose pages could be a group (for the admin's options).
	Candidates []string `json:"candidates,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

// GroupMember reports whether page i is part of a group (it becomes content under a template).
func (b *SiteBinding) GroupMember(i int) bool {
	for _, g := range b.Groups {
		for _, p := range g.Posts {
			if p.Page == i {
				return true
			}
		}
	}
	return false
}

// SiteFolders lists the folders of a converted site's pages that hold at least two pages (the
// folder's own index page aside): the candidates for groups and listings.
func SiteFolders(res *SiteImport) map[string][]int {
	out := map[string][]int{}
	for i, p := range res.Pages {
		dir := path.Dir(p.Path)
		base := strings.TrimSuffix(path.Base(p.Path), path.Ext(p.Path))
		if dir == "." || strings.EqualFold(base, "index") || p.URL == "" {
			continue
		}
		out[dir] = append(out[dir], i)
	}
	for k, v := range out {
		if len(v) < 2 {
			delete(out, k)
		}
	}
	return out
}

// SiteLeafSets lists the pages saved with pretty permalinks (a WordPress copy: every post is
// <slug>/index.html, alone in its folder, beside its siblings): the leaf pages of each parent
// folder, keyed "*" (the site's top) or "<parent>/*", split by kind when their <body> classes
// (without the ones holding digits: postid-1699) differ — "*:single-post" names a kind by a class
// its pages share and the others lack. Sets of two pages or more are candidates for groups and
// listings like folders (BindSite).
func SiteLeafSets(res *SiteImport) map[string][]int {
	dirs := map[string]int{} // folder → pages in it or below
	for _, p := range res.Pages {
		for d := path.Dir(p.Path); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d]++
		}
	}
	type leaf struct {
		i   int
		sig string
		cls map[string]bool
	}
	byParent := map[string][]leaf{}
	for i, p := range res.Pages {
		dir, base := path.Dir(p.Path), path.Base(p.Path)
		if dir == "." || p.URL == "" || p.Code == nil || !strings.EqualFold(strings.TrimSuffix(base, path.Ext(base)), "index") || dirs[dir] != 1 {
			continue
		}
		parent := path.Dir(dir)
		if parent == "." {
			parent = ""
		}
		cls := map[string]bool{}
		var sig []string
		for _, c := range strings.Fields(attrValue(p.Code.BodyAttrs, "class")) {
			if strings.IndexAny(c, "0123456789") < 0 {
				cls[c] = true
				sig = append(sig, c)
			}
		}
		sort.Strings(sig)
		byParent[parent] = append(byParent[parent], leaf{i, strings.Join(sig, " "), cls})
	}
	out := map[string][]int{}
	for parent, leaves := range byParent {
		key := "*"
		if parent != "" {
			key = parent + "/*"
		}
		kinds := map[string][]leaf{}
		var order []string
		for _, l := range leaves {
			if _, ok := kinds[l.sig]; !ok {
				order = append(order, l.sig)
			}
			kinds[l.sig] = append(kinds[l.sig], l)
		}
		for n, sig := range order {
			ls := kinds[sig]
			if len(ls) < 2 {
				continue
			}
			name := key
			if len(order) > 1 {
				name = key + ":" + strconv.Itoa(n+1)
				// A class all of this kind's pages have and no other leaf has.
				var cands []string
				for c := range ls[0].cls {
					cands = append(cands, c)
				}
				sort.Sort(sort.Reverse(sort.StringSlice(cands))) // single-post before post-template-default
				for _, c := range cands {
					mine, others := true, false
					for _, l := range leaves {
						if l.sig == sig {
							mine = mine && l.cls[c]
						} else if l.cls[c] {
							others = true
						}
					}
					if mine && !others && siteSlug(c) == c {
						name = key + ":" + c
						break
					}
				}
			}
			for _, l := range ls {
				out[name] = append(out[name], l.i)
			}
		}
	}
	return out
}

// SiteLeafParent is the parent folder of a leaf set ("" for the site's top); ok is false for a
// folder's name.
func SiteLeafParent(key string) (string, bool) {
	k, _, _ := strings.Cut(key, ":")
	if k == "*" {
		return "", true
	}
	if strings.HasSuffix(k, "/*") {
		return strings.TrimSuffix(k, "/*"), true
	}
	return "", false
}

// attrValue reads one attribute from an attribute string (`class="a b" data-x="1"`).
func attrValue(attrs, name string) string {
	nodes, err := parseFragment("<div " + attrs + "></div>")
	if err != nil || len(nodes) == 0 {
		return ""
	}
	return attr(nodes[0], name)
}

// BindSite plans the bindings (see the file comment). It changes res: listing pages' sections and
// header template 0 get content.binds (the menu's ids are the caller's: SiteMenu.Binds).
func BindSite(res *SiteImport, o SiteBindOptions) *SiteBinding {
	b := &SiteBinding{Plans: map[int]*SitePagePlan{}}
	if o.MenuLocation == "" {
		o.MenuLocation = MenuLocationPrimary
	}
	if o.Menus && len(res.Headers) > 0 {
		b.Menu = planMenu(&res.Headers[0], &b.Notes)
	}
	folders := SiteFolders(res)
	for k, v := range SiteLeafSets(res) {
		folders[k] = v
	}
	var names []string
	for f := range folders {
		names = append(names, f)
	}
	sort.Strings(names)
	b.Candidates = names
	for _, f := range names {
		if !o.Groups["*"] && !o.Groups[f] {
			continue
		}
		members := folders[f]
		gs, notes := planGroups(res, f, members)
		b.Notes = append(b.Notes, notes...)
		for _, g := range gs {
			g.AsPosts = o.PostSets[f]
			b.Groups = append(b.Groups, g)
			for _, p := range g.Posts {
				pl := b.plan(p.Page)
				pl.Title = p.Title
				for k, v := range p.Fields {
					pl.Fields[k] = v
				}
			}
		}
		b.planListings(res, f, members, o.FolderParents[f], o.PostSets[f])
	}
	return b
}

func (b *SiteBinding) plan(i int) *SitePagePlan {
	if p, ok := b.Plans[i]; ok {
		return p
	}
	p := &SitePagePlan{Fields: map[string]string{}}
	b.Plans[i] = p
	return p
}

// ---------------------------------------------------------------------------------------------
// Rendering a plan (the check that a binding reproduces the design).

type planPost struct {
	Title, URL, Content, Excerpt string
	Fields                       map[string]string
}

type planMenuItem struct {
	ID, Label, URL, Target string
	Children               []*planMenuItem
}

type planRes struct {
	post   *planPost
	posts  []*planPost
	menu   []*planMenuItem
	item   *planMenuItem
	parent *planRes
}

func (r *planRes) nearestPost() *planPost {
	for x := r; x != nil; x = x.parent {
		if x.post != nil {
			return x.post
		}
	}
	return nil
}

func (r *planRes) Text(slot SlotName, a SlotArgs) (string, bool) {
	if slot == SlotMenuLabel {
		if r.item != nil {
			return r.item.Label, r.item.Label != ""
		}
		return "", false
	}
	p := r.nearestPost()
	if p == nil {
		return "", false
	}
	switch slot {
	case SlotPostTitleText:
		return p.Title, p.Title != ""
	case SlotPostField:
		v := p.Fields[a.Field]
		return v, v != ""
	}
	return "", false
}

func (r *planRes) HTML(slot SlotName, a SlotArgs) (string, bool) {
	p := r.nearestPost()
	if p == nil {
		return "", false
	}
	switch slot {
	case SlotPostContent:
		return p.Content, p.Content != ""
	case SlotPostExcerpt:
		v := html.EscapeString(p.Excerpt)
		return v, v != ""
	case SlotPostFieldHTML:
		v := p.Fields[TrustedHTMLField+a.Field]
		if v == "" {
			v = p.Fields[a.Field]
		}
		return v, v != ""
	}
	return "", false
}

func (r *planRes) Attrs(slot SlotName, a SlotArgs) (map[string]string, bool) {
	if slot == SlotMenuLink {
		if r.item == nil {
			return nil, false
		}
		m := map[string]string{"href": r.item.URL}
		if r.item.Target != "" {
			m["target"] = r.item.Target
		}
		return m, true
	}
	p := r.nearestPost()
	if p == nil {
		return nil, false
	}
	switch slot {
	case SlotPostURL:
		return map[string]string{"href": p.URL}, true
	case SlotPostImage:
		if v := p.Fields[a.Field]; v != "" {
			m := map[string]string{"src": v}
			if alt, ok := p.Fields[a.Field+"_alt"]; ok {
				m["alt"] = alt
			}
			return m, true
		}
	case SlotPostFieldAttrs:
		m := map[string]string{}
		for _, k := range SlotAttrs {
			if v := p.Fields[a.Field+"_"+strings.ReplaceAll(k, "-", "_")]; v != "" {
				m[k] = v
			}
		}
		return m, len(m) > 0
	}
	return nil, false
}

func (r *planRes) Loop(string, SlotArgs) (LoopIter, bool) {
	items := make([]Resolver, 0, len(r.posts))
	for _, p := range r.posts {
		items = append(items, &planRes{post: p, parent: r})
	}
	return &SliceIter{Items: items}, true
}

func (r *planRes) Menu(string, SlotArgs) (LoopIter, bool) {
	items := make([]Resolver, 0, len(r.menu))
	for _, it := range r.menu {
		items = append(items, &planRes{item: it, parent: r})
	}
	return &SliceIter{Items: items}, true
}

func (r *planRes) ItemKey() string {
	if r.item == nil {
		return ""
	}
	return "k:" + r.item.ID
}

func (r *planRes) HasChildren() bool { return r.item != nil && len(r.item.Children) > 0 }

func (r *planRes) SubMenu() (LoopIter, bool) {
	if r.item == nil {
		return nil, false
	}
	items := make([]Resolver, 0, len(r.item.Children))
	for _, it := range r.item.Children {
		items = append(items, &planRes{item: it, parent: r})
	}
	return &SliceIter{Items: items}, true
}

// renderPlan compiles a part's bindings and fills them from r.
func renderPlan(src, binds string, r Resolver) (string, bool) {
	compiled := CompileBinds(src, "s", ParseCodeBinds(binds), CodeFillOptions{})
	p, err := Compile(compiled)
	if err != nil {
		return "", false
	}
	out, err := p.ExecString(r)
	return out, err == nil
}

// normMarkup is markup as the comparison sees it: parsed and rendered again, without the editor's
// ids and the binding attributes, text white space collapsed (outside pre and textarea).
func normMarkup(src string) string {
	nodes, err := parseFragment(src)
	if err != nil {
		return src
	}
	var walk func(n *xhtml.Node, pre bool)
	walk = func(n *xhtml.Node, pre bool) {
		switch n.Type {
		case xhtml.ElementNode:
			attrs := n.Attr[:0]
			for _, a := range n.Attr {
				if a.Namespace == "" && (a.Key == AttrCodeID || strings.HasPrefix(a.Key, "data-wb-")) {
					continue
				}
				if a.Namespace == "" && a.Key == "style" {
					// A staggered entrance's delay per item (style="animation-delay:50ms") is the
					// only difference a repeated template keeps: the item ends where the design's does.
					a.Val = strings.TrimSpace(delayRE.ReplaceAllString(a.Val, ""))
				}
				attrs = append(attrs, a)
			}
			n.Attr = attrs
			if n.DataAtom == atom.Pre || n.DataAtom == atom.Textarea || n.DataAtom == atom.Script || n.DataAtom == atom.Style {
				pre = true
			}
		case xhtml.TextNode:
			if !pre {
				n.Data = wsRunRE.ReplaceAllString(n.Data, " ")
			}
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			walk(k, pre)
		}
	}
	for _, n := range nodes {
		walk(n, false)
	}
	out, _ := renderNodes(nodes)
	return out
}

var wsRunRE = regexp.MustCompile(`[ \t\r\n\f]+`)

// bindDebug, when set (tests), is told where a planned binding did not reproduce the design.
var bindDebug func(what, got, want string)

// sameMarkup compares a rendered plan with the design's markup (normMarkup).
func sameMarkup(what, got, want string) bool {
	g, w := normMarkup(got), normMarkup(want)
	if g == w {
		return true
	}
	if bindDebug != nil {
		bindDebug(what, g, w)
	}
	return false
}

// ---------------------------------------------------------------------------------------------
// Menu.

// planMenu finds the header's navigation lists and plans a menu that reproduces them.
func planMenu(t *SiteTemplate, notes *[]string) *SiteMenu {
	for si, sec := range t.Sections {
		nodes, err := parseFragment(sec.HTML)
		if err != nil {
			continue
		}
		var lists []*xhtml.Node
		var walk func(n *xhtml.Node, inNav bool)
		walk = func(n *xhtml.Node, inNav bool) {
			if n.Type != xhtml.ElementNode || n.Namespace != "" {
				return
			}
			if n.DataAtom == atom.Nav || attr(n, "role") == "navigation" {
				inNav = true
			}
			if inNav && (n.DataAtom == atom.Ul || n.DataAtom == atom.Ol) && attr(n, AttrCodeID) != "" {
				lists = append(lists, n)
				return
			}
			for k := n.FirstChild; k != nil; k = k.NextSibling {
				walk(k, inNav)
			}
		}
		for _, n := range nodes {
			walk(n, false)
		}
		var m *SiteMenu
		for _, l := range lists {
			items, subs, ok := menuItemsOf(l, 0)
			if !ok || len(items) < 2 {
				continue
			}
			if m == nil {
				m = &SiteMenu{Section: si, Items: items}
				m.Lists = []string{attr(l, AttrCodeID)}
				m.Subs = subs
				if !menuReproduces(sec.HTML, m) {
					*notes = append(*notes, "header navigation: the bound menu would not reproduce the design's list; kept as markup")
					m = nil
				}
				continue
			}
			// Another list of the same items (a mobile menu): bound to the same menu when it
			// reproduces too.
			if !sameMenu(m.Items, items) {
				continue
			}
			trial := *m
			trial.Lists = append(append([]string{}, m.Lists...), attr(l, AttrCodeID))
			trial.Subs = append(append([]string{}, m.Subs...), subs...)
			trial.Items = mergeTpls(m.Items, items)
			if menuReproduces(sec.HTML, &trial) {
				*m = trial
			}
		}
		if m != nil {
			return m
		}
	}
	return nil
}

// menuItemsOf reads the items of a list: each element child is an item with a label (and a link,
// else a button: URL "#"); a list inside an item whose children are links gives its sub-items.
func menuItemsOf(list *xhtml.Node, depth int) ([]SiteMenuItem, []string, bool) {
	var items []SiteMenuItem
	var subs []string
	for k := list.FirstChild; k != nil; k = k.NextSibling {
		if k.Type != xhtml.ElementNode {
			if k.Type == xhtml.TextNode && strings.TrimSpace(k.Data) != "" {
				return nil, nil, false
			}
			continue
		}
		id := attr(k, AttrCodeID)
		if id == "" {
			return nil, nil, false
		}
		var sub *xhtml.Node
		if depth == 0 {
			sub = subListOf(k)
		}
		link, label := menuLinkLabel(k, sub)
		if label == "" {
			return nil, nil, false
		}
		it := SiteMenuItem{Label: label, URL: "#", Tpls: []string{id}}
		if link != nil {
			it.URL = attr(link, "href")
			it.Target = attr(link, "target")
			if it.URL == "" {
				return nil, nil, false
			}
		}
		if sub != nil {
			children, _, ok := menuItemsOf(sub, depth+1)
			if !ok || len(children) == 0 {
				return nil, nil, false
			}
			it.Children = children
			subs = append(subs, attr(sub, AttrCodeID))
		}
		items = append(items, it)
	}
	return items, subs, len(items) > 0
}

// subListOf finds a dropdown list inside an item: the first ul/ol below it whose items hold links.
func subListOf(item *xhtml.Node) *xhtml.Node {
	var found *xhtml.Node
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for k := n.FirstChild; k != nil && found == nil; k = k.NextSibling {
			if k.Type != xhtml.ElementNode || k.Namespace != "" {
				continue
			}
			if (k.DataAtom == atom.Ul || k.DataAtom == atom.Ol) && attr(k, AttrCodeID) != "" && firstLink(k) != nil {
				found = k
				return
			}
			walk(k)
		}
	}
	walk(item)
	return found
}

func firstLink(n *xhtml.Node) *xhtml.Node {
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type != xhtml.ElementNode || k.Namespace != "" {
			continue
		}
		if k.DataAtom == atom.A {
			return k
		}
		if f := firstLink(k); f != nil {
			return f
		}
	}
	return nil
}

// menuLinkLabel mirrors bindCompiler.menuItem: the item's first link (outside sub) and the text of
// its label element.
func menuLinkLabel(item, sub *xhtml.Node) (*xhtml.Node, string) {
	var link, firstText *xhtml.Node
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == sub || n.Namespace != "" || n.DataAtom == atom.Script || n.DataAtom == atom.Style || n.DataAtom == atom.Template {
			return
		}
		if link == nil && n.DataAtom == atom.A {
			link = n
		}
		if firstText == nil && firstOwnText(n) != "" {
			firstText = n
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			if k.Type == xhtml.ElementNode {
				walk(k)
			}
		}
	}
	walk(item)
	label := firstText
	if link != nil {
		if firstOwnText(link) != "" {
			label = link
		} else {
			var inner func(n *xhtml.Node) *xhtml.Node
			inner = func(n *xhtml.Node) *xhtml.Node {
				for k := n.FirstChild; k != nil; k = k.NextSibling {
					if k.Type != xhtml.ElementNode || k.Namespace != "" {
						continue
					}
					if firstOwnText(k) != "" {
						return k
					}
					if f := inner(k); f != nil {
						return f
					}
				}
				return nil
			}
			if l := inner(link); l != nil {
				label = l
			}
		}
	}
	if label == nil {
		return link, ""
	}
	return link, firstOwnText(label)
}

// ownText is the element's first non-blank direct text, trimmed.
func firstOwnText(n *xhtml.Node) string {
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == xhtml.TextNode && strings.TrimSpace(k.Data) != "" {
			return strings.TrimSpace(k.Data)
		}
	}
	return ""
}

func sameMenu(a, b []SiteMenuItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Label != b[i].Label || ActivePath(a[i].URL) != ActivePath(b[i].URL) || !sameMenu(a[i].Children, b[i].Children) {
			return false
		}
	}
	return true
}

func mergeTpls(a, b []SiteMenuItem) []SiteMenuItem {
	out := make([]SiteMenuItem, len(a))
	for i := range a {
		out[i] = a[i]
		out[i].Tpls = append(append([]string{}, a[i].Tpls...), b[i].Tpls...)
		out[i].Children = mergeTpls(a[i].Children, b[i].Children)
	}
	return out
}

// menuReproduces renders the section with the planned menu and compares it with the design.
func menuReproduces(src string, m *SiteMenu) bool {
	var ids []int64
	var plan func(items []SiteMenuItem) []*planMenuItem
	n := int64(0)
	plan = func(items []SiteMenuItem) []*planMenuItem {
		var out []*planMenuItem
		for _, it := range items {
			n++
			ids = append(ids, n)
			p := &planMenuItem{ID: strconv.FormatInt(n, 10), Label: it.Label, URL: it.URL, Target: it.Target}
			p.Children = plan(it.Children)
			out = append(out, p)
		}
		return out
	}
	items := plan(m.Items)
	out, ok := renderPlan(src, m.Binds("", "primary", ids), &planRes{menu: items})
	return ok && sameMarkup("menu", out, src)
}

// ---------------------------------------------------------------------------------------------
// Groups: one skeleton, holes where the pages differ.

// genHole is one place where the aligned pages differ: an element's own text, its whole content,
// or one of its attributes. nodes and vals are per page.
type genHole struct {
	kind  string // text | html | attr
	attr  string
	nodes []*xhtml.Node
	vals  []string
}

// originalIDAttr: an attribute where a saved site keeps its own record numbers (id, class,
// data-*), not the editor's.
func originalIDAttr(k string) bool {
	return k == "id" || k == "class" || strings.HasPrefix(k, "data-") && k != AttrCodeID && !strings.HasPrefix(k, "data-wb-")
}

var digitRunRE = regexp.MustCompile(`[0-9]{2,}`)

func digitsNormed(vals []string) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = digitRunRE.ReplaceAllString(v, "#")
	}
	return out
}

func setAttr(n *xhtml.Node, k, v string) {
	for i := range n.Attr {
		if n.Attr[i].Namespace == "" && n.Attr[i].Key == k {
			n.Attr[i].Val = v
		}
	}
}

// looseIDMarkup is markup with the numbers in its id, class and data-* values replaced and its
// inline scripts' code left out (the group check's view of a page whose template shows the first
// page's record numbers and per-page script values).
func looseIDMarkup(src string) string {
	nodes, err := parseFragment(src)
	if err != nil {
		return src
	}
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			for i, a := range n.Attr {
				if a.Namespace == "" && (originalIDAttr(a.Key) || a.Key == "value" && strings.EqualFold(attr(n, "type"), "hidden")) {
					n.Attr[i].Val = digitRunRE.ReplaceAllString(a.Val, "#")
				}
			}
			if n.DataAtom == atom.Script || n.DataAtom == atom.Noscript {
				for n.FirstChild != nil {
					n.RemoveChild(n.FirstChild)
				}
			}
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	out, _ := renderNodes(nodes)
	return out
}

type generalizer struct {
	// looseIDs: the original site's record numbers in id, class and data-* may differ (a group).
	looseIDs bool
	holes    []*genHole
}

var bindableAttrs = toSet([]string{"href", "src", "srcset", "sizes", "width", "height", "alt", "datetime", "target"})

var delayRE = regexp.MustCompile(`(?i)(animation|transition)-delay\s*:\s*[^;]*;?`)

func attrMap(n *xhtml.Node) (map[string]string, []string) {
	m := map[string]string{}
	var keys []string
	for _, a := range n.Attr {
		k := a.Key
		if a.Namespace != "" {
			k = a.Namespace + ":" + k
		}
		if k == AttrCodeID {
			continue
		}
		if _, dup := m[k]; !dup {
			keys = append(keys, k)
		}
		m[k] = a.Val
	}
	return m, keys
}

// kidsOf lists a node's children that matter for alignment (comments aside).
func kidsOf(n *xhtml.Node) []*xhtml.Node {
	var out []*xhtml.Node
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == xhtml.ElementNode || k.Type == xhtml.TextNode {
			out = append(out, k)
		}
	}
	return out
}

func allSame(vals []string) bool {
	for _, v := range vals[1:] {
		if v != vals[0] {
			return false
		}
	}
	return true
}

// holeable: an element whose content can be a hole (an editor id, HTML, not raw text or SVG).
func holeable(n *xhtml.Node) bool {
	return n.Type == xhtml.ElementNode && n.Namespace == "" && attr(n, AttrCodeID) != "" && !codeOpaque[n.DataAtom]
}

// gen aligns one element per page; false when they cannot be one element of a template.
func (g *generalizer) gen(ns []*xhtml.Node) bool {
	e0 := ns[0]
	for _, e := range ns[1:] {
		if e.Type != e0.Type || e.Data != e0.Data || e.Namespace != e0.Namespace {
			return false
		}
	}
	if e0.Type != xhtml.ElementNode {
		return true // text is compared by the parent
	}
	maps := make([]map[string]string, len(ns))
	var keys []string
	// The class and the state attributes may come and go (the current page's mark: the active-link
	// rule's); every other attribute must be on every page.
	counted := func(m map[string]string) int {
		c := 0
		for k := range m {
			if k != "class" && !activeStateAttrs[k] {
				c++
			}
		}
		return c
	}
	for i, e := range ns {
		m, ks := attrMap(e)
		maps[i] = m
		if i == 0 {
			keys = ks
		} else if counted(m) != counted(maps[0]) {
			return false
		}
	}
	var local []*genHole
	for _, k := range keys {
		vals := make([]string, len(ns))
		for i := range ns {
			v, ok := maps[i][k]
			if !ok && k != "class" && !activeStateAttrs[k] {
				return false
			}
			vals[i] = v
		}
		if allSame(vals) {
			continue
		}
		if g.looseIDs && (originalIDAttr(k) || k == "value" && strings.EqualFold(attr(e0, "type"), "hidden")) && allSame(digitsNormed(vals)) {
			// The original site's numbers (WordPress's id="post-1699", class "postid-1699"): each
			// page shows the first page's; nothing on this site refers to them.
			for i := range ns {
				setAttr(ns[i], k, vals[0])
			}
			continue
		}
		switch {
		case k == "class" || activeStateAttrs[k]:
			// the active-link rule's (ClusterParts)
		case k == "style" && styleDelaysOnly(vals):
		case bindableAttrs[k] && holeable(e0):
			local = append(local, &genHole{kind: "attr", attr: k, nodes: ns, vals: vals})
		default:
			return false
		}
	}
	if codeOpaque[e0.DataAtom] || e0.Namespace != "" {
		// SVG, scripts, selects, …: their insides must be the same
		r0 := renderInner(e0)
		for _, e := range ns[1:] {
			if renderInner(e) != r0 {
				if g.looseIDs && (e0.DataAtom == atom.Script || e0.DataAtom == atom.Noscript) {
					// An inline script the original site printed per page (its post id, a
					// nonce): every page shows the first page's.
					for e.FirstChild != nil {
						e.RemoveChild(e.FirstChild)
					}
					for k := e0.FirstChild; k != nil; k = k.NextSibling {
						e.AppendChild(cloneNode(k))
					}
					continue
				}
				return false
			}
		}
		g.holes = append(g.holes, local...)
		return true
	}
	kids := make([][]*xhtml.Node, len(ns))
	aligned := true
	for i, e := range ns {
		kids[i] = kidsOf(e)
		if len(kids[i]) != len(kids[0]) {
			aligned = false
		}
	}
	if aligned {
		for j := range kids[0] {
			for i := range ns {
				a, b := kids[0][j], kids[i][j]
				if a.Type != b.Type || (a.Type == xhtml.ElementNode && (a.Data != b.Data || a.Namespace != b.Namespace)) {
					aligned = false
				}
			}
		}
	}
	mark := len(g.holes)
	asHTML := func() bool {
		g.holes = g.holes[:mark]
		if !holeable(e0) {
			return false
		}
		vals := make([]string, len(ns))
		for i, e := range ns {
			vals[i] = StripCodeIDs(renderInner(e))
		}
		g.holes = append(g.holes, local...)
		g.holes = append(g.holes, &genHole{kind: "html", nodes: ns, vals: vals})
		return true
	}
	if !aligned {
		return asHTML()
	}
	textHole := false
	for j := range kids[0] {
		col := make([]*xhtml.Node, len(ns))
		for i := range ns {
			col[i] = kids[i][j]
		}
		if col[0].Type == xhtml.ElementNode {
			if !g.gen(col) {
				return asHTML()
			}
			continue
		}
		vals := make([]string, len(ns))
		for i, t := range col {
			vals[i] = t.Data
		}
		if allSame(vals) {
			continue
		}
		// A varying text: the element's own text when it is its only text that is not blank.
		if textHole || !holeable(e0) {
			return asHTML()
		}
		for i := range ns {
			if strings.TrimSpace(vals[i]) == "" || countOwnText(ns[i]) != 1 {
				return asHTML()
			}
			vals[i] = strings.TrimSpace(vals[i])
		}
		textHole = true
		g.holes = append(g.holes, &genHole{kind: "text", nodes: ns, vals: vals})
	}
	g.holes = append(g.holes, local...)
	return true
}

func countOwnText(n *xhtml.Node) int {
	c := 0
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == xhtml.TextNode && strings.TrimSpace(k.Data) != "" {
			c++
		}
	}
	return c
}

func styleDelaysOnly(vals []string) bool {
	first := strings.TrimSpace(delayRE.ReplaceAllString(vals[0], ""))
	for _, v := range vals[1:] {
		if strings.TrimSpace(delayRE.ReplaceAllString(v, "")) != first {
			return false
		}
	}
	return true
}

func renderInner(n *xhtml.Node) string {
	var b strings.Builder
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		_ = xhtml.Render(&b, k)
	}
	return b.String()
}

// genSections aligns the pages' sections (all pages: the same number of sections in the same
// wrappers); nodes[p][s] are page p's top-level nodes of section s.
func (g *generalizer) genSections(nodes [][][]*xhtml.Node) bool {
	for s := range nodes[0] {
		for p := range nodes {
			if len(nodes[p][s]) != len(nodes[0][s]) {
				return false
			}
		}
		for j := range nodes[0][s] {
			col := make([]*xhtml.Node, len(nodes))
			for p := range nodes {
				col[p] = nodes[p][s][j]
			}
			if col[0].Type == xhtml.TextNode {
				for _, t := range col {
					if strings.TrimSpace(t.Data) != strings.TrimSpace(col[0].Data) {
						return false
					}
				}
				continue
			}
			if !g.gen(col) {
				return false
			}
		}
	}
	return true
}

// groupSectionHTML is a member's section as a group compares it: a tail section (the page's
// scripts after the design's shell) that loads the same scripts as the first member's, differing
// only in values the original site printed per page (its post id, nonces), is the first member's.
func groupSectionHTML(res *SiteImport, pages []int, i, j int) string {
	s := res.Pages[pages[i]].Code.Sections[j]
	if i == 0 || !s.Tail {
		return s.HTML
	}
	first := res.Pages[pages[0]].Code
	if j < len(first.Sections) && first.Sections[j].Tail && scriptShape(first.Sections[j].HTML) == scriptShape(s.HTML) {
		return first.Sections[j].HTML
	}
	return s.HTML
}

// scriptShape is a tail's elements without what the original site printed per page: their
// tags and attributes (sources without their query), not their inline code (a post id, nonces,
// a tracking pixel's page title).
func scriptShape(src string) string {
	nodes, err := parseFragment(src)
	if err != nil {
		return src
	}
	var b strings.Builder
	for _, n := range nodes {
		if n.Type != xhtml.ElementNode {
			continue
		}
		b.WriteString("<" + n.Data)
		for _, a := range n.Attr {
			v := a.Val
			if a.Key == "src" || a.Key == "href" {
				v, _, _ = strings.Cut(v, "?")
			}
			if a.Key != AttrCodeID {
				b.WriteString(" " + a.Key + "=" + v)
			}
		}
		b.WriteString(">")
	}
	return b.String()
}

// parseSections parses each page's main sections.
func parseSections(res *SiteImport, pages []int) ([][][]*xhtml.Node, bool) {
	out := make([][][]*xhtml.Node, len(pages))
	for i, pi := range pages {
		cr := res.Pages[pi].Code
		if cr == nil {
			return nil, false
		}
		for j := range cr.Sections {
			ns, err := parseFragment(groupSectionHTML(res, pages, i, j))
			if err != nil {
				return nil, false
			}
			var keep []*xhtml.Node
			for _, n := range ns {
				if n.Type == xhtml.ElementNode || n.Type == xhtml.TextNode {
					keep = append(keep, n)
				}
			}
			out[i] = append(out[i], keep)
		}
	}
	return out, true
}

// planGroups makes the single templates of a folder's pages: one when they share a skeleton,
// else one per variant of the region that sets them apart (a category's side menu), for variants
// of two pages or more.
func planGroups(res *SiteImport, folder string, members []int) ([]SiteGroup, []string) {
	var notes []string
	note := func(f string, a ...any) { notes = append(notes, folder+"/: "+fmt.Sprintf(f, a...)) }
	// The same sections in the same wrappers with the same head.
	first := res.Pages[members[0]].Code
	for _, m := range members[1:] {
		cr := res.Pages[m].Code
		if cr == nil || first == nil || len(cr.Sections) != len(first.Sections) || groupHead(cr.Head) != groupHead(first.Head) {
			note("the pages do not share their sections and head markup; kept as pages")
			return nil, notes
		}
		for j := range cr.Sections {
			if cr.Sections[j].Wrap != first.Sections[j].Wrap {
				note("the pages' sections sit in different wrappers; kept as pages")
				return nil, notes
			}
		}
	}
	g, why := planGroup(res, folder, members)
	if g != nil {
		note("%d pages share one template", len(members))
		return []SiteGroup{*g}, notes
	}
	// Variants: split by the region with a few distinct versions.
	parts := splitVariants(res, members)
	if len(parts) < 2 {
		note("not one template: %s; kept as pages", why)
		return nil, notes
	}
	var out []SiteGroup
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		if g, why2 := planGroup(res, folder, part); g != nil {
			out = append(out, *g)
			note("%d pages share one template (a variant)", len(part))
		} else {
			note("%d pages of a variant kept as pages: %s", len(part), why2)
		}
	}
	if len(out) == 0 {
		note("not one template: %s; kept as pages", why)
	}
	return out, notes
}

// groupHead is a page's head as a group compares it: without its structured data (JSON-LD, the
// page's own; the site's SEO prints the posts') and with cache-busters and nonces normalized.
func groupHead(head string) string {
	return normVolatile(groupHeadRE.ReplaceAllString(head, ""), nil)
}

var groupHeadRE = regexp.MustCompile(`(?is)<script\b[^>]*type="application/ld\+json"[^>]*>.*?</script>\n?`)

// splitVariants partitions the pages by the html region (other than the largest) whose
// normalized versions take two to four values shared by two pages or more.
func splitVariants(res *SiteImport, members []int) [][]int {
	nodes, ok := parseSections(res, members)
	if !ok {
		return nil
	}
	g := &generalizer{}
	if !g.genSections(nodes) {
		return nil
	}
	var htmlHoles []*genHole
	for _, h := range g.holes {
		if h.kind == "html" {
			htmlHoles = append(htmlHoles, h)
		}
	}
	if len(htmlHoles) < 2 {
		return nil
	}
	sort.SliceStable(htmlHoles, func(i, j int) bool { return avgLen(htmlHoles[i].vals) > avgLen(htmlHoles[j].vals) })
	var best [][]int
	for _, h := range htmlHoles {
		by := map[string][]int{}
		var order []string
		for i, v := range h.vals {
			k := normVariant(v)
			if _, ok := by[k]; !ok {
				order = append(order, k)
			}
			by[k] = append(by[k], members[i])
		}
		if len(order) < 2 || len(order) > 4 {
			continue
		}
		var parts [][]int
		multi := 0
		for _, k := range order {
			parts = append(parts, by[k])
			if len(by[k]) >= 2 {
				multi++
			}
		}
		if multi >= 1 && (best == nil || len(parts) < len(best)) {
			best = parts
		}
	}
	return best
}

// normVariant is a region without its classes, state attributes and ids (the current page's mark
// does not make another variant).
func normVariant(v string) string {
	nodes, err := parseFragment(v)
	if err != nil {
		return v
	}
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			attrs := n.Attr[:0]
			for _, a := range n.Attr {
				if a.Key == "class" || a.Key == AttrCodeID || activeStateAttrs[a.Key] {
					continue
				}
				attrs = append(attrs, a)
			}
			n.Attr = attrs
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	out, _ := renderNodes(nodes)
	return out
}

func avgLen(vals []string) int {
	t := 0
	for _, v := range vals {
		t += len(v)
	}
	return t / max(1, len(vals))
}

// planGroup makes one template of pages that share a skeleton; nil and the reason otherwise.
func planGroup(res *SiteImport, folder string, members []int) (*SiteGroup, string) {
	nodes, ok := parseSections(res, members)
	if !ok {
		return nil, "a page could not be read"
	}
	g := &generalizer{looseIDs: true}
	if !g.genSections(nodes) {
		return nil, "their sections differ in structure"
	}
	// The title: the varying text of an <h1>.
	var title *genHole
	for _, h := range g.holes {
		if h.kind == "text" && h.nodes[0].DataAtom == atom.H1 {
			if title != nil {
				return nil, "more than one varying <h1>"
			}
			title = h
		}
	}
	if title == nil {
		return nil, "no varying <h1> to take the title from"
	}
	// The content: the largest varying region.
	var content *genHole
	mainBytes := 0
	for _, pi := range members {
		for _, s := range res.Pages[pi].Code.Sections {
			mainBytes += len(s.HTML)
		}
	}
	mainBytes /= len(members)
	// The content differs on every page; a region with a few versions (a category's side menu)
	// is not it.
	distinct := func(h *genHole) bool {
		seen := map[string]bool{}
		for _, v := range h.vals {
			k := normVariant(v)
			if seen[k] {
				return false
			}
			seen[k] = true
		}
		return true
	}
	for _, h := range g.holes {
		if h.kind == "html" && distinct(h) && (content == nil || avgLen(h.vals) > avgLen(content.vals)) {
			content = h
		}
	}
	if content != nil && avgLen(content.vals) < 80 {
		content = nil
	}
	other, otherBytes := 0, 0
	for _, h := range g.holes {
		if h.kind == "html" && h != content {
			other++
			otherBytes += avgLen(h.vals)
		}
	}
	if other > 8 || otherBytes*5 > mainBytes {
		return nil, fmt.Sprintf("besides the title and the content, %d regions differ (%d bytes a page)", other, otherBytes)
	}
	if len(g.holes) > 40 {
		return nil, fmt.Sprintf("%d places differ", len(g.holes))
	}
	// Holes of other elements whose texts are the title (a breadcrumb's last item) show the title.
	isTitle := func(h *genHole) bool {
		if h.kind != "text" {
			return false
		}
		for i := range h.vals {
			if h.vals[i] != title.vals[i] {
				return false
			}
		}
		return true
	}
	// Bindings by element (member 0's ids), custom fields numbered in document order.
	type elemHoles struct {
		n     *xhtml.Node
		holes []*genHole
	}
	var order []*elemHoles
	byNode := map[*xhtml.Node]*elemHoles{}
	for _, h := range g.holes {
		n := h.nodes[0]
		eh := byNode[n]
		if eh == nil {
			eh = &elemHoles{n: n}
			byNode[n] = eh
			order = append(order, eh)
		}
		eh.holes = append(eh.holes, h)
	}
	posts := make([]SiteGroupPost, len(members))
	for i, pi := range members {
		posts[i] = SiteGroupPost{Page: pi, Title: title.vals[i], Fields: map[string]string{}}
		if content != nil {
			posts[i].Content = content.vals[i]
		}
	}
	binds := map[string]*CodeBind{} // by member-0 data-wbc
	var fields []string
	n := 0
	for _, eh := range order {
		id := attr(eh.n, AttrCodeID)
		var text, htmlH *genHole
		var attrs []*genHole
		for _, h := range eh.holes {
			switch h.kind {
			case "text":
				text = h
			case "html":
				htmlH = h
			case "attr":
				attrs = append(attrs, h)
			}
		}
		switch {
		case htmlH == content && content != nil:
			if len(attrs) > 0 {
				return nil, "the content region's own attributes differ"
			}
			binds[id] = &CodeBind{Kind: BindField, Field: "content"}
			continue
		case text != nil && (text == title || isTitle(text)) && len(attrs) == 0:
			binds[id] = &CodeBind{Kind: BindField, Field: "title"}
			continue
		}
		n++
		name := "design_" + strconv.Itoa(n)
		fields = append(fields, name)
		b := &CodeBind{Kind: BindField, Field: "custom", Name: name}
		switch {
		case htmlH != nil:
			if len(attrs) > 0 {
				return nil, "a varying region's own attributes differ too"
			}
			b.Attr = "html"
			for i := range posts {
				posts[i].Fields[TrustedHTMLField+name] = htmlH.vals[i]
			}
		case len(attrs) > 0:
			b.Attr = "attrs"
			for _, h := range attrs {
				for i := range posts {
					posts[i].Fields[name+"_"+strings.ReplaceAll(h.attr, "-", "_")] = h.vals[i]
				}
			}
			if text != nil {
				b.Text = true
				for i := range posts {
					posts[i].Fields[name] = text.vals[i]
				}
			}
		default:
			for i := range posts {
				posts[i].Fields[name] = text.vals[i]
			}
		}
		binds[id] = b
	}
	// The pages with every varying place set to the first page's values differ only in classes and
	// state attributes now: the active-link learner makes the shared markup and its rule.
	for _, h := range g.holes {
		for i := 1; i < len(h.nodes); i++ {
			fillLike(h, i)
		}
	}
	samples := make([]ActiveSample, len(members))
	for i, pi := range members {
		samples[i].Path = res.Pages[pi].URL
		for _, sec := range nodes[i] {
			s, _ := renderNodes(sec)
			samples[i].Parts = append(samples[i].Parts, s)
		}
	}
	clusters := ClusterParts(samples)
	if len(clusters) != 1 || len(clusters[0].Members) != len(members) {
		return nil, "their remaining differences are not the current page's mark"
	}
	cl := clusters[0]
	cr := res.Pages[members[0]].Code
	heads := make([]string, len(members))
	for i, pi := range members {
		heads[i] = res.Pages[pi].Code.Head
	}
	hattrs, battrs := make([]string, len(members)), make([]string, len(members))
	for i, pi := range members {
		hattrs[i], battrs[i] = res.Pages[pi].Code.HTMLAttrs, res.Pages[pi].Code.BodyAttrs
	}
	grp := &SiteGroup{Folder: folder, Head: groupCommonHead(heads), Lang: cr.Lang, Attrs: [2]string{CommonAttrs(hattrs), CommonAttrs(battrs)}, Rule: cl.Rule, Fields: fields}
	// Each section gets the bindings of its elements.
	for j, part := range cl.Parts {
		root := parseSource(part)
		ids := map[string]*srcNode{}
		root.index(ids)
		sb := CodeBinds{}
		for id, b := range binds {
			if ids[id] != nil {
				sb[id] = b
			}
		}
		grp.Sections = append(grp.Sections, CodePart{Name: cr.Sections[j].Name, HTML: part, Wrap: cr.Sections[j].Wrap, Binds: sb.String(), Tail: cr.Sections[j].Tail})
	}
	// The check: every page, rendered from the template with its own content, is the design.
	for i, pi := range members {
		pp := &planPost{Title: posts[i].Title, Content: posts[i].Content, Fields: posts[i].Fields, URL: res.Pages[pi].URL}
		for j, sec := range grp.Sections {
			out, ok := renderPlan(sec.HTML, sec.Binds, &planRes{post: pp})
			if !ok {
				return nil, "the template could not be rendered"
			}
			if grp.Rule != nil {
				out = ApplyActive(out, grp.Rule, res.Pages[pi].URL)
			}
			if !sameMarkup("group "+res.Pages[pi].Path, looseIDMarkup(out), looseIDMarkup(groupSectionHTML(res, members, i, j))) {
				return nil, fmt.Sprintf("the template does not reproduce %s", res.Pages[pi].Path)
			}
		}
	}
	grp.Posts = posts
	return grp, ""
}

// groupCommonHead is the head of a group's template: the first page's head elements that every
// page has, compared without cache-busters and nonces (a stylesheet saved with another ?ver= is
// the same file), without the pages' JSON-LD.
func groupCommonHead(heads []string) string {
	var keep []string
	for _, e := range headElements(heads[0]) {
		if groupHeadRE.MatchString(e) {
			continue
		}
		ne := normVolatile(e, nil)
		all := true
		for _, h := range heads[1:] {
			if !strings.Contains(normVolatile(h, nil), ne) {
				all = false
				break
			}
		}
		if all {
			keep = append(keep, e)
		}
	}
	return strings.Join(keep, "\n")
}

// fillLike sets page i's side of a hole to page 0's value (in its parsed tree).
func fillLike(h *genHole, i int) {
	n0, n := h.nodes[0], h.nodes[i]
	switch h.kind {
	case "attr":
		for k := range n.Attr {
			if n.Attr[k].Key == h.attr {
				n.Attr[k].Val = attr(n0, h.attr)
			}
		}
	case "text":
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			if k.Type == xhtml.TextNode && strings.TrimSpace(k.Data) != "" {
				for k0 := n0.FirstChild; k0 != nil; k0 = k0.NextSibling {
					if k0.Type == xhtml.TextNode && strings.TrimSpace(k0.Data) != "" {
						k.Data = k0.Data
						break
					}
				}
				break
			}
		}
	case "html":
		for n.FirstChild != nil {
			n.RemoveChild(n.FirstChild)
		}
		for k := n0.FirstChild; k != nil; k = k.NextSibling {
			n.AppendChild(cloneNode(k))
		}
	}
}

// ---------------------------------------------------------------------------------------------
// Listings.

// planListings binds the lists on the site's other pages whose items link to the folder's pages.
func (b *SiteBinding) planListings(res *SiteImport, folder string, members []int, parent string, posts bool) {
	if parent == "" && !posts {
		if _, leaf := SiteLeafParent(folder); leaf {
			b.Notes = append(b.Notes, folder+": lists of these pages stay markup: a loop of pages selects them by their parent page, which they do not have to themselves")
		}
		return
	}
	byURL := map[string]int{}
	isMember := map[int]bool{}
	for _, m := range members {
		byURL[ActivePath(res.Pages[m].URL)] = m
		isMember[m] = true
	}
	titles := map[int]string{}
	for _, m := range members {
		if t := pageH1(res.Pages[m].Code); t != "" {
			titles[m] = t
		}
	}
	type found struct {
		page, section int
		list          *xhtml.Node
		cards         []*xhtml.Node
		members       []int
	}
	var lists []found
	for pi, pg := range res.Pages {
		if isMember[pi] || pg.Code == nil {
			continue
		}
		for si, sec := range pg.Code.Sections {
			nodes, err := parseFragment(sec.HTML)
			if err != nil {
				continue
			}
			var walk func(n *xhtml.Node)
			walk = func(n *xhtml.Node) {
				if n.Type != xhtml.ElementNode || n.Namespace != "" || codeOpaque[n.DataAtom] {
					return
				}
				var cards []*xhtml.Node
				var ms []int
				seen := map[int]bool{}
				ok := attr(n, AttrCodeID) != ""
				for k := n.FirstChild; k != nil && ok; k = k.NextSibling {
					switch k.Type {
					case xhtml.TextNode:
						if strings.TrimSpace(k.Data) != "" {
							ok = false
						}
					case xhtml.ElementNode:
						l := k
						if k.DataAtom != atom.A {
							l = firstLink(k)
						}
						m, hit := -1, false
						if l != nil {
							m, hit = byURL[ActivePath(attr(l, "href"))]
						}
						if !hit || seen[m] {
							ok = false
							break
						}
						seen[m] = true
						cards = append(cards, k)
						ms = append(ms, m)
					}
				}
				if ok && len(cards) >= 2 {
					lists = append(lists, found{page: pi, section: si, list: n, cards: cards, members: ms})
					return
				}
				for k := n.FirstChild; k != nil; k = k.NextSibling {
					walk(k)
				}
			}
			for _, n := range nodes {
				walk(n)
			}
		}
	}
	if len(lists) == 0 {
		return
	}
	// The longest list that can be a loop sets the pages' order (an author archive whose cards
	// differ in structure must not decide it); the others must show a window of it.
	sort.SliceStable(lists, func(i, j int) bool { return len(lists[i].members) > len(lists[j].members) })
	for i, l := range lists {
		if binds, _, _ := listingBinds(res, l.list, l.cards, l.members, titles, 0); binds != nil {
			lists[0], lists[i] = lists[i], lists[0]
			sort.SliceStable(lists[1:], func(a, b int) bool { return len(lists[1+a].members) > len(lists[1+b].members) })
			break
		}
	}
	orderOf := map[int]int{}
	for i, m := range lists[0].members {
		orderOf[m] = i
	}
	next := len(lists[0].members)
	for _, m := range members {
		if _, ok := orderOf[m]; !ok {
			orderOf[m] = next
			next++
		}
	}
	byOrder := make([]int, next)
	for m, o := range orderOf {
		byOrder[o] = m
	}
	listN := 0
	for _, l := range lists {
		off := orderOf[l.members[0]]
		window := true
		for i, m := range l.members {
			if orderOf[m] != off+i {
				window = false
			}
		}
		if !window || off > MaxLoopOffset || len(l.members) > MaxLoopPerPage {
			b.Notes = append(b.Notes, fmt.Sprintf("%s: a list of %s/ pages shows them in another order; kept as markup", res.Pages[l.page].Path, folder))
			continue
		}
		listN++
		binds, plans, why := listingBinds(res, l.list, l.cards, l.members, titles, listN)
		if binds == nil {
			b.Notes = append(b.Notes, fmt.Sprintf("%s: a list of %s/ pages kept as markup: %s", res.Pages[l.page].Path, folder, why))
			continue
		}
		listID := attr(l.list, AttrCodeID)
		q := map[string]string{LoopPostType: "page", LoopParent: parent, LoopOrderBy: "menu_order",
			LoopOrder: "asc", LoopPerPage: strconv.Itoa(len(l.members)), LoopOffset: strconv.Itoa(off)}
		if posts {
			// Posts: newest first, as a blog lists them; the import dates them in the list's order,
			// so a post written later comes first.
			q[LoopPostType], q[LoopOrderBy], q[LoopOrder] = "post", "date", "desc"
			delete(q, LoopParent)
		}
		binds[listID] = &CodeBind{Kind: BindLoop, Query: q}
		sec := &res.Pages[l.page].Code.Sections[l.section]
		all := ParseCodeBinds(sec.Binds)
		if all == nil {
			all = CodeBinds{}
		}
		for k, v := range binds {
			all[k] = v
		}
		// The check: the list rendered from the planned pages is the design's.
		var posts []*planPost
		for o := off; o < off+len(l.members); o++ {
			m := byOrder[o]
			pl := plans[m]
			posts = append(posts, &planPost{Title: titles[m], URL: res.Pages[m].URL, Excerpt: pl.Excerpt, Fields: pl.Fields})
		}
		out, ok := renderPlan(sec.HTML, all.String(), &planRes{posts: posts})
		if !ok || !sameMarkup("listing "+res.Pages[l.page].Path, looseIDMarkup(out), looseIDMarkup(sec.HTML)) {
			b.Notes = append(b.Notes, fmt.Sprintf("%s: a list of %s/ pages kept as markup: the loop would not reproduce it", res.Pages[l.page].Path, folder))
			continue
		}
		conflict := false
		for m, pl := range plans {
			ex := b.Plans[m]
			if ex != nil && pl.Excerpt != "" && ex.Excerpt != "" && ex.Excerpt != pl.Excerpt {
				conflict = true
			}
		}
		if conflict {
			b.Notes = append(b.Notes, fmt.Sprintf("%s: a list of %s/ pages kept as markup: its summaries differ from another list's", res.Pages[l.page].Path, folder))
			continue
		}
		sec.Binds = all.String()
		for m, pl := range plans {
			ex := b.plan(m)
			if titles[m] != "" { // a page with two <h1> keeps the title its group found
				ex.Title = titles[m]
			}
			if pl.Excerpt != "" {
				ex.Excerpt = pl.Excerpt
			}
			for k, v := range pl.Fields {
				ex.Fields[k] = v
			}
		}
		for _, m := range members {
			ex := b.plan(m)
			ex.MenuOrder, ex.HasOrder = orderOf[m], true
			if ex.Title == "" {
				ex.Title = titles[m]
			}
		}
		b.Listings = append(b.Listings, SiteListing{Page: l.page, Section: l.section, Folder: folder, Members: l.members, Offset: off})
		b.Notes = append(b.Notes, fmt.Sprintf("%s: a list of %d %s/ pages is a loop", res.Pages[l.page].Path, len(l.members), folder))
	}
}

// listingBinds generalizes a list's cards: the link to the page is its URL, a text equal to the
// page's <h1> its title, the longest other text its excerpt, a varying image the image field, the
// rest custom fields list<n>_<k>.
func listingBinds(res *SiteImport, list *xhtml.Node, cards []*xhtml.Node, ms []int, titles map[int]string, listN int) (CodeBinds, map[int]*SitePagePlan, string) {
	g := &generalizer{looseIDs: true}
	if !g.gen(cards) {
		return nil, nil, "its items differ in structure"
	}
	plans := map[int]*SitePagePlan{}
	for _, m := range ms {
		plans[m] = &SitePagePlan{Fields: map[string]string{}}
	}
	binds := CodeBinds{}
	byNode := map[*xhtml.Node][]*genHole{}
	var order []*xhtml.Node
	for _, h := range g.holes {
		if _, ok := byNode[h.nodes[0]]; !ok {
			order = append(order, h.nodes[0])
		}
		byNode[h.nodes[0]] = append(byNode[h.nodes[0]], h)
	}
	// The excerpt: the longest varying text (of an element with no other content) of 40+ characters.
	var excerpt *genHole
	for _, h := range g.holes {
		if h.kind == "text" && kidsAreText(h.nodes[0]) && avgLen(h.vals) >= 40 && (excerpt == nil || avgLen(h.vals) > avgLen(excerpt.vals)) {
			ok := true
			for i, v := range h.vals {
				if v == titles[ms[i]] || len(strings.Fields(v)) > MaxDynWords {
					ok = false
				}
			}
			if ok {
				excerpt = h
			}
		}
	}
	urlOK := false
	k := 0
	for _, n := range order {
		hs := byNode[n]
		id := attr(n, AttrCodeID)
		var text *genHole
		var attrs []*genHole
		var hh *genHole
		for _, h := range hs {
			switch h.kind {
			case "text":
				text = h
			case "attr":
				attrs = append(attrs, h)
			case "html":
				hh = h
			}
		}
		// The link to the page.
		if n.DataAtom == atom.A && len(attrs) == 1 && attrs[0].attr == "href" && hh == nil {
			same := true
			for i, v := range attrs[0].vals {
				if ActivePath(v) != ActivePath(res.Pages[ms[i]].URL) {
					same = false
				}
			}
			if same {
				urlOK = true
				if text != nil {
					// <a href>Title</a>: the link and its text in one element.
					if !holeIsTitle(text, ms, titles) {
						return nil, nil, "a link's text is not the page's title"
					}
					binds[id] = &CodeBind{Kind: BindField, Field: "link"}
					continue
				}
				binds[id] = &CodeBind{Kind: BindField, Field: "url"}
				continue
			}
		}
		if hh == nil && len(attrs) == 0 && text != nil && holeIsTitle(text, ms, titles) {
			binds[id] = &CodeBind{Kind: BindField, Field: "title"}
			continue
		}
		if text == excerpt && excerpt != nil && len(attrs) == 0 && hh == nil {
			b := &CodeBind{Kind: BindField, Field: "excerpt"}
			if w := maxWords(excerpt.vals); w > 55 {
				b.Words = MaxDynWords
			}
			binds[id] = b
			for i, m := range ms {
				plans[m].Excerpt = excerpt.vals[i]
			}
			continue
		}
		k++
		name := "list" + strconv.Itoa(listN) + "_" + strconv.Itoa(k)
		if n.DataAtom == atom.Img && hh == nil && text == nil && len(attrs) > 0 && hasHoleAttr(attrs, "src") {
			// The image: the page's featured image when it has one, else this field.
			ok := true
			for _, h := range attrs {
				if h.attr != "src" && h.attr != "alt" {
					ok = false
				}
			}
			if ok {
				binds[id] = &CodeBind{Kind: BindField, Field: "image", Name: name}
				for _, h := range attrs {
					key := name
					if h.attr == "alt" {
						key = name + "_alt"
					}
					for i, m := range ms {
						plans[m].Fields[key] = h.vals[i]
					}
				}
				continue
			}
		}
		b := &CodeBind{Kind: BindField, Field: "custom", Name: name}
		switch {
		case hh != nil:
			if len(attrs) > 0 {
				return nil, nil, "an item region's own attributes differ"
			}
			b.Attr = "html"
			for i, m := range ms {
				plans[m].Fields[TrustedHTMLField+name] = hh.vals[i]
			}
		case len(attrs) > 0:
			b.Attr = "attrs"
			for _, h := range attrs {
				for i, m := range ms {
					plans[m].Fields[name+"_"+strings.ReplaceAll(h.attr, "-", "_")] = h.vals[i]
				}
			}
			if text != nil {
				b.Text = true
				for i, m := range ms {
					plans[m].Fields[name] = text.vals[i]
				}
			}
		default:
			for i, m := range ms {
				plans[m].Fields[name] = text.vals[i]
			}
		}
		binds[id] = b
	}
	if !urlOK {
		return nil, nil, "its items' links could not be bound"
	}
	return binds, plans, ""
}

func hasHoleAttr(hs []*genHole, a string) bool {
	for _, h := range hs {
		if h.attr == a {
			return true
		}
	}
	return false
}

func kidsAreText(n *xhtml.Node) bool {
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == xhtml.ElementNode {
			return false
		}
	}
	return true
}

func maxWords(vals []string) int {
	m := 0
	for _, v := range vals {
		if w := len(strings.Fields(v)); w > m {
			m = w
		}
	}
	return m
}

func holeIsTitle(h *genHole, ms []int, titles map[int]string) bool {
	for i, v := range h.vals {
		if t := titles[ms[i]]; t == "" || strings.Join(strings.Fields(v), " ") != t {
			return false
		}
	}
	return true
}

// pageH1 is the text of a page's only <h1> ("" when it has none or several).
func pageH1(cr *CodeImport) string {
	if cr == nil {
		return ""
	}
	var found []string
	for _, s := range cr.Sections {
		nodes, err := parseFragment(s.HTML)
		if err != nil {
			continue
		}
		var walk func(n *xhtml.Node)
		walk = func(n *xhtml.Node) {
			if n.Type == xhtml.ElementNode && n.DataAtom == atom.H1 {
				found = append(found, strings.Join(strings.Fields(textOf(n)), " "))
				return
			}
			for k := n.FirstChild; k != nil; k = k.NextSibling {
				walk(k)
			}
		}
		for _, n := range nodes {
			walk(n)
		}
	}
	if len(found) != 1 || utf8.RuneCountInString(found[0]) > 200 {
		return ""
	}
	return found[0]
}
