package plugins

import (
	"strings"

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
// changed part never starts or ends inside a tag.

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
		p.logs.add("warning", "runtime", hook+": a change inside a script, style or comment was dropped (needs the content:html permission)")
	}
	return clean
}

// rawTextElements hold text the browser does not parse as HTML; a change inside one cannot be
// made safe by sanitising it as HTML.
var rawTextElements = []string{"script", "style", "textarea", "title", "noscript", "xmp", "iframe", "noembed", "noframes"}

// sanitizeChange returns out with the part that differs from in passed through the sanitiser. ok
// is false when the change sits inside raw text or a comment and was therefore dropped (in is
// returned).
func sanitizeChange(in, out string) (string, bool) {
	if in == out {
		return out, true
	}
	pre := 0
	for pre < len(in) && pre < len(out) && in[pre] == out[pre] {
		pre++
	}
	suf := 0
	for suf < len(in)-pre && suf < len(out)-pre && in[len(in)-1-suf] == out[len(out)-1-suf] {
		suf++
	}
	// The prefix must not end inside a tag: move it back to the tag's "<".
	if lt, gt := strings.LastIndexByte(out[:pre], '<'), strings.LastIndexByte(out[:pre], '>'); lt > gt {
		pre = lt
	}
	// The suffix must not start inside a tag: move it past the tag's ">".
	tail := out[len(out)-suf:]
	if gt := strings.IndexByte(tail, '>'); gt >= 0 {
		if lt := strings.IndexByte(tail, '<'); lt < 0 || gt < lt {
			suf -= gt + 1
		}
	}
	if suf > len(out)-pre {
		suf = len(out) - pre
	}
	head := out[:pre]
	if insideRawText(head) {
		return in, false
	}
	return head + sanitizeHTML(out[pre:len(out)-suf]) + out[len(out)-suf:], true
}

// insideRawText reports whether html ends inside a raw-text element or a comment.
func insideRawText(html string) bool {
	low := strings.ToLower(html)
	if open := strings.LastIndex(low, "<!--"); open >= 0 && strings.LastIndex(low, "-->") < open {
		return true
	}
	for _, el := range rawTextElements {
		open := strings.LastIndex(low, "<"+el)
		if open < 0 {
			continue
		}
		// "<titlex" is not "<title": the name must end there.
		if next := open + 1 + len(el); next < len(low) {
			if c := low[next]; c != '>' && c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '/' && c != '\f' {
				continue
			}
		}
		if strings.LastIndex(low, "</"+el) < open {
			return true
		}
	}
	return false
}
