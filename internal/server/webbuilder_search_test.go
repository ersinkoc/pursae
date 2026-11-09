package server

import "testing"

func TestWBRewriteSearchForms(t *testing.T) {
	calls := 0
	target := func() (string, string) {
		calls++
		return "https://example.com/", `<input type="hidden" name="lang" value="fr" />`
	}
	in := `<form data-wb-form="" method="post" action="https://example.com/wp-json/web-builder/v2/forms/4/s1"></form>` +
		`<form data-wb-search="" class="sf x action=" role="search" method="get" action="https://example.com/"><input type="search" name="s"/></form>` +
		`<form class="b" data-wb-search="" method="get"><input type="search" name="s"/></form>`
	want := `<form data-wb-form="" method="post" action="https://example.com/wp-json/web-builder/v2/forms/4/s1"></form>` +
		`<form data-wb-search="" class="sf x action=" role="search" method="get" action="https://example.com/"><input type="hidden" name="lang" value="fr" /><input type="search" name="s"/></form>` +
		`<form class="b" data-wb-search="" method="get" action="https://example.com/"><input type="hidden" name="lang" value="fr" /><input type="search" name="s"/></form>`
	if got := wbRewriteSearchForms(in, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if calls != 1 {
		t.Errorf("target asked %d times", calls)
	}
	if got := wbRewriteSearchForms(`<form data-wb-search="" method="get" action="/"></form>`, func() (string, string) { return `https://e.com/"x`, "" }); got != `<form data-wb-search="" method="get" action="https://e.com/&#34;x"></form>` {
		t.Errorf("escaping: %s", got)
	}
}
