package webbuilder

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Active links (docs/developer/web-builder-code-sections.md, "Active links"): a static design marks
// the current page in its menu by hand, so its header differs from page to page only in the classes
// and attributes of one or two links (text-brand-600 and aria-current="page" on "Anasayfa" of
// index.html, the same classes on the "Kurumsal" menu button of hakkimizda.html). The site import
// learns that difference from the pages' copies of the header (LearnActive): one template keeps the
// header with every element in its plain state, and an ActiveRule tells the front end which
// element to mark for the requested URL (ApplyActive).

// activeStateAttrs are the attributes a design may change on the current page's link besides its
// classes; any other difference makes two copies of a header different headers.
var activeStateAttrs = map[string]bool{"aria-current": true, "aria-selected": true, "data-active": true,
	"data-current": true, "data-state": true, "data-selected": true}

// ActiveState is how an element looks: its class attribute and its state attributes.
type ActiveState struct {
	Class string            `json:"class"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

func (s ActiveState) key() string {
	keys := make([]string, 0, len(s.Attrs))
	for k := range s.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(s.Class)
	for _, k := range keys {
		b.WriteString("\x00" + k + "=" + s.Attrs[k])
	}
	return b.String()
}

// ActiveRole is a kind of element that the current page changes (the menu's top-level links, its
// dropdown items, its dropdown buttons): Base is how such an element looks normally, Exact on the
// page it links to, Near on a page of its section (a dropdown button for a page in its menu, the
// news link on a news article). A missing Exact is Near and a missing Near is Exact without
// aria-current.
type ActiveRole struct {
	Tag   string       `json:"tag"`
	Base  ActiveState  `json:"base"`
	Exact *ActiveState `json:"exact,omitempty"`
	Near  *ActiveState `json:"near,omitempty"`
}

// ActiveItem is one element of a role and the pages that mark it. Key names it: "href:/path/" (a
// link, by the path it points at), "controls:<id>" (a menu button), "id:<id>", or "#<n>" (the n-th
// element of its role). Exact: marked on the page it links to; Paths: other pages (site paths)
// that mark it; Prefixes: sections ("/haber/") all of whose pages mark it.
type ActiveItem struct {
	Role     int      `json:"role"`
	Key      string   `json:"key"`
	Exact    bool     `json:"exact,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`
}

// ActiveRule is what LearnActive found: stored with a template part (code section content.active).
type ActiveRule struct {
	Roles []ActiveRole `json:"roles"`
	Items []ActiveItem `json:"items"`
}

// ParseActiveRule reads content.active ("" or a bad value: nil).
func ParseActiveRule(s string) *ActiveRule {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var r ActiveRule
	if json.Unmarshal([]byte(s), &r) != nil || len(r.Items) == 0 {
		return nil
	}
	for _, it := range r.Items {
		if it.Role < 0 || it.Role >= len(r.Roles) {
			return nil
		}
	}
	return &r
}

// String is the rule as stored.
func (r *ActiveRule) String() string {
	if r == nil {
		return ""
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// ActivePath normalizes a link or a request path for matching: the path of a same-site URL
// ("https://host/a/?x#y" and "/a" are "/a/"; a file path such as "/a.pdf" keeps its name); ""
// for links elsewhere (other hosts, mailto:, #anchors).
func ActivePath(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return ""
	}
	p := u.Path
	if u.Host != "" && p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "" // a relative link: imported links are site paths
	}
	if !strings.HasSuffix(p, "/") && path.Ext(p) == "" {
		p += "/"
	}
	return p
}

// activeEl is an element of a part's markup as the learner sees it.
type activeEl struct {
	n     *html.Node
	tag   string
	state ActiveState
	key   string // href / controls / id key (the ordinal is added per role)
	href  string // ActivePath of its href
}

// activeElements flattens markup into its elements (document order) and the skeleton that must be
// the same for two copies to be one part: tags, other attributes and text.
func activeElements(nodes []*html.Node) ([]*activeEl, string) {
	var els []*activeEl
	var sk strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			sk.WriteString("\x01" + n.Data)
			return
		case html.CommentNode:
			sk.WriteString("\x02" + n.Data)
			return
		case html.ElementNode:
		default:
			return
		}
		e := &activeEl{n: n, tag: n.Data}
		sk.WriteString("<" + n.Data)
		for _, a := range n.Attr {
			key := a.Key
			if a.Namespace != "" {
				key = a.Namespace + ":" + a.Key
			}
			switch {
			case key == AttrCodeID:
			case key == "class":
				e.state.Class = a.Val
			case activeStateAttrs[key]:
				if e.state.Attrs == nil {
					e.state.Attrs = map[string]string{}
				}
				e.state.Attrs[key] = a.Val
			default:
				sk.WriteString(" " + key + "=" + a.Val)
			}
		}
		sk.WriteString(">")
		e.href = ActivePath(attr(n, "href"))
		switch {
		case e.href != "" && (n.DataAtom == atom.A || n.DataAtom == atom.Area):
			e.key = "href:" + e.href
		case attr(n, "aria-controls") != "":
			e.key = "controls:" + attr(n, "aria-controls")
		case attr(n, "id") != "":
			e.key = "id:" + attr(n, "id")
		}
		els = append(els, e)
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
		sk.WriteString("</>")
	}
	for _, n := range nodes {
		walk(n)
	}
	return els, sk.String()
}

// parseParts parses each part's markup (a fragment in body context).
func parseParts(parts []string) ([][]*html.Node, error) {
	out := make([][]*html.Node, len(parts))
	for i, p := range parts {
		ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
		nodes, err := html.ParseFragment(strings.NewReader(p), ctx)
		if err != nil {
			return nil, err
		}
		out[i] = nodes
	}
	return out, nil
}

// ActiveSample is one page's copy of a template part: the page's site path ("/hakkimizda/"; "" for
// a page that is not a URL, the 404 page) and the part's markup (one string per section).
type ActiveSample struct {
	Path  string
	Parts []string
}

// ActiveCluster is a group of samples that are one part: Parts is the shared markup with every
// element in its plain state (the ids of the sample it was made from), Rule marks the current
// page's link (nil when nothing differs), Members the samples it reproduces exactly.
type ActiveCluster struct {
	Parts   []string
	Rule    *ActiveRule
	Members []int
}

// ClusterParts groups pages' copies of a part (a header with its skip link and sprite, a footer):
// copies that differ only in the current page's link are one cluster with an ActiveRule; every
// member is checked by applying the rule to the shared markup, and copies it does not reproduce
// exactly form clusters of their own. Clusters come largest first.
func ClusterParts(samples []ActiveSample) []ActiveCluster {
	type parsed struct {
		nodes [][]*html.Node
		els   []*activeEl
		sk    string
		plain string // the markup without editor ids
	}
	ps := make([]*parsed, len(samples))
	// norm: the samples as the parser reads them (rendered again), what the rule is checked against.
	norm := make([]ActiveSample, len(samples))
	groups := map[string][]int{}
	var order []string
	for i, s := range samples {
		nodes, err := parseParts(s.Parts)
		p := &parsed{plain: StripCodeIDs(strings.Join(s.Parts, "\x00"))}
		norm[i] = ActiveSample{Path: s.Path, Parts: s.Parts}
		if err == nil {
			p.nodes = nodes
			norm[i].Parts = make([]string, len(nodes))
			for j, ns := range nodes {
				var b strings.Builder
				for _, x := range ns {
					_ = html.Render(&b, x)
				}
				norm[i].Parts[j] = b.String()
			}
			var all []*html.Node
			var sk strings.Builder
			for j, ns := range nodes {
				all = append(all, ns...)
				els, k := activeElements(ns)
				p.els = append(p.els, els...)
				sk.WriteString(strconv.Itoa(j) + "\x03" + k)
			}
			p.sk = sk.String()
		} else {
			p.sk = "\x04" + p.plain
		}
		ps[i] = p
		if _, ok := groups[p.sk]; !ok {
			order = append(order, p.sk)
		}
		groups[p.sk] = append(groups[p.sk], i)
	}
	var out []ActiveCluster
	rest := map[string][]int{} // exact copies the rule did not reproduce
	var restOrder []string
	for _, key := range order {
		members := groups[key]
		if len(members) == 1 || ps[members[0]].nodes == nil {
			for _, m := range members {
				if _, ok := rest[ps[m].plain]; !ok {
					restOrder = append(restOrder, ps[m].plain)
				}
				rest[ps[m].plain] = append(rest[ps[m].plain], m)
			}
			continue
		}
		cl, missed := learnGroup(norm, members, func(i int) ([][]*html.Node, []*activeEl) { return ps[i].nodes, ps[i].els })
		if len(cl.Members) > 0 {
			out = append(out, cl)
		}
		for _, m := range missed {
			if _, ok := rest[ps[m].plain]; !ok {
				restOrder = append(restOrder, ps[m].plain)
			}
			rest[ps[m].plain] = append(rest[ps[m].plain], m)
		}
	}
	for _, k := range restOrder {
		ms := rest[k]
		out = append(out, ActiveCluster{Parts: samples[ms[0]].Parts, Members: ms})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Members) > len(out[j].Members) })
	return out
}

// learnGroup learns the rule of copies with the same skeleton (see ClusterParts); missed are the
// members the rule does not reproduce.
func learnGroup(samples []ActiveSample, members []int, get func(int) ([][]*html.Node, []*activeEl)) (ActiveCluster, []int) {
	n := len(members)
	_, els0 := get(members[0])
	count := len(els0)
	type varying struct {
		idx   int
		base  ActiveState
		on    map[int]ActiveState // member index → its state where not base
		tag   string
		key   string
		href  string
		exact *ActiveState
		near  *ActiveState
		bad   bool
	}
	type seen struct {
		idx    int
		el     *activeEl
		states []ActiveState
		cnt    map[string]int
		byKey  map[string]ActiveState
	}
	var list []*seen
	// global counts every state over all the elements that change: an element's plain state is
	// the one the elements show most (a menu button marked on most pages of a site whose pages are
	// mostly in its menu still has the plain look of the other buttons).
	global := map[string]int{}
	for e := 0; e < count; e++ {
		states := make([]ActiveState, n)
		same := true
		for mi, m := range members {
			_, els := get(m)
			states[mi] = els[e].state
			if states[mi].key() != states[0].key() {
				same = false
			}
		}
		if same {
			continue
		}
		sn := &seen{idx: e, el: els0[e], states: states, cnt: map[string]int{}, byKey: map[string]ActiveState{}}
		for mi, m := range members {
			k := states[mi].key()
			sn.byKey[k] = states[mi]
			if sn.el.href == "" || samples[m].Path != sn.el.href { // not counting the page it links to
				sn.cnt[k]++
				global[k]++
			}
		}
		list = append(list, sn)
	}
	var vars []*varying
	for _, sn := range list {
		el := sn.el
		keys := make([]string, 0, len(sn.byKey))
		for k := range sn.byKey {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if global[keys[i]] != global[keys[j]] {
				return global[keys[i]] > global[keys[j]]
			}
			if sn.cnt[keys[i]] != sn.cnt[keys[j]] {
				return sn.cnt[keys[i]] > sn.cnt[keys[j]]
			}
			if a, b := len(sn.byKey[keys[i]].Attrs), len(sn.byKey[keys[j]].Attrs); a != b {
				return a < b
			}
			return keys[i] < keys[j]
		})
		v := &varying{idx: sn.idx, base: sn.byKey[keys[0]], on: map[int]ActiveState{}, tag: el.tag, key: el.key, href: el.href}
		for mi, m := range members {
			st := sn.states[mi]
			if st.key() == keys[0] {
				continue
			}
			v.on[mi] = st
			slot := &v.near
			if el.href != "" && samples[m].Path == el.href {
				slot = &v.exact
			}
			if *slot == nil {
				c := st
				*slot = &c
			} else if (*slot).key() != st.key() {
				v.bad = true
			}
		}
		vars = append(vars, v)
	}
	// Roles: elements of one tag and plain state whose marked states agree.
	rule := &ActiveRule{}
	roleOf := map[*varying]int{}
	for _, v := range vars {
		if v.bad {
			continue
		}
		found := -1
		for ri, r := range rule.Roles {
			if r.Tag != v.tag || r.Base.key() != v.base.key() {
				continue
			}
			if (r.Exact != nil && v.exact != nil && r.Exact.key() != v.exact.key()) || (r.Near != nil && v.near != nil && r.Near.key() != v.near.key()) {
				continue
			}
			found = ri
			break
		}
		if found < 0 {
			rule.Roles = append(rule.Roles, ActiveRole{Tag: v.tag, Base: v.base})
			found = len(rule.Roles) - 1
		}
		r := &rule.Roles[found]
		if r.Exact == nil && v.exact != nil {
			r.Exact = v.exact
		}
		if r.Near == nil && v.near != nil {
			r.Near = v.near
		}
		roleOf[v] = found
	}
	// Items: which pages mark each element.
	paths := map[string]bool{}
	for _, m := range members {
		if samples[m].Path != "" {
			paths[samples[m].Path] = true
		}
	}
	for _, v := range vars {
		ri, ok := roleOf[v]
		if !ok {
			continue
		}
		it := ActiveItem{Role: ri, Key: v.key}
		if it.Key == "" {
			// No key of its own: its place among the elements of its tag and plain state.
			k := 0
			for e := 0; e < v.idx; e++ {
				st := els0[e].state
				for _, x := range vars {
					if x.idx == e {
						st = x.base
					}
				}
				if els0[e].tag == v.tag && st.key() == v.base.key() {
					k++
				}
			}
			it.Key = "#" + strconv.Itoa(k)
		}
		var near []string
		for mi := range v.on {
			p := samples[members[mi]].Path
			if p == "" {
				continue
			}
			if v.href != "" && p == v.href {
				it.Exact = true
			} else {
				near = append(near, p)
			}
		}
		// A section all of whose pages mark the element (news articles under /haber/ mark the news
		// link): kept as a prefix, so pages added there later are marked too.
		inNear := map[string]bool{}
		for _, p := range near {
			inNear[p] = true
		}
		dirs := map[string]bool{}
		for _, p := range near {
			if d := path.Dir(strings.TrimSuffix(p, "/")); d != "/" && d != "." {
				dirs[d+"/"] = true
			}
		}
		for d := range dirs {
			under, all := 0, true
			for p := range paths {
				if strings.HasPrefix(p, d) && p != d {
					under++
					all = all && inNear[p]
				}
			}
			if all && under >= 2 {
				it.Prefixes = append(it.Prefixes, d)
			}
		}
		sort.Strings(it.Prefixes)
		for _, p := range near {
			covered := false
			for _, d := range it.Prefixes {
				covered = covered || (strings.HasPrefix(p, d) && p != d)
			}
			if !covered {
				it.Paths = append(it.Paths, p)
			}
		}
		sort.Strings(it.Paths)
		rule.Items = append(rule.Items, it)
	}
	if len(rule.Items) == 0 {
		rule = nil
	}
	// The shared markup: the copy with the fewest marked elements, every element set to its plain state.
	best, bestN := 0, -1
	for mi := range members {
		c := 0
		for _, v := range vars {
			if _, on := v.on[mi]; on {
				c++
			}
		}
		if bestN < 0 || c < bestN {
			best, bestN = mi, c
		}
	}
	nodes, els := get(members[best])
	for _, v := range vars {
		setState(els[v.idx].n, v.base)
	}
	parts := make([]string, len(nodes))
	for i, ns := range nodes {
		var b strings.Builder
		for _, x := range ns {
			_ = html.Render(&b, x)
		}
		parts[i] = b.String()
	}
	cl := ActiveCluster{Parts: parts, Rule: rule}
	var missed []int
	// Checked as the front end applies it: over the whole part (ordinals run across its sections).
	plainBase := make([]string, len(parts))
	for i, p := range parts {
		plainBase[i] = StripCodeIDs(p)
	}
	base := strings.Join(plainBase, "\x00")
	for _, m := range members {
		got := make([]string, len(samples[m].Parts))
		for i, p := range samples[m].Parts {
			got[i] = StripCodeIDs(p)
		}
		if len(got) == len(parts) && ApplyActive(base, rule, samples[m].Path) == strings.Join(got, "\x00") {
			cl.Members = append(cl.Members, m)
		} else {
			missed = append(missed, m)
		}
	}
	return cl, missed
}

// setState gives an element a state: its class attribute and state attributes (changed in place,
// removed, or added at the end).
func setState(n *html.Node, st ActiveState) {
	n.Attr = stateAttrs(n.Attr, st)
}

func stateAttrs(attrs []html.Attribute, st ActiveState) []html.Attribute {
	out := make([]html.Attribute, 0, len(attrs)+len(st.Attrs))
	hasClass := false
	done := map[string]bool{}
	for _, a := range attrs {
		key := a.Key
		if a.Namespace != "" {
			key = a.Namespace + ":" + a.Key
		}
		switch {
		case key == "class":
			hasClass = true
			if st.Class != "" {
				a.Val = st.Class
				out = append(out, a)
			}
		case activeStateAttrs[key]:
			if v, ok := st.Attrs[key]; ok {
				a.Val = v
				out = append(out, a)
				done[key] = true
			}
		default:
			out = append(out, a)
		}
	}
	if !hasClass && st.Class != "" {
		out = append(out, html.Attribute{Key: "class", Val: st.Class})
	}
	keys := make([]string, 0, len(st.Attrs))
	for k := range st.Attrs {
		if !done[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, html.Attribute{Key: k, Val: st.Attrs[k]})
	}
	// Attributes in name order stay so (the HTML parser sorts those of <a> and the other
	// formatting elements): a new one goes to its place, as the import would have written it.
	sorted := sort.SliceIsSorted(attrs, func(i, j int) bool { return attrs[i].Key < attrs[j].Key })
	if sorted && len(keys) > 0 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	}
	return out
}

// ApplyActive marks the elements of a part's published markup that the rule gives the requested
// page (current: a site path, see ActivePath). Only the opening tags it changes are rewritten; the
// rest of the markup is copied as it is.
func ApplyActive(src string, rule *ActiveRule, current string) string {
	if rule == nil || len(rule.Items) == 0 || current == "" {
		return src
	}
	current = ActivePath(current)
	byKey := make(map[string][]int, len(rule.Items))
	// exactRole: roles some of whose links are marked on the page they link to. A link of such a
	// role that the rule does not know (a menu item added after the import, bound to a Pursae menu)
	// is marked the same way on its own page.
	exactRole := map[int]bool{}
	for i, it := range rule.Items {
		k := strconv.Itoa(it.Role) + "\x00" + it.Key
		byKey[k] = append(byKey[k], i)
		if it.Exact && strings.HasPrefix(it.Key, "href:") {
			exactRole[it.Role] = true
		}
	}
	var out bytes.Buffer
	z := html.NewTokenizer(strings.NewReader(src))
	ordinal := map[string]int{} // tag + plain state → elements seen
	changed := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := z.Raw()
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		tok := z.Token()
		var st ActiveState
		var href, controls, id string
		for _, a := range tok.Attr {
			switch {
			case a.Key == "class":
				st.Class = a.Val
			case activeStateAttrs[a.Key]:
				if st.Attrs == nil {
					st.Attrs = map[string]string{}
				}
				st.Attrs[a.Key] = a.Val
			case a.Key == "href":
				href = a.Val
			case a.Key == "aria-controls":
				controls = a.Val
			case a.Key == "id":
				id = a.Val
			}
		}
		var target *ActiveState
		counted := false
		for ri := range rule.Roles {
			r := &rule.Roles[ri]
			if r.Tag != tok.Data || r.Base.key() != st.key() {
				continue
			}
			group := r.Tag + "\x00" + r.Base.key()
			ord := ordinal[group]
			if !counted {
				ordinal[group]++
				counted = true
			}
			hp := ""
			key := ""
			switch {
			case href != "" && (tok.Data == "a" || tok.Data == "area") && ActivePath(href) != "":
				hp = ActivePath(href)
				key = "href:" + hp
			case controls != "":
				key = "controls:" + controls
			case id != "":
				key = "id:" + id
			default:
				key = "#" + strconv.Itoa(ord)
			}
			for _, ii := range byKey[strconv.Itoa(ri)+"\x00"+key] {
				it := rule.Items[ii]
				if it.Exact && hp != "" && hp == current {
					target = r.exactState()
				} else if activeNear(it, current) {
					target = r.nearState()
				}
				if target != nil {
					break
				}
			}
			if target == nil && hp != "" && hp == current && exactRole[ri] && len(byKey[strconv.Itoa(ri)+"\x00"+key]) == 0 {
				target = r.exactState()
			}
			if target != nil {
				break
			}
		}
		if target == nil {
			out.Write(raw)
			continue
		}
		tok.Attr = stateAttrs(tok.Attr, *target)
		out.WriteString(tok.String())
		changed = true
	}
	if !changed {
		return src
	}
	return out.String()
}

func activeNear(it ActiveItem, current string) bool {
	for _, p := range it.Paths {
		if p == current {
			return true
		}
	}
	for _, d := range it.Prefixes {
		if strings.HasPrefix(current, d) && current != d {
			return true
		}
	}
	return false
}

func (r *ActiveRole) exactState() *ActiveState {
	if r.Exact != nil {
		return r.Exact
	}
	return r.Near
}

func (r *ActiveRole) nearState() *ActiveState {
	if r.Near != nil {
		return r.Near
	}
	if r.Exact == nil {
		return nil
	}
	st := ActiveState{Class: r.Exact.Class}
	for k, v := range r.Exact.Attrs {
		if k != "aria-current" {
			if st.Attrs == nil {
				st.Attrs = map[string]string{}
			}
			st.Attrs[k] = v
		}
	}
	return &st
}
