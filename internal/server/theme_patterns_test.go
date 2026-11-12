package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/blocks"
)

// TestBlockPatternsRendersInContent checks which patterns the inserter offers: those whose blocks
// render in post content.
func TestBlockPatternsRendersInContent(t *testing.T) {
	blocks.RegisterDynamic("test/pattern-dyn", func(context.Context, *blocks.Block, string) string { return "x" })
	for content, want := range map[string]bool{
		`<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->`:                                               true,
		`<!-- wp:group --><div class="wp-block-group"><!-- wp:test/pattern-dyn /--></div><!-- /wp:group -->`: true,
		`<!-- wp:group --><div class="wp-block-group"><!-- wp:test/no-renderer /--></div><!-- /wp:group -->`: false,
		`<!-- wp:template-part {"slug":"sidebar"} /-->`:                                                      false,
	} {
		if got := patternRendersInContent(content); got != want {
			t.Errorf("%s: %v", content, got)
		}
	}
}

// TestTwentyTwentyFivePatterns checks that the block editor gets Twenty Twenty-Five's patterns and
// pattern categories from /wp/v2/block-patterns, with working image URLs, and that the generated
// patterns file has every pattern of the theme without PHP.
func TestTwentyTwentyFivePatterns(t *testing.T) {
	raw, err := os.ReadFile("../../web/themes/twentytwentyfive/patterns.json")
	if err != nil {
		t.Fatal(err)
	}
	var file themePatternSet
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Patterns) < 90 || len(file.Categories) != 2 {
		t.Fatalf("patterns.json: %d patterns, %d categories", len(file.Patterns), len(file.Categories))
	}
	for _, p := range file.Patterns {
		content, _ := p["content"].(string)
		if !strings.HasPrefix(p["name"].(string), "twentytwentyfive/") || content == "" || strings.Contains(content, "<?php") ||
			strings.Contains(content, "<!-- wp:pattern ") || strings.Contains(content, "example.test") {
			t.Errorf("pattern %v: bad content", p["name"])
		}
	}

	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	if code, _, _ := get(t, &http.Client{}, base+"/wp-json/wp/v2/block-patterns/patterns"); code != http.StatusUnauthorized {
		t.Errorf("anonymous patterns: %d", code)
	}
	code, body := restGet(t, c, base, "/wp-json/wp/v2/block-patterns/patterns")
	if code != http.StatusOK {
		t.Fatalf("patterns: %d %s", code, body)
	}
	var patterns []map[string]any
	if err := json.Unmarshal([]byte(body), &patterns); err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for _, p := range patterns {
		byName[p["name"].(string)] = p
	}
	if byName["core/query-standard-posts"] == nil || len(patterns) != len(corePatterns())+len(file.Patterns) {
		t.Errorf("%d patterns", len(patterns))
	}
	banner := byName["twentytwentyfive/banner-intro-image"]
	if banner == nil {
		t.Fatal("no twentytwentyfive/banner-intro-image")
	}
	if banner["title"] != "Short heading and paragraph and image on the left" || banner["inserter"] == false {
		t.Errorf("banner: %v", banner)
	}
	if cats, _ := json.Marshal(banner["categories"]); string(cats) != `["banner","featured"]` {
		t.Errorf("banner categories %s", cats)
	}
	content := banner["content"].(string)
	img := regexp.MustCompile(`src="(http://[^"]+/wp-content/themes/twentytwentyfive/images/botany-flowers\.webp)"`).FindStringSubmatch(content)
	if img == nil || !strings.Contains(content, `alt="Picture of a flower"`) || !strings.Contains(content, "New arrivals") {
		t.Fatalf("banner content: %.600s", content)
	}
	if code, _, _ := get(t, c, img[1]); code != http.StatusOK {
		t.Errorf("pattern image %s: %d", img[1], code)
	}
	// Inserted into a post, the pattern renders with the theme's layout classes: constrained groups get
	// the root padding (theme.json's useRootPaddingAwareAlignments).
	if out := blocks.Render(context.Background(), content); !strings.Contains(out, `class="wp-block-group alignfull has-global-padding is-layout-constrained`) {
		t.Errorf("rendered banner: %.400s", out)
	}
	// Old core categories are migrated (query → posts), as WP_REST_Block_Patterns_Controller does.
	if cats, _ := json.Marshal(byName["twentytwentyfive/template-query-loop"]["categories"]); string(cats) != `["posts"]` {
		t.Errorf("query loop categories %s", cats)
	}
	// Page layouts are offered; template patterns that need the Site Editor stay out of the inserter, like
	// "Inserter: no". Header and footer render in content now that navigation and site logo are
	// server-side blocks, so they are offered like in WordPress (their files have no "Inserter: no").
	if byName["twentytwentyfive/page-business-home"]["inserter"] == false {
		t.Error("page-business-home is hidden")
	}
	for _, name := range []string{"twentytwentyfive/hidden-404"} {
		if byName[name] == nil || byName[name]["inserter"] != false {
			t.Errorf("%s inserter %v", name, byName[name]["inserter"])
		}
	}

	code, body = restGet(t, c, base, "/wp-json/wp/v2/block-patterns/categories")
	var cats []map[string]string
	_ = json.Unmarshal([]byte(body), &cats)
	found := map[string]map[string]string{}
	for _, cat := range cats {
		found[cat["name"]] = cat
	}
	if code != http.StatusOK || found["banner"] == nil || found["twentytwentyfive_page"]["label"] != "Pages" ||
		found["twentytwentyfive_page"]["description"] != "A collection of full page layouts." || found["twentytwentyfive_post-format"] == nil {
		t.Errorf("categories: %d %s", code, body)
	}

	// In Turkish the editor lists them with Turkish titles, descriptions and category labels (the
	// theme's text domain: WordPress.org's twentytwentyfive pack, shipped in web/languages).
	if err := srv.settings.Set(context.Background(), map[string]any{"WPLANG": "tr_TR"}); err != nil {
		t.Fatal(err)
	}
	_, body = restGet(t, c, base, "/wp-json/wp/v2/block-patterns/patterns")
	if !strings.Contains(body, `"title":"Kitap açıklaması içeren banner"`) || strings.Contains(body, `"title":"Banner with book description"`) {
		t.Errorf("Turkish pattern titles: %.300s", body)
	}
	_, body = restGet(t, c, base, "/wp-json/wp/v2/block-patterns/categories")
	if !strings.Contains(body, `"label":"Sayfalar"`) || !strings.Contains(body, `"description":"Tam sayfa düzenlerinden oluşan bir koleksiyon."`) {
		t.Errorf("Turkish pattern categories: %s", body)
	}
	for _, p := range srv.themePatterns(srv.activeTheme()).Patterns { // the registry itself stays English
		if p["name"] == "twentytwentyfive/banner-about-book" && p["title"] != "Banner with book description" {
			t.Errorf("registry title %v", p["title"])
		}
	}
}
