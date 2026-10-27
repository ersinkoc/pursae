package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// GoWebEditor Pro: the site data behind dynamic tags, the posts/loop queries and the Pro theme
// elements (RenderContext.Pro), plus the editor's render_tags action.

// gweProData builds builder.ProData for a request.
func (s *Server) gweProData(ctx context.Context) *builder.ProData {
	lb := s.links(ctx)
	c, _ := ctx.(*gin.Context)
	pd := &builder.ProData{DateFormat: s.settings.String("date_format"), TimeFormat: s.settings.String("time_format"),
		Now: time.Now().In(s.loc()), AdminAjaxURL: lb.HomeURL("/wp-admin/admin-ajax"), RestURL: lb.HomeURL("/wp-json/")}
	if pd.DateFormat == "" {
		pd.DateFormat = "F j, Y"
	}
	if pd.TimeFormat == "" {
		pd.TimeFormat = "g:i a"
	}
	if c != nil {
		pd.User = s.gweProUserInfo(ctx, currentUser(c))
		pd.CurrentURL = lb.HomeURL(c.Request.URL.RequestURI())
		pd.Request = func(kind, name string) string {
			switch kind {
			case "post":
				return c.PostForm(name)
			case "server":
				return c.GetHeader(strings.ReplaceAll(strings.TrimPrefix(name, "HTTP_"), "_", "-"))
			}
			return c.Query(name)
		}
		pd.IsEditor = gwePreviewing(c) || strings.Contains(c.Request.URL.Path, "admin-ajax")
	}
	v, _ := ctx.Value(ctxGweView).(*siteView)
	if v != nil && v.Q != nil {
		if v.Q.Term != nil {
			pd.ArchiveTermID = v.Q.Term.ID
		}
		if v.Q.Author != nil {
			pd.ArchiveAuthorID = v.Q.Author.ID
		}
	}
	pd.UserByID = func(id int64) *builder.UserInfo {
		u, err := s.store.UserByID(ctx, id)
		if err != nil {
			return nil
		}
		return s.gweProUserInfo(ctx, u)
	}
	pd.AuthorOf = func(postID int64) *builder.UserInfo {
		p, err := s.store.GetPost(ctx, postID)
		if err != nil {
			return nil
		}
		return pd.UserByID(p.AuthorID)
	}
	pd.PostMeta = func(postID int64, key string) any {
		p, err := s.store.GetPost(ctx, postID)
		if err != nil {
			return nil
		}
		switch key {
		case "_modified_iso":
			return p.ModifiedAt.In(s.loc()).Format(time.RFC3339)
		case "_parent_id":
			return p.ParentID
		case "_gwe_raw_excerpt": // the manual excerpt only (posts widget trims the content otherwise)
			return p.Excerpt
		case "_gwe_the_content": // full-content skin for posts not built with GoWebEditor
			if c == nil {
				return ""
			}
			return s.theContent(ctx, c, p, false, lb)
		case "_gallery_ids":
			rows, err := s.store.Pool.Query(ctx, `SELECT id FROM media WHERE post_id = $1 AND deleted_at IS NULL AND mime_type LIKE 'image/%' ORDER BY id`, postID)
			if err != nil {
				return []any{}
			}
			defer rows.Close()
			out := []any{}
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					out = append(out, map[string]any{"id": id})
				}
			}
			return out
		}
		return p.Meta[key]
	}
	pd.PostTerms = func(postID int64, taxonomy string) []builder.TermLink {
		m, _ := s.store.TermsForPosts(ctx, []int64{postID}, taxonomy)
		var out []builder.TermLink
		for _, t := range m[postID] {
			out = append(out, builder.TermLink{Name: html.EscapeString(t.Name), URL: lb.TermLink(t)})
		}
		return out
	}
	pd.PostTermIDs = func(postID int64, taxonomy string) []int64 {
		m, _ := s.store.TermsForPosts(ctx, []int64{postID}, taxonomy)
		var out []int64
		for _, t := range m[postID] {
			out = append(out, t.ID)
		}
		return out
	}
	pd.PostByID = func(id int64) *builder.PostData {
		p, err := s.store.GetPost(ctx, id)
		if err != nil {
			return nil
		}
		return s.gweProPostData(ctx, p, lb)
	}
	pd.TermURL = func(id int64) string {
		t, err := s.store.GetTerm(ctx, id)
		if err != nil {
			return ""
		}
		return lb.TermLink(t)
	}
	pd.TermByID = func(id int64) *builder.TermItem {
		t, err := s.store.GetTerm(ctx, id)
		if err != nil {
			return nil
		}
		return s.gweProTermItem(t, lb)
	}
	pd.AttachmentURL = func(id int64) string {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return ""
		}
		return s.uploadURL(ctx, a.FilePath)
	}
	pd.Query = func(q *builder.PostQuery) *builder.PostList { return s.gweLoopQuery(ctx, q, v, lb) } // gap-loops: gowebeditor_loop.go
	pd.Loop = s.gweLoopState(ctx)                                                                    // gap-loops
	pd.Terms = func(taxonomy string, args map[string]any) []*builder.TermItem {
		hideEmpty, _ := args["hide_empty"].(bool)
		terms, err := s.store.HierarchicalTerms(ctx, taxonomy, hideEmpty)
		if err != nil {
			return nil
		}
		var out []*builder.TermItem
		for _, t := range terms {
			out = append(out, s.gweProTermItem(t, lb))
		}
		return out
	}
	pd.CommentsHTML = func(postID int64) string {
		if p, err := s.store.GetPost(ctx, postID); err == nil && c != nil { // gap-widgets: theme comments template (gwe_post_comments.go)
			return s.gweCommentsHTML(c, p, lb)
		}
		if c == nil {
			return ""
		}
		p, err := s.store.GetPost(ctx, postID)
		if err != nil {
			return ""
		}
		cv := s.commentsView(c, p, lb, currentUser(c))
		if cv == nil {
			return ""
		}
		return string(cv.Title) + string(cv.List) + string(cv.Form)
	}
	pd.Template = func(id int64) *builder.Document {
		p, err := s.store.GetPost(ctx, id)
		if err != nil || !gweBuilt(p) {
			return nil
		}
		// Published templates only, except in the editor for users who may edit them; a fresh
		// document every call (rendering mutates it).
		if p.Status != "publish" && p.Status != "private" {
			if c == nil || !pd.IsEditor || !currentUser(c).Can("edit_post", p) {
				return nil
			}
		}
		return gweDocument(p)
	}
	pd.Previous = func(postID int64, sameTerm, taxonomy string) *builder.PostData {
		return s.gweProAdjacent(ctx, postID, sameTerm, taxonomy, true, lb)
	}
	pd.Next = func(postID int64, sameTerm, taxonomy string) *builder.PostData {
		return s.gweProAdjacent(ctx, postID, sameTerm, taxonomy, false, lb)
	}
	pd.Breadcrumbs = func() []builder.TermLink { return s.gweBreadcrumbTrail(ctx, v, lb) } // gap-widgets: native trail (gwe_breadcrumbs.go)
	pd.PostTypeLabel = func(typ string) string {
		switch typ {
		case "post":
			return "Posts"
		case "page":
			return "Pages"
		}
		return strings.ToUpper(typ[:1]) + typ[1:]
	}
	pd.Sitemap = func(kind string, args map[string]any) []*builder.SitemapNode {
		return s.gweProSitemap(ctx, kind, args, lb)
	}
	pd.LoopItemClasses = func(postID int64, typ string) string {
		p, err := s.store.GetPost(ctx, postID)
		if err != nil {
			return ""
		}
		return s.gweProPostClass(ctx, p)
	}
	return pd
}

// gweProUserInfo converts a user.
func (s *Server) gweProUserInfo(ctx context.Context, u *store.User) *builder.UserInfo {
	if u == nil {
		return nil
	}
	lb := s.links(ctx)
	info := &builder.UserInfo{ID: u.ID, Login: u.Login, Email: u.Email, URL: u.URL, Nicename: u.Nicename, DisplayName: u.DisplayName,
		FirstName: u.Profile["first_name"], LastName: u.Profile["last_name"], Description: u.Profile["description"], Roles: u.Roles,
		PostsURL: lb.AuthorLink(u.ID, u.Nicename), Meta: map[string]any{}}
	for k, v := range u.Profile {
		info.Meta[k] = v
	}
	for k, v := range u.Meta {
		info.Meta[k] = v
	}
	info.Meta["registered_iso"] = u.RegisteredAt.In(s.loc()).Format(time.RFC3339)
	if c, ok := ctx.(*gin.Context); ok {
		info.AvatarURL = s.avatarURL(c, u.Email, 96)
	}
	if counts, err := s.store.UserPostCounts(ctx, []int64{u.ID}); err == nil {
		info.PostCount = counts[u.ID]
	}
	return info
}

// gweProPostData is a post as the query widgets print it (no rendered content).
func (s *Server) gweProPostData(ctx context.Context, p *store.Post, lb *links.Builder) *builder.PostData {
	pd := &builder.PostData{ID: p.ID, Type: p.Type, Title: html.EscapeString(p.Title), URL: lb.Permalink(p), CommentCount: p.CommentCount,
		ImageID: p.FeaturedMediaID}
	if p.Title == "" {
		pd.Title = "(no title)"
	}
	if p.PublishedAt != nil {
		t := p.PublishedAt.In(s.loc())
		pd.Date = wp.FormatDate(t, s.settings.String("date_format"))
		pd.DateISO = t.Format(time.RFC3339)
		pd.Time = wp.FormatDate(t, s.settings.String("time_format"))
	}
	pd.CommentsURL = pd.URL + "#comments"
	if p.CommentCount > 0 {
		pd.CommentsURL = pd.URL + "#comments"
	} else {
		pd.CommentsURL = pd.URL + "#respond"
	}
	if a, err := s.store.UserByID(ctx, p.AuthorID); err == nil {
		pd.AuthorName = html.EscapeString(a.DisplayName)
		pd.AuthorURL = lb.AuthorLink(a.ID, a.Nicename)
	}
	if p.Password != "" {
		pd.Excerpt = "There is no excerpt because this is a protected post."
	} else if p.Excerpt != "" {
		pd.Excerpt = wp.StripTags(p.Excerpt)
	} else {
		text, _, _, _ := wp.SplitMore(stripBlocks(p.Content))
		pd.Excerpt = strings.Join(strings.Fields(html.UnescapeString(wp.StripTags(text))), " ")
	}
	if p.FeaturedMediaID > 0 {
		if a, err := s.store.GetAttachment(ctx, p.FeaturedMediaID); err == nil {
			pd.ImageURL = s.uploadURL(ctx, a.FilePath)
		}
	}
	cats, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "category")
	tags, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "post_tag")
	for _, t := range cats[p.ID] {
		pd.Categories = append(pd.Categories, builder.TermLink{Name: html.EscapeString(t.Name), URL: lb.TermLink(t)})
	}
	for _, t := range tags[p.ID] {
		pd.Tags = append(pd.Tags, builder.TermLink{Name: html.EscapeString(t.Name), URL: lb.TermLink(t)})
	}
	pd.Class = s.gweProPostClass(ctx, p)
	return pd
}

// gweProPostClass is post_class() for a post.
func (s *Server) gweProPostClass(ctx context.Context, p *store.Post) string {
	classes := []string{"post-" + itoa64(p.ID), p.Type, "type-" + p.Type, "status-" + p.Status}
	if p.Type == "post" {
		classes = append(classes, "format-"+p.Format)
	}
	if p.FeaturedMediaID > 0 {
		classes = append(classes, "has-post-thumbnail")
	}
	if p.Sticky {
		classes = append(classes, "sticky")
	}
	classes = append(classes, "hentry")
	cats, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "category")
	tags, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "post_tag")
	for _, t := range cats[p.ID] {
		classes = append(classes, "category-"+t.Slug)
	}
	for _, t := range tags[p.ID] {
		classes = append(classes, "tag-"+t.Slug)
	}
	return strings.Join(classes, " ")
}

func (s *Server) gweProTermItem(t *store.Term, lb *links.Builder) *builder.TermItem {
	return &builder.TermItem{ID: t.ID, Taxonomy: t.Taxonomy, Name: html.EscapeString(t.Name), Slug: t.Slug, URL: lb.TermLink(t),
		Count: t.Count, ParentID: t.ParentID, Description: t.Description, Meta: t.Meta} // gap-loops2: Description, Meta
}

// gweProQuery runs a posts/loop query (Pro's Group_Control_Query) against the posts table.
func (s *Server) gweProQuery(ctx context.Context, q *builder.PostQuery, v *siteView, lb *links.Builder) *builder.PostList {
	out := &builder.PostList{CurrentPage: 1}
	if q == nil {
		return out
	}
	if q.CurrentQuery && v != nil {
		for _, pv := range v.Posts {
			out.Posts = append(out.Posts, s.gwePostData(pv))
		}
		out.Total, out.Pages, out.CurrentPage = v.TotalPosts, v.TotalPages, max(1, v.Q.Paged)
		return out
	}
	var args []any
	add := func(x any) string {
		args = append(args, x)
		return "$" + strconv.Itoa(len(args))
	}
	conds := []string{"p.status = 'publish'"}
	types := q.PostTypes
	if len(types) == 0 {
		types = []string{"post"}
	}
	conds = append(conds, "p.type = ANY("+add(types)+")")
	if len(q.IncludeIDs) > 0 {
		conds = append(conds, "p.id = ANY("+add(q.IncludeIDs)+")")
	}
	if len(q.ExcludeIDs) > 0 {
		conds = append(conds, "NOT (p.id = ANY("+add(q.ExcludeIDs)+"))")
	}
	if len(q.IncludeAuthors) > 0 {
		conds = append(conds, "p.author_id = ANY("+add(q.IncludeAuthors)+")")
	}
	if len(q.ExcludeAuthors) > 0 {
		conds = append(conds, "NOT (coalesce(p.author_id,0) = ANY("+add(q.ExcludeAuthors)+"))")
	}
	if len(q.IncludeTermIDs) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = ANY("+add(q.IncludeTermIDs)+"))")
	}
	if len(q.ExcludeTermIDs) > 0 {
		conds = append(conds, "NOT EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = ANY("+add(q.ExcludeTermIDs)+"))")
	}
	if q.RelatedTo > 0 {
		tax := q.RelatedTaxonomy
		if tax == "" {
			tax = "category"
		}
		conds = append(conds, "p.id <> "+add(q.RelatedTo)+" AND EXISTS (SELECT 1 FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = p.id AND t.taxonomy = "+add(tax)+
			" AND pt.term_id IN (SELECT term_id FROM post_terms WHERE post_id = "+add(q.RelatedTo)+"))")
	}
	if q.Search != "" {
		conds = append(conds, "(p.title ILIKE "+add("%"+q.Search+"%")+" OR p.content ILIKE "+add("%"+q.Search+"%")+")")
	}
	now := time.Now().In(s.loc())
	switch q.DateMode {
	case "today":
		conds = append(conds, "p.published_at >= "+add(now.Add(-24*time.Hour)))
	case "week":
		conds = append(conds, "p.published_at >= "+add(now.AddDate(0, 0, -7)))
	case "month":
		conds = append(conds, "p.published_at >= "+add(now.AddDate(0, -1, 0)))
	case "quarter":
		conds = append(conds, "p.published_at >= "+add(now.AddDate(0, -3, 0)))
	case "year":
		conds = append(conds, "p.published_at >= "+add(now.AddDate(-1, 0, 0)))
	case "exact":
		if t, err := time.ParseInLocation("2006-01-02 15:04", q.DateAfter, s.loc()); err == nil {
			conds = append(conds, "p.published_at >= "+add(t))
		}
		if t, err := time.ParseInLocation("2006-01-02 15:04", q.DateBefore, s.loc()); err == nil {
			conds = append(conds, "p.published_at <= "+add(t))
		}
	}
	order := "p.published_at DESC NULLS LAST, p.id DESC"
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	switch q.OrderBy {
	case "post_title", "title":
		order = "p.title " + dir
	case "menu_order":
		order = "p.menu_order " + dir + ", p.title ASC"
	case "rand":
		order = "random()"
	case "comment_count":
		order = "p.comment_count " + dir
	case "modified":
		order = "p.modified_at " + dir
	case "ID", "id":
		order = "p.id " + dir
	case "post_date", "date", "":
		order = "p.published_at " + dir + " NULLS LAST, p.id " + dir
	}
	if !q.IgnoreSticky && q.OrderBy != "rand" && len(q.IncludeIDs) == 0 {
		order = "p.sticky DESC, " + order
	}
	where := strings.Join(conds, " AND ")
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts p WHERE `+where, args...).Scan(&total); err != nil {
		return out
	}
	perPage := q.PerPage
	if perPage <= 0 {
		perPage = 6
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	offset := q.Offset + (page-1)*perPage
	total -= q.Offset
	if total < 0 {
		total = 0
	}
	out.Total = total
	out.Pages = (total + perPage - 1) / perPage
	out.CurrentPage = page
	rows, err := s.store.Pool.Query(ctx, `SELECT p.id FROM posts p WHERE `+where+` ORDER BY `+order+` LIMIT `+add(perPage)+` OFFSET `+add(offset), args...)
	if err != nil {
		return out
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if p, err := s.store.GetPost(ctx, id); err == nil {
			out.Posts = append(out.Posts, s.gweProPostData(ctx, p, lb))
		}
	}
	return out
}

// gweProAdjacent is get_previous_post / get_next_post.
func (s *Server) gweProAdjacent(ctx context.Context, postID int64, sameTerm, taxonomy string, prev bool, lb *links.Builder) *builder.PostData {
	p, err := s.store.GetPost(ctx, postID)
	if err != nil || p.PublishedAt == nil {
		return nil
	}
	op, order := "<", "DESC"
	if !prev {
		op, order = ">", "ASC"
	}
	args := []any{p.Type, *p.PublishedAt, p.ID}
	where := `type = $1 AND status = 'publish' AND (published_at ` + op + ` $2 OR (published_at = $2 AND id ` + op + ` $3))`
	if sameTerm == "yes" {
		if taxonomy == "" {
			taxonomy = "category"
		}
		args = append(args, taxonomy)
		where += ` AND EXISTS (SELECT 1 FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = posts.id AND t.taxonomy = $4
			AND pt.term_id IN (SELECT term_id FROM post_terms WHERE post_id = $3))`
	}
	var id int64
	if err := s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE `+where+` ORDER BY published_at `+order+`, id `+order+` LIMIT 1`, args...).Scan(&id); err != nil {
		return nil
	}
	adj, err := s.store.GetPost(ctx, id)
	if err != nil {
		return nil
	}
	return s.gweProPostData(ctx, adj, lb)
}

// gweProBreadcrumbs is the trail of the page being shown: Home › (archive | parents › page).
func (s *Server) gweProBreadcrumbs(ctx context.Context, v *siteView, lb *links.Builder) []builder.TermLink {
	out := []builder.TermLink{{Name: "Home", URL: lb.HomeURL("/")}}
	if v == nil || v.Q == nil {
		return out
	}
	q := v.Q
	switch {
	case q.Post != nil && (q.Kind == "single" || q.Kind == "page"):
		var chain []*store.Post
		cur := q.Post
		for i := 0; i < 10 && cur.ParentID > 0; i++ {
			parent, err := s.store.GetPost(ctx, cur.ParentID)
			if err != nil {
				break
			}
			chain = append([]*store.Post{parent}, chain...)
			cur = parent
		}
		if q.Post.Type == "post" {
			cats, _ := s.store.TermsForPosts(ctx, []int64{q.Post.ID}, "category")
			if len(cats[q.Post.ID]) > 0 {
				t := cats[q.Post.ID][0]
				out = append(out, builder.TermLink{Name: html.EscapeString(t.Name), URL: lb.TermLink(t)})
			}
		}
		for _, p := range chain {
			out = append(out, builder.TermLink{Name: html.EscapeString(p.Title), URL: lb.Permalink(p)})
		}
		out = append(out, builder.TermLink{Name: html.EscapeString(q.Post.Title)})
	case q.Term != nil:
		out = append(out, builder.TermLink{Name: html.EscapeString(q.Term.Name)})
	case q.Author != nil:
		out = append(out, builder.TermLink{Name: html.EscapeString(q.Author.DisplayName)})
	case q.Kind == "search":
		out = append(out, builder.TermLink{Name: "Search results for: " + html.EscapeString(q.Search)})
	case q.Kind == "404":
		out = append(out, builder.TermLink{Name: "Error 404: Page not found"})
	case q.Year > 0:
		_, title := q.archiveTitle()
		out = append(out, builder.TermLink{Name: title})
	}
	return out
}

// gweProSitemap lists posts of a type or terms of a taxonomy (sitemap widget).
func (s *Server) gweProSitemap(ctx context.Context, kind string, args map[string]any, lb *links.Builder) []*builder.SitemapNode {
	var out []*builder.SitemapNode
	if kind == "post_type" {
		typ, _ := args["source"].(string)
		if typ == "" {
			typ = "post"
		}
		order, _ := args["orderby"].(string)
		dir, _ := args["order"].(string)
		posts, _, err := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{typ}, Statuses: []string{"publish"}, PerPage: -1, Page: 1, OrderBy: order, Order: strings.ToUpper(dir)})
		if err != nil {
			return nil
		}
		byParent := map[int64][]*store.Post{}
		for _, p := range posts {
			byParent[p.ParentID] = append(byParent[p.ParentID], p)
		}
		hier, _ := args["hierarchical"].(bool)
		var build func(parent int64) []*builder.SitemapNode
		build = func(parent int64) []*builder.SitemapNode {
			var nodes []*builder.SitemapNode
			for _, p := range byParent[parent] {
				n := &builder.SitemapNode{Title: html.EscapeString(p.Title), URL: lb.Permalink(p)}
				if hier {
					n.Children = build(p.ID)
				}
				nodes = append(nodes, n)
			}
			return nodes
		}
		if hier {
			return build(0)
		}
		for _, p := range posts {
			out = append(out, &builder.SitemapNode{Title: html.EscapeString(p.Title), URL: lb.Permalink(p)})
		}
		return out
	}
	tax, _ := args["source"].(string)
	if tax == "" {
		tax = "category"
	}
	hideEmpty, _ := args["hide_empty"].(bool)
	terms, err := s.store.HierarchicalTerms(ctx, tax, hideEmpty)
	if err != nil {
		return nil
	}
	hier, _ := args["hierarchical"].(bool)
	if !hier {
		for _, t := range terms {
			out = append(out, &builder.SitemapNode{Title: html.EscapeString(t.Name), URL: lb.TermLink(t)})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Title < out[j].Title })
		return out
	}
	byParent := map[int64][]*store.Term{}
	for _, t := range terms {
		byParent[t.ParentID] = append(byParent[t.ParentID], t)
	}
	var build func(parent int64) []*builder.SitemapNode
	build = func(parent int64) []*builder.SitemapNode {
		var nodes []*builder.SitemapNode
		for _, t := range byParent[parent] {
			nodes = append(nodes, &builder.SitemapNode{Title: html.EscapeString(t.Name), URL: lb.TermLink(t), Children: build(t.ID)})
		}
		return nodes
	}
	return build(0)
}

// --- render_tags -------------------------------------------------------------------------------

// gweProRenderTags answers the editor's render_tags action: {post_id, tags: ["<b64 name>-<b64 settings>", ...]}
// → {key: content}.
func (s *Server) gweProRenderTags(c *gin.Context, data map[string]any, editorID int64) (any, error) {
	id := gweInt(data["post_id"])
	if id == 0 {
		id = editorID
	}
	p, err := s.gwePostFor(c, id, "edit_post")
	if err != nil {
		return nil, err
	}
	s.gweThemeSample(c, p)
	rc := s.gweRenderContext(c, true)
	doc := gweDocument(p)
	out := map[string]any{}
	for _, raw := range asAnyList(data["tags"]) {
		key := gweStr(raw)
		parts := strings.SplitN(key, "-", 2)
		if len(parts) != 2 {
			out[key] = ""
			continue
		}
		name, err1 := base64.StdEncoding.DecodeString(parts[0])
		st, err2 := base64.StdEncoding.DecodeString(parts[1])
		if err1 != nil || err2 != nil {
			out[key] = ""
			continue
		}
		settings, _ := url.QueryUnescape(string(st))
		var m map[string]any
		if json.Unmarshal([]byte(settings), &m) != nil {
			m = map[string]any{}
		}
		sj, _ := json.Marshal(m)
		text := `[elementor-tag id="` + strings.ReplaceAll(key[:7], `"`, "") + `" name="` + string(name) + `" settings="` + url.QueryEscape(string(sj)) + `"]`
		out[key] = s.gweFixURLsAny(c, builder.RenderDynamicTag(text, doc, rc))
	}
	return out, nil
}

// gweFixURLsAny rebases lab asset URLs in a tag value (string or object).
func (s *Server) gweFixURLsAny(c *gin.Context, v any) any {
	switch t := v.(type) {
	case string:
		return s.gweFixURLs(c, t)
	case map[string]any:
		for k, x := range t {
			if str, ok := x.(string); ok {
				t[k] = s.gweFixURLs(c, str)
			}
		}
	}
	return v
}

var _ = http.StatusOK
