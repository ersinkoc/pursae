package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWidgetsEditorAndREST(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Widget Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}

	// The screen boots WordPress' block widgets editor with its REST data preloaded.
	code, body, _ := get(t, c, base+"/wp-admin/widgets")
	if code != http.StatusOK {
		t.Fatalf("widgets screen: %d", code)
	}
	for _, want := range []string{`id="widgets-editor" class="blocks-widgets-container"`, `wp.editWidgets.initialize( "widgets-editor"`,
		"edit-widgets.min.js", "format-library.min.js", "createPreloadingMiddleware", `/wp/v2/sidebars?context=edit` + "\\u0026per_page=-1",
		"widgetTypesToHideFromLegacyWidgetBlock", `"/batch/v1"`, "block-editor-page", "wp.blocks.setCategories", "edit-widgets/style.min.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("widgets screen misses %q", want)
		}
	}
	if strings.Contains(body, "Template error") {
		t.Fatal("template error")
	}

	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	rest := func(method, path string, payload any) (int, any) {
		t.Helper()
		var rd io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+"/wp-json"+path, rd)
		req.Header.Set("X-WP-Nonce", nonce)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out any
		raw, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: bad JSON %q", method, path, raw)
		}
		return resp.StatusCode, out
	}
	sidebar := func(id string) []any {
		t.Helper()
		code, out := rest("GET", "/wp/v2/sidebars/"+id+"?context=edit", nil)
		if code != http.StatusOK {
			t.Fatalf("sidebar %s: %d %v", id, code, out)
		}
		return out.(map[string]any)["widgets"].([]any)
	}
	front := func() string {
		_, body, _ := get(t, c, base+"/")
		return body
	}

	// Anonymous requests are refused.
	if resp, err := http.Get(base + "/wp-json/wp/v2/sidebars"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous sidebars should be 401")
	}

	code, out := rest("GET", "/wp/v2/sidebars?context=edit&per_page=-1", nil)
	list, _ := out.([]any)
	if code != http.StatusOK || len(list) < 2 {
		t.Fatalf("sidebars: %d %v", code, out)
	}
	first := list[0].(map[string]any)
	last := list[len(list)-1].(map[string]any)
	if first["id"] != "sidebar-1" || first["status"] != "active" || last["id"] != "wp_inactive_widgets" {
		t.Fatalf("sidebars list shape: %v", list)
	}
	defaults := sidebar("sidebar-1")
	if len(defaults) == 0 {
		t.Fatal("sidebar-1 has no default widgets")
	}

	code, out = rest("GET", "/wp/v2/widget-types?context=edit&per_page=-1", nil)
	if code != http.StatusOK || !strings.Contains(mustJSON(out), `"is_multi":true`) {
		t.Fatalf("widget types: %d %v", code, out)
	}

	// Create a paragraph block widget in sidebar-1.
	code, out = rest("POST", "/wp/v2/widgets", map[string]any{"id_base": "block", "sidebar": "sidebar-1",
		"instance": map[string]any{"raw": map[string]any{"content": "<!-- wp:paragraph -->\n<p>Hello widget world</p>\n<!-- /wp:paragraph -->"}}})
	if code != http.StatusCreated {
		t.Fatalf("create widget: %d %v", code, out)
	}
	w := out.(map[string]any)
	id, _ := w["id"].(string)
	if !strings.HasPrefix(id, "block-") || w["sidebar"] != "sidebar-1" || !strings.Contains(w["rendered"].(string), "Hello widget world") ||
		!strings.Contains(mustJSON(w["instance"]), "Hello widget world") {
		t.Fatalf("created widget: %v", w)
	}
	if got := sidebar("sidebar-1"); got[len(got)-1] != id {
		t.Fatalf("new widget not appended: %v", got)
	}
	if !strings.Contains(front(), "Hello widget world") {
		t.Fatal("front page misses the new widget")
	}

	// List with the sidebar filter and the embedded widget type, as the editor loads it.
	code, out = rest("GET", "/wp/v2/widgets?context=edit&per_page=-1&_embed=about&sidebar=sidebar-1", nil)
	if code != http.StatusOK || !strings.Contains(mustJSON(out), id) || !strings.Contains(mustJSON(out), `"_embedded":{"about":[{`) {
		t.Fatalf("widgets list: %d %v", code, out)
	}

	// Reorder: the new widget first.
	order := append([]any{id}, defaults...)
	if code, out = rest("PUT", "/wp/v2/sidebars/sidebar-1", map[string]any{"widgets": order}); code != http.StatusOK {
		t.Fatalf("reorder: %d %v", code, out)
	}
	if got := sidebar("sidebar-1"); got[0] != id || len(got) != len(order) {
		t.Fatalf("reorder result: %v", got)
	}
	if body := front(); strings.Index(body, "Hello widget world") > strings.Index(body, "widget_search") {
		t.Fatal("front page order not updated")
	}

	// Move to sidebar-2 and back through a batch request, updating the content on the way.
	if code, out = rest("PUT", "/wp/v2/widgets/"+id, map[string]any{"sidebar": "sidebar-2"}); code != http.StatusOK || out.(map[string]any)["sidebar"] != "sidebar-2" {
		t.Fatalf("move: %d %v", code, out)
	}
	if got := sidebar("sidebar-2"); len(got) != 1 || got[0] != id {
		t.Fatalf("sidebar-2 after move: %v", got)
	}
	code, out = rest("POST", "/batch/v1", map[string]any{"validation": "require-all-validate", "requests": []any{
		map[string]any{"method": "PUT", "path": "/wp/v2/widgets/" + id + "?context=edit", "body": map[string]any{"id": id, "sidebar": "sidebar-1",
			"instance": map[string]any{"raw": map[string]any{"content": "<!-- wp:paragraph -->\n<p>Updated widget text</p>\n<!-- /wp:paragraph -->"}}}},
	}})
	if code != http.StatusMultiStatus {
		t.Fatalf("batch: %d %v", code, out)
	}
	resp := out.(map[string]any)["responses"].([]any)[0].(map[string]any)
	if resp["status"].(float64) != 200 || resp["body"].(map[string]any)["sidebar"] != "sidebar-1" {
		t.Fatalf("batch response: %v", resp)
	}
	if body := front(); !strings.Contains(body, "Updated widget text") || strings.Contains(body, "Hello widget world") {
		t.Fatal("front page misses the updated widget")
	}

	// Omitted widgets go to the inactive area; deleting without force does the same.
	if code, _ = rest("PUT", "/wp/v2/sidebars/sidebar-1", map[string]any{"widgets": defaults}); code != http.StatusOK {
		t.Fatalf("drop widget: %d", code)
	}
	if got := sidebar("wp_inactive_widgets"); len(got) != 1 || got[0] != id {
		t.Fatalf("inactive after drop: %v", got)
	}
	if strings.Contains(front(), "Updated widget text") {
		t.Fatal("inactive widget still shown")
	}

	// Delete for good.
	code, out = rest("DELETE", "/wp/v2/widgets/"+id+"?force=true", nil)
	if code != http.StatusOK || out.(map[string]any)["deleted"] != true {
		t.Fatalf("delete: %d %v", code, out)
	}
	if code, _ = rest("GET", "/wp/v2/widgets/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("deleted widget still found: %d", code)
	}

	// Server-side rendered blocks preview through the block renderer.
	code, out = rest("GET", "/wp/v2/block-renderer/core/archives?context=edit&attributes%5BshowPostCounts%5D=true", nil)
	if code != http.StatusOK || !strings.Contains(mustJSON(out), "wp-block-archives") {
		t.Fatalf("block renderer: %d %v", code, out)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
