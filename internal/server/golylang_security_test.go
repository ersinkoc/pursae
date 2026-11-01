package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Regression tests for the Pursae Multi Language security review.

// Dropping the hidden default language's code from a URL must not turn "/en//evil.example" into the
// protocol-relative redirect "//evil.example".
func TestGolylangDefaultLangRedirectStaysOnSite(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	visitor := asNewClient()
	for _, p := range []string{"/en//evil.example/", "/en///evil.example", "/en/%5Cevil.example", "/en/%5C%5Cevil.example", "/en/%09/evil.example"} {
		code, _, loc := get(t, visitor, base+p)
		if code == http.StatusMovedPermanently && (strings.HasPrefix(loc, "//") || strings.Contains(loc, "\\") || strings.ContainsAny(loc, "\t\r\n")) {
			t.Errorf("%s redirects off site: %d %q", p, code, loc)
		}
	}
	if code, _, loc := get(t, visitor, base+"/en/sample-page/"); code != http.StatusMovedPermanently || loc != "/sample-page/" {
		t.Errorf("default language code not dropped: %d %q", code, loc)
	}
}

// REST translations[lang]=id linked any post into the caller's translation group (and synchronized
// the caller's fields onto it); it needs edit_post on the linked post, like the admin-ajax linking.
func TestGolylangRESTTranslationsNeedEditPost(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	adminPost, _ := pllCreatePost(t, c, base, "?lang=en", "Admin announcement")

	asCreateUser(t, srv, "contrib", "contributor", asStrongPass)
	cc := asNewClient()
	if code, _, _ := asLogin(t, cc, base, "/wp-login", "contrib", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	code, obj, _ := pllREST(t, cc, base, http.MethodPost, "/wp-json/wp/v2/posts?lang=de", map[string]any{"title": "Mein Entwurf", "status": "draft"})
	if code != http.StatusCreated {
		t.Fatalf("contributor draft: %d %v", code, obj)
	}
	own := int64(obj["id"].(float64))
	code, _, _ = pllREST(t, cc, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(own)+"?lang=de&translations[en]="+itoa64(adminPost), map[string]any{"title": "Mein Entwurf"})
	if code != http.StatusOK {
		t.Fatalf("contributor update: %d", code)
	}
	if tr := srv.pllTranslations(ctx, pllPost, adminPost); tr["de"] == own {
		t.Fatalf("contributor linked the administrator's post as a translation: %v", tr)
	}
	// The administrator may link both (edit_post on each).
	code, _, _ = pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(own)+"?lang=de&translations[en]="+itoa64(adminPost), map[string]any{"title": "Mein Entwurf"})
	if tr := srv.pllTranslations(ctx, pllPost, adminPost); code != http.StatusOK || tr["de"] != own {
		t.Errorf("administrator linking: %d %v", code, tr)
	}
}
