package plugins

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestExampleManifestsAreValid(t *testing.T) {
	for _, dir := range []string{"examples/plugins/hello-gopher", "examples/plugins/word-counter", "internal/plugins/testdata/probe"} {
		data, err := os.ReadFile(moduleRoot + "/" + dir + "/plugin.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseManifest(data); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
}

func TestManifestValidationReportsAllProblems(t *testing.T) {
	_, err := ParseManifest([]byte(`{
		"slug": "Bad Slug",
		"name": "",
		"version": "one",
		"plugin_uri": "ftp://example.org",
		"permissions": ["options:read", "files:delete", "options:read"],
		"hooks": ["the_content", "admin_notices", "bad hook!", "init", "init"]
	}`))
	var me *ManifestError
	if !errors.As(err, &me) {
		t.Fatalf("want *ManifestError, got %v", err)
	}
	want := []string{
		`"slug" "Bad Slug"`,
		`"name" is required`,
		`"version" "one"`,
		`"plugin_uri"`,
		`unknown permission "files:delete"`,
		`permission "options:read" is listed twice`,
		`hook "the_content" requires the "content:filter" permission`,
		`hook "admin_notices" requires the "admin:notices" permission`,
		`hook name "bad hook!" is invalid`,
		`hook "init" is listed twice`,
	}
	msg := err.Error()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("error does not mention %s:\n%s", w, msg)
		}
	}
	if len(me.Problems) != len(want) {
		t.Errorf("got %d problems, want %d:\n%s", len(me.Problems), len(want), msg)
	}
}

func TestManifestRejectsUnknownFieldsAndBadJSON(t *testing.T) {
	if _, err := ParseManifest([]byte(`{"slug":"ok-slug","name":"x","version":"1.0.0","permision":[]}`)); err == nil ||
		!strings.Contains(err.Error(), "permision") {
		t.Fatalf("unknown field not reported: %v", err)
	}
	if _, err := ParseManifest([]byte(`{"slug":`)); err == nil {
		t.Fatal("truncated JSON accepted")
	}
}

func TestCheckCompatible(t *testing.T) {
	m := &Manifest{Slug: "x", Requires: "0.2.0"}
	for core, ok := range map[string]bool{"0.1.9": false, "0.2.0": true, "0.10.0": true, "1.0.0-beta": true, "": true} {
		if err := m.CheckCompatible(core); (err == nil) != ok {
			t.Errorf("core %q: err=%v, want ok=%v", core, err, ok)
		}
	}
}

func TestHookPermission(t *testing.T) {
	cases := map[string]Permission{
		"the_content":         PermContentFilter,
		"the_title":           PermContentFilter,
		"comment_text":        PermContentFilter,
		"admin_notices":       PermAdminNotices,
		"save_post":           PermPostsRead,
		"save_post_page":      PermPostsRead,
		"wpgo_post_stats":     PermPostsRead,
		"wp_insert_post_data": PermPostsWrite,
		"cron_hourly":         PermCron,
		"init":                "",
		"wp_head":             PermContentFilter,
		"wp_login":            PermUsersRead,
	}
	for hook, want := range cases {
		if got := HookPermission(hook); got != want {
			t.Errorf("HookPermission(%q) = %q, want %q", hook, got, want)
		}
	}
	for _, p := range AllPermissions() {
		if p.Description() == "" {
			t.Errorf("permission %q has no description", p)
		}
	}
}

func TestManifestRequiresPlugins(t *testing.T) {
	base := `{"slug":"dep-test","name":"Dep","version":"1.0.0","permissions":[],"hooks":[],"requires_plugins":%s}`
	for _, tc := range []struct {
		list string
		ok   bool
	}{{`["contact-form-7"]`, true}, {`["Bad Slug"]`, false}, {`["dep-test"]`, false}, {`["a-b","a-b"]`, false}} {
		_, err := ParseManifest([]byte(fmt.Sprintf(base, tc.list)))
		if (err == nil) != tc.ok {
			t.Errorf("requires_plugins %s: err = %v", tc.list, err)
		}
	}
}
