package plugins

import (
	"strings"
	"testing"
)

// A plugin's route pattern is text it supplies. It must stay inside the plugin's own namespace: "|" and
// unbalanced parentheses must not let it match another plugin's or the core's paths.
func TestRestRouteCannotEscapeNamespace(t *testing.T) {
	re, err := compileRestRoute("aaa", "/x|.*")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/bbb/v1/submit", "/wp/v2/posts", "/x", "/other"} {
		if re.MatchString(path) {
			t.Errorf("route \"/x|.*\" of namespace aaa matched %q outside its namespace", path)
		}
	}
	for _, path := range []string{"/aaa/x", "/aaa/anything"} {
		if !re.MatchString(path) {
			t.Errorf("route \"/x|.*\" of namespace aaa should match %q inside its namespace", path)
		}
	}
	if _, err := compileRestRoute("aaa", "/x)|(.*"); err == nil {
		t.Error("a pattern that closes the surrounding group must be rejected")
	}
	if _, err := compileRestRoute("aaa", "/items/(?P<id>[0-9]+"); err == nil {
		t.Error("an unbalanced pattern must be rejected")
	}
	ok, err := compileRestRoute("my-plugin/v1", `/items/(?P<id>\d+)`)
	if err != nil || !ok.MatchString("/my-plugin/v1/items/42") || ok.MatchString("/my-plugin/v1/items/abc") || ok.MatchString("/my-plugin/v2/items/42") {
		t.Errorf("an ordinary route stopped working: %v", err)
	}
}

func TestValidSlug(t *testing.T) {
	for _, ok := range []string{"hello-gopher", "ab", "a1-b2-c3"} {
		if !ValidSlug(ok) {
			t.Errorf("ValidSlug(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "a", "../x", "x/y", "a--b", "-a", "Abc", "a b", "a.zip", "..%2fx"} {
		if ValidSlug(bad) {
			t.Errorf("ValidSlug(%q) = true", bad)
		}
	}
}

// The contact form hooks carry visitors' personal data and the spam verdict, so they need a permission.
func TestContactFormHooksNeedAPermission(t *testing.T) {
	for _, h := range []string{"wpcf7_mail_sent", "wpcf7_spam", "wpcf7_anything", "preprocess_comment", "pre_comment_approved", "comment_post"} {
		if HookPermission(h) != PermForms {
			t.Errorf("HookPermission(%q) = %q, want %q", h, HookPermission(h), PermForms)
		}
	}
	if !PermForms.Valid() {
		t.Error("forms:access is not a valid permission")
	}
}

// A package slugged after a core REST namespace root could answer under that namespace.
func TestReservedSlugsAreRefused(t *testing.T) {
	for _, slug := range []string{"core", "wp", "wc", "pll", "contact-form-7", "oembed", "pursae", "web-builder"} {
		_, err := ParseManifest([]byte(`{"slug":"` + slug + `","name":"X","version":"1.0.0","permissions":["rest:routes"],"hooks":[]}`))
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("slug %q accepted: %v", slug, err)
		}
	}
	if _, err := ParseManifest([]byte(`{"slug":"wp-tools","name":"X","version":"1.0.0","permissions":[],"hooks":[]}`)); err != nil {
		t.Errorf("ordinary slug refused: %v", err)
	}
}
