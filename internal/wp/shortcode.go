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
	expand := func(full string, m shortcodeMatch) string {
		if m.lead == "[" && m.trail == "]" {
			return full[1 : len(full)-1] // escaped
		}
		attrs := ParseShortcodeAttrs(m.attrs)
		return m.lead + fn(m.tag, attrs, unhideBrackets(m.content)) + m.trail
	}
	if !strings.Contains(content, "<") {
		return replaceShortcodes(content, re, expand)
	}
	content = shortcodesInHTMLTags(content, re, expand)
	return unhideBrackets(replaceShortcodes(content, re, expand))
}

// shortcodeExpander replaces one matched shortcode (full is its whole text).
type shortcodeExpander func(full string, m shortcodeMatch) string

// shortcodeMatch is one shortcode found by nextShortcode: content[start:end] is the whole match.
type shortcodeMatch struct {
	start, end                       int
	lead, tag, attrs, content, trail string
}

// nextShortcode finds the first shortcode at or after from, like one match of WordPress'
// get_shortcode_regex(). re matches only the opening tag; the content of an enclosing shortcode
// runs to the first [/tag] after it (the same tag does not nest) and is found with one
// strings.Index. noCloser records, per tag, an offset after which no [/tag] exists, so a tag
// without a closer is searched for once per call instead of once per opener: scanning stays
// linear in the size of content (a regex with an optional lazy content group rescans the rest
// of the input for every unclosed opener).
func nextShortcode(content string, re *regexp.Regexp, from int, noCloser map[string]int) (shortcodeMatch, bool) {
	loc := re.FindStringSubmatchIndex(content[from:])
	if loc == nil {
		return shortcodeMatch{}, false
	}
	m := shortcodeMatch{start: from + loc[0], end: from + loc[1]}
	if loc[2] >= 0 {
		m.lead = content[from+loc[2] : from+loc[3]]
	}
	selfClosing := false
	// Groups: 1 escape "[", then three per tag (tag, attributes, self-closing "/").
	for g := 2; 2*g+5 < len(loc); g += 3 {
		if loc[2*g] < 0 {
			continue
		}
		m.tag = content[from+loc[2*g] : from+loc[2*g+1]]
		if loc[2*g+2] >= 0 {
			m.attrs = content[from+loc[2*g+2] : from+loc[2*g+3]]
		}
		selfClosing = loc[2*g+4] >= 0
		break
	}
	if !selfClosing {
		if after, ok := noCloser[m.tag]; !ok || m.end < after {
			closer := "[/" + m.tag + "]"
			if i := strings.Index(content[m.end:], closer); i >= 0 {
				m.content = content[m.end : m.end+i]
				m.end += i + len(closer)
			} else {
				noCloser[m.tag] = m.end
			}
		}
	}
	if m.end < len(content) && content[m.end] == ']' {
		m.trail = "]"
		m.end++
	}
	return m, true
}

// replaceShortcodes replaces every shortcode in content with expand, left to right.
func replaceShortcodes(content string, re *regexp.Regexp, expand shortcodeExpander) string {
	noCloser := map[string]int{}
	var b strings.Builder
	pos := 0
	for pos < len(content) {
		m, ok := nextShortcode(content, re, pos, noCloser)
		if !ok {
			break
		}
		if b.Len() == 0 {
			b.Grow(len(content))
		}
		b.WriteString(content[pos:m.start])
		b.WriteString(expand(content[m.start:m.end], m))
		pos = m.end
	}
	if pos == 0 {
		return content
	}
	b.WriteString(content[pos:])
	return b.String()
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
func shortcodesInHTMLTags(content string, re *regexp.Regexp, expand shortcodeExpander) string {
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
func shortcodesInAttributes(el string, re *regexp.Regexp, expand shortcodeExpander) string {
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
func attrWithShortcodes(name, raw string, re *regexp.Regexp, expand shortcodeExpander) (string, bool) {
	if !strings.Contains(raw, "[") || !strings.Contains(raw, "]") {
		return "", false
	}
	if strings.HasPrefix(name, "on") || name == "srcdoc" {
		return "", false
	}
	n := 0
	out := replaceShortcodes(raw, re, func(full string, m shortcodeMatch) string {
		n++
		return expand(full, m)
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
	key := strings.Join(tags, "\x00")
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

// buildShortcodeRegex builds the opening-tag part of the WordPress shortcode regex for the given
// tags; nextShortcode finds the content and the closing tag.
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
	// Per tag the groups are: tag, attributes, self-closing "/". The attribute group must start with
	// whitespace, so [boxes] never matches the tag "box" (WordPress uses a lookahead). The optional
	// content up to [/tag] (WordPress' \[\/\2\]) is deliberately not part of the regex: RE2 would keep
	// that branch alive to the end of the input for every opener without a closer (quadratic time).
	alts := make([]string, len(names))
	for i, n := range names {
		alts[i] = `(` + n + `)((?:\s(?:[^\]\/]|\/[^\]])*)?)(?:(\/)\]|\])`
	}
	// Groups: 1 escape "[", then three per tag; the escape "]" is checked by nextShortcode.
	return regexp.MustCompile(`(\[?)\[(?:` + strings.Join(alts, "|") + `)`)
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
		if m, ok := nextShortcode(inner, re, 0, map[string]int{}); ok && m.start == 0 && m.end == len(inner) {
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
