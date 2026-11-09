package server

import (
	"strings"
	"testing"

	"wpgo/internal/links"
)

func TestSearchFormAction(t *testing.T) {
	for _, tc := range []struct {
		name, front, action, hidden string
	}{
		{"no plugin", "", "https://example.com/", ""},
		{"directory", "https://example.com/fr/", "https://example.com/fr/", ""},
		{"query mode", "https://example.com/?lang=fr", "https://example.com/", `<input type="hidden" name="lang" value="fr" />`},
		{"escaped", "https://example.com/?lang=%22x%3E", "https://example.com/", `<input type="hidden" name="lang" value="&#34;x&gt;" />`},
	} {
		lb := &links.Builder{Home: "https://example.com", Structure: "/%postname%/"}
		if tc.front != "" {
			front := tc.front
			lb.LinkFilter = func(kind string, _ any, link string) string {
				if kind == "home" {
					return front
				}
				return link
			}
		}
		action, hidden := searchFormAction(lb)
		if action != tc.action || string(hidden) != tc.hidden {
			t.Errorf("%s: searchFormAction = %q %q, want %q %q", tc.name, action, hidden, tc.action, tc.hidden)
		}
		if strings.Contains(action, "?") {
			t.Errorf("%s: the action keeps a query string: %s", tc.name, action)
		}
	}
}
