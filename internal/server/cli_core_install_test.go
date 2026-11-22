package server

import (
	"context"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/config"
)

// TestCoreInstallCLI installs a fresh (migrated, not installed) site with `core install`, like the
// install screen does, and checks is-installed and the refusals.
func TestCoreInstallCLI(t *testing.T) {
	_, _, srv := newTestServer(t, false) // a migrated, empty database
	cfg := config.Config{DatabaseURL: config.Env("TEST_DB"), UploadsDir: srv.cfg.UploadsDir, WebDir: srv.cfg.WebDir, HubURL: "off", SiteURL: "http://default.test"}
	ctx := context.Background()

	if code, _, _ := runOpsCLI(t, cfg, "core", "is-installed"); code != 1 {
		t.Fatalf("is-installed before install: %d", code)
	}
	// Before the install, writing siteurl (the install marker) or any content is refused.
	for _, early := range [][]string{{"option", "update", "siteurl", "https://early.example.com"}, {"post", "list"}} {
		if code, _, errs := runOpsCLI(t, cfg, early...); code != 1 || !strings.Contains(errs, "Pursae is not installed; run 'pursae core install'") {
			t.Fatalf("%v before install: %d %q", early, code, errs)
		}
	}
	if code, out, errs := runOpsCLI(t, cfg, "option", "get", "blogname"); code == 2 || strings.Contains(errs, "not installed") {
		t.Fatalf("option get before install: %d %q %q", code, out, errs)
	}
	for _, bad := range [][]string{
		{"core", "install", "--admin_email=a@example.com"},                                         // no user
		{"core", "install", "--admin_user=bad*name", "--admin_email=a@example.com"},                // invalid login
		{"core", "install", "--admin_user=admin", "--admin_email=nope"},                            // invalid e-mail
		{"core", "install", "--admin_user=admin", "--admin_email=a@example.com", "--url=ftp://x"},  // invalid URL
		{"core", "install", "--admin_user=admin", "--admin_email=a@example.com", "--locale=xx_YY"}, // invalid locale
	} {
		if code, _, errs := runOpsCLI(t, cfg, bad...); code != 2 {
			t.Fatalf("%v: code %d %q", bad, code, errs)
		}
	}
	code, out, errs := runOpsCLI(t, cfg, "core", "install", "--url=https://cli.example.com/", "--title=CLI Site",
		"--admin_user=owner", "--admin_email=owner@example.com", "--discourage-search")
	if code != 0 || !strings.Contains(out, "Success: Pursae installed at https://cli.example.com.") {
		t.Fatalf("core install: %d %q %q", code, out, errs)
	}
	first, _, _ := strings.Cut(out, "\n")
	pw, ok := strings.CutPrefix(first, "Admin password: ")
	if !ok || pw == "" {
		t.Fatalf("generated password not printed: %q", out)
	}
	var siteurl, home, blogname string
	var public bool
	if err := srv.store.Pool.QueryRow(ctx, `SELECT (SELECT value#>>'{}' FROM settings WHERE key='siteurl'), (SELECT value#>>'{}' FROM settings WHERE key='home'),
		(SELECT value#>>'{}' FROM settings WHERE key='blogname'), (SELECT (value#>>'{}')::bool FROM settings WHERE key='blog_public')`).Scan(&siteurl, &home, &blogname, &public); err != nil {
		t.Fatal(err)
	}
	if siteurl != "https://cli.example.com" || home != siteurl || blogname != "CLI Site" || public {
		t.Fatalf("settings: %q %q %q public=%v", siteurl, home, blogname, public)
	}
	u, err := srv.store.UserByLoginOrEmail(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := auth.CheckPassword(pw, u.PasswordHash); !ok {
		t.Fatal("the printed password does not match the stored hash")
	}
	var posts int
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE slug IN ('hello-world', 'sample-page', 'privacy-policy')`).Scan(&posts); err != nil || posts != 3 {
		t.Fatalf("default content: %d %v", posts, err)
	}
	if code, _, _ := runOpsCLI(t, cfg, "core", "is-installed"); code != 0 {
		t.Fatalf("is-installed after install: %d", code)
	}
	// A second install is a no-op that says so.
	if code, out, errs := runOpsCLI(t, cfg, "core", "install", "--admin_user=other", "--admin_email=o@example.com"); code != 0 || !strings.Contains(out+errs, "already installed") {
		t.Fatalf("second install: %d %q %q", code, out, errs)
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "other"); err == nil {
		t.Fatal("a second install created a user")
	}
}

// cliTestInstalled marks a test site installed (the siteurl setting, siteInstalled) so the
// command line runs on it, for tests that create their users directly instead of installing.
func cliTestInstalled(t *testing.T, srv *Server) {
	t.Helper()
	if err := srv.settings.Set(context.Background(), map[string]any{"siteurl": srv.cfg.SiteURL, "home": srv.cfg.SiteURL}); err != nil {
		t.Fatal(err)
	}
}
