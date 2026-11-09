package server

import "testing"

func TestGolylangRelativePreservesFragments(t *testing.T) {
	root := "https://example.com"
	o := pllOptions{ForceLang: 1, HideDefault: true, Rewrite: true}
	langs := []*pllLanguage{{Slug: "en", IsDefault: true}, {Slug: "de"}}
	for _, tc := range []struct{ suffix, want string }{
		{"/de/article/?lang=de#details", "/article/#details"},
		{"/de#details", "/#details"},
		{"/de/article/?lang=de&page=2#x?y", "/article/?page=2#x?y"},
		{"/de/article/#", "/article/#"},
		{"/de/article/#a%23b", "/article/#a%23b"},
	} {
		if got, ok := pllRelativeLangs(root, root+tc.suffix, o, langs); !ok || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q, true", tc.suffix, got, ok, tc.want)
		}
	}
}
