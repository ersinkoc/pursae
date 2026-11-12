package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Settings that used to be saved without effect: secure checkout, order attribution and the
// Accounts & Privacy erasure / retention options.

const gooAttrCookie = "goo_order_attribution"

// gooForceSSL redirects checkout and My account to https when "Force secure checkout" is on.
func (s *Server) gooForceSSL(c *gin.Context) bool {
	if !s.gooOptBool("force_ssl_checkout") || c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		return false
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	h := c.Request.Host
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]" {
		return false
	}
	c.Redirect(http.StatusMovedPermanently, "https://"+c.Request.Host+c.Request.URL.RequestURI())
	return true
}

// gooAttributionFrom is the source data of a request: utm_* parameters, or an external referrer.
func gooAttributionFrom(q url.Values, referer, host string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term"} {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			out[k] = v
		}
	}
	if len(out) > 0 {
		out["source_type"] = "utm"
		return out
	}
	if u, err := url.Parse(referer); err == nil && u.Host != "" && !strings.EqualFold(u.Host, host) {
		out["utm_source"], out["source_type"], out["referrer"] = u.Hostname(), "referral", referer
	}
	return out
}

// gooCaptureAttribution remembers the visitor's first marketing touch (WC Order Attribution).
func (s *Server) gooCaptureAttribution(c *gin.Context) {
	if !s.gooOptBool("feature_order_attribution") || c.Request.Method != http.MethodGet {
		return
	}
	if ck, err := c.Request.Cookie(gooAttrCookie); err == nil && ck.Value != "" {
		return
	}
	a := gooAttributionFrom(c.Request.URL.Query(), c.Request.Referer(), c.Request.Host)
	if len(a) == 0 {
		return
	}
	b, _ := json.Marshal(a)
	http.SetCookie(c.Writer, &http.Cookie{Name: gooAttrCookie, Value: url.QueryEscape(string(b)), Path: "/", MaxAge: 6 * 30 * 86400,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"})
}

// gooApplyAttribution copies the remembered touch to the order meta (_wc_order_attribution_*).
func (s *Server) gooApplyAttribution(c *gin.Context, o *gooOrder) {
	if !s.gooOptBool("feature_order_attribution") {
		return
	}
	ck, err := c.Request.Cookie(gooAttrCookie)
	if err != nil {
		return
	}
	raw, _ := url.QueryUnescape(ck.Value)
	var a map[string]string
	if json.Unmarshal([]byte(raw), &a) != nil {
		return
	}
	for k, v := range a {
		o.Meta["_wc_order_attribution_"+k] = v
	}
}

// gooAttributionRows is the order screen's "Order attribution" box (WC's order-attribution metabox):
// origin first, then the recorded source fields. Empty when the order has no attribution.
func gooAttributionRows(o *gooOrder) []gooOpt {
	get := func(k string) string { v, _ := o.Meta["_wc_order_attribution_"+k].(string); return v }
	typ, src := get("source_type"), get("utm_source")
	if typ == "" && src == "" {
		return nil
	}
	origin := "Unknown"
	switch typ {
	case "utm":
		origin = "Source: " + src
	case "referral":
		origin = "Referral: " + src
	case "typein":
		origin = "Direct"
	case "admin":
		origin = "Web admin"
	}
	rows := []gooOpt{{"Origin", origin}}
	for _, f := range []struct{ key, label string }{{"source_type", "Source type"}, {"utm_campaign", "UTM campaign"}, {"utm_source", "UTM source"},
		{"utm_medium", "UTM medium"}, {"utm_content", "UTM content"}, {"utm_term", "UTM term"}, {"referrer", "Referral"}} {
		if v := get(f.key); v != "" {
			rows = append(rows, gooOpt{f.label, v})
		}
	}
	return rows
}

// gooErasePersonalData applies the erasure options to an erasure request for email.
func (s *Server) gooErasePersonalData(ctx context.Context, email string) {
	if s.gooOptBool("erasure_request_removes_order_data") {
		_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_orders SET billing = '{}', shipping = '{}', billing_email = '', customer_ip = '', user_agent = '',
			customer_note = '', meta = meta || '{"_anonymized":true}'
			WHERE type = 'shop_order' AND lower(billing_email) = lower($1) AND status IN ('completed','cancelled','refunded','failed')`, email)
	}
	if s.gooOptBool("erasure_request_removes_download_data") {
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_download_permissions WHERE lower(user_email) = lower($1)`, email)
	}
}

// gooDeleteInactiveCustomers deletes customers with no order for N months (Retain inactive accounts).
func (s *Server) gooDeleteInactiveCustomers(ctx context.Context) {
	months := s.gooOptInt("delete_inactive_accounts")
	if months <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, -months, 0)
	rows, err := s.store.Pool.Query(ctx, `SELECT u.id FROM users u WHERE u.registered_at < $1
		AND EXISTS (SELECT 1 FROM user_roles r JOIN roles ro ON ro.id = r.role_id WHERE r.user_id = u.id AND ro.slug = 'customer')
		AND NOT EXISTS (SELECT 1 FROM user_roles r JOIN roles ro ON ro.id = r.role_id WHERE r.user_id = u.id AND ro.slug <> 'customer')
		AND NOT EXISTS (SELECT 1 FROM goo_orders o WHERE o.customer_id = u.id AND o.date_created >= $1)`, cutoff)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_ = s.store.DeleteUser(ctx, id, 0)
	}
}
