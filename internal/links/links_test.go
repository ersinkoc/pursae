package links

import (
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
