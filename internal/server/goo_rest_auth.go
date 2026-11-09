package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// wc/v3 authentication with WooCommerce API keys (WC_REST_Authentication): a consumer key / secret
// pair from Settings › Advanced › REST API, sent as HTTP Basic auth (ck_…:cs_…) or as the
// consumer_key / consumer_secret query parameters. Keys are looked up by hex(sha256(consumer key)),
// the secret is compared in constant time, the key's permissions (read, write, read_write) are
// enforced by method and the request runs as the key's user. Like WooCommerce, keys are only
// accepted over HTTPS — local development hosts (localhost, *.localhost, *.test) excepted.

const gooCtxAPIKey = "goo.apiKey"

// gooHashKey is the stored form of a consumer key.
func gooHashKey(ck string) string {
	sum := sha256.Sum256([]byte(ck))
	return hex.EncodeToString(sum[:])
}

// gooRESTNewKey creates an API key for a user and returns the consumer key and secret (shown once).
func (s *Server) gooRESTNewKey(ctx context.Context, userID int64, description, permissions string) (ck, cs string, id int64, err error) {
	switch permissions {
	case "read", "write", "read_write":
	default:
		permissions = "read"
	}
	ck, cs = "ck_"+gooRandomKey(40), "cs_"+gooRandomKey(40)
	err = s.store.Pool.QueryRow(ctx, `INSERT INTO goo_api_keys (user_id, description, permissions, consumer_key, consumer_secret, truncated_key)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, userID, description, permissions, gooHashKey(ck), cs, ck[len(ck)-7:]).Scan(&id)
	return
}

// gooV3User is the user of a wc/v3 request: the core REST user (cookie + nonce, application
// password) or the owner of the consumer key the request carries.
func (s *Server) gooV3User(c *gin.Context, method string) (*store.User, *gooAPIErr) {
	if u := restCurrent(c); u != nil {
		return u, nil
	}
	ck, cs := c.Query("consumer_key"), c.Query("consumer_secret")
	if ck == "" {
		if user, pass, ok := c.Request.BasicAuth(); ok && strings.HasPrefix(user, "ck_") {
			ck, cs = user, pass
		}
	}
	if ck == "" {
		return nil, nil
	}
	return s.gooKeyAuth(c, ck, cs, method)
}

// gooKeyAuth checks a consumer key / secret and the key's permission for the method.
func (s *Server) gooKeyAuth(c *gin.Context, ck, cs, method string) (*store.User, *gooAPIErr) {
	if !gooSecureRequest(c) {
		return nil, gooErr(http.StatusUnauthorized, "woocommerce_rest_authentication_error", "API keys are only accepted over HTTPS.")
	}
	var id, userID int64
	var secret, perms string
	err := s.store.Pool.QueryRow(c, `SELECT id, user_id, consumer_secret, permissions FROM goo_api_keys WHERE consumer_key = $1`, gooHashKey(ck)).
		Scan(&id, &userID, &secret, &perms)
	if err != nil {
		return nil, gooErr(http.StatusUnauthorized, "woocommerce_rest_authentication_error", "Consumer key is invalid.")
	}
	if subtle.ConstantTimeCompare([]byte(secret), []byte(cs)) != 1 {
		return nil, gooErr(http.StatusUnauthorized, "woocommerce_rest_authentication_error", "Consumer secret is invalid.")
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if perms != "read" && perms != "read_write" {
			return nil, gooErr(http.StatusUnauthorized, "woocommerce_rest_authentication_error", "The API key provided does not have read permissions.")
		}
	default:
		if perms != "write" && perms != "read_write" {
			return nil, gooErr(http.StatusUnauthorized, "woocommerce_rest_authentication_error", "The API key provided does not have write permissions.")
		}
	}
	u, err := s.store.UserByID(c, userID)
	if err != nil {
		return nil, gooErr(http.StatusUnauthorized, "woocommerce_rest_authentication_error", "Consumer key is invalid.")
	}
	_, _ = s.store.Pool.Exec(c, `UPDATE goo_api_keys SET last_access = now() WHERE id = $1`, id)
	c.Set(restUserKey, u)
	c.Set(gooCtxAPIKey, id)
	return u, nil
}

// gooKeyRoute reports whether a REST route belongs to the store's API (wc/…, wc-…), the only routes
// that accept API keys (WC_REST_Authentication::is_request_to_rest_api).
func gooKeyRoute(route string) bool {
	route = strings.TrimLeft(route, "/")
	return strings.HasPrefix(route, "wc/") || strings.HasPrefix(route, "wc-")
}

// gooQueryKeyRoute reports whether route authenticates consumer_key / consumer_secret query parameters:
// only the management API (wc/v3, through gooV3User). The Store API (wc/store/…) and the other wc routes
// ignore them, so a query key there is no credential.
func gooQueryKeyRoute(route string) bool {
	route = strings.TrimLeft(route, "/")
	return route == gooV3NS || strings.HasPrefix(route, gooV3NS+"/")
}

// gooSecureRequest reports whether keys may be sent: HTTPS (directly or behind a proxy) or a local
// development host.
func gooSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		return true
	}
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test")
}
