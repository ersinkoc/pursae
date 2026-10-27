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
