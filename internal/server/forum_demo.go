package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/auth"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// The "forum" section of a theme's demo/demo.json (Pursae Forum demo content):
//
//	"forum": {
//	  "title": "Campfire Community",
//	  "members": [{"ref": "mara", "display_name": "Mara Lind", "role": "forum_participant", "bio": "Woodworker."}],
//	  "forums": [{"ref": "general", "title": "General", "kind": "category", "order": 0},
//	             {"ref": "intro", "title": "Introductions", "parent": "general", "description": "Say hello.", "order": 1, "closed": false}],
//	  "topics": [{"ref": "welcome", "forum": "intro", "author": "admin", "title": "Welcome!", "content": "<p>…</p>",
//	              "sticky": true, "closed": false, "hours_ago": 96,
//	              "replies": [{"author": "mara", "content": "<p>…</p>", "hours_ago": 90}]}]
//	}
//
// Importing switches the forum on, creates the members (login demo-<ref>, an unusable random
// password, an example.invalid address) and the forums, topics and replies, each marked with
// _wpgo_demo / _wpgo_demo_ref so a re-import adds nothing and Remove Demo Content deletes exactly
// them. "author": "admin" is the user running the import.

// Demo limits.
const (
	forumDemoMaxMembers = 12
	forumDemoMaxForums  = 30
	forumDemoMaxTopics  = 50
	forumDemoMaxReplies = 300
	forumDemoMaxContent = 20 << 10
	forumDemoMaxHours   = 24 * 365 * 5
)

type forumDemo struct {
	Title   string            `json:"title"`
	Members []forumDemoMember `json:"members"`
	Forums  []forumDemoForum  `json:"forums"`
	Topics  []forumDemoTopic  `json:"topics"`
}

type forumDemoMember struct {
	Ref         string `json:"ref"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Bio         string `json:"bio"`
}

type forumDemoForum struct {
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Kind        string `json:"kind"`
	Parent      string `json:"parent"`
	Description string `json:"description"`
	Order       int    `json:"order"`
	Closed      bool   `json:"closed"`
}

type forumDemoTopic struct {
	Ref      string           `json:"ref"`
	Forum    string           `json:"forum"`
	Author   string           `json:"author"`
	Title    string           `json:"title"`
	Content  string           `json:"content"`
	Sticky   bool             `json:"sticky"`
	Closed   bool             `json:"closed"`
	HoursAgo float64          `json:"hours_ago"`
	Replies  []forumDemoReply `json:"replies"`
}

type forumDemoReply struct {
	Author   string  `json:"author"`
	Content  string  `json:"content"`
	HoursAgo float64 `json:"hours_ago"`
}

// forumDemoRefRe is a demo ref: lowercase letters, digits, - and _.
var forumDemoRefRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// forumDemoRoles are the roles a demo member may get (never one that edits content or the site).
var forumDemoRoles = map[string]bool{forumRoleParticipant: true, forumRoleModerator: true, "subscriber": true}

// forumValidateDemo returns the problems of a demo's forum section (nil when it is fine or absent).
func forumValidateDemo(raw json.RawMessage, media []demoMedia) []string {
	_ = media // the forum section has no media
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var d forumDemo
	if err := json.Unmarshal(raw, &d); err != nil {
		return []string{"forum: " + err.Error()}
	}
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, "forum."+fmt.Sprintf(format, args...)) }
	if len([]rune(d.Title)) > 80 {
		bad("title is longer than 80 characters")
	}
	if len(d.Members) > forumDemoMaxMembers {
		bad("members: at most %d", forumDemoMaxMembers)
	}
	if len(d.Forums) > forumDemoMaxForums {
		bad("forums: at most %d", forumDemoMaxForums)
	}
	if len(d.Topics) > forumDemoMaxTopics {
		bad("topics: at most %d", forumDemoMaxTopics)
	}
	members := map[string]bool{}
	for i, m := range d.Members {
		if !forumDemoRefRe.MatchString(m.Ref) || members[m.Ref] || m.Ref == "admin" {
			bad("members[%d].ref %q is invalid or listed twice", i, m.Ref)
		}
		members[m.Ref] = true
		if strings.TrimSpace(m.DisplayName) == "" || len([]rune(m.DisplayName)) > 80 {
			bad("members[%d].display_name is required (at most 80 characters)", i)
		}
		if m.Role != "" && !forumDemoRoles[m.Role] {
			bad("members[%d].role %q must be forum_participant, forum_moderator or subscriber", i, m.Role)
		}
		if len(m.Bio) > 2000 {
			bad("members[%d].bio is longer than 2000 bytes", i)
		}
	}
	kinds := map[string]string{}
	for i, f := range d.Forums {
		if !forumDemoRefRe.MatchString(f.Ref) {
			bad("forums[%d].ref %q is invalid", i, f.Ref)
		} else if _, dup := kinds[f.Ref]; dup {
			bad("forums[%d].ref %q is listed twice", i, f.Ref)
		}
		switch f.Kind {
		case "", "forum", "category":
		default:
			bad("forums[%d].kind %q must be forum or category", i, f.Kind)
		}
		kind := f.Kind
		if kind == "" {
			kind = "forum"
		}
		kinds[f.Ref] = kind
		if strings.TrimSpace(f.Title) == "" || len([]rune(f.Title)) > 200 {
			bad("forums[%d].title is required (at most 200 characters)", i)
		}
		if len(f.Description) > forumDemoMaxContent {
			bad("forums[%d].description is longer than 20 KB", i)
		}
	}
	for i, f := range d.Forums {
		if f.Parent == "" {
			continue
		}
		if kinds[f.Ref] == "category" {
			bad("forums[%d]: a category cannot have a parent", i)
			continue
		}
		pk, ok := kinds[f.Parent]
		if !ok || pk != "category" || f.Parent == f.Ref {
			bad("forums[%d].parent %q is not a category of the demo", i, f.Parent)
		}
	}
	author := func(at, a string) {
		if a != "admin" && !members[a] {
			bad("%s.author %q is neither \"admin\" nor a member of the demo", at, a)
		}
	}
	hours := func(at string, h float64) {
		if h < 0 || h > forumDemoMaxHours {
			bad("%s.hours_ago must be between 0 and %d", at, forumDemoMaxHours)
		}
	}
	topics := map[string]bool{}
	replies := 0
	for i, t := range d.Topics {
		at := fmt.Sprintf("topics[%d]", i)
		if !forumDemoRefRe.MatchString(t.Ref) || topics[t.Ref] {
			bad("%s.ref %q is invalid or listed twice", at, t.Ref)
		}
		topics[t.Ref] = true
		if k, ok := kinds[t.Forum]; !ok || k != "forum" {
			bad("%s.forum %q is not a forum of the demo (categories hold no topics)", at, t.Forum)
		}
		author(at, t.Author)
		if n := len([]rune(strings.TrimSpace(t.Title))); n < 3 || n > 120 {
			bad("%s.title needs 3 to 120 characters", at)
		}
		if strings.TrimSpace(t.Content) == "" || len(t.Content) > forumDemoMaxContent {
			bad("%s.content is required (at most 20 KB)", at)
		}
		hours(at, t.HoursAgo)
		for j, r := range t.Replies {
			rat := fmt.Sprintf("%s.replies[%d]", at, j)
			author(rat, r.Author)
			if strings.TrimSpace(r.Content) == "" || len(r.Content) > forumDemoMaxContent {
				bad("%s.content is required (at most 20 KB)", rat)
			}
			hours(rat, r.HoursAgo)
		}
		replies += len(t.Replies)
	}
	if replies > forumDemoMaxReplies {
		bad("topics: at most %d replies in all", forumDemoMaxReplies)
	}
	return problems
}

// forumDemoFind is the id of an imported demo post (type, ref), 0 when the demo has none yet.
func (s *Server) forumDemoFind(ctx context.Context, typ, slug, ref string) (int64, error) {
	var id int64
	err := s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = $1 AND meta->>'`+demoMetaKey+`' = $2 AND meta->>'`+demoMetaRef+`' = $3
		ORDER BY id LIMIT 1`, typ, slug, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// forumDemoMark marks a post as the demo's.
func (s *Server) forumDemoMark(ctx context.Context, id int64, slug, ref string) error {
	m, _ := json.Marshal(map[string]any{demoMetaKey: slug, demoMetaRef: ref})
	_, err := s.store.Pool.Exec(ctx, `UPDATE posts SET meta = coalesce(meta,'{}') || $2::jsonb WHERE id = $1`, id, m)
	return err
}

// forumDemoPrevKey is the option holding the forum title a demo replaced.
func forumDemoPrevKey(slug string) string { return "pursae_forum_demo_prev_" + slug }

// forumImportDemo imports the forum section and returns the number of posts it created. It is
// idempotent: members, forums, topics and replies already imported (same demo and ref) are reused.
func (s *Server) forumImportDemo(ctx context.Context, uid int64, slug string, raw json.RawMessage, mediaIDs map[string]int64) (int, error) {
	_ = mediaIDs
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	if problems := forumValidateDemo(raw, nil); len(problems) > 0 {
		return 0, fmt.Errorf("forum demo: %s", strings.Join(problems, "; "))
	}
	var d forumDemo
	if err := json.Unmarshal(raw, &d); err != nil {
		return 0, err
	}
	if !s.forumOn() {
		if err := s.forumActivate(ctx); err != nil {
			return 0, err
		}
	}

	// Members.
	people := map[string]int64{"admin": uid}
	for _, m := range d.Members {
		id, err := s.forumDemoMember(ctx, slug, m)
		if err != nil {
			return 0, err
		}
		people[m.Ref] = id
	}

	created := 0
	// Forums: categories first, so forums find their parent.
	forums := map[string]int64{}
	for pass := 0; pass < 2; pass++ {
		for _, f := range d.Forums {
			if (pass == 0) != (f.Parent == "") {
				continue
			}
			id, err := s.forumDemoFind(ctx, "forum", slug, f.Ref)
			if err != nil {
				return created, err
			}
			if id == 0 {
				id, err = s.forumSaveForum(ctx, forumForumInput{ParentID: forums[f.Parent], Title: f.Title, Slug: f.Slug,
					Description: f.Description, Kind: f.Kind, Closed: f.Closed, Order: f.Order}, uid)
				if err != nil {
					return created, fmt.Errorf("forum demo: forum %q: %w", f.Ref, err)
				}
				if err := s.forumDemoMark(ctx, id, slug, f.Ref); err != nil {
					return created, err
				}
				created++
			}
			forums[f.Ref] = id
		}
	}

	// Topics and their replies.
	now := time.Now()
	ago := func(h float64) time.Time {
		return now.Add(-time.Duration(h * float64(time.Hour))).Truncate(time.Second)
	}
	for _, t := range d.Topics {
		at := ago(t.HoursAgo)
		tid, err := s.forumDemoFind(ctx, "topic", slug, t.Ref)
		if err != nil {
			return created, err
		}
		if tid == 0 {
			topic, err := s.forumCreateTopic(ctx, forumNewTopic{ForumID: forums[t.Forum], AuthorID: people[t.Author], Title: t.Title,
				Content: strings.TrimSpace(wp.KsesPost(t.Content)), At: at})
			if err != nil {
				return created, fmt.Errorf("forum demo: topic %q: %w", t.Ref, err)
			}
			tid = topic.ID
			if err := s.forumDemoMark(ctx, tid, slug, t.Ref); err != nil {
				return created, err
			}
			status := "open"
			if t.Closed {
				status = "closed"
			}
			if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET sticky = $2, comment_status = $3 WHERE id = $1`, tid, t.Sticky, status); err != nil {
				return created, err
			}
			created++
		}
		last := at
		for j, r := range t.Replies {
			ref := t.Ref + "/" + strconv.Itoa(j)
			rid, err := s.forumDemoFind(ctx, "reply", slug, ref)
			if err != nil {
				return created, err
			}
			rat := ago(r.HoursAgo)
			if !rat.After(last) { // replies follow the topic and each other
				rat = last.Add(time.Minute)
			}
			if rat.After(now) {
				rat = now
			}
			last = rat
			if rid != 0 {
				continue
			}
			reply, err := s.forumCreateReply(ctx, forumNewReply{TopicID: tid, AuthorID: people[r.Author], Content: strings.TrimSpace(wp.KsesPost(r.Content)), At: rat})
			if err != nil {
				return created, fmt.Errorf("forum demo: reply %s: %w", ref, err)
			}
			if err := s.forumDemoMark(ctx, reply.ID, slug, ref); err != nil {
				return created, err
			}
			created++
		}
	}

	// Counters from SQL, every demo forum.
	for _, id := range forums {
		if err := s.forumRecount(ctx, 0, id); err != nil {
			return created, err
		}
	}

	// The forum title (the previous one comes back on Remove Demo Content).
	if t := strings.TrimSpace(d.Title); t != "" {
		st := s.forumLoad()
		if s.settings.Get(forumDemoPrevKey(slug)) == nil {
			if err := s.settings.Set(ctx, map[string]any{forumDemoPrevKey(slug): st.Title}); err != nil {
				return created, err
			}
		}
		st.Title = t
		st.GoogleClientSecret = "" // keeps the stored secret
		if err := s.forumSave(ctx, st); err != nil {
			return created, err
		}
	}
	s.forumPurge(ctx)
	return created, nil
}

// forumDemoMember finds or creates a demo member.
func (s *Server) forumDemoMember(ctx context.Context, slug string, m forumDemoMember) (int64, error) {
	var id int64
	err := s.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE meta->>'`+demoMetaKey+`' = $1 AND meta->>'`+demoMetaRef+`' = $2 ORDER BY id LIMIT 1`,
		slug, m.Ref).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	role := m.Role
	if role == "" {
		role = forumRoleParticipant
	}
	base := "demo-" + m.Ref
	login := base
	for i := 2; ; i++ {
		lt, et, err := s.store.LoginOrEmailTaken(ctx, login, login+"@example.invalid")
		if err != nil {
			return 0, err
		}
		if !lt && !et {
			break
		}
		if i > 200 {
			return 0, fmt.Errorf("forum demo: no free login for member %q", m.Ref)
		}
		login = base + "-" + strconv.Itoa(i)
	}
	hash, err := auth.HashPassword(auth.RandomToken(32))
	if err != nil {
		return 0, err
	}
	profile := map[string]string{}
	if bio := strings.TrimSpace(m.Bio); bio != "" {
		profile["description"] = wp.StripTags(bio)
	}
	id, err = s.store.CreateUser(ctx, store.NewUser{Login: login, Email: login + "@example.invalid", PasswordHash: hash,
		DisplayName: strings.TrimSpace(m.DisplayName), Role: role, Profile: profile})
	if err != nil {
		return 0, err
	}
	if err := s.forumSetUserMeta(ctx, id, map[string]any{demoMetaKey: slug, demoMetaRef: m.Ref}); err != nil {
		return 0, err
	}
	return id, nil
}

// forumRemoveDemo deletes a demo's forum posts, then the demo members who own nothing else.
//
// Demo replies and topics go (a demo topic takes every reply with it, also replies members added
// later: they would be orphans). A demo forum or category that still holds topics or forums of the
// site's own is kept and loses its demo mark, so the generic remover leaves it alone too. Members go
// only when no post of any kind still names them as author (posts.author_id is ON DELETE SET NULL,
// so deleting them would silently orphan the site's own content).
func (s *Server) forumRemoveDemo(ctx context.Context, slug string) {
	ids := func(q string, args ...any) []int64 {
		rows, err := s.store.Pool.Query(ctx, q, args...)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				out = append(out, id)
			}
		}
		return out
	}
	mark := `meta->>'` + demoMetaKey + `' = $1`
	touched := map[int64]bool{} // forums to recount
	for _, id := range ids(`SELECT DISTINCT coalesce(parent_id,0) FROM posts WHERE type = 'topic' AND `+mark, slug) {
		touched[id] = true
	}
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM posts WHERE type = 'reply' AND (`+mark+` OR parent_id IN (SELECT id FROM posts WHERE type = 'topic' AND `+mark+`))`, slug)
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM posts WHERE type = 'topic' AND `+mark, slug)
	// Forums before categories; anything still holding the site's own topics or forums stays.
	for pass := 0; pass < 2; pass++ {
		cond := `coalesce(parent_id,0) <> 0`
		if pass == 1 {
			cond = `coalesce(parent_id,0) = 0`
		}
		for _, id := range ids(`SELECT id FROM posts WHERE type = 'forum' AND `+mark+` AND `+cond, slug) {
			var held bool
			_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE parent_id = $1 AND type IN ('topic','forum'))`, id).Scan(&held)
			if held {
				_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta - '`+demoMetaKey+`' - '`+demoMetaRef+`' WHERE id = $1`, id)
				touched[id] = true
				continue
			}
			_, _ = s.store.Pool.Exec(ctx, `DELETE FROM posts WHERE id = $1 AND type = 'forum'`, id)
			delete(touched, id)
		}
	}
	for id := range touched {
		if id > 0 {
			_ = s.forumRecount(ctx, 0, id)
		}
	}
	// Members who own nothing any more.
	for _, id := range ids(`SELECT u.id FROM users u WHERE u.`+mark+` AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.author_id = u.id)
		AND NOT EXISTS (SELECT 1 FROM media m WHERE m.uploader_id = u.id)`, slug) {
		_ = s.store.DeleteUser(ctx, id, 0)
	}
	// The forum title the import replaced.
	if prev, ok := s.settings.Get(forumDemoPrevKey(slug)).(string); ok {
		st := s.forumLoad()
		st.Title = prev
		st.GoogleClientSecret = ""
		_ = s.forumSave(ctx, st)
		_ = s.settings.Set(ctx, map[string]any{forumDemoPrevKey(slug): nil})
	}
	s.forumPurge(ctx)
}
