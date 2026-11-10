package server

import (
	"log/slog"
	"strings"

	"wpgo/internal/store"
)

// Translator capabilities. manage_options keeps full control; manage_translations lets a user work
// on the strings, visual translation and machine translation of every language, and
// translate_{lang} (for example translate_de) of one language only. Grant them per user (allow
// exceptions) or through a role; they never open the Languages or Settings screens.
const (
	pllCapManageTranslations = "manage_translations"
	pllCapTranslatePrefix    = "translate_"
)

// pllCanTranslate reports whether u may translate into the language slug.
func pllCanTranslate(u *store.User, slug string) bool {
	if u == nil {
		return false
	}
	return u.Can("manage_options", nil) || u.Can(pllCapManageTranslations, nil) ||
		(slug != "" && u.Can(pllCapTranslatePrefix+slug, nil))
}

// pllCanTranslateAny reports whether u may translate into at least one language.
func pllCanTranslateAny(u *store.User) bool {
	if u == nil {
		return false
	}
	if u.Can("manage_options", nil) || u.Can(pllCapManageTranslations, nil) {
		return true
	}
	for c, ok := range u.Caps {
		if ok && strings.HasPrefix(c, pllCapTranslatePrefix) && len(c) > len(pllCapTranslatePrefix) {
			return true
		}
	}
	return false
}

// pllTranslatableLangs filters langs down to the ones u may translate into.
func pllTranslatableLangs(u *store.User, langs []*pllLanguage) []*pllLanguage {
	var out []*pllLanguage
	for _, l := range langs {
		if pllCanTranslate(u, l.Slug) {
			out = append(out, l)
		}
	}
	return out
}

// pllLog logs a failed Languages write and returns the error, so callers can report the first one
// instead of showing a success notice.
func pllLog(op string, err error) error {
	if err != nil {
		slog.Warn("golylang: "+op+" failed", "err", err)
	}
	return err
}
