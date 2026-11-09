package wp

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDate(t *testing.T) {
	ts := time.Date(2026, 10, 3, 14, 5, 9, 0, time.UTC)
	cases := map[string]string{
		"F j, Y":      "October 3, 2026",
		"g:i a":       "2:05 pm",
		"Y-m-d H:i:s": "2026-10-03 14:05:09",
		"jS \\o\\f F": "3rd of October",
		"D, d M y":    "Sat, 03 Oct 26",
	}
	for f, want := range cases {
		if got := FormatDate(ts, f); got != want {
			t.Errorf("%q: got %q want %q", f, got, want)
		}
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"Hello world!":             "hello-world",
		"Çılgın Şeyler Öğrendik":   "cilgin-seyler-ogrendik",
		"  <b>Tags</b> &amp; Co. ": "tags-co",
		"a -- b":                   "a-b",
		"Новости":                  "новости",
		"Привет мир":               "привет-мир",
		"Ελληνικά Νέα":             "ελληνικά-νέα",
		"日本語のタイトル":                 "日本語のタイトル",
		"مرحبا بالعالم":            "مرحبا-بالعالم",
		"हिन्दी समाचार":            "हिन्दी-समाचार",
		"%d0%bf%d1%80%d0%b8%d0%b2%d0%b5%d1%82-%d0%bc%d0%b8%d1%80": "привет-мир",
		"100% pure":    "100-pure",
		"Café ☕ Crème": "cafe-creme",
		"!!!":          "",
	}
	for in, want := range cases {
		if got := SanitizeTitle(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestAutop(t *testing.T) {
	got := Autop("First line\nsecond line\n\nNew para\n\n<ul><li>x</li></ul>")
	if !strings.Contains(got, "<p>First line<br />\nsecond line</p>") || !strings.Contains(got, "<p>New para</p>") {
		t.Errorf("autop paragraphs: %q", got)
	}
	if strings.Contains(got, "<p><ul>") {
		t.Errorf("block element wrapped: %q", got)
	}
}

func TestSplitMore(t *testing.T) {
	teaser, rest, text, ok := SplitMore("<p>a</p><!--more Read on--><p>b</p>")
	if !ok || teaser != "<p>a</p>" || rest != "<p>b</p>" || text != "Read on" {
		t.Errorf("got %q %q %q %v", teaser, rest, text, ok)
	}
}
