package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestPllMTBlocksSanitized: a machine translation answer is untrusted. Markup KSES removes never
// reaches the stored content, and a part KSES would change (written with unfiltered_html) is not
// sent at all.
func TestPllMTBlocksSanitized(t *testing.T) {
	in := "<!-- wp:paragraph -->\n<p>Hello world</p>\n<!-- /wp:paragraph -->\n\n" +
		"<!-- wp:html -->\n<script>track()</script><p>Admin widget</p>\n<!-- /wp:html -->"
	var sent []string
	out, err := pllMTBlocks(in, func(texts []string) ([]string, error) {
		sent = texts
		res := make([]string, len(texts))
		for i := range texts {
			res[i] = `<p onclick="steal()">Bonjour</p><img src=x onerror="alert(1)"><script>alert(2)</script><a href="javascript:alert(3)">x</a>`
		}
		return res, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || strings.Contains(sent[0], "script") {
		t.Fatalf("sent %q: the part with a script must not be sent", sent)
	}
	for _, bad := range []string{"onclick", "onerror", "alert(2)", "javascript:"} {
		if strings.Contains(strings.SplitN(out, "<!-- wp:html -->", 2)[0], bad) {
			t.Errorf("translated part keeps %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "Bonjour") || !strings.Contains(out, "<!-- wp:paragraph -->") || !strings.Contains(out, "<!-- /wp:paragraph -->") {
		t.Errorf("translation or delimiters lost:\n%s", out)
	}
	if !strings.Contains(out, "<script>track()</script><p>Admin widget</p>") {
		t.Errorf("the unfiltered part was changed:\n%s", out)
	}
}

func TestPllMTKeyTarget(t *testing.T) {
	same := [][2][2]string{
		{{"deepl", ""}, {"deepl", "https://evil.example/v1"}}, // DeepL ignores the endpoint
		{{"openai", ""}, {"openai", "https://api.openai.com/v1"}},
		{{"openai", "https://API.example.com/v1"}, {"openai", "https://api.example.com/v2"}},
	}
	for _, c := range same {
		if pllMTKeyTarget(c[0][0], c[0][1]) != pllMTKeyTarget(c[1][0], c[1][1]) {
			t.Errorf("%v and %v should keep the key", c[0], c[1])
		}
	}
	diff := [][2][2]string{
		{{"deepl", ""}, {"openai", ""}},
		{{"google", ""}, {"deepl", ""}},
		{{"openai", ""}, {"openai", "https://attacker.example/v1"}},
	}
	for _, c := range diff {
		if pllMTKeyTarget(c[0][0], c[0][1]) == pllMTKeyTarget(c[1][0], c[1][1]) {
			t.Errorf("%v and %v must ask for the key again", c[0], c[1])
		}
	}
}

func TestPllMediaTextEqual(t *testing.T) {
	a, b := "x", "x"
	if !pllMediaTextEqual(pllMediaText{"alt": &a}, pllMediaText{"alt": &b, "title": nil}) {
		t.Error("equal texts")
	}
	c := "y"
	if pllMediaTextEqual(pllMediaText{"alt": &a}, pllMediaText{"alt": &c}) || pllMediaTextEqual(pllMediaText{}, pllMediaText{"alt": &a}) {
		t.Error("different texts")
	}
}

// TestGolylangVisualHostModes: with languages on subdomains or domains, the visual editor (same
// origin shell, iframe and messages) is not offered and its shell explains why.
func TestGolylangVisualHostModes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	pllCreatePost(t, c, base, "", "Host mode post")

	if _, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings"); !strings.Contains(body, "page=mlang_visual") {
		t.Fatal("directory mode: the Translations screen has no Translate visually link")
	}
	for _, mode := range []int{2, 3} {
		o := srv.pllOpts()
		o.ForceLang = mode
		if mode == 3 {
			o.Domains = map[string]string{"en": base, "fr": "http://fr.example.test"}
		}
		if err := srv.pllSaveOpts(ctx, o); err != nil {
			t.Fatal(err)
		}
		if _, body, _ := get(t, c, base+pllVisualURL("/", "fr")); !strings.Contains(body, "not available while the language is set from subdomains") || strings.Contains(body, "pll-v-frame") {
			t.Errorf("mode %d: the shell does not explain the unsupported mode", mode)
		}
		if _, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings"); strings.Contains(body, "page=mlang_visual") {
			t.Errorf("mode %d: the Translations screen links to the visual editor", mode)
		}
		if _, body, _ := get(t, c, base+"/wp-admin/edit"); strings.Contains(body, "Translate visually") {
			t.Errorf("mode %d: posts list offers Translate visually", mode)
		}
		if code, body, _ := get(t, c, base+"/?lang=en"); code == http.StatusOK && strings.Contains(body, "wp-admin-bar-translate-page") {
			t.Errorf("mode %d: the toolbar offers Translate Page", mode)
		}
	}
}
