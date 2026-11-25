package server

import (
	"context"
	"sort"
	"strings"
	"sync"

	"wpgo/internal/store"
)

// viewMemo is a request-scoped cache for the data a front-end render reads per post: terms, authors
// (display fields only, no capabilities), attachments with their sizes, avatars and is_categorized_blog.
// A loop prefetches its posts in a few queries (prefetchPosts); anything else is loaded once on first
// use. Without a memo in the context every accessor queries the store directly.
type viewMemo struct {
	mu          sync.Mutex
	terms       map[int64][]*store.Term // post id → terms of every taxonomy, ordered by name
	users       map[int64]*store.User   // nil: no such user
	attachments map[int64]*store.Attachment
	avatarIDs   map[string]int64 // lower-cased email → avatar attachment id (0: none)
	categorized *bool
	ancestors   map[int64][]*store.Post // parent id → that parent's chain, root first (no content); nil: not found
	catPaths    map[int64]string        // parent term id → "grandparent/parent/" slug prefix of its children's paths

	// Pursae Cache dependencies found while building the page (cacheTags, wpgocache.go): widgets is
	// set when the page shows something only the blunt "widgets" tag tracks (a widget area, a list
	// block or shortcode in the content); deps are the narrower tags of what else it lists.
	widgets bool
	deps    map[string]bool

	thumbSeen bool // a post thumbnail was printed (thumbnailLoading)
}

type viewMemoKey struct{}

// withViewMemo returns ctx with a fresh memo, or ctx itself when it already carries one.
func withViewMemo(ctx context.Context) context.Context {
	if viewMemoFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, viewMemoKey{}, &viewMemo{terms: map[int64][]*store.Term{}, users: map[int64]*store.User{},
		attachments: map[int64]*store.Attachment{}, avatarIDs: map[string]int64{}, deps: map[string]bool{},
		ancestors: map[int64][]*store.Post{}, catPaths: map[int64]string{}})
}

func viewMemoFrom(ctx context.Context) *viewMemo {
	if ctx == nil {
		return nil
	}
	m, _ := ctx.Value(viewMemoKey{}).(*viewMemo)
	return m
}

// prefetchPosts loads what rendering the posts needs in one query per kind: their terms, authors,
// featured images and the authors' avatars.
func (s *Server) prefetchPosts(ctx context.Context, posts []*store.Post) {
	m := viewMemoFrom(ctx)
	if m == nil || len(posts) == 0 {
		return
	}
	m.mu.Lock()
	var postIDs, userIDs, mediaIDs []int64
	seenU, seenM := map[int64]bool{}, map[int64]bool{}
	for _, p := range posts {
		if _, ok := m.terms[p.ID]; !ok {
			postIDs = append(postIDs, p.ID)
		}
		if _, ok := m.users[p.AuthorID]; !ok && p.AuthorID != 0 && !seenU[p.AuthorID] {
			seenU[p.AuthorID] = true
			userIDs = append(userIDs, p.AuthorID)
		}
		if _, ok := m.attachments[p.FeaturedMediaID]; !ok && p.FeaturedMediaID != 0 && !seenM[p.FeaturedMediaID] {
			seenM[p.FeaturedMediaID] = true
			mediaIDs = append(mediaIDs, p.FeaturedMediaID)
		}
	}
	m.mu.Unlock()

	if terms, err := s.store.TermsForPosts(ctx, postIDs, ""); err == nil {
		m.mu.Lock()
		for _, id := range postIDs {
			m.terms[id] = terms[id]
		}
		m.mu.Unlock()
	}
	if users, err := s.store.UserNames(ctx, userIDs); err == nil {
		m.mu.Lock()
		for _, id := range userIDs {
			u := users[id]
			m.users[id] = u
			if u != nil {
				email := strings.ToLower(strings.TrimSpace(u.Email))
				if _, ok := m.avatarIDs[email]; !ok && email != "" {
					id := avatarIDOf(u)
					m.avatarIDs[email] = id
					if _, ok := m.attachments[id]; !ok && id != 0 && !seenM[id] {
						seenM[id] = true
						mediaIDs = append(mediaIDs, id)
					}
				}
			}
		}
		m.mu.Unlock()
	}
	if atts, err := s.store.GetAttachments(ctx, mediaIDs); err == nil {
		m.mu.Lock()
		for _, id := range mediaIDs {
			m.attachments[id] = atts[id]
		}
		m.mu.Unlock()
	}
}

// avatarIDOf is the user's avatar attachment (users.meta.avatar_id), as avatarFor's query reads it.
func avatarIDOf(u *store.User) int64 {
	switch v := u.Meta["avatar_id"].(type) {
	case float64:
		return int64(v)
	case string:
		var n int64
		for _, r := range v {
			if r < '0' || r > '9' {
				return 0
			}
			n = n*10 + int64(r-'0')
		}
		return n
	}
	return 0
}

// postTerms is get_the_terms(): the post's terms of a taxonomy, ordered by name.
func (s *Server) postTerms(ctx context.Context, postID int64, taxonomy string) []*store.Term {
	m := viewMemoFrom(ctx)
	if m == nil {
		ts, _ := s.store.TermsForPosts(ctx, []int64{postID}, taxonomy)
		return ts[postID]
	}
	m.mu.Lock()
	all, ok := m.terms[postID]
	m.mu.Unlock()
	if !ok {
		ts, err := s.store.TermsForPosts(ctx, []int64{postID}, "")
		if err != nil {
			return nil
		}
		all = ts[postID]
		m.mu.Lock()
		m.terms[postID] = all
		m.mu.Unlock()
	}
	var out []*store.Term
	for _, t := range all {
		if t.Taxonomy == taxonomy {
			out = append(out, t)
		}
	}
	return out
}

// displayUser is a user for display (name, nicename, email, profile, meta) without capabilities.
func (s *Server) displayUser(ctx context.Context, id int64) *store.User {
	if id == 0 {
		return nil
	}
	m := viewMemoFrom(ctx)
	if m != nil {
		m.mu.Lock()
		u, ok := m.users[id]
		m.mu.Unlock()
		if ok {
			return u
		}
	}
	users, err := s.store.UserNames(ctx, []int64{id})
	if err != nil {
		return nil
	}
	u := users[id]
	if m != nil {
		m.mu.Lock()
		m.users[id] = u
		m.mu.Unlock()
	}
	return u
}

// prefetchUsers loads the display users of ids into the request memo in one query (displayUser then
// answers from it).
func (s *Server) prefetchUsers(ctx context.Context, ids []int64) {
	m := viewMemoFrom(ctx)
	if m == nil {
		return
	}
	var need []int64
	seen := map[int64]bool{}
	m.mu.Lock()
	for _, id := range ids {
		if _, ok := m.users[id]; ok || id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		need = append(need, id)
	}
	m.mu.Unlock()
	if len(need) == 0 {
		return
	}
	users, err := s.store.UserNames(ctx, need)
	if err != nil {
		return
	}
	m.mu.Lock()
	for _, id := range need {
		m.users[id] = users[id]
	}
	m.mu.Unlock()
}

// ancestors is store.Ancestors (the parent chain, root first) through the request memo, keyed by the
// parent: sibling pages in a menu or list share one walk, and a walk stops at a parent already known.
// The posts carry no excerpt or content (permalinks read slugs, breadcrumbs titles and ids). A chain
// whose walk fails is nil, as links.Builder.Ancestors gets it without a memo.
func (s *Server) ancestors(ctx context.Context, p *store.Post) []*store.Post {
	if p.ParentID == 0 {
		return nil
	}
	m := viewMemoFrom(ctx)
	if m == nil {
		a, _ := s.store.Ancestors(ctx, p)
		return a
	}
	m.mu.Lock()
	a, ok := m.ancestors[p.ParentID]
	m.mu.Unlock()
	if ok {
		return a
	}
	var chain []*store.Post
	cur := p
	for i := 0; cur.ParentID != 0 && i < 20; i++ {
		m.mu.Lock()
		known, ok := m.ancestors[cur.ParentID]
		m.mu.Unlock()
		if ok {
			if known == nil { // that walk failed: so does this one
				chain = nil
			} else {
				chain = append(append([]*store.Post{}, known...), chain...)
				if len(chain) > 20 { // store.Ancestors stops after 20 levels: keep the nearest
					chain = chain[len(chain)-20:]
				}
			}
			break
		}
		parent, err := s.store.GetPostNoContent(ctx, cur.ParentID)
		if err != nil {
			chain = nil
			break
		}
		chain = append([]*store.Post{parent}, chain...)
		cur = parent
	}
	chain = chain[:len(chain):len(chain)] // a caller appending never writes into the memo
	m.mu.Lock()
	m.ancestors[p.ParentID] = chain
	m.mu.Unlock()
	return chain
}

// prefetchAncestors fills the request memo with the parent chains of posts, loading the missing
// ancestors one depth level per query (a menu of child pages costs one query, not one per parent).
// Without a memo, or if a query fails, ancestors walks lazily as before.
func (s *Server) prefetchAncestors(ctx context.Context, posts []*store.Post) {
	m := viewMemoFrom(ctx)
	if m == nil || len(posts) == 0 {
		return
	}
	known := map[int64]*store.Post{}
	var want []*store.Post
	m.mu.Lock()
	for _, p := range posts {
		known[p.ID] = p
		if _, ok := m.ancestors[p.ParentID]; !ok && p.ParentID != 0 {
			want = append(want, p)
		}
	}
	m.mu.Unlock()
	if len(want) == 0 {
		return
	}
	tried := map[int64]bool{}
	for round := 0; round < 20; round++ {
		var need []int64
		for _, p := range known {
			if id := p.ParentID; id != 0 && known[id] == nil && !tried[id] {
				tried[id] = true
				need = append(need, id)
			}
		}
		if len(need) == 0 {
			break
		}
		got, err := s.store.PostsByIDsNoContent(ctx, need)
		if err != nil {
			return
		}
		for id, p := range got {
			known[id] = p
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range want {
		if _, ok := m.ancestors[p.ParentID]; ok {
			continue
		}
		// store.Ancestors' walk: at most 20 levels, nil when a parent does not exist.
		var chain []*store.Post
		cur := p
		for i := 0; cur.ParentID != 0 && i < 20; i++ {
			par := known[cur.ParentID]
			if par == nil {
				if !tried[cur.ParentID] {
					return // not loaded (the round limit): leave the rest to the lazy walk
				}
				chain = nil
				break
			}
			chain = append([]*store.Post{par}, chain...)
			cur = par
		}
		m.ancestors[p.ParentID] = chain[:len(chain):len(chain)]
	}
}

// categoryPath is the term's parent/child slug path (links.Builder.CategoryPath), with the prefix of
// each parent remembered in the request memo: a categories list resolves every parent once.
func (s *Server) categoryPath(ctx context.Context, t *store.Term) string {
	if t.ParentID == 0 {
		return t.Slug
	}
	m := viewMemoFrom(ctx)
	if m != nil {
		m.mu.Lock()
		prefix, ok := m.catPaths[t.ParentID]
		m.mu.Unlock()
		if ok {
			return prefix + t.Slug
		}
	}
	prefix := ""
	cur := t
	for i := 0; cur.ParentID != 0 && i < 20; i++ {
		parent, err := s.store.GetTerm(ctx, cur.ParentID)
		if err != nil {
			break
		}
		prefix = parent.Slug + "/" + prefix
		cur = parent
	}
	if m != nil {
		m.mu.Lock()
		m.catPaths[t.ParentID] = prefix
		m.mu.Unlock()
	}
	return prefix + t.Slug
}

// attachment is GetAttachment through the request memo. The page then depends on the media item
// (Pursae Cache: editing or deleting it purges "media:<id>", purgeAttachment).
func (s *Server) attachment(ctx context.Context, id int64) (*store.Attachment, error) {
	m := viewMemoFrom(ctx)
	if m == nil {
		return s.store.GetAttachment(ctx, id)
	}
	cacheDep(ctx, "media:"+itoa64(id))
	m.mu.Lock()
	a, ok := m.attachments[id]
	m.mu.Unlock()
	if !ok {
		var err error
		a, err = s.store.GetAttachment(ctx, id)
		if err != nil && err != store.ErrNotFound {
			return nil, err
		}
		m.mu.Lock()
		m.attachments[id] = a
		m.mu.Unlock()
	}
	if a == nil {
		return nil, store.ErrNotFound
	}
	return a, nil
}

// avatarID is the avatar attachment of the user with that (lower-cased, trimmed) email.
func (s *Server) avatarID(ctx context.Context, email string) int64 {
	m := viewMemoFrom(ctx)
	if m != nil {
		m.mu.Lock()
		id, ok := m.avatarIDs[email]
		m.mu.Unlock()
		if ok {
			return id
		}
	}
	var id int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce((meta->>'avatar_id')::bigint, 0) FROM users WHERE lower(email) = $1`, email).Scan(&id)
	if m != nil {
		m.mu.Lock()
		m.avatarIDs[email] = id
		m.mu.Unlock()
	}
	return id
}

// prefetchAvatars loads the avatars of the given emails into the request memo with one query for the
// users and one for their images, so a comment list does not look each commenter up on its own.
func (s *Server) prefetchAvatars(ctx context.Context, emails []string) {
	m := viewMemoFrom(ctx)
	if m == nil {
		return
	}
	var need []string
	seen := map[string]bool{}
	m.mu.Lock()
	for _, e := range emails {
		e = strings.ToLower(strings.TrimSpace(e))
		if _, ok := m.avatarIDs[e]; ok || e == "" || seen[e] {
			continue
		}
		seen[e] = true
		need = append(need, e)
	}
	m.mu.Unlock()
	if len(need) == 0 {
		return
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT lower(email), coalesce((meta->>'avatar_id')::bigint, 0) FROM users WHERE lower(email) = ANY($1)`, need)
	if err != nil {
		return
	}
	found := map[string]int64{}
	for rows.Next() {
		var e string
		var id int64
		if rows.Scan(&e, &id) == nil {
			found[e] = id
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return
	}
	var ids []int64
	m.mu.Lock()
	for _, e := range need {
		m.avatarIDs[e] = found[e]
		if id := found[e]; id != 0 {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	s.attachmentsByIDs(ctx, ids)
}

// cacheDep records cache tags the page depends on (purging one of them drops the page).
func cacheDep(ctx context.Context, tags ...string) {
	if m := viewMemoFrom(ctx); m != nil {
		m.mu.Lock()
		for _, t := range tags {
			m.deps[t] = true
		}
		m.mu.Unlock()
	}
}

// cacheDepWidgets marks the page as showing site-wide lists only the "widgets" tag tracks.
func cacheDepWidgets(ctx context.Context) {
	if m := viewMemoFrom(ctx); m != nil {
		m.mu.Lock()
		m.widgets = true
		m.mu.Unlock()
	}
}

// listBlockMarkers are the blocks (and the shortcode opener) whose output lists other posts, terms,
// comments or pages; content with one of them depends on "widgets", like a sidebar does.
var listBlockMarkers = []string{"<!-- wp:latest-posts", "<!-- wp:latest-comments", "<!-- wp:archives", "<!-- wp:categories",
	"<!-- wp:calendar", "<!-- wp:tag-cloud", "<!-- wp:page-list", "<!-- wp:query", "<!-- wp:navigation", "<!-- wp:block ",
	"<!-- wp:rss", "<!-- wp:widget-group", "<!-- wp:legacy-widget"}

// contentListsOthers reports whether post content may render lists of other objects: a list block or
// a shortcode (plugins' shortcodes can print anything).
func contentListsOthers(content string) bool {
	for _, m := range listBlockMarkers {
		if strings.Contains(content, m) {
			return true
		}
	}
	for i := strings.IndexByte(content, '['); i >= 0 && i+1 < len(content); {
		if c := content[i+1]; c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return true
		}
		j := strings.IndexByte(content[i+1:], '[')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return false
}

// pageCacheDeps returns whether the page needs the "widgets" tag and its other dependency tags. A page
// built without a memo (or with Pursae Multi Language, whose switchers list translations) keeps "widgets".
func (s *Server) pageCacheDeps(ctx context.Context) (widgets bool, deps []string) {
	m := viewMemoFrom(ctx)
	if m == nil || s.golylangOn() {
		return true, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for t := range m.deps {
		deps = append(deps, t)
	}
	sort.Strings(deps)
	return m.widgets, deps
}

// attachmentsByIDs loads attachments with their sizes in one query (through the request memo when
// there is one); missing ids are absent from the map.
func (s *Server) attachmentsByIDs(ctx context.Context, ids []int64) map[int64]*store.Attachment {
	out := map[int64]*store.Attachment{}
	m := viewMemoFrom(ctx)
	var need []int64
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		cacheDep(ctx, "media:"+itoa64(id)) // Pursae Cache: the page shows this media item
		if m != nil {
			m.mu.Lock()
			a, ok := m.attachments[id]
			m.mu.Unlock()
			if ok {
				if a != nil {
					out[id] = a
				}
				continue
			}
		}
		need = append(need, id)
	}
	got, err := s.store.GetAttachments(ctx, need)
	if err != nil {
		return out
	}
	for _, id := range need {
		a := got[id]
		if a != nil {
			out[id] = a
		}
		if m != nil {
			m.mu.Lock()
			m.attachments[id] = a
			m.mu.Unlock()
		}
	}
	return out
}
