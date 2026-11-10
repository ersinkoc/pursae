package server

import "testing"

// TestMCPToolRoutesAreRegistered: every tool's method and path must match a registered REST route, so a
// route that is renamed or removed breaks a test instead of only the tool at run time. No database.
func TestMCPToolRoutesAreRegistered(t *testing.T) {
	s := &Server{}
	s.routes()
	if s.rest == nil || len(s.rest.routes) == 0 {
		t.Fatal("no REST routes registered")
	}
	for _, tool := range mcpTools {
		if tool.special == "rest" || tool.special == "info" {
			continue // chosen at call time / served by the index
		}
		route, method := tool.Path, tool.Method
		if tool.special == "upload" {
			route, method = "/wp/v2/media", "POST"
		}
		found := false
		// Placeholders become a number, a slug and a hex-like word, so numeric and word patterns both match.
		for _, v := range []string{"1", "categories", "abc123"} {
			concrete := mcpPathArg.ReplaceAllStringFunc(route, func(m string) string {
				if m == "{id}" {
					return "1"
				}
				return v
			})
			for _, r := range s.rest.routes {
				if _, ok := r.Methods[method]; ok && r.re.MatchString(concrete) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("tool %s: no REST route for %s %s", tool.Name, method, route)
		}
	}
}
