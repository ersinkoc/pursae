package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The WP7 front end: the member list (search, sort, pages, noindex search, the setting), levels and
// badges next to post authors and on profiles, and an administrator awarding and revoking a badge
// from a profile.
func TestForumMembersFront(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	_, alice := fx.member(t, "alice", "forum_participant")
	modClient, bob := fx.member(t, "bob", "forum_moderator")
	fx.member(t, "carol", "forum_participant") // never posts: not listed while enumeration is blocked
	_, eve := fx.member(t, "eve", "forum_participant")
	var lead *forumTopic
	for i := 0; i < 4; i++ {
		tp, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: alice, Title: "Alice topic " + string(rune('A'+i)), Content: "<p>Hello there</p>"})
		if err != nil {
			t.Fatal(err)
		}
		if lead == nil {
			lead = tp
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: lead.ID, AuthorID: alice, Content: "<p>More from me</p>"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: lead.ID, AuthorID: bob, Content: "<p>Moderator note</p>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: eve, Title: "Eve topic", Content: "<p>soon blocked</p>"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetUserCapExceptions(ctx, eve, map[string]string{capForumParticipate: "deny"}); err != nil {
		t.Fatal(err)
	}
	srv.forumPurge(ctx)

	// The list: posters only, no email, no blocked member; level and badges; canonical, indexable.
	members := fx.url("members")
	code, body, hdr := getHdr(t, fx.anon, members)
	if code != http.StatusOK {
		t.Fatalf("members: %d", code)
	}
	forumNoTemplateError(t, "members", body)
	wbtfWant(t, "members", body, `data-view="members"`, `class="pf-member-grid"`, ">Alice</a>", ">Bob</a>", "pf-level--member",
		"First Post", ">Verified<", `rel="canonical" href="`+members+`"`, "pf-view-members", `class="pf-sort-link is-current"`)
	wbtfBad(t, "members", body, ">Carol</a>", ">Eve</a>", "@example.test", "noindex", "_wpnonce")
	if strings.Contains(hdr.Get("Cache-Control"), "no-store") {
		t.Error("the plain member list is no-store")
	}
	// Alice (14 points: Member) and Bob's Moderator label.
	if !regexp.MustCompile(`(?s)>Alice</a>.*?pf-level--member`).MatchString(body) {
		t.Error("Alice has no Member level chip")
	}

	// Search: noindex, no canonical, no-store; ?q kept on the sort links.
	code, body, hdr = getHdr(t, fx.anon, members+"?q=ali")
	if code != http.StatusOK || !strings.Contains(body, ">Alice</a>") || strings.Contains(body, ">Bob</a>") {
		t.Fatalf("member search: %d", code)
	}
	wbtfWant(t, "member search", body, "noindex", "1 member found", `href="`+members+`?q=ali&amp;sort=name"`)
	wbtfBad(t, "member search", body, `rel="canonical"`)
	if hdr.Get("X-Robots-Tag") != "noindex" || !strings.Contains(hdr.Get("Cache-Control"), "no-store") {
		t.Errorf("member search headers: %v", hdr)
	}
	if _, body, _ := get(t, fx.anon, members+"?q=nobody-like-this"); !strings.Contains(body, "No members match your search.") {
		t.Error("empty member search")
	}
	// Sorts: name puts Alice before Bob, newest Bob (registered later) first.
	_, byName, _ := get(t, fx.anon, members+"?sort=name")
	_, byNew, _ := get(t, fx.anon, members)
	if strings.Index(byName, ">Alice</a>") > strings.Index(byName, ">Bob</a>") {
		t.Error("sort=name: Bob before Alice")
	}
	if strings.Index(byNew, ">Bob</a>") > strings.Index(byNew, ">Alice</a>") {
		t.Error("newest: Alice before Bob")
	}
	if code, _, _ := get(t, fx.anon, members+"?sort=email"); code != http.StatusOK {
		t.Errorf("unknown sort: %d", code)
	}
	// Pages.
	if code, _, _ := get(t, fx.anon, members+"page/2/"); code != http.StatusNotFound {
		t.Errorf("members page 2: %d, want 404", code)
	}
	if code, _, loc := get(t, fx.anon, members+"page/1/"); code != http.StatusMovedPermanently || !strings.HasSuffix(loc, "/forums/members/") {
		t.Errorf("members page 1: %d %s", code, loc)
	}
	// The index links the list.
	if _, idx, _ := get(t, fx.anon, fx.url()); !strings.Contains(idx, `href="`+members+`"`) {
		t.Error("the index does not link the member list")
	}

	// Post authors: avatar, level, badges, the Author label on the lead post too.
	_, topic, _ := get(t, fx.anon, lead.URL)
	forumNoTemplateError(t, "topic", topic)
	wbtfWant(t, "topic", topic, `class="pf-post-author-level"`, `pf-post-author-badges`, `pf-avatar--initials`, "6 posts", "pf-level--member")
	leadPost := topic[strings.Index(topic, `id="post-`+itoa64(lead.ID)+`"`):]
	leadPost = leadPost[:strings.Index(leadPost, "</article>")]
	if !strings.Contains(leadPost, "pf-badge--op") {
		t.Error("the lead post has no Author label")
	}

	// Profile: level, progress to the next level, badges with descriptions, counts.
	_, prof, _ := get(t, fx.anon, fx.url("u", "alice"))
	forumNoTemplateError(t, "profile", prof)
	wbtfWant(t, "profile", prof, `class="pf-card pf-profile"`, `class="pf-level-progress"`, "--pf-progress:", "36 points to Regular",
		"14 points", "Wrote a first topic or reply.", `class="pf-badge-list"`, "Last active", "<dd>14</dd>", `href="`+members+`"`)
	wbtfBad(t, "profile", prof, "Award badge")

	// Award and revoke a manual badge (administrators only).
	_, adminProf, _ := get(t, fx.admin, fx.url("u", "alice"))
	if !strings.Contains(adminProf, "Award badge") || !strings.Contains(adminProf, `value="founding-member"`) {
		t.Fatal("the administrator has no badge form on the profile")
	}
	nonce := forumFormNonce(t, adminProf, "badge_award")
	code, _, loc := post(t, fx.admin, fx.url("u", "alice"), url.Values{"forum_action": {"badge_award"}, "_wpnonce": {nonce}, "badge": {"founding-member"}})
	if code != http.StatusFound || !strings.Contains(loc, "forum_msg=badge_awarded") {
		t.Fatalf("award: %d %s", code, loc)
	}
	_, prof, _ = get(t, fx.anon, fx.url("u", "alice"))
	if !strings.Contains(prof, "Founding Member") {
		t.Error("the awarded badge is not on the profile")
	}
	_, adminProf, _ = get(t, fx.admin, fx.url("u", "alice")+"?forum_msg=badge_awarded")
	wbtfWant(t, "admin profile", adminProf, "The badge is awarded.", `name="badge" value="founding-member"`)
	code, _, loc = post(t, fx.admin, fx.url("u", "alice"), url.Values{"forum_action": {"badge_revoke"}, "_wpnonce": {forumFormNonce(t, adminProf, "badge_revoke")}, "badge": {"founding-member"}})
	if code != http.StatusFound || !strings.Contains(loc, "forum_msg=badge_revoked") {
		t.Fatalf("revoke: %d %s", code, loc)
	}
	if _, prof, _ = get(t, fx.anon, fx.url("u", "alice")); strings.Contains(prof, "Founding Member") {
		t.Error("the revoked badge is still on the profile")
	}
	if code, body, _ := post(t, fx.admin, fx.url("u", "alice"), url.Values{"forum_action": {"badge_award"}, "_wpnonce": {nonce}, "badge": {"no-such-badge"}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Please choose a badge.") {
		t.Errorf("unknown badge: %d", code)
	}
	// A badge without an icon or image gets the fallback icon where only the picture shows (escaped name).
	plain, err := srv.forumSaveBadge(ctx, forumBadge{Name: `Plain <b>one</b>`, Rule: forumRuleManual, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.forumAward(ctx, plain.ID, alice); err != nil {
		t.Fatal(err)
	}
	_, prof, _ = get(t, fx.anon, fx.url("u", "alice"))
	wbtfWant(t, "plain badge profile", prof, "Plain &lt;b&gt;one&lt;/b&gt;", "pf-icon--"+forumBadgeFallbackIcon)
	wbtfBad(t, "plain badge profile", prof, "<b>one</b>")
	_, mem, _ := get(t, fx.anon, members)
	if !regexp.MustCompile(`pf-badge--` + plain.ID + `"[^>]*><svg class="pf-icon pf-icon--` + forumBadgeFallbackIcon).MatchString(mem) {
		t.Error("the icon-less badge has no fallback icon in the member card")
	}
	if err := srv.forumDeleteBadge(ctx, plain.ID); err != nil {
		t.Fatal(err)
	}

	// A moderator may not award badges.
	_, modProf, _ := get(t, modClient, fx.url("u", "alice"))
	wbtfBad(t, "moderator profile", modProf, "Award badge")
	if code, _, _ := post(t, modClient, fx.url("u", "alice"), url.Values{"forum_action": {"badge_award"}, "_wpnonce": {forumFormNonce(t, modProf, "block")}, "badge": {"founding-member"}}); code != http.StatusForbidden {
		t.Errorf("moderator award: %d, want 403", code)
	}

	// The setting: off → 404 and no link.
	fx.settings(t, func(st *forumSettings) { st.ShowMembers = false })
	if code, _, _ := get(t, fx.anon, members); code != http.StatusNotFound {
		t.Errorf("members while off: %d", code)
	}
	if _, idx, _ := get(t, fx.anon, fx.url()); strings.Contains(idx, `href="`+members+`"`) {
		t.Error("the index links the member list while it is off")
	}
}
