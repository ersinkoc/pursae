package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestGooikeInert(t *testing.T) {
	code := `<script async src="https://www.googletagmanager.com/gtag/js?id=G-ABC123"></script>
<script type="module">import x from "/a.js";</script>
<noscript><img src="https://px.example/p.gif"></noscript>
<iframe src="https://example.com/frame"></iframe><img src="https://px.example/b.gif" srcset="https://px.example/b2.gif 2x">`
	out := gooikeInert(code, "statistics")
	for _, want := range []string{
		`<script type="text/plain" data-gooike-category="statistics" async data-gooike-src="https://www.googletagmanager.com/gtag/js?id=G-ABC123"></script>`,
		`<script type="text/plain" data-gooike-category="statistics" data-gooike-type="module">import x from "/a.js";</script>`,
		`<iframe data-gooike-category="statistics" data-gooike-src="https://example.com/frame">`,
		`data-gooike-srcset="https://px.example/b2.gif 2x"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inert output lacks %s\n%s", want, out)
		}
	}
	if strings.Contains(out, "<noscript") || regexp.MustCompile(`\ssrc=`).MatchString(out) {
		t.Errorf("a blocked snippet keeps a noscript or a live src:\n%s", out)
	}
	if got := gooikeInert("console.log('x')", "marketing"); got != "<script type=\"text/plain\" data-gooike-category=\"marketing\">\nconsole.log('x')\n</script>" {
		t.Errorf("plain JS: %q", got)
	}
	if got := gooikeInert(`<script src="/x.js"></script>`, "necessary"); got != `<script src="/x.js"></script>` {
		t.Errorf("necessary scripts are touched: %q", got)
	}
}

func TestGooikeHelpers(t *testing.T) {
	for in, want := range map[string]string{"203.0.113.77": "203.0.113.0", "2001:db8:abcd:12::1": "2001:db8:abcd::", "nope": ""} {
		if got := gooikeTruncateIP(in); got != want {
			t.Errorf("gooikeTruncateIP(%q) = %q, want %q", in, got, want)
		}
	}
	st := gooikeDefaults()
	for src, want := range map[string]string{
		"https://www.youtube.com/embed/abc":                        "youtube",
		"https://www.youtube-nocookie.com/embed/abc":               "youtube",
		"//player.vimeo.com/video/1":                               "vimeo",
		"https://www.google.com/maps/embed?pb=1":                   "maps",
		"https://example.com/embed":                                "",
		"https://notyoutube.com/embed":                             "",
		"https://www.google.com/search?q=maps":                     "",
		"https://www.youtube.com/embed/abc?feature=oembed&amp;x=1": "youtube",
	} {
		if got, _ := gooikeEmbedFor(&st, src); got != want {
			t.Errorf("gooikeEmbedFor(%q) = %q, want %q", src, got, want)
		}
	}
	st.Scripts = []gooikeScript{{Service: "meta", ServiceID: "123456789", Enabled: true, Category: "marketing"},
		{Code: `<script src="https://cdn.example.org/x.js"></script>`, Enabled: true, Category: "statistics"},
		{Service: "ga4", ServiceID: "G-OFF1234", Enabled: false}}
	st.ExtraHosts = "https://api.example.net\njavascript:alert(1)"
	src := gooikeCSPSources(&st)
	for _, want := range []string{"https://connect.facebook.net", "https://cdn.example.org", "https://api.example.net"} {
		if !gooikeContains(src["script-src"], want) {
			t.Errorf("script-src lacks %s: %v", want, src["script-src"])
		}
	}
	if gooikeContains(src["script-src"], "https://www.googletagmanager.com") || gooikeContains(src["script-src"], "javascript:alert(1)") {
		t.Errorf("disabled or invalid sources allowed: %v", src["script-src"])
	}
	// Sanitizing brings back the built-in categories and keeps keys unique.
	bad := gooikeSanitize(gooikeSettings{Categories: []gooikeCategory{{Key: "Social Media!"}, {Key: "social-media"}}})
	if len(bad.Categories) != 5 || bad.Categories[0].Key != "necessary" || bad.category("social-media") == nil {
		t.Errorf("sanitized categories: %+v", bad.Categories)
	}
	if len(gooikeThemes) < 12 {
		t.Errorf("only %d banner themes", len(gooikeThemes))
	}
}

// gooikeSite installs a site, logs the admin in and activates Gooike.
func gooikeSite(t *testing.T) (string, *http.Client, *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=gooike[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for Pursae Cookie on the Plugins screen")
	}
	if code, _, loc := get(t, c, ts.URL+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "activate=true") {
		t.Fatalf("activate Pursae Cookie: %d %s", code, loc)
	}
	if !srv.gooikeOn() {
		t.Fatal("Pursae Cookie is not active after activation")
	}
	return ts.URL, c, srv
}

// gooikePost submits a Gooike admin form.
func gooikePost(t *testing.T, c *http.Client, base string, form url.Values) string {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/gooike?page="+form.Get("page"))
	form.Set("_wpnonce", formNonce(t, body))
	code, _, loc := post(t, c, base+"/wp-admin/gooike", form)
	if code != http.StatusFound || strings.Contains(loc, "gooike-error") {
		t.Fatalf("%s: %d %s", form.Get("do"), code, loc)
	}
	return loc
}

func gooikeConsent(t *testing.T, base string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(base+"/wp-json/gooike/v1/consent", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp
}

func TestGooikeOffByDefault(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	if srv.gooikeOn() {
		t.Fatal("Pursae Cookie is active on a fresh install")
	}
	if b := bodyOf(anonGet(t, base+"/")); strings.Contains(b, "gooike") {
		t.Error("the front end mentions Pursae Cookie while it is off")
	}
	for _, u := range []string{"/wp-admin/gooike", "/wp-admin/gooike?page=logs", gooikeAssets + "/gooike.js"} {
		if code, _, _ := get(t, c, base+u); code != http.StatusNotFound {
			t.Errorf("%s while off: %d", u, code)
		}
	}
	if r := gooikeConsent(t, base, map[string]any{"consent_id": "0123456789abcdef0123", "action": "accept_all"}); r.StatusCode != http.StatusNotFound {
		t.Errorf("consent endpoint while off: %d", r.StatusCode)
	}
	if _, body, _ := get(t, c, base+"/wp-admin/"); strings.Contains(body, "toplevel_page_gooike") {
		t.Error("the Pursae Cookie menu shows while the plugin is off")
	}
}

func TestGooikeFrontAndScripts(t *testing.T) {
	base, c, srv := gooikeSite(t)
	if _, body, _ := get(t, c, base+"/wp-admin/"); !strings.Contains(body, "toplevel_page_gooike") || !strings.Contains(body, "gooike?page=logs") {
		t.Error("the Pursae Cookie menu is missing while the plugin is active")
	}
	for _, p := range []string{"", "banner", "banner&tab=design", "banner&tab=texts", "categories", "categories&tab=cookies", "categories&tab=scan", "scripts", "logs", "settings", "preview"} {
		if code, body, _ := get(t, c, base+"/wp-admin/gooike?page="+p); code != http.StatusOK || strings.Contains(body, "Template error") {
			t.Errorf("screen %q: %d", p, code)
		}
	}

	r := anonGet(t, base+"/")
	page := bodyOf(r)
	for _, want := range []string{`id="gooike-banner"`, `gk-theme-classic`, `id="gooike-prefs"`, `role="dialog" aria-modal="true"`, `id="gooike-config"`, gooikeAssets + `/gooike.js`,
		`data-gooike="accept"`, `data-gooike="reject"`, `data-gooike-cat="statistics"`, `id="gooike-consent-mode"`} {
		if !strings.Contains(page, want) {
			t.Errorf("front page lacks %s", want)
		}
	}
	// Consent Mode defaults come before every other script of the page.
	cm := strings.Index(page, `id="gooike-consent-mode"`)
	if first := strings.Index(page, "<script"); first < 0 || cm < 0 || first < cm-len(`<script `) {
		t.Errorf("Consent Mode defaults are not the first script (first script at %d, consent mode at %d)", first, cm)
	}
	if !strings.Contains(page, `gtag('consent','default',d)`) || !strings.Contains(page, `"ad_user_data"`) || !strings.Contains(page, `"ad_personalization"`) {
		t.Error("Consent Mode v2 defaults are incomplete")
	}
	// Accept and Reject are equally prominent.
	if !strings.Contains(page, `class="gk-btn gk-btn-primary gk-btn-reject"`) || !strings.Contains(page, `class="gk-btn gk-btn-primary gk-btn-accept"`) {
		t.Error("Accept and Reject are not the same kind of button")
	}

	// Script manager: a known service, a custom marketing pixel right after <body>, a necessary footer script.
	gooikePost(t, c, base, url.Values{"page": {"scripts"}, "do": {"service_add"}, "service": {"ga4"}, "service_id": {"g-test1234"}, "category": {"statistics"}})
	gooikePost(t, c, base, url.Values{"page": {"scripts"}, "do": {"script_save"}, "name": {"Pixel"}, "category": {"marketing"}, "placement": {"body"}, "enabled": {"1"},
		"code": {`<script src="https://px.example.com/p.js"></script><noscript><img src="https://px.example.com/p.gif"></noscript>`}})
	gooikePost(t, c, base, url.Values{"page": {"scripts"}, "do": {"script_save"}, "name": {"Essential"}, "category": {"necessary"}, "placement": {"footer"}, "enabled": {"1"},
		"code": {`window.essentialRan = true;`}})
	st := srv.gooikeLoad()
	if len(st.Scripts) != 3 || !gooikeDeclared(&st, "_ga") {
		t.Fatalf("scripts %d, _ga declared %v", len(st.Scripts), gooikeDeclared(&st, "_ga"))
	}
	r = anonGet(t, base+"/")
	page = bodyOf(r)
	head, rest, _ := strings.Cut(page, "</head>")
	if !strings.Contains(head, `<script type="text/plain" data-gooike-category="statistics" async data-gooike-src="https://www.googletagmanager.com/gtag/js?id=G-TEST1234"></script>`) {
		t.Error("GA4 is not in <head>, inert")
	}
	bodyOpen := regexp.MustCompile(`(?s)<body[^>]*>\s*(.{0,400})`).FindStringSubmatch(rest)
	if bodyOpen == nil || !strings.Contains(bodyOpen[1], `<script type="text/plain" data-gooike-category="marketing" data-gooike-src="https://px.example.com/p.js">`) {
		t.Errorf("the body-open script is not right after <body>: %q", bodyOpen)
	}
	if strings.Contains(page, "p.gif") {
		t.Error("the noscript fallback of a blocked script is printed")
	}
	foot := rest[strings.Index(rest, `id="gooike-banner"`):]
	if !strings.Contains(foot, "<script>\nwindow.essentialRan = true;\n</script>") {
		t.Error("the necessary script is not printed as a live script in the footer")
	}
	csp := r.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://www.googletagmanager.com") || !strings.Contains(csp, "https://px.example.com") || !strings.Contains(csp, "https://*.google-analytics.com") {
		t.Errorf("CSP does not allow the managed scripts: %s", csp)
	}

	// Theme selection changes the markup and the CSS.
	gooikePost(t, c, base, url.Values{"page": {"banner"}, "do": {"theme"}, "theme": {"wall"}})
	page = bodyOf(anonGet(t, base+"/"))
	if !strings.Contains(page, "gk-theme-wall gk-layout-wall gk-pos-center") || !strings.Contains(page, `--gk-primary:#111827`) || strings.Contains(page, "gk-theme-classic") {
		t.Error("the wall theme is not on the page")
	}
	if !strings.Contains(page, `id="gooike-banner" class="gooike gk-banner gk-theme-wall gk-layout-wall gk-pos-center gk-mode-gdpr" role="dialog" aria-modal="true"`) {
		t.Error("the consent wall is not a modal dialog")
	}
	gooikePost(t, c, base, url.Values{"page": {"banner"}, "tab": {"design"}, "do": {"design"}, "position": {"center"}, "background": {"#123456"}, "text": {"#ffffff"},
		"primary": {"#ff0000"}, "primary_text": {"#000000"}, "font": {"serif"}, "radius": {"12"}, "font_size": {"16"}, "custom_css": {".gk-banner{outline:1px solid red}</style><script>"}})
	page = bodyOf(anonGet(t, base+"/"))
	if !strings.Contains(page, "--gk-bg:#123456") || !strings.Contains(page, ".gk-banner{outline:1px solid red}") || strings.Contains(page, "</style><script>") {
		t.Error("design changes are not on the page, or custom CSS escapes its style element")
	}

	// Shortcodes and embed blocking.
	p := &store.Post{Type: "page", Title: "Cookies", Slug: "cookies", Status: "publish", AuthorID: 1,
		Content: "[gooike_cookie_table]\n\n[gooike_preferences text=\"Manage cookies\"]\n\n<iframe width=\"560\" height=\"315\" src=\"https://www.youtube.com/embed/xyz\"></iframe>\n\n<iframe src=\"https://example.org/widget\"></iframe>\n\n<iframe src=https://player.vimeo.com/video/42></iframe>"}
	if err := srv.store.InsertPost(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(context.Background())
	page = bodyOf(anonGet(t, srv.links(context.Background()).Permalink(p)))
	for _, want := range []string{`class="gooike-cookie-table"`, `<code>_ga</code>`, `>Manage cookies</a>`, `data-gooike-src="https://www.youtube.com/embed/xyz"`,
		`data-gooike-accept-cat="marketing"`, `<iframe src="https://example.org/widget"`} {
		if !strings.Contains(page, want) {
			t.Errorf("cookie page lacks %s", want)
		}
	}
	if strings.Contains(page, `src="https://www.youtube.com/embed/xyz"`) && !strings.Contains(page, `data-gooike-src="https://www.youtube.com/embed/xyz"`) {
		t.Error("the YouTube iframe loads before consent")
	}
	if regexp.MustCompile(`\ssrc=https://player\.vimeo\.com`).MatchString(page) || !strings.Contains(page, `data-gooike-src=https://player.vimeo.com/video/42`) {
		t.Error("an iframe with an unquoted src loads before consent")
	}

	// Deactivating: nothing on the site, settings kept.
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=deactivate&amp;plugin=gooike[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Deactivate link")
	}
	get(t, c, base+html.UnescapeString(m[1]))
	if srv.gooikeOn() {
		t.Fatal("still active")
	}
	if b := bodyOf(anonGet(t, base+"/")); strings.Contains(b, "gooike") || strings.Contains(b, "px.example.com") {
		t.Error("Pursae Cookie output remains after deactivation")
	}
	if len(srv.gooikeLoad().Scripts) != 3 {
		t.Error("settings were lost on deactivation")
	}
}

func TestGooikeConsentLog(t *testing.T) {
	base, c, srv := gooikeSite(t)
	ctx := context.Background()
	var jobs int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = $1`, gooikePurgeJob).Scan(&jobs)
	if jobs != 1 {
		t.Errorf("purge job scheduled %d times", jobs)
	}
	id := "6f1c2b9e-1d2a-4c3b-9e8f-0a1b2c3d4e5f"
	r := gooikeConsent(t, base, map[string]any{"consent_id": id, "action": "custom", "categories": []string{"necessary", "statistics", "bogus"},
		"policy_version": 1, "url": "/hello/", "gpc": false, "regulation": "gdpr"})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("consent: %d %s", r.StatusCode, bodyOf(r))
	}
	for _, bad := range []map[string]any{{"consent_id": "x", "action": "custom"}, {"consent_id": id, "action": "hack"}} {
		if r := gooikeConsent(t, base, bad); r.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid record accepted: %v -> %d", bad, r.StatusCode)
		}
	}
	var ip, ua string
	var cats []string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT ip_anon, ua_hash, categories FROM gooike_consent_log WHERE consent_id = $1`, id).Scan(&ip, &ua, &cats); err != nil {
		t.Fatal(err)
	}
	if ip != "127.0.0.0" || ua == "" || strings.Join(cats, ",") != "necessary,statistics" {
		t.Errorf("stored ip %q ua %q cats %v", ip, ua, cats)
	}
	_, logs, _ := get(t, c, base+"/wp-admin/gooike?page=logs")
	if !strings.Contains(logs, id) || !strings.Contains(logs, "127.0.0.0") || strings.Contains(logs, "127.0.0.1<") {
		t.Error("the logs screen does not show the record")
	}
	_, filtered, _ := get(t, c, base+"/wp-admin/gooike?page=logs&log_action=reject_all")
	if strings.Contains(filtered, id) {
		t.Error("the action filter does not filter")
	}
	nonce := regexp.MustCompile(`action=export&amp;_wpnonce=([a-f0-9]+)`).FindStringSubmatch(logs)
	if nonce == nil {
		t.Fatal("no Export CSV link")
	}
	resp, err := c.Get(base + "/wp-admin/gooike?page=logs&action=export&_wpnonce=" + nonce[1])
	if err != nil {
		t.Fatal(err)
	}
	csvBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(string(csvBody), id) || !strings.Contains(string(csvBody), "necessary statistics") {
		t.Errorf("CSV export: %s\n%s", resp.Header.Get("Content-Type"), csvBody)
	}
	_, dash, _ := get(t, c, base+"/wp-admin/gooike")
	if !regexp.MustCompile(`Custom choice</span><span class="num">1<`).MatchString(dash) {
		t.Error("the dashboard does not count the decision")
	}

	// Hashed IPs; logging off.
	st := srv.gooikeLoad()
	st.IPMode = "hash"
	_ = srv.gooikeSave(ctx, st)
	gooikeConsent(t, base, map[string]any{"consent_id": "aaaaaaaa-1111-4222-8333-444444444444", "action": "accept_all"})
	_ = srv.store.Pool.QueryRow(ctx, `SELECT ip_anon FROM gooike_consent_log WHERE consent_id = 'aaaaaaaa-1111-4222-8333-444444444444'`).Scan(&ip)
	if !strings.HasPrefix(ip, "h:") || strings.Contains(ip, "127") {
		t.Errorf("hashed ip %q", ip)
	}

	// Retention purge.
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO gooike_consent_log (consent_id, action, created_at) VALUES ('old-0000000000000000', 'accept_all', now() - interval '2 years')`)
	n, err := srv.gooikePurge(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if err := srv.jobHandlers()[gooikePurgeJob](ctx, nil); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gooike_consent_log`).Scan(&left)
	if left != 2 {
		t.Errorf("%d records left after the purge, want 2", left)
	}
	st.RetentionMonths = 0
	_ = srv.gooikeSave(ctx, st)
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO gooike_consent_log (consent_id, action, created_at) VALUES ('old-0000000000000001', 'accept_all', now() - interval '20 years')`)
	if n, _ := srv.gooikePurge(ctx); n != 0 {
		t.Error("retention 0 still purges")
	}
	st.LogConsent = false
	_ = srv.gooikeSave(ctx, st)
	if r := gooikeConsent(t, base, map[string]any{"consent_id": "bbbbbbbb-1111-4222-8333-444444444444", "action": "accept_all"}); r.StatusCode != http.StatusOK || !strings.Contains(bodyOf(r), `"logged":false`) {
		t.Errorf("logging off: %d %s", r.StatusCode, bodyOf(r))
	}
}

func TestGooikePageCache(t *testing.T) {
	base, c, srv := gooikeSite(t)
	if !srv.settings.Bool("wpgo_cache_enabled") {
		t.Skip("Pursae Cache is off by default")
	}
	gooikePost(t, c, base, url.Values{"page": {"scripts"}, "do": {"service_add"}, "service": {"meta"}, "service_id": {"123456789012"}})
	first := anonGet(t, base+"/")
	second := anonGet(t, base+"/")
	consented := anonGet(t, base+"/", &http.Cookie{Name: gooikeCookie, Value: url.QueryEscape(`{"id":"6f1c2b9e-1d2a-4c3b-9e8f-0a1b2c3d4e5f","v":1,"t":1,"c":["necessary","marketing"],"a":"accept_all"}`)})
	if second.Header.Get("X-WPGo-Cache") != "hit" || consented.Header.Get("X-WPGo-Cache") != "hit" {
		t.Fatalf("not served from the cache: %q %q", second.Header.Get("X-WPGo-Cache"), consented.Header.Get("X-WPGo-Cache"))
	}
	if b1, b2, b3 := bodyOf(first), bodyOf(second), bodyOf(consented); b1 != b2 || b2 != b3 || !strings.Contains(b3, `data-gooike-category="marketing"`) {
		t.Error("visitors with and without consent do not get the same cached HTML")
	}
	if !strings.Contains(consented.Header.Get("Content-Security-Policy"), "https://connect.facebook.net") {
		t.Error("the cached page lost its CSP allowance")
	}
	// The consent endpoint is never cached.
	r1 := gooikeConsent(t, base, map[string]any{"consent_id": "cccccccc-1111-4222-8333-444444444444", "action": "reject_all"})
	if r1.StatusCode != http.StatusCreated || r1.Header.Get("X-WPGo-Cache") == "hit" {
		t.Errorf("consent through the cache: %d %q", r1.StatusCode, r1.Header.Get("X-WPGo-Cache"))
	}
	// A settings change purges the cache.
	gooikePost(t, c, base, url.Values{"page": {"banner"}, "do": {"theme"}, "theme": {"pill"}})
	if r := anonGet(t, base+"/"); r.Header.Get("X-WPGo-Cache") == "hit" || !strings.Contains(bodyOf(r), "gk-theme-pill") {
		t.Error("changing the theme does not purge the cache")
	}
}

func TestGooikeScanAndGeo(t *testing.T) {
	base, c, srv := gooikeSite(t)
	gooikePost(t, c, base, url.Values{"page": {"scripts"}, "do": {"service_add"}, "service": {"clarity"}, "service_id": {"ABCD1234EF"}})
	st := srv.gooikeLoad()
	st.Cookies = []gooikeCookieRow{} // start from an empty declaration
	_ = srv.gooikeSave(context.Background(), st)
	_, body, _ := get(t, c, base+"/wp-admin/gooike?page=categories&tab=scan")
	code, page, _ := post(t, c, base+"/wp-admin/gooike", url.Values{"_wpnonce": {formNonce(t, body)}, "page": {"categories"}, "tab": {"scan"}, "do": {"scan"}})
	if code != http.StatusOK || !strings.Contains(page, "Microsoft Clarity") || !strings.Contains(page, "<code>_clck</code>") {
		t.Fatalf("scan results: %d", code)
	}
	gooikeAddRe := regexp.MustCompile(`name="add\[\]" value="(\d+)"`)
	form := url.Values{"_wpnonce": {formNonce(t, page)}, "page": {"categories"}, "tab": {"scan"}, "do": {"scan_add"}}
	for _, k := range []string{"row_name[]", "row_provider[]", "row_category[]", "row_purpose[]", "row_duration[]"} {
		for _, m := range regexp.MustCompile(`name="`+regexp.QuoteMeta(k)+`" value="([^"]*)"`).FindAllStringSubmatch(page, -1) {
			form.Add(k, html.UnescapeString(m[1]))
		}
	}
	for _, m := range gooikeAddRe.FindAllStringSubmatch(page, -1) {
		form.Add("add[]", m[1])
	}
	if code, _, loc := post(t, c, base+"/wp-admin/gooike", form); code != http.StatusFound || !strings.Contains(loc, "gooike-msg") {
		t.Fatalf("scan_add: %d %s", code, loc)
	}
	if st := srv.gooikeLoad(); !gooikeDeclared(&st, "_clck") || !gooikeDeclared(&st, "_clsk") {
		t.Error("scanned cookies were not added to the declaration")
	}

	// Geo targeting answers from the CDN's country header.
	st = srv.gooikeLoad()
	st.Geo = true
	_ = srv.gooikeSave(context.Background(), st)
	for country, want := range map[string]string{"DE": "gdpr", "TR": "kvkk", "US": "ccpa", "JP": "none", "": "gdpr"} {
		req := httptest.NewRequest(http.MethodGet, base+"/wp-json/gooike/v1/geo", nil)
		req.RequestURI = ""
		if country != "" {
			req.Header.Set("CF-IPCountry", country)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var g struct{ Regulation string }
		_ = json.NewDecoder(resp.Body).Decode(&g)
		resp.Body.Close()
		if g.Regulation != want {
			t.Errorf("geo %q: %q, want %q", country, g.Regulation, want)
		}
	}
}
