package server

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"wpgo/internal/caps"
	"wpgo/internal/config"
	"wpgo/internal/store"
)

func TestCLIUserRolesAfter(t *testing.T) {
	cases := []struct {
		op, role string
		cur      []string
		want     []string
		changed  bool
		err      bool
	}{
		{"set", "editor", []string{"author", "contributor"}, []string{"editor"}, true, false},
		{"set", "editor", []string{"editor"}, []string{"editor"}, false, false},
		{"add", "editor", []string{"author"}, []string{"author", "editor"}, true, false},
		{"add", "author", []string{"author"}, []string{"author"}, false, false},
		{"add", "author", nil, []string{"author"}, true, false},
		{"remove", "author", []string{"author", "editor"}, []string{"editor"}, true, false},
		{"remove", "", []string{"author", "editor"}, []string{}, true, false},
		{"remove", "", nil, []string{}, false, false},
		{"remove", "admin", []string{"author"}, nil, false, true},
	}
	for _, tc := range cases {
		got, changed, err := cliUserRolesAfter(tc.op, tc.cur, tc.role)
		if (err != nil) != tc.err {
			t.Errorf("%s %q on %v: err %v", tc.op, tc.role, tc.cur, err)
			continue
		}
		if tc.err {
			continue
		}
		if !slices.Equal(got, tc.want) || changed != tc.changed {
			t.Errorf("%s %q on %v = %v, %v; want %v, %v", tc.op, tc.role, tc.cur, got, changed, tc.want, tc.changed)
		}
		if tc.op == "remove" && got == nil {
			t.Errorf("remove must give a non-nil list (nil means delete for removingLastAdmin)")
		}
	}
	// The input is never modified.
	cur := []string{"author", "editor"}
	_, _, _ = cliUserRolesAfter("remove", cur, "author")
	if !slices.Equal(cur, []string{"author", "editor"}) {
		t.Errorf("input changed: %v", cur)
	}
}

func TestCLIUserCapsAfter(t *testing.T) {
	exc := map[string]string{"upload_files": "allow", "publish_posts": "deny"}
	next, changed := cliUserCapsAfter("add", exc, []string{"upload_files", "edit_theme_options", " "})
	if next["edit_theme_options"] != "allow" || !slices.Equal(changed, []string{"edit_theme_options"}) {
		t.Errorf("add: %v %v", next, changed)
	}
	if len(exc) != 2 {
		t.Error("input modified")
	}
	next, changed = cliUserCapsAfter("deny", exc, []string{"upload_files"})
	if next["upload_files"] != "deny" || len(changed) != 1 {
		t.Errorf("deny: %v %v", next, changed)
	}
	next, changed = cliUserCapsAfter("remove", exc, []string{"publish_posts", "nope"})
	if _, ok := next["publish_posts"]; ok || !slices.Equal(changed, []string{"publish_posts"}) {
		t.Errorf("remove: %v %v", next, changed)
	}
}

func TestCLIUserMetaValue(t *testing.T) {
	if b, err := cliUserMetaValue(`{"a": 1}`, "plaintext"); err != nil || string(b) != `"{\"a\": 1}"` {
		t.Errorf("plaintext: %s %v", b, err)
	}
	if b, err := cliUserMetaValue(`{"a": 1}`, "json"); err != nil || string(b) != `{"a":1}` {
		t.Errorf("json: %s %v", b, err)
	}
	if b, err := cliUserMetaValue(`false`, "json"); err != nil || string(b) != `false` {
		t.Errorf("json bool: %s %v", b, err)
	}
	if _, err := cliUserMetaValue(`{`, "json"); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestCLIUserPasswordProblem(t *testing.T) {
	if cliUserPasswordProblem("") == "" || cliUserPasswordProblem(`a\b`) == "" || cliUserPasswordProblem("good-pass") != "" {
		t.Error("password checks")
	}
	if p := cliUserGeneratePassword(); len(p) != 24 || cliUserPasswordProblem(p) != "" {
		t.Errorf("generated password %q", p)
	}
}

func TestCLIUserCapRows(t *testing.T) {
	u := &store.User{Caps: caps.Grants{"read": true, "upload_files": true, "edit_posts": false}}
	roleCaps := map[string]bool{"read": true, "edit_posts": true}
	exc := map[string]string{"upload_files": "allow", "edit_posts": "deny"}
	all := cliUserCapRows("all", u, roleCaps, exc)
	if len(all) != 2 || all[0]["name"] != "read" || all[0]["source"] != "role" || all[1]["name"] != "upload_files" || all[1]["source"] != "user" {
		t.Errorf("all: %v", all)
	}
	if rows := cliUserCapRows("role", u, roleCaps, exc); len(rows) != 2 {
		t.Errorf("role: %v", rows)
	}
	if rows := cliUserCapRows("user", u, roleCaps, exc); len(rows) != 2 || rows[0]["name"] != "edit_posts" || rows[0]["effect"] != "deny" {
		t.Errorf("user: %v", rows)
	}
}

func TestCLIUserHelp(t *testing.T) {
	r, closeEnv := NewCLI(config.Config{}, "pursae", nil)
	defer closeEnv()
	for _, p := range []string{"user list", "user create", "user delete", "user reset-password", "user set-role", "user add-cap",
		"user list-caps", "user meta update", "user session destroy", "user application-password create", "role list", "cap list"} {
		if r.Lookup(p) == nil {
			t.Errorf("%s is not registered", p)
		}
	}
	var out, errb bytes.Buffer
	if code := CLI(context.Background(), config.Config{}, "pursae", []string{"help", "user", "create"}, strings.NewReader(""), &out, &errb); code != 0 ||
		!strings.Contains(out.String(), "user create") {
		t.Errorf("help user create: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := CLI(context.Background(), config.Config{}, "pursae", []string{"user", "session", "destroy"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("missing argument: exit %d", code)
	}
}

// TestCLIUserIntegration runs the user commands against a fresh database (PURSAE_TEST_DB).
func TestCLIUserIntegration(t *testing.T) {
	_, _, srv := newTestServerSetup(t, false, "")
	cliTestInstalled(t, srv)
	cfg := srv.cfg
	cfg.DatabaseURL = config.Env("TEST_DB")
	ctx := context.Background()
	run := func(want int, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := CLI(ctx, cfg, "pursae", args, strings.NewReader(""), &out, &errb)
		if code != want {
			t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out.String(), errb.String())
		}
		return strings.TrimSpace(out.String())
	}
	st := srv.store

	aliceID := run(0, "user", "create", "alice", "alice@example.com", "--role=administrator", "--user_pass=Very-long-pass-123", "--porcelain")
	if aliceID == "" {
		t.Fatal("no id")
	}
	out := run(0, "user", "create", "bob", "bob@example.com", "--role=author", "--first_name=Bob")
	if !strings.Contains(out, "Password: ") {
		t.Errorf("generated password not printed: %q", out)
	}
	run(1, "user", "create", "BOB", "other@example.com")
	run(1, "user", "create", "carol", "not-an-email")
	run(1, "user", "create", "carol", "carol@example.com", "--role=nope")

	if got := run(0, "user", "list", "--fields=user_login,roles", "--format=csv"); got != "user_login,roles\nalice,administrator\nbob,author" {
		t.Errorf("list: %q", got)
	}
	if got := run(0, "user", "list", "--format=count"); got != "2" {
		t.Errorf("count: %q", got)
	}
	if got := run(0, "user", "get", "bob@example.com", "--field=first_name"); got != "Bob" {
		t.Errorf("get: %q", got)
	}

	// Roles: the last administrator stays.
	run(1, "user", "set-role", "alice", "editor")
	run(1, "user", "remove-role", "alice")
	run(0, "user", "add-role", "bob", "editor")
	if got := run(0, "user", "get", "bob", "--field=roles"); got != "author,editor" {
		t.Errorf("add-role: %q", got)
	}
	run(0, "user", "remove-role", "bob", "author")
	if got := run(0, "user", "get", "bob", "--field=roles"); got != "editor" {
		t.Errorf("remove-role: %q", got)
	}

	// Capabilities.
	run(0, "user", "deny-cap", "bob", "publish_posts")
	run(0, "user", "add-cap", "bob", "upload_files", "edit_theme_options")
	if got := run(0, "user", "list-caps", "bob", "--origin=user", "--format=csv", "--fields=name,effect"); got != "name,effect\nedit_theme_options,allow\npublish_posts,deny\nupload_files,allow" {
		t.Errorf("list-caps user: %q", got)
	}
	if got := run(0, "user", "list-caps", "bob"); strings.Contains(got, "publish_posts") || !strings.Contains(got, "edit_theme_options") {
		t.Errorf("list-caps all: %q", got)
	}
	run(0, "user", "remove-cap", "bob", "publish_posts")
	run(1, "user", "remove-cap", "bob", "publish_posts")

	// Meta.
	run(0, "user", "meta", "update", "bob", "admin_color", "midnight")
	if got := run(0, "user", "meta", "get", "bob", "admin_color"); got != "midnight" {
		t.Errorf("meta get: %q", got)
	}
	run(0, "user", "meta", "update", "bob", "prefs", `{"a":[1,2]}`, "--format=json")
	if got := run(0, "user", "meta", "get", "bob", "prefs", "--format=json"); !strings.Contains(got, `"a"`) {
		t.Errorf("meta json: %q", got)
	}
	run(1, "user", "meta", "add", "bob", "admin_color", "x")
	run(0, "user", "meta", "delete", "bob", "admin_color", "prefs")
	run(1, "user", "meta", "get", "bob", "admin_color")

	// Application passwords and sessions; a password reset revokes both.
	bob, err := st.UserByLoginOrEmail(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	pw := run(0, "user", "application-password", "create", "bob", "CI", "--scopes=content", "--porcelain")
	if len(strings.ReplaceAll(pw, " ", "")) != 24 {
		t.Errorf("app password %q", pw)
	}
	run(1, "user", "application-password", "create", "bob", "ci")
	run(1, "user", "application-password", "create", "bob", "Other", "--scopes=bogus")
	if got := run(0, "user", "application-password", "list", "bob", "--format=count"); got != "1" {
		t.Errorf("app password count: %q", got)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO sessions(user_id, token_hash, expires_at) VALUES ($1, 'x', now() + interval '1 day'), ($1, 'y', now() + interval '1 day')`, bob.ID); err != nil {
		t.Fatal(err)
	}
	if got := run(0, "user", "session", "list", "bob", "--format=count"); got != "2" {
		t.Errorf("sessions: %q", got)
	}
	run(1, "user", "session", "destroy", "bob", "--all") // no --yes, not a terminal
	sid := strings.Fields(run(0, "user", "session", "list", "bob", "--format=ids"))[0]
	run(0, "user", "session", "destroy", "bob", sid)
	if got := run(0, "user", "session", "list", "bob", "--format=count"); got != "1" {
		t.Errorf("sessions after destroy: %q", got)
	}
	oldHash := bob.PasswordHash
	run(1, "user", "reset-password", "bob", `--password=a\b`)
	if got := run(0, "user", "reset-password", "bob", "--porcelain"); len(got) != 24 {
		t.Errorf("reset-password porcelain: %q", got)
	}
	bob, _ = st.UserByID(ctx, bob.ID)
	if bob.PasswordHash == oldHash || !strings.HasPrefix(bob.PasswordHash, "$argon2id$") {
		t.Error("password hash not replaced with argon2id")
	}
	var n int
	_ = st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM sessions WHERE user_id=$1) + (SELECT count(*) FROM app_passwords WHERE user_id=$1)`, bob.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d sessions / application passwords survived the reset", n)
	}

	// Update.
	run(0, "user", "update", "bob", "--display_name=Robert", "--user_url=example.org", "--locale=en_US")
	bob, _ = st.UserByID(ctx, bob.ID)
	if bob.DisplayName != "Robert" || bob.URL != "http://example.org" || bob.Locale != "" {
		t.Errorf("update: %+v", bob)
	}
	run(1, "user", "update", "bob", "--user_email=alice@example.com")
	run(2, "user", "update", "bob")

	// Roles and caps lists.
	if got := run(0, "role", "list", "--format=ids"); !strings.HasPrefix(got, "administrator editor") {
		t.Errorf("role list: %q", got)
	}
	if got := run(0, "cap", "list", "administrator"); !strings.Contains(got, "manage_options") {
		t.Errorf("cap list: %q", got)
	}
	run(1, "cap", "list", "nope")

	// Delete: confirmation, the last administrator, reassignment.
	var postID int64
	if err := st.Pool.QueryRow(ctx, `INSERT INTO posts(type, status, title, author_id) VALUES ('post', 'draft', 'Bob''s', $1) RETURNING id`, bob.ID).Scan(&postID); err != nil {
		t.Logf("inserting a post (skipping the reassign check): %v", err)
	}
	run(1, "user", "delete", "bob")
	run(1, "user", "delete", "alice", "--yes")
	run(1, "user", "delete", "bob", "--reassign=bob", "--yes")
	run(0, "user", "delete", "bob", "--reassign=alice", "--yes")
	run(1, "user", "exists", "bob")
	run(0, "user", "exists", "alice")
	if postID > 0 {
		var author int64
		_ = st.Pool.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1`, postID).Scan(&author)
		if itoa64(author) != aliceID {
			t.Errorf("post author after reassign: %d, want %s", author, aliceID)
		}
	}

	// With Pursae Security on, CLI actions are audited as "cli" (or "cli:<login>" with --user).
	if _, err := st.Pool.Exec(ctx, `INSERT INTO settings(key, value) VALUES ('native_adminsecgo', 'true') ON CONFLICT (key) DO UPDATE SET value = 'true'`); err != nil {
		t.Fatal(err)
	}
	run(0, "user", "create", "dave", "dave@example.com", "--porcelain", "--skip-password-policy")
	run(0, "user", "set-role", "dave", "editor", "--user=alice")
	var audited int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM adminsec_log WHERE (event = 'user_created' AND username = 'cli' AND object = 'dave')
		OR (event = 'role_changed' AND username = 'cli:alice' AND object = 'dave')`).Scan(&audited)
	if audited != 2 {
		t.Errorf("audit rows: %d, want 2", audited)
	}
}
