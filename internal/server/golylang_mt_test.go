package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPllMTLangCodes(t *testing.T) {
	l := func(locale, slug string) *pllLanguage { return &pllLanguage{Locale: locale, Slug: slug, Name: "X"} }
	cases := []struct {
		l              *pllLanguage
		deeplT, deeplS string
		google         string
	}{
		{l("en_US", "en"), "EN-US", "EN", "en"},
		{l("en_GB", "en"), "EN-GB", "EN", "en"},
		{l("", "en"), "EN-US", "EN", "en"},
		{l("pt_BR", "pt"), "PT-BR", "PT", "pt"},
		{l("pt_PT", "pt"), "PT-PT", "PT", "pt-PT"},
		{l("zh_CN", "zh"), "ZH", "ZH", "zh-CN"},
		{l("zh_TW", "tw"), "ZH-HANT", "ZH", "zh-TW"},
		{l("zh_HK", "hk"), "ZH-HANT", "ZH", "zh-TW"},
		{l("nb_NO", "nb"), "NB", "NB", "no"},
		{l("de_DE_formal", "de"), "DE", "DE", "de"},
		{l("fr_FR", "fr"), "FR", "FR", "fr"},
		{l("tr_TR", "tr"), "TR", "TR", "tr"},
		{l("fil", "fil"), "FIL", "FIL", "tl"},
	}
	for _, c := range cases {
		if got := pllDeepLLang(c.l, true); got != c.deeplT {
			t.Errorf("DeepL target %q: %q, want %q", c.l.Locale, got, c.deeplT)
		}
		if got := pllDeepLLang(c.l, false); got != c.deeplS {
			t.Errorf("DeepL source %q: %q, want %q", c.l.Locale, got, c.deeplS)
		}
		if got := pllGoogleLang(c.l, true); got != c.google {
			t.Errorf("Google %q: %q, want %q", c.l.Locale, got, c.google)
		}
	}
	if got := pllOpenAILang(&pllLanguage{Name: "Français", Locale: "fr_FR"}, true); got != "Français (fr-FR)" {
		t.Errorf("OpenAI language: %q", got)
	}
}

func TestPllMTEndpointOK(t *testing.T) {
	for v, want := range map[string]bool{
		"https://api.openai.com/v1": true, "https://llm.example.com": true,
		"http://api.openai.com/v1": false, "ftp://x": false, "https://user:pw@x.com": false, "https://x.com/?a=1": false,
		"https:///nohost": false, "javascript:alert(1)": false, "": false,
	} {
		if got := pllMTEndpointOK(v); got != want {
			t.Errorf("pllMTEndpointOK(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestPllMTDeepLProvider(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v2/translate" || r.Header.Get("Authorization") != "DeepL-Auth-Key k:fx" {
			http.Error(w, `{"message":"bad"}`, http.StatusForbidden)
			return
		}
		var in struct {
			Text        []string `json:"text"`
			Target      string   `json:"target_lang"`
			Source      string   `json:"source_lang"`
			TagHandling string   `json:"tag_handling"`
			Formality   string   `json:"formality"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if len(in.Text) > 50 {
			t.Errorf("DeepL batch of %d texts", len(in.Text))
		}
		if in.Formality != "prefer_more" {
			t.Errorf("formality %q", in.Formality)
		}
		out := map[string]any{}
		var tr []map[string]string
		for _, s := range in.Text {
			if strings.Contains(s, "notranslate") && in.TagHandling != "html" {
				t.Error("protected placeholders were sent without tag_handling=html")
			}
			tr = append(tr, map[string]string{"text": "[" + in.Target + "/" + in.Source + "] " + s})
		}
		out["translations"] = tr
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer ts.Close()
	p := &pllDeepL{key: "k:fx", base: ts.URL, formality: "prefer_more", client: ts.Client()}
	fr := &pllLanguage{Slug: "fr", Locale: "fr_FR"}
	en := &pllLanguage{Slug: "en", Locale: "en_US"}
	texts := make([]string, 120)
	for i := range texts {
		texts[i] = "Hello"
	}
	texts[3] = "  "
	texts[4] = "1234"
	texts[5] = " Read %s now "
	res, err := pllMTRun(context.Background(), p, texts, en, fr, false)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 { // 50 + 50 + 17 plain, 1 protected
		t.Errorf("%d requests, want 4", calls.Load())
	}
	if res[0] != "[FR/EN] Hello" || res[3] != "  " || res[4] != "1234" {
		t.Errorf("translations: %q %q %q", res[0], res[3], res[4])
	}
	if res[5] != " [FR/EN] Read %s now " {
		t.Errorf("placeholder text: %q", res[5])
	}
	// Same source and target: nothing is sent.
	calls.Store(0)
	if res, _ := pllMTRun(context.Background(), p, []string{"Bonjour"}, fr, fr, false); res[0] != "Bonjour" || calls.Load() != 0 {
		t.Errorf("same language: %q, %d calls", res[0], calls.Load())
	}
	// A wrong key: the error explains it and never carries the key.
	bad := &pllDeepL{key: "secret-key-123", base: ts.URL, client: ts.Client()}
	_, err = pllMTRun(context.Background(), bad, []string{"Hello"}, nil, fr, false)
	if err == nil || !strings.Contains(err.Error(), "check the API key") || strings.Contains(err.Error(), "secret-key-123") {
		t.Errorf("bad key error: %v", err)
	}
}

func TestPllMTGoogleProvider(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/language/translate/v2" || r.URL.RawQuery != "" || r.Header.Get("X-Goog-Api-Key") != "gk" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"API key not valid gk"}}`)
			return
		}
		var in struct {
			Q      []string `json:"q"`
			Target string   `json:"target"`
			Format string   `json:"format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var tr []map[string]string
		for _, s := range in.Q {
			tr = append(tr, map[string]string{"translatedText": in.Format + ":" + in.Target + ":" + s})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"translations": tr}})
	}))
	defer ts.Close()
	p := &pllGoogle{key: "gk", base: ts.URL, client: ts.Client()}
	res, err := pllMTRun(context.Background(), p, []string{"<p>Hi</p>"}, nil, &pllLanguage{Locale: "zh_TW"}, true)
	if err != nil || res[0] != "html:zh-TW:<p>Hi</p>" {
		t.Fatalf("google: %q %v", res, err)
	}
	_, err = pllMTRun(context.Background(), &pllGoogle{key: "wrong", base: ts.URL, client: ts.Client()}, []string{"Hi"}, nil, &pllLanguage{Locale: "de_DE"}, false)
	if err == nil || !strings.Contains(err.Error(), "API key not valid") {
		t.Errorf("google error: %v", err)
	}
}

func TestPllMTOpenAIProvider(t *testing.T) {
	var fenced atomic.Bool
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in struct {
			Model    string `json:"model"`
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Model != "m1" || len(in.Messages) != 2 || !strings.Contains(in.Messages[0].Content, "Français (fr-FR)") ||
			!strings.Contains(in.Messages[0].Content, "%1$s") {
			t.Errorf("chat request: %+v", in)
		}
		var texts []string
		_ = json.Unmarshal([]byte(in.Messages[1].Content), &texts)
		for i := range texts {
			texts[i] = "FR " + texts[i]
		}
		raw, _ := json.Marshal(map[string]any{"translations": texts})
		content := string(raw)
		if fenced.Load() {
			arr, _ := json.Marshal(texts)
			content = "```json\n" + string(arr) + "\n```"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}})
	}))
	defer ts.Close()
	p := &pllOpenAI{key: "ok", endpoint: ts.URL + "/v1", model: "m1", client: ts.Client()}
	fr := &pllLanguage{Name: "Français", Locale: "fr_FR", Slug: "fr"}
	res, err := pllMTRun(context.Background(), p, []string{"One", "Two %s"}, nil, fr, false)
	if err != nil || res[0] != "FR One" || res[1] != "FR Two %s" {
		t.Fatalf("openai: %q %v", res, err)
	}
	fenced.Store(true)
	if res, err = pllMTRun(context.Background(), p, []string{"Three"}, nil, fr, false); err != nil || res[0] != "FR Three" {
		t.Fatalf("openai fenced: %q %v", res, err)
	}
	if _, err := pllMTParseChat("sorry, I cannot"); err == nil {
		t.Error("a prose answer was accepted")
	}
}

// The OpenAI-compatible endpoint is chosen by the admin: the shared client refuses private and
// loopback addresses when it connects, whatever the host name.
func TestPllMTRefusesPrivateEndpoints(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the private endpoint was reached")
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	for _, ep := range []string{ts.URL, "https://localhost:" + u.Port()} {
		p := &pllOpenAI{key: "k", endpoint: ep, model: "m", client: pllMTClient}
		_, err := p.Translate(context.Background(), []string{"Hi"}, "", "French", false)
		if err == nil || !strings.Contains(err.Error(), "private network") {
			t.Errorf("%s: %v", ep, err)
		}
	}
}

func TestPllMTBlocksAndPlaceholders(t *testing.T) {
	in := "<!-- wp:paragraph {\"align\":\"center\"} -->\n<p class=\"has-text-align-center\">Hello world</p>\n<!-- /wp:paragraph -->\n\n" +
		"<!-- wp:image {\"id\":5} -->\n<figure class=\"wp-block-image\"><img src=\"a.jpg\" alt=\"\"/></figure>\n<!-- /wp:image -->\n\n<!-- wp:separator /-->"
	var sent []string
	out, err := pllMTBlocks(in, func(texts []string) ([]string, error) {
		sent = texts
		res := make([]string, len(texts))
		for i, s := range texts {
			res[i] = strings.ReplaceAll(s, "Hello world", "Bonjour le monde")
		}
		return res, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 { // the paragraph and the figure; whitespace between blocks is not sent
		t.Errorf("sent %d parts: %q", len(sent), sent)
	}
	for _, s := range sent {
		if strings.Contains(s, "wp:") {
			t.Errorf("a block delimiter was sent: %q", s)
		}
	}
	if want := strings.ReplaceAll(in, "Hello world", "Bonjour le monde"); out != want {
		t.Errorf("blocks:\n%s\nwant\n%s", out, want)
	}
	p := pllMTProtect(`Save & go: %1$s of {count} <b>`)
	if strings.Count(p, `class="notranslate"`) != 2 || !strings.Contains(p, "&amp;") || !strings.Contains(p, "&lt;b&gt;") {
		t.Errorf("protect: %s", p)
	}
	if got := pllMTUnprotect(p); got != `Save & go: %1$s of {count} <b>` {
		t.Errorf("unprotect: %s", got)
	}
}

// TestPllMachineTranslation runs the module end to end against a fake DeepL: settings (sealed
// key), the REST proxy, the strings screen and "Machine translate the copy".
func TestPllMachineTranslation(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")

	var calls atomic.Int32
	deepl := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "DeepL-Auth-Key abc-123:fx" {
			http.Error(w, `{"message":"Wrong key"}`, http.StatusForbidden)
			return
		}
		var in struct {
			Text   []string `json:"text"`
			Target string   `json:"target_lang"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var tr []map[string]string
		for _, s := range in.Text {
			tr = append(tr, map[string]string{"text": "[" + in.Target + "] " + s})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"translations": tr})
	}))
	defer deepl.Close()
	oldBase, oldClient := pllMTDeepLFreeBase, pllMTClient
	pllMTDeepLFreeBase, pllMTClient = deepl.URL, deepl.Client()
	defer func() { pllMTDeepLFreeBase, pllMTClient = oldBase, oldClient }()

	// Off: the proxy refuses and nothing is sent.
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/translate", map[string]any{"texts": []string{"Hi"}, "target": "fr"}); code != http.StatusBadRequest || obj["code"] != "pll_mt_disabled" {
		t.Fatalf("translate while off: %d %v", code, obj)
	}

	// Settings: the row has a form, an http endpoint is refused, the key is sealed and never shown.
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	if !strings.Contains(body, `id="pll-mt-form"`) || strings.Contains(body, "Not available in Pursae") {
		t.Fatal("the Machine translation module has no settings form")
	}
	nonce := formNonce(t, body)
	code, _, loc := post(t, c, base+"/wp-admin/admin?page=mlang_settings", url.Values{"_wpnonce": {nonce}, "module": {"machine-translation"},
		"mt_provider": {"openai"}, "mt_key": {"abc"}, "mt_endpoint": {"http://10.0.0.1/v1"}})
	if code != http.StatusFound || !strings.Contains(loc, "pll_error=") {
		t.Fatalf("an http endpoint was accepted: %d %s", code, loc)
	}
	code, _, loc = post(t, c, base+"/wp-admin/admin?page=mlang_settings", url.Values{"_wpnonce": {nonce}, "module": {"machine-translation"},
		"mt_provider": {"deepl"}, "mt_key": {"abc-123:fx"}, "mt_formality": {"prefer_less"}})
	if code != http.StatusFound || !strings.Contains(loc, "settings-updated=true") {
		t.Fatalf("save settings: %d %s", code, loc)
	}
	if o := srv.pllOpts(); !strings.HasPrefix(o.MTKey, pllSealPrefix) || o.MTProvider != "deepl" || o.MTFormality != "prefer_less" {
		t.Fatalf("saved options: provider %q formality %q key sealed %v", o.MTProvider, o.MTFormality, strings.HasPrefix(o.MTKey, pllSealPrefix))
	}
	// Saving again with the field empty keeps the key.
	_, _, _ = post(t, c, base+"/wp-admin/admin?page=mlang_settings", url.Values{"_wpnonce": {nonce}, "module": {"machine-translation"}, "mt_provider": {"deepl"}})
	if srv.pllMTKey() != "abc-123:fx" {
		t.Fatal("an empty key field removed the saved key")
	}
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_settings&module=machine-translation")
	if strings.Contains(body, "abc-123") || !strings.Contains(body, "(saved)") {
		t.Fatal("the settings screen shows the key or no saved placeholder")
	}
	if srv.pllMTEnabled() {
		t.Fatal("machine translation is enabled before the module is activated")
	}
	// Test connection works before activation (it tests the saved settings).
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/translate/test", map[string]any{}); code != http.StatusOK || obj["sample"] != "[FR] Hello, world!" {
		t.Fatalf("test connection: %d %v", code, obj)
	}
	m := regexp.MustCompile(`href="(/wp-admin/admin\?page=mlang_settings&amp;pll_action=activate&amp;module=machine-translation[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for Machine translation")
	}
	get(t, c, base+strings.ReplaceAll(m[1], "&amp;", "&"))
	if !srv.pllMTEnabled() {
		t.Fatal("machine translation is not enabled after activation")
	}
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	if strings.Contains(body, "abc-123") {
		t.Fatal("the settings screen leaks the key")
	}
	if code, obj, _ := pllREST(t, c, base, http.MethodGet, "/wp-json/pll/v1/settings", nil); code != http.StatusOK || strings.Contains(strings.Join(pllMTMapKeys(obj), ","), "mt_key") {
		t.Fatalf("pll/v1/settings: %d %v", code, pllMTMapKeys(obj))
	}

	// REST proxy.
	code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/translate", map[string]any{"texts": []string{"Hello", "", "<b>Bold</b> text"}, "target": "fr"})
	if code != http.StatusOK {
		t.Fatalf("translate: %d %v", code, obj)
	}
	tr, _ := obj["translations"].([]any)
	if len(tr) != 3 || tr[0] != "[FR] Hello" || tr[1] != "" || tr[2] != "[FR] <b>Bold</b> text" {
		t.Fatalf("translations: %v", tr)
	}
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/translate", map[string]any{"texts": []string{"Hello"}, "target": "xx"}); code != http.StatusBadRequest {
		t.Fatalf("unknown target: %d %v", code, obj)
	}
	// Without the REST nonce the cookie does not count: forbidden.
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/pll/v1/translate", strings.NewReader(`{"texts":["Hi"],"target":"fr"}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("translate without nonce: %v %v", resp.StatusCode, err)
	} else {
		resp.Body.Close()
	}

	// Strings screen: page and per-row tools, and "Translate all empty strings".
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if !strings.Contains(body, "Translate all empty strings") || !strings.Contains(body, "pll-mt-suggest") {
		t.Fatal("the strings screen has no machine translation tools")
	}
	sm := regexp.MustCompile(`id="pll-mt-fill">\s*<input type="hidden" name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if sm == nil {
		t.Fatal("no fill form")
	}
	code, _, loc = post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {sm[1]}, "pll_action": {"mt_fill"}, "mt_lang": {"fr"}})
	if code != http.StatusFound || !strings.Contains(loc, "pll_msg=mt_filled") {
		t.Fatalf("fill: %d %s", code, loc)
	}
	if got := srv.pllStringMap(context.Background(), "fr")["Toggle Site"]; got != "[FR] Toggle Site" {
		t.Fatalf("site title in French: %q", got)
	}

	// New translation with Duplicate content and Machine translate the copy.
	id, _ := pllCreatePost(t, c, base, "?lang=en", "Machine source")
	_, edit, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(id)+"&action=edit")
	nm := regexp.MustCompile(`var nonce = "([^"]+)"`).FindStringSubmatch(edit)
	if nm == nil || !strings.Contains(edit, "pll_mt_copy") {
		t.Fatal("the Languages box has no Machine translate the copy checkbox")
	}
	for _, a := range []string{"pll_toggle_duplicate", "pll_toggle_mt"} {
		if code, out, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {a}, "_pll_nonce": {nm[1]}, "post_id": {itoa64(id)}, "on": {"1"}}); code != http.StatusOK || out != "1" {
			t.Fatalf("%s: %d %q", a, code, out)
		}
	}
	_, edit, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(id)+"&action=edit")
	edit = strings.NewReplacer(`\u0026`, "&", `\"`, `"`).Replace(edit) // the block editor panel carries the box as a JS string
	lm := regexp.MustCompile(`post-new\?post_type=post&amp;from_post=` + itoa64(id) + `&amp;new_lang=fr&amp;_wpnonce=([^"&]+)`).FindStringSubmatch(edit)
	if lm == nil {
		t.Fatal("no Add a translation link")
	}
	get(t, c, base+"/wp-admin/post-new?post_type=post&from_post="+itoa64(id)+"&new_lang=fr&_wpnonce="+lm[1])
	frID := srv.pllTranslations(context.Background(), pllPost, id)["fr"]
	if frID == 0 {
		t.Fatal("no French translation was created")
	}
	p, err := srv.store.GetPost(context.Background(), frID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "[FR] Machine source" || p.Content != "<!-- wp:paragraph -->[FR] <p>Machine source body</p><!-- /wp:paragraph -->" {
		t.Fatalf("translated copy: %q %q", p.Title, p.Content)
	}
}

func pllMTMapKeys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
