package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
)

// GoWebEditor Pro payment buttons: a port of upstream Pro 4.3.0's modules/payments. The PayPal
// button posts its (legacy) form straight to PayPal, so pages that carry one get PayPal added to
// their CSP form-action (and nothing else). The Stripe button's front-end handler posts
// submit_stripe_form to admin-ajax; the server creates a Stripe Checkout session with the test or
// live secret key from GoWebEditor › Settings › Integrations and the handler opens its URL.

// gweStripeAPI is Stripe's API base (Stripe_Handler::STRIPE_ENDPOINT_URL).
const gweStripeAPI = "https://api.stripe.com/v1/"

var gweStripeHost = gweHosts("api.stripe.com")

// gweStripeZeroDecimal are Stripe's zero-decimal currencies (currency_adaptation).
var gweStripeZeroDecimal = map[string]bool{"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true, "KRW": true, "MGA": true,
	"PYG": true, "RWF": true, "UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true}

// gweStripeAmount converts a price to Stripe's smallest currency unit (floored like upstream).
func gweStripeAmount(currency string, price float64) int64 {
	if gweStripeZeroDecimal[strings.ToUpper(currency)] {
		return int64(math.Floor(price))
	}
	return int64(math.Floor(price*100 + 1e-9))
}

func gweStripeHeader(secret string) http.Header {
	return http.Header{"Authorization": {"Bearer " + secret}, "Content-Type": {"application/x-www-form-urlencoded"}}
}

// gweStripeValidate checks a secret key (ajax_validate_secret_key: a tax_rates request).
func gweStripeValidate(ctx context.Context, secret string) error {
	if secret == "" {
		return errors.New("No key.")
	}
	res, err := gweOutboundDo(ctx, http.MethodGet, gweStripeAPI+"tax_rates?limit=1", gweStripeHeader(secret), nil, gweStripeHost, 0)
	if err != nil {
		return err
	}
	if res.Code != http.StatusOK {
		return errors.New("Stripe answered HTTP " + strconv.Itoa(res.Code) + ".")
	}
	return nil
}

// gweStripeTaxRates is pro_get_stripe_tax_rates: the active tax rates of the live and test accounts
// as select options keyed by ["<id>","inclusive|exclusive"].
func (s *Server) gweStripeTaxRates(ctx context.Context) map[string]any {
	return map[string]any{
		"live_api_key": gweStripeTaxOptions(ctx, strings.TrimSpace(s.settings.String("gwe_stripe_live_secret_key"))),
		"test_api_key": gweStripeTaxOptions(ctx, strings.TrimSpace(s.settings.String("gwe_stripe_test_secret_key"))),
	}
}

func gweStripeTaxOptions(ctx context.Context, secret string) builder.Ordered {
	opts := builder.Ordered{{K: "", V: "None"}}
	if secret == "" {
		return opts
	}
	res, err := gweOutboundDo(ctx, http.MethodGet, gweStripeAPI+"tax_rates?active=true", gweStripeHeader(secret), nil, gweStripeHost, 0)
	if err != nil || res.Code != http.StatusOK {
		return opts
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			Inclusive   bool   `json:"inclusive"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if json.Unmarshal(res.Body, &out) != nil {
		return opts
	}
	for _, r := range out.Data {
		behavior := "exclusive"
		if r.Inclusive {
			behavior = "inclusive"
		}
		key, _ := json.Marshal([]string{r.ID, behavior})
		name := r.DisplayName
		if r.Description != "" {
			name += " - " + r.Description
		}
		opts = append(opts, builder.KV{K: string(key), V: name})
	}
	return opts
}

// gweStripeReply mirrors what upstream's wp_send_json( $response ) sends: the remote response's
// code and its raw body, which the front-end handler parses.
func gweStripeReply(c *gin.Context, code int, body string) {
	c.JSON(http.StatusOK, gin.H{"response": gin.H{"code": code, "message": http.StatusText(code)}, "body": body})
}

func gweStripeError(c *gin.Context, code int, msg string) {
	b, _ := json.MarshalIndent(map[string]any{"error": map[string]any{"message": msg}}, "", "    ")
	gweStripeReply(c, code, string(b))
}

// gweStripeSubmit is admin-ajax submit_stripe_form (Module::submit_stripe_form).
func (s *Server) gweStripeSubmit(c *gin.Context) {
	data := func(k string) string { return c.PostForm("data[" + k + "]") }
	// The button's nonce field: verified when the page carries one (cached pages may not).
	if n := data("nonce"); n != "" && !s.checkNonceValue(c, n, "stripe_form_submit") {
		gweStripeError(c, http.StatusForbidden, "Something went wrong, please refresh the page.")
		return
	}
	_, settings := s.gweProFindWidget(c, gweInt(data("postId")), data("widgetId"), "stripe-button")
	if settings == nil {
		gweStripeError(c, http.StatusBadRequest, "Something went wrong, please refresh the page.")
		return
	}
	lb := s.links(c)
	home := lb.HomeURL("/")
	pageURL := data("pageUrl")
	if !gweSameSite(pageURL, home) {
		pageURL = home
	}
	productName := gweStr(settings["product_name"])
	if productName == "" {
		productName = "Product"
	}
	price, _ := strconv.ParseFloat(strings.TrimSpace(gweStr(settings["stripe_product_price"])), 64)
	currency := strings.ToUpper(gweStr(settings["stripe_currency"]))
	if currency == "" {
		currency = "USD"
	}
	quantity := strings.TrimSpace(gweStr(settings["stripe_quantity"]))
	if n, err := strconv.Atoi(quantity); err != nil || n < 1 {
		quantity = "1"
	}
	success := phpStringish(gweMap(settings["redirect_after_success"])["url"])
	if success == "" {
		success = pageURL
	}
	if u, err := url.Parse(success); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		success = pageURL
	}
	testMode := gweStr(settings["sandbox_mode"]) == "yes"
	taxRates := gweStr(settings["stripe_live_env_tax_rates_list"])
	secret := strings.TrimSpace(s.settings.String("gwe_stripe_live_secret_key"))
	if testMode {
		taxRates = gweStr(settings["stripe_test_env_tax_rates_list"])
		secret = strings.TrimSpace(s.settings.String("gwe_stripe_test_secret_key"))
	}
	if secret == "" {
		gweStripeError(c, http.StatusUnauthorized, "You have not entered a valid secret key for this environment, Please add a valid secret key")
		return
	}
	body := url.Values{
		"cancel_url":                          {pageURL},
		"payment_method_types[0]":             {"card"},
		"success_url":                         {success},
		"mode":                                {"payment"},
		"line_items[0][quantity]":             {quantity},
		"line_items[0][price_data][currency]": {currency},
		"line_items[0][price_data][product_data][name]": {productName},
		"line_items[0][price_data][unit_amount]":        {strconv.FormatInt(gweStripeAmount(currency, price), 10)},
	}
	if ship, _ := strconv.ParseFloat(strings.TrimSpace(gweStr(settings["shipping_amount"])), 64); ship > 0 {
		body.Set("shipping_options[0][shipping_rate_data][type]", "fixed_amount")
		body.Set("shipping_options[0][shipping_rate_data][fixed_amount][amount]", strconv.FormatInt(int64(math.Floor(ship*100+1e-9)), 10))
		body.Set("shipping_options[0][shipping_rate_data][fixed_amount][currency]", currency)
		body.Set("shipping_options[0][shipping_rate_data][display_name]", "shipping fee")
	}
	var tax []string
	if taxRates != "" && json.Unmarshal([]byte(taxRates), &tax) == nil && len(tax) == 2 && tax[0] != "" && tax[1] != "" {
		body.Set("line_items[0][price_data][tax_behavior]", tax[1])
		body.Set("line_items[0][tax_rates][0]", tax[0])
	}
	res, err := gweOutboundDo(c.Request.Context(), http.MethodPost, gweStripeAPI+"checkout/sessions", gweStripeHeader(secret), []byte(body.Encode()), gweStripeHost, 0)
	if err != nil {
		gweStripeError(c, http.StatusBadGateway, "Can not connect to Stripe.")
		return
	}
	// The browser only needs the session URL (or Stripe's error message).
	var out map[string]any
	_ = json.Unmarshal(res.Body, &out)
	if res.Code == http.StatusOK {
		u, _ := out["url"].(string)
		if !strings.HasPrefix(u, "https://checkout.stripe.com/") {
			gweStripeError(c, http.StatusBadGateway, "Unexpected answer from Stripe.")
			return
		}
		b, _ := json.Marshal(map[string]any{"id": out["id"], "url": u})
		gweStripeReply(c, res.Code, string(b))
		return
	}
	msg := "An error occurred."
	if e := gweMap(out["error"]); e != nil && gweStr(e["message"]) != "" {
		msg = gweStr(e["message"])
	}
	gweStripeError(c, res.Code, msg)
}

func phpStringish(v any) string { return strings.TrimSpace(gweStr(v)) }

// gweSameSite reports whether raw is an http(s) URL on the site's host.
func gweSameSite(raw, home string) bool {
	u, err := url.Parse(raw)
	h, err2 := url.Parse(home)
	if err != nil || err2 != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return strings.EqualFold(u.Host, h.Host)
}

// gwePayPalFormAction are the form targets of the PayPal button (production and sandbox; the
// sandbox host redirects to www.sandbox.paypal.com, which form-action also covers).
const gwePayPalFormAction = "https://www.paypal.com https://sandbox.paypal.com https://www.sandbox.paypal.com"

// gwePaymentsCSP adds PayPal to the page's CSP form-action when a rendered document carries a PayPal
// button form. Called while the page renders (the response is buffered, so headers can change).
func gwePaymentsCSP(c *gin.Context, results []gweRendered) {
	has := false
	for _, r := range results {
		if r.res != nil && (strings.Contains(r.res.HTML, `action="https://www.paypal.com/cgi-bin/webscr"`) ||
			strings.Contains(r.res.HTML, `action="https://sandbox.paypal.com/cgi-bin/webscr"`)) {
			has = true
			break
		}
	}
	if !has {
		return
	}
	h := c.Writer.Header()
	csp := h.Get("Content-Security-Policy")
	if csp == "" || strings.Contains(csp, "paypal.com") {
		return
	}
	h.Set("Content-Security-Policy", strings.Replace(csp, "form-action 'self'", "form-action 'self' "+gwePayPalFormAction, 1))
}
