package server

import (
	"context"
	"errors"
	"html"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/microcosm-cc/bluemonday"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
	"wpgo/internal/wp"
)

// Sample data for the editor's dynamic widgets (contract §11.6): a DynContext is built from a
// preview object — post:<id>, term:<tax>:<id>, author:<id>, type:<ptype>, search:<q>, front,
// home, 404, latest — by resolving it to the same siteQuery the front end would and running
// buildSiteView over it, so the editor shows what the site shows (menus, archive titles,
// pagination, Pursae Multi Language included). Post content is sanitized with the UGC policy:
// the editor shows it read-only.

// wbPreview is a parsed preview object.
type wbPreview struct {
	Object string // as written (normalized)
	Kind   string // post, term, author, type, search, front, home, 404, latest
	ID     int64
	Name   string // term taxonomy or post type
	Search string
}

var (
	errWBBadPreview  = errors.New("web builder: bad preview object")
	errWBPreviewNone = errors.New("web builder: preview object names nothing")
	wbSlugRe         = regexp.MustCompile(webbuilder.SlugPattern)
)

// wbParsePreview parses a preview object (errWBBadPreview when it is malformed).
func wbParsePreview(raw string) (wbPreview, error) {
	p := wbPreview{Object: raw}
	if raw == "" || len(raw) > webbuilder.MaxPreviewObjectBytes || !utf8.ValidString(raw) {
		return p, errWBBadPreview
	}
	id := func(s string) (int64, bool) {
		if s == "" || s[0] == '0' || len(s) > 19 {
			return 0, false
		}
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil && n > 0
	}
	switch raw {
	case webbuilder.PreviewFront, webbuilder.PreviewHome, webbuilder.Preview404, webbuilder.PreviewLatest:
		p.Kind = raw
		return p, nil
	}
	kind, rest, ok := strings.Cut(raw, ":")
	if !ok {
		return p, errWBBadPreview
	}
	p.Kind = kind
	switch kind {
	case "post", "author":
		if p.ID, ok = id(rest); !ok {
			return p, errWBBadPreview
		}
	case "term":
		tax, n, found := strings.Cut(rest, ":")
		if !found || !wbSlugRe.MatchString(tax) {
			return p, errWBBadPreview
		}
		if p.ID, ok = id(n); !ok {
			return p, errWBBadPreview
		}
		p.Name = tax
	case "type":
		if !wbSlugRe.MatchString(rest) {
			return p, errWBBadPreview
		}
		p.Name = rest
	case "search":
		if utf8.RuneCountInString(rest) > webbuilder.MaxPreviewSearchRunes || strings.IndexFunc(rest, unicode.IsControl) >= 0 {
			return p, errWBBadPreview
		}
		p.Search = rest
	default:
		return p, errWBBadPreview
	}
	return p, nil
}

// wbPreviewObjectOf is the stored preview object of a template, or its type's default.
func wbPreviewObjectOf(t *store.WBTemplate) string {
	if t == nil {
		return webbuilder.PreviewFront
	}
	if t.Preview != "" {
		if _, err := wbParsePreview(t.Preview); err == nil {
			return t.Preview
		}
	}
	if d, ok := webbuilder.DefaultPreviews[t.Type]; ok {
		return d
	}
	return webbuilder.PreviewFront
}

// wbTemplatePostType is the post type a template is about: post / page for the single types,
// the first include/singular/<ptype> rule for TemplateSingle.
func wbTemplatePostType(t *store.WBTemplate) string {
	switch t.Type {
	case webbuilder.TemplateSinglePost:
		return "post"
	case webbuilder.TemplateSinglePage:
		return "page"
	case webbuilder.TemplateSingleProduct:
		return "product"
	case webbuilder.TemplateSingle:
		for _, c := range webbuilder.ParseConditions(t.Conditions) {
			if !c.Exclude && c.PostType != "" {
				return c.PostType
			}
		}
	}
	return ""
}

// wbResolvePreview turns a preview object into the main query the front end would build for it
// (nil, errWBPreviewNone when it names nothing the user may see). t gives "latest" its meaning.
func (s *Server) wbResolvePreview(c *gin.Context, pv wbPreview, t *store.WBTemplate) (*siteQuery, error) {
	ctx := c.Request.Context()
	q := &siteQuery{Preview: true}
	switch pv.Kind {
	case webbuilder.PreviewLatest:
		if t == nil {
			return s.home(c, q)
		}
		typ := wbTemplatePostType(t)
		if typ == "" {
			return s.home(c, q)
		}
		if typ == "page" && s.settings.String("show_on_front") == "page" {
			if id := int64(s.settings.Int("page_on_front")); id > 0 {
				if p, err := s.store.GetPost(ctx, id); err == nil && p.Status == "publish" {
					sq, err := s.singular(c, q, p)
					if err == nil {
						sq.Kind = "front-page"
					}
					return sq, err
				}
			}
		}
		posts, _, err := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{typ}, Statuses: []string{"publish"}, OrderBy: "date", Order: "DESC", Page: 1, PerPage: 1})
		if err != nil || len(posts) == 0 {
			return nil, errWBPreviewNone
		}
		return s.wbPreviewSingular(c, q, posts[0])
	case "post":
		p, err := s.store.GetPost(ctx, pv.ID)
		if err != nil {
			return nil, errWBPreviewNone
		}
		u := currentUser(c)
		if !u.Can("read_post", p) && !u.Can("edit_post", p) {
			return nil, errWBPreviewNone
		}
		return s.wbPreviewSingular(c, q, p)
	case "term":
		tm, err := s.store.GetTerm(ctx, pv.ID)
		if err != nil || tm.Taxonomy != pv.Name {
			return nil, errWBPreviewNone
		}
		if tm.Taxonomy == "product_cat" || tm.Taxonomy == "product_tag" {
			if !s.gooOn() {
				return nil, errWBPreviewNone
			}
			post := s.gooSyntheticPost(ctx, tm.ID, tm.Name, "", nil)
			post.Excerpt = tm.Description
			post.Meta[wbPreviewURLMeta] = s.gooTermLink(ctx, tm)
			return &siteQuery{Preview: true, Kind: "page", Post: post, Store: tm.Taxonomy, StoreTax: tm.Taxonomy, StoreTerm: tm.ID}, nil
		}
		switch tm.Taxonomy {
		case "category":
			q.Kind = "category"
		case "post_tag":
			q.Kind = "tag"
		default:
			return nil, errWBPreviewNone // v1 routes category and post_tag archives only
		}
		q.Term = tm
		return q, nil
	case "author":
		au, err := s.store.UserByID(ctx, pv.ID)
		if err != nil {
			return nil, errWBPreviewNone
		}
		q.Kind, q.Author = "author", au
		return q, nil
	case "type":
		if pv.Name == "product" {
			if !s.gooOn() {
				return nil, errWBPreviewNone
			}
			title, slug, id := s.gooL10nText(ctx, "Shop"), "shop", s.gooPageIDIn(ctx, "shop")
			var src *store.Post
			if id > 0 {
				if pg, err := s.store.GetPost(ctx, id); err == nil {
					title, slug, src = pg.Title, pg.Slug, pg
				}
			}
			post := s.gooSyntheticPost(ctx, max(id, 0), html.UnescapeString(title), slug, src)
			post.Meta[wbPreviewURLMeta] = s.gooShopURL(ctx)
			return &siteQuery{Preview: true, Kind: "page", Post: post, Store: webbuilder.StoreKindShop}, nil
		}
		if siteArchiveType(pv.Name) == nil {
			return nil, errWBPreviewNone
		}
		q.Kind, q.PostType = "post-type-archive", pv.Name
		return q, nil
	case "search":
		q.Kind, q.Search = "search", strings.TrimSpace(pv.Search)
		return q, nil
	case webbuilder.PreviewFront:
		return s.home(c, q)
	case webbuilder.PreviewHome:
		if s.settings.String("show_on_front") == "page" {
			if id := s.golylangPage(c, int64(s.settings.Int("page_for_posts"))); id > 0 {
				if p, err := s.store.GetPost(ctx, id); err == nil && p.Status == "publish" {
					q.Kind, q.PostsPage = "home", p
					return q, nil
				}
			}
		}
		q.Kind = "home"
		return q, nil
	case webbuilder.Preview404:
		q.Kind = "404"
		return q, nil
	}
	return nil, errWBBadPreview
}

// wbPreviewURLMeta carries the front URL of a synthetic store preview (wbPreviewURL).
const wbPreviewURLMeta = "_wb_preview_url"

// wbPreviewSingular is singular() for a preview: the static front page keeps its front-page kind;
// a product is the store's product view (Pursae Commerce).
func (s *Server) wbPreviewSingular(c *gin.Context, q *siteQuery, p *store.Post) (*siteQuery, error) {
	if p.Type == "product" {
		return s.wbPreviewProduct(c, q, p)
	}
	sq, err := s.singular(c, q, p)
	if err != nil {
		return nil, errWBPreviewNone
	}
	if sq.Kind == "page" && s.settings.String("show_on_front") == "page" && p.ID == s.golylangPage(c, int64(s.settings.Int("page_on_front"))) {
		sq.Kind = "front-page"
	}
	return sq, nil
}

// wbPreviewProduct is the store's single product view of p as a preview query.
func (s *Server) wbPreviewProduct(c *gin.Context, q *siteQuery, p *store.Post) (*siteQuery, error) {
	ctx := c.Request.Context()
	if !s.gooOn() {
		return nil, errWBPreviewNone
	}
	gp, err := s.gooGetProduct(ctx, p.ID)
	if err != nil || gp.Type == gooTypeVariation {
		return nil, errWBPreviewNone
	}
	post := s.gooSyntheticPost(ctx, gp.ID, gp.Name, s.gooProductBase()+"/"+gp.Slug, p)
	post.Excerpt = wp.StripTags(p.Excerpt)
	u := s.gooPermalink(ctx, gp)
	if p.Status != "publish" && p.Status != "private" {
		u += previewSep(u) + "preview=true"
	}
	post.Meta[wbPreviewURLMeta] = u
	sq := *q
	sq.Kind, sq.Post, sq.Store = "page", post, webbuilder.StoreKindProduct
	return &sq, nil
}

// wbPreviewLabel is the "Preview as" label of a resolved preview ("Post: Hello world").
func (s *Server) wbPreviewLabel(q *siteQuery) string {
	if q.Post != nil {
		switch q.Store {
		case webbuilder.StoreKindProduct:
			return "Product: " + wbPlain(q.Post.Title)
		case webbuilder.StoreKindShop:
			return "Shop"
		case webbuilder.StoreKindProductCat:
			return "Product category: " + wbPlain(q.Post.Title)
		case webbuilder.StoreKindProductTag:
			return "Product tag: " + wbPlain(q.Post.Title)
		}
	}
	switch q.Kind {
	case "single", "page":
		if q.Post != nil {
			return wbTypeSingular(q.Post.Type) + ": " + wbPlain(q.Post.Title)
		}
	case "front-page":
		return "Front Page"
	case "home":
		if q.PostsPage != nil {
			return "Posts Page: " + wbPlain(q.PostsPage.Title)
		}
		return "Posts Page"
	case "category":
		return "Category: " + q.Term.Name
	case "tag":
		return "Tag: " + q.Term.Name
	case "taxonomy":
		return "Term: " + q.Term.Name
	case "author":
		return "Author: " + q.Author.DisplayName
	case "post-type-archive":
		return "Archive: " + wbTypePlural(q.PostType)
	case "search":
		return "Search: " + q.Search
	case "404":
		return "404 Page"
	}
	return "Front Page"
}

// wbPreviewURL is the front URL of a resolved preview; with a template id it carries
// ?wb_template_preview=<id>.
func (s *Server) wbPreviewURL(lb *links.Builder, q *siteQuery, templateID int64) string {
	var u string
	if q.Store != "" && q.Post != nil {
		u, _ = q.Post.Meta[wbPreviewURLMeta].(string)
	}
	switch {
	case u != "":
	case q.Kind == "single", q.Kind == "page", q.Kind == "front-page":
		if q.Kind == "front-page" {
			u = lb.HomeURL("/")
		} else {
			u = lb.Permalink(q.Post)
			if q.Post.Status != "publish" && q.Post.Status != "private" {
				u += previewSep(u) + "preview=true"
			}
		}
	case q.Kind == "home":
		if q.PostsPage != nil {
			u = lb.Permalink(q.PostsPage)
		} else {
			u = lb.HomeURL("/")
		}
	case q.Kind == "category", q.Kind == "tag", q.Kind == "taxonomy":
		u = lb.TermLink(q.Term)
	case q.Kind == "author":
		u = lb.AuthorLink(q.Author.ID, q.Author.Nicename)
	case q.Kind == "post-type-archive":
		u = lb.PostTypeArchiveLink(q.PostType)
	case q.Kind == "search":
		u = lb.SearchLink(q.Search)
	case q.Kind == "404":
		u = lb.HomeURL("/?p=0") // no post has id 0: the 404 view
	}
	if u == "" {
		u = lb.HomeURL("/")
	}
	if templateID > 0 {
		u += previewSep(u) + webbuilder.PreviewQuery + "=" + itoa64(templateID)
	}
	return u
}

// wbTemplatePreviewInfo is the TemplatePreview of t (its stored object or the type's default).
// A preview that names nothing falls back to the front page.
func (s *Server) wbTemplatePreviewInfo(c *gin.Context, t *store.WBTemplate) (webbuilder.TemplatePreview, *siteQuery) {
	obj := wbPreviewObjectOf(t)
	pv, _ := wbParsePreview(obj)
	q, err := s.wbResolvePreview(c, pv, t)
	if err != nil || q == nil {
		q, _ = s.home(c, &siteQuery{Preview: true})
	}
	lb := s.links(c)
	return webbuilder.TemplatePreview{Object: obj, Label: s.wbPreviewLabel(q), URL: s.wbPreviewURL(lb, q, t.ID)}, q
}

// --- DynContext ------------------------------------------------------------------------------------

// wbUGC is the policy of DynPost.contentHtml.
var wbUGC = sync.OnceValue(func() *bluemonday.Policy { return bluemonday.UGCPolicy() })

// wbStrict strips every tag (plain text fields).
var wbStrict = sync.OnceValue(func() *bluemonday.Policy { return bluemonday.StrictPolicy() })

// wbPlain is s as plain text: tags removed, entities decoded.
func wbPlain(s string) string {
	return strings.TrimSpace(html.UnescapeString(wbStrict().Sanitize(s)))
}

func wbTypeSingular(typ string) string {
	switch typ {
	case "post":
		return "Post"
	case "page":
		return "Page"
	}
	if pt := siteCPT(typ); pt != nil {
		if v := pt.Labels["singular_name"]; v != "" {
			return v
		}
		return pt.label("name")
	}
	return typ
}

func wbTypePlural(typ string) string {
	switch typ {
	case "post":
		return "Posts"
	case "page":
		return "Pages"
	}
	if pt := siteCPT(typ); pt != nil && pt.label("name") != "" {
		return pt.label("name")
	}
	return typ
}

// wbEmptyDyn is a DynContext with the contract's empty shape.
func wbEmptyDyn(lb *links.Builder) webbuilder.DynContext {
	return webbuilder.DynContext{Site: webbuilder.DynSite{Home: strings.TrimRight(lb.HomeURL("/"), "/")},
		Locations: []webbuilder.MenuLocation{}, Menus: map[string][]webbuilder.DynMenuItem{}, Posts: []webbuilder.DynPost{},
		Languages: []webbuilder.DynLanguage{}}
}

// wbDynContext builds the sample data of a resolved preview (object is echoed in the result).
func (s *Server) wbDynContext(c *gin.Context, object string, q *siteQuery) (webbuilder.DynContext, error) {
	lb := s.links(c)
	out := wbEmptyDyn(lb)
	out.Object = object
	v, err := s.buildSiteView(c, q)
	if err != nil {
		return out, err
	}
	ctx := v.ctx
	if ctx == nil {
		ctx = c.Request.Context()
	}
	out.Site = s.wbDynSite(ctx, v.Theme, lb)
	out.Site.Title, out.Site.Tagline = v.SiteName, v.Description
	for _, loc := range v.Theme.MenuLocations {
		out.Locations = append(out.Locations, webbuilder.MenuLocation{Slug: loc.Slug, Name: loc.Name})
		if items := v.Menus[loc.Slug]; len(items) > 0 {
			out.Menus[loc.Slug] = wbDynMenu(items)
		}
	}
	kind := webbuilder.DynViewArchive
	switch q.Kind {
	case "single":
		kind = webbuilder.DynViewSingle
	case "page":
		kind = webbuilder.DynViewPage
	case "front-page":
		kind = webbuilder.DynViewFrontPage
	case "home":
		kind = webbuilder.DynViewHome
	case "search":
		kind = webbuilder.DynViewSearch
	case "404":
		kind = webbuilder.DynView404
	}
	out.View = webbuilder.DynView{Kind: kind, Search: q.Search, Total: v.TotalPosts}
	if kind == webbuilder.DynViewArchive {
		_, plain := q.archiveTitle()
		out.View.ArchiveTitle = html.UnescapeString(plain)
		if q.Term != nil {
			out.View.ArchiveDesc = wbPlain(q.Term.Description)
		}
	}
	if v.Post != nil {
		dp := s.wbDynPost(ctx, v.Post, lb)
		out.Post = &dp
	}
	for i, pv := range v.Posts {
		if i >= webbuilder.MaxLoopPerPage {
			break
		}
		out.Posts = append(out.Posts, s.wbDynPost(ctx, pv, lb))
	}
	if len(out.Posts) == 0 && v.Post != nil && q.Post != nil {
		// A singular view lists no posts of its own: sample the recent entries so the editor can
		// draw loop cards (query and related loops) and the post navigation.
		for _, pv := range s.wbDynSamplePosts(ctx, c, q.Post, v.Post, lb) {
			out.Posts = append(out.Posts, s.wbDynPost(ctx, pv, lb))
		}
	}
	cur := q.Paged
	if cur < 1 {
		cur = 1
	}
	out.Pagination = webbuilder.DynPaging{Current: cur, Total: v.TotalPages}
	var preview *gooProduct
	if q.Store == webbuilder.StoreKindProduct && q.Post != nil && s.gooOn() {
		preview, _ = s.gooGetProduct(ctx, q.Post.ID)
	}
	out.Shop = s.wbDynShop(c, preview)
	out.Languages = s.wbDynLanguages(c, q)
	return out, nil
}

// wbDynLanguages are the language switcher's sample: the active languages with the URL of the
// preview object in each ([] while Pursae Multi Language is off).
func (s *Server) wbDynLanguages(c *gin.Context, q *siteQuery) []webbuilder.DynLanguage {
	out := []webbuilder.DynLanguage{}
	if !s.golylangOn() || len(s.pllLanguages(c.Request.Context())) == 0 {
		return out
	}
	a := pllDefaultArgs()
	a.HideIfEmpty = false
	for _, it := range s.pllSwitcherItems(c, q, a) {
		if it.Lang == nil {
			continue
		}
		out = append(out, webbuilder.DynLanguage{Slug: it.Lang.Slug, Name: it.Lang.Name, Flag: it.Lang.FlagEmoji(), URL: it.URL, Current: it.Current})
	}
	return out
}

// wbDynShop is the Pursae Commerce sample data (nil while it is off): the newest catalog
// products, the product categories, an empty cart and the store URLs; preview is the product a
// single-product template or a product's own doc is drawn for.
func (s *Server) wbDynShop(c *gin.Context, preview *gooProduct) *webbuilder.DynShop {
	if !s.gooOn() {
		return nil
	}
	ctx := c.Request.Context()
	out := &webbuilder.DynShop{Products: []webbuilder.DynProduct{}, Categories: []webbuilder.DynProductCat{},
		Cart:    webbuilder.DynCart{SubtotalHTML: string(s.gooPrice(0))},
		ShopURL: s.gooShopURL(ctx), CartURL: s.gooPageURL(ctx, "cart"), CheckoutURL: s.gooPageURL(ctx, "checkout")}
	list, _, err := s.gooQueryProducts(ctx, gooProductQuery{Visibility: "catalog", OrderBy: "date", Order: "DESC", Page: 1, PerPage: webbuilder.MaxShopLimit})
	if err != nil {
		log.Printf("web builder: shop sample: %v", err)
	}
	ids := make([]int64, 0, len(list)+1)
	for _, p := range list {
		ids = append(ids, p.ID)
	}
	if preview != nil {
		ids = append(ids, preview.ID)
	}
	cats := map[int64][]*store.Term{}
	if len(ids) > 0 {
		cats, _ = s.store.TermsForPosts(ctx, ids, "product_cat")
	}
	for _, p := range list {
		out.Products = append(out.Products, s.wbDynProduct(c, p, cats[p.ID], false))
	}
	if preview != nil {
		dp := s.wbDynProduct(c, preview, cats[preview.ID], true)
		out.Product = &dp
	}
	terms, _ := s.store.HierarchicalTerms(ctx, "product_cat", false)
	for i, t := range terms {
		if i >= 100 {
			break
		}
		out.Categories = append(out.Categories, webbuilder.DynProductCat{ID: t.ID, Name: t.Name, URL: s.gooTermLink(ctx, t), Count: t.Count, Parent: t.ParentID})
	}
	return out
}

// wbDynProduct is DynProduct of a product; full adds the rendered descriptions and the gallery.
func (s *Server) wbDynProduct(c *gin.Context, p *gooProduct, cats []*store.Term, full bool) webbuilder.DynProduct {
	ctx := c.Request.Context()
	dp := webbuilder.DynProduct{ID: p.ID, Type: p.Type, Title: p.Name, URL: s.gooPermalink(ctx, p), SKU: p.SKU,
		PriceHTML: s.gooL10n(c, string(s.gooPriceHTML(c, p))), OnSale: s.gooOnSale(c, p), InStock: p.StockStatus != "outofstock",
		StockHTML: s.gooL10n(c, s.gooFrontStockHTML(c, p)), Rating: p.AverageRating, RatingCount: p.RatingCount,
		Gallery: []webbuilder.DynImage{}, Categories: []int64{}, AddToCartText: s.gooL10nText(ctx, s.gooAddToCartText(c, p, true))}
	img := func(id int64) *webbuilder.DynImage {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return nil
		}
		alt := a.Alt
		if alt == "" {
			alt = p.Name
		}
		return &webbuilder.DynImage{URL: s.uploadURL(ctx, a.FilePath), Width: a.Width, Height: a.Height, Alt: alt}
	}
	if p.ImageID > 0 {
		dp.Image = img(p.ImageID)
	}
	for _, t := range cats {
		dp.Categories = append(dp.Categories, t.ID)
	}
	if full {
		for i, id := range p.GalleryIDs {
			if i >= 8 {
				break
			}
			if im := img(id); im != nil && id != p.ImageID {
				dp.Gallery = append(dp.Gallery, *im)
			}
		}
		if p.Post != nil {
			if ex := strings.TrimSpace(p.Post.Excerpt); ex != "" {
				dp.ShortHTML = strings.TrimSpace(wbUGC().Sanitize(s.gooRenderContent(ctx, c, ex)))
			}
			dp.ContentHTML = strings.TrimSpace(wbUGC().Sanitize(s.gooRenderContent(ctx, c, p.Post.Content)))
		}
	}
	return dp
}

// wbDynSamplePosts are the sample posts of a singular preview: the recent published entries of the
// current post's type, newest first, with the current post in its date position (so post.nav finds
// its neighbours), then the recent posts when the type is another one (loops of posts on a page).
func (s *Server) wbDynSamplePosts(ctx context.Context, c *gin.Context, cur *store.Post, curView *postView, lb *links.Builder) []*postView {
	u := currentUser(c)
	types := []string{cur.Type}
	if cur.Type != "post" {
		types = append(types, "post")
	}
	var out []*postView
	for _, typ := range types {
		if !siteViewableType(typ) {
			continue
		}
		posts, _, err := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{typ}, Statuses: []string{"publish"},
			OrderBy: "date", Order: "desc", Page: 1, PerPage: webbuilder.MaxLoopPerPage, Loc: s.loc()})
		if err != nil {
			log.Printf("web builder: preview sample posts: %v", err)
			continue
		}
		if typ == cur.Type {
			found := false
			for _, p := range posts {
				found = found || p.ID == cur.ID
			}
			if !found {
				at := len(posts)
				for i, p := range posts {
					if p.Date().Before(cur.Date()) {
						at = i
						break
					}
				}
				posts = append(posts[:at], append([]*store.Post{cur}, posts[at:]...)...)
			}
		}
		for _, p := range posts {
			if p.ID == cur.ID && curView != nil {
				out = append(out, curView)
				continue
			}
			out = append(out, s.postView(ctx, c, p, false, lb, u))
		}
	}
	return out
}

// wbDynSite is DynSite of the theme (title and tagline from the options; buildSiteView's values
// are language-swapped and override them when available).
func (s *Server) wbDynSite(ctx context.Context, th *Theme, lb *links.Builder) webbuilder.DynSite {
	site := webbuilder.DynSite{Title: s.settings.String("blogname"), Tagline: s.settings.String("blogdescription"),
		Home: strings.TrimRight(lb.HomeURL("/"), "/"), Year: strconv.Itoa(time.Now().In(s.loc()).Year())}
	if th == nil {
		return site
	}
	if id := int64(modInt(s.themeMods(th.Slug), "custom_logo", 0)); id > 0 && themeSupportsLogo(th) {
		if a, err := s.store.GetAttachment(ctx, id); err == nil {
			alt := a.Alt
			if alt == "" {
				alt = site.Title
			}
			site.Logo = &webbuilder.DynImage{URL: s.uploadURL(ctx, a.FilePath), Width: a.Width, Height: a.Height, Alt: alt}
		}
	}
	return site
}

// wbDynMenu is the top level of a menu (children are empty in v1).
func wbDynMenu(items []*store.MenuItem) []webbuilder.DynMenuItem {
	out := make([]webbuilder.DynMenuItem, 0, len(items))
	for _, it := range items {
		out = append(out, webbuilder.DynMenuItem{ID: it.ID, Label: wbPlain(it.Label), URL: it.URL, Current: it.Current, Children: []webbuilder.DynMenuItem{}})
	}
	return out
}

// wbDynPost is DynPost of a post view.
func (s *Server) wbDynPost(ctx context.Context, pv *postView, lb *links.Builder) webbuilder.DynPost {
	p := pv.Post
	dp := webbuilder.DynPost{ID: pv.ID, Type: pv.Type, Title: wbPlain(string(pv.Title)), URL: pv.Permalink,
		ContentHTML: strings.TrimSpace(wbUGC().Sanitize(string(pv.Content))),
		Date:        pv.DateISO, DateText: pv.DateText, Modified: pv.ModifiedISO, ModifiedText: pv.ModifiedText,
		Author:        webbuilder.DynAuthor{Name: pv.AuthorName, URL: pv.AuthorURL, Bio: pv.AuthorBio},
		Terms:         map[string][]webbuilder.DynTerm{},
		CommentsCount: pv.CommentCount, CommentsURL: pv.CommentsURL}
	if p == nil {
		return dp
	}
	if p.Password != "" {
		dp.Excerpt = "There is no excerpt because this is a protected post."
	} else if p.Excerpt != "" {
		dp.Excerpt = wbPlain(p.Excerpt)
	} else {
		text, _, _, _ := wp.SplitMore(stripBlocks(p.Content))
		dp.Excerpt = wbPlain(wp.TrimWords(wbStrict().Sanitize(text), 55, "…"))
	}
	if pv.AuthorEmail != "" {
		if a := s.avatarFor(ctx, pv.AuthorEmail, 96); a != defaultAvatar {
			dp.Author.Avatar = a
		}
	}
	if p.FeaturedMediaID > 0 {
		if a, err := s.store.GetAttachment(ctx, p.FeaturedMediaID); err == nil {
			dp.Image = &webbuilder.DynImage{URL: s.uploadURL(ctx, a.FilePath), Width: a.Width, Height: a.Height, Alt: a.Alt}
		}
	}
	for _, t := range pv.Categories {
		dp.Terms["category"] = append(dp.Terms["category"], webbuilder.DynTerm{Name: t.Name, URL: t.URL})
	}
	for _, t := range pv.Tags {
		dp.Terms["post_tag"] = append(dp.Terms["post_tag"], webbuilder.DynTerm{Name: t.Name, URL: t.URL})
	}
	return dp
}

// wbSiteBoot is Boot.Site: the site, the active theme's menus (from the boot's DynContext),
// whether Hello Pursae renders the parts, the viewable post types and the v1 taxonomies.
func (s *Server) wbSiteBoot(c *gin.Context, dyn *webbuilder.DynContext) webbuilder.SiteBoot {
	lb := s.links(c)
	th := s.activeTheme()
	site := s.wbDynSite(c.Request.Context(), th, lb)
	out := webbuilder.SiteBoot{Title: site.Title, Tagline: site.Tagline, Home: site.Home, Logo: site.Logo,
		Locations: []webbuilder.MenuLocation{}, Menus: map[string][]webbuilder.DynMenuItem{},
		ThemeParts: s.wbOn() && th != nil && th.Slug == webbuilder.HelloPursaeTheme,
		PostTypes:  []webbuilder.PostTypeInfo{}, Taxonomies: []webbuilder.TaxonomyInfo{}}
	if dyn != nil && len(dyn.Locations) > 0 {
		out.Locations, out.Menus = dyn.Locations, dyn.Menus
	} else if th != nil {
		for _, loc := range th.MenuLocations {
			out.Locations = append(out.Locations, webbuilder.MenuLocation{Slug: loc.Slug, Name: loc.Name})
		}
	}
	postsArchive := lb.HomeURL("/")
	if s.settings.String("show_on_front") == "page" {
		postsArchive = ""
		if id := int64(s.settings.Int("page_for_posts")); id > 0 {
			if p, err := s.store.GetPost(c, id); err == nil && p.Status == "publish" {
				postsArchive = lb.Permalink(p)
			}
		}
	}
	out.PostTypes = append(out.PostTypes,
		webbuilder.PostTypeInfo{Slug: "post", Label: "Posts", Archive: postsArchive},
		webbuilder.PostTypeInfo{Slug: "page", Label: "Pages", Hierarchical: true})
	cats := webbuilder.TaxonomyInfo{Slug: "category", Label: "Categories", Types: []string{"post"}}
	tags := webbuilder.TaxonomyInfo{Slug: "post_tag", Label: "Tags", Types: []string{"post"}}
	for _, pt := range siteRoutedTypes() {
		out.PostTypes = append(out.PostTypes, webbuilder.PostTypeInfo{Slug: pt.Name, Label: wbTypePlural(pt.Name),
			Archive: lb.PostTypeArchiveLink(pt.Name), Hierarchical: pt.Hierarchical})
		for _, tx := range pt.Taxonomies {
			switch tx {
			case "category":
				cats.Types = append(cats.Types, pt.Name)
			case "post_tag":
				tags.Types = append(tags.Types, pt.Name)
			}
		}
	}
	out.Taxonomies = append(out.Taxonomies, cats, tags)
	if s.gooOn() { // Pursae Commerce: the conditions dialog offers products and their taxonomies
		out.PostTypes = append(out.PostTypes, webbuilder.PostTypeInfo{Slug: "product", Label: "Products", Archive: s.gooShopURL(c.Request.Context())})
		out.Taxonomies = append(out.Taxonomies,
			webbuilder.TaxonomyInfo{Slug: "product_cat", Label: "Product categories", Types: []string{"product"}},
			webbuilder.TaxonomyInfo{Slug: "product_tag", Label: "Product tags", Types: []string{"product"}})
	}
	return out
}

// wbDynamicFor is Boot.Dynamic: the template's preview object, else the post itself; the empty
// shape when it cannot be built.
func (s *Server) wbDynamicFor(c *gin.Context, p *store.Post, t *store.WBTemplate) webbuilder.DynContext {
	lb := s.links(c)
	var q *siteQuery
	var object string
	var err error
	if t != nil {
		var info webbuilder.TemplatePreview
		info, q = s.wbTemplatePreviewInfo(c, t)
		object = info.Object
	} else {
		object = webbuilder.PreviewPost + itoa64(p.ID)
		if q, err = s.wbPreviewSingular(c, &siteQuery{Preview: true}, p); err != nil {
			out := wbEmptyDyn(lb)
			out.Object = object
			return out
		}
	}
	dyn, err := s.wbDynContext(c, object, q)
	if err != nil {
		out := wbEmptyDyn(lb)
		out.Object = object
		return out
	}
	return dyn
}
