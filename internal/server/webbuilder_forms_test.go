package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Tests of the public form endpoint POST web-builder/v2/forms/{post}/{section}. The unit tests run
// without a database; the integration tests need WPGO_TEST_ADMIN_DB / WPGO_TEST_DB.

// --- unit --------------------------------------------------------------------------------------

func TestWebBuilderFormSpam(t *testing.T) {
	cases := []struct {
		v    map[string]string
		spam bool
	}{
		{map[string]string{}, false},                               // no JS: no timing
		{map[string]string{"wb_t": ""}, false},                     // empty timing
		{map[string]string{"wb_t": "   "}, false},                  // blank timing
		{map[string]string{"wb_t": "2500"}, false},                 // human
		{map[string]string{"wb_t": "2000"}, false},                 // the boundary
		{map[string]string{"wb_t": "1999"}, true},                  // too fast
		{map[string]string{"wb_t": "abc"}, true},                   // tampered
		{map[string]string{"wb_t": "-5"}, true},                    // tampered
		{map[string]string{"wb_hp": "x", "wb_t": "9000"}, true},    // honeypot
		{map[string]string{"wb_hp": "   ", "wb_t": "9000"}, false}, // whitespace honeypot, human timing
	}
	for i, tc := range cases {
		if got := wbFormSpam(tc.v); got != tc.spam {
			t.Errorf("case %d %v: spam=%v, want %v", i, tc.v, got, tc.spam)
		}
	}
}

func TestWebBuilderFormLimiter(t *testing.T) {
	l := &wbFormLimiter{hits: map[string]*wbFormHits{}}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < wbFormLimit; i++ {
		if ok, _ := l.allow("1.2.3.4", now); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	ok, wait := l.allow("1.2.3.4", now.Add(time.Minute))
	if ok || wait != wbFormWindow-time.Minute {
		t.Fatalf("over the limit: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.allow("5.6.7.8", now); !ok {
		t.Fatal("another address is limited too")
	}
	if ok, _ := l.allow("1.2.3.4", now.Add(wbFormWindow)); !ok {
		t.Fatal("the window did not reset")
	}
	// Expired entries are swept.
	l.allow("9.9.9.9", now.Add(3*wbFormWindow))
	if _, ok := l.hits["5.6.7.8"]; ok {
		t.Fatal("stale entry kept")
	}
}

func TestWebBuilderFormIPKey(t *testing.T) {
	if got := wbFormIPKey("203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("v4: %s", got)
	}
	if a, b := wbFormIPKey("2001:db8:1:2:aaaa::1"), wbFormIPKey("2001:db8:1:2:bbbb::9"); a != b || a != "2001:db8:1:2::" {
		t.Fatalf("v6 /64: %s %s", a, b)
	}
}

func TestWebBuilderFormHelpers(t *testing.T) {
	if got := wbFormBackURL("http://example.test/contact/", "sent", "s_abc"); got != "http://example.test/contact/?wb_form=sent#s_abc" {
		t.Fatalf("back: %s", got)
	}
	if got := wbFormBackURL("http://example.test/?page_id=4", "error", "s_abc"); got != "http://example.test/?page_id=4&wb_form=error#s_abc" {
		t.Fatalf("back with query: %s", got)
	}
	if got := wbFormLine("Hi\r\nBcc: evil@example.test\t x\u2028y"); got != "Hi Bcc: evil@example.test x y" {
		t.Fatalf("line: %q", got)
	}
}

// --- integration -------------------------------------------------------------------------------

type wbformSite struct {
	srv  *Server
	base string
	user *http.Client // logged in (cookies, no nonce)
	page *store.Post
}

func wbformStart(t *testing.T) *wbformSite {
	t.Helper()
	ts, c, srv := newTestServer(t, true)
	installAndLogin(t, ts, c)
	if !srv.wbOn() {
		t.Fatal("Pursae Web Builder is off")
	}
	return &wbformSite{srv: srv, base: ts.URL, user: c, page: wbformPage(t, srv, "Contact", "publish")}
}

// wbformPage inserts a page and publishes the variants-form fixture as its builder doc.
func wbformPage(t *testing.T, srv *Server, title, status string) *store.Post {
	t.Helper()
	ctx := context.Background()
	p := &store.Post{Type: "page", Title: title, Slug: strings.ToLower(title), Status: status, AuthorID: 1, Content: "<p>fallback</p>"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "webbuilder", "testdata", "render", "variants-form.doc.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := &store.WebBuilderDoc{PostID: p.ID, Doc: raw, Renderer: webbuilder.RendererVersion, ContentHash: store.WebBuilderContentHash(p.Content)}
	if err := srv.store.WebBuilder().PublishDoc(ctx, d, 0); err != nil {
		t.Fatal(err)
	}
	return p
}

// formToken is the wb_nonce the handler accepts for this client and form.
func (w *wbformSite) formToken(c *http.Client, postID int64, section string) string {
	t := ""
	if c != nil && c.Jar != nil {
		u, _ := url.Parse(w.base)
		for _, ck := range c.Jar.Cookies(u) {
			if ck.Name == auth.CookieName {
				t = ck.Value
			}
		}
	}
	if t == "" {
		return "s." + w.srv.auth.Sign(wbFormSignMsg(postID, section))
	}
	sess, err := w.srv.auth.Lookup(context.Background(), t)
	if err != nil {
		return "s." + w.srv.auth.Sign(wbFormSignMsg(postID, section))
	}
	return "n." + w.srv.auth.Nonce(wbFormNonceAction(postID, section), sess.UserID, sess.Token)
}

// submit posts form values to a form of the page; json selects the runtime's Accept header.
// A missing wb_t is filled with a human timing. wb_t or wb_nonce set to "omit" is left out.
// wb_origin "omit" sends no Origin; any other wb_origin value is sent as the Origin header.
func (w *wbformSite) submit(t *testing.T, c *http.Client, postID int64, section string, v url.Values, asJSON bool) (int, map[string]any, http.Header) {
	t.Helper()
	origin := w.base
	switch v.Get("wb_origin") {
	case "":
	case "omit":
		origin = ""
	default:
		origin = v.Get("wb_origin")
	}
	v.Del("wb_origin")
	if v.Get(webbuilder.FormNonce) == "omit" {
		v.Del(webbuilder.FormNonce)
	} else if v.Get(webbuilder.FormNonce) == "" {
		v.Set(webbuilder.FormNonce, w.formToken(c, postID, section))
	}
	if v.Get("wb_t") == "omit" {
		v.Del("wb_t")
	} else if _, ok := v["wb_t"]; !ok {
		v.Set("wb_t", "4200")
	}
	req, _ := http.NewRequest(http.MethodPost, w.base+"/wp-json/web-builder/v2/forms/"+strconv.FormatInt(postID, 10)+"/"+section, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if asJSON {
		req.Header.Set("Accept", "application/json")
	}
	if c == nil {
		c = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, resp.Header
}

const (
	wbfContact    = "s_dc4fyxe" // contact-list: name, email, choice, message
	wbfNewsletter = "s_gngcnd3" // newsletter: email
)

func wbfValid() url.Values {
	return url.Values{"name": {"Ada"}, "email": {"ada@example.test"}, "choice": {"Sales"}, "message": {"Hello\nthere"}, "wb_hp": {""}, "wb_t": {"4200"}}
}

func TestWebBuilderFormSubmit(t *testing.T) {
	w := wbformStart(t)

	// A visitor (no cookies) sends a valid message: JSON answer, one mail to admin_email.
	v := wbfValid()
	v.Set("evil", "dropped")
	v.Set("phone", "+1 555") // a known field name the contact-list form does not have
	code, out, hdr := w.submit(t, nil, w.page.ID, wbfContact, v, true)
	if code != http.StatusOK || out["ok"] != true || out["message"] == "" {
		t.Fatalf("submit: %d %v", code, out)
	}
	if cc := hdr.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control %q", cc)
	}
	jobs := mailJobs(t, w.srv)
	if len(jobs) != 1 {
		t.Fatalf("mails: %d", len(jobs))
	}
	m := jobs[0]
	to, _ := m["To"].([]any)
	body, _ := m["Body"].(string)
	if len(to) != 1 || to[0] != "admin@example.test" || m["ReplyTo"] != "ada@example.test" {
		t.Fatalf("mail header: %v", m)
	}
	if subj, _ := m["Subject"].(string); !strings.HasPrefix(subj, "[Toggle Site] ") {
		t.Fatalf("subject %q", subj)
	}
	for _, want := range []string{"Ada", "ada@example.test", "Sales", "Hello\nthere", "/contact"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body misses %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{"dropped", "+1 555", "4200"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body has %q:\n%s", bad, body)
		}
	}

	// No JavaScript: 303 back to the page with ?wb_form=sent#section.
	code, _, hdr = w.submit(t, nil, w.page.ID, wbfNewsletter, url.Values{"email": {"bob@example.test"}}, false)
	if loc := hdr.Get("Location"); code != http.StatusSeeOther || !strings.Contains(loc, "wb_form=sent") || !strings.HasSuffix(loc, "#"+wbfNewsletter) {
		t.Fatalf("no-JS: %d %s", code, loc)
	}
	if n := len(mailJobs(t, w.srv)); n != 2 {
		t.Fatalf("mails after no-JS submit: %d", n)
	}

	// A logged-in browser posts with its session nonce.
	code, out, _ = w.submit(t, w.user, w.page.ID, wbfNewsletter, url.Values{"email": {"carol@example.test"}}, true)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("with cookies: %d %v", code, out)
	}
	// A visitor who logged in after the page loaded still holds the anonymous token.
	anonTok := url.Values{"email": {"dave@example.test"}, "wb_nonce": {w.formToken(nil, w.page.ID, wbfNewsletter)}}
	code, out, _ = w.submit(t, w.user, w.page.ID, wbfNewsletter, anonTok, true)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("logged-in with anonymous token: %d %v", code, out)
	}
}

func TestWebBuilderFormTokenAndTiming(t *testing.T) {
	w := wbformStart(t)
	code, out, _ := w.submit(t, nil, w.page.ID, wbfNewsletter, url.Values{"email": {"a@example.test"}, "wb_nonce": {"omit"}}, true)
	if code != http.StatusForbidden || out["code"] != webbuilder.ErrInvalidNonce {
		t.Fatalf("missing token: %d %v", code, out)
	}
	code, out, _ = w.submit(t, nil, w.page.ID, wbfNewsletter, url.Values{"email": {"a@example.test"}, "wb_origin": {"https://evil.example"}}, true)
	if code != http.StatusForbidden || out["code"] != webbuilder.ErrInvalidNonce {
		t.Fatalf("foreign origin: %d %v", code, out)
	}
	code, out, _ = w.submit(t, nil, w.page.ID, wbfNewsletter, url.Values{"email": {"a@example.test"}, "wb_origin": {"omit"}}, true)
	if code != http.StatusForbidden || out["code"] != webbuilder.ErrInvalidNonce {
		t.Fatalf("missing origin: %d %v", code, out)
	}
	code, out, _ = w.submit(t, nil, w.page.ID, wbfNewsletter, url.Values{"email": {"a@example.test"}, "wb_t": {"omit"}}, true)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("missing timing: %d %v", code, out)
	}
	// Only the post without timing (a no-JS browser) is mailed.
	if n := len(mailJobs(t, w.srv)); n != 1 {
		t.Fatalf("rejected posts were mailed: %d", n)
	}
}

func TestWebBuilderFormInvalid(t *testing.T) {
	w := wbformStart(t)
	v := wbfValid()
	v.Set("email", "not an email")
	v.Set("name", strings.Repeat("x", webbuilder.FormNameMax+1))
	v.Del("message")
	code, out, _ := w.submit(t, nil, w.page.ID, wbfContact, v, true)
	if code != http.StatusUnprocessableEntity || out["code"] != webbuilder.ErrFormInvalid {
		t.Fatalf("invalid: %d %v", code, out)
	}
	data, _ := out["data"].(map[string]any)
	fields, _ := data["fields"].(map[string]any)
	if fields["email"] != webbuilder.FormInvalid || fields["name"] != webbuilder.FormTooLong || fields["message"] != webbuilder.FormRequired {
		t.Fatalf("fields: %v", fields)
	}
	// No JavaScript: back to the page with ?wb_form=error.
	code, _, hdr := w.submit(t, nil, w.page.ID, wbfContact, v, false)
	if code != http.StatusSeeOther || !strings.Contains(hdr.Get("Location"), "wb_form=error") {
		t.Fatalf("no-JS invalid: %d %s", code, hdr.Get("Location"))
	}
	if n := len(mailJobs(t, w.srv)); n != 0 {
		t.Fatalf("mails: %d", n)
	}
	// Not form data.
	req, _ := http.NewRequest(http.MethodPost, w.base+"/wp-json/web-builder/v2/forms/"+itoa64(w.page.ID)+"/"+wbfContact, strings.NewReader(`{"email":"a@b.test"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("JSON body: %d", resp.StatusCode)
	}
	// Too large.
	big := wbfValid()
	big.Set("message", strings.Repeat("a", webbuilder.MaxFormBodyBytes))
	if code, out, _ := w.submit(t, nil, w.page.ID, wbfContact, big, true); code != http.StatusRequestEntityTooLarge || out["code"] != webbuilder.ErrTooLarge {
		t.Fatalf("too large: %d %v", code, out)
	}
}

func TestWebBuilderFormSpamSilent(t *testing.T) {
	w := wbformStart(t)
	for name, mod := range map[string]func(url.Values){
		"honeypot": func(v url.Values) { v.Set("wb_hp", "http://spam.example") },
		"too fast": func(v url.Values) { v.Set("wb_t", "150") },
		"disallowed": func(v url.Values) {
			v.Set("message", "Buy CHEAP pills now")
		},
	} {
		if name == "disallowed" {
			if err := w.srv.settings.Set(context.Background(), map[string]any{"disallowed_keys": "foo\ncheap pills"}); err != nil {
				t.Fatal(err)
			}
		}
		v := wbfValid()
		mod(v)
		code, out, _ := w.submit(t, nil, w.page.ID, wbfContact, v, true)
		if code != http.StatusOK || out["ok"] != true {
			t.Fatalf("%s: %d %v", name, code, out)
		}
	}
	if n := len(mailJobs(t, w.srv)); n != 0 {
		t.Fatalf("spam was mailed: %d", n)
	}
}

func TestWebBuilderFormRateLimit(t *testing.T) {
	w := wbformStart(t)
	v := url.Values{"email": {"x@example.test"}, "wb_t": {"5000"}}
	for i := 0; i < wbFormLimit; i++ {
		if code, out, _ := w.submit(t, nil, w.page.ID, wbfNewsletter, v, true); code != http.StatusOK {
			t.Fatalf("attempt %d: %d %v", i+1, code, out)
		}
	}
	code, out, hdr := w.submit(t, nil, w.page.ID, wbfNewsletter, v, true)
	if code != http.StatusTooManyRequests || out["code"] != webbuilder.ErrRateLimited {
		t.Fatalf("limit: %d %v", code, out)
	}
	if ra, err := strconv.Atoi(hdr.Get("Retry-After")); err != nil || ra < 1 || ra > int(wbFormWindow/time.Second) {
		t.Fatalf("Retry-After %q", hdr.Get("Retry-After"))
	}
	// Probing other ids counts too and is limited before any lookup.
	if code, _, _ := w.submit(t, nil, 999999, "s_nope", v, true); code != http.StatusTooManyRequests {
		t.Fatalf("probe: %d", code)
	}
	if n := len(mailJobs(t, w.srv)); n != wbFormLimit {
		t.Fatalf("mails: %d", n)
	}
}

func TestWebBuilderFormNotFound(t *testing.T) {
	w := wbformStart(t)
	ctx := context.Background()
	draft := wbformPage(t, w.srv, "Draft", "draft")
	private := wbformPage(t, w.srv, "Private", "private")
	detached := wbformPage(t, w.srv, "Detached", "publish")
	if err := w.srv.store.WebBuilder().SetDetached(ctx, detached.ID, true); err != nil {
		t.Fatal(err)
	}
	plain := &store.Post{Type: "page", Title: "Plain", Slug: "plain", Status: "publish", AuthorID: 1}
	if err := w.srv.store.InsertPost(ctx, plain); err != nil {
		t.Fatal(err)
	}
	locked := wbformPage(t, w.srv, "Locked", "publish")
	if _, err := w.srv.store.Pool.Exec(ctx, `UPDATE posts SET password = 'secret' WHERE id = $1`, locked.ID); err != nil {
		t.Fatal(err)
	}
	v := url.Values{"email": {"x@example.test"}}
	cases := []struct {
		name    string
		id      int64
		section string
		code    string
	}{
		{"draft", draft.ID, wbfNewsletter, webbuilder.ErrNotFound},
		{"private", private.ID, wbfNewsletter, webbuilder.ErrNotFound},
		{"detached", detached.ID, wbfNewsletter, webbuilder.ErrNotFound},
		{"no builder doc", plain.ID, wbfNewsletter, webbuilder.ErrNotFound},
		{"password", locked.ID, wbfNewsletter, webbuilder.ErrNotFound},
		{"missing post", 999999, wbfNewsletter, webbuilder.ErrNotFound},
		{"not a form section", w.page.ID, "s_nope", webbuilder.ErrNotFound},
	}
	for _, tc := range cases {
		if code, out, _ := w.submit(t, nil, tc.id, tc.section, v, true); code != http.StatusNotFound || out["code"] != tc.code {
			t.Errorf("%s: %d %v", tc.name, code, out)
		}
	}
	// A post type the builder does not edit.
	att := &store.Post{Type: "wp_block", Title: "Pattern", Slug: "pattern", Status: "publish", AuthorID: 1}
	if err := w.srv.store.InsertPost(ctx, att); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := w.submit(t, nil, att.ID, wbfNewsletter, v, true); code != http.StatusNotFound || out["code"] != webbuilder.ErrNotBuilder {
		t.Errorf("wp_block: %d %v", code, out)
	}
	// Content edited outside the builder (drift not yet flagged) hides the form too.
	drift := wbformPage(t, w.srv, "Drift", "publish")
	if _, err := w.srv.store.Pool.Exec(ctx, `UPDATE posts SET content = '<p>edited</p>' WHERE id = $1`, drift.ID); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := w.submit(t, nil, drift.ID, wbfNewsletter, v, true); code != http.StatusNotFound {
		t.Errorf("drift: %d", code)
	}
	if n := len(mailJobs(t, w.srv)); n != 0 {
		t.Fatalf("mails: %d", n)
	}

	// Plugin off: no route.
	if err := w.srv.settings.Set(ctx, map[string]any{webbuilder.PluginOption: false}); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := w.submit(t, nil, w.page.ID, wbfNewsletter, v, true); code != http.StatusNotFound || out["code"] != "rest_no_route" {
		t.Fatalf("plugin off: %d %v", code, out)
	}
}

// captchaRT answers siteverify requests: success when the token is "good".
type captchaRT struct{ calls int }

func (r *captchaRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	_ = req.ParseForm()
	ok := req.PostForm.Get("response") == "good" && req.PostForm.Get("secret") == "ts-secret"
	body := `{"success":` + strconv.FormatBool(ok) + `}`
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestWebBuilderFormCaptcha(t *testing.T) {
	w := wbformStart(t)
	rt := &captchaRT{}
	old := captchaClient
	captchaClient = &http.Client{Transport: rt}
	t.Cleanup(func() { captchaClient = old })

	// Keys without Pursae Contact Form active: not checked.
	if err := w.srv.settings.Set(context.Background(), map[string]any{"gontact_turnstile_sitekey": "ts-site", "gontact_turnstile_secret": "ts-secret"}); err != nil {
		t.Fatal(err)
	}
	v := url.Values{"email": {"x@example.test"}}
	if code, _, _ := w.submit(t, nil, w.page.ID, wbfNewsletter, v, true); code != http.StatusOK || rt.calls != 0 {
		t.Fatalf("contact form off: %d calls=%d", code, rt.calls)
	}
	if err := w.srv.settings.Set(context.Background(), map[string]any{gontactSetting: true}); err != nil {
		t.Fatal(err)
	}
	check := func(token, want string) {
		t.Helper()
		v := url.Values{"email": {"x@example.test"}}
		if token != "" {
			v.Set("cf-turnstile-response", token)
		}
		code, out, _ := w.submit(t, nil, w.page.ID, wbfNewsletter, v, true)
		if want == "" {
			if code != http.StatusOK {
				t.Fatalf("token %q: %d %v", token, code, out)
			}
			return
		}
		data, _ := out["data"].(map[string]any)
		fields, _ := data["fields"].(map[string]any)
		if code != http.StatusUnprocessableEntity || fields["captcha"] != want {
			t.Fatalf("token %q: %d %v", token, code, out)
		}
	}
	check("", webbuilder.FormRequired)
	check("bad", webbuilder.FormInvalid)
	check("good", "")
	if n := len(mailJobs(t, w.srv)); n != 2 {
		t.Fatalf("mails: %d", n)
	}
}

// Pursae Security's "REST API for logged-in users only" keeps the public form route open.
func TestWebBuilderFormRESTVisitorsOnly(t *testing.T) {
	w := wbformStart(t)
	if err := w.srv.settings.Set(context.Background(), map[string]any{asSetting: true}); err != nil {
		t.Fatal(err)
	}
	asConfigure(t, w.srv, func(st *asSettings) { st.RESTVisitors = true })
	v := url.Values{"email": {"x@example.test"}}
	if code, out, _ := w.submit(t, nil, w.page.ID, wbfNewsletter, v, true); code != http.StatusOK || out["ok"] != true {
		t.Fatalf("form with REST for logged-in users only: %d %v", code, out)
	}
	req, _ := http.NewRequest(http.MethodGet, w.base+"/wp-json/wp/v2/posts", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("restriction not active: %d", resp.StatusCode)
	}
}

// A template form's page URL trusts the Referer only on the home URL's host, never on a forged
// Host header.
func TestWebBuilderFormTemplatePageURL(t *testing.T) {
	w := wbformStart(t)
	tp := wbtaDesigned(t, w.srv, webbuilder.TemplateFooter, "Footer form", []string{"include/general"}, "publish")
	p, err := w.srv.store.GetPost(context.Background(), tp.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := wbtCtx(w.srv, nil, "/")
	home := w.srv.links(c).HomeURL("/")
	hu, _ := url.Parse(home)

	c, _ = wbtCtx(w.srv, nil, "/")
	c.Request.Host = "evil.example"
	c.Request.Header.Set("Referer", "https://evil.example/login")
	if got := w.srv.wbFormPageURL(c, p); got != home {
		t.Errorf("forged Host: page URL %q, want %q", got, home)
	}
	c, _ = wbtCtx(w.srv, nil, "/")
	c.Request.Header.Set("Referer", hu.Scheme+"://"+hu.Host+"/about/?x=1")
	if got := w.srv.wbFormPageURL(c, p); got != hu.Scheme+"://"+hu.Host+"/about/?x=1" {
		t.Errorf("same-origin Referer: page URL %q", got)
	}
}
