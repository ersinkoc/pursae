package webbuilder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Local styles of code sections (docs/developer/web-builder-code-sections.md, "Styling"; contract
// §8.1 "Local styles").
//
// A code section's `local` field holds the style edits made in the element editor, per element
// (its data-wbc id) and per device × state variant:
//
//	"local": {"214": {"v": [
//	  {"bp": "",       "st": "",      "p": {"padding-top": "96px"}},
//	  {"bp": "tablet", "st": "",      "p": {"padding-top": "64px"}},
//	  {"bp": "",       "st": "hover", "p": {"background-color": "#0f172a !important"}}]}}
//
// Values are raw CSS (calc(), var(), clamp(), any unit); a value ending in !important is written
// so (the editor adds it when the design's winning declaration is important). The design's own
// markup and CSS are never changed: on publish the element gets the class e{post}-{id} (FillCode;
// data-wbc is still stripped) and the page gets one stylesheet with a rule per element, device and
// state:
//
//	.e5-214:not(#_):not(#_){padding-top:96px}
//	.e5-214:not(#_):not(#_):hover{background-color:#0f172a !important}
//	@media (max-width:1023px){
//	.e5-214:not(#_):not(#_){padding-top:64px}
//	}
//
// :not(#_) raises the specificity by one id without changing what matches, so the rule beats the
// design's own rules (up to one id) without touching them. Devices are desktop first (max-width):
// tablet applies at the tablet breakpoint and below, mobile at the mobile breakpoint and below, so
// a mobile view inherits the tablet value unless it has its own. The breakpoints are the design
// kit's (Breakpoints, suggested from the design's @media queries).
//
// LocalCSS is mirrored byte for byte by builder/host/elements/local.ts (the editor's live preview);
// testdata/local holds the shared fixtures both test suites run.

// LocalStyles is a code section's `local` field: data-wbc id → entry.
type LocalStyles map[string]LocalEntry

// LocalEntry is one element's local style.
type LocalEntry struct {
	// Label is an optional name (the editor names elements in content.names; kept for API clients).
	Label string         `json:"label,omitempty"`
	V     []LocalVariant `json:"v"`
}

// LocalVariant is one device × state of an element's local style.
type LocalVariant struct {
	BP string            `json:"bp"` // LocalDevices
	St string            `json:"st"` // LocalStates
	P  map[string]string `json:"p"`  // CSS property → raw value
}

// Breakpoints are the max-width thresholds (px) of the tablet and mobile devices.
type Breakpoints struct {
	Tablet int `json:"tablet"`
	Mobile int `json:"mobile"`
}

// DefaultBreakpoints are used when a kit has none (Tailwind's lg and sm: tablet ≤ 1023 px, mobile
// ≤ 639 px).
var DefaultBreakpoints = Breakpoints{Tablet: 1023, Mobile: 639}

// Limits of the local field (beside MaxStringBytes for every string).
const (
	MaxLocalElements = 2000
	MaxLocalVariants = 15 // 3 devices × 5 states
	MaxLocalProps    = 120
	MaxLocalValueLen = 4096
	MaxLocalBytes    = 512 << 10 // the whole field, as JSON

	// Breakpoint ranges: the editor's tablet frame (768 px) must be inside the tablet range and
	// outside the mobile range, its mobile frame (390 px) inside the mobile range.
	minTabletBP, maxTabletBP = 768, 1439
	minMobileBP, maxMobileBP = 390, 767
)

// LocalDevices and LocalStates in output order.
var (
	LocalDevices = []string{"", "tablet", "mobile"}
	LocalStates  = []string{"", "hover", "focus", "focus-visible", "active"}
)

var (
	localIDRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	localPropRE = regexp.MustCompile(`^(?:--[A-Za-z0-9_-]{1,80}|-?[a-z][a-z0-9-]{0,62})$`)
)

// orDefault replaces out-of-range thresholds (zero included) by DefaultBreakpoints.
func (b Breakpoints) orDefault() Breakpoints {
	if b.Tablet < minTabletBP || b.Tablet > maxTabletBP {
		b.Tablet = DefaultBreakpoints.Tablet
	}
	if b.Mobile < minMobileBP || b.Mobile > maxMobileBP || b.Mobile >= b.Tablet {
		b.Mobile = DefaultBreakpoints.Mobile
	}
	return b
}

// Normalize returns the breakpoints with out-of-range values replaced by the defaults.
func (b Breakpoints) Normalize() Breakpoints { return b.orDefault() }

// SuggestLocalBreakpoints picks the tablet and mobile thresholds from a design's stylesheets: the
// most used @media switch between 800 and 1280 px (tablet) and between 480 and 768 px (mobile),
// ties going to the one nearest 1024 / 640. A min-width of 1024 px and a max-width of 1023 px are
// the same switch (≤ 1023 px is the tablet range).
func SuggestLocalBreakpoints(css []string) Breakpoints {
	counts := map[int]int{}
	for _, b := range SuggestBreakpoints(css, 0) {
		px := b.Px
		if px%2 == 1 { // max-width: 1023px → the switch at 1024
			px++
		}
		counts[px] += b.Count
	}
	pick := func(lo, hi, near int) int {
		best, bestN := 0, 0
		for px, n := range counts {
			if px < lo || px > hi {
				continue
			}
			if n > bestN || (n == bestN && abs(px-near) < abs(best-near)) || (n == bestN && abs(px-near) == abs(best-near) && px < best) {
				best, bestN = px, n
			}
		}
		return best
	}
	out := DefaultBreakpoints
	if px := pick(800, 1280, 1024); px > 0 {
		out.Tablet = px - 1
	}
	if px := pick(480, 768, 640); px > 0 {
		out.Mobile = px - 1
	}
	return out.orDefault()
}

// LocalDeclOK reports whether name: value is a declaration local styles may hold: a property name
// (custom properties included), a value the CSS sanitizer keeps (no forbidden tokens, safe URLs,
// balanced), no comment, no "<", no control characters and at most MaxLocalValueLen bytes.
func LocalDeclOK(name, value string) bool {
	if !localPropRE.MatchString(name) {
		return false
	}
	v := localTrim(value)
	if v == "" || len(v) > MaxLocalValueLen || strings.ContainsAny(v, "<;{}") || strings.Contains(v, "/*") || strings.Contains(v, "*/") {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 && v[i] != '\t' || v[i] == 0x7f {
			return false
		}
	}
	if strings.HasSuffix(v, `\`) {
		return false
	}
	items := splitCSSItems(name + ":" + v)
	if len(items) != 1 || items[0].block || items[0].stray {
		return false
	}
	c := &cssCleaner{}
	return c.declOK(items[0].text, false)
}

// localTrim trims ASCII whitespace (the TS mirror trims the same set).
func localTrim(s string) string {
	return strings.Trim(s, " \t\n\r\f")
}

// ParseLocal decodes a local field; nil for null or an empty object.
func ParseLocal(raw json.RawMessage) (LocalStyles, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var l LocalStyles
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	return l, nil
}

// localProblem is one validation finding: a path below the field and a message.
type localProblem struct {
	path []any
	msg  string
}

// checkLocal validates a local field.
func checkLocal(l LocalStyles, size int) []localProblem {
	var out []localProblem
	if size > MaxLocalBytes {
		return append(out, localProblem{nil, fmt.Sprintf("local styles are larger than %d bytes", MaxLocalBytes)})
	}
	if len(l) > MaxLocalElements {
		return append(out, localProblem{nil, fmt.Sprintf("more than %d elements", MaxLocalElements)})
	}
	for id, e := range l {
		if !localIDRE.MatchString(id) {
			out = append(out, localProblem{[]any{id}, "bad element id"})
			continue
		}
		if len(e.Label) > 160 {
			out = append(out, localProblem{[]any{id, "label"}, "label too long"})
		}
		if len(e.V) > MaxLocalVariants {
			out = append(out, localProblem{[]any{id, "v"}, fmt.Sprintf("more than %d variants", MaxLocalVariants)})
			continue
		}
		for i, v := range e.V {
			if !inList(v.BP, LocalDevices) {
				out = append(out, localProblem{[]any{id, "v", i, "bp"}, "unknown device"})
			}
			if !inList(v.St, LocalStates) {
				out = append(out, localProblem{[]any{id, "v", i, "st"}, "unknown state"})
			}
			if len(v.P) > MaxLocalProps {
				out = append(out, localProblem{[]any{id, "v", i, "p"}, fmt.Sprintf("more than %d properties", MaxLocalProps)})
				continue
			}
			for k, val := range v.P {
				if !LocalDeclOK(k, val) {
					out = append(out, localProblem{[]any{id, "v", i, "p", k}, "unsafe or invalid CSS declaration"})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i].path...) < fmt.Sprint(out[j].path...) })
	return out
}

// LocalClass is the class an element with local styles gets in the published markup: e{post}-{id}
// (e-{id} without a post). The post keeps the classes of the header template, the page and the
// footer template apart: their data-wbc ids are numbered per document.
func LocalClass(post, id string) string {
	return "e" + post + "-" + id
}

// LocalSelector is the published selector of an element.
func LocalSelector(post string) func(id string) string {
	return func(id string) string { return "." + LocalClass(post, id) }
}

// LocalCSS is the stylesheet of local styles (see the file comment); sel gives an element's
// selector (LocalSelector on publish; the editor uses [data-wbc="…"]). Declarations that are not
// LocalDeclOK are skipped. Mirrored by local.ts localCSS.
func LocalCSS(l LocalStyles, bps Breakpoints, sel func(id string) string) string {
	if len(l) == 0 {
		return ""
	}
	bps = bps.orDefault()
	ids := make([]string, 0, len(l))
	for id := range l {
		if localIDRE.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, bp := range LocalDevices {
		var lines []string
		for _, id := range ids {
			for _, st := range LocalStates {
				p := map[string]string{}
				for _, v := range l[id].V {
					if v.BP != bp || v.St != st {
						continue
					}
					for k, val := range v.P {
						p[k] = val
					}
				}
				keys := make([]string, 0, len(p))
				for k, val := range p {
					if LocalDeclOK(k, val) {
						keys = append(keys, k)
					}
				}
				if len(keys) == 0 {
					continue
				}
				sort.Strings(keys)
				decls := make([]string, len(keys))
				for i, k := range keys {
					decls[i] = k + ":" + localTrim(p[k])
				}
				s := sel(id) + ":not(#_):not(#_)"
				if st != "" {
					s += ":" + st
				}
				lines = append(lines, s+"{"+strings.Join(decls, ";")+"}")
			}
		}
		if len(lines) == 0 {
			continue
		}
		if bp == "" {
			b.WriteString(strings.Join(lines, "\n") + "\n")
			continue
		}
		px := bps.Tablet
		if bp == "mobile" {
			px = bps.Mobile
		}
		b.WriteString("@media (max-width:" + strconv.Itoa(px) + "px){\n" + strings.Join(lines, "\n") + "\n}\n")
	}
	return b.String()
}

// styledIDs are the ids of l with at least one declaration LocalCSS prints.
func styledIDs(l LocalStyles) map[string]bool {
	out := map[string]bool{}
	for id, e := range l {
		if !localIDRE.MatchString(id) {
			continue
		}
		for _, v := range e.V {
			if !inList(v.BP, LocalDevices) || !inList(v.St, LocalStates) {
				continue
			}
			for k, val := range v.P {
				if LocalDeclOK(k, val) {
					out[id] = true
				}
			}
		}
	}
	return out
}
