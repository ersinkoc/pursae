package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// fakeHub is a minimal WordPress Go Hub (wpgo-admin/API.md) listing the word-counter example package.
func fakeHub(t *testing.T) (*httptest.Server, *[]map[string]string) {
	t.Helper()
	zip, err := os.ReadFile("../../examples/plugins/dist/word-counter-1.0.0.zip")
	if err != nil {
		t.Skip("example packages not built:", err)
	}
	pub, _ := os.ReadFile("../../examples/plugins/dist/dev-key.pub")
	sum := sha256.Sum256(zip)
	var pings []map[string]string
	var mu sync.Mutex
	var hub *httptest.Server
	hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/index":
			json.NewEncoder(w).Encode(map[string]any{"hub": "Test Hub", "api": 1,
				"core": map[string]any{"latest": "9.9.9", "download_url": "https://example.test/dl", "notes_url": "https://example.test/notes"},
				"keys": []map[string]string{{"id": "dev", "public": lastLine(string(pub))}}})
		case r.URL.Path == "/api/v1/plugins" || r.URL.Path == "/api/v1/plugins/word-counter":
			item := map[string]any{"slug": "word-counter", "name": "Word Counter", "version": "1.0.0", "description": "Counts words.", "author": "Gopher",
				"permissions": []string{"posts:read"}, "downloads": 42, "updated": "2026-10-20T10:00:00Z", "sha256": hex.EncodeToString(sum[:]),
				"download_url": hub.URL + "/api/v1/plugins/word-counter/download/1.0.0"}
			if r.URL.Path == "/api/v1/plugins" {
				json.NewEncoder(w).Encode(map[string]any{"total": 1, "items": []any{item}})
			} else {
				json.NewEncoder(w).Encode(item)
			}
		case strings.HasPrefix(r.URL.Path, "/api/v1/plugins/word-counter/download"):
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zip)
		case r.URL.Path == "/api/v1/sites/ping" && r.Method == http.MethodPost:
			var p map[string]string
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &p)
			mu.Lock()
			pings = append(pings, p)
			mu.Unlock()
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(hub.Close)
	return hub, &pings
}

func TestHubMarketplaceAndUpdates(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	hub, pings := fakeHub(t)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_hub_url": hub.URL}); err != nil {
		t.Fatal(err)
	}

	// Plugins › Add Plugin lists the marketplace.
	_, body, _ := get(t, c, base+"/wp-admin/plugin-install")
	for _, want := range []string{"Word Counter", "42 Installations", "October 20, 2026", "source=hub"} {
		if !strings.Contains(body, want) {
			t.Errorf("plugin-install lacks %q", want)
		}
	}
	m := regexp.MustCompile(`href="([^"]*action=install-plugin&amp;source=hub&amp;plugin=word-counter[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Hub install link")
	}
	// Installing downloads the package, checks the checksum and the Hub-published key verifies the signature.
	code, _, loc := get(t, c, base+html.UnescapeString(m[1]))
	if code != http.StatusFound || !strings.Contains(loc, "installed=Word+Counter") {
		t.Fatalf("hub install: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(body, `data-slug="word-counter"`) {
		t.Error("word-counter is not installed")
	}
	_, body, _ = get(t, c, base+"/wp-admin/plugin-install")
	if !strings.Contains(body, ">Installed<") {
		t.Error("the marketplace does not mark the plugin installed")
	}

	// Updates: the Hub announced 9.9.9.
	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	if !strings.Contains(body, "WordPress Go 9.9.9</strong> is available") || !strings.Contains(body, "https://example.test/dl") {
		t.Error("update-core does not announce the new release")
	}

	// The daily check sends the Hub the site URL and version, nothing else.
	if err := srv.hubPing(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*pings) != 1 {
		t.Fatalf("pings: %d", len(*pings))
	}
	p := (*pings)[0]
	if p["url"] != base || p["version"] != Version || len(p) != 2 {
		t.Errorf("ping payload %v", p)
	}
	// Opt-out stops the ping but keeps the release check.
	srv.settings.Set(ctx, map[string]any{"wpgo_hub_ping": false})
	srv.hubPing(ctx)
	if len(*pings) != 1 {
		t.Error("wpgo_hub_ping=false still pinged")
	}

	// Switched off: no marketplace, no update notice.
	srv.settings.Set(ctx, map[string]any{"wpgo_hub_url": "off"})
	_, body, _ = get(t, c, base+"/wp-admin/update-core?force-check=1")
	if !strings.Contains(body, "Update checks are off") {
		t.Error("update-core does not say the Hub is off")
	}
}

func TestVersionLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.1.0", "0.2.0", true}, {"0.2.0", "0.1.9", false}, {"1.0.0", "1.0.0", false}, {"v0.1.0", "0.1.1-rc1", true}, {"0.1", "0.1.0", false}} {
		if got := versionLess(tc.a, tc.b); got != tc.want {
			t.Errorf("versionLess(%q,%q)=%v", tc.a, tc.b, got)
		}
	}
	for _, s := range []string{"ab", "ab-cd", "x1"} {
		if !validSlug(s) {
			t.Errorf("validSlug(%q) false", s)
		}
	}
	for _, s := range []string{"-ab", "ab-", "A", "a b", "a/b", ""} {
		if validSlug(s) {
			t.Errorf("validSlug(%q) true", s)
		}
	}
}
