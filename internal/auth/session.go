package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	CookieName      = "wpgo_logged_in"
	SessionShort    = 2 * 24 * time.Hour  // WordPress default without "Remember Me"
	SessionRemember = 14 * 24 * time.Hour // WordPress default with "Remember Me"

	// Login throttling (throttle.go): this many failures from one client network (ClientNet: an IPv4
	// address, an IPv6 /64) within the window block further attempts from it.
	MaxFailedLogins = 5
	FailedWindow    = 15 * time.Minute
	// MaxFailedLoginsPerLogin failures at one account (from every network) within FailedWindow put it
	// "under attack": networks that have not signed in to it recently are slowed down and get fewer
	// guesses there (BeginLogin). It refuses nobody by itself, so failing at an account from many
	// addresses cannot lock its owner out.
	MaxFailedLoginsPerLogin = 20
)

var ErrNoSession = errors.New("no session")

type Session struct {
	ID        int64
	UserID    int64
	Token     string // raw cookie token; only known for the current request
	ExpiresAt time.Time
}

type Manager struct {
	pool   *pgxpool.Pool
	secret []byte
	// Idle, when set, returns the idle timeout: a session unused for longer ends (0: never).
	Idle func() time.Duration
}

func NewManager(pool *pgxpool.Pool, secret string) *Manager {
	return &Manager{pool: pool, secret: []byte(secret)}
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Create starts a session and returns the cookie token.
func (m *Manager) Create(ctx context.Context, userID int64, remember bool, ip, ua string) (*Session, error) {
	ttl := SessionShort
	if remember {
		ttl = SessionRemember
	}
	token := RandomToken(32)
	s := &Session{UserID: userID, Token: token, ExpiresAt: time.Now().Add(ttl)}
	err := m.pool.QueryRow(ctx, `INSERT INTO sessions(user_id, token_hash, ip, user_agent, expires_at)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`, userID, hashToken(token), ip, ua, s.ExpiresAt).Scan(&s.ID)
	if err != nil {
		return nil, err
	}
	// Bookkeeping only: the login itself succeeded, but a failure should still be visible.
	if _, err := m.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID); err != nil {
		slog.Debug("auth: last_login_at update", "user_id", userID, "err", err)
	}
	return s, nil
}

// Lookup resolves a cookie token to a live session and refreshes last_seen_at at most once a minute.
func (m *Manager) Lookup(ctx context.Context, token string) (*Session, error) {
	return m.lookup(ctx, token, true)
}

// LookupPassive resolves a session like Lookup but does not count the request as activity: for
// background polling (the heartbeat) that must not keep an unattended session from going idle.
func (m *Manager) LookupPassive(ctx context.Context, token string) (*Session, error) {
	return m.lookup(ctx, token, false)
}

func (m *Manager) lookup(ctx context.Context, token string, touch bool) (*Session, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	s := &Session{Token: token}
	var lastSeen time.Time
	err := m.pool.QueryRow(ctx, `SELECT id, user_id, expires_at, last_seen_at FROM sessions
		WHERE token_hash = $1 AND expires_at > now()`, hashToken(token)).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	if m.Idle != nil {
		if idle := m.Idle(); idle > 0 && time.Since(lastSeen) > idle {
			// The session is refused either way; a failed delete leaves the row for the expiry cleanup.
			if _, err := m.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, s.ID); err != nil {
				slog.Debug("auth: session idle delete", "session", s.ID, "err", err)
			}
			return nil, ErrNoSession
		}
	}
	if touch && time.Since(lastSeen) > time.Minute {
		if _, err := m.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1`, s.ID); err != nil {
			slog.Debug("auth: session touch", "session", s.ID, "err", err)
		}
	}
	return s, nil
}

func (m *Manager) Destroy(ctx context.Context, token string) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

// DestroyOthers ends every session of the user except the given one ("Log Out Everywhere Else").
func (m *Manager) DestroyOthers(ctx context.Context, userID, keepID int64) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id <> $2`, userID, keepID)
	return err
}

// Nonce is the wp_create_nonce() counterpart: an HMAC over the action, user,
// session and a 12-hour tick. A nonce stays valid for two ticks (12 to 24 hours).
func (m *Manager) Nonce(action string, userID int64, sessionToken string) string {
	return m.nonceAt(action, userID, sessionToken, nonceTick(time.Now()))
}

// VerifyNonce returns 1 when the nonce was made in the current tick, 2 for the previous one, 0 when invalid.
func (m *Manager) VerifyNonce(nonce, action string, userID int64, sessionToken string) int {
	if nonce == "" {
		return 0
	}
	tick := nonceTick(time.Now())
	if hmac.Equal([]byte(nonce), []byte(m.nonceAt(action, userID, sessionToken, tick))) {
		return 1
	}
	if hmac.Equal([]byte(nonce), []byte(m.nonceAt(action, userID, sessionToken, tick-1))) {
		return 2
	}
	return 0
}

func (m *Manager) nonceAt(action string, userID int64, token string, tick int64) string {
	mac := hmac.New(sha256.New, m.secret)
	fmt.Fprintf(mac, "%d|%s|%d|%s", tick, action, userID, token)
	return hex.EncodeToString(mac.Sum(nil))[:10]
}

func nonceTick(t time.Time) int64 {
	const life = 24 * 60 * 60
	return int64(float64(t.Unix()) / (life / 2))
}

// Throttled reports whether the client network of ip has too many recent failed logins (completed ones:
// attempts still being checked are not failures yet).
func (m *Manager) Throttled(ctx context.Context, ip string) (bool, error) {
	var n int
	err := m.pool.QueryRow(ctx, `SELECT count(*) FROM login_attempts
		WHERE ip = $1 AND NOT success AND NOT pending AND attempted_at > now() - $2::interval`,
		ClientNet(ip), fmt.Sprintf("%d seconds", int(FailedWindow.Seconds()))).Scan(&n)
	return n >= MaxFailedLogins, err
}

// BeginAttempt is BeginLogin for callers that only need its refusal: it does not report the wait an
// account under attack asks for, so sign-in forms use BeginLogin.
func (m *Manager) BeginAttempt(ctx context.Context, ip, login string) (id int64, throttled bool, err error) {
	a, err := m.BeginLogin(ctx, ip, login)
	return a.ID, a.Throttled, err
}

// ProvisionalAttempt stores a login attempt as a failure before its password is checked, like BeginLogin
// but without the core thresholds: Pursae Security counts it against its own limits.
func (m *Manager) ProvisionalAttempt(ctx context.Context, ip, login string) (int64, error) {
	var id int64
	err := m.pool.QueryRow(ctx, `INSERT INTO login_attempts(ip, login, success, pending) VALUES ($1,$2,false,true) RETURNING id`, ClientNet(ip), login).Scan(&id)
	return id, err
}

// AttemptFailed marks an attempt stored by BeginLogin, BeginAppPasswordAttempt or ProvisionalAttempt as a
// completed failure: its password was checked and was wrong. Only completed failures count towards lockouts (Throttled, Pursae
// Security); a pending attempt only counts against requests running at the same time.
func (m *Manager) AttemptFailed(ctx context.Context, id int64) {
	if id != 0 {
		_, _ = m.pool.Exec(ctx, `UPDATE login_attempts SET pending = false, updated_at = now() WHERE id = $1`, id)
	}
}

// DropAttempt removes an attempt stored by BeginLogin, BeginAppPasswordAttempt or ProvisionalAttempt that
// turned out not to be a failed guess: a correct password (the sign-in records its own success) or a request refused unchecked.
func (m *Manager) DropAttempt(ctx context.Context, id int64) {
	if id != 0 {
		_, _ = m.pool.Exec(ctx, `DELETE FROM login_attempts WHERE id = $1`, id)
	}
}

// AttemptSucceeded turns the attempt BeginLogin stored into a success (which makes the network known to
// the account, LoginPressure) and clears this login's earlier failures from the network (RecordAttempt
// with success).
func (m *Manager) AttemptSucceeded(ctx context.Context, id int64, ip, login string) {
	if id != 0 {
		_, _ = m.pool.Exec(ctx, `UPDATE login_attempts SET success = true, pending = false, updated_at = now() WHERE id = $1`, id)
	}
	_, _ = m.pool.Exec(ctx, `DELETE FROM login_attempts WHERE ip = $1 AND lower(login) = lower($2) AND NOT success`, ClientNet(ip), login)
}

// Sign is a tick-free HMAC of msg with the site secret (hex). Unlike Nonce it does not expire; callers
// put whatever must invalidate it (a password hash, a user ID) into msg.
func (m *Manager) Sign(msg string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// RecordAttempt stores a finished attempt under the client network of ip.
func (m *Manager) RecordAttempt(ctx context.Context, ip, login string, success bool) {
	ip = ClientNet(ip)
	_, _ = m.pool.Exec(ctx, `INSERT INTO login_attempts(ip, login, success) VALUES ($1,$2,$3)`, ip, login, success)
	if success {
		// Only this login's failures: a success on one account must not reset the count of guesses
		// made at another account from the same address.
		_, _ = m.pool.Exec(ctx, `DELETE FROM login_attempts WHERE ip = $1 AND lower(login) = lower($2) AND NOT success`, ip, login)
	}
}
