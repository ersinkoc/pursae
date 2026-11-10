package server

import (
	"net/http"
	"testing"
)

// Plugin REST handlers may set only an allow-list of response headers (and their own X-<Slug>-*).
func TestPluginRestHeaderAllowed(t *testing.T) {
	cases := []struct {
		k, v string
		want bool
	}{
		{"cache-control", "no-store", true},
		{"x-wp-total", "3", true},
		{"x-shortcode-demo-count", "1", true},
		{"x-other-plugin-count", "1", false},
		{"location", "/wp-json/shortcode-demo/v1/facts", true},
		{"location", "https://evil.example/", false},
		{"location", "//evil.example/", false},
		{"location", "/\\evil.example/", false},
		{"clear-site-data", `"cookies"`, false},
		{"x-accel-redirect", "/internal/", false},
		{"access-control-allow-origin", "*", false},
		{"content-security-policy", "default-src *", false},
		{"content-encoding", "gzip", false},
		{"set-cookie", "a=b", false},
		{"refresh", "0;url=https://evil.example/", false},
	}
	for _, c := range cases {
		if got := pluginRestHeaderAllowed("shortcode-demo", http.CanonicalHeaderKey(c.k), c.v); got != c.want {
			t.Errorf("%s: %q = %v, want %v", c.k, c.v, got, c.want)
		}
	}
}
