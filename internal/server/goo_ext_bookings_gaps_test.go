package server

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// gxGapProduct is a saved bookable product: 1-hour blocks, one place per block, always available.
func gxGapProduct(t *testing.T, srv *Server, overnight bool) *gooProduct {
	t.Helper()
	if err := srv.gooSetExtension(context.Background(), bkKey, true); err != nil {
		t.Fatal(err)
	}
	p := gooNewProduct(bkType)
	p.Name = "Night room"
	p.Meta = map[string]any{}
	for k, v := range map[string]any{"duration_type": "fixed", "duration": 1.0, "duration_unit": "hour", "qty": 1.0, "min_date": 0.0,
		"min_date_unit": "day", "max_date": 12.0, "max_date_unit": "month", "first_block_time": "00:00", "cost": 10.0, "allow_overnight": overnight} {
		bkSetMeta(p, k, v)
	}
	p.Virtual, p.SoldIndividually = true, true
	if err := srv.gooSaveProduct(context.Background(), p, 1); err != nil {
		t.Fatal(err)
	}
	p, err := srv.gooGetProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBookingsLastSlotRace(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gxGapProduct(t, srv, false)
	day := gxNextWeekday(1)
	form := map[string][]string{"wc_bookings_field_start_date": {day.Format("2006-01-02")}, "wc_bookings_field_start_date_time": {"10:00"}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if bkAddToCart(srv, ctx, p, 1, form, map[string]any{}) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d concurrent adds of the last place succeeded, want 1", ok)
	}
}

func TestBookingsAvailabilityErrorRefuses(t *testing.T) {
	_, _, srv := gooSite(t)
	p := gxGapProduct(t, srv, false)
	day := gxNextWeekday(1)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := srv.gooBkExistingErr(cctx, p.ID, 0, day, day.AddDate(0, 0, 1), 0); err == nil {
		t.Fatal("a failed availability query returned no error")
	}
	start := day.Add(10 * 3600e9)
	if err := srv.gxbCheck(cctx, p, bkConfOf(p), nil, start, 1, 0, 0); err == nil {
		t.Error("a booking was accepted although availability could not be read")
	}
}

func TestBookingsOvernightBlocks(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	day := gxNextWeekday(1)
	start := day.Add(23*3600e9 + 1800e9)
	off := gxGapProduct(t, srv, false)
	if err := srv.gxbCheck(ctx, off, bkConfOf(off), nil, start, 1, 0, 0); err == nil {
		t.Error("a block past midnight was accepted without the setting")
	}
	on := gxGapProduct(t, srv, true)
	if err := srv.gxbCheck(ctx, on, bkConfOf(on), nil, start, 1, 0, 0); err != nil {
		t.Errorf("a block past midnight with the setting: %v", err)
	}
}

func TestBookingsICSAndPaidCancel(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gxGapProduct(t, srv, false)
	day := gxNextWeekday(1)
	b := &bkBooking{ProductID: p.ID, Status: "paid", Start: day.Add(10 * 3600e9), End: day.Add(11 * 3600e9)}
	if err := srv.bkInsert(ctx, b); err != nil {
		t.Fatal(err)
	}
	c, w := gxGetCtx("/?goo-ajax=booking_ics&booking_id=" + itoa64(b.ID) + "&token=bad")
	srv.bkICSRoute(c)
	if w.Code != 404 {
		t.Errorf("a bad token answered %d", w.Code)
	}
	c, w = gxGetCtx("/?goo-ajax=booking_ics&booking_id=" + itoa64(b.ID) + "&token=" + srv.bkICSToken("booking", b.ID))
	srv.bkICSRoute(c)
	body := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/calendar") || !strings.Contains(body, "BEGIN:VEVENT") ||
		!strings.Contains(body, "SUMMARY:Night room") || !strings.Contains(body, "DTSTART:") || !strings.Contains(body, "\r\nEND:VCALENDAR\r\n") {
		t.Errorf("ics: %d %q", w.Code, body)
	}
	c, w = gxGetCtx("/?goo-ajax=booking_feed&token=" + srv.bkICSToken("feed", 0))
	srv.bkICSRoute(c)
	if !strings.Contains(w.Body.String(), "UID:booking-"+itoa64(b.ID)+"@") {
		t.Errorf("feed: %q", w.Body.String())
	}

	// The admin cancellation e-mail carries the refund line.
	srv.bkMailWith(ctx, "admin_booking_cancelled", b, "A refund is required: test")
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = 'wp_mail' AND payload::text LIKE '%refund is required%'`).Scan(&n)
	if n == 0 {
		t.Error("the refund line is not in the e-mail")
	}
}
