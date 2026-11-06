package server

import (
	"html"
	"slices"
)

// Small string helpers shared by the admin screens.

// esc escapes s for HTML text and attribute values (esc_html / esc_attr).
func esc(s string) string { return html.EscapeString(s) }

// containsStr reports whether list holds s.
func containsStr(list []string, s string) bool { return slices.Contains(list, s) }
