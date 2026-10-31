package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/store"
	"wpgo/internal/wp"

	gomail "wpgo/internal/mail"
)

// Golementor Pro forms: admin-ajax action elementor_pro_forms_send_form, a port of upstream Pro's
// Ajax_Handler + Form_Record with the email, email2, redirect, webhook, save-to-database
// ("Collect Submissions") and popup actions; the marketing integrations (Mailchimp, Drip,
// ActiveCampaign, GetResponse, ConvertKit, MailerLite, Slack, Discord) live in
// gowebeditor_integrations*.go. GWEFormSubmit is the entry point other form front ends call.

var gweFormMessages = map[string]string{
	"success":                   "Your submission was successful.",
	"error":                     "Your submission failed because of an error.",
	"required_field":            "This field is required.",
	"invalid_form":              "Your submission failed because the form is invalid.",
	"server_error":              "Your submission failed because of a server error.",
	"subscriber_already_exists": "Subscriber already exists.",
}

// gweFormField is one submitted field (Form_Record fields).
type gweFormField struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Value    string `json:"value"`
	RawValue any    `json:"raw_value"`
	Required bool   `json:"required"`
}

type gweFormResult struct {
	success     bool
	errors      map[string]string
	messages    []string
	adminErrors []string
	data        map[string]any
	settings    map[string]any
}

func (r *gweFormResult) message(id string) string {
	if truthyAny(r.settings["custom_messages"]) {
		if v, ok := r.settings[id+"_message"].(string); ok {
			return v
		}
	}
	if m, ok := gweFormMessages[id]; ok {
		return m
	}
	return "Unknown error."
}

func (r *gweFormResult) addError(field, msg string) {
	r.errors[field] = msg
	r.success = false
}

func (r *gweFormResult) addErrorMessage(msg string) {
	r.messages = append(r.messages, msg)
	r.success = false
}

// gweProAjax handles Pro's top-level admin-ajax actions; false = not Pro's.
func (s *Server) gweProAjax(c *gin.Context, action string) bool {
	switch action {
	case "elementor_pro_forms_send_form":
		s.gweProSendForm(c)
		return true
	case gweV4FormNonce: // gap-atomic: atomic form submissions (gowebeditor_v4_form.go)
		s.gweV4SendForm(c)
		return true
	case "submit_stripe_form": // Stripe button (gowebeditor_payments.go)
		s.gweStripeSubmit(c)
		return true
	case "gwe_integration_validate": // Settings › Integrations "Validate API Key"
		s.gweIntegrationValidate(c)
		return true
	}
	return false
}

// gweProFormsEditorConfig is elementorProEditorConfig.forms (reCAPTCHA settings).
func (s *Server) gweProFormsEditorConfig() map[string]any {
	key := func(k string) any {
		if v := s.settings.String(k); v != "" {
			return v
		}
		return false
	}
	return map[string]any{
		"recaptcha": map[string]any{"enabled": s.settings.String("gwe_recaptcha_site_key") != "", "type": "v2_checkbox", "site_key": key("gwe_recaptcha_site_key"),
			"setup_message": "To use reCAPTCHA, add the API keys under Pursae Web Builder › Settings › Integrations."},
		"recaptcha_v3": map[string]any{"enabled": s.settings.String("gwe_recaptcha_v3_site_key") != "", "type": "v3", "site_key": key("gwe_recaptcha_v3_site_key"),
			"setup_message": "To use reCAPTCHA V3, add the API keys under Pursae Web Builder › Settings › Integrations."},
	}
}

// gweProFindForm locates the form widget: its document, the element and its settings with defaults.
func (s *Server) gweProFindForm(c *gin.Context, postID int64, formID string) (*store.Post, map[string]any) {
	return s.gweProFindWidget(c, postID, formID, "form")
}

// gweProFindWidget locates a widget of the given type in a document (a global widget's settings
// live in its template) and returns its document and settings with defaults.
func (s *Server) gweProFindWidget(c *gin.Context, postID int64, elementID, widgetType string) (*store.Post, map[string]any) {
	p, err := s.store.GetPost(c, postID)
	if err != nil || !gweBuilt(p) {
		return nil, nil
	}
	// Forms live in published documents (or templates); drafts only for their editors.
	if p.Status != "publish" && p.Status != "private" && !currentUser(c).Can("edit_post", p) {
		return nil, nil
	}
	doc := gweDocument(p)
	el := builder.FindElement(doc.Elements, elementID)
	if el == nil {
		return nil, nil
	}
	rc := s.gweRenderContext(c, false)
	// A global widget: the settings live in its template.
	if el.WidgetType == "global" || (el.TemplateID > 0 && el.WidgetType != widgetType) {
		if tpl, err := s.store.GetPost(c, el.TemplateID); err == nil {
			tdoc := gweDocument(tpl)
			if len(tdoc.Elements) > 0 && tdoc.Elements[0] != nil && tdoc.Elements[0].WidgetType == widgetType {
				el = tdoc.Elements[0]
				p = tpl
			}
		}
	}
	if el.WidgetType != widgetType {
		return nil, nil
	}
	settings := builder.WidgetSettings(el, rc)
	settings["id"] = elementID
	settings["form_post_id"] = p.ID
	return p, settings
}

func (s *Server) gweProSendForm(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		_ = c.Request.ParseForm()
	}
	postID := gweInt(c.PostForm("post_id"))
	formID := c.PostForm("form_id")
	res := newGWEFormResult(nil)
	p, settings := s.gweProFindForm(c, postID, formID)
	if settings == nil {
		res.addErrorMessage(res.message("invalid_form"))
		s.gweProFormSend(c, res)
		return
	}
	res.settings = settings
	items := asAnyList(settings["form_fields"])
	if len(items) == 0 {
		res.addErrorMessage(res.message("invalid_form"))
		s.gweProFormSend(c, res)
		return
	}
	// Form_Record::set_fields
	var fields []*gweFormField
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		f := &gweFormField{ID: gweStr(m["custom_id"]), Type: gweStr(m["field_type"]), Title: gweStr(m["field_label"]), Required: truthyAny(m["required"])}
		if f.ID == "" {
			continue
		}
		key := "form_fields[" + f.ID + "]"
		if vals := c.Request.PostForm[key+"[]"]; len(vals) > 0 {
			f.RawValue = vals
			f.Value = strings.Join(vals, ", ")
		} else if vals, ok := c.Request.PostForm[key]; ok && len(vals) > 0 {
			f.RawValue = vals[0]
			f.Value = vals[0]
		}
		f.Value = gweProSanitizeField(f.Type, f.Value)
		fields = append(fields, f)
	}
	// Request-bound validation (honeypot, reCAPTCHA, uploads); the rest runs in gweFormProcess.
	var kept []*gweFormField
	for _, f := range fields {
		switch f.Type {
		case "honeypot":
			if f.Value != "" {
				res.addError("invalid_form", "Invalid Form.")
			}
			continue // removed from the record
		case "recaptcha", "recaptcha_v3":
			if err := s.gweProVerifyRecaptcha(c, f.Type == "recaptcha_v3"); err != "" {
				res.addError(f.ID, err)
			}
			continue
		case "upload":
			if err := s.gweProFormUpload(c, f, items); err != "" {
				res.addError(f.ID, err)
			}
		}
		kept = append(kept, f)
	}
	s.gweFormProcess(c, p, settings, kept, res)
	s.gweProFormSend(c, res)
}

// GWEFormField is one submitted field of a form record (exported for other form front ends).
type GWEFormField = gweFormField

// GWEFormResult is the outcome of a submission: Success, the field errors and the response data.
type GWEFormResult = gweFormResult

// Success reports whether the submission passed validation and every action.
func (r *gweFormResult) Success() bool { return r.success }

// Errors are the per-field validation messages (field id => message).
func (r *gweFormResult) Errors() map[string]string { return r.errors }

// GWEFormSubmit is the stable entry point of the forms back end, shared by the classic Form widget
// and the atomic form. It validates a parsed submission (required fields, e-mail and URL syntax),
// stores it when "save-to-database" (Collect Submissions) is among settings["submit_actions"] and
// then runs the other Actions After Submit in upstream's order (email, email2, redirect, webhook,
// mailchimp, drip, activecampaign, getresponse, convertkit, mailerlite, slack, discord, popup),
// logging each action's status on the stored submission.
//
//   - post is the document that holds the form (may be nil); it is stored with the submission.
//   - settings uses the classic form widget's setting names (form_name, submit_actions, email_to,
//     mailchimp_list, mailchimp_fields_map, slack_webhook, custom_messages, success_message, ...).
//     settings["id"] must be the form element id.
//   - fields are in form order with ID, Type, Title, Value and Required set. Honeypot, reCAPTCHA
//     and upload fields must already be checked by the caller (uploads stored, Value = their URLs).
//
// Reply with s.GWEFormRespond(c, res) (the elementor_pro_forms_send_form JSON shape) or read
// res.Success() / res.Errors().
func (s *Server) GWEFormSubmit(c *gin.Context, post *store.Post, settings map[string]any, fields []*GWEFormField) *GWEFormResult {
	res := newGWEFormResult(settings)
	s.gweFormProcess(c, post, settings, fields, res)
	return res
}

// GWEFormRespond writes the admin-ajax JSON reply for a result (Ajax_Handler::send).
func (s *Server) GWEFormRespond(c *gin.Context, res *GWEFormResult) { s.gweProFormSend(c, res) }

func newGWEFormResult(settings map[string]any) *gweFormResult {
	if settings == nil {
		settings = map[string]any{}
	}
	return &gweFormResult{success: true, errors: map[string]string{}, data: map[string]any{}, settings: settings}
}

// gweFormActionOrder is upstream's action registration order (the order actions run in).
var gweFormActionOrder = []string{"save-to-database", "email", "email2", "redirect", "webhook", "mailchimp", "drip", "activecampaign",
	"getresponse", "convertkit", "mailerlite", "slack", "discord", "popup"}

// gweFormProcess validates fields and runs the actions; errors already in res are kept.
func (s *Server) gweFormProcess(c *gin.Context, p *store.Post, settings map[string]any, fields []*gweFormField, res *gweFormResult) {
	res.settings = settings
	for _, f := range fields {
		switch f.Type {
		case "step", "html", "honeypot", "recaptcha", "recaptcha_v3":
			continue
		}
		if f.Required && strings.TrimSpace(f.Value) == "" {
			if _, dup := res.errors[f.ID]; !dup {
				res.addError(f.ID, res.message("required_field"))
			}
		}
		if f.Type == "email" && f.Value != "" {
			if _, err := mail.ParseAddress(f.Value); err != nil {
				res.addError(f.ID, "Invalid email address.")
			}
		}
		if f.Type == "url" && f.Value != "" {
			if u, err := url.Parse(f.Value); err != nil || u.Scheme == "" || u.Host == "" {
				res.addError(f.ID, "Invalid URL.")
			}
		}
	}
	var kept []*gweFormField
	for _, f := range fields {
		if f.Type != "step" && f.Type != "html" && f.Type != "honeypot" && f.Type != "recaptcha" && f.Type != "recaptcha_v3" {
			kept = append(kept, f)
		}
	}
	fields = kept
	if !res.success {
		res.addErrorMessage(res.message("error"))
		return
	}
	meta := s.gweProFormMeta(c, settings)
	chosen := map[string]bool{}
	for _, a := range asAnyList(settings["submit_actions"]) {
		chosen[gweStr(a)] = true
	}
	rec := s.gweNewFormRecord(c, settings, fields)
	var subID int64
	var log []map[string]any
	for _, a := range gweFormActionOrder {
		if !chosen[a] {
			continue
		}
		var err error
		label := a
		switch a {
		case "save-to-database":
			id, serr := s.gweProSaveSubmission(c, p, settings, fields, meta)
			if serr != nil {
				res.adminErrors = append(res.adminErrors, "Collect Submissions "+serr.Error())
				res.addErrorMessage(res.message("server_error"))
			}
			subID = id
			continue // not logged on itself
		case "email":
			label = "Email"
			err = s.gweProFormEmail(c, settings, fields, meta, "")
		case "email2":
			label = "Email 2"
			err = s.gweProFormEmail(c, settings, fields, meta, "_2")
		case "redirect":
			label = "Redirect"
			to := gweProReplaceShortcodes(gweStr(settings["redirect_to"]), fields, true)
			if u, perr := url.Parse(to); perr == nil && u.Scheme != "" && u.Host != "" {
				res.data["redirect_url"] = to
			}
		case "webhook":
			label = "Webhook"
			err = s.gweProFormWebhook(c, settings, fields, meta)
		case "popup":
			label = "Popup"
			if act := gweStr(settings["popup_action"]); act != "" {
				res.data["popup"] = map[string]any{"action": act, "id": gweStr(settings["popup_action_popup_id"])}
			}
		default:
			in := gweIntegrationByName(a)
			if in == nil {
				continue
			}
			label = in.label
			err = in.run(s, c.Request.Context(), rec)
		}
		entry := map[string]any{"name": a, "label": label, "status": "success"}
		if err != nil {
			entry["status"] = "failed"
			entry["log"] = err.Error()
			res.adminErrors = append(res.adminErrors, label+" "+err.Error())
			res.addErrorMessage(res.message("error"))
		}
		log = append(log, entry)
		if subID > 0 {
			s.gweProSubmissionLog(c, subID, log)
		}
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// gweProFormSend mirrors Ajax_Handler::send.
func (s *Server) gweProFormSend(c *gin.Context, r *gweFormResult) {
	if r.success {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"message": r.message("success"), "data": r.data}})
		return
	}
	if len(r.messages) == 0 && len(r.errors) > 0 {
		r.messages = append(r.messages, r.message("invalid_form"))
	}
	msg := strings.Join(r.messages, "<br>")
	pid := gweInt(r.settings["form_post_id"])
	if pid == 0 {
		pid = gweInt(c.PostForm("post_id"))
	}
	if p, err := s.store.GetPost(c, pid); err == nil && currentUser(c).Can("edit_post", p) && len(r.adminErrors) > 0 {
		admin := append(r.adminErrors, "This message is not visible to site visitors.")
		msg += `<div class="elementor-forms-admin-errors">` + strings.Join(admin, "<br>") + `</div>`
	}
	errs := map[string]string{}
	for k, v := range r.errors {
		errs[k] = v
	}
	c.JSON(http.StatusOK, gin.H{"success": false, "data": gin.H{"message": msg, "errors": errs, "data": r.data}})
}

var gweTagsRe = regexp.MustCompile(`<[^>]*>`)

// gweProSanitizeField mirrors Form_Record::sanitize_field.
func gweProSanitizeField(typ, v string) string {
	switch typ {
	case "text", "password", "hidden", "search", "checkbox", "radio", "select", "tel", "number", "date", "time", "acceptance":
		v = strings.TrimSpace(strings.Join(strings.Fields(gweTagsRe.ReplaceAllString(v, "")), " "))
	case "url":
		v = strings.TrimSpace(v)
		if u, err := url.Parse(v); err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
			v = ""
		}
	case "textarea":
		lines := strings.Split(gweTagsRe.ReplaceAllString(v, ""), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, "\r \t")
		}
		v = strings.TrimSpace(strings.Join(lines, "\n"))
	case "email":
		v = strings.TrimSpace(v)
		v = regexp.MustCompile(`[^A-Za-z0-9.!#$%&'*+/=?^_{|}~@-]`).ReplaceAllString(v, "")
	}
	return v
}

// gweProFormMeta mirrors Form_Record::get_form_meta for the email action's metadata setting.
func (s *Server) gweProFormMeta(c *gin.Context, settings map[string]any) []map[string]string {
	now := time.Now().In(s.loc())
	var out []map[string]string
	keys := asAnyList(settings["form_metadata"])
	if keys == nil {
		keys = []any{"date", "time", "page_url", "user_agent", "remote_ip", "credit"}
	}
	for _, k := range keys {
		switch gweStr(k) {
		case "date":
			out = append(out, map[string]string{"id": "date", "title": "Date", "value": wp.FormatDate(now, s.settings.String("date_format"))})
		case "time":
			out = append(out, map[string]string{"id": "time", "title": "Time", "value": wp.FormatDate(now, s.settings.String("time_format"))})
		case "page_url":
			out = append(out, map[string]string{"id": "page_url", "title": "Page URL", "value": c.PostForm("referrer")})
		case "page_title":
			out = append(out, map[string]string{"id": "page_title", "title": "Page Title", "value": c.PostForm("referer_title")})
		case "user_agent":
			out = append(out, map[string]string{"id": "user_agent", "title": "User Agent", "value": c.Request.UserAgent()})
		case "remote_ip":
			out = append(out, map[string]string{"id": "remote_ip", "title": "Remote IP", "value": c.ClientIP()})
		case "credit":
			out = append(out, map[string]string{"id": "credit", "title": "Powered by", "value": "Pursae Web Builder"})
		}
	}
	return out
}

var gweFieldShortcodeRe = regexp.MustCompile(`\[field[^\]]*id="(\w+)"[^\]]*\]`)

// gweProReplaceShortcodes mirrors Form_Record::replace_setting_shortcodes.
func gweProReplaceShortcodes(text string, fields []*gweFormField, urlencode bool) string {
	return gweFieldShortcodeRe.ReplaceAllStringFunc(text, func(m string) string {
		id := gweFieldShortcodeRe.FindStringSubmatch(m)[1]
		for _, f := range fields {
			if f.ID == id {
				if urlencode {
					return url.QueryEscape(f.Value)
				}
				return f.Value
			}
		}
		return ""
	})
}

func gweFieldFormatted(title, value string) string {
	if title != "" {
		return title + ": " + value
	}
	return value
}

// gweProFormEmail mirrors the Email / Email 2 actions (suffix "" or "_2").
func (s *Server) gweProFormEmail(c *gin.Context, settings map[string]any, fields []*gweFormField, meta []map[string]string, suffix string) error {
	get := func(k string) string {
		return strings.TrimSpace(gweProReplaceShortcodes(gweStr(settings[k+suffix]), fields, false))
	}
	sendHTML := gweStr(settings["email_content_type"+suffix]) != "plain"
	br := "\n"
	if sendHTML {
		br = "<br>"
	}
	site := s.settings.String("blogname")
	to := get("email_to")
	if to == "" {
		to = s.settings.String("admin_email")
	}
	subject := get("email_subject")
	if subject == "" {
		subject = `New message from "` + site + `"`
	}
	content := get("email_content")
	if content == "" {
		content = "[all-fields]"
	}
	if strings.Contains(content, "[all-fields]") {
		var b strings.Builder
		for _, f := range fields {
			v := f.Value
			if f.Type == "textarea" && sendHTML {
				v = strings.ReplaceAll(html.EscapeString(v), "\n", "<br />")
			} else if sendHTML {
				v = html.EscapeString(v)
			}
			t := f.Title
			if sendHTML {
				t = html.EscapeString(t)
			}
			if fm := gweFieldFormatted(t, v); fm != "" || f.Title != "" {
				b.WriteString(fm + br)
			}
		}
		content = strings.ReplaceAll(content, "[all-fields]", b.String())
	}
	var metaText strings.Builder
	for _, m := range meta {
		v := m["value"]
		if sendHTML {
			v = html.EscapeString(v)
		}
		metaText.WriteString(gweFieldFormatted(m["title"], v) + br)
	}
	if metaText.Len() > 0 {
		content += br + "---" + br + br + metaText.String()
	}
	// Reply-To: the email of the chosen field (email action), or the setting itself (email 2).
	replyTo := ""
	if suffix == "" {
		if id := gweStr(settings["email_reply_to"]); id != "" {
			for _, f := range fields {
				if f.ID == id {
					if _, err := mail.ParseAddress(f.Value); err == nil {
						replyTo = f.Value
					}
				}
			}
		}
	} else {
		replyTo = get("email_reply_to")
	}
	msg := gomail.Message{To: splitEmails(to), Subject: subject, ReplyTo: replyTo}
	if sendHTML {
		msg.HTML = content
		msg.Body = html.UnescapeString(gweTagsRe.ReplaceAllString(strings.ReplaceAll(content, "<br>", "\n"), ""))
	} else {
		msg.Body = content
	}
	m := s.mailer()
	if name := get("email_from_name"); name != "" || get("email_from") != "" {
		addr := get("email_from")
		if addr == "" {
			if a, err := mail.ParseAddress(m.From); err == nil {
				addr = a.Address
			}
		}
		if name == "" {
			name = site
		}
		if _, err := mail.ParseAddress(addr); err == nil {
			cp := *m
			cp.From = (&mail.Address{Name: name, Address: addr}).String()
			m = &cp
		}
	}
	if err := m.Send(c.Request.Context(), msg); err != nil {
		return err
	}
	for _, list := range []string{get("email_to_cc"), get("email_to_bcc")} {
		for _, addr := range splitEmails(list) {
			cp := msg
			cp.To = []string{addr}
			if err := m.Send(c.Request.Context(), cp); err != nil {
				return err
			}
		}
	}
	return nil
}

func splitEmails(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// gweProFormWebhook mirrors the Webhook action: a form-encoded POST to a public URL.
func (s *Server) gweProFormWebhook(c *gin.Context, settings map[string]any, fields []*gweFormField, meta []map[string]string) error {
	target := gweStr(settings["webhooks"])
	if target == "" {
		return nil
	}
	if err := gweSafeURL(target); err != nil {
		return err
	}
	form := url.Values{}
	if gweStr(settings["webhooks_advanced_data"]) == "yes" {
		fj, _ := json.Marshal(fields)
		mj, _ := json.Marshal(meta)
		form.Set("form[id]", gweStr(settings["id"]))
		form.Set("form[name]", gweStr(settings["form_name"]))
		form.Set("fields", string(fj))
		form.Set("meta", string(mj))
	} else {
		for _, f := range fields {
			k := f.Title
			if k == "" {
				k = "No Label " + f.ID
			}
			form.Set(k, f.Value)
		}
		for _, m := range meta {
			form.Set(m["title"], m["value"])
		}
		form.Set("form_id", gweStr(settings["id"]))
		form.Set("form_name", gweStr(settings["form_name"]))
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, _ []*http.Request) error { return gweSafeURL(r.URL.String()) }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Webhook error.")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Webhook error.")
	}
	return nil
}

// gweSafeURL refuses non-HTTP URLs and hosts that resolve to private networks (wp_safe_remote_post).
func gweSafeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("Invalid webhook URL.")
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("Webhook host not found.")
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("Webhook URL is not public.")
		}
	}
	return nil
}

// gweProVerifyRecaptcha checks the g-recaptcha-response with Google ("" = valid).
func (s *Server) gweProVerifyRecaptcha(c *gin.Context, v3 bool) string {
	secretKey := "gwe_recaptcha_secret_key"
	if v3 {
		secretKey = "gwe_recaptcha_v3_secret_key"
	}
	secret := s.settings.String(secretKey)
	if secret == "" {
		return "" // not configured: the field renders nothing either
	}
	token := c.PostForm("g-recaptcha-response")
	if token == "" {
		return "The Captcha field cannot be blank. Please enter a value."
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.google.com/recaptcha/api/siteverify",
		strings.NewReader(url.Values{"secret": {secret}, "response": {token}, "remoteip": {c.ClientIP()}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "Can not connect to the reCAPTCHA server."
	}
	defer resp.Body.Close()
	var out struct {
		Success bool    `json:"success"`
		Score   float64 `json:"score"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || !out.Success {
		return "Invalid form, reCAPTCHA validation failed."
	}
	if v3 {
		threshold := 0.5
		if t := s.settings.Float("gwe_recaptcha_v3_threshold"); t > 0 {
			threshold = t
		}
		if out.Score < threshold {
			return "Invalid form, reCAPTCHA validation failed."
		}
	}
	return ""
}

// gweProFormUpload stores an upload field's files in uploads/elementor/forms and sets the value to
// their URLs.
func (s *Server) gweProFormUpload(c *gin.Context, f *gweFormField, items []any) string {
	var item map[string]any
	for _, it := range items {
		if m, _ := it.(map[string]any); m != nil && gweStr(m["custom_id"]) == f.ID {
			item = m
		}
	}
	if c.Request.MultipartForm == nil {
		return ""
	}
	files := c.Request.MultipartForm.File["form_fields["+f.ID+"]"]
	files = append(files, c.Request.MultipartForm.File["form_fields["+f.ID+"][]"]...)
	if len(files) == 0 {
		return ""
	}
	maxMB := gweInt(item["file_sizes"])
	allowed := map[string]bool{}
	for _, t := range strings.Split(gweStr(item["file_types"]), ",") {
		if t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "."))); t != "" {
			allowed[t] = true
		}
	}
	if !truthyAny(item["allow_multiple_upload"]) && len(files) > 1 {
		return "There are too many files."
	}
	if mx := gweInt(item["max_files"]); mx > 0 && int64(len(files)) > mx {
		return "There are too many files."
	}
	blocked := map[string]bool{"php": true, "phtml": true, "php3": true, "php4": true, "php5": true, "phps": true, "cgi": true, "exe": true,
		"pl": true, "py": true, "sh": true, "js": true, "html": true, "htm": true, "svg": true, "jar": true, "asp": true, "aspx": true}
	dir := filepath.Join(s.cfg.UploadsDir, "elementor", "forms")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "There was an error while trying to upload your file."
	}
	var urls []string
	for _, fh := range files {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fh.Filename), "."))
		if blocked[ext] || (len(allowed) > 0 && !allowed[ext]) {
			return "This file type is not allowed."
		}
		if maxMB > 0 && fh.Size > maxMB<<20 {
			return "This file exceeds the maximum allowed size."
		}
		src, err := fh.Open()
		if err != nil {
			return "There was an error while trying to upload your file."
		}
		rnd := make([]byte, 12)
		_, _ = rand.Read(rnd)
		name := hex.EncodeToString(rnd) + "." + ext
		dst, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			src.Close()
			return "There was an error while trying to upload your file."
		}
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return "There was an error while trying to upload your file."
		}
		urls = append(urls, s.links(c).HomeURL("/wp-content/uploads/elementor/forms/"+name))
	}
	f.Value = strings.Join(urls, " , ")
	f.RawValue = urls
	return ""
}

// gweProSaveSubmission is the Collect Submissions action; it returns the new submission's id.
func (s *Server) gweProSaveSubmission(c *gin.Context, p *store.Post, settings map[string]any, fields []*gweFormField, meta []map[string]string) (int64, error) {
	fj, _ := json.Marshal(fields)
	mm := map[string]string{}
	for _, m := range meta {
		mm[m["id"]] = m["value"]
	}
	mj, _ := json.Marshal(mm)
	var uid any
	if u := currentUser(c); u != nil && u.ID > 0 {
		uid = u.ID
	}
	var pid any
	if p != nil {
		pid = p.ID
		if fp := gweInt(c.PostForm("post_id")); fp > 0 {
			pid = fp // the page the form was submitted on (a global form's template id otherwise)
		}
	}
	var id int64
	err := s.store.Pool.QueryRow(c, `INSERT INTO gwe_form_submissions (post_id, element_id, form_name, referer, referer_title, user_id, user_ip,
		user_agent, fields, meta, actions) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,'[]'::jsonb) RETURNING id`,
		pid, gweStr(settings["id"]), gweStr(settings["form_name"]), c.PostForm("referrer"), c.PostForm("referer_title"), uid,
		c.ClientIP(), c.Request.UserAgent(), string(fj), string(mj)).Scan(&id)
	return id, err
}

// gweProSubmissionLog stores the actions log on a submission (save_action_log).
func (s *Server) gweProSubmissionLog(c *gin.Context, id int64, log []map[string]any) {
	lj, _ := json.Marshal(log)
	_, _ = s.store.Pool.Exec(c, `UPDATE gwe_form_submissions SET actions = $2::jsonb, updated_at = now() WHERE id = $1`, id, string(lj))
}

// gweProNoPriv reports whether a logged-out request is one of Pro's public admin-ajax actions.
func gweProNoPriv(c *gin.Context) bool {
	if c.Request.URL.Path != "/wp-admin/admin-ajax" || c.Request.Method != http.MethodPost {
		return false
	}
	action := c.Query("action")
	if action == "" {
		action = c.PostForm("action")
	}
	return action == "elementor_pro_forms_send_form" || action == "submit_stripe_form" || action == gweV4FormNonce // gap-atomic: + atomic forms
}
