package wp

import (
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Shortcodes follow WordPress' shortcode syntax (get_shortcode_regex()):
//
//	[tag]                    self-closing, no attributes
//	[tag attr="v" k='v' n=3 flag]
//	[tag]content[/tag]       enclosing
//	[tag /]                  explicitly self-closing
//	[[tag]]                  escaped: rendered as the literal text [tag]
//
// Only registered tags are recognised; anything else is left untouched, as in
// WordPress. Enclosing shortcodes do not nest the same tag inside themselves.

// ShortcodeFunc renders one shortcode. attrs holds the named attributes plus
// positional ones under "0", "1", … content is the enclosed text (empty for
// self-closing tags). The return value replaces the whole shortcode.
type ShortcodeFunc func(tag string, attrs map[string]string, content string) string

// ValidShortcodeTag reports whether tag is an acceptable shortcode name:
// 1–64 lowercase letters, digits, underscores and dashes.
func ValidShortcodeTag(tag string) bool {
	return shortcodeTagRe.MatchString(tag)
}

var shortcodeTagRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// DoShortcode expands every occurrence of the given tags in content with fn.
// It mirrors do_shortcode(): escaped shortcodes ([[tag]]) lose one pair of
// brackets, unknown tags are left alone, and a <p> that holds nothing but a
// shortcode is removed first (shortcode_unautop()).
//
// Like do_shortcodes_in_html_tags(), shortcodes inside HTML markup are not
// expanded as text: inside an attribute value the output is escaped for the
// attribute (and dropped when the attribute cannot safely hold it), and
// brackets anywhere else in a tag, comment or CDATA section are left alone.
func DoShortcode(content string, tags []string, fn ShortcodeFunc) string {
	if len(tags) == 0 || !strings.Contains(content, "[") {
		return content
	}
	re := shortcodeRegex(tags)
	if re == nil {
		return content
	}
	content = unautopShortcodes(content, re)
	expand := func(m string) string {
		lead, tag, rawAttrs, content, trail := shortcodeParts(re.FindStringSubmatch(m))
		if lead == "[" && trail == "]" {
			return m[1 : len(m)-1] // escaped
		}
		attrs := ParseShortcodeAttrs(rawAttrs)
		return lead + fn(tag, attrs, unhideBrackets(content)) + trail
	}
	if !strings.Contains(content, "<") {
		return re.ReplaceAllStringFunc(content, expand)
	}
	content = shortcodesInHTMLTags(content, re, expand)
	return unhideBrackets(re.ReplaceAllStringFunc(content, expand))
}

// Brackets inside markup are swapped for these private-use characters while
// the text is expanded (WordPress uses &#91; / &#93;), then swapped back.
const (
	hiddenOpen  = "\uE000"
	hiddenClose = "\uE001"
)

var (
	hideBrackets   = strings.NewReplacer("[", hiddenOpen, "]", hiddenClose)
	unhideReplacer = strings.NewReplacer(hiddenOpen, "[", hiddenClose, "]")
	// sentinelEscaper keeps characters that are already in the content intact.
	sentinelEscaper = strings.NewReplacer(hiddenOpen, "&#xE000;", hiddenClose, "&#xE001;")
)

func unhideBrackets(s string) string {
	if !strings.ContainsAny(s, hiddenOpen+hiddenClose) {
		return s
	}
	return unhideReplacer.Replace(s)
}

// shortcodesInHTMLTags is do_shortcodes_in_html_tags(): it expands shortcodes
// found in attribute values (escaped for the attribute) and hides every other
// bracket inside tags, comments and CDATA sections from the text pass.
func shortcodesInHTMLTags(content string, re *regexp.Regexp, expand func(string) string) string {
	if strings.ContainsAny(content, hiddenOpen+hiddenClose) {
		content = sentinelEscaper.Replace(content)
	}
	var b strings.Builder
	b.Grow(len(content))
	for i := 0; i < len(content); {
		lt := strings.IndexByte(content[i:], '<')
		if lt < 0 {
			b.WriteString(content[i:])
			break
		}
		b.WriteString(content[i : i+lt])
		i += lt
		end, kind := htmlElementEnd(content, i)
		if kind == elemText {
			b.WriteByte('<')
			i++
			continue
		}
		el := content[i:end]
		i = end
		if strings.Contains(el, "[") && strings.Contains(el, "]") && kind == elemStartTag {
			el = shortcodesInAttributes(el, re, expand)
		}
		b.WriteString(hideBrackets.Replace(el))
	}
	return b.String()
}

type elemKind int

const (
	elemText elemKind = iota // a "<" that does not open markup
	elemStartTag
	elemOther // end tag, comment, CDATA, doctype, processing instruction
)

// htmlElementEnd returns the end of the markup starting at content[i] == '<'.
// Attribute values are skipped as a browser does, so a ">" inside a quoted
// value does not end the tag. Unterminated markup runs to the end.
func htmlElementEnd(content string, i int) (int, elemKind) {
	rest := content[i:]
	closeAt := func(marker string, from int) int {
		if j := strings.Index(rest[from:], marker); j >= 0 {
			return i + from + j + len(marker)
		}
		return len(content)
	}
	switch {
	case strings.HasPrefix(rest, "<!--"):
		return closeAt("-->", 4), elemOther
	case strings.HasPrefix(rest, "<![CDATA["):
		return closeAt("]]>", 9), elemOther
	case len(rest) > 1 && (rest[1] == '/' || rest[1] == '!' || rest[1] == '?'):
		return closeAt(">", 1), elemOther
	case len(rest) > 1 && isASCIILetter(rest[1]):
	default:
		return i + 1, elemText
	}
	j := 1
	for j < len(rest) {
		switch c := rest[j]; c {
		case '>':
			return i + j + 1, elemStartTag
		case '"', '\'':
			// Only a quote that starts a value (after "=") is a delimiter.
			k := j - 1
			for k > 0 && isHTMLSpace(rest[k]) {
				k--
			}
			if rest[k] == '=' {
				if q := strings.IndexByte(rest[j+1:], c); q >= 0 {
					j += q + 2
					continue
				}
				return len(content), elemStartTag
			}
		}
		j++
	}
	return len(content), elemStartTag
}

func isASCIILetter(c byte) bool { return (c|0x20) >= 'a' && (c|0x20) <= 'z' }

func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// uriAttributes is wp_kses_uri_attributes().
var uriAttributes = map[string]bool{
	"action": true, "archive": true, "background": true, "cite": true, "classid": true, "codebase": true,
	"data": true, "formaction": true, "href": true, "icon": true, "longdesc": true, "manifest": true,
	"poster": true, "profile": true, "src": true, "usemap": true, "xmlns": true,
}

// shortcodesInAttributes expands shortcodes in the attribute values of one
// start tag. The output is sanitised like wp_kses_one_attr(): escaped for the
// attribute, URL attributes keep allowed protocols only and style goes through
// SafeCSS. An attribute that cannot hold it (event handlers, srcdoc, a bad
// URL) keeps its original text.
func shortcodesInAttributes(el string, re *regexp.Regexp, expand func(string) string) string {
	var b strings.Builder
	j := 1
	for j < len(el) && !isHTMLSpace(el[j]) && el[j] != '>' && el[j] != '/' {
		j++
	}
	b.WriteString(el[:j])
	for j < len(el) {
		// Whitespace and stray slashes between attributes.
		k := j
		for k < len(el) && (isHTMLSpace(el[k]) || el[k] == '/') {
			k++
		}
		b.WriteString(el[j:k])
		j = k
		if j >= len(el) || el[j] == '>' {
			b.WriteString(el[j:])
			return b.String()
		}
		// Attribute name.
		k = j
		for k < len(el) && !isHTMLSpace(el[k]) && el[k] != '=' && el[k] != '>' && el[k] != '/' {
			k++
		}
		if k == j { // a lone "=": copy it
			k++
		}
		name := strings.ToLower(el[j:k])
		b.WriteString(el[j:k])
		j = k
		// Optional "= value".
		k = j
		for k < len(el) && isHTMLSpace(el[k]) {
			k++
		}
		if k >= len(el) || el[k] != '=' {
			continue
		}
		k++
		for k < len(el) && isHTMLSpace(el[k]) {
			k++
		}
		eq := el[j:k]
		var raw string
		quote := byte(0)
		if k < len(el) && (el[k] == '"' || el[k] == '\'') {
			quote = el[k]
			q := strings.IndexByte(el[k+1:], quote)
			if q < 0 {
				b.WriteString(el[j:])
				return b.String()
			}
			raw = el[k+1 : k+1+q]
			j = k + q + 2
		} else {
			e := k
			for e < len(el) && !isHTMLSpace(el[e]) && el[e] != '>' {
				e++
			}
			raw = el[k:e]
			j = e
		}
		if v, ok := attrWithShortcodes(name, raw, re, expand); ok {
			b.WriteString("=\"" + v + "\"")
			continue
		}
		b.WriteString(eq)
		if quote != 0 {
			b.WriteByte(quote)
			b.WriteString(raw)
			b.WriteByte(quote)
		} else {
			b.WriteString(raw)
		}
	}
	return b.String()
}

// attrWithShortcodes expands the shortcodes in one attribute value and
// returns the value escaped for a double-quoted attribute; ok is false when
// nothing was expanded or the result is not safe for this attribute.
func attrWithShortcodes(name, raw string, re *regexp.Regexp, expand func(string) string) (string, bool) {
	if !strings.Contains(raw, "[") || !strings.Contains(raw, "]") {
		return "", false
	}
	if strings.HasPrefix(name, "on") || name == "srcdoc" {
		return "", false
	}
	n := 0
	out := re.ReplaceAllStringFunc(raw, func(m string) string {
		n++
		return expand(m)
	})
	if n == 0 || out == raw {
		return "", false
	}
	val := html.UnescapeString(out)
	switch {
	case uriAttributes[name]:
		if scheme, ok := urlScheme(val); ok && !protocolAllowed(scheme) {
			return "", false
		}
	case name == "style":
		val = SafeCSS(val)
	}
	if strings.TrimSpace(val) == "" {
		return "", false
	}
	return html.EscapeString(val), true
}

func protocolAllowed(scheme string) bool {
	scheme = strings.ToLower(scheme)
	for _, p := range AllowedProtocols {
		if p == scheme {
			return true
		}
	}
	return false
}

// shortcodeRegexes caches shortcodeRegex by tag list: content filters run it for every post they
// render, and the tag lists are few (they change only when plugins do).
var (
	shortcodeRegexMu sync.Mutex
	shortcodeRegexes = map[string]*regexp.Regexp{}
)

// shortcodeRegex is the WordPress shortcode regex for the given tags (nil when none is valid).
func shortcodeRegex(tags []string) *regexp.Regexp {
	var kb strings.Builder
	for _, tag := range tags {
		kb.WriteString(strconv.Itoa(len(tag)))
		kb.WriteByte(':')
		kb.WriteString(tag)
	}
	key := kb.String()
	shortcodeRegexMu.Lock()
	re, ok := shortcodeRegexes[key]
	shortcodeRegexMu.Unlock()
	if ok {
		return re
	}
	re = buildShortcodeRegex(tags)
	shortcodeRegexMu.Lock()
	if len(shortcodeRegexes) >= 256 { // a bound against ever-changing tag lists
		clear(shortcodeRegexes)
	}
	shortcodeRegexes[key] = re
	shortcodeRegexMu.Unlock()
	return re
}

// buildShortcodeRegex builds the WordPress shortcode regex for the given tags.
func buildShortcodeRegex(tags []string) *regexp.Regexp {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		if ValidShortcodeTag(t) {
			names = append(names, regexp.QuoteMeta(t))
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) }) // longest first, like WordPress
	// Go's RE2 has no backreferences, so each tag gets its own alternative that closes with its own
	// [/tag] (WordPress' \[\/\2\]): [row][col]x[/col][/row] gives row the content [col]x[/col].
	// Per tag the groups are: tag, attributes, self-closing "/", content. The attribute group must
	// start with whitespace, so [boxes] never matches the tag "box" (WordPress uses a lookahead).
	// The content runs to the first [/tag], so the same tag does not nest, as in WordPress.
	alts := make([]string, len(names))
	for i, n := range names {
		alts[i] = `(` + n + `)((?:\s(?:[^\]\/]|\/[^\]])*)?)(?:(\/)\]|\](?:((?s:.*?))\[\/` + n + `\])?)`
	}
	// Groups: 1 escape "[", then four per tag, then the escape "]".
	return regexp.MustCompile(`(\[?)\[(?:` + strings.Join(alts, "|") + `)(\]?)`)
}

// shortcodeParts splits a shortcodeRegex submatch into its leading "[", tag, attributes, content
// and trailing "]".
func shortcodeParts(sub []string) (lead, tag, attrs, content, trail string) {
	if len(sub) < 6 {
		return
	}
	lead, trail = sub[1], sub[len(sub)-1]
	for i := 2; i+3 < len(sub)-1; i += 4 {
		if sub[i] != "" {
			return lead, sub[i], sub[i+1], sub[i+3], trail
		}
	}
	return
}

// lonePShortcodeRe is a whole paragraph (no "</p>" inside), which must be one shortcode.
var lonePShortcodeRe = regexp.MustCompile(`<p>\s*(\[(?:[^<]|<(?:[^/]|/[^p]|/p[^>]))*?\])\s*</p>`)

// unautopShortcodes removes the <p>…</p> wpautop() wraps around a lone shortcode.
func unautopShortcodes(content string, re *regexp.Regexp) string {
	if !strings.Contains(content, "<p>") {
		return content
	}
	return lonePShortcodeRe.ReplaceAllStringFunc(content, func(m string) string {
		inner := lonePShortcodeRe.FindStringSubmatch(m)[1]
		if loc := re.FindStringIndex(inner); loc != nil && loc[0] == 0 && loc[1] == len(inner) {
			return inner
		}
		return m
	})
}

// shortcodeAttrRe is get_shortcode_atts_regex(): attribute names may hold dashes (data-id="5").
var shortcodeAttrRe = regexp.MustCompile(`([\w-]+)\s*=\s*"([^"]*)"|([\w-]+)\s*=\s*'([^']*)'|([\w-]+)\s*=\s*([^\s'"]+)|"([^"]*)"|'([^']*)'|(\S+)`)

// ParseShortcodeAttrs is shortcode_parse_atts(): named attributes keep their
// (lower-cased) name, bare words and quoted strings become positional "0",
// "1", … values.
func ParseShortcodeAttrs(text string) map[string]string {
	out := map[string]string{}
	text = strings.TrimSpace(text)
	if text == "" {
		return out
	}
	pos := 0
	for _, m := range shortcodeAttrRe.FindAllStringSubmatch(text, -1) {
		switch {
		case m[1] != "":
			out[strings.ToLower(m[1])] = m[2]
		case m[3] != "":
			out[strings.ToLower(m[3])] = m[4]
		case m[5] != "":
			out[strings.ToLower(m[5])] = m[6]
		case m[7] != "":
			out[strconv.Itoa(pos)] = m[7]
			pos++
		case m[8] != "":
			out[strconv.Itoa(pos)] = m[8]
			pos++
		default:
			out[strconv.Itoa(pos)] = m[9]
			pos++
		}
	}
	return out
}

// HasShortcode reports whether content contains a shortcode with tag.
func HasShortcode(content, tag string) bool {
	if !strings.Contains(content, "["+tag) {
		return false
	}
	re := shortcodeRegex([]string{tag})
	return re != nil && re.MatchString(content)
}
