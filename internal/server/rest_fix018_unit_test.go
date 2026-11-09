package server

import (
	"testing"
	"time"
)

func TestBatchRouteDetection(t *testing.T) {
	for _, p := range []string{"/batch/v1", "//batch/v1", "///batch/v1/", "/BATCH/v1", "/%62atch/v1", "/batch/v1?x=1", "/batch"} {
		if !batchRoute(p) {
			t.Errorf("%q not detected as the batch route", p)
		}
	}
	for _, p := range []string{"/wp/v2/posts", "/batches/v1", "/wp/v2/batch"} {
		if batchRoute(p) {
			t.Errorf("%q detected as the batch route", p)
		}
	}
}

func TestOAuthRegisterLimiter(t *testing.T) {
	key := "203.0.113.77-limiter-test"
	now := time.Now()
	for i := 0; i < oauthRegisterLimit; i++ {
		if !oauthRegisterAllow(key, now) {
			t.Fatalf("registration %d refused", i+1)
		}
	}
	if oauthRegisterAllow(key, now) {
		t.Fatal("registration over the limit allowed")
	}
	if !oauthRegisterAllow(key, now.Add(oauthRegisterWindow+time.Second)) {
		t.Fatal("registration refused after the window")
	}
}

func TestOAuthRegisterKeyAndGlobalLimit(t *testing.T) {
	if a, b := oauthRegisterKey("2001:db8:1:2::1"), oauthRegisterKey("2001:db8:1:2:ffff:ee:dd:cc"); a != b {
		t.Errorf("same /64 gives different keys: %q %q", a, b)
	}
	if a, b := oauthRegisterKey("2001:db8:1:2::1"), oauthRegisterKey("2001:db8:1:3::1"); a == b {
		t.Errorf("different /64 share a key: %q", a)
	}
	if k := oauthRegisterKey("203.0.113.9"); k != "203.0.113.9" {
		t.Errorf("IPv4 key changed: %q", k)
	}
	if k := oauthRegisterKey("::ffff:203.0.113.9"); k != "::ffff:203.0.113.9" {
		t.Errorf("IPv4-mapped key changed: %q", k)
	}

	oauthRegisterLimiter.Lock()
	saved, savedG := oauthRegisterLimiter.hits, oauthRegisterLimiter.global
	oauthRegisterLimiter.hits, oauthRegisterLimiter.global = map[string]oauthRegisterHits{}, oauthRegisterHits{}
	oauthRegisterLimiter.Unlock()
	defer func() {
		oauthRegisterLimiter.Lock()
		oauthRegisterLimiter.hits, oauthRegisterLimiter.global = saved, savedG
		oauthRegisterLimiter.Unlock()
	}()

	now := time.Now()
	// Two addresses in one /64 share the per-address budget.
	for i := 0; i < oauthRegisterLimit; i++ {
		addr := "2001:db8:aa:bb::1"
		if i%2 == 1 {
			addr = "2001:db8:aa:bb::2"
		}
		if !oauthRegisterAllow(oauthRegisterKey(addr), now) {
			t.Fatalf("registration %d refused", i+1)
		}
	}
	if oauthRegisterAllow(oauthRegisterKey("2001:db8:aa:bb::99"), now) {
		t.Fatal("a second address in the same /64 escapes the limit")
	}
	// Rotating addresses run into the global ceiling.
	allowed := oauthRegisterLimit
	for i := 0; i < oauthRegisterGlobalLimit; i++ {
		if oauthRegisterAllow("198.51.100."+itoa64(int64(i)), now) {
			allowed++
		}
	}
	if allowed != oauthRegisterGlobalLimit {
		t.Errorf("allowed %d registrations in one window, want %d", allowed, oauthRegisterGlobalLimit)
	}
	if !oauthRegisterAllow("198.51.100.250", now.Add(oauthRegisterWindow+time.Second)) {
		t.Error("registration refused after the global window")
	}
}
