package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

const gweLibFixture = "testdata/golementor-library"

// TestGolementorLibraryLoad loads the fixture library and checks the validation of broken files.
func TestGolementorLibraryLoad(t *testing.T) {
	lib := gweLoadLibrary(os.DirFS(gweLibFixture))
	if len(lib.Errors) > 0 {
		t.Fatalf("fixture errors:\n%s", strings.Join(lib.Errors, "\n"))
	}
	var ids []string
	for _, s := range lib.Sections {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, ","); got != "hero/hero-split,cta/cta-banner,contact/contact-form" { // category order, then slug
		t.Errorf("sections: %s", got)
	}
	if len(lib.Kits) != 1 || lib.Kits[0].Slug != "fixture-studio" || len(lib.Kits[0].Parts) != 7 || len(lib.Kits[0].Pages) != 5 {
		t.Fatalf("kits: %+v", lib.Kits)
	}

	// Broken files: each is left out with a precise message, the valid ones still load.
	img := &fstest.MapFile{Data: []byte("RIFF")}
	section := func(category, body string) string {
		return `{"title":"T","category":"` + category + `","tags":["a"],"thumbnail":"s.webp","content":` + body + `}`
	}
	okTree := `[{"elType":"container","settings":{},"elements":[{"elType":"widget","widgetType":"heading","settings":{"title":"x"}}]}]`
	fsys := fstest.MapFS{
		"images/a.webp":              img,
		"sections/hero/ok.json":      {Data: []byte(strings.Replace(section("hero", okTree), "s.webp", "ok.webp", 1))},
		"sections/hero/ok.webp":      img,
		"sections/hero/s.json":       {Data: []byte(section("cta", okTree))},
		"sections/cta/s.json":        {Data: []byte(section("cta", `[{"elType":"widget","widgetType":"heading","settings":{}}]`))},
		"sections/cta/s.webp":        img,
		"sections/faq/s.json":        {Data: []byte(section("faq", `[{"elType":"container","settings":{},"elements":[{"elType":"widget","widgetType":"no-such-widget","settings":{}}]}]`))},
		"sections/faq/s.webp":        img,
		"sections/team/s.json":       {Data: []byte(section("team", `[{"elType":"container","settings":{"__globals__":{"background_color":"globals/colors?id=fxlight","x":"nonsense"}},"elements":[]}]`))},
		"sections/team/s.webp":       img,
		"sections/logos/s.json":      {Data: []byte(section("logos", `[{"elType":"container","settings":{"u":"{{library:images/missing.webp}}","v":"{{media:x}}","w":"{{page:Bad Slug}}"},"elements":[]}]`))},
		"sections/logos/s.webp":      img,
		"sections/stats/s.json":      {Data: []byte(`{"title":"T","category":"stats","thumbnail":"s.webp","content":[],"colour":"red"}`)},
		"sections/stats/s.webp":      img,
		"sections/blog/s.json":       {Data: []byte(`{"title":`)},
		"sections/nope/s.json":       {Data: []byte(section("nope", okTree))},
		"sections/gallery/s.json":    {Data: []byte(section("gallery", okTree))},
		"sections/hero/deep/x.json":  {Data: []byte(section("hero", okTree))},
		"kits/bad/kit.json":          {Data: []byte(`{"title":"Bad","category":"x","screenshot":"shot.webp","globals":{"colors":[{"_id":"primary","title":"P","color":"blue"}],"typography":[]},"parts":[{"type":"footer","conditions":["include/everywhere"],"content":` + okTree + `}],"pages":[{"slug":"home","title":"Home","front_page":true,"content":` + okTree + `}],"menus":[{"location":"sidebar","name":"M","items":[{"page":"nope"}]}],"posts":[{"title":"P","image":"images/a.webp"}]}`)},
		"sections/newsletter/s.json": {Data: []byte(section("newsletter", okTree))},
		"sections/newsletter/s.webp": img,
	}
	lib = gweLoadLibrary(fsys)
	if len(lib.Sections) != 2 || lib.section("hero/ok") == nil || lib.section("newsletter/s") == nil {
		t.Errorf("valid sections: %d", len(lib.Sections))
	}
	all := strings.Join(lib.Errors, "\n")
	for _, want := range []string{
		`sections/hero/s.json: category "cta" does not match its directory "hero"`,
		"sections/hero/s.json: category \"cta\" does not match its directory \"hero\"; thumbnail s.webp is missing",
		`sections/cta/s.json: content: top-level elements must be containers (found widget "heading")`,
		`sections/faq/s.json: content: unknown widget "no-such-widget"`,
		`sections/team/s.json: content: __globals__.background_color: unknown global color "fxlight"`,
		`__globals__.x: malformed reference "nonsense"`,
		"token {{library:images/missing.webp}}: file is missing",
		"unknown token {{media:x}}",
		"token {{page:Bad Slug}}: not a page slug",
		`sections/stats/s.json: invalid JSON: json: unknown field "colour"`,
		"sections/blog/s.json: invalid JSON",
		`sections/nope/s.json: unknown category directory "nope"`,
		"sections/gallery/s.json: thumbnail s.webp is missing",
		"sections/hero/deep/x.json: sections must be sections/<category>/<slug>.json",
		`kits/bad/kit.json: screenshot "shot.webp" is missing`,
		`globals.colors[0]: color "blue" is not a hex color`,
		`globals.colors: "secondary" is missing`,
		`globals.typography: "primary" is missing`,
		`parts[0] (footer): invalid condition "include/everywhere"`,
		"parts: a header is required",
		`menus[0]: location "sidebar" is not primary or footer`,
		`menus[0].items[0]: the kit has no page "nope"`,
		"posts[0]: image must be a {{library:images/…}} token",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing error %q in:\n%s", want, all)
		}
	}
	if len(lib.Kits) != 0 {
		t.Error("the broken kit loaded")
	}
}

// TestGolementorLibraryContent validates the library that ships in web/gowebeditor/library (the
// Studio sections and kits): every file must load without a problem.
func TestGolementorLibraryContent(t *testing.T) {
	lib := gweLoadLibrary(os.DirFS("../../web/gowebeditor/library"))
	if len(lib.Errors) > 0 {
		t.Errorf("%d invalid library file(s):\n%s", len(lib.Errors), strings.Join(lib.Errors, "\n"))
	}
	t.Logf("%d sections, %d kits", len(lib.Sections), len(lib.Kits))
}

// TestGolementorLibraryFreshTree: fresh ids everywhere, the key order of every object kept.
func TestGolementorLibraryFreshTree(t *testing.T) {
	in := `[{"elType":"container","settings":{"z":1,"a":{"y":2,"b":3}},"elements":[{"id":"old0001","elType":"widget","widgetType":"heading","settings":{"title":"x"}}]}]`
	out, err := gweLibFreshTree(json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`^\[\{"id":"([0-9a-f]{7})","elType":"container","settings":\{"z":1,"a":\{"y":2,"b":3\}\},"elements":\[\{"id":"([0-9a-f]{7})","elType":"widget","widgetType":"heading","settings":\{"title":"x"\},"elements":\[\]\}\]\}\]$`).FindStringSubmatch(string(out))
	if m == nil || m[1] == m[2] || m[2] == "old0001" {
		t.Errorf("fresh tree: %s", out)
	}
	if got := gweLibReplace(`{"u":"{{library:images/a b.webp}}","t":"{{page:x}}"}`, true, func(kind, arg string) string { return kind + `"` + arg }); got != `{"u":"library\"images/a b.webp","t":"page\"x"}` {
		t.Errorf("replace: %s", got)
	}
}

// TestGolementorLibraryREST: the editor routes, the classic library's Blocks tab and inserting.
func TestGolementorLibraryREST(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	srv.gweLibOverride = os.DirFS(gweLibFixture)
	base := ts.URL
	installAndLogin(t, ts, c)
	assets := base + "/wp-content/plugins/web-builder/assets/library/"

	// Editors only: a cookie without the REST nonce counts as logged out.
	if code, _, _ := get(t, c, base+"/wp-json/web-builder/v1/library/sections"); code != http.StatusUnauthorized {
		t.Errorf("without nonce: %d", code)
	}
	code, body := restGet(t, c, base, "/wp-json/web-builder/v1/library/sections")
	if code != http.StatusOK {
		t.Fatalf("sections: %d %s", code, body)
	}
	var list struct {
		Categories []struct {
			Slug, Title string
			Count       int
		}
		Items []struct {
			ID, Title, Category, Thumbnail string
			Tags                           []string
		}
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Categories) != 3 || list.Categories[0].Slug != "hero" || list.Categories[0].Title != "Hero" || list.Categories[0].Count != 1 ||
		list.Categories[1].Title != "Call to action" || list.Categories[2].Slug != "contact" {
		t.Errorf("categories: %+v", list.Categories)
	}
	if len(list.Items) != 3 || list.Items[0].ID != "hero/hero-split" || list.Items[0].Title != "Hero with image on the right" ||
		list.Items[0].Category != "hero" || strings.Join(list.Items[0].Tags, "|") != "image|two columns|cta" ||
		list.Items[0].Thumbnail != assets+"sections/hero/hero-split.webp" {
		t.Errorf("items: %+v", list.Items)
	}
	for _, want := range []string{`"categories":[{"`, `"items":[{"`, `"count":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("sections JSON misses %s", want)
		}
	}

	fetch := func(id string) []any {
		t.Helper()
		code, body := restGet(t, c, base, "/wp-json/web-builder/v1/library/sections/"+id)
		var out map[string][]any
		if code != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil || len(out["content"]) == 0 || len(out) != 1 {
			t.Fatalf("section %s: %d %.300s", id, code, body)
		}
		return out["content"]
	}
	ids := func(tree []any) []string {
		var out []string
		var walk func(list []any)
		walk = func(list []any) {
			for _, it := range list {
				m := it.(map[string]any)
				out = append(out, m["id"].(string))
				walk(m["elements"].([]any))
			}
		}
		walk(tree)
		return out
	}
	hero := fetch("hero/hero-split")
	raw, _ := json.Marshal(hero)
	if !strings.Contains(string(raw), `"url":"`+assets+`images/hero.webp"`) || !strings.Contains(string(raw), `"link":{"is_external":"","nofollow":"","url":"#"}`) ||
		strings.Contains(string(raw), "{{") {
		t.Errorf("tokens: %s", raw)
	}
	first, second := ids(hero), ids(fetch("hero/hero-split"))
	seen := map[string]bool{}
	for _, id := range append(first, second...) {
		if !regexp.MustCompile(`^[0-9a-f]{7}$`).MatchString(id) || seen[id] {
			t.Errorf("element id %q is not fresh", id)
		}
		seen[id] = true
	}
	if len(first) != 6 {
		t.Errorf("hero elements: %v", first)
	}
	if cta := ids(fetch("cta/cta-banner")); strings.Contains(strings.Join(cta, ","), "keepme") {
		t.Errorf("ids kept: %v", cta)
	}
	if code, _ := restGet(t, c, base, "/wp-json/web-builder/v1/library/sections/hero/nope"); code != http.StatusNotFound {
		t.Errorf("unknown section: %d", code)
	}

	code, body = restGet(t, c, base, "/wp-json/web-builder/v1/library/kits")
	if code != http.StatusOK {
		t.Fatalf("kits: %d %s", code, body)
	}
	for _, want := range []string{`"slug":"fixture-studio"`, `"title":"Fixture Studio"`, `"screenshot":"` + assets + `kits/fixture-studio/screenshot.webp"`,
		`{"front_page":true,"optional":false,"posts_page":false,"slug":"home","title":"Home"}`, `"parts":["header","footer","single-post","single-page","archive","search-results","error-404"]`,
		`{"_id":"primary","color":"#1D4ED8","title":"Primary"}`, `{"_id":"primary","family":"Fraunces","title":"Headings"}`, `"posts":3`, `"tags":["business","test"]`} {
		if !strings.Contains(body, want) {
			t.Errorf("kits JSON misses %s:\n%s", want, body)
		}
	}

	// The classic library: the Blocks tab lists the sections (source remote, type block) with a
	// category filter, and inserts them.
	code, body = restGet(t, c, base, "/wp-json/elementor/v1/template-library/templates?source=remote")
	var tl struct {
		Templates []map[string]any
		Config    map[string]any
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &tl) != nil {
		t.Fatalf("template library: %d %.300s", code, body)
	}
	var blocks []map[string]any
	for _, tp := range tl.Templates {
		if tp["type"] == "block" {
			blocks = append(blocks, tp)
		}
	}
	if len(blocks) != 3 || blocks[0]["template_id"] != "gwe-section:hero/hero-split" || blocks[0]["source"] != "remote" ||
		blocks[0]["subtype"] != "Hero" || blocks[0]["thumbnail"] != assets+"sections/hero/hero-split.webp" || blocks[0]["accessTier"] != "free" {
		t.Errorf("blocks: %v", blocks)
	}
	if cats, _ := json.Marshal(tl.Config); string(cats) != `{"block":{"categories":["Hero","Call to action","Contact"]}}` {
		t.Errorf("library config: %s", cats)
	}
	_, editor, _ := get(t, c, base+"/wp-admin/post?post=2&action=web-builder")
	nonce := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(editor)
	if nonce == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	if !strings.Contains(editor, "templates/source/is-remote") || !strings.Contains(editor, `<style id="golementor-library-blocks">`) {
		t.Error("the editor lacks the remote-source filter or the Blocks tab")
	}
	resp, err := c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {nonce[1]}, "editor_post_id": {"2"},
		"actions": {`{"d":{"action":"get_template_data","data":{"source":"remote","template_id":"gwe-section:contact/contact-form","edit_mode":true}}}`}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"success":true`) || !strings.Contains(string(b), "FX-CONTACT-FORM") || !strings.Contains(string(b), `"widgetType":"form"`) {
		t.Errorf("block data: %.400s", b)
	}

	// Off with the plugin.
	togglePlugin(t, c, base, "deactivate")
	if code, _ := restGet(t, c, base, "/wp-json/web-builder/v1/library/sections"); code != http.StatusNotFound {
		t.Errorf("library while Pursae Web Builder is off: %d", code)
	}
}
