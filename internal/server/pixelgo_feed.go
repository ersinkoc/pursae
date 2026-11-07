package server

import (
	"context"
	"crypto/subtle"
	"encoding/xml"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// The product catalog feed: /pixelgo-feed/<token> in the Google Merchant / Meta Commerce Manager RSS 2.0
// format (g: namespace). One item per product (a variable product is listed once, at its lowest price),
// with the same ids the events send (id or SKU, plus the prefix and suffix of the settings), so the
// catalog and the events match. The token is the only access control; hidden, password-protected and
// unpublished products are left out.

var pixelTagRe = regexp.MustCompile(`(?s)<[^>]*>`)

type pixelFeedRow struct {
	ID             int64
	Title, Excerpt string
	Content        string
}

// pixelFeedIDs are the ids of the products that go into the feed.
func (s *Server) pixelFeedRows(ctx context.Context) ([]pixelFeedRow, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT po.id, po.title, po.excerpt, po.content FROM goo_products gp JOIN posts po ON po.id = gp.post_id
		WHERE po.status = 'publish' AND po.password = '' AND gp.type <> 'variation' AND gp.catalog_visibility <> 'hidden' ORDER BY po.id LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pixelFeedRow
	for rows.Next() {
		var r pixelFeedRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Excerpt, &r.Content); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func pixelPlain(v string, n int) string {
	v = pixelTagRe.ReplaceAllString(v, " ")
	v = strings.Join(strings.Fields(html.UnescapeString(v)), " ")
	if len([]rune(v)) > n {
		v = string([]rune(v)[:n])
	}
	return v
}

func pixelAvail(status string) string {
	switch status {
	case "outofstock":
		return "out of stock"
	case "onbackorder":
		return "backorder"
	}
	return "in stock"
}

// pixelFeed serves the feed.
func (s *Server) pixelFeed(c *gin.Context) {
	st := s.pixelLoad()
	if !st.FeedEnabled || st.FeedToken == "" || !s.gooOn() ||
		subtle.ConstantTimeCompare([]byte(strings.TrimSuffix(c.Param("token"), ".xml")), []byte(st.FeedToken)) != 1 {
		c.Status(http.StatusNotFound)
		return
	}
	ctx := c.Request.Context()
	rows, err := s.pixelFeedRows(ctx)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	products, err := s.gooGetProducts(ctx, ids)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	lb := s.links(ctx)
	cur := s.gooCurrency()
	brand := st.FeedBrand
	if brand == "" {
		brand = s.settings.String("blogname")
	}
	var b strings.Builder
	esc := func(v string) string { var sb strings.Builder; _ = xml.EscapeText(&sb, []byte(v)); return sb.String() }
	tag := func(name, v string) {
		if v != "" {
			b.WriteString("<g:" + name + ">" + esc(v) + "</g:" + name + ">")
		}
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<rss xmlns:g="http://base.google.com/ns/1.0" version="2.0"><channel><title>` + esc(s.settings.String("blogname")) + `</title><link>` + esc(lb.HomeURL("/")) +
		`</link><description>` + esc("Product catalog of "+s.settings.String("blogname")) + `</description>` + "\n")
	for _, r := range rows {
		p := products[r.ID]
		if p == nil || p.Type == "external" && p.ProductURL == "" {
			continue
		}
		price := pixelBasePrice(p)
		regular := price
		if p.RegularPrice != nil {
			regular = *p.RegularPrice
		}
		if regular <= 0 && price <= 0 {
			continue
		}
		desc := pixelPlain(r.Content, 5000)
		if e := pixelPlain(r.Excerpt, 5000); e != "" {
			desc = e
		}
		if desc == "" {
			desc = r.Title
		}
		b.WriteString("<item>")
		tag("id", pixelItemID(&st, p.ID, p.SKU))
		tag("title", pixelPlain(r.Title, 150))
		tag("description", desc)
		tag("link", s.gooPermalink(ctx, p))
		if img, _, _ := s.gooImageURL(ctx, p.ImageID, "full"); img != "" {
			if strings.HasPrefix(img, "/") {
				img = lb.HomeURL(img)
			}
			tag("image_link", img)
		}
		tag("availability", pixelAvail(p.StockStatus))
		if regular > price && price > 0 {
			tag("price", strconv.FormatFloat(regular, 'f', 2, 64)+" "+cur)
			tag("sale_price", strconv.FormatFloat(price, 'f', 2, 64)+" "+cur)
		} else {
			tag("price", strconv.FormatFloat(max(price, regular), 'f', 2, 64)+" "+cur)
		}
		tag("condition", st.FeedCondition)
		tag("brand", brand)
		tag("mpn", p.SKU)
		if p.SKU == "" {
			b.WriteString("<g:identifier_exists>no</g:identifier_exists>")
		}
		tag("google_product_category", st.FeedGoogleCat)
		if terms, err := s.gooProductTerms(c, p.ID); err == nil {
			for _, t := range terms {
				if t.Taxonomy == "product_cat" {
					tag("product_type", t.Name)
					break
				}
			}
		}
		b.WriteString("</item>\n")
	}
	b.WriteString("</channel></rss>\n")
	c.Header("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", []byte(b.String()))
}
