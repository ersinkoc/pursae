package builder

// Basic widgets: heading, text-editor, button, icon-list.

import (
	"strings"
)

func init() {
	registerWidget("heading", &widgetDef{render: renderHeading, styles: static("widget-heading")})
	registerWidget("text-editor", &widgetDef{render: renderTextEditor, styles: func(w *elemCtx) []string {
		if w.s("drop_cap") == "yes" {
			return []string{"widget-text-editor"}
		}
		return nil
	}})
	registerWidget("button", &widgetDef{render: renderButton})
	registerWidget("icon-list", &widgetDef{render: renderIconList, styles: static("widget-icon-list")})
}

func static(handles ...string) func(*elemCtx) []string {
	return func(*elemCtx) []string { return handles }
}

func renderHeading(w *elemCtx) string {
	title := w.get("title")
	if strictEq(title, "") || title == nil {
		return ""
	}
	a := w.attr("title")
	a.add("class", "elementor-heading-title")
	if size := w.s("size"); size != "" && truthy(w.get("size")) {
		a.add("class", "elementor-size-"+size)
	} else {
		a.add("class", "elementor-size-default")
	}
	w.addInlineEditing("title", "")
	t := ksesPost(phpString(title))
	if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
		w.addLinkAttributes("url", lm, false)
		t = "<a " + w.attrString("url") + ">" + t + "</a>"
	}
	tag := validateHTMLTag(w.s("header_size"))
	return "\t\t\t\t\t<" + tag + " " + w.attrString("title") + ">" + t + "</" + tag + ">\t\t\t\t"
}

func renderTextEditor(w *elemCtx) string {
	inline := w.editing()
	content := w.r.parseTextEditor(w.s("editor"))
	if isEmpty(content) {
		return ""
	}
	if inline {
		w.attr("editor").add("class", "elementor-text-editor", "elementor-clearfix")
	}
	w.addInlineEditing("editor", "advanced")
	var b strings.Builder
	b.WriteString("\t\t\t\t\t\t\t\t")
	if inline {
		b.WriteString("<div " + w.attrString("editor") + ">\n\t\t\t\t")
	}
	b.WriteString(content)
	if inline {
		b.WriteString("\t\t\t\t\t</div>\n\t\t\t\t")
	}
	b.WriteString("\t\t\t\t\t\t\t\t")
	return b.String()
}

// renderButtonMarkup mirrors Button_Trait::render_button for a widget instance.
func renderButtonMarkup(w *elemCtx) string {
	if !w.has("text") && !truthy(sub(w.get("selected_icon"), "value")) {
		return ""
	}
	w.attr("wrapper").add("class", "elementor-button-wrapper")
	btn := w.attr("button")
	btn.add("class", "elementor-button")
	if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
		w.addLinkAttributes("button", lm, false)
		btn.add("class", "elementor-button-link")
	} else {
		btn.add("role", "button")
	}
	if w.has("button_css_id") {
		btn.add("id", w.s("button_css_id"))
	}
	if w.has("size") {
		btn.add("class", "elementor-size-"+w.s("size"))
	} else {
		btn.add("class", "elementor-size-sm")
	}
	if w.has("hover_animation") {
		btn.add("class", "elementor-animation-"+w.s("hover_animation"))
	}
	var b strings.Builder
	b.WriteString("\t\t\t\t\t\t\t\t\t<a " + btn.String() + ">\n")
	b.WriteString(buttonText(w))
	b.WriteString("\t\t\t\t\t</a>\n\t\t\t\t\t\t\t\t")
	return b.String()
}

func buttonText(w *elemCtx) string {
	cw := w.attr("content-wrapper")
	cw.add("class", "elementor-button-content-wrapper")
	w.attr("icon").add("class", "elementor-button-icon")
	w.attr("text").add("class", "elementor-button-text")
	var b strings.Builder
	b.WriteString("\t\t\t\t\t\t<span " + cw.String() + ">\n")
	if w.has("icon") || truthy(sub(w.get("selected_icon"), "value")) {
		b.WriteString("\t\t\t\t\t\t\t\t<span " + w.attrString("icon") + ">\n\t\t\t\t")
		if !w.has("icon") || w.isMigrated("selected_icon") {
			b.WriteString(renderIcon(w.get("selected_icon"), newAttrs().add("aria-hidden", "true"), "i"))
		} else {
			b.WriteString(`<i class="` + escAttr(w.s("icon")) + `" aria-hidden="true"></i>`)
		}
		b.WriteString("\t\t\t</span>\n")
	}
	if w.has("text") {
		b.WriteString("\t\t\t\t\t\t\t\t\t<span " + w.attrString("text") + ">" + ksesPost(w.s("text")) + "</span>\n")
	}
	b.WriteString("\t\t\t\t\t</span>\n")
	return b.String()
}

// isMigrated reports __fa4_migrated[key].
func (e *elemCtx) isMigrated(key string) bool {
	m := asMap(e.get("__fa4_migrated"))
	if m == nil {
		m = asMap(e.raw("__fa4_migrated"))
	}
	_, ok := m[key]
	return ok
}

func renderButton(w *elemCtx) string { return renderButtonMarkup(w) }

func renderIconList(w *elemCtx) string {
	list := w.attr("icon_list")
	list.add("class", "elementor-icon-list-items")
	item := w.attr("list_item")
	item.add("class", "elementor-icon-list-item")
	if w.s("view") == "inline" {
		list.add("class", "elementor-inline-items")
		item.add("class", "elementor-inline-item")
	}
	var b strings.Builder
	b.WriteString("\t\t\t\t\t<ul " + list.String() + ">\n")
	for index, it := range asList(w.get("icon_list")) {
		im := asMap(it)
		if im == nil {
			continue
		}
		rk := repeaterKey("text", "icon_list", index)
		w.attr(rk).add("class", "elementor-icon-list-text")
		w.addInlineEditing(rk, "")
		b.WriteString("\t\t\t\t\t\t\t<li " + item.String() + ">\n")
		link := asMap(im["link"])
		hasLink := link != nil && truthy(link["url"])
		if hasLink {
			lk := "link_" + itoa(index)
			w.addLinkAttributes(lk, link, false)
			b.WriteString("\t\t\t\t\t\t\t\t\t\t\t<a " + w.attrString(lk) + ">\n\n")
		}
		_, hasOld := im["icon"]
		_, migrated := asMap(im["__fa4_migrated"])["selected_icon"]
		isNew := !hasOld
		if truthy(im["icon"]) || (truthy(sub(im["selected_icon"], "value")) && isNew) {
			b.WriteString("\t\t\t\t\t\t\t\t\t\t\t<span class=\"elementor-icon-list-icon\">\n\t\t\t\t\t\t\t")
			if isNew || migrated {
				b.WriteString(renderIcon(im["selected_icon"], newAttrs().add("aria-hidden", "true"), "i"))
			} else {
				b.WriteString(`<i class="` + escAttr(phpString(im["icon"])) + `" aria-hidden="true"></i>`)
			}
			b.WriteString("\t\t\t\t\t\t</span>\n")
		}
		b.WriteString("\t\t\t\t\t\t\t\t\t\t<span " + w.attrString(rk) + ">" + ksesPost(str(im["text"])) + "</span>\n")
		if hasLink {
			b.WriteString("\t\t\t\t\t\t\t\t\t\t\t</a>\n")
		}
		b.WriteString("\t\t\t\t\t\t\t\t\t</li>\n")
	}
	b.WriteString("\t\t\t\t\t\t</ul>\n\t\t\t\t\t\t")
	return b.String()
}
