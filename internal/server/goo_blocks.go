package server

import (
	"context"
	"strconv"
	"strings"

	"wpgo/internal/blocks"
)

// Goocommerce's dynamic blocks (the server-rendered counterparts of WooCommerce's Products,
// Featured Product, Product Categories, Mini-Cart and Product Search blocks). Each block maps
// its attributes onto the matching shortcode and reuses the shortcode renderer, so the markup
// is identical to the shortcode's. They render nothing while the store is off.

func gooBlockAttr(b *blocks.Block, k string) string {
	switch v := b.Attrs[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, 0, len(v))
		for _, x := range v {
			switch n := x.(type) {
			case float64:
				parts = append(parts, strconv.FormatFloat(n, 'f', -1, 64))
			case string:
				parts = append(parts, strings.TrimSpace(n))
			}
		}
		return strings.Join(parts, ",")
	}
	return ""
}

// gooBlockRender expands tag with the block's attributes (attr name -> shortcode attribute).
func gooBlockRender(s *Server, tag string, ctx context.Context, b *blocks.Block, m map[string]string, defaults map[string]string) string {
	if !s.gooOn() {
		return ""
	}
	a := map[string]string{}
	for k, v := range defaults {
		a[k] = v
	}
	for attr, sc := range m {
		if v := gooBlockAttr(b, attr); v != "" {
			a[sc] = v
		}
	}
	c := gooCtxGin(ctx)
	out := s.gooL10n(c, s.gooShortcode(c, tag, a))
	if out == "" {
		return ""
	}
	return out
}

func registerGooBlocks(s *Server) {
	blocks.RegisterDynamic("goocommerce/products", func(ctx context.Context, b *blocks.Block, _ string) string {
		return gooBlockRender(s, "products", ctx, b, map[string]string{
			"columns": "columns", "limit": "limit", "category": "category", "orderby": "orderby",
			"order": "order", "onSale": "on_sale", "visibility": "visibility", "ids": "ids", "tag": "tag",
		}, map[string]string{"limit": "8", "columns": "4"})
	})
	blocks.RegisterDynamic("goocommerce/featured-product", func(ctx context.Context, b *blocks.Block, _ string) string {
		return gooBlockRender(s, "product", ctx, b, map[string]string{"productId": "id"}, nil)
	})
	blocks.RegisterDynamic("goocommerce/product-categories", func(ctx context.Context, b *blocks.Block, _ string) string {
		return gooBlockRender(s, "product_categories", ctx, b, map[string]string{
			"limit": "number", "hideEmpty": "hide_empty", "parent": "parent", "orderby": "orderby",
		}, nil)
	})
	blocks.RegisterDynamic("goocommerce/mini-cart", func(ctx context.Context, b *blocks.Block, _ string) string {
		return gooBlockRender(s, "goocommerce_mini_cart", ctx, b, map[string]string{"title": "title"}, nil)
	})
	blocks.RegisterDynamic("goocommerce/product-search", func(ctx context.Context, b *blocks.Block, _ string) string {
		return gooBlockRender(s, "goocommerce_product_search", ctx, b, map[string]string{"title": "title"}, nil)
	})
}
