package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// Regression tests for the Pursae Commerce security review (feature-plugin code the core audit left
// out of scope).

func gooBasic(k, s string) map[string]string {
	return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(k+":"+s))}
}

// A read-only API key must not write through ?_method= / X-HTTP-Method-Override, and API keys only sign
// in to the store's namespaces (WC_REST_Authentication), never to wp/v2.
func TestGooAPIKeyMethodOverrideAndScope(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Pen", 2, nil)
	rck, rcs, _, _ := srv.gooRESTNewKey(ctx, 1, "Reader", "read")
	wck, wcs, _, _ := srv.gooRESTNewKey(ctx, 1, "Writer", "read_write")
	anon := &http.Client{}
	api := base + "/wp-json"

	if r := gooReq(t, anon, "GET", fmt.Sprintf("%s/wc/v3/products/%d?_method=DELETE&force=true", api, p.ID), nil, gooBasic(rck, rcs)); r.Code != 401 ||
		!strings.Contains(gooJS(r.Body, "message"), "write permissions") {
		t.Errorf("read key, GET ?_method=DELETE: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, anon, "GET", fmt.Sprintf("%s/wc/v3/products/%d?_method=PUT", api, p.ID), map[string]any{"name": "Renamed"}, gooBasic(rck, rcs)); r.Code != 401 {
		t.Errorf("read key, GET ?_method=PUT: %d %s", r.Code, r.Raw)
	}
	hdr := gooBasic(rck, rcs)
	hdr["X-HTTP-Method-Override"] = "DELETE"
	if r := gooReq(t, anon, "POST", fmt.Sprintf("%s/wc/v3/products/%d?force=true", api, p.ID), nil, hdr); r.Code != 401 {
		t.Errorf("read key, POST with X-HTTP-Method-Override: DELETE: %d %s", r.Code, r.Raw)
	}
	if got, err := srv.gooGetProduct(ctx, p.ID); err != nil || got.Name != "Pen" {
		t.Fatalf("product changed by a read-only key: %v %v", got, err)
	}
	if r := gooReq(t, anon, "GET", fmt.Sprintf("%s/wc/v3/products/%d", api, p.ID), nil, gooBasic(rck, rcs)); r.Code != 200 {
		t.Errorf("read key GET: %d %s", r.Code, r.Raw)
	}

	// Outside wc/…, a consumer key is an unknown user name: no users list, no user creation.
	if r := gooReq(t, anon, "GET", api+"/wp/v2/users/me", nil, gooBasic(wck, wcs)); r.Code != 401 {
		t.Errorf("key on wp/v2/users/me: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, anon, "POST", api+"/wp/v2/users", map[string]any{"username": "keyadmin", "password": "Violet-Harbor-Lantern-93!",
		"email": "keyadmin@example.test", "roles": []string{"administrator"}}, gooBasic(wck, wcs)); r.Code != 401 {
		t.Errorf("key on POST wp/v2/users: %d %s", r.Code, r.Raw)
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "keyadmin"); err == nil {
		t.Fatal("a consumer key created a user through wp/v2")
	}
	if r := gooReq(t, anon, "PUT", fmt.Sprintf("%s/wc/v3/products/%d", api, p.ID), map[string]any{"name": "Pen 2"}, gooBasic(wck, wcs)); r.Code != 200 {
		t.Errorf("read_write key PUT on wc/v3: %d %s", r.Code, r.Raw)
	}
}

// A shop manager (manage_woocommerce, no edit_users) may only create, change and revoke their own API
// keys; a key for an administrator would let them act as one.
func TestGooAPIKeyAssignmentNeedsEditUser(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	smID := asCreateUser(t, srv, "manager", "shop_manager", asStrongPass)
	sm := asNewClient()
	if code, _, _ := asLogin(t, sm, base, "/wp-login", "manager", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	page := base + "/wp-admin/goocommerce?page=settings&tab=advanced&section=keys"
	send := func(form url.Values) string {
		t.Helper()
		_, body, _ := get(t, sm, page+"&create-key=1")
		form.Set("_wpnonce", formNonce(t, body))
		code, out, loc := post(t, sm, page, form)
		if code != http.StatusFound && code != http.StatusOK {
			t.Fatalf("POST keys: %d %s", code, out)
		}
		return loc + out
	}
	keys := func(uid int64) int { return asCount(t, `SELECT count(*) FROM goo_api_keys WHERE user_id = $1`, uid) }

	if out := send(url.Values{"key_description": {"steal"}, "key_user": {"1"}, "key_permissions": {"read_write"}}); !strings.Contains(out, "permission") {
		t.Errorf("shop manager minting an admin key: %s", out)
	}
	if keys(1) != 0 {
		t.Fatal("shop manager created an API key for the administrator")
	}
	if out := send(url.Values{"key_description": {"mine"}, "key_user": {itoa64(smID)}, "key_permissions": {"read"}}); !strings.Contains(out, "ck_") {
		t.Errorf("own key not generated: %s", out[:min(len(out), 300)])
	}
	if keys(smID) != 1 {
		t.Fatal("own key missing")
	}

	// An administrator's existing key can be neither reassigned nor revoked by the shop manager.
	_, _, adminKey, _ := srv.gooRESTNewKey(ctx, 1, "Admin integration", "read")
	send(url.Values{"key_id": {itoa64(adminKey)}, "key_description": {"x"}, "key_user": {itoa64(smID)}, "key_permissions": {"read_write"}})
	send(url.Values{"goo_do": {"revoke:" + itoa64(adminKey)}})
	var owner int64
	var perms string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT user_id, permissions FROM goo_api_keys WHERE id = $1`, adminKey).Scan(&owner, &perms); err != nil || owner != 1 || perms != "read" {
		t.Errorf("admin key after shop manager edits: owner %d perms %q err %v", owner, perms, err)
	}
}

// Webhook deliveries refuse loopback / private / metadata addresses, also when a public URL redirects
// there (WooCommerce delivers with wp_safe_remote_request).
func TestGooWebhookRefusesPrivateAddresses(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	var hits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer internal.Close()
	var id int64
	if err := srv.store.Pool.QueryRow(ctx, `INSERT INTO goo_webhooks (name, status, topic, delivery_url, secret) VALUES ('Internal', 'active', 'product.created', $1, 's')
		RETURNING id`, internal.URL+"/admin").Scan(&id); err != nil {
		t.Fatal(err)
	}
	gooMakeSimple(t, srv, "Hooked", 1, nil)
	gooWebhookWG.Wait()
	if hits.Load() != 0 {
		t.Fatal("webhook delivered to a loopback address")
	}
	var failures int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT failures FROM goo_webhooks WHERE id = $1`, id).Scan(&failures)
	if failures != 1 {
		t.Errorf("refused delivery not counted as a failure: %d", failures)
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Errorf("%s treated as public", ip)
		}
	}
}
