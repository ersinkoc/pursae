package server

// Per-response Content-Security-Policy allow-list.
//
// securityHeaders sets a strict front-end policy on every response. A handler that knows its page
// needs a third-party source (a YouTube player, a Vimeo iframe) adds it for that response only with
// cspAllow; cspWriter merges the additions into the header just before it is sent. Features that
// scan the rendered page for embeds register a cspResponseHooks entry; embedNeeds maps a video URL
// to exactly the hosts its player loads from.

import (
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

const ctxCSPAllow = "wpgo.csp.allow"

// cspResponseHooks run when an HTML response with a policy starts, before the allow-list is
// merged; each may call cspAllow for what the page needs (a feature registers one in init).
var cspResponseHooks []func(*gin.Context)

// cspAllow adds sources to a directive of this response's Content-Security-Policy.
func cspAllow(c *gin.Context, directive string, sources ...string) {
	if c == nil || len(sources) == 0 {
		return
	}
	m, _ := c.Get(ctxCSPAllow)
	allow, _ := m.(map[string][]string)
	if allow == nil {
		allow = map[string][]string{}
		c.Set(ctxCSPAllow, allow)
	}
	for _, src := range sources {
		if !cspHas(allow[directive], src) {
			allow[directive] = append(allow[directive], src)
		}
	}
}

func cspHas(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// cspMerge adds sources to the directives of a policy. A directive the policy lacks starts from
// default-src; sources a directive already covers (the same source, or the https: scheme for an
// https:// host) are not repeated.
func cspMerge(policy string, allow map[string][]string) string {
	if policy == "" || len(allow) == 0 {
		return policy
	}
	type directive struct {
		name    string
		sources []string
	}
	var dirs []*directive
	byName := map[string]*directive{}
	for _, part := range strings.Split(policy, ";") {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		d := &directive{name: strings.ToLower(f[0]), sources: f[1:]}
		dirs = append(dirs, d)
		byName[d.name] = d
	}
	names := make([]string, 0, len(allow))
	for n := range allow {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := byName[n]
		if d == nil {
			d = &directive{name: n}
			if def := byName["default-src"]; def != nil {
				d.sources = append(d.sources, def.sources...)
			}
			if cspHas(d.sources, "'none'") {
				d.sources = nil
			}
			dirs = append(dirs, d)
			byName[n] = d
		}
		for _, src := range allow[n] {
			if cspHas(d.sources, src) || (strings.HasPrefix(src, "https://") && cspHas(d.sources, "https:")) {
				continue
			}
			if len(d.sources) == 1 && d.sources[0] == "'none'" {
				d.sources = nil
			}
			d.sources = append(d.sources, src)
		}
	}
	parts := make([]string, 0, len(dirs))
	for _, d := range dirs {
		parts = append(parts, strings.TrimSpace(d.name+" "+strings.Join(d.sources, " ")))
	}
	return strings.Join(parts, "; ")
}

// cspWriter applies the response's allow-list to the Content-Security-Policy header when the
// response starts.
type cspWriter struct {
	gin.ResponseWriter
	c    *gin.Context
	done bool
}

func (w *cspWriter) apply() {
	if w.done {
		return
	}
	w.done = true
	h := w.ResponseWriter.Header()
	policy := h.Get("Content-Security-Policy")
	if policy == "" || !strings.Contains(h.Get("Content-Type"), "text/html") {
		return
	}
	for _, hook := range cspResponseHooks {
		hook(w.c)
	}
	m, _ := w.c.Get(ctxCSPAllow)
	if allow, _ := m.(map[string][]string); len(allow) > 0 {
		h.Set("Content-Security-Policy", cspMerge(policy, allow))
	}
}

func (w *cspWriter) WriteHeaderNow() {
	w.apply()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *cspWriter) Write(b []byte) (int, error) {
	w.apply()
	return w.ResponseWriter.Write(b)
}

func (w *cspWriter) WriteString(s string) (int, error) {
	w.apply()
	return w.ResponseWriter.WriteString(s)
}

func (w *cspWriter) Flush() {
	w.apply()
	w.ResponseWriter.Flush()
}

// --- video embeds ------------------------------------------------------------------------------

// Hosts the video players load from.
const (
	cspYouTube        = "https://www.youtube.com"
	cspYouTubeNoCook  = "https://www.youtube-nocookie.com"
	cspVimeoPlayer    = "https://player.vimeo.com"
	cspDailymotion    = "https://dailymotion.com"
	cspDailymotionWWW = "https://www.dailymotion.com"
	cspDailymotionGeo = "https://geo.dailymotion.com" // the embed redirects to its player
	cspVideoPress     = "https://videopress.com"
)

var (
	cspYouTubeRe = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?(?:youtube\.com|youtube-nocookie\.com|youtu\.be)/`)
	cspVimeoRe   = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?vimeo\.com/`)
	cspDailyRe   = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?(?:dailymotion\.com|dai\.ly)/`)
	cspVPressRe  = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?(?:videopress\.com|video\.wordpress\.com)/`)
)

// embedNeeds is what a page's embeds load: script-src and frame-src sources.
type embedNeeds struct {
	script, frame []string
}

func (n *embedNeeds) add(script bool, srcs ...string) {
	for _, s := range srcs {
		if script {
			if !cspHas(n.script, s) {
				n.script = append(n.script, s)
			}
		} else if !cspHas(n.frame, s) {
			n.frame = append(n.frame, s)
		}
	}
}

// video records a video URL. api is true when the frontend drives it through the provider's
// player API (YouTube IFrame API, Vimeo player.js), false when it is a plain iframe.
func (n *embedNeeds) video(u string, api, privacy bool) {
	u = strings.TrimSpace(u)
	switch {
	case cspYouTubeRe.MatchString(u):
		nocookie := privacy || strings.Contains(strings.ToLower(u), "youtube-nocookie.com")
		if api {
			n.add(true, cspYouTube) // iframe_api and the widget API it loads
		}
		if nocookie {
			n.add(false, cspYouTubeNoCook)
		} else {
			n.add(false, cspYouTube)
		}
	case cspVimeoRe.MatchString(u):
		if api {
			n.add(true, cspVimeoPlayer)
		}
		n.add(false, cspVimeoPlayer)
	case cspDailyRe.MatchString(u):
		n.add(false, cspDailymotion, cspDailymotionWWW, cspDailymotionGeo)
	case cspVPressRe.MatchString(u):
		n.add(false, cspVideoPress)
	}
}

// allow adds the collected sources to this response's policy.
func (n *embedNeeds) allow(c *gin.Context) {
	cspAllow(c, "script-src", n.script...)
	cspAllow(c, "frame-src", n.frame...)
}
