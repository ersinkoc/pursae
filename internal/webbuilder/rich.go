package webbuilder

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Rich text: the upstream rich.ts subset kept in plain string fields (RichTextElements). Values
// without one of those tags are plain text; the renderers escape them. A rich value is
// re-sanitized on the server with the same rules as sanitizeRich(html, {lists: true}).

var (
	richRE      = regexp.MustCompile(`(?i)</?(b|strong|i|em|u|s|strike|del|code|a|br|ul|ol|li)\b[^>]*>`)
	richBr3RE   = regexp.MustCompile(`(<br>){3,}`)
	richLeadRE  = regexp.MustCompile(`^(<br>)+`)
	richTrailRE = regexp.MustCompile(`(<br>)+(</li>|</?(?:ul|ol)>|$)`)
	richTagRE   = regexp.MustCompile(`<(?:[^>"']|"[^"]*"|'[^']*')*>`)
	plainBrRE   = regexp.MustCompile(`(?i)<br\s*/?>`)
	plainLiRE   = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	plainListRE = regexp.MustCompile(`(?i)</(ul|ol)>`)
	plainNLRE   = regexp.MustCompile(`\n{3,}`)
)

var richDrop = toSet([]string{"script", "style", "template", "iframe", "frame", "object", "embed", "noscript", "head", "title", "meta", "link", "base", "svg", "math", "img", "picture", "video", "audio", "source", "canvas", "input", "button", "select", "textarea", "form", "option"})

var richMap = map[string]string{"b": "b", "strong": "b", "i": "i", "em": "i", "u": "u", "ins": "u", "s": "s", "strike": "s", "del": "s", "code": "code", "kbd": "code", "tt": "code", "samp": "code"}

var richBlock = toSet([]string{"p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "section", "article", "header", "footer", "aside", "nav", "main", "figure", "figcaption", "tr", "dt", "dd", "table"})

// IsRich reports whether v holds rich-text markup (upstream isRich).
func IsRich(v string) bool { return v != "" && richRE.MatchString(v) }

// SanitizeRich keeps b, i, u, s, code, a[href] (+target/rel for http links), br, ul, ol, li.
func SanitizeRich(src string) string {
	nodes, err := parseFragment(src)
	if err != nil {
		return ""
	}
	box := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range nodes {
		richClean(n, box, false, false, 0)
	}
	var b strings.Builder
	for c := box.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	out := strings.ReplaceAll(b.String(), "<br/>", "<br>")
	out = richBr3RE.ReplaceAllString(out, "<br><br>")
	out = richLeadRE.ReplaceAllString(out, "")
	for {
		next := richTrailRE.ReplaceAllString(out, "$2")
		if next == out {
			break
		}
		out = next
	}
	return out
}

// richClean walks one source node (and its subtree) into out.
func richClean(n *html.Node, out *html.Node, inLink, inList bool, depth int) {
	if depth > 64 {
		return
	}
	switch n.Type {
	case html.TextNode:
		if n.Data != "" {
			out.AppendChild(&html.Node{Type: html.TextNode, Data: n.Data})
		}
		return
	case html.ElementNode:
	default:
		return
	}
	tag := strings.ToLower(n.Data)
	if n.Namespace != "" || richDrop[tag] {
		return
	}
	kids := func(dst *html.Node, link, list bool) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			richClean(c, dst, link, list, depth+1)
		}
	}
	lastName := func() string {
		if out.LastChild == nil {
			return ""
		}
		if out.LastChild.Type != html.ElementNode {
			return "#text"
		}
		return out.LastChild.Data
	}
	brk := func() { out.AppendChild(el("br")) }
	switch {
	case tag == "br":
		brk()
	case tag == "a":
		href := ""
		if !inLink {
			if v, ok := getAttr(n, "href"); ok {
				href, _ = SafeHref(v)
			}
		}
		if href == "" {
			kids(out, inLink, inList)
			return
		}
		a := el("a")
		a.Attr = append(a.Attr, html.Attribute{Key: "href", Val: href})
		if p := strings.ToLower(href); strings.HasPrefix(p, "http:") || strings.HasPrefix(p, "https:") {
			a.Attr = append(a.Attr, html.Attribute{Key: "target", Val: "_blank"}, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
		}
		kids(a, true, inList)
		if textContent(a) != "" {
			out.AppendChild(a)
		}
	case tag == "ul" || tag == "ol":
		list := el(tag)
		kids(list, inLink, true)
		if list.FirstChild != nil {
			out.AppendChild(list)
		}
	case tag == "li":
		if inList && out.Type == html.ElementNode && (out.Data == "ul" || out.Data == "ol") {
			li := el("li")
			kids(li, inLink, false)
			out.AppendChild(li)
			return
		}
		if out.LastChild != nil && lastName() != "br" {
			brk()
		}
		out.AppendChild(&html.Node{Type: html.TextNode, Data: "• "})
		kids(out, inLink, false)
		brk()
	case richBlock[tag]:
		if ln := lastName(); out.LastChild != nil && ln != "br" && ln != "ul" && ln != "ol" {
			brk()
		}
		kids(out, inLink, false)
		if ln := lastName(); out.LastChild != nil && ln != "br" && ln != "ul" && ln != "ol" {
			brk()
		}
	case richMap[tag] != "":
		t := el(richMap[tag])
		kids(t, inLink, inList)
		if textContent(t) != "" || hasDescendant(t, "br") {
			out.AppendChild(t)
		}
	default:
		kids(out, inLink, inList)
	}
}

func el(tag string) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func hasDescendant(n *html.Node, tag string) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag || hasDescendant(c, tag) {
			return true
		}
	}
	return false
}

// ToPlain is upstream toPlain: plain text of a (rich) value for labels, mail and excerpts.
func ToPlain(v string) string {
	if v == "" || !IsRich(v) {
		return v
	}
	t := plainBrRE.ReplaceAllString(v, "\n")
	t = plainLiRE.ReplaceAllString(t, "\n• ")
	t = plainListRE.ReplaceAllString(t, "\n")
	t = richTagRE.ReplaceAllString(t, "")
	t = html.UnescapeString(t)
	t = plainNLRE.ReplaceAllString(t, "\n\n")
	return strings.TrimSpace(t)
}

// RichHTML is upstream toHtml: a stored value as safe HTML (rich values sanitized, plain values
// escaped with newlines as <br>).
func RichHTML(v string) string {
	if IsRich(v) {
		return SanitizeRich(v)
	}
	return strings.ReplaceAll(html.EscapeString(v), "\n", "<br>")
}
