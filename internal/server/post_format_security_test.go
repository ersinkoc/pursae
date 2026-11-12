package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// A post format is one of the core slugs. The classic editor, REST and a value already in the
// database must not be able to put markup into the theme's format text or the editor body class.
func TestPostFormatAllowlist(t *testing.T) {
	if store.CanonicalPostFormat("aside") != "aside" || store.CanonicalPostFormat("standard") != "standard" ||
		store.CanonicalPostFormat("0") != "standard" || store.CanonicalPostFormat(`x"><script>alert(1)</script>`) != "standard" {
		t.Fatal("CanonicalPostFormat")
	}
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"native_classic_editor": true, "classic-editor-replace": "classic", "classic-editor-allow-users": "disallow"}); err != nil {
		t.Fatal(err)
	}
	p := &store.Post{Type: "post", Title: "Format", Slug: "format-xss", Content: "<p>Hi</p>", Status: "publish", AuthorID: 1,
		CommentStatus: "open", PingStatus: "open", Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	payload := `aside"><script>alert(1)</script>`

	_, edit, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(p.ID)+"&action=edit")
	code, _, _ := post(t, c, base+"/wp-admin/post", url.Values{
		"post_ID": {itoa64(p.ID)}, "_wpnonce": {formNonce(t, edit)}, "post_title": {"Format"},
		"content": {"<p>Hi</p>"}, "post_format": {payload}, "save": {"Save"}, "visibility": {"public"},
	})
	if code != http.StatusFound {
		t.Fatalf("classic save: %d", code)
	}
	if got, _ := srv.store.GetPost(ctx, p.ID); got.Format != "standard" {
		t.Fatalf("classic save stored %q", got.Format)
	}

	admin := newRESTClient(t, c, base)
	code, out, _, _ := admin.do("POST", "/wp/v2/posts/"+itoa64(p.ID), map[string]any{"format": payload})
	if code != http.StatusBadRequest {
		t.Fatalf("REST rejected format: %d %v", code, out)
	}
	if got, _ := srv.store.GetPost(ctx, p.ID); got.Format != "standard" {
		t.Fatalf("REST stored %q", got.Format)
	}
	code, out, _, _ = admin.do("POST", "/wp/v2/posts/"+itoa64(p.ID), map[string]any{"format": "aside"})
	if code != http.StatusOK || out["format"] != "aside" {
		t.Fatalf("REST aside: %d %v", code, out["format"])
	}

	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET format = $2 WHERE id = $1`, p.ID, payload); err != nil {
		t.Fatal(err)
	}
	anon := asNewClient()
	code, _, loc := get(t, anon, base+"/?p="+itoa64(p.ID))
	if code == http.StatusMovedPermanently || code == http.StatusFound {
		if strings.HasPrefix(loc, "/") {
			loc = base + loc
		}
		_, front, _ := get(t, anon, loc)
		if strings.Contains(front, "alert(1)") || strings.Contains(front, payload) {
			t.Fatal("the theme prints the stored format")
		}
		if !strings.Contains(front, "format-standard") {
			t.Error("the theme did not fall back to the standard format")
		}
	} else {
		t.Fatalf("front: %d %s", code, loc)
	}
	_, editor, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(p.ID)+"&action=edit")
	if strings.Contains(editor, "alert(1)") || strings.Contains(editor, payload) {
		t.Fatal("the classic editor body class includes the stored format")
	}
	code, out, _, _ = admin.do("GET", "/wp/v2/posts/"+itoa64(p.ID), nil)
	if code != http.StatusOK || out["format"] != "standard" {
		t.Fatalf("REST format of a stored payload: %d %v", code, out["format"])
	}
}
