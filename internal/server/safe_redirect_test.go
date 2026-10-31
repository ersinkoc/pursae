package server

import "testing"

func TestSafeRedirect(t *testing.T) {
	const fb = "/wp-admin/"
	same := []string{"/wp-admin/edit?post_type=page", "/", "/a/b?x=1&y=2", "/blog/hello-world"}
	for _, u := range same {
		if got := safeRedirect(u, fb); got != u {
			t.Errorf("safeRedirect(%q) = %q, want it unchanged", u, got)
		}
	}
	// Anything a browser could read as another host, or that carries control characters, falls back.
	bad := []string{
		"", "evil.example", "//evil.example", `/\evil.example`, "https://evil.example/", "javascript:alert(1)",
		"/\t/evil.example", "/\n/evil.example", "/\r/evil.example", "/%09/evil.example" + "\x00",
		"/ok\x7f", "/\x01x", "/a?b=\t//evil.example",
	}
	for _, u := range bad {
		if got := safeRedirect(u, fb); got != fb {
			t.Errorf("safeRedirect(%q) = %q, want fallback %q", u, got, fb)
		}
	}
}
