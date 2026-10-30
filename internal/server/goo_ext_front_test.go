package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// gooXCRequest is a gin context for a front request (form posted when non-nil), with cookies and a user.
func gooXCRequest(method, target string, form url.Values, cookies []*http.Cookie, u *store.User) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	c.Request = httptest.NewRequest(method, target, body)
	if form != nil {
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, ck := range cookies {
		c.Request.AddCookie(ck)
	}
	if u != nil {
		c.Set(ctxUser, u)
	}
	return c, rec
}

// gooXCCookies merges the cookies a response set into a jar (deleted ones removed).
func gooXCCookies(jar []*http.Cookie, rec *httptest.ResponseRecorder) []*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range jar {
		out[c.Name] = c
	}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(out, c.Name)
		} else {
			out[c.Name] = &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	var list []*http.Cookie
	for _, c := range out {
		list = append(list, c)
	}
	return list
}

func gooXCDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return m
}

// gooXCRoute runs the module front routes like the storefront does.
func gooXCRoute(srv *Server, c *gin.Context) bool {
	path := strings.Trim(c.Request.URL.Path, "/")
	for _, r := range gooFrontRoutes {
		if r(srv, c, path) {
			return true
		}
	}
	return false
}

func TestGooExtCompare(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Material"}); err != nil {
		t.Fatal(err)
	}
	a := gooMakeSimple(t, srv, "Oak Chair", 120, func(p *gooProduct) {
		p.SKU, p.Weight, p.Length, p.Width, p.Height = "OAK-1", "7.5", "50", "45", "90"
		p.Attributes = []gooProductAttr{{Name: "Finish", Options: []string{"Matte"}, Visible: true}}
	})
	b := gooMakeSimple(t, srv, "Pine Chair", 80, func(p *gooProduct) { p.SKU = "PINE-1"; p.SalePrice = gooFloatPtr(70) })
	d := gooMakeSimple(t, srv, "Birch Chair", 90, nil)

	// Off: no buttons, no route.
	if h := srv.gooHTML(gooTestCtx(), "loop_after_item", a); strings.Contains(string(h), "goo-compare") {
		t.Error("compare button while off")
	}
	if c, _ := gooXCRequest("GET", "/?goo-ajax=compare_toggle&product_id="+itoa64(a.ID), nil, nil, nil); gooXCRoute(srv, c) {
		t.Error("compare route while off")
	}

	gooExtSwitch(t, srv, gooCmpKey, true)
	if srv.gooPageID("compare") == 0 {
		t.Fatal("no Compare products page after activation")
	}
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_compare_settings": map[string]any{"max_products": float64(2), "button_text": "Compare", "show_in_loops": true}})

	var jar []*http.Cookie
	toggle := func(id int64) map[string]any {
		c, rec := gooXCRequest("POST", "/?goo-ajax=compare_toggle", url.Values{"product_id": {itoa64(id)}}, jar, nil)
		if !gooXCRoute(srv, c) {
			t.Fatal("compare route did not answer")
		}
		jar = gooXCCookies(jar, rec)
		return gooXCDecode(t, rec)
	}
	if m := toggle(a.ID); m["in_compare"] != true || m["count"].(float64) != 1 {
		t.Fatalf("add a: %v", m)
	}
	if m := toggle(b.ID); m["in_compare"] != true || m["count"].(float64) != 2 || !strings.Contains(m["bar"].(string), "Compare now") {
		t.Fatalf("add b: %v", m)
	}
	if m := toggle(d.ID); m["success"] != false || !strings.Contains(m["message"].(string), "up to 2") {
		t.Errorf("max products: %v", m)
	}

	// The next request sees the list: buttons, bar, table.
	c, _ := gooXCRequest("GET", "/compare/", nil, jar, nil)
	if h := string(srv.gooHTML(c, "single_summary_end", a)); !strings.Contains(h, "goo-compare-button added") || !strings.Contains(h, `aria-pressed="true"`) {
		t.Errorf("single button: %s", h)
	}
	if h := string(srv.gooPageHTML(c, "footer")); !strings.Contains(h, "goo-compare-bar") || !strings.Contains(h, "Oak Chair") || !strings.Contains(h, "(2)") {
		t.Errorf("footer bar: %s", h)
	}
	table := srv.gooFilterContent(c, "<p>[goocommerce_compare]</p>")
	for _, want := range []string{"goo-compare-table", "OAK-1", "PINE-1", "7.5 kg", "50 × 45 × 90 cm", "Finish", "Matte", "<del", "add-to-cart=" + itoa64(a.ID), "In stock"} {
		if !strings.Contains(table, want) {
			t.Errorf("compare table lacks %q", want)
		}
	}
	// No-JS remove and clear.
	c, rec := gooXCRequest("POST", "/compare/", url.Values{"goo_module": {"compare"}, "goo_compare_action": {"remove"}, "product_id": {itoa64(a.ID)}}, jar, nil)
	if !gooXCRoute(srv, c) || c.Writer.Status() != http.StatusSeeOther {
		t.Fatalf("remove form: %d", c.Writer.Status())
	}
	jar = gooXCCookies(jar, rec)
	c, _ = gooXCRequest("GET", "/", nil, jar, nil)
	if ids := srv.gooCmpList(c); len(ids) != 1 || ids[0] != b.ID {
		t.Errorf("after remove: %v", ids)
	}
	c, rec = gooXCRequest("POST", "/", url.Values{"goo_module": {"compare"}, "goo_compare_action": {"clear"}}, jar, nil)
	gooXCRoute(srv, c)
	jar = gooXCCookies(jar, rec)
	c, _ = gooXCRequest("GET", "/", nil, jar, nil)
	if ids := srv.gooCmpList(c); len(ids) != 0 {
		t.Errorf("after clear: %v", ids)
	}
	if out := srv.gooFilterContent(c, "[goocommerce_compare]"); !strings.Contains(out, "No products to compare") {
		t.Errorf("empty compare page: %s", out)
	}
}

func TestGooExtQuickView(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Linen Shirt", 35, func(p *gooProduct) { p.SKU = "LIN-1" })
	p.Post.Excerpt = "<p>Breathable summer linen.</p>"
	_ = srv.gooSaveProduct(ctx, p, 1)
	if h := srv.gooHTML(gooTestCtx(), "loop_after_item", p); strings.Contains(string(h), "goo-quick-view") {
		t.Error("quick view button while off")
	}
	if c, _ := gooXCRequest("GET", "/?goo-ajax=quick_view&product_id="+itoa64(p.ID), nil, nil, nil); gooXCRoute(srv, c) {
		t.Error("quick view route while off")
	}
	gooExtSwitch(t, srv, gooQVKey, true)
	if h := string(srv.gooHTML(gooTestCtx(), "loop_after_item", p)); !strings.Contains(h, "goo-quick-view-button") || !strings.Contains(h, `data-product_id="`+itoa64(p.ID)+`"`) || !strings.Contains(h, "Quick view") {
		t.Errorf("loop button: %s", h)
	}
	footer := string(srv.gooPageHTML(gooTestCtx(), "footer"))
	for _, want := range []string{`role="dialog"`, `aria-modal="true"`, "ext-quick-view.js", "ext-quick-view.css", "goo-ajax=quick_view", "data-goo-qv-close"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer lacks %q", want)
		}
	}
	c, rec := gooXCRequest("GET", "/?goo-ajax=quick_view&product_id="+itoa64(p.ID), nil, nil, nil)
	if !gooXCRoute(srv, c) || rec.Code != http.StatusOK {
		t.Fatalf("quick view: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Linen Shirt", `id="goo-quick-view-title"`, "35.00", "Breathable summer linen", "single_add_to_cart_button", `name="add-to-cart" value="` + itoa64(p.ID), "LIN-1", "View full details"} {
		if !strings.Contains(body, want) {
			t.Errorf("quick view lacks %q", want)
		}
	}
	// A variable product gets the variation picker.
	_ = srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Size"})
	for _, n := range []string{"S", "M"} {
		_ = srv.store.InsertTerm(ctx, termFor("pa_size", n))
	}
	v := gooNewProduct(gooTypeVariable)
	v.Name = "Linen Trousers"
	v.Attributes = []gooProductAttr{{Taxonomy: "pa_size", Options: []string{"s", "m"}, Visible: true, Variation: true}}
	_ = srv.gooSaveProduct(ctx, v, 1)
	for i, sz := range []string{"s", "m"} {
		vv := gooNewProduct(gooTypeVariation)
		vv.ParentID, vv.Name, vv.RegularPrice, vv.VariationAttrs = v.ID, "Linen Trousers - "+sz, gooFloatPtr(float64(40+i*5)), map[string]string{"pa_size": sz}
		_ = srv.gooSaveProduct(ctx, vv, 1)
	}
	c, rec = gooXCRequest("GET", "/?goo-ajax=quick_view&product_id="+itoa64(v.ID), nil, nil, nil)
	gooXCRoute(srv, c)
	for _, want := range []string{"variations_form", `name="attribute_pa_size"`, "Choose an option", "data-product_variations", "variation_id", "40.00"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("variable quick view lacks %q", want)
		}
	}
	c, rec = gooXCRequest("GET", "/?goo-ajax=quick_view&product_id=999999", nil, nil, nil)
	if gooXCRoute(srv, c); rec.Code != http.StatusNotFound {
		t.Errorf("missing product: %d", rec.Code)
	}
	// Mobile setting.
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_quick_view_settings": map[string]any{"show_on_mobile": false, "button_text": "Peek"}})
	if h := string(srv.gooHTML(gooTestCtx(), "loop_after_item", p)); !strings.Contains(h, "goo-quick-view-hide-mobile") || !strings.Contains(h, "Peek") {
		t.Errorf("settings not applied: %s", h)
	}
}

func TestGooExtWishlist(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Silk Scarf", 50, nil)
	q := gooMakeSimple(t, srv, "Wool Hat", 25, nil)
	if h := srv.gooHTML(gooTestCtx(), "single_summary_end", p); strings.Contains(string(h), "goo-wishlist") {
		t.Error("wishlist button while off")
	}
	if c, _ := gooXCRequest("POST", "/?goo-ajax=wishlist_toggle", url.Values{"product_id": {itoa64(p.ID)}}, nil, nil); gooXCRoute(srv, c) {
		t.Error("wishlist route while off")
	}
	gooExtSwitch(t, srv, gooWLKey, true)
	if srv.gooPageID("wishlist") == 0 {
		t.Fatal("no Wishlist page after activation")
	}
	guestNonce := srv.nonce(gooTestCtx(), gooWLNonce)

	// A guest adds a product through AJAX.
	var jar []*http.Cookie
	c, rec := gooXCRequest("POST", "/?goo-ajax=wishlist_toggle", url.Values{"product_id": {itoa64(p.ID)}, "_wpnonce": {guestNonce}}, nil, nil)
	if !gooXCRoute(srv, c) {
		t.Fatal("wishlist route did not answer")
	}
	if m := gooXCDecode(t, rec); m["success"] != true || m["added"] != true || m["count"].(float64) != 1 {
		t.Fatalf("toggle: %v", m)
	}
	jar = gooXCCookies(jar, rec)
	hasCookie := false
	for _, ck := range jar {
		if ck.Name == gooWLCookie {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Fatal("no wishlist key cookie")
	}
	// Bad nonce.
	c, rec = gooXCRequest("POST", "/?goo-ajax=wishlist_toggle", url.Values{"product_id": {itoa64(q.ID)}, "_wpnonce": {"bad"}}, jar, nil)
	gooXCRoute(srv, c)
	if m := gooXCDecode(t, rec); m["success"] != false {
		t.Errorf("bad nonce accepted: %v", m)
	}
	// No-JS add of the second product.
	c, rec = gooXCRequest("POST", "/product/wool-hat/", url.Values{"goo_module": {"wishlist"}, "goo_wishlist_action": {"add"}, "product_id": {itoa64(q.ID)}, "_wpnonce": {guestNonce}}, jar, nil)
	if !gooXCRoute(srv, c) || c.Writer.Status() != http.StatusSeeOther {
		t.Fatalf("form add: %d", c.Writer.Status())
	}
	jar = gooXCCookies(jar, rec)

	// The wishlist page shows both; the heart is filled; a price drop shows.
	q.RegularPrice = gooFloatPtr(20)
	_ = srv.gooSaveProduct(ctx, q, 1)
	c, _ = gooXCRequest("GET", "/wishlist/", nil, jar, nil)
	page := srv.gooFilterContent(c, "[goocommerce_wishlist]")
	for _, want := range []string{"goo-wishlist-table", "Silk Scarf", "Wool Hat", "Price dropped", "Add to cart", "This wishlist is private"} {
		if !strings.Contains(page, want) {
			t.Errorf("wishlist page lacks %q", want)
		}
	}
	if strings.Count(page, "Price dropped") != 1 {
		t.Errorf("price drop badges: %d", strings.Count(page, "Price dropped"))
	}
	if h := string(srv.gooHTML(c, "single_summary_end", p)); !strings.Contains(h, "goo-wishlist-button") || !strings.Contains(h, `aria-pressed="true"`) {
		t.Errorf("button state: %s", h)
	}
	w := srv.gooWLDefault(c, false)
	if w == nil {
		t.Fatal("no default list")
	}
	// Private lists are not visible by link; shared ones are.
	other, _ := gooXCRequest("GET", "/wishlist/?goo-wishlist="+w.Token, nil, nil, nil)
	if out := srv.gooFilterContent(other, "[goocommerce_wishlist]"); !strings.Contains(out, "private or does not exist") {
		t.Errorf("private list visible to others: %.300s", out)
	}
	c, rec = gooXCRequest("POST", "/wishlist/", url.Values{"goo_module": {"wishlist"}, "goo_wishlist_action": {"update"}, "wishlist": {w.Token},
		"wishlist_title": {"Birthday"}, "wishlist_privacy": {"shared"}, "_wpnonce": {guestNonce}}, jar, nil)
	gooXCRoute(srv, c)
	jar = gooXCCookies(jar, rec)
	other, _ = gooXCRequest("GET", "/wishlist/?goo-wishlist="+w.Token, nil, nil, nil)
	if out := srv.gooFilterContent(other, "[goocommerce_wishlist]"); !strings.Contains(out, "Birthday") || !strings.Contains(out, "Silk Scarf") || strings.Contains(out, "goo_wishlist_action\" value=\"remove") {
		t.Errorf("shared list for others: %.500s", out)
	}
	// The share link redirects to the wishlist page.
	c, rec = gooXCRequest("GET", "/?goo-wishlist="+w.Token, nil, nil, nil)
	if !gooXCRoute(srv, c) || rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/wishlist/?goo-wishlist="+w.Token) {
		t.Errorf("share link: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// Someone else cannot change the list.
	c, rec = gooXCRequest("POST", "/wishlist/", url.Values{"goo_module": {"wishlist"}, "goo_wishlist_action": {"remove"}, "wishlist": {w.Token},
		"product_id": {itoa64(p.ID)}, "_wpnonce": {guestNonce}}, nil, nil)
	gooXCRoute(srv, c)
	if items, _ := srv.gooWLItems(ctx, w.ID); len(items) != 2 {
		t.Errorf("a stranger changed the list: %d items", len(items))
	}

	// Move to cart.
	c, rec = gooXCRequest("POST", "/wishlist/", url.Values{"goo_module": {"wishlist"}, "goo_wishlist_action": {"move_to_cart"}, "wishlist": {w.Token},
		"product_id": {itoa64(q.ID)}, "variation_id": {"0"}, "_wpnonce": {guestNonce}}, jar, nil)
	gooXCRoute(srv, c)
	jar = gooXCCookies(jar, rec)
	c, _ = gooXCRequest("GET", "/", nil, jar, nil)
	if cart := srv.gooCartFor(c); cart.Count() != 1 || cart.Items[0].ProductID != q.ID {
		t.Errorf("cart after move: %d", cart.Count())
	}
	if items, _ := srv.gooWLItems(ctx, w.ID); len(items) != 1 {
		t.Errorf("items after move to cart: %d", len(items))
	}

	// Logging in merges the guest list into the customer's.
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "wlbuyer", Email: "wl@example.test", PasswordHash: "x", DisplayName: "WL Buyer", Role: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	user, _ := srv.store.UserByID(ctx, uid)
	c, rec = gooXCRequest("GET", "/my-account/wishlist/", nil, jar, user)
	lists := srv.gooWLLists(c)
	if len(lists) != 1 || lists[0].UserID != uid || lists[0].Count != 1 {
		t.Fatalf("merged lists: %+v", lists)
	}
	acct := string(srv.gooWLAccount(c, ""))
	if !strings.Contains(acct, "Create a new wishlist") || !strings.Contains(acct, "Birthday") {
		t.Errorf("my account wishlist: %.400s", acct)
	}
	var guests int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_wishlists WHERE user_id = 0`).Scan(&guests)
	if guests != 0 {
		t.Errorf("guest lists left after merge: %d", guests)
	}

	// Require login.
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_wishlist_settings": map[string]any{"require_login": true}})
	c, rec = gooXCRequest("POST", "/?goo-ajax=wishlist_toggle", url.Values{"product_id": {itoa64(q.ID)}, "_wpnonce": {guestNonce}}, nil, nil)
	gooXCRoute(srv, c)
	if m := gooXCDecode(t, rec); m["login_required"] != true {
		t.Errorf("require login: %v", m)
	}

	// Admin screen.
	gooAdminPageOK(t, client, base+"/wp-admin/goocommerce?page=wishlists", "Silk Scarf")
}
