package server

import (
	"context"
	"errors"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/caps"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Theme Builder on the server (docs/developer/web-builder-contract.md §11): the in-memory index
// of published templates, the per-request pick of one template per location (header, main,
// footer) and the capability gate every template path shares.
//
// The index is one query (store.PublishedTemplates) plus, when templates have Pursae Multi
// Language rows, one query for their languages. It is rebuilt when its generation changes
// (wbTemplatesChanged: a template was published, unpublished, trashed or got new conditions) or
// after wbTemplateTTL, so a change that bypasses the builder (a core trash, a language switch)
// is picked up at the latest then. A winner's published doc is loaded once per index.
//
// The picker is pure code over webbuilder.QueryFacts (conditions.go); this file only turns the
// request's siteQuery into facts, applies the translation mapping and the
// ?wb_template_preview=<id> override.

// wbTemplateTTL bounds how long an index is trusted without a generation change.
const wbTemplateTTL = 5 * time.Minute

// wbTemplateState is one Server's template index and generation counter.
type wbTemplateState struct {
	gen atomic.Uint64
	mu  sync.Mutex // one rebuild at a time
	idx atomic.Pointer[wbTemplateIndex]
}

var wbTemplateStates sync.Map // *Server → *wbTemplateState

func (s *Server) wbTemplateState() *wbTemplateState {
	if v, ok := wbTemplateStates.Load(s); ok {
		return v.(*wbTemplateState)
	}
	v, _ := wbTemplateStates.LoadOrStore(s, &wbTemplateState{})
	return v.(*wbTemplateState)
}

// wbTemplateIndex is a snapshot of the published templates.
type wbTemplateIndex struct {
	gen   uint64
	built time.Time

	entries   []store.WBTemplateIndexEntry
	cands     []webbuilder.TemplateCandidate // same order as entries
	needTerms bool                           // some rule is singular/<ptype>/in/<tax>/<id>

	// Pursae Multi Language: the language and translation group of templates that have one.
	lang  map[int64]string
	group map[int64]int64

	docMu sync.Mutex
	docs  map[int64]*store.WebBuilderDoc // published docs of winners, loaded on demand (nil = none)

	failed bool // the build failed: an empty stand-in, not cached, whose responses are not cached either
}

// wbTemplatesChanged is called whenever the set of active templates or their rules may have
// changed: the index generation is bumped and WPGo Cache is purged completely (every page may
// show a header or footer template).
func (s *Server) wbTemplatesChanged(ctx context.Context) {
	s.wbTemplateState().gen.Add(1)
	s.purgeAll(ctx)
}

// wbTemplateIndex returns the current index, rebuilding it when it is stale. A failed rebuild
// returns an empty index (no template renders) without caching it.
func (s *Server) wbTemplateIndex(ctx context.Context) *wbTemplateIndex {
	st := s.wbTemplateState()
	fresh := func(idx *wbTemplateIndex) bool {
		return idx != nil && idx.gen == st.gen.Load() && time.Since(idx.built) < wbTemplateTTL
	}
	if idx := st.idx.Load(); fresh(idx) {
		return idx
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if idx := st.idx.Load(); fresh(idx) {
		return idx
	}
	gen := st.gen.Load() // read before the query: a change during the build makes it stale again
	idx, err := s.wbBuildTemplateIndex(ctx)
	if err != nil {
		log.Printf("web builder: template index: %v", err)
		return &wbTemplateIndex{docs: map[int64]*store.WebBuilderDoc{}, failed: true}
	}
	idx.gen, idx.built = gen, time.Now()
	st.idx.Store(idx)
	return idx
}

func (s *Server) wbBuildTemplateIndex(ctx context.Context) (*wbTemplateIndex, error) {
	entries, err := s.store.WebBuilder().PublishedTemplates(ctx)
	if err != nil {
		return nil, err
	}
	idx := &wbTemplateIndex{entries: entries, cands: make([]webbuilder.TemplateCandidate, len(entries)),
		lang: map[int64]string{}, group: map[int64]int64{}, docs: map[int64]*store.WebBuilderDoc{}}
	ids := make([]int64, 0, len(entries))
	for i, e := range entries {
		conds := webbuilder.ParseConditions(e.Conditions)
		for _, c := range conds {
			if c.Scope == webbuilder.ScopeInTerm {
				idx.needTerms = true
			}
		}
		idx.cands[i] = webbuilder.TemplateCandidate{ID: e.ID, Type: e.Type, Conditions: conds, Priority: e.Priority, Modified: e.ModifiedAt}
		ids = append(ids, e.ID)
	}
	if len(ids) > 0 {
		rows, err := s.store.Pool.Query(ctx, `SELECT post_id, lang, grp FROM pll_post_langs WHERE post_id = ANY($1)`, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, grp int64
			var lang string
			if err := rows.Scan(&id, &lang, &grp); err != nil {
				return nil, err
			}
			idx.lang[id], idx.group[id] = lang, grp
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

// candidates are the index's candidates for one request language. A template without a snapshot
// (an import not rendered yet) never renders, so it never competes and does not hide the next best
// one. Of each translation group only one usable member competes — the one in lang, else the one
// in the default language, else the oldest. Templates without a group always compete.
func (idx *wbTemplateIndex) candidates(lang, def string) []webbuilder.TemplateCandidate {
	rank := func(id int64) int {
		l := idx.lang[id]
		switch {
		case lang != "" && l == lang:
			return 2
		case l == "" || l == def:
			return 1
		}
		return 0
	}
	usable := func(i int) bool { return i < len(idx.entries) && idx.entries[i].HasHTML }
	best := map[int64]int64{} // group → representative id
	for i, c := range idx.cands {
		g, ok := idx.group[c.ID]
		if !ok || !usable(i) {
			continue
		}
		cur, seen := best[g]
		if !seen || rank(c.ID) > rank(cur) || (rank(c.ID) == rank(cur) && c.ID < cur) {
			best[g] = c.ID
		}
	}
	out := make([]webbuilder.TemplateCandidate, 0, len(idx.cands))
	for i, c := range idx.cands {
		if !usable(i) {
			continue
		}
		if g, ok := idx.group[c.ID]; ok && best[g] != c.ID {
			continue
		}
		out = append(out, c)
	}
	return out
}

// translation maps a winner to its published translation in lang (contract §11.3 step 5).
func (idx *wbTemplateIndex) translation(id int64, lang string) int64 {
	g, ok := idx.group[id]
	if !ok || lang == "" || idx.lang[id] == lang {
		return id
	}
	for _, e := range idx.entries {
		if e.ID != id && idx.group[e.ID] == g && idx.lang[e.ID] == lang {
			return e.ID
		}
	}
	return id
}

func (idx *wbTemplateIndex) entry(id int64) *store.WBTemplateIndexEntry {
	for i := range idx.entries {
		if idx.entries[i].ID == id {
			return &idx.entries[i]
		}
	}
	return nil
}

// doc is the published doc of template id, loaded once per index (nil when it has no snapshot).
func (idx *wbTemplateIndex) doc(ctx context.Context, s *Server, id int64) *store.WebBuilderDoc {
	idx.docMu.Lock()
	d, ok := idx.docs[id]
	idx.docMu.Unlock()
	if ok {
		return d
	}
	d, err := s.store.WebBuilder().Doc(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		d = nil
	case err != nil:
		log.Printf("web builder: template %d doc: %v", id, err)
		return nil // not cached: try again on the next request
	case d.HTML == "":
		d = nil
	}
	idx.docMu.Lock()
	idx.docs[id] = d
	idx.docMu.Unlock()
	return d
}

// wbPickTemplate is the template that fills location (webbuilder.LocationHeader, LocationMain,
// LocationFooter) on the request whose main query is q: the template forced by a
// ?wb_template_preview=<id> of a user with edit_theme_options, else the winner of the display
// conditions (contract §11.3), mapped to its translation in the request language. It returns the
// published doc (a copy; its HTML is the sanitized snapshot, not yet filled) and the template id,
// or nil, 0 when the location falls back to the theme's default. Feeds never get a template.
func (s *Server) wbPickTemplate(c *gin.Context, location string, q *siteQuery) (*store.WebBuilderDoc, int64) {
	if c == nil || q == nil || q.Feed != "" || !s.wbOn() {
		return nil, 0
	}
	if d, id, ok := s.wbTemplatePreview(c, location); ok {
		return d, id
	}
	ctx := c.Request.Context()
	idx := s.wbTemplateIndex(ctx)
	if idx.failed {
		// The parts of this response are a fallback for a passing error: never cache it, or it
		// would outlive the error (the index generation does not change, so nothing purges it).
		c.Header(cache.HeaderControl, "no-store")
		return nil, 0
	}
	if len(idx.cands) == 0 {
		return nil, 0
	}
	lang, def := "", ""
	if l := s.pllCurrent(ctx); l != nil {
		lang = l.Slug
	}
	if len(idx.group) > 0 {
		if d := s.pllDefault(ctx); d != nil {
			def = d.Slug
		}
	}
	facts := s.wbQueryFacts(c, q, idx.needTerms)
	usable := idx.candidates(lang, def)
	i := webbuilder.PickTemplate(location, usable, facts)
	if i < 0 {
		return nil, 0
	}
	id := idx.translation(usable[i].ID, lang)
	if e := idx.entry(id); e == nil || !e.HasHTML {
		id = usable[i].ID
	}
	d := idx.doc(ctx, s, id)
	if d == nil {
		return nil, 0
	}
	cp := *d
	return &cp, id
}

// wbTemplatePreview applies ?wb_template_preview=<id>: for a user with edit_theme_options the
// template (any status but trash, with a snapshot) fills its own location whatever its
// conditions, and the response is never cached. ok is false when the request does not preview a
// template of this location.
func (s *Server) wbTemplatePreview(c *gin.Context, location string) (*store.WebBuilderDoc, int64, bool) {
	raw, has := c.GetQuery(webbuilder.PreviewQuery)
	if !has {
		return nil, 0, false
	}
	u := currentUser(c)
	if u == nil || !u.Can(webbuilder.TemplateCap, nil) {
		return nil, 0, false
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	c.Header(cache.HeaderControl, "no-store")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, 0, false
	}
	ctx := c.Request.Context()
	t, err := s.store.WebBuilder().Template(ctx, id)
	if err != nil || t.Status == "trash" || t.Type == "" || webbuilder.TemplateLocation(t.Type) != location {
		return nil, 0, false
	}
	d, err := s.store.WebBuilder().Doc(ctx, id)
	if err != nil || d.HTML == "" {
		return nil, 0, false
	}
	return d, id, true
}

// wbTemplatePreviewing reports whether the request previews a template (TC2 marks such pages
// private and adds the admin bar node).
func (s *Server) wbTemplatePreviewing(c *gin.Context) bool {
	if _, has := c.GetQuery(webbuilder.PreviewQuery); !has {
		return false
	}
	u := currentUser(c)
	return u != nil && u.Can(webbuilder.TemplateCap, nil)
}

// ctxWBFacts memoizes the request's QueryFacts (three locations ask for them).
const ctxWBFacts = "wb.facts"

type wbFactsMemo struct {
	q     *siteQuery
	terms bool
	facts webbuilder.QueryFacts
}

// wbQueryFacts describes the request for the condition resolver. Terms of a singular entry are
// loaded only when some rule needs them.
func (s *Server) wbQueryFacts(c *gin.Context, q *siteQuery, needTerms bool) webbuilder.QueryFacts {
	if v, ok := c.Get(ctxWBFacts); ok {
		if m, _ := v.(*wbFactsMemo); m != nil && m.q == q && (m.terms || !needTerms) {
			return m.facts
		}
	}
	f := wbFactsOf(q)
	if needTerms && f.View == webbuilder.ViewSingular && f.PostID > 0 {
		f.Terms = map[string][]int64{}
		rows, err := s.store.Pool.Query(c.Request.Context(), `SELECT t.taxonomy, t.id FROM post_terms pt JOIN terms t ON t.id = pt.term_id
			WHERE pt.post_id = $1`, f.PostID)
		if err == nil {
			for rows.Next() {
				var tax string
				var id int64
				if rows.Scan(&tax, &id) == nil {
					f.Terms[tax] = append(f.Terms[tax], id)
				}
			}
			rows.Close()
		}
	}
	c.Set(ctxWBFacts, &wbFactsMemo{q: q, terms: needTerms, facts: f})
	return f
}

// wbFactsOf maps a siteQuery to QueryFacts (no database access).
func wbFactsOf(q *siteQuery) webbuilder.QueryFacts {
	f := webbuilder.QueryFacts{Store: q.Store != ""}
	singular := func(p *store.Post) {
		f.View, f.PostType, f.PostID, f.ParentID, f.AuthorID = webbuilder.ViewSingular, p.Type, p.ID, p.ParentID, p.AuthorID
	}
	switch q.Kind {
	case "404":
		f.View = webbuilder.View404
		return f
	case "home":
		f.View, f.Archive, f.FrontPage = webbuilder.ViewArchive, webbuilder.ArchiveHome, q.PostsPage == nil
		return f
	case "category", "tag":
		f.View, f.Archive = webbuilder.ViewArchive, webbuilder.ArchiveTerm
		if q.Term != nil {
			f.Taxonomy, f.TermID = q.Term.Taxonomy, q.Term.ID
		}
		return f
	case "author":
		f.View, f.Archive = webbuilder.ViewArchive, webbuilder.ArchiveAuthor
		if q.Author != nil {
			f.AuthorID = q.Author.ID
		}
		return f
	case "date":
		f.View, f.Archive = webbuilder.ViewArchive, webbuilder.ArchiveDate
		return f
	case "search":
		f.View, f.Archive = webbuilder.ViewArchive, webbuilder.ArchiveSearch
		return f
	case "post-type-archive":
		f.View, f.Archive, f.PostType = webbuilder.ViewArchive, webbuilder.ArchivePostType, q.PostType
		return f
	}
	switch q.Store { // the store's synthetic pages: report what the view really is
	case "product":
		f.View, f.PostType = webbuilder.ViewSingular, "product"
		if q.Post != nil {
			f.PostID = q.Post.ID // the product id
		}
		return f
	case "shop":
		f.View, f.Archive, f.PostType = webbuilder.ViewArchive, webbuilder.ArchivePostType, "product"
		return f
	case "product_cat", "product_tag", "product_attr":
		f.View, f.Archive, f.Taxonomy, f.TermID = webbuilder.ViewArchive, webbuilder.ArchiveTerm, q.StoreTax, q.StoreTerm
		return f
	}
	if q.Post != nil { // single, page, front-page and the store's own pages (cart, checkout, account)
		singular(q.Post)
		f.FrontPage = q.Kind == "front-page"
		return f
	}
	f.View = webbuilder.ViewArchive // a store listing (shop, product category)
	return f
}

// Templates are site design: every post meta capability on one (edit_post, delete_post,
// publish_post, read_post) maps to TemplateCap, so the core screens that only check edit_post
// (Quick Edit, bulk edit, revisions, trash, Pursae Multi Language ajax) cannot change them for
// users without edit_theme_options.
func init() { caps.RegisterTypeCap(webbuilder.TemplatePostType, webbuilder.TemplateCap) }

// wbEditable reports whether u may edit p in the builder: a template needs edit_theme_options
// (also what caps.MapMetaCap maps its meta caps to); any other post needs a builder post type
// and edit_post.
func (s *Server) wbEditable(u *store.User, p *store.Post) bool {
	if u == nil || p == nil {
		return false
	}
	if p.Type == webbuilder.TemplatePostType {
		return u.Can(webbuilder.TemplateCap, nil)
	}
	return s.wbEnabledType(p.Type) && u.Can("edit_post", p)
}

// wbIsTemplate reports whether p is a Theme Builder template.
func wbIsTemplate(p *store.Post) bool { return p != nil && p.Type == webbuilder.TemplatePostType }

// wbPllPostTypes are the post types the builder adds to Pursae Multi Language's translated types:
// templates, while the plugin is on.
func (s *Server) wbPllPostTypes() []string {
	if !s.wbOn() {
		return nil
	}
	return []string{webbuilder.TemplatePostType}
}

// wbThemeBuilderURL is Appearance › Theme Builder (site-relative).
func wbThemeBuilderURL() string {
	return "/wp-admin/themes?page=" + webbuilder.ThemeBuilderPage
}

// wbTemplateStatusOK: a template is published (active) or a draft (disabled); pending and
// private do not exist for templates.
func wbTemplateStatusOK(status string) bool {
	return status == webbuilder.StatusPublish || status == webbuilder.StatusDraft
}
