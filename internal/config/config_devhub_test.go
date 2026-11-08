//go:build devhub

package config

import "testing"

// A devhub build can point at a local Manager (or switch it off); WPGO_HUB_URL is not read.
func TestDevHubURL(t *testing.T) {
	t.Setenv("WPGO_HUB_URL", "https://evil.example.com")
	t.Setenv("WPGO_DEV_HUB_URL", "")
	if got := Load().HubURL; got != DefaultHubURL {
		t.Errorf("HubURL = %q", got)
	}
	t.Setenv("WPGO_DEV_HUB_URL", "http://localhost:8090/")
	if got := Load().HubURL; got != "http://localhost:8090" {
		t.Errorf("HubURL = %q", got)
	}
	t.Setenv("WPGO_DEV_HUB_URL", "off")
	if got := Load().HubURL; got != "off" {
		t.Errorf("HubURL = %q, want off", got)
	}
}
