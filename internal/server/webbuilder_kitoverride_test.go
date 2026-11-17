package server

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Kit overrides (element editor, classes and variables): the draft is written and versioned, a
// stale base is a conflict, invalid CSS is refused, publishing makes it live on every page whose
// code head loads the kit (after the design's stylesheets) without republishing the pages, and a
// page of a kit without an override prints exactly what it printed before.
func TestWBKitOverride(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	files := map[string]any{"files": []any{map[string]any{"path": "css/site.css", "text": "@layer utilities{.rounded-2xl{border-radius:1rem}} :root{--color-brand-600:#0d9488}"}}}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/ovkit/files", files, nil); code != http.StatusOK {
		t.Fatalf("kit files: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/ovkit/commit", map[string]any{"name": "Override kit"}, nil); code != http.StatusOK {
		t.Fatalf("kit commit: %d %v", code, v)
	}

	post := wbrPost(t, srv, "page", "Override page", "draft", 1)
	style := map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}}
	head := `<link rel="stylesheet" href="/wp-content/uploads/web-builder/kits/ovkit/css/site.css">`
	sec := map[string]any{"id": "c1", "kind": "code", "name": "Hero", "hidden": false, "items": []any{}, "style": style,
		"content": map[string]any{"html": `<section class="rounded-2xl" data-wbc="1"><h1 data-wbc="2">Hi</h1></section>`, "head": head, "kit": "ovkit",
			"binds": `{"2":{"kind":"field","field":"title"}}`}} // a bound page: filled element by element
	doc := map[string]any{"site": map[string]any{"brand": "#2271b1", "fontPair": "modern", "corners": "soft"}, "sections": []any{sec}}
	body := wbrPublishBody(doc, map[string]any{}, 0, "publish", nil)
	body["serverRender"] = true
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), body, nil); code != http.StatusOK {
		t.Fatalf("publish: %d %v", code, v)
	}
	front := func() string {
		resp, err := http.Get(base + "/?page_id=" + itoa64(post.ID))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}
	before := front()
	if strings.Contains(before, ">Hi</h1>") || !strings.Contains(before, "<h1") {
		t.Fatalf("the bound title is not filled: %.1500s", before)
	}
	if strings.Contains(before, "data-wb-kit-override") {
		t.Fatalf("an override without edits: %.1500s", before)
	}

	// The editor reads the empty state, writes a draft.
	code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/kits/ovkit/override", nil, nil)
	if code != http.StatusOK || wbrObj(v)["draftVersion"] != float64(0) {
		t.Fatalf("get: %d %v", code, v)
	}
	draft := map[string]any{"rules": []any{
		map[string]any{"ctx": []any{"@layer utilities"}, "sel": ".rounded-2xl", "p": map[string]any{"border-radius": "2rem"}},
		map[string]any{"sel": ":root", "p": map[string]any{"--color-brand-600": "#c2410c"}},
	}}
	code, v, _ = admin.do(http.MethodPut, "/web-builder/v2/kits/ovkit/override", map[string]any{"draft": draft, "base": 0}, nil)
	if code != http.StatusOK || wbrObj(v)["draftVersion"] != float64(1) || wbrObj(v)["pending"] != true {
		t.Fatalf("put: %d %v", code, v)
	}
	// A stale base is a conflict; invalid CSS is refused.
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/kits/ovkit/override", map[string]any{"draft": draft, "base": 0}, nil); code != http.StatusConflict || wbrCode(v) != "wb_kit_override_conflict" {
		t.Errorf("stale base: %d %v", code, v)
	}
	bad := map[string]any{"rules": []any{map[string]any{"sel": ".a</style><script>", "p": map[string]any{"color": "red"}}}}
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/kits/ovkit/override", map[string]any{"draft": bad}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("bad override: %d %v", code, v)
	}
	bad = map[string]any{"rules": []any{map[string]any{"sel": ".a", "p": map[string]any{"background": "url(javascript:alert(1))"}}}}
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/kits/ovkit/override", map[string]any{"draft": bad}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("javascript url: %d %v", code, v)
	}
	// The draft is not live yet.
	if strings.Contains(front(), "data-wb-kit-override") {
		t.Error("a draft is live")
	}
	// An author can read neither write nor publish it.
	author := wbrUser(t, srv, base, "ovauthor", "author")
	if code, _, _ := author.do(http.MethodPut, "/web-builder/v2/kits/ovkit/override", map[string]any{"draft": draft}, nil); code != http.StatusForbidden {
		t.Errorf("author put: %d", code)
	}
	if code, _, _ := author.do(http.MethodPost, "/web-builder/v2/kits/ovkit/override/publish", map[string]any{}, nil); code != http.StatusForbidden {
		t.Errorf("author publish: %d", code)
	}

	// Publish: live on the page at once, after the kit's stylesheet.
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/ovkit/override/publish", map[string]any{"base": 1}, nil); code != http.StatusOK || wbrObj(v)["version"] != float64(1) || wbrObj(v)["pending"] != false {
		t.Fatalf("publish override: %d %v", code, v)
	}
	after := front()
	headPart, _, _ := strings.Cut(after, "</head>")
	want := "<style data-wb-kit-override=\"ovkit\">\n@layer utilities{.rounded-2xl{border-radius:2rem}}\n:root{--color-brand-600:#c2410c}\n</style>"
	i, j := strings.Index(headPart, "kits/ovkit/css/site.css"), strings.Index(headPart, want)
	if i < 0 || j < i {
		t.Fatalf("override not after the kit's stylesheet: %.3000s", headPart)
	}
	nonce := regexp.MustCompile(`nonce="[^"]*"|"nonce":"[^"]*"|[0-9a-f]{10}|\s+`)
	if a, b := nonce.ReplaceAllString(strings.Replace(after, want, "", 1), ""), nonce.ReplaceAllString(before, ""); a != b {
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		t.Errorf("the override changed more than its own stylesheet, at %d: %.200q / %.200q", i, a[i:], b[i:])
	}
	if false {
		t.Error("the override changed more than its own stylesheet")
	}
	// The pages of the kit (the "From this design" palette).
	code, v, _ = admin.do(http.MethodGet, "/web-builder/v2/kits/ovkit/pages", nil, nil)
	pages, _ := wbrObj(v)["pages"].([]any)
	if code != http.StatusOK || len(pages) != 1 || !strings.Contains(strAny(wbrObj(pages[0])["html"]), `class="rounded-2xl"`) {
		t.Errorf("pages: %d %v", code, v)
	}
	// Deleting the kit drops its override.
	if code, _, _ := admin.do(http.MethodDelete, "/web-builder/v2/kits/ovkit", nil, nil); code != http.StatusOK {
		t.Fatal("delete kit")
	}
	if _, ok := srv.wbKitOverrides()["ovkit"]; ok {
		t.Error("override kept after the kit was deleted")
	}
	_ = ctx
}
