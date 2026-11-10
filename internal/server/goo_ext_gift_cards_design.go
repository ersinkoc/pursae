package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// Gift card designs and the printable card. A design is a colour theme the buyer picks on the product
// page (the products enable a subset); the choice is kept in the order item's gift card meta, so no
// card column is needed. The e-mail carries a signed "print at home" link to a standalone page with
// print CSS (the browser's Print dialog saves it as a PDF).

type gcDesign struct{ Key, Label, Accent, Bg string }

var gcDesigns = []gcDesign{
	{"classic", "Classic", "#7f54b3", "#faf7fd"},
	{"birthday", "Birthday", "#e0457b", "#fff3f8"},
	{"holiday", "Holiday", "#2e7d32", "#f3faf3"},
	{"thanks", "Thank you", "#c77700", "#fff8ec"},
	{"minimal", "Minimal", "#333333", "#ffffff"},
}

func gcDesignFor(key string) gcDesign {
	for _, d := range gcDesigns {
		if d.Key == key {
			return d
		}
	}
	return gcDesigns[0]
}

func gcDesignKeys() []string {
	var out []string
	for _, d := range gcDesigns {
		out = append(out, d.Key)
	}
	return out
}

// gcParseDesigns keeps the known keys of a comma list, once each, in the given order.
func gcParseDesigns(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '|' }) {
		if !seen[k] && gcDesignFor(k).Key == k {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// gcProductDesigns are the designs a buyer may choose; empty means the product has no selector.
func gcProductDesigns(p *gooProduct) []string {
	return gcParseDesigns(gxStr(p.Meta["_gift_card_designs"]))
}

func (s *Server) gcPrintToken(id int64) string {
	m := hmac.New(sha256.New, []byte("goo-gift-card-print|"+s.siteSecret()))
	fmt.Fprintf(m, "%d", id)
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func (s *Server) gcPrintURL(ctx context.Context, g *gcCard) string {
	q := url.Values{"goo-ajax": {"print_gift_card"}, "card": {fmt.Sprint(g.ID)}, "token": {s.gcPrintToken(g.ID)}}
	return s.gooShopURL(ctx) + "?" + q.Encode()
}

// gcCardDesign finds the design chosen when the card was bought.
func (s *Server) gcCardDesign(ctx context.Context, g *gcCard) string {
	if g.Design != "" || g.OrderID == 0 {
		return g.Design
	}
	o, err := s.gooGetOrder(ctx, g.OrderID)
	if err != nil || o == nil {
		return ""
	}
	for _, it := range o.Items {
		if it.ID == g.OrderItemID {
			if m, ok := it.Meta["_goo_gift_card"].(map[string]any); ok {
				return gxStr(m["design"])
			}
		}
	}
	return ""
}

// gcPrintPage renders the card for printing; the signed token is the only credential.
func (s *Server) gcPrintPage(c *gin.Context) {
	ctx := c.Request.Context()
	id := int64(gxAtoi(c.Query("card")))
	tok := c.Query("token")
	g, err := s.gcGet(ctx, id)
	if err != nil || g == nil || !hmac.Equal([]byte(tok), []byte(s.gcPrintToken(id))) {
		c.String(http.StatusNotFound, "Gift card not found.")
		return
	}
	g.Design = s.gcCardDesign(ctx, g)
	d := gcDesignFor(g.Design)
	amount := s.gooPricePlain(g.Initial, g.Currency)
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html><head><meta charset="utf-8"><meta name="robots" content="noindex"><title>Gift card</title><style>`+
		`body{font-family:Arial,sans-serif;margin:0;padding:24px;color:#333}.card{max-width:560px;margin:0 auto;border:3px solid %s;background:%s;border-radius:14px;padding:32px;text-align:center}`+
		`.code{font-size:28px;font-weight:bold;letter-spacing:3px;margin:12px 0}.amt{font-size:22px;color:%s}.msg{font-style:italic;margin:16px 0}.note{font-size:12px;color:#777;margin-top:18px}`+
		`@media print{.noprint{display:none}body{padding:0}}</style></head><body><p class="noprint" style="text-align:center"><button onclick="window.print()">Print</button></p>`+
		`<div class="card"><div style="text-transform:uppercase;letter-spacing:2px;color:%s">%s gift card</div><div class="amt">%s</div>`, d.Accent, d.Bg, d.Accent, d.Accent, html.EscapeString(d.Label), html.EscapeString(amount))
	if g.Sender != "" {
		fmt.Fprintf(&b, `<p>From %s</p>`, html.EscapeString(g.Sender))
	}
	if g.Message != "" {
		fmt.Fprintf(&b, `<p class="msg">%s</p>`, strings.ReplaceAll(html.EscapeString(g.Message), "\n", "<br>"))
	}
	fmt.Fprintf(&b, `<div class="code">%s</div>`, html.EscapeString(g.Code))
	if g.Expires != nil {
		fmt.Fprintf(&b, `<div class="note">Expires on %s</div>`, html.EscapeString(s.gxDate(*g.Expires)))
	}
	fmt.Fprintf(&b, `<div class="note">Redeem at %s</div></div></body></html>`, html.EscapeString(s.gooShopURL(ctx)))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(b.String()))
}
