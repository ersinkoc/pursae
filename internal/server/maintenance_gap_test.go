package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"wpgo/internal/webbuilder"
)

func TestMaintPasswordOK(t *testing.T) {
	h, err := maintPasswordHash("open sesame")
	if err != nil || !strings.HasPrefix(h, "$2") || strings.Contains(h, "sesame") {
		t.Fatalf("hash %q %v", h, err)
	}
	if ok, legacy := maintPasswordOK(h, "open sesame"); !ok || legacy {
		t.Error("hash: right password")
	}
	if ok, _ := maintPasswordOK(h, "nope"); ok {
		t.Error("hash: wrong password")
	}
	if ok, legacy := maintPasswordOK("plain1", "plain1"); !ok || !legacy {
		t.Error("legacy plain text")
	}
	if ok, _ := maintPasswordOK("", ""); ok {
		t.Error("empty password accepted")
	}
}

func TestMaintenanceGapPasswordHashedAndNotPrinted(t *testing.T) {
	ts, c, srv := maintenanceSite(t)
	base := ts.URL
	maintSaveTab(t, c, base, "access", url.Values{"site_password": {"open sesame"}, "show_logged_in": {"1"}, "password_button": {"1"}})
	st := srv.maintLoad()
	if !strings.HasPrefix(st.SitePassword, "$2") {
		t.Fatalf("stored password %q is not a hash", st.SitePassword)
	}
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=maintenance&tab=access")
	if strings.Contains(body, "open sesame") || strings.Contains(body, st.SitePassword) {
		t.Error("the password or its hash is printed in the form")
	}
	// An empty field keeps it, the remove box clears it.
	maintSaveTab(t, c, base, "access", url.Values{"show_logged_in": {"1"}, "password_button": {"1"}})
	if srv.maintLoad().SitePassword != st.SitePassword {
		t.Error("an empty field must keep the saved password")
	}
	maintEdit(t, srv, func(s *maintSettings) { s.Enabled = true })
	b := bodyOf(anonGet(t, base+"/"))
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(b)
	if nonce == nil {
		t.Skip("no password form on the page (button off)")
	}
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ := anon.PostForm(base+"/wp-admin/admin-post?action=mtnc_unlock", url.Values{"_wpnonce": {nonce[1]}, "password": {"open sesame"}, "_wp_http_referer": {"/"}})
	resp.Body.Close()
	var got bool
	for _, ck := range resp.Cookies() {
		got = got || ck.Name == maintUnlockCookie
	}
	if !got {
		t.Error("the right password did not unlock")
	}
	maintSaveTab(t, c, base, "access", url.Values{"show_logged_in": {"1"}, "site_password_remove": {"1"}})
	if srv.maintLoad().SitePassword != "" {
		t.Error("remove did not clear the password")
	}
}

func TestMaintenanceGapSubscriberNotify(t *testing.T) {
	ts, c, srv := maintenanceSite(t)
	base := ts.URL
	maintSaveTab(t, c, base, "modules", url.Values{"subscribe": {"1"}, "double_optin": {"1"}, "notify_on_open": {"1"},
		"notify_subject": {"%sitetitle% is live"}, "notify_body": {"Come in: %siteurl%"}})
	maintEdit(t, srv, func(st *maintSettings) { st.Enabled = true })
	b := bodyOf(anonGet(t, base+"/"))
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(b)[1]
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := anon.PostForm(base+"/wp-admin/admin-post?action=mtnc_subscribe", url.Values{"_wpnonce": {nonce}, "email": {"gap@example.test"}, "_wp_http_referer": {"/"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ctx := context.Background()
	subs, _ := srv.maintSubscribers(ctx)
	if len(subs) != 1 || !subs[0].Pending {
		t.Fatalf("subscribers %+v, want one pending", subs)
	}
	// Unconfirmed addresses are not mailed.
	if n, _ := srv.maintNotifySubscribers(ctx, "x", "y"); n != 0 {
		t.Errorf("notified %d unconfirmed subscribers", n)
	}
	// Wrong token refused, right one confirms.
	if r := anonGet(t, base+"/wp-admin/admin-post?action=mtnc_confirm&e=gap@example.test&t=bad"); r.StatusCode != http.StatusForbidden {
		t.Errorf("bad confirm token: %d", r.StatusCode)
	}
	if r := anonGet(t, srv.maintSubURL(ctx, "mtnc_confirm", "gap@example.test")); r.StatusCode != http.StatusOK {
		t.Fatalf("confirm: %d", r.StatusCode)
	}
	if subs, _ = srv.maintSubscribers(ctx); subs[0].Pending {
		t.Fatal("still pending after the link")
	}
	before := len(mailJobs(t, srv))
	if err := srv.maintSetMode(ctx, false); err != nil {
		t.Fatal(err)
	}
	jobs := mailJobs(t, srv)
	if len(jobs) != before+1 {
		t.Fatalf("mails queued on opening: %d, want 1", len(jobs)-before)
	}
	last := jobs[len(jobs)-1]
	if !strings.Contains(last["Subject"].(string), "is live") || !strings.Contains(last["Body"].(string), "mtnc_unsubscribe") {
		t.Errorf("mail %v", last)
	}
	if n, _ := srv.maintNotifySubscribers(ctx, "x", "y"); n != 0 {
		t.Error("a subscriber was told twice")
	}
	// Unsubscribe needs a POST.
	u := srv.maintSubURL(ctx, "mtnc_unsubscribe", "gap@example.test")
	anonGet(t, u)
	if subs, _ = srv.maintSubscribers(ctx); len(subs) != 1 {
		t.Fatal("a GET removed the subscriber")
	}
	pu, _ := url.Parse(u)
	resp, _ = anon.PostForm(u, pu.Query())
	resp.Body.Close()
	if subs, _ = srv.maintSubscribers(ctx); len(subs) != 0 {
		t.Error("unsubscribe did not remove the address")
	}
}

func TestMaintenanceGapAnalyticsConsentAndRESTStatus(t *testing.T) {
	ts, _, srv := maintenanceSite(t)
	base := ts.URL
	maintEdit(t, srv, func(st *maintSettings) { st.Enabled, st.Analytics, st.BlockREST = true, "G-ABC12345", true })
	if b := bodyOf(anonGet(t, base+"/")); !strings.Contains(b, `<script async src="https://www.googletagmanager.com`) {
		t.Error("without Pursae Cookie the tag loads as before")
	}
	if err := srv.settings.Set(context.Background(), map[string]any{gooikeSetting: true}); err != nil {
		t.Fatal(err)
	}
	b := bodyOf(anonGet(t, base+"/"))
	if strings.Contains(b, `<script async src="https://www.googletagmanager.com`) || !strings.Contains(b, `data-gooike-category="statistics"`) ||
		!strings.Contains(b, "gooike-consent-mode") {
		t.Error("with Pursae Cookie the tag must be held back until consent")
	}
	// Coming Soon: REST is refused with 403, not 503; Maintenance keeps 503.
	if r := anonGet(t, base+"/wp-json/wp/v2/posts"); r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("maintenance REST: %d", r.StatusCode)
	}
	maintEdit(t, srv, func(st *maintSettings) { st.Mode = "coming_soon" })
	r := anonGet(t, base+"/wp-json/wp/v2/posts")
	if r.StatusCode != http.StatusForbidden || r.Header.Get("Retry-After") != "" {
		t.Errorf("coming soon REST: %d retry-after %q", r.StatusCode, r.Header.Get("Retry-After"))
	}
}

func TestMaintenanceUnlockRateLimitAndPasswordLength(t *testing.T) {
	ts, c, srv := maintenanceSite(t)
	base := ts.URL
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=maintenance&tab=access")
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "tab": {"access"}, "show_logged_in": {"1"}, "site_password": {"short12"}}
	if _, _, loc := post(t, c, base+"/wp-admin/admin?page=maintenance", form); !strings.Contains(loc, "mtnc-error") {
		t.Fatalf("a 7-character site password was accepted: %s", loc)
	}
	maintSaveTab(t, c, base, "access", url.Values{"site_password": {"open sesame"}, "show_logged_in": {"1"}, "password_button": {"1"}})
	maintEdit(t, srv, func(st *maintSettings) { st.Enabled = true })
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(bodyOf(anonGet(t, base+"/")))
	if nonce == nil {
		t.Fatal("no password form on the maintenance page")
	}
	defer func() { gooikeLimiter.Lock(); gooikeLimiter.hits = map[string]int{}; gooikeLimiter.Unlock() }()
	gooikeLimiter.Lock()
	gooikeLimiter.hits = map[string]int{}
	gooikeLimiter.window = time.Now()
	gooikeLimiter.Unlock()
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	try := func(pw string) *http.Response {
		resp, err := anon.PostForm(base+"/wp-admin/admin-post?action=mtnc_unlock", url.Values{"_wpnonce": {nonce[1]}, "password": {pw}, "_wp_http_referer": {"/"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	for i := 0; i < maintUnlockPerMinute; i++ {
		if r := try("wrong guess"); r.StatusCode != http.StatusSeeOther {
			t.Fatalf("attempt %d: %d", i+1, r.StatusCode)
		}
	}
	r := try("open sesame") // even the right password is refused once the limit is hit
	if r.StatusCode != http.StatusTooManyRequests || len(r.Cookies()) != 0 {
		t.Errorf("after %d attempts: %d, cookies %v", maintUnlockPerMinute, r.StatusCode, r.Cookies())
	}
}

func TestMaintenanceScheduledStartAndLoadCache(t *testing.T) {
	ts, _, srv := maintenanceSite(t)
	base := ts.URL
	fmtT := func(d time.Duration) string { return time.Now().In(srv.loc()).Add(d).Format("2006-01-02T15:04") }
	maintEdit(t, srv, func(st *maintSettings) { st.Enabled, st.StartDate = true, fmtT(2*time.Hour) })
	if r := anonGet(t, base+"/"); r.StatusCode != http.StatusOK {
		t.Errorf("before the scheduled start the site must be open, got %d", r.StatusCode)
	}
	maintEdit(t, srv, func(st *maintSettings) { st.StartDate = fmtT(-time.Hour) })
	if r := anonGet(t, base+"/"); r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("after the start the maintenance page is due, got %d", r.StatusCode)
	}
	// The scheduler tick alone (no visitor) switches the mode off when the countdown ended.
	maintEdit(t, srv, func(st *maintSettings) {
		st.StartDate, st.Countdown, st.AutoDisable, st.CountdownEnd = "", true, true, fmtT(-time.Minute)
	})
	srv.maintTick(context.Background())
	if srv.maintLoad().Enabled {
		t.Error("maintTick did not apply the auto-disable")
	}
	// The cached decode is invalidated by a save and not shared by reference.
	a := srv.maintLoad()
	a.Social["x"] = "mutated"
	if _, ok := srv.maintLoad().Social["x"]; ok {
		t.Error("a caller's change leaked into the cached settings")
	}
	maintEdit(t, srv, func(st *maintSettings) { st.Heading = "Fresh heading" })
	if srv.maintLoad().Heading != "Fresh heading" {
		t.Error("the settings cache returned stale data after a save")
	}
}

func TestMaintenanceTranslatedPage(t *testing.T) {
	ts, c, srv := maintenanceSite(t)
	base := ts.URL
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	maintEdit(t, srv, func(st *maintSettings) { st.Enabled, st.Heading, st.Content = true, "Back soon", "We are upgrading." })
	ctx := context.Background()
	if err := srv.pllSetString(ctx, "tr", "Back soon", "Birazdan döneceğiz"); err != nil {
		t.Fatal(err)
	}
	srv.pllFlushStrings()
	get := func(accept, path string) string {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		if accept != "" {
			req.Header.Set("Accept-Language", accept)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return bodyOf(resp)
	}
	if b := get("tr-TR,tr;q=0.9", "/"); !strings.Contains(b, "Birazdan döneceğiz") || !strings.Contains(b, `lang="tr-TR"`) {
		t.Errorf("Turkish visitor did not get the translated page:\n%.600s", b)
	}
	if b := get("tr-TR", "/en/"); !strings.Contains(b, "Back soon") || strings.Contains(b, "Birazdan") {
		t.Error("the language code in the URL must win over Accept-Language")
	}
	if b := get("de", "/"); !strings.Contains(b, "Back soon") {
		t.Error("an untranslated or unknown language falls back to the original text")
	}
	if !strings.Contains(get("tr", "/"), "We are upgrading.") {
		t.Error("a string without a translation must stay as written")
	}
}

func TestMaintenanceWebBuilderPage(t *testing.T) {
	ts, c, srv := maintenanceSite(t)
	base := ts.URL
	tpl := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Back soon design", nil, 0, "publish")
	draft := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Draft design", nil, 0, "draft")
	// The select offers the published template only; a draft is refused on save.
	code0, body, _ := get(t, c, base+"/wp-admin/admin?page=maintenance&tab=general")
	_ = code0
	if !strings.Contains(body, "Back soon design") || strings.Contains(body, "Draft design") {
		t.Error("the Page Design select must list published templates only")
	}
	_, nbody, _ := get(t, c, base+"/wp-admin/admin?page=maintenance&tab=general")
	bad := url.Values{"_wpnonce": {formNonce(t, nbody)}, "tab": {"general"}, "status": {"1"}, "web_builder_page": {itoa64(draft.ID)}}
	if _, _, loc := post(t, c, base+"/wp-admin/admin?page=maintenance", bad); !strings.Contains(loc, "mtnc-error") {
		t.Errorf("a draft template was accepted: %s", loc)
	}
	maintSaveTab(t, c, base, "general", url.Values{"status": {"1"}, "web_builder_page": {itoa64(tpl.ID)}, "mode": {"maintenance"}})
	if srv.maintLoad().WBTemplate != tpl.ID {
		t.Fatal("the template was not saved")
	}
	r := anonGet(t, base+"/")
	b := bodyOf(r)
	if r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(b, "Back soon design") || !strings.Contains(b, `data-wb-root`) || strings.Contains(b, "mtnc-") {
		t.Fatalf("Web Builder page: %d\n%.600s", r.StatusCode, b)
	}
	if r.Header.Get("Retry-After") == "" || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Error("the Web Builder page must keep the maintenance headers")
	}
	// A template that stops being published falls back to the built-in page.
	if _, err := srv.store.Pool.Exec(context.Background(), `UPDATE posts SET status = 'draft' WHERE id = $1`, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if b := bodyOf(anonGet(t, base+"/")); strings.Contains(b, "Back soon design") || !strings.Contains(b, "mtnc-") {
		t.Error("an unpublished template must fall back to the built-in page")
	}
}
