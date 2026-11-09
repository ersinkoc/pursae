package server

import (
	"context"
	"strings"
)

// feedLanguage is bloginfo_rss('language') of a feed: the current language's tag on a multilingual
// site (/tr/feed/ is tr-TR, as Polylang filters the locale), else the site locale.
func (s *Server) feedLanguage(ctx context.Context) string {
	if r := pllFrom(ctx); r != nil && r.Front && r.Lang != nil && s.golylangOn() {
		return r.Lang.W3C()
	}
	return strings.ReplaceAll(s.siteLocale(), "_", "-")
}

// feedBlogInfo is get_bloginfo_rss('name' / 'description'): the site title or tagline, translated into
// the current language by Pursae Multi Language's string translations.
func (s *Server) feedBlogInfo(ctx context.Context, key string) string {
	v := s.settings.String(key)
	if r := pllFrom(ctx); r != nil && r.Front && r.Lang != nil && s.golylangOn() {
		return s.pllTranslate(ctx, v, r.Lang.Slug)
	}
	return v
}
