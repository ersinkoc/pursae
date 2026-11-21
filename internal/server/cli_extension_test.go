package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpgo/internal/cli"
	"wpgo/internal/config"
	"wpgo/internal/plugins"
	"wpgo/internal/webbuilder"
)

func TestPluginStatus(t *testing.T) {
	cases := []struct {
		row  pluginRow
		want string
	}{
		{pluginRow{Active: true}, "active"},
		{pluginRow{}, "inactive"},
		{pluginRow{Disabled: true, DisabledWhy: "trap"}, "disabled"},
		{pluginRow{Active: true, Disabled: true}, "disabled"},
	}
	for _, tc := range cases {
		if got := pluginStatus(tc.row); got != tc.want {
			t.Errorf("pluginStatus(%+v) = %q, want %q", tc.row, got, tc.want)
		}
	}
}

func TestPluginCLIRow(t *testing.T) {
	r := pluginRow{Slug: webbuilder.PluginSlug, Name: "Pursae Web Builder", Version: "1.0", Native: true, Active: true, Source: pluginSourceNative,
		Description: "<b>Build</b> pages", Permissions: []permRow{{Name: "options:read"}}}
	row := pluginCLIRow(r)
	if row["name"] != webbuilder.PluginSlug || row["title"] != "Pursae Web Builder" || row["status"] != "active" || row["source"] != "native" {
		t.Fatalf("row: %v", row)
	}
	if row["description"] != "Build pages" {
		t.Errorf("description not stripped: %q", row["description"])
	}
	if p, _ := row["permissions"].([]string); len(p) != 1 || p[0] != "options:read" {
		t.Errorf("permissions: %v", row["permissions"])
	}
	if row["alias"] != "web-builder" {
		t.Errorf("alias: %v", row["alias"])
	}
	if row["disabled"] != false || row["update_available"] != false {
		t.Errorf("defaults missing: %v", row)
	}
}

func TestFindPluginRowAlias(t *testing.T) {
	rows := []pluginRow{{Slug: "classic-editor"}, {Slug: webbuilder.PluginSlug}, {Slug: "my-plugin"}}
	if r, ok := findPluginRow(rows, "web-builder"); !ok || r.Slug != webbuilder.PluginSlug {
		t.Errorf("web-builder alias not resolved: %v %v", r, ok)
	}
	if _, ok := findPluginRow(rows, "nope"); ok {
		t.Error("unknown slug found")
	}
	if err := pluginNotFound(rows, "my-plugi"); err == nil || !strings.Contains(err.Error(), "Did you mean 'my-plugin'") {
		t.Errorf("suggestion: %v", err)
	}
}

func TestGrantedPermissions(t *testing.T) {
	declared := []permRow{{Name: "options:read"}, {Name: "content:read"}}
	got, err := grantedPermissions(declared, nil, false)
	if err != nil || strings.Join(got, ",") != "options:read,content:read" {
		t.Errorf("default: %v %v", got, err)
	}
	if got, err := grantedPermissions(declared, []string{"none"}, true); err != nil || len(got) != 0 {
		t.Errorf("none: %v %v", got, err)
	}
	if got, err := grantedPermissions(declared, []string{"all"}, true); err != nil || len(got) != 2 {
		t.Errorf("all: %v %v", got, err)
	}
	if got, err := grantedPermissions(declared, []string{"content:read", "content:read"}, true); err != nil || strings.Join(got, ",") != "content:read" {
		t.Errorf("subset: %v %v", got, err)
	}
	if _, err := grantedPermissions(declared, []string{"admin:scripts"}, true); err == nil {
		t.Error("undeclared permission granted")
	} else if ue := (*cli.UsageError)(nil); !errors.As(err, &ue) {
		t.Errorf("undeclared permission: want a usage error, got %T %v", err, err)
	}
}

func TestPluginActionResult(t *testing.T) {
	cases := []struct {
		loc    string
		status int
		body   string
		ok     bool
		msg    string
	}{
		{"/wp-admin/plugins?activate=true", http.StatusFound, "", true, ""},
		{"/wp-admin/seogo?page=wizard", http.StatusFound, "", true, ""}, // a setup wizard is a success
		{"/wp-admin/plugins?error=It+requires+foo", http.StatusFound, "", false, "It requires foo"},
		{"", http.StatusForbidden, "<div>The link you followed has expired.</div>", false, "The link you followed has expired."},
		{"", http.StatusInternalServerError, "", false, "could not be activated"},
	}
	for _, tc := range cases {
		err := pluginActionResult(tc.loc, tc.status, tc.body, "activate")
		if (err == nil) != tc.ok {
			t.Errorf("%q %d: err = %v", tc.loc, tc.status, err)
			continue
		}
		if err != nil && !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%q %d: %q does not contain %q", tc.loc, tc.status, err, tc.msg)
		}
	}
	if err := pluginActionResult("", http.StatusInternalServerError, "", "delete"); err == nil || !strings.Contains(err.Error(), "could not be deleted") {
		t.Errorf("delete message: %v", err)
	}
}

func TestIsPackagePath(t *testing.T) {
	exists := func(p string) bool { return p == "local-file" }
	for arg, want := range map[string]bool{
		"-": true, "my-plugin.zip": true, "MY.ZIP": true, "./dist/x": true, `C:\x`: true,
		"hello-dolly": false, "local-file": true,
	} {
		if got := isPackagePath(arg, exists); got != want {
			t.Errorf("isPackagePath(%q) = %v, want %v", arg, got, want)
		}
	}
}

func TestMatchLibraryTheme(t *testing.T) {
	m := &themeManifest{Slug: "aurora", Name: "Aurora", Description: "A calm blog theme", Category: "portfolio", Tags: []string{"dark-mode"}}
	for term, want := range map[string]bool{"": true, "AURORA": true, "blog": true, "portfolio": true, "dark": true, "shop": false} {
		if got := matchLibraryTheme(m, term); got != want {
			t.Errorf("match %q = %v, want %v", term, got, want)
		}
	}
}

func TestStatusSummaries(t *testing.T) {
	out := pluginStatusSummary([]pluginRow{{Slug: "classic-editor", Version: "1.6.7", Native: true, Active: true}, {Slug: "my-plugin", Version: "2.0"}})
	if !strings.Contains(out, "2 installed plugins:") || !strings.Contains(out, "AN classic-editor 1.6.7") || !strings.Contains(out, "I  my-plugin") {
		t.Errorf("plugin summary:\n%s", out)
	}
	d := pluginStatusDetails(pluginRow{Slug: "x", Name: "X", Disabled: true, DisabledWhy: "trap", Source: "upload"})
	if !strings.Contains(d, "Status: Disabled (trap)") || !strings.Contains(d, "Plugin package (upload)") {
		t.Errorf("plugin details:\n%s", d)
	}
	th := themeStatusSummary([]cli.Row{{"name": "twentytwentyfive", "status": "active", "version": "1.0"}, {"name": "aurora", "status": "inactive", "version": "2.0"}})
	if !strings.Contains(th, "A twentytwentyfive 1.0") || !strings.Contains(th, "I aurora") {
		t.Errorf("theme summary:\n%s", th)
	}
}

func TestPluginBatchResult(t *testing.T) {
	if pluginBatchResult("activated", 2, 0) != nil {
		t.Error("no failures should be nil")
	}
	if err := pluginBatchResult("activated", 1, 2); err == nil || err.Error() != "only 1 of 3 plugins activated" {
		t.Errorf("batch: %v", err)
	}
}

func TestExtensionCLIRegistered(t *testing.T) {
	r, closeEnv := NewCLI(extTestConfig(), "pursae", nil)
	defer closeEnv()
	for _, args := range [][]string{
		{"help", "plugin", "activate"}, {"help", "plugin", "uninstall"}, {"help", "plugin", "key", "add"},
		{"help", "theme", "install"}, {"help", "theme", "search"},
	} {
		var out, errb bytes.Buffer
		if code := r.Run(context.Background(), args, cli.IO{Out: &out, Err: &errb}); code != 0 {
			t.Errorf("%v: exit %d, %s", args, code, errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := r.Run(context.Background(), []string{"plugin", "activate", "x", "--bogus"}, cli.IO{Out: &out, Err: &errb}); code != cli.ExitUsage {
		t.Errorf("unknown flag: exit %d", code)
	}
}

// extTestConfig is a config whose database is unreachable unless PURSAE_TEST_DB is set: help never connects.
func extTestConfig() (cfg config.Config) {
	cfg.DatabaseURL = config.Env("TEST_DB")
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "postgres://invalid.invalid:1/none"
	}
	return cfg
}

// TestExtensionCLIDatabase runs the commands against a fresh, installed test site.
func TestExtensionCLIDatabase(t *testing.T) {
	ts, client, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, client)
	cfg := srv.cfg
	cfg.DatabaseURL = config.Env("TEST_DB")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := CLI(context.Background(), cfg, "pursae", args, strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, errOut := run("plugin", "list", "--format=json")
	if code != 0 {
		t.Fatalf("plugin list: %d %s", code, errOut)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) == 0 {
		t.Fatalf("plugin list json: %v %q", err, out)
	}
	if code, _, errOut = run("plugin", "deactivate", "classic-editor"); code != 0 {
		t.Fatalf("plugin deactivate: %d %s", code, errOut)
	}
	if code, out, errOut = run("plugin", "get", "classic-editor", "--field=status"); code != 0 || strings.TrimSpace(out) != "inactive" {
		t.Fatalf("plugin get: %d %q %s", code, out, errOut)
	}
	if code, _, errOut = run("plugin", "activate", "classic-editor"); code != 0 {
		t.Fatalf("plugin activate: %d %s", code, errOut)
	}
	if err := srv.settings.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !srv.settings.Bool("native_classic_editor") {
		t.Error("activate did not switch the built-in plugin on")
	}
	if code, out, _ = run("plugin", "list", "--status=active", "--format=ids"); code != 0 || !strings.Contains(out, "classic-editor") {
		t.Errorf("active ids: %d %q", code, out)
	}
	if code, _, errOut = run("plugin", "deactivate", "classic-editor"); code != 0 {
		t.Fatalf("plugin deactivate: %d %s", code, errOut)
	}
	if code, _, errOut = run("plugin", "delete", "classic-editor", "--yes"); code != 1 || !strings.Contains(errOut, "built-in") {
		t.Errorf("deleting a built-in plugin: %d %s", code, errOut)
	}
	if code, _, _ = run("plugin", "get", "no-such-plugin"); code != 1 {
		t.Errorf("unknown plugin: exit %d", code)
	}

	// Themes.
	if code, out, errOut = run("theme", "list", "--status=active", "--field=name"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("theme list: %d %q %s", code, out, errOut)
	}
	active := strings.TrimSpace(out)
	code, out, _ = run("theme", "list", "--format=ids")
	var other string
	for _, slug := range strings.Fields(out) {
		if slug != active {
			other = slug
			break
		}
	}
	if code != 0 || other == "" {
		t.Fatalf("theme ids: %d %q", code, out)
	}
	if code, _, errOut = run("theme", "delete", active, "--yes"); code != 1 {
		t.Errorf("deleting the active theme: %d %s", code, errOut)
	}
	if code, _, errOut = run("theme", "activate", other); code != 0 {
		t.Fatalf("theme activate: %d %s", code, errOut)
	}
	if err := srv.settings.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.settings.String("stylesheet") != other {
		t.Errorf("stylesheet = %q, want %q", srv.settings.String("stylesheet"), other)
	}
	if code, _, errOut = run("theme", "activate", active); code != 0 {
		t.Fatalf("theme activate back: %d %s", code, errOut)
	}
	// Without --yes and without a terminal, destructive commands refuse.
	if code, _, errOut = run("theme", "delete", other); code != 1 || !strings.Contains(errOut, "--yes") {
		t.Errorf("delete without --yes: %d %s", code, errOut)
	}
	if code, out, _ = run("plugin", "key", "list", "--format=count"); code != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("key list: %d %q", code, out)
	}
	if code, out, errOut = run("theme", "search", "--format=count"); code != 0 {
		t.Errorf("theme search: %d %q %s", code, out, errOut)
	}
}

// TestExtensionCLIPackages installs, activates, deactivates and deletes a Wasm plugin package and
// installs and deletes a theme package through the CLI.
func TestExtensionCLIPackages(t *testing.T) {
	ts, client, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, client)
	ctx := context.Background()
	zip := demoPackage(t)
	srv.cfg.AllowUnsignedPlugins = true
	if err := srv.settings.Set(ctx, map[string]any{"plugin_allow_unsigned": true}); err != nil {
		t.Fatal(err)
	}
	cfg := srv.cfg
	cfg.DatabaseURL = config.Env("TEST_DB")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := CLI(ctx, cfg, "pursae", args, strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}
	file := filepath.Join(t.TempDir(), "demo.zip")
	if err := os.WriteFile(file, zip, 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := plugins.LoadPackage(bytes.NewReader(zip), int64(len(zip)), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	slug := pkg.Manifest.Slug
	status := func() string {
		var st string
		_ = srv.store.Pool.QueryRow(ctx, `SELECT status FROM plugins WHERE slug = $1`, slug).Scan(&st)
		return st
	}
	if code, _, errOut := run("plugin", "install", file, "--activate"); code != 0 {
		t.Fatalf("install --activate: %d %s", code, errOut)
	}
	if st := status(); st != "active" {
		t.Fatalf("status after install --activate = %q", st)
	}
	if code, out, _ := run("plugin", "get", slug, "--field=status"); code != 0 || strings.TrimSpace(out) != "active" {
		t.Errorf("plugin get: %d %q", code, out)
	}
	if code, _, errOut := run("plugin", "delete", slug, "--yes"); code != 1 || !strings.Contains(errOut, "active") {
		t.Errorf("deleting an active plugin: %d %s", code, errOut)
	}
	if code, _, errOut := run("plugin", "deactivate", slug); code != 0 || status() != "inactive" {
		t.Fatalf("deactivate: %d %s (%s)", code, errOut, status())
	}
	if code, _, errOut := run("plugin", "activate", slug, "--grant=none"); code != 0 || status() != "active" {
		t.Fatalf("activate --grant=none: %d %s", code, errOut)
	}
	var perms string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT permissions::text FROM plugins WHERE slug = $1`, slug).Scan(&perms)
	if perms != "[]" && perms != "null" { // pluginAction stores an empty grant as null, like the consent screen
		t.Errorf("--grant=none granted %s", perms)
	}
	if code, _, errOut := run("plugin", "delete", slug, "--deactivate", "--yes"); code != 0 {
		t.Fatalf("delete --deactivate: %d %s", code, errOut)
	}
	if st := status(); st != "" {
		t.Errorf("row left after delete: %q", st)
	}
	if _, err := os.Stat(filepath.Join(srv.plugins.dir, slug+".zip")); !os.IsNotExist(err) {
		t.Errorf("package file left after delete: %v", err)
	}

	// A theme package.
	themeZip, _ := fixtureThemePackage(t, "cli-fixture", "CLI Fixture", "1.0.0", "blog")
	tfile := filepath.Join(t.TempDir(), "cli-fixture.zip")
	if err := os.WriteFile(tfile, themeZip, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run("theme", "install", tfile); code != 0 {
		t.Fatalf("theme install: %d %s", code, errOut)
	}
	if code, out, _ := run("theme", "get", "cli-fixture", "--field=source"); code != 0 || strings.TrimSpace(out) != "upload" {
		t.Errorf("theme get: %d %q", code, out)
	}
	if code, _, errOut := run("theme", "install", tfile); code != 1 || !strings.Contains(errOut, "not newer") {
		t.Errorf("reinstalling the same version: %d %s", code, errOut)
	}
	if code, _, errOut := run("theme", "delete", "cli-fixture", "--yes"); code != 0 {
		t.Fatalf("theme delete: %d %s", code, errOut)
	}
	if code, _, _ := run("theme", "get", "cli-fixture"); code != 1 {
		t.Errorf("deleted theme still found: %d", code)
	}
}
