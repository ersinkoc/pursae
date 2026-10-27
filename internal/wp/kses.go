package wp

import (
	"regexp"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	ksesOnce   sync.Once
	ksesPolicy *bluemonday.Policy
	blockDelim = regexp.MustCompile(`<!--\s+/?wp:[^>]*?-->`)
)

// KsesPost is wp_kses_post() for users without unfiltered_html: it keeps the tags
// and attributes WordPress allows in post content and preserves block delimiters
// and <!--more--> so the block structure survives sanitizing.
func KsesPost(html string) string {
	ksesOnce.Do(func() {
		p := bluemonday.UGCPolicy()
		p.AllowElements("figure", "figcaption", "section", "article", "aside", "header", "footer", "nav", "main",
			"mark", "small", "sub", "sup", "time", "cite", "abbr", "address", "details", "summary", "audio", "video",
			"source", "track", "picture", "u", "s", "del", "ins", "kbd", "code", "pre", "hr", "br", "span", "div")
		p.AllowAttrs("class", "id", "style", "title", "lang", "dir", "role").Globally()
		p.AllowAttrs("aria-label", "aria-hidden", "aria-describedby", "aria-labelledby").Globally()
		p.AllowAttrs("data-id", "data-type", "data-align").Globally()
		p.AllowAttrs("datetime").OnElements("time", "del", "ins")
		p.AllowAttrs("controls", "src", "poster", "preload", "loop", "muted", "autoplay", "playsinline").OnElements("audio", "video", "source")
		p.AllowAttrs("srcset", "sizes", "width", "height", "loading", "decoding").OnElements("img", "source")
		p.AllowAttrs("target", "rel").OnElements("a")
		p.AllowStyling()
		p.AllowUnsafe(false)
		ksesPolicy = p
	})
	// bluemonday drops comments, so lift block delimiters out and put them back afterwards.
	var saved []string
	marked := blockDelim.ReplaceAllStringFunc(html, func(m string) string {
		saved = append(saved, m)
		return "\x00WPBLOCK\x00"
	})
	marked = regexp.MustCompile(`<!--more(.*?)?-->`).ReplaceAllStringFunc(marked, func(m string) string {
		saved = append(saved, m)
		return "\x00WPBLOCK\x00"
	})
	clean := ksesPolicy.Sanitize(marked)
	i := 0
	return regexp.MustCompile("\x00WPBLOCK\x00").ReplaceAllStringFunc(clean, func(string) string {
		if i >= len(saved) {
			return ""
		}
		i++
		return saved[i-1]
	})
}
