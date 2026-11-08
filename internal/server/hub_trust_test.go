package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"wpgo/internal/config"
	"wpgo/internal/plugins"
	"wpgo/internal/settings"
)

// withRootKeys replaces the build's root keys (the pinned Pursae root and the -ldflags ones) with
// throwaway roots for one test; no roots is a build without any root key.
func withRootKeys(t *testing.T, roots ...ed25519.PublicKey) {
	t.Helper()
	savedBuiltin, savedLinked := rootKeysBuiltin, rootKeysLinked
	var list []string
	for _, r := range roots {
		list = append(list, base64.StdEncoding.EncodeToString(r))
	}
	rootKeysBuiltin, rootKeysLinked = nil, strings.Join(list, ",")
	t.Cleanup(func() { rootKeysBuiltin, rootKeysLinked = savedBuiltin, savedLinked })
}

// withTrustHubKeys sets the devhub WPGO_TRUST_HUB_KEYS opt-in for one test.
func withTrustHubKeys(t *testing.T, on bool) {
	t.Helper()
	saved := trustHubKeysForce
	trustHubKeysForce = on
	t.Cleanup(func() { trustHubKeysForce = saved })
}

// pursaeReleaseRoot is the production release root key pinned in rootKeysBuiltin.
const pursaeReleaseRoot = "LxoPRqTiJfpXB0fc+peWabyDCWO/NzATjpk7pSe1IdY="

// Every build pins the Pursae release root; a linked key adds a root (rotation), never replaces it.
func TestPursaeRootKeyPinned(t *testing.T) {
	want, err := plugins.ParsePublicKey(pursaeReleaseRoot)
	if err != nil {
		t.Fatal(err)
	}
	has := func() bool {
		for _, k := range rootKeys() {
			if k.Equal(want) {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Fatalf("rootKeys() lacks the Pursae release root %s", pursaeReleaseRoot)
	}
	if err := coreTrustCheck(); err != nil {
		t.Errorf("a production build cannot update itself: %v", err)
	}
	extra, _, _ := ed25519.GenerateKey(rand.Reader)
	saved := rootKeysLinked
	rootKeysLinked = base64.StdEncoding.EncodeToString(extra)
	t.Cleanup(func() { rootKeysLinked = saved })
	if !has() || len(rootKeys()) != len(rootKeysBuiltin)+1 {
		t.Errorf("a linked root replaced the pinned one: %d roots", len(rootKeys()))
	}
	// An unendorsed Manager key verifies neither releases nor plugins under the real root.
	hubPub, _, _ := ed25519.GenerateKey(rand.Reader)
	listed := []hubKey{{ID: "hub-2026", Public: base64.StdEncoding.EncodeToString(hubPub)}}
	if _, err := coreReleaseKeys(listed); err == nil {
		t.Error("an unendorsed Manager key verifies releases")
	}
	if got := pluginHubKeys(listed); len(got) != 0 {
		t.Errorf("an unendorsed Manager key signs plugins: %d", len(got))
	}
}

// A production build ignores WPGO_TRUST_HUB_KEYS: only a devhub build reads it.
func TestTrustHubKeysEnvIgnored(t *testing.T) {
	t.Setenv("WPGO_TRUST_HUB_KEYS", "1")
	if trustHubKeysEnv() != config.DevHub {
		t.Errorf("trustHubKeysEnv() = %v in a build with DevHub=%v", trustHubKeysEnv(), config.DevHub)
	}
}

// endorse is a root's endorsement of a Manager key, as the index carries it.
func endorse(root ed25519.PrivateKey, id string, pub ed25519.PublicKey) hubKey {
	return hubKey{ID: id, Public: base64.StdEncoding.EncodeToString(pub),
		Endorsement: base64.StdEncoding.EncodeToString(ed25519.Sign(root, hubKeyEndorsement(id, pub)))}
}

func TestCoreReleaseKeysNeedARoot(t *testing.T) {
	hubPub, _, _ := ed25519.GenerateKey(rand.Reader)
	listed := []hubKey{{ID: "hub-2026", Public: base64.StdEncoding.EncodeToString(hubPub)}}

	// No root key, no opt-in: refused, also by coreUpdateTarget (the Updates screen says why).
	withRootKeys(t)
	withTrustHubKeys(t, false)
	if _, err := coreReleaseKeys(listed); !errors.Is(err, errCoreNoRoot) {
		t.Fatalf("no root: %v", err)
	}
	if _, err := coreUpdateTarget(); !errors.Is(err, errCoreNoRoot) {
		t.Fatalf("coreUpdateTarget without a root: %v", err)
	}
	// The devhub opt-in trusts the listed keys, with or without a root (a local Manager's key).
	withTrustHubKeys(t, true)
	if keys, err := coreReleaseKeys(listed); err != nil || len(keys) != 1 {
		t.Fatalf("opt-in: %v %v", keys, err)
	}
	rootPub, rootPriv, _ := ed25519.GenerateKey(rand.Reader)
	withRootKeys(t, rootPub)
	if keys, err := coreReleaseKeys(listed); err != nil || len(keys) != 1 {
		t.Fatalf("opt-in with a root: %v %v", keys, err)
	}
	if got := pluginHubKeys(listed); len(got) != 1 {
		t.Errorf("plugin hub keys with the opt-in: %d", len(got))
	}

	// Without it, a pinned root makes only endorsed keys count.
	withTrustHubKeys(t, false)
	if _, err := coreReleaseKeys(listed); err == nil {
		t.Fatal("an unendorsed Manager key verifies releases")
	}
	_, otherRoot, _ := ed25519.GenerateKey(rand.Reader)
	forged := endorse(otherRoot, "hub-2026", hubPub)
	good := endorse(rootPriv, "hub-2026", hubPub)
	renamed := good
	renamed.ID = "hub-2099" // the endorsement covers the id
	keys, err := coreReleaseKeys([]hubKey{forged, renamed, good})
	if err != nil || len(keys) != 1 || keys[0].Endorsement != good.Endorsement {
		t.Fatalf("endorsed keys: %v %v", keys, err)
	}
	if err := coreTrustCheck(); err != nil {
		t.Errorf("a pinned root still blocks updates: %v", err)
	}
	// Plugin keys: with a root, only endorsed ones are trusted.
	if got := pluginHubKeys([]hubKey{forged, good}); len(got) != 1 {
		t.Errorf("plugin hub keys with a root: %d", len(got))
	}
	withRootKeys(t)
	if got := pluginHubKeys(listed); len(got) != 1 {
		t.Errorf("plugin hub keys without a root: %d", len(got))
	}
}

func TestHubURLAllowed(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://hub.example.com/api": true, "http://localhost:8090/api": true, "http://127.0.0.1:8090": true,
		"http://[::1]:8090": true, "http://hub.localhost": true, "http://manager.test": true,
		"http://hub.example.com/api": false, "http://10.0.0.5": false, "ftp://hub.example.com": false, "http://localhost.example.com": false,
	} {
		u, _ := url.Parse(raw)
		if err := hubURLAllowed(u); (err == nil) != ok {
			t.Errorf("hubURLAllowed(%s) = %v", raw, err)
		}
	}
	s := &Server{}
	if _, err := s.hubNewRequest(t.Context(), http.MethodGet, "http://hub.example.com/api/v1/index", nil); !errors.Is(err, errHubInsecure) {
		t.Errorf("plain http Manager on a public host: %v", err)
	}
}

// A redirect to another host must not carry the license key or the site URL.
func TestHubRedirectDropsCredentials(t *testing.T) {
	var got http.Header
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	defer other.Close()
	// Same loopback address, other host name: localhost vs 127.0.0.1.
	target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+"/elsewhere", http.StatusFound)
	}))
	defer hub.Close()
	req, _ := http.NewRequest(http.MethodGet, hub.URL+"/api/v1/plugins/x/download/1", nil)
	req.Header.Set("X-Pursae-License", "PRS-SECRET")
	req.Header.Set("X-Pursae-Site", "https://site.example")
	resp, err := (&Server{}).hubClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got == nil {
		t.Fatal("redirect not followed")
	}
	if got.Get("X-Pursae-License") != "" || got.Get("X-Pursae-Site") != "" {
		t.Errorf("credentials sent to another host: %v", got)
	}
}

// hubTrustKeys replaces the Manager's keys (a key it stops listing is no longer trusted) and is
// safe while packages are being verified.
func TestPluginKeySnapshot(t *testing.T) {
	ps := &pluginSystem{}
	site, _, _ := ed25519.GenerateKey(rand.Reader)
	ps.siteKeys = []ed25519.PublicKey{site}
	a, _, _ := ed25519.GenerateKey(rand.Reader)
	b, _, _ := ed25519.GenerateKey(rand.Reader)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				ps.setHubKeys([]ed25519.PublicKey{a, b})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = ps.trusted()
				_ = ps.isHubKey(a)
			}
		}()
	}
	wg.Wait()
	ps.setHubKeys([]ed25519.PublicKey{b}) // a was revoked
	if ps.isHubKey(a) || len(ps.trusted()) != 2 {
		t.Errorf("revoked key still trusted: %d keys", len(ps.trusted()))
	}
	_ = plugins.EncodeKey
}

func TestFaviconAndLogoRules(t *testing.T) {
	s := &Server{settings: settings.New(nil)}
	s.cfg.HubURL = "https://hub.example.com"
	if err := s.hubFetchLogo(t.Context(), "http://cdn.example.net/logo.svg"); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("a third-party http logo: %v", err)
	}
	if err := s.hubFetchLogo(t.Context(), "https://127.0.0.1:1/logo.svg"); err == nil || !strings.Contains(err.Error(), "private network") {
		t.Errorf("a logo on a private address: %v", err)
	}
}
