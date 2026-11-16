package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/config"
	"wpgo/internal/store"
)

func TestParseAuthorMapping(t *testing.T) {
	m, err := parseAuthorMapping([]byte(`{"alice": "bob", "carol": 7, "dave": null}`))
	if err != nil {
		t.Fatal(err)
	}
	if m["alice"] != "bob" || m["carol"] != "7" || m["dave"] != "" || len(m) != 3 {
		t.Errorf("json mapping: %v", m)
	}
	m, err = parseAuthorMapping([]byte("\xef\xbb\xbfold_user_login,new_user_login\nalice, bob\n\ncarol,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["alice"] != "bob" || m["carol"] != "" || len(m) != 2 {
		t.Errorf("csv mapping: %v", m)
	}
	if _, err := parseAuthorMapping([]byte("a,b,c\n")); err == nil {
		t.Error("three columns accepted")
	}
	if _, err := parseAuthorMapping([]byte(`{"a": [1]}`)); err == nil {
		t.Error("array value accepted")
	}
}

func TestWXRAuthorLogins(t *testing.T) {
	data := []byte(`<?xml version="1.0"?><rss xmlns:wp="http://wordpress.org/export/1.2/"><channel>
<wp:author><wp:author_login>alice</wp:author_login></wp:author>
<wp:author><wp:author_login>bob</wp:author_login></wp:author></channel></rss>`)
	got, err := wxrAuthorLogins(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "alice,bob" {
		t.Errorf("logins = %v", got)
	}
	if _, err := wxrAuthorLogins([]byte("not xml")); err == nil {
		t.Error("non-WXR input accepted")
	}
}

func TestFindMenu(t *testing.T) {
	menus := []*store.Menu{{ID: 3, Slug: "main", Name: "Main Menu"}, {ID: 4, Slug: "3", Name: "Footer"}}
	for ref, want := range map[string]int64{"3": 3, "main": 3, "main menu": 3, "footer": 4} {
		if m := findMenu(menus, ref); m == nil || m.ID != want {
			t.Errorf("findMenu(%q) = %v, want %d", ref, m, want)
		}
	}
	if findMenu(menus, "nope") != nil {
		t.Error("unknown menu found")
	}
}

func TestValidMenuItemURLAndRemote(t *testing.T) {
	for u, want := range map[string]bool{"https://x.test/a": true, "/about": true, "mailto:a@b.c": true, "tel:+1": true,
		"javascript:alert(1)": false, "": false, "ftp://x": false} {
		if got := validMenuItemURL(u); got != want {
			t.Errorf("validMenuItemURL(%q) = %v", u, got)
		}
	}
	if !isRemoteMedia("HTTPS://x.test/a.png") || isRemoteMedia("./http-file.png") {
		t.Error("isRemoteMedia")
	}
}

func TestImportSummary(t *testing.T) {
	if got := importSummary(&importReport{}); got != "nothing new" {
		t.Errorf("empty: %q", got)
	}
	r := &importReport{}
	addImportReport(r, &importReport{Posts: 2, Attachments: 1, Skipped: 3})
	addImportReport(r, &importReport{Posts: 1})
	if got := importSummary(r); got != "3 posts, 1 attachments (3 skipped)" {
		t.Errorf("summary: %q", got)
	}
}

// Every media group command has help and a synopsis without touching the database.
func TestMediaCLIHelp(t *testing.T) {
	for _, path := range []string{"media list", "media get", "media import", "media update", "media delete", "media regenerate",
		"menu list", "menu create", "menu delete", "menu item list", "menu item add-post", "menu item add-term",
		"menu item add-custom", "menu item update", "menu item delete", "menu location list", "menu location assign",
		"menu location remove", "import"} {
		var out, errb bytes.Buffer
		code := CLI(context.Background(), config.Config{}, "pursae", append(strings.Fields(path), "--help"), strings.NewReader(""), &out, &errb)
		if code != 0 || !strings.Contains(out.String(), "pursae "+path) {
			t.Errorf("%s --help: code %d\n%s%s", path, code, out.String(), errb.String())
		}
	}
}

// --- integration (WPGO_TEST_DB) -------------------------------------------------------------

func mediaCLIRun(t *testing.T, cfg config.Config, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := CLI(context.Background(), cfg, "pursae", args, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func mediaCLIMust(t *testing.T, cfg config.Config, args ...string) string {
	t.Helper()
	out, errs, code := mediaCLIRun(t, cfg, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d\n%s%s", args, code, out, errs)
	}
	return out
}

func TestMediaCLIIntegration(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	cliTestInstalled(t, srv)
	ctx := context.Background()
	cfg := srv.cfg
	cfg.DatabaseURL = os.Getenv("WPGO_TEST_DB")
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateUser(ctx, store.NewUser{Login: "admin", Email: "admin@example.test", PasswordHash: hash, Role: "administrator"}); err != nil {
		t.Fatal(err)
	}

	// media import / list / get / update / regenerate / delete
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for x := 0; x < 400; x++ {
		img.Set(x, x%300, color.RGBA{200, 10, 10, 255})
	}
	file := filepath.Join(t.TempDir(), "red-line.png")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	idStr := strings.TrimSpace(mediaCLIMust(t, cfg, "media", "import", file, "--alt=A red line", "--porcelain"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		t.Fatalf("porcelain output %q", idStr)
	}
	a, err := srv.store.GetAttachment(ctx, id)
	if err != nil || a.Alt != "A red line" || a.Title != "red-line" || len(a.Variants) == 0 {
		t.Fatalf("imported attachment: %+v %v", a, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.UploadsDir, filepath.FromSlash(a.FilePath))); err != nil {
		t.Fatalf("file not in uploads: %v", err)
	}
	if out := mediaCLIMust(t, cfg, "media", "list", "--format=ids"); strings.TrimSpace(out) != idStr {
		t.Errorf("media list ids = %q", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(mediaCLIMust(t, cfg, "media", "get", idStr, "--format=json")), &got); err != nil || got["mime_type"] != "image/png" {
		t.Errorf("media get json: %v %v", got, err)
	}
	mediaCLIMust(t, cfg, "media", "update", idStr, "--title=Red", `--meta={"credit":"me"}`)
	if a, _ = srv.store.GetAttachment(ctx, id); a.Title != "Red" || a.Meta["credit"] != "me" {
		t.Errorf("after update: %q %v", a.Title, a.Meta)
	}
	if out := mediaCLIMust(t, cfg, "media", "regenerate", idStr); !strings.Contains(out, "Regenerated 1 of 1") {
		t.Errorf("regenerate: %s", out)
	}
	if _, errs, code := mediaCLIRun(t, cfg, "media", "delete", idStr); code != 1 || !strings.Contains(errs, "--yes") {
		t.Errorf("delete without --yes: %d %s", code, errs)
	}
	mediaCLIMust(t, cfg, "media", "delete", idStr, "--yes")
	if _, err := srv.store.GetAttachment(ctx, id); err == nil {
		t.Error("attachment still there")
	}

	// menus
	menuID := strings.TrimSpace(mediaCLIMust(t, cfg, "menu", "create", "Main Menu", "--porcelain"))
	itemID := strings.TrimSpace(mediaCLIMust(t, cfg, "menu", "item", "add-custom", "main-menu", "Shop", "https://shop.example.test", "--porcelain"))
	if _, errs, code := mediaCLIRun(t, cfg, "menu", "item", "add-custom", "main-menu", "Bad", "javascript:alert(1)"); code == 0 {
		t.Errorf("javascript: URL accepted: %s", errs)
	}
	mediaCLIMust(t, cfg, "menu", "item", "update", itemID, "--title=Store", "--target=_blank")
	out := mediaCLIMust(t, cfg, "menu", "item", "list", menuID, "--format=csv", "--fields=db_id,title,link,position")
	if !strings.Contains(out, itemID+",Store,https://shop.example.test,1") {
		t.Errorf("menu item list: %s", out)
	}
	mediaCLIMust(t, cfg, "menu", "location", "assign", "main-menu", "primary")
	if out := mediaCLIMust(t, cfg, "menu", "location", "list", "--format=csv"); !strings.Contains(out, "primary,Main Menu,"+menuID) {
		t.Errorf("location list: %s", out)
	}
	mediaCLIMust(t, cfg, "menu", "location", "remove", "main-menu", "primary")
	mediaCLIMust(t, cfg, "menu", "item", "delete", itemID, "--yes")
	mediaCLIMust(t, cfg, "menu", "delete", menuID, "--yes")
	if out := mediaCLIMust(t, cfg, "menu", "list", "--format=count"); strings.TrimSpace(out) != "0" {
		t.Errorf("menus left: %s", out)
	}

	// import (WXR)
	wxr := filepath.Join(t.TempDir(), "export.xml")
	if err := os.WriteFile(wxr, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel><wp:wxr_version>1.2</wp:wxr_version>
<wp:author><wp:author_id>2</wp:author_id><wp:author_login>jane</wp:author_login><wp:author_email>jane@example.test</wp:author_email><wp:author_display_name>Jane</wp:author_display_name></wp:author>
<item><title>Imported by CLI</title><dc:creator>jane</dc:creator><content:encoded><![CDATA[<p>Hello</p>]]></content:encoded>
<wp:post_id>90</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>imported-by-cli</wp:post_name><wp:status>publish</wp:status><wp:post_type>post</wp:post_type></item>
</channel></rss>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := mediaCLIMust(t, cfg, "import", wxr, "--authors=create", "--no-fetch-attachments"); !strings.Contains(out, "1 posts") {
		t.Errorf("import: %s", out)
	}
	p, err := srv.store.PostBySlug(ctx, "post", "imported-by-cli")
	if err != nil {
		t.Fatalf("imported post: %v", err)
	}
	jane, err := srv.store.UserByLoginOrEmail(ctx, "jane")
	if err != nil || p.AuthorID != jane.ID {
		t.Errorf("author: post %d, jane %v %v", p.AuthorID, jane, err)
	}
}
