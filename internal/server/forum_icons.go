package server

import (
	"html/template"
	"sort"
	"strconv"
)

// Pursae Forum's built-in icon set: simple stroke icons drawn on a 24x24 grid with currentColor, so
// a badge or level takes its colour from CSS (--pf-chip). The paths are written for Pursae (no icon
// font, no CDN, no third-party set); badges and levels store the icon's name.

// forumIconPaths maps an icon name to the SVG body drawn inside the 24x24 viewBox.
var forumIconPaths = map[string]string{
	"anchor":    `<circle cx="12" cy="5" r="2"/><path d="M12 7v14M8 11h8M4 13a8 8 0 0 0 16 0"/>`,
	"bolt":      `<path d="M13 3 5 13h6l-1 8 8-10h-6z"/>`,
	"book":      `<path d="M5 4h12a2 2 0 0 1 2 2v14H7a2 2 0 0 1-2-2z"/><path d="M5 18a2 2 0 0 1 2-2h12M9 8h6"/>`,
	"calendar":  `<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 10h18M8 3v4M16 3v4"/>`,
	"camera":    `<path d="M3 8h4l2-3h6l2 3h4v11H3z"/><circle cx="12" cy="13" r="3.5"/>`,
	"chat":      `<path d="M4 5h16v11H9l-5 4z"/><path d="M8 9h8M8 12h5"/>`,
	"check":     `<path d="M4 12.5l5 5L20 6.5"/>`,
	"code":      `<path d="M8 7l-5 5 5 5M16 7l5 5-5 5M14 4l-4 16"/>`,
	"coffee":    `<path d="M4 9h13v5a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5z"/><path d="M17 10h1.5a2.5 2.5 0 0 1 0 5H17M8 3v3M12 3v3"/>`,
	"compass":   `<circle cx="12" cy="12" r="9"/><path d="M15.5 8.5l-2 5-5 2 2-5z"/>`,
	"crown":     `<path d="M3 8l4.5 4L12 5l4.5 7L21 8l-2 11H5z"/>`,
	"feather":   `<path d="M19 4C11 4 7 9 7 16v4"/><path d="M7 16h6c3.5-1.5 5.5-5.5 6-12"/><path d="M7 12h6"/>`,
	"flag":      `<path d="M5 21V4M5 4h12l-2 4 2 4H5"/>`,
	"flame":     `<path d="M12 21c-3.9 0-7-2.9-7-6.6 0-3.3 2.4-5.4 4-7.4.3 2 1.3 3.2 2.5 3.6C11 7 12.5 4.6 14.5 3c.4 3.4 4.5 5.6 4.5 11.4 0 3.7-3.1 6.6-7 6.6z"/>`,
	"gamepad":   `<rect x="2" y="7" width="20" height="11" rx="5"/><path d="M7 10v4M5 12h4"/><circle cx="16" cy="11" r="1"/><circle cx="18" cy="14" r="1"/>`,
	"gem":       `<path d="M6 4h12l3 5-9 11L3 9z"/><path d="M3 9h18M9 4l3 16 3-16"/>`,
	"gift":      `<rect x="3" y="8" width="18" height="4"/><path d="M5 12v9h14v-9M12 8v13M12 8c-1-3-5-4-5-1.5S10 8 12 8zM12 8c1-3 5-4 5-1.5S14 8 12 8z"/>`,
	"globe":     `<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.5 3.8 5.5 3.8 9s-1.3 6.5-3.8 9c-2.5-2.5-3.8-5.5-3.8-9S9.5 5.5 12 3z"/>`,
	"heart":     `<path d="M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.6-7 10-7 10z"/>`,
	"key":       `<circle cx="8" cy="15" r="4"/><path d="M11 12l9-9M17 6l3 3M14 9l2 2"/>`,
	"leaf":      `<path d="M5 19C5 10 10 5 20 4c-1 10-6 15-15 15z"/><path d="M5 19l8-8"/>`,
	"lightbulb": `<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3z"/>`,
	"lock":      `<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>`,
	"mail":      `<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>`,
	"medal":     `<circle cx="12" cy="15" r="5"/><path d="M8.5 11.5 6 3h4l2 5 2-5h4l-2.5 8.5"/>`,
	"moon":      `<path d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z"/>`,
	"mountain":  `<path d="M2 20l7-12 4 6 3-4 6 10z"/>`,
	"music":     `<path d="M9 18V5l11-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="17" cy="16" r="3"/>`,
	"paw":       `<circle cx="7" cy="10" r="1.8"/><circle cx="10.5" cy="6" r="1.8"/><circle cx="13.5" cy="6" r="1.8"/><circle cx="17" cy="10" r="1.8"/><path d="M12 12c-3 0-5 3.5-5 5.5S9 20 12 19c3 1 5 0 5-1.5S15 12 12 12z"/>`,
	"pencil":    `<path d="M4 20l1-4L16 5l3 3L8 19z"/><path d="M14 7l3 3"/>`,
	"pin":       `<path d="M12 21s-6-5.5-6-11a6 6 0 0 1 12 0c0 5.5-6 11-6 11z"/><circle cx="12" cy="10" r="2"/>`,
	"puzzle":    `<path d="M4 8h4a2 2 0 1 1 4 0h4v4a2 2 0 1 1 0 4v4H4v-4a2 2 0 1 0 0-4z"/>`,
	"rocket":    `<path d="M12 3c3 2 5 5.5 5 9.5L15 16H9l-2-3.5C7 8.5 9 5 12 3z"/><circle cx="12" cy="10" r="1.5"/><path d="M9 16l-2 4 3-1M15 16l2 4-3-1"/>`,
	"seedling":  `<path d="M12 21v-9"/><path d="M12 12c0-4-3-6-7-6 0 4 3 6 7 6zM12 14c0-4 3-7 7-7 0 4-3 7-7 7z"/>`,
	"shield":    `<path d="M12 3l7 3v5c0 4.5-3 8.3-7 10-4-1.7-7-5.5-7-10V6z"/>`,
	"sparkles":  `<path d="M10 3l1.6 4.4L16 9l-4.4 1.6L10 15l-1.6-4.4L4 9l4.4-1.6z"/><path d="M18 14l.8 2.2L21 17l-2.2.8L18 20l-.8-2.2L15 17l2.2-.8z"/>`,
	"star":      `<path d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9z"/>`,
	"sun":       `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>`,
	"target":    `<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="5"/><circle cx="12" cy="12" r="1"/>`,
	"trophy":    `<path d="M8 4h8v5a4 4 0 0 1-8 0z"/><path d="M8 6H5a3 3 0 0 0 3 4M16 6h3a3 3 0 0 1-3 4"/><path d="M12 13v4M8 20h8M10 17h4"/>`,
	"user":      `<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>`,
	"users":     `<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 4.5a3.5 3.5 0 0 1 0 7M18 14a6 6 0 0 1 3.5 6"/>`,
	"verified":  `<path d="M12 2.5l2.4 1.8 3-.2.9 2.8 2.4 1.8-1 2.8 1 2.8-2.4 1.8-.9 2.8-3-.2L12 21.5l-2.4-1.8-3 .2-.9-2.8-2.4-1.8 1-2.8-1-2.8 2.4-1.8.9-2.8 3 .2z"/><path d="M8.5 12l2.5 2.5 4.5-5"/>`,
}

// forumIconNames are the icon names in alphabetical order (the admin icon picker).
func forumIconNames() []string {
	out := make([]string, 0, len(forumIconPaths))
	for k := range forumIconPaths {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// forumIconValid reports whether name is an icon of the set.
func forumIconValid(name string) bool { _, ok := forumIconPaths[name]; return ok }

// forumIcon is the inline SVG of icon name at size px (16 when size <= 0); "" for an unknown name.
// Decorative: aria-hidden, the badge or level name next to it is the text.
func forumIcon(name string, size int) template.HTML {
	body, ok := forumIconPaths[name]
	if !ok {
		return ""
	}
	if size <= 0 || size > 512 {
		size = 16
	}
	n := strconv.Itoa(size)
	return template.HTML(`<svg class="pf-icon pf-icon--` + name + `" width="` + n + `" height="` + n +
		`" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">` +
		body + `</svg>`)
}
