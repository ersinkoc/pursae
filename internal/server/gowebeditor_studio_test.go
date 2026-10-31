package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Golementor Studio: the editor page loads the Studio by default, only the toggle once the user
// picked Advanced, and the preference endpoint saves the choice per user.
func TestGolementorStudio(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	editor := base + "/wp-admin/post?post=2&action=web-builder"
	studioAssets := []string{"studio/studio.css", "studio/core.js", "studio/fields.js", "studio/library.js", "studio/inspector.js",
		"studio/layers.js", "studio/site.js", "studio/canvas.js", "studio/app.js"}

	// Default: Studio mode with every asset, the class set before paint, and its configuration.
	code, body, _ := get(t, c, editor)
	if code != http.StatusOK {
		t.Fatalf("editor: %d", code)
	}
	for _, want := range append(studioAssets, "studio/toggle.js", "window.GWEStudioConfig = {", `"mode":"studio"`, `"postId":2`,
		"document.body.classList.add('gwe-studio')", `"newPage":"`) {
		if !strings.Contains(body, want) {
			t.Errorf("Studio editor page misses %q", want)
		}
	}
	if i, j := strings.Index(body, "studio/studio.css"), strings.Index(body, "</head>"); i < 0 || i > j {
		t.Error("studio.css is not in the head")
	}
	scriptTag := regexp.MustCompile(`<script src="[^"]*/studio/(\w+)\.js\?ver=`)
	var loaded []string
	for _, m := range scriptTag.FindAllStringSubmatch(body, -1) {
		loaded = append(loaded, m[1])
	}
	if got := strings.Join(loaded, ","); got != "core,fields,library,inspector,layers,site,canvas,app" {
		t.Errorf("Studio scripts: %s", got)
	}
	for _, f := range append(studioAssets, "studio/toggle.js") {
		if code, _, _ := get(t, c, base+"/wp-content/plugins/web-builder/assets/"+f); code != http.StatusOK {
			t.Errorf("asset %s: %d", f, code)
		}
	}

	// The preference endpoint.
	if code, b := restGet(t, c, base, "/wp-json/web-builder/v1/studio/preferences"); code != http.StatusOK || !strings.Contains(b, `"mode":"studio"`) {
		t.Errorf("GET preferences: %d %s", code, b)
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/web-builder/v1/studio/preferences", `{"mode":"fancy"}`); code != http.StatusBadRequest {
		t.Errorf("bad mode: %d", code)
	}
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/web-builder/v1/studio/preferences", `{"mode":"advanced"}`); code != http.StatusOK || !strings.Contains(b, `"mode":"advanced"`) {
		t.Fatalf("POST preferences: %d %s", code, b)
	}
	if code, b := restGet(t, c, base, "/wp-json/web-builder/v1/studio/preferences"); code != http.StatusOK || !strings.Contains(b, `"mode":"advanced"`) {
		t.Errorf("preference not saved: %d %s", code, b)
	}
	resp, err := http.Post(base+"/wp-json/web-builder/v1/studio/preferences", "application/json", strings.NewReader(`{"mode":"studio"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("logged-out preferences: %d", resp.StatusCode)
	}

	// Advanced: the upstream editor with only the toggle (which loads the Studio on demand).
	_, body, _ = get(t, c, editor)
	if !strings.Contains(body, `"mode":"advanced"`) || !strings.Contains(body, "studio/toggle.js") {
		t.Error("Advanced editor page has no Studio toggle")
	}
	for _, bad := range []string{`id="gwe-studio-css"`, "document.body.classList.add('gwe-studio')"} {
		if strings.Contains(body, bad) {
			t.Errorf("Advanced editor page includes %q", bad)
		}
	}
	if m := scriptTag.FindAllString(body, -1); len(m) != 0 {
		t.Errorf("Advanced editor page loads Studio scripts: %v", m)
	}
	if !strings.Contains(body, `<div id="elementor-editor-wrapper">`) || !strings.Contains(body, `id="tmpl-elementor-panel"`) {
		t.Error("the upstream editor is missing")
	}

	// Back to Studio.
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/web-builder/v1/studio/preferences", `{"mode":"studio"}`); code != http.StatusOK {
		t.Fatalf("POST studio: %d", code)
	}
	if _, body, _ = get(t, c, editor); !strings.Contains(body, `id="gwe-studio-css"`) {
		t.Error("Studio is not back after switching")
	}

	// The section library the gallery reads (served by the stand-in until the library lands).
	code, b := restGet(t, c, base, "/wp-json/web-builder/v1/library/sections")
	var list struct {
		Categories []struct {
			Slug, Title string
			Count       int
		}
		Items []struct{ ID, Title, Category, Thumbnail string }
	}
	if code != http.StatusOK || json.Unmarshal([]byte(b), &list) != nil || len(list.Items) == 0 || len(list.Categories) == 0 {
		t.Fatalf("sections: %d %.200s", code, b)
	}
	code, b = restGet(t, c, base, "/wp-json/web-builder/v1/library/sections/"+list.Items[0].ID)
	var one struct {
		Content []map[string]any `json:"content"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(b), &one) != nil || len(one.Content) == 0 || one.Content[0]["elType"] != "container" {
		t.Errorf("section: %d %.200s", code, b)
	}
	if code, _ := restGet(t, c, base, "/wp-json/web-builder/v1/library/sections/nope"); code != http.StatusNotFound {
		t.Errorf("unknown section: %d", code)
	}
}
