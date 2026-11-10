package main

import (
	"strings"
	"testing"
)

func TestParseFeed(t *testing.T) {
	items, err := ParseFeed(200, `[{"title":"A","url":"https://x.test/a"},{"title":"","url":"https://x.test/b"},{"title":"C","url":"javascript:alert(1)"}]`)
	if err != nil || len(items) != 1 || items[0].Title != "A" {
		t.Fatalf("items %v err %v", items, err)
	}
	if _, err := ParseFeed(500, "[]"); err == nil {
		t.Fatal("HTTP 500 must fail")
	}
	if _, err := ParseFeed(200, "nope"); err == nil {
		t.Fatal("bad JSON must fail")
	}
}

func TestRender(t *testing.T) {
	items := []Item{{"<b>x</b>", "https://x.test/?a=1&b=2"}, {"y", "https://y.test"}}
	out := Render(items, 1)
	if strings.Contains(out, "<b>") || strings.Contains(out, "y.test") || !strings.Contains(out, "&amp;b=2") {
		t.Fatalf("render %s", out)
	}
	if Render(nil, 0) != "" {
		t.Fatal("empty feed renders nothing")
	}
}
