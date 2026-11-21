package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// WooCommerce data of a WP to Pursae package into Pursae Commerce: tax rates, coupons, orders (with
// their items, notes, refunds and download permissions) and customers. Pursae Commerce keeps orders
// the way WooCommerce's HPOS tables do and order items with WooCommerce's item meta names (rate_id,
// method_id, discount_amount, _reduced_stock, _refunded_item_id ...), so most fields map one to one;
// ids are mapped through wp2p_map. Nothing here sends e-mails, changes stock or records sales again:
// the orders arrive with the bookkeeping flags they had on WordPress.

// wp2pNum is a WooCommerce amount: a JSON number or a decimal string ("" is 0).
type wp2pNum float64

func (n *wp2pNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		f = 0
	}
	*n = wp2pNum(f)
	return nil
}

// wp2pOptNum is an amount or limit that may be empty (null / ""): no minimum, no usage limit.
type wp2pOptNum struct {
	V     float64
	Valid bool
}

func (n *wp2pOptNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	f, err := strconv.ParseFloat(s, 64)
	*n = wp2pOptNum{V: f, Valid: err == nil}
	return nil
}

func (n wp2pOptNum) floatPtr() *float64 {
	if !n.Valid {
		return nil
	}
	v := n.V
	return &v
}

func (n wp2pOptNum) intPtr() *int {
	if !n.Valid || n.V <= 0 {
		return nil
	}
	v := int(n.V)
	return &v
}

// wp2pTime is a Unix timestamp (0 / null = none).
type wp2pTime int64

func (t wp2pTime) ptr() *time.Time {
	if t <= 0 {
		return nil
	}
	v := time.Unix(int64(t), 0).UTC()
	return &v
}

func (t wp2pTime) or(def time.Time) time.Time {
	if p := t.ptr(); p != nil {
		return *p
	}
	return def
}

func (t *wp2pTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	n, _ := strconv.ParseInt(s, 10, 64)
	*t = wp2pTime(n)
	return nil
}

func wp2pMetaNum(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func wp2pMetaStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "1"
		}
		return ""
	case nil:
		return ""
	}
	raw, _ := json.Marshal(m[k])
	return string(raw)
}

// Tax rates ---------------------------------------------------------------------------------------

type wp2pTaxRate struct {
	ID        int64    `json:"id"`
	Country   string   `json:"country"`
	State     string   `json:"state"`
	Rate      wp2pNum  `json:"rate"`
	Name      string   `json:"name"`
	Priority  int      `json:"priority"`
	Compound  bool     `json:"compound"`
	Shipping  bool     `json:"shipping"`
	Order     int      `json:"order"`
	Class     string   `json:"class"`
	Postcodes []string `json:"postcodes"`
	Cities    []string `json:"cities"`
}

// importTaxRates adds the rates that are not here yet (the same location, rate, name and class is the same rate).
func (r *wp2pRun) importTaxRates(ctx context.Context) error {
	r.taxRates = r.mapLoad(ctx, "tax_rate")
	return wp2pEachLine(filepath.Join(r.dir, "woo", "tax_rates.jsonl"), func(raw []byte) error {
		var t wp2pTaxRate
		if json.Unmarshal(raw, &t) != nil || t.ID == 0 {
			r.rep.skip("tax_rate", "", "unreadable line")
			return nil //nolint:nilerr // a bad record is reported and skipped, not fatal
		}
		r.prog.Items["tax_rates"]++
		if _, ok := r.taxRates[t.ID]; ok {
			r.rep.Existing["tax_rates"]++
			return nil
		}
		var exID int64
		_ = r.s.store.Pool.QueryRow(ctx, `SELECT id FROM goo_tax_rates WHERE country = upper($1) AND state = upper($2) AND rate = $3 AND name = $4 AND class = $5
			AND priority = $6 ORDER BY id LIMIT 1`, t.Country, t.State, float64(t.Rate), t.Name, t.Class, t.Priority).Scan(&exID)
		if exID != 0 {
			r.rep.Existing["tax_rates"]++
		} else {
			gr := &gooTaxRate{Country: t.Country, State: t.State, Rate: float64(t.Rate), Name: defaultStr(t.Name, "Tax"), Priority: max(t.Priority, 1),
				Compound: t.Compound, Shipping: t.Shipping, Position: t.Order, Class: t.Class, Postcodes: nonNilSlice(t.Postcodes), Cities: nonNilSlice(t.Cities)}
			if err := r.s.gooSaveTaxRate(ctx, gr); err != nil {
				r.rep.skip("tax_rate", t.Name, err.Error())
				return nil
			}
			exID = gr.ID
			r.rep.Counts["tax_rates"]++
		}
		r.taxRates[t.ID] = exID
		return r.mapSet(ctx, "tax_rate", t.ID, exID)
	})
}

// Coupons -----------------------------------------------------------------------------------------

type wp2pCoupon struct {
	ID                        int64          `json:"id"`
	Code                      string         `json:"code"`
	Description               string         `json:"description"`
	Status                    string         `json:"status"`
	DiscountType              string         `json:"discount_type"`
	Amount                    wp2pNum        `json:"amount"`
	DateExpires               wp2pTime       `json:"date_expires"`
	FreeShipping              bool           `json:"free_shipping"`
	MinimumAmount             wp2pOptNum     `json:"minimum_amount"`
	MaximumAmount             wp2pOptNum     `json:"maximum_amount"`
	IndividualUse             bool           `json:"individual_use"`
	ExcludeSaleItems          bool           `json:"exclude_sale_items"`
	ProductIDs                []int64        `json:"product_ids"`
	ExcludedProductIDs        []int64        `json:"excluded_product_ids"`
	ProductCategories         []int64        `json:"product_categories"`
	ExcludedProductCategories []int64        `json:"excluded_product_categories"`
	EmailRestrictions         []string       `json:"email_restrictions"`
	UsageLimit                wp2pOptNum     `json:"usage_limit"`
	UsageLimitPerUser         wp2pOptNum     `json:"usage_limit_per_user"`
	LimitUsageToXItems        wp2pOptNum     `json:"limit_usage_to_x_items"`
	UsageCount                int            `json:"usage_count"`
	UsedBy                    []string       `json:"used_by"`
	DateCreated               wp2pTime       `json:"date_created"`
	DateModified              wp2pTime       `json:"date_modified"`
	Meta                      map[string]any `json:"meta"`
}

// mapIDs maps ids of the file; ids that were not imported are dropped and counted.
func (r *wp2pRun) mapIDs(ids []int64, m map[int64]int64, what string) []int64 {
	out := []int64{}
	for _, id := range ids {
		if n, ok := m[id]; ok {
			out = append(out, n)
		} else if id != 0 {
			r.rep.Unmapped[what]++
		}
	}
	return out
}

func (r *wp2pRun) importCoupons(ctx context.Context) error {
	if err := r.loadUsers(ctx); err != nil {
		return err
	}
	r.loadContentMaps(ctx)
	r.coupons, r.couponByCode = r.mapLoad(ctx, "coupon"), map[string]int64{}
	return wp2pEachLine(filepath.Join(r.dir, "woo", "coupons.jsonl"), func(raw []byte) error {
		var wc wp2pCoupon
		if json.Unmarshal(raw, &wc) != nil || wc.ID == 0 {
			r.rep.skip("coupon", "", "unreadable line")
			return nil //nolint:nilerr // a bad record is reported and skipped, not fatal
		}
		r.prog.Items["coupons"]++
		code := strings.ToLower(strings.TrimSpace(wc.Code))
		if id, ok := r.coupons[wc.ID]; ok {
			r.couponByCode[code] = id
			r.rep.Existing["coupons"]++
			return nil
		}
		if ex, err := r.s.gooCouponByCode(ctx, code); err == nil {
			r.coupons[wc.ID], r.couponByCode[code] = ex.ID, ex.ID
			r.rep.Existing["coupons"]++
			r.rep.Skipped = append(r.rep.Skipped, wp2pSkip{Kind: "coupon", Item: code, Reason: "a coupon with this code already exists here: kept as it is"})
			return r.mapSet(ctx, "coupon", wc.ID, ex.ID)
		}
		status := "publish"
		switch wc.Status {
		case "publish", "":
		case "trash":
			r.rep.skip("coupon", code, "in the trash on WordPress")
			return nil
		default:
			status = "draft"
		}
		c := &gooCoupon{Code: code, Description: wc.Description, Status: status, DiscountType: wc.DiscountType, Amount: float64(wc.Amount),
			DateExpires: wc.DateExpires.ptr(), FreeShipping: wc.FreeShipping, MinimumAmount: wc.MinimumAmount.floatPtr(), MaximumAmount: wc.MaximumAmount.floatPtr(),
			IndividualUse: wc.IndividualUse, ExcludeSaleItems: wc.ExcludeSaleItems,
			ProductIDs:                r.mapIDs(wc.ProductIDs, r.posts, "coupon products that were not imported"),
			ExcludedProductIDs:        r.mapIDs(wc.ExcludedProductIDs, r.posts, "coupon products that were not imported"),
			ProductCategories:         r.mapIDs(wc.ProductCategories, r.terms, "coupon categories that were not imported"),
			ExcludedProductCategories: r.mapIDs(wc.ExcludedProductCategories, r.terms, "coupon categories that were not imported"),
			EmailRestrictions:         nonNilSlice(wc.EmailRestrictions), UsageLimit: wc.UsageLimit.intPtr(), UsageLimitPerUser: wc.UsageLimitPerUser.intPtr(),
			LimitUsageToXItems: wc.LimitUsageToXItems.intPtr(), Meta: wc.Meta}
		switch wc.DiscountType {
		case "percent", "fixed_cart", "fixed_product":
		default:
			r.rep.Unmapped["coupon type "+wc.DiscountType+" (imported as a fixed cart discount)"]++
		}
		if err := r.s.gooSaveCoupon(ctx, c); err != nil {
			r.rep.skip("coupon", code, err.Error())
			return nil
		}
		// used_by holds user ids (or e-mails of guests): the ids become ours.
		usedBy := make([]string, 0, len(wc.UsedBy))
		for _, u := range wc.UsedBy {
			if old, err := strconv.ParseInt(u, 10, 64); err == nil {
				if id, ok := r.users[old]; ok {
					usedBy = append(usedBy, strconv.FormatInt(id, 10))
					continue
				}
			}
			usedBy = append(usedBy, u)
		}
		created := wc.DateCreated.or(time.Now())
		if _, err := r.s.store.Pool.Exec(ctx, `UPDATE goo_coupons SET usage_count = $2, used_by = $3, date_created = $4, date_modified = $5 WHERE id = $1`,
			c.ID, wc.UsageCount, usedBy, created, wc.DateModified.or(created)); err != nil {
			return err
		}
		r.coupons[wc.ID], r.couponByCode[code] = c.ID, c.ID
		r.rep.Counts["coupons"]++
		r.save(false)
		return r.mapSet(ctx, "coupon", wc.ID, c.ID)
	})
}

// Orders ------------------------------------------------------------------------------------------

type wp2pItem struct {
	ID   int64          `json:"id"`
	Type string         `json:"type"`
	Name string         `json:"name"`
	Meta map[string]any `json:"meta"` // woocommerce_order_itemmeta, values unserialized
}

type wp2pNote struct {
	Content    string   `json:"content"`
	IsCustomer bool     `json:"customer_note"`
	AddedBy    string   `json:"added_by"`
	Date       wp2pTime `json:"date"`
}

type wp2pDownload struct {
	DownloadID         string     `json:"download_id"`
	ProductID          int64      `json:"product_id"`
	UserEmail          string     `json:"user_email"`
	UserID             int64      `json:"user_id"`
	DownloadsRemaining wp2pOptNum `json:"downloads_remaining"`
	AccessGranted      wp2pTime   `json:"access_granted"`
	AccessExpires      wp2pTime   `json:"access_expires"`
	DownloadCount      int        `json:"download_count"`
}

type wp2pRefund struct {
	ID              int64          `json:"id"`
	Amount          wp2pNum        `json:"amount"`
	Reason          string         `json:"reason"`
	RefundedBy      int64          `json:"refunded_by"`
	RefundedPayment bool           `json:"refunded_payment"`
	DateCreated     wp2pTime       `json:"date_created"`
	ShippingTotal   wp2pNum        `json:"shipping_total"`
	ShippingTax     wp2pNum        `json:"shipping_tax"`
	CartTax         wp2pNum        `json:"cart_tax"`
	DiscountTotal   wp2pNum        `json:"discount_total"`
	DiscountTax     wp2pNum        `json:"discount_tax"`
	TotalTax        wp2pNum        `json:"total_tax"`
	Meta            map[string]any `json:"meta"`
	Items           []wp2pItem     `json:"items"`
}

type wp2pOrder struct {
	ID                  int64             `json:"id"`
	Status              string            `json:"status"`
	Currency            string            `json:"currency"`
	PricesIncludeTax    bool              `json:"prices_include_tax"`
	CustomerID          int64             `json:"customer_id"`
	Billing             map[string]string `json:"billing"`
	Shipping            map[string]string `json:"shipping"`
	PaymentMethod       string            `json:"payment_method"`
	PaymentMethodTitle  string            `json:"payment_method_title"`
	TransactionID       string            `json:"transaction_id"`
	CustomerIP          string            `json:"customer_ip"`
	UserAgent           string            `json:"user_agent"`
	CustomerNote        string            `json:"customer_note"`
	CreatedVia          string            `json:"created_via"`
	CartHash            string            `json:"cart_hash"`
	OrderKey            string            `json:"order_key"`
	DiscountTotal       wp2pNum           `json:"discount_total"`
	DiscountTax         wp2pNum           `json:"discount_tax"`
	ShippingTotal       wp2pNum           `json:"shipping_total"`
	ShippingTax         wp2pNum           `json:"shipping_tax"`
	CartTax             wp2pNum           `json:"cart_tax"`
	Total               wp2pNum           `json:"total"`
	TotalTax            wp2pNum           `json:"total_tax"`
	DateCreated         wp2pTime          `json:"date_created"`
	DateModified        wp2pTime          `json:"date_modified"`
	DatePaid            wp2pTime          `json:"date_paid"`
	DateCompleted       wp2pTime          `json:"date_completed"`
	StockReduced        bool              `json:"stock_reduced"`
	DownloadsGranted    bool              `json:"downloads_granted"`
	NewOrderEmailSent   bool              `json:"new_order_email_sent"`
	RecordedSales       bool              `json:"recorded_sales"`
	RecordedCouponUsage bool              `json:"recorded_coupon_usage"`
	Meta                map[string]any    `json:"meta"`
	Items               []wp2pItem        `json:"items"`
	Notes               []wp2pNote        `json:"notes"`
	Refunds             []wp2pRefund      `json:"refunds"`
	Downloads           []wp2pDownload    `json:"downloads"`
}

// wp2pOrderStatus maps a WooCommerce status (with or without "wc-") to Pursae Commerce's; ok is false
// for statuses Pursae Commerce does not have (custom statuses of other plugins).
func wp2pOrderStatus(st string) (string, bool) {
	st = strings.TrimPrefix(st, "wc-")
	for _, o := range gooOrderStatuses {
		if o.Value == st {
			return st, true
		}
	}
	return "on-hold", false
}

// wp2pTaxes reads _line_tax_data / taxes ({"total": {rate id: amount}, "subtotal": {...}}) with the rate ids mapped.
func (r *wp2pRun) wp2pTaxes(v any) gooItemTaxes {
	out := gooItemTaxes{Total: map[string]float64{}, Subtotal: map[string]float64{}}
	m, _ := v.(map[string]any)
	conv := func(x any, into map[string]float64) {
		amounts, _ := x.(map[string]any)
		for k, a := range amounts {
			old, err := strconv.ParseInt(k, 10, 64)
			if id, ok := r.taxRates[old]; err == nil && ok {
				k = strconv.FormatInt(id, 10)
			}
			into[k] += wp2pMetaNum(map[string]any{"v": a}, "v")
		}
	}
	conv(m["total"], out.Total)
	conv(m["subtotal"], out.Subtotal)
	return out
}

// orderItem is one woocommerce_order_items row with its meta as a Pursae Commerce item.
func (r *wp2pRun) orderItem(wi wp2pItem, itemIDs map[int64]int64) *gooOrderItem {
	m := wi.Meta
	if m == nil {
		m = map[string]any{}
	}
	it := &gooOrderItem{Type: wi.Type, Name: wi.Name, Quantity: 1, Meta: map[string]any{}}
	take := func(k string) any {
		v := m[k]
		delete(m, k)
		return v
	}
	num := func(k string) float64 { v := wp2pMetaNum(m, k); delete(m, k); return v }
	str := func(k string) string { v := wp2pMetaStr(m, k); delete(m, k); return v }
	switch wi.Type {
	case "line_item":
		pid, vid := int64(num("_product_id")), int64(num("_variation_id"))
		if pid != 0 {
			if id, ok := r.posts[pid]; ok {
				it.ProductID = id
			} else {
				r.rep.Unmapped["order lines of products that were not imported (kept by name)"]++
			}
		}
		if vid != 0 {
			if id, ok := r.posts[vid]; ok {
				it.VariationID = id
			}
		}
		it.Quantity = num("_qty")
		it.TaxClass = str("_tax_class")
		it.Subtotal, it.SubtotalTax = num("_line_subtotal"), num("_line_subtotal_tax")
		it.Total, it.TotalTax = num("_line_total"), num("_line_tax")
		it.Taxes = r.wp2pTaxes(take("_line_tax_data"))
	case "fee":
		it.TaxClass = str("_tax_class")
		it.Total, it.TotalTax = num("_line_total"), num("_line_tax")
		it.Subtotal = it.Total
		it.Taxes = r.wp2pTaxes(take("_line_tax_data"))
	case "shipping":
		it.Total, it.TotalTax = num("cost"), num("total_tax")
		it.Subtotal = it.Total
		it.Taxes = r.wp2pTaxes(take("taxes"))
	case "coupon":
		it.Total, it.TotalTax = wp2pMetaNum(m, "discount_amount"), wp2pMetaNum(m, "discount_amount_tax")
		if id, ok := r.couponByCode[strings.ToLower(wi.Name)]; ok {
			it.Meta["coupon_id"] = float64(id)
		}
	case "tax":
		if old := int64(num("rate_id")); old != 0 {
			if id, ok := r.taxRates[old]; ok {
				it.Meta["rate_id"] = float64(id)
			} else {
				it.Meta["rate_id"] = float64(old)
				r.rep.Unmapped["tax lines of tax rates that were not imported"]++
			}
		}
		for _, k := range []string{"tax_amount", "shipping_tax_amount", "rate_percent"} {
			if _, ok := m[k]; ok {
				it.Meta[k] = num(k)
			}
		}
		if _, ok := m["compound"]; ok {
			c := str("compound")
			it.Meta["compound"] = c == "1" || c == "yes" || c == "true"
		}
	default:
		r.rep.Unmapped["order item type "+wi.Type]++
	}
	for k, v := range m {
		switch k {
		case "_reduced_stock", "discount_amount", "discount_amount_tax":
			it.Meta[k] = wp2pMetaNum(m, k)
		case "_refunded_item_id":
			old := int64(wp2pMetaNum(m, k))
			if id, ok := itemIDs[old]; ok {
				it.Meta[k] = float64(id)
			}
		default:
			it.Meta[k] = v
		}
	}
	return it
}

// importOrders imports the orders one by one, each with its refunds, notes and download permissions
// in one transaction, so a run that stops never leaves half an order.
func (r *wp2pRun) importOrders(ctx context.Context) error {
	if err := r.loadUsers(ctx); err != nil {
		return err
	}
	r.loadContentMaps(ctx)
	if r.taxRates == nil {
		r.taxRates = r.mapLoad(ctx, "tax_rate")
	}
	if r.coupons == nil {
		r.coupons, r.couponByCode = r.mapLoad(ctx, "coupon"), map[string]int64{}
		rows, err := r.s.store.Pool.Query(ctx, `SELECT lower(code), id FROM goo_coupons WHERE id = ANY($1)`, wp2pMapValues(r.coupons))
		if err == nil {
			for rows.Next() {
				var code string
				var id int64
				if rows.Scan(&code, &id) == nil {
					r.couponByCode[code] = id
				}
			}
			rows.Close()
		}
	}
	return wp2pEachLine(filepath.Join(r.dir, "woo", "orders.jsonl"), func(raw []byte) error {
		var wo wp2pOrder
		if json.Unmarshal(raw, &wo) != nil || wo.ID == 0 {
			r.rep.skip("order", "", "unreadable line")
			return nil //nolint:nilerr // a bad record is reported and skipped, not fatal
		}
		r.prog.Items["orders"]++
		if r.mapGet(ctx, "order", wo.ID) != 0 {
			r.rep.Existing["orders"]++
			return nil
		}
		if err := r.importOrder(ctx, &wo); err != nil {
			r.rep.skip("order", "#"+strconv.FormatInt(wo.ID, 10), err.Error())
			return nil
		}
		r.rep.Counts["orders"]++
		r.rep.Counts["refunds"] += len(wo.Refunds)
		r.save(false)
		return nil
	})
}

func wp2pMapValues(m map[int64]int64) []int64 {
	out := make([]int64, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func (r *wp2pRun) importOrder(ctx context.Context, wo *wp2pOrder) error {
	status, known := wp2pOrderStatus(wo.Status)
	meta := wo.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	if !known {
		meta["_wp2p_original_status"] = wo.Status
		r.rep.Unmapped["orders with a status Pursae Commerce does not have (imported as on-hold)"]++
	}
	if wo.RecordedSales {
		meta["_recorded_sales"] = true
	}
	if wo.RecordedCouponUsage {
		meta["_recorded_coupon_usage_counts"] = true
	}
	meta["_wp2p_order_id"] = float64(wo.ID) // the order number customers know from WordPress
	var customer int64
	if wo.CustomerID != 0 {
		if id, ok := r.users[wo.CustomerID]; ok {
			customer = id
		} else {
			r.rep.Unmapped["orders whose customer account was not imported (kept as guest orders)"]++
		}
	}
	created := wo.DateCreated.or(time.Now().UTC())
	o := &gooOrder{Type: "shop_order", Status: status, Currency: wo.Currency, PricesIncludeTax: wo.PricesIncludeTax, CustomerID: customer,
		Billing: nonNilMap(wo.Billing), Shipping: nonNilMap(wo.Shipping), PaymentMethod: wo.PaymentMethod, PaymentMethodTitle: wo.PaymentMethodTitle,
		TransactionID: wo.TransactionID, CustomerIP: wo.CustomerIP, UserAgent: wo.UserAgent, CustomerNote: wo.CustomerNote,
		CreatedVia: defaultStr(wo.CreatedVia, "checkout"), CartHash: wo.CartHash, OrderKey: wo.OrderKey,
		DiscountTotal: float64(wo.DiscountTotal), DiscountTax: float64(wo.DiscountTax), ShippingTotal: float64(wo.ShippingTotal),
		ShippingTax: float64(wo.ShippingTax), CartTax: float64(wo.CartTax), Total: float64(wo.Total), TotalTax: float64(wo.TotalTax),
		StockReduced: wo.StockReduced, DownloadsGranted: wo.DownloadsGranted || len(wo.Downloads) > 0, NewOrderEmailSent: true,
		Meta: meta, DatePaid: wo.DatePaid.ptr(), DateCompleted: wo.DateCompleted.ptr()}
	oldItemIDs := make([]int64, 0, len(wo.Items))
	for _, wi := range wo.Items {
		o.Items = append(o.Items, r.orderItem(wi, nil))
		oldItemIDs = append(oldItemIDs, wi.ID)
	}
	return pgx.BeginFunc(ctx, r.s.store.Pool, func(tx pgx.Tx) error {
		if err := r.s.gooSaveOrderTx(ctx, tx, o, true); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE goo_orders SET date_created = $2, date_modified = $3 WHERE id = $1`, o.ID, created, wo.DateModified.or(created)); err != nil {
			return err
		}
		itemIDs := map[int64]int64{}
		for i, it := range o.Items {
			itemIDs[oldItemIDs[i]] = it.ID
		}
		for _, n := range wo.Notes {
			if _, err := tx.Exec(ctx, `INSERT INTO goo_order_notes (order_id, content, is_customer_note, added_by, created_at) VALUES ($1, $2, $3, $4, $5)`,
				o.ID, n.Content, n.IsCustomer, wp2pNoteBy(n.AddedBy), n.Date.or(created)); err != nil {
				return err
			}
		}
		for _, wr := range wo.Refunds {
			by := int64(0)
			if id, ok := r.users[wr.RefundedBy]; ok {
				by = id
			}
			rmeta := wr.Meta
			if rmeta == nil {
				rmeta = map[string]any{}
			}
			if wr.RefundedPayment {
				rmeta["_refunded_payment"] = true
			}
			ro := &gooOrder{Type: "shop_order_refund", ParentID: o.ID, Status: "completed", Currency: o.Currency, PricesIncludeTax: o.PricesIncludeTax,
				Total: -float64(wr.Amount), RefundReason: wr.Reason, RefundedBy: by, Billing: map[string]string{}, Shipping: map[string]string{},
				ShippingTotal: float64(wr.ShippingTotal), ShippingTax: float64(wr.ShippingTax), CartTax: float64(wr.CartTax),
				DiscountTotal: float64(wr.DiscountTotal), DiscountTax: float64(wr.DiscountTax), TotalTax: float64(wr.TotalTax), Meta: rmeta}
			for _, wi := range wr.Items {
				ro.Items = append(ro.Items, r.orderItem(wi, itemIDs))
			}
			if err := r.s.gooSaveOrderTx(ctx, tx, ro, true); err != nil {
				return err
			}
			rc := wr.DateCreated.or(created)
			if _, err := tx.Exec(ctx, `UPDATE goo_orders SET date_created = $2, date_modified = $2 WHERE id = $1`, ro.ID, rc); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO wp2p_map (source, kind, old_id, new_id) VALUES ($1, 'refund', $2, $3)
				ON CONFLICT (source, kind, old_id) DO UPDATE SET new_id = EXCLUDED.new_id`, r.src, wr.ID, ro.ID); err != nil {
				return err
			}
		}
		for _, d := range wo.Downloads {
			pid, ok := r.posts[d.ProductID]
			if !ok {
				r.rep.Unmapped["download permissions of products that were not imported"]++
				continue
			}
			uid := int64(0)
			if id, ok := r.users[d.UserID]; ok {
				uid = id
			}
			var remaining *int
			if d.DownloadsRemaining.Valid {
				v := int(d.DownloadsRemaining.V)
				remaining = &v
			}
			if _, err := tx.Exec(ctx, `INSERT INTO goo_download_permissions (download_id, product_id, order_id, order_key, user_email, user_id,
				downloads_remaining, access_granted, access_expires, download_count) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				d.DownloadID, pid, o.ID, o.OrderKey, d.UserEmail, uid, remaining, d.AccessGranted.or(created), d.AccessExpires.ptr(), d.DownloadCount); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO wp2p_map (source, kind, old_id, new_id) VALUES ($1, 'order', $2, $3)
			ON CONFLICT (source, kind, old_id) DO UPDATE SET new_id = EXCLUDED.new_id`, r.src, wo.ID, o.ID)
		return err
	})
}

// wp2pNoteBy is the "added by" of a note: WooCommerce writes "system" for its own notes, Pursae ”.
func wp2pNoteBy(v string) string {
	if strings.EqualFold(v, "system") || strings.EqualFold(v, "WooCommerce") {
		return ""
	}
	return v
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// Customers ---------------------------------------------------------------------------------------

type wp2pCustomer struct {
	UserID     int64             `json:"user_id"`
	Email      string            `json:"email"`
	Billing    map[string]string `json:"billing"`
	Shipping   map[string]string `json:"shipping"`
	Paying     bool              `json:"is_paying_customer"`
	LastActive wp2pTime          `json:"last_active"`
}

// importCustomers completes the customer accounts (created with the users) with their addresses;
// guest customers have no account here either: Customers lists them from their orders.
func (r *wp2pRun) importCustomers(ctx context.Context) error {
	if err := r.loadUsers(ctx); err != nil {
		return err
	}
	return wp2pEachLine(filepath.Join(r.dir, "woo", "customers.jsonl"), func(raw []byte) error {
		var c wp2pCustomer
		if json.Unmarshal(raw, &c) != nil {
			r.rep.skip("customer", "", "unreadable line")
			return nil //nolint:nilerr // a bad record is reported and skipped, not fatal
		}
		r.prog.Items["customers"]++
		if c.UserID == 0 {
			r.rep.Counts["guest_customers"]++
			return nil
		}
		id, ok := r.users[c.UserID]
		if !ok {
			r.rep.skip("customer", c.Email, "the customer's user account was not imported")
			return nil
		}
		meta := map[string]any{}
		for k, v := range c.Billing {
			if v != "" {
				meta["billing_"+k] = v
			}
		}
		for k, v := range c.Shipping {
			if v != "" {
				meta["shipping_"+k] = v
			}
		}
		if c.Paying {
			meta["paying_customer"] = "1"
		}
		if t := c.LastActive.ptr(); t != nil {
			meta["_goo_last_active"] = float64(t.Unix())
		}
		if len(meta) > 0 {
			if err := r.s.gooSetUserMeta(ctx, id, meta); err != nil {
				return fmt.Errorf("customer %d: %w", c.UserID, err)
			}
		}
		r.rep.Counts["customers"]++
		return nil
	})
}
