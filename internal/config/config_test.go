package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"WPGO_ADDR", "DATABASE_URL", "WPGO_SITE_URL", "WPGO_UPLOADS_DIR", "WPGO_DEV_HUB_URL", "WPGO_SMTP_PORT", "WPGO_SECRET", "WPGO_WEB_DIR"} {
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
	if c.HubURL != "https://hub.pursae.com" {
		t.Errorf("HubURL = %q, want https://hub.pursae.com", c.HubURL)
	}
}

func TestLoadTrimsTrailingSlashes(t *testing.T) {
	t.Setenv("WPGO_SITE_URL", "https://example.com///")
	c := Load()
	if c.SiteURL != "https://example.com" {
		t.Errorf("SiteURL = %q", c.SiteURL)
	}
}

func TestHubOff(t *testing.T) {
	for v, off := range map[string]bool{"": false, "on": false, "https://x.example": false, "off": true, "OFF": true, " none ": true, "0": true, "false": true, "no": true} {
		t.Setenv("WPGO_HUB", v)
		c := Load()
		if c.HubOff != off || (off && c.HubURL != "off") || (!off && c.HubURL == "off") {
			t.Errorf("WPGO_HUB=%q: HubOff=%v HubURL=%q", v, c.HubOff, c.HubURL)
		}
	}
}
