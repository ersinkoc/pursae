package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestBmgrCategoriesAndPaths(t *testing.T) {
	for table, want := range map[string]string{"posts": "content", "media": "media", "users": "users", "settings": "settings", "goo_orders": "commerce",
		"gwe_global_classes": "builder", "hg_entries": "headless", "pll_languages": "multilang", "redirects": "seo", "seogo_404": "seo",
		"adminsec_log": "security", "audit_log": "security", "maintenance_subscribers": "other", "plugins": "plugins"} {
		if got := bmgrCategoryOf(table); got != want {
			t.Errorf("bmgrCategoryOf(%q) = %q, want %q", table, got, want)
		}
	}
	for _, rel := range []string{"../x", "/etc/passwd", "a/../../b", "", "a\\b", "a//b"} {
		if _, err := bmgrSafeJoin("/data", rel); err == nil {
			t.Errorf("bmgrSafeJoin accepted %q", rel)
		}
	}
	if p, err := bmgrSafeJoin("/data", "2026/05/a.jpg"); err != nil || p != filepath.Join("/data", "2026/05/a.jpg") {
		t.Errorf("bmgrSafeJoin: %q %v", p, err)
	}
	s := &Server{}
	for _, name := range []string{"../a.zip", "a/b.zip", "a.txt", ".zip", "a b.zip", ""} {
		if bmgrNameRe.MatchString(name) {
			t.Errorf("archive name %q accepted", name)
		}
	}
	_ = s
}

func bmgrRun(t *testing.T, s *Server, kind string, f func(j *bmgrJob) error) {
	t.Helper()
	j, err := s.bmgrStart(kind)
	if err != nil {
		t.Fatal(err)
	}
	err = f(j)
	s.bmgrFinish(j, err)
	if err != nil {
		t.Fatalf("%s: %v\n%s", kind, err, strings.Join(s.bmgrJobByID(j.ID).Log, "\n"))
	}
}

func allCats() []string {
	var out []string
	for _, c := range bmgrCategories {
		out = append(out, c.Key)
	}
	return out
}

func TestBmgrRoundTrip(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.bmgrPluginAction(ctx, "activate"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WPGO_BACKUPS_DIR", t.TempDir())
	pool := srv.store.Pool

	// The screens render for an administrator.
	for _, tab := range []string{"create", "backups", "import"} {
		code, body, _ := get(t, c, ts.URL+"/wp-admin/admin?page=backup-manager&tab="+tab)
		if code != http.StatusOK || !strings.Contains(body, "Backup Manager") {
			t.Fatalf("tab %s: %d", tab, code)
		}
		if tab == "create" && !strings.Contains(body, "Media library") {
			t.Fatal("the Create tab lists no categories")
		}
	}

	// State to protect: a file, an option with the site address inside, the posts.
	uploads := srv.cfg.UploadsDir
	if err := os.MkdirAll(filepath.Join(uploads, "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	photo := filepath.Join(uploads, "2026", "photo.txt")
	if err := os.WriteFile(photo, []byte("original bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldURL := bmgrSiteURL(srv)
	if err := srv.settings.Set(ctx, map[string]any{"bm_probe": map[string]any{"link": oldURL + "/about/"}, "blogname": "Original name"}); err != nil {
		t.Fatal(err)
	}
	var posts, users int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posts`).Scan(&posts)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users)
	if posts == 0 || users == 0 {
		t.Fatalf("the fresh site has no content to test with: %d posts, %d users", posts, users)
	}
	var secret string
	secret = srv.settings.String("auth_secret")

	var archive string
	bmgrRun(t, srv, "backup", func(j *bmgrJob) (err error) {
		archive, err = srv.bmgrRunBackup(ctx, j, bmgrSelection{Categories: allCats(), UserMode: "all", Note: "round trip"})
		return err
	})
	m, err := bmgrReadManifest(filepath.Join(srv.bmgrDir(), archive))
	if err != nil || m.SiteURL != oldURL || m.Note != "round trip" {
		t.Fatalf("manifest: %+v %v", m, err)
	}
	// Sessions never travel; the migrations table neither.
	for _, tb := range m.Tables {
		if bmgrNever[tb.Name] {
			t.Errorf("table %s is in the archive", tb.Name)
		}
	}

	// Wreck the site, change its address, restore with the address rewritten.
	newURL := "http://moved.example"
	if _, err := pool.Exec(ctx, `DELETE FROM posts`); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(photo)
	if err := srv.settings.Set(ctx, map[string]any{"blogname": "Wrecked", "home": newURL, "siteurl": newURL}); err != nil {
		t.Fatal(err)
	}
	bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
		return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: archive, Categories: allCats(), RewriteURL: true, Safety: true, Me: 1})
	})
	// Restoring the users replaces the sessions, as the screen warns: the old session is gone, the backup's login works.
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/admin?page=backup-manager&tab=backups"); code != http.StatusFound {
		t.Errorf("the session survived a restore of the users: %d", code)
	}
	if code, _, _ := post(t, c, ts.URL+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login with the restored account: %d", code)
	}
	var gotPosts, gotUsers int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posts`).Scan(&gotPosts)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&gotUsers)
	if gotPosts != posts || gotUsers != users {
		t.Fatalf("restored %d posts, %d users; want %d, %d", gotPosts, gotUsers, posts, users)
	}
	if b, err := os.ReadFile(photo); err != nil || string(b) != "original bytes" {
		t.Fatalf("the uploaded file did not come back: %q %v", b, err)
	}
	if got := srv.settings.String("blogname"); got != "Original name" {
		t.Errorf("blogname = %q", got)
	}
	if got := srv.settings.String("home"); got != newURL {
		t.Errorf("home = %q: this site's address must win", got)
	}
	if got := srv.settings.String("auth_secret"); got != secret {
		t.Error("this site's secret was replaced by the archive's")
	}
	if !srv.bmgrOn() {
		t.Error("Backup Manager switched itself off")
	}
	probe, _ := srv.settings.Get("bm_probe").(map[string]any)
	if probe["link"] != newURL+"/about/" {
		t.Errorf("the old address was not rewritten: %v", probe)
	}
	var seq int64
	if err := pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('posts', 'id'))`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	var maxID int64
	_ = pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM posts`).Scan(&maxID)
	if seq <= maxID {
		t.Errorf("the posts sequence (%d) is behind the restored ids (%d)", seq, maxID)
	}
	var safety int
	for _, e := range srv.bmgrList() {
		if e.M != nil && e.M.Kind == "safety" {
			safety++
		}
	}
	if safety != 1 {
		t.Errorf("%d safety snapshots, want 1", safety)
	}
	// Foreign keys are back to immediate.
	var deferrable int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND condeferrable`).Scan(&deferrable)
	if deferrable != 0 {
		t.Errorf("%d foreign keys stayed deferrable", deferrable)
	}

	// A partial backup (one role's users only) restores content without users and keeps the admin's session.
	var partial string
	bmgrRun(t, srv, "backup", func(j *bmgrJob) (err error) {
		partial, err = srv.bmgrRunBackup(ctx, j, bmgrSelection{Categories: []string{"content", "users"}, UserMode: "roles", Roles: []string{"subscriber"}})
		return err
	})
	pm, _ := bmgrReadManifest(filepath.Join(srv.bmgrDir(), partial))
	if pm == nil || pm.UserMode != "roles" || pm.Users != 0 {
		t.Errorf("partial manifest: %+v", pm)
	}
	bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
		return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: partial, Categories: []string{"content"}, Me: 1})
	})
	var authorless int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE author_id IS NOT NULL AND author_id NOT IN (SELECT id FROM users)`).Scan(&authorless)
	if authorless != 0 {
		t.Errorf("%d posts point at users that do not exist", authorless)
	}

	// Download, then upload through the HTTP screens.
	_, body, _ := get(t, c, ts.URL+"/wp-admin/admin?page=backup-manager&tab=backups")
	nonce := formNonceOrLink(t, body)
	resp, err := c.Get(ts.URL + "/wp-admin/admin?page=backup-manager&action=download&file=" + url.QueryEscape(archive) + "&_wpnonce=" + nonce)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(data) < 100 || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download: %d, %d bytes, %q", resp.StatusCode, len(data), resp.Header.Get("Cache-Control"))
	}
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/admin?page=backup-manager&action=download&file="+url.QueryEscape("../"+archive)+"&_wpnonce="+nonce); code != http.StatusNotFound {
		t.Errorf("a path outside the backups folder was served: %d", code)
	}
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/admin?page=backup-manager&action=download&file="+url.QueryEscape(archive)); code != http.StatusForbidden {
		t.Errorf("download without a nonce: %d", code)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("archive", "site.zip")
	_, _ = fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/wp-admin/admin?page=backup-manager&action=upload&_wpnonce="+nonce, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	noRedirect := *c
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	up, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	up.Body.Close()
	if loc := up.Header.Get("Location"); up.StatusCode != http.StatusFound || !strings.Contains(loc, "tab=restore") {
		t.Fatalf("upload: %d %s", up.StatusCode, loc)
	}
	// A file that is not a backup is refused.
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("archive", "x.zip")
	zb := &bytes.Buffer{}
	zw := zip.NewWriter(zb)
	w, _ := zw.Create("readme.txt")
	_, _ = w.Write([]byte("hi"))
	zw.Close()
	_, _ = fw.Write(zb.Bytes())
	mw.Close()
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/wp-admin/admin?page=backup-manager&action=upload&_wpnonce="+nonce, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	up, err = noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	up.Body.Close()
	if loc := up.Header.Get("Location"); !strings.Contains(loc, "bmgr-error") {
		t.Errorf("a non-backup zip was accepted: %s", loc)
	}
}

// formNonceOrLink pulls the Backup Manager nonce out of a download link on the Backups screen.
func formNonceOrLink(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`action=download[^"]*?_wpnonce=([A-Za-z0-9]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no download link on the Backups screen")
	}
	return m[1]
}

// A partial restore must not wipe links from tables outside the restored set: ON DELETE SET NULL fires during
// the restore's DELETE even on a deferred key, so restoring Users alone used to null every post author,
// Media alone every featured image, and Content alone every attachment's parent post.
func TestBmgrPartialRestoreKeepsLinks(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.bmgrPluginAction(ctx, "activate"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WPGO_BACKUPS_DIR", t.TempDir())
	pool := srv.store.Pool

	p := &store.Post{Type: "post", Status: "publish", Title: "Linked", Slug: "linked", AuthorID: 1, Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	a := &store.Attachment{UploaderID: 1, PostID: p.ID, Slug: "pic", FilePath: "2026/11/pic.png", Mime: "image/png", Title: "Pic"}
	if err := srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE posts SET featured_media_id = $1 WHERE id = $2`, a.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	var archive string
	bmgrRun(t, srv, "backup", func(j *bmgrJob) (err error) {
		archive, err = srv.bmgrRunBackup(ctx, j, bmgrSelection{Categories: allCats(), UserMode: "all"})
		return err
	})
	check := func(after string) {
		t.Helper()
		var author, featured, uploader, parent *int64
		if err := pool.QueryRow(ctx, `SELECT p.author_id, p.featured_media_id, m.uploader_id, m.post_id FROM posts p, media m WHERE p.id = $1 AND m.id = $2`,
			p.ID, a.ID).Scan(&author, &featured, &uploader, &parent); err != nil {
			t.Fatalf("after %s: %v", after, err)
		}
		if author == nil || *author != 1 || featured == nil || *featured != a.ID || uploader == nil || *uploader != 1 || parent == nil || *parent != p.ID {
			t.Errorf("after restoring %s: author %v, featured %v, uploader %v, parent %v", after, author, featured, uploader, parent)
		}
	}
	for _, cat := range []string{"users", "media", "content"} {
		bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
			return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: archive, Categories: []string{cat}, Me: 1})
		})
		check(cat)
	}
	// The lifted keys are back in their original form.
	var def string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'media_post_fk'`).Scan(&def); err != nil ||
		!strings.Contains(def, "ON DELETE SET NULL") || strings.Contains(def, "DEFERRABLE") {
		t.Errorf("media_post_fk after the restores: %q %v", def, err)
	}
}

// An archive from 0.1.7 keeps each menu's location in menus.location and has no menu_locations table:
// restoring it maps the column into menu_locations instead of emptying the locations.
func TestBmgrRestoreLegacyMenuLocations(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.bmgrPluginAction(ctx, "activate"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WPGO_BACKUPS_DIR", t.TempDir())
	pool := srv.store.Pool
	mainID, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	footID, err := srv.store.CreateMenu(ctx, "Foot")
	if err != nil {
		t.Fatal(err)
	}
	var archive string
	bmgrRun(t, srv, "backup", func(j *bmgrJob) (err error) {
		archive, err = srv.bmgrRunBackup(ctx, j, bmgrSelection{Categories: []string{"content"}, UserMode: "all"})
		return err
	})

	// Rewrite the archive the way 0.1.7 wrote it: menus carry a location column, no menu_locations.
	legacy := map[int64]string{mainID: "primary", footID: "footer"}
	src, err := zip.OpenReader(filepath.Join(srv.bmgrDir(), archive))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range src.File {
		if f.Name == "database/menu_locations.jsonl" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		switch f.Name {
		case "manifest.json":
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			m["schema"], m["version"] = "0036_golylang_translate", "0.1.7"
			var tables []any
			for _, raw := range m["tables"].([]any) {
				tb := raw.(map[string]any)
				switch tb["name"] {
				case "menu_locations":
					continue
				case "menus":
					tb["columns"] = append(tb["columns"].([]any), "location")
				}
				tables = append(tables, tb)
			}
			m["tables"] = tables
			data, _ = json.Marshal(m)
		case "database/menus.jsonl":
			var out []string
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var row map[string]any
				if err := json.Unmarshal([]byte(line), &row); err != nil {
					t.Fatal(err)
				}
				row["location"] = legacy[int64(row["id"].(float64))]
				b, _ := json.Marshal(row)
				out = append(out, string(b))
			}
			data = []byte(strings.Join(out, "\n") + "\n")
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	}
	src.Close()
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	old := "legacy-017.zip"
	if err := os.WriteFile(filepath.Join(srv.bmgrDir(), old), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// The site lost its locations since; the old archive brings them back.
	if _, err := pool.Exec(ctx, `DELETE FROM menu_locations`); err != nil {
		t.Fatal(err)
	}
	bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
		return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: old, Categories: []string{"content"}, Me: 1})
	})
	got := map[string]int64{}
	rows, err := pool.Query(ctx, `SELECT location, menu_id FROM menu_locations`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var l string
		var id int64
		_ = rows.Scan(&l, &id)
		got[l] = id
	}
	rows.Close()
	if len(got) != 2 || got["primary"] != mainID || got["footer"] != footID {
		t.Errorf("menu locations after restoring a 0.1.7 archive: %v, want primary=%d footer=%d", got, mainID, footID)
	}
}
