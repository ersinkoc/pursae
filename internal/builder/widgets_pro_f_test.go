package builder

// Fixture context for GoWebEditor Pro fork F (author box, post navigation, floating buttons,
// link in bio): mirrors what the lab printed for the pro-f-* pages.

import (
	"strings"
	"testing"
)

func init() {
	prev := proTestRC
	proTestRC = func(rc *RenderContext) {
		if prev != nil {
			prev(rc)
		}
		pd := rc.Pro
		author := &UserInfo{ID: 1, Login: "labadmin", Nicename: "labadmin", DisplayName: "labadmin",
			PostsURL:  "http://localhost:8095/author/labadmin/",
			AvatarURL: "https://secure.gravatar.com/avatar/c62e7ccf16c5eee4fa47ea5e5e244a3d230efe74618ab5c397ae4e3143b6f5a8?s=96&d=mm&r=g",
			Meta:      map[string]any{}}
		// Only the pro-f-1 page (262) is changed, so other forks' fixtures keep the shared context.
		prevAuthor, prevPrev, prevNext := pd.AuthorOf, pd.Previous, pd.Next
		pd.AuthorOf = func(id int64) *UserInfo {
			if id == 262 || prevAuthor == nil {
				return author
			}
			return prevAuthor(id)
		}
		pd.Previous = func(id int64, sameTerm, tax string) *PostData {
			if id == 262 { // the lab's previous page was another fixture page
				return &PostData{ID: 250, Title: "WPGO fixture pro-b-3", URL: "http://localhost:8095/wpgo-fx-pro-b-3/"}
			}
			if prevPrev != nil {
				return prevPrev(id, sameTerm, tax)
			}
			return nil
		}
		pd.Next = func(id int64, sameTerm, tax string) *PostData {
			if id != 262 && prevNext != nil {
				return prevNext(id, sameTerm, tax)
			}
			return nil
		}
	}
}

func TestProFBreadcrumbsAndComments(t *testing.T) {
	els, err := ParseElements([]byte(`[{"id":"fa00001","elType":"widget","widgetType":"breadcrumbs","settings":{"html_tag":"nav"},"elements":[]},
		{"id":"fa00002","elType":"widget","widgetType":"post-comments","settings":{},"elements":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	rc := &RenderContext{HomeURL: "http://x.test", Theme: themeTestData(), Pro: &ProData{
		Breadcrumbs: func() []TermLink {
			return []TermLink{{Name: "Home", URL: "http://x.test/"}, {Name: "News", URL: "http://x.test/news/"}, {Name: "Hello"}}
		},
		CommentsHTML: func(id int64) string { return `<div id="comments">post ` + itoa(int(id)) + `</div>` },
	}}
	res, err := Render(&Document{ID: 5, Type: "single-post", Elements: els}, rc)
	if err != nil {
		t.Fatal(err)
	}
	sep := `<span class="breadcrumb-separator" aria-hidden="true"> » </span>`
	li := `<span itemprop="itemListElement" itemscope itemtype="https://schema.org/ListItem">`
	for _, want := range []string{`<nav id="breadcrumbs" aria-label="Breadcrumb"><span itemscope itemtype="https://schema.org/BreadcrumbList">` +
		li + `<a itemprop="item" href="http://x.test/"><span itemprop="name">Home</span></a><meta itemprop="position" content="1" /></span>` + sep +
		li + `<a itemprop="item" href="http://x.test/news/"><span itemprop="name">News</span></a><meta itemprop="position" content="2" /></span>` + sep +
		`<span class="breadcrumb_last" aria-current="page" itemprop="itemListElement" itemscope itemtype="https://schema.org/ListItem"><span itemprop="name">Hello</span><meta itemprop="position" content="3" /></span></span></nav>`,
		`elementor-widget-breadcrumbs`, `<div id="comments">post 42</div>`} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("missing %q in\n%s", want, res.HTML)
		}
	}
}
