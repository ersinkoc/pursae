package builder

// GoWebEditor Pro widgets (group B): share-buttons, hotspot, table-of-contents, video-playlist,
// slides, paypal-button, stripe-button, template and global. Ports of the render() methods of
// upstream Pro 4.3.0's modules/{share-buttons,hotspot,table-of-contents,video-playlist,slides,
// payments,library,global-widget}/widgets/*.php (GPL-3.0, see NOTICE).

import (
	"strconv"
	"strings"
)

func init() {
	registerWidget("share-buttons", &widgetDef{render: proBRenderShareButtons,
		styles: static("widget-share-buttons", "e-apple-webkit", "elementor-icons-fa-solid", "elementor-icons-fa-brands")})
	registerWidget("hotspot", &widgetDef{render: proBRenderHotspot, styles: static("widget-hotspot")})
	registerWidget("table-of-contents", &widgetDef{render: proBRenderTOC, styles: static("widget-table-of-contents")})
	registerWidget("video-playlist", &widgetDef{render: proBRenderVideoPlaylist, styles: static("widget-video-playlist")})
	registerWidget("slides", &widgetDef{render: proBRenderSlides, styles: proBSlidesStyles, scripts: []string{"imagesloaded", "swiper"}})
	registerWidget("paypal-button", &widgetDef{render: proBRenderPayPal})
	registerWidget("stripe-button", &widgetDef{render: proBRenderStripe})
	registerWidget("template", &widgetDef{render: proBRenderTemplate})
	// The global widget has no controls of its own: it renders the widget saved in its template.
	s := loadSchema()
	if s.widgets["global"] == nil {
		s.widgets["global"] = &widgetSchema{Common: "common-optimized"}
	}
	registerWidget("global", &widgetDef{render: proBRenderGlobal, styles: proBGlobalStyles})
}

// --- share buttons ---------------------------------------------------------------------------------

// proBShareNetworks are Share_Buttons Module::$networks titles (deprecated networks are skipped).
var proBShareNetworks = map[string]string{"facebook": "Facebook", "twitter": "Twitter", "linkedin": "LinkedIn", "pinterest": "Pinterest",
	"reddit": "Reddit", "vk": "VK", "odnoklassniki": "OK", "tumblr": "Tumblr", "digg": "Digg", "skype": "Skype",
	"stumbleupon": "StumbleUpon", "mix": "Mix", "telegram": "Telegram", "pocket": "Pocket", "xing": "XING", "whatsapp": "WhatsApp",
	"email": "Email", "print": "Print", "x-twitter": "X", "threads": "Threads"}

// proBShareIcon mirrors get_network_icon_data (icon migration allowed).
func proBShareIcon(network string) map[string]any {
	switch network {
	case "pocket":
		return map[string]any{"value": "fab fa-get-pocket", "library": "fa-brands"}
	case "email":
		return map[string]any{"value": "fas fa-envelope", "library": "fa-solid"}
	case "print":
		return map[string]any{"value": "fas fa-print", "library": "fa-solid"}
	}
	return map[string]any{"value": "fab fa-" + network, "library": "fa-brands"}
}

// proBFontIcon mirrors Icons_Manager::render_font_icon with SVG icons on.
func proBFontIcon(icon map[string]any, a *attrs) string {
	if svg := fontIconSVG(icon, a); svg != "" {
		return svg
	}
	if a == nil {
		a = newAttrs()
	}
	a.add("class", phpString(icon["value"]))
	return "<i " + a.String() + "></i>"
}

func proBRenderShareButtons(w *elemCtx) string {
	buttons := asList(w.get("share_buttons"))
	if len(buttons) == 0 {
		return ""
	}
	wrap := w.attr("wrapper").add("class", "elementor-grid")
	item := w.attr("item_wrapper").add("class", "elementor-grid-item")
	if len(buttons) > 1 {
		wrap.add("role", "list")
		item.add("role", "listitem")
	}
	view := w.s("view")
	showText := view == "text" || w.s("show_label") == "yes"
	var b strings.Builder
	b.WriteString("\t\t<div " + wrap.String() + ">\n")
	for i, bt := range buttons {
		bm := asMap(bt)
		network := phpString(bm["button"])
		title, ok := proBShareNetworks[network]
		if !ok {
			continue
		}
		key := "item_link_" + strconv.Itoa(i)
		w.attr(key).add("class", "elementor-share-btn", "elementor-share-btn_"+network).add("role", "button").add("tabindex", "0").
			add("aria-label", "Share on "+escAttr(network))
		b.WriteString("\t\t\t\t\t<div " + item.String() + ">\n")
		b.WriteString("\t\t\t\t\t\t<div " + w.attrString(key) + ">\n")
		if view == "icon" || view == "icon-text" {
			b.WriteString("\t\t\t\t\t\t\t\t<span class=\"elementor-share-btn__icon\">\n\t\t\t\t\t\t\t\t" +
				proBFontIcon(proBShareIcon(network), newAttrs().add("aria-hidden", "true")) + "\t\t\t\t\t\t\t</span>\n")
		}
		if showText {
			b.WriteString("\t\t\t\t\t\t\t<div class=\"elementor-share-btn__text\">\n")
			if w.s("show_label") == "yes" || view == "text" {
				text := phpString(bm["text"])
				if !truthy(bm["text"]) {
					text = title
				}
				b.WriteString("\t\t\t\t\t\t\t\t\t<span class=\"elementor-share-btn__title\">\n\t\t\t\t\t\t\t\t\t" + text + "\t\t\t\t\t\t\t\t\t</span>\n")
			}
			b.WriteString("\t\t\t\t\t\t\t</div>\n")
		}
		b.WriteString("\t\t\t\t\t\t</div>\n\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t</div>\n")
	return b.String()
}

// --- hotspot -----------------------------------------------------------------------------------------

func proBRenderHotspot(w *elemCtx) string {
	img := asMap(w.get("image"))
	if img == nil || !truthy(img["url"]) {
		return ""
	}
	anim := w.s("tooltip_animation")
	isDirection := anim == "e-hotspot--slide-direction" || anim == "e-hotspot--fade-direction"
	trigger := w.s("tooltip_trigger")
	showTooltip := trigger == "none"
	seq := ""
	if w.s("hotspot_sequenced_animation") == "yes" {
		seq = "e-hotspot--sequenced"
	}
	var b strings.Builder
	b.WriteString(w.r.attachmentImageHTML(w.disp.m, "image", "image"))
	for i, it := range asList(w.get("hotspot")) {
		h := asMap(it)
		if h == nil {
			continue
		}
		label := phpString(h["hotspot_label"])
		icon := asMap(h["hotspot_icon"])
		iconValue := truthy(sub(icon, "value"))
		isCircle := !truthy(h["hotspot_label"]) && !iconValue
		isOnlyIcon := !truthy(h["hotspot_label"]) && iconValue
		posX, posY := "", ""
		if phpString(sub(h["hotspot_offset_x"], "unit")) == "%" {
			posX = "e-hotspot--position-" + phpString(h["hotspot_horizontal"])
		}
		if phpString(sub(h["hotspot_offset_y"], "unit")) == "%" {
			posY = "e-hotspot--position-" + phpString(h["hotspot_vertical"])
		}
		isLink := truthy(sub(h["hotspot_link"], "url"))
		tag := "div"
		linkClass, noTip := "", ""
		if isLink {
			tag = "a"
			linkClass = "e-hotspot--link"
			if trigger == "click" {
				noTip = "e-hotspot--no-tooltip"
			}
		}
		hk := repeaterKey("hotspot", "hotspots", i)
		w.attr(hk).add("class", "e-hotspot", "elementor-repeater-item-"+phpString(h["_id"]), seq, posX, posY, linkClass, noTip)
		if isCircle {
			w.attr(hk).add("class", "e-hotspot--circle")
		}
		if isOnlyIcon {
			w.attr(hk).add("class", "e-hotspot--icon")
		}
		if isLink {
			w.addLinkAttributes(hk, h["hotspot_link"], false)
		}
		tk := repeaterKey("trigger", "hotspots", i)
		w.attr(tk).add("class", "e-hotspot__button", w.s("hotspot_animation"))
		mk := repeaterKey("e-hotspot__direction-mask", "hotspots", i)
		maskPos := ""
		if isDirection {
			maskPos = "e-hotspot--tooltip-position"
		}
		w.attr(mk).add("class", "e-hotspot__direction-mask", maskPos)
		custom := ""
		if isDirection && truthy(h["hotspot_tooltip_position"]) && truthy(h["hotspot_position"]) {
			custom = "e-hotspot--override-tooltip-animation-from-" + phpString(h["hotspot_position"])
		}
		ttk := repeaterKey("tooltip", "hotspots", i)
		show, pos, animClass := "", "", ""
		if showTooltip {
			show = "e-hotspot--show-tooltip"
		} else {
			animClass = anim
		}
		if !isDirection {
			pos = "e-hotspot--tooltip-position"
		}
		w.attr(ttk).add("class", "e-hotspot__tooltip", show, pos, animClass, custom)

		b.WriteString("\t\t\t<" + tag + " " + w.attrString(hk) + ">\n")
		b.WriteString("\t\t\t\t<div " + w.attrString(tk) + ">\n")
		if isCircle {
			b.WriteString("\t\t\t\t\t\t<div class=\"e-hotspot__outer-circle\"></div>\n\t\t\t\t\t\t<div class=\"e-hotspot__inner-circle\"></div>\n")
		} else {
			if iconValue {
				b.WriteString("\t\t\t\t\t\t\t<div class=\"e-hotspot__icon\">" + renderIcon(icon, nil, "i") + "</div>\n")
			}
			if label != "" {
				b.WriteString("\t\t\t\t\t\t\t<div class=\"e-hotspot__label\">" + label + "</div>\n")
			}
		}
		b.WriteString("\t\t\t\t</div>\n")
		if truthy(h["hotspot_tooltip_content"]) && !(trigger == "click" && isLink) {
			if isDirection {
				b.WriteString("\t\t\t\t\t\t<div " + w.attrString(mk) + ">\n")
			}
			b.WriteString("\t\t\t\t\t<div " + w.attrString(ttk) + " >\n\t\t\t\t\t\t" + phpString(h["hotspot_tooltip_content"]) + "\t\t\t\t\t</div>\n")
			if isDirection {
				b.WriteString("\t\t\t\t\t\t</div>\n")
			}
		}
		b.WriteString("\t\t\t</" + tag + ">\n")
	}
	return b.String()
}

// --- table of contents ----------------------------------------------------------------------------

func proBRenderTOC(w *elemCtx) string {
	// get_frontend_settings adds the marker icon's SVG for the frontend list builder.
	if ic := asMap(w.get("icon")); ic != nil && truthy(ic["value"]) {
		cp := map[string]any{}
		for k, v := range ic {
			cp[k] = v
		}
		cp["rendered_tag"] = proBFontIcon(ic, nil)
		w.disp.m["icon"] = cp
	}
	tocID := "elementor-toc__" + w.el.ID
	w.attr("header").add("class", "elementor-toc__header")
	body := w.attr("body").add("id", tocID).add("class", "elementor-toc__body")
	if w.has("collapse_subitems") {
		body.add("class", "elementor-toc__list-items--collapsible")
	}
	minimize := w.s("minimize_box") == "yes"
	if minimize {
		w.attr("expand-button").add("class", "elementor-toc__toggle-button elementor-toc__toggle-button--expand").add("role", "button").
			add("tabindex", "0").add("aria-controls", tocID).add("aria-expanded", "true").add("aria-label", "Open table of contents")
		w.attr("collapse-button").add("class", "elementor-toc__toggle-button elementor-toc__toggle-button--collapse").add("role", "button").
			add("tabindex", "0").add("aria-controls", tocID).add("aria-expanded", "true").add("aria-label", "Close table of contents")
	}
	tag := validateHTMLTag(w.s("html_tag"))
	var b strings.Builder
	title := w.get("title")
	if !miscIsEmpty(title) || minimize {
		b.WriteString("\t\t\t<div " + w.attrString("header") + ">\n")
		if !miscIsEmpty(title) {
			b.WriteString("\t\t\t<" + tag + " class=\"elementor-toc__header-title\">\n\t\t\t\t" + ksesPost(phpString(title)) + "\t\t\t</" + tag + ">\n")
		}
		if minimize {
			b.WriteString("\t\t\t\t<div " + w.attrString("expand-button") + ">" + renderIcon(w.get("expand_icon"), newAttrs().add("aria-hidden", "true"), "i") + "</div>\n")
			b.WriteString("\t\t\t\t<div " + w.attrString("collapse-button") + ">" + renderIcon(w.get("collapse_icon"), newAttrs().add("aria-hidden", "true"), "i") + "</div>\n")
		}
		b.WriteString("\t\t\t</div>\n")
	}
	b.WriteString("\t\t<div " + w.attrString("body") + ">\n\t\t\t<div class=\"elementor-toc__spinner-container\">\n\t\t\t\t")
	b.WriteString(renderIcon(map[string]any{"library": "eicons", "value": "eicon-loading"},
		newAttrs().add("class", "elementor-toc__spinner", "eicon-animation-spin").add("aria-hidden", "true"), "i"))
	b.WriteString("\t\t\t</div>\n\t\t</div>\n")
	return b.String()
}

// --- video playlist -------------------------------------------------------------------------------

func proBRenderVideoPlaylist(w *elemCtx) string {
	items := asList(w.get("tabs"))
	idInt := idInt(w.el.ID)
	if len(idInt) > 3 {
		idInt = idInt[:3]
	}
	tag := validateHTMLTag(w.s("playlist_title_tag"))
	eid := escAttr(w.el.ID)
	showThumbs := w.has("show_thumbnail")
	playIcon := w.get("play_icon")
	watchedIcon := w.get("watched_icon")
	aria := func() *attrs { return newAttrs().add("aria-hidden", "true") }

	type tab struct {
		title, content, key, attr, class string
	}
	type item struct {
		typ, title, tag, url, duration, thumb string
		overlay, inner                        bool
		tabs                                  []tab
		titleAttrs, videoAttrs                *attrs
	}
	var list []*item
	count := 0
	for i, raw := range items {
		m := asMap(raw)
		if m == nil {
			continue
		}
		n := strconv.Itoa(i + 1)
		it := &item{typ: phpString(m["type"]), inner: truthy(m["inner_tab_is_content_visible"])}
		switch it.typ {
		case "youtube", "vimeo", "hosted":
			count++
			it.title = phpString(m["title"])
			it.tag = validateHTMLTag(phpString(m["video_html_tag"]))
			switch {
			case truthy(m["youtube_url"]) && it.typ == "youtube":
				it.url = phpString(m["youtube_url"])
			case truthy(m["vimeo_url"]) && it.typ == "vimeo":
				it.url = phpString(m["vimeo_url"])
			case truthy(m["external_url"]) && it.typ == "hosted" && phpString(m["is_external_url"]) == "yes":
				it.url = phpString(sub(m["external_url"], "url"))
			case truthy(m["hosted_url"]) && it.typ == "hosted" && phpString(m["is_external_url"]) != "yes":
				it.url = phpString(sub(m["hosted_url"], "url"))
			}
			if w.has("show_duration") {
				it.duration = phpString(m["duration"])
			}
			if showThumbs {
				it.thumb = phpString(sub(m["thumbnail"], "url"))
			}
			it.titleAttrs = newAttrs().add("id", "e-tab-title-"+idInt+n).add("class", "e-tab-title", "e-tab-desktop-title")
			if i == 0 {
				it.titleAttrs.add("aria-selected", "true")
			} else {
				it.titleAttrs.add("aria-selected", "false")
			}
			it.titleAttrs.add("data-tab", n).add("role", "tab")
			if i == 0 {
				it.titleAttrs.add("tabindex", "0")
			} else {
				it.titleAttrs.add("tabindex", "-1")
			}
			it.titleAttrs.add("aria-controls", "e-tab-content-"+idInt+n)
			if (truthy(m["inner_tab_content_1"]) || truthy(m["inner_tab_content_2"])) && it.inner {
				active := false
				titles := []string{w.s("inner_tab_title_1"), w.s("inner_tab_title_2")}
				for ti, c := range []any{m["inner_tab_content_1"], m["inner_tab_content_2"]} {
					t := tab{title: titles[ti], content: phpString(c), key: repeaterKey("inner_tab_content_"+strconv.Itoa(ti+1), "tabs", i)}
					if truthy(c) {
						if active {
							t.attr = "hidden"
						} else {
							t.class = "e-inner-tab-active"
						}
						active = true
					}
					it.tabs = append(it.tabs, t)
				}
			}
			if i == 0 && w.s("autoplay_on_load") != "yes" && w.has("show_image_overlay") {
				it.overlay = true
			}
		case "section":
			it.title = phpString(m["title"])
			it.tag = validateHTMLTag(phpString(m["section_html_tag"]))
		}
		it.videoAttrs = newAttrs().add("id", "e-tab-content-"+idInt+n).add("class", "e-tab-content", "elementor-clearfix").add("data-tab", n).
			add("role", "tabpanel").add("aria-labelledby", "e-tab-title-"+idInt+n).add("tabindex", "0").add("data-video-url", it.url).
			add("data-video-type", phpString(m["type"])).add("data-video-title", phpString(m["title"])).add("data-video-duration", phpString(m["duration"]))
		if i > 0 {
			it.videoAttrs.add("hidden", "hidden")
		}
		list = append(list, it)
	}
	overlayImage := ""
	if ov := asMap(w.get("image_overlay")); ov != nil && truthy(ov["url"]) {
		overlayImage = w.r.attachmentImageSrc(toInt64(ov["id"]), "image_overlay", w.disp.m)
		if len(overlayImage) < 2 {
			overlayImage = phpString(ov["url"])
		}
	}

	var b strings.Builder
	b.WriteString("\t\t<div class=\"e-tabs\" role=\"region\" aria-labelledby=\"e-playlist-" + eid + "\">\n\t\t\t<div class=\"e-tabs-main-area\">\n" +
		"\t\t\t\t<div class=\"e-tabs-wrapper\">\n\t\t\t\t\t<div class=\"e-tabs-header\">\n")
	b.WriteString("\t\t\t\t\t\t<" + tag + " class=\"e-tabs-title\" id=\"e-playlist-" + eid + "\">" + w.s("playlist_title") + "</" + tag + ">\n")
	b.WriteString("\t\t\t\t\t\t<div class=\"e-tabs-header-right-side\">\n")
	if w.has("show_video_count") {
		b.WriteString("\t\t\t\t\t\t\t\t<span class=\"e-tabs-videos-count\">" + strconv.Itoa(count) + " Videos</span>\n")
	}
	b.WriteString(renderIcon(map[string]any{"library": "eicons", "value": "eicon-caret-down"},
		newAttrs().add("aria-hidden", "true").add("class", "e-tabs-toggle-videos-display-button", "rotate-down"), "i"))
	b.WriteString("\t\t\t\t\t\t</div>\n\t\t\t\t\t</div>\n\t\t\t\t\t<div class=\"e-tabs-items-wrapper\">\n\t\t\t\t\t\t<div class=\"e-tabs-items\" role=\"tablist\">\n")
	for _, it := range list {
		if it.typ == "section" {
			b.WriteString("\t\t\t\t\t\t\t\t\t<" + it.tag + " class=\"e-section-title\">" + it.title + "</" + it.tag + ">\n")
			continue
		}
		if it.titleAttrs == nil {
			continue
		}
		b.WriteString("\t\t\t\t\t\t\t\t\t<div " + it.titleAttrs.String() + ">\n")
		icons := "<span class=\"icon-play\">" + renderIcon(playIcon, aria(), "i") + "</span>\n<span class=\"icon-watched\">" + renderIcon(watchedIcon, aria(), "i") + "</span>\n"
		if showThumbs {
			b.WriteString("<div class=\"e-tab-thumbnail\">\n")
			if it.thumb != "" {
				b.WriteString("<img src=\"" + escURL(it.thumb) + "\" alt=\"" + escAttr(it.title) + "\" loading=\"lazy\" />\n")
			}
			b.WriteString(icons + "</div>\n")
		} else {
			b.WriteString(icons)
		}
		b.WriteString("<" + it.tag + " class=\"e-tab-title-text\">\n<button type=\"button\">" + it.title + "</button>\n</" + it.tag + ">\n")
		if it.duration != "" {
			b.WriteString("<span class=\"e-tab-duration\">" + escHTML(it.duration) + "</span>\n")
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("\t\t\t\t\t\t</div>\n\t\t\t\t\t\t<div class=\"shadow shadow-top\" aria-hidden=\"true\"></div>\n" +
		"\t\t\t\t\t\t<div class=\"shadow shadow-bottom\" aria-hidden=\"true\"></div>\n\t\t\t\t\t</div>\n\t\t\t\t</div>\n")
	b.WriteString("\t\t\t\t<div class=\"e-tabs-content-wrapper\" role=\"tablist\" aria-orientation=\"vertical\">\n")
	for _, it := range list {
		b.WriteString("\t\t\t\t\t\t<div " + it.videoAttrs.String() + ">\n\t\t\t\t\t\t\t<div></div>\n")
		if it.overlay {
			b.WriteString("<div class=\"elementor-custom-embed-image-overlay elementor-clickable\" style=\"background-image: url('" + escURL(overlayImage) + "');\">\n")
			if ic := asMap(w.get("show_play_icon")); truthy(sub(ic, "value")) {
				b.WriteString("<div class=\"elementor-custom-embed-play\" role=\"button\" tabindex=\"0\" aria-label=\"Play Video\">\n" + renderIcon(ic, aria(), "i") + "</div>\n")
			}
			b.WriteString("</div>\n")
		}
		b.WriteString("\t\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t\t</div>\n\t\t\t</div>\n\t\t\t<div class=\"e-tabs-inner-tabs\" >\n")
	for _, it := range list {
		if !it.inner || it.typ == "section" {
			if !(it.inner && it.typ == "section") {
				continue
			}
		}
		b.WriteString("<div " + it.videoAttrs.String() + ">\n")
		if len(it.tabs) > 0 {
			b.WriteString("<div class=\"e-inner-tabs-wrapper\">\n")
			for _, t := range it.tabs {
				if t.content != "" {
					b.WriteString("<div class=\"e-inner-tab-title " + t.class + "\">\n<a class=\"e-inner-tab-title-text\" tabindex=\"0\">" + t.title + "</a>\n</div>\n")
				}
			}
			b.WriteString("</div>\n<div class=\"e-inner-tabs-content-wrapper\">\n")
			for _, t := range it.tabs {
				if t.content == "" {
					continue
				}
				b.WriteString("<div class=\"e-inner-tab-title e-tab-mobile-title " + t.class + "\">\n" + t.title + "</div>\n")
				b.WriteString("<div " + t.attr + " class=\"e-inner-tab-content " + t.class + " " + escAttr(w.s("inner_tab_is_content_collapsible")) + "\">\n<div class=\"e-inner-tab-text\">\n")
				w.addInlineEditing(t.key, "advanced")
				b.WriteString("<div " + w.attrString(t.key) + ">\n" + w.r.parseTextEditor(t.content) + "</div>\n</div>\n")
				b.WriteString("<div class=\"e-inner-tab-buttons\">\n<button class=\"show-button\">" + w.s("inner_tab_label_show_more") + "</button>\n<button>" + w.s("inner_tab_label_show_less") + "</button>\n</div>\n</div>\n")
			}
			b.WriteString("</div>\n")
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("\t\t\t</div>\n\t\t</div>\n")
	return b.String()
}

// --- slides ---------------------------------------------------------------------------------------------

func proBRenderSlides(w *elemCtx) string {
	slides := asList(w.get("slides"))
	if len(slides) == 0 {
		return ""
	}
	w.attr("wrapper").add("class", "elementor-slides-wrapper", "elementor-main-swiper", "swiper").add("role", "region").
		add("aria-roledescription", "carousel").add("aria-label", w.s("slides_name")).add("dir", "ltr").add("data-animation", w.s("content_animation"))
	titleTag := validateHTMLTag(w.s("slides_title_tag"))
	descTag := validateHTMLTag(w.s("slides_description_tag"))
	w.attr("button").add("class", "elementor-button", "elementor-slide-button")
	if w.has("button_size") {
		w.attr("button").add("class", "elementor-size-"+w.s("button_size"))
	}
	var out []string
	for i, raw := range slides {
		s := asMap(raw)
		if s == nil {
			continue
		}
		idx := strconv.Itoa(i)
		slideEl, btnEl := "div", "div"
		slideAttrs, btnAttrs := "", ""
		if truthy(sub(s["link"], "url")) {
			lk := "slide_link" + idx
			w.addLinkAttributes(lk, s["link"], false)
			if phpString(s["link_click"]) == "button" {
				btnEl, btnAttrs = "a", w.attrString(lk)
			} else {
				slideEl, slideAttrs = "a", w.attrString(lk)
			}
		}
		bg := w.attr("slide_bg_"+idx).add("class", "swiper-slide-bg")
		if truthy(s["background_ken_burns"]) {
			bg.add("class", "elementor-ken-burns", "elementor-ken-burns--"+phpString(s["zoom_direction"]))
		}
		if img := asMap(s["background_image"]); truthy(sub(img, "id")) {
			bg.add("role", "img").add("aria-label", w.r.imageAlt(img))
		}
		h := "<" + slideEl + " class=\"swiper-slide-inner\" " + slideAttrs + ">"
		h += "<div class=\"swiper-slide-contents\">"
		if truthy(s["heading"]) {
			h += "<" + titleTag + " class=\"elementor-slide-heading\">" + phpString(s["heading"]) + "</" + titleTag + ">"
		}
		if truthy(s["description"]) {
			h += "<" + descTag + " class=\"elementor-slide-description\">" + phpString(s["description"]) + "</" + descTag + ">"
		}
		if truthy(s["button_text"]) {
			h += "<" + btnEl + " " + btnAttrs + " " + w.attrString("button") + ">" + phpString(s["button_text"]) + "</" + btnEl + ">"
		}
		h += "</div></" + slideEl + ">"
		if phpString(s["background_overlay"]) == "yes" {
			h = "<div class=\"elementor-background-overlay\"></div>" + h
		}
		h = "<div " + bg.String() + "></div>" + h
		out = append(out, "<div class=\"elementor-repeater-item-"+escAttr(phpString(s["_id"]))+" swiper-slide\" role=\"group\" aria-roledescription=\"slide\">"+h+"</div>")
	}
	nav := w.s("navigation")
	var b strings.Builder
	b.WriteString("\t\t\t<div " + w.attrString("wrapper") + ">\n\t\t\t\t<div class=\"swiper-wrapper elementor-slides\">\n")
	b.WriteString(strings.Join(out, ""))
	b.WriteString("\t\t\t\t</div>\n")
	if len(slides) > 1 {
		if nav == "arrows" || nav == "both" {
			b.WriteString("<div class=\"elementor-swiper-button elementor-swiper-button-prev\" role=\"button\" tabindex=\"0\" aria-label=\"Previous slide\">\n" +
				renderIcon(map[string]any{"library": "eicons", "value": "eicon-chevron-left"}, newAttrs().add("aria-hidden", "true"), "i") + "</div>\n")
			b.WriteString("<div class=\"elementor-swiper-button elementor-swiper-button-next\" role=\"button\" tabindex=\"0\" aria-label=\"Next slide\">\n" +
				renderIcon(map[string]any{"library": "eicons", "value": "eicon-chevron-right"}, newAttrs().add("aria-hidden", "true"), "i") + "</div>\n")
		}
		if nav == "dots" || nav == "both" {
			b.WriteString("<div class=\"swiper-pagination\"></div>\n")
		}
	}
	b.WriteString("\t\t\t</div>\n")
	return b.String()
}

// proBSlidesStyles adds the content animation's stylesheet (the animation_slides_content control
// declares no asset of its own) before the widget's styles.
func proBSlidesStyles(w *elemCtx) []string {
	var out []string
	if a := w.s("content_animation"); a != "" && a != "none" {
		out = append(out, "e-animation-"+a)
	}
	return append(out, "e-swiper", "widget-slides")
}

// --- payment buttons ----------------------------------------------------------------------------------
// GoWebEditor renders the payment buttons' markup only: the PayPal button posts its (legacy) form
// straight to PayPal; the Stripe button's form needs the submit_stripe_form ajax action, which
// GoWebEditor does not provide (no payment processing).

var proBPayPalCycles = map[string]string{"days": "D", "weeks": "W", "months": "M", "years": "Y"}

// proBPaymentMessage mirrors Payment_Button::get_custom_message.
func proBPaymentMessage(w *elemCtx, id string) string {
	if m := w.s("error_message_" + id); m != "" {
		return m
	}
	switch id {
	case "global":
		return "An error occurred."
	case "payment":
		return "No payment method connected. Contact seller."
	}
	return "Unknown error."
}

// proBPaymentButton mirrors Payment_Button::render_button with the given tag.
func proBPaymentButton(w *elemCtx, tag string) string {
	w.attr("button").add("class", "elementor-payment-button")
	return "<" + tag + " " + w.attrString("button") + ">\n" + buttonText(w) + "</" + tag + ">\n"
}

// proBPaymentRender mirrors Payment_Button::render around a widget's render_button.
func proBPaymentRender(w *elemCtx, inner func() string) string {
	btn := w.attr("button")
	btn.add("class", "elementor-button").add("role", "button")
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
	return inner()
}

func proBErrorsHTML(errs [][2]string) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("<div class=\"elementor-message elementor-message-danger elementor-hidden elementor-error-message-" + e[0] + "\">\n" + escHTML(e[1]) + "</div>\n")
	}
	return b.String()
}

// proBNumeric mirrors print_numeric_setting (doubleval, at least min).
func proBNumeric(w *elemCtx, key string, min float64) string {
	n := toFloat(w.get(key))
	if min > n {
		n = min
	}
	return phpFloatString(n)
}

func proBRenderPayPal(w *elemCtx) string {
	return proBPaymentRender(w, func() string {
		if w.s("merchant_account") == "advanced" {
			return "" // the PayPal SDK flow needs a token server; not rendered upstream either
		}
		action, email := "https://www.paypal.com/cgi-bin/webscr", w.s("email")
		sandbox := w.s("sandbox_mode") == "yes"
		if sandbox {
			action, email = "https://sandbox.paypal.com/cgi-bin/webscr", w.s("sandbox_email")
			w.attr("button").add("class", "elementor-payment-sandbox-mode")
		}
		target := "_top"
		if w.s("open_in_new_window") == "yes" {
			target = "_blank"
		}
		typ := w.s("type")
		var cmd, priceName, priceValue, autoRenewal, cycle string
		switch typ {
		case "checkout":
			cmd, priceName, priceValue = "_xclick", "amount", w.s("product_price")
		case "donation":
			cmd, priceName = "_donations", "amount"
			if w.s("donation_type") == "fixed" {
				priceValue = w.s("donation_amount")
			}
		case "subscription":
			cmd, priceName, priceValue = "_xclick-subscriptions", "a3", w.s("product_price")
			autoRenewal = "0"
			if w.s("auto_renewal") == "yes" {
				autoRenewal = "1"
			}
			cycle = proBPayPalCycles[w.s("billing_cycle")]
		}
		var b strings.Builder
		b.WriteString("<form action=\"" + escAttr(action) + "\" method=\"post\" target=\"" + escAttr(target) + "\">\n")
		hidden := func(name, value string) {
			b.WriteString("<input type=\"hidden\" name=\"" + name + "\" value=\"" + value + "\" />\n")
		}
		hidden("cmd", escAttr(cmd))
		hidden("business", escAttr(email))
		hidden("lc", "US")
		hidden("item_name", escAttr(w.s("product_name")))
		hidden("item_number", escAttr(w.s("product_sku")))
		hidden("currency_code", escAttr(w.s("currency")))
		hidden(escAttr(priceName), escAttr(priceValue))
		b.WriteString("<input type=\"hidden\" name=\"no_note\" value=\"1\">\n")
		switch typ {
		case "checkout":
			hidden("shipping", proBNumeric(w, "shipping_price", 0))
			hidden("tax_rate", proBNumeric(w, "tax_rate", 0))
			hidden("quantity", proBNumeric(w, "quantity", 1))
		case "subscription":
			hidden("src", autoRenewal)
			hidden("p3", "1")
			hidden("t3", cycle)
			hidden("no-shipping", "1")
		}
		if u := phpString(sub(w.get("redirect_after_success"), "url")); u != "" {
			b.WriteString("<input type=\"hidden\" name=\"return\" value=\"" + escURL(u) + "\">\n")
		}
		w.attr("button").add("type", "submit").add("class", "elementor-paypal-legacy")
		b.WriteString(proBPaymentButton(w, "button"))
		b.WriteString(proBErrorsHTML(proBPayPalErrors(w)))
		b.WriteString("</form>\n")
		return b.String()
	})
}

// proBPayPalErrors mirrors PayPal_Button::get_errors (front end only).
func proBPayPalErrors(w *elemCtx) [][2]string {
	if w.editing() {
		return nil
	}
	var errs [][2]string
	if w.s("merchant_account") != "advanced" {
		if !w.has("email") || (w.s("sandbox_mode") == "yes" && !w.has("sandbox_email")) {
			errs = append(errs, [2]string{"payment", proBPaymentMessage(w, "payment")})
		}
	}
	if (w.s("type") != "donation" && !w.has("product_price")) || (w.s("donation_type") == "fixed" && !w.has("donation_amount")) ||
		(w.has("tax_type") && !w.has("tax_rate")) {
		errs = append([][2]string{{"global", proBPaymentMessage(w, "global")}}, errs...)
	}
	if len(errs) > 1 {
		return [][2]string{{"global", proBPaymentMessage(w, "global")}}
	}
	return errs
}

func proBRenderStripe(w *elemCtx) string {
	return proBPaymentRender(w, func() string {
		var b strings.Builder
		ajax, referer := "", ""
		if pd := w.r.rc.Pro; pd != nil {
			ajax = pd.AdminAjaxURL
			referer = pd.CurrentURL
			if i := strings.Index(referer, "://"); i >= 0 {
				if j := strings.Index(referer[i+3:], "/"); j >= 0 {
					referer = referer[i+3+j:]
				} else {
					referer = "/"
				}
			}
		}
		current := "/index.php" // $_SERVER['PHP_SELF'] of a front-end request
		b.WriteString("<form class=\"elementor-stripe-form\">\n")
		b.WriteString("<input type=\"hidden\" name=\"url\" value=\"" + escAttr(ajax) + "\">\n")
		b.WriteString("<input type=\"hidden\" name=\"action\" value=\"submit_stripe_form\"/>\n")
		b.WriteString("<input type=\"hidden\" name=\"widget_id\" value=\"" + escAttr(w.el.ID) + "\"/>\n")
		b.WriteString("<input type=\"hidden\" name=\"current_url\" value=\"" + escAttr(current) + "\"/>\n")
		b.WriteString("<input type=\"hidden\" name=\"custom_error_msg\" value=\"" + escAttr(w.s("custom_messages")) + "\" />\n")
		b.WriteString("<input type=\"hidden\" name=\"custom_error_msg_global\" value=\"" + escAttr(w.s("error_message_global")) + "\" />\n")
		b.WriteString("<input type=\"hidden\" name=\"custom_error_msg_payment\" value=\"" + escAttr(w.s("error_message_payment")) + "\" />\n")
		// wp_nonce_field( 'stripe_form_submit' ): GoWebEditor does not process Stripe payments, so the
		// nonce stays empty.
		b.WriteString("<input type=\"hidden\" id=\"stripe_form_submit_nonce\" name=\"stripe_form_submit_nonce\" value=\"\" />" +
			"<input type=\"hidden\" name=\"_wp_http_referer\" value=\"" + escAttr(referer) + "\" />")
		b.WriteString("<input type=\"hidden\" name=\"open_in_new_window\" value=\"" + escAttr(w.s("open_in_new_window")) + "\"/>\n")
		b.WriteString(proBPaymentButton(w, "button"))
		if !w.has("product_name") || !w.has("stripe_product_price") {
			b.WriteString(proBErrorsHTML([][2]string{{"global", proBPaymentMessage(w, "global")}}))
		}
		b.WriteString("</form>\n")
		return b.String()
	})
}

// --- template and global widgets ------------------------------------------------------------------------

// proBRenderTemplate mirrors Library\Widgets\Template::render: a published library template's
// content printed inline (get_builder_content_for_display). The nested template's CSS is printed
// inline before it, as upstream does in the editor; on the front end upstream enqueues the
// template's CSS file instead.
func proBRenderTemplate(w *elemCtx) string {
	id := toInt64(w.raw("template_id"))
	if id == 0 {
		return ""
	}
	pd := w.r.rc.Pro
	if pd == nil || pd.Template == nil {
		return ""
	}
	tdoc := pd.Template(id)
	if tdoc == nil || !tdoc.proBPublished() {
		return ""
	}
	body := proBNestedDocument(w, tdoc)
	if body == "" {
		return ""
	}
	return "\t\t<div class=\"elementor-template\">\n\t\t\t" + body + "\t\t</div>\n"
}

// proBPublished reports whether a template document is published (the server marks unpublished
// templates with __status in their settings; absence means published).
func (d *Document) proBPublished() bool {
	st, ok := d.Settings["__status"].(string)
	return !ok || st == "publish"
}

// proBNestedDocument renders a library document inside another (get_builder_content): its wrapper
// with data-elementor-post-type, its elements, and its stylesheet. Recursion is cut by
// remembering the documents being rendered.
func proBNestedDocument(w *elemCtx, tdoc *Document) string {
	if w.r.doc != nil && w.r.doc.ID == tdoc.ID {
		if w.editing() {
			return "<div class=\"elementor-alert elementor-alert-danger\">Invalid Data: The Template ID cannot be the same as the currently edited template. Please choose a different one.</div>"
		}
		return ""
	}
	lrc := *w.r.rc
	lrc.Editing = false
	// Recursion is cut by hiding this template from the documents rendered inside it.
	if w.r.rc.Pro != nil {
		cp := *w.r.rc.Pro
		parent, self := cp.Template, tdoc.ID
		cp.Template = func(id int64) *Document {
			if id == self || parent == nil {
				return nil
			}
			return parent(id)
		}
		lrc.Pro = &cp
	}
	res, err := Render(tdoc, &lrc)
	if err != nil || res == nil {
		return ""
	}
	w.r.styles = addUnique(w.r.styles, res.Styles...)
	w.r.scripts = addUnique(w.r.scripts, res.Scripts...)
	html := res.HTML
	// get_builder_content's wrapper carries the post type.
	if i := strings.Index(html, ">"); i > 0 && strings.HasPrefix(html, "<div ") && !strings.Contains(html[:i], "data-elementor-post-type") {
		html = html[:i] + " data-elementor-post-type=\"elementor_library\"" + html[i:]
	}
	if css := strings.TrimSpace(res.CSS); css != "" {
		html = "<style id=\"elementor-post-" + itoa64(tdoc.ID) + "\">" + css + "</style>" + html
	}
	return html
}

// --- global widget ------------------------------------------------------------------------------------

// ProBGlobalTemplateID returns a global widget's template id. Upstream keeps it in the element's
// "templateID" key, which Element does not decode; the server (or a later Element field) provides it.
var ProBGlobalTemplateID = func(el *Element) int64 { return toInt64(el.Settings["templateID"]) }

// proBGlobalWidget returns the widget saved in a global widget's template.
func proBGlobalWidget(w *elemCtx) *Element {
	id := ProBGlobalTemplateID(w.el)
	pd := w.r.rc.Pro
	if id == 0 || pd == nil || pd.Template == nil {
		return nil
	}
	tdoc := pd.Template(id)
	if tdoc == nil || len(tdoc.Elements) == 0 || tdoc.Elements[0] == nil || tdoc.Elements[0].ElType != "widget" {
		return nil
	}
	el := tdoc.Elements[0]
	cp := *el
	cp.ID = w.el.ID
	return &cp
}

// proBRenderGlobal mirrors Global_Widget::render_content: the original widget's content with the
// template's settings.
func proBRenderGlobal(w *elemCtx) string {
	orig := proBGlobalWidget(w)
	if orig == nil {
		return ""
	}
	oe := w.r.newElemCtx(orig)
	if oe == nil || oe.def == nil {
		return ""
	}
	// The wrapper says which widget it is (elementor-global-<id> elementor-widget-<type>).
	cp := *w.def
	cp.wrapperClass = "elementor-widget-global elementor-global-" + itoa64(ProBGlobalTemplateID(w.el)) + " elementor-widget-" + orig.WidgetType
	w.def = &cp
	return oe.widgetContent()
}

func proBGlobalStyles(w *elemCtx) []string {
	orig := proBGlobalWidget(w)
	if orig == nil {
		return nil
	}
	if d := widgetDefs[orig.WidgetType]; d != nil && d.styles != nil {
		if oe := w.r.newElemCtx(orig); oe != nil {
			return d.styles(oe)
		}
	}
	return nil
}
