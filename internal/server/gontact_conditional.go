package server

import (
	"encoding/json"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Conditional fields for Pursae Contact Form (the idea of CF7 Conditional Fields).
//
// Form template: wrap fields in a group,
//
//	[radio topic "Sales" "Support"]
//	[group sales-extra]
//	  [text company]
//	[/group]
//
// and show it by rules in Additional Settings, one condition per line:
//
//	conditional: group | field | operator | value
//
// operators: equals, not equals, is empty, not empty, contains, greater than, less than. A group
// is shown when all its conditions hold (and every group around it is shown). The browser hides
// and disables a hidden group's fields; the server decides again from the posted values: fields of
// a hidden group are not validated, not required, and dropped from the mail and the stored entry.

var (
	gfGroupMarkRe = regexp.MustCompile(`\[group\s+([A-Za-z][-A-Za-z0-9_]*)[^\]]*\]|\[/group\]`)
	gfCondOps     = map[string]bool{"equals": true, "not equals": true, "is empty": true, "not empty": true, "contains": true, "greater than": true, "less than": true}
)

type gfCond struct{ Group, Field, Op, Value string }

// conditions parses the `conditional:` settings; malformed lines are ignored.
func (f *gfForm) conditions() []gfCond {
	var out []gfCond
	for _, line := range f.setting("conditional") {
		p := strings.Split(line, "|")
		if len(p) < 3 {
			continue
		}
		c := gfCond{Group: strings.TrimSpace(p[0]), Field: strings.TrimSpace(p[1]), Op: strings.ToLower(strings.TrimSpace(p[2]))}
		if len(p) > 3 {
			c.Value = strings.TrimSpace(strings.Join(p[3:], "|"))
		}
		if c.Group != "" && c.Field != "" && gfCondOps[c.Op] {
			out = append(out, c)
		}
	}
	return out
}

// gfGroupInfo is the group structure of a form template.
type gfGroupInfo struct {
	parent map[string]string   // group → enclosing group
	fields map[string][]string // group → names of the fields inside (nested ones included)
}

func gfGroups(form string) gfGroupInfo {
	g := gfGroupInfo{parent: map[string]string{}, fields: map[string][]string{}}
	var stack []string
	pos := 0
	addFields := func(seg string) {
		_, tags := gfScan(seg, nil)
		for _, t := range tags {
			if t.Name == "" {
				continue
			}
			for _, name := range stack {
				g.fields[name] = append(g.fields[name], t.Name)
			}
		}
	}
	for _, m := range gfGroupMarkRe.FindAllStringSubmatchIndex(form, -1) {
		addFields(form[pos:m[0]])
		pos = m[1]
		if m[2] >= 0 {
			name := form[m[2]:m[3]]
			if len(stack) > 0 {
				g.parent[name] = stack[len(stack)-1]
			}
			if _, seen := g.fields[name]; !seen {
				g.fields[name] = nil
			}
			stack = append(stack, name)
		} else if len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
	}
	addFields(form[pos:])
	return g
}

func gfCondHolds(c gfCond, vals []string) bool {
	joined := strings.TrimSpace(strings.Join(vals, ""))
	switch c.Op {
	case "is empty":
		return joined == ""
	case "not empty":
		return joined != ""
	case "equals", "not equals":
		found := false
		for _, v := range vals {
			if strings.TrimSpace(v) == c.Value {
				found = true
			}
		}
		return found == (c.Op == "equals")
	case "contains":
		for _, v := range vals {
			if strings.Contains(strings.ToLower(v), strings.ToLower(c.Value)) {
				return true
			}
		}
		return false
	case "greater than", "less than":
		a, e1 := strconv.ParseFloat(joined, 64)
		b, e2 := strconv.ParseFloat(c.Value, 64)
		if e1 != nil || e2 != nil {
			return false
		}
		return (c.Op == "greater than") == (a > b) && a != b
	}
	return false
}

// hiddenFields returns the names of the fields inside groups that are hidden for these values.
// value gives the posted values of a field.
func (f *gfForm) hiddenFields(value func(string) []string) map[string]bool {
	conds := f.conditions()
	hidden := map[string]bool{}
	if len(conds) == 0 && !strings.Contains(f.Form, "[group") {
		return hidden
	}
	info := gfGroups(f.Form)
	memo := map[string]bool{} // group → visible
	busy := map[string]bool{}
	fieldGroups := map[string][]string{}
	for g, names := range info.fields {
		for _, n := range names {
			fieldGroups[n] = append(fieldGroups[n], g)
		}
	}
	var visible func(g string) bool
	fieldVals := func(name string) []string {
		for _, g := range fieldGroups[name] {
			if !visible(g) {
				return nil // a field of a hidden group counts as empty
			}
		}
		return value(name)
	}
	visible = func(g string) bool {
		if v, ok := memo[g]; ok {
			return v
		}
		if busy[g] { // conditions that depend on each other: hidden
			return false
		}
		busy[g] = true
		defer delete(busy, g)
		ok := true
		if p := info.parent[g]; p != "" {
			ok = visible(p)
		}
		for _, c := range conds {
			if ok && c.Group == g && !gfCondHolds(c, fieldVals(c.Field)) {
				ok = false
			}
		}
		memo[g] = ok
		return ok
	}
	for g, names := range info.fields {
		if !visible(g) {
			for _, n := range names {
				hidden[n] = true
			}
		}
	}
	return hidden
}

// applyConditions drops the posted values of hidden groups' fields (before validation and mail).
func (sub *gfSubmission) applyConditions() {
	sub.hidden = sub.form.hiddenFields(func(n string) []string { return sub.raw[n] })
	for n := range sub.hidden {
		delete(sub.raw, n)
		delete(sub.posted, n)
	}
}

// gfGroupOpen / gfGroupClose are the wrapper elements of [group]: shown by default, the script
// hides them when their rules say so.
func gfGroupOpen(name string) string {
	return `<div class="wpcf7cf-group" data-wpcf7cf-group="` + html.EscapeString(name) + `">`
}

// conditionsJSON is the rule list for the browser script (nil when the form has none).
func (f *gfForm) conditionsJSON() string {
	conds := f.conditions()
	if len(conds) == 0 {
		return ""
	}
	type rule struct {
		Group string `json:"group"`
		Field string `json:"field"`
		Op    string `json:"op"`
		Value string `json:"value"`
	}
	var rules []rule
	for _, c := range conds {
		rules = append(rules, rule{c.Group, c.Field, c.Op, c.Value})
	}
	b, _ := json.Marshal(rules)
	return strings.ReplaceAll(string(b), "<", `<`)
}
