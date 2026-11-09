package webbuilder

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeResolver resolves from maps; items are child resolvers that fall back to the parent.
type fakeResolver struct {
	parent *fakeResolver
	text   map[SlotName]string
	html   map[SlotName]string
	attrs  map[SlotName]map[string]string
	loops  map[string][]*fakeResolver
	menus  map[string][]*fakeResolver
	args   []SlotArgs // every SlotArgs seen (shared with the root)
	calls  *[]string
}

func (f *fakeResolver) root() *fakeResolver {
	for f.parent != nil {
		f = f.parent
	}
	return f
}

func (f *fakeResolver) log(s string) {
	if r := f.root(); r.calls != nil {
		*r.calls = append(*r.calls, s)
	}
}

func (f *fakeResolver) Text(slot SlotName, a SlotArgs) (string, bool) {
	f.log("text:" + string(slot))
	f.root().args = append(f.root().args, a)
	for r := f; r != nil; r = r.parent {
		if v, ok := r.text[slot]; ok {
			return v, true
		}
	}
	return "", false
}

func (f *fakeResolver) HTML(slot SlotName, a SlotArgs) (string, bool) {
	f.log("html:" + string(slot))
	for r := f; r != nil; r = r.parent {
		if v, ok := r.html[slot]; ok {
			return v, true
		}
	}
	return "", false
}

func (f *fakeResolver) Attrs(slot SlotName, a SlotArgs) (map[string]string, bool) {
	f.log("attrs:" + string(slot))
	for r := f; r != nil; r = r.parent {
		if v, ok := r.attrs[slot]; ok {
			return v, true
		}
	}
	return nil, false
}

func (f *fakeResolver) iter(items []*fakeResolver) LoopIter {
	rs := make([]Resolver, len(items))
	for i, it := range items {
		it.parent = f
		rs[i] = it
	}
	return &SliceIter{Items: rs}
}

func (f *fakeResolver) Loop(id string, a SlotArgs) (LoopIter, bool) {
	f.log("loop:" + id)
	items, ok := f.root().loops[id]
	if !ok {
		return nil, false
	}
	return f.iter(items), true
}

func (f *fakeResolver) Menu(loc string, a SlotArgs) (LoopIter, bool) {
	f.log("menu:" + loc)
	items, ok := f.root().menus[loc]
	if !ok {
		return nil, false
	}
	return f.iter(items), true
}

func run(t *testing.T, src string, r Resolver) string {
	t.Helper()
	p, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.ExecString(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDynamicStaticFastPath(t *testing.T) {
	for _, src := range []string{"", goodSnapshot, `<div class="wb-page" data-wb-root=""><p data-wb-motion="fade">x</p></div>`, `<p>data-wb-slot in text</p>`} {
		p, err := Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		if p.HasDynamic {
			t.Errorf("dynamic: %q", src)
		}
		got, ok := p.Static()
		if !ok || got != src {
			t.Errorf("static text changed: %q → %q", src, got)
		}
		out, _ := p.ExecString(nil)
		if out != src {
			t.Errorf("exec changed %q → %q", src, out)
		}
	}
	// A marker only on SVG content or in attribute values stays static and byte-identical.
	src := `<div title="data-wb-slot"><svg viewBox="0 0 1 1"><path data-wb-slot="post.title" d="M0 0"></path></svg></div>`
	p, _ := Compile(src)
	if p.HasDynamic {
		t.Error("svg marker made it dynamic")
	}
	if out, _ := p.ExecString(nil); out != src {
		t.Errorf("got %q", out)
	}
}

func TestDynamicSlots(t *testing.T) {
	r := &fakeResolver{
		text: map[SlotName]string{
			SlotSiteTitle: `Tom & "Jerry" <script>alert(1)</script>`,
			SlotPostDate:  "June 1, 2026",
			SlotSiteYear:  "2026",
		},
		html: map[SlotName]string{SlotPostTitle: "<em>Hello</em> world", SlotPostContent: "<p>Body</p>"},
		attrs: map[SlotName]map[string]string{
			SlotSiteURL:  {"href": "https://example.com/"},
			SlotPostDate: {"datetime": "2026-06-01T10:00:00Z"},
			SlotSiteLogo: {"src": "/wp-content/uploads/logo.png", "width": "120", "height": "40", "alt": `Logo "x"`, "onerror": "alert(1)", "style": "x"},
		},
	}
	src := `<div class="wb-page" data-wb-root="">` +
		`<h1 data-wb-slot="site.title">Sample title</h1>` +
		`<h2 data-wb-slot="post.title"><b>sample</b></h2>` +
		`<time data-wb-slot="post.date" data-wb-format="F j, Y">sample</time>` +
		`<a class="x" data-wb-slot="site.url" href="#"><span>Home</span> <span data-wb-slot="site.year"></span></a>` +
		`<img data-wb-slot="site.logo" alt="">` +
		`<p data-wb-slot="no.such">placeholder</p>` +
		`<div data-wb-slot="post.content"></div>` +
		`</div>`
	out := run(t, src, r)
	for _, want := range []string{
		`<h1 data-wb-slot="site.title">Tom &amp; &#34;Jerry&#34; &lt;script&gt;alert(1)&lt;/script&gt;</h1>`,
		`<h2 data-wb-slot="post.title"><em>Hello</em> world</h2>`,
		`<time data-wb-slot="post.date" data-wb-format="F j, Y" datetime="2026-06-01T10:00:00Z">June 1, 2026</time>`,
		`<a class="x" data-wb-slot="site.url" href="https://example.com/"><span>Home</span> <span data-wb-slot="site.year">2026</span></a>`,
		`<img data-wb-slot="site.logo" alt="Logo &#34;x&#34;" src="/wp-content/uploads/logo.png" width="120" height="40"/>`,
		`<p data-wb-slot="no.such"></p>`,
		`<div data-wb-slot="post.content"><p>Body</p></div>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin %s", want, out)
		}
	}
	for _, bad := range []string{"onerror", "style=", "Sample title", "<b>sample</b>", "placeholder"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q in %s", bad, out)
		}
	}
}

func TestDynamicAttrSafety(t *testing.T) {
	r := &fakeResolver{attrs: map[SlotName]map[string]string{
		SlotPostURL:   {"href": "javascript:alert(1)", "class": `ok bad"x is-current`},
		SlotPostImage: {"src": "javascript:x", "srcset": "javascript:x 1x", "width": "100;x"},
		SlotMenuURL:   {"href": "//evil.example/", "aria-current": "page\" onclick=\"x"},
	}}
	src := `<div><a data-wb-slot="post.url" href="#" class="card">t</a><img data-wb-slot="post.image"><a data-wb-slot="menu.url" href="/x">m</a></div>`
	out := run(t, src, r)
	want := `<div><a class="card ok is-current" data-wb-slot="post.url" href="#">t</a><img data-wb-slot="post.image"/><a data-wb-slot="menu.url" href="/x">m</a></div>`
	if out != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	// An image slot without a value drops the image.
	out = run(t, `<div><img data-wb-slot="post.image" alt=""><img data-wb-slot="no.such"></div>`, &fakeResolver{})
	if out != `<div><img data-wb-slot="no.such"/></div>` {
		t.Fatalf("got %s", out)
	}
}

// Image slots write src/srcset/sizes only on images: an iframe keeps the src the sanitizer vetted.
func TestDynamicImageSlotOnIframe(t *testing.T) {
	r := &fakeResolver{attrs: map[SlotName]map[string]string{
		SlotPostImage: {"src": "/wp-content/uploads/file.pdf", "srcset": "/wp-content/uploads/a.png 1x", "sizes": "100vw", "alt": "x"},
	}}
	out := run(t, `<div><iframe src="https://www.youtube.com/embed/x" data-wb-slot="post.image"></iframe></div>`, r)
	if strings.Contains(out, "file.pdf") || strings.Contains(out, "srcset") || strings.Contains(out, "sizes") || !strings.Contains(out, `src="https://www.youtube.com/embed/x"`) {
		t.Errorf("iframe got image attributes: %s", out)
	}
	out = run(t, `<div><img data-wb-slot="post.image"></div>`, r)
	if !strings.Contains(out, `src="/wp-content/uploads/file.pdf"`) {
		t.Errorf("img lost its src: %s", out)
	}
}

func TestDynamicConditions(t *testing.T) {
	r := &fakeResolver{text: map[SlotName]string{SlotSiteTagline: "Just another site", SlotSearchQuery: ""}}
	src := `<div><p data-wb-if="site.tagline">has</p><p data-wb-if-not="site.tagline">none</p>` +
		`<p data-wb-if="search.query">q</p><p data-wb-if-not="search.query">no query</p>` +
		`<p data-wb-if="bogus">bogus-if</p><p data-wb-if-not="bogus">bogus-ifnot</p>` +
		`<p data-wb-if="site.tagline" data-wb-if-not="site.tagline">never</p></div>`
	out := run(t, src, r)
	want := `<div><p data-wb-if="site.tagline">has</p><p data-wb-if-not="search.query">no query</p><p data-wb-if-not="bogus">bogus-ifnot</p></div>`
	if out != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

func card(title, url string) *fakeResolver {
	return &fakeResolver{
		html:  map[SlotName]string{SlotPostTitle: title},
		attrs: map[SlotName]map[string]string{SlotPostURL: {"href": url}},
	}
}

func TestDynamicLoop(t *testing.T) {
	r := &fakeResolver{
		text: map[SlotName]string{SlotSiteTitle: "Site"},
		loops: map[string][]*fakeResolver{
			"posts": {card("One", "/one/"), card("Two", "/two/")},
			"none":  {},
		},
	}
	src := `<div>` +
		`<ul data-wb-loop="posts" class="grid"><li data-wb-loop-item=""><a data-wb-slot="post.url"><span data-wb-slot="post.title">Sample</span></a> <i data-wb-slot="site.title"></i></li><li data-wb-loop-item="">second template</li><li data-wb-loop-empty="">Nothing</li></ul>` +
		`<ul data-wb-loop="none"><li data-wb-loop-item=""><span data-wb-slot="post.title"></span></li><li data-wb-loop-empty="">Nothing yet</li></ul>` +
		`<ul data-wb-loop="missing"><li data-wb-loop-item="">x</li></ul>` +
		`<ul data-wb-loop="bad id!"><li data-wb-loop-item="">x</li><li data-wb-loop-empty="">e</li></ul>` +
		`</div>`
	out := run(t, src, r)
	want := `<div>` +
		`<ul data-wb-loop="posts" class="grid"><li data-wb-loop-item=""><a data-wb-slot="post.url" href="/one/"><span data-wb-slot="post.title">One</span></a> <i data-wb-slot="site.title">Site</i></li><li data-wb-loop-item=""><a data-wb-slot="post.url" href="/two/"><span data-wb-slot="post.title">Two</span></a> <i data-wb-slot="site.title">Site</i></li></ul>` +
		`<ul data-wb-loop="none"><li data-wb-loop-empty="">Nothing yet</li></ul>` +
		`<ul data-wb-loop="missing"></ul>` +
		`<ul data-wb-loop="bad id!"><li data-wb-loop-empty="">e</li></ul>` +
		`</div>`
	if out != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	p, _ := Compile(src)
	if strings.Join(p.Loops, ",") != "posts,none,missing" {
		t.Errorf("Loops = %v", p.Loops)
	}
	if !p.Uses.Has(SlotPostTitle) || !p.Uses.Has(SlotPostURL) || p.Uses.Has(SlotPostContent) {
		t.Errorf("Uses = %v", p.Uses)
	}
}

func TestDynamicLoopLimits(t *testing.T) {
	var many []*fakeResolver
	for i := 0; i < 80; i++ {
		many = append(many, card(fmt.Sprint(i), "/p/"))
	}
	r := &fakeResolver{loops: map[string][]*fakeResolver{"a": many}}
	out := run(t, `<ul data-wb-loop="a"><li data-wb-loop-item=""><b data-wb-slot="post.title"></b></li></ul>`, r)
	if n := strings.Count(out, "<li"); n != MaxLoopIterations {
		t.Errorf("%d items, want %d", n, MaxLoopIterations)
	}
	// Depth: a loop in a loop in a loop renders the third level without items.
	r = &fakeResolver{loops: map[string][]*fakeResolver{"a": {card("A", "/")}, "b": {card("B", "/")}, "c": {card("C", "/")}}}
	src := `<div data-wb-loop="a"><div data-wb-loop-item=""><b data-wb-slot="post.title"></b>` +
		`<div data-wb-loop="b"><div data-wb-loop-item=""><i data-wb-slot="post.title"></i>` +
		`<div data-wb-loop="c"><div data-wb-loop-item=""><u data-wb-slot="post.title"></u></div><p data-wb-loop-empty="">deep</p></div>` +
		`</div></div></div></div>`
	out = run(t, src, r)
	if !strings.Contains(out, "<b data-wb-slot=\"post.title\">A</b>") || !strings.Contains(out, "<i data-wb-slot=\"post.title\">B</i>") {
		t.Errorf("two levels expected: %s", out)
	}
	if strings.Contains(out, ">C<") || !strings.Contains(out, "deep") {
		t.Errorf("third level rendered: %s", out)
	}
	// Menus count toward the depth too.
	r = &fakeResolver{loops: map[string][]*fakeResolver{"a": {card("A", "/")}, "b": {card("B", "/")}}, menus: map[string][]*fakeResolver{"primary": {{text: map[SlotName]string{SlotMenuLabel: "Home"}}}}}
	out = run(t, `<div data-wb-loop="a"><div data-wb-loop-item=""><div data-wb-loop="b"><div data-wb-loop-item=""><ul data-wb-menu-loc="primary"><li data-wb-loop-item=""><span data-wb-slot="menu.label"></span></li></ul></div></div></div></div>`, r)
	if strings.Contains(out, "Home") {
		t.Errorf("menu at depth 3 rendered: %s", out)
	}
}

func TestDynamicMenu(t *testing.T) {
	item := func(label, url string, cur bool) *fakeResolver {
		a := map[string]string{"href": url}
		if cur {
			a["aria-current"], a["class"] = "page", ClassCurrent
		}
		return &fakeResolver{text: map[SlotName]string{SlotMenuLabel: label}, attrs: map[SlotName]map[string]string{SlotMenuURL: a}}
	}
	r := &fakeResolver{menus: map[string][]*fakeResolver{"primary": {
		item(`A&B`, "/a/", false), item("<b>", "/b/", true), item("C", "javascript:x", false),
	}}}
	tpl := `<li data-wb-loop-item=""><a data-wb-slot="menu.url" class="l"><span data-wb-slot="menu.label">Sample</span></a></li>`
	out := run(t, `<nav><ul data-wb-menu-loc="primary">`+tpl+`</ul></nav>`, r)
	want := `<nav><ul data-wb-menu-loc="primary">` +
		`<li data-wb-loop-item=""><a class="l" data-wb-slot="menu.url" href="/a/"><span data-wb-slot="menu.label">A&amp;B</span></a></li>` +
		`<li data-wb-loop-item=""><a class="l is-current" data-wb-slot="menu.url" href="/b/" aria-current="page"><span data-wb-slot="menu.label">&lt;b&gt;</span></a></li>` +
		`<li data-wb-loop-item=""><a class="l" data-wb-slot="menu.url"><span data-wb-slot="menu.label">C</span></a></li>` +
		`</ul></nav>`
	if out != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	// Split-logo navs: 1/2 gets the first ceil(n/2) items, 2/2 the rest.
	out = run(t, `<div><ul data-wb-menu-loc="primary" data-wb-loop-part="1/2">`+tpl+`</ul><ul data-wb-menu-loc="primary" data-wb-loop-part="2/2">`+tpl+`</ul></div>`, r)
	halves := strings.Split(out, "</ul>")
	if strings.Count(halves[0], "<li") != 2 || strings.Count(halves[1], "<li") != 1 {
		t.Fatalf("split wrong: %s", out)
	}
	p, _ := Compile(`<ul data-wb-menu-loc="primary">` + tpl + `</ul><ul data-wb-menu-loc="bad loc">` + tpl + `</ul>`)
	if strings.Join(p.Menus, ",") != "primary" {
		t.Errorf("Menus = %v", p.Menus)
	}
}

func TestDynamicSlotArgs(t *testing.T) {
	r := &fakeResolver{}
	run(t, `<div><span data-wb-slot="post.date" data-wb-format="j F Y" data-wb-size="large" data-wb-tax="post_tag" data-wb-sep=" · " data-wb-words="20" data-wb-item="c-1_x"></span>`+
		`<span data-wb-slot="post.date" data-wb-format="&lt;script&gt;" data-wb-size="huge" data-wb-tax="Bad Tax" data-wb-sep="123456789" data-wb-words="500" data-wb-item="bad id!"></span></div>`, r)
	if len(r.args) != 2 {
		t.Fatalf("args %v", r.args)
	}
	if got := r.args[0]; got != (SlotArgs{Tag: "span", Format: "j F Y", Size: "large", Tax: "post_tag", Sep: " · ", Words: 20, Item: "c-1_x"}) {
		t.Errorf("valid args %+v", got)
	}
	if got := r.args[1]; got != (SlotArgs{Tag: "span"}) {
		t.Errorf("invalid args leaked %+v", got)
	}
}

// The engine never asks for a slot the snapshot does not use, nor for unknown slots.
func TestDynamicResolverCalls(t *testing.T) {
	var calls []string
	r := &fakeResolver{calls: &calls}
	run(t, `<div><p data-wb-slot="site.title"></p><p data-wb-slot="x.y"></p><p data-wb-if="z"></p></div>`, r)
	if strings.Join(calls, ",") != "text:site.title" {
		t.Errorf("calls %v", calls)
	}
}

// A slot inside a replaced element is not compiled (its content is replaced anyway).
func TestDynamicNestedReplaced(t *testing.T) {
	var calls []string
	r := &fakeResolver{calls: &calls, html: map[SlotName]string{SlotPostTitle: "T"}}
	out := run(t, `<h1 data-wb-slot="post.title"><span data-wb-slot="post.content"></span></h1>`, r)
	if out != `<h1 data-wb-slot="post.title">T</h1>` || strings.Join(calls, ",") != "html:post.title" {
		t.Errorf("out %s calls %v", out, calls)
	}
}

func TestDynamicPreserveStaticParts(t *testing.T) {
	src := `<div class="wb-page" data-wb-root=""><section class="sf"><p style="color:red">Static &amp; text<br/></p><svg viewBox="0 0 24 24"><path d="M0 0"></path></svg><pre>

code</pre></section><p data-wb-slot="site.title"></p></div>`
	out := run(t, src, &fakeResolver{text: map[SlotName]string{SlotSiteTitle: "S"}})
	want := strings.Replace(src, `<p data-wb-slot="site.title"></p>`, `<p data-wb-slot="site.title">S</p>`, 1)
	if out != want {
		t.Fatalf("got  %q\nwant %q", out, want)
	}
	// Leading newline inside a dynamic <pre> survives.
	out = run(t, "<pre data-wb-if-not=\"x.y\">\n\nline</pre>", nil)
	if out != "<pre data-wb-if-not=\"x.y\">\n\nline</pre>" {
		t.Fatalf("pre: %q", out)
	}
}

func TestDynamicTruncation(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	mk := func() []*fakeResolver {
		var many []*fakeResolver
		for i := 0; i < MaxLoopIterations; i++ {
			many = append(many, &fakeResolver{html: map[SlotName]string{SlotPostContent: big}})
		}
		return many
	}
	r := &fakeResolver{loops: map[string][]*fakeResolver{"a": mk(), "b": mk()}}
	p, _ := Compile(`<div data-wb-loop="a"><div data-wb-loop-item=""><div data-wb-loop="b"><p data-wb-loop-item="" data-wb-slot="post.content"></p></div></div></div>`)
	var b strings.Builder
	err := p.Exec(&b, r)
	if err != ErrExecTruncated {
		t.Fatalf("err %v", err)
	}
	if b.Len() > MaxExecBytes+2<<20 {
		t.Errorf("output %d bytes", b.Len())
	}
	if !strings.HasSuffix(b.String(), "</div></div></div>") {
		t.Error("output not closed")
	}
}

func TestProgramCache(t *testing.T) {
	c := NewProgramCache(2)
	dyn := func(i int) string { return fmt.Sprintf(`<p data-wb-slot="site.title">%d</p>`, i) }
	p1, _ := c.Compile(1, 1, dyn(1))
	again, _ := c.Compile(1, 1, dyn(1))
	if p1 != again {
		t.Error("cache miss on the same key")
	}
	if c.Len() != 1 {
		t.Errorf("len %d", c.Len())
	}
	// Static snapshots are not cached.
	if p, _ := c.Compile(9, 1, "<p>x</p>"); p.HasDynamic || c.Len() != 1 {
		t.Error("static program cached")
	}
	c.Compile(1, 2, dyn(1)) // new rev = new key
	c.Compile(2, 1, dyn(2)) // evicts 1:1
	if c.Len() != 2 {
		t.Errorf("len %d", c.Len())
	}
	if p, _ := c.Compile(1, 1, dyn(1)); p == p1 {
		t.Error("LRU did not evict the oldest entry")
	}
	if k := ProgramKey(42, 7, "x"); !strings.HasPrefix(k, "42:7:") || len(k) != len("42:7:")+16 {
		t.Errorf("key %q", k)
	}
	c.Purge()
	if c.Len() != 0 {
		t.Error("purge")
	}
	// Concurrent use.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				p, err := c.Compile(int64(j%5), 1, dyn(j%5))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = p.ExecString(&fakeResolver{text: map[SlotName]string{SlotSiteTitle: "x"}})
			}
		}(i)
	}
	wg.Wait()
}

// Sanitized snapshot → Compile → Exec keeps the snapshot safe: whatever text a resolver
// returns, text slots cannot add markup.
func TestDynamicOverSanitizedSnapshot(t *testing.T) {
	in := wrapRoot(`<h1 data-wb-slot="post.title" onclick="x">t</h1><ul data-wb-loop="s1"><li data-wb-loop-item=""><a data-wb-slot="post.url" href="javascript:x"><span data-wb-slot="site.title"></span></a></li></ul>`)
	out, _, err := SanitizeSnapshot(in, SnapshotOptions{FormActionPrefix: testFormPrefix})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{
		text:  map[SlotName]string{SlotSiteTitle: `<img src=x onerror=alert(1)>`},
		html:  map[SlotName]string{SlotPostTitle: "Title"},
		loops: map[string][]*fakeResolver{"s1": {card("a", "/a/")}},
	}
	got := run(t, out, r)
	if strings.Contains(got, "<img") || strings.Contains(got, "onclick") || strings.Contains(got, "javascript:") {
		t.Fatalf("unsafe: %s", got)
	}
	assertSafe(t, got, SnapshotOptions{FormActionPrefix: testFormPrefix})
}
