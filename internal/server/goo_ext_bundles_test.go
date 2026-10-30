package server

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestGooExtBundles(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true})
	z := &gooZone{Name: "United States", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	if err := srv.gooSaveZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"}); err != nil {
		t.Fatal(err)
	}

	shirt := gooMakeSimple(t, srv, "Shirt", 20, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(5) })
	hat := gooMakeSimple(t, srv, "Hat", 10, nil)
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Color"}); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"Red", "Blue"} {
		if err := srv.store.InsertTerm(ctx, termFor("pa_color", col)); err != nil {
			t.Fatal(err)
		}
	}
	hoodie := gooNewProduct(gooTypeVariable)
	hoodie.Name = "Hoodie"
	hoodie.Attributes = []gooProductAttr{{Taxonomy: "pa_color", Options: []string{"red", "blue"}, Visible: true, Variation: true}}
	if err := srv.gooSaveProduct(ctx, hoodie, 1); err != nil {
		t.Fatal(err)
	}
	for i, col := range []string{"red", "blue"} {
		v := gooNewProduct(gooTypeVariation)
		v.ParentID, v.Name, v.RegularPrice = hoodie.ID, "Hoodie - "+col, gooFloatPtr(float64(50+10*i))
		v.VariationAttrs = map[string]string{"pa_color": col}
		if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
			t.Fatal(err)
		}
	}

	b := gooNewProduct(bnType)
	b.Name, b.RegularPrice = "Starter kit", gooFloatPtr(30)
	b = gxSavePanels(t, srv, b, bnKey, url.Values{
		"_wc_pb_layout": {"tabular"}, "_wc_pb_shipping_type": {"unassembled"},
		"bundle_item_id[]":                   {"", "", ""},
		"bundle_product_id[]":                {itoa64(shirt.ID), itoa64(hat.ID), itoa64(hoodie.ID)},
		"bundle_quantity_default[]":          {"2", "1", "1"},
		"bundle_quantity_min[]":              {"2", "1", "1"},
		"bundle_quantity_max[]":              {"2", "1", "1"},
		"bundle_optional[]":                  {"no", "yes", "no"},
		"bundle_priced_individually[]":       {"no", "yes", "yes"},
		"bundle_discount[]":                  {"", "10", ""},
		"bundle_single_product_visibility[]": {"visible", "visible", "visible"},
		"bundle_cart_visibility[]":           {"visible", "visible", "visible"},
		"bundle_order_visibility[]":          {"visible", "visible", "visible"},
		"bundle_override_title[]":            {"no", "yes", "no"},
		"bundle_title[]":                     {"", "Cap", ""},
	})
	items := bnItemsOf(b)
	if len(items) != 3 || items[0].ID != 1 || items[2].ID != 3 || !items[1].Optional || items[1].Discount != 10 || items[1].title(hat) != "Cap" {
		t.Fatalf("saved bundled items: %+v", items)
	}

	// Off: nothing.
	if srv.gooIsPurchasable(ctx, b) {
		t.Error("a bundle is purchasable while the module is off")
	}
	c0 := gooTestCtx()
	if _, err := srv.gooCartFor(c0).Add(ctx, b.ID, 1, 0, nil, nil, nil); err == nil {
		t.Error("a bundle was added while the module is off")
	}

	if err := srv.gooSetExtension(ctx, bnKey, true); err != nil {
		t.Fatal(err)
	}
	// Price: from 30 + 50 (the hoodie is required) to 30 + 9 + 60.
	lo, hi := srv.bnPriceRange(ctx, b)
	gooAlmost(t, "min price", lo, 80)
	gooAlmost(t, "max price", hi, 99)
	if h := string(srv.gooFilterPriceHTML(ctx, b, "")); !strings.Contains(h, "From:") || !strings.Contains(h, "80.00") {
		t.Errorf("price html: %s", h)
	}
	if out := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", b)); !strings.Contains(out, "bundle_attribute_pa_color_3") || !strings.Contains(out, "bundle_selected_optional_2") || !strings.Contains(out, "layout_tabular") {
		t.Errorf("bundle form: %s", out)
	}

	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	if _, err := cart.Add(ctx, b.ID, 1, 0, nil, map[string][]string{"bundle_selected_optional_2": {"1"}}, nil); err == nil || !strings.Contains(err.Error(), "options") {
		t.Errorf("bundle without the hoodie's color: %v", err)
	}
	if _, err := cart.Add(ctx, b.ID, 3, 0, nil, map[string][]string{"bundle_attribute_pa_color_3": {"blue"}}, nil); err == nil || !strings.Contains(err.Error(), "not enough stock") {
		t.Errorf("3 bundles need 6 shirts out of 5: %v", err)
	}
	key, err := cart.Add(ctx, b.ID, 1, 0, nil, map[string][]string{"bundle_attribute_pa_color_3": {"blue"}, "bundle_selected_optional_2": {"1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cart.Items) != 4 {
		t.Fatalf("cart lines after adding the bundle: %d", len(cart.Items))
	}
	prices := map[int64]float64{}
	qtys := map[int64]int{}
	for _, it := range cart.Items {
		prices[it.ProductID], qtys[it.ProductID] = it.Price, it.Quantity
		if it.Key != key && bnBundledBy(it) != key {
			t.Errorf("child %d not bundled by the container", it.ProductID)
		}
	}
	gooAlmost(t, "container price", prices[b.ID], 30)
	gooAlmost(t, "shirt price", prices[shirt.ID], 0)
	gooAlmost(t, "hat price", prices[hat.ID], 9)
	gooAlmost(t, "hoodie price", prices[hoodie.ID], 60)
	gooAlmost(t, "subtotal", cart.Subtotal, 99)
	if qtys[shirt.ID] != 2 {
		t.Errorf("shirt quantity %d", qtys[shirt.ID])
	}
	var shirtLine *gooCartItem
	for _, it := range cart.Items {
		if it.ProductID == shirt.ID {
			shirtLine = it
		}
	}
	if kv := srv.gooItemData(ctx, shirtLine); len(kv) == 0 || kv[0].Key != "Part of" || kv[0].Value != "Starter kit" {
		t.Errorf("child item data: %+v", kv)
	}
	if kv := srv.gooItemData(ctx, cart.Item(key)); len(kv) == 0 || !strings.Contains(kv[0].Value, "Shirt × 2") || !strings.Contains(kv[0].Value, "Cap × 1") {
		t.Errorf("container item data: %+v", kv)
	}
	// The children follow the container's quantity, and survive a reload of the session.
	if err := cart.SetQuantity(ctx, key, 2); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "subtotal × 2", cart.Subtotal, 198)
	reloaded := &gooCart{s: srv, c: c, sess: cart.sess}
	reloaded.load(ctx)
	reloaded.Calculate()
	if len(reloaded.Items) != 4 || reloaded.Subtotal != 198 {
		t.Errorf("reloaded bundle: %d lines, subtotal %v", len(reloaded.Items), reloaded.Subtotal)
	}
	for _, it := range reloaded.Items {
		if it.ProductID == shirt.ID && it.Quantity != 4 {
			t.Errorf("shirt quantity after ×2: %d", it.Quantity)
		}
	}
	// Removing the container removes its items; Undo brings them back.
	reloaded.Remove(key)
	if !reloaded.IsEmpty() {
		t.Errorf("children left after removing the bundle: %d", len(reloaded.Items))
	}
	reloaded.Restore(ctx, key)
	if len(reloaded.Items) != 4 {
		t.Errorf("restored bundle lines: %d", len(reloaded.Items))
	}
	_ = reloaded.SetQuantity(ctx, key, 1)

	// Without the optional item.
	c2 := gooTestCtx()
	cart2 := srv.gooCartFor(c2)
	if _, err := cart2.Add(ctx, b.ID, 1, 0, nil, map[string][]string{"bundle_attribute_pa_color_3": {"red"}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(cart2.Items) != 3 {
		t.Errorf("lines without the optional hat: %d", len(cart2.Items))
	}
	gooAlmost(t, "subtotal without the hat (red hoodie)", cart2.Subtotal, 80)

	// Bundle size limits.
	b.Meta["_wc_pb_min_bundle_size"] = float64(5)
	if err := srv.gooSaveProduct(ctx, b, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := cart2.Add(ctx, b.ID, 1, 0, nil, map[string][]string{"bundle_attribute_pa_color_3": {"red"}}, nil); err == nil || !strings.Contains(err.Error(), "at least 5") {
		t.Errorf("min bundle size: %v", err)
	}
	b.Meta["_wc_pb_min_bundle_size"] = float64(0)
	// Assembled: the bundle ships, its items do not.
	b.Meta["_wc_pb_shipping_type"] = "assembled"
	if err := srv.gooSaveProduct(ctx, b, 1); err != nil {
		t.Fatal(err)
	}
	cart2b := &gooCart{s: srv, c: c2, sess: cart2.sess}
	cart2b.load(ctx)
	cart2b.Calculate()
	for _, it := range cart2b.Items {
		ships := srv.gooNeedsShipping(ctx, it.Product())
		if it.ProductID == b.ID && !ships || it.ProductID != b.ID && ships {
			t.Errorf("assembled bundle: product %d needs shipping %v", it.ProductID, ships)
		}
	}
	b.Meta["_wc_pb_shipping_type"] = "unassembled"
	_ = srv.gooSaveProduct(ctx, b, 1)

	// Checkout (the first cart, one bundle with the hat): the order keeps the relation; children reduce stock.
	c.Set("goo.cart", reloaded)
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	lines := o.ItemsOf("line_item")
	if len(lines) != 4 {
		t.Fatalf("order lines %d", len(lines))
	}
	var containerKey string
	for _, li := range lines {
		if li.ProductID == b.ID {
			containerKey = li.MetaString("_bundle_cart_key")
			if l, ok := li.Meta["_bundled_items"].([]any); !ok || len(l) != 3 {
				t.Errorf("container meta: %+v", li.Meta)
			}
		}
	}
	for _, li := range lines {
		if li.ProductID != b.ID && li.MetaString("_bundled_by") != containerKey {
			t.Errorf("child %s meta: %+v", li.Name, li.Meta)
		}
		if li.ProductID == hat.ID && li.Name != "Cap" {
			t.Errorf("override title on the order: %s", li.Name)
		}
	}
	gooAlmost(t, "order total", o.Total, 99+5)
	if p, _ := srv.gooGetProduct(ctx, shirt.ID); p.StockQuantity == nil || *p.StockQuantity != 3 {
		t.Errorf("shirt stock after the order: %v", p.StockQuantity)
	}
}

// Bundled items whose cart / order visibility is "hidden" are left out of the cart, mini-cart,
// checkout review, order details, e-mails and invoices, while still counting in the totals.
func TestGooExtBundlesHiddenItems(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	virt := func(p *gooProduct) { p.Virtual = true }
	sock := gooMakeSimple(t, srv, "Hidden Sockpair", 4, virt)
	mug := gooMakeSimple(t, srv, "Visible Mugcup", 6, virt)
	b := gooNewProduct(bnType)
	b.Name, b.RegularPrice = "Gift box", gooFloatPtr(10)
	b.Virtual = true
	b = gxSavePanels(t, srv, b, bnKey, url.Values{
		"bundle_item_id[]":                   {"", ""},
		"bundle_product_id[]":                {itoa64(sock.ID), itoa64(mug.ID)},
		"bundle_quantity_default[]":          {"1", "1"},
		"bundle_quantity_min[]":              {"1", "1"},
		"bundle_quantity_max[]":              {"1", "1"},
		"bundle_optional[]":                  {"no", "no"},
		"bundle_priced_individually[]":       {"yes", "yes"},
		"bundle_single_product_visibility[]": {"visible", "visible"},
		"bundle_cart_visibility[]":           {"hidden", "visible"},
		"bundle_order_visibility[]":          {"hidden", "visible"},
		"bundle_hide_thumbnail[]":            {"yes", "no"},
	})
	if items := bnItemsOf(b); len(items) != 2 || items[0].VisibleCart || items[0].VisibleOrder || !items[0].HideThumbnail {
		t.Fatalf("saved visibility: %+v", items)
	}
	if err := srv.gooSetExtension(ctx, bnKey, true); err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(ctx, b.ID, 1, 0, nil, map[string][]string{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(cart.Items) != 3 {
		t.Fatalf("cart lines: %d", len(cart.Items))
	}
	gooAlmost(t, "subtotal counts the hidden item", cart.Subtotal, 20)
	check := func(where, out string) {
		t.Helper()
		if strings.Contains(out, "Hidden Sockpair") {
			t.Errorf("%s lists the hidden bundled item", where)
		}
		if !strings.Contains(out, "Visible Mugcup") {
			t.Errorf("%s misses the visible bundled item", where)
		}
	}
	check("cart", srv.gooCartPageHTML(c))
	check("mini-cart", string(srv.gooMiniCart(c)))
	check("checkout review", srv.gooReviewOrderHTML(c, cart))
	storeCart, _ := json.Marshal(srv.gooStoreCartJSON(c, cart))
	check("Store API cart", string(storeCart))

	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	if len(o.ItemsOf("line_item")) != 3 {
		t.Fatalf("order lines: %d", len(o.ItemsOf("line_item")))
	}
	check("order details", srv.gooOrderDetailsHTML(gooTestCtx(), o, false))
	storeOrder, _ := json.Marshal(srv.gooStoreOrderJSON(ctx, o))
	check("Store API order", string(storeOrder))
	h, plain := srv.gooEmailOrderDetails(ctx, o, false, nil)
	check("e-mail", h)
	check("plain e-mail", plain)

	// Switched off, nothing is hidden.
	if err := srv.gooSetExtension(ctx, bnKey, false); err != nil {
		t.Fatal(err)
	}
	if h, _ := srv.gooEmailOrderDetails(ctx, o, false, nil); !strings.Contains(h, "Hidden Sockpair") {
		t.Error("hidden item still hidden with the module off")
	}
}
