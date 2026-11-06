package webbuilder

import (
	"strings"
	"testing"
)

func TestSanitizeCSSDrops(t *testing.T) {
	cases := []struct{ in, want string }{
		{`a{color:red;width:expression(alert(1))}`, `a{color:red;}`},
		{`a{width:EXPRESSION (alert(1))}`, `a{}`},
		{`a{width:exp/**/ression(alert(1))}`, `a{}`},
		{`a{width:\65 xpression(alert(1))}`, `a{}`},
		{`a{behavior:url(x.htc)}`, `a{}`},
		{`a{-moz-binding:url(http://x/x.xml#x)}`, `a{}`},
		{`a{background:url(javascript:alert(1))}`, `a{}`},
		{`a{background:url("java\73 cript:alert(1)")}`, `a{}`},
		{`a{background:url( 'vbscript:x' )}`, `a{}`},
		{`a{background:url(data:text/html,x)}`, `a{}`},
		{`a{background:url(//evil.example/x.png)}`, `a{}`},
		{`a{background:image-set("javascript:x" 1x)}`, `a{}`},
		{`a{-o-link:javascript:x}`, `a{}`},
		{`@import url(//evil.example/x.css);a{color:red}`, `a{color:red;}`},
		{`@import "x.css";`, ``},
		{`@charset "utf-8";a{color:red}`, `a{color:red;}`},
		{`@namespace svg url(http://www.w3.org/2000/svg);`, ``},
		{`@document url(http://x){a{color:red}}`, ``},
		{`a{color:red}}b{color:blue}`, `a{color:red;}b{color:blue;}`},
		{`@media (min-width:1px){@import "x";a{background:url(javascript:x)}}`, `@media (min-width:1px){a{}}`},
		{`@font-face{font-family:X;src:url(https://evil.example/x.woff2)}`, `@font-face{font-family:X;}`},
		{`@font-face{src:url(/wp-content/uploads/web-builder/fonts/../../x.woff2)}`, `@font-face{}`},
	}
	for _, c := range cases {
		got, changed, err := SanitizeCSS(c.in, CSSOptions{})
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want || !changed {
			t.Errorf("SanitizeCSS(%q)=%q changed=%v, want %q", c.in, got, changed, c.want)
		}
	}
}

func TestSanitizeCSSKeeps(t *testing.T) {
	keep := []string{
		`.a{color:red;}`,
		`@layer theme,base,components,utilities;`,
		`@layer utilities{.p-4{padding:calc(var(--spacing) * 4);}}`,
		`.hover\:bg-red:hover{@media (hover:hover){background-color:var(--color-red-500);}}`,
		`.x{&:hover{color:red;}}`,
		`@media (min-width:48rem){.md\:flex{display:flex;}}`,
		`@container canvas (width >= 64rem){.\@4xl\:grid-cols-2{grid-template-columns:repeat(2,minmax(0,1fr));}}`,
		`@supports (color:color-mix(in lab,red,red)){.a{color:color-mix(in oklab,var(--a) 50%,transparent);}}`,
		`@property --tw-x{syntax:"*";inherits:false;initial-value:0;}`,
		`@keyframes spin{to{transform:rotate(360deg);}}`,
		`@font-face{font-family:"Inter";font-weight:400;src:url(/wp-content/uploads/web-builder/fonts/inter-400.woff2) format("woff2");}`,
		`.bg{background-image:url("https://example.com/a.png"),url(/b.png),url(data:image/png;base64,AAAA);}`,
		`.m{mask:url(#m);content:"a;b{c}";}`,
		`.i{background:url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'></svg>");}`,
		`#s1 .x{--brand:#2271b1;}`,
	}
	for _, in := range keep {
		got, changed, err := SanitizeCSS(in, CSSOptions{})
		if err != nil || changed || got != in {
			t.Errorf("SanitizeCSS(%q)=%q changed=%v err=%v", in, got, changed, err)
		}
	}
	got, _, _ := SanitizeCSS(`@font-face{src:url(https://example.com/wp-content/uploads/web-builder/fonts/a.woff2)}`, CSSOptions{SiteOrigin: "https://example.com"})
	if !strings.Contains(got, "src:") {
		t.Errorf("site-origin font dropped: %q", got)
	}
	got, _, _ = SanitizeCSS("/* c */a{color:red /* x */}", CSSOptions{})
	if got != "a{color:red;}" {
		t.Errorf("comments: %q", got)
	}
}

func TestSanitizeCSSUnclosed(t *testing.T) {
	if got, _, _ := SanitizeCSS(`a{color:red`, CSSOptions{}); got != `a{color:red;}` {
		t.Errorf("unclosed block: %q", got)
	}
}

func TestSanitizeCSSErrors(t *testing.T) {
	for _, in := range []string{`a{content:"</style><script>alert(1)</script>"}`, `</STYLE>`, `a{}</style >`} {
		if _, _, err := SanitizeCSS(in, CSSOptions{}); err != ErrCSSStyleTag {
			t.Errorf("%q: err=%v", in, err)
		}
	}
	if _, _, err := SanitizeCSS(strings.Repeat("a", MaxCSSBytes+1), CSSOptions{}); err != ErrCSSTooLarge {
		t.Errorf("too large: %v", err)
	}
}

func TestSanitizeStyleAttr(t *testing.T) {
	cases := map[string]string{
		`color:red;background:url(javascript:alert(1))`:           `color:red`,
		`width:expression(alert(1));height:1px`:                   `height:1px`,
		`--brand:#2271b1;font-family:'Space Grotesk', sans-serif`: `--brand:#2271b1;font-family:'Space Grotesk', sans-serif`,
		`a{color:red}`:                   ``,
		`color:red;}x{color:blue`:        `color:red`,
		`background:u\72l(javascript:x)`: ``,
		`<script>:x`:                     ``,
	}
	for in, want := range cases {
		if got, _ := SanitizeStyleAttr(in); got != want {
			t.Errorf("SanitizeStyleAttr(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCSSValueOK(t *testing.T) {
	for v, want := range map[string]bool{
		"#fff": true, "rgba(0,0,0,.5)": true, "1px solid var(--line)": true,
		"0 10px 30px -10px rgba(0,0,0,.3)": true, "url(/a.png)": true,
		"linear-gradient(135deg,#000,#fff)": true,
		"red;background:url(javascript:x)":  false, "expression(alert(1))": false,
		"url(javascript:x)": false, "red}</style><script>": false, "x{": false,
	} {
		if got := CSSValueOK(v); got != want {
			t.Errorf("CSSValueOK(%q)=%v want %v", v, got, want)
		}
	}
}
