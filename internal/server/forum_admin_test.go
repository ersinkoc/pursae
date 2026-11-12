package server

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Forums admin template parses together with the rest of the admin templates.
func TestForumAdminTemplateParses(t *testing.T) {
	tp := &templates{s: &Server{}}
	set, err := parseTree(os.DirFS("../../web"), "admin/templates", tp.funcs())
	if err != nil {
		t.Fatal(err)
	}
	if set.Lookup("admin/forum") == nil {
		t.Fatal("admin/forum is not defined")
	}
}

func TestForumAdminErr(t *testing.T) {
	if got := forumAdminErr(errForumNotFound); got != "The forum does not exist." {
		t.Errorf("not found: %q", got)
	}
	if got := forumAdminErr(errString("forum: the parent must be a top-level category")); got != "The parent must be a top-level category." {
		t.Errorf("store error: %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// forumAdminSite installs a site, activates Pursae Forum and returns the admin client.
func forumAdminSite(t *testing.T) (string, *http.Client, *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	forumActivateLink(t, c, ts.URL)
	return ts.URL, c, srv
}

// forumAdminNonceOf reads the admin nonce from a Forums screen.
func forumAdminNonceOf(t *testing.T, c *http.Client, base, tab string) string {
	t.Helper()
	code, body, _ := get(t, c, base+"/wp-admin/forum?tab="+tab)
	if code != http.StatusOK {
		t.Fatalf("GET tab %s: %d", tab, code)
	}
	return formNonce(t, body)
}

// Settings round trip: the Google secret is write-only (never printed, an empty field keeps it),
// the other settings are sanitized; the redirect URI is shown.
func TestForumAdminSettings(t *testing.T) {
	base, c, srv := forumAdminSite(t)

	code, body, _ := get(t, c, base+"/wp-admin/forum")
	if code != http.StatusOK || !strings.Contains(body, "pf-admin-stats") || !strings.Contains(body, `class="nav-tab nav-tab-active"`) {
		t.Fatalf("Overview tab (default): %d", code)
	}
	for _, tab := range []string{"forums", "moderation", "members", "badges", "settings", "google"} {
		code, body, _ := get(t, c, base+"/wp-admin/forum?tab="+tab)
		if code != http.StatusOK || strings.Contains(body, "Template error") || strings.Contains(body, "template:") {
			t.Fatalf("tab %s: %d (template error?)", tab, code)
		}
	}
	if _, body, _ := get(t, c, base+"/wp-admin/forum?tab=forums"); !strings.Contains(body, "Add New Forum") {
		t.Fatal("Forums tab: no add form")
	}

	nonce := forumAdminNonceOf(t, c, base, "settings")
	code, _, loc := post(t, c, base+"/wp-admin/forum?tab=settings", url.Values{"_wpnonce": {nonce}, "base": {"community"}, "title": {"Our Community"},
		"topics_per_page": {"500"}, "replies_per_page": {"10"}, "default_role": {"administrator"}, "edit_window": {"15"}, "flood_seconds": {"5"},
		"moderate_first_post": {"1"}})
	if code != http.StatusFound || !strings.Contains(loc, "settings-updated=true") {
		t.Fatalf("save settings: %d %s", code, loc)
	}
	st := srv.forumLoad()
	if st.Base != "community" || st.Title != "Our Community" || st.TopicsPerPage != 100 || st.RepliesPerPage != 10 ||
		st.DefaultRole != forumRoleParticipant || st.Registration || st.RequireVerify || !st.ModerateFirstPost || st.EditWindow != 15 || st.FloodSeconds != 5 {
		t.Errorf("saved settings: %+v", st)
	}
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=settings&settings-updated=true")
	if !strings.Contains(body, "Settings saved.") || !strings.Contains(body, `value="community"`) {
		t.Error("settings screen after save")
	}

	const secret = "GOCSPX-very-secret-value"
	nonce = forumAdminNonceOf(t, c, base, "google")
	code, _, loc = post(t, c, base+"/wp-admin/forum?tab=google", url.Values{"_wpnonce": {nonce}, "google_enabled": {"1"},
		"google_client_id": {" 123-abc.apps.googleusercontent.com "}, "google_client_secret": {secret}})
	if code != http.StatusFound || !strings.Contains(loc, "msg=google_saved") {
		t.Fatalf("save google: %d %s", code, loc)
	}
	if st := srv.forumLoad(); st.GoogleClientSecret != secret || st.GoogleClientID != "123-abc.apps.googleusercontent.com" || !st.GoogleEnabled {
		t.Errorf("google settings: %+v", st)
	}
	if !srv.forumGoogleReady() {
		t.Error("google not ready")
	}
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=google")
	if strings.Contains(body, secret) {
		t.Fatal("the client secret is printed")
	}
	if !strings.Contains(body, base+"/forum-auth/google/callback") || !strings.Contains(body, "Saved (leave empty to keep it)") || !strings.Contains(body, "is-ready") {
		t.Error("google tab: redirect URI, saved placeholder or ready status missing")
	}
	// An empty secret field keeps the stored one; saving the Settings tab keeps it too.
	nonce = formNonce(t, body)
	post(t, c, base+"/wp-admin/forum?tab=google", url.Values{"_wpnonce": {nonce}, "google_enabled": {"1"}, "google_client_id": {"123-abc.apps.googleusercontent.com"}})
	nonce = forumAdminNonceOf(t, c, base, "settings")
	post(t, c, base+"/wp-admin/forum?tab=settings", url.Values{"_wpnonce": {nonce}, "base": {"community"}, "title": {"Our Community"}})
	if st := srv.forumLoad(); st.GoogleClientSecret != secret || !st.GoogleEnabled {
		t.Errorf("secret lost on save: %+v", st)
	}
	// Removing the secret turns Google off.
	nonce = forumAdminNonceOf(t, c, base, "google")
	post(t, c, base+"/wp-admin/forum?tab=google", url.Values{"_wpnonce": {nonce}, "google_enabled": {"1"}, "google_client_id": {"123-abc.apps.googleusercontent.com"}, "google_clear_secret": {"1"}})
	if st := srv.forumLoad(); st.GoogleClientSecret != "" || st.GoogleEnabled || srv.forumGoogleReady() {
		t.Errorf("clear secret: %+v", st)
	}

	// Enabled without the secret: the status says what is missing.
	nonce = forumAdminNonceOf(t, c, base, "google")
	post(t, c, base+"/wp-admin/forum?tab=google", url.Values{"_wpnonce": {nonce}, "google_enabled": {"1"}, "google_client_id": {"123-abc.apps.googleusercontent.com"}})
	if _, body, _ := get(t, c, base+"/wp-admin/forum?tab=google"); !strings.Contains(body, "Enabled, but the Client secret is missing") {
		t.Error("google tab: incomplete status missing")
	}
	// ?error= takes a code only: free text in the link is never printed.
	if _, body, _ := get(t, c, base+"/wp-admin/forum?tab=forums&error=Your+site+was+hacked"); strings.Contains(body, "was hacked") {
		t.Error("free text from ?error= is printed")
	}

	// No nonce: refused.
	if code, _, _ := post(t, c, base+"/wp-admin/forum?tab=settings", url.Values{"base": {"x"}}); code != http.StatusForbidden {
		t.Errorf("settings without nonce: %d", code)
	}
	if code, _, loc := get(t, c, base+"/wp-admin/forum?tab=bogus"); code != http.StatusFound || !strings.Contains(loc, "tab=overview") {
		t.Errorf("unknown tab: %d %s", code, loc)
	}
}

// Capabilities: a Forum Moderator opens Moderation but not the configuration tabs; a participant
// opens nothing.
func TestForumAdminCaps(t *testing.T) {
	base, _, srv := forumAdminSite(t)
	mod := hgSecondUser(t, srv, base, "modda", forumRoleModerator)
	code, body, _ := get(t, mod, base+"/wp-admin/forum")
	if code != http.StatusOK || !strings.Contains(body, "pf-admin-stats") || strings.Contains(body, "Add New Forum") {
		t.Fatalf("moderator default tab: %d", code)
	}
	for _, hidden := range []string{"tab=settings", "tab=badges", "tab=google", "tab=forums"} {
		if strings.Contains(body, hidden) {
			t.Errorf("the moderator sees %s", hidden)
		}
	}
	for _, shown := range []string{"tab=moderation", "tab=members"} {
		if !strings.Contains(body, shown) {
			t.Errorf("the moderator misses %s", shown)
		}
	}
	for _, tab := range []string{"moderation", "members"} {
		if code, _, _ := get(t, mod, base+"/wp-admin/forum?tab="+tab); code != http.StatusOK {
			t.Errorf("moderator GET %s: %d", tab, code)
		}
	}
	for _, tab := range []string{"forums", "settings", "google", "badges"} {
		if code, _, _ := get(t, mod, base+"/wp-admin/forum?tab="+tab); code != http.StatusForbidden {
			t.Errorf("moderator GET %s: %d", tab, code)
		}
	}
	nonce := forumAdminNonceOf(t, mod, base, "moderation")
	if code, _, _ := post(t, mod, base+"/wp-admin/forum?tab=settings", url.Values{"_wpnonce": {nonce}, "base": {"hacked"}}); code != http.StatusForbidden {
		t.Errorf("moderator POST settings: %d", code)
	}
	if srv.forumLoad().Base != "forums" {
		t.Error("a moderator changed the settings")
	}
	part := hgSecondUser(t, srv, base, "parti", forumRoleParticipant)
	if code, _, _ := get(t, part, base+"/wp-admin/forum?tab=moderation"); code != http.StatusForbidden {
		t.Errorf("participant GET moderation: %d", code)
	}
}

// Forum CRUD through the screen; a forum with topics cannot be deleted.
func TestForumAdminForums(t *testing.T) {
	base, c, srv := forumAdminSite(t)
	ctx := context.Background()
	nonce := forumAdminNonceOf(t, c, base, "forums")
	add := func(v url.Values) (int, string, string) {
		v.Set("_wpnonce", nonce)
		return post(t, c, base+"/wp-admin/forum?tab=forums", v)
	}
	if code, _, loc := add(url.Values{"title": {"General"}, "kind": {"category"}}); code != http.StatusFound || !strings.Contains(loc, "msg=forum_added") {
		t.Fatalf("add category: %d %s", code, loc)
	}
	list, _ := srv.forumList(ctx)
	if len(list) != 1 || list[0].Kind != "category" {
		t.Fatalf("after adding a category: %+v", list)
	}
	cat := list[0].ID
	if code, _, _ := add(url.Values{"title": {"Introductions"}, "parent": {strconv.FormatInt(cat, 10)}, "description": {"Say <b>hello</b>.<script>x</script>"}, "order": {"2"}}); code != http.StatusFound {
		t.Fatalf("add forum: %d", code)
	}
	// A category with a parent is refused and the form keeps what was typed.
	code, body, _ := add(url.Values{"title": {"Nested cat"}, "kind": {"category"}, "parent": {strconv.FormatInt(cat, 10)}})
	if code != http.StatusOK || !strings.Contains(body, "A category cannot have a parent.") || !strings.Contains(body, `value="Nested cat"`) {
		t.Errorf("nested category: %d", code)
	}
	list, _ = srv.forumList(ctx)
	if len(list) != 1 || len(list[0].Children) != 1 {
		t.Fatalf("tree: %+v", list)
	}
	f := list[0].Children[0]
	if f.Slug != "introductions" || strings.Contains(string(f.Description), "<script") || f.Order != 2 {
		t.Errorf("forum: %+v", f)
	}
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=forums")
	if !strings.Contains(body, ">Introductions</a>") || !strings.Contains(body, "pf-admin-badge--category") {
		t.Error("forum list")
	}

	// Edit: rename and close.
	id := strconv.FormatInt(f.ID, 10)
	code, body, _ = get(t, c, base+"/wp-admin/forum?tab=forums&action=edit&id="+id)
	if code != http.StatusOK || !strings.Contains(body, `id="edittag"`) || !strings.Contains(body, `value="Introductions"`) {
		t.Fatalf("edit screen: %d", code)
	}
	code, _, loc := post(t, c, base+"/wp-admin/forum?tab=forums&action=edit&id="+id, url.Values{"_wpnonce": {formNonce(t, body)}, "id": {id},
		"title": {"Say hello"}, "slug": {"hello"}, "parent": {strconv.FormatInt(cat, 10)}, "kind": {"forum"}, "closed": {"1"}, "order": {"3"}})
	if code != http.StatusFound || !strings.Contains(loc, "msg=forum_updated") {
		t.Fatalf("update: %d %s", code, loc)
	}
	if g, _ := srv.forumByID(ctx, f.ID); g == nil || g.Title != "Say hello" || g.Slug != "hello" || !g.Closed || g.Order != 3 || g.ParentID != cat {
		t.Errorf("updated: %+v", g)
	}

	// Delete: refused while a topic (even a pending one) is inside, done when empty.
	if _, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: f.ID, AuthorID: 1, Title: "First", Content: "<p>Hi</p>", Pending: true}); err != nil {
		t.Fatal(err)
	}
	delURL := base + "/wp-admin/forum?" + url.Values{"tab": {"forums"}, "do": {"delete"}, "id": {id}, "_wpnonce": {nonce}}.Encode()
	code, _, loc = get(t, c, delURL)
	if code != http.StatusFound || !strings.Contains(loc, "error=") {
		t.Errorf("delete non-empty: %d %s", code, loc)
	}
	if _, err := srv.forumByID(ctx, f.ID); err != nil {
		t.Fatal("a forum with a topic was deleted")
	}
	_, body, _ = get(t, c, base+loc)
	if !strings.Contains(body, "still holds topics or forums") {
		t.Error("no refusal notice")
	}
	// The category with a forum cannot go either.
	code, _, loc = get(t, c, base+"/wp-admin/forum?"+url.Values{"tab": {"forums"}, "do": {"delete"}, "id": {strconv.FormatInt(cat, 10)}, "_wpnonce": {nonce}}.Encode())
	if code != http.StatusFound || !strings.Contains(loc, "error=") {
		t.Errorf("delete category with forums: %d %s", code, loc)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/forum?tab=forums&do=delete&id="+id); code != http.StatusForbidden {
		t.Errorf("delete without nonce: %d", code)
	}
	_, _ = srv.store.Pool.Exec(ctx, `DELETE FROM posts WHERE type = 'topic' AND parent_id = $1`, f.ID)
	code, _, loc = get(t, c, delURL)
	if code != http.StatusFound || !strings.Contains(loc, "msg=forum_deleted") {
		t.Errorf("delete empty: %d %s", code, loc)
	}
	if _, err := srv.forumByID(ctx, f.ID); err == nil {
		t.Error("empty forum not deleted")
	}
}

var forumAdminRowActionRe = regexp.MustCompile(`href="(/wp-admin/forum\?[^"]*do=approve[^"]*)"`)

// Moderation: filters, bulk approve / spam / unspam / trash / restore / delete permanently, a row
// action link.
func TestForumAdminModeration(t *testing.T) {
	base, c, srv := forumAdminSite(t)
	ctx := context.Background()
	cat, _ := srv.forumSaveForum(ctx, forumForumInput{Title: "General", Kind: "category"}, 1)
	fid, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Lobby", ParentID: cat}, 1)
	if err != nil {
		t.Fatal(err)
	}
	topic, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Waiting topic", Content: "<p>Please approve me</p>", Pending: true, IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topic.ID, AuthorID: 1, Content: "<p>Waiting reply</p>", Pending: true})
	if err != nil {
		t.Fatal(err)
	}
	pub, err := srv.forumCreateReply(ctx, forumNewReply{TopicID: topic.ID, AuthorID: 1, Content: "<p>Published reply</p>"})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(list ...int64) []string {
		var out []string
		for _, id := range list {
			out = append(out, strconv.FormatInt(id, 10))
		}
		return out
	}
	status := func(id int64) (string, bool) {
		var st string
		var spam bool
		_ = srv.store.Pool.QueryRow(ctx, `SELECT status, coalesce(meta->>'_forum_spam','') = 'true' FROM posts WHERE id = $1`, id).Scan(&st, &spam)
		return st, spam
	}

	code, body, _ := get(t, c, base+"/wp-admin/forum?tab=moderation&view=pending")
	if code != http.StatusOK || !strings.Contains(body, "Waiting topic") || !strings.Contains(body, "Waiting reply") || strings.Contains(body, "Published reply") {
		t.Fatalf("pending view: %d", code)
	}
	if !strings.Contains(body, "203.0.113.9") || !strings.Contains(body, `class="subsubsub"`) || !strings.Contains(body, `<span class="pending-count">2</span>`) {
		t.Error("pending view: IP or counts")
	}
	nonce := formNonce(t, body)
	bulk := func(view, op string, list ...int64) string {
		t.Helper()
		code, _, loc := post(t, c, base+"/wp-admin/forum?tab=moderation", url.Values{"_wpnonce": {nonce}, "view": {view}, "action": {op}, "item[]": ids(list...)})
		if code != http.StatusFound {
			t.Fatalf("bulk %s: %d", op, code)
		}
		return loc
	}
	loc := bulk("pending", "approve", topic.ID, reply.ID)
	if !strings.Contains(loc, "done=approve") || !strings.Contains(loc, "n=2") {
		t.Errorf("approve redirect: %s", loc)
	}
	if st, _ := status(topic.ID); st != "publish" {
		t.Errorf("topic after approve: %s", st)
	}
	if st, _ := status(reply.ID); st != "publish" {
		t.Errorf("reply after approve: %s", st)
	}
	_, body, _ = get(t, c, base+loc)
	if !strings.Contains(body, "2 items approved.") {
		t.Error("approve notice")
	}
	if tp, _ := srv.forumTopicByID(ctx, topic.ID); tp.ReplyCount != 2 {
		t.Errorf("reply count after approve: %d", tp.ReplyCount)
	}

	bulk("all", "spam", pub.ID)
	if st, spam := status(pub.ID); st != "trash" || !spam {
		t.Errorf("after spam: %s %v", st, spam)
	}
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=moderation&view=spam")
	if !strings.Contains(body, "Published reply") || !strings.Contains(body, `value="unspam"`) || strings.Contains(body, `value="approve"`) {
		t.Error("spam view")
	}
	bulk("spam", "unspam", pub.ID)
	if st, spam := status(pub.ID); st != "publish" || spam {
		t.Errorf("after unspam: %s %v", st, spam)
	}
	// Delete permanently only works from the Trash.
	if loc := bulk("all", "delete", pub.ID); !strings.Contains(loc, "failed=1") {
		t.Errorf("delete a published item: %s", loc)
	}
	bulk("all", "trash", topic.ID)
	bulk("trash", "restore", topic.ID)
	if st, _ := status(topic.ID); st != "publish" {
		t.Errorf("after restore: %s", st)
	}

	// A row action link (GET with the nonce): back to pending, then approve via the link.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'pending' WHERE id = $1`, reply.ID)
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=moderation&view=pending")
	m := forumAdminRowActionRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Approve row action")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "done=approve") {
		t.Errorf("row approve: %d %s", code, loc)
	}
	if st, _ := status(reply.ID); st != "publish" {
		t.Errorf("after row approve: %s", st)
	}

	// Trash the topic, then delete it permanently: its replies go with it and the forum is recounted.
	bulk("all", "trash", topic.ID)
	bulk("trash", "delete", topic.ID)
	var left int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE id = $1 OR parent_id = $1`, topic.ID).Scan(&left)
	if left != 0 {
		t.Errorf("topic and replies left: %d", left)
	}
	if f, _ := srv.forumByID(ctx, fid); f.TopicCount != 0 || f.ReplyCount != 0 {
		t.Errorf("forum counts after delete: %d/%d", f.TopicCount, f.ReplyCount)
	}

	// Search and an empty list.
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=moderation&s=nothingmatches")
	if !strings.Contains(body, "No topics or replies match your search.") {
		t.Error("empty search")
	}
	// No nonce: refused.
	if code, _, _ := post(t, c, base+"/wp-admin/forum?tab=moderation", url.Values{"action": {"approve"}, "item[]": {"1"}}); code != http.StatusForbidden {
		t.Errorf("bulk without nonce: %d", code)
	}
}
