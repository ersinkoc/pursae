package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"wpgo/internal/cache"
)

func TestMemoryHostStorageDeleteAndList(t *testing.T) {
	h := NewMemoryHost()
	ctx := context.Background()
	for _, k := range []string{"order:3", "order:1", "order:2", "cfg"} {
		_ = h.StorageSet(ctx, "p", k, "v")
	}
	keys, _ := h.StorageList(ctx, "p", "order:", "", 2)
	if len(keys) != 2 || keys[0] != "order:1" || keys[1] != "order:2" {
		t.Fatalf("page 1 = %v", keys)
	}
	keys, _ = h.StorageList(ctx, "p", "order:", "order:2", 10)
	if len(keys) != 1 || keys[0] != "order:3" {
		t.Fatalf("page 2 = %v", keys)
	}
	_ = h.StorageDelete(ctx, "p", "order:1")
	if _, ok, _ := h.StorageGet(ctx, "p", "order:1"); ok {
		t.Fatal("key survived storage_delete")
	}
	if err := h.StorageDelete(ctx, "p", "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRouteHeaders(t *testing.T) {
	got, err := checkRouteHeaders([]string{"stripe-signature", "X-Hub-Signature-256"})
	if err != nil || got[0] != "Stripe-Signature" || got[1] != "X-Hub-Signature-256" {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range [][]string{{"Cookie"}, {"authorization"}, {"Bad Header"}, {""}} {
		if _, err := checkRouteHeaders(bad); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
}

func TestServeRestContentTypeValidation(t *testing.T) {
	// ServeRest validates after the guest call; exercise the allow-list directly.
	for ct, want := range map[string]bool{"text/csv": true, "application/xml": true, "text/javascript": false, "application/json": false} {
		if RestContentTypes[ct] != want {
			t.Errorf("RestContentTypes[%q] = %v", ct, RestContentTypes[ct])
		}
	}
	var r RestResponse
	if err := json.Unmarshal([]byte(`{"status":200,"body":"a,b","content_type":"text/csv"}`), &r); err != nil || r.ContentType != "text/csv" {
		t.Fatalf("decode: %v %+v", err, r)
	}
}

// A page rendered while a plugin callback was skipped is incomplete and must not be cached.
func TestDegradedRenderIsNotCached(t *testing.T) {
	r := httptest.NewRequest("GET", "/p/", nil)
	r = cache.WithDegradedFlag(r)
	if cache.Degraded(r) {
		t.Fatal("flag set too early")
	}
	cache.MarkDegraded(r.Context())
	if !cache.Degraded(r) {
		t.Fatal("MarkDegraded did not stick")
	}
	cache.MarkDegraded(context.Background()) // no flag: must not panic
	_ = http.StatusOK
}
