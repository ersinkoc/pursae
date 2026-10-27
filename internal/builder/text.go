package builder

// Text filters WordPress applies to widget text: wptexturize, shortcode handling, wpautop,
// wp_kses_normalize_entities. Ports of wp-includes/formatting.php (simplified where noted).

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// parseTextEditor mirrors Widget_Base::parse_text_editor (minus oEmbed auto-embeds).
func (r *renderer) parseTextEditor(content string) string {
	content = shortcodeUnautop(content)
	content = r.doShortcode(content)
	content = wptexturize(content)
	return content
}

func (r *renderer) doShortcode(s string) string {
	if r.rc.Shortcode == nil || !strings.Contains(s, "[") {
		return s
	}
	return r.rc.Shortcode(s)
}

var shortcodeUnautopRe = regexp.MustCompile(`<p>\s*(\[[^\]]+\])\s*</p>`)

func shortcodeUnautop(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	return shortcodeUnautopRe.ReplaceAllString(s, "$1")
}

const (
	txOpenSQ   = "&#8216;"
	txCloseSQ  = "&#8217;"
	txApos     = "&#8217;"
	txOpenQ    = "&#8220;"
	txCloseQ   = "&#8221;"
	txPrime    = "&#8242;"
	txDPrime   = "&#8243;"
	txEmDash   = "&#8212;"
	txEnDash   = "&#8211;"
	flagApos   = "<!--apos-->"
	flagOpenSQ = "<!--oq-->"
	flagOpenQ  = "<!--oqq-->"
	flagPrime  = "<!--wp-prime-or-quote-->"
)

var noTexturizeTags = map[string]bool{"pre": true, "code": true, "kbd": true, "style": true, "script": true, "tt": true}

var texturizeSplitRe = regexp.MustCompile(`(<!--[\s\S]*?-->|<[^>]*>|\[[^\[\]]*\])`)

// wptexturize mirrors WordPress wptexturize() for the default (en_US) quote set.
func wptexturize(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	var out strings.Builder
	var tagStack []string
	last := 0
	for _, m := range texturizeSplitRe.FindAllStringIndex(text, -1) {
		out.WriteString(texturizeText(text[last:m[0]], len(tagStack) > 0))
		tok := text[m[0]:m[1]]
		if strings.HasPrefix(tok, "<") && !strings.HasPrefix(tok, "<!--") {
			name := tagName(tok)
			if noTexturizeTags[name] {
				if strings.HasPrefix(tok, "</") {
					if len(tagStack) > 0 && tagStack[len(tagStack)-1] == name {
						tagStack = tagStack[:len(tagStack)-1]
					}
				} else if !strings.HasSuffix(tok, "/>") {
					tagStack = append(tagStack, name)
				}
			}
			out.WriteString(escapeLoneAmp(tok))
		} else if strings.HasPrefix(tok, "[") {
			out.WriteString(tok)
		} else {
			out.WriteString(tok)
		}
		last = m[1]
	}
	out.WriteString(texturizeText(text[last:], len(tagStack) > 0))
	return out.String()
}

func tagName(tok string) string {
	t := strings.TrimPrefix(strings.TrimPrefix(tok, "<"), "/")
	end := strings.IndexFunc(t, func(r rune) bool { return r == ' ' || r == '>' || r == '/' || r == '\t' || r == '\n' })
	if end >= 0 {
		t = t[:end]
	}
	return strings.ToLower(t)
}

func isTxSpaceAt(s string, i int) (int, bool) {
	if i < 0 || i >= len(s) {
		return 0, false
	}
	switch s[i] {
	case ' ', '\t', '\n', '\r':
		return 1, true
	}
	if strings.HasPrefix(s[i:], " ") {
		return 2, true
	}
	if strings.HasPrefix(s[i:], "&nbsp;") {
		return 6, true
	}
	return 0, false
}

func endsWithTxSpace(s string) bool {
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return strings.HasSuffix(s, " ") || strings.HasSuffix(s, "&nbsp;")
}

func texturizeText(curl string, skip bool) string {
	if skip || curl == "" {
		return curl
	}
	for _, p := range [][2]string{{"...", "&#8230;"}, {"``", txOpenQ}, {"''", txCloseQ}, {" (tm)", " &#8482;"}} {
		curl = strings.ReplaceAll(curl, p[0], p[1])
	}
	if strings.Contains(curl, "'") {
		curl = texturizeApos(curl)
		curl = texturizePrimes(curl, "'", txPrime, flagOpenSQ, txCloseSQ)
		curl = strings.ReplaceAll(curl, flagApos, txApos)
		curl = strings.ReplaceAll(curl, flagOpenSQ, txOpenSQ)
	}
	if strings.Contains(curl, `"`) {
		curl = texturizeQuotes(curl)
		curl = texturizePrimes(curl, `"`, txDPrime, flagOpenQ, txCloseQ)
		curl = strings.ReplaceAll(curl, flagOpenQ, txOpenQ)
	}
	if strings.Contains(curl, "-") {
		curl = texturizeDashes(curl)
	}
	curl = texturizeTimes(curl)
	curl = escapeLoneAmp(curl)
	return curl
}

var entityAfterAmpRe = regexp.MustCompile(`(?i)^&(?:#(?:\d+|x[a-f0-9]+);|[a-z1-4]{1,8};)`)

func escapeLoneAmp(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '&' && !entityAfterAmpRe.MatchString(s[i:]) {
			b.WriteString("&#038;")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWordByteBefore(s string, i int) bool {
	if i <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// texturizeApos applies the apostrophe/single-quote dynamic patterns in order.
func texturizeApos(s string) string {
	// 1. '99' -> apos 99 close-single-quote when followed by end/punct/space
	s = scanReplace(s, "'", func(s string, i int) (string, int, bool) {
		if i+3 < len(s)+0 && i+3 <= len(s)-1 && isDigit(s[i+1]) && isDigit(s[i+2]) && s[i+3] == '\'' {
			if afterQuoteOK(s, i+4) {
				return flagApos + s[i+1:i+3] + txCloseSQ, 4, true
			}
		}
		return "", 0, false
	})
	// 1b. '99" -> apos 99 closing double quote
	s = scanReplace(s, "'", func(s string, i int) (string, int, bool) {
		if i+3 <= len(s)-1 && isDigit(s[i+1]) && isDigit(s[i+2]) && s[i+3] == '"' {
			if afterQuoteOK(s, i+4) {
				return flagApos + s[i+1:i+3] + txCloseQ, 4, true
			}
		}
		return "", 0, false
	})
	// 2. '99 '99s -> apostrophe (never '9, '99%, '999, '99.0)
	s = scanReplace(s, "'", func(s string, i int) (string, int, bool) {
		if i+2 < len(s)+0 && i+2 <= len(s)-1 && isDigit(s[i+1]) && isDigit(s[i+2]) {
			j := i + 3
			if j >= len(s) {
				return flagApos, 1, true
			}
			if s[j] == '%' || isDigit(s[j]) {
				return "", 0, false
			}
			if (s[j] == '.' || s[j] == ',') && j+1 < len(s) && isDigit(s[j+1]) {
				return "", 0, false
			}
			return flagApos, 1, true
		}
		return "", 0, false
	})
	// 3. quoted numbers '0.42'
	s = scanReplace(s, "'", func(s string, i int) (string, int, bool) {
		if !(i == 0 || endsWithTxSpace(s[:i])) {
			return "", 0, false
		}
		j := i + 1
		if j >= len(s) || !isDigit(s[j]) {
			return "", 0, false
		}
		k := j + 1
		for k < len(s) && (isDigit(s[k]) || s[k] == '.' || s[k] == ',') {
			k++
		}
		// greedy then backtrack to find a closing quote
		for e := k; e > j; e-- {
			if e < len(s) && s[e] == '\'' {
				return flagOpenSQ + s[j:e] + txCloseSQ, e + 1 - i, true
			}
		}
		return "", 0, false
	})
	// 4. opening single quote at start or after ( [ { " - &lt; or spaces
	s = scanReplace(s, "'", func(s string, i int) (string, int, bool) {
		if i == 0 || strings.ContainsRune(`([{"-`, rune(s[i-1])) || strings.HasSuffix(s[:i], "&lt;") || endsWithTxSpace(s[:i]) {
			return flagOpenSQ, 1, true
		}
		return "", 0, false
	})
	// 5. apostrophe in a word
	s = scanReplace(s, "'", func(s string, i int) (string, int, bool) {
		if endsWithTxSpace(s[:i]) {
			return "", 0, false
		}
		j := i + 1
		if j >= len(s) {
			return "", 0, false
		}
		if strings.ContainsRune(`.,:;!?"'(){}[]-`, rune(s[j])) || strings.HasPrefix(s[j:], "&lt;") || strings.HasPrefix(s[j:], "&gt;") {
			return "", 0, false
		}
		if _, sp := isTxSpaceAt(s, j); sp {
			return "", 0, false
		}
		return flagApos, 1, true
	})
	return s
}

func afterQuoteOK(s string, j int) bool {
	if j >= len(s) {
		return true
	}
	if strings.ContainsRune(".,:;!?)}-]", rune(s[j])) || strings.HasPrefix(s[j:], "&gt;") {
		return true
	}
	_, sp := isTxSpaceAt(s, j)
	return sp
}

func texturizeQuotes(s string) string {
	// quoted numbers "42"
	s = scanReplace(s, `"`, func(s string, i int) (string, int, bool) {
		if !(i == 0 || endsWithTxSpace(s[:i])) {
			return "", 0, false
		}
		j := i + 1
		if j >= len(s) || !isDigit(s[j]) {
			return "", 0, false
		}
		k := j + 1
		for k < len(s) && (isDigit(s[k]) || s[k] == '.' || s[k] == ',') {
			k++
		}
		for e := k; e > j; e-- {
			if e < len(s) && s[e] == '"' {
				return flagOpenQ + s[j:e] + txCloseQ, e + 1 - i, true
			}
		}
		return "", 0, false
	})
	// opening double quote
	s = scanReplace(s, `"`, func(s string, i int) (string, int, bool) {
		if !(i == 0 || strings.ContainsRune(`([{-`, rune(s[i-1])) || strings.HasSuffix(s[:i], "&lt;") || endsWithTxSpace(s[:i])) {
			return "", 0, false
		}
		if _, sp := isTxSpaceAt(s, i+1); sp {
			return "", 0, false
		}
		return flagOpenQ, 1, true
	})
	return s
}

func texturizeDashes(s string) string {
	s = strings.ReplaceAll(s, "---", txEmDash)
	s = scanReplace(s, "--", func(s string, i int) (string, int, bool) {
		before := i == 0 || endsWithTxSpace(s[:i])
		after := i+2 >= len(s)
		if !after {
			_, after = isTxSpaceAt(s, i+2)
		}
		if before && after {
			return txEmDash, 2, true
		}
		return "", 0, false
	})
	s = scanReplace(s, "--", func(s string, i int) (string, int, bool) {
		if strings.HasSuffix(s[:i], "xn") {
			return "", 0, false
		}
		return txEnDash, 2, true
	})
	s = scanReplace(s, "-", func(s string, i int) (string, int, bool) {
		before := i == 0 || endsWithTxSpace(s[:i])
		after := i+1 >= len(s)
		if !after {
			_, after = isTxSpaceAt(s, i+1)
		}
		if before && after {
			return txEnDash, 1, true
		}
		return "", 0, false
	})
	return s
}

var timesRe = regexp.MustCompile(`\b(\d[\d\.,]*)x(\d[\d\.,]*)\b`)

func texturizeTimes(s string) string {
	if !strings.Contains(s, "x") {
		return s
	}
	return timesRe.ReplaceAllStringFunc(s, func(m string) string {
		sm := timesRe.FindStringSubmatch(m)
		if strings.HasPrefix(sm[1], "0") && len(sm[1]) == 1 {
			return m // 0x9999 is never a multiplication
		}
		return sm[1] + "&#215;" + sm[2]
	})
}

// scanReplace walks s and, at every occurrence of needle, asks fn for a replacement.
func scanReplace(s, needle string, fn func(s string, i int) (string, int, bool)) string {
	if !strings.Contains(s, needle) {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], needle) {
			// lookbehind must see the already-rewritten prefix like PCRE does on the original
			// subject; patterns here only look at the original text, so pass the original.
			if repl, n, ok := fn(s, i); ok {
				b.WriteString(repl)
				i += n
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// texturizePrimes mirrors wptexturize_primes.
func texturizePrimes(haystack, needle, prime, openQuote, closeQuote string) string {
	sentences := strings.Split(haystack, openQuote)
	quoteAt := func(s string, i int) bool { return afterQuoteOK(s, i+len(needle)) }
	for key, sentence := range sentences {
		if !strings.Contains(sentence, needle) {
			continue
		}
		replacePrimes := func(s string) string {
			return scanReplace(s, needle, func(s string, i int) (string, int, bool) {
				if i > 0 && isDigit(s[i-1]) {
					return prime, len(needle), true
				}
				return "", 0, false
			})
		}
		if key != 0 && strings.Count(sentence, closeQuote) == 0 {
			count := 0
			sentence = scanReplace(sentence, needle, func(s string, i int) (string, int, bool) {
				if quoteAt(s, i) {
					count++
					return flagPrime, len(needle), true
				}
				return "", 0, false
			})
			if count > 1 {
				count2 := 0
				sentence = scanReplace(sentence, flagPrime, func(s string, i int) (string, int, bool) {
					if i > 0 && isDigit(s[i-1]) {
						return "", 0, false
					}
					count2++
					return closeQuote, len(flagPrime), true
				})
				if count2 == 0 {
					pos := strings.LastIndex(sentence, flagPrime+".")
					if pos < 0 {
						pos = strings.LastIndex(sentence, flagPrime)
					}
					if pos >= 0 {
						sentence = sentence[:pos] + closeQuote + sentence[pos+len(flagPrime):]
					}
				}
				sentence = replacePrimes(sentence)
				sentence = scanReplace(sentence, flagPrime, func(s string, i int) (string, int, bool) {
					if i > 0 && isDigit(s[i-1]) {
						return prime, len(flagPrime), true
					}
					return "", 0, false
				})
				sentence = strings.ReplaceAll(sentence, flagPrime, closeQuote)
			} else if count == 1 {
				sentence = strings.ReplaceAll(sentence, flagPrime, closeQuote)
				sentence = replacePrimes(sentence)
			} else {
				sentence = replacePrimes(sentence)
			}
		} else {
			sentence = replacePrimes(sentence)
			sentence = scanReplace(sentence, needle, func(s string, i int) (string, int, bool) {
				if quoteAt(s, i) {
					return closeQuote, len(needle), true
				}
				return "", 0, false
			})
		}
		if needle == `"` && strings.Contains(sentence, `"`) {
			sentence = strings.ReplaceAll(sentence, `"`, closeQuote)
		}
		sentences[key] = sentence
	}
	return strings.Join(sentences, openQuote)
}

// ksesPost approximates wp_kses_post for trusted editor content: entities are normalised (lone &
// becomes &amp;); markup is kept as is (the HTML sanitizer itself is not ported).
func ksesPost(s string) string {
	return ksesNormalizeEntities(s)
}

var ksesEntityRe = regexp.MustCompile(`^&(?:[A-Za-z][A-Za-z0-9]{0,31}|#[0-9]{1,7}|#[xX][0-9A-Fa-f]{1,6});`)

func ksesNormalizeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			if m := ksesEntityRe.FindString(s[i:]); m != "" {
				b.WriteString(m)
				i += len(m) - 1
				continue
			}
			b.WriteString("&amp;")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// wpautop mirrors WordPress wpautop() for the common cases (paragraphs and <br />).
func wpautop(text string, br bool) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	text = text + "\n"
	allBlocks := `(?:table|thead|tfoot|caption|col|colgroup|tbody|tr|td|th|div|dl|dd|dt|ul|ol|li|pre|form|map|area|blockquote|address|style|p|h[1-6]|hr|fieldset|legend|section|article|aside|hgroup|header|footer|nav|figure|figcaption|details|menu|summary)`
	text = regexp.MustCompile(`<br\s*/?>\s*<br\s*/?>`).ReplaceAllString(text, "\n\n")
	text = regexp.MustCompile(`(<`+allBlocks+`[\s/>])`).ReplaceAllString(text, "\n\n$1")
	text = regexp.MustCompile(`(</`+allBlocks+`>)`).ReplaceAllString(text, "$1\n\n")
	text = regexp.MustCompile(`<hr\s*?/?>`).ReplaceAllString(text, "<hr />\n\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = regexp.MustCompile(`\n\s*\n`).ReplaceAllString(text, "\n\n")
	var b strings.Builder
	for _, p := range regexp.MustCompile(`\n\s*\n`).Split(text, -1) {
		p = strings.Trim(p, "\n")
		if strings.TrimSpace(p) == "" {
			continue
		}
		b.WriteString("<p>" + p + "</p>\n")
	}
	text = b.String()
	text = regexp.MustCompile(`<p>\s*</p>`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`<p>([^<]+)</(div|address|form)>`).ReplaceAllString(text, "<p>$1</p></$2>")
	text = regexp.MustCompile(`<p>\s*(</?`+allBlocks+`[^>]*>)\s*</p>`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`<p>(<li.+?)</p>`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`(?i)<p><blockquote([^>]*)>`).ReplaceAllString(text, "<blockquote$1><p>")
	text = strings.ReplaceAll(text, "</blockquote></p>", "</p></blockquote>")
	text = regexp.MustCompile(`<p>\s*(</?`+allBlocks+`[^>]*>)`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`(</?`+allBlocks+`[^>]*>)\s*</p>`).ReplaceAllString(text, "$1")
	if br {
		text = regexp.MustCompile(`<br>|<br/>`).ReplaceAllString(text, "<br />")
		text = regexp.MustCompile(`(<br />)?\s*\n`).ReplaceAllStringFunc(text, func(m string) string {
			return "<br />\n"
		})
		text = regexp.MustCompile(`(</?`+allBlocks+`[^>]*>)\s*<br />`).ReplaceAllString(text, "$1")
		text = strings.ReplaceAll(text, "<br />\n</p>", "</p>")
		text = regexp.MustCompile(`<br />(\s*</?(?:p|li|div|dl|dd|dt|th|pre|td|ul|ol)[^>]*>)`).ReplaceAllString(text, "$1")
		text = regexp.MustCompile(`<br />\n$`).ReplaceAllString(text, "\n")
	}
	text = regexp.MustCompile(`\n</p>$`).ReplaceAllString(text, "</p>")
	return text
}

var customAttrKeyRe = regexp.MustCompile(`[-_a-z0-9]+`)
