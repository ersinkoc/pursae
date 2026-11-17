package webbuilder

import (
	"regexp"
	"sort"
	"strconv"
)

// Breakpoint is a viewport width a design's CSS changes at: the threshold of an @media query's
// min-width or max-width condition, in pixels (em and rem count 16 px), and how many conditions
// use it.
type Breakpoint struct {
	Px    int `json:"px"`
	Count int `json:"count"`
}

var (
	mediaRuleRE = regexp.MustCompile(`(?is)@media\b([^{]*)\{`)
	mediaCondRE = regexp.MustCompile(`(?i)\(\s*(?:min|max)-width\s*:\s*([0-9]*\.?[0-9]+)\s*(px|em|rem)\s*\)`)
)

// SuggestBreakpoints lists the widths the stylesheets' @media queries switch at, most used first
// (ties: the smaller width first); at most limit (0: all). It reads only min-width and max-width
// conditions in px, em and rem, the widths a page is most likely styled for.
func SuggestBreakpoints(css []string, limit int) []Breakpoint {
	counts := map[int]int{}
	for _, src := range css {
		for _, m := range mediaRuleRE.FindAllStringSubmatch(src, -1) {
			for _, c := range mediaCondRE.FindAllStringSubmatch(m[1], -1) {
				v, err := strconv.ParseFloat(c[1], 64)
				if err != nil || v <= 0 {
					continue
				}
				if u := c[2]; u == "em" || u == "rem" {
					v *= 16
				}
				if px := int(v + 0.5); px >= 200 && px <= 4000 {
					counts[px]++
				}
			}
		}
	}
	out := make([]Breakpoint, 0, len(counts))
	for px, n := range counts {
		out = append(out, Breakpoint{px, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Px < out[j].Px
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
