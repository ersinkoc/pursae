package wpgotest_test

import (
	"testing"

	"wpgo/sdk/go/wpgo"
	"wpgo/sdk/go/wpgo/wpgotest"
)

func TestFakeHost(t *testing.T) {
	h := wpgotest.New(t)
	h.SetOption("blogname", "Site")
	if v, found, err := wpgo.GetOption("blogname"); err != nil || !found || v != "Site" {
		t.Fatalf("option: %q %v %v", v, found, err)
	}
	if err := wpgo.Storage.Set("a", "1"); err != nil {
		t.Fatal(err)
	}
	if v, found, _ := wpgo.Storage.Get("a"); !found || v != "1" || h.Storage()["a"] != "1" {
		t.Fatal("storage round trip")
	}
	keys, _, _ := wpgo.Storage.List("", "", 10)
	if len(keys) != 1 {
		t.Fatalf("keys %v", keys)
	}
	h.StubHTTP("https://x.test/", 200, "hi")
	if r, err := wpgo.HTTPGet("https://x.test/"); err != nil || r.Body != "hi" {
		t.Fatalf("http: %v %v", r, err)
	}
	if _, err := wpgo.HTTPGet("https://nope.test/"); err == nil {
		t.Fatal("unstubbed URL must fail")
	}
	id := h.AddPost(wpgo.Post{Title: "Hello"})
	if p, found, _ := wpgo.GetPost(id); !found || p.Title != "Hello" {
		t.Fatal("get post")
	}
	if l, _ := wpgo.QueryPosts(wpgo.PostQuery{Search: "hell"}); l.Total != 1 {
		t.Fatalf("query: %+v", l)
	}
	if u, _ := wpgo.CurrentUser(); u != nil {
		t.Fatal("anonymous expected")
	}
	h.SetUser(&wpgo.User{ID: 3, Caps: []string{"edit_posts"}})
	if u, _ := wpgo.CurrentUser(); !u.Can("edit_posts") {
		t.Fatal("user caps")
	}
	var v string
	_ = wpgo.Transients.Set("t", "x", 0)
	if found, _ := wpgo.Transients.Get("t", &v); !found || v != "x" {
		t.Fatal("transient")
	}
}

func TestRestoredAfterTest(t *testing.T) {
	t.Run("inner", func(t *testing.T) { wpgotest.New(t) })
	if _, _, err := wpgo.GetOption("x"); err != wpgo.ErrNotInHost {
		t.Fatalf("host must be removed after the test, got %v", err)
	}
}
