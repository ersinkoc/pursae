package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// pixelPageConfig parses the pixelgo-config block of a page.
func pixelPageConfig(t *testing.T, page string) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/json" id="pixelgo-config">(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the page has no pixelgo-config block")
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(m[1]), &cfg); err != nil {
		t.Fatalf("pixelgo-config is not JSON: %v", err)
	}
	return cfg
}

func pixelEventNamed(cfg map[string]any, name string) map[string]any {
	evs, _ := cfg["events"].([]any)
	for _, e := range evs {
		if m, _ := e.(map[string]any); m["name"] == name {
			return m
		}
	}
	return nil
}

// TestPixelWooFunnel follows a product through the store: ViewContent on its page, the Purchase of its
// order on the order-received page (only with the order key), and the server-side Purchase when the
// payment completes (once, with the same event id the page uses, and with hashed customer data).
func TestPixelWooFunnel(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	fake := pixelFakeAPIs(t)
	if err := srv.pixelActivate(ctx); err != nil {
		t.Fatal(err)
	}
	st := srv.pixelLoad()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{"123456789012345"}, Token: "META_TOKEN", ServerSide: true}
	st.Advanced = true
	st.ContentID = "sku"
	st.IDPrefix = "shop_"
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}

	p := gooMakeSimple(t, srv, "Teapot", 10, func(p *gooProduct) { p.SKU = "TEA-1" })
	srv.purgeAll(ctx)

	// The product page: ViewContent with the catalog id (prefix + SKU) and its price.
	page := bodyOf(anonGet(t, srv.gooPermalink(ctx, p)))
	cfg := pixelPageConfig(t, page)
	vc := pixelEventNamed(cfg, "ViewContent")
	if vc == nil {
		t.Fatalf("no ViewContent on the product page: %v", cfg["events"])
	}
	params := vc["p"].(map[string]any)
	items := params["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "shop_TEA-1" || params["value"] != 10.0 || params["content_name"] != "Teapot" {
		t.Errorf("ViewContent params: %v", params)
	}
	if cfg["page"].(map[string]any)["product"] == nil {
		t.Error("the product page gives the add-to-cart button no product data")
	}
	if pixelEventNamed(cfg, "PageView") == nil {
		t.Error("no PageView")
	}

	// The shop page is a category view, not a product view.
	if cfg2 := pixelPageConfig(t, bodyOf(anonGet(t, base+"/?post_type=product"))); pixelEventNamed(cfg2, "ViewContent") != nil {
		t.Error("ViewContent on a page that is not a product")
	}

	// An order: payment completes → one server-side Purchase with the shared event id.
	o := gooTestOrder(t, srv, p, 2)
	if err := srv.gooPaymentComplete(ctx, o, "tx-1"); err != nil {
		t.Fatal(err)
	}
	var hit *pixelHit
	for i := 0; i < 50 && hit == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		hit = fake.find("/meta/123456789012345/events")
	}
	if hit == nil {
		t.Fatal("the paid order never reached the Conversions API")
	}
	d := hit.Body["data"].([]any)[0].(map[string]any)
	cd, ud := d["custom_data"].(map[string]any), d["user_data"].(map[string]any)
	if d["event_name"] != "Purchase" || d["event_id"] != pixelPurchaseEventID(o.ID) || cd["order_id"] != strconv.FormatInt(o.ID, 10) ||
		cd["value"] != o.Total || cd["currency"] == "" || cd["content_ids"].([]any)[0] != "shop_TEA-1" {
		t.Errorf("server Purchase: %v", d)
	}
	if em, _ := ud["em"].([]any); len(em) != 1 || em[0] != pixelHash("ann@example.test") {
		t.Errorf("customer e-mail is not sent hashed: %v", ud)
	}
	if strings.Contains(pixelJSON(hit.Body), "ann@example.test") {
		t.Error("a plain e-mail address reached the platform")
	}
	// A second completion (a webhook retry) sends nothing more.
	if err := srv.gooPaymentComplete(ctx, o, "tx-1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	fake.Lock()
	n := len(fake.hits)
	fake.Unlock()
	if n != 1 {
		t.Errorf("the order was sent %d times", n)
	}
	var logged int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pixelgo_log WHERE platform = 'meta' AND event_id = $1 AND source = 'order' AND status = 'ok'`, pixelPurchaseEventID(o.ID)).Scan(&logged)
	if logged != 1 {
		t.Errorf("log rows for the order: %d", logged)
	}

	// The order-received page carries the Purchase, with the same id, once and without server forwarding.
	o, _ = srv.gooGetOrder(ctx, o.ID)
	rr := anonGet(t, srv.gooOrderReceivedURL(ctx, o))
	if cc := rr.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("the order page may be cached: %q", cc)
	}
	pc := pixelPageConfig(t, bodyOf(rr))
	pe := pixelEventNamed(pc, "Purchase")
	if pe == nil {
		t.Fatalf("no Purchase on the order-received page: %v", pc["events"])
	}
	if pe["id"] != pixelPurchaseEventID(o.ID) || pe["once"] != true || pe["skip_server"] != true || pe["p"].(map[string]any)["value"] != o.Total {
		t.Errorf("page Purchase: %v", pe)
	}
	if u, _ := pe["user"].(map[string]any); u["em"] != pixelHash("ann@example.test") {
		t.Errorf("advanced matching data: %v", pe["user"])
	}
	// Without the order key the page tells nothing about the order.
	bare := strings.Split(srv.gooOrderReceivedURL(ctx, o), "?")[0]
	if r := anonGet(t, bare+"?key=wc_order_wrong"); r.StatusCode == http.StatusOK {
		if pe := pixelEventNamed(pixelPageConfig(t, bodyOf(r)), "Purchase"); pe != nil {
			t.Error("a wrong order key still yields the order's Purchase")
		}
	}
	if r := anonGet(t, bare); r.StatusCode == http.StatusOK {
		if pe := pixelEventNamed(pixelPageConfig(t, bodyOf(r)), "Purchase"); pe != nil {
			t.Error("no order key still yields the order's Purchase")
		}
	}
}

func pixelJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// When the payment completes in the checkout request itself (a free order, the test gateway, a card gateway),
// pixelPaymentComplete sees the snapshot pixelOrderCreated just put on the same *gooOrder, not a copy reloaded
// from the database: the visitor's identifiers must still reach the platform.
func TestPixelPurchaseSameRequest(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	fake := pixelFakeAPIs(t)
	if err := srv.pixelActivate(ctx); err != nil {
		t.Fatal(err)
	}
	st := srv.pixelLoad()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{"123456789012345"}, Token: "META_TOKEN", ServerSide: true}
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, "Mug", 5, nil)
	o := gooTestOrder(t, srv, p, 1)
	delete(o.Meta, pixelOrderMeta)

	gin.SetMode(gin.TestMode)
	gc, _ := gin.CreateTestContext(httptest.NewRecorder())
	gc.Request = httptest.NewRequest("POST", "http://example.test/checkout", nil)
	gc.Request.AddCookie(&http.Cookie{Name: "_fbp", Value: "fb.1.1700000000.42"})
	srv.pixelOrderCreated(gc, o)
	if err := srv.gooPaymentComplete(ctx, o, "tx-same"); err != nil {
		t.Fatal(err)
	}
	var hit *pixelHit
	for i := 0; i < 50 && hit == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		hit = fake.find("/meta/123456789012345/events")
	}
	if hit == nil {
		t.Fatal("the Purchase never reached the Conversions API")
	}
	ud := hit.Body["data"].([]any)[0].(map[string]any)["user_data"].(map[string]any)
	if ud["fbp"] != "fb.1.1700000000.42" {
		t.Errorf("the visitor's _fbp was lost: %v", ud)
	}
	// Advanced matching is off here: no hashed e-mail, phone, name or address leaves the server.
	for _, k := range []string{"em", "ph", "fn", "ln", "ct", "st", "zp", "country", "external_id"} {
		if _, ok := ud[k]; ok {
			t.Errorf("user_data has %s without Advanced matching: %v", k, ud)
		}
	}
}

func TestPixelSnapshotTypes(t *testing.T) {
	if got := pixelSnapStrings([]string{"meta", ""}); len(got) != 1 || got[0] != "meta" {
		t.Errorf("[]string: %v", got)
	}
	if got := pixelSnapStrings([]any{"meta", 3}); len(got) != 1 || got[0] != "meta" {
		t.Errorf("[]any: %v", got)
	}
	if got := pixelSnapStringMap(map[string]string{"fbp": "x"}); got["fbp"] != "x" {
		t.Errorf("map[string]string: %v", got)
	}
	if got := pixelSnapStringMap(map[string]any{"fbp": "x", "n": 1}); got["fbp"] != "x" || len(got) != 1 {
		t.Errorf("map[string]any: %v", got)
	}
}
