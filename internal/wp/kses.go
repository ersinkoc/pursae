package wp

import (
	"html"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	ksesOnce    sync.Once
	ksesPolicy  *bluemonday.Policy
	blockDelim  = regexp.MustCompile(`<!--\s+/?wp:[^>]*?-->`)
	moreTagRe   = regexp.MustCompile(`<!--more(.*?)?-->`)
	blockMarkRe = regexp.MustCompile("\x00WPBLOCK\x00")
	// styleAttrRe finds style attributes in bluemonday output, where attribute values are always
	// double-quoted and escaped (a literal " never appears inside one, nor in text).
	styleAttrRe = regexp.MustCompile(`\sstyle="([^"]*)"`)

	ksesDataOnce   sync.Once
	ksesDataPolicy *bluemonday.Policy
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
	marked = moreTagRe.ReplaceAllStringFunc(marked, func(m string) string {
		saved = append(saved, m)
		return "\x00WPBLOCK\x00"
	})
	clean := safeStyles(ksesPolicy.Sanitize(marked))
	i := 0
	return blockMarkRe.ReplaceAllStringFunc(clean, func(string) string {
		if i >= len(saved) {
			return ""
		}
		i++
		return saved[i-1]
	})
}

// safeStyles runs every style attribute of sanitized HTML through SafeCSS (safecss_filter_attr(),
// which wp_kses_attr_check() applies to style), dropping the attribute when nothing is left.
func safeStyles(clean string) string {
	if !strings.Contains(clean, " style=\"") {
		return clean
	}
	return styleAttrRe.ReplaceAllStringFunc(clean, func(m string) string {
		v := SafeCSS(html.UnescapeString(styleAttrRe.FindStringSubmatch(m)[1]))
		if v == "" {
			return ""
		}
		return ` style="` + html.EscapeString(v) + `"`
	})
}

// KsesData is wp_kses_data(): the small $allowedtags set WordPress allows in comments from users
// without unfiltered_html (pre_comment_content) — a, abbr, acronym, b, blockquote, cite, code, del,
// em, i, q, s, strike and strong with their few attributes; links keep allowed protocols only.
func KsesData(s string) string {
	ksesDataOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowElements("b", "cite", "code", "em", "i", "s", "strike", "strong")
		p.AllowAttrs("href", "title").OnElements("a")
		p.AllowAttrs("title").OnElements("abbr", "acronym")
		p.AllowAttrs("cite").OnElements("blockquote", "q")
		p.AllowAttrs("datetime").OnElements("del")
		p.AllowElements("a", "abbr", "acronym", "blockquote", "del", "q")
		p.AllowURLSchemes(AllowedProtocols...)
		p.AllowRelativeURLs(true)
		p.RequireParseableURLs(true)
		ksesDataPolicy = p
	})
	return ksesDataPolicy.Sanitize(s)
}
