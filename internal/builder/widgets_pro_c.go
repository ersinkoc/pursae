package builder

// GoWebEditor Pro carousels: media-carousel, testimonial-carousel and reviews (ports of upstream
// Pro's modules/carousel/widgets/*.php over the shared Base::print_slider).

import (
	"net/url"
	"strconv"
	"strings"
)

func init() {
	registerWidget("media-carousel", &widgetDef{render: proCRenderMediaCarousel,
		styles: static("e-swiper", "widget-media-carousel", "widget-carousel-module-base"), scripts: []string{"swiper"}})
	registerWidget("testimonial-carousel", &widgetDef{render: proCRenderTestimonialCarousel,
		styles: static("e-swiper", "widget-testimonial-carousel", "widget-carousel-module-base"), scripts: []string{"swiper"}})
	registerWidget("reviews", &widgetDef{render: proCRenderReviews,
		styles: static("e-swiper", "widget-testimonial-carousel", "widget-reviews", "widget-star-rating", "widget-carousel-module-base"), scripts: []string{"swiper"}})
}

// proCSlider is one Base::print_slider call.
type proCSlider struct {
	containerClass string
	showArrows     bool
	thumbs         bool
	videoPlayIcon  bool
}

// proCCarousel holds the per-widget state of the carousel base (slide_prints_count).
type proCCarousel struct {
	w      *elemCtx
	prints int
	slide  func(c *proCCarousel, slide map[string]any, opt proCSlider, key string) string
}

func proCSlides(w *elemCtx) []map[string]any {
	var out []map[string]any
	for _, it := range asList(w.get("slides")) {
		if m := asMap(it); m != nil {
			out = append(out, m)
		} else {
			out = append(out, map[string]any{})
		}
	}
	return out
}

// printSlider mirrors Base::print_slider (e_optimized_markup on: no .elementor-swiper wrapper).
func (c *proCCarousel) printSlider(opt proCSlider) string {
	w := c.w
	slides := proCSlides(w)
	wa := w.attr("wrapper")
	wa.add("class", opt.containerClass, "swiper").add("role", "region").add("aria-roledescription", "carousel").add("aria-label", w.s("slides_name"))
	var b strings.Builder
	b.WriteString("\t\t\t<div " + wa.String() + ">\n\t\t\t\t<div class=\"swiper-wrapper\">\n")
	for i, sl := range slides {
		c.prints++
		b.WriteString("\t\t\t\t\t\t<div class=\"swiper-slide\" role=\"group\" aria-roledescription=\"slide\">\n")
		b.WriteString(c.slide(c, sl, opt, "slide-"+strconv.Itoa(i)+"-"+strconv.Itoa(c.prints)))
		b.WriteString("\t\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t\t</div>\n")
	if len(slides) > 1 {
		if opt.showArrows {
			b.WriteString("\t\t\t\t\t\t<div class=\"elementor-swiper-button elementor-swiper-button-prev\" role=\"button\" tabindex=\"0\" aria-label=\"Previous\">\n" +
				renderIcon(map[string]any{"library": "eicons", "value": "eicon-chevron-left"}, newAttrs().add("aria-hidden", "true"), "i") + "\t\t\t\t\t\t</div>\n")
			b.WriteString("\t\t\t\t\t\t<div class=\"elementor-swiper-button elementor-swiper-button-next\" role=\"button\" tabindex=\"0\" aria-label=\"Next\">\n" +
				renderIcon(map[string]any{"library": "eicons", "value": "eicon-chevron-right"}, newAttrs().add("aria-hidden", "true"), "i") + "\t\t\t\t\t\t</div>\n")
		}
		if w.has("pagination") {
			b.WriteString("\t\t\t\t\t\t<div class=\"swiper-pagination\"></div>\n")
		}
	}
	b.WriteString("\t\t\t</div>\n")
	return b.String()
}

func proCDefaultSlider(w *elemCtx) proCSlider {
	return proCSlider{containerClass: "elementor-main-swiper", showArrows: w.has("show_arrows"), videoPlayIcon: true}
}

// proCSlideImageURL mirrors Base::get_slide_image_url.
func (w *elemCtx) proCSlideImageURL(slide map[string]any) string {
	img := asMap(slide["image"])
	u := ""
	if id := int64(toInt(img["id"])); id > 0 {
		u = w.r.attachmentImageSrc(id, "image_size", w.disp.m)
	}
	if u == "" {
		u = phpString(img["url"])
	}
	return u
}

// ---- media-carousel ----

func proCRenderMediaCarousel(w *elemCtx) string {
	if w.has("overlay") {
		w.attr("image-overlay").add("class", "elementor-carousel-image-overlay", "e-overlay-animation-"+w.s("overlay_animation"))
	}
	c := &proCCarousel{w: w, slide: proCMediaSlide}
	out := c.printSlider(proCDefaultSlider(w))
	if w.s("skin") != "slideshow" || len(proCSlides(w)) <= 1 {
		return out
	}
	return out + c.printSlider(proCSlider{containerClass: "elementor-thumbnails-swiper", thumbs: true})
}

func proCMediaLinkTo(slide map[string]any) string {
	img := asMap(slide["image"])
	if truthy(sub(slide["video"], "url")) {
		if truthy(img["url"]) {
			return phpString(img["url"])
		}
		return "#"
	}
	t := phpString(slide["image_link_to_type"])
	if t == "" {
		return ""
	}
	if t == "custom" {
		return phpString(sub(slide["image_link_to"], "url"))
	}
	return phpString(img["url"])
}

func proCMediaSlide(c *proCCarousel, slide map[string]any, opt proCSlider, key string) string {
	w := c.w
	playIcon := opt.videoPlayIcon && !opt.thumbs
	img := asMap(slide["image"])
	if img == nil {
		img = map[string]any{}
	}
	ia := w.attr(key + "-image")
	ia.add("class", "elementor-carousel-image").add("role", "img").add("aria-label", w.r.imageAlt(img))
	src := w.proCSlideImageURL(slide)
	if w.s("lazyload") == "yes" {
		ia.add("class", "swiper-lazy").add("data-background", src)
	} else {
		ia.add("style", "background-image: url('"+src+"')")
	}
	var b strings.Builder
	linkTo := proCMediaLinkTo(slide)
	if linkTo != "" && !opt.thumbs {
		lk := key + "_link"
		if phpString(slide["image_link_to_type"]) == "custom" {
			w.addLinkAttributes(lk, slide["image_link_to"], false)
		} else {
			w.attr(lk).add("href", escURL(linkTo))
			w.addLightboxData(lk, int64(toInt(img["id"])), "yes", w.el.ID, false)
			if w.editing() {
				w.attr(lk).add("class", "elementor-clickable")
			}
		}
		if phpString(slide["type"]) == "video" && truthy(sub(slide["video"], "url")) {
			p := newOmap()
			p.Set("autoplay", 1)
			p.Set("rel", 0)
			p.Set("controls", 0)
			if u, ok := embedURL(phpString(sub(slide["video"], "url")), p, false, 0); ok {
				w.attr(lk).add("data-elementor-lightbox-video", u)
			}
		}
		b.WriteString("<a " + w.attrString(lk) + ">")
	}
	b.WriteString("\t\t<div " + ia.String() + ">\n")
	if w.s("lazyload") == "yes" {
		b.WriteString("\t\t\t\t<div class=\"swiper-lazy-preloader\"></div>\n")
	}
	if phpString(slide["type"]) == "video" && playIcon {
		b.WriteString("\t\t\t\t<div class=\"elementor-custom-embed-play\">\n" +
			renderIcon(map[string]any{"library": "eicons", "value": "eicon-play"}, newAttrs().add("aria-hidden", "true"), "i") +
			"\t\t\t\t\t<span class=\"elementor-screen-only\">Play</span>\n\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t\t\t</div>\n")
	if w.has("overlay") {
		b.WriteString("\t\t\t<div " + w.attrString("image-overlay") + ">\n")
		if w.s("overlay") == "text" {
			b.WriteString(ksesPost(proCMediaCaption(w, img)))
		} else {
			b.WriteString(renderIcon(map[string]any{"library": "fa-solid", "value": "fas fa-" + w.s("icon")}, nil, "i"))
		}
		b.WriteString("\t\t\t</div>\n")
	}
	if linkTo != "" {
		b.WriteString("\t\t</a>")
	}
	return b.String()
}

func proCMediaCaption(w *elemCtx, img map[string]any) string {
	t := phpString(w.raw("caption"))
	if t == "" {
		return ""
	}
	a := w.r.attachment(int64(toInt(img["id"])))
	if a == nil {
		return ""
	}
	switch t {
	case "caption":
		return a.Caption
	case "title":
		return a.Title
	}
	return a.Description
}

// ---- testimonial-carousel ----

func proCRenderTestimonialCarousel(w *elemCtx) string {
	c := &proCCarousel{w: w, slide: proCTestimonialSlide}
	return c.printSlider(proCDefaultSlider(w))
}

func proCTestimonialCite(w *elemCtx, slide map[string]any, location string) string {
	if isEmpty(slide["name"]) && isEmpty(slide["title"]) {
		return ""
	}
	layout := phpString(w.raw("layout"))
	if phpString(w.raw("skin")) == "bubble" {
		layout = "image_inline"
	}
	outside := location == "outside" && (layout == "image_above" || layout == "image_right" || layout == "image_left")
	inside := location == "inside" && (layout == "image_inline" || layout == "image_stacked")
	if !outside && !inside {
		return ""
	}
	h := `<cite class="elementor-testimonial__cite">`
	if !isEmpty(slide["name"]) {
		h += `<span class="elementor-testimonial__name">` + phpString(slide["name"]) + `</span>`
	}
	if !isEmpty(slide["title"]) {
		h += `<span class="elementor-testimonial__title">` + phpString(slide["title"]) + `</span>`
	}
	return h + "</cite>"
}

// proCSlideImgAttrs sets the slide <img> attributes; alt is the alt text to use.
func (w *elemCtx) proCSlideImgAttrs(key string, slide map[string]any, alt string) {
	src := w.proCSlideImageURL(slide)
	a := w.attr(key + "-image")
	if w.s("lazyload") == "yes" {
		a.add("class", "swiper-lazy").add("data-src", src)
	} else {
		a.add("src", src)
	}
	a.add("alt", alt)
}

func proCTestimonialSlide(c *proCCarousel, slide map[string]any, opt proCSlider, key string) string {
	w := c.w
	lazy := w.s("lazyload") == "yes"
	w.attr(key+"-testimonial").add("class", "elementor-testimonial")
	img := asMap(slide["image"])
	hasImg := truthy(img["url"])
	if hasImg {
		alt := phpString(img["alt"])
		if isEmpty(img["alt"]) {
			alt = phpString(slide["name"])
		}
		w.proCSlideImgAttrs(key, slide, alt)
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString(key+"-testimonial") + ">\n")
	if truthy(slide["content"]) {
		b.WriteString("\t\t\t\t<div class=\"elementor-testimonial__content\">\n\t\t\t\t\t<div class=\"elementor-testimonial__text\">\n\t\t\t\t\t\t" +
			phpString(slide["content"]) + "\t\t\t\t\t</div>\n\t\t\t\t\t" + proCTestimonialCite(w, slide, "outside") + "\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t<div class=\"elementor-testimonial__footer\">\n")
	if hasImg {
		b.WriteString("\t\t\t\t\t<div class=\"elementor-testimonial__image\">\n\t\t\t\t\t\t<img " + w.attrString(key+"-image") + ">\n")
		if lazy {
			b.WriteString("\t\t\t\t\t\t\t<div class=\"swiper-lazy-preloader\"></div>\n")
		}
		b.WriteString("\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t\t" + proCTestimonialCite(w, slide, "inside") + "\t\t\t</div>\n\t\t</div>\n")
	return b.String()
}

// ---- reviews ----

func proCRenderReviews(w *elemCtx) string {
	c := &proCCarousel{w: w, slide: proCReviewSlide}
	return c.printSlider(proCDefaultSlider(w))
}

func proCReviewStars(w *elemCtx, slide map[string]any) string {
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
	rating := toFloat(slide["rating"])
	if rating > 5 {
		rating = 5
	}
	floored := int(rating)
	var b strings.Builder
	for s := 1; s <= 5; s++ {
		switch {
		case s <= floored:
			b.WriteString(`<i class="elementor-star-full">` + icon + `</i>`)
		case floored+1 == s && rating != float64(floored):
			b.WriteString(`<i class="elementor-star-` + phpFloatString((rating-float64(floored))*10) + `">` + icon + `</i>`)
		default:
			b.WriteString(`<i class="elementor-star-empty">` + icon + `</i>`)
		}
	}
	return `<div class="elementor-star-rating">` + b.String() + `</div>`
}

func proCReviewCite(w *elemCtx, slide map[string]any) string {
	if isEmpty(slide["name"]) && isEmpty(slide["title"]) {
		return ""
	}
	h := `<cite class="elementor-testimonial__cite">`
	if !isEmpty(slide["name"]) {
		h += `<span class="elementor-testimonial__name">` + phpString(slide["name"]) + `</span>`
	}
	if !isEmpty(slide["rating"]) {
		h += proCReviewStars(w, slide)
	}
	if !isEmpty(slide["title"]) {
		h += `<span class="elementor-testimonial__title">` + phpString(slide["title"]) + `</span>`
	}
	return ksesPost(h + "</cite>")
}

func proCReviewIcon(w *elemCtx, slide map[string]any, key string) string {
	old := phpString(slide["social_icon"])
	sel := asMap(slide["selected_social_icon"])
	if old == "" && isEmpty(slide["selected_social_icon"]) {
		return ""
	}
	migrated := asMap(slide["__fa4_migrated"]) != nil && sub(slide["__fa4_migrated"], "selected_social_icon") != nil
	isNew := old == ""
	icon, social := "", ""
	if isNew || migrated {
		icon = renderIcon(sel, newAttrs().add("aria-hidden", "true"), "i")
	} else {
		icon = `<i class="` + escAttr(old) + `" aria-hidden="true"></i>`
	}
	if old != "" {
		social = strings.ReplaceAll(old, "fa fa-", "")
	}
	if (isNew || migrated) && phpString(sel["library"]) != "svg" {
		parts := strings.SplitN(phpString(sel["value"]), " ", 2)
		if len(parts) < 2 || parts[1] == "" {
			social = ""
		} else {
			social = strings.ReplaceAll(parts[1], "fa-", "")
		}
	}
	if phpString(sel["library"]) == "svg" {
		social = ""
	}
	a := w.attr("icon_wrapper_" + key)
	a.add("class", "elementor-testimonial__icon elementor-icon").add("class", "elementor-icon-"+social).add("aria-label", "Read More")
	return "<div " + a.String() + ">" + icon + "</div>"
}

func proCReviewSlide(c *proCCarousel, slide map[string]any, opt proCSlider, key string) string {
	w := c.w
	lazy := w.s("lazyload") == "yes"
	w.attr(key+"-testimonial").add("class", "elementor-testimonial").add("class", "elementor-repeater-item-"+phpString(slide["_id"]))
	img := asMap(slide["image"])
	hasImg := truthy(img["url"])
	if hasImg {
		alt := ""
		if !isEmpty(slide["name"]) {
			alt = phpString(slide["name"])
		} else if !isEmpty(img["alt"]) {
			alt = phpString(img["alt"])
		}
		w.proCSlideImgAttrs(key, slide, alt)
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString(key+"-testimonial") + ">\n")
	if hasImg || !isEmpty(slide["name"]) || !isEmpty(slide["title"]) {
		link := phpString(sub(slide["link"], "url"))
		tag := "div"
		hk := "header_" + phpString(slide["_id"])
		w.attr(hk).add("class", "elementor-testimonial__header")
		if link != "" {
			tag = "a"
			w.addLinkAttributes(hk, slide["link"], false)
		}
		b.WriteString("\t\t\t\t<" + tag + " " + w.attrString(hk) + ">\n")
		if hasImg {
			b.WriteString("\t\t\t\t\t\t<div class=\"elementor-testimonial__image\">\n\t\t\t\t\t\t\t<img " + w.attrString(key+"-image") + ">\n")
			if lazy {
				b.WriteString("\t\t\t\t\t\t\t\t<div class=\"swiper-lazy-preloader\"></div>\n")
			}
			b.WriteString("\t\t\t\t\t\t</div>\n")
		}
		b.WriteString(proCReviewCite(w, slide) + proCReviewIcon(w, slide, key) + "\t\t\t\t</" + tag + ">\n")
	}
	if truthy(slide["content"]) {
		b.WriteString("\t\t\t\t<div class=\"elementor-testimonial__content\">\n\t\t\t\t\t<div class=\"elementor-testimonial__text\">\n\t\t\t\t\t\t" +
			phpString(slide["content"]) + "\t\t\t\t\t</div>\n\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t</div>\n")
	return b.String()
}

// ---- nested-carousel ----

func init() {
	registerWidget("nested-carousel", &widgetDef{render: proCRenderNestedCarousel, styles: static("e-swiper", "widget-nested-carousel"),
		scripts: []string{"swiper"}, wrapperClass: "elementor-widget-n-carousel"})
	registerWidget("gallery", &widgetDef{render: proCRenderGallery, styles: static("widget-gallery", "elementor-gallery", "e-transitions"),
		scripts: []string{"elementor-gallery"}})
}

// proCCarouselFooter mirrors Base_Carousel_Trait::render_carousel_footer; items is the number of
// carousel items ("" counts as "unknown", which shows the navigation).
func proCCarouselFooter(w *elemCtx, items int) string {
	show := items > 1
	var b strings.Builder
	if w.s("arrows") == "yes" && show {
		for _, t := range []string{"previous", "next"} {
			cls, label := "prev", "Previous"
			if t == "next" {
				cls, label = "next", "Next"
			}
			icon := ""
			if im := asMap(w.get("navigation_" + t + "_icon")); im != nil && truthy(im["value"]) {
				icon = renderIcon(im, newAttrs().add("aria-hidden", "true"), "i")
			}
			b.WriteString("\t\t\t<div class=\"elementor-swiper-button elementor-swiper-button-" + cls + "\" role=\"button\" tabindex=\"0\" aria-label=\"" + label + "\">\n\t\t\t\t" + icon + "\t\t\t</div>\n")
		}
	}
	if w.has("pagination") && show {
		b.WriteString("\t\t\t<div class=\"swiper-pagination\"></div>\n")
	}
	return b.String()
}

func proCRenderNestedCarousel(w *elemCtx) string {
	slides := asList(w.get("carousel_items"))
	out := w.attr("carousel-outside-wrapper")
	out.add("class", "e-n-carousel", "swiper").add("role", "region").add("aria-roledescription", "carousel").add("aria-label", w.s("carousel_name"))
	in := w.attr("carousel-inside-wrapper")
	live := "polite"
	if w.s("autoplay") == "yes" {
		live = "off"
	}
	in.add("class", "swiper-wrapper").add("aria-live", live)
	if d := w.s("direction"); d != "" {
		out.add("dir", d)
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + out.String() + ">\n\t\t\t<div " + in.String() + ">\n")
	for i := range slides {
		k := repeaterKey("slide_wrapper", "slide", i)
		a := w.attr(k)
		a.add("class", "swiper-slide").add("data-slide", strconv.Itoa(i+1)).add("role", "group").add("aria-roledescription", "slide").
			add("aria-label", strconv.Itoa(i+1)+" of "+strconv.Itoa(len(slides)))
		b.WriteString("\t\t\t\t\t\t<div " + a.String() + ">\n" + w.printNestedChild(i, nil) + "\t\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t\t\t</div>\n\t\t</div>\n")
	b.WriteString(proCCarouselFooter(w, len(slides)))
	return b.String()
}

// ---- gallery ----

func proCRenderGallery(w *elemCtx) string {
	multiple := w.s("gallery_type") == "multiple" && !isEmpty(w.get("galleries"))
	single := w.s("gallery_type") == "single" && !isEmpty(w.get("gallery"))
	hasDesc := w.has("overlay_description")
	hasTitle := w.has("overlay_title")
	hasAnim := w.has("image_hover_animation") || w.has("content_hover_animation") || w.has("background_overlay_hover_animation")
	tag := "div"
	if w.has("link_to") {
		tag = "a"
	}
	type gal struct {
		index int
		items []any
	}
	var galleries []gal
	var b strings.Builder
	if multiple {
		tc := w.attr("titles-container")
		tc.add("class", "elementor-gallery__titles-container").add("aria-label", "Gallery filter")
		if w.has("pointer") {
			tc.add("class", "e--pointer-"+w.s("pointer"))
			for _, k := range w.disp.keys {
				if strings.HasPrefix(k, "animation") && truthy(w.disp.m[k]) {
					tc.add("class", "e--animation-"+phpString(w.disp.m[k]))
					break
				}
			}
		}
		b.WriteString("\t\t\t<div " + tc.String() + ">\n")
		if w.has("show_all_galleries") {
			b.WriteString("\t\t\t\t\t<a class=\"elementor-item elementor-gallery-title\" role=\"button\" tabindex=\"0\" data-gallery-index=\"all\">\n\t\t\t\t\t\t" +
				ksesPost(w.s("show_all_galleries_label")) + "\t\t\t\t\t</a>\n")
		}
		for i, g := range asList(w.get("galleries")) {
			gm := asMap(g)
			if isEmpty(gm["multiple_gallery"]) {
				continue
			}
			galleries = append(galleries, gal{i, asList(gm["multiple_gallery"])})
			b.WriteString("\t\t\t\t\t<a class=\"elementor-item elementor-gallery-title\" role=\"button\" tabindex=\"0\" data-gallery-index=\"" + strconv.Itoa(i) + "\">\n\t\t\t\t\t\t" +
				phpString(gm["gallery_title"]) + "\t\t\t\t\t</a>\n")
		}
		b.WriteString("\t\t\t</div>\n")
	} else if single {
		galleries = append(galleries, gal{0, asList(w.get("gallery"))})
	} else if w.editing() {
		b.WriteString("\t\t\t<i class=\"elementor-widget-empty-icon eicon-gallery-justified\"></i>\n")
	}
	w.attr("gallery_container").add("class", "elementor-gallery__container")
	if hasTitle || hasDesc {
		w.attr("gallery_item_content").add("class", "elementor-gallery-item__content")
		if hasTitle {
			w.attr("gallery_item_title").add("class", "elementor-gallery-item__title")
		}
		if hasDesc {
			w.attr("gallery_item_description").add("class", "elementor-gallery-item__description")
		}
	}
	w.attr("gallery_item_background_overlay").add("class", "elementor-gallery-item__overlay")
	var order []int64
	tags := map[int64][]string{}
	for _, g := range galleries {
		for _, it := range g.items {
			id := int64(toInt(sub(it, "id")))
			if _, ok := tags[id]; !ok {
				order = append(order, id)
			}
			tags[id] = append(tags[id], strconv.Itoa(g.index))
		}
	}
	if len(galleries) == 0 {
		return b.String()
	}
	size := w.s("thumbnail_image_size")
	b.WriteString("\t\t<div " + w.attrString("gallery_container") + ">\n")
	for _, id := range order {
		a := w.r.attachment(id)
		if a == nil {
			continue
		}
		src, width, height, ok := a.imageSrc(size)
		if !ok {
			continue
		}
		media, _, _, _ := a.imageSrc("full")
		data := map[string]string{"alt": a.Alt, "media": media, "src": src, "width": strconv.Itoa(width), "height": strconv.Itoa(height),
			"caption": a.Caption, "description": a.Description, "title": a.Title}
		if size == "custom" {
			data["src"] = w.r.attachmentImageSrc(id, "thumbnail_image", w.disp.m)
			dim := asMap(w.get("thumbnail_image_custom_dimension"))
			data["width"], data["height"] = phpString(dim["width"]), phpString(dim["height"])
		}
		key := "gallery_item_" + strconv.FormatInt(id, 10)
		ia := w.attr(key)
		ia.add("class", "e-gallery-item", "elementor-gallery-item")
		if hasAnim {
			ia.add("class", "elementor-animated-content")
		}
		if multiple {
			ia.add("data-e-gallery-tags", strings.Join(tags[id], ","))
		}
		if hasTitle && tag == "div" {
			ia.add("tabindex", "0")
		}
		if tag == "a" {
			switch w.s("link_to") {
			case "file":
				ia.add("href", escURL(data["media"]))
				if w.editing() {
					ia.add("class", "elementor-clickable")
				}
				w.addLightboxData(key, id, w.get("open_lightbox"), w.el.ID, false)
			case "custom":
				w.addLinkAttributes(key, w.get("url"), false)
			}
		}
		im := w.attr("gallery_item_image_" + strconv.FormatInt(id, 10))
		im.add("class", "e-gallery-image", "elementor-gallery-item__image").add("data-thumbnail", data["src"]).
			add("data-width", data["width"]).add("data-height", data["height"]).add("aria-label", data["alt"]).add("role", "img")
		b.WriteString("\t\t\t\t<" + tag + " " + w.attrString(key) + ">\n\t\t\t\t\t<div " + im.String() + " ></div>\n")
		if w.has("overlay_background") {
			b.WriteString("\t\t\t\t\t\t<div " + w.attrString("gallery_item_background_overlay") + "></div>\n")
		}
		if hasTitle || hasDesc {
			b.WriteString("\t\t\t\t\t<div " + w.attrString("gallery_item_content") + ">\n")
			if hasTitle {
				if t := data[w.s("overlay_title")]; t != "" {
					b.WriteString("\t\t\t\t\t\t\t\t<div " + w.attrString("gallery_item_title") + ">\n\t\t\t\t\t\t\t\t\t" + t + "\t\t\t\t\t\t\t\t</div>\n")
				}
			}
			if hasDesc {
				if t := data[w.s("overlay_description")]; t != "" {
					b.WriteString("\t\t\t\t\t\t\t\t<div " + w.attrString("gallery_item_description") + ">\n\t\t\t\t\t\t\t\t\t" + t + "\t\t\t\t\t\t\t\t</div>\n")
				}
			}
			b.WriteString("\t\t\t\t\t</div>\n")
		}
		b.WriteString("\t\t\t\t</" + tag + ">\n")
	}
	b.WriteString("\t\t</div>\n")
	return b.String()
}

// ---- off-canvas ----

func init() {
	registerWidget("off-canvas", &widgetDef{render: proCRenderOffCanvas, styles: static("widget-off-canvas")})
	registerWidget("mega-menu", &widgetDef{render: proCRenderMegaMenu, styles: static("widget-mega-menu"), wrapperClass: "elementor-widget-n-menu"})
}

func proCRenderOffCanvas(w *elemCtx) string {
	wa := w.attr("off-canvas__wrapper")
	wa.add("id", "off-canvas-"+w.el.ID).add("class", "e-off-canvas").add("role", "dialog").add("aria-hidden", "true").
		add("aria-label", w.s("off_canvas_name")).add("aria-modal", "true").add("inert", "").add("data-delay-child-handlers", "true")
	w.attr("off-canvas__overlay").add("class", "e-off-canvas__overlay")
	tag := validateHTMLTag(w.s("wrapper_html_tag"))
	var b strings.Builder
	b.WriteString("\t\t<" + tag + " " + wa.String() + ">\n\t\t\t<div " + w.attrString("off-canvas__overlay") + "></div>\n\t\t\t<div class=\"e-off-canvas__main\">\n\t\t\t\t<div class=\"e-off-canvas__content\">\n")
	for _, ch := range w.el.Elements {
		if ch != nil {
			b.WriteString(w.r.printElement(ch))
		}
	}
	b.WriteString("\t\t\t\t</div>\n\t\t\t</div>\n\t\t</" + tag + ">\n")
	return b.String()
}

// ---- mega-menu ----

// proCURLParts mirrors Url_Helper_Trait::parse_url.
func proCURLParts(u string) [3]string {
	var out [3]string
	pu, err := url.Parse(u)
	if err != nil {
		return out
	}
	out[0] = strings.ReplaceAll(pu.Host, "www.", "")
	out[1] = strings.Trim(pu.Path, "/")
	out[2] = pu.RawQuery
	return out
}

// proCCurrentPermalink mirrors get_permalink_for_current_page for singular and archive views.
func proCCurrentPermalink(w *elemCtx) string {
	if th := w.r.rc.Theme; th != nil {
		if th.Archive != nil {
			if a := th.Archive(); a != nil && a.PageURL != nil {
				return a.PageURL(1)
			}
		}
		if th.Post != nil {
			if p := th.Post(); p != nil {
				return p.URL
			}
		}
	}
	return ""
}

func proCMenuCurrentClass(w *elemCtx, link string) string {
	link = strings.TrimSpace(link)
	if strings.Contains(link, "#") {
		return "e-anchor"
	}
	cur := proCCurrentPermalink(w)
	if link == "" || cur == "" {
		return ""
	}
	if proCURLParts(cur) == proCURLParts(link) {
		return "e-current"
	}
	return ""
}

func proCHasDropdownContent(w *elemCtx, index int, has bool) bool {
	return has && index < len(w.el.Elements) && w.el.Elements[index] != nil && len(w.el.Elements[index].Elements) > 0
}

func proCRenderMegaMenu(w *elemCtx) string {
	num := idInt3(w.el.ID)
	nav := w.attr("e-n-menu")
	nav.add("class", "e-n-menu").add("data-widget-number", num)
	if w.has("menu_name") {
		nav.add("aria-label", w.s("menu_name"))
	}
	var titles strings.Builder
	for i, it := range asList(w.get("menu_items")) {
		titles.WriteString(proCMegaMenuTitle(w, i, asMap(it), num))
	}
	anim := ""
	if w.has("menu_toggle_icon_hover_animation") {
		anim = " elementor-animation-" + w.s("menu_toggle_icon_hover_animation")
	}
	tg := w.attr("menu-toggle")
	tg.add("class", "e-n-menu-toggle"+anim).add("id", "menu-toggle-"+num).add("aria-haspopup", "true").add("aria-expanded", "false").
		add("aria-controls", "menubar-"+num).add("aria-label", "Menu Toggle")
	normal := asMap(w.get("menu_toggle_icon_normal"))
	if normal == nil || !truthy(normal["value"]) {
		normal = map[string]any{"library": "eicons", "value": "eicon-menu-bar"}
	}
	active := asMap(w.get("menu_toggle_icon_active"))
	if active == nil || !truthy(active["value"]) {
		active = map[string]any{"library": "eicons", "value": "eicon-close"}
	}
	mw := w.attr("e-n-menu-wrapper")
	mw.add("class", "e-n-menu-wrapper").add("id", "menubar-"+num).add("aria-labelledby", "menu-toggle-"+num)
	var b strings.Builder
	b.WriteString("\t\t<nav " + nav.String() + ">\n\t\t\t\t<button " + tg.String() + ">\n\t\t\t<span class=\"e-n-menu-toggle-icon e-open\">\n\t\t\t\t" +
		renderIcon(normal, nil, "i") + "\t\t\t</span>\n\t\t\t<span class=\"e-n-menu-toggle-icon e-close\">\n\t\t\t\t" + renderIcon(active, nil, "i") +
		"\t\t\t</span>\n\t\t</button>\n\t\t\t\t<div " + mw.String() + ">\n\t\t\t\t<ul class=\"e-n-menu-heading\">\n\t\t\t\t\t" + titles.String() +
		"\t\t\t\t</ul>\n\t\t\t</div>\n\t\t</nav>\n")
	return b.String()
}

func proCMegaMenuTitle(w *elemCtx, index int, item map[string]any, num string) string {
	if item == nil {
		item = map[string]any{}
	}
	hasContent := phpString(item["item_dropdown_content"]) == "yes"
	linkURL := phpString(sub(item["item_link"], "url"))
	classes := []string{"e-n-menu-title"}
	if c := proCMenuCurrentClass(w, linkURL); c != "" {
		classes = append(classes, c)
	}
	if w.s("open_on") == "click" && proCHasDropdownContent(w, index, hasContent) {
		classes = append(classes, "e-click")
	}
	dropClasses := []string{"e-n-menu-dropdown-icon"}
	if hasContent {
		dropClasses = append(dropClasses, "e-focus")
	}
	iconHTML := renderIcon(w.get("menu_item_icon"), newAttrs().add("aria-hidden", "true"), "i")
	iconActiveHTML := renderIcon(w.get("menu_item_icon_active"), newAttrs().add("aria-hidden", "true"), "i")
	display := index + 1
	ds := strconv.Itoa(display)
	itemID := phpString(item["element_id"])
	if itemID == "" {
		itemID = "e-n-menu-title-" + num + ds
	}
	dropID := "e-n-menu-dropdown-icon-" + num + ds
	key := repeaterKey("item_title", "menu_items", display)
	itemIcon := renderIcon(item["item_icon"], newAttrs().add("aria-hidden", "true"), "i")
	itemActiveIcon := itemIcon
	if ia := asMap(item["item_icon_active"]); ia != nil && truthy(ia["value"]) {
		itemActiveIcon = renderIcon(ia, newAttrs().add("aria-hidden", "true"), "i")
	}
	if w.has("hover_animation") {
		classes = append(classes, "elementor-animation-"+w.s("hover_animation"))
	}
	w.attr(key).add("id", itemID).add("class", classes...)
	da := w.attr(key + "_link")
	da.add("id", dropID).add("class", dropClasses...).add("data-tab-index", ds).add("aria-haspopup", tern(hasContent, "true", "false")).
		add("aria-expanded", "false").add("aria-controls", "e-n-menu-content-"+num+ds)
	// title container
	tcKey := "e-n-menu-title-container-" + phpString(item["_id"])
	w.attr(tcKey).remove("class")
	tc := w.attr(tcKey)
	tc.add("class", "e-n-menu-title-container")
	if linkURL != "" {
		tc.add("class", "e-focus", "e-link")
	}
	w.addLinkAttributes(tcKey, item["item_link"], false)
	if proCMenuCurrentClass(w, linkURL) != "" {
		tc.add("aria-current", "page")
	}
	title := phpString(item["item_title"])
	var b strings.Builder
	b.WriteString("\t\t\t<li class=\"e-n-menu-item\">\n\t\t\t\t<div " + w.attrString(key) + ">\n\t\t\t\t\t")
	if linkURL != "" {
		b.WriteString("<a " + tc.String() + ">")
	} else {
		b.WriteString("<div " + tc.String() + ">")
	}
	if itemIcon != "" {
		b.WriteString("\t\t\t\t\t\t\t<span class=\"e-n-menu-icon\">\n\t\t\t\t\t\t\t\t<span class=\"icon-active\">" + itemActiveIcon +
			"</span>\n\t\t\t\t\t\t\t\t<span class=\"icon-inactive\">" + itemIcon + "</span>\n\t\t\t\t\t\t\t</span>\n")
	}
	b.WriteString("\t\t\t\t\t\t<span class=\"e-n-menu-title-text\">\n\t\t\t\t\t\t\t" + title + "\t\t\t\t\t\t</span>\n\t\t\t\t\t")
	if linkURL != "" {
		b.WriteString("</a>")
	} else {
		b.WriteString("</div>")
	}
	if hasContent {
		b.WriteString("\t\t\t\t\t\t\t<button " + da.String() + " >\n\t\t\t\t\t\t\t<span class=\"e-n-menu-dropdown-icon-opened\">\n\t\t\t\t\t\t\t\t" + iconActiveHTML +
			"\t\t\t\t\t\t\t\t<span class=\"elementor-screen-only\">Close " + title + "</span>\n\t\t\t\t\t\t\t</span>\n" +
			"\t\t\t\t\t\t\t<span class=\"e-n-menu-dropdown-icon-closed\">\n\t\t\t\t\t\t\t\t" + iconHTML +
			"\t\t\t\t\t\t\t\t<span class=\"elementor-screen-only\">Open " + title + "</span>\n\t\t\t\t\t\t\t</span>\n\t\t\t\t\t\t</button>\n")
	}
	b.WriteString("\t\t\t\t</div>\n")
	if hasContent {
		b.WriteString("\t\t\t\t\t\t\t\t\t<div class=\"e-n-menu-content\">\n")
		if proCHasDropdownContent(w, index, true) {
			mi := strconv.Itoa(index + 1)
			b.WriteString(w.printNestedChild(index, func(a *attrs) {
				a.add("id", "e-n-menu-content-"+num+mi).add("data-tab-index", mi).add("aria-labelledby", dropID)
			}))
		}
		b.WriteString("\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t</li>\n\t\t")
	return b.String()
}
