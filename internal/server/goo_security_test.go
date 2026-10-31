package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"wpgo/internal/store"
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

// Adding MaxInt64 to a cart line twice used to wrap its quantity to -1: a negative line total, a free or
// discounted order, and stock added back on payment.
func TestGooCartQuantityCannotOverflow(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	z := gooMakeSimple(t, srv, "Z", 90, nil)
	y := gooMakeSimple(t, srv, "Y", 100, nil)
	cart := srv.gooCartFor(gooTestCtx())
	key, err := cart.Add(ctx, z.ID, 1, 0, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := cart.Add(ctx, z.ID, math.MaxInt64, 0, nil, nil, nil); err == nil {
			t.Fatal("MaxInt64 quantity accepted")
		}
	}
	if _, err := cart.Add(ctx, z.ID, gooMaxLineQty, 0, nil, nil, nil); err == nil {
		t.Fatal("line pushed over the quantity cap")
	}
	if err := cart.SetQuantity(ctx, key, math.MaxInt64); err == nil {
		t.Fatal("SetQuantity accepted MaxInt64")
	}
	if _, err := cart.Add(ctx, y.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if it := cart.Item(key); it == nil || it.Quantity != 1 {
		t.Fatalf("line Z after overflow attempts: %+v", it)
	}
	if cart.Total < 190 {
		t.Fatalf("cart total %v, want 190", cart.Total)
	}

	// A session that already holds a negative quantity (forged or from before the cap) loses that line.
	cart.sess.Data.Cart[0].Quantity = -1
	again := &gooCart{s: srv, c: cart.c, sess: cart.sess}
	again.load(ctx)
	again.Calculate()
	if len(again.Items) != 1 || again.Items[0].ProductID != y.ID || again.Total < 100 {
		t.Fatalf("negative line kept: %d items, total %v", len(again.Items), again.Total)
	}
}

// Private products are purchasable only by users who may edit them (is_purchasable()); visitors guessing
// IDs with ?add-to-cart= used to buy them.
func TestGooPrivateProductNotForSale(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	secret := gooMakeSimple(t, srv, "Unreleased", 5, func(p *gooProduct) { p.Status = "private" })
	open := gooMakeSimple(t, srv, "Released", 5, nil)

	guest := srv.gooCartFor(gooTestCtx())
	if _, err := guest.Add(ctx, secret.ID, 1, 0, nil, nil, nil); err == nil {
		t.Fatal("a visitor added a private product")
	}
	if _, err := guest.Add(ctx, open.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	g := gooGuest()
	get(t, g, fmt.Sprintf("%s/?add-to-cart=%d", base, secret.ID))
	if _, body, _ := get(t, g, base+"/cart/"); strings.Contains(body, "Unreleased") {
		t.Fatal("private product in a visitor's cart through ?add-to-cart=")
	}

	admin, err := srv.store.UserByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	c.Set(ctxUser, admin)
	staff := srv.gooCartFor(c)
	if _, err := staff.Add(ctx, secret.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatalf("shop staff cannot buy their own private product: %v", err)
	}
	// The line disappears when the same session is loaded for a visitor.
	again := &gooCart{s: srv, c: gooTestCtx(), sess: staff.sess}
	again.load(ctx)
	if len(again.Items) != 0 {
		t.Fatalf("private product kept in a visitor's cart: %d items", len(again.Items))
	}
}

// An individual-use coupon cannot be paired with another one by applying them in two sessions and
// logging in (the guest cart's coupons are merged into the customer's).
func TestGooIndividualUseCouponSurvivesSessionMerge(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Mug", 100, nil)
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "solo", DiscountType: "percent", Amount: 50, IndividualUse: true}); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "extra", DiscountType: "percent", Amount: 40}); err != nil {
		t.Fatal(err)
	}
	cart := srv.gooCartFor(gooTestCtx())
	if _, err := cart.Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := cart.ApplyCoupon(ctx, "solo"); err != nil {
		t.Fatal(err)
	}
	if err := cart.ApplyCoupon(ctx, "extra"); err == nil {
		t.Fatal("second coupon applied next to an individual-use one")
	}
	// What the login merge produces: both codes in the session.
	cart.sess.Data.AppliedCoupons = []string{"extra", "solo"}
	merged := &gooCart{s: srv, c: gooTestCtx(), sess: cart.sess}
	merged.load(ctx)
	merged.Calculate()
	if strings.Join(merged.AppliedCoupons, ",") != "solo" || merged.Total < 50 {
		t.Fatalf("coupons after merge %v, total %v", merged.AppliedCoupons, merged.Total)
	}
}

// With "Allow customers to create an account during checkout" off, a posted createaccount=1 must not
// open an account (WC_Checkout::get_posted_data() ignores the box then).
func TestGooCheckoutCreateAccountHonoursSetting(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	gooShopSetup(t, srv)
	p := gooMakeSimple(t, srv, "Mug", 10, nil)
	if err := srv.gooSetOpts(ctx, map[string]any{"enable_guest_checkout": "yes", "enable_signup_and_login_from_checkout": "no"}); err != nil {
		t.Fatal(err)
	}
	v := gooFrontClient()
	get(t, v, base+"/?add-to-cart="+itoa64(p.ID))
	_, body, _ := get(t, v, base+"/checkout/")
	form := gooFrontCheckoutForm(gooHidden(t, body, "woocommerce-process-checkout-nonce"))
	form.Set("createaccount", "1")
	form.Set("account_username", "sneaky")
	form.Set("account_password", asStrongPass)
	code, _, loc := post(t, v, base+"/checkout/", form)
	if code != http.StatusFound || !strings.Contains(loc, "/checkout/order-received/") {
		t.Fatalf("guest checkout: %d %s", code, loc)
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "sneaky"); err == nil {
		t.Fatal("checkout created an account although sign-up at checkout is off")
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "ada@example.test"); err == nil {
		t.Fatal("checkout created an account for the billing e-mail although sign-up at checkout is off")
	}
}

// Memberships restricts a post's content on its page; the REST API (content.rendered, the_content in
// WordPress) and the excerpt must not hand the same content to non-members.
func TestGooMembershipsRestrictRESTContent(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	news := &store.Post{Type: "post", Title: "Members digest", Slug: "members-digest", Content: "<p>The members-only recipe is here.</p>",
		Excerpt: "A teaser.", Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	open := &store.Post{Type: "post", Title: "Open notes", Slug: "open-notes", Content: "<p>Open words [goocommerce_members_only]hidden bonus[/goocommerce_members_only].</p>",
		Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	for _, p := range []*store.Post{news, open} {
		if err := srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	plan := &gooMembershipPlan{Name: "Velvet", AccessMethod: "manual", AccessLength: "unlimited", Rules: gmemRules{ContentPostIDs: []int64{news.ID}}}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetExtension(ctx, gmemKey, true); err != nil {
		t.Fatal(err)
	}
	anon := gooGuest()
	r := gooReq(t, anon, "GET", fmt.Sprintf("%s/wp-json/wp/v2/posts/%d", base, news.ID), nil, nil)
	if r.Code != 200 || strings.Contains(r.Raw, "members-only recipe") || !strings.Contains(gooJS(r.Body, "content", "rendered"), "wc-memberships-content-restricted-message") {
		t.Errorf("restricted post over REST: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, anon, "GET", base+"/wp-json/wp/v2/posts?per_page=50", nil, nil); strings.Contains(r.Raw, "members-only recipe") || strings.Contains(r.Raw, "hidden bonus") {
		t.Errorf("restricted content in the REST list: %s", r.Raw)
	}
	// Without "show excerpts" the excerpt goes too; the front-end archive excerpt drops the shortcode text.
	if err := srv.gooSetOpts(ctx, map[string]any{"ext_" + gmemKey + "_settings": map[string]any{"show_excerpts": "no"}}); err != nil {
		t.Fatal(err)
	}
	if r := gooReq(t, anon, "GET", fmt.Sprintf("%s/wp-json/wp/v2/posts/%d", base, news.ID), nil, nil); strings.Contains(r.Raw, "A teaser.") {
		t.Errorf("excerpt shown without show_excerpts: %s", gooJS(r.Body, "excerpt", "rendered"))
	}
	if _, body, _ := get(t, anon, base+"/feed/"); strings.Contains(body, "hidden bonus") {
		t.Error("members-only shortcode text in the feed")
	}
	r = gooReq(t, anon, "GET", fmt.Sprintf("%s/wp-json/wp/v2/posts/%d", base, open.ID), nil, nil)
	if !strings.Contains(r.Raw, "Open words") || strings.Contains(r.Raw, "hidden bonus") {
		t.Errorf("members-only shortcode over REST: %s", r.Raw)
	}
}

// My Account › Gift Cards matched cards by the account's e-mail, which a customer can change without
// verification: pointing it at a recipient's address revealed the recipient's codes.
func TestGooGiftCardCodesNotShownByEmailMatch(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	g := &gcCard{Initial: 50, Recipient: "victim@example.test", Delivered: true}
	if err := srv.gcCreate(ctx, g, 0, "test"); err != nil {
		t.Fatal(err)
	}
	attacker := gxaCustomer(t, srv, "victim@example.test") // an account that took the recipient's address
	c := gooTestCtx()
	c.Set(ctxUser, attacker)
	out := string(gcAccount(srv, c, ""))
	if strings.Contains(out, g.Code) || !strings.Contains(out, gcMaskCode(g.Code)) {
		t.Fatalf("recipient-matched card code shown: %s", out)
	}
	if strings.Contains(out, "50.00") {
		t.Errorf("recipient-matched card balance shown: %s", out)
	}
	if m := gcMaskCode("ABCD-EFGH-IJKL-MNOP"); m != "XXXX-XXXX-XXXX-MNOP" {
		t.Errorf("mask %q", m)
	}
}

// A failed order can still be paid (Pay for order) with its gift card discount, so failing it must not
// credit the card back: that let one balance pay for the order and then be spent again. Cancelling does.
func TestGooGiftCardKeptOnFailedOrder(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	lamp := gooMakeSimple(t, srv, "Lamp", 150, func(p *gooProduct) { p.Virtual = true })
	card := &gcCard{Initial: 100, Recipient: "r@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, card, 1, "test"); err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	if _, err := cart.Add(ctx, lamp.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gcApply(c, card.Code); err != nil {
		t.Fatal(err)
	}
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	if err := srv.gooSetStatus(ctx, o, "failed", "Payment declined.", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 0 {
		t.Fatalf("card credited back on a failed order that can still be paid: %v", g.Balance)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if err := srv.gooSetStatus(ctx, o, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 100 {
		t.Errorf("card after the failed order was cancelled: %v", g.Balance)
	}
}

// Two checkouts at once must not both spend one gift card balance or one points balance: the debit is
// taken atomically before the order is saved, and the second checkout fails.
func TestGooGiftCardAndPointsCannotBeDoubleSpent(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	for _, k := range []string{gcKey, gptsKey} {
		if err := srv.gooSetExtension(ctx, k, true); err != nil {
			t.Fatal(err)
		}
	}
	card := &gcCard{Initial: 100, Recipient: "r@example.test"}
	if err := srv.gcCreate(ctx, card, 1, "test"); err != nil {
		t.Fatal(err)
	}
	cust := gxaCustomer(t, srv, "points@example.test")
	if err := srv.gptsLog(ctx, cust.ID, 100, "admin-adjustment", 0, ""); err != nil {
		t.Fatal(err)
	}
	order := func() *gooOrder {
		return &gooOrder{CustomerID: cust.ID, Meta: map[string]any{"_wc_points_redeemed": float64(100)},
			Items: []*gooOrderItem{{Type: "fee", Name: "Gift card", Quantity: 1, Total: -100, Meta: map[string]any{"_fee_id": gcFeePref + card.Code}}}}
	}
	var wg sync.WaitGroup
	var gcOK, ptsOK atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := gcReserve(srv, ctx, nil, order()); err == nil {
				gcOK.Add(1)
			}
			if _, err := gptsReserve(srv, ctx, nil, order()); err == nil {
				ptsOK.Add(1)
			}
		}()
	}
	wg.Wait()
	if gcOK.Load() != 1 || ptsOK.Load() != 1 {
		t.Fatalf("concurrent checkouts that spent the balance: gift card %d, points %d (want 1 each)", gcOK.Load(), ptsOK.Load())
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 0 || g.Status != "redeemed" {
		t.Errorf("card after one debit: %v %s", g.Balance, g.Status)
	}
	if b := srv.gptsBalance(ctx, cust.ID); b != 0 {
		t.Errorf("points balance %d", b)
	}

	// A checkout that fails after the reservation gives the balance back.
	card2 := &gcCard{Initial: 100, Recipient: "r@example.test"}
	_ = srv.gcCreate(ctx, card2, 1, "test")
	o := order()
	o.Items[0].Meta["_fee_id"] = gcFeePref + card2.Code
	release, err := gcReserve(srv, ctx, nil, o)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if g, _ := srv.gcGet(ctx, card2.ID); g.Balance != 100 || g.Status != "active" {
		t.Errorf("card after release: %v %s", g.Balance, g.Status)
	}
}
