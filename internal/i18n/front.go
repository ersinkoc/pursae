package i18n

import (
	"bytes"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Front-end string translation (Pursae Multi Language) ----------------------------------------
//
// TranslateFront translates the text of a rendered front-end page with the site's own string
// table: text between tags and the user-facing attributes (alt, title, placeholder, aria-label,
// aria-description, the value of submit/button/reset inputs). Unlike TranslateHTML (the admin)
// it matches whole strings only: the trimmed text with its whitespace runs collapsed and its
// entities decoded must be a key of the table. No printf patterns, dates or separator parts:
// those heuristics are tuned for WordPress' own messages and would mistranslate site text.
//
// Never touched: scripts, styles, <textarea>, <pre>, <code>, <template>, <svg>, <math>, comments,
// subtrees marked translate="no", and user content between the sentinels <!--pll:uc--> and
// <!--/pll:uc--> (posts are translated per post, not as strings). Bytes outside replaced spans are
// copied as they are.

// UCOpen and UCClose are the sentinels around user content (post content, titles, comments, Web
// Builder snapshots). UCOpenPost marks the content of one post: <!--pll:uc post=123-->.
const (
	UCOpen  = "<!--pll:uc-->"
	UCClose = "<!--/pll:uc-->"
)

// UCOpenPost is the opening sentinel of the content of post id.
func UCOpenPost(id int64) string {
	return "<!--pll:uc post=" + strconv.FormatInt(id, 10) + "-->"
}

// TranslateFront returns doc with every string lookup knows replaced by its translation. lookup
// gets the plain (entity-decoded, whitespace-normalised) text and returns plain text; it may be
// nil (nothing is replaced). visit, when not nil, is called for every translatable string with
// the plain text and the attribute name ("" for a text node), whether or not it was translated;
// a non-empty marker it returns wraps the text node as <pll-t data-pll-s="MARKER">…</pll-t>, or
// adds data-pll-a-<attr>="MARKER" to the element (visual translation mode only). Text inside
// <title> and <option> is translated but never wrapped.
func TranslateFront(doc []byte, lookup func(s string) (string, bool), visit func(s string, attr string) (marker string)) []byte {
	var lk func(string, bool) (string, bool)
	if lookup != nil {
		lk = func(s string, _ bool) (string, bool) { return lookup(s) }
	}
	return TranslateFrontWith(doc, lk, visit)
}

// TranslateFrontWith is TranslateFront with a lookup that is told whether the string is page text
// worth recording (record): false for the document <title>, which joins a post title with the
// site name (user content no string table should collect). visit is never called for <title>.
func TranslateFrontWith(doc []byte, lookup func(s string, record bool) (string, bool), visit func(s string, attr string) (marker string)) []byte {
	if len(doc) == 0 || (lookup == nil && visit == nil) {
		return doc
	}
	w := frontWalker{src: string(doc), lookup: lookup, visit: visit}
	w.out.Grow(len(doc) + len(doc)/16)
	w.run()
	return []byte(w.out.String())
}

type frontWalker struct {
	src    string
	out    strings.Builder
	lookup func(string, bool) (string, bool)
	visit  func(string, string) string
}

// frontRaw are elements whose content is copied through untouched.
var frontRaw = map[string]bool{"script": true, "style": true, "textarea": true, "pre": true, "code": true, "template": true,
	"iframe": true, "noembed": true, "noframes": true, "xmp": true}

// frontSkip are elements whose whole subtree (attributes of descendants included) is left alone.
var frontSkip = map[string]bool{"svg": true, "math": true}

// frontAttrs are the attributes that carry user-facing text.
var frontAttrs = map[string]bool{"alt": true, "title": true, "placeholder": true, "aria-label": true, "aria-description": true}

// frontNoAttrs are elements whose attributes are not page text (or not in the body).
var frontNoAttrs = map[string]bool{"html": true, "head": true, "meta": true, "link": true, "base": true, "script": true, "style": true}

func (w *frontWalker) run() {
	src := w.src
	skipName, skipDepth := "", 0
	lastOpen := "" // the last opening tag: a text node right after <option> is never wrapped
	textStart := 0
	i := 0
	flush := func(end int) {
		if end > textStart {
			if skipDepth > 0 {
				w.out.WriteString(src[textStart:end])
			} else {
				w.text(src[textStart:end], lastOpen != "option", true)
			}
		}
	}
	for i < len(src) {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			break
		}
		i += lt
		// A '<' that does not start a tag, a comment or a declaration is text.
		if i+1 >= len(src) || !(isTagStart(src[i+1])) {
			i++
			continue
		}
		flush(i)
		if strings.HasPrefix(src[i:], "<!--") {
			end := w.commentEnd(i)
			w.out.WriteString(src[i:end])
			i, textStart = end, end
			continue
		}
		gt := tagEndAt(src, i)
		if gt < 0 {
			w.out.WriteString(src[i:])
			i, textStart = len(src), len(src)
			break
		}
		tag := src[i : gt+1]
		name, closing := tagName(tag)
		i, textStart = gt+1, gt+1
		if name == "" { // <!DOCTYPE …>, <?…>
			w.out.WriteString(tag)
			continue
		}
		if closing {
			w.out.WriteString(tag)
			if skipDepth > 0 && name == skipName {
				skipDepth--
			}
			if name == lastOpen || name == "select" {
				lastOpen = ""
			}
			continue
		}
		selfClosing := strings.HasSuffix(tag, "/>") || voidElements[name]
		if skipDepth > 0 {
			if name == skipName && !selfClosing {
				skipDepth++
			}
			w.out.WriteString(tag)
			continue
		}
		attrs := scanAttrs(tag)
		if v, ok := attrs.get("translate"); (ok && strings.EqualFold(strings.TrimSpace(v), "no")) || frontSkip[name] {
			w.out.WriteString(tag)
			if !selfClosing {
				skipName, skipDepth = name, 1
			}
			continue
		}
		if frontNoAttrs[name] {
			w.out.WriteString(tag)
		} else {
			w.out.WriteString(w.attrs(tag, name, attrs))
		}
		if !selfClosing {
			lastOpen = name
		}
		if selfClosing {
			continue
		}
		if name == "title" { // RCDATA: its text is one string, never wrapped
			end := indexClose(src, i, "title")
			if end < 0 {
				continue
			}
			w.text(src[i:end], false, false)
			i, textStart = end, end
			continue
		}
		if frontRaw[name] {
			end := indexClose(src, i, name)
			if end < 0 {
				w.out.WriteString(src[i:])
				i, textStart = len(src), len(src)
				break
			}
			w.out.WriteString(src[i:end])
			i, textStart = end, end
		}
	}
	if textStart < len(src) {
		if skipDepth > 0 {
			w.out.WriteString(src[textStart:])
		} else {
			w.text(src[textStart:], lastOpen != "option", true)
		}
	}
}

func isTagStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '/' || c == '!' || c == '?'
}

// commentEnd returns the index just past the comment at src[i]. A user content sentinel spans
// to its matching closing sentinel (sentinels nest), so the whole region is copied as it is; an
// unclosed one runs to the end of the document.
func (w *frontWalker) commentEnd(i int) int {
	src := w.src
	if strings.HasPrefix(src[i:], "<!--pll:uc") {
		depth := 0
		k := i
		for k < len(src) {
			o := strings.Index(src[k:], "<!--pll:uc")
			c := strings.Index(src[k:], UCClose)
			if c < 0 {
				return len(src)
			}
			if o >= 0 && o < c {
				depth++
				k += o + len("<!--pll:uc")
				continue
			}
			depth--
			k += c + len(UCClose)
			if depth == 0 {
				return k
			}
		}
		return len(src)
	}
	end := strings.Index(src[i+4:], "-->")
	if end < 0 {
		return len(src)
	}
	return i + 4 + end + 3
}

// frontKey is the lookup key of escaped HTML text: entities decoded, whitespace runs collapsed.
func frontKey(t string) string {
	return strings.TrimSpace(spaceRun.ReplaceAllString(html.UnescapeString(t), " "))
}

// text translates one text node, keeping its surrounding whitespace. rec false (the document
// <title>): translated, but neither recorded nor visited.
func (w *frontWalker) text(t string, wrap, rec bool) {
	core := strings.TrimSpace(t)
	if core == "" || !hasLetter(core) {
		w.out.WriteString(t)
		return
	}
	key := frontKey(core)
	if key == "" || !hasLetter(key) {
		w.out.WriteString(t)
		return
	}
	repl := core
	if w.lookup != nil {
		if tr, ok := w.lookup(key, rec); ok && tr != "" {
			repl = escapeText(tr)
		}
	}
	marker := ""
	if w.visit != nil && rec {
		marker = w.visit(key, "")
	}
	lead := t[:strings.Index(t, core)]
	w.out.WriteString(lead)
	if marker != "" && wrap {
		w.out.WriteString(`<pll-t data-pll-s="` + html.EscapeString(marker) + `">` + repl + `</pll-t>`)
	} else {
		w.out.WriteString(repl)
	}
	w.out.WriteString(t[len(lead)+len(core):])
}

// attrs translates the user-facing attributes of an opening tag and adds the visual markers.
func (w *frontWalker) attrs(tag, name string, a attrList) string {
	button := false
	if name == "input" {
		typ, _ := a.get("type")
		switch strings.ToLower(strings.TrimSpace(typ)) {
		case "submit", "button", "reset":
			button = true
		}
	}
	var b strings.Builder
	var markers strings.Builder
	last := 0
	for _, at := range a {
		if at.valStart < 0 || !(frontAttrs[at.name] || at.name == "value" && button) {
			continue
		}
		key := frontKey(tag[at.valStart:at.valEnd])
		if key == "" || !hasLetter(key) {
			continue
		}
		if w.visit != nil {
			if m := w.visit(key, at.name); m != "" {
				markers.WriteString(` data-pll-a-` + at.name + `="` + html.EscapeString(m) + `"`)
			}
		}
		if w.lookup == nil {
			continue
		}
		tr, ok := w.lookup(key, true)
		if !ok || tr == "" {
			continue
		}
		val := escapeText(tr)
		switch at.quote {
		case '"':
		case '\'':
			val = strings.ReplaceAll(val, "'", "&#039;")
		default:
			val = `"` + val + `"`
		}
		b.WriteString(tag[last:at.valStart])
		b.WriteString(val)
		last = at.valEnd
	}
	if last == 0 && markers.Len() == 0 {
		return tag
	}
	b.WriteString(tag[last:])
	out := b.String()
	if markers.Len() > 0 {
		cut := len(out) - 1
		if strings.HasSuffix(out, "/>") {
			cut = len(out) - 2
		}
		out = out[:cut] + markers.String() + out[cut:]
	}
	return out
}

// ucSentinel matches a user content sentinel, also in the escaped forms a template may have
// written it in (an attribute: &lt;!--pll:uc--&gt;; a script string: <!--pll:uc-->).
var ucSentinel = regexp.MustCompile(`(?i)(?:<|&lt;|&#0*60;|&#x0*3c;|\\u003c|\\x3c)!--(\\?/)?pll:uc(?:\s+post(?:=|\\u003d|&#0*61;|&#x0*3d;)(\d+))?\s*--(?:>|&gt;|&#0*62;|&#x0*3e;|\\u003e|\\x3e)`)

var ucMarker = []byte("pll:uc")

// HasUserContentSentinels reports whether doc may hold a sentinel (a cheap check).
func HasUserContentSentinels(doc []byte) bool {
	return bytes.Contains(doc, ucMarker)
}

// StripUserContentSentinels removes every user content sentinel from doc.
func StripUserContentSentinels(doc []byte) []byte {
	return ReplaceUserContentSentinels(doc, nil)
}

// ReplaceUserContentSentinels replaces the sentinels written as HTML comments with what repl
// returns for them (open is false for a closing sentinel; post is the id of an opening post
// sentinel, else 0). Sentinels a template escaped are always removed. A nil repl removes all.
func ReplaceUserContentSentinels(doc []byte, repl func(open bool, post int64) string) []byte {
	if !HasUserContentSentinels(doc) {
		return doc
	}
	// The regexp runs only on a small window around each "pll:uc" (running it over the whole page
	// costs about as much as the string pass itself).
	var out []byte
	last, pos := 0, 0
	for pos < len(doc) {
		i := bytes.Index(doc[pos:], ucMarker)
		if i < 0 {
			break
		}
		i += pos
		ms, me, open, post, plain := ucPlainAt(doc, i, last)
		if ms < 0 {
			ws, we := max(last, i-ucWindowBefore), min(len(doc), i+ucWindowAfter)
			var hit []int
			for _, m := range ucSentinel.FindAllSubmatchIndex(doc[ws:we], -1) {
				if ws+m[0] <= i && i < ws+m[1] {
					hit = m
					break
				}
			}
			if hit == nil {
				pos = i + len(ucMarker)
				continue
			}
			ms, me = ws+hit[0], ws+hit[1]
			plain = doc[ms] == '<' && doc[me-1] == '>'
			open, post = hit[2] < 0, 0
			if hit[4] >= 0 {
				post, _ = strconv.ParseInt(string(doc[ws+hit[4]:ws+hit[5]]), 10, 64)
			}
		}
		if out == nil {
			out = make([]byte, 0, len(doc))
		}
		out = append(out, doc[last:ms]...)
		if repl != nil && plain {
			out = append(out, repl(open, post)...)
		}
		last, pos = me, me
	}
	if out == nil {
		return doc
	}
	return append(out, doc[last:]...)
}

// ucPlainAt parses the sentinel whose "pll:uc" is at doc[i] when it is written the way UCOpen,
// UCClose and UCOpenPost write it (the common case, no regexp); ms is -1 otherwise.
func ucPlainAt(doc []byte, i, lo int) (ms, me int, open bool, post int64, plain bool) {
	open = true
	ms = i - 4
	if ms >= lo && i > 0 && doc[i-1] == '/' {
		open, ms = false, i-5
	}
	if ms < lo || string(doc[ms:ms+4]) != "<!--" {
		return -1, 0, false, 0, false
	}
	k := i + len(ucMarker)
	if open && len(doc)-k > 6 && string(doc[k:k+6]) == " post=" {
		d := k + 6
		for d < len(doc) && doc[d] >= '0' && doc[d] <= '9' {
			d++
		}
		if d == k+6 || d-(k+6) > 18 {
			return -1, 0, false, 0, false
		}
		post, _ = strconv.ParseInt(string(doc[k+6:d]), 10, 64)
		k = d
	}
	if len(doc)-k < 3 || string(doc[k:k+3]) != "-->" {
		return -1, 0, false, 0, false
	}
	return ms, k + 3, open, post, true
}

// The window searched for a sentinel around its "pll:uc": the longest escaped "<!--" and "/"
// before it, a post id and the escaped "-->" after it.
const (
	ucWindowBefore = 48
	ucWindowAfter  = 96
)
