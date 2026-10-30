package i18n

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// TranslateHTML translates a rendered admin page: text between tags and the user-facing
// attributes (title, placeholder, aria-label, alt, label, button values) whose text is a msgid
// of the catalog, or matches a msgid with printf placeholders ("%s items"). Scripts, styles,
// <textarea>, <pre>, <code>, comments and elements marked translate="no" (user content: post
// titles, names) are never touched.
func (c *Catalog) TranslateHTML(doc []byte) []byte {
	if c.Empty() || len(doc) == 0 {
		return doc
	}
	src := string(doc)
	var out strings.Builder
	out.Grow(len(src) + len(src)/8)
	skipName, skipDepth := "", 0 // the element that opened a translate="no" region, and its nesting
	i := 0
	for i < len(src) {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			out.WriteString(c.text(src[i:], skipDepth > 0))
			break
		}
		out.WriteString(c.text(src[i:i+lt], skipDepth > 0))
		i += lt
		// Comments.
		if strings.HasPrefix(src[i:], "<!--") {
			end := strings.Index(src[i+4:], "-->")
			if end < 0 {
				out.WriteString(src[i:])
				break
			}
			out.WriteString(src[i : i+4+end+3])
			i += 4 + end + 3
			continue
		}
		gt := tagEndAt(src, i)
		if gt < 0 {
			out.WriteString(src[i:])
			break
		}
		tag := src[i : gt+1]
		name, closing := tagName(tag)
		i = gt + 1
		if name == "" {
			out.WriteString(tag)
			continue
		}
		if closing {
			out.WriteString(tag)
			if skipDepth > 0 && name == skipName {
				skipDepth--
			}
			continue
		}
		selfClosing := strings.HasSuffix(tag, "/>") || voidElements[name]
		attrs := scanAttrs(tag)
		if skipDepth > 0 {
			if name == skipName && !selfClosing {
				skipDepth++
			}
			out.WriteString(tag)
		} else if v, ok := attrs.get("translate"); (ok && strings.EqualFold(v, "no")) || name == "option" && !attrs.has("value") || quickEditData(attrs) {
			// translate="no" marks user content; an <option> without a value submits its text;
			// the list tables' hidden quick-edit data (id="inline_N") holds raw values.
			out.WriteString(tag)
			if !selfClosing {
				skipName, skipDepth = name, 1
			}
		} else {
			out.WriteString(c.attrs(tag, name, attrs))
			// The document title: "Page &lsaquo; Site &#8212; WordPress Go" translates the page part.
			if name == "title" {
				if end := strings.Index(src[i:], "</title>"); end >= 0 {
					t := src[i : i+end]
					page, rest, found := strings.Cut(t, " &lsaquo; ")
					if tr, ok := c.translateText(strings.TrimSpace(page)); ok && found {
						out.WriteString(tr + " &lsaquo; " + rest)
						i += end
						continue
					}
				}
			}
			// A sentence with inline markup ("… <a href="%s">…</a>.") is one message.
			if containers[name] && !selfClosing {
				if inner, end, ok := inlineContent(src, i, name); ok {
					if tr, ok := c.translateMarkup(inner); ok {
						out.WriteString(tr)
						i = end
						continue
					}
					if MissHook != nil {
						MissHook(spaceRun.ReplaceAllString(strings.TrimSpace(inner), " "))
					}
				}
			}
		}
		// Raw-text elements: copy through to the closing tag untouched.
		if rawElements[name] && !selfClosing {
			end := indexClose(src, i, name)
			if end < 0 {
				out.WriteString(src[i:])
				break
			}
			out.WriteString(src[i:end])
			i = end
		}
	}
	return []byte(out.String())
}

var rawElements = map[string]bool{"script": true, "style": true, "textarea": true, "pre": true, "code": true, "template": true}

var voidElements = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

// MissHook, when set, receives every text that had no translation (development: finding the
// strings WordPress Go needs translations of its own for).
var MissHook func(text string)

// text translates one text node, keeping its surrounding whitespace.
func (c *Catalog) text(t string, skip bool) string {
	if skip {
		return t
	}
	core := strings.TrimSpace(t)
	if core == "" || !hasLetter(core) {
		return t
	}
	tr, ok := c.translateText(core)
	if !ok {
		c.miss(core)
		return t
	}
	lead := t[:strings.Index(t, core)]
	return lead + tr + t[len(lead)+len(core):]
}

var spaceRun = regexp.MustCompile(`\s+`)

// miss reports escaped text without a translation to MissHook, unless the language writes the
// message as it is ("Peru" in Turkish).
func (c *Catalog) miss(core string) {
	if MissHook == nil {
		return
	}
	plain := html.UnescapeString(spaceRun.ReplaceAllString(core, " "))
	if !c.has(plain) && !c.has(core) {
		MissHook(plain)
	}
}

// translateText translates escaped HTML text; the result is escaped HTML too.
func (c *Catalog) translateText(core string) (string, bool) {
	norm := spaceRun.ReplaceAllString(core, " ")
	// msgids keep entities as WordPress writes them ("&hellip;"), so try the raw text first.
	if tr, ok := c.lookup(norm); ok {
		return tr, true
	}
	plain := html.UnescapeString(norm)
	if tr, ok := c.lookup(plain); ok {
		return escapeText(tr), true
	}
	// "Settings:" and "Settings" are usually the same message.
	if strings.HasSuffix(plain, ":") {
		if tr, ok := c.lookup(strings.TrimSuffix(plain, ":")); ok {
			return escapeText(tr) + ":", true
		}
	}
	if tr, ok := c.matchPattern(plain); ok {
		return escapeText(tr), true
	}
	if tr, ok := c.translateDate(plain); ok {
		return escapeText(tr), true
	}
	if tr, ok := c.translateParts(plain, 0); ok {
		return escapeText(tr), true
	}
	return "", false
}

var (
	monthNames = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	dateRe     = regexp.MustCompile(`^(January|February|March|April|May|June|July|August|September|October|November|December) (\d{1,2}), (\d{4})$`)
)

// translateDate rewrites a date in WordPress' default format ("October 5, 2026", date_i18n()
// with "F j, Y") in the language's own format and month names.
func (c *Catalog) translateDate(plain string) (string, bool) {
	m := dateRe.FindStringSubmatch(plain)
	if m == nil {
		return "", false
	}
	month, ok := c.lookup(m[1])
	if !ok {
		return "", false
	}
	format := c.T("F j, Y")
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		switch ch := format[i]; ch {
		case 'F':
			b.WriteString(month)
		case 'j':
			b.WriteString(m[2])
		case 'd':
			if len(m[2]) == 1 {
				b.WriteByte('0')
			}
			b.WriteString(m[2])
		case 'Y':
			b.WriteString(m[3])
		case 'n', 'm':
			for k, name := range monthNames {
				if name == m[1] {
					if ch == 'm' && k < 9 {
						b.WriteByte('0')
					}
					b.WriteString(strconv.Itoa(k + 1))
				}
			}
		case '\\':
			if i+1 < len(format) {
				i++
				b.WriteByte(format[i])
			}
		default:
			b.WriteByte(ch)
		}
	}
	return b.String(), true
}

// partSeps join messages that are translated one by one: "Turkey — Adana" (a country and a
// state), "Shop · Minimal apparel boutique" (a category and a description), "Alaska, United
// States (US)". A comma only joins when every part is a message of its own.
var partSeps = []string{" — ", " · ", " | ", " – ", " › ", ", "}

// codePart is a part that stays as it is: a currency or country code, a number, a symbol.
var codePart = regexp.MustCompile(`^(?:[A-Z]{2,4}|[^\p{L}]*|\([^()]*\))$`)

// has reports whether msgid is a message of the catalog, even one translated as itself.
func (c *Catalog) has(msgid string) bool {
	e, _ := c.find(msgid)
	return e != nil
}

// nameLike reports whether a part reads as a proper name ("Adana", "Rio Grande do Sul",
// "Crumb & Crust"): a few words, the first capitalized, no sentence punctuation.
func nameLike(p string) bool {
	w := strings.Fields(p)
	if len(w) == 0 || len(w) > 4 || strings.ContainsAny(p, ".!?:;") {
		return false
	}
	r := []rune(w[0])
	return unicode.IsUpper(r[0])
}

var (
	trailingParen = regexp.MustCompile(`^(.*\p{L}.*?)(\s*\([^()]*\))$`)
	edgePunct     = regexp.MustCompile(`^([^\p{L}\p{N}"'“‘(\[{]*)(.*?\p{L}.*?)([^\p{L}\p{N}"'”’)\]}%]*)$`)
)

// translateParts translates text made of messages that the page put together: separated
// parts, a trailing parenthesis ("Shop (ID: 5)", "Algerian dinar (د.ج)") or punctuation around a
// message ("> Add shipping zone").
func (c *Catalog) translateParts(plain string, depth int) (string, bool) {
	if depth > 2 {
		return "", false
	}
	one := func(p string) (string, bool) {
		if tr, ok := c.lookup(p); ok {
			return tr, true
		}
		return c.translateParts(p, depth+1)
	}
	for _, sep := range partSeps {
		if !strings.Contains(plain, sep) {
			continue
		}
		parts := strings.Split(plain, sep)
		if sep == ", " && (len(parts) != 2 || len(strings.Fields(plain)) > 6) {
			continue
		}
		translated := 0
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if codePart.MatchString(p) {
				continue
			}
			tr, ok := one(p)
			if !ok && c.has(p) { // a message the language writes the same ("Türkiye" in Turkish)
				tr, ok = p, true
			}
			if !ok {
				if nameLike(p) { // "Adana" in "Turkey — Adana" is a place name, not a message
					continue
				}
				translated = -1
				break
			}
			parts[i] = tr
			translated++
		}
		if translated > 0 {
			return strings.Join(parts, sep), true
		}
	}
	if m := trailingParen.FindStringSubmatch(plain); m != nil {
		if tr, ok := one(strings.TrimSpace(m[1])); ok {
			return tr + m[2], true
		}
	}
	if m := edgePunct.FindStringSubmatch(plain); m != nil && (m[1] != "" || m[3] != "") && m[2] != plain {
		if tr, ok := c.lookup(m[2]); ok {
			return m[1] + tr + m[3], true
		}
	}
	return "", false
}

var entityRe = regexp.MustCompile(`^&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);`)

// escapeText escapes a translation for HTML text like esc_html(): existing entities
// ("&#8220;", "&hellip;") are kept, not double-encoded.
func escapeText(s string) string {
	if !strings.ContainsAny(s, "<>&\"") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; ch {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '&':
			if entityRe.MatchString(s[i:]) {
				b.WriteByte('&')
			} else {
				b.WriteString("&amp;")
			}
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

var containers = map[string]bool{"p": true, "label": true, "td": true, "th": true, "li": true, "span": true, "div": true,
	"dd": true, "dt": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "legend": true,
	"figcaption": true, "button": true, "a": true, "strong": true, "em": true, "small": true, "caption": true}

var inlineTags = map[string]bool{"img": true, "a": true, "strong": true, "em": true, "b": true, "i": true, "code": true, "span": true,
	"abbr": true, "br": true, "kbd": true, "small": true, "sup": true, "sub": true}

// inlineContent returns the content of the element opened just before src[i] when it holds
// text with inline markup only (no block elements, no element of the same name), and the
// index of its closing tag.
func inlineContent(src string, i int, name string) (string, int, bool) {
	limit := i + 2000
	if limit > len(src) {
		limit = len(src)
	}
	end := strings.Index(src[i:limit], "</"+name)
	if end < 0 {
		return "", 0, false
	}
	inner := src[i : i+end]
	if !strings.Contains(inner, "<") || !hasLetter(inner) {
		return "", 0, false
	}
	for k := 0; k < len(inner); k++ {
		if inner[k] != '<' {
			continue
		}
		gt := tagEndAt(inner, k)
		if gt < 0 {
			return "", 0, false
		}
		n, _ := tagName(inner[k : gt+1])
		if !inlineTags[n] || n == name {
			return "", 0, false
		}
		if v, ok := scanAttrs(inner[k : gt+1]).get("translate"); ok && strings.EqualFold(v, "no") {
			return "", 0, false
		}
		k = gt
	}
	return inner, i + end, true
}

// translateMarkup translates inner HTML that WordPress writes as one message with markup.
func (c *Catalog) translateMarkup(inner string) (string, bool) {
	core := strings.TrimSpace(inner)
	norm := spaceRun.ReplaceAllString(core, " ")
	tr, ok := c.lookup(norm)
	if !ok {
		tr, ok = c.matchMarkup(norm)
	}
	if !ok { // "Howdy, %s" with the name's markup as the argument
		c.patOnce.Do(c.buildPatterns)
		tr, ok = matchIn(c.patterns, norm, true)
	}
	if !ok {
		return "", false
	}
	lead := inner[:strings.Index(inner, core)]
	return lead + tr + inner[len(lead)+len(core):], true
}

// translatableAttrs are the attributes WordPress' own strings go into.
var translatableAttrs = map[string]bool{"title": true, "placeholder": true, "aria-label": true, "alt": true, "label": true,
	"data-title": true, "aria-description": true}

func (c *Catalog) attrs(tag, name string, a attrList) string {
	var b strings.Builder
	last := 0
	button := false
	if name == "input" {
		typ, _ := a.get("type")
		switch strings.ToLower(typ) {
		case "submit", "button", "reset":
			button = true
		}
	}
	for _, at := range a {
		if at.valStart < 0 || !(translatableAttrs[at.name] || at.name == "value" && button) {
			continue
		}
		val := tag[at.valStart:at.valEnd]
		core := strings.TrimSpace(val)
		if core == "" || !hasLetter(core) {
			continue
		}
		tr, ok := c.translateText(core)
		if !ok {
			c.miss(core)
			continue
		}
		if at.quote != 0 {
			tr = strings.ReplaceAll(tr, string(at.quote), map[byte]string{'"': "&quot;", '\'': "&#039;"}[at.quote])
		} else {
			tr = `"` + strings.ReplaceAll(tr, `"`, "&quot;") + `"`
		}
		b.WriteString(tag[last:at.valStart])
		b.WriteString(tr)
		last = at.valEnd
	}
	if last == 0 {
		return tag
	}
	b.WriteString(tag[last:])
	return b.String()
}

// Printf patterns ------------------------------------------------------------------------

type pattern struct {
	re    *regexp.Regexp
	args  []int // argument number of each capture group (1-based)
	forms entry // the translation; several forms for a plural message
	rule  *pluralRule
}

var placeholderRe = regexp.MustCompile(`%(?:(\d+)\$)?[-+ 0#']*\d*(?:\.\d+)?([sdfuFxXeEgGc%])`)

func (c *Catalog) buildPatterns() {
	c.patterns, c.markup = map[string][]*pattern{}, map[string][]*pattern{}
	seen := map[string]bool{}
	add := func(msgid string, e entry, rule *pluralRule) {
		if _, id, ok := strings.Cut(msgid, ctxSep); ok {
			msgid = id
		}
		if seen[msgid] {
			return
		}
		markup := strings.Contains(msgid, "<")
		if !strings.Contains(msgid, "%") && !markup {
			return
		}
		seen[msgid] = true
		if p, key := compilePattern(msgid, markup); p != nil {
			p.forms, p.rule = e, rule
			if markup {
				c.markup[key] = append(c.markup[key], p)
			} else {
				c.patterns[key] = append(c.patterns[key], p)
			}
		}
	}
	for _, l := range c.layers {
		for msgid, e := range l.mo.entries {
			add(msgid, e, l.mo.plural)
		}
		for pl, key := range l.mo.plurals {
			add(pl, l.mo.entries[key], l.mo.plural)
		}
	}
}

// compilePattern turns "%1$s of %2$s" into ^(.+?) of (.+?)$ and returns it with the first word
// of its longest literal (the lookup key). Patterns with too little literal text are skipped:
// "%s" alone would match everything.
func compilePattern(msgid string, raw bool) (*pattern, string) {
	unesc := html.UnescapeString
	if raw { // markup messages match the escaped HTML as written
		unesc = func(v string) string { return v }
	}
	quote := func(lit string) string {
		return strings.ReplaceAll(regexp.QuoteMeta(spaceRun.ReplaceAllString(unesc(lit), " ")), " ", `\s+`)
	}
	var re strings.Builder
	re.WriteString("^")
	var args []int
	var literals []string
	last, seq := 0, 0
	for _, m := range placeholderRe.FindAllStringSubmatchIndex(msgid, -1) {
		lit := msgid[last:m[0]]
		literals = append(literals, lit)
		re.WriteString(quote(lit))
		last = m[1]
		if msgid[m[4]:m[5]] == "%" {
			re.WriteString("%")
			continue
		}
		seq++
		n := seq
		if m[2] >= 0 {
			n, _ = strconv.Atoi(msgid[m[2]:m[3]])
		}
		args = append(args, n)
		if strings.ContainsAny(msgid[m[4]:m[5]], "dfuF") {
			re.WriteString(`([\d.,\s\x{00a0}]+?)`)
		} else {
			re.WriteString("(.+?)")
		}
	}
	if len(args) == 0 {
		return nil, ""
	}
	literals = append(literals, msgid[last:])
	re.WriteString(quote(msgid[last:]))
	re.WriteString("$")
	longest := ""
	letters := 0
	for _, l := range literals {
		l = html.UnescapeString(stripTags(l))
		for _, r := range l {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if len(strings.TrimSpace(l)) > len(strings.TrimSpace(longest)) {
			longest = l
		}
	}
	key := firstWord(longest)
	if letters < 4 || key == "" {
		return nil, ""
	}
	r, err := regexp.Compile(re.String())
	if err != nil {
		return nil, ""
	}
	return &pattern{re: r, args: args}, key
}

func firstWord(s string) string {
	f := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range f {
		if len(w) >= 2 {
			return strings.ToLower(w)
		}
	}
	return ""
}

func stripTags(s string) string {
	return tagRe.ReplaceAllString(s, " ")
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func (c *Catalog) matchPattern(text string) (string, bool) {
	c.patOnce.Do(c.buildPatterns)
	return matchIn(c.patterns, text, false)
}

// matchMarkup matches inner HTML against the messages that contain markup.
func (c *Catalog) matchMarkup(inner string) (string, bool) {
	c.patOnce.Do(c.buildPatterns)
	return matchIn(c.markup, inner, true)
}

func matchIn(index map[string][]*pattern, text string, raw bool) (string, bool) {
	words := text
	if raw {
		words = stripTags(text)
	}
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(words, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		w = strings.ToLower(w)
		if seen[w] {
			continue
		}
		seen[w] = true
		for _, p := range index[w] {
			m := p.re.FindStringSubmatch(text)
			if m == nil {
				continue
			}
			if !raw && sentenceArg(m[1:]) {
				continue
			}
			vals := map[int]string{}
			count := int64(-1)
			for i, n := range p.args {
				vals[n] = m[i+1]
				if count < 0 {
					if v, err := strconv.ParseInt(strings.NewReplacer(",", "", ".", "", " ", "", "\u00a0", "").Replace(m[i+1]), 10, 64); err == nil {
						count = v
					}
				}
			}
			msgstr := p.forms[0]
			if len(p.forms) > 1 && count >= 0 {
				if k := p.rule.index(count); k >= 0 && k < len(p.forms) && p.forms[k] != "" {
					msgstr = p.forms[k]
				}
			}
			return fillPlaceholders(msgstr, vals, raw), true
		}
	}
	return "", false
}

var sentenceEnd = regexp.MustCompile(`[.!?]\s+\p{Lu}|[.!?]$`)

// sentenceArg reports whether a captured argument is prose rather than a name or number:
// "Customize %s" must not take "Customize WordPress with powerful, professional and intuitive
// fields: … PRO." apart, leaving a translated fragment around English text.
func sentenceArg(args []string) bool {
	for _, a := range args {
		if len(strings.Fields(a)) >= 6 && sentenceEnd.MatchString(strings.TrimSpace(a)) {
			return true
		}
	}
	return false
}

// fillPlaceholders writes the captured arguments into a translation's placeholders.
func fillPlaceholders(msgstr string, vals map[int]string, raw bool) string {
	seq := 0
	return placeholderRe.ReplaceAllStringFunc(msgstr, func(ph string) string {
		m := placeholderRe.FindStringSubmatch(ph)
		if m[2] == "%" {
			return "%"
		}
		seq++
		n := seq
		if m[1] != "" {
			n, _ = strconv.Atoi(m[1])
		}
		if v, ok := vals[n]; ok {
			if raw {
				return v
			}
			return html.UnescapeString(v)
		}
		return ph
	})
}

// Tag scanning --------------------------------------------------------------------------

type attr struct {
	name             string
	valStart, valEnd int // byte offsets of the value inside the tag, -1 without a value
	quote            byte
	value            string
}

type attrList []attr

func (a attrList) get(name string) (string, bool) {
	for _, at := range a {
		if at.name == name {
			return at.value, true
		}
	}
	return "", false
}

func quickEditData(a attrList) bool {
	id, _ := a.get("id")
	return strings.HasPrefix(id, "inline_")
}

func (a attrList) has(name string) bool {
	_, ok := a.get(name)
	return ok
}

func tagName(tag string) (string, bool) {
	j := 1
	closing := false
	if j < len(tag) && tag[j] == '/' {
		closing = true
		j++
	}
	s := j
	for j < len(tag) && (tag[j] >= 'a' && tag[j] <= 'z' || tag[j] >= 'A' && tag[j] <= 'Z' || tag[j] >= '0' && tag[j] <= '9' || tag[j] == '-') {
		j++
	}
	return strings.ToLower(tag[s:j]), closing
}

// scanAttrs returns the attributes of an opening tag with the positions of their values.
func scanAttrs(tag string) attrList {
	var out attrList
	j := 1
	for j < len(tag) && !isSpace(tag[j]) && tag[j] != '>' && tag[j] != '/' {
		j++
	}
	for j < len(tag) {
		for j < len(tag) && (isSpace(tag[j]) || tag[j] == '/') {
			j++
		}
		if j >= len(tag) || tag[j] == '>' {
			break
		}
		ns := j
		for j < len(tag) && !isSpace(tag[j]) && tag[j] != '=' && tag[j] != '>' {
			j++
		}
		a := attr{name: strings.ToLower(tag[ns:j]), valStart: -1, valEnd: -1}
		for j < len(tag) && isSpace(tag[j]) {
			j++
		}
		if j < len(tag) && tag[j] == '=' {
			j++
			for j < len(tag) && isSpace(tag[j]) {
				j++
			}
			if j < len(tag) && (tag[j] == '"' || tag[j] == '\'') {
				q := tag[j]
				k := strings.IndexByte(tag[j+1:], q)
				if k < 0 {
					break
				}
				a.quote, a.valStart, a.valEnd, a.value = q, j+1, j+1+k, tag[j+1:j+1+k]
				j += k + 2
			} else {
				vs := j
				for j < len(tag) && !isSpace(tag[j]) && tag[j] != '>' {
					j++
				}
				a.valStart, a.valEnd, a.value = vs, j, tag[vs:j]
			}
		}
		if a.name != "" {
			out = append(out, a)
		}
	}
	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// tagEndAt returns the index of the '>' closing the tag at s[i] (quotes skipped), or -1.
func tagEndAt(s string, i int) int {
	inQ := byte(0)
	for j := i + 1; j < len(s); j++ {
		ch := s[j]
		switch {
		case inQ != 0:
			if ch == inQ {
				inQ = 0
			}
		case ch == '"' || ch == '\'':
			// Quotes only open inside an attribute value (after '=').
			k := j - 1
			for k > i && isSpace(s[k]) {
				k--
			}
			if s[k] == '=' {
				inQ = ch
			}
		case ch == '>':
			return j
		}
	}
	return -1
}

// indexClose finds the closing tag of a raw-text element from position i.
func indexClose(s string, i int, name string) int {
	lc := s[i:] // templates write lowercase tags
	k := 0
	for {
		m := strings.Index(lc[k:], "</"+name)
		if m < 0 {
			return -1
		}
		k += m
		after := k + 2 + len(name)
		if after >= len(lc) || lc[after] == '>' || isSpace(lc[after]) {
			return i + k
		}
		k = after
	}
}
