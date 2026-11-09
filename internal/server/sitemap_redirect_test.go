package server

import (
	"context"
	"net/http"
	"testing"
)

// /sitemap.xml leads to the core index while the site is public; a hidden site has no sitemap.
func TestCoreSitemapXMLRedirect(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	if err := srv.settings.Set(context.Background(), map[string]any{"blog_public": true}); err != nil {
		t.Fatal(err)
	}
	code, _, loc := get(t, c, ts.URL+"/sitemap.xml")
	if code != http.StatusMovedPermanently || loc != "/wp-sitemap.xml" {
		t.Errorf("/sitemap.xml: %d %q, want 301 to /wp-sitemap.xml", code, loc)
	}
	if code, _, _ = get(t, c, ts.URL+loc); code != http.StatusOK {
		t.Errorf("the index: %d", code)
	}
	// A doubled leading slash must not become a protocol-relative (off-site) Location.
	if code, _, loc = get(t, c, ts.URL+"//sitemap.xml"); code != http.StatusMovedPermanently || loc != "/wp-sitemap.xml" {
		t.Errorf("//sitemap.xml: %d %q, want 301 to /wp-sitemap.xml", code, loc)
	}
	if err := srv.settings.Set(context.Background(), map[string]any{"blog_public": false}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ = get(t, c, ts.URL+"/sitemap.xml"); code != http.StatusNotFound {
		t.Errorf("/sitemap.xml of a hidden site: %d, want 404", code)
	}
}
