package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"wpgo/internal/store"
)

func TestGfSniffMatches(t *testing.T) {
	pdf := []byte("%PDF-1.4\n1 0 obj")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF")
	for _, c := range []struct {
		ext  string
		data []byte
		want bool
	}{
		{"pdf", pdf, true}, {"pdf", []byte("MZ\x90\x00 an executable"), false},
		{"jpg", jpg, true}, {"jpg", pdf, false}, {"png", png, true}, {"png", jpg, false},
		{"txt", []byte("hello"), true}, {"txt", pdf, false}, {"docx", []byte("PK\x03\x04rest"), true},
		{"docx", []byte("plain text"), false}, {"dwg", []byte("anything"), true},
	} {
		if got := gfSniffMatches(c.ext, c.data); got != c.want {
			t.Errorf("gfSniffMatches(%q, %.8q) = %v, want %v", c.ext, c.data, got, c.want)
		}
	}
}

func TestGfSelectDataLists(t *testing.T) {
	tg := gfParseTag("select", "country data:countries include_blank", "")
	if len(tg.Values) < 200 || !gfContains(tg.Values, "Germany") || !gfContains(tg.Labels, "France") {
		t.Fatalf("countries: %d values", len(tg.Values))
	}
	st := gfParseTag("select", "state data:states:US", "")
	if !gfContains(st.Values, "California") {
		t.Fatalf("states: %v", st.Values)
	}
	// Explicit values win over data:.
	if own := gfParseTag("select", `x data:countries "A" "B"`, ""); len(own.Values) != 2 {
		t.Fatalf("own values: %v", own.Values)
	}
	r := &gfRender{}
	if out := r.selectHTML(tg); !strings.Contains(out, `<option value="Germany">Germany</option>`) {
		t.Fatalf("render: %.200s", out)
	}
}

func TestGfConditionalGroups(t *testing.T) {
	f := &gfForm{
		Form:       "[radio topic \"Sales\" \"Support\"]\n[group sales]\n[text* company]\n[group deep]\n[text vat]\n[/group]\n[/group]\n[text always]",
		Additional: "conditional: sales | topic | equals | Sales\nconditional: deep | company | not empty\n",
	}
	info := gfGroups(f.Form)
	if info.parent["deep"] != "sales" || !gfContains(info.fields["sales"], "vat") || gfContains(info.fields["deep"], "company") {
		t.Fatalf("groups: %+v", info)
	}
	vals := func(m map[string][]string) func(string) []string { return func(n string) []string { return m[n] } }
	h := f.hiddenFields(vals(map[string][]string{"topic": {"Support"}, "company": {"X"}}))
	if !h["company"] || !h["vat"] || h["always"] {
		t.Fatalf("support: %v", h)
	}
	h = f.hiddenFields(vals(map[string][]string{"topic": {"Sales"}}))
	if h["company"] || !h["vat"] { // deep needs a company
		t.Fatalf("sales, no company: %v", h)
	}
	h = f.hiddenFields(vals(map[string][]string{"topic": {"Sales"}, "company": {"X"}}))
	if len(h) != 0 {
		t.Fatalf("sales + company: %v", h)
	}
	if !gfCondHolds(gfCond{Op: "greater than", Value: "5"}, []string{"6"}) || gfCondHolds(gfCond{Op: "greater than", Value: "5"}, []string{"5"}) ||
		!gfCondHolds(gfCond{Op: "contains", Value: "AB"}, []string{"xaby"}) || !gfCondHolds(gfCond{Op: "is empty"}, nil) {
		t.Fatal("operators")
	}
	// Conditions that depend on each other never loop.
	loop := &gfForm{Form: "[group a][text x][/group][group b][text y][/group]", Additional: "conditional: a | y | not empty\nconditional: b | x | not empty"}
	_ = loop.hiddenFields(vals(map[string][]string{"x": {"1"}, "y": {"1"}}))
}

func TestGontactConditionalSubmission(t *testing.T) {
	base, _, srv, f, hidden, post := gontactEntryEnv(t)
	ctx := context.Background()
	form := "[text* your-name]\n[email* your-email]\n[radio topic default:1 \"Sales\" \"Support\"]\n[group extra]\n[text* company]\n[/group]\n[textarea your-message]\n[submit]"
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || jsonb_build_object('_form', $2::text, '_additional_settings', $3::text) WHERE id = $1`,
		f.ID, form, "conditional: extra | topic | equals | Sales"); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	_ = hidden
	// Support: the required company field is hidden, so it is neither required nor mailed.
	if _, out, raw := post("topic", "Support", "company", "ShouldBeDropped"); out["status"] != "mail_sent" {
		t.Fatalf("hidden group: %s", raw)
	}
	if n := entryCount(t, srv, "WHERE posted::text LIKE '%ShouldBeDropped%'"); n != 0 {
		t.Fatalf("hidden field stored: %d", n)
	}
	// Sales: shown, so required.
	if _, out, raw := post("topic", "Sales"); out["status"] != "validation_failed" {
		t.Fatalf("shown group: %s", raw)
	}
	if _, out, raw := post("topic", "Sales", "company", "Acme"); out["status"] != "mail_sent" {
		t.Fatalf("shown group, filled: %s", raw)
	}
	// The page carries the group wrapper and the rules for the browser.
	pg := &store.Post{Type: "page", Title: "Second", Slug: "second", Content: `[contact-form-7 id="` + itoa64(f.ID) + `" title="x"]`, Status: "publish", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, pg); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	_, page, _ := get(t, anonClient(), base+"/second/")
	if !strings.Contains(page, `data-wpcf7cf-group="extra"`) || !strings.Contains(page, `"op":"equals"`) {
		t.Fatalf("page:\n%s", page)
	}
}

func TestGontactWebhookAndHooks(t *testing.T) {
	_, _, srv, f, _, post := gontactEntryEnv(t)
	ctx := context.Background()
	var mu sync.Mutex
	var gotBody, gotSig string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 1<<16)
		n, _ := r.Body.Read(b)
		mu.Lock()
		gotBody, gotSig = string(b[:n]), r.Header.Get("X-Pursae-Signature")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()
	gooWebhookAllowPrivate.Store(true)
	defer gooWebhookAllowPrivate.Store(false)
	settings := "webhook_url: " + hook.URL + "\nwebhook_secret: s3cret\nwebhook_url: ftp://bad.example/x"
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || jsonb_build_object('_additional_settings', $2::text) WHERE id = $1`, f.ID, settings); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	if _, out, raw := post(); out["status"] != "mail_sent" {
		t.Fatalf("submit: %s", raw)
	}
	var payload map[string]any
	if err := srv.store.Pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE name = $1`, gontactWebhookJob).Scan(&payload); err != nil {
		t.Fatalf("no webhook job: %v", err)
	}
	if n := jobCount(t, srv, gontactWebhookJob); n != 1 { // the ftp:// URL is refused
		t.Fatalf("webhook jobs: %d", n)
	}
	if err := srv.gontactWebhookRun(ctx, payload); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(gotBody, `"your-email":"ann@example.org"`) || gotSig != gontactWebhookSign([]byte(gotBody), "s3cret") {
		t.Fatalf("delivery: sig=%q body=%s", gotSig, gotBody)
	}
	// A failing endpoint makes the job fail (and so retry).
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	if err := srv.gontactWebhookRun(ctx, map[string]any{"url": bad.URL, "body": "{}"}); err == nil {
		t.Fatal("500 did not fail the job")
	}
	// An address on the private network is refused for good (no retry).
	gooWebhookAllowPrivate.Store(false)
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer private.Close()
	if err := srv.gontactWebhookRun(ctx, map[string]any{"url": private.URL, "body": "{}"}); err != nil {
		t.Fatalf("private address: %v", err)
	}

	// Hooks: wpcf7_validate adds an error, wpcf7_before_send_mail aborts or skips the mail.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || jsonb_build_object('_additional_settings', '') WHERE id = $1`, f.ID); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	reg := srv.plugins.hooks
	id := reg.AddFilter("wpcf7_validate", 10, func(_ context.Context, v any, args ...any) any {
		return map[string]any{"your-subject": "No spam subjects."}
	})
	if _, out, raw := post(); out["status"] != "validation_failed" || !strings.Contains(raw, "No spam subjects.") {
		t.Fatalf("wpcf7_validate: %s", raw)
	}
	reg.Remove(id)
	mailsBefore := len(mailJobs(t, srv))
	id = reg.AddFilter("wpcf7_before_send_mail", 10, func(_ context.Context, v any, args ...any) any {
		return map[string]any{"abort": "Blocked by a plugin."}
	})
	if _, out, raw := post(); out["status"] != "aborted" || out["message"] != "Blocked by a plugin." || len(mailJobs(t, srv)) != mailsBefore {
		t.Fatalf("abort: %s", raw)
	}
	reg.Remove(id)
	id = reg.AddFilter("wpcf7_before_send_mail", 10, func(_ context.Context, v any, args ...any) any {
		m := v.(map[string]any)
		m["skip_mail"] = true
		return m
	})
	if _, out, raw := post(); out["status"] != "mail_sent" || len(mailJobs(t, srv)) != mailsBefore {
		t.Fatalf("skip_mail: %s", raw)
	}
	reg.Remove(id)
}

func jobCount(t *testing.T, srv *Server, name string) int {
	t.Helper()
	var n int
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE name = $1`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGontactRecaptchaWaitsForConsent(t *testing.T) {
	_, _, srv := gontactSetup(t, true)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"gontact_recaptcha_sitekey": "SITE", "gontact_recaptcha_secret": "SEC"}); err != nil {
		t.Fatal(err)
	}
	if out := srv.gontactAssetTags(nil); !strings.Contains(out, `<script src="https://www.google.com/recaptcha/api.js`) {
		t.Fatalf("without Pursae Cookie the script loads at once:\n%s", out)
	}
	if err := srv.settings.Set(ctx, map[string]any{gooikeSetting: true}); err != nil {
		t.Fatal(err)
	}
	if !srv.gooikeOn() {
		t.Skip("Pursae Cookie could not be switched on in this test")
	}
	out := srv.gontactAssetTags(nil)
	if strings.Contains(out, `<script src="https://www.google.com`) || !strings.Contains(out, `type="text/plain" data-gooike-category="marketing"`) ||
		!strings.Contains(out, `data-gooike-src="https://www.google.com/recaptcha/api.js`) {
		t.Fatalf("with Pursae Cookie the script is inert:\n%s", out)
	}
}
