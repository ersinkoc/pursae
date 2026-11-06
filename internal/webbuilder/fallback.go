package webbuilder

import (
	"html"
	"regexp"
	"strings"
)

// Fallback HTML (contract §6.2): readable, semantic HTML generated from the doc (never from the
// snapshot) for posts.content. It is what REST content.rendered, Pursae Headless, feeds, search,
// sitemaps and the theme (plugin off or page detached) show. Per visible section: headings (h1
// for the first hero title, h2 for section titles, h3 for items), paragraphs, lists, images with
// alt, links and buttons as <a>, quotes as <blockquote>, FAQ as h3 + p, nav and footer links as
// <ul>. Forms, html sections, decor and motion are omitted.

// FallbackHTML renders the fallback HTML of a doc.
func FallbackHTML(d *DocState) string {
	f := &fallback{}
	for i := range d.Sections {
		f.section(&d.Sections[i], 0, true)
	}
	return strings.TrimSpace(f.b.String())
}

type fallback struct {
	b     strings.Builder
	hasH1 bool
}

var (
	headingTagRE = regexp.MustCompile(`^h[1-6]$`)
	richListRE   = regexp.MustCompile(`<(ul|ol)\b`)
)

func (f *fallback) w(s ...string) {
	for _, x := range s {
		f.b.WriteString(x)
	}
}

func esc(s string) string { return html.EscapeString(s) }

// visible: the section is not hidden and not hidden on every device.
func visible(s *Section) bool {
	if s.Hidden {
		return false
	}
	if len(s.Style.HideOn) >= 3 {
		seen := map[string]bool{}
		for _, d := range s.Style.HideOn {
			seen[d] = true
		}
		if seen["desktop"] && seen["tablet"] && seen["mobile"] {
			return false
		}
	}
	return true
}

// shown: the element is not removed / hidden in the section's element styles.
func shown(s *Section, el string) bool {
	es, ok := s.Els[el]
	if !ok {
		return true
	}
	return !(es.Removed != nil && *es.Removed) && !(es.Hide != nil && *es.Hide)
}

func (f *fallback) val(s *Section, key string) string {
	if !shown(s, key) {
		return ""
	}
	return strings.TrimSpace(s.Content[key])
}

// inline renders a value for use inside a heading or link (lists flattened).
func inline(v string) string {
	if v == "" {
		return ""
	}
	if IsRich(v) {
		out := SanitizeRich(v)
		if richListRE.MatchString(out) {
			return esc(ToPlain(v))
		}
		return out
	}
	return strings.ReplaceAll(esc(v), "\n", "<br>")
}

// para renders a text value as paragraphs (blank lines split paragraphs; rich lists stay lists).
func (f *fallback) para(v string) {
	if v == "" {
		return
	}
	if IsRich(v) {
		out := SanitizeRich(v)
		if out == "" {
			return
		}
		if richListRE.MatchString(out) {
			f.w("<div>", out, "</div>\n")
			return
		}
		f.w("<p>", out, "</p>\n")
		return
	}
	for _, p := range strings.Split(strings.ReplaceAll(v, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			f.w("<p>", strings.ReplaceAll(esc(p), "\n", "<br>"), "</p>\n")
		}
	}
}

func (f *fallback) heading(level int, v string) {
	if v != "" {
		f.headingHTML(level, inline(v))
	}
}

func (f *fallback) headingHTML(level int, h string) {
	if h == "" {
		return
	}
	if level == 1 {
		if f.hasH1 {
			level = 2
		} else {
			f.hasH1 = true
		}
	}
	t := "h" + string(rune('0'+level))
	f.w("<", t, ">", h, "</", t, ">\n")
}

func linkHTML(text, href string) string {
	if text == "" {
		return ""
	}
	h, ok := SafeHref(href)
	if !ok {
		return inline(text)
	}
	return `<a href="` + esc(h) + `">` + inline(text) + "</a>"
}

// buttons renders the given button/link content keys (with their <key>Href) as one paragraph.
func (f *fallback) buttons(s *Section, keys ...string) {
	var links []string
	for _, k := range keys {
		if t := f.val(s, k); t != "" {
			links = append(links, linkHTML(t, s.Content[k+"Href"]))
		}
	}
	if len(links) > 0 {
		f.w("<p>", strings.Join(links, " "), "</p>\n")
	}
}

func (f *fallback) img(src, alt string) {
	src = strings.TrimSpace(src)
	if src == "" || !ImageURLOK(src, false) {
		return
	}
	f.w(`<img src="`, esc(src), `" alt="`, esc(ToPlain(alt)), `" loading="lazy">`, "\n")
}

func (f *fallback) image(s *Section, key, alt string) {
	if v := f.val(s, key); v != "" {
		f.img(v, alt)
	}
}

func (f *fallback) linkList(s *Section) {
	var lis []string
	for i := range s.Items {
		it := &s.Items[i]
		if !shown(s, "items."+it.ID+".title") {
			continue
		}
		if t := strings.TrimSpace(it.Title); t != "" {
			lis = append(lis, "<li>"+linkHTML(t, it.Href)+"</li>")
		}
	}
	if len(lis) > 0 {
		f.w("<ul>\n", strings.Join(lis, "\n"), "\n</ul>\n")
	}
}

// cards renders items as h3 + p (features, steps, faq).
func (f *fallback) cards(s *Section) {
	for i := range s.Items {
		it := &s.Items[i]
		if shown(s, "items."+it.ID+".title") {
			title := strings.TrimSpace(it.Title)
			if it.Href != "" {
				f.w("<h3>", linkHTML(title, it.Href), "</h3>\n")
			} else {
				f.heading(3, title)
			}
		}
		if shown(s, "items."+it.ID+".text") {
			f.para(strings.TrimSpace(it.Text))
		}
	}
}

func (f *fallback) intro(s *Section, level int) {
	f.para(f.val(s, "eyebrow"))
	title := inline(f.val(s, "title"))
	if acc := inline(f.val(s, "accent")); acc != "" {
		if title != "" {
			title += "<br>"
		}
		title += acc
	}
	f.headingHTML(level, title)
	f.para(f.val(s, "body"))
}

func (f *fallback) section(s *Section, depth int, wrap bool) {
	if depth > MaxDepth+1 || !visible(s) || s.Kind == KindHTML || s.Kind == KindForm {
		return
	}
	tag := "section"
	switch s.Kind {
	case "nav":
		tag = "nav"
	case "footer":
		tag = "footer"
	}
	start := f.b.Len()
	if wrap {
		id := s.ID
		if s.Style.CSSID != "" {
			id = s.Style.CSSID
		}
		f.w("<", tag, ` id="`, esc(id), `">`, "\n")
	}
	body := f.b.Len()
	switch s.Kind {
	case "nav":
		if b := f.val(s, "brand"); b != "" {
			f.w("<p><strong>", inline(b), "</strong></p>\n")
		}
		f.linkList(s)
		f.buttons(s, "primary")
	case "hero":
		f.intro(s, 1)
		f.buttons(s, "primary", "secondary")
		if s.Content["visual"] == "image" {
			f.image(s, "image", s.Content["title"])
		}
		f.highlights(s)
	case "banner":
		text := f.val(s, "text")
		link := linkHTML(f.val(s, "link"), s.Content["linkHref"])
		if text != "" || link != "" {
			parts := []string{}
			if text != "" {
				parts = append(parts, inline(text))
			}
			if link != "" {
				parts = append(parts, link)
			}
			f.w("<p>", strings.Join(parts, " "), "</p>\n")
		}
	case "features", "steps", "faq":
		f.intro(s, 2)
		f.cards(s)
	case "media":
		f.intro(s, 2)
		f.buttons(s, "primary")
		f.image(s, "image", s.Content["title"])
		f.image(s, "image2", "")
		f.image(s, "image3", "")
	case "quote":
		q := f.val(s, "quote")
		if q != "" {
			f.w("<blockquote>\n")
			f.para(q)
			who := []string{}
			if a := f.val(s, "author"); a != "" {
				who = append(who, inline(a))
			}
			if r := f.val(s, "role"); r != "" {
				who = append(who, inline(r))
			}
			if len(who) > 0 {
				f.w("<p><cite>", strings.Join(who, ", "), "</cite></p>\n")
			}
			f.w("</blockquote>\n")
		}
	case "cta":
		f.intro(s, 2)
		f.buttons(s, "primary", "secondary")
	case "footer":
		if b := f.val(s, "brand"); b != "" {
			f.w("<p><strong>", inline(b), "</strong></p>\n")
		}
		f.linkList(s)
		f.para(f.val(s, "note"))
	case "text":
		f.heading(2, f.val(s, "title"))
		f.para(f.val(s, "body"))
	case "image":
		src := f.val(s, "image")
		caption := f.val(s, "caption")
		if s.Content["video"] == "1" {
			if src != "" && ImageURLOK(src, false) {
				label := caption
				if label == "" {
					label = "Video"
				}
				f.w("<p>", linkHTML(label, src), "</p>\n")
			}
			break
		}
		if src != "" && ImageURLOK(src, false) {
			f.w("<figure>\n")
			f.img(src, caption)
			if caption != "" {
				f.w("<figcaption>", inline(caption), "</figcaption>\n")
			}
			f.w("</figure>\n")
		}
	case KindLayout:
		f.layout(s, depth)
	case KindCustom:
		f.custom(s)
	default:
		f.intro(s, 2)
		f.buttons(s, "primary", "secondary")
		f.cards(s)
	}
	if wrap {
		if f.b.Len() == body {
			// Nothing readable: drop the empty wrapper.
			cut := f.b.String()[:start]
			f.b.Reset()
			f.b.WriteString(cut)
			return
		}
		f.w("</", tag, ">\n")
	}
}

func (f *fallback) highlights(s *Section) {
	var lis []string
	for i := range s.Items {
		it := &s.Items[i]
		if t := strings.TrimSpace(it.Title); t != "" && shown(s, "items."+it.ID+".title") {
			lis = append(lis, "<li>"+inline(t)+"</li>")
		}
	}
	if len(lis) > 0 {
		f.w("<ul>\n", strings.Join(lis, "\n"), "\n</ul>\n")
	}
}

// layout cells: item.title is the cell type, item.text its content, item.sub a nested container.
func (f *fallback) layout(s *Section, depth int) {
	for i := range s.Items {
		it := &s.Items[i]
		if it.Sub != nil {
			f.section(it.Sub, depth+1, false)
			continue
		}
		if !shown(s, "items."+it.ID+".text") {
			continue
		}
		text := strings.TrimSpace(it.Text)
		switch it.Title {
		case "heading":
			f.heading(2, text)
		case "button":
			if text != "" {
				f.w("<p>", linkHTML(text, it.Href), "</p>\n")
			}
		case "image":
			f.img(text, "")
		case "text":
			f.para(text)
		}
	}
}

// custom sections: a tree of nodes (item.parent), rendered depth-first in item order.
func (f *fallback) custom(s *Section) {
	kids := map[string][]*Item{}
	ids := map[string]bool{}
	for i := range s.Items {
		ids[s.Items[i].ID] = true
	}
	for i := range s.Items {
		it := &s.Items[i]
		p := it.Parent
		if !ids[p] {
			p = ""
		}
		kids[p] = append(kids[p], it)
	}
	seen := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		if depth > 32 {
			return
		}
		for _, it := range kids[parent] {
			if seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			el := "items." + it.ID + ".title"
			if !shown(s, el) {
				continue
			}
			text := strings.TrimSpace(it.Title)
			switch it.Node {
			case "box", "":
				if it.Node == "box" {
					walk(it.ID, depth+1)
					continue
				}
				f.para(text)
			case "heading":
				level := 2
				if headingTagRE.MatchString(it.Tag) {
					level = int(it.Tag[1] - '0')
				}
				f.heading(level, text)
			case "text":
				f.para(text)
			case "button", "link":
				if text != "" {
					f.w("<p>", linkHTML(text, it.Href), "</p>\n")
				}
			case "image":
				f.img(text, it.Text)
			case "divider":
				f.w("<hr>\n")
			}
		}
	}
	walk("", 0)
}
