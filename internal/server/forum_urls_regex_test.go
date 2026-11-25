package server

import (
	"regexp"
	"strings"
	"testing"
)

// The pre-change regexes, kept to prove the rune-count + "+" rewrite is equivalent.
var (
	oldForumSlugRe     = regexp.MustCompile(`^[\p{L}\p{Mn}\p{Mc}\p{N}_%-]{1,200}$`)
	oldForumNicenameRe = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{Lm}\p{Mn}\p{Mc}\p{N}_%.@-]{1,100}$`)
)

func TestForumValidatorsMatchOldRegex(t *testing.T) {
	cases := []string{"", "a", "a-b_c%20", "Abc", "ünï", "日本語", "a.b", "a@b", "a b", "a/b", "\xff", "a\xffb", "\xc3", "é", "٣٤", "ǅ", "ß"}
	for _, n := range []int{99, 100, 101, 199, 200, 201, 400} {
		cases = append(cases, strings.Repeat("a", n), strings.Repeat("ü", n), strings.Repeat("日", n), strings.Repeat("a", n-1)+"\xff", strings.Repeat("a", n-1)+".")
	}
	for _, c := range cases {
		oldSlug := c == strings.ToLower(c) && oldForumSlugRe.MatchString(c)
		if got := forumValidSlug(c); got != oldSlug {
			t.Errorf("slug %q: new=%v old=%v", c, got, oldSlug)
		}
		if got, want := forumValidNicename(c), oldForumNicenameRe.MatchString(c); got != want {
			t.Errorf("nicename %q: new=%v old=%v", c, got, want)
		}
	}
}
