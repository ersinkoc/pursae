package builder

// upstream Embed (YouTube/Vimeo/Dailymotion/VideoPress embed URLs) and WordPress add_query_arg.

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

type embedProvider struct {
	name  string
	masks []*regexp.Regexp
}

var embedProviders = []embedProvider{
	{"youtube", []*regexp.Regexp{regexp.MustCompile(`^.*(?:youtu\.be/|youtube(?:-nocookie)?\.com/(?:(?:watch)?\?(?:.*&)?vi?=|(?:embed|v|vi|user|shorts)/))([^\?&"'>]+)`)}},
	{"vimeo", []*regexp.Regexp{regexp.MustCompile("^.*vimeo\\.com/(?:[a-z]*/)*([‌​0-9]{6,11})[?]?.*")}},
	{"dailymotion", []*regexp.Regexp{regexp.MustCompile(`^.*dailymotion.com/(?:video|hub)/([^_]+)[^#]*(#video=([^_&]+))?`)}},
	{"videopress", []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(?:http(?:s)?://)?videos\.files\.wordpress\.com/([a-zA-Z\d]{8,})/`),
		regexp.MustCompile(`(?i)^(?:http(?:s)?://)?(?:www\.)?video(?:\.word)?press\.com/(?:v|embed)/([a-zA-Z\d]{8,})(.+)?`),
	}},
}

var embedPatterns = map[string]string{
	"youtube":     "https://www.youtube{NO_COOKIE}.com/embed/{VIDEO_ID}?feature=oembed",
	"vimeo":       "https://player.vimeo.com/video/{VIDEO_ID}#t={TIME}",
	"dailymotion": "https://dailymotion.com/embed/video/{VIDEO_ID}",
	"videopress":  "https://videopress.com/embed/{VIDEO_ID}",
}

// videoProperties mirrors Embed::get_video_properties.
func videoProperties(u string) (provider, id string, ok bool) {
	for _, p := range embedProviders {
		for _, m := range p.masks {
			if sm := m.FindStringSubmatch(u); sm != nil {
				return p.name, sm[1], true
			}
		}
	}
	return "", "", false
}

func isEmbedVideo(u string) bool {
	p, _, ok := videoProperties(u)
	return ok && (p == "youtube" || p == "vimeo")
}

var vimeoHashRe = regexp.MustCompile(`(?:[\?|\&]h=([\w]+)|\d/([\w]+))`)

// embedURL mirrors Embed::get_embed_url. params is an ordered parameter list (value nil = omitted).
func embedURL(videoURL string, params *omap, privacy bool, start int) (string, bool) {
	provider, id, ok := videoProperties(videoURL)
	if !ok {
		return "", false
	}
	pattern := embedPatterns[provider]
	pattern = strings.ReplaceAll(pattern, "{VIDEO_ID}", id)
	if params == nil {
		params = newOmap()
	}
	switch provider {
	case "youtube":
		nc := ""
		if privacy {
			nc = "-nocookie"
		}
		pattern = strings.ReplaceAll(pattern, "{NO_COOKIE}", nc)
	case "vimeo":
		t := ""
		if start > 0 {
			d := time.Unix(int64(start), 0).UTC()
			t = d.Format("15") + "h" + d.Format("04") + "m" + d.Format("05") + "s"
		}
		pattern = strings.ReplaceAll(pattern, "{TIME}", t)
		if m := vimeoHashRe.FindStringSubmatch(videoURL); m != nil {
			h := m[1]
			if h == "" {
				h = m[2]
			}
			params.Set("h", h)
		}
	}
	return addQueryArg(params, pattern), true
}

// addQueryArg mirrors WordPress add_query_arg( array, url ) without URL-encoding values.
func addQueryArg(args *omap, uri string) string {
	frag := ""
	if i := strings.Index(uri, "#"); i >= 0 {
		frag = uri[i:]
		uri = uri[:i]
	}
	base, query := uri, ""
	if i := strings.Index(uri, "?"); i >= 0 {
		base = uri[:i+1]
		query = uri[i+1:]
	} else if !strings.Contains(uri, "=") || strings.Contains(uri, "://") {
		base = uri + "?"
	} else {
		base = ""
		query = uri
	}
	qs := newOmap()
	for _, part := range strings.Split(query, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		ku, err := url.QueryUnescape(k)
		if err == nil {
			k = ku
		}
		vu, err := url.QueryUnescape(v)
		if err == nil {
			v = vu
		}
		qs.Set(k, v)
	}
	for _, k := range args.keys {
		qs.Set(k, args.m[k])
	}
	var parts []string
	for _, k := range qs.keys {
		v := qs.m[k]
		if v == nil {
			continue
		}
		if b, ok := v.(bool); ok {
			if !b {
				continue // add_query_arg drops false
			}
			v = "1"
		}
		parts = append(parts, k+"="+phpString(v))
	}
	ret := strings.Join(parts, "&")
	ret = strings.Trim(ret, "?")
	ret = emptyArgRe.ReplaceAllString(ret, "$1")
	out := base + ret + frag
	return strings.TrimRight(out, "?")
}

var emptyArgRe = regexp.MustCompile(`=(&|$)`)

// embedHTML mirrors Embed::get_embed_html. frame holds extra iframe attributes (ordered).
func embedHTML(videoURL string, params *omap, privacy bool, start int, lazy bool, frame *omap) string {
	provider, _, ok := videoProperties(videoURL)
	if !ok {
		return ""
	}
	src, ok := embedURL(videoURL, params, privacy, start)
	if !ok {
		return ""
	}
	attrs := newOmap()
	attrs.Set("class", "elementor-video-iframe")
	attrs.Set("0", "allowfullscreen")
	attrs.Set("allow", "clipboard-write")
	attrs.Set("title", provider+" Video Player")
	if !lazy {
		attrs.Set("src", src)
	} else {
		attrs.Set("data-lazy-load", src)
	}
	if params != nil && params.Has("autoplay") {
		attrs.Set("allow", "autoplay")
	}
	if frame != nil {
		for _, k := range frame.keys {
			attrs.Set(k, frame.m[k])
		}
	}
	var out []string
	for _, k := range attrs.keys {
		v := escAttr(phpString(attrs.m[k]))
		if numericString(k) {
			out = append(out, v)
		} else {
			out = append(out, k+`="`+v+`"`)
		}
	}
	return "<iframe " + strings.Join(out, " ") + "></iframe>"
}
