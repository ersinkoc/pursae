package builder

// Golementor Pro widgets (group A): blockquote, flip-box, call-to-action, price-list,
// price-table, countdown, animated-headline, progress-tracker, code-highlight and lottie. Ports of
// the render() methods of upstream Pro 4.3.0's modules/<name>/widgets/<name>.php.

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

func init() {
	registerWidget("blockquote", &widgetDef{render: proARenderBlockquote, styles: static("widget-blockquote", "elementor-icons-fa-brands")})
	registerWidget("flip-box", &widgetDef{render: proARenderFlipBox, styles: static("widget-flip-box")})
	registerWidget("call-to-action", &widgetDef{render: proARenderCTA, styles: static("widget-call-to-action", "e-transitions")})
	registerWidget("price-list", &widgetDef{render: proARenderPriceList, styles: static("widget-price-list")})
	registerWidget("price-table", &widgetDef{render: proARenderPriceTable, styles: static("widget-price-table")})
	registerWidget("countdown", &widgetDef{render: proARenderCountdown, styles: static("widget-countdown")})
	registerWidget("animated-headline", &widgetDef{render: proARenderAnimatedHeadline, styles: static("widget-animated-headline")})
	registerWidget("progress-tracker", &widgetDef{render: proARenderProgressTracker, styles: static("widget-progress-tracker")})
	// Code Highlight: upstream loads Prism from a CDN; Golementor never loads external scripts, so
	// the handles are declared (asset parity) but nothing is printed for them.
	registerWidget("code-highlight", &widgetDef{render: proARenderCodeHighlight, styles: static("widget-code-highlight")})
	registerWidget("lottie", &widgetDef{render: proARenderLottie, styles: static("widget-lottie", "e-lottie"), scripts: []string{"lottie"}})
}

// --- PHP URL helpers -----------------------------------------------------------------------

// proARawURLEncode mirrors PHP rawurlencode (RFC 3986: keeps A-Z a-z 0-9 - _ . ~).
func proARawURLEncode(s string) string { return proAURLEncode(s, true) }

// proAPHPURLEncode mirrors PHP urlencode (keeps A-Z a-z 0-9 - _ ., space becomes +).
func proAPHPURLEncode(s string) string { return proAURLEncode(s, false) }

func proAURLEncode(s string, raw bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		case c == '~' && raw:
			b.WriteByte(c)
		case c == ' ' && !raw:
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// proAAddQueryArg mirrors add_query_arg( key, value, url ): existing arguments are decoded and
// re-encoded with urlencode, the new value is appended as given.
func proAAddQueryArg(u, key, value string) string {
	base, query, _ := strings.Cut(u, "?")
	type kv struct{ k, v string }
	var args []kv
	if query != "" {
		for _, part := range strings.Split(query, "&") {
			if part == "" {
				continue
			}
			k, v, _ := strings.Cut(part, "=")
			dk, err := url.QueryUnescape(k)
			if err != nil {
				dk = k
			}
			dv, err := url.QueryUnescape(v)
			if err != nil {
				dv = v
			}
			args = append(args, kv{dk, proAPHPURLEncode(dv)})
		}
	}
	replaced := false
	for i := range args {
		if args[i].k == key {
			args[i].v = value
			replaced = true
		}
	}
	if !replaced {
		args = append(args, kv{key, value})
	}
	var parts []string
	for _, a := range args {
		parts = append(parts, a.k+"="+a.v)
	}
	return base + "?" + strings.Join(parts, "&")
}

// --- blockquote ----------------------------------------------------------------------------

func proARenderBlockquote(w *elemCtx) string {
	content := w.s("blockquote_content")
	author := w.s("author_name")
	tweet := w.s("tweet_button") == "yes"
	if utilsIsEmpty(w.get("blockquote_content")) && utilsIsEmpty(w.get("author_name")) && !tweet {
		return ""
	}
	view := w.s("tweet_button_view")
	share := "https://twitter.com/intent/tweet"
	text := proARawURLEncode(content)
	if !utilsIsEmpty(w.get("author_name")) {
		text += " — " + author
	}
	share = proAAddQueryArg(share, "text", text)
	switch w.s("url_type") {
	case "current_page":
		cur := w.r.pro().CurrentURL
		if cur == "" {
			if p := w.currentPostAny(); p != nil {
				cur = p.URL
			} else {
				cur = w.homeURL()
			}
		}
		share = proAAddQueryArg(share, "url", proARawURLEncode(cur))
	case "custom":
		share = proAAddQueryArg(share, "url", proARawURLEncode(w.s("url")))
	}
	if user := w.s("user_name"); user != "" {
		user = strings.TrimPrefix(user, "@")
		share = proAAddQueryArg(share, "via", proARawURLEncode(user))
	}
	w.attr("blockquote_content").add("class", "elementor-blockquote__content")
	w.attr("author_name").add("class", "elementor-blockquote__author")
	w.attr("tweet_button_label").add("class", "elementor-blockquote__tweet-label")
	w.addInlineEditing("blockquote_content", "")
	w.addInlineEditing("author_name", "none")
	w.addInlineEditing("tweet_button_label", "none")
	var b strings.Builder
	b.WriteString("\t\t<blockquote class=\"elementor-blockquote\">\n\t\t\t<p " + w.attrString("blockquote_content") + ">\n\t\t\t\t" + ksesPost(content) + "\t\t\t</p>\n")
	if !utilsIsEmpty(w.get("author_name")) || tweet {
		b.WriteString("\t\t\t\t<div class=\"e-q-footer\">\n")
		if !utilsIsEmpty(w.get("author_name")) {
			b.WriteString("\t\t\t\t\t<cite " + w.attrString("author_name") + ">" + ksesPost(author) + "</cite>\n")
		}
		if tweet {
			b.WriteString("\t\t\t\t\t<a href=\"" + escAttr(share) + "\" class=\"elementor-blockquote__tweet-button\" target=\"_blank\">\n")
			if view != "text" {
				icon := map[string]any{"value": "fab fa-twitter", "library": "fa-brands"}
				if svg := renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i"); svg != "" {
					b.WriteString(svg)
				} else {
					b.WriteString(`<i class="fa fa-twitter" aria-hidden="true"></i>`)
				}
				if view != "icon-text" {
					b.WriteString("\t\t\t\t\t\t\t\t<span class=\"elementor-screen-only\">Tweet</span>\n")
				}
			}
			if view == "icon-text" || view == "text" {
				b.WriteString("\t\t\t\t\t\t\t<span " + w.attrString("tweet_button_label") + ">" + ksesPost(w.s("tweet_button_label")) + "</span>\n")
			}
			b.WriteString("\t\t\t\t\t</a>\n")
		}
		b.WriteString("\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t</blockquote>\n\t\t")
	return b.String()
}

// --- shared: icon / graphic element --------------------------------------------------------

// proAGraphicIcon prints the icon of flip-box / call-to-action ($settings['icon'] fallback).
func proAGraphicIcon(w *elemCtx) string {
	_, hasOld := w.disp.m["icon"]
	isNew := !w.has("icon")
	_ = hasOld
	if isNew || w.isMigrated("selected_icon") {
		return renderIcon(w.get("selected_icon"), newAttrs().add("aria-hidden", "true"), "i")
	}
	return "<i " + w.attrString("icon") + "></i>"
}

func proAHasIcon(w *elemCtx) bool {
	return w.has("icon") || truthy(w.get("selected_icon"))
}

// --- flip box ------------------------------------------------------------------------------

func proARenderFlipBox(w *elemCtx) string {
	wrapperTag, buttonTag := "div", "a"
	titleTag := validateHTMLTag(w.s("title_tag"))
	descTag := validateHTMLTag(w.s("description_tag"))
	w.attr("button").add("class", "elementor-flip-box__button", "elementor-button", "elementor-size-"+w.s("button_size"))
	w.attr("wrapper").add("class", "elementor-flip-box__layer elementor-flip-box__back")
	if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
		el := "button"
		if w.s("link_click") == "box" {
			wrapperTag, buttonTag, el = "a", "span", "wrapper"
		}
		w.addLinkAttributes(el, lm, false)
	} else {
		w.attr("button").add("role", "button").add("tabindex", "0")
	}
	if w.s("graphic_element") == "icon" {
		iw := w.attr("icon-wrapper")
		iw.add("class", "elementor-icon-wrapper")
		iw.add("class", "elementor-view-"+w.s("icon_view"))
		if w.s("icon_view") != "default" {
			iw.add("class", "elementor-shape-"+w.s("icon_shape"))
		}
		if w.has("icon") {
			w.attr("icon").add("class", w.s("icon")).add("aria-hidden", "true")
		}
	}
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-flip-box\" tabindex=\"0\">\n\t\t\t<div class=\"elementor-flip-box__layer elementor-flip-box__front\">\n")
	b.WriteString("\t\t\t\t<div class=\"elementor-flip-box__layer__overlay\">\n\t\t\t\t\t<div class=\"elementor-flip-box__layer__inner\">\n")
	if w.s("graphic_element") == "image" && truthy(sub(w.get("image"), "url")) {
		b.WriteString("\t\t\t\t\t\t\t<div class=\"elementor-flip-box__image\">\n\t\t\t\t\t\t\t\t" + w.r.attachmentImageHTML(w.disp.m, "image", "image") + "\t\t\t\t\t\t\t</div>\n")
	} else if w.s("graphic_element") == "icon" && proAHasIcon(w) {
		b.WriteString("\t\t\t\t\t\t\t<div " + w.attrString("icon-wrapper") + ">\n\t\t\t\t\t\t\t\t<div class=\"elementor-icon\">\n\t\t\t\t\t\t\t\t\t")
		b.WriteString(proAGraphicIcon(w))
		b.WriteString("\t\t\t\t\t\t\t\t</div>\n\t\t\t\t\t\t\t</div>\n")
	}
	if !utilsIsEmpty(w.get("title_text_a")) {
		b.WriteString("\t\t\t\t\t\t\t<" + titleTag + " class=\"elementor-flip-box__layer__title\">\n\t\t\t\t\t\t\t\t" + ksesPost(w.s("title_text_a")) + "\t\t\t\t\t\t\t</" + titleTag + ">\n")
	}
	if !utilsIsEmpty(w.get("description_text_a")) {
		b.WriteString("\t\t\t\t\t\t\t<" + descTag + " class=\"elementor-flip-box__layer__description\">\n\t\t\t\t\t\t\t\t" + ksesPost(w.s("description_text_a")) + "\t\t\t\t\t\t\t</" + descTag + ">\n")
	}
	b.WriteString("\t\t\t\t\t</div>\n\t\t\t\t</div>\n\t\t\t</div>\n")
	b.WriteString("\t\t\t<" + wrapperTag + " " + w.attrString("wrapper") + ">\n\t\t\t<div class=\"elementor-flip-box__layer__overlay\">\n\t\t\t\t<div class=\"elementor-flip-box__layer__inner\">\n")
	if !utilsIsEmpty(w.get("title_text_b")) {
		b.WriteString("\t\t\t\t\t\t<" + titleTag + " class=\"elementor-flip-box__layer__title\">\n\t\t\t\t\t\t\t" + ksesPost(w.s("title_text_b")) + "\t\t\t\t\t\t</" + titleTag + ">\n")
	}
	if !utilsIsEmpty(w.get("description_text_b")) {
		b.WriteString("\t\t\t\t\t\t<" + descTag + " class=\"elementor-flip-box__layer__description\">\n\t\t\t\t\t\t\t" + ksesPost(w.s("description_text_b")) + "\t\t\t\t\t\t</" + descTag + ">\n")
	}
	if !utilsIsEmpty(w.get("button_text")) {
		b.WriteString("\t\t\t\t\t\t<" + buttonTag + " " + w.attrString("button") + ">\n\t\t\t\t\t\t\t" + ksesPost(w.s("button_text")) + "\t\t\t\t\t\t</" + buttonTag + ">\n")
	}
	b.WriteString("\t\t\t</div>\n\t\t</div>\n\t\t</" + wrapperTag + ">\n\t\t</div>\n\t\t")
	return b.String()
}

// --- call to action -------------------------------------------------------------------------

func proARenderCTA(w *elemCtx) string {
	wrapperTag, buttonTag := "div", "a"
	titleTag := validateHTMLTag(w.s("title_tag"))
	descTag := validateHTMLTag(w.s("description_tag"))
	contentAnimation := w.s("content_animation")
	animationClass := ""
	printBg, printContent := true, true
	bgImage := ""
	bg := asMap(w.get("bg_image"))
	if id := toInt64(sub(bg, "id")); id > 0 {
		bgImage = w.r.attachmentImageSrc(id, "bg_image", w.disp.m)
	} else if truthy(sub(bg, "url")) {
		bgImage = phpString(sub(bg, "url"))
	}
	if bgImage == "" && w.s("skin") == "classic" {
		printBg = false
	}
	ge := w.s("graphic_element")
	if utilsIsEmpty(w.get("title")) && utilsIsEmpty(w.get("description")) && utilsIsEmpty(w.get("button")) && ge == "none" {
		printContent = false
	}
	w.attr("wrapper").add("class", "elementor-cta")
	bgAlt := ""
	if bg != nil {
		bgAlt = w.r.imageAlt(bg)
	}
	w.attr("background_image").add("style", "background-image: url("+escURL(bgImage)+");").add("role", "img").add("aria-label", bgAlt)
	w.attr("title").add("class", "elementor-cta__title", "elementor-cta__content-item", "elementor-content-item")
	w.attr("description").add("class", "elementor-cta__description", "elementor-cta__content-item", "elementor-content-item")
	w.attr("button").add("class", "elementor-cta__button", "elementor-button", "elementor-size-"+w.s("button_size"))
	w.attr("graphic_element").add("class", "elementor-content-item", "elementor-cta__content-item")
	if ge == "icon" {
		g := w.attr("graphic_element")
		g.add("class", "elementor-icon-wrapper", "elementor-cta__icon")
		g.add("class", "elementor-view-"+w.s("icon_view"))
		if w.s("icon_view") != "default" {
			g.add("class", "elementor-shape-"+w.s("icon_shape"))
		}
		if w.has("icon") {
			w.attr("icon").add("class", w.s("icon"))
		}
	} else if ge == "image" && truthy(sub(w.get("graphic_image"), "url")) {
		w.attr("graphic_element").add("class", "elementor-cta__image")
	}
	if contentAnimation != "" && w.s("skin") == "cover" {
		animationClass = "elementor-animated-item--" + contentAnimation
		w.attr("title").add("class", animationClass)
		w.attr("graphic_element").add("class", animationClass)
		w.attr("description").add("class", animationClass)
	}
	if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
		el := "button"
		if w.s("link_click") == "box" {
			wrapperTag, buttonTag, el = "a", "span", "wrapper"
		}
		w.addLinkAttributes(el, lm, false)
	}
	w.addInlineEditing("title", "")
	w.addInlineEditing("description", "")
	w.addInlineEditing("button", "")
	var b strings.Builder
	b.WriteString("\t\t<" + wrapperTag + " " + w.attrString("wrapper") + ">\n")
	if printBg {
		b.WriteString("\t\t\t<div class=\"elementor-cta__bg-wrapper\">\n\t\t\t\t<div class=\"elementor-cta__bg elementor-bg\" " + w.attrString("background_image") + "></div>\n\t\t\t\t<div class=\"elementor-cta__bg-overlay\"></div>\n\t\t\t</div>\n")
	}
	if printContent {
		b.WriteString("\t\t\t<div class=\"elementor-cta__content\">\n")
		if ge == "image" && truthy(sub(w.get("graphic_image"), "url")) {
			b.WriteString("\t\t\t\t\t<div " + w.attrString("graphic_element") + ">\n\t\t\t\t\t\t" + w.r.attachmentImageHTML(w.disp.m, "graphic_image", "graphic_image") + "\t\t\t\t\t</div>\n")
		} else if ge == "icon" && proAHasIcon(w) {
			b.WriteString("\t\t\t\t\t<div " + w.attrString("graphic_element") + ">\n\t\t\t\t\t\t<div class=\"elementor-icon\">\n\t\t\t\t\t\t\t")
			b.WriteString(proAGraphicIcon(w))
			b.WriteString("\t\t\t\t\t\t</div>\n\t\t\t\t\t</div>\n")
		}
		if !utilsIsEmpty(w.get("title")) {
			b.WriteString("\t\t\t\t\t<" + titleTag + " " + w.attrString("title") + ">\n\t\t\t\t\t\t" + ksesPost(w.s("title")) + "\t\t\t\t\t</" + titleTag + ">\n")
		}
		if !utilsIsEmpty(w.get("description")) {
			b.WriteString("\t\t\t\t\t<" + descTag + " " + w.attrString("description") + ">\n\t\t\t\t\t\t" + ksesPost(w.s("description")) + "\t\t\t\t\t</" + descTag + ">\n")
		}
		if !utilsIsEmpty(w.get("button")) {
			b.WriteString("\t\t\t\t\t<div class=\"elementor-cta__button-wrapper elementor-cta__content-item elementor-content-item " + escAttr(animationClass) + "\">\n")
			b.WriteString("\t\t\t\t\t<" + buttonTag + " " + w.attrString("button") + ">\n\t\t\t\t\t\t" + ksesPost(w.s("button")) + "\t\t\t\t\t</" + buttonTag + ">\n\t\t\t\t\t</div>\n")
		}
		b.WriteString("\t\t\t</div>\n")
	}
	b.WriteString(proARibbon(w, "ribbon_title"))
	b.WriteString("\t\t</" + wrapperTag + ">\n\t\t")
	return b.String()
}

// proARibbon prints the ribbon of call-to-action and price-table.
func proARibbon(w *elemCtx, key string) string {
	if utilsIsEmpty(w.get(key)) {
		return ""
	}
	rw := w.attr("ribbon-wrapper")
	rw.add("class", "elementor-ribbon")
	if pos := w.s("ribbon_horizontal_position"); pos != "" {
		rw.add("class", "elementor-ribbon-"+pos)
	}
	w.attr(key).add("class", "elementor-ribbon-inner")
	w.addInlineEditing(key, "")
	return "\t\t\t<div " + rw.String() + ">\n\t\t\t\t<div " + w.attrString(key) + ">\n\t\t\t\t\t" + ksesPost(w.s(key)) + "\t\t\t\t</div>\n\t\t\t</div>\n"
}

// --- price list -----------------------------------------------------------------------------

func proARenderPriceList(w *elemCtx) string {
	var b strings.Builder
	b.WriteString("\t\t<ul class=\"elementor-price-list\">\n")
	titleTag := validateHTMLTag(w.s("title_tag"))
	descTag := validateHTMLTag(w.s("description_tag"))
	for i, it := range asList(w.get("price_list")) {
		item := asMap(it)
		if item == nil {
			continue
		}
		if utilsIsEmpty(item["title"]) && utilsIsEmpty(item["price"]) && utilsIsEmpty(item["item_description"]) {
			continue
		}
		tk := repeaterKey("title", "price_list", i)
		dk := repeaterKey("item_description", "price_list", i)
		w.addInlineEditing(tk, "")
		w.addInlineEditing(dk, "")
		w.attr(tk).add("class", "elementor-price-list-title")
		w.attr(dk).add("class", "elementor-price-list-description")
		link := asMap(item["link"])
		hasLink := link != nil && truthy(link["url"])
		if hasLink {
			lk := "item-link-" + phpString(item["_id"])
			w.attr(lk).add("class", "elementor-price-list-item")
			w.addLinkAttributes(lk, link, false)
			b.WriteString("<li><a " + w.attrString(lk) + ">")
		} else {
			b.WriteString(`<li class="elementor-price-list-item">`)
		}
		if truthy(sub(item["image"], "url")) {
			b.WriteString("\t\t\t\t\t<div class=\"elementor-price-list-image\">\n\t\t\t\t\t" + proAPriceListImage(w, item) + "\t\t\t\t</div>\n")
		}
		b.WriteString("\t\t\t\t<div class=\"elementor-price-list-text\">\n")
		if !utilsIsEmpty(item["title"]) || !utilsIsEmpty(item["price"]) {
			b.WriteString("\t\t\t\t\t<div class=\"elementor-price-list-header\">\n")
			if !utilsIsEmpty(item["title"]) {
				b.WriteString("\t\t\t\t\t\t<" + titleTag + " " + w.attrString(tk) + ">\n\t\t\t\t\t\t\t" + phpString(item["title"]) + "\t\t\t\t\t\t</" + titleTag + ">\n")
			}
			if w.s("separator_style") != "none" {
				b.WriteString("\t\t\t\t\t\t\t<span class=\"elementor-price-list-separator\"></span>\n")
			}
			if !utilsIsEmpty(item["price"]) {
				b.WriteString("\t\t\t\t\t\t\t<span class=\"elementor-price-list-price\">" + phpString(item["price"]) + "</span>\n")
			}
			b.WriteString("\t\t\t\t</div>\n")
		}
		if !utilsIsEmpty(item["item_description"]) {
			b.WriteString("\t\t\t\t\t\t<" + descTag + " " + w.attrString(dk) + ">\n\t\t\t\t\t\t\t" + phpString(item["item_description"]) + "\t\t\t\t\t\t</" + descTag + ">\n")
		}
		b.WriteString("\t\t\t</div>\n")
		if hasLink {
			b.WriteString("</a></li>")
		} else {
			b.WriteString("</li>")
		}
	}
	b.WriteString("\t\t</ul>\n\t\t")
	return b.String()
}

func proAPriceListImage(w *elemCtx, item map[string]any) string {
	id := toInt64(sub(item["image"], "id"))
	size := w.s("image_size_size")
	src := ""
	if size == "custom" {
		src = w.r.attachmentImageSrc(id, "image_size", w.disp.m)
	} else if a := w.r.attachment(id); a != nil {
		src, _, _, _ = a.imageSrc(size)
	}
	return `<img src="` + escURL(src) + `" alt="` + escAttr(ksesPost(phpString(item["title"]))) + `" loading="lazy" />`
}

// --- price table ----------------------------------------------------------------------------

var proACurrencySymbols = map[string]string{
	"dollar": "&#36;", "euro": "&#128;", "franc": "&#8355;", "pound": "&#163;", "ruble": "&#8381;", "shekel": "&#8362;",
	"baht": "&#3647;", "yen": "&#165;", "won": "&#8361;", "guilder": "&fnof;", "peso": "&#8369;", "peseta": "&#8359",
	"lira": "&#8356;", "rupee": "&#8360;", "indian_rupee": "&#8377;", "real": "R$", "krona": "kr",
}

func proARenderPriceTable(w *elemCtx) string {
	symbol := ""
	if cs := w.s("currency_symbol"); cs != "" {
		if cs != "custom" {
			symbol = proACurrencySymbols[cs]
		} else {
			symbol = w.s("currency_symbol_custom")
		}
	}
	format := w.s("currency_format")
	if format == "" {
		format = "."
	}
	parts := strings.Split(w.s("price"), format)
	intpart, fraction := parts[0], ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	position := phpString(w.raw("currency_position"))
	if position == "" {
		position = "before"
	}
	currency := func(loc string) string {
		if symbol != "" && loc == position {
			return `<span class="elementor-price-table__currency">` + escHTML(symbol) + `</span>`
		}
		return ""
	}
	bt := w.attr("button_text")
	bt.add("class", "elementor-price-table__button", "elementor-button", "elementor-size-"+w.s("button_size"))
	if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
		w.addLinkAttributes("button_text", lm, false)
	}
	if w.has("button_hover_animation") {
		bt.add("class", "elementor-animation-"+w.s("button_hover_animation"))
	}
	w.attr("heading").add("class", "elementor-price-table__heading")
	w.attr("sub_heading").add("class", "elementor-price-table__subheading")
	w.attr("period").add("class", "elementor-price-table__period", "elementor-typo-excluded")
	w.attr("footer_additional_info").add("class", "elementor-price-table__additional_info")
	w.addInlineEditing("heading", "none")
	w.addInlineEditing("sub_heading", "none")
	w.addInlineEditing("period", "none")
	w.addInlineEditing("footer_additional_info", "")
	w.addInlineEditing("button_text", "")
	periodPos := w.s("period_position")
	period := "<span " + w.attrString("period") + ">" + ksesPost(w.s("period")) + "</span>"
	headingTag := validateHTMLTag(w.s("heading_tag"))
	hasPeriod := !utilsIsEmpty(w.get("period"))
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-price-table\">\n")
	if w.has("heading") || w.has("sub_heading") {
		b.WriteString("\t\t\t\t<div class=\"elementor-price-table__header\">\n")
		if !utilsIsEmpty(w.get("heading")) {
			b.WriteString("\t\t\t\t\t\t<" + headingTag + " " + w.attrString("heading") + ">\n\t\t\t\t\t\t" + ksesPost(w.s("heading")) + "\t\t\t\t\t\t</" + headingTag + ">\n")
		}
		if !utilsIsEmpty(w.get("sub_heading")) {
			b.WriteString("\t\t\t\t\t\t<span " + w.attrString("sub_heading") + ">\n\t\t\t\t\t\t\t" + ksesPost(w.s("sub_heading")) + "\t\t\t\t\t\t</span>\n")
		}
		b.WriteString("\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t<div class=\"elementor-price-table__price\">\n")
	if w.s("sale") == "yes" && !utilsIsEmpty(w.get("original_price")) {
		b.WriteString("\t\t\t\t\t<div class=\"elementor-price-table__original-price elementor-typo-excluded\">\n\t\t\t\t\t\t" +
			currency("before") + ksesPost(w.s("original_price")) + currency("after") + "\t\t\t\t\t</div>\n")
	}
	b.WriteString(currency("before"))
	if intpart != "" {
		b.WriteString("\t\t\t\t\t<span class=\"elementor-price-table__integer-part\">\n\t\t\t\t\t\t" + intpart + "\t\t\t\t\t</span>\n")
	}
	if fraction != "" || (hasPeriod && periodPos == "beside") {
		b.WriteString("\t\t\t\t\t<div class=\"elementor-price-table__after-price\">\n\t\t\t\t\t\t<span class=\"elementor-price-table__fractional-part\">\n\t\t\t\t\t\t\t" + fraction + "\t\t\t\t\t\t</span>\n")
		if hasPeriod && periodPos == "beside" {
			b.WriteString(period)
		}
		b.WriteString("\t\t\t\t\t</div>\n")
	}
	b.WriteString(currency("after"))
	if hasPeriod && periodPos == "below" {
		b.WriteString(period)
	}
	b.WriteString("\t\t\t</div>\n")
	if items := asList(w.get("features_list")); len(items) > 0 {
		b.WriteString("\t\t\t\t<ul class=\"elementor-price-table__features-list\">\n")
		for i, it := range items {
			item := asMap(it)
			if item == nil {
				item = map[string]any{}
			}
			key := repeaterKey("item_text", "features_list", i)
			w.addInlineEditing(key, "")
			_, oldSet := item["item_icon"]
			_, migrated := asMap(item["__fa4_migrated"])["selected_item_icon"]
			isNew := !oldSet
			b.WriteString("\t\t\t\t\t\t<li class=\"elementor-repeater-item-" + escAttr(phpString(item["_id"])) + "\">\n\t\t\t\t\t\t\t<div class=\"elementor-price-table__feature-inner\">\n")
			if truthy(item["item_icon"]) || truthy(item["selected_item_icon"]) {
				if isNew || migrated {
					b.WriteString(renderIcon(item["selected_item_icon"], newAttrs().add("aria-hidden", "true"), "i"))
				} else {
					b.WriteString(`<i class="` + escAttr(phpString(item["item_icon"])) + `" aria-hidden="true"></i>`)
				}
			}
			if !utilsIsEmpty(item["item_text"]) {
				b.WriteString("\t\t\t\t\t\t\t\t\t<span " + w.attrString(key) + ">\n\t\t\t\t\t\t\t\t\t\t" + ksesPost(phpString(item["item_text"])) + "\t\t\t\t\t\t\t\t\t</span>\n")
			} else {
				b.WriteString("&nbsp;")
			}
			b.WriteString("\t\t\t\t\t\t\t</div>\n\t\t\t\t\t\t</li>\n")
		}
		b.WriteString("\t\t\t\t</ul>\n")
	}
	if !utilsIsEmpty(w.get("button_text")) || !utilsIsEmpty(w.get("footer_additional_info")) {
		b.WriteString("\t\t\t\t<div class=\"elementor-price-table__footer\">\n")
		if !utilsIsEmpty(w.get("button_text")) {
			b.WriteString("\t\t\t\t\t\t<a " + bt.String() + ">\n\t\t\t\t\t\t\t" + ksesPost(w.s("button_text")) + "\t\t\t\t\t\t</a>\n")
		}
		if !utilsIsEmpty(w.get("footer_additional_info")) {
			b.WriteString("\t\t\t\t\t\t<div " + w.attrString("footer_additional_info") + ">\n\t\t\t\t\t\t\t" + ksesPost(w.s("footer_additional_info")) + "\t\t\t\t\t\t</div>\n")
		}
		b.WriteString("\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t</div>\n\n\t\t")
	if w.s("show_ribbon") == "yes" {
		b.WriteString(proARibbon(w, "ribbon_title"))
	}
	return b.String()
}

// --- countdown ------------------------------------------------------------------------------

var proACountdownLabels = map[string]string{"label_days": "Days", "label_hours": "Hours", "label_minutes": "Minutes", "label_seconds": "Seconds"}

func proARenderCountdown(w *elemCtx) string {
	item := func(label, class string) string {
		s := `<div class="elementor-countdown-item"><span class="elementor-countdown-digits ` + class + `"></span>`
		if w.has("show_labels") {
			l := proACountdownLabels[label]
			if w.has("custom_labels") {
				l = w.s(label)
			}
			s += ` <span class="elementor-countdown-label">` + l + `</span>`
		}
		return s + "</div>"
	}
	var str strings.Builder
	if w.has("show_days") {
		str.WriteString(item("label_days", "elementor-countdown-days"))
	}
	if w.has("show_hours") {
		str.WriteString(item("label_hours", "elementor-countdown-hours"))
	}
	if w.has("show_minutes") {
		str.WriteString(item("label_minutes", "elementor-countdown-minutes"))
	}
	if w.has("show_seconds") {
		str.WriteString(item("label_seconds", "elementor-countdown-seconds"))
	}
	due := w.s("due_date")
	div := w.attr("div")
	if w.s("countdown_type") == "evergreen" {
		interval := toInt(w.get("evergreen_counter_hours"))*3600 + toInt(w.get("evergreen_counter_minutes"))*60
		div.add("data-evergreen-interval", strconv.Itoa(interval))
	} else {
		loc := time.UTC
		if pd := w.r.rc.Pro; pd != nil && !pd.Now.IsZero() {
			loc = pd.Now.Location()
		}
		t := time.Now().In(loc)
		for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if p, err := time.ParseInLocation(layout, strings.TrimSpace(due), loc); err == nil {
				t = p
				break
			}
		}
		due = strconv.FormatInt(t.Unix(), 10)
	}
	var actions []*omap
	if !w.editing() {
		for _, a := range asList(w.get("expire_actions")) {
			typ := phpString(a)
			o := newOmap()
			o.Set("type", escHTML(typ))
			if typ == "redirect" {
				u := phpString(sub(w.get("expire_redirect_url"), "url"))
				if u == "" {
					continue
				}
				if pu, err := url.Parse(u); err == nil && pu.Scheme != "" && pu.Host != "" {
					o.Set("redirect_url", escURL(escURL(u)))
				} else {
					o.Set("redirect_url", nil)
				}
			}
			actions = append(actions, o)
		}
	}
	if len(actions) > 0 {
		list := make([]any, len(actions))
		for i, a := range actions {
			list[i] = a
		}
		div.add("data-expire-actions", phpJSON(list))
	}
	div.add("class", "elementor-countdown-wrapper")
	div.add("data-date", due)
	var b strings.Builder
	b.WriteString("\t\t<div " + div.String() + ">\n\t\t\t" + str.String() + "\t\t</div>\n\t\t")
	for _, a := range actions {
		if phpString(a.Get("type")) != "message" {
			continue
		}
		b.WriteString("\t\t\t\t<div class=\"elementor-countdown-expire--message\">\n\t\t\t\t\t" + escHTML(w.s("message_after_expire")) + "\t\t\t\t</div>\n\t\t\t\t")
	}
	return b.String()
}

// --- animated headline ----------------------------------------------------------------------

func proARenderAnimatedHeadline(w *elemCtx) string {
	tag := validateHTMLTag(w.s("tag"))
	h := w.attr("headline")
	h.add("class", "elementor-headline")
	style := w.s("headline_style")
	if style == "rotate" {
		at := w.s("animation_type")
		h.add("class", "elementor-headline-animation-type-"+at)
		switch at {
		case "typing", "swirl", "blinds", "wave":
			h.add("class", "elementor-headline-letters")
		}
	}
	var b strings.Builder
	lm := asMap(w.get("link"))
	hasLink := lm != nil && truthy(lm["url"])
	if hasLink {
		w.addLinkAttributes("url", lm, false)
		b.WriteString("\t\t\t<a " + w.attrString("url") + ">\n\t\t\t")
	}
	b.WriteString("\t\t<" + tag + " " + h.String() + ">\n")
	if !utilsIsEmpty(w.get("before_text")) {
		b.WriteString("\t\t\t\t<span class=\"elementor-headline-plain-text elementor-headline-text-wrapper\">" + ksesPost(w.s("before_text")) + "</span>\n")
	}
	b.WriteString("\t\t\t\t<span class=\"elementor-headline-dynamic-wrapper elementor-headline-text-wrapper\">\n")
	if style == "rotate" && w.has("rotating_text") {
		for i, text := range strings.Split(w.s("rotating_text"), "\n") {
			cls := "elementor-headline-dynamic-text"
			if i < 1 {
				cls += " elementor-headline-text-active"
			}
			b.WriteString("\t\t\t\t\t<span class=\"" + cls + "\">\n\t\t\t\t" + strings.ReplaceAll(text, " ", "&nbsp;") + "\t\t\t</span>\n")
		}
	} else if style == "highlight" && !utilsIsEmpty(w.get("highlighted_text")) {
		b.WriteString("\t\t\t\t\t<span class=\"elementor-headline-dynamic-text elementor-headline-text-active\">" + ksesPost(w.s("highlighted_text")) + "</span>\n")
	}
	b.WriteString("\t\t\t\t</span>\n")
	if !utilsIsEmpty(w.get("after_text")) {
		b.WriteString("\t\t\t\t<span class=\"elementor-headline-plain-text elementor-headline-text-wrapper\">" + ksesPost(w.s("after_text")) + "</span>\n")
	}
	b.WriteString("\t\t\t\t</" + tag + ">\n\t\t")
	if hasLink {
		b.WriteString("</a>")
	}
	return b.String()
}

// --- progress tracker -----------------------------------------------------------------------

func proARenderProgressTracker(w *elemCtx) string {
	typ := w.s("type")
	w.attr("scrolling-percentage").add("class", "current-progress-percentage")
	w.attr("scrolling-tracker").add("class", "elementor-scrolling-tracker", "elementor-scrolling-tracker-"+typ, "elementor-scrolling-tracker-alignment-"+w.s("align"))
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString("scrolling-tracker") + ">\n")
	if typ == "horizontal" {
		b.WriteString("\t\t\t\t<div class=\"current-progress\">\n\t\t\t\t\t<div " + w.attrString("scrolling-percentage") + "></div>\n\t\t\t\t</div>\n")
	} else {
		b.WriteString("\t\t\t\t<svg\n\t\t\t\t\t\twidth=\"100%\"\n\t\t\t\t\t\theight=\"100%\">\n\t\t\t\t\t<circle class=\"circle\"\n\t\t\t\t\t\t\tr=\"40%\"\n\t\t\t\t\t\t\tcx=\"50%\"\n\t\t\t\t\t\t\tcy=\"50%\"/>\n" +
			"\t\t\t\t\t<circle class=\"current-progress\"\n\t\t\t\t\t\t\tr=\"40%\"\n\t\t\t\t\t\t\tcx=\"50%\"\n\t\t\t\t\t\t\tcy=\"50%\"/>\n\t\t\t\t</svg>\n")
		b.WriteString("\t\t\t\t<div " + w.attrString("scrolling-percentage") + "></div>\n")
	}
	b.WriteString("\t\t</div>\n\t\t")
	return b.String()
}

// --- code highlight -------------------------------------------------------------------------

func proARenderCodeHighlight(w *elemCtx) string {
	// get_script_depends: the Prism handles (declared for parity; never loaded from the CDN).
	deps := []string{"prismjs_core", "prismjs_loader", "prismjs_normalize", "highlight_handler"}
	if w.editing() || w.has("line_numbers") {
		deps = append(deps, "prismjs_line_numbers")
	}
	if w.editing() || w.has("highlight_lines") {
		deps = append(deps, "prismjs_line_highlight")
	}
	if w.editing() || w.has("copy_to_clipboard") {
		deps = append(deps, "prismjs_copy_to_clipboard")
	}
	if utilsIsEmpty(w.get("code")) {
		return ""
	}
	w.r.scripts = addUnique(w.r.scripts, deps...)
	lang := escAttr(w.s("language"))
	var b strings.Builder
	b.WriteString("\t\t<div class=\"prismjs-" + escAttr(w.s("theme")) + " " + escAttr(w.s("copy_to_clipboard")) + " " + escAttr(w.s("word_wrap")) + "\">\n")
	b.WriteString("\t\t\t<pre data-line=\"" + escAttr(w.s("highlight_lines")) + "\" class=\"highlight-height language-" + lang + " " + escAttr(w.s("line_numbers")) + "\">\n")
	b.WriteString("\t\t\t\t<code readonly=\"true\" class=\"language-" + lang + "\">\n\t\t\t\t\t<xmp>" + w.s("code") + "</xmp>\n\t\t\t\t</code>\n\t\t\t</pre>\n\t\t</div>\n\t\t")
	return b.String()
}

// --- lottie ---------------------------------------------------------------------------------

func proALottieCaption(w *elemCtx) string {
	src := w.s("source")
	cs := w.s("caption_source")
	if src == "external_url" && w.s("caption") != "" {
		return w.s("caption")
	}
	if cs == "custom" && src == "media_file" && cs != "none" {
		return w.s("caption")
	}
	id := toInt64(sub(w.get("source_json"), "id"))
	switch cs {
	case "caption":
		if a := w.r.attachment(id); a != nil {
			return a.Caption
		}
	case "title":
		if w.r.rc.PostTitle != nil {
			return w.r.rc.PostTitle(id)
		}
	}
	return ""
}

func proARenderLottie(w *elemCtx) string {
	caption := proALottieCaption(w)
	widgetCaption := ""
	if caption != "" {
		widgetCaption = `<p class="e-lottie__caption"> ` + escHTML(caption) + `</p>`
	}
	out := `<div class="e-lottie__container"><div class="e-lottie__animation"></div>` + widgetCaption + `</div>`
	if lm := asMap(w.get("custom_link")); lm != nil && truthy(lm["url"]) && w.s("link_to") == "custom" {
		w.addLinkAttributes("url", lm, false)
		out = `<a class="e-lottie__container__link" ` + w.attrString("url") + `>` + out + `</a>`
	}
	return out
}
