package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// forumAuthSetup is a site with Pursae Forum switched on and an empty rate limiter.
func forumAuthSetup(t *testing.T) (*httptest.Server, *http.Client, *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	forumActivateLink(t, c, ts.URL)
	forumLimiterReset()
	t.Cleanup(forumLimiterReset)
	return ts, c, srv
}

// forumJarClient is a fresh visitor (own cookies, redirects not followed).
func forumJarClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// forumAuthFormNonce is the _wpnonce of the form whose forum_action is action.
func forumAuthFormNonce(t *testing.T, body, action string) string {
	t.Helper()
	if strings.Contains(body, "forum template error") {
		t.Fatalf("template error in the page with forum_action=%s", action)
	}
	m := regexp.MustCompile(`name="forum_action" value="` + action + `" />\s*<input type="hidden" name="_wpnonce" value="([^"]*)"`).FindStringSubmatch(body)
	if m == nil || m[1] == "" {
		t.Fatalf("no nonce for forum_action=%s", action)
	}
	return m[1]
}

// forumRegisterForm is a complete, valid sign-up form (time token old enough).
func forumRegisterForm(srv *Server, nonce, login, email, pass string) url.Values {
	return url.Values{"forum_action": {"register"}, "_wpnonce": {nonce}, "user_login": {login}, "user_email": {email}, "user_pass": {pass},
		commentTimeField: {srv.commentTimeToken(0, time.Now().Add(-5*time.Second))}}
}

// forumLastMail is the newest queued email of a kind: its recipient and body.
func forumLastMail(t *testing.T, srv *Server, kind string) (int, string, string) {
	t.Helper()
	var n int
	var to, body string
	ctx := context.Background()
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = 'wp_mail' AND payload->>'Kind' = $1`, kind).Scan(&n)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT coalesce(payload->'To'->>0,''), coalesce(payload->>'Body','') FROM jobs WHERE name = 'wp_mail' AND payload->>'Kind' = $1 ORDER BY id DESC LIMIT 1`, kind).Scan(&to, &body)
	return n, to, body
}

func forumUserByLogin(t *testing.T, srv *Server, login string) *store.User {
	t.Helper()
	u, err := srv.store.UserByLoginOrEmail(context.Background(), login)
	if err != nil {
		t.Fatalf("user %s: %v", login, err)
	}
	return u
}

// forumLogin logs a visitor in through the forum's login form.
func forumLogin(t *testing.T, base string, c *http.Client, login, pass, redirect string) (int, string, string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/forums/login/")
	form := url.Values{"forum_action": {"login"}, "_wpnonce": {forumAuthFormNonce(t, body, "login")}, "log": {login}, "pwd": {pass}}
	if redirect != "" {
		form.Set("redirect", redirect)
	}
	return post(t, c, base+"/forums/login/", form)
}

// Email sign-up: the form, spam traps, the account with the forum role, the queued confirmation mail,
// posting blocked until confirmed, tampered / expired / valid links, resend without enumeration,
// login with redirects, the account page, and Registration off.
func TestForumAccountEmail(t *testing.T) {
	ts, _, srv := forumAuthSetup(t)
	base := ts.URL
	ctx := context.Background()
	anon := forumJarClient()

	code, body, hdr := func() (int, string, http.Header) {
		resp, err := anon.Get(base + "/forums/register/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}()
	if code != http.StatusOK || !strings.Contains(body, `data-view="register"`) || !strings.Contains(body, `name="user_login"`) ||
		!strings.Contains(body, `name="wpgo_website"`) || strings.Contains(body, "Continue with Google") {
		t.Fatalf("register form: %d", code)
	}
	if hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("register view is cacheable: %q", hdr.Get("Cache-Control"))
	}
	nonce := forumAuthFormNonce(t, body, "register")

	// Spam traps: the honeypot, a token that is too fresh, no token at all.
	f := forumRegisterForm(srv, nonce, "spammer", "spam@example.test", "long enough pw")
	f.Set(commentHoneypot, "http://spam.example")
	if code, _, _ := post(t, anon, base+"/forums/register/", f); code != http.StatusUnprocessableEntity {
		t.Errorf("honeypot: %d", code)
	}
	f = forumRegisterForm(srv, nonce, "spammer", "spam@example.test", "long enough pw")
	f.Set(commentTimeField, srv.commentTimeToken(0, time.Now()))
	if code, _, _ := post(t, anon, base+"/forums/register/", f); code != http.StatusUnprocessableEntity {
		t.Errorf("too fast: %d", code)
	}
	f.Del(commentTimeField)
	if code, _, _ := post(t, anon, base+"/forums/register/", f); code != http.StatusUnprocessableEntity {
		t.Errorf("no time token: %d", code)
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "spammer"); err == nil {
		t.Fatal("spam sign-up created a user")
	}
	// A bad nonce and field errors.
	if code, _, _ := post(t, anon, base+"/forums/register/", forumRegisterForm(srv, "bad", "alice", "alice@example.test", "long enough pw")); code != http.StatusForbidden {
		t.Errorf("bad nonce: %d", code)
	}
	code, body, _ = post(t, anon, base+"/forums/register/", forumRegisterForm(srv, nonce, "a", "not-an-email", "short"))
	if code != http.StatusUnprocessableEntity || strings.Count(body, `class="pf-field-error"`) != 3 || !strings.Contains(body, `aria-invalid="true"`) {
		t.Errorf("field errors: %d", code)
	}
	code, body, _ = post(t, anon, base+"/forums/register/", forumRegisterForm(srv, nonce, "admin", "admin@example.test", "long enough pw"))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "This username is taken") || !strings.Contains(body, "already exists") {
		t.Errorf("taken login/email: %d", code)
	}

	// A valid sign-up: the member exists with the forum role, unverified, and the mail is queued; not logged in.
	code, body, _ = post(t, anon, base+"/forums/register/", forumRegisterForm(srv, nonce, "alice", "alice@example.test", "long enough pw"))
	if code != http.StatusOK || !strings.Contains(body, "Check your inbox") || !strings.Contains(body, "alice@example.test") {
		t.Fatalf("sign-up: %d", code)
	}
	alice := forumUserByLogin(t, srv, "alice")
	if alice.PrimaryRole() != forumRoleParticipant || !forumMetaBool(alice.Meta, forumUMetaUnverified) || forumNoPassword(alice) {
		t.Errorf("new member: role %s meta %v", alice.PrimaryRole(), alice.Meta)
	}
	if vm, _ := alice.Meta[forumUMetaVerify].(map[string]any); vm == nil || len(forumMetaStr(vm, "hash")) != 64 {
		t.Errorf("verify meta: %v", alice.Meta[forumUMetaVerify])
	}
	n, to, mailBody := forumLastMail(t, srv, "forum_verify")
	if n != 1 || to != "alice@example.test" {
		t.Fatalf("verify mail: %d to %q", n, to)
	}
	m := regexp.MustCompile(`/forums/verify/\?u=(\d+)&k=([A-Za-z0-9_-]+)`).FindStringSubmatch(mailBody)
	if m == nil || m[1] != strconv.FormatInt(alice.ID, 10) {
		t.Fatalf("no link in mail: %s", mailBody)
	}
	key := m[2]
	if strings.Contains(mailBody, forumMetaStr(alice.Meta[forumUMetaVerify].(map[string]any), "hash")) {
		t.Error("the mail carries the stored hash")
	}
	if code, _, _ := get(t, anon, base+"/forums/account/"); code != http.StatusFound {
		t.Errorf("sign-up logged the member in: %d", code)
	}

	// Unverified members can log in but cannot post.
	member := forumJarClient()
	if code, _, loc := forumLogin(t, base, member, "alice", "long enough pw", ""); code != http.StatusFound || loc != "/forums/" {
		t.Fatalf("unverified login: %d %s", code, loc)
	}
	_, body, _ = get(t, member, base+"/forums/account/")
	if !strings.Contains(body, "Please confirm your email address") || !strings.Contains(body, `value="resend"`) {
		t.Error("no verify banner on the account page")
	}
	{
		w := httptest.NewRecorder()
		gc, _ := gin.CreateTestContext(w)
		gc.Request = httptest.NewRequest(http.MethodGet, "/forums/", nil)
		gc.Set(ctxUser, forumUserByLogin(t, srv, "alice"))
		if p := srv.forumNewPage(gc, forumViewIndex, ""); p.CanPost || !p.Unverified {
			t.Errorf("unverified member can post: %+v", p.CanPost)
		}
	}

	// Tampered and expired links fail; the valid one confirms and logs in.
	verifyURL := base + "/forums/verify/?u=" + m[1] + "&k="
	if code, body, _ := get(t, anon, verifyURL+key+"x"); code != http.StatusUnprocessableEntity || !strings.Contains(body, "invalid or has expired") {
		t.Errorf("tampered key: %d", code)
	}
	if code, _, _ := get(t, anon, base+"/forums/verify/?u=999999&k="+key); code != http.StatusUnprocessableEntity {
		t.Errorf("other user: %d", code)
	}
	vm := alice.Meta[forumUMetaVerify].(map[string]any)
	_ = srv.forumSetUserMeta(ctx, alice.ID, map[string]any{forumUMetaVerify: map[string]any{"hash": vm["hash"], "expires": time.Now().Add(-time.Minute).Unix()}})
	if code, _, _ := get(t, anon, verifyURL+key); code != http.StatusUnprocessableEntity {
		t.Errorf("expired key: %d", code)
	}
	_ = srv.forumSetUserMeta(ctx, alice.ID, map[string]any{forumUMetaVerify: vm})
	// A HEAD (mail link scanners) never confirms.
	if req, _ := http.NewRequest(http.MethodHead, verifyURL+key, nil); req != nil {
		if resp, err := anon.Do(req); err != nil || resp.StatusCode != http.StatusOK {
			t.Errorf("HEAD on the link: %v %v", err, resp)
		} else {
			resp.Body.Close()
		}
	}
	if !forumMetaBool(forumUserByLogin(t, srv, "alice").Meta, forumUMetaUnverified) {
		t.Fatal("HEAD confirmed the email")
	}
	if code, _, loc := get(t, anon, verifyURL+key); code != http.StatusFound || !strings.HasSuffix(loc, "/forums/?forum_msg=verified") {
		t.Fatalf("valid key: %d %s", code, loc)
	}
	alice = forumUserByLogin(t, srv, "alice")
	if vm, _ := alice.Meta[forumUMetaVerify].(map[string]any); forumMetaBool(alice.Meta, forumUMetaUnverified) || !forumMetaBool(vm, "used") {
		t.Errorf("verify meta after confirming: %v", alice.Meta)
	}
	if code, body, _ := get(t, anon, base+"/forums/account/"); code != http.StatusOK || !strings.Contains(body, "alice@example.test") {
		t.Errorf("verify did not log in: %d", code)
	}
	// A second open (the member after a link scanner) says "confirmed" and asks to log in: no second session.
	again := forumJarClient()
	if code, _, loc := get(t, again, verifyURL+key); code != http.StatusFound || !strings.HasSuffix(loc, "/forums/login/?forum_msg=verified") {
		t.Errorf("a used key: %d %s", code, loc)
	}
	if code, _, _ := get(t, again, base+"/forums/account/"); code != http.StatusFound {
		t.Error("a used key logged in again")
	}
	if code, _, loc := get(t, anon, base+"/forums/verify/"); code != http.StatusFound || !strings.HasSuffix(loc, "/forums/account/") {
		t.Errorf("verify view for a confirmed member: %d %s", code, loc)
	}

	// Resend: one answer for unknown, verified and unverified addresses; only the last one sends.
	bob, _ := srv.store.CreateUser(ctx, store.NewUser{Login: "bob", Email: "bob@example.test", PasswordHash: "x", Role: forumRoleParticipant})
	_ = srv.forumSetUserMeta(ctx, bob, map[string]any{forumUMetaUnverified: true})
	visitor := forumJarClient()
	_, body, _ = get(t, visitor, base+"/forums/verify/")
	rn := forumAuthFormNonce(t, body, "resend")
	before, _, _ := forumLastMail(t, srv, "forum_verify")
	var locs []string
	for _, email := range []string{"nobody@example.test", "alice@example.test", "bob@example.test"} {
		code, _, loc := post(t, visitor, base+"/forums/verify/", url.Values{"forum_action": {"resend"}, "_wpnonce": {rn}, "user_email": {email}})
		if code != http.StatusFound {
			t.Errorf("resend %s: %d", email, code)
		}
		locs = append(locs, loc)
	}
	if locs[0] != locs[1] || locs[1] != locs[2] || !strings.HasSuffix(locs[0], "forum_msg=verify_sent") {
		t.Errorf("resend answers differ: %v", locs)
	}
	after, to, _ := forumLastMail(t, srv, "forum_verify")
	if after != before+1 || to != "bob@example.test" {
		t.Errorf("resend sent %d mails (last to %s)", after-before, to)
	}
	// The per-network limit: 3 an hour.
	if code, _, _ := post(t, visitor, base+"/forums/verify/", url.Values{"forum_action": {"resend"}, "_wpnonce": {rn}, "user_email": {"bob@example.test"}}); code != http.StatusTooManyRequests {
		t.Errorf("resend limit: %d", code)
	}

	// Login: redirect kept when local, ignored when off-site; already logged in → the target.
	c2 := forumJarClient()
	if code, _, loc := forumLogin(t, base, c2, "alice@example.test", "long enough pw", "/forums/f/general/"); code != http.StatusFound || loc != "/forums/f/general/" {
		t.Errorf("login redirect: %d %s", code, loc)
	}
	c3 := forumJarClient()
	if code, _, loc := forumLogin(t, base, c3, "alice", "long enough pw", "//evil.example/x"); code != http.StatusFound || loc != "/forums/" {
		t.Errorf("off-site redirect: %d %s", code, loc)
	}
	if code, _, loc := get(t, c3, base+"/forums/login/?redirect=https://evil.example/"); code != http.StatusFound || loc != "/forums/" {
		t.Errorf("logged-in login view: %d %s", code, loc)
	}
	if code, body, _ := forumLogin(t, base, forumJarClient(), "alice", "wrong password", ""); code != http.StatusUnprocessableEntity ||
		!strings.Contains(body, "Wrong username or password.") || !strings.Contains(body, `value="alice"`) {
		t.Errorf("wrong password: %d", code)
	}
	if code, body, _ := forumLogin(t, base, forumJarClient(), "ghost", "wrong password", ""); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Wrong username or password.") {
		t.Errorf("unknown user answer differs: %d", code)
	}

	// Account: profile, password (current required), unlink without Google is a no-op.
	_, body, _ = get(t, c3, base+"/forums/account/")
	an := forumAuthFormNonce(t, body, "account")
	if code, _, loc := post(t, c3, base+"/forums/account/", url.Values{"forum_action": {"account"}, "_wpnonce": {an}, "display_name": {"Alice <b>A</b>"}, "bio": {"Hello there"}}); code != http.StatusFound || !strings.Contains(loc, "forum_msg=saved") {
		t.Errorf("profile save: %d %s", code, loc)
	}
	alice = forumUserByLogin(t, srv, "alice")
	if alice.DisplayName != "Alice A" || alice.Profile["description"] != "Hello there" {
		t.Errorf("profile: %q %q", alice.DisplayName, alice.Profile["description"])
	}
	if code, _, _ := post(t, c3, base+"/forums/account/", url.Values{"forum_action": {"account"}, "_wpnonce": {"bad"}, "display_name": {"X"}}); code != http.StatusForbidden {
		t.Errorf("account without nonce: %d", code)
	}
	pn := forumAuthFormNonce(t, body, "password")
	if code, _, _ := post(t, c3, base+"/forums/account/", url.Values{"forum_action": {"password"}, "_wpnonce": {pn}, "current_pass": {"nope"}, "new_pass": {"a brand new pw"}}); code != http.StatusUnprocessableEntity {
		t.Errorf("wrong current password: %d", code)
	}
	if code, _, _ := post(t, c3, base+"/forums/account/", url.Values{"forum_action": {"password"}, "_wpnonce": {pn}, "current_pass": {"long enough pw"}, "new_pass": {"short"}}); code != http.StatusUnprocessableEntity {
		t.Errorf("short new password: %d", code)
	}
	if code, _, loc := post(t, c3, base+"/forums/account/", url.Values{"forum_action": {"password"}, "_wpnonce": {pn}, "current_pass": {"long enough pw"}, "new_pass": {"a brand new pw"}}); code != http.StatusFound || !strings.Contains(loc, "password_changed") {
		t.Errorf("password change: %d %s", code, loc)
	}
	if ok, _ := auth.CheckPasswordCtx(ctx, "a brand new pw", forumUserByLogin(t, srv, "alice").PasswordHash); !ok {
		t.Error("password not changed")
	}
	if code, _, _ := get(t, c2, base+"/forums/account/"); code != http.StatusFound {
		t.Error("the other session survived the password change")
	}
	if code, _, _ := get(t, c3, base+"/forums/account/"); code != http.StatusOK {
		t.Error("the changing session was logged out")
	}

	// Without verification: the sign-up logs in at once.
	st := srv.forumLoad()
	st.RequireVerify = false
	if err := srv.forumSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	c4 := forumJarClient()
	_, body, _ = get(t, c4, base+"/forums/register/?redirect=/forums/f/news/")
	f = forumRegisterForm(srv, forumAuthFormNonce(t, body, "register"), "carol", "carol@example.test", "long enough pw")
	f.Set("redirect", "/forums/f/news/")
	if code, _, loc := post(t, c4, base+"/forums/register/", f); code != http.StatusFound || loc != "/forums/f/news/" {
		t.Errorf("sign-up without verification: %d %s", code, loc)
	}
	if code, _, _ := get(t, c4, base+"/forums/account/"); code != http.StatusOK {
		t.Error("not logged in after sign-up")
	}
	if u := forumUserByLogin(t, srv, "carol"); forumMetaBool(u.Meta, forumUMetaUnverified) {
		t.Error("carol is unverified")
	}

	// Registration off: the form is a 404 and the POST is refused.
	st.Registration = false
	if err := srv.forumSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	c5 := forumJarClient()
	if code, _, _ := get(t, c5, base+"/forums/register/"); code != http.StatusNotFound {
		t.Errorf("register view while closed: %d", code)
	}
	if code, _, _ := post(t, c5, base+"/forums/register/", forumRegisterForm(srv, nonce, "dave", "dave@example.test", "long enough pw")); code != http.StatusNotFound {
		t.Errorf("register POST while closed: %d", code)
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "dave"); err == nil {
		t.Error("closed registration created a user")
	}
	if _, body, _ := get(t, c5, base+"/forums/login/"); strings.Contains(body, "Create an account</a>") {
		t.Error("login view links to the closed registration")
	}
}

// Login throttling: after 5 failures from one address the form refuses even the right password.
func TestForumAccountThrottle(t *testing.T) {
	ts, _, srv := forumAuthSetup(t)
	base := ts.URL
	ctx := context.Background()
	hash, _ := auth.HashPassword("right password")
	if _, err := srv.store.CreateUser(ctx, store.NewUser{Login: "erin", Email: "erin@example.test", PasswordHash: hash, Role: forumRoleParticipant}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if code, _, _ := forumLogin(t, base, forumJarClient(), "erin", "wrong", ""); code != http.StatusUnprocessableEntity {
			t.Fatalf("failure %d: %d", i+1, code)
		}
	}
	code, body, _ := forumLogin(t, base, forumJarClient(), "erin", "right password", "")
	if code != http.StatusTooManyRequests || !strings.Contains(body, "Too many failed login attempts") {
		t.Errorf("not throttled: %d", code)
	}
}

// forumPostAvatar sends the account page's photo form with file data (nil = no file part).
func forumPostAvatar(t *testing.T, c *http.Client, account, nonce, action string, data []byte) (int, string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("forum_action", action)
	_ = mw.WriteField("_wpnonce", nonce)
	if data != nil {
		fw, _ := mw.CreateFormFile("avatar", "me.png")
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	resp, err := c.Post(account, mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, bodyOf(resp), resp.Header.Get("Location")
}

// The account page's profile photo: upload (shown on the account page and posts), a refused
// file, remove (back to the initials avatar); blocked members may still change their photo.
func TestForumAccountAvatar(t *testing.T) {
	fx := forumFrontStart(t, false)
	c, id := fx.member(t, "pat", "forum_participant")
	if _, err := fx.srv.forumCreateTopic(fx.ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: id, Title: "Pat says hi", Content: "<p>hi</p>"}); err != nil {
		t.Fatal(err)
	}
	account := fx.url("account")
	_, body, _ := get(t, c, account)
	forumNoTemplateError(t, "account", body)
	wbtfWant(t, "account", body, `class="pf-card pf-avatar-card"`, `enctype="multipart/form-data"`, `name="avatar"`, `accept="image/jpeg,image/png,image/webp,image/gif"`,
		"pf-avatar--initials", "Upload photo", "pf-level--newcomer", "points to Member")
	wbtfBad(t, "account", body, "Remove photo")
	nonce := forumFormNonce(t, body, "avatar_upload")

	// A text file is refused with the reason.
	code, body, _ := forumPostAvatar(t, c, account, nonce, "avatar_upload", []byte("not an image at all"))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Please choose a JPEG, PNG, WebP or GIF image.") {
		t.Errorf("text upload: %d", code)
	}
	if code, body, _ := forumPostAvatar(t, c, account, nonce, "avatar_upload", nil); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Please choose a photo to upload.") {
		t.Errorf("no file: %d", code)
	}
	if code, _, _ := forumPostAvatar(t, c, account, "bad-nonce", "avatar_upload", forumTestImage(t, 64, 64, "png", false)); code != http.StatusForbidden {
		t.Errorf("bad nonce: %d", code)
	}

	// Blocked members may change their photo.
	if err := fx.srv.store.SetUserCapExceptions(fx.ctx, id, map[string]string{capForumParticipate: "deny"}); err != nil {
		t.Fatal(err)
	}
	code, _, loc := forumPostAvatar(t, c, account, nonce, "avatar_upload", forumTestImage(t, 120, 80, "png", false))
	if code != http.StatusFound || !strings.Contains(loc, "forum_msg=avatar_saved") {
		t.Fatalf("upload: %d %s", code, loc)
	}
	u, err := fx.srv.store.UserByID(fx.ctx, id)
	if err != nil || avatarIDOf(u) == 0 {
		t.Fatalf("avatar_id not set: %v", err)
	}
	_, body, _ = get(t, c, account+"?forum_msg=avatar_saved")
	wbtfWant(t, "account after upload", body, "Your profile photo is updated.", `<img class="pf-avatar avatar" style="--pf-av-size:96px"`, "Remove photo", "Choose a new photo")
	_, prof, _ := get(t, fx.anon, fx.url("u", "pat"))
	if !strings.Contains(prof, `<img class="pf-avatar avatar" style="--pf-av-size:112px"`) {
		t.Error("the profile does not show the photo")
	}

	// Remove: the initials avatar again.
	code, _, loc = forumPostAvatar(t, c, account, forumFormNonce(t, body, "avatar_remove"), "avatar_remove", nil)
	if code != http.StatusFound || !strings.Contains(loc, "forum_msg=avatar_removed") {
		t.Fatalf("remove: %d %s", code, loc)
	}
	_, body, _ = get(t, c, account)
	wbtfWant(t, "account after remove", body, "pf-avatar--initials")
	wbtfBad(t, "account after remove", body, "Remove photo", `<img class="pf-avatar avatar"`)
	if u, _ := fx.srv.store.UserByID(fx.ctx, id); avatarIDOf(u) != 0 {
		t.Error("avatar_id is still set after remove")
	}
}

// Photo changes are bounded per member and window.
func TestForumAvatarThrottle(t *testing.T) {
	now := time.Now()
	const id = -7001 // never a real user id: the counter is per process
	for i := 0; i < forumAvatarMax; i++ {
		if !forumAvatarAllow(id, now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("change %d refused", i+1)
		}
	}
	if forumAvatarAllow(id, now.Add(time.Minute)) {
		t.Error("change over the limit allowed")
	}
	if forumAvatarAllow(id+1, now.Add(time.Minute)) == false {
		t.Error("another member is limited")
	}
	if !forumAvatarAllow(id, now.Add(forumAvatarWindow+time.Minute)) {
		t.Error("still limited after the window")
	}
}
