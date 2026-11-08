package server

import (
	"context"
	"fmt"
	"html/template"
	"strings"
)

// Pursae Multi Language: gettext for themes and plugins. Theme templates call __, _e, _x, _n,
// esc_html__ and esc_attr__ (the site view first, like the pll_* functions); Wasm plugins call the
// "translate" host import (wpgo.T / wpgo.X). Each string is translated into the request language
// (the Strings translations table first, then the installed language packs, then the string
// itself) and recorded in pll_string_sources so it shows on Languages > Translations under its
// theme or plugin. While the plugin is off every function returns its msgid unchanged.

// pllDomainRegistered is the domain of strings registered the Polylang way (pll_register_string,
// pll__, theme mods): their context column holds the string's name and they are translated by the
// original alone. Other rows with a context are translated under "context\x04original".
const pllDomainRegistered = "pll"

// pllStringKey is the pll_strings.original a string is translated under.
func pllStringKey(original, context, domain string) string {
	if context == "" || domain == pllDomainRegistered {
		return original
	}
	return context + "\x04" + original
}

// pllGettext translates msgid (with an optional gettext context; plural and n for _n) into the
// language of ctx and records it with source and domain.
func (s *Server) pllGettext(ctx context.Context, msgid, plural string, n int, context, domain, source string) string {
	out := msgid
	if plural != "" && n != 1 {
		out = plural
	}
	if msgid == "" || !s.golylangOn() {
		return out
	}
	r := pllFrom(ctx)
	if r == nil || r.Lang == nil {
		return out
	}
	s.pllCaptureString(ctx, msgid, context, domain, source, pllRequestPath(ctx))
	l := r.Lang
	tr := ""
	if plural == "" {
		tr = s.pllStringMap(ctx, l.Slug)[pllStringKey(msgid, context, domain)]
	}
	// The language pack catalog is used for the other languages only: the default language shows
	// what the theme printed while the plugin was off (unless the Strings table translates it).
	if tr == "" && !l.IsDefault && s.lang != nil && l.Locale != "" && l.Locale != "en_US" {
		cat := s.lang.Catalog(l.Locale)
		switch {
		case plural != "":
			tr = cat.N(msgid, plural, n)
		case context != "":
			tr = cat.X(msgid, context)
		default:
			tr = cat.T(msgid)
		}
	}
	if tr == "" {
		tr = out
	}
	if plural == "" {
		pllMarkKnown(ctx, tr, pllStringKey(msgid, context, domain)) // the page string pass does not record it again as page text
	} else {
		// A plural form has no single key to edit: known (never recorded as page text), but with an
		// empty key, which visual translation mode leaves out of its list.
		pllMarkKnown(ctx, tr, "")
	}
	return tr
}

// pllMarkKnown remembers, for this request, text that is already translated (the output of a
// gettext call), so the page string pass does not record it as a new original. key is the
// pll_strings key the text was translated from (visual translation mode edits that key, not the
// translated text); without it the text is its own key.
func pllMarkKnown(ctx context.Context, text string, key ...string) {
	if r := pllFrom(ctx); r != nil {
		if len(key) > 0 && key[0] != "" {
			r.cache.Store("known:"+pllCollapseWS(text), key[0])
			return
		}
		if len(key) > 0 { // known, not editable (a plural form)
			r.cache.LoadOrStore("known:"+pllCollapseWS(text), "")
			return
		}
		r.cache.LoadOrStore("known:"+pllCollapseWS(text), text)
	}
}

func pllKnown(r *pllReq, key string) bool {
	if r == nil {
		return false
	}
	_, ok := r.cache.Load("known:" + key)
	return ok
}

// pllCollapseWS is the key the page string pass looks text up under: whitespace runs collapsed,
// trimmed (i18n.TranslateFront also decodes entities; strings are stored as plain text).
func pllCollapseWS(v string) string {
	if !strings.ContainsAny(v, " \t\n\r\f\v") {
		return v
	}
	return strings.Join(strings.Fields(v), " ")
}

// pllRequestPath is the path of the request ctx belongs to (the page a string was seen on).
func pllRequestPath(ctx context.Context) string {
	if rc := renderFrom(ctx); rc != nil && rc.c != nil && rc.c.Request != nil {
		return rc.c.Request.URL.Path
	}
	if r := pllFrom(ctx); r != nil && r.Front {
		return r.Path
	}
	return ""
}

// pllThemeSource is the capture source of the active theme's strings.
func (s *Server) pllThemeSource(v *siteView) (source, domain string) {
	t := (*Theme)(nil)
	if v != nil {
		t = v.Theme
	}
	if t == nil {
		t = s.activeTheme()
	}
	if t == nil {
		return "core", "default"
	}
	return "theme:" + t.Slug, t.Slug
}

// pllGettextFuncs are the gettext template functions. They take the site view first ({{__ . "Skip
// to content"}}, {{_x $ "Post" "noun"}}, {{_n . "%d comment" "%d comments" .Count}}); an optional
// last argument is the text domain (the theme's slug by default). Anything but the site view as
// the first argument gets the msgid back.
func (s *Server) pllGettextFuncs() template.FuncMap {
	view := func(v any) *siteView {
		sv, _ := v.(*siteView)
		return sv
	}
	tr := func(v any, msgid, plural string, n int, context string, domain []string) string {
		sv := view(v)
		if sv == nil || sv.ctx == nil {
			if plural != "" && n != 1 {
				return plural
			}
			return msgid
		}
		source, dom := s.pllThemeSource(sv)
		if len(domain) > 0 && domain[0] != "" {
			dom = domain[0]
		}
		return s.pllGettext(sv.ctx, msgid, plural, n, context, dom, source)
	}
	t := func(v any, msgid string, domain ...string) string { return tr(v, msgid, "", 0, "", domain) }
	x := func(v any, msgid, context string, domain ...string) string {
		return tr(v, msgid, "", 0, context, domain)
	}
	return template.FuncMap{
		"__":         t,
		"_e":         t,
		"esc_html__": t,
		"esc_attr__": t,
		"esc_html_e": t,
		"esc_attr_e": t,
		"_x":         x,
		"_ex":        x,
		"esc_html_x": x,
		"esc_attr_x": x,
		"_n": func(v any, single, plural string, n any, domain ...string) string {
			return tr(v, single, plural, pllInt(n), "", domain)
		},
	}
}

func pllInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(n)
	case uint:
		return int(n)
	case string:
		var i int
		_, _ = fmt.Sscan(n, &i)
		return i
	}
	return 0
}

// Theme mods ---------------------------------------------------------------------------------

// themeModFilter, when set, filters the value of every theme_mod template call (theme_package.go).
var themeModFilter func(v *siteView, id string, val any) any

func init() {
	themeModFilter = func(v *siteView, id string, val any) any {
		str, ok := val.(string)
		if !ok || strings.TrimSpace(str) == "" || v == nil || v.ctx == nil {
			return val
		}
		rc := renderFrom(v.ctx)
		if rc == nil || rc.s == nil {
			return val
		}
		return rc.s.pllThemeMod(v, id, str)
	}
}

// pllThemeMod translates a text or textarea Customizer setting of the theme being rendered into the
// current language; the setting is listed under "Theme: <name>" with its id as the name.
func (s *Server) pllThemeMod(v *siteView, id, str string) string {
	if !s.golylangOn() || v.Theme == nil {
		return str
	}
	sp := czSpecFor(v.Theme)
	if sp == nil {
		return str
	}
	ctl := sp.control(id)
	if ctl == nil || (ctl.Type != "text" && ctl.Type != "textarea") {
		return str
	}
	l := s.pllCurrent(v.ctx)
	if l == nil {
		return str
	}
	if s.pllThemeModSaved(v, id, str) {
		s.pllCaptureString(v.ctx, str, id, pllDomainRegistered, "theme:"+v.Theme.Slug, pllRequestPath(v.ctx))
	}
	tr := s.pllTranslate(v.ctx, str, l.Slug)
	pllMarkKnown(v.ctx, tr, str)
	return tr
}

// pllThemeModSaved reports whether str is the saved value of the theme mod id, outside a Customizer
// preview: draft values typed in the Customizer (each preview refresh renders the changeset) are
// translated but never recorded.
func (s *Server) pllThemeModSaved(v *siteView, id, str string) bool {
	if rc := renderFrom(v.ctx); rc != nil && rc.c != nil && rc.c.Request != nil {
		if rc.c.Query(czParam) != "" || rc.c.Query("preview") != "" {
			return false
		}
	}
	saved, ok := s.themeMods(v.Theme.Slug)[id].(string)
	return ok && saved == str
}

// Wasm plugins -------------------------------------------------------------------------------

// Translate is the plugins' "translate" host import: the text in the request language, recorded
// under the plugin (domain = slug, source plugin:<slug>).
func (h *pluginHost) Translate(ctx context.Context, plugin, text, context string) string {
	return h.s.pllGettext(ctx, text, "", 0, context, plugin, "plugin:"+plugin)
}
