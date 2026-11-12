package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// The contact form mails (the site mail and Mail (2), an auto-reply to an address the visitor
// types) carry site links ([_site_url], the page permalink). On a site whose home URL is a local
// or private address, a visitor naming another private address in Host must not get those links
// pointed at that address: the mail goes out from the trusted site.
func TestGontactMailIgnoresForgedLocalHost(t *testing.T) {
	base, _, srv := gontactSetup(t, true)
	ctx := context.Background()
	forms, _, _ := srv.gontactForms(ctx, "", "", "", 1, -1)
	if len(forms) != 1 {
		t.Fatalf("forms: %d", len(forms))
	}
	f := forms[0]
	// Mail (2): an auto-reply to the submitter that names the site and the page.
	f.Mail2.Active = true
	f.Mail2.Recipient = "[your-email]"
	f.Mail2.Subject = "Thanks"
	f.Mail2.Body = "Visit [_site_url] or [_post_url]"
	if err := srv.gontactSave(ctx, f, 1); err != nil {
		t.Fatal(err)
	}
	_, link := gontactPage(t, srv, `[contact-form-7 id="`+itoa64(f.ID)+`"]`)
	anon := anonClient()
	_, page, _ := get(t, anon, link)
	hidden := hiddenFields(t, page)

	fields := map[string]string{"your-name": "Ann", "your-email": "victim@example.org", "your-subject": "Hi", "your-message": "Hello"}
	for k, v := range hidden {
		fields[k] = v
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/contact-form-7/v1/contact-forms/"+itoa64(f.ID)+"/feedback", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("User-Agent", testUA)
	req.Host = "192.168.66.6:8080" // the attacker's address on the same private network
	resp, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["status"] != "mail_sent" {
		t.Fatalf("submission: %d %s", resp.StatusCode, raw)
	}
	jobs := mailJobs(t, srv)
	if len(jobs) != 2 {
		t.Fatalf("queued mails: %d", len(jobs))
	}
	for i, m := range jobs {
		body, _ := m["Body"].(string)
		if strings.Contains(body, "192.168.66.6") || !strings.Contains(body, base+"/") {
			t.Fatalf("mail %d does not keep the configured home %q:\n%s", i+1, base, body)
		}
	}
}
