package server

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestThemeFileEditor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", dir)
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Theme Files"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}

	nonceRe := regexp.MustCompile(`id="nonce" name="nonce" value="([^"]+)"`)
	editor := func(file string) (nonce, body string) {
		t.Helper()
		code, body, _ := get(t, c, base+"/wp-admin/theme-editor?theme=twentytwentyfive&file="+url.QueryEscape(file))
		if code != http.StatusOK || strings.Contains(body, "Template error") {
			t.Fatalf("editor %s: %d", file, code)
		}
		m := nonceRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("editor %s is not editable", file)
		}
		return m[1], body
	}
	save := func(file, nonce, content string) (bool, string) {
		t.Helper()
		_, body, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{
			"action": {"edit-theme-plugin-file"}, "nonce": {nonce}, "theme": {"twentytwentyfive"}, "file": {file}, "newcontent": {content},
		})
		var out struct {
			Success bool
			Data    struct{ Code, Message string }
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("save %s: bad JSON %q", file, body)
		}
		return out.Success, out.Data.Code
	}
	front := func() string {
		t.Helper()
		code, body, _ := get(t, c, base+"/")
		if code != http.StatusOK {
			t.Fatalf("front page: %d", code)
		}
		return body
	}
	orig, err := os.ReadFile("../../web/themes/twentytwentyfive/templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	header := string(orig)

	// The editor is editable and offers Update File.
	nonce, body := editor("templates/header.html")
	if !strings.Contains(body, `value="Update File"`) || !strings.Contains(body, `"readOnly":false`) {
		t.Fatal("editor is not editable")
	}

	// A template edit applies at once, without a rebuild.
	edited := strings.Replace(header, "Skip to content", "Skip to the edited content", 1)
	if ok, code := save("templates/header.html", nonce, edited); !ok {
		t.Fatalf("save header: %s", code)
	}
	if !strings.Contains(front(), "Skip to the edited content") {
		t.Fatal("edited template not rendered")
	}
	if _, err := os.Stat(filepath.Join(dir, "twentytwentyfive", "templates", "header.html")); err != nil {
		t.Fatalf("edited copy not stored: %v", err)
	}

	// A template that does not parse is rolled back; so is one that fails when rendered.
	for code, broken := range map[string]string{
		"template_error": strings.Replace(header, "{{.DocumentTitle}}", "{{if .DocumentTitle}}", 1),
		"render_error":   strings.Replace(header, "{{.DocumentTitle}}", `{{template "wpgo-no-such-template"}}`, 1),
	} {
		if ok, got := save("templates/header.html", nonce, broken); ok || got != code {
			t.Fatalf("broken template: success=%v code=%q, want %q", ok, got, code)
		}
		if !strings.Contains(front(), "Skip to the edited content") {
			t.Fatalf("%s: previous edit not kept", code)
		}
	}

	// Stylesheets are served from the edited copy.
	cssNonce, _ := editor("style.css")
	css, _ := os.ReadFile("../../web/themes/twentytwentyfive/assets/style.css")
	if ok, code := save("style.css", cssNonce, string(css)+"\n/* wpgo-edited */\n"); !ok {
		t.Fatalf("save style.css: %s", code)
	}
	if _, body, _ := get(t, c, base+"/wp-content/themes/twentytwentyfive/style.css"); !strings.HasSuffix(body, "/* wpgo-edited */\n") {
		t.Fatal("edited style.css not served")
	}

	// Nonces are checked.
	if ok, code := save("style.css", nonce, "x"); ok || code != "nonce_failure" {
		t.Fatalf("wrong nonce: success=%v code=%q", ok, code)
	}

	// Restore original drops the edited copy.
	_, body = editor("templates/header.html")
	m := regexp.MustCompile(`href="(/wp-admin/theme-editor\?action=restore[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Restore original link")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "restored=1") {
		t.Fatalf("restore: %d %q", code, loc)
	}
	if strings.Contains(front(), "Skip to the edited content") {
		t.Fatal("restored template still edited")
	}
	if _, err := os.Stat(filepath.Join(dir, "twentytwentyfive", "templates", "header.html")); !os.IsNotExist(err) {
		t.Fatalf("edited copy still stored: %v", err)
	}
}
