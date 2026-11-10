package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// Regression tests for the REST core audit fixes (collection filters, search subtypes, comment
// rules, synced patterns, settings validation, comment moves, readable drafts and autosaves).

func restListIDs(list []any) []int64 {
	out := []int64{}
	for _, it := range list {
		out = append(out, int64(it.(map[string]any)["id"].(float64)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sameIDs(got []int64, want ...float64) bool {
	w := make([]int64, 0, len(want))
	for _, x := range want {
		w = append(w, int64(x))
	}
	sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
	if len(got) != len(w) {
		return false
	}
	for i := range got {
		if got[i] != w[i] {
			return false
		}
	}
	return true
}

func TestRESTAuditCollectionFilters(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	mk := func(path string, body map[string]any) float64 {
		t.Helper()
		code, out, _, _ := s.admin.do("POST", path, body)
		if code != http.StatusCreated {
			t.Fatalf("POST %s: %d %v", path, code, out)
		}
		return out["id"].(float64)
	}
	catA := mk("/wp/v2/categories", map[string]any{"name": "Alpha"})
	catB := mk("/wp/v2/categories", map[string]any{"name": "Beta"})
	tag := mk("/wp/v2/tags", map[string]any{"name": "Tee"})
	p1 := mk("/wp/v2/posts", map[string]any{"title": "Filter one", "slug": "filter-one", "status": "publish", "date": "2020-01-01T10:00:00", "categories": []any{catA}, "tags": []any{tag}})
	p2 := mk("/wp/v2/posts", map[string]any{"title": "Filter two", "slug": "filter-two", "status": "publish", "date": "2021-01-01T10:00:00", "categories": []any{catA}, "sticky": true})
	p3 := mk("/wp/v2/posts", map[string]any{"title": "Filter three", "slug": "filter-three", "status": "publish", "date": "2022-01-01T10:00:00", "categories": []any{catB}, "tags": []any{tag}})
	if _, err := s.srv.store.Pool.Exec(ctx, `UPDATE posts SET modified_at = '2019-01-01' WHERE id = $1`, int64(p1)); err != nil {
		t.Fatal(err)
	}
	scope := "&include=" + itoa64(int64(p1)) + "," + itoa64(int64(p2)) + "," + itoa64(int64(p3))
	cases := []struct {
		q    string
		want []float64
	}{
		{"categories=" + itoa64(int64(catA)) + "&tags=" + itoa64(int64(tag)), []float64{p1}},
		{"categories=" + itoa64(int64(catA)) + "&tags=" + itoa64(int64(tag)) + "&tax_relation=OR", []float64{p1, p2, p3}},
		{"categories_exclude=" + itoa64(int64(catA)), []float64{p3}},
		{"tags_exclude=" + itoa64(int64(tag)), []float64{p2}},
		{"after=2020-06-01T00:00:00", []float64{p2, p3}},
		{"before=2021-06-01T00:00:00", []float64{p1, p2}},
		{"after=2020-06-01T00:00:00&before=2021-06-01T00:00:00", []float64{p2}},
		{"modified_after=2025-01-01T00:00:00", []float64{p2, p3}},
		{"modified_before=2025-01-01T00:00:00", []float64{p1}},
		{"orderby=id&order=asc&per_page=1&offset=1", []float64{p2}},
		{"author=1,999999", []float64{p1, p2, p3}},
		{"author_exclude=1", nil},
		{"slug=filter-one,filter-three", []float64{p1, p3}},
		{"sticky=true", []float64{p2}},
		{"sticky=false", []float64{p1, p3}},
	}
	for _, tc := range cases {
		code, e, list, hdr := s.admin.do("GET", "/wp/v2/posts?"+tc.q+scope, nil)
		if code != http.StatusOK {
			t.Errorf("%s: %d %v", tc.q, code, e)
			continue
		}
		if got := restListIDs(list); !sameIDs(got, tc.want...) {
			t.Errorf("%s: got %v want %v", tc.q, got, tc.want)
		}
		if !strings.Contains(tc.q, "offset") && hdr.Get("X-WP-Total") != itoa64(int64(len(tc.want))) {
			t.Errorf("%s: X-WP-Total %q", tc.q, hdr.Get("X-WP-Total"))
		}
	}
	// slug[] form
	if _, _, list, _ := s.admin.do("GET", "/wp/v2/posts?slug[]=filter-two&slug[]=filter-three"+scope, nil); !sameIDs(restListIDs(list), p2, p3) {
		t.Errorf("slug[]: %v", restListIDs(list))
	}
	// Pages are not post-sticky: ?sticky=false must not empty the page list.
	pg := mk("/wp/v2/pages", map[string]any{"title": "A page", "status": "publish"})
	if _, _, list, _ := s.admin.do("GET", "/wp/v2/pages?sticky=false&include="+itoa64(int64(pg)), nil); !sameIDs(restListIDs(list), pg) {
		t.Errorf("pages sticky=false: %v", restListIDs(list))
	}
	for _, q := range []string{"tax_relation=XOR", "after=yesterday", "modified_before=nope", "offset=-1"} {
		if code, e, _, _ := s.admin.do("GET", "/wp/v2/posts?"+q, nil); code != http.StatusBadRequest || e["code"] != "rest_invalid_param" {
			t.Errorf("%s: %d %v", q, code, e)
		}
	}
}

func TestRESTAuditSearchSubtype(t *testing.T) {
	s := secStart(t)
	anon := s.anon()
	s.post("Searchable story", "publish", "")
	if code, _, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Hidden pattern", "content": "<p>secretword</p>", "status": "publish"}); code != http.StatusCreated {
		t.Fatalf("create pattern: %d", code)
	}
	for _, st := range []string{"wp_block", "post,wp_block", "wpcf7_contact_form"} {
		if code, e, _, _ := anon.do("GET", "/wp/v2/search?subtype="+st+"&search=secretword", nil); code != http.StatusBadRequest || e["code"] != "rest_invalid_param" {
			t.Errorf("subtype %s: %d %v", st, code, e)
		}
	}
	if _, _, list, _ := anon.do("GET", "/wp/v2/search?search=pattern", nil); len(list) != 0 {
		t.Errorf("default search finds a pattern: %v", list)
	}
	if code, _, list, _ := anon.do("GET", "/wp/v2/search?subtype=post&search=Searchable", nil); code != http.StatusOK || len(list) != 1 {
		t.Errorf("subtype=post: %d %v", code, list)
	}
	// Term search reports the real total, not the page length.
	for _, n := range []string{"Termy one", "Termy two", "Termy three"} {
		s.admin.do("POST", "/wp/v2/tags", map[string]any{"name": n})
	}
	if _, _, list, hdr := anon.do("GET", "/wp/v2/search?type=term&subtype=post_tag&search=Termy&per_page=1", nil); len(list) != 1 || hdr.Get("X-WP-Total") != "3" {
		t.Errorf("term search: %d items, total %q", len(list), hdr.Get("X-WP-Total"))
	}
}

func TestRESTAuditCommentRules(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	_, sub := s.user("subby", "subscriber")
	old := s.post("Old post", "publish", "")
	if _, err := s.srv.store.Pool.Exec(ctx, `UPDATE posts SET published_at = now() - interval '60 days' WHERE id = $1`, int64(old)); err != nil {
		t.Fatal(err)
	}
	if err := s.srv.settings.Set(ctx, map[string]any{"close_comments_for_old_posts": true, "close_comments_days_old": 14}); err != nil {
		t.Fatal(err)
	}
	if code, e, _, _ := sub.do("POST", "/wp/v2/comments", map[string]any{"post": old, "content": "late reply"}); code != http.StatusForbidden || e["code"] != "rest_comment_closed" {
		t.Errorf("comment on an auto-closed post: %d %v", code, e)
	}
	fresh := s.post("Fresh post", "publish", "")
	if code, e, _, _ := sub.do("POST", "/wp/v2/comments", map[string]any{"post": fresh, "content": "first"}); code != http.StatusCreated {
		t.Fatalf("first comment: %d %v", code, e)
	}
	if code, e, _, _ := sub.do("POST", "/wp/v2/comments", map[string]any{"post": fresh, "content": "second"}); code != http.StatusTooManyRequests || e["code"] != "comment_flood" {
		t.Errorf("flood: %d %v", code, e)
	}
	// Moderators are not throttled.
	s.comment(fresh, "mod one", "m@example.test")
	s.comment(fresh, "mod two", "m@example.test")

	// Moving a comment recounts both posts.
	a := s.post("Move from", "publish", "")
	b := s.post("Move to", "publish", "")
	cid := s.comment(a, "travelling comment", "t@example.test")
	if p, _ := s.srv.store.GetPost(ctx, int64(a)); p.CommentCount != 1 {
		t.Fatalf("count before move: %d", p.CommentCount)
	}
	if code, e, _, _ := s.admin.do("PUT", "/wp/v2/comments/"+itoa64(int64(cid)), map[string]any{"post": b}); code != http.StatusOK {
		t.Fatalf("move: %d %v", code, e)
	}
	pa, _ := s.srv.store.GetPost(ctx, int64(a))
	pb, _ := s.srv.store.GetPost(ctx, int64(b))
	if pa.CommentCount != 0 || pb.CommentCount != 1 {
		t.Errorf("counts after move: from %d, to %d", pa.CommentCount, pb.CommentCount)
	}
}

func TestRESTAuditBlocksAccess(t *testing.T) {
	s := secStart(t)
	anon := s.anon()
	_, contrib := s.user("conny", "contributor")
	code, made, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Internal snippet", "content": "<p>internal</p>"})
	if code != http.StatusCreated || made["status"] != "publish" {
		t.Fatalf("admin pattern: %d %v", code, made["status"])
	}
	if code, _, list, _ := anon.do("GET", "/wp/v2/blocks", nil); code != http.StatusOK || len(list) != 0 {
		t.Errorf("anonymous pattern list: %d %v", code, list)
	}
	if code, _, _, _ := anon.do("GET", "/wp/v2/blocks/"+itoa64(int64(made["id"].(float64))), nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous pattern read: %d", code)
	}
	if code, _, list, _ := contrib.do("GET", "/wp/v2/blocks", nil); code != http.StatusOK || len(list) != 1 {
		t.Errorf("contributor pattern list: %d %v", code, list)
	}
	code, mine, _, _ := contrib.do("POST", "/wp/v2/blocks", map[string]any{"title": "Contrib pattern", "content": "<p>c</p>"})
	if code != http.StatusCreated || mine["status"] == "publish" {
		t.Errorf("contributor pattern published: %d %v", code, mine["status"])
	}
}

func TestRESTAuditSettingsValidation(t *testing.T) {
	s := secStart(t)
	before := s.srv.settings.String("siteurl")
	for _, body := range []map[string]any{
		{"url": "example.com/"},
		{"email": "nope"},
		{"posts_per_page": "ten"},
		{"posts_per_page": 0},
		{"show_on_front": "banana"},
		{"page_on_front": map[string]any{"a": 1}},
		{"page_on_front": 99999999},
		{"title": 12},
		{"language": "xx_YY"},
		{"timezone": "Mars/Olympus"},
		{"use_smilies": "maybe"},
		{"title": "Good title", "posts_per_page": "ten"}, // nothing is written when one value is bad
	} {
		if code, e, _, _ := s.admin.do("POST", "/wp/v2/settings", body); code != http.StatusBadRequest || e["code"] != "rest_invalid_param" {
			t.Errorf("%v: %d %v", body, code, e)
		}
	}
	if s.srv.settings.String("siteurl") != before || s.srv.settings.String("blogname") == "Good title" {
		t.Error("an invalid request changed settings")
	}
	code, out, _, _ := s.admin.do("POST", "/wp/v2/settings", map[string]any{"posts_per_page": "7", "url": "https://example.test/", "show_on_front": "posts", "use_smilies": false, "timezone": "Europe/Istanbul"})
	if code != http.StatusOK {
		t.Fatalf("valid settings: %d %v", code, out)
	}
	if s.srv.settings.Int("posts_per_page") != 7 || s.srv.settings.String("siteurl") != "https://example.test" || s.srv.settings.Bool("use_smilies") {
		t.Errorf("stored: %v %q %v", s.srv.settings.Get("posts_per_page"), s.srv.settings.String("siteurl"), s.srv.settings.Get("use_smilies"))
	}
}

func TestRESTAuditReadableDraftsAndAutosave(t *testing.T) {
	s := secStart(t)
	for i := 0; i < 3; i++ {
		s.post("Admin draft", "draft", "")
	}
	_, contrib := s.user("conny", "contributor")
	if code, _, _, _ := contrib.do("POST", "/wp/v2/posts", map[string]any{"title": "My draft"}); code != http.StatusCreated {
		t.Fatalf("contributor draft: %d", code)
	}
	code, _, list, hdr := contrib.do("GET", "/wp/v2/posts?status=draft&per_page=1", nil)
	if code != http.StatusOK || len(list) != 1 || hdr.Get("X-WP-Total") != "1" {
		t.Errorf("contributor drafts: %d %d items, total %q", code, len(list), hdr.Get("X-WP-Total"))
	}
	if code, _, _, hdr := s.admin.do("GET", "/wp/v2/posts?status=draft", nil); code != http.StatusOK || hdr.Get("X-WP-Total") != "4" {
		t.Errorf("admin drafts total %q", hdr.Get("X-WP-Total"))
	}

	pid := s.post("Autosaved", "publish", "")
	ps := itoa64(int64(pid))
	code, as, _, _ := s.admin.do("POST", "/wp/v2/posts/"+ps+"/autosaves", map[string]any{"content": "autosaved text"})
	if code != http.StatusOK {
		t.Fatalf("autosave: %d %v", code, as)
	}
	aid := itoa64(int64(as["id"].(float64)))
	code, one, _, _ := s.admin.do("GET", "/wp/v2/posts/"+ps+"/autosaves/"+aid, nil)
	if code != http.StatusOK || one["id"] != as["id"] || one["parent"] != pid {
		t.Errorf("get autosave: %d %v", code, one)
	}
	if code, e, _, _ := s.admin.do("GET", "/wp/v2/posts/"+ps+"/autosaves/99999999", nil); code != http.StatusNotFound || e["code"] != "rest_post_invalid_id" {
		t.Errorf("missing autosave: %d %v", code, e)
	}
}
