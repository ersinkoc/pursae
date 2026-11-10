package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// Server-side delivery: Meta Conversions API, TikTok Events API, Pinterest Conversions API and the GA4
// Measurement Protocol.
//
// Two sources feed it:
//   - pixelgo.js forwards each browser event to POST /wp-json/pixelgo/v1/event with the same event id the
//     browser pixel used, so Meta, TikTok and Pinterest deduplicate the pair. Purchases are not
//     forwarded: they come from the order below.
//   - a completed payment (payment_complete) sends the Purchase of the order with the id
//     purchase_<order id>, the same id the order-received page uses. It works when the customer never
//     returns to the shop (off-site payments), which is where server-side tracking earns its keep.
//
// GA4's Measurement Protocol has no event-id deduplication, so it gets Purchase only (deduplicated by
// transaction_id). Hosts are fixed, redirects are not followed, tokens go in headers or the request body
// (Google asks for the API secret in the query; its URL is never logged) and are never shown again.

// The endpoints are variables so tests can point them at a local server.
var (
	pixelMetaAPI   = "https://graph.facebook.com/v21.0/"
	pixelTikTokAPI = "https://business-api.tiktok.com/open_api/v1.3/event/track/"
	pixelPinAPI    = "https://api.pinterest.com/v5/ad_accounts/"
	pixelGA4API    = "https://www.google-analytics.com/mp/collect"
	pixelGA4Debug  = "https://www.google-analytics.com/debug/mp/collect"
)

const (
	pixelOrderMeta = "_pixelgo"      // order meta: the visitor snapshot taken at checkout
	pixelSentMeta  = "_pixelgo_sent" // order meta: platform → time the platform accepted the Purchase
)

// pixelRetryDelays are the waits before the 2nd, 3rd and 4th delivery attempt of an event whose platform
// answered 429, 5xx or not at all. A variable so tests can shorten it.
var pixelRetryDelays = []time.Duration{2 * time.Second, 15 * time.Second, 90 * time.Second}

// pixelInflight holds "<order id>:<platform>" of the Purchases being delivered right now, so a second
// payment_complete (a webhook retry) does not send the same Purchase again before the first is marked.
var (
	pixelInflight sync.Map
	pixelSentMu   sync.Mutex
)

// pixelRetryable: a failure that may pass by itself (network error, rate limit, server error).
func pixelRetryable(status string, code int) bool {
	return status == "error" && (code == 0 || code == http.StatusTooManyRequests || code >= 500)
}

// pixelSentSet reads the order meta of the platforms that already accepted the Purchase. The first
// version stored one timestamp string for all platforms; it counts as "all sent".
func pixelSentSet(o *gooOrder) (all bool, set map[string]bool) {
	set = map[string]bool{}
	switch v := o.Meta[pixelSentMeta].(type) {
	case string:
		return v != "", set
	case map[string]any:
		for k := range v {
			set[k] = true
		}
	case map[string]string:
		for k := range v {
			set[k] = true
		}
	}
	return false, set
}

// pixelMarkSent records that a platform accepted the Purchase of an order, in a fresh copy of the order.
func (s *Server) pixelMarkSent(ctx context.Context, orderID int64, platform string) {
	pixelSentMu.Lock()
	defer pixelSentMu.Unlock()
	o, err := s.gooGetOrder(ctx, orderID)
	if err != nil || o == nil {
		return
	}
	if o.Meta == nil {
		o.Meta = map[string]any{}
	}
	m := map[string]any{}
	switch v := o.Meta[pixelSentMeta].(type) {
	case map[string]any:
		m = v
	case string:
		return // legacy: everything is sent
	}
	m[platform] = time.Now().UTC().Format(time.RFC3339)
	o.Meta[pixelSentMeta] = m
	_ = s.gooSaveOrder(ctx, o, false)
}

var pixelHTTP = &http.Client{
	Timeout:       8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// pixelSlots bounds concurrent deliveries.
var pixelSlots = make(chan struct{}, 8)

// pixelQueueMax bounds the deliveries waiting for a slot. When the platforms answer slowly, browser
// events (anonymous, up to 150 a minute per address) would otherwise pile up as goroutines without
// limit; past it they are dropped. Purchases from orders are always queued.
const pixelQueueMax = 512

var (
	pixelQueued  atomic.Int64 // deliveries started and not finished
	pixelDropped atomic.Int64
)

// pixelAdmit reserves a place in the delivery queue for an event from source; the caller releases
// it with pixelQueued.Add(-1) once the delivery ends.
func pixelAdmit(source string) bool {
	if n := pixelQueued.Add(1); n > pixelQueueMax && source != "order" {
		pixelQueued.Add(-1)
		if d := pixelDropped.Add(1); d == 1 || d%1000 == 0 {
			slog.Warn("pixelgo: delivery queue full, browser events dropped", "dropped", d)
		}
		return false
	}
	return true
}

var (
	pixelEventIDRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,64}$`)
	pixelKeyOK     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,39}$`)
	pixelPathRe    = regexp.MustCompile(`^/[^\s<>"']{0,500}$`)
)

// pixelServerEvent is one event for the server-side senders, in canonical form.
type pixelServerEvent struct {
	Name, ID, Source, URL string
	Params                map[string]any
	Time                  time.Time
	IP, UA                string
	Cookies               map[string]string // fbp, fbc, ttp, ttclid, epik, ga
	User                  map[string]string // hashed: em, ph, fn, ln, ct, st, zp, country, external_id
	Platforms             []string          // limit to these (empty: all that deliver server-side)
	Test                  bool
	OrderID               int64 // Source "order": the order whose Purchase this is; a platform's 2xx is recorded on it
}

func (s *Server) registerPixelREST() {
	s.rest.register(pixelNamespace, "/"+pixelNamespace+"/event", map[string]restHandler{http.MethodPost: s.pixelEventREST}, nil)
}

// pixelConsentCategories reads the Pursae Cookie consent cookie of a request: the accepted
// categories and whether the visitor has decided (a current policy version).
func (s *Server) pixelConsentCategories(c *gin.Context) ([]string, bool) {
	raw, err := c.Cookie(gooikeCookie)
	if err != nil || raw == "" {
		return nil, false
	}
	var v struct {
		V int      `json:"v"`
		C []string `json:"c"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil || v.V != s.gooikeLoad().PolicyVersion {
		return nil, false
	}
	return v.C, true
}

// pixelAllowed lists the platforms an event may be sent to for this request: consent decides when
// Pursae Cookie is the gate.
func (s *Server) pixelAllowed(c *gin.Context, st *pixelSettings, want []*pixelPlatformDef) []string {
	var cats []string
	gate := s.pixelConsentOn(st)
	if gate {
		cats, _ = s.pixelConsentCategories(c)
	}
	g := s.gooikeLoad()
	var out []string
	for _, d := range want {
		if !gate || pixelHas(cats, pixelCategory(&g, d)) {
			out = append(out, d.Key)
		}
	}
	return out
}

// pixelEventREST receives a browser event and delivers its server-side copy.
func (s *Server) pixelEventREST(c *gin.Context, r *restRequest) {
	c.Header("Cache-Control", "no-store")
	if o := c.GetHeader("Origin"); o != "" && hostOf(o) != "" && !strings.EqualFold(hostOf(o), hostOf("http://"+c.Request.Host)) {
		restErr(c, http.StatusForbidden, "pixelgo_origin", "Cross-site events are not accepted.")
		return
	}
	if !gooikeAllow("px:"+gooikeTruncateIP(clientIP(c)), 150) {
		restErr(c, http.StatusTooManyRequests, "pixelgo_rate_limited", "Too many events from this address.")
		return
	}
	st := s.pixelLoad()
	ready := st.serverReady()
	name, _ := r.Str(c, "name")
	id, _ := r.Str(c, "event_id")
	if !pixelEventIDRe.MatchString(id) {
		restErr(c, http.StatusBadRequest, "pixelgo_invalid_id", "Invalid event id.")
		return
	}
	if !st.knownEvent(name) {
		restErr(c, http.StatusBadRequest, "pixelgo_invalid_event", "Unknown event.")
		return
	}
	if len(ready) == 0 || name == "Purchase" || name == "Refund" { // purchases and refunds are sent from the order
		c.JSON(http.StatusAccepted, gin.H{"queued": 0})
		return
	}
	platforms := s.pixelAllowed(c, &st, ready)
	if want, ok := r.Body["platforms"].([]any); ok {
		var limited []string
		for _, w := range want {
			if k, _ := w.(string); pixelHas(platforms, k) {
				limited = append(limited, k)
			}
		}
		platforms = limited
	}
	if len(platforms) == 0 {
		c.JSON(http.StatusAccepted, gin.H{"queued": 0})
		return
	}
	var params map[string]any
	if m, ok := r.Body["p"].(map[string]any); ok {
		params = pixelCleanParams(m)
	}
	pageURL := ""
	if p, _ := r.Str(c, "url"); pixelPathRe.MatchString(p) && !strings.HasPrefix(p, "//") {
		pageURL = s.links(c).HomeURL(p)
	}
	ev := pixelServerEvent{Name: name, ID: id, Source: "browser", URL: pageURL, Params: params, Time: time.Now(), IP: clientIP(c),
		UA: c.Request.UserAgent(), Cookies: s.pixelRequestCookies(c), Platforms: platforms, User: map[string]string{}}
	if st.Advanced {
		if u := currentUser(c); u != nil {
			ev.User["em"] = pixelHash(u.Email)
			ev.User["external_id"] = pixelHash(strconv.FormatInt(u.ID, 10))
		}
	}
	s.pixelDispatch(ev)
	c.JSON(http.StatusAccepted, gin.H{"queued": len(platforms)})
}

// knownEvent: a canonical event or one of the enabled custom events.
func (st *pixelSettings) knownEvent(name string) bool {
	if _, ok := pixelEventMap[name]; ok {
		return true
	}
	for _, ce := range st.Custom {
		if ce.Enabled && ce.Name == name {
			return true
		}
	}
	return false
}

// pixelRequestCookies are the identifiers the platforms match visitors with.
func (s *Server) pixelRequestCookies(c *gin.Context) map[string]string {
	m := map[string]string{}
	for _, k := range []string{"_fbp", "_fbc", "_ttp"} {
		if v, err := c.Cookie(k); err == nil && len(v) <= 200 && !strings.ContainsAny(v, " \"'<>") {
			m[strings.TrimPrefix(k, "_")] = v
		}
	}
	if v, err := c.Cookie("_ga"); err == nil {
		if p := strings.Split(v, "."); len(p) >= 4 {
			m["ga"] = p[2] + "." + p[3]
		}
	}
	if raw, err := c.Cookie("pixelgo_attr"); err == nil {
		var a map[string]string
		if json.Unmarshal([]byte(raw), &a) == nil {
			for _, k := range []string{"ttclid", "epik", "fbclid", "gclid", "msclkid"} {
				if v := a[k]; v != "" && len(v) <= 300 && !strings.ContainsAny(v, " \"'<>") {
					m[k] = v
				}
			}
			if m["fbc"] == "" && m["fbclid"] != "" {
				m["fbc"] = pixelFbc(a["fbts"], m["fbclid"], time.Now())
			}
		}
	}
	return m
}

// pixelFbc builds Meta's click id value fb.1.<ms the click was first seen>.<fbclid>. firstSeen is the
// pixelgo_attr time (milliseconds) of the click; without it (a cookie from before it was recorded, or a
// malformed value) the current time is used, which is the best the server knows.
func pixelFbc(firstSeen, fbclid string, now time.Time) string {
	ms := now.UnixMilli()
	if v, err := strconv.ParseInt(firstSeen, 10, 64); err == nil && v > 1e12 && v <= ms {
		ms = v
	}
	return "fb.1." + strconv.FormatInt(ms, 10) + "." + fbclid
}

// pixelCleanParams keeps only the canonical, well-formed parameters of an event and bounds their size.
func pixelCleanParams(in map[string]any) map[string]any {
	out := map[string]any{}
	str := func(v any, n int) string { s, _ := v.(string); return pixelTrim(s, n) }
	num := func(v any) (float64, bool) {
		switch x := v.(type) {
		case float64:
			return x, x >= 0 && x < 1e10
		case string:
			f, err := strconv.ParseFloat(x, 64)
			return f, err == nil && f >= 0 && f < 1e10
		}
		return 0, false
	}
	for _, k := range []string{"content_name", "content_category", "content_type", "search_string", "order_id", "payment_type"} {
		if v := str(in[k], 200); v != "" {
			out[k] = v
		}
	}
	for _, k := range []string{"value", "tax", "shipping", "num_items"} {
		if f, ok := num(in[k]); ok {
			out[k] = f
		}
	}
	if cur := strings.ToUpper(str(in["currency"], 3)); pixelCurRe.MatchString(cur) {
		out["currency"] = cur
	}
	if raw, ok := in["items"].([]any); ok {
		var items []map[string]any
		for _, r := range raw {
			m, _ := r.(map[string]any)
			id := str(m["id"], 100)
			if id == "" || len(items) >= 50 {
				continue
			}
			it := map[string]any{"id": id}
			if v := str(m["name"], 200); v != "" {
				it["name"] = v
			}
			if v := str(m["category"], 200); v != "" {
				it["category"] = v
			}
			if f, ok := num(m["price"]); ok {
				it["price"] = f
			}
			q, _ := num(m["quantity"])
			it["quantity"] = int(min(max(q, 1), 10000))
			items = append(items, it)
		}
		if len(items) > 0 {
			out["items"] = items
		}
	}
	if ex, ok := in["extra"].(map[string]any); ok {
		extra := map[string]any{}
		for k, v := range ex {
			if !pixelKeyOK.MatchString(k) || len(extra) >= 25 {
				continue
			}
			switch x := v.(type) {
			case string:
				extra[k] = pixelTrim(x, 200)
			case float64:
				extra[k] = x
			case bool:
				extra[k] = x
			}
		}
		if len(extra) > 0 {
			out["extra"] = extra
		}
	}
	return out
}

// pixelDispatch delivers an event to the platforms asynchronously; every delivery is logged.
func (s *Server) pixelDispatch(ev pixelServerEvent) {
	st := s.pixelLoad()
	for _, def := range st.serverReady() {
		if len(ev.Platforms) > 0 && !pixelHas(ev.Platforms, def.Key) {
			continue
		}
		def, cfg := def, st.Platforms[def.Key]
		claim := ""
		if ev.OrderID > 0 && ev.Source == "order" {
			claim = strconv.FormatInt(ev.OrderID, 10) + ":" + def.Key
			if _, busy := pixelInflight.LoadOrStore(claim, true); busy {
				continue
			}
		}
		if !pixelAdmit(ev.Source) {
			if claim != "" {
				pixelInflight.Delete(claim)
			}
			continue
		}
		go func() {
			defer pixelQueued.Add(-1)
			if claim != "" {
				defer pixelInflight.Delete(claim)
			}
			status, code, msg := s.pixelDeliverRetry(def.Key, cfg, ev)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if claim != "" && (status == "ok" || status == "skipped") {
				s.pixelMarkSent(ctx, ev.OrderID, def.Key)
			}
			if status != "skipped" || ev.Test {
				s.pixelLog(ctx, &st, def.Key, ev, status, code, msg)
			}
		}()
	}
}

// pixelDeliverRetry delivers an event and, while the platform fails in a way that can pass (network
// error, 429, 5xx), tries again after growing waits. A delivery slot is held only during an attempt.
func (s *Server) pixelDeliverRetry(platform string, cfg pixelPlatformCfg, ev pixelServerEvent) (string, int, string) {
	for attempt := 0; ; attempt++ {
		pixelSlots <- struct{}{}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		status, code, msg := s.pixelDeliver(ctx, platform, cfg, ev)
		cancel()
		<-pixelSlots
		if ev.Test || attempt >= len(pixelRetryDelays) || !pixelRetryable(status, code) {
			if attempt > 0 && status == "error" {
				msg = "After " + strconv.Itoa(attempt+1) + " attempts: " + msg
			}
			return status, code, msg
		}
		time.Sleep(pixelRetryDelays[attempt])
	}
}

// pixelDeliver sends one event to one platform. status: ok, error or skipped.
func (s *Server) pixelDeliver(ctx context.Context, platform string, cfg pixelPlatformCfg, ev pixelServerEvent) (string, int, string) {
	name, ok := pixelPlatformEvent(platform, ev.Name)
	if !ok {
		return "skipped", 0, "The event has no equivalent on this platform."
	}
	var code int
	var body []byte
	var err error
	switch platform {
	case "meta":
		code, body, err = pixelSendMeta(ctx, cfg, name, ev)
	case "tiktok":
		if name == "@page" {
			return "skipped", 0, "TikTok page views are sent by the browser only."
		}
		code, body, err = pixelSendTikTok(ctx, cfg, name, ev)
	case "pinterest":
		code, body, err = pixelSendPinterest(ctx, cfg, name, ev)
	case "ga4":
		if ev.Name != "Purchase" && ev.Name != "Refund" {
			return "skipped", 0, "GA4 gets Purchase and Refund server-side only."
		}
		code, body, err = pixelSendGA4(ctx, cfg, name, ev, false)
	default:
		return "skipped", 0, "No server-side API."
	}
	if err != nil {
		return "error", 0, pixelNetErr(err)
	}
	if code >= 200 && code < 300 {
		return "ok", code, pixelShort(body)
	}
	return "error", code, pixelShort(body)
}

func pixelNetErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) { // url.Error carries the request URL, which can hold a secret
		return pixelTrim(ue.Err.Error(), 200)
	}
	return pixelTrim(err.Error(), 200)
}

func pixelShort(b []byte) string {
	t := strings.Join(strings.Fields(string(b)), " ")
	if len(t) > 300 {
		t = t[:300]
	}
	return t
}

func pixelPost(ctx context.Context, endpoint string, header map[string]string, payload any) (int, []byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := pixelHTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	return res.StatusCode, b, nil
}

func pixelStr(p map[string]any, k string) string { s, _ := p[k].(string); return s }

func pixelItemList(p map[string]any) []map[string]any {
	switch v := p["items"].(type) {
	case []map[string]any:
		return v
	}
	return nil
}

func pixelNum(p map[string]any, k string) (float64, bool) {
	switch v := p[k].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}

func pixelEventTime(ev pixelServerEvent) int64 {
	if ev.Time.IsZero() {
		return time.Now().Unix()
	}
	return ev.Time.Unix()
}

// pixelSendMeta posts to the Conversions API of every Pixel ID of the platform.
func pixelSendMeta(ctx context.Context, cfg pixelPlatformCfg, name string, ev pixelServerEvent) (int, []byte, error) {
	user := map[string]any{"client_user_agent": ev.UA}
	if ev.IP != "" {
		user["client_ip_address"] = ev.IP
	}
	if v := ev.Cookies["fbp"]; v != "" {
		user["fbp"] = v
	}
	if v := ev.Cookies["fbc"]; v != "" {
		user["fbc"] = v
	}
	for k, v := range ev.User {
		switch k {
		case "em", "ph", "fn", "ln", "ct", "st", "zp", "country", "external_id":
			user[k] = []string{v}
		}
	}
	custom := map[string]any{}
	p := ev.Params
	if v, ok := pixelNum(p, "value"); ok {
		custom["value"] = v
	}
	if v := pixelStr(p, "currency"); v != "" {
		custom["currency"] = v
	}
	if items := pixelItemList(p); len(items) > 0 {
		var ids []string
		var contents []map[string]any
		for _, it := range items {
			id, _ := it["id"].(string)
			ids = append(ids, id)
			c := map[string]any{"id": id, "quantity": it["quantity"]}
			if pr, ok := it["price"]; ok {
				c["item_price"] = pr
			}
			contents = append(contents, c)
		}
		custom["content_ids"], custom["contents"], custom["content_type"] = ids, contents, "product"
	}
	for _, k := range []string{"content_name", "content_category", "search_string", "order_id"} {
		if v := pixelStr(p, k); v != "" {
			custom[k] = v
		}
	}
	if v, ok := pixelNum(p, "num_items"); ok {
		custom["num_items"] = int(v)
	}
	if extra, ok := p["extra"].(map[string]any); ok {
		for k, v := range extra {
			custom[k] = v
		}
	}
	data := map[string]any{"event_name": name, "event_time": pixelEventTime(ev), "event_id": ev.ID, "action_source": "website", "user_data": user, "custom_data": custom}
	if ev.URL != "" {
		data["event_source_url"] = ev.URL
	}
	var lastCode int
	var lastBody []byte
	for _, id := range cfg.IDs {
		payload := map[string]any{"data": []any{data}, "access_token": cfg.Token}
		if cfg.TestCode != "" {
			payload["test_event_code"] = cfg.TestCode
		}
		code, body, err := pixelPost(ctx, pixelMetaAPI+url.PathEscape(id)+"/events", nil, payload)
		if err != nil {
			return 0, nil, err
		}
		lastCode, lastBody = code, body
		if code < 200 || code >= 300 {
			break
		}
	}
	return lastCode, lastBody, nil
}

func pixelSendTikTok(ctx context.Context, cfg pixelPlatformCfg, name string, ev pixelServerEvent) (int, []byte, error) {
	user := map[string]any{"user_agent": ev.UA}
	if ev.IP != "" {
		user["ip"] = ev.IP
	}
	if v := ev.Cookies["ttp"]; v != "" {
		user["ttp"] = v
	}
	if v := ev.Cookies["ttclid"]; v != "" {
		user["ttclid"] = v
	}
	if v := ev.User["em"]; v != "" {
		user["email"] = v
	}
	if v := ev.User["ph"]; v != "" {
		user["phone_number"] = v
	}
	if v := ev.User["external_id"]; v != "" {
		user["external_id"] = v
	}
	props := map[string]any{}
	p := ev.Params
	if v, ok := pixelNum(p, "value"); ok {
		props["value"] = v
	}
	if v := pixelStr(p, "currency"); v != "" {
		props["currency"] = v
	}
	if v := pixelStr(p, "search_string"); v != "" {
		props["query"] = v
	}
	if items := pixelItemList(p); len(items) > 0 {
		var contents []map[string]any
		for _, it := range items {
			c := map[string]any{"content_id": it["id"], "content_type": "product", "quantity": it["quantity"]}
			if v, ok := it["name"]; ok {
				c["content_name"] = v
			}
			if v, ok := it["price"]; ok {
				c["price"] = v
			}
			contents = append(contents, c)
		}
		props["contents"], props["content_type"] = contents, "product"
	}
	d := map[string]any{"event": name, "event_time": pixelEventTime(ev), "event_id": ev.ID, "user": user, "properties": props}
	if ev.URL != "" {
		d["page"] = map[string]any{"url": ev.URL}
	}
	var lastCode int
	var lastBody []byte
	for _, id := range cfg.IDs {
		payload := map[string]any{"event_source": "web", "event_source_id": id, "data": []any{d}}
		if cfg.TestCode != "" {
			payload["test_event_code"] = cfg.TestCode
		}
		code, body, err := pixelPost(ctx, pixelTikTokAPI, map[string]string{"Access-Token": cfg.Token}, payload)
		if err != nil {
			return 0, nil, err
		}
		lastCode, lastBody = code, body
		if code == 200 { // TikTok answers 200 with a non-zero "code" for errors
			var r struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(body, &r) == nil && r.Code != 0 {
				lastCode = http.StatusBadRequest
				break
			}
		}
		if lastCode < 200 || lastCode >= 300 {
			break
		}
	}
	return lastCode, lastBody, nil
}

// pixelPinName turns the tag's event name into the Conversions API's.
func pixelPinName(n string) string {
	switch n {
	case "@page", "pagevisit":
		return "page_visit"
	case "addtocart":
		return "add_to_cart"
	case "viewcategory":
		return "view_category"
	}
	return n // search, checkout, lead, signup, custom
}

func pixelSendPinterest(ctx context.Context, cfg pixelPlatformCfg, name string, ev pixelServerEvent) (int, []byte, error) {
	if cfg.AccountID == "" {
		return 0, nil, errors.New("the Pinterest ad account ID is not set")
	}
	user := map[string]any{"client_user_agent": ev.UA}
	if ev.IP != "" {
		user["client_ip_address"] = ev.IP
	}
	if v := ev.User["em"]; v != "" {
		user["em"] = []string{v}
	}
	if v := ev.Cookies["epik"]; v != "" {
		user["click_id"] = v
	}
	custom := map[string]any{}
	p := ev.Params
	if v, ok := pixelNum(p, "value"); ok {
		custom["value"] = strconv.FormatFloat(v, 'f', 2, 64)
	}
	if v := pixelStr(p, "currency"); v != "" {
		custom["currency"] = v
	}
	if v := pixelStr(p, "order_id"); v != "" {
		custom["order_id"] = v
	}
	if v, ok := pixelNum(p, "num_items"); ok {
		custom["num_items"] = int(v)
	}
	if v := pixelStr(p, "search_string"); v != "" {
		custom["search_string"] = v
	}
	if items := pixelItemList(p); len(items) > 0 {
		var ids []string
		var contents []map[string]any
		for _, it := range items {
			id, _ := it["id"].(string)
			ids = append(ids, id)
			c := map[string]any{"id": id, "quantity": it["quantity"]}
			if pr, ok := it["price"].(float64); ok {
				c["item_price"] = strconv.FormatFloat(pr, 'f', 2, 64)
			}
			contents = append(contents, c)
		}
		custom["content_ids"], custom["contents"] = ids, contents
	}
	d := map[string]any{"event_name": pixelPinName(name), "action_source": "web", "event_time": pixelEventTime(ev), "event_id": ev.ID, "user_data": user, "custom_data": custom}
	if ev.URL != "" {
		d["event_source_url"] = ev.URL
	}
	return pixelPost(ctx, pixelPinAPI+url.PathEscape(cfg.AccountID)+"/events", map[string]string{"Authorization": "Bearer " + cfg.Token}, map[string]any{"data": []any{d}})
}

// pixelSendGA4 posts a purchase to the Measurement Protocol (debug: the validation endpoint).
func pixelSendGA4(ctx context.Context, cfg pixelPlatformCfg, name string, ev pixelServerEvent, debug bool) (int, []byte, error) {
	client := ev.Cookies["ga"]
	if client == "" {
		client = "pixelgo." + pixelRandom(10)
	}
	p := ev.Params
	params := map[string]any{}
	if v, ok := pixelNum(p, "value"); ok {
		params["value"] = v
	}
	if v := pixelStr(p, "currency"); v != "" {
		params["currency"] = v
	}
	if v := pixelStr(p, "order_id"); v != "" {
		params["transaction_id"] = v
	}
	for _, k := range []string{"tax", "shipping"} {
		if v, ok := pixelNum(p, k); ok {
			params[k] = v
		}
	}
	if items := pixelItemList(p); len(items) > 0 {
		var out []map[string]any
		for _, it := range items {
			g := map[string]any{"item_id": it["id"], "quantity": it["quantity"]}
			if v, ok := it["name"]; ok {
				g["item_name"] = v
			}
			if v, ok := it["price"]; ok {
				g["price"] = v
			}
			out = append(out, g)
		}
		params["items"] = out
	}
	payload := map[string]any{"client_id": client, "events": []any{map[string]any{"name": name, "params": params}}}
	var code int
	var body []byte
	var err error
	base := pixelGA4API
	if debug {
		base = pixelGA4Debug
	}
	for _, id := range cfg.IDs {
		code, body, err = pixelPost(ctx, base+"?measurement_id="+url.QueryEscape(id)+"&api_secret="+url.QueryEscape(cfg.Token), nil, payload)
		if err != nil || code < 200 || code >= 300 {
			break
		}
	}
	return code, body, err
}

// pixelLog records one delivery (never a token, never a URL).
func (s *Server) pixelLog(ctx context.Context, st *pixelSettings, platform string, ev pixelServerEvent, status string, code int, msg string) {
	if !st.LogServer && status == "ok" {
		return
	}
	_, _ = s.store.Pool.Exec(ctx, `INSERT INTO pixelgo_log (platform, event, event_id, source, status, http_code, message) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		platform, pixelTrim(ev.Name, 40), pixelTrim(ev.ID, 64), ev.Source, status, code, pixelTrim(msg, 300))
}

// pixelPurge deletes log rows older than the retention.
func (s *Server) pixelPurge(ctx context.Context) (int64, error) {
	st := s.pixelLoad()
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM pixelgo_log WHERE created_at < now() - make_interval(days => $1)`, st.LogRetention)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// pixelSendTest sends a test event to a platform and reports the answer; the admin shows it.
func (s *Server) pixelSendTest(ctx context.Context, platform string) (string, int, string) {
	st := s.pixelLoad()
	def := pixelPlatformByKey(platform)
	if def == nil {
		return "error", 0, "Unknown platform."
	}
	cfg := st.Platforms[platform]
	if !cfg.Enabled || len(cfg.IDs) == 0 || cfg.Token == "" {
		return "error", 0, "Enable the platform, add an ID and the access token first."
	}
	ev := pixelServerEvent{Name: "PageView", ID: "test_" + pixelRandom(12), Source: "test", Time: time.Now(), Test: true, IP: "203.0.113.10",
		UA: "Mozilla/5.0 (Pursae Pixel test)", URL: s.links(ctx).HomeURL("/"), Params: map[string]any{}, Platforms: []string{platform}}
	switch platform {
	case "tiktok":
		ev.Name = "ViewContent"
	case "ga4":
		ev.Name = "Purchase"
		ev.Params = map[string]any{"value": 1.0, "currency": "USD", "order_id": "pixelgo-test"}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var status string
	var code int
	var msg string
	if platform == "ga4" { // the validation endpoint answers with the problems it finds
		var body []byte
		var err error
		code, body, err = pixelSendGA4(cctx, cfg, "purchase", ev, true)
		var r struct {
			Msgs []struct{ Description string } `json:"validationMessages"`
		}
		switch {
		case err != nil:
			status, msg = "error", pixelNetErr(err)
		case json.Unmarshal(body, &r) == nil && len(r.Msgs) == 0 && code == 200:
			status, msg = "ok", "The Measurement Protocol accepted the event (validation endpoint; nothing was recorded)."
		case len(r.Msgs) > 0:
			status, msg = "error", pixelTrim(r.Msgs[0].Description, 200)
		default:
			status, msg = "error", pixelShort(body)
		}
	} else {
		status, code, msg = s.pixelDeliver(cctx, platform, cfg, ev)
		if status == "ok" && cfg.TestCode != "" {
			msg = "Delivered with your test event code: check Test Events in the platform. " + msg
		}
	}
	s.pixelLog(cctx, &st, platform, ev, status, code, msg)
	return status, code, msg
}

func init() {
	gooOn("order_created", 90, func(s *Server, ctx context.Context, arg any) { s.pixelOrderCreated(ctx, arg.(*gooOrder)) })
	gooOn("payment_complete", 90, func(s *Server, ctx context.Context, arg any) { s.pixelPaymentComplete(ctx, arg.(*gooOrder)) })
	gooOn("order_fully_refunded", 90, func(s *Server, ctx context.Context, arg any) { s.pixelRefunded(ctx, arg.(*gooRefundEvent)) })
	gooOn("order_partially_refunded", 90, func(s *Server, ctx context.Context, arg any) { s.pixelRefunded(ctx, arg.(*gooRefundEvent)) })
	gooOn("hourly", 90, func(s *Server, ctx context.Context, _ any) { s.pixelRetryPurchases(ctx) })
}

// pixelRetryPurchases sends again the Purchases of orders paid in the last 24 hours that a platform has
// not accepted yet: the attempts of pixelDispatch ran out, or the process stopped in the middle of one.
// Orders without a checkout snapshot (no platform was ready at checkout) are not touched.
func (s *Server) pixelRetryPurchases(ctx context.Context) int {
	if !s.pixelOn() {
		return 0
	}
	st := s.pixelLoad()
	if len(st.serverReady()) == 0 || !st.Woo["purchase"] {
		return 0
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM goo_orders WHERE type = 'shop_order' AND meta->'`+pixelOrderMeta+`' IS NOT NULL
		AND COALESCE(date_paid, date_created) BETWEEN now() - interval '24 hours' AND now() - interval '5 minutes' ORDER BY id LIMIT 200`)
	if err != nil {
		return 0
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		if o, err := s.gooGetOrder(ctx, id); err == nil && s.pixelRetryOrder(ctx, o) {
			n++
		}
	}
	return n
}

// pixelRetryOrder sends the Purchase of one order again to the platforms that have not accepted it;
// false when nothing is pending.
func (s *Server) pixelRetryOrder(ctx context.Context, o *gooOrder) bool {
	if o == nil || !pixelPurchaseWanted(o) {
		return false
	}
	all, sent := pixelSentSet(o)
	if all || !s.pixelPendingAny(o, sent) {
		return false
	}
	s.pixelPaymentComplete(ctx, o)
	return true
}

// pixelPendingAny: a platform the Purchase may go to (the consented ones of the checkout snapshot, or every
// server-side platform without a consent gate) is missing from the sent set.
func (s *Server) pixelPendingAny(o *gooOrder, sent map[string]bool) bool {
	st := s.pixelLoad()
	if !s.pixelConsentOn(&st) { // no consent gate: every server-side platform is a target
		for _, d := range st.serverReady() {
			if !sent[d.Key] {
				return true
			}
		}
		return false
	}
	snap, _ := o.Meta[pixelOrderMeta].(map[string]any)
	for _, k := range pixelSnapStrings(snap["platforms"]) {
		if !sent[k] {
			return true
		}
	}
	return false
}

// pixelRefunded sends a refund to Google Analytics 4 (the Measurement Protocol "refund" event, with the
// transaction id of the Purchase) when GA4 received that Purchase from the server. Meta, TikTok and
// Pinterest have no refund event; their reports keep the order's revenue.
func (s *Server) pixelRefunded(ctx context.Context, e *gooRefundEvent) {
	if !s.pixelOn() || e == nil || e.Order == nil || e.Refund == nil {
		return
	}
	st := s.pixelLoad()
	cfg := st.Platforms["ga4"]
	if !st.Woo["purchase"] || !cfg.Enabled || len(cfg.IDs) == 0 || !cfg.ServerSide || cfg.Token == "" {
		return
	}
	o, r := e.Order, e.Refund
	if all, sent := pixelSentSet(o); !all && !sent["ga4"] {
		return // GA4 never recorded the sale: there is nothing to take back
	}
	cur := o.Currency
	if cur == "" {
		cur = s.gooCurrency()
	}
	params := map[string]any{"value": pixelMoney(-r.Total), "currency": cur, "order_id": strconv.FormatInt(o.ID, 10)}
	if len(r.Items) > 0 && r.Total != o.Total { // a partial refund names what came back
		var items []pixelItem
		for _, it := range r.Items {
			pid := it.ProductID
			if st.VariationIDs && it.VariationID > 0 {
				pid = it.VariationID
			}
			if pid <= 0 || it.Type != "line_item" && it.Type != "" {
				continue
			}
			sku := ""
			if p, err := s.gooGetProduct(ctx, pid); err == nil && p != nil {
				sku = p.SKU
			}
			qty := max(int(-it.Quantity), 1)
			items = append(items, pixelItem{ID: pixelItemID(&st, pid, sku), Name: it.Name, Price: -it.Total / float64(qty), Qty: qty})
		}
		if len(items) > 0 {
			params["items"] = pixelItems(items)
		}
	}
	snap, _ := o.Meta[pixelOrderMeta].(map[string]any)
	ev := pixelServerEvent{Name: "Refund", ID: "refund_" + strconv.FormatInt(r.ID, 10), Source: "refund", Time: time.Now(), Params: params,
		Cookies: pixelSnapStringMap(snap["cookies"]), User: map[string]string{}, Platforms: []string{"ga4"}}
	s.pixelDispatch(ev)
}

// pixelOrderCreated keeps what the checkout request knows about the visitor (identifiers, address,
// and the platforms they consented to) with the order, because the payment may complete later in a
// request without those cookies (an off-site gateway's webhook).
func (s *Server) pixelOrderCreated(ctx context.Context, o *gooOrder) {
	if !s.pixelOn() {
		return
	}
	st := s.pixelLoad()
	ready := st.serverReady()
	if len(ready) == 0 {
		return
	}
	var gc *gin.Context
	if v, ok := ctx.(*gin.Context); ok {
		gc = v
	} else if cart := gooCartFromCtx(ctx); cart != nil {
		gc = cart.c
	}
	if gc == nil || gc.Request == nil {
		return
	}
	// JSON-shaped values ([]any, map[string]any), the same types a reload from the database gives: the payment
	// can complete in this same request on this same *gooOrder (free orders, the test gateway, card gateways).
	plats := []any{}
	for _, k := range s.pixelAllowed(gc, &st, ready) {
		plats = append(plats, k)
	}
	cks := map[string]any{}
	for k, v := range s.pixelRequestCookies(gc) {
		cks[k] = v
	}
	snap := map[string]any{"ip": clientIP(gc), "ua": gc.Request.UserAgent(), "cookies": cks, "platforms": plats,
		"url": s.links(gc).HomeURL("/")}
	if o.Meta == nil {
		o.Meta = map[string]any{}
	}
	o.Meta[pixelOrderMeta] = snap
	_ = s.gooSaveOrder(ctx, o, false)
}

// pixelSnapStrings reads a string list of the order snapshot, stored as []any (from the database) or []string.
func pixelSnapStrings(v any) []string {
	var out []string
	switch l := v.(type) {
	case []string:
		for _, k := range l {
			if k != "" {
				out = append(out, k)
			}
		}
	case []any:
		for _, p := range l {
			if k, _ := p.(string); k != "" {
				out = append(out, k)
			}
		}
	}
	return out
}

// pixelSnapStringMap reads a string map of the order snapshot, stored as map[string]any or map[string]string.
func pixelSnapStringMap(v any) map[string]string {
	out := map[string]string{}
	switch m := v.(type) {
	case map[string]string:
		for k, sv := range m {
			if sv != "" {
				out[k] = sv
			}
		}
	case map[string]any:
		for k, x := range m {
			if sv, _ := x.(string); sv != "" {
				out[k] = sv
			}
		}
	}
	return out
}

// pixelPaymentComplete sends the Purchase of a paid order to the server-side platforms the visitor allowed.
func (s *Server) pixelPaymentComplete(ctx context.Context, o *gooOrder) {
	if !s.pixelOn() || o == nil || !pixelPurchaseWanted(o) {
		return
	}
	st := s.pixelLoad()
	ready := st.serverReady()
	allSent, sent := pixelSentSet(o)
	if len(ready) == 0 || !st.Woo["purchase"] || allSent {
		return
	}
	snap, _ := o.Meta[pixelOrderMeta].(map[string]any)
	var platforms []string
	if s.pixelConsentOn(&st) {
		platforms = pixelSnapStrings(snap["platforms"]) // no snapshot, no consent evidence: nothing is sent
	} else {
		for _, d := range ready {
			platforms = append(platforms, d.Key)
		}
	}
	var pending []string // the platforms that have not accepted the Purchase yet
	for _, k := range platforms {
		if !sent[k] {
			pending = append(pending, k)
		}
	}
	if platforms = pending; len(platforms) == 0 {
		return
	}
	ev := pixelServerEvent{Name: "Purchase", ID: pixelPurchaseEventID(o.ID), Source: "order", OrderID: o.ID, Time: time.Now(), IP: o.CustomerIP, UA: o.UserAgent,
		Params: s.pixelOrderParamsAny(ctx, &st, o), Cookies: map[string]string{}, User: map[string]string{}, Platforms: platforms}
	if st.Advanced { // hashed customer data only with Advanced matching, as on the order-received page
		ev.User = pixelUserFromOrder(o)
	}
	if snap != nil {
		if v, _ := snap["ip"].(string); v != "" {
			ev.IP = v
		}
		if v, _ := snap["ua"].(string); v != "" {
			ev.UA = v
		}
		ev.URL, _ = snap["url"].(string)
		for k, v := range pixelSnapStringMap(snap["cookies"]) {
			ev.Cookies[k] = v
		}
	}
	if o.CustomerID > 0 && st.Advanced {
		ev.User["external_id"] = pixelHash(strconv.FormatInt(o.CustomerID, 10))
	}
	s.pixelDispatch(ev)
}

// pixelOrderParamsAny is pixelOrderParams with the types the senders read (items as []map[string]any).
func (s *Server) pixelOrderParamsAny(ctx context.Context, st *pixelSettings, o *gooOrder) map[string]any {
	p := s.pixelOrderParams(ctx, st, o)
	if n, ok := p["num_items"].(int); ok {
		p["num_items"] = float64(n)
	}
	return p
}
