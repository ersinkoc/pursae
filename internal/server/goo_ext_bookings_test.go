package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// gxPostCtx is a gin context carrying a posted form (a product panel's Save, an admin form).
func gxPostCtx(form url.Values) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = c.Request.ParseForm()
	return c
}

// gxGetCtx is a GET request context with a recorder to read the response.
func gxGetCtx(target string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c, w
}

// gxSavePanels runs a module's product panels' Save with a form, saves and reloads the product.
func gxSavePanels(t *testing.T, srv *Server, p *gooProduct, module string, form url.Values) *gooProduct {
	t.Helper()
	c := gxPostCtx(form)
	for _, pn := range gooProductPanels {
		if pn.Module == module {
			if out := pn.Render(srv, gooTestCtx(), p); out == "" {
				t.Errorf("panel %s renders nothing", pn.ID)
			}
			pn.Save(srv, c, p)
		}
	}
	if err := srv.gooSaveProduct(context.Background(), p, 1); err != nil {
		t.Fatal(err)
	}
	out, err := srv.gooGetProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func gxTestGateway(t *testing.T, srv *Server) {
	t.Helper()
	if err := srv.gooSetOpts(context.Background(), map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
}

var gxCheckoutForm = map[string][]string{
	"billing_first_name": {"Ada"}, "billing_last_name": {"Lovelace"}, "billing_country": {"US"}, "billing_address_1": {"1 Main St"},
	"billing_city": {"Beverly Hills"}, "billing_state": {"CA"}, "billing_postcode": {"90210"}, "billing_phone": {"555-1234"},
	"billing_email": {"buyer@example.test"}, "payment_method": {"goo_test"},
}

func gxCheckout(t *testing.T, srv *Server, c *gin.Context, method string) (*gooOrder, []string) {
	t.Helper()
	form := map[string][]string{}
	for k, v := range gxCheckoutForm {
		form[k] = v
	}
	form["payment_method"] = []string{method}
	_, o, errs := srv.gooProcessCheckout(c, form)
	if o != nil {
		o, _ = srv.gooGetOrder(context.Background(), o.ID)
	}
	return o, errs
}

func gxMailCount(t *testing.T, srv *Server, subject string) int {
	t.Helper()
	var n int
	_ = srv.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE name = 'wp_mail' AND payload->>'Subject' LIKE '%' || $1 || '%'`, subject).Scan(&n)
	return n
}

// gxNextWeekday is the first given weekday at least 7 days ahead (UTC midnight).
func gxNextWeekday(wd time.Weekday) time.Time {
	d := time.Now().UTC().AddDate(0, 0, 7)
	d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	for d.Weekday() != wd {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

func TestGooExtBookings(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)

	p := gooNewProduct(bkType)
	p.Name = "Kayak tour"
	panelForm := url.Values{
		"_wc_booking_duration_type": {"fixed"}, "_wc_booking_duration": {"1"}, "_wc_booking_duration_unit": {"hour"},
		"_wc_booking_qty": {"2"}, "_wc_booking_min_date": {"0"}, "_wc_booking_min_date_unit": {"day"}, "_wc_booking_max_date": {"12"},
		"_wc_booking_max_date_unit": {"month"}, "_wc_booking_first_block_time": {"09:00"}, "_wc_booking_user_can_cancel": {"yes"},
		"_wc_booking_cancel_limit": {"1"}, "_wc_booking_cancel_limit_unit": {"day"},
		"_wc_booking_default_date_availability": {"non-available"},
		"wc_booking_availability_type":          {"time"}, "wc_booking_availability_from": {"09:00"}, "wc_booking_availability_to": {"12:00"},
		"wc_booking_availability_bookable": {"yes"}, "wc_booking_availability_priority": {"10"},
		"_wc_booking_cost": {"10"}, "_wc_booking_block_cost": {"20"},
		"wc_booking_pricing_type[]": {"days"}, "wc_booking_pricing_from[]": {"6"}, "wc_booking_pricing_to[]": {"7"},
		"wc_booking_pricing_base_cost_modifier[]": {"plus"}, "wc_booking_pricing_base_cost[]": {"0"},
		"wc_booking_pricing_cost_modifier[]": {"times"}, "wc_booking_pricing_cost[]": {"2"},
		"_wc_booking_has_persons": {"yes"}, "_wc_booking_min_persons_group": {"1"}, "_wc_booking_has_person_types": {"yes"},
		"person_id[]": {"adult", "child"}, "person_name[]": {"Adults", "Children"}, "person_block_cost[]": {"5", "0"}, "person_cost[]": {"0", "0"},
		"person_min[]": {"0", "0"}, "person_max[]": {"", ""}, "person_description[]": {"", "Under 12"},
	}
	// The availability rows use [] names.
	for _, k := range []string{"type", "from", "to", "bookable", "priority"} {
		panelForm["wc_booking_availability_"+k+"[]"] = panelForm["wc_booking_availability_"+k]
		delete(panelForm, "wc_booking_availability_"+k)
	}

	// Off: the type is not purchasable and the hooks do nothing.
	p = gxSavePanels(t, srv, p, bkKey, panelForm)
	if srv.gooIsPurchasable(ctx, p) {
		t.Error("a bookable product is purchasable while Bookings is off")
	}
	if h := srv.gooFilterPriceHTML(ctx, p, "x"); h != "x" {
		t.Errorf("price html changed while off: %s", h)
	}
	if out := srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", p); out != "" {
		t.Error("booking form shown while off")
	}

	if err := srv.gooSetExtension(ctx, bkKey, true); err != nil {
		t.Fatal(err)
	}
	conf := bkConfOf(p)
	if conf.Unit != "hour" || conf.MaxPerBlock != 2 || len(conf.Rules) != 1 || len(conf.CostRules) != 1 || len(conf.PersonTypes) != 2 || !p.Virtual || !p.SoldIndividually {
		t.Fatalf("saved booking settings: %+v virtual %v", conf, p.Virtual)
	}
	if p.RegularPrice == nil || *p.RegularPrice != 35 { // 10 + 20 + one adult × 5
		t.Errorf("from price stored as %v, want 35", *p.RegularPrice)
	}
	if h := string(srv.gooFilterPriceHTML(ctx, p, "")); !strings.Contains(h, "From:") {
		t.Errorf("price html: %s", h)
	}
	if out := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", p)); !strings.Contains(out, "wc-bookings-booking-form") || !strings.Contains(out, "wc_bookings_field_persons_adult") {
		t.Errorf("booking form: %s", out)
	}

	monday := gxNextWeekday(time.Monday)
	slots := srv.bkSlots(ctx, p, conf, monday)
	if len(slots) != 3 || slots[0].Time != "09:00" || slots[2].Time != "11:00" || slots[0].Available != 2 {
		t.Fatalf("slots: %+v", slots)
	}

	// The month route lists the days the calendar greys out: past days, not the open Monday.
	month := monday.Format("2006-01")
	mc, mw := gxGetCtx("/?goo-ajax=booking_month&product_id=" + itoa64(p.ID) + "&month=" + month)
	if !bkFrontRoute(srv, mc, "") {
		t.Fatal("the month route did not answer")
	}
	var mres struct {
		Unavailable []string `json:"unavailable"`
	}
	_ = json.Unmarshal(mw.Body.Bytes(), &mres)
	if containsStr(mres.Unavailable, monday.Format("2006-01-02")) {
		t.Errorf("an open Monday is greyed out: %s", mw.Body.String())
	}
	yesterday := time.Now().In(srv.gxLoc()).AddDate(0, 0, -1)
	if yesterday.Format("2006-01") == month && !containsStr(mres.Unavailable, yesterday.Format("2006-01-02")) {
		t.Errorf("a past day is bookable: %s", mw.Body.String())
	}
	if past := srv.gooBkUnavailableDays(ctx, p, conf, time.Date(2020, 1, 1, 0, 0, 0, 0, srv.gxLoc()), 0); len(past) != 31 {
		t.Errorf("a past month has %d unavailable days, want 31", len(past))
	}

	// The AJAX route: slots and the cost of 2 adults at 10:00 = 10 + 20 + 2 × 5.
	rc, w := gxGetCtx("/?goo-ajax=booking_slots&product_id=" + itoa64(p.ID) + "&date=" + monday.Format("2006-01-02") + "&time=10:00&wc_bookings_field_persons_adult=2")
	if !bkFrontRoute(srv, rc, "") {
		t.Fatal("the slots route did not answer")
	}
	var res struct {
		Slots []bkSlot `json:"slots"`
		Cost  float64  `json:"cost"`
		Error string   `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Slots) != 3 || res.Cost != 40 || res.Error != "" {
		t.Errorf("slots route: %s", w.Body.String())
	}

	form := func(day time.Time, tm string, adults int) map[string][]string {
		return map[string][]string{"wc_bookings_field_start_date": {day.Format("2006-01-02")}, "wc_bookings_field_start_date_time": {tm},
			"wc_bookings_field_persons_adult": {itoa(adults)}}
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	if _, err := cart.Add(ctx, p.ID, 1, 0, nil, form(monday, "14:00", 1), nil); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("a booking outside the rules: %v", err)
	}
	if _, err := cart.Add(ctx, p.ID, 1, 0, nil, form(monday, "10:00", 0), nil); err == nil || !strings.Contains(err.Error(), "minimum persons") {
		t.Errorf("zero persons: %v", err)
	}
	key, err := cart.Add(ctx, p.ID, 1, 0, nil, form(monday, "10:00", 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	it := cart.Item(key)
	gooAlmost(t, "booking price", it.Price, 40)
	data := srv.gooItemData(ctx, it)
	joined := ""
	for _, kv := range data {
		joined += kv.Key + "=" + kv.Value + ";"
	}
	for _, want := range []string{"Booking Date=", "Booking Time=", "Duration=1 hour", "Adults=2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("item data lacks %q: %s", want, joined)
		}
	}
	// Saturday: the block cost is doubled (10 + 30 × 2).
	saturday := gxNextWeekday(time.Saturday)
	c2 := gooTestCtx()
	cart2 := srv.gooCartFor(c2)
	k2, err := cart2.Add(ctx, p.ID, 1, 0, nil, form(saturday, "09:00", 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "weekend price", cart2.Item(k2).Price, 70)
	// Capacity: two bookings per block (one held in cart already).
	if _, err := cart2.Add(ctx, p.ID, 1, 0, nil, form(monday, "10:00", 1), nil); err != nil {
		t.Fatalf("second booking of the block: %v", err)
	}
	if _, err := cart2.Add(ctx, p.ID, 1, 0, nil, form(monday, "10:00", 1), nil); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Errorf("a third booking of a full block: %v", err)
	}

	// Checkout: the booking follows the order (unpaid → paid).
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	bks := srv.bkQuery(ctx, `order_id = $1`, o.ID)
	if len(bks) != 1 || bks[0].Status != "paid" || bks[0].OrderItemID == 0 {
		t.Fatalf("bookings of the order: %+v", bks)
	}
	if gxMailCount(t, srv, "New booking") == 0 {
		t.Error("no new booking e-mail")
	}
	// Cancelling the order cancels the booking.
	if err := srv.gooSetStatus(ctx, o, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := srv.bkGet(ctx, bks[0].ID); b.Status != "cancelled" {
		t.Errorf("booking after the order was cancelled: %s", b.Status)
	}

	// Requires confirmation: "Check booking availability" places the order unpaid.
	q := gooNewProduct(bkType)
	q.Name = "Private lesson"
	q = gxSavePanels(t, srv, q, bkKey, url.Values{"_wc_booking_duration_type": {"fixed"}, "_wc_booking_duration": {"1"}, "_wc_booking_duration_unit": {"day"},
		"_wc_booking_requires_confirmation": {"yes"}, "_wc_booking_cost": {"50"}, "_wc_booking_max_date": {"12"}, "_wc_booking_max_date_unit": {"month"}, "_wc_booking_qty": {"1"}})
	c3 := gooTestCtx()
	cart3 := srv.gooCartFor(c3)
	if _, err := cart3.Add(ctx, q.ID, 1, 0, nil, map[string][]string{"wc_bookings_field_start_date": {monday.Format("2006-01-02")}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, errs := gxCheckout(t, srv, c3, "goo_test"); len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "Check booking availability") {
		t.Errorf("a confirmation booking paid directly: %v", errs)
	}
	o3, errs := gxCheckout(t, srv, c3, bkGatewayID)
	if len(errs) > 0 {
		t.Fatalf("confirmation request: %v", errs)
	}
	b3 := srv.bkQuery(ctx, `order_id = $1`, o3.ID)
	if o3.Status != "pending" || o3.CreatedVia != "bookings" || len(b3) != 1 || b3[0].Status != "pending-confirmation" {
		t.Fatalf("confirmation order %s via %s, bookings %+v", o3.Status, o3.CreatedVia, b3)
	}
	if !srv.gooCartFor(c3).IsEmpty() {
		t.Error("cart not emptied after the request")
	}
	// The admin confirms from the list; the customer is told and pays.
	_, body, _ := get(t, client, base+"/wp-admin/goocommerce?page=bookings&post_status=pending-confirmation")
	if strings.Contains(body, "Template error") || !strings.Contains(body, "Booking #"+itoa64(b3[0].ID)) {
		t.Fatalf("bookings list: %.400s", body)
	}
	m := strings.Index(body, "action=confirm&amp;id="+itoa64(b3[0].ID)+"&amp;_wpnonce=")
	if m < 0 {
		t.Fatal("no Confirm action")
	}
	confirmURL := body[m:]
	confirmURL = strings.ReplaceAll(confirmURL[:strings.Index(confirmURL, `"`)], "&amp;", "&")
	if code, _, loc := get(t, client, base+"/wp-admin/goocommerce?page=bookings&"+confirmURL); code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("confirm: %d %s", code, loc)
	}
	if b, _ := srv.bkGet(ctx, b3[0].ID); b.Status != "confirmed" {
		t.Errorf("after confirm: %s", b.Status)
	}
	if gxMailCount(t, srv, "has been confirmed") == 0 {
		t.Error("no booking confirmed e-mail")
	}
	if err := srv.gooPaymentComplete(ctx, o3, "tx1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := srv.bkGet(ctx, b3[0].ID); b.Status != "paid" {
		t.Errorf("after payment: %s", b.Status)
	}

	// The in-cart hold expires: the hourly job removes it and the cart drops the item.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_bookings SET date_created = now() - interval '2 hours' WHERE status = 'in-cart'`)
	srv.gooFire(ctx, "hourly", nil)
	var inCart int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_bookings WHERE status = 'in-cart'`).Scan(&inCart)
	if inCart != 0 {
		t.Errorf("%d in-cart bookings after the hourly job", inCart)
	}
	cart2.Calculate()
	if !cart2.IsEmpty() || len(cart2.Errors()) == 0 {
		t.Errorf("expired bookings still in the cart: %d items", len(cart2.Items))
	}

	// Daily: the reminder for tomorrow's paid booking, and past bookings completed.
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	tomorrow = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 10, 0, 0, 0, time.UTC)
	rb := &bkBooking{ProductID: p.ID, Status: "paid", Start: tomorrow, End: tomorrow.Add(time.Hour), CustomerID: 1}
	past := &bkBooking{ProductID: p.ID, Status: "paid", Start: tomorrow.AddDate(0, 0, -5), End: tomorrow.AddDate(0, 0, -5).Add(time.Hour)}
	for _, b := range []*bkBooking{rb, past} {
		if err := srv.bkInsert(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	srv.gooFire(ctx, "daily", nil)
	if b, _ := srv.bkGet(ctx, rb.ID); !b.Reminded {
		t.Error("no reminder sent for tomorrow's booking")
	}
	if b, _ := srv.bkGet(ctx, past.ID); b.Status != "complete" {
		t.Errorf("past booking status %s", b.Status)
	}
	if gxMailCount(t, srv, "Booking reminder") == 0 {
		t.Error("no reminder e-mail queued")
	}

	// My Account › Bookings, with the cancel link while allowed.
	fb := &bkBooking{ProductID: p.ID, Status: "paid", Start: monday.Add(11 * time.Hour), End: monday.Add(12 * time.Hour), CustomerID: 1}
	if err := srv.bkInsert(ctx, fb); err != nil {
		t.Fatal(err)
	}
	admin, _ := srv.store.UserByID(ctx, 1)
	ac := gooTestCtx()
	ac.Set(ctxUser, admin)
	if out := string(bkAccountBookings(srv, ac, "")); !strings.Contains(out, "Upcoming Bookings") || !strings.Contains(out, "cancel_booking=true") {
		t.Errorf("account bookings: %s", out)
	}

	// Admin screens.
	for _, u := range []string{"", "&action=calendar&calendar_month=" + monday.Format("2006-01"), "&action=create", "&action=edit&id=" + itoa64(rb.ID), "&filter_bookings=" + itoa64(p.ID) + "&m=" + monday.Format("2006-01")} {
		code, body, _ := get(t, client, base+"/wp-admin/goocommerce?page=bookings"+u)
		if code != http.StatusOK || strings.Contains(body, "Template error") {
			t.Errorf("bookings admin %s: %d", u, code)
		}
	}
	// Create a booking with a new order from the admin.
	_, body, _ = get(t, client, base+"/wp-admin/goocommerce?page=bookings&action=create")
	code, _, loc := post(t, client, base+"/wp-admin/goocommerce?page=bookings&action=create", url.Values{"_wpnonce": {formNonce(t, body)},
		"bookable_product_id": {itoa64(q.ID)}, "customer_id": {"1"}, "booking_order": {"new"}, "wc_bookings_field_start_date": {saturday.Format("2006-01-02")}})
	if code != http.StatusFound || !strings.Contains(loc, "action=edit") {
		t.Fatalf("create booking: %d %s", code, loc)
	}
	created := srv.bkQuery(ctx, `product_id = $1 AND order_id <> 0 AND customer_id = 1`, q.ID)
	if len(created) != 1 || created[0].Status != "unpaid" {
		t.Fatalf("created booking: %+v", created)
	}
	if co, err := srv.gooGetOrder(ctx, created[0].OrderID); err != nil || co.Total != 50 || co.Status != "pending" {
		t.Errorf("created order: %+v %v", co, err)
	}
	// Edit: change the status.
	_, body, _ = get(t, client, base+"/wp-admin/goocommerce?page=bookings&action=edit&id="+itoa64(created[0].ID))
	start := created[0].Start.UTC()
	code, _, _ = post(t, client, base+"/wp-admin/goocommerce?page=bookings&action=edit&id="+itoa64(created[0].ID), url.Values{"_wpnonce": {formNonce(t, body)},
		"_booking_status": {"cancelled"}, "product_id": {itoa64(q.ID)}, "_booking_all_day": {"yes"}, "booking_start_date": {start.Format("2006-01-02")},
		"booking_end_date": {start.Format("2006-01-02")}, "_booking_customer_id": {"1"}, "_booking_order_id": {itoa64(created[0].OrderID)}})
	if b, _ := srv.bkGet(ctx, created[0].ID); code != http.StatusFound || b.Status != "cancelled" || !b.End.Equal(b.Start.AddDate(0, 0, 1)) {
		t.Errorf("edit booking: %d %+v", code, b)
	}
}

func TestGooExtBookingsResources(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, bkKey, true); err != nil {
		t.Fatal(err)
	}

	// Bookings › Resources: add "Guide A" from the admin; "Guide B" is not bookable on Mondays.
	code, body, _ := get(t, client, base+"/wp-admin/goocommerce?page=booking-resources&action=edit")
	if code != http.StatusOK || !strings.Contains(body, "Available Quantity") {
		t.Fatalf("resource edit screen: %d", code)
	}
	code, _, loc := post(t, client, base+"/wp-admin/goocommerce?page=booking-resources&action=edit", url.Values{"_wpnonce": {formNonce(t, body)},
		"post_title": {"Guide A"}, "_wc_booking_qty": {"1"}, "menu_order": {"0"}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("save resource: %d %s", code, loc)
	}
	all := srv.gooBkResources(ctx)
	if len(all) != 1 || all[0].Name != "Guide A" || all[0].Qty != 1 {
		t.Fatalf("resources: %+v", all)
	}
	resA := all[0]
	resB := &gooBkResource{Name: "Guide B", Qty: 1, Order: 1, Rules: []bkRule{{Type: "days", From: "1", To: "1", Bookable: false, Priority: 1}}}
	if err := srv.gooBkSaveResource(ctx, resB); err != nil {
		t.Fatal(err)
	}
	if r, _ := srv.gooBkResource(ctx, resB.ID); r == nil || len(r.Rules) != 1 || r.Rules[0].Bookable {
		t.Fatalf("resource B reloaded: %+v", r)
	}
	if _, body, _ := get(t, client, base+"/wp-admin/goocommerce?page=booking-resources"); !strings.Contains(body, "Guide A") || !strings.Contains(body, "Guide B") {
		t.Errorf("resources list: %.300s", body)
	}

	// A day product with both resources, automatically assigned.
	day := url.Values{"_wc_booking_duration_type": {"fixed"}, "_wc_booking_duration": {"1"}, "_wc_booking_duration_unit": {"day"},
		"_wc_booking_qty": {"5"}, "_wc_booking_max_date": {"12"}, "_wc_booking_max_date_unit": {"month"}, "_wc_booking_cost": {"10"}, "_wc_booking_block_cost": {"20"},
		"_wc_booking_has_resources": {"yes"}, "_wc_booking_resource_label": {"Guide"}, "_wc_booking_resources_assignment": {"automatic"},
		"resource_id[]": {itoa64(resA.ID), itoa64(resB.ID)}, "resource_base_cost[]": {"5", "0"}, "resource_block_cost[]": {"1", "3"}}
	p := gooNewProduct(bkType)
	p.Name = "Hiking day"
	p = gxSavePanels(t, srv, p, bkKey, day)
	conf := bkConfOf(p)
	if !conf.HasResources || conf.ResourceAssign != "automatic" || conf.resourceLabel() != "Guide" || len(conf.Resources) != 2 || conf.Resources[0].BaseCost != 5 {
		t.Fatalf("resource settings: %+v", conf)
	}
	if p.RegularPrice == nil || *p.RegularPrice != 33 { // 10 + 20 + the cheapest resource (B: 3)
		t.Errorf("from price %v, want 33", *p.RegularPrice)
	}
	if out := string(gooBkPanelResources(srv, gooTestCtx(), p)); !strings.Contains(out, "Guide A") || !strings.Contains(out, "Automatically assigned") {
		t.Errorf("resources panel: %s", out)
	}
	if out := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", p)); strings.Contains(out, `name="wc_bookings_field_resource"`) {
		t.Error("automatic resources show a resource select")
	}

	dayForm := func(d time.Time, res int64) map[string][]string {
		f := map[string][]string{"wc_bookings_field_start_date": {d.Format("2006-01-02")}}
		if res != 0 {
			f["wc_bookings_field_resource"] = []string{itoa64(res)}
		}
		return f
	}
	monday := gxNextWeekday(time.Monday)
	tuesday := monday.AddDate(0, 0, 1)
	wednesday := monday.AddDate(0, 0, 2)

	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	key, err := cart.Add(ctx, p.ID, 1, 0, nil, dayForm(monday, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	it := cart.Item(key)
	gooAlmost(t, "resource A price", it.Price, 36) // 10 + 5 + 20 + 1
	joined := ""
	for _, kv := range srv.gooItemData(ctx, it) {
		joined += kv.Key + "=" + kv.Value + ";"
	}
	if !strings.Contains(joined, "Guide=Guide A") {
		t.Errorf("item data: %s", joined)
	}
	// Monday: A is taken and B is not available on Mondays.
	other := srv.gooCartFor(gooTestCtx())
	if _, err := other.Add(ctx, p.ID, 1, 0, nil, dayForm(monday, 0), nil); err == nil {
		t.Error("a second Monday booking found a resource")
	}
	// Tuesday: A, then B, then nothing — the product's own quantity (5) does not apply.
	k1, err := other.Add(ctx, p.ID, 1, 0, nil, dayForm(tuesday, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := other.Add(ctx, p.ID, 1, 0, nil, dayForm(tuesday, 0), nil)
	if err != nil {
		t.Fatalf("second Tuesday booking: %v", err)
	}
	if a, b := bkItemBooking(other.Item(k1)), bkItemBooking(other.Item(k2)); int64(gxNum(a["_resource_id"])) != resA.ID || int64(gxNum(b["_resource_id"])) != resB.ID {
		t.Errorf("assigned resources %v, %v", a["_resource_id"], b["_resource_id"])
	}
	gooAlmost(t, "resource B price", other.Item(k2).Price, 33)
	if _, err := other.Add(ctx, p.ID, 1, 0, nil, dayForm(tuesday, 0), nil); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Errorf("a third Tuesday booking: %v", err)
	}

	// A second product, customer selected, sharing Guide A: its capacity is shared.
	q := gooNewProduct(bkType)
	q.Name = "Climbing day"
	qf := url.Values{}
	for k, v := range day {
		qf[k] = v
	}
	qf["_wc_booking_resources_assignment"] = []string{"customer"}
	qf["resource_id[]"], qf["resource_base_cost[]"], qf["resource_block_cost[]"] = []string{itoa64(resA.ID)}, []string{"0"}, []string{"0"}
	q = gxSavePanels(t, srv, q, bkKey, qf)
	if out := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", q)); !strings.Contains(out, `name="wc_bookings_field_resource"`) || !strings.Contains(out, "Guide A") {
		t.Errorf("customer selected resource form: %s", out)
	}
	qc := srv.gooCartFor(gooTestCtx())
	if _, err := qc.Add(ctx, q.ID, 1, 0, nil, dayForm(tuesday, resA.ID), nil); err == nil {
		t.Error("Guide A booked twice on Tuesday across products")
	}
	if _, err := qc.Add(ctx, q.ID, 1, 0, nil, dayForm(wednesday, resB.ID), nil); err == nil || !strings.Contains(err.Error(), "valid guide") {
		t.Errorf("a resource the product does not have: %v", err)
	}
	if _, err := qc.Add(ctx, q.ID, 1, 0, nil, dayForm(wednesday, resA.ID), nil); err != nil {
		t.Errorf("Guide A on Wednesday: %v", err)
	}

	// The AJAX cost with a chosen resource: B on Thursday = 10 + 20 + 3.
	rc, w := gxGetCtx("/?goo-ajax=booking_slots&product_id=" + itoa64(p.ID) + "&date=" + monday.AddDate(0, 0, 3).Format("2006-01-02") + "&wc_bookings_field_resource=" + itoa64(resB.ID))
	if !bkFrontRoute(srv, rc, "") {
		t.Fatal("the slots route did not answer")
	}
	var res struct {
		Cost     float64 `json:"cost"`
		Resource string  `json:"resource"`
		Error    string  `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Cost != 33 || res.Resource != "Guide B" || res.Error != "" {
		t.Errorf("slots route with a resource: %s", w.Body.String())
	}

	// Timed slots: automatic assignment sums the places of the resources.
	h := gooNewProduct(bkType)
	h.Name = "Lesson"
	h = gxSavePanels(t, srv, h, bkKey, url.Values{"_wc_booking_duration_type": {"fixed"}, "_wc_booking_duration": {"1"}, "_wc_booking_duration_unit": {"hour"},
		"_wc_booking_qty": {"1"}, "_wc_booking_max_date": {"12"}, "_wc_booking_max_date_unit": {"month"}, "_wc_booking_first_block_time": {"09:00"},
		"_wc_booking_has_resources": {"yes"}, "_wc_booking_resources_assignment": {"automatic"}, "resource_id[]": {itoa64(resA.ID), itoa64(resB.ID)}})
	friday := monday.AddDate(0, 0, 4)
	if sl := srv.gooBkSlotsAll(ctx, h, bkConfOf(h), friday, 0); len(sl) == 0 || sl[0].Time != "09:00" || sl[0].Available != 2 {
		t.Errorf("merged slots: %+v", sl)
	}
	if sl := srv.gooBkSlotsAll(ctx, h, bkConfOf(h), friday, resA.ID); len(sl) == 0 || sl[0].Available != 1 {
		t.Errorf("slots of A: %+v", sl)
	}
	if sl := srv.gooBkSlotsAll(ctx, h, bkConfOf(h), monday, 0); len(sl) != 0 {
		t.Errorf("Monday slots with A held all day and B off: %+v", sl)
	}

	// Checkout: the booking keeps its resource; admin, e-mails and My Account show it.
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	bks := srv.bkQuery(ctx, `order_id = $1`, o.ID)
	if len(bks) != 1 || bks[0].ResourceID != resA.ID || bks[0].Cost != 36 {
		t.Fatalf("order bookings: %+v", bks)
	}
	bk := bks[0]
	bk.CustomerID = 1
	_ = srv.bkUpdate(ctx, bk)
	var mails int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = 'wp_mail' AND payload::text LIKE '%Guide A%'`).Scan(&mails)
	if mails == 0 {
		t.Error("the booking e-mail does not name the resource")
	}
	if _, body, _ := get(t, client, base+"/wp-admin/goocommerce?page=bookings"); !strings.Contains(body, `<td class="booking_resource">Guide A</td>`) {
		t.Error("bookings list lacks the resource")
	}
	_, body, _ = get(t, client, base+"/wp-admin/goocommerce?page=bookings&action=calendar&calendar_month="+monday.Format("2006-01"))
	if !strings.Contains(body, "Hiking day (Guide A)") {
		t.Error("calendar lacks the resource")
	}
	_, body, _ = get(t, client, base+"/wp-admin/goocommerce?page=bookings&action=edit&id="+itoa64(bk.ID))
	if !strings.Contains(body, `name="_booking_resource_id"`) {
		t.Fatal("booking edit lacks the resource select")
	}
	start := bk.Start.UTC()
	code, _, _ = post(t, client, base+"/wp-admin/goocommerce?page=bookings&action=edit&id="+itoa64(bk.ID), url.Values{"_wpnonce": {formNonce(t, body)},
		"_booking_status": {bk.Status}, "product_id": {itoa64(p.ID)}, "_booking_all_day": {"yes"}, "booking_start_date": {start.Format("2006-01-02")},
		"booking_end_date": {start.Format("2006-01-02")}, "_booking_customer_id": {"1"}, "_booking_order_id": {itoa64(o.ID)}, "_booking_resource_id": {itoa64(resB.ID)}})
	if b, _ := srv.bkGet(ctx, bk.ID); code != http.StatusFound || b.ResourceID != resB.ID {
		t.Errorf("edit resource: %d %+v", code, b)
	}
	admin, _ := srv.store.UserByID(ctx, 1)
	ac := gooTestCtx()
	ac.Set(ctxUser, admin)
	if out := string(bkAccountBookings(srv, ac, "")); !strings.Contains(out, "Guide: Guide B") {
		t.Errorf("account bookings: %s", out)
	}

	// Admin Create Booking assigns a resource automatically.
	_, body, _ = get(t, client, base+"/wp-admin/goocommerce?page=bookings&action=create")
	code, _, loc = post(t, client, base+"/wp-admin/goocommerce?page=bookings&action=create", url.Values{"_wpnonce": {formNonce(t, body)},
		"bookable_product_id": {itoa64(p.ID)}, "customer_id": {"1"}, "booking_order": {""}, "wc_bookings_field_start_date": {monday.AddDate(0, 0, 5).Format("2006-01-02")},
		"wc_bookings_field_resource": {"0"}})
	if code != http.StatusFound || !strings.Contains(loc, "action=edit") {
		t.Fatalf("create booking: %d %s", code, loc)
	}
	if cr := srv.bkQuery(ctx, `product_id = $1 AND order_id = 0 AND status = 'unpaid'`, p.ID); len(cr) != 1 || cr[0].ResourceID != resA.ID {
		t.Errorf("admin-created booking: %+v", cr)
	}

	// Deleting a resource unlinks it from the products.
	_, body, _ = get(t, client, base+"/wp-admin/goocommerce?page=booking-resources")
	i := strings.Index(body, "action=delete&amp;id="+itoa64(resB.ID)+"&amp;_wpnonce=")
	if i < 0 {
		t.Fatal("no Delete action")
	}
	del := body[i:]
	del = strings.ReplaceAll(del[:strings.Index(del, `"`)], "&amp;", "&")
	if code, _, _ := get(t, client, base+"/wp-admin/goocommerce?page=booking-resources&"+del); code != http.StatusFound {
		t.Errorf("delete resource: %d", code)
	}
	if list := srv.gooBkProductResources(ctx, bkConfOf(p)); len(list) != 1 || list[0].ID != resA.ID {
		t.Errorf("product resources after delete: %+v", list)
	}
}
