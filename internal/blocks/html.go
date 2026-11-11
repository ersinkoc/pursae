package blocks

import (
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

var (
	classAttr = regexp.MustCompile(`(?i)\sclass\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>\x60]+))`)
)

// AddClass adds classes to the first element of html, the way WP_HTML_Tag_Processor::add_class() is used by render callbacks.
// When tags is non-empty only the first element with one of those tag names is changed.
//
// Class names are attribute values: tokens holding quotes, angle brackets, "&", "=", "`" or control
// characters are dropped so a block attribute can never break out of the class attribute.
func AddClass(html, classes string, tags ...string) string {
	classes = strings.Join(SafeClassTokens(classes), " ")
	if classes == "" {
		return html
	}
	z := xhtml.NewTokenizer(strings.NewReader(html))
	offset := 0
	for {
		typ := z.Next()
		if typ == xhtml.ErrorToken {
			return html
		}
		tag := string(z.Raw())
		start := offset
		offset += len(tag)
		if typ != xhtml.StartTagToken && typ != xhtml.SelfClosingTagToken {
			continue
		}
		nameBytes, _ := z.TagName()
		name := strings.ToLower(string(nameBytes))
		if len(tags) > 0 {
			match := false
			for _, tag := range tags {
				if strings.EqualFold(tag, name) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		var updated string
		if m := findClassAttr(tag); m != nil {
			start, end := m[2], m[3]
			unquoted := false
			if start < 0 {
				start, end = m[4], m[5]
			}
			if start < 0 {
				start, end = m[6], m[7]
				unquoted = true
			}
			existing := strings.Fields(tag[start:end])
			for _, c := range strings.Fields(classes) { // already filtered by SafeClassTokens
				if !contains(existing, c) {
					existing = append(existing, c)
				}
			}
			value := strings.Join(existing, " ")
			if unquoted {
				value = `"` + value + `"`
			}
			updated = tag[:start] + value + tag[end:]
		} else {
			nameEnd := 1
			for nameEnd < len(tag) && !strings.ContainsRune(" \t\r\n\f/>", rune(tag[nameEnd])) {
				nameEnd++
			}
			updated = tag[:nameEnd] + ` class="` + classes + `"` + tag[nameEnd:]
		}
		return html[:start] + updated + html[offset:]
	}
}

// findClassAttr skips matches that occur inside another attribute's quoted value.
func findClassAttr(tag string) []int {
	var quote byte
	pos := 0
	for _, m := range classAttr.FindAllStringSubmatchIndex(tag, -1) {
		for pos < m[0] {
			c := tag[pos]
			if quote != 0 && c == quote {
				quote = 0
			} else if quote == 0 && (c == '\'' || c == '"') {
				quote = c
			}
			pos++
		}
		if quote == 0 {
			return m
		}
	}
	return nil
}

// SafeClassTokens splits a class list into the tokens that are safe inside a double- or
// single-quoted class attribute without further escaping.
func SafeClassTokens(classes string) []string {
	var out []string
	for _, c := range strings.Fields(classes) {
		if strings.ContainsAny(c, "\"'<>&=`\\") || strings.IndexFunc(c, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
