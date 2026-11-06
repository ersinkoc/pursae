package server

import (
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

func TestWBNavigator(t *testing.T) {
	srv, base, admin := wbrStart(t)
	code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": "header", "title": "Main <b>header</b>"}, nil)
	if code != http.StatusCreated {
		t.Fatalf("create template: %d %v", code, v)
	}
	tplID := int64(wbrObj(v)["id"].(float64))

	authorID := asCreateUser(t, srv, "nav-author", "author", asStrongPass)
	own := wbrPost(t, srv, "page", "Author draft", "draft", authorID)
	other := wbrPost(t, srv, "page", "Admin page", "publish", 1)
	trashed := wbrPost(t, srv, "page", "Gone", "trash", 1)
	adminDraft := wbrPost(t, srv, "page", "Admin draft", "draft", 1)

	rows := func(v any, key string) []map[string]any {
		var out []map[string]any
		for _, r := range wbrObj(v)[key].([]any) {
			out = append(out, wbrObj(r))
		}
		return out
	}
	hasPost := func(v any, id int64) bool {
		for _, r := range rows(v, "posts") {
			if int64(r["id"].(float64)) == id {
				return true
			}
		}
		return false
	}

	// Admin: the template and every non-trashed page.
	code, v, _ = admin.do(http.MethodGet, "/web-builder/v2/navigator", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("admin: %d %v", code, v)
	}
	ts := rows(v, "templates")
	if len(ts) != 1 || int64(ts[0]["id"].(float64)) != tplID || ts[0]["type"] != "header" || ts[0]["typeLabel"] != webbuilder.TemplateTypeLabels[webbuilder.TemplateHeader] ||
		ts[0]["disabled"] != true || ts[0]["status"] != "draft" || !strings.HasSuffix(ts[0]["editUrl"].(string), wbEditURL(tplID)) {
		t.Errorf("template row %v", ts)
	}
	for _, p := range []int64{own.ID, other.ID, adminDraft.ID} {
		if !hasPost(v, p) {
			t.Errorf("admin list lacks post %d", p)
		}
	}
	if hasPost(v, trashed.ID) {
		t.Error("trashed post listed")
	}
	for _, r := range rows(v, "posts") {
		if r["postType"] != "page" && r["postType"] != "post" || r["typeLabel"] == "" || !strings.HasSuffix(r["editUrl"].(string), wbEditURL(int64(r["id"].(float64)))) {
			t.Errorf("post row %v", r)
		}
	}

	// Editor: no templates, every page.
	ed := wbrUser(t, srv, base, "nav-editor", "editor")
	code, v, _ = ed.do(http.MethodGet, "/web-builder/v2/navigator", nil, nil)
	if code != http.StatusOK || len(rows(v, "templates")) != 0 || !hasPost(v, other.ID) || !hasPost(v, own.ID) || hasPost(v, trashed.ID) {
		t.Errorf("editor: %d %v", code, v)
	}

	// Author: no templates, only their own pages (an author cannot edit pages at all: only posts).
	au := newAuthorClient(t, base)
	code, v, _ = au.do(http.MethodGet, "/web-builder/v2/navigator", nil, nil)
	if code != http.StatusOK || len(rows(v, "templates")) != 0 || hasPost(v, other.ID) || hasPost(v, adminDraft.ID) {
		t.Errorf("author: %d %v", code, v)
	}
}

func newAuthorClient(t *testing.T, base string) *wbrClient {
	t.Helper()
	cc := asNewClient()
	if code, _, _ := asLogin(t, cc, base, "/wp-login", "nav-author", asStrongPass); code != http.StatusFound {
		t.Fatalf("login author: %d", code)
	}
	return newWBRClient(t, cc, base)
}

func TestWBNavigatorNeedsHandshake(t *testing.T) {
	_, _, admin := wbrStart(t)
	code, _, _ := admin.do(http.MethodGet, "/web-builder/v2/navigator", nil, map[string]string{webbuilder.APIHeader: "0"})
	if code != http.StatusConflict {
		t.Errorf("stale handshake: %d", code)
	}
}
