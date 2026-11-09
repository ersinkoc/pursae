package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// Bypass reasons reported by [Cache.Classify].
const (
	ReasonDisabled      = "disabled"
	ReasonMethod        = "method"
	ReasonAuthorization = "authorization"
	ReasonExcludedPath  = "excluded-path"
	ReasonUserAgent     = "user-agent"
	ReasonQuery         = "query-string"
	ReasonNoCache       = "no-cache"
	ReasonCookie        = "cookie"
	ReasonLoggedIn      = "logged-in"
)

// Decision is the outcome of classifying a request.
type Decision struct {
	// Key is the canonical cache key; empty when the request bypasses the cache.
	Key string
	// Private is true for a per-session bucket of a logged-in user (CacheLoggedIn).
	Private bool
	// Bypass names the reason the cache is skipped; empty when Key is set.
	Bypass string
}

var defaultMobileAgents = []string{
	"mobile", "android", "silk/", "kindle", "blackberry", "opera mini", "opera mobi", "iphone", "ipod", "iemobile", "webos",
}

// Classify decides whether r may be served from (and stored in) the cache and
// computes its key: scheme://host/path?allowed-sorted-query#vary-bucket.
func (c *Cache) Classify(r *http.Request) Decision {
	cfg := &c.cfg
	if !cfg.Enabled {
		return Decision{Bypass: ReasonDisabled}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return Decision{Bypass: ReasonMethod}
	}
	if r.Header.Get("Authorization") != "" {
		return Decision{Bypass: ReasonAuthorization}
	}
	p := r.URL.Path
	if p == "" {
		p = "/"
	}
	for _, pat := range cfg.ExcludePaths {
		if matchPrefixOrWildcard(pat, p) {
			return Decision{Bypass: ReasonExcludedPath}
		}
	}
	ua := strings.ToLower(r.UserAgent())
	for _, s := range cfg.ExcludeUserAgents {
		if s != "" && strings.Contains(ua, strings.ToLower(s)) {
			return Decision{Bypass: ReasonUserAgent}
		}
	}
	query, ok := c.keyQuery(r.URL.RawQuery)
	if !ok {
		return Decision{Bypass: ReasonQuery}
	}
	if query == "\x00nocache" {
		return Decision{Bypass: ReasonNoCache}
	}

	var login string
	for _, ck := range r.Cookies() {
		for _, pat := range cfg.ExcludeCookies {
			if matchPrefixOrWildcard(pat, ck.Name) {
				return Decision{Bypass: ReasonCookie}
			}
		}
		for _, pat := range c.bypassCookies {
			if matchPrefixOrWildcard(pat, ck.Name) {
				return Decision{Bypass: ReasonCookie}
			}
		}
		if ck.Name == c.loginCookie && ck.Value != "" {
			login = ck.Value
		}
	}
	if login != "" {
		if !cfg.CacheLoggedIn {
			return Decision{Bypass: ReasonLoggedIn}
		}
		// A logged-in user asking for a fresh copy (Cache-Control: no-cache, as
		// sent by a hard reload or a purge request) skips the cache.
		if hasToken(r.Header.Get("Cache-Control"), "no-cache") || hasToken(r.Header.Get("Pragma"), "no-cache") {
			return Decision{Bypass: ReasonNoCache}
		}
	}

	var b strings.Builder
	b.WriteString(requestScheme(r))
	b.WriteString("://")
	b.WriteString(strings.ToLower(r.Host))
	b.WriteString(r.URL.EscapedPath())
	if r.URL.Path == "" {
		b.WriteByte('/')
	}
	if query != "" {
		b.WriteByte('?')
		b.WriteString(query)
	}
	b.WriteByte('#')
	if cfg.CacheMobileSeparately && c.isMobile(ua) {
		b.WriteString("m")
	} else {
		b.WriteString("d")
	}
	d := Decision{}
	if login != "" {
		sum := sha256.Sum256([]byte(c.loginCookie + "=" + login))
		b.WriteString(",u:")
		b.WriteString(hex.EncodeToString(sum[:12]))
		d.Private = true
	}
	d.Key = b.String()
	return d
}

// Key returns the cache key of r, or "" when r bypasses the cache.
func (c *Cache) Key(r *http.Request) string { return c.Classify(r).Key }

// keyQuery returns the canonical query part of the key (allowed parameters, sorted,
// re-encoded). ok is false when a parameter is neither allowed nor dropped. A
// "nocache" parameter yields the sentinel "\x00nocache".
func (c *Cache) keyQuery(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return "", false
	}
	if _, ok := vals["nocache"]; ok {
		return "\x00nocache", true
	}
	keep := url.Values{}
	for name, vs := range vals {
		switch {
		case c.allowedQuery[name]:
			keep[name] = vs
		case c.dropped(name):
		default:
			return "", false
		}
	}
	return keep.Encode(), true // Sort keys, preserving value order used by Query().Get.
}

func (c *Cache) dropped(name string) bool {
	for _, pat := range c.cfg.DropQueryStrings {
		if pat == name || (strings.HasSuffix(pat, "*") && strings.HasPrefix(name, pat[:len(pat)-1])) {
			return true
		}
	}
	return false
}

func (c *Cache) isMobile(lowerUA string) bool {
	list := c.cfg.MobileUserAgents
	if len(list) == 0 {
		list = defaultMobileAgents
	}
	for _, s := range list {
		if s != "" && strings.Contains(lowerUA, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if p := strings.ToLower(r.Header.Get("X-Forwarded-Proto")); p == "https" || p == "http" {
		return p
	}
	if r.URL.Scheme == "https" {
		return "https"
	}
	return "http"
}

// hasToken reports whether a comma-separated header value contains token
// (case-insensitive, ignoring directive arguments).
func hasToken(v, token string) bool {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if i := strings.IndexByte(part, '='); i >= 0 {
			part = part[:i]
		}
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func mediaType(h http.Header) string {
	ct := h.Get("Content-Type")
	if ct == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	}
	return mt
}

// cacheableType reports whether a media type is a page the cache stores.
func cacheableType(mt string) bool {
	switch mt {
	case "text/html", "application/xhtml+xml", "application/rss+xml", "application/atom+xml",
		"application/rdf+xml", "application/xml", "text/xml":
		return true
	}
	return false
}

func isFeedType(mt string) bool {
	return mt == "application/rss+xml" || mt == "application/atom+xml" || mt == "application/rdf+xml"
}

func isFeedPath(p string) bool {
	p = strings.TrimSuffix(p, "/")
	return strings.HasSuffix(p, "/feed") || strings.Contains(p, "/feed/")
}

// isStaticType reports whether a media type is a static asset eligible for the
// browser cache TTL.
func isStaticType(mt string) bool {
	switch {
	case strings.HasPrefix(mt, "image/"), strings.HasPrefix(mt, "font/"),
		strings.HasPrefix(mt, "audio/"), strings.HasPrefix(mt, "video/"):
		return true
	}
	switch mt {
	case "text/css", "text/javascript", "application/javascript", "application/x-javascript",
		"application/font-woff", "application/font-woff2", "application/vnd.ms-fontobject",
		"application/x-font-ttf", "application/wasm":
		return true
	}
	return false
}

func parseTags(v string) []string {
	if v == "" {
		return nil
	}
	return normalizeTags(strings.Split(v, ","))
}
