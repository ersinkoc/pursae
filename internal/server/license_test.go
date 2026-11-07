package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLicenseFreshAndHash(t *testing.T) {
	h := licenseHash("PRS-1234")
	if len(h) != 16 || h != licenseHash("PRS-1234") || h == licenseHash("PRS-1235") {
		t.Errorf("licenseHash = %q", h)
	}
	var none *licenseStatus
	if none.fresh(h) {
		t.Error("nil status is fresh")
	}
	now := time.Now()
	for _, tc := range []struct {
		name string
		l    licenseStatus
		want bool
	}{
		{"just fetched", licenseStatus{KeyHash: h, Fetched: now}, true},
		{"another key", licenseStatus{KeyHash: licenseHash("other"), Fetched: now}, false},
		{"older than the TTL", licenseStatus{KeyHash: h, Fetched: now.Add(-licenseTTL - time.Minute)}, false},
		{"valid answer an hour old", licenseStatus{KeyHash: h, Valid: true, Fetched: now.Add(-time.Hour)}, true},
		{"unreachable, within the retry", licenseStatus{KeyHash: h, Error: "unreachable", Fetched: now.Add(-licenseRetry / 2)}, true},
		{"unreachable, past the retry", licenseStatus{KeyHash: h, Error: "unreachable", Fetched: now.Add(-licenseRetry - time.Minute)}, false},
	} {
		if got := tc.l.fresh(h); got != tc.want {
			t.Errorf("%s: fresh = %v", tc.name, got)
		}
	}
}

// TestLicenseFetchStates: no key, Manager off, an unreachable Manager (not cached in the database,
// re-asked after licenseRetry), an invalid answer without a code, and a key that cannot be saved.
func TestLicenseFetchStates(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()

	if l := srv.license(ctx); l.Error != "missing" || l.Valid {
		t.Errorf("no key: %+v", l)
	}
	if err := srv.settings.Set(ctx, map[string]any{licenseOption: " PRS-TEST ", "wpgo_hub_url": "off"}); err != nil {
		t.Fatal(err)
	}
	if srv.licenseKey() != "PRS-TEST" {
		t.Errorf("licenseKey = %q", srv.licenseKey())
	}
	srv.licenseForget(ctx)
	if l := srv.license(ctx); l.Error != "off" {
		t.Errorf("Manager off: %+v", l)
	}

	// A Manager that fails, then answers valid=false without a code.
	var calls atomic.Int32
	var mode atomic.Value
	mode.Store("down")
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/license" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if r.Header.Get("X-Pursae-License") != "PRS-TEST" {
			t.Errorf("license header = %q", r.Header.Get("X-Pursae-License"))
		}
		if mode.Load() == "down" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"valid": false})
	}))
	defer hub.Close()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_hub_url": hub.URL}); err != nil {
		t.Fatal(err)
	}
	srv.licenseForget(ctx)
	if l := srv.license(ctx); l.Error != "unreachable" || !strings.Contains(l.Message, "could not be reached") {
		t.Fatalf("Manager down: %+v", l)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM transients WHERE key = $1`, licenseCacheKey).Scan(&n)
	if n != 0 {
		t.Error("an unreachable answer was cached in the database")
	}
	if l := srv.license(ctx); l.Error != "unreachable" || calls.Load() != 1 {
		t.Errorf("the unreachable answer is not kept in memory for the retry window: %d calls", calls.Load())
	}
	if srv.licensePro() || srv.licenseBeta() {
		t.Error("pro/beta without a valid license")
	}
	// Past the retry window the Manager is asked again.
	l := srv.mgr.license.Load()
	old := *l
	old.Fetched = time.Now().Add(-licenseRetry - time.Minute)
	srv.mgr.license.Store(&old)
	mode.Store("invalid")
	if l := srv.license(ctx); l.Error != "unknown" || l.Valid || calls.Load() != 2 {
		t.Errorf("invalid answer without a code: %+v (%d calls)", l, calls.Load())
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM transients WHERE key = $1`, licenseCacheKey).Scan(&n)
	if n != 1 {
		t.Error("a definite answer was not cached")
	}

	// The screen refuses a key with a line break and keeps the old one.
	_, body, _ := get(t, c, ts.URL+"/wp-admin/options-license")
	code, _, loc := post(t, c, ts.URL+"/wp-admin/options-license", url.Values{"_wpnonce": {formNonce(t, body)}, licenseOption: {"PRS-A\nPRS-B"}})
	if code != http.StatusFound || !strings.Contains(loc, "error=invalid") || srv.licenseKey() != "PRS-TEST" {
		t.Errorf("multi-line key: %d %s, key now %q", code, loc, srv.licenseKey())
	}
	if code, _, loc := post(t, c, ts.URL+"/wp-admin/options-license", url.Values{"_wpnonce": {"bad"}, licenseOption: {"PRS-X"}}); code == http.StatusFound && !strings.Contains(loc, "error") {
		t.Errorf("saved without a valid nonce: %d %s", code, loc)
	}
	if srv.licenseKey() != "PRS-TEST" {
		t.Errorf("key changed without a valid nonce: %q", srv.licenseKey())
	}
}
