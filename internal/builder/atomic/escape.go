package atomic

// Escaping helpers with PHP/WordPress semantics: htmlspecialchars, Twig's html_attr escaper,
// json_encode, strip_tags, esc_url, esc_attr and the wp_kses subset atomic text props use.

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// escapeHTML mirrors htmlspecialchars($s, ENT_QUOTES | ENT_SUBSTITUTE) (Twig's html strategy).
func escapeHTML(s string) string {
	if !strings.ContainsAny(s, "&<>\"'") && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i, w := 0, 0; i < len(s); i += w {
		r, size := utf8.DecodeRuneInString(s[i:])
		w = size
		if r == utf8.RuneError && size == 1 {
			b.WriteString("�")
			continue
		}
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#039;")
		default:
			b.WriteString(s[i : i+size])
		}
	}
	return b.String()
}

// escapeHTMLAttr mirrors Twig's html_attr escaping strategy.
func escapeHTMLAttr(s string) string {
	var b strings.Builder
	for i, w := 0, 0; i < len(s); i += w {
		r, size := utf8.DecodeRuneInString(s[i:])
		w = size
		c := s[i]
		if size == 1 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ',' || c == '.' || c == '-' || c == '_') {
			b.WriteByte(c)
			continue
		}
		if size == 1 {
			if (c <= 0x1F && c != '\t' && c != '\n' && c != '\r') || (c >= 0x7F && c <= 0x9F) {
				b.WriteString("&#xFFFD;")
				continue
			}
			switch c {
			case '"':
				b.WriteString("&quot;")
			case '&':
				b.WriteString("&amp;")
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			default:
				fmt.Fprintf(&b, "&#x%02X;", c)
			}
			continue
		}
		if r >= 0x7F && r <= 0x9F {
			b.WriteString("&#xFFFD;")
			continue
		}
		fmt.Fprintf(&b, "&#x%04X;", r)
	}
	return b.String()
}

func urlEncode(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

var entityRe = regexp.MustCompile(`^&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);`)

// escAttr mirrors esc_attr(): htmlspecialchars that leaves existing entities alone.
func escAttr(s string) string {
	if !strings.ContainsAny(s, "&<>\"'") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '&':
			if m := entityRe.FindString(s[i:]); m != "" {
				b.WriteString(m)
				i += len(m) - 1
				continue
			}
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#039;")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

var allowedProtocols = []string{"http", "https", "ftp", "ftps", "mailto", "news", "irc", "irc6", "ircs", "gopher", "nntp", "feed", "telnet", "mms", "rtsp", "sms", "svn", "tel", "fax", "xmpp", "webcal", "urn", "skype", "viber"}

var escURLStrip = regexp.MustCompile(`[^a-zA-Z0-9-~+_.?#=!&;,/:%@$|*'()\[\]\x{80}-\x{ffff}]`)

// escURL mirrors esc_url() for display.
func escURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	u = strings.ReplaceAll(u, " ", "%20")
	u = escURLStrip.ReplaceAllString(u, "")
	if u == "" {
		return ""
	}
	if strings.Contains(u, ":") && !strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "#") && !strings.HasPrefix(u, "?") {
		scheme, _, _ := strings.Cut(u, ":")
		if !strings.ContainsAny(scheme, "/?#") {
			ok := false
			for _, p := range allowedProtocols {
				if strings.EqualFold(scheme, p) {
					ok = true
					break
				}
			}
			if !ok {
				return ""
			}
		}
	} else if !strings.Contains(u, ":") && !strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "#") && !strings.HasPrefix(u, "?") && !strings.HasPrefix(u, "&") && !strings.HasSuffix(strings.SplitN(u, "/", 2)[0], ".php") {
		u = "http://" + u
	}
	var b strings.Builder
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c == '&' {
			if m := entityRe.FindString(u[i:]); m != "" {
				if m == "&amp;" {
					b.WriteString("&#038;")
				} else {
					b.WriteString(m)
				}
				i += len(m) - 1
				continue
			}
			b.WriteString("&#038;")
			continue
		}
		if c == '\'' {
			b.WriteString("&#039;")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// allowedWrapperTags mirrors Utils::get_allowed_html_wrapper_tags() with the atomic module's
// inline tags added.
var allowedWrapperTags = map[string]bool{"a": true, "article": true, "aside": true, "button": true, "form": true, "div": true,
	"footer": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true, "main": true,
	"nav": true, "p": true, "section": true, "span": true, "b": true, "i": true, "em": true, "u": true, "ul": true, "ol": true,
	"li": true, "blockquote": true, "del": true, "br": true, "strong": true, "sup": true, "sub": true, "s": true}

// validateHTMLTag mirrors Utils::validate_html_tag.
func validateHTMLTag(tag string) string {
	if tag != "" && allowedWrapperTags[strings.ToLower(tag)] {
		return tag
	}
	return "div"
}

// phpJSONEncode mirrors json_encode() with default flags (escaped slashes and unicode).
func phpJSONEncode(v any) string {
	var b strings.Builder
	writePHPJSON(&b, v)
	return b.String()
}

func writePHPJSON(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if x == float64(int64(x)) && x < 1e15 && x > -1e15 {
			b.WriteString(strconv.FormatInt(int64(x), 10))
		} else {
			b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case string:
		writePHPJSONString(b, x)
	case Markup:
		writePHPJSONString(b, string(x))
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writePHPJSON(b, e)
		}
		b.WriteByte(']')
	case *Map:
		if x.Len() == 0 {
			b.WriteString("[]")
			return
		}
		// a PHP array with keys 0..n-1 encodes as a list
		list := true
		for i, k := range x.Keys() {
			if k != strconv.Itoa(i) {
				list = false
				break
			}
		}
		if list {
			writePHPJSON(b, listOf(x))
			return
		}
		b.WriteByte('{')
		for i, k := range x.Keys() {
			if i > 0 {
				b.WriteByte(',')
			}
			writePHPJSONString(b, k)
			b.WriteByte(':')
			writePHPJSON(b, x.Get(k))
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

func writePHPJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '/':
			b.WriteString(`\/`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(b, `\u%04x`, r)
			case r < 0x80:
				b.WriteRune(r)
			case r >= 0x10000:
				r -= 0x10000
				fmt.Fprintf(b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			default:
				fmt.Fprintf(b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
}

// stripTags mirrors PHP strip_tags($s, $allowed) where allowed is like "<b><strong>".
func stripTags(s, allowed string) string {
	allow := map[string]bool{}
	for _, m := range regexp.MustCompile(`<([a-zA-Z0-9]+)>`).FindAllStringSubmatch(allowed, -1) {
		allow[strings.ToLower(m[1])] = true
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c != '<' {
			b.WriteByte(c)
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				return b.String()
			}
			i += 4 + end + 3
			continue
		}
		// a '<' followed by whitespace is literal text
		if i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t' || s[i+1] == '\n' || s[i+1] == '\r') {
			b.WriteByte(c)
			i++
			continue
		}
		// find the end of the tag, honouring quotes
		j := i + 1
		var quote byte
		depth := 1
		for j < len(s) {
			ch := s[j]
			if quote != 0 {
				if ch == quote {
					quote = 0
				}
			} else if ch == '"' || ch == '\'' {
				quote = ch
			} else if ch == '<' {
				depth++
			} else if ch == '>' {
				depth--
				if depth == 0 {
					break
				}
			}
			j++
		}
		if j >= len(s) {
			return b.String()
		}
		tag := s[i : j+1]
		name := strings.TrimLeft(tag[1:], "/")
		k := 0
		for k < len(name) && (name[k] >= 'a' && name[k] <= 'z' || name[k] >= 'A' && name[k] <= 'Z' || name[k] >= '0' && name[k] <= '9') {
			k++
		}
		if allow[strings.ToLower(name[:k])] && k > 0 {
			b.WriteString(tag)
		}
		i = j + 1
	}
	return b.String()
}

// ksesInline mirrors wp_kses($s, Html_Prop_Type::get_base_allowed_tags()): only the wrapper tags
// survive, without attributes (except href and target on <a>).
func ksesInline(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c != '<' {
			if c == '>' {
				b.WriteString("&gt;")
			} else if c == '&' {
				if m := entityRe.FindString(s[i:]); m != "" {
					b.WriteString(m)
					i += len(m)
					continue
				}
				b.WriteString("&amp;")
			} else {
				b.WriteByte(c)
			}
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		j := strings.IndexByte(s[i+1:], '>')
		if j < 0 {
			b.WriteString("&lt;")
			i++
			continue
		}
		tag := s[i+1 : i+1+j]
		i += j + 2
		closing := strings.HasPrefix(tag, "/")
		body := strings.TrimPrefix(tag, "/")
		k := 0
		for k < len(body) && (body[k] >= 'a' && body[k] <= 'z' || body[k] >= 'A' && body[k] <= 'Z' || body[k] >= '0' && body[k] <= '9') {
			k++
		}
		name := strings.ToLower(body[:k])
		if k == 0 || !allowedWrapperTags[name] {
			continue
		}
		if closing {
			b.WriteString("</" + name + ">")
			continue
		}
		selfClose := strings.HasSuffix(strings.TrimSpace(body), "/")
		b.WriteString("<" + name)
		if name == "a" {
			for _, m := range ksesAttrRe.FindAllStringSubmatch(body[k:], -1) {
				an := strings.ToLower(m[1])
				if an != "href" && an != "target" {
					continue
				}
				val := m[2]
				if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') {
					val = val[1 : len(val)-1]
				}
				if an == "href" {
					val = ksesURL(val)
				}
				b.WriteString(" " + an + "=\"" + val + "\"")
			}
		}
		if selfClose {
			b.WriteString(" /")
		}
		b.WriteString(">")
	}
	return b.String()
}

var ksesAttrRe = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)

func ksesURL(u string) string {
	if i := strings.Index(u, ":"); i > 0 && !strings.ContainsAny(u[:i], "/?#") {
		scheme := strings.ToLower(u[:i])
		for _, p := range allowedProtocols {
			if scheme == p {
				return u
			}
		}
		return u[i+1:]
	}
	return u
}
