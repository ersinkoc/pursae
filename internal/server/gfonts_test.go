package server

import (
	"context"
	"testing"
)

// The default font fetch refuses everything but https:// on the two Google Fonts hosts, before
// any connection is made.
func TestGoogleFontFetchHosts(t *testing.T) {
	for _, raw := range []string{
		"http://fonts.googleapis.com/css?family=Roboto",
		"https://example.com/x.woff2",
		"https://fonts.gstatic.com:8443/x.woff2",
		"https://user@fonts.gstatic.com/x.woff2",
		"https://fonts.gstatic.com.evil.test/x.woff2",
	} {
		if _, err := gfontFetch(context.Background(), raw); err == nil {
			t.Errorf("%s was fetched", raw)
		}
	}
}
