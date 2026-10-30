package server

import (
	"context"
	"html"
	"html/template"
	"strconv"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Template functions for store themes (theme packages call them from their templates):
//
//	{{if goo_on}} … {{end}}
//	{{goo_cart_link .}}            the header cart link: a.cart-contents with the amount and the count
//	{{goo_cart_count .}}           items in the cart
//	{{goo_mini_cart .}}            the mini-cart (div.widget_shopping_cart_content)
//	{{goo_url . "shop"}}           shop, cart, checkout, myaccount page URLs
//	{{goo_product_search .}}       a product search form (post_type=product)
//	{{range goo_categories . 6}}   top-level product categories: .Name .URL .Count .Image
//	{{goo_language_switcher .}}    Golylang's languages of the current page (nav.goo-lang-switcher), empty without it
//
// Every function is inert while Goocommerce is off (empty output, zero counts), so a store theme
// still works as a plain theme.

// gooThemeCategory is a product category for a theme's template.
type gooThemeCategory struct {
	ID          int64
	Name, Slug  string
	URL, Image  string
	Description string
	Count       int
}

func gooViewCtx(v *siteView) (context.Context, *gin.Context) {
	if v == nil || v.ctx == nil {
		return context.Background(), nil
	}
	if rc := renderFrom(v.ctx); rc != nil {
		return v.ctx, rc.c
	}
	return v.ctx, nil
}

func (s *Server) gooTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"goo_on": func() bool { return s.gooOn() },
		"goo_cart_count": func(v *siteView) int {
			_, c := gooViewCtx(v)
			if !s.gooOn() || c == nil {
				return 0
			}
			return s.gooCartFor(c).Count()
		},
		"goo_cart_link": func(v *siteView) template.HTML {
			_, c := gooViewCtx(v)
			if !s.gooOn() || c == nil {
				return ""
			}
			return s.gooThemeCartLink(c)
		},
		"goo_mini_cart": func(v *siteView) template.HTML {
			_, c := gooViewCtx(v)
			if !s.gooOn() || c == nil {
				return ""
			}
			return s.gooThemeMiniCart(c)
		},
		"goo_url": func(v *siteView, page string) string {
			ctx, _ := gooViewCtx(v)
			if !s.gooOn() {
				return s.links(ctx).HomeURL("/")
			}
			if page == "shop" {
				return s.gooShopURL(ctx)
			}
			return s.gooPageURL(ctx, page)
		},
		"goo_product_search": func(v *siteView) template.HTML {
			ctx, _ := gooViewCtx(v)
			if !s.gooOn() {
				return ""
			}
			home := html.EscapeString(s.links(ctx).HomeURL("/"))
			return template.HTML(`<form role="search" method="get" class="woocommerce-product-search" action="` + home + `">` +
				`<label class="screen-reader-text" for="woocommerce-product-search-field">Search for:</label>` +
				`<input type="search" id="woocommerce-product-search-field" class="search-field" placeholder="Search products&hellip;" value="" name="s">` +
				`<button type="submit" value="Search">Search</button><input type="hidden" name="post_type" value="product"></form>`)
		},
		"goo_language_switcher": func(v *siteView) template.HTML { // Golylang: the languages of the current page (goo_golylang.go)
			_, c := gooViewCtx(v)
			if c == nil || v == nil {
				return ""
			}
			return s.gooPllSwitcher(c, v.Q)
		},
		"goo_categories": func(v *siteView, n int) []gooThemeCategory {
			ctx, _ := gooViewCtx(v)
			if !s.gooOn() {
				return nil
			}
			return s.gooThemeCategories(ctx, n)
		},
	}
}

// gooThemeCartLink is the header cart link (storefront_cart_link()).
func (s *Server) gooThemeCartLink(c *gin.Context) template.HTML {
	if h, ok := gooCartLinkHTML(s, c); ok {
		return h
	}
	ctx := c.Request.Context()
	cart := s.gooCartFor(c)
	n := cart.Count()
	label := strconv.Itoa(n) + " items"
	if n == 1 {
		label = "1 item"
	}
	return template.HTML(`<a class="cart-contents" href="` + html.EscapeString(s.gooPageURL(ctx, "cart")) + `" title="View your shopping cart">` +
		string(s.gooPrice(cart.displayedSubtotal())) + ` <span class="count">` + label + `</span></a>`)
}

// gooThemeMiniCart is the mini-cart for theme headers.
func (s *Server) gooThemeMiniCart(c *gin.Context) template.HTML {
	if h, ok := gooMiniCartHTML(s, c); ok {
		return h
	}
	return template.HTML(`<div class="widget_shopping_cart_content"></div>`)
}

// gooCartLinkHTML / gooMiniCartHTML let the storefront provide the canonical markup (goo_front.go).
var (
	gooCartLinkHTML = func(s *Server, c *gin.Context) (template.HTML, bool) { return s.gooCartLink(c), true }
	gooMiniCartHTML = func(s *Server, c *gin.Context) (template.HTML, bool) { return s.gooMiniCart(c), true }
)

// gooThemeCategories are the top-level product categories with products, by name.
func (s *Server) gooThemeCategories(ctx context.Context, n int) []gooThemeCategory {
	terms, _, err := s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: "product_cat", OrderBy: "name"})
	if err != nil {
		return nil
	}
	lb := s.links(ctx)
	var out []gooThemeCategory
	for _, t := range terms {
		if t.ParentID != 0 || t.Slug == "uncategorized" {
			continue
		}
		tc := gooThemeCategory{ID: t.ID, Name: t.Name, Slug: t.Slug, Description: t.Description, Count: t.Count,
			URL: lb.HomeURL("/" + defaultStr(s.gooOptString("category_base"), "product-category") + "/" + t.Slug + "/")}
		if id, ok := t.Meta["thumbnail_id"].(float64); ok && id > 0 {
			if a, err := s.store.GetAttachment(ctx, int64(id)); err == nil {
				tc.Image = s.uploadURL(ctx, a.FilePath)
				if v := a.Variant("medium_large"); v != nil {
					tc.Image = s.uploadURL(ctx, v.FilePath)
				}
			}
		}
		out = append(out, tc)
		if n > 0 && len(out) >= n {
			break
		}
	}
	return out
}
