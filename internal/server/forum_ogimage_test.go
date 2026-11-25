package server

import "testing"

func TestForumSanitizeOGImage(t *testing.T) {
	for in, want := range map[string]string{
		"/wp-content/uploads/forum-og.png": "/wp-content/uploads/forum-og.png",
		"  https://example.com/og.png  ":   "https://example.com/og.png",
		"javascript:alert(1)":              "",
		"https://x.com/a\"onload=\"y.png":  "",
		"":                                 "",
	} {
		st := forumDefaults()
		st.OGImage = in
		if got := forumSanitize(st).OGImage; got != want {
			t.Errorf("OGImage %q: got %q, want %q", in, got, want)
		}
	}
}
