package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/store"
)

// Orders (WC_Order, High-Performance Order Storage): the order row, its items (line items, shipping,
// fees, coupons, tax lines), notes, status transitions with stock and coupon bookkeeping, refunds.

var gooOrderStatuses = []gooOpt{
	{"pending", "Pending payment"}, {"processing", "Processing"}, {"on-hold", "On hold"}, {"completed", "Completed"},
	{"cancelled", "Cancelled"}, {"refunded", "Refunded"}, {"failed", "Failed"}, {"checkout-draft", "Draft"},
}

// gooStatusLabel is wc_get_order_status_name().
func gooStatusLabel(st string) string {
	st = strings.TrimPrefix(st, "wc-")
	for _, o := range gooOrderStatuses {
		if o.Value == st {
			return o.Label
		}
	}
	return st
}

// gooPaidStatuses are wc_get_is_paid_statuses().
var gooPaidStatuses = []string{"processing", "completed"}

type gooOrderItem struct {
	ID          int64              `json:"id"`
	OrderID     int64              `json:"order_id"`
	Type        string             `json:"type"`
	Name        string             `json:"name"`
	ProductID   int64              `json:"product_id"`
	VariationID int64              `json:"variation_id"`
	Quantity    float64            `json:"quantity"`
	TaxClass    string             `json:"tax_class"`
	Subtotal    float64            `json:"subtotal"`
	SubtotalTax float64            `json:"subtotal_tax"`
	Total       float64            `json:"total"`
	TotalTax    float64            `json:"total_tax"`
	Taxes       gooItemTaxes       `json:"taxes"`
	Meta        map[string]any     `json:"meta_data"`
	Position    int                `json:"-"`
	refunded    map[string]float64 // computed: refunded qty / total / tax
}

// gooItemTaxes are an item's taxes per rate id.
type gooItemTaxes struct {
	Total    map[string]float64 `json:"total"`
	Subtotal map[string]float64 `json:"subtotal"`
}

// MetaString reads a string meta value.
func (it *gooOrderItem) MetaString(k string) string {
	switch v := it.Meta[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Qty is the quantity as an int.
func (it *gooOrderItem) Qty() int { return int(it.Quantity) }

type gooOrderNote struct {
	ID         int64
	OrderID    int64
	Content    string
	IsCustomer bool
	AddedBy    string
	CreatedAt  time.Time
}

type gooOrder struct {
	ID                 int64
	Type               string // shop_order, shop_order_refund
	ParentID           int64
	Status             string
	Currency           string
	PricesIncludeTax   bool
	CustomerID         int64
	Billing            map[string]string
	Shipping           map[string]string
	PaymentMethod      string
	PaymentMethodTitle string
	TransactionID      string
	CustomerIP         string
	UserAgent          string
	CustomerNote       string
	CreatedVia         string
	CartHash           string
	OrderKey           string
	DiscountTotal      float64
	DiscountTax        float64
	ShippingTotal      float64
	ShippingTax        float64
	CartTax            float64
	Total              float64
	TotalTax           float64
	RefundReason       string
	RefundedBy         int64
	StockReduced       bool
	DownloadsGranted   bool
	NewOrderEmailSent  bool
	Meta               map[string]any
	DateCreated        time.Time
	DateModified       time.Time
	DatePaid           *time.Time
	DateCompleted      *time.Time

	Items []*gooOrderItem
}

const gooOrderCols = `id, type, parent_id, status, currency, prices_include_tax, customer_id, billing, shipping, payment_method,
	payment_method_title, transaction_id, customer_ip, user_agent, customer_note, created_via, cart_hash, order_key,
	discount_total::float8, discount_tax::float8, shipping_total::float8, shipping_tax::float8, cart_tax::float8, total::float8,
	total_tax::float8, refund_reason, refunded_by, stock_reduced, downloads_granted, new_order_email_sent, meta, date_created,
	date_modified, date_paid, date_completed`

func gooScanOrder(row pgx.Row) (*gooOrder, error) {
	o := &gooOrder{}
	var billing, shipping, meta []byte
	err := row.Scan(&o.ID, &o.Type, &o.ParentID, &o.Status, &o.Currency, &o.PricesIncludeTax, &o.CustomerID, &billing, &shipping, &o.PaymentMethod,
		&o.PaymentMethodTitle, &o.TransactionID, &o.CustomerIP, &o.UserAgent, &o.CustomerNote, &o.CreatedVia, &o.CartHash, &o.OrderKey,
		&o.DiscountTotal, &o.DiscountTax, &o.ShippingTotal, &o.ShippingTax, &o.CartTax, &o.Total,
		&o.TotalTax, &o.RefundReason, &o.RefundedBy, &o.StockReduced, &o.DownloadsGranted, &o.NewOrderEmailSent, &meta, &o.DateCreated,
		&o.DateModified, &o.DatePaid, &o.DateCompleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(billing, &o.Billing)
	_ = json.Unmarshal(shipping, &o.Shipping)
	_ = json.Unmarshal(meta, &o.Meta)
	if o.Billing == nil {
		o.Billing = map[string]string{}
	}
	if o.Shipping == nil {
		o.Shipping = map[string]string{}
	}
	if o.Meta == nil {
		o.Meta = map[string]any{}
	}
	return o, nil
}

// gooGetOrder is wc_get_order() with its items.
func (s *Server) gooGetOrder(ctx context.Context, id int64) (*gooOrder, error) {
	o, err := gooScanOrder(s.store.Pool.QueryRow(ctx, `SELECT `+gooOrderCols+` FROM goo_orders WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	o.Items, err = s.gooOrderItems(ctx, o.ID)
	return o, err
}

// gooOrderByKey finds an order by its key (order received / pay links of guests).
func (s *Server) gooOrderByKey(ctx context.Context, key string) (*gooOrder, error) {
	var id int64
	if err := s.store.Pool.QueryRow(ctx, `SELECT id FROM goo_orders WHERE order_key = $1 AND type = 'shop_order'`, key).Scan(&id); err != nil {
		return nil, store.ErrNotFound
	}
	return s.gooGetOrder(ctx, id)
}

func (s *Server) gooOrderItems(ctx context.Context, orderID int64) ([]*gooOrderItem, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, order_id, type, name, product_id, variation_id, quantity::float8, tax_class, subtotal::float8,
		subtotal_tax::float8, total::float8, total_tax::float8, taxes, meta, position FROM goo_order_items WHERE order_id = $1 ORDER BY position, id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*gooOrderItem
	for rows.Next() {
		it := &gooOrderItem{}
		var taxes, meta []byte
		if err := rows.Scan(&it.ID, &it.OrderID, &it.Type, &it.Name, &it.ProductID, &it.VariationID, &it.Quantity, &it.TaxClass, &it.Subtotal,
			&it.SubtotalTax, &it.Total, &it.TotalTax, &taxes, &meta, &it.Position); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(taxes, &it.Taxes)
		_ = json.Unmarshal(meta, &it.Meta)
		if it.Meta == nil {
			it.Meta = map[string]any{}
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemsOf filters the items by type (line_item, shipping, fee, coupon, tax).
func (o *gooOrder) ItemsOf(typ string) []*gooOrderItem {
	var out []*gooOrderItem
	for _, it := range o.Items {
		if it.Type == typ {
			out = append(out, it)
		}
	}
	return out
}

// Number is get_order_number().
func (o *gooOrder) Number() string { return itoa64(o.ID) }

// IsPaid is is_paid().
func (o *gooOrder) IsPaid() bool { return containsStr(gooPaidStatuses, o.Status) }

// BillingName is the formatted billing full name.
func (o *gooOrder) BillingName() string {
	return strings.TrimSpace(o.Billing["first_name"] + " " + o.Billing["last_name"])
}

// MetaString reads a string meta value.
func (o *gooOrder) MetaString(k string) string {
	if v, ok := o.Meta[k].(string); ok {
		return v
	}
	return ""
}

// Subtotal is get_subtotal(): line item subtotals (ex tax).
func (o *gooOrder) Subtotal() float64 {
	var t float64
	for _, it := range o.ItemsOf("line_item") {
		t += it.Subtotal
	}
	return t
}

// ItemCount is get_item_count().
func (o *gooOrder) ItemCount() int {
	n := 0
	for _, it := range o.ItemsOf("line_item") {
		n += it.Qty()
	}
	return n
}

// gooSaveOrder writes the order row (insert when ID is 0) and, with items, replaces its items.
func (s *Server) gooSaveOrder(ctx context.Context, o *gooOrder, withItems bool) error {
	if o.Type == "" {
		o.Type = "shop_order"
	}
	if o.Currency == "" {
		o.Currency = s.gooCurrency()
	}
	if o.Status == "" {
		o.Status = "pending"
	}
	if o.OrderKey == "" && o.Type == "shop_order" {
		o.OrderKey = "wc_order_" + gooRandomKey(13)
	}
	if o.Meta == nil {
		o.Meta = map[string]any{}
	}
	billing, _ := json.Marshal(o.Billing)
	shipping, _ := json.Marshal(o.Shipping)
	meta, _ := json.Marshal(o.Meta)
	email := strings.ToLower(strings.TrimSpace(o.Billing["email"]))
	return pgx.BeginFunc(ctx, s.store.Pool, func(tx pgx.Tx) error {
		var err error
		if o.ID == 0 {
			err = tx.QueryRow(ctx, `INSERT INTO goo_orders (type, parent_id, status, currency, prices_include_tax, customer_id, billing, shipping,
				billing_email, payment_method, payment_method_title, transaction_id, customer_ip, user_agent, customer_note, created_via, cart_hash,
				order_key, discount_total, discount_tax, shipping_total, shipping_tax, cart_tax, total, total_tax, refund_reason, refunded_by,
				stock_reduced, downloads_granted, new_order_email_sent, meta, date_paid, date_completed)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33)
				RETURNING id, date_created, date_modified`,
				o.Type, o.ParentID, o.Status, o.Currency, o.PricesIncludeTax, o.CustomerID, billing, shipping, email, o.PaymentMethod,
				o.PaymentMethodTitle, o.TransactionID, o.CustomerIP, o.UserAgent, o.CustomerNote, o.CreatedVia, o.CartHash, o.OrderKey,
				o.DiscountTotal, o.DiscountTax, o.ShippingTotal, o.ShippingTax, o.CartTax, o.Total, o.TotalTax, o.RefundReason, o.RefundedBy,
				o.StockReduced, o.DownloadsGranted, o.NewOrderEmailSent, meta, o.DatePaid, o.DateCompleted).Scan(&o.ID, &o.DateCreated, &o.DateModified)
		} else {
			_, err = tx.Exec(ctx, `UPDATE goo_orders SET status=$2, currency=$3, prices_include_tax=$4, customer_id=$5, billing=$6, shipping=$7,
				billing_email=$8, payment_method=$9, payment_method_title=$10, transaction_id=$11, customer_ip=$12, user_agent=$13, customer_note=$14,
				created_via=$15, cart_hash=$16, discount_total=$17, discount_tax=$18, shipping_total=$19, shipping_tax=$20, cart_tax=$21, total=$22,
				total_tax=$23, refund_reason=$24, refunded_by=$25, stock_reduced=$26, downloads_granted=$27, new_order_email_sent=$28, meta=$29,
				date_paid=$30, date_completed=$31, date_created=$32, date_modified=now() WHERE id=$1`,
				o.ID, o.Status, o.Currency, o.PricesIncludeTax, o.CustomerID, billing, shipping, email, o.PaymentMethod, o.PaymentMethodTitle,
				o.TransactionID, o.CustomerIP, o.UserAgent, o.CustomerNote, o.CreatedVia, o.CartHash, o.DiscountTotal, o.DiscountTax,
				o.ShippingTotal, o.ShippingTax, o.CartTax, o.Total, o.TotalTax, o.RefundReason, o.RefundedBy, o.StockReduced, o.DownloadsGranted,
				o.NewOrderEmailSent, meta, o.DatePaid, o.DateCompleted, o.DateCreated)
		}
		if err != nil || !withItems {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM goo_order_items WHERE order_id = $1`, o.ID); err != nil {
			return err
		}
		for i, it := range o.Items {
			it.OrderID, it.Position = o.ID, i
			if it.Meta == nil {
				it.Meta = map[string]any{}
			}
			taxes, _ := json.Marshal(it.Taxes)
			im, _ := json.Marshal(it.Meta)
			if err := tx.QueryRow(ctx, `INSERT INTO goo_order_items (order_id, type, name, product_id, variation_id, quantity, tax_class, subtotal,
				subtotal_tax, total, total_tax, taxes, meta, position) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
				o.ID, it.Type, it.Name, it.ProductID, it.VariationID, it.Quantity, it.TaxClass, it.Subtotal, it.SubtotalTax, it.Total, it.TotalTax,
				taxes, im, it.Position).Scan(&it.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// gooRecalculate is WC_Order::calculate_totals() for an order edited in the admin: line taxes are kept
// as entered, totals are summed from the items.
func (s *Server) gooRecalculate(o *gooOrder) {
	dp := s.gooDecimals()
	var cartTax, shipTax, shipTotal, fees, items, subtotal, subtotalTax float64
	for _, it := range o.Items {
		switch it.Type {
		case "line_item":
			items += it.Total
			cartTax += it.TotalTax
			subtotal += it.Subtotal
			subtotalTax += it.SubtotalTax
		case "fee":
			fees += it.Total
			cartTax += it.TotalTax
		case "shipping":
			shipTotal += it.Total
			shipTax += it.TotalTax
		}
	}
	o.DiscountTotal = gooRound(subtotal-items, dp)
	o.DiscountTax = gooRound(subtotalTax-cartTax, dp)
	if o.DiscountTax < 0 {
		o.DiscountTax = 0
	}
	o.CartTax, o.ShippingTax, o.ShippingTotal = gooRound(cartTax, dp), gooRound(shipTax, dp), gooRound(shipTotal, dp)
	o.TotalTax = gooRound(cartTax+shipTax, dp)
	o.Total = gooRound(items+fees+shipTotal+o.TotalTax, dp)
}

// Notes ---------------------------------------------------------------------------------------------

// gooAddOrderNote is WC_Order::add_order_note(); a customer note is e-mailed to the customer.
func (s *Server) gooAddOrderNote(ctx context.Context, o *gooOrder, note string, customer bool, by string) error {
	if strings.TrimSpace(note) == "" {
		return nil
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO goo_order_notes (order_id, content, is_customer_note, added_by) VALUES ($1,$2,$3,$4)`,
		o.ID, note, customer, by); err != nil {
		return err
	}
	if customer {
		s.gooFire(ctx, "customer_note", &gooNoteEvent{Order: o, Note: note})
	}
	return nil
}

// gooNoteEvent is the argument of customer_note.
type gooNoteEvent struct {
	Order *gooOrder
	Note  string
}

// gooOrderNotes lists an order's notes, newest first; customerOnly for the customer's view.
func (s *Server) gooOrderNotes(ctx context.Context, orderID int64, customerOnly bool) ([]*gooOrderNote, error) {
	q := `SELECT id, order_id, content, is_customer_note, added_by, created_at FROM goo_order_notes WHERE order_id = $1`
	if customerOnly {
		q += ` AND is_customer_note`
	}
	rows, err := s.store.Pool.Query(ctx, q+` ORDER BY created_at DESC, id DESC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*gooOrderNote
	for rows.Next() {
		n := &gooOrderNote{}
		if err := rows.Scan(&n.ID, &n.OrderID, &n.Content, &n.IsCustomer, &n.AddedBy, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Status -------------------------------------------------------------------------------------------

// gooSetStatus is WC_Order::update_status(): saves, records a note and runs the transition's
// bookkeeping (stock, coupons, downloads, e-mails through order_status_changed).
func (s *Server) gooSetStatus(ctx context.Context, o *gooOrder, to, note string, by string) error {
	to = strings.TrimPrefix(to, "wc-")
	valid := false
	for _, st := range gooOrderStatuses {
		if st.Value == to {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("invalid order status %q", to)
	}
	from := o.Status
	if from == to {
		if note != "" {
			return s.gooAddOrderNote(ctx, o, note, false, by)
		}
		return nil
	}
	o.Status = to
	now := time.Now()
	if containsStr(gooPaidStatuses, to) && o.DatePaid == nil {
		o.DatePaid = &now
	}
	if to == "completed" {
		o.DateCompleted = &now
	}
	if err := s.gooSaveOrder(ctx, o, false); err != nil {
		return err
	}
	msg := fmt.Sprintf("Order status changed from %s to %s.", gooStatusLabel(from), gooStatusLabel(to))
	if note != "" {
		msg = strings.TrimSpace(note + " " + msg)
	}
	_ = s.gooAddOrderNote(ctx, o, msg, false, by)
	s.gooStatusTransition(ctx, o, from, to)
	s.gooFire(ctx, "order_status_changed", &gooStatusChange{Order: o, From: from, To: to})
	s.gooPurgeOrder(ctx, o)
	return nil
}

// gooStatusTransition is WooCommerce's status hooks: stock is reduced when an order becomes
// processing / completed / on-hold and restored when it is cancelled (or pending again); coupons
// count their uses likewise; downloads are granted when paid.
func (s *Server) gooStatusTransition(ctx context.Context, o *gooOrder, from, to string) {
	switch to {
	case "processing", "completed", "on-hold":
		s.gooReduceStock(ctx, o)
		s.gooRecordCouponUsage(ctx, o, true)
		if to != "on-hold" || !s.gooOptBool("downloads_grant_access_after_payment") {
			s.gooGrantDownloads(ctx, o)
		}
		if containsStr(gooPaidStatuses, to) {
			s.gooRecordSales(ctx, o)
		}
	case "cancelled", "pending", "failed":
		if to == "cancelled" || from == "on-hold" || from == "processing" {
			s.gooRestoreStock(ctx, o)
		}
		if to == "cancelled" {
			s.gooRecordCouponUsage(ctx, o, false)
		}
	}
}

// gooReduceStock is wc_reduce_stock_levels(): once per order.
func (s *Server) gooReduceStock(ctx context.Context, o *gooOrder) {
	if o.StockReduced || !s.gooManageStock() {
		return
	}
	var changes []string
	for _, it := range o.ItemsOf("line_item") {
		pid := it.ProductID
		if it.VariationID != 0 {
			pid = it.VariationID
		}
		p, err := s.gooGetProduct(ctx, pid)
		if err != nil || !s.gooManagingStock(ctx, p) {
			continue
		}
		owner := s.gooStockOwner(ctx, p)
		old := 0
		if owner.StockQuantity != nil {
			old = *owner.StockQuantity
		}
		n, err := s.gooUpdateStock(ctx, p, -it.Qty(), false)
		if err != nil {
			continue
		}
		it.Meta["_reduced_stock"] = float64(it.Qty())
		label := s.gooProductTitle(ctx, p)
		if p.SKU != "" {
			label += " (" + p.SKU + ")"
		}
		changes = append(changes, fmt.Sprintf("%s %d&rarr;%d", html.EscapeString(label), old, n))
		if n < 0 {
			s.gooFire(ctx, "backorder", &gooBackorderEvent{Order: o, Product: p, Quantity: it.Qty()})
		}
	}
	o.StockReduced = true
	_ = s.gooSaveOrder(ctx, o, true)
	if len(changes) > 0 {
		_ = s.gooAddOrderNote(ctx, o, "Stock levels reduced: "+strings.Join(changes, ", "), false, "")
	}
}

// gooBackorderEvent is the argument of backorder.
type gooBackorderEvent struct {
	Order    *gooOrder
	Product  *gooProduct
	Quantity int
}

// gooRestoreStock is wc_increase_stock_levels(): puts back what was reduced.
func (s *Server) gooRestoreStock(ctx context.Context, o *gooOrder) {
	if !o.StockReduced {
		return
	}
	var changes []string
	for _, it := range o.ItemsOf("line_item") {
		red, _ := it.Meta["_reduced_stock"].(float64)
		if red <= 0 {
			continue
		}
		pid := it.ProductID
		if it.VariationID != 0 {
			pid = it.VariationID
		}
		p, err := s.gooGetProduct(ctx, pid)
		if err != nil {
			continue
		}
		owner := s.gooStockOwner(ctx, p)
		old := 0
		if owner.StockQuantity != nil {
			old = *owner.StockQuantity
		}
		n, err := s.gooUpdateStock(ctx, p, int(red), false)
		if err != nil {
			continue
		}
		delete(it.Meta, "_reduced_stock")
		changes = append(changes, fmt.Sprintf("%s %d&rarr;%d", html.EscapeString(s.gooProductTitle(ctx, p)), old, n))
	}
	o.StockReduced = false
	_ = s.gooSaveOrder(ctx, o, true)
	if len(changes) > 0 {
		_ = s.gooAddOrderNote(ctx, o, "Stock levels increased: "+strings.Join(changes, ", "), false, "")
	}
}

// gooRecordCouponUsage counts (or uncounts) the order's coupon uses once.
func (s *Server) gooRecordCouponUsage(ctx context.Context, o *gooOrder, increase bool) {
	recorded, _ := o.Meta["_recorded_coupon_usage_counts"].(bool)
	if recorded == increase {
		return
	}
	who := o.Billing["email"]
	if o.CustomerID != 0 {
		who = itoa64(o.CustomerID)
	}
	for _, c := range o.ItemsOf("coupon") {
		if increase {
			_ = s.gooIncreaseCouponUsage(ctx, c.Name, who)
		} else {
			_ = s.gooDecreaseCouponUsage(ctx, c.Name, who)
		}
	}
	o.Meta["_recorded_coupon_usage_counts"] = increase
	_ = s.gooSaveOrder(ctx, o, false)
}

// gooRecordSales counts total_sales once per order.
func (s *Server) gooRecordSales(ctx context.Context, o *gooOrder) {
	if done, _ := o.Meta["_recorded_sales"].(bool); done {
		return
	}
	for _, it := range o.ItemsOf("line_item") {
		_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_products SET total_sales = total_sales + $2 WHERE post_id = $1`, it.ProductID, it.Qty())
	}
	o.Meta["_recorded_sales"] = true
	_ = s.gooSaveOrder(ctx, o, false)
}

// gooGrantDownloads is wc_downloadable_product_permissions(): once per order.
func (s *Server) gooGrantDownloads(ctx context.Context, o *gooOrder) {
	if o.DownloadsGranted {
		return
	}
	for _, it := range o.ItemsOf("line_item") {
		pid := it.ProductID
		if it.VariationID != 0 {
			pid = it.VariationID
		}
		p, err := s.gooGetProduct(ctx, pid)
		if err != nil || !p.Downloadable {
			continue
		}
		for _, d := range p.Downloads {
			var remaining any
			if p.DownloadLimit > 0 {
				remaining = p.DownloadLimit * it.Qty()
			}
			var expires any
			if p.DownloadExpiry > 0 {
				expires = time.Now().AddDate(0, 0, p.DownloadExpiry)
			}
			_, _ = s.store.Pool.Exec(ctx, `INSERT INTO goo_download_permissions (download_id, product_id, order_id, order_key, user_email, user_id,
				downloads_remaining, access_expires) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, d.ID, p.ID, o.ID, o.OrderKey, o.Billing["email"], o.CustomerID, remaining, expires)
		}
	}
	o.DownloadsGranted = true
	_ = s.gooSaveOrder(ctx, o, false)
}

// gooPaymentComplete is WC_Order::payment_complete(): the transaction id, then processing (or
// completed when nothing needs processing: only virtual downloadable items).
func (s *Server) gooPaymentComplete(ctx context.Context, o *gooOrder, transactionID string) error {
	if !containsStr([]string{"on-hold", "pending", "failed", "cancelled", "checkout-draft"}, o.Status) {
		return nil
	}
	if transactionID != "" {
		o.TransactionID = transactionID
	}
	now := time.Now()
	o.DatePaid = &now
	to := "processing"
	if !s.gooOrderNeedsProcessing(ctx, o) {
		to = "completed"
	}
	if err := s.gooSetStatus(ctx, o, to, "", ""); err != nil {
		return err
	}
	s.gooFire(ctx, "payment_complete", o)
	return nil
}

// gooOrderNeedsProcessing is needs_processing(): an item that is not virtual and downloadable.
func (s *Server) gooOrderNeedsProcessing(ctx context.Context, o *gooOrder) bool {
	for _, it := range o.ItemsOf("line_item") {
		pid := it.ProductID
		if it.VariationID != 0 {
			pid = it.VariationID
		}
		p, err := s.gooGetProduct(ctx, pid)
		if err != nil {
			return true
		}
		if !(p.Virtual && p.Downloadable) {
			return true
		}
	}
	return false
}

// Refunds -------------------------------------------------------------------------------------------

// gooRefundLine is a line of a refund: an item, a quantity and amounts.
type gooRefundLine struct {
	ItemID   int64
	Qty      int
	Total    float64
	TaxTotal map[string]float64
}

// gooRefundEvent is the argument of order_refunded.
type gooRefundEvent struct {
	Order  *gooOrder
	Refund *gooOrder
}

// gooRefunds are an order's refunds, newest first.
func (s *Server) gooRefunds(ctx context.Context, orderID int64) ([]*gooOrder, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+gooOrderCols+` FROM goo_orders WHERE parent_id = $1 AND type = 'shop_order_refund' ORDER BY date_created DESC, id DESC`, orderID)
	if err != nil {
		return nil, err
	}
	var out []*gooOrder
	for rows.Next() {
		r, err := gooScanOrder(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()
	for _, r := range out {
		r.Items, _ = s.gooOrderItems(ctx, r.ID)
	}
	return out, nil
}

// gooTotalRefunded is get_total_refunded().
func (s *Server) gooTotalRefunded(ctx context.Context, orderID int64) float64 {
	var t float64
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(sum(-total),0)::float8 FROM goo_orders WHERE parent_id = $1 AND type = 'shop_order_refund'`, orderID).Scan(&t)
	return t
}

// gooCreateRefund is wc_create_refund(): a refund order with negative lines, optional stock restock,
// the gateway's refund when apiRefund is set, and the order becomes refunded when fully refunded.
func (s *Server) gooCreateRefund(ctx context.Context, o *gooOrder, amount float64, reason string, lines []gooRefundLine, restock, apiRefund bool, by int64) (*gooOrder, error) {
	dp := s.gooDecimals()
	amount = gooRound(amount, dp)
	already := s.gooTotalRefunded(ctx, o.ID)
	if amount <= 0 || amount > gooRound(o.Total-already, dp)+0.00001 {
		return nil, errors.New("Invalid refund amount.")
	}
	r := &gooOrder{Type: "shop_order_refund", ParentID: o.ID, Status: "completed", Currency: o.Currency, Total: -amount, RefundReason: reason,
		RefundedBy: by, Billing: map[string]string{}, Shipping: map[string]string{}, Meta: map[string]any{}}
	byID := map[int64]*gooOrderItem{}
	for _, it := range o.Items {
		byID[it.ID] = it
	}
	var taxTotal float64
	for _, l := range lines {
		it := byID[l.ItemID]
		if it == nil || (l.Qty == 0 && l.Total == 0 && len(l.TaxTotal) == 0) {
			continue
		}
		ri := &gooOrderItem{Type: it.Type, Name: it.Name, ProductID: it.ProductID, VariationID: it.VariationID, Quantity: float64(-l.Qty),
			TaxClass: it.TaxClass, Total: -l.Total, Subtotal: -l.Total, Meta: map[string]any{"_refunded_item_id": float64(it.ID)},
			Taxes: gooItemTaxes{Total: map[string]float64{}, Subtotal: map[string]float64{}}}
		for k, v := range l.TaxTotal {
			ri.Taxes.Total[k], ri.Taxes.Subtotal[k] = -v, -v
			ri.TotalTax -= v
			taxTotal += v
		}
		ri.SubtotalTax = ri.TotalTax
		r.Items = append(r.Items, ri)
	}
	r.TotalTax = -gooRound(taxTotal, dp)
	if apiRefund && o.PaymentMethod != "" {
		gw := s.gooGateway(o.PaymentMethod)
		if gw == nil || !gw.Supports("refunds") {
			return nil, errors.New("The payment gateway for this order does not support automatic refunds.")
		}
		if err := gw.ProcessRefund(s, ctx, o, amount, reason); err != nil {
			return nil, err
		}
		r.Meta["_refunded_payment"] = true
	}
	if err := s.gooSaveOrder(ctx, r, true); err != nil {
		return nil, err
	}
	if restock {
		var changes []string
		for _, l := range lines {
			it := byID[l.ItemID]
			if it == nil || it.Type != "line_item" || l.Qty <= 0 {
				continue
			}
			pid := it.ProductID
			if it.VariationID != 0 {
				pid = it.VariationID
			}
			p, err := s.gooGetProduct(ctx, pid)
			if err != nil || !s.gooManagingStock(ctx, p) {
				continue
			}
			old := s.gooStockQty(ctx, p)
			n, _ := s.gooUpdateStock(ctx, p, l.Qty, false)
			if red, ok := it.Meta["_reduced_stock"].(float64); ok {
				it.Meta["_reduced_stock"] = red - float64(l.Qty)
			}
			changes = append(changes, fmt.Sprintf("%s %d&rarr;%d", html.EscapeString(s.gooProductTitle(ctx, p)), old, n))
		}
		if len(changes) > 0 {
			_ = s.gooSaveOrder(ctx, o, true)
			_ = s.gooAddOrderNote(ctx, o, "Item #"+itoa64(o.ID)+" stock increased: "+strings.Join(changes, ", "), false, "")
		}
	}
	note := fmt.Sprintf("Refunded %s", s.gooPricePlain(amount, o.Currency))
	if reason != "" {
		note += " &ndash; " + html.EscapeString(reason)
	}
	_ = s.gooAddOrderNote(ctx, o, note, false, "")
	if gooRound(already+amount, dp) >= gooRound(o.Total, dp) {
		_ = s.gooSetStatus(ctx, o, "refunded", "Order status set to refunded.", "")
		s.gooFire(ctx, "order_fully_refunded", &gooRefundEvent{Order: o, Refund: r})
	} else {
		s.gooFire(ctx, "order_partially_refunded", &gooRefundEvent{Order: o, Refund: r})
	}
	s.gooFire(ctx, "order_refunded", &gooRefundEvent{Order: o, Refund: r})
	return r, nil
}

// gooDeleteRefund removes a refund (the admin's "Delete refund").
func (s *Server) gooDeleteRefund(ctx context.Context, refundID int64) error {
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_orders WHERE id = $1 AND type = 'shop_order_refund'`, refundID)
	return err
}

// Queries ----------------------------------------------------------------------------------------------

// gooOrderQuery is wc_get_orders() for the parts the admin, My Account and the REST API need.
type gooOrderQuery struct {
	Status     []string
	CustomerID int64
	Email      string
	Search     string
	After      *time.Time
	Before     *time.Time
	OrderBy    string // date, id, total
	Order      string
	Page       int
	PerPage    int
	IDs        []int64
}

func (s *Server) gooQueryOrders(ctx context.Context, q gooOrderQuery) ([]*gooOrder, int, error) {
	conds := []string{"type = 'shop_order'"}
	args := []any{}
	add := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if len(q.Status) > 0 {
		conds = append(conds, "status = ANY("+add(q.Status)+")")
	} else {
		conds = append(conds, "status NOT IN ('trash', 'checkout-draft')")
	}
	if q.CustomerID != 0 {
		conds = append(conds, "customer_id = "+add(q.CustomerID))
	}
	if q.Email != "" {
		conds = append(conds, "billing_email = "+add(strings.ToLower(q.Email)))
	}
	if q.Search != "" {
		p := add("%" + strings.ToLower(q.Search) + "%")
		cond := "(lower(billing::text) LIKE " + p + " OR lower(shipping::text) LIKE " + p + " OR id IN (SELECT order_id FROM goo_order_items WHERE lower(name) LIKE " + p + ")"
		if n, err := strconv.ParseInt(strings.TrimPrefix(q.Search, "#"), 10, 64); err == nil {
			cond += " OR id = " + add(n)
		}
		conds = append(conds, cond+")")
	}
	if q.After != nil {
		conds = append(conds, "date_created >= "+add(*q.After))
	}
	if q.Before != nil {
		conds = append(conds, "date_created < "+add(*q.Before))
	}
	if len(q.IDs) > 0 {
		conds = append(conds, "id = ANY("+add(q.IDs)+")")
	}
	where := strings.Join(conds, " AND ")
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_orders WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	col := "date_created"
	switch q.OrderBy {
	case "id":
		col = "id"
	case "total":
		col = "total"
	case "modified":
		col = "date_modified"
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	sql := `SELECT ` + gooOrderCols + ` FROM goo_orders WHERE ` + where + ` ORDER BY ` + col + ` ` + dir + `, id ` + dir
	if q.PerPage > 0 {
		page := max(q.Page, 1)
		sql += fmt.Sprintf(" LIMIT %d OFFSET %d", q.PerPage, (page-1)*q.PerPage)
	}
	rows, err := s.store.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	var out []*gooOrder
	for rows.Next() {
		o, err := gooScanOrder(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, o)
	}
	rows.Close()
	for _, o := range out {
		o.Items, _ = s.gooOrderItems(ctx, o.ID)
	}
	return out, total, nil
}

// gooOrderStatusCounts counts orders per status (the admin list's views).
func (s *Server) gooOrderStatusCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	rows, err := s.store.Pool.Query(ctx, `SELECT status, count(*) FROM goo_orders WHERE type = 'shop_order' GROUP BY status`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			out[st] = n
		}
	}
	return out
}

// gooPurgeOrder drops the cached pages an order affects (the products whose stock changed purge themselves).
func (s *Server) gooPurgeOrder(ctx context.Context, o *gooOrder) {
	s.purgeTags("goo:order:" + itoa64(o.ID))
}
