package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Goocommerce with Golylang, the way Polylang for WooCommerce works:
//
//   - products, product categories, tags and global attribute terms are translated objects (always on,
//     like posts and pages); variations follow their product and are linked as translations of the
//     source variations;
//   - what is not text is shared: a save copies prices, SKU, stock, tax, shipping, downloads, images
//     (their translations when media are translated), attributes (the translated terms), linked
//     products (their translations) and the variations to every translation; a stock change reaches
//     every translation at once; a SKU may repeat inside a translation group;
//   - the shop, cart, checkout, my account and terms pages are resolved in the current language, so
//     a translated cart page is the cart in that language; product, category and tag URLs carry their
//     own language; the cart shows the products in the visitor's language;
//   - store strings (notice, e-mail subjects, headings and footer, payment and shipping titles) are
//     registered in Languages › Translations, translated on the front end, and e-mails go out in the
//     language the order was placed in;
//   - the language switcher and hreflang links know the store views.

// gooPllOn reports whether both plugins are on and at least one language exists.
func (s *Server) gooPllOn(ctx context.Context) bool {
	return s.golylangOn() && s.gooOn() && len(s.pllLanguages(ctx)) > 0
}

// gooPllPostTypes are the store's translated post types (added to pllPostTypes).
func (s *Server) gooPllPostTypes() []string {
	if !s.gooOn() {
		return nil
	}
	return []string{"product"}
}

var gooPllAttrCache struct {
	sync.Mutex
	at    time.Time
	taxes []string
}

// gooPllTaxonomies are the store's translated taxonomies (added to pllTaxonomies): product categories,
// tags and the global attributes (cached for a minute; attribute saves flush it).
func (s *Server) gooPllTaxonomies() []string {
	if !s.gooOn() {
		return nil
	}
	gooPllAttrCache.Lock()
	defer gooPllAttrCache.Unlock()
	if time.Since(gooPllAttrCache.at) > time.Minute {
		var taxes []string
		if attrs, err := s.gooAttributes(context.Background()); err == nil {
			for _, a := range attrs {
				taxes = append(taxes, a.Taxonomy())
			}
		}
		gooPllAttrCache.taxes, gooPllAttrCache.at = taxes, time.Now()
	}
	return append([]string{"product_cat", "product_tag"}, gooPllAttrCache.taxes...)
}

// gooPllFlushAttributes forgets the attribute taxonomies (an attribute was added or deleted).
func gooPllFlushAttributes() {
	gooPllAttrCache.Lock()
	gooPllAttrCache.at = time.Time{}
	gooPllAttrCache.Unlock()
}

// gooPllUnfiltered is ctx without the language filter: products asked for by id (cart items, orders,
// linked products) load whatever their language.
func gooPllUnfiltered(ctx context.Context) context.Context {
	r := pllFrom(ctx)
	if r == nil || !r.Filter {
		return ctx
	}
	nr := &pllReq{srv: r.srv, Lang: r.Lang, Front: r.Front, Path: r.Path}
	return context.WithValue(ctx, pllCtxKey{}, nr)
}

// gooPllProductCond is the language condition of the catalog query (gooQueryProducts): products in
// the request language, and products without one in the default language. alias is the posts alias.
func (s *Server) gooPllProductCond(ctx context.Context, alias string, add func(any) string) string {
	r := pllFrom(ctx)
	if r == nil || !r.Filter || r.Lang == nil || !s.golylangOn() {
		return ""
	}
	c := "(" + alias + ".type <> 'product' OR " + alias + ".id IN (SELECT post_id FROM pll_post_langs WHERE lang = " + add(r.Lang.Slug) + ")"
	if r.Lang.IsDefault {
		c += " OR NOT EXISTS (SELECT 1 FROM pll_post_langs gl WHERE gl.post_id = " + alias + ".id)"
	}
	return c + ")"
}

// gooTermsPage is the published Terms and conditions page in the request language, or nil:
// wc_terms_and_conditions_checkbox_enabled(). The checkbox is shown, and required, only when it is set.
func (s *Server) gooTermsPage(ctx context.Context) *store.Post {
	tid := s.gooPageIDIn(ctx, "terms")
	if tid <= 0 {
		return nil
	}
	if tp, err := s.store.GetPost(ctx, tid); err == nil && tp.Status == "publish" {
		return tp
	}
	return nil
}

// gooPageIDIn is the store page of a key in the current front-end language (wc_get_page_id() filtered
// by Polylang for WooCommerce): its translation when it has a published one, the page itself otherwise.
func (s *Server) gooPageIDIn(ctx context.Context, key string) int64 {
	id := s.gooPageID(key)
	if id <= 0 || !s.golylangOn() {
		return id
	}
	r := pllFrom(ctx)
	if r == nil || !r.Front || r.Lang == nil {
		return id
	}
	if t := s.pllGet(ctx, pllPost, id, r.Lang.Slug); t > 0 && t != id {
		if p, err := s.store.GetPost(ctx, t); err == nil && p.Status == "publish" {
			return t
		}
	}
	return id
}

// gooPageIs reports whether id is the store page of a key or one of its translations.
func (s *Server) gooPageIs(ctx context.Context, id int64, key string) bool {
	pid := s.gooPageID(key)
	if id <= 0 || pid <= 0 {
		return false
	}
	if id == pid {
		return true
	}
	if !s.golylangOn() {
		return false
	}
	for _, t := range s.pllTranslations(ctx, pllPost, pid) {
		if t == id {
			return true
		}
	}
	return false
}

// gooPllPagePaths are the paths of a store page and its translations (cache exclusions).
func (s *Server) gooPllPagePaths(ctx context.Context, id int64) []int64 {
	if !s.golylangOn() {
		return []int64{id}
	}
	var out []int64
	for _, t := range s.pllTranslations(ctx, pllPost, id) {
		out = append(out, t)
	}
	if len(out) == 0 {
		out = append(out, id)
	}
	return out
}

// gooPllSitePath strips the language part off a URL of the site (the path site() routes by).
func (s *Server) gooPllSitePath(ctx context.Context, link string) string {
	if !s.golylangOn() || len(s.pllLanguages(ctx)) == 0 {
		return link
	}
	root := s.links(ctx).Home
	if rel, ok := s.pllRelative(ctx, root, link, s.pllOpts()); ok {
		return root + rel
	}
	return link
}

// gooPllLink gives a product or term URL the language of its object (links built with HomeURL carry the
// current language).
func (s *Server) gooPllLink(ctx context.Context, kind string, obj any, link string) string {
	if !s.golylangOn() {
		return link
	}
	lb := s.links(ctx)
	if lb.LinkFilter == nil {
		return link
	}
	return lb.LinkFilter(kind, obj, link)
}

// gooPllSlugBases are the store's URL bases Golylang's Translate slugs translates (product, product
// category and product tag bases).
func (s *Server) gooPllSlugBases() [][2]string {
	if !s.gooOn() {
		return nil
	}
	return [][2]string{
		{"product", s.gooProductBase()},
		{"product_cat", strings.Trim(defaultStr(s.gooOptString("category_base"), "product-category"), "/")},
		{"product_tag", strings.Trim(defaultStr(s.gooOptString("tag_base"), "product-tag"), "/")},
	}
}

// Store views and the language switcher -------------------------------------------------------------

// gooPllResolvedLang is the language of a store view (the product, the category, the shop page): ok is
// false for other views. golylangResolved makes it the current language.
func (s *Server) gooPllResolvedLang(c *gin.Context, q *siteQuery) (string, bool) {
	if q == nil || q.Post == nil || q.Post.Meta["_goo_synthetic"] == nil {
		return "", false
	}
	fv := gooViewFrom(c)
	if fv == nil {
		return "", true
	}
	switch {
	case fv.Product != nil:
		return s.pllObjLang(c, pllPost, fv.Product.ID), true
	case fv.Term != nil:
		if s.pllTranslatedTaxonomy(fv.Term.Taxonomy) {
			return s.pllObjLang(c, pllTerm, fv.Term.ID), true
		}
	case fv.PageID > 0:
		return s.pllObjLang(c, pllPost, fv.PageID), true
	}
	return "", true
}

// gooPllTranslationURL is the URL of a store view in another language (switcher, hreflang); ok is false
// for other views.
func (s *Server) gooPllTranslationURL(c *gin.Context, q *siteQuery, l *pllLanguage) (u string, has, ok bool) {
	if q == nil || q.Post == nil || q.Post.Meta["_goo_synthetic"] == nil {
		return "", false, false
	}
	fv := gooViewFrom(c)
	if fv == nil {
		return "", false, true
	}
	ctx := c.Request.Context()
	switch {
	case fv.Product != nil:
		if t := s.pllGet(ctx, pllPost, fv.Product.ID, l.Slug); t > 0 {
			if p, err := s.gooGetProduct(ctx, t); err == nil && p.Status == "publish" {
				return s.gooPermalink(ctx, p), true, true
			}
		}
		return "", false, true
	case fv.Term != nil:
		if t := s.pllGet(ctx, pllTerm, fv.Term.ID, l.Slug); t > 0 {
			if tm, err := s.store.GetTerm(ctx, t); err == nil {
				return s.gooTermLink(ctx, tm), true, true
			}
		}
		return "", false, true
	case fv.PageID > 0:
		if t := s.pllGet(ctx, pllPost, fv.PageID, l.Slug); t > 0 {
			if p, err := s.store.GetPost(ctx, t); err == nil && p.Status == "publish" {
				link := s.links(ctx).Permalink(p)
				if fv.Endpoint != "" && fv.Kind != "shop" { // my-account/orders/ → mein-konto/orders/
					link = strings.TrimRight(link, "/") + "/" + s.gooEndpointSlug(fv.Endpoint) + "/"
					if fv.Value != "" {
						link += url.PathEscape(fv.Value) + "/"
					}
				}
				return link, true, true
			}
		}
		return "", false, true
	}
	return "", false, true
}

// gooPllSwitcher is the language switcher of the store themes ({{goo_language_switcher .}}): a list of
// languages with the URL of the current page in each, empty while Golylang is off.
func (s *Server) gooPllSwitcher(c *gin.Context, q *siteQuery) template.HTML {
	if c == nil || !s.golylangOn() || len(s.pllActiveLanguages(c)) < 2 {
		return ""
	}
	a := pllDefaultArgs()
	a.ShowFlags, a.DisplayNamesAs = true, "slug" // languages without published content stay hidden (hide_if_empty)
	items := s.pllSwitcherItems(c, q, a)
	if len(items) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<nav class="goo-lang-switcher" aria-label="Language"><ul>`)
	for _, it := range items {
		cls := it.Classes
		fmt.Fprintf(&b, `<li class="%s"><a lang="%s" hreflang="%s" href="%s"%s title="%s">`, html.EscapeString(cls), html.EscapeString(it.Lang.W3C()),
			html.EscapeString(it.Lang.W3C()), html.EscapeString(it.URL), map[bool]string{true: ` aria-current="true"`}[it.Current], html.EscapeString(it.Lang.Name))
		if f := it.Lang.FlagEmoji(); f != "" {
			b.WriteString(`<span class="goo-lang-flag" aria-hidden="true">` + f + `</span>`)
		}
		b.WriteString(`<span class="goo-lang-code">` + html.EscapeString(strings.ToUpper(it.Lang.Slug)) + `</span></a></li>`)
	}
	b.WriteString(`</ul></nav>`)
	return template.HTML(b.String())
}

// Admin -------------------------------------------------------------------------------------------------

// gooPllProductColumns adds a column per language to the products list (pencil, tick or plus).
func (s *Server) gooPllProductColumns(c *gin.Context, d *gooProductsData, products []*gooProduct) {
	if !s.gooPllOn(c) || len(products) == 0 {
		return
	}
	posts := make([]*store.Post, 0, len(products))
	for _, p := range products {
		if p.Post == nil {
			return
		}
		posts = append(posts, p.Post)
	}
	ld := &postsListData{}
	for range d.Rows {
		ld.Rows = append(ld.Rows, postRow{})
	}
	if len(ld.Rows) != len(posts) {
		return
	}
	s.golylangPostColumns(c, ld, posts)
	d.PluginHeads = ld.PluginHeads
	for i := range d.Rows {
		d.Rows[i].PluginCols = ld.Rows[i].PluginCols
	}
}

// gooPllNewTranslation fills a new product translation (post-new?post_type=product&from_post=…&new_lang=…)
// from its source: Golylang links it and copies the post data, then the store data and the variations
// follow.
func (s *Server) gooPllNewTranslation(c *gin.Context, p *gooProduct) {
	if !s.gooPllOn(c) || p.Post == nil {
		return
	}
	s.golylangNewPost(c, p.Post)
	from, _ := strconv.ParseInt(c.Query("from_post"), 10, 64)
	if from <= 0 || s.pllGet(c, pllPost, from, s.pllObjLang(c, pllPost, p.ID)) != p.ID {
		return
	}
	src, err := s.gooGetProduct(c, from)
	if err != nil || src.Type == gooTypeVariation {
		return
	}
	lang := s.pllObjLang(c, pllPost, p.ID)
	if post, err := s.store.GetPost(c, p.ID); err == nil {
		p.Post = post
		p.Name = post.Title
	}
	s.gooPllApplyShared(c, src, p, lang)
	p.PurchaseNote, p.ButtonText = src.PurchaseNote, src.ButtonText
	for k, v := range src.Meta {
		if _, ok := p.Meta[k]; !ok {
			p.Meta[k] = v
		}
	}
	p.Status = "auto-draft"
	if err := s.gooSaveProduct(gooPllSyncing(c), p, currentUser(c).ID); err != nil {
		return
	}
	s.gooPllSyncVariations(gooPllSyncing(c), src, p, lang)
}

// Shared data ---------------------------------------------------------------------------------------------

type gooPllSyncKey struct{}

// gooPllSyncing marks ctx as inside a synchronization (the saves it makes do not synchronize again).
func gooPllSyncing(ctx context.Context) context.Context {
	return context.WithValue(ctx, gooPllSyncKey{}, true)
}

func gooPllInSync(ctx context.Context) bool { v, _ := ctx.Value(gooPllSyncKey{}).(bool); return v }

func init() {
	gooOn("product_saved", 50, func(s *Server, ctx context.Context, arg any) {
		if p, ok := arg.(*gooProduct); ok {
			s.gooPllProductSaved(ctx, p)
		}
	})
	gooCheckoutHooks = append(gooCheckoutHooks, func(s *Server, ctx context.Context, cart *gooCart, o *gooOrder) error {
		if l := s.pllCurrent(ctx); l != nil && o != nil {
			if o.Meta == nil {
				o.Meta = map[string]any{}
			}
			o.Meta[gooPllOrderLang] = l.Slug // e-mails go out in this language
		}
		return nil
	})
}

const gooPllOrderLang = "_pll_lang"

// gooPllProductSaved gives a saved product (or variation) its language and copies its shared data to its
// translations.
func (s *Server) gooPllProductSaved(ctx context.Context, p *gooProduct) {
	if p == nil || !s.gooPllOn(ctx) || gooPllInSync(ctx) {
		return
	}
	if p.Type == gooTypeVariation {
		if p.ParentID > 0 {
			if own := s.pllObjLang(ctx, pllPost, p.ParentID); own != "" {
				tbl, col := pllPost.table()
				var has bool
				_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+tbl+` WHERE `+col+` = $1)`, p.ID).Scan(&has)
				if !has {
					_ = s.pllSetLang(ctx, pllPost, p.ID, own)
				}
			}
		}
		tr := s.pllTranslations(ctx, pllPost, p.ID)
		for lang, id := range tr {
			if id == p.ID {
				continue
			}
			if t, err := s.gooGetProduct(ctx, id); err == nil && t.Type == gooTypeVariation {
				s.gooPllApplyShared(ctx, p, t, lang)
				_ = s.gooSaveProduct(gooPllSyncing(ctx), t, t.Post.AuthorID)
			}
		}
		if len(tr) <= 1 && p.ParentID > 0 { // a new variation: the product's translations get it now
			if parent, err := s.gooGetProduct(gooPllUnfiltered(ctx), p.ParentID); err == nil && parent.Status != "auto-draft" {
				for lang, id := range s.pllTranslations(ctx, pllPost, parent.ID) {
					if id == parent.ID {
						continue
					}
					if t, err := s.gooGetProduct(gooPllUnfiltered(ctx), id); err == nil && t.Type != gooTypeVariation {
						s.gooPllSyncVariations(gooPllSyncing(ctx), parent, t, lang)
					}
				}
			}
		}
		return
	}
	if p.Post == nil || p.Status == "auto-draft" {
		return
	}
	s.pllEnsureLang(ctx, p.Post)
	for lang, id := range s.pllTranslations(ctx, pllPost, p.ID) {
		if id == p.ID {
			continue
		}
		t, err := s.gooGetProduct(gooPllUnfiltered(ctx), id)
		if err != nil || t.Type == gooTypeVariation {
			continue
		}
		s.gooPllApplyShared(ctx, p, t, lang)
		if err := s.gooSaveProduct(gooPllSyncing(ctx), t, t.Post.AuthorID); err != nil {
			continue
		}
		s.gooPllSyncVariations(gooPllSyncing(ctx), p, t, lang)
	}
}

// gooPllDeleteVariation deletes the translations of a variation that is being deleted (variations are
// shared data: a size removed in one language is gone in all), and syncs their products' prices.
func (s *Server) gooPllDeleteVariation(ctx context.Context, id int64) {
	if !s.gooPllOn(ctx) {
		return
	}
	for _, t := range s.pllTranslations(ctx, pllPost, id) {
		if t == id {
			continue
		}
		v, err := s.gooGetProduct(gooPllUnfiltered(ctx), t)
		if err != nil || v.Type != gooTypeVariation {
			continue
		}
		_ = s.store.DeletePost(ctx, t)
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_products WHERE post_id = $1`, t)
		if parent, err := s.gooGetProduct(ctx, v.ParentID); err == nil {
			_ = s.gooSyncVariable(ctx, parent)
			s.gooPurgeProduct(ctx, parent)
		}
	}
}

// gooPllApplyShared copies what translations share from src to dst (lang is dst's language).
func (s *Server) gooPllApplyShared(ctx context.Context, src, dst *gooProduct, lang string) {
	dst.Type, dst.SKU = src.Type, src.SKU
	dst.RegularPrice, dst.SalePrice, dst.SaleFrom, dst.SaleTo = src.RegularPrice, src.SalePrice, src.SaleFrom, src.SaleTo
	dst.TaxStatus, dst.TaxClass = src.TaxStatus, src.TaxClass
	dst.ManageStock, dst.StockQuantity, dst.StockStatus, dst.Backorders, dst.LowStockAmount = src.ManageStock, src.StockQuantity, src.StockStatus, src.Backorders, src.LowStockAmount
	dst.SoldIndividually = src.SoldIndividually
	dst.Weight, dst.Length, dst.Width, dst.Height, dst.ShippingClassID = src.Weight, src.Length, src.Width, src.Height, src.ShippingClassID
	dst.Virtual, dst.Downloadable, dst.Downloads, dst.DownloadLimit, dst.DownloadExpiry = src.Virtual, src.Downloadable, src.Downloads, src.DownloadLimit, src.DownloadExpiry
	dst.ProductURL = src.ProductURL
	dst.Featured, dst.CatalogVisibility = src.Featured, src.CatalogVisibility
	dst.ImageID = s.pllTranslatedMedia(ctx, src.ImageID, lang)
	dst.GalleryIDs = nil
	for _, id := range src.GalleryIDs {
		dst.GalleryIDs = append(dst.GalleryIDs, s.pllTranslatedMedia(ctx, id, lang))
	}
	tr := func(ids []int64) []int64 {
		var out []int64
		for _, id := range ids {
			out = append(out, s.pllTranslatedID(ctx, pllPost, id, lang))
		}
		return out
	}
	dst.UpsellIDs, dst.CrossSellIDs, dst.Children = tr(src.UpsellIDs), tr(src.CrossSellIDs), tr(src.Children)
	// Attributes: global ones take the translated terms; custom (text) ones keep the translation's values.
	own := map[string]gooProductAttr{}
	for _, a := range dst.Attributes {
		if a.Taxonomy == "" {
			own[strings.ToLower(a.Name)] = a
		}
	}
	var attrs []gooProductAttr
	for _, a := range src.Attributes {
		na := a
		if a.Taxonomy != "" {
			na.Options = nil
			for _, o := range a.Options {
				na.Options = append(na.Options, s.gooPllTermSlug(ctx, a.Taxonomy, o, lang))
			}
		} else if mine, ok := own[strings.ToLower(a.Name)]; ok && len(mine.Options) == len(a.Options) {
			na.Options = mine.Options
		}
		attrs = append(attrs, na)
	}
	dst.Attributes = attrs
	dst.DefaultAttributes = s.gooPllAttrMap(ctx, src.DefaultAttributes, lang, "")
	if src.Type == gooTypeVariation {
		dst.VariationAttrs = s.gooPllAttrMap(ctx, src.VariationAttrs, lang, "")
	}
}

// gooPllAttrMap translates the term slugs of an attribute map (pa_color → red); prefix is stripped from
// the keys before the taxonomy is looked at ("attribute_" for cart items).
func (s *Server) gooPllAttrMap(ctx context.Context, m map[string]string, lang, prefix string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		tax := strings.TrimPrefix(k, prefix)
		if strings.HasPrefix(tax, "pa_") && v != "" {
			v = s.gooPllTermSlug(ctx, tax, v, lang)
		}
		out[k] = v
	}
	return out
}

// gooPllTermSlug is the slug of a term's translation in lang (the slug itself when there is none).
func (s *Server) gooPllTermSlug(ctx context.Context, tax, slug, lang string) string {
	if !s.pllTranslatedTaxonomy(tax) {
		return slug
	}
	t, err := s.store.TermBySlug(ctx, tax, slug)
	if err != nil {
		return slug
	}
	if id := s.pllGet(ctx, pllTerm, t.ID, lang); id > 0 && id != t.ID {
		if tt, err := s.store.GetTerm(ctx, id); err == nil {
			return tt.Slug
		}
	}
	return slug
}

// gooPllSyncVariations gives dst (a translation of src) the variations of src: each one linked as a
// translation of the source variation, created when missing, with the shared data copied.
func (s *Server) gooPllSyncVariations(ctx context.Context, src, dst *gooProduct, lang string) {
	if !gooSubsVariableLike(src.Type) {
		return
	}
	vars, _ := s.gooVariations(gooPllUnfiltered(ctx), src.ID, true)
	srcLang := s.pllObjLang(ctx, pllPost, src.ID)
	for _, v := range vars {
		if s.pllObjLang(ctx, pllPost, v.ID) != srcLang {
			_ = s.pllSetLang(ctx, pllPost, v.ID, srcLang)
		}
		var t *gooProduct
		if id := s.pllGet(ctx, pllPost, v.ID, lang); id > 0 {
			if tv, err := s.gooGetProduct(ctx, id); err == nil && tv.ParentID == dst.ID {
				t = tv
			}
		}
		if t == nil {
			t = gooNewProduct(gooTypeVariation)
			t.ParentID = dst.ID
			t.Description = v.Description
			t.Post = &store.Post{Type: "product_variation", Status: v.Status, CommentStatus: "closed", PingStatus: "closed", MenuOrder: v.Post.MenuOrder,
				Meta: map[string]any{}}
		}
		s.gooPllApplyShared(ctx, v, t, lang)
		t.Name = dst.Name
		if err := s.gooSaveProduct(ctx, t, dst.Post.AuthorID); err != nil {
			continue
		}
		_ = s.pllSetLang(ctx, pllPost, t.ID, lang)
		tr := s.pllTranslations(ctx, pllPost, v.ID)
		tr[srcLang], tr[lang] = v.ID, t.ID
		_ = s.pllSaveTranslations(ctx, pllPost, tr)
	}
	// A variation of dst with no counterpart in src's language was deleted there: it goes too.
	if own, err := s.gooVariations(gooPllUnfiltered(ctx), dst.ID, true); err == nil {
		for _, v := range own {
			var linked bool // only variations already in the translation system: they had a counterpart
			_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pll_post_langs WHERE post_id = $1)`, v.ID).Scan(&linked)
			if linked && s.pllGet(ctx, pllPost, v.ID, srcLang) == 0 {
				_ = s.store.DeletePost(ctx, v.ID)
				_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_products WHERE post_id = $1`, v.ID)
			}
		}
	}
	if latest, err := s.gooGetProduct(ctx, dst.ID); err == nil {
		_ = s.gooSyncVariable(ctx, latest)
	}
}

// gooPllStockCanonical is the row every stock write of a translated product goes to: the lowest
// post id among its translations. Each translation keeps a synced copy of the stock (gooPllStockSync),
// but the atomic delta and the strict last-unit check (gooStockDelta) must lock one shared row, or two
// orders in different languages could both take the last unit.
func (s *Server) gooPllStockCanonical(ctx context.Context, p *gooProduct) *gooProduct {
	if p == nil || !s.gooPllOn(ctx) {
		return p
	}
	low := p.ID
	for _, id := range s.pllTranslations(ctx, pllPost, p.ID) {
		if id > 0 && id < low {
			low = id
		}
	}
	if low == p.ID {
		return p
	}
	if c, err := s.gooGetProduct(ctx, low); err == nil && c.ManageStock == p.ManageStock {
		return c
	}
	return p
}

// gooPllStockSync gives the translations of a product (or variation) its stock after a change.
func (s *Server) gooPllStockSync(ctx context.Context, owner *gooProduct) {
	if owner == nil || !s.gooPllOn(ctx) {
		return
	}
	var ids []int64
	for _, id := range s.pllTranslations(ctx, pllPost, owner.ID) {
		if id != owner.ID {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_products t SET stock_quantity = o.stock_quantity, stock_status = o.stock_status, updated_at = now()
		FROM goo_products o WHERE o.post_id = $1 AND t.post_id = ANY($2)`, owner.ID, ids)
	for _, id := range ids {
		if t, err := s.gooGetProduct(ctx, id); err == nil {
			if t.Type == gooTypeVariation {
				if parent := s.gooParent(ctx, t); parent != nil {
					_ = s.gooSyncVariable(ctx, parent)
				}
			}
			s.gooPurgeProduct(ctx, t)
		}
	}
}

// gooPllSiblings are the ids of a product's translations, itself included (a SKU may repeat among them).
func (s *Server) gooPllSiblings(ctx context.Context, id int64) []int64 {
	out := []int64{id}
	if id <= 0 || !s.golylangOn() {
		return out
	}
	for _, t := range s.pllTranslations(ctx, pllPost, id) {
		if t != id {
			out = append(out, t)
		}
	}
	return out
}

// gooPllTermSiblings are the ids of a term's translations, itself included.
func (s *Server) gooPllTermSiblings(ctx context.Context, id int64) []int64 {
	out := []int64{id}
	if id <= 0 || !s.golylangOn() {
		return out
	}
	for _, t := range s.pllTranslations(ctx, pllTerm, id) {
		if t != id {
			out = append(out, t)
		}
	}
	return out
}

// gooCouponItemIDs are the product ids a coupon's product lists are matched against for an item: the
// product and the variation with, under Multi Language, their translations (coupons are set up with
// one language's ids, like Polylang for WooCommerce maps them).
func (s *Server) gooCouponItemIDs(ctx context.Context, productID, variationID int64) []int64 {
	ids := s.gooPllSiblings(ctx, productID)
	if variationID != 0 {
		ids = append(ids, s.gooPllSiblings(ctx, variationID)...)
	}
	return ids
}

// gooCouponItemCats are an item's product_cat ids with, under Multi Language, their translations.
func (s *Server) gooCouponItemCats(ctx context.Context, cats []int64) []int64 {
	if !s.golylangOn() {
		return cats
	}
	var out []int64
	for _, c := range cats {
		for _, t := range s.gooPllTermSiblings(ctx, c) {
			if !gooInt64In(t, out) {
				out = append(out, t)
			}
		}
	}
	return out
}

// Cart --------------------------------------------------------------------------------------------------

// gooPllCartItems shows the cart in the visitor's language: each item takes the translation of its
// product (and variation) in the current language.
func (s *Server) gooPllCartItems(ctx context.Context, items []gooStoredItem) []gooStoredItem {
	r := pllFrom(ctx)
	if r == nil || !r.Front || r.Lang == nil || !s.gooPllOn(ctx) {
		return items
	}
	lang := r.Lang.Slug
	out := make([]gooStoredItem, len(items))
	for i, it := range items {
		if t := s.pllGet(ctx, pllPost, it.ProductID, lang); t > 0 && t != it.ProductID {
			it.ProductID = t
			if it.VariationID != 0 {
				if tv := s.pllGet(ctx, pllPost, it.VariationID, lang); tv > 0 {
					it.VariationID = tv
				}
			}
			it.Variation = s.gooPllAttrMap(ctx, it.Variation, lang, "attribute_")
		}
		out[i] = it
	}
	return out
}

// Strings -------------------------------------------------------------------------------------------

// gooPllStrings are the store strings listed in Languages › Translations (group Goocommerce).
func (s *Server) gooPllStrings() []pllRegistered {
	if !s.gooOn() {
		return nil
	}
	var out []pllRegistered
	add := func(name, str string, multi bool) {
		if strings.TrimSpace(str) != "" {
			out = append(out, pllRegistered{Name: name, String: str, Group: "Pursae Commerce", Multiline: multi})
		}
	}
	add("Store notice", s.gooOptString("demo_store_notice"), true)
	add("E-mail sender name", s.gooOptString("email_from_name"), false)
	add("E-mail footer text", s.gooOptString("email_footer_text"), true)
	for _, d := range gooEmailDefs {
		st := s.gooEmailSettings(d.ID)
		add(d.Title+": subject", defaultStr(st["subject"], d.Subject), false)
		add(d.Title+": heading", defaultStr(st["heading"], d.Heading), false)
		add(d.Title+": additional content", st["additional_content"], true)
	}
	for _, g := range s.gooGatewaysOrdered() {
		if !g.Enabled() {
			continue
		}
		add(g.MethodTitle()+": title", g.Title(), false)
		add(g.MethodTitle()+": description", g.Description(), true)
		add(g.MethodTitle()+": instructions", s.gooGatewaySetting(g.ID(), "instructions"), true)
	}
	seen := map[string]bool{}
	if zones, err := s.gooZones(context.Background(), true); err == nil {
		for _, z := range zones {
			ms, _ := s.gooMethodInstances(context.Background(), z.ID)
			for _, m := range ms {
				if t := m.Title(); !seen[t] {
					seen[t] = true
					add("Shipping method", t, false)
				}
			}
		}
	}
	return out
}

// gooPllLang is the language store strings are shown in: the order's (e-mails) or the front end's.
func (s *Server) gooPllLang(ctx context.Context) string {
	if !s.golylangOn() {
		return ""
	}
	if l := s.pllCurrent(ctx); l != nil {
		return l.Slug
	}
	return ""
}

// gooT translates a store string into the current language (Languages › Translations).
func (s *Server) gooT(ctx context.Context, str string) string {
	if lang := s.gooPllLang(ctx); lang != "" {
		return s.pllTranslate(ctx, str, lang)
	}
	return str
}

// gooPllOrderCtx is ctx in the language an order was placed in (its e-mails, links and strings).
func (s *Server) gooPllOrderCtx(ctx context.Context, o *gooOrder) context.Context {
	if o == nil || !s.golylangOn() {
		return ctx
	}
	lang, _ := o.Meta[gooPllOrderLang].(string)
	l := s.pllLang(ctx, lang)
	if l == nil {
		return ctx
	}
	if r := pllFrom(ctx); r != nil && r.Lang != nil && r.Lang.Slug == l.Slug {
		return ctx
	}
	return context.WithValue(ctx, pllCtxKey{}, &pllReq{srv: s, Lang: l, Front: true})
}

// Demo content --------------------------------------------------------------------------------------

// gooPllDemoImported gives the items of a demo import the default language (products, store pages,
// categories, attribute terms, posts and pages alike), so they are the source of their translations.
func (s *Server) gooPllDemoImported(ctx context.Context) {
	if !s.golylangOn() || len(s.pllLanguages(ctx)) == 0 {
		return
	}
	_ = s.pllAssignDefault(ctx)
	// Variations take their product's language.
	_, _ = s.store.Pool.Exec(ctx, `INSERT INTO pll_post_langs (post_id, lang, grp)
		SELECT v.id, pl.lang, nextval('pll_translation_groups') FROM posts v JOIN pll_post_langs pl ON pl.post_id = v.parent_id
		WHERE v.type = 'product_variation' ON CONFLICT (post_id) DO NOTHING`)
	s.pllFlush()
}
