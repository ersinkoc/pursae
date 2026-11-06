package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
	"wpgo/internal/wp"
)

// Theme Builder slots on the server (contract §11.5): the webbuilder.Resolver that fills a
// published snapshot's placeholders from the request's siteView, and the post loops.
//
// Values come from what buildSiteView already computed (v.Posts, v.Post, v.PostNav, v.Comments,
// v.Menus, archive title, search query, pagination). A custom loop (source query or related) adds
// one QueryPosts and, when a card shows terms, one batched TermsForPosts. Expensive values are
// produced only when the snapshot reads the slot (the engine asks for a slot only when an element
// uses it): theContent of a loop post, its featured image, its author.
//
// Escaping: text slots are escaped by the engine; html slots only return markup of trusted core
// generators (theTitle, theContent, paginateLinks, the theme's comments template) or values this
// file escapes itself (term links, excerpts); attr slots return raw values that the engine checks
// (SafeHref for href, ImageURLOK for src, …) before writing them.

// wbPrograms caches compiled snapshots (key postID:rev:sha256(html)[:16]).
var wbPrograms = webbuilder.NewProgramCache(webbuilder.CompiledCacheSize)

// --- loop specs ----------------------------------------------------------------------------------

// wbLoopDef is a loop section of a stored doc.
type wbLoopDef struct {
	spec webbuilder.LoopSpec
	// typeSet: content.postType was given (related loops default to the current post's type).
	typeSet bool
}

// wbLoopSpecs caches the loop sections of stored docs, keyed "postID:rev:updated" (a publish
// bumps rev; the time keeps two databases of one process, as in tests, apart).
var wbLoopSpecs struct {
	sync.Mutex
	m map[string]map[string]wbLoopDef
}

const wbLoopSpecsMax = 512

// wbLoopDefs reads the loop sections of a stored doc (never the snapshot's attributes).
func wbLoopDefs(d *store.WebBuilderDoc) map[string]wbLoopDef {
	key := itoa64(d.PostID) + ":" + strconv.Itoa(d.Rev) + ":" + strconv.FormatInt(d.UpdatedAt.UnixNano(), 36)
	wbLoopSpecs.Lock()
	if m, ok := wbLoopSpecs.m[key]; ok {
		wbLoopSpecs.Unlock()
		return m
	}
	wbLoopSpecs.Unlock()
	out := map[string]wbLoopDef{}
	var doc webbuilder.DocState
	if len(d.Doc) > 0 {
		if err := json.Unmarshal(d.Doc, &doc); err != nil {
			log.Printf("web builder: doc %d: %v", d.PostID, err)
		}
	}
	var walk func(s *webbuilder.Section, depth int)
	walk = func(s *webbuilder.Section, depth int) {
		if s.Kind == webbuilder.KindLoop && s.ID != "" {
			if _, seen := out[s.ID]; !seen {
				out[s.ID] = wbLoopDef{spec: webbuilder.ParseLoopSpec(s.Content), typeSet: strings.TrimSpace(s.Content[webbuilder.LoopPostType]) != ""}
			}
		}
		if depth > webbuilder.MaxDepth+1 {
			return
		}
		for i := range s.Items {
			if sub := s.Items[i].Sub; sub != nil {
				walk(sub, depth+1)
			}
		}
	}
	for i := range doc.Sections {
		walk(&doc.Sections[i], 0)
	}
	wbLoopSpecs.Lock()
	if wbLoopSpecs.m == nil || len(wbLoopSpecs.m) >= wbLoopSpecsMax {
		wbLoopSpecs.m = map[string]map[string]wbLoopDef{}
	}
	wbLoopSpecs.m[key] = out
	wbLoopSpecs.Unlock()
	return out
}

// --- environment ---------------------------------------------------------------------------------

// wbSlotEnv is what the resolvers of one filled snapshot share.
type wbSlotEnv struct {
	s   *Server
	c   *gin.Context
	ctx context.Context
	v   *siteView
	lb  *links.Builder
	u   *store.User

	doc  *store.WebBuilderDoc
	prog *webbuilder.Program
	// own: the snapshot is the main post's own builder doc (its post.content would be itself).
	own bool

	main  *wbSlotPost // the singular post of the request, nil elsewhere
	users map[int64]*store.User
	loops bool // the snapshot has a post loop (cache tag wb-loop)
	// loopMemo: the posts of each loop id, resolved once per fill. A loop's posts depend only on
	// the doc and the request (never on the outer item of a nested loop), so every data-wb-loop
	// element of the same id, at any depth, shares one query.
	loopMemo map[string]wbLoopResult

	logo     map[string]string
	logoDone bool
}

// wbSlotPost is one post a resolver shows: the request's singular post, a main-loop item (both
// with the postView buildSiteView made) or a custom-loop item.
type wbSlotPost struct {
	p    *store.Post
	pv   *postView
	main bool

	content     *string
	terms       map[string][]*store.Term // by taxonomy; nil = not loaded
	author      *store.User
	authorDone  bool
	avatar      *string
	image       map[string]map[string]string // by size
	permalink   string
	permalinkOK bool
}

func newWBSlotEnv(s *Server, c *gin.Context, v *siteView, d *store.WebBuilderDoc, prog *webbuilder.Program, own bool) *wbSlotEnv {
	ctx := v.ctx
	if ctx == nil {
		ctx = c.Request.Context()
	}
	e := &wbSlotEnv{s: s, c: c, ctx: ctx, v: v, lb: s.links(ctx), u: currentUser(c), doc: d, prog: prog, own: own,
		users: map[int64]*store.User{}, loops: len(prog.Loops) > 0}
	if v.Post != nil && v.Q != nil && v.Q.Post != nil {
		switch v.Q.Kind {
		case "single", "page", "front-page", "attachment":
			e.main = &wbSlotPost{p: v.Q.Post, pv: v.Post, main: true}
		}
	}
	return e
}

// root is the resolver of the snapshot's top level: post.* is the singular post.
func (e *wbSlotEnv) root() *wbSlotResolver { return &wbSlotResolver{env: e, post: e.main} }

func (e *wbSlotEnv) user(id int64) *store.User {
	if id <= 0 {
		return nil
	}
	if u, ok := e.users[id]; ok {
		return u
	}
	u, err := e.s.store.UserByID(e.ctx, id)
	if err != nil {
		u = nil
	}
	e.users[id] = u
	return u
}

// --- resolver ------------------------------------------------------------------------------------

// wbSlotResolver resolves one scope: the snapshot (post = the singular post), a loop item (post)
// or a menu item (menu). Slots outside its scope go to the parent.
type wbSlotResolver struct {
	env    *wbSlotEnv
	post   *wbSlotPost
	menu   *store.MenuItem
	parent *wbSlotResolver
}

var _ webbuilder.Resolver = (*wbSlotResolver)(nil)

func (r *wbSlotResolver) nearestMenu() *store.MenuItem {
	for x := r; x != nil; x = x.parent {
		if x.menu != nil {
			return x.menu
		}
	}
	return nil
}

func (r *wbSlotResolver) nearestPost() *wbSlotPost {
	for x := r; x != nil; x = x.parent {
		if x.post != nil {
			return x.post
		}
	}
	return nil
}

func (r *wbSlotResolver) Text(slot webbuilder.SlotName, a webbuilder.SlotArgs) (string, bool) {
	e := r.env
	v := e.v
	switch slot {
	case webbuilder.SlotSiteTitle:
		return html.UnescapeString(v.SiteName), v.SiteName != ""
	case webbuilder.SlotSiteTagline:
		return html.UnescapeString(v.Description), v.Description != ""
	case webbuilder.SlotSiteYear:
		return strconv.Itoa(time.Now().In(e.s.loc()).Year()), true
	case webbuilder.SlotMenuLabel:
		if m := r.nearestMenu(); m != nil {
			l := wbPlain(m.Label)
			return l, l != ""
		}
		return "", false
	case webbuilder.SlotSearchQuery:
		if v.Q != nil && v.Q.Kind == "search" {
			return v.SearchQuery, v.SearchQuery != ""
		}
		return "", false
	case webbuilder.SlotSearchCount:
		if v.Q != nil && v.Q.Kind == "search" {
			return strconv.Itoa(v.TotalPosts), true
		}
		return "", false
	}
	sp := r.nearestPost()
	if sp == nil {
		return "", false
	}
	p := sp.p
	switch slot {
	case webbuilder.SlotPostDate, webbuilder.SlotPostModified:
		t := p.Date()
		if slot == webbuilder.SlotPostModified {
			t = p.ModifiedAt
		}
		f := a.Format
		if f == "" {
			f = e.s.settings.String("date_format")
		}
		out := wp.FormatDate(t.In(e.s.loc()), f)
		return out, out != ""
	case webbuilder.SlotPostAuthor:
		if au := sp.authorOf(e); au != nil {
			return au.DisplayName, au.DisplayName != ""
		}
	case webbuilder.SlotPostAuthorBio:
		if au := sp.authorOf(e); au != nil {
			b := au.Profile["description"]
			return b, b != ""
		}
	case webbuilder.SlotPostCommentsCount:
		return strconv.Itoa(p.CommentCount), true
	}
	return "", false
}

func (r *wbSlotResolver) HTML(slot webbuilder.SlotName, a webbuilder.SlotArgs) (string, bool) {
	e := r.env
	v := e.v
	switch slot {
	case webbuilder.SlotArchiveTitle:
		t := e.archiveTitle()
		return t, t != ""
	case webbuilder.SlotArchiveDescription:
		if wbArchiveView(v) {
			return string(v.ArchiveDesc), v.ArchiveDesc != ""
		}
		return "", false
	case webbuilder.SlotLoopPagination:
		out := e.pagination()
		return out, out != ""
	}
	sp := r.nearestPost()
	if sp == nil {
		return "", false
	}
	p := sp.p
	switch slot {
	case webbuilder.SlotPostTitle:
		if sp.pv != nil {
			return string(sp.pv.Title), sp.pv.Title != ""
		}
		t := theTitle(p)
		return t, t != ""
	case webbuilder.SlotPostContent:
		if sp.main && e.own {
			return "", false // the page's own doc: its content is this snapshot
		}
		out := sp.contentOf(e)
		return out, strings.TrimSpace(out) != ""
	case webbuilder.SlotPostExcerpt:
		out := wbExcerpt(p, a.Words)
		return out, out != ""
	case webbuilder.SlotPostTerms:
		tax := a.Tax
		if tax == "" {
			tax = "category"
		}
		sep := a.Sep
		if sep == "" {
			sep = ", "
		}
		terms := sp.termsOf(e, tax)
		parts := make([]string, 0, len(terms))
		for _, t := range terms {
			parts = append(parts, `<a href="`+html.EscapeString(e.lb.TermLink(t))+`" rel="tag">`+html.EscapeString(t.Name)+`</a>`)
		}
		return strings.Join(parts, html.EscapeString(sep)), len(parts) > 0
	case webbuilder.SlotPostNav:
		if sp.main {
			return string(v.PostNav), v.PostNav != ""
		}
	case webbuilder.SlotPostComments:
		if sp.main {
			out := e.comments()
			return out, out != ""
		}
	}
	return "", false
}

func (r *wbSlotResolver) Attrs(slot webbuilder.SlotName, a webbuilder.SlotArgs) (map[string]string, bool) {
	e := r.env
	switch slot {
	case webbuilder.SlotSiteURL:
		return map[string]string{"href": e.lb.HomeURL("/")}, true
	case webbuilder.SlotSiteLogo:
		m := e.siteLogo()
		return m, len(m) > 0
	case webbuilder.SlotMenuURL:
		m := r.nearestMenu()
		if m == nil {
			return nil, false
		}
		out := map[string]string{"href": m.URL}
		if m.Current {
			out["aria-current"] = "page"
			out["class"] = "is-current"
		}
		return out, true
	}
	sp := r.nearestPost()
	if sp == nil {
		return nil, false
	}
	p := sp.p
	switch slot {
	case webbuilder.SlotPostURL:
		return map[string]string{"href": sp.permalinkOf(e)}, true
	case webbuilder.SlotPostCommentsURL:
		return map[string]string{"href": sp.permalinkOf(e) + "#comments"}, true
	case webbuilder.SlotPostDate:
		return map[string]string{"datetime": p.Date().In(e.s.loc()).Format(time.RFC3339)}, true
	case webbuilder.SlotPostModified:
		return map[string]string{"datetime": p.ModifiedAt.In(e.s.loc()).Format(time.RFC3339)}, true
	case webbuilder.SlotPostAuthorURL:
		if au := sp.authorOf(e); au != nil {
			return map[string]string{"href": e.lb.AuthorLink(au.ID, au.Nicename)}, true
		}
	case webbuilder.SlotPostAvatar:
		if src := sp.avatarOf(e); src != "" {
			alt := ""
			if au := sp.authorOf(e); au != nil {
				alt = au.DisplayName
			}
			return map[string]string{"src": src, "alt": alt}, true
		}
	case webbuilder.SlotPostImage:
		m := sp.imageOf(e, a.Size)
		return m, len(m) > 0
	}
	return nil, false
}

func (r *wbSlotResolver) Loop(id string, _ webbuilder.SlotArgs) (webbuilder.LoopIter, bool) {
	posts, ok := r.env.loopPosts(id)
	if !ok {
		return nil, false
	}
	items := make([]webbuilder.Resolver, 0, len(posts))
	for _, sp := range posts {
		items = append(items, &wbSlotResolver{env: r.env, post: sp, parent: r})
	}
	return &webbuilder.SliceIter{Items: items}, true
}

func (r *wbSlotResolver) Menu(location string, _ webbuilder.SlotArgs) (webbuilder.LoopIter, bool) {
	menu := r.env.v.Menus[location]
	if len(menu) == 0 {
		return nil, false
	}
	items := make([]webbuilder.Resolver, 0, len(menu))
	for _, it := range menu { // top level only in v1
		items = append(items, &wbSlotResolver{env: r.env, menu: it, parent: r})
	}
	return &webbuilder.SliceIter{Items: items}, true
}

// --- values --------------------------------------------------------------------------------------

// wbArchiveView: the request lists posts (posts index, term, author, date, type archive, search).
func wbArchiveView(v *siteView) bool {
	if v == nil || v.Q == nil {
		return false
	}
	switch v.Q.Kind {
	case "home", "category", "tag", "author", "date", "search", "post-type-archive":
		return true
	}
	return false
}

// archiveTitle is get_the_archive_title() for archive views (escaped HTML); the posts index shows
// the posts page title, search results "Search Results for: …".
func (e *wbSlotEnv) archiveTitle() string {
	v := e.v
	if !wbArchiveView(v) {
		return ""
	}
	switch v.Q.Kind {
	case "search":
		return "Search Results for: <span>" + html.EscapeString(v.SearchQuery) + "</span>"
	case "home":
		if v.Q.PostsPage != nil {
			return wp.Texturize(wp.EscText(v.Q.PostsPage.Title))
		}
		return "Posts"
	}
	if v.ArchiveTitle != "" {
		return string(v.ArchiveTitle)
	}
	t, _ := v.Q.archiveTitle()
	return t
}

// pagination is loop.pagination: the main query's links, in the mode of the snapshot's first
// main loop (numbers when it has none; none hides them). Singular views have no main pagination.
func (e *wbSlotEnv) pagination() string {
	v := e.v
	if !wbArchiveView(v) || v.TotalPages < 2 {
		return ""
	}
	mode := "numbers"
	defs := wbLoopDefs(e.doc)
	for _, id := range e.prog.Loops {
		if d, ok := defs[id]; ok && d.spec.Source == webbuilder.LoopSourceMain {
			mode = d.spec.Pagination
			break
		}
	}
	switch mode {
	case "none":
		return ""
	case "prev-next":
		return wbPrevNext(e.lb, e.s.archiveBase(v.Q, e.lb), max(v.Q.Paged, 1), v.TotalPages)
	}
	return string(v.Pagination)
}

// wbPrevNext is the_posts_pagination() reduced to the previous / next links.
func wbPrevNext(lb *links.Builder, base string, current, total int) string {
	var out []string
	if current > 1 {
		out = append(out, fmt.Sprintf(`<a class="prev page-numbers" href="%s">Previous page</a>`, html.EscapeString(lb.Paged(base, current-1))))
	}
	if current < total {
		out = append(out, fmt.Sprintf(`<a class="next page-numbers" href="%s">Next page</a>`, html.EscapeString(lb.Paged(base, current+1))))
	}
	if len(out) == 0 {
		return ""
	}
	return `
	<nav class="navigation pagination" aria-label="Posts pagination">
		<h2 class="screen-reader-text">Posts pagination</h2>
		<div class="nav-links">` + strings.Join(out, "\n") + `</div>
	</nav>`
}

// comments is the theme's comments template for the singular post ("" when the theme has none).
func (e *wbSlotEnv) comments() string {
	v := e.v
	if v.Comments == nil || v.Theme == nil {
		return ""
	}
	set := e.s.tmpl.getTheme(true, v.Theme.Slug)
	if set == nil || set.Lookup("comments") == nil {
		return ""
	}
	var b bytes.Buffer
	if err := set.ExecuteTemplate(&b, "comments", v); err != nil {
		log.Printf("web builder: comments template: %v", err)
		return ""
	}
	return b.String()
}

// siteLogo is the img attributes of the custom logo of the rendered theme.
func (e *wbSlotEnv) siteLogo() map[string]string {
	if e.logoDone {
		return e.logo
	}
	e.logoDone = true
	v := e.v
	if v.Theme == nil || !themeSupportsLogo(v.Theme) {
		return nil
	}
	mods := v.ThemeMods
	if mods == nil {
		mods = e.s.themeMods(v.Theme.Slug)
	}
	id := int64(modInt(mods, "custom_logo", 0))
	if id <= 0 {
		return nil
	}
	a, err := e.s.store.GetAttachment(e.ctx, id)
	if err != nil {
		return nil
	}
	e.logo = e.s.wbImageAttrs(e.ctx, a, "full", html.UnescapeString(v.SiteName))
	return e.logo
}

// wbImageAttrs is wp_get_attachment_image() as attributes: the size closest to the requested one
// (large when it has no such variant), srcset over the generated sizes, sizes and alt.
func (s *Server) wbImageAttrs(ctx context.Context, a *store.Attachment, size, altFallback string) map[string]string {
	if a == nil || a.FilePath == "" {
		return nil
	}
	src, w, h := s.uploadURL(ctx, a.FilePath), a.Width, a.Height
	if size != "full" {
		if vr := a.Variant(size); vr != nil {
			src, w, h = s.uploadURL(ctx, vr.FilePath), vr.Width, vr.Height
		} else if vr := a.Variant("large"); vr != nil {
			src, w, h = s.uploadURL(ctx, vr.FilePath), vr.Width, vr.Height
		}
	}
	out := map[string]string{"src": src}
	alt := a.Alt
	if alt == "" {
		alt = altFallback
	}
	out["alt"] = alt
	if w > 0 && h > 0 {
		out["width"], out["height"] = strconv.Itoa(w), strconv.Itoa(h)
	}
	if a.Width > 0 {
		set := []string{fmt.Sprintf("%s %dw", s.uploadURL(ctx, a.FilePath), a.Width)}
		for _, n := range []string{"medium", "medium_large", "large", "1536x1536", "2048x2048"} {
			if vr := a.Variant(n); vr != nil && vr.Width > 0 {
				set = append(set, fmt.Sprintf("%s %dw", s.uploadURL(ctx, vr.FilePath), vr.Width))
			}
		}
		if len(set) > 1 {
			out["srcset"] = strings.Join(set, ", ")
			if w > 0 {
				out["sizes"] = fmt.Sprintf("(max-width: %dpx) 100vw, %dpx", w, w)
			}
		}
	}
	return out
}

// wbExcerpt is the post's excerpt as escaped text: the manual excerpt or the content before
// <!--more-->, trimmed to words (55 when 0) with an ellipsis. Plain text, so it fits the
// paragraph the widget draws around it.
func wbExcerpt(p *store.Post, words int) string {
	if p.Password != "" {
		return "There is no excerpt because this is a protected post."
	}
	if words <= 0 {
		words = 55
	}
	src := p.Excerpt
	if src == "" {
		src, _, _, _ = wp.SplitMore(stripBlocks(p.Content))
		src = shortcodeRe.ReplaceAllString(src, "")
	}
	return strings.TrimSpace(wp.TrimWords(src, words, "&hellip;"))
}

func (sp *wbSlotPost) permalinkOf(e *wbSlotEnv) string {
	if sp.pv != nil {
		return sp.pv.Permalink
	}
	if !sp.permalinkOK {
		sp.permalink, sp.permalinkOK = e.lb.Permalink(sp.p), true
	}
	return sp.permalink
}

func (sp *wbSlotPost) authorOf(e *wbSlotEnv) *store.User {
	if !sp.authorDone {
		sp.author, sp.authorDone = e.user(sp.p.AuthorID), true
	}
	return sp.author
}

func (sp *wbSlotPost) avatarOf(e *wbSlotEnv) string {
	if sp.avatar == nil {
		src := ""
		if au := sp.authorOf(e); au != nil && au.Email != "" {
			if a := e.s.avatarFor(e.ctx, au.Email, 96); a != defaultAvatar {
				src = a
			}
		}
		sp.avatar = &src
	}
	return *sp.avatar
}

func (sp *wbSlotPost) imageOf(e *wbSlotEnv, size string) map[string]string {
	if size == "" {
		size = "large"
	}
	if m, ok := sp.image[size]; ok {
		return m
	}
	if sp.image == nil {
		sp.image = map[string]map[string]string{}
	}
	var m map[string]string
	if sp.p.FeaturedMediaID > 0 {
		if a, err := e.s.store.GetAttachment(e.ctx, sp.p.FeaturedMediaID); err == nil {
			m = e.s.wbImageAttrs(e.ctx, a, size, "")
		}
	}
	sp.image[size] = m
	return m
}

// contentOf is the_content() of the post: the postView's when buildSiteView made one, else
// rendered once on first use.
func (sp *wbSlotPost) contentOf(e *wbSlotEnv) string {
	if sp.pv != nil {
		return string(sp.pv.Content)
	}
	if sp.content == nil {
		out := e.s.theContent(e.ctx, e.c, sp.p, false, e.lb)
		sp.content = &out
	}
	return *sp.content
}

// termsOf is the post's terms of a taxonomy (loop items arrive with theirs batched).
func (sp *wbSlotPost) termsOf(e *wbSlotEnv, tax string) []*store.Term {
	if sp.terms == nil {
		sp.terms = map[string][]*store.Term{}
		if m, err := e.s.wbTermsForPosts(e.ctx, []int64{sp.p.ID}); err == nil {
			sp.terms = m[sp.p.ID]
		}
	}
	return sp.terms[tax]
}

// wbTermsForPosts is every term of the posts, by post and taxonomy, in one query
// (store.TermsForPosts reads one taxonomy).
func (s *Server) wbTermsForPosts(ctx context.Context, ids []int64) (map[int64]map[string][]*store.Term, error) {
	out := map[int64]map[string][]*store.Term{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT pt.post_id, t.id, t.taxonomy, coalesce(t.parent_id,0), t.slug, t.name
		FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = ANY($1)
		ORDER BY pt.post_id, t.taxonomy, pt.sort_order, lower(t.name)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		t := &store.Term{}
		if err := rows.Scan(&pid, &t.ID, &t.Taxonomy, &t.ParentID, &t.Slug, &t.Name); err != nil {
			return nil, err
		}
		if out[pid] == nil {
			out[pid] = map[string][]*store.Term{}
		}
		out[pid][t.Taxonomy] = append(out[pid][t.Taxonomy], t)
	}
	return out, rows.Err()
}

// --- loops ---------------------------------------------------------------------------------------

// loopPosts are the posts of the loop section id of the stored doc: the request's own posts
// (main), a custom query or the related posts of the singular post. ok is false when the doc has
// no such loop (a forged or stale data-wb-loop id).
func (e *wbSlotEnv) loopPosts(id string) ([]*wbSlotPost, bool) {
	if r, ok := e.loopMemo[id]; ok {
		return r.posts, r.ok
	}
	posts, ok := e.queryLoop(id)
	if e.loopMemo == nil {
		e.loopMemo = map[string]wbLoopResult{}
	}
	e.loopMemo[id] = wbLoopResult{posts, ok}
	return posts, ok
}

// wbLoopResult is one memoized loopPosts answer.
type wbLoopResult struct {
	posts []*wbSlotPost
	ok    bool
}

// queryLoop runs the query of loop id (see loopPosts).
func (e *wbSlotEnv) queryLoop(id string) ([]*wbSlotPost, bool) {
	def, ok := wbLoopDefs(e.doc)[id]
	if !ok {
		return nil, false
	}
	sp := def.spec
	if sp.Source == webbuilder.LoopSourceMain {
		if !wbArchiveView(e.v) {
			return nil, true
		}
		out := make([]*wbSlotPost, 0, len(e.v.Posts))
		for _, pv := range e.v.Posts {
			if pv.Post != nil {
				out = append(out, &wbSlotPost{p: pv.Post, pv: pv})
			}
		}
		return out, true
	}
	typ := sp.PostType
	var termIDs []int64
	exclude := []int64{}
	if sp.Source == webbuilder.LoopSourceRelated {
		if e.main == nil {
			return nil, true
		}
		if !def.typeSet {
			typ = e.main.p.Type
		}
		taxes := map[string]bool{}
		for _, t := range sp.Terms {
			taxes[t.Taxonomy] = true
		}
		m, err := e.s.wbTermsForPosts(e.ctx, []int64{e.main.p.ID})
		if err != nil {
			return nil, true
		}
		for tax, terms := range m[e.main.p.ID] {
			if taxes[tax] {
				for _, t := range terms {
					termIDs = append(termIDs, t.ID)
				}
			}
		}
		if len(termIDs) == 0 {
			return nil, true
		}
		exclude = append(exclude, e.main.p.ID)
	} else {
		for _, t := range sp.Terms {
			if t.ID > 0 {
				termIDs = append(termIDs, t.ID)
			}
		}
		if sp.ExcludeCurrent && e.main != nil {
			exclude = append(exclude, e.main.p.ID)
		}
	}
	if !siteViewableType(typ) {
		return nil, true
	}
	pq := store.PostQuery{Types: []string{typ}, Statuses: []string{"publish"}, AuthorID: sp.Author, TermIDs: termIDs,
		OrderBy: sp.OrderBy, Order: sp.Order, Page: 1, PerPage: sp.PerPage + sp.Offset, Loc: e.s.loc()}
	if e.u.Can("read_private_posts", nil) {
		pq.Statuses = append(pq.Statuses, "private")
	}
	pq.ExcludeID = append(exclude, e.s.gooListingKinds(withPluginUser(e.ctx, e.u), pq.Types)...) // Goocommerce Memberships
	random := sp.OrderBy == "rand"
	if random {
		pq.OrderBy, pq.Order, pq.PerPage = "date", "desc", webbuilder.MaxLoopIterations
	}
	posts, _, err := e.s.store.QueryPosts(e.ctx, pq)
	if err != nil {
		log.Printf("web builder: loop %s: %v", id, err)
		return nil, true
	}
	if random {
		rand.Shuffle(len(posts), func(i, j int) { posts[i], posts[j] = posts[j], posts[i] })
	} else if sp.Offset > 0 {
		if sp.Offset >= len(posts) {
			posts = nil
		} else {
			posts = posts[sp.Offset:]
		}
	}
	if len(posts) > sp.PerPage {
		posts = posts[:sp.PerPage]
	}
	out := make([]*wbSlotPost, 0, len(posts))
	ids := make([]int64, 0, len(posts))
	for _, p := range posts {
		out = append(out, &wbSlotPost{p: p})
		ids = append(ids, p.ID)
	}
	if len(ids) > 0 && e.prog.Uses.Has(webbuilder.SlotPostTerms) {
		if m, err := e.s.wbTermsForPosts(e.ctx, ids); err == nil {
			for _, item := range out {
				item.terms = m[item.p.ID]
				if item.terms == nil {
					item.terms = map[string][]*store.Term{}
				}
			}
		}
	}
	return out, true
}

// --- filling -------------------------------------------------------------------------------------

// wbFilled is a filled snapshot.
type wbFilled struct {
	html  string
	loops bool // it has a post loop (cache tag wb-loop)
}

// wbFill fills a published snapshot over the request's view, then applies
// wp_filter_content_tags to it (images: dimensions, srcset, loading). own: d is the main post's
// own builder doc.
func (s *Server) wbFill(c *gin.Context, v *siteView, d *store.WebBuilderDoc, own bool) wbFilled {
	ctx := v.ctx
	if ctx == nil {
		ctx = c.Request.Context()
	}
	prog, err := wbPrograms.Compile(d.PostID, d.Rev, d.HTML)
	if err != nil {
		log.Printf("web builder: compile %d: %v", d.PostID, err)
		return wbFilled{html: s.filterContentTags(ctx, d.HTML)}
	}
	if static, ok := prog.Static(); ok {
		return wbFilled{html: s.filterContentTags(ctx, static)}
	}
	env := newWBSlotEnv(s, c, v, d, prog, own)
	out, err := prog.ExecString(env.root())
	if err != nil {
		log.Printf("web builder: fill %d: %v", d.PostID, err)
	}
	return wbFilled{html: s.filterContentTags(ctx, out), loops: env.loops}
}
