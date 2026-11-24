package server

import (
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSingleMediaDeletionPreservesBytesOnDatabaseRejection(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "serve-from-s3": {"1"}})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.srv.store.Pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`SELECT setval(pg_get_serial_sequence('media','id'), greatest((SELECT max(id) FROM posts),(SELECT max(id) FROM media))+10)`)
	const contents = "media attached to a reference"
	uploaded := s.upload("referenced-delete.txt", []byte(contents))
	id := int64(uploaded["id"].(float64))
	rel := gocdnRelOfURL(uploaded["source_url"].(string))
	item := s.srv.gocdnItemByPath(t.Context(), rel)
	if item == nil {
		t.Fatal("missing offload mapping")
	}
	exec(`CREATE TABLE keep_media (id bigint REFERENCES media(id))`)
	exec(`INSERT INTO keep_media VALUES ($1)`, id)
	_, list, _ := get(t, s.c, s.ts.URL+"/wp-admin/upload?mode=list")
	link := regexp.MustCompile(`href="(/wp-admin/post\?action=delete&amp;post=` + itoa64(id) + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(list)
	if len(link) != 2 {
		t.Fatal("attachment delete link missing")
	}
	target, err := url.Parse(html.UnescapeString(link[1]))
	if err != nil {
		t.Fatal(err)
	}
	code, _, loc := get(t, s.c, s.ts.URL+target.String())
	if code != http.StatusFound || !strings.Contains(loc, "delete-failed=1") {
		t.Fatalf("library deletion: %d %q", code, loc)
	}
	_, screen, _ := get(t, s.c, s.ts.URL+loc)
	if !strings.Contains(screen, "A media file could not be deleted.") {
		t.Fatal("library failure notice missing")
	}
	code, body, _ := post(t, s.c, s.ts.URL+"/wp-admin/admin-ajax", url.Values{
		"action": {"delete-post"}, "id": {itoa64(id)}, "_wpnonce": {target.Query().Get("_wpnonce")},
	})
	if code != http.StatusOK || strings.TrimSpace(body) != "0" {
		t.Fatalf("modal deletion: %d %q", code, body)
	}
	b, err := os.ReadFile(s.srv.gocdnLocalPath(rel))
	remote := s.fake.Object(item.Bucket, item.Key)
	if err != nil || string(b) != contents || remote == nil || string(remote.Body) != contents {
		t.Fatalf("failed deletion lost attachment bytes: %q %v", b, err)
	}
}

func TestLocalMediaCleanupResumesInterruptedAndBlockedDeletes(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	localFile := func(name string) (int64, string) {
		t.Helper()
		a, err := srv.insertAttachmentFile(t.Context(), name, strings.NewReader("local media"), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return a.ID, a.FilePath
	}
	first, firstPath := localFile("in-flight-delete.txt")
	second, secondPath := localFile("stopped-delete.txt")
	for _, id := range []int64{first, second} {
		if _, err := srv.detachOffloadsAndDeleteAttachment(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := srv.store.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	var held string
	if err := tx.QueryRow(t.Context(), `SELECT path FROM media_file_deletes WHERE path=$1 FOR UPDATE`, firstPath).Scan(&held); err != nil {
		t.Fatal(err)
	}
	srv.gocdnHousekeepingOnce(t.Context())
	if _, err := os.Stat(srv.gocdnLocalPath(firstPath)); err != nil {
		t.Fatalf("already processing: %v", err)
	}
	if _, err := os.Stat(srv.gocdnLocalPath(secondPath)); !os.IsNotExist(err) {
		t.Fatalf("maintenance missed a stopped deletion: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	blockedID, blockedPath := localFile("blocked-delete.txt")
	a, err := srv.store.GetAttachment(t.Context(), blockedID)
	if err != nil {
		t.Fatal(err)
	}
	local := srv.gocdnLocalPath(blockedPath)
	moved := filepath.Join(t.TempDir(), "media")
	if err := os.Rename(local, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.deleteAttachmentRecordAndFiles(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(local); err != nil || !info.IsDir() {
		t.Fatal("a media cleanup should leave directory obstructions intact")
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, local); err != nil {
		t.Fatal(err)
	}
	srv.gocdnHousekeepingOnce(t.Context())
	for _, rel := range []string{firstPath, blockedPath} {
		if _, err := os.Stat(srv.gocdnLocalPath(rel)); !os.IsNotExist(err) {
			t.Fatalf("retry did not clear %s: %v", rel, err)
		}
	}
}
