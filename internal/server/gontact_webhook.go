package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Pursae Contact Form outbound webhooks and the two Wasm hooks that shape a submission.
//
// Additional Settings of a form (one per line, like the other CF7 settings):
//
//	webhook_url: https://example.com/hook      (repeat the line for several endpoints)
//	webhook_secret: shared-secret              (optional: signs the body)
//
// After the mail is sent, each URL gets a JSON POST through the job queue (so a slow or failing
// endpoint never delays the visitor and is retried with backoff). The body is signed as
// X-Pursae-Signature: sha256=<hex HMAC-SHA256(body, secret)> when a secret is set. Delivery goes
// through gooWebhookClient, so it reaches public addresses only.
//
// Hooks (permission forms:access): the filter wpcf7_validate collects extra validation errors and
// the filter wpcf7_before_send_mail may change the posted values, skip the mail or stop the
// submission. See docs/developer/plugins/hooks.md.

const gontactWebhookJob = "gontact_webhook"

type gontactWebhookJobPayload struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
	Body   string `json:"body"`
	Form   int64  `json:"form"`
}

// gontactWebhookURLs are the valid http(s) endpoints of the form's webhook_url settings.
func (f *gfForm) webhookURLs() []string {
	var out []string
	for _, v := range f.setting("webhook_url") {
		if u, err := url.Parse(v); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			out = append(out, v)
		}
	}
	return out
}

func gontactWebhookSign(body []byte, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// queueWebhooks enqueues one delivery per configured URL.
func (sub *gfSubmission) queueWebhooks() {
	urls := sub.form.webhookURLs()
	if len(urls) == 0 || sub.form.demoMode() {
		return
	}
	var secret string
	if v := sub.form.setting("webhook_secret"); len(v) > 0 {
		secret = v[len(v)-1]
	}
	body, _ := json.Marshal(map[string]any{
		"event": "submission.mail_sent", "form_id": sub.form.ID, "form_title": sub.form.Title,
		"entry_id": sub.entryID, "submitted_at": sub.timestamp.UTC().Format(time.RFC3339),
		"page_url": sub.pageURL, "remote_ip": sub.remoteIP, "posted_data": sub.flatPosted(),
	})
	for _, u := range urls {
		p := gontactWebhookJobPayload{URL: u, Secret: secret, Body: string(body), Form: sub.form.ID}
		if err := sub.s.queueJob(context.WithoutCancel(sub.ctx), gontactWebhookJob, p, time.Time{}); err != nil {
			slog.Error("gontact form: queue webhook", "form", sub.form.ID, "err", err)
		}
	}
}

// gontactWebhookRun delivers one queued webhook; an error makes the job queue retry it.
func (s *Server) gontactWebhookRun(ctx context.Context, p map[string]any) error {
	raw, _ := json.Marshal(p)
	var job gontactWebhookJobPayload
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil // a payload that cannot be read never will be
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, job.URL, bytes.NewReader([]byte(job.Body)))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pursae-ContactForm/"+gontactVersion+" Webhook")
	req.Header.Set("X-Pursae-Event", "submission.mail_sent")
	req.Header.Set("X-Pursae-Form-ID", fmt.Sprint(job.Form))
	if job.Secret != "" {
		req.Header.Set("X-Pursae-Signature", gontactWebhookSign([]byte(job.Body), job.Secret))
	}
	resp, err := gooWebhookClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "private network address") {
			slog.Warn("gontact form: webhook URL refused (private network address)", "form", job.Form)
			return nil
		}
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("webhook answered HTTP " + resp.Status)
	}
	return nil
}

// pluginValidate runs the wpcf7_validate filter: a plugin returns {field name: message}.
func (sub *gfSubmission) pluginValidate() {
	if sub.s.plugins == nil || !sub.s.plugins.hooks.HasFilter("wpcf7_validate") {
		return
	}
	res, _ := sub.s.applyFilters(sub.ctx, "wpcf7_validate", map[string]any{}, map[string]any{
		"contact_form_id": sub.form.ID, "posted_data": sub.flatPosted()}).(map[string]any)
	for _, t := range sub.tags {
		if msg, _ := res[t.Name].(string); t.Name != "" && msg != "" {
			sub.invalidateWith(t, msg)
		}
	}
}

// invalidateWith is invalidate() with a message of the plugin's own.
func (sub *gfSubmission) invalidateWith(t *gfTag, msg string) {
	for _, inv := range sub.invalid {
		if inv.name == t.Name {
			return
		}
	}
	sub.invalid = append(sub.invalid, gfInvalid{name: t.Name, reason: msg, idref: t.idOption()})
}

// beforeSendMail runs the wpcf7_before_send_mail filter. The value is
// {posted_data: {name: value}, skip_mail: bool, abort: "message"}; a plugin may change posted_data
// of existing fields (what the mail prints), set skip_mail to send no mail, or abort to stop the
// submission with that message. It reports false when the submission was aborted.
func (sub *gfSubmission) beforeSendMail() bool {
	if sub.s.plugins == nil || !sub.s.plugins.hooks.HasFilter("wpcf7_before_send_mail") {
		return true
	}
	in := map[string]any{"posted_data": sub.flatPosted(), "skip_mail": sub.skipMail, "abort": ""}
	res, ok := sub.s.applyFilters(sub.ctx, "wpcf7_before_send_mail", in, map[string]any{"contact_form_id": sub.form.ID, "title": sub.form.Title}).(map[string]any)
	if !ok {
		return true
	}
	if msg, _ := res["abort"].(string); msg != "" {
		sub.status, sub.message = "aborted", msg
		return false
	}
	if pd, ok := res["posted_data"].(map[string]any); ok {
		for k, v := range pd {
			if str, isStr := v.(string); isStr {
				if _, exists := sub.posted[k]; exists {
					sub.posted[k] = []string{str}
				}
			}
		}
	}
	if b, ok := res["skip_mail"].(bool); ok {
		sub.skipMail = b
	}
	return true
}
