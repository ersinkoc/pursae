package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Store admin tests (Home, Orders, Customers, Coupons, Marketing, Settings, Reports, Analytics, Status).

// gooStoreGet fetches an admin page and fails on anything but 200 or a template error.
func gooStoreGet(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	code, body, loc := get(t, c, u)
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d %s %s", u, code, loc, body[:min(len(body), 300)])
	}
	if strings.Contains(body, "Template error") {
		t.Fatalf("GET %s: template error (%s)", u, lastRenderError.Load())
	}
	return body
}

// gooStorePost posts a form with the page's nonce and expects a redirect; it returns the Location.
func gooStorePost(t *testing.T, c *http.Client, page string, form url.Values) string {
	t.Helper()
	body := gooStoreGet(t, c, page)
	form.Set("_wpnonce", formNonce(t, body))
	code, out, loc := post(t, c, page, form)
	if code != http.StatusFound {
		t.Fatalf("POST %s: %d %s", page, code, out[:min(len(out), 400)])
	}
	if strings.Contains(loc, "goo-error=") {
		u, _ := url.Parse(loc)
		t.Fatalf("POST %s: error %q", page, u.Query().Get("goo-error"))
	}
	return loc
}

func TestGooStorePagesRender(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Render Shirt", 10, nil)
	o := &gooOrder{Status: "processing", Billing: map[string]string{"first_name": "Ada", "last_name": "Lovelace", "email": "ada@example.test", "country": "US", "state": "CA"},
		Shipping: map[string]string{}, Items: []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: 1, Subtotal: 10, Total: 10}}}
	srv.gooRecalculate(o)
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	cp := &gooCoupon{Code: "render10", DiscountType: "percent", Amount: 10}
	if err := srv.gooSaveCoupon(ctx, cp); err != nil {
		t.Fatal(err)
	}
	z := &gooZone{Name: "Domestic", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	_ = srv.gooSaveZone(ctx, z)
	m, _ := srv.gooAddMethod(ctx, z.ID, "flat_rate", nil)
	pages := []string{
		"", "?page=orders", "?page=orders&status=wc-processing", "?page=orders&s=ada", "?page=orders&action=edit&id=" + itoa64(o.ID),
		"?page=customers", "?page=customers&orderby=name&order=asc", "?page=coupons", "?page=coupons&action=new", "?page=coupons&action=edit&id=" + itoa64(cp.ID),
		"?page=marketing", "?page=reports", "?page=reports&tab=orders&report=sales_by_product", "?page=reports&tab=orders&report=sales_by_category",
		"?page=reports&tab=orders&report=coupon_usage", "?page=reports&tab=orders&report=sales_by_date&range=year", "?page=reports&tab=orders&report=sales_by_date&range=custom&start_date=2026-01-01&end_date=2026-02-01",
		"?page=reports&tab=customers", "?page=reports&tab=customers&report=customer_list", "?page=reports&tab=stock", "?page=reports&tab=stock&report=out_of_stock", "?page=reports&tab=stock&report=most_stocked",
		"?page=status", "?page=status&tab=tools", "?page=status&tab=logs", "?page=status&tab=action-scheduler",
		"?page=settings&tab=shipping&zone_id=" + itoa64(z.ID), "?page=settings&tab=shipping&zone_id=new", "?page=settings&tab=shipping&zone_id=0",
		"?page=settings&tab=shipping&instance_id=" + itoa64(m.InstanceID), "?page=settings&tab=checkout&section=bacs", "?page=settings&tab=checkout&section=cod",
		"?page=settings&tab=advanced&section=keys&create-key=1", "?page=settings&tab=advanced&section=webhooks&edit-webhook=0",
	}
	for _, tab := range gooSettingsTabs() {
		pages = append(pages, "?page=settings&tab="+tab.ID)
		for _, sec := range tab.Sections {
			if sec.ID != "" {
				pages = append(pages, "?page=settings&tab="+tab.ID+"&section="+sec.ID)
			}
		}
	}
	for _, cl := range srv.gooTaxClasses() {
		pages = append(pages, "?page=settings&tab=tax&section="+gooTaxSection(cl.Value))
	}
	for _, e := range gooEmailDefs {
		pages = append(pages, "?page=settings&tab=email&section="+e.ID)
	}
	for _, a := range gooAnalyticsPages {
		u := "?page=analytics"
		if a.Value != "" {
			u += "&path=" + a.Value
		}
		pages = append(pages, u, u+"&period=year&compare=previous_period")
	}
	for _, pg := range pages {
		body := gooStoreGet(t, c, base+"/wp-admin/goocommerce"+pg)
		if strings.Contains(body, "WooCommerce") && !strings.Contains(body, "compatible with WooCommerce") {
			for _, line := range strings.Split(body, "\n") {
				if strings.Contains(line, "WooCommerce") && !strings.Contains(line, "woocommerce.com") {
					t.Errorf("%s says WooCommerce: %s", pg, strings.TrimSpace(line)[:min(len(strings.TrimSpace(line)), 200)])
					break
				}
			}
		}
	}
	// The new order screen creates a draft and redirects to it.
	if code, _, loc := get(t, c, base+"/wp-admin/goocommerce?page=orders&action=new"); code != http.StatusFound || !strings.Contains(loc, "action=edit") {
		t.Fatalf("new order: %d %s", code, loc)
	}
	// CSV downloads.
	for _, u := range []string{"?page=customers&download=csv", "?page=analytics&path=revenue&download=csv", "?page=settings&tab=tax&section=standard&export=1",
		"?page=reports&tab=stock&report=most_stocked&download=csv"} {
		resp, err := c.Get(base + "/wp-admin/goocommerce" + u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
			t.Errorf("%s: %d %s", u, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

func TestGooStoreSettingsRoundTrip(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	page := base + "/wp-admin/goocommerce?page=settings"

	// General: text, select, checkbox group, multiselect, number, country:state.
	loc := gooStorePost(t, c, page+"&tab=general", url.Values{
		"woocommerce_store_address": {"1 Main St"}, "woocommerce_store_city": {"Austin"}, "woocommerce_default_country": {"US:TX"},
		"woocommerce_allowed_countries": {"specific"}, "woocommerce_specific_allowed_countries[]": {"", "US", "CA"},
		"woocommerce_calc_taxes": {"1"}, "woocommerce_currency": {"EUR"}, "woocommerce_price_num_decimals": {"3"}, "woocommerce_currency_pos": {"right"},
	})
	if !strings.Contains(loc, "goo-msg=") {
		t.Fatalf("no saved message: %s", loc)
	}
	if srv.gooOptString("store_city") != "Austin" || srv.gooBaseState() != "TX" || srv.gooCurrency() != "EUR" || srv.gooDecimals() != 3 {
		t.Errorf("general not saved: %s %s %s %d", srv.gooOptString("store_city"), srv.gooBaseState(), srv.gooCurrency(), srv.gooDecimals())
	}
	if v, ok := srv.gooOpt("price_num_decimals").(float64); !ok || v != 3 {
		t.Errorf("number stored as %T", srv.gooOpt("price_num_decimals"))
	}
	if !srv.gooTaxEnabled() || srv.gooCouponsEnabled() {
		t.Errorf("checkboxes: taxes %v coupons %v", srv.gooTaxEnabled(), srv.gooCouponsEnabled())
	}
	if l := srv.gooOptList("specific_allowed_countries"); strings.Join(l, ",") != "US,CA" {
		t.Errorf("multiselect: %v", l)
	}
	body := gooStoreGet(t, c, page+"&tab=general")
	for _, want := range []string{`value="Austin"`, `<option value="US:TX" selected="selected">`, `<option value="EUR" selected="selected">`, `name="woocommerce_calc_taxes" id="woocommerce_calc_taxes" type="checkbox" class="" value="1" checked="checked"`} {
		if !strings.Contains(body, want) {
			t.Errorf("general form lacks %s", want)
		}
	}
	// Products › Inventory (section) and Accounts & Privacy, Emails, Advanced.
	gooStorePost(t, c, page+"&tab=products&section=inventory", url.Values{"woocommerce_notify_low_stock_amount": {"5"}, "woocommerce_stock_format": {"no_amount"}})
	if srv.gooOptInt("notify_low_stock_amount") != 5 || srv.gooOptString("stock_format") != "no_amount" || srv.gooManageStock() {
		t.Errorf("inventory: %d %s %v", srv.gooOptInt("notify_low_stock_amount"), srv.gooOptString("stock_format"), srv.gooManageStock())
	}
	gooStorePost(t, c, page+"&tab=account", url.Values{"woocommerce_enable_guest_checkout": {"1"}, "woocommerce_trash_pending_orders": {"7"}})
	if !srv.gooOptBool("enable_guest_checkout") || srv.gooOptInt("trash_pending_orders") != 7 {
		t.Error("accounts not saved")
	}
	gooStorePost(t, c, page+"&tab=email", url.Values{"woocommerce_email_from_name": {"Shop"}, "woocommerce_email_base_color": {"#123456"}})
	if srv.gooOptString("email_from_name") != "Shop" || srv.gooOptString("email_base_color") != "#123456" {
		t.Error("email options not saved")
	}
	gooStorePost(t, c, page+"&tab=advanced", url.Values{"woocommerce_cart_page_id": {strconv.FormatInt(srv.gooPageID("cart"), 10)}, "woocommerce_checkout_pay_endpoint": {"pay-now"}})
	if srv.gooOptString("checkout_pay_endpoint") != "pay-now" {
		t.Error("advanced not saved")
	}
	gooStorePost(t, c, page+"&tab=tax", url.Values{"woocommerce_prices_include_tax": {"yes"}, "woocommerce_tax_classes": {"Reduced rate\nZero rate\nLuxury"}})
	if !srv.gooPricesIncludeTax() || len(srv.gooTaxClasses()) != 4 {
		t.Errorf("tax options: %v %v", srv.gooPricesIncludeTax(), srv.gooTaxClasses())
	}

	// Tax rates grid: two new rows, then an edit and a removal.
	gooStorePost(t, c, page+"&tab=tax&section=standard", url.Values{
		"tax_rate_country[new-0]": {"US"}, "tax_rate_state[new-0]": {"TX"}, "tax_rate_postcode[new-0]": {"73301; 78701"}, "tax_rate_city[new-0]": {"Austin"},
		"tax_rate[new-0]": {"8.25"}, "tax_rate_name[new-0]": {"TX Tax"}, "tax_rate_priority[new-0]": {"1"}, "tax_rate_shipping[new-0]": {"1"}, "tax_rate_order[new-0]": {"0"},
		"tax_rate_country[new-1]": {"CA"}, "tax_rate[new-1]": {"5"}, "tax_rate_name[new-1]": {"GST"}, "tax_rate_priority[new-1]": {"1"}, "tax_rate_compound[new-1]": {"1"}, "tax_rate_order[new-1]": {"1"},
		"tax_rate_country[new-2]": {""}, "tax_rate[new-2]": {""}, "tax_rate_name[new-2]": {""}, // left empty: skipped
	})
	rates, _ := srv.gooTaxRates(ctx, "")
	if len(rates) != 2 || rates[0].Country != "US" || rates[0].Rate != 8.25 || strings.Join(rates[0].Postcodes, ",") != "73301,78701" || !rates[0].Shipping || !rates[1].Compound || rates[1].Shipping {
		t.Fatalf("tax rates: %+v", rates)
	}
	id0, id1 := itoa64(rates[0].ID), itoa64(rates[1].ID)
	gooStorePost(t, c, page+"&tab=tax&section=standard", url.Values{
		"tax_rate_country[" + id0 + "]": {"US"}, "tax_rate_state[" + id0 + "]": {"TX"}, "tax_rate[" + id0 + "]": {"6.25"}, "tax_rate_name[" + id0 + "]": {"TX"},
		"tax_rate_priority[" + id0 + "]": {"2"}, "tax_rate_order[" + id0 + "]": {"0"},
		"tax_rate_country[" + id1 + "]": {"CA"}, "tax_rate[" + id1 + "]": {"5"}, "remove_tax_rate[" + id1 + "]": {"1"},
	})
	rates, _ = srv.gooTaxRates(ctx, "")
	if len(rates) != 1 || rates[0].Rate != 6.25 || rates[0].Priority != 2 || len(rates[0].Postcodes) != 0 {
		t.Fatalf("tax rates after edit: %+v", rates)
	}
	if body := gooStoreGet(t, c, page+"&tab=tax&section=luxury"); !strings.Contains(body, `Tax rates for the "Luxury" class`) {
		t.Error("luxury class section missing")
	}

	// Shipping: a zone with regions and postcodes, a flat rate with class costs, a class.
	gooStorePost(t, c, page+"&tab=shipping&section=classes", url.Values{"new_shipping_class_name": {"Bulky"}, "new_shipping_class_description": {"Big items"}})
	classes := srv.gooShippingClassTerms(ctx)
	if len(classes) != 1 || classes[0].Name != "Bulky" || classes[0].Slug != "bulky" {
		t.Fatalf("classes: %+v", classes)
	}
	cid := itoa64(classes[0].ID)
	gooStorePost(t, c, page+"&tab=shipping&section=classes", url.Values{"shipping_class_name[" + cid + "]": {"Heavy"}, "shipping_class_slug[" + cid + "]": {"heavy"}, "shipping_class_description[" + cid + "]": {"x"}})
	if cl := srv.gooShippingClassTerms(ctx); cl[0].Name != "Heavy" || cl[0].Slug != "heavy" {
		t.Errorf("class edit: %+v", cl[0])
	}
	loc = gooStorePost(t, c, page+"&tab=shipping&zone_id=new", url.Values{"zone_name": {"Texas"}, "zone_locations[]": {"state:US:TX", "continent:EU"},
		"zone_limit_postcodes": {"1"}, "zone_postcodes": {"73301\n787*"}})
	zones, _ := srv.gooZones(ctx, false)
	if len(zones) != 1 || zones[0].Name != "Texas" || len(zones[0].Locations) != 4 {
		t.Fatalf("zone: %+v (%s)", zones, loc)
	}
	zid := itoa64(zones[0].ID)
	gooStorePost(t, c, page+"&tab=shipping&zone_id="+zid, url.Values{"zone_name": {"Texas"}, "zone_locations[]": {"state:US:TX"}, "methods_present": {"1"},
		"goo_do": {"add_method"}, "add_method_id": {"flat_rate"}})
	z, _ := srv.gooZone(ctx, zones[0].ID)
	if len(z.Methods) != 1 || z.Methods[0].MethodID != "flat_rate" || len(z.Locations) != 1 {
		t.Fatalf("zone methods: %+v %+v", z.Methods, z.Locations)
	}
	inst := itoa64(z.Methods[0].InstanceID)
	if body := gooStoreGet(t, c, page+"&tab=shipping&instance_id="+inst); !strings.Contains(body, `name="woocommerce_flat_rate_class_cost_`+cid+`"`) || !strings.Contains(body, `"Heavy" shipping class cost`) {
		t.Error("flat rate lacks the class cost field")
	}
	gooStorePost(t, c, page+"&tab=shipping&instance_id="+inst, url.Values{"woocommerce_flat_rate_title": {"Courier"}, "woocommerce_flat_rate_cost": {"5 * [qty]"},
		"woocommerce_flat_rate_class_cost_" + cid: {"12"}, "woocommerce_flat_rate_no_class_cost": {"1"}, "woocommerce_flat_rate_type": {"order"}, "woocommerce_flat_rate_tax_status": {"none"}})
	m := srv.gooMethodInstance(ctx, z.Methods[0].InstanceID)
	if m.Title() != "Courier" || m.Setting("cost") != "5 * [qty]" || m.Setting("class_cost_"+cid) != "12" || m.Setting("type") != "order" || m.Setting("tax_status") != "none" {
		t.Errorf("method settings: %+v", m.Settings)
	}
	// Disable the method from the zone screen.
	gooStorePost(t, c, page+"&tab=shipping&zone_id="+zid, url.Values{"zone_name": {"Texas"}, "zone_locations[]": {"state:US:TX"}, "methods_present": {"1"}})
	if m := srv.gooMethodInstance(ctx, z.Methods[0].InstanceID); m.Enabled {
		t.Error("method still enabled")
	}
	if body := gooStoreGet(t, c, page+"&tab=shipping"); !strings.Contains(body, "Texas, United States (US)") || !strings.Contains(body, "Rest of the world") {
		t.Error("zones list lacks the region summary")
	}
	gooStorePost(t, c, page+"&tab=shipping&section=options", url.Values{"woocommerce_ship_to_destination": {"shipping"}})
	if srv.gooOptString("ship_to_destination") != "shipping" || srv.gooOptBool("enable_shipping_calc") {
		t.Error("shipping options not saved")
	}

	// Payments: order and toggles, BACS accounts, COD methods.
	gooStorePost(t, c, page+"&tab=checkout", url.Values{"gateway_toggles": {"1"}, "gateway_order[]": {"cod", "bacs", "cheque", "goo_test"}, "gateway_enabled[cod]": {"1"}, "gateway_enabled[bacs]": {"1"}})
	gws := srv.gooGatewaysOrdered()
	if gws[0].ID() != "cod" || gws[1].ID() != "bacs" || !gws[0].Enabled() || !gws[1].Enabled() || gws[2].Enabled() {
		t.Errorf("gateways: %s %s %v %v %v", gws[0].ID(), gws[1].ID(), gws[0].Enabled(), gws[1].Enabled(), gws[2].Enabled())
	}
	gooStorePost(t, c, page+"&tab=checkout&section=bacs", url.Values{"woocommerce_bacs_enabled": {"1"}, "woocommerce_bacs_title": {"Wire"},
		"bacs_account_name[]": {"Main", ""}, "bacs_account_number[]": {"123", ""}, "bacs_bank_name[]": {"Bank", ""}, "bacs_sort_code[]": {"", ""},
		"bacs_iban[]": {"DE89", ""}, "bacs_bic[]": {"BIC1", ""}})
	st := srv.gooGatewaySettings("bacs")
	accounts, _ := st["accounts"].([]any)
	if srv.gooGatewaySetting("bacs", "title") != "Wire" || srv.gooGatewaySetting("bacs", "enabled") != "yes" || len(accounts) != 1 {
		t.Fatalf("bacs: %+v", st)
	}
	if a := accounts[0].(map[string]any); a["iban"] != "DE89" || a["account_name"] != "Main" {
		t.Errorf("bacs account: %+v", a)
	}
	if !strings.Contains(srv.gooBACSDetails(), "DE89") {
		t.Error("bacs details lack the account")
	}
	if body := gooStoreGet(t, c, page+"&tab=checkout&section=bacs"); !strings.Contains(body, `value="DE89" name="bacs_iban[]"`) {
		t.Error("bacs form lacks the saved account")
	}
	gooStorePost(t, c, page+"&tab=checkout&section=cod", url.Values{"woocommerce_cod_enabled": {"1"}, "woocommerce_cod_enable_for_methods[]": {"", "flat_rate:" + inst}})
	if l := gooValList(srv.gooGatewaySettings("cod")["enable_for_methods"]); len(l) != 1 || l[0] != "flat_rate:"+inst || srv.gooGatewaySetting("cod", "enable_for_virtual") != "no" {
		t.Errorf("cod: %+v", srv.gooGatewaySettings("cod"))
	}

	// E-mails: a registered e-mail's settings.
	def := &gooEmailDef{ID: "goo_test_mail", Title: "Test mail", Subject: "Hello {site_title}", Heading: "Hi"}
	gooEmailDefs = append(gooEmailDefs, def)
	defer func() { gooEmailDefs = gooEmailDefs[:len(gooEmailDefs)-1] }()
	if body := gooStoreGet(t, c, page+"&tab=email"); !strings.Contains(body, "Test mail") {
		t.Error("emails list lacks the e-mail")
	}
	gooStorePost(t, c, page+"&tab=email&section=goo_test_mail", url.Values{"woocommerce_goo_test_mail_recipient": {"a@x.test, b@x.test"},
		"woocommerce_goo_test_mail_subject": {"New!"}, "woocommerce_goo_test_mail_email_type": {"plain"}})
	es := srv.gooEmailSettings("goo_test_mail")
	if es["enabled"] != "no" || es["recipient"] != "a@x.test, b@x.test" || es["subject"] != "New!" || es["email_type"] != "plain" {
		t.Errorf("email settings: %+v", es)
	}
}

func TestGooStoreAPIKeysAndWebhooks(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	page := base + "/wp-admin/goocommerce?page=settings&tab=advanced&section=keys"
	body := gooStoreGet(t, c, page+"&create-key=1")
	code, out, _ := post(t, c, page, url.Values{"_wpnonce": {formNonce(t, body)}, "key_description": {"ERP"}, "key_user": {"1"}, "key_permissions": {"read_write"}})
	if code != http.StatusOK {
		t.Fatalf("generate key: %d", code)
	}
	ck := regexp.MustCompile(`id="key_consumer_key" type="text" value="(ck_[0-9a-f]{40})"`).FindStringSubmatch(out)
	cs := regexp.MustCompile(`id="key_consumer_secret" type="text" value="(cs_[0-9a-f]{40})"`).FindStringSubmatch(out)
	if ck == nil || cs == nil {
		t.Fatal("generated keys are not shown")
	}
	var hash, secret, trunc, perm string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT consumer_key, consumer_secret, truncated_key, permissions FROM goo_api_keys WHERE description = 'ERP'`).Scan(&hash, &secret, &trunc, &perm); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(ck[1]))
	if hash != hex.EncodeToString(sum[:]) || secret != cs[1] || trunc != ck[1][len(ck[1])-7:] || perm != "read_write" {
		t.Errorf("stored key: %s %s %s %s", hash, secret, trunc, perm)
	}
	// Shown once: the list and the edit screen only show the truncated key.
	list := gooStoreGet(t, c, page)
	if strings.Contains(list, ck[1]) || strings.Contains(list, cs[1]) || !strings.Contains(list, trunc) {
		t.Error("keys list shows the secret or lacks the truncated key")
	}
	var id int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM goo_api_keys`).Scan(&id)
	if edit := gooStoreGet(t, c, page+"&edit-key="+itoa64(id)); strings.Contains(edit, cs[1]) {
		t.Error("edit screen shows the secret")
	}
	gooStorePost(t, c, page, url.Values{"goo_do": {"revoke:" + itoa64(id)}})
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_api_keys`).Scan(&n)
	if n != 0 {
		t.Error("key not revoked")
	}
	// Webhooks.
	hooks := base + "/wp-admin/goocommerce?page=settings&tab=advanced&section=webhooks"
	body = gooStoreGet(t, c, hooks+"&edit-webhook=0")
	_, _, loc := post(t, c, hooks, url.Values{"_wpnonce": {formNonce(t, body)}, "webhook_name": {"Orders"}, "webhook_status": {"active"}, "webhook_topic": {"order.created"},
		"webhook_delivery_url": {"https://example.test/hook"}, "webhook_secret": {"s3cret"}})
	if !strings.Contains(loc, "edit-webhook=") {
		t.Fatalf("webhook save: %s", loc)
	}
	var topic, urlStr string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT topic, delivery_url FROM goo_webhooks WHERE name = 'Orders'`).Scan(&topic, &urlStr)
	if topic != "order.created" || urlStr != "https://example.test/hook" {
		t.Errorf("webhook: %s %s", topic, urlStr)
	}
	_, _, loc = post(t, c, hooks, url.Values{"_wpnonce": {formNonce(t, body)}, "webhook_name": {"Bad"}, "webhook_topic": {"order.created"}, "webhook_delivery_url": {"ftp://x"}})
	if !strings.Contains(loc, "goo-error=") {
		t.Error("invalid delivery URL accepted")
	}
	if list := gooStoreGet(t, c, hooks); !strings.Contains(list, "https://example.test/hook") {
		t.Error("webhooks list lacks the webhook")
	}
}

func TestGooStoreCoupons(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Coupon Mug", 20, nil)
	page := base + "/wp-admin/goocommerce?page=coupons&action=new"
	body := gooStoreGet(t, c, page)
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "post_title": {"SPRING25"}, "excerpt": {"Spring sale"}, "discount_type": {"percent"}, "coupon_amount": {"25"},
		"free_shipping": {"yes"}, "expiry_date": {"2030-05-01"}, "minimum_amount": {"10"}, "individual_use": {"yes"}, "product_ids[]": {itoa64(p.ID)},
		"customer_email": {"a@x.test, *@y.test"}, "usage_limit": {"100"}, "usage_limit_per_user": {"1"}, "publish": {"Publish"}}
	code, _, loc := post(t, c, page, form)
	if code != http.StatusFound || !strings.Contains(loc, "action=edit") {
		t.Fatalf("create coupon: %d %s", code, loc)
	}
	cp, err := srv.gooCouponByCode(ctx, "spring25")
	if err != nil {
		t.Fatal(err)
	}
	if cp.DiscountType != "percent" || cp.Amount != 25 || !cp.FreeShipping || cp.DateExpires == nil || cp.DateExpires.In(srv.loc()).Format("2006-01-02") != "2030-05-01" ||
		*cp.MinimumAmount != 10 || !cp.IndividualUse || len(cp.ProductIDs) != 1 || len(cp.EmailRestrictions) != 2 || *cp.UsageLimit != 100 || *cp.UsageLimitPerUser != 1 || cp.Status != "publish" {
		t.Errorf("coupon: %+v", cp)
	}
	edit := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=coupons&action=edit&id="+itoa64(cp.ID))
	for _, want := range []string{`value="spring25"`, `<option value="percent" selected="selected">`, `value="2030-05-01"`, `selected="selected">Coupon Mug`} {
		if !strings.Contains(edit, want) {
			t.Errorf("edit form lacks %s", want)
		}
	}
	// A duplicate code is refused with WooCommerce's error, the form keeps the values.
	form.Set("_wpnonce", formNonce(t, body))
	code, out, _ := post(t, c, page, form)
	if code != http.StatusOK || !strings.Contains(out, "Coupon code already exists") || !strings.Contains(out, `value="Spring sale"`) && !strings.Contains(out, "Spring sale") {
		t.Errorf("duplicate code: %d", code)
	}
	form.Set("post_title", "")
	code, out, _ = post(t, c, page, form)
	if code != http.StatusOK || !strings.Contains(out, "Please enter a coupon code.") {
		t.Errorf("empty code: %d", code)
	}
	// List, search, trash, restore, delete.
	list := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=coupons")
	if !strings.Contains(list, "<code>spring25</code>") || !strings.Contains(list, "Percentage discount") || !strings.Contains(list, "0 / 100") {
		t.Error("coupon list row")
	}
	if l := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=coupons&s=nomatch"); strings.Contains(l, "<code>spring25</code>") {
		t.Error("search")
	}
	gooStorePost(t, c, base+"/wp-admin/goocommerce?page=coupons", url.Values{"action": {"trash"}, "post[]": {itoa64(cp.ID)}})
	if cp, _ = srv.gooCouponByID(ctx, cp.ID); cp.Status != "trash" {
		t.Error("not trashed")
	}
	gooStorePost(t, c, base+"/wp-admin/goocommerce?page=coupons&post_status=trash", url.Values{"action": {"untrash"}, "post[]": {itoa64(cp.ID)}})
	if cp, _ = srv.gooCouponByID(ctx, cp.ID); cp.Status != "publish" {
		t.Errorf("restored as %s", cp.Status)
	}
	if body := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=marketing"); !strings.Contains(body, "spring25") {
		t.Error("marketing lacks the coupon")
	}
}

// gooOrderForm reads the order editor's current form (hidden fields included) for re-posting.
func gooOrderForm(t *testing.T, body string) url.Values {
	t.Helper()
	f := url.Values{}
	f.Set("_wpnonce", formNonce(t, body))
	for _, m := range regexp.MustCompile(`<input[^>]*name="([^"]+)"[^>]*value="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		name := html.UnescapeString(m[1])
		if strings.HasPrefix(name, "refund_") || name == "_wpnonce" || strings.Contains(m[0], `type="checkbox"`) || strings.Contains(m[0], `type="search"`) {
			continue
		}
		f.Add(name, html.UnescapeString(m[2]))
	}
	return f
}

func TestGooStoreOrderEditor(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"calc_taxes": true, "tax_based_on": "billing", "manage_stock": true}); err != nil {
		t.Fatal(err)
	}
	_ = srv.gooSaveTaxRate(ctx, &gooTaxRate{Country: "US", State: "CA", Rate: 10, Name: "CA Tax", Priority: 1, Shipping: true})
	p := gooMakeSimple(t, srv, "Editor Lamp", 50, func(p *gooProduct) { p.ManageStock, p.StockQuantity, p.SKU = true, gooIntPtr(10), "LAMP-1" })
	cp := &gooCoupon{Code: "tenoff", DiscountType: "fixed_cart", Amount: 10}
	_ = srv.gooSaveCoupon(ctx, cp)

	// Add order → a draft.
	_, _, loc := get(t, c, base+"/wp-admin/goocommerce?page=orders&action=new")
	id, _ := strconv.ParseInt(regexp.MustCompile(`id=(\d+)`).FindStringSubmatch(loc)[1], 10, 64)
	page := base + "/wp-admin/goocommerce?page=orders&action=edit&id=" + itoa64(id)
	body := gooStoreGet(t, c, page)
	if !strings.Contains(body, "Add new order") || !strings.Contains(body, ">Create</button>") {
		t.Fatal("new order screen")
	}
	if l := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=orders"); strings.Contains(l, "order-"+itoa64(id)+`"`) {
		t.Error("the draft is listed")
	}
	// Billing and an item (2 lamps).
	f := gooOrderForm(t, body)
	f.Set("_billing_first_name", "Grace")
	f.Set("_billing_last_name", "Hopper")
	f.Set("_billing_email", "grace@example.test")
	f.Set("_billing_country", "US")
	f.Set("_billing_state", "CA")
	f.Set("_billing_postcode", "90210")
	f.Set("_payment_method", "goo_test")
	f.Set("goo_do", "add_item")
	f.Set("add_item_id", itoa64(p.ID))
	f.Set("add_item_qty", "2")
	if code, out, loc := post(t, c, page, f); code != http.StatusFound || strings.Contains(loc, "goo-error") {
		t.Fatalf("add item: %d %s %s", code, loc, out)
	}
	o, _ := srv.gooGetOrder(ctx, id)
	if len(o.ItemsOf("line_item")) != 1 || o.ItemsOf("line_item")[0].Qty() != 2 || o.Billing["first_name"] != "Grace" || o.Status != "checkout-draft" {
		t.Fatalf("after add item: %+v %+v", o.Items, o.Billing)
	}
	gooAlmost(t, "line tax", o.ItemsOf("line_item")[0].TotalTax, 10)
	gooAlmost(t, "total", o.Total, 110)
	// Shipping line with a cost, a fee, then Recalculate.
	body = gooStoreGet(t, c, page)
	f = gooOrderForm(t, body)
	f.Set("goo_do", "add_shipping")
	post(t, c, page, f)
	o, _ = srv.gooGetOrder(ctx, id)
	ship := o.ItemsOf("shipping")[0]
	body = gooStoreGet(t, c, page)
	f = gooOrderForm(t, body)
	f.Set("shipping_cost["+itoa64(ship.ID)+"]", "5")
	f.Set("shipping_method_title["+itoa64(ship.ID)+"]", "Courier")
	f.Set("shipping_method["+itoa64(ship.ID)+"]", "flat_rate")
	f.Set("goo_do", "recalculate")
	if _, _, loc := post(t, c, page, f); strings.Contains(loc, "goo-error") {
		t.Fatal(loc)
	}
	o, _ = srv.gooGetOrder(ctx, id)
	gooAlmost(t, "shipping", o.ShippingTotal, 5)
	gooAlmost(t, "shipping tax", o.ShippingTax, 0.5)
	gooAlmost(t, "total after recalc", o.Total, 115.5)
	if o.ItemsOf("shipping")[0].Name != "Courier" || len(o.ItemsOf("tax")) != 1 {
		t.Errorf("shipping line / tax lines: %+v", o.Items)
	}
	// Apply a coupon: 10 off the items, taxes recomputed.
	body = gooStoreGet(t, c, page)
	f = gooOrderForm(t, body)
	f.Set("goo_do", "apply_coupon")
	f.Set("coupon_code", "TENOFF")
	post(t, c, page, f)
	o, _ = srv.gooGetOrder(ctx, id)
	gooAlmost(t, "discount", o.DiscountTotal, 10)
	gooAlmost(t, "total with coupon", o.Total, 104.5)
	// Create as processing: stock reduced, note added.
	body = gooStoreGet(t, c, page)
	if !strings.Contains(body, "tenoff") || !strings.Contains(body, "LAMP-1") {
		t.Error("items box lacks the coupon or the SKU")
	}
	f = gooOrderForm(t, body)
	f.Set("order_status", "wc-processing")
	f.Set("goo_do", "save")
	_, _, loc = post(t, c, page, f)
	if !strings.Contains(loc, "Order+created") {
		t.Errorf("create: %s", loc)
	}
	o, _ = srv.gooGetOrder(ctx, id)
	if o.Status != "processing" || !o.StockReduced {
		t.Fatalf("status %s, stock reduced %v", o.Status, o.StockReduced)
	}
	pp, _ := srv.gooGetProduct(ctx, p.ID)
	if *pp.StockQuantity != 8 {
		t.Errorf("stock = %d, want 8", *pp.StockQuantity)
	}
	// Notes: private and customer, then delete one.
	body = gooStoreGet(t, c, page)
	f = gooOrderForm(t, body)
	f.Set("goo_do", "add_note")
	f.Set("order_note", "Called the <customer>")
	f.Set("order_note_type", "customer")
	post(t, c, page, f)
	notes, _ := srv.gooOrderNotes(ctx, id, true)
	if len(notes) != 1 || notes[0].Content != "Called the &lt;customer&gt;" {
		t.Fatalf("customer note: %+v", notes)
	}
	body = gooStoreGet(t, c, page)
	if !strings.Contains(body, `class="note customer-note"`) || !strings.Contains(body, "system-note") {
		t.Error("notes styling")
	}
	f = gooOrderForm(t, body)
	f.Set("goo_do", "delete_note:"+itoa64(notes[0].ID))
	post(t, c, page, f)
	if notes, _ = srv.gooOrderNotes(ctx, id, true); len(notes) != 0 {
		t.Error("note not deleted")
	}
	// Refund 1 lamp with restock (the test gateway supports refunds).
	o, _ = srv.gooGetOrder(ctx, id)
	line := o.ItemsOf("line_item")[0]
	body = gooStoreGet(t, c, page)
	if !strings.Contains(body, "via Test payment") {
		t.Error("no API refund button for the test gateway")
	}
	f = gooOrderForm(t, body)
	lid := itoa64(line.ID)
	rate := ""
	for k := range line.Taxes.Total {
		rate = k
	}
	f.Set("goo_do", "refund_api")
	f.Set("refund_order_item_qty["+lid+"]", "1")
	f.Set("refund_line_total["+lid+"]", "45")
	f.Set("refund_line_tax["+lid+"]["+rate+"]", "4.5")
	f.Set("restock_refunded_items", "on")
	f.Set("refund_reason", "Broken")
	if _, _, loc := post(t, c, page, f); strings.Contains(loc, "goo-error") {
		t.Fatal(loc)
	}
	gooAlmost(t, "refunded", srv.gooTotalRefunded(ctx, id), 49.5)
	pp, _ = srv.gooGetProduct(ctx, p.ID)
	if *pp.StockQuantity != 9 {
		t.Errorf("stock after restock = %d, want 9", *pp.StockQuantity)
	}
	body = gooStoreGet(t, c, page)
	if !strings.Contains(body, "Broken") || !strings.Contains(body, "Net Payment:") || !strings.Contains(body, `<small class="refunded">-1</small>`) {
		t.Error("refund not shown in the items box")
	}
	// Delete the refund.
	refunds, _ := srv.gooRefunds(ctx, id)
	f = gooOrderForm(t, body)
	f.Set("goo_do", "delete_refund:"+itoa64(refunds[0].ID))
	post(t, c, page, f)
	if srv.gooTotalRefunded(ctx, id) != 0 {
		t.Error("refund not deleted")
	}
	// Order action: resend new order notification (records a note even without an e-mail sender).
	body = gooStoreGet(t, c, page)
	f = gooOrderForm(t, body)
	f.Set("order_status", "wc-processing")
	f.Set("wc_order_action", "send_order_details_admin")
	f.Set("goo_do", "save")
	_, _, loc = post(t, c, page, f)
	if !strings.Contains(loc, "Order+updated") {
		t.Errorf("update: %s", loc)
	}
	all, _ := srv.gooOrderNotes(ctx, id, false)
	found := false
	for _, n := range all {
		if strings.Contains(n.Content, "manually sent to admin") {
			found = true
		}
	}
	if !found {
		t.Error("order action did not run")
	}
	// Cancel from the editor: stock restored.
	f = gooOrderForm(t, gooStoreGet(t, c, page))
	f.Set("order_status", "wc-cancelled")
	f.Set("goo_do", "save")
	post(t, c, page, f)
	pp, _ = srv.gooGetProduct(ctx, p.ID)
	if *pp.StockQuantity != 10 {
		t.Errorf("stock after cancel = %d, want 10", *pp.StockQuantity)
	}
	// Downloads: grant access to a downloadable product.
	dl := gooMakeSimple(t, srv, "E-book", 5, func(p *gooProduct) {
		p.Downloadable, p.Virtual = true, true
		p.Downloads = []gooDownload{{ID: "f1", Name: "book.pdf", File: "https://example.test/book.pdf"}}
	})
	f = gooOrderForm(t, gooStoreGet(t, c, page))
	f.Set("goo_do", "grant_access")
	f.Set("grant_access_id[]", itoa64(dl.ID))
	post(t, c, page, f)
	body = gooStoreGet(t, c, page)
	if !strings.Contains(body, "book.pdf") {
		t.Fatal("permission not listed")
	}
	var pid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM goo_download_permissions WHERE order_id = $1`, id).Scan(&pid)
	f = gooOrderForm(t, body)
	f.Set("goo_do", "revoke_access:"+itoa64(pid))
	post(t, c, page, f)
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_download_permissions WHERE order_id = $1`, id).Scan(&n)
	if n != 0 {
		t.Error("permission not revoked")
	}
}

func TestGooStoreOrdersList(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "List Pen", 3, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(20) })
	mk := func(first, email, status string) *gooOrder {
		o := &gooOrder{Status: "pending", Billing: map[string]string{"first_name": first, "last_name": "Test", "email": email, "country": "US"}, Shipping: map[string]string{},
			Items: []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: 2, Subtotal: 6, Total: 6}}}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		if status != "pending" {
			_ = srv.gooSetStatus(ctx, o, status, "", "")
		}
		return o
	}
	a := mk("Alice", "alice@example.test", "processing")
	b := mk("Bob", "bob@example.test", "on-hold")
	cc := mk("Carol", "carol@example.test", "pending")
	page := base + "/wp-admin/goocommerce?page=orders"
	body := gooStoreGet(t, c, page)
	for _, want := range []string{`Processing <span class="count">(1)</span>`, `On hold <span class="count">(1)</span>`, `All <span class="count">(3)</span>`,
		"#" + itoa64(a.ID) + " Alice Test", `status-processing`, `goo-order-preview-` + itoa64(b.ID)} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %s", want)
		}
	}
	if l := gooStoreGet(t, c, page+"&status=wc-on-hold"); !strings.Contains(l, "Bob Test") || strings.Contains(l, "Alice Test") {
		t.Error("status view")
	}
	if l := gooStoreGet(t, c, page+"&s=carol"); !strings.Contains(l, "Carol Test") || strings.Contains(l, "Bob Test") {
		t.Error("search")
	}
	if l := gooStoreGet(t, c, page+"&m="+time.Now().In(srv.loc()).Format("200601")); !strings.Contains(l, "Carol Test") {
		t.Error("month filter")
	}
	// Bulk: complete Bob and Carol.
	gooStorePost(t, c, page, url.Values{"action": {"mark_completed"}, "id[]": {itoa64(b.ID), itoa64(cc.ID)}})
	for _, id := range []int64{b.ID, cc.ID} {
		if o, _ := srv.gooGetOrder(ctx, id); o.Status != "completed" {
			t.Errorf("order %d is %s", id, o.Status)
		}
	}
	if pp, _ := srv.gooGetProduct(ctx, p.ID); *pp.StockQuantity != 14 {
		t.Errorf("stock after bulk: %d", *pp.StockQuantity)
	}
	// Trash, restore, delete permanently.
	gooStorePost(t, c, page, url.Values{"action": {"trash"}, "id[]": {itoa64(a.ID)}})
	if o, _ := srv.gooGetOrder(ctx, a.ID); o.Status != "trash" {
		t.Fatal("not trashed")
	}
	if l := gooStoreGet(t, c, page+"&status=trash"); !strings.Contains(l, "Alice Test") || !strings.Contains(l, "Restore") {
		t.Error("trash view")
	}
	gooStorePost(t, c, page+"&status=trash", url.Values{"action": {"untrash"}, "id[]": {itoa64(a.ID)}})
	if o, _ := srv.gooGetOrder(ctx, a.ID); o.Status != "processing" {
		t.Errorf("restored as %s", o.Status)
	}
	gooStorePost(t, c, page, url.Values{"action": {"trash"}, "id[]": {itoa64(a.ID)}})
	gooStorePost(t, c, page+"&status=trash", url.Values{"action2": {"delete"}, "id[]": {itoa64(a.ID)}})
	if _, err := srv.gooGetOrder(ctx, a.ID); err == nil {
		t.Error("not deleted")
	}
	// The Complete action button.
	d := mk("Dan", "dan@example.test", "processing")
	body = gooStoreGet(t, c, page)
	nonce := formNonce(t, body)
	if code, _, _ := get(t, c, page+"&action=mark_status&status=complete&id="+itoa64(d.ID)+"&_wpnonce="+nonce); code != http.StatusFound {
		t.Fatal("mark status")
	}
	if o, _ := srv.gooGetOrder(ctx, d.ID); o.Status != "completed" {
		t.Errorf("mark complete: %s", o.Status)
	}
}

func TestGooStoreCustomers(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	uid, err := srv.gooCreateCustomer(ctx, "reg@example.test", "regcust", "pw-long-enough", map[string]string{"billing_first_name": "Regina", "billing_last_name": "Reg"})
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.gooSetUserMeta(ctx, uid, map[string]any{"billing_city": "Berlin", "billing_country": "DE", "billing_postcode": "10115"})
	for _, o := range []*gooOrder{
		{Status: "completed", CustomerID: uid, Billing: map[string]string{"first_name": "Regina", "email": "reg@example.test"}, Total: 40},
		{Status: "processing", CustomerID: uid, Billing: map[string]string{"first_name": "Regina", "email": "reg@example.test"}, Total: 20},
		{Status: "processing", Billing: map[string]string{"first_name": "Gus", "last_name": "Guest", "email": "gus@example.test", "city": "Paris", "country": "FR"}, Total: 15},
	} {
		o.Shipping = map[string]string{}
		if err := srv.gooSaveOrder(ctx, o, false); err != nil {
			t.Fatal(err)
		}
	}
	body := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=customers")
	for _, want := range []string{"Regina Reg", "regcust", "Berlin", "Gus Guest", "gus@example.test", "Paris", "France"} {
		if !strings.Contains(body, want) {
			t.Errorf("customers lack %s", want)
		}
	}
	rows := srv.gooCustomers(ctx)
	for _, r := range rows {
		if r.UserID == uid && (r.Orders != 2 || r.Spend != 60 || r.AOV != 30) {
			t.Errorf("registered: %+v", r)
		}
		if r.Email == "gus@example.test" && (r.Orders != 1 || r.Spend != 15 || r.UserID != 0) {
			t.Errorf("guest: %+v", r)
		}
	}
	if b := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=customers&s=paris"); strings.Contains(b, "Regina Reg") || !strings.Contains(b, "Gus Guest") {
		t.Error("search")
	}
}

func TestGooStoreReportsAnalytics(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Report Hat", 25, nil)
	now := time.Now()
	for i, st := range []string{"completed", "processing", "pending"} {
		o := &gooOrder{Status: st, Billing: map[string]string{"first_name": "R" + strconv.Itoa(i), "email": "r" + strconv.Itoa(i) + "@example.test"}, Shipping: map[string]string{},
			Items: []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: float64(i + 1), Subtotal: 25 * float64(i+1), Total: 25 * float64(i+1), TotalTax: 2},
				{Type: "shipping", Name: "Flat", Total: 5}}}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		_ = now
	}
	// Two paid orders: totals 25+2+5=32 and 50+2+5=57 → gross 89, tax 4, shipping 10, items 3.
	from := time.Now().Add(-24 * time.Hour)
	tt := srv.gooTotals(ctx, from, time.Now().Add(time.Hour))
	if tt.Orders != 2 || tt.Items != 3 {
		t.Fatalf("totals: %+v", tt)
	}
	gooAlmost(t, "gross", tt.Gross, 89)
	gooAlmost(t, "net", tt.Net, 75)
	perf := srv.gooPerformance(ctx, from, time.Now().Add(time.Hour))
	gooAlmost(t, "net sales", perf.NetSales, 75)
	gooAlmost(t, "aov", perf.AOV, 37.5)
	body := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=reports&tab=orders&report=sales_by_date&range=7day")
	if !strings.Contains(body, "gross sales in this period") || !strings.Contains(body, "89.00") || !strings.Contains(body, "<svg class=\"goo-chart\"") {
		t.Error("sales by date")
	}
	if !strings.Contains(body, "<strong>3</strong> items purchased") || !strings.Contains(body, "<strong>2</strong> orders placed") {
		t.Error("legend counts")
	}
	body = gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=reports&tab=orders&report=sales_by_product")
	if !strings.Contains(body, "Report Hat") || !strings.Contains(body, "75.00") {
		t.Error("sales by product")
	}
	body = gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&period=month")
	for _, want := range []string{"Total sales", "Net sales", "75.00", "Top products - Items sold", "Report Hat"} {
		if !strings.Contains(body, want) {
			t.Errorf("analytics overview lacks %s", want)
		}
	}
	body = gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&path=products&period=month")
	if !strings.Contains(body, "Report Hat") {
		t.Error("analytics products")
	}
	resp, _ := c.Get(base + "/wp-admin/goocommerce?page=analytics&path=orders&period=month&download=csv")
	b := bodyOf(resp)
	if !strings.Contains(b, "Order #") || strings.Count(b, "\n") != 3 {
		t.Errorf("orders csv: %q", b)
	}
	// Home shows today's sales.
	if body := gooStoreGet(t, c, base+"/wp-admin/goocommerce"); !strings.Contains(body, "89.00") || !strings.Contains(body, "Things to do next") {
		t.Error("home stats")
	}
}

func TestGooStoreStatusTools(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	body := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=status")
	for _, want := range []string{"### Pursae Commerce System Status Report ###", "goo_orders", "[goocommerce_cart] &#10004;", "PostgreSQL"} {
		if !strings.Contains(body, want) {
			t.Errorf("status lacks %s", want)
		}
	}
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO goo_sessions (session_key, data, expires_at) VALUES ('old', '{}', now() - interval '1 day'), ('new', '{}', now() + interval '1 day')`)
	_ = srv.gooSetOpts(ctx, map[string]any{"cart_page_id": float64(0)})
	page := base + "/wp-admin/goocommerce?page=status&tab=tools"
	for _, tool := range gooTools {
		loc := gooStorePost(t, c, page, url.Values{"action": {tool.ID}})
		if !strings.Contains(loc, "goo-msg=") {
			t.Errorf("%s: %s", tool.ID, loc)
		}
		if tool.ID == "clear_expired_sessions" {
			var n int
			_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_sessions`).Scan(&n)
			if n != 1 {
				t.Errorf("sessions after clearing expired: %d", n)
			}
		}
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("sessions after clearing all: %d", n)
	}
	if srv.gooPageID("cart") == 0 {
		t.Error("install pages did not restore the cart page")
	}
	// A tool without a nonce is refused.
	if code, _, _ := post(t, c, page, url.Values{"action": {"clear_sessions"}}); code == http.StatusFound {
		t.Error("tool ran without a nonce")
	}
	if body := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=status&tab=action-scheduler"); !strings.Contains(body, "goocommerce_daily") {
		t.Error("scheduled actions lack the daily job")
	}
}
