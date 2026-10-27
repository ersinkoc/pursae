package cache

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// effectiveHint resolves HintAuto for one request.
func (c *Cache) effectiveHint(r *http.Request) ServerHint {
	switch c.cfg.ServerHint {
	case HintAuto:
		if c.isLiteSpeed(r) {
			return HintLiteSpeed
		}
		return HintNone
	case HintLiteSpeed, HintNginx, HintGeneric:
		return c.cfg.ServerHint
	}
	return HintNone
}

func (c *Cache) isLiteSpeed(r *http.Request) bool {
	if c.detectLS != nil {
		return c.detectLS(r)
	}
	return c.lsEnv || r.Header.Get("X-LSCACHE") != ""
}

func (c *Cache) prefixed(tags []string) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = c.cfg.TagPrefix + t
	}
	return out
}

// writePublicHints adds front-proxy headers for a cacheable public response.
// remaining is the entry's remaining lifetime, lifetime its full TTL.
func (c *Cache) writePublicHints(h http.Header, r *http.Request, remaining, lifetime time.Duration, tags []string) {
	rem := secs(remaining)
	switch c.effectiveHint(r) {
	case HintLiteSpeed:
		h.Set("X-LiteSpeed-Cache-Control", "public,max-age="+strconv.Itoa(rem))
		if len(tags) > 0 {
			h.Set("X-LiteSpeed-Tag", strings.Join(c.prefixed(tags), ","))
		}
	case HintNginx:
		h.Set("X-Accel-Expires", strconv.Itoa(rem))
	case HintGeneric:
		// Age (set by the caller) plus s-maxage give shared caches the true freshness.
		h.Set("Cache-Control", "public, s-maxage="+strconv.Itoa(secs(lifetime))+", max-age=0")
		if len(tags) > 0 {
			p := c.prefixed(tags)
			h.Set("Surrogate-Key", strings.Join(p, " "))
			h.Set("Cache-Tag", strings.Join(p, ","))
		}
	}
}

// writeNoCacheHints tells a front proxy not to cache this page response.
func (c *Cache) writeNoCacheHints(h http.Header, r *http.Request) {
	switch c.effectiveHint(r) {
	case HintLiteSpeed:
		h.Set("X-LiteSpeed-Cache-Control", "no-cache")
	case HintNginx:
		h.Set("X-Accel-Expires", "0")
	case HintGeneric:
		if h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", "private, no-cache")
		}
	}
}

// PurgeHeader returns the X-LiteSpeed-Purge header value that purges tags from a
// LiteSpeed front cache ("*" purges everything when no tag is given). It returns ""
// unless ServerHint is litespeed or auto; with auto the header is harmless when no
// LiteSpeed server is in front. The core sends it on the admin response that
// triggered the purge:
//
//	if v := c.PurgeHeader("post:12", "home"); v != "" { w.Header().Set("X-LiteSpeed-Purge", v) }
func (c *Cache) PurgeHeader(tags ...string) string {
	if c.cfg.ServerHint != HintLiteSpeed && c.cfg.ServerHint != HintAuto {
		return ""
	}
	tags = normalizeTags(tags)
	if len(tags) == 0 {
		return "*"
	}
	parts := make([]string, len(tags))
	for i, t := range tags {
		parts[i] = "tag=" + c.cfg.TagPrefix + t
	}
	return strings.Join(parts, ", ")
}

// BrowserCacheHeader returns the Cache-Control value for static assets, or "" when
// BrowserCacheTTL is not set. The middleware applies it automatically to asset
// responses without a Cache-Control of their own.
func (c *Cache) BrowserCacheHeader() string {
	if c.cfg.BrowserCacheTTL <= 0 {
		return ""
	}
	return "public, max-age=" + strconv.Itoa(secs(c.cfg.BrowserCacheTTL))
}

func secs(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}
