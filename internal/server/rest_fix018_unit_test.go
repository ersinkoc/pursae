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
