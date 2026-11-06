package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

func TestWebBuilderRESTSiteSettings(t *testing.T) {
	srv, base, admin := wbrStart(t)
	const path = "/web-builder/v2/site-settings"

	code, v, _ := admin.do(http.MethodGet, path, nil, nil)
	o := wbrObj(v)
	if code != http.StatusOK || o["show_on_front"] != "posts" || o["pages"] == nil {
		t.Fatalf("get: %d %v", code, v)
	}

	home := wbrPost(t, srv, "page", "Start Here", "publish", 1)
	blog := wbrPost(t, srv, "page", "The Blog", "publish", 1)
	put := map[string]any{"blogname": "My <b>Site</b>", "blogdescription": "Built in Go", "show_on_front": "page",
		"page_on_front": home.ID, "page_for_posts": blog.ID, "custom_css": "body{color:red}"}
	code, v, _ = admin.do(http.MethodPut, path, put, nil)
	o = wbrObj(v)
	if code != http.StatusOK || o["blogname"] != "My Site" || o["blogdescription"] != "Built in Go" || o["show_on_front"] != "page" ||
		o["page_on_front"] != float64(home.ID) || o["custom_css"] != "body{color:red}" {
		t.Fatalf("put: %d %v", code, v)
	}
	// Stored where the Customizer reads it, so both screens agree.
	if srv.settings.String("blogname") != "My Site" || srv.settings.Int("page_on_front") != int(home.ID) ||
		modString(srv.themeMods(srv.activeTheme().Slug), "custom_css", "") != "body{color:red}" {
		t.Fatalf("not stored like the Customizer does: %q %d", srv.settings.String("blogname"), srv.settings.Int("page_on_front"))
	}
	// A partial update leaves the rest alone; an empty CSS removes it.
	if code, v, _ = admin.do(http.MethodPut, path, map[string]any{"custom_css": ""}, nil); code != http.StatusOK || wbrObj(v)["blogname"] != "My Site" || wbrObj(v)["custom_css"] != "" {
		t.Fatalf("partial put: %d %v", code, v)
	}

	// Invalid input is refused and nothing changes.
	for name, body := range map[string]map[string]any{
		"unknown key":       {"admin_email": "x@example.com"},
		"bad show_on_front": {"show_on_front": "nowhere"},
		"not a page":        {"page_on_front": 999999},
		"same page twice":   {"page_on_front": home.ID, "page_for_posts": home.ID},
		"not an image":      {"site_icon": home.ID},
	} {
		if code, v, _ := admin.do(http.MethodPut, path, body, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, code, v)
		}
	}
	if srv.settings.String("blogname") != "My Site" {
		t.Errorf("a refused update changed the site title")
	}

	// Permissions and handshake.
	sub := wbrUser(t, srv, base, "sub", "subscriber")
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		if code, _, _ := sub.do(m, path, map[string]any{"blogname": "x"}, nil); code != http.StatusForbidden {
			t.Errorf("subscriber %s: %d", m, code)
		}
	}
	if code, _, _ := admin.do(http.MethodGet, path, nil, map[string]string{"X-WB-Api": "0"}); code != http.StatusConflict {
		t.Errorf("handshake: %d", code)
	}
}

// While the builder is active its site settings replace the Customizer: no menu row, no admin bar
// link, /wp-admin/customize opens the Theme Builder; with the builder off nothing changes.
func TestWebBuilderHidesCustomizer(t *testing.T) {
	srv, base, admin := wbrStart(t)
	cl := *admin.c
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	page := func(path string) (int, string, string) {
		resp, err := cl.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Location")
	}

	code, _, loc := page("/wp-admin/customize")
	if code != http.StatusFound || loc != "/wp-admin/themes?page="+webbuilder.ThemeBuilderPage {
		t.Fatalf("customize with the builder on: %d %q", code, loc)
	}
	_, dash, _ := page("/wp-admin/")
	if strings.Contains(dash, `href="/wp-admin/customize"`) || strings.Contains(dash, "wp-admin-bar-customize") {
		t.Errorf("the dashboard still links to the Customizer")
	}
	// The editor knows the user may open the site settings.
	post := wbrPost(t, srv, "page", "Edit me", "publish", 1)
	_, editor, _ := page("/wp-admin/post?post=" + itoa64(post.ID) + "&action=web-builder")
	if !strings.Contains(editor, `"editThemeOptions":true`) {
		t.Errorf("boot config lacks caps.editThemeOptions")
	}

	if err := srv.settings.Set(context.Background(), map[string]any{webbuilder.PluginOption: false}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := page("/wp-admin/customize"); code != http.StatusOK {
		t.Errorf("customize with the builder off: %d", code)
	}
	_, dash, _ = page("/wp-admin/")
	if !strings.Contains(dash, `href="/wp-admin/customize"`) {
		t.Errorf("the Customizer is gone with the builder off")
	}
}
