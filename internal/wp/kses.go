package wp

import (
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	ksesOnce   sync.Once
	ksesPolicy *bluemonday.Policy
	// keptCommentRe finds the comments kses keeps: block delimiters and the <!--more-->,
	// <!--nextpage--> and <!--noteaser--> tags. None may hold ">", so a kept comment can never be
	// closed early by "--!>" and let markup inside it through.
	keptCommentRe = regexp.MustCompile(`<!--\s+/?wp:[^>]*?-->|<!--(?:more[^>]*?|nextpage|noteaser)-->`)
	// mediaURLRe accepts a relative URL or one with an allowed protocol for the media src/poster
	// attributes bluemonday does not check itself (wp_kses_bad_protocol() on every URI attribute).
	mediaURLRe  = regexp.MustCompile(`(?i)^\s*(?:(?:` + strings.Join(AllowedProtocols, "|") + `):|[^:/?#]*(?:[/?#]|$))`)
	blockMarkRe = regexp.MustCompile("\x00WPBLOCK([0-9]+)\x00")
	// styleAttrRe finds style attributes in bluemonday output, where attribute values are always
	// double-quoted and escaped (a literal " never appears inside one, nor in text).
	styleAttrRe = regexp.MustCompile(`\sstyle="([^"]*)"`)

	ksesAdminOnce   sync.Once
	ksesAdminPolicy *bluemonday.Policy

	ksesDataOnce   sync.Once
	ksesDataPolicy *bluemonday.Policy
)

// KsesPost is wp_kses_post() for users without unfiltered_html: it keeps the tags
// and attributes WordPress allows in post content and preserves block delimiters
// and <!--more--> so the block structure survives sanitizing.
func KsesPost(html string) string {
	ksesOnce.Do(func() { ksesPolicy = newPostPolicy() })
	return ksesWith(ksesPolicy, html)
}

// KsesAdmin sanitises HTML a plugin prints inside the admin (its own pages, dashboard widgets and
// meta boxes): everything KsesPost keeps plus form controls (input, select, textarea, button,
// label, fieldset). There is no <form> element, no formaction and no form attribute, so the
// controls can only ever be submitted by the form the core wraps around them; scripts, event
// handlers, iframes and file inputs are removed as in KsesPost.
func KsesAdmin(html string) string {
	ksesAdminOnce.Do(func() {
		p := newPostPolicy()
		p.AllowElements("fieldset", "legend", "label", "select", "option", "optgroup", "textarea", "button", "input")
		p.AllowAttrs("for").OnElements("label")
		p.AllowAttrs("name", "disabled", "required").OnElements("input", "select", "textarea", "button", "fieldset")
		p.AllowAttrs("value").OnElements("input", "option", "button")
		p.AllowAttrs("type").Matching(regexp.MustCompile(`(?i)^(text|number|email|url|tel|search|checkbox|radio|hidden|date|time|datetime-local|month|week|color|range|submit|button|reset)$`)).OnElements("input")
		p.AllowAttrs("type").Matching(regexp.MustCompile(`(?i)^(submit|button|reset)$`)).OnElements("button")
		p.AllowAttrs("checked", "readonly", "placeholder", "min", "max", "step", "maxlength", "minlength", "pattern", "size", "autocomplete").OnElements("input", "textarea")
		p.AllowAttrs("rows", "cols").Matching(bluemonday.Integer).OnElements("textarea")
		p.AllowAttrs("multiple").OnElements("select")
		p.AllowAttrs("selected", "label").OnElements("option", "optgroup")
		p.AllowAttrs("colspan", "rowspan", "scope").OnElements("td", "th")
		ksesAdminPolicy = p
	})
	return ksesWith(ksesAdminPolicy, html)
}

// newPostPolicy builds the wp_kses_post() policy.
func newPostPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowElements("figure", "figcaption", "section", "article", "aside", "header", "footer", "nav", "main",
		"mark", "small", "sub", "sup", "time", "cite", "abbr", "address", "details", "summary", "audio", "video",
		"source", "track", "picture", "u", "s", "del", "ins", "kbd", "code", "pre", "hr", "br", "span", "div")
	p.AllowAttrs("class", "id", "style", "title", "lang", "dir", "role").Globally()
	p.AllowAttrs("aria-label", "aria-hidden", "aria-describedby", "aria-labelledby").Globally()
	p.AllowAttrs("data-id", "data-type", "data-align").Globally()
	p.AllowAttrs("datetime").OnElements("time", "del", "ins")
	p.AllowAttrs("controls", "preload", "loop", "muted", "autoplay", "playsinline").OnElements("audio", "video", "source")
	p.AllowAttrs("srcset", "sizes", "width", "height", "loading", "decoding").OnElements("img", "source")
	p.AllowAttrs("src", "poster").Matching(mediaURLRe).OnElements("audio", "video", "source")
	p.AllowAttrs("target", "rel").OnElements("a")
	p.AllowAttrs("start").Matching(bluemonday.Integer).OnElements("ol")
	p.AllowAttrs("reversed").Matching(regexp.MustCompile(`(?i)^(|reversed)$`)).OnElements("ol")
	// WordPress keeps post links as written: every wp_allowed_protocols() scheme (tel:, sms:, …)
	// and no rel="nofollow" (that is only added to comments).
	p.AllowURLSchemes(AllowedProtocols...)
	p.RequireNoFollowOnLinks(false)
	p.AllowStyling()
	p.AllowUnsafe(false)
	return p
}

// ksesWith sanitises html with policy, keeping block delimiters and the <!--more--> family.
func ksesWith(policy *bluemonday.Policy, html string) string {
	// wp_kses_no_null(): a NUL could otherwise forge the placeholder used below.
	html = strings.ReplaceAll(html, "\x00", "")
	// bluemonday drops comments, so lift the kept ones out and put them back afterwards. The
	// placeholders are numbered, so one dropped with its element does not shift the others.
	var saved []string
	marked := keptCommentRe.ReplaceAllStringFunc(html, func(m string) string {
		saved = append(saved, m)
		return "\x00WPBLOCK" + strconv.Itoa(len(saved)-1) + "\x00"
	})
	clean := safeStyles(policy.Sanitize(marked))
	return blockMarkRe.ReplaceAllStringFunc(clean, func(m string) string {
		i, err := strconv.Atoi(m[len("\x00WPBLOCK") : len(m)-1])
		if err != nil || i >= len(saved) {
			return ""
		}
		return saved[i]
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

var (
	anchorTagRe = regexp.MustCompile(`<a\s[^>]*>`)
	relAttrRe   = regexp.MustCompile(`\srel="([^"]*)"`)
)

// RelUGC is wp_rel_ugc() for HTML that went through KsesPost or KsesData (attribute values double-
// quoted, no ">" inside them): every link gets rel="nofollow ugc", keeping the rel values it had.
// WordPress adds it to comments, which KsesPost itself leaves alone like any post content.
func RelUGC(clean string) string {
	if !strings.Contains(clean, "<a ") {
		return clean
	}
	return anchorTagRe.ReplaceAllStringFunc(clean, func(tag string) string {
		if m := relAttrRe.FindStringSubmatchIndex(tag); m != nil {
			rel := strings.Fields(tag[m[2]:m[3]])
			for _, want := range []string{"nofollow", "ugc"} {
				if !slices.Contains(rel, want) {
					rel = append(rel, want)
				}
			}
			return tag[:m[2]] + strings.Join(rel, " ") + tag[m[3]:]
		}
		end := len(tag) - 1
		if strings.HasSuffix(tag, "/>") {
			end--
		}
		return tag[:end] + ` rel="nofollow ugc"` + tag[end:]
	})
}
