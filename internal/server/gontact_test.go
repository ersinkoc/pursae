package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/mail"
	"wpgo/internal/store"
)

// gontactLink finds the Activate or Deactivate link of Gontact Form on the Plugins screen.
func gontactLink(t *testing.T, c *http.Client, base, action string) string {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=gontact-form[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for Pursae Contact Form on the Plugins screen", action)
	}
	return html.UnescapeString(m[1])
}

// gontactSetup installs the site, logs in and (when on) activates Gontact Form.
func gontactSetup(t *testing.T, on bool) (string, *http.Client, *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	if on {
		code, _, loc := get(t, c, ts.URL+gontactLink(t, c, ts.URL, "activate"))
		if code != http.StatusFound || !strings.Contains(loc, "activate=true") {
			t.Fatalf("activate Pursae Contact Form: %d %s", code, loc)
		}
	}
	return ts.URL, c, srv
}

// gontactPage publishes a page holding content and returns its URL.
func gontactPage(t *testing.T, srv *Server, content string) (*store.Post, string) {
	t.Helper()
	ctx := context.Background()
	p := &store.Post{Type: "page", Title: "Contact", Slug: "contact", Content: content, Status: "publish", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	return p, srv.links(ctx).Permalink(p)
}

func anonClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// hiddenFields reads the hidden inputs of the first form on a page.
func hiddenFields(t *testing.T, body string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)" />`).FindAllStringSubmatch(body, -1) {
		if _, ok := out[m[1]]; !ok {
			out[m[1]] = html.UnescapeString(m[2])
		}
	}
	if out["_wpcf7"] == "" || out["_wpcf7_unit_tag"] == "" {
		t.Fatalf("no CF7 hidden fields in the page:\n%s", body)
	}
	return out
}

type upload struct {
	field, name string
	data        []byte
}

func multipartPost(t *testing.T, c *http.Client, u string, fields map[string]string, files []upload, ua string) (int, map[string]any, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for _, f := range files {
		fw, _ := w.CreateFormFile(f.field, f.name)
		_, _ = fw.Write(f.data)
	}
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, u, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, string(b)
}

const testUA = "Mozilla/5.0 (Pursae Contact Form test)"

// mailJobs returns the payloads of the queued wp_mail jobs.
func mailJobs(t *testing.T, srv *Server) []map[string]any {
	t.Helper()
	rows, err := srv.store.Pool.Query(context.Background(), `SELECT payload FROM jobs WHERE name = 'wp_mail' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw []byte
		_ = rows.Scan(&raw)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		out = append(out, m)
	}
	return out
}

func TestGontactOffByDefault(t *testing.T) {
	base, c, srv := gontactSetup(t, false)
	if srv.gontactOn() {
		t.Fatal("Pursae Contact Form is on after a fresh install")
	}
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if row := regexp.MustCompile(`(?s)<tr class="(\w+)" data-slug="gontact-form"`).FindStringSubmatch(body); row == nil || row[1] != "inactive" {
		t.Fatalf("Pursae Contact Form is not listed as an inactive plugin: %v", row)
	}
	if strings.Contains(body, "toplevel_page_wpcf7") {
		t.Fatal("the Contact menu is shown while the plugin is off")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/admin?page=wpcf7"); code != http.StatusForbidden {
		t.Fatalf("Contact Forms screen while off: %d", code)
	}
	if code, _, _ := get(t, c, base+gontactAssets+"/styles.css"); code != http.StatusNotFound {
		t.Fatalf("assets while off: %d", code)
	}
	code, out, _ := multipartPost(t, anonClient(), base+"/wp-json/contact-form-7/v1/contact-forms/1/feedback", map[string]string{"_wpcf7_unit_tag": "wpcf7-f1-o1"}, nil, testUA)
	if code != http.StatusNotFound || out["code"] != "rest_no_route" {
		t.Fatalf("feedback route while off: %d %v", code, out)
	}
	_, idx := restGet(t, c, base, "/wp-json/")
	if strings.Contains(idx, "contact-form-7") {
		t.Fatal("the REST index lists contact-form-7/v1 while the plugin is off")
	}
	_, link := gontactPage(t, srv, `<!-- wp:shortcode -->[contact-form-7 id="1" title="x"]<!-- /wp:shortcode -->`)
	_, page, _ := get(t, anonClient(), link)
	if !strings.Contains(page, `[contact-form-7 id="1" title="x"]`) || strings.Contains(page, "wpcf7") {
		t.Fatal("the shortcode is expanded while the plugin is off")
	}
}

func TestGontactSubmitFlow(t *testing.T) {
	base, c, srv := gontactSetup(t, true)
	ctx := context.Background()

	// The sample form exists and the admin screens work.
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=wpcf7")
	if !strings.Contains(body, "Contact form 1") || !strings.Contains(body, "toplevel_page_wpcf7") {
		t.Fatal("Contact Forms list does not show Contact form 1 or the Contact menu")
	}
	sc := regexp.MustCompile(`value="(\[contact-form-7 id=&#34;[0-9a-f]{7}&#34; title=&#34;Contact form 1&#34;\])"`).FindStringSubmatch(body)
	if sc == nil {
		t.Fatalf("no hash shortcode in the list:\n%s", body)
	}
	shortcode := html.UnescapeString(sc[1])
	forms, _, _ := srv.gontactForms(ctx, "", "", "", 1, -1)
	if len(forms) != 1 {
		t.Fatalf("forms: %d", len(forms))
	}
	f := forms[0]
	if code, body, _ := get(t, c, base+"/wp-admin/admin?page=wpcf7&post="+itoa64(f.ID)+"&action=edit"); code != http.StatusOK ||
		!strings.Contains(body, `name="wpcf7-mail[recipient]"`) || !strings.Contains(body, "[your-email]") || !strings.Contains(body, `id="tag-generator-list"`) {
		t.Fatalf("editor: %d", code)
	}

	p, link := gontactPage(t, srv, "<!-- wp:paragraph --><p>Write to us.</p><!-- /wp:paragraph -->\n\n<!-- wp:contact-form-7/contact-form-selector {\"id\":"+itoa64(f.ID)+"} --><div class=\"wp-block-contact-form-7-contact-form-selector\">"+shortcode+"</div><!-- /wp:contact-form-7/contact-form-selector -->")
	anon := anonClient()
	_, page, _ := get(t, anon, link)
	for _, want := range []string{`<div class="wpcf7 no-js" id="wpcf7-f` + itoa64(f.ID) + `-p` + itoa64(p.ID) + `-o1"`, `class="wpcf7-form init"`, `data-status="init"`,
		`<span class="wpcf7-form-control-wrap" data-name="your-name"><input size="40" maxlength="400" class="wpcf7-form-control wpcf7-text wpcf7-validates-as-required" autocomplete="name" aria-required="true" aria-invalid="false" value="" type="text" name="your-name" /></span>`,
		`class="wpcf7-form-control wpcf7-email wpcf7-validates-as-required wpcf7-text wpcf7-validates-as-email"`,
		`<input class="wpcf7-form-control wpcf7-submit has-spinner" type="submit" value="Submit" />`, `contact-form-7-css`, `"namespace":"contact-form-7/v1"`,
		`<div class="wpcf7-response-output" aria-hidden="true"></div>`, `<p><label> Your name<br />`} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q:\n%s", want, page)
		}
	}
	hidden := hiddenFields(t, page)
	feedback := base + "/wp-json/contact-form-7/v1/contact-forms/" + itoa64(f.ID) + "/feedback"

	// Wrong payload type and missing unit tag.
	resp, err := anon.PostForm(feedback, url.Values{"_wpcf7_unit_tag": {hidden["_wpcf7_unit_tag"]}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("urlencoded feedback: %d", resp.StatusCode)
	}
	if code, out, _ := multipartPost(t, anon, feedback, map[string]string{}, nil, testUA); code != http.StatusBadRequest || out["code"] != "wpcf7_unit_tag_not_found" {
		t.Fatalf("no unit tag: %d %v", code, out)
	}

	// Invalid input.
	fields := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range hidden {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	code, out, raw := multipartPost(t, anon, feedback, fields("your-name", "", "your-email", "not-an-email", "your-subject", "Hi"), nil, testUA)
	if code != http.StatusOK || out["status"] != "validation_failed" || out["message"] != "One or more fields have an error. Please check and try again." {
		t.Fatalf("invalid submission: %d %s", code, raw)
	}
	inv, _ := out["invalid_fields"].([]any)
	if len(inv) != 2 || out["into"] != "#"+hidden["_wpcf7_unit_tag"] || out["posted_data_hash"] != "" {
		t.Fatalf("invalid fields: %s", raw)
	}
	byField := map[string]string{}
	for _, x := range inv {
		m := x.(map[string]any)
		byField[m["field"].(string)] = m["message"].(string)
		if m["error_id"] != hidden["_wpcf7_unit_tag"]+"-ve-"+m["field"].(string) {
			t.Fatalf("error_id: %v", m)
		}
	}
	if byField["your-name"] != "Please fill out this field." || byField["your-email"] != "Please enter an email address." {
		t.Fatalf("messages: %v", byField)
	}
	if n := len(mailJobs(t, srv)); n != 0 {
		t.Fatalf("mail queued for an invalid submission: %d", n)
	}

	// Spam: a missing user agent.
	if _, out, raw := multipartPost(t, anon, feedback, fields("your-name", "Ann", "your-email", "ann@example.org", "your-subject", "Hi"), nil, "x"); out["status"] != "spam" {
		t.Fatalf("spam check: %s", raw)
	}

	// Valid input: mail queued with CF7's default template.
	code, out, raw = multipartPost(t, anon, feedback, fields("your-name", "Ann Example", "your-email", "ann@example.org", "your-subject", "Hello there", "your-message", "Line one\nLine two"), nil, testUA)
	if code != http.StatusOK || out["status"] != "mail_sent" || out["message"] != "Thank you for your message. It has been sent." || out["posted_data_hash"] == "" {
		t.Fatalf("valid submission: %s", raw)
	}
	jobs := mailJobs(t, srv)
	if len(jobs) != 1 {
		t.Fatalf("queued mails: %d", len(jobs))
	}
	m := jobs[0]
	to, _ := m["To"].([]any)
	if len(to) != 1 || to[0] != "<admin@example.test>" || m["Subject"] != `Toggle Site "Hello there"` || m["ReplyTo"] != "<ann@example.org>" {
		t.Fatalf("mail: %v", m)
	}
	body2, _ := m["Body"].(string)
	if !strings.Contains(body2, "From: Ann Example ann@example.org") || !strings.Contains(body2, "Line one\nLine two") || !strings.Contains(body2, "(Toggle Site "+base+"/)") {
		t.Fatalf("mail body:\n%s", body2)
	}
	if from, _ := m["From"].(string); !strings.Contains(from, "admin@example.test") {
		t.Fatalf("From: %q", from)
	}
	// The mail really renders (attachments-free path of the core mailer).
	if _, err := srv.mailer().Build(gontactMsg(t, m)); err != nil {
		t.Fatal(err)
	}

	// Without JavaScript the form posts back to the page and shows the errors inline.
	vals := url.Values{}
	for k, v := range fields("your-name", "", "your-email", "ann@example.org", "your-subject", "") {
		vals.Set(k, v)
	}
	req, _ := http.NewRequest(http.MethodPost, link, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", testUA)
	r2, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	fallback := string(b)
	for _, want := range []string{`class="wpcf7-form invalid"`, `<span class="wpcf7-not-valid-tip" aria-hidden="true">Please fill out this field.</span>`,
		`aria-invalid="true" aria-describedby="` + hidden["_wpcf7_unit_tag"] + `-ve-your-name"`, `value="ann@example.org"`,
		`One or more fields have an error. Please check and try again.</div>`} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("non-JS submission page lacks %q (status %d):\n%s", want, r2.StatusCode, fallback)
		}
	}

	// Schema and refill endpoints.
	_, schema, _ := get(t, anon, base+"/wp-json/contact-form-7/v1/contact-forms/"+itoa64(f.ID)+"/feedback/schema")
	if !strings.Contains(schema, `"rule":"required","field":"your-name"`) && !strings.Contains(schema, `"field":"your-name","rule":"required"`) {
		t.Fatalf("schema: %s", schema)
	}
}

func gontactMsg(t *testing.T, payload map[string]any) (msg mail.Message) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestGontactEditorUploadQuizAcceptance(t *testing.T) {
	base, c, srv := gontactSetup(t, true)
	ctx := context.Background()

	// Add a form through Contact › Add Contact Form.
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=wpcf7-new")
	nonce := formNonce(t, body)
	form := `<label> Name [text* your-name] </label>
<label> CV [file* your-file limit:1kb filetypes:txt|pdf] </label>
[quiz your-quiz "1+2=?|3"]
[acceptance your-consent] I agree. [/acceptance]
[checkbox your-topics use_label_element "Sales|sales@example.org" "Support|help@example.org"]
[submit "Send"]`
	vals := url.Values{"_wpnonce": {nonce}, "action": {"save"}, "post_ID": {"-1"}, "post_title": {"Job application"}, "wpcf7-form": {form},
		"wpcf7-mail[recipient]": {"hr@example.org"}, "wpcf7-mail[sender]": {"[_site_title] <wordpress@example.test>"}, "wpcf7-mail[subject]": {"Application from [your-name]"},
		"wpcf7-mail[body]":        {"Name: [your-name]\nTopics: [your-topics] / [_raw_your-topics]\nConsent: [your-consent]\nFile: [your-file]\nForm: [_contact_form_title]"},
		"wpcf7-mail[attachments]": {"[your-file]"}, "wpcf7-mail[additional_headers]": {"Cc: boss@example.org"},
		"wpcf7-mail-2[active]": {"1"}, "wpcf7-mail-2[recipient]": {"[your-topics]"}, "wpcf7-mail-2[sender]": {"<wordpress@example.test>"},
		"wpcf7-mail-2[subject]": {"Copy"}, "wpcf7-mail-2[body]": {"Thanks [your-name]"},
		"wpcf7-additional-settings": {"acceptance_as_validation: off"}}
	code, _, loc := post(t, c, base+"/wp-admin/admin?page=wpcf7-new", vals)
	if code != http.StatusFound || !strings.Contains(loc, "message=created") {
		t.Fatalf("create form: %d %s", code, loc)
	}
	id := regexp.MustCompile(`post=(\d+)`).FindStringSubmatch(loc)[1]
	_, body, _ = get(t, c, base+loc)
	if !strings.Contains(body, "Contact form created.") || !strings.Contains(body, "Job application") {
		t.Fatal("editor after create")
	}
	var fid int64
	for _, ch := range id {
		fid = fid*10 + int64(ch-'0')
	}
	f, err := srv.gontactForm(ctx, fid)
	if err != nil || !f.Mail2.Active || f.Mail.Attachments != "[your-file]" {
		t.Fatalf("saved form: %v %+v", err, f)
	}

	_, link := gontactPage(t, srv, `[contact-form-7 id="`+f.ShortHash()+`"]`)
	anon := anonClient()
	_, page, _ := get(t, anon, link)
	if !strings.Contains(page, `enctype="multipart/form-data"`) || !strings.Contains(page, `accept=".txt,.pdf"`) || !strings.Contains(page, `<span class="wpcf7-quiz-label">1+2=?</span>`) {
		t.Fatalf("form markup:\n%s", page)
	}
	hidden := hiddenFields(t, page)
	feedback := base + "/wp-json/contact-form-7/v1/contact-forms/" + id + "/feedback"
	fields := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range hidden {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	// Wrong quiz answer, file type and missing consent.
	_, out, raw := multipartPost(t, anon, feedback, fields("your-name", "Bo", "your-quiz", "4"), []upload{{"your-file", "cv.exe", []byte("MZ")}}, testUA)
	if out["status"] != "validation_failed" {
		t.Fatalf("invalid upload: %s", raw)
	}
	if !strings.Contains(raw, "The answer to the quiz is incorrect.") || !strings.Contains(raw, "You are not allowed to upload files of this type.") {
		t.Fatalf("messages: %s", raw)
	}
	// Too large a file.
	_, _, raw = multipartPost(t, anon, feedback, fields("your-name", "Bo", "your-quiz", "3", "your-consent", "1"), []upload{{"your-file", "cv.txt", bytes.Repeat([]byte("a"), 2048)}}, testUA)
	if !strings.Contains(raw, "The uploaded file is too large.") {
		t.Fatalf("size limit: %s", raw)
	}
	// Valid, but the acceptance box unticked.
	_, out, raw = multipartPost(t, anon, feedback, fields("your-name", "Bo", "your-quiz", " 3 "), []upload{{"your-file", "cv.txt", []byte("hello")}}, testUA)
	if out["status"] != "acceptance_missing" || out["message"] != "You must accept the terms and conditions before sending your message." {
		t.Fatalf("acceptance: %s", raw)
	}
	// Everything right.
	_, out, raw = multipartPost(t, anon, feedback, fields("your-name", "Bo", "your-quiz", "3", "your-consent", "1", "your-topics[]", "Support"), []upload{{"your-file", "my cv.txt", []byte("hello world")}}, testUA)
	if out["status"] != "mail_sent" {
		t.Fatalf("valid upload: %s", raw)
	}
	if q, ok := out["quiz"].(map[string]any); !ok || q["your-quiz"] == nil {
		t.Fatalf("no quiz refill in the response: %s", raw)
	}
	jobs := mailJobs(t, srv)
	if len(jobs) != 2 {
		t.Fatalf("queued mails: %d", len(jobs))
	}
	msg := gontactMsg(t, jobs[0])
	if msg.Subject != "Application from Bo" || len(msg.Cc) != 1 || len(msg.Attachments) != 1 || string(msg.Attachments[0].Data) != "hello world" || msg.Attachments[0].Name != "my-cv.txt" {
		t.Fatalf("mail: %+v", msg)
	}
	for _, want := range []string{"Topics: help@example.org / Support", "Consent: Consented", "File: my-cv.txt", "Form: Job application"} {
		if !strings.Contains(msg.Body, want) {
			t.Fatalf("mail body lacks %q:\n%s", want, msg.Body)
		}
	}
	raw2, err := srv.mailer().Build(msg)
	if err != nil || !bytes.Contains(raw2, []byte("multipart/mixed")) || !bytes.Contains(raw2, []byte(`filename="my-cv.txt"`)) {
		t.Fatalf("built mail: %v\n%s", err, raw2)
	}
	if m2 := gontactMsg(t, jobs[1]); len(m2.To) != 1 || !strings.Contains(m2.To[0], "help@example.org") || m2.Body != "Thanks Bo" {
		t.Fatalf("mail (2): %+v", m2)
	}

	// demo_mode: the mail is not sent but the visitor sees success.
	f.Additional = "demo_mode: on"
	if err := srv.gontactSave(ctx, f, 1); err != nil {
		t.Fatal(err)
	}
	_, out, raw = multipartPost(t, anon, feedback, fields("your-name", "Bo", "your-quiz", "3", "your-consent", "1"), []upload{{"your-file", "cv.txt", []byte("x")}}, testUA)
	if out["status"] != "mail_sent" || out["demo_mode"] != true || len(mailJobs(t, srv)) != 2 {
		t.Fatalf("demo mode: %s", raw)
	}

	// Duplicate and delete from the list.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=wpcf7")
	dup := regexp.MustCompile(`href="(/wp-admin/admin\?page=wpcf7&amp;[^"]*action=copy[^"]*post=` + id + `[^"]*|/wp-admin/admin\?page=wpcf7&amp;[^"]*post=` + id + `[^"]*action=copy[^"]*)"`).FindStringSubmatch(body)
	if dup == nil {
		t.Fatalf("no Duplicate link:\n%s", body)
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(dup[1])); code != http.StatusFound || !strings.Contains(loc, "message=created") {
		t.Fatalf("duplicate: %d %s", code, loc)
	}
	if forms, _, _ := srv.gontactForms(ctx, "Job application", "", "", 1, -1); len(forms) != 2 {
		t.Fatalf("after duplicate: %d forms", len(forms))
	}

	// Deactivating removes the screens again; the forms stay.
	code, _, loc = get(t, c, base+gontactLink(t, c, base, "deactivate"))
	if code != http.StatusFound || !strings.Contains(loc, "deactivate=true") {
		t.Fatalf("deactivate: %d %s", code, loc)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/admin?page=wpcf7"); code != http.StatusForbidden {
		t.Fatalf("list after deactivate: %d", code)
	}
	if forms, _, _ := srv.gontactForms(ctx, "", "", "", 1, -1); len(forms) != 3 {
		t.Fatalf("forms after deactivate: %d", len(forms))
	}
}

func TestGontactFormTags(t *testing.T) {
	out, tags := gfScan(`[text* your-name placeholder "Jane"] [[text escaped]] [select menu include_blank "A" "B|b@x"] [acceptance ok optional]Yes[/acceptance] [submit "Go"] [unknown x]`, func(t *gfTag) string { return "<" + t.Type + ">" })
	if out != `<text*> [text escaped] <select> <acceptance> <submit> [unknown x]` {
		t.Fatalf("scan: %q", out)
	}
	if len(tags) != 4 || tags[0].Name != "your-name" || !tags[0].required() || !tags[0].has("placeholder") || tags[0].Values[0] != "Jane" {
		t.Fatalf("text tag: %+v", tags[0])
	}
	if tags[1].Values[1] != "B" || tags[1].pipeAfter("B") != "b@x" || tags[2].Content != "Yes" || tags[3].Values[0] != "Go" {
		t.Fatalf("tags: %+v %+v %+v", tags[1], tags[2], tags[3])
	}
	tt := gfParseTag("textarea", "msg 40x5 maxlength:100 class:a class:b id:m", "")
	if tt.colsOption("") != "40" || tt.rowsOption("") != "5" || tt.maxlengthOption("") != "100" || tt.classOption("x") != "x a b" || tt.idOption() != "m" {
		t.Fatalf("options: %+v", tt)
	}
	if f := gfParseTag("file", "cv limit:2mb", ""); f.limitOption() != 2<<20 {
		t.Fatalf("limit: %d", f.limitOption())
	}
	if gfParseTag("text", "1bad", "") != nil {
		t.Fatal("invalid name accepted")
	}
	if gfCanonicalize(" To-kyo ") != "tokyo" {
		t.Fatal("canonicalize")
	}
}
