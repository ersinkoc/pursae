package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// An edited bundled file records the hash of the original it was based on; a later original
// (a core update) flags it, restoring or re-saving clears it, and legacy edits are not flagged.
func TestThemeSafetyOverlayBaseMismatch(t *testing.T) {
	dir := t.TempDir()
	base := fstest.MapFS{
		"themes/demo/assets/style.css":      {Data: []byte("body{}")},
		"themes/demo/templates/header.html": {Data: []byte("<header>")},
		"themes/demo/templates/footer.html": {Data: []byte("<footer>")},
	}
	o := overlayFS{base: base, dir: dir}
	edit := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, "demo", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("assets/style.css", "body{color:red}")
	edit("templates/header.html", "<header class=mine>")
	edit("templates/footer.html", "<footer mine>") // a legacy edit: no recorded base
	for _, p := range []string{"themes/demo/assets/style.css", "themes/demo/templates/header.html"} {
		if err := recordThemeEditBase(o, p, true); err != nil {
			t.Fatal(err)
		}
	}
	if got := staleThemeEdits(o, "demo"); len(got) != 0 {
		t.Fatalf("nothing changed yet, got %+v", got)
	}

	// A core update ships new originals of all three files.
	base["themes/demo/assets/style.css"] = &fstest.MapFile{Data: []byte("body{margin:0}")}
	base["themes/demo/templates/header.html"] = &fstest.MapFile{Data: []byte("<header v2>")}
	base["themes/demo/templates/footer.html"] = &fstest.MapFile{Data: []byte("<footer v2>")}
	got := staleThemeEdits(o, "demo")
	if len(got) != 2 || got[0].File != "style.css" || got[1].File != "templates/header.html" {
		t.Fatalf("stale edits %+v", got)
	}

	// Saving again records the new base; restoring drops the entry.
	if err := recordThemeEditBase(o, "themes/demo/assets/style.css", true); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, "demo", "templates", "header.html"))
	if err := recordThemeEditBase(o, "themes/demo/templates/header.html", false); err != nil {
		t.Fatal(err)
	}
	if got := staleThemeEdits(o, "demo"); len(got) != 0 {
		t.Fatalf("after save/restore %+v", got)
	}
	if m := readThemeEditBase(dir, "demo"); len(m) != 1 {
		t.Fatalf("base manifest %v", m)
	}

	// An original that is no longer shipped is reported as removed.
	delete(base, "themes/demo/assets/style.css")
	if got := staleThemeEdits(o, "demo"); len(got) != 1 || !got[0].Removed {
		t.Fatalf("removed original %+v", got)
	}
}

// Updating a package backs up the files edited on the server before the folder is replaced.
func TestThemeSafetyBackupEditedFiles(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "starter")
	for rel, content := range map[string]string{"assets/style.css": "edited css", "templates/index.html": "edited tpl", "assets/other.css": "untouched"} {
		p := filepath.Join(pkgDir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := &themePackage{Dir: pkgDir}
	pkg.loadEdited()
	if bak, _, err := backupEditedThemeFiles(dir, pkg, "starter", "1.0.0"); err != nil || bak != "" {
		t.Fatalf("nothing edited: %q %v", bak, err)
	}
	if err := pkg.markEdited("style.css"); err != nil {
		t.Fatal(err)
	}
	if err := pkg.markEdited(themeTemplateEditPrefix + "templates/index.html"); err != nil {
		t.Fatal(err)
	}
	pkg.loadEdited()
	bak, files, err := backupEditedThemeFiles(dir, pkg, "starter", "1.0.0")
	if err != nil || len(files) != 2 || !strings.HasPrefix(bak, filepath.Join(dir, themeBackupDir, "starter-1.0.0-")) {
		t.Fatalf("backup %q %v %v", bak, files, err)
	}
	for rel, want := range map[string]string{"assets/style.css": "edited css", "templates/index.html": "edited tpl"} {
		if b, err := os.ReadFile(filepath.Join(bak, filepath.FromSlash(rel))); err != nil || string(b) != want {
			t.Fatalf("backup of %s: %q %v", rel, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bak, "assets", "other.css")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an unedited file was backed up")
	}
}

// End to end: a package that stops loading shows a notice (and keeps the stylesheet option); an
// update of a package with edited files backs them up and says so.
func TestThemeSafetyNoticesAndUpdateBackup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PURSAE_THEMES_DIR", dir)
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Safety"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	t.Cleanup(func() { unregisterTheme("starter") })

	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)
	if _, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", "starter.zip", zipDir(t, starterDir)); loc != "/wp-admin/themes?installed=starter" {
		t.Fatalf("upload: %q %s", loc, body)
	}

	// An edited asset and template, then version 1.0.1 is uploaded.
	th := themeBySlug("starter")
	if err := os.WriteFile(filepath.Join(dir, "starter", "assets", "style.css"), []byte("/* mine */"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := th.Package.markEdited("style.css"); err != nil {
		t.Fatal(err)
	}
	newer := map[string][]byte{}
	_ = filepath.WalkDir(starterDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(starterDir, p)
			newer[filepath.ToSlash(rel)], _ = os.ReadFile(p)
		}
		return err
	})
	newer["theme.json"] = []byte(starterManifest(t, map[string]string{`"version": "1.0.0"`: `"version": "1.0.1"`}))
	_, body, _ = get(t, c, base+"/wp-admin/theme-install?browse=upload")
	if _, body, loc := postZip(t, c, base+"/wp-admin/theme-install", formNonce(t, body), "themezip", "starter.zip", zipFiles(t, newer)); !strings.HasSuffix(loc, "=starter") {
		t.Fatalf("update: %q %s", loc, body)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, themeBackupDir, "starter-1.0.0-*", "assets", "style.css"))
	if len(matches) != 1 {
		t.Fatalf("no backup: %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != "/* mine */" {
		t.Fatalf("backup content %q", b)
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes?updated=starter")
	if !strings.Contains(body, "replaced files you had edited on this server") || !strings.Contains(body, "<code>assets/style.css</code>") {
		t.Fatal("themes screen lacks the backup notice")
	}

	// Activate it, then make it fail validation (as a stricter validator would) and reload the packages.
	if err := srv.switchTheme(context.Background(), "starter"); err != nil {
		t.Fatal(err)
	}
	m := starterManifest(t, map[string]string{`"theme_url": "https:`: `"theme_url": "ftp:`})
	if err := os.WriteFile(filepath.Join(dir, "starter", "theme.json"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.loadThemePackages()
	if srv.activeTheme().Slug != defaultTheme || srv.settings.String("stylesheet") != "starter" {
		t.Fatalf("fallback: active %s, stylesheet %s", srv.activeTheme().Slug, srv.settings.String("stylesheet"))
	}
	for _, page := range []string{"/wp-admin/themes", "/wp-admin/"} {
		_, body, _ := get(t, c, base+page)
		if !strings.Contains(body, "Your active theme Starter could not be loaded:") || !strings.Contains(body, "must be an http(s) URL") {
			t.Fatalf("%s lacks the load failure notice", page)
		}
	}

	// Fixed again: it loads, the notice goes and the site is back on it.
	if err := os.WriteFile(filepath.Join(dir, "starter", "theme.json"), newer["theme.json"], 0o644); err != nil {
		t.Fatal(err)
	}
	srv.loadThemePackages()
	if srv.activeTheme().Slug != "starter" {
		t.Fatalf("not back: %s", srv.activeTheme().Slug)
	}
	if _, body, _ := get(t, c, base+"/wp-admin/themes"); strings.Contains(body, "could not be loaded") {
		t.Fatal("notice still shown")
	}
}
