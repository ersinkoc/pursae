package server

import (
	"context"
	"crypto/hmac"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Live Preview isolation. A previewed theme is code nobody reviewed for this site (a Library theme
// that is not even installed yet), and its templates and scripts run in the preview. Served on the
// site's own origin, its JavaScript could call /wp-admin and the REST API with the administrator's
// cookie. So every sandbox response carries
//
//	Content-Security-Policy: sandbox allow-scripts allow-forms allow-popups
//
// without allow-same-origin: the document gets an opaque origin, its scripts run but cannot read the
// site's cookies or storage, cannot send a credentialed request to the site, cannot reach into the
// admin page that frames it and cannot navigate the top window. X-Frame-Options is dropped (SAMEORIGIN
// would refuse the opaque-origin frame); frame-ancestors 'self' keeps framing to the site.
//
// A sandboxed document never sends the administrator's SameSite=Lax login cookie, not for its
// stylesheets, fonts and images, nor for its links: its origin is opaque, so every request it makes
// is cross-site. The cookie-checked /wp-admin/theme-preview/<slug>/… therefore sends a browser on to
//
//	/wp-content/theme-preview/<slug>/<token>/…
//
// where the token is the credential: an HMAC (auth.Sign) over the slug, the user and an expiry,
// minted for a user who may preview. It is checked on every request, together with the user's
// switch_themes capability, and is only good for that one theme's preview (read-only, GET and HEAD,
// never the sandbox's dashboard or login). The sandbox's URLs are rewritten under that prefix, so
// navigation keeps the token. /wp-content/ is outside the admin, so Pursae Security's hidden login,
// maintenance mode, headless mode, Golylang and GoCDN leave it alone, like the site's static files.
// The Preparing preview page (previewPreparing) is not sandboxed: it is core markup without theme
// code, and its status poll needs the cookie.

const (
	previewFrameRoute    = "/wp-content/theme-preview" // + "/<slug>/<token>/…"
	previewTokenTTL      = 6 * time.Hour
	previewSandboxPolicy = "sandbox allow-scripts allow-forms allow-popups"
)

// previewSandboxAllowed are the sandbox flags a preview may have: never allow-same-origin,
// allow-top-navigation or allow-popups-to-escape-sandbox.
var previewSandboxAllowed = map[string]bool{"allow-scripts": true, "allow-forms": true, "allow-popups": true}

// previewFramePrefix is the URL prefix a sandbox is served under with token.
func previewFramePrefix(slug, token string) string {
	return previewFrameRoute + "/" + slug + "/" + token
}

func (s *Server) previewTokenMAC(slug string, uid, exp int64) string {
	return s.auth.Sign("theme-preview|" + slug + "|" + strconv.FormatInt(uid, 10) + "|" + strconv.FormatInt(exp, 10))[:32]
}

// previewToken is the credential of user uid for slug's preview: "<expiry>.<user>.<mac>". The expiry
// is rounded to the hour, so the URLs (and the browser's cache of the preview's assets) stay the same
// for an hour.
func (s *Server) previewToken(slug string, uid int64, now time.Time) string {
	exp := now.Add(previewTokenTTL).Truncate(time.Hour).Add(time.Hour).Unix()
	return strconv.FormatInt(exp, 36) + "." + strconv.FormatInt(uid, 36) + "." + s.previewTokenMAC(slug, uid, exp)
}

// previewTokenUser is the user a token for slug was minted for, nil when it is not valid, has expired
// or the user may no longer preview themes.
func (s *Server) previewTokenUser(ctx context.Context, slug, token string, now time.Time) *store.User {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	exp, err := strconv.ParseInt(parts[0], 36, 64)
	if err != nil {
		return nil
	}
	uid, err := strconv.ParseInt(parts[1], 36, 64)
	if err != nil || uid <= 0 {
		return nil
	}
	if t := now.Unix(); exp <= t || exp > t+int64((previewTokenTTL+time.Hour)/time.Second) {
		return nil
	}
	if !hmac.Equal([]byte(parts[2]), []byte(s.previewTokenMAC(slug, uid, exp))) {
		return nil
	}
	u, err := s.store.UserByID(ctx, uid)
	if err != nil || !canPreview(u, false) {
		return nil
	}
	return u
}

// previewNavigation reports a browser loading a document (the preview iframe, a link in it, a tab):
// Fetch Metadata, or Upgrade-Insecure-Requests from browsers without it.
func previewNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return r.Header.Get("Upgrade-Insecure-Requests") == "1"
}

// previewFrameRedirect sends a browser that loads a preview document at the cookie-checked address to
// the sandboxed one; false when the request is served where it is (scripts and tools without Fetch
// Metadata, which get the sandbox header too).
func (s *Server) previewFrameRedirect(c *gin.Context, u *store.User, slug, sub string) bool {
	if !previewNavigation(c.Request) {
		return false
	}
	target := previewFramePrefix(slug, s.previewToken(slug, u.ID, time.Now())) + sub
	if q := c.Request.URL.RawQuery; q != "" {
		target += "?" + q
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Redirect(http.StatusFound, target)
	return true
}

// themePreviewFrame serves /wp-content/theme-preview/<slug>/<token>/… from the theme's sandbox, the
// token standing in for the login cookie a sandboxed frame does not send.
func (s *Server) themePreviewFrame(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "same-origin")
	slug, rest, _ := strings.Cut(strings.TrimPrefix(c.Param("rest"), "/"), "/")
	token, sub, more := strings.Cut(rest, "/")
	if !validSlug(slug) || token == "" {
		c.Status(http.StatusNotFound)
		return
	}
	u := s.previewTokenUser(c.Request.Context(), slug, token, time.Now())
	if u == nil {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		c.String(http.StatusForbidden, "This preview link has expired or is not valid. Open Live Preview again from Appearance › Themes.")
		return
	}
	prefix := previewFramePrefix(slug, token)
	if !more && c.Request.Method == http.MethodGet { // …/<token>: the home page
		c.Redirect(http.StatusMovedPermanently, prefix+"/")
		return
	}
	if msg := previewRefused(c.Request.Method, "/"+sub); msg != "" {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		c.String(http.StatusForbidden, msg)
		return
	}
	s.previewServe(c, u, slug, "/"+sub, prefix)
}

// previewSandboxHeaders makes a sandbox response safe to show on the site's origin: the CSP sandbox
// (merged into the response's own policy, see previewSandboxCSP), no X-Frame-Options, CORS for the
// fonts an opaque-origin document requests, a referrer only for the site itself, and never a shared
// cache (the page cache stores no private response).
func previewSandboxHeaders(h http.Header) {
	if vs := h.Values("Content-Security-Policy"); len(vs) > 0 {
		vs[0] = previewSandboxCSP(vs[0])
		h["Content-Security-Policy"] = vs
	} else {
		h.Set("Content-Security-Policy", previewSandboxCSP(""))
	}
	h.Del("X-Frame-Options")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Referrer-Policy", "same-origin")
	if cc := strings.ToLower(h.Get("Cache-Control")); !strings.Contains(cc, "private") && !strings.Contains(cc, "no-store") {
		if cc == "" {
			h.Set("Cache-Control", "private")
		} else {
			h.Set("Cache-Control", "private, "+h.Get("Cache-Control"))
		}
	}
}

// previewSandboxCSP is policy with the preview's sandbox directive first. The directive is put into
// the response's policy rather than sent as a second header, because the site's CSP writer merges
// its additions into the first Content-Security-Policy value only (cspWriter.apply). A sandbox
// directive the response already has (uploads are served with a bare "sandbox") stays when it allows
// no more than the preview's; any other is replaced. frame-ancestors 'self' is added when missing.
func previewSandboxCSP(policy string) string {
	sandbox := ""
	var rest []string
	ancestors := false
	for _, part := range strings.Split(policy, ";") {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		name := strings.ToLower(f[0])
		if name == "sandbox" {
			if sandbox == "" && previewSandboxFlagsOK(f[1:]) {
				sandbox = strings.Join(f, " ")
			}
			continue
		}
		if name == "frame-ancestors" {
			ancestors = true
		}
		rest = append(rest, strings.Join(f, " "))
	}
	if sandbox == "" {
		sandbox = previewSandboxPolicy
	}
	if !ancestors {
		rest = append(rest, "frame-ancestors 'self'")
	}
	return strings.Join(append([]string{sandbox}, rest...), "; ")
}

func previewSandboxFlagsOK(flags []string) bool {
	for _, f := range flags {
		if !previewSandboxAllowed[strings.ToLower(f)] {
			return false
		}
	}
	return true
}
