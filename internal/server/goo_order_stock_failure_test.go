package server

import (
	"context"
	"strings"
	"testing"
)

// A line whose stock update fails for a reason other than a shortage (a database error) is not
// reduced and not marked, but the shop is told in an order note (wc_reduce_stock_levels) instead of
// the inventory going silently wrong.
func TestGooReduceStockFailureNoted(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Broken shelf", 10, func(p *gooProduct) {
		p.ManageStock, p.StockQuantity, p.SKU = true, gooIntPtr(20), "SHELF-1"
	})
	o := gooRaceOrder(t, srv, "s@example.test", 0, map[*gooProduct]int{p: 2})
	before, _ := gooStockOf(t, srv, p.ID)

	// The trigger fails updates of this product only: other tests may share the database.
	fn, trg := "goo_test_fail_stock_"+itoa64(p.ID), "goo_test_fail_stock_trg_"+itoa64(p.ID)
	if _, err := srv.store.Pool.Exec(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.post_id = `+itoa64(p.ID)+` THEN RAISE EXCEPTION 'test'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = srv.store.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+fn+`() CASCADE`) })
	if _, err := srv.store.Pool.Exec(ctx, `CREATE TRIGGER `+trg+` BEFORE UPDATE ON goo_products FOR EACH ROW EXECUTE FUNCTION `+fn+`()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = srv.store.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+trg+` ON goo_products`)
	})

	if err := srv.gooReduceStock(ctx, o, false); err != nil {
		t.Fatalf("reduce stock: %v", err)
	}
	notes, _ := srv.gooOrderNotes(ctx, o.ID, false)
	found := false
	for _, n := range notes {
		if strings.Contains(n.Content, "Unable to reduce stock for item Broken shelf (SHELF-1)") {
			found = true
		}
	}
	if !found {
		t.Errorf("no note for the failed line: %+v", notes)
	}
	got, _ := srv.gooGetOrder(ctx, o.ID)
	if !got.StockReduced {
		t.Error("the order is not marked stock reduced")
	}
	for _, it := range got.ItemsOf("line_item") {
		if _, ok := it.Meta["_reduced_stock"]; ok {
			t.Errorf("failed line marked reduced: %v", it.Meta)
		}
	}
	if after, _ := gooStockOf(t, srv, p.ID); after != before {
		t.Errorf("stock %d, want %d", after, before)
	}
	// Restoring does not put back what was never taken.
	srv.gooRestoreStock(ctx, got)
	if after, _ := gooStockOf(t, srv, p.ID); after != before {
		t.Errorf("stock after restore %d, want %d", after, before)
	}
}
