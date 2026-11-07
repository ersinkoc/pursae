package wp

import (
	"strings"
	"testing"
)

func TestCleanURL(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a?b=1&c=2": "https://example.com/a?b=1&c=2",
		"example.com":                   "http://example.com",
		"/relative/path":                "/relative/path",
		"//cdn.example.com/x.js":        "//cdn.example.com/x.js",
		"#frag":                         "#frag",
		"mailto:a@b.co":                 "mailto:a@b.co",
		"tel:+123":                      "tel:+123",
		"javascript:alert(1)":           "",
		"JaVaScRiPt:alert(1)":           "",
		" javascript:alert(1)":          "",
		"java\tscript:alert(1)":         "",
		"java&#09;script:alert(1)":      "",
		"javascript&#58;alert(1)":       "http://javascript&#58;alert(1)",
		"data:text/html,<script>":       "",
		"vbscript:msgbox":               "",
		"https://ex.com/\"onmouseover=": "https://ex.com/onmouseover=",
	}
	for in, want := range cases {
		if got := CleanURL(in); got != want {
			t.Errorf("CleanURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CleanURL("ftp://x", "http", "https"); got != "" {
		t.Errorf("custom protocols: got %q", got)
	}
	if got := EscURL("https://e.com/?a=1&b='x'"); got != "https://e.com/?a=1&amp;b=&#39;x&#39;" {
		t.Errorf("EscURL = %q", got)
	}
}

func TestSafeCSS(t *testing.T) {
	cases := map[string]string{
		"color: red; margin-top: var(--wp--preset--spacing--50)": "color: red;margin-top: var(--wp--preset--spacing--50)",
		"--wp--custom: 3px":                                           "--wp--custom: 3px",
		"width: calc(100% - 2px)":                                     "width: calc(100% - 2px)",
		"background-image: url(https://e.com/a.png)":                  "background-image: url(https://e.com/a.png)",
		"background-image: url(javascript:alert(1))":                  "",
		"background: linear-gradient(135deg,rgb(1,2,3) 0%,#fff 100%)": "background: linear-gradient(135deg,rgb(1,2,3) 0%,#fff 100%)",
		"behavior: url(x.htc)":                                        "",
		"color: expression(alert(1))":                                 "",
		"position: fixed; top: 0; left: 0":                            "position: fixed;top: 0;left: 0",
		"color: red /* x */":                                          "",
		"font-family: a\\62 c":                                        "",
		"-moz-binding: url(x)":                                        "",
	}
	for in, want := range cases {
		if got := SafeCSS(in); got != want {
			t.Errorf("SafeCSS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKsesPostFiltersStyle(t *testing.T) {
	got := KsesPost(`<p style="color:red;background-image:url(javascript:alert(1))">x</p><div style="behavior:url(a.htc)">y</div>`)
	if strings.Contains(got, "javascript") || strings.Contains(got, "behavior") {
		t.Fatalf("unsafe style kept: %s", got)
	}
	if !strings.Contains(got, `<p style="color:red">x</p>`) || !strings.Contains(got, "<div>y</div>") {
		t.Fatalf("safe style lost: %s", got)
	}
	// Block delimiters still survive.
	if got := KsesPost(`<!-- wp:paragraph --><p style="margin-top:var(--wp--preset--spacing--20)">a</p><!-- /wp:paragraph -->`); !strings.Contains(got, "<!-- wp:paragraph -->") || !strings.Contains(got, "var(--wp--preset--spacing--20)") {
		t.Fatalf("block content changed: %s", got)
	}
}

func TestKsesData(t *testing.T) {
	in := `<a href="https://e.com" title="t" class="x" style="position:fixed">l</a><a href="javascript:alert(1)">j</a><strong>b</strong><img src="https://e.com/i.png"><div id="d">d</div><script>alert(1)</script><blockquote cite="https://e.com">q</blockquote>`
	got := KsesData(in)
	for _, bad := range []string{"class=", "style=", "<img", "<div", "<script", "javascript:"} {
		if strings.Contains(got, bad) {
			t.Errorf("KsesData kept %q: %s", bad, got)
		}
	}
	for _, good := range []string{`<a href="https://e.com" title="t"`, "<strong>b</strong>", `<blockquote cite="https://e.com">q</blockquote>`} {
		if !strings.Contains(got, good) {
			t.Errorf("KsesData dropped %q: %s", good, got)
		}
	}
}
