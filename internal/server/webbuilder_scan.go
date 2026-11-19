package server

import "strings"

// Literal scanners for the regular expressions the Web Builder front end ran over whole pages on
// every request (wbCodeCSSURLRE, wbKitRefRE). Go's regexp runs these patterns (an
// alternation or a case-insensitive prefix with no literal to skip to) through its NFA at every
// byte of a 100-200 KB page; these find the few candidate positions with strings.Index /
// IndexByte and check the pattern by hand there. Each one returns exactly what its regexp
// returns, case folding included: under (?i) Go also matches 's' with U+017F (ſ) and 'k' with
// U+212A (Kelvin sign); no other ASCII letter has a non-ASCII fold (webbuilder_scan_test.go
// checks every scanner against its regexp, also by fuzzing).

// scanSpace reports whether b is in Go's \s ([\t\n\f\r ]).
func scanSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

// scanFold matches the lower-case ASCII literal lit at s[i:] the way (?i) does, and returns the
// index after it (or -1). An 's' also matches "ſ" and a 'k' "K".
func scanFold(s string, i int, lit string) int {
	for j := 0; j < len(lit); j++ {
		if i >= len(s) {
			return -1
		}
		c, l := s[i], lit[j]
		switch {
		case c == l, 'a' <= l && l <= 'z' && c == l-'a'+'A':
			i++
		case l == 's' && strings.HasPrefix(s[i:], "ſ"):
			i += len("ſ")
		case l == 'k' && strings.HasPrefix(s[i:], "K"):
			i += len("K")
		default:
			return -1
		}
	}
	return i
}

// wbCSSURLs calls fn with the capture of every match of wbCodeCSSURLRE in src, in order:
//
//	(?i)(?:url\(\s*|@import\s+)(?:&#39;|&quot;|['"])?(https://[^'"\s)&;]+)
//
// Matches start at "url(" or "@import"; every '(' and '@' of src is a candidate, checked left to
// right, a candidate inside the previous match skipped (the regexp's matches never overlap).
func wbCSSURLs(src string, fn func(string)) {
	end := 0 // where the previous match ended
	paren, at := wbScanNext(src, 0, '('), wbScanNext(src, 0, '@')
	for paren >= 0 || at >= 0 {
		var start, i int
		if at < 0 || (paren >= 0 && paren-3 < at) {
			// "url(": the '(' at paren, "url" before it (ASCII letters only: no fold).
			start, i = paren-3, -1
			if start >= end && start >= 0 && scanFold(src, start, "url") == paren {
				i = paren + 1
				for i < len(src) && scanSpace(src[i]) {
					i++
				}
			}
			paren = wbScanNext(src, paren+1, '(')
		} else {
			start, i = at, -1
			if start >= end {
				if j := scanFold(src, at+1, "import"); j > 0 && j < len(src) && scanSpace(src[j]) {
					i = j
					for i < len(src) && scanSpace(src[i]) {
						i++
					}
				}
			}
			at = wbScanNext(src, at+1, '@')
		}
		if i < 0 {
			continue
		}
		// (?:&#39;|&quot;|['"])? then https:// (a quote that is not followed by it is no match:
		// without the quote, the next byte would have to be 'h').
		switch {
		case strings.HasPrefix(src[i:], "&#39;"):
			i += 5
		case scanFold(src, i, "&quot;") > 0:
			i += 6
		case i < len(src) && (src[i] == '\'' || src[i] == '"'):
			i++
		}
		from := i
		j := scanFold(src, i, "https://")
		if j < 0 {
			continue
		}
		k := j
		for k < len(src) && !wbCSSURLStop(src[k]) {
			k++
		}
		if k == j {
			continue
		}
		fn(src[from:k])
		end = k
		for paren >= 0 && paren-3 < end {
			paren = wbScanNext(src, paren+1, '(')
		}
		for at >= 0 && at < end {
			at = wbScanNext(src, at+1, '@')
		}
	}
}

// wbCSSURLStop reports whether b ends the URL of wbCSSURLs ([^'"\s)&;]).
func wbCSSURLStop(b byte) bool {
	return b == '\'' || b == '"' || b == ')' || b == '&' || b == ';' || scanSpace(b)
}

// wbScanNext is the index of the next b in src at or after from, -1 when none.
func wbScanNext(src string, from int, b byte) int {
	if from >= len(src) {
		return -1
	}
	if i := strings.IndexByte(src[from:], b); i >= 0 {
		return from + i
	}
	return -1
}

// wbKitRefs calls fn for every match of wbKitRefRE in src, in order, with the match's bounds and
// its groups:
//
//	(/wp-content/uploads/web-builder/kits/([a-z0-9-]{1,64})/[A-Za-z0-9_./@+-]{1,512}?\.[A-Za-z0-9]{1,8})(\?v=\d+)?(["'\s)>,])
//
// ref is group 1 (the file URL), slug group 2, term group 4 (one byte). The pattern is case
// sensitive and every match starts with wbKitURLPrefix: those are the candidates.
func wbKitRefs(src string, fn func(start, end int, ref, slug, term string)) {
	end := 0
	for from := 0; ; {
		i := strings.Index(src[from:], wbKitURLPrefix)
		if i < 0 {
			return
		}
		start := from + i
		from = start + 1
		if start < end {
			continue
		}
		// ([a-z0-9-]{1,64})/
		p := start + len(wbKitURLPrefix)
		q := p
		for q < len(src) && q-p <= 64 && wbKitSlugByte(src[q]) {
			q++
		}
		if q == p || q-p > 64 || q >= len(src) || src[q] != '/' {
			continue
		}
		slug := src[p:q]
		// [A-Za-z0-9_./@+-]{1,512}? \. — the shortest path whose '.' is followed by the rest.
		pathStart := q + 1
		if pathStart >= len(src) || !wbKitPathByte(src[pathStart]) {
			continue
		}
		for d := pathStart + 1; d < len(src) && d-pathStart <= 512; d++ {
			if src[d] == '.' {
				if refEnd, mEnd, ok := wbKitRefTail(src, d); ok {
					fn(start, mEnd, src[start:refEnd], slug, src[mEnd-1:mEnd])
					end = mEnd
					from = mEnd
					break
				}
			}
			if !wbKitPathByte(src[d]) {
				break
			}
		}
	}
}

// wbKitRefTail checks \.[A-Za-z0-9]{1,8}(\?v=\d+)?(["'\s)>,]) at src[dot:]: the end of group 1 and
// of the match.
func wbKitRefTail(src string, dot int) (refEnd, end int, ok bool) {
	k := dot + 1
	for k < len(src) && k-dot <= 9 && wbKitAlnum(src[k]) {
		k++
	}
	if n := k - dot - 1; n < 1 || n > 8 || (k < len(src) && wbKitAlnum(src[k])) {
		return 0, 0, false
	}
	refEnd = k
	if strings.HasPrefix(src[k:], "?v=") {
		d := k + 3
		for d < len(src) && '0' <= src[d] && src[d] <= '9' {
			d++
		}
		if d > k+3 && d < len(src) && wbKitRefTerm(src[d]) {
			return refEnd, d + 1, true
		}
	}
	if k < len(src) && wbKitRefTerm(src[k]) {
		return refEnd, k + 1, true
	}
	return 0, 0, false
}

func wbKitSlugByte(b byte) bool { return b == '-' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' }

func wbKitAlnum(b byte) bool {
	return '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

func wbKitPathByte(b byte) bool {
	return wbKitAlnum(b) || b == '_' || b == '.' || b == '/' || b == '@' || b == '+' || b == '-'
}

func wbKitRefTerm(b byte) bool {
	return b == '"' || b == '\'' || b == ')' || b == '>' || b == ',' || scanSpace(b)
}
