package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"wpgo/internal/plugins"
)

// fakeManager is a minimal Pursae Manager (pursae-manager/API.md) listing the word-counter example
// package. It records every request's site and license headers, the pings and the events, answers
// /api/v1/license for validKey, and can publish newer word-counter versions and a core release.
type fakeManager struct {
	*httptest.Server
	mu       sync.Mutex
	reqs     []managerReq
	pings    []map[string]any
	events   []hubEvent
	zips     map[string][]byte // word-counter by version
	latest   string
	badSHA   bool // announce a wrong sha256 for the latest version
	keys     []map[string]string
	core     map[string]any
	coreBin  []byte
	coreAt   string // when set, core downloads redirect here (a GitHub release asset)
	access   string
	channel  string
	validKey string
	themeLib fakeThemes // GET /api/v1/themes… (theme_manager_test.go)
}

type managerReq struct{ Method, Path, Site, License string }

func fakeHub(t *testing.T) *fakeManager {
	t.Helper()
	zip, err := os.ReadFile("../../examples/plugins/dist/word-counter-1.0.0.zip")
	if err != nil {
		t.Skip("example packages not built (tools/build-plugins.sh):", err)
	}
	pub, _ := os.ReadFile("../../examples/plugins/dist/dev-key.pub")
	m := &fakeManager{zips: map[string][]byte{"1.0.0": zip}, latest: "1.0.0", access: "public", channel: "stable", validKey: "PRS-7Q4M-K2XD-9VNA-H3TC",
		keys: []map[string]string{{"id": "dev", "public": lastLine(string(pub))}},
		core: map[string]any{"latest": "9.9.9", "channel": "stable", "download_url": "https://example.test/dl", "notes_url": "https://example.test/notes"}}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.Close)
	return m
}

func (m *fakeManager) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs = append(m.reqs, managerReq{r.Method, r.URL.Path, r.Header.Get("X-Pursae-Site"), r.Header.Get("X-Pursae-License")})
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/v1/index":
		json.NewEncoder(w).Encode(map[string]any{"hub": "Test Manager", "api": 1, "core": m.core, "keys": m.keys})
	case r.URL.Path == "/api/v1/license":
		if r.Header.Get("X-Pursae-License") == m.validKey && m.validKey != "" {
			json.NewEncoder(w).Encode(map[string]any{"valid": true, "plan": "pro", "pro": true, "beta": true, "expires": "2027-10-31T00:00:00Z",
				"domains": []string{r.Header.Get("X-Pursae-Site")}, "packages": []string{"plugin/acme-pro"}, "checked": "2026-10-31T12:00:00Z"})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"valid": false, "error": "unknown", "message": "This key does not exist."})
		}
	case r.URL.Path == "/api/v1/plugins" || r.URL.Path == "/api/v1/plugins/word-counter":
		sum := sha256.Sum256(m.zips[m.latest])
		sha := hex.EncodeToString(sum[:])
		if m.badSHA {
			sha = strings.Repeat("0", 64)
		}
		item := map[string]any{"slug": "word-counter", "name": "Word Counter", "version": m.latest, "description": "Counts words.", "author": "Gopher",
			"permissions": []string{"posts:read"}, "downloads": 42, "updated": "2026-10-20T10:00:00Z", "sha256": sha,
			"download_url": m.URL + "/api/v1/plugins/word-counter/download/" + m.latest, "access": m.access, "channel": m.channel, "homepage": "https://example.test/word-counter"}
		if r.URL.Path == "/api/v1/plugins" {
			json.NewEncoder(w).Encode(map[string]any{"total": 1, "items": []any{item}})
		} else {
			json.NewEncoder(w).Encode(item)
		}
	case strings.HasPrefix(r.URL.Path, "/api/v1/plugins/word-counter/download/"):
		zip, ok := m.zips[strings.TrimPrefix(r.URL.Path, "/api/v1/plugins/word-counter/download/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zip)
	case strings.HasPrefix(r.URL.Path, "/api/v1/core/") && m.coreAt != "":
		http.Redirect(w, r, m.coreAt, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/api/v1/core/"):
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(m.coreBin)
	case r.URL.Path == "/api/v1/sites/ping" && r.Method == http.MethodPost:
		var p map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &p)
		m.pings = append(m.pings, p)
		w.Write([]byte(`{"ok":true}`))
	case r.URL.Path == "/api/v1/sites/events" && r.Method == http.MethodPost:
		var body struct {
			URL, Version string
			Events       []hubEvent
		}
		b, _ := io.ReadAll(r.Body)
		if json.Unmarshal(b, &body) != nil || len(body.Events) > 100 || body.URL == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.events = append(m.events, body.Events...)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"ok":true,"accepted":%d}`, len(body.Events))
	case r.URL.Path == "/api/v1/themes" || strings.HasPrefix(r.URL.Path, "/api/v1/themes/"):
		m.serveThemes(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// publish adds a word-counter version signed with priv (the 1.0.0 code, a new version number).
func (m *fakeManager) publish(t *testing.T, version string, priv ed25519.PrivateKey) {
	t.Helper()
	pkg, err := plugins.LoadPackage(bytes.NewReader(m.zips["1.0.0"]), int64(len(m.zips["1.0.0"])), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Manifest.Version = version
	manifest, _ := json.Marshal(pkg.Manifest)
	var buf bytes.Buffer
	if err := plugins.WritePackage(&buf, manifest, pkg.Wasm, nil, priv); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.zips[version], m.latest = buf.Bytes(), version
	m.mu.Unlock()
}

func (m *fakeManager) requests() []managerReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]managerReq(nil), m.reqs...)
}

func (m *fakeManager) eventTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, e := range m.events {
		out = append(out, e.Type+":"+e.Slug)
	}
	return out
}

func TestHubMarketplaceAndUpdates(t *testing.T) {
	withTrustHubKeys(t, true) // the fake Manager's dev key carries no endorsement by the pinned root
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	hub := fakeHub(t)
	pings := &hub.pings
	ctx := context.Background()
	srv.cfg.HubURL = hub.URL // the Manager URL is fixed in production builds

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
	if !strings.Contains(body, "Pursae 9.9.9</strong> is available") || !strings.Contains(body, "https://example.test/dl") {
		t.Error("update-core does not announce the new release")
	}

	// The daily check sends the site, its platform, license and inventory (always on, no opt-out).
	if err := srv.hubPing(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*pings) != 1 {
		t.Fatalf("pings: %d", len(*pings))
	}
	p := (*pings)[0]
	if p["url"] != base || p["version"] != Version || p["os"] != runtime.GOOS || p["arch"] != runtime.GOARCH || p["locale"] != "en_US" ||
		p["license"] != "" || p["theme"] != srv.activeTheme().Slug || len(p) != 9 {
		t.Errorf("ping payload %v", p)
	}
	inv := map[string]map[string]any{}
	for _, it := range p["plugins"].([]any) {
		e := it.(map[string]any)
		inv[e["slug"].(string)] = e
	}
	if wc := inv["word-counter"]; wc == nil || wc["source"] != "hub" || wc["version"] != "1.0.0" || wc["active"] != false {
		t.Errorf("word-counter in the inventory: %v", wc)
	}
	if ce := inv["classic-editor"]; ce == nil || ce["source"] != "native" {
		t.Errorf("classic-editor in the inventory: %v", ce)
	}
	themes, _ := p["themes"].([]any)
	var activeThemes int
	for _, it := range themes {
		if e := it.(map[string]any); e["active"] == true {
			activeThemes++
			if e["slug"] != p["theme"] || e["source"] != "native" {
				t.Errorf("active theme %v", e)
			}
		}
	}
	if len(themes) == 0 || activeThemes != 1 {
		t.Errorf("themes inventory %v", themes)
	}
	// The ping flushed the queued install event.
	if got := strings.Join(hub.eventTypes(), ","); got != "plugin_install:word-counter" {
		t.Errorf("events after the ping: %s", got)
	}

	// Switched off: no marketplace, no update notice.
	srv.cfg.HubURL = "off"
	_, body, _ = get(t, c, base+"/wp-admin/update-core?force-check=1")
	if !strings.Contains(body, "Update checks are off") {
		t.Error("update-core does not say the Hub is off")
	}
}

func TestVersionLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.1.0", "0.2.0", true}, {"0.2.0", "0.1.9", false}, {"1.0.0", "1.0.0", false}, {"v0.1.0", "0.1.1-rc1", true}, {"0.1", "0.1.0", false},
		// pre-releases: a beta site is offered the release, never the other way round
		{"0.2.0-beta.1", "0.2.0", true}, {"0.2.0", "0.2.0-beta.1", false}, {"0.2.0-beta.1", "0.2.0-beta.2", true},
		{"0.2.0-beta.2", "0.2.0-beta.10", true}, {"0.2.0-beta.2", "0.2.0-rc.1", true}, {"0.2.0-rc.1", "0.2.0-beta.9", false},
		{"0.2.0-beta", "0.2.0-beta.1", true}, {"0.2.0-1", "0.2.0-beta", true}, {"0.2.0-beta.1", "0.2.0-beta.1", false},
		{"1.0.0+build.5", "1.0.0", false}, {"1.0.0", "1.0.0+build.5", false}, {"0.1.9-beta.1", "0.2.0-beta.1", true}} {
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
