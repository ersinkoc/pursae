package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wpgo/internal/config"
)

func TestOptionCoerce(t *testing.T) {
	cases := []struct {
		key, raw string
		json     bool
		cur      any
		exists   bool
		want     any
		err      bool
	}{
		{"posts_per_page", "5", false, nil, false, 5, false},
		{"posts_per_page", "five", false, nil, false, nil, true},
		{"users_can_register", "yes", false, nil, false, true, false},
		{"users_can_register", "maybe", false, nil, false, nil, true},
		{"gmt_offset", "3.5", false, nil, false, 3.5, false},
		{"blogname", "42", false, nil, false, "42", false},
		{"my_option", "hello", false, nil, false, "hello", false},
		{"my_option", "12", false, nil, false, "12", false},
		{"my_option", `{"a":1}`, false, nil, false, map[string]any{"a": 1.0}, false},
		{"my_option", "12", false, 3.0, true, int64(12), false},
		{"my_option", "true", false, false, true, true, false},
		{"my_option", `[1,2]`, true, nil, false, []any{1.0, 2.0}, false},
		{"my_option", `{bad`, true, nil, false, nil, true},
	}
	for _, tc := range cases {
		got, err := optionCoerce(tc.key, tc.raw, tc.json, tc.cur, tc.exists)
		if (err != nil) != tc.err {
			t.Errorf("%s=%q: err %v", tc.key, tc.raw, err)
			continue
		}
		if !tc.err && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s=%q: got %#v, want %#v", tc.key, tc.raw, got, tc.want)
		}
	}
}

func TestOptionNormalize(t *testing.T) {
	ok := []struct {
		key  string
		in   any
		want map[string]any
	}{
		{"home", "https://example.com/", map[string]any{"home": "https://example.com"}},
		{"timezone_string", "Europe/Istanbul", map[string]any{"timezone_string": "Europe/Istanbul"}},
		{"timezone_string", "UTC+3", map[string]any{"timezone_string": "", "gmt_offset": 3.0}},
		{"timezone_string", "UTC-5.5", map[string]any{"timezone_string": "", "gmt_offset": -5.5}},
		{"posts_per_page", 0, map[string]any{"posts_per_page": 1}},
		{"start_of_week", -2, map[string]any{"start_of_week": 0}},
		{"permalink_structure", "%postname%/", map[string]any{"permalink_structure": "/%postname%/"}},
		{"permalink_structure", "/blog/", map[string]any{"permalink_structure": ""}},
		{"category_base", "/topics/ ", map[string]any{"category_base": "topics"}},
		{"image_output_format", "webp", map[string]any{"image_output_format": "webp"}},
		{"WPLANG", "en_US", map[string]any{"WPLANG": ""}},
		{"show_on_front", "page", map[string]any{"show_on_front": "page"}},
	}
	for _, tc := range ok {
		got, err := optionNormalize(tc.key, tc.in)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s=%v: got %v, %v; want %v", tc.key, tc.in, got, err, tc.want)
		}
	}
	bad := []struct {
		key string
		in  any
	}{
		{"siteurl", "ftp://example.com"}, {"home", "example.com"}, {"timezone_string", "Mars/Olympus"},
		{"timezone_string", "UTC+x"}, {"image_output_format", "gif"}, {"show_on_front", "blog"},
		{"default_comment_status", "maybe"}, {"admin_email", "nope"}, {"WPLANG", "xx_YY"},
	}
	for _, tc := range bad {
		if _, err := optionNormalize(tc.key, tc.in); err == nil {
			t.Errorf("%s=%v: no error", tc.key, tc.in)
		}
	}
}

func TestOptionNeedsRestart(t *testing.T) {
	for k, want := range map[string]bool{"wpgo_cache_serve": true, "wpgo_opt_lazy": true, "native_seogo": true, "WPLANG": true, "blogname": false} {
		if got := optionNeedsRestart(k); got != want {
			t.Errorf("%s: %v", k, got)
		}
	}
}

func TestOptGlob(t *testing.T) {
	if got := optGlobToLike("wpgo_cache_*"); got != `wpgo\_cache\_%` {
		t.Errorf("like: %q", got)
	}
	if got := optGlobToLike("a?%"); got != `a_\%` {
		t.Errorf("like: %q", got)
	}
	for _, tc := range []struct {
		g, n string
		want bool
	}{
		{"wpgo_*", "wpgo_cache", true}, {"*cache*", "wpgo_cache_serve", true}, {"blog?ame", "blogname", true},
		{"blog", "blogname", false}, {"", "x", true}, {"*", "", true}, {"a*b", "ab", true}, {"a*b", "abc", false},
	} {
		if got := optGlobMatch(tc.g, tc.n); got != tc.want {
			t.Errorf("%q ~ %q = %v", tc.g, tc.n, got)
		}
	}
}

func TestJSONPath(t *testing.T) {
	v := map[string]any{"a": map[string]any{"b": 1.0}, "list": []any{"x", "y"}}
	if got, err := jsonPathGet(v, []string{"a", "b"}); err != nil || got != 1.0 {
		t.Errorf("get a.b: %v %v", got, err)
	}
	if got, err := jsonPathGet(v, []string{"list", "1"}); err != nil || got != "y" {
		t.Errorf("get list.1: %v %v", got, err)
	}
	if _, err := jsonPathGet(v, []string{"a", "zz"}); err == nil {
		t.Error("missing key: no error")
	}
	if _, err := jsonPathPatch(v, "insert", []string{"a", "b"}, 2.0); err == nil {
		t.Error("insert over an existing key: no error")
	}
	if _, err := jsonPathPatch(v, "update", []string{"a", "c"}, 2.0); err == nil {
		t.Error("update of a missing key: no error")
	}
	out, err := jsonPathPatch(v, "update", []string{"a", "b"}, 2.0)
	if err != nil {
		t.Fatal(err)
	}
	out, err = jsonPathPatch(out, "insert", []string{"list", "2"}, "z")
	if err != nil {
		t.Fatal(err)
	}
	out, err = jsonPathPatch(out, "delete", []string{"list", "0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": map[string]any{"b": 2.0}, "list": []any{"y", "z"}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("patched: %v", out)
	}
	if got, err := patchValue("false", false, true); err != nil || got != false {
		t.Errorf("patchValue bool: %v %v", got, err)
	}
	if got, err := patchValue("7", false, 1.0); err != nil || got != 7.0 {
		t.Errorf("patchValue number: %v %v", got, err)
	}
	if got, _ := patchValue("7", false, nil); got != "7" {
		t.Errorf("patchValue new: %v", got)
	}
}

func TestPgJSONEscape(t *testing.T) {
	if got := pgJSONEscape("http://a.b/\"x\"\n\\é"); got != `http://a.b/\"x\"\n\\é` {
		t.Errorf("got %q", got)
	}
}

func TestSrSelectAndSQL(t *testing.T) {
	cols := []srColumn{{"posts", "content", "text"}, {"posts", "slug", "text"}, {"posts", "blocks", "jsonb"}, {"terms", "name", "text"}, {"settings", "value", "jsonb"}}
	got := srSelect(cols, []string{"posts"}, nil, []string{"slug"}, nil)
	if len(got) != 2 || got[0].Column != "content" || got[1].Column != "blocks" {
		t.Errorf("select: %v", got)
	}
	got = srSelect(cols, nil, []string{"set*"}, nil, []string{"posts.content", "name"})
	if len(got) != 2 || got[1].Table != "terms" {
		t.Errorf("include: %v", got)
	}
	count, update := srSQL(srColumn{"settings", "value", "jsonb"}, "key NOT IN ('x')")
	if !strings.Contains(count, `strpos("value"::text, $1) > 0 AND key NOT IN ('x')`) || !strings.Contains(update, `SET "value" = (replace("value"::text, $1, $2))::jsonb`) {
		t.Errorf("sql:\n%s\n%s", count, update)
	}
}

func TestCacheHelpers(t *testing.T) {
	if got, _ := cacheAbsURL("https://ex.com/", "/about/?a=1"); got != "https://ex.com/about/?a=1" {
		t.Errorf("abs: %q", got)
	}
	if got, _ := cacheAbsURL("https://ex.com", "https://other.com/x"); got != "https://other.com/x" {
		t.Errorf("abs keep: %q", got)
	}
	if _, err := cacheAbsURL("", "/x"); err == nil {
		t.Error("no home: no error")
	}
	uploads := filepath.Join(t.TempDir(), "uploads")
	dir := cacheDiskDir(uploads)
	if filepath.Base(dir) != "wpgo-cache" || filepath.Dir(dir) != filepath.Dir(uploads) {
		t.Fatalf("dir %q", dir)
	}
	if n, err := cacheDiskClear(dir); n != 0 || err != nil {
		t.Errorf("missing dir: %d %v", n, err)
	}
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{"a.wpc", "b.wpc", ".tmp-1", "keep.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte("xx"), 0o644)
	}
	if files, size, _ := cacheDiskStats(dir); files != 2 || size != 4 {
		t.Errorf("stats %d %d", files, size)
	}
	if n, _ := cacheDiskClear(dir); n != 3 {
		t.Errorf("cleared %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Error("removed an unrelated file")
	}
}

func TestCLICacheFlushReportsDiskError(t *testing.T) {
	ts, client, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, client)
	cfg := srv.cfg
	cfg.DatabaseURL = config.Env("TEST_DB")
	cfg.UploadsDir = filepath.Join(t.TempDir(), "uploads")
	if err := os.WriteFile(cacheDiskDir(cfg.UploadsDir), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errs, code := mediaCLIRun(t, cfg, "cache", "flush")
	if code != 1 || !strings.Contains(errs, "disk cache") || strings.Contains(out+errs, "page cache was flushed") {
		t.Fatalf("cache flush: exit=%d stdout=%q stderr=%q", code, out, errs)
	}
}

func TestRewriteRows(t *testing.T) {
	rows := rewriteRows("https://ex.com", "/%year%/%postname%/", "topics", "", time.UTC)
	byKind := map[string]string{}
	for _, r := range rows {
		byKind[r["kind"].(string)] = r["example"].(string)
	}
	if byKind["post"] != "https://ex.com/2026/sample-post/" || byKind["category"] != "https://ex.com/topics/news/" || byKind["tag"] != "https://ex.com/tag/go/" {
		t.Errorf("pretty: %v", byKind)
	}
	rows = rewriteRows("https://ex.com", "", "", "", time.UTC)
	if ex := rows[0]["example"].(string); ex != "https://ex.com/?p=123" {
		t.Errorf("plain: %q", ex)
	}
}

// TestOptionCLI runs the option group's commands against a test database (PURSAE_TEST_DB).
func TestOptionCLI(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	cliTestInstalled(t, srv)
	ctx := context.Background()
	cfg := config.Config{DatabaseURL: config.Env("TEST_DB"), UploadsDir: t.TempDir(), SiteURL: "http://example.test"}
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := CLI(ctx, cfg, "pursae", args, strings.NewReader(stdin), &out, &errb)
		return code, out.String(), errb.String()
	}

	if code, out, _ := run("", "option", "update", "posts_per_page", "7"); code != 0 || !strings.Contains(out, "Updated") {
		t.Fatalf("update: %d %s", code, out)
	}
	if code, out, _ := run("", "option", "get", "posts_per_page"); code != 0 || strings.TrimSpace(out) != "7" {
		t.Errorf("get: %d %q", code, out)
	}
	if code, _, _ := run("", "option", "add", "posts_per_page", "8"); code != 1 {
		t.Errorf("add over an existing option: %d", code)
	}
	if code, _, _ := run("", "option", "update", "home", "not-a-url"); code != 1 {
		t.Errorf("invalid home accepted: %d", code)
	}
	if code, _, _ := run("", "option", "add", "cli_test_obj", `{"a":{"b":1}}`, "--format=json"); code != 0 {
		t.Fatalf("add json: %d", code)
	}
	if code, _, _ := run("", "option", "patch", "update", "cli_test_obj", "a", "b", "2"); code != 0 {
		t.Errorf("patch: %d", code)
	}
	if code, out, _ := run("", "option", "pluck", "cli_test_obj", "a", "b"); code != 0 || strings.TrimSpace(out) != "2" {
		t.Errorf("pluck: %d %q", code, out)
	}
	if code, out, _ := run("", "option", "list", "--search=cli_test_*", "--format=ids"); code != 0 || strings.TrimSpace(out) != "cli_test_obj" {
		t.Errorf("list: %d %q", code, out)
	}
	if code, _, errs := run("", "option", "delete", "auth_secret", "--yes"); code != 1 || !strings.Contains(errs, "refusing") {
		t.Errorf("delete auth_secret: %d %s", code, errs)
	}
	if code, _, _ := run("", "option", "delete", "cli_test_obj"); code != 1 {
		t.Errorf("delete without --yes: %d", code)
	}
	if code, _, _ := run("", "option", "delete", "cli_test_obj", "--yes"); code != 0 {
		t.Errorf("delete: %d", code)
	}
	if code, _, _ := run("", "option", "get", "cli_test_obj"); code != 1 {
		t.Errorf("deleted option still readable: %d", code)
	}

	// transients
	if code, _, _ := run("", "transient", "set", "cli_t", "hello", "60"); code != 0 {
		t.Errorf("transient set: %d", code)
	}
	if code, out, _ := run("", "transient", "get", "cli_t"); code != 0 || strings.TrimSpace(out) != "hello" {
		t.Errorf("transient get: %d %q", code, out)
	}
	if code, _, _ := run("", "transient", "delete", "cli_t"); code != 0 {
		t.Errorf("transient delete: %d", code)
	}

	// search-replace
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('cli_sr', '{"u":"http://old.test/x"}')`); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run("", "search-replace", "http://old.test", "https://new.test", "settings", "--dry-run", "--format=count"); code != 0 || out != "1\n" {
		t.Errorf("dry run: %d %q", code, out)
	}
	if code, _, _ := run("", "search-replace", "http://old.test", "https://new.test", "settings", "--yes"); code != 0 {
		t.Errorf("search-replace: %d", code)
	}
	var u string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT value->>'u' FROM settings WHERE key = 'cli_sr'`).Scan(&u); err != nil || u != "https://new.test/x" {
		t.Errorf("replaced: %q %v", u, err)
	}

	// rewrite + maintenance
	if code, _, _ := run("", "rewrite", "structure", "%postname%"); code != 0 {
		t.Errorf("rewrite structure: %d", code)
	}
	if code, out, _ := run("", "option", "get", "permalink_structure"); code != 0 || strings.TrimSpace(out) != "/%postname%" {
		t.Errorf("structure stored: %q", out)
	}
	if code, _, _ := run("", "maintenance-mode", "activate"); code != 1 {
		t.Errorf("activate with the plugin off: %d", code)
	}
	if code, _, _ := run("", "maintenance-mode", "activate", "--activate-plugin"); code != 0 {
		t.Errorf("activate: %d", code)
	}
	if code, _, _ := run("", "maintenance-mode", "deactivate", "--no-notify"); code != 0 {
		t.Errorf("deactivate: %d", code)
	}
	if code, _, _ := run("", "maintenance-mode", "is-active"); code != 1 {
		t.Errorf("is-active after deactivate: %d", code)
	}
}
