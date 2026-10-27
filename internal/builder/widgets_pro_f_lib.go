package builder

// Link in Bio: the free link-in-bio ("Minimalist") widget and upstream Pro's variants
// link-in-bio-var-2..7. Ports of modules/link-in-bio/classes/render/*.php (core: Core_Render,
// Render_Base; Pro: Icons_Below_Cta_Render, Single_Button_Cta_Render).

import (
	"strconv"
	"strings"
)

func init() {
	reg := func(name string, layout func(*proFBio) string, footer bool) {
		registerWidget(name, &widgetDef{
			render: func(w *elemCtx) string { return layout(&proFBio{w: w, name: name, footerBio: footer}) },
			styles: func(*elemCtx) []string {
				if name == "link-in-bio" {
					return []string{"widget-link-in-bio-base"}
				}
				return []string{"widget-link-in-bio-base", "widget-" + name}
			}})
	}
	reg("link-in-bio", (*proFBio).renderCore, false)
	reg("link-in-bio-var-2", (*proFBio).renderIconsBelowCta, false)
	reg("link-in-bio-var-3", (*proFBio).renderCore, false)
	reg("link-in-bio-var-4", (*proFBio).renderIconsBelowCta, false)
	reg("link-in-bio-var-5", (*proFBio).renderCore, false)
	reg("link-in-bio-var-6", (*proFBio).renderCore, false)
	reg("link-in-bio-var-7", (*proFBio).renderSingleCta, true)
}

type proFBio struct {
	w         *elemCtx
	name      string
	footerBio bool // get_description_position() === 'bottom'
	ctas      []map[string]any
}

func (b *proFBio) s(k string) string { return b.w.s(k) }

// orDefault mirrors `$settings[k] ?? def` (only null falls back).
func (b *proFBio) orDefault(k, def string) string {
	if b.w.get(k) == nil {
		return def
	}
	return b.s(k)
}

// proFCleanArray mirrors Shared_Widget_Controls_Trait::clean_array: drops items with no value.
func proFCleanArray(list []any) []map[string]any {
	var out []map[string]any
	for _, it := range list {
		m := asMap(it)
		if m == nil {
			continue
		}
		keep := false
		for _, v := range m {
			if v != nil && !strictEq(v, "") {
				keep = true
				break
			}
		}
		if keep {
			out = append(out, m)
		}
	}
	return out
}

// linkAttrs mirrors get_link_attributes: href/target/rel of the link, then the other attributes
// (array_merge: a repeated key keeps its first position), then the link's custom attributes.
func (b *proFBio) linkAttrs(key string, link any, other [][2]string) {
	lm := asMap(link)
	type kv struct{ k, v string }
	var list []kv
	set := func(k, v string) {
		for i := range list {
			if list[i].k == k {
				list[i].v = v
				return
			}
		}
		list = append(list, kv{k, v})
	}
	rel := ""
	if truthy(lm["url"]) {
		set("href", escURL(phpString(lm["url"])))
	}
	if truthy(lm["is_external"]) {
		set("target", "_blank")
		rel += "noopener "
	}
	if truthy(lm["nofollow"]) {
		rel += "nofollow "
	}
	if rel != "" {
		set("rel", rel)
	}
	for _, o := range other {
		set(o[0], o[1])
	}
	for _, ca := range parseCustomAttributes(phpString(lm["custom_attributes"])) {
		set(ca[0], ca[1])
	}
	a := b.w.attr(key)
	for _, x := range list {
		a.add(x.k, x.v)
	}
}

func (b *proFBio) image(img map[string]any, size, class, key string) string {
	return proFImage(b.w, img, size, class, key)
}

func proFHasImage(img map[string]any) bool {
	return img != nil && (truthy(img["url"]) || truthy(img["id"]))
}

func (b *proFBio) layout() string {
	c := "e-link-in-bio e-" + b.name
	if b.s("background_show_border") == "yes" {
		c += " has-border"
	}
	if b.s("advanced_layout_full_width_custom") == "yes" {
		c += " is-full-width"
	}
	if b.s("advanced_layout_full_screen_height") == "yes" {
		c += " is-full-height"
	}
	for _, bp := range asList(b.w.get("advanced_layout_full_screen_height_controls")) {
		c += " is-full-height-" + phpString(bp)
	}
	if v := b.s("advanced_custom_css_classes"); v != "" {
		c += " " + v
	}
	a := b.w.attr("layout")
	a.add("class", c)
	if v := b.s("advanced_custom_css_id"); v != "" {
		a.add("id", v)
	}
	return "<div " + a.String() + ">\n"
}

const proFBioBg = "<div class=\"e-link-in-bio__bg\">\n<div class=\"e-link-in-bio__bg-overlay\"></div>\n</div>\n</div>\n"

func (b *proFBio) renderCore() string {
	return b.layout() + "<div class=\"e-link-in-bio__content\">\n" + b.identity() + b.bio() + b.icons() + b.imageLinks() + b.ctaLinks() +
		b.footer() + "</div>\n" + proFBioBg
}

func (b *proFBio) renderIconsBelowCta() string {
	return b.layout() + "<div class=\"e-link-in-bio__content\">\n" + b.identity() + b.bio() + b.ctaLinks() + b.icons() + "</div>\n" + proFBioBg
}

// renderSingleCta mirrors Single_Button_Cta_Render: the CTA section is one button built from the
// widget's own cta_* settings.
func (b *proFBio) renderSingleCta() string {
	item := map[string]any{"cta_link_text": b.orDefault("cta_section_button_text", "")}
	for _, k := range []string{"cta_link_type", "cta_link_url", "cta_link_mail", "cta_link_mail_subject", "cta_link_mail_body",
		"cta_link_number", "cta_link_location", "cta_link_username", "cta_link_file"} {
		item[k] = b.w.get(k)
	}
	b.ctas = []map[string]any{item}
	return b.renderCore()
}

func (b *proFBio) imageLinks() string {
	items := proFCleanArray(asList(b.w.get("image_links")))
	if len(items) == 0 {
		return ""
	}
	cols := b.orDefault("image_links_per_row", "2")
	cls := "e-link-in-bio__image-links"
	if cols != "" && cols != "0" {
		cls += " has-" + cols + "-columns"
	}
	var out strings.Builder
	out.WriteString("<div class=\"" + escAttr(cls) + "\">\n")
	for i, it := range items {
		key := "image-links-link" + strconv.Itoa(i)
		b.linkAttrs(key, it["image_links_url"], [][2]string{{"class", "e-link-in-bio__image-links-link"},
			{"href", escURL(phpString(sub(it["image_links_url"], "url")))}})
		out.WriteString("<a " + b.w.attrString(key) + ">\n")
		out.WriteString(b.image(asMap(it["image_links_image"]), "thumbnail", "e-link-in-bio__image-links-img", "image-links-img-"+strconv.Itoa(i)))
		out.WriteString("</a>\n")
	}
	out.WriteString("</div>\n")
	return out.String()
}

// ctaLink mirrors get_formatted_link_based_on_type_for_cta.
func proFCtaLink(cta map[string]any) string {
	g := func(k string) string { return phpString(cta[k]) }
	link := phpString(sub(cta["cta_link_url"], "url"))
	switch g("cta_link_type") {
	case "Email":
		return proFEmailLink(g("cta_link_mail"), g("cta_link_mail_subject"), g("cta_link_mail_body"))
	case "Telephone":
		if n := g("cta_link_number"); n != "" {
			return "tel:" + n
		}
		return ""
	case "Messenger":
		if n := g("cta_link_username"); n != "" {
			return "https://m.me/" + n
		}
		return ""
	case "Waze":
		return phpString(sub(cta["cta_link_location"], "url"))
	case "WhatsApp":
		if n := g("cta_link_number"); n != "" {
			return "https://wa.me/" + n
		}
		return ""
	case "File Download", "Save contact (vCard)":
		return phpString(sub(cta["cta_link_file"], "url"))
	}
	return link
}

func (b *proFBio) ctaLinks() string {
	items := b.ctas
	if items == nil {
		items = proFCleanArray(asList(b.w.get("cta_link")))
	} else {
		items = proFCleanArray([]any{items[0]})
	}
	if len(items) == 0 {
		return ""
	}
	corners := b.orDefault("cta_links_corners", "rounded")
	border := truthy(b.w.get("cta_links_show_border"))
	typ := b.orDefault("cta_links_type", "button")
	var out strings.Builder
	out.WriteString("<div class=\"e-link-in-bio__ctas has-type-" + escAttr(typ) + "\">\n")
	for i, cta := range items {
		img := asMap(cta["cta_link_image"])
		hasImage := proFHasImage(img) && typ == "button"
		cls := "e-link-in-bio__cta is-type-" + typ
		if typ == "button" && border {
			cls += " has-border"
		}
		if hasImage {
			cls += " has-image"
		}
		if typ == "button" {
			cls += " has-corners-" + corners
		}
		other := [][2]string{{"class", cls}, {"href", escURL(proFCtaLink(cta))}}
		if lt := phpString(cta["cta_link_type"]); lt == "File Download" || lt == "Save contact (vCard)" {
			other = append(other, [2]string{"download", "download"})
		}
		ctaURL := cta["cta_link_url"]
		if phpString(cta["cta_link_type"]) == "Waze" {
			ctaURL = cta["cta_link_location"]
		}
		key := "cta-" + strconv.Itoa(i)
		b.linkAttrs(key, ctaURL, other)
		out.WriteString("<a " + b.w.attrString(key) + ">\n")
		if hasImage {
			out.WriteString("<span class=\"e-link-in-bio__cta-image\">\n" + b.image(img, "thumbnail", "e-link-in-bio__cta-image-element", "cta-link-image"+strconv.Itoa(i)) + "</span>\n")
		}
		out.WriteString("<span class=\"e-link-in-bio__cta-text\">\n" + escHTML(phpString(cta["cta_link_text"])) + "\n</span>\n</a>\n")
	}
	out.WriteString("</div>\n")
	return out.String()
}

// iconLink mirrors get_formatted_link_for_icon.
func proFIconLink(icon map[string]any) string {
	g := func(k string) string { return phpString(icon[k]) }
	switch g("icon_platform") {
	case "Email":
		return proFEmailLink(g("icon_mail"), g("icon_mail_subject"), g("icon_mail_body"))
	case "Telephone":
		if n := g("icon_number"); n != "" {
			return "tel:" + n
		}
		return ""
	case "Messenger":
		if n := g("icon_username"); n != "" {
			return "https://m.me/" + n
		}
		return ""
	case "Waze":
		return phpString(sub(icon["icon_location"], "url"))
	case "WhatsApp":
		if n := g("icon_number"); n != "" {
			return "https://wa.me/" + n
		}
		return ""
	}
	return phpString(sub(icon["icon_url"], "url"))
}

func (b *proFBio) icons() string {
	list := asList(b.w.get("icon"))
	if len(list) == 0 {
		return ""
	}
	size := b.orDefault("icons_size", "small")
	border := truthy(b.w.get("icons_border_show_border"))
	var out strings.Builder
	out.WriteString("<div class=\"e-link-in-bio__icons has-size-" + escAttr(size) + "\">\n")
	for i, it := range list {
		icon := asMap(it)
		if icon == nil {
			continue
		}
		cls := "e-link-in-bio__icon is-size-" + size
		if border {
			cls += " has-border"
		}
		platform := phpString(icon["icon_platform"])
		iconURL := icon["icon_url"]
		if platform == "Waze" {
			iconURL = icon["icon_location"]
		}
		key := "icon-link-" + strconv.Itoa(i)
		b.linkAttrs(key, iconURL, [][2]string{{"aria-label", platform}, {"class", "e-link-in-bio__icon-link"}, {"href", escURL(proFIconLink(icon))}})
		out.WriteString("<div class=\"" + escAttr(cls) + "\">\n<a " + b.w.attrString(key) + ">\n<span class=\"e-link-in-bio__icon-svg\">\n" +
			proFNetworkIcon(platform, true) + "\n</span>\n")
		if t := phpString(icon["icon_text"]); t != "" {
			out.WriteString("<span class=\"e-link-in-bio__icon-label\">\n" + escHTML(t) + "\n</span>\n")
		}
		out.WriteString("</a>\n</div>\n")
	}
	out.WriteString("</div>\n")
	return out.String()
}

func (b *proFBio) heading(key, tagKey, def, class, text string) string {
	tag := validateHTMLTag(b.orDefault(tagKey, def))
	return "<" + tag + " class=\"" + class + "\">" + escHTML(text) + "</" + tag + ">\n"
}

func (b *proFBio) bio() string {
	heading, title := b.s("bio_heading"), b.s("bio_title")
	var about, desc string
	if !b.footerBio {
		about, desc = b.s("bio_about"), b.s("bio_description")
	}
	if heading == "" && title == "" && about == "" && desc == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString("<div class=\"e-link-in-bio__bio\">\n")
	if heading != "" {
		out.WriteString(b.heading("heading", "bio_heading_tag", "h2", "e-link-in-bio__heading", heading))
	}
	if title != "" {
		out.WriteString(b.heading("title", "bio_title_tag", "h2", "e-link-in-bio__title", title))
	}
	if about != "" {
		out.WriteString(b.heading("about-heading", "bio_about_tag", "h3", "e-link-in-bio__about-heading", about))
	}
	if desc != "" {
		out.WriteString("<p class=\"e-link-in-bio__description\">" + escHTML(desc) + "</p>\n")
	}
	out.WriteString("</div>\n")
	return out.String()
}

func (b *proFBio) footer() string {
	if !b.footerBio {
		return ""
	}
	about, desc := b.s("bio_about"), b.s("bio_description")
	if about == "" && desc == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString("<div class=\"e-link-in-bio__bio e-link-in-bio__bio--footer\">\n")
	if about != "" {
		out.WriteString(b.heading("about-heading", "bio_about_tag", "h3", "e-link-in-bio__about-heading", about))
	}
	if desc != "" {
		out.WriteString("<p class=\"e-link-in-bio__description\">" + escHTML(desc) + "</p>\n")
	}
	out.WriteString("</div>\n")
	return out.String()
}

func (b *proFBio) identity() string {
	primary := asMap(b.w.get("identity_image"))
	secondary := asMap(b.w.get("identity_image_cover"))
	pr, sr := proFHasImage(primary), proFHasImage(secondary)
	if !pr && !sr {
		return ""
	}
	type img struct {
		value                                    map[string]any
		render                                   bool
		style, shape                             string
		showBorder, showBottomBorder, hasDivider bool
	}
	p := img{value: primary, render: pr}
	s := img{value: secondary, render: sr}
	if pr {
		p.shape = b.orDefault("identity_image_shape", "circle")
		p.style = b.orDefault("identity_image_style", "profile")
		p.showBorder = truthy(b.w.get("identity_image_show_border"))
		p.showBottomBorder = truthy(b.w.get("identity_image_bottom_show_border"))
	}
	if sr {
		s.style = "cover"
		s.showBottomBorder = truthy(b.w.get("identity_image_bottom_show_border"))
		if b.w.has("identity_section_style_cover_divider_bottom") {
			s.hasDivider = true
			s.showBottomBorder = false
		}
		p.style = "profile"
	}
	var out strings.Builder
	out.WriteString("<div class=\"e-link-in-bio__identity\">\n")
	for i, im := range []img{s, p} {
		if !im.render {
			continue
		}
		cls := "e-link-in-bio__identity-image e-link-in-bio__identity-image-" + im.style
		if im.showBorder || im.showBottomBorder {
			cls += " has-border"
		}
		if im.shape != "" && im.style == "profile" {
			cls += " has-style-" + im.shape
		}
		if im.hasDivider {
			cls += " has-shape-divider"
		}
		key := []string{"secondary_image", "primary_image"}[i]
		out.WriteString("<div class=\"" + escAttr(cls) + "\">\n")
		out.WriteString(b.image(im.value, "medium", "e-link-in-bio__identity-image-element", "identity_image_src"+key))
		if im.hasDivider {
			out.WriteString(b.shapeDivider())
		}
		out.WriteString("</div>\n")
	}
	out.WriteString("</div>\n")
	return out.String()
}

// shapeDivider mirrors print_shape_divider( 'bottom' ).
func (b *proFBio) shapeDivider() string {
	file := b.s("identity_section_style_cover_divider_bottom")
	if file == "" {
		return ""
	}
	negative := b.w.has("identity_section_style_cover_divider_bottom_negative")
	svg := shapeSVG(file, negative)
	if svg == "" {
		return ""
	}
	neg := "false"
	if negative {
		neg = "true"
	}
	return "<div\n\t\t\tclass=\"elementor-shape elementor-shape-bottom\"\n\t\t\taria-hidden=\"true\"\n\t\t\tdata-negative=\"" + neg + "\"\n\t\t>\n" + svg + "\n</div>\n"
}
