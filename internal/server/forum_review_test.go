package server

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/i18n"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// forumReviewStart is a site with the forum on, a forum and a member (user id returned).
func forumReviewStart(t *testing.T) (*Server, int64, int64) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.forumActivate(ctx); err != nil {
		t.Fatal(err)
	}
	fid, err := srv.forumSaveForum(ctx, forumForumInput{Title: "General"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "member", Email: "member@example.test", PasswordHash: "x", Role: forumRoleParticipant})
	if err != nil {
		t.Fatal(err)
	}
	return srv, fid, uid
}

// The core Trash purge takes a trashed or spammed topic's replies with it: none stays published
// without a topic.
func TestForumTrashPurgeTakesReplies(t *testing.T) {
	srv, fid, uid := forumReviewStart(t)
	ctx := context.Background()
	topic, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: uid, Title: "Spam topic", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topic.ID, AuthorID: 1, Content: "reply"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.forumModerate(ctx, topic.ID, "spam", 1); err != nil {
		t.Fatal(err)
	}
	forumBackdateTrash(t, srv, topic.ID)
	if err := srv.jobHandlers()["wp_scheduled_delete"](ctx, nil); err != nil {
		t.Fatal(err)
	}
	var topics, replies int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE id = $1), count(*) FILTER (WHERE type = 'reply' AND (parent_id = $1 OR parent_id IS NULL))
		FROM posts WHERE type IN ('topic','reply')`, topic.ID).Scan(&topics, &replies)
	if topics != 0 || replies != 0 {
		t.Errorf("after the purge: topic %d, replies left %d", topics, replies)
	}
	// Other trashed posts still go through the core delete.
	p := &store.Post{Type: "post", AuthorID: 1, Status: "trash", Title: "Old"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	forumBackdateTrash(t, srv, p.ID)
	if err := srv.jobHandlers()["wp_scheduled_delete"](ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.GetPost(ctx, p.ID); err == nil {
		t.Error("a trashed post survived the purge")
	}
}

// forumBackdateTrash moves a post's updated_at past the Trash purge age (the trigger is paused, as it
// would set it back to now).
func forumBackdateTrash(t *testing.T, srv *Server, id int64) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{`ALTER TABLE posts DISABLE TRIGGER posts_updated_at`,
		`UPDATE posts SET updated_at = now() - interval '400 days' WHERE id = ` + strconv.FormatInt(id, 10),
		`ALTER TABLE posts ENABLE TRIGGER posts_updated_at`} {
		if _, err := srv.store.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

// Replies inside a spammed topic count nowhere: not in member statistics, points or first-post moderation.
func TestForumStatsSkipDeadTopics(t *testing.T) {
	srv, fid, uid := forumReviewStart(t)
	ctx := context.Background()
	other, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Someone else's", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: other.ID, AuthorID: uid, Content: "me too"}); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := srv.forumUserStats(ctx, uid); st.Replies != 4 {
		t.Fatalf("before: %d replies", st.Replies)
	}
	if err := srv.forumModerate(ctx, other.ID, "spam", 1); err != nil {
		t.Fatal(err)
	}
	if st, _ := srv.forumUserStats(ctx, uid); st.Replies != 0 {
		t.Errorf("profile counts %d replies of a spammed topic", st.Replies)
	}
	ms, err := srv.forumMemberStats(ctx, []int64{uid})
	if err != nil {
		t.Fatal(err)
	}
	if m := ms[uid]; m != nil && (m.Replies != 0 || m.Points != 0) {
		t.Errorf("member stats count a spammed topic's replies: %+v", m)
	}
	if ok, _ := srv.forumHasPublished(ctx, uid); ok {
		t.Error("replies of a spammed topic pass first-post moderation")
	}
}

// A moderator's reply link points to the page of their own view, which also lists pending replies.
func TestForumModeratorReplyAnchor(t *testing.T) {
	srv, fid, uid := forumReviewStart(t)
	ctx := context.Background()
	st := srv.forumLoad()
	st.RepliesPerPage = 5
	if err := srv.forumSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	topic, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Paged", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	for i := 0; i < 7; i++ {
		_, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topic.ID, AuthorID: uid, Content: "r", Pending: i >= 4, At: at.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topic.ID, AuthorID: 1, Content: "mod reply"})
	if err != nil {
		t.Fatal(err)
	}
	topic, _ = srv.forumTopicByID(ctx, topic.ID)
	admin, _ := srv.store.UserByID(ctx, 1)
	member, _ := srv.store.UserByID(ctx, uid)
	if got := srv.forumReplyAnchor(ctx, topic, r, admin); !strings.Contains(got, "/page/2/") {
		t.Errorf("moderator anchor: %s (the reply is 8th in their view)", got)
	}
	if got := srv.forumReplyAnchor(ctx, topic, r, member); strings.Contains(got, "/page/") {
		t.Errorf("member anchor: %s (the reply is 5th of the published ones)", got)
	}
}

// The forum type capabilities apply only while the plugin is on (a same-named type of another
// source keeps the core rules), and the type names are reserved for Pursae Custom Fields.
func TestForumTypeCapsFollowPlugin(t *testing.T) {
	srv, _, _ := forumReviewStart(t)
	ctx := context.Background()
	editor, err := srv.store.CreateUser(ctx, store.NewUser{Login: "ed", Email: "ed@example.test", PasswordHash: "x", Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	ed, _ := srv.store.UserByID(ctx, editor)
	post := &store.Post{ID: 9, Type: "topic", AuthorID: editor, Status: "publish"}
	forumPost := &store.Post{ID: 10, Type: "forum", AuthorID: editor, Status: "publish"}
	if !ed.Can("edit_post", post) || ed.Can("edit_post", forumPost) {
		t.Error("forum on: an editor (forum_moderate) edits topics, not forums")
	}
	if err := srv.forumDeactivate(ctx); err != nil {
		t.Fatal(err)
	}
	if forumCapsOn() {
		t.Fatal("type capabilities still on with the plugin off")
	}
	if !ed.Can("edit_post", forumPost) {
		t.Error("forum off: the forum type still needs manage_options")
	}
	for _, k := range []string{"forum", "topic", "reply"} {
		if !acfgoReservedTypes[k] {
			t.Errorf("%s is not reserved for Pursae Custom Fields", k)
		}
	}
}

// The posting lock serializes one member's flood check and insert.
func TestForumPostLockSerializes(t *testing.T) {
	srv, _, uid := forumReviewStart(t)
	ctx := context.Background()
	release, err := srv.forumPostLock(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		r2, err := srv.forumPostLock(ctx, uid)
		if err == nil {
			r2()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second post of the same member ran while the first held the lock")
	case <-time.After(300 * time.Millisecond):
	}
	if r3, err := srv.forumPostLock(ctx, uid+1); err != nil {
		t.Fatal(err)
	} else {
		r3() // another member is not blocked
	}
	release()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not released")
	}
}

// Unit: the relative time units go through the catalog (_n with WordPress' msgids).
func TestForumTimeDiffTranslated(t *testing.T) {
	now := time.Now()
	n := func(single, plural string, count int) string {
		m := map[string]string{"%s hour": "%s saat", "%s min": "%s dakika"}
		if v, ok := m[single]; ok {
			return v
		}
		if count == 1 {
			return single
		}
		return plural
	}
	if got := wp.HumanTimeDiffN(now.Add(-3*time.Hour), now, n); got != "3 saat" {
		t.Errorf("hours: %q", got)
	}
	if got := wp.HumanTimeDiffN(now.Add(-time.Minute), now, n); got != "1 dakika" {
		t.Errorf("one min: %q", got)
	}
	if got := wp.HumanTimeDiff(now.Add(-2*24*time.Hour), now); got != "2 days" {
		t.Errorf("English: %q", got)
	}
	if got := forumTimeDiff((*i18n.Catalog)(nil), now.Add(-time.Hour)); got != "1 hour" {
		t.Errorf("nil catalog: %q", got)
	}
}
