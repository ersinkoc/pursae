package blocks

import (
	"regexp"
	"strings"
)

var (
	firstTagRe = regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9-]*)(\s[^>]*)?>`)
	classAttr  = regexp.MustCompile(`\sclass="([^"]*)"`)
)

// AddClass adds classes to the first element of html, the way WP_HTML_Tag_Processor::add_class() is used by render callbacks.
// When tags is non-empty only the first element with one of those tag names is changed.
func AddClass(html, classes string, tags ...string) string {
	locs := firstTagRe.FindAllStringSubmatchIndex(html, -1)
	for _, loc := range locs {
		name := strings.ToLower(html[loc[2]:loc[3]])
		if len(tags) > 0 && !contains(tags, name) {
			continue
		}
		tag := html[loc[0]:loc[1]]
		var updated string
		if m := classAttr.FindStringSubmatchIndex(tag); m != nil {
			existing := strings.Fields(tag[m[2]:m[3]])
			for _, c := range strings.Fields(classes) {
				if !contains(existing, c) {
					existing = append(existing, c)
				}
			}
			updated = tag[:m[2]] + strings.Join(existing, " ") + tag[m[3]:]
		} else {
			end := len(tag) - 1
			if strings.HasSuffix(tag, "/>") {
				end--
			}
			updated = tag[:loc[3]-loc[0]] + ` class="` + classes + `"` + tag[loc[3]-loc[0]:]
			_ = end
		}
		return html[:loc[0]] + updated + html[loc[1]:]
	}
	return html
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
