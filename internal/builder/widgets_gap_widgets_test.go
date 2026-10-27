package builder

import (
	"strings"
	"testing"
)

// Page Title tag: "include context" prefixes the post type's singular name on singular pages
// (upstream Pro\Core\Utils::get_page_title).
func TestGapPageTitleIncludeContext(t *testing.T) {
	for _, tc := range []struct {
		typ, settings, want string
	}{
		{"post", `{"include_context":"yes"}`, ">Post: Hello &amp; Go<"},
		{"page", `{"include_context":"yes"}`, ">Page: Hello &amp; Go<"},
		{"page", `{}`, ">Hello &amp; Go<"},
	} {
		els, err := ParseElements([]byte(`[{"id":"gp1","elType":"widget","widgetType":"heading","settings":{"title":"x","__dynamic__":{"title":"[elementor-tag id=\"t\" name=\"page-title\" settings=\"` +
			strings.ReplaceAll(strings.ReplaceAll(tc.settings, `"`, "%22"), ":", "%3A") + `\"]"}},"elements":[]}]`))
		if err != nil {
			t.Fatal(err)
		}
		post := &PostData{ID: 42, Type: tc.typ, Title: "Hello &amp; Go"}
		rc := &RenderContext{HomeURL: "http://x.test", Theme: &ThemeData{Post: func() *PostData { return post }}}
		res, err := Render(&Document{ID: 9, Type: "single-post", Elements: els}, rc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.HTML, tc.want) {
			t.Errorf("%s %s: want %q in\n%s", tc.typ, tc.settings, tc.want, res.HTML)
		}
	}
}

// Breadcrumbs options (separator, home text, hiding home / current item) and Post Comments on a
// page without theme post data (the editor render): it prints the document's comments.
func TestGapBreadcrumbOptionsAndComments(t *testing.T) {
	els, err := ParseElements([]byte(`[{"id":"gb1","elType":"widget","widgetType":"breadcrumbs","settings":{"separator":"/","home_text":"Start"},"elements":[]},
		{"id":"gb2","elType":"widget","widgetType":"breadcrumbs","settings":{"show_home":"","show_current":""},"elements":[]},
		{"id":"gb3","elType":"widget","widgetType":"post-comments","settings":{},"elements":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	rc := &RenderContext{HomeURL: "http://x.test", Pro: &ProData{
		Breadcrumbs: func() []TermLink {
			return []TermLink{{Name: "Home", URL: "http://x.test/"}, {Name: "Parent", URL: "http://x.test/parent/"}, {Name: "Child"}}
		},
		CommentsHTML: func(id int64) string { return `<div id="comments">comments of ` + itoa(int(id)) + `</div>` },
	}}
	res, err := Render(&Document{ID: 77, Type: "wp-page", Elements: els}, rc)
	if err != nil {
		t.Fatal(err)
	}
	gb1 := res.HTML[strings.Index(res.HTML, "elementor-element-gb1"):strings.Index(res.HTML, "elementor-element-gb2")]
	gb2 := res.HTML[strings.Index(res.HTML, "elementor-element-gb2"):strings.Index(res.HTML, "elementor-element-gb3")]
	for _, want := range []string{`<p id="breadcrumbs">`, `<span itemprop="name">Start</span>`, `<span class="breadcrumb-separator" aria-hidden="true"> / </span>`,
		`<meta itemprop="position" content="3" />`} {
		if !strings.Contains(gb1, want) {
			t.Errorf("gb1 misses %q:\n%s", want, gb1)
		}
	}
	if strings.Contains(gb2, ">Home<") || strings.Contains(gb2, ">Child<") || !strings.Contains(gb2, `<span itemprop="name">Parent</span>`) {
		t.Errorf("gb2 should show only the parent:\n%s", gb2)
	}
	if !strings.Contains(res.HTML, `<div id="comments">comments of 77</div>`) {
		t.Errorf("post comments of the document missing:\n%s", res.HTML)
	}
}
