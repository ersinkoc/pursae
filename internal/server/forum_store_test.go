package server

import (
	"context"
	"testing"
	"time"
)

// The store round trip: forums in a category, a topic with three replies, counters and last activity
// recomputed on every write, spam / trash / restore, pending items, sticky ordering, reply pages,
// member stats, forum CRUD rules.
func TestForumStoreRoundTrip(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.forumActivate(ctx); err != nil {
		t.Fatal(err)
	}

	cat, err := srv.forumSaveForum(ctx, forumForumInput{Title: "General", Kind: "category"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	fid, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Introductions", ParentID: cat, Description: "Say hello.<script>x</script>", Order: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Introductions", ParentID: cat, Order: 2}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := srv.forumByID(ctx, other); f == nil || f.Slug != "introductions-2" {
		t.Errorf("unique slug: %+v", f)
	}
	if _, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Deep", ParentID: fid}, 1); err == nil {
		t.Error("a forum under a forum (three levels)")
	}
	if _, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Sub cat", Kind: "category", ParentID: cat}, 1); err == nil {
		t.Error("a category under a category")
	}
	if _, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: cat, AuthorID: 1, Title: "No", Content: "x"}); err == nil {
		t.Error("a topic in a category")
	}

	t0 := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	topic, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Hello world!", Content: "<p>First</p>", IP: "203.0.113.9", At: t0})
	if err != nil {
		t.Fatal(err)
	}
	if topic.Slug != "hello-world" || topic.ForumTitle != "Introductions" || topic.Author == nil || topic.Author.Login != "admin" || topic.IP != "" {
		t.Errorf("topic: %+v", topic)
	}
	var replies []*forumReply
	for i := 1; i <= 3; i++ {
		r, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topic.ID, AuthorID: 1, Content: "<p>reply</p>", At: t0.Add(time.Duration(i) * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if r.Position != i || r.ForumID != fid {
			t.Errorf("reply %d: position %d forum %d", i, r.Position, r.ForumID)
		}
		replies = append(replies, r)
	}
	check := func(label string, topicReplies, forumTopics, forumReplies int, last time.Time) {
		t.Helper()
		tp, err := srv.forumTopicByID(ctx, topic.ID)
		if err != nil {
			t.Fatal(err)
		}
		f, err := srv.forumByID(ctx, fid)
		if err != nil {
			t.Fatal(err)
		}
		if tp.ReplyCount != topicReplies || f.TopicCount != forumTopics || f.ReplyCount != forumReplies {
			t.Errorf("%s: topic replies %d, forum topics %d replies %d", label, tp.ReplyCount, f.TopicCount, f.ReplyCount)
		}
		if !last.IsZero() && (!tp.LastActive.Equal(last.UTC()) || !f.LastActive.Equal(last.UTC())) {
			t.Errorf("%s: last active topic %v forum %v, want %v", label, tp.LastActive, f.LastActive, last.UTC())
		}
	}
	check("3 replies", 3, 1, 3, t0.Add(3*time.Hour))
	if f, _ := srv.forumByID(ctx, fid); f.LastTopicTitle != "Hello world!" || f.LastAuthor == nil || f.LastTopicURL == "" {
		t.Errorf("forum last topic: %+v", f)
	}
	if page := srv.forumReplyPage(ctx, replies[2], 2); page != 2 {
		t.Errorf("reply page: %d", page)
	}

	// Spam, trash and restore recount; spam rows are trash plus the flag.
	if err := srv.forumModerate(ctx, replies[2].ID, "spam", 1); err != nil {
		t.Fatal(err)
	}
	check("spam", 2, 1, 2, t0.Add(2*time.Hour))
	if spam, n, _ := srv.forumReplies(ctx, forumReplyQuery{TopicID: topic.ID, Statuses: []string{"trash"}, Spam: true}); n != 1 || !spam[0].Spam || spam[0].Status != "trash" {
		t.Errorf("spam list: %d", n)
	}
	if err := srv.forumModerate(ctx, replies[1].ID, "trash", 1); err != nil {
		t.Fatal(err)
	}
	check("trash", 1, 1, 1, t0.Add(1*time.Hour))
	if err := srv.forumModerate(ctx, replies[2].ID, "unspam", 1); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumModerate(ctx, replies[1].ID, "restore", 1); err != nil {
		t.Fatal(err)
	}
	check("restored", 3, 1, 3, t0.Add(3*time.Hour))
	if err := srv.forumModerate(ctx, replies[0].ID, "stick", 1); err == nil {
		t.Error("stick on a reply")
	}
	if err := srv.forumModerate(ctx, topic.ID, "bogus", 1); err == nil {
		t.Error("unknown op")
	}
	// Trashing the topic hides its replies from the forum counts.
	if err := srv.forumModerate(ctx, topic.ID, "trash", 1); err != nil {
		t.Fatal(err)
	}
	if f, _ := srv.forumByID(ctx, fid); f.TopicCount != 0 || f.ReplyCount != 0 || f.LastTopicID != 0 {
		t.Errorf("trashed topic still counted: %+v", f)
	}
	if err := srv.forumModerate(ctx, topic.ID, "restore", 1); err != nil {
		t.Fatal(err)
	}
	check("topic restored", 3, 1, 3, t0.Add(3*time.Hour))

	// Pending: not counted until approved.
	pending, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topic.ID, AuthorID: 1, Content: "<p>wait</p>", Pending: true})
	if err != nil {
		t.Fatal(err)
	}
	check("pending", 3, 1, 3, t0.Add(3*time.Hour))
	if err := srv.forumModerate(ctx, pending.ID, "approve", 1); err != nil {
		t.Fatal(err)
	}
	check("approved", 4, 1, 4, time.Time{})

	// Edits mark the post.
	title := "Hello again"
	if err := srv.forumUpdateContent(ctx, topic.ID, &title, "<p>Edited</p>"); err != nil {
		t.Fatal(err)
	}
	if tp, _ := srv.forumTopicByID(ctx, topic.ID); tp.Title != "Hello again" || tp.Slug != "hello-again" || tp.EditedAt == nil || tp.Content != "<p>Edited</p>" {
		t.Errorf("edit: %+v", tp)
	}

	// Sticky first, inside the forum; then by last activity.
	newer, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Newer", Content: "<p>n</p>"})
	if err != nil {
		t.Fatal(err)
	}
	list, total, err := srv.forumTopics(ctx, forumTopicQuery{ForumID: fid, StickyFirst: true})
	if err != nil || total != 2 || list[0].ID != newer.ID {
		t.Fatalf("activity order: %d %v", total, err)
	}
	if err := srv.forumModerate(ctx, topic.ID, "stick", 1); err != nil {
		t.Fatal(err)
	}
	list, _, _ = srv.forumTopics(ctx, forumTopicQuery{ForumID: fid, StickyFirst: true, WithIP: true})
	if list[0].ID != topic.ID || !list[0].Sticky || list[0].IP != "203.0.113.9" {
		t.Errorf("sticky first: %+v", list[0])
	}
	if list, _, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: fid}); list[0].ID != newer.ID {
		t.Error("sticky applied without StickyFirst")
	}
	if err := srv.forumModerate(ctx, topic.ID, "close", 1); err != nil {
		t.Fatal(err)
	}
	if tp, _ := srv.forumTopicByID(ctx, topic.ID); !tp.Closed {
		t.Error("close")
	}
	if found, n, _ := srv.forumTopics(ctx, forumTopicQuery{Search: "Newer"}); n != 1 || found[0].ID != newer.ID {
		t.Errorf("search: %d", n)
	}

	// The tree, member stats, flood and first-post helpers.
	tree, err := srv.forumList(ctx)
	if err != nil || len(tree) != 1 || tree[0].Kind != "category" || len(tree[0].Children) != 2 || tree[0].TopicCount != 2 {
		t.Fatalf("tree: %v %+v", err, tree)
	}
	if desc := string(tree[0].Children[0].Description); desc != "Say hello." {
		t.Errorf("description: %q", desc)
	}
	st, err := srv.forumUserStats(ctx, 1)
	if err != nil || st.Topics != 2 || st.Replies != 4 || st.Registered.IsZero() {
		t.Errorf("stats: %+v %v", st, err)
	}
	if at, _ := srv.forumLastPostAt(ctx, 1); time.Since(at) > time.Minute {
		t.Errorf("last post at: %v", at)
	}
	if ok, _ := srv.forumHasPublished(ctx, 1); !ok {
		t.Error("has published")
	}
	if ok, _ := srv.forumHasPublished(ctx, 999); ok {
		t.Error("has published for nobody")
	}

	// Forum CRUD rules.
	if err := srv.forumDeleteForum(ctx, fid); err == nil {
		t.Error("deleted a forum with topics")
	}
	if err := srv.forumDeleteForum(ctx, cat); err == nil {
		t.Error("deleted a category with forums")
	}
	if err := srv.forumDeleteForum(ctx, other); err != nil {
		t.Errorf("delete an empty forum: %v", err)
	}
	if _, err := srv.forumSaveForum(ctx, forumForumInput{ID: fid, Title: "Intros", Kind: "category"}, 1); err == nil {
		t.Error("a forum with topics became a category")
	}
	if _, err := srv.forumSaveForum(ctx, forumForumInput{ID: fid, Title: "Intros", ParentID: cat, Closed: true, Kind: "forum"}, 1); err != nil {
		t.Fatal(err)
	}
	if f, _ := srv.forumBySlug(ctx, "introductions"); f == nil || !f.Closed || f.TopicCount != 2 {
		t.Errorf("updated forum: %+v", f)
	}
	// An update without a Kind keeps the kind (a category stays a category).
	if _, err := srv.forumSaveForum(ctx, forumForumInput{ID: cat, Title: "General talk"}, 1); err != nil {
		t.Fatal(err)
	}
	if f, _ := srv.forumByID(ctx, cat); f == nil || f.Kind != "category" || f.Title != "General talk" || f.Slug != "general" {
		t.Errorf("category after a kind-less update: %+v", f)
	}
	// Sticky is per forum: a cross-forum list ignores StickyFirst.
	if list, _, _ := srv.forumTopics(ctx, forumTopicQuery{StickyFirst: true}); len(list) == 0 || list[0].ID != newer.ID {
		t.Error("sticky applied on a cross-forum list")
	}
	if _, err := srv.forumUserStats(ctx, 999999); err != errForumNotFound {
		t.Errorf("stats of nobody: %v", err)
	}
}
