package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/i18n"
	"wpgo/internal/mail"
	"wpgo/internal/store"
)

// Pursae Forum membership mail: the email confirmation link and the small in-memory rate limits of
// the logged-out forms (register, resend).

// forumVerifyTTL is how long a confirmation link works.
const forumVerifyTTL = 48 * time.Hour

// forumSendVerify issues a fresh confirmation key for u (only its SHA-256 is stored, with the expiry),
// marks the account unverified and queues the email with the link.
func (s *Server) forumSendVerify(ctx context.Context, u *store.User) error {
	key := auth.RandomToken(20)
	sum := sha256.Sum256([]byte(key))
	if err := s.forumSetUserMeta(ctx, u.ID, map[string]any{
		forumUMetaUnverified: true,
		forumUMetaVerify:     map[string]any{"hash": hex.EncodeToString(sum[:]), "expires": time.Now().Add(forumVerifyTTL).Unix()},
	}); err != nil {
		return err
	}
	site := s.settings.String("blogname")
	if site == "" {
		site = "Pursae"
	}
	link := s.forumVerifyLink(ctx, u.ID, key)
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = u.Login
	}
	// The front end's catalog (the language the member registered in), like the forum's pages.
	cat := s.gooFrontCatalog(ctx)
	tf := func(format string, args ...string) string { return i18n.Sprintf(cat.T(format), args...) }
	body := tf("Hi %s,", name) + "\n\n" +
		tf("Thanks for joining the %1$s at %2$s. Please confirm your email address by opening this link:", s.forumLoad().Title, site) + "\n\n" +
		link + "\n\n" +
		cat.T("The link works for 48 hours. If you did not create this account, you can ignore this email.") + "\n"
	s.sendMail(ctx, mail.Message{Kind: "forum_verify", To: []string{u.Email}, Subject: tf("[%s] Confirm your email", site), Body: body})
	return nil
}

// forumVerifyLink is the confirmation URL of a key.
func (s *Server) forumVerifyLink(ctx context.Context, userID int64, key string) string {
	return s.forumURL(ctx, "verify") + "?u=" + strconv.FormatInt(userID, 10) + "&k=" + key
}

// forumCheckVerify reports whether key is u's current, unexpired confirmation key (constant-time).
func forumCheckVerify(u *store.User, key string, now time.Time) bool {
	if u == nil || key == "" || len(key) > 200 {
		return false
	}
	m, _ := u.Meta[forumUMetaVerify].(map[string]any)
	if m == nil {
		return false
	}
	want, _ := m["hash"].(string)
	exp := forumMetaInt64(m, "expires")
	if want == "" || exp <= 0 || now.Unix() > exp {
		return false
	}
	sum := sha256.Sum256([]byte(key))
	got := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// forumLimiter is a fixed-window counter per key (register and resend limits). Process-local, like
// the other front-end limiters; a restart forgets it.
var forumLimiter = struct {
	sync.Mutex
	hits map[string]*forumWindow
}{hits: map[string]*forumWindow{}}

type forumWindow struct {
	start time.Time
	n     int
}

// forumAllow counts one hit for key and reports whether it stays within limit per window.
func forumAllow(key string, limit int, window time.Duration) bool {
	now := time.Now()
	forumLimiter.Lock()
	defer forumLimiter.Unlock()
	if len(forumLimiter.hits) > 20000 { // drop expired windows now and then
		for k, w := range forumLimiter.hits {
			if now.Sub(w.start) > time.Hour {
				delete(forumLimiter.hits, k)
			}
		}
	}
	w := forumLimiter.hits[key]
	if w == nil || now.Sub(w.start) > window {
		w = &forumWindow{start: now}
		forumLimiter.hits[key] = w
	}
	w.n++
	return w.n <= limit
}

// forumLimiterReset forgets every counter (tests).
func forumLimiterReset() {
	forumLimiter.Lock()
	clear(forumLimiter.hits)
	forumLimiter.Unlock()
}
