package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Regression tests for the authorization gaps the security audit found. Each one builds a site with an
// administrator, a low-privilege user and content that user must not see or change.

type secSite struct {
	t     *testing.T
	base  string
	srv   *Server
	admin *restClient
}

func secStart(t *testing.T) *secSite {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	return &secSite{t: t, base: ts.URL, srv: srv, admin: newRESTClient(t, c, ts.URL)}
}

// user creates an account with the role and returns a logged-in cookie client and its REST client.
func (s *secSite) user(login, role string) (*http.Client, *restClient) {
	s.t.Helper()
	asCreateUser(s.t, s.srv, login, role, asStrongPass)
	c := asNewClient()
	if code, _, _ := asLogin(s.t, c, s.base, "/wp-login", login, asStrongPass); code != http.StatusFound {
		s.t.Fatalf("login %s: %d", login, code)
	}
	return c, newRESTClient(s.t, c, s.base)
}

func (s *secSite) anon() *restClient {
	return &restClient{t: s.t, c: asNewClient(), base: s.base}
}

func (s *secSite) post(title, status, password string) float64 {
	s.t.Helper()
	body := map[string]any{"title": title, "content": title + " body words", "status": status}
	if password != "" {
		body["password"] = password
	}
	code, out, _, _ := s.admin.do("POST", "/wp/v2/posts", body)
	if code != http.StatusCreated {
		s.t.Fatalf("create post %q: %d %v", title, code, out)
	}
	return out["id"].(float64)
}

func (s *secSite) comment(postID float64, content, email string) float64 {
	s.t.Helper()
	code, out, _, _ := s.admin.do("POST", "/wp/v2/comments", map[string]any{"post": postID, "content": content, "author_name": "Visitor", "author_email": email, "status": "approved"})
	if code != http.StatusCreated {
		s.t.Fatalf("create comment: %d %v", code, out)
	}
	return out["id"].(float64)
}

// The posts screen lets contributors in (edit_posts) but must show only their own posts, never other
// users' drafts, private posts or post passwords (WordPress forces author=self without edit_others_posts).
func TestPostsListScopedForUsersWithoutEditOthers(t *testing.T) {
	s := secStart(t)
	s.post("Admin secret draft", "draft", "")
	s.post("Admin private note", "private", "")
	s.post("Locked announcement", "publish", "hunter2-password")
	contrib, _ := s.user("conny", "contributor")

	for _, q := range []string{"", "?post_status=draft", "?post_status=private", "?post_status=all", "?post_status=trash"} {
		_, body, _ := get(t, contrib, s.base+"/wp-admin/edit"+q)
		for _, leak := range []string{"Admin secret draft", "Admin private note", "hunter2-password"} {
			if strings.Contains(body, leak) {
				t.Errorf("contributor sees %q on /wp-admin/edit%s", leak, q)
			}
		}
	}
	// The administrator still sees them, with the password in the quick-edit data.
	_, body, _ := get(t, s.admin.c, s.base+"/wp-admin/edit?post_status=draft")
	if !strings.Contains(body, "Admin secret draft") {
		t.Error("administrator no longer sees the draft")
	}
	_, body, _ = get(t, s.admin.c, s.base+"/wp-admin/edit")
	if !strings.Contains(body, "hunter2-password") {
		t.Error("administrator lost the post password in the quick-edit data")
	}
}

// Comments on private and password-protected posts are not for every logged-in user, and comment search
// must not match the commenter's e-mail address or IP, which visitors may not filter on either.
func TestRESTCommentsRespectPostVisibilityAndHideEmail(t *testing.T) {
	s := secStart(t)
	priv := s.post("Private thread", "private", "")
	prot := s.post("Protected thread", "publish", "pw-for-thread")
	pub := s.post("Public thread", "publish", "")
	s.comment(priv, "private comment text", "probe-priv@example.test")
	s.comment(prot, "protected comment text", "probe-prot@example.test")
	s.comment(pub, "public comment text", "probe-pub@example.test")
	_, sub := s.user("sally", "subscriber")

	for name, rc := range map[string]*restClient{"subscriber": sub, "anonymous": s.anon()} {
		code, _, list, _ := rc.do("GET", "/wp/v2/comments?per_page=100", nil)
		if code != http.StatusOK {
			t.Fatalf("%s list: %d", name, code)
		}
		var all strings.Builder
		for _, it := range list {
			all.WriteString(fmt.Sprint(it.(map[string]any)["content"]))
		}
		if strings.Contains(all.String(), "private comment text") || strings.Contains(all.String(), "protected comment text") {
			t.Errorf("%s lists comments of posts it cannot read: %s", name, all.String())
		}
		if !strings.Contains(all.String(), "public comment text") {
			t.Errorf("%s no longer sees comments on public posts", name)
		}
	}
	// Searching for an e-mail fragment or an IP must not reveal that a commenter used it.
	anon := s.anon()
	for _, q := range []string{"probe-pub%40example", "probe-pub@example", "127.0.0"} {
		if _, _, list, _ := anon.do("GET", "/wp/v2/comments?search="+q, nil); len(list) != 0 {
			t.Errorf("anonymous search %q matched a commenter's e-mail or IP: %v", q, list)
		}
	}
	if _, _, list, _ := anon.do("GET", "/wp/v2/comments?search=public+comment", nil); len(list) != 1 {
		t.Errorf("searching the comment text must still work, got %d", len(list))
	}
}

// A password-protected post must not give its first words away through excerpt.rendered.
func TestRESTProtectedPostHidesExcerpt(t *testing.T) {
	s := secStart(t)
	id := s.post("Locked story", "publish", "story-password")
	anon := s.anon()
	code, out, _, _ := anon.do("GET", fmt.Sprintf("/wp/v2/posts/%d", int(id)), nil)
	if code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	if ex, _ := out["excerpt"].(map[string]any); strings.Contains(fmt.Sprint(ex["rendered"]), "body words") {
		t.Errorf("excerpt of a protected post leaks its text: %v", ex)
	}
	if ct, _ := out["content"].(map[string]any); strings.Contains(fmt.Sprint(ct["rendered"]), "body words") {
		t.Errorf("content of a protected post leaks its text: %v", ct)
	}
}

// The front-end comment form must not accept comments on a private post the visitor cannot read.
func TestFrontCommentOnPrivatePostIsRefused(t *testing.T) {
	s := secStart(t)
	priv := s.post("Hidden thread", "private", "")
	anon := asNewClient()
	code, _, _ := post(t, anon, s.base+"/wp-comments-post", url.Values{
		"comment_post_ID": {fmt.Sprint(int(priv))}, "author": {"Eve"}, "email": {"eve@example.test"}, "comment": {"let me in"}})
	if code == http.StatusFound {
		t.Errorf("anonymous comment on a private post was accepted (302)")
	}
	if n := asCount(t, `SELECT count(*) FROM comments WHERE content = 'let me in'`); n != 0 {
		t.Errorf("%d comment(s) stored on a private post", n)
	}
}

// An author may edit comments on their own posts but must not move one onto a post they cannot edit.
func TestRESTCommentMoveNeedsAccessToTarget(t *testing.T) {
	s := secStart(t)
	_, author := s.user("ann", "author")
	code, mine, _, _ := author.do("POST", "/wp/v2/posts", map[string]any{"title": "Ann's post", "content": "x", "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("author post: %d %v", code, mine)
	}
	mineID := mine["id"].(float64)
	other := s.post("Admin private target", "private", "")
	cid := s.comment(mineID, "movable comment", "mover@example.test")

	code, out, _, _ := author.do("POST", fmt.Sprintf("/wp/v2/comments/%d", int(cid)), map[string]any{"post": other})
	if code < 400 {
		t.Errorf("moving a comment onto a post the author cannot edit returned %d: %v", code, out)
	}
	if n := asCount(t, `SELECT count(*) FROM comments WHERE id = $1 AND post_id = $2`, int64(cid), int64(other)); n != 0 {
		t.Error("the comment was moved onto the private post")
	}
}

// The comments screen is for people who can moderate; a contributor must not read other users' comments,
// spam or commenter e-mail addresses and IPs.
func TestAdminCommentsScreenScopedForContributors(t *testing.T) {
	s := secStart(t)
	priv := s.post("Admin private thread", "private", "")
	s.comment(priv, "secret thread reply", "leaky-reader@example.test")
	contrib, _ := s.user("carl", "contributor")
	for _, q := range []string{"", "?comment_status=approved", "?comment_status=spam", "?comment_status=all"} {
		code, body, _ := get(t, contrib, s.base+"/wp-admin/edit-comments"+q)
		if code == http.StatusOK && (strings.Contains(body, "leaky-reader@example.test") || strings.Contains(body, "secret thread reply")) {
			t.Errorf("contributor reads other users' comments on /wp-admin/edit-comments%s", q)
		}
	}
}

// Changing a password must end the user's other sessions (the changer keeps the current one), also when an
// administrator changes it for someone else.
func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	s := secStart(t)
	id := asCreateUser(s.t, s.srv, "pat", "editor", asStrongPass)
	login := func() (*http.Client, *restClient) {
		c := asNewClient()
		if code, _, _ := asLogin(t, c, s.base, "/wp-login", "pat", asStrongPass); code != http.StatusFound {
			t.Fatalf("login: %d", code)
		}
		return c, newRESTClient(t, c, s.base)
	}
	alive := func(c *http.Client) bool {
		code, _, _ := get(t, c, s.base+"/wp-admin/")
		return code == http.StatusOK
	}
	c1, rc1 := login()
	c2, _ := login()
	if !alive(c1) || !alive(c2) {
		t.Fatal("sessions should work before the change")
	}
	const newPass = "Granite-Ribbon-Orbit-71!"
	if code, out, _, _ := rc1.do("POST", "/wp/v2/users/me", map[string]any{"password": newPass}); code != http.StatusOK {
		t.Fatalf("change own password: %d %v", code, out)
	}
	if !alive(c1) {
		t.Error("the session that changed the password was ended")
	}
	if alive(c2) {
		t.Error("another session of the user survived the password change")
	}
	// An administrator changing it for the user ends all of the user's sessions.
	c3 := asNewClient()
	if code, _, _ := asLogin(t, c3, s.base, "/wp-login", "pat", newPass); code != http.StatusFound {
		t.Fatalf("login with the new password: %d", code)
	}
	if code, out, _, _ := s.admin.do("POST", fmt.Sprintf("/wp/v2/users/%d", id), map[string]any{"password": "Maple-Quartz-Delta-52!"}); code != http.StatusOK {
		t.Fatalf("admin changes password: %d %v", code, out)
	}
	if alive(c1) || alive(c3) {
		t.Error("a session survived an administrator's password change")
	}
}

// A password change drops application passwords and OAuth grants the way it drops other sessions.
// The application password that performed the change stays; a reset link drops that one too.
func TestPasswordChangeRevokesAppPasswordsAndOAuth(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	id := asCreateUser(t, s.srv, "pat", "editor", asStrongPass)
	plainA, apA, err := s.srv.createAppPassword(gooTestCtx(), id, "Phone", "", appScopeFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	plainB, _, err := s.srv.createAppPassword(gooTestCtx(), id, "Tablet", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.store.Pool.Exec(ctx, `INSERT INTO oauth_clients (client_id, name, redirect_uris) VALUES ('00000000-0000-4000-8000-0000000000bb', 'Phone', '{http://localhost/cb}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.store.Pool.Exec(ctx, `INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
		VALUES ('00000000-0000-4000-8000-0000000000bb', $1, 'acc-pat', 'ref-pat', now() + interval '1 hour', now() + interval '1 day')`, id); err != nil {
		t.Fatal(err)
	}
	const newPass = "Granite-Ribbon-Orbit-71!"
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r := gooReq(t, anon, "POST", s.base+"/wp-json/wp/v2/users/me", map[string]any{"password": newPass}, gooBasic("pat", plainA)); r.Code != http.StatusOK {
		t.Fatalf("change password with application password: %d %s", r.Code, r.Raw)
	}
	var n int
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM app_passwords WHERE user_id = $1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("application passwords left: %d %v", n, err)
	}
	var left int64
	_ = s.srv.store.Pool.QueryRow(ctx, `SELECT id FROM app_passwords WHERE user_id = $1`, id).Scan(&left)
	if left != apA.ID {
		t.Errorf("kept application password %d, want %d", left, apA.ID)
	}
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_tokens WHERE user_id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("oauth grants left: %d %v", n, err)
	}
	if r := gooReq(t, anon, "GET", s.base+"/wp-json/wp/v2/users/me", nil, gooBasic("pat", plainB)); r.Code != http.StatusUnauthorized {
		t.Errorf("revoked application password still works: %d", r.Code)
	}
	if r := gooReq(t, anon, "GET", s.base+"/wp-json/wp/v2/users/me", nil, gooBasic("pat", plainA)); r.Code != http.StatusOK {
		t.Errorf("the application password that changed the password was revoked: %d %s", r.Code, r.Raw)
	}
	if _, err := s.srv.store.Pool.Exec(ctx, `INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
		VALUES ('00000000-0000-4000-8000-0000000000bb', $1, 'acc-pat-2', 'ref-pat-2', now() + interval '1 hour', now() + interval '1 day')`, id); err != nil {
		t.Fatal(err)
	}
	// Default clients follow the 302, so the status the test sees would be the confirmation page.
	noredir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _, _ := post(t, noredir, s.base+"/wp-login?action=lostpassword", url.Values{"user_login": {"pat"}}); code != http.StatusFound {
		t.Fatalf("lost password: %d", code)
	}
	rp := firstURL(t, lastMail(t, "Password Reset").Body, "action=rp")
	ru, _ := url.Parse(rp)
	resetForm := url.Values{"rp_login": {"pat"}, "rp_key": {ru.Query().Get("key")}, "pass1": {"Maple-Quartz-Delta-52!"}, "pass2": {"Maple-Quartz-Delta-52!"}}
	if code, _, loc := post(t, noredir, s.base+"/wp-login?action=resetpass", resetForm); code != http.StatusFound || !strings.Contains(loc, "password=changed") {
		t.Fatalf("reset: %d %s", code, loc)
	}
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM app_passwords WHERE user_id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("application passwords after reset: %d %v", n, err)
	}
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_tokens WHERE user_id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("oauth grants after reset: %d %v", n, err)
	}
}

// Pursae SEO builds the page head from the stored post. For a password-protected post the head must not contain
// anything derived from the protected body (here the og:image fallback, the first image of the content).
func TestSEOGoHeadHidesProtectedPostContent(t *testing.T) {
	s := secStart(t)
	if err := s.srv.settings.Set(context.Background(), map[string]any{"native_seogo": true}); err != nil {
		t.Fatal(err)
	}
	code, out, _, _ := s.admin.do("POST", "/wp/v2/posts", map[string]any{"title": "Members only", "status": "publish", "password": "seo-pass", "slug": "members-only",
		"content": `<p>intro</p><img src="https://marker.example/secret-picture.png" alt=""><p>The rest of the secret.</p>` +
			`<!-- wp:seogo/faq {"questions":[{"id":"q1","question":"Where is the key?","answer":"HIDDENANSWER-under-the-mat"}]} /-->`})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	// Positive control: the same blocks on an unprotected post do show up in the head.
	code, out, _, _ = s.admin.do("POST", "/wp/v2/posts", map[string]any{"title": "Open post", "status": "publish", "slug": "open-post",
		"content": `<p>intro</p><!-- wp:seogo/faq {"questions":[{"id":"q1","question":"Where is the key?","answer":"HIDDENANSWER-under-the-mat"}]} /-->`})
	if code != http.StatusCreated {
		t.Fatalf("create open post: %d %v", code, out)
	}
	_, open, _ := get(t, asNewClient(), s.base+"/open-post/")
	if !strings.Contains(open, "HIDDENANSWER") {
		t.Skip("Pursae SEO does not emit the FAQ schema in this setup, so the protected-post check cannot be exercised here")
	}
	anon := asNewClient()
	_, body, _ := get(t, anon, s.base+"/members-only/")
	if !strings.Contains(body, "og:title") && !strings.Contains(body, "application/ld+json") {
		t.Fatal("Pursae SEO head is not rendered, so this test would prove nothing")
	}
	if strings.Contains(body, "secret-picture.png") || strings.Contains(body, "The rest of the secret") || strings.Contains(body, "HIDDENANSWER") {
		t.Error("the page head of a password-protected post exposes its content")
	}
}

// The core must not leak account names even when AdminSecGo is off: no /?author=N redirect to the author's
// slug, no anonymous user list, no per-account login messages. WPGO_EXPOSE_USERS=1 is the opt-out.
func TestCoreBlocksUserEnumerationWithoutAnyPlugin(t *testing.T) {
	s := secStart(t)
	if s.srv.adminsecOn() {
		t.Fatal("Pursae Security must be off in this test")
	}
	s.post("Hello from the author", "publish", "") // gives the administrator a published post, so WordPress would list them
	anon := asNewClient()

	if code, _, loc := get(t, anon, s.base+"/?author=1"); code == http.StatusMovedPermanently || code == http.StatusFound || strings.Contains(loc, "/author/") {
		t.Errorf("/?author=1 redirects to the author's slug: %d %s", code, loc)
	}
	if code, _, _ := get(t, anon, s.base+"/?author=1"); code != http.StatusNotFound {
		t.Errorf("/?author=1 for a visitor: %d, want 404", code)
	}
	arc := s.anon()
	if code, _, _, _ := arc.do("GET", "/wp/v2/users", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /wp/v2/users: %d, want 401", code)
	}
	if code, _, _, _ := arc.do("GET", "/wp/v2/users/1", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /wp/v2/users/1: %d, want 401", code)
	}
	if _, body, _ := get(t, anon, s.base+"/wp-sitemap-users-1.xml"); strings.Contains(body, "/author/") {
		t.Errorf("the users sitemap lists authors")
	}
	// Logged-in users keep what they had.
	if code, _, list, _ := s.admin.do("GET", "/wp/v2/users", nil); code != http.StatusOK || len(list) == 0 {
		t.Errorf("administrator lost the user list: %d", code)
	}
	// The login form answers alike for an unknown account and a wrong password.
	_, unknown, _ := asLogin(t, anon, s.base, "/wp-login", "no-such-person", "x")
	_, wrong, _ := asLogin(t, anon, s.base, "/wp-login", "admin", "not-the-password")
	for name, body := range map[string]string{"unknown account": unknown, "wrong password": wrong} {
		if !strings.Contains(body, "The username or password you entered is incorrect") || strings.Contains(body, "not registered") || strings.Contains(body, "for the username") {
			t.Errorf("login message for %s names the account or its state", name)
		}
	}
	// The explicit opt-out brings the public behaviour back.
	s.srv.cfg.ExposeUsers = true
	if code, _, _, _ := arc.do("GET", "/wp/v2/users", nil); code != http.StatusOK {
		t.Errorf("with WPGO_EXPOSE_USERS the list should be public again: %d", code)
	}
}

// Visitors and users who cannot upload files may read media of the public site only: not the attachments of
// drafts, private or password-protected posts, and not the "post-process" action.
func TestRESTMediaHidesAttachmentsOfUnpublishedPosts(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	draft := int64(s.post("A draft", "draft", ""))
	locked := int64(s.post("A locked post", "publish", "media-pass"))
	open := int64(s.post("An open post", "publish", ""))
	ids := map[string]int64{}
	for name, parent := range map[string]int64{"draft": draft, "locked": locked, "open": open, "loose": 0} {
		a := &store.Attachment{UploaderID: 1, PostID: parent, Slug: "img-" + name, FilePath: "2026/10/img-" + name + ".png", Mime: "image/png", Title: "Picture " + name}
		if err := s.srv.store.InsertAttachment(ctx, a); err != nil {
			t.Fatal(err)
		}
		ids[name] = a.ID
	}
	_, sub := s.user("sam", "subscriber")
	for who, rc := range map[string]*restClient{"anonymous": s.anon(), "subscriber": sub} {
		_, _, list, _ := rc.do("GET", "/wp/v2/media?per_page=100", nil)
		var titles strings.Builder
		for _, it := range list {
			titles.WriteString(fmt.Sprint(it.(map[string]any)["title"]))
		}
		// Unattached files stay public, like WordPress.
		for _, hidden := range []string{"Picture draft", "Picture locked"} {
			if strings.Contains(titles.String(), hidden) {
				t.Errorf("%s lists %q", who, hidden)
			}
		}
		if !strings.Contains(titles.String(), "Picture open") {
			t.Errorf("%s no longer sees the public attachment", who)
		}
		for _, hidden := range []string{"draft", "locked"} {
			if code, _, _, _ := rc.do("GET", fmt.Sprintf("/wp/v2/media/%d", ids[hidden]), nil); code < 400 {
				t.Errorf("%s reads the %s attachment directly: %d", who, hidden, code)
			}
		}
		if code, _, _, _ := rc.do("POST", fmt.Sprintf("/wp/v2/media/%d/post-process", ids["open"]), map[string]any{"action": "create-image-subsizes"}); code < 400 {
			t.Errorf("%s may run post-process: %d", who, code)
		}
	}
	// Whoever can upload files still sees everything.
	if _, _, list, _ := s.admin.do("GET", "/wp/v2/media?per_page=100", nil); len(list) != 4 {
		t.Errorf("administrator sees %d attachments, want 4", len(list))
	}
}
