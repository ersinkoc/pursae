package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// The shared event model of Pursae Pixel. Every event has a canonical name (the Meta standard
// names, plus a few of our own) and canonical parameters; the platform mapping below turns them into
// each platform's own event names. The same table is sent to pixelgo.js, so the browser and the
// server-side senders (pixelgo_capi.go) agree.
//
// Canonical parameters (p): value, currency, items [{id, name, price, quantity, category}],
// content_name, content_category, search_string, order_id, num_items, tax, shipping, payment_type,
// content_type, and "extra": a flat map of custom parameters.

// pixelCanonicalEvents are the events the plugin knows by name, in the order the admin lists them.
var pixelCanonicalEvents = []string{
	"PageView", "ViewContent", "ViewCategory", "Search", "AddToCart", "RemoveFromCart", "ViewCart", "AddToWishlist",
	"InitiateCheckout", "AddPaymentInfo", "Purchase", "Lead", "CompleteRegistration", "Contact", "Download",
	"OutboundClick", "Comment", "Scroll", "TimeOnPage", "VideoPlay", "VideoComplete",
}

// pixelEventMap: canonical event → platform → the platform's event name. "@page" is the platform's
// page call (ttq.page(), pintrk('page')); an empty or missing entry means the platform does not get the event.
var pixelEventMap = map[string]map[string]string{
	"PageView":             {"meta": "PageView", "ga4": "page_view", "tiktok": "@page", "pinterest": "@page"},
	"ViewContent":          {"meta": "ViewContent", "ga4": "view_item", "gads": "view_item", "tiktok": "ViewContent", "pinterest": "pagevisit", "bing": "view_item"},
	"ViewCategory":         {"meta": "ViewCategory", "ga4": "view_item_list", "gads": "view_item_list", "tiktok": "ViewContent", "pinterest": "viewcategory", "bing": "view_item_list"},
	"Search":               {"meta": "Search", "ga4": "search", "gads": "view_search_results", "tiktok": "Search", "pinterest": "search", "bing": "search"},
	"AddToCart":            {"meta": "AddToCart", "ga4": "add_to_cart", "gads": "add_to_cart", "tiktok": "AddToCart", "pinterest": "addtocart", "bing": "add_to_cart"},
	"RemoveFromCart":       {"meta": "RemoveFromCart", "ga4": "remove_from_cart", "bing": "remove_from_cart"},
	"ViewCart":             {"meta": "ViewCart", "ga4": "view_cart", "bing": "view_cart"},
	"AddToWishlist":        {"meta": "AddToWishlist", "ga4": "add_to_wishlist", "tiktok": "AddToWishlist", "bing": "add_to_wishlist"},
	"InitiateCheckout":     {"meta": "InitiateCheckout", "ga4": "begin_checkout", "gads": "begin_checkout", "tiktok": "InitiateCheckout", "bing": "begin_checkout"},
	"AddPaymentInfo":       {"meta": "AddPaymentInfo", "ga4": "add_payment_info", "tiktok": "AddPaymentInfo", "bing": "add_payment_info"},
	"Purchase":             {"meta": "Purchase", "ga4": "purchase", "gads": "purchase", "tiktok": "CompletePayment", "pinterest": "checkout", "bing": "purchase"},
	"Lead":                 {"meta": "Lead", "ga4": "generate_lead", "gads": "generate_lead", "tiktok": "SubmitForm", "pinterest": "lead", "bing": "generate_lead"},
	"CompleteRegistration": {"meta": "CompleteRegistration", "ga4": "sign_up", "tiktok": "CompleteRegistration", "pinterest": "signup", "bing": "sign_up"},
	"Contact":              {"meta": "Contact", "ga4": "contact", "tiktok": "Contact", "pinterest": "custom", "bing": "contact"},
	"Download":             {"meta": "Download", "ga4": "file_download", "tiktok": "Download", "bing": "file_download"},
	"OutboundClick":        {"meta": "OutboundClick", "ga4": "click", "tiktok": "ClickButton", "bing": "outbound_click"},
	"Comment":              {"meta": "Comment", "ga4": "comment", "bing": "comment"},
	"Scroll":               {"meta": "Scroll", "ga4": "scroll", "bing": "scroll"},
	"TimeOnPage":           {"meta": "TimeOnPage", "ga4": "time_on_page", "bing": "time_on_page"},
	"VideoPlay":            {"meta": "VideoPlay", "ga4": "video_start", "bing": "video_start"},
	"VideoComplete":        {"meta": "VideoComplete", "ga4": "video_complete", "bing": "video_complete"},
}

// pixelMetaStandard are the names Meta tracks with track() instead of trackCustom().
var pixelMetaStandard = map[string]bool{"PageView": true, "ViewContent": true, "Search": true, "AddToCart": true, "AddToWishlist": true,
	"InitiateCheckout": true, "AddPaymentInfo": true, "Purchase": true, "Lead": true, "CompleteRegistration": true, "Contact": true, "Download": false}

// pixelPlatformEvent is the name a platform uses for an event; custom events keep their own name.
func pixelPlatformEvent(platform, event string) (string, bool) {
	if m, ok := pixelEventMap[event]; ok {
		n := m[platform]
		return n, n != ""
	}
	n := strings.TrimSpace(event)
	if platform == "ga4" {
		n = strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(n))
	}
	if platform == "pinterest" {
		return "custom", true
	}
	return n, n != ""
}

// pixelItem is one product line of an event.
type pixelItem struct {
	ID, Name, Category string
	Price              float64
	Qty                int
}

func (it pixelItem) toMap() map[string]any {
	m := map[string]any{"id": it.ID, "price": pixelMoney(it.Price), "quantity": max(it.Qty, 1)}
	if it.Name != "" {
		m["name"] = it.Name
	}
	if it.Category != "" {
		m["category"] = it.Category
	}
	return m
}

func pixelItems(items []pixelItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.toMap())
	}
	return out
}

func pixelMoney(v float64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 2, 64), 64)
	return f
}

// pixelItemID is the product id sent to the platforms: the product id or SKU with the prefix and suffix
// the catalog feed uses, so events match the catalog.
func pixelItemID(st *pixelSettings, id int64, sku string) string {
	base := strconv.FormatInt(id, 10)
	if st.ContentID == "sku" && strings.TrimSpace(sku) != "" {
		base = strings.TrimSpace(sku)
	}
	return st.IDPrefix + base + st.IDSuffix
}

func pixelBasePrice(p *gooProduct) float64 {
	switch {
	case p == nil:
		return 0
	case p.Price != nil:
		return *p.Price
	case p.MinPrice != nil:
		return *p.MinPrice
	case p.RegularPrice != nil:
		return *p.RegularPrice
	}
	return 0
}

// pixelProductCategory is the first product category of a product ("A > B" is not built: the
// platforms take one name).
func (s *Server) pixelProductCategory(c *gin.Context, id int64) string {
	terms, err := s.gooProductTerms(c, id)
	if err != nil {
		return ""
	}
	for _, t := range terms {
		if t.Taxonomy == "product_cat" {
			return t.Name
		}
	}
	return ""
}

func (s *Server) pixelProductItem(c *gin.Context, st *pixelSettings, p *gooProduct, qty int) pixelItem {
	pid := p.ID
	parent := p
	if p.ParentID > 0 {
		pid = p.ParentID
	}
	return pixelItem{ID: pixelItemID(st, pid, parent.SKU), Name: p.Name, Price: pixelBasePrice(p), Qty: qty, Category: s.pixelProductCategory(c, pid)}
}

// pixelOrderLineItems are the product lines of an order.
func (s *Server) pixelOrderLineItems(ctx context.Context, st *pixelSettings, o *gooOrder) ([]pixelItem, float64, float64, float64) {
	var items []pixelItem
	var net, withTax float64
	for _, it := range o.Items {
		if it.Type != "line_item" && it.Type != "" || it.ProductID <= 0 {
			continue
		}
		sku := ""
		if p, err := s.gooGetProduct(ctx, it.ProductID); err == nil && p != nil {
			sku = p.SKU
		}
		qty := max(it.Qty(), 1)
		items = append(items, pixelItem{ID: pixelItemID(st, it.ProductID, sku), Name: it.Name, Price: it.Total / float64(qty), Qty: qty})
		net += it.Total
		withTax += it.Total + it.TotalTax
	}
	return items, net, withTax, 0
}

// pixelOrderValue is the Purchase value by the chosen mode.
func pixelOrderValue(st *pixelSettings, o *gooOrder, net, withTax float64) float64 {
	switch st.ValueMode {
	case "net":
		return net
	case "subtotal":
		return withTax
	}
	return o.Total
}

// pixelOrderParams are the canonical parameters of an order's Purchase event.
func (s *Server) pixelOrderParams(ctx context.Context, st *pixelSettings, o *gooOrder) map[string]any {
	items, net, withTax, _ := s.pixelOrderLineItems(ctx, st, o)
	num := 0
	for _, it := range items {
		num += it.Qty
	}
	cur := o.Currency
	if cur == "" {
		cur = s.gooCurrency()
	}
	return map[string]any{
		"value": pixelMoney(pixelOrderValue(st, o, net, withTax)), "currency": cur, "items": pixelItems(items), "num_items": num,
		"order_id": strconv.FormatInt(o.ID, 10), "tax": pixelMoney(o.TotalTax), "shipping": pixelMoney(o.ShippingTotal),
		"payment_type": o.PaymentMethodTitle, "content_type": "product",
	}
}

// pixelPurchaseEventID is the id the browser and the server share for an order, so a platform counts it once.
func pixelPurchaseEventID(orderID int64) string { return "purchase_" + strconv.FormatInt(orderID, 10) }

// pixelPurchaseWanted is false for orders that are no sale: failed, cancelled, refunded, drafts.
func pixelPurchaseWanted(o *gooOrder) bool {
	switch o.Status {
	case "failed", "cancelled", "refunded", "trash", "checkout-draft", "auto-draft":
		return false
	}
	return o.Type == "" || o.Type == "shop_order"
}

// pixelHash is the SHA-256 hex of the normalized value (lower case, trimmed), the form Meta, TikTok and
// Pinterest ask for personal data in.
func pixelHash(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func pixelHashPhone(v string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, v)
	return pixelHash(d)
}

// pixelUserFromOrder is the hashed personal data of a customer, for matching.
func pixelUserFromOrder(o *gooOrder) map[string]string {
	b := o.Billing
	u := map[string]string{
		"em": pixelHash(b["email"]), "ph": pixelHashPhone(b["phone"]), "fn": pixelHash(b["first_name"]), "ln": pixelHash(b["last_name"]),
		"ct": pixelHash(strings.ReplaceAll(b["city"], " ", "")), "st": pixelHash(b["state"]), "zp": pixelHash(strings.ReplaceAll(b["postcode"], " ", "")),
		"country": pixelHash(b["country"]),
	}
	for k, v := range u {
		if v == "" {
			delete(u, k)
		}
	}
	return u
}

// pixelPageEvents are the events of the page being rendered, in canonical form. They are the same for
// every visitor of a page, except the Purchase of an order-received page (which carries its own key).
func (s *Server) pixelPageEvents(c *gin.Context, q *siteQuery, st *pixelSettings) (events []map[string]any, page map[string]any) {
	page = map[string]any{}
	emit := func(ev map[string]any) { events = append(events, ev) }
	add := func(name string, p map[string]any) { emit(map[string]any{"name": name, "p": p}) }
	if st.General["page_view"] {
		add("PageView", map[string]any{})
	}
	if q != nil && q.Kind == "search" && q.Search != "" && st.General["search"] {
		add("Search", map[string]any{"search_string": pixelTrim(q.Search, 100)})
	}
	if !s.gooOn() {
		return events, page
	}
	fv := gooViewFrom(c)
	if fv == nil {
		return events, page
	}
	cur := s.gooCurrency()
	switch fv.Kind {
	case "product":
		if fv.Product == nil {
			break
		}
		item := s.pixelProductItem(c, st, fv.Product, 1)
		p := map[string]any{"value": pixelMoney(item.Price), "currency": cur, "items": pixelItems([]pixelItem{item}), "content_name": item.Name,
			"content_category": item.Category, "content_type": "product", "num_items": 1}
		if st.Woo["view_content"] {
			add("ViewContent", p)
		}
		page["product"] = p // the add-to-cart button of the page builds its event from it
	case "shop", "product_cat", "product_tag", "product_attr":
		if st.Woo["view_category"] {
			name := "Shop"
			if fv.Term != nil {
				name = fv.Term.Name
			}
			add("ViewCategory", map[string]any{"content_name": name, "content_category": name, "content_type": "product_group", "currency": cur})
		}
		if st.General["search"] && fv.Search != "" {
			add("Search", map[string]any{"search_string": pixelTrim(fv.Search, 100), "content_type": "product"})
		}
	case "cart":
		if st.Woo["view_cart"] {
			if ev, ok := s.pixelCartEvent(c, st); ok {
				add("ViewCart", ev)
			}
		}
	case "checkout":
		if fv.Endpoint == "order-received" {
			s.pixelOrderReceived(c, st, fv, emit)
			break
		}
		if fv.Endpoint != "" {
			break
		}
		if ev, ok := s.pixelCartEvent(c, st); ok {
			if st.Woo["initiate_checkout"] {
				add("InitiateCheckout", ev)
			}
			if st.Woo["add_payment_info"] {
				page["checkout"] = ev // pixelgo.js fires AddPaymentInfo from it
			}
		}
	}
	return events, page
}

// pixelCartEvent is the canonical event data of the visitor's cart.
func (s *Server) pixelCartEvent(c *gin.Context, st *pixelSettings) (map[string]any, bool) {
	cart := s.gooCartFor(c)
	if cart == nil || len(cart.Items) == 0 {
		return nil, false
	}
	var items []pixelItem
	num := 0
	for _, it := range cart.Items {
		p := it.Product()
		if p == nil {
			continue
		}
		item := s.pixelProductItem(c, st, p, it.Quantity)
		item.Price = it.Price
		items = append(items, item)
		num += it.Quantity
	}
	value := cart.Total
	switch st.ValueMode {
	case "net":
		value = cart.CartContentsTotal
	case "subtotal":
		value = cart.CartContentsTotal + cart.CartContentsTax
	}
	return map[string]any{"value": pixelMoney(value), "currency": s.gooCurrency(), "items": pixelItems(items), "num_items": num, "content_type": "product"}, len(items) > 0
}

// pixelOrderReceived adds the Purchase of the order on the order-received page. The order key in the
// URL must match, so nobody can read another customer's order by guessing ids.
func (s *Server) pixelOrderReceived(c *gin.Context, st *pixelSettings, fv *gooFrontView, emit func(map[string]any)) {
	if !st.Woo["purchase"] {
		return
	}
	id, err := strconv.ParseInt(fv.Value, 10, 64)
	if err != nil || id <= 0 {
		return
	}
	o, err := s.gooGetOrder(c.Request.Context(), id)
	if err != nil || o == nil || subtle.ConstantTimeCompare([]byte(o.OrderKey), []byte(c.Query("key"))) != 1 || o.OrderKey == "" || !pixelPurchaseWanted(o) {
		return
	}
	c.Header("Cache-Control", "private, no-store")
	p := s.pixelOrderParams(c.Request.Context(), st, o)
	ev := map[string]any{"name": "Purchase", "id": pixelPurchaseEventID(o.ID), "p": p, "once": st.PurchaseOnce, "skip_server": true}
	if st.Advanced {
		ev["user"] = pixelUserFromOrder(o)
	}
	emit(ev)
}
