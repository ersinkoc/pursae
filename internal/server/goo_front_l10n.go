package server

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
)

// The store front in the site language. WordPress loads the "woocommerce" text domain on the front
// end too (get_locale(), or the current language with Polylang), so shop, cart, checkout and
// account pages speak the language the admin was set to. Pursae Commerce writes its markup in
// English like WooCommerce's templates and translates it after rendering with the same catalog
// as the admin: the WordPress.org WooCommerce pack (downloaded with the site language, rebranded),
// core's pack and Pursae's own strings (web/languages). Only the store's own markup is translated:
// the theme, menus and post content around it are the user's.

const gooCtxKeep = "goo.keepHTML"

// gooFrontLocale is the locale of the store front: the Golylang language of the request, else the
// site language.
func (s *Server) gooFrontLocale(ctx context.Context) string {
	if l := s.pllCurrent(ctx); l != nil && l.Locale != "" {
		return l.Locale
	}
	return s.siteLocale()
}

// gooFrontCatalog is the catalog of the store front's locale (empty for English).
func (s *Server) gooFrontCatalog(ctx context.Context) *i18n.Catalog {
	if s.lang == nil {
		return &i18n.Catalog{}
	}
	return s.lang.Catalog(s.gooFrontLocale(ctx))
}

// gooKeep records user content (a product description, a review) rendered into store markup, so
// gooL10n leaves it as it is. It returns h, marked as Multi Language user content (golylang_uc.go).
func (s *Server) gooKeep(c *gin.Context, h string) string {
	if c == nil || len(strings.TrimSpace(h)) < 2 {
		return h
	}
	list, _ := c.Get(gooCtxKeep)
	kept, _ := list.([]string)
	c.Set(gooCtxKeep, append(kept, h))
	return s.pllUC(c, h, 0) // Pursae Multi Language: user content, never page strings
}

// gooL10n translates store markup into the front end's language. User content recorded with
// gooKeep is swapped out for comments (which the translator copies through) and put back after.
func (s *Server) gooL10n(c *gin.Context, markup string) string {
	if c == nil || c.Request == nil || strings.TrimSpace(markup) == "" {
		return markup
	}
	cat := s.gooFrontCatalog(c.Request.Context())
	if cat.Empty() {
		return markup
	}
	list, _ := c.Get(gooCtxKeep)
	kept, _ := list.([]string)
	var used []string
	if len(kept) > 0 {
		kept = append([]string(nil), kept...)
		sort.SliceStable(kept, func(i, j int) bool { return len(kept[i]) > len(kept[j]) }) // a description before a paragraph of it
		for _, h := range kept {
			if strings.Contains(markup, h) {
				markup = strings.ReplaceAll(markup, h, gooKeepToken(len(used)))
				used = append(used, h)
			}
		}
	}
	out := string(cat.TranslateHTML([]byte(markup)))
	for i := len(used) - 1; i >= 0; i-- {
		out = strings.ReplaceAll(out, gooKeepToken(i), used[i])
	}
	return out
}

func gooKeepToken(i int) string { return "<!--goo-keep-" + strconv.Itoa(i) + "-->" }

// gooL10nText translates a plain store string (a page title, a script message).
func (s *Server) gooL10nText(ctx context.Context, text string) string {
	cat := s.gooFrontCatalog(ctx)
	if cat.Empty() {
		return text
	}
	return cat.Text(text)
}

// gooFieldName is a checkout field's name in an error: "Billing First name" (_x( 'Billing %s',
// 'checkout-validation' )) with the field label translated first, like WooCommerce.
func (s *Server) gooFieldName(c *gin.Context, prefix, label string) string {
	if c == nil || c.Request == nil {
		return prefix + " " + label
	}
	ctx := c.Request.Context()
	return s.gooL10nText(ctx, prefix+" "+s.gooL10nText(ctx, label))
}

// gooL10nf is sprintf( __( format ), args… ) in the store front's language, for messages too
// short to be found in rendered text ("via %s").
func (s *Server) gooL10nf(ctx context.Context, format string, args ...string) string {
	if tr, ok := s.gooFrontCatalog(ctx).Lookup(format); ok {
		format = tr
	}
	return i18n.Sprintf(format, args...)
}
