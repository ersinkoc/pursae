package webbuilder

import (
	"strings"
	"testing"
)

const testHome = "https://example.com/"

func TestSanitizeSearchForm(t *testing.T) {
	opts := SnapshotOptions{FormActionPrefix: testFormPrefix, SearchAction: testHome}
	in := `<form data-wb-search="" data-wb-form="" method="post" action="https://evil.example/" role="search" class="sf">` +
		`<label>Search <input type="search" name="s" placeholder="Search…" aria-label="Search"></label>` +
		`<input type="hidden" name="name" value="x"><input name="post_type" value="page"><input type="text" name="email">` +
		`<select name="choice"><option>a</option></select><textarea name="message"></textarea>` +
		`<button type="submit" aria-label="Search"><svg viewBox="0 0 24 24"><path d="M0 0"></path></svg></button></form>`
	out, changed := sanitizeOK(t, in, opts)
	if !changed {
		t.Error("changed not reported")
	}
	for _, want := range []string{
		`method="get" action="https://example.com/"`, `role="search"`, `<input placeholder="Search…" aria-label="Search" type="search" name="s"/>`,
		`<button type="submit" aria-label="Search"><svg`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	for _, bad := range []string{"evil", "post_type", `name="name"`, `name="email"`, "<select", "<textarea", "data-wb-form", `method="post"`} {
		if strings.Contains(out, bad) {
			t.Errorf("%q survived in %s", bad, out)
		}
	}
	assertSafe(t, out, opts)
	// Idempotent and reported unchanged on a second pass.
	again, changed, err := SanitizeSnapshot(out, opts)
	if err != nil || again != out || changed {
		t.Fatalf("second pass changed=%v err=%v\n%s\n%s", changed, err, out, again)
	}

	// A text input becomes the search input; only the first one stays.
	out, _ = sanitizeOK(t, `<form data-wb-search><input name="q"><input type="search" name="s"><input type="submit" name="go" value="Go"></form>`, opts)
	if strings.Count(out, "<input") != 2 || !strings.Contains(out, `<input type="search" name="s"/>`) || !strings.Contains(out, `<input type="submit" value="Go"/>`) {
		t.Errorf("inputs: %s", out)
	}
	// No SearchAction: the action is "/".
	out, _ = sanitizeOK(t, `<form data-wb-search><input type="search" name="s"></form>`, SnapshotOptions{})
	if !strings.Contains(out, `method="get" action="/"`) {
		t.Errorf("default action: %s", out)
	}
	// An unsafe SearchAction falls back to "/".
	out, _ = sanitizeOK(t, `<form data-wb-search><input type="search" name="s"></form>`, SnapshotOptions{SearchAction: "javascript:alert(1)"})
	if !strings.Contains(out, `action="/"`) {
		t.Errorf("unsafe action: %s", out)
	}
}

// Without data-wb-search nothing changes: method post, builder action only, no search inputs.
func TestSanitizeOrdinaryFormUnchanged(t *testing.T) {
	opts := SnapshotOptions{FormActionPrefix: testFormPrefix, SearchAction: testHome}
	out, _ := sanitizeOK(t, `<form method="get" action="`+testHome+`"><input type="search" name="s"></form>`, opts)
	if !strings.Contains(out, `method="post"`) || strings.Contains(out, "action=") || strings.Contains(out, `type="search"`) || strings.Contains(out, `name="s"`) {
		t.Errorf("ordinary form: %s", out)
	}
	// name=s / type=search outside any form are dropped too.
	out, _ = sanitizeOK(t, `<input type="search" name="s">`, opts)
	if strings.Contains(out, "search") || strings.Contains(out, `name="s"`) {
		t.Errorf("loose input: %s", out)
	}
	assertSafe(t, out, opts)
}
