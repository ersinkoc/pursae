package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const forumDemoSample = `{
  "title": "Campfire Community",
  "members": [{"ref": "mara", "display_name": "Mara Lind", "role": "forum_participant", "bio": "Woodworker."},
              {"ref": "ozan", "display_name": "Ozan K.", "role": "forum_moderator"}],
  "forums": [{"ref": "general", "title": "General", "kind": "category", "order": 0},
             {"ref": "intro", "title": "Introductions", "parent": "general", "description": "Say hello.", "order": 1},
             {"ref": "shop", "title": "Workshop", "parent": "general", "order": 2, "closed": true}],
  "topics": [{"ref": "welcome", "forum": "intro", "author": "admin", "title": "Welcome!", "content": "<p>Glad you are here.</p><script>x</script>",
              "sticky": true, "hours_ago": 96,
              "replies": [{"author": "mara", "content": "<p>Hi all</p>", "hours_ago": 90},
                          {"author": "ozan", "content": "<p>Welcome, Mara.</p>", "hours_ago": 100}]},
             {"ref": "tools", "forum": "shop", "author": "mara", "title": "Favourite chisels", "content": "<p>Which ones?</p>", "closed": true, "hours_ago": 10}]
}`

func TestForumValidateDemo(t *testing.T) {
	if p := forumValidateDemo(nil, nil); p != nil {
		t.Errorf("absent section: %v", p)
	}
	if p := forumValidateDemo(json.RawMessage(forumDemoSample), nil); len(p) != 0 {
		t.Fatalf("sample: %v", p)
	}
	cases := map[string]string{
		"bad ref":             `{"forums":[{"ref":"Bad Ref","title":"X"}]}`,
		"duplicate member":    `{"members":[{"ref":"a","display_name":"A"},{"ref":"a","display_name":"B"}]}`,
		"admin role":          `{"members":[{"ref":"a","display_name":"A","role":"administrator"}]}`,
		"parent not cat":      `{"forums":[{"ref":"a","title":"A"},{"ref":"b","title":"B","parent":"a"}]}`,
		"category parent":     `{"forums":[{"ref":"a","title":"A","kind":"category"},{"ref":"b","title":"B","kind":"category","parent":"a"}]}`,
		"unknown parent":      `{"forums":[{"ref":"b","title":"B","parent":"nope"}]}`,
		"topic in category":   `{"forums":[{"ref":"a","title":"A","kind":"category"}],"topics":[{"ref":"t","forum":"a","author":"admin","title":"Topic","content":"x"}]}`,
		"unknown author":      `{"forums":[{"ref":"a","title":"A"}],"topics":[{"ref":"t","forum":"a","author":"ghost","title":"Topic","content":"x"}]}`,
		"reply author":        `{"forums":[{"ref":"a","title":"A"}],"topics":[{"ref":"t","forum":"a","author":"admin","title":"Topic","content":"x","replies":[{"author":"ghost","content":"y"}]}]}`,
		"short title":         `{"forums":[{"ref":"a","title":"A"}],"topics":[{"ref":"t","forum":"a","author":"admin","title":"x","content":"x"}]}`,
		"negative hours":      `{"forums":[{"ref":"a","title":"A"}],"topics":[{"ref":"t","forum":"a","author":"admin","title":"Topic","content":"x","hours_ago":-1}]}`,
		"huge content":        `{"forums":[{"ref":"a","title":"A"}],"topics":[{"ref":"t","forum":"a","author":"admin","title":"Topic","content":"` + strings.Repeat("x", 21000) + `"}]}`,
		"not json":            `[1,2]`,
		"bad kind":            `{"forums":[{"ref":"a","title":"A","kind":"board"}]}`,
		"member called admin": `{"members":[{"ref":"admin","display_name":"A"}]}`,
	}
	for name, raw := range cases {
		if p := forumValidateDemo(json.RawMessage(raw), nil); len(p) == 0 {
			t.Errorf("%s: no problem reported", name)
		}
	}
	// Limits.
	var b strings.Builder
	b.WriteString(`{"members":[`)
	for i := 0; i < 13; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"ref":"m` + string(rune('a'+i)) + `","display_name":"M"}`)
	}
	b.WriteString(`]}`)
	if p := forumValidateDemo(json.RawMessage(b.String()), nil); len(p) == 0 {
		t.Error("13 members accepted")
	}
}

// Import creates members, forums, topics and replies, is idempotent, and removal deletes exactly the
// demo's posts and members.
func TestForumDemoImportRemove(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if srv.forumOn() {
		t.Fatal("forum on before the import")
	}
	n, err := srv.forumImportDemo(ctx, 1, "campfire", json.RawMessage(forumDemoSample), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3+2+2 { // 3 forums, 2 topics, 2 replies
		t.Errorf("created %d posts", n)
	}
	if !srv.forumOn() {
		t.Error("the import did not switch the forum on")
	}
	if st := srv.forumLoad(); st.Title != "Campfire Community" {
		t.Errorf("title: %q", st.Title)
	}
	tree, _ := srv.forumList(ctx)
	if len(tree) != 1 || tree[0].Kind != "category" || len(tree[0].Children) != 2 {
		t.Fatalf("tree: %+v", tree)
	}
	intro, shop := tree[0].Children[0], tree[0].Children[1]
	if intro.Title != "Introductions" || intro.TopicCount != 1 || intro.ReplyCount != 2 || !shop.Closed {
		t.Errorf("forums: %+v / %+v", intro, shop)
	}
	topics, _, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: intro.ID})
	if len(topics) != 1 || !topics[0].Sticky || strings.Contains(topics[0].Content, "<script") || topics[0].Author == nil || topics[0].Author.ID != 1 {
		t.Fatalf("topic: %+v", topics)
	}
	replies, _, _ := srv.forumReplies(ctx, forumReplyQuery{TopicID: topics[0].ID})
	if len(replies) != 2 || !replies[0].CreatedAt.After(topics[0].CreatedAt) || !replies[1].CreatedAt.After(replies[0].CreatedAt) {
		t.Errorf("replies order / times: %+v", replies)
	}
	if replies[0].Author == nil || replies[0].Author.DisplayName != "Mara Lind" {
		t.Errorf("reply author: %+v", replies[0].Author)
	}
	shopTopics, _, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: shop.ID})
	if len(shopTopics) != 1 || !shopTopics[0].Closed {
		t.Errorf("closed topic: %+v", shopTopics)
	}
	mara, err := srv.store.UserByLoginOrEmail(ctx, "demo-mara")
	if err != nil {
		t.Fatal(err)
	}
	if mara.Email != "demo-mara@example.invalid" || !mara.Can(capForumParticipate, nil) || mara.Can("edit_posts", nil) || mara.Profile["description"] != "Woodworker." {
		t.Errorf("member: %+v", mara)
	}
	if ozan, err := srv.store.UserByLoginOrEmail(ctx, "demo-ozan"); err != nil || !ozan.Can(capForumModerate, nil) {
		t.Errorf("moderator member: %v", err)
	}

	// Re-import adds nothing.
	if n, err := srv.forumImportDemo(ctx, 1, "campfire", json.RawMessage(forumDemoSample), nil); err != nil || n != 0 {
		t.Fatalf("re-import: %d %v", n, err)
	}
	var users int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE login LIKE 'demo-%'`).Scan(&users)
	if users != 2 {
		t.Errorf("members after re-import: %d", users)
	}
	// Another demo with the same refs gets its own members.
	if _, err := srv.forumImportDemo(ctx, 1, "devboard", json.RawMessage(`{"members":[{"ref":"mara","display_name":"Mara"}]}`), nil); err != nil {
		t.Fatal(err)
	}
	if u, err := srv.store.UserByLoginOrEmail(ctx, "demo-mara-2"); err != nil || u == nil {
		t.Errorf("second demo's member: %v", err)
	}

	// The site's own content: a topic in a demo forum keeps that forum (and its category); a
	// moderator who also wrote a post of their own is kept.
	own, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: shop.ID, AuthorID: 1, Title: "My own topic", Content: "<p>Mine</p>"})
	if err != nil {
		t.Fatal(err)
	}
	ozan, _ := srv.store.UserByLoginOrEmail(ctx, "demo-ozan")
	if _, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: shop.ID, AuthorID: ozan.ID, Title: "Ozan writes", Content: "<p>Real post</p>"}); err != nil {
		t.Fatal(err)
	}

	srv.forumRemoveDemo(ctx, "campfire")
	var demoPosts int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE meta->>'_wpgo_demo' = 'campfire'`).Scan(&demoPosts)
	if demoPosts != 0 {
		t.Errorf("demo posts left: %d", demoPosts)
	}
	if _, err := srv.forumByID(ctx, intro.ID); err == nil {
		t.Error("the empty demo forum is still there")
	}
	kept, err := srv.forumByID(ctx, shop.ID)
	if err != nil || kept.TopicCount != 2 {
		t.Errorf("forum with the site's topics: %+v %v", kept, err)
	}
	if _, err := srv.forumByID(ctx, tree[0].ID); err != nil {
		t.Error("the category of a kept forum was deleted")
	}
	if _, err := srv.forumTopicByID(ctx, own.ID); err != nil {
		t.Error("the site's own topic was deleted")
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "demo-mara"); err == nil {
		t.Error("demo member mara is still there")
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "demo-ozan"); err != nil {
		t.Error("demo member ozan, author of a real post, was deleted")
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "demo-mara-2"); err != nil {
		t.Error("another demo's member was deleted")
	}
	if st := srv.forumLoad(); st.Title != "Forums" {
		t.Errorf("title after removal: %q", st.Title)
	}
}
