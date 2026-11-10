package wp

import (
	"strings"
	"testing"
	"time"
)

// Comments kses keeps must never let markup out: "--!>" closes a comment in browsers, so a
// "<!--more" text that holds ">" is dropped instead of kept verbatim.
func TestKsesPostKeptComments(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"more tag closed early by --!>", "<!--more-->x<!--more --!><img src=x onerror=alert(1)>-->", `<!--more-->x<img src="x">--&gt;`},
		{"more text with markup is dropped", "<!--more <img src=x onerror=alert(1)> -->", ``},
		{"more text kept", "<p>a</p><!--more Read on--><p>b</p>", "<p>a</p><!--more Read on--><p>b</p>"},
		{"more block keeps its order", "<!-- wp:more --><!--more--><!-- /wp:more -->", "<!-- wp:more --><!--more--><!-- /wp:more -->"},
		{"page break survives", "<!-- wp:nextpage --><!--nextpage--><!-- /wp:nextpage -->", "<!-- wp:nextpage --><!--nextpage--><!-- /wp:nextpage -->"},
		{"noteaser survives", "<!--more--><!--noteaser-->", "<!--more--><!--noteaser-->"},
		{"forged placeholder", "<p>\x00WPBLOCK0\x00</p><!--more-->", "<p>WPBLOCK0</p><!--more-->"},
		{"dropped placeholder does not shift the rest", "<script><!-- wp:x --></script><!-- wp:y --><!--more-->", "<!-- wp:y --><!--more-->"},
		{"other comments dropped", "<!-- note --><p>x</p>", "<p>x</p>"},
	}
	for _, tc := range cases {
		if got := KsesPost(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// Post links keep every wp_allowed_protocols() scheme and get no rel="nofollow" (wp_kses_post);
// media URLs are checked like any other URI attribute.
func TestKsesPostLinksAndMedia(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"no nofollow", `<a href="https://e.com/x">x</a>`, `<a href="https://e.com/x">x</a>`},
		{"relative link", `<a href="/about/">x</a>`, `<a href="/about/">x</a>`},
		{"rel kept as written", `<a href="https://e.com/" rel="sponsored">x</a>`, `<a href="https://e.com/" rel="sponsored">x</a>`},
		{"tel", `<a href="tel:+90123">x</a>`, `<a href="tel:+90123">x</a>`},
		{"sms", `<a href="sms:+90123">x</a>`, `<a href="sms:+90123">x</a>`},
		{"ftp", `<a href="ftp://e.com/f">x</a>`, `<a href="ftp://e.com/f">x</a>`},
		{"javascript", `<a href="javascript:alert(1)">x</a>`, `x`},
		{"javascript with tab entity", `<a href="java&#09;script:alert(1)">x</a>`, `x`},
		{"data", `<a href="data:text/html,x">x</a>`, `x`},
		{"vbscript", `<a href="VBScript:x">x</a>`, `x`},
		{"ordered list start", `<ol start="3" reversed><li value="4">a</li></ol>`, `<ol start="3" reversed=""><li value="4">a</li></ol>`},
		{"bad start", `<ol start="x;color:red"><li>a</li></ol>`, `<ol><li>a</li></ol>`},
		{"poster javascript", `<video poster="javascript:alert(1)" src="/a.mp4"></video>`, `<video src="/a.mp4"></video>`},
		{"poster relative", `<video poster="/p.jpg" controls=""></video>`, `<video poster="/p.jpg" controls=""></video>`},
		{"source javascript", `<video><source src="JavaScript:alert(1)"></video>`, `<video></video>`},
		{"source with control char", "<audio><source src=\"\x01javascript:alert(1)\"></audio>", `<audio></audio>`},
		{"source https", `<audio><source src="https://e.com/a.mp3"></audio>`, `<audio><source src="https://e.com/a.mp3"></audio>`},
	}
	for _, tc := range cases {
		if got := KsesPost(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestAutopKeepsMarkupIntact(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"newline inside a tag", "<a\nhref=\"x\">link</a>", "<p><a\nhref=\"x\">link</a></p>\n"},
		{"newline inside an attribute value", "Text <img src=\"a.png\" alt=\"a\nb\"> more", "<p>Text <img src=\"a.png\" alt=\"a\nb\"> more</p>\n"},
		{"blank line inside a tag", "<span\n\nclass=\"x\">t</span>", "<p><span\n\nclass=\"x\">t</span></p>\n"},
		{"quoted > in a tag", "<a title=\"a > b\nc\" href=\"/\">x</a>", "<p><a title=\"a > b\nc\" href=\"/\">x</a></p>\n"},
		{"newline inside a comment", "a <!-- c\nd --> b", "<p>a <!-- c\nd --> b</p>\n"},
		{"script keeps its newlines", "<script>\nvar a = 1;\nvar b = 2;\n</script>", "<p><script>\nvar a = 1;\nvar b = 2;\n</script></p>\n"},
		{"svg keeps its newlines", "<svg>\n<path d=\"M0 0\"/>\n</svg>", "<p><svg>\n<path d=\"M0 0\"/>\n</svg></p>\n"},
		{"options", "<select>\n<option>a</option>\n<option>b</option>\n</select>", "<p><select><option>a</option><option>b</option></select></p>\n"},
		{"text line breaks still work", "line1\nline2\n\npara2", "<p>line1<br />\nline2</p>\n<p>para2</p>\n"},
		{"less-than in text", "a < b\nc\n\nd", "<p>a < b<br />\nc</p>\n<p>d</p>\n"},
		{"pre untouched", "<pre>a\n\nb</pre>\n\nc", "<pre>a\n\nb</pre>\n<p>c</p>\n"},
	}
	for _, tc := range cases {
		if got := Autop(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// Enclosing shortcodes close on their own tag (WordPress' \[\/\2\]), so different shortcodes nest.
func TestDoShortcodeNesting(t *testing.T) {
	tags := []string{"row", "col", "box"}
	cases := []struct{ name, in, want string }{
		{"nested different tags", "[row][col]x[/col][/row]", "<row |[col]x[/col]>"},
		{"siblings inside", "[row a=1][col]x[/col][col]y[/col][/row] z", "<row a=1|[col]x[/col][col]y[/col]> z"},
		{"self-closing inner", "[row][box][/row]", "<row |[box]>"},
		{"same tag does not nest", "[box]a[box]b[/box]c[/box]", "<box |a[box]b>c[/box]"},
		{"stray closer of another tag", "[box]a[/col]b[/box]", "<box |a[/col]b>"},
		{"no closer: self-closing", "[row] text", "<row |> text"},
		{"multiline content", "[row]a\nb[/row]", "<row |a\nb>"},
		{"escaped", "[[row]x[/row]]", "[row]x[/row]"},
		{"prefix tag", "[rows]", "[rows]"},
		{"explicit self-closing", "[box/]", "<box |>"},
	}
	for _, tc := range cases {
		if got := DoShortcode(tc.in, tags, echoShortcode); got != tc.want {
			t.Errorf("%s: DoShortcode(%q)\n got %q\nwant %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestParseShortcodeAttrsDashedNames(t *testing.T) {
	got := ParseShortcodeAttrs(`data-id="5" Link-To='file' max-w=300 bare`)
	want := map[string]string{"data-id": "5", "link-to": "file", "max-w": "300", "0": "bare"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestHumanTimeDiffRounds(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30 seconds"},
		{90 * time.Second, "2 mins"},
		{400 * day, "1 year"},
		{600 * day, "2 years"},
		{1000 * day, "3 years"},
	}
	for _, tc := range cases {
		if got := HumanTimeDiff(base, base.Add(tc.d)); got != tc.want {
			t.Errorf("%v: got %q want %q", tc.d, got, tc.want)
		}
	}
	if !strings.Contains(HumanTimeDiff(base.Add(600*day), base), "2 years") {
		t.Error("diff must not depend on order")
	}
}

func TestRelUGC(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<a href="https://e.com">x</a>`, `<a href="https://e.com" rel="nofollow ugc">x</a>`},
		{`<a href="/x" rel="external">x</a> <a href="/y" rel="nofollow">y</a>`, `<a href="/x" rel="external nofollow ugc">x</a> <a href="/y" rel="nofollow ugc">y</a>`},
		{`<a href="/x" title="a rel=&#34;b&#34;">x</a>`, `<a href="/x" title="a rel=&#34;b&#34;" rel="nofollow ugc">x</a>`},
		{`<abbr title="x">y</abbr><p>a href</p>`, `<abbr title="x">y</abbr><p>a href</p>`},
	}
	for _, tc := range cases {
		if got := RelUGC(tc.in); got != tc.want {
			t.Errorf("RelUGC(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
	// Comment markup as stored (KsesData) and rendered (KsesPost) keeps the link and gets the rel.
	if got := RelUGC(KsesPost(KsesData(`<a href="https://e.com" rel="me">x</a>`))); got != `<a href="https://e.com" rel="nofollow ugc">x</a>` {
		t.Errorf("comment link: %q", got)
	}
}

func TestShortcodeUnautopNested(t *testing.T) {
	tags := []string{"row", "col", "box"}
	cases := []struct{ in, want string }{
		{"<p>[row][col]x[/col][/row]</p>", "<row |[col]x[/col]>"},
		{"<p>[row]\n[col]x[/col]\n[/row]</p>", "<row |\n[col]x[/col]\n>"},
		{"<p>[box] x</p>\n<p>[box]</p>", "<p><box |> x</p>\n<box |>"},
		{"<p>[box] and [box]</p>", "<p><box |> and <box |></p>"},
		{"<p>[row]<em>a</em>[/row]</p>", "<row |<em>a</em>>"},
	}
	for _, tc := range cases {
		if got := DoShortcode(tc.in, tags, echoShortcode); got != tc.want {
			t.Errorf("DoShortcode(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}
