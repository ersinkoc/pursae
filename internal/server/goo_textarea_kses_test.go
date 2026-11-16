package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/caps"
	"wpgo/internal/store"
)

// Store textarea settings are printed unescaped on public pages: a user without unfiltered_html
// (a shop manager) has their markup filtered, plain text is kept byte for byte, and an
// administrator keeps raw HTML.
func TestGooKsesTextarea(t *testing.T) {
	mgr := &store.User{ID: 2, Caps: caps.Grants{"manage_woocommerce": true}}
	admin := &store.User{ID: 1, Caps: caps.Grants{"unfiltered_html": true}}
	in := `<p>Members <strong>only</strong></p><script>steal()</script><img src=x onerror="steal()">`
	got := gooKsesTextarea(mgr, in)
	if strings.Contains(got, "<script") || strings.Contains(got, "onerror") || !strings.Contains(got, "<strong>only</strong>") {
		t.Fatalf("manager: %q", got)
	}
	if gooKsesTextarea(admin, in) != in {
		t.Fatal("administrator's markup changed")
	}
	if plain := "Rates & fees: 5 %\nline 2"; gooKsesTextarea(mgr, plain) != plain {
		t.Fatal("plain text changed")
	}
}

// The settings screen filters the textarea of a shop manager whose unfiltered_html was taken away
// (the default role has it, from the editor's, as in WooCommerce).
func TestGooSettingsTextareaKses(t *testing.T) {
	base, _, srv := gooSite(t)
	id := asCreateUser(t, srv, "txtmgr", "shop_manager", asStrongPass)
	if err := srv.store.SetUserCapExceptions(context.Background(), id, map[string]string{"unfiltered_html": "deny"}); err != nil {
		t.Fatal(err)
	}
	sm := asNewClient()
	if code, _, _ := asLogin(t, sm, base, "/wp-login", "txtmgr", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	page := base + "/wp-admin/goocommerce?page=settings&tab=account"
	_, body, _ := get(t, sm, page)
	if !strings.Contains(body, "woocommerce_registration_privacy_policy_text") {
		t.Fatal("no privacy text field on the Accounts tab")
	}
	post(t, sm, page, url.Values{"_wpnonce": {formNonce(t, body)}, "save": {"Save changes"},
		"woocommerce_registration_privacy_policy_text": {`Read our <a href="/privacy">policy</a>.<script>steal()</script>`}})
	if v := srv.gooOptString("registration_privacy_policy_text"); strings.Contains(v, "<script") || !strings.Contains(v, `<a href="/privacy">policy</a>`) {
		t.Fatalf("stored: %q", v)
	}
}
