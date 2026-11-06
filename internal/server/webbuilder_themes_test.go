package server

import (
	"context"
	"net/http"
	"os"
	"testing"

	"wpgo/internal/webbuilder"
)

// Tests of the theme library of Pursae Web Builder (webbuilder_themes.go). The unit test reads the
// packages in library/themes; the integration test needs WPGO_TEST_ADMIN_DB / WPGO_TEST_DB.

func TestWBThemePacksOfTheLibrary(t *testing.T) {
	entries, err := os.ReadDir(themeLibraryDir)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{themeLibrary: os.DirFS(themeLibraryDir)}
	n := 0
	for _, lt := range srv.dirLibraryThemes() {
		if lt.Manifest.WebBuilder == "" {
			continue
		}
		n++
		pack, _, err := srv.wbThemePackOf(context.Background(), &lt)
		if err != nil || pack == nil {
			t.Errorf("%s: %v", lt.Manifest.Slug, err)
			continue
		}
		types := map[string]bool{}
		for _, tp := range pack.Templates {
			types[tp.Type] = true
			res, perr := webbuilder.ParseDoc(wbThemeTokens(tp.Doc, map[string]string{"hero": "/x.jpg", "about": "/y.jpg"}), webbuilder.DocOptions{UnfilteredHTML: true})
			if perr != nil || !res.Valid() {
				t.Errorf("%s/%s: %v %v", lt.Manifest.Slug, tp.Key, perr, res.Errors)
			}
		}
		for _, want := range []string{"header", "footer", "front-page", "single-post", "single-page", "archive", "search-results", "error-404"} {
			if !types[want] {
				t.Errorf("%s has no %s template", lt.Manifest.Slug, want)
			}
		}
	}
	if n < 3 || len(entries) < n {
		t.Errorf("%d library themes carry builder parts, want at least the toy shop, pet store and plant shop", n)
	}
}

func TestWBThemePackValidation(t *testing.T) {
	good := wbPackTemplate{Key: "header", Type: "header", Conditions: []string{"include/general"}, Doc: []byte(`{"sections":[]}`)}
	cases := map[string]wbThemePack{
		"format":    {Format: "x", Version: 1, Templates: []wbPackTemplate{good}},
		"none":      {Format: wbThemeFormat, Version: 1},
		"type":      {Format: wbThemeFormat, Version: 1, Templates: []wbPackTemplate{{Key: "a", Type: "nope", Conditions: []string{"include/general"}, Doc: good.Doc}}},
		"single":    {Format: wbThemeFormat, Version: 1, Templates: []wbPackTemplate{{Key: "a", Type: "single", Conditions: []string{"include/general"}, Doc: good.Doc}}},
		"dup key":   {Format: wbThemeFormat, Version: 1, Templates: []wbPackTemplate{good, good}},
		"condition": {Format: wbThemeFormat, Version: 1, Templates: []wbPackTemplate{{Key: "a", Type: "header", Conditions: []string{"include/bogus"}, Doc: good.Doc}}},
		"no rule":   {Format: wbThemeFormat, Version: 1, Templates: []wbPackTemplate{{Key: "a", Type: "header", Doc: good.Doc}}},
	}
	for name, p := range cases {
		if err := p.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := wbThemePack{Format: wbThemeFormat, Version: 1, Templates: []wbPackTemplate{good}}
	if err := ok.validate(); err != nil {
		t.Errorf("good pack: %v", err)
	}
}

func TestWBThemeImportEndToEnd(t *testing.T) {
	srv, ts, admin := wbrStart(t)
	srv.themeLibrary = os.DirFS(themeLibraryDir)
	ctx := context.Background()

	code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/themes", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %v", code, v)
	}
	themes, _ := wbrObj(v)["themes"].([]any)
	if len(themes) < 3 {
		t.Fatalf("themes %v", v)
	}
	card := wbrObj(themes[0])
	if len(card["templates"].([]any)) < 8 || card["imported"] != false {
		t.Errorf("card %v", card)
	}

	// A site with its own header: it is replaced (disabled), never deleted.
	old := wbtTemplate(t, srv, webbuilder.TemplateHeader, "My header", []string{"include/general"}, 0, "publish")
	slug := "kite-and-crayon"
	base := "/web-builder/v2/themes/" + slug

	if code, _, _ := admin.do(http.MethodPost, base+"/import", map[string]any{"mode": "nope"}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("bad mode: %d", code)
	}
	if code, _, _ := admin.do(http.MethodPost, "/web-builder/v2/themes/no-such-theme/import", map[string]any{}, nil); code != http.StatusNotFound {
		t.Errorf("unknown theme: %d", code)
	}

	importIt := func(mode string) (map[string]any, []int64, []int64) {
		code, v, _ := admin.do(http.MethodPost, base+"/import", map[string]any{"mode": mode}, nil)
		if code != http.StatusOK {
			t.Fatalf("import %s: %d %v", mode, code, v)
		}
		o := wbrObj(v)
		var tpl, pg []int64
		for _, x := range o["templates"].([]any) {
			tpl = append(tpl, int64(wbrObj(x)["id"].(float64)))
		}
		for _, x := range o["pages"].([]any) {
			pg = append(pg, int64(wbrObj(x)["id"].(float64)))
		}
		return o, tpl, pg
	}
	publish := func(ids []int64, o map[string]any, key string) {
		doc, render := wbrFixture(t, "initial")
		for i, x := range o[key].([]any) {
			rev := int(wbrObj(x)["rev"].(float64))
			page := map[string]any{"title": wbrObj(x)["title"], "slug": "x" + itoa64(ids[i]), "indexable": true}
			if code, v, _ := admin.do(http.MethodPost, wbrDocPath(ids[i], "/publish"), wbrPublishBody(doc, render, rev, "publish", page), nil); code != http.StatusOK {
				t.Fatalf("publish %d: %d %v", ids[i], code, v)
			}
		}
	}

	o, tpl, pg := importIt("replace")
	if len(tpl) != 8 || len(pg) != 2 || o["media"].(float64) < 10 {
		t.Fatalf("import: %d templates, %d pages, %v images", len(tpl), len(pg), o["media"])
	}
	// Nothing of the theme is live before the editor published it, and the old header still is.
	if p, _ := srv.store.GetPost(ctx, tpl[0]); p.Status != "draft" {
		t.Errorf("imported template is %s", p.Status)
	}
	if p, _ := srv.store.GetPost(ctx, old.ID); p.Status != "publish" {
		t.Errorf("old header is %s before finish", p.Status)
	}
	publish(tpl, o, "templates")
	publish(pg, o, "pages")
	code, v, _ = admin.do(http.MethodPost, base+"/finish", map[string]any{"mode": "replace", "templates": tpl, "pages": pg}, nil)
	if code != http.StatusOK {
		t.Fatalf("finish: %d %v", code, v)
	}
	if d, _ := wbrObj(v)["disabled"].([]any); len(d) != 1 || d[0] != "My header" {
		t.Errorf("disabled %v", v)
	}
	if wbrObj(v)["menus"].(float64) < 1 {
		t.Errorf("menus %v", v)
	}
	if p, _ := srv.store.GetPost(ctx, old.ID); p.Status != "draft" {
		t.Errorf("old header is %s after finish", p.Status)
	}
	// Foreign ids are ignored: only the theme's own posts count.
	other := wbtTemplate(t, srv, webbuilder.TemplateFooter, "Own footer", []string{"include/general"}, 0, "publish")
	if code, v, _ := admin.do(http.MethodPost, base+"/finish", map[string]any{"mode": "replace", "templates": []int64{other.ID}}, nil); code != http.StatusOK || len(wbrObj(v)["disabled"].([]any)) != 0 {
		t.Errorf("foreign finish: %d %v", code, v)
	}
	if p, _ := srv.store.GetPost(ctx, other.ID); p.Status != "publish" {
		t.Errorf("a foreign template was touched: %s", p.Status)
	}

	// Idempotent: the same posts come back, no new images, no copies.
	o2, tpl2, pg2 := importIt("replace")
	if len(tpl2) != len(tpl) || tpl2[0] != tpl[0] || pg2[0] != pg[0] || o2["media"].(float64) != 0 {
		t.Errorf("second import: %v %v media %v (first %v %v)", tpl2, pg2, o2["media"], tpl, pg)
	}
	// Keep: drafts of their own, the live templates stay.
	_, kept, _ := importIt("keep")
	if kept[0] == tpl[0] {
		t.Error("keep reused a live template")
	}
	if p, _ := srv.store.GetPost(ctx, tpl[0]); p.Status != "publish" {
		t.Errorf("a live template changed in keep mode: %s", p.Status)
	}

	// Capabilities: an editor may not list or import.
	editor := wbrUser(t, srv, ts, "themeeditor", "editor")
	if code, _, _ := editor.do(http.MethodGet, "/web-builder/v2/themes", nil, nil); code != http.StatusForbidden {
		t.Errorf("editor list: %d", code)
	}
	if code, _, _ := editor.do(http.MethodPost, base+"/import", map[string]any{}, nil); code != http.StatusForbidden {
		t.Errorf("editor import: %d", code)
	}
	if code, _, _ := admin.do(http.MethodGet, "/web-builder/v2/themes", nil, map[string]string{"X-WB-Api": "0"}); code != http.StatusConflict {
		t.Errorf("handshake: %d", code)
	}
}
