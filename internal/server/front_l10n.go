package server

import (
	"context"
	"html"
	"strings"

	"wpgo/internal/i18n"
)

// Core's own front-end strings (post navigation, the comment form): WordPress prints them with
// __() in the "default" text domain, so they follow get_locale() — or the current language with
// Polylang. With Pursae Multi Language on and a request language, frontT goes through pllGettext:
// the Strings translations table first, then the language packs, and the string is recorded under
// the core source (Languages › Translations, group "Pursae") so it can be translated there when no
// pack has it. Without a request language it uses the site language's catalog.

const frontDomain = "default" // WordPress core's text domain

// frontT is __( msgid ) for a core front-end string, as plain text (not escaped).
func (s *Server) frontT(ctx context.Context, msgid string) string {
	if s.golylangOn() {
		if r := pllFrom(ctx); r != nil && r.Lang != nil {
			return s.pllGettext(ctx, msgid, "", 0, "", frontDomain, "core")
		}
	}
	if s.lang == nil {
		return msgid
	}
	return s.lang.Catalog(s.siteLocale()).T(msgid)
}

// frontE is esc_html__( msgid ).
func (s *Server) frontE(ctx context.Context, msgid string) string {
	return html.EscapeString(s.frontT(ctx, msgid))
}

// frontEf is sprintf( esc_html__( format ), args… ): the translated format is escaped, args are
// HTML already (escaped by the caller).
func (s *Server) frontEf(ctx context.Context, format string, args ...string) string {
	out := i18n.Sprintf(html.EscapeString(s.frontT(ctx, format)), args...)
	if !strings.Contains(out, "<") {
		// The whole sentence is a translated string: the page string pass does not record it as
		// page text (it holds a user's name).
		pllMarkKnown(ctx, html.UnescapeString(out), format)
	}
	return out
}
