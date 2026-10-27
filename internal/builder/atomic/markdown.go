package atomic

// The markdown pipeline the atomic accordion's FAQ schema is built from (upstream 4.3:
// Element_Base / Widget_Base / atomic elements' render_markdown(), modules/markdown-render
// Html_To_Markdown, and Atomic_Accordion::strip_markdown_syntax).

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

// RenderMarkdown mirrors render_markdown() of an atomic element.
func (e *Env) RenderMarkdown(n *Node) string {
	et := Lookup(n.Type())
	if et == nil {
		return ""
	}
	settings := func() *Map { return e.atomicSettings(n, et) }
	switch n.Type() {
	case "e-heading":
		s := settings()
		title := stripTags(phpString(s.Get("title")), "")
		if phpEmpty(title) {
			return ""
		}
		level := 2
		if t := phpString(s.Get("tag")); len(t) == 2 && t[0] == 'h' && t[1] >= '1' && t[1] <= '6' {
			level = int(t[1] - '0')
		}
		if href := phpString(get(s.Get("link"), "href")); href != "" {
			return strings.Repeat("#", level) + " [" + title + "](" + href + ")"
		}
		return strings.Repeat("#", level) + " " + title
	case "e-paragraph":
		c := phpString(settings().Get("paragraph"))
		if phpEmpty(c) {
			return ""
		}
		return HTMLToMarkdown(c)
	case "e-button":
		s := settings()
		text := stripTags(phpString(s.Get("text")), "")
		if phpEmpty(text) {
			return ""
		}
		if href := phpString(get(s.Get("link"), "href")); href != "" {
			return "[" + text + "](" + href + ")"
		}
		return "**" + text + "**"
	case "e-image":
		img := asMap(settings().Get("image"))
		src := phpString(get(img, "src"))
		if m := asMap(get(img, "src")); m != nil {
			src = phpString(m.Get("url"))
		}
		if phpEmpty(src) {
			return ""
		}
		return "![" + phpString(get(img, "alt")) + "](" + src + ")"
	case "e-youtube":
		u := phpString(settings().Get("source"))
		if phpEmpty(u) {
			return ""
		}
		return "[Video](" + u + ")"
	case "e-divider":
		return "---"
	case "e-form":
		return ""
	case "e-list":
		var lines []string
		for _, item := range n.Children {
			if item.Type() != "e-list-item" {
				continue
			}
			text := ""
			for _, c := range item.Children {
				if c.Type() == "e-list-item-content" {
					var parts []string
					for _, cc := range c.Children {
						if md := e.RenderMarkdown(cc); strings.TrimSpace(md) != "" {
							parts = append(parts, md)
						}
					}
					text = strings.Join(parts, " ")
					break
				}
			}
			if strings.TrimSpace(text) != "" {
				lines = append(lines, "- "+strings.TrimSpace(text))
			}
		}
		return strings.Join(lines, "\n")
	case "e-accordion":
		var blocks []string
		for _, item := range n.Children {
			if item.Type() != "e-accordion-item" {
				continue
			}
			title := strings.TrimSpace(e.accordionPart(item, "e-accordion-item-header", "e-accordion-item-title"))
			content := strings.TrimSpace(e.accordionPart(item, "", "e-accordion-item-content"))
			if title == "" && content == "" {
				continue
			}
			blocks = append(blocks, "### "+title+"\n\n"+content)
		}
		return strings.Join(blocks, "\n\n")
	}
	if et.Widget { // Widget_Base::render_markdown: the rendered content, converted
		out := e.Render(n)
		if strings.TrimSpace(out) == "" {
			return ""
		}
		return HTMLToMarkdown(out)
	}
	var parts []string
	for _, c := range n.Children {
		if md := e.RenderMarkdown(c); strings.TrimSpace(md) != "" {
			parts = append(parts, md)
		}
	}
	return strings.Join(parts, "\n\n")
}

// accordionPart is the markdown of an accordion item's sub-element (looked up through via when set).
func (e *Env) accordionPart(item *Node, via, target string) string {
	scope := item
	if via != "" {
		scope = nil
		for _, c := range item.Children {
			if c.Type() == via {
				scope = c
				break
			}
		}
		if scope == nil {
			return ""
		}
	}
	for _, c := range scope.Children {
		if c.Type() == target {
			return e.RenderMarkdown(c)
		}
	}
	return ""
}

// faqText mirrors Atomic_Accordion::get_faq_item_text.
func (e *Env) faqText(item *Node, via, target string) string {
	return strings.TrimSpace(stripTags(stripMarkdownSyntax(e.accordionPart(item, via, target)), ""))
}

var (
	mdFenced     = regexp.MustCompile("(?s)```[^\\n`]*\\n(.*?)\\n```")
	mdHeading    = regexp.MustCompile(`(?m)^#{1,6}[ \t]+`)
	mdList       = regexp.MustCompile(`(?m)^[ \t]*(?:[-*+]|\d+\.)[ \t]+`)
	mdQuote      = regexp.MustCompile(`(?m)^(?:>[ \t]?)+`)
	mdStrike     = regexp.MustCompile(`(?s)~~(.+?)~~`)
	mdUnescape   = regexp.MustCompile("\\\\([\\\\`*_\\[\\]])")
	mdSpaces     = regexp.MustCompile(`[ \t]+`)
	mdNewlines   = regexp.MustCompile(`\s*\n\s*`)
	mdLinkTarget = regexp.MustCompile(`^\]\([^)]*\)`)
)

// stripMarkdownSyntax mirrors Atomic_Accordion::strip_markdown_syntax. Go's regexp has no
// lookbehind, so the "unescaped delimiter" rules are applied by a small scanner.
func stripMarkdownSyntax(md string) string {
	t := mdFenced.ReplaceAllString(md, "$1")
	t = replaceDelimited(t, "`", true)
	t = stripLinks(t, true)
	t = stripLinks(t, false)
	t = mdHeading.ReplaceAllString(t, "")
	t = mdList.ReplaceAllString(t, "")
	t = mdQuote.ReplaceAllString(t, "")
	t = replaceDelimited(t, "**", false)
	t = replaceDelimited(t, "__", false)
	t = replaceDelimited(t, "*", false)
	t = replaceDelimited(t, "_", false)
	t = mdStrike.ReplaceAllString(t, "$1")
	t = mdUnescape.ReplaceAllString(t, "$1")
	t = mdSpaces.ReplaceAllString(t, " ")
	t = mdNewlines.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

func escapedAt(s string, i int) bool { return i > 0 && s[i-1] == '\\' }

// replaceDelimited removes pairs of an unescaped delimiter, keeping the enclosed text (runs of
// the delimiter count as one when run is set, like `+ for code spans).
func replaceDelimited(s, d string, run bool) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], d) && !escapedAt(s, i) {
			open := i + len(d)
			if run {
				for open < len(s) && strings.HasPrefix(s[open:], d) {
					open += len(d)
				}
			}
			// find the closing unescaped delimiter (non-empty content)
			j := open + 1
			found := -1
			for j <= len(s)-len(d) {
				if strings.HasPrefix(s[j:], d) && !escapedAt(s, j) {
					found = j
					break
				}
				j++
			}
			if found > open || (found == open && run) {
				b.WriteString(s[open:found])
				end := found + len(d)
				if run {
					for end < len(s) && strings.HasPrefix(s[end:], d) {
						end += len(d)
					}
				}
				i = end
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// stripLinks turns [text](url) (or ![alt](src) when image) into its text.
func stripLinks(s string, image bool) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		start := i
		if image {
			if !(s[i] == '!' && i+1 < len(s) && s[i+1] == '[' && !escapedAt(s, i)) {
				b.WriteByte(s[i])
				i++
				continue
			}
			start = i + 1
		} else if s[i] != '[' || escapedAt(s, i) {
			b.WriteByte(s[i])
			i++
			continue
		}
		close := strings.IndexByte(s[start+1:], ']')
		if close >= 0 {
			cl := start + 1 + close
			if m := mdLinkTarget.FindString(s[cl:]); m != "" {
				b.WriteString(s[start+1 : cl])
				i = cl + len(m)
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// ---- Html_To_Markdown -----------------------------------------------------------------------------

var mdEscaper = strings.NewReplacer(`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`)

type mdListState struct {
	ol    bool
	index int
}

type mdConverter struct {
	out, inline           strings.Builder
	lists                 []mdListState
	quote                 int
	inPre, inCode, inLink bool
	pre, preLang          string
	href                  string
	link                  strings.Builder
	heading               int
}

var (
	mdCodeMarks  = regexp.MustCompile("(?s)`CODE_OPEN`(.*?)`CODE_CLOSE`")
	mdTrailingWS = regexp.MustCompile("[ \t]+\n")
	mdManyNL     = regexp.MustCompile(`\n{3,}`)
	mdCollapse   = regexp.MustCompile(`[ \t\n]+`)
	mdTicks      = regexp.MustCompile("`+")
	mdScriptTags = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>`)
	mdSafeScheme = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.\-]*):`)
)

// HTMLToMarkdown mirrors Html_To_Markdown::convert (tables are flattened to text).
func HTMLToMarkdown(src string) string {
	if src == "" {
		return ""
	}
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	src = mdScriptTags.ReplaceAllString(src, "")
	src = strings.ReplaceAll(src, "​", "")
	c := &mdConverter{}
	z := xhtml.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		tok := z.Token()
		switch tt {
		case xhtml.TextToken:
			c.text(tok.Data)
		case xhtml.StartTagToken:
			c.open(tok)
			if mdVoid[tok.Data] {
				c.close(tok.Data)
			}
		case xhtml.SelfClosingTagToken:
			c.open(tok)
			c.close(tok.Data)
		case xhtml.EndTagToken:
			c.close(tok.Data)
		}
	}
	c.flush()
	o := mdTrailingWS.ReplaceAllString(c.out.String(), "\n")
	o = mdManyNL.ReplaceAllString(o, "\n\n")
	return strings.TrimSpace(o)
}

var mdVoid = map[string]bool{"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true, "area": true, "base": true,
	"col": true, "embed": true, "param": true, "source": true, "track": true, "wbr": true}

var mdBlocks = map[string]bool{"p": true, "div": true, "section": true, "article": true, "header": true, "footer": true, "main": true,
	"aside": true, "figure": true, "table": true, "tr": true}

func attr(t xhtml.Token, k string) string {
	for _, a := range t.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func (c *mdConverter) escape(s string, code bool) string {
	s = html.UnescapeString(s)
	if code {
		return s
	}
	return mdEscaper.Replace(s)
}

func (c *mdConverter) text(t string) {
	if t == "" {
		return
	}
	if c.inPre {
		c.pre += t
		return
	}
	esc := c.escape(mdCollapse.ReplaceAllString(t, " "), c.inCode)
	if c.inLink {
		c.link.WriteString(esc)
		return
	}
	c.inline.WriteString(esc)
}

func (c *mdConverter) appendInline(m string) {
	if c.inLink {
		c.link.WriteString(m)
		return
	}
	c.inline.WriteString(m)
}

func (c *mdConverter) open(t xhtml.Token) {
	tag := t.Data
	switch {
	case mdBlocks[tag] || tag == "li":
		c.flush()
	case len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6':
		c.flush()
		c.heading = int(tag[1] - '0')
		c.inline.Reset()
		c.inline.WriteString(strings.Repeat("#", c.heading) + " ")
	}
	switch tag {
	case "br":
		if c.heading > 0 {
			c.inline.WriteString(" ")
		} else {
			c.inline.WriteString("\n")
		}
	case "hr":
		c.flush()
		c.out.WriteString("\n\n---\n\n")
	case "strong", "b":
		c.appendInline("**")
	case "em", "i":
		c.appendInline("*")
	case "del", "s", "strike":
		c.appendInline("~~")
	case "code":
		if c.inPre {
			if m := regexp.MustCompile(`language-([A-Za-z0-9_+\-]+)`).FindStringSubmatch(attr(t, "class")); m != nil {
				c.preLang = m[1]
			}
			return
		}
		c.inCode = true
		c.appendInline("`CODE_OPEN`")
	case "pre":
		c.flush()
		c.inPre, c.pre, c.preLang = true, "", ""
	case "a":
		c.inLink, c.href = true, attr(t, "href")
		c.link.Reset()
	case "img":
		src, alt := attr(t, "src"), attr(t, "alt")
		if src == "" || !mdSafeURL(src) {
			return
		}
		c.inline.WriteString("![" + c.escape(alt, false) + "](" + mdURL(src) + ")")
	case "ul", "ol":
		if len(c.lists) > 0 && strings.TrimSpace(c.inline.String()) != "" {
			c.listItem()
		} else {
			c.flush()
		}
		c.lists = append(c.lists, mdListState{ol: tag == "ol", index: 1})
	case "blockquote":
		c.flush()
		c.quote++
	case "td", "th":
		c.inline.WriteString(" ")
	}
}

func (c *mdConverter) close(tag string) {
	switch {
	case mdBlocks[tag]:
		c.flush()
		return
	case len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6':
		s := mdSpaces.ReplaceAllString(c.inline.String(), " ")
		c.inline.Reset()
		c.inline.WriteString(s)
		c.flush()
		c.heading = 0
		return
	}
	switch tag {
	case "strong", "b":
		c.appendInline("**")
	case "em", "i":
		c.appendInline("*")
	case "del", "s", "strike":
		c.appendInline("~~")
	case "code":
		if c.inPre {
			return
		}
		c.appendInline("`CODE_CLOSE`")
		c.inCode = false
	case "pre":
		content := strings.TrimRight(html.UnescapeString(c.pre), "\n")
		c.out.WriteString("\n\n```" + c.preLang + "\n" + content + "\n```\n\n")
		c.inPre, c.pre, c.preLang = false, "", ""
	case "a":
		text := c.link.String()
		if c.href == "" || c.href == "#" || !mdSafeURL(c.href) {
			c.inline.WriteString(text)
		} else {
			c.inline.WriteString("[" + text + "](" + mdURL(c.href) + ")")
		}
		c.inLink, c.href = false, ""
		c.link.Reset()
	case "ul", "ol":
		c.flush()
		if len(c.lists) > 0 {
			c.lists = c.lists[:len(c.lists)-1]
		}
		if len(c.lists) == 0 {
			c.out.WriteString("\n\n")
		}
	case "li":
		c.listItem()
	case "blockquote":
		c.flush()
		if c.quote > 0 {
			c.quote--
		}
		if c.quote == 0 {
			c.out.WriteString("\n")
		}
	}
}

func (c *mdConverter) prefixQuote(s string) string {
	if c.quote == 0 {
		return s
	}
	p := strings.Repeat("> ", c.quote)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = p + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (c *mdConverter) listItem() {
	content := strings.TrimSpace(c.inline.String())
	c.inline.Reset()
	if content == "" {
		return
	}
	if len(c.lists) == 0 {
		c.out.WriteString(content + "\n")
		return
	}
	top := &c.lists[len(c.lists)-1]
	indent := strings.Repeat("  ", len(c.lists)-1)
	marker := "- "
	if top.ol {
		marker = strconv.Itoa(top.index) + ". "
		top.index++
	}
	lines := strings.Split(content, "\n")
	r := indent + marker + lines[0]
	for _, l := range lines[1:] {
		r += "\n" + indent + "  " + l
	}
	c.out.WriteString(c.prefixQuote(r) + "\n")
}

func (c *mdConverter) flush() {
	if c.inline.Len() == 0 {
		return
	}
	content := mdCodeMarks.ReplaceAllStringFunc(c.inline.String(), func(m string) string {
		inner := mdCodeMarks.FindStringSubmatch(m)[1]
		inner = regexp.MustCompile(`\s+`).ReplaceAllString(inner, " ")
		max := 0
		for _, r := range mdTicks.FindAllString(inner, -1) {
			if len(r) > max {
				max = len(r)
			}
		}
		fence := strings.Repeat("`", max+1)
		pad := ""
		if inner != "" && (inner[0] == '`' || inner[len(inner)-1] == '`') {
			pad = " "
		}
		return fence + pad + inner + pad + fence
	})
	c.inline.Reset()
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if len(c.lists) > 0 {
		c.inline.WriteString(content)
		return
	}
	c.out.WriteString("\n\n" + c.prefixQuote(content) + "\n\n")
}

func mdURL(u string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(u)
}

func mdSafeURL(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if u[0] == '#' || u[0] == '/' || u[0] == '?' {
		return true
	}
	l := strings.ToLower(u)
	for _, p := range []string{"data:image/png", "data:image/jpeg", "data:image/jpg", "data:image/gif", "data:image/webp", "data:image/svg+xml"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	m := mdSafeScheme.FindStringSubmatch(u)
	if m == nil {
		return true
	}
	switch strings.ToLower(m[1]) {
	case "http", "https", "mailto", "tel", "ftp", "ftps":
		return true
	}
	return false
}
