package server

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// TestDuplicatePost follows the posts list's Duplicate link: the copy is a draft by the current
// user with the content, meta, terms, featured image and Web Builder doc of the original.
func TestDuplicatePost(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()
	var uid, catID int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM terms WHERE taxonomy = 'category' ORDER BY id LIMIT 1`).Scan(&catID)

	src := &store.Post{Type: "page", Title: "About us", Status: "publish", AuthorID: uid, Content: "<p>Hello</p>", Excerpt: "short",
		CommentStatus: "closed", PingStatus: "closed", MenuOrder: 3,
		Meta: map[string]any{"_wp_page_template": "full-width", "subtitle": "Team", "_edit_lock": "123:1", "_wp_old_slug": "about"}}
	if err := srv.store.InsertPost(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetPostTerms(ctx, src.ID, "category", []int64{catID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO web_builder_docs (post_id, doc, html) VALUES ($1, '{"v":1}', '<p>built</p>')`, src.ID); err != nil {
		t.Fatal(err)
	}

	_, list, _ := get(t, c, base+"/wp-admin/edit?post_type=page")
	m := regexp.MustCompile(`href="(/wp-admin/post\?post=` + itoa64(src.ID) + `&amp;action=duplicate&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(list)
	if m == nil {
		t.Fatal("no Duplicate row action")
	}
	if !strings.Contains(list, `>Quick&nbsp;Edit</button> | </span><span class="duplicate">`) {
		t.Error("Duplicate is not after Quick Edit")
	}
	code, _, loc := get(t, c, base+html.UnescapeString(m[1]))
	if code != http.StatusFound || !strings.Contains(loc, "duplicated=") {
		t.Fatalf("duplicate: %d %s", code, loc)
	}
	var copyID int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE title = 'About us (Copy)'`).Scan(&copyID)
	cp, err := srv.store.GetPost(ctx, copyID)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Status != "draft" || cp.Type != "page" || cp.Content != src.Content || cp.Excerpt != "short" || cp.MenuOrder != 3 ||
		cp.CommentStatus != "closed" || cp.AuthorID != uid {
		t.Errorf("copy: %+v", cp)
	}
	if cp.Meta["_wp_page_template"] != "full-width" || cp.Meta["subtitle"] != "Team" || cp.Meta["_edit_lock"] != nil || cp.Meta["_wp_old_slug"] != nil {
		t.Errorf("copy meta: %v", cp.Meta)
	}
	var terms, docs int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM post_terms WHERE post_id = $1 AND term_id = $2`, copyID, catID).Scan(&terms)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM web_builder_docs WHERE post_id = $1 AND html = '<p>built</p>'`, copyID).Scan(&docs)
	if terms != 1 || docs != 1 {
		t.Errorf("terms %d, builder docs %d", terms, docs)
	}
	if _, page, _ := get(t, c, base+loc); !strings.Contains(page, "duplicated as a new draft") {
		t.Error("no notice after duplicating")
	}

	// A bad nonce is refused; a trashed post has no Duplicate link.
	if code, _, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(src.ID)+"&action=duplicate&_wpnonce=bad"); code == http.StatusFound {
		t.Error("duplicate with a bad nonce")
	}
	_ = srv.store.TrashPost(ctx, src.ID)
	if _, list, _ := get(t, c, base+"/wp-admin/edit?post_type=page&post_status=trash"); strings.Contains(list, "action=duplicate") {
		t.Error("Duplicate offered in the Trash")
	}
}
