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
