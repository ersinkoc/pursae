package server

// Per-response Content-Security-Policy allow-list.
//
// securityHeaders sets a strict front-end policy on every response. A handler that knows its page
// needs a third-party source (a YouTube player, a Vimeo iframe) adds it for that response only with
// cspAllow; cspWriter merges the additions into the header just before it is sent. GoWebEditor
// pages are also checked automatically: the rendered builder documents (and popups) are scanned
// for video widgets, background videos, video playlists, the atomic YouTube element and lightbox
// videos, and exactly the hosts those embeds load from are allowed.

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

const ctxCSPAllow = "wpgo.csp.allow"

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
	gweEmbedCSP(w.c)
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
	gweCSPSettingsRe = regexp.MustCompile(`\sdata-(settings|elementor-lightbox)="([^"]*)"`)
	gweCSPVideoURLRe = regexp.MustCompile(`\sdata-video-url="([^"]*)"`)
	gweCSPIframeRe   = regexp.MustCompile(`<iframe\b[^>]*?\s(?:data-lazy-load|src)="([^"]+)"`)
	gweCSPActionRe   = regexp.MustCompile(`elementor-action(?:%3A|:)action(?:%3D|=)lightbox(?:%26|&(?:amp;)?)settings(?:%3D|=)([A-Za-z0-9%=+/_-]+)`)
	gweCSPYouTubeRe  = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?(?:youtube\.com|youtube-nocookie\.com|youtu\.be)/`)
	gweCSPVimeoRe    = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?vimeo\.com/`)
	gweCSPDailyRe    = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?(?:dailymotion\.com|dai\.ly)/`)
	gweCSPVPressRe   = regexp.MustCompile(`(?i)^(?:https?:)?//(?:[a-z0-9-]+\.)?(?:videopress\.com|video\.wordpress\.com)/`)
)

// gweEmbedNeeds is what a page's embeds load: script-src and frame-src sources.
type gweEmbedNeeds struct {
	script, frame []string
}

func (n *gweEmbedNeeds) add(script bool, srcs ...string) {
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
func (n *gweEmbedNeeds) video(u string, api, privacy bool) {
	u = strings.TrimSpace(u)
	switch {
	case gweCSPYouTubeRe.MatchString(u):
		nocookie := privacy || strings.Contains(strings.ToLower(u), "youtube-nocookie.com")
		if api {
			n.add(true, cspYouTube) // iframe_api and the widget API it loads
		}
		if nocookie {
			n.add(false, cspYouTubeNoCook)
		} else {
			n.add(false, cspYouTube)
		}
	case gweCSPVimeoRe.MatchString(u):
		if api {
			n.add(true, cspVimeoPlayer)
		}
		n.add(false, cspVimeoPlayer)
	case gweCSPDailyRe.MatchString(u):
		n.add(false, cspDailymotion, cspDailymotionWWW, cspDailymotionGeo)
	case gweCSPVPressRe.MatchString(u):
		n.add(false, cspVideoPress)
	}
}

// gweEmbedScan finds the video embeds of rendered builder markup.
func gweEmbedScan(markup string, n *gweEmbedNeeds) {
	if markup == "" {
		return
	}
	for _, m := range gweCSPSettingsRe.FindAllStringSubmatch(markup, -1) {
		raw := html.UnescapeString(m[2])
		if !strings.Contains(raw, "//") && !strings.Contains(raw, `\/\/`) {
			continue
		}
		var v any
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		// data-settings: video / background video / atomic YouTube players use the provider
		// APIs; data-elementor-lightbox: the lightbox prints a plain iframe of the embed URL.
		gweEmbedWalk(v, n, m[1] == "settings", false)
	}
	for _, m := range gweCSPVideoURLRe.FindAllStringSubmatch(markup, -1) { // video playlist
		n.video(html.UnescapeString(m[1]), true, false)
	}
	for _, m := range gweCSPIframeRe.FindAllStringSubmatch(markup, -1) {
		n.video(html.UnescapeString(m[1]), false, false)
	}
	for _, m := range gweCSPActionRe.FindAllStringSubmatch(markup, -1) { // lightbox actions in links
		enc, err := url.QueryUnescape(m[1])
		if err != nil {
			enc = m[1]
		}
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			if b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(enc, "=")); err != nil {
				continue
			}
		}
		var v any
		if json.Unmarshal(b, &v) == nil {
			gweEmbedWalk(v, n, false, false)
		}
	}
}

func gweEmbedWalk(v any, n *gweEmbedNeeds, api, privacy bool) {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range []string{"yt_privacy", "privacy", "background_privacy_mode"} {
			switch pv := t[k].(type) {
			case bool:
				privacy = privacy || pv
			case string:
				privacy = privacy || pv == "yes"
			}
		}
		for _, x := range t {
			gweEmbedWalk(x, n, api, privacy)
		}
	case []any:
		for _, x := range t {
			gweEmbedWalk(x, n, api, privacy)
		}
	case string:
		if strings.Contains(t, "//") {
			n.video(t, api, privacy)
		}
	}
}

// gweEmbedCSP allows the video hosts the page's GoWebEditor documents need. The editor preview
// gets every video host: widgets are added there at any time.
func gweEmbedCSP(c *gin.Context) {
	if c == nil {
		return
	}
	n := &gweEmbedNeeds{}
	if gwePreviewing(c) {
		n.add(true, cspYouTube, cspVimeoPlayer)
		n.add(false, cspYouTube, cspYouTubeNoCook, cspVimeoPlayer, cspDailymotion, cspDailymotionWWW, cspDailymotionGeo, cspVideoPress)
	} else {
		for _, r := range gweResults(c) {
			if r.res != nil {
				gweEmbedScan(r.res.HTML, n)
			}
		}
	}
	cspAllow(c, "script-src", n.script...)
	cspAllow(c, "frame-src", n.frame...)
}
