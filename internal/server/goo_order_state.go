package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"time"
)

// Order state under concurrency. Several writers can hold the same order at once: the payment
// callback, the hourly job that cancels unpaid orders, the admin edit screen, the wc/v3 API. Two
// rules keep them from losing each other's work:
//
//   - A status change is serialized per order (gooWithOrderLock in this process, a row lock with a
//     re-check of the current status in the database) and starts from the status the database has,
//     not from the copy the caller loaded earlier (gooRefreshOrderState).
//   - gooSaveOrder writes the contested columns (status, stock_reduced, downloads_granted, the
//     bookkeeping meta keys, transaction_id, date_paid, date_completed, the line items'
//     _reduced_stock) only when the caller changed them since
//     the order was loaded, like WC_Data::save() writes get_changes(). A stale copy saved later
//     therefore cannot undo a status change or the stock / coupon bookkeeping made meanwhile.
//
// The bookkeeping itself is claimed with conditional UPDATEs (gooClaimOrderFlag), so it happens once
// per order even if two instances of the app race.

// errGooStatusChanged is returned when the order's status is no longer one the change starts from.
var errGooStatusChanged = errors.New("The order status changed while it was being updated. Please review the order and try again.")

// gooOrderBookkeepingKeys are the order meta keys the status bookkeeping owns.
var gooOrderBookkeepingKeys = []string{"_recorded_coupon_usage_counts", "_recorded_sales"}

// gooOrderSnap is the contested part of an order as last read from or written to the database.
type gooOrderSnap struct {
	status           string
	stockReduced     bool
	downloadsGranted bool
	transactionID    string
	datePaid         *time.Time
	dateCompleted    *time.Time
	meta             map[string]any // bookkeeping keys only
}

// snapshot records the contested fields as stored.
func (o *gooOrder) snapshot() {
	sn := &gooOrderSnap{status: o.Status, stockReduced: o.StockReduced, downloadsGranted: o.DownloadsGranted, transactionID: o.TransactionID,
		datePaid: o.DatePaid, dateCompleted: o.DateCompleted, meta: map[string]any{}}
	for _, k := range gooOrderBookkeepingKeys {
		if v, ok := o.Meta[k]; ok {
			sn.meta[k] = v
		}
	}
	o.orig = sn
}

// gooSameTime reports whether two optional timestamps are equal.
func gooSameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// keptBookkeepingKeys are the bookkeeping keys the caller did not change: a save keeps the stored values.
func (o *gooOrder) keptBookkeepingKeys() []string {
	keep := []string{}
	if o.orig == nil {
		return keep
	}
	for _, k := range gooOrderBookkeepingKeys {
		cur, ok1 := o.Meta[k]
		was, ok2 := o.orig.meta[k]
		if ok1 == ok2 && reflect.DeepEqual(cur, was) {
			keep = append(keep, k)
		}
	}
	return keep
}

// itemSnapshot records an item's _reduced_stock as stored.
func (it *gooOrderItem) snapshot() {
	it.origReduced, it.hasOrig = it.Meta["_reduced_stock"], true
}

// keepsReduced reports whether a save keeps the stored _reduced_stock (the caller did not change it).
func (it *gooOrderItem) keepsReduced() bool {
	return it.hasOrig && reflect.DeepEqual(it.Meta["_reduced_stock"], it.origReduced)
}

// gooRefreshOrderState reloads the contested fields of o from the database (status, the bookkeeping
// flags and meta, the items' _reduced_stock, the paid / completed dates). Changes the caller made to
// the other fields (a transaction id, a paid date about to be saved) are kept as changes.
func (s *Server) gooRefreshOrderState(ctx context.Context, o *gooOrder) error {
	var meta []byte
	var paid, completed *time.Time
	if err := s.store.Pool.QueryRow(ctx, `SELECT status, stock_reduced, downloads_granted, meta, date_paid, date_completed FROM goo_orders WHERE id = $1`,
		o.ID).Scan(&o.Status, &o.StockReduced, &o.DownloadsGranted, &meta, &paid, &completed); err != nil {
		return err
	}
	o.gooMergeBookkeeping(meta)
	if o.orig == nil {
		o.orig = &gooOrderSnap{transactionID: "\x00"} // the transaction id was never loaded: a save writes it
	}
	sn := o.orig
	sn.status, sn.stockReduced, sn.downloadsGranted, sn.datePaid, sn.dateCompleted = o.Status, o.StockReduced, o.DownloadsGranted, paid, completed
	sn.meta = map[string]any{}
	for _, k := range gooOrderBookkeepingKeys {
		if v, ok := o.Meta[k]; ok {
			sn.meta[k] = v
		}
	}
	if paid != nil {
		o.DatePaid = paid
	}
	if completed != nil {
		o.DateCompleted = completed
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, meta->'_reduced_stock' FROM goo_order_items WHERE order_id = $1`, o.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	stored := map[int64][]byte{}
	for rows.Next() {
		var id int64
		var red []byte
		if err := rows.Scan(&id, &red); err != nil {
			return err
		}
		stored[id] = red
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range o.Items {
		if red, ok := stored[it.ID]; ok {
			it.setReduced(red)
			it.snapshot()
		}
	}
	return nil
}

// gooMergeBookkeeping copies the bookkeeping keys of a stored meta document into o.Meta.
func (o *gooOrder) gooMergeBookkeeping(raw []byte) {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if o.Meta == nil {
		o.Meta = map[string]any{}
	}
	for _, k := range gooOrderBookkeepingKeys {
		if v, ok := m[k]; ok {
			o.Meta[k] = v
		} else {
			delete(o.Meta, k)
		}
	}
}

// setReduced sets the item's _reduced_stock from a stored JSON value (null or absent removes it).
func (it *gooOrderItem) setReduced(raw []byte) {
	if it.Meta == nil {
		it.Meta = map[string]any{}
	}
	var v any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &v)
	}
	if v == nil {
		delete(it.Meta, "_reduced_stock")
		return
	}
	it.Meta["_reduced_stock"] = v
}

// gooClaimOrderFlag sets a boolean bookkeeping flag of the order from !to to to in one conditional
// UPDATE and reports whether this call made the change: of two racing callers exactly one gets true.
// flag is a column (stock_reduced, downloads_granted) or a meta key.
func (s *Server) gooClaimOrderFlag(ctx context.Context, o *gooOrder, flag string, to bool) (bool, error) {
	q, args := `UPDATE goo_orders SET meta = jsonb_set(meta, ARRAY[$3::text], to_jsonb($2::boolean))
		WHERE id = $1 AND coalesce((meta->>$3::text)::boolean, false) <> $2`, []any{o.ID, to, flag}
	if flag == "stock_reduced" || flag == "downloads_granted" {
		q, args = `UPDATE goo_orders SET `+flag+` = $2 WHERE id = $1 AND `+flag+` <> $2`, args[:2]
	}
	tag, err := s.store.Pool.Exec(ctx, q, args...)
	if err != nil {
		return false, err
	}
	// The in-memory copy follows the database either way, and a later save keeps it as stored.
	switch flag {
	case "stock_reduced":
		o.StockReduced = to
		if o.orig != nil {
			o.orig.stockReduced = to
		}
	case "downloads_granted":
		o.DownloadsGranted = to
		if o.orig != nil {
			o.orig.downloadsGranted = to
		}
	default:
		if o.Meta == nil {
			o.Meta = map[string]any{}
		}
		o.Meta[flag] = to
		if o.orig != nil {
			o.orig.meta[flag] = to
		}
	}
	return tag.RowsAffected() == 1, nil
}

// gooSetItemReduced stores an item's _reduced_stock (qty <= 0 removes it) without touching its other meta.
func (s *Server) gooSetItemReduced(ctx context.Context, it *gooOrderItem, qty float64) error {
	var err error
	if qty > 0 {
		_, err = s.store.Pool.Exec(ctx, `UPDATE goo_order_items SET meta = meta || jsonb_build_object('_reduced_stock', $2::float8) WHERE id = $1`, it.ID, qty)
		it.Meta["_reduced_stock"] = qty
	} else {
		_, err = s.store.Pool.Exec(ctx, `UPDATE goo_order_items SET meta = meta - '_reduced_stock' WHERE id = $1`, it.ID)
		delete(it.Meta, "_reduced_stock")
	}
	it.snapshot()
	return err
}

// Per-order lock --------------------------------------------------------------------------------------

type gooOrderMutex struct {
	sync.Mutex
	refs int
}

var (
	gooOrderLocksMu sync.Mutex
	gooOrderLocks   = map[int64]*gooOrderMutex{}
)

type gooHeldOrdersKey struct{}

// gooHeldOrder is the list of orders whose lock the context's goroutine holds.
type gooHeldOrder struct {
	id   int64
	next *gooHeldOrder
}

// gooWithOrderLock runs fn while holding the order's lock. The lock is re-entrant through ctx: a hook
// that runs inside a status change (and gets its ctx) may change the same order's status again.
func gooWithOrderLock(ctx context.Context, id int64, fn func(ctx context.Context) error) error {
	if id == 0 {
		return fn(ctx)
	}
	held, _ := ctx.Value(gooHeldOrdersKey{}).(*gooHeldOrder)
	for h := held; h != nil; h = h.next {
		if h.id == id {
			return fn(ctx)
		}
	}
	gooOrderLocksMu.Lock()
	m := gooOrderLocks[id]
	if m == nil {
		m = &gooOrderMutex{}
		gooOrderLocks[id] = m
	}
	m.refs++
	gooOrderLocksMu.Unlock()
	m.Lock()
	defer func() {
		m.Unlock()
		gooOrderLocksMu.Lock()
		if m.refs--; m.refs == 0 {
			delete(gooOrderLocks, id)
		}
		gooOrderLocksMu.Unlock()
	}()
	return fn(context.WithValue(ctx, gooHeldOrdersKey{}, &gooHeldOrder{id: id, next: held}))
}
