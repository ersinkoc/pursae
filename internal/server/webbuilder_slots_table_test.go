package server

import (
	"strings"
	"testing"

	"wpgo/internal/links"
)

// Table tests of the pure helpers of webbuilder_slots.go (no database).

func TestWBArchiveViewTable(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{"home", true}, {"category", true}, {"tag", true}, {"taxonomy", true}, {"author", true},
		{"date", true}, {"search", true}, {"post-type-archive", true},
		{"single", false}, {"page", false}, {"front", false}, {"404", false}, {"", false},
	} {
		if got := wbArchiveView(&siteView{Q: &siteQuery{Kind: tc.kind}}); got != tc.want {
			t.Errorf("kind %q: %v, want %v", tc.kind, got, tc.want)
		}
	}
	if wbArchiveView(nil) || wbArchiveView(&siteView{}) {
		t.Error("nil view counts as an archive")
	}
}

func TestWBPrevNextTable(t *testing.T) {
	pretty := &links.Builder{Home: "http://example.test", Structure: "/%postname%/"}
	plain := &links.Builder{Home: "http://example.test"}
	for _, tc := range []struct {
		name           string
		lb             *links.Builder
		base           string
		current, total int
		want, bad      []string
	}{
		{"single page", pretty, "http://example.test/blog/", 1, 1, nil, []string{"page-numbers"}},
		{"first of three", pretty, "http://example.test/blog/", 1, 3, []string{`class="next page-numbers" href="http://example.test/blog/page/2/"`}, []string{"prev page-numbers"}},
		{"middle", pretty, "http://example.test/blog/", 2, 3, []string{`href="http://example.test/blog/"`, `href="http://example.test/blog/page/3/"`}, nil},
		{"last", pretty, "http://example.test/blog/", 3, 3, []string{"prev page-numbers"}, []string{"next page-numbers"}},
		{"plain links", plain, "http://example.test/?cat=3", 1, 2, []string{"paged=2", "cat=3"}, nil},
		{"escaped", pretty, `http://example.test/?s="x"`, 1, 2, []string{"%22x%22"}, []string{`"x"`, "&#34;x&#34;"}},
	} {
		got := wbPrevNext(tc.lb, tc.base, tc.current, tc.total)
		if len(tc.want) == 0 && got != "" && tc.total <= 1 {
			t.Errorf("%s: %q, want nothing", tc.name, got)
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in %s", tc.name, w, got)
			}
		}
		for _, b := range tc.bad {
			if strings.Contains(got, b) {
				t.Errorf("%s: unexpected %q in %s", tc.name, b, got)
			}
		}
	}
}
