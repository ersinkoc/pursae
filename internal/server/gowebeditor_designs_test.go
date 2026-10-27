package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The builder's Pages panel and the ready-made homepage designs: the panel is on, the homepage
// endpoint follows Settings › Reading, pages carry user_can, a page duplicates, and the designs
// list in the library's Pages tab and insert as templates.
func TestGolementorPagesPanelAndDesigns(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)

	_, body, _ := get(t, c, base+"/wp-admin/post?post=2&action=golementor")
	if !strings.Contains(body, `"is_pages_panel_active":true`) {
		t.Error("the Pages panel is not switched on")
	}
	if !strings.Contains(body, `"subscription_plans":{"free"`) {
		t.Error("the library has no free plan for its listings")
	}
	if code, _, loc := get(t, c, base+"/wp-admin/post?post=2&action=elementor"); code != http.StatusMovedPermanently || !strings.Contains(loc, "action=golementor") {
		t.Errorf("old editor URL: %d %q", code, loc)
	}
	nonce := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no elementor_ajax nonce")
	}

	// Homepage: 0 while the site shows posts, the page once Settings › Reading points at it.
	if code, b := restGet(t, c, base, "/wp-json/elementor/v1/site-navigation/homepage"); code != http.StatusOK || strings.TrimSpace(b) != "0" {
		t.Errorf("homepage before: %d %s", code, b)
	}
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings", `{"show_on_front":"page","page_on_front":2}`); code != http.StatusOK {
		t.Fatalf("set homepage: %d %.200s", code, b)
	}
	if _, b := restGet(t, c, base, "/wp-json/elementor/v1/site-navigation/homepage"); strings.TrimSpace(b) != "2" {
		t.Errorf("homepage after: %s", b)
	}
	if _, b := restGet(t, c, base, "/wp-json/wp/v2/pages?status=any&_fields=id,user_can"); !strings.Contains(b, `"user_can":{"delete":true,"edit":true}`) {
		t.Errorf("pages lack user_can: %.300s", b)
	}
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/elementor/v1/site-navigation/duplicate-post", `{"post_id":2,"title":"Sample Page"}`); code != http.StatusOK {
		t.Errorf("duplicate: %d %s", code, b)
	} else if _, list := restGet(t, c, base, "/wp-json/wp/v2/pages?status=draft&_fields=title"); !strings.Contains(list, "Sample Page (copy)") {
		t.Errorf("the copy is not listed: %.300s", list)
	}

	// Designs: listed as remote page templates and inserted with fresh element ids.
	code, b := restGet(t, c, base, "/wp-json/elementor/v1/template-library/templates?source=remote")
	var lib struct {
		Templates []map[string]any `json:"templates"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(b), &lib) != nil || len(lib.Templates) != len(gweDesigns) {
		t.Fatalf("library remote list: %d %.300s", code, b)
	}
	if lib.Templates[0]["type"] != "page" || lib.Templates[0]["accessTier"] != "free" {
		t.Errorf("design listing: %v", lib.Templates[0])
	}
	if code, _, _ := get(t, c, lib.Templates[0]["thumbnail"].(string)); code != http.StatusOK {
		t.Errorf("design thumbnail: %d", code)
	}
	resp, err := c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {nonce[1]}, "editor_post_id": {"2"},
		"actions": {`{"d":{"action":"get_template_data","data":{"source":"remote","template_id":"gwe-home-business","edit_mode":true}}}`}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), `"success":true`) || !strings.Contains(string(raw), "We help growing companies") {
		t.Errorf("design data: %.300s", raw)
	}
	for _, d := range gweDesigns {
		if b, err := json.Marshal(d.Build()); err != nil || len(b) < 500 {
			t.Errorf("design %s does not build", d.ID)
		}
	}
}

// restSend sends a JSON body to a REST route with the cookie session's nonce.
func restSend(t *testing.T, c *http.Client, base, method, path, body string) (int, string) {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	req.Header.Set("X-WP-Nonce", nonce)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
