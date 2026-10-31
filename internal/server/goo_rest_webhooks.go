package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Webhooks (WC_Webhook): the active rows of goo_webhooks are delivered when their topic happens —
// order.created / updated / deleted, product.created / updated / deleted, customer.created / updated /
// deleted, coupon.created / updated / deleted. The payload is the wc/v3 resource; the request carries
// WooCommerce's X-WC-Webhook-* headers with a base64 HMAC-SHA256 signature of the body made with the
// webhook's secret. Deliveries run in the background; a webhook is disabled after 5 consecutive failures.

const gooWebhookMaxFailures = 5

// gooWebhookClient delivers webhooks (a variable so tests can swap it). Like wp_safe_remote_request,
// which WooCommerce delivers with, it connects to public addresses only: the address is checked when
// the connection is made, after DNS resolution and on every redirect, so a delivery URL cannot reach
// the server's own services, the private network or a cloud metadata endpoint.
var gooWebhookClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{TLSHandshakeTimeout: 10 * time.Second, DialContext: (&net.Dialer{Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if !gooWebhookAllowPrivate.Load() && !publicIP(net.ParseIP(host)) {
				return errors.New("webhook delivery URL points at a private network address")
			}
			return nil
		}}).DialContext},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("unsupported redirect scheme")
		}
		return nil
	},
}

// gooWebhookAllowPrivate lets tests deliver to httptest servers on the loopback address.
var gooWebhookAllowPrivate atomic.Bool

// gooWebhookWG tracks deliveries in flight (tests wait on it).
var gooWebhookWG sync.WaitGroup

func init() {
	gooOn("order_created", 50, func(s *Server, ctx context.Context, arg any) {
		if o, ok := arg.(*gooOrder); ok {
			s.gooWebhookOrder(ctx, "order.created", o)
		}
	})
	gooOn("order_status_changed", 50, func(s *Server, ctx context.Context, arg any) {
		if ev, ok := arg.(*gooStatusChange); ok {
			s.gooWebhookOrder(ctx, "order.updated", ev.Order)
		}
	})
	gooOn("order_partially_refunded", 50, func(s *Server, ctx context.Context, arg any) {
		if ev, ok := arg.(*gooRefundEvent); ok {
			s.gooWebhookOrder(ctx, "order.updated", ev.Order)
		}
	})
	gooOn("product_saved", 50, func(s *Server, ctx context.Context, arg any) {
		p, ok := arg.(*gooProduct)
		if !ok || !s.gooWebhooksFor(ctx, "product.created", "product.updated") {
			return
		}
		topic := "product.updated"
		var created, modified time.Time
		if s.store.Pool.QueryRow(ctx, `SELECT created_at, modified_at FROM posts WHERE id = $1`, p.ID).Scan(&created, &modified) == nil && modified.Sub(created) < time.Millisecond {
			topic = "product.created"
		}
		fresh, err := s.gooGetProduct(ctx, p.ID)
		if err != nil {
			return
		}
		payload := s.gooV3ProductJSON(ctx, fresh)
		if fresh.Type == gooTypeVariation {
			payload = s.gooV3VariationJSON(ctx, fresh)
		}
		s.gooWebhookFire(ctx, topic, p.ID, payload)
	})
	gooOn("customer_created", 50, func(s *Server, ctx context.Context, arg any) {
		var id int64
		switch v := arg.(type) {
		case *gooNewCustomer:
			id = v.UserID
		case int64:
			id = v
		}
		if id == 0 || !s.gooWebhooksFor(ctx, "customer.created") {
			return
		}
		if u, err := s.store.UserByID(ctx, id); err == nil {
			s.gooWebhookFire(ctx, "customer.created", id, s.gooV3CustomerJSON(ctx, u))
		}
	})
}

// gooWebhookRow is an active webhook.
type gooWebhookRow struct {
	ID         int64
	Topic, URL string
	Secret     string
	UserID     int64
	Failures   int
}

// gooWebhooksFor reports whether any active webhook listens to one of the topics.
func (s *Server) gooWebhooksFor(ctx context.Context, topics ...string) bool {
	if !s.gooOn() {
		return false
	}
	var ok bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_webhooks WHERE status = 'active' AND (topic = ANY($1) OR topic = 'action.*'))`, topics).Scan(&ok)
	return ok
}

// gooWebhookOrder delivers an order topic with the order's v3 representation.
func (s *Server) gooWebhookOrder(ctx context.Context, topic string, o *gooOrder) {
	if o == nil || o.Type != "shop_order" || !s.gooWebhooksFor(ctx, topic) {
		return
	}
	fresh, err := s.gooGetOrder(ctx, o.ID)
	if err != nil {
		return
	}
	s.gooWebhookFire(ctx, topic, o.ID, s.gooV3OrderJSON(ctx, fresh))
}

// gooWebhookCoupon delivers a coupon topic with the coupon's wc/v3 resource.
func (s *Server) gooWebhookCoupon(ctx context.Context, topic string, id int64) {
	if !s.gooWebhooksFor(ctx, topic) {
		return
	}
	if cp, err := s.gooCouponByID(ctx, id); err == nil {
		s.gooWebhookFire(ctx, topic, id, s.gooV3CouponJSON(ctx, cp))
	}
}

// gooWebhookProduct delivers a product topic with the product's wc/v3 resource.
func (s *Server) gooWebhookProduct(ctx context.Context, topic string, id int64) {
	if !s.gooWebhooksFor(ctx, topic) {
		return
	}
	if p, err := s.gooGetProduct(ctx, id); err == nil {
		s.gooWebhookFire(ctx, topic, id, s.gooV3ProductJSON(ctx, p))
	}
}

// gooWebhookFire delivers a topic to every active webhook subscribed to it, in the background.
func (s *Server) gooWebhookFire(ctx context.Context, topic string, resourceID int64, payload any) {
	if !s.gooOn() {
		return
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, topic, delivery_url, secret, user_id, failures FROM goo_webhooks WHERE status = 'active' AND topic = $1`, topic)
	if err != nil {
		return
	}
	var hooks []gooWebhookRow
	for rows.Next() {
		var h gooWebhookRow
		if rows.Scan(&h.ID, &h.Topic, &h.URL, &h.Secret, &h.UserID, &h.Failures) == nil {
			hooks = append(hooks, h)
		}
	}
	rows.Close()
	if len(hooks) == 0 {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	source := strings.TrimRight(s.links(ctx).Home, "/") + "/"
	bg := context.WithoutCancel(ctx)
	for _, h := range hooks {
		h := h
		gooWebhookWG.Add(1)
		go func() {
			defer gooWebhookWG.Done()
			s.gooWebhookDeliver(bg, h, source, resourceID, body)
		}()
	}
}

// gooWebhookSignature is WC_Webhook::generate_signature(): base64(HMAC-SHA256(body, secret)).
func gooWebhookSignature(body []byte, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// gooWebhookDeliver posts one delivery and records success or failure.
func (s *Server) gooWebhookDeliver(ctx context.Context, h gooWebhookRow, source string, resourceID int64, body []byte) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resource, event, _ := strings.Cut(h.Topic, ".")
	ok := false
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Goocommerce/"+gooVersion+" Hookshot (Pursae)")
		req.Header.Set("X-WC-Webhook-Source", source)
		req.Header.Set("X-WC-Webhook-Topic", h.Topic)
		req.Header.Set("X-WC-Webhook-Resource", resource)
		req.Header.Set("X-WC-Webhook-Event", event)
		req.Header.Set("X-WC-Webhook-Signature", gooWebhookSignature(body, h.Secret))
		req.Header.Set("X-WC-Webhook-ID", strconv.FormatInt(h.ID, 10))
		req.Header.Set("X-WC-Webhook-Delivery-ID", gooRandomKey(32))
		if resp, err := gooWebhookClient.Do(req); err == nil {
			ok = resp.StatusCode >= 200 && resp.StatusCode < 300
			_ = resp.Body.Close()
		}
	}
	if ok {
		_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_webhooks SET failures = 0 WHERE id = $1`, h.ID)
		return
	}
	var failures int
	if err := s.store.Pool.QueryRow(ctx, `UPDATE goo_webhooks SET failures = failures + 1,
		status = CASE WHEN failures + 1 >= $2 THEN 'disabled' ELSE status END WHERE id = $1 RETURNING failures`, h.ID, gooWebhookMaxFailures).Scan(&failures); err == nil && failures >= gooWebhookMaxFailures {
		log.Printf("goocommerce: webhook %d (%s) disabled after %d failed deliveries", h.ID, h.Topic, failures)
	}
}
