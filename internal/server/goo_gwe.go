package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Goocommerce with Golementor: the store widgets of the editor (internal/builder/widgets_store.go)
// get their markup here, the Theme Builder gets Single Product and Products Archive parts plus store
// display conditions (all products, product archives, a category, the cart, checkout and my account
// pages), and a theme package can carry a Golementor design (theme.json "golementor"): a store theme
// built from Theme Builder parts and the kit's global styles, rendered through Hello Golementor.
//
// A design package is a theme of Appearance › Themes like any other. Activating it loads its design
// into the working copy (the site's theme parts and kit, exactly like activating a saved Golementor
// theme, and the working copy is kept as a backup first when it holds unsaved work), so the Theme
// Builder edits what the store shows. Its demo pages may be Golementor documents ("golementor" in
// demo.json pages).

// Store views -----------------------------------------------------------------------------------------

// gooStoreKind names a store view for the Theme Builder's conditions: product, shop, product_cat,
// product_tag, product_attr, cart, checkout or myaccount ("" for other pages).
func (s *Server) gooStoreKind(c *gin.Context, q *siteQuery) string {
	if c == nil || !s.gooOn() {
		return ""
	}
	if fv := gooViewFrom(c); fv != nil {
		return fv.Kind
	}
	if q != nil && q.Post != nil && q.Post.Type == "page" {
		for _, key := range []string{"cart", "checkout", "myaccount"} {
			if s.gooPageIs(c, q.Post.ID, key) {
				return key
			}
		}
	}
	return ""
}

// gooArchiveKind reports whether a store view is a product archive.
func gooArchiveKind(kind string) bool {
	return kind == "shop" || kind == "product_cat" || kind == "product_tag" || kind == "product_attr"
}

// gweStoreCondScore scores the store conditions: product[/<id>], product_archive[/shop|/product_cat|
// /product_tag[/<term id>]] and goocommerce/<cart|checkout|myaccount>. ok is false for other rules.
func gweStoreCondScore(parts []string, q *siteQuery) (int, bool) {
	kind := q.Store
	switch parts[0] {
	case "product":
		if kind != "product" {
			return 0, true
		}
		if len(parts) > 1 && parts[1] != "" {
			if q.Post != nil && parts[1] == itoa64(q.Post.ID) {
				return 55, true
			}
			return 0, true
		}
		return 40, true
	case "product_archive":
		if !gooArchiveKind(kind) {
			return 0, true
		}
		if len(parts) == 1 {
			return 40, true
		}
		if parts[1] != kind {
			return 0, true
		}
		if len(parts) == 2 {
			return 45, true
		}
		if q.StoreTerm > 0 && parts[2] == itoa64(q.StoreTerm) {
			return 55, true
		}
		return 0, true
	case "goocommerce":
		if len(parts) > 1 && parts[1] == kind && kind != "" {
			return 55, true
		}
		return 0, true
	}
	return 0, false
}

// Store widgets -----------------------------------------------------------------------------------------

const gooGweSampleKey = "goo.gwe.sample" // *gooProduct the editor previews a Single Product part with

// gooGweSampleProduct is the product the editor and the preview of a theme part show: the latest
// published one.
func (s *Server) gooGweSampleProduct(c *gin.Context) *gooProduct {
	if v, ok := c.Get(gooGweSampleKey); ok {
		p, _ := v.(*gooProduct)
		return p
	}
	var p *gooProduct
	list, _, err := s.gooQueryProducts(c, gooProductQuery{Visibility: "catalog", PerPage: 1, Page: 1, OrderBy: "date", Order: "DESC"})
	if err == nil && len(list) > 0 {
		p = list[0]
	}
	c.Set(gooGweSampleKey, p)
	return p
}

// gooGweStore is ThemeData.Store: the markup of a store widget for the request.
func (s *Server) gooGweStore(ctx context.Context) func(name string, st map[string]any, editing bool) string {
	return func(name string, st map[string]any, editing bool) string {
		c := gooCtxGin(ctx)
		if c == nil || !s.gooOn() {
			return ""
		}
		fv := gooViewFrom(c)
		preview := editing || gwePreviewing(c)
		str := func(k, def string) string {
			if v, ok := st[k]; ok && v != nil {
				if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
					return s
				}
			}
			return def
		}
		// sw reads a switcher: an empty saved value is off; a missing one takes the control's default.
		sw := func(k, on, def string) bool {
			v, ok := st[k]
			if !ok || v == nil {
				return def == on
			}
			return fmt.Sprint(v) == on
		}
		product := func() *gooProduct {
			if fv != nil && fv.Product != nil {
				return fv.Product
			}
			if preview {
				return s.gooGweSampleProduct(c)
			}
			return nil
		}
		switch name {
		case "woocommerce-products":
			cols := gooAtoi(str("columns", "4"))
			a := map[string]string{"columns": strconv.Itoa(cols), "limit": strconv.Itoa(cols * max(gooAtoi(str("rows", "2")), 1)),
				"orderby": str("orderby", "date"), "order": strings.ToUpper(str("order", "desc"))}
			if sw("paginate", "yes", "") {
				a["paginate"] = "true"
			}
			switch str("query_post_type", "recent") {
			case "featured":
				a["visibility"] = "featured"
			case "sale":
				a["on_sale"] = "true"
			case "best_selling":
				a["best_selling"] = "true"
			case "top_rated":
				a["top_rated"] = "true"
			case "category":
				a["category"] = str("query_category", "")
			}
			return s.gooProductsShortcode(c, "products", a)
		case "wc-archive-products":
			cls := "goo-archive-products"
			if !sw("show_result_count", "yes", "yes") {
				cls += " goo-hide-result-count"
			}
			if !sw("allow_order", "yes", "yes") {
				cls += " goo-hide-ordering"
			}
			if fv != nil && gooArchiveKind(fv.Kind) {
				return `<div class="` + cls + `">` + s.gooArchiveHTML(c, fv) + `</div>`
			}
			if preview {
				return `<div class="` + cls + `">` + s.gooProductsShortcode(c, "products", map[string]string{"limit": "8", "columns": "4", "orderby": "date", "order": "DESC"}) + `</div>`
			}
			return ""
		case "wc-categories":
			hide := "0"
			if sw("hide_empty", "yes", "yes") {
				hide = "1"
			}
			return s.gooCategoriesShortcode(c, map[string]string{"number": str("number", "4"), "columns": str("columns", "4"), "hide_empty": hide, "parent": "0"})
		case "woocommerce-breadcrumb":
			if fv == nil && preview {
				if p := product(); p != nil {
					return s.gooBreadcrumb(c, &gooFrontView{Kind: "product", Product: p})
				}
			}
			if fv == nil {
				return ""
			}
			return s.gooBreadcrumb(c, fv)
		case "woocommerce-menu-cart":
			return s.gooGweMenuCart(c, str("icon", "bag"), sw("show_subtotal", "yes", "yes"), sw("hide_empty_indicator", "hide", ""))
		case "woocommerce-cart":
			return s.gooCartPageHTML(c)
		case "woocommerce-checkout-page":
			if preview && fv == nil && s.gooCartFor(c).IsEmpty() {
				return `<div class="woocommerce"><div class="woocommerce-info">The checkout form shows here once the cart has products.</div></div>`
			}
			return s.gooCheckoutPageHTML(c)
		case "woocommerce-my-account":
			return s.gooAccountPageHTML(c)
		case "woocommerce-notices":
			return `<div class="woocommerce">` + s.gooNoticesHTML(c) + `</div>`
		case "golylang-language-switcher":
			return s.gooGweLanguageSwitcher(c, str("layout", "horizontal"), sw("show_flags", "yes", "yes"), str("display", "code"), sw("hide_current", "yes", ""))
		}
		p := product()
		if p == nil {
			return ""
		}
		switch name {
		case "woocommerce-product-title":
			return gooEsc(p.Name)
		case "woocommerce-product-images":
			sale := ""
			if s.gooOnSale(c, p) {
				sale = `<span class="onsale">Sale!</span>`
			}
			return sale + s.gooGalleryHTML(c, p)
		case "woocommerce-product-price":
			return `<p class="price">` + string(s.gooPriceHTML(c, p)) + `</p>`
		case "woocommerce-product-add-to-cart":
			return s.gooAddToCartForm(c, p)
		case "woocommerce-product-rating":
			if !s.gooOptBool("enable_review_rating") || p.RatingCount == 0 {
				return ""
			}
			n := strconv.Itoa(p.RatingCount)
			word := map[bool]string{true: "customer review", false: "customer reviews"}[p.RatingCount == 1]
			return `<div class="woocommerce-product-rating">` + gooRatingHTML(p.AverageRating, p.RatingCount) +
				`<a href="#reviews" class="woocommerce-review-link" rel="nofollow">(<span class="count">` + n + `</span> ` + word + `)</a></div>`
		case "woocommerce-product-stock":
			return s.gooFrontStockHTML(c, p)
		case "woocommerce-product-meta":
			return s.gooProductMeta(c, p)
		case "woocommerce-product-short-description":
			if p.Post == nil || strings.TrimSpace(p.Post.Excerpt) == "" {
				return ""
			}
			return `<div class="woocommerce-product-details__short-description">` + s.gooRenderContent(c, c, p.Post.Excerpt) + `</div>`
		case "woocommerce-product-data-tabs":
			return s.gooTabsHTML(c, p)
		case "woocommerce-product-related":
			return s.gooRelatedHTML(c, p)
		case "woocommerce-product-upsell":
			return s.gooUpsellsHTML(c, p)
		}
		return ""
	}
}

// gooGweMenuCart is the Menu Cart widget: the cart icon with the item count and subtotal, and the
// mini-cart that opens on hover or focus (the count and the mini-cart follow AJAX cart changes).
func (s *Server) gooGweMenuCart(c *gin.Context, icon string, subtotal, hideEmpty bool) string {
	cart := s.gooCartFor(c)
	n := cart.Count()
	icons := map[string]string{
		"cart":   `<path d="M3 4h2.2l2.1 10.2a1.6 1.6 0 0 0 1.6 1.3h7.7a1.6 1.6 0 0 0 1.5-1.2L20 8H6.2"/><circle cx="9.5" cy="19.5" r="1.3"/><circle cx="16.5" cy="19.5" r="1.3"/>`,
		"basket": `<path d="M4 10h16l-1.6 8.4a2 2 0 0 1-2 1.6H7.6a2 2 0 0 1-2-1.6z"/><path d="M8.5 10 11 4.5M15.5 10 13 4.5M9 14v2.5M12 14v2.5M15 14v2.5"/>`,
		"bag":    `<path d="M5.5 8h13l-1 11.2a1.5 1.5 0 0 1-1.5 1.3H8a1.5 1.5 0 0 1-1.5-1.3z"/><path d="M9 10V7a3 3 0 0 1 6 0v3"/>`,
	}
	svg, ok := icons[icon]
	if !ok {
		svg = icons["bag"]
	}
	cls := "goo-menu-cart"
	if n == 0 {
		cls += " goo-menu-cart--empty"
		if hideEmpty {
			cls += " goo-menu-cart--hide-empty"
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="` + cls + `">`)
	b.WriteString(`<a class="goo-menu-cart__toggle" href="` + gooEsc(s.gooPageURL(c, "cart")) + `" aria-label="Cart">`)
	b.WriteString(`<svg class="goo-menu-cart__icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + svg + `</svg>`)
	b.WriteString(`<span class="goo-menu-cart__count"><span class="goo-cart-count">` + strconv.Itoa(n) + `</span></span></a>`)
	if subtotal {
		b.WriteString(`<span class="goo-menu-cart__subtotal">` + string(s.gooCartLink(c)) + `</span>`)
	}
	b.WriteString(`<div class="goo-menu-cart__panel">` + string(s.gooMiniCart(c)) + `</div></div>`)
	return b.String()
}

// gooGweLanguageSwitcher is the Language Switcher widget (Golylang): links or a dropdown.
func (s *Server) gooGweLanguageSwitcher(c *gin.Context, layout string, flags bool, display string, hideCurrent bool) string {
	if !s.golylangOn() || len(s.pllActiveLanguages(c)) == 0 {
		return ""
	}
	a := pllDefaultArgs()
	a.HideIfEmpty, a.ShowFlags, a.HideCurrent = false, flags, hideCurrent
	a.ShowNames = display != "none"
	if display == "code" {
		a.DisplayNamesAs = "slug"
	}
	q := siteQueryFrom(c)
	if layout == "dropdown" {
		return `<div class="goo-lang-switcher goo-lang-switcher--dropdown">` + s.pllSwitcherHTML(c, q, a, "goo-lang-"+strconv.Itoa(len(a.DisplayNamesAs))) + `</div>`
	}
	var b strings.Builder
	b.WriteString(`<nav class="goo-lang-switcher" aria-label="Language"><ul>`)
	for _, it := range s.pllSwitcherItems(c, q, a) {
		fmt.Fprintf(&b, `<li class="%s"><a lang="%s" hreflang="%s" href="%s"%s title="%s">`, html.EscapeString(it.Classes), html.EscapeString(it.Lang.W3C()),
			html.EscapeString(it.Lang.W3C()), html.EscapeString(it.URL), map[bool]string{true: ` aria-current="true"`}[it.Current], html.EscapeString(it.Lang.Name))
		if flags && it.Lang.FlagEmoji() != "" {
			b.WriteString(`<span class="goo-lang-flag" aria-hidden="true">` + it.Lang.FlagEmoji() + `</span>`)
		}
		if a.ShowNames {
			label := it.Label
			if display == "code" {
				label = strings.ToUpper(label)
			}
			b.WriteString(`<span class="goo-lang-code">` + html.EscapeString(label) + `</span>`)
		}
		b.WriteString(`</a></li>`)
	}
	b.WriteString(`</ul></nav>`)
	return b.String()
}

// gooGweWrapPart puts a Single Product part inside the product's div (the storefront's styles and the
// variation script expect the gallery and the form inside div.product).
func (s *Server) gooGweWrapPart(c *gin.Context, location string, out template.HTML) template.HTML {
	if location != "single-product" || !s.gooOn() {
		return out
	}
	fv := gooViewFrom(c)
	if fv == nil || fv.Product == nil {
		return out
	}
	p := fv.Product
	return template.HTML(`<div class="woocommerce">` + s.gooNoticesHTML(c) + `<div id="product-` + itoa64(p.ID) + `" class="` + s.gooProductClasses(c, p, "") + `">` +
		string(out) + `</div></div>`)
}

// gooUsesStoreWidgets reports whether a page is built with Golementor and holds store widgets (the
// storefront's stylesheets are needed).
func gooUsesStoreWidgets(p *store.Post) bool {
	if p == nil || !gweBuilt(p) {
		return false
	}
	raw := string(gweElementsRaw(p))
	return strings.Contains(raw, `"widgetType":"woocommerce-`) || strings.Contains(raw, `"widgetType":"wc-`)
}

// Design packages ------------------------------------------------------------------------------------------

// gweDesignOf is the Golementor design of a theme package (nil for other themes).
func gweDesignOf(t *Theme) string {
	if t == nil || t.Package == nil || t.Package.Manifest == nil {
		return ""
	}
	return t.Package.Manifest.Golementor
}

// gweDesignTheme reports whether a theme is a Golementor design package.
func gweDesignTheme(t *Theme) bool { return gweDesignOf(t) != "" }

// gweDesignFile is the design file of a package: a saved theme's manifest.json shape (format
// gowebeditor-theme) whose parts may give "elements" as a JSON array instead of a string.
type gweDesignFile struct {
	Format    string         `json:"format"`
	Version   int            `json:"version"`
	Name      string         `json:"name"`
	Kit       map[string]any `json:"kit"`
	CustomCSS string         `json:"custom_css"`
	Parts     []struct {
		Type       string          `json:"type"`
		Title      string          `json:"title"`
		Status     string          `json:"status"`
		Elements   json.RawMessage `json:"elements"`
		Settings   map[string]any  `json:"settings"`
		Conditions []string        `json:"conditions"`
	} `json:"parts"`
}

// parseGweDesign reads and checks a design file; the snapshot it returns loads like a saved theme's.
func parseGweDesign(fsys fs.FS, name string) (*gweSnapshot, error) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	var d gweDesignFile
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	if d.Format != gweThemeFormat {
		return nil, fmt.Errorf("%s: format must be %q", name, gweThemeFormat)
	}
	snap := &gweSnapshot{Format: d.Format, Version: d.Version, Name: d.Name, Kit: d.Kit, CustomCSS: d.CustomCSS}
	if snap.Kit == nil {
		snap.Kit = map[string]any{}
	}
	known := map[string]bool{}
	for _, t := range gwePartTypes {
		known[t.Type] = true
	}
	for i, p := range d.Parts {
		if !known[p.Type] {
			return nil, fmt.Errorf("%s: parts[%d].type %q is not a theme part type", name, i, p.Type)
		}
		els := strings.TrimSpace(string(p.Elements))
		if strings.HasPrefix(els, `"`) { // a string holding the tree
			if err := json.Unmarshal(p.Elements, &els); err != nil {
				return nil, fmt.Errorf("%s: parts[%d].elements: %v", name, i, err)
			}
		}
		var tree []any
		if err := json.Unmarshal([]byte(els), &tree); err != nil {
			return nil, fmt.Errorf("%s: parts[%d].elements: %v", name, i, err)
		}
		if len(p.Conditions) == 0 {
			p.Conditions = gweDefaultConditions[p.Type]
		}
		snap.Parts = append(snap.Parts, &gwePart{Type: p.Type, Title: p.Title, Status: defaultStr(p.Status, "publish"), Elements: els,
			Settings: p.Settings, Conditions: p.Conditions})
	}
	if len(snap.Parts) == 0 {
		return nil, fmt.Errorf("%s: the design has no parts", name)
	}
	return snap, nil
}

// gweDesignSnapshot is the design of an installed design package.
func gweDesignSnapshot(t *Theme) (*gweSnapshot, error) {
	if !gweDesignTheme(t) {
		return nil, errors.New("not a Golementor design theme")
	}
	return parseGweDesign(os.DirFS(t.Package.Dir), gweDesignOf(t))
}

// gweLoadDesignTheme loads a design package into the working copy (switchTheme): the site's parts go
// to the trash (kept as a saved theme first when they hold unsaved work), the design's parts and kit
// take their place. Golementor is switched on when it is off.
func (s *Server) gweLoadDesignTheme(ctx context.Context, t *Theme) error {
	snap, err := gweDesignSnapshot(t)
	if err != nil {
		return err
	}
	if !s.golementorOn() {
		if err := s.settings.Set(ctx, map[string]any{golementorSetting: true}); err != nil {
			return err
		}
		s.golementorStart(ctx)
	}
	return s.gweLoadSnapshot(ctx, snap, t.Slug)
}

// Theme Builder -------------------------------------------------------------------------------------------

// gwePartTypeOn reports whether the Theme Builder offers a part type (the store's need Goocommerce).
func (s *Server) gwePartTypeOn(typ string) bool {
	switch typ {
	case "single-product", "product-archive":
		return s.gooOn()
	}
	return true
}

// gooGweRuleOpts adds the store's display conditions to the conditions screen.
func (s *Server) gooGweRuleOpts(c *gin.Context, opts []gweRuleGroup) []gweRuleGroup {
	if !s.gooOn() {
		return opts
	}
	g := gweRuleGroup{Label: "Store"}
	for _, r := range []string{"product", "product_archive", "product_archive/shop", "product_archive/product_cat", "product_archive/product_tag",
		"goocommerce/cart", "goocommerce/checkout", "goocommerce/myaccount"} {
		g.Opts = append(g.Opts, gweRuleOpt{r, gweRuleLabels[r]})
	}
	// "Other" holds saved rules the lists did not know; the store rules now have a group of their own.
	known := map[string]bool{}
	for _, o := range g.Opts {
		known[o.Value] = true
	}
	cats := gweRuleGroup{Label: "Specific Product Category"}
	if terms, _, err := s.store.QueryTerms(gooPllUnfiltered(c), store.TermQuery{Taxonomy: "product_cat", OrderBy: "name", Order: "ASC", PerPage: 200, Page: 1}); err == nil {
		for _, t := range terms {
			v := "product_archive/product_cat/" + itoa64(t.ID)
			cats.Opts = append(cats.Opts, gweRuleOpt{v, t.Name})
			known[v] = true
		}
	}
	var out []gweRuleGroup
	for _, grp := range opts {
		if grp.Label == "Other" {
			var keep []gweRuleOpt
			for _, o := range grp.Opts {
				if !known[o.Value] {
					keep = append(keep, o)
				}
			}
			if len(keep) == 0 {
				continue
			}
			grp.Opts = keep
		}
		out = append(out, grp)
	}
	out = append(out, g)
	if len(cats.Opts) > 0 {
		out = append(out, cats)
	}
	return out
}

// gooGwePartLangs is a part's row of language links in the Theme Builder (Golylang): its own language,
// the edit link of each translation and an add link for the missing ones.
func (s *Server) gooGwePartLangs(c *gin.Context, p *store.Post) template.HTML {
	if !s.golylangOn() || len(s.pllLanguages(c)) < 2 {
		return ""
	}
	own := s.pllObjLang(c, pllPost, p.ID)
	tr := s.pllTranslations(c, pllPost, p.ID)
	nonce := s.nonce(c, "gwe-theme-builder")
	var b strings.Builder
	b.WriteString(`<span class="gwe-tb-langs__label">Languages:</span> `)
	for _, l := range s.pllLanguages(c) {
		label := html.EscapeString(strings.ToUpper(l.Slug))
		switch {
		case l.Slug == own:
			fmt.Fprintf(&b, `<span class="gwe-tb-lang is-own" title="%s">%s</span> `, html.EscapeString(l.Name), label)
		case tr[l.Slug] > 0:
			fmt.Fprintf(&b, `<a class="gwe-tb-lang is-translated" href="%s" title="Edit the translation in %s">%s</a> `, gweEditURL(tr[l.Slug]), html.EscapeString(l.Name), label)
		default:
			fmt.Fprintf(&b, `<a class="gwe-tb-lang is-missing" href="%s?action=translate&amp;post=%d&amp;lang=%s&amp;_wpnonce=%s" title="Add a translation in %s">+ %s</a> `,
				gweThemeBuilderPath, p.ID, html.EscapeString(l.Slug), nonce, html.EscapeString(l.Name), label)
		}
	}
	return template.HTML(b.String())
}

// gooGweTranslatePart creates a part's translation (a draft copy in the language, linked to it) and
// opens it in Golementor; an existing translation just opens.
func (s *Server) gooGweTranslatePart(c *gin.Context) {
	if !s.checkNonce(c, "gwe-theme-builder") {
		s.denyNonce(c)
		return
	}
	u := currentUser(c)
	p, err := s.store.GetPost(c, gweInt(c.Query("post")))
	l := s.pllLang(c, c.Query("lang"))
	if err != nil || p.Type != gwePartType || l == nil || !s.golylangOn() || !u.Can("edit_post", p) {
		s.gweTBRedirect(c, "?error="+urlQueryEscape("That theme part cannot be translated."))
		return
	}
	tr := s.pllTranslations(c, pllPost, p.ID)
	if id := tr[l.Slug]; id > 0 && id != p.ID {
		c.Redirect(302, gweEditURL(id))
		return
	}
	own := s.pllObjLang(c, pllPost, p.ID)
	src := gwePartFromPost(p)
	src.Status, src.Title = "draft", p.Title+" ("+l.Name+")"
	np, err := s.gweCopyPart(c, src, "", u.ID)
	if err != nil {
		s.gweTBRedirect(c, "?error="+urlQueryEscape(err.Error()))
		return
	}
	_ = s.pllSetLang(c, pllPost, p.ID, own)
	_ = s.pllSetLang(c, pllPost, np.ID, l.Slug)
	tr[own], tr[l.Slug] = p.ID, np.ID
	_ = s.pllSaveTranslations(c, pllPost, tr)
	c.Redirect(302, gweEditURL(np.ID))
}

// gooGweLangParts keeps the parts of the current language: a part shows in its own language, and in
// a language it has no translation in when it is in the default language (Golylang).
func (s *Server) gooGweLangParts(ctx context.Context, parts []*gwePart) []*gwePart {
	cur := s.pllCurrent(ctx)
	if cur == nil || len(parts) == 0 {
		return parts
	}
	def := defaultSlug(s.pllDefault(ctx))
	out := parts[:0:0]
	for _, p := range parts {
		lang := s.pllObjLang(ctx, pllPost, p.ID)
		if lang == cur.Slug {
			out = append(out, p)
			continue
		}
		if t := s.pllGet(ctx, pllPost, p.ID, cur.Slug); t > 0 && t != p.ID {
			continue // its translation shows instead
		}
		if lang == def {
			out = append(out, p)
		}
	}
	return out
}
