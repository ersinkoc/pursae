//go:build !(wasip1 && wasm)

package wpgotest_test

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"wpgo/sdk/go/wpgo"
	"wpgo/sdk/go/wpgo/wpgotest"
)

func TestQueryPostsMatchesHostFiltersAndOrder(t *testing.T) {
	h := wpgotest.New(t)
	h.AddPost(wpgo.Post{Title: "Zebra", Date: "2026-02-02T02:00:00+02:00", Terms: map[string][]wpgo.Term{"category": {{ID: 9}}}})
	h.AddPost(wpgo.Post{Title: "apple", Date: "2026-02-02T01:00:00Z"})
	h.AddPost(wpgo.Post{Title: "About", Type: "page"})
	h.AddPost(wpgo.Post{Title: "Removed", Status: "trash"})

	for _, tc := range []struct {
		query wpgo.PostQuery
		want  string
	}{
		{wpgo.PostQuery{}, "apple,Zebra"},
		{wpgo.PostQuery{Type: "page"}, "About"},
		{wpgo.PostQuery{Term: 9}, "Zebra"},
		{wpgo.PostQuery{OrderBy: "title", Order: "ASC"}, "apple,Zebra"},
		{wpgo.PostQuery{OrderBy: "id", Order: "ASC"}, "Zebra,apple"},
		{wpgo.PostQuery{Status: "any"}, "apple,Zebra"},
		{wpgo.PostQuery{Status: "trash"}, "Removed"},
	} {
		list, err := wpgo.QueryPosts(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, p := range list.Posts {
			titles = append(titles, p.Title)
		}
		if got := strings.Join(titles, ","); got != tc.want {
			t.Errorf("query %+v returned %q, want %q", tc.query, got, tc.want)
		}
	}
	for _, size := range []int{-1, 101} {
		_, err := wpgo.QueryPosts(wpgo.PostQuery{PerPage: size})
		var hostErr *wpgo.Error
		if !errors.As(err, &hostErr) || hostErr.Code != wpgo.CodeInvalidRequest {
			t.Errorf("PerPage %d returned %v", size, err)
		}
	}
}

func TestFakeTransientExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wpgotest.New(t)
		if err := wpgo.Transients.Set("ephemeral", "temporary", time.Second); err != nil {
			t.Fatal(err)
		}
		if err := wpgo.Transients.Set("persistent", "stable", 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		var out string
		if found, err := wpgo.Transients.Get("ephemeral", &out); err != nil || found {
			t.Fatalf("expired transient = %q, found=%v, err=%v", out, found, err)
		}
		if found, err := wpgo.Transients.Get("persistent", &out); err != nil || !found || out != "stable" {
			t.Fatalf("persistent transient = %q, found=%v, err=%v", out, found, err)
		}
	})
}

func TestRenderAdminPageUsesLatestRegistration(t *testing.T) {
	h := wpgotest.New(t)
	for _, result := range []string{"first", "replacement"} {
		wpgo.RegisterAdminPage(wpgo.AdminPage{Slug: "repeat", Render: func(wpgo.Context, wpgo.AdminRequest) wpgo.AdminResponse {
			return wpgo.AdminResponse{HTML: result}
		}})
	}
	wpgo.Init()
	if out, err := h.RenderAdminPage("repeat", wpgo.AdminRequest{}); err != nil || out.HTML != "replacement" {
		t.Fatalf("render = %q, err=%v", out.HTML, err)
	}
	if got := len(h.Registered("register_admin_page")); got != 2 {
		t.Fatalf("registration history contains %d entries", got)
	}
}
