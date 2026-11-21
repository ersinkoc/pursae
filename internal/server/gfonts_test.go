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

// Only the Web Builder's library families are localized, by their exact name, with the weights the
// library lists for them.
func TestGoogleFontFamilyAndURL(t *testing.T) {
	for _, name := range []string{"Inter", " Geist "} {
		if _, ok := gfontFamily(name); !ok {
			t.Errorf("%q is not served", name)
		}
	}
	for _, name := range []string{"inter", "Comic Sans MS", "", "Alef Hebrew"} {
		if _, ok := gfontFamily(name); ok {
			t.Errorf("%q is served", name)
		}
	}
	f, _ := gfontFamily("Open Sans")
	want := "https://fonts.googleapis.com/css?family=Open+Sans:300,400,500,600,700,800,300italic,400italic,500italic,600italic,700italic,800italic&display=swap"
	if got := gfontURL(f); got != want {
		t.Errorf("gfontURL = %s, want %s", got, want)
	}
}
