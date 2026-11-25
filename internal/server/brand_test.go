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
	darkSVG := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1" fill="#fff"/></svg>`
	const mark = "/wp-includes/images/brand/pursae-mark-white.svg"
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
		case "/media/logo-dark.svg":
			w.Write([]byte(darkSVG))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hub.Close()
	ctx := context.Background()
	srv.cfg.HubURL = hub.URL // the Manager URL is fixed in production builds

	_, shipped, _ := get(t, c, base+"/wp-includes/images/brand/logo-64.png")
	if !strings.HasPrefix(shipped, "\x89PNG") {
		t.Fatal("the shipped logo is not served")
	}
	// Browsers must not keep the shipped logo longer than a Hub logo, which replaces it at the
	// same URL.
	for _, p := range []string{"/wp-includes/images/brand/logo-64.png", "/favicon.ico"} {
		if resp, err := c.Head(base + p); err != nil || resp.Header.Get("Cache-Control") != "public, max-age=3600" {
			t.Errorf("%s: Cache-Control %q, want public, max-age=3600", p, resp.Header.Get("Cache-Control"))
		}
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
		if !strings.HasPrefix(resp.Header.Get("Content-Security-Policy"), "sandbox") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: the Hub's SVG is served without the sandbox CSP and nosniff", p)
		}
	}

	// Without a dark-mode logo the admin bar's mark keeps the shipped light mark: the Hub's
	// light-mode logo is a dark mark, invisible on the dark admin bar.
	if _, body, _ := get(t, c, base+mark); body == svg || !strings.Contains(body, "<svg") {
		t.Errorf("admin bar mark without a dark logo: %q", body)
	}
	// With one, the mark shows it and every other file keeps the light-mode logo.
	brand = map[string]any{"logo_url": "/media/logo.svg", "logo_version": "1",
		"logo_dark_url": "/media/logo-dark.svg", "logo_dark_version": "d1"}
	srv.hubRefresh(ctx)
	if _, body, _ := get(t, c, base+mark); body != darkSVG {
		t.Errorf("admin bar mark with a dark logo: %q", body)
	}
	if _, body, _ := get(t, c, base+"/wp-includes/images/brand/logo-256.png"); body != svg {
		t.Errorf("login logo with a dark logo: %q", body)
	}
	// Dropping the dark logo brings the shipped mark back, not the light-mode logo.
	brand = map[string]any{"logo_url": "/media/logo.svg", "logo_version": "1"}
	srv.hubRefresh(ctx)
	if _, body, _ := get(t, c, base+mark); body == svg || body == darkSVG {
		t.Errorf("admin bar mark after the dark logo is dropped: %q", body)
	}

	brand = nil
	srv.hubRefresh(ctx)
	if _, body, _ := get(t, c, base+"/wp-includes/images/brand/logo-64.png"); body != shipped {
		t.Error("without brand.logo_url the shipped logo does not come back")
	}
}
