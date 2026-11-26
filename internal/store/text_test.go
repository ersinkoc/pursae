package store

import (
	"testing"
	"unicode/utf8"
)

func TestValidText(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"plain":            "plain",
		"ışık İstanbul":    "ışık İstanbul",
		"\xff":             "\uFFFD",
		"caf\xe9":          "caf\uFFFD",
		"a\x00b":           "a\uFFFDb",
		"\xe2\x82":         "\uFFFD", // a truncated sequence is one replacement
		"<scr\xffipt>":     "<scr\uFFFDipt>",
		"ok \U0001F600 ok": "ok \U0001F600 ok",
	} {
		got := ValidText(in)
		if got != want || !utf8.ValidString(got) {
			t.Errorf("ValidText(%q) = %q, want %q", in, got, want)
		}
	}
	c := &Comment{AuthorName: "J\xf6rg", Content: "x\x00y", UserAgent: "UA \xff", AuthorEmail: "a@b.test"}
	validComment(c)
	if c.AuthorName != "J\uFFFDrg" || c.Content != "x\uFFFDy" || c.UserAgent != "UA \uFFFD" || c.AuthorEmail != "a@b.test" {
		t.Errorf("validComment: %+v", c)
	}
}
