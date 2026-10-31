package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeAPI is an httptest server standing in for every third-party API: gweOutboundTransport
// sends each (already host-checked) request to it, keeping the original host in X-Orig-Host.
type fakeAPI struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []fakeReq
	// handle answers a request: status and body.
	handle func(r fakeReq) (int, string)
}

type fakeReq struct {
	Method, Host, Path, Query string
	Header                    http.Header
	Body                      string
}

func newFakeAPI(t *testing.T, handle func(r fakeReq) (int, string)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{handle: handle}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fr := fakeReq{Method: r.Method, Host: r.Header.Get("X-Orig-Host"), Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(b)}
		f.mu.Lock()
		f.reqs = append(f.reqs, fr)
		f.mu.Unlock()
		code, body := f.handle(fr)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	old := gweOutboundTransport
	gweOutboundTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r2 := r.Clone(r.Context())
		r2.Header.Set("X-Orig-Host", r.URL.Host)
		r2.URL.Scheme = "http"
		r2.URL.Host = f.srv.Listener.Addr().String()
		r2.Host = ""
		return http.DefaultTransport.RoundTrip(r2)
	})
	t.Cleanup(func() {
		gweOutboundTransport = old
		f.srv.Close()
	})
	return f
}

func (f *fakeAPI) requests() []fakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeReq(nil), f.reqs...)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGWEOutboundHostChecks(t *testing.T) {
	cases := []struct {
		url   string
		allow func(string) bool
		ok    bool
	}{
		{"https://us12.api.mailchimp.com/3.0/lists", gweMailchimpHostRe.MatchString, true},
		{"http://us12.api.mailchimp.com/3.0/lists", gweMailchimpHostRe.MatchString, false},
		{"https://us12.api.mailchimp.com.evil.test/3.0/", gweMailchimpHostRe.MatchString, false},
		{"https://us1.api.mailchimp.com:8443/3.0/", gweMailchimpHostRe.MatchString, false},
		{"https://user@us1.api.mailchimp.com/", gweMailchimpHostRe.MatchString, false},
		{"https://acme.api-us1.com/admin/api.php", gweActiveCampaignHostRe.MatchString, true},
		{"https://acme.activehosted.com/admin/api.php", gweActiveCampaignHostRe.MatchString, true},
		{"https://127.0.0.1/admin/api.php", gweActiveCampaignHostRe.MatchString, false},
		{"https://hooks.slack.com/services/x", gweHosts("hooks.slack.com"), true},
		{"https://api.stripe.com/v1/", gweStripeHost, true},
		{"https://fonts.gstatic.com/s/x.woff2", gweHosts("fonts.googleapis.com", "fonts.gstatic.com"), true},
		{"https://example.com/x.woff2", gweHosts("fonts.googleapis.com", "fonts.gstatic.com"), false},
	}
	for _, tc := range cases {
		_, err := gweCheckOutboundURL(tc.url, tc.allow)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.url, err, tc.ok)
		}
	}
	// A crafted Mailchimp key cannot point the client elsewhere.
	for _, key := range []string{"abc-us1.evil.test", "abc-eu1", "abc", ""} {
		if _, err := newGWEMailchimp(context.Background(), key); err == nil {
			t.Errorf("mailchimp key %q accepted", key)
		}
	}
}

// testRecord builds a form record with an email and a name field.
func testRecord(settings map[string]any) *gweFormRecord {
	fields := []*gweFormField{{ID: "name", Type: "text", Title: "Name", Value: "Ada Lovelace"}, {ID: "email", Type: "email", Title: "Email", Value: "Ada@Example.test"}}
	r := &gweFormRecord{settings: settings, fields: fields, byID: map[string]*gweFormField{}, clientIP: "203.0.113.9", referrer: "https://site.test/contact/", siteURL: "https://site.test"}
	for _, f := range fields {
		r.byID[f.ID] = f
	}
	return r
}

func fieldsMap(pairs ...string) []any {
	var out []any
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, map[string]any{"remote_id": pairs[i], "local_id": pairs[i+1]})
	}
	return out
}

func TestGWEIntegrationsRunAgainstFakes(t *testing.T) {
	s := &Server{}
	hash := gweEmailHash("Ada@Example.test")
	f := newFakeAPI(t, func(r fakeReq) (int, string) {
		switch {
		case r.Host == "us7.api.mailchimp.com" && r.Method == "PUT":
			return 200, `{"list_id":"L1","email_address":"ada@example.test","status":"subscribed","tags":[{"id":1,"name":"old"}]}`
		case r.Host == "us7.api.mailchimp.com" && strings.HasSuffix(r.Path, "/tags"):
			return 204, ``
		case r.Host == "us7.api.mailchimp.com" && strings.HasSuffix(r.Path, "/lists"):
			return 200, `{"lists":[{"id":"L1","name":"Newsletter"},{"id":"L2","name":"Leads"}]}`
		case r.Host == "api.getdrip.com" && r.Path == "/v2/accounts":
			return 200, `{"accounts":[{"id":"9001","name":"Shop"}]}`
		case r.Host == "api.getdrip.com":
			return 200, `{"subscribers":[{}]}`
		case r.Host == "acme.api-us1.com" && strings.Contains(r.Query, "list_list"):
			return 200, `{"0":{"id":"3","name":"Main list"},"result_code":1}`
		case r.Host == "acme.api-us1.com" && strings.Contains(r.Query, "list_field_view"):
			return 200, `{"0":{"id":"7","title":"Company","type":"text","isrequired":"0"},"result_code":1}`
		case r.Host == "acme.api-us1.com":
			return 200, `{"result_code":1}`
		case r.Host == "api.getresponse.com" && r.Path == "/v3/campaigns":
			return 200, `[{"campaignId":"cA","name":"Campaign A"}]`
		case r.Host == "api.getresponse.com" && r.Path == "/v3/contacts":
			return 409, `{"message":"exists"}`
		case r.Host == "api.convertkit.com" && strings.HasPrefix(r.Path, "/v3/forms/") && r.Method == "GET":
			return 200, `{"forms":[{"id":11,"name":"Signup"}]}`
		case r.Host == "api.convertkit.com" && strings.HasSuffix(r.Path, "/subscribe"):
			return 200, `{"subscription":{}}`
		case r.Host == "api.mailerlite.com" && r.Path == "/api/v2/groups" && r.Method == "GET":
			return 200, `[{"id":5,"name":"Group 5"}]`
		case r.Host == "api.mailerlite.com":
			return 200, `{"id":1}`
		case r.Host == "hooks.slack.com":
			return 200, `ok`
		case r.Host == "discord.com":
			return 204, ``
		}
		return 404, `{}`
	})
	ctx := context.Background()
	// Mailchimp: upsert the member, then set the tags (old + new).
	err := s.gweMailchimpRun(ctx, testRecord(map[string]any{"mailchimp_api_key_source": "custom", "mailchimp_api_key": "k123-us7",
		"mailchimp_list": "L1", "mailchimp_tags": "vip, news", "mailchimp_double_opt_in": "", "mailchimp_groups": []any{"g1"},
		"mailchimp_fields_map": fieldsMap("email", "email", "FNAME", "name")}))
	if err != nil {
		t.Fatalf("mailchimp: %v", err)
	}
	reqs := f.requests()
	if len(reqs) != 2 || reqs[0].Method != "PUT" || reqs[0].Path != "/3.0/lists/L1/members/"+hash {
		t.Fatalf("mailchimp requests: %+v", reqs)
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:k123-us7")) {
		t.Errorf("mailchimp auth %q", got)
	}
	var put map[string]any
	_ = json.Unmarshal([]byte(reqs[0].Body), &put)
	if put["email_address"] != "Ada@Example.test" || put["status"] != "subscribed" || gweMap(put["merge_fields"])["FNAME"] != "Ada Lovelace" ||
		gweMap(put["interests"])["g1"] != true {
		t.Errorf("mailchimp member: %s", reqs[0].Body)
	}
	if !strings.Contains(reqs[1].Body, `"name":"old"`) || !strings.Contains(reqs[1].Body, `"name":"vip"`) {
		t.Errorf("mailchimp tags: %s", reqs[1].Body)
	}
	// Mailchimp panel shapes.
	out, err := s.gweMailchimpPanel(ctx, map[string]any{"mailchimp_action": "lists", "api_key": "k123-us7", "use_global_api_key": "custom"})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(out); string(b) != `{"lists":{"":"Select...","L1":"Newsletter","L2":"Leads"}}` {
		t.Errorf("mailchimp lists: %s", b)
	}

	// Drip
	if err := s.gweDripRun(ctx, testRecord(map[string]any{"drip_api_token_source": "custom", "drip_custom_api_token": "tok", "drip_account": "9001",
		"drip_fields_map": fieldsMap("email", "email"), "drip_custom_fields": "yes"})); err != nil {
		t.Fatalf("drip: %v", err)
	}
	last := f.requests()[len(f.requests())-1]
	if last.Path != "/v2/9001/subscribers/" || !strings.Contains(last.Body, `"email":"Ada@Example.test"`) || !strings.Contains(last.Body, `"name":"Ada Lovelace"`) {
		t.Errorf("drip create: %+v", last)
	}
	// ActiveCampaign: form-encoded contact_sync to the account's API host.
	if err := s.gweActiveCampaignRun(ctx, testRecord(map[string]any{"activecampaign_api_credentials_source": "custom", "activecampaign_api_key": "ak",
		"activecampaign_api_url": "https://acme.api-us1.com", "activecampaign_list": "3", "activecampaign_fields_map": fieldsMap("email", "email", "first_name", "name")})); err != nil {
		t.Fatalf("activecampaign: %v", err)
	}
	last = f.requests()[len(f.requests())-1]
	if form, _ := url.ParseQuery(last.Body); !strings.Contains(last.Query, "api_action=contact_sync") || form.Get("email") != "Ada@Example.test" || form.Get("p[3]") != "3" ||
		form.Get("first_name") != "Ada Lovelace" {
		t.Errorf("activecampaign sync: %+v", last)
	}
	acPanel, err := s.gweActiveCampaignPanel(ctx, map[string]any{"api_cred": "custom", "api_key": "ak", "api_url": "https://acme.api-us1.com"})
	if b, _ := json.Marshal(acPanel); err != nil || string(b) != `{"fields":[{"remote_id":"field[7,0]","remote_label":"Company","remote_required":false,"remote_type":"text"}],"lists":{"":"Select...","3":"Main list"}}` {
		t.Errorf("activecampaign panel: %s %v", b, err)
	}
	if _, err := s.gweActiveCampaignPanel(ctx, map[string]any{"api_cred": "custom", "api_key": "ak", "api_url": "https://intranet.local"}); err == nil {
		t.Errorf("activecampaign accepted a non-ActiveCampaign host")
	}
	// GetResponse: 409 (contact exists) counts as success.
	if err := s.gweGetResponseRun(ctx, testRecord(map[string]any{"getresponse_api_key_source": "custom", "getresponse_custom_api_key": "gk",
		"getresponse_list": "cA", "getresponse_fields_map": fieldsMap("email", "email", "name", "name")})); err != nil {
		t.Errorf("getresponse: %v", err)
	}
	// ConvertKit
	if err := s.gweConvertKitRun(ctx, testRecord(map[string]any{"convertkit_api_key_source": "custom", "convertkit_custom_api_key": "ck",
		"convertkit_form": "11", "convertkit_fields_map": fieldsMap("email", "email", "first_name", "name")})); err != nil {
		t.Errorf("convertkit: %v", err)
	}
	last = f.requests()[len(f.requests())-1]
	if last.Path != "/v3/forms/11/subscribe" || !strings.Contains(last.Query, "api_key=ck") || !strings.Contains(last.Body, `"first_name":"Ada Lovelace"`) {
		t.Errorf("convertkit subscribe: %+v", last)
	}
	// MailerLite
	if err := s.gweMailerLiteRun(ctx, testRecord(map[string]any{"mailerlite_api_key_source": "custom", "mailerlite_custom_api_key": "mk",
		"mailerlite_group": "5", "mailerlite_fields_map": fieldsMap("email", "email", "name", "name")})); err != nil {
		t.Errorf("mailerlite: %v", err)
	}
	last = f.requests()[len(f.requests())-1]
	if last.Path != "/api/v2/groups/5/subscribers" || last.Header.Get("X-Mailerlite-Apikey") != "mk" {
		t.Errorf("mailerlite subscribe: %+v", last)
	}
	// Slack and Discord
	if err := s.gweSlackRun(ctx, testRecord(map[string]any{"slack_webhook": "https://hooks.slack.com/services/T/B/X", "slack_add_fields": "yes"})); err != nil {
		t.Errorf("slack: %v", err)
	}
	last = f.requests()[len(f.requests())-1]
	if last.Host != "hooks.slack.com" || !strings.Contains(last.Body, `"title":"Name","value":"Ada Lovelace"`) {
		t.Errorf("slack: %+v", last)
	}
	if err := s.gweDiscordRun(ctx, testRecord(map[string]any{"discord_webhook": "https://discord.com/api/webhooks/1/abc", "discord_form_data": "yes"})); err != nil {
		t.Errorf("discord: %v", err)
	}
	n := len(f.requests())
	// Webhooks on other hosts are ignored, like upstream.
	if err := s.gweSlackRun(ctx, testRecord(map[string]any{"slack_webhook": "https://evil.test/services/"})); err != nil || len(f.requests()) != n {
		t.Errorf("slack sent to a foreign host")
	}
	// A failing service reports upstream's message.
	f.handle = func(fakeReq) (int, string) { return 401, `{"detail":"bad key"}` }
	if err := s.gweDripRun(ctx, testRecord(map[string]any{"drip_api_token_source": "custom", "drip_custom_api_token": "tok", "drip_account": "9001",
		"drip_fields_map": fieldsMap("email", "email")})); err == nil || err.Error() != "Rest Client Error: response code 401." {
		t.Errorf("drip failure: %v", err)
	}
}

func TestGWEStripeAmount(t *testing.T) {
	if gweStripeAmount("usd", 19.99) != 1999 || gweStripeAmount("JPY", 500.7) != 500 || gweStripeAmount("EUR", 0.1) != 10 {
		t.Errorf("stripe amounts")
	}
}

const fakeGoogleCSS = `/* cyrillic */
@font-face {
  font-family: 'Roboto';
  font-style: normal;
  font-weight: 400;
  font-display: swap;
  src: url(https://fonts.gstatic.com/s/roboto/v30/KFOmCnqEu92Fr1Mu5mxKOzY.woff2) format('woff2');
  unicode-range: U+0301, U+0400-045F;
}
/* latin */
@font-face {
  font-family: 'Roboto';
  font-style: normal;
  font-weight: 400;
  font-display: swap;
  src: url(https://fonts.gstatic.com/s/roboto/v30/KFOmCnqEu92Fr1Mu4mxK.woff2) format('woff2');
  unicode-range: U+0000-00FF;
}
/* latin */
@font-face {
  font-family: 'Roboto';
  font-style: normal;
  font-weight: 700;
  font-display: swap;
  src: url(https://fonts.gstatic.com/s/roboto/v30/KFOlCnqEu92Fr1MmWUlfBBc4.woff2) format('woff2');
  unicode-range: U+0000-00FF;
}
`

// fakeFontFetch serves fakeGoogleCSS and font bytes, recording the URLs asked for.
func fakeFontFetch(t *testing.T) *[]string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	old := gweFontFetch
	gweFontFetch = func(ctx context.Context, raw string) ([]byte, error) {
		mu.Lock()
		got = append(got, raw)
		mu.Unlock()
		if _, err := gweCheckOutboundURL(raw, gweHosts("fonts.googleapis.com", "fonts.gstatic.com")); err != nil {
			t.Errorf("font fetch to %s: %v", raw, err)
			return nil, err
		}
		if strings.HasPrefix(raw, "https://fonts.googleapis.com/") {
			return []byte(fakeGoogleCSS), nil
		}
		return []byte("wOF2-fake-" + raw), nil
	}
	t.Cleanup(func() { gweFontFetch = old })
	return &got
}

func TestGWEGoogleFontsLocal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	got := fakeFontFetch(t)
	dir := t.TempDir()
	s := &Server{}
	s.cfg.UploadsDir = dir
	if err := s.gweDownloadGoogleFont(context.Background(), "Roboto", "googlefonts"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 4 || !strings.Contains((*got)[0], "family=Roboto:100,100italic") || !strings.Contains((*got)[0], "display=swap") {
		t.Errorf("fetched %v", *got)
	}
	css, err := os.ReadFile(filepath.Join(dir, "web-builder", "google-fonts", "css", "roboto.css"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), "gstatic") || !strings.Contains(string(css), "url(../fonts/roboto-") || strings.Count(string(css), "font-display: swap") != 3 {
		t.Errorf("local css:\n%s", css)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "web-builder", "google-fonts", "fonts", "roboto-*.woff2"))
	if len(files) != 3 {
		t.Errorf("font files: %v", files)
	}
	// The preload is the regular latin face.
	pre := gweFontPreloadFile(string(css))
	if b, _ := os.ReadFile(filepath.Join(dir, "web-builder", "google-fonts", "fonts", pre)); !strings.Contains(string(b), "KFOmCnqEu92Fr1Mu4mxK.woff2") {
		t.Errorf("preload %q = %s", pre, b)
	}
	// Early access stylesheets get font-display added.
	if out := gweFontFaceRe.ReplaceAllStringFunc("@font-face { font-family: 'X'; src: url(a.woff2); }", func(face string) string {
		return strings.TrimSuffix(strings.TrimRight(face, " \n\t}"), ";") + ";\n  font-display: swap;\n}"
	}); !strings.Contains(out, "font-display: swap") {
		t.Errorf("font-display: %s", out)
	}
	if gweGoogleFontType("Roboto") != "googlefonts" || gweGoogleFontType("Arial") != "" || gweGoogleFontType("Alef Hebrew") != "earlyaccess" {
		t.Errorf("font types")
	}
	if gweFontKey("Roboto Slab") != "robotoslab" {
		t.Errorf("font key")
	}
}

// TestGWEIntegrationsServer runs the form actions, the settings screen, the Stripe and PayPal
// buttons and local Google Fonts through a real server (database needed).
func TestGWEIntegrationsServer(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Shop"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	fonts := fakeFontFetch(t)
	var stripeBody string
	var mu sync.Mutex
	f := newFakeAPI(t, func(r fakeReq) (int, string) {
		switch {
		case r.Host == "us7.api.mailchimp.com" && r.Method == "PUT":
			if strings.Contains(r.Body, "fail@example.test") {
				return 400, `{"detail":"Member In Compliance State"}`
			}
			return 200, `{"list_id":"L1","email_address":"ada@example.test","tags":[]}`
		case r.Host == "us7.api.mailchimp.com" && strings.HasSuffix(r.Path, "/lists"):
			return 200, `{"lists":[{"id":"L1","name":"Newsletter"}]}`
		case r.Host == "hooks.slack.com":
			return 200, "ok"
		case r.Host == "api.stripe.com" && r.Path == "/v1/checkout/sessions":
			mu.Lock()
			stripeBody = r.Body
			mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer sk_test_fake_123456" {
				return 401, `{"error":{"message":"Invalid API Key provided"}}`
			}
			return 200, `{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/cs_test_1","object":"checkout.session"}`
		case r.Host == "api.stripe.com" && r.Path == "/v1/tax_rates":
			return 200, `{"data":[{"id":"txr_1","inclusive":false,"display_name":"VAT","description":"EU"}]}`
		}
		return 404, `{}`
	})
	ctx := context.Background()
	pool := getPool(t)

	// --- settings screen: keys are saved, never printed back -------------------------------------
	_, body, _ := get(t, c, base+"/wp-admin/web-builder?tab=settings")
	for _, want := range []string{`id="integrations"`, `name="gwe_mailchimp_api_key"`, `name="gwe_stripe_test_secret_key"`, `name="gwe_local_google_fonts"`, "Validate API Key"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings screen misses %q", want)
		}
	}
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "action": {"save"}, "gwe_mailchimp_api_key": {"secretkey9876-us7"},
		"gwe_stripe_test_secret_key": {"sk_test_fake_123456"}, "gwe_local_google_fonts": {"1"}, "gwe_recaptcha_secret_key": {"recaptcha-secret-4321"}}
	if code, _, loc := post(t, c, base+"/wp-admin/web-builder?tab=settings", form); code != http.StatusFound || !strings.Contains(loc, "message=") {
		t.Fatalf("save settings: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/web-builder?tab=settings")
	if strings.Contains(body, "secretkey9876") || strings.Contains(body, "sk_test_fake_123456") || strings.Contains(body, "recaptcha-secret") || !strings.Contains(body, "••••••••••••-us7") {
		t.Errorf("settings screen echoes a key")
	}
	// Saving the masked value keeps the key; the validate button checks it against the API.
	mask := regexp.MustCompile(`name="gwe_mailchimp_api_key" type="text" class="regular-text code" value="([^"]*)"`).FindStringSubmatch(body)
	if mask == nil {
		t.Fatal("no masked field")
	}
	form.Set("_wpnonce", formNonce(t, body))
	form.Set("gwe_mailchimp_api_key", mask[1])
	form.Set("gwe_stripe_test_secret_key", gweMaskSecret("sk_test_fake_123456"))
	form.Set("gwe_recaptcha_secret_key", gweMaskSecret("recaptcha-secret-4321"))
	post(t, c, base+"/wp-admin/web-builder?tab=settings", form)
	var stored string
	_ = pool.QueryRow(ctx, `SELECT value #>> '{}' FROM settings WHERE key = 'gwe_mailchimp_api_key'`).Scan(&stored)
	if stored != "secretkey9876-us7" {
		t.Errorf("stored mailchimp key = %q", stored)
	}
	_ = pool.QueryRow(ctx, `SELECT value #>> '{}' FROM settings WHERE key = 'gwe_recaptcha_secret_key'`).Scan(&stored)
	if stored != "recaptcha-secret-4321" {
		t.Errorf("stored reCAPTCHA secret = %q", stored)
	}
	vn := regexp.MustCompile(`fd.append\('_nonce', '([^']+)'\)`).FindStringSubmatch(body)
	if vn == nil {
		t.Fatal("no validate nonce")
	}
	_, vbody, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"gwe_integration_validate"}, "_nonce": {vn[1]}, "service": {"mailchimp"},
		"gwe_mailchimp_api_key": {mask[1]}})
	if !strings.Contains(vbody, `"success":true`) {
		t.Errorf("validate: %s", vbody)
	}

	// --- a page with a form, a Stripe and a PayPal button and a Google font --------------------------
	elements := `[{"id":"c1","elType":"container","isInner":false,"settings":{},"elements":[
		{"id":"h1","elType":"widget","widgetType":"heading","settings":{"title":"Hi","typography_typography":"custom","typography_font_family":"Roboto"},"elements":[]},
		{"id":"f1","elType":"widget","widgetType":"form","settings":{"form_name":"Lead","submit_actions":["save-to-database","mailchimp","slack"],
			"mailchimp_api_key_source":"default","mailchimp_list":"L1","mailchimp_fields_map":[{"remote_id":"email","local_id":"email"}],
			"slack_webhook":"https://hooks.slack.com/services/T/B/C",
			"form_fields":[{"_id":"a1","custom_id":"name","field_type":"text","field_label":"Name"},
				{"_id":"a2","custom_id":"email","field_type":"email","field_label":"Email","required":"true"}]},"elements":[]},
		{"id":"s1","elType":"widget","widgetType":"stripe-button","settings":{"product_name":"Mug","stripe_product_price":"12.5","stripe_currency":"EUR",
			"stripe_quantity":2,"sandbox_mode":"yes","shipping_amount":"3","stripe_test_env_tax_rates_list":"[\"txr_1\",\"exclusive\"]"},"elements":[]},
		{"id":"p1","elType":"widget","widgetType":"paypal-button","settings":{"email":"shop@example.test","product_name":"Mug","product_price":"9"},"elements":[]}
	]}]`
	var compact strings.Builder
	for _, line := range strings.Split(elements, "\n") {
		compact.WriteString(strings.TrimSpace(line))
	}
	meta, _ := json.Marshal(map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": "wp-page", "_elementor_data": compact.String()})
	var pageID string
	if err := pool.QueryRow(ctx, `INSERT INTO posts (type, author_id, title, slug, status, published_at, meta) VALUES ('page', 1, 'Shop', 'shop', 'publish', now(), $1::jsonb)
		RETURNING id::text`, string(meta)).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(base + "/?page_id=" + pageID)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(pb)
	if !strings.Contains(page, "elementor-stripe-form") || !strings.Contains(page, "elementor-paypal-legacy") {
		t.Fatalf("page misses the payment buttons: %.500s", page)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' https://www.paypal.com") {
		t.Errorf("CSP without PayPal: %s", csp)
	}
	if !strings.Contains(page, "/wp-content/uploads/web-builder/google-fonts/css/roboto.css?ver=") || !regexp.MustCompile(`<link rel="preload" href="[^"]+/google-fonts/fonts/roboto-[0-9a-f]{8}\.woff2" as="font" type="font/woff2" crossorigin>`).MatchString(page) {
		t.Errorf("page misses the local Google font")
	}
	if strings.Contains(page, "fonts.googleapis.com") || strings.Contains(page, "fonts.gstatic.com") {
		t.Errorf("page links Google")
	}
	if len(*fonts) == 0 {
		t.Errorf("fonts not downloaded")
	}
	// The local stylesheet is served.
	if code, css, _ := get(t, c, base+"/wp-content/uploads/web-builder/google-fonts/css/roboto.css"); code != 200 || !strings.Contains(css, "font-display: swap") {
		t.Errorf("local css: %d", code)
	}

	// --- form submission: saved first, then the actions, each logged -----------------------------
	send := func(email string) map[string]any {
		resp, err := http.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_pro_forms_send_form"}, "post_id": {pageID}, "form_id": {"f1"},
			"referrer": {base + "/shop/"}, "form_fields[name]": {"Ada"}, "form_fields[email]": {email}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		b, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(b, &out) != nil {
			t.Fatalf("form reply: %s", b)
		}
		return out
	}
	if out := send("ada@example.test"); out["success"] != true {
		t.Errorf("submission failed: %v", out)
	}
	var actions string
	if err := pool.QueryRow(ctx, `SELECT actions::text FROM gwe_form_submissions WHERE element_id = 'f1' ORDER BY id DESC LIMIT 1`).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(actions, `"name": "mailchimp"`) || !strings.Contains(actions, `"name": "slack"`) || strings.Contains(actions, "failed") {
		t.Errorf("actions log: %s", actions)
	}
	if out := send("fail@example.test"); out["success"] != false {
		t.Errorf("failing action should fail the submission: %v", out)
	}
	_ = pool.QueryRow(ctx, `SELECT actions::text FROM gwe_form_submissions WHERE element_id = 'f1' ORDER BY id DESC LIMIT 1`).Scan(&actions)
	if !strings.Contains(actions, `"status": "failed"`) || !strings.Contains(actions, "HTTP 400 - Member In Compliance State") {
		t.Errorf("failed action not logged: %s", actions)
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM gwe_form_submissions WHERE element_id = 'f1'`).Scan(&count)
	if count != 2 {
		t.Errorf("submissions stored: %d", count)
	}

	// --- Stripe: a Checkout session on the test key ----------------------------------------------
	resp, err = http.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"submit_stripe_form"}, "data[action]": {"submit_stripe_form"},
		"data[postId]": {pageID}, "data[widgetId]": {"s1"}, "data[pageUrl]": {base + "/shop/"}, "data[nonce]": {""}})
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var sr struct {
		Response struct{ Code int } `json:"response"`
		Body     string             `json:"body"`
	}
	if json.Unmarshal(sb, &sr) != nil || sr.Response.Code != 200 || !strings.Contains(sr.Body, `"url":"https://checkout.stripe.com/c/pay/cs_test_1"`) {
		t.Errorf("stripe reply: %s", sb)
	}
	mu.Lock()
	sent, _ := url.ParseQuery(stripeBody)
	mu.Unlock()
	for k, v := range map[string]string{"mode": "payment", "line_items[0][price_data][unit_amount]": "1250", "line_items[0][price_data][currency]": "EUR",
		"line_items[0][quantity]": "2", "line_items[0][price_data][product_data][name]": "Mug", "cancel_url": base + "/shop/", "success_url": base + "/shop/",
		"shipping_options[0][shipping_rate_data][fixed_amount][amount]": "300", "line_items[0][tax_rates][0]": "txr_1",
		"line_items[0][price_data][tax_behavior]": "exclusive"} {
		if sent.Get(k) != v {
			t.Errorf("stripe %s = %q, want %q", k, sent.Get(k), v)
		}
	}
	// A foreign page URL is not used as the return URL.
	http.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"submit_stripe_form"}, "data[postId]": {pageID}, "data[widgetId]": {"s1"}, "data[pageUrl]": {"https://evil.test/"}})
	mu.Lock()
	sent, _ = url.ParseQuery(stripeBody)
	mu.Unlock()
	if strings.Contains(sent.Get("cancel_url"), "evil") {
		t.Errorf("stripe cancel_url %q", sent.Get("cancel_url"))
	}

	// --- editor: panel data, tax rates, alerts, local fonts --------------------------------------------
	_, ebody, _ := get(t, c, base+"/wp-admin/post?post="+pageID+"&action=web-builder")
	if !strings.Contains(ebody, `id="gwe-local-google-fonts-js"`) {
		t.Errorf("editor misses the local fonts script")
	}
	m := regexp.MustCompile(`"ajax":\{"url":"[^"]*","nonce":"([^"]+)"`).FindStringSubmatch(ebody)
	if m == nil {
		m = regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(ebody)
	}
	if m == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	ajax := func(actions string) map[string]any {
		resp, err := c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {m[1]}, "editor_post_id": {pageID},
			"initial_document_id": {pageID}, "actions": {actions}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		r, _ := gweMap(gweMap(out["data"])["responses"])["a"].(map[string]any)
		if r == nil || r["success"] != true {
			t.Fatalf("ajax %s: %s", actions, b)
		}
		return r
	}
	r := ajax(`{"a":{"action":"pro_forms_panel_action_data","data":{"service":"mailchimp","mailchimp_action":"lists","api_key":"","use_global_api_key":"default","editor_post_id":` + pageID + `}}}`)
	if b, _ := json.Marshal(r["data"]); string(b) != `{"lists":{"":"Select...","L1":"Newsletter"}}` {
		t.Errorf("panel lists: %s", b)
	}
	r = ajax(`{"a":{"action":"pro_get_stripe_tax_rates","data":{"editor_post_id":` + pageID + `}}}`)
	if b, _ := json.Marshal(r["data"]); !strings.Contains(string(b), `"test_api_key":{"":"None","[\"txr_1\",\"exclusive\"]":"VAT - EU"}`) {
		t.Errorf("tax rates: %s", b)
	}
	r = ajax(`{"a":{"action":"enqueue_google_fonts","data":{"font_name":"Roboto"}}}`)
	if d := gweMap(r["data"]); !strings.Contains(gweStr(d["url"]), "/google-fonts/css/roboto.css") || !strings.Contains(gweStr(d["preload"]), ".woff2") {
		t.Errorf("enqueue_google_fonts: %v", r["data"])
	}
	r = ajax(`{"a":{"action":"get_widgets_config","data":{"exclude":{}}}}`)
	wc, _ := json.Marshal(r["data"])
	if !strings.Contains(string(wc), "You are using MailChimp API Key set in the") || !strings.Contains(string(wc), "Set your Drip API Token in the") ||
		strings.Contains(string(wc), "page=elementor-settings") || !strings.Contains(string(wc), "web-builder?tab=settings#integrations") {
		t.Errorf("widgets config alerts not patched")
	}
	for _, req := range f.requests() {
		if req.Host != "us7.api.mailchimp.com" && req.Host != "hooks.slack.com" && req.Host != "api.stripe.com" {
			t.Errorf("unexpected outbound host %s", req.Host)
		}
	}
}
