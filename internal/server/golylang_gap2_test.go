package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// fakeTr prefixes every text with "FR:" (inside the first tag of an HTML fragment).
func fakeTr(texts []string, asHTML bool) ([]string, error) {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = "FR:" + t
	}
	return out, nil
}

const pllWBTestDoc = `{"sections":[{"id":"H1","kind":"hero","name":"","hidden":false,"content":{"title":"Fast and simple","body":"Build <b>pages</b> quickly","cta":"Start now","ctaHref":"/start"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":0,"hideOn":[]}}],"site":{}}`

func TestPllWBPlanTranslatesSnapshotAndDoc(t *testing.T) {
	snap := `<div class="wb-page" data-wb-root data-wb-post="7"><section id="hero" class="hero"><h1 class="t">Fast and simple</h1>` +
		`<p>Build <b class="x">pages</b> quickly</p><a href="/start" class="btn">Start now</a><img src="/a.png" alt="Hero picture">` +
		`<script>var s = "Do not touch";</script><span class="notranslate">Brand</span></section></div>`
	from := &store.WebBuilderDoc{PostID: 7, Doc: json.RawMessage(pllWBTestDoc), Page: json.RawMessage(`{"title":"Home"}`), HTML: snap,
		Renderer: webbuilder.RendererVersion, SnapshotUnfiltered: true}
	d, content, err := (&Server{}).pllWBPlan(from, 7, 9, "FR:Home", fakeTr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<h1 class="t">FR:Fast and simple</h1>`, `FR:Build <b class="x">pages</b> quickly`, `FR:Start now`, `alt="FR:Hero picture"`,
		`var s = "Do not touch"`, `>Brand<`, `data-wb-post="9"`, `href="/start"`} {
		if !strings.Contains(d.HTML, want) {
			t.Errorf("snapshot misses %q:\n%s", want, d.HTML)
		}
	}
	if strings.Contains(d.HTML, "FR:Brand") || strings.Contains(d.HTML, "FR:var") {
		t.Errorf("a skipped part was translated:\n%s", d.HTML)
	}
	doc := string(d.Doc)
	for _, want := range []string{`FR:Fast and simple`, `FR:Start now`, `FR:Build`} {
		if !strings.Contains(doc, want) {
			t.Errorf("builder doc misses %q:\n%s", want, doc)
		}
	}
	if !strings.Contains(content, "FR:Fast and simple") {
		t.Errorf("fallback content not regenerated from the translated doc:\n%s", content)
	}
	if !strings.Contains(string(d.Page), `"FR:Home"`) || d.PostID != 9 {
		t.Errorf("page settings/post id: %s %d", d.Page, d.PostID)
	}
}

func TestPllHTMLUnitSetDropsForeignMarkup(t *testing.T) {
	d, err := pllHTMLParse(`<p>Read <a href="/x" class="l">more</a> here</p>`)
	if err != nil || len(d.Units) != 1 || !d.Units[0].HTML {
		t.Fatalf("units: %+v %v", d.Units, err)
	}
	d.Set(0, `Lire <a href="javascript:alert(1)" onclick="x()">plus</a> <img src=x onerror=alert(1)><script>alert(1)</script>ici`)
	got := d.Render()
	if !strings.HasPrefix(got, `<p>Lire <a `) || !strings.Contains(got, `href="/x"`) || !strings.Contains(got, `class="l"`) ||
		!strings.HasSuffix(got, `>plus</a> ici</p>`) || strings.Contains(got, "alert") || strings.Contains(got, "onclick") {
		t.Fatalf("got %s", got)
	}
}

func TestPllXLIFFParseAndTr(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
 <file original="post-5" source-language="en-US" target-language="fr-FR" datatype="html"><body>
  <trans-unit id="title"><source>Hello</source><target>Bonjour</target></trans-unit>
  <group><trans-unit id="u1"><source>&lt;p&gt;World&lt;/p&gt;</source><target>&lt;p&gt;Monde&lt;/p&gt;</target></trans-unit></group>
  <trans-unit id="u2"><source>Empty</source><target></target></trans-unit>
 </body></file></xliff>`
	doc, err := parseXLIFF(strings.NewReader(src))
	if err != nil || len(doc.Files) != 1 {
		t.Fatalf("%v %+v", err, doc)
	}
	f := doc.Files[0]
	if f.Original != "post-5" || f.Target != "fr-FR" {
		t.Fatalf("file attrs: %+v", f)
	}
	tr, n := pllXLIFFTr(f.Body.all())
	if n != 2 {
		t.Fatalf("%d translations, want 2", n)
	}
	got, _ := tr([]string{" Hello ", "<p>World</p>", "Empty", "Changed"}, false)
	want := []string{" Bonjour ", "<p>Monde</p>", "Empty", "Changed"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %q want %q", i, got[i], want[i])
		}
	}
	if _, err := parseXLIFF(strings.NewReader(`<xliff version="2.0"></xliff>`)); err == nil {
		t.Error("XLIFF 2.0 accepted")
	}
}

// pllFakeDeepL answers every DeepL request with "FR:" + text and counts the calls.
func pllFakeDeepL(t *testing.T, srv *Server) func() {
	t.Helper()
	deepl := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text []string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var tr []map[string]string
		for _, s := range in.Text {
			tr = append(tr, map[string]string{"text": "FR:" + s})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"translations": tr})
	}))
	oldBase, oldClient, oldInline := pllMTDeepLFreeBase, pllMTClient, pllJobInline
	pllMTDeepLFreeBase, pllMTClient, pllJobInline = deepl.URL, deepl.Client(), true
	o := srv.pllOpts()
	o.MTProvider, o.MTKey, o.MT = "deepl", "abc-123:fx", true
	if err := srv.pllSaveOpts(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	return func() {
		deepl.Close()
		pllMTDeepLFreeBase, pllMTClient, pllJobInline = oldBase, oldClient, oldInline
	}
}

func TestPllBulkTranslateMTAgainAndViews(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	defer pllFakeDeepL(t, srv)()
	if !srv.pllMTEnabled() {
		t.Skip("machine translation could not be enabled directly")
	}
	ctx := context.Background()
	id1, _ := pllCreatePost(t, c, base, "?lang=en", "Hello world one")
	id2, _ := pllCreatePost(t, c, base, "?lang=en", "Second story")

	// Both are untranslated into French.
	_, body, _ := get(t, c, base+"/wp-admin/edit")
	if !strings.Contains(body, "pll_filter=untranslated&amp;pll_target=fr") || !strings.Contains(body, `value="pll_translate"`) || !strings.Contains(body, `id="pll-bulk-lang"`) {
		t.Fatal("the posts list has no Untranslated view or no Translate bulk action")
	}
	_, view, _ := get(t, c, base+"/wp-admin/edit?pll_filter=untranslated&pll_target=fr")
	if !strings.Contains(view, "Hello world one") || !strings.Contains(view, "Second story") {
		t.Fatal("the Untranslated (French) view misses a post")
	}

	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)[1]
	q := url.Values{"post_type": {"post"}, "action": {pllBulkTranslate}, "pll_to": {"fr"}, "bulk_action": {"Apply"}, "_wpnonce": {nonce},
		"post[]": {itoa64(id1), itoa64(id2)}}
	code, _, loc := get(t, c, base+"/wp-admin/edit?"+q.Encode())
	if code != http.StatusFound || !strings.Contains(loc, "pll_started=1") {
		t.Fatalf("bulk translate: %d %s", code, loc)
	}
	if j := srv.pllJobState(); j == nil || j.Done != 2 || j.Fails != 0 {
		t.Fatalf("job: %+v", j)
	}
	fr1 := srv.pllTranslations(ctx, pllPost, id1)["fr"]
	if fr1 == 0 || srv.pllTranslations(ctx, pllPost, id2)["fr"] == 0 {
		t.Fatal("no French translation was created")
	}
	p, err := srv.store.GetPost(ctx, fr1)
	if err != nil || p.Title != "FR:Hello world one" || !strings.Contains(p.Content, "FR:") || p.Status != "draft" {
		t.Fatalf("translation: %+v %v", p, err)
	}
	// Running it again creates nothing new.
	get(t, c, base+"/wp-admin/edit?"+q.Encode())
	if got := srv.pllTranslations(ctx, pllPost, id1)["fr"]; got != fr1 {
		t.Fatalf("a second run replaced the translation: %d != %d", got, fr1)
	}

	// The source changes: the translation is outdated.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET title = 'Hello world changed', modified_at = now() + interval '1 minute' WHERE id = $1`, id1); err != nil {
		t.Fatal(err)
	}
	_, view, _ = get(t, c, base+"/wp-admin/edit?pll_filter=outdated&pll_target=fr")
	if !strings.Contains(view, "Hello world changed") || strings.Contains(view, "Second story") {
		t.Fatal("the Outdated (French) view is wrong")
	}
	if _, list, _ := get(t, c, base+"/wp-admin/edit"); !strings.Contains(list, "pll_icon_outdated") {
		t.Error("no outdated icon in the language column")
	}

	// "Machine translate again" on the edit screen of the translation.
	_, edit, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(fr1)+"&action=edit")
	edit = strings.NewReplacer(`\"`, `"`, `\u0026`, `&`, `\u003c`, `<`, `\u003e`, `>`).Replace(edit) // the block editor panel is a JSON string
	m := regexp.MustCompile(`href="(/wp-admin/admin\?page=mlang_translate&amp;pll_action=mt_again[^"]+)"`).FindStringSubmatch(edit)
	if m == nil {
		t.Fatal("no Machine translate again link on the translation's edit screen")
	}
	code, _, loc = get(t, c, base+strings.ReplaceAll(m[1], "&amp;", "&"))
	if code != http.StatusFound || !strings.Contains(loc, "pll_mt=done") {
		t.Fatalf("translate again: %d %s", code, loc)
	}
	if p, _ := srv.store.GetPost(ctx, fr1); p.Title != "FR:Hello world changed" {
		t.Fatalf("title after translate again: %q", p.Title)
	}
	var revs int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM post_revisions WHERE post_id = $1 AND title = 'FR:Hello world one'`, fr1).Scan(&revs)
	if revs == 0 {
		t.Error("the previous translation was not kept as a revision")
	}
	// A bad nonce is refused.
	if code, _, _ := get(t, c, base+"/wp-admin/admin?page=mlang_translate&pll_action=mt_again&post="+itoa64(fr1)+"&_wpnonce=bad"); code == http.StatusFound {
		t.Error("translate again accepted a bad nonce")
	}
}

func TestPllXLIFFRoundTrip(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	ctx := context.Background()
	id, _ := pllCreatePost(t, c, base, "?lang=en", "Garden tips")

	_, body, _ := get(t, c, base+"/wp-admin/edit")
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)[1]
	q := url.Values{"post_type": {"post"}, "action": {pllBulkXLIFF}, "pll_to": {"de"}, "bulk_action": {"Apply"}, "_wpnonce": {nonce}, "post[]": {itoa64(id)}}
	resp, err := c.Get(base + "/wp-admin/edit?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), "translation-de.xliff") {
		t.Fatalf("export: %d %s", resp.StatusCode, raw)
	}
	doc, err := parseXLIFF(bytes.NewReader(raw))
	if err != nil || len(doc.Files) != 1 || doc.Files[0].Original != "post-"+itoa64(id) || doc.Files[0].Target != "de-DE" {
		t.Fatalf("exported file: %v %+v\n%s", err, doc, raw)
	}
	// A translator fills the targets.
	filled := regexp.MustCompile(`(?s)<source>(.*?)</source><target state="needs-translation"></target>`).ReplaceAllString(string(raw), `<source>$1</source><target state="translated">DE:$1</target>`)

	_, strs, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if !strings.Contains(strs, `id="pll-xliff-import"`) {
		t.Fatal("no XLIFF import form on Strings translations")
	}
	ieNonce := regexp.MustCompile(`(?s)id="pll-xliff-import".*?name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(strs)[1]
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_wpnonce", ieNonce)
	_ = mw.WriteField("pll_action", "xliff_import")
	fw, _ := mw.CreateFormFile("xliff", "translation-de.xliff")
	_, _ = fw.Write([]byte(filled))
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-admin/admin?page=mlang_translate", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || !strings.Contains(loc, "pll_msg=xliff_imported&n=1") {
		t.Fatalf("import: %d %s", resp.StatusCode, loc)
	}
	de := srv.pllTranslations(ctx, pllPost, id)["de"]
	p, err := srv.store.GetPost(ctx, de)
	if de == 0 || err != nil || p.Title != "DE:Garden tips" || !strings.Contains(p.Content, "DE:") || !strings.Contains(p.Content, "<!-- wp:paragraph -->") {
		t.Fatalf("imported translation: %d %+v %v", de, p, err)
	}
}
