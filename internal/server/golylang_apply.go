package server

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
)

// Pursae Multi Language: the page string pass. golylangHTML runs it on every front-end page of a
// language: text the theme, plugins or the Web Builder wrote that has a translation in the Strings
// translations table (Languages > Translations) is replaced, the way TranslatePress translates a
// page. User content (posts, titles, comments, builder snapshots) is marked with sentinels and
// left alone: posts are translated per post.
//
// Cost: nothing at all while the plugin is off or the language has no string translation and
// nobody records strings (the common anonymous case); otherwise one walk over the page
// (i18n.TranslateFront) with an in-memory lookup. Pursae Cache stores the finished page, so an
// anonymous page costs one walk per cache miss.
//
// String capture: while "Record strings on pages" is on, the pass records the strings it finds
// on pages of the default language in pll_string_sources (source "page") — only for a user who can
// manage_options, or in visual translation mode, never for anonymous visitors (pages echo
// request data: ?s=, 404 paths). Search results, 404s, previews and Customizer previews never
// record.

// pllFrontTable is the lookup table of one language for the page string pass.
type pllFrontTable struct {
	tr  map[string]string // whitespace-normalised original => translation
	rev map[string]bool   // whitespace-normalised translations (already translated text)

	// Translated Date Format / Time Format settings (golylang_dates.go): source and target format.
	dateSrc, dateDst, timeSrc, timeDst string
}

var pllFrontTables = map[*Server]map[string]*pllFrontTable{} // guarded by pllStringsMu; cleared by pllFlushStrings

// pllFrontTableFor builds (once per string change) the table of lang. Strings with a gettext
// context and the URL slug bases are left out: they are no page text.
func (s *Server) pllFrontTableFor(ctx context.Context, lang string) *pllFrontTable {
	pllStringsMu.Lock()
	if t := pllFrontTables[s][lang]; t != nil {
		pllStringsMu.Unlock()
		return t
	}
	gen := pllStringsGen
	pllStringsMu.Unlock()
	src, loaded := s.pllStringMapOK(ctx, lang) // loads the strings (takes pllStringsMu)
	skip := map[string]bool{}
	if s.pllOpts().TranslateSlugs {
		for _, b := range s.pllSlugBases() {
			skip[b[1]] = true
		}
	}
	t := &pllFrontTable{tr: make(map[string]string, len(src)), rev: make(map[string]bool, len(src))}
	for o, tr := range src {
		if tr == "" || skip[o] || strings.Contains(o, "\x04") {
			continue
		}
		k := pllCollapseWS(strings.TrimSpace(o))
		v := strings.TrimSpace(tr)
		if k == "" || v == "" {
			continue
		}
		t.rev[pllCollapseWS(v)] = true
		if v != k {
			t.tr[k] = v
		}
	}
	for _, f := range []struct {
		opt      string
		src, dst *string
	}{{"date_format", &t.dateSrc, &t.dateDst}, {"time_format", &t.timeSrc, &t.timeDst}} {
		if o := s.settings.String(f.opt); o != "" {
			if tr := strings.TrimSpace(src[o]); tr != "" && tr != o {
				*f.src, *f.dst = o, tr
			}
		}
	}
	pllStringsMu.Lock()
	// Stored only when built from loaded strings no flush replaced meanwhile (a stale table would
	// outlive the flush, and Pursae Cache would store pages with the old translations).
	if loaded && gen == pllStringsGen {
		if pllFrontTables[s] == nil {
			pllFrontTables[s] = map[string]*pllFrontTable{}
		}
		pllFrontTables[s][lang] = t
	}
	pllStringsMu.Unlock()
	return t
}

// pllCaptureAllowed reports whether this page records its untranslated strings: the setting is on
// and the request is visual translation mode (visual) or comes from a user who can manage_options.
func (s *Server) pllCaptureAllowed(c *gin.Context, visual bool) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if !visual { // checked first: an anonymous page costs no option read
		if u := currentUser(c); u == nil || !u.Can("manage_options", nil) {
			return false
		}
	}
	if q := siteQueryFrom(c); q != nil && (q.Kind == "search" || q.Kind == "404" || q.Preview) {
		return false
	}
	if c.Query(czParam) != "" || c.Query("preview") != "" {
		return false
	}
	return s.pllOpts().StringCapture
}

// pllApplyStrings is the page string pass for language l: page with every known string translated.
// visit is the visual translation mode's callback (i18n.TranslateFront): it is called for every
// translatable string and returns the marker the string is wrapped with; nil on normal requests,
// which never get markers. The sentinels stay in place (golylangHTML removes them afterwards).
func (s *Server) pllApplyStrings(c *gin.Context, page []byte, l *pllLanguage, visit func(str, attr string) string) []byte {
	if l == nil || len(page) == 0 || !s.golylangOn() {
		return page
	}
	var ctx context.Context = c
	if c == nil {
		ctx = context.Background()
	}
	t := s.pllFrontTableFor(ctx, l.Slug)
	// Only a page of the default language records: originals are default-language text. A page of
	// another language shows its own text (a translated post's blocks or fields, the switcher's
	// language names), which is no source string.
	capture := l.IsDefault && s.pllCaptureAllowed(c, visit != nil)
	if len(t.tr) == 0 && !capture && visit == nil {
		return page
	}
	var r *pllReq
	path := ""
	if c != nil {
		r = pllFrom(c)
		if c.Request != nil {
			path = c.Request.URL.Path
		}
	}
	var names map[string]bool // language names and slugs (switchers, hreflang labels): never page text
	if capture {
		names = pllLanguageLabels(s.pllLanguages(ctx))
	}
	lookup := func(k string, record bool) (string, bool) {
		if v, ok := t.tr[k]; ok {
			return v, true
		}
		if v, ok := s.pllReformatDate(t, k, l); ok {
			return v, true
		}
		if record && capture && !t.rev[k] && !names[k] && !pllKnown(r, k) {
			s.pllCaptureQueue(ctx, k, "", "", "page", path)
		}
		return "", false
	}
	return i18n.TranslateFrontWith(page, lookup, visit)
}

// pllLanguageLabels are the texts a language switcher shows for langs: names, slugs and names
// with the flag emoji, whitespace collapsed.
func pllLanguageLabels(langs []*pllLanguage) map[string]bool {
	out := make(map[string]bool, 3*len(langs))
	for _, l := range langs {
		for _, v := range []string{l.Name, l.Slug} {
			if v = pllCollapseWS(strings.TrimSpace(v)); v != "" {
				out[v] = true
				if f := l.FlagEmoji(); f != "" {
					out[f+" "+v] = true
				}
			}
		}
	}
	return out
}

// pllVisualPage is set by visual translation mode (golylang_visual.go). golylangHTML calls it with
// the page (lang and hreflang done, sentinels still in place); when the request is a visual
// session it returns the finished page (string pass with markers, sentinels turned into regions
// by pllUCFinish(page, true), editor script added) and true.
var pllVisualPage func(s *Server, c *gin.Context, page []byte, l *pllLanguage) ([]byte, bool)
