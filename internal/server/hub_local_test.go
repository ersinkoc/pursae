package server

import (
	"testing"

	"wpgo/internal/config"
)

// A development or test install (a local address) reports nothing to the public Manager, so it
// never appears among hub.pursae.com's sites; a local Manager (a development hub) still hears from it.
func TestHubSiteIsLocal(t *testing.T) {
	for _, c := range []struct {
		hub, home string
		want      bool
	}{
		{config.DefaultHubURL, "http://localhost:9212", true},
		{config.DefaultHubURL, "http://test-41.localhost:9212", true},
		{config.DefaultHubURL, "http://shop.test", true},
		{config.DefaultHubURL, "http://192.168.1.20", true},
		{config.DefaultHubURL, "http://127.0.0.1:8080", true},
		{config.DefaultHubURL, "https://example.com", false},
		{config.DefaultHubURL, "https://blog.pursae.com", false},
		{"http://127.0.0.1:8096", "http://localhost:9212", false},
		{"http://hub.localhost:8096", "http://test-1.localhost:9211", false},
	} {
		if got := hubLocalSite(c.hub, c.home); got != c.want {
			t.Errorf("hub %s, home %s: local %v, want %v", c.hub, c.home, got, c.want)
		}
	}
}
