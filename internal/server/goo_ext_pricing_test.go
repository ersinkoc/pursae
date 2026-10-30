package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// gooExtSwitch turns a module on or off.
func gooExtSwitch(t *testing.T, srv *Server, key string, on bool) {
	t.Helper()
	if err := srv.gooSetExtension(context.Background(), key, on); err != nil {
		t.Fatal(err)
	}
}

// gooAdminPageOK fetches an admin page and checks it renders.
func gooAdminPageOK(t *testing.T, c *http.Client, u, want string) string {
	t.Helper()
	code, body, _ := get(t, c, u)
	if code != http.StatusOK || strings.Contains(body, "Template error") {
		t.Fatalf("%s: %d template error %v\n%.600s", u, code, strings.Contains(body, "Template error"), body)
	}
	if want != "" && !strings.Contains(body, want) {
		t.Errorf("%s lacks %q", u, want)
	}
	return body
}

func TestGooExtPricing(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	cat := termFor("product_cat", "Mugs")
	if err := srv.store.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	mug := gooMakeSimple(t, srv, "Mug", 10, nil)
	_ = srv.store.SetPostTerms(ctx, mug.ID, "product_cat", []int64{cat.ID})
	cup := gooMakeSimple(t, srv, "Cup", 20, nil)
	_ = srv.store.SetPostTerms(ctx, cup.ID, "product_cat", []int64{cat.ID})
	pen := gooMakeSimple(t, srv, "Pen", 5, nil)

	rules := []*gooDPRule{
		{Name: "Mug bulk", Type: gooDPProductBulk, Active: true, Priority: 1,
			Conditions: gooDPConditions{Who: "everyone", AppliesTo: "products", Products: []int64{mug.ID}, Collector: "product"},
			Rules:      gooDPRules{Tiers: []gooDPTier{{Min: 1, Max: 4, Type: "percentage_discount", Amount: 0}, {Min: 5, Max: 9, Type: "percentage_discount", Amount: 10}, {Min: 10, Type: "fixed_price", Amount: 7}}}},
		{Name: "Cup category", Type: gooDPCategoryBulk, Active: true, Priority: 2,
			Conditions: gooDPConditions{Who: "everyone", AppliesTo: "categories", Categories: []int64{cat.ID}, Collector: "category"},
			Rules:      gooDPRules{Tiers: []gooDPTier{{Min: 6, Type: "price_discount", Amount: 2}}}},
		{Name: "Pens 3 for 2", Type: gooDPSpecialOffer, Active: true, Priority: 3,
			Conditions: gooDPConditions{Who: "everyone", AppliesTo: "products", Products: []int64{pen.ID}},
			Rules:      gooDPRules{Buy: 2, Receive: 1, Discount: 100, Repeating: true}},
		{Name: "Big spender", Type: gooDPCartTotal, Active: true, Priority: 4, Conditions: gooDPConditions{Who: "everyone"},
			Rules: gooDPRules{Tiers: []gooDPTier{{Min: 100, Type: "percentage_discount", Amount: 5}, {Min: 200, Type: "percentage_discount", Amount: 10}}}},
		{Name: "Wholesale", Type: gooDPRole, Active: true, Priority: 5, Conditions: gooDPConditions{Who: "roles", Roles: []string{"wholesale"}, AppliesTo: "all"},
			Rules: gooDPRules{Type: "percentage_discount", Amount: 20}},
	}
	for _, r := range rules {
		if err := srv.gooDPSaveRule(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// Off: no effect.
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(ctx, mug.ID, 5, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "mugs while off", cart.Subtotal, 50)
	if len(cart.Fees) != 0 {
		t.Errorf("fees while off: %+v", cart.Fees)
	}

	gooExtSwitch(t, srv, gooDPKey, true)
	cart.Calculate()
	// 5 mugs: 10% off (product tier); the category rule needs 6 → not applied; stacking off.
	gooAlmost(t, "5 mugs", cart.Subtotal, 45)
	if orig, ok := srv.gooDPOriginalPrice(c, cart, cart.Items[0]); !ok || orig != 10 {
		t.Errorf("original price %v %v", orig, ok)
	}
	_ = cart.SetQuantity(ctx, cart.Items[0].Key, 10)
	gooAlmost(t, "10 mugs at fixed 7", cart.Subtotal, 70)
	// 1 cup: category count = 11 ≥ 6 → 2 off; the mug keeps its first (product) rule.
	if _, err := cart.Add(ctx, cup.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "mugs + cup", cart.Subtotal, 70+18)
	if len(cart.Fees) != 0 {
		t.Errorf("cart total fee below 100: %+v", cart.Fees)
	}
	// Pens: buy 2 get 1 free, repeating: 6 pens → 2 free → 20.
	if _, err := cart.Add(ctx, pen.ID, 6, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "with pens", cart.Subtotal, 70+18+20)
	// Subtotal 108 ≥ 100 → 5% cart discount as a negative fee.
	if len(cart.Fees) != 1 || cart.Fees[0].Name != "Big spender" {
		t.Fatalf("cart total fee: %+v", cart.Fees)
	}
	gooAlmost(t, "cart fee", cart.Fees[0].Total, -5.4)
	gooAlmost(t, "total", cart.Total, 108-5.4)

	// Stacking: the mug gets the product tier then the category tier.
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_" + gooDPKey + "_settings": map[string]any{"stack_rules": true, "apply_to_sale": true, "show_table": true}})
	cart.Calculate()
	gooAlmost(t, "stacked", cart.Subtotal, 50+18+20)
	// Sale items excluded.
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_" + gooDPKey + "_settings": map[string]any{"stack_rules": false, "apply_to_sale": false, "show_table": true}})
	mug.SalePrice = gooFloatPtr(9)
	_ = srv.gooSaveProduct(ctx, mug, 1)
	c2 := gooTestCtx()
	cart2 := srv.gooCartFor(c2)
	_, _ = cart2.Add(ctx, mug.ID, 10, 0, nil, nil, nil)
	gooAlmost(t, "sale mugs untouched", cart2.Subtotal, 90)
	mug.SalePrice = nil
	_ = srv.gooSaveProduct(ctx, mug, 1)

	// Role pricing: catalog price, price HTML and cart.
	c3 := gooTestCtx()
	c3.Set(ctxUser, &store.User{ID: 77, Roles: []string{"wholesale"}})
	if v := srv.gooDisplayPrice(c3, pen); v != 4 {
		t.Errorf("wholesale catalog price %v", v)
	}
	if v := srv.gooDisplayPrice(gooTestCtx(), pen); v != 5 {
		t.Errorf("guest catalog price %v", v)
	}
	if h := string(srv.gooFilterPriceHTML(c3, pen, srv.gooPrice(5))); !strings.Contains(h, "<del") || !strings.Contains(h, "4.00") {
		t.Errorf("role price html %s", h)
	}
	cart3 := srv.gooCartFor(c3)
	_, _ = cart3.Add(ctx, pen.ID, 1, 0, nil, nil, nil)
	gooAlmost(t, "wholesale cart", cart3.Subtotal, 4)

	// Pricing table on the single product.
	h := string(srv.gooHTML(gooTestCtx(), "single_summary_end", mug))
	for _, want := range []string{"goo-dynamic-pricing-table", "Quantity", "Price per unit", "5 - 9", "10+", "9.00", "7.00"} {
		if !strings.Contains(h, want) {
			t.Errorf("pricing table lacks %q: %s", want, h)
		}
	}
	gooExtSwitch(t, srv, gooDPKey, false)
	if h := srv.gooHTML(gooTestCtx(), "single_summary_end", mug); strings.Contains(string(h), "goo-dynamic-pricing-table") {
		t.Error("pricing table while off")
	}
	gooExtSwitch(t, srv, gooDPKey, true)

	// Admin: list, editor, save.
	gooAdminPageOK(t, client, base+"/wp-admin/goocommerce?page=pricing-rules", "Mug bulk")
	body := gooAdminPageOK(t, client, base+"/wp-admin/goocommerce?page=pricing-rules&action=new", "Quantity / subtotal tiers")
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)[1]
	form := url.Values{"_wpnonce": {nonce}, "name": {"Admin rule"}, "type": {gooDPCategoryBulk}, "active": {"1"}, "priority": {"7"},
		"who": {"everyone"}, "applies_to": {"categories"}, "categories[]": {itoa64(cat.ID)}, "collector": {"category"},
		"tier_min[]": {"3", ""}, "tier_max[]": {"", ""}, "tier_type[]": {"percentage_discount", "percentage_discount"}, "tier_amount[]": {"15", ""}}
	code, _, loc := post(t, client, base+"/wp-admin/goocommerce?page=pricing-rules", form)
	if code != http.StatusFound || !strings.Contains(loc, "action=edit") {
		t.Fatalf("save rule: %d %s", code, loc)
	}
	all, _ := srv.gooDPRuleList(ctx, false)
	var saved *gooDPRule
	for _, r := range all {
		if r.Name == "Admin rule" {
			saved = r
		}
	}
	if saved == nil || len(saved.Rules.Tiers) != 1 || saved.Rules.Tiers[0].Amount != 15 || saved.Priority != 7 || !gooInt64In(cat.ID, saved.Conditions.Categories) {
		t.Fatalf("saved rule: %+v", saved)
	}
	gooAdminPageOK(t, client, base+"/wp-admin/goocommerce?page=pricing-rules&action=edit&id="+itoa64(saved.ID), "Admin rule")
}
