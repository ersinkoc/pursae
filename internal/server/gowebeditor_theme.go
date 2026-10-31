package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Golementor theme building. Theme parts are Golementor documents of the content type gwe_template
// (meta _elementor_template_type = header, footer, single-post, single-page, archive,
// search-results or error-404) with display conditions in _gwe_conditions, upstream's condition
// strings ("include/general", "include/singular/page/12", "exclude/archive/category/3", ...).
// The site's own parts (_gwe_theme = "") are the working copy of the Hello Golementor theme
// (theme_hellogowebeditor.go); a saved theme's parts live in its snapshot
// (gowebeditor_theme_custom.go) and are only read while live-previewing it.
//
// When Hello Golementor is active (or a saved theme is previewed), or when one of the site's
// parts matches a page on another bundled theme, the page renders through the Hello Golementor
// templates (web/themes/hello-web-builder): wp_head, the header part, the single/archive/404 part
// (or the template's own minimal markup), the footer part and wp_footer.

const (
	gwePartType  = "gwe_template"
	ctxGweView   = "gwe.view"   // *siteView being built (Site widgets read the query from it)
	ctxGweKit    = "gwe.kit"    // *store.Post: kit override while live-previewing a saved theme
	ctxGweSample = "gwe.sample" // *gweSample: preview data for the editor's render of a theme part
	// gwePreviewIDBase is added to a snapshot part's id while a saved theme is live-previewed: the
	// snapshot keeps the ids its parts had when they were saved, and the rendered CSS
	// (uploads/elementor/css/post-<id>.css) must not overwrite the working copy's files.
	gwePreviewIDBase int64 = 1_000_000_000_000
)

// gwePartTypes are the theme part types in Theme Builder order.
var gwePartTypes = []struct{ Type, Label, Plural string }{
	{"header", "Header", "Headers"},
	{"footer", "Footer", "Footers"},
	{"single-post", "Single Post", "Single Posts"},
	{"single-page", "Single Page", "Single Pages"},
	{"archive", "Archive", "Archives"},
	{"search-results", "Search Results", "Search Results"},
	{"error-404", "404 Page", "404 Pages"},
	{"single-product", "Single Product", "Single Products"},      // Goocommerce (goo_gwe.go)
	{"product-archive", "Products Archive", "Products Archives"}, // Goocommerce
}

func gwePartLabel(typ string) string {
	for _, t := range gwePartTypes {
		if t.Type == typ {
			return t.Label
		}
	}
	return ""
}

// gweDefaultConditions are the conditions of a new part.
var gweDefaultConditions = map[string][]string{
	"header": {"include/general"}, "footer": {"include/general"},
	"single-post": {"include/singular/post"}, "single-page": {"include/singular/page"},
	"archive": {"include/archive"}, "search-results": {"include/archive/search"},
	"error-404":      {"include/singular/not_found404"},
	"single-product": {"include/product"}, "product-archive": {"include/product_archive"},
}

// gwePart is a theme part, from a post or from a custom theme's snapshot.
type gwePart struct {
	ID         int64          `json:"id"`
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Elements   string         `json:"elements"` // the element tree as JSON text (key order is significant)
	Settings   map[string]any `json:"settings"`
	Conditions []string       `json:"conditions"`
	Modified   time.Time      `json:"modified"`
}

func gweConditionsOf(p *store.Post) []string {
	var out []string
	switch v := p.Meta["_gwe_conditions"].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out
}

func gwePartTheme(p *store.Post) string {
	s, _ := p.Meta["_gwe_theme"].(string)
	return s
}

func gwePartFromPost(p *store.Post) *gwePart {
	return &gwePart{ID: p.ID, Type: gweDocType(p), Title: p.Title, Status: p.Status, Elements: string(gweElementsRaw(p)),
		Settings: gwePageSettings(p), Conditions: gweConditionsOf(p), Modified: p.ModifiedAt}
}

// gwePartPosts lists the part posts of a theme ("" = the site's own parts), oldest first.
func (s *Server) gwePartPosts(ctx context.Context, theme string, statuses ...string) ([]*store.Post, error) {
	if len(statuses) == 0 {
		statuses = []string{"publish", "draft", "pending", "private"}
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM posts WHERE type = $1 AND coalesce(meta->>'_gwe_theme','') = $2
		AND status = ANY($3) ORDER BY id`, gwePartType, theme, statuses)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}
	posts, _, err := s.store.QueryPosts(ctx, store.PostQuery{IDs: ids, Types: []string{gwePartType}, Statuses: statuses, PerPage: -1, OrderBy: "id", Order: "ASC"})
	return posts, err
}

// gweActiveParts are the published parts that apply with theme t: a saved theme's snapshot while
// it is previewed, or the site's own parts (the working copy) otherwise.
func (s *Server) gweActiveParts(ctx context.Context, t *Theme) []*gwePart {
	if t != nil && t.Custom {
		snap := gweSnapshotFor(t.Slug)
		if snap == nil {
			return nil
		}
		var out []*gwePart
		for _, p := range snap.Parts {
			if p.Status == "publish" {
				out = append(out, p)
			}
		}
		return out
	}
	posts, err := s.gwePartPosts(ctx, "", "publish")
	if err != nil {
		log.Printf("gowebeditor: theme parts: %v", err)
		return nil
	}
	out := make([]*gwePart, 0, len(posts))
	for _, p := range posts {
		out = append(out, gwePartFromPost(p))
	}
	return s.gooGweLangParts(ctx, out) // Golylang: the parts of the current language (goo_gwe.go)
}

// --- display conditions ------------------------------------------------------------------------

// gweCondScore returns how specifically a condition (without include/exclude) matches the query;
// 0 means no match.
func gweCondScore(rule string, q *siteQuery) int {
	parts := strings.Split(rule, "/")
	if sc, ok := gweStoreCondScore(parts, q); ok { // Goocommerce: products, archives, cart, checkout (goo_gwe.go)
		return sc
	}
	singular := q.Post != nil && (q.Kind == "single" || q.Kind == "page" || q.Kind == "front-page")
	archive := q.Kind == "home" || q.Kind == "category" || q.Kind == "tag" || q.Kind == "author" || q.Kind == "date" || q.Kind == "search"
	switch parts[0] {
	case "general":
		return 10
	case "singular":
		if len(parts) == 1 {
			if singular || q.Kind == "404" {
				return 20
			}
			return 0
		}
		switch parts[1] {
		case "front_page":
			if q.Kind == "front-page" {
				return 45
			}
		case "not_found404":
			if q.Kind == "404" {
				return 45
			}
		case "post", "page":
			if !singular || q.Post.Type != parts[1] {
				return 0
			}
			if len(parts) == 2 {
				return 30
			}
			if parts[2] == itoa64(q.Post.ID) {
				return 50
			}
		}
		return 0
	case "archive":
		if !archive {
			return 0
		}
		if len(parts) == 1 {
			return 20
		}
		kind := map[string]string{"post_archive": "home", "category": "category", "post_tag": "tag", "author": "author", "date": "date", "search": "search"}[parts[1]]
		if kind == "" || q.Kind != kind {
			return 0
		}
		if len(parts) == 2 {
			return 30
		}
		if q.Term != nil && parts[2] == itoa64(q.Term.ID) {
			return 50
		}
		if q.Author != nil && parts[2] == itoa64(q.Author.ID) {
			return 50
		}
	}
	return 0
}

// gweMatch applies a part's conditions: the best include score, or 0 when nothing includes the
// page or an exclude matches.
func gweMatch(conds []string, q *siteQuery) int {
	best := 0
	for _, c := range conds {
		mode, rule, _ := strings.Cut(c, "/")
		sc := gweCondScore(rule, q)
		if sc == 0 {
			continue
		}
		if mode == "exclude" {
			return 0
		}
		if mode == "include" && sc > best {
			best = sc
		}
	}
	return best
}

// gweLocationTypes are the part types that can fill a location for the query.
func gweLocationTypes(location string, q *siteQuery) []string {
	switch location {
	case "header", "footer":
		return []string{location}
	}
	switch { // Goocommerce views (goo_gwe.go)
	case q.Store == "product":
		return []string{"single-product", "single-post", "single-page"}
	case gooArchiveKind(q.Store):
		return []string{"product-archive", "single-page", "single-post"}
	}
	switch q.Kind {
	case "single", "page", "front-page":
		return []string{"single-post", "single-page"}
	case "404":
		return []string{"error-404"}
	case "home", "category", "tag", "author", "date":
		return []string{"archive"}
	case "search":
		return []string{"search-results", "archive"}
	}
	return nil
}

// gwePick chooses the part for a location: the most specific match, the newest on a tie.
func gwePick(parts []*gwePart, location string, q *siteQuery) *gwePart {
	types := gweLocationTypes(location, q)
	var best *gwePart
	bestScore := 0
	for _, p := range parts {
		ok := false
		for _, t := range types {
			if p.Type == t {
				ok = true
			}
		}
		if !ok {
			continue
		}
		sc := gweMatch(p.Conditions, q)
		if sc == 0 {
			continue
		}
		if p.Type == "search-results" {
			sc++ // a search template beats a generic archive template
		}
		if sc > bestScore || (sc == bestScore && best != nil && p.ID > best.ID) {
			best, bestScore = p, sc
		}
	}
	return best
}

// --- front end ---------------------------------------------------------------------------------

// gweThemeView is what the Hello Golementor templates print: the rendered parts (empty when
// no part matches, so the template shows its own markup) and the data of the minimal defaults.
type gweThemeView struct {
	Header, Main, Footer template.HTML
	HeaderPart, MainPart *gwePart
	FooterPart           *gwePart
	FooterMenu           template.HTML // the footer menu location, for the default footer
	Year                 int
	HideTitle            bool // the page's Golementor document hides its title
}

// gweThemeBegin runs when buildSiteView knows the theme: the Site widgets find the view through
// the request context, and a live-previewed saved theme brings its own kit.
func (s *Server) gweThemeBegin(c *gin.Context, v *siteView) {
	if c == nil || !s.golementorOn() {
		return
	}
	c.Set(ctxGweView, v)
	if v.Theme != nil && v.Theme.Custom {
		if snap := gweSnapshotFor(v.Theme.Slug); snap != nil && snap.KitID > 0 {
			if kit, err := s.store.GetPost(c, snap.KitID); err == nil && kit.Status != "trash" {
				c.Set(ctxGweKit, kit)
			}
		}
	}
}

// gweThemePrepare decides whether the page renders through the Hello Golementor templates and
// renders the matching parts; it runs in buildSiteView before wp_head is printed (so the parts'
// styles get enqueued). Hello Golementor itself (and a previewed saved theme) always renders
// this way; other bundled themes only when one of the site's parts matches the page.
func (s *Server) gweThemePrepare(c *gin.Context, v *siteView, bodyClasses *[]string) {
	q := v.Q
	if c == nil || q == nil || q.Feed != "" || !s.golementorOn() {
		return
	}
	hello := v.Theme.Slug == helloThemeSlug || v.Theme.Custom || gweDesignTheme(v.Theme) // + Golementor design packages (goo_gwe.go)
	q.Store = s.gooStoreKind(c, q)
	if fv := gooViewFrom(c); fv != nil && fv.Term != nil {
		q.StoreTerm = fv.Term.ID
	}
	var parts []*gwePart
	if q.Post != nil && (q.Post.Type == gwePartType || q.Post.Type == gweLibrary) {
		if !hello {
			return // a template previewed on its own (canvas)
		}
	} else {
		parts = s.gweActiveParts(c, v.Theme)
	}
	header, footer := gwePick(parts, "header", q), gwePick(parts, "footer", q)
	main := gwePick(parts, "main", q)
	if !hello && header == nil && footer == nil && main == nil {
		return
	}
	gv := &gweThemeView{HeaderPart: header, MainPart: main, FooterPart: footer, Year: time.Now().In(s.loc()).Year()}
	render := func(part *gwePart, location string) template.HTML {
		if v.Theme.Custom { // a previewed snapshot: render under ids of its own
			p := *part
			p.ID += gwePreviewIDBase
			part = &p
		}
		return s.gweRenderPart(c, part, location)
	}
	if header != nil {
		gv.Header = render(header, "header")
	}
	if main != nil {
		gv.Main = s.gooGweWrapPart(c, main.Type, render(main, main.Type)) // a Single Product part sits in div.product
	}
	if footer != nil {
		gv.Footer = render(footer, "footer")
	}
	if items := v.Menus["footer"]; len(items) > 0 {
		gv.FooterMenu = renderMenu(items, "menu-footer-container", "menu", "", "")
	} else if items, err := s.store.MenuAt(c, "footer"); err == nil && len(items) > 0 {
		s.resolveMenu(c, items, s.links(c), q)
		gv.FooterMenu = renderMenu(items, "menu-footer-container", "menu", "", "")
	}
	if q.Post != nil && gweBuilt(q.Post) {
		gv.HideTitle = gwePageSettings(q.Post)["hide_title"] == "yes"
	}
	v.GWE = gv
	extra := []string{"gwe-theme"}
	if kit, err := s.gweKit(c); err == nil {
		extra = append(extra, "elementor-default", "elementor-kit-"+itoa64(kit.ID))
	}
	seen := map[string]bool{}
	for _, cl := range *bodyClasses {
		seen[cl] = true
	}
	for _, cl := range extra {
		if !seen[cl] {
			*bodyClasses = append(*bodyClasses, cl)
			seen[cl] = true
		}
	}
	if len(gweResults(c)) == 0 {
		// No builder document on the page: still load the frontend and kit styles.
		p := q.Post
		if p == nil {
			p = &store.Post{ID: 0, Title: v.SiteName}
		}
		s.gweRecord(c, p, &builder.Result{})
	}
}

// gweRenderPart renders a part as <div data-elementor-type="header" ... elementor-location-header>.
func (s *Server) gweRenderPart(c *gin.Context, part *gwePart, location string) template.HTML {
	els, err := builder.ParseElements([]byte(part.Elements))
	if err != nil {
		log.Printf("gowebeditor: part %d: %v", part.ID, err)
	}
	doc := &builder.Document{ID: part.ID, Type: part.Type, Elements: els, Settings: part.Settings}
	res, err := builder.Render(doc, s.gweRenderContext(c, false))
	if err != nil || res == nil {
		log.Printf("gowebeditor: render part %d: %v", part.ID, err)
		return ""
	}
	res.HTML, res.CSS = s.gweFixURLs(c, res.HTML), s.gweFixURLs(c, res.CSS)
	fake := &store.Post{ID: part.ID, Type: gwePartType, Title: part.Title, ModifiedAt: part.Modified}
	s.gweWriteCSS(fake, res.CSS)
	s.gweRecord(c, fake, res)
	out := strings.Replace(res.HTML, `class="elementor elementor-`+itoa64(part.ID)+`"`,
		`class="elementor elementor-`+itoa64(part.ID)+` elementor-location-`+location+`" data-elementor-post-type="`+gwePartType+`"`, 1)
	return template.HTML(out)
}

// gweRenderShell renders the page through the Hello Golementor templates when theme parts (or
// a previewed saved theme) take over the layout.
func (s *Server) gweRenderShell(c *gin.Context, v *siteView, status int) bool {
	if v == nil || v.GWE == nil || !s.golementorOn() {
		return false
	}
	s.renderTemplate(c, status, s.tmpl.gweShell(), "theme/"+v.Template, v)
	return true
}

// --- Site widget data --------------------------------------------------------------------------

// gweSample is the content the editor previews a theme part with.
type gweSample struct {
	post    *postView
	archive *builder.ArchiveData
}

// gweThemeSample sets the editor preview data for a theme part document.
func (s *Server) gweThemeSample(c *gin.Context, p *store.Post) {
	if c == nil || p == nil || p.Type != gwePartType {
		return
	}
	if _, ok := c.Get(ctxGweSample); ok {
		return
	}
	smp := &gweSample{}
	lb := s.links(c)
	u := currentUser(c)
	typ := gweDocType(p)
	switch typ {
	case "single-post", "single-page", "header", "footer":
		want := "post"
		if typ == "single-page" {
			want = "page"
		}
		if posts, _, err := s.store.QueryPosts(c, store.PostQuery{Types: []string{want}, Statuses: []string{"publish"}, PerPage: 1, Page: 1,
			OrderBy: "date", Order: "DESC"}); err == nil && len(posts) > 0 {
			smp.post = s.postView(c, c, posts[0], true, lb, u)
		}
	case "archive", "search-results":
		posts, total, err := s.store.QueryPosts(c, store.PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, PerPage: 6, Page: 1,
			OrderBy: "date", Order: "DESC"})
		if err == nil {
			a := &builder.ArchiveData{Title: "Archives", PlainTitle: "Archives", Page: 1, Pages: (total + 5) / 6, Search: typ == "search-results"}
			if typ == "search-results" {
				a.Title, a.PlainTitle = "Search results for: <span>example</span>", "example"
			}
			for _, p := range posts {
				a.Posts = append(a.Posts, s.gwePostData(s.postView(c, c, p, false, lb, u)))
			}
			a.PageURL = func(n int) string { return lb.Paged(lb.HomeURL("/"), n) }
			smp.archive = a
		}
	}
	c.Set(ctxGweSample, smp)
}

func (s *Server) gwePostData(pv *postView) *builder.PostData {
	if pv == nil {
		return nil
	}
	pd := &builder.PostData{ID: pv.ID, Type: pv.Type, Title: string(pv.Title), URL: pv.Permalink, Date: pv.DateText, DateISO: pv.DateISO,
		AuthorName: pv.AuthorName, AuthorURL: pv.AuthorURL, CommentCount: pv.CommentCount, CommentsURL: pv.CommentsURL, Class: pv.Class,
		ImageURL: pv.ThumbnailURL}
	if !pv.Date.IsZero() {
		pd.Time = wp.FormatDate(pv.Date, s.settings.String("time_format"))
	}
	if pv.Post != nil {
		pd.ImageID = pv.Post.FeaturedMediaID
		if pv.Post.Password != "" {
			pd.Excerpt = "There is no excerpt because this is a protected post."
		} else if pv.Post.Excerpt != "" {
			pd.Excerpt = wp.StripTags(pv.Post.Excerpt)
		} else {
			text, _, _, _ := wp.SplitMore(stripBlocks(pv.Post.Content))
			pd.Excerpt = strings.Join(strings.Fields(html.UnescapeString(wp.StripTags(text))), " ")
		}
	}
	for _, t := range pv.Categories {
		pd.Categories = append(pd.Categories, builder.TermLink{Name: t.Name, URL: t.URL})
	}
	for _, t := range pv.Tags {
		pd.Tags = append(pd.Tags, builder.TermLink{Name: t.Name, URL: t.URL})
	}
	return pd
}

func gweMenuItems(items []*store.MenuItem) []*builder.MenuItem {
	out := make([]*builder.MenuItem, 0, len(items))
	for _, it := range items {
		out = append(out, &builder.MenuItem{ID: it.ID, Label: it.Label, URL: it.URL, Target: it.Target, Classes: it.Classes,
			Kind: it.Kind, Object: it.Object, Current: it.Current, CurrentAncestor: it.CurrentAncestor, Children: gweMenuItems(it.Children)})
	}
	return out
}

// gweMenuByID is MenuAt for a menu id.
func (s *Server) gweMenuByID(ctx context.Context, id int64) []*store.MenuItem {
	var loc string
	if err := s.store.Pool.QueryRow(ctx, `SELECT coalesce(location,'') FROM menus WHERE id = $1`, id).Scan(&loc); err != nil {
		return nil
	}
	if loc != "" {
		items, _ := s.store.MenuAt(ctx, loc)
		return items
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT i.id, coalesce(i.parent_id,0), i.kind, i.object, coalesce(i.target_id,0),
		i.url, i.label, i.target, i.css_classes FROM menu_items i WHERE i.menu_id = $1 AND i.status = 'publish' ORDER BY i.sort_order, i.id`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var flat []*store.MenuItem
	for rows.Next() {
		it := &store.MenuItem{}
		if rows.Scan(&it.ID, &it.ParentID, &it.Kind, &it.Object, &it.TargetID, &it.URL, &it.Label, &it.Target, &it.Classes) == nil {
			flat = append(flat, it)
		}
	}
	byID := map[int64]*store.MenuItem{}
	for _, it := range flat {
		byID[it.ID] = it
	}
	var roots []*store.MenuItem
	for _, it := range flat {
		if p, ok := byID[it.ParentID]; ok {
			p.Children = append(p.Children, it)
		} else {
			roots = append(roots, it)
		}
	}
	return roots
}

// gweThemeData is RenderContext.Theme for this request: the site, plus the query of the page being
// built (or the editor's sample content for a theme part).
func (s *Server) gweThemeData(ctx context.Context) *builder.ThemeData {
	lb := s.links(ctx)
	td := &builder.ThemeData{SiteName: s.settings.String("blogname"), Tagline: s.settings.String("blogdescription"),
		HomeURL: lb.HomeURL("/"), SearchURL: lb.HomeURL("/"), Now: time.Now().In(s.loc())}
	if s.gooOn() || s.golylangOn() {
		td.Store = s.gooGweStore(ctx) // the store widgets and the language switcher (goo_gwe.go)
	}
	v, _ := ctx.Value(ctxGweView).(*siteView)
	smp, _ := ctx.Value(ctxGweSample).(*gweSample)
	if v != nil {
		td.SearchQuery = v.SearchQuery
	}
	// Logo: Site Settings › Site Identity, else the theme's custom logo.
	if _, kit := s.gweKitSettings(ctx); kit != nil {
		if m, ok := kit["site_logo"].(map[string]any); ok {
			td.LogoID = gweInt(m["id"])
			td.LogoURL, _ = m["url"].(string)
		}
	}
	if td.LogoID == 0 && td.LogoURL == "" {
		mods := map[string]any{}
		if v != nil && v.ThemeMods != nil {
			mods = v.ThemeMods
		} else {
			mods = s.themeMods(s.activeTheme().Slug)
		}
		td.LogoID = int64(modInt(mods, "custom_logo", 0))
	}
	q := &siteQuery{}
	if v != nil && v.Q != nil {
		q = v.Q
	}
	td.Menu = func(location string, id int64) []*builder.MenuItem {
		var items []*store.MenuItem
		if id > 0 {
			items = s.gweMenuByID(ctx, id)
		} else if location != "" {
			items, _ = s.store.MenuAt(ctx, location)
		}
		if len(items) == 0 {
			return nil
		}
		s.resolveMenu(ctx, items, lb, q)
		return gweMenuItems(items)
	}
	var postOnce sync.Once
	var post *builder.PostData
	td.Post = func() *builder.PostData {
		postOnce.Do(func() {
			switch {
			case v != nil && v.Post != nil && (v.Template == "single" || v.Template == "page"):
				post = s.gwePostData(v.Post)
			case v != nil && v.Post == nil && v.Q != nil && v.Q.Post != nil && (v.Template == "single" || v.Template == "page"): // gap-loops: still rendering the_content
				post = s.gweProPostData(ctx, v.Q.Post, lb)
			case smp != nil && smp.post != nil:
				post = s.gwePostData(smp.post)
			}
		})
		return post
	}
	td.PostContent = func() string {
		switch {
		case v != nil && v.Post != nil:
			return string(v.Post.Content)
		case smp != nil && smp.post != nil:
			return string(smp.post.Content)
		}
		return ""
	}
	td.Archive = func() *builder.ArchiveData {
		if v != nil && v.Q != nil {
			switch v.Template {
			case "index", "archive", "search":
				a := &builder.ArchiveData{Page: max(v.Q.Paged, 1), Pages: v.TotalPages, Search: v.Template == "search"}
				a.Home, a.HomeTitle = v.Q.Kind == "home", s.documentTitle(v.Q, "") // gap-loops2: Page Title on the blog home
				switch v.Template {
				case "archive":
					a.Title = string(v.ArchiveTitle)
					_, a.PlainTitle = v.Q.archiveTitle()
					a.Description = string(v.ArchiveDesc)
				case "search":
					a.Title = "Search results for: <span>" + html.EscapeString(v.SearchQuery) + "</span>"
					a.PlainTitle = html.EscapeString(v.SearchQuery)
				default:
					a.Title = "Blog"
					if v.PostsPageTitle != "" {
						a.Title = html.EscapeString(v.PostsPageTitle)
					}
					a.PlainTitle = a.Title
				}
				for _, p := range v.Posts {
					a.Posts = append(a.Posts, s.gwePostData(p))
				}
				base := s.archiveBase(v.Q, lb)
				a.PageURL = func(n int) string { return lb.Paged(base, n) }
				return a
			}
			return nil
		}
		if smp != nil {
			return smp.archive
		}
		return nil
	}
	return td
}

// --- editor integration ------------------------------------------------------------------------

var gweThemeWidgetsCache sync.Map // home URL -> widgets config with the Site widgets

// gweThemeWidgetsConfig adds the Site widgets' controls to the widgets config (get_widgets_config)
// and fills the menu choices of the Nav Menu widget.
func (s *Server) gweThemeWidgetsConfig(c *gin.Context, base []byte) []byte {
	home := s.links(c).Home
	var merged []byte
	if v, ok := gweThemeWidgetsCache.Load(home); ok {
		merged = v.([]byte)
	} else {
		obj, err := gweOrderedRaw(base)
		if err != nil {
			return base
		}
		for _, w := range builder.ThemeWidgets() {
			entry := map[string]json.RawMessage{"controls": w.ControlsJSON(),
				"tabs_controls": json.RawMessage(`{"content":"Content","style":"Style","advanced":"Advanced"}`)}
			raw, _ := json.Marshal(entry)
			obj = obj.set(w.Name, raw)
		}
		merged, _ = json.Marshal(obj)
		merged = []byte(s.gweFill(c, string(merged)))
		gweThemeWidgetsCache.Store(home, merged)
	}
	return s.gweMenuChoices(c, merged)
}

// gweMenuChoices fills the Nav Menu widget's location and menu choices (they change at runtime, so
// the cached configs carry the static defaults and are patched per request).
func (s *Server) gweMenuChoices(c *gin.Context, cfg []byte) []byte {
	locs := builder.Ordered{}
	seen := map[string]bool{}
	for _, l := range append(append([]MenuLocation{}, s.activeTheme().MenuLocations...), gweMenuLocations...) {
		if !seen[l.Slug] {
			seen[l.Slug] = true
			locs = append(locs, builder.KV{K: l.Slug, V: l.Name})
		}
	}
	menus := builder.Ordered{{K: "", V: "— Select —"}}
	if list, err := s.store.Menus(c); err == nil {
		for _, m := range list {
			menus = append(menus, builder.KV{K: itoa64(m.ID), V: m.Name})
		}
	}
	lj, _ := json.Marshal(locs)
	mj, _ := json.Marshal(menus)
	out := strings.Replace(string(cfg), `"options":{"primary":"Primary Menu","footer":"Footer Menu"}`, `"options":`+string(lj), 1)
	out = strings.Replace(out, `"options":{"":"— Select —"}`, `"options":`+string(mj), 1)
	return []byte(out)
}

// gweRawObject is a JSON object decoded with its key order.
type gweRawObject []struct {
	K string
	V json.RawMessage
}

func gweOrderedRaw(raw []byte) (gweRawObject, error) {
	var out gweRawObject
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not an object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, struct {
			K string
			V json.RawMessage
		}{kt.(string), v})
	}
	return out, nil
}

func (o gweRawObject) set(k string, v json.RawMessage) gweRawObject {
	for i := range o {
		if o[i].K == k {
			o[i].V = v
			return o
		}
	}
	return append(o, struct {
		K string
		V json.RawMessage
	}{k, v})
}

// insertAfter puts k after the key after (or at the end).
func (o gweRawObject) insertAfter(after, k string, v json.RawMessage) gweRawObject {
	for i := range o {
		if o[i].K == k {
			return o
		}
	}
	item := struct {
		K string
		V json.RawMessage
	}{k, v}
	for i := range o {
		if o[i].K == after {
			out := append(gweRawObject{}, o[:i+1]...)
			out = append(out, item)
			return append(out, o[i+1:]...)
		}
	}
	return append(o, item)
}

func (o gweRawObject) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, kv := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv.K)
		b.Write(k)
		b.WriteByte(':')
		b.Write(kv.V)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

var (
	gweDocWidgetsOnce sync.Once
	gweDocWidgetsJSON json.RawMessage
)

// gweDocWidgets is the document "widgets" list with the Site widgets shown in the panel. The Site
// widgets carry their controls here (core widgets get theirs from get_widgets_config later): the
// editor builds the saved elements' settings models when the document opens, and a widget known
// without controls at that point never gets its defaults or the common (Advanced) controls.
func gweDocWidgets() json.RawMessage {
	gweDocWidgetsOnce.Do(func() {
		base := gweData().docWidgets
		obj, err := gweOrderedRaw(base)
		if err != nil {
			gweDocWidgetsJSON = base
			return
		}
		for _, w := range builder.ThemeWidgets() {
			entry := map[string]any{"name": w.Name, "elType": "widget", "title": w.Title, "icon": w.Icon, "reload_preview": false,
				"help_url": "#", "widget_type": w.Name, "keywords": w.Keywords, "categories": w.Categories,
				"html_wrapper_class": w.WrapperClass, "show_in_panel": true, "hide_on_search": false, "upsale_data": nil,
				"is_dynamic_content": true, "has_widget_inner_wrapper": true, "controls": w.ControlsJSON(),
				"tabs_controls": json.RawMessage(`{"content":"Content","style":"Style","advanced":"Advanced"}`)}
			raw, _ := json.Marshal(entry)
			obj = obj.set(w.Name, raw)
		}
		gweDocWidgetsJSON, _ = json.Marshal(obj)
	})
	return gweDocWidgetsJSON
}

// gweThemeDocConfig adjusts a document config for theme building: every document gets the Site
// widgets; theme parts get their panel title, the Single category and links back to the Theme
// Builder.
func (s *Server) gweThemeDocConfig(c *gin.Context, p *store.Post, out map[string]any) {
	typ := gweDocType(p)
	if typ == "kit" {
		return
	}
	out["widgets"] = json.RawMessage(s.gweMenuChoices(c, []byte(s.gweFill(c, string(gweDocWidgets())))))
	label := gwePartLabel(typ)
	var panel map[string]json.RawMessage
	if raw, ok := out["panel"].(json.RawMessage); ok && json.Unmarshal(raw, &panel) == nil {
		cats, err := gweOrderedRaw(panel["elements_categories"])
		if err == nil {
			cats = cats.insertAfter("layout", "theme-elements", json.RawMessage(`{"title":"Site","icon":"eicon-site-identity","active":`+fmt.Sprint(label != "")+`}`))
			if strings.HasPrefix(typ, "single-") {
				cats = cats.insertAfter("theme-elements", "theme-elements-single", json.RawMessage(`{"title":"Single","icon":"eicon-single-post","active":true}`))
			}
			if s.gooOn() { // Goocommerce: the store widgets (goo_gwe.go)
				after := "theme-elements"
				if typ == "single-product" {
					cats = cats.insertAfter("theme-elements-single", "woocommerce-elements-single", json.RawMessage(`{"title":"Store Single","icon":"eicon-product-images","active":true}`))
					after = "woocommerce-elements-single"
				}
				cats = cats.insertAfter(after, "woocommerce-elements", json.RawMessage(`{"title":"Store","icon":"eicon-woocommerce","active":`+fmt.Sprint(typ == "single-product" || typ == "product-archive")+`}`))
			}
			panel["elements_categories"], _ = json.Marshal(cats)
		}
		if label != "" {
			panel["title"], _ = json.Marshal(label)
			panel["messages"], _ = json.Marshal(map[string]string{"publish_notification": "Hurray! Your " + label + " is live."})
		}
		b, _ := json.Marshal(panel)
		out["panel"] = json.RawMessage(b)
	}
	if label == "" {
		return
	}
	out["post_type_title"] = "Theme Part"
	lb := s.links(c)
	if urls, ok := out["urls"].(map[string]string); ok {
		urls["exit_to_dashboard"] = lb.HomeURL(gweThemeBuilderPath)
		urls["all_post_type"] = lb.HomeURL(gweThemeBuilderPath)
		urls["have_a_look"] = lb.HomeURL("/")
	}
	if rl, ok := out["remoteLibrary"].(json.RawMessage); ok {
		var m map[string]any
		if json.Unmarshal(rl, &m) == nil {
			m["category"] = ""
			b, _ := json.Marshal(m)
			out["remoteLibrary"] = json.RawMessage(b)
		}
	}
}

// gweAfterPartSave runs after save_builder: a part or kit change invalidates the page cache. The
// working copy is edited in place; saved themes only change when they are saved again.
func (s *Server) gweAfterPartSave(c *gin.Context, p *store.Post) {
	if p.Type != gwePartType && !(p.Type == gweLibrary && gweDocType(p) == "kit") {
		return
	}
	s.purgeAll(c)
}

// gweEditorThemeScript is the editor's theme-building script: the Site widgets' common controls,
// and for theme parts the "Theme Builder" / "Save as theme" buttons.
func (s *Server) gweEditorThemeScript(c *gin.Context, p *store.Post) string {
	var names []string
	for _, w := range builder.ThemeWidgets() {
		names = append(names, w.Name)
	}
	nj, _ := json.Marshal(names)
	// The Site widgets arrive with the document (their controls included), before the common
	// (Advanced tab) controls; the editor merges common controls only once per widget, so merge
	// them here whenever the widgets cache is updated.
	common := `<script id="gowebeditor-site-widgets">
( function() {
	if ( ! window.elementor || ! elementor.addWidgetsCache ) { return; }
	var names = ` + string(nj) + `, add = elementor.addWidgetsCache;
	function merge() {
		var cache = elementor.widgetsCache || {}, common = cache.common && cache.common.controls;
		if ( ! common ) { return; }
		names.forEach( function( n ) {
			var w = cache[ n ];
			if ( w && w.controls && ! w.gweCommon ) { jQuery.extend( w.controls, common ); w.gweCommon = true; w.commonMerged = true; }
		} );
	}
	elementor.addWidgetsCache = function() { var r = add.apply( this, arguments ); merge(); return r; };
} )();
</script>
`
	if p.Type != gwePartType {
		return common
	}
	lb := s.links(c)
	builderURL := strconvQuote(lb.HomeURL(gweThemeBuilderPath))
	saveURL := strconvQuote(lb.HomeURL(gweThemeBuilderPath + "?action=save-theme"))
	return common + `<script id="gowebeditor-theme-bar">
( function() {
	function mount() {
		var bar = document.querySelector( '#elementor-editor-wrapper-v2 header .MuiToolbar-root, #elementor-panel-header' );
		if ( ! bar || document.getElementById( 'gwe-theme-bar' ) ) { return !! bar; }
		var box = document.createElement( 'div' );
		box.id = 'gwe-theme-bar';
		box.style.cssText = 'position:fixed;right:16px;bottom:16px;z-index:99999;display:flex;gap:8px;font:13px/1.2 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif';
		var a = document.createElement( 'a' );
		a.href = ` + builderURL + `;
		a.textContent = 'Theme Builder';
		a.style.cssText = 'padding:9px 14px;border-radius:4px;background:#fff;color:#1d2327;text-decoration:none;box-shadow:0 2px 8px rgba(0,0,0,.2)';
		var b = document.createElement( 'a' );
		b.href = ` + saveURL + `;
		b.textContent = 'Save as theme';
		b.style.cssText = 'padding:9px 14px;border-radius:4px;background:#2271b1;color:#fff;text-decoration:none;box-shadow:0 2px 8px rgba(0,0,0,.2)';
		b.addEventListener( 'click', function( e ) {
			if ( window.elementor && elementor.saver && elementor.saver.isEditorChanged && elementor.saver.isEditorChanged() ) {
				e.preventDefault();
				$e.run( 'document/save/publish' ).then( function() { location.href = b.href; } );
			}
		} );
		box.appendChild( a );
		box.appendChild( b );
		document.body.appendChild( box );
		return true;
	}
	var tries = 0;
	var t = setInterval( function() { if ( mount() || ++tries > 120 ) { clearInterval( t ); } }, 500 );
} )();
</script>
`
}

// gweSortParts orders parts for the Theme Builder list: by type order, then title.
func gweSortParts(list []*store.Post) {
	order := map[string]int{}
	for i, t := range gwePartTypes {
		order[t.Type] = i
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := order[gweDocType(list[i])], order[gweDocType(list[j])]
		if a != b {
			return a < b
		}
		return strings.ToLower(list[i].Title) < strings.ToLower(list[j].Title)
	})
}
