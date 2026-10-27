package atomic

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strings"
)

// processSVG mirrors Svg_Src_Transformer::process_svg: set fill="currentColor" and the inline size
// style on the root <svg> (WP_HTML_Tag_Processor inserts new attributes right after the tag name),
// then sanitize it (Svg_Sanitizer: comments, line breaks, scripts and event handlers removed,
// whitespace-only text dropped, re-serialized by libxml with namespace declarations first and no
// self-closing tags).
func processSVG(content string) string {
	loc := svgOpenRe.FindStringIndex(content)
	if loc == nil {
		return ""
	}
	tag := content[loc[0]:loc[1]]
	tag = setTagAttr(tag, "fill", "currentColor")
	const inline = "width: 100%; height: 100%; overflow: unset;"
	style := ""
	if m := attrValueRe("style").FindStringSubmatch(tag); m != nil {
		style = strings.TrimSpace(htmlUnescapeAttr(m[2]))
	}
	merged := inline
	if style != "" {
		merged = strings.TrimRight(style, ";") + "; " + inline
	}
	tag = setTagAttr(tag, "style", merged)
	return sanitizeSVG(content[:loc[0]] + tag + content[loc[1]:])
}

var svgOpenRe = regexp.MustCompile(`(?is)<svg(\s[^>]*)?>`)

func attrValueRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?is)(\s` + regexp.QuoteMeta(name) + `\s*=\s*)("[^"]*"|'[^']*'|[^\s>]+)`)
}

func htmlUnescapeAttr(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		v = v[1 : len(v)-1]
	}
	return strings.NewReplacer("&quot;", `"`, "&#039;", "'", "&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(v)
}

// setTagAttr sets an attribute on an opening tag the way WP_HTML_Tag_Processor does.
func setTagAttr(tag, name, value string) string {
	esc := escAttr(value)
	re := attrValueRe(name)
	if re.MatchString(tag) {
		return re.ReplaceAllString(tag, `${1}"`+strings.ReplaceAll(esc, "$", "$$")+`"`)
	}
	return tag[:4] + " " + name + `="` + esc + `"` + tag[4:]
}

var (
	svgCommentRe = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	svgCSSComRe  = regexp.MustCompile(`(?s)/\*(.*?)\*/`)
	svgPHPRe     = regexp.MustCompile(`(?s)<\?(php)?.*?\?>`)
)

var svgBadElements = map[string]bool{"script": true, "foreignobject": true, "iframe": true, "embed": true, "object": true, "handler": true, "listener": true}

func sanitizeSVG(content string) string {
	content = svgCommentRe.ReplaceAllString(content, "")
	content = svgCSSComRe.ReplaceAllString(content, "")
	if strings.Contains(content, "<!--") || strings.Contains(content, "/*") {
		return ""
	}
	content = svgPHPRe.ReplaceAllString(content, "")
	content = strings.NewReplacer("\r", "", "\n", "").Replace(content)
	start := strings.Index(content, "<svg")
	end := strings.LastIndex(content, "</svg>")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	content = content[start : end+6]
	dec := xml.NewDecoder(strings.NewReader(content))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	var b bytes.Buffer
	skip := 0
	for {
		tok, err := dec.RawToken()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := qname(t.Name)
			if skip > 0 || svgBadElements[strings.ToLower(t.Name.Local)] {
				skip++
				continue
			}
			b.WriteString("<" + name)
			// namespace declarations first (libxml serializes nsDef before attributes)
			for _, pass := range []bool{true, false} {
				for _, a := range t.Attr {
					isNS := a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns")
					if isNS != pass {
						continue
					}
					an := qname(a.Name)
					low := strings.ToLower(a.Name.Local)
					if strings.HasPrefix(low, "on") {
						continue
					}
					if (low == "href") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Value)), "javascript:") {
						continue
					}
					b.WriteString(" " + an + `="` + xmlEscapeAttr(a.Value) + `"`)
				}
			}
			b.WriteString(">")
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			b.WriteString("</" + qname(t.Name) + ">")
		case xml.CharData:
			if skip > 0 || len(bytes.TrimSpace(t)) == 0 {
				continue
			}
			b.WriteString(xmlEscapeText(string(t)))
		}
	}
	return b.String()
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

func xmlEscapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;", "\t", "&#9;").Replace(s)
}

func xmlEscapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
