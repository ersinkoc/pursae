package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// wbsBasic calls the REST API with an Application Password.
func wbsBasic(t *testing.T, base, pass, method, route string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+"/wp-json"+route, rd)
	req.SetBasicAuth("admin", pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webbuilder.APIHeader, strconv.Itoa(webbuilder.APIVersion))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// An Application Password with only the Content scope (which no longer carries unfiltered_html: only
// Appearance does) cannot write site-wide code: no code import, no code sections kept in a
// published page, no kit or kit override writes, no contact form changes (contact-form-7/v1 belongs
// to no scope). With Appearance (themes) as well, or full access, the same calls work.
func TestWBContentScopeNoSiteCode(t *testing.T) {
	srv, base, admin := wbrStart(t)
	_, c := admin.base, admin.c
	contentPass, contentCall := mcpTestAgent(t, base, c, "Content only", []string{"content"})
	themesPass, themesCall := mcpTestAgent(t, base, c, "Content and Appearance", []string{"content", "themes"})
	wbsKit(t, admin, "scopekit", map[string]string{"site.css": ".a{color:red}"})

	// MCP web_builder_import_html, method code.
	html := `<!doctype html><html><head><title>T</title></head><body><section><h1>Hi</h1><script>window.x=1</script></section></body></html>`
	r := contentCall("web_builder_import_html", map[string]any{"title": "Code page", "method": "code", "html": html})
	if r["isError"] != true || !(strings.Contains(mcpText(r), "Appearance") || strings.Contains(mcpText(r), "unfiltered_html")) {
		t.Errorf("content scope, method code: %s", mcpText(r))
	}
	if r := themesCall("web_builder_import_html", map[string]any{"title": "Code page", "method": "code", "html": html}); r["isError"] == true {
		t.Errorf("themes scope, method code: %s", mcpText(r))
	}

	// A published page: the content-only password's code section is dropped.
	post := wbrPost(t, srv, "page", "Scoped page", "draft", 1)
	style := map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}}
	sec := map[string]any{"id": "c1", "kind": "code", "name": "Hero", "hidden": false, "items": []any{}, "style": style,
		"content": map[string]any{"html": `<section><p>code</p><script>window.scoped=1</script></section>`, "head": "", "kit": "scopekit"}}
	doc := map[string]any{"site": map[string]any{"brand": "#2271b1", "fontPair": "modern", "corners": "soft"}, "sections": []any{sec}}
	body := wbrPublishBody(doc, map[string]any{}, 0, "publish", nil)
	body["serverRender"] = true
	code, out := wbsBasic(t, base, contentPass, http.MethodPost, wbrDocPath(post.ID, "/publish"), body)
	if code != http.StatusOK {
		t.Fatalf("content scope publish: %d %v", code, out)
	}
	if d, err := srv.store.WebBuilder().Doc(t.Context(), post.ID); err != nil || strings.Contains(string(d.Doc), "window.scoped") || strings.Contains(d.HTML, "window.scoped") {
		t.Errorf("the content-only password's code section was kept (%v)", err)
	}
	post2 := wbrPost(t, srv, "page", "Scoped page two", "draft", 1)
	if code, out := wbsBasic(t, base, themesPass, http.MethodPost, wbrDocPath(post2.ID, "/publish"), body); code != http.StatusOK {
		t.Fatalf("themes scope publish: %d %v", code, out)
	}
	if d, err := srv.store.WebBuilder().Doc(t.Context(), post2.ID); err != nil || !strings.Contains(d.HTML, "window.scoped") {
		t.Errorf("the themes password's code section was dropped (%v)", err)
	}

	// Kit writes and kit override writes.
	files := map[string]any{"files": []any{map[string]any{"path": "x.js", "text": "window.k=1"}}}
	if code, out := wbsBasic(t, base, contentPass, http.MethodPost, "/web-builder/v2/kits/scopekit/files", files); code != http.StatusForbidden {
		t.Errorf("content scope kit files: %d %v", code, out)
	}
	draft := map[string]any{"draft": map[string]any{"rules": []any{map[string]any{"sel": ".a", "p": map[string]any{"color": "blue"}}}}}
	if code, out := wbsBasic(t, base, contentPass, http.MethodPut, "/web-builder/v2/kits/scopekit/override", draft); code != http.StatusForbidden {
		t.Errorf("content scope override: %d %v", code, out)
	}
	if code, out := wbsBasic(t, base, themesPass, http.MethodPut, "/web-builder/v2/kits/scopekit/override", draft); code != http.StatusOK {
		t.Errorf("themes scope override: %d %v", code, out)
	}

	// Contact forms (their recipients and mail headers) need full access.
	if code, out := wbsBasic(t, base, contentPass, http.MethodGet, "/contact-form-7/v1/contact-forms", nil); code != http.StatusForbidden && code != http.StatusUnauthorized && code != http.StatusNotFound {
		t.Errorf("content scope contact forms: %d %v", code, out)
	} else if code == http.StatusForbidden && out["code"] != "rest_scope_forbidden" && out["code"] != "rest_forbidden" {
		t.Errorf("content scope contact forms: %d %v", code, out)
	}
	if appScopeForRoute("/contact-form-7/v1/contact-forms/1") != "" {
		t.Error("contact-form-7/v1 belongs to a scope")
	}
}
