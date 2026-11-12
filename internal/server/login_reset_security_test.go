package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A site whose home URL is a local or private address (a LAN / intranet install, *.local, a
// private IP) must not build e-mailed credential links from the request's Host header: anyone who
// can reach the server can name their own private address in Host and receive the victim's reset key.
func TestLostPassword_ResetLinkIgnoresForgedLocalHost(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installUsersSite(t, c, base)

	form := url.Values{"user_login": {"admin"}}
	req, err := http.NewRequest(http.MethodPost, base+"/wp-login?action=lostpassword", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "192.168.66.6:8080" // the attacker's machine on the same private network
	resp, err := asNewClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "checkemail=confirm") {
		t.Fatalf("lost password request not accepted: %d %q", resp.StatusCode, loc)
	}
	m := lastMail(t, "Password Reset")
	link := firstURL(t, m.Body, "action=rp")
	if strings.Contains(m.Body, "192.168.66.6") {
		t.Fatalf("password reset mail names the forged Host: %s", m.Body)
	}
	if !strings.HasPrefix(link, base+"/") {
		t.Fatalf("password reset link %q does not use the configured home %q", link, base)
	}
}

// localOrigin still lets rendered pages of a development site follow the request's address, while
// configuredLinks (credential links, mail) never does.
func TestConfiguredLinks_IgnoreRequestOrigin(t *testing.T) {
	ctx := context.WithValue(context.Background(), originKey{}, "http://192.168.66.6:8080")
	home := "http://127.0.0.1:8088"
	if got := localOrigin(ctx, home); got != "http://192.168.66.6:8080" {
		t.Fatalf("localOrigin on a development site = %q, want the request origin", got)
	}
	if got := localOrigin(withConfiguredOrigin(ctx), home); got != home {
		t.Fatalf("localOrigin with withConfiguredOrigin = %q, want %q", got, home)
	}
}
