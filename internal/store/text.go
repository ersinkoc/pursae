package store

import (
	"strings"
	"unicode/utf8"
)

// ValidText is s with what a PostgreSQL text value cannot hold replaced by U+FFFD: invalid UTF-8
// (a search for ?s=%FF, a Latin-1 comment form) and NUL bytes. Sent as they are, both fail the whole
// statement ("invalid byte sequence for encoding UTF8"), so a visitor's search or comment answered
// 500. U+FFFD is what encoding/json already turns invalid UTF-8 in a JSON body into; WordPress drops
// such text instead (wp_check_invalid_utf8), with the same effect for a search: nothing matches it.
// Replacing, not stripping, never joins the text around a bad byte into something new.
func ValidText(s string) string {
	if utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "\uFFFD")
}

// validComment applies ValidText to the text fields of a comment a visitor wrote or sent (the
// user agent is a request header).
func validComment(c *Comment) {
	for _, f := range []*string{&c.AuthorName, &c.AuthorEmail, &c.AuthorURL, &c.AuthorIP, &c.UserAgent, &c.Content, &c.Type, &c.Status} {
		*f = ValidText(*f)
	}
}
