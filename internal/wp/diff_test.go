package wp

import (
	"strings"
	"testing"
)

func TestInlineDiffUnicodeWhitespace(t *testing.T) {
	for _, space := range []string{" ", "\t", "\u00a0", "\u2003"} {
		left, right := inlineDiff("alpha"+space+"old", "alpha"+space+"new")
		if !strings.Contains(left, "<del>old</del>") || !strings.Contains(right, "<ins>new</ins>") ||
			!strings.Contains(left, "alpha"+space) || !strings.Contains(right, "alpha"+space) {
			t.Errorf("space %q: left=%q right=%q", space, left, right)
		}
	}
}

func TestTextDiffNormalizesLineEndings(t *testing.T) {
	for _, tc := range [][2]string{{"a\rb", "a\nb"}, {"a\r\nb\rc", "a\nb\nc"}, {"\r", "\n"}, {"a\r\nb", "a\nb"}} {
		if got := TextDiff(tc[0], tc[1]); got != "" {
			t.Errorf("equivalent lines %q / %q produced %q", tc[0], tc[1], got)
		}
	}
	if got := TextDiff("a\rold", "a\nnew"); !strings.Contains(got, "old") || !strings.Contains(got, "new") {
		t.Errorf("changed lines missing from %q", got)
	}
}
