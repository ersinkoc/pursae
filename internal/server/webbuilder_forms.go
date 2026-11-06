package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"wpgo/internal/mail"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Pursae Web Builder: the public form endpoint POST web-builder/v2/forms/{post}/{section}
// (docs/developer/web-builder-contract.md §3.5). A `form` section of a published builder page posts
// here, with fetch and Accept: application/json from the site runtime (→ JSON) or as a plain form
// post without JavaScript (→ 303 back to the page).
//
// Nothing in the request is trusted: the fields come from the stored doc (webbuilder.FindForm), the
// values are validated against that schema and every other field is dropped. A filled honeypot, a
// submit faster than webbuilder.FormMinMillis or a disallowed word (Settings › Discussion) is spam:
// the visitor sees "sent" and nothing is mailed. Each address gets wbFormLimit attempts per
// wbFormWindow. When Pursae Contact Form is active with reCAPTCHA or Turnstile keys, the matching
// token is required. The message goes to admin_email through the core mailer (the wp_mail job).
//
// The route needs no login and no nonce: a cookie without X-WP-Nonce is a visitor to the REST API
// (restAuthUser), so a logged-in browser posting the form is not a CSRF vector and needs nothing more.

const (
	wbFormLimit   = 10               // attempts per address and window
	wbFormWindow  = 10 * time.Minute // fixed window of the per-address limit
	wbFormSentMsg = "Thank you, your message was sent."
	wbFormBadMsg  = "Please check the highlighted fields."
	wbFormFailMsg = "Sorry, the message could not be sent."
	wbFormTitle   = 200 // runes of the mail subject's title part
)

// registerWebBuilderForms adds the public form route (the handler checks wbOn on every request).
func (s *Server) registerWebBuilderForms() {
	s.rest.register(webbuilder.RESTNamespace, `/web-builder/v2/forms/(?P<id>[\d]+)/(?P<section>[A-Za-z0-9_-]{1,64})`,
		map[string]restHandler{http.MethodPost: s.wbRESTForm}, nil)
}

// --- rate limit --------------------------------------------------------------------------------

// wbFormLimiter is a fixed-window counter per address, one per Server.
type wbFormLimiter struct {
	mu    sync.Mutex
	hits  map[string]*wbFormHits
	swept time.Time
}

type wbFormHits struct {
	start time.Time
	n     int
}

var wbFormLimiters sync.Map // *Server → *wbFormLimiter

func (s *Server) wbFormLimiter() *wbFormLimiter {
	if v, ok := wbFormLimiters.Load(s); ok {
		return v.(*wbFormLimiter)
	}
	v, _ := wbFormLimiters.LoadOrStore(s, &wbFormLimiter{hits: map[string]*wbFormHits{}})
	return v.(*wbFormLimiter)
}

// allow counts one attempt of key; when the limit is spent it returns false and the time until the
// window ends.
func (l *wbFormLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.swept) > wbFormWindow {
		for k, h := range l.hits {
			if now.Sub(h.start) >= wbFormWindow {
				delete(l.hits, k)
			}
		}
		l.swept = now
	}
	h := l.hits[key]
	if h == nil || now.Sub(h.start) >= wbFormWindow {
		h = &wbFormHits{start: now}
		l.hits[key] = h
	}
	h.n++
	if h.n > wbFormLimit {
		return false, h.start.Add(wbFormWindow).Sub(now)
	}
	return true, 0
}

// wbFormIPKey is the limiter key of an address: IPv4 as is, IPv6 by its /64 (one host's range).
func wbFormIPKey(addr string) string {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return addr
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}

// --- handler -----------------------------------------------------------------------------------

// wbFormOutcome is how a submit ended.
type wbFormOutcome int

const (
	wbFormSent wbFormOutcome = iota
	wbFormInvalid
	wbFormFailed
)

// wbRESTForm handles POST web-builder/v2/forms/{id}/{section}.
func (s *Server) wbRESTForm(c *gin.Context, r *restRequest) {
	if !s.wbOn() {
		restErr(c, http.StatusNotFound, "rest_no_route", "No route was found matching the URL and request method.")
		return
	}
	c.Header("Cache-Control", "no-store")
	wantJSON := strings.Contains(strings.ToLower(c.GetHeader("Accept")), "application/json")

	// Count the attempt before any lookup, so probing for forms costs the same as posting to one.
	if ok, wait := s.wbFormLimiter().allow(wbFormIPKey(clientIP(c)), time.Now()); !ok {
		secs := int((wait + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		c.Header("Retry-After", strconv.Itoa(secs))
		restErr(c, http.StatusTooManyRequests, webbuilder.ErrRateLimited, "Too many messages from this address. Please try again later.",
			map[string]any{"retryAfter": secs})
		return
	}

	p, schema, ok := s.wbFormTarget(c, r)
	if !ok {
		return
	}
	values, ok := wbFormValues(c)
	if !ok {
		return
	}
	finish := func(o wbFormOutcome, fields map[string]string) {
		s.wbFormRespond(c, p, schema.SectionID, wantJSON, o, fields)
	}

	if wbFormSpam(values) || s.wbFormDisallowed(values, clientIP(c), c.Request.UserAgent()) {
		finish(wbFormSent, nil) // spam looks like a success and is not mailed
		return
	}
	clean, errs := schema.Validate(values)
	if len(errs) > 0 {
		finish(wbFormInvalid, errs)
		return
	}
	if reason := s.wbFormCaptcha(c.Request.Context(), values, clientIP(c)); reason != "" {
		finish(wbFormInvalid, map[string]string{"captcha": reason})
		return
	}
	if err := s.wbFormMail(c.Request.Context(), p, s.wbFormPageURL(c, p), schema, clean, clientIP(c)); err != nil {
		slog.Error("web builder form: mail", "post", p.ID, "section", schema.SectionID, "err", err)
		finish(wbFormFailed, nil)
		return
	}
	finish(wbFormSent, nil)
}

// wbFormTarget loads the published page and the stored schema of the {section} form. Anything that
// is not a visible form section of a published, non-detached builder page or of a published
// Theme Builder template (contract §11.9) answers 404.
func (s *Server) wbFormTarget(c *gin.Context, r *restRequest) (*store.Post, webbuilder.FormSchema, bool) {
	notFound := func() (*store.Post, webbuilder.FormSchema, bool) {
		restErr(c, http.StatusNotFound, webbuilder.ErrNotFound, "Invalid form.")
		return nil, webbuilder.FormSchema{}, false
	}
	p, err := s.store.GetPost(c, r.ID())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return notFound()
		}
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the post.")
		return nil, webbuilder.FormSchema{}, false
	}
	if !s.wbEnabledType(p.Type) && !wbIsTemplate(p) {
		restErr(c, http.StatusNotFound, webbuilder.ErrNotBuilder, "Pursae Web Builder is not enabled for this post type.")
		return nil, webbuilder.FormSchema{}, false
	}
	if p.Status != "publish" || s.passwordRequired(c, p) {
		return notFound()
	}
	row, err := s.store.WebBuilder().Doc(c, p.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return notFound()
		}
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the page.")
		return nil, webbuilder.FormSchema{}, false
	}
	// A detached page (content edited outside the builder, §6.3) shows the theme fallback: its
	// forms are not on the page any more. Templates have no core editor and never detach.
	if !wbIsTemplate(p) && (row.Detached || (row.ContentHash != "" && row.ContentHash != store.WebBuilderContentHash(p.Content))) {
		return notFound()
	}
	var doc webbuilder.DocState
	if err := json.Unmarshal(row.Doc, &doc); err != nil {
		slog.Warn("web builder form: stored doc", "post", p.ID, "err", err)
		return notFound()
	}
	schema, ok := webbuilder.FindForm(&doc, r.Params["section"])
	if !ok {
		return notFound()
	}
	return p, schema, true
}

// wbFormValues reads the posted fields: application/x-www-form-urlencoded or multipart/form-data
// (files are ignored), at most webbuilder.MaxFormBodyBytes. The first value of each name wins.
func wbFormValues(c *gin.Context) (map[string]string, bool) {
	limit := int64(webbuilder.MaxFormBodyBytes)
	if c.Request.ContentLength > limit {
		restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, "The request body is too large.", map[string]any{"limit": limit})
		return nil, false
	}
	mt, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if mt != "application/x-www-form-urlencoded" && mt != "multipart/form-data" {
		restErr(c, http.StatusUnsupportedMediaType, "rest_invalid_param", "Send the form as application/x-www-form-urlencoded or multipart/form-data.")
		return nil, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	var form url.Values
	if mt == "multipart/form-data" {
		if err := c.Request.ParseMultipartForm(limit); err != nil {
			return wbFormBodyErr(c, err)
		}
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
			form = c.Request.MultipartForm.Value
		}
	} else {
		if err := c.Request.ParseForm(); err != nil {
			return wbFormBodyErr(c, err)
		}
		form = c.Request.PostForm
	}
	out := make(map[string]string, len(form))
	for k, v := range form {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out, true
}

func wbFormBodyErr(c *gin.Context, err error) (map[string]string, bool) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, "The request body is too large.",
			map[string]any{"limit": webbuilder.MaxFormBodyBytes})
	} else {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "The form data could not be read.")
	}
	return nil, false
}

// wbFormSpam: a filled honeypot, or a timing the runtime measured below webbuilder.FormMinMillis.
// A missing timing (a plain post without JavaScript) is not spam by itself.
func wbFormSpam(values map[string]string) bool {
	if strings.TrimSpace(values[webbuilder.FormHoneypot]) != "" {
		return true
	}
	if t := strings.TrimSpace(values[webbuilder.FormTiming]); t != "" {
		ms, err := strconv.ParseInt(t, 10, 64)
		if err != nil || ms < webbuilder.FormMinMillis {
			return true
		}
	}
	return false
}

// wbFormDisallowed checks the Discussion screen's disallowed keys (the comment blocklist, which
// Pursae Contact Form also applies) against the values, the address and the user agent.
func (s *Server) wbFormDisallowed(values map[string]string, ip, ua string) bool {
	keys := s.settings.String("disallowed_keys")
	if strings.TrimSpace(keys) == "" {
		return false
	}
	parts := make([]string, 0, len(webbuilder.FormFields)+2)
	for _, f := range webbuilder.FormFields {
		parts = append(parts, values[f])
	}
	hay := strings.ToLower(strings.Join(append(parts, ip, ua), "\n"))
	for _, k := range strings.Split(keys, "\n") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" && strings.Contains(hay, k) {
			return true
		}
	}
	return false
}

// wbFormCaptcha verifies the captcha Pursae Contact Form is configured with (its keys, its
// verifier). It returns "" when there is nothing to check or the token is valid, else
// webbuilder.FormRequired (no token) or webbuilder.FormInvalid.
func (s *Server) wbFormCaptcha(ctx context.Context, values map[string]string, ip string) string {
	if !s.gontactOn() {
		return ""
	}
	type check struct{ secret, site, field, endpoint string }
	for _, k := range []check{
		{"gontact_turnstile_secret", "gontact_turnstile_sitekey", "cf-turnstile-response", "https://challenges.cloudflare.com/turnstile/v0/siteverify"},
		{"gontact_recaptcha_secret", "gontact_recaptcha_sitekey", "g-recaptcha-response", "https://www.google.com/recaptcha/api/siteverify"},
	} {
		secret := s.settings.String(k.secret)
		if secret == "" || s.settings.String(k.site) == "" {
			continue
		}
		token := strings.TrimSpace(values[k.field])
		if token == "" {
			return webbuilder.FormRequired
		}
		score, ok := s.gontactVerifyCaptcha(ctx, k.endpoint, secret, token, ip)
		if !ok || score < 0.5 { // Pursae Contact Form's reCAPTCHA threshold; Turnstile scores 1
			return webbuilder.FormInvalid
		}
	}
	return ""
}

// wbFormMail queues the message to admin_email (the wp_mail job of the core mailer). link is the
// page the form was sent from (wbFormPageURL).
func (s *Server) wbFormMail(ctx context.Context, p *store.Post, link string, schema webbuilder.FormSchema, clean map[string]string, ip string) error {
	to := strings.TrimSpace(s.settings.String("admin_email"))
	if to == "" {
		return errors.New("no admin_email")
	}
	blog := wbFormLine(s.settings.String("blogname"))
	title := wbFormLine(schema.Title)
	if title == "" {
		title = wbFormLine(p.Title)
	}
	if title == "" {
		title = "Form"
	}
	if utf8.RuneCountInString(title) > wbFormTitle {
		title = string([]rune(title)[:wbFormTitle]) + "…"
	}

	var b strings.Builder
	b.WriteString("New message from the form \"" + title + "\" on " + defaultStr(wbFormLine(p.Title), "(no title)") + "\n")
	b.WriteString(link + "\n\n")
	for _, f := range schema.Fields {
		v, ok := clean[f.Name]
		if !ok {
			continue
		}
		label := defaultStr(wbFormLine(f.Label), f.Name)
		if f.Type == "textarea" {
			b.WriteString(label + ":\n" + v + "\n\n")
		} else {
			b.WriteString(label + ": " + v + "\n")
		}
	}
	b.WriteString("\n--\nSent by Pursae Web Builder from " + defaultStr(blog, s.links(ctx).HomeURL("/")) + "\n")
	if ip != "" {
		b.WriteString("IP address: " + ip + "\n")
	}

	subject := title
	if blog != "" {
		subject = "[" + blog + "] " + title
	}
	msg := mail.Message{To: []string{to}, Subject: subject, Body: b.String(), ReplyTo: clean["email"]}
	if _, err := s.mailer().Build(msg); err != nil {
		return err
	}
	return s.queueJob(context.WithoutCancel(ctx), "wp_mail", msg, time.Time{})
}

// wbFormLine is v on one line: control characters and line breaks become spaces (header safety).
func wbFormLine(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 {
			return ' '
		}
		return r
	}, v)
	return strings.Join(strings.Fields(v), " ")
}

// wbFormRespond answers JSON for the runtime and 303 back to the page for a plain form post.
func (s *Server) wbFormRespond(c *gin.Context, p *store.Post, section string, wantJSON bool, o wbFormOutcome, fields map[string]string) {
	if wantJSON {
		switch o {
		case wbFormInvalid:
			restErr(c, http.StatusUnprocessableEntity, webbuilder.ErrFormInvalid, wbFormBadMsg, map[string]any{"fields": fields})
		case wbFormFailed:
			restErr(c, http.StatusInternalServerError, "wb_form_failed", wbFormFailMsg)
		default:
			c.JSON(http.StatusOK, webbuilder.FormResponse{OK: true, Message: wbFormSentMsg})
		}
		return
	}
	state := "sent"
	if o != wbFormSent {
		state = "error"
	}
	c.Redirect(http.StatusSeeOther, wbFormBackURL(s.wbFormPageURL(c, p), state, section))
}

// wbFormPageURL is the page a form was sent from: the builder page itself, or for a form inside a
// Theme Builder template (shown on many pages) the same-origin Referer, else the home page.
func (s *Server) wbFormPageURL(c *gin.Context, p *store.Post) string {
	lb := s.links(c)
	if !wbIsTemplate(p) {
		return lb.Permalink(p)
	}
	home := lb.HomeURL("/")
	ref, err := url.Parse(c.GetHeader("Referer"))
	if err != nil || (ref.Scheme != "http" && ref.Scheme != "https") || ref.Host == "" || ref.User != nil {
		return home
	}
	same := strings.EqualFold(ref.Host, c.Request.Host)
	if hu, err := url.Parse(home); err == nil && strings.EqualFold(ref.Host, hu.Host) {
		same = true
	}
	if !same {
		return home
	}
	path := ref.RequestURI()
	if safeRedirect(path, "") == "" { // a path on this site, nothing a browser would read as another host
		return home
	}
	return ref.Scheme + "://" + ref.Host + path
}

// wbFormBackURL is the page with ?wb_form=<state> and #<section>.
func wbFormBackURL(link, state, section string) string {
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	q := u.Query()
	q.Set(webbuilder.FormSentQuery, state)
	u.RawQuery = q.Encode()
	u.Fragment = section
	return u.String()
}
