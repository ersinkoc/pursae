package builder

// Floating elements: the free contact-buttons ("Single Chat") and floating-bars-var-1 widgets and
// upstream Pro's variants contact-buttons-var-1,3..10 and floating-bars-var-2,3. Ports of
// modules/floating-buttons/classes/render/*.php (core and Pro) and Social_Network_Provider.

import (
	"net/url"
	"strconv"
	"strings"
)

func init() {
	cb := func(name string, render func(*proFFloat) string) {
		registerWidget(name, &widgetDef{
			render: func(w *elemCtx) string { return render(&proFFloat{w: w, name: name}) },
			styles: func(*elemCtx) []string {
				if name == "contact-buttons" {
					return []string{"widget-contact-buttons-base"}
				}
				return []string{"widget-contact-buttons-base", "widget-" + name}
			}})
	}
	cb("contact-buttons", (*proFFloat).renderCore)
	cb("contact-buttons-var-1", (*proFFloat).renderVar1)
	cb("contact-buttons-var-3", (*proFFloat).renderVar3)
	cb("contact-buttons-var-4", (*proFFloat).renderVar4)
	cb("contact-buttons-var-5", (*proFFloat).renderVar5)
	cb("contact-buttons-var-6", (*proFFloat).renderVar6)
	cb("contact-buttons-var-7", (*proFFloat).renderVar7)
	cb("contact-buttons-var-8", (*proFFloat).renderVar8)
	cb("contact-buttons-var-9", (*proFFloat).renderVar9)
	cb("contact-buttons-var-10", (*proFFloat).renderVar10)
	fb := func(name string, render func(*proFFloat) string) {
		registerWidget(name, &widgetDef{
			render: func(w *elemCtx) string { return render(&proFFloat{w: w, name: name}) },
			styles: static("widget-floating-bars-base", "widget-"+name)})
	}
	fb("floating-bars-var-1", (*proFFloat).renderBarsVar1)
	fb("floating-bars-var-2", (*proFFloat).renderBarsVar2)
	fb("floating-bars-var-3", (*proFFloat).renderBarsVar3)
}

// --- Social_Network_Provider ----------------------------------------------------------------------------

type proFNetwork struct{ text, icon, name string }

var proFNetworks = map[string]proFNetwork{
	"Save contact (vCard)": {"Save contact (vCard)", "fab fa-outlook", "vcf"},
	"Facebook":             {"Facebook", "fab fa-facebook", "facebook"},
	"X (Twitter)":          {"X (Twitter)", "fab fa-x-twitter", "x-twitter"},
	"Instagram":            {"Instagram", "fab fa-instagram", "instagram"},
	"LinkedIn":             {"LinkedIn", "fab fa-linkedin-in", "linkedin"},
	"Pinterest":            {"Pinterest", "fab fa-pinterest", "pinterest"},
	"YouTube":              {"YouTube", "fab fa-youtube", "youtube"},
	"TikTok":               {"TikTok", "fab fa-tiktok", "tiktok"},
	"WhatsApp":             {"WhatsApp", "fab fa-whatsapp", "whatsapp"},
	"Apple Music":          {"Apple Music", "fa fa-music", "apple-music"},
	"Spotify":              {"Spotify", "fab fa-spotify", "spotify"},
	"SoundCloud":           {"SoundCloud", "fab fa-soundcloud", "soundcloud"},
	"Behance":              {"Behance", "fab fa-behance", "behance"},
	"Dribbble":             {"Dribbble", "fab fa-dribbble", "dribble"},
	"Vimeo":                {"Vimeo", "fab fa-vimeo-v", "vimeo"},
	"Waze":                 {"Waze", "fab fa-waze", "waze"},
	"Messenger":            {"Messenger", "fab fa-facebook-messenger", "messenger"},
	"Telephone":            {"Telephone", "fas fa-phone-alt", "phone"},
	"Email":                {"Email", "fas fa-envelope", "email"},
	"Url":                  {"URL", "fas fa-globe", "url"},
	"File Download":        {"File Download", "fas fa-download", "download"},
	"SMS":                  {"SMS", "fas fa-sms", "sms"},
	"VIBER":                {"Viber", "fab fa-viber", "viber"},
	"Skype":                {"Skype", "fab fa-skype", "skype"},
}

// proFNetworkIcon renders a platform's icon (Icons_Manager::render_icon with the mapped class).
func proFNetworkIcon(platform string, hidden bool) string {
	mapping := proFNetworks[platform].icon
	lib := "fa-solid"
	if strings.SplitN(mapping, " ", 2)[0] == "fab" {
		lib = "fa-brands"
	}
	var a *attrs
	if hidden {
		a = newAttrs().add("aria-hidden", "true")
	}
	return renderIcon(map[string]any{"library": lib, "value": mapping}, a, "i")
}

// proFEmailLink mirrors Social_Network_Provider::build_email_link.
func proFEmailLink(email, subject, body string) string {
	if email == "" {
		return ""
	}
	link := "mailto:" + email
	if subject != "" {
		link += "?subject=" + subject
	}
	if body != "" {
		if subject != "" {
			link += "&"
		} else {
			link += "?"
		}
		link += "body=" + body
	}
	return link
}

// proFLink is the $link array the render classes build.
type proFLink struct {
	platform, number, username, viberAction string
	mail, subject, body                     string
	url, location                           any
}

// formatted mirrors Contact_Buttons_Render_Base::get_formatted_link.
func (l proFLink) formatted() string {
	switch l.platform {
	case "Email":
		return proFEmailLink(l.mail, l.subject, l.body)
	case "SMS":
		if l.number != "" {
			return "sms:" + l.number
		}
	case "Messenger":
		if l.username != "" {
			return "https://m.me/" + l.username
		}
	case "WhatsApp":
		if l.number != "" {
			return "https://wa.me/" + l.number
		}
	case "VIBER":
		if l.number != "" {
			action := l.viberAction
			return "viber://" + action + "?number=" + url.QueryEscape(l.number)
		}
	case "Skype":
		if l.username != "" {
			return "skype:" + l.username + "?chat"
		}
	case "Telephone":
		if l.number != "" {
			return "tel:" + l.number
		}
	}
	return ""
}

func proFIsURLLink(platform string) bool { return platform == "Url" || platform == "Waze" }

// --- render state -------------------------------------------------------------------------------------

type proFFloat struct {
	w    *elemCtx
	name string
}

func (f *proFFloat) s(k string) string { return f.w.s(k) }

func (f *proFFloat) documentID() string {
	if p := proFCurrentPost(f.w); p != nil {
		return strconv.FormatInt(p.ID, 10)
	}
	if f.w.r.doc != nil {
		return strconv.FormatInt(f.w.r.doc.ID, 10)
	}
	return "0"
}

// chatLink is the chat button's link (settings chat_button_*).
func (f *proFFloat) chatLink() proFLink {
	return proFLink{platform: f.s("chat_button_platform"), number: f.s("chat_button_number"), username: f.s("chat_button_username"),
		mail: f.s("chat_button_mail"), subject: f.s("chat_button_mail_subject"), body: f.s("chat_button_mail_body"),
		viberAction: f.s("chat_button_viber_action"), location: f.w.get("chat_button_waze"), url: f.w.get("chat_button_url")}
}

// itemLink is a contact repeater item's link (contact_icon_*).
func proFItemLink(it map[string]any) proFLink {
	g := func(k string) string { return phpString(it[k]) }
	return proFLink{platform: g("contact_icon_platform"), number: g("contact_icon_number"), username: g("contact_icon_username"),
		mail: g("contact_icon_mail"), subject: g("contact_icon_mail_subject"), body: g("contact_icon_mail_body"),
		viberAction: g("contact_icon_viber_action"), url: it["contact_icon_url"], location: it["contact_icon_waze"]}
}

// linkAttributes mirrors render_link_attributes (Waze / URL links use add_link_attributes).
func (f *proFFloat) linkAttributes(l proFLink, key string) {
	var m map[string]any
	if l.platform == "Waze" {
		m = proFCopyMap(asMap(l.location))
	} else {
		m = proFCopyMap(asMap(l.url))
	}
	if !truthy(m["url"]) {
		m["url"] = "#"
	}
	f.w.addLinkAttributes(key, m, false)
}

func proFCopyMap(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (f *proFFloat) items() []map[string]any {
	var out []map[string]any
	for _, it := range asList(f.w.get("contact_repeater")) {
		if m := asMap(it); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func (f *proFFloat) layoutAttr(classes string) {
	f.w.attr("layout").add("class", classes).add("id", f.s("advanced_custom_css_id")).
		add("data-document-id", f.documentID()).add("role", "dialog")
}

// layoutCore mirrors Contact_Buttons_Render_Base::build_layout_render_attribute.
func (f *proFFloat) layoutCore() {
	c := "e-contact-buttons e-" + f.name
	if p := f.s("chat_button_platform"); p != "" {
		c += " has-platform-" + proFNetworks[p].name
	}
	if v := f.s("style_chat_box_corners"); v != "" {
		c += " has-corners-" + v
	}
	c += f.alignClasses()
	if f.hasAnimations() {
		c += " has-animations"
	}
	if v := f.s("advanced_custom_css_classes"); v != "" {
		c += " " + v
	}
	f.layoutAttr(c)
}

func (f *proFFloat) alignClasses() string {
	c := ""
	if v := f.s("advanced_horizontal_position"); v != "" {
		c += " has-h-alignment-" + v
	}
	if v := f.s("advanced_vertical_position"); v != "" {
		c += " has-v-alignment-" + v
	}
	return c
}

func (f *proFFloat) hasAnimations() bool {
	return f.w.has("style_chat_box_exit_animation") || f.w.has("style_chat_box_entrance_animation")
}

func (f *proFFloat) customClasses() string {
	if v := f.s("advanced_custom_css_classes"); v != "" {
		return " " + v
	}
	return ""
}

func (f *proFFloat) contentWrapperAttr() string {
	return `aria-hidden="true" aria-label="Links window" class="e-contact-buttons__content-wrapper hidden" id="e-contact-buttons__content-wrapper"`
}

func (f *proFFloat) contentClass(withDuration bool) string {
	c := "e-contact-buttons__content"
	if withDuration {
		if v := f.s("style_chat_box_animation_duration"); v != "" {
			c += " has-animation-duration-" + v
		}
	}
	return c
}

// chatButtonClasses are the chat button's classes (entrance animation flags optional).
func (f *proFFloat) chatButtonClasses(base string, entrance, dot bool) string {
	c := base
	if v := f.s("style_chat_button_size"); v != "" {
		c += " has-size-" + v
	}
	if v := f.s("style_button_color_hover_animation"); v != "" {
		c += " elementor-animation-" + v
	}
	if entrance {
		if v := f.s("style_chat_button_animation"); v != "" && v != "none" {
			c += " has-entrance-animation"
		}
		if f.w.has("style_chat_button_animation_delay") {
			c += " has-entrance-animation-delay"
		}
		if v := f.s("style_chat_button_animation_duration"); v != "" {
			c += " has-entrance-animation-duration-" + v
		}
	}
	if dot && f.s("chat_button_show_dot") == "yes" {
		c += " has-dot"
	}
	return c
}

func (f *proFFloat) customIcon() string { return renderIcon(f.w.get("chat_button_icon"), nil, "i") }

// --- core pieces -------------------------------------------------------------------------------------------

func (f *proFFloat) chatButtonCore(icon string) string {
	a := f.w.attr("button")
	a.add("class", f.chatButtonClasses("e-contact-buttons__chat-button e-contact-buttons__chat-button-shadow", true, true)).
		add("aria-controls", "e-contact-buttons__content-wrapper").add("aria-label", "Toggle "+f.s("chat_aria_label")).add("type", "button")
	return "<div class=\"e-contact-buttons__chat-button-container\">\n<button " + a.String() + ">\n" + icon + "\n</button>\n</div>\n"
}

func (f *proFFloat) closeButtonCore() string {
	a := f.w.attr("close-button")
	a.add("class", "e-contact-buttons__close-button").add("aria-controls", "e-contact-buttons__content-wrapper").
		add("aria-label", "Close "+f.s("chat_aria_label")).add("type", "button")
	return "<button " + a.String() + ">\n<i class=\"eicon-close\"></i>\n</button>\n"
}

func (f *proFFloat) topBarCore() string {
	img := asMap(f.w.get("top_bar_image"))
	cls := "e-contact-buttons__profile-image"
	if v := f.s("style_top_bar_image_size"); v != "" {
		cls += " has-size-" + v
	}
	if f.s("top_bar_show_dot") == "yes" {
		cls += " has-dot"
	}
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__top-bar\">\n")
	b.WriteString(f.closeButtonCore())
	b.WriteString("<div class=\"" + escAttr(cls) + "\">\n")
	b.WriteString(f.image(img, "medium", "e-contact-buttons__profile-image-el", "profile-image-src"))
	b.WriteString("</div>\n<div class=\"e-contact-buttons__top-bar-details\">\n")
	if t := f.s("top_bar_title"); t != "" {
		b.WriteString("<p class=\"e-contact-buttons__top-bar-title\">" + escHTML(t) + "</p>\n")
	}
	if t := f.s("top_bar_subtitle"); t != "" {
		b.WriteString("<p class=\"e-contact-buttons__top-bar-subtitle\">" + escHTML(t) + "</p>\n")
	}
	b.WriteString("</div>\n</div>\n")
	return b.String()
}

// image prints an attachment image (wp_get_attachment_image) or a plain <img> for a URL.
func (f *proFFloat) image(img map[string]any, size, class, key string) string {
	return proFImage(f.w, img, size, class, key)
}

func proFImage(w *elemCtx, img map[string]any, size, class, key string) string {
	if id := toInt64(img["id"]); id > 0 {
		if a := w.r.attachment(id); a != nil {
			return w.r.wpGetAttachmentImage(a, size, newAttrs().add("class", class))
		}
		return ""
	}
	a := w.attr(key)
	a.add("alt", "").add("class", class).add("src", escURL(phpString(img["url"])))
	return "<img " + a.String() + " />\n"
}

func (f *proFFloat) bubbleContainer() string {
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__bubble-container\">\n<div class=\"e-contact-buttons__bubble\">\n")
	if t := f.s("message_bubble_name"); t != "" {
		b.WriteString("<p class=\"e-contact-buttons__message-bubble-name\">" + escHTML(t) + "</p>\n")
	}
	if t := f.s("message_bubble_body"); t != "" {
		b.WriteString("<p class=\"e-contact-buttons__message-bubble-body\">" + escHTML(t) + "</p>\n")
	}
	b.WriteString("<p class=\"e-contact-buttons__message-bubble-time\" data-time-format=\"" + escAttr(f.s("chat_button_time_format")) + "\"></p>\n")
	b.WriteString("</div>\n</div>\n")
	return b.String()
}

func (f *proFFloat) messageBubbleCore(typing bool) string {
	cls := "e-contact-buttons__message-bubble"
	anim := typing && f.s("chat_button_show_animation") == "yes"
	if anim {
		cls += " has-typing-animation"
	}
	var b strings.Builder
	b.WriteString("<div class=\"" + cls + "\">\n")
	if anim {
		b.WriteString("<div class=\"e-contact-buttons__dots-container\">\n<span class=\"e-contact-buttons__dot e-contact-buttons__dot-1\"></span>\n" +
			"<span class=\"e-contact-buttons__dot e-contact-buttons__dot-2\"></span>\n<span class=\"e-contact-buttons__dot e-contact-buttons__dot-3\"></span>\n</div>\n")
	}
	b.WriteString(f.bubbleContainer())
	b.WriteString("</div>\n")
	return b.String()
}

func (f *proFFloat) sendButtonCore() string {
	platform := f.s("chat_button_platform")
	cls := "e-contact-buttons__send-cta"
	if v := f.s("style_send_hover_animation"); v != "" {
		cls += " elementor-animation-" + v
	}
	a := f.w.attr("formatted-cta")
	a.add("class", cls).add("href", f.chatLink().formatted()).add("rel", "noopener noreferrer").add("target", "_blank")
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__send-button\">\n<div class=\"e-contact-buttons__send-button-container\">\n")
	if t := f.s("send_button_text"); t != "" {
		b.WriteString("<a " + a.String() + ">\n" + proFNetworkIcon(platform, true) + "\n" + escHTML(t) + "\n</a>\n")
	}
	b.WriteString("</div>\n</div>\n")
	return b.String()
}

func (f *proFFloat) contactLinksCore() string {
	size := f.s("style_contact_button_size")
	if f.w.get("style_contact_button_size") == nil {
		size = "small"
	}
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__contact-links\">\n")
	for i, it := range f.items() {
		l := proFItemLink(it)
		cls := "e-contact-buttons__contact-icon-link has-size-" + size
		if v := f.s("style_contact_button_hover_animation"); v != "" {
			cls += " elementor-animation-" + v
		}
		key := "icon-link-" + strconv.Itoa(i)
		f.w.attr(key).add("aria-label", "Open "+proFNetworks[l.platform].text).add("class", cls).add("href", l.formatted()).
			add("rel", "noopener noreferrer").add("target", "_blank")
		b.WriteString("<a " + f.w.attrString(key) + ">\n" + proFNetworkIcon(l.platform, true) + "\n</a>\n")
	}
	b.WriteString("</div>\n")
	return b.String()
}

func (f *proFFloat) contactSectionCore() string {
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__contact\">\n")
	if t := f.s("contact_cta_text"); t != "" {
		b.WriteString("<p class=\"e-contact-buttons__contact-text\">" + escHTML(t) + "</p>\n")
	}
	b.WriteString(f.contactLinksCore())
	b.WriteString("</div>\n")
	return b.String()
}

// itemAnchor adds an item link's attributes: URL/Waze links get add_link_attributes, others the
// formatted link (aria-label first, as the Pro variants order it unless ariaLast).
func (f *proFFloat) itemAnchor(key string, l proFLink, cls, aria string, ariaLast bool) {
	a := f.w.attr(key)
	if proFIsURLLink(l.platform) {
		f.linkAttributes(l, key)
		a.add("class", cls)
		if ariaLast && aria != "" {
			a.add("aria-label", aria)
		}
		return
	}
	if !ariaLast {
		a.add("aria-label", aria)
	}
	a.add("class", cls).add("href", l.formatted()).add("rel", "noopener noreferrer").add("target", "_blank")
	if ariaLast {
		a.add("aria-label", aria)
	}
}

func (f *proFFloat) wrapContent(layout, inner, after string) string {
	return "<div " + f.w.attrString("layout") + ">\n" + inner + after + "</div>\n"
}

func (f *proFFloat) contentBlock(contentClass, inner string) string {
	return "<div " + f.contentWrapperAttr() + ">\n<div class=\"" + contentClass + "\">\n" + inner + "</div>\n</div>\n"
}

// --- contact buttons variants --------------------------------------------------------------------------------

func (f *proFFloat) renderCore() string {
	f.layoutCore()
	inner := f.contentBlock(f.contentClass(true), f.topBarCore()+f.messageBubbleCore(true)+f.sendButtonCore())
	return f.wrapContent("", inner, f.chatButtonCore(proFNetworkIcon(f.s("chat_button_platform"), true)))
}

func (f *proFFloat) renderVar1() string {
	f.layoutCore()
	inner := f.contentBlock(f.contentClass(true), f.topBarCore()+f.messageBubbleCore(false)+f.contactSectionCore())
	return f.wrapContent("", inner, f.chatButtonCore(f.customIcon()))
}

func (f *proFFloat) renderVar3() string {
	c := "e-contact-buttons e-" + f.name + f.alignClasses()
	if v := f.s("style_chat_box_corners"); v != "" {
		c += " has-corners-" + v
	}
	if f.hasAnimations() {
		c += " has-animations"
	}
	c += f.customClasses()
	f.w.attr("layout").add("class", c).add("id", f.s("advanced_custom_css_id"))
	// top bar
	close := f.w.attr("close-button")
	close.add("class", "e-contact-buttons__close-button").add("aria-controls", "e-contact-buttons__content-wrapper").
		add("aria-label", "Close "+f.s("chat_aria_label")).add("type", "button")
	var top strings.Builder
	top.WriteString("<div class=\"e-contact-buttons__top-bar\">\n<button " + close.String() + ">\n<i class=\"eicon-close\"></i>\n</button>\n<div class=\"e-contact-buttons__top-bar-details\">\n")
	if t := f.s("top_bar_title"); t != "" {
		top.WriteString("<p class=\"e-contact-buttons__top-bar-title\">" + escHTML(t) + "</p>\n")
	}
	top.WriteString("</div>\n</div>\n")
	// links
	var links strings.Builder
	links.WriteString("<div class=\"e-contact-buttons__links\">\n")
	for i, it := range f.items() {
		l := proFItemLink(it)
		cls := "e-contact-buttons__contact-icon-link"
		if v := f.s("style_info_links_icon_position"); v != "" {
			cls += " has-icon-position-" + v
		}
		if f.s("style_info_links_dividers") == "yes" {
			cls += " has-dividers"
		}
		if v := f.s("style_info_links_hover_animation"); v != "" {
			cls += " elementor-animation-" + v
		}
		key := "icon-link-" + strconv.Itoa(i)
		f.itemAnchor(key, l, cls, l.platform, false)
		links.WriteString("<a " + f.w.attrString(key) + ">\n<span class=\"e-contact-buttons__contact-icon-container\">\n" + proFNetworkIcon(l.platform, true) + "\n</span>\n")
		if t := phpString(it["contact_tooltip"]); t != "" {
			links.WriteString("<span class=\"e-contact-buttons__contact-tooltip\">" + escHTML(t) + "</span>\n")
		}
		links.WriteString("</a>\n")
	}
	links.WriteString("</div>\n")
	// send button
	cls := "e-contact-buttons__cta-button"
	if v := f.s("style_send_hover_animation"); v != "" {
		cls += " elementor-animation-" + v
	}
	text := f.s("send_button_text")
	send := f.w.attr("send-button")
	send.add("aria-label", text).add("class", cls).add("href", escURL(phpString(sub(f.w.get("send_button_url"), "url")))).
		add("rel", "noopener noreferrer").add("target", "_blank")
	links.WriteString("<a " + send.String() + ">\n" + escHTML(text) + "\n</a>\n")
	inner := f.contentBlock("e-contact-buttons__content", top.String()+"<div class=\"e-contact-buttons__links-container\">\n"+links.String()+"</div>\n")
	// chat button
	btn := f.w.attr("button-")
	btn.add("class", f.chatButtonClasses("e-contact-buttons__chat-button e-contact-buttons__chat-button-shadow", true, false)).
		add("aria-controls", "e-contact-buttons__content-wrapper").add("type", "button").add("aria-label", "Toggle "+f.s("chat_aria_label")).
		add("aria-expanded", "true")
	chat := "<div class=\"e-contact-buttons__chat-button-container\">\n<button " + btn.String() + ">\n" + f.customIcon() + "\n</button>\n</div>\n"
	return f.wrapContent("", inner, chat)
}

// chatButtonWithClose is the chat button of var-4 and var-8: open button plus a close button.
func (f *proFFloat) chatButtonWithClose(label string) string {
	btn := f.w.attr("button-")
	btn.add("class", f.chatButtonClasses("e-contact-buttons__chat-button e-contact-buttons__chat-button-shadow", true, false)).
		add("aria-controls", "e-contact-buttons__content-wrapper").add("aria-label", label+" "+f.s("chat_aria_label")).add("type", "button").
		add("aria-expanded", "true")
	cls := "e-contact-buttons__close-button e-contact-buttons__chat-button-shadow"
	if v := f.s("style_chat_button_size"); v != "" {
		cls += " has-size-" + v
	}
	close := f.w.attr("close-button")
	close.add("class", cls).add("aria-controls", "e-contact-buttons__content-wrapper").add("aria-label", "Close "+f.s("chat_aria_label")).
		add("type", "button").add("aria-expanded", "false")
	return "<div class=\"e-contact-buttons__chat-button-container\">\n<button " + btn.String() + ">\n" + f.customIcon() + "\n</button>\n" +
		"<button " + close.String() + ">\n<i class=\"eicon-close\"></i>\n</button>\n</div>\n"
}

func (f *proFFloat) renderVar4() string {
	c := "e-contact-buttons e-" + f.name
	if f.hasAnimations() {
		c += " has-animations"
	}
	c += f.alignClasses() + f.customClasses()
	f.layoutAttr(c)
	size := f.s("style_chat_button_size")
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__contact\">\n<div class=\"e-contact-buttons__contact-links\">\n")
	for i, it := range f.items() {
		l := proFItemLink(it)
		key := "icon-link-" + strconv.Itoa(i)
		f.itemAnchor(key, l, "e-contact-buttons__contact-icon-link has-size-"+size, l.platform, false)
		b.WriteString("<a " + f.w.attrString(key) + ">\n")
		if t := phpString(it["contact_tooltip"]); t != "" {
			b.WriteString("<span class=\"e-contact-buttons__contact-tooltip e-contact-buttons__contact-box-shadow\">" + escHTML(t) + "</span>\n")
		}
		b.WriteString("<span class=\"e-contact-buttons__contact-icon-container e-contact-buttons__contact-box-shadow\">\n" + proFNetworkIcon(l.platform, true) + "\n</span>\n</a>\n")
	}
	b.WriteString("</div>\n</div>\n")
	inner := f.contentBlock(f.contentClass(true), b.String())
	return f.wrapContent("", inner, f.chatButtonWithClose("Expand"))
}

func (f *proFFloat) platformClass() string {
	if p := f.s("chat_button_platform"); p != "" {
		return " has-platform-" + proFNetworks[p].name
	}
	return ""
}

func (f *proFFloat) renderVar5() string {
	c := "e-contact-buttons e-" + f.name + f.alignClasses()
	if f.hasAnimations() {
		c += " has-animations"
	}
	c += f.customClasses() + f.platformClass()
	f.layoutAttr(c)
	platform := f.s("chat_button_platform")
	aria := "Open " + f.s("chat_aria_label") + " " + proFNetworks[platform].text
	cls := "e-contact-buttons__chat-button e-contact-buttons__chat-button-shadow"
	if v := f.s("style_chat_button_size"); v != "" {
		cls += " has-size-" + v
	}
	if v := f.s("style_button_color_hover_animation"); v != "" {
		cls += " elementor-animation-" + v
	}
	if f.s("chat_button_show_dot") == "yes" {
		cls += " has-dot"
	}
	l := f.chatLink()
	l.url = nil
	a := f.w.attr("formatted-cta")
	if proFIsURLLink(platform) {
		f.linkAttributes(l, "formatted-cta")
		a.add("class", cls).add("aria-label", aria)
	} else {
		a.add("class", cls).add("href", l.formatted()).add("rel", "noopener noreferrer").add("target", "_blank").add("aria-label", aria)
	}
	chat := "<div class=\"e-contact-buttons__chat-button-container\">\n<a " + a.String() + ">\n" + proFNetworkIcon(platform, true) + "\n</a>\n</div>\n"
	return f.wrapContent("", "", chat)
}

func (f *proFFloat) renderVar6() string {
	f.layoutAttr("e-contact-buttons e-" + f.name + f.alignClasses() + f.customClasses())
	size := f.s("style_contact_button_size")
	if f.w.get("style_contact_button_size") == nil {
		size = "small"
	}
	cls := "e-contact-buttons__contact-links"
	if v := f.s("style_contact_button_bar_corners"); v != "" {
		cls += " has-corners-" + v
	}
	var b strings.Builder
	b.WriteString("<div class=\"" + escAttr(cls) + "\">\n")
	for i, it := range f.items() {
		l := proFItemLink(it)
		l.url = nil
		aria := "Access " + f.s("contact_aria_label") + " " + proFNetworks[l.platform].text
		ic := "e-contact-buttons__contact-icon-link has-size-" + size
		if v := f.s("style_contact_button_hover_animation"); v != "" {
			ic += " elementor-animation-" + v
		}
		key := "icon-link-" + strconv.Itoa(i)
		f.itemAnchor(key, l, ic, aria, true)
		b.WriteString("<a " + f.w.attrString(key) + ">\n" + proFNetworkIcon(l.platform, true) + "\n</a>\n")
	}
	b.WriteString("</div>\n")
	return f.wrapContent("", b.String(), "")
}

func (f *proFFloat) renderVar7() string {
	c := "e-contact-buttons e-" + f.name + f.alignClasses()
	if f.w.has("advanced_mobile_full_width") {
		c += " has-mobile-full-width"
	}
	c += f.customClasses() + f.platformClass()
	f.layoutAttr(c)
	platform := f.s("chat_button_platform")
	var text, aria string
	if f.s("chat_button_display_text_select") == "cta" {
		text = f.s("chat_button_display_text")
		aria = text
	} else {
		switch platform {
		case "Email":
			text = f.s("chat_button_mail")
		case "SMS", "WhatsApp", "VIBER", "Telephone":
			text = f.s("chat_button_number")
		case "Messenger", "Skype":
			text = f.s("chat_button_username")
		case "Waze":
			text = phpString(sub(f.w.get("chat_button_waze"), "url"))
		case "Url":
			text = phpString(sub(f.w.get("chat_button_url"), "url"))
		}
		aria = "Open " + f.s("chat_aria_label") + " " + proFNetworks[platform].text
	}
	cls := "e-contact-buttons__chat-button"
	if v := f.s("style_button_color_hover_animation"); v != "" {
		cls += " elementor-animation-" + v
	}
	l := f.chatLink()
	a := f.w.attr("formatted-cta")
	if proFIsURLLink(platform) {
		f.linkAttributes(l, "formatted-cta")
		a.add("class", cls).add("aria-label", aria)
	} else {
		a.add("class", cls).add("href", l.formatted()).add("rel", "noopener noreferrer").add("target", "_blank").add("aria-label", aria)
	}
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__chat-button-container\">\n<a " + a.String() + ">\n")
	if text != "" {
		b.WriteString("<span class=\"e-contact-buttons__chat-button-text\">\n" + escHTML(text) + "\n</span>\n")
	}
	b.WriteString(proFNetworkIcon(platform, true) + "\n</a>\n</div>\n")
	return f.wrapContent("", "", b.String())
}

func (f *proFFloat) renderVar8() string {
	c := "e-contact-buttons e-" + f.name
	if v := f.s("style_chat_box_corners"); v != "" {
		c += " has-corners-" + v
	}
	c += f.alignClasses()
	if f.hasAnimations() {
		c += " has-animations"
	}
	c += f.customClasses()
	f.layoutAttr(c)
	var top strings.Builder
	top.WriteString("<div class=\"e-contact-buttons__top-bar\">\n<div class=\"e-contact-buttons__top-bar-details\">\n")
	if t := f.s("top_bar_title"); t != "" {
		top.WriteString("<p class=\"e-contact-buttons__top-bar-title\">" + escHTML(t) + "</p>\n")
	}
	if t := f.s("top_bar_subtitle"); t != "" {
		top.WriteString("<p class=\"e-contact-buttons__top-bar-subtitle\">" + escHTML(t) + "</p>\n")
	}
	top.WriteString("</div>\n</div>\n")
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__links-container\">\n<div class=\"e-contact-buttons__contact-links\">\n")
	for i, it := range f.items() {
		l := proFItemLink(it)
		cls := "e-contact-buttons__contact-icon-link"
		if v := f.s("style_resource_links_button_size"); v != "" {
			cls += " has-size-" + v
		}
		if n := proFNetworks[l.platform].name; n != "" {
			cls += " has-platform-" + n
		}
		if v := f.s("style_resource_links_color_select"); v != "" {
			cls += " has-colors-type-" + v
		}
		if v := f.s("style_resource_links_hover_animation"); v != "" {
			cls += " elementor-animation-" + v
		}
		key := "icon-link-" + strconv.Itoa(i)
		f.itemAnchor(key, l, cls, l.platform, false)
		b.WriteString("<a " + f.w.attrString(key) + ">\n<span class=\"e-contact-buttons__contact-icon-container\">\n" + proFNetworkIcon(l.platform, true) + "\n</span>\n")
		if t := phpString(it["contact_title"]); t != "" {
			b.WriteString("<span class=\"e-contact-buttons__contact-title\">" + escHTML(t) + "</span>\n")
		}
		if t := phpString(it["contact_description"]); t != "" {
			b.WriteString("<span class=\"e-contact-buttons__contact-description\">" + escHTML(t) + "</span>\n")
		}
		b.WriteString("</a>\n")
	}
	b.WriteString("</div>\n</div>\n")
	inner := f.contentBlock("e-contact-buttons__content", top.String()+b.String())
	return f.wrapContent("", inner, f.chatButtonWithClose("Open"))
}

func (f *proFFloat) renderVar9() string {
	c := "e-contact-buttons e-" + f.name + f.alignClasses()
	if f.hasAnimations() {
		c += " has-animations"
	}
	c += f.customClasses() + f.platformClass()
	f.layoutAttr(c)
	platform := f.s("chat_button_platform")
	cls := "e-contact-buttons__chat-button e-contact-buttons__chat-button-drop-shadow"
	if v := f.s("style_chat_button_size"); v != "" {
		cls += " has-size-" + v
	}
	l := f.chatLink()
	a := f.w.attr("formatted-cta")
	if proFIsURLLink(platform) {
		f.linkAttributes(l, "formatted-cta")
		a.add("class", cls)
	} else {
		a.add("class", cls).add("href", l.formatted()).add("rel", "noopener noreferrer").add("target", "_blank")
	}
	chat := "<div class=\"e-contact-buttons__chat-button-container\">\n<a " + a.String() + ">\n<span class=\"e-contact-buttons__chat-button-icon-container\">" +
		proFNetworkIcon(platform, true) + "</span>\n<span class=\"e-contact-buttons__chat-button-text\">" + escHTML(f.s("chat_button_display_text")) + "</span>\n</a>\n</div>\n"
	return f.wrapContent("", "", chat)
}

func (f *proFFloat) renderVar10() string {
	f.layoutAttr("e-contact-buttons e-" + f.name + f.alignClasses() + f.customClasses())
	var b strings.Builder
	b.WriteString("<div class=\"e-contact-buttons__links-container\">\n<div class=\"e-contact-buttons__contact-links\">\n")
	for i, it := range f.items() {
		l := proFItemLink(it)
		cls := "e-contact-buttons__contact-icon-link e-contact-buttons__contact-box-shadow"
		if v := f.s("style_contact_button_size"); v != "" {
			cls += " has-size-" + v
		}
		if v := f.s("style_contact_corners"); v != "" {
			cls += " has-corners-" + v
		}
		key := "icon-link-" + strconv.Itoa(i)
		f.itemAnchor(key, l, cls, l.platform, false)
		b.WriteString("<a " + f.w.attrString(key) + ">\n")
		if t := phpString(it["contact_title"]); t != "" {
			b.WriteString("<span class=\"e-contact-buttons__contact-title\">" + escHTML(t) + "</span>\n")
		}
		b.WriteString("<span class=\"e-contact-buttons__contact-icon-container\">\n" + proFNetworkIcon(l.platform, true) + "\n</span>\n</a>\n")
	}
	b.WriteString("</div>\n</div>\n")
	return f.wrapContent("", b.String(), "")
}

// --- floating bars ---------------------------------------------------------------------------------------------

func (f *proFFloat) barsLayout() {
	c := "e-floating-bars e-" + f.name + " has-vertical-position-" + f.s("advanced_vertical_position")
	if f.s("floating_bar_close_switch") == "yes" {
		c += " has-close-button"
	}
	if f.s("advanced_toggle_sticky") == "yes" {
		c += " is-sticky"
	}
	f.w.attr("layout").add("class", c).add("id", f.s("advanced_custom_css_id")).add("data-document-id", f.documentID()).add("role", "alertdialog")
}

func (f *proFFloat) iconSpan(key, class string) string {
	icon := asMap(f.w.get(key))
	if icon == nil || phpString(icon["value"]) == "" {
		return ""
	}
	return "<span class=\"" + class + "\">" + renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i") + "</span>\n"
}

func (f *proFFloat) announcementText() string {
	text := f.s("announcement_text")
	a := f.w.attr("announcement_text")
	a.add("class", "e-floating-bars__announcement-text")
	if text == "" {
		return ""
	}
	return "<p " + a.String() + ">\n" + escHTML(text) + "\n</p>\n"
}

func (f *proFFloat) ctaButton() string {
	text := f.s("cta_text")
	cls := "e-floating-bars__cta-button"
	if v := f.s("style_cta_button_hover_animation"); v != "" {
		cls += " elementor-animation-" + v
	}
	if v := f.s("style_cta_button_corners"); v != "" {
		cls += " has-corners-" + v
	}
	if v := f.s("style_cta_type"); v != "" {
		cls += " is-type-" + v
	}
	if v := f.s("style_cta_button_animation"); v != "" && v != "none" {
		cls += " has-entrance-animation"
	}
	if f.s("style_cta_button_show_border") == "yes" {
		cls += " has-border"
	}
	f.w.attr("cta-button").add("class", cls)
	f.w.attr("cta_text").add("class", "e-floating-bars__cta-text")
	if text == "" {
		return ""
	}
	f.w.addLinkAttributes("cta-button", f.w.get("cta_link"), false)
	icon := ""
	if ic := asMap(f.w.get("cta_icon")); ic != nil && phpString(ic["value"]) != "" {
		icon = "<span class=\"e-floating-bars__cta-icon\">" + renderIcon(ic, newAttrs().add("aria-hidden", "true"), "i") + "</span>\n"
	}
	return "<div class=\"e-floating-bars__cta-button-container\">\n<a " + f.w.attrString("cta-button") + ">\n" + icon +
		"<span " + f.w.attrString("cta_text") + ">" + escHTML(text) + "</span>\n</a>\n</div>\n"
}

func (f *proFFloat) barsCloseButton() string {
	a := f.w.attr("close-button")
	a.add("class", "e-floating-bars__close-button").add("aria-label", "Close "+f.s("accessible_name")).add("type", "button").add("aria-controls", "e-floating-bars")
	return "<button " + a.String() + ">\n<i class=\"eicon-close\"></i>\n</button>\n"
}

func (f *proFFloat) renderBarsVar1() string {
	f.w.addInlineEditing("announcement_text", "none")
	f.w.addInlineEditing("cta_text", "none")
	f.barsLayout()
	var b strings.Builder
	b.WriteString("<div " + f.w.attrString("layout") + ">\n")
	b.WriteString(f.announcementText())
	b.WriteString(f.iconSpan("announcement_icon", "e-floating-bars__announcement-icon"))
	b.WriteString(f.ctaButton())
	if f.s("floating_bar_close_switch") == "yes" {
		b.WriteString(f.barsCloseButton())
	}
	b.WriteString("<div class=\"e-floating-bars__overlay\"></div>\n</div>\n")
	return b.String()
}

func (f *proFFloat) renderBarsVar2() string {
	f.barsLayout()
	var b strings.Builder
	b.WriteString("<div " + f.w.attrString("layout") + ">\n")
	cls := "e-floating-bars__headlines"
	if f.s("style_ticker_pause_hover") == "yes" {
		cls += " has-pause-hover"
	}
	b.WriteString("<div class=\"" + cls + "\">\n<div class=\"e-floating-bars__headlines-inner\">\n")
	for i, it := range asList(f.w.get("headlines_repeater")) {
		h := asMap(it)
		if h == nil {
			continue
		}
		key := "headline-" + strconv.Itoa(i)
		f.w.attr(key).add("class", "e-floating-bars__headline")
		f.w.addLinkAttributes(key, h["headlines_url"], false)
		text := phpString(h["headlines_text"])
		if text == "" {
			continue
		}
		b.WriteString("<a " + f.w.attrString(key) + ">\n")
		if ic := asMap(h["headlines_icon"]); ic != nil && truthy(ic["value"]) {
			b.WriteString("<span class=\"e-floating-bars__headline-icon\">" + renderIcon(ic, newAttrs().add("aria-hidden", "true"), "i") + "</span>\n")
		}
		b.WriteString("<span class=\"e-floating-bars__headline-text\">" + escHTML(text) + "</span>\n</a>\n")
	}
	b.WriteString("</div>\n<div class=\"e-floating-bars__overlay\"></div>\n</div>\n")
	pause, closeBtn := f.s("floating_bar_pause_switch") == "yes", f.s("floating_bar_close_switch") == "yes"
	if pause || closeBtn {
		b.WriteString("<div class=\"e-floating-bars__ticker-controls\">\n")
		if pause {
			name := f.s("accessible_name")
			hidden := func() *attrs { return newAttrs().add("aria-hidden", "true") }
			b.WriteString("<div class=\"e-floating-bars__play-pause-controls\">\n")
			b.WriteString("<button class=\"e-floating-bars__pause-button\" aria-label=\"" + escAttr("Pause "+name) + "\">" + renderIcon(f.w.get("floating_bar_pause_icon"), hidden(), "i") + "</button>\n")
			b.WriteString("<button class=\"e-floating-bars__play-button\" aria-label=\"" + escAttr("Play "+name) + "\">" + renderIcon(f.w.get("floating_bar_play_icon"), hidden(), "i") + "</button>\n")
			b.WriteString("</div>\n")
		}
		if closeBtn {
			b.WriteString(f.barsCloseButton())
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("</div>\n")
	return b.String()
}

var proFCouponShapes = map[string]string{
	"price-tag":     `<svg class="e-floating-bars__shape e-floating-bars__price-tag" fill="none" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 130.67 42"><path class="shape path" d="M124.67 1H21.37c-1.3 0-2.56.51-3.49 1.42l-15.37 15a5.005 5.005 0 0 0 0 7.16l15.38 15c.93.91 2.19 1.42 3.49 1.42h103.29c2.76 0 5-2.24 5-5V6c0-2.76-2.24-5-5-5ZM15.34 25c-2.21 0-4-1.79-4-4s1.79-4 4-4 4 1.79 4 4-1.79 4-4 4Z"/></svg>`,
	"gift-tag":      "<svg class=\"e-floating-bars__shape e-floating-bars__gift-tag\" viewBox=\"0 0 111 40\" fill=\"none\" xmlns=\"http://www.w3.org/2000/svg\">\n<path d=\"M1 20C1 9.50229 9.50229 1 20 1H110V39H20C9.50229 39 1 30.4977 1 20ZM11 20C11 22.7623 13.2377 25 16 25C18.7623 25 21 22.7623 21 20C21 17.2377 18.7623 15 16 15C13.2377 15 11 17.2377 11 20Z\" class=\"shape path\"/>\n</svg>",
	"round-ticket":  `<svg class="e-floating-bars__shape e-floating-bars__round-ticket" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 104 40"><path class="shape path" d="M96.5 8.51v.522l.428.298A12.972 12.972 0 0 1 102.5 20c0 4.42-2.199 8.319-5.572 10.67l-.428.298V39h-89v-8.032l-.428-.298A12.972 12.972 0 0 1 1.5 20c0-4.42 2.199-8.319 5.572-10.67l.428-.298V1h89v7.51Z"/></svg>`,
	"square-ticket": `<svg class="e-floating-bars__shape e-floating-bars__square-ticket" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 106 40"><path class="shape path" d="M7.429 1H99.57a7.003 7.003 0 0 0 4.929 5.712v26.576A7.015 7.015 0 0 0 99.57 39H7.43A7.011 7.011 0 0 0 1.5 33.071V6.93A7.012 7.012 0 0 0 7.429 1Z"/></svg>`,
}

func (f *proFFloat) renderBarsVar3() string {
	f.w.addInlineEditing("announcement_text", "none")
	f.barsLayout()
	var b strings.Builder
	b.WriteString("<div " + f.w.attrString("layout") + ">\n")
	b.WriteString(f.announcementText())
	b.WriteString(f.iconSpan("announcement_icon", "e-floating-bars__announcement-icon"))
	// coupon
	text := f.s("coupon_code")
	typ := f.s("style_coupon_type")
	cls := "e-floating-bars__coupon-button"
	if v := f.s("style_coupon_animation"); v != "" && v != "none" {
		cls += " has-entrance-animation"
	}
	if typ != "" {
		cls += " is-type-" + typ
	}
	if f.s("style_coupon_show_border") == "yes" {
		cls += " has-border"
	}
	a := f.w.attr("coupon-button")
	a.add("class", cls).add("type", "button").add("aria-label", "Copy "+f.s("accessible_name")+" coupon code")
	if text != "" {
		hidden := func() *attrs { return newAttrs().add("aria-hidden", "true") }
		b.WriteString("<button " + a.String() + ">\n" + proFCouponShapes[typ] + "\n")
		b.WriteString("<span class=\"e-floating-bars__coupon-text-group e-floating-bars__coupon-code\">\n")
		if ic := asMap(f.w.get("coupon_copy_icon")); ic != nil && phpString(ic["value"]) != "" {
			b.WriteString("<span class=\"e-floating-bars__coupon-icon\">" + renderIcon(ic, hidden(), "i") + "</span>\n")
		}
		b.WriteString("<span class=\"e-floating-bars__coupon-text\">" + escHTML(text) + "</span>\n</span>\n")
		b.WriteString("<span class=\"e-floating-bars__coupon-text-group e-floating-bars__coupon-success is-hidden\">\n")
		if ic := asMap(f.w.get("coupon_success_icon")); ic != nil && phpString(ic["value"]) != "" {
			b.WriteString("<span class=\"e-floating-bars__coupon-icon e-floating-bars__coupon-success-icon\">" + renderIcon(ic, hidden(), "i") + "</span>\n")
		}
		b.WriteString("<span class=\"e-floating-bars__coupon-text e-floating-bars__coupon-success-message\">" + escHTML(f.s("coupon_success_message")) + "</span>\n</span>\n</button>\n")
	}
	if f.s("floating_bar_close_switch") == "yes" {
		b.WriteString(f.barsCloseButton())
	}
	b.WriteString("<div class=\"e-floating-bars__overlay\"></div>\n</div>\n")
	return b.String()
}
