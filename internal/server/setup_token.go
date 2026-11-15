package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wpgo/internal/auth"
)

// The setup token guards /wp-admin/install: without it, whoever reaches a fresh instance first could
// install the site and become its administrator. The token is WPGO_SETUP_TOKEN when set; otherwise the
// first process that starts on an uninstalled site generates one, keeps its SHA-256 in the setup_token
// table (so every instance and restart checks the same token) and prints the install URL once
// (cmd/wpgo). `wpgo setup-token` issues a new one. Entered once, the token is remembered by a signed
// cookie bound to it; installing the site deletes it, so the token and the cookie stop working.

const (
	setupTokenCookie = "wpgo_setup"
	setupTokenTTL    = 12 * time.Hour
)

// querier is what the setup token helpers need from a pool or a connection.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func hashSetupToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// SetupURL is the install address carrying the token, the link printed for the site owner.
func SetupURL(siteURL, token string) string {
	return strings.TrimRight(siteURL, "/") + "/wp-admin/install?token=" + url.QueryEscape(token)
}

// siteInstalled reads the database itself, not a process's settings cache (the CLI has none).
func siteInstalled(ctx context.Context, q querier) (bool, error) {
	var installed bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings WHERE key = 'siteurl')`).Scan(&installed)
	return installed, err
}

// IssueSetupToken generates a new setup token, replacing the stored one (whose links and cookies stop
// working), and returns it. It refuses once the site is installed.
func IssueSetupToken(ctx context.Context, q querier) (string, error) {
	installed, err := siteInstalled(ctx, q)
	if err != nil {
		return "", err
	}
	if installed {
		return "", errAlreadyInstalled
	}
	token := auth.RandomToken(32)
	_, err = q.Exec(ctx, `INSERT INTO setup_token (id, token_hash, created_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = EXCLUDED.created_at`, hashSetupToken(token))
	if err != nil {
		return "", err
	}
	return token, nil
}

// EnsureSetupToken runs at start-up. On an uninstalled site without WPGO_SETUP_TOKEN it generates the
// setup token unless another process (or an earlier run) already did, and returns it so it can be
// printed; "" means there is nothing to print (installed, token from the environment, or already issued).
func (s *Server) EnsureSetupToken(ctx context.Context) (string, error) {
	if s.cfg.SetupToken != "" {
		return "", nil
	}
	installed, err := siteInstalled(ctx, s.store.Pool)
	if err != nil || installed {
		return "", err
	}
	token := auth.RandomToken(32)
	tag, err := s.store.Pool.Exec(ctx, `INSERT INTO setup_token (id, token_hash) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, hashSetupToken(token))
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", nil
	}
	return token, nil
}

// setupTokenHash is the SHA-256 of the token in force: WPGO_SETUP_TOKEN, else the stored one. nil means
// no token was issued, and the installer stays closed.
func (s *Server) setupTokenHash(ctx context.Context) ([]byte, error) {
	if s.cfg.SetupToken != "" {
		return hashSetupToken(s.cfg.SetupToken), nil
	}
	var h []byte
	err := s.store.Pool.QueryRow(ctx, `SELECT token_hash FROM setup_token WHERE id = 1`).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return h, err
}

func setupTokenMatches(given string, current []byte) bool {
	return given != "" && len(current) == sha256.Size && subtle.ConstantTimeCompare(hashSetupToken(given), current) == 1
}

func (s *Server) setupCookieMAC(exp string, current []byte) string {
	m := hmac.New(sha256.New, []byte("pursae-setup-token|"+s.siteSecret()))
	m.Write([]byte(exp + "|" + hex.EncodeToString(current)))
	return hex.EncodeToString(m.Sum(nil))
}

// setupCookieValid: the cookie was signed for the token in force and has not expired.
func (s *Server) setupCookieValid(c *gin.Context, current []byte) bool {
	if current == nil || s.siteSecret() == "" {
		return false
	}
	raw, err := c.Cookie(setupTokenCookie)
	if err != nil {
		return false
	}
	exp, mac, ok := strings.Cut(raw, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > n {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(s.setupCookieMAC(exp, current)))
}

func (s *Server) setSetupCookie(c *gin.Context, current []byte) {
	exp := strconv.FormatInt(time.Now().Add(setupTokenTTL).Unix(), 10)
	// Secure follows the request: a fresh site is often installed over plain http (localhost, a LAN address).
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // G124: Secure is set on https requests
		Name: setupTokenCookie, Value: exp + "." + s.setupCookieMAC(exp, current),
		Path: "/wp-admin/install", HttpOnly: true, Secure: s.requestHTTPS(c.Request), SameSite: http.SameSiteLaxMode})
}

// setupGate lets an installer request through when it carries the setup token (?token= on the form,
// setup_token in a POST) or the cookie that remembers it. Otherwise it answers itself, with the token
// form, and returns false. A token given in the URL is moved into the cookie and the URL dropped,
// so it does not stay in the history or leak in a Referer.
func (s *Server) setupGate(c *gin.Context) bool {
	current, err := s.setupTokenHash(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "The setup token could not be read: %v", err)
		return false
	}
	if s.setupCookieValid(c, current) {
		return true
	}
	given := c.Query("token")
	if c.Request.Method == http.MethodPost {
		given = c.PostForm("setup_token")
	}
	if setupTokenMatches(given, current) {
		s.setSetupCookie(c, current)
		if c.Request.Method == http.MethodPost {
			return true
		}
		q := c.Request.URL.Query()
		q.Del("token")
		dest := "/wp-admin/install"
		if enc := q.Encode(); enc != "" {
			dest += "?" + enc
		}
		c.Header("Referrer-Policy", "no-referrer")
		c.Redirect(http.StatusSeeOther, dest)
		return false
	}
	v := installView{NeedToken: true}
	if given != "" {
		v.Error = "That setup token is not valid. Use the link printed in the server log, or run \"wpgo setup-token\" on the server for a new one."
	}
	c.Header("Referrer-Policy", "no-referrer")
	s.renderAdminTemplate(c, http.StatusForbidden, "admin/install", v)
	return false
}

// Installed reports whether the site has been installed (the settings cache of this process).
func (s *Server) Installed() bool { return s.settings.Installed() }
