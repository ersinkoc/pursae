package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseURLDetails(t *testing.T) {
	base, _ := url.Parse("https://example.test/blog/post")
	html := `<!doctype html><html><head>
<meta charset="iso-8859-1">
<title>  Caf` + "\xe9" + ` &amp; Co
 news </title>
<link rel="stylesheet" href="/style.css">
<link rel="shortcut icon" href="/favicon.ico">
<link rel="icon" href="/other.png">
<meta property="og:image" content="javascript:alert(1)">
<meta name="description" content="A &lt;b&gt;short&lt;/b&gt;  description">
<meta property="og:description" content="Second description">
</head><body><title>Not this</title><meta property="og:image" content="/late.png"></body></html>`
	d := parseURLDetails([]byte(html), "text/html", base)
	if d.Title != "Café & Co news" {
		t.Errorf("title %q", d.Title)
	}
	if d.Icon != "https://example.test/favicon.ico" {
		t.Errorf("icon %q", d.Icon)
	}
	if d.Description != "A short description" {
		t.Errorf("description %q", d.Description)
	}
	if d.Image != "" {
		t.Errorf("a javascript: image or one outside the head was kept: %q", d.Image)
	}
	d = parseURLDetails([]byte(`<head><meta property="og:description" content="OG"><meta property="og:image" content="img/a.jpg"></head>`), "text/html; charset=utf-8", base)
	if d.Description != "OG" || d.Image != "https://example.test/blog/img/a.jpg" || d.Title != "" || d.Icon != "" {
		t.Errorf("og fallbacks: %+v", d)
	}
}

func TestURLDetailsFetchable(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://example.com/":        true,
		"http://example.com:8080/x":   true,
		"http://example.com:22/":      false,
		"ftp://example.com/":          false,
		"https://user:pw@example.com": false,
		"https:///path":               false,
	} {
		u, _ := url.Parse(raw)
		if got := urlDetailsFetchable(u); got != want {
			t.Errorf("%s: %v, want %v", raw, got, want)
		}
	}
}

func TestRESTURLDetails(t *testing.T) {
	var hits atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><head><title>Remote page</title><meta name="description" content="About the page">
<link rel="icon" href="/favicon.png"><meta property="og:image" content="/cover.jpg"></head><body>` + strings.Repeat("x", 400<<10) + `</body></html>`))
		case "/moved":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/empty":
			w.Header().Set("Content-Type", "text/html")
		case "/image":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG...."))
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	s := secStart(t)
	get := func(rc *restClient, target string) (int, map[string]any) {
		t.Helper()
		code, m, _, _ := rc.do("GET", "/wp-block-editor/v1/url-details?url="+url.QueryEscape(target), nil)
		return code, m
	}

	// Private addresses are refused when the connection is made (the test hook is off).
	urlDetailsAllowPrivate.Store(false)
	for _, target := range []string{"http://127.0.0.1/page", "http://localhost/", "http://[::1]:80/", "http://10.0.0.1:8080/", "http://169.254.169.254/latest/meta-data"} {
		if code, m := get(s.admin, target); code != http.StatusInternalServerError || m["code"] != "http_request_failed" {
			t.Errorf("%s: %d %v", target, code, m)
		}
	}
	if code, m := get(s.admin, remote.URL+"/page"); code != http.StatusBadRequest || m["code"] != "rest_invalid_param" {
		t.Errorf("a port other than 80/443/8080: %d %v", code, m)
	}
	if hits.Load() != 0 {
		t.Fatalf("the remote server was reached %d times with the hook off", hits.Load())
	}

	urlDetailsAllowPrivate.Store(true)
	defer urlDetailsAllowPrivate.Store(false)

	code, m := get(s.admin, remote.URL+"/page/")
	if code != http.StatusOK {
		t.Fatalf("page: %d %v", code, m)
	}
	if m["title"] != "Remote page" || m["description"] != "About the page" || m["icon"] != remote.URL+"/favicon.png" || m["image"] != remote.URL+"/cover.jpg" {
		t.Errorf("page details: %v", m)
	}
	// Cached for an hour by URL (the trailing slash does not count).
	before := hits.Load()
	if code, m2 := get(s.admin, remote.URL+"/page"); code != http.StatusOK || m2["title"] != "Remote page" || hits.Load() != before {
		t.Errorf("not served from the cache: %d %v (hits %d → %d)", code, m2, before, hits.Load())
	}
	if code, m := get(s.admin, remote.URL+"/moved"); code != http.StatusOK || m["title"] != "Remote page" {
		t.Errorf("redirect: %d %v", code, m)
	}
	if code, m := get(s.admin, remote.URL+"/loop"); code != http.StatusInternalServerError || m["code"] != "http_request_failed" {
		t.Errorf("redirect loop: %d %v", code, m)
	}
	if code, m := get(s.admin, remote.URL+"/missing"); code != http.StatusNotFound || m["code"] != "no_response" {
		t.Errorf("404 page: %d %v", code, m)
	}
	if code, m := get(s.admin, remote.URL+"/empty"); code != http.StatusNotFound || m["code"] != "no_content" {
		t.Errorf("empty page: %d %v", code, m)
	}
	if code, m := get(s.admin, remote.URL+"/image"); code != http.StatusOK || m["title"] != "" || m["icon"] != "" {
		t.Errorf("not a web page: %d %v", code, m)
	}

	// Bad input.
	if code, m, _, _ := s.admin.do("GET", "/wp-block-editor/v1/url-details", nil); code != http.StatusBadRequest || m["code"] != "rest_missing_callback_param" {
		t.Errorf("no url: %d %v", code, m)
	}
	for _, target := range []string{"mailto:a@example.test", "gopher://example.com/", "not a url"} {
		if code, m := get(s.admin, target); code != http.StatusBadRequest || m["code"] != "rest_invalid_param" {
			t.Errorf("%q: %d %v", target, code, m)
		}
	}

	// edit_posts is required.
	if code, m := get(s.anon(), remote.URL+"/page"); code != http.StatusUnauthorized || m["code"] != "rest_cannot_view_url_details" {
		t.Errorf("anonymous: %d %v", code, m)
	}
	_, sub := s.user("subby", "subscriber")
	if code, m := get(sub, remote.URL+"/page"); code != http.StatusForbidden || m["code"] != "rest_cannot_view_url_details" {
		t.Errorf("subscriber: %d %v", code, m)
	}
	_, contrib := s.user("conny", "contributor")
	if code, m := get(contrib, remote.URL+"/page"); code != http.StatusOK || m["title"] != "Remote page" {
		t.Errorf("contributor: %d %v", code, m)
	}
}
