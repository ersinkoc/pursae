package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrandSniff(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	for in, want := range map[string]string{
		string(png): "image/png",
		`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`: "image/svg+xml",
		"<html><body>not a logo</body></html>":                           "",
	} {
		if got := brandSniff([]byte(in)); got != want {
			t.Errorf("brandSniff(%.20q) = %q, want %q", in, got, want)
		}
	}
}

func TestLegacyUploadPath(t *testing.T) {
	if got := legacyUploadPath("/wp-content/uploads/" + legacyBuilderUploadsDir + "/fonts/a.woff2"); got != "/wp-content/uploads/web-builder/fonts/a.woff2" {
		t.Errorf("legacy upload path: %s", got)
	}
	if got := legacyUploadPath("/wp-content/uploads/2026/10/a.png"); got != "/wp-content/uploads/2026/10/a.png" {
		t.Errorf("other uploads changed: %s", got)
	}
}

// The Hub replaces the shipped logo on every screen; dropping brand.logo_url brings it back.
func TestHubBrandLogo(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`
	brand := map[string]any{"logo_url": "/media/logo.svg", "logo_version": "1"}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/index":
			idx := map[string]any{"hub": "Test Hub", "api": 1}
			if brand != nil {
				idx["brand"] = brand
			}
			json.NewEncoder(w).Encode(idx)
		case "/media/logo.svg":
			w.Write([]byte(svg))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hub.Close()
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_hub_url": hub.URL}); err != nil {
		t.Fatal(err)
	}

	_, shipped, _ := get(t, c, base+"/wp-includes/images/brand/logo-64.png")
	if !strings.HasPrefix(shipped, "\x89PNG") {
		t.Fatal("the shipped logo is not served")
	}
	srv.hubRefresh(ctx)
	for _, p := range []string{"/wp-includes/images/brand/logo-64.png", "/wp-includes/images/brand/logo-192.png", "/favicon.ico"} {
		resp, err := c.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
			t.Errorf("%s: Content-Type %q, want the Hub's SVG", p, ct)
		}
	}

	brand = nil
	srv.hubRefresh(ctx)
	if _, body, _ := get(t, c, base+"/wp-includes/images/brand/logo-64.png"); body != shipped {
		t.Error("without brand.logo_url the shipped logo does not come back")
	}
}
