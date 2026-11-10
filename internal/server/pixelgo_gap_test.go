package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/web"
)

func TestPixelFbcKeepsTheClickTime(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	if got := pixelFbc("1700000000123", "abc", now); got != "fb.1.1700000000123.abc" {
		t.Errorf("recorded click time not used: %s", got)
	}
	for _, bad := range []string{"", "x", "5", "1900000000000"} { // missing, malformed, too small, in the future
		if got := pixelFbc(bad, "abc", now); got != "fb.1.1800000000000.abc" {
			t.Errorf("%q: %s", bad, got)
		}
	}
	// Two requests with the same cookie give the same fbc.
	a, b := pixelFbc("1700000000123", "abc", now), pixelFbc("1700000000123", "abc", now.Add(time.Hour))
	if a != b {
		t.Errorf("fbc changes between requests: %s %s", a, b)
	}
}

func TestPixelDeliverRetriesTransientFailures(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			_, _ = w.Write([]byte(`{"events_received":1}`))
		}
	}))
	defer ts.Close()
	oldM, oldD := pixelMetaAPI, pixelRetryDelays
	pixelMetaAPI, pixelRetryDelays = ts.URL+"/meta/", []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { pixelMetaAPI, pixelRetryDelays = oldM, oldD }()
	ev := pixelServerEvent{Name: "Purchase", ID: "purchase_1", Source: "order", Time: time.Now(), Params: map[string]any{"value": 1.0, "currency": "USD"},
		Cookies: map[string]string{}, User: map[string]string{}}
	cfg := pixelPlatformCfg{IDs: []string{"123456789012345"}, Token: "T"}
	if st, code, msg := (&Server{}).pixelDeliverRetry("meta", cfg, ev); st != "ok" || code != 200 || calls.Load() != 3 {
		t.Errorf("503, 429, then 200: %s %d %s after %d calls", st, code, msg, calls.Load())
	}
	// A client error is final: no retry.
	calls.Store(0)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusBadRequest) }))
	defer bad.Close()
	pixelMetaAPI = bad.URL + "/meta/"
	if st, code, _ := (&Server{}).pixelDeliverRetry("meta", cfg, ev); st != "error" || code != 400 || calls.Load() != 1 {
		t.Errorf("400: %s %d after %d calls", st, code, calls.Load())
	}
	// A test event is never retried.
	calls.Store(0)
	pixelMetaAPI = "http://127.0.0.1:1/meta/"
	ev.Test = true
	if st, _, _ := (&Server{}).pixelDeliverRetry("meta", cfg, ev); st != "error" {
		t.Errorf("unreachable: %s", st)
	}
}

func pixelWaitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestPixelPurchaseIsMarkedOnlyAfterSuccess: a Purchase the platform never accepted is not marked as
// sent, can be retried (admin Retry / the hourly sweep use pixelRetryOrder), and is marked per platform
// once accepted.
func TestPixelPurchaseIsMarkedOnlyAfterSuccess(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	var up atomic.Bool
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		if !up.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"events_received":1}`))
	}))
	defer ts.Close()
	oldM, oldD := pixelMetaAPI, pixelRetryDelays
	pixelMetaAPI, pixelRetryDelays = ts.URL+"/meta/", []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { pixelMetaAPI, pixelRetryDelays = oldM, oldD }()
	if err := srv.pixelActivate(ctx); err != nil {
		t.Fatal(err)
	}
	st := srv.pixelLoad()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{"123456789012345"}, Token: "META_TOKEN", ServerSide: true}
	st.RequireConsent = false
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, "Teapot", 10, nil)
	o := gooTestOrder(t, srv, p, 1)
	if err := srv.gooPaymentComplete(ctx, o, "tx-1"); err != nil {
		t.Fatal(err)
	}
	var logged int
	pixelWaitFor(t, "the failure to be logged", func() bool {
		_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pixelgo_log WHERE status = 'error' AND event_id = $1 AND message LIKE 'After 3 attempts%'`, pixelPurchaseEventID(o.ID)).Scan(&logged)
		return logged == 1
	})
	if calls.Load() != 3 {
		t.Errorf("%d attempts, want 3", calls.Load())
	}
	fresh, _ := srv.gooGetOrder(ctx, o.ID)
	if all, sent := pixelSentSet(fresh); all || sent["meta"] {
		t.Fatalf("a failed Purchase is marked as sent: %v", fresh.Meta[pixelSentMeta])
	}

	// The platform recovers; Retry sends it and marks the platform.
	up.Store(true)
	if !srv.pixelRetryOrder(ctx, fresh) {
		t.Fatal("nothing to retry")
	}
	pixelWaitFor(t, "the Purchase to be marked", func() bool {
		f, _ := srv.gooGetOrder(ctx, o.ID)
		_, sent := pixelSentSet(f)
		return sent["meta"]
	})
	again, _ := srv.gooGetOrder(ctx, o.ID)
	if srv.pixelRetryOrder(ctx, again) {
		t.Error("a delivered Purchase is retried")
	}
	n := calls.Load()
	if err := srv.gooPaymentComplete(ctx, again, "tx-1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != n {
		t.Error("a second payment_complete sent the Purchase again")
	}
	// The log offers Retry for failed order Purchases only.
	row := pixelLogRow{Event: "Purchase", Source: "order", Stat: "error", EventID: pixelPurchaseEventID(o.ID)}
	if !row.CanRetry() || row.OrderID() != o.ID {
		t.Errorf("retry row: %v %d", row.CanRetry(), row.OrderID())
	}
	if (pixelLogRow{Event: "ViewContent", Source: "browser", Stat: "error", EventID: "abc"}).CanRetry() {
		t.Error("a browser event offers Retry")
	}
}

// TestPixelRefundGoesToGA4: a refund of an order GA4 recorded sends the GA4 refund event with the
// transaction id; an order GA4 never saw sends nothing.
func TestPixelRefundGoesToGA4(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	fake := pixelFakeAPIs(t)
	if err := srv.pixelActivate(ctx); err != nil {
		t.Fatal(err)
	}
	st := srv.pixelLoad()
	st.Platforms["ga4"] = pixelPlatformCfg{Enabled: true, IDs: []string{"G-ABCD1234"}, Token: "GA_SECRET", ServerSide: true}
	st.RequireConsent = false
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, "Teapot", 10, nil)
	o := gooTestOrder(t, srv, p, 2)
	if err := srv.gooPaymentComplete(ctx, o, "tx-1"); err != nil {
		t.Fatal(err)
	}
	pixelWaitFor(t, "GA4 to be marked", func() bool {
		f, _ := srv.gooGetOrder(ctx, o.ID)
		_, sent := pixelSentSet(f)
		return sent["ga4"]
	})
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if _, err := srv.gooCreateRefund(ctx, o, 5, "damaged", nil, false, false, 1); err != nil {
		t.Fatal(err)
	}
	refund := func() map[string]any {
		fake.Lock()
		defer fake.Unlock()
		for _, h := range fake.hits {
			if evs, _ := h.Body["events"].([]any); strings.HasPrefix(h.Path, "/ga/collect") && len(evs) == 1 && evs[0].(map[string]any)["name"] == "refund" {
				return evs[0].(map[string]any)["params"].(map[string]any)
			}
		}
		return nil
	}
	pixelWaitFor(t, "the refund event", func() bool { return refund() != nil })
	prm := refund()
	if prm["transaction_id"] != strconv.FormatInt(o.ID, 10) || prm["value"] != 5.0 || prm["currency"] == "" {
		t.Errorf("refund params: %v", prm)
	}

	// An order GA4 never recorded is not refunded there.
	o2 := gooTestOrder(t, srv, p, 1)
	if _, err := srv.gooCreateRefund(ctx, o2, 3, "", nil, false, false, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	fake.Lock()
	defer fake.Unlock()
	for _, h := range fake.hits {
		raw, _ := json.Marshal(h.Body)
		if strings.Contains(string(raw), `"transaction_id":"`+strconv.FormatInt(o2.ID, 10)+`"`) {
			t.Errorf("an order GA4 never saw was refunded: %s", raw)
		}
	}
}

// TestPixelRefundIsNotABrowserEvent: the browser cannot forge the server-only refund event.
func TestPixelRefundIsNotABrowserEvent(t *testing.T) {
	if _, ok := pixelPlatformEvent("meta", "Refund"); ok {
		t.Error("Meta has no refund event")
	}
	if n, ok := pixelPlatformEvent("ga4", "Refund"); !ok || n != "refund" {
		t.Errorf("ga4: %q", n)
	}
	if _, ok := pixelPlatformEvent("gtm", "Refund"); ok {
		t.Error("the data layer is a browser platform: no refunds")
	}
}

func TestPixelGTMDataLayer(t *testing.T) {
	st := pixelDefaults()
	def := pixelPlatformByKey("gtm")
	if def == nil || def.TokenLabel != "" {
		t.Fatal("no browser-only gtm platform")
	}
	code := pixelSnippet(def, &st, pixelPlatformCfg{Enabled: true, IDs: []string{"GTM-ABC1234"}})
	if !strings.Contains(code, "gtm.js?id=") || !strings.Contains(code, `"GTM-ABC1234"`) || !strings.Contains(code, "window.dataLayer=window.dataLayer||[]") {
		t.Errorf("gtm snippet: %s", code)
	}
	if got := pixelSanitize(pixelSettings{Platforms: map[string]pixelPlatformCfg{"gtm": {Enabled: true, IDs: []string{"gtm-abc1234", "bad id"}}}}); len(got.Platforms["gtm"].IDs) != 1 || got.Platforms["gtm"].IDs[0] != "GTM-ABC1234" {
		t.Errorf("gtm ids: %v", got.Platforms["gtm"].IDs)
	}
	for ev, want := range map[string]string{"Purchase": "purchase", "AddToCart": "add_to_cart", "ViewContent": "view_item", "Quote Request": "quote_request"} {
		if n, ok := pixelPlatformEvent("gtm", ev); !ok || n != want {
			t.Errorf("%s on gtm = %q", ev, n)
		}
	}
	raw, err := fs.ReadFile(web.FS(""), "pixelgo/pixelgo.js")
	if js := string(raw); err != nil || !strings.Contains(js, "gtm: function") || !strings.Contains(js, "ecommerce: null") || !strings.Contains(js, "fbts") {
		t.Errorf("pixelgo.js has no data layer sender: %v", err)
	}
}

// TestPixelFeedVariationsAndPaging: variations are items with item_group_id when the option is on, the
// variable product is then not listed itself, and the feed has no 10,000 cut.
func TestPixelFeedVariationsAndPaging(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.pixelActivate(ctx); err != nil {
		t.Fatal(err)
	}
	st := srv.pixelLoad()
	st.FeedEnabled, st.FeedToken, st.ContentID = true, "feedtoken1234567890abcdef", "sku"
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	hoodie := gooNewProduct(gooTypeVariable)
	hoodie.Name, hoodie.SKU = "Hoodie", "HOOD"
	hoodie.Attributes = []gooProductAttr{{Name: "Size", Options: []string{"S", "M"}, Visible: true, Variation: true}}
	if err := srv.gooSaveProduct(ctx, hoodie, 1); err != nil {
		t.Fatal(err)
	}
	for i, size := range []string{"S", "M"} {
		v := srv.gooNewVariation(ctx, hoodie, map[string]string{hoodie.Attributes[0].Key(): size}, i)
		v.RegularPrice, v.SKU = gooFloatPtr(float64(30+i)), "HOOD-"+size
		if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
			t.Fatal(err)
		}
	}
	gooMakeSimple(t, srv, "Mug", 12, func(p *gooProduct) { p.SKU = "MUG" })
	feed := func() string {
		resp := anonGet(t, base+"/pixelgo-feed/"+st.FeedToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("feed: %d", resp.StatusCode)
		}
		return bodyOf(resp)
	}
	off := feed()
	if strings.Contains(off, "item_group_id") || !strings.Contains(off, "<g:id>HOOD</g:id>") || !strings.Contains(off, "<g:id>MUG</g:id>") {
		t.Errorf("feed without variations:\n%s", off)
	}
	st.VariationIDs = true
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	on := feed()
	for _, want := range []string{"<g:id>HOOD-S</g:id>", "<g:id>HOOD-M</g:id>", "<g:item_group_id>HOOD</g:item_group_id>", "<g:size>S</g:size>", "<g:id>MUG</g:id>"} {
		if !strings.Contains(on, want) {
			t.Errorf("feed with variations lacks %s:\n%s", want, on)
		}
	}
	if strings.Contains(on, "<g:id>HOOD</g:id>") {
		t.Error("the variable product is listed next to its variations")
	}
	if strings.Count(on, "<item>") != 3 || !strings.HasSuffix(strings.TrimSpace(on), "</rss>") {
		t.Errorf("feed shape: %d items", strings.Count(on, "<item>"))
	}
	if src, _ := os.ReadFile("pixelgo_feed.go"); strings.Contains(string(src), "LIMIT 10000") {
		t.Error("the 10,000 products cut is back")
	}
}

// TestPixelWithholdsTagsWithoutConsentManager: "Require consent" on and Pursae Cookie off prints nothing
// (safe default); switching the setting off, or activating Pursae Cookie, prints the tags.
func TestPixelWithholdsTagsWithoutConsentManager(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.pixelActivate(ctx); err != nil {
		t.Fatal(err)
	}
	st := srv.pixelLoad()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{"123456789012345"}}
	st.RequireConsent = true
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := srv.settings.Set(ctx, map[string]any{gooikeSetting: false}); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.pixelFront(c, nil); ok {
		t.Error("tags printed with consent required and no consent manager")
	}
	if err := srv.settings.Set(ctx, map[string]any{gooikeSetting: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.pixelFront(c, nil); !ok {
		t.Error("tags withheld although Pursae Cookie is active")
	}
	if err := srv.settings.Set(ctx, map[string]any{gooikeSetting: false}); err != nil {
		t.Fatal(err)
	}
	st.RequireConsent = false
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.pixelFront(c, nil); !ok {
		t.Error("tags withheld although consent is not required")
	}
}

// TestPixelUETAndEnhancedConversionsMarkup: every UET tag ID gets an instance, Google Ads turns on
// enhanced conversions and pixelgo.js sets the hashed user data and listens to YouTube and Vimeo.
func TestPixelUETAndEnhancedConversionsMarkup(t *testing.T) {
	st := pixelDefaults()
	st.General["page_view"] = true
	bing := pixelSnippet(pixelPlatformByKey("bing"), &st, pixelPlatformCfg{IDs: []string{"12345678", "87654321"}})
	if !strings.Contains(bing, `ids=["12345678","87654321"]`) || !strings.Contains(bing, `x.push("pageLoad")`) {
		t.Errorf("uet snippet: %s", bing)
	}
	gads := pixelSnippet(pixelPlatformByKey("gads"), &st, pixelPlatformCfg{IDs: []string{"AW-123456789"}})
	if !strings.Contains(gads, "allow_enhanced_conversions:true") {
		t.Errorf("gads snippet: %s", gads)
	}
	raw, err := fs.ReadFile(web.FS(""), "pixelgo/pixelgo.js")
	js := string(raw)
	for _, want := range []string{"sha256_email_address", "'user_data'", "pixelgoUET", "enablejsapi=", "player\\.vimeo\\.com", "onStateChange"} {
		if err != nil || !strings.Contains(js, want) {
			t.Errorf("pixelgo.js lacks %q (%v)", want, err)
		}
	}
}

// TestPixelgoJSBehaviour runs pixelgo.js against a DOM stub (node): Google Ads enhanced conversions data,
// every UET instance, and YouTube/Vimeo player messages becoming VideoPlay events.
func TestPixelgoJSBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	raw, err := exec.Command(node, "testdata/pixelgo_harness.js", "../../web/pixelgo/pixelgo.js").Output()
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	var out struct {
		Gtag      [][]any  `json:"gtag"`
		UET       [][]any  `json:"uet"`
		Posts     []string `json:"posts"`
		IframeSrc string   `json:"iframeSrc"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var set, conv int
	for i, g := range out.Gtag {
		if len(g) >= 3 && g[0] == "set" && g[1] == "user_data" {
			set++
			ud, _ := g[2].(map[string]any)
			if ud["sha256_email_address"] != "e1" || ud["sha256_phone_number"] != "p1" {
				t.Errorf("user_data: %v", ud)
			}
			if i+1 >= len(out.Gtag) || out.Gtag[i+1][1] != "conversion" {
				t.Errorf("user_data must be set right before the conversion: %v", out.Gtag)
			}
		}
		if len(g) >= 2 && g[1] == "conversion" {
			conv++
		}
	}
	if set != 1 || conv != 1 {
		t.Errorf("user_data sets %d, conversions %d: %v", set, conv, out.Gtag)
	}
	inst := map[string]int{}
	videos := 0
	for _, u := range out.UET {
		inst[u[0].(string)]++
		if u[2] == "VideoPlay" {
			videos++
		}
	}
	if inst["a"] != inst["b"] || inst["a"] == 0 || videos != 4 {
		t.Errorf("uet instances %v, video events %d: %v", inst, videos, out.UET)
	}
	if !strings.Contains(out.IframeSrc, "enablejsapi=1") || len(out.Posts) < 4 {
		t.Errorf("embeds not hooked: %q %v", out.IframeSrc, out.Posts)
	}
}

// TestPixelAdminExportKeepsTokensOut: the settings export carries no token, and importing it keeps the
// stored ones.
func TestPixelAdminExportKeepsTokensOut(t *testing.T) {
	base, c, srv := pixelSite(t)
	ctx := context.Background()
	st := srv.pixelLoad()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{"123456789012345"}, Token: "SECRET_META_TOKEN", ServerSide: true}
	st.FeedToken = "feedtokenvalue123"
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	_, page, _ := get(t, c, base+"/wp-admin/pixelgo?page=settings")
	nonce := formNonce(t, page)
	code, body, _ := get(t, c, base+"/wp-admin/pixelgo?action=export&_wpnonce="+nonce)
	if code != http.StatusOK || strings.Contains(body, "SECRET_META_TOKEN") || strings.Contains(body, "feedtokenvalue123") {
		t.Fatalf("export leaks a token or failed: %d %s", code, body)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/pixelgo?action=export"); code == http.StatusOK {
		t.Error("export without a nonce was served")
	}
	post(t, c, base+"/wp-admin/pixelgo", url.Values{"_wpnonce": {nonce}, "page": {"settings"}, "do": {"import"}, "json": {body}})
	got := srv.pixelLoad()
	if got.Platforms["meta"].Token != "SECRET_META_TOKEN" || got.FeedToken != "feedtokenvalue123" || len(got.Platforms["meta"].IDs) != 1 {
		t.Errorf("import lost the stored tokens: %+v", got.Platforms["meta"])
	}
}
