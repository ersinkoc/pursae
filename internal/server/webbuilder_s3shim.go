package server

import "wpgo/internal/webbuilder"

// Integration shim (merge 3): S5–S7 call wbOn and wbEnabledType, which S3 defines in
// webbuilder_plugin.go. S3 is not merged into web-builder yet; these are verbatim copies of its
// definitions. DELETE THIS FILE when wb/s3 is merged (the build then reports duplicate symbols).

// wbOn reports whether Pursae Web Builder is active (option native_golementor).
func (s *Server) wbOn() bool {
	return s.settings.Bool(webbuilder.PluginOption)
}

// wbPostTypes are the post types the builder edits: pages and posts.
var wbPostTypes = []string{"page", "post"}

// wbEnabledType reports whether posts of the type open in the builder.
func wbEnabledType(typ string) bool {
	for _, t := range wbPostTypes {
		if t == typ {
			return true
		}
	}
	return false
}
