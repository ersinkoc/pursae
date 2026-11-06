package i18n

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeMO builds a little-endian .mo file from msgid -> msgstr ("\x00" separates plural forms;
// a key "singular\x00plural" is a plural message).
func writeMO(entries map[string]string) []byte {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	n := len(keys)
	var origs, trans bytes.Buffer
	type span struct{ l, o int }
	var os_, ts []span
	base := 28 + n*16
	for _, k := range keys {
		os_ = append(os_, span{len(k), origs.Len()})
		origs.WriteString(k)
		origs.WriteByte(0)
	}
	for _, k := range keys {
		ts = append(ts, span{len(entries[k]), trans.Len()})
		trans.WriteString(entries[k])
		trans.WriteByte(0)
	}
	var b bytes.Buffer
	w := func(v int) { _ = binary.Write(&b, binary.LittleEndian, uint32(v)) }
	w(0x950412de)
	w(0)
	w(n)
	w(28)
	w(28 + n*8)
	w(0)
	w(0)
	for _, s := range os_ {
		w(s.l)
		w(base + s.o)
	}
	for _, s := range ts {
		w(s.l)
		w(base + origs.Len() + s.o)
	}
	b.Write(origs.Bytes())
	b.Write(trans.Bytes())
	return b.Bytes()
}

func testManager(t *testing.T) *Manager {
	dir := t.TempDir()
	core := writeMO(map[string]string{
		"":                     "Content-Type: text/plain; charset=UTF-8\nPlural-Forms: nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);\n",
		"Posts":                "Wpisy",
		"Settings":             "Ustawienia",
		"Save Changes":         "Zapisz zmiany",
		"Search Posts":         "Szukaj wpisów",
		"Howdy, %s":            "Witaj, %s",
		"%s item\x00%s items":  "%s element\x00%s elementy\x00%s elementów",
		"post\x04Add New Post": "Dodaj wpis",
		"%1$s of %2$s":         "%2$s: %1$s",
		"Tagline":              "Opis &#8220;strony&#8221;",
		"Read the <a href=\"%s\">documentation</a>.": "Przeczytaj <a href=\"%s\">dokumentację</a>.",
		"Page Title": "Tytuł strony",
	})
	if err := os.WriteFile(filepath.Join(dir, "admin-pl_PL.mo"), core, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "plugins"), 0o755)
	plug := writeMO(map[string]string{"Forms": "Formularze Contact Form 7"})
	if err := os.WriteFile(filepath.Join(dir, "plugins", "contact-form-7-pl_PL.mo"), plug, 0o644); err != nil {
		t.Fatal(err)
	}
	return NewManager(dir, nil)
}

func TestCatalogLookups(t *testing.T) {
	c := testManager(t).Catalog("pl_PL")
	if c.Empty() {
		t.Fatal("empty catalog")
	}
	if got := c.T("Posts"); got != "Wpisy" {
		t.Errorf("T: %s", got)
	}
	if got := c.X("Add New Post", "post"); got != "Dodaj wpis" {
		t.Errorf("X: %s", got)
	}
	for n, want := range map[int]string{1: "%s element", 3: "%s elementy", 5: "%s elementów", 22: "%s elementy", 12: "%s elementów"} {
		if got := c.N("%s item", "%s items", n); got != want {
			t.Errorf("N(%d) = %s, want %s", n, got, want)
		}
	}
	if got := c.T("Forms"); got != "Formularze Pursae Contact Form" {
		t.Errorf("plugin pack not rebranded: %s", got)
	}
	if !testManager(t).Catalog("en_US").Empty() {
		t.Error("English must have no catalog")
	}
}

// TestLabelAfterControl: a checkbox label whose text has markup is one message, not the
// fragments on each side of the <code>; without that message the fragments still translate.
func TestLabelAfterControl(t *testing.T) {
	dir := t.TempDir()
	core := writeMO(map[string]string{"": "Plural-Forms: nplurals=2; plural=n != 1;\n",
		"Upload objects as <code>public-read</code>": "<code>public-read</code> olarak yükle",
		"Path-style requests (":                      "Yol tarzı istekler ("})
	if err := os.WriteFile(filepath.Join(dir, "admin-tr_TR.mo"), core, 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewManager(dir, nil).Catalog("tr_TR")
	in := `<label><input type="checkbox" name="public-read" value="1" checked> Upload objects as <code>public-read</code></label>` +
		`<label><input type="checkbox" name="p"> Path-style requests (<code>a/b</code>)</label>`
	got := string(c.TranslateHTML([]byte(in)))
	for _, want := range []string{`<label><input type="checkbox" name="public-read" value="1" checked> <code>public-read</code> olarak yükle</label>`,
		`<input type="checkbox" name="p"> Yol tarzı istekler (<code>a/b</code>)</label>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
}

func TestTranslateHTML(t *testing.T) {
	c := testManager(t).Catalog("pl_PL")
	in := `<html lang="en-US"><head><title>Page Title &lsaquo; Site &#8212; Pursae</title></head><body>` +
		`<h1>Posts</h1><a class="row-title" translate="no"><span>Settings</span></a><p>Settings</p>` +
		`<input type="submit" value="Save Changes"><input placeholder="Search Posts"><input type="text" value="Settings">` +
		`<span>3 items</span><span>21 items</span><b>Add New Post</b><p>Tagline</p>` +
		`<p class="description">Read the <a href="/docs">documentation</a>.</p>` +
		`<a class="ab-item">Howdy, <span class="display-name">Ann</span><img alt="" src="a.png"></a>` +
		`<script>var s = "Settings";</script><textarea>Settings</textarea><!-- Settings -->` +
		`<script type="text/template" id="tmpl-x"><# if ( data.a > 1 ) { #><p>Settings</p><# } #><b>{{ data.name }}</b></script></body></html>`
	got := string(c.TranslateHTML([]byte(in)))
	for _, want := range []string{
		`<title>Tytuł strony &lsaquo; Site`, `<h1>Wpisy</h1>`, `translate="no"><span>Settings</span></a><p>Ustawienia</p>`,
		`value="Zapisz zmiany"`, `placeholder="Szukaj wpisów"`, `<input type="text" value="Settings">`,
		`<span>3 elementy</span>`, `<span>21 element</span>`, `<b>Dodaj wpis</b>`, `<p>Opis &#8220;strony&#8221;</p>`,
		`Przeczytaj <a href="/docs">dokumentację</a>.`, `Witaj, <span class="display-name">Ann</span><img`,
		`var s = "Settings";`, `<textarea>Settings</textarea>`, `<!-- Settings -->`,
		`<script type="text/template" id="tmpl-x"><# if ( data.a > 1 ) { #><p>Ustawienia</p><# } #><b>{{ data.name }}</b></script>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
}

func TestPluralRules(t *testing.T) {
	for expr, cases := range map[string]map[int64]int{
		"n != 1": {0: 1, 1: 0, 2: 1},
		"n > 1":  {0: 0, 1: 0, 2: 1},
		"0":      {5: 0},
		"(n==1 ? 0 : n==2 ? 1 : (n>=3 && n<=10) ? 2 : 3)": {1: 0, 2: 1, 7: 2, 11: 3},
	} {
		r, err := compilePlural(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		for n, want := range cases {
			if got := r.index(n); got != want {
				t.Errorf("%s with n=%d: %d, want %d", expr, n, got, want)
			}
		}
	}
	if _, err := compilePlural("n +"); err == nil {
		t.Error("bad expression accepted")
	}
}

func TestEscapeAndLanguages(t *testing.T) {
	if got := escapeText(`a & b <c> &hellip; "q"`); got != `a &amp; b &lt;c&gt; &hellip; &quot;q&quot;` {
		t.Errorf("escapeText: %s", got)
	}
	if len(Languages()) < 100 {
		t.Errorf("only %d languages", len(Languages()))
	}
	if l, ok := LanguageByLocale("tr_TR"); !ok || l.Native != "Türkçe" {
		t.Errorf("tr_TR: %+v", l)
	}
	if !IsRTL("ar") || IsRTL("de_DE") {
		t.Error("IsRTL")
	}
	m := testManager(t)
	if got := m.Installed(); len(got) != 1 || got[0] != "pl_PL" {
		t.Errorf("Installed: %v", got)
	}
	if rebrandString("Contact Form 7 and Pursae Contact Form") != "Pursae Contact Form and Pursae Contact Form" {
		t.Error(rebrandString("Contact Form 7 and Pursae Contact Form"))
	}
}

func TestInstallAndScriptTranslations(t *testing.T) {
	zipOf := func(files map[string][]byte) []byte {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for name, data := range files {
			w, _ := zw.Create(name)
			w.Write(data)
		}
		zw.Close()
		return b.Bytes()
	}
	js := `{"locale_data":{"messages":{"":{"domain":"messages"},"Publish":["Opublikuj"]}}}`
	pjs := `{"locale_data":{"messages":{"":{"domain":"messages"},"Edit with Contact Form 7":["Edytuj w Contact Form 7"]}}}`
	sum := md5.Sum([]byte("wp-includes/js/dist/editor.js"))
	core := zipOf(map[string][]byte{
		"admin-pl_PL.mo": writeMO(map[string]string{"Posts": "Wpisy"}), "admin-pl_PL.po": []byte("po"),
		"pl_PL-" + hex.EncodeToString(sum[:]) + ".json": []byte(js), "../evil.mo": []byte("x"),
	})
	plug := zipOf(map[string][]byte{"contact-form-7-pl_PL.mo": writeMO(map[string]string{"Forms": "Formularze"}),
		"contact-form-7-pl_PL-abc.json": []byte(pjs)})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/core/1.0/":
			fmt.Fprintf(w, `{"translations":[{"language":"pl_PL","package":"%s/core.zip"}]}`, srv.URL)
		case r.URL.Path == "/plugins/1.0/" && r.URL.Query().Get("slug") == "contact-form-7":
			fmt.Fprintf(w, `{"translations":[{"language":"pl_PL","package":"%s/el.zip"}]}`, srv.URL)
		case r.URL.Path == "/plugins/1.0/":
			fmt.Fprint(w, `{"translations":[]}`)
		case r.URL.Path == "/core.zip":
			w.Write(core)
		case r.URL.Path == "/el.zip":
			w.Write(plug)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	m := NewManager(t.TempDir(), nil)
	if err := m.Install(context.Background(), &Installer{API: srv.URL}, "pl_PL", []string{"contact-form-7", "polylang"}); err != nil {
		t.Fatal(err)
	}
	if !m.IsInstalled("pl_PL") || m.Catalog("pl_PL").T("Posts") != "Wpisy" || m.Catalog("pl_PL").T("Forms") != "Formularze" {
		t.Fatal("pack not installed")
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "admin-pl_PL.po")); err == nil {
		t.Error(".po extracted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.Dir), "evil.mo")); err == nil {
		t.Error("zip path escaped the directory")
	}
	if got := string(m.ScriptTranslations("pl_PL", "/wp-includes/js/dist/editor.min.js", "")); !strings.Contains(got, `"Publish":["Opublikuj"]`) {
		t.Errorf("script translations: %s", got)
	}
	if got := string(m.PluginScriptTranslations("pl_PL", "contact-form-7")); !strings.Contains(got, "Edytuj w Pursae Contact Form") {
		t.Errorf("plugin script translations: %s", got)
	}
	if err := m.Install(context.Background(), &Installer{API: srv.URL}, "xx_XX", nil); err == nil {
		t.Error("unknown locale installed")
	}
}

func TestUpstreamNames(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "admin-de_DE.mo"), writeMO(map[string]string{
		"You have the latest version of WordPress.": "Du hast die neueste Version von WordPress.",
		"What&#8217;s new":                          "Neuigkeiten",
	}), 0o644)
	c := NewManager(dir, nil).Catalog("de_DE")
	if tr, ok := c.Lookup("You have the latest version of Pursae."); !ok || tr != "Du hast die neueste Version von Pursae." {
		t.Errorf("upstream name: %q %v", tr, ok)
	}
	if tr, ok := c.Lookup("What’s new"); !ok || tr != "Neuigkeiten" {
		t.Errorf("normalized: %q %v", tr, ok)
	}
}

func TestOptionWithoutValueKept(t *testing.T) {
	c := testManager(t).Catalog("pl_PL")
	got := string(c.TranslateHTML([]byte(`<select><option>Posts</option><option value="p">Posts</option></select>`)))
	if got != `<select><option>Posts</option><option value="p">Wpisy</option></select>` {
		t.Errorf("got %s", got)
	}
}

func partsCatalog(t *testing.T) *Catalog {
	dir := t.TempDir()
	core := writeMO(map[string]string{
		"":               "Content-Type: text/plain; charset=UTF-8\n",
		"Customize %s":   "Dostosuj %s",
		"Shop":           "Sklep",
		"Brazil":         "Brazylia",
		"Spain":          "Hiszpania",
		"Polska":         "Polska",
		"Algerian dinar": "Dinar algierski",
		"Add zone":       "Dodaj strefę",
		"October":        "październik",
		"F j, Y":         "j F Y",
		"Built-in":       "Wbudowane",
	})
	if err := os.WriteFile(filepath.Join(dir, "admin-pl_PL.mo"), core, 0o644); err != nil {
		t.Fatal(err)
	}
	return NewManager(dir, nil).Catalog("pl_PL")
}

// Text the page puts together from messages is translated part by part; prose never becomes the
// argument of a short pattern.
func TestTranslateParts(t *testing.T) {
	c := partsCatalog(t)
	for in, want := range map[string]string{
		"Brazil — Amazonas":                  "Brazylia — Amazonas",
		"Alaska, Spain":                      "Alaska, Hiszpania",
		"Polska — Mazowieckie":               "Polska — Mazowieckie", // a message translated as itself
		"Algerian dinar (د.ج) — DZD":         "Dinar algierski (د.ج) — DZD",
		"Shop (ID: 5)":                       "Sklep (ID: 5)",
		"&gt; Add zone":                      "&gt; Dodaj strefę",
		"October 5, 2026":                    "5 październik 2026",
		"Customize Hello Pursae Web Builder": "Dostosuj Hello Pursae Web Builder",
	} {
		if got, ok := c.translateText(in); !ok || got != want {
			t.Errorf("%q: got %q (%v), want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"Customize WordPress with powerful, professional and intuitive fields: field groups and blocks. A native clone of ACF.",
		"Shop — a sentence that is not a message at all.",
		"Brazil, really, and more",
	} {
		if got, ok := c.translateText(in); ok {
			t.Errorf("%q must stay English, got %q", in, got)
		}
	}
}

// TestTitleParts: every part of an admin title but the site's is translated on its own.
func TestTitleParts(t *testing.T) {
	c := testManager(t).Catalog("pl_PL")
	got := string(c.TranslateHTML([]byte(`<title>Settings ‹ Pursae Security &lsaquo; Site &#8212; Pursae</title><title>Analytics › Posts ‹ Shop &lsaquo; Site &#8212; Pursae</title>`)))
	for _, want := range []string{`<title>Ustawienia ‹ Pursae Security &lsaquo; Site &#8212; Pursae</title>`, `<title>Analytics › Wpisy ‹ Shop &lsaquo; Site &#8212; Pursae</title>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}
