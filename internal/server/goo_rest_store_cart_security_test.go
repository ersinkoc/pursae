package server

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"testing"
)

// A logged-in customer's Cart-Token (session key u:<id>) is bound to that user's auth session:
// another client cannot use it, and it stops working after logout.
func TestGooStoreCartTokenUserSessionBoundToAuth(t *testing.T) {
	base, admin, srv := gooSite(t)
	gooShopSetup(t, srv)
	p := gooMakeSimple(t, srv, "Mug", 10, nil)
	api := base + "/wp-json/wc/store/v1"

	r := gooReq(t, admin, "GET", api+"/cart", nil, nil)
	token := r.Header.Get("Cart-Token")
	if r.Code != 200 || token == "" {
		t.Fatalf("GET cart as admin: %d %q", r.Code, token)
	}
	// The owner may use it.
	if r := gooReq(t, admin, "POST", api+"/cart/add-item", map[string]any{"id": p.ID, "quantity": 1}, map[string]string{"Cart-Token": token}); r.Code != 201 {
		t.Fatalf("owner add-item with Cart-Token: %d %s", r.Code, r.Raw)
	}
	// A cookieless client holding the token gets neither the session nor a nonce bypass.
	if r := gooReq(t, &http.Client{}, "POST", api+"/cart/add-item", map[string]any{"id": p.ID}, map[string]string{"Cart-Token": token}); r.Code != 401 {
		t.Errorf("stolen user Cart-Token from another client: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, &http.Client{}, "GET", api+"/cart", nil, map[string]string{"Cart-Token": token}); gooJ(r.Body, "items_count") != float64(0) {
		t.Errorf("stolen user Cart-Token reads the customer's cart: %s", r.Raw)
	}

	// Log out, log back in: the old token belongs to a dead auth session.
	_, body, _ := get(t, admin, base+"/wp-admin/")
	m := regexp.MustCompile(`(/wp-login\?action=logout&amp;_wpnonce=[0-9a-f]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no logout link")
	}
	get(t, admin, base+html.UnescapeString(m[1]))
	if code, _, _ := post(t, admin, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("re-login: %d", code)
	}
	if r := gooReq(t, admin, "POST", api+"/cart/add-item", map[string]any{"id": p.ID}, map[string]string{"Cart-Token": token}); r.Code != 401 {
		t.Errorf("Cart-Token issued before logout still accepted: %d %s", r.Code, r.Raw)
	}
}
