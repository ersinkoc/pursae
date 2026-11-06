package server

import (
	"context"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Tests of Appearance › Theme Builder (webbuilder_templates_admin.go): section id regeneration,
// condition labels, the screen, its row and bulk actions, the core post screen guards and the
// "no Elementor" rule. Integration tests need WPGO_TEST_ADMIN_DB / WPGO_TEST_DB.

// --- unit ------------------------------------------------------------------------------------------

func TestWBTemplatesAdminReplaceTokens(t *testing.T) {
	m := map[string]string{"s_abc1234": "s_new0001", "s_x9": "s_new0002"}
	in := `<section id="s_abc1234"><a href="#s_x9">s_abc12345</a></section>#s_abc1234:hover{} .s_abc1234-title{} /forms/7/s_x9`
	want := `<section id="s_new0001"><a href="#s_new0002">s_abc12345</a></section>#s_new0001:hover{} .s_abc1234-title{} /forms/7/s_new0002`
	if got := wbReplaceTokens(in, m); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestWBTemplatesAdminRegenerateIDs(t *testing.T) {
	doc := json.RawMessage(`{"sections":[{"id":"s_head001","kind":"nav","items":[]},{"id":"s_loop002","kind":"loop","items":[{"id":"card","sub":{"id":"s_card003","kind":"layout","items":[]}}],"content":{"source":"main"}}],"site":{}}`)
	htmlIn := `<div class="wb-page"><section class="sf" id="s_head001"></section><section class="sf" id="s_loop002"><div data-wb-loop="s_loop002"><div id="s_card003"></div></div></section></div>`
	css := `#s_head001{color:red}#s_card003 a{color:blue}`
	newDoc, newHTML, newCSS, kept := wbRegenerateSectionIDs(doc, htmlIn, css)
	if !kept {
		t.Fatal("distinctive ids should keep the snapshot")
	}
	for _, old := range []string{"s_head001", "s_loop002", "s_card003"} {
		if strings.Contains(string(newDoc), old) || strings.Contains(newHTML, old) || strings.Contains(newCSS, old) {
			t.Errorf("%s survived", old)
		}
	}
	var root map[string]any
	if err := json.Unmarshal(newDoc, &root); err != nil {
		t.Fatal(err)
	}
	var ids []string
	wbWalkSections(root, func(sec map[string]any) { ids = append(ids, sec["id"].(string)) })
	if len(ids) != 3 {
		t.Fatalf("ids %v", ids)
	}
	re := regexp.MustCompile(`^s_[0-9a-z]{7}$`)
	for _, id := range ids {
		if !re.MatchString(id) || !strings.Contains(newHTML, `id="`+id+`"`) {
			t.Errorf("new id %q not in the snapshot %s", id, newHTML)
		}
	}
	if !strings.Contains(newHTML, `data-wb-loop="`+ids[1]+`"`) || !strings.Contains(newCSS, "#"+ids[0]+"{") || !strings.Contains(newCSS, "#"+ids[2]+" a") {
		t.Errorf("renaming is not consistent: %s / %s", newHTML, newCSS)
	}
	if root["site"] == nil || root["sections"].([]any)[1].(map[string]any)["content"] == nil {
		t.Error("doc lost data")
	}

	// Editor item ids are regenerated too, with their prefix; parent links and element style keys
	// follow, short item ids ("card" above) stay (they are scoped by their section).
	if !strings.Contains(string(newDoc), `"id":"card"`) {
		t.Errorf("short item id renamed: %s", newDoc)
	}
	items := json.RawMessage(`{"sections":[{"id":"s_cust001","kind":"custom","items":[{"id":"n_root001","node":"box"},{"id":"n_kid0002","node":"text","parent":"n_root001"}],"els":{"n_kid0002":{"color":"red"}}}]}`)
	d3, h3, c3, kept3 := wbRegenerateSectionIDs(items, `<section id="s_cust001"><div class="n_root001"><p class="n_kid0002"></p></div></section>`, `.n_kid0002{color:red}`)
	if !kept3 {
		t.Fatal("item ids: snapshot dropped")
	}
	for _, old := range []string{"n_root001", "n_kid0002", "s_cust001"} {
		if strings.Contains(string(d3), old) || strings.Contains(h3, old) || strings.Contains(c3, old) {
			t.Errorf("item id %s survived: %s %s %s", old, d3, h3, c3)
		}
	}
	var r3 struct {
		Sections []struct {
			Items []struct{ ID, Parent string }
			Els   map[string]any
		}
	}
	if err := json.Unmarshal(d3, &r3); err != nil {
		t.Fatal(err)
	}
	it := r3.Sections[0].Items
	nre := regexp.MustCompile(`^n_[0-9a-z]{7}$`)
	if !nre.MatchString(it[0].ID) || !nre.MatchString(it[1].ID) || it[1].Parent != it[0].ID || r3.Sections[0].Els[it[1].ID] == nil ||
		!strings.Contains(h3, `class="`+it[1].ID+`"`) || !strings.Contains(c3, "."+it[1].ID+"{") {
		t.Errorf("item renaming is not consistent: %s / %s / %s", d3, h3, c3)
	}

	// Short ids cannot be renamed in the markup safely: the doc is renamed, the snapshot dropped.
	short := json.RawMessage(`{"sections":[{"id":"a","kind":"text","items":[],"style":{"padding":1.5}}]}`)
	d2, h2, c2, kept2 := wbRegenerateSectionIDs(short, `<a id="a">a</a>`, `#a{}`)
	if kept2 || h2 != "" || c2 != "" {
		t.Errorf("short ids kept the snapshot: %v %q %q", kept2, h2, c2)
	}
	if strings.Contains(string(d2), `"id":"a"`) || !strings.Contains(string(d2), `"padding":1.5`) {
		t.Errorf("short doc %s", d2)
	}
}

func TestWBTemplatesAdminConditionLabels(t *testing.T) {
	srv := &Server{}
	site := webbuilder.SiteBoot{PostTypes: []webbuilder.PostTypeInfo{{Slug: "post", Label: "Posts"}, {Slug: "page", Label: "Pages"}},
		Taxonomies: []webbuilder.TaxonomyInfo{{Slug: "category", Label: "Categories"}}}
	names := &wbCondNames{s: srv, posts: map[int64]string{5: "About"}, terms: map[int64]string{3: "News"}, users: map[int64]string{2: "Ann"}}
	cases := map[string]string{
		"include/general":                     "Entire Site",
		"include/404":                         "404 Page",
		"include/singular":                    "All Singular",
		"include/singular/front_page":         "Front Page",
		"include/singular/post":               "Posts",
		"include/singular/page/5":             "Pages: “About”",
		"include/singular/page/child_of/5":    "Pages: child of “About”",
		"include/singular/post/by_author/2":   "Posts by “Ann”",
		"include/singular/post/in/category/3": "Posts in Categories: “News”",
		"include/archive":                     "All Archives",
		"include/archive/home":                "Posts Page",
		"include/archive/search":              "Search Results",
		"include/archive/type/post":           "Posts Archive",
		"include/archive/author/2":            "Author Archives: “Ann”",
		"exclude/archive/category/3":          "Excluded: Categories: “News”",
		"include/archive/book":                "book",
		"nonsense":                            "nonsense",
	}
	for rule, want := range cases {
		if got, _, _ := srv.wbConditionLabel(site, rule, names); got != want {
			t.Errorf("%s: %q, want %q", rule, got, want)
		}
	}
	if _, ex, id := srv.wbConditionLabel(site, "exclude/singular/page/5", names); !ex || id != "About" {
		t.Errorf("exclude/id: %v %q", ex, id)
	}
}

// The admin, the builder's front end and Hello Pursae never say "Elementor" (branding rule).
func TestWBTemplatesAdminNoElementor(t *testing.T) {
	for _, dir := range []string{"../../web/admin", "../../web/web-builder", "../../web/themes/hello-pursae"} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err == nil && strings.Contains(strings.ToLower(string(b)), "elementor") {
				t.Errorf("%s mentions Elementor", p)
			}
			return nil
		})
	}
}

// --- integration helpers ---------------------------------------------------------------------------

const wbtaScreen = "/wp-admin/themes?page=" + webbuilder.ThemeBuilderPage

// wbtaDesigned creates a template whose published doc has distinctive section ids.
func wbtaDesigned(t *testing.T, srv *Server, typ webbuilder.TemplateType, title string, conds []string, status string) *store.WBTemplate {
	t.Helper()
	ctx := context.Background()
	wb := srv.store.WebBuilder()
	tp, err := wb.CreateTemplate(ctx, store.NewWBTemplate{Type: typ, Title: title, Conditions: conds, AuthorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	id := itoa64(tp.ID)
	d := &store.WebBuilderDoc{PostID: tp.ID, Page: []byte(`{"title":"` + title + `"}`), Fonts: []byte(`[]`), Features: []string{}, Renderer: webbuilder.RendererVersion,
		Doc:  []byte(`{"sections":[{"id":"s_orig001","kind":"nav","name":"Nav","hidden":false,"content":{},"items":[]}]}`),
		HTML: `<div class="wb-page" data-wb-root data-wb-post="` + id + `"><section class="sf" id="s_orig001">` + title + `</section></div>`,
		CSS:  `#s_orig001{color:red}`}
	if err := wb.PublishDoc(ctx, d, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = $2 WHERE id = $1`, tp.ID, status); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	return tp
}

// wbtaLink is the row action href (unescaped) of template id on the page.
func wbtaLink(t *testing.T, body string, id int64, action string) string {
	t.Helper()
	m := regexp.MustCompile(`href="(/wp-admin/themes\?page=web-builder-templates&amp;post=` + itoa64(id) + `&amp;action=` + action + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for %d", action, id)
	}
	return html.UnescapeString(m[1])
}

func wbtaStatus(t *testing.T, srv *Server, id int64) string {
	t.Helper()
	p, err := srv.store.GetPost(context.Background(), id)
	if err != nil {
		return "gone"
	}
	return p.Status
}

// wbtaDirect runs a handler as u with a nonce for action (no session: the nonce is the user's).
func wbtaDirect(srv *Server, u *store.User, path, nonceAction string, h func(*gin.Context)) *httptest.ResponseRecorder {
	c, w := wbtCtx(srv, u, path)
	n := srv.nonce(c, nonceAction)
	c.Request = httptest.NewRequest(http.MethodGet, path+"&_wpnonce="+n, nil)
	h(c)
	return w
}

// --- integration -----------------------------------------------------------------------------------

func TestWBTemplatesAdminScreen(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	about := wbrPost(t, srv, "page", "About", "publish", 1)
	head := wbtaDesigned(t, srv, webbuilder.TemplateHeader, "Main header", []string{"include/general"}, "publish")
	single := wbtaDesigned(t, srv, webbuilder.TemplateSinglePage, "About page", []string{"include/singular/page/" + itoa64(about.ID), "exclude/singular/post/by_author/1"}, "draft")

	code, body, _ := get(t, admin.c, base+wbtaScreen)
	if code != http.StatusOK {
		t.Fatalf("screen: %d %.300s", code, body)
	}
	for _, want := range []string{`<h1 class="wp-heading-inline">Theme Builder</h1>`, `class="page-title-action" id="web-builder-add-template-button"`,
		`<table class="wp-list-table widefat fixed striped table-view-list posts`, `<ul class="subsubsub">`,
		`Header <span class="count">(1)</span>`, `Single Page <span class="count">(1)</span>`, `404 Page <span class="count">(0)</span>`,
		`id="post-` + itoa64(head.ID) + `"`, `Entire Site`, `Pages: “About”`, `Excluded: Posts by “`, `<span class="post-state">Disabled</span>`,
		`Edit with Pursae Web Builder`, `Edit Conditions`, `>Duplicate</a>`, `>Disable</a>`, `>Enable</a>`, `>Trash</a>`,
		`id="web-builder-add-template"`, `id="web-builder-conditions"`, `web-builder-templates.js`, `id="bulk-action-selector-top"`} {
		if !strings.Contains(body, want) {
			t.Errorf("screen lacks %s", want)
		}
	}
	if !regexp.MustCompile(`<li class="(?:wp-first-item )?current"><a href="/wp-admin/themes\?page=web-builder-templates"[^>]*>Theme Builder</a>`).MatchString(body) {
		t.Error("no current Web Builder › Theme Builder menu entry")
	}
	if !strings.Contains(body, `data-conditions="[&#34;include/singular/page/`+itoa64(about.ID)) || !strings.Contains(body, `&#34;include/singular/page/`+itoa64(about.ID)+`&#34;:&#34;About&#34;}"`) {
		t.Errorf("row data for the conditions dialog: %s", regexp.MustCompile(`data-conditions="[^"]*" data-labels="[^"]*"`).FindAllString(body, -1))
	}
	if strings.Contains(body, "web-builder-theme-notice") {
		t.Error("theme notice while Hello Pursae is active")
	}
	_ = wbtaLink(t, body, single.ID, "enable")
	// Filters: type tab and search.
	if _, b, _ := get(t, admin.c, base+wbtaScreen+"&template_type=header"); !strings.Contains(b, "Main header") || strings.Contains(b, "About page") {
		t.Error("type filter")
	}
	if _, b, _ := get(t, admin.c, base+wbtaScreen+"&s=about"); strings.Contains(b, "Main header") || !strings.Contains(b, "About page") {
		t.Error("search")
	}
	// The plugins row describes the Theme Builder.
	if _, b, _ := get(t, admin.c, base+"/wp-admin/plugins"); !strings.Contains(b, "Appearance &rsaquo; Theme Builder") {
		t.Error("plugins row description")
	}

	// Core screens: the posts list of templates is the Theme Builder, a template opens in the builder.
	if code, _, loc := get(t, admin.c, base+"/wp-admin/edit?post_type="+webbuilder.TemplatePostType+"&trashed=1&ids=4"); code != http.StatusFound || loc != wbtaScreen+"&ids=4&trashed=1" {
		t.Errorf("edit?post_type: %d %s", code, loc)
	}
	if code, _, loc := get(t, admin.c, base+"/wp-admin/post?post="+itoa64(head.ID)+"&action=edit"); code != http.StatusFound || loc != wbEditURL(head.ID) {
		t.Errorf("post edit: %d %s", code, loc)
	}

	// Authors and editors get nothing.
	for _, role := range []string{"author", "editor"} {
		cl := wbrUser(t, srv, base, "tba-"+role, role)
		for _, p := range []string{wbtaScreen, "/wp-admin/edit?post_type=" + webbuilder.TemplatePostType, "/wp-admin/post?post=" + itoa64(head.ID) + "&action=edit",
			wbEditURL(head.ID)} {
			if code, b, _ := get(t, cl.c, base+p); code != http.StatusForbidden || strings.Contains(b, "Main header") {
				t.Errorf("%s %s: %d", role, p, code)
			}
		}
		if _, b, _ := get(t, cl.c, base+"/wp-admin/"); strings.Contains(b, "page=web-builder-templates") {
			t.Errorf("%s sees the Theme Builder menu", role)
		}
	}

	// Another theme: the notice offers to activate Hello Pursae.
	if err := srv.switchTheme(ctx, "twentytwentyfive"); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(ctx, map[string]any{wbHelloOfferedOption: true}); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, admin.c, base+wbtaScreen)
	m := regexp.MustCompile(`href="([^"]*action=activate-hello[^"]*)" class="button button-primary" id="web-builder-activate-hello"`).FindStringSubmatch(body)
	if !strings.Contains(body, `id="web-builder-theme-notice"`) || m == nil {
		t.Fatal("no Activate notice on another theme")
	}
	if code, _, loc := get(t, admin.c, base+html.UnescapeString(m[1])); code != http.StatusFound || loc != wbtaScreen+"&hello=1" {
		t.Fatalf("activate: %d %s", code, loc)
	}
	if got := srv.settings.String("stylesheet"); got != webbuilder.HelloPursaeTheme {
		t.Errorf("stylesheet %s", got)
	}

	// Plugin off: no screen, no menu.
	if err := srv.wbSetActive(ctx, false); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := get(t, admin.c, base+wbtaScreen); code != http.StatusForbidden {
		t.Errorf("screen while off: %d", code)
	}
	if _, b, _ := get(t, admin.c, base+"/wp-admin/"); strings.Contains(b, "page=web-builder-templates") {
		t.Error("menu while off")
	}
}

func TestWBTemplatesAdminActions(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	wb := srv.store.WebBuilder()
	tp := wbtaDesigned(t, srv, webbuilder.TemplateFooter, "Site footer", []string{"include/general", "exclude/404"}, "publish")
	bare, err := wb.CreateTemplate(ctx, store.NewWBTemplate{Type: webbuilder.Template404, Title: "Not designed", AuthorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := wb.SetTemplateConditions(ctx, tp.ID, []string{"include/general", "exclude/404"}, 7); err != nil {
		t.Fatal(err)
	}
	screen := func() string {
		_, b, _ := get(t, admin.c, base+wbtaScreen)
		return b
	}
	act := func(link string) string {
		t.Helper()
		code, _, loc := get(t, admin.c, base+link)
		if code != http.StatusFound {
			t.Fatalf("%s: %d", link, code)
		}
		return loc
	}

	// Duplicate: a disabled copy with the rules and new section and item ids.
	loc := act(wbtaLink(t, screen(), tp.ID, "duplicate"))
	m := regexp.MustCompile(`duplicated=(\d+)`).FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("duplicate redirect %s", loc)
	}
	cpID, _ := strconv.ParseInt(m[1], 10, 64)
	cp, err := wb.Template(ctx, cpID)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Title != "Site footer (Copy)" || cp.Status != "draft" || cp.Type != webbuilder.TemplateFooter || strings.Join(cp.Conditions, ",") != "include/general,exclude/404" || cp.Priority != 7 {
		t.Errorf("copy %+v", cp)
	}
	d, err := wb.Doc(ctx, cpID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(d.Doc), "s_orig001") || strings.Contains(d.HTML, "s_orig001") || strings.Contains(d.CSS, "s_orig001") {
		t.Errorf("copy kept the source ids: %s %s %s", d.Doc, d.HTML, d.CSS)
	}
	newID := regexp.MustCompile(`"id": ?"(s_[0-9a-z]{7})"`).FindStringSubmatch(string(d.Doc))
	if newID == nil || !strings.Contains(d.HTML, `id="`+newID[1]+`"`) || !strings.Contains(d.CSS, "#"+newID[1]+"{") || !strings.Contains(d.HTML, `data-wb-post="`+itoa64(cpID)+`"`) {
		t.Errorf("copy doc %s / %s / %s", d.Doc, d.HTML, d.CSS)
	}

	// Disable / Enable.
	if loc := act(wbtaLink(t, screen(), tp.ID, "disable")); !strings.Contains(loc, "disabled=1") || wbtaStatus(t, srv, tp.ID) != "draft" {
		t.Errorf("disable: %s %s", loc, wbtaStatus(t, srv, tp.ID))
	}
	if loc := act(wbtaLink(t, screen(), tp.ID, "enable")); !strings.Contains(loc, "enabled=1") || wbtaStatus(t, srv, tp.ID) != "publish" {
		t.Errorf("enable: %s %s", loc, wbtaStatus(t, srv, tp.ID))
	}
	if loc := act(wbtaLink(t, screen(), bare.ID, "enable")); !strings.Contains(loc, "wb_error=snapshot") || wbtaStatus(t, srv, bare.ID) != "draft" {
		t.Errorf("enable without a design: %s %s", loc, wbtaStatus(t, srv, bare.ID))
	}
	if _, b, _ := get(t, admin.c, base+wbtaScreen+"&wb_error=snapshot"); !strings.Contains(b, "published once in Pursae Web Builder") {
		t.Error("snapshot error notice")
	}

	// Trash, the trash view, Restore, Delete Permanently.
	if loc := act(wbtaLink(t, screen(), tp.ID, "trash")); !strings.Contains(loc, "trashed=1") || wbtaStatus(t, srv, tp.ID) != "trash" {
		t.Errorf("trash: %s", loc)
	}
	_, trash, _ := get(t, admin.c, base+wbtaScreen+"&post_status=trash")
	if !strings.Contains(trash, "Site footer") || !strings.Contains(trash, `<option value="untrash">Restore</option>`) || strings.Contains(trash, "Not designed") {
		t.Error("trash view")
	}
	if loc := act(wbtaLink(t, trash, tp.ID, "untrash")); !strings.Contains(loc, "untrashed=1") || wbtaStatus(t, srv, tp.ID) != "draft" {
		t.Errorf("untrash: %s %s", loc, wbtaStatus(t, srv, tp.ID))
	}
	act(wbtaLink(t, screen(), tp.ID, "trash"))
	_, trash, _ = get(t, admin.c, base+wbtaScreen+"&post_status=trash")
	if loc := act(wbtaLink(t, trash, tp.ID, "delete")); !strings.Contains(loc, "deleted=1") || wbtaStatus(t, srv, tp.ID) != "gone" {
		t.Errorf("delete: %s", loc)
	}
	// A stale or foreign nonce does nothing.
	if code, _, _ := get(t, admin.c, base+wbtaScreen+"&post="+itoa64(bare.ID)+"&action=trash&_wpnonce=bad"); code != http.StatusForbidden || wbtaStatus(t, srv, bare.ID) != "draft" {
		t.Errorf("bad nonce: %d", code)
	}

	// Bulk: the list form's nonce.
	body := screen()
	bn := regexp.MustCompile(`<input type="hidden" name="_wpnonce" value="([^"]+)" />\s*<div class="tablenav top">`).FindStringSubmatch(body)
	if bn == nil {
		t.Fatal("no bulk nonce")
	}
	other := wbtaDesigned(t, srv, webbuilder.TemplateHeader, "Bulk header", []string{"include/general"}, "publish")
	bulk := wbtaScreen + "&action=-1&action2=trash&bulk_action=Apply&_wpnonce=" + bn[1] + "&post%5B%5D=" + itoa64(other.ID) + "&post%5B%5D=" + itoa64(cpID)
	if loc := act(bulk); !strings.Contains(loc, "trashed=2") || wbtaStatus(t, srv, other.ID) != "trash" || wbtaStatus(t, srv, cpID) != "trash" {
		t.Errorf("bulk trash: %s", loc)
	}
	_, undo, _ := get(t, admin.c, base+wbtaScreen+"&trashed=2&ids="+itoa64(other.ID)+","+itoa64(cpID))
	um := regexp.MustCompile(`<a href="([^"]+)">Undo</a>`).FindStringSubmatch(undo)
	if um == nil {
		t.Fatal("no Undo link")
	}
	if loc := act(html.UnescapeString(um[1])); !strings.Contains(loc, "untrashed=2") || wbtaStatus(t, srv, other.ID) != "draft" {
		t.Errorf("undo: %s", loc)
	}
	// Searching with a bulk action selected only lists.
	if code, _, _ := get(t, admin.c, base+wbtaScreen+"&action=trash&s=Bulk"); code != http.StatusOK {
		t.Errorf("search with an action selected: %d", code)
	}

	// Add New without JavaScript.
	cm := regexp.MustCompile(`name="action" value="create" />\s*<input type="hidden" name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if cm == nil {
		t.Fatal("no create nonce")
	}
	code, _, loc := get(t, admin.c, base+wbtaScreen+"&action=create&type=archive&title=Blog&starter=archive&_wpnonce="+cm[1])
	if code != http.StatusFound || !strings.HasPrefix(loc, "/wp-admin/post?post=") || !strings.HasSuffix(loc, "&action="+wbEditAction) {
		t.Fatalf("create: %d %s", code, loc)
	}
	list, _ := wb.Templates(ctx, store.WBTemplateFilter{Type: webbuilder.TemplateArchive})
	if len(list) != 1 || list[0].Title != "Blog" || list[0].Starter != webbuilder.StarterArchive || list[0].Status != "draft" {
		t.Errorf("created %+v", list)
	}
	if _, _, loc := get(t, admin.c, base+wbtaScreen+"&action=create&type=single&_wpnonce="+cm[1]); !strings.Contains(loc, "wb_error=create") {
		t.Errorf("single without a post type: %s", loc)
	}
}

func TestWBTemplatesAdminCoreTrash(t *testing.T) {
	srv, base, _ := wbrStart(t)
	ctx := context.Background()
	tp := wbtaDesigned(t, srv, webbuilder.TemplateHeader, "Guarded", []string{"include/general"}, "publish")
	edID := asCreateUser(t, srv, "tba-core-editor", "editor", asStrongPass)
	_ = base
	editor, err := srv.store.UserByID(ctx, edID)
	if err != nil {
		t.Fatal(err)
	}
	adminU, err := srv.store.UserByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := itoa64(tp.ID)
	// An editor may delete_post a wb_template by the core's mapping, but not here.
	for _, a := range []string{"trash", "delete"} {
		w := wbtaDirect(srv, editor, "/wp-admin/post?post="+id+"&action="+a, a+"-post_"+id, srv.postEdit)
		if w.Code != http.StatusForbidden || wbtaStatus(t, srv, tp.ID) != "publish" {
			t.Errorf("editor %s: %d %s", a, w.Code, wbtaStatus(t, srv, tp.ID))
		}
	}
	w := wbtaDirect(srv, adminU, "/wp-admin/post?post="+id+"&action=trash", "trash-post_"+id, srv.postEdit)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), wbtaScreen) || wbtaStatus(t, srv, tp.ID) != "trash" {
		t.Errorf("admin core trash: %d %s %s", w.Code, w.Header().Get("Location"), wbtaStatus(t, srv, tp.ID))
	}
	w = wbtaDirect(srv, editor, "/wp-admin/post?post="+id+"&action=untrash", "untrash-post_"+id, srv.postEdit)
	if w.Code != http.StatusForbidden || wbtaStatus(t, srv, tp.ID) != "trash" {
		t.Errorf("editor untrash: %d", w.Code)
	}
	w = wbtaDirect(srv, adminU, "/wp-admin/post?post="+id+"&action=untrash", "untrash-post_"+id, srv.postEdit)
	if w.Code != http.StatusFound || wbtaStatus(t, srv, tp.ID) != "draft" {
		t.Errorf("admin core untrash: %d %s", w.Code, wbtaStatus(t, srv, tp.ID))
	}
	// The Theme Builder's own links refuse an editor too.
	w = wbtaDirect(srv, editor, wbtaScreen+"&post="+id+"&action=trash", "trash-wb-template_"+id, srv.themesScreen)
	if w.Code != http.StatusForbidden || wbtaStatus(t, srv, tp.ID) != "draft" {
		t.Errorf("editor Theme Builder trash: %d", w.Code)
	}
}

// Core screens that only check edit_post (Quick Edit, revisions, Pursae Multi Language ajax)
// cannot change a template for an editor: template meta caps map to edit_theme_options.
func TestWBTemplatesCoreScreensNeedThemeCap(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	head := wbtaDesigned(t, srv, webbuilder.TemplateHeader, "Site header", []string{"include/general"}, "publish")
	var revID int64
	if err := srv.store.Pool.QueryRow(ctx, `INSERT INTO post_revisions(post_id, author_id, title, excerpt, content) VALUES ($1, 1, 'Old header', '', 'old') RETURNING id`, head.ID).Scan(&revID); err != nil {
		t.Fatal(err)
	}
	ed := wbrUser(t, srv, base, "tbcore-editor", "editor")
	_, body, _ := get(t, ed.c, base+"/wp-admin/edit")
	inline := attr(t, body, `id="_inline_edit" name="_inline_edit" value="([^"]+)"`)
	_, out, _ := post(t, ed.c, base+"/wp-admin/admin-ajax", url.Values{"action": {"inline-save"}, "_inline_edit": {inline}, "post_ID": {itoa64(head.ID)},
		"post_type": {webbuilder.TemplatePostType}, "post_title": {"Hacked"}, "_status": {"draft"}})
	if !strings.Contains(out, "not allowed") {
		t.Errorf("editor inline-save on a template = %.200q", out)
	}
	if got := wbtaStatus(t, srv, head.ID); got != "publish" {
		t.Errorf("template status after editor inline-save = %s", got)
	}
	if code, _, _ := get(t, ed.c, base+"/wp-admin/revision?revision="+itoa64(revID)); code != http.StatusForbidden {
		t.Errorf("editor revision screen on a template: %d", code)
	}
	// The admin keeps Quick Edit on templates.
	_, body, _ = get(t, admin.c, base+"/wp-admin/edit")
	inline = attr(t, body, `id="_inline_edit" name="_inline_edit" value="([^"]+)"`)
	post(t, admin.c, base+"/wp-admin/admin-ajax", url.Values{"action": {"inline-save"}, "_inline_edit": {inline}, "post_ID": {itoa64(head.ID)},
		"post_type": {webbuilder.TemplatePostType}, "post_title": {"Site header"}, "_status": {"draft"}})
	if got := wbtaStatus(t, srv, head.ID); got != "draft" {
		t.Errorf("admin inline-save on a template: status %s", got)
	}
}
