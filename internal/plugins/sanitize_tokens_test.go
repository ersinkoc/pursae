package plugins

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// A content filter without content:html cannot add an event handler or a tag by cutting into a
// tag whose quoted attribute holds a raw '>' or '<' (D13): the cut points follow the HTML
// tokenizer, not the last '<' and '>' bytes.
func TestSanitizeChangeQuotedAngleBrackets(t *testing.T) {
	for _, c := range []struct{ name, in, out string }{
		{"gt-in-attr-insert-handler",
			`<p><a title="1 > 0" href="/x">link</a></p>`,
			`<p><a title="1 > 0" onmouseover=alert(1) href="/x">link</a></p>`},
		{"lt-in-attr-delete-only",
			`<p>A</p><a title="<img src=x onerror=alert(1)>">link</a>`,
			`<p>A</p><img src=x onerror=alert(1)>">link</a>`},
		{"plain-script-control",
			`<p>Hello</p>`,
			`<p>Hello</p><script>alert(1)</script>`},
		{"single-quoted-attr-gt",
			`<p><span data-x='a>b' class="c">t</span></p>`,
			`<p><span data-x='a>b' onclick=alert(1) class="c">t</span></p>`},
		{"gt-in-attr-handler-at-end",
			`<p><abbr title="a>b">x</abbr></p>`,
			`<p><abbr title="a>b" onclick="alert(1)">x</abbr></p>`},
		{"two-quoted-gts",
			`<div><a title="x>y" data-a="b>c" href="/">t</a></div>`,
			`<div><a title="x>y" data-a="b>c" onfocus=alert(1) autofocus href="/">t</a></div>`},
		// The suffix must start where the original was outside a tag: the original's attribute
		// value must not turn into markup once the tag around it is gone.
		{"attr-value-becomes-markup",
			`<p><a title="<img src=x onerror=alert(1)>" href="/">t</a></p>`,
			`<p><b><img src=x onerror=alert(1)>" href="/">t</a></p>`},
		// …nor the original's script text, once its <script> start tag is changed.
		{"script-text-becomes-markup",
			`<p>a</p><script>var s="<img src=x onerror=alert(1)>";</script>`,
			`<p>a</p><b>var s="<img src=x onerror=alert(1)>";</script>`},
		// A kept prefix ending in a bare "<" would turn the sanitised text after it into a tag.
		{"prefix-ends-in-lt",
			`<p>1 < 2</p>`,
			`<p>1 <img src=x onerror=alert(1)//2</p>`},
		{"prefix-ends-in-lt-text",
			`<p>1 <<b>x</b></p>`,
			`<p>1 <img src=x onerror=alert(1)//<b>x</b></p>`},
		{"prefix-ends-in-text-lt-before-removed-tag",
			`<p>1 <2</p>`,
			`<p>1 <<script></script>img src=x onerror=alert(1)//2</p>`},
	} {
		got, ok := sanitizeChange(c.in, c.out)
		// What follows the value on the page must not complete a tag it leaves open either.
		if hasLiveHandler(got) || hasLiveHandler(got+`">`) {
			t.Errorf("%s: a script or an event handler is live (ok=%v)\n  in =%q\n  out=%q\n  got=%q", c.name, ok, c.in, c.out, got)
		}
	}
}

// hasLiveHandler reports whether s, as a browser tokenizes it, has a script start tag or a start
// tag with an on* attribute.
func hasLiveHandler(s string) bool {
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			if string(name) == "script" {
				return true
			}
			for more {
				var key []byte
				key, _, more = z.TagAttr()
				if strings.HasPrefix(string(key), "on") {
					return true
				}
			}
		}
	}
}

// The legitimate cases keep working: the core's markup before and after the change is untouched,
// and only the plugin's part is sanitised.
func TestSanitizeChangeKeepsCoreMarkup(t *testing.T) {
	core := `<p title="a > b">Intro</p><form action="/c"><input name="n"></form>`
	for _, c := range []struct{ out, want string }{
		// A changed word in a paragraph after a quoted '>'.
		{`<p title="a > b">Intro!</p><form action="/c"><input name="n"></form>`, `<p title="a > b">Intro!</p><form action="/c"><input name="n"></form>`},
		// A class added to the first tag: the tag is sanitised whole, the form after it is kept.
		{`<p title="a > b" class="x">Intro</p><form action="/c"><input name="n"></form>`, ""},
	} {
		got, ok := sanitizeChange(core, c.out)
		if !ok {
			t.Errorf("dropped: %q", c.out)
			continue
		}
		if !strings.HasSuffix(got, `<form action="/c"><input name="n"></form>`) {
			t.Errorf("the core's form was touched: %q", got)
		}
		if c.want != "" && got != c.want {
			t.Errorf("sanitizeChange(%q) = %q, want %q", c.out, got, c.want)
		}
	}
	// Wrapping the original keeps it whole even when the wrapper has a quoted '>' (the marker
	// check follows the tokenizer as well).
	got, ok := sanitizeChange(core, `<div class="w" title="x > y">`+core+`</div>`)
	if !ok || !strings.Contains(got, core) || !strings.HasPrefix(got, `<div`) {
		t.Errorf("wrapper: %q %v", got, ok)
	}
}

// The original value placed inside an attribute of a tag whose earlier attribute holds a quoted
// '>' is not text: the change is dropped, whether or not the sanitiser escapes that '>' (the
// marker check follows the tokenizer, not the last '<' and '>').
func TestSanitizeAroundQuotedAngleBracket(t *testing.T) {
	core := `<p>"onmouseover="alert(1)</p>`
	out := `<a href="/" title="1 > 0" class="` + core + `">x</a>`
	got, ok := sanitizeChange(core, out)
	if ok || got != core {
		t.Errorf("original inside an attribute kept: %q %v", got, ok)
	}
	if hasLiveHandler(got) {
		t.Errorf("live handler: %q", got)
	}
}

// A change inside raw text or a comment is still dropped, however the raw text is quoted.
func TestSanitizeChangeRawTextAndComments(t *testing.T) {
	for _, c := range []struct{ in, out string }{
		{`<style>a{content:">"}</style><p>x</p>`, `<style>a{content:">"}</style><p>x</p>`},
		{`<textarea title="a"><b>x</b></textarea>`, `<textarea title="a"><b>x</b><img src=x onerror=alert(1)></textarea>`},
		{`<title>a > b</title>`, `<title>a > b<script>alert(1)</script></title>`},
		{`<!-- a > b --><p>x</p>`, `<!-- a > b <img src=x onerror=alert(1)> --><p>x</p>`},
		{`<script>var a = "</div>";</script>`, `<script>var a = "</div>";alert(1)</script>`},
	} {
		got, ok := sanitizeChange(c.in, c.out)
		if c.in == c.out {
			if !ok || got != c.out {
				t.Errorf("unchanged %q: %q %v", c.in, got, ok)
			}
			continue
		}
		if ok || got != c.in {
			t.Errorf("change inside raw text or a comment kept:\n  in =%q\n  out=%q\n  got=%q %v", c.in, c.out, got, ok)
		}
	}
}

// An original that ends inside a construct (an unclosed tag, a "<") is not wrapped with the marker:
// what a plugin appends after it is sanitised together with the cut-off part.
func TestSanitizeChangeOriginalEndsOpen(t *testing.T) {
	for _, in := range []string{`a <`, `<p>x</p><a title="`, `<script>x`} {
		out := in + `img src=x onerror=alert(1)//`
		got, ok := sanitizeChange(in, out)
		if !ok && got == in {
			continue // dropped (the change is inside the script)
		}
		if hasLiveHandler(got + `">`) {
			t.Errorf("sanitizeChange(%q, %q) = %q", in, out, got)
		}
	}
}

// The tokens cover the input without gaps and mark raw text.
func TestHTMLTokens(t *testing.T) {
	s := `<p title="a>b">x</p><script>if(a<b)c()</script><b`
	toks := htmlTokens(s)
	end := 0
	for _, tok := range toks.toks {
		if tok.start != end {
			t.Fatalf("gap before %+v", tok)
		}
		end = tok.end
	}
	if end != len(s) {
		t.Fatalf("tokens end at %d of %d", end, len(s))
	}
	if !toks.rawAt(strings.Index(s, "if(")) || toks.rawAt(strings.Index(s, "x</p>")) {
		t.Error("raw text not marked")
	}
	if toks.endsInData(s) {
		t.Error("a cut-off tag ends in data")
	}
	if p, ok := toks.headCut(s, strings.Index(s, "b\">")); !ok || p != 0 {
		t.Errorf("headCut inside the quoted '>' = %d %v", p, ok)
	}
	if q := toks.tailCut(s, strings.Index(s, ">b\"")); q != strings.Index(s, "x</p>") {
		t.Errorf("tailCut inside the quoted '>' = %d", q)
	}
}
