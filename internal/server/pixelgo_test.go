package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPixelSanitize(t *testing.T) {
	st := pixelDefaults()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{" 123456789012345 ", "abc", "123456789012345", "987654321"}, Token: "EAAB token with spaces", ServerSide: true}
	st.Platforms["ga4"] = pixelPlatformCfg{Enabled: true, IDs: []string{"g-abc123xyz"}, Token: "secret_123", ServerSide: true}
	st.Platforms["tiktok"] = pixelPlatformCfg{Enabled: true, IDs: []string{"nope!"}}
	st.Platforms["gads"] = pixelPlatformCfg{Enabled: true, IDs: []string{"AW-1234567890"}, ServerSide: true, Labels: map[string]string{"Purchase": "abC-123_x", "Lead": "bad label"}}
	st.ScrollDepths, st.TimeSeconds = "50, 25;25,200,0", "30,abc,99999"
	st.DownloadExts = "PDF, .zip;Exe"
	st.ValueMode, st.ContentID = "weird", "weird"
	st.Custom = []pixelCustomEvent{
		{ID: "x", Name: "Quote Request", Enabled: true, Trigger: "click", Selector: "a.btn<script>", Value: "12.5"},
		{ID: "BAD ID", Name: "Quote Request 2", Enabled: true, Trigger: "click", Selector: "a.quote", Value: "12.5", Currency: "usd", Params: "plan=pro\nbad key=1\nsource=hero", Platforms: []string{"meta", "nope"}},
		{Name: "bad<name>", Trigger: "page_visit"},
		{Name: "Scrolled", Enabled: true, Trigger: "scroll", Number: 900, Value: "1e9"},
	}
	got := pixelSanitize(st)
	if m := got.Platforms["meta"]; strings.Join(m.IDs, ",") != "123456789012345,987654321" || m.Token != "" || m.ServerSide {
		t.Errorf("meta: %+v (spaces in a token must drop it, and server-side with it)", m)
	}
	if g := got.Platforms["ga4"]; strings.Join(g.IDs, ",") != "G-ABC123XYZ" || !g.ServerSide {
		t.Errorf("ga4: %+v", g)
	}
	if tt := got.Platforms["tiktok"]; tt.Enabled || len(tt.IDs) != 0 {
		t.Errorf("a platform with no valid ID stays off: %+v", tt)
	}
	if g := got.Platforms["gads"]; g.ServerSide || g.Labels["Purchase"] != "abC-123_x" || g.Labels["Lead"] != "" {
		t.Errorf("gads: %+v", g)
	}
	if got.ScrollDepths != "25,50" || got.TimeSeconds != "30" || got.DownloadExts != "pdf,zip,exe" {
		t.Errorf("lists: %q %q %q", got.ScrollDepths, got.TimeSeconds, got.DownloadExts)
	}
	if got.ValueMode != "total" || got.ContentID != "id" {
		t.Errorf("modes: %q %q", got.ValueMode, got.ContentID)
	}
	// The click event with the unusable selector and the one with the invalid name are dropped.
	if len(got.Custom) != 2 {
		t.Fatalf("custom events: %+v", got.Custom)
	}
	if ce := got.Custom[0]; ce.Name != "Quote Request 2" || ce.Selector != "a.quote" || ce.Currency != "USD" || ce.Value != "12.5" ||
		ce.Params != "plan=pro\nsource=hero" || len(ce.Platforms) != 1 || ce.Platforms[0] != "meta" || !pixelIDRe.MatchString(ce.ID) {
		t.Errorf("click event: %+v", ce)
	}
	if sc := got.Custom[1]; sc.Name != "Scrolled" || sc.Number != 100 || sc.Value != "" {
		t.Errorf("scroll event: %+v", sc)
	}
}

func TestPixelSnippets(t *testing.T) {
	st := pixelDefaults()
	for _, def := range pixelPlatforms {
		cfg := pixelPlatformCfg{Enabled: true, IDs: []string{map[string]string{"meta": "123456789012345", "ga4": "G-ABCD1234", "gads": "AW-1234567890",
			"tiktok": "C1ABCDEFGHIJ", "pinterest": "2612345678901", "bing": "12345678"}[def.Key]}}
		code := pixelSnippet(def, &st, cfg)
		if code == "" || !strings.Contains(code, "pixelgoReady."+def.Key+"=1") || !strings.Contains(code, cfg.IDs[0]) {
			t.Errorf("%s: snippet lacks its id or ready flag:\n%s", def.Key, code)
		}
		if n := strings.Count(strings.ToLower(code), "<script"); n != strings.Count(strings.ToLower(code), "</script>") {
			t.Errorf("%s: unbalanced script tags", def.Key)
		}
		if strings.Contains(code, ",}") || strings.Contains(code, "(o),}") {
			t.Errorf("%s: a trailing comma breaks the snippet:\n%s", def.Key, code)
		}
		inert := gooikeInert(code, "marketing")
		if regexp.MustCompile(`<script[^>]*\ssrc=`).MatchString(inert) || !strings.Contains(inert, `type="text/plain"`) {
			t.Errorf("%s: the snippet is not inert under consent:\n%s", def.Key, inert)
		}
	}
	// Even a hostile ID (it cannot pass the sanitizer, but the snippet must not rely on it) stays inside its string.
	meta := pixelPlatformByKey("meta")
	code := pixelSnippet(meta, &st, pixelPlatformCfg{IDs: []string{`1');alert(1);//</script>`}})
	if strings.Count(code, "</script>") != 1 || strings.Contains(code, `</script><`) || !strings.Contains(code, `fbq('init',"1');alert(1);//\u003c/script\u003e")`) {
		t.Errorf("an id escapes its string literal: %s", code)
	}
	st.General["page_view"] = false
	bing := pixelSnippet(pixelPlatformByKey("bing"), &st, pixelPlatformCfg{IDs: []string{"12345678"}})
	if strings.Contains(bing, "pageLoad") || !strings.Contains(bing, "new UET(o)}") {
		t.Errorf("bing without page views: %s", bing)
	}
}

func TestPixelEventMapAndParams(t *testing.T) {
	for ev, want := range map[string]map[string]string{
		"Purchase": {"meta": "Purchase", "ga4": "purchase", "tiktok": "CompletePayment", "pinterest": "checkout", "bing": "purchase"},
		"Lead":     {"ga4": "generate_lead", "tiktok": "SubmitForm"},
		"PageView": {"tiktok": "@page"},
	} {
		for plat, name := range want {
			if got, ok := pixelPlatformEvent(plat, ev); !ok || got != name {
				t.Errorf("%s on %s = %q, want %q", ev, plat, got, name)
			}
		}
	}
	if n, ok := pixelPlatformEvent("ga4", "Quote Request"); !ok || n != "quote_request" {
		t.Errorf("custom event on ga4: %q", n)
	}
	if n, _ := pixelPlatformEvent("pinterest", "Quote Request"); n != "custom" {
		t.Errorf("custom event on pinterest: %q", n)
	}
	if _, ok := pixelPlatformEvent("gads", "ViewCart"); ok {
		t.Error("ViewCart has no Google Ads equivalent")
	}
	in := map[string]any{"value": "19.90", "currency": "eur", "content_name": strings.Repeat("x", 500), "evil": "<b>", "num_items": float64(3),
		"items": []any{map[string]any{"id": "42", "name": "Mug", "price": 9.95, "quantity": float64(2)}, map[string]any{"name": "no id"}, "junk"},
		"extra": map[string]any{"plan": "pro", "bad key": "x", "n": float64(1), "obj": map[string]any{"a": 1}}}
	out := pixelCleanParams(in)
	if out["value"] != 19.9 || out["currency"] != "EUR" || len(out["content_name"].(string)) != 200 || out["evil"] != nil || out["num_items"] != float64(3) {
		t.Errorf("scalars: %v", out)
	}
	items, _ := out["items"].([]map[string]any)
	if len(items) != 1 || items[0]["id"] != "42" || items[0]["quantity"] != 2 {
		t.Errorf("items: %v", out["items"])
	}
	if ex, _ := out["extra"].(map[string]any); len(ex) != 2 || ex["plan"] != "pro" || ex["n"] != float64(1) {
		t.Errorf("extra: %v", out["extra"])
	}
}

func TestPixelHash(t *testing.T) {
	if pixelHash("  Test@Example.COM ") != pixelHash("test@example.com") || pixelHash("") != "" || len(pixelHash("a")) != 64 {
		t.Error("e-mail hashing is not normalized")
	}
	if pixelHashPhone("+90 (532) 123-45-67") != pixelHash("905321234567") {
		t.Error("phone hashing keeps digits only")
	}
	st := pixelDefaults()
	st.IDPrefix, st.IDSuffix = "p-", "-x"
	if pixelItemID(&st, 7, "SKU1") != "p-7-x" {
		t.Error("id mode")
	}
	st.ContentID = "sku"
	if pixelItemID(&st, 7, "SKU1") != "p-SKU1-x" || pixelItemID(&st, 7, "") != "p-7-x" {
		t.Error("sku mode falls back to the id")
	}
}

// pixelAPIs starts a local stand-in for the four server-side APIs and points the senders at it.
type pixelAPIs struct {
	sync.Mutex
	hits []pixelHit
}
type pixelHit struct {
	Path, Query, Auth, Token string
	Body                     map[string]any
}

func pixelFakeAPIs(t *testing.T) *pixelAPIs {
	t.Helper()
	f := &pixelAPIs{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		f.Lock()
		f.hits = append(f.hits, pixelHit{Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), Token: r.Header.Get("Access-Token"), Body: body})
		f.Unlock()
		if strings.Contains(r.URL.Path, "debug") {
			_, _ = w.Write([]byte(`{"validationMessages":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"events_received":1,"code":0}`))
	}))
	t.Cleanup(ts.Close)
	oldM, oldT, oldP, oldG, oldD := pixelMetaAPI, pixelTikTokAPI, pixelPinAPI, pixelGA4API, pixelGA4Debug
	pixelMetaAPI, pixelTikTokAPI, pixelPinAPI, pixelGA4API, pixelGA4Debug = ts.URL+"/meta/", ts.URL+"/tiktok", ts.URL+"/pin/", ts.URL+"/ga/collect", ts.URL+"/ga/debug"
	t.Cleanup(func() {
		pixelMetaAPI, pixelTikTokAPI, pixelPinAPI, pixelGA4API, pixelGA4Debug = oldM, oldT, oldP, oldG, oldD
	})
	return f
}

func (f *pixelAPIs) find(prefix string) *pixelHit {
	f.Lock()
	defer f.Unlock()
	for i := range f.hits {
		if strings.HasPrefix(f.hits[i].Path, prefix) {
			return &f.hits[i]
		}
	}
	return nil
}

func TestPixelSenders(t *testing.T) {
	f := pixelFakeAPIs(t)
	ctx := context.Background()
	ev := pixelServerEvent{Name: "Purchase", ID: "purchase_77", Source: "order", Time: time.Unix(1700000000, 0), IP: "203.0.113.9", UA: "UA/1.0", URL: "https://shop.example/thanks",
		Cookies: map[string]string{"fbp": "fb.1.1.2", "fbc": "fb.1.1.abc", "ttp": "ttp-1", "ttclid": "tt-click", "epik": "pin-click", "ga": "111.222"},
		User:    map[string]string{"em": pixelHash("a@b.co"), "ph": pixelHashPhone("+1 555 0100")},
		Params: map[string]any{"value": 25.5, "currency": "USD", "order_id": "77", "num_items": 2.0, "tax": 1.5, "shipping": 4.0,
			"items": []map[string]any{{"id": "42", "name": "Mug", "price": 12.75, "quantity": 2}}}}

	if st, code, msg := (&Server{}).pixelDeliver(ctx, "meta", pixelPlatformCfg{IDs: []string{"123456789012345"}, Token: "META_TOKEN", TestCode: "TEST1"}, ev); st != "ok" || code != 200 {
		t.Fatalf("meta: %s %d %s", st, code, msg)
	}
	h := f.find("/meta/123456789012345/events")
	if h == nil {
		t.Fatal("no Meta request")
	}
	if strings.Contains(h.Query, "META_TOKEN") || h.Body["access_token"] != "META_TOKEN" || h.Body["test_event_code"] != "TEST1" {
		t.Errorf("meta token placement: query %q body %v", h.Query, h.Body)
	}
	d := h.Body["data"].([]any)[0].(map[string]any)
	ud, cd := d["user_data"].(map[string]any), d["custom_data"].(map[string]any)
	if d["event_name"] != "Purchase" || d["event_id"] != "purchase_77" || d["action_source"] != "website" || d["event_time"] != float64(1700000000) ||
		ud["fbp"] != "fb.1.1.2" || ud["client_ip_address"] != "203.0.113.9" || ud["em"].([]any)[0] != pixelHash("a@b.co") ||
		cd["value"] != 25.5 || cd["currency"] != "USD" || cd["order_id"] != "77" || cd["content_ids"].([]any)[0] != "42" {
		t.Errorf("meta payload: %v", d)
	}

	if st, _, msg := (&Server{}).pixelDeliver(ctx, "tiktok", pixelPlatformCfg{IDs: []string{"C1ABCDEFGHIJ"}, Token: "TT_TOKEN"}, ev); st != "ok" {
		t.Fatalf("tiktok: %s %s", st, msg)
	}
	h = f.find("/tiktok")
	td := h.Body["data"].([]any)[0].(map[string]any)
	if h.Token != "TT_TOKEN" || h.Body["event_source_id"] != "C1ABCDEFGHIJ" || td["event"] != "CompletePayment" || td["event_id"] != "purchase_77" ||
		td["user"].(map[string]any)["ttclid"] != "tt-click" || td["properties"].(map[string]any)["value"] != 25.5 {
		t.Errorf("tiktok: %v header %q", h.Body, h.Token)
	}

	if st, _, msg := (&Server{}).pixelDeliver(ctx, "pinterest", pixelPlatformCfg{IDs: []string{"2612345678901"}, Token: "PIN_TOKEN", AccountID: "5551234"}, ev); st != "ok" {
		t.Fatalf("pinterest: %s %s", st, msg)
	}
	h = f.find("/pin/5551234/events")
	pd := h.Body["data"].([]any)[0].(map[string]any)
	if h.Auth != "Bearer PIN_TOKEN" || pd["event_name"] != "checkout" || pd["event_id"] != "purchase_77" || pd["custom_data"].(map[string]any)["value"] != "25.50" ||
		pd["user_data"].(map[string]any)["click_id"] != "pin-click" {
		t.Errorf("pinterest: %v auth %q", h.Body, h.Auth)
	}
	if st, _, _ := (&Server{}).pixelDeliver(ctx, "pinterest", pixelPlatformCfg{IDs: []string{"2612345678901"}, Token: "x"}, ev); st != "error" {
		t.Error("pinterest without an ad account must fail clearly")
	}

	if st, _, msg := (&Server{}).pixelDeliver(ctx, "ga4", pixelPlatformCfg{IDs: []string{"G-ABCD1234"}, Token: "GA_SECRET"}, ev); st != "ok" {
		t.Fatalf("ga4: %s %s", st, msg)
	}
	h = f.find("/ga/collect")
	if !strings.Contains(h.Query, "measurement_id=G-ABCD1234") || h.Body["client_id"] != "111.222" {
		t.Errorf("ga4: %q %v", h.Query, h.Body)
	}
	ge := h.Body["events"].([]any)[0].(map[string]any)
	if ge["name"] != "purchase" || ge["params"].(map[string]any)["transaction_id"] != "77" {
		t.Errorf("ga4 event: %v", ge)
	}
	view := ev
	view.Name = "ViewContent"
	if st, _, _ := (&Server{}).pixelDeliver(ctx, "ga4", pixelPlatformCfg{IDs: []string{"G-ABCD1234"}, Token: "x"}, view); st != "skipped" {
		t.Error("GA4 gets purchases only: the Measurement Protocol cannot deduplicate the rest")
	}
}

func TestPixelNetErrorHidesSecret(t *testing.T) {
	_, err := pixelSendGA4Err()
	if err == "" || strings.Contains(err, "api_secret") || strings.Contains(err, "SECRET") {
		t.Errorf("a network error leaks the request URL: %q", err)
	}
}

func pixelSendGA4Err() (int, string) {
	old := pixelGA4API
	pixelGA4API = "http://127.0.0.1:1/mp/collect"
	defer func() { pixelGA4API = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := pixelSendGA4(ctx, pixelPlatformCfg{IDs: []string{"G-ABCD1234"}, Token: "SECRET"}, "purchase", pixelServerEvent{Params: map[string]any{}}, false)
	if err == nil {
		return 0, ""
	}
	return 0, pixelNetErr(err)
}

// pixelSite installs a test site and activates Pursae Pixel through the Plugins screen.
func pixelSite(t *testing.T) (string, *http.Client, *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=pixelgo[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for Pursae Pixel on the Plugins screen")
	}
	if code, _, loc := get(t, c, ts.URL+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "activate=true") {
		t.Fatalf("activate Pursae Pixel: %d %s", code, loc)
	}
	if !srv.pixelOn() {
		t.Fatal("Pursae Pixel is not active after activation")
	}
	return ts.URL, c, srv
}

func pixelEventPost(t *testing.T, base string, body map[string]any, cookies ...*http.Cookie) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/pixelgo/v1/event", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestPixelPluginLifecycle(t *testing.T) {
	base, c, srv := pixelSite(t)
	ctx := context.Background()
	fake := pixelFakeAPIs(t)

	// Nothing is printed until a platform is configured.
	if b := bodyOf(anonGet(t, base+"/")); strings.Contains(b, "pixelgo") {
		t.Error("Pursae Pixel prints something without a configured platform")
	}
	var jobs int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = $1`, pixelPurgeJob).Scan(&jobs)
	if jobs != 1 {
		t.Errorf("purge job scheduled %d times", jobs)
	}

	st := srv.pixelLoad()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{"123456789012345"}, Token: "META_TOKEN", ServerSide: true}
	st.Platforms["ga4"] = pixelPlatformCfg{Enabled: true, IDs: []string{"G-ABCD1234"}}
	st.Custom = []pixelCustomEvent{{ID: "q1", Name: "Quote Request", Enabled: true, Trigger: "click", Selector: "a.quote", Params: "plan=pro", Platforms: []string{"meta"}}}
	if err := srv.pixelSave(ctx, st); err != nil {
		t.Fatal(err)
	}

	// Pursae Cookie off: the tags are plain, the config and script are there, the page is the same for everyone.
	page := bodyOf(anonGet(t, base+"/"))
	for _, want := range []string{`fbq('init',"123456789012345")`, `gtag('config',"G-ABCD1234",{send_page_view:false})`, `pixelgoReady.meta=1`,
		`<script type="application/json" id="pixelgo-config">`, `/wp-content/plugins/pixelgo/assets/pixelgo.js`, `"name":"PageView"`, `"name":"Quote Request"`} {
		if !strings.Contains(page, want) {
			t.Errorf("home page lacks %s", want)
		}
	}
	for _, leak := range []string{"META_TOKEN"} {
		if strings.Contains(page, leak) {
			t.Errorf("the page leaks %s", leak)
		}
	}
	if code := anonGet(t, base+"/wp-content/plugins/pixelgo/assets/pixelgo.js").StatusCode; code != http.StatusOK {
		t.Errorf("pixelgo.js: %d", code)
	}
	cfg := regexp.MustCompile(`(?s)<script type="application/json" id="pixelgo-config">(.*?)</script>`).FindStringSubmatch(page)
	var parsed map[string]any
	if cfg == nil || json.Unmarshal([]byte(cfg[1]), &parsed) != nil {
		t.Fatal("the page config is not valid JSON")
	}
	if cats := parsed["consent"].(map[string]any)["cats"].(map[string]any); cats["ga4"] != "statistics" || cats["meta"] != "marketing" {
		t.Errorf("consent categories: %v", cats)
	}
	if plats := parsed["platforms"].(map[string]any); plats["meta"].(map[string]any)["server"] != true || plats["ga4"].(map[string]any)["server"] != false {
		t.Errorf("platform flags: %v", plats)
	}

	// Pursae Cookie on: the same tags, inert in their consent category.
	if err := srv.gooikeActivate(ctx); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	page = bodyOf(anonGet(t, base+"/"))
	if !strings.Contains(page, `type="text/plain" data-gooike-category="marketing"`) || !strings.Contains(page, `data-gooike-category="statistics"`) {
		t.Error("tags are not inert under Pursae Cookie")
	}
	if regexp.MustCompile(`<script async src="https://www.googletagmanager.com`).MatchString(page) || strings.Contains(page, `<script>!function(f,b,e,v,n,t,s)`) {
		t.Error("a tag loads before consent")
	}

	// Server-side event route: consent first.
	ev := map[string]any{"name": "AddToCart", "event_id": "evt-12345678", "p": map[string]any{"value": 10, "currency": "usd", "items": []any{map[string]any{"id": "5", "quantity": 1, "price": 10}}}}
	if code, body := pixelEventPost(t, base, ev); code != http.StatusAccepted || !strings.Contains(body, `"queued":0`) {
		t.Errorf("an event without consent must not be forwarded: %d %s", code, body)
	}
	consent := &http.Cookie{Name: gooikeCookie, Value: url.QueryEscape(`{"v":1,"id":"6f1c2b9e-1d2a-4c3b-9e8f-0a1b2c3d4e5f","c":["necessary","marketing"],"t":1}`)}
	if code, body := pixelEventPost(t, base, ev, consent, &http.Cookie{Name: "_fbp", Value: "fb.1.2.3"}); code != http.StatusAccepted || !strings.Contains(body, `"queued":1`) {
		t.Errorf("a consented event is not queued: %d %s", code, body)
	}
	for _, bad := range []map[string]any{{"name": "Nope", "event_id": "evt-12345678"}, {"name": "AddToCart", "event_id": "x"}} {
		if code, _ := pixelEventPost(t, base, bad, consent); code != http.StatusBadRequest {
			t.Errorf("invalid event accepted: %v -> %d", bad, code)
		}
	}
	if code, body := pixelEventPost(t, base, map[string]any{"name": "Purchase", "event_id": "evt-12345678"}, consent); code != http.StatusAccepted || !strings.Contains(body, `"queued":0`) {
		t.Errorf("browser purchases are not forwarded (the order sends them): %d %s", code, body)
	}
	var hit *pixelHit
	for i := 0; i < 50 && hit == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		hit = fake.find("/meta/123456789012345/events")
	}
	if hit == nil {
		t.Fatal("the event never reached the Conversions API")
	}
	d := hit.Body["data"].([]any)[0].(map[string]any)
	if d["event_name"] != "AddToCart" || d["event_id"] != "evt-12345678" || d["user_data"].(map[string]any)["fbp"] != "fb.1.2.3" {
		t.Errorf("forwarded event: %v", d)
	}
	var status, msg string
	for i := 0; i < 50; i++ {
		_ = srv.store.Pool.QueryRow(ctx, `SELECT status, message FROM pixelgo_log WHERE platform = 'meta' AND event_id = 'evt-12345678'`).Scan(&status, &msg)
		if status != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != "ok" || strings.Contains(msg, "META_TOKEN") {
		t.Errorf("log row: %q %q", status, msg)
	}

	// Admin: dashboard, every screen, the saved token is never echoed.
	for _, p := range []string{"", "pixels", "events", "custom", "feed", "logs", "settings"} {
		code, body, _ := get(t, c, base+"/wp-admin/pixelgo?page="+p)
		if code != http.StatusOK || !strings.Contains(body, "Pursae Pixel") {
			t.Errorf("screen %q: %d", p, code)
		}
		if strings.Contains(body, "META_TOKEN") {
			t.Errorf("screen %q echoes a saved token", p)
		}
	}
	if code := anonGet(t, base+"/wp-admin/pixelgo").StatusCode; code == http.StatusOK {
		t.Error("the admin screen is open to visitors")
	}
	if code := anonGet(t, base+"/pixelgo-feed/whatever").StatusCode; code != http.StatusNotFound {
		t.Errorf("feed with a wrong token: %d", code)
	}

	// Deactivation: nothing on the site, routes are gone, settings stay.
	_, plugins, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=deactivate&amp;plugin=pixelgo[^"]*)"`).FindStringSubmatch(plugins)
	if m == nil {
		t.Fatal("no Deactivate link")
	}
	get(t, c, base+html.UnescapeString(m[1]))
	if srv.pixelOn() {
		t.Fatal("still active")
	}
	if b := bodyOf(anonGet(t, base+"/")); strings.Contains(b, "pixelgo") || strings.Contains(b, "fbq(") {
		t.Error("Pursae Pixel output remains after deactivation")
	}
	if code, _ := pixelEventPost(t, base, ev, consent); code != http.StatusNotFound {
		t.Errorf("the event route answers %d while the plugin is off", code)
	}
	if srv.pixelLoad().Platforms["meta"].Token != "META_TOKEN" {
		t.Error("settings were lost on deactivation")
	}
}
