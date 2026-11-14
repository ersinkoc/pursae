package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

// Tests of the v0.2.5 forum features (the "merge" design): forum meta, tags, sort tabs, views,
// likes, solutions, online counts, members of the week, moderators, levels and badges, statistics,
// the forum picker, previews, drafts, subscriptions, similar topics, sign-up choices, reports and
// the demo format.

// forumRESTDo sends a REST request (JSON body) with an optional X-WP-Nonce and returns the status and
// the decoded body.
func forumRESTDo(t *testing.T, c *http.Client, method, u string, body any, nonce, ua string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, u, r)
	req.Header.Set("Content-Type", "application/json")
	if nonce != "" {
		req.Header.Set("X-WP-Nonce", nonce)
	}
	if ua == "" {
		ua = "Mozilla/5.0 (Macintosh) Firefox/140.0"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// forumRESTNonce is the wp_rest nonce of forum.js's configuration on a member page.
func forumRESTNonce(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no REST nonce in pf-config")
	}
	return m[1]
}

// Forum meta, tags, sort tabs, the tag views and the cache key parameters.
func TestForumDesignMetaTagsSort(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx

	// Design meta: saved, sanitized, kept by an update without it.
	id, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Plugins", ParentID: fx.cat, Design: &forumDesignInput{
		Icon: "rocket", Color: "MINT", Label: "Plugins", Number: "02", Chips: []string{"SDK", "Hooks", "sdk", " "},
		Tips: []string{"Search first.", ""}, StaffOnly: true}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := srv.forumByID(ctx, id)
	if f.Icon != "rocket" || f.Color != "mint" || f.Label != "Plugins" || f.Number != "02" || len(f.Chips) != 2 ||
		f.Chips[0].Slug != "sdk" || !strings.HasSuffix(f.Chips[0].URL, "/forums/f/plugins/?tag=sdk") || len(f.Tips) != 1 || !f.StaffOnly {
		t.Fatalf("design meta: %+v", f)
	}
	if _, err := srv.forumSaveForum(ctx, forumForumInput{ID: id, Title: "Plugins!", ParentID: fx.cat}, 1); err != nil {
		t.Fatal(err)
	}
	if f, _ = srv.forumByID(ctx, id); f.Icon != "rocket" || f.Title != "Plugins!" {
		t.Errorf("an update without design lost the meta: %+v", f)
	}
	if d := forumSanitizeDesign(forumDesignInput{Icon: "nope", Color: "red"}); d.Icon != "" || d.Color != "" {
		t.Errorf("unknown icon / colour kept: %+v", d)
	}

	// Tags: parsed, stored, filtered, listed.
	if names, slugs := forumParseTags("Cron, wasm; cron #Go,a,b,c,d,e"); len(slugs) != 5 || names[0] != "Cron" || slugs[0] != "cron" || slugs[2] != "go" {
		t.Errorf("parse tags: %v %v", names, slugs)
	}
	mk := func(title, tags string, at time.Time) *forumTopic {
		t.Helper()
		tp, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: 1, Title: title, Content: "<p>Body of " + title + "</p>", Tags: tags, At: at})
		if err != nil {
			t.Fatal(err)
		}
		return tp
	}
	now := time.Now()
	cronT := mk("Cron jobs do not run", "cron, wasm", now.Add(-3*time.Hour))
	mk("Webhook signature fails", "stripe", now.Add(-2*time.Hour))
	quiet := mk("A quiet question", "", now.Add(-1*time.Hour))
	if len(cronT.Tags) != 2 || cronT.Tags[0].Name != "cron" || !strings.HasSuffix(cronT.Tags[0].URL, "/forums/tag/cron/") {
		t.Fatalf("topic tags: %+v", cronT.Tags)
	}
	if list, n, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: fx.forum.ID, Tag: "cron"}); n != 1 || list[0].ID != cronT.ID {
		t.Errorf("tag filter: %d", n)
	}
	tags, err := srv.forumTagList(ctx, 0, 0)
	if err != nil || len(tags) != 3 {
		t.Fatalf("tag list: %v %+v", err, tags)
	}
	if _, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: cronT.ID, AuthorID: 1, Content: "<p>a reply</p>"}); err != nil {
		t.Fatal(err)
	}
	// Sorts: unanswered leaves the answered topic out; popular puts views first.
	if _, n, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: fx.forum.ID, Sort: "unanswered"}); n != 2 {
		t.Errorf("unanswered: %d", n)
	}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO forum_topic_views (topic_id, views) VALUES ($1, 500)`, quiet.ID); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: fx.forum.ID, Sort: "popular"}); list[0].ID != quiet.ID || list[0].Views != 500 {
		t.Errorf("popular: %+v", list[0])
	}

	// The views: tag page, tag list, all topics, sort tabs, canonical redirects, 404s.
	code, body, _ := get(t, fx.anon, fx.url("tag", "cron"))
	if code != 200 || !strings.Contains(body, "Cron jobs do not run") || strings.Contains(body, "Webhook signature") {
		t.Errorf("tag view: %d", code)
	}
	forumNoTemplateError(t, "tag view", body)
	if code, body, _ := get(t, fx.anon, fx.url("tags")); code != 200 || !strings.Contains(body, "#stripe") {
		t.Errorf("tags view: %d", code)
	}
	if code, _, _ := get(t, fx.anon, fx.url("tag", "nothing-here")); code != 404 {
		t.Errorf("unknown tag: %d", code)
	}
	code, body, _ = get(t, fx.anon, fx.url("topics")+"?sort=unanswered")
	if code != 200 || !strings.Contains(body, "A quiet question") || strings.Contains(body, "Cron jobs do not run") || !strings.Contains(body, `aria-current="true"`) {
		t.Errorf("topics?sort=unanswered: %d", code)
	}
	forumNoTemplateError(t, "topics view", body)
	code, body, _ = get(t, fx.anon, fx.forum.URL+"?tag=stripe")
	if code != 200 || !strings.Contains(body, "Webhook signature fails") || strings.Contains(body, "A quiet question") {
		t.Errorf("forum ?tag=: %d", code)
	}
	for _, q := range []string{"?sort=bogus", "?sort=latest", "?tag=Stripe"} {
		if code, _, loc := get(t, fx.anon, fx.forum.URL+q); code != http.StatusMovedPermanently || strings.Contains(loc, "sort=latest") || strings.Contains(loc, "bogus") {
			t.Errorf("%s: %d %s", q, code, loc)
		}
	}
	// SEO: a public topic and forum carry a description and Open Graph tags.
	_, body, _ = get(t, fx.anon, cronT.URL)
	if !strings.Contains(body, `<meta name="description" content="Body of Cron jobs do not run" />`) ||
		!strings.Contains(body, `<meta property="og:title" content="Cron jobs do not run" />`) || !strings.Contains(body, `og:type" content="article"`) {
		t.Error("topic page: no description / Open Graph")
	}
	if _, body, _ := get(t, fx.anon, fx.forum.URL); !strings.Contains(body, `<meta name="description" content="Talk about anything." />`) {
		t.Error("forum page: no description")
	}
	// Pursae SEO's default social image becomes the forum's og:image.
	if err := srv.seogoActivate(ctx); err == nil {
		o := *srv.seogoOpts()
		o.DefaultImage = "https://example.test/og.jpg"
		if err := srv.seogoSave(ctx, &o); err != nil {
			t.Fatal(err)
		}
		srv.purgeAll(ctx)
		if _, body, _ := get(t, fx.anon, fx.forum.URL); !strings.Contains(body, `<meta property="og:image" content="https://example.test/og.jpg" />`) ||
			!strings.Contains(body, `twitter:card" content="summary_large_image"`) {
			t.Error("forum page: no og:image from Pursae SEO")
		}
	}
	// The forum title is a Multi Language string once the owner changed it.
	if len(srv.forumPllStrings()) != 0 {
		t.Error("the default title is listed for translation")
	}
	fx.settings(t, func(st *forumSettings) { st.Title = "Pursae Topluluk" })
	if l := srv.forumPllStrings(); len(l) != 1 || l[0].String != "Pursae Topluluk" || l[0].Group != "Pursae Forum" {
		t.Errorf("forum pll strings: %+v", l)
	}
	// Localized dates for the profile and member list.
	pg := &forumPage{Extra: map[string]any{}}
	srv.forumPageData(ctx, pg)
	if d := pg.Date(time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)); !strings.Contains(d, "2026") {
		t.Errorf("date: %q", d)
	}

	// The cache keys ?sort= and ?tag= while the forum is on.
	cfg := srv.cacheConfig()
	if !strings.Contains(strings.Join(cfg.CacheQueryStrings, ","), "sort") {
		t.Errorf("cache query strings: %v", cfg.CacheQueryStrings)
	}

	// REST carries the meta and the topic fields.
	_, body, _ = get(t, fx.anon, fx.base+"/wp-json/forum/v1/forums")
	if !strings.Contains(body, `"color":"mint"`) || !strings.Contains(body, `"staff_only":true`) {
		t.Errorf("REST forums: %s", body)
	}
	_, body, _ = get(t, fx.anon, fx.base+"/wp-json/forum/v1/forums/"+strconv.FormatInt(fx.forum.ID, 10)+"/topics?tag=cron")
	if !strings.Contains(body, `"tags":[{`) || strings.Contains(body, "Webhook") {
		t.Errorf("REST topics ?tag=: %s", body)
	}
}

// Views, likes, solutions, the ledger and members of the week, online counts, subscriptions,
// reports, previews, drafts and the forum picker.
func TestForumDesignEngage(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	forumLimiterReset()
	forumViewSeenReset()
	forumPresenceReset()
	mara, maraID := fx.member(t, "mara", forumRoleParticipant)
	bob, bobID := fx.member(t, "bob", forumRoleParticipant)
	cem, _ := fx.member(t, "cem", forumRoleParticipant)

	// The forum picker: /forums/new/ lists the forums; a POST names the forum.
	code, body, _ := getHdr(t, mara, fx.url("new"))
	if code != 200 || !strings.Contains(body, `name="forum" value="`+strconv.FormatInt(fx.forum.ID, 10)+`"`) {
		t.Fatalf("picker: %d", code)
	}
	nonce := forumFormNonce(t, body, "new_topic")
	if code, body, _ := post(t, mara, fx.url("new"), url.Values{"forum_action": {"new_topic"}, "_wpnonce": {nonce}, "title": {"No forum chosen"}, "content": {"Body"}}); code != 422 || !strings.Contains(body, "Please choose a forum") {
		t.Errorf("no forum: %d", code)
	}
	// Preview: rendered, nothing saved.
	code, body, _ = post(t, mara, fx.url("new"), url.Values{"forum_action": {"new_topic"}, "_wpnonce": {nonce}, "title": {"Preview me"},
		"content": {"Hello <b>bold</b><script>x</script>"}, "preview": {"1"}, "forum": {strconv.FormatInt(fx.forum.ID, 10)}})
	if code != 200 || !strings.Contains(body, `class="pf-preview"`) || !strings.Contains(body, "<b>bold</b>") || strings.Contains(body, "<script>x") {
		t.Errorf("preview: %d", code)
	}
	// Draft: saved, then the form comes back filled.
	if code, _, loc := post(t, mara, fx.url("new"), url.Values{"forum_action": {"new_topic"}, "_wpnonce": {nonce}, "title": {"Draft title here"},
		"content": {"Draft body"}, "tags": {"cron"}, "draft": {"1"}}); code != 302 || !strings.Contains(loc, "draft_saved") {
		t.Errorf("draft: %d %s", code, loc)
	}
	if _, body, _ := getHdr(t, mara, fx.url("new")); !strings.Contains(body, `value="Draft title here"`) || !strings.Contains(body, "Draft body") {
		t.Error("the draft does not fill the form")
	}
	// The topic, tagged, followed by its author.
	code, _, loc := post(t, mara, fx.url("new"), url.Values{"forum_action": {"new_topic"}, "_wpnonce": {nonce}, "title": {"Caddy and HTTPS setup"},
		"content": {"<p>Links still use http behind Caddy.</p>"}, "tags": {"caddy, https"}, "notify": {"1"}, "forum": {strconv.FormatInt(fx.forum.ID, 10)}})
	if code != 302 {
		t.Fatalf("new topic from the picker: %d", code)
	}
	topics, _, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: fx.forum.ID})
	if len(topics) != 1 || len(topics[0].Tags) != 2 || !srv.forumSubscribed(ctx, maraID, topics[0].ID) {
		t.Fatalf("topic: %+v", topics)
	}
	topic := topics[0]
	if u, _ := srv.store.UserByID(ctx, maraID); forumDraftOf(u) != nil {
		t.Error("the draft outlived the posted topic")
	}
	_ = loc

	// A reply by bob emails mara (she follows) with an unsubscribe link.
	_, body, _ = getHdr(t, bob, topic.URL)
	rnonce := forumFormNonce(t, body, "reply")
	before := len(sentMail(t))
	if code, _, _ := post(t, bob, topic.URL, url.Values{"forum_action": {"reply"}, "_wpnonce": {rnonce}, "content": {"<p>Set WPGO_SITE_URL to https.</p>"}}); code != 302 {
		t.Fatalf("reply: %d", code)
	}
	mails := sentMail(t)[before:]
	if len(mails) != 1 || mails[0].To[0] != "mara@example.test" || !strings.Contains(mails[0].Body, "/forums/unsubscribe/?t=") {
		t.Fatalf("reply email: %+v", mails)
	}
	unsub := regexp.MustCompile(`https?://\S+/forums/unsubscribe/\S+`).FindString(mails[0].Body)
	replies, _, _ := srv.forumReplies(ctx, forumReplyQuery{TopicID: topic.ID})
	reply := replies[0]

	// Views: counted once per visitor, never for robots.
	view := fx.base + "/wp-json/forum/v1/topics/" + strconv.FormatInt(topic.ID, 10) + "/view"
	if code, out := forumRESTDo(t, fx.anon, "POST", view, nil, "", ""); code != 200 || out["views"] != float64(1) || out["counted"] != true {
		t.Errorf("view: %d %v", code, out)
	}
	if _, out := forumRESTDo(t, fx.anon, "POST", view, nil, "", ""); out["views"] != float64(1) || out["counted"] != false {
		t.Errorf("repeat view counted: %v", out)
	}
	if _, out := forumRESTDo(t, fx.anon, "POST", view, nil, "", "Googlebot/2.1"); out["counted"] != false {
		t.Errorf("robot counted: %v", out)
	}
	if code, _ := forumRESTDo(t, fx.anon, "POST", fx.base+"/wp-json/forum/v1/topics/999999/view", nil, "", ""); code != 404 {
		t.Errorf("view of a missing topic: %d", code)
	}

	// Likes: the front form (bob likes mara's topic), never your own, REST with the nonce.
	_, body, _ = getHdr(t, bob, topic.URL)
	if !strings.Contains(body, `name="forum_action" value="like"`) {
		t.Fatal("no like form for bob")
	}
	bnonce := forumFormNonce(t, body, "like")
	if code, _, _ := post(t, bob, topic.URL, url.Values{"forum_action": {"like"}, "_wpnonce": {bnonce}, "post_id": {strconv.FormatInt(topic.ID, 10)}}); code != 302 {
		t.Errorf("like: %d", code)
	}
	if code, _, _ := post(t, bob, topic.URL, url.Values{"forum_action": {"like"}, "_wpnonce": {bnonce}, "post_id": {strconv.FormatInt(reply.ID, 10)}}); code != 403 {
		t.Errorf("liking your own reply: %d", code)
	}
	if n := srv.forumLikeCount(ctx, topic.ID); n != 1 {
		t.Errorf("likes: %d", n)
	}
	_, body, _ = getHdr(t, mara, topic.URL)
	mrest := forumRESTNonce(t, body)
	like := fx.base + "/wp-json/forum/v1/posts/" + strconv.FormatInt(reply.ID, 10) + "/like"
	if code, _ := forumRESTDo(t, mara, "POST", like, nil, "", ""); code != 401 {
		t.Errorf("REST like without a nonce: %d", code)
	}
	if code, out := forumRESTDo(t, mara, "POST", like, nil, mrest, ""); code != 200 || out["liked"] != true || out["likes"] != float64(1) {
		t.Errorf("REST like: %d %v", code, out)
	}
	if code, out := forumRESTDo(t, mara, "GET", fx.base+"/wp-json/forum/v1/me?topic="+strconv.FormatInt(topic.ID, 10), nil, mrest, ""); code != 200 ||
		out["subscribed"] != true || out["can_solve"] != true || len(out["liked"].([]any)) != 1 {
		t.Errorf("REST me: %d %v", code, out)
	}

	// Solutions: the topic's author marks bob's reply; a third member may not.
	_, body, _ = getHdr(t, cem, topic.URL)
	if strings.Contains(body, `value="solve"`) {
		t.Error("a third member is offered the solve button")
	}
	cnonce := forumFormNonce(t, body, "like")
	if code, _, _ := post(t, cem, topic.URL, url.Values{"forum_action": {"solve"}, "_wpnonce": {cnonce}, "post_id": {strconv.FormatInt(reply.ID, 10)}}); code != 403 {
		t.Errorf("solve by a stranger: %d", code)
	}
	_, body, _ = getHdr(t, mara, topic.URL)
	mnonce := forumFormNonce(t, body, "solve")
	if code, _, _ := post(t, mara, topic.URL, url.Values{"forum_action": {"solve"}, "_wpnonce": {mnonce}, "post_id": {strconv.FormatInt(reply.ID, 10)}}); code != 302 {
		t.Errorf("solve: %d", code)
	}
	fresh, _ := srv.forumTopicByID(ctx, topic.ID)
	if !fresh.Solved || fresh.SolutionID != reply.ID {
		t.Fatalf("solved: %+v", fresh)
	}
	_, body, _ = get(t, fx.anon, topic.URL)
	if !strings.Contains(body, "pf-post--solution") || !strings.Contains(body, "pf-solution-callout") || strings.Contains(body, "<form") {
		t.Errorf("anonymous topic page: solution not shown, or a form")
	}
	if _, body, _ := get(t, fx.anon, fx.forum.URL+"?sort=solved"); !strings.Contains(body, "Caddy and HTTPS setup") || !strings.Contains(body, "pf-badge--solved") {
		t.Error("solved list")
	}

	// Points: bob has a reply (1), a solution (10) and mara's like (1); mara a topic (3) and bob's like (1).
	authors, _ := srv.forumAuthors(ctx, []int64{bobID, maraID})
	if a := authors[bobID]; a.Solutions != 1 || a.LikesReceived != 1 || a.Points != 1+10+1 {
		t.Errorf("bob: solutions %d points %d", a.Solutions, a.Points)
	}
	if a := authors[maraID]; a.LikesReceived != 1 || a.Points != 3+1 {
		t.Errorf("mara: likes %d points %d", a.LikesReceived, a.Points)
	}
	week, err := srv.forumWeeklyMembers(ctx, 5)
	if err != nil || len(week) < 2 || week[0].Author.ID != bobID || week[0].Delta != 12 {
		t.Fatalf("weekly: %v %+v", err, week)
	}
	if err := srv.forumAdjustPoints(ctx, maraID, 1, 20, "Great guide"); err != nil {
		t.Fatal(err)
	}
	if week, _ = srv.forumWeeklyMembers(ctx, 5); week[0].Author.ID != maraID || week[0].Delta != 24 {
		t.Errorf("weekly after a bonus: %+v", week[0])
	}
	// Unsolve takes the points back; trashing an accepted answer withdraws it.
	if err := srv.forumSolve(ctx, mustUser(t, srv, maraID), topic.ID, 0); err != nil {
		t.Fatal(err)
	}
	if fresh, _ = srv.forumTopicByID(ctx, topic.ID); fresh.Solved {
		t.Error("unsolve")
	}
	_ = srv.forumSolve(ctx, mustUser(t, srv, maraID), topic.ID, reply.ID)
	if err := srv.forumModerate(ctx, reply.ID, "trash", 1); err != nil {
		t.Fatal(err)
	}
	if fresh, _ = srv.forumTopicByID(ctx, topic.ID); fresh.Solved {
		t.Error("a trashed reply still solves the topic")
	}
	_ = srv.forumModerate(ctx, reply.ID, "restore", 1)

	// Online: forum pages mark the visitor; REST reads the counts.
	forumPresenceReset()
	if _, body, _ := get(t, fx.anon, fx.forum.URL); !strings.Contains(body, `data-pf-online="forum"`) {
		t.Error("forum page has no online count")
	}
	if n, _ := srv.forumOnlineCount(fx.forum.ID); n != 0 {
		t.Errorf("a robot (Go-http-client) counted as online: %d", n)
	}
	// A cached copy of the page does not reach the forum; forum.js's presence call does.
	if code, out := forumRESTDo(t, fx.anon, "POST", fx.base+"/wp-json/forum/v1/presence", map[string]any{"forum": fx.forum.ID}, "", ""); code != 200 || out["forum_online"] != float64(1) {
		t.Errorf("presence: %d %v", code, out)
	}

	// Reports: a member reports; the moderation queue's Reported view lists it; dismiss closes it.
	_, body, _ = getHdr(t, cem, topic.URL)
	cnonce = forumFormNonce(t, body, "report")
	if code, _, loc := post(t, cem, topic.URL, url.Values{"forum_action": {"report"}, "_wpnonce": {cnonce}, "post_id": {strconv.FormatInt(reply.ID, 10)},
		"reason": {"abuse"}, "note": {"Rude words"}}); code != 302 || !strings.Contains(loc, "forum_msg=reported") {
		t.Errorf("report: %d %s", code, loc)
	}
	_, body, _ = get(t, fx.admin, fx.base+"/wp-admin/forum?tab=moderation&view=reported")
	if !strings.Contains(body, "Reported 1&times;") || !strings.Contains(body, "Rude or abusive") || !strings.Contains(body, "Rude words") {
		t.Errorf("admin reported view")
	}
	if err := srv.forumAdminApply(ctx, reply.ID, "dismiss", 1); err != nil {
		t.Fatal(err)
	}
	if r := srv.forumOpenReports(ctx, []int64{reply.ID}); len(r) != 0 {
		t.Error("dismiss left the report open")
	}

	// Unsubscribe link: GET asks, POST stops the emails.
	if code, body, _ := get(t, fx.anon, unsub); code != 200 || !strings.Contains(body, `value="unsubscribe_link"`) {
		t.Fatalf("unsubscribe page: %d", code)
	}
	if !srv.forumSubscribed(ctx, maraID, topic.ID) {
		t.Fatal("a GET unsubscribed")
	}
	u, _ := url.Parse(unsub)
	if code, _, _ := post(t, fx.anon, fx.url("unsubscribe"), url.Values{"forum_action": {"unsubscribe_link"}, "t": {u.Query().Get("t")}, "u": {u.Query().Get("u")}, "k": {forgedKey(u.Query().Get("k"))}}); code != 403 {
		t.Errorf("forged key: %d", code)
	}
	if code, _, _ := post(t, fx.anon, fx.url("unsubscribe"), url.Values{"forum_action": {"unsubscribe_link"}, "t": {u.Query().Get("t")}, "u": {u.Query().Get("u")}, "k": {u.Query().Get("k")}}); code != 302 {
		t.Errorf("unsubscribe: %d", code)
	}
	if srv.forumSubscribed(ctx, maraID, topic.ID) {
		t.Error("still subscribed")
	}

	// Similar topics; the topic view's sidebar data.
	other, _ := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: 1, Title: "HTTPS behind nginx", Content: "<p>x</p>", Tags: "https"})
	sim, err := srv.forumSimilarTopics(ctx, "Caddy and HTTPS setup", []string{"https"}, topic.ID, 5)
	if err != nil || len(sim) != 1 || sim[0].ID != other.ID {
		t.Errorf("similar: %v %+v", err, sim)
	}
	if code, out := forumRESTDo(t, fx.anon, "GET", fx.base+"/wp-json/forum/v1/stats", nil, "", ""); code != 200 || out["topics"] != float64(2) || out["solved"] != float64(0) { // the trashed answer withdrew the solution
		t.Errorf("REST stats: %d %v", code, out)
	}
}

func mustUser(t *testing.T, srv *Server, id int64) *store.User {
	t.Helper()
	u, err := srv.store.UserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Staff-only forums, forum moderators, levels and badges for themes, the lead author's statistics.
func TestForumDesignStaffLevels(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	mara, maraID := fx.member(t, "mara", forumRoleParticipant)
	id, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Announcements", ParentID: fx.cat, Design: &forumDesignInput{StaffOnly: true, Moderators: []int64{maraID}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := srv.forumByID(ctx, id)
	if code, _, _ := getHdr(t, mara, fx.url("f", f.Slug, "new")); code != 200 {
		t.Errorf("staff-only new topic form: %d", code)
	}
	_, body, _ := getHdr(t, mara, fx.url("new"))
	if !regexp.MustCompile(`value="` + strconv.FormatInt(id, 10) + `" disabled`).MatchString(body) {
		t.Error("the picker offers a staff-only forum to a member")
	}
	nonce := forumFormNonce(t, body, "new_topic")
	if code, _, _ := post(t, mara, fx.url("new"), url.Values{"forum_action": {"new_topic"}, "_wpnonce": {nonce}, "title": {"Not allowed here"}, "content": {"Body"}, "forum": {strconv.FormatInt(id, 10)}}); code != 403 {
		t.Errorf("member topic in a staff-only forum: %d", code)
	}
	// The admin may; members may reply.
	tp, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: id, AuthorID: 1, Title: "Version 0.2.5 is out", Content: "<p>Notes</p>"})
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ = getHdr(t, mara, tp.URL)
	if !strings.Contains(body, `value="reply"`) {
		t.Error("members cannot reply in a staff-only forum")
	}
	// Moderators: the forum's own list.
	mods, err := srv.forumModeratorsOf(ctx, id)
	if err != nil || len(mods) != 1 || mods[0].ID != maraID {
		t.Errorf("forum moderators: %v %+v", err, mods)
	}
	// Without a list: the site's moderators, the administrator first.
	mods, _ = srv.forumModeratorsOf(ctx, fx.forum.ID)
	if len(mods) == 0 || !mods[0].Admin {
		t.Errorf("site moderators: %+v", mods)
	}
	_, body, _ = get(t, fx.anon, f.URL)
	if !strings.Contains(body, "Only the team starts topics in this forum.") || !strings.Contains(body, "Mara") {
		t.Error("staff-only forum page")
	}

	// Levels and badges for themes; the lead author's statistics.
	p := &forumPage{Extra: map[string]any{}}
	srv.forumPageData(ctx, p)
	lv := p.Levels()
	if len(lv) != 6 || lv[0].Rank != 1 || lv[5].Rank != 6 {
		t.Errorf("levels: %+v", lv)
	}
	var solver, streak bool
	for _, b := range p.Badges() {
		solver = solver || b.Rule == forumRuleSolutions
		streak = streak || b.Rule == forumRuleStreak
	}
	if !solver || !streak {
		t.Error("default badges lack the solutions and streak rules")
	}
	if pts := p.Points(); pts.SolutionPoints != 10 || pts.LikePoints != 1 {
		t.Errorf("points: %+v", pts)
	}
	tp2, _ := srv.forumTopicByID(ctx, tp.ID)
	if tp2.Author == nil || !tp2.Author.Stats || tp2.Author.Level == nil {
		t.Error("the lead author's statistics are not loaded")
	}
	// Rules of the new badges.
	now := time.Now()
	if !forumBadgeEarned(forumBadge{Rule: forumRuleStreak, Threshold: 7}, &forumMemberStat{Streak: 7}, now) ||
		forumBadgeEarned(forumBadge{Rule: forumRuleSolutions, Threshold: 5}, &forumMemberStat{Solutions: 4}, now) ||
		!forumBadgeEarned(forumBadge{Rule: forumRuleLikes, Threshold: 2}, &forumMemberStat{Likes: 3}, now) {
		t.Error("badge rules")
	}
	// A posting streak: posts on three consecutive days.
	for d := 3; d >= 1; d-- {
		if _, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: maraID, Title: "Day " + strconv.Itoa(d), Content: "<p>x</p>",
			At: now.Add(-time.Duration(d) * 24 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := srv.forumMemberStats(ctx, []int64{maraID})
	if st[maraID].Streak != 3 {
		t.Errorf("streak: %d", st[maraID].Streak)
	}
	// Compact numbers.
	if got := p.Compact(3100); got != "3.1K" {
		t.Errorf("compact: %q", got)
	}
	if got := p.Compact(950); got != "950" {
		t.Errorf("compact: %q", got)
	}
}

// Sign-up: the rules checkbox (required when set) and the digest choice are stored; "change the
// address" works for an unconfirmed account and refuses forged tokens.
func TestForumDesignSignup(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	forumLimiterReset()
	fx.settings(t, func(st *forumSettings) { st.RulesURL = "/community-rules/"; st.RequireRules = true })
	cj, _ := cookiejar.New(nil)
	jar := &http.Client{Jar: cj, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, body, _ := getHdr(t, jar, fx.url("register"))
	if !strings.Contains(body, `name="rules"`) || !strings.Contains(body, `href="/community-rules/"`) {
		t.Fatal("register form has no rules checkbox")
	}
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)[1]
	tok := regexp.MustCompile(`name="` + commentTimeField + `" value="([^"]+)"`).FindStringSubmatch(body)[1]
	form := url.Values{"forum_action": {"register"}, "_wpnonce": {nonce}, commentTimeField: {tok}, "user_login": {"newbie"},
		"user_email": {"newbie@example.test"}, "user_pass": {"a long password 1"}, "digest": {"1"}}
	time.Sleep(3 * time.Second) // the time trap
	if code, body, _ := post(t, jar, fx.url("register"), form); code != 422 || !strings.Contains(body, "Please accept the community rules.") {
		t.Fatalf("without the rules: %d", code)
	}
	form.Set("rules", "1")
	code, body, _ := post(t, jar, fx.url("register"), form)
	if code != 200 || !strings.Contains(body, `name="change_token"`) {
		t.Fatalf("register: %d", code)
	}
	u, err := srv.store.UserByLoginOrEmail(ctx, "newbie")
	if err != nil || forumMetaStr(u.Meta, forumUMetaRules) == "" || !forumMetaBool(u.Meta, forumUMetaDigest) {
		t.Fatalf("stored choices: %v %v", err, u.Meta)
	}
	ctok := regexp.MustCompile(`name="change_token" value="([^"]+)"`).FindStringSubmatch(body)[1]
	anonce := regexp.MustCompile(`value="change_email" />\s*<input type="hidden" name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)[1]
	if code, _, _ := post(t, jar, fx.url("verify"), url.Values{"forum_action": {"change_email"}, "_wpnonce": {anonce}, "change_token": {ctok + "0"},
		"old_email": {"newbie@example.test"}, "user_email": {"other@example.test"}}); code != 403 {
		t.Errorf("forged token: %d", code)
	}
	before := len(sentMail(t))
	if code, _, loc := post(t, jar, fx.url("verify"), url.Values{"forum_action": {"change_email"}, "_wpnonce": {anonce}, "change_token": {ctok},
		"old_email": {"newbie@example.test"}, "user_email": {"fixed@example.test"}}); code != 302 || !strings.Contains(loc, "email_changed") {
		t.Fatalf("change email: %d %s", code, loc)
	}
	if u, _ = srv.store.UserByID(ctx, u.ID); u.Email != "fixed@example.test" {
		t.Errorf("email: %s", u.Email)
	}
	if m := sentMail(t)[before:]; len(m) != 1 || m[0].To[0] != "fixed@example.test" {
		t.Errorf("new confirmation mail: %+v", m)
	}
	// A confirmed account cannot use the form.
	_ = srv.forumVerifyMembers(ctx, u.ID)
	if code, _, _ := post(t, jar, fx.url("verify"), url.Values{"forum_action": {"change_email"}, "_wpnonce": {anonce}, "change_token": {srv.forumChangeEmailToken(u.ID, "fixed@example.test", time.Now().Add(time.Hour))},
		"old_email": {"fixed@example.test"}, "user_email": {"again@example.test"}}); code != 403 {
		t.Errorf("confirmed account: %d", code)
	}
}

// The demo format's v0.2.5 fields; with the plugin off the new REST routes are gone.
func TestForumDesignDemoAndOff(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	raw := json.RawMessage(`{
	  "rules_url": "/rules/", "require_rules": true,
	  "members": [{"ref": "elif", "display_name": "Elif Demir"}, {"ref": "mert", "display_name": "Mert Kaya"}],
	  "forums": [{"ref": "start", "title": "Getting started", "icon": "rocket", "color": "sky", "label": "Start", "number": "01",
	              "chips": ["Install", "Migration"], "tips": ["Search first."], "moderators": ["elif"]},
	             {"ref": "news", "title": "News", "staff_only": true, "color": "lilac"}],
	  "topics": [{"ref": "caddy", "forum": "start", "author": "elif", "title": "Caddy and HTTPS", "content": "<p>Help</p>",
	              "tags": ["caddy", "https"], "views": 214, "likes": 8, "solution": 0, "hours_ago": 3,
	              "replies": [{"author": "mert", "content": "<p>Use https</p>", "likes": 21, "hours_ago": 2}]}]
	}`)
	if p := forumValidateDemo(raw, nil); len(p) != 0 {
		t.Fatalf("demo problems: %v", p)
	}
	for name, bad := range map[string]string{
		"icon":     `{"forums":[{"ref":"a","title":"A","icon":"nope"}]}`,
		"color":    `{"forums":[{"ref":"a","title":"A","color":"red"}]}`,
		"solution": `{"forums":[{"ref":"a","title":"A"}],"topics":[{"ref":"t","forum":"a","author":"admin","title":"Topic","content":"x","solution":2}]}`,
		"views":    `{"forums":[{"ref":"a","title":"A"}],"topics":[{"ref":"t","forum":"a","author":"admin","title":"Topic","content":"x","views":-1}]}`,
		"mod":      `{"forums":[{"ref":"a","title":"A","moderators":["ghost"]}]}`,
	} {
		if p := forumValidateDemo(json.RawMessage(bad), nil); len(p) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := srv.forumImportDemo(ctx, 1, "merge", raw, nil); err != nil {
		t.Fatal(err)
	}
	f, err := srv.forumBySlug(ctx, "getting-started")
	if err != nil || f.Icon != "rocket" || f.Color != "sky" || f.Number != "01" || len(f.Chips) != 2 {
		t.Fatalf("demo forum: %v %+v", err, f)
	}
	topics, _, _ := srv.forumTopics(ctx, forumTopicQuery{ForumID: f.ID})
	if len(topics) != 1 {
		t.Fatal("demo topic")
	}
	tp := topics[0]
	if tp.Views != 214 || tp.Likes != 8 || !tp.Solved || len(tp.Tags) != 2 {
		t.Errorf("demo topic fields: views %d likes %d solved %v tags %v", tp.Views, tp.Likes, tp.Solved, tp.Tags)
	}
	replies, _, _ := srv.forumReplies(ctx, forumReplyQuery{TopicID: tp.ID})
	if len(replies) != 1 || replies[0].Likes != 21 {
		t.Errorf("demo reply likes: %+v", replies)
	}
	if st := srv.forumLoad(); st.RulesURL != "/rules/" || !st.RequireRules {
		t.Errorf("demo rules: %+v", st)
	}
	// Re-importing adds nothing.
	if n, err := srv.forumImportDemo(ctx, 1, "merge", raw, nil); err != nil || n != 0 {
		t.Errorf("re-import: %d %v", n, err)
	}

	// Off: the new routes answer rest_no_route, the forum URLs 404.
	if err := srv.forumDeactivate(ctx); err != nil {
		t.Fatal(err)
	}
	if code, out := forumRESTDo(t, fx.anon, "GET", fx.base+"/wp-json/forum/v1/stats", nil, "", ""); code != 404 || out["code"] != "rest_no_route" {
		t.Errorf("stats while off: %d %v", code, out)
	}
	if code, _ := forumRESTDo(t, fx.anon, "POST", fx.base+"/wp-json/forum/v1/topics/"+strconv.FormatInt(tp.ID, 10)+"/view", nil, "", ""); code != 404 {
		t.Errorf("view beacon while off: %d", code)
	}
	if code, _, _ := get(t, fx.anon, fx.url("tags")); code != 404 {
		t.Errorf("tags while off: %d", code)
	}
	if cfg := srv.cacheConfig(); strings.Contains(strings.Join(cfg.CacheQueryStrings, ","), "tag") {
		t.Errorf("cache keys while off: %v", cfg.CacheQueryStrings)
	}
}

// forgedKey changes the first hex digit of a key (never to itself).
func forgedKey(k string) string {
	if k == "" {
		return "0"
	}
	if k[0] == '0' {
		return "1" + k[1:]
	}
	return "0" + k[1:]
}
