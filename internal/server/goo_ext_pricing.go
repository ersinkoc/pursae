package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Goocommerce Dynamic Pricing (WooCommerce Dynamic Pricing): rule sets that change cart prices —
// product and category bulk pricing (quantity tiers), special offers (buy X get Y), cart total
// adjustments (a negative fee) and role pricing (also shown in the catalog). Rules live in
// goo_pricing_rules and are managed on Goocommerce › Pricing rules (page=pricing-rules).

const gooDPKey = "dynamic_pricing"

// Rule types.
const (
	gooDPProductBulk  = "product_bulk"
	gooDPCategoryBulk = "category_bulk"
	gooDPSpecialOffer = "special_offer"
	gooDPCartTotal    = "cart_total"
	gooDPRole         = "role"
)

var gooDPTypes = []gooOpt{
	{gooDPProductBulk, "Product bulk pricing"},
	{gooDPCategoryBulk, "Category bulk pricing"},
	{gooDPSpecialOffer, "Special offer (buy X get Y)"},
	{gooDPCartTotal, "Cart total adjustment"},
	{gooDPRole, "Role pricing"},
}

// Adjustment types of bulk tiers and role pricing.
var gooDPAdjustments = []gooOpt{
	{"percentage_discount", "Percentage discount"},
	{"price_discount", "Price discount"},
	{"fixed_price", "Fixed price"},
}

// gooDPConditions are a rule set's conditions.
type gooDPConditions struct {
	Who        string   `json:"who"` // everyone, roles, logged_in
	Roles      []string `json:"roles,omitempty"`
	AppliesTo  string   `json:"applies_to"` // all, products, categories
	Products   []int64  `json:"products,omitempty"`
	Categories []int64  `json:"categories,omitempty"`
	// Collector is what quantities are counted for bulk tiers: "product" (the same product in the
	// cart, all its variations) or "category" (the items of CollectorCats, default Categories).
	Collector     string  `json:"collector"`
	CollectorCats []int64 `json:"collector_categories,omitempty"`
}

// gooDPTier is a quantity tier of bulk pricing or a subtotal tier of a cart total adjustment.
type gooDPTier struct {
	Min    float64 `json:"min"`
	Max    float64 `json:"max"` // 0 = no maximum
	Type   string  `json:"type"`
	Amount float64 `json:"amount"`
}

// gooDPRules are a rule set's adjustments (which fields apply depends on the type).
type gooDPRules struct {
	Tiers     []gooDPTier `json:"tiers,omitempty"`
	Buy       int         `json:"buy,omitempty"`
	Receive   int         `json:"receive,omitempty"`
	Discount  float64     `json:"discount,omitempty"` // special offer: % off the received items
	Repeating bool        `json:"repeating,omitempty"`
	Type      string      `json:"type,omitempty"` // role pricing adjustment
	Amount    float64     `json:"amount,omitempty"`
}

// gooDPRule is a pricing rule set.
type gooDPRule struct {
	ID         int64
	Name       string
	Type       string
	Active     bool
	Priority   int
	DateFrom   *time.Time
	DateTo     *time.Time
	Conditions gooDPConditions
	Rules      gooDPRules
}

// TypeLabel is the rule type's name.
func (r *gooDPRule) TypeLabel() string {
	for _, t := range gooDPTypes {
		if t.Value == r.Type {
			return t.Label
		}
	}
	return r.Type
}

// gooDPVersion changes whenever rules are saved, so per-request caches notice.
var gooDPVersion atomic.Int64

func init() {
	gooRegisterExtension(&gooExtension{
		Key:         gooDPKey,
		Name:        "Dynamic Pricing",
		Description: "Bulk pricing by quantity, buy X get Y offers, cart total discounts and role-based prices. Rules are managed on Goocommerce › Pricing rules.",
		Icon:        "dashicons-tag",
		Category:    "Merchandising",
		Fields: []gooField{
			{ID: "general", Title: "Dynamic Pricing options", Type: "title"},
			{ID: "stack_rules", Title: "Stack rules", Type: "checkbox", Default: false, Desc: "Let several rule sets adjust the same cart item (in priority order). When off, the first matching rule set wins."},
			{ID: "apply_to_sale", Title: "Sale items", Type: "checkbox", Default: true, Desc: "Apply pricing rules to products that are on sale"},
			{ID: "show_table", Title: "Pricing table", Type: "checkbox", Default: true, Desc: "Show the bulk pricing table on the single product page"},
			{ID: "table_title", Title: "Pricing table title", Type: "text", Default: "Bulk pricing"},
			{ID: "general_end", Type: "sectionend"},
		},
		Pages: []gooExtMenuItem{{Page: "pricing-rules", Title: "Pricing rules"}},
	})
	gooExtAdminPages["pricing-rules"] = func(s *Server, c *gin.Context) { s.gooDPAdmin(c) }
	gooCartItemPriceHooks = append(gooCartItemPriceHooks, gooDPCartItemPrice)
	gooCartFeesHooks = append(gooCartFeesHooks, gooDPCartFees)
	gooProductPriceHooks = append(gooProductPriceHooks, gooDPProductPrice)
	gooPriceHTMLHooks = append(gooPriceHTMLHooks, gooDPPriceHTML)
	gooOnHTML("single_summary_end", gooDPPricingTable)
}

// Storage ---------------------------------------------------------------------------------------------

const gooDPCols = `id, name, type, active, priority, date_from, date_to, conditions, rules`

func gooDPScan(row interface{ Scan(...any) error }) (*gooDPRule, error) {
	r := &gooDPRule{}
	var cond, rules []byte
	if err := row.Scan(&r.ID, &r.Name, &r.Type, &r.Active, &r.Priority, &r.DateFrom, &r.DateTo, &cond, &rules); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(cond, &r.Conditions)
	_ = json.Unmarshal(rules, &r.Rules)
	return r, nil
}

// gooDPRuleList lists the rule sets in priority order (activeOnly: switched on and inside their dates).
func (s *Server) gooDPRuleList(ctx context.Context, activeOnly bool) ([]*gooDPRule, error) {
	q := `SELECT ` + gooDPCols + ` FROM goo_pricing_rules`
	if activeOnly {
		q += ` WHERE active AND (date_from IS NULL OR date_from <= now()) AND (date_to IS NULL OR date_to >= now())`
	}
	rows, err := s.store.Pool.Query(ctx, q+` ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*gooDPRule
	for rows.Next() {
		r, err := gooDPScan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// gooDPGetRule loads one rule set.
func (s *Server) gooDPGetRule(ctx context.Context, id int64) (*gooDPRule, error) {
	r, err := gooDPScan(s.store.Pool.QueryRow(ctx, `SELECT `+gooDPCols+` FROM goo_pricing_rules WHERE id = $1`, id))
	if err != nil {
		return nil, store.ErrNotFound
	}
	return r, nil
}

// gooDPSaveRule inserts (ID 0) or updates a rule set.
func (s *Server) gooDPSaveRule(ctx context.Context, r *gooDPRule) error {
	cond, _ := json.Marshal(r.Conditions)
	rules, _ := json.Marshal(r.Rules)
	defer gooDPVersion.Add(1)
	if r.ID == 0 {
		return s.store.Pool.QueryRow(ctx, `INSERT INTO goo_pricing_rules (name, type, active, priority, date_from, date_to, conditions, rules)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, r.Name, r.Type, r.Active, r.Priority, r.DateFrom, r.DateTo, cond, rules).Scan(&r.ID)
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_pricing_rules SET name=$2, type=$3, active=$4, priority=$5, date_from=$6, date_to=$7,
		conditions=$8, rules=$9, updated_at=now() WHERE id=$1`, r.ID, r.Name, r.Type, r.Active, r.Priority, r.DateFrom, r.DateTo, cond, rules)
	return err
}

func (s *Server) gooDPDeleteRule(ctx context.Context, id int64) error {
	defer gooDPVersion.Add(1)
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_pricing_rules WHERE id = $1`, id)
	return err
}

// gooDPActiveRules are the active rule sets, cached on the request (or loaded for a plain context).
func (s *Server) gooDPActiveRules(ctx context.Context) []*gooDPRule {
	type cached struct {
		ver   int64
		rules []*gooDPRule
	}
	gc, _ := ctx.(*gin.Context)
	ver := gooDPVersion.Load()
	if gc != nil {
		if v, ok := gc.Get("goo.dp.rules"); ok {
			if cv := v.(cached); cv.ver == ver {
				return cv.rules
			}
		}
	}
	rules, _ := s.gooDPRuleList(ctx, true)
	if gc != nil {
		gc.Set("goo.dp.rules", cached{ver, rules})
	}
	return rules
}

// Matching ----------------------------------------------------------------------------------------------

// gooDPUserMatches checks the rule's "who" condition for a visitor.
func gooDPUserMatches(r *gooDPRule, u *store.User) bool {
	switch r.Conditions.Who {
	case "logged_in":
		return u != nil
	case "roles":
		if u == nil {
			return false
		}
		for _, role := range u.Roles {
			if containsStr(r.Conditions.Roles, role) {
				return true
			}
		}
		return false
	}
	return true
}

// gooDPProductMatches checks the rule's product / category targets for a product (and its categories).
func gooDPProductMatches(r *gooDPRule, productID, variationID int64, cats []int64) bool {
	c := r.Conditions
	applies := c.AppliesTo
	if r.Type == gooDPCategoryBulk && applies == "" {
		applies = "categories"
	}
	if r.Type == gooDPProductBulk && applies == "" {
		applies = "products"
	}
	switch applies {
	case "products":
		return gooInt64In(productID, c.Products) || (variationID != 0 && gooInt64In(variationID, c.Products))
	case "categories":
		for _, id := range cats {
			if gooInt64In(id, c.Categories) {
				return true
			}
		}
		return false
	}
	return true
}

// gooDPAdjust applies an adjustment to a unit price (never below zero).
func gooDPAdjust(price float64, typ string, amount float64) float64 {
	var v float64
	switch typ {
	case "price_discount":
		v = price - amount
	case "fixed_price":
		v = amount
	default: // percentage_discount
		v = price * (1 - amount/100)
	}
	return math.Max(0, v)
}

// gooDPTierFor finds the tier a quantity falls in.
func gooDPTierFor(tiers []gooDPTier, qty float64) *gooDPTier {
	for i := range tiers {
		t := &tiers[i]
		if qty >= t.Min && (t.Max <= 0 || qty <= t.Max) {
			return t
		}
	}
	return nil
}

// gooDPCartUser is the visitor of a cart (nil for a guest or outside a request).
func gooDPCartUser(cart *gooCart) *store.User {
	if cart == nil || cart.c == nil {
		return nil
	}
	return currentUser(cart.c)
}

// gooDPApplied records which items were discounted in this calculation (key → original unit price).
type gooDPApplied struct {
	calc     *gooCart
	original map[string]float64
	rules    map[string][]string
}

func gooDPState(cart *gooCart) *gooDPApplied {
	if v, ok := cart.c.Get("goo.dp.applied"); ok {
		if st := v.(*gooDPApplied); st.calc == cart {
			return st
		}
	}
	st := &gooDPApplied{calc: cart, original: map[string]float64{}, rules: map[string][]string{}}
	cart.c.Set("goo.dp.applied", st)
	return st
}

// gooDPCartItemPrice is the cart price hook: the rule sets of the item, in priority order.
func gooDPCartItemPrice(s *Server, ctx context.Context, cart *gooCart, it *gooCartItem) {
	if !s.gooExtOn(gooDPKey) || it.product == nil || cart.c == nil {
		return
	}
	st := gooDPState(cart)
	if it == cart.Items[0] {
		// A new pass of Calculate: forget the previous pass.
		st.original, st.rules = map[string]float64{}, map[string][]string{}
	}
	if !s.gooExtBool(gooDPKey, "apply_to_sale") && it.product.IsOnSale() {
		return
	}
	stack := s.gooExtBool(gooDPKey, "stack_rules")
	u := gooDPCartUser(cart)
	price := it.Price
	applied := false
	for _, r := range s.gooDPActiveRules(ctx) {
		if r.Type == gooDPCartTotal || !gooDPUserMatches(r, u) || !gooDPProductMatches(r, it.ProductID, it.VariationID, it.categories) {
			continue
		}
		if applied && !stack {
			break
		}
		np, ok := gooDPRulePrice(r, cart, it, price)
		if !ok {
			continue
		}
		if !applied {
			st.original[it.Key] = it.Price
		}
		price, applied = np, true
		st.rules[it.Key] = append(st.rules[it.Key], r.Name)
	}
	if applied {
		it.Price = gooRound(price, s.gooDecimals()+2)
	}
}

// gooDPRulePrice is a rule's unit price for an item (false when the rule does not change it).
func gooDPRulePrice(r *gooDPRule, cart *gooCart, it *gooCartItem, price float64) (float64, bool) {
	switch r.Type {
	case gooDPProductBulk, gooDPCategoryBulk:
		qty := gooDPCollect(r, cart, it)
		t := gooDPTierFor(r.Rules.Tiers, qty)
		if t == nil {
			return price, false
		}
		return gooDPAdjust(price, t.Type, t.Amount), true
	case gooDPSpecialOffer:
		buy, recv := r.Rules.Buy, r.Rules.Receive
		if buy <= 0 || recv <= 0 || it.Quantity < buy+1 {
			return price, false
		}
		groups := it.Quantity / (buy + recv)
		rest := it.Quantity % (buy + recv)
		free := groups * recv
		if rest > buy { // a partial group still gets its received items
			free += rest - buy
			groups++
		}
		if !r.Rules.Repeating && groups > 1 {
			free = min(recv, it.Quantity-buy)
		}
		if free <= 0 {
			return price, false
		}
		disc := r.Rules.Discount
		if disc <= 0 {
			disc = 100
		}
		off := price * math.Min(disc, 100) / 100 * float64(free)
		return math.Max(0, price-off/float64(it.Quantity)), true
	case gooDPRole:
		return gooDPAdjust(price, r.Rules.Type, r.Rules.Amount), true
	}
	return price, false
}

// gooDPCollect counts the quantity a bulk rule looks at for an item.
func gooDPCollect(r *gooDPRule, cart *gooCart, it *gooCartItem) float64 {
	qty := 0
	if r.Conditions.Collector == "category" {
		cats := r.Conditions.CollectorCats
		if len(cats) == 0 {
			cats = r.Conditions.Categories
		}
		for _, o := range cart.Items {
			for _, id := range o.categories {
				if gooInt64In(id, cats) {
					qty += o.Quantity
					break
				}
			}
		}
		if qty > 0 {
			return float64(qty)
		}
	}
	for _, o := range cart.Items {
		if o.ProductID == it.ProductID {
			qty += o.Quantity
		}
	}
	return float64(qty)
}

// gooDPCartFees adds the cart total adjustments: the best matching tier of each rule set, as a
// negative fee (stacked only when "Stack rules" is on).
func gooDPCartFees(s *Server, ctx context.Context, cart *gooCart) {
	if !s.gooExtOn(gooDPKey) || cart.IsEmpty() {
		return
	}
	stack := s.gooExtBool(gooDPKey, "stack_rules")
	u := gooDPCartUser(cart)
	subtotal := cart.displayedSubtotal()
	for _, r := range s.gooDPActiveRules(ctx) {
		if r.Type != gooDPCartTotal || !gooDPUserMatches(r, u) {
			continue
		}
		var best *gooDPTier
		for i := range r.Rules.Tiers {
			t := &r.Rules.Tiers[i]
			if subtotal >= t.Min && (t.Max <= 0 || subtotal <= t.Max) && (best == nil || t.Min > best.Min) {
				best = t
			}
		}
		if best == nil || best.Amount <= 0 {
			continue
		}
		amt := best.Amount
		if best.Type != "fixed" && best.Type != "price_discount" && best.Type != "fixed_price" {
			amt = cart.CartContentsTotal * best.Amount / 100
		}
		amt = math.Min(amt, cart.CartContentsTotal)
		if amt <= 0 {
			continue
		}
		name := defaultStr(r.Name, "Discount")
		f := cart.AddFee("goo_dp_"+itoa64(r.ID), name, -gooRound(amt, s.gooDecimals()), false, "")
		f.Meta = map[string]any{"_pricing_rule": float64(r.ID)}
		if !stack {
			return
		}
	}
}

// gooDPOriginalPrice is an item's unit price before the pricing rules (ok false when no rule applied):
// the storefront shows it struck through in the cart.
func (s *Server) gooDPOriginalPrice(c *gin.Context, cart *gooCart, it *gooCartItem) (float64, bool) {
	if c == nil {
		return 0, false
	}
	v, ok := c.Get("goo.dp.applied")
	if !ok {
		return 0, false
	}
	st := v.(*gooDPApplied)
	if st.calc != cart {
		return 0, false
	}
	p, ok := st.original[it.Key]
	return p, ok
}

// Catalog -----------------------------------------------------------------------------------------------

// gooDPRolePrice is the role price of a product for the request's visitor (ok false when none applies).
func (s *Server) gooDPRolePrice(ctx context.Context, p *gooProduct, price float64) (float64, bool) {
	if !s.gooExtOn(gooDPKey) || p == nil {
		return price, false
	}
	gc, _ := ctx.(*gin.Context)
	if gc == nil {
		return price, false
	}
	u := currentUser(gc)
	if u == nil {
		return price, false
	}
	if !s.gooExtBool(gooDPKey, "apply_to_sale") && p.IsOnSale() {
		return price, false
	}
	pid, vid := p.ID, int64(0)
	if p.Type == gooTypeVariation {
		pid, vid = p.ParentID, p.ID
	}
	var cats []int64
	if terms, err := s.store.TermsForPosts(ctx, []int64{pid}, "product_cat"); err == nil {
		for _, t := range terms[pid] {
			cats = append(cats, t.ID)
			if t.ParentID != 0 {
				cats = append(cats, t.ParentID)
			}
		}
	}
	stack := s.gooExtBool(gooDPKey, "stack_rules")
	applied := false
	for _, r := range s.gooDPActiveRules(ctx) {
		if r.Type != gooDPRole || !gooDPUserMatches(r, u) || !gooDPProductMatches(r, pid, vid, cats) {
			continue
		}
		price = gooDPAdjust(price, r.Rules.Type, r.Rules.Amount)
		applied = true
		if !stack {
			break
		}
	}
	return price, applied
}

// gooDPProductPrice is the catalog price hook (role pricing).
func gooDPProductPrice(s *Server, ctx context.Context, p *gooProduct, price float64) float64 {
	v, _ := s.gooDPRolePrice(ctx, p, price)
	return v
}

// gooDPPriceHTML strikes the regular price through when a role price applies.
func gooDPPriceHTML(s *Server, ctx context.Context, p *gooProduct, h template.HTML) template.HTML {
	if p == nil || p.Type == gooTypeVariable || p.Type == gooTypeGrouped || !p.HasPrice() {
		return h
	}
	orig := p.PriceValue()
	v, ok := s.gooDPRolePrice(ctx, p, orig)
	if !ok || v == orig {
		return h
	}
	return template.HTML(`<del aria-hidden="true">`) + s.gooPrice(orig) + `</del> <span class="screen-reader-text">Original price was: ` +
		template.HTML(gooEsc(s.gooPricePlain(orig, s.gooCurrency()))) + `.</span><ins aria-hidden="true">` + s.gooPrice(v) +
		`</ins><span class="screen-reader-text">Current price is: ` + template.HTML(gooEsc(s.gooPricePlain(v, s.gooCurrency()))) + `.</span>`
}

// gooDPPricingTable is the bulk pricing table of a product (single_summary_end).
func gooDPPricingTable(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	if !s.gooExtOn(gooDPKey) || p == nil || !s.gooExtBool(gooDPKey, "show_table") {
		return ""
	}
	ctx := context.Context(c)
	var cats []int64
	if terms, err := s.store.TermsForPosts(ctx, []int64{p.ID}, "product_cat"); err == nil {
		for _, t := range terms[p.ID] {
			cats = append(cats, t.ID)
			if t.ParentID != 0 {
				cats = append(cats, t.ParentID)
			}
		}
	}
	u := currentUser(c)
	base := p.PriceValue()
	if !s.gooExtBool(gooDPKey, "apply_to_sale") && p.IsOnSale() {
		return ""
	}
	var b strings.Builder
	for _, r := range s.gooDPActiveRules(ctx) {
		if (r.Type != gooDPProductBulk && r.Type != gooDPCategoryBulk) || len(r.Rules.Tiers) == 0 || !gooDPUserMatches(r, u) || !gooDPProductMatches(r, p.ID, 0, cats) {
			continue
		}
		title := defaultStr(s.gooExtString(gooDPKey, "table_title"), "Bulk pricing")
		b.WriteString(`<div class="goo-dynamic-pricing-table-wrapper"><h3 class="goo-dynamic-pricing-table-title">` + gooEsc(title) + `</h3>`)
		b.WriteString(`<table class="goo-dynamic-pricing-table shop_table"><thead><tr><th>Quantity</th><th>Price per unit</th></tr></thead><tbody>`)
		for _, t := range r.Rules.Tiers {
			qty := strconv.FormatFloat(t.Min, 'f', -1, 64)
			if t.Max > 0 {
				qty += " - " + strconv.FormatFloat(t.Max, 'f', -1, 64)
			} else {
				qty += "+"
			}
			cell := string(s.gooPrice(gooRound(gooDPAdjust(base, t.Type, t.Amount), s.gooDecimals())))
			if p.Type == gooTypeVariable {
				switch t.Type {
				case "fixed_price":
					cell = string(s.gooPrice(t.Amount))
				case "price_discount":
					cell = string(s.gooPrice(t.Amount)) + " off"
				default:
					cell = strconv.FormatFloat(t.Amount, 'f', -1, 64) + "% off"
				}
			}
			b.WriteString(`<tr><td class="quantity">` + gooEsc(qty) + `</td><td class="price">` + cell + `</td></tr>`)
		}
		b.WriteString(`</tbody></table></div>`)
		break // the first matching rule set's table, like WooCommerce Dynamic Pricing
	}
	return template.HTML(b.String())
}

// Admin ---------------------------------------------------------------------------------------------------

// gooDPAdmin is Goocommerce › Pricing rules: the list, the editor, delete and activate / deactivate.
func (s *Server) gooDPAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	back := "/wp-admin/goocommerce?page=pricing-rules"
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-pricing-rules") {
			s.denyNonce(c)
			return
		}
		id, _ := strconv.ParseInt(c.PostForm("id"), 10, 64)
		switch c.PostForm("goo_action") {
		case "delete":
			_ = s.gooDPDeleteRule(ctx, id)
			c.Redirect(http.StatusFound, back+"&goo-msg="+url.QueryEscape("Rule set deleted."))
			return
		case "toggle":
			if r, err := s.gooDPGetRule(ctx, id); err == nil {
				r.Active = !r.Active
				_ = s.gooDPSaveRule(ctx, r)
			}
			c.Redirect(http.StatusFound, back+"&goo-msg="+url.QueryEscape("Rule set updated."))
			return
		}
		r := &gooDPRule{ID: id}
		if id != 0 {
			if have, err := s.gooDPGetRule(ctx, id); err == nil {
				r = have
			}
		}
		gooDPReadForm(c, r)
		if r.Name == "" {
			r.Name = "Pricing rule"
		}
		if err := s.gooDPSaveRule(ctx, r); err != nil {
			c.Redirect(http.StatusFound, back+"&action=edit&id="+itoa64(id)+"&goo-error="+url.QueryEscape(err.Error()))
			return
		}
		s.purgeAll(ctx)
		c.Redirect(http.StatusFound, back+"&action=edit&id="+itoa64(r.ID)+"&goo-msg="+url.QueryEscape("Rule set saved."))
		return
	}
	nonce := s.nonce(c, "goo-pricing-rules")
	if c.Query("action") == "edit" || c.Query("action") == "new" {
		id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
		r := &gooDPRule{Type: defaultStr(c.Query("type"), gooDPProductBulk), Active: true, Priority: 10,
			Conditions: gooDPConditions{Who: "everyone", AppliesTo: "products", Collector: "product"}}
		if id != 0 {
			have, err := s.gooDPGetRule(ctx, id)
			if err != nil {
				c.String(http.StatusNotFound, "Rule set not found.")
				return
			}
			r = have
		}
		// Five tier rows: the saved ones and empty ones to add.
		tiers := append([]gooDPTier{}, r.Rules.Tiers...)
		for len(tiers) < 5 {
			tiers = append(tiers, gooDPTier{Type: "percentage_discount"})
		}
		cats, _, _ := s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: "product_cat", OrderBy: "name", PerPage: 500})
		var catOpts []gooDPOption
		for _, t := range cats {
			catOpts = append(catOpts, gooDPOption{Value: itoa64(t.ID), Label: t.Name,
				Selected: gooInt64In(t.ID, r.Conditions.Categories), Collector: gooInt64In(t.ID, r.Conditions.CollectorCats)})
		}
		var roleOpts []gooDPOption
		for _, ro := range s.asRoleNames(ctx) {
			roleOpts = append(roleOpts, gooDPOption{Value: ro.Slug, Label: ro.Name, Selected: containsStr(r.Conditions.Roles, ro.Slug)})
		}
		var prods []string
		for _, id := range r.Conditions.Products {
			prods = append(prods, itoa64(id))
		}
		date := func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02")
		}
		title := "Add rule set"
		if r.ID != 0 {
			title = "Edit rule set"
		}
		s.adminPage(c, "goocommerce?page=pricing-rules", title, "goocommerce_page_goo-pricing-rules", "admin/goo-ext-pricing",
			map[string]any{"Edit": true, "Rule": r, "Tiers": tiers, "Types": gooDPTypes, "Adjustments": gooDPAdjustments,
				"Categories": catOpts, "Roles": roleOpts, "Products": strings.Join(prods, ", "), "DateFrom": date(r.DateFrom), "DateTo": date(r.DateTo),
				"Nonce": nonce, "Title": title}, gooFlash(c)...)
		return
	}
	rules, err := s.gooDPRuleList(ctx, false)
	var notices []notice
	if err != nil {
		notices = append(notices, notice{Type: "error", Message: template.HTML(gooEsc(err.Error()))})
	}
	type row struct {
		*gooDPRule
		Summary string
		Dates   string
	}
	var rows []row
	for _, r := range rules {
		rows = append(rows, row{r, s.gooDPSummary(r), gooDPDates(r)})
	}
	s.adminPage(c, "goocommerce?page=pricing-rules", "Pricing rules", "goocommerce_page_goo-pricing-rules", "admin/goo-ext-pricing",
		map[string]any{"Edit": false, "Rows": rows, "Types": gooDPTypes, "Nonce": nonce, "On": s.gooExtOn(gooDPKey)}, append(gooFlash(c), notices...)...)
}

type gooDPOption struct {
	Value, Label        string
	Selected, Collector bool
}

func gooDPDates(r *gooDPRule) string {
	switch {
	case r.DateFrom != nil && r.DateTo != nil:
		return r.DateFrom.Format("2006-01-02") + " – " + r.DateTo.Format("2006-01-02")
	case r.DateFrom != nil:
		return "From " + r.DateFrom.Format("2006-01-02")
	case r.DateTo != nil:
		return "Until " + r.DateTo.Format("2006-01-02")
	}
	return "Always"
}

// gooDPSummary describes a rule set's adjustments in one line.
func (s *Server) gooDPSummary(r *gooDPRule) string {
	adj := func(typ string, amt float64) string {
		switch typ {
		case "fixed_price":
			return s.gooPricePlain(amt, s.gooCurrency()) + " each"
		case "price_discount":
			return s.gooPricePlain(amt, s.gooCurrency()) + " off"
		case "fixed":
			return s.gooPricePlain(amt, s.gooCurrency()) + " off"
		}
		if r.Type == gooDPCartTotal && typ == "fixed_price" {
			return s.gooPricePlain(amt, s.gooCurrency()) + " off"
		}
		return strconv.FormatFloat(amt, 'f', -1, 64) + "% off"
	}
	var parts []string
	switch r.Type {
	case gooDPProductBulk, gooDPCategoryBulk:
		for _, t := range r.Rules.Tiers {
			q := strconv.FormatFloat(t.Min, 'f', -1, 64)
			if t.Max > 0 {
				q += "–" + strconv.FormatFloat(t.Max, 'f', -1, 64)
			} else {
				q += "+"
			}
			parts = append(parts, q+": "+adj(t.Type, t.Amount))
		}
	case gooDPCartTotal:
		for _, t := range r.Rules.Tiers {
			parts = append(parts, "Subtotal ≥ "+s.gooPricePlain(t.Min, s.gooCurrency())+": "+adj(t.Type, t.Amount))
		}
	case gooDPSpecialOffer:
		rep := ""
		if r.Rules.Repeating {
			rep = " (repeating)"
		}
		parts = append(parts, fmt.Sprintf("Buy %d, get %d at %s%% off%s", r.Rules.Buy, r.Rules.Receive, strconv.FormatFloat(r.Rules.Discount, 'f', -1, 64), rep))
	case gooDPRole:
		parts = append(parts, strings.Join(r.Conditions.Roles, ", ")+": "+adj(r.Rules.Type, r.Rules.Amount))
	}
	return strings.Join(parts, "; ")
}

// gooDPReadForm reads the rule editor.
func gooDPReadForm(c *gin.Context, r *gooDPRule) {
	num := func(k string) float64 {
		f, _ := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(c.PostForm(k), ",", ".")), 64)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	}
	ids := func(vals []string) []int64 {
		var out []int64
		for _, v := range vals {
			for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
				if id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil && id > 0 {
					out = append(out, id)
				}
			}
		}
		return out
	}
	date := func(k string, end bool) *time.Time {
		v := strings.TrimSpace(c.PostForm(k))
		if v == "" {
			return nil
		}
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			return nil
		}
		if end {
			t = t.Add(24*time.Hour - time.Second)
		}
		return &t
	}
	r.Name = strings.TrimSpace(c.PostForm("name"))
	r.Type = c.PostForm("type")
	valid := false
	for _, t := range gooDPTypes {
		if t.Value == r.Type {
			valid = true
		}
	}
	if !valid {
		r.Type = gooDPProductBulk
	}
	r.Active = c.PostForm("active") != ""
	r.Priority = int(num("priority"))
	r.DateFrom, r.DateTo = date("date_from", false), date("date_to", true)
	r.Conditions = gooDPConditions{
		Who: c.PostForm("who"), Roles: c.PostFormArray("roles[]"), AppliesTo: c.PostForm("applies_to"),
		Products: ids([]string{c.PostForm("products")}), Categories: ids(c.PostFormArray("categories[]")),
		Collector: c.PostForm("collector"), CollectorCats: ids(c.PostFormArray("collector_categories[]")),
	}
	r.Rules = gooDPRules{Type: c.PostForm("role_type"), Amount: num("role_amount"),
		Buy: int(num("buy")), Receive: int(num("receive")), Discount: num("discount"), Repeating: c.PostForm("repeating") != ""}
	mins, maxs, types, amounts := c.PostFormArray("tier_min[]"), c.PostFormArray("tier_max[]"), c.PostFormArray("tier_type[]"), c.PostFormArray("tier_amount[]")
	for i := range mins {
		if strings.TrimSpace(mins[i]) == "" || i >= len(amounts) || strings.TrimSpace(amounts[i]) == "" {
			continue
		}
		t := gooDPTier{}
		t.Min, _ = strconv.ParseFloat(strings.TrimSpace(mins[i]), 64)
		if i < len(maxs) {
			t.Max, _ = strconv.ParseFloat(strings.TrimSpace(maxs[i]), 64)
		}
		if i < len(types) {
			t.Type = types[i]
		}
		t.Amount, _ = strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(amounts[i], ",", ".")), 64)
		r.Rules.Tiers = append(r.Rules.Tiers, t)
	}
	sort.SliceStable(r.Rules.Tiers, func(i, j int) bool { return r.Rules.Tiers[i].Min < r.Rules.Tiers[j].Min })
}
