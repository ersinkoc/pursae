package server

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Gontact Form is WordPress Go's native clone of Contact Form 7 (see docs/gontact-form-features.md).
// Like the other native plugins it is off on a fresh install and switched on the Plugins screen (option
// native_gontact_form). Its routes stay registered; while it is off they answer 404, the REST
// namespace contact-form-7/v1 is hidden, the [contact-form-7] shortcode is left alone and the admin
// menu has no Contact entry. Forms are posts of type wpcf7_contact_form with CF7's meta keys.

const (
	gontactSlug      = "gontact-form"
	gontactSetting   = "native_gontact_form"
	gontactPostType  = "wpcf7_contact_form"
	gontactNamespace = "contact-form-7/v1"
	gontactVersion   = "6.2.3" // the Contact Form 7 release Gontact Form follows (_wpcf7_version)
	gontactAssets    = "/wp-content/gontact-form"
	gontactSecretKey = "gontact_form_secret" // HMAC key for quiz answers and posted data hashes (never shown to plugins)
)

// gontactOn reports whether Gontact Form is active.
func (s *Server) gontactOn() bool { return s.settings.Bool(gontactSetting) }

// gontactOnly is the middleware of Gontact Form's own routes: 404 while the plugin is off.
func (s *Server) gontactOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.gontactOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// gontactRESTHidden reports whether a REST namespace belongs to an inactive Gontact Form.
func (s *Server) gontactRESTHidden(ns string) bool { return ns == gontactNamespace && !s.gontactOn() }

// registerGontact wires the front-end assets; registerGontactREST the REST routes.
func (s *Server) registerGontact(r *gin.Engine) {
	s.static(r, gontactAssets, "admin/assets/gontact", s.gontactOnly())
	r.GET(gfCaptchaPath+"/:file", s.gontactOnly(), s.gontactCaptchaImage) // [captchac] images (gontact_captcha.go)
}

// gontactActivate switches the plugin on: the post type, the secret and, the first time, the
// sample "Contact form 1" (wpcf7_install()).
func (s *Server) gontactActivate(ctx context.Context, authorID int64) error {
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ($1, 'Contact Forms', false, false, '', false, '{"title":true}', '{"name":"Contact Forms","singular_name":"Contact Form"}')
		ON CONFLICT (slug) DO NOTHING`, gontactPostType); err != nil {
		return err
	}
	vals := map[string]any{gontactSetting: true}
	if s.settings.String(gontactSecretKey) == "" {
		vals[gontactSecretKey] = gfRandomHex(32)
	}
	if err := s.settings.Set(ctx, vals); err != nil {
		return err
	}
	if !s.settings.Bool("gontact_form_installed") {
		var n int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = $1`, gontactPostType).Scan(&n)
		if n == 0 {
			f := s.gontactDefaultForm()
			f.Title = "Contact form 1"
			f.AuthorID = authorID
			if err := s.gontactSave(ctx, f, authorID); err != nil {
				return err
			}
		}
		_ = s.settings.Set(ctx, map[string]any{"gontact_form_installed": true})
	}
	s.purgeAll(ctx)
	return nil
}

// gontactDeactivate switches the plugin off; the forms stay in the database.
func (s *Server) gontactDeactivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{gontactSetting: false}); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// Forms --------------------------------------------------------------------------

// gfMail is one mail template (the _mail and _mail_2 meta of CF7).
type gfMail struct {
	Active            bool
	Subject, Sender   string
	Body, Recipient   string
	AdditionalHeaders string
	Attachments       string
	UseHTML           bool
	ExcludeBlank      bool
}

// gfForm is a contact form (WPCF7_ContactForm).
type gfForm struct {
	ID         int64
	Title      string
	Hash       string
	Locale     string
	AuthorID   int64
	Date       time.Time
	Form       string
	Mail       gfMail
	Mail2      gfMail
	Messages   map[string]string
	Additional string
}

// ShortHash is the 7-character id the shortcode uses (CF7 5.8+).
func (f *gfForm) ShortHash() string {
	if len(f.Hash) >= 7 {
		return f.Hash[:7]
	}
	return strconv.FormatInt(f.ID, 10)
}

// Shortcode is the [contact-form-7 id="…" title="…"] code shown in the admin.
func (f *gfForm) Shortcode() string {
	return `[contact-form-7 id="` + f.ShortHash() + `" title="` + strings.ReplaceAll(f.Title, `"`, "") + `"]`
}

func (f *gfForm) message(key string) string {
	if v, ok := f.Messages[key]; ok {
		return v
	}
	for _, m := range gfMessageDefs {
		if m.Key == key {
			return m.Default
		}
	}
	return ""
}

var gfHashRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

var gfSettingRe = regexp.MustCompile(`^([a-zA-Z0-9_]+)[\t ]*:(.*)$`)

// setting is additional_setting(): the values of every "name: value" line, last one wins in isTrue.
func (f *gfForm) setting(name string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(f.Additional, "\r\n", "\n"), "\n") {
		if m := gfSettingRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil && m[1] == name {
			out = append(out, strings.Trim(strings.TrimSpace(m[2]), `"'`))
		}
	}
	return out
}

// isTrue is WPCF7_ContactForm::is_true().
func (f *gfForm) isTrue(name string) bool {
	vals := f.setting(name)
	if len(vals) == 0 {
		return false
	}
	switch strings.ToLower(vals[len(vals)-1]) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}

func (f *gfForm) demoMode() bool { return f.isTrue("demo_mode") }

// gfMessageDef is one entry of the Messages tab.
type gfMessageDef struct{ Key, Description, Default string }

var gfMessageDefs = []gfMessageDef{
	{"mail_sent_ok", "Sender’s message was sent successfully", "Thank you for your message. It has been sent."},
	{"mail_sent_ng", "Sender’s message failed to send", "There was an error trying to send your message. Please try again later."},
	{"validation_error", "Validation errors occurred", "One or more fields have an error. Please check and try again."},
	{"spam", "Submission was referred to as spam", "There was an error trying to send your message. Please try again later."},
	{"accept_terms", "There are terms that the sender must accept", "You must accept the terms and conditions before sending your message."},
	{"invalid_required", "There is a field that the sender must fill in", "Please fill out this field."},
	{"invalid_too_long", "There is a field with input that is longer than the maximum allowed length", "This field has a too long input."},
	{"invalid_too_short", "There is a field with input that is shorter than the minimum allowed length", "This field has a too short input."},
	{"upload_failed", "Uploading a file fails for any reason", "There was an unknown error uploading the file."},
	{"upload_file_type_invalid", "Uploaded file is not allowed for file type", "You are not allowed to upload files of this type."},
	{"upload_file_too_large", "Uploaded file is too large", "The uploaded file is too large."},
	{"upload_failed_php_error", "Uploading a file fails for PHP error", "There was an error uploading the file."},
	{"invalid_date", "Date format that the sender entered is invalid", "Please enter a date in YYYY-MM-DD format."},
	{"date_too_early", "Date is earlier than minimum limit", "This field has a too early date."},
	{"date_too_late", "Date is later than maximum limit", "This field has a too late date."},
	{"invalid_number", "Number format that the sender entered is invalid", "Please enter a number."},
	{"number_too_small", "Number is smaller than minimum limit", "This field has a too small number."},
	{"number_too_large", "Number is larger than maximum limit", "This field has a too large number."},
	{"quiz_answer_not_correct", "Sender does not enter the correct answer to the quiz", "The answer to the quiz is incorrect."},
	{"captcha_not_match", "The code that sender entered does not match the CAPTCHA", "Your entered code is incorrect."},
	{"invalid_email", "Email address that the sender entered is invalid", "Please enter an email address."},
	{"invalid_url", "URL that the sender entered is invalid", "Please enter a URL."},
	{"invalid_tel", "Telephone number that the sender entered is invalid", "Please enter a telephone number."},
}

const gfDefaultFormTemplate = `<label> Your name
    [text* your-name autocomplete:name] </label>

<label> Your email
    [email* your-email autocomplete:email] </label>

<label> Subject
    [text* your-subject] </label>

<label> Your message (optional)
    [textarea your-message] </label>

[submit "Submit"]`

// gontactFromEmail is WPCF7_ContactFormTemplate::from_email().
func (s *Server) gontactFromEmail() string {
	admin := s.settings.String("admin_email")
	host := strings.ToLower(hostOf(s.settings.String("home")))
	if host == "" || host == "localhost" || strings.HasPrefix(host, "127.") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test") {
		return admin
	}
	host = strings.TrimPrefix(host, "www.")
	if strings.HasSuffix(strings.ToLower(admin), "@"+host) {
		return admin
	}
	return "wordpress@" + host
}

// gontactDefaultForm is a new form filled with CF7's default templates.
func (s *Server) gontactDefaultForm() *gfForm {
	from := "[_site_title] <" + s.gontactFromEmail() + ">"
	f := &gfForm{Locale: "en_US", Form: gfDefaultFormTemplate, Messages: map[string]string{}}
	f.Mail = gfMail{
		Subject: `[_site_title] "[your-subject]"`, Sender: from, Recipient: "[_site_admin_email]",
		Body: "From: [your-name] [your-email]\nSubject: [your-subject]\n\nMessage Body:\n[your-message]\n\n-- \n" +
			"This is a notification that a contact form was submitted on your website ([_site_title] [_site_url]).",
		AdditionalHeaders: "Reply-To: [your-email]",
	}
	f.Mail2 = gfMail{
		Subject: `[_site_title] "[your-subject]"`, Sender: from, Recipient: "[your-email]",
		Body: "Message Body:\n[your-message]\n\n-- \n" +
			"This email is a receipt for your contact form submission on our website ([_site_title] [_site_url]) in which your email address was used. If that was not you, please ignore this message.",
		AdditionalHeaders: "Reply-To: [_site_admin_email]",
	}
	for _, m := range gfMessageDefs {
		f.Messages[m.Key] = m.Default
	}
	return f
}

func gfMailMeta(m gfMail, withActive bool) map[string]any {
	out := map[string]any{"subject": m.Subject, "sender": m.Sender, "body": m.Body, "recipient": m.Recipient,
		"additional_headers": m.AdditionalHeaders, "attachments": m.Attachments, "use_html": m.UseHTML, "exclude_blank": m.ExcludeBlank}
	if withActive {
		out["active"] = m.Active
	}
	return out
}

func gfMailFrom(v any) gfMail {
	m, _ := v.(map[string]any)
	str := func(k string) string { s, _ := m[k].(string); return s }
	b := func(k string) bool {
		switch t := m[k].(type) {
		case bool:
			return t
		case float64:
			return t != 0
		case string:
			return t == "1" || t == "true"
		}
		return false
	}
	return gfMail{Active: b("active"), Subject: str("subject"), Sender: str("sender"), Body: str("body"), Recipient: str("recipient"),
		AdditionalHeaders: str("additional_headers"), Attachments: str("attachments"), UseHTML: b("use_html"), ExcludeBlank: b("exclude_blank")}
}

// gontactFormFromPost reads a form from its post.
func gontactFormFromPost(p *store.Post) *gfForm {
	str := func(k string) string { s, _ := p.Meta[k].(string); return s }
	f := &gfForm{ID: p.ID, Title: p.Title, AuthorID: p.AuthorID, Date: p.Date(), Hash: str("_hash"), Locale: str("_locale"),
		Form: str("_form"), Additional: str("_additional_settings"), Messages: map[string]string{}}
	if f.Locale == "" {
		f.Locale = "en_US"
	}
	if _, ok := p.Meta["_form"]; !ok {
		f.Form = p.Content
	}
	f.Mail, f.Mail2 = gfMailFrom(p.Meta["_mail"]), gfMailFrom(p.Meta["_mail_2"])
	if m, ok := p.Meta["_messages"].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				f.Messages[k] = s
			}
		}
	}
	return f
}

// gontactSave inserts or updates a form (WPCF7_ContactForm::save()).
func (s *Server) gontactSave(ctx context.Context, f *gfForm, editorID int64) error {
	if f.Hash == "" {
		sum := sha1.Sum([]byte(gfRandomHex(16) + strconv.FormatInt(time.Now().UnixNano(), 10)))
		f.Hash = hex.EncodeToString(sum[:])
	}
	if strings.TrimSpace(f.Title) == "" {
		f.Title = "Untitled"
	}
	msgs := map[string]any{}
	for k, v := range f.Messages {
		msgs[k] = v
	}
	meta := map[string]any{"_form": f.Form, "_mail": gfMailMeta(f.Mail, false), "_mail_2": gfMailMeta(f.Mail2, true),
		"_messages": msgs, "_additional_settings": f.Additional, "_locale": f.Locale, "_hash": f.Hash}
	if f.ID == 0 {
		p := &store.Post{Type: gontactPostType, AuthorID: f.AuthorID, Title: f.Title, Content: f.Form, Status: "publish",
			CommentStatus: "closed", PingStatus: "closed", Meta: meta}
		if err := s.store.InsertPost(ctx, p); err != nil {
			return err
		}
		f.ID, f.Date = p.ID, p.Date()
		return nil
	}
	p, err := s.store.GetPost(ctx, f.ID)
	if err != nil {
		return err
	}
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	for k, v := range meta {
		p.Meta[k] = v
	}
	p.Title, p.Content = f.Title, f.Form
	return s.store.UpdatePost(ctx, p, editorID)
}

// gontactForm loads a form by post id.
func (s *Server) gontactForm(ctx context.Context, id int64) (*gfForm, error) {
	p, err := s.store.GetPost(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Type != gontactPostType || p.Status == "trash" {
		return nil, fmt.Errorf("not a contact form")
	}
	return gontactFormFromPost(p), nil
}

// gontactFormByRef resolves a shortcode id (hash prefix or post id) or, failing that, a title
// (wpcf7_get_contact_form_by_hash / wpcf7_contact_form / wpcf7_get_contact_form_by_title).
func (s *Server) gontactFormByRef(ctx context.Context, id, title string) *gfForm {
	id = strings.TrimSpace(id)
	if id != "" {
		var pid int64
		if gfHashRe.MatchString(id) && s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = $1 AND status = 'publish' AND meta->>'_hash' LIKE $2 || '%' ORDER BY id LIMIT 1`,
			gontactPostType, strings.ToLower(id)).Scan(&pid) == nil {
			if f, err := s.gontactForm(ctx, pid); err == nil {
				return f
			}
		}
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			if f, err := s.gontactForm(ctx, n); err == nil {
				return f
			}
		}
	}
	if title != "" {
		var pid int64
		if err := s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = $1 AND status = 'publish' AND title = $2 ORDER BY id LIMIT 1`,
			gontactPostType, title).Scan(&pid); err == nil {
			if f, err := s.gontactForm(ctx, pid); err == nil {
				return f
			}
		}
	}
	return nil
}

// gontactForms lists the forms for the admin list table and the block.
func (s *Server) gontactForms(ctx context.Context, search, orderby, order string, page, perPage int) ([]*gfForm, int, error) {
	q := store.PostQuery{Types: []string{gontactPostType}, Statuses: []string{"publish"}, Search: search, Page: page, PerPage: perPage,
		OrderBy: "title", Order: "asc"}
	if orderby == "date" || orderby == "title" {
		q.OrderBy = orderby
	}
	if order != "" {
		q.Order = order
	}
	posts, total, err := s.store.QueryPosts(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*gfForm, 0, len(posts))
	for _, p := range posts {
		out = append(out, gontactFormFromPost(p))
	}
	return out, total, nil
}

func gfRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Admin menu ------------------------------------------------------------------------

// gontactMenu adds the Contact menu after Comments while the plugin is on (wpcf7_admin_menu()).
func (s *Server) gontactMenu(menu []*menuTop, u *store.User, screen string) []*menuTop {
	if !s.gontactOn() || !u.Can("edit_posts", nil) {
		return menu
	}
	top := &menuTop{Slug: "admin?page=wpcf7", Title: "Contact", ID: "toplevel_page_wpcf7", Icon: "dashicons-email"}
	subs := []struct{ slug, title string }{{"admin?page=wpcf7", "Contact Forms"}}
	if u.Can("publish_pages", nil) {
		subs = append(subs, struct{ slug, title string }{"admin?page=wpcf7-new", "Add Contact Form"})
	}
	if u.Can("publish_pages", nil) {
		subs = append(subs, struct{ slug, title string }{"admin?page=" + gontactEntriesPage, "Entries"})
	}
	if u.Can("manage_options", nil) {
		subs = append(subs, struct{ slug, title string }{"admin?page=wpcf7-integration", "Integration"})
	}
	for i, sd := range subs {
		ms := &menuSub{Slug: sd.slug, Title: template.HTML(sd.title), First: i == 0, Current: sd.slug == screen}
		if ms.Current {
			top.Current = true
		}
		top.Sub = append(top.Sub, ms)
	}
	at := len(menu)
	for i, m := range menu {
		if m.ID == "menu-comments" {
			at = i + 1
			break
		}
	}
	out := make([]*menuTop, 0, len(menu)+1)
	out = append(out, menu[:at]...)
	out = append(out, top)
	out = append(out, menu[at:]...)
	for i, m := range out {
		if m.Separator {
			continue
		}
		m.First = i == 0 || out[i-1].Separator
		m.Last = i == len(out)-1 || out[i+1].Separator
	}
	return out
}

// gontactAdminURL builds an admin?page=… URL.
func gontactAdminURL(page string, q url.Values) string {
	u := "/wp-admin/admin?page=" + page
	if len(q) > 0 {
		u += "&" + q.Encode()
	}
	return u
}
