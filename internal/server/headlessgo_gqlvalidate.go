package server

import (
	"fmt"
	"sort"
	"strings"
)

// Query validation: before anything runs, the selection is checked against the generated schema, so
// a field the type does not have, a leaf with a sub-selection, an object without one, or an unknown
// argument on a root field is a GraphQL validation error (HTTP 400, extensions.code
// GRAPHQL_VALIDATION_FAILED, no data) instead of a null in the answer. Fragments and directives
// are already expanded when this runs. Introspection (__schema, __type) is answered by its own
// code and is not walked; JSON scalars may carry a sub-selection (it picks keys of the value).

const hgGQLValidationCode = "GRAPHQL_VALIDATION_FAILED"

// hgGQLValidationErrors turns messages into the errors array of a validation failure.
func hgGQLValidationErrors(msgs []string) []any {
	out := make([]any, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]any{"message": m, "extensions": map[string]any{"code": hgGQLValidationCode}}
	}
	return out
}

// validate checks the root selection of an operation of the given kind (query, mutation,
// subscription) and returns every problem found.
func (ex *hgGQLExec) validate(sel []*hgGQLField, kind string) []string {
	g := ex.schema()
	root := map[string]string{"query": g.query, "mutation": g.mutation, "subscription": g.subscription}[kind]
	if root == "" || g.types[root] == nil {
		return []string{fmt.Sprintf("This schema has no %s type.", kind)}
	}
	var errs []string
	g.validateSel(root, sel, true, &errs)
	return errs
}

func hgGQLBase(r *hgGQLRef) string {
	for r.wrap != "" {
		r = r.of
	}
	return r.name
}

func (g *hgGQLSchema) validateSel(parent string, sel []*hgGQLField, root bool, errs *[]string) {
	t := g.types[parent]
	if t == nil {
		return
	}
	for _, f := range sel {
		if f.name == "..." {
			on := parent
			if f.on != "" {
				on = f.on
				if g.types[on] == nil {
					*errs = append(*errs, fmt.Sprintf("Unknown type %q.", on))
					continue
				}
			}
			g.validateSel(on, f.sel, root, errs)
			continue
		}
		if f.name == "__typename" || (root && (f.name == "__schema" || f.name == "__type")) {
			continue
		}
		var fd *hgGQLFieldDef
		if t.kind == "OBJECT" {
			for _, x := range t.fields {
				if x.name == f.name {
					fd = x
					break
				}
			}
		}
		if fd == nil {
			msg := fmt.Sprintf("Cannot query field %q on type %q.", f.name, parent)
			if t.kind == "OBJECT" {
				if s := hgGQLSuggest(f.name, t.fields); s != "" {
					msg += fmt.Sprintf(" Did you mean %q?", s)
				}
			} else if t.kind == "UNION" {
				msg += " Use an inline fragment on one of the union's types to select its fields."
			}
			*errs = append(*errs, msg)
			continue
		}
		if root {
			known := map[string]bool{}
			for _, a := range fd.args {
				known[a.name] = true
			}
			var unknown []string
			for k := range f.args {
				if !known[k] {
					unknown = append(unknown, k)
				}
			}
			sort.Strings(unknown)
			for _, k := range unknown {
				*errs = append(*errs, fmt.Sprintf("Unknown argument %q on field \"%s.%s\".", k, parent, f.name))
			}
		}
		base := hgGQLBase(fd.typ)
		bt := g.types[base]
		composite := bt != nil && (bt.kind == "OBJECT" || bt.kind == "UNION")
		switch {
		case composite && len(f.sel) == 0:
			*errs = append(*errs, fmt.Sprintf("Field %q of type %q must have a selection of subfields.", f.name, hgGQLRefString(fd.typ)))
		case !composite && len(f.sel) > 0 && base != "JSON":
			*errs = append(*errs, fmt.Sprintf("Field %q must not have a selection since type %q has no subfields.", f.name, hgGQLRefString(fd.typ)))
		case composite:
			g.validateSel(base, f.sel, false, errs)
		}
	}
}

// hgGQLSuggest names the field closest to a misspelled one (one or two edits away).
func hgGQLSuggest(name string, fields []*hgGQLFieldDef) string {
	best, bestD := "", 3
	for _, f := range fields {
		if d := hgEditDistance(strings.ToLower(name), strings.ToLower(f.name)); d < bestD {
			best, bestD = f.name, d
		}
	}
	return best
}

func hgEditDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
