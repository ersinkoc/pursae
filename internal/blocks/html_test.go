package blocks

import (
	"strings"
	"testing"
)

func TestAddClassRejectsUnsafeTokens(t *testing.T) {
	got := AddClass(`<div class="a">x</div>`, `ok is-layout-x" autofocus onfocus="alert(1) <b> it's`)
	if strings.Count(got, `"`) != 2 || strings.Contains(got, "<b>") || strings.Contains(got, "'") {
		t.Fatalf("unsafe class token reached the markup: %s", got)
	}
	if want := `<div class="a ok autofocus">x</div>`; got != want {
		t.Fatalf("AddClass = %s, want %s", got, want)
	}
	if got := AddClass(`<p>x</p>`, `y"><script>`); got != `<p>x</p>` {
		t.Fatalf("all-unsafe classes must leave the markup alone: %s", got)
	}
	if got := AddClass(`<p>x</p>`, "wp-block-x is-layout-flow"); got != `<p class="wp-block-x is-layout-flow">x</p>` {
		t.Fatalf("plain classes: %s", got)
	}
}

func TestAddClassPreservesSingleQuotedAttribute(t *testing.T) {
	got := AddClass(`<div CLASS = 'old'>x</div>`, "new")
	if got != `<div CLASS = 'old new'>x</div>` {
		t.Fatalf("AddClass = %s", got)
	}
}

func TestAddClassPreservesUnquotedAttribute(t *testing.T) {
	got := AddClass(`<div class=old id=x>x</div>`, "new")
	if got != `<div class="old new" id=x>x</div>` {
		t.Fatalf("AddClass = %s", got)
	}
}

func TestAddClassIgnoresClassTextInsideAttribute(t *testing.T) {
	got := AddClass(`<div title=" class='fake'" class='real'>x</div>`, "new")
	if got != `<div title=" class='fake'" class='real new'>x</div>` {
		t.Fatalf("AddClass = %s", got)
	}
}

func TestAddClassScansRealTagsWithQuotedDelimiters(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`<div title="a > b" class="old">x</div>`, `<div title="a > b" class="old new">x</div>`},
		{`<!-- <div> --><div>x</div>`, `<!-- <div> --><div class="new">x</div>`},
	} {
		if got := AddClass(tc.in, "new"); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestAddClassMatchesTagFiltersCaseInsensitively(t *testing.T) {
	if got := AddClass(`<h2>Hi</h2>`, "new", "H2"); got != `<h2 class="new">Hi</h2>` {
		t.Fatalf("got %q", got)
	}
}
