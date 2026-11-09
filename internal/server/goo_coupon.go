package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/store"
)

// Coupons (WC_Coupon and WC_Discounts): percentage, fixed cart and fixed product discounts with
// WooCommerce's usage restrictions and limits.

type gooCoupon struct {
	ID                        int64
	Code                      string
	Description               string
	Status                    string
	DiscountType              string // percent, fixed_cart, fixed_product
	Amount                    float64
	DateExpires               *time.Time
	FreeShipping              bool
	MinimumAmount             *float64
	MaximumAmount             *float64
	IndividualUse             bool
	ExcludeSaleItems          bool
	ProductIDs                []int64
	ExcludedProductIDs        []int64
	ProductCategories         []int64
	ExcludedProductCategories []int64
	EmailRestrictions         []string
	UsageLimit                *int
	UsageLimitPerUser         *int
	LimitUsageToXItems        *int
	UsageCount                int
	UsedBy                    []string
	Meta                      map[string]any
	DateCreated, DateModified time.Time
}

var gooCouponTypes = []gooOpt{{"percent", "Percentage discount"}, {"fixed_cart", "Fixed cart discount"}, {"fixed_product", "Fixed product discount"}}

const gooCouponCols = `id, code, description, status, discount_type, amount::float8, date_expires, free_shipping, minimum_amount::float8,
	maximum_amount::float8, individual_use, exclude_sale_items, product_ids, excluded_product_ids, product_categories,
	excluded_product_categories, email_restrictions, usage_limit, usage_limit_per_user, limit_usage_to_x_items, usage_count, used_by,
	meta, date_created, date_modified`

func gooScanCoupon(row pgx.Row) (*gooCoupon, error) {
	c := &gooCoupon{}
	err := row.Scan(&c.ID, &c.Code, &c.Description, &c.Status, &c.DiscountType, &c.Amount, &c.DateExpires, &c.FreeShipping, &c.MinimumAmount,
		&c.MaximumAmount, &c.IndividualUse, &c.ExcludeSaleItems, &c.ProductIDs, &c.ExcludedProductIDs, &c.ProductCategories,
		&c.ExcludedProductCategories, &c.EmailRestrictions, &c.UsageLimit, &c.UsageLimitPerUser, &c.LimitUsageToXItems, &c.UsageCount, &c.UsedBy,
		&c.Meta, &c.DateCreated, &c.DateModified)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if c.Meta == nil {
		c.Meta = map[string]any{}
	}
	return c, err
}

// gooCouponByCode is new WC_Coupon($code): published coupons only, case-insensitive.
func (s *Server) gooCouponByCode(ctx context.Context, code string) (*gooCoupon, error) {
	return gooScanCoupon(s.store.Pool.QueryRow(ctx, `SELECT `+gooCouponCols+` FROM goo_coupons WHERE lower(code) = lower($1) AND status = 'publish'`, strings.TrimSpace(code)))
}

func (s *Server) gooCouponByID(ctx context.Context, id int64) (*gooCoupon, error) {
	return gooScanCoupon(s.store.Pool.QueryRow(ctx, `SELECT `+gooCouponCols+` FROM goo_coupons WHERE id = $1`, id))
}

// gooSaveCoupon inserts (ID 0) or updates a coupon. Codes are unique (case-insensitive) among non-trashed coupons.
func (s *Server) gooSaveCoupon(ctx context.Context, c *gooCoupon) error {
	c.Code = strings.ToLower(strings.TrimSpace(c.Code))
	if c.Code == "" {
		return errors.New("Please enter a coupon code.")
	}
	if c.Status == "" {
		c.Status = "publish"
	}
	switch c.DiscountType {
	case "percent", "fixed_cart", "fixed_product":
	default:
		c.DiscountType = "fixed_cart"
	}
	if c.DiscountType == "percent" && c.Amount > 100 {
		c.Amount = 100
	}
	var taken bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_coupons WHERE lower(code) = $1 AND id <> $2 AND status <> 'trash')`, c.Code, c.ID).Scan(&taken)
	if taken {
		return fmt.Errorf("Coupon code already exists - customers will use the latest coupon with this code.")
	}
	ids := func(v []int64) []int64 { return nonNilSlice(v) }
	strs := func(v []string) []string { return nonNilSlice(v) }
	if c.Meta == nil {
		c.Meta = map[string]any{}
	}
	if c.ID == 0 {
		return s.store.Pool.QueryRow(ctx, `INSERT INTO goo_coupons (code, description, status, discount_type, amount, date_expires, free_shipping,
			minimum_amount, maximum_amount, individual_use, exclude_sale_items, product_ids, excluded_product_ids, product_categories,
			excluded_product_categories, email_restrictions, usage_limit, usage_limit_per_user, limit_usage_to_x_items, meta)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) RETURNING id, date_created, date_modified`,
			c.Code, c.Description, c.Status, c.DiscountType, c.Amount, c.DateExpires, c.FreeShipping, c.MinimumAmount, c.MaximumAmount,
			c.IndividualUse, c.ExcludeSaleItems, ids(c.ProductIDs), ids(c.ExcludedProductIDs), ids(c.ProductCategories), ids(c.ExcludedProductCategories),
			strs(c.EmailRestrictions), c.UsageLimit, c.UsageLimitPerUser, c.LimitUsageToXItems, c.Meta).Scan(&c.ID, &c.DateCreated, &c.DateModified)
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_coupons SET code=$2, description=$3, status=$4, discount_type=$5, amount=$6, date_expires=$7,
		free_shipping=$8, minimum_amount=$9, maximum_amount=$10, individual_use=$11, exclude_sale_items=$12, product_ids=$13,
		excluded_product_ids=$14, product_categories=$15, excluded_product_categories=$16, email_restrictions=$17, usage_limit=$18,
		usage_limit_per_user=$19, limit_usage_to_x_items=$20, meta=$21, date_modified=now() WHERE id=$1`,
		c.ID, c.Code, c.Description, c.Status, c.DiscountType, c.Amount, c.DateExpires, c.FreeShipping, c.MinimumAmount, c.MaximumAmount,
		c.IndividualUse, c.ExcludeSaleItems, ids(c.ProductIDs), ids(c.ExcludedProductIDs), ids(c.ProductCategories), ids(c.ExcludedProductCategories),
		strs(c.EmailRestrictions), c.UsageLimit, c.UsageLimitPerUser, c.LimitUsageToXItems, c.Meta)
	return err
}

// gooQueryCoupons lists coupons for the admin (status "" = all but trash).
func (s *Server) gooQueryCoupons(ctx context.Context, status, search string, page, perPage int) ([]*gooCoupon, int, error) {
	where := `status <> 'trash'`
	args := []any{}
	if status != "" {
		args = append(args, status)
		where = `status = $1`
	}
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		where += fmt.Sprintf(` AND (lower(code) LIKE $%d OR lower(description) LIKE $%d)`, len(args), len(args))
	}
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_coupons WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT `+gooCouponCols+` FROM goo_coupons WHERE `+where+fmt.Sprintf(` ORDER BY date_created DESC, id DESC LIMIT %d OFFSET %d`, perPage, (page-1)*perPage), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*gooCoupon
	for rows.Next() {
		c, err := gooScanCoupon(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// gooCouponError is a coupon validation failure with WooCommerce's message.
type gooCouponError struct{ Msg string }

func (e *gooCouponError) Error() string { return e.Msg }

func gooCouponErr(format string, a ...any) error { return &gooCouponError{fmt.Sprintf(format, a...)} }

// gooValidateCoupon is WC_Discounts::is_coupon_valid() for a cart.
func (s *Server) gooValidateCoupon(ctx context.Context, c *gooCoupon, cart *gooCart) error {
	if c.Status != "publish" {
		return gooCouponErr(`Coupon "%s" does not exist!`, c.Code)
	}
	if c.DateExpires != nil && time.Now().After(*c.DateExpires) {
		return gooCouponErr("This coupon has expired.")
	}
	if c.UsageLimit != nil && *c.UsageLimit > 0 && c.UsageCount >= *c.UsageLimit {
		return gooCouponErr("Coupon usage limit has been reached.")
	}
	if c.UsageLimitPerUser != nil && *c.UsageLimitPerUser > 0 {
		who := cart.customerKeys()
		used := 0
		for _, u := range c.UsedBy {
			for _, w := range who {
				if strings.EqualFold(u, w) {
					used++
					break
				}
			}
		}
		if used >= *c.UsageLimitPerUser {
			return gooCouponErr("Coupon usage limit has been reached.")
		}
	}
	subtotal := cart.displayedSubtotal()
	if c.MinimumAmount != nil && *c.MinimumAmount > 0 && subtotal < *c.MinimumAmount {
		return gooCouponErr("The minimum spend for this coupon is %s.", s.gooPricePlain(*c.MinimumAmount, s.gooCurrency()))
	}
	if c.MaximumAmount != nil && *c.MaximumAmount > 0 && subtotal > *c.MaximumAmount {
		return gooCouponErr("The maximum spend for this coupon is %s.", s.gooPricePlain(*c.MaximumAmount, s.gooCurrency()))
	}
	// Product and category restrictions.
	if len(c.ProductIDs) > 0 || len(c.ProductCategories) > 0 {
		if c.DiscountType == "fixed_cart" {
			// A fixed cart coupon with product restrictions needs at least one eligible product.
			found := false
			for _, it := range cart.Items {
				if s.gooCouponAppliesTo(ctx, c, it) {
					found = true
				}
			}
			if !found {
				return gooCouponErr("Sorry, this coupon is not applicable to selected products.")
			}
		}
	}
	if c.DiscountType == "fixed_cart" || c.DiscountType == "percent" {
		for _, it := range cart.Items {
			excluded := false
			for _, id := range s.gooCouponItemIDs(ctx, it.ProductID, it.VariationID) {
				excluded = excluded || gooInt64In(id, c.ExcludedProductIDs)
			}
			if excluded {
				if c.DiscountType == "fixed_cart" {
					return gooCouponErr("Sorry, this coupon is not applicable to the products: %s.", it.product.Name)
				}
			}
			if c.ExcludeSaleItems && it.product.IsOnSale() && c.DiscountType == "fixed_cart" {
				return gooCouponErr("Sorry, this coupon is not valid for sale items.")
			}
		}
	}
	any := false
	for _, it := range cart.Items {
		if s.gooCouponAppliesTo(ctx, c, it) {
			any = true
		}
	}
	if !any && len(cart.Items) > 0 && c.DiscountType != "fixed_cart" {
		if c.ExcludeSaleItems {
			return gooCouponErr("Sorry, this coupon is not valid for sale items.")
		}
		return gooCouponErr("Sorry, this coupon is not applicable to selected products.")
	}
	// E-mail restrictions are checked at checkout when the billing e-mail is known.
	if len(c.EmailRestrictions) > 0 {
		if email := cart.billingEmail(); email != "" && !gooEmailAllowed(email, c.EmailRestrictions) {
			return gooCouponErr(`Sorry, it seems the coupon "%s" is not yours - it has now been removed from your order.`, c.Code)
		}
	}
	return nil
}

// gooEmailAllowed matches an e-mail against restrictions, which may use * wildcards (*@example.com).
func gooEmailAllowed(email string, list []string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, r := range list {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == email {
			return true
		}
		if strings.Contains(r, "*") {
			pre, post, _ := strings.Cut(r, "*")
			if strings.HasPrefix(email, pre) && strings.HasSuffix(email, post) && len(email) >= len(pre)+len(post) {
				return true
			}
		}
	}
	return false
}

// gooCouponAppliesTo is WC_Coupon::is_valid_for_product() for a cart item.
func (s *Server) gooCouponAppliesTo(ctx context.Context, c *gooCoupon, it *gooCartItem) bool {
	p := it.product
	if p == nil {
		return false
	}
	productIDs := s.gooCouponItemIDs(ctx, it.ProductID, it.VariationID)
	cats := s.gooCouponItemCats(ctx, it.categories)
	valid := false
	if len(c.ProductIDs) == 0 && len(c.ProductCategories) == 0 {
		valid = true
	}
	for _, id := range productIDs {
		if gooInt64In(id, c.ProductIDs) {
			valid = true
		}
	}
	for _, cat := range cats {
		if gooInt64In(cat, c.ProductCategories) {
			valid = true
		}
	}
	for _, id := range productIDs {
		if gooInt64In(id, c.ExcludedProductIDs) {
			valid = false
		}
	}
	for _, cat := range cats {
		if gooInt64In(cat, c.ExcludedProductCategories) {
			valid = false
		}
	}
	if c.ExcludeSaleItems && p.IsOnSale() {
		valid = false
	}
	return valid
}

func gooInt64In(v int64, list []int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// gooApplyDiscounts is WC_Discounts::apply_coupon() for every applied coupon, in order: it sets each
// item's discount (in the entered price terms: tax inclusive when prices include tax) and the
// coupon totals. Amounts are in minor units internally to avoid float drift, like WooCommerce.
func (s *Server) gooApplyDiscounts(ctx context.Context, cart *gooCart) {
	dp := s.gooDecimals()
	unit := math.Pow(10, float64(dp))
	for _, it := range cart.Items {
		it.discount = 0
	}
	cart.CouponTotals = map[string]float64{}
	sequential := s.gooOptBool("calc_discounts_sequentially")
	for _, c := range cart.coupons {
		// Items sorted by price, highest first (WC sorts by price for fixed and percent limits).
		items := make([]*gooCartItem, 0, len(cart.Items))
		for _, it := range cart.Items {
			if s.gooCouponAppliesTo(ctx, c, it) {
				items = append(items, it)
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].Price > items[j].Price })
		remainingOf := func(it *gooCartItem) float64 {
			if sequential {
				return math.Round(it.Subtotal*unit) - it.discount
			}
			return math.Round(it.Subtotal * unit)
		}
		var total float64
		switch c.DiscountType {
		case "percent":
			limit := -1
			if c.LimitUsageToXItems != nil && *c.LimitUsageToXItems > 0 {
				limit = *c.LimitUsageToXItems
			}
			for _, it := range items {
				qty := it.Quantity
				if limit >= 0 {
					qty = min(qty, limit)
					limit -= qty
				}
				if qty <= 0 {
					continue
				}
				base := remainingOf(it) * float64(qty) / float64(it.Quantity)
				d := math.Floor(base * c.Amount / 100)
				avail := math.Round(it.Subtotal*unit) - it.discount
				d = math.Min(d, avail)
				it.discount += d
				total += d
			}
		case "fixed_product":
			limit := -1
			if c.LimitUsageToXItems != nil && *c.LimitUsageToXItems > 0 {
				limit = *c.LimitUsageToXItems
			}
			amt := math.Round(c.Amount * unit)
			for _, it := range items {
				qty := it.Quantity
				if limit >= 0 {
					qty = min(qty, limit)
					limit -= qty
				}
				avail := math.Round(it.Subtotal*unit) - it.discount
				d := math.Min(amt*float64(qty), avail)
				if d <= 0 {
					continue
				}
				it.discount += d
				total += d
			}
		case "fixed_cart":
			amt := math.Round(c.Amount * unit)
			var base float64
			for _, it := range items {
				base += math.Round(it.Subtotal*unit) - it.discount
			}
			if base <= 0 {
				break
			}
			amt = math.Min(amt, base)
			var applied float64
			for _, it := range items {
				avail := math.Round(it.Subtotal*unit) - it.discount
				d := math.Floor(amt * avail / base)
				d = math.Min(d, avail)
				it.discount += d
				applied += d
			}
			// Distribute the remaining cents one by one.
			for rem := amt - applied; rem >= 1; {
				moved := false
				for _, it := range items {
					if rem < 1 {
						break
					}
					if math.Round(it.Subtotal*unit)-it.discount >= 1 {
						it.discount++
						rem--
						applied++
						moved = true
					}
				}
				if !moved {
					break
				}
			}
			total = applied
		}
		cart.CouponTotals[c.Code] = total / unit
	}
	for _, it := range cart.Items {
		it.Discount = it.discount / unit
	}
}

// gooTryIncreaseCouponUsage records a use (an order placed with the coupon) in one conditional UPDATE.
// strict counts it only within the usage limit and the per-customer limit (keys are the customer's
// entries in used_by: user id, lower-case e-mail), so two orders racing for a coupon's last use cannot
// both get it; it then reports false. A coupon that no longer exists has nothing to count (true).
func (s *Server) gooTryIncreaseCouponUsage(ctx context.Context, code, usedBy string, keys []string, strict bool) (bool, error) {
	tag, err := s.store.Pool.Exec(ctx, `UPDATE goo_coupons SET usage_count = usage_count + 1, used_by = array_append(used_by, $2)
		WHERE lower(code) = lower($1) AND status <> 'trash' AND (NOT $3 OR (
			(usage_limit IS NULL OR usage_limit <= 0 OR usage_count < usage_limit) AND
			(usage_limit_per_user IS NULL OR usage_limit_per_user <= 0 OR
				(SELECT count(*) FROM unnest(used_by) u WHERE lower(u) = ANY($4::text[])) < usage_limit_per_user)))`,
		code, usedBy, strict, keys)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var exists bool
	err = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_coupons WHERE lower(code) = lower($1) AND status <> 'trash')`, code).Scan(&exists)
	return !exists, err
}

// gooDecreaseCouponUsage undoes a use (a cancelled order).
func (s *Server) gooDecreaseCouponUsage(ctx context.Context, code, usedBy string) error {
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_coupons SET usage_count = greatest(usage_count - 1, 0),
		used_by = coalesce(used_by[1:array_position(used_by, $2)-1] || used_by[array_position(used_by, $2)+1:], used_by)
		WHERE lower(code) = lower($1)`, code, usedBy)
	return err
}
