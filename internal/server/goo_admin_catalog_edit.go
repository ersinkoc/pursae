package server

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// The product editor: post-new.php?post_type=product and post.php?post=<product>&action=edit, the
// classic edit screen with WooCommerce's meta boxes (Product data, Product short description,
// Product image, Product gallery, Product categories, Product tags and the Publish box with the
// catalog visibility). Unlike WooCommerce, attributes and variations are saved with the form (the
// "Save attributes", "Generate variations" and "Add manually" buttons submit it), so the screen works
// without the AJAX endpoints.

// gooMaxLinkedVariations is WC_MAX_LINKED_VARIATIONS: how many variations one "Generate" creates.
const gooMaxLinkedVariations = 50

type gooTermOpt struct {
	ID       int64
	Slug     string
	Name     string
	Selected bool
}

type gooAttrRow struct {
	Index     string
	Label     string
	Name      string // the field value of attribute_names[i]: pa_<slug> or the custom name
	Taxonomy  string
	Key       string
	Values    string // custom attributes: "a | b"
	Terms     []gooTermOpt
	Options   []gooOpt // value → label, for variation selects
	Visible   bool
	Variation bool
	Position  int
	Default   string
}

type gooVarAttr struct {
	Key, Label, Selected string
	Options              []gooOpt
}

type gooVariationRow struct {
	Index                           int
	ID                              int64
	V                               *gooProduct
	Attrs                           []gooVarAttr
	Enabled                         bool
	Regular, Sale, SaleFrom, SaleTo string
	Stock, LowStock                 string
	Thumb                           string
	Downloads                       []gooDownload
	DownloadLimit, DownloadExpiry   string
	ShipClass                       int64
	TaxClass                        string
	MenuOrder                       int
	Extra                           template.HTML // module fields (gxsVariationFieldHooks)
}

type gooPanelView struct {
	ID, Label, Class string
	HTML             template.HTML
}

type gooImage struct {
	ID  int64
	URL string
}

type gooEditData struct {
	LangBox      template.HTML // Golylang: the Languages box (goo_golylang.go)
	Post         *store.Post
	P            *gooProduct
	IsNew        bool
	Nonce        string
	StatusLabel  string
	Visibility   string
	DateLabel    string
	PublishLabel string
	ViewURL      string
	TrashURL     string
	CanPublish   bool
	LastEdit     string
	SampleLink   template.HTML
	SampleNonce  string
	TS           touchTime
	Editor       template.HTML
	Thumbnail    template.HTML
	Gallery      []gooImage
	GalleryIDs   string

	Categories  []*store.Term
	Checked     map[int64]bool
	TagNames    string
	PopularTags []*store.Term

	Types       []gooOpt
	TaxClasses  []gooOpt
	ShipClasses []*store.Term
	CatalogVis  []gooOpt
	CatalogLbl  string
	Symbol      template.HTML
	WeightUnit  string
	DimUnit     string
	ManageStock bool // the store's "Enable stock management"

	Regular, Sale, SaleFrom, SaleTo string
	Stock, LowStock                 string
	DownloadLimit, DownloadExpiry   string
	Upsells, CrossSells, Grouped    []gooOpt
	Attributes                      []gooAttrRow
	VariationAttrs                  []gooAttrRow
	GlobalAttrs                     []gooAttrRow // templates for "Add" (index __i__)
	CustomAttr                      gooAttrRow
	Variations                      []gooVariationRow
	Panels                          []gooPanelView
	SearchURL                       string
	Tab                             string
	ProductBase                     string
}

// gooProductEditor serves post-new / post.php for products: GET shows the editor (or runs the trash,
// untrash and delete row actions), POST saves.
func (s *Server) gooProductEditor(c *gin.Context, id int64) {
	ctx := c.Request.Context()
	u := currentUser(c)
	if c.Request.Method == http.MethodPost {
		s.gooProductSave(c, id)
		return
	}
	if id == 0 {
		if !u.Can("edit_products", nil) {
			c.String(http.StatusForbidden, "Sorry, you are not allowed to create posts as this user.")
			return
		}
		post, err := s.store.CreateAutoDraft(ctx, "product", u.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		post.CommentStatus = "open"
		p, err := s.gooGetProduct(ctx, post.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		if c.Query("from_post") != "" || c.Query("new_lang") != "" { // Golylang: a new translation (goo_golylang.go)
			s.gooPllNewTranslation(c, p)
			if np, err := s.gooGetProduct(ctx, post.ID); err == nil {
				np.Post.CommentStatus = "open"
				p = np
			}
		} else {
			s.golylangNewPost(c, p.Post)
		}
		if t := c.Query("post_title"); t != "" || p.Post.Title == "Auto Draft" {
			p.Post.Title = t
		}
		s.gooRenderEditor(c, p, true)
		return
	}
	p, err := s.gooGetProduct(ctx, id)
	if err != nil {
		c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
		return
	}
	switch action := c.DefaultQuery("action", "edit"); action {
	case "trash", "untrash", "delete":
		if !s.checkNonce(c, action+"-post_"+itoa64(p.ID)) {
			s.denyNonce(c)
			return
		}
		if !gooCanProduct(u, "delete", p.Post) {
			c.String(http.StatusForbidden, "Sorry, you are not allowed to move this item to the Trash.")
			return
		}
		if err := s.gooTrashAction(ctx, p, action); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		key := map[string]string{"trash": "trashed", "untrash": "untrashed", "delete": "deleted"}[action]
		c.Redirect(http.StatusFound, "/wp-admin/edit?post_type=product&"+key+"=1&ids="+itoa64(p.ID))
		return
	}
	if !gooCanProduct(u, "edit", p.Post) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	if p.Status == "trash" {
		c.String(http.StatusForbidden, "You cannot edit this item because it is in the Trash. Please restore it and try again.")
		return
	}
	s.gooRenderEditor(c, p, false)
}

// gooDateInput formats a sale date for the date fields (site time zone).
func (s *Server) gooDateInput(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.In(s.loc()).Format("2006-01-02")
}

func gooAmount(v *float64) string {
	if v == nil {
		return ""
	}
	return gooFormatDecimal(*v, -1)
}

func gooIntStr(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func gooLimitStr(v int) string {
	if v < 0 {
		return ""
	}
	return strconv.Itoa(v)
}

// gooRenderEditor renders the product edit screen.
func (s *Server) gooRenderEditor(c *gin.Context, p *gooProduct, isNew bool) {
	ctx := c.Request.Context()
	u := currentUser(c)
	post := p.Post
	lb := s.links(ctx)
	loc := s.loc()
	d := gooEditData{Post: post, P: p, IsNew: isNew, Nonce: s.nonce(c, "update-post_"+itoa64(p.ID)),
		CanPublish: u.Can("publish_products", nil), Checked: map[int64]bool{},
		TrashURL: "/wp-admin/post?post=" + itoa64(p.ID) + "&action=trash&_wpnonce=" + s.nonce(c, "trash-post_"+itoa64(p.ID)),
		Types:    s.gooProductTypeOptions(), TaxClasses: s.gooTaxClasses(), Symbol: template.HTML(gooCurrencySymbol(s.gooCurrency())),
		WeightUnit: defaultStr(s.gooOptString("weight_unit"), "kg"), DimUnit: defaultStr(s.gooOptString("dimension_unit"), "cm"),
		ManageStock: s.gooManageStock(), SearchURL: "/wp-admin/edit?post_type=product", Tab: c.Query("goo_tab"),
		Regular: gooAmount(p.RegularPrice), Sale: gooAmount(p.SalePrice), SaleFrom: s.gooDateInput(p.SaleFrom), SaleTo: s.gooDateInput(p.SaleTo),
		Stock: gooIntStr(p.StockQuantity), LowStock: gooIntStr(p.LowStockAmount),
		DownloadLimit: gooLimitStr(p.DownloadLimit), DownloadExpiry: gooLimitStr(p.DownloadExpiry)}
	d.LangBox = s.golylangPostBox(c, post) // Golylang: language and translations
	d.CatalogVis = []gooOpt{{"visible", "Shop and search results"}, {"catalog", "Shop only"}, {"search", "Search results only"}, {"hidden", "Hidden"}}
	for _, o := range d.CatalogVis {
		if o.Value == defaultStr(p.CatalogVisibility, "visible") {
			d.CatalogLbl = o.Label
		}
	}
	d.ShipClasses, _, _ = s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: "product_shipping_class"})

	// Categories and tags.
	d.Categories, _ = s.store.HierarchicalTerms(ctx, "product_cat", false)
	if terms, err := s.store.TermsForPosts(ctx, []int64{p.ID}, "product_cat"); err == nil {
		for _, t := range terms[p.ID] {
			d.Checked[t.ID] = true
		}
	}
	if tags, err := s.store.TermsForPosts(ctx, []int64{p.ID}, "product_tag"); err == nil {
		var names []string
		for _, t := range tags[p.ID] {
			names = append(names, t.Name)
		}
		d.TagNames = strings.Join(names, ", ")
	}
	d.PopularTags, _, _ = s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: "product_tag", OrderBy: "count", Order: "desc", PerPage: 45})

	// Linked products.
	linked := func(ids []int64) []gooOpt {
		var out []gooOpt
		m, _ := s.gooGetProducts(ctx, ids)
		for _, id := range ids {
			if lp := m[id]; lp != nil {
				out = append(out, gooOpt{itoa64(id), s.gooFormattedName(ctx, lp)})
			}
		}
		return out
	}
	d.Upsells, d.CrossSells = linked(p.UpsellIDs), linked(p.CrossSellIDs)
	if p.Type == gooTypeGrouped {
		d.Grouped = linked(p.Children)
	}

	// Gallery.
	for _, id := range p.GalleryIDs {
		if src := s.gooAttachmentURL(ctx, id, "thumbnail"); src != "" {
			d.Gallery = append(d.Gallery, gooImage{id, src})
		}
	}
	var gids []string
	for _, g := range d.Gallery {
		gids = append(gids, itoa64(g.ID))
	}
	d.GalleryIDs = strings.Join(gids, ",")

	// Attributes.
	for i, a := range p.Attributes {
		row := s.gooAttrRowFor(ctx, a, strconv.Itoa(i))
		row.Default = p.DefaultAttributes[a.Key()]
		d.Attributes = append(d.Attributes, row)
		if a.Variation {
			d.VariationAttrs = append(d.VariationAttrs, row)
		}
	}
	if globals, err := s.gooAttributes(ctx); err == nil {
		for _, ga := range globals {
			d.GlobalAttrs = append(d.GlobalAttrs, s.gooAttrRowFor(ctx, gooProductAttr{ID: ga.ID, Name: ga.Name, Taxonomy: ga.Taxonomy(), Visible: true}, "__i__"))
		}
	}
	d.CustomAttr = gooAttrRow{Index: "__i__", Visible: true}

	// Variations.
	if gooSubsVariableLike(p.Type) {
		vars, _ := s.gooVariations(ctx, p.ID, true)
		for i, v := range vars {
			d.Variations = append(d.Variations, s.gooVariationRowFor(ctx, d.VariationAttrs, v, i))
		}
	}

	// Module panels (gooProductPanels).
	for _, pn := range gooProductPanels {
		if pn.Module != "" && !s.gooExtOn(pn.Module) {
			continue
		}
		cls := ""
		for _, t := range pn.ShowFor {
			cls += " show_if_" + t
		}
		v := gooPanelView{ID: pn.ID, Label: pn.Label, Class: cls}
		if pn.Render != nil {
			v.HTML = pn.Render(s, c, p)
		}
		d.Panels = append(d.Panels, v)
	}

	// Publish box (post_submit_meta_box()).
	switch post.Status {
	case "publish":
		d.StatusLabel = "Published"
	case "future":
		d.StatusLabel = "Scheduled"
	case "pending":
		d.StatusLabel = "Pending Review"
	case "private":
		d.StatusLabel = "Privately Published"
	default:
		d.StatusLabel = "Draft"
	}
	switch {
	case post.Status == "private":
		d.Visibility = "private"
	case post.Password != "":
		d.Visibility = "password"
	default:
		d.Visibility = "public"
	}
	if post.Status != "auto-draft" {
		permalink := s.gooPermalink(ctx, p)
		if post.Status == "publish" || post.Status == "private" {
			d.ViewURL = permalink
		} else {
			d.ViewURL = lb.HomeURL("/?post_type=product&p=" + itoa64(p.ID) + "&preview=true")
		}
		d.SampleLink = s.gooSamplePermalinkHTML(c, p)
	}
	d.SampleNonce = s.nonce(c, "samplepermalink")
	d.ProductBase = s.gooProductBaseURL(ctx)
	if post.PublishedAt != nil {
		t := post.PublishedAt.In(loc)
		stamp := wp.FormatDate(t, "M j, Y") + " at " + wp.FormatDate(t, "H:i")
		switch post.Status {
		case "future":
			d.DateLabel = "Scheduled for: <b>" + stamp + "</b>"
		case "publish", "private":
			d.DateLabel = "Published on: <b>" + stamp + "</b>"
		default:
			d.DateLabel = "Schedule for: <b>" + stamp + "</b>"
		}
	} else {
		d.DateLabel = "Publish <b>immediately</b>"
	}
	switch {
	case post.Status == "publish" || post.Status == "private" || post.Status == "future":
		d.PublishLabel = "Update"
	case !d.CanPublish:
		d.PublishLabel = "Submit for Review"
	case post.PublishedAt != nil && post.PublishedAt.After(time.Now()):
		d.PublishLabel = "Schedule"
	default:
		d.PublishLabel = "Publish"
	}
	ts := time.Now().In(loc)
	if post.PublishedAt != nil {
		ts = post.PublishedAt.In(loc)
	}
	d.TS = touchTime{MM: ts.Format("01"), JJ: ts.Format("02"), AA: ts.Format("2006"), HH: ts.Format("15"), MN: ts.Format("04"), SS: ts.Format("05")}
	for m := 1; m <= 12; m++ {
		v := fmt.Sprintf("%02d", m)
		d.TS.Months = append(d.TS.Months, monthChoice{Value: v, Label: v + "-" + time.Month(m).String()[:3], Selected: v == d.TS.MM})
	}
	if !isNew {
		d.LastEdit = "Last edited on " + wp.FormatDate(post.ModifiedAt.In(loc), "F j, Y") + " at " + wp.FormatDate(post.ModifiedAt.In(loc), "g:i a")
	}

	editorPost := *post
	editorPost.Type = "product"
	d.Editor = s.wpEditor(c, &editorPost)
	thumb := string(s.postThumbnailHTML(c, post, post.FeaturedMediaID))
	thumb = strings.NewReplacer("Set featured image", "Set product image", "Remove featured image", "Remove product image").Replace(thumb)
	d.Thumbnail = template.HTML(thumb)
	s.enqueueMedia(c, p.ID)
	s.pageAssets(c).Script("jquery-ui-sortable", "jquery-ui-datepicker")
	s.gooCatalogAssets(c)

	title := "Edit product"
	screen := "post?post_type=product"
	if isNew {
		title, screen = "Add new product", "post-new?post_type=product"
	}
	s.adminPage(c, screen, title, "post-php post-type-product", "admin/goo-product-edit", d, s.gooEditNotices(c, d.ViewURL)...)
}

// gooSamplePermalinkHTML is get_sample_permalink_html() for a product: …/product/<slug>/ [Edit].
func (s *Server) gooSamplePermalinkHTML(c *gin.Context, p *gooProduct) template.HTML {
	ctx := c.Request.Context()
	slug := p.Slug
	if slug == "" {
		slug = wp.SanitizeTitle(p.Name)
	}
	if slug == "" {
		slug = itoa64(p.ID)
	}
	if u, err := s.store.UniqueSlug(ctx, "product", 0, slug, p.ID); err == nil {
		slug = u
	}
	before := s.gooProductBaseURL(ctx)
	view := before + slug + "/"
	if p.Status != "publish" && p.Status != "private" {
		view = s.links(ctx).HomeURL("/?post_type=product&p=" + itoa64(p.ID) + "&preview=true")
	}
	esc := template.HTMLEscapeString
	return template.HTML(`<strong>Permalink:</strong>` + "\n" +
		`<span id="sample-permalink"><a href="` + esc(view) + `">` + esc(before) + `<span id="editable-post-name">` + esc(slug) + `</span>/</a></span>` + "\n" +
		`&lrm;<span id="edit-slug-buttons"><button type="button" class="edit-slug button button-small hide-if-no-js" aria-label="Edit permalink">Edit</button></span>` + "\n" +
		`<span id="editable-post-name-full">` + esc(slug) + `</span>`)
}

// gooProductBaseURL is the permalink of products without the slug: https://site/product/.
func (s *Server) gooProductBaseURL(ctx context.Context) string {
	return s.links(ctx).HomeURL("/" + s.gooProductBase() + "/")
}

// gooAttrRowFor is one attribute of the Attributes tab (and its values for the variation selects).
func (s *Server) gooAttrRowFor(ctx context.Context, a gooProductAttr, index string) gooAttrRow {
	row := gooAttrRow{Index: index, Name: a.Name, Taxonomy: a.Taxonomy, Key: a.Key(), Visible: a.Visible, Variation: a.Variation, Position: a.Position}
	if a.Taxonomy != "" {
		row.Name = a.Taxonomy
		row.Label = s.gooAttrLabel(ctx, a)
		selected := map[string]bool{}
		for _, o := range a.Options {
			selected[o] = true
		}
		terms, _ := s.gooAttributeTerms(ctx, a.Taxonomy)
		// The editor lists the terms of the product's language (pllEditorLang), but a selected term must
		// never drop out of the form: a translation keeps the source slug of an untranslated term, and
		// terms made before Multi Language have no language. Saving would otherwise wipe the attribute.
		listed := map[string]bool{}
		for _, t := range terms {
			listed[t.Slug] = true
		}
		for _, o := range a.Options {
			if !listed[o] {
				if all, err := s.gooAttributeTerms(gooPllUnfiltered(ctx), a.Taxonomy); err == nil {
					for _, t := range all {
						if selected[t.Slug] && !listed[t.Slug] {
							listed[t.Slug] = true
							terms = append(terms, t)
						}
					}
				}
				break
			}
		}
		for _, t := range terms {
			row.Terms = append(row.Terms, gooTermOpt{ID: t.ID, Slug: t.Slug, Name: t.Name, Selected: selected[t.Slug]})
			if selected[t.Slug] {
				row.Options = append(row.Options, gooOpt{t.Slug, t.Name})
			}
		}
		return row
	}
	row.Label = a.Name
	row.Values = strings.Join(a.Options, " | ")
	for _, o := range a.Options {
		row.Options = append(row.Options, gooOpt{o, o})
	}
	return row
}

func (s *Server) gooVariationRowFor(ctx context.Context, attrs []gooAttrRow, v *gooProduct, i int) gooVariationRow {
	r := gooVariationRow{Index: i, ID: v.ID, V: v, Enabled: v.Status == "publish",
		Regular: gooAmount(v.RegularPrice), Sale: gooAmount(v.SalePrice), SaleFrom: s.gooDateInput(v.SaleFrom), SaleTo: s.gooDateInput(v.SaleTo),
		Stock: gooIntStr(v.StockQuantity), LowStock: gooIntStr(v.LowStockAmount), Downloads: v.Downloads,
		DownloadLimit: gooLimitStr(v.DownloadLimit), DownloadExpiry: gooLimitStr(v.DownloadExpiry), ShipClass: v.ShippingClassID,
		TaxClass: v.TaxClass, MenuOrder: v.Post.MenuOrder}
	if v.ShippingClassID == 0 {
		r.ShipClass = -1
	}
	if v.ImageID > 0 {
		r.Thumb = s.gooAttachmentURL(ctx, v.ImageID, "thumbnail")
	}
	for _, a := range attrs {
		r.Attrs = append(r.Attrs, gooVarAttr{Key: a.Key, Label: a.Label, Options: a.Options, Selected: v.VariationAttrs[a.Key]})
	}
	for _, h := range gxsVariationFieldHooks {
		r.Extra += h(s, ctx, v, i)
	}
	return r
}

// gooEditNotices are WooCommerce's product_updated_messages plus the save errors.
func (s *Server) gooEditNotices(c *gin.Context, view string) []notice {
	v := template.HTMLEscapeString(view)
	var out []notice
	msg := map[string]string{
		"1":  `Product updated. <a href="` + v + `">View Product</a>`,
		"4":  "Product updated.",
		"6":  `Product published. <a href="` + v + `">View Product</a>`,
		"7":  "Product saved.",
		"8":  `Product submitted. <a target="_blank" href="` + v + `">Preview product</a>`,
		"9":  `Product scheduled. <a target="_blank" href="` + v + `">Preview product</a>`,
		"10": `Product draft updated. <a target="_blank" href="` + v + `">Preview product</a>`,
	}[c.Query("message")]
	if msg != "" {
		out = append(out, notice{Type: "success", Message: template.HTML(msg), Dismissible: true})
	}
	if n, _ := strconv.Atoi(c.Query("goo_variations_added")); n > 0 || c.Query("goo_variations_added") == "0" {
		text := strconv.Itoa(n) + " variations added"
		if n == 1 {
			text = "1 variation added"
		} else if n == 0 {
			text = "No variations added"
		}
		out = append(out, notice{Type: "success", Message: template.HTML(text), Dismissible: true})
	}
	for _, code := range strings.Split(c.Query("goo_error"), ",") {
		switch {
		case code == "":
		case code == "sku":
			out = append(out, notice{Type: "error", Message: "Invalid or duplicated SKU.", Dismissible: true})
		case strings.HasPrefix(code, "vsku"):
			out = append(out, notice{Type: "error", Message: template.HTML("#" + template.HTMLEscapeString(strings.TrimPrefix(code, "vsku")) + " &ndash; Variation SKU must be unique."), Dismissible: true})
		case code == "noattrs":
			out = append(out, notice{Type: "error", Message: "Before you can add a variation you need to add some variation attributes on the <strong>Attributes</strong> tab.", Dismissible: true})
		}
	}
	return out
}

// Saving ------------------------------------------------------------------------------------------------

// gooForm reads the posted form.
type gooForm struct{ c *gin.Context }

func (f gooForm) str(k string) string    { return strings.TrimSpace(f.c.PostForm(k)) }
func (f gooForm) raw(k string) string    { return f.c.PostForm(k) }
func (f gooForm) on(k string) bool       { v := f.c.PostForm(k); return v != "" && v != "no" && v != "0" }
func (f gooForm) list(k string) []string { return f.c.PostFormArray(k) }
func (f gooForm) has(k string) bool      { _, ok := f.c.GetPostForm(k); return ok }
func (f gooForm) ids(k string) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, raw := range f.c.PostFormArray(k) {
		for _, part := range strings.Split(raw, ",") {
			if id, _ := strconv.ParseInt(strings.TrimSpace(part), 10, 64); id > 0 && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// gooIndices are the sorted [i] indices of fields named prefix[i].
func (f gooForm) indices(prefix string) []string {
	seen := map[string]bool{}
	var out []string
	for name := range f.c.Request.PostForm {
		rest, ok := strings.CutPrefix(name, prefix+"[")
		if !ok {
			continue
		}
		idx, _, _ := strings.Cut(rest, "]")
		if !seen[idx] {
			seen[idx] = true
			out = append(out, idx)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, ea := strconv.Atoi(out[i])
		b, eb := strconv.Atoi(out[j])
		if ea == nil && eb == nil {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

// gooSaleDate parses a sale schedule date: from at 00:00:00, to at 23:59:59 (site time zone).
func (s *Server) gooSaleDate(v string, end bool) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02", v, s.loc())
	if err != nil {
		return nil
	}
	if end {
		t = t.Add(24*time.Hour - time.Second)
	}
	return &t
}

func gooIntPtrFrom(v string) *int {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	n := int(f)
	return &n
}

func gooLimitFrom(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return -1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// gooProductSave is the POST of the product editor (editpost() + WC_Meta_Box_Product_Data::save()).
func (s *Server) gooProductSave(c *gin.Context, id int64) {
	ctx := c.Request.Context()
	u := currentUser(c)
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type == gooTypeVariation {
		c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
		return
	}
	if !s.checkNonce(c, "update-post_"+itoa64(p.ID)) {
		s.denyNonce(c)
		return
	}
	if !gooCanProduct(u, "edit", p.Post) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	f := gooForm{c}
	post := p.Post
	wasPublished := post.Status == "publish" || post.Status == "private" || post.Status == "future"
	var errs []string

	// The post: title, content, short description, slug, reviews, menu order, image, author, date.
	p.Name = f.str("post_title")
	content := f.raw("content")
	excerpt := f.raw("excerpt")
	if !u.Can("unfiltered_html", nil) {
		content, excerpt = wp.KsesPost(content), wp.KsesPost(excerpt)
	}
	post.Content = strings.ReplaceAll(content, "\r\n", "\n")
	post.Excerpt = strings.ReplaceAll(excerpt, "\r\n", "\n")
	if f.has("post_name") {
		p.Slug = f.str("post_name")
		post.Slug = p.Slug
	}
	if f.has("menu_order") {
		post.MenuOrder, _ = strconv.Atoi(f.str("menu_order"))
	}
	post.CommentStatus = map[bool]string{true: "open", false: "closed"}[f.str("comment_status") == "open"]
	if raw := f.str("_thumbnail_id"); raw != "" {
		tid, _ := strconv.ParseInt(raw, 10, 64)
		p.ImageID = max(tid, 0)
	}
	if a, _ := strconv.ParseInt(f.str("post_author_override"), 10, 64); a > 0 && u.Can("edit_others_products", nil) {
		post.AuthorID = a
	}
	if post.AuthorID == 0 {
		post.AuthorID = u.ID
	}
	if f.str("edit_date") == "1" && f.str("aa") != "" {
		num := func(k string) int { n, _ := strconv.Atoi(f.str(k)); return n }
		when := time.Date(num("aa"), time.Month(num("mm")), num("jj"), num("hh"), num("mn"), num("ss"), 0, s.loc())
		post.PublishedAt = &when
	}

	// Publish box: status from the button, visibility, catalog visibility and featured.
	visibility := defaultStr(f.str("visibility"), "public")
	post.Password = ""
	if visibility == "password" {
		post.Password = f.raw("post_password")
	}
	status := post.Status
	switch {
	case f.str("publish") != "":
		if u.Can("publish_products", nil) {
			status = "publish"
			if post.PublishedAt != nil && post.PublishedAt.After(time.Now()) {
				status = "future"
			}
		} else {
			status = "pending"
		}
	case f.str("save") != "":
		if !wasPublished {
			status = "draft"
			if f.str("post_status") == "pending" {
				status = "pending"
			}
		} else if st := f.str("post_status"); st == "draft" || st == "pending" {
			status = st
		}
	}
	if visibility == "private" && u.Can("publish_products", nil) && (status == "publish" || wasPublished) {
		status = "private"
	} else if status == "private" && visibility != "private" {
		status = "publish"
	}
	if status == "auto-draft" {
		status = "draft"
	}
	p.Status = status
	if f.has("_visibility") {
		p.CatalogVisibility = f.str("_visibility")
	}
	if f.has("_visibility") || f.has("_featured") || f.has("current_featured") {
		p.Featured = f.on("_featured")
	}

	// Product data: type, flags.
	typ := defaultStr(f.str("product-type"), p.Type)
	if typ == gooTypeVariation {
		typ = gooTypeSimple
	}
	known := typ == gooTypeSimple || typ == gooTypeVariable || typ == gooTypeGrouped || typ == gooTypeExternal
	if def := gooProductTypes[typ]; !known && (def == nil || (def.Module != "" && !s.gooExtOn(def.Module))) {
		typ = gooTypeSimple
	}
	p.Type = typ
	p.Virtual = f.on("_virtual")
	p.Downloadable = f.on("_downloadable")
	if def := gooProductTypes[typ]; def != nil && def.Virtual {
		p.Virtual = true
	}

	// General.
	p.RegularPrice = s.gooParseAmount(f.str("_regular_price"))
	p.SalePrice = s.gooParseAmount(f.str("_sale_price"))
	p.SaleFrom = s.gooSaleDate(f.str("_sale_price_dates_from"), false)
	p.SaleTo = s.gooSaleDate(f.str("_sale_price_dates_to"), true)
	if p.SaleTo != nil && p.SaleFrom == nil {
		now := time.Now().In(s.loc())
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc())
		p.SaleFrom = &from // WooCommerce starts a sale with only an end date today
	}
	p.Downloads = gooDownloadsFrom(f.list("_wc_file_names[]"), f.list("_wc_file_urls[]"), f.list("_wc_file_hashes[]"))
	p.DownloadLimit = gooLimitFrom(f.str("_download_limit"))
	p.DownloadExpiry = gooLimitFrom(f.str("_download_expiry"))
	if f.has("_tax_status") {
		p.TaxStatus = f.str("_tax_status")
	}
	if f.has("_tax_class") {
		p.TaxClass = f.str("_tax_class")
	}
	p.ProductURL = f.str("_product_url")
	p.ButtonText = f.str("_button_text")

	// Inventory.
	if sku := f.str("_sku"); f.has("_sku") && sku != p.SKU {
		if s.gooSKUTaken(ctx, sku, p.ID) {
			errs = append(errs, "sku")
		} else {
			p.SKU = sku
		}
	}
	p.ManageStock = f.on("_manage_stock")
	if p.ManageStock {
		p.StockQuantity = gooIntPtrFrom(f.str("_stock"))
		p.Backorders = defaultStr(f.str("_backorders"), "no")
		p.LowStockAmount = gooIntPtrFrom(f.str("_low_stock_amount"))
	} else {
		p.StockQuantity, p.LowStockAmount = nil, nil
		if f.has("_backorders") {
			p.Backorders = defaultStr(f.str("_backorders"), "no")
		}
		if st := f.str("_stock_status"); st != "" {
			p.StockStatus = st
		}
	}
	p.SoldIndividually = f.on("_sold_individually")

	// Shipping.
	p.Weight, p.Length, p.Width, p.Height = f.str("_weight"), f.str("_length"), f.str("_width"), f.str("_height")
	if sc, _ := strconv.ParseInt(f.str("product_shipping_class"), 10, 64); sc > 0 {
		p.ShippingClassID = sc
	} else {
		p.ShippingClassID = 0
	}

	// Linked products.
	p.UpsellIDs = gooWithout(f.ids("upsell_ids[]"), p.ID)
	p.CrossSellIDs = gooWithout(f.ids("crosssell_ids[]"), p.ID)
	if typ == gooTypeGrouped {
		p.Children = gooWithout(f.ids("grouped_products[]"), p.ID)
	} else if !gooSubsVariableLike(p.Type) {
		p.Children = nil
	}

	// Attributes.
	if f.has("goo_attributes_loaded") {
		p.Attributes = s.gooAttributesFromForm(ctx, f)
	}
	if f.has("goo_attributes_loaded") || f.has("goo_variations_loaded") {
		p.DefaultAttributes = map[string]string{}
		for _, a := range p.Attributes {
			if v := f.raw("default_attribute_" + a.Key()); v != "" && a.Variation {
				p.DefaultAttributes[a.Key()] = v
			}
		}
	}

	// Advanced.
	p.PurchaseNote = f.raw("_purchase_note")

	// Images.
	if f.has("product_image_gallery") {
		p.GalleryIDs = f.ids("product_image_gallery")
	}

	// Module panels save their fields (into p.Meta and the like).
	for _, pn := range gooProductPanels {
		if pn.Save == nil || (pn.Module != "" && !s.gooExtOn(pn.Module)) {
			continue
		}
		if len(pn.ShowFor) > 0 && !containsStr(pn.ShowFor, p.Type) {
			continue
		}
		pn.Save(s, c, p)
	}

	if err := s.gooSaveProduct(ctx, p, u.ID); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// Categories and tags.
	var catIDs []int64
	for _, raw := range f.list("tax_input[product_cat][]") {
		if rest, ok := strings.CutPrefix(raw, "new:"); ok {
			parentRaw, name, _ := strings.Cut(rest, ":")
			parent, _ := strconv.ParseInt(parentRaw, 10, 64)
			if strings.TrimSpace(name) == "" || !u.Can("manage_product_terms", nil) {
				continue
			}
			t, err := s.store.TermByName(ctx, "product_cat", strings.TrimSpace(name))
			if err != nil || t.ParentID != parent {
				t = &store.Term{Taxonomy: "product_cat", Name: strings.TrimSpace(name), ParentID: parent}
				if err := s.store.InsertTerm(ctx, t); err != nil {
					continue
				}
			}
			catIDs = append(catIDs, t.ID)
			continue
		}
		if cid, _ := strconv.ParseInt(raw, 10, 64); cid > 0 {
			catIDs = append(catIDs, cid)
		}
	}
	if len(catIDs) == 0 {
		if t, err := s.store.TermBySlug(ctx, "product_cat", s.gooDefaultCatSlug()); err == nil {
			catIDs = []int64{t.ID}
		}
	}
	_ = s.store.SetPostTerms(ctx, p.ID, "product_cat", catIDs)
	if f.has("tax_input[product_tag]") {
		tagIDs := s.postTermIDsByNames(ctx, p.Post, "product_tag", strings.Split(f.raw("tax_input[product_tag]"), ","))
		_ = s.store.SetPostTerms(ctx, p.ID, "product_tag", tagIDs)
	}

	// Variations: the rows of the form, removals, then the Variations tab's actions.
	added := -1
	tab := f.str("goo_tab")
	if !regexp.MustCompile(`^[a-z0-9_-]{1,40}$`).MatchString(tab) {
		tab = ""
	}
	if gooSubsVariableLike(p.Type) {
		errs = append(errs, s.gooSaveVariationsForm(c, f, p)...)
		switch {
		case f.str("goo_generate_variations") != "":
			tab = "variations"
			if n, err := s.gooGenerateVariations(ctx, p, u.ID); err == nil {
				added = n
			} else {
				errs = append(errs, "noattrs")
			}
		case f.str("goo_add_variation") != "":
			tab = "variations"
			if _, err := s.gooAddVariation(ctx, p, u.ID); err != nil {
				errs = append(errs, "noattrs")
			}
		case f.str("goo_variation_bulk") != "":
			tab = "variations"
			s.gooVariationBulk(ctx, p, f.str("variable_bulk_action"), f.str("variable_bulk_value"), u.ID)
		}
		if fresh, err := s.gooGetProduct(ctx, p.ID); err == nil {
			_ = s.gooSyncVariable(ctx, fresh)
			s.gooPurgeProduct(ctx, fresh)
		}
	}
	if f.str("goo_save_attributes") != "" {
		tab = "attributes"
	}
	s.afterPostSave(ctx, post)

	msg := "1"
	switch {
	case !wasPublished && (p.Status == "publish" || p.Status == "private"):
		msg = "6"
	case p.Status == "future":
		msg = "9"
	case p.Status == "pending":
		msg = "8"
	case p.Status == "draft":
		msg = "10"
	}
	target := links.EditLink(p.ID) + "&message=" + msg
	if added >= 0 {
		target += "&goo_variations_added=" + strconv.Itoa(added)
	}
	if len(errs) > 0 {
		target += "&goo_error=" + url.QueryEscape(strings.Join(errs, ","))
	}
	if tab != "" {
		target += "&goo_tab=" + tab
	}
	c.Redirect(http.StatusFound, target)
}

// gooDefaultCatSlug is the default product category (Uncategorized).
func (s *Server) gooDefaultCatSlug() string {
	if id := s.gooOptInt("default_product_cat"); id > 0 {
		if t, err := s.store.GetTerm(context.Background(), int64(id)); err == nil {
			return t.Slug
		}
	}
	return "uncategorized"
}

func gooWithout(ids []int64, id int64) []int64 {
	out := ids[:0]
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// gooDownloadsFrom builds downloadable files from the repeater's columns (empty rows skipped).
func gooDownloadsFrom(names, urls, hashes []string) []gooDownload {
	var out []gooDownload
	for i, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		d := gooDownload{File: u}
		if i < len(names) {
			d.Name = strings.TrimSpace(names[i])
		}
		if d.Name == "" {
			d.Name = u[strings.LastIndex(u, "/")+1:]
		}
		if i < len(hashes) {
			d.ID = strings.TrimSpace(hashes[i])
		}
		out = append(out, d)
	}
	return out
}

// gooAttributesFromForm reads the Attributes tab (attribute_names[i], attribute_values[i],
// attribute_visibility[i], attribute_variation[i], attribute_position[i]). Taxonomy values are term
// ids (or slugs); attribute_new_values[i] creates terms ("a | b").
func (s *Server) gooAttributesFromForm(ctx context.Context, f gooForm) []gooProductAttr {
	var out []gooProductAttr
	seen := map[string]bool{}
	for _, i := range f.indices("attribute_names") {
		name := f.str("attribute_names[" + i + "]")
		if name == "" {
			continue
		}
		pos, _ := strconv.Atoi(f.str("attribute_position[" + i + "]"))
		a := gooProductAttr{Name: name, Position: pos, Visible: f.on("attribute_visibility[" + i + "]"), Variation: f.on("attribute_variation[" + i + "]")}
		if strings.HasPrefix(name, "pa_") {
			ga := s.gooAttributeByTaxonomy(ctx, name)
			if ga == nil {
				continue
			}
			a.ID, a.Taxonomy, a.Name = ga.ID, name, ga.Name
			picked := map[string]bool{}
			for _, raw := range f.list("attribute_values[" + i + "][]") {
				var t *store.Term
				var err error
				if tid, perr := strconv.ParseInt(raw, 10, 64); perr == nil && tid > 0 {
					t, err = s.store.GetTerm(ctx, tid)
					if err == nil && t.Taxonomy != name {
						err = store.ErrNotFound
					}
				} else {
					t, err = s.store.TermBySlug(ctx, name, raw)
				}
				if err == nil && !picked[t.Slug] {
					picked[t.Slug] = true
					a.Options = append(a.Options, t.Slug)
				}
			}
			for _, v := range gooSplitValues(f.raw("attribute_new_values[" + i + "]")) {
				t, err := s.store.TermByName(ctx, name, v)
				if err != nil {
					t = &store.Term{Taxonomy: name, Name: v}
					if s.store.InsertTerm(ctx, t) != nil {
						continue
					}
				}
				if !picked[t.Slug] {
					picked[t.Slug] = true
					a.Options = append(a.Options, t.Slug)
				}
			}
		} else {
			a.Options = gooSplitValues(f.raw("attribute_values[" + i + "]"))
		}
		if len(a.Options) == 0 || seen[a.Key()] {
			continue
		}
		seen[a.Key()] = true
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	for i := range out {
		out[i].Position = i
	}
	return out
}

// gooSplitValues is wc_get_text_attributes(): values separated by | (commas accepted too when there is no |).
func gooSplitValues(v string) []string {
	sep := "|"
	if !strings.Contains(v, "|") && strings.Contains(v, ",") {
		sep = ","
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(v, sep) {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

// gooSaveVariationsForm saves the variation rows (WC_Meta_Box_Product_Data::save_variations()) and
// deletes the variations removed from the form. Returns error codes (duplicate variation SKUs).
func (s *Server) gooSaveVariationsForm(c *gin.Context, f gooForm, parent *gooProduct) []string {
	ctx := c.Request.Context()
	u := currentUser(c)
	var errs []string
	if !f.has("goo_variations_loaded") {
		return nil
	}
	existing, _ := s.gooVariations(ctx, parent.ID, true)
	byID := map[int64]*gooProduct{}
	for _, v := range existing {
		byID[v.ID] = v
	}
	kept := map[int64]bool{}
	removed := map[int64]bool{}
	for _, id := range f.ids("goo_delete_variations[]") {
		removed[id] = true
	}
	for _, i := range f.indices("variable_post_id") {
		vid, _ := strconv.ParseInt(f.str("variable_post_id["+i+"]"), 10, 64)
		v := byID[vid]
		if v == nil || removed[vid] {
			continue
		}
		kept[vid] = true
		k := func(name string) string { return name + "[" + i + "]" }
		for _, a := range parent.Attributes {
			if a.Variation {
				v.VariationAttrs[a.Key()] = f.raw("attribute_" + a.Key() + "[" + i + "]")
			}
		}
		for key := range v.VariationAttrs {
			if !gooHasVariationAttr(parent, key) {
				delete(v.VariationAttrs, key)
			}
		}
		if sku := f.str(k("variable_sku")); sku != v.SKU {
			if s.gooSKUTaken(ctx, sku, v.ID) {
				errs = append(errs, "vsku"+itoa64(v.ID))
			} else {
				v.SKU = sku
			}
		}
		v.Status = "private"
		if f.on(k("variable_enabled")) {
			v.Status = "publish"
		}
		v.Downloadable = f.on(k("variable_is_downloadable"))
		v.Virtual = f.on(k("variable_is_virtual"))
		v.ManageStock = f.on(k("variable_manage_stock"))
		v.RegularPrice = s.gooParseAmount(f.str(k("variable_regular_price")))
		v.SalePrice = s.gooParseAmount(f.str(k("variable_sale_price")))
		v.SaleFrom = s.gooSaleDate(f.str(k("variable_sale_price_dates_from")), false)
		v.SaleTo = s.gooSaleDate(f.str(k("variable_sale_price_dates_to")), true)
		if v.ManageStock {
			v.StockQuantity = gooIntPtrFrom(f.str(k("variable_stock")))
			v.LowStockAmount = gooIntPtrFrom(f.str(k("variable_low_stock_amount")))
		} else {
			v.StockQuantity, v.LowStockAmount = nil, nil
		}
		v.Backorders = defaultStr(f.str(k("variable_backorders")), "no")
		if st := f.str(k("variable_stock_status")); st != "" {
			v.StockStatus = st
		}
		v.Weight, v.Length, v.Width, v.Height = f.str(k("variable_weight")), f.str(k("variable_length")), f.str(k("variable_width")), f.str(k("variable_height"))
		if sc, _ := strconv.ParseInt(f.str(k("variable_shipping_class")), 10, 64); sc > 0 {
			v.ShippingClassID = sc
		} else {
			v.ShippingClassID = 0
		}
		if f.has(k("variable_tax_class")) {
			v.TaxClass = f.str(k("variable_tax_class"))
		}
		v.Description = f.raw(k("variable_description"))
		if img := f.str(k("upload_image_id")); img != "" {
			iid, _ := strconv.ParseInt(img, 10, 64)
			v.ImageID = max(iid, 0)
		}
		vid64 := itoa64(v.ID)
		v.Downloads = gooDownloadsFrom(f.list("_wc_variation_file_names["+vid64+"][]"), f.list("_wc_variation_file_urls["+vid64+"][]"),
			f.list("_wc_variation_file_hashes["+vid64+"][]"))
		v.DownloadLimit = gooLimitFrom(f.str(k("variable_download_limit")))
		v.DownloadExpiry = gooLimitFrom(f.str(k("variable_download_expiry")))
		if f.has(k("variable_menu_order")) {
			v.Post.MenuOrder, _ = strconv.Atoi(f.str(k("variable_menu_order")))
		}
		for _, h := range gxsVariationSaveHooks {
			h(s, c, parent, v, i)
		}
		v.Name = s.gooVariationTitle(ctx, parent, v)
		v.ParentID = parent.ID
		if err := s.gooSaveProduct(ctx, v, u.ID); err != nil {
			errs = append(errs, "vsave"+itoa64(v.ID))
		}
	}
	for _, v := range existing {
		if !kept[v.ID] || removed[v.ID] {
			s.gooPllDeleteVariation(ctx, v.ID) // Golylang: and its translations (goo_golylang.go)
			_ = s.store.DeletePost(ctx, v.ID)
		}
	}
	return errs
}

func gooHasVariationAttr(p *gooProduct, key string) bool {
	for _, a := range p.Attributes {
		if a.Variation && a.Key() == key {
			return true
		}
	}
	return false
}

// gooVariationTitle is the variation's post title: "Parent - Red, Large".
func (s *Server) gooVariationTitle(ctx context.Context, parent *gooProduct, v *gooProduct) string {
	var vals []string
	for _, a := range parent.Attributes {
		if !a.Variation {
			continue
		}
		if val := v.VariationAttrs[a.Key()]; val != "" {
			vals = append(vals, s.gooAttrValueLabel(ctx, a, val))
		}
	}
	if len(vals) == 0 {
		return parent.Name
	}
	return parent.Name + " - " + strings.Join(vals, ", ")
}

// gooNewVariation is a new enabled variation of a parent.
func (s *Server) gooNewVariation(ctx context.Context, parent *gooProduct, attrs map[string]string, order int) *gooProduct {
	v := gooNewProduct(gooTypeVariation)
	v.ParentID = parent.ID
	v.TaxClass = "parent"
	v.Status = "publish"
	v.VariationAttrs = attrs
	v.Name = s.gooVariationTitle(ctx, parent, v)
	v.Post = &store.Post{Type: "product_variation", CommentStatus: "closed", PingStatus: "closed", MenuOrder: order, Meta: map[string]any{}}
	return v
}

// gooGenerateVariations is WC_Product_Data_Store_CPT::create_all_product_variations(): every
// combination of the variation attributes that no variation covers yet (at most 50 per run).
func (s *Server) gooGenerateVariations(ctx context.Context, p *gooProduct, editorID int64) (int, error) {
	var attrs []gooProductAttr
	for _, a := range p.Attributes {
		if a.Variation && len(a.Options) > 0 {
			attrs = append(attrs, a)
		}
	}
	if len(attrs) == 0 {
		return 0, fmt.Errorf("no variation attributes")
	}
	existing, _ := s.gooVariations(ctx, p.ID, true)
	combos := []map[string]string{{}}
	for _, a := range attrs {
		var next []map[string]string
		for _, base := range combos {
			for _, o := range a.Options {
				m := map[string]string{}
				for k, v := range base {
					m[k] = v
				}
				m[a.Key()] = o
				next = append(next, m)
			}
		}
		combos = next
	}
	added := 0
	order := len(existing)
	for _, combo := range combos {
		if added >= gooMaxLinkedVariations {
			break
		}
		covered := false
		for _, v := range existing {
			match := true
			for k, val := range combo {
				if got := v.VariationAttrs[k]; got != "" && got != val {
					match = false
					break
				}
			}
			if match {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		v := s.gooNewVariation(ctx, p, combo, order)
		if err := s.gooSaveProduct(ctx, v, editorID); err != nil {
			return added, err
		}
		existing = append(existing, v)
		order++
		added++
	}
	return added, nil
}

// gooAddVariation is "Add manually": one variation with "Any …" attributes.
func (s *Server) gooAddVariation(ctx context.Context, p *gooProduct, editorID int64) (*gooProduct, error) {
	attrs := map[string]string{}
	n := 0
	for _, a := range p.Attributes {
		if a.Variation {
			attrs[a.Key()] = ""
			n++
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("no variation attributes")
	}
	existing, _ := s.gooVariations(ctx, p.ID, true)
	v := s.gooNewVariation(ctx, p, attrs, len(existing))
	return v, s.gooSaveProduct(ctx, v, editorID)
}

// gooVariationBulk runs a "Bulk actions" choice of the Variations tab (WC_AJAX::bulk_edit_variations()).
func (s *Server) gooVariationBulk(ctx context.Context, p *gooProduct, action, value string, editorID int64) {
	vars, _ := s.gooVariations(ctx, p.ID, true)
	if action == "delete_all" {
		for _, v := range vars {
			s.gooPllDeleteVariation(ctx, v.ID) // Golylang: and its translations
			_ = s.store.DeletePost(ctx, v.ID)
		}
		return
	}
	amount := func() *float64 { return s.gooParseAmount(strings.TrimSuffix(value, "%")) }
	for _, v := range vars {
		switch action {
		case "toggle_enabled":
			v.Status = map[bool]string{true: "private", false: "publish"}[v.Status == "publish"]
		case "toggle_downloadable":
			v.Downloadable = !v.Downloadable
		case "toggle_virtual":
			v.Virtual = !v.Virtual
		case "toggle_manage_stock":
			v.ManageStock = !v.ManageStock
		case "variable_regular_price":
			v.RegularPrice = amount()
		case "variable_sale_price":
			v.SalePrice = amount()
		case "variable_regular_price_increase":
			v.RegularPrice = s.gooChangePrice(v.RegularPrice, "2", value, nil)
		case "variable_regular_price_decrease":
			v.RegularPrice = s.gooChangePrice(v.RegularPrice, "3", value, nil)
		case "variable_sale_price_increase":
			v.SalePrice = s.gooChangePrice(v.SalePrice, "2", value, nil)
		case "variable_sale_price_decrease":
			v.SalePrice = s.gooChangePrice(v.SalePrice, "3", value, nil)
		case "variable_stock":
			if v.ManageStock {
				v.StockQuantity = gooIntPtrFrom(value)
			}
		case "variable_low_stock_amount":
			v.LowStockAmount = gooIntPtrFrom(value)
		case "variable_stock_status_instock", "variable_stock_status_outofstock", "variable_stock_status_onbackorder":
			if !v.ManageStock {
				v.StockStatus = strings.TrimPrefix(action, "variable_stock_status_")
			}
		case "variable_weight":
			v.Weight = value
		case "variable_length":
			v.Length = value
		case "variable_width":
			v.Width = value
		case "variable_height":
			v.Height = value
		case "variable_download_limit":
			v.DownloadLimit = gooLimitFrom(value)
		case "variable_download_expiry":
			v.DownloadExpiry = gooLimitFrom(value)
		default:
			return
		}
		_ = s.gooSaveProduct(ctx, v, editorID)
	}
}
