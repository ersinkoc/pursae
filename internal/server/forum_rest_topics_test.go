package server

import (
	"net/http"
	"strings"
	"testing"
)

// POST pursae/v1/forum/topics goes through the checks of the site's new-topic form
// (forumStartTopic): closed and staff-only forums, the confirmed email address, the member's HTML
// filter, the disallowed words, the flood limit and the moderation hold.
func TestForumRESTTopicChecks(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	mc, mid := fx.member(t, "rita", "forum_participant")
	member := newWBRClient(t, mc, fx.base)
	topic := func(forum int64, title, content string) (int, map[string]any) {
		code, v, _ := member.do(http.MethodPost, "/pursae/v1/forum/topics", map[string]any{"forum_id": forum, "title": title, "content": content}, nil)
		return code, wbrObj(v)
	}

	// The member's HTML filter: no script, no event handler.
	code, v := topic(fx.forum.ID, "Hello there", `<p onclick="alert(1)">Hi <script>alert(2)</script><b>all</b></p>`)
	if code != http.StatusCreated || v["status"] != "publish" {
		t.Fatalf("topic: %d %v", code, v)
	}
	tp, err := srv.forumTopicByID(ctx, int64(v["id"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tp.Content, "<script") || strings.Contains(tp.Content, "onclick") || !strings.Contains(tp.Content, "<b>all</b>") {
		t.Errorf("content not filtered: %q", tp.Content)
	}
	// A title that is too short.
	if code, v := topic(fx.forum.ID, "Hi", "Some text here."); code != http.StatusUnprocessableEntity {
		t.Errorf("short title: %d %v", code, v)
	}

	// A closed forum, a staff-only forum.
	closed, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Announcements", ParentID: fx.cat, Closed: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if code, v := topic(closed, "In a closed forum", "Some text here."); code != http.StatusForbidden || !strings.Contains(strAny(v["message"]), "closed") {
		t.Errorf("closed forum: %d %v", code, v)
	}
	staff, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Team news", ParentID: fx.cat}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || '{"`+forumMetaStaffOnly+`": true}' WHERE id = $1`, staff); err != nil {
		t.Fatal(err)
	}
	if code, v := topic(staff, "In a staff forum", "Some text here."); code != http.StatusForbidden {
		t.Errorf("staff-only forum: %d %v", code, v)
	}

	// Disallowed words.
	if err := srv.settings.Set(ctx, map[string]any{"disallowed_keys": "casino"}); err != nil {
		t.Fatal(err)
	}
	if code, v := topic(fx.forum.ID, "Best casino deals", "Some text here."); code != http.StatusUnprocessableEntity {
		t.Errorf("disallowed words: %d %v", code, v)
	}
	_ = srv.settings.Set(ctx, map[string]any{"disallowed_keys": ""})

	// Flood limit.
	fx.settings(t, func(st *forumSettings) { st.FloodSeconds = 60 })
	if code, v := topic(fx.forum.ID, "Too fast again", "Some text here."); code != http.StatusTooManyRequests || wbrCode(v) != "forum_throttled" {
		t.Errorf("flood: %d %v", code, v)
	}
	fx.settings(t, func(st *forumSettings) { st.FloodSeconds = 0 })

	// The first post of a new member waits for a moderator.
	fx.settings(t, func(st *forumSettings) { st.ModerateFirstPost = true })
	nc, _ := fx.member(t, "nora", "forum_participant")
	code, raw, _ := newWBRClient(t, nc, fx.base).do(http.MethodPost, "/pursae/v1/forum/topics", map[string]any{"forum_id": fx.forum.ID, "title": "My first topic", "content": "Some text here."}, nil)
	if obj := wbrObj(raw); code != http.StatusCreated || obj["status"] != "pending" {
		t.Errorf("first post: %d %v", code, raw)
	}
	fx.settings(t, func(st *forumSettings) { st.ModerateFirstPost = false })

	// An unconfirmed email address.
	fx.settings(t, func(st *forumSettings) { st.RequireVerify = true })
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || '{"`+forumUMetaUnverified+`": true}' WHERE id = $1`, mid); err != nil {
		t.Fatal(err)
	}
	if code, v := topic(fx.forum.ID, "Not yet confirmed", "Some text here."); code != http.StatusForbidden || !strings.Contains(strAny(v["message"]), "confirm") {
		t.Errorf("unverified: %d %v", code, v)
	}
}
