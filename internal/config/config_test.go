package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"WPGO_ADDR", "DATABASE_URL", "WPGO_SITE_URL", "WPGO_UPLOADS_DIR", "WPGO_HUB_URL", "WPGO_SMTP_PORT", "WPGO_SECRET", "WPGO_WEB_DIR"} {
		t.Setenv(k, "")
	}
	c := Load()
	if c.Addr != ":8080" || c.UploadsDir != "./data/uploads" || c.SMTPPort != "587" {
		t.Errorf("defaults changed: %+v", c)
	}
	if c.SiteURL != "http://localhost:8080" {
		t.Errorf("SiteURL = %q", c.SiteURL)
	}
	if c.Secret != "" || c.WebDir != "" {
		t.Errorf("Secret and WebDir must stay empty unless set: %+v", c)
	}
}

func TestLoadTrimsTrailingSlashes(t *testing.T) {
	t.Setenv("WPGO_SITE_URL", "https://example.com///")
	t.Setenv("WPGO_HUB_URL", "https://hub.example.com/")
	c := Load()
	if c.SiteURL != "https://example.com" {
		t.Errorf("SiteURL = %q", c.SiteURL)
	}
	if c.HubURL != "https://hub.example.com" {
		t.Errorf("HubURL = %q", c.HubURL)
	}
}

func TestLoadHubOffIsKept(t *testing.T) {
	t.Setenv("WPGO_HUB_URL", "off")
	if got := Load().HubURL; got != "off" {
		t.Errorf("HubURL = %q, want off", got)
	}
}
