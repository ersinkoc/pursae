package config

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// captureWarnings sends slog to a buffer and forgets which variables were already reported.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	envWarned = sync.Map{}
	t.Cleanup(func() {
		slog.SetDefault(prev)
		envWarned = sync.Map{}
	})
	return &buf
}

func TestEnvNewName(t *testing.T) {
	buf := captureWarnings(t)
	t.Setenv("PURSAE_ENVTEST_X", "new")
	for _, name := range []string{"ENVTEST_X", "PURSAE_ENVTEST_X"} {
		if got := Env(name); got != "new" {
			t.Errorf("Env(%q) = %q, want new", name, got)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("warned for a PURSAE_ variable: %s", buf)
	}
	if v, ok := LookupEnv("ENVTEST_UNSET"); ok || v != "" {
		t.Errorf("LookupEnv of an unset variable = %q, %v", v, ok)
	}
	if got := EnvName("SITE_URL"); got != "PURSAE_SITE_URL" {
		t.Errorf("EnvName = %q", got)
	}
}

func TestEnvLegacyNameWarnsOnce(t *testing.T) {
	buf := captureWarnings(t)
	t.Setenv("WPGO_ENVTEST_X", "old-secret-value")
	for range 3 {
		if got := Env("ENVTEST_X"); got != "old-secret-value" {
			t.Fatalf("Env = %q, want the WPGO_ value", got)
		}
	}
	WarnDeprecatedEnv() // the start-up scan does not repeat a warning either
	out := buf.String()
	if n := strings.Count(out, "deprecated environment variable"); n != 1 {
		t.Fatalf("%d warnings, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "old=WPGO_ENVTEST_X") || !strings.Contains(out, "new=PURSAE_ENVTEST_X") {
		t.Errorf("the warning does not name both variables: %s", out)
	}
	if strings.Contains(out, "old-secret-value") {
		t.Errorf("the warning logs the value: %s", out)
	}
}

func TestEnvBothSet(t *testing.T) {
	buf := captureWarnings(t)
	t.Setenv("PURSAE_ENVTEST_X", "new")
	t.Setenv("WPGO_ENVTEST_X", "old")
	if got := Env("ENVTEST_X"); got != "new" {
		t.Errorf("Env = %q, want the PURSAE_ value", got)
	}
	Env("ENVTEST_X")
	out := buf.String()
	if n := strings.Count(out, "level=WARN"); n != 1 || !strings.Contains(out, "old=WPGO_ENVTEST_X") {
		t.Errorf("want one warning about the ignored old name:\n%s", out)
	}
	if strings.Contains(out, "=new") || strings.Contains(out, "=old ") {
		t.Errorf("the warning logs a value: %s", out)
	}

	// The same value under both names (an image that sets both for older releases) is not a conflict.
	buf.Reset()
	t.Setenv("PURSAE_ENVTEST_Y", "same")
	t.Setenv("WPGO_ENVTEST_Y", "same")
	if got := Env("ENVTEST_Y"); got != "same" || buf.Len() != 0 {
		t.Errorf("Env = %q, warnings: %s", got, buf)
	}

	// An empty PURSAE_ value is a value: it wins over the old name (a test or compose file clearing it).
	buf.Reset()
	t.Setenv("PURSAE_ENVTEST_Z", "")
	t.Setenv("WPGO_ENVTEST_Z", "1")
	if v, ok := LookupEnv("ENVTEST_Z"); v != "" || !ok {
		t.Errorf("LookupEnv = %q, %v; want the empty PURSAE_ value", v, ok)
	}
}

func TestWarnDeprecatedEnvAtStartup(t *testing.T) {
	buf := captureWarnings(t)
	t.Setenv("WPGO_ENVTEST_B", "1")
	t.Setenv("WPGO_ENVTEST_A", "1")
	t.Setenv("WPGO_LISTEN_FD", "7") // passed between Pursae processes, not a setting
	WarnDeprecatedEnv()
	out := buf.String()
	a, b := strings.Index(out, "WPGO_ENVTEST_A"), strings.Index(out, "WPGO_ENVTEST_B")
	if a < 0 || b < 0 || a > b {
		t.Errorf("want sorted warnings for both variables:\n%s", out)
	}
	if strings.Contains(out, "LISTEN_FD") {
		t.Errorf("warned about an internal variable:\n%s", out)
	}
}

// Load reads the old names too, so an existing deployment keeps working.
func TestLoadLegacyNames(t *testing.T) {
	captureWarnings(t)
	t.Setenv("PURSAE_SITE_URL", "")
	t.Setenv("PURSAE_HUB", "")
	t.Setenv("PURSAE_ADDR", "")
	t.Setenv("PURSAE_SMTP_PORT", "")
	t.Setenv("WPGO_SITE_URL", "https://old.example/")
	t.Setenv("WPGO_HUB", "off")
	if c := Load(); c.SiteURL != "http://localhost:8080" || c.HubOff {
		t.Errorf("an empty PURSAE_ variable must win (default site URL, Manager on), got %+v", c)
	}
	unset(t, "PURSAE_SITE_URL", "PURSAE_HUB", "PURSAE_ADDR", "PURSAE_SMTP_PORT")
	t.Setenv("WPGO_ADDR", ":9999")
	t.Setenv("WPGO_SMTP_PORT", "2525")
	c := Load()
	if c.SiteURL != "https://old.example" || !c.HubOff || c.Addr != ":9999" || c.SMTPPort != "2525" {
		t.Errorf("WPGO_ names not read: %+v", c)
	}
}

// unset removes variables for one test (t.Setenv first, so they are restored afterwards).
func unset(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		t.Setenv(n, "")
		if err := os.Unsetenv(n); err != nil {
			t.Fatal(err)
		}
	}
}
