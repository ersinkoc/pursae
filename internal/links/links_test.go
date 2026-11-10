package links

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

func TestPermalinks(t *testing.T) {
	date := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	post := &store.Post{ID: 7, Type: "post", Slug: "hello-world", Status: "publish", PublishedAt: &date}
	b := &Builder{Home: "http://ex.test", Structure: "/%year%/%monthnum%/%day%/%postname%/", Loc: time.UTC}

	if got := b.Permalink(post); got != "http://ex.test/2026/10/03/hello-world/" {
		t.Errorf("pretty permalink = %s", got)
	}
	if got := b.Paged(b.HomeURL("/category/news/"), 3); got != "http://ex.test/category/news/page/3/" {
		t.Errorf("paged = %s", got)
	}
	if got := b.Paged("http://ex.test/category/news/page/3/", 1); got != "http://ex.test/category/news/" {
		t.Errorf("paged back to 1 = %s", got)
	}

	plain := &Builder{Home: "http://ex.test"}
	if got := plain.Permalink(post); got != "http://ex.test/?p=7" {
		t.Errorf("plain permalink = %s", got)
	}
	if got := plain.Paged(plain.HomeURL("/"), 2); got != "http://ex.test/?paged=2" {
		t.Errorf("plain paged = %s", got)
	}

	draft := &store.Post{ID: 8, Type: "post", Status: "draft"}
	if got := b.Permalink(draft); got != "http://ex.test/?p=8" {
		t.Errorf("draft permalink = %s", got)
	}
}

func TestPagedFirstPageFollowsSlashPolicy(t *testing.T) {
	for _, tc := range []struct{ structure, base, want string }{
		{"/%postname%", "https://example.test/cat/page/2?x=1#top", "https://example.test/cat?x=1#top"},
		{"/%postname%/", "https://example.test/cat/page/2/", "https://example.test/cat/"},
		{"/%postname%", "https://example.test/page/2", "https://example.test/"},
	} {
		b := Builder{Structure: tc.structure}
		if got := b.Paged(tc.base, 1); got != tc.want {
			t.Errorf("Paged(%q, 1) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestPlainCommentsFeedLink(t *testing.T) {
	b := Builder{Home: "https://example.test"}
	if got := b.FeedLink("comments_rss2"); got != "https://example.test/?feed=comments-rss2" {
		t.Fatalf("comments feed = %q", got)
	}
}

func TestPostCommentsFeedPreservesFilteredURL(t *testing.T) {
	p := &store.Post{ID: 7, Type: "post", Slug: "мир", Status: "publish"}
	for _, structure := range []string{"", "/%postname%/"} {
		b := Builder{Home: "https://example.test", Structure: structure, LinkFilter: func(_ string, _ any, link string) string {
			sep := "?"
			if strings.Contains(link, "?") {
				sep = "&"
			}
			return link + sep + "lang=fr#top"
		}}
		got := b.PostCommentsFeedFormat(p, "atom")
		u, err := url.Parse(got)
		if err != nil || u.Query().Get("lang") != "fr" || u.Fragment != "top" {
			t.Fatalf("structure %q: %q (%v)", structure, got, err)
		}
		if structure == "" && (u.Query().Get("p") != "7" || u.Query().Get("feed") != "atom") {
			t.Errorf("plain feed = %q", got)
		}
		if structure != "" && u.EscapedPath() != "/%D0%BC%D0%B8%D1%80/feed/atom/" {
			t.Errorf("pretty feed = %q", got)
		}
	}
}

func TestPlainFrontPageCommentsFeedKeepsPageID(t *testing.T) {
	b := Builder{Home: "https://example.test", FrontPageID: 7}
	p := &store.Post{ID: 7, Type: "page", Status: "publish", Slug: "home"}
	u, err := url.Parse(b.PostCommentsFeed(p))
	if err != nil || u.Query().Get("page_id") != "7" || u.Query().Get("feed") != "rss2" {
		t.Fatalf("front page feed = %v, err=%v", u, err)
	}
}

func TestMatcher(t *testing.T) {
	m := NewMatcher("/%year%/%monthnum%/%day%/%postname%/")
	got, ok := m.Match("2026/10/03/hello-world")
	if !ok || got["postname"] != "hello-world" || got["year"] != "2026" {
		t.Fatalf("match = %v %v", got, ok)
	}
	if _, ok := m.Match("2026/10"); ok {
		t.Error("date archive must not match a post")
	}
	if NewMatcher("") != nil {
		t.Error("plain structure has no matcher")
	}
}

func TestCustomPostTypeLinks(t *testing.T) {
	date := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	typeBase := func(typ string) (string, string) {
		switch typ {
		case "book":
			return "books", "library"
		case "event":
			return "/event/", ""
		}
		return "", ""
	}
	parent := &store.Post{ID: 3, Type: "book", Slug: "series", Status: "publish", PublishedAt: &date}
	b := &Builder{Home: "http://ex.test", Structure: "/%postname%/", Loc: time.UTC, TypeBase: typeBase,
		TypeHierarchical: func(typ string) bool { return typ == "book" },
		Ancestors: func(p *store.Post) []*store.Post {
			if p.ParentID == 3 {
				return []*store.Post{parent}
			}
			return nil
		}}
	book := &store.Post{ID: 9, Type: "book", Slug: "dune", Status: "publish", PublishedAt: &date}
	if got := b.Permalink(book); got != "http://ex.test/books/dune/" {
		t.Errorf("cpt permalink = %s", got)
	}
	child := &store.Post{ID: 10, Type: "book", Slug: "part-two", ParentID: 3, Status: "publish", PublishedAt: &date}
	if got := b.Permalink(child); got != "http://ex.test/books/series/part-two/" {
		t.Errorf("hierarchical cpt permalink = %s", got)
	}
	// A non-hierarchical type never adds ancestors (its router cannot resolve them).
	evChild := &store.Post{ID: 14, Type: "event", Slug: "day-two", ParentID: 3, Status: "publish", PublishedAt: &date}
	if got := b.Permalink(evChild); got != "http://ex.test/event/day-two/" {
		t.Errorf("non-hierarchical cpt with a parent = %s", got)
	}
	if got := b.PermalinkTemplate(book); got != "http://ex.test/books/%postname%/" {
		t.Errorf("cpt permalink template = %s", got)
	}
	draft := &store.Post{ID: 11, Type: "book", Slug: "draft", Status: "draft"}
	if got := b.Permalink(draft); got != "http://ex.test/?p=11" {
		t.Errorf("draft cpt permalink = %s", got)
	}
	ev := &store.Post{ID: 12, Type: "event", Slug: "launch", Status: "publish", PublishedAt: &date}
	if got := b.Permalink(ev); got != "http://ex.test/event/launch/" {
		t.Errorf("trimmed base permalink = %s", got)
	}
	unknown := &store.Post{ID: 13, Type: "wb_template", Slug: "header", Status: "publish", PublishedAt: &date}
	if got := b.Permalink(unknown); got != "http://ex.test/?p=13" {
		t.Errorf("unrouted type permalink = %s", got)
	}
	if got := b.PostTypeArchiveLink("book"); got != "http://ex.test/library/" {
		t.Errorf("archive link = %s", got)
	}
	if got := b.Paged(b.PostTypeArchiveLink("book"), 2); got != "http://ex.test/library/page/2/" {
		t.Errorf("archive paged = %s", got)
	}
	if got := b.PostTypeArchiveLink("event"); got != "" {
		t.Errorf("no-archive type link = %q", got)
	}
	if got := b.PostTypeArchiveLink("post"); got != "" {
		t.Errorf("post archive link = %q", got)
	}

	plain := &Builder{Home: "http://ex.test", TypeBase: typeBase}
	if got := plain.Permalink(book); got != "http://ex.test/?p=9" {
		t.Errorf("plain cpt permalink = %s", got)
	}
	if got := plain.PostTypeArchiveLink("book"); got != "http://ex.test/?post_type=book" {
		t.Errorf("plain archive link = %s", got)
	}
	if got := plain.Paged(plain.PostTypeArchiveLink("book"), 2); got != "http://ex.test/?paged=2&post_type=book" {
		t.Errorf("plain archive paged = %s", got)
	}
}

// The static front page's permalink is the home URL (_get_page_link()).
func TestFrontPageLink(t *testing.T) {
	date := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	home := &store.Post{ID: 7, Type: "page", Slug: "home", Status: "publish", PublishedAt: &date}
	other := &store.Post{ID: 8, Type: "page", Slug: "about", Status: "publish", PublishedAt: &date}
	b := &Builder{Home: "http://ex.test", Structure: "/%postname%/", Loc: time.UTC, FrontPageID: 7}
	if got := b.Permalink(home); got != "http://ex.test/" {
		t.Errorf("front page permalink = %s", got)
	}
	if got := b.Permalink(other); got != "http://ex.test/about/" {
		t.Errorf("other page permalink = %s", got)
	}
	plain := &Builder{Home: "http://ex.test", FrontPageID: 7}
	if got := plain.Permalink(home); got != "http://ex.test/" {
		t.Errorf("plain front page permalink = %s", got)
	}
	b.FrontPageID = 0
	if got := b.Permalink(home); got != "http://ex.test/home/" {
		t.Errorf("posts on the front: page permalink = %s", got)
	}
}

// TestPermalinkLookupsOnlyWhenUsed: %category% and %author% are looked up only when the structure
// has them (each lookup is a query; a loop of posts used to pay both for every link).
func TestPermalinkLookupsOnlyWhenUsed(t *testing.T) {
	date := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	post := &store.Post{ID: 7, Type: "post", Slug: "hello-world", Status: "publish", PublishedAt: &date, AuthorID: 3}
	cats, authors := 0, 0
	b := &Builder{Home: "http://ex.test", Structure: "/%year%/%postname%/", Loc: time.UTC,
		FirstCategory: func(*store.Post) *store.Term { cats++; return &store.Term{Slug: "news"} },
		AuthorSlug:    func(int64) string { authors++; return "ada" }}
	if got := b.Permalink(post); got != "http://ex.test/2026/hello-world/" || cats != 0 || authors != 0 {
		t.Errorf("permalink %s, %d category and %d author lookups", got, cats, authors)
	}
	b.Structure = "/%category%/%author%/%postname%/"
	if got := b.Permalink(post); got != "http://ex.test/news/ada/hello-world/" || cats != 1 || authors != 1 {
		t.Errorf("permalink %s, %d category and %d author lookups", got, cats, authors)
	}
}

// TestNonLatinSlugLinks: slugs stored as UTF-8 come out percent-encoded, ASCII slugs unchanged, and the
// %postname% placeholder of the REST permalink template is not escaped.
func TestNonLatinSlugLinks(t *testing.T) {
	date := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	b := &Builder{Home: "http://ex.test", Structure: "/%postname%/", Loc: time.UTC}
	ru := &store.Post{ID: 9, Type: "post", Slug: "мир", Status: "publish", PublishedAt: &date}
	if got := b.Permalink(ru); got != "http://ex.test/%D0%BC%D0%B8%D1%80/" {
		t.Errorf("cyrillic permalink = %s", got)
	}
	if got := b.PermalinkTemplate(ru); got != "http://ex.test/%postname%/" {
		t.Errorf("permalink template = %s", got)
	}
	if got := b.TermLink(&store.Term{Taxonomy: "post_tag", Slug: "日本"}); got != "http://ex.test/tag/%E6%97%A5%E6%9C%AC/" {
		t.Errorf("tag link = %s", got)
	}
	if got := (&Builder{Home: "http://ex.test"}).TermLink(&store.Term{Taxonomy: "post_tag", Slug: "日本"}); got != "http://ex.test/?tag=%E6%97%A5%E6%9C%AC" {
		t.Errorf("plain tag link = %s", got)
	}
	page := &store.Post{ID: 10, Type: "page", Slug: "hello-world", Status: "publish"}
	if got := b.Permalink(page); got != "http://ex.test/hello-world/" {
		t.Errorf("ascii page = %s", got)
	}
}

// Page n of a <!--nextpage--> static front page lives under the pagination base: "/2/" after the
// home URL is no route (_wp_link_page()).
func TestFrontPagePageLink(t *testing.T) {
	date := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	home := &store.Post{ID: 7, Type: "page", Slug: "home", Status: "publish", PublishedAt: &date}
	other := &store.Post{ID: 8, Type: "page", Slug: "about", Status: "publish", PublishedAt: &date}
	cases := []struct {
		b    *Builder
		p    *store.Post
		n    int
		want string
	}{
		{&Builder{Home: "http://ex.test", Structure: "/%postname%/", FrontPageID: 7}, home, 2, "http://ex.test/page/2/"},
		{&Builder{Home: "http://ex.test", Structure: "/%postname%", FrontPageID: 7}, home, 3, "http://ex.test/page/3"},
		{&Builder{Home: "http://ex.test", Structure: "/%postname%/", FrontPageID: 7}, home, 1, "http://ex.test/"},
		{&Builder{Home: "http://ex.test", Structure: "/%postname%/", FrontPageID: 7}, other, 2, "http://ex.test/about/2/"},
		{&Builder{Home: "http://ex.test", FrontPageID: 7}, home, 2, "http://ex.test/?page=2"},
		{&Builder{Home: "http://ex.test", Structure: "/%postname%/"}, home, 2, "http://ex.test/home/2/"},
	}
	for _, tc := range cases {
		if got := tc.b.PostPageLink(tc.p, tc.n); got != tc.want {
			t.Errorf("PostPageLink(%s, %d) with %q = %s, want %s", tc.p.Slug, tc.n, tc.b.Structure, got, tc.want)
		}
	}
}
