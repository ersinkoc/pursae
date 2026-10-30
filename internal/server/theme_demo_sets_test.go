package server

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestTwentyTwentyFiveDemoSets imports the demo sets from Appearance › Themes: the chooser, one set
// (homepage, menu, color scheme, WebP images named after the set), another set replacing it without
// leaving the first one's pages or front page behind, and Remove restoring the site.
func TestTwentyTwentyFiveDemoSets(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	anon := &http.Client{CheckRedirect: c.CheckRedirect}

	_, body, _ := get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(body, `href="/wp-admin/themes?action=demo-content&amp;stylesheet=twentytwentyfive"`) {
		t.Fatal("the active theme's card has no Import Demo Content link")
	}
	code, body, _ := get(t, c, base+"/wp-admin/themes?action=demo-content&stylesheet=twentytwentyfive")
	if code != http.StatusOK {
		t.Fatalf("chooser: %d", code)
	}
	for _, want := range []string{"Wildflower Journal", "Botanica", "Field &amp; Light", "Slow Seasons", `src="/wp-content/themes/twentytwentyfive/demos/botanica.webp?ver=1.5"`} {
		if !strings.Contains(body, want) {
			t.Errorf("chooser misses %q", want)
		}
	}
	importURL := func(set string) string {
		t.Helper()
		_, body, _ := get(t, c, base+"/wp-admin/themes?action=demo-content&stylesheet=twentytwentyfive")
		m := regexp.MustCompile(`href="(/wp-admin/themes\?action=import-demo&amp;stylesheet=twentytwentyfive&amp;demo=` + set + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no Import link for %s", set)
		}
		return base + html.UnescapeString(m[1])
	}
	if code, _, _ := get(t, c, strings.Replace(importURL("botanica"), "_wpnonce=", "_wpnonce=x", 1)); code != http.StatusForbidden {
		t.Errorf("bad nonce: %d", code)
	}

	// Botanica: a static homepage without its title, the menu, the Afternoon colors, the images.
	code, _, loc := get(t, c, importURL("botanica"))
	if code != http.StatusFound || !strings.Contains(loc, "demo=imported") {
		t.Fatalf("import botanica: %d %s", code, loc)
	}
	front := int64(srv.settings.Int("page_on_front"))
	if srv.settings.String("show_on_front") != "page" || front == 0 || srv.settings.String("blogname") != "Botanica" {
		t.Fatalf("reading settings after botanica: %s %d", srv.settings.String("show_on_front"), front)
	}
	_, home, _ := get(t, anon, base+"/")
	for _, want := range []string{"Flowers grown, cut and arranged by hand", `class="wp-block-cover alignfull`, "twentytwentyfive-color-scheme-inline-css",
		"--wp--preset--color--base:#DAE7BD", `<span class="wp-block-navigation-item__label">Workshops</span>`, "twentytwentyfive-botanica-hibiscus-wide",
		`fetchpriority="high"`, ".wp-block-cover,.wp-block-cover-image{", "From the garden diary"} {
		if !strings.Contains(home, want) {
			t.Errorf("botanica home misses %q", want)
		}
	}
	if strings.Contains(home, `<h1 class="wp-block-post-title">Home</h1>`) {
		t.Error("the demo homepage prints its title")
	}
	var posts, media int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type='post' AND meta->>'_wpgo_demo' = 'twentytwentyfive-botanica'`).Scan(&posts)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE meta->>'_wpgo_demo' = 'twentytwentyfive-botanica' AND file_path LIKE '%twentytwentyfive-botanica-%.webp'`).Scan(&media)
	if posts < 6 || media < 8 {
		t.Errorf("botanica: %d posts, %d WebP images", posts, media)
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes?action=demo-content&stylesheet=twentytwentyfive")
	if !strings.Contains(body, `On your site now: <strong translate="no">Botanica</strong>`) || !strings.Contains(body, "action=remove-demo") {
		t.Error("the chooser does not show the imported set")
	}

	// Slow Seasons replaces it: Botanica's pages, posts and front page are gone.
	if code, _, loc := get(t, c, importURL("slow-seasons")); code != http.StatusFound || !strings.Contains(loc, "demo=imported") {
		t.Fatalf("import slow-seasons: %d %s", code, loc)
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE meta->>'_wpgo_demo' = 'twentytwentyfive-botanica'`).Scan(&posts)
	if posts != 0 {
		t.Errorf("%d Botanica items left after importing Slow Seasons", posts)
	}
	if id := int64(srv.settings.Int("page_on_front")); id == front || id == 0 {
		t.Errorf("front page %d after switching sets (Botanica's was %d)", id, front)
	}
	_, home, _ = get(t, anon, base+"/")
	if !strings.Contains(home, "Unhurried days, well spent") || strings.Contains(home, "Flowers grown") || strings.Contains(home, "Workshops") ||
		!strings.Contains(home, "--wp--preset--color--base:#DFDCD7") {
		t.Error("the homepage is not Slow Seasons' after switching sets")
	}
	if n := strings.Count(home, `<nav class="is-responsive`); n != 1 {
		t.Errorf("%d header navigations", n)
	}

	// Remove: the site is back to its install state.
	_, body, _ = get(t, c, base+"/wp-admin/themes?action=demo-content&stylesheet=twentytwentyfive")
	m := regexp.MustCompile(`href="(/wp-admin/themes\?action=remove-demo&amp;stylesheet=twentytwentyfive&amp;demo=slow-seasons&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Remove link")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "demo=removed") {
		t.Fatalf("remove: %d %s", code, loc)
	}
	if srv.settings.String("show_on_front") != "posts" || srv.settings.String("blogname") != "Toggle Site" || srv.themeMods("twentytwentyfive")["color_scheme"] != nil {
		t.Errorf("settings not restored: %s %q %v", srv.settings.String("show_on_front"), srv.settings.String("blogname"), srv.themeMods("twentytwentyfive"))
	}
	_, home, _ = get(t, anon, base+"/")
	if !strings.Contains(home, "Hello world!") || strings.Contains(home, "color-scheme") {
		t.Error("the blog is not back after removing the demo")
	}
}
