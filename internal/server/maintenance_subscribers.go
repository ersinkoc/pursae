package server

import (
	"context"
	"crypto/hmac"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"html"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	wpmail "wpgo/internal/mail"
	"wpgo/internal/store"
)

// The maintenance page's two visitor forms, admin-post.php style (admin_post_nopriv_*): the
// subscribe form (e-mail addresses kept in maintenance_subscribers, listed and exported on the
// Subscribers tab) and the site password form of the original's Pro "Direct Access Password".
// No IP address or user agent is stored with a subscriber; only what the visitor typed, the time
// and whether the consent box was ticked.

// maintEnsureTable creates the subscribers table. It belongs to the plugin, so it is created when
// the plugin is activated (and on first use), not by a core migration.
func maintEnsureTable(ctx context.Context, st *store.Store) error {
	_, err := st.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS maintenance_subscribers (
		id         bigserial PRIMARY KEY,
		email      text NOT NULL UNIQUE,
		consent    boolean NOT NULL DEFAULT false,
		created_at timestamptz NOT NULL DEFAULT now()
	)`)
	if err != nil {
		return err
	}
	// pending: waiting for the double opt-in link; notified_at: the "site is open" mail went out.
	_, err = st.Pool.Exec(ctx, `ALTER TABLE maintenance_subscribers
		ADD COLUMN IF NOT EXISTS pending boolean NOT NULL DEFAULT false,
		ADD COLUMN IF NOT EXISTS notified_at timestamptz`)
	return err
}

var maintSubscribeErrors = map[string]string{
	"email":   "Please enter a valid e-mail address.",
	"consent": "Please tick the consent box to subscribe.",
	"expired": "The form expired. Please try again.",
	"busy":    "Too many sign-ups from your network. Please try again in a minute.",
}

// maintBack is where a form returns to: the page it was sent from, with flags added.
func maintBack(r *http.Request, flags url.Values) string {
	back := safeRedirect(r.PostFormValue("_wp_http_referer"), "/")
	u, err := url.Parse(back)
	if err != nil {
		return "/"
	}
	q := u.Query()
	for _, k := range []string{"mtnc_subscribed", "mtnc_error", "mtnc_unlock"} {
		q.Del(k)
	}
	for k, vs := range flags {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) maintSubscribe(w http.ResponseWriter, r *http.Request) {
	st := s.maintLoad()
	if r.Method != http.MethodPost || !st.Subscribe {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	w.Header().Set("Cache-Control", "no-store")
	fail := func(code string) {
		http.Redirect(w, r, maintBack(r, url.Values{"mtnc_error": {code}})+"#mtnc-subscribe", http.StatusSeeOther)
	}
	if s.auth.VerifyNonce(r.PostFormValue("_wpnonce"), "mtnc_public", 0, "") == 0 {
		fail("expired")
		return
	}
	if r.PostFormValue("mtnc_website") != "" { // the honeypot: a bot; pretend it worked
		http.Redirect(w, r, maintBack(r, url.Values{"mtnc_subscribed": {"1"}})+"#mtnc-subscribe", http.StatusSeeOther)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		fail("email")
		return
	}
	consent := r.PostFormValue("gdpr") == "1"
	if st.GDPR && !consent {
		fail("consent")
		return
	}
	// One nonce is good for a day and the page is public: without a limit a script could fill the
	// table and, with "notify", the administrator's mailbox.
	if !gooikeAllow("mtnc:"+gooikeTruncateIP(s.requestIP(r)), 5) {
		fail("busy")
		return
	}
	ctx := r.Context()
	if err := maintEnsureTable(ctx, s.store); err != nil {
		http.Error(w, "Could not save the subscription.", http.StatusInternalServerError)
		return
	}
	tag, err := s.store.Pool.Exec(ctx, `INSERT INTO maintenance_subscribers (email, consent, pending) VALUES ($1, $2, $3) ON CONFLICT (email) DO NOTHING`, email, consent, st.DoubleOptIn)
	if err != nil {
		http.Error(w, "Could not save the subscription.", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 1 {
		if st.DoubleOptIn { // the address counts, and the admin hears of it, once its owner clicks the link
			site := s.settings.String("blogname")
			s.sendMail(ctx, wpmail.Message{To: []string{email}, Subject: "[" + site + "] Please confirm your subscription",
				Body: "Please confirm that you want to hear from " + site + " when it opens:\n\n" + s.maintSubURL(ctx, "mtnc_confirm", email) +
					"\n\nIf you did not ask for this, ignore this message and nothing will be sent to you.\n"})
		} else {
			s.maintTellAdmin(ctx, st, email)
		}
	}
	http.Redirect(w, r, maintBack(r, url.Values{"mtnc_subscribed": {"1"}})+"#mtnc-subscribe", http.StatusSeeOther)
}

// maintTellAdmin tells the site admin about a new, confirmed subscriber (when the setting is on).
func (s *Server) maintTellAdmin(ctx context.Context, st maintSettings, email string) {
	if !st.SubscribeNotify {
		return
	}
	if to := s.settings.String("admin_email"); to != "" {
		site := s.settings.String("blogname")
		s.sendMail(ctx, wpmail.Message{To: []string{to}, Subject: "[" + site + "] New subscriber",
			Body: "A visitor subscribed on the maintenance page of " + site + ":\n\n" + email + "\n\nAll subscribers: " +
				s.links(ctx).HomeURL("/wp-admin/admin?page=maintenance&tab=subscribers") + "\n"})
	}
}

// maintSubURL is a signed link of a subscriber mail (confirm or unsubscribe): the token binds the
// action and the address to the site secret, so nobody can confirm or remove another address.
func (s *Server) maintSubURL(ctx context.Context, action, email string) string {
	q := url.Values{"action": {action}, "e": {email}, "t": {s.maintToken(action, email)}}
	return s.links(ctx).HomeURL("/wp-admin/admin-post?" + q.Encode())
}

// maintSubLink handles the confirm and unsubscribe links. Confirming is idempotent (a GET); the
// unsubscribe link shows a button first, because mail scanners follow links.
func (s *Server) maintSubLink(w http.ResponseWriter, r *http.Request, action string) {
	_ = r.ParseForm()
	email := r.Form.Get("e")
	w.Header().Set("Cache-Control", "no-store")
	if !hmac.Equal([]byte(r.Form.Get("t")), []byte(s.maintToken(action, email))) {
		http.Error(w, "This link is not valid.", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	if err := maintEnsureTable(ctx, s.store); err != nil {
		http.Error(w, "Could not save your choice.", http.StatusInternalServerError)
		return
	}
	page := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>%s</title><body style="font:16px/1.5 sans-serif;max-width:32em;margin:15vh auto;padding:0 1em"><p>%s</p>`, html.EscapeString(s.settings.String("blogname")), msg)
	}
	switch action {
	case "mtnc_confirm":
		tag, _ := s.store.Pool.Exec(ctx, `UPDATE maintenance_subscribers SET pending = false WHERE email = $1 AND pending`, email)
		if tag.RowsAffected() == 1 {
			s.maintTellAdmin(ctx, s.maintLoad(), email)
		}
		page("Thank you! Your subscription is confirmed.")
	case "mtnc_unsubscribe":
		if r.Method == http.MethodPost {
			_, _ = s.store.Pool.Exec(ctx, `DELETE FROM maintenance_subscribers WHERE email = $1`, email)
			page("You have been unsubscribed and will not get any more e-mail from us.")
			return
		}
		page(fmt.Sprintf(`Unsubscribe <strong>%s</strong>?</p><form method="post"><input type="hidden" name="e" value="%s"><input type="hidden" name="t" value="%s"><button type="submit">Unsubscribe</button></form>`,
			html.EscapeString(email), html.EscapeString(email), html.EscapeString(r.Form.Get("t"))))
	}
}

// maintNotifyOpen mails the subscribers that the site is open, when "notify on open" is on.
func (s *Server) maintNotifyOpen(ctx context.Context, st maintSettings) {
	if !st.NotifyOnOpen || !st.Subscribe {
		return
	}
	if _, err := s.maintNotifySubscribers(ctx, st.NotifySubject, st.NotifyBody); err != nil {
		log.Printf("maintenance: notify subscribers: %v", err)
	}
}

// maintNotifySubscribers queues one mail per confirmed subscriber that has not been told yet, each
// with its own unsubscribe link, and returns how many it queued. The
// queue hands them to Pursae SMTP at its own pace.
func (s *Server) maintNotifySubscribers(ctx context.Context, subject, body string) (int, error) {
	if err := maintEnsureTable(ctx, s.store); err != nil {
		return 0, err
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT email FROM maintenance_subscribers WHERE NOT pending AND notified_at IS NULL ORDER BY id`)
	if err != nil {
		return 0, err
	}
	var emails []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			rows.Close()
			return 0, err
		}
		emails = append(emails, e)
	}
	rows.Close()
	if rows.Err() != nil {
		return 0, rows.Err()
	}
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(s.maintMailVars(ctx, subject))
	body = s.maintMailVars(ctx, body)
	for _, e := range emails {
		unsub := s.maintSubURL(ctx, "mtnc_unsubscribe", e)
		s.sendMail(ctx, wpmail.Message{To: []string{e}, Subject: subject,
			Kind: "maintenance_open",
			Body: body + "\n\n--\nYou get this because you asked to be told when the site opens. Unsubscribe: " + unsub + "\n"})
		_, _ = s.store.Pool.Exec(ctx, `UPDATE maintenance_subscribers SET notified_at = now() WHERE email = $1`, e)
	}
	return len(emails), nil
}

func (s *Server) maintMailVars(ctx context.Context, v string) string {
	return strings.NewReplacer("%siteurl%", s.links(ctx).HomeURL("/")).Replace(s.maintVars(v))
}

// maintPasswordHash stores the site password as a bcrypt hash.
func maintPasswordHash(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

// maintPasswordOK compares a typed password with the stored one: a bcrypt hash, or (settings saved
// before passwords were hashed) the plain text, which maintUnlock upgrades on the first good use.
func maintPasswordOK(stored, typed string) (ok, legacy bool) {
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(typed)) == nil, false
	}
	return stored != "" && hmac.Equal([]byte(typed), []byte(stored)), true
}

// maintUnlockPerMinute is how many site-password attempts one address gets per minute.
const maintUnlockPerMinute = 10

// maintUnlock checks the site password and remembers it in a cookie for the access link's lifetime.
func (s *Server) maintUnlock(w http.ResponseWriter, r *http.Request) {
	st := s.maintLoad()
	if r.Method != http.MethodPost || st.SitePassword == "" {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	w.Header().Set("Cache-Control", "no-store")
	ok, legacy := maintPasswordOK(st.SitePassword, r.PostFormValue("password"))
	if !gooikeAllow("mtnc-unlock:"+gooikeTruncateIP(s.requestIP(r)), maintUnlockPerMinute) { // guesses per address and minute
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many password attempts. Try again in a minute.", http.StatusTooManyRequests)
		return
	}
	if s.auth.VerifyNonce(r.PostFormValue("_wpnonce"), "mtnc_public", 0, "") == 0 || !ok {
		time.Sleep(300 * time.Millisecond) // slow down guessing a little
		http.Redirect(w, r, maintBack(r, url.Values{"mtnc_unlock": {"wrong"}}), http.StatusSeeOther)
		return
	}
	if legacy { // a plain-text password from an older version: keep only its hash from now on
		if h, err := maintPasswordHash(st.SitePassword); err == nil {
			st.SitePassword = h
			_ = s.maintSave(context.WithoutCancel(r.Context()), st)
		}
	}
	hours := st.SecretHours
	if hours <= 0 {
		hours = 24
	}
	http.SetCookie(w, &http.Cookie{Name: maintUnlockCookie, Value: s.maintToken("password", st.SitePassword), Path: "/",
		MaxAge: hours * 3600, HttpOnly: true, Secure: s.httpsRequest(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, maintBack(r, nil), http.StatusSeeOther)
}

type maintSubscriber struct {
	ID       int64
	Email    string
	Consent  bool
	Pending  bool
	Notified bool
	Created  time.Time
}

func (s *Server) maintSubscribers(ctx context.Context) ([]maintSubscriber, error) {
	if err := maintEnsureTable(ctx, s.store); err != nil {
		return nil, err
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, email, consent, pending, notified_at IS NOT NULL, created_at FROM maintenance_subscribers ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []maintSubscriber
	for rows.Next() {
		var m maintSubscriber
		if err := rows.Scan(&m.ID, &m.Email, &m.Consent, &m.Pending, &m.Notified, &m.Created); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
