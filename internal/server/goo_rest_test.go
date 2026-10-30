package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"wpgo/internal/store"
)

// gooResp is a decoded REST response.
type gooResp struct {
	Code   int
	Header http.Header
	Body   any
	Raw    string
}

func (r gooResp) obj() map[string]any { m, _ := r.Body.(map[string]any); return m }
func (r gooResp) list() []any         { l, _ := r.Body.([]any); return l }

// gooReq sends a JSON request and decodes the answer.
func gooReq(t *testing.T, c *http.Client, method, u string, body any, hdr map[string]string) gooResp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, u, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := gooResp{Code: resp.StatusCode, Header: resp.Header, Raw: string(raw)}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

// gooJ walks a decoded JSON value: keys for objects, ints for arrays.
func gooJ(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			l, _ := v.([]any)
			if k >= len(l) {
				return nil
			}
			v = l[k]
		}
	}
	return v
}

func gooJS(v any, path ...any) string { return gooStr(gooJ(v, path...)) }

func gooGuest() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// gooAdminAPI returns a wc/v3 caller authenticated as the logged-in admin (cookie + X-WP-Nonce).
func gooAdminAPI(t *testing.T, base string, c *http.Client) func(method, path string, body any) gooResp {
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	return func(method, path string, body any) gooResp {
		t.Helper()
		return gooReq(t, c, method, base+"/wp-json/wc/v3"+path, body, map[string]string{"X-WP-Nonce": strings.TrimSpace(nonce)})
	}
}

func gooShopSetup(t *testing.T, srv *Server) (zoneID int64) {
	t.Helper()
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}, "default_country": "US:CA"}); err != nil {
		t.Fatal(err)
	}
	z := &gooZone{Name: "United States", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	if err := srv.gooSaveZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"}); err != nil {
		t.Fatal(err)
	}
	return z.ID
}

var gooTestAddress = map[string]any{"first_name": "Ann", "last_name": "Lee", "address_1": "1 Main St", "city": "Los Angeles", "state": "CA",
	"postcode": "90210", "country": "US", "phone": "555-1234", "email": "ann@example.test"}

func TestGooAPIStoreCartFlow(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	gooShopSetup(t, srv)
	p := gooMakeSimple(t, srv, "Mug", 10, nil)
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "ten", DiscountType: "percent", Amount: 10}); err != nil {
		t.Fatal(err)
	}
	guest := gooGuest()
	api := base + "/wp-json/wc/store/v1"

	r := gooReq(t, guest, "GET", api+"/cart", nil, nil)
	if r.Code != 200 || r.Header.Get("Nonce") == "" || r.Header.Get("Cart-Token") == "" {
		t.Fatalf("GET cart: %d nonce=%q token=%q %s", r.Code, r.Header.Get("Nonce"), r.Header.Get("Cart-Token"), r.Raw)
	}
	nonce := map[string]string{"Nonce": r.Header.Get("Nonce")}
	if r := gooReq(t, guest, "POST", api+"/cart/add-item", map[string]any{"id": p.ID, "quantity": 2}, nil); r.Code != 401 || gooJS(r.Body, "code") != "woocommerce_rest_missing_nonce" {
		t.Fatalf("add-item without nonce: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, guest, "POST", api+"/cart/add-item", map[string]any{"id": p.ID, "quantity": 2}, map[string]string{"Nonce": "bogus"}); r.Code != 403 {
		t.Fatalf("add-item with a bad nonce: %d", r.Code)
	}
	r = gooReq(t, guest, "POST", api+"/cart/add-item", map[string]any{"id": p.ID, "quantity": 2}, nonce)
	if r.Code != 201 {
		t.Fatalf("add-item: %d %s", r.Code, r.Raw)
	}
	if got := gooJS(r.Body, "totals", "total_items"); got != "2000" {
		t.Errorf("total_items = %q, want 2000", got)
	}
	if gooJ(r.Body, "totals", "currency_minor_unit") != float64(2) || gooJS(r.Body, "totals", "currency_code") != "USD" {
		t.Errorf("currency fields: %v", gooJ(r.Body, "totals"))
	}
	if gooJS(r.Body, "items", 0, "prices", "price") != "1000" || gooJ(r.Body, "items", 0, "quantity") != float64(2) {
		t.Errorf("item: %v", gooJ(r.Body, "items", 0))
	}
	key := gooJS(r.Body, "items", 0, "key")

	r = gooReq(t, guest, "POST", api+"/cart/apply-coupon", map[string]any{"code": "ten"}, nonce)
	if r.Code != 200 || gooJS(r.Body, "totals", "total_discount") != "200" || gooJS(r.Body, "coupons", 0, "code") != "ten" {
		t.Fatalf("apply-coupon: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, guest, "POST", api+"/cart/apply-coupon", map[string]any{"code": "nope"}, nonce); r.Code != 400 || gooJS(r.Body, "code") != "woocommerce_rest_cart_coupon_error" {
		t.Errorf("unknown coupon: %d %s", r.Code, r.Raw)
	}

	r = gooReq(t, guest, "POST", api+"/cart/update-customer", map[string]any{"billing_address": gooTestAddress, "shipping_address": gooTestAddress}, nonce)
	if r.Code != 200 {
		t.Fatalf("update-customer: %d %s", r.Code, r.Raw)
	}
	rates, _ := gooJ(r.Body, "shipping_rates", 0, "shipping_rates").([]any)
	if len(rates) == 0 {
		t.Fatalf("no shipping rates: %s", r.Raw)
	}
	rateID := gooJS(rates[0], "rate_id")
	r = gooReq(t, guest, "POST", api+"/cart/select-shipping-rate", map[string]any{"package_id": 0, "rate_id": rateID}, nonce)
	if r.Code != 200 || gooJS(r.Body, "totals", "total_shipping") != "500" || gooJS(r.Body, "totals", "total_price") != "2300" {
		t.Fatalf("select-shipping-rate: %d totals=%v", r.Code, gooJ(r.Body, "totals"))
	}
	if gooJ(r.Body, "shipping_rates", 0, "shipping_rates", 0, "selected") != true {
		t.Error("the chosen rate is not selected")
	}

	// update-item / cart/items/{key}.
	if r := gooReq(t, guest, "POST", api+"/cart/update-item", map[string]any{"key": key, "quantity": 3}, nonce); r.Code != 200 || gooJS(r.Body, "totals", "total_items") != "3000" {
		t.Fatalf("update-item: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, guest, "PUT", api+"/cart/items/"+key, map[string]any{"quantity": 2}, nonce); r.Code != 200 || gooJ(r.Body, "quantity") != float64(2) {
		t.Fatalf("PUT cart/items/{key}: %d %s", r.Code, r.Raw)
	}

	// Checkout with the test gateway.
	r = gooReq(t, guest, "POST", api+"/checkout", map[string]any{"billing_address": gooTestAddress, "shipping_address": gooTestAddress,
		"customer_note": "Leave at the door", "payment_method": "goo_test", "payment_data": []any{map[string]any{"key": "goo_test_card_holder", "value": "Ann"}}}, nonce)
	if r.Code != 200 {
		t.Fatalf("checkout: %d %s", r.Code, r.Raw)
	}
	orderID := int64(gooJ(r.Body, "order_id").(float64))
	if orderID == 0 || gooJS(r.Body, "status") != "processing" || gooJS(r.Body, "payment_result", "payment_status") != "success" {
		t.Fatalf("checkout result: %s", r.Raw)
	}
	o, err := srv.gooGetOrder(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "order total", o.Total, 23)
	if o.PaymentMethod != "goo_test" || o.CustomerNote != "Leave at the door" || o.Billing["email"] != "ann@example.test" {
		t.Errorf("order: %+v", o)
	}
	if r := gooReq(t, guest, "GET", api+"/cart", nil, nil); len(gooJ(r.Body, "items").([]any)) != 0 {
		t.Errorf("the cart is not empty after the checkout: %s", r.Raw)
	}
	// The order endpoint needs the key and, for guests, the billing e-mail.
	q := fmt.Sprintf("%s/order/%d?key=%s&billing_email=%s", api, orderID, o.OrderKey, url.QueryEscape("ann@example.test"))
	if r := gooReq(t, gooGuest(), "GET", q, nil, nil); r.Code != 200 || gooJS(r.Body, "totals", "total_price") != "2300" {
		t.Errorf("GET order: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, gooGuest(), "GET", fmt.Sprintf("%s/order/%d?key=%s&billing_email=x@y.z", api, orderID, o.OrderKey), nil, nil); r.Code != 401 {
		t.Errorf("GET order with another e-mail: %d", r.Code)
	}

	// Cart-Token: a client without cookies keeps its cart with the token alone.
	headless := &http.Client{}
	r = gooReq(t, headless, "GET", api+"/cart", nil, nil)
	token := r.Header.Get("Cart-Token")
	r = gooReq(t, headless, "POST", api+"/cart/add-item", map[string]any{"id": p.ID, "quantity": 1}, map[string]string{"Cart-Token": token})
	if r.Code != 201 {
		t.Fatalf("add-item with Cart-Token: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, headless, "GET", api+"/cart", nil, map[string]string{"Cart-Token": token}); gooJ(r.Body, "items_count") != float64(1) {
		t.Errorf("the Cart-Token cart: %s", r.Raw)
	}
	if r := gooReq(t, headless, "POST", api+"/cart/add-item", map[string]any{"id": p.ID}, map[string]string{"Cart-Token": token + "x"}); r.Code != 401 {
		t.Errorf("a forged Cart-Token: %d", r.Code)
	}
}

func TestGooAPIStoreProducts(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	shirts := &store.Term{Taxonomy: "product_cat", Name: "Shirts"}
	if err := srv.store.InsertTerm(ctx, shirts); err != nil {
		t.Fatal(err)
	}
	a := gooMakeSimple(t, srv, "Alpha shirt", 10, func(p *gooProduct) { p.Featured = true })
	_ = srv.store.SetPostTerms(ctx, a.ID, "product_cat", []int64{shirts.ID})
	b := gooMakeSimple(t, srv, "Beta mug", 25, func(p *gooProduct) { p.SalePrice = gooFloatPtr(20) })
	c := gooMakeSimple(t, srv, "Gamma cap", 30, nil)
	api := base + "/wp-json/wc/store/v1/products"
	ids := func(r gooResp) []int64 {
		var out []int64
		for _, x := range r.list() {
			out = append(out, gooInt(gooJ(x, "id")))
		}
		return out
	}
	guest := gooGuest()
	r := gooReq(t, guest, "GET", api+"?per_page=2", nil, nil)
	if r.Code != 200 || len(r.list()) != 2 || r.Header.Get("X-WP-Total") != "3" || r.Header.Get("X-WP-TotalPages") != "2" {
		t.Fatalf("products page: %d %v %s", r.Code, r.Header, r.Raw)
	}
	for q, want := range map[string][]int64{
		"category=" + itoa64(shirts.ID): {a.ID},
		"category=shirts":               {a.ID},
		"on_sale=true":                  {b.ID},
		"featured=true":                 {a.ID},
		"min_price=1500&orderby=price":  {b.ID, c.ID},
		"max_price=1500":                {a.ID},
		"orderby=price&order=desc":      {c.ID, b.ID, a.ID},
		"search=Gamma":                  {c.ID},
		"include=" + itoa64(b.ID):       {b.ID},
		"exclude=" + itoa64(a.ID) + "&orderby=title&order=asc": {b.ID, c.ID},
	} {
		got := ids(gooReq(t, guest, "GET", api+"?"+q, nil, nil))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("products?%s = %v, want %v", q, got, want)
		}
	}
	one := gooReq(t, guest, "GET", fmt.Sprintf("%s/%d", api, b.ID), nil, nil)
	if one.Code != 200 || gooJS(one.Body, "prices", "price") != "2000" || gooJS(one.Body, "prices", "regular_price") != "2500" || gooJ(one.Body, "on_sale") != true {
		t.Errorf("product: %d %s", one.Code, one.Raw)
	}
	if !strings.Contains(gooJS(one.Body, "price_html"), "<del") {
		t.Errorf("price_html of a sale: %s", gooJS(one.Body, "price_html"))
	}
	cd := gooReq(t, guest, "GET", api+"/collection-data?calculate_price_range=true&calculate_stock_status_counts=true", nil, nil)
	if gooJS(cd.Body, "price_range", "min_price") != "1000" || gooJS(cd.Body, "price_range", "max_price") != "3000" {
		t.Errorf("collection-data: %s", cd.Raw)
	}
	cats := gooReq(t, guest, "GET", api+"/categories", nil, nil)
	found := false
	for _, x := range cats.list() {
		if gooJS(x, "slug") == "shirts" {
			found = true
		}
	}
	if !found {
		t.Errorf("categories: %s", cats.Raw)
	}
}

func TestGooAPIV3Products(t *testing.T) {
	base, c, srv := gooSite(t)
	v3 := gooAdminAPI(t, base, c)
	attr := v3("POST", "/products/attributes", map[string]any{"name": "Color", "slug": "color"})
	if attr.Code != 201 || gooJS(attr.Body, "slug") != "pa_color" {
		t.Fatalf("create attribute: %d %s", attr.Code, attr.Raw)
	}
	attrID := gooInt(gooJ(attr.Body, "id"))
	if r := v3("POST", fmt.Sprintf("/products/attributes/%d/terms", attrID), map[string]any{"name": "Green"}); r.Code != 201 || gooJS(r.Body, "slug") != "green" {
		t.Fatalf("create term: %d %s", r.Code, r.Raw)
	}
	cat := v3("POST", "/products/categories", map[string]any{"name": "Tops", "description": "All tops", "display": "products"})
	if cat.Code != 201 || gooJS(cat.Body, "display") != "products" {
		t.Fatalf("create category: %d %s", cat.Code, cat.Raw)
	}
	catID := gooInt(gooJ(cat.Body, "id"))

	prod := v3("POST", "/products", map[string]any{"name": "Shirt", "type": "variable", "sku": "SHIRT", "description": "<p>Soft</p>", "short_description": "Soft shirt",
		"categories":         []any{map[string]any{"id": catID}},
		"attributes":         []any{map[string]any{"id": attrID, "visible": true, "variation": true, "options": []any{"Red", "Blue"}}},
		"default_attributes": []any{map[string]any{"id": attrID, "option": "Red"}}, "meta_data": []any{map[string]any{"key": "fabric", "value": "cotton"}}})
	if prod.Code != 201 {
		t.Fatalf("create product: %d %s", prod.Code, prod.Raw)
	}
	pid := gooInt(gooJ(prod.Body, "id"))
	if gooJS(prod.Body, "type") != "variable" || gooJS(prod.Body, "attributes", 0, "options", 1) != "Blue" || gooJS(prod.Body, "categories", 0, "name") != "Tops" ||
		gooJS(prod.Body, "short_description") != "Soft shirt" || gooJS(prod.Body, "meta_data", 0, "value") != "cotton" {
		t.Errorf("product: %s", prod.Raw)
	}
	if r := v3("POST", "/products", map[string]any{"name": "Dup", "sku": "SHIRT"}); r.Code != 400 || gooJS(r.Body, "code") != "product_invalid_sku" {
		t.Errorf("duplicate SKU: %d %s", r.Code, r.Raw)
	}
	var varIDs []int64
	for i, color := range []string{"Red", "Blue"} {
		v := v3("POST", fmt.Sprintf("/products/%d/variations", pid), map[string]any{"regular_price": fmt.Sprint(12 + i), "sku": "SHIRT-" + color,
			"manage_stock": true, "stock_quantity": 5, "attributes": []any{map[string]any{"id": attrID, "option": color}}})
		if v.Code != 201 || gooJS(v.Body, "attributes", 0, "option") != color || gooJS(v.Body, "regular_price") != fmt.Sprint(12+i) {
			t.Fatalf("create variation: %d %s", v.Code, v.Raw)
		}
		varIDs = append(varIDs, gooInt(gooJ(v.Body, "id")))
	}
	got := v3("GET", fmt.Sprintf("/products/%d", pid), nil)
	if len(gooJ(got.Body, "variations").([]any)) != 2 || gooJS(got.Body, "price") != "12" {
		t.Errorf("variable product: %s", got.Raw)
	}
	list := v3("GET", fmt.Sprintf("/products/%d/variations", pid), nil)
	if len(list.list()) != 2 || list.Header.Get("X-WP-Total") != "2" {
		t.Errorf("variations: %s", list.Raw)
	}
	if r := v3("PUT", fmt.Sprintf("/products/%d/variations/%d", pid, varIDs[1]), map[string]any{"sale_price": "9"}); r.Code != 200 || gooJ(r.Body, "on_sale") != true {
		t.Errorf("update variation: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", fmt.Sprintf("/products/%d", pid), map[string]any{"name": "Shirt II", "featured": true}); r.Code != 200 || gooJS(r.Body, "name") != "Shirt II" || gooJ(r.Body, "featured") != true {
		t.Errorf("update product: %d %s", r.Code, r.Raw)
	}
	// Batch: create two, update one, delete one.
	batch := v3("POST", "/products/batch", map[string]any{
		"create": []any{map[string]any{"name": "B1", "regular_price": "3"}, map[string]any{"name": "B2", "regular_price": "4", "status": "draft"}},
	})
	if batch.Code != 200 || len(gooJ(batch.Body, "create").([]any)) != 2 {
		t.Fatalf("batch create: %d %s", batch.Code, batch.Raw)
	}
	b1, b2 := gooInt(gooJ(batch.Body, "create", 0, "id")), gooInt(gooJ(batch.Body, "create", 1, "id"))
	batch = v3("POST", "/products/batch", map[string]any{"update": []any{map[string]any{"id": b1, "regular_price": "5"}}, "delete": []any{b2}})
	if gooJS(batch.Body, "update", 0, "regular_price") != "5" || gooInt(gooJ(batch.Body, "delete", 0, "id")) != b2 {
		t.Errorf("batch update / delete: %s", batch.Raw)
	}
	if _, err := srv.gooGetProduct(context.Background(), b2); err == nil {
		t.Error("batch delete left the product")
	}
	filtered := v3("GET", "/products?sku=SHIRT", nil)
	if len(filtered.list()) != 1 || gooInt(gooJ(filtered.Body, 0, "id")) != pid {
		t.Errorf("products?sku: %s", filtered.Raw)
	}
	if r := v3("GET", "/products?category="+itoa64(catID), nil); len(r.list()) != 1 {
		t.Errorf("products?category: %s", r.Raw)
	}
	if r := v3("DELETE", fmt.Sprintf("/products/%d", b1), nil); r.Code != 200 || gooJS(r.Body, "status") != "trash" {
		t.Errorf("trash product: %d %s", r.Code, r.Raw)
	}
	if r := v3("DELETE", fmt.Sprintf("/products/%d/variations/%d?force=true", pid, varIDs[0]), nil); r.Code != 200 {
		t.Errorf("delete variation: %d %s", r.Code, r.Raw)
	}
	if r := v3("DELETE", fmt.Sprintf("/products/categories/%d?force=true", catID), nil); r.Code != 200 {
		t.Errorf("delete category: %d %s", r.Code, r.Raw)
	}
	// Reviews.
	rev := v3("POST", "/products/reviews", map[string]any{"product_id": pid, "review": "Great", "reviewer": "Zoe", "reviewer_email": "zoe@example.test", "rating": 4})
	if rev.Code != 201 || gooJ(rev.Body, "rating") != float64(4) || gooJS(rev.Body, "status") != "approved" {
		t.Fatalf("create review: %d %s", rev.Code, rev.Raw)
	}
	if p, _ := srv.gooGetProduct(context.Background(), pid); p.RatingCount != 1 || p.AverageRating != 4 {
		t.Errorf("rating after review: %d %v", p.RatingCount, p.AverageRating)
	}
}

func TestGooAPIV3OrdersAndMore(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	gooShopSetup(t, srv)
	p := gooMakeSimple(t, srv, "Lamp", 10, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(10) })
	v3 := gooAdminAPI(t, base, c)

	o := v3("POST", "/orders", map[string]any{"payment_method": "goo_test", "set_paid": true, "billing": gooTestAddress, "shipping": gooTestAddress,
		"line_items":     []any{map[string]any{"product_id": p.ID, "quantity": 2}},
		"shipping_lines": []any{map[string]any{"method_id": "flat_rate", "method_title": "Flat rate", "total": "5.00"}}})
	if o.Code != 201 {
		t.Fatalf("create order: %d %s", o.Code, o.Raw)
	}
	oid := gooInt(gooJ(o.Body, "id"))
	if gooJS(o.Body, "status") != "processing" || gooJS(o.Body, "total") != "25.00" || gooJS(o.Body, "line_items", 0, "total") != "20.00" ||
		gooJS(o.Body, "payment_method_title") != "Test payment" || gooJ(o.Body, "date_paid") == nil {
		t.Errorf("order: %s", o.Raw)
	}
	if lp, _ := srv.gooGetProduct(ctx, p.ID); lp.StockQuantity == nil || *lp.StockQuantity != 8 {
		t.Errorf("stock after the paid REST order: %v", lp.StockQuantity)
	}
	if r := v3("GET", "/orders?status=processing", nil); len(r.list()) != 1 || r.Header.Get("X-WP-Total") != "1" {
		t.Errorf("orders?status: %s", r.Raw)
	}
	if r := v3("GET", fmt.Sprintf("/orders?product=%d", p.ID), nil); len(r.list()) != 1 {
		t.Errorf("orders?product: %s", r.Raw)
	}
	if r := v3("POST", fmt.Sprintf("/orders/%d/notes", oid), map[string]any{"note": "Packed", "customer_note": true}); r.Code != 201 || gooJ(r.Body, "customer_note") != true {
		t.Errorf("order note: %d %s", r.Code, r.Raw)
	}
	ref := v3("POST", fmt.Sprintf("/orders/%d/refunds", oid), map[string]any{"amount": "5.00", "reason": "Damaged"})
	if ref.Code != 201 || gooJS(ref.Body, "amount") != "5.00" || gooJS(ref.Body, "reason") != "Damaged" || gooJ(ref.Body, "refunded_payment") != true {
		t.Fatalf("refund: %d %s", ref.Code, ref.Raw)
	}
	got := v3("GET", fmt.Sprintf("/orders/%d", oid), nil)
	if len(gooJ(got.Body, "refunds").([]any)) != 1 || gooJS(got.Body, "status") != "processing" {
		t.Errorf("order after a partial refund: %s", got.Raw)
	}
	if r := v3("POST", fmt.Sprintf("/orders/%d/refunds", oid), map[string]any{"amount": "100"}); r.Code == 201 {
		t.Error("a refund over the total was accepted")
	}
	v3("POST", fmt.Sprintf("/orders/%d/refunds", oid), map[string]any{"amount": "20.00"})
	if got := v3("GET", fmt.Sprintf("/orders/%d", oid), nil); gooJS(got.Body, "status") != "refunded" {
		t.Errorf("order after the full refund: %s", gooJS(got.Body, "status"))
	}
	upd := v3("PUT", fmt.Sprintf("/orders/%d", oid), map[string]any{"status": "completed", "customer_note": "Thanks"})
	if gooJS(upd.Body, "status") != "completed" || gooJS(upd.Body, "customer_note") != "Thanks" {
		t.Errorf("update order: %s", upd.Raw)
	}

	// Coupons.
	cp := v3("POST", "/coupons", map[string]any{"code": "SAVE5", "discount_type": "fixed_cart", "amount": "5", "minimum_amount": "20"})
	if cp.Code != 201 || gooJS(cp.Body, "code") != "save5" || gooJS(cp.Body, "amount") != "5.00" || gooJS(cp.Body, "minimum_amount") != "20.00" {
		t.Fatalf("create coupon: %d %s", cp.Code, cp.Raw)
	}
	cpID := gooInt(gooJ(cp.Body, "id"))
	if r := v3("PUT", fmt.Sprintf("/coupons/%d", cpID), map[string]any{"amount": "7", "free_shipping": true}); gooJS(r.Body, "amount") != "7.00" || gooJ(r.Body, "free_shipping") != true {
		t.Errorf("update coupon: %s", r.Raw)
	}
	if r := v3("POST", "/coupons", map[string]any{"code": "save5"}); r.Code != 400 {
		t.Errorf("duplicate coupon: %d", r.Code)
	}
	if r := v3("GET", "/coupons?code=save5", nil); len(r.list()) != 1 {
		t.Errorf("coupons?code: %s", r.Raw)
	}
	if r := v3("DELETE", fmt.Sprintf("/coupons/%d?force=true", cpID), nil); r.Code != 200 {
		t.Errorf("delete coupon: %d", r.Code)
	}
	if _, err := srv.gooCouponByID(ctx, cpID); err == nil {
		t.Error("the coupon survived a forced delete")
	}

	// Customers.
	cu := v3("POST", "/customers", map[string]any{"email": "cust@example.test", "first_name": "Cu", "last_name": "St", "username": "custy", "password": "secret-pass-1",
		"billing": map[string]any{"city": "Paris", "country": "FR"}})
	if cu.Code != 201 || gooJS(cu.Body, "role") != "customer" || gooJS(cu.Body, "billing", "city") != "Paris" || gooJS(cu.Body, "username") != "custy" {
		t.Fatalf("create customer: %d %s", cu.Code, cu.Raw)
	}
	cuID := gooInt(gooJ(cu.Body, "id"))
	if r := v3("PUT", fmt.Sprintf("/customers/%d", cuID), map[string]any{"last_name": "Stone", "shipping": map[string]any{"city": "Lyon"}}); gooJS(r.Body, "last_name") != "Stone" || gooJS(r.Body, "shipping", "city") != "Lyon" {
		t.Errorf("update customer: %s", r.Raw)
	}
	if r := v3("GET", "/customers", nil); len(r.list()) != 1 || gooJS(r.Body, 0, "email") != "cust@example.test" {
		t.Errorf("customers: %s", r.Raw)
	}
	if r := v3("DELETE", fmt.Sprintf("/customers/%d", cuID), nil); r.Code != 501 {
		t.Errorf("delete customer without force: %d", r.Code)
	}
	if r := v3("DELETE", fmt.Sprintf("/customers/%d?force=true", cuID), nil); r.Code != 200 {
		t.Errorf("delete customer: %d %s", r.Code, r.Raw)
	}

	// Taxes.
	tx := v3("POST", "/taxes", map[string]any{"country": "US", "state": "CA", "rate": "7.5", "name": "CA", "shipping": true})
	if tx.Code != 201 || gooJS(tx.Body, "rate") != "7.5000" || gooJS(tx.Body, "class") != "standard" {
		t.Fatalf("create tax: %d %s", tx.Code, tx.Raw)
	}
	txID := gooInt(gooJ(tx.Body, "id"))
	tb := v3("POST", "/taxes/batch", map[string]any{"create": []any{map[string]any{"country": "US", "rate": "5", "class": "reduced-rate"}},
		"update": []any{map[string]any{"id": txID, "name": "CA Tax"}}})
	if gooJS(tb.Body, "create", 0, "class") != "reduced-rate" || gooJS(tb.Body, "update", 0, "name") != "CA Tax" {
		t.Errorf("tax batch: %s", tb.Raw)
	}
	if r := v3("GET", "/taxes?class=reduced-rate", nil); len(r.list()) != 1 {
		t.Errorf("taxes?class: %s", r.Raw)
	}
	if r := v3("GET", "/taxes/classes", nil); gooJS(r.Body, 0, "slug") != "standard" {
		t.Errorf("tax classes: %s", r.Raw)
	}
	if r := v3("DELETE", fmt.Sprintf("/taxes/%d?force=true", txID), nil); r.Code != 200 || srv.gooTaxRateByID(ctx, txID) != nil {
		t.Errorf("delete tax: %d", r.Code)
	}

	// Shipping zones, locations and methods.
	z := v3("POST", "/shipping/zones", map[string]any{"name": "Europe", "order": 2})
	if z.Code != 201 {
		t.Fatalf("create zone: %d %s", z.Code, z.Raw)
	}
	zid := gooInt(gooJ(z.Body, "id"))
	if r := v3("PUT", fmt.Sprintf("/shipping/zones/%d/locations", zid), map[string]any{"locations": []any{map[string]any{"code": "EU", "type": "continent"}, map[string]any{"code": "GB"}}}); len(r.list()) != 2 || gooJS(r.Body, 1, "type") != "country" {
		t.Errorf("zone locations: %s", r.Raw)
	}
	m := v3("POST", fmt.Sprintf("/shipping/zones/%d/methods", zid), map[string]any{"method_id": "flat_rate", "settings": map[string]any{"cost": "9", "title": "Courier"}})
	if m.Code != 201 || gooJS(m.Body, "settings", "cost", "value") != "9" || gooJS(m.Body, "title") != "Courier" {
		t.Fatalf("create method: %d %s", m.Code, m.Raw)
	}
	mid := gooInt(gooJ(m.Body, "id"))
	if r := v3("PUT", fmt.Sprintf("/shipping/zones/%d/methods/%d", zid, mid), map[string]any{"enabled": false}); gooJ(r.Body, "enabled") != false {
		t.Errorf("update method: %s", r.Raw)
	}
	if r := v3("GET", "/shipping/zones", nil); gooInt(gooJ(r.Body, 0, "id")) != 0 || len(r.list()) != 3 {
		t.Errorf("zones: %s", r.Raw)
	}
	if r := v3("GET", "/shipping_methods", nil); gooJS(r.Body, 0, "id") != "flat_rate" {
		t.Errorf("shipping methods: %s", r.Raw)
	}
	if r := v3("DELETE", fmt.Sprintf("/shipping/zones/%d?force=true", zid), nil); r.Code != 200 {
		t.Errorf("delete zone: %d", r.Code)
	}

	// Settings.
	groups := v3("GET", "/settings", nil)
	ids := map[string]bool{}
	for _, g := range groups.list() {
		ids[gooJS(g, "id")] = true
	}
	for _, want := range []string{"general", "products", "tax", "email", "email_new_order"} {
		if !ids[want] {
			t.Errorf("settings groups lack %s", want)
		}
	}
	gen := v3("GET", "/settings/general", nil)
	found := false
	for _, s := range gen.list() {
		if gooJS(s, "id") == "woocommerce_currency" && gooJS(s, "value") == "USD" && gooJ(s, "options") != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("settings/general: %.400s", gen.Raw)
	}
	if r := v3("PUT", "/settings/general/woocommerce_currency", map[string]any{"value": "EUR"}); r.Code != 200 || gooJS(r.Body, "value") != "EUR" || srv.gooCurrency() != "EUR" {
		t.Errorf("update currency: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", "/settings/general/woocommerce_currency", map[string]any{"value": "XXX"}); r.Code != 400 {
		t.Errorf("invalid currency accepted: %d", r.Code)
	}
	sb := v3("POST", "/settings/general/batch", map[string]any{"update": []any{map[string]any{"id": "woocommerce_calc_taxes", "value": "yes"}, map[string]any{"id": "woocommerce_currency", "value": "USD"}}})
	if sb.Code != 200 || !srv.gooTaxEnabled() || srv.gooCurrency() != "USD" {
		t.Errorf("settings batch: %s", sb.Raw)
	}
	if r := v3("PUT", "/settings/email_new_order/enabled", map[string]any{"value": "no"}); r.Code != 200 || srv.gooEmailSettings("new_order")["enabled"] != "no" {
		t.Errorf("e-mail setting: %d %s", r.Code, r.Raw)
	}

	// Gateways, data, reports, system status.
	if r := v3("PUT", "/payment_gateways/bacs", map[string]any{"enabled": true, "title": "Bank"}); gooJ(r.Body, "enabled") != true || gooJS(r.Body, "title") != "Bank" {
		t.Errorf("update gateway: %s", r.Raw)
	}
	if r := v3("GET", "/data/countries/US", nil); gooJS(r.Body, "name") != "United States (US)" || len(gooJ(r.Body, "states").([]any)) == 0 {
		t.Errorf("data/countries/US: %.200s", r.Raw)
	}
	if r := v3("GET", "/data/currencies/current", nil); gooJS(r.Body, "code") != "USD" {
		t.Errorf("current currency: %s", r.Raw)
	}
	if r := v3("GET", "/reports/orders/totals", nil); r.Code != 200 || len(r.list()) == 0 {
		t.Errorf("reports/orders/totals: %s", r.Raw)
	}
	if r := v3("GET", "/reports/top_sellers?period=year", nil); gooInt(gooJ(r.Body, 0, "product_id")) != p.ID {
		t.Errorf("top sellers: %s", r.Raw)
	}
	if r := v3("GET", "/system_status", nil); r.Code != 200 || gooJS(r.Body, "settings", "currency") != "USD" {
		t.Errorf("system_status: %d", r.Code)
	}
}

func TestGooAPIConsumerKeys(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	gooMakeSimple(t, srv, "Pen", 2, nil)
	ck, cs, _, err := srv.gooRESTNewKey(ctx, 1, "Integration", "read_write")
	if err != nil {
		t.Fatal(err)
	}
	rck, rcs, _, _ := srv.gooRESTNewKey(ctx, 1, "Reader", "read")
	anon := &http.Client{}
	q := func(k, s string) string {
		return "consumer_key=" + url.QueryEscape(k) + "&consumer_secret=" + url.QueryEscape(s)
	}
	api := base + "/wp-json/wc/v3"
	if r := gooReq(t, anon, "GET", api+"/products", nil, nil); r.Code != 401 || gooJS(r.Body, "code") != "woocommerce_rest_cannot_view" {
		t.Errorf("no auth: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, anon, "GET", api+"/products?"+q(ck, cs), nil, nil); r.Code != 200 || len(r.list()) != 1 {
		t.Fatalf("good key: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, anon, "GET", api+"/products?"+q(ck, cs+"x"), nil, nil); r.Code != 401 || gooJS(r.Body, "message") != "Consumer secret is invalid." {
		t.Errorf("wrong secret: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, anon, "GET", api+"/products?"+q("ck_nope", cs), nil, nil); r.Code != 401 {
		t.Errorf("unknown key: %d", r.Code)
	}
	if r := gooReq(t, anon, "GET", api+"/products?"+q(rck, rcs), nil, nil); r.Code != 200 {
		t.Errorf("read key GET: %d", r.Code)
	}
	if r := gooReq(t, anon, "POST", api+"/products?"+q(rck, rcs), map[string]any{"name": "X"}, nil); r.Code != 401 || !strings.Contains(gooJS(r.Body, "message"), "write permissions") {
		t.Errorf("read key POST: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, anon, "POST", api+"/products?"+q(ck, cs), map[string]any{"name": "Made by key", "regular_price": "4"}, nil); r.Code != 201 {
		t.Errorf("read_write key POST: %d %s", r.Code, r.Raw)
	}
	var last *string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT last_access::text FROM goo_api_keys WHERE consumer_key = $1`, gooHashKey(ck)).Scan(&last)
	if last == nil {
		t.Error("last_access was not updated")
	}
}

func TestGooAPIPluginOff(t *testing.T) {
	base, c, srv := gooSite(t)
	v3 := gooAdminAPI(t, base, c)
	if r := v3("GET", "/products", nil); r.Code != 200 {
		t.Fatalf("v3 while on: %d", r.Code)
	}
	if err := srv.gooDeactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := v3("GET", "/products", nil); r.Code != 404 || gooJS(r.Body, "code") != "rest_no_route" {
		t.Errorf("v3 while off: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, gooGuest(), "GET", base+"/wp-json/wc/store/v1/cart", nil, nil); r.Code != 404 {
		t.Errorf("store API while off: %d", r.Code)
	}
}

func TestGooAPIWebhooks(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	var mu sync.Mutex
	var got []*http.Request
	var bodies [][]byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got, bodies = append(got, r), append(bodies, b)
		mu.Unlock()
	}))
	defer hook.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer failing.Close()
	var okID, badID int64
	_ = srv.store.Pool.QueryRow(ctx, `INSERT INTO goo_webhooks (name, status, topic, delivery_url, secret) VALUES ('Orders', 'active', 'order.created', $1, 's3cret') RETURNING id`, hook.URL).Scan(&okID)
	_ = srv.store.Pool.QueryRow(ctx, `INSERT INTO goo_webhooks (name, status, topic, delivery_url, secret) VALUES ('Broken', 'active', 'product.created', $1, 'x') RETURNING id`, failing.URL).Scan(&badID)
	p := gooMakeSimple(t, srv, "Hook product", 3, nil)
	v3 := gooAdminAPI(t, base, c)
	o := v3("POST", "/orders", map[string]any{"billing": gooTestAddress, "line_items": []any{map[string]any{"product_id": p.ID, "quantity": 1}}})
	if o.Code != 201 {
		t.Fatalf("create order: %d %s", o.Code, o.Raw)
	}
	gooWebhookWG.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("deliveries: %d", len(got))
	}
	r := got[0]
	if r.Header.Get("X-WC-Webhook-Topic") != "order.created" || r.Header.Get("X-WC-Webhook-Resource") != "order" || r.Header.Get("X-WC-Webhook-Event") != "created" ||
		r.Header.Get("X-WC-Webhook-ID") != itoa64(okID) || r.Header.Get("X-WC-Webhook-Delivery-ID") == "" || !strings.HasPrefix(r.Header.Get("X-WC-Webhook-Source"), "http") {
		t.Errorf("headers: %v", r.Header)
	}
	if r.Header.Get("X-WC-Webhook-Signature") != gooWebhookSignature(bodies[0], "s3cret") {
		t.Error("bad signature")
	}
	var payload map[string]any
	_ = json.Unmarshal(bodies[0], &payload)
	if gooInt(payload["id"]) != gooInt(gooJ(o.Body, "id")) || payload["line_items"] == nil {
		t.Errorf("payload: %s", bodies[0])
	}
	// The broken endpoint is disabled after five failures (the product above was the first).
	for i := 0; i < 4; i++ {
		gooMakeSimple(t, srv, fmt.Sprintf("P%d", i), 1, nil)
	}
	gooWebhookWG.Wait()
	var status string
	var failures int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status, failures FROM goo_webhooks WHERE id = $1`, badID).Scan(&status, &failures)
	if status != "disabled" || failures != 5 {
		t.Errorf("failing webhook: %s after %d failures", status, failures)
	}
}
