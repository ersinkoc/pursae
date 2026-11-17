package webbuilder

import (
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// fakeMenu is a menu item resolver for the engine tests.
type fakeMenu struct {
	id       string
	label    string
	url      string
	target   string
	children []*fakeMenu
}

type fakeRes struct {
	menu  map[string][]*fakeMenu
	item  *fakeMenu
	posts []map[string]string
	post  map[string]string
}

func (r *fakeRes) Text(slot SlotName, a SlotArgs) (string, bool) {
	switch slot {
	case SlotMenuLabel:
		if r.item != nil {
			return r.item.label, true
		}
	case SlotPostTitleText, SlotPostDate, SlotPostTermNames, SlotPostField:
		if r.post != nil {
			v := r.post[string(slot)+a.Field]
			return v, v != ""
		}
	}
	return "", false
}

func (r *fakeRes) HTML(slot SlotName, a SlotArgs) (string, bool) {
	if r.post != nil && (slot == SlotPostContent || slot == SlotPostExcerpt) {
		v := r.post[string(slot)]
		return v, v != ""
	}
	return "", false
}

func (r *fakeRes) Attrs(slot SlotName, a SlotArgs) (map[string]string, bool) {
	switch slot {
	case SlotMenuLink:
		if r.item != nil {
			m := map[string]string{"href": r.item.url}
			if r.item.target != "" {
				m["target"] = r.item.target
			}
			return m, true
		}
	case SlotPostURL:
		if r.post != nil {
			return map[string]string{"href": r.post["url"]}, true
		}
	case SlotPostImage:
		if r.post != nil {
			return map[string]string{"src": r.post["img"], "alt": r.post["alt"]}, true
		}
	case SlotPostFieldAttrs:
		if r.post != nil {
			return map[string]string{"href": r.post[a.Field+"_href"]}, true
		}
	}
	return nil, false
}

func (r *fakeRes) Loop(id string, _ SlotArgs) (LoopIter, bool) {
	var items []Resolver
	for _, p := range r.posts {
		items = append(items, &fakeRes{post: p})
	}
	return &SliceIter{Items: items}, true
}

func (r *fakeRes) Menu(loc string, _ SlotArgs) (LoopIter, bool) {
	var items []Resolver
	for _, m := range r.menu[loc] {
		items = append(items, &fakeRes{item: m})
	}
	return &SliceIter{Items: items}, true
}

func (r *fakeRes) ItemKey() string {
	if r.item == nil {
		return ""
	}
	return "k:" + r.item.id
}

func (r *fakeRes) HasChildren() bool { return r.item != nil && len(r.item.children) > 0 }

func (r *fakeRes) SubMenu() (LoopIter, bool) {
	if r.item == nil {
		return nil, false
	}
	var items []Resolver
	for _, m := range r.item.children {
		items = append(items, &fakeRes{item: m})
	}
	return &SliceIter{Items: items}, true
}

func fill(t *testing.T, src string, r Resolver) string {
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

const menuSrc = `<nav data-wbc="0"><ul data-wbc="1" class="flex">
<li data-wbc="2"><a data-wbc="3" href="/a/" class="x">Home</a></li>
<li data-wbc="4" class="relative"><button data-wbc="5" type="button">Docs<svg data-wbc="6" viewBox="0 0 2 2"><path d="M0 0"/></svg></button><div data-wbc="7" class="panel"><ul data-wbc="8"><li data-wbc="9"><a data-wbc="10" href="https://x.test/" target="_blank" class="sub">Law<svg data-wbc="11"></svg></a></li></ul></div></li>
<li data-wbc="12"><a data-wbc="13" href="/c/" class="x cta">Contact</a></li>
</ul></nav>`

func TestCompileBindsMenu(t *testing.T) {
	binds := ParseCodeBinds(`{"1":{"kind":"menu","location":"primary","items":{"100":"2","101":"4","102":"9","105":"12"}},"8":{"kind":"submenu"}}`)
	if len(binds) != 2 {
		t.Fatalf("binds: %v", binds)
	}
	out := CompileBinds(menuSrc, "s1", binds, CodeFillOptions{})
	for _, want := range []string{
		`<ul data-wbc="1" class="flex" data-wb-menu-loc="primary" data-wb-menu-tree="">`,
		`<li data-wbc="2" data-wb-loop-item="default k:100">`,
		`<a data-wbc="3" href="/a/" class="x" data-wb-slot="menu.link" data-wb-text="menu.label">Home</a>`,
		`<li data-wbc="4" class="relative" data-wb-loop-item="parent k:101">`,
		`<button data-wbc="5" type="button" data-wb-text="menu.label">Docs<svg`,
		`<ul data-wbc="8" data-wb-menu-sub="">`,
		`<li data-wbc="9" data-wb-loop-item="default k:102">`,
		`<li data-wbc="12" data-wb-loop-item="k:105">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compiled markup lacks %s\n%s", want, out)
		}
	}
	// The rest is byte for byte the source.
	if StripCodeIDs(removeAttrs(out)) != StripCodeIDs(menuSrc) {
		t.Errorf("markup outside the bound tags changed:\n%s", out)
	}
	r := &fakeRes{menu: map[string][]*fakeMenu{"primary": {
		{id: "100", label: "Home", url: "/"},
		{id: "101", label: "Docs & more", url: "#", children: []*fakeMenu{{id: "102", label: "Laws", url: "https://y.test/", target: "_blank"}, {id: "103", label: "New sub", url: "/n/"}}},
		{id: "105", label: "Contact", url: "/c/"},
		{id: "106", label: "Blog", url: "/blog/"},
	}}}
	got := StripCodeIDs(removeAttrs(fill(t, out, r)))
	for _, want := range []string{
		`<li><a class="x" href="/">Home</a></li>`,
		`<li class="relative"><button type="button">Docs &amp; more<svg`,
		`<ul><li><a class="sub" href="https://y.test/" target="_blank">Laws<svg></svg></a></li><li><a class="sub" href="/n/" target="_blank">New sub<svg></svg></a></li></ul>`,
		`<li><a class="x cta" href="/c/">Contact</a></li>`,
		`<li><a class="x" href="/blog/">Blog</a></li>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("filled menu lacks %s\n%s", want, got)
		}
	}
	if strings.Count(got, "<li") != 6 {
		t.Errorf("want 6 items (4 + 2 sub):\n%s", got)
	}
}

// removeAttrs drops the binding attributes the compiler adds (for comparisons with the source).
func removeAttrs(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return b.String()
		}
		raw := string(z.Raw())
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			for _, a := range []string{AttrMenuLoc, AttrMenuTree, AttrLoopItem, AttrSlot, AttrText, AttrMenuSub, AttrLoop, AttrRaw, AttrField, AttrMeta, AttrFormat} {
				for {
					i := strings.Index(raw, " "+a+`="`)
					if i < 0 {
						break
					}
					j := strings.Index(raw[i+len(a)+3:], `"`)
					raw = raw[:i] + raw[i+len(a)+3+j+1:]
				}
			}
		}
		b.WriteString(raw)
	}
}

func TestCompileBindsLoopAndFields(t *testing.T) {
	src := `<section data-wbc="1"><ul data-wbc="2" class="grid">` +
		`<li data-wbc="3" style="animation-delay:0ms"><a data-wbc="4" href="/n/1/"><img data-wbc="5" src="/k/1.jpg" alt="One"><span data-wbc="6"><svg data-wbc="7"></svg>27.01.2020</span><h3 data-wbc="8">One</h3><p data-wbc="9">Lead one</p></a></li>` +
		`<li data-wbc="10" style="animation-delay:50ms"><a data-wbc="11" href="/n/2/">Two</a></li>` +
		`</ul></section>`
	binds := ParseCodeBinds(`{"2":{"kind":"loop","query":{"postType":"page","parent":"7","orderBy":"menu_order","order":"asc","perPage":"12"}},` +
		`"4":{"kind":"field","field":"url"},"5":{"kind":"field","field":"image"},"6":{"kind":"field","field":"date","format":"d.m.Y"},` +
		`"8":{"kind":"field","field":"title"},"9":{"kind":"field","field":"excerpt"},"11":{"kind":"field","field":"title"}}`)
	out := CompileBinds(src, "sec", binds, CodeFillOptions{})
	if strings.Contains(out, "Two") {
		t.Errorf("sample cards after the template stay on the page:\n%s", out)
	}
	for _, want := range []string{`data-wb-loop="sec-w2"`, `<li data-wbc="3" style="animation-delay:0ms" data-wb-loop-item="">`,
		`<img data-wbc="5" src="/k/1.jpg" alt="One" data-wb-slot="post.image" data-wb-raw="">`,
		`<span data-wbc="6" data-wb-text="post.date" data-wb-format="d.m.Y">`, `<h3 data-wbc="8" data-wb-slot="post.title-text">`} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %s\n%s", want, out)
		}
	}
	r := &fakeRes{posts: []map[string]string{
		{"url": "/n/a/", "img": "/u/a.jpg", "alt": "A", string(SlotPostDate): "01.02.2021", string(SlotPostTitleText): "Alpha's", string(SlotPostExcerpt): "Lead a"},
		{"url": "/n/b/", "img": "/u/b.jpg", "alt": "B", string(SlotPostDate): "2021", string(SlotPostTitleText): "Beta", string(SlotPostExcerpt): "Lead b"},
	}}
	got := StripCodeIDs(fill(t, out, r))
	for _, want := range []string{`<a data-wb-slot="post.url" href="/n/a/"><img src="/u/a.jpg" alt="A" data-wb-slot="post.image" data-wb-raw=""/><span data-wb-text="post.date" data-wb-format="d.m.Y"><svg></svg>01.02.2021</span><h3 data-wb-slot="post.title-text">Alpha&#39;s</h3>`,
		`Lead b</p>`} {
		if !strings.Contains(got, want) {
			t.Errorf("filled loop lacks %s\n%s", want, got)
		}
	}
	if spec := CodeLoops(&DocState{Sections: []Section{{ID: "sec", Kind: KindCode, Content: map[string]string{ContentBinds: binds.String()}}}})["sec-w2"]; spec.Parent != 7 || spec.Source != LoopSourceQuery || spec.PostType != "page" || spec.OrderBy != "menu_order" || spec.PerPage != 12 {
		t.Errorf("loop spec: %+v", spec)
	}
}

func TestCompileBindsForm(t *testing.T) {
	src := `<div data-wbc="1"><form data-wbc="2" id="contact-form" novalidate data-mail="a@b.test"><h2 data-wbc="3">Write to us</h2>` +
		`<label data-wbc="4" for="cf-name">Name <span data-wbc="5">*</span></label><input data-wbc="6" id="cf-name" name="name" type="text">` +
		`<input data-wbc="7" name="company"><input data-wbc="8" name="email" type="email" required>` +
		`<select data-wbc="9" name="subject"><option value="" disabled selected>Pick</option><option value="A">A</option></select>` +
		`<textarea data-wbc="10" name="message"></textarea><input data-wbc="11" name="consent" type="checkbox"><button data-wbc="12" type="submit">Send</button></form>` +
		`<div data-wbc="13" id="cf-done" class="hidden">Thanks</div></div>`
	binds := `{"2":{"kind":"form","done":"13","required":["name","consent"]}}`
	out := CompileBinds(src, "s", ParseCodeBinds(binds), CodeFillOptions{FormActionPrefix: "https://x.test/wp-json/web-builder/v2/forms/9/"})
	for _, want := range []string{`<form data-wbc="2" id="contact-form" novalidate="" data-mail="a@b.test" action="https://x.test/wp-json/web-builder/v2/forms/9/s-w2" method="post" data-wb-form="" data-wb-code-form="s-w2">`,
		`name="wb_t"`, `name="wb_hp"`, `<div data-wbc="13" id="cf-done" class="hidden" data-wb-form-done="s-w2">`} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %s\n%s", want, out)
		}
	}
	d := &DocState{Sections: []Section{{ID: "s", Kind: KindCode, Content: map[string]string{"html": src, ContentBinds: binds}}}}
	fs, ok := FindForm(d, "s-w2")
	if !ok {
		t.Fatal("no schema")
	}
	got := map[string]FormField{}
	for _, f := range fs.Fields {
		got[f.Input] = f
	}
	if fs.Title != "Write to us" || len(fs.Fields) != 6 || got["name"].Label != "Name" || !got["name"].Required || got["company"].Name != "company" ||
		got["email"].Type != "email" || !got["email"].Required || got["subject"].Type != "select" || got["message"].Type != "textarea" ||
		got["consent"].Name != "field_consent" || !got["consent"].Required {
		t.Errorf("schema: %+v", fs)
	}
	clean, errs := fs.Validate(map[string]string{"name": "Ada", "email": "ada@x.test", "subject": "B", "consent": "on"})
	if errs["subject"] != FormInvalid || len(errs) != 1 || clean["field_consent"] != "on" {
		t.Errorf("validate: %v %v", clean, errs)
	}
}

func TestCompileSplicedCodeSnapshot(t *testing.T) {
	src := `<div class="wb-page" data-wb-root="" data-wb-code-page="" style="display:contents"><template data-wb-code-head><link rel=stylesheet href="x.css"></template>` +
		`<div hidden><p>static &amp; <b>text</b></p></div>` + "\n" +
		`<ul data-wb-menu-loc="primary" data-wb-menu-tree="">` + "\n" + `<li data-wb-loop-item="default"><a href="/" data-wb-slot="menu.link" data-wb-text="menu.label">x</a></li>` + "\n" + `</ul></div>`
	r := &fakeRes{menu: map[string][]*fakeMenu{"primary": {{id: "1", label: "A", url: "/a/"}, {id: "2", label: "B", url: "/b/"}}}}
	got := fill(t, src, r)
	for _, want := range []string{`<template data-wb-code-head><link rel=stylesheet href="x.css"></template><div hidden><p>static &amp; <b>text</b></p></div>`,
		`href="/a/">A</a></li>` + "\n<li", `href="/b/">B</a></li>` + "\n</ul></div>"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q\n%s", want, got)
		}
	}
}

func TestApplyActiveNewLink(t *testing.T) {
	rule := &ActiveRule{Roles: []ActiveRole{{Tag: "a", Base: ActiveState{Class: "nav off"}, Exact: &ActiveState{Class: "nav on", Attrs: map[string]string{"aria-current": "page"}}}},
		Items: []ActiveItem{{Role: 0, Key: "href:/a/", Exact: true}}}
	src := `<a class="nav off" href="/a/">A</a><a class="nav off" href="/new/">New</a>`
	got := ApplyActive(src, rule, "/new/")
	if !strings.Contains(got, `nav on`) || !strings.Contains(got, `aria-current="page"`) || strings.Count(got, "nav on") != 1 || !strings.Contains(got, `<a class="nav off" href="/a/">`) {
		t.Errorf("a new link to the current page is not marked: %s", got)
	}
	if strings.Contains(ApplyActive(src, rule, "/other/"), "nav on") {
		t.Errorf("marked on another page")
	}
}

func TestCheckCodeBinds(t *testing.T) {
	for _, bad := range []string{`[]`, `{"x":{"kind":"menu","location":"primary"}}`, `{"1":{"kind":"menu"}}`, `{"1":{"kind":"field","field":"nope"}}`,
		`{"1":{"kind":"field","field":"custom"}}`, `{"1":{"kind":"loop","query":{"evil":"1"}}}`, `{"1":{"kind":"form","fields":{"a":"password"}}}`} {
		if len(CheckCodeBinds(bad)) == 0 {
			t.Errorf("%s passed", bad)
		}
	}
	for _, good := range []string{``, `{"1":{"kind":"menu","menu":4,"items":{"9":"2"}}}`, `{"1":{"kind":"field","field":"custom","name":"design_1","attr":"attrs","text":true}}`,
		`{"1":{"kind":"form","fields":{"consent":"field_consent","x":"-"}}}`} {
		if m := CheckCodeBinds(good); len(m) != 0 {
			t.Errorf("%s: %v", good, m)
		}
	}
	if s, w, ok := SplitBindKey("abc-w-w12"); !ok || s != "abc-w" || w != "12" {
		t.Errorf("split: %q %q %v", s, w, ok)
	}
}

// A custom field binding (and an image binding's fallback URL field) marks its element with
// data-wb-meta, the only way a post's own meta is read; the snapshot sanitizer drops the marker
// from an author's markup, which reads registered fields only.
func TestCompileBindsCustomFieldMeta(t *testing.T) {
	src := `<div data-wbc="1"><p data-wbc="2">x</p><img data-wbc="3" src="/a.jpg" alt=""></div>`
	out := CompileBinds(src, "s", ParseCodeBinds(`{"2":{"kind":"field","field":"custom","name":"design_lead"},"3":{"kind":"field","field":"image","name":"design_pic"}}`), CodeFillOptions{})
	for _, want := range []string{`data-wb-field="design_lead" data-wb-meta=""`, `data-wb-field="design_pic" data-wb-meta=""`} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %s\n%s", want, out)
		}
	}
	snap, _ := sanitizeOK(t, `<p data-wb-slot="post.field" data-wb-field="secret" data-wb-meta="">x</p>`, SnapshotOptions{})
	if strings.Contains(snap, AttrMeta) || !strings.Contains(snap, `data-wb-field="secret"`) {
		t.Errorf("the sanitizer must drop data-wb-meta only: %q", snap)
	}
}

// A code form's radio group and checkboxes declare their values like a select's options; errors
// come back under the design's input names.
func TestCodeFormChoicesAndInputErrors(t *testing.T) {
	src := `<form data-wbc="1"><input name="full-name" required>` +
		`<input type="radio" name="pref" value="mail"><input type="radio" name="pref" value="phone">` +
		`<input type="checkbox" name="news"><input type="checkbox" name="terms" value="yes" required></form>`
	list := codeFormSchemas("s", src, ParseCodeBinds(`{"1":{"kind":"form"}}`))
	if len(list) != 1 {
		t.Fatalf("schemas: %+v", list)
	}
	fs := list[0]
	by := map[string]FormField{}
	for _, f := range fs.Fields {
		by[f.Input] = f
	}
	if f := by["pref"]; f.Type != "radio" || f.Name != "field_pref" || !reflect.DeepEqual(f.Options, []string{"mail", "phone"}) {
		t.Errorf("radio: %+v", f)
	}
	if f := by["news"]; f.Type != "checkbox" || !reflect.DeepEqual(f.Options, []string{"on"}) {
		t.Errorf("checkbox without a value: %+v", f)
	}
	if len(fs.Fields) != 4 {
		t.Errorf("fields: %+v", fs.Fields)
	}
	_, errs := fs.Validate(map[string]string{"full-name": "", "pref": "fax", "news": "on", "terms": "no"})
	got := fs.InputErrors(errs)
	want := map[string]string{"full-name": FormRequired, "pref": FormInvalid, "terms": FormInvalid}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %v, want %v", got, want)
	}
	clean, errs := fs.Validate(map[string]string{"full-name": "Ada", "pref": "phone", "terms": "yes"})
	if len(errs) != 0 || clean["field_pref"] != "phone" || clean["field_terms"] != "yes" {
		t.Errorf("valid: %v %v", clean, errs)
	}
	// Builder forms keep the Pursae names.
	if got := (FormSchema{Variant: "contact"}).InputErrors(map[string]string{"email": FormInvalid}); got["email"] != FormInvalid {
		t.Errorf("builder form errors: %v", got)
	}
}
