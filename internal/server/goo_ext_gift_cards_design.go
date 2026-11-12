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
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Gift card designs and the printable card. A design is a colour theme the buyer picks on the product
// page (the products enable a subset), optionally with an image from the Media Library (module
// settings › Designs, like WooCommerce Gift Cards' card images); the choice is kept in the order item's
// gift card meta, so no card column is needed. The e-mail shows the design's image, carries the card as
// a PDF attachment (the pure-Go writer of PDF Invoices) and a signed "print at home" link to a
// standalone page with print CSS and a PDF download.

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

// gcDesignFields are the module settings of the designs' Media Library images.
func gcDesignFields() []gooField {
	out := []gooField{{Type: "title", Title: "Designs", Desc: "An image for each design, shown on top of the gift card e-mail, the printable card and its PDF. Leave empty for the colour-only design."}}
	for _, d := range gcDesigns {
		out = append(out, gooField{ID: "design_image_" + d.Key, Title: d.Label, Type: "image", Default: float64(0)})
	}
	return append(out, gooField{Type: "sectionend"})
}

// gcDesignImage is the attachment ID of a design's image (0 = none).
func (s *Server) gcDesignImage(key string) int64 {
	return int64(s.gooExtInt(gcKey, "design_image_"+gcDesignFor(key).Key))
}

// gcDesignImageURL is the public URL of a design's image ("" = none).
func (s *Server) gcDesignImageURL(ctx context.Context, key string) string {
	id := s.gcDesignImage(key)
	if id <= 0 {
		return ""
	}
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil || !strings.HasPrefix(a.Mime, "image/") {
		return ""
	}
	return s.uploadURL(ctx, a.FilePath)
}

// gcHexRGB turns "#rrggbb" into PDF colour components.
func gcHexRGB(hex string) (float64, float64, float64) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0.5, 0.5, 0.5
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0.5, 0.5, 0.5
	}
	return float64(n>>16&0xff) / 255, float64(n>>8&0xff) / 255, float64(n&0xff) / 255
}

// gcPDF renders a gift card as a one-page PDF in its design (paper size of the PDF Invoices settings).
func (s *Server) gcPDF(ctx context.Context, g *gcCard) []byte {
	w, h := gooPDFA4W, gooPDFA4H
	if s.gooExtString(gooPDFKey, "paper_size") == "letter" {
		w, h = gooPDFLetterW, gooPDFLetterH
	}
	p := newGooPDF(w, h)
	p.Title = "Gift card " + g.Code
	key := s.gcCardDesign(ctx, g)
	d := gcDesignFor(key)
	img, iw, ih := -1, 0.0, 0.0
	x, cw := 60.0, w-120
	inner := cw - 48
	if data := s.gooUploadsBytes(ctx, strconv.FormatInt(s.gcDesignImage(key), 10)); data != nil {
		if i, err := p.AddJPEG(data); err == nil {
			if pw, ph := p.ImageSize(i); pw > 0 && ph > 0 {
				img, iw = i, inner
				ih = iw * float64(ph) / float64(pw)
				if ih > 220 {
					ih, iw = 220, 220*float64(pw)/float64(ph)
				}
			}
		}
	}
	var msg []string
	if g.Message != "" {
		msg = gooPDFWrap(g.Message, 12, false, inner)
	}
	// Card height: padding, image, title, amount, sender, message, code, expiry, shop line.
	height := 48 + 24 + 34 + 46 + float64(len(msg))*16 + 30
	if img >= 0 {
		height += ih + 20
	}
	if g.Sender != "" {
		height += 20
	}
	if g.Expires != nil {
		height += 18
	}
	height += 18
	y := 60.0
	r, gr, bl := gcHexRGB(d.Accent)
	p.RectRGB(x, y, cw, height, r, gr, bl)
	br, bg, bb := gcHexRGB(d.Bg)
	p.RectRGB(x+4, y+4, cw-8, height-8, br, bg, bb)
	cx := w / 2
	y += 24
	if img >= 0 {
		p.Image(img, cx-iw/2, y, iw, ih)
		y += ih + 20
	}
	p.SetGray(0.35)
	y += 12
	p.TextCenterOn(-1, cx, y, 12, true, strings.ToUpper(d.Label+" gift card"))
	y += 34
	p.SetGray(0)
	p.TextCenterOn(-1, cx, y, 26, true, s.gooPricePlain(g.Initial, g.Currency))
	if g.Sender != "" {
		y += 20
		p.SetGray(0.2)
		p.TextCenterOn(-1, cx, y, 12, false, "From "+g.Sender)
	}
	y += 10
	for _, l := range msg {
		y += 16
		p.SetGray(0.25)
		p.TextCenterOn(-1, cx, y, 12, false, l)
	}
	y += 36
	p.SetGray(0)
	p.TextCenterOn(-1, cx, y, 22, true, g.Code)
	p.SetGray(0.45)
	if g.Expires != nil {
		y += 18
		p.TextCenterOn(-1, cx, y, 10, false, "Expires on "+s.gxDate(*g.Expires))
	}
	y += 18
	p.TextCenterOn(-1, cx, y, 10, false, "Redeem at "+s.gooShopURL(ctx))
	return p.Bytes()
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
	if c.Query("format") == "pdf" {
		gooPDFStream(c, s.gcPDF(ctx, g), "gift-card-"+g.Code+".pdf", c.Query("download") == "")
		return
	}
	g.Design = s.gcCardDesign(ctx, g)
	d := gcDesignFor(g.Design)
	imgURL := s.gcDesignImageURL(ctx, g.Design)
	amount := s.gooPricePlain(g.Initial, g.Currency)
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html><head><meta charset="utf-8"><meta name="robots" content="noindex"><title>Gift card</title><style>`+
		`body{font-family:Arial,sans-serif;margin:0;padding:24px;color:#333}.card{max-width:560px;margin:0 auto;border:3px solid %s;background:%s;border-radius:14px;padding:32px;text-align:center}`+
		`.code{font-size:28px;font-weight:bold;letter-spacing:3px;margin:12px 0}.amt{font-size:22px;color:%s}.msg{font-style:italic;margin:16px 0}.note{font-size:12px;color:#777;margin-top:18px}`+
		`.card img{max-width:100%%;max-height:260px;border-radius:8px;margin-bottom:16px}@media print{.noprint{display:none}body{padding:0}}</style></head><body><p class="noprint" style="text-align:center"><button onclick="window.print()">Print</button> <a href="%s">Download PDF</a></p>`+
		`<div class="card">%s<div style="text-transform:uppercase;letter-spacing:2px;color:%s">%s gift card</div><div class="amt">%s</div>`, d.Accent, d.Bg, d.Accent, html.EscapeString(s.gcPrintURL(ctx, g)+"&format=pdf&download=1"), gcImgTag(imgURL), d.Accent, html.EscapeString(d.Label), html.EscapeString(amount))
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

func gcImgTag(u string) string {
	if u == "" {
		return ""
	}
	return `<img src="` + html.EscapeString(u) + `" alt="">`
}
