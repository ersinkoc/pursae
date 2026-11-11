package plugins

import (
	"context"
	"math"
	"testing"
)

func TestMemoryHostPaginationOverflow(t *testing.T) {
	h := NewMemoryHost()
	h.AddPost(PostData{ID: 1, Type: "post", Status: "publish"})
	for _, q := range []PostQuery{{Page: math.MaxInt, PerPage: 2}, {Page: 2, PerPage: math.MaxInt}} {
		got, err := h.QueryPosts(context.Background(), "p", q)
		if err != nil || got.Pages != 1 || len(got.Posts) != 0 {
			t.Fatalf("query=%+v result=%+v err=%v", q, got, err)
		}
	}
}
