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

	// An empty PURSAE_ value does not hide a non-empty old one: the compose files pass every
	// PURSAE_ key (empty when .env has neither name) while an override file may set the WPGO_ name.
	buf.Reset()
	t.Setenv("PURSAE_ENVTEST_Z", "")
	t.Setenv("WPGO_ENVTEST_Z", "old-z-value")
	if v, ok := LookupEnv("ENVTEST_Z"); v != "old-z-value" || !ok {
		t.Errorf("LookupEnv = %q, %v; want the non-empty WPGO_ value", v, ok)
	}
	if out := buf.String(); strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, "using=WPGO_ENVTEST_Z") || strings.Contains(out, "old-z-value") {
		t.Errorf("want one warning saying the old name is used, without the value:\n%s", out)
	}

	// Both empty: set, empty, no warning.
	buf.Reset()
	t.Setenv("PURSAE_ENVTEST_W", "")
	t.Setenv("WPGO_ENVTEST_W", "")
	if v, ok := LookupEnv("ENVTEST_W"); v != "" || !ok || buf.Len() != 0 {
		t.Errorf("LookupEnv = %q, %v; warnings: %s", v, ok, buf)
	}
}

// In the Docker image the warning says to update the compose files before renaming .env.
func TestEnvLegacyWarningInContainer(t *testing.T) {
	buf := captureWarnings(t)
	t.Setenv("PURSAE_BIN_DIR", "/data/bin")
	t.Setenv("WPGO_BIN_DIR", "/data/bin")
	t.Setenv("WPGO_ENVTEST_C", "1")
	WarnDeprecatedEnv()
	out := buf.String()
	if !strings.Contains(out, "WPGO_ENVTEST_C") || !strings.Contains(out, "update the compose files") {
		t.Errorf("want the compose hint:\n%s", out)
	}
	if strings.Contains(out, "BIN_DIR") {
		t.Errorf("the same value under both names must not warn:\n%s", out)
	}
	unset(t, "PURSAE_BIN_DIR", "WPGO_BIN_DIR")
	buf2 := captureWarnings(t)
	Env("ENVTEST_C")
	if strings.Contains(buf2.String(), "compose") {
		t.Errorf("compose hint outside a container:\n%s", buf2)
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
	// The compose files set every PURSAE_ key, empty when .env has neither name; an override file
	// that still sets the WPGO_ name must keep working.
	if c := Load(); c.SiteURL != "https://old.example" || !c.HubOff {
		t.Errorf("an empty PURSAE_ variable hid the WPGO_ value, got %+v", c)
	}
	unset(t, "PURSAE_SITE_URL", "PURSAE_HUB", "PURSAE_ADDR", "PURSAE_SMTP_PORT")
	t.Setenv("WPGO_ADDR", ":9999")
	t.Setenv("WPGO_SMTP_PORT", "2525")
	c := Load()
	if c.SiteURL != "https://old.example" || !c.HubOff || c.Addr != ":9999" || c.SMTPPort != "2525" {
		t.Errorf("WPGO_ names not read: %+v", c)
	}
	// Two non-empty values: PURSAE_ wins.
	t.Setenv("PURSAE_SITE_URL", "https://new.example")
	if c := Load(); c.SiteURL != "https://new.example" {
		t.Errorf("PURSAE_SITE_URL did not win: %q", c.SiteURL)
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

// A rollback execs a release before 0.5.0, which reads only the WPGO_* names.
func TestWithLegacyNames(t *testing.T) {
	in := []string{"PATH=/bin", "PURSAE_SITE_URL=https://a.example", "PURSAE_SECRET=", "PURSAE_HUB=on", "WPGO_HUB=off", "PURSAE_=x", "PURSAE_X=a=b"}
	got := WithLegacyNames(in)
	want := map[string]int{
		"WPGO_SITE_URL=https://a.example": 1,
		"WPGO_SECRET=":                    1,
		"WPGO_HUB=off":                    1, // an existing old name is kept as it is
		"WPGO_X=a=b":                      1,
	}
	count := map[string]int{}
	for _, kv := range got {
		count[kv]++
	}
	for kv, n := range want {
		if count[kv] != n {
			t.Errorf("%q appears %d times, want %d: %v", kv, count[kv], n, got)
		}
	}
	if count["WPGO_HUB=on"] != 0 || count["WPGO_=x"] != 0 || len(got) != len(in)+3 {
		t.Errorf("unexpected entries: %v", got)
	}
	if len(in) != 7 || in[0] != "PATH=/bin" {
		t.Errorf("the input was changed: %v", in)
	}
}
