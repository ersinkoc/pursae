package server

import (
	"context"
	"net/http"
	"strings"
)

// maintGroup is the Strings screen group of the maintenance page's texts (Pursae Multi Language).
const maintGroup = "Pursae Maintenance"

// maintTexts lists the page's translatable settings: label (Strings screen) and the field.
func maintTexts(st *maintSettings) []struct {
	Name string
	P    *string
} {
	return []struct {
		Name string
		P    *string
	}{
		{"Maintenance: page title", &st.Title}, {"Maintenance: headline", &st.Heading}, {"Maintenance: text", &st.Content},
		{"Maintenance: footer", &st.FooterText}, {"Maintenance: meta description", &st.MetaDescription},
		{"Maintenance: countdown label", &st.CountdownLabel}, {"Maintenance: progress label", &st.ProgressLabel},
		{"Maintenance: subscribe title", &st.SubscribeTitle}, {"Maintenance: subscribe button", &st.SubscribeButton},
		{"Maintenance: subscribe thanks", &st.SubscribeThanks}, {"Maintenance: consent text", &st.GDPRText},
		{"Maintenance: password button", &st.PasswordButtonT}, {"Maintenance: wrong password", &st.PasswordWrong},
		{"Maintenance: login tooltip", &st.LoginTooltip},
	}
}

// maintRegisterStrings records the page's texts as translatable strings of Pursae Multi Language
// (a no-op while that plugin is off), so they show up on its Strings screen.
func (s *Server) maintRegisterStrings(ctx context.Context, st maintSettings) {
	if !s.golylangOn() {
		return
	}
	for _, t := range maintTexts(&st) {
		s.pllRegisterString(ctx, t.Name, *t.P, maintGroup, strings.Contains(*t.P, "\n"))
	}
}

// maintLang picks the language of a visitor of the maintenance page: the language code in the URL
// (/fr/...), ?lang=, the language cookie, then Accept-Language, then the default language. nil when
// Pursae Multi Language is off or has no language.
func (s *Server) maintLang(r *http.Request) *pllLanguage {
	if !s.golylangOn() {
		return nil
	}
	ctx := r.Context()
	var langs []*pllLanguage
	for _, l := range s.pllLanguages(ctx) {
		if l.Active {
			langs = append(langs, l)
		}
	}
	if len(langs) == 0 {
		return nil
	}
	if seg, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/"); pllFind(langs, seg) != nil {
		return pllFind(langs, seg)
	}
	if l := pllFind(langs, r.URL.Query().Get("lang")); l != nil {
		return l
	}
	if ck, err := r.Cookie(pllCookie); err == nil && pllFind(langs, ck.Value) != nil {
		return pllFind(langs, ck.Value)
	}
	if l := pllBrowserLang(r.Header.Get("Accept-Language"), langs); l != nil {
		return l
	}
	return s.pllDefault(ctx)
}

// maintTranslate replaces the page's texts with their translations in the visitor's language (the
// text itself where there is none) and returns the language for <html lang>.
func (s *Server) maintTranslate(r *http.Request, st *maintSettings) *pllLanguage {
	l := s.maintLang(r)
	if l == nil {
		return nil
	}
	ctx := r.Context()
	s.maintRegisterStrings(ctx, *st)
	for _, t := range maintTexts(st) {
		*t.P = s.pllTranslate(ctx, *t.P, l.Slug)
	}
	return l
}
