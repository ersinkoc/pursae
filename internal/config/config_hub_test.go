//go:build !devhub

package config

import "testing"

// A production build talks to hub.pursae.com whatever the environment says.
func TestProductionHubURLIsFixed(t *testing.T) {
	t.Setenv("WPGO_HUB_URL", "https://evil.example.com")
	t.Setenv("WPGO_DEV_HUB_URL", "http://localhost:8090")
	if got := Load().HubURL; got != DefaultHubURL {
		t.Errorf("HubURL = %q, want %q", got, DefaultHubURL)
	}
	t.Setenv("WPGO_DEV_HUB_URL", "off")
	if got := Load().HubURL; got != DefaultHubURL {
		t.Errorf("HubURL with WPGO_DEV_HUB_URL=off = %q", got)
	}
	if DevHub {
		t.Error("DevHub in a production build")
	}
}
