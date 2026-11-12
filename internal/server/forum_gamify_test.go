package server

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Unit tests -------------------------------------------------------------------------------------------

func TestForumLevelThresholds(t *testing.T) {
	levels := forumDefaultLevels().Levels
	cases := []struct {
		points   int
		want     string
		next     string
		progress int
	}{
		{0, "newcomer", "member", 0}, {9, "newcomer", "member", 90}, {10, "member", "regular", 0},
		{49, "member", "regular", 97}, {50, "regular", "contributor", 0}, {399, "contributor", "veteran", 99},
		{1000, "legend", "", 100}, {99999, "legend", "", 100}, {-5, "newcomer", "member", 0},
	}
	for _, c := range cases {
		cur, next, p := forumLevelFor(levels, c.points)
		nx := ""
		if next != nil {
			nx = next.ID
		}
		if cur.ID != c.want || nx != c.next || p != c.progress {
			t.Errorf("%d points: %s→%s %d%%, want %s→%s %d%%", c.points, cur.ID, nx, p, c.want, c.next, c.progress)
		}
	}
	if l, n, p := forumLevelFor(nil, 5); l.ID != "" || n != nil || p != 100 {
		t.Error("no levels")
	}
}

func TestForumSanitizeLevels(t *testing.T) {
	in := forumLevels{TopicPoints: -4, ReplyPoints: 5000, Levels: []forumLevel{
		{Name: "Pro", Min: 100, Color: "red", Icon: "nope"},
		{Name: "  ", Min: 5},
		{Name: "Starter", Min: 3, Color: "#ABC", Icon: "star"},
		{Name: "Pro again", Min: 100},
		{Name: "Pro", Min: 200, ID: "bad id!"},
		{Name: "<b>Mid</b>\x00", Min: 50, ImageID: -3},
	}}
	got := forumSanitizeLevels(in)
	if got.TopicPoints != 0 || got.ReplyPoints != 1000 {
		t.Errorf("points: %+v", got)
	}
	var ids []string
	for _, l := range got.Levels {
		ids = append(ids, l.ID)
	}
	if strings.Join(ids, ",") != "starter,b-mid-b,pro,pro-2" {
		t.Errorf("ids: %v", ids)
	}
	if got.Levels[0].Min != 0 || got.Levels[0].Color != "#abc" || got.Levels[0].Icon != "star" {
		t.Errorf("first level: %+v", got.Levels[0])
	}
	if got.Levels[2].Color != forumDefaultColor || got.Levels[2].Icon != "" || got.Levels[1].ImageID != 0 {
		t.Errorf("sanitized: %+v %+v", got.Levels[1], got.Levels[2])
	}
	if d := forumSanitizeLevels(forumLevels{}); len(d.Levels) != 6 || d.Levels[0].ID != "newcomer" {
		t.Errorf("empty list → defaults: %+v", d.Levels)
	}
}

func TestForumBadgeRules(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	list := forumDefaultBadges()
	ids := func(bs []forumBadge) string {
		var out []string
		for _, b := range bs {
			out = append(out, b.ID)
		}
		return strings.Join(out, ",")
	}
	fresh := &forumMemberStat{Joined: now.Add(-24 * time.Hour)}
	if got := ids(forumBadgesEarned(list, fresh, now)); got != "" {
		t.Errorf("unverified newcomer: %q", got)
	}
	st := &forumMemberStat{Topics: 5, Replies: 50, Verified: true, Joined: now.AddDate(-1, 0, -1)}
	if got := ids(forumBadgesEarned(list, st, now)); got != "first-post,conversation-starter,helpful,one-year-club,verified" {
		t.Errorf("active member: %q", got)
	}
	st = &forumMemberStat{Topics: 100, Replies: 150, Moderator: true, Manual: []string{"founding-member", "gone"}, Joined: now}
	if got := ids(forumBadgesEarned(list, st, now)); got != "first-post,conversation-starter,helpful,prolific,moderator,founding-member" {
		t.Errorf("moderator with manual award: %q", got)
	}
	// Disabled badges are neither earned nor awarded; points / google rules.
	list[0].Enabled = false
	extra := []forumBadge{{ID: "pts", Rule: forumRulePoints, Threshold: 30, Enabled: true}, {ID: "g", Rule: forumRuleGoogle, Enabled: true}}
	st = &forumMemberStat{Topics: 1, Points: 30, Google: true, Manual: []string{"first-post"}}
	if got := ids(forumBadgesEarned(append(list, extra...), st, now)); got != "pts,g" {
		t.Errorf("disabled/points/google: %q", got)
	}
	if forumBadgesEarned(list, nil, now) != nil {
		t.Error("nil stats")
	}
}

func TestForumSanitizeBadge(t *testing.T) {
	used := map[string]bool{"helpful": true}
	if _, err := forumSanitizeBadge(forumBadge{Name: " ", Rule: forumRuleManual}, used); err == nil {
		t.Error("empty name accepted")
	}
	if _, err := forumSanitizeBadge(forumBadge{Name: "X", Rule: "sql'; drop"}, used); err == nil {
		t.Error("unknown rule accepted")
	}
	if _, err := forumSanitizeBadge(forumBadge{Name: "X", Rule: forumRuleTopics}, used); err == nil {
		t.Error("threshold rule without N")
	}
	b, err := forumSanitizeBadge(forumBadge{Name: "Helpful", Rule: forumRuleVerified, Threshold: 9, Color: "#GGG", Icon: "x"}, used)
	if err != nil || b.ID != "helpful-2" || b.Threshold != 0 || b.Color != forumDefaultColor || b.Icon != "" {
		t.Errorf("badge: %+v %v", b, err)
	}
}

func TestForumIcons(t *testing.T) {
	names := forumIconNames()
	if len(names) < 40 {
		t.Errorf("%d icons", len(names))
	}
	for _, n := range names {
		svg := string(forumIcon(n, 24))
		d := xml.NewDecoder(strings.NewReader(svg))
		var root string
		for {
			tok, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("icon %s: %v", n, err)
				break
			}
			if se, ok := tok.(xml.StartElement); ok && root == "" {
				root = se.Name.Local
			}
		}
		if root != "svg" || !strings.Contains(svg, `viewBox="0 0 24 24"`) || !strings.Contains(svg, `stroke="currentColor"`) {
			t.Errorf("icon %s: %s", n, svg)
		}
	}
	if forumIcon("nope", 16) != "" || forumIcon("<script>", 16) != "" {
		t.Error("unknown icon rendered")
	}
	if !strings.Contains(string(forumIcon("star", 0)), `width="16"`) {
		t.Error("default size")
	}
}

func TestForumChipHTML(t *testing.T) {
	h := string(forumBadgeHTML(forumBadge{ID: "x", Name: `<script>alert(1)</script>`, Description: `"><img onerror=x>`, Color: `red;background:url(x)`, Icon: "star"}))
	if strings.Contains(h, "<script>") || strings.Contains(h, `"><img`) || strings.Contains(h, "url(x)") {
		t.Errorf("not escaped: %s", h)
	}
	if !strings.Contains(h, `class="pf-badge-chip pf-badge pf-badge--x"`) || !strings.Contains(h, `--pf-chip:`+forumDefaultColor) || !strings.Contains(h, "pf-icon--star") {
		t.Errorf("chip: %s", h)
	}
	h = string(forumBadgeHTML(forumBadge{ID: "bad id", Name: "B", ImageURL: `/a.png?x="y"`, Icon: "star", Color: "#ABCDEF"}))
	if strings.Contains(h, "pf-badge--bad") || !strings.Contains(h, `src="/a.png?x=&#34;y&#34;"`) || strings.Contains(h, "pf-icon") || !strings.Contains(h, "#abcdef") {
		t.Errorf("image chip: %s", h)
	}
	l := string(forumLevelHTML(&forumLevel{ID: "legend", Name: "Legend", Color: "#7f54b3", Icon: "trophy"}))
	if !strings.Contains(l, `class="pf-badge-chip pf-level pf-level--legend"`) {
		t.Errorf("level chip: %s", l)
	}
	if forumLevelHTML(nil) != "" || forumBadgeHTML(forumBadge{}) != "" {
		t.Error("empty chips")
	}
}

func TestForumInitialsAvatar(t *testing.T) {
	for _, c := range [][3]string{{"Ada Lovelace", "ada", "AL"}, {"ada", "x", "A"}, {"  ", "bob_smith", "BS"}, {"çağrı öz", "", "ÇÖ"}, {"!!", "--", "?"}, {"Jean-Luc Picard", "", "JP"}} {
		if got := forumInitials(c[0], c[1]); got != c[2] {
			t.Errorf("initials(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	a := &forumAuthor{ID: 7, DisplayName: "<b>X</b>", Initials: "<B"}
	h := string(forumAvatarHTML(a, 48))
	if !strings.Contains(h, `pf-avatar--initials`) || !strings.Contains(h, "&lt;B") || !strings.Contains(h, "--pf-av-size:48px") || !strings.Contains(h, "hsl(") {
		t.Errorf("initials avatar: %s", h)
	}
	a.HasPhoto, a.AvatarURL = true, `/u/a.jpg"onload="x`
	h = string(forumAvatarHTML(a, 48))
	if !strings.HasPrefix(h, `<img class="pf-avatar avatar"`) || strings.Contains(h, `"onload`) {
		t.Errorf("photo avatar: %s", h)
	}
	if !strings.Contains(string(forumAvatarHTML(nil, 0)), "pf-avatar--empty") {
		t.Error("nil author")
	}
	if forumHue(1) == forumHue(2) || forumHue(-3) < 0 {
		t.Error("hue")
	}
}

func TestForumMemberQueryWhitelist(t *testing.T) {
	q := forumNormalizeMemberQuery(forumMemberQuery{Sort: "name; DROP TABLE users", Order: "desc, id", Filter: "blocked", PerPage: 1000, Page: -2})
	if q.Sort != "newest" || q.Order != "" || q.Filter != "all" || q.PerPage != 100 || q.Page != 1 {
		t.Errorf("front query: %+v", q)
	}
	q = forumNormalizeMemberQuery(forumMemberQuery{Sort: "points", Order: "asc", Filter: "blocked", Admin: true})
	if q.Sort != "points" || q.Order != "asc" || q.Filter != "blocked" || q.PerPage != 20 {
		t.Errorf("admin query: %+v", q)
	}
	for k, v := range forumMemberSorts {
		if strings.Contains(v.expr, ";") || strings.Contains(v.expr, "$") {
			t.Errorf("sort %s: %s", k, v.expr)
		}
	}
	if rt, ok := forumParsePath("forums", "forums/members"); !ok || rt.View != forumViewMembers || rt.Page != 1 {
		t.Errorf("members route: %+v %v", rt, ok)
	}
	if rt, ok := forumParsePath("forums", "forums/members/page/3"); !ok || rt.View != forumViewMembers || rt.Page != 3 {
		t.Errorf("members page 3: %+v %v", rt, ok)
	}
	if _, ok := forumParsePath("forums", "forums/members/x"); ok {
		t.Error("members/x parsed")
	}
}

// Integration ------------------------------------------------------------------------------------------

func TestForumGamifyIntegration(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	_, alice := fx.member(t, "alice", "forum_participant")
	_, bob := fx.member(t, "bob", "forum_moderator")
	_, carol := fx.member(t, "carol", "forum_participant")
	_, dave := fx.member(t, "dave", "subscriber")
	_, eve := fx.member(t, "eve", "forum_participant")
	if err := srv.forumSetUserMeta(ctx, carol, map[string]any{forumUMetaUnverified: true}); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, dave, map[string]any{forumUMetaGoogleSub: "g-123"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetUserCapExceptions(ctx, eve, map[string]string{capForumParticipate: "deny"}); err != nil {
		t.Fatal(err)
	}
	// alice: 4 topics, 3 replies (one spam) → 4*3+2 = 14 points; bob: 1 reply; carol: one pending topic.
	var first *forumTopic
	for i := 0; i < 4; i++ {
		tp, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: alice, Title: "Topic " + string(rune('A'+i)), Content: "<p>x</p>"})
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = tp
		}
	}
	var spamID int64
	for i := 0; i < 3; i++ {
		r, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: first.ID, AuthorID: alice, Content: "<p>r</p>"})
		if err != nil {
			t.Fatal(err)
		}
		spamID = r.ID
	}
	if err := srv.forumModerate(ctx, spamID, "spam", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: first.ID, AuthorID: bob, Content: "<p>mod</p>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: carol, Title: "Pending", Content: "<p>p</p>", Pending: true}); err != nil {
		t.Fatal(err)
	}

	stats, err := srv.forumMemberStats(ctx, []int64{alice, bob, carol, dave, eve, 999999})
	if err != nil {
		t.Fatal(err)
	}
	if st := stats[alice]; st == nil || st.Topics != 4 || st.Replies != 2 || st.Points != 14 || !st.Verified || st.LastActive.IsZero() {
		t.Errorf("alice stats: %+v", st)
	}
	if st := stats[bob]; st == nil || !st.Moderator || st.Points != 1 {
		t.Errorf("bob stats: %+v", st)
	}
	if st := stats[carol]; st == nil || st.Verified || st.Topics != 0 {
		t.Errorf("carol stats: %+v", st)
	}
	if st := stats[dave]; st == nil || !st.Google || st.Moderator {
		t.Errorf("dave stats: %+v", st)
	}
	if st := stats[eve]; st == nil || !st.Blocked {
		t.Errorf("eve stats: %+v", st)
	}
	if stats[999999] != nil {
		t.Error("unknown user has stats")
	}

	// Authors carry level and badges (batched through forumAuthors).
	authors, err := srv.forumAuthors(ctx, []int64{alice, bob})
	if err != nil {
		t.Fatal(err)
	}
	badgeIDs := func(a *forumAuthor) string {
		var out []string
		for _, b := range a.Badges {
			out = append(out, b.ID)
		}
		return strings.Join(out, ",")
	}
	if a := authors[alice]; !a.Stats || a.Level == nil || a.Level.ID != "member" || a.NextLevel == nil || a.NextLevel.ID != "regular" || a.Points != 14 ||
		badgeIDs(a) != "first-post,verified" || a.Initials != "A" || a.HasPhoto {
		t.Errorf("alice author: %+v level %+v badges %s", a, a.Level, badgeIDs(a))
	}
	if b := authors[bob]; badgeIDs(b) != "first-post,verified,moderator" || b.Level.ID != "newcomer" {
		t.Errorf("bob badges: %s", badgeIDs(b))
	}

	// Manual award / revoke; awarding an unknown badge fails; revoke keeps rule badges.
	if err := srv.forumAward(ctx, "founding-member", alice, bob, alice); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumAward(ctx, "founding-member", alice); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := srv.forumAward(ctx, "nope", alice); err == nil {
		t.Error("unknown badge awarded")
	}
	stats, _ = srv.forumMemberStats(ctx, []int64{alice, bob})
	if m := stats[alice].Manual; len(m) != 1 || m[0] != "founding-member" {
		t.Errorf("alice manual: %v", m)
	}
	if err := srv.forumRevoke(ctx, "founding-member", bob); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumRevoke(ctx, "verified", alice); err != nil {
		t.Fatal(err)
	}
	authors, _ = srv.forumAuthors(ctx, []int64{alice, bob})
	if badgeIDs(authors[alice]) != "first-post,verified,founding-member" || badgeIDs(authors[bob]) != "first-post,verified,moderator" {
		t.Errorf("after revoke: alice %s bob %s", badgeIDs(authors[alice]), badgeIDs(authors[bob]))
	}

	// Badge CRUD: add, edit in place, delete strips manual awards.
	nb, err := srv.forumSaveBadge(ctx, forumBadge{Name: "Bug Hunter", Rule: forumRuleManual, Icon: "target", Color: "#123456", Enabled: true})
	if err != nil || nb.ID != "bug-hunter" {
		t.Fatalf("new badge: %+v %v", nb, err)
	}
	nb.Description = "Found a bug."
	if nb2, err := srv.forumSaveBadge(ctx, nb); err != nil || nb2.ID != "bug-hunter" || len(srv.forumBadges(ctx)) != 9 {
		t.Errorf("edit badge: %+v %v (%d badges)", nb2, err, len(srv.forumBadges(ctx)))
	}
	if _, err := srv.forumSaveBadge(ctx, forumBadge{Name: "", Rule: forumRuleManual}); err == nil {
		t.Error("nameless badge saved")
	}
	if err := srv.forumAward(ctx, "bug-hunter", carol); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumDeleteBadge(ctx, "bug-hunter"); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumDeleteBadge(ctx, "bug-hunter"); err == nil {
		t.Error("deleted twice")
	}
	if st, _ := srv.forumMemberStats(ctx, []int64{carol}); len(st[carol].Manual) != 0 {
		t.Errorf("carol manual after delete: %v", st[carol].Manual)
	}

	// Levels setting round trip: custom points move alice up.
	lv := srv.forumLevelsLoad(ctx)
	lv.TopicPoints = 10
	lv.Levels = append(lv.Levels, forumLevel{Name: "Rookie", Min: 5, Icon: "rocket", Color: "#00ff00"})
	if _, err := srv.forumSaveLevels(ctx, lv); err != nil {
		t.Fatal(err)
	}
	if got := srv.forumLevelsLoad(ctx); got.TopicPoints != 10 || len(got.Levels) != 7 || got.Levels[1].ID != "rookie" {
		t.Errorf("levels saved: %+v", got)
	}
	if l := srv.forumUserLevel(ctx, 42); l.ID != "member" {
		t.Errorf("42 points: %s", l.ID)
	}
	authors, _ = srv.forumAuthors(ctx, []int64{alice})
	if a := authors[alice]; a.Points != 42 || a.Level.ID != "member" {
		t.Errorf("alice with 10-point topics: %d %s", a.Points, a.Level.ID)
	}

	// Members: filters, search, sorts, scopes.
	list := func(q forumMemberQuery) ([]string, int) {
		t.Helper()
		rows, total, err := srv.forumMembers(ctx, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Login)
			if r.Level == nil || !r.Stats {
				t.Errorf("%s: no level", r.Login)
			}
			if q.Admin != (r.Email != "") {
				t.Errorf("%s: email %q in scope admin=%v", r.Login, r.Email, q.Admin)
			}
		}
		return out, total
	}
	eq := func(label string, got []string, total int, want string, wantTotal int) {
		t.Helper()
		if strings.Join(got, ",") != want || total != wantTotal {
			t.Errorf("%s: %v (%d), want %s (%d)", label, got, total, want, wantTotal)
		}
	}
	got, n := list(forumMemberQuery{Admin: true, Filter: "unverified"})
	eq("unverified", got, n, "carol", 1)
	got, n = list(forumMemberQuery{Admin: true, Filter: "blocked"})
	eq("blocked", got, n, "eve", 1)
	got, n = list(forumMemberQuery{Admin: true, Filter: "google"})
	eq("google", got, n, "dave", 1)
	got, n = list(forumMemberQuery{Admin: true, Filter: "moderators", Sort: "name"})
	eq("moderators", got, n, "admin,bob", 2)
	got, n = list(forumMemberQuery{Admin: true, Sort: "name"})
	eq("all by name", got, n, "admin,alice,bob,carol,dave,eve", 6)
	got, n = list(forumMemberQuery{Admin: true, Sort: "name", Order: "desc", PerPage: 2, Page: 2})
	eq("name desc page 2", got, n, "carol,bob", 6)
	got, n = list(forumMemberQuery{Admin: true, Sort: "points"})
	if len(got) < 2 || got[0] != "alice" || got[1] != "bob" || n != 6 {
		t.Errorf("points: %v", got)
	}
	got, _ = list(forumMemberQuery{Admin: true, Sort: "topics", Order: "asc", Filter: "all", PostedOnly: true})
	eq("posted only, topics asc", got, 2, "bob,alice", 2)
	got, n = list(forumMemberQuery{Admin: true, Sort: "last_active", PostedOnly: true})
	eq("last active", got, n, "bob,alice", 2)
	got, n = list(forumMemberQuery{Admin: true, Identity: true, Search: "DAVE@example"})
	eq("admin email search", got, n, "dave", 1)
	got, n = list(forumMemberQuery{Admin: true, Search: "dave@example"})
	eq("moderator email search (no list_users)", got, n, "", 0)
	got, n = list(forumMemberQuery{Search: "dave@example"})
	eq("front email search", got, n, "", 0)
	got, n = list(forumMemberQuery{Search: "Car"})
	eq("front name search", got, n, "carol", 1)
	got, n = list(forumMemberQuery{Sort: "name"})
	eq("front hides blocked", got, n, "admin,alice,bob,carol,dave", 5)
	got, n = list(forumMemberQuery{Search: "%"})
	eq("wildcard is literal", got, n, "", 0)
	got, n = list(forumMemberQuery{Admin: true, Search: "'; DROP TABLE users; --", Sort: "name; DROP TABLE users"})
	eq("injection", got, n, "", 0)
	if _, total, _ := srv.forumMembers(ctx, forumMemberQuery{Admin: true}); total != 6 {
		t.Errorf("users table damaged? %d", total)
	}

	// Member management: verify, block gate, forum role swap keeping other roles.
	adminU, _ := srv.store.UserByID(ctx, 1)
	bobU, _ := srv.store.UserByID(ctx, bob)
	if err := srv.forumVerifyMembers(ctx, carol); err != nil {
		t.Fatal(err)
	}
	if st, _ := srv.forumMemberStats(ctx, []int64{carol}); !st[carol].Verified {
		t.Error("carol not verified")
	}
	if err := srv.forumSetBlocked(ctx, bobU, 1, true); err == nil {
		t.Error("moderator blocked the administrator")
	}
	if err := srv.forumSetBlocked(ctx, bobU, dave, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := srv.forumMemberStats(ctx, []int64{dave}); !st[dave].Blocked {
		t.Error("dave not blocked")
	}
	if err := srv.forumSetBlocked(ctx, adminU, dave, false); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetForumRole(ctx, bobU, dave, forumRoleModerator); err == nil {
		t.Error("a moderator changed roles")
	}
	if err := srv.forumSetForumRole(ctx, adminU, dave, forumRoleModerator); err != nil {
		t.Fatal(err)
	}
	if du, _ := srv.store.UserByID(ctx, dave); len(du.Roles) != 2 || du.Roles[0] != "subscriber" || du.Roles[1] != forumRoleModerator || !du.Can(capForumModerate, nil) {
		t.Errorf("dave roles: %v", du.Roles)
	}
	if err := srv.forumSetForumRole(ctx, adminU, dave, forumRoleParticipant); err != nil {
		t.Fatal(err)
	}
	if du, _ := srv.store.UserByID(ctx, dave); strings.Join(du.Roles, ",") != "subscriber,forum_participant" {
		t.Errorf("dave roles after demotion: %v", du.Roles)
	}
	if err := srv.forumSetForumRole(ctx, adminU, bob, forumRoleParticipant); err != nil {
		t.Fatal(err)
	}
	if bu, _ := srv.store.UserByID(ctx, bob); strings.Join(bu.Roles, ",") != "forum_participant" {
		t.Errorf("bob roles: %v", bu.Roles)
	}
	if err := srv.forumSetForumRole(ctx, adminU, bob, forumRoleModerator); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, carol, map[string]any{forumUMetaUnverified: true}); err != nil {
		t.Fatal(err)
	}

	// Overview.
	o, err := srv.forumOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Forums != 1 || o.Categories != 1 || o.Topics != 4 || o.Replies != 3 || o.Members != 6 || o.PendingTopics != 1 || o.SpamReplies != 1 || o.Pending != 1 || o.Spam != 1 {
		t.Errorf("overview totals: %+v", o)
	}
	if len(o.Days) != 30 || o.NewMembers != 6 || o.NewTopics != 4 || o.NewReplies != 3 || o.Days[29].Topics != 4 || o.DayMax < 7 {
		t.Errorf("overview series: members %d topics %d replies %d today %+v max %d", o.NewMembers, o.NewTopics, o.NewReplies, o.Days[29], o.DayMax)
	}
	if len(o.Top) != 2 || o.Top[0].Login != "alice" || len(o.Recent) != 7 || o.Recent[0].Author == nil {
		t.Errorf("overview top %d recent %d", len(o.Top), len(o.Recent))
	}

	// The members route answers 200; ShowMembers off is a 404.
	if code, _, _ := get(t, fx.anon, fx.url("members")); code != http.StatusOK {
		t.Errorf("members page: %d", code)
	}
	if !srv.forumLoad().ShowMembers {
		t.Error("ShowMembers default off")
	}
	fx.settings(t, func(st *forumSettings) { st.ShowMembers = false })
	if srv.forumLoad().ShowMembers {
		t.Error("ShowMembers did not save")
	}
	if code, _, _ := get(t, fx.anon, fx.url("members")); code != http.StatusNotFound {
		t.Errorf("members off: %d", code)
	}
}
