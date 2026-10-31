package builder

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func themeTestData() *ThemeData {
	post := &PostData{ID: 42, Type: "post", Title: "Hello &amp; Go", URL: "http://x.test/hello/", Excerpt: "One two three four five six",
		Date: "May 1, 2026", DateISO: "2026-05-01T10:00:00+00:00", AuthorName: "Ada", AuthorURL: "http://x.test/author/ada/",
		Categories: []TermLink{{"News", "http://x.test/category/news/"}}, ImageURL: "http://x.test/wp-content/uploads/a.jpg"}
	return &ThemeData{
		SiteName: "Go Site", Tagline: "Fast pages", HomeURL: "http://x.test/", SearchURL: "http://x.test/",
		Now: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC),
		Menu: func(loc string, id int64) []*MenuItem {
			if loc != "primary" && id != 7 {
				return nil
			}
			return []*MenuItem{{ID: 1, Label: "Home", URL: "http://x.test/", Current: true},
				{ID: 2, Label: "About", URL: "http://x.test/about/", Children: []*MenuItem{{ID: 3, Label: "Team", URL: "http://x.test/team/"}}}}
		},
		Post:        func() *PostData { return post },
		PostContent: func() string { return "<p>Body text</p>" },
		Archive: func() *ArchiveData {
			return &ArchiveData{Title: "Category: News", PlainTitle: "News", Posts: []*PostData{post}, Page: 2, Pages: 3,
				PageURL: func(n int) string { return "http://x.test/category/news/page/" + itoa(n) + "/" }}
		},
	}
}

func TestThemeWidgetsRender(t *testing.T) {
	tree := `[{"id":"c000001","elType":"container","settings":{},"elements":[
		{"id":"w000001","elType":"widget","widgetType":"theme-site-title","settings":{"header_size":"h1","title_color":"#112233"},"elements":[]},
		{"id":"w000002","elType":"widget","widgetType":"theme-site-tagline","settings":{},"elements":[]},
		{"id":"w000003","elType":"widget","widgetType":"nav-menu","settings":{"menu_location":"primary"},"elements":[]},
		{"id":"w000004","elType":"widget","widgetType":"theme-post-title","settings":{},"elements":[]},
		{"id":"w000005","elType":"widget","widgetType":"theme-post-content","settings":{},"elements":[]},
		{"id":"w000006","elType":"widget","widgetType":"post-info","settings":{},"elements":[]},
		{"id":"w000007","elType":"widget","widgetType":"theme-archive-title","settings":{"include_context":""},"elements":[]},
		{"id":"w000008","elType":"widget","widgetType":"archive-posts","settings":{"columns":"2"},"elements":[]},
		{"id":"w000009","elType":"widget","widgetType":"search-form","settings":{},"elements":[]},
		{"id":"w000010","elType":"widget","widgetType":"theme-site-copyright","settings":{},"elements":[]},
		{"id":"w000011","elType":"widget","widgetType":"theme-site-logo","settings":{},"elements":[]},
		{"id":"w000012","elType":"widget","widgetType":"theme-post-featured-image","settings":{},"elements":[]}
	]}]`
	els, err := ParseElements([]byte(tree))
	if err != nil {
		t.Fatal(err)
	}
	repanic = true
	defer func() { repanic = false }()
	res, err := Render(&Document{ID: 9, Type: "header", Elements: els}, &RenderContext{HomeURL: "http://x.test", Theme: themeTestData()})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`elementor-widget-theme-site-title elementor-widget-heading`,
		`<h1 class="elementor-heading-title elementor-size-default"><a href="http://x.test/">Go Site</a></h1>`,
		`>Fast pages</p>`,
		`<li class="menu-item current-menu-item menu-item-1"><a href="http://x.test/" aria-current="page" class="elementor-item elementor-item-active">Home</a></li>`,
		`<ul class="sub-menu elementor-nav-menu--dropdown">`,
		`elementor-nav-menu--dropdown-tablet`,
		`>Hello &amp; Go</h1>`,
		`<p>Body text</p>`,
		`elementor-post-info__item--type-author">Ada</span>`,
		`<time datetime="2026-05-01T10:00:00+00:00">May 1, 2026</time>`,
		`>News</h1>`,
		`elementor-grid-2 elementor-grid-tablet-2 elementor-grid-mobile-1`,
		`<h3 class="elementor-post__title"><a href="http://x.test/hello/">Hello &amp; Go</a></h3>`,
		`One two three four five six`,
		`href="http://x.test/category/news/page/3/">Next &raquo;</a>`,
		`<input id="elementor-search-form-w000009"`,
		`© 2031 Go Site. All rights reserved.`,
		`elementor-widget-theme-site-logo elementor-widget-image`,
		`src="http://x.test/wp-content/plugins/web-builder/assets/images/placeholder.png"`,
		`src="http://x.test/wp-content/uploads/a.jpg"`,
	} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("missing %s", want)
		}
	}
	if !strings.Contains(res.CSS, ".elementor-9 .elementor-element.elementor-element-w000001 .elementor-heading-title, .elementor-9 .elementor-element.elementor-element-w000001 .elementor-heading-title a{color:#112233;}") {
		t.Errorf("site title color CSS missing:\n%s", res.CSS)
	}
	if !strings.Contains(strings.Join(res.Styles, ","), "widget-theme-elements") {
		t.Errorf("styles: %v", res.Styles)
	}

	// Without data the front end prints nothing and the editor a placeholder.
	nav := &Element{ID: "n1", ElType: "widget", WidgetType: "nav-menu", Settings: map[string]any{"menu_location": "footer"}}
	if out, _ := RenderWidget(nav, &Document{ID: 1}, &RenderContext{Theme: themeTestData()}); !strings.Contains(out, "No menu is assigned") {
		t.Errorf("editor placeholder: %q", out)
	}
	res, _ = Render(&Document{ID: 1, Elements: []*Element{nav}}, &RenderContext{Theme: themeTestData()})
	if strings.Contains(res.HTML, "elementor-widget-nav-menu") {
		t.Errorf("empty menu rendered on the front end: %s", res.HTML)
	}
	// A post's own document never embeds its content.
	pc := &Element{ID: "p1", ElType: "widget", WidgetType: "theme-post-content", Settings: map[string]any{}}
	res, _ = Render(&Document{ID: 42, Elements: []*Element{pc}}, &RenderContext{Theme: themeTestData()})
	if strings.Contains(res.HTML, "Body text") {
		t.Errorf("post content recursion")
	}
}

func TestThemeWidgetsConfig(t *testing.T) {
	names := map[string]bool{}
	for _, w := range ThemeWidgets() {
		names[w.Name] = true
		var ctrls map[string]map[string]any
		if err := json.Unmarshal(w.ControlsJSON(), &ctrls); err != nil {
			t.Fatalf("%s: %v", w.Name, err)
		}
		for name, c := range ctrls {
			if c["type"] != "section" && (c["section"] == nil || c["tab"] == nil) {
				t.Errorf("%s.%s has no section/tab", w.Name, name)
			}
		}
	}
	for _, n := range []string{"theme-site-logo", "theme-site-title", "theme-site-tagline", "theme-post-title", "theme-post-content",
		"theme-post-featured-image", "post-info", "theme-archive-title", "archive-posts", "search-form", "theme-site-copyright"} {
		if !names[n] {
			t.Errorf("widget %s not registered", n)
		}
	}
	// The controls object keeps panel order.
	w := ThemeWidgets()[0]
	if !strings.HasPrefix(string(w.ControlsJSON()), `{"section_image":`) {
		t.Errorf("controls order: %.80s", w.ControlsJSON())
	}
}
