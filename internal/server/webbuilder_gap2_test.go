package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"wpgo/internal/cache"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Scheduled publish: status future stores the snapshot now with the given date; the page stays
// off the site until the publish_future_post cron flips it. A past date publishes at once, a bad
// date is refused, and a user without publish_posts still only submits for review.
func TestWebBuilderRESTScheduledPublish(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	page := wbrPost(t, srv, "page", "Scheduled Page", "draft", 1)
	doc, render := wbrFixture(t, "initial")

	body := wbrPublishBody(doc, render, 0, "future", map[string]any{"title": "Scheduled Page", "slug": "scheduled-page", "indexable": true})
	body["date"] = "not a date"
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), body, nil); code != http.StatusBadRequest {
		t.Fatalf("bad date: %d %v", code, v)
	}
	when := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	body["date"] = when.Format(time.RFC3339)
	code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), body, nil)
	if o := wbrObj(v); code != http.StatusOK || o["status"] != "future" || o["date"] == "" {
		t.Fatalf("schedule: %d %v", code, v)
	}
	p, err := srv.store.GetPost(ctx, page.ID)
	if err != nil || p.Status != "future" || p.PublishedAt == nil || !p.PublishedAt.Equal(when) {
		t.Fatalf("stored post: %+v %v", p, err)
	}
	// The document reports the schedule to the editor.
	if code, v, _ := admin.do(http.MethodGet, wbrDocPath(page.ID, ""), nil, nil); code != http.StatusOK || wbrObj(v)["status"] != "future" || wbrObj(v)["date"] == "" {
		t.Fatalf("document: %d %v", code, v)
	}
	if r := anonGet(t, base+"/?page_id="+itoa64(page.ID)); r.StatusCode != http.StatusNotFound {
		t.Errorf("a scheduled page is public: %d", r.StatusCode)
	}
	// The cron publishes it once the date has passed; the stored snapshot is what visitors get.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET published_at = now() - interval '1 minute' WHERE id = $1`, page.ID); err != nil {
		t.Fatal(err)
	}
	srv.publishDuePosts(ctx)
	if p, _ := srv.store.GetPost(ctx, page.ID); p.Status != "publish" {
		t.Fatalf("after the cron: %s", p.Status)
	}
	if r := anonGet(t, base+"/scheduled-page/"); r.StatusCode != http.StatusOK || !strings.Contains(bodyOf(r), "wb-page") {
		t.Errorf("published by the cron: %d", r.StatusCode)
	}

	// A date in the past publishes now.
	page2 := wbrPost(t, srv, "page", "Past Page", "draft", 1)
	body2 := wbrPublishBody(doc, render, 0, "future", nil)
	body2["date"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(page2.ID, "/publish"), body2, nil); code != http.StatusOK || wbrObj(v)["status"] != "publish" {
		t.Fatalf("past date: %d %v", code, v)
	}

	// A contributor cannot schedule: the page goes to review.
	uid := asCreateUser(t, srv, "contsched", "contributor", asStrongPass)
	cc := asNewClient()
	if code, _, _ := asLogin(t, cc, base, "/wp-login", "contsched", asStrongPass); code != http.StatusFound {
		t.Fatalf("login contributor: %d", code)
	}
	cont := newWBRClient(t, cc, base)
	page3 := wbrPost(t, srv, "post", "Contrib Post", "draft", uid)
	body3 := wbrPublishBody(doc, render, 0, "future", nil)
	body3["date"] = when.Format(time.RFC3339)
	if code, v, _ := cont.do(http.MethodPost, wbrDocPath(page3.ID, "/publish"), body3, nil); code != http.StatusOK || wbrObj(v)["status"] != "pending" {
		t.Fatalf("contributor: %d %v", code, v)
	}
	if p, _ := srv.store.GetPost(ctx, page3.ID); p.Status != "pending" {
		t.Errorf("contributor post: %s", p.Status)
	}

	// A template cannot be scheduled.
	tpl := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Hdr", []string{"include/general"}, 0, "")
	body4 := wbrPublishBody(doc, render, 1, "future", nil)
	body4["date"] = when.Format(time.RFC3339)
	if code, _, _ := admin.do(http.MethodPost, wbrDocPath(tpl.ID, "/publish"), body4, nil); code != http.StatusBadRequest {
		t.Errorf("template schedule: %d", code)
	}
}

// Audience rules: a header for logged-in users only. Anonymous visitors never get it (and their
// page stays cacheable); a logged-in visitor gets it, and that page is never cached.
func TestWBAudienceConditionFront(t *testing.T) {
	srv, base, admin := wbtfStart(t)
	about := wbtfPost(t, srv, "page", "About Us", "About body.", 0)
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Members header", []string{"include/general", "include/user/logged_in"}, `<p>MEMBERS-MARK</p>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Editors footer", []string{"include/general", "include/user/role/editor"}, `<p>EDITORS-MARK</p>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Guest footer", []string{"include/general", "exclude/user/logged_in"}, `<p>GUEST-MARK</p>`, "", "", nil)

	r := anonGet(t, base+"/"+about.Slug+"/")
	body := bodyOf(r)
	wbtfBad(t, "anonymous", body, "MEMBERS-MARK", "EDITORS-MARK")
	wbtfWant(t, "anonymous", body, "GUEST-MARK", "About body.")

	resp, err := admin.Get(base + "/" + about.Slug + "/")
	if err != nil {
		t.Fatal(err)
	}
	body = bodyOf(resp)
	wbtfWant(t, "admin", body, "MEMBERS-MARK")
	wbtfBad(t, "admin", body, "GUEST-MARK", "EDITORS-MARK") // administrator is not editor
	// Pursae Cache strips its control header before the response leaves; check the guard directly.
	c, _ := wbtCtx(srv, nil, "/")
	srv.wbAudienceGuard(c, &wbTemplateIndex{needUser: true})
	if h := c.Writer.Header().Get(cache.HeaderControl); h != "" {
		t.Errorf("anonymous visitor with an audience rule: %s = %q, want none", cache.HeaderControl, h)
	}
	c, _ = wbtCtx(srv, &store.User{ID: 1}, "/")
	srv.wbAudienceGuard(c, &wbTemplateIndex{needUser: true})
	if h := c.Writer.Header().Get(cache.HeaderControl); h != "no-cache" {
		t.Errorf("logged-in visitor with an audience rule: %s = %q, want no-cache", cache.HeaderControl, h)
	}

	// An editor matches the role rule.
	asCreateUser(t, srv, "audeditor", "editor", asStrongPass)
	ed := asNewClient()
	if code, _, _ := asLogin(t, ed, base, "/wp-login", "audeditor", asStrongPass); code != http.StatusFound {
		t.Fatalf("login editor: %d", code)
	}
	resp, err = ed.Get(base + "/" + about.Slug + "/")
	if err != nil {
		t.Fatal(err)
	}
	body = bodyOf(resp)
	wbtfWant(t, "editor", body, "EDITORS-MARK")
	wbtfBad(t, "editor", body, "GUEST-MARK")

	// The REST grammar accepts the rules, refuses a bad role.
	cl := newWBRClient(t, admin, base)
	tpl := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Rules", nil, 0, "draft")
	path := "/web-builder/v2/templates/" + itoa64(tpl.ID) + "/conditions"
	if code, v, _ := cl.do(http.MethodPut, path, map[string]any{"conditions": []string{"include/general", "include/user/role/shop_manager", "exclude/user/logged_out"}, "priority": 0}, nil); code != http.StatusOK {
		t.Fatalf("audience rules: %d %v", code, v)
	}
	if code, _, _ := cl.do(http.MethodPut, path, map[string]any{"conditions": []string{"include/user/role/Bad Role"}, "priority": 0}, nil); code == http.StatusOK {
		t.Errorf("bad role accepted")
	}
}

// Popups show with any theme, not only Hello Pursae.
func TestWBPopupOtherTheme(t *testing.T) {
	srv, base, _ := wbtfStart(t)
	if err := srv.switchTheme(context.Background(), "twentytwentyfive"); err != nil {
		t.Fatal(err)
	}
	about := wbtfPost(t, srv, "page", "About Us", "About body.", 0)
	pop := wbtfTemplate(t, srv, webbuilder.TemplatePopup, "Offer", []string{"include/general"}, `<h2>OTHER-THEME-POPUP</h2>`, "", ".wbx{color:red}", nil)
	code, body := wbtfGet(t, base+"/"+about.Slug+"/")
	if code != http.StatusOK {
		t.Fatalf("page: %d", code)
	}
	id := itoa64(pop.ID)
	wbtfWant(t, "tt5", body, `data-wb-popup="`+id+`"`, "OTHER-THEME-POPUP", `id="wb-popup-js"`, `id="wb-popups"`, `<style id="wb-css-`+id+`">`, `id="wb-tokens"`, "About body.")
	if strings.Count(body, `id="wb-popup-js"`) != 1 {
		t.Errorf("popup script repeated")
	}
	if i, j := strings.Index(body, `id="wb-popups"`), strings.Index(body, "</head>"); i < 0 || i > j {
		t.Errorf("popup CSS not in the head")
	}
}

// Template files: export a popup with its settings and conditions, import it as a new disabled
// template; a file that is not a template file, or a bad rule in it, is refused.
func TestWebBuilderRESTTemplateExportImport(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	pop := wbtTemplate(t, srv, webbuilder.TemplatePopup, "Spring Sale", []string{"include/singular/page", "exclude/user/logged_in"}, 5, "")
	if err := srv.store.WebBuilder().SetTemplatePopup(ctx, pop.ID, `{"trigger":"scroll","delay":0,"scroll":30,"selector":"","frequency":"once","days":7,"version":2,"position":"top","width":500,"overlay":true,"afterConsent":false}`); err != nil {
		t.Fatal(err)
	}
	code, v, hdr := admin.do(http.MethodGet, "/web-builder/v2/templates/"+itoa64(pop.ID)+"/export", nil, nil)
	f := wbrObj(v)
	if code != http.StatusOK || f["format"] != webbuilder.TemplateFileFormat || f["type"] != "popup" || f["title"] != "Spring Sale" || f["priority"] != float64(5) {
		t.Fatalf("export: %d %v", code, v)
	}
	if cd := hdr.Get("Content-Disposition"); !strings.Contains(cd, "wb-template-popup-spring-sale.json") {
		t.Errorf("download name: %q", cd)
	}
	if p, _ := f["popup"].(map[string]any); p["trigger"] != "scroll" || p["width"] != float64(500) {
		t.Errorf("popup settings: %v", f["popup"])
	}

	code, v, _ = admin.do(http.MethodPost, "/web-builder/v2/templates/import", f, nil)
	id := int64(0)
	if n, ok := wbrObj(v)["id"].(float64); ok {
		id = int64(n)
	}
	if code != http.StatusCreated || id == 0 || id == pop.ID {
		t.Fatalf("import: %d %v", code, v)
	}
	got, err := srv.store.WebBuilder().Template(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != webbuilder.TemplatePopup || got.Title != "Spring Sale" || got.Priority != 5 || len(got.Conditions) != 2 || got.Status == "publish" {
		t.Errorf("imported template: %+v", got)
	}
	if raw, _ := srv.store.WebBuilder().TemplatePopup(ctx, id); webbuilder.ParsePopup(raw).Trigger != "scroll" {
		t.Errorf("imported popup settings: %s", raw)
	}

	bad := map[string]any{}
	b, _ := json.Marshal(f)
	_ = json.Unmarshal(b, &bad)
	bad["format"] = "something-else"
	if code, _, _ := admin.do(http.MethodPost, "/web-builder/v2/templates/import", bad, nil); code == http.StatusCreated {
		t.Errorf("foreign file imported")
	}
	bad["format"] = webbuilder.TemplateFileFormat
	bad["conditions"] = []string{"include/nowhere/at/all"}
	if code, _, _ := admin.do(http.MethodPost, "/web-builder/v2/templates/import", bad, nil); code == http.StatusCreated {
		t.Errorf("bad rule imported")
	}

	// Only template managers may import or export.
	ed := wbrUser(t, srv, base, "tplio", "author")
	if code, _, _ := ed.do(http.MethodPost, "/web-builder/v2/templates/import", f, nil); code != http.StatusForbidden {
		t.Errorf("author import: %d", code)
	}
	if code, _, _ := ed.do(http.MethodGet, "/web-builder/v2/templates/"+itoa64(pop.ID)+"/export", nil, nil); code != http.StatusForbidden {
		t.Errorf("author export: %d", code)
	}
}

// Export Theme Parts: the published templates become a library parts file the theme import reads.
func TestWBThemePartsExport(t *testing.T) {
	srv, _, _ := wbtfStart(t)
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Head A", []string{"include/general"}, `<p>A</p>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Head B", []string{"include/singular"}, `<p>B</p>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Foot", []string{"include/general"}, `<p>F</p>`, "", "", nil)
	c, _ := wbtCtx(srv, &store.User{ID: 1}, "/")
	pack, err := srv.wbThemePartsExport(c)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, p := range pack.Templates {
		keys[p.Key] = p.Title
		if len(p.Doc) == 0 {
			t.Errorf("%s: no doc", p.Key)
		}
	}
	if pack.Format != "web-builder-theme" || keys["header"] != "Head A" || keys["header-2"] != "Head B" || keys["footer"] != "Foot" {
		t.Fatalf("pack: %+v", keys)
	}
	b, _ := json.Marshal(pack)
	var back wbThemePack
	if json.Unmarshal(b, &back) != nil || len(back.Templates) != 3 {
		t.Fatalf("round trip: %s", b)
	}
}
