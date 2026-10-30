package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGooExtAddons(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)

	mug := gooMakeSimple(t, srv, "Mug", 20, func(p *gooProduct) { p.Virtual = true })
	if err := srv.paoSaveGroup(ctx, &paoGroup{Name: "Wrapping", Priority: 5, AllProducts: true, Fields: []paoAddon{
		{Name: "Gift wrap", Type: "checkbox", Options: []paoOption{{Label: "Yes", Price: 5, PriceType: "flat_fee"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.paoSaveGroup(ctx, &paoGroup{Name: "Other category", Priority: 1, Categories: []int64{999999}, Fields: []paoAddon{{Name: "Never", Type: "custom_text"}}}); err != nil {
		t.Fatal(err)
	}
	mug = gxSavePanels(t, srv, mug, paoKey, url.Values{
		"product_addon_name[]":               {"Engraving", "Color", "Extra shots", "Upload", "Tip", "Delivery"},
		"product_addon_type[]":               {"custom_text", "multiple_choice", "input_multiplier", "file_upload", "custom_price", "datepicker"},
		"product_addon_display[]":            {"select", "radiobutton", "select", "select", "select", "select"},
		"product_addon_title_format[]":       {"label", "label", "label", "label", "label", "label"},
		"product_addon_description_enable[]": {"yes", "no", "no", "no", "no", "no"},
		"product_addon_description[]":        {"Up to 10 letters", "", "", "", "", ""},
		"product_addon_required[]":           {"yes", "no", "no", "no", "no", "no"},
		"product_addon_options[]":            {"", "Red | 0\nGold | 10 | percentage_based", "", "", "", ""},
		"product_addon_adjust_price[]":       {"yes", "no", "yes", "no", "no", "no"},
		"product_addon_price_type[]":         {"quantity_based", "flat_fee", "quantity_based", "flat_fee", "flat_fee", "flat_fee"},
		"product_addon_price[]":              {"3", "", "1", "", "", ""},
		"product_addon_restrictions[]":       {"yes", "no", "no", "no", "yes", "no"},
		"product_addon_restrictions_type[]":  {"only_letters", "any_text", "any_text", "any_text", "any_text", "any_text"},
		"product_addon_min[]":                {"", "", "", "", "1", ""},
		"product_addon_max[]":                {"10", "", "", "", "50", ""},
	})
	if got := paoDecode(mug.Meta["_product_addons"]); len(got) != 6 || len(got[1].Options) != 2 || got[1].Options[1].PriceType != "percentage_based" || !got[0].Required {
		t.Fatalf("saved product add-ons: %+v", got)
	}

	// Off: no fields, no validation, no price change.
	c0 := gooTestCtx()
	cart0 := srv.gooCartFor(c0)
	k0, err := cart0.Add(ctx, mug.ID, 1, 0, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "price while off", cart0.Item(k0).Price, 20)
	if out := srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", mug); out != "" {
		t.Error("add-on fields shown while off")
	}

	if err := srv.gooSetExtension(ctx, paoKey, true); err != nil {
		t.Fatal(err)
	}
	addons := srv.paoAddonsFor(ctx, mug)
	if len(addons) != 7 || addons[0].Name != "Gift wrap" {
		t.Fatalf("add-ons of the mug: %d (%+v)", len(addons), addons)
	}
	name := func(i int) string { return paoFieldName(mug.ID, addons[i], i) }
	html := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", mug))
	for _, want := range []string{name(0) + "[]", name(1), name(2) + `" value="gold-2"`, "product-addons-total", "wc-pao-addon-file-upload"} {
		if !strings.Contains(html, want) {
			t.Errorf("add-on fields lack %q", want)
		}
	}

	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{}, nil); err == nil || !strings.Contains(err.Error(), "required field") {
		t.Errorf("missing required engraving: %v", err)
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hello World"}}, nil); err == nil || !strings.Contains(err.Error(), "maximum allowed length") {
		t.Errorf("too long engraving: %v", err)
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hi5"}}, nil); err == nil || !strings.Contains(err.Error(), "Only letters") {
		t.Errorf("digits in a letters-only engraving: %v", err)
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hi"}, name(5): {"500"}}, nil); err == nil || !strings.Contains(err.Error(), "maximum allowed amount") {
		t.Errorf("tip above the max: %v", err)
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hi"}, name(4): {"https://evil.example/x.php"}}, nil); err == nil {
		t.Error("a foreign file URL was accepted")
	}
	// 2 mugs: 20 + engraving 3 + gold 10% (2) + 2 extra shots × 1 + gift wrap 5 once (2.50 each) + tip 4 each.
	key, err := cart.Add(ctx, mug.ID, 2, 0, nil, map[string][]string{name(0) + "[]": {"yes-1"}, name(1): {"Hi"}, name(2): {"gold-2"}, name(3): {"2"}, name(5): {"4"}, name(6): {"2026-12-24"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := cart.Item(key)
	gooAlmost(t, "unit price with add-ons", it.Price, 20+3+2+2+2.5+4)
	gooAlmost(t, "line subtotal", it.LineSubtotal, 2*(20+3+2+2+4)+5)
	got := map[string]string{}
	for _, kv := range srv.gooItemData(ctx, it) {
		got[kv.Key] = kv.Value
	}
	for k, v := range map[string]string{"Gift wrap (+$5.00)": "Yes", "Engraving (+$3.00)": "Hi", "Color (+10%)": "Gold", "Extra shots (+$2.00)": "2", "Delivery": "2026-12-24"} {
		if got[k] != v {
			t.Errorf("item data %q = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}

	// File upload through the AJAX route, then used in the cart.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("goo_module", "product_addons")
	fw, _ := mw.CreateFormFile("file", "my logo.png")
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	_ = mw.Close()
	w := httptest.NewRecorder()
	uc, _ := gin.CreateTestContext(w)
	uc.Request = httptest.NewRequest(http.MethodPost, "/?goo-ajax=addon_upload", &body)
	uc.Request.Header.Set("Content-Type", mw.FormDataContentType())
	if !paoFrontRoute(srv, uc, "") {
		t.Fatal("upload route not served")
	}
	var up struct{ URL, Name, Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &up)
	if up.URL == "" || !strings.Contains(up.URL, "/wp-content/uploads/goocommerce_uploads/") || up.Name != "my-logo.png" {
		t.Fatalf("upload: %s", w.Body.String())
	}
	rel := up.URL[strings.Index(up.URL, "goocommerce_uploads/"):]
	if _, err := os.Stat(filepath.Join(srv.cfg.UploadsDir, filepath.FromSlash(rel))); err != nil {
		t.Errorf("uploaded file not stored: %v", err)
	}
	// A disallowed type is refused.
	body.Reset()
	mw = multipart.NewWriter(&body)
	fw, _ = mw.CreateFormFile("file", "shell.php")
	_, _ = fw.Write([]byte("<?php"))
	_ = mw.Close()
	w = httptest.NewRecorder()
	uc, _ = gin.CreateTestContext(w)
	uc.Request = httptest.NewRequest(http.MethodPost, "/?goo-ajax=addon_upload", &body)
	uc.Request.Header.Set("Content-Type", mw.FormDataContentType())
	paoFrontRoute(srv, uc, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("a .php upload: %d %s", w.Code, w.Body.String())
	}
	k2, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Yo"}, name(4): {up.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kv := srv.gooItemData(ctx, cart.Item(k2)); len(kv) < 2 || kv[1].Value != "my-logo.png" {
		t.Errorf("upload item data: %+v", kv)
	}

	// Checkout keeps the add-ons in the order item meta.
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	var found bool
	for _, li := range o.ItemsOf("line_item") {
		if l, ok := li.Meta["_goo_addons"].([]any); ok && len(l) == 6 {
			found = true
			if d, ok := li.Meta["_display"].([]any); !ok || len(d) != 6 {
				t.Errorf("display meta: %+v", li.Meta["_display"])
			}
		}
	}
	if !found {
		t.Error("no add-on meta on the order item")
	}
	gooAlmost(t, "order total", o.Total, 2*(20+3+2+2+4)+5+(20+3))

	// Excluding the global add-ons.
	mug.Meta["_product_addons_exclude_global"] = true
	if n := srv.paoAddonsCount(ctx, mug); n != 6 {
		t.Errorf("add-ons with globals excluded: %d", n)
	}

	// Admin: the groups list, a new group.
	code, page, _ := get(t, client, base+"/wp-admin/goocommerce?page=product-addons")
	if code != http.StatusOK || strings.Contains(page, "Template error") || !strings.Contains(page, "Wrapping") {
		t.Fatalf("add-ons admin: %d", code)
	}
	_, page, _ = get(t, client, base+"/wp-admin/goocommerce?page=product-addons&edit=0")
	code, _, loc := post(t, client, base+"/wp-admin/goocommerce?page=product-addons", url.Values{"_wpnonce": {formNonce(t, page)}, "addon-reference": {"Personalise"},
		"addon-priority": {"20"}, "addon-all-products": {"1"}, "product_addon_name[]": {"Card"}, "product_addon_type[]": {"custom_textarea"},
		"product_addon_adjust_price[]": {"yes"}, "product_addon_price[]": {"1.5"}})
	if code != http.StatusFound || !strings.Contains(loc, "edit=") {
		t.Fatalf("save group: %d %s", code, loc)
	}
	if code, page, _ := get(t, client, base+loc[strings.Index(loc, "/wp-admin"):]); code != http.StatusOK || !strings.Contains(page, "Personalise") || strings.Contains(page, "Template error") {
		t.Errorf("group edit screen: %d", code)
	}
	if gs := srv.paoGroups(ctx); len(gs) != 3 || gs[2].Fields[0].Price != 1.5 || !gs[2].AllProducts {
		t.Errorf("groups after save: %d", len(gs))
	}
}
