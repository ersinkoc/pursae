package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Customer sessions (WC_Session_Handler): a random key in the wp_goocommerce_session cookie for
// visitors, the user id for logged-in customers (their cart persists across devices, like WooCommerce's
// persistent cart). The cart, applied coupons, chosen methods, the customer's addresses and pending
// notices live in goo_sessions. The goocommerce_items_in_cart cookie tells caches to step aside.

const (
	gooSessionCookie = "wp_goocommerce_session"
	gooItemsCookie   = "goocommerce_items_in_cart"
	gooCartHashCook  = "goocommerce_cart_hash"
	gooCtxSession    = "goo.session"
	gooSessionTTL    = 48 * time.Hour
)

// gooStoredItem is a cart item as stored in the session.
type gooStoredItem struct {
	Key         string            `json:"key"`
	ProductID   int64             `json:"product_id"`
	VariationID int64             `json:"variation_id"`
	Variation   map[string]string `json:"variation"`
	Quantity    int               `json:"quantity"`
	Data        map[string]any    `json:"data,omitempty"`
}

type gooNotice struct {
	Type string `json:"type"` // success, error, notice
	Msg  string `json:"msg"`  // HTML (written by Goocommerce, user values escaped)
}

// gooSessionData is the session's content.
type gooSessionData struct {
	Cart                 []gooStoredItem   `json:"cart"`
	AppliedCoupons       []string          `json:"applied_coupons"`
	ChosenShipping       map[string]string `json:"chosen_shipping_methods"` // package index → rate id
	ChosenPayment        string            `json:"chosen_payment_method"`
	Customer             gooCustomerData   `json:"customer"`
	Notices              []gooNotice       `json:"notices"`
	OrderAwaitingPayment int64             `json:"order_awaiting_payment"`
	Removed              []gooStoredItem   `json:"removed_cart_contents"`
	Extra                map[string]any    `json:"extra,omitempty"` // module data (wishlist of a guest, compare list, …)
}

// gooCustomerData is the customer's addresses (WC_Customer's session data).
type gooCustomerData struct {
	Billing           map[string]string `json:"billing"`
	Shipping          map[string]string `json:"shipping"`
	ShipToDifferent   bool              `json:"ship_to_different_address"`
	CalculatedShip    bool              `json:"calculated_shipping"`
	IsVATExempt       bool              `json:"is_vat_exempt"`
	HasFullAddress    bool              `json:"has_full_address"`
	LoadedFromAccount bool              `json:"loaded"`
}

type gooSession struct {
	Key   string
	Data  gooSessionData
	dirty bool
	isNew bool
	user  int64
}

func gooRandomKey(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// gooSessionFor returns the request's session, loading or starting it once per request.
func (s *Server) gooSessionFor(c *gin.Context) *gooSession {
	if v, ok := c.Get(gooCtxSession); ok {
		return v.(*gooSession)
	}
	sess := &gooSession{}
	ctx := c.Request.Context()
	u := currentUser(c)
	guestKey := ""
	if ck, err := c.Cookie(gooSessionCookie); err == nil && len(ck) == 32 && gooHexOnly(ck) {
		guestKey = ck
	}
	if u != nil {
		sess.Key, sess.user = "u:"+itoa64(u.ID), u.ID
		found := s.gooLoadSession(ctx, sess)
		if guestKey != "" {
			// Logging in merges the visitor cart into the customer's (wc_load_persistent_cart).
			guest := &gooSession{Key: guestKey}
			if s.gooLoadSession(ctx, guest) && (len(guest.Data.Cart) > 0 || len(guest.Data.Extra) > 0) {
				have := map[string]bool{}
				for _, it := range sess.Data.Cart {
					have[it.Key] = true
				}
				for _, it := range guest.Data.Cart {
					if !have[it.Key] {
						sess.Data.Cart = append(sess.Data.Cart, it)
					}
				}
				for _, code := range guest.Data.AppliedCoupons {
					if !containsStr(sess.Data.AppliedCoupons, code) {
						sess.Data.AppliedCoupons = append(sess.Data.AppliedCoupons, code)
					}
				}
				for k, v := range guest.Data.Extra { // module data (compare list, …)
					if sess.Data.Extra == nil {
						sess.Data.Extra = map[string]any{}
					}
					if _, ok := sess.Data.Extra[k]; !ok {
						sess.Data.Extra[k] = v
					}
				}
				sess.dirty = true
			}
			_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_sessions WHERE session_key = $1`, guestKey)
			http.SetCookie(c.Writer, &http.Cookie{Name: gooSessionCookie, Value: "", Path: "/", MaxAge: -1, Secure: s.httpsRequest(c.Request)})
		}
		if !found {
			sess.isNew = true
		}
		if !sess.Data.Customer.LoadedFromAccount {
			s.gooLoadCustomerFromUser(sess, u.Meta)
			sess.Data.Customer.LoadedFromAccount = true
			sess.dirty = true
		}
	} else if guestKey != "" {
		sess.Key = guestKey
		if !s.gooLoadSession(ctx, sess) {
			sess.isNew = true
		}
	} else {
		sess.Key, sess.isNew = gooRandomKey(32), true
	}
	if sess.Data.ChosenShipping == nil {
		sess.Data.ChosenShipping = map[string]string{}
	}
	if sess.Data.Customer.Billing == nil {
		sess.Data.Customer.Billing = map[string]string{}
	}
	if sess.Data.Customer.Shipping == nil {
		sess.Data.Customer.Shipping = map[string]string{}
	}
	c.Set(gooCtxSession, sess)
	return sess
}

func gooHexOnly(v string) bool {
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (s *Server) gooLoadSession(ctx context.Context, sess *gooSession) bool {
	var raw []byte
	err := s.store.Pool.QueryRow(ctx, `SELECT data FROM goo_sessions WHERE session_key = $1 AND expires_at > now()`, sess.Key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return false
	}
	_ = json.Unmarshal(raw, &sess.Data)
	return true
}

// gooLoadCustomerFromUser fills the session addresses from the customer's saved ones (user meta).
func (s *Server) gooLoadCustomerFromUser(sess *gooSession, meta map[string]any) {
	for _, f := range gooAddressKeys {
		if v, ok := meta["billing_"+f].(string); ok && v != "" && sess.Data.Customer.Billing[f] == "" {
			sess.Data.Customer.Billing[f] = v
		}
		if v, ok := meta["shipping_"+f].(string); ok && v != "" && sess.Data.Customer.Shipping[f] == "" {
			sess.Data.Customer.Shipping[f] = v
		}
	}
}

// gooAddressKeys are the fields of an address (billing also has email and phone).
var gooAddressKeys = []string{"first_name", "last_name", "company", "address_1", "address_2", "city", "state", "postcode", "country", "email", "phone"}

// touch marks the session changed.
func (sess *gooSession) touch() { sess.dirty = true }

// gooSaveSession writes a changed session and sets the cookies: the session key for visitors, the
// items-in-cart flag and the cart hash while the cart is not empty. Call it before writing the body.
func (s *Server) gooSaveSession(c *gin.Context) {
	v, ok := c.Get(gooCtxSession)
	if !ok {
		return
	}
	sess := v.(*gooSession)
	if !sess.dirty {
		return
	}
	empty := len(sess.Data.Cart) == 0 && len(sess.Data.AppliedCoupons) == 0 && len(sess.Data.Notices) == 0 &&
		sess.Data.OrderAwaitingPayment == 0 && len(sess.Data.Extra) == 0 && !sess.Data.Customer.CalculatedShip
	ctx := c.Request.Context()
	secure := s.httpsRequest(c.Request)
	if empty && sess.user == 0 {
		if !sess.isNew {
			_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_sessions WHERE session_key = $1`, sess.Key)
		}
		s.clearGooCookie(c, gooSessionCookie)
	} else {
		raw, _ := json.Marshal(sess.Data)
		ttl := gooSessionTTL
		if sess.user != 0 {
			ttl = 30 * 24 * time.Hour
		}
		_, _ = s.store.Pool.Exec(ctx, `INSERT INTO goo_sessions (session_key, data, expires_at) VALUES ($1, $2, $3)
			ON CONFLICT (session_key) DO UPDATE SET data = EXCLUDED.data, expires_at = EXCLUDED.expires_at`, sess.Key, raw, time.Now().Add(ttl))
		if sess.user == 0 {
			http.SetCookie(c.Writer, &http.Cookie{Name: gooSessionCookie, Value: sess.Key, Path: "/", Expires: time.Now().Add(gooSessionTTL),
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})
		}
	}
	if len(sess.Data.Cart) > 0 {
		http.SetCookie(c.Writer, &http.Cookie{Name: gooItemsCookie, Value: "1", Path: "/", SameSite: http.SameSiteLaxMode, Secure: secure})
		http.SetCookie(c.Writer, &http.Cookie{Name: gooCartHashCook, Value: gooCartHash(sess.Data.Cart), Path: "/", SameSite: http.SameSiteLaxMode, Secure: secure})
	} else {
		if _, err := c.Cookie(gooItemsCookie); err == nil {
			s.clearGooCookie(c, gooItemsCookie)
			s.clearGooCookie(c, gooCartHashCook)
		}
	}
	sess.dirty, sess.isNew = false, false
}

func gooClearCookie(c *gin.Context, name string) {
	if _, err := c.Cookie(name); err == nil {
		http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	}
}

func (s *Server) clearGooCookie(c *gin.Context, name string) {
	if _, err := c.Cookie(name); err == nil {
		http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: s.httpsRequest(c.Request)})
	}
}

// httpsRequest is the login cookie's TLS test (the connection is TLS, or a trusted proxy sent
// X-Forwarded-Proto: https). An untrusted client cannot turn the Secure flag on.
func (s *Server) httpsRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && strings.HasPrefix(la.Network(), "unix") {
		return true
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(peer))
	return ip != nil && s.isTrustedProxy(ip)
}

// gooCartHash is the cart's content hash (the cart fragments' cache key).
func gooCartHash(items []gooStoredItem) string {
	raw, _ := json.Marshal(items)
	return gooMD5(string(raw))
}

// gooAddNotice is wc_add_notice(): shown on the next store page.
func (s *Server) gooAddNotice(c *gin.Context, typ, msg string) {
	sess := s.gooSessionFor(c)
	sess.Data.Notices = append(sess.Data.Notices, gooNotice{Type: typ, Msg: msg})
	sess.touch()
}

// gooTakeNotices returns and clears the pending notices (wc_print_notices()).
func (s *Server) gooTakeNotices(c *gin.Context) []gooNotice {
	sess := s.gooSessionFor(c)
	n := sess.Data.Notices
	if len(n) > 0 {
		sess.Data.Notices = nil
		sess.touch()
	}
	return n
}

// gooHasNoticeErrors is wc_notice_count('error') > 0.
func (s *Server) gooHasNoticeErrors(c *gin.Context) bool {
	for _, n := range s.gooSessionFor(c).Data.Notices {
		if n.Type == "error" {
			return true
		}
	}
	return false
}

// gooCleanupSessions deletes expired sessions (the daily woocommerce_cleanup_sessions job).
func (s *Server) gooCleanupSessions(ctx context.Context) error {
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_sessions WHERE expires_at < now()`)
	return err
}

// gooCustomerEmail is the e-mail address the session knows: billing e-mail or the account's.
func (s *Server) gooCustomerEmail(c *gin.Context) string {
	if e := strings.TrimSpace(s.gooSessionFor(c).Data.Customer.Billing["email"]); e != "" {
		return e
	}
	if u := currentUser(c); u != nil {
		return u.Email
	}
	return ""
}
