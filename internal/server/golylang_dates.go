package server

import (
	"strings"

	"wpgo/internal/wp"
)

// Per-language date and time formats. Date Format and Time Format are registered as strings on
// Languages > Translations: a translation (for example "j F Y" for German) reformats every date
// the site printed with the Settings > General format, with the language's month and weekday names.

// pllDateString is the Translations screen row of a date or time format setting.
func (s *Server) pllDateStrings() []pllRegistered {
	var out []pllRegistered
	for _, o := range []struct{ name, opt string }{{"Date Format", "date_format"}, {"Time Format", "time_format"}} {
		if f := s.settings.String(o.opt); f != "" {
			out = append(out, pllRegistered{Name: o.name, String: f, Group: "WordPress"})
		}
	}
	return out
}

// pllReformatDate rewrites text when it is a date or a time in the site formats and the language
// translates that format; ok is false for any other text.
func (s *Server) pllReformatDate(t *pllFrontTable, text string, l *pllLanguage) (string, bool) {
	if t == nil || (t.dateDst == "" && t.timeDst == "") || len(text) > 48 {
		return "", false
	}
	var names *wp.DateNames
	if s.lang != nil {
		if cat := s.lang.Catalog(l.Locale); !cat.Empty() {
			names = cat.DateNames()
		}
	}
	for _, f := range [][2]string{{t.dateSrc, t.dateDst}, {t.timeSrc, t.timeDst}} {
		if f[1] == "" || strings.TrimSpace(text) == "" {
			continue
		}
		if d, ok := wp.ParseDate(text, f[0]); ok {
			return wp.FormatDateNames(d, f[1], names), true
		}
	}
	return "", false
}
