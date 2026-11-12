package wp

import (
	"strings"
	"testing"
	"time"
)

// Unclosed shortcode openers ([contact-form-7] without [/contact-form-7]) must be expanded in
// time roughly linear in the content size: stored post content is expanded on every view, and a
// regex with an optional lazy content group rescanned the rest of the input for every opener
// (68 KB took seconds, 136 KB tens of seconds).
func TestShortcodeUnclosedOpenersLinear(t *testing.T) {
	in := strings.Repeat("[contact-form-7] ", 4000)
	fn := func(string, map[string]string, string) string { return "x" }
	st := time.Now()
	out := DoShortcode(in, []string{"contact-form-7", "contact-form"}, fn)
	if d := time.Since(st); d > time.Second {
		t.Fatalf("DoShortcode on %d bytes of unclosed openers took %v (superlinear scan to the end per opener)", len(in), d)
	}
	if want := strings.Repeat("x ", 4000); out != want {
		t.Fatalf("unclosed openers expanded wrongly: %q...", out[:min(len(out), 40)])
	}
	// The same inside HTML (the attribute and text passes) and with a closer of another tag.
	html := "<p>" + strings.Repeat(`<a title="[contact-form-7]">[contact-form-7]</a> `, 2000) + "[contact-form]x[/contact-form]</p>"
	st = time.Now()
	DoShortcode(html, []string{"contact-form-7", "contact-form"}, fn)
	if d := time.Since(st); d > time.Second {
		t.Fatalf("DoShortcode on %d bytes of HTML with unclosed openers took %v", len(html), d)
	}
}

// The linear matcher keeps WordPress' shortcode semantics.
func TestShortcodeMatcherSemantics(t *testing.T) {
	tags := []string{"box", "b"}
	cases := []struct{ in, want string }{
		{"[box]a[box]b[/box]", "<box |a[box]b>"},                           // the first [/box] closes, no nesting
		{"[box]a[/box][box]b[/box]", "<box |a><box |b>"},                   // two enclosing shortcodes
		{"[box] [box]x[/box]", "<box | [box]x>"},                           // the first opener takes the content
		{"[b] [box] [b]y[/b]", "<b | [box] [b]y>"},                         // other tags inside content are kept
		{"[box] [b] [box]", "<box |> <b |> <box |>"},                       // no closers at all
		{"[[box]]", "[box]"},                                               // escaped
		{"[[box]x[/box]]", "[box]x[/box]"},                                 // escaped enclosing
		{"[box]x[/box]]", "<box |x>]"},                                     // stray trailing bracket kept
		{"[box /]x[/box]", "<box |>x[/box]"},                               // self-closing takes no content
		{"[box a=1]x[/b][/box]", "<box a=1|x[/b]>"},                        // only its own closer ends it
		{"[boxes]x[/box]", "[boxes]x[/box]"},                               // tag boundary
		{"<p>[box]x[/box]</p>", "<box |x>"},                                // shortcode_unautop
		{"<p>[box]x[/box] [b]</p>", "<p><box |x> <b |></p>"},               // not a lone shortcode
		{"[b]1[/b] [box] [b]2[/b] [box]", "<b |1> <box |> <b |2> <box |>"}, // closer cache per tag
	}
	for _, tc := range cases {
		if got := DoShortcode(tc.in, tags, echoShortcode); got != tc.want {
			t.Errorf("DoShortcode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
