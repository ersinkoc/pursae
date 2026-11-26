//go:build !devhub

package server

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The tests of this package run their Pursae Managers on httptest servers (http://127.0.0.1:…),
// which a production build refuses (localDevHost). They opt in here; TestHubProductionRefusesLocalHosts
// checks what a binary does.
func init() { localHubsForTests.Store(true) }

// withoutLocalHubs runs the rest of the test as a production binary runs: no local Manager hosts.
func withoutLocalHubs(t *testing.T) {
	t.Helper()
	localHubsForTests.Store(false)
	t.Cleanup(func() { localHubsForTests.Store(true) })
}

// D17: outside a devhub build the Manager client never treats localhost, *.localhost, *.test or a
// loopback address as a trusted development host: no plain http, no redirect to it, no DNS bypass.
func TestHubProductionRefusesLocalHosts(t *testing.T) {
	withoutLocalHubs(t)
	for _, h := range []string{"localhost", "hub.localhost", "manager.test", "127.0.0.1", "::1", "LOCALHOST."} {
		if localDevHost(h) {
			t.Errorf("localDevHost(%q) in a production build", h)
		}
	}
	for _, raw := range []string{"http://localhost:8090/api", "http://127.0.0.1:8090", "http://[::1]:8090", "http://hub.localhost", "http://manager.test"} {
		u, _ := url.Parse(raw)
		if err := hubURLAllowed(u); err == nil {
			t.Errorf("hubURLAllowed(%s) in a production build", raw)
		}
	}
	if u, _ := url.Parse("https://hub.pursae.com/api/v1/index"); hubURLAllowed(u) != nil {
		t.Error("the production Manager over https is refused")
	}
	first, _ := http.NewRequest(http.MethodGet, "https://hub.example/api", nil)
	for _, raw := range []string{"http://127.0.0.1:9/api", "https://127.0.0.1:9/api", "https://localhost:9/api", "https://[::1]:9/api"} {
		next, _ := http.NewRequest(http.MethodGet, raw, nil)
		if err := hubCheckRedirect(next, []*http.Request{first}); err == nil {
			t.Errorf("hubCheckRedirect followed a redirect to %s", raw)
		}
	}
	if err := hubResolvedPublic("localhost"); err == nil {
		t.Error("hubResolvedPublic(localhost) in a production build")
	}
	// The dial refuses loopback even when the URL names it.
	req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1:1/api", nil)
	if resp, err := hubHTTPClient(2 * time.Second).Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("the Manager client dialed a loopback address")
	}
}
