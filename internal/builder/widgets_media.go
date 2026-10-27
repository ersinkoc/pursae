package builder

// Media and icon widgets: image, image-box, icon, icon-box, image-gallery, image-carousel,
// testimonial, star-rating, rating, social-icons (ports of includes/widgets/*.php render()).

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	registerWidget("image", &widgetDef{render: renderImage, styles: static("widget-image")})
	registerWidget("image-box", &widgetDef{render: renderImageBox, styles: static("widget-image-box")})
	registerWidget("icon", &widgetDef{render: renderIconWidget})
	registerWidget("icon-box", &widgetDef{render: renderIconBox, styles: static("widget-icon-box")})
	registerWidget("image-gallery", &widgetDef{render: renderImageGallery, styles: static("widget-image-gallery")})
	registerWidget("image-carousel", &widgetDef{render: renderImageCarousel, styles: static("e-swiper", "widget-image-carousel"), scripts: []string{"swiper"}})
	registerWidget("testimonial", &widgetDef{render: renderTestimonial, styles: static("widget-testimonial")})
	registerWidget("star-rating", &widgetDef{render: renderStarRating, styles: static("widget-star-rating")})
	registerWidget("rating", &widgetDef{render: renderRating, styles: static("widget-rating")})
	registerWidget("social-icons", &widgetDef{render: renderSocialIcons, styles: static("widget-social-icons", "e-apple-webkit")})
}

// utilsIsEmpty mirrors Utils::is_empty for a scalar.
func utilsIsEmpty(v any) bool {
	return !strictEq(v, "0") && isEmpty(v)
}

// ---- image ----

func renderImage(w *elemCtx) string {
	image := asMap(w.get("image"))
	if image == nil || !truthy(image["url"]) {
		return ""
	}
	cs := w.s("caption_source")
	hasCaption := cs != "" && cs != "none"
	var link map[string]any
	switch w.s("link_to") {
	case "none":
	case "custom":
		if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
			link = lm
		}
	default:
		link = map[string]any{"url": image["url"]}
	}
	if link != nil {
		w.addLinkAttributes("link", link, false)
		if w.editing() {
			w.attr("link").add("class", "elementor-clickable")
		}
		if w.s("link_to") != "custom" {
			w.addLightboxData("link", int64(toInt(image["id"])), w.get("open_lightbox"), "", false)
		}
	}
	var b strings.Builder
	if hasCaption {
		b.WriteString("\t\t\t\t<figure class=\"wp-caption\">\n")
	}
	if link != nil {
		b.WriteString("\t\t\t\t\t<a " + w.attrString("link") + ">\n")
	}
	b.WriteString("\t\t\t" + w.r.attachmentImageHTML(w.disp.m, "image", "") + "\t\t\t\t")
	if link != nil {
		b.WriteString("\t\t\t\t</a>\n")
	}
	if hasCaption {
		caption := ""
		switch cs {
		case "attachment":
			if att := w.r.attachment(int64(toInt(image["id"]))); att != nil {
				caption = att.Caption
			}
		case "custom":
			if !utilsIsEmpty(w.get("caption")) {
				caption = w.s("caption")
			}
		}
		b.WriteString("\t\t\t\t\t<figcaption class=\"widget-image-caption wp-caption-text\">" + ksesPost(caption) + "</figcaption>\n")
		b.WriteString("\t\t\t\t</figure>\n")
	}
	b.WriteString("\t\t\t\t\t\t")
	return b.String()
}

// ---- image-box ----

func renderImageBox(w *elemCtx) string {
	desc := ksesPost(w.s("description_text"))
	title := ksesPost(w.s("title_text"))
	image := asMap(w.get("image"))
	hasImage := image != nil && truthy(image["url"])
	hasContent := !utilsIsEmpty(title) || !utilsIsEmpty(desc)
	if !hasImage && !hasContent {
		return ""
	}
	html := `<div class="elementor-image-box-wrapper">`
	lm := asMap(w.get("link"))
	hasLink := lm != nil && truthy(lm["url"])
	if hasLink {
		w.addLinkAttributes("link", lm, false)
	}
	if hasImage {
		img := ksesPost(w.r.attachmentImageHTML(w.disp.m, "thumbnail", "image"))
		if hasLink {
			img = "<a " + w.attrString("link") + ` tabindex="-1">` + img + "</a>"
		}
		html += `<figure class="elementor-image-box-img">` + img + "</figure>"
	}
	if hasContent {
		html += `<div class="elementor-image-box-content">`
		if !utilsIsEmpty(title) {
			th := title
			w.attr("title_text").add("class", "elementor-image-box-title")
			w.addInlineEditing("title_text", "none")
			if hasLink {
				th = "<a " + w.attrString("link") + ">" + title + "</a>"
			}
			tag := validateHTMLTag(w.s("title_size"))
			html += "<" + tag + " " + w.attrString("title_text") + ">" + th + "</" + tag + ">"
		}
		if !utilsIsEmpty(desc) {
			w.attr("description_text").add("class", "elementor-image-box-description")
			w.addInlineEditing("description_text", "")
			html += "<p " + w.attrString("description_text") + ">" + desc + "</p>"
		}
		html += "</div>"
	}
	html += "</div>"
	return html
}

// ---- icon ----

func renderIconWidget(w *elemCtx) string {
	if !truthy(sub(w.get("selected_icon"), "value")) {
		return ""
	}
	w.attr("wrapper").add("class", "elementor-icon-wrapper")
	iw := w.attr("icon-wrapper")
	iw.add("class", "elementor-icon")
	if w.has("hover_animation") {
		iw.add("class", "elementor-animation-"+w.s("hover_animation"))
	}
	tag := "div"
	if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
		w.addLinkAttributes("icon-wrapper", lm, false)
		tag = "a"
	}
	if w.has("icon") {
		w.attr("icon").add("class", w.s("icon")).add("aria-hidden", "true")
	}
	isNew := !w.has("icon")
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString("wrapper") + ">\n")
	b.WriteString("\t\t\t<" + tag + " " + w.attrString("icon-wrapper") + ">\n\t\t\t")
	if isNew || w.isMigrated("selected_icon") {
		b.WriteString(renderIcon(w.get("selected_icon"), newAttrs().add("aria-hidden", "true"), "i"))
	} else {
		b.WriteString("\t\t\t\t<i " + w.attrString("icon") + "></i>\n")
	}
	b.WriteString("\t\t\t</" + tag + ">\n\t\t</div>\n\t\t")
	return b.String()
}

// ---- icon-box ----

func renderIconBox(w *elemCtx) string {
	lm := asMap(w.get("link"))
	hasLink := lm != nil && truthy(lm["url"])
	tag := "span"
	if hasLink {
		tag = "a"
	}
	icon := w.attr("icon")
	icon.add("class", "elementor-icon")
	if w.has("hover_animation") {
		icon.add("class", "elementor-animation-"+w.s("hover_animation"))
	}
	hasIcon := truthy(sub(w.get("selected_icon"), "value"))
	hasContent := !utilsIsEmpty(w.get("title_text")) || !utilsIsEmpty(w.get("description_text"))
	if !hasIcon && !hasContent {
		return ""
	}
	if hasLink {
		w.addLinkAttributes("link", lm, false)
		icon.add("tabindex", "-1")
		if w.has("title_text") {
			icon.add("aria-label", w.s("title_text"))
		}
	}
	_, iconSet := w.disp.m["icon"]
	if w.has("icon") {
		w.attr("i").add("class", w.s("icon")).add("aria-hidden", "true")
	}
	w.attr("description_text").add("class", "elementor-icon-box-description")
	w.addInlineEditing("title_text", "none")
	w.addInlineEditing("description_text", "")
	isNew := !iconSet
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-icon-box-wrapper\">\n\n")
	if hasIcon {
		b.WriteString("\t\t\t\t\t<div class=\"elementor-icon-box-icon\">\n")
		b.WriteString("\t\t\t\t<" + tag + " " + w.attrString("link") + " " + icon.String() + ">\n\t\t\t\t")
		if isNew || w.isMigrated("selected_icon") {
			b.WriteString(renderIcon(w.get("selected_icon"), newAttrs().add("aria-hidden", "true"), "i"))
		} else if w.has("icon") {
			b.WriteString("<i " + w.attrString("i") + "></i>")
		}
		b.WriteString("\t\t\t\t</" + tag + ">\n\t\t\t</div>\n\t\t\t\n")
	}
	if hasContent {
		b.WriteString("\t\t\t\t\t<div class=\"elementor-icon-box-content\">\n\n")
		if !utilsIsEmpty(w.get("title_text")) {
			ts := validateHTMLTag(w.s("title_size"))
			b.WriteString("\t\t\t\t\t\t\t\t\t<" + ts + " class=\"elementor-icon-box-title\">\n")
			b.WriteString("\t\t\t\t\t\t<" + tag + " " + w.attrString("link") + " " + w.attrString("title_text") + ">\n")
			b.WriteString("\t\t\t\t\t\t\t" + ksesPost(w.s("title_text")) + "\t\t\t\t\t\t</" + tag + ">\n")
			b.WriteString("\t\t\t\t\t</" + ts + ">\n\t\t\t\t\n")
		}
		if !utilsIsEmpty(w.get("description_text")) {
			b.WriteString("\t\t\t\t\t\t\t\t\t<p " + w.attrString("description_text") + ">\n")
			b.WriteString("\t\t\t\t\t\t" + ksesPost(w.s("description_text")) + "\t\t\t\t\t</p>\n\t\t\t\t\n")
		}
		b.WriteString("\t\t\t</div>\n\t\t\t\n")
	}
	b.WriteString("\t\t</div>\n\t\t")
	return b.String()
}

// ---- testimonial ----

func renderTestimonial(w *elemCtx) string {
	img := asMap(w.get("testimonial_image"))
	hasContent := w.has("testimonial_content")
	hasImage := img != nil && truthy(img["url"])
	hasName := w.has("testimonial_name")
	hasJob := w.has("testimonial_job")
	if !hasContent && !hasImage && !hasName && !hasJob {
		return ""
	}
	w.attr("wrapper").add("class", "elementor-testimonial-wrapper")
	meta := w.attr("meta")
	meta.add("class", "elementor-testimonial-meta")
	if img != nil && truthy(img["url"]) {
		meta.add("class", "elementor-has-image")
	}
	if pos := w.s("testimonial_image_position"); truthy(w.get("testimonial_image_position")) {
		meta.add("class", "elementor-testimonial-image-position-"+pos)
	}
	lm := asMap(w.get("link"))
	hasLink := lm != nil && truthy(lm["url"])
	if hasLink {
		w.addLinkAttributes("link", lm, false)
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString("wrapper") + ">\n")
	if hasContent {
		w.attr("testimonial_content").add("class", "elementor-testimonial-content")
		w.addInlineEditing("testimonial_content", "")
		b.WriteString("\t\t\t\t<div " + w.attrString("testimonial_content") + ">" + ksesPost(w.s("testimonial_content")) + "</div>\n")
	}
	if hasImage || hasName || hasJob {
		b.WriteString("\t\t\t<div " + meta.String() + ">\n\t\t\t\t<div class=\"elementor-testimonial-meta-inner\">\n")
		if hasImage {
			ih := w.r.attachmentImageHTML(w.disp.m, "testimonial_image", "")
			if hasLink {
				ih = "<a " + w.attrString("link") + ">" + ih + "</a>"
			}
			b.WriteString("\t\t\t\t\t\t<div class=\"elementor-testimonial-image\">\n\t\t\t\t\t\t\t" + ksesPost(ih) + "\t\t\t\t\t\t</div>\n")
		}
		if hasName || hasJob {
			b.WriteString("\t\t\t\t\t<div class=\"elementor-testimonial-details\">\n")
			for _, k := range []string{"name", "job"} {
				key := "testimonial_" + k
				if !w.has(key) {
					continue
				}
				w.attr(key).add("class", "elementor-testimonial-"+k)
				w.addInlineEditing(key, "none")
				if hasLink {
					b.WriteString("\t\t\t\t\t\t\t\t<a " + w.attrString(key) + " " + w.attrString("link") + ">" + ksesPost(w.s(key)) + "</a>\n")
				} else {
					b.WriteString("\t\t\t\t\t\t\t\t<div " + w.attrString(key) + ">" + ksesPost(w.s(key)) + "</div>\n")
				}
			}
			b.WriteString("\t\t\t\t\t</div>\n")
		}
		b.WriteString("\t\t\t\t</div>\n\t\t\t</div>\n")
	}
	b.WriteString("\t\t</div>\n\t\t")
	return b.String()
}

// ---- star-rating ----

func renderStarRating(w *elemCtx) string {
	scale := toInt(w.get("rating_scale"))
	rating := math.Min(toFloat(w.get("rating")), float64(scale))
	textual := escHTML("Rated " + phpFloatString(rating) + " out of " + strconv.Itoa(scale))
	icon := "&#xE934;"
	switch w.s("star_style") {
	case "star_fontawesome":
		if w.s("unmarked_star_style") == "outline" {
			icon = "&#xE933;"
		}
	case "star_unicode":
		icon = "&#9733;"
		if w.s("unmarked_star_style") == "outline" {
			icon = "&#9734;"
		}
	}
	a := w.attr("icon_wrapper")
	a.add("class", "elementor-star-rating").add("itemtype", "http://schema.org/Rating").add("itemscope", "").add("itemprop", "reviewRating")
	floored := math.Floor(rating)
	var stars strings.Builder
	for s := 1.0; s <= float64(scale); s++ {
		switch {
		case s <= floored:
			stars.WriteString(`<i class="elementor-star-full" aria-hidden="true">` + icon + `</i>`)
		case floored+1 == s && rating != floored:
			stars.WriteString(`<i class="elementor-star-` + phpFloatString((rating-floored)*10) + `" aria-hidden="true">` + icon + `</i>`)
		default:
			stars.WriteString(`<i class="elementor-star-empty" aria-hidden="true">` + icon + `</i>`)
		}
	}
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-star-rating__wrapper\">\n")
	if !utilsIsEmpty(w.get("title")) {
		b.WriteString("\t\t\t\t<div class=\"elementor-star-rating__title\">" + escHTML(w.s("title")) + "</div>\n")
	}
	b.WriteString("\t\t\t<div " + a.String() + ">\n\t\t\t\t" + stars.String() + " <span itemprop=\"ratingValue\" class=\"elementor-screen-only\">" + textual + "</span>\n\t\t\t</div>\n\t\t</div>\n\t\t")
	return b.String()
}

// ---- rating ----

func renderRating(w *elemCtx) string {
	scale := toInt(sub(w.get("rating_scale"), "size"))
	rv := w.get("rating_value")
	value := float64(scale)
	if !strictEq(rv, "") {
		value = toFloat(rv)
	}
	value = math.Round(value*100) / 100
	w.attr("widget").add("class", "e-rating").add("itemtype", "https://schema.org/Rating").add("itemscope", "").add("itemprop", "reviewRating")
	w.attr("widget_wrapper").add("class", "e-rating-wrapper").add("itemprop", "ratingValue").add("content", phpFloatString(value)).add("role", "img").
		add("aria-label", escHTML("Rated "+phpFloatString(value)+" out of "+strconv.Itoa(scale)))
	icon := w.get("rating_icon")
	var icons strings.Builder
	for i := 1; i <= scale; i++ {
		key := "icon_marked_" + strconv.Itoa(i)
		w.attr(key).add("class", "e-icon-wrapper e-icon-marked")
		width := "0%"
		if value >= float64(i) {
			width = "100%"
		} else if int(math.Ceil(value)) == i {
			width = phpFloatString((value-float64(i-1))*100) + "%"
		}
		if width != "100%" {
			w.attr(key).add("style", "--e-rating-icon-marked-width: "+width+";")
		}
		ih := renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i")
		icons.WriteString("\t\t\t<div class=\"e-icon\">\n\t\t\t\t<div " + w.attrString(key) + ">\n\t\t\t\t\t" + ih + "\t\t\t\t</div>\n")
		icons.WriteString("\t\t\t\t<div class=\"e-icon-wrapper e-icon-unmarked\">\n\t\t\t\t\t" + ih + "\t\t\t\t</div>\n\t\t\t</div>\n\t\t\t")
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString("widget") + ">\n")
	b.WriteString("\t\t\t<meta itemprop=\"worstRating\" content=\"0\">\n")
	b.WriteString("\t\t\t<meta itemprop=\"bestRating\" content=\"" + strconv.Itoa(scale) + "\">\n")
	b.WriteString("\t\t\t<div " + w.attrString("widget_wrapper") + ">\n\t\t\t\t" + icons.String() + "\t\t\t</div>\n\t\t</div>\n\t\t")
	return b.String()
}

// ---- social-icons ----

func renderSocialIcons(w *elemCtx) string {
	list := asList(w.get("social_icon_list"))
	wr := w.attr("wrapper")
	wr.add("class", "elementor-social-icons-wrapper", "elementor-grid")
	iw := w.attr("item_wrapper")
	iw.add("class", "elementor-grid-item")
	if len(list) > 1 {
		wr.add("role", "list")
		iw.add("role", "listitem")
	}
	anim := ""
	if w.has("hover_animation") {
		anim = " elementor-animation-" + w.s("hover_animation")
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + wr.String() + ">\n")
	for index, it := range list {
		item := asMap(it)
		if item == nil {
			continue
		}
		_, migrated := asMap(item["__fa4_migrated"])["social_icon"]
		isNew := isEmpty(item["social"])
		social := ""
		if truthy(item["social"]) {
			social = strings.ReplaceAll(phpString(item["social"]), "fa fa-", "")
		}
		si := asMap(item["social_icon"])
		lib := phpString(si["library"])
		if (isNew || migrated) && lib != "svg" {
			parts := strings.SplitN(phpString(si["value"]), " ", 2)
			if len(parts) < 2 || isEmpty(parts[1]) {
				social = ""
			} else {
				social = strings.ReplaceAll(parts[1], "fa-", "")
			}
		}
		if lib == "svg" {
			social = ""
			if att := w.r.attachment(int64(toInt(sub(si["value"], "id")))); att != nil {
				social = att.Alt
			}
		}
		lk := "link_" + strconv.Itoa(index)
		w.attr(lk).add("class", "elementor-icon", "elementor-social-icon", "elementor-social-icon-"+social+anim, "elementor-repeater-item-"+phpString(item["_id"]))
		w.addLinkAttributes(lk, item["link"], false)
		b.WriteString("\t\t\t\t<span " + iw.String() + ">\n\t\t\t\t\t<a " + w.attrString(lk) + ">\n")
		b.WriteString("\t\t\t\t\t\t<span class=\"elementor-screen-only\">" + escHTML(ucwords(social)) + "</span>\n\t\t\t\t\t\t")
		if isNew || migrated {
			b.WriteString(renderIcon(si, newAttrs().add("aria-hidden", "true"), "i"))
		} else {
			b.WriteString(`<i class="` + escAttr(phpString(item["social"])) + `" aria-hidden="true"></i>`)
		}
		b.WriteString("\t\t\t\t\t</a>\n\t\t\t\t</span>\n\t\t\t")
	}
	b.WriteString("\t\t</div>\n\t\t")
	return b.String()
}

// ucwords mirrors PHP ucwords (default delimiters: space, tab, newlines).
func ucwords(s string) string {
	b := []byte(s)
	up := true
	for i, c := range b {
		if up && c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
		up = c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
	}
	return string(b)
}

// ---- image-carousel ----

func renderImageCarousel(w *elemCtx) string {
	lazy := w.s("lazyload") == "yes"
	carousel := asList(w.get("carousel"))
	if len(carousel) == 0 {
		return ""
	}
	var slides []string
	for index, it := range carousel {
		att := asMap(it)
		if att == nil {
			att = map[string]any{}
		}
		id := int64(toInt(att["id"]))
		imageURL := ""
		if id > 0 {
			imageURL = w.r.attachmentImageSrc(id, "thumbnail", w.disp.m)
		}
		if imageURL == "" {
			if u, ok := att["url"]; ok {
				imageURL = phpString(u)
			}
		}
		alt := escAttr(w.r.imageAlt(att))
		var img string
		if lazy {
			img = `<img class="swiper-slide-image swiper-lazy" data-src="` + escAttr(imageURL) + `" alt="` + alt + `" />`
		} else {
			img = `<img class="swiper-slide-image" src="` + escAttr(imageURL) + `" alt="` + alt + `" />`
		}
		linkTag := ""
		var link map[string]any
		switch w.s("link_to") {
		case "none":
		case "custom":
			if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
				link = lm
			}
		default:
			u := ""
			if a := w.r.attachment(id); a != nil {
				u = a.URL
			} else if w.r.rc.MediaURL != nil && id > 0 {
				u = w.r.rc.MediaURL(id, "")
			}
			link = map[string]any{"url": u}
		}
		if link != nil {
			lk := "link_" + strconv.Itoa(index)
			w.addLightboxData(lk, id, w.get("open_lightbox"), w.el.ID, false)
			if w.editing() {
				w.attr(lk).add("class", "elementor-clickable")
			}
			w.addLinkAttributes(lk, link, false)
			linkTag = "<a " + w.attrString(lk) + ">"
		}
		caption := ""
		if ct := w.s("caption_type"); ct != "" {
			if a := w.r.attachment(id); a != nil {
				switch ct {
				case "caption":
					caption = a.Caption
				case "title":
					caption = a.Title
				default:
					caption = a.Description
				}
			}
		}
		sk := "swiper_slide_" + strconv.Itoa(index)
		w.attr(sk).add("class", "swiper-slide").add("role", "group").add("aria-roledescription", "slide").
			add("aria-label", strconv.Itoa(index+1)+" of "+strconv.Itoa(len(carousel)))
		html := "<div " + w.attrString(sk) + ">" + linkTag + `<figure class="swiper-slide-inner">` + img
		if lazy {
			html += `<div class="swiper-lazy-preloader"></div>`
		}
		if caption != "" {
			html += `<figcaption class="elementor-image-carousel-caption">` + ksesPost(caption) + "</figcaption>"
		}
		html += "</figure>"
		if link != nil {
			html += "</a>"
		}
		html += "</div>"
		slides = append(slides, html)
	}
	live := "polite"
	if w.s("autoplay") == "yes" {
		live = "off"
	}
	c := w.attr("carousel")
	c.add("class", "elementor-image-carousel swiper-wrapper").add("aria-live", live)
	cw := w.attr("carousel-wrapper")
	cw.add("class", "elementor-image-carousel-wrapper swiper").add("role", "region").add("aria-roledescription", "carousel").
		add("aria-label", w.s("carousel_name")).add("dir", w.s("direction"))
	nav := w.s("navigation")
	showDots := nav == "dots" || nav == "both"
	showArrows := nav == "arrows" || nav == "both"
	if w.s("image_stretch") == "yes" {
		c.add("class", "swiper-image-stretch")
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + cw.String() + ">\n\t\t\t<div " + c.String() + ">\n\t\t\t\t\t\t\t\t" + strings.Join(slides, "") + "\t\t\t</div>\n")
	if len(carousel) > 1 {
		if showArrows {
			for _, t := range []string{"previous", "next"} {
				cls, dir := "prev", "left"
				if t == "next" {
					cls, dir = "next", "right"
				}
				icon := asMap(w.get("navigation_" + t + "_icon"))
				if icon == nil || !truthy(icon["value"]) {
					icon = map[string]any{"library": "eicons", "value": "eicon-chevron-" + dir}
				}
				b.WriteString("\t\t\t\t\t\t\t\t\t<div class=\"elementor-swiper-button elementor-swiper-button-" + cls + "\" role=\"button\" tabindex=\"0\">\n\t\t\t\t\t\t" +
					renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i") + "\t\t\t\t\t</div>\n")
			}
		}
		if showDots {
			b.WriteString("\t\t\t\t\n\t\t\t\t\t\t\t\t\t<div class=\"swiper-pagination\"></div>\n")
		}
	}
	b.WriteString("\t\t\t\t\t</div>\n\t\t")
	return b.String()
}

// ---- image-gallery (WordPress [gallery] shortcode with upstream's lightbox link filter) ----

func renderImageGallery(w *elemCtx) string {
	gallery := asList(w.get("wp_gallery"))
	if isEmpty(w.get("wp_gallery")) {
		return ""
	}
	columns := 3
	if truthy(w.get("gallery_columns")) {
		columns = toInt(w.get("gallery_columns"))
	}
	link := ""
	if truthy(w.get("gallery_link")) {
		link = w.s("gallery_link")
	}
	size := w.s("thumbnail_size")
	if size == "" {
		size = "thumbnail"
	}
	var atts []*Attachment
	for _, it := range gallery {
		id := int64(toInt(sub(it, "id")))
		if a := w.r.attachment(id); a != nil {
			atts = append(atts, a)
		}
	}
	if len(atts) == 0 {
		return "\t\t<div class=\"elementor-image-gallery\">\n\t\t\t\t\t</div>\n\t\t"
	}
	instance := galleryInstance(w)
	selector := "gallery-" + strconv.Itoa(instance)
	postID := int64(0)
	if w.r.doc != nil {
		postID = w.r.doc.ID
	}
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-image-gallery\">\n\t\t\t")
	b.WriteString("<div id='" + selector + "' class='gallery galleryid-" + strconv.FormatInt(postID, 10) + " gallery-columns-" + strconv.Itoa(columns) + " gallery-size-" + escAttr(size) + "'>")
	for _, a := range atts {
		imgAttrs := newAttrs()
		if strings.TrimSpace(a.Caption) != "" {
			imgAttrs.add("aria-describedby", selector+"-"+strconv.FormatInt(a.ID, 10))
		}
		img := w.r.wpGetAttachmentImage(a, size, imgAttrs)
		var inner string
		switch link {
		case "file":
			inner = w.galleryLink(a, a.URL, img)
		case "none":
			inner = img
		default:
			inner = w.galleryLink(a, a.URL, img) // attachment page links are not resolvable here
		}
		orientation := ""
		if a.Width > 0 && a.Height > 0 {
			if a.Height > a.Width {
				orientation = "portrait"
			} else {
				orientation = "landscape"
			}
		}
		b.WriteString("<figure class='gallery-item'>\n\t\t\t<div class='gallery-icon " + orientation + "'>\n\t\t\t\t" + inner + "\n\t\t\t</div>")
		if strings.TrimSpace(a.Caption) != "" {
			b.WriteString("\n\t\t\t\t<figcaption class='wp-caption-text gallery-caption' id='" + selector + "-" + strconv.FormatInt(a.ID, 10) + "'>\n\t\t\t\t" + wptexturize(a.Caption) + "\n\t\t\t\t</figcaption>")
		}
		b.WriteString("</figure>")
	}
	b.WriteString("\n\t\t</div>\n\n\t\t\t</div>\n\t\t")
	return b.String()
}

// galleryLink mirrors wp_get_attachment_link filtered by add_lightbox_data_to_image_link.
func (w *elemCtx) galleryLink(a *Attachment, href, img string) string {
	key := "link"
	if w.editing() {
		w.attr(key).set("class", "elementor-clickable")
	}
	w.addLightboxData(key, a.ID, w.get("open_lightbox"), w.el.ID, true)
	return "<a " + w.attrString(key) + " href='" + escURL(href) + "'>" + img + "</a>"
}

// galleryInstance mirrors gallery_shortcode's per-request instance counter: the position of this
// widget among the document's image galleries.
func galleryInstance(w *elemCtx) int {
	if w.r.doc == nil {
		return 1
	}
	n := 0
	found := 0
	var walk func(els []*Element)
	walk = func(els []*Element) {
		for _, el := range els {
			if el == nil || found > 0 {
				continue
			}
			if el.ElType == "widget" && el.WidgetType == "image-gallery" && len(asList(el.Settings["wp_gallery"])) > 0 {
				n++
				if el == w.el || el.ID == w.el.ID {
					found = n
					return
				}
			}
			walk(el.Elements)
		}
	}
	walk(w.r.doc.Elements)
	if found == 0 {
		return 1
	}
	return found
}

// ---- the_content image/iframe post-processing ----

var (
	contentTagRe = regexp.MustCompile(`<(img|iframe)\s[^>]+>`)
	attrWidthRe  = regexp.MustCompile(` width=["']([0-9]+)["']`)
	attrHeightRe = regexp.MustCompile(` height=["']([0-9]+)["']`)
	attrLoadRe   = regexp.MustCompile(` loading=["']([A-Za-z]+)["']`)
	attrFetchRe  = regexp.MustCompile(` fetchpriority=["']([A-Za-z]+)["']`)
	attrDecodeRe = regexp.MustCompile(` decoding=["']([A-Za-z]+)["']`)
)

// filterContentTags mirrors wp_filter_content_tags as applied by the_content to the rendered
// document (main query, in the loop): decoding="async" on images, fetchpriority="high" on the
// first large image, loading="lazy" from the fourth sized image/iframe on. Identical tags are
// processed once (WordPress de-duplicates them).
func filterContentTags(content string) string {
	matches := contentTagRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return content
	}
	done := map[string]bool{}
	count := 0
	highFlag := true
	for _, m := range matches {
		tag, name := m[0], m[1]
		if done[tag] {
			continue
		}
		done[tag] = true
		filtered := tag
		wm := attrWidthRe.FindStringSubmatch(tag)
		hm := attrHeightRe.FindStringSubmatch(tag)
		hasWH := wm != nil && hm != nil
		lm := attrLoadRe.FindStringSubmatch(tag)
		fm := attrFetchRe.FindStringSubmatch(tag)
		// loading optimization attributes
		var loading, fetch string
		decoding := ""
		if name == "img" {
			decoding = "async"
			if dm := attrDecodeRe.FindStringSubmatch(tag); dm != nil {
				decoding = dm[1]
			}
		}
		if hasWH {
			var inView *bool
			t, f := true, false
			if lm != nil {
				if lm[1] == "lazy" {
					inView = &f
				} else {
					inView = &t
				}
			}
			existing := ""
			if fm != nil {
				existing = fm[1]
			}
			if existing == "high" {
				inView = &t
			} else if existing == "low" {
				inView = &f
			}
			if inView == nil {
				if count < 3 {
					inView = &t
				} else {
					inView = &f
				}
			}
			if *inView {
				if name == "img" && (existing == "" || existing == "auto") && highFlag {
					if toInt(wm[1])*toInt(hm[1]) >= 50000 {
						fetch = "high"
						highFlag = false
					}
				} else if existing == "high" {
					highFlag = false
				}
			} else if existing != "low" {
				loading = "lazy"
			}
			if existing != "auto" {
				count++
			}
		}
		if name == "img" {
			if !strings.Contains(filtered, ` src="`) {
				continue
			}
			if attrDecodeRe.FindStringSubmatch(tag) == nil && decoding != "" {
				filtered = strings.Replace(filtered, "<img", `<img decoding="`+decoding+`"`, -1)
			}
			if !strings.Contains(filtered, ` width="`) || !strings.Contains(filtered, ` height="`) {
				if filtered != tag {
					content = strings.ReplaceAll(content, tag, filtered)
				}
				continue
			}
			if lm == nil && loading != "" {
				filtered = strings.Replace(filtered, "<img", `<img loading="`+loading+`"`, -1)
			}
			if fm == nil && fetch != "" {
				filtered = strings.Replace(filtered, "<img", `<img fetchpriority="`+fetch+`"`, -1)
			}
		} else {
			if lm == nil && loading != "" && strings.Contains(filtered, ` src="`) && strings.Contains(filtered, ` width="`) && strings.Contains(filtered, ` height="`) {
				filtered = strings.Replace(filtered, "<iframe", `<iframe loading="`+loading+`"`, -1)
			}
		}
		if filtered != tag {
			content = strings.ReplaceAll(content, tag, filtered)
		}
	}
	return content
}
