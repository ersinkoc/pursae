package wp

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var accentMap = map[rune]string{
	'ı': "i", 'İ': "I", 'ş': "s", 'Ş': "S", 'ğ': "g", 'Ğ': "G", 'ç': "c", 'Ç': "C", 'ö': "o", 'Ö': "O",
	'ü': "u", 'Ü': "U", 'ß': "ss", 'æ': "ae", 'Æ': "AE", 'ø': "o", 'Ø': "O", 'œ': "oe", 'Œ': "OE",
	'đ': "d", 'Đ': "D", 'ł': "l", 'Ł': "L", 'þ': "th", 'Þ': "TH", 'ð': "d", 'Ð': "D",
}

// RemoveAccents is remove_accents(): maps accented Latin letters to ASCII.
func RemoveAccents(s string) string {
	var sb strings.Builder
	for _, r := range norm.NFD.String(s) {
		if rep, ok := accentMap[r]; ok {
			sb.WriteString(rep)
			continue
		}
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		sb.WriteRune(r)
	}
	return norm.NFC.String(sb.String())
}

var (
	tagRe         = regexp.MustCompile(`<[^>]*>`)
	entityRe      = regexp.MustCompile(`&[^;\s]+;`)
	slugInvalidRe = regexp.MustCompile(`[^a-z0-9_\-]+`)
	dashesRe      = regexp.MustCompile(`-+`)
)

// SanitizeTitle is sanitize_title(): a lowercase, dash-separated slug.
func SanitizeTitle(title string) string {
	s := StripTags(title)
	s = entityRe.ReplaceAllString(s, "")
	s = RemoveAccents(s)
	s = strings.ToLower(s)
	s = strings.NewReplacer(".", "-", "/", "-", " ", "-", " ", "-", "–", "-", "—", "-").Replace(s)
	s = slugInvalidRe.ReplaceAllString(s, "")
	s = dashesRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// StripTags is wp_strip_all_tags() without whitespace collapsing.
func StripTags(s string) string {
	s = regexp.MustCompile(`(?is)<(script|style)[^>]*?>.*?</(script|style)>`).ReplaceAllString(s, "")
	return tagRe.ReplaceAllString(s, "")
}

// TrimWords is wp_trim_words(): the first n words of the text content followed by more.
func TrimWords(text string, n int, more string) string {
	text = html.UnescapeString(StripTags(text))
	words := strings.Fields(text)
	if len(words) <= n {
		return html.EscapeString(strings.Join(words, " "))
	}
	return html.EscapeString(strings.Join(words[:n], " ")) + more
}

// SplitMore splits post content at the <!--more--> tag. teaser is the part before
// it, or the whole content when there is no tag; hasMore reports whether it was found.
func SplitMore(content string) (teaser string, rest string, moreText string, hasMore bool) {
	re := regexp.MustCompile(`<!--more(.*?)?-->`)
	loc := re.FindStringSubmatchIndex(content)
	if loc == nil {
		return content, "", "", false
	}
	moreText = strings.TrimSpace(content[loc[2]:loc[3]])
	return content[:loc[0]], content[loc[1]:], moreText, true
}

var (
	blockElems   = `(?:table|thead|tfoot|caption|col|colgroup|tbody|tr|td|th|div|dl|dd|dt|ul|ol|li|pre|form|map|area|blockquote|address|math|style|p|h[1-6]|hr|fieldset|legend|section|article|aside|hgroup|header|footer|nav|figure|figcaption|details|menu|summary)`
	openBlockRe  = regexp.MustCompile(`(?i)(<` + blockElems + `[\s/>])`)
	closeBlockRe = regexp.MustCompile(`(?i)(</` + blockElems + `>)`)
	pEmptyRe     = regexp.MustCompile(`<p>\s*</p>`)
	pAroundOpen  = regexp.MustCompile(`(?i)<p>\s*(</?` + blockElems + `[^>]*>)`)
	pAroundClose = regexp.MustCompile(`(?i)(</?` + blockElems + `[^>]*>)\s*</p>`)
	brAfterBlock = regexp.MustCompile(`(?i)(</?` + blockElems + `[^>]*>)\s*<br />`)
	brBeforeBlk  = regexp.MustCompile(`(?i)<br />(\s*</?(?:p|li|div|dl|dd|dt|th|pre|td|ul|ol)[^>]*>)`)
	multiNL      = regexp.MustCompile(`\n\s*\n`)
)

// Autop is a compact port of wpautop(): double line breaks become paragraphs,
// single ones become <br />, block-level elements are left unwrapped.
func Autop(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n") + "\n"

	// Keep <pre> contents untouched.
	var pres []string
	preRe := regexp.MustCompile(`(?is)<pre[^>]*>.*?</pre>`)
	text = preRe.ReplaceAllStringFunc(text, func(m string) string {
		pres = append(pres, m)
		return "<pre wp-pre-tag-" + strconv.Itoa(len(pres)-1) + "></pre>"
	})

	text = openBlockRe.ReplaceAllString(text, "\n\n$1")
	text = closeBlockRe.ReplaceAllString(text, "$1\n\n")
	text = multiNL.ReplaceAllString(text, "\n\n")

	var sb strings.Builder
	for _, chunk := range strings.Split(text, "\n\n") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		sb.WriteString("<p>" + chunk + "</p>\n")
	}
	out := sb.String()
	out = pEmptyRe.ReplaceAllString(out, "")
	out = pAroundOpen.ReplaceAllString(out, "$1")
	out = pAroundClose.ReplaceAllString(out, "$1")
	// Single newlines inside paragraphs become line breaks.
	out = regexp.MustCompile(`(?s)<p>(.*?)</p>`).ReplaceAllStringFunc(out, func(p string) string {
		inner := p[3 : len(p)-4]
		return "<p>" + strings.ReplaceAll(strings.TrimSpace(inner), "\n", "<br />\n") + "</p>"
	})
	out = brAfterBlock.ReplaceAllString(out, "$1")
	out = brBeforeBlk.ReplaceAllString(out, "$1")

	for i, pre := range pres {
		out = strings.Replace(out, "<pre wp-pre-tag-"+strconv.Itoa(i)+"></pre>", pre, 1)
	}
	return out
}

// EscText escapes <, > and & but leaves quotes alone so Texturize can still curl them.
func EscText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Texturize is a small subset of wptexturize(): curly quotes, dashes and ellipses in plain text.
func Texturize(s string) string {
	if strings.ContainsAny(s, "<>") {
		return s
	}
	r := strings.NewReplacer(" -- ", " &#8212; ", "---", "&#8212;", " - ", " &#8211; ", "--", "&#8211;", "...", "&#8230;")
	s = r.Replace(s)
	s = regexp.MustCompile(`(^|[\s(\[{])'`).ReplaceAllString(s, "$1&#8216;")
	s = regexp.MustCompile(`'`).ReplaceAllString(s, "&#8217;")
	s = regexp.MustCompile(`(^|[\s(\[{])"`).ReplaceAllString(s, "$1&#8220;")
	s = regexp.MustCompile(`"`).ReplaceAllString(s, "&#8221;")
	return s
}
