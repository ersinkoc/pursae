package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Regression tests for three bugs found while building a site with Pursae Multi Language, Pursae
// Custom Fields, Pursae SEO and a theme package.

// A theme package declares its page templates in theme.json "page_templates"; each needs its file.
func TestThemePackagePageTemplates(t *testing.T) {
	edit := map[string]string{`"slug": "starter",`: `"slug": "starter", "page_templates": {"page-projects": "Projects"},`}
	m, err := parseThemeManifest([]byte(starterManifest(t, edit)))
	if err != nil {
		t.Fatalf("page_templates rejected: %v", err)
	}
	if m.PageTemplates["page-projects"] != "Projects" {
		t.Fatalf("PageTemplates = %v", m.PageTemplates)
	}
	for name, tc := range map[string]struct{ json, want string }{
		"bad name":    {`{"Page Projects": "Projects"}`, `page_templates key "Page Projects"`},
		"html suffix": {`{"page-projects.html": "Projects"}`, `page_templates key "page-projects.html"`},
		"hierarchy":   {`{"page": "Page"}`, "template of the hierarchy"},
		"no label":    {`{"page-projects": " "}`, `page_templates["page-projects"]`},
		"markup":      {`{"page-projects": "<b>Projects</b>"}`, `page_templates["page-projects"]`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseThemeManifest([]byte(starterManifest(t, map[string]string{`"slug": "starter",`: `"slug": "starter", "page_templates": ` + tc.json + `,`})))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.want)
			}
		})
	}

	dir := filepath.Join(t.TempDir(), "starter")
	if err := os.CopyFS(dir, os.DirFS(starterDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(starterManifest(t, edit)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadThemePackage(dir, "starter"); err == nil || !strings.Contains(err.Error(), "templates/page-projects.html") {
		t.Fatalf("a page template without its file loaded: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "page-projects.html"), []byte(`{{template "page" .}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := loadThemePackage(dir, "starter")
	if err != nil {
		t.Fatal(err)
	}
	if th.Templates["page-projects"] != "Projects" {
		t.Fatalf("Theme.Templates = %v", th.Templates)
	}
}

// A page whose _wp_page_template the theme does not list keeps it: the editors offer it, saving
// does not reset it, and the REST template field reads and writes it.
func TestPageTemplateKeptWhenNotListed(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	pool := getPool(t)

	code, body := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages", `{"title":"Projects","content":"<!-- wp:paragraph --><p>Work.</p><!-- /wp:paragraph -->","status":"publish"}`)
	if code != http.StatusCreated {
		t.Fatalf("create page: %d %s", code, body)
	}
	var created map[string]any
	_ = json.Unmarshal([]byte(body), &created)
	id := strconv.FormatInt(int64(created["id"].(float64)), 10)
	// As bulk edit on the Pages list stores it: a template of a theme package that could not declare it.
	if _, err := pool.Exec(ctx, `UPDATE posts SET meta = meta || '{"_wp_page_template":"page-projects"}' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	stored := func() string {
		t.Helper()
		var tpl *string
		_ = pool.QueryRow(ctx, `SELECT meta->>'_wp_page_template' FROM posts WHERE id = $1`, id).Scan(&tpl)
		if tpl == nil {
			return "<none>"
		}
		return *tpl
	}

	// REST reads the template.
	code, body = restGet(t, c, base, "/wp-json/wp/v2/pages/"+id+"?context=edit")
	if code != http.StatusOK || !strings.Contains(body, `"template":"page-projects"`) {
		t.Fatalf("REST template not read: %d %s", code, body)
	}
	// Pursae Editor offers it in availableTemplates, so its Template panel keeps it.
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+id+"&action=edit")
	if !strings.Contains(body, `"page-projects":"page-projects"`) {
		t.Error("the block editor's availableTemplates lacks the page's template")
	}
	// Saving from Pursae Editor (REST, unchanged template sent back) keeps it.
	if code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages/"+id, `{"title":"Projects","template":"page-projects","status":"publish"}`); code != http.StatusOK {
		t.Fatalf("REST save: %d %s", code, body)
	}
	if got := stored(); got != "page-projects" {
		t.Fatalf("REST save changed the template to %q", got)
	}
	if code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages/"+id, `{"title":"Projects again"}`); code != http.StatusOK || stored() != "page-projects" {
		t.Fatalf("REST save without template: %d, template %q", code, stored())
	}
	// REST writes: a listed template, "" for the default; an unknown one is refused.
	if code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages/"+id, `{"template":"no-such-template"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown template accepted: %d %s", code, body)
	}
	if code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages/"+id, `{"template":"page-no-title"}`); code != http.StatusOK || !strings.Contains(body, `"template":"page-no-title"`) || stored() != "page-no-title" {
		t.Fatalf("listed template not written: %d %q %s", code, stored(), body)
	}
	if code, _ = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages/"+id, `{"template":""}`); code != http.StatusOK || stored() != "<none>" {
		t.Fatalf("default template not written: %d %q", code, stored())
	}
	if code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages", `{"title":"Wide","template":"page-no-title"}`); code != http.StatusCreated || !strings.Contains(body, `"template":"page-no-title"`) {
		t.Fatalf("create with a template: %d %s", code, body)
	}

	// The classic editor form selects the unlisted template, so saving it posts the same value back.
	if _, err := pool.Exec(ctx, `UPDATE posts SET meta = meta || '{"_wp_page_template":"page-projects"}' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(ctx, map[string]any{"classic-editor-replace": "classic", "classic-editor-allow-users": "disallow"}); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+id+"&action=edit")
	if !strings.Contains(body, `<option value="page-projects" selected="selected">page-projects</option>`) {
		t.Error("the classic editor's Template select does not keep the page's template")
	}
	// Quick Edit's shared select has an option for it too.
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=page")
	if !strings.Contains(body, `<option value="page-projects">page-projects</option>`) {
		t.Error("Quick Edit's Template select lacks the page's template")
	}
}

// With Share slugs on, saving a page without a lang parameter keeps the slug it shares with its
// translation (it was made unique across languages: "projects-2").
func TestShareSlugsSaveKeepsSlug(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	ctx := context.Background()
	o := srv.pllOpts()
	o.ShareSlugs = true
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}

	page := func(query, title string) int64 {
		t.Helper()
		code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages"+query, map[string]any{"title": title, "slug": "projects", "status": "publish"})
		if code != http.StatusCreated || obj["slug"] != "projects" {
			t.Fatalf("create %q: %d %v", title, code, obj["slug"])
		}
		return int64(obj["id"].(float64))
	}
	en := page("?lang=en", "Projects")
	tr := page("?lang=tr&translations[en]="+strconv.FormatInt(en, 10), "Projeler")

	for _, tc := range []struct {
		id   int64
		body map[string]any
	}{
		{en, map[string]any{"title": "Projects", "content": "Updated."}},
		{en, map[string]any{"title": "Projects", "slug": "projects"}},
		{tr, map[string]any{"title": "Projeler", "slug": "projects"}},
	} {
		code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages/"+strconv.FormatInt(tc.id, 10), tc.body)
		if code != http.StatusOK || obj["slug"] != "projects" {
			t.Errorf("save page %d without lang: %d slug %v, want projects", tc.id, code, obj["slug"])
		}
	}
	if code, _, loc := get(t, c, base+"/projects/"); code != http.StatusOK {
		t.Errorf("/projects/: %d %s", code, loc)
	}
	if code, body, _ := get(t, c, base+"/tr/projects/"); code != http.StatusOK || !strings.Contains(body, "Projeler") {
		t.Errorf("/tr/projects/: %d", code)
	}
}

// The canonical link of a translated static front page is that language's home URL.
func TestSEOGoCanonicalOfTranslatedFrontPage(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	seogoToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	ctx := context.Background()

	code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages?lang=en", map[string]any{"title": "Home", "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("create home: %d", code)
	}
	en := int64(obj["id"].(float64))
	code, _, _ = pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages?lang=tr&translations[en]="+strconv.FormatInt(en, 10), map[string]any{"title": "Anasayfa", "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("create TR home: %d", code)
	}
	if err := srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": en}); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{"/": base + "/", "/tr/": base + "/tr/"} {
		v := &http.Client{} // a visitor: no session, no language cookie
		code, body, _ := get(t, v, base+path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
		if !strings.Contains(body, `<link rel="canonical" href="`+want+`" />`) {
			i := strings.Index(body, `rel="canonical"`)
			t.Errorf("%s: canonical is not %s: %s", path, want, body[max(0, i-10):min(len(body), i+80)])
		}
		if !strings.Contains(body, `<meta property="og:url" content="`+want+`" />`) {
			t.Errorf("%s: og:url is not %s", path, want)
		}
	}
}
