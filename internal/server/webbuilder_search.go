package server

import (
	"context"
	"html"
	"regexp"
	"strings"
)

// The Web Builder search form (form[data-wb-search], contract §11.5) is published with the home URL
// of the editor's request as its action (SanitizeSnapshot). A stored snapshot serves every
// language, so at request time the action becomes the home of the request language and, in query
// mode, the language moves into a hidden input: the same searchFormAction the core search forms use
// (Pursae Multi Language).

var (
	wbFormTagRE  = regexp.MustCompile(`<form\b[^>]*>`)
	wbTagAttrsRE = regexp.MustCompile(`\s+([^\s"'>/=]+)(?:="([^"]*)")?`)
)

// wbSearchForms rewrites the action of every search form of a filled snapshot to the home of the
// request language. html.Render (the sanitizer's serializer) always writes double-quoted values and
// escapes '"', '<' and '>', so the tag and its attributes are found without a parse.
func (s *Server) wbSearchForms(ctx context.Context, h string) string {
	if !strings.Contains(h, `data-wb-search`) {
		return h
	}
	return wbRewriteSearchForms(h, func() (string, string) {
		a, hid := searchFormAction(s.links(ctx))
		return a, string(hid)
	})
}

// wbRewriteSearchForms sets the action (and the hidden inputs) of every form[data-wb-search];
// target is asked once, on the first search form.
func wbRewriteSearchForms(h string, target func() (action, hidden string)) string {
	var action, hidden string
	resolved := false
	return wbFormTagRE.ReplaceAllStringFunc(h, func(tag string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(tag, "<form"), ">")
		attrs := wbTagAttrsRE.FindAllStringSubmatch(inner, -1)
		search := false
		for _, a := range attrs {
			if strings.EqualFold(a[1], "data-wb-search") {
				search = true
				break
			}
		}
		if !search {
			return tag
		}
		if !resolved {
			action, hidden = target()
			resolved = true
		}
		var b strings.Builder
		b.WriteString("<form")
		for _, a := range attrs {
			if strings.EqualFold(a[1], "action") {
				continue
			}
			b.WriteString(a[0])
		}
		b.WriteString(` action="` + html.EscapeString(action) + `">`)
		b.WriteString(hidden)
		return b.String()
	})
}
