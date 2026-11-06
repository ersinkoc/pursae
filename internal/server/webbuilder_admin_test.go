package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// The admin menu no longer carries the old builder's screens.
func TestWebBuilderAdminMenu(t *testing.T) {
	for _, cache := range []bool{true, false} {
		for _, d := range adminMenu(0, cache) {
			if d.id == "menu-gowebeditor" || strings.HasPrefix(d.slug, "web-builder") {
				t.Errorf("menu still has %q (%s)", d.title, d.slug)
			}
			for _, sd := range d.sub {
				if sd.slug == "theme-builder" || strings.HasPrefix(sd.slug, "web-builder") {
					t.Errorf("%s still has %q (%s)", d.title, sd.title, sd.slug)
				}
			}
		}
	}
}

// wbNotOldScreen fails when a former admin screen of the old builder still answers.
func wbNotOldScreen(t *testing.T, c *http.Client, u string) {
	t.Helper()
	code, body, _ := get(t, c, u)
	switch {
	case code == http.StatusNotFound || code == http.StatusMovedPermanently || code == http.StatusFound:
	case code == http.StatusOK && strings.Contains(body, "is not available yet"): // the admin catch-all
	default:
		t.Errorf("GET %s = %d, the old screen still answers:\n%.400s", u, code, body)
	}
}

// Every entry point of Pursae Web Builder with the plugin on: the editors' button, the list row
// action and post state, the site toolbar, the editor action and its old development action; and
// the old builder's routes, page templates and front assets are gone.
func TestWebBuilderEntryPoints(t *testing.T) {
	srv, base, c := wbfStart(t)
	ctx := context.Background()
	built := wbfPage(t, srv, "page", "Built Page", []string{}, "", "")
	plain := wbTestPost(t, srv, "page", "Plain Page", "publish", 1)
	plainPost := wbTestPost(t, srv, "post", "Plain Post", "publish", 1)

	// Pages list: the row action on every page, the post state only on the builder page.
	_, body, _ := get(t, c, base+"/wp-admin/edit?post_type=page")
	for _, p := range []*store.Post{built, plain} {
		want := `<span class="edit_with_web_builder"><a href="/wp-admin/post?post=` + itoa64(p.ID) + `&amp;action=web-builder"`
		if !strings.Contains(body, want) {
			t.Errorf("pages list lacks the row action of %q", p.Title)
		}
	}
	if n := strings.Count(body, `<span class="post-state">Pursae Web Builder</span>`); n != 1 {
		t.Errorf("pages list: %d Pursae Web Builder post states, want 1", n)
	}
	if !wbRowHasState(body, built.ID) || wbRowHasState(body, plain.ID) {
		t.Error("the post state is not on the builder page's row only")
	}
	_, body, _ = get(t, c, base+"/wp-admin/edit")
	if !strings.Contains(body, `post=`+itoa64(plainPost.ID)+`&amp;action=web-builder"`) {
		t.Error("posts list lacks the row action")
	}
	for _, old := range []string{"edit_with_gowebeditor", "elementor"} {
		if strings.Contains(body, old) {
			t.Errorf("posts list still carries %q", old)
		}
	}

	// Block editor: the header toolbar button, pointing at the editor action.
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(plain.ID)+"&action=edit")
	if !strings.Contains(body, "web-builder-switch-mode") || !strings.Contains(body, strconvQuote(wbEditURL(plain.ID))) {
		t.Error("block editor lacks Edit with Pursae Web Builder")
	}
	for _, old := range []string{"gowebeditor-switch", "elementor_canvas", "elementor_header_footer"} {
		if strings.Contains(body, old) {
			t.Errorf("block editor still carries %q", old)
		}
	}

	// Classic editor: the button below the title; a save it asked for continues to the builder.
	if err := srv.settings.Set(ctx, map[string]any{"native_classic_editor": true, "classic-editor-replace": "classic"}); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(plain.ID)+"&action=edit")
	if !strings.Contains(body, "web-builder-switch-mode") || !strings.Contains(body, wbClassicRedirectField) {
		t.Error("classic editor lacks Edit with Pursae Web Builder")
	}
	nonce := formNonce(t, body)
	code, _, loc := post(t, c, base+"/wp-admin/post", url.Values{"_wpnonce": {nonce}, "action": {"editpost"}, "post_ID": {itoa64(plain.ID)},
		"post_type": {"page"}, "post_title": {"Plain Page"}, "content": {"Hello"}, "original_post_status": {"publish"}, "post_status": {"publish"},
		wbClassicRedirectField: {"1"}})
	if code != http.StatusFound || !strings.HasSuffix(loc, wbEditURL(plain.ID)) {
		t.Errorf("classic save with the builder redirect: %d %q, want %s", code, loc, wbEditURL(plain.ID))
	}
	if err := srv.settings.Set(ctx, map[string]any{"native_classic_editor": false}); err != nil {
		t.Fatal(err)
	}

	// The editor action opens the new editor; the development action redirects to it.
	if code, body, _ := wbOpen(t, c, base, built.ID); code != http.StatusOK || !strings.Contains(body, "__WEB_BUILDER__") {
		t.Errorf("action=web-builder: %d, new editor: %v", code, strings.Contains(body, "__WEB_BUILDER__"))
	}
	code, _, loc = get(t, c, base+"/wp-admin/post?post="+itoa64(built.ID)+"&action=web-builder-next")
	if code != http.StatusMovedPermanently || loc != wbEditURL(built.ID) {
		t.Errorf("action=web-builder-next: %d %q", code, loc)
	}
	for _, old := range []string{"elementor", "golementor"} { // the old builder's actions open the core editor
		if code, body, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(built.ID)+"&action="+old); code != http.StatusOK || strings.Contains(body, "__WEB_BUILDER__") {
			t.Errorf("action=%s: %d", old, code)
		}
	}

	// Site toolbar: the node on the builder page only.
	_, body, _ = get(t, c, wbfURL(base, built))
	if !strings.Contains(body, `id='wp-admin-bar-web_builder_edit_page'`) || !strings.Contains(body, `href='/wp-admin/post?post=`+itoa64(built.ID)+`&amp;action=web-builder'`) {
		t.Errorf("builder page toolbar lacks Edit with Pursae Web Builder")
	}
	if strings.Contains(body, "plugins/web-builder/assets") || strings.Contains(body, "golementor_edit_page") {
		t.Error("builder page still loads the old builder")
	}
	_, body, _ = get(t, c, wbfURL(base, plain))
	if strings.Contains(body, "web_builder_edit_page") {
		t.Error("a page without a builder document offers Edit with Pursae Web Builder")
	}
	if strings.Contains(body, "plugins/web-builder/assets") || strings.Contains(body, "elementor") {
		t.Error("a theme page still loads the old builder")
	}

	// The old builder's routes are gone.
	wbNotOldScreen(t, c, base+"/wp-admin/theme-builder")
	wbNotOldScreen(t, c, base+"/wp-admin/web-builder-setup")
	for _, path := range []string{"/wp-json/elementor/v1/globals", "/wp-json/web-builder/v1/studio/preferences", "/wp-content/plugins/web-builder/assets/js/frontend.min.js",
		"/wp-content/plugins/gowebeditor/assets/js/frontend.min.js", "/wp-content/web-builder/theme-screenshot/hello-web-builder"} {
		if code, _, _ := get(t, c, base+path); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
	_, body, _ = get(t, c, base+"/wp-json/")
	if strings.Contains(body, `"elementor/v1"`) || strings.Contains(body, `"web-builder/v1"`) || !strings.Contains(body, `"web-builder/v2"`) {
		t.Error("REST index: old namespaces listed or web-builder/v2 missing")
	}

	// The core heartbeat still answers without the old builder's AJAX.
	if code, out, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"heartbeat"}, "data[wp-refresh-nonces]": {"1"}}); code != http.StatusOK ||
		!strings.Contains(out, `"server_time"`) || !strings.Contains(out, `"rest_nonce"`) || strings.Contains(out, "elementor") {
		t.Errorf("heartbeat: %d %s", code, out)
	}
}

// wbRowHasState reports whether the list row of a post carries the Pursae Web Builder post state.
func wbRowHasState(body string, id int64) bool {
	i := strings.Index(body, `id="post-`+itoa64(id)+`"`)
	if i < 0 {
		return false
	}
	row := body[i:]
	if j := strings.Index(row, "</tr>"); j >= 0 {
		row = row[:j]
	}
	return strings.Contains(row, "Pursae Web Builder</span>")
}

// With the plugin off no entry point shows and web-builder/v2 leaves the REST index.
func TestWebBuilderEntryPointsOff(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	built := wbfPage(t, srv, "page", "Built Page", []string{}, "", "")
	_, body, _ := get(t, c, base+"/wp-admin/edit?post_type=page")
	if strings.Contains(body, "edit_with_web_builder") || strings.Contains(body, "Pursae Web Builder</span>") {
		t.Error("pages list offers the builder while it is off")
	}
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(built.ID)+"&action=edit")
	if strings.Contains(body, "web-builder-switch-mode") {
		t.Error("block editor offers the builder while it is off")
	}
	_, body, _ = get(t, c, wbfURL(base, built))
	if strings.Contains(body, "web_builder_edit_page") {
		t.Error("toolbar offers the builder while it is off")
	}
	code, _, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(built.ID)+"&action=web-builder-next")
	if code != http.StatusOK {
		t.Errorf("action=web-builder-next while off: %d, want the core editor", code)
	}
	_, body, _ = get(t, c, base+"/wp-json/")
	if strings.Contains(body, `"web-builder/v2"`) {
		t.Error("REST index lists web-builder/v2 while the plugin is off")
	}
}
