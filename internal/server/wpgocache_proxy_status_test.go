package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCLICachePurgeReportsProxyFailure(t *testing.T) {
	ts, client, srv := newTestServer(t, false)
	installAndLogin(t, ts, client)
	cfg := srv.cfg
	cfg.DatabaseURL = os.Getenv("WPGO_TEST_DB")

	for _, tc := range []struct {
		name   string
		status int
	}{
		{"accepted", http.StatusNoContent},
		{"rejected", http.StatusMethodNotAllowed},
		{"method-changing redirect", http.StatusFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/final" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if tc.status == http.StatusFound {
					http.Redirect(w, r, "/final", tc.status)
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer proxy.Close()
			if err := srv.settings.Set(context.Background(), map[string]any{
				"wpgo_cache_server": "generic", "wpgo_cache_purge_url": proxy.URL,
			}); err != nil {
				t.Fatal(err)
			}

			var out, errs bytes.Buffer
			code := CLI(context.Background(), cfg, "pursae", []string{"cache", "purge", "--tag=home"},
				strings.NewReader(""), &out, &errs)
			if tc.status == http.StatusNoContent {
				if code != 0 || !strings.Contains(out.String(), "Purge sent") {
					t.Fatalf("accepted purge: exit %d, stdout %q, stderr %q", code, out.String(), errs.String())
				}
				return
			}
			if code == 0 || !strings.Contains(errs.String(), fmt.Sprintf("HTTP %d", tc.status)) || strings.Contains(out.String(), "Purge sent") {
				t.Fatalf("failed purge: exit %d, stdout %q, stderr %q", code, out.String(), errs.String())
			}
		})
	}
}
