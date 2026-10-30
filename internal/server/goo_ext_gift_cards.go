package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Gift Cards (WooCommerce Gift Cards): a "gift-card" product type sold in fixed or custom amounts to a
// recipient; paid orders issue XXXX-XXXX-XXXX-XXXX codes that are e-mailed (now or on the delivery
// date). Codes are redeemed in the cart / checkout as negative fees capped at the order total
// (shipping and taxes included), debited when the order is placed and credited back when it is
// cancelled or refunded. Every balance change is logged in goo_gift_card_activity.

const (
	gcKey  = "gift_cards"
	gcType = "gift-card"
	// gcTaxClass is a tax class without rates: a gift card is a payment, it does not reduce taxes.
	gcTaxClass = "gift-card"
	gcFeePref  = "gift_card_"
)

func init() {
	gooRegisterExtension(&gooExtension{
		Key: gcKey, Name: "Gift Cards", Icon: "dashicons-tickets-alt", Category: "Payments & checkout",
		Description: "Sell prepaid digital gift cards that customers e-mail to friends and redeem in the cart or at checkout, with balances, expiry dates and an activity log.",
		Fields: []gooField{
			{Type: "title", Title: "Gift Cards"},
			{ID: "enable_cart", Title: "Cart page", Type: "checkbox", Default: true, Desc: "Show the &quot;Have a gift card?&quot; form on the cart page."},
			{ID: "enable_checkout", Title: "Checkout page", Type: "checkbox", Default: true, Desc: "Show the &quot;Have a gift card?&quot; form on the checkout page."},
			{ID: "account_cards", Title: "My Account", Type: "checkbox", Default: true, Desc: "Let customers add gift cards to their account and see their balances under My Account › Gift Cards."},
			{Type: "sectionend"},
		},
		Pages: []gooExtMenuItem{{Page: "gift-cards", Title: "Gift Cards"}},
	})
	gooRegisterProductType(&gooProductTypeDef{
		Type: gcType, Label: "Gift card", Module: gcKey, Virtual: true,
		Purchasable: func(s *Server, ctx context.Context, p *gooProduct) bool {
			return s.gooExtOn(gcKey) && (p.Status == "publish" || p.Status == "private") && (len(gcAmounts(p)) > 0 || gxBool(p.Meta["_gift_card_custom_amount"]))
		},
		NeedsShipping: func(s *Server, ctx context.Context, p *gooProduct) bool { return false },
		AddToCartText: func(s *Server, p *gooProduct, single bool) string {
			if single {
				return "Add to cart"
			}
			return "Select amount"
		},
	})
	gooAddToCartHooks = append(gooAddToCartHooks, gcAddToCart)
	gooCartItemPriceHooks = append(gooCartItemPriceHooks, gcCartPrice)
	gooCartItemDataHooks = append(gooCartItemDataHooks, gcItemData)
	gooCartFeesHooks = append(gooCartFeesHooks, gcCartFees)
	gooPriceHTMLHooks = append(gooPriceHTMLHooks, gcPriceHTML)
	gooOn("order_created", 20, func(s *Server, ctx context.Context, arg any) { s.gcOrderCreated(ctx, arg.(*gooOrder)) })
	gooOn("order_status_changed", 20, func(s *Server, ctx context.Context, arg any) { s.gcOrderStatus(ctx, arg.(*gooStatusChange)) })
	gooOn("cart_emptied", 20, func(s *Server, ctx context.Context, arg any) {
		if cart, ok := arg.(*gooCart); ok && cart.sess.Data.Extra != nil {
			if _, had := cart.sess.Data.Extra[gcKey]; had {
				delete(cart.sess.Data.Extra, gcKey)
				cart.sess.touch()
			}
		}
	})
	gooOn("daily", 20, func(s *Server, ctx context.Context, _ any) { s.gcDaily(ctx) })
	gooRegisterEmail(&gooEmailDef{ID: "gift_card_received", Title: "Gift card received", Module: gcKey, Customer: true,
		Description: "Gift card e-mails are sent to gift card recipients when their gift card is issued, or on its delivery date.",
		Subject:     "You have received a {giftcard_amount} gift card from {sender}", Heading: "You have received a gift card!"})
}

// Product -----------------------------------------------------------------------------------------------

// gcAmounts are the product's fixed amounts, ascending.
func gcAmounts(p *gooProduct) []float64 {
	var out []float64
	for _, v := range strings.FieldsFunc(gxStr(p.Meta["_gift_card_amounts"]), func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '|' }) {
		if f := gxAtof(v); f > 0 {
			out = append(out, f)
		}
	}
	sort.Float64s(out)
	return out
}

// Cards ------------------------------------------------------------------------------------------------

type gcCard struct {
	ID            int64
	Code          string
	Initial       float64
	Balance       float64
	Currency      string
	OrderID       int64
	OrderItemID   int64
	Recipient     string
	RecipientName string
	Sender        string
	SenderEmail   string
	Message       string
	DeliverDate   *time.Time
	Delivered     bool
	Expires       *time.Time
	Status        string // active, disabled, redeemed (fully used)
	RedeemedBy    int64
	Created       time.Time
}

const gcCols = `id, code, initial::float8, balance::float8, currency, order_id, order_item_id, recipient, recipient_name, sender, sender_email, message,
	deliver_date, delivered, expires, status, redeemed_by, created`

func gcScan(row pgx.Row) (*gcCard, error) {
	g := &gcCard{}
	err := row.Scan(&g.ID, &g.Code, &g.Initial, &g.Balance, &g.Currency, &g.OrderID, &g.OrderItemID, &g.Recipient, &g.RecipientName, &g.Sender,
		&g.SenderEmail, &g.Message, &g.DeliverDate, &g.Delivered, &g.Expires, &g.Status, &g.RedeemedBy, &g.Created)
	return g, err
}

func (s *Server) gcQuery(ctx context.Context, where string, args ...any) []*gcCard {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+gcCols+` FROM goo_gift_cards WHERE `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*gcCard
	for rows.Next() {
		if g, err := gcScan(rows); err == nil {
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) gcGet(ctx context.Context, id int64) (*gcCard, error) {
	return gcScan(s.store.Pool.QueryRow(ctx, `SELECT `+gcCols+` FROM goo_gift_cards WHERE id = $1`, id))
}

func (s *Server) gcByCode(ctx context.Context, code string) (*gcCard, error) {
	return gcScan(s.store.Pool.QueryRow(ctx, `SELECT `+gcCols+` FROM goo_gift_cards WHERE code = $1`, gcNormalize(code)))
}

// gcNormalize upper-cases a code and restores its dashes.
func gcNormalize(code string) string {
	code = strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(code)))
	if len(code) == 16 {
		return code[0:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:16]
	}
	return code
}

const gcAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// gcNewCode is a random XXXX-XXXX-XXXX-XXXX code.
func gcNewCode() string {
	var b strings.Builder
	for i := 0; i < 16; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(gcAlphabet))))
		b.WriteByte(gcAlphabet[n.Int64()])
	}
	return b.String()
}

// gcCreate inserts a card with a fresh code and logs its issue.
func (s *Server) gcCreate(ctx context.Context, g *gcCard, userID int64, note string) error {
	if g.Currency == "" {
		g.Currency = s.gooCurrency()
	}
	if g.Status == "" {
		g.Status = "active"
	}
	g.Balance = g.Initial
	var err error
	for i := 0; i < 5; i++ {
		g.Code = gcNewCode()
		err = s.store.Pool.QueryRow(ctx, `INSERT INTO goo_gift_cards (code, initial, balance, currency, order_id, order_item_id, recipient, recipient_name, sender,
			sender_email, message, deliver_date, delivered, expires, status, redeemed_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			ON CONFLICT (code) DO NOTHING RETURNING id, created`, g.Code, g.Initial, g.Balance, g.Currency, g.OrderID, g.OrderItemID, g.Recipient, g.RecipientName,
			g.Sender, g.SenderEmail, g.Message, g.DeliverDate, g.Delivered, g.Expires, g.Status, g.RedeemedBy).Scan(&g.ID, &g.Created)
		if !errors.Is(err, pgx.ErrNoRows) {
			break
		}
	}
	if err != nil {
		return err
	}
	return s.gcLog(ctx, g.ID, "issued", g.Initial, g.OrderID, userID, note)
}

func (s *Server) gcLog(ctx context.Context, cardID int64, typ string, amount float64, orderID, userID int64, note string) error {
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO goo_gift_card_activity (card_id, type, amount, order_id, user_id, note) VALUES ($1,$2,$3,$4,$5,$6)`,
		cardID, typ, amount, orderID, userID, note)
	return err
}

type gcActivity struct {
	ID      int64
	CardID  int64
	Type    string
	Amount  float64
	OrderID int64
	UserID  int64
	Note    string
	Date    time.Time
}

func (s *Server) gcActivities(ctx context.Context, cardID int64) []gcActivity {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, card_id, type, amount::float8, order_id, user_id, note, date FROM goo_gift_card_activity WHERE card_id = $1 ORDER BY date DESC, id DESC`, cardID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []gcActivity
	for rows.Next() {
		var a gcActivity
		if rows.Scan(&a.ID, &a.CardID, &a.Type, &a.Amount, &a.OrderID, &a.UserID, &a.Note, &a.Date) == nil {
			out = append(out, a)
		}
	}
	return out
}

// gcUsable checks that a card can pay: active, not expired, with a balance.
func (s *Server) gcUsable(g *gcCard) error {
	switch {
	case g.Status == "disabled":
		return errors.New("This gift card has been disabled.")
	case g.Expires != nil && time.Now().After(g.Expires.AddDate(0, 0, 1)):
		return errors.New("This gift card has expired.")
	case g.Balance <= 0:
		return errors.New("This gift card has no remaining balance.")
	}
	return nil
}

// gcAdjust changes a balance by delta (negative = debit), keeping the status in step, and logs it.
func (s *Server) gcAdjust(ctx context.Context, g *gcCard, delta float64, typ string, orderID, userID int64, note string) error {
	dp := s.gooDecimals()
	g.Balance = gooRound(max(g.Balance+delta, 0), dp)
	if g.Status != "disabled" {
		g.Status = "active"
		if g.Balance <= 0 {
			g.Status = "redeemed"
		}
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET balance = $2, status = $3 WHERE id = $1`, g.ID, g.Balance, g.Status); err != nil {
		return err
	}
	amount := delta
	if amount < 0 {
		amount = -amount
	}
	return s.gcLog(ctx, g.ID, typ, gooRound(amount, dp), orderID, userID, note)
}

// Cart --------------------------------------------------------------------------------------------------

// gcAddToCart validates the amount and the recipient fields.
func gcAddToCart(s *Server, ctx context.Context, p *gooProduct, qty int, form map[string][]string, data map[string]any) error {
	if !s.gooExtOn(gcKey) || p.Type != gcType {
		return nil
	}
	amount := 0.0
	if v := gxForm(form, "wc_gc_giftcard_amount"); v != "" && v != "custom" {
		amount = gxAtof(v)
		ok := false
		for _, a := range gcAmounts(p) {
			if a == amount {
				ok = true
			}
		}
		if !ok {
			return gooCartErr("Please choose a valid gift card amount.")
		}
	} else if gxBool(p.Meta["_gift_card_custom_amount"]) {
		amount = gxAtof(gxForm(form, "wc_gc_giftcard_custom_amount"))
		mn, mx := gxNum(p.Meta["_gift_card_min"]), gxNum(p.Meta["_gift_card_max"])
		if amount <= 0 {
			return gooCartErr("Please enter a gift card amount.")
		}
		if mn > 0 && amount < mn {
			return gooCartErr("The minimum gift card amount is %s.", html.EscapeString(s.gooPricePlain(mn, s.gooCurrency())))
		}
		if mx > 0 && amount > mx {
			return gooCartErr("The maximum gift card amount is %s.", html.EscapeString(s.gooPricePlain(mx, s.gooCurrency())))
		}
	} else {
		return gooCartErr("Please choose a gift card amount.")
	}
	to := gxForm(form, "wc_gc_giftcard_to")
	if a, err := mail.ParseAddress(to); err != nil || a.Address != to || !strings.Contains(to, "@") {
		return gooCartErr("Please enter a valid email address in the &quot;To&quot; field.")
	}
	from := gxForm(form, "wc_gc_giftcard_from")
	if from == "" {
		return gooCartErr("Please enter your name in the &quot;From&quot; field.")
	}
	msg := gxForm(form, "wc_gc_giftcard_message")
	if len([]rune(msg)) > 500 {
		return gooCartErr("The gift card message cannot be longer than 500 characters.")
	}
	deliver := gxForm(form, "wc_gc_giftcard_delivery")
	if deliver != "" {
		d, err := time.ParseInLocation("2006-01-02", deliver, s.gxLoc())
		if err != nil {
			return gooCartErr("Please enter a valid delivery date.")
		}
		now := time.Now().In(s.gxLoc())
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.gxLoc())
		if d.Before(today) {
			return gooCartErr("The delivery date cannot be in the past.")
		}
		if !d.After(today) {
			deliver = ""
		}
	}
	data["gift_card"] = map[string]any{"amount": gooRound(amount, s.gooDecimals()), "to": to, "to_name": gxForm(form, "wc_gc_giftcard_to_name"),
		"from": from, "message": msg, "deliver": deliver}
	return nil
}

func gcItemCard(it *gooCartItem) map[string]any {
	m, _ := it.Data["gift_card"].(map[string]any)
	return m
}

// gcCartPrice sets the gift card's price to the chosen amount.
func gcCartPrice(s *Server, ctx context.Context, cart *gooCart, it *gooCartItem) {
	if !s.gooExtOn(gcKey) {
		return
	}
	if m := gcItemCard(it); m != nil {
		it.Price = gxNum(m["amount"])
	}
}

// gcItemData shows To / From / Message / Delivery Date under a gift card.
func gcItemData(s *Server, ctx context.Context, it *gooCartItem) []gooKV {
	if !s.gooExtOn(gcKey) {
		return nil
	}
	m := gcItemCard(it)
	if m == nil {
		return nil
	}
	to := gxStr(m["to"])
	if n := gxStr(m["to_name"]); n != "" {
		to = n + " <" + to + ">"
	}
	out := []gooKV{{"To", to}, {"From", gxStr(m["from"])}}
	if v := gxStr(m["message"]); v != "" {
		out = append(out, gooKV{"Message", v})
	}
	if v := gxStr(m["deliver"]); v != "" {
		if d, err := time.ParseInLocation("2006-01-02", v, s.gxLoc()); err == nil {
			out = append(out, gooKV{"Delivery Date", s.gxDate(d)})
		}
	}
	return out
}

// gcApplied is the session's gift card codes.
func gcApplied(sess *gooSession) []string {
	var out []string
	if sess.Data.Extra == nil {
		return nil
	}
	switch v := sess.Data.Extra[gcKey].(type) {
	case []any:
		for _, x := range v {
			if c := gxStr(x); c != "" {
				out = append(out, c)
			}
		}
	case []string:
		out = v
	}
	return out
}

func gcSetApplied(sess *gooSession, codes []string) {
	if sess.Data.Extra == nil {
		sess.Data.Extra = map[string]any{}
	}
	if len(codes) == 0 {
		delete(sess.Data.Extra, gcKey)
	} else {
		list := make([]any, len(codes))
		for i, c := range codes {
			list[i] = c
		}
		sess.Data.Extra[gcKey] = list
	}
	sess.touch()
}

// gcShippingEstimate is the shipping the cart will charge (cost and taxes of the chosen rate), worked
// out the way Calculate does after the fees (gift cards pay for shipping too).
func (s *Server) gcShippingEstimate(ctx context.Context, cart *gooCart) float64 {
	if !cart.NeedsShipping() {
		return 0
	}
	dest := cart.shippingDestination()
	if dest.Country == "" || !s.gooShipsTo(dest.Country) {
		return 0
	}
	cu := cart.sess.Data.Customer
	if s.gooOptBool("shipping_cost_requires_address") && !cu.CalculatedShip && cu.Billing["country"] == "" && cu.Shipping["country"] == "" {
		return 0
	}
	pkg := &gooPackage{Destination: dest, Coupons: cart.coupons, cart: cart}
	for _, it := range cart.Items {
		if s.gooNeedsShipping(ctx, it.product) {
			pkg.Items = append(pkg.Items, it)
			pkg.Contents += it.LineTotal
			pkg.ContentsTax += it.LineTax
			pkg.Subtotal += it.LineSubtotal
			if s.gooOptString("tax_display_cart") == "incl" {
				pkg.Subtotal += it.LineSubtotalTax
			}
		}
	}
	rates := s.gooPackageRates(ctx, pkg)
	if len(rates) == 0 {
		return 0
	}
	chosen := rates[0]
	for _, r := range rates {
		if r.ID == cart.sess.Data.ChosenShipping["0"] {
			chosen = r
		}
	}
	dp := s.gooDecimals()
	cost := gooRound(chosen.Cost, dp)
	if chosen.Taxable && s.gooTaxEnabled() {
		class := s.gooOptString("shipping_tax_class")
		if class == "inherit" {
			class = cart.inheritedShippingTaxClass()
		}
		loc := cart.taxLocation()
		if chosen.MethodID == "local_pickup" {
			loc = s.gooBaseLocation()
		}
		cost += gooRound(gooSumTaxes(gooCalcTax(cost, s.gooFindRates(ctx, class, loc, true), false)), dp)
	}
	return cost
}

// gcCartFees applies the session's gift cards as negative fees, capped at what is left to pay.
func gcCartFees(s *Server, ctx context.Context, cart *gooCart) {
	if !s.gooExtOn(gcKey) {
		return
	}
	codes := gcApplied(cart.sess)
	if len(codes) == 0 {
		return
	}
	remaining := cart.CartContentsTotal + gooSumTaxes(cart.Taxes)
	for _, f := range cart.Fees {
		remaining += f.Amount
		if f.Taxable {
			// approximate: the fee's taxes are added by Calculate after the hooks
			remaining += gooSumTaxes(gooCalcTax(f.Amount, s.gooFindRates(ctx, f.TaxClass, cart.TaxLocation, false), false))
		}
	}
	remaining += s.gcShippingEstimate(ctx, cart)
	dp := s.gooDecimals()
	var keep []string
	for _, code := range codes {
		g, err := s.gcByCode(ctx, code)
		if err != nil {
			cart.errors = append(cart.errors, fmt.Sprintf("Gift card %s has been removed from your cart because it no longer exists.", html.EscapeString(code)))
			continue
		}
		if err := s.gcUsable(g); err != nil {
			cart.errors = append(cart.errors, fmt.Sprintf("Gift card %s has been removed from your cart: %s", html.EscapeString(g.Code), html.EscapeString(err.Error())))
			continue
		}
		keep = append(keep, g.Code)
		amount := gooRound(min(g.Balance, max(remaining, 0)), dp)
		if amount <= 0 {
			continue
		}
		f := cart.AddFee(gcFeePref+strings.ToLower(strings.ReplaceAll(g.Code, "-", "")), "Gift card: "+g.Code, -amount, true, gcTaxClass)
		f.Meta = map[string]any{"gift_card_id": float64(g.ID), "gift_card_code": g.Code}
		remaining -= amount
	}
	if len(keep) != len(codes) {
		gcSetApplied(cart.sess, keep)
	}
}

// gcCartGiftCards lists the applied gift cards with the amount each pays.
func gcCartGiftCards(cart *gooCart) []*gooFee {
	var out []*gooFee
	for _, f := range cart.Fees {
		if strings.HasPrefix(f.ID, gcFeePref) {
			out = append(out, f)
		}
	}
	return out
}

// Orders -----------------------------------------------------------------------------------------------

// gcOrderFees are the order's gift card fee lines: card code → amount paid.
func gcOrderFees(o *gooOrder) map[string]float64 {
	out := map[string]float64{}
	for _, it := range o.ItemsOf("fee") {
		id := it.MetaString("_fee_id")
		if !strings.HasPrefix(id, gcFeePref) {
			continue
		}
		code := gcNormalize(strings.TrimPrefix(id, gcFeePref))
		out[code] += -(it.Total + it.TotalTax)
	}
	return out
}

// gcOrderCreated debits the gift cards used by the order.
func (s *Server) gcOrderCreated(ctx context.Context, o *gooOrder) {
	if !s.gooExtOn(gcKey) {
		return
	}
	for code, amount := range gcOrderFees(o) {
		g, err := s.gcByCode(ctx, code)
		if err != nil || amount <= 0 {
			continue
		}
		_ = s.gcAdjust(ctx, g, -amount, "used", o.ID, o.CustomerID, "")
		_ = s.gooAddOrderNote(ctx, o, fmt.Sprintf("Gift card %s debited: %s.", g.Code, s.gooPricePlain(amount, o.Currency)), false, "")
	}
}

// gcOrderStatus issues the cards of a paid order and credits back the cards that paid for an order
// that was cancelled, failed or refunded.
func (s *Server) gcOrderStatus(ctx context.Context, ch *gooStatusChange) {
	if !s.gooExtOn(gcKey) {
		return
	}
	o := ch.Order
	switch ch.To {
	case "processing", "completed":
		s.gcIssue(ctx, o)
	case "cancelled", "refunded", "failed":
		rows, err := s.store.Pool.Query(ctx, `SELECT a.card_id, sum(CASE WHEN a.type = 'used' THEN a.amount ELSE -a.amount END)::float8
			FROM goo_gift_card_activity a WHERE a.order_id = $1 AND a.type IN ('used', 'refunded') GROUP BY a.card_id`, o.ID)
		if err != nil {
			return
		}
		type due struct {
			id  int64
			amt float64
		}
		var list []due
		for rows.Next() {
			var d due
			if rows.Scan(&d.id, &d.amt) == nil && d.amt > 0.0001 {
				list = append(list, d)
			}
		}
		rows.Close()
		for _, d := range list {
			if g, err := s.gcGet(ctx, d.id); err == nil {
				_ = s.gcAdjust(ctx, g, d.amt, "refunded", o.ID, o.CustomerID, "Order "+gooStatusLabel(ch.To))
				_ = s.gooAddOrderNote(ctx, o, fmt.Sprintf("Gift card %s credited: %s.", g.Code, s.gooPricePlain(d.amt, o.Currency)), false, "")
			}
		}
	}
}

// gcIssue creates the order's gift cards (once per unit) and sends those due today.
func (s *Server) gcIssue(ctx context.Context, o *gooOrder) {
	for _, it := range o.ItemsOf("line_item") {
		m, ok := it.Meta["_goo_gift_card"].(map[string]any)
		if !ok {
			continue
		}
		var have int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_gift_cards WHERE order_item_id = $1`, it.ID).Scan(&have)
		var expires *time.Time
		if p, err := s.gooGetProduct(ctx, it.ProductID); err == nil {
			if days := int(gxNum(p.Meta["_gift_card_expiration_days"])); days > 0 {
				e := time.Now().In(s.gxLoc()).AddDate(0, 0, days)
				expires = &e
			}
		}
		var deliver *time.Time
		if d, err := time.ParseInLocation("2006-01-02", gxStr(m["deliver"]), s.gxLoc()); err == nil {
			deliver = &d
		}
		for i := have; i < it.Qty(); i++ {
			g := &gcCard{Initial: gxNum(m["amount"]), Currency: o.Currency, OrderID: o.ID, OrderItemID: it.ID, Recipient: gxStr(m["to"]),
				RecipientName: gxStr(m["to_name"]), Sender: gxStr(m["from"]), SenderEmail: o.Billing["email"], Message: gxStr(m["message"]),
				DeliverDate: deliver, Expires: expires}
			if err := s.gcCreate(ctx, g, o.CustomerID, fmt.Sprintf("Order #%d", o.ID)); err != nil {
				continue
			}
			_ = s.gooAddOrderNote(ctx, o, fmt.Sprintf("Gift card %s issued to %s.", g.Code, g.Recipient), false, "")
			if deliver == nil || !deliver.After(time.Now()) {
				s.gcDeliver(ctx, g)
			}
		}
	}
}

// gcDeliver e-mails a card to its recipient.
func (s *Server) gcDeliver(ctx context.Context, g *gcCard) {
	amount := s.gooPricePlain(g.Initial, g.Currency)
	var b strings.Builder
	hello := "Hi there,"
	if g.RecipientName != "" {
		hello = "Hi " + html.EscapeString(g.RecipientName) + ","
	}
	fmt.Fprintf(&b, `<p>%s</p><p>%s sent you a gift card worth <strong>%s</strong>!</p>`, hello, html.EscapeString(g.Sender), html.EscapeString(amount))
	if g.Message != "" {
		b.WriteString(`<blockquote style="border-left:3px solid #7f54b3;margin:16px 0;padding:8px 16px;font-style:italic">` + strings.ReplaceAll(html.EscapeString(g.Message), "\n", "<br>") + `</blockquote>`)
	}
	fmt.Fprintf(&b, `<div style="border:2px dashed #7f54b3;border-radius:8px;padding:20px;text-align:center;margin:20px 0"><div style="font-size:13px;color:#888;text-transform:uppercase;letter-spacing:1px">Gift card code</div>`+
		`<div style="font-size:24px;font-weight:bold;letter-spacing:2px;color:#333;margin:8px 0">%s</div><div style="font-size:18px;color:#7f54b3">%s</div>`, g.Code, html.EscapeString(amount))
	if g.Expires != nil {
		fmt.Fprintf(&b, `<div style="font-size:12px;color:#888;margin-top:6px">Expires on %s</div>`, html.EscapeString(s.gxDate(*g.Expires)))
	}
	b.WriteString(`</div>`)
	fmt.Fprintf(&b, `<p>To use your gift card, enter the code in the &quot;Have a gift card?&quot; field of the cart or checkout at <a href="%s">%s</a>.</p>`,
		html.EscapeString(s.gooShopURL(ctx)), html.EscapeString(s.settings.String("blogname")))
	if s.gxSendEmail(ctx, "gift_card_received", []string{g.Recipient}, map[string]string{"giftcard_amount": amount, "sender": g.Sender, "giftcard_code": g.Code}, b.String()) {
		g.Delivered = true
		_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET delivered = true WHERE id = $1`, g.ID)
	}
}

// gcDaily sends the cards whose delivery date came.
func (s *Server) gcDaily(ctx context.Context) {
	if !s.gooExtOn(gcKey) {
		return
	}
	now := time.Now().In(s.gxLoc())
	for _, g := range s.gcQuery(ctx, `NOT delivered AND status <> 'disabled' AND deliver_date IS NOT NULL AND deliver_date <= $1`, now.Format("2006-01-02")) {
		s.gcDeliver(ctx, g)
	}
}
