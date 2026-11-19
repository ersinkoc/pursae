package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/cache"
)

func TestPageCacheRelativePurgeKeepsPathEncoding(t *testing.T) {
	ts, client, srv := newTestServer(t, false)
	installAndLogin(t, ts, client)
	if err := srv.settings.Set(context.Background(), map[string]any{
		"wpgo_cache_enabled": true, "wpgo_cache_serve": true,
	}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	c := srv.currentCache()
	const encodedPath = "/cat%61log/"
	encodedKey := c.Key(httptest.NewRequest(http.MethodGet, ts.URL+encodedPath, nil))
	plainKey := c.Key(httptest.NewRequest(http.MethodGet, ts.URL+"/catalog/", nil))

	for _, via := range []string{"admin", "REST"} {
		t.Run(via, func(t *testing.T) {
			for _, key := range []string{encodedKey, plainKey} {
				now := time.Now()
				e := &cache.Entry{
					Key: key, Status: http.StatusOK, Body: []byte("cached"),
					Created: now, Expires: now.Add(time.Hour),
				}
				if err := c.Store().Set(e); err != nil {
					t.Fatal(err)
				}
			}
			if via == "admin" {
				code, _, loc := post(t, client, ts.URL+"/wp-admin/admin?page=wpgo-cache", url.Values{
					"action": {"purge_url"}, "purge_url": {encodedPath},
					"_wpnonce": {cacheNonce(t, srv, client, ts.URL, "wpgo-cache-purge")},
				})
				if code != http.StatusFound || !strings.Contains(loc, "Purged") {
					t.Fatalf("admin purge: %d -> %s", code, loc)
				}
			} else {
				code, body := restSend(t, client, ts.URL, http.MethodPost, "/wp-json/pursae/v1/cache/purge",
					`{"url":"/cat%61log/"}`)
				if code != http.StatusOK {
					t.Fatalf("REST purge: %d %s", code, body)
				}
			}
			if _, ok := c.Store().Get(encodedKey); ok {
				t.Error("encoded path survived the purge")
			}
			if _, ok := c.Store().Get(plainKey); !ok {
				t.Error("the separately keyed plain path was purged")
			}
		})
	}
}
