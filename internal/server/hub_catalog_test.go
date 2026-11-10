package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// TestHubCatalogPagesAndErrors: a catalog spread over several pages is collected whole and cached,
// a changed license key reads the Manager again, and 403/429/5xx answers are errors that are not cached.
func TestHubCatalogPagesAndErrors(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()

	var calls atomic.Int32
	var status atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/plugins" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if st := int(status.Load()); st != 0 {
			w.WriteHeader(st)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var items []map[string]any
		for i := 0; i < 2 && (page-1)*2+i < 5; i++ { // 5 listings, 2 per page
			items = append(items, map[string]any{"slug": fmt.Sprintf("plug-%d", (page-1)*2+i), "name": "P", "version": "1.0.0"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 5, "pages": 3, "items": items})
	}))
	defer hub.Close()
	srv.cfg.HubURL = hub.URL

	items, err := srv.hubCatalog(ctx, "plugins")
	if err != nil || len(items) != 5 || calls.Load() != 3 {
		t.Fatalf("paged catalog: %d items, %d calls, err %v", len(items), calls.Load(), err)
	}
	if items[4].Slug != "plug-4" {
		t.Errorf("last item = %q", items[4].Slug)
	}
	if _, err := srv.hubCatalog(ctx, "plugins"); err != nil || calls.Load() != 3 {
		t.Errorf("the second read was not served from the cache (%d calls)", calls.Load())
	}

	// A new license key has its own cache key, so it reads the Manager again.
	if err := srv.settings.Set(ctx, map[string]any{licenseOption: "PRS-NEW"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.hubCatalog(ctx, "plugins"); err != nil || calls.Load() != 6 {
		t.Errorf("a new key reused the old cache (%d calls)", calls.Load())
	}

	// Error answers surface and are not cached.
	if err := srv.settings.Set(ctx, map[string]any{licenseOption: "PRS-OTHER"}); err != nil {
		t.Fatal(err)
	}
	for _, st := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		status.Store(int32(st))
		_, err := srv.hubCatalog(ctx, "plugins")
		if err == nil || !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(st)) {
			t.Errorf("status %d: err = %v", st, err)
		}
	}
	status.Store(0)
	if items, err := srv.hubCatalog(ctx, "plugins"); err != nil || len(items) != 5 {
		t.Errorf("after the errors: %d items, %v (an error was cached?)", len(items), err)
	}
}

// TestHubBulkUpdatePartialFailure: Update Plugins updates what it can and reports the plugin that
// failed in the same redirect.
func TestHubBulkUpdatePartialFailure(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hub.keys = append(hub.keys, map[string]string{"id": "hub-2026", "public": base64.StdEncoding.EncodeToString(pub)})
	hubInstallWordCounter(t, base, c)
	hub.publish(t, "1.1.0", priv)
	srv.hubDropCaches(ctx)
	_, body, _ := get(t, c, base+"/wp-admin/update-core")
	nonce := formNonce(t, body[strings.Index(body, "upgrade-plugins"):])
	_, _, loc := post(t, c, base+"/wp-admin/update-core?action=do-plugin-upgrade", url.Values{"_wpnonce": {nonce}, "checked[]": {"word-counter", "no-such-plugin"}})
	if !strings.Contains(loc, "plugins-updated=Word+Counter+1.1.0") || !strings.Contains(loc, "no-such-plugin") {
		t.Errorf("partial failure not reported: %s", loc)
	}
}
