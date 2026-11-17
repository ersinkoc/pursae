package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// wbsKit uploads and commits a design kit as the admin.
func wbsKit(t *testing.T, admin *wbrClient, slug string, files map[string]string) {
	t.Helper()
	list := []any{}
	for p, text := range files {
		list = append(list, map[string]any{"path": p, "text": text})
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/"+slug+"/files", map[string]any{"files": list}, nil); code != http.StatusOK {
		t.Fatalf("kit files: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/"+slug+"/commit", map[string]any{}, nil); code != http.StatusOK {
		t.Fatalf("kit commit: %d %v", code, v)
	}
}

// wbsCodePage publishes a page of one code section of the kit as the admin.
func wbsCodePage(t *testing.T, srv *Server, admin *wbrClient, title, kit, html, head string) int64 {
	t.Helper()
	post := wbrPost(t, srv, "page", title, "draft", 1)
	style := map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}}
	if head == "" {
		head = `<link rel="stylesheet" href="/wp-content/uploads/web-builder/kits/` + kit + `/site.css">`
	}
	sec := map[string]any{"id": "c1", "kind": "code", "name": "Hero", "hidden": false, "items": []any{}, "style": style,
		"content": map[string]any{"html": html, "head": head, "kit": kit}}
	doc := map[string]any{"site": map[string]any{"brand": "#2271b1", "fontPair": "modern", "corners": "soft"}, "sections": []any{sec}}
	body := wbrPublishBody(doc, map[string]any{}, 0, "publish", map[string]any{"title": title, "slug": strings.ToLower(strings.ReplaceAll(title, " ", "-")), "indexable": true})
	body["serverRender"] = true
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), body, nil); code != http.StatusOK {
		t.Fatalf("publish %s: %d %v", title, code, v)
	}
	return post.ID
}

// wbsLogin creates a user with the role and logs it in.
func wbsLogin(t *testing.T, srv *Server, base, login, role string) (*wbrClient, int64) {
	t.Helper()
	id := asCreateUser(t, srv, login, role, asStrongPass)
	cc := asNewClient()
	if code, _, _ := asLogin(t, cc, base, "/wp-login", login, asStrongPass); code != http.StatusFound {
		t.Fatalf("login %s: %d", login, code)
	}
	return newWBRClient(t, cc, base), id
}

// GET kits/{slug}/pages gives an author the published, public pages and their own, never another
// user's draft, private or password-protected page; the kit's writer gets every page. The kit list
// and file list are for unfiltered_html authors only.
func TestWBKitPagesVisibility(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	wbsKit(t, admin, "vis", map[string]string{"site.css": ".a{color:red}"})
	pub := wbsCodePage(t, srv, admin, "Public page", "vis", `<p class="pub">public</p>`, "")
	draft := wbsCodePage(t, srv, admin, "Draft page", "vis", `<p class="secret-draft">draft</p>`, "")
	locked := wbsCodePage(t, srv, admin, "Locked page", "vis", `<p class="secret-pass">locked</p>`, "")
	priv := wbsCodePage(t, srv, admin, "Private page", "vis", `<p class="secret-private">private</p>`, "")
	mine := wbsCodePage(t, srv, admin, "Own page", "vis", `<p class="own-draft">own</p>`, "")
	author, authorID := wbsLogin(t, srv, base, "kitauthor", "author")
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE posts SET status = 'draft' WHERE id = $1`, []any{draft}},
		{`UPDATE posts SET password = 'pw' WHERE id = $1`, []any{locked}},
		{`UPDATE posts SET status = 'private' WHERE id = $1`, []any{priv}},
		{`UPDATE posts SET status = 'draft', type = 'post', author_id = $2 WHERE id = $1`, []any{mine, authorID}}, // an author edits posts
	} {
		if _, err := srv.store.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(c *wbrClient) map[int64]string {
		code, v, _ := c.do(http.MethodGet, "/web-builder/v2/kits/vis/pages", nil, nil)
		if code != http.StatusOK {
			t.Fatalf("pages: %d %v", code, v)
		}
		out := map[int64]string{}
		pages, _ := wbrObj(v)["pages"].([]any)
		for _, p := range pages {
			out[int64(wbrObj(p)["id"].(float64))] = strAny(wbrObj(p)["html"])
		}
		return out
	}
	got := ids(author)
	if len(got) != 2 || !strings.Contains(got[pub], "pub") || !strings.Contains(got[mine], "own-draft") {
		t.Errorf("author sees %v", got)
	}
	for _, id := range []int64{draft, locked, priv} {
		if _, ok := got[id]; ok {
			t.Errorf("author sees page %d", id)
		}
	}
	if got := ids(admin); len(got) != 5 {
		t.Errorf("admin sees %d pages, want 5", len(got))
	}
	contributor, _ := wbsLogin(t, srv, base, "kitcontrib", "contributor")
	if got := ids(contributor); len(got) != 1 || got[pub] == "" {
		t.Errorf("contributor sees %v", got)
	}
	// The kit list and its files: unfiltered_html authors only.
	for _, p := range []string{"/web-builder/v2/kits", "/web-builder/v2/kits/vis"} {
		if code, _, _ := author.do(http.MethodGet, p, nil, nil); code != http.StatusForbidden {
			t.Errorf("author GET %s: %d", p, code)
		}
		if code, _, _ := admin.do(http.MethodGet, p, nil, nil); code != http.StatusOK {
			t.Errorf("admin GET %s: %d", p, code)
		}
	}
	// The override stays readable to the editor's users (the frame shows it).
	if code, _, _ := author.do(http.MethodGet, "/web-builder/v2/kits/vis/override", nil, nil); code != http.StatusOK {
		t.Errorf("author GET override: %d", code)
	}
}
