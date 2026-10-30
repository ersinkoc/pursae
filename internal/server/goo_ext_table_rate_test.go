package server

import (
	"context"
	"testing"
)

func TestGooExtTableRateParse(t *testing.T) {
	rates := gooTRParseRates("# comment\nweight|0|5||4.50|0|0|0|Light parcel|\n\nfoo|bar|baz\nITEMS|10|||,5|x|0|10%|Big|abort\n  price | 1,5 | | Heavy | 2 ")
	if len(rates) != 4 {
		t.Fatalf("parsed %d rates: %+v", len(rates), rates)
	}
	r := rates[0]
	if r.Condition != "weight" || *r.Min != 0 || *r.Max != 5 || r.RowCost != 4.5 || r.Label != "Light parcel" || r.Abort {
		t.Errorf("rate 1: %+v", r)
	}
	if g := rates[1]; g.Condition != "none" || g.Min != nil || g.Max != nil || g.RowCost != 0 {
		t.Errorf("garbage row: %+v", g)
	}
	if a := rates[2]; a.Condition != "items" || *a.Min != 10 || a.Max != nil || a.RowCost != 0.5 || a.ItemCost != 0 || a.Percent != 10 || !a.Abort {
		t.Errorf("rate 3: %+v", a)
	}
	if p := rates[3]; p.Condition != "price" || *p.Min != 1.5 || p.Class != "heavy" || p.RowCost != 2 {
		t.Errorf("rate 4: %+v", p)
	}
	if pr := gooTRPriorities("heavy:20, bulky = 5; bad"); pr["heavy"] != 20 || pr["bulky"] != 5 || len(pr) != 2 {
		t.Errorf("priorities %v", pr)
	}
}

func TestGooExtTableRate(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	heavy, bulky := termFor("product_shipping_class", "Heavy"), termFor("product_shipping_class", "Bulky")
	if err := srv.store.InsertTerm(ctx, heavy); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.InsertTerm(ctx, bulky); err != nil {
		t.Fatal(err)
	}
	light := gooMakeSimple(t, srv, "Light", 10, func(p *gooProduct) { p.Weight = "1" })
	hv := gooMakeSimple(t, srv, "Anvil", 30, func(p *gooProduct) { p.Weight = "6"; p.ShippingClassID = heavy.ID })
	bk := gooMakeSimple(t, srv, "Sofa", 100, func(p *gooProduct) { p.Weight = "20"; p.ShippingClassID = bulky.ID })

	z := &gooZone{Name: "US", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	if err := srv.gooSaveZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	m, err := srv.gooAddMethod(ctx, z.ID, "table_rate", map[string]any{"rates": "weight|0|5||4.50|0|0|0|Light parcel|\nweight|5|||10|1.50|0.40|0||"})
	if err != nil {
		t.Fatal(err)
	}
	set := func(kv map[string]any) {
		t.Helper()
		for k, v := range kv {
			m.Settings[k] = v
		}
		if err := srv.gooSaveMethod(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "NY", "postcode": "10001"}
	lightKey, _ := cart.Add(ctx, light.ID, 2, 0, nil, nil, nil)
	rate := func() *gooShippingRate {
		t.Helper()
		cart.Calculate()
		if len(cart.Packages) == 0 {
			return nil
		}
		for _, r := range cart.Packages[0].Rates {
			if r.MethodID == "table_rate" {
				return r
			}
		}
		return nil
	}

	// Off: the method does not run.
	if r := rate(); r != nil {
		t.Fatalf("table rate while off: %+v", r)
	}
	gooExtSwitch(t, srv, gooTRKey, true)

	// Per order, first matching rate.
	r := rate()
	if r == nil || r.Cost != 4.5 || r.Label != "Light parcel" {
		t.Fatalf("2 light items: %+v", r)
	}
	hvKey, _ := cart.Add(ctx, hv.ID, 1, 0, nil, nil, nil)
	// Weight 8 → row 2: 10 + 1.50 × 3 items + 0.40 × 8 kg.
	if r := rate(); r == nil || !gooClose(r.Cost, 10+4.5+3.2) || r.Label != "Table rate" {
		t.Errorf("weight 8: %+v", r)
	}
	// Min / max / handling fee.
	set(map[string]any{"max_cost": "15", "handling_fee": "1.25"})
	if r := rate(); r == nil || !gooClose(r.Cost, 16.25) {
		t.Errorf("max + handling: %+v", r)
	}
	set(map[string]any{"max_cost": "", "min_cost": "20", "handling_fee": "10%"})
	// Contents 50 → 10% = 5.
	if r := rate(); r == nil || !gooClose(r.Cost, 25) {
		t.Errorf("min + percent handling: %+v", r)
	}
	set(map[string]any{"min_cost": "", "handling_fee": ""})

	// Calculate all matching rates + an abort row.
	set(map[string]any{"rates": "none||||1|0|0|0||\nitems|2||heavy|5|0|0|0||\nitems_in_class|1||heavy|0|2|0|0||\nitems|10|||0|0|0|0||abort", "calculate_all": "yes"})
	// Order class = heavy (only class besides none → heavy has priority over no class): 1 + 5 + 2×1.
	if r := rate(); r == nil || !gooClose(r.Cost, 8) {
		t.Errorf("all matching: %+v", r)
	}
	_ = cart.SetQuantity(ctx, lightKey, 9)
	if r := rate(); r != nil {
		t.Errorf("abort row did not hide the method: %+v", r)
	}
	_ = cart.SetQuantity(ctx, lightKey, 2)
	set(map[string]any{"calculate_all": "no"})

	// Class priorities (per order): a mixed cart uses the highest priority class.
	bkKey, _ := cart.Add(ctx, bk.ID, 1, 0, nil, nil, nil)
	set(map[string]any{"rates": "none|||heavy|20|0|0|0||\nnone|||bulky|30|0|0|0||\nnone||||1|0|0|0||", "class_priorities": "heavy:5, bulky:20"})
	if r := rate(); r == nil || r.Cost != 30 {
		t.Errorf("bulky priority: %+v", r)
	}
	set(map[string]any{"class_priorities": "heavy:50"})
	if r := rate(); r == nil || r.Cost != 20 {
		t.Errorf("heavy priority: %+v", r)
	}
	cart.Remove(bkKey)

	// Per item: heavy items 5 each, others 2 each → 2×2 + 5.
	set(map[string]any{"calculation_type": "item", "rates": "none|||heavy|5|0|0|0||\nnone||||2|0|0|0||", "class_priorities": ""})
	if r := rate(); r == nil || r.Cost != 9 {
		t.Errorf("per item: %+v", r)
	}
	// An item no rate matches makes the method unavailable.
	set(map[string]any{"rates": "none|||heavy|5|0|0|0||"})
	if r := rate(); r != nil {
		t.Errorf("unmatched item: %+v", r)
	}
	// Per line: 1 + 0.5 × qty + 10% of the line: light (1 + 1 + 2) + anvil (1 + 0.5 + 3).
	set(map[string]any{"calculation_type": "line", "rates": "none||||1|0.5|0|10||"})
	if r := rate(); r == nil || !gooClose(r.Cost, 8.5) {
		t.Errorf("per line: %+v", r)
	}
	// Per class: 3 + 1 per item in the class: none (3 + 2) + heavy (3 + 1).
	set(map[string]any{"calculation_type": "class", "rates": "items_in_class|1|||3|1|0|0||"})
	if r := rate(); r == nil || !gooClose(r.Cost, 9) {
		t.Errorf("per class: %+v", r)
	}
	// Weight cost per class: heavy items over 5 kg.
	set(map[string]any{"rates": "weight|5||heavy|0|0|1|0||\nnone||||0|0|0|0||"})
	if r := rate(); r == nil || !gooClose(r.Cost, 6) {
		t.Errorf("per class weight: %+v", r)
	}
	// Tax status none.
	set(map[string]any{"tax_status": "none"})
	if r := rate(); r == nil || r.Taxable {
		t.Errorf("tax status: %+v", r)
	}
	_ = hvKey
	// The method shows in the "Add shipping method" list only while the module is on.
	found := false
	for _, d := range srv.gooShippingMethodOrder() {
		if d.ID == "table_rate" {
			found = true
		}
	}
	if !found {
		t.Error("table rate missing from the method list")
	}
	gooExtSwitch(t, srv, gooTRKey, false)
	for _, d := range srv.gooShippingMethodOrder() {
		if d.ID == "table_rate" {
			t.Error("table rate listed while off")
		}
	}
}

func gooClose(a, b float64) bool { return a-b < 0.001 && b-a < 0.001 }
