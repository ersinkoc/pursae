package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/xml"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	ParentID       int64 // a variation: its variable product
	Title, Excerpt string
	Content        string
}

// pixelFeedPage is how many rows a page of the feed holds: the feed is read and written page by page, so
// a catalog of any size streams with a bounded memory.
const pixelFeedPage = 500

// pixelFeedRows reads the next page of the feed after the post id `after`: published, visible, unprotected
// products in id order. With variations on, a variable product is replaced by its published variations
// (each a row with the parent's text); otherwise only the products are listed (a variable one once, at its
// lowest price).
func (s *Server) pixelFeedRows(ctx context.Context, after int64, variations bool) ([]pixelFeedRow, error) {
	q := `SELECT po.id, 0::bigint, po.title, po.excerpt, po.content FROM goo_products gp JOIN posts po ON po.id = gp.post_id
		WHERE po.status = 'publish' AND po.password = '' AND gp.type <> 'variation' AND gp.catalog_visibility <> 'hidden' AND po.id > $1
		AND ($2::bool = false OR gp.type <> 'variable' OR NOT EXISTS (SELECT 1 FROM posts v WHERE v.parent_id = po.id AND v.type = 'product_variation' AND v.status = 'publish'))`
	if variations {
		q += ` UNION ALL SELECT po.id, pp.id, pp.title, pp.excerpt, pp.content FROM posts po
			JOIN posts pp ON pp.id = po.parent_id JOIN goo_products gpp ON gpp.post_id = pp.id
			WHERE po.status = 'publish' AND po.type = 'product_variation' AND po.id > $1 AND pp.status = 'publish' AND pp.password = ''
			AND gpp.catalog_visibility <> 'hidden'`
	}
	q += ` ORDER BY 1 LIMIT ` + strconv.Itoa(pixelFeedPage)
	rows, err := s.store.Pool.Query(ctx, q, after, variations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pixelFeedRow
	for rows.Next() {
		var r pixelFeedRow
		if err := rows.Scan(&r.ID, &r.ParentID, &r.Title, &r.Excerpt, &r.Content); err != nil {
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

// pixelFeedCache keeps the last feed for a few minutes: the catalog crawlers fetch it often and building
// it reads every product. Feeds above the size limit are not kept.
var pixelFeedCache struct {
	sync.Mutex
	key  string
	at   time.Time
	data []byte
}

const (
	pixelFeedTTL      = 5 * time.Minute
	pixelFeedCacheMax = 8 << 20
)

var pixelFeedAttrs = map[string]string{"color": "color", "colour": "color", "size": "size", "material": "material", "pattern": "pattern", "gender": "gender"}

// pixelFeed serves the feed.
func (s *Server) pixelFeed(c *gin.Context) {
	st := s.pixelLoad()
	if !st.FeedEnabled || st.FeedToken == "" || !s.gooOn() ||
		subtle.ConstantTimeCompare([]byte(strings.TrimSuffix(c.Param("token"), ".xml")), []byte(st.FeedToken)) != 1 {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "private, max-age=300")
	key := strings.Join([]string{st.FeedToken, st.ContentID, st.IDPrefix, st.IDSuffix, st.FeedBrand, st.FeedCondition, st.FeedGoogleCat, strconv.FormatBool(st.VariationIDs),
		s.settings.String("blogname"), s.gooCurrency()}, "\x00")
	pixelFeedCache.Lock()
	if pixelFeedCache.key == key && time.Since(pixelFeedCache.at) < pixelFeedTTL {
		data := pixelFeedCache.data
		pixelFeedCache.Unlock()
		c.Data(http.StatusOK, "application/xml; charset=utf-8", data)
		return
	}
	pixelFeedCache.Unlock()

	ctx := c.Request.Context()
	lb := s.links(ctx)
	cur := s.gooCurrency()
	brand := st.FeedBrand
	if brand == "" {
		brand = s.settings.String("blogname")
	}
	var keep bytes.Buffer // a copy for the cache, dropped when the feed grows past the limit
	keeping := true
	c.Status(http.StatusOK)
	c.Header("Content-Type", "application/xml; charset=utf-8")
	var b strings.Builder
	flush := func() {
		if keeping {
			keep.WriteString(b.String())
			keeping = keep.Len() <= pixelFeedCacheMax
		}
		_, _ = c.Writer.WriteString(b.String())
		b.Reset()
	}
	esc := func(v string) string { var sb strings.Builder; _ = xml.EscapeText(&sb, []byte(v)); return sb.String() }
	tag := func(name, v string) {
		if v != "" {
			b.WriteString("<g:" + name + ">" + esc(v) + "</g:" + name + ">")
		}
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<rss xmlns:g="http://base.google.com/ns/1.0" version="2.0"><channel><title>` + esc(s.settings.String("blogname")) + `</title><link>` + esc(lb.HomeURL("/")) +
		`</link><description>` + esc("Product catalog of "+s.settings.String("blogname")) + `</description>` + "\n")
	var after int64
	for {
		rows, err := s.pixelFeedRows(ctx, after, st.VariationIDs)
		if err != nil {
			if after == 0 && len(keep.Bytes()) == 0 && !c.Writer.Written() {
				c.Status(http.StatusInternalServerError)
				return
			}
			break // the head is out already: end the document where it is
		}
		if len(rows) == 0 {
			break
		}
		after = rows[len(rows)-1].ID
		ids := make([]int64, 0, len(rows)*2)
		for _, r := range rows {
			ids = append(ids, r.ID)
			if r.ParentID > 0 {
				ids = append(ids, r.ParentID)
			}
		}
		products, err := s.gooGetProducts(ctx, ids)
		if err != nil {
			break
		}
		for _, r := range rows {
			p := products[r.ID]
			if p == nil || p.Type == "external" && p.ProductURL == "" {
				continue
			}
			var parent *gooProduct // set for a variation
			if r.ParentID > 0 {
				if parent = products[r.ParentID]; parent == nil {
					continue
				}
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
			if p.Description != "" && parent != nil {
				desc = pixelPlain(p.Description, 5000)
			}
			title := r.Title
			if parent != nil {
				title = s.gooVariationTitle(ctx, parent, p)
			}
			if desc == "" {
				desc = title
			}
			imageID := p.ImageID
			if imageID == 0 && parent != nil {
				imageID = parent.ImageID
			}
			b.WriteString("<item>")
			tag("id", pixelItemID(&st, p.ID, p.SKU))
			if parent != nil {
				tag("item_group_id", pixelItemID(&st, parent.ID, parent.SKU))
			}
			tag("title", pixelPlain(title, 150))
			tag("description", desc)
			tag("link", s.gooPermalink(ctx, p))
			if img, _, _ := s.gooImageURL(ctx, imageID, "full"); img != "" {
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
			if parent != nil {
				keys := make([]string, 0, len(p.VariationAttrs))
				for k := range p.VariationAttrs {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				done := map[string]bool{}
				for _, k := range keys {
					if g := pixelFeedAttrs[strings.ToLower(strings.TrimPrefix(k, "pa_"))]; g != "" && !done[g] && p.VariationAttrs[k] != "" {
						done[g] = true
						tag(g, strings.ReplaceAll(p.VariationAttrs[k], "-", " "))
					}
				}
			}
			tag("google_product_category", st.FeedGoogleCat)
			termsOf := r.ID
			if parent != nil {
				termsOf = parent.ID
			}
			if terms, err := s.gooProductTerms(c, termsOf); err == nil {
				for _, t := range terms {
					if t.Taxonomy == "product_cat" {
						tag("product_type", t.Name)
						break
					}
				}
			}
			b.WriteString("</item>\n")
		}
		flush()
		if len(rows) < pixelFeedPage {
			break
		}
	}
	b.WriteString("</channel></rss>\n")
	flush()
	if keeping && ctx.Err() == nil {
		pixelFeedCache.Lock()
		pixelFeedCache.key, pixelFeedCache.at, pixelFeedCache.data = key, time.Now(), append([]byte(nil), keep.Bytes()...)
		pixelFeedCache.Unlock()
	}
}
