package server

import (
	"context"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

func TestPreviewRefused(t *testing.T) {
	for p, refused := range map[string]bool{
		"/": false, "/about/": false, "/wp-json/wp/v2/posts": false, "/wp-admin/css/common.min.css": false,
		"/wp-admin/assets/css/x.css": false, "/wp-content/uploads/a.jpg": false, "/?s=sea": false,
		"/wp-admin": true, "/wp-admin/": true, "/wp-admin/themes": true, "/wp-admin/css/": true, "/wp-login": true,
		"/wp-login.php": true, "/wp-comments-post": true, "/xmlrpc.php": true, "/wp-admin/../wp-admin/options": true,
	} {
		if got := previewRefused(http.MethodGet, p) != ""; got != refused {
			t.Errorf("GET %s refused=%v, want %v", p, got, refused)
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if previewRefused(m, "/") == "" {
			t.Errorf("%s allowed", m)
		}
	}
	if previewSchema("wpgo-fixture") != "wpgo_preview_wpgo_fixture" {
		t.Fatal(previewSchema("wpgo-fixture"))
	}
	if long := previewSchema(strings.Repeat("a", 64)); len(long) > 63 || !strings.HasPrefix(long, previewSchemaBase) {
		t.Fatalf("long schema %q", long)
	}
}

func TestPreviewRewriter(t *testing.T) {
	rw := newPreviewRewriter("127.0.0.1:8110", "http://localhost:8110", "/wp-admin/theme-preview/x")
	in := `<a href="http://127.0.0.1:8110/about/">a</a><a href="http://127.0.0.1:8110">h</a><a href="http://127.0.0.1:81100/no">n</a>` +
		`<link href="/wp-content/themes/x/style.css?ver=1"><img src="/wp-content/uploads/a.jpg" srcset="/wp-content/uploads/a-300.jpg 300w, /wp-content/uploads/a.jpg 600w">` +
		`<a href="//cdn.example.com/x">c</a><a href="https://example.org/">e</a><form action="/"><div style="background:url('/img.png')">` +
		`<script>var s={"root":"http:\/\/127.0.0.1:8110\/wp-json\/"};</script><a href="http://localhost:8110/x">l</a>`
	got := string(rw.body([]byte(in), true))
	for _, want := range []string{`href="http://127.0.0.1:8110/wp-admin/theme-preview/x/about/"`, `href="http://127.0.0.1:8110/wp-admin/theme-preview/x/"`,
		`href="http://127.0.0.1:81100/no"`, `href="/wp-admin/theme-preview/x/wp-content/themes/x/style.css?ver=1"`,
		`src="/wp-admin/theme-preview/x/wp-content/uploads/a.jpg"`, `srcset="/wp-admin/theme-preview/x/wp-content/uploads/a-300.jpg 300w, /wp-admin/theme-preview/x/wp-content/uploads/a.jpg 600w"`,
		`href="//cdn.example.com/x"`, `href="https://example.org/"`, `action="/wp-admin/theme-preview/x/"`, `url('/wp-admin/theme-preview/x/img.png')`,
		`"http:\/\/127.0.0.1:8110\/wp-admin\/theme-preview\/x\/wp-json\/"`, `href="http://localhost:8110/wp-admin/theme-preview/x/x"`} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten markup lacks %s\n%s", want, got)
		}
	}
	if h := rw.header("/about/"); h != "/wp-admin/theme-preview/x/about/" {
		t.Error(h)
	}
	if h := rw.header("http://127.0.0.1:8110/about/"); h != "http://127.0.0.1:8110/wp-admin/theme-preview/x/about/" {
		t.Error(h)
	}
}

// TestThemePreviewSandbox previews the fixture library theme: a sandbox is built with the demo content,
// its pages render under the preview prefix with every link inside it, and the site itself (posts,
// settings, active theme, theme registry, uploads, themes directory) is untouched.
func TestThemePreviewSandbox(t *testing.T) {
	themesDir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", themesDir)
	lib := libraryCopy(t)
	ts, c, srv := testServerBuilderOff(t)
	srv.themeLibrary = os.DirFS(lib)
	t.Cleanup(srv.closePreviews)
	t.Cleanup(func() {
		unregisterTheme("wpgo-fixture")
		unregisterTheme("wpgo-fixture-two")
	})
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Main Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	ctx := context.Background()
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := srv.store.Pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	files := func(dir string) []string {
		var out []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				out = append(out, strings.TrimPrefix(p, dir))
			}
			return nil
		})
		sort.Strings(out)
		return out
	}
	type siteState struct {
		Posts, Media, Terms, Menus, Widgets, Users, Schemas int
		Blogname, Stylesheet, Home                          string
		Uploads, Themes                                     string
	}
	state := func() siteState {
		return siteState{
			Posts: count(`SELECT count(*) FROM posts`), Media: count(`SELECT count(*) FROM media`), Terms: count(`SELECT count(*) FROM terms`),
			Menus: count(`SELECT count(*) FROM menus`), Widgets: count(`SELECT count(*) FROM widgets`), Users: count(`SELECT count(*) FROM users`),
			Schemas:  count(`SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`),
			Blogname: srv.settings.String("blogname"), Stylesheet: srv.settings.String("stylesheet"), Home: srv.settings.String("home"),
			Uploads: strings.Join(files(srv.cfg.UploadsDir), ","), Themes: strings.Join(files(themesDir), ","),
		}
	}
	before := state()
	prefix := "/wp-admin/theme-preview/wpgo-fixture"

	// The Library offers Live Preview.
	_, body, _ := get(t, c, base+"/wp-admin/theme-install")
	if !strings.Contains(body, `href="/wp-admin/theme-preview?theme=wpgo-fixture"`) || !strings.Contains(body, `href="/wp-admin/theme-preview?theme=wpgo-fixture-two"`) {
		t.Fatal("library cards lack Live Preview")
	}
	if strings.Contains(body, "Clear theme previews") {
		t.Fatal("Clear theme previews offered with no previews")
	}

	// The preview screen: WordPress's theme-install overlay around the sandbox.
	code, body, _ := get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture")
	if code != http.StatusOK {
		t.Fatalf("preview screen: %d %s", code, body)
	}
	for _, want := range []string{`theme-install-overlay wp-full-overlay expanded`, `class="wp-full-overlay-sidebar"`, `<iframe src="` + prefix + `/"`,
		`class="close-full-overlay" href="/wp-admin/theme-install"`, "Fixture Journal", "Version: 1.1.0", "Quiet serif journal",
		`>Install</a>`, "Install &amp; Import Demo", `class="preview-tablet"`, `class="preview-mobile"`, "collapse-sidebar", "full-overlay-active",
		`class="previous-theme" href="/wp-admin/theme-preview?theme=wpgo-fixture-two"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("preview screen lacks %q", want)
		}
	}
	waitReady(t, c, base, "wpgo-fixture")

	// Home: the demo's static front page with the theme's stylesheet, the menu and the widgets.
	resp, err := c.Get(base + prefix + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Robots-Tag") != "noindex, nofollow" || len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("sandbox headers: %v", resp.Header)
	}
	code, home, _ := get(t, c, base+prefix+"/")
	if code != http.StatusOK {
		t.Fatalf("preview home: %d %s", code, home)
	}
	for _, want := range []string{"fixture-page-wide", prefix + `/wp-content/themes/wpgo-fixture/style.css?ver=1.1.0`,
		"--fixture-accent:#c2410c", "Fixture Journal", "noindex"} {
		if !strings.Contains(home, want) {
			t.Fatalf("preview home lacks %q:\n%s", want, home)
		}
	}
	checkPreviewLinks(t, home, base, prefix)
	// The posts page: the demo posts, the menu and the widgets.
	code, blog, _ := get(t, c, base+prefix+"/blog/")
	for _, want := range []string{"First light", "Slow coffee", "Unfiled", "Elsewhere", "See <a href=", "about us"} {
		if code != http.StatusOK || !strings.Contains(blog, want) {
			t.Fatalf("preview posts page lacks %q: %d\n%s", want, code, blog)
		}
	}
	checkPreviewLinks(t, blog, base, prefix)
	if strings.Contains(home, "Hello world!") || strings.Contains(home, "wpadminbar") {
		t.Fatal("preview home shows the install sample or the toolbar")
	}
	if code, css, _ := get(t, c, base+prefix+"/wp-content/themes/wpgo-fixture/style.css?ver=1.1.0"); code != http.StatusOK || css == "" {
		t.Fatalf("theme stylesheet: %d", code)
	}
	href := func(body, pattern string) string {
		t.Helper()
		m := regexp.MustCompile(`(?:href|src)="(` + pattern + `)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no link matching %s", pattern)
		}
		u := html.UnescapeString(m[1])
		if strings.HasPrefix(u, "/") {
			u = base + u
		}
		return u
	}
	// Post, page, category, upload: every URL stays in the preview.
	for _, tc := range []struct{ pattern, want string }{
		{regexp.QuoteMeta(base+prefix) + `/[^"]*first-light/`, "single single-post"},
		{regexp.QuoteMeta(base+prefix) + `/about/`, "page-template-default page page-id-"},
		{regexp.QuoteMeta(base+prefix) + `/category/travel/`, "First light"},
	} {
		u := href(blog, tc.pattern)
		code, page, loc := get(t, c, u)
		if code != http.StatusOK || !strings.Contains(page, tc.want) {
			t.Fatalf("%s: %d %s, lacks %q", u, code, loc, tc.want)
		}
		checkPreviewLinks(t, page, base, prefix)
	}
	// A demo image: in the sandbox's uploads, served under the prefix.
	var file string
	if err := srv.previews.get("wpgo-fixture").pool.QueryRow(ctx, `SELECT file_path FROM media ORDER BY id LIMIT 1`).Scan(&file); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv.previews.root(), "wpgo-fixture", "uploads", file)); err != nil {
		t.Fatalf("demo image not in the sandbox uploads: %v", err)
	}
	img := base + prefix + "/wp-content/uploads/" + file
	if resp, err := c.Get(img); err != nil || resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		t.Fatalf("demo image %s: %v %v", img, err, resp)
	} else {
		resp.Body.Close()
	}
	if code, page, _ := get(t, c, base+prefix+"/wp-json/wp/v2/media"); code != http.StatusOK || !strings.Contains(page, base+prefix+"/wp-content/uploads/") {
		t.Fatalf("REST media in the preview: %d %s", code, page)
	}
	// The posts page, a search and REST reads work; a redirect stays inside.
	if code, page, _ := get(t, c, base+prefix+"/?s=sea"); code != http.StatusOK || !strings.Contains(page, "First light") {
		t.Fatalf("search: %d", code)
	}
	if code, page, _ := get(t, c, base+prefix+"/wp-json/wp/v2/posts"); code != http.StatusOK || !strings.Contains(page, `First light`) ||
		!strings.Contains(page, base+prefix+"/") {
		t.Fatalf("REST in the preview: %d %s", code, page)
	}
	if code, _, loc := get(t, c, base+prefix+"/about"); code != http.StatusMovedPermanently || !strings.HasPrefix(loc, base+prefix+"/about/") {
		t.Fatalf("canonical redirect: %d %q", code, loc)
	}

	// Read-only: no POST, no dashboard, no login, no comment posting.
	for _, p := range []string{"/", "/wp-comments-post", "/wp-json/wp/v2/posts", "/wp-login"} {
		if code, _, _ := post(t, c, base+prefix+p, url.Values{"comment": {"x"}, "comment_post_ID": {"1"}}); code != http.StatusForbidden {
			t.Fatalf("POST %s: %d", p, code)
		}
	}
	for _, p := range []string{"/wp-admin/", "/wp-admin/options-general", "/wp-login", "/wp-login.php"} {
		if code, _, _ := get(t, c, base+prefix+p); code != http.StatusForbidden {
			t.Fatalf("GET %s: %d", p, code)
		}
	}

	// The site is untouched.
	after := state()
	if after != before {
		t.Fatalf("the site changed:\nbefore %+v\nafter  %+v", before, after)
	}
	if themeBySlug("wpgo-fixture") != nil || srv.installedTheme("wpgo-fixture") != nil {
		t.Fatal("the previewed theme is registered on the site")
	}
	if _, body, _ := get(t, c, base+"/"); strings.Contains(body, "Fixture Journal") || !strings.Contains(body, "Main Site") {
		t.Fatal("the site's front page shows the preview")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/themes"); strings.Contains(body, `"id":"wpgo-fixture"`) {
		t.Fatal("the previewed theme is listed as installed")
	}
	if count(`SELECT count(*) FROM pg_namespace WHERE nspname = 'wpgo_preview_wpgo_fixture'`) != 1 {
		t.Fatal("no sandbox schema")
	}

	// Others: not for non-administrators.
	hash, _ := auth.HashPassword("author pass")
	if _, err := srv.store.CreateUser(ctx, store.NewUser{Login: "writer", Email: "writer@example.test", PasswordHash: hash, Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	before.Users++
	jar, _ := cookiejar.New(nil)
	ed := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _, _ := post(t, ed, base+"/wp-login", url.Values{"log": {"writer"}, "pwd": {"author pass"}}); code != http.StatusFound {
		t.Fatalf("editor login: %d", code)
	}
	for _, p := range []string{"/wp-admin/theme-preview?theme=wpgo-fixture", prefix + "/", prefix + "/about/", "/wp-admin/theme-preview?theme=wpgo-fixture&action=status"} {
		if code, _, _ := get(t, ed, base+p); code != http.StatusForbidden {
			t.Fatalf("editor GET %s: %d", p, code)
		}
	}
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _, loc := get(t, anon, base+prefix+"/"); code != http.StatusFound || !strings.Contains(loc, "/wp-login") {
		t.Fatalf("visitor: %d %s", code, loc)
	}

	// A sandbox kept: a second preview reuses it; a new version of the theme rebuilds it.
	box := srv.previews.get("wpgo-fixture")
	key := box.key
	get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture")
	if srv.previews.get("wpgo-fixture") != box {
		t.Fatal("an unchanged theme was rebuilt")
	}
	editFile(t, filepath.Join(lib, "wpgo-fixture", "theme.json"), map[string]string{`"version": "1.1.0"`: `"version": "1.2.0"`})
	if _, body, _ := get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture"); !strings.Contains(body, "Version: 1.2.0") {
		t.Fatal("preview screen shows the old version")
	}
	waitReady(t, c, base, "wpgo-fixture")
	nb := srv.previews.get("wpgo-fixture")
	if nb == box || nb.key == key {
		t.Fatal("the sandbox was not rebuilt for the new version")
	}
	if _, home, _ := get(t, c, base+prefix+"/blog/"); !strings.Contains(home, "style.css?ver=1.2.0") || !strings.Contains(home, "First light") {
		t.Fatal("the rebuilt preview does not show version 1.2.0 with its demo")
	}
	// A restart: a new manager opens the kept sandbox instead of building it again.
	srv.closePreviews()
	if code, _, _ := get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture"); code != http.StatusOK {
		t.Fatal("preview after close")
	}
	waitReady(t, c, base, "wpgo-fixture")
	if count(`SELECT count(*) FROM wpgo_preview_wpgo_fixture.posts WHERE meta->>'_wpgo_demo' = 'wpgo-fixture'`) != 7 {
		t.Fatal("the reopened sandbox lost its demo content")
	}

	// A theme without demo content previews on a fresh site.
	get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture-two")
	waitReady(t, c, base, "wpgo-fixture-two")
	if code, page, _ := get(t, c, base+"/wp-admin/theme-preview/wpgo-fixture-two/"); code != http.StatusOK || !strings.Contains(page, "Hello world!") ||
		!strings.Contains(page, "/wp-admin/theme-preview/wpgo-fixture-two/wp-content/themes/wpgo-fixture-two/style.css") {
		t.Fatalf("theme without demo: %d", code)
	}

	// Clear theme previews drops every sandbox.
	_, body, _ = get(t, c, base+"/wp-admin/theme-install")
	if !strings.Contains(body, "2 theme previews are kept") {
		t.Fatal("Library does not offer Clear theme previews")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/theme-preview", url.Values{"action": {"clear"}, "_wpnonce": {"x"}}); code != http.StatusForbidden {
		t.Fatal("clear with a bad nonce")
	}
	code, _, loc := post(t, c, base+"/wp-admin/theme-preview", url.Values{"action": {"clear"}, "_wpnonce": {formNonce(t, body[strings.Index(body, "wpgo-clear-previews"):])}})
	if code != http.StatusFound || loc != "/wp-admin/theme-install?previews=cleared" {
		t.Fatalf("clear: %d %s", code, loc)
	}
	if n := count(`SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'wpgo\_preview\_%'`); n != 0 {
		t.Fatalf("%d sandbox schemas left", n)
	}
	if _, err := os.Stat(srv.previews.root()); !os.IsNotExist(err) {
		t.Fatalf("preview directory left: %v", err)
	}
	if after := state(); after != before {
		t.Fatalf("the site changed after clearing:\n%+v\n%+v", before, after)
	}

	// An installed theme with a demo: Themes offers Preview with demo content (built from the installed files).
	_, body, _ = get(t, c, base+"/wp-admin/theme-install")
	install := regexp.MustCompile(`href="(/wp-admin/theme-install\?action=install-theme&amp;theme=wpgo-fixture&amp;_wpnonce=[^"&]+)"`).FindStringSubmatch(body)
	if install == nil {
		t.Fatal("no Install link")
	}
	if code, _, _ := get(t, c, base+html.UnescapeString(install[1])); code != http.StatusFound {
		t.Fatal("install")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/themes"); !strings.Contains(body, `"wpgoDemoPreview":"/wp-admin/theme-preview?theme=wpgo-fixture\u0026source=installed"`) {
		t.Fatal("Themes lacks Preview with demo content")
	}
	if code, body, _ := get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture&source=installed"); code != http.StatusOK ||
		!strings.Contains(body, `class="close-full-overlay" href="/wp-admin/themes?theme=wpgo-fixture"`) || !strings.Contains(body, ">Activate</a>") {
		t.Fatalf("installed preview screen: %d", code)
	}
	waitReady(t, c, base, "wpgo-fixture")
	if _, home, _ := get(t, c, base+prefix+"/blog/"); !strings.Contains(home, "First light") {
		t.Fatal("installed theme preview lacks the demo")
	}
	if srv.activeTheme().Slug != before.Stylesheet || count(`SELECT count(*) FROM posts`) != before.Posts {
		t.Fatal("previewing an installed theme changed the site")
	}
}

// waitReady polls the preview status until the sandbox is ready.
func waitReady(t *testing.T, c *http.Client, base, slug string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		_, body, _ := get(t, c, base+"/wp-admin/theme-preview?theme="+slug+"&action=status")
		var st struct{ State, Error string }
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			t.Fatalf("status: %v %s", err, body)
		}
		switch st.State {
		case "ready":
			return
		case "failed":
			t.Fatalf("preview of %s failed: %s", slug, st.Error)
		case "building":
			// The iframe shows Preparing preview meanwhile.
			if _, page, _ := get(t, c, base+"/wp-admin/theme-preview/"+slug+"/"); !strings.Contains(page, "Preparing preview") && !strings.Contains(page, "<html") {
				t.Fatalf("building preview page: %s", page)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("preview of %s not ready", slug)
}

var previewLinkRe = regexp.MustCompile(`(?:href|src|action)="([^"]*)"`)

// checkPreviewLinks fails when a link of a preview page leaves the preview for the site.
func checkPreviewLinks(t *testing.T, page, base, prefix string) {
	t.Helper()
	for _, m := range previewLinkRe.FindAllStringSubmatch(page, -1) {
		u := html.UnescapeString(m[1])
		switch {
		case strings.HasPrefix(u, base):
			if !strings.HasPrefix(u, base+prefix+"/") && u != base+prefix {
				t.Errorf("link leaves the preview: %s", u)
			}
		case strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//"):
			if !strings.HasPrefix(u, prefix+"/") {
				t.Errorf("root-relative link leaves the preview: %s", u)
			}
		}
	}
}
