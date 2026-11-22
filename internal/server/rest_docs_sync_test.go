package server

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// restDocPath converts a registered route pattern to the notation of docs/developer/rest-api/endpoints.md:
// the wp/v2 prefix is dropped and every named group `(?P<id>…)` becomes `{id}`.
func restDocPath(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); {
		if strings.HasPrefix(pattern[i:], "(?P<") {
			end := strings.IndexByte(pattern[i:], '>')
			b.WriteString("{" + pattern[i+4:i+end] + "}")
			depth := 0
			for j := i; j < len(pattern); j++ {
				switch pattern[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth == 0 {
					i = j + 1
					break
				}
			}
			continue
		}
		b.WriteByte(pattern[i])
		i++
	}
	return strings.TrimPrefix(b.String(), "/wp/v2")
}

// TestRESTEndpointsDocumented keeps docs/developer/rest-api/endpoints.md in step with the core routes
// registered in restRoutes(): every route needs a table row naming its path, and that row must list each
// registered GET, POST and DELETE (PUT, PATCH and HEAD are covered by the page's intro).
func TestRESTEndpointsDocumented(t *testing.T) {
	raw, err := os.ReadFile("../../docs/developer/rest-api/endpoints.md")
	if err != nil {
		t.Fatal(err)
	}
	docMethods := map[string]string{} // documented path -> methods cell
	tick := regexp.MustCompile("`([^`]+)`")
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		for _, m := range tick.FindAllStringSubmatch(cells[1], -1) {
			docMethods[m[1]] += " " + cells[2]
		}
	}

	api := (&Server{}).restRoutes()
	var problems []string
	for _, r := range api.routes {
		path := restDocPath(r.Pattern)
		cell, ok := docMethods[path]
		if !ok {
			problems = append(problems, "missing from endpoints.md: "+path)
			continue
		}
		for _, m := range []string{"GET", "POST", "DELETE"} {
			if _, registered := r.Methods[m]; registered && !strings.Contains(cell, m) {
				problems = append(problems, "endpoints.md row of "+path+" does not list "+m)
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}
