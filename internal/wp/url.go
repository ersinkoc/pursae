package wp

import (
	"html"
	"strings"
)

// AllowedProtocols is wp_allowed_protocols(): the URL schemes esc_url() and kses keep.
var AllowedProtocols = []string{"http", "https", "ftp", "ftps", "mailto", "news", "irc", "irc6", "ircs", "gopher", "nntp",
	"feed", "telnet", "mms", "rtsp", "sms", "svn", "tel", "fax", "xmpp", "webcal", "urn"}

// urlByteOK is the character class esc_url() keeps: [a-z0-9-~+_.?#=!&;,/:%@$|*'()\[\]\x80-\xff].
func urlByteOK(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c >= 0x80:
		return true
	}
	return strings.IndexByte("-~+_.?#=!&;,/:%@$|*'()[]", c) >= 0
}

// CleanURL is esc_url( $url, $protocols, 'db' ) (esc_url_raw()): characters URLs cannot hold are
// dropped, a URL without a scheme gets http:// unless it is relative (/, # or ?), and a URL whose
// scheme is not in protocols (AllowedProtocols when none are given) becomes "". The scheme is read
// after decoding character references and dropping whitespace and control characters, the way
// wp_kses_bad_protocol() does, so "java&#09;script:" and "javascript&#58;" are refused too.
// The result is not HTML-escaped: use EscURL for attribute values.
func CleanURL(raw string, protocols ...string) string {
	raw = strings.ReplaceAll(strings.TrimLeft(raw, " \t\n\r\x00\x0B"), " ", "%20")
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if urlByteOK(raw[i]) {
			b.WriteByte(raw[i])
		}
	}
	u := b.String()
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(u), "mailto:") {
		for _, bad := range []string{"%0d", "%0a", "%0D", "%0A"} { // _deep_replace()
			for strings.Contains(u, bad) {
				u = strings.ReplaceAll(u, bad, "")
			}
		}
	}
	u = strings.ReplaceAll(u, ";//", "://")
	if !strings.Contains(u, ":") && !strings.ContainsAny(u[:1], "/#?") && !looksLikePHPFile(u) {
		u = "http://" + u
	}
	if len(protocols) == 0 {
		protocols = AllowedProtocols
	}
	scheme, ok := urlScheme(u)
	if !ok {
		return u // no scheme before the first / ? #: a relative URL
	}
	for _, p := range protocols {
		if strings.EqualFold(scheme, p) {
			return u
		}
	}
	return ""
}

// EscURL is esc_url(): CleanURL escaped for an HTML attribute.
func EscURL(raw string, protocols ...string) string {
	return html.EscapeString(CleanURL(raw, protocols...))
}

func looksLikePHPFile(u string) bool {
	i := strings.Index(strings.ToLower(u), ".php")
	if i <= 0 {
		return false
	}
	for _, c := range u[:i] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// urlScheme returns the scheme of u as a browser would read it: character references decoded,
// whitespace and control characters removed. ok is false when no ":" comes before the first
// "/", "?" or "#" (protocol-relative "//host" counts as having a scheme of "").
func urlScheme(u string) (string, bool) {
	dec := html.UnescapeString(u)
	var b strings.Builder
	for _, r := range dec {
		if r <= 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	dec = b.String()
	if strings.HasPrefix(dec, "//") {
		return "", false
	}
	for i := 0; i < len(dec); i++ {
		switch dec[i] {
		case ':':
			return dec[:i], true
		case '/', '?', '#':
			return "", false
		}
	}
	return "", false
}
