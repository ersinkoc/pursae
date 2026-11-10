package wp

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func echoShortcode(tag string, attrs map[string]string, content string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+attrs[k])
	}
	return fmt.Sprintf("<%s %s|%s>", tag, strings.Join(parts, ","), content)
}

func TestDoShortcode(t *testing.T) {
	tags := []string{"box", "b"}
	cases := []struct{ in, want string }{
		{"[box]", "<box |>"},
		{"[box /]", "<box |>"},
		{`[box color="red" size='lg' n=3 flag]`, "<box 0=flag,color=red,n=3,size=lg|>"},
		{"[box]inner[/box]", "<box |inner>"},
		{"a [box]x[/box] b [b]y[/b] c", "a <box |x> b <b |y> c"},
		{"[[box]]", "[box]"},
		{"[unknown attr=1]", "[unknown attr=1]"},
		{"<p>[box]</p>", "<box |>"},
		{"<p>[box]inner[/box]</p>", "<box |inner>"},
		{"<p>text [box] more</p>", "<p>text <box |> more</p>"},
		{"[box]<p>html</p>[/box]", "<box |<p>html</p>>"},
		{"[BOX]", "[BOX]"},
		{"no shortcodes here", "no shortcodes here"},
		{`[box url="https://example.com/a/b"]`, "<box url=https://example.com/a/b|>"},
	}
	for _, tc := range cases {
		if got := DoShortcode(tc.in, tags, echoShortcode); got != tc.want {
			t.Errorf("DoShortcode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDoShortcodeUnregisteredTags(t *testing.T) {
	if got := DoShortcode("[box]", nil, echoShortcode); got != "[box]" {
		t.Fatalf("got %q", got)
	}
	if got := DoShortcode("[box]", []string{"BAD TAG"}, echoShortcode); got != "[box]" {
		t.Fatalf("invalid tag must be ignored, got %q", got)
	}
}

func TestParseShortcodeAttrs(t *testing.T) {
	got := ParseShortcodeAttrs(` Title="A b" id=7 'quoted' bare `)
	want := map[string]string{"title": "A b", "id": "7", "0": "quoted", "1": "bare"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if !HasShortcode("x [box id=1] y", "box") || HasShortcode("x [boxes] y", "box") {
		t.Error("HasShortcode")
	}
	if ValidShortcodeTag("Box") || !ValidShortcodeTag("my_box-2") {
		t.Error("ValidShortcodeTag")
	}
}

// Shortcodes inside markup follow do_shortcodes_in_html_tags(): attribute values get the output
// escaped for the attribute, everything else inside tags, comments and CDATA is left alone.
func TestDoShortcodeInHTMLTags(t *testing.T) {
	box := func(tag string, attrs map[string]string, content string) string {
		switch tag {
		case "box":
			return `<div class="box box-` + attrs["style"] + `">z</div>`
		case "url":
			return attrs["to"]
		case "css":
			return "color: red"
		}
		return ""
	}
	tags := []string{"box", "url", "css"}
	cases := []struct{ name, in, want string }{
		{"attribute output is escaped",
			`<a href="/x" title="[box style=x/onmouseover=alert(1)//]">t</a>`,
			`<a href="/x" title="&lt;div class=&#34;box box-x/onmouseover=alert(1)//&#34;&gt;z&lt;/div&gt;">t</a>`},
		{"plain attribute value", `<a title="[url to=hello]">t</a>`, `<a title="hello">t</a>`},
		{"single-quoted and unquoted values", `<a title='[url to=a]' data-x=[css]>t</a>`, `<a title="a" data-x="color: red">t</a>`},
		{"bad protocol keeps the original", `<a href="[url to=javascript:alert(1)]">t</a>`, `<a href="[url to=javascript:alert(1)]">t</a>`},
		{"good protocol", `<a href="[url to=https://e.com/a]">t</a>`, `<a href="https://e.com/a">t</a>`},
		{"event handler is never expanded", `<a onclick="[url to=x]">t</a>`, `<a onclick="[url to=x]">t</a>`},
		{"style goes through SafeCSS", `<p style="[css]">t</p>`, `<p style="color: red">t</p>`},
		{"comment untouched", `<!-- [box style=a] --><p>x [url to=b]</p>`, `<!-- [box style=a] --><p>x b</p>`},
		{"enclosing shortcode around markup", `[url to=x]<a title="[y]">t</a>`, `x<a title="[y]">t</a>`},
		{"quoted > does not end the tag", `<a title="a>b [url to=c]">t</a>`, `<a title="a&gt;b c">t</a>`},
		{"less-than in text", `a < b [url to=c]`, `a < b c`},
		{"sentinel characters survive", "<p>\uE000[url to=c]</p>", "<p>&#xE000;c</p>"},
	}
	for _, tc := range cases {
		if got := DoShortcode(tc.in, tags, box); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}
