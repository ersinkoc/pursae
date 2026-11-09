package server

import (
	"html"
	"html/template"
	"net/url"
	"sort"
	"strings"

	"wpgo/internal/links"
)

// searchFormAction is the action of a front-end search form and the hidden inputs it needs. The
// action is the home of the current language (lb.Front() runs the language link filter, as Polylang
// filters get_search_form), so a search started on /fr/ stays French. In query mode the language home
// is "/?lang=fr"; a GET form replaces the action's query string with its own fields, so the query
// moves into hidden inputs (Polylang adds a hidden "lang" input the same way).
func searchFormAction(lb *links.Builder) (string, template.HTML) {
	u := lb.Front()
	base, raw, ok := strings.Cut(u, "?")
	if !ok {
		return u, ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil || len(vals) == 0 {
		return base, ""
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		for _, v := range vals[k] {
			sb.WriteString(`<input type="hidden" name="` + html.EscapeString(k) + `" value="` + html.EscapeString(v) + `" />`)
		}
	}
	return base, template.HTML(sb.String())
}
