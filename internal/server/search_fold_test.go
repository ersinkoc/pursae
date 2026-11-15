package server

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// TestAdminSearchDottedDotlessI: the admin searches (order customers and products, coupons, the
// shop's product search, Plugins) find Turkish text whatever i the term is typed with (PRS-I18N-001).
func TestAdminSearchDottedDotlessI(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "IŞIK Eklentisi", 10, nil)
	if _, err := srv.store.CreateUser(ctx, store.NewUser{Login: "isikyilmaz", Email: "isik@example.test", DisplayName: "Işık Yılmaz", Role: "customer"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "IŞIK10", Description: "Işık kampanyası", DiscountType: "percent", Amount: 10}); err != nil {
		t.Fatal(err)
	}
	ajax := func(action, term string) map[string]string {
		t.Helper()
		body := gooStoreGet(t, c, base+gooAdminURL("orders", "action", action, "term", term))
		out := map[string]string{}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s %q: %v (%s)", action, term, err, body)
		}
		return out
	}
	for _, term := range []string{"ışık", "IŞIK", "işik"} {
		if got := ajax("search_products", term); got[itoa64(p.ID)] == "" {
			t.Errorf("search_products %q = %v, want the IŞIK Eklentisi product", term, got)
		}
		if got := ajax("search_customers", term); len(got) != 1 || !strings.Contains(strings.Join(mapValues(got), ""), "Işık Yılmaz") {
			t.Errorf("search_customers %q = %v, want Işık Yılmaz", term, got)
		}
		coupons, _, err := srv.gooQueryCoupons(ctx, "", term, 1, 20)
		if err != nil || len(coupons) != 1 {
			t.Errorf("coupons %q = %d, %v; want the IŞIK10 coupon", term, len(coupons), err)
		}
		list, _, err := srv.gooQueryProducts(ctx, gooProductQuery{Search: term})
		if err != nil || len(list) != 1 || list[0].ID != p.ID {
			t.Errorf("shop search %q = %d products, %v", term, len(list), err)
		}
	}
	// No Turkish casing elsewhere: "INFO"-style upper case still matches plain i.
	if got := ajax("search_products", "EKLENTİSİ"); got[itoa64(p.ID)] == "" {
		t.Errorf("search_products EKLENTİSİ = %v", got)
	}
	// Plugins: a dotless ı typed on a Turkish keyboard still finds "Classic Editor".
	if body := gooStoreGet(t, c, base+"/wp-admin/plugins?s="+url.QueryEscape("classıc edıtor")); !strings.Contains(body, "Classic Editor") {
		t.Error("Plugins search for \"classıc edıtor\" lacks Classic Editor")
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
