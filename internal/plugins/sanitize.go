package plugins

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"wpgo/internal/wp"
)

// Output sanitising. A plugin with content:filter changes what visitors see; without the
// content:html permission whatever HTML it adds goes through KSES (wp_kses_post, the rules for a
// user without unfiltered_html), so a plugin cannot put a script, an event handler or an iframe on
// the page, where it would also run for logged-in administrators.
//
// Shortcode output is the plugin's alone and is sanitised as a whole. A content filter receives
// the core's rendered HTML, which may legitimately hold markup KSES removes (contact forms,
// embeds, store widgets), so only the part the plugin changed is sanitised: the common prefix and
// suffix of the value before and after the filter are kept as they are, widened so that the
// changed part never starts or ends inside a tag. When the original value is still there as a
// whole (the plugin added something above and below it, or wrapped it), only what was added
// around it is sanitised, so the core's markup is never run through KSES.
//
// Where a tag starts and ends is decided by the HTML tokenizer of golang.org/x/net/html, which
// reads markup the way a browser does: a '>' or '<' inside a quoted attribute value neither ends
// nor starts a tag, and the content of script, style, textarea, title and the other raw-text
// elements is not markup.

// sanitizeHTML is the sanitiser (a variable for tests).
var sanitizeHTML = wp.KsesPost

// sanitizeShortcode sanitises a shortcode's output unless the plugin may print raw HTML.
func (p *Plugin) sanitizeShortcode(out string) string {
	if p.perms.Has(PermContentHTML) || out == "" {
		return out
	}
	return sanitizeHTML(out)
}

// sanitizeFilter sanitises what a content filter changed (hook values that are strings, for the
// hooks content:filter guards) unless the plugin may print raw HTML.
func (p *Plugin) sanitizeFilter(hook string, before, after any) any {
	if p.perms.Has(PermContentHTML) || HookPermission(hook) != PermContentFilter {
		return after
	}
	in, ok1 := before.(string)
	out, ok2 := after.(string)
	if !ok1 || !ok2 || in == out {
		return after
	}
	clean, ok := sanitizeChange(in, out)
	if !ok {
		p.logs.add("warning", "runtime", hook+": a change inside a tag, script, style or comment was dropped (needs the content:html permission)")
	}
	return clean
}

// sanitizeChange returns out with the part that differs from in passed through the sanitiser. ok
// is false when the change sits inside raw text or a comment and was therefore dropped (in is
// returned).
func sanitizeChange(in, out string) (string, bool) {
	if in == out {
		return out, true
	}
	inToks := htmlTokens(in)
	if in != "" && inToks.endsInData(in) {
		if i := strings.Index(out, in); i >= 0 {
			return sanitizeAround(in, out[:i], out[i+len(in):])
		}
	}
	pre := 0
	for pre < len(in) && pre < len(out) && in[pre] == out[pre] {
		pre++
	}
	suf := 0
	for suf < len(in)-pre && suf < len(out)-pre && in[len(in)-1-suf] == out[len(out)-1-suf] {
		suf++
	}
	// The kept prefix must end where the browser reads text, outside any tag: move it back to the
	// start of the tag it cuts into. Inside raw text or a comment the change is dropped.
	pre, ok := htmlTokens(out).headCut(out, pre)
	if !ok {
		return in, false
	}
	// The kept suffix must start where the original was read as text outside any tag, comment or
	// raw text, so that it is read the same way after the sanitised part: move it forward past
	// the construct it starts in. It is the original's markup, so the original's tokens decide.
	suf = len(in) - inToks.tailCut(in, len(in)-suf)
	if suf > len(out)-pre {
		suf = len(out) - pre
	}
	mid := sanitizeHTML(out[pre : len(out)-suf])
	if t := htmlTokens(mid); !t.endsInData(mid) {
		// Never let the sanitised part leave the browser inside a tag the suffix would complete.
		return in, false
	}
	return out[:pre] + mid + out[len(out)-suf:], true
}

// sanitizeAround sanitises what a filter put before and after the unchanged original value in,
// as one fragment so that a wrapper (<div>…</div>) stays intact. The original is kept out of the
// sanitiser through a random marker, and is put back only where the marker ends up as text; when
// it does not (the plugin opened a tag, an attribute, a comment or a script around it) the
// change is dropped.
func sanitizeAround(in, before, after string) (string, bool) {
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])
	mark := "wpgokeep" + hex.EncodeToString(nonce[:])
	clean := sanitizeHTML(before + mark + after)
	i := strings.Index(clean, mark)
	if i < 0 || strings.Count(clean, mark) != 1 {
		return in, false
	}
	if !htmlTokens(clean).plainText(clean, i, i+len(mark)) {
		return in, false
	}
	return clean[:i] + in + clean[i+len(mark):], true
}

// rawTextElements are the elements whose content the tokenizer, like the browser, reads as text
// up to their end tag: the set golang.org/x/net/html switches to raw text for.
var rawTextElements = map[string]bool{"iframe": true, "noembed": true, "noframes": true, "noscript": true,
	"plaintext": true, "script": true, "style": true, "textarea": true, "title": true, "xmp": true}

// htmlTok is one token of an HTML fragment: its byte range, its type, and whether it starts inside
// a raw-text element (the element's text, or the end tag that closes it). typ is html.ErrorToken
// for a construct the input ends in the middle of (a tag without its '>').
type htmlTok struct {
	start, end int
	typ        html.TokenType
	raw        bool
}

// htmlToks are the tokens of a fragment, in order and without gaps, and whether the fragment ends
// inside a raw-text element.
type htmlToks struct {
	toks   []htmlTok
	eofRaw bool
}

// htmlTokens tokenizes s as a browser does. The tokenizer does not know the tree builder's
// context, so the content of a raw-text element is raw text even inside SVG, which only makes the
// callers more careful.
func htmlTokens(s string) htmlToks {
	var t htmlToks
	z := html.NewTokenizer(strings.NewReader(s))
	off := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		n := len(z.Raw())
		t.toks = append(t.toks, htmlTok{start: off, end: off + n, typ: tt, raw: t.eofRaw})
		off += n
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			if name, _ := z.TagName(); rawTextElements[string(name)] {
				t.eofRaw = true // its content, up to and including the start of its end tag
			}
		case html.EndTagToken:
			t.eofRaw = false // in raw text the tokenizer returns no end tag but the closing one
		}
	}
	if off < len(s) { // the input ends inside a tag: the browser drops it
		t.toks = append(t.toks, htmlTok{start: off, end: len(s), typ: html.ErrorToken, raw: t.eofRaw})
	}
	return t
}

// at returns the token that holds byte p (start <= p < end), or nil at the end of the input.
func (t htmlToks) at(p int) *htmlTok {
	i := sort.Search(len(t.toks), func(i int) bool { return t.toks[i].end > p })
	if i < len(t.toks) && t.toks[i].start <= p {
		return &t.toks[i]
	}
	return nil
}

// rawAt reports whether the input at p (or its end) is inside a raw-text element.
func (t htmlToks) rawAt(p int) bool {
	if tok := t.at(p); tok != nil {
		return tok.raw
	}
	return t.eofRaw
}

// textEnd moves p, a place in the text token starting at start, back over a trailing "<" or
// "</": after them the browser is reading a tag, and whatever follows decides which.
func textEnd(s string, start, p int) int {
	for p > start {
		switch {
		case s[p-1] == '<':
			p--
		case p-2 >= start && s[p-2:p] == "</":
			p -= 2
		default:
			return p
		}
	}
	return p
}

// headCut returns the largest q <= p at which s[:q] leaves the browser reading text outside any
// tag. ok is false when that place is inside raw text (a script, a style, a textarea …) or a
// comment, where nothing can be inserted as HTML.
func (t htmlToks) headCut(s string, p int) (int, bool) {
	// tok is the token s[:p] ends in (start < p <= end). Ending exactly after a whole tag, comment
	// or doctype is fine; inside one, or after text (which may end in "<"), is looked at.
	if tok := t.at(p - 1); p > 0 && tok != nil && (p < tok.end || tok.typ == html.TextToken || tok.typ == html.ErrorToken) {
		switch {
		case tok.raw || tok.typ == html.CommentToken:
			return 0, false
		case tok.typ == html.TextToken:
			p = textEnd(s, tok.start, p)
		default: // a tag, a doctype or a cut-off tag
			p = tok.start
		}
	}
	if t.rawAt(p) {
		return 0, false
	}
	return p, true
}

// tailCut returns the smallest q >= p from which s[q:] is read on its own as it is after s[:q]:
// outside tags, comments and raw text.
func (t htmlToks) tailCut(s string, p int) int {
	for p < len(s) {
		tok := t.at(p)
		switch {
		case tok == nil:
			return p
		case tok.raw: // past the raw text and the end tag that closes it
			p = tok.end
		case tok.start == p, tok.typ == html.TextToken:
			return p
		default: // inside a tag, a comment, a doctype or a cut-off tag
			p = tok.end
		}
	}
	return len(s)
}

// plainText reports whether s[i:j] lies inside one text token that is not raw text, so that what
// replaces it is read as markup from the start.
func (t htmlToks) plainText(s string, i, j int) bool {
	tok := t.at(i)
	return tok != nil && tok.typ == html.TextToken && !tok.raw && j <= tok.end && textEnd(s, tok.start, i) == i
}

// endsInData reports whether s, read on its own, ends outside any tag, comment or raw text, so that
// whatever follows it is read as markup from the start.
func (t htmlToks) endsInData(s string) bool {
	if t.eofRaw {
		return false
	}
	if len(t.toks) == 0 {
		return true
	}
	switch last := t.toks[len(t.toks)-1]; last.typ {
	case html.ErrorToken:
		return false
	case html.TextToken:
		return textEnd(s, last.start, len(s)) == len(s)
	case html.CommentToken, html.DoctypeToken:
		return s[last.end-1] == '>' // a comment the input ends in goes on into what follows
	}
	return true
}
