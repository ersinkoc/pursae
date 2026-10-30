package server

import (
	"context"
	"html/template"
	"log"
	"sort"

	"github.com/gin-gonic/gin"
)

// Goocommerce's extension points: the Go counterpart of the WooCommerce actions and filters its
// premium extensions hook into. Modules (goo_ext_*.go) register in init(); every hook of a module
// checks s.gooExtOn(module) itself, so a switched-off module changes nothing.

// gooProductTypeDef is a product type beyond the core four (bundle, subscription, booking, gift card).
type gooProductTypeDef struct {
	Type   string
	Label  string
	Module string // the extension that must be on for the type to be offered
	// Purchasable / NeedsShipping override the core rules when set.
	Purchasable   func(s *Server, ctx context.Context, p *gooProduct) bool
	NeedsShipping func(s *Server, ctx context.Context, p *gooProduct) bool
	// AddToCartText is the loop / single button text.
	AddToCartText func(s *Server, p *gooProduct, single bool) string
	// Virtual reports whether the product type is always virtual (bookings, gift cards).
	Virtual bool
}

var gooProductTypes = map[string]*gooProductTypeDef{}

// gooRegisterProductType adds a product type (product_type_selector).
func gooRegisterProductType(d *gooProductTypeDef) { gooProductTypes[d.Type] = d }

// gooActionFunc is an action callback (do_action).
type gooActionFunc func(s *Server, ctx context.Context, arg any)

type gooAction struct {
	prio int
	fn   gooActionFunc
}

var gooActions = map[string][]gooAction{}

// gooOn registers an action: product_saved (*gooProduct), low_stock / no_stock (*gooProduct),
// order_created (*gooOrder), order_status_changed (*gooStatusChange), payment_complete (*gooOrder),
// order_refunded (*gooRefundEvent), customer_created (int64 user id), cart_emptied (*gooCart).
func gooOn(name string, prio int, fn gooActionFunc) {
	gooActions[name] = append(gooActions[name], gooAction{prio, fn})
	sort.SliceStable(gooActions[name], func(i, j int) bool { return gooActions[name][i].prio < gooActions[name][j].prio })
}

// gooFire runs an action's callbacks; a panicking callback is logged, not fatal.
func (s *Server) gooFire(ctx context.Context, name string, arg any) {
	for _, a := range gooActions[name] {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("goocommerce: action %s: %v", name, r)
				}
			}()
			a.fn(s, ctx, arg)
		}()
	}
}

// gooStatusChange is the argument of order_status_changed.
type gooStatusChange struct {
	Order    *gooOrder
	From, To string
}

// Cart filters --------------------------------------------------------------------------------------

// gooAddToCartHook validates an add-to-cart request and may add cart item data (Product Add-Ons'
// fields, a booking's date, a gift card's recipient). form is the posted form (nil from the Store API
// without one); returning an error rejects the request with that message.
type gooAddToCartHook func(s *Server, ctx context.Context, p *gooProduct, qty int, form map[string][]string, data map[string]any) error

var gooAddToCartHooks []gooAddToCartHook

// gooCartItemPriceHook adjusts a cart item's unit price (woocommerce_before_calculate_totals /
// set_price): add-on prices, bundle pricing, dynamic pricing rules. It runs in registration order.
type gooCartItemPriceHook func(s *Server, ctx context.Context, cart *gooCart, item *gooCartItem)

var gooCartItemPriceHooks []gooCartItemPriceHook

// gooCartFeesHook adds fees (or negative fees: gift cards, points) to a cart (woocommerce_cart_calculate_fees).
type gooCartFeesHook func(s *Server, ctx context.Context, cart *gooCart)

var gooCartFeesHooks []gooCartFeesHook

// gooCartItemDataHook lists an item's extra data for display (woocommerce_get_item_data): label → value.
type gooCartItemDataHook func(s *Server, ctx context.Context, item *gooCartItem) []gooKV

var gooCartItemDataHooks []gooCartItemDataHook

// gooKV is a label / value pair shown under a cart or order item.
type gooKV struct {
	Key, Value string
}

// gooCheckoutHook validates the checkout before an order is created and may add order meta / items
// (woocommerce_after_checkout_validation, woocommerce_checkout_create_order).
type gooCheckoutHook func(s *Server, ctx context.Context, cart *gooCart, order *gooOrder) error

var gooCheckoutHooks []gooCheckoutHook

// Front-end output ----------------------------------------------------------------------------------

// gooHTMLHook prints markup at a template location for a product:
// "single_before_add_to_cart" (inside the form: add-on fields, booking form), "single_after_add_to_cart",
// "single_summary_end" (wishlist / compare buttons), "loop_after_item" (buttons under a catalog item),
// "single_meta_end".
type gooHTMLHook func(s *Server, c *gin.Context, p *gooProduct) template.HTML

var gooHTMLHooks = map[string][]gooHTMLHook{}

func gooOnHTML(location string, fn gooHTMLHook) {
	gooHTMLHooks[location] = append(gooHTMLHooks[location], fn)
}

func (s *Server) gooHTML(c *gin.Context, location string, p *gooProduct) template.HTML {
	var out template.HTML
	for _, fn := range gooHTMLHooks[location] {
		out += fn(s, c, p)
	}
	return out
}

// gooPageHook prints markup on a store page: "cart_before", "cart_after", "checkout_before_payment",
// "checkout_after_order_review", "thankyou", "footer" (quick view modal, compare bar).
type gooPageHook func(s *Server, c *gin.Context) template.HTML

var gooPageHooks = map[string][]gooPageHook{}

func gooOnPage(location string, fn gooPageHook) {
	gooPageHooks[location] = append(gooPageHooks[location], fn)
}

func (s *Server) gooPageHTML(c *gin.Context, location string) template.HTML {
	var out template.HTML
	for _, fn := range gooPageHooks[location] {
		out += fn(s, c)
	}
	return out
}

// gooAccountEndpoint is a My Account tab added by a module (Subscriptions, Memberships, Bookings,
// Wishlist, Points, Gift Cards).
type gooAccountEndpoint struct {
	Slug   string
	Title  string
	Module string
	After  string // the core endpoint it follows ("orders", "downloads", …)
	Render func(s *Server, c *gin.Context, arg string) template.HTML
}

var gooAccountEndpoints []*gooAccountEndpoint

// gooProductTab is a tab on the single product page (woocommerce_product_tabs).
type gooProductTab struct {
	Key      string
	Title    func(s *Server, c *gin.Context, p *gooProduct) string // "" hides the tab
	Priority int
	Render   func(s *Server, c *gin.Context, p *gooProduct) template.HTML
}

var gooProductTabs []*gooProductTab

// gooFrontRoute is a front-end URL handled by a module (/?goo-ajax=… actions, invoice downloads):
// Match receives the path without slashes and returns true when it served the request.
type gooFrontRoute func(s *Server, c *gin.Context, path string) bool

var gooFrontRoutes []gooFrontRoute

// gooEmailAttachHook returns attachments for an e-mail (PDF invoices): file name → content.
type gooEmailAttachHook func(s *Server, ctx context.Context, emailID string, o *gooOrder) map[string][]byte

var gooEmailAttachHooks []gooEmailAttachHook

// gooOrderActionHook adds entries to the admin order screen's "Order actions" box (Generate invoice…).
type gooOrderAction struct {
	ID, Label string
	Module    string
	Run       func(s *Server, c *gin.Context, o *gooOrder) error
}

var gooOrderActions []*gooOrderAction

// gooAdminPanelHook prints a panel on the admin product data box ("product_options" after General,
// "product_tabs" adds tabs) and saves its fields. Modules use it for their product settings.
type gooProductPanel struct {
	ID, Label string
	Module    string
	ShowFor   []string // product types the tab shows for, nil = all
	Render    func(s *Server, c *gin.Context, p *gooProduct) template.HTML
	Save      func(s *Server, c *gin.Context, p *gooProduct)
}

var gooProductPanels []*gooProductPanel

// E-mails ------------------------------------------------------------------------------------------------

// gooEmailDef is a transactional e-mail (WC_Email): its settings live in the option
// "email_<id>_settings" ({enabled, recipient, subject, heading, additional_content, email_type}).
type gooEmailDef struct {
	ID          string
	Title       string
	Description string
	Customer    bool // sent to the customer; else to Recipient (default: the admin e-mail)
	Manual      bool // only sent from the order screen (customer invoice)
	Subject     string
	Heading     string
	Additional  string
	Module      string // the extension that provides it, "" for core
}

var gooEmailDefs []*gooEmailDef

func gooRegisterEmail(d *gooEmailDef) { gooEmailDefs = append(gooEmailDefs, d) }

// gooEmailDefByID finds a registered e-mail.
func gooEmailDefByID(id string) *gooEmailDef {
	for _, d := range gooEmailDefs {
		if d.ID == id {
			return d
		}
	}
	return nil
}

// gooEmailSettings are an e-mail's settings over its defaults (enabled "yes"/"no", email_type html).
func (s *Server) gooEmailSettings(id string) map[string]string {
	out := map[string]string{"enabled": "yes", "email_type": "html", "recipient": "", "subject": "", "heading": "", "additional_content": ""}
	if d := gooEmailDefByID(id); d != nil {
		out["subject"], out["heading"], out["additional_content"] = d.Subject, d.Heading, d.Additional
		if !d.Customer {
			out["recipient"] = s.settings.String("admin_email")
		}
	}
	for k, v := range s.gooOptMap("email_" + id + "_settings") {
		if str, ok := v.(string); ok {
			out[k] = str
		}
	}
	return out
}

// gooEmailSender sends a registered e-mail for an order (set by goo_emails.go).
var gooEmailSender func(s *Server, ctx context.Context, id string, o *gooOrder, args map[string]any) error

// gooTriggerEmail sends an e-mail now (order actions: "Email invoice / order details to customer",
// "Resend new order notification").
func (s *Server) gooTriggerEmail(ctx context.Context, id string, o *gooOrder, args map[string]any) error {
	if gooEmailSender == nil {
		return nil
	}
	return gooEmailSender(s, ctx, id, o, args)
}
