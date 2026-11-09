package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"wpgo/internal/store"
)

// Regression tests for the 0.1.8 core REST fixes.

// TestRESTPostsFix018 covers the posts controller: the edit context of collections, footnotes meta,
// pattern creation, cache purge on force delete and the batched list lookups.
func TestRESTPostsFix018(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	anon := s.anon()
	_, sub := s.user("subby", "subscriber")
	_, contrib := s.user("conny", "contributor")
	_, author := s.user("arthur", "author")

	secret := s.post("Protected one", "publish", "hunter2pw")
	open := s.post("Open one", "publish", "")

	// Collections refuse the edit context without edit_posts (it carries passwords and raw content).
	for _, path := range []string{"/wp/v2/posts?context=edit", "/wp/v2/pages?context=edit", "/wp/v2/blocks?context=edit"} {
		if code, e, _, _ := anon.do("GET", path, nil); code != http.StatusUnauthorized || e["code"] != "rest_forbidden_context" {
			t.Errorf("anonymous %s: %d %v", path, code, e)
		}
	}
	if code, e, _, _ := sub.do("GET", "/wp/v2/posts?context=edit", nil); code != http.StatusForbidden || e["code"] != "rest_forbidden_context" {
		t.Errorf("subscriber edit context: %d %v", code, e)
	}
	// A contributor may use it; another author's post keeps its raw fields but never its password,
	// and a password-protected one also hides its raw content.
	code, _, list, _ := contrib.do("GET", "/wp/v2/posts?context=edit&per_page=100", nil)
	if code != http.StatusOK {
		t.Fatalf("contributor edit context: %d", code)
	}
	found, foundOpen := false, false
	for _, it := range list {
		m := it.(map[string]any)
		switch m["id"] {
		case secret:
			found = true
			if _, ok := m["password"]; ok {
				t.Errorf("contributor reads another author's post password: %v", m["password"])
			}
			if _, ok := m["content"].(map[string]any)["raw"]; ok {
				t.Error("contributor reads another author's protected raw content")
			}
			if _, ok := m["excerpt"].(map[string]any)["raw"]; ok {
				t.Error("contributor reads another author's protected raw excerpt")
			}
		case open:
			foundOpen = true
			if _, ok := m["password"]; ok {
				t.Errorf("contributor gets the password field of another author's post: %v", m["password"])
			}
			if m["content"].(map[string]any)["raw"] != "Open one body words" || m["title"].(map[string]any)["raw"] != "Open one" {
				t.Errorf("contributor misses raw fields of an unprotected post: %v %v", m["title"], m["content"])
			}
		}
	}
	if !found || !foundOpen {
		t.Errorf("posts missing from the contributor's list: protected %v, open %v", found, foundOpen)
	}
	// The block editor's inserter builds other users' patterns from content.raw (context=edit).
	pat := `<!-- wp:paragraph --><p>Admin pattern</p><!-- /wp:paragraph -->`
	code, made, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Admin pattern", "content": pat, "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("admin create pattern: %d %v", code, made)
	}
	code, _, list, _ = author.do("GET", "/wp/v2/blocks?context=edit&per_page=100", nil)
	gotPat := false
	for _, it := range list {
		m := it.(map[string]any)
		if m["id"] == made["id"] {
			gotPat = m["content"].(map[string]any)["raw"] == pat && m["title"].(map[string]any)["raw"] == "Admin pattern"
		}
	}
	if code != http.StatusOK || !gotPat {
		t.Errorf("author lists an admin pattern without content.raw: %d %v", code, list)
	}
	code, _, list, _ = s.admin.do("GET", "/wp/v2/posts?context=edit&include="+strconv.Itoa(int(secret)), nil)
	if code != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["password"] != "hunter2pw" {
		t.Errorf("admin edit context: %d %v", code, list)
	}

	// Footnotes are read and written through meta.footnotes.
	notes := `[{"id":"fn-1","content":"First note"}]`
	code, created, _, _ := s.admin.do("POST", "/wp/v2/posts", map[string]any{"title": "With notes", "content": "<p>x</p>", "status": "publish", "meta": map[string]any{"footnotes": notes}})
	if code != http.StatusCreated || created["meta"].(map[string]any)["footnotes"] != notes {
		t.Fatalf("create with footnotes: %d %v", code, created["meta"])
	}
	fnID := int64(created["id"].(float64))
	if code, got, _, _ := s.admin.do("GET", "/wp/v2/posts/"+itoa64(fnID)+"?context=edit", nil); code != http.StatusOK || got["meta"].(map[string]any)["footnotes"] != notes {
		t.Errorf("footnotes round trip: %d %v", code, got["meta"])
	}
	if p, _ := s.srv.store.GetPost(ctx, fnID); metaString(p.Meta, "footnotes") != notes {
		t.Errorf("footnotes not stored: %v", p.Meta)
	}
	// Authors without unfiltered_html get them filtered.
	code, own, _, _ := author.do("POST", "/wp/v2/posts", map[string]any{"title": "Author notes", "meta": map[string]any{"footnotes": `[{"id":"a","content":"ok<script>alert(1)</script>"}]`}})
	if code != http.StatusCreated {
		t.Fatalf("author create: %d %v", code, own)
	}
	if fn, _ := own["meta"].(map[string]any)["footnotes"].(string); fn == "" || strings.Contains(fn, "<script") {
		t.Errorf("author footnotes not filtered: %q", fn)
	}

	// A new pattern with a sync status (meta was nil on create).
	if code, b, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Unsynced", "content": "<p>p</p>", "wp_pattern_sync_status": "unsynced"}); code != http.StatusCreated || b["wp_pattern_sync_status"] != "unsynced" {
		t.Errorf("create unsynced pattern: %d %v", code, b)
	}

	// Force delete purges the post from Pursae Cache before the row goes.
	var mu sync.Mutex
	var purged []int64
	prev := s.srv.onPostSaved
	s.srv.onPostSaved = func(ctx context.Context, p *store.Post) {
		mu.Lock()
		purged = append(purged, p.ID)
		mu.Unlock()
		if prev != nil {
			prev(ctx, p)
		}
	}
	victim := int64(s.post("Doomed", "publish", ""))
	mu.Lock()
	purged = nil
	mu.Unlock()
	if code, out, _, _ := s.admin.do("DELETE", "/wp/v2/posts/"+itoa64(victim)+"?force=true", nil); code != http.StatusOK || out["deleted"] != true {
		t.Fatalf("force delete: %d %v", code, out)
	}
	mu.Lock()
	if len(purged) == 0 || purged[0] != victim {
		t.Errorf("force delete did not purge: %v", purged)
	}
	mu.Unlock()
	s.srv.onPostSaved = prev

	// The list output with batched lookups matches the single-post output.
	code, tagged, _, _ := s.admin.do("POST", "/wp/v2/tags", map[string]any{"name": "Batched"})
	if code != http.StatusCreated {
		t.Fatalf("create tag: %d %v", code, tagged)
	}
	code, p, _, _ := s.admin.do("POST", "/wp/v2/posts", map[string]any{"title": "Embed me", "content": "<p>one</p>", "status": "publish", "tags": []any{tagged["id"]}})
	if code != http.StatusCreated {
		t.Fatalf("create post: %d %v", code, p)
	}
	pid := itoa64(int64(p["id"].(float64)))
	if code, _, _, _ := s.admin.do("POST", "/wp/v2/posts/"+pid, map[string]any{"content": "<p>two</p>"}); code != http.StatusOK {
		t.Fatalf("update post: %d", code)
	}
	_, single, _, _ := s.admin.do("GET", "/wp/v2/posts/"+pid+"?_embed", nil)
	code, _, list, _ = s.admin.do("GET", "/wp/v2/posts?_embed&include="+pid, nil)
	if code != http.StatusOK || len(list) != 1 {
		t.Fatalf("list: %d %d", code, len(list))
	}
	item := list[0].(map[string]any)
	vh := func(m map[string]any) any {
		return m["_links"].(map[string]any)["version-history"].([]any)[0].(map[string]any)["count"]
	}
	if vh(item) != vh(single) || vh(item) == float64(0) {
		t.Errorf("version-history: list %v, single %v", vh(item), vh(single))
	}
	if len(item["tags"].([]any)) != 1 || len(item["categories"].([]any)) != 1 {
		t.Errorf("terms: %v %v", item["tags"], item["categories"])
	}
	emb := item["_embedded"].(map[string]any)
	if emb["author"].([]any)[0].(map[string]any)["slug"] != "admin" {
		t.Errorf("embedded author: %v", emb["author"])
	}
	terms := emb["wp:term"].([]any)
	if len(terms) != 2 || len(terms[1].([]any)) != 1 || terms[1].([]any)[0].(map[string]any)["name"] != "Batched" {
		t.Errorf("embedded terms: %v", terms)
	}
}

// TestRESTTermsMediaBatchFix018 covers tag updates by contributors, anonymous media writes and nested batches.
func TestRESTTermsMediaBatchFix018(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	anon := s.anon()
	_, sub := s.user("subby", "subscriber")
	_, contrib := s.user("conny", "contributor")

	// Contributors create tags but cannot rename existing ones.
	code, tag, _, _ := contrib.do("POST", "/wp/v2/tags", map[string]any{"name": "Fresh"})
	if code != http.StatusCreated {
		t.Fatalf("contributor create tag: %d %v", code, tag)
	}
	tid := itoa64(int64(tag["id"].(float64)))
	if code, e, _, _ := contrib.do("POST", "/wp/v2/tags/"+tid, map[string]any{"name": "Hijacked", "slug": "hijacked"}); code != http.StatusForbidden || e["code"] != "rest_cannot_update" {
		t.Errorf("contributor update tag: %d %v", code, e)
	}
	if code, e, _, _ := anon.do("POST", "/wp/v2/tags/"+tid, map[string]any{"name": "Anon"}); code != http.StatusUnauthorized {
		t.Errorf("anonymous update tag: %d %v", code, e)
	}
	if code, upd, _, _ := s.admin.do("POST", "/wp/v2/tags/"+tid, map[string]any{"name": "Renamed"}); code != http.StatusOK || upd["name"] != "Renamed" {
		t.Errorf("admin update tag: %d %v", code, upd)
	}

	// Media writes without a user answer 401, not a nil-pointer 500.
	var adminID int64
	_ = s.srv.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE login = 'admin'`).Scan(&adminID)
	a := &store.Attachment{UploaderID: adminID, Slug: "fix018", FilePath: "2026/11/fix018.jpg", Mime: "image/jpeg", Title: "fix018"}
	if err := s.srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	mid := itoa64(a.ID)
	if code, e, _, _ := anon.do("POST", "/wp/v2/media/"+mid, map[string]any{"title": "x"}); code != http.StatusUnauthorized || e["code"] != "rest_cannot_edit" {
		t.Errorf("anonymous media update: %d %v", code, e)
	}
	if code, e, _, _ := anon.do("DELETE", "/wp/v2/media/"+mid+"?force=true", nil); code != http.StatusUnauthorized || e["code"] != "rest_cannot_delete" {
		t.Errorf("anonymous media delete: %d %v", code, e)
	}
	if code, e, _, _ := sub.do("POST", "/wp/v2/media/"+mid, map[string]any{"title": "x"}); code != http.StatusForbidden || e["code"] != "rest_cannot_edit" {
		t.Errorf("subscriber media update: %d %v", code, e)
	}
	if code, upd, _, _ := s.admin.do("POST", "/wp/v2/media/"+mid, map[string]any{"title": "Renamed"}); code != http.StatusOK || upd["title"].(map[string]any)["raw"] != "Renamed" {
		t.Errorf("admin media update: %d %v", code, upd)
	}

	// Batches cannot nest, whatever the spelling of the path.
	inner := map[string]any{"requests": []any{map[string]any{"method": "POST", "path": "/wp/v2/posts", "body": map[string]any{"title": "x"}}}}
	code, out, _, _ := anon.do("POST", "/batch/v1", map[string]any{"requests": []any{
		map[string]any{"method": "POST", "path": "//batch/v1", "body": inner},
		map[string]any{"method": "POST", "path": "/%62atch/v1", "body": inner},
		map[string]any{"method": "POST", "path": "/BATCH/v1", "body": inner},
	}})
	if code != http.StatusMultiStatus {
		t.Fatalf("nested batch: %d %v", code, out)
	}
	for i, r := range out["responses"].([]any) {
		m := r.(map[string]any)
		if m["status"] != float64(http.StatusBadRequest) || m["body"].(map[string]any)["code"] != "rest_batch_not_allowed" {
			t.Errorf("nested batch %d ran: %v", i, m)
		}
	}
	// A plain batch still works.
	code, out, _, _ = s.admin.do("POST", "/batch/v1", map[string]any{"requests": []any{
		map[string]any{"method": "POST", "path": "/wp/v2/posts", "body": map[string]any{"title": "From batch"}},
	}})
	if code != http.StatusMultiStatus || out["responses"].([]any)[0].(map[string]any)["status"] != float64(http.StatusCreated) {
		t.Errorf("plain batch: %d %v", code, out)
	}
}

// TestRESTUsersCommentsFix018 covers the users list (search columns, capabilities in SQL, preferences)
// and the comments list (SQL filters and readability), plus deleting a user's content.
func TestRESTUsersCommentsFix018(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	pool := s.srv.store.Pool
	_, sub := s.user("subby", "subscriber")
	_, _ = s.user("conny", "contributor")
	_, authorA := s.user("arthur", "author")
	asCreateUser(t, s.srv, "denied", "author", asStrongPass)
	for i := 0; i < 3; i++ {
		asCreateUser(t, s.srv, "cust"+strconv.Itoa(i), "subscriber", asStrongPass)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_capabilities (user_id, capability, effect) SELECT id, 'edit_posts', 'deny' FROM users WHERE login = 'denied'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET meta = meta || '{"persisted_preferences":{"core":{"fixedToolbar":true}}}'::jsonb WHERE login = 'admin'`); err != nil {
		t.Fatal(err)
	}
	var adminID int64
	_ = pool.QueryRow(ctx, `SELECT id FROM users WHERE login = 'admin'`).Scan(&adminID)
	s.post("Admin public", "publish", "")

	// who=authors resolves edit_posts in SQL: roles, minus the deny exception; pagination stays right.
	logins := func(list []any) map[string]bool {
		out := map[string]bool{}
		for _, it := range list {
			out[it.(map[string]any)["slug"].(string)] = true
		}
		return out
	}
	code, _, list, hdr := s.admin.do("GET", "/wp/v2/users?who=authors&per_page=100", nil)
	got := logins(list)
	if code != http.StatusOK || !got["admin"] || !got["arthur"] || !got["conny"] || got["denied"] || got["subby"] || got["cust0"] || hdr.Get("X-WP-Total") != "3" {
		t.Errorf("who=authors: %d %v total %s", code, got, hdr.Get("X-WP-Total"))
	}
	if code, _, list, hdr := s.admin.do("GET", "/wp/v2/users?who=authors&per_page=1&page=2", nil); code != http.StatusOK || len(list) != 1 || hdr.Get("X-WP-Total") != "3" || hdr.Get("X-WP-TotalPages") != "3" {
		t.Errorf("who=authors page 2: %d %d %v", code, len(list), hdr)
	}
	if code, _, list, _ := s.admin.do("GET", "/wp/v2/users?capabilities=edit_posts&offset=1&per_page=10", nil); code != http.StatusOK || len(list) != 2 {
		t.Errorf("capabilities with offset: %d %d", code, len(list))
	}
	if code, _, list, _ := s.admin.do("GET", "/wp/v2/users?capabilities=edit_post", nil); code != http.StatusOK || len(list) != 0 {
		t.Errorf("meta capability without a post: %d %d", code, len(list))
	}
	// Admin search still covers email.
	if code, _, list, _ := s.admin.do("GET", "/wp/v2/users?search=arthur%40example", nil); code != http.StatusOK || len(list) != 1 {
		t.Errorf("admin email search: %d %d", code, len(list))
	}

	// Without list_users, search covers id, login, slug and name only; LIKE wildcards are literal.
	for _, q := range []string{"admin%40example", "example.test", url.QueryEscape("%"), url.QueryEscape("_")} {
		if code, _, list, _ := sub.do("GET", "/wp/v2/users?search="+q, nil); code != http.StatusOK || len(list) != 0 {
			t.Errorf("subscriber search %q: %d %v", q, code, list)
		}
	}
	if code, _, list, _ := sub.do("GET", "/wp/v2/users?search=adm", nil); code != http.StatusOK || len(list) != 1 {
		t.Errorf("subscriber login search: %d %d", code, len(list))
	}

	// persisted_preferences is edit-context only.
	if code, u, _, _ := sub.do("GET", "/wp/v2/users/"+itoa64(adminID), nil); code != http.StatusOK || len(u["meta"].(map[string]any)) != 0 {
		t.Errorf("view context meta: %d %v", code, u["meta"])
	}
	if code, u, _, _ := s.admin.do("GET", "/wp/v2/users/me?context=edit", nil); code != http.StatusOK || u["meta"].(map[string]any)["persisted_preferences"] == nil {
		t.Errorf("edit context meta: %d %v", code, u["meta"])
	}

	// Comments: anonymous lists see only comments of public posts, filtered and paginated in SQL.
	pub := s.post("Commented", "publish", "")
	c1 := s.comment(pub, "first public", "v1@example.test")
	c2 := s.comment(pub, "second public", "v2@example.test")
	code, draft, _, _ := authorA.do("POST", "/wp/v2/posts", map[string]any{"title": "Author draft", "status": "draft"})
	if code != http.StatusCreated {
		t.Fatalf("author draft: %d %v", code, draft)
	}
	var hidden int64
	if err := pool.QueryRow(ctx, `INSERT INTO comments (post_id, content, status, author_name) VALUES ($1, 'on a draft', 'approved', 'X') RETURNING id`, int64(draft["id"].(float64))).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	ids := func(list []any) map[float64]bool {
		out := map[float64]bool{}
		for _, it := range list {
			out[it.(map[string]any)["id"].(float64)] = true
		}
		return out
	}
	anon := s.anon()
	code, _, list, hdr = anon.do("GET", "/wp/v2/comments?per_page=100", nil)
	all := ids(list)
	if code != http.StatusOK || !all[c1] || !all[c2] || all[float64(hidden)] || hdr.Get("X-WP-Total") != strconv.Itoa(len(list)) {
		t.Errorf("anonymous comments: %d %v total %s", code, all, hdr.Get("X-WP-Total"))
	}
	if code, _, page, hdr := anon.do("GET", "/wp/v2/comments?per_page=1", nil); code != http.StatusOK || len(page) != 1 || hdr.Get("X-WP-Total") != strconv.Itoa(len(list)) {
		t.Errorf("anonymous per_page=1: %d %d %s", code, len(page), hdr.Get("X-WP-Total"))
	}
	if code, _, l, _ := anon.do("GET", "/wp/v2/comments?post="+itoa64(int64(pub))+"&exclude="+itoa64(int64(c1)), nil); code != http.StatusOK || len(l) != 1 || !ids(l)[c2] {
		t.Errorf("post + exclude: %d %v", code, ids(l))
	}
	if code, _, l, _ := anon.do("GET", "/wp/v2/comments?include="+itoa64(hidden), nil); code != http.StatusOK || len(l) != 0 {
		t.Errorf("include of a draft's comment: %d %v", code, ids(l))
	}
	// The draft's author still sees the comment on it.
	if code, _, l, _ := authorA.do("GET", "/wp/v2/comments?per_page=100", nil); code != http.StatusOK || !ids(l)[float64(hidden)] {
		t.Errorf("author's own draft comment: %d %v", code, ids(l))
	}
	if code, _, l, _ := s.admin.do("GET", "/wp/v2/comments?author_email=V1@example.test", nil); code != http.StatusOK || len(l) != 1 || !ids(l)[c1] {
		t.Errorf("author_email filter: %d %v", code, ids(l))
	}

	// Deleting a user with their content keeps post types without author support.
	gone := asCreateUser(t, s.srv, "leaver", "author", asStrongPass)
	cat := int64(s.srv.settings.Int("default_category"))
	var before int
	_ = pool.QueryRow(ctx, `SELECT post_count FROM terms WHERE id = $1`, cat).Scan(&before)
	mk := func(typ string) int64 {
		p := &store.Post{Type: typ, AuthorID: gone, Status: "publish", Title: "Leaver " + typ, Content: "x"}
		if err := s.srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	postID, productID, blockID := mk("post"), mk("product"), mk("wp_block")
	if err := s.srv.store.SetPostTerms(ctx, postID, "category", []int64{cat}); err != nil {
		t.Fatal(err)
	}
	if err := s.srv.store.DeleteUser(ctx, gone, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.store.GetPost(ctx, postID); err == nil {
		t.Error("the user's post survived")
	}
	for _, id := range []int64{productID, blockID} {
		p, err := s.srv.store.GetPost(ctx, id)
		if err != nil {
			t.Errorf("post %d deleted with its author", id)
		} else if p.AuthorID != 0 {
			t.Errorf("post %d still has author %d", id, p.AuthorID)
		}
	}
	var after int
	_ = pool.QueryRow(ctx, `SELECT post_count FROM terms WHERE id = $1`, cat).Scan(&after)
	if after != before {
		t.Errorf("category count %d after delete, want %d", after, before)
	}
}
