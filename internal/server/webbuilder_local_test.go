package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Local styles of a code page (element editor, Style panel): the kit's breakpoints are suggested
// from its stylesheets on commit, the publish gives each styled element its class and the page one
// stylesheet with the kit's breakpoints, the front end prints it in the head, a publish without
// local styles keeps the markup as it was (an author's save keeping them is TestDocLocalKeptForUnprivileged).
func TestWBCodeLocalStyles(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()

	// A kit whose CSS switches at 48rem / 62rem (Bootstrap-like): tablet ≤ 991, mobile ≤ 767.
	files := map[string]any{"files": []any{map[string]any{"path": "css/site.css",
		"text": "@media (min-width:62rem){.a{}} @media (min-width:62rem){.b{}} @media (min-width:48rem){.c{}} @media (max-width:47.99rem){.d{}}"}}}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/lockit/files", files, nil); code != http.StatusOK {
		t.Fatalf("kit files: %d %v", code, v)
	}
	code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/lockit/commit", map[string]any{"name": "Local kit"}, nil)
	bps := wbrObj(wbrObj(v)["breakpoints"])
	if code != http.StatusOK || bps["tablet"] != float64(991) || bps["mobile"] != float64(767) {
		t.Fatalf("kit commit: %d %v", code, v)
	}

	post := wbrPost(t, srv, "page", "Local Styles", "draft", 1)
	style := map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}}
	html := `<section class="hero" data-wbc="1"><h1 data-wbc="2">Hi</h1><a class="btn" data-wbc="3" href="#">Go</a></section>`
	sec := map[string]any{"id": "c1", "kind": "code", "name": "Hero", "hidden": false, "items": []any{}, "style": style,
		"content": map[string]any{"html": html, "kit": "lockit"}}
	doc := map[string]any{"site": map[string]any{"brand": "#2271b1", "fontPair": "modern", "corners": "soft"}, "sections": []any{sec}}
	publish := func(c *wbrClient, d map[string]any, rev int) (int, any) {
		body := wbrPublishBody(d, map[string]any{}, rev, "publish", nil)
		body["serverRender"] = true
		code, v, _ := c.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), body, nil)
		return code, v
	}
	if code, v := publish(admin, doc, 0); code != http.StatusOK {
		t.Fatalf("publish: %d %v", code, v)
	}
	row, _ := srv.store.WebBuilder().Doc(ctx, post.ID)
	plain := row.HTML
	if strings.Contains(plain, "data-wb-local") || !strings.Contains(plain, `<h1>Hi</h1>`) {
		t.Fatalf("no local styles: %s", plain)
	}

	// Local styles: padding on tablet, the button's background on hover, the heading's color.
	sec["local"] = map[string]any{
		"1": map[string]any{"v": []any{map[string]any{"bp": "tablet", "st": "", "p": map[string]any{"padding-top": "64px"}}}},
		"2": map[string]any{"v": []any{map[string]any{"bp": "", "st": "", "p": map[string]any{"color": "#0f172a"}}}},
		"3": map[string]any{"v": []any{map[string]any{"bp": "", "st": "hover", "p": map[string]any{"background-color": "rgb(255 0 0)"}}}},
	}
	if code, v := publish(admin, doc, int(row.Rev)); code != http.StatusOK {
		t.Fatalf("publish local: %d %v", code, v)
	}
	row, _ = srv.store.WebBuilder().Doc(ctx, post.ID)
	pid := itoa64(post.ID)
	for _, want := range []string{
		`<section class="hero e` + pid + `-1">`, `<h1 class="e` + pid + `-2">Hi</h1>`, `<a class="btn e` + pid + `-3" href="#">`,
		".e" + pid + "-2:not(#_):not(#_){color:#0f172a}\n.e" + pid + "-3:not(#_):not(#_):hover{background-color:rgb(255 0 0)}\n" +
			"@media (max-width:991px){\n.e" + pid + "-1:not(#_):not(#_){padding-top:64px}\n}\n",
	} {
		if !strings.Contains(row.HTML, want) {
			t.Errorf("snapshot lacks %q:\n%s", want, row.HTML)
		}
	}
	resp, err := http.Get(base + "/?page_id=" + pid)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	head, _, _ := strings.Cut(string(page), "</head>")
	if !strings.Contains(head, `<style data-wb-local="">`) || strings.Contains(string(page), "data-wbc") {
		t.Errorf("front page: %.2000s", page)
	}

	// Invalid local styles are refused.
	bad := map[string]any{"site": doc["site"], "sections": []any{map[string]any{"id": "c1", "kind": "code", "name": "Hero", "hidden": false, "items": []any{}, "style": style,
		"content": map[string]any{"html": html}, "local": map[string]any{"2": map[string]any{"v": []any{map[string]any{"bp": "", "st": "", "p": map[string]any{"color": "red;x:url(javascript:1)"}}}}}}}}
	if code, v := publish(admin, bad, int(row.Rev)); code != http.StatusUnprocessableEntity || !strings.Contains(mustJSON(v), "/local/2/v/0/p/color") {
		t.Errorf("bad local: %d %v", code, v)
	}
}
