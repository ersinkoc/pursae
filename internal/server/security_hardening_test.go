package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// A comment author's URL is shown as a link, so only http and https URLs may be stored or rendered.
func TestCommentAuthorURL(t *testing.T) {
	ok := map[string]string{
		"https://example.com/a?b=1": "https://example.com/a?b=1",
		"http://example.com":        "http://example.com",
		"example.com/me":            "http://example.com/me", // like the comment form: a missing scheme becomes http://
		"  https://example.com  ":   "https://example.com",
		"":                          "",
		"example.com:8080/x":        "http://example.com:8080/x",
	}
	for in, want := range ok {
		if got := commentAuthorURL(in); got != want {
			t.Errorf("commentAuthorURL(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{
		"javascript:alert(1)", "JaVaScRiPt:alert(1)", "java\tscript:alert(1)", "java\nscript:alert(1)", "vbscript:x",
		"data:text/html;base64,PHNjcmlwdD4=", "//evil.example", "http://", "https://exa mple.com", "http://example.com/\x00",
		"ftp://example.com/", "mailto:a@b.example", "http://[::1", "http:///x",
	} {
		if got := commentAuthorURL(in); got != "" {
			t.Errorf("commentAuthorURL(%q) = %q, want it dropped", in, got)
		}
	}
}

// update_option must stay inside the plugin's own options. A slug that equals the first word of a core
// option must not reach that option.
func TestPluginOptionAllowed(t *testing.T) {
	allow := []struct{ slug, name string }{
		{"hello-gopher", "hello_gopher_count"}, {"hello-gopher", "plugin_hello_gopher_greeting"},
		{"word-counter", "word_counter_total"}, {"default", "default_greeting"}, {"allow", "plugin_allow_mine"},
	}
	for _, c := range allow {
		if !pluginOptionAllowed(c.slug, c.name) {
			t.Errorf("pluginOptionAllowed(%q, %q) = false, want true", c.slug, c.name)
		}
	}
	deny := []struct{ slug, name string }{
		{"default", "default_role"}, {"users", "users_can_register"}, {"plugin-allow", "plugin_allow_unsigned"},
		{"allow", "plugin_allow_unsigned"}, {"trusted", "plugin_trusted_keys"}, {"wpgo", "wpgo_hub_url"}, {"wpgo", "wpgo_hub_trust_keys"},
		{"auth", "auth_secret"}, {"wpgo", "wpgo_plugin_note"}, {"native", "native_golementor"}, {"hello-gopher", "default_role"}, {"hello-gopher", "blogname"},
		{"blogname", "blogname"}, {"admin", "admin_email"}, {"date", "date_format"}, {"time", "time_format"}, {"start", "start_of_week"},
	}
	for _, c := range deny {
		if pluginOptionAllowed(c.slug, c.name) {
			t.Errorf("pluginOptionAllowed(%q, %q) = true, want false", c.slug, c.name)
		}
	}
}

func clientIPFor(t *testing.T, spec, remote, xff string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := applyTrustedProxies(r, spec); err != nil {
		t.Fatal(err)
	}
	r.GET("/", func(c *gin.Context) { c.String(200, clientIP(c)) })
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

// Behind a proxy on a private address the visitor's address is used, so one visitor's failed logins
// do not lock out everybody; a client that reaches the server directly cannot choose its own address.
func TestClientIPTrustedProxies(t *testing.T) {
	cases := []struct{ name, spec, remote, xff, want string }{
		{"proxy on a private address", "", "10.0.0.2:5555", "203.0.113.9", "203.0.113.9"},
		{"proxy on loopback", "", "127.0.0.1:5555", "203.0.113.9", "203.0.113.9"},
		{"chain, the proxy appends the client", "", "172.18.0.2:5555", "198.51.100.77, 203.0.113.9", "203.0.113.9"},
		{"direct public client cannot spoof", "", "198.51.100.7:5555", "203.0.113.9", "198.51.100.7"},
		{"no header", "", "10.0.0.2:5555", "", "10.0.0.2"},
		{"none trusts nobody", "none", "10.0.0.2:5555", "203.0.113.9", "10.0.0.2"},
		{"explicit list, listed proxy", "198.51.100.0/24", "198.51.100.7:5555", "203.0.113.9", "203.0.113.9"},
		{"explicit list, unlisted private peer", "198.51.100.0/24", "10.0.0.2:5555", "203.0.113.9", "10.0.0.2"},
		{"garbage header from a trusted proxy", "", "10.0.0.2:5555", "not-an-ip", "10.0.0.2"},
	}
	for _, c := range cases {
		if got := clientIPFor(t, c.spec, c.remote, c.xff); got != c.want {
			t.Errorf("%s: clientIP = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestApplyTrustedProxiesRejectsGarbage(t *testing.T) {
	if err := applyTrustedProxies(gin.New(), "10.0.0.0/8, not-a-cidr"); err == nil {
		t.Error("an unparsable entry must be an error, not silently ignored")
	}
}
