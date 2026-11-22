package server

import (
	"context"
	"strings"
	"testing"
)

func TestWXRNavLinks(t *testing.T) {
	content := `<!-- wp:navigation-link {"label":"Home &amp; more","type":"page","id":5,"url":"https://old.test/","kind":"post-type"} /-->
<!-- wp:navigation-submenu {"label":"<strong>Topics</strong>","url":"","kind":"custom"} -->
<!-- wp:navigation-link {"label":"News","type":"category","id":7,"url":"https://old.test/category/news/","kind":"taxonomy"} /-->
<!-- wp:navigation-link {"label":"Docs","url":"https://docs.example.test/","kind":"custom"} /-->
<!-- /wp:navigation-submenu -->
<!-- wp:page-list /-->
<!-- wp:home-link {"label":"Start"} /-->`
	got := wxrNavLinks(content)
	if len(got) != 3 {
		t.Fatalf("links: %+v", got)
	}
	if got[0].Label != "Home & more" || got[0].ID != 5 || got[0].Kind != "post-type" || got[0].Type != "page" {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Label != "Topics" || len(got[1].Children) != 2 || got[1].Children[0].ID != 7 || got[1].Children[1].URL != "https://docs.example.test/" {
		t.Errorf("submenu: %+v", got[1])
	}
	if got[2].Label != "Start" || got[2].URL != "/" || got[2].Kind != "custom" {
		t.Errorf("home link: %+v", got[2])
	}
}

const wxrBlockThemeFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel><wp:wxr_version>1.2</wp:wxr_version>
<wp:category><wp:term_id>7</wp:term_id><wp:category_nicename>news</wp:category_nicename><wp:category_parent></wp:category_parent><wp:cat_name><![CDATA[News]]></wp:cat_name></wp:category>
<item><title>About us</title><content:encoded><![CDATA[<p>About</p>]]></content:encoded>
<wp:post_id>5</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>about-us</wp:post_name><wp:status>publish</wp:status><wp:post_type>page</wp:post_type></item>
<item><title>Header navigation</title><content:encoded><![CDATA[<!-- wp:navigation-link {"label":"About","type":"page","id":5,"url":"https://old.test/about-us/","kind":"post-type"} /-->
<!-- wp:navigation-submenu {"label":"Topics","url":"https://old.test/topics/","kind":"custom"} -->
<!-- wp:navigation-link {"label":"News","type":"category","id":7,"url":"https://old.test/category/news/","kind":"taxonomy"} /-->
<!-- /wp:navigation-submenu -->]]></content:encoded>
<wp:post_id>20</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>header-navigation</wp:post_name><wp:status>publish</wp:status><wp:post_type>wp_navigation</wp:post_type></item>
<item><title>Draft navigation</title><content:encoded><![CDATA[<!-- wp:navigation-link {"label":"X","url":"https://old.test/x/","kind":"custom"} /-->]]></content:encoded>
<wp:post_id>21</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>draft-navigation</wp:post_name><wp:status>draft</wp:status><wp:post_type>wp_navigation</wp:post_type></item>
<item><title>Single Posts</title><content:encoded><![CDATA[<!-- wp:template-part {"slug":"header"} /-->]]></content:encoded>
<wp:post_id>30</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>single</wp:post_name><wp:status>publish</wp:status><wp:post_type>wp_template</wp:post_type></item>
<item><title>Header</title><content:encoded><![CDATA[<!-- wp:site-title /-->]]></content:encoded>
<wp:post_id>31</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>header</wp:post_name><wp:status>publish</wp:status><wp:post_type>wp_template_part</wp:post_type></item>
<item><title>Custom Styles</title><content:encoded><![CDATA[{"version":3,"isGlobalStylesUserThemeJSON":true}]]></content:encoded>
<wp:post_id>32</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>wp-global-styles-twentytwentyfive</wp:post_name><wp:status>publish</wp:status><wp:post_type>wp_global_styles</wp:post_type></item>
</channel></rss>`

func TestWXRImportBlockTheme(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	rep, err := srv.importWXRWith(ctx, admin, []byte(wxrBlockThemeFixture), wxrImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Navigations != 1 || rep.SkippedSiteEditor != 3 || rep.MenuItems != 3 {
		t.Errorf("report: %+v", rep)
	}
	log := strings.Join(rep.Log, "\n")
	if strings.Contains(log, "Invalid post type") || !strings.Contains(log, "Skipped 3 Site Editor items") || !strings.Contains(log, "Primary Menu") {
		t.Errorf("log:\n%s", log)
	}
	menus, err := srv.store.Menus(ctx)
	if err != nil || len(menus) != 1 || menus[0].Name != "Header navigation" || !menus[0].HasLocation("primary") {
		t.Fatalf("menus: %+v %v", menus, err)
	}
	page, err := srv.store.PostBySlug(ctx, "page", "about-us")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		label, kind, object string
		target, parent      int64
	}
	rows, err := srv.store.Pool.Query(ctx, `SELECT label, kind, object, coalesce(target_id, 0), coalesce(parent_id, 0) FROM menu_items WHERE menu_id = $1 ORDER BY id`, menus[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.label, &r.kind, &r.object, &r.target, &r.parent); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 3 {
		t.Fatalf("items: %+v", got)
	}
	if got[0].label != "About" || got[0].kind != "post_type" || got[0].object != "page" || got[0].target != page.ID {
		t.Errorf("about: %+v (page %d)", got[0], page.ID)
	}
	if got[1].label != "Topics" || got[1].kind != "custom" || got[1].parent != 0 {
		t.Errorf("topics: %+v", got[1])
	}
	if got[2].label != "News" || got[2].kind != "taxonomy" || got[2].object != "category" || got[2].parent == 0 {
		t.Errorf("news: %+v", got[2])
	}

	// A second import adds no menu.
	rep, err = srv.importWXRWith(ctx, admin, []byte(wxrBlockThemeFixture), wxrImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Navigations != 0 || rep.MenuItems != 0 {
		t.Errorf("re-import report: %+v", rep)
	}
	if menus, _ := srv.store.Menus(ctx); len(menus) != 1 {
		t.Errorf("re-import menus: %d", len(menus))
	}
}
