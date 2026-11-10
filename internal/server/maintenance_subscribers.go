package server

import (
	"context"
	"crypto/hmac"
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
	tag, err := s.store.Pool.Exec(ctx, `INSERT INTO maintenance_subscribers (email, consent) VALUES ($1, $2) ON CONFLICT (email) DO NOTHING`, email, consent)
	if err != nil {
		http.Error(w, "Could not save the subscription.", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 1 && st.SubscribeNotify {
		if to := s.settings.String("admin_email"); to != "" {
			site := s.settings.String("blogname")
			s.sendMail(ctx, wpmail.Message{To: []string{to}, Subject: "[" + site + "] New subscriber",
				Body: "A visitor subscribed on the maintenance page of " + site + ":\n\n" + email + "\n\nAll subscribers: " +
					s.links(ctx).HomeURL("/wp-admin/admin?page=maintenance&tab=subscribers") + "\n"})
		}
	}
	http.Redirect(w, r, maintBack(r, url.Values{"mtnc_subscribed": {"1"}})+"#mtnc-subscribe", http.StatusSeeOther)
}

// maintUnlock checks the site password and remembers it in a cookie for the access link's lifetime.
func (s *Server) maintUnlock(w http.ResponseWriter, r *http.Request) {
	st := s.maintLoad()
	if r.Method != http.MethodPost || st.SitePassword == "" {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	w.Header().Set("Cache-Control", "no-store")
	if s.auth.VerifyNonce(r.PostFormValue("_wpnonce"), "mtnc_public", 0, "") == 0 ||
		!hmac.Equal([]byte(r.PostFormValue("password")), []byte(st.SitePassword)) {
		time.Sleep(300 * time.Millisecond) // slow down guessing a little
		http.Redirect(w, r, maintBack(r, url.Values{"mtnc_unlock": {"wrong"}}), http.StatusSeeOther)
		return
	}
	hours := st.SecretHours
	if hours <= 0 {
		hours = 24
	}
	http.SetCookie(w, &http.Cookie{Name: maintUnlockCookie, Value: s.maintToken("password", st.SitePassword), Path: "/",
		MaxAge: hours * 3600, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, maintBack(r, nil), http.StatusSeeOther)
}

type maintSubscriber struct {
	ID      int64
	Email   string
	Consent bool
	Created time.Time
}

func (s *Server) maintSubscribers(ctx context.Context) ([]maintSubscriber, error) {
	if err := maintEnsureTable(ctx, s.store); err != nil {
		return nil, err
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, email, consent, created_at FROM maintenance_subscribers ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []maintSubscriber
	for rows.Next() {
		var m maintSubscriber
		if err := rows.Scan(&m.ID, &m.Email, &m.Consent, &m.Created); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
