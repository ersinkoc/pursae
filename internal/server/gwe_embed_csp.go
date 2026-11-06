package server

// Golementor pages' video embeds in the per-response Content-Security-Policy (csp_embed.go): the
// rendered builder documents (and popups) are scanned for video widgets, background videos, video
// playlists, the atomic YouTube element and lightbox videos, and exactly the hosts those embeds
// load from are allowed.

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

func init() { cspResponseHooks = append(cspResponseHooks, gweEmbedCSP) }

var (
	gweCSPSettingsRe = regexp.MustCompile(`\sdata-(settings|elementor-lightbox)="([^"]*)"`)
	gweCSPVideoURLRe = regexp.MustCompile(`\sdata-video-url="([^"]*)"`)
	gweCSPIframeRe   = regexp.MustCompile(`<iframe\b[^>]*?\s(?:data-lazy-load|src)="([^"]+)"`)
	gweCSPActionRe   = regexp.MustCompile(`elementor-action(?:%3A|:)action(?:%3D|=)lightbox(?:%26|&(?:amp;)?)settings(?:%3D|=)([A-Za-z0-9%=+/_-]+)`)
)

// gweEmbedScan finds the video embeds of rendered builder markup.
func gweEmbedScan(markup string, n *embedNeeds) {
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

func gweEmbedWalk(v any, n *embedNeeds, api, privacy bool) {
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

// gweEmbedCSP allows the video hosts the page's Golementor documents need. The editor preview
// gets every video host: widgets are added there at any time.
func gweEmbedCSP(c *gin.Context) {
	if c == nil {
		return
	}
	n := &embedNeeds{}
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
	n.allow(c)
}
