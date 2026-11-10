package main

import "testing"

func TestPayload(t *testing.T) {
	body, ok := Payload(map[string]any{"id": 7.0, "title": "Hi", "link": "https://x.test/hi", "status": "publish"})
	if !ok || body != `{"id":7,"link":"https://x.test/hi","title":"Hi"}` {
		t.Fatalf("body %q ok %v", body, ok)
	}
	if _, ok := Payload(map[string]any{"status": "draft"}); ok {
		t.Fatal("drafts must not notify")
	}
	if _, ok := Payload("junk"); ok {
		t.Fatal("non-object must not notify")
	}
}
