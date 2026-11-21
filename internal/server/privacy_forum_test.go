package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

// Erasing a member's personal data anonymizes their forum presence: the topics and replies stay
// published (threads keep working) without author or IP, likes they gave stay anonymous, and no
// forum table links anything to them any more. No other member's points, likes or data change, and
// every view renders the anonymous posts as "Deleted user".
func TestPrivacyForumAnonymize(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	forumActivateLink(t, c, ts.URL)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := srv.store.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := srv.store.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	people := map[string]int64{}
	for _, login := range []string{"ada", "bob", "carol", "dan", "eve"} {
		var id int64
		if err := srv.store.Pool.QueryRow(ctx, `INSERT INTO users (login, email, password_hash, nicename, display_name, profile)
			VALUES ($1, $1 || '@example.test', '', $1, initcap($1), jsonb_build_object('description', initcap($1) || ' bio')) RETURNING id`, login).Scan(&id); err != nil {
			t.Fatal(err)
		}
		people[login] = id
	}
	ada, bob, carol, dan, eve := people["ada"], people["bob"], people["carol"], people["dan"], people["eve"]
	member := func(id int64) *store.User { return &store.User{ID: id} }

	fid, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Lobby", Design: &forumDesignInput{Moderators: []int64{ada, bob}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-2 * time.Hour)
	topic := func(author int64, title string) *forumTopic {
		t.Helper()
		tp, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: author, Title: title, Content: "<p>" + title + " body</p>",
			IP: fmt.Sprintf("192.0.2.%d", author), At: at})
		if err != nil {
			t.Fatal(err)
		}
		return tp
	}
	reply := func(author, topicID int64, text string) *forumReply {
		t.Helper()
		at = at.Add(time.Minute)
		r, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topicID, AuthorID: author, Content: "<p>" + text + "</p>",
			IP: fmt.Sprintf("198.51.100.%d", author), At: at})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	adaTopic := topic(ada, "Ada asks")
	bobTopic := topic(bob, "Bob asks")
	carolAnswer := reply(carol, adaTopic.ID, "Carol answers Ada")
	adaReply := reply(ada, bobTopic.ID, "Ada answers Bob")
	bobReply := reply(bob, bobTopic.ID, "Bob thanks")
	adaLast := reply(ada, adaTopic.ID, "Ada closes her thread") // Ada is the last poster of her topic and of the forum

	// Ada accepted Carol's answer (Carol earns the solution points); Ada's own reply is Bob's solution.
	if err := srv.forumDemoSolve(ctx, adaTopic.ID, carolAnswer.ID, ada, carol, at); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumDemoSolve(ctx, bobTopic.ID, adaReply.ID, bob, ada, at); err != nil {
		t.Fatal(err)
	}
	// Likes: Ada likes Bob's topic and Carol's answer; others like Ada's posts and each other's.
	for _, l := range []struct {
		who  int64
		post int64
	}{{ada, bobTopic.ID}, {ada, carolAnswer.ID}, {dan, bobTopic.ID}, {bob, adaTopic.ID}, {carol, adaReply.ID}, {dan, bobReply.ID}} {
		if _, _, err := srv.forumSetLike(ctx, member(l.who), l.post, true); err != nil {
			t.Fatal(err)
		}
	}
	// An earlier erasure already left an anonymous like on Bob's topic: Ada's like there must merge
	// into its ledger row (one event per kind, post and actor).
	exec(`INSERT INTO forum_likes (post_id, user_id) VALUES ($1, NULL)`, bobTopic.ID)
	exec(`INSERT INTO forum_points (user_id, kind, points, post_id, actor_id) VALUES ($1, 'like', 1, $2, NULL)`, bob, bobTopic.ID)
	// Ada, as an administrator, adjusted Dan's points; Eve adjusted Ada's.
	if err := srv.forumAdjustPoints(ctx, dan, ada, 7, "Helpful"); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumAdjustPoints(ctx, ada, eve, 3, "Welcome"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct{ who, topic int64 }{{ada, bobTopic.ID}, {bob, bobTopic.ID}, {carol, adaTopic.ID}} {
		if err := srv.forumSubscribe(ctx, s.who, s.topic, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.forumReport(ctx, member(ada), bobReply.ID, "abuse", "Ada finds this rude"); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumReport(ctx, member(bob), adaReply.ID, "spam", "Bob finds this spammy"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE forum_reports SET status = 'resolved', resolved_by = $1, resolved_at = now() WHERE user_id = $2`, ada, bob)
	// Ada moderated Bob's reply last; she edited her topic in the post editor (a revision); Bob edited his.
	exec(`UPDATE posts SET meta = meta || jsonb_build_object('_forum_mod', jsonb_build_object('op', 'approve', 'by', $1::bigint, 'at', 'x')) WHERE id = $2`, ada, bobReply.ID)
	exec(`INSERT INTO post_revisions (post_id, author_id, title, content) VALUES ($1, $2, 'Ada asks', 'old'), ($3, $4, 'Bob asks', 'old')`,
		adaTopic.ID, ada, bobTopic.ID, bob)
	// Ada's forum profile and sign-in; Bob has the same keys.
	for _, id := range []int64{ada, bob} {
		exec(`UPDATE users SET meta = coalesce(meta, '{}') || jsonb_build_object('`+forumUMetaDraft+`', 'draft', '`+forumUMetaRules+`', '2026-01-01T00:00:00Z',
			'`+forumUMetaDigest+`', true, '`+forumUMetaBadges+`', '["helper"]', '`+forumUMetaAvatar+`', 990000 + $1::bigint, 'avatar_id', 990000 + $1::bigint,
			'`+forumUMetaGoogleSub+`', 'sub-' || $1::text, '`+forumUMetaGoogleEmail+`', 'g' || $1::text || '@gmail.test') WHERE id = $1`, id)
	}

	// What must not change: everyone else's points (totals, likes received, solutions, the dated
	// ledger), every post's like count, the number of posts.
	others := []int64{bob, carol, dan, eve}
	type pts struct{ points, likes, solutions, bonus, ledger int }
	snapshot := func() map[int64]pts {
		t.Helper()
		authors, err := srv.forumAuthors(ctx, others)
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]pts{}
		for _, id := range others {
			a := authors[id]
			out[id] = pts{a.Points, a.LikesReceived, a.Solutions, a.Bonus,
				count(`SELECT coalesce(sum(points), 0)::int FROM forum_points WHERE user_id = $1 AND created_at > now() - interval '7 days'`, id)}
		}
		return out
	}
	allPosts := []int64{adaTopic.ID, bobTopic.ID, carolAnswer.ID, adaReply.ID, bobReply.ID, adaLast.ID}
	before, likesBefore := snapshot(), srv.forumLikeCounts(ctx, allPosts)
	if before[carol].solutions != 1 || before[bob].likes != 4 || before[dan].bonus != 7 {
		t.Fatalf("fixture: %+v", before)
	}
	postsBefore := count(`SELECT count(*) FROM posts WHERE type IN ('topic', 'reply') AND status = 'publish'`)
	bobLedgerBefore := count(`SELECT count(*) FROM forum_points WHERE user_id = $1`, bob)

	res := srv.erasePersonalDataRegistered(ctx, "ADA@example.test")
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	// Removed: 4 posts, 1 revision, last author on a topic and the forum, solved_by, _forum_mod,
	// moderators, 2 likes, Ada's ledger rows, the like ledger rows she caused and the manual adjustment
	// she made, 1 subscription, 1 report, 1 resolved report, 6 profile values and the bio.
	if res.Removed < 20 || res.Retained != 1 || len(res.Messages) != 2 {
		t.Errorf("erase result: %+v", res)
	}

	// The posts stay, published, with their text, and no author.
	if n := count(`SELECT count(*) FROM posts WHERE type IN ('topic', 'reply') AND status = 'publish'`); n != postsBefore {
		t.Errorf("published forum posts: %d, want %d", n, postsBefore)
	}
	if n := count(`SELECT count(*) FROM posts WHERE id = ANY($1) AND author_id IS NULL AND content LIKE '%Ada%'`, []int64{adaTopic.ID, adaReply.ID, adaLast.ID}); n != 3 {
		t.Errorf("Ada's posts anonymized with their text: %d of 3", n)
	}

	// Nothing in any forum table links to Ada.
	cols, err := srv.store.Pool.Query(ctx, `SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = current_schema()
		AND (table_name LIKE 'forum\_%' AND column_name IN ('user_id', 'actor_id', 'resolved_by')
			OR table_name IN ('posts', 'post_revisions') AND column_name = 'author_id')`)
	if err != nil {
		t.Fatal(err)
	}
	var checks [][2]string
	for cols.Next() {
		var tbl, col string
		if err := cols.Scan(&tbl, &col); err != nil {
			t.Fatal(err)
		}
		checks = append(checks, [2]string{tbl, col})
	}
	cols.Close()
	if len(checks) < 8 {
		t.Fatalf("forum columns found: %v", checks)
	}
	for _, ch := range checks {
		where := ch[1] + ` = $1`
		if ch[0] == "posts" {
			where += ` AND type IN ('forum', 'topic', 'reply')`
		}
		if n := count(`SELECT count(*) FROM `+ch[0]+` WHERE `+where, ada); n != 0 {
			t.Errorf("%s.%s still holds Ada (%d rows)", ch[0], ch[1], n)
		}
	}
	adaID := strconv.FormatInt(ada, 10)
	if n := count(`SELECT count(*) FROM posts WHERE type IN ('forum', 'topic', 'reply') AND (meta->>'`+forumMetaLastAuthor+`' = $1
		OR meta->>'`+forumMetaSolvedBy+`' = $1 OR meta->'_forum_mod'->>'by' = $1 OR meta->'`+forumMetaModerators+`' @> to_jsonb($1::bigint)
		OR meta::text LIKE '%192.0.2.' || $1 || '"%' OR meta::text LIKE '%198.51.100.' || $1 || '"%')`, adaID); n != 0 {
		t.Errorf("forum post meta still names Ada or holds her IP: %d posts", n)
	}
	if n := count(`SELECT count(*) FROM forum_reports WHERE note LIKE '%Ada%'`); n != 0 {
		t.Error("Ada's report note survived")
	}
	if n := count(`SELECT count(*) FROM forum_reports WHERE user_id IS NULL AND reason = 'abuse' AND post_id = $1`, bobReply.ID); n != 1 {
		t.Error("Ada's report left the moderation queue")
	}
	var meta map[string]any
	var bio string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT meta, coalesce(profile->>'description', '') FROM users WHERE id = $1`, ada).Scan(&meta, &bio); err != nil {
		t.Fatal(err)
	}
	for _, k := range append(append([]string{}, forumPrivacyEraseMeta...), "avatar_id") {
		if _, ok := meta[k]; ok {
			t.Errorf("Ada's %s survived", k)
		}
	}
	if bio != "" {
		t.Error("Ada's bio survived")
	}
	if meta[forumUMetaGoogleSub] == nil {
		t.Error("the account's Google sign-in was removed (retained with the account)")
	}
	if n := count(`SELECT count(*) FROM forum_likes WHERE user_id IS NULL`); n != 3 {
		t.Errorf("anonymous likes: %d, want 3", n)
	}

	// No one else's points, likes or data changed.
	after, likesAfter := snapshot(), srv.forumLikeCounts(ctx, allPosts)
	for _, id := range others {
		if before[id] != after[id] {
			t.Errorf("member %d points: %+v, was %+v", id, after[id], before[id])
		}
	}
	for _, id := range allPosts {
		if likesBefore[id] != likesAfter[id] {
			t.Errorf("post %d likes: %d, was %d", id, likesAfter[id], likesBefore[id])
		}
	}
	if n := count(`SELECT count(*) FROM forum_points WHERE user_id = $1`, bob); n != bobLedgerBefore-1 {
		t.Errorf("Bob's ledger rows: %d, want %d (Ada's like of his topic merged into the anonymous one)", n, bobLedgerBefore-1)
	}
	if n := count(`SELECT count(*) FROM posts WHERE author_id = $1 AND meta->>'`+forumMetaIP+`' LIKE '%.' || $1::text`, bob); n != 2 {
		t.Errorf("Bob's posts or IPs changed: %d", n)
	}
	if n := count(`SELECT count(*) FROM forum_subscriptions WHERE user_id <> $1`, ada); n != 2 {
		t.Errorf("other subscriptions: %d", n)
	}
	if n := count(`SELECT count(*) FROM forum_reports WHERE user_id = $1 AND note = 'Bob finds this spammy'`, bob); n != 1 {
		t.Error("Bob's report changed")
	}
	if n := count(`SELECT count(*) FROM post_revisions WHERE author_id = $1`, bob); n != 1 {
		t.Error("Bob's revision changed")
	}
	if n := count(`SELECT count(*) FROM users WHERE id = $1 AND meta ? '`+forumUMetaDraft+`' AND meta ? 'avatar_id' AND profile->>'description' = 'Bob bio'`, bob); n != 1 {
		t.Error("Bob's profile changed")
	}
	var mods []int64
	var raw []byte
	_ = srv.store.Pool.QueryRow(ctx, `SELECT meta->'`+forumMetaModerators+`' FROM posts WHERE id = $1`, fid).Scan(&raw)
	if json.Unmarshal(raw, &mods) != nil || len(mods) != 1 || mods[0] != bob {
		t.Errorf("forum moderators: %s", raw)
	}

	// A second erasure finds nothing left.
	if again := srv.erasePersonalDataRegistered(ctx, "ada@example.test"); again.Err != nil || again.Removed != 0 {
		t.Errorf("second erase: %+v", again)
	}
	// The export no longer lists the anonymized posts.
	for _, g := range srv.registeredPersonalData(ctx, "ada@example.test") {
		if g.ID == "pursae-forum-posts" || g.ID == "pursae-forum-likes" {
			t.Errorf("export still has %s", g.ID)
		}
	}

	// Every view renders the anonymous posts.
	anon := &http.Client{}
	abs := func(u string) string { // forum URLs are absolute on the site address; ask the test server
		p, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		return ts.URL + p.RequestURI()
	}
	for _, u := range []string{adaTopic.URL, bobTopic.URL, srv.forumURL(ctx), srv.forumURL(ctx, "topics"), srv.forumURL(ctx, "search") + "?q=Ada",
		srv.forumProfileURL(ctx, "bob")} {
		code, body, _ := get(t, anon, abs(u))
		if code != http.StatusOK || strings.Contains(body, "Template error") || strings.Contains(body, "forum template error") {
			t.Errorf("GET %s: %d", u, code)
			continue
		}
		if strings.Contains(body, "192.0.2."+adaID) {
			t.Errorf("GET %s shows Ada's IP", u)
		}
	}
	_, body, _ := get(t, anon, abs(adaTopic.URL))
	if !strings.Contains(body, "Deleted user") || !strings.Contains(body, "Ada closes her thread") || !strings.Contains(body, "Carol answers Ada") {
		t.Error("topic page: no Deleted user, or a post is missing")
	}
	if strings.Contains(body, `/u/ada`) {
		t.Error("topic page still links to Ada's profile")
	}
	_, body, _ = get(t, anon, abs(srv.forumURL(ctx, "search")+"?q=closes"))
	if !strings.Contains(body, "Deleted user") {
		t.Error("search result: no Deleted user")
	}
	code, body, _ := get(t, anon, ts.URL+"/wp-json/forum/v1/topics/"+strconv.FormatInt(adaTopic.ID, 10))
	var rest map[string]any
	if code != http.StatusOK || json.Unmarshal([]byte(body), &rest) != nil || rest["author"] != nil {
		t.Errorf("REST topic: %d %s", code, body)
	}
	code, body, _ = get(t, anon, ts.URL+"/wp-json/forum/v1/topics/"+strconv.FormatInt(adaTopic.ID, 10)+"/replies")
	if code != http.StatusOK || !strings.Contains(body, "Ada closes her thread") {
		t.Errorf("REST replies: %d %s", code, body)
	}
	for _, u := range []string{"/wp-admin/forum", "/wp-admin/forum?tab=moderation", "/wp-admin/forum?tab=moderation&status=reported", "/wp-admin/forum?tab=members"} {
		if code, body, _ := get(t, c, ts.URL+u); code != http.StatusOK || strings.Contains(body, "Template error") {
			t.Errorf("admin %s: %d", u, code)
		}
	}
	// Ada's profile has no posts any more: a 404 for visitors (no account probing), empty for moderators.
	if code, _, _ := get(t, anon, abs(srv.forumProfileURL(ctx, "ada"))); code != http.StatusNotFound {
		t.Errorf("Ada's profile for a visitor: %d", code)
	}
	if code, body, _ := get(t, c, abs(srv.forumProfileURL(ctx, "ada"))); code != http.StatusOK || strings.Contains(body, "Ada asks") || strings.Contains(body, "Ada bio") {
		t.Errorf("Ada's profile for a moderator: %d, or it still lists her posts or bio", code)
	}
	if srv.lang != nil {
		if got := srv.lang.Catalog("tr_TR").T("Deleted user"); got != "Silinmiş kullanıcı" {
			t.Errorf("tr_TR Deleted user: %q", got)
		}
	}
}
