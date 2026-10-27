package builder

// Tabs and accordions: the legacy tabs/accordion/toggle widgets and the nested (container based)
// nested-tabs / nested-accordion widgets.

import (
	"regexp"
	"strconv"
	"strings"
)

func init() {
	registerWidget("tabs", &widgetDef{render: renderLegacyTabs, styles: static("widget-tabs")})
	registerWidget("accordion", &widgetDef{render: func(w *elemCtx) string { return renderLegacyAccordion(w, "accordion") }, styles: static("widget-accordion")})
	registerWidget("toggle", &widgetDef{render: func(w *elemCtx) string { return renderLegacyAccordion(w, "toggle") }, styles: static("widget-toggle")})
	registerWidget("nested-tabs", &widgetDef{render: renderNestedTabs, styles: static("widget-nested-tabs"), wrapperClass: "elementor-widget-n-tabs"})
	registerWidget("nested-accordion", &widgetDef{render: renderNestedAccordion, styles: static("widget-nested-accordion"), wrapperClass: "elementor-widget-n-accordion"})
}

// idInt mirrors Controls_Stack::get_id_int (hexdec of the element id) as a decimal string.
func idInt(id string) string {
	var n uint64
	for _, c := range strings.ToLower(id) {
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		default:
			continue // hexdec ignores non-hex characters
		}
		n = n*16 + d
	}
	return strconv.FormatUint(n, 10)
}

func idInt3(id string) string {
	s := idInt(id)
	if len(s) > 3 {
		s = s[:3]
	}
	return s
}

var stripScriptStyleRe = regexp.MustCompile(`(?is)<(script|style)[^>]*?>.*?</(script|style)>`)

// wpStripAllTags mirrors wp_strip_all_tags (without line-break removal).
func wpStripAllTags(s string) string {
	s = stripScriptStyleRe.ReplaceAllString(s, "")
	return strings.TrimSpace(stripTags(s))
}

func faqSchemaScript(names, texts []string) string {
	entities := make([]any, 0, len(names))
	for i := range names {
		ans := newOmap()
		ans.Set("@type", "Answer")
		ans.Set("text", texts[i])
		q := newOmap()
		q.Set("@type", "Question")
		q.Set("name", wpStripAllTags(names[i]))
		q.Set("acceptedAnswer", ans)
		entities = append(entities, q)
	}
	j := newOmap()
	j.Set("@context", "https://schema.org")
	j.Set("@type", "FAQPage")
	j.Set("mainEntity", entities)
	return "\t\t\t\t<script type=\"application/ld+json\">" + phpJSON(j) + "</script>\n"
}

func setAttrs(a *attrs, kv ...string) {
	for i := 0; i+1 < len(kv); i += 2 {
		a.add(kv[i], kv[i+1])
	}
}

func renderLegacyTabs(w *elemCtx) string {
	tabs := asList(w.get("tabs"))
	id := idInt3(w.el.ID)
	w.attr("elementor-tabs").add("class", "elementor-tabs")
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString("elementor-tabs") + ">\n\t\t\t<div class=\"elementor-tabs-wrapper\" role=\"tablist\" >\n")
	for index, it := range tabs {
		item := asMap(it)
		n := strconv.Itoa(index + 1)
		key := repeaterKey("tab_title", "tabs", index)
		first := index == 0
		a := w.attr(key)
		a.add("id", "elementor-tab-title-"+id+n)
		a.add("class", "elementor-tab-title", "elementor-tab-desktop-title")
		a.add("aria-selected", tern(first, "true", "false"))
		a.add("data-tab", n)
		a.add("role", "tab")
		a.add("tabindex", tern(first, "0", "-1"))
		a.add("aria-controls", "elementor-tab-content-"+id+n)
		a.add("aria-expanded", "false")
		b.WriteString("\t\t\t\t\t<div " + a.String() + ">" + str(item["tab_title"]) + "</div>\n")
	}
	b.WriteString("\t\t\t\t\t</div>\n\t\t\t<div class=\"elementor-tabs-content-wrapper\" role=\"tablist\" aria-orientation=\"vertical\">\n")
	for index, it := range tabs {
		item := asMap(it)
		n := strconv.Itoa(index + 1)
		first := index == 0
		ck := repeaterKey("tab_content", "tabs", index)
		mk := repeaterKey("tab_title_mobile", "tabs", index+1)
		c := w.attr(ck)
		c.add("id", "elementor-tab-content-"+id+n)
		c.add("class", "elementor-tab-content", "elementor-clearfix")
		c.add("data-tab", n)
		c.add("role", "tabpanel")
		c.add("aria-labelledby", "elementor-tab-title-"+id+n)
		c.add("tabindex", "0")
		c.add("hidden", tern(first, "false", "hidden"))
		m := w.attr(mk)
		m.add("class", "elementor-tab-title", "elementor-tab-mobile-title")
		m.add("aria-selected", tern(first, "true", "false"))
		m.add("data-tab", n)
		m.add("role", "tab")
		m.add("tabindex", tern(first, "0", "-1"))
		m.add("aria-controls", "elementor-tab-content-"+id+n)
		m.add("aria-expanded", "false")
		w.addInlineEditing(ck, "advanced")
		b.WriteString("\t\t\t\t\t<div " + m.String() + ">" + str(item["tab_title"]) + "</div>\n")
		b.WriteString("\t\t\t\t\t<div " + w.attrString(ck) + ">" + w.r.parseTextEditor(str(item["tab_content"])) + "</div>\n")
	}
	b.WriteString("\t\t\t\t\t</div>\n\t\t</div>\n\t\t")
	return b.String()
}

func tern(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

// renderLegacyAccordion renders the accordion and toggle widgets (kind = "accordion"/"toggle").
func renderLegacyAccordion(w *elemCtx, kind string) string {
	oldIcon := w.s("icon")
	isNew := oldIcon == ""
	migrated := w.isMigrated("selected_icon")
	hasIcon := !isNew || truthy(sub(w.get("selected_icon"), "value"))
	id := idInt3(w.el.ID)
	tag := validateHTMLTag(w.s("title_html_tag"))
	tabs := asList(w.get("tabs"))
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-" + kind + "\">\n")
	for index, it := range tabs {
		item := asMap(it)
		n := strconv.Itoa(index + 1)
		tk := repeaterKey("tab_title", "tabs", index)
		ck := repeaterKey("tab_content", "tabs", index)
		t := w.attr(tk)
		t.add("id", "elementor-tab-title-"+id+n)
		t.add("class", "elementor-tab-title")
		t.add("data-tab", n)
		t.add("role", "button")
		t.add("aria-controls", "elementor-tab-content-"+id+n)
		t.add("aria-expanded", "false")
		c := w.attr(ck)
		c.add("id", "elementor-tab-content-"+id+n)
		c.add("class", "elementor-tab-content", "elementor-clearfix")
		c.add("data-tab", n)
		c.add("role", "region")
		c.add("aria-labelledby", "elementor-tab-title-"+id+n)
		w.addInlineEditing(ck, "advanced")
		b.WriteString("\t\t\t\t<div class=\"elementor-" + kind + "-item\">\n\t\t\t\t\t<" + tag + " " + t.String() + ">\n")
		if hasIcon {
			b.WriteString("\t\t\t\t\t\t\t\t\t\t\t\t<span class=\"elementor-" + kind + "-icon elementor-" + kind + "-icon-" + escAttr(w.s("icon_align")) + "\" aria-hidden=\"true\">\n")
			if isNew || migrated {
				opened := newAttrs()
				if kind == "toggle" {
					opened.add("class", "elementor-toggle-icon-opened")
				}
				b.WriteString("\t\t\t\t\t\t\t\t<span class=\"elementor-" + kind + "-icon-closed\">" + renderIcon(w.get("selected_icon"), nil, "i") + "</span>\n")
				b.WriteString("\t\t\t\t\t\t\t\t<span class=\"elementor-" + kind + "-icon-opened\">" + renderIcon(w.get("selected_active_icon"), opened, "i") + "</span>\n")
			} else {
				b.WriteString("\t\t\t\t\t\t\t\t<i class=\"elementor-" + kind + "-icon-closed " + escAttr(oldIcon) + "\"></i>\n")
				b.WriteString("\t\t\t\t\t\t\t\t<i class=\"elementor-" + kind + "-icon-opened " + escAttr(w.s("icon_active")) + "\"></i>\n")
			}
			b.WriteString("\t\t\t\t\t\t\t\t</span>\n")
		}
		b.WriteString("\t\t\t\t\t\t\t\t<a class=\"elementor-" + kind + "-title\" tabindex=\"0\">" + str(item["tab_title"]) + "</a>\n")
		b.WriteString("\t\t\t\t\t</" + tag + ">\n")
		b.WriteString("\t\t\t\t\t<div " + c.String() + ">" + w.r.parseTextEditor(str(item["tab_content"])) + "</div>\n")
		b.WriteString("\t\t\t\t</div>\n")
	}
	if w.s("faq_schema") == "yes" {
		var names, texts []string
		for _, it := range tabs {
			item := asMap(it)
			names = append(names, str(item["tab_title"]))
			texts = append(texts, w.r.parseTextEditor(str(item["tab_content"])))
		}
		b.WriteString(faqSchemaScript(names, texts))
	}
	b.WriteString("\t\t\t\t\t</div>\n\t\t")
	return b.String()
}

// printNestedChild prints child index of a nested widget, letting pre add wrapper attributes
// first (Widget_Nested_Base::print_child with the should_render filter).
func (w *elemCtx) printNestedChild(index int, pre func(*attrs)) string {
	if index < 0 || index >= len(w.el.Elements) || w.el.Elements[index] == nil {
		return ""
	}
	child := w.el.Elements[index]
	if pre != nil {
		if w.r.preAttrs == nil {
			w.r.preAttrs = map[*Element]func(*attrs){}
		}
		w.r.preAttrs[child] = pre
		defer delete(w.r.preAttrs, child)
	}
	return w.r.printElement(child)
}

func activeIconExists(m map[string]any, key string) bool {
	v, ok := m[key]
	return ok && truthy(v) && truthy(sub(v, "value"))
}

func renderNestedTabs(w *elemCtx) string {
	num := idInt(w.el.ID)
	if lm := asMap(w.get("link")); len(lm) > 0 {
		w.addLinkAttributes("elementor-tabs", lm, false)
	}
	t := w.attr("elementor-tabs")
	t.add("class", "e-n-tabs")
	t.add("data-widget-number", num)
	t.add("aria-label", "Tabs. Open items with Enter or Space, close with Escape and navigate using the Arrow keys.")
	w.attr("tab-title-text").add("class", "e-n-tab-title-text")
	w.attr("tab-icon").add("class", "e-n-tab-icon")
	w.attr("tab-icon-active").add("class", "e-n-tab-icon")
	type itemSettings struct {
		index                       int
		count                       string
		tabID, titleID, containerID string
	}
	tabs := asList(w.get("tabs"))
	var items []itemSettings
	var b strings.Builder
	b.WriteString("\t\t<div " + t.String() + ">\n\t\t\t<div class=\"e-n-tabs-heading\" role=\"tablist\">\n")
	for index, it := range tabs {
		item := asMap(it)
		if item == nil {
			item = map[string]any{}
		}
		n := strconv.Itoa(index + 1)
		titleID := "e-n-tab-title-" + num + n
		tabID := titleID
		if eid := str(item["element_id"]); truthy(item["element_id"]) {
			tabID = eid
		}
		is := itemSettings{index, n, tabID, titleID, "e-n-tab-content-" + num + n}
		items = append(items, is)
		key := repeaterKey("tab_title", "tabs", index)
		a := w.attr(key)
		a.add("id", tabID)
		a.add("data-tab-title-id", titleID)
		a.add("class", "e-n-tab-title")
		if w.has("hover_animation") {
			a.add("class", "elementor-animation-"+w.s("hover_animation"))
		}
		a.add("aria-selected", tern(index == 0, "true", "false"))
		a.add("data-tab-index", n)
		a.add("role", "tab")
		a.add("tabindex", tern(index == 0, "0", "-1"))
		a.add("aria-controls", is.containerID)
		a.add("style", "--n-tabs-title-order: "+n+";")
		b.WriteString("\t\t<button " + a.String() + ">\n")
		if icon := item["tab_icon"]; truthy(sub(icon, "value")) {
			active := icon
			if activeIconExists(item, "tab_icon_active") {
				active = item["tab_icon_active"]
			}
			b.WriteString("\t\t\t<span " + w.attrString("tab-icon") + ">\n\t\t\t" +
				renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i") +
				renderIcon(active, newAttrs().add("aria-hidden", "true"), "i") + "\t\t</span>\n")
		}
		b.WriteString("\t\t\t<span " + w.attrString("tab-title-text") + ">\n\t\t\t\t" + ksesPost(str(item["tab_title"])) + "\t\t\t</span>\n\t\t</button>\n")
	}
	b.WriteString("\t\t\t</div>\n\t\t\t<div class=\"e-n-tabs-content\">\n")
	for _, is := range items {
		is := is
		b.WriteString(w.printNestedChild(is.index, func(a *attrs) {
			a.add("id", is.containerID)
			a.add("role", "tabpanel")
			a.add("aria-labelledby", is.tabID)
			a.add("data-tab-index", is.count)
			a.add("style", "--n-tabs-title-order: "+is.count+";")
			a.add("class", tern(is.index == 0, "e-active", ""))
		}))
	}
	b.WriteString("\t\t\t</div>\n\t\t</div>\n\t\t")
	return b.String()
}

func renderNestedAccordion(w *elemCtx) string {
	items := asList(w.get("items"))
	id := idInt3(w.el.ID)
	icon := w.get("accordion_item_title_icon")
	iconHTML := renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i")
	activeHTML := iconHTML
	if activeIconExists(w.disp.m, "accordion_item_title_icon_active") {
		activeHTML = renderIcon(w.get("accordion_item_title_icon_active"), newAttrs().add("aria-hidden", "true"), "i")
	}
	icons := "\t\t<span class='e-n-accordion-item-title-icon'>\n\t\t\t<span class='e-opened' >" + activeHTML + "</span>\n\t\t\t<span class='e-closed'>" + iconHTML + "</span>\n\t\t</span>\n\n\t\t"
	acc := w.attr("elementor-accordion")
	acc.add("class", "e-n-accordion")
	acc.add("aria-label", "Accordion. Open links with Enter or Space, close with Escape, and navigate with Arrow Keys")
	expanded := w.s("default_state") == "expanded"
	tag := validateHTMLTag(w.s("title_tag"))
	var faqNames, faqTexts []string
	faqIdx := map[string]int{}
	var itemsHTML strings.Builder
	for index, it := range items {
		item := asMap(it)
		if item == nil {
			item = map[string]any{}
		}
		key := repeaterKey("item_title", "items", index)
		sumKey := repeaterKey("item_summary", "items", index)
		itemID := "e-n-accordion-item-" + id + strconv.Itoa(index)
		if truthy(item["element_css_id"]) {
			itemID = str(item["element_css_id"])
		}
		title := str(item["item_title"])
		isOpen := ""
		if expanded && index == 0 {
			isOpen = "open"
		}
		a := w.attr(key)
		a.add("id", itemID)
		a.add("class", "e-n-accordion-item")
		s := w.attr(sumKey)
		s.add("class", "e-n-accordion-item-title")
		s.add("data-accordion-index", strconv.Itoa(index+1))
		s.add("tabindex", tern(index == 0, "0", "-1"))
		s.add("aria-expanded", tern(expanded && index == 0, "true", "false"))
		s.add("aria-controls", itemID)
		content := w.printNestedChild(index, func(c *attrs) {
			c.add("role", "region")
			c.add("aria-labelledby", itemID)
		})
		if i, ok := faqIdx[title]; ok {
			faqTexts[i] = content
		} else {
			faqIdx[title] = len(faqNames)
			faqNames = append(faqNames, title)
			faqTexts = append(faqTexts, content)
		}
		itemsHTML.WriteString("\t\t\t<details " + ksesPost(a.String()+" "+isOpen) + ">\n")
		itemsHTML.WriteString("\t\t\t\t<summary " + ksesPost(s.String()) + " >\n")
		itemsHTML.WriteString("\t\t\t\t\t<span class='e-n-accordion-item-title-header'>" + ksesPost("<"+tag+" class=\"e-n-accordion-item-title-text\"> "+title+" </"+tag+">") + "</span>\n")
		if truthy(sub(icon, "value")) {
			itemsHTML.WriteString("\t\t\t\t\t" + icons)
		}
		itemsHTML.WriteString("\t\t\t\t</summary>\n\t\t\t\t" + content + "\t\t\t</details>\n\t\t\t")
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + acc.String() + ">\n\t\t\t" + itemsHTML.String() + "\t\t</div>\n\t\t")
	if w.s("faq_schema") == "yes" {
		texts := make([]string, len(faqTexts))
		for i, t := range faqTexts {
			texts[i] = wpStripAllTags(t)
		}
		b.WriteString(faqSchemaScript(faqNames, texts))
	}
	return b.String()
}
