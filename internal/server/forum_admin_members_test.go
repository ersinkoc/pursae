package server

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// forumAdminFixture is an admin site with Pursae Forum on, a forum and a few members in every state.
type forumAdminFixture struct {
	base  string
	c     *http.Client // the administrator (user 1)
	srv   *Server
	ctx   context.Context
	forum int64
	ids   map[string]int64
}

// forumAdminSeed creates members: alice (busy, Google), bob (moderator), carol (unverified, a pending
// topic), dave (blocked), erin (subscriber, no posts) and some backdated activity for the chart.
func forumAdminSeed(t *testing.T) *forumAdminFixture {
	t.Helper()
	base, c, srv := forumAdminSite(t)
	ctx := context.Background()
	cat, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Community", Kind: "category"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	fid, err := srv.forumSaveForum(ctx, forumForumInput{Title: "General discussion", ParentID: cat}, 1)
	if err != nil {
		t.Fatal(err)
	}
	fx := &forumAdminFixture{base: base, c: c, srv: srv, ctx: ctx, forum: fid, ids: map[string]int64{}}
	people := []struct{ login, name, role string }{
		{"alice", "Alice Archer", forumRoleParticipant}, {"bob", "Bob Builder", forumRoleModerator},
		{"carol", "Carol Chen", forumRoleParticipant}, {"dave", "Dave Diaz", forumRoleParticipant}, {"erin", "Erin Evans", "subscriber"},
	}
	for _, p := range people {
		id := asCreateUser(t, srv, p.login, p.role, asStrongPass)
		if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET display_name = $2 WHERE id = $1`, id, p.name); err != nil {
			t.Fatal(err)
		}
		fx.ids[p.login] = id
	}
	if err := srv.forumSetUserMeta(ctx, fx.ids["alice"], map[string]any{forumUMetaGoogleSub: "g-alice"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, fx.ids["carol"], map[string]any{forumUMetaUnverified: true}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetUserCapExceptions(ctx, fx.ids["dave"], map[string]string{capForumParticipate: "deny"}); err != nil {
		t.Fatal(err)
	}
	// alice: 6 topics and 9 replies spread over the last days; bob: 3 replies; carol: a pending topic.
	var first int64
	for i := 0; i < 6; i++ {
		tp, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: fx.ids["alice"], Title: "Alice topic " + strconv.Itoa(i+1), Content: "<p>Hello everyone, this is a topic about the garden.</p>"})
		if err != nil {
			t.Fatal(err)
		}
		if first == 0 {
			first = tp.ID
		}
		_, _ = srv.store.Pool.Exec(ctx, `UPDATE posts SET created_at = now() - make_interval(days => $2) WHERE id = $1`, tp.ID, i*3)
	}
	for i := 0; i < 9; i++ {
		r, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: first, AuthorID: fx.ids["alice"], Content: "<p>A reply with some words.</p>"})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = srv.store.Pool.Exec(ctx, `UPDATE posts SET created_at = now() - make_interval(days => $2) WHERE id = $1`, r.ID, i*2)
	}
	for i := 0; i < 3; i++ {
		if _, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: first, AuthorID: fx.ids["bob"], Content: "<p>Moderator here.</p>"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: fx.ids["carol"], Title: "Waiting for approval", Content: "<p>New here</p>", Pending: true}); err != nil {
		t.Fatal(err)
	}
	return fx
}

// Overview: totals, the queue, the 30-day chart, top contributors and recent activity.
func TestForumAdminOverview(t *testing.T) {
	fx := forumAdminSeed(t)
	code, body, _ := get(t, fx.c, fx.base+"/wp-admin/forum?tab=overview")
	if code != http.StatusOK {
		t.Fatalf("overview: %d", code)
	}
	for _, want := range []string{
		`pf-admin-stat--topics`, `pf-admin-stat--members`,
		`href="/wp-admin/forum?tab=moderation&amp;view=pending"><strong>1</strong>`, // carol's pending topic
		`class="pf-admin-top"`, ">Alice Archer</a>", "pf-level", // the top contributor with a level chip
		`class="pf-admin-activity"`, "Alice topic 1",
		`role="img" aria-label="In the last 30 days:`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview misses %q", want)
		}
	}
	if n := strings.Count(body, `<li class="pf-admin-chart__day`); n != 30 {
		t.Errorf("chart days: %d", n)
	}
	if !regexp.MustCompile(`class="pf-admin-chart__bar is-topics" style="height:\d+%"`).MatchString(body) {
		t.Error("chart bars carry no height")
	}
	// Alice (6 topics * 3 + 9 replies = 27 points) leads Bob (3 points).
	if a, b := strings.Index(body, ">Alice Archer</a>"), strings.Index(body, ">Bob Builder</a>"); a < 0 || b < 0 || a > b {
		t.Errorf("top contributors order: alice %d bob %d", a, b)
	}
}

var forumAdminMemberActionRe = regexp.MustCompile(`href="(/wp-admin/forum\?[^"]*do=block[^"]*user=(\d+)[^"]*)"`)

// Members: columns, filters with counts, search, sort, bulk and row actions, the member screen.
func TestForumAdminMembers(t *testing.T) {
	fx := forumAdminSeed(t)
	srv, ctx, ids := fx.srv, fx.ctx, fx.ids
	code, body, _ := get(t, fx.c, fx.base+"/wp-admin/forum?tab=members")
	if code != http.StatusOK {
		t.Fatalf("members: %d", code)
	}
	for _, want := range []string{
		"alice@example.test", ">Alice Archer</a>", `class="pf-admin-state is-blocked">Blocked`, `class="pf-admin-state is-unverified">Unverified`,
		`class="pf-admin-state is-role">Moderator`, `<span class="unverified-count">1</span>`, `<span class="blocked-count">1</span>`,
		`<span class="google-count">1</span>`, `<span class="moderators-count">`, `value="award"`, `name="badge"`, "sorting-indicators",
		"column-points", "column-last_active",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("members list misses %q", want)
		}
	}
	// Filters.
	_, body, _ = get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&filter=blocked")
	if !strings.Contains(body, ">Dave Diaz</a>") || strings.Contains(body, ">Alice Archer</a>") {
		t.Error("blocked filter")
	}
	_, body, _ = get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&filter=google")
	if !strings.Contains(body, ">Alice Archer</a>") || strings.Contains(body, ">Bob Builder</a>") {
		t.Error("google filter")
	}
	// Search by email (admin scope), wildcards are literal.
	_, body, _ = get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&s=carol%40example")
	if !strings.Contains(body, ">Carol Chen</a>") || strings.Contains(body, ">Alice Archer</a>") {
		t.Error("search by email")
	}
	if _, body, _ = get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&s=%25"); !strings.Contains(body, "No members match your search.") {
		t.Error("a % search matches everyone")
	}
	// Sort by points, ascending and descending; a bogus orderby falls back to the default.
	_, body, _ = get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&orderby=points&order=desc")
	if a, b := strings.Index(body, ">Alice Archer</a>"), strings.Index(body, ">Bob Builder</a>"); a < 0 || b < 0 || a > b {
		t.Error("points desc order")
	}
	if !strings.Contains(body, `column-points num sorted desc`) {
		t.Error("points column not marked sorted")
	}
	// A page past the end goes to the last page once (no redirect loop).
	if code, _, loc := get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&filter=blocked&paged=40"); code != http.StatusFound || strings.Contains(loc, "paged=") || !strings.Contains(loc, "filter=blocked") {
		t.Errorf("past the last page: %d %s", code, loc)
	}
	if code, _, loc := get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&filter=blocked"); code != http.StatusOK {
		t.Errorf("after the past-the-end redirect: %d %s", code, loc)
	}
	if code, _, _ := get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&orderby=id;DROP+TABLE+users&order=sideways"); code != http.StatusOK {
		t.Errorf("bogus sort: %d", code)
	}

	nonce := formNonce(t, body)
	bulk := func(form url.Values) string {
		t.Helper()
		form.Set("_wpnonce", nonce)
		code, _, loc := post(t, fx.c, fx.base+"/wp-admin/forum?tab=members&filter=all&orderby=points", form)
		if code != http.StatusFound {
			t.Fatalf("bulk %v: %d", form, code)
		}
		return loc
	}
	sid := func(names ...string) []string {
		var out []string
		for _, n := range names {
			out = append(out, strconv.FormatInt(ids[n], 10))
		}
		return out
	}
	// Verify carol.
	loc := bulk(url.Values{"action": {"verify"}, "users[]": sid("carol")})
	if !strings.Contains(loc, "done=verify") || !strings.Contains(loc, "n=1") || !strings.Contains(loc, "orderby=points") {
		t.Errorf("verify redirect: %s", loc)
	}
	if st, _ := srv.forumMemberStats(ctx, []int64{ids["carol"]}); !st[ids["carol"]].Verified {
		t.Error("carol not verified")
	}
	_, body, _ = get(t, fx.c, fx.base+loc)
	if !strings.Contains(body, "1 member marked as verified.") {
		t.Error("verify notice")
	}
	// Block alice and another administrator: the administrator is refused with a reason.
	ids["adam"] = asCreateUser(t, srv, "adam", "administrator", asStrongPass)
	loc = bulk(url.Values{"action": {"block"}, "users[]": sid("alice", "adam")})
	if !strings.Contains(loc, "n=1") || !strings.Contains(loc, "failed=1") || !strings.Contains(loc, "error=admin_block") {
		t.Errorf("block redirect: %s", loc)
	}
	_, body, _ = get(t, fx.c, fx.base+loc)
	if !strings.Contains(body, "Administrators cannot be blocked.") || !strings.Contains(body, "1 member could not be changed.") {
		t.Error("block notices")
	}
	if st, _ := srv.forumMemberStats(ctx, []int64{ids["alice"], ids["adam"]}); !st[ids["alice"]].Blocked || st[ids["adam"]].Blocked {
		t.Error("block result")
	}
	bulk(url.Values{"action": {"-1"}, "action2": {"unblock"}, "users[]": sid("alice", "dave")})
	if st, _ := srv.forumMemberStats(ctx, []int64{ids["alice"], ids["dave"]}); st[ids["alice"]].Blocked || st[ids["dave"]].Blocked {
		t.Error("unblock via the bottom bulk select")
	}
	// Award and revoke a badge; a missing badge is an error.
	if loc := bulk(url.Values{"action": {"award"}, "users[]": sid("alice", "erin")}); !strings.Contains(loc, "error=no_badge") {
		t.Errorf("award without a badge: %s", loc)
	}
	bulk(url.Values{"action": {"award"}, "badge": {"founding-member"}, "users[]": sid("alice", "erin")})
	st, _ := srv.forumMemberStats(ctx, []int64{ids["alice"], ids["erin"]})
	if len(st[ids["erin"]].Manual) != 1 || st[ids["erin"]].Manual[0] != "founding-member" {
		t.Errorf("award: %+v", st[ids["erin"]].Manual)
	}
	bulk(url.Values{"action": {"revoke"}, "badge": {"founding-member"}, "users[]": sid("erin")})
	if st, _ := srv.forumMemberStats(ctx, []int64{ids["erin"]}); len(st[ids["erin"]].Manual) != 0 {
		t.Error("revoke")
	}
	// Forum role: erin becomes a moderator (her subscriber role stays).
	bulk(url.Values{"action": {"role_moderator"}, "users[]": sid("erin")})
	if u, _ := srv.store.UserByID(ctx, ids["erin"]); !u.Can(capForumModerate, nil) || !strings.Contains(strings.Join(u.Roles, ","), "subscriber") {
		t.Errorf("role change: %v", u.Roles)
	}
	if loc := bulk(url.Values{"action": {"verify"}}); !strings.Contains(loc, "error=no_members") {
		t.Errorf("no members selected: %s", loc)
	}
	// A row action link (GET with the nonce) blocks; it returns to the same list view.
	_, body, _ = get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&filter=all")
	m := forumAdminMemberActionRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Block row action")
	}
	for _, admin := range []string{"1", strconv.FormatInt(ids["adam"], 10)} {
		if strings.Contains(body, "do=block&amp;user="+admin+"&") {
			t.Errorf("a Block row action for administrator %s", admin)
		}
	}
	code, _, loc = get(t, fx.c, fx.base+html.UnescapeString(m[1]))
	if code != http.StatusFound || !strings.Contains(loc, "done=block") {
		t.Errorf("row block: %d %s", code, loc)
	}
	target, _ := strconv.ParseInt(m[2], 10, 64)
	if st, _ := srv.forumMemberStats(ctx, []int64{target}); !st[target].Blocked {
		t.Error("row block result")
	}
	if code, _, _ := get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&do=unblock&user="+m[2]); code != http.StatusForbidden {
		t.Errorf("row action without nonce: %d", code)
	}
	if code, _, _ := post(t, fx.c, fx.base+"/wp-admin/forum?tab=members", url.Values{"action": {"verify"}, "users[]": sid("carol")}); code != http.StatusForbidden {
		t.Errorf("bulk without nonce: %d", code)
	}

	// The member screen: level, stats, badges; hand-award by checkbox, then take it back.
	aid := strconv.FormatInt(ids["alice"], 10)
	code, body, _ = get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&action=member&user="+aid)
	if code != http.StatusOK || !strings.Contains(body, "Alice Archer") || !strings.Contains(body, "pf-admin-progress") || !strings.Contains(body, `name="badges[]" value="founding-member" checked`) {
		t.Fatalf("member screen: %d", code)
	}
	if !strings.Contains(body, "points to") {
		t.Error("member screen: no points-to-next text")
	}
	mnonce := formNonce(t, body)
	member := func(form url.Values) string {
		t.Helper()
		form.Set("_wpnonce", mnonce)
		form.Set("user", aid)
		code, _, loc := post(t, fx.c, fx.base+"/wp-admin/forum?tab=members&action=member&user="+aid, form)
		if code != http.StatusFound || !strings.Contains(loc, "action=member") {
			t.Fatalf("member %v: %d %s", form, code, loc)
		}
		return loc
	}
	if loc := member(url.Values{"member_do": {"badges"}, "badges[]": {"helpful", "prolific"}}); !strings.Contains(loc, "msg=member_badges") {
		t.Errorf("badges: %s", loc)
	}
	if st, _ := srv.forumMemberStats(ctx, []int64{ids["alice"]}); strings.Join(st[ids["alice"]].Manual, ",") != "helpful,prolific" {
		t.Errorf("manual badges after save: %v", st[ids["alice"]].Manual)
	}
	member(url.Values{"member_do": {"role"}, "forum_role": {forumRoleModerator}})
	if u, _ := srv.store.UserByID(ctx, ids["alice"]); !u.Can(capForumModerate, nil) {
		t.Error("member role")
	}
	member(url.Values{"member_do": {"block"}})
	if st, _ := srv.forumMemberStats(ctx, []int64{ids["alice"]}); !st[ids["alice"]].Blocked {
		t.Error("member block")
	}
	if code, _, loc := post(t, fx.c, fx.base+"/wp-admin/forum?tab=members", url.Values{"_wpnonce": {mnonce}, "member_do": {"role"}, "user": {"1"}, "forum_role": {forumRoleParticipant}}); code != http.StatusFound || !strings.Contains(loc, "error=self_role") {
		t.Errorf("own role: %d %s", code, loc)
	}
	if code, _, loc := get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&action=member&user=999999"); code != http.StatusFound || !strings.Contains(loc, "error=member_not_found") {
		t.Errorf("unknown member: %d %s", code, loc)
	}
}

// A Forum Moderator lists members, verifies and blocks participants, but cannot award badges, change
// roles or block another moderator.
func TestForumAdminMembersModerator(t *testing.T) {
	fx := forumAdminSeed(t)
	mod := hgSecondUser(t, fx.srv, fx.base, "mona", forumRoleModerator)
	code, body, _ := get(t, mod, fx.base+"/wp-admin/forum?tab=members")
	if code != http.StatusOK || !strings.Contains(body, ">Alice Archer</a>") {
		t.Fatalf("moderator members: %d", code)
	}
	if strings.Contains(body, `value="award"`) || strings.Contains(body, `value="role_moderator"`) || strings.Contains(body, `name="badge"`) {
		t.Error("the moderator is offered badge or role actions")
	}
	if strings.Contains(body, "do=block&amp;user="+strconv.FormatInt(fx.ids["bob"], 10)+"&") {
		t.Error("the moderator is offered to block another moderator")
	}
	// Without list_users the moderator sees no email addresses (list or member screen).
	if strings.Contains(body, "alice@example.test") || strings.Contains(body, "mailto:") {
		t.Error("the moderator sees members' email addresses")
	}
	if _, mb, _ := get(t, mod, fx.base+"/wp-admin/forum?tab=members&action=member&user="+strconv.FormatInt(fx.ids["alice"], 10)); strings.Contains(mb, "alice@example.test") {
		t.Error("the moderator sees an email address on the member screen")
	}
	// Nor can the search tell them which addresses contain a string (an email oracle).
	if _, sb, _ := get(t, mod, fx.base+"/wp-admin/forum?tab=members&s=alice%40example"); strings.Contains(sb, ">Alice Archer</a>") {
		t.Error("the moderator's member search matches email addresses")
	}
	if _, sb, _ := get(t, fx.c, fx.base+"/wp-admin/forum?tab=members&s=alice%40example"); !strings.Contains(sb, ">Alice Archer</a>") {
		t.Error("the administrator's member search no longer matches email addresses")
	}
	nonce := formNonce(t, body)
	ids := []string{strconv.FormatInt(fx.ids["alice"], 10)}
	for _, op := range []string{"award", "revoke", "role_moderator", "role_participant"} {
		if code, _, _ := post(t, mod, fx.base+"/wp-admin/forum?tab=members", url.Values{"_wpnonce": {nonce}, "action": {op}, "badge": {"founding-member"}, "users[]": ids}); code != http.StatusForbidden {
			t.Errorf("moderator %s: %d", op, code)
		}
	}
	code, _, loc := post(t, mod, fx.base+"/wp-admin/forum?tab=members", url.Values{"_wpnonce": {nonce}, "action": {"block"},
		"users[]": {ids[0], strconv.FormatInt(fx.ids["bob"], 10)}})
	if code != http.StatusFound || !strings.Contains(loc, "n=1") || !strings.Contains(loc, "error=mod_block") {
		t.Errorf("moderator block: %d %s", code, loc)
	}
	// The member screen: no badge or role forms.
	_, body, _ = get(t, mod, fx.base+"/wp-admin/forum?tab=members&action=member&user="+ids[0])
	if strings.Contains(body, `name="member_do" value="badges"`) || strings.Contains(body, `name="member_do" value="role"`) {
		t.Error("the moderator sees the badge or role form")
	}
	if code, _, _ := post(t, mod, fx.base+"/wp-admin/forum?tab=members", url.Values{"_wpnonce": {nonce}, "member_do": {"badges"}, "user": {ids[0]}, "badges[]": {"helpful"}}); code != http.StatusForbidden {
		t.Errorf("moderator member badges: %d", code)
	}
}

var forumAdminBadgeActionRe = regexp.MustCompile(`href="(/wp-admin/forum\?[^"]*badge=([a-z0-9-]+)[^"]*do=(up|down|disable|enable|delete)[^"]*)"`)

// Badges & Levels: badge add / validation / edit / reorder / disable / delete / restore, levels and
// points save, add and remove rows, restore defaults.
func TestForumAdminBadges(t *testing.T) {
	base, c, srv := forumAdminSite(t)
	ctx := context.Background()
	code, body, _ := get(t, c, base+"/wp-admin/forum?tab=badges")
	if code != http.StatusOK || !strings.Contains(body, "Add New Badge") || !strings.Contains(body, `id="pf-level-template"`) {
		t.Fatalf("badges tab: %d", code)
	}
	if n := strings.Count(body, `name="icon" value="`); n != len(forumIconNames())+1 {
		t.Errorf("icon picker radios: %d", n)
	}
	for _, want := range []string{"pf-badge--first-post", "Conversation Starter", `value="newcomer"`, `name="level_5_name" value="Legend"`, "media-views"} {
		if !strings.Contains(body, want) {
			t.Errorf("badges tab misses %q", want)
		}
	}
	nonce := formNonce(t, body)
	send := func(form url.Values) (int, string, string) {
		t.Helper()
		form.Set("_wpnonce", nonce)
		return post(t, c, base+"/wp-admin/forum?tab=badges", form)
	}

	// Add: a validation error keeps the form; then a valid badge (script in the name is plain text).
	code, body, _ = send(url.Values{"do": {"save_badge"}, "name": {"Bug Hunter"}, "rule": {"replies"}, "threshold": {"0"}, "icon": {"target"}, "color": {"#d63638"}})
	if code != http.StatusOK || !strings.Contains(body, "Please enter a number of at least 1 for this rule.") || !strings.Contains(body, `value="Bug Hunter"`) || !strings.Contains(body, `name="icon" value="target" checked`) {
		t.Fatalf("invalid badge: %d", code)
	}
	code, _, loc := send(url.Values{"do": {"save_badge"}, "name": {"<script>alert(1)</script>Bug Hunter"}, "description": {"Found 10 bugs."}, "rule": {"replies"},
		"threshold": {"10"}, "icon": {"target"}, "color": {"#d63638"}, "enabled": {"1"}})
	if code != http.StatusFound || !strings.Contains(loc, "msg=badge_added") {
		t.Fatalf("add badge: %d %s", code, loc)
	}
	var added forumBadge
	for _, b := range srv.forumBadges(ctx) {
		if strings.Contains(b.Name, "Bug Hunter") {
			added = b
		}
	}
	if added.ID == "" || added.Rule != forumRuleReplies || added.Threshold != 10 || added.Icon != "target" || added.Color != "#d63638" || !added.Enabled {
		t.Fatalf("added badge: %+v", added)
	}
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=badges")
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("the badge name is not escaped")
	}
	// Edit it.
	code, body, _ = get(t, c, base+"/wp-admin/forum?tab=badges&action=edit&badge="+added.ID)
	if code != http.StatusOK || !strings.Contains(body, "Update Badge") || !strings.Contains(body, `name="badge_id" value="`+added.ID+`"`) {
		t.Fatalf("edit screen: %d", code)
	}
	code, _, loc = post(t, c, base+"/wp-admin/forum?tab=badges&action=edit&badge="+added.ID, url.Values{"_wpnonce": {nonce}, "do": {"save_badge"},
		"badge_id": {added.ID}, "name": {"Bug Hunter"}, "rule": {"manual"}, "threshold": {"10"}, "icon": {""}, "color": {"#000000"}})
	if code != http.StatusFound || !strings.Contains(loc, "msg=badge_updated") {
		t.Fatalf("update badge: %d %s", code, loc)
	}
	if b, _ := srv.forumBadgeByID(ctx, added.ID); b.Name != "Bug Hunter" || b.Rule != forumRuleManual || b.Threshold != 0 || b.Icon != "" || b.Enabled {
		t.Errorf("updated badge: %+v", b)
	}
	if code, _, loc := send(url.Values{"do": {"save_badge"}, "badge_id": {"no-such-badge"}, "name": {"X"}, "rule": {"manual"}}); code != http.StatusFound || !strings.Contains(loc, "error=badge_not_found") {
		t.Errorf("edit unknown badge: %d %s", code, loc)
	}

	// Row actions: move the last badge up, enable it, delete it.
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=badges")
	links := map[string]string{}
	for _, m := range forumAdminBadgeActionRe.FindAllStringSubmatch(body, -1) {
		if m[2] == added.ID {
			links[m[3]] = html.UnescapeString(m[1])
		}
	}
	if links["up"] == "" || links["enable"] == "" || links["delete"] == "" || links["down"] != "" {
		t.Fatalf("row actions of the last badge: %v", links)
	}
	get(t, c, base+links["up"])
	list := srv.forumBadges(ctx)
	if list[len(list)-2].ID != added.ID {
		t.Error("move up")
	}
	get(t, c, base+links["enable"])
	if b, _ := srv.forumBadgeByID(ctx, added.ID); !b.Enabled {
		t.Error("enable")
	}
	_ = srv.forumAward(ctx, added.ID, 1)
	if code, _, loc := get(t, c, base+links["delete"]); code != http.StatusFound || !strings.Contains(loc, "msg=badge_deleted") {
		t.Errorf("delete: %d %s", code, loc)
	}
	if _, ok := srv.forumBadgeByID(ctx, added.ID); ok {
		t.Error("badge not deleted")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/forum?tab=badges&do=delete&badge=helpful"); code != http.StatusForbidden {
		t.Errorf("delete without nonce: %d", code)
	}

	// Restore defaults: a deleted default comes back, an edited default is reset, own badges stay.
	if err := srv.forumDeleteBadge(ctx, "helpful"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.forumSaveBadge(ctx, forumBadge{ID: "verified", Name: "Checked", Rule: forumRuleVerified, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.forumSaveBadge(ctx, forumBadge{Name: "Own badge", Rule: forumRuleManual, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	send(url.Values{"do": {"restore_badges"}})
	got := map[string]forumBadge{}
	for _, b := range srv.forumBadges(ctx) {
		got[b.ID] = b
	}
	if _, ok := got["helpful"]; !ok || got["verified"].Name != "Verified" || !got["verified"].Enabled || got["own-badge"].Name != "Own badge" {
		t.Errorf("restore badges: %+v", got)
	}

	// Levels: rename one, remove one, add one, change the weights.
	form := url.Values{"do": {"save_levels"}, "topic_points": {"5"}, "reply_points": {"2"}, "level_count": {"7"}}
	for i, l := range forumDefaultLevels().Levels {
		k := "level_" + strconv.Itoa(i) + "_"
		form.Set(k+"id", l.ID)
		form.Set(k+"name", l.Name)
		form.Set(k+"min", strconv.Itoa(l.Min))
		form.Set(k+"icon", l.Icon)
		form.Set(k+"color", l.Color)
	}
	form.Set("level_1_name", "Friend")
	form.Set("level_4_remove", "1")
	form.Set("level_6_name", "Mythic")
	form.Set("level_6_min", "5000")
	form.Set("level_6_icon", "gem")
	form.Set("level_6_color", "#ff00aa")
	if code, _, loc := send(form); code != http.StatusFound || !strings.Contains(loc, "msg=levels_saved") {
		t.Fatalf("save levels: %d %s", code, loc)
	}
	lv := srv.forumLevelsLoad(ctx)
	var names []string
	for _, l := range lv.Levels {
		names = append(names, l.Name)
	}
	if lv.TopicPoints != 5 || lv.ReplyPoints != 2 || strings.Join(names, ",") != "Newcomer,Friend,Regular,Contributor,Legend,Mythic" || lv.Levels[1].ID != "member" || lv.Levels[5].Icon != "gem" {
		t.Errorf("levels after save: %+v", lv)
	}
	if code, _, loc := send(url.Values{"do": {"save_levels"}, "level_count": {"1"}, "level_0_name": {"  "}}); code != http.StatusFound || !strings.Contains(loc, "error=levels_empty") {
		t.Errorf("no levels: %d %s", code, loc)
	}
	// A named level without minimum points, or two levels with the same minimum, is refused (not dropped).
	before := srv.forumLevelsLoad(ctx)
	for _, tc := range []struct{ min1, want string }{{"", "error=levels_min"}, {"0", "error=levels_same_min"}} {
		f := url.Values{"do": {"save_levels"}, "level_count": {"2"}, "level_0_name": {"Newcomer"}, "level_0_min": {"0"},
			"level_1_name": {"Rookie"}, "level_1_min": {tc.min1}}
		if code, _, loc := send(f); code != http.StatusFound || !strings.Contains(loc, tc.want) {
			t.Errorf("level min %q: %d %s", tc.min1, code, loc)
		}
	}
	if after := srv.forumLevelsLoad(ctx); len(after.Levels) != len(before.Levels) {
		t.Errorf("a refused levels form was saved: %+v", after)
	}
	send(url.Values{"do": {"restore_levels"}})
	if lv := srv.forumLevelsLoad(ctx); len(lv.Levels) != 6 || lv.TopicPoints != 3 || lv.Levels[1].Name != "Member" {
		t.Errorf("restore levels: %+v", lv)
	}
	if code, _, _ := post(t, c, base+"/wp-admin/forum?tab=badges", url.Values{"do": {"restore_levels"}}); code != http.StatusForbidden {
		t.Errorf("levels without nonce: %d", code)
	}
}

// The Settings tab saves the member list switch.
func TestForumAdminShowMembers(t *testing.T) {
	base, c, srv := forumAdminSite(t)
	if !srv.forumLoad().ShowMembers {
		t.Fatal("the member list is off by default")
	}
	nonce := forumAdminNonceOf(t, c, base, "settings")
	post(t, c, base+"/wp-admin/forum?tab=settings", url.Values{"_wpnonce": {nonce}, "base": {"forums"}, "title": {"Forums"}})
	if srv.forumLoad().ShowMembers {
		t.Error("show_members not cleared")
	}
	_, body, _ := get(t, c, base+"/wp-admin/forum?tab=settings")
	if !strings.Contains(body, `name="show_members"`) || strings.Contains(body, `id="pf-show-members" type="checkbox" value="1" checked`) {
		t.Error("settings checkbox")
	}
	post(t, c, base+"/wp-admin/forum?tab=settings", url.Values{"_wpnonce": {nonce}, "base": {"forums"}, "title": {"Forums"}, "show_members": {"1"}})
	if !srv.forumLoad().ShowMembers {
		t.Error("show_members not set")
	}
}

// The member list state helpers keep only known values.
func TestForumAdminMemberState(t *testing.T) {
	q := url.Values{"filter": {"blocked"}, "s": {" bob "}, "orderby": {"points"}, "order": {"asc"}, "paged": {"3"}}
	st := forumAdminMemberState(q.Get)
	if got := forumAdminURL(forumTabMembers, st...); got != "/wp-admin/forum?filter=blocked&order=asc&orderby=points&paged=3&s=bob&tab=members" {
		t.Errorf("state URL: %s", got)
	}
	bad := url.Values{"filter": {"all"}, "orderby": {"u.id; DROP"}, "order": {"asc"}, "paged": {"-1"}}
	if got := forumAdminURL(forumTabMembers, forumAdminMemberState(bad.Get)...); got != "/wp-admin/forum?tab=members" {
		t.Errorf("bad state URL: %s", got)
	}
	links := forumAdminMemberSortLinks(forumAdminMemberState(url.Values{}.Get))
	if links["joined"].Class != "sorted desc" || links["points"].Class != "sortable asc" || !strings.Contains(links["points"].URL, "order=desc") || !strings.Contains(links["name"].URL, "order=asc") {
		t.Errorf("sort links: %+v", links)
	}
	if forumAdminPct(0, 10) != 0 || forumAdminPct(1, 1000) != 2 || forumAdminPct(10, 10) != 100 || forumAdminPct(5, 10) != 50 {
		t.Error("bar heights")
	}
}
