package plugins

import (
	"context"
	"testing"
)

func TestMemoryHostGetPostTermsIndependent(t *testing.T) {
	h := NewMemoryHost()
	h.AddPost(PostData{ID: 1, Type: "post", Status: "publish", Terms: map[string][]TermData{"category": {{ID: 1, Name: "original"}}}})
	ctx := context.Background()
	post, err := h.GetPost(ctx, "p", 1)
	if err != nil || post == nil {
		t.Fatalf("post=%v err=%v", post, err)
	}
	gate, done := make(chan struct{}), make(chan struct{})
	go func() { <-gate; post.Terms["category"][0].Name = "changed"; close(done) }()
	close(gate)
	<-done
	got, err := h.GetPost(ctx, "p", 1)
	if err != nil || got.Terms["category"][0].Name != "original" {
		t.Fatalf("post=%+v err=%v", got, err)
	}
}

func TestMemoryHostQueryPostTermsIndependent(t *testing.T) {
	h := NewMemoryHost()
	h.AddPost(PostData{ID: 1, Type: "post", Status: "publish", Terms: map[string][]TermData{"category": {{ID: 1, Name: "original"}}}})
	ctx := context.Background()
	list, err := h.QueryPosts(ctx, "p", PostQuery{})
	if err != nil || len(list.Posts) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	gate, done := make(chan struct{}), make(chan struct{})
	go func() { <-gate; list.Posts[0].Terms["category"][0].Name = "changed"; close(done) }()
	close(gate)
	<-done
	got, err := h.GetPost(ctx, "p", 1)
	if err != nil || got.Terms["category"][0].Name != "original" {
		t.Fatalf("post=%+v err=%v", got, err)
	}
}

func TestMemoryHostAddPostTermsIndependent(t *testing.T) {
	h := NewMemoryHost()
	post := PostData{ID: 1, Type: "post", Status: "publish", Terms: map[string][]TermData{"category": {{ID: 1, Name: "original"}}}}
	h.AddPost(post)
	gate, done := make(chan struct{}), make(chan struct{})
	go func() { <-gate; post.Terms["category"][0].Name = "changed"; close(done) }()
	close(gate)
	<-done
	got, err := h.GetPost(context.Background(), "p", 1)
	if err != nil || got.Terms["category"][0].Name != "original" {
		t.Fatalf("post=%+v err=%v", got, err)
	}
}
