package server

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWBImportWizardUndoCanRetryFinalization(t *testing.T) {
	for _, step := range []struct {
		name, block, unblock string
	}{
		{"ownership lookup", `ALTER TABLE posts RENAME COLUMN title TO undo_hidden_title`, `ALTER TABLE posts RENAME COLUMN undo_hidden_title TO title`},
		{"URL claims", `ALTER TABLE settings ADD CONSTRAINT undo_claim_guard CHECK (key <> 'wb_site_claims' OR value ? 'imported-route')`, `ALTER TABLE settings DROP CONSTRAINT undo_claim_guard`},
		{"homepage", `ALTER TABLE settings ADD CONSTRAINT undo_home_guard CHECK (key <> 'show_on_front' OR value = '"page"'::jsonb)`, `ALTER TABLE settings DROP CONSTRAINT undo_home_guard`},
		{"staging directory", "", ""},
		{"undo record", `ALTER TABLE settings ADD CONSTRAINT undo_record_guard CHECK (key <> 'wb_site_imports' OR value <> '{}'::jsonb)`, `ALTER TABLE settings DROP CONSTRAINT undo_record_guard`},
	} {
		t.Run(step.name, func(t *testing.T) {
			srv, _, _, w := wizStart(t)
			post := wbrPost(t, srv, "page", "Imported page", "publish", 1)
			id, kit := wbWizNewID(), "finalization-kit"
			rec := &wbWizRecord{ID: id, Kit: kit, Pages: []int64{post.ID}, FrontSet: true, PrevShow: "posts"}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := srv.store.Pool.Exec(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`UPDATE posts SET meta = jsonb_build_object($2::text, $3::text) WHERE id = $1`, post.ID, wbSiteMetaKit, kit)
			if err := srv.settings.Set(t.Context(), map[string]any{
				wbWizImportsOption: map[string]*wbWizRecord{id: rec},
				"show_on_front":    "page", "page_on_front": post.ID,
			}); err != nil {
				t.Fatal(err)
			}
			if err := srv.wbSiteSetClaims(t.Context(), kit, map[string]int64{"imported-route": post.ID}); err != nil {
				t.Fatal(err)
			}
			staged := srv.wbWizDir(id)
			if err := os.MkdirAll(staged, 0o700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(staged, "site.css")
			if err := os.WriteFile(file, []byte("body{}"), 0o600); err != nil {
				t.Fatal(err)
			}

			if step.block != "" {
				exec(step.block)
			} else {
				t.Cleanup(func() { _ = os.Chmod(staged, 0o700) })
				if err := os.Chmod(staged, 0o500); err != nil {
					t.Skipf("cannot set directory permissions: %v", err)
				}
				probe, err := os.OpenFile(filepath.Join(staged, "permission-check"), os.O_CREATE|os.O_WRONLY, 0o600)
				if err == nil {
					_ = probe.Close()
					t.Skip("directory permissions do not restrict this process")
				}
				if !errors.Is(err, fs.ErrPermission) {
					t.Fatal(err)
				}
			}
			undo := func() string {
				t.Helper()
				target := wbWizURL + "?action=undo&import=" + id
				code, form, _ := w.get(target)
				nonce := wizHiddenRE.FindStringSubmatch(form)
				if code != http.StatusOK || len(nonce) != 2 {
					t.Fatalf("undo form: %d %s", code, wbSnippet(form, "notice"))
				}
				code, body, _ := w.postForm(target, url.Values{"_wpnonce": {nonce[1]}, "confirm": {"1"}})
				if code != http.StatusOK {
					t.Fatalf("undo: %d %s", code, wbSnippet(body, "notice"))
				}
				return body
			}
			if body := undo(); !strings.Contains(body, "Retry the removal") || srv.wbWizRecords()[id] == nil {
				t.Fatalf("undo lost its retry: %s", wbSnippet(body, "notice"))
			}
			if step.unblock != "" {
				exec(step.unblock)
			} else if err := os.Chmod(staged, 0o700); err != nil {
				t.Fatal(err)
			}
			if body := undo(); !strings.Contains(body, "pages and templates") || srv.wbWizRecords()[id] != nil {
				t.Fatalf("retry did not finish: %s", wbSnippet(body, "notice"))
			}
			if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staging after retry: %v", err)
			}
			if n := wizCount(t, srv, `SELECT count(*) FROM posts WHERE id = $1`, post.ID); n != 0 {
				t.Fatal("retry left the owned page")
			}
			if srv.wbSiteClaims()["imported-route"].ID != 0 {
				t.Fatal("retry left the URL claim")
			}
			if srv.settings.String("show_on_front") != "posts" || srv.settings.Int("page_on_front") != 0 {
				t.Fatal("retry did not restore the homepage")
			}
		})
	}
}
