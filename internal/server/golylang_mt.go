package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
	"wpgo/internal/wp"
)

// Pursae Multi Language: the Machine translation module (Polylang Pro's DeepL module, with Google
// Cloud Translation and OpenAI-compatible chat APIs too). Text leaves the site only through the
// server (the admin pages keep connect-src 'self') and only when the module is on, a key is saved
// and a manager asks for a translation: the strings screen, the visual editor's "Suggest" button,
// a new translation with "Machine translate the copy", or the REST proxy POST pll/v1/translate.
// Nothing is translated automatically for visitors, and the proxy never saves.
//
// Language codes come from the language's locale (pllLanguage.Locale, else the slug):
//
//	DeepL   target: en_US/en → EN-US, other en_* → EN-GB; pt_BR → PT-BR, other pt → PT-PT;
//	        zh_TW/zh_HK/zh_MO → ZH-HANT, other zh → ZH; nb/nn/no → NB; else the base code in
//	        capitals (FR, DE…). Source: the base code (EN, PT, ZH, NB…).
//	Google  zh_TW/zh_HK/zh_MO → zh-TW, other zh → zh-CN; pt_PT → pt-PT, other pt → pt;
//	        nb/nn/no → no; fil → tl; else the base code (fr, de…).
//	OpenAI  the language name and its W3C tag ("French (fr-FR)").
//
// A nil source language means "detect it" (strings registered by themes are usually English
// whatever the default language is).

// mtProvider is one machine translation service.
type mtProvider interface {
	// Translate returns one translation per text, in order. src may be "" (detect).
	Translate(ctx context.Context, texts []string, src, dst string, html bool) ([]string, error)
	// Lang is the provider's code of a language (as a target or as a source).
	Lang(l *pllLanguage, target bool) string
	// Limits are the most texts and bytes one request may carry.
	Limits() (texts, bytes int)
	// Protects says whether the provider keeps <span translate="no" class="notranslate"> untouched.
	Protects() bool
}

const (
	pllMTMaxResponse = 8 << 20 // bytes read from a provider answer
	pllMTMaxTexts    = 200     // texts per REST proxy call
	pllMTMaxBytes    = 200_000 // bytes per REST proxy call
	pllMTRatePerMin  = 60      // REST proxy calls per user and minute
	pllMTOpenAIBase  = "https://api.openai.com/v1"
)

// Base URLs of the fixed-host providers; tests point them at httptest servers.
var (
	pllMTDeepLFreeBase = "https://api-free.deepl.com"
	pllMTDeepLProBase  = "https://api.deepl.com"
	pllMTGoogleBase    = "https://translation.googleapis.com"
)

// pllMTAllowPrivate lets tests reach httptest servers on the loopback address.
var pllMTAllowPrivate atomic.Bool

// pllMTClient talks to the providers: public addresses only, checked when the connection is made
// (after DNS resolution, so a custom endpoint whose name points at 127.0.0.1 or a cloud metadata
// address is refused), no proxy (it would hide the real destination), no redirects, 20 s.
var pllMTClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second,
			Control: publicOnly(pllMTAllowPrivate.Load, "the translation service address points at a private network")}).DialContext},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

var errPllMTOff = errors.New("machine translation is not enabled: activate the Machine translation module and save an API key in Languages > Settings")

// pllMTProviders are the choices of the settings form.
var pllMTProviders = [][2]string{{"deepl", "DeepL"}, {"google", "Google Cloud Translation"}, {"openai", "OpenAI-compatible API"}}

// pllMTFormalities are the formality choices (DeepL; a hint for chat models).
var pllMTFormalities = [][2]string{{"default", "Default"}, {"prefer_more", "More formal"}, {"prefer_less", "Less formal"}}

// pllMTEndpointOK checks an OpenAI-compatible base URL: https, a host, no credentials, no query.
func pllMTEndpointOK(v string) bool {
	u, err := url.Parse(strings.TrimSpace(v))
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !hasControlChar(v)
}

// pllMTEnabled says whether machine translation can be used: plugin and module on, a key saved,
// and for the OpenAI-compatible provider a valid https endpoint.
func (s *Server) pllMTEnabled() bool {
	if !s.golylangOn() {
		return false
	}
	o := s.pllOpts()
	_, err := s.pllMTProviderFor(o)
	return o.MT && err == nil
}

// pllMTProviderFor builds the provider of the saved settings.
func (s *Server) pllMTProviderFor(o pllOptions) (mtProvider, error) {
	key := s.pllRevealMTKey(o.MTKey)
	if key == "" {
		return nil, errors.New("no API key is saved")
	}
	switch o.MTProvider {
	case "deepl", "":
		base := pllMTDeepLProBase
		if strings.HasSuffix(key, ":fx") {
			base = pllMTDeepLFreeBase
		}
		return &pllDeepL{key: key, base: base, formality: o.MTFormality, client: pllMTClient}, nil
	case "google":
		return &pllGoogle{key: key, base: pllMTGoogleBase, client: pllMTClient}, nil
	case "openai":
		ep := strings.TrimRight(strings.TrimSpace(o.MTEndpoint), "/")
		if ep == "" {
			ep = pllMTOpenAIBase
		}
		if !pllMTEndpointOK(ep) {
			return nil, errors.New("the API endpoint must be an https URL")
		}
		model := strings.TrimSpace(o.MTModel)
		if model == "" {
			model = "gpt-4o-mini"
		}
		return &pllOpenAI{key: key, endpoint: ep, model: model, formality: o.MTFormality, client: pllMTClient}, nil
	}
	return nil, errors.New("unknown machine translation provider")
}

// pllMachineTranslate translates texts from src (nil: detect) into dst with the saved provider.
// Blank texts and texts without letters come back unchanged; the rest is sent in batches within
// the provider's limits. With html the texts are HTML and their tags are kept. In text mode
// placeholders (%s, %1$s, %d, {name}) are protected from the translation.
func (s *Server) pllMachineTranslate(ctx context.Context, texts []string, src, dst *pllLanguage, html bool) ([]string, error) {
	if !s.golylangOn() {
		return nil, errPllMTOff
	}
	o := s.pllOpts()
	if !o.MT {
		return nil, errPllMTOff
	}
	p, err := s.pllMTProviderFor(o)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", errPllMTOff, err)
	}
	return pllMTRun(ctx, p, texts, src, dst, html)
}

// pllMTRun is pllMachineTranslate with a given provider (tests).
func pllMTRun(ctx context.Context, p mtProvider, texts []string, src, dst *pllLanguage, asHTML bool) ([]string, error) {
	if dst == nil {
		return nil, errors.New("no target language")
	}
	out := make([]string, len(texts))
	copy(out, texts)
	srcCode := ""
	if src != nil {
		srcCode = p.Lang(src, false)
	}
	dstCode := p.Lang(dst, true)
	if srcCode != "" && strings.EqualFold(srcCode, dstCode) {
		return out, nil
	}
	type job struct {
		idx       int
		text      string
		lead, end string
		protected bool
	}
	var jobs []job
	for i, t := range texts {
		trim := strings.TrimSpace(t)
		if trim == "" || !hasLetter(trim) {
			continue
		}
		j := job{idx: i, text: trim}
		j.lead = t[:strings.Index(t, trim)]
		j.end = t[len(j.lead)+len(trim):]
		if !asHTML && p.Protects() && pllMTPlaceholder.MatchString(trim) {
			j.text, j.protected = pllMTProtect(trim), true
		}
		jobs = append(jobs, j)
	}
	maxN, maxB := p.Limits()
	// Protected texts travel as HTML; send them in their own batches.
	for _, protected := range []bool{false, true} {
		var batch []job
		size := 0
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			in := make([]string, len(batch))
			for i, j := range batch {
				in[i] = j.text
			}
			res, err := p.Translate(ctx, in, srcCode, dstCode, asHTML || protected)
			if err != nil {
				return err
			}
			if len(res) != len(in) {
				return fmt.Errorf("the translation service answered %d texts for %d", len(res), len(in))
			}
			for i, j := range batch {
				tr := res[i]
				if j.protected {
					tr = pllMTUnprotect(tr)
				}
				if strings.TrimSpace(tr) == "" {
					tr = texts[j.idx]
				} else {
					tr = j.lead + strings.TrimSpace(tr) + j.end
				}
				out[j.idx] = tr
			}
			batch, size = nil, 0
			return nil
		}
		for _, j := range jobs {
			if j.protected != protected {
				continue
			}
			if len(batch) > 0 && (len(batch) >= maxN || size+len(j.text) > maxB) {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			batch = append(batch, j)
			size += len(j.text)
		}
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// pllMTPlaceholder matches printf placeholders (%s, %1$s, %d, %.2f) and {name} tokens.
var pllMTPlaceholder = regexp.MustCompile(`%(?:\d+\$)?[-+ 0#]*\d*(?:\.\d+)?[sdfFuxXboceEgG]|\{[A-Za-z0-9_.:-]+\}`)

var pllMTProtected = regexp.MustCompile(`(?s)<span[^>]*notranslate[^>]*>(.*?)</span>`)

// pllMTProtect turns a plain text into HTML whose placeholders are marked as not translatable.
func pllMTProtect(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range pllMTPlaceholder.FindAllStringIndex(s, -1) {
		b.WriteString(html.EscapeString(s[last:m[0]]))
		b.WriteString(`<span translate="no" class="notranslate">` + html.EscapeString(s[m[0]:m[1]]) + `</span>`)
		last = m[1]
	}
	b.WriteString(html.EscapeString(s[last:]))
	return b.String()
}

// pllMTUnprotect reverses pllMTProtect on a translation.
func pllMTUnprotect(s string) string {
	return html.UnescapeString(pllMTProtected.ReplaceAllString(s, "$1"))
}

// Language codes -------------------------------------------------------------------------------

// pllMTLocale splits a language's locale into its lowercase base code and uppercase region.
func pllMTLocale(l *pllLanguage) (base, region string) {
	loc := l.Locale
	if loc == "" {
		loc = l.Slug
	}
	loc = strings.ReplaceAll(loc, "-", "_")
	base, region, _ = strings.Cut(loc, "_")
	base = strings.ToLower(base)
	region = strings.ToUpper(region)
	if i := strings.Index(region, "_"); i >= 0 { // de_DE_formal
		region = region[:i]
	}
	return base, region
}

func pllMTTraditional(region string) bool { return region == "TW" || region == "HK" || region == "MO" }

// pllDeepLLang is DeepL's code of a language (target or source_lang).
func pllDeepLLang(l *pllLanguage, target bool) string {
	base, region := pllMTLocale(l)
	switch base {
	case "nb", "nn", "no":
		return "NB"
	}
	if !target {
		return strings.ToUpper(base)
	}
	switch base {
	case "en":
		if region == "" || region == "US" {
			return "EN-US"
		}
		return "EN-GB"
	case "pt":
		if region == "BR" {
			return "PT-BR"
		}
		return "PT-PT"
	case "zh":
		if pllMTTraditional(region) {
			return "ZH-HANT"
		}
		return "ZH"
	}
	return strings.ToUpper(base)
}

// pllGoogleLang is Google Cloud Translation's code of a language.
func pllGoogleLang(l *pllLanguage, _ bool) string {
	base, region := pllMTLocale(l)
	switch base {
	case "zh":
		if pllMTTraditional(region) {
			return "zh-TW"
		}
		return "zh-CN"
	case "pt":
		if region == "PT" {
			return "pt-PT"
		}
		return "pt"
	case "nb", "nn", "no":
		return "no"
	case "fil":
		return "tl"
	}
	return base
}

// pllOpenAILang names a language for a chat model.
func pllOpenAILang(l *pllLanguage, _ bool) string {
	name := strings.TrimSpace(l.Name)
	tag := l.W3C()
	if tag == "" {
		tag = l.Slug
	}
	if name == "" {
		return tag
	}
	return name + " (" + tag + ")"
}

// HTTP helpers ---------------------------------------------------------------------------------

// pllMTDo sends a JSON request and decodes a JSON answer. Errors never carry the URL or the key.
func pllMTDo(ctx context.Context, client *http.Client, req *http.Request, key string, out any, errMsg func([]byte) string) error {
	if gc, ok := ctx.(*gin.Context); ok && gc.Request != nil {
		// Never the *gin.Context itself: the transport touches the request's context after the
		// body is closed, by then gin may have reused it for another request (a data race).
		ctx = gc.Request.Context()
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Pursae/"+Version+" (Multi Language)")
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if errors.Is(err, context.DeadlineExceeded) || (ue != nil && ue.Timeout()) {
			return errors.New("the translation service did not answer in time")
		}
		return errors.New("cannot reach the translation service: " + pllMTScrub(err.Error(), key))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, pllMTMaxResponse+1))
	if err != nil {
		return errors.New("cannot read the translation service answer")
	}
	if len(body) > pllMTMaxResponse {
		return errors.New("the translation service answer is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := ""
		if errMsg != nil {
			msg = errMsg(body)
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			msg = "authorization failed, check the API key" + pllMTSuffix(msg)
		case resp.StatusCode == http.StatusTooManyRequests:
			msg = "too many requests, try again later" + pllMTSuffix(msg)
		case resp.StatusCode == 456:
			msg = "the translation quota is used up" + pllMTSuffix(msg)
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			msg = "the translation service redirected the request"
		case msg == "":
			msg = "HTTP " + strconv.Itoa(resp.StatusCode)
		}
		return errors.New("the translation service refused the request: " + pllMTScrub(msg, key))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("the translation service answer is not valid JSON")
	}
	return nil
}

func pllMTSuffix(msg string) string {
	if msg == "" {
		return ""
	}
	return " (" + msg + ")"
}

// pllMTScrub shortens a provider message and removes the key from it.
func pllMTScrub(msg, key string) string {
	if len(key) >= 6 { // a real key; a short test value would mangle words
		msg = strings.ReplaceAll(msg, key, "…")
	}
	msg = strings.TrimSpace(msg)
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return msg
}

// pllMTErrMessage reads {"message": …} and {"error": {"message": …}} error bodies.
func pllMTErrMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	switch v := e.Error.(type) {
	case string:
		return v
	case map[string]any:
		if m, _ := v["message"].(string); m != "" {
			return m
		}
	}
	return ""
}

// DeepL ----------------------------------------------------------------------------------------

type pllDeepL struct {
	key, base, formality string
	client               *http.Client
}

func (p *pllDeepL) Lang(l *pllLanguage, target bool) string { return pllDeepLLang(l, target) }
func (p *pllDeepL) Limits() (int, int)                      { return 50, 100_000 }
func (p *pllDeepL) Protects() bool                          { return true }

// deeplFormality turns the saved formality into one DeepL accepts for every language.
func deeplFormality(f string) string {
	switch f {
	case "more", "prefer_more":
		return "prefer_more"
	case "less", "prefer_less":
		return "prefer_less"
	}
	return ""
}

func (p *pllDeepL) Translate(ctx context.Context, texts []string, src, dst string, asHTML bool) ([]string, error) {
	body := map[string]any{"text": texts, "target_lang": dst}
	if src != "" {
		body["source_lang"] = src
	}
	if asHTML {
		body["tag_handling"] = "html"
	}
	if f := deeplFormality(p.formality); f != "" {
		body["formality"] = f
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, p.base+"/v2/translate", bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid translation service address")
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+p.key)
	var out struct {
		Translations []struct {
			Text string `json:"text"`
		} `json:"translations"`
	}
	if err := pllMTDo(ctx, p.client, req, p.key, &out, pllMTErrMessage); err != nil {
		return nil, err
	}
	res := make([]string, len(out.Translations))
	for i, t := range out.Translations {
		res[i] = t.Text
	}
	return res, nil
}

// Google Cloud Translation (v2, API key) -----------------------------------------------------

type pllGoogle struct {
	key, base string
	client    *http.Client
}

func (p *pllGoogle) Lang(l *pllLanguage, target bool) string { return pllGoogleLang(l, target) }
func (p *pllGoogle) Limits() (int, int)                      { return 128, 30_000 }
func (p *pllGoogle) Protects() bool                          { return true }

func (p *pllGoogle) Translate(ctx context.Context, texts []string, src, dst string, asHTML bool) ([]string, error) {
	format := "text"
	if asHTML {
		format = "html"
	}
	body := map[string]any{"q": texts, "target": dst, "format": format}
	if src != "" {
		body["source"] = src
	}
	raw, _ := json.Marshal(body)
	// The key goes in a header, not in ?key=, so it can never appear in an error or a log line.
	req, err := http.NewRequest(http.MethodPost, p.base+"/language/translate/v2", bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid translation service address")
	}
	req.Header.Set("X-Goog-Api-Key", p.key)
	var out struct {
		Data struct {
			Translations []struct {
				TranslatedText string `json:"translatedText"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := pllMTDo(ctx, p.client, req, p.key, &out, pllMTErrMessage); err != nil {
		return nil, err
	}
	res := make([]string, len(out.Data.Translations))
	for i, t := range out.Data.Translations {
		res[i] = t.TranslatedText
	}
	return res, nil
}

// OpenAI-compatible chat completions ---------------------------------------------------------

type pllOpenAI struct {
	key, endpoint, model, formality string
	client                          *http.Client
}

func (p *pllOpenAI) Lang(l *pllLanguage, target bool) string { return pllOpenAILang(l, target) }
func (p *pllOpenAI) Limits() (int, int)                      { return 50, 12_000 }
func (p *pllOpenAI) Protects() bool                          { return false }

func (p *pllOpenAI) Translate(ctx context.Context, texts []string, src, dst string, asHTML bool) ([]string, error) {
	from := "the source language (detect it)"
	if src != "" {
		from = src
	}
	sys := "You are a translation engine. Translate every string of the JSON array the user sends from " + from + " into " + dst + ". " +
		"Answer with a JSON object {\"translations\": [...]} holding exactly one translated string per input string, in the same order, and nothing else. " +
		"Keep placeholders such as %s, %d, %1$s and {name} unchanged. Do not add explanations, quotes or notes. "
	if asHTML {
		sys += "The strings are HTML: keep every tag, attribute and HTML comment unchanged and translate only the human-readable text. "
	} else {
		sys += "The strings are plain text. "
	}
	switch deeplFormality(p.formality) {
	case "prefer_more":
		sys += "Use a formal tone."
	case "prefer_less":
		sys += "Use an informal tone."
	}
	in, _ := json.Marshal(texts)
	body := map[string]any{"model": p.model, "temperature": 0,
		"messages": []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": string(in)}}}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, p.endpoint+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid translation service address")
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := pllMTDo(ctx, p.client, req, p.key, &out, pllMTErrMessage); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("the translation service gave no answer")
	}
	res, err := pllMTParseChat(out.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	if len(res) != len(texts) {
		return nil, fmt.Errorf("the translation service answered %d texts for %d", len(res), len(texts))
	}
	return res, nil
}

// pllMTParseChat reads a chat answer: {"translations": [...]} or a bare array, maybe in a code fence.
func pllMTParseChat(content string) ([]string, error) {
	content = strings.TrimSpace(content)
	if i := strings.IndexAny(content, "[{"); i > 0 {
		content = content[i:]
	}
	if i := strings.LastIndexAny(content, "]}"); i >= 0 {
		content = content[:i+1]
	}
	var arr []string
	if json.Unmarshal([]byte(content), &arr) == nil {
		return arr, nil
	}
	var obj struct {
		Translations []string `json:"translations"`
	}
	if json.Unmarshal([]byte(content), &obj) == nil && obj.Translations != nil {
		return obj.Translations, nil
	}
	return nil, errors.New("the translation service answer is not a list of translations")
}

// Block content --------------------------------------------------------------------------------

// pllBlockDelim matches the block comment delimiters of serialized block content.
var pllBlockDelim = regexp.MustCompile(`(?s)<!--\s*/?wp:.*?-->`)

// pllMTTranslateHTML translates post content: the HTML between block comment delimiters is sent,
// the delimiters (and their attributes) are kept byte for byte.
func (s *Server) pllMTTranslateHTML(ctx context.Context, content string, src, dst *pllLanguage) (string, error) {
	return pllMTBlocks(content, func(texts []string) ([]string, error) { return s.pllMachineTranslate(ctx, texts, src, dst, true) })
}

// pllMTKsesSafe reports whether KSES keeps the markup of an HTML part (same tags and attributes):
// only such parts are machine translated, and their translation is run through KSES.
func pllMTKsesSafe(part string) bool {
	k := wp.KsesPost(part)
	return strings.Count(k, "<") == strings.Count(part, "<") && strings.Count(k, "=") == strings.Count(part, "=")
}

// pllMTBlocks splits content at block comment delimiters, translates the HTML parts that hold
// letters with tr (one call) and joins it back. Parts KSES would change are left as they are and
// every translated part goes through wp.KsesPost (the answer of a translation service is untrusted).
func pllMTBlocks(content string, tr func([]string) ([]string, error)) (string, error) {
	if strings.TrimSpace(content) == "" {
		return content, nil
	}
	var parts []string
	last := 0
	for _, m := range pllBlockDelim.FindAllStringIndex(content, -1) {
		parts = append(parts, content[last:m[0]], content[m[0]:m[1]])
		last = m[1]
	}
	parts = append(parts, content[last:])
	// Even indexes are HTML, odd ones are delimiters.
	var texts []string
	var at []int
	for i := 0; i < len(parts); i += 2 {
		if hasLetter(parts[i]) && pllMTKsesSafe(parts[i]) {
			texts = append(texts, parts[i])
			at = append(at, i)
		}
	}
	if len(texts) == 0 {
		return content, nil
	}
	res, err := tr(texts)
	if err != nil {
		return "", err
	}
	if len(res) != len(texts) {
		return "", errors.New("the translation service answered a wrong number of texts")
	}
	for k, i := range at {
		parts[i] = wp.KsesPost(res[k])
	}
	return strings.Join(parts, ""), nil
}

// pllMTTranslatePost machine-translates the title, excerpt and content a new translation copied
// from its source (Duplicate content with "Machine translate the copy").
//
// The provider's answer is untrusted (a prompt-injected or compromised service can return any
// markup), so the translated title, excerpt and every translated HTML part of the content go
// through wp.KsesPost. A part whose source holds markup KSES would remove (written by a user with
// unfiltered_html, e.g. a Custom HTML block) is not sent and stays as copied.
func (s *Server) pllMTTranslatePost(ctx context.Context, p *store.Post, src, dst *pllLanguage) error {
	if !s.pllMTEnabled() || p == nil || dst == nil {
		return errPllMTOff
	}
	title, excerpt := p.Title, p.Excerpt
	if res, err := s.pllMachineTranslate(ctx, []string{p.Title, p.Excerpt}, src, dst, false); err != nil {
		return err
	} else {
		title, excerpt = wp.KsesPost(strings.TrimSpace(res[0])), wp.KsesPost(res[1])
	}
	content := p.Content
	// A Web Builder page keeps its doc (the source of truth, copied in the source language) and the
	// content generated from it: rewriting posts.content would detach the doc. Its texts are
	// translated in the builder (or the visual editor).
	if _, builder := p.Meta[webbuilder.PostMarkerMeta]; !builder {
		var err error
		if content, err = s.pllMTTranslateHTML(ctx, p.Content, src, dst); err != nil {
			return err
		}
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET title = $2, excerpt = $3, content = $4 WHERE id = $1`, p.ID, title, excerpt, content); err != nil {
		return err
	}
	p.Title, p.Excerpt, p.Content = title, excerpt, content
	return nil
}

// pllMTCopyPerMin is how many new translations one user can machine translate per minute (the
// site's paid quota is shared by every author who can create translations).
const pllMTCopyPerMin = 10

// pllMTCopyWanted is the user's "Machine translate the copy" choice for a post type.
func pllMTCopyWanted(u *store.User, typ string) bool {
	if u == nil {
		return false
	}
	m, _ := u.Meta["pll_mt_copy"].(map[string]any)
	on, _ := m[typ].(bool)
	return on
}

// pllMTAfterCopy runs after a new translation copied its source: when Duplicate content and
// "Machine translate the copy" are on, the copy is translated. A failure keeps the copy as it is.
func (s *Server) pllMTAfterCopy(c *gin.Context, src, p *store.Post, l *pllLanguage, duplicate bool) {
	if !duplicate || !pllMTCopyWanted(currentUser(c), p.Type) || !s.pllMTEnabled() {
		return
	}
	if u := currentUser(c); u == nil || !gooikeAllow("pll-mt-copy:"+strconv.FormatInt(u.ID, 10), pllMTCopyPerMin) {
		slog.Warn("golylang: machine translation of a new translation skipped (rate limit)", "post", p.ID)
		return
	}
	from := s.pllLang(c, s.pllObjLang(c, pllPost, src.ID))
	if err := s.pllMTTranslatePost(c, p, from, l); err != nil {
		slog.Warn("golylang: machine translation of a new translation failed", "post", p.ID, "source", src.ID, "err", err)
	}
}

// Strings --------------------------------------------------------------------------------------

// pllMTStrings translates registered strings (source detected); texts with HTML tags go as HTML.
func (s *Server) pllMTStrings(ctx context.Context, texts []string, dst *pllLanguage) ([]string, error) {
	out := make([]string, len(texts))
	var plain, tagged []string
	var plainAt, taggedAt []int
	for i, t := range texts {
		if strings.Contains(t, "<") && strings.Contains(t, ">") {
			tagged, taggedAt = append(tagged, t), append(taggedAt, i)
		} else {
			plain, plainAt = append(plain, t), append(plainAt, i)
		}
	}
	for _, g := range []struct {
		in   []string
		at   []int
		html bool
	}{{plain, plainAt, false}, {tagged, taggedAt, true}} {
		if len(g.in) == 0 {
			continue
		}
		res, err := s.pllMachineTranslate(ctx, g.in, nil, dst, g.html)
		if err != nil {
			return nil, err
		}
		for k, i := range g.at {
			out[i] = res[k]
		}
	}
	return out, nil
}

// pllMTFillMax is the most strings one "Translate all empty strings" run translates.
const pllMTFillMax = 1000

// pllMTFillStrings translates the registered strings without a translation in lang and saves them.
func (s *Server) pllMTFillStrings(ctx context.Context, l *pllLanguage) (int, error) {
	have := s.pllStringMap(ctx, l.Slug)
	seen := map[string]bool{}
	var todo, keys []string
	for _, r := range s.pllStrings(ctx) {
		// The key (context\x04msgid for _x strings) is what the translation is stored and looked up
		// under. URL slug bases are path segments: a machine translation ("Catégorie") is no slug.
		k := r.Key()
		if r.Group == pllSlugGroup || seen[k] || strings.TrimSpace(have[k]) != "" || !hasLetter(r.String) {
			continue
		}
		seen[k] = true
		todo = append(todo, r.String)
		keys = append(keys, k)
		if len(todo) >= pllMTFillMax {
			break
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}
	res, err := s.pllMTStrings(ctx, todo, l)
	if err != nil {
		return 0, err
	}
	n := 0
	for i, orig := range todo {
		if tr := strings.TrimSpace(res[i]); tr != "" && tr != orig {
			if s.pllSetString(ctx, l.Slug, keys[i], tr) == nil {
				n++
			}
		}
	}
	s.pllFlushStrings()
	s.purgeAll(ctx)
	return n, nil
}

// REST proxy -----------------------------------------------------------------------------------

// registerPllMTREST adds POST pll/v1/translate (the proxy) and POST pll/v1/translate/test.
func (s *Server) registerPllMTREST() {
	guard := func(c *gin.Context) *store.User {
		c.Header("Cache-Control", "no-store")
		u := restCurrent(c)
		if u == nil || !u.Can("manage_options", nil) {
			restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
			return nil
		}
		if !gooikeAllow("pll-mt:"+strconv.FormatInt(u.ID, 10), pllMTRatePerMin) {
			c.Header("Retry-After", "60")
			restErr(c, http.StatusTooManyRequests, "pll_mt_rate_limited", "Too many translation requests. Please wait a minute.")
			return nil
		}
		return u
	}
	s.rest.register(pllRESTNamespace, "/pll/v1/translate", map[string]restHandler{
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			if guard(c) == nil {
				return
			}
			if !s.pllMTEnabled() {
				restErr(c, http.StatusBadRequest, "pll_mt_disabled", errPllMTOff.Error())
				return
			}
			raw, ok := r.Body["texts"].([]any)
			if !ok {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): texts")
				return
			}
			if len(raw) > pllMTMaxTexts {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", fmt.Sprintf("At most %d texts can be translated at once.", pllMTMaxTexts))
				return
			}
			texts := make([]string, 0, len(raw))
			size := 0
			for _, v := range raw {
				t, ok := v.(string)
				if !ok {
					restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): texts")
					return
				}
				size += len(t)
				texts = append(texts, t)
			}
			if size > pllMTMaxBytes {
				restErr(c, http.StatusRequestEntityTooLarge, "rest_invalid_param", "The texts are too long.")
				return
			}
			tgt, _ := r.Str(c, "target")
			dst := s.pllLang(c, tgt)
			if dst == nil {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): target")
				return
			}
			var src *pllLanguage
			if v, _ := r.Str(c, "source"); v != "" {
				if src = s.pllLang(c, v); src == nil {
					restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): source")
					return
				}
			}
			var res []string
			var err error
			if h, ok := r.Bool(c, "html"); ok {
				res, err = s.pllMachineTranslate(c, texts, src, dst, h)
			} else if src == nil {
				res, err = s.pllMTStrings(c, texts, dst)
			} else {
				res, err = s.pllMachineTranslate(c, texts, src, dst, false)
			}
			if err != nil {
				restErr(c, http.StatusBadGateway, "pll_mt_failed", err.Error())
				return
			}
			restJSON(c, http.StatusOK, map[string]any{"translations": res, "target": dst.Slug})
		},
	}, nil)
	s.rest.register(pllRESTNamespace, "/pll/v1/translate/test", map[string]restHandler{
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			if guard(c) == nil {
				return
			}
			o := s.pllOpts()
			p, err := s.pllMTProviderFor(o)
			if err != nil {
				restErr(c, http.StatusBadRequest, "pll_mt_disabled", "Save the settings first: "+err.Error()+".")
				return
			}
			dst := s.pllMTTestTarget(c)
			res, err := pllMTRun(c, p, []string{"Hello, world!"}, &pllLanguage{Slug: "en", Locale: "en_US", Name: "English"}, dst, false)
			if err != nil {
				restErr(c, http.StatusBadGateway, "pll_mt_failed", err.Error())
				return
			}
			restJSON(c, http.StatusOK, map[string]any{"ok": true, "language": dst.Name, "sample": res[0]})
		},
	}, nil)
}

// pllMTTestTarget is the language "Test connection" translates into: the first one that is not
// English, else French.
func (s *Server) pllMTTestTarget(ctx context.Context) *pllLanguage {
	for _, l := range s.pllLanguages(ctx) {
		if b, _ := pllMTLocale(l); b != "en" {
			return l
		}
	}
	return &pllLanguage{Slug: "fr", Locale: "fr_FR", Name: "French"}
}

// Settings and screens -------------------------------------------------------------------------

// pllMTSettingsSave applies the Machine translation settings form.
func (s *Server) pllMTSettingsSave(c *gin.Context, o *pllOptions) string {
	prov := c.PostForm("mt_provider")
	ok := false
	for _, p := range pllMTProviders {
		ok = ok || p[0] == prov
	}
	if !ok {
		prov = "deepl"
	}
	ep := strings.TrimRight(strings.TrimSpace(c.PostForm("mt_endpoint")), "/")
	if ep != "" && !pllMTEndpointOK(ep) {
		return "The API endpoint must be an https URL."
	}
	// The saved key is never rendered back, so it must not follow the settings to another service:
	// changing the provider or the OpenAI-compatible endpoint's host needs the key typed again.
	newKey := strings.TrimSpace(c.PostForm("mt_key"))
	typed := newKey != "" && newKey != pllMTKeyPlaceholder
	remove := c.PostForm("mt_key_remove") == "1"
	if o.MTKey != "" && !typed && !remove && pllMTKeyTarget(prov, ep) != pllMTKeyTarget(o.MTProvider, o.MTEndpoint) {
		return "Enter the API key again: the saved key is not sent to another provider or endpoint."
	}
	o.MTProvider = prov
	o.MTKey = s.pllMTKeyInput(o.MTKey, c.PostForm("mt_key"), remove)
	f := c.PostForm("mt_formality")
	o.MTFormality = "default"
	for _, x := range pllMTFormalities {
		if x[0] == f {
			o.MTFormality = f
		}
	}
	if m := strings.TrimSpace(c.PostForm("mt_model")); m != "" && len(m) <= 200 && !hasControlChar(m) {
		o.MTModel = m
	}
	o.MTEndpoint = ep
	return ""
}

// pllMTKeyTarget is where a provider sends the key: the provider, and for the OpenAI-compatible
// one the endpoint's host (the default endpoint when empty).
func pllMTKeyTarget(provider, endpoint string) string {
	if provider != "openai" {
		return provider
	}
	if endpoint == "" {
		endpoint = pllMTOpenAIBase
	}
	host := endpoint
	if u, err := url.Parse(endpoint); err == nil {
		host = strings.ToLower(u.Host)
	}
	return provider + "|" + host
}

// pllMTView is what the settings row and the strings screen need to know.
type pllMTView struct {
	Enabled    bool
	HasKey     bool
	RESTNonce  string
	Providers  [][2]string
	Formality  [][2]string
	OpenAIBase string
}

func (s *Server) pllMTViewFor(c *gin.Context) pllMTView {
	o := s.pllOpts()
	return pllMTView{Enabled: s.pllMTEnabled(), HasKey: o.MTKey != "", RESTNonce: s.nonce(c, "wp_rest"),
		Providers: pllMTProviders, Formality: pllMTFormalities, OpenAIBase: pllMTOpenAIBase}
}

// pllMTStringsFill handles "Translate all empty strings" of the strings screen.
func (s *Server) pllMTStringsFill(c *gin.Context) {
	if !s.checkNonce(c, "string-translation") {
		s.denyNonce(c)
		return
	}
	back := "/wp-admin/admin?page=mlang_strings"
	l := s.pllLang(c, c.PostForm("mt_lang"))
	if l == nil {
		c.Redirect(http.StatusFound, back+"&pll_msg=import_error&err="+url.QueryEscape("Choose a language."))
		return
	}
	n, err := s.pllMTFillStrings(c, l)
	if err != nil {
		c.Redirect(http.StatusFound, back+"&pll_msg=import_error&err="+url.QueryEscape("Machine translation failed: "+err.Error()))
		return
	}
	c.Redirect(http.StatusFound, back+"&pll_msg=mt_filled&n="+strconv.Itoa(n))
}
