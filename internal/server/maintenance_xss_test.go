package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestPllCleanTranslation(t *testing.T) {
	admin := &store.User{ID: 1, Caps: map[string]bool{"unfiltered_html": true}}
	tr := &store.User{ID: 2, Caps: map[string]bool{"translate_fr": true}}
	if got := pllCleanTranslation(admin, "<script>x</script>"); got != "<script>x</script>" {
		t.Errorf("unfiltered_html: %q", got)
	}
	for _, u := range []*store.User{tr, nil} {
		if got := pllCleanTranslation(u, "<script>alert(1)</script><p>ok</p>"); strings.Contains(got, "<script") || !strings.Contains(got, "<p>ok</p>") {
			t.Errorf("not cleaned: %q", got)
		}
		// Plain text is kept as typed: no &amp; for a translation shown as text.
		for _, v := range []string{"Tom & Jerry", "a < b", "Café"} {
			if got := pllCleanTranslation(u, v); got != v {
				t.Errorf("plain %q became %q", v, got)
			}
		}
	}
}

// maintPllSite is a maintenance site with Pursae Multi Language on (en default, fr) and the page
// closed, plus a logged-in user who may translate into French only.
func maintPllSite(t *testing.T) (base string, admin, translator *http.Client, srv *Server) {
	t.Helper()
	ts, c, srv := maintenanceSite(t)
	base = ts.URL
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	maintEdit(t, srv, func(st *maintSettings) {
		st.Enabled = true
		st.Content = "<p>We will be back soon.</p>"
		st.FooterText = "Footer note"
	})
	id := asCreateUser(t, srv, "fr-translator", "subscriber", asStrongPass)
	if err := srv.store.SetUserCapExceptions(context.Background(), id, map[string]string{"translate_fr": "allow"}); err != nil {
		t.Fatal(err)
	}
	translator = asNewClient()
	if code, _, _ := asLogin(t, translator, base, "/wp-login", "fr-translator", asStrongPass); code != http.StatusFound {
		t.Fatalf("login translator: %d", code)
	}
	return base, c, translator, srv
}

func maintPllSaveString(t *testing.T, c *http.Client, base, orig, tr string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {formNonce(t, body)},
		"original[0]": {orig}, "translation[fr][0]": {tr}}); code != http.StatusFound {
		t.Fatalf("save strings: %d", code)
	}
}

const maintXSS = "<script>alert(1)</script><p>ok</p>"

// A translator without unfiltered_html cannot plant script on the public maintenance page through
// the translation of its text (Polylang runs wp_kses_post on string translations).
func TestMaintenanceTranslatedTextIsKsesed(t *testing.T) {
	base, admin, translator, srv := maintPllSite(t)
	ctx := context.Background()
	const orig = "<p>We will be back soon.</p>"

	maintPllSaveString(t, translator, base, orig, maintXSS)
	if got := srv.pllStringMap(ctx, "fr")[orig]; strings.Contains(got, "<script") || !strings.Contains(got, "<p>ok</p>") {
		t.Fatalf("stored translation %q", got)
	}
	page := bodyOf(anonGet(t, base+"/fr/"))
	if !strings.Contains(page, "<p>ok</p>") || strings.Contains(page, "<script>alert(1)") {
		t.Fatal("the French maintenance page prints the translator's script")
	}

	// Plain text is stored as typed (no &amp;).
	maintPllSaveString(t, translator, base, "Footer note", "Tom & Jerry")
	if got := srv.pllStringMap(ctx, "fr")["Footer note"]; got != "Tom & Jerry" {
		t.Errorf("plain translation stored as %q", got)
	}

	// The visual translator REST save is cleaned the same way.
	if code, obj, _ := pllREST(t, translator, base, http.MethodPost, "/wp-json/pll/v1/visual/strings",
		map[string]any{"lang": "fr", "items": []any{map[string]any{"original": orig, "translation": maintXSS}}}); code != http.StatusOK {
		t.Fatalf("visual save: %d %v", code, obj)
	}
	if got := srv.pllStringMap(ctx, "fr")[orig]; strings.Contains(got, "<script") || !strings.Contains(got, "<p>ok</p>") {
		t.Fatalf("visual save stored %q", got)
	}

	// An administrator holds unfiltered_html: the markup is kept, as in WordPress.
	maintPllSaveString(t, admin, base, orig, maintXSS)
	if got := srv.pllStringMap(ctx, "fr")[orig]; got != maintXSS {
		t.Errorf("administrator's translation stored as %q", got)
	}
}

// Machine translation output is untrusted: it is kses'd whoever runs the fill.
func TestPllMTFillCleansProviderOutput(t *testing.T) {
	base, _, _, srv := maintPllSite(t)
	ctx := context.Background()
	deepl := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text []string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var tr []map[string]string
		for _, s := range in.Text {
			tr = append(tr, map[string]string{"text": "<script>alert(1)</script>FR " + s})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"translations": tr})
	}))
	defer deepl.Close()
	oldBase, oldClient := pllMTDeepLFreeBase, pllMTClient
	pllMTDeepLFreeBase, pllMTClient = deepl.URL, deepl.Client()
	defer func() { pllMTDeepLFreeBase, pllMTClient = oldBase, oldClient }()
	o := srv.pllOpts()
	o.MT, o.MTProvider, o.MTKey = true, "deepl", "abc-123:fx"
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}
	_ = bodyOf(anonGet(t, base+"/fr/")) // registers the maintenance strings
	n, err := srv.pllMTFillStrings(ctx, srv.pllLang(ctx, "fr"))
	if err != nil || n == 0 {
		t.Fatalf("fill: %d %v", n, err)
	}
	m := srv.pllStringMap(ctx, "fr")
	if len(m) == 0 {
		t.Fatal("nothing stored")
	}
	for k, v := range m {
		if strings.Contains(v, "<script") {
			t.Errorf("machine translation of %q stored with script: %q", k, v)
		}
	}
}

// The maintenance admin screen: manage_options without unfiltered_html cannot store raw markup.
func TestMaintenanceAdminSaveNeedsUnfilteredHTML(t *testing.T) {
	ts, c, srv := maintenanceSite(t)
	base := ts.URL
	id := asCreateUser(t, srv, "no-unfiltered", "administrator", asStrongPass)
	if err := srv.store.SetUserCapExceptions(context.Background(), id, map[string]string{"unfiltered_html": "deny"}); err != nil {
		t.Fatal(err)
	}
	lim := asNewClient()
	if code, _, _ := asLogin(t, lim, base, "/wp-login", "no-unfiltered", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	maintEdit(t, srv, func(st *maintSettings) { st.CustomHTML = "<!-- kept -->" })
	maintSaveTab(t, lim, base, "general", url.Values{"content": {maintXSS}, "footer_text": {"<script>f()</script>Footer"}})
	st := srv.maintLoad()
	if strings.Contains(st.Content, "<script") || !strings.Contains(st.Content, "<p>ok</p>") || strings.Contains(st.FooterText, "<script") {
		t.Errorf("content %q footer %q", st.Content, st.FooterText)
	}
	maintSaveTab(t, lim, base, "modules", url.Values{"custom_html": {"<script>x()</script>"}})
	if got := srv.maintLoad().CustomHTML; got != "<!-- kept -->" {
		t.Errorf("custom HTML replaced without unfiltered_html: %q", got)
	}
	// The administrator keeps raw HTML.
	maintSaveTab(t, c, base, "general", url.Values{"content": {maintXSS}})
	maintSaveTab(t, c, base, "modules", url.Values{"custom_html": {"<script>x()</script>"}})
	if st := srv.maintLoad(); st.Content != maintXSS || st.CustomHTML != "<script>x()</script>" {
		t.Errorf("administrator: content %q custom %q", st.Content, st.CustomHTML)
	}
}
