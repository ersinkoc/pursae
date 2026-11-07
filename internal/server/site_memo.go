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

	// Pursae Cache dependencies found while building the page (cacheTags, wpgocache.go): widgets is
	// set when the page shows something only the blunt "widgets" tag tracks (a widget area, a list
	// block or shortcode in the content); deps are the narrower tags of what else it lists.
	widgets bool
	deps    map[string]bool
}

type viewMemoKey struct{}

// withViewMemo returns ctx with a fresh memo, or ctx itself when it already carries one.
func withViewMemo(ctx context.Context) context.Context {
	if viewMemoFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, viewMemoKey{}, &viewMemo{terms: map[int64][]*store.Term{}, users: map[int64]*store.User{},
		attachments: map[int64]*store.Attachment{}, avatarIDs: map[string]int64{}, deps: map[string]bool{}})
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

// attachment is GetAttachment through the request memo.
func (s *Server) attachment(ctx context.Context, id int64) (*store.Attachment, error) {
	m := viewMemoFrom(ctx)
	if m == nil {
		return s.store.GetAttachment(ctx, id)
	}
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
