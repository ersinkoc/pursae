package server

import "wpgo/internal/webbuilder"

// Plugins screen links show a few built-in plugins under their visible name; the stored slug
// (active plugins, settings, option keys) keeps the old one.

// pluginURLSlugs maps a stored slug to the slug its Plugins screen links show.
var pluginURLSlugs = map[string]string{webbuilder.PluginSlug: "web-builder"}

// pluginURLSlug is a plugin's slug as Plugins screen links show it.
func pluginURLSlug(slug string) string {
	if u, ok := pluginURLSlugs[slug]; ok {
		return u
	}
	return slug
}

// pluginSlugFromURL maps a slug from a Plugins screen link back to the stored one.
func pluginSlugFromURL(slug string) string {
	for stored, u := range pluginURLSlugs {
		if slug == u {
			return stored
		}
	}
	return slug
}
