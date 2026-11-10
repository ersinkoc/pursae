package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpgo/internal/config"
)

// The archive's sealed values are re-encrypted with the derivations the plugins use themselves.
func TestBmgrResealMatchesPlugins(t *testing.T) {
	a := &Server{cfg: config.Config{Secret: "secret-of-site-a"}}
	b := &Server{cfg: config.Config{Secret: "secret-of-site-b"}}
	sealed := map[string]string{
		"smtp": a.msSeal("smtp-pass"),
		"cdn":  a.gocdnSeal("cdn-secret"),
		"mt":   a.pllSealMTKey("mt-key"),
		"totp": a.asSeal("JBSWY3DPEHPK3PXP"),
	}
	raw, _ := json.Marshal(map[string]any{"key": "x", "value": sealed, "plain": "enc:v1:not-sealed"})
	r := &bmgrResealer{from: "secret-of-site-a", to: "secret-of-site-b"}
	var got struct {
		Value map[string]string `json:"value"`
		Plain string            `json:"plain"`
	}
	if err := json.Unmarshal([]byte(r.line(string(raw))), &got); err != nil {
		t.Fatal(err)
	}
	if r.n != 4 {
		t.Errorf("re-encrypted %d values, want 4", r.n)
	}
	if v := b.msReveal(got.Value["smtp"]); v != "smtp-pass" {
		t.Errorf("smtp: %q", v)
	}
	if v := b.gocdnReveal(got.Value["cdn"]); v != "cdn-secret" {
		t.Errorf("cdn: %q", v)
	}
	if v := b.pllRevealMTKey(got.Value["mt"]); v != "mt-key" {
		t.Errorf("mt: %q", v)
	}
	if v := b.asReveal(got.Value["totp"]); v != "JBSWY3DPEHPK3PXP" {
		t.Errorf("totp: %q", v)
	}
	if got.Plain != "enc:v1:not-sealed" {
		t.Errorf("a value that does not open was changed: %q", got.Plain)
	}
	// Another secret than the archive's: left as it is.
	other := &bmgrResealer{from: "wrong", to: "secret-of-site-b"}
	if line := other.line(string(raw)); line != string(raw) || other.n != 0 {
		t.Errorf("a value sealed under another secret was rewritten")
	}
}

// bmgrRewriteArchive copies an archive with edit applied to each file's bytes.
func bmgrRewriteArchive(t *testing.T, dir, from, to string, edit func(name string, data []byte) []byte) {
	t.Helper()
	src, err := zip.OpenReader(filepath.Join(dir, from))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range src.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(edit(f.Name, data))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, to), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// An archive of a site with logins and e-mails that differ only by case (made before migration 0048,
// which then left the columns non-unique) restores on a site that has the unique indexes; an archive
// without such accounts keeps them unique.
func TestBmgrRestoreCaseDuplicateUsers(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.bmgrPluginAction(ctx, "activate"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WPGO_BACKUPS_DIR", t.TempDir())
	pool := srv.store.Pool
	index := func(name string) bool {
		var ok bool
		_ = pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&ok)
		return ok
	}
	if !index("users_lower_login_key") || !index("users_lower_email_key") {
		t.Fatal("the test database lacks the 0048 unique indexes")
	}
	var archive string
	bmgrRun(t, srv, "backup", func(j *bmgrJob) (err error) {
		archive, err = srv.bmgrRunBackup(ctx, j, bmgrSelection{Categories: []string{"users"}, UserMode: "all"})
		return err
	})
	// A plain restore keeps the indexes unique.
	bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
		return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: archive, Categories: []string{"users"}, Me: 1})
	})
	if !index("users_lower_login_key") || !index("users_lower_email_key") || index("users_lower_login") {
		t.Errorf("after a plain restore: login_key %v email_key %v plain login %v", index("users_lower_login_key"), index("users_lower_email_key"), index("users_lower_login"))
	}

	dup := "case-dups.zip"
	bmgrRewriteArchive(t, srv.bmgrDir(), archive, dup, func(name string, data []byte) []byte {
		if name != "database/users.jsonl" {
			return data
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &row); err != nil {
			t.Fatal(err)
		}
		row["id"], row["login"], row["email"], row["nicename"] = 9001, strings.ToUpper(row["login"].(string)), strings.ToUpper(row["email"].(string)), "admin-upper"
		b, _ := json.Marshal(row)
		return append(data, append(b, '\n')...)
	})
	bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
		return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: dup, Categories: []string{"users"}, Me: 1})
	})
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(login) = 'admin'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("restored accounts named admin: %d %v", n, err)
	}
	if index("users_lower_login_key") || index("users_lower_email_key") || !index("users_lower_login") || !index("users_lower_email") {
		t.Errorf("after restoring case duplicates: login_key %v email_key %v plain login %v plain email %v",
			index("users_lower_login_key"), index("users_lower_email_key"), index("users_lower_login"), index("users_lower_email"))
	}
	// Restoring the clean archive again makes them unique again.
	bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
		return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: archive, Categories: []string{"users"}, Me: 1})
	})
	if !index("users_lower_login_key") || !index("users_lower_email_key") {
		t.Errorf("the unique indexes did not come back after a clean restore")
	}
}

// Secrets sealed on the archive's site open on this one after a restore: a site restored elsewhere keeps
// its mail password and its users' authenticator apps.
func TestBmgrRestoreResealsSecrets(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.bmgrPluginAction(ctx, "activate"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WPGO_BACKUPS_DIR", t.TempDir())
	if srv.cfg.Secret != "" {
		t.Skip("WPGO_SECRET is set for the tests")
	}
	if err := srv.settings.Set(ctx, map[string]any{"r2_sealed_probe": srv.msSeal("smtp-pass")}); err != nil {
		t.Fatal(err)
	}
	var archive string
	bmgrRun(t, srv, "backup", func(j *bmgrJob) (err error) {
		archive, err = srv.bmgrRunBackup(ctx, j, bmgrSelection{Categories: []string{"settings"}, UserMode: "all"})
		return err
	})
	// This site gets a new secret (another server, a reinstall): the archive's value no longer opens.
	if err := srv.settings.Set(ctx, map[string]any{"auth_secret": "a-brand-new-secret-for-this-site"}); err != nil {
		t.Fatal(err)
	}
	if v := srv.msReveal(srv.settings.String("r2_sealed_probe")); v != "" {
		t.Fatalf("sealed value opened under another secret: %q", v)
	}
	bmgrRun(t, srv, "restore", func(j *bmgrJob) error {
		return srv.bmgrRunRestore(ctx, j, bmgrRestoreReq{File: archive, Categories: []string{"settings"}, Me: 1})
	})
	if got := srv.settings.String("auth_secret"); got != "a-brand-new-secret-for-this-site" {
		t.Errorf("auth_secret after the restore: %q", got)
	}
	if v := srv.msReveal(srv.settings.String("r2_sealed_probe")); v != "smtp-pass" {
		t.Errorf("sealed value after the restore: %q", v)
	}
}
