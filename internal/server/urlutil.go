package server

import "strings"

// absURL makes a site-relative URL absolute.
func absURL(home, u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return strings.TrimRight(home, "/") + u
}
