package webbuilder

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Kit overrides (docs/developer/web-builder-code-sections.md, "Classes and variables"; contract
// §8.1 "Kit overrides").
//
// The element editor edits a design's classes and variables without writing the kit's files: each
// edit is a declaration in the kit's override stylesheet, a list of rules with the design rule's
// own selector inside the design rule's own at-rule context (@layer, @media, @supports,
// @container), printed after the design's stylesheets on every page that uses the kit:
//
//	{"rules": [
//	  {"ctx": ["@layer utilities"], "sel": ".rounded-2xl", "p": {"border-radius": "2rem"}},
//	  {"ctx": ["@layer theme"], "sel": ":root, :host", "p": {"--color-brand-600": "#c2410c"}}]}
//
//	@layer utilities{.rounded-2xl{border-radius:2rem}}
//	@layer theme{:root, :host{--color-brand-600:#c2410c}}
//
// Same selector, same layer: the override has the design rule's specificity and layer and comes
// later, so it wins exactly where the design rule did. Rules are printed in list order, one per
// line, declarations sorted by property name. KitOverrideCSS is mirrored byte for byte by
// builder/host/elements/overrides.ts (shared fixtures in testdata/override).

// KitOverride is a kit's override stylesheet as the editor edits it.
type KitOverride struct {
	Rules []OverrideRule `json:"rules"`
}

// OverrideRule is one rule of a kit override: at-rule preludes (outermost first), the selector and
// its declarations (raw CSS values, as local styles take them).
type OverrideRule struct {
	Ctx []string          `json:"ctx,omitempty"`
	Sel string            `json:"sel"`
	P   map[string]string `json:"p"`
}

// Limits of a kit override.
const (
	MaxOverrideRules = 2000
	MaxOverrideCtx   = 6
	MaxOverrideSel   = 1024
	MaxOverrideBytes = 256 << 10 // the whole override, as JSON
)

var overrideCtxRE = regexp.MustCompile(`^@(?:media|layer|supports|container)(?:[ \t][^{};<>\\]{0,300})?$`)

// ErrBadOverride is returned by ParseKitOverride for an override that is not well formed; the
// message names the first problem.
var ErrBadOverride = errors.New("webbuilder: bad kit override")

// overrideSelOK checks a selector of an override rule: no block or statement characters, no
// markup, no URLs or forbidden tokens, balanced.
func overrideSelOK(sel string) bool {
	if sel == "" || len(sel) > MaxOverrideSel || strings.ContainsAny(sel, "{};<>") || strings.Contains(sel, "/*") || strings.HasPrefix(sel, "@") {
		return false
	}
	for i := 0; i < len(sel); i++ {
		if sel[i] < 0x20 && sel[i] != '\t' || sel[i] == 0x7f {
			return false
		}
	}
	return (&cssCleaner{}).preludeOK(sel)
}

func overrideCtxOK(ctx string) bool {
	return overrideCtxRE.MatchString(ctx) && !strings.Contains(ctx, "/*") && (&cssCleaner{}).preludeOK(ctx)
}

// ParseKitOverride decodes and validates an override (null or {} is an empty one). Every rule
// must be well formed and every declaration LocalDeclOK; the stylesheet it prints must pass the CSS
// sanitizer unchanged.
func ParseKitOverride(raw json.RawMessage) (*KitOverride, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return &KitOverride{}, nil
	}
	if len(raw) > MaxOverrideBytes {
		return nil, ErrTooLargeOverride
	}
	var o KitOverride
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, badOverride("the override must be {rules: [{ctx, sel, p}]}")
	}
	if len(o.Rules) > MaxOverrideRules {
		return nil, badOverride("at most " + strconv.Itoa(MaxOverrideRules) + " rules")
	}
	seen := map[string]bool{}
	for i, r := range o.Rules {
		at := "rules[" + strconv.Itoa(i) + "]"
		if len(r.Ctx) > MaxOverrideCtx {
			return nil, badOverride(at + ".ctx: at most " + strconv.Itoa(MaxOverrideCtx) + " at-rules")
		}
		for _, c := range r.Ctx {
			if !overrideCtxOK(c) {
				return nil, badOverride(at + ".ctx: not an @media, @layer, @supports or @container prelude: " + truncate(c, 80))
			}
		}
		if !overrideSelOK(r.Sel) {
			return nil, badOverride(at + ".sel: not a selector the override takes: " + truncate(r.Sel, 80))
		}
		key := overrideKey(r)
		if seen[key] {
			return nil, badOverride(at + ": the same selector and context twice")
		}
		seen[key] = true
		if len(r.P) > MaxLocalProps {
			return nil, badOverride(at + ".p: at most " + strconv.Itoa(MaxLocalProps) + " properties")
		}
		for k, v := range r.P {
			if !LocalDeclOK(k, v) {
				return nil, badOverride(at + ".p: not a declaration CSS accepts here: " + truncate(k+": "+v, 80))
			}
		}
	}
	css := KitOverrideCSS(&o)
	if _, changed, err := SanitizeCSS(css, CSSOptions{}); err != nil || changed {
		return nil, badOverride("the stylesheet does not pass the CSS sanitizer")
	}
	return &o, nil
}

// ErrTooLargeOverride is returned for an override above MaxOverrideBytes.
var ErrTooLargeOverride = errors.New("webbuilder: kit override too large")

type overrideErr struct{ msg string }

func (e overrideErr) Error() string { return e.msg }
func (e overrideErr) Unwrap() error { return ErrBadOverride }

func badOverride(msg string) error { return overrideErr{msg} }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func overrideKey(r OverrideRule) string {
	return strings.Join(r.Ctx, "\x01") + "\x02" + r.Sel
}

// overrideRuleShape: the rule's at-rule preludes and selector are ones ParseKitOverride takes.
func overrideRuleShape(r OverrideRule) bool {
	if len(r.Ctx) > MaxOverrideCtx || !overrideSelOK(r.Sel) {
		return false
	}
	for _, c := range r.Ctx {
		if !overrideCtxOK(c) {
			return false
		}
	}
	return true
}

// KitOverrideCSS prints an override (see the file comment). Rules without declarations print
// nothing; nil prints "".
func KitOverrideCSS(o *KitOverride) string {
	if o == nil {
		return ""
	}
	var b strings.Builder
	for _, r := range o.Rules {
		if !overrideRuleShape(r) { // never printed, whoever wrote the override (ParseKitOverride refuses it)
			continue
		}
		keys := make([]string, 0, len(r.P))
		for k, v := range r.P {
			if LocalDeclOK(k, v) {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			continue
		}
		sort.Strings(keys)
		decls := make([]string, len(keys))
		for i, k := range keys {
			decls[i] = k + ":" + localTrim(r.P[k])
		}
		for _, c := range r.Ctx {
			b.WriteString(strings.TrimSpace(c) + "{")
		}
		b.WriteString(strings.TrimSpace(r.Sel) + "{" + strings.Join(decls, ";") + "}")
		b.WriteString(strings.Repeat("}", len(r.Ctx)))
		b.WriteByte('\n')
	}
	return b.String()
}

// Empty reports whether the override prints nothing.
func (o *KitOverride) Empty() bool { return KitOverrideCSS(o) == "" }
