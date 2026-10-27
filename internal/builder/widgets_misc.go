package builder

// Misc widgets: video, google_maps, counter, progress, alert, spacer, divider, html, shortcode,
// menu-anchor, read-more (ports of includes/widgets/*.php render()).

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	registerWidget("video", &widgetDef{render: renderVideo, styles: static("widget-video")})
	registerWidget("google_maps", &widgetDef{render: renderGoogleMaps, styles: static("widget-google_maps")})
	registerWidget("counter", &widgetDef{render: renderCounter, styles: static("widget-counter"), scripts: []string{"jquery-numerator"}})
	registerWidget("progress", &widgetDef{render: renderProgress, styles: static("widget-progress")})
	registerWidget("alert", &widgetDef{render: renderAlert, styles: static("widget-alert")})
	registerWidget("spacer", &widgetDef{render: renderSpacer, styles: static("widget-spacer")})
	registerWidget("divider", &widgetDef{render: renderDivider, styles: static("widget-divider")})
	registerWidget("html", &widgetDef{render: renderHTMLWidget})
	registerWidget("shortcode", &widgetDef{render: renderShortcodeWidget})
	registerWidget("menu-anchor", &widgetDef{render: renderMenuAnchor, styles: static("widget-menu-anchor")})
	registerWidget("read-more", &widgetDef{render: renderReadMore})
}

// miscIsEmpty mirrors Utils::is_empty for a scalar.
func miscIsEmpty(v any) bool { return !strictEq(v, "0") && isEmpty(v) }

// ---- video ----

func videoHasOverlay(w *elemCtx) bool {
	return truthy(sub(w.get("image_overlay"), "url")) && w.s("show_image_overlay") == "yes"
}

func videoEmbedParams(w *elemCtx) *omap {
	p := newOmap()
	if w.has("autoplay") && !videoHasOverlay(w) {
		p.Set("autoplay", "1")
		if w.has("play_on_mobile") {
			p.Set("playsinline", "1")
		}
	}
	type dictEntry struct{ setting, param string }
	var dict []dictEntry
	plain := func(names ...string) {
		for _, n := range names {
			dict = append(dict, dictEntry{n, n})
		}
	}
	switch w.s("video_type") {
	case "youtube":
		plain("loop", "controls", "mute", "rel", "cc_load_policy")
		if w.has("loop") {
			_, id, _ := videoProperties(w.s("youtube_url"))
			p.Set("playlist", id)
		}
		p.Set("start", w.get("start"))
		p.Set("end", w.get("end"))
		p.Set("wmode", "opaque")
	case "vimeo":
		plain("loop")
		dict = append(dict, dictEntry{"mute", "muted"}, dictEntry{"vimeo_title", "title"}, dictEntry{"vimeo_portrait", "portrait"}, dictEntry{"vimeo_byline", "byline"})
		p.Set("color", strings.ReplaceAll(phpString(w.get("color")), "#", ""))
		p.Set("autopause", "0")
		if w.has("yt_privacy") {
			p.Set("dnt", "true")
		}
	case "dailymotion":
		plain("controls", "mute")
		dict = append(dict, dictEntry{"showinfo", "ui-start-screen-info"}, dictEntry{"logo", "ui-logo"})
		p.Set("ui-highlight", strings.ReplaceAll(phpString(w.get("color")), "#", ""))
		p.Set("start", w.get("start"))
		p.Set("endscreen-enable", "0")
	case "videopress":
		plain("controls")
		dict = append(dict, dictEntry{"autoplay", "autoPlay"}, dictEntry{"mute", "muted"})
		plain("loop")
		dict = append(dict, dictEntry{"play_on_mobile", "playsinline"})
		p.Set("at", w.get("start"))
	}
	for _, d := range dict {
		v := "0"
		if w.has(d.setting) {
			v = "1"
		}
		p.Set(d.param, v)
	}
	return p
}

func videoHostedURL(w *elemCtx) string {
	var u string
	if w.has("insert_url") {
		u = phpString(sub(w.get("external_url"), "url"))
	} else {
		u = phpString(sub(w.get("hosted_url"), "url"))
	}
	if u == "" {
		return ""
	}
	if w.has("start") || w.has("end") {
		u += "#t="
	}
	if w.has("start") {
		u += w.s("start")
	}
	if w.has("end") {
		u += "," + w.s("end")
	}
	return u
}

func videoHostedParams(w *elemCtx) *omap {
	p := newOmap()
	for _, o := range []string{"autoplay", "loop", "controls"} {
		if w.has(o) {
			p.Set(o, "")
		}
	}
	if w.has("preload") {
		p.Set("preload", w.s("preload"))
	}
	if w.has("mute") {
		p.Set("muted", "muted")
	}
	if w.has("play_on_mobile") {
		p.Set("playsinline", "")
	}
	if !w.has("download_button") {
		p.Set("controlsList", "nodownload")
	}
	if truthy(sub(w.get("poster"), "url")) {
		p.Set("poster", phpString(sub(w.get("poster"), "url")))
	}
	return p
}

func renderVideo(w *elemCtx) string {
	vt := w.s("video_type")
	videoURL := w.s(vt + "_url")
	var params *omap
	privacy, lazy := false, false
	start := 0
	if vt == "hosted" {
		videoURL = videoHostedURL(w)
	} else {
		if vt == "videopress" {
			if w.has("insert_url") {
				videoURL = w.s("videopress_url")
			} else {
				videoURL = phpString(sub(w.get("hosted_url"), "url"))
			}
		}
		params = videoEmbedParams(w)
		if vt == "youtube" {
			privacy = w.has("yt_privacy")
		} else if vt == "vimeo" {
			start = toInt(w.get("start"))
		}
		lazy = w.has("lazy_load")
	}
	if videoURL == "" {
		return ""
	}
	videoHTML := ""
	if vt == "youtube" {
		videoHTML = `<div class="elementor-video"></div>`
	}
	if vt == "hosted" {
		w.attr("video-wrapper").add("class", "e-hosted-video")
		hp := videoHostedParams(w)
		var ab strings.Builder
		for i, k := range hp.keys {
			if i > 0 {
				ab.WriteString(" ")
			}
			ab.WriteString(k + `="` + escAttr(phpString(hp.m[k])) + `"`)
		}
		videoHTML = "\t\t<video class=\"elementor-video\" src=\"" + escAttr(videoURL) + "\" " + ab.String() + "></video>\n\t\t"
	} else if vt != "youtube" {
		videoHTML = embedHTML(videoURL, params, privacy, start, lazy, nil)
	}
	if videoHTML == "" {
		return escURL(videoURL)
	}
	lightbox := w.has("lightbox")
	vw := w.attr("video-wrapper")
	vw.add("class", "elementor-wrapper")
	if lightbox {
		vw.add("class", "elementor-open-lightbox")
	} else {
		vw.add("class", "elementor-open-inline")
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + vw.String() + ">\n\t\t\t")
	if !lightbox {
		b.WriteString(videoHTML)
	}
	if videoHasOverlay(w) {
		ov := w.attr("image-overlay")
		ov.add("class", "elementor-custom-embed-image-overlay")
		overlay := asMap(w.get("image_overlay"))
		if lightbox {
			var lbURL string
			if vt == "hosted" {
				lbURL = videoURL
			} else {
				lbURL, _ = embedURL(videoURL, videoEmbedParams(w), privacy, start)
			}
			mo := newOmap()
			mo.Set("id", "elementor-lightbox-"+w.el.ID)
			mo.Set("entranceAnimation", w.get("lightbox_content_animation"))
			mo.Set("entranceAnimation_tablet", w.get("lightbox_content_animation_tablet"))
			mo.Set("entranceAnimation_mobile", w.get("lightbox_content_animation_mobile"))
			if ar, ok := w.disp.m["aspect_ratio"]; ok && ar != nil {
				mo.Set("videoAspectRatio", ar)
			} else {
				mo.Set("videoAspectRatio", "169")
			}
			lo := newOmap()
			lo.Set("type", "video")
			lo.Set("videoType", vt)
			lo.Set("url", lbURL)
			lo.Set("autoplay", w.get("autoplay"))
			lo.Set("modalOptions", mo)
			if vt == "hosted" {
				lo.Set("videoParams", videoHostedParams(w))
			}
			ov.add("data-elementor-open-lightbox", "yes")
			ov.add("data-elementor-lightbox", phpJSON(lo))
			ov.add("data-e-action-hash", createActionHash("lightbox", lo))
			if w.editing() {
				ov.add("class", "elementor-clickable")
			}
		} else {
			var imageURL string
			if !truthy(overlay["id"]) && truthy(overlay["url"]) {
				imageURL = phpString(overlay["url"])
			} else {
				imageURL = w.r.attachmentImageSrc(int64(toInt(overlay["id"])), "image_overlay", w.disp.m)
			}
			ov.add("style", "background-image: url("+imageURL+");")
		}
		b.WriteString("\t\t\t\t<div " + ov.String() + ">\n")
		if lightbox {
			b.WriteString("\t\t\t\t\t\t" + w.r.attachmentImageHTML(w.disp.m, "image_overlay", "") + "\t\t\t\t\t\t\t\t")
		}
		if w.s("show_play_icon") == "yes" {
			label := "Play Video"
			if alt := phpString(overlay["alt"]); truthy(overlay["alt"]) {
				label = "Play Video about " + escAttr(alt)
			}
			icon := w.get("play_icon")
			if !truthy(sub(icon, "value")) {
				icon = map[string]any{"library": "eicons", "value": "eicon-play"}
			}
			b.WriteString("\t\t\t\t\t\t<div class=\"elementor-custom-embed-play\" role=\"button\" aria-label=\"" + label + "\" tabindex=\"0\">\n\t\t\t\t\t\t\t")
			b.WriteString(renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i"))
			b.WriteString("\t\t\t\t\t\t</div>\n")
		}
		b.WriteString("\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t</div>\n\t\t")
	return b.String()
}

// ---- google maps ----

func renderGoogleMaps(w *elemCtx) string {
	if !w.has("address") {
		return ""
	}
	zoom := int(math.Abs(float64(toInt(sub(w.get("zoom"), "size")))))
	if zoom == 0 {
		zoom = 10
	}
	addr := w.s("address")
	u := "https://maps.google.com/maps?q=" + rawURLEncode(addr) + "&amp;t=m&amp;z=" + strconv.Itoa(zoom) + "&amp;output=embed&amp;iwloc=near"
	if GoogleMapsAPIKey != "" {
		u = "https://www.google.com/maps/embed/v1/place?key=" + escHTML(GoogleMapsAPIKey) + "&q=" + rawURLEncode(addr) + "&amp;zoom=" + strconv.Itoa(zoom)
	}
	return "\t\t<div class=\"elementor-custom-embed\">\n\t\t\t<iframe loading=\"lazy\"\n\t\t\t\t\tsrc=\"" + escURL(u) + "\"\n\t\t\t\t\ttitle=\"" + escAttr(addr) + "\"\n\t\t\t\t\taria-label=\"" + escAttr(addr) + "\"\n\t\t\t></iframe>\n\t\t</div>\n\t\t"
}

// GoogleMapsAPIKey mirrors the elementor_google_maps_api_key option (empty = keyless embed).
var GoogleMapsAPIKey string

// ---- counter ----

func renderCounter(w *elemCtx) string {
	w.attr("elementor-counter").add("class", "elementor-counter")
	w.attr("counter-number").add("class", "elementor-counter-number-wrapper")
	c := w.attr("counter")
	c.add("class", "elementor-counter-number")
	c.add("data-duration", phpString(w.get("duration")))
	c.add("data-to-value", phpString(w.get("ending_number")))
	c.add("data-from-value", phpString(w.get("starting_number")))
	if w.has("thousand_separator") {
		d := w.s("thousand_separator_char")
		if !w.has("thousand_separator_char") {
			d = ","
		}
		c.add("data-delimiter", d)
	}
	w.attr("prefix").add("class", "elementor-counter-number-prefix")
	w.attr("suffix").add("class", "elementor-counter-number-suffix")
	w.attr("counter-title").add("class", "elementor-counter-title")
	w.addInlineEditing("counter-title", "")
	tag := validateHTMLTag(w.s("title_tag"))
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString("elementor-counter") + ">\n\t\t\t")
	if w.has("title") {
		b.WriteString("<" + tag + " " + w.attrString("counter-title") + ">" + ksesPost(w.s("title")) + "</" + tag + ">")
	}
	b.WriteString("\t\t\t<div " + w.attrString("counter-number") + ">\n")
	b.WriteString("\t\t\t\t<span " + w.attrString("prefix") + ">" + ksesPost(w.s("prefix")) + "</span>\n")
	b.WriteString("\t\t\t\t<span " + w.attrString("counter") + ">" + ksesPost(w.s("starting_number")) + "</span>\n")
	b.WriteString("\t\t\t\t<span " + w.attrString("suffix") + ">" + ksesPost(w.s("suffix")) + "</span>\n")
	b.WriteString("\t\t\t</div>\n\t\t</div>\n\t\t")
	return b.String()
}

// ---- progress ----

func renderProgress(w *elemCtx) string {
	size := sub(w.get("percent"), "size")
	if !w.has("title") && isEmpty(size) {
		return ""
	}
	id := "elementor-progress-bar-" + w.el.ID
	pct := "0"
	if isNumeric(size) {
		pct = phpString(size)
		if toFloat(size) > 100 {
			pct = "100"
		}
	}
	title := w.get("title")
	wr := w.attr("wrapper")
	if !miscIsEmpty(title) {
		if w.s("title_display") == "yes" {
			w.attr("title").add("class", "elementor-title").add("id", id)
			w.addInlineEditing("title", "")
			wr.add("aria-labelledby", id)
		} else {
			wr.add("aria-label", phpString(title))
		}
	}
	wr.add("class", "elementor-progress-wrapper")
	wr.add("role", "progressbar")
	wr.add("aria-valuemin", "0")
	wr.add("aria-valuemax", "100")
	wr.add("aria-valuenow", pct)
	if w.has("inner_text") {
		wr.add("aria-valuetext", pct+"% ("+w.s("inner_text")+")")
	}
	if w.has("progress_type") {
		wr.add("class", "progress-"+w.s("progress_type"))
	}
	w.attr("progress-bar").add("class", "elementor-progress-bar").add("data-max", pct)
	w.attr("inner_text").add("class", "elementor-progress-text")
	w.addInlineEditing("inner_text", "")
	var b strings.Builder
	if !miscIsEmpty(title) && w.s("title_display") == "yes" {
		tag := validateHTMLTag(w.s("title_tag"))
		b.WriteString("\t\t\t<" + tag + " " + w.attrString("title") + ">\n\t\t\t\t" + ksesPost(phpString(title)) + "\n\t\t\t</" + tag + ">\n\t\t")
	}
	b.WriteString("\n\t\t<div " + wr.String() + ">\n")
	b.WriteString("\t\t\t<div " + w.attrString("progress-bar") + ">\n")
	b.WriteString("\t\t\t\t<span " + w.attrString("inner_text") + ">" + ksesPost(w.s("inner_text")) + "</span>\n")
	if w.s("display_percentage") == "show" {
		b.WriteString("\t\t\t\t\t<span class=\"elementor-progress-percentage\">" + pct + "%</span>\n")
	}
	b.WriteString("\t\t\t</div>\n\t\t</div>\n\t\t")
	return b.String()
}

// ---- alert ----

func renderAlert(w *elemCtx) string {
	title, desc := w.get("alert_title"), w.get("alert_description")
	if miscIsEmpty(title) && miscIsEmpty(desc) {
		return ""
	}
	w.attr("alert_wrapper").add("class", "elementor-alert").add("role", "alert")
	w.attr("alert_title").add("class", "elementor-alert-title")
	w.attr("alert_description").add("class", "elementor-alert-description")
	w.addInlineEditing("alert_title", "none")
	w.addInlineEditing("alert_description", "")
	var b strings.Builder
	b.WriteString("\t\t<div " + w.attrString("alert_wrapper") + ">\n\n")
	if !miscIsEmpty(title) {
		b.WriteString("\t\t\t<span " + w.attrString("alert_title") + ">" + ksesPost(phpString(title)) + "</span>\n")
	}
	if !miscIsEmpty(desc) {
		b.WriteString("\t\t\t<span " + w.attrString("alert_description") + ">" + ksesPost(phpString(desc)) + "</span>\n")
	}
	if w.s("show_dismiss") == "show" {
		b.WriteString("\t\t\t<button type=\"button\" class=\"elementor-alert-dismiss\" aria-label=\"Dismiss this alert.\">\n\t\t\t\t")
		if truthy(sub(w.get("dismiss_icon"), "value")) {
			b.WriteString(renderIcon(w.get("dismiss_icon"), newAttrs().add("aria-hidden", "true"), "i"))
		} else {
			b.WriteString("\t\t\t\t\t<span aria-hidden=\"true\">&times;</span>\n\t\t\t\t")
		}
		b.WriteString("\t\t\t</button>\n")
	}
	b.WriteString("\n\t\t</div>\n\t\t")
	return b.String()
}

// ---- spacer ----

func renderSpacer(w *elemCtx) string {
	sp := w.get("space")
	if isEmpty(sp) || isEmpty(sub(sp, "size")) {
		return ""
	}
	return "\t\t<div class=\"elementor-spacer\">\n\t\t\t<div class=\"elementor-spacer-inner\"></div>\n\t\t</div>\n\t\t"
}

// ---- divider ----

type dividerStyle struct {
	shape    string
	preserve bool
	group    string
	viewBox  string
}

var dividerStyles = map[string]dividerStyle{
	"curly":          {`<path d="M0,21c3.3,0,8.3-0.9,15.7-7.1c6.6-5.4,4.4-9.3,2.4-10.3c-3.4-1.8-7.7,1.3-7.3,8.8C11.2,20,17.1,21,24,21"/>`, false, "line", ""},
	"curved":         {`<path d="M0,6c6,0,6,13,12,13S18,6,24,6"/>`, false, "line", ""},
	"multiple":       {`<path d="M24,8v12H0V8H24z M24,4v1H0V4H24z"/>`, false, "pattern", ""},
	"slashes":        {`<g transform="translate(-12.000000, 0)"><path d="M28,0L10,18"/><path d="M18,0L0,18"/><path d="M48,0L30,18"/><path d="M38,0L20,18"/></g>`, false, "line", "0 0 20 16"},
	"squared":        {"<polyline points=\"0,6 6,6 6,18 18,18 18,6 24,6 \t\"/>", false, "line", ""},
	"wavy":           {`<path d="M0,6c6,0,0.9,11.1,6.9,11.1S18,6,24,6"/>`, false, "line", ""},
	"zigzag":         {`<polyline points="0,18 12,6 24,18 "/>`, false, "line", ""},
	"arrows":         {`<path d="M14.2,4c0.3,0,0.5,0.1,0.7,0.3l7.9,7.2c0.2,0.2,0.3,0.4,0.3,0.7s-0.1,0.5-0.3,0.7l-7.9,7.2c-0.2,0.2-0.4,0.3-0.7,0.3s-0.5-0.1-0.7-0.3s-0.3-0.4-0.3-0.7l0-2.9l-11.5,0c-0.4,0-0.7-0.3-0.7-0.7V9.4C1,9,1.3,8.7,1.7,8.7l11.5,0l0-3.6c0-0.3,0.1-0.5,0.3-0.7S13.9,4,14.2,4z"/>`, true, "pattern", ""},
	"pluses":         {`<path d="M21.4,9.6h-7.1V2.6c0-0.9-0.7-1.6-1.6-1.6h-1.6c-0.9,0-1.6,0.7-1.6,1.6v7.1H2.6C1.7,9.6,1,10.3,1,11.2v1.6c0,0.9,0.7,1.6,1.6,1.6h7.1v7.1c0,0.9,0.7,1.6,1.6,1.6h1.6c0.9,0,1.6-0.7,1.6-1.6v-7.1h7.1c0.9,0,1.6-0.7,1.6-1.6v-1.6C23,10.3,22.3,9.6,21.4,9.6z"/>`, true, "pattern", ""},
	"rhombus":        {`<path d="M12.7,2.3c-0.4-0.4-1.1-0.4-1.5,0l-8,9.1c-0.3,0.4-0.3,0.9,0,1.2l8,9.1c0.4,0.4,1.1,0.4,1.5,0l8-9.1c0.3-0.4,0.3-0.9,0-1.2L12.7,2.3z"/>`, false, "pattern", ""},
	"parallelogram":  {`<polygon points="9.4,2 24,2 14.6,21.6 0,21.6"/>`, false, "pattern", ""},
	"rectangles":     {`<rect x="15" y="0" width="30" height="30"/>`, false, "pattern", "0 0 60 30"},
	"dots_tribal":    {`<path d="M3,10.2c2.6,0,2.6,2,2.6,3.2S4.4,16.5,3,16.5s-3-1.4-3-3.2S0.4,10.2,3,10.2z M18.8,10.2c1.7,0,3.2,1.4,3.2,3.2s-1.4,3.2-3.2,3.2c-1.7,0-3.2-1.4-3.2-3.2S17,10.2,18.8,10.2z M34.6,10.2c1.5,0,2.6,1.4,2.6,3.2s-0.5,3.2-1.9,3.2c-1.5,0-3.4-1.4-3.4-3.2S33.1,10.2,34.6,10.2z M50.5,10.2c1.7,0,3.2,1.4,3.2,3.2s-1.4,3.2-3.2,3.2c-1.7,0-3.3-0.9-3.3-2.6S48.7,10.2,50.5,10.2z M66.2,10.2c1.5,0,3.4,1.4,3.4,3.2s-1.9,3.2-3.4,3.2c-1.5,0-2.6-0.4-2.6-2.1S64.8,10.2,66.2,10.2z M82.2,10.2c1.7,0.8,2.6,1.4,2.6,3.2s-0.1,3.2-1.6,3.2c-1.5,0-3.7-1.4-3.7-3.2S80.5,9.4,82.2,10.2zM98.6,10.2c1.5,0,2.6,0.4,2.6,2.1s-1.2,4.2-2.6,4.2c-1.5,0-3.7-0.4-3.7-2.1S97.1,10.2,98.6,10.2z M113.4,10.2c1.2,0,2.2,0.9,2.2,3.2s-0.1,3.2-1.3,3.2s-3.1-1.4-3.1-3.2S112.2,10.2,113.4,10.2z"/>`, true, "tribal", "0 0 126 26"},
	"trees_2_tribal": {`<path d="M111.9,18.3v3.4H109v-3.4H111.9z M90.8,18.3v3.4H88v-3.4H90.8z M69.8,18.3v3.4h-2.9v-3.4H69.8z M48.8,18.3v3.4h-2.9v-3.4H48.8z M27.7,18.3v3.4h-2.9v-3.4H27.7z M6.7,18.3v3.4H3.8v-3.4H6.7z M46.4,4l4.3,4.8l-1.8,0l3.5,4.4l-2.2-0.1l3,3.3l-11,0.4l3.6-3.8l-2.9-0.1l3.1-4.2l-1.9,0L46.4,4z M111.4,4l2.4,4.8l-1.8,0l3.5,4.4l-2.5-0.1l3.3,3.3h-11l3.1-3.4l-2.5-0.1l3.1-4.2l-1.9,0L111.4,4z M89.9,4l2.9,4.8l-1.9,0l3.2,4.2l-2.5,0l3.5,3.5l-11-0.4l3-3.1l-2.4,0L88,8.8l-1.9,0L89.9,4z M68.6,4l3,4.4l-1.9,0.1l3.4,4.1l-2.7,0.1l3.8,3.7H63.8l2.9-3.6l-2.9,0.1L67,8.7l-2,0.1L68.6,4z M26.5,4l3,4.4l-1.9,0.1l3.7,4.7l-2.5-0.1l3.3,3.3H21l3.1-3.4l-2.5-0.1l3.2-4.3l-2,0.1L26.5,4z M4.9,4l3.7,4.8l-1.5,0l3.1,4.2L7.6,13l3.4,3.4H0l3-3.3l-2.3,0.1l3.5-4.4l-2.3,0L4.9,4z"/>`, true, "tribal", "0 0 126 26"},
	"rounds_tribal":  {`<path d="M11.9,15.9L11.9,15.9L0,16c-0.2-3.7,1.5-5.7,4.9-6C10,9.6,12.4,14.2,11.9,15.9zM26.9,15.9L26.9,15.9L15,16c0.5-3.7,2.5-5.7,5.9-6C26,9.6,27.4,14.2,26.9,15.9z M37.1,10c3.4,0.3,5.1,2.3,4.9,6H30.1C29.5,14.4,31.9,9.6,37.1,10z M57,15.9L57,15.9L45,16c0-3.4,1.6-5.4,4.9-5.9C54.8,9.3,57.4,14.2,57,15.9z M71.9,15.9L71.9,15.9L60,16c-0.2-3.7,1.5-5.7,4.9-6C70,9.6,72.4,14.2,71.9,15.9z M82.2,10c3.4,0.3,5,2.3,4.8,6H75.3C74,13,77.1,9.6,82.2,10zM101.9,15.9L101.9,15.9L90,16c-0.2-3.7,1.5-5.7,4.9-6C100,9.6,102.4,14.2,101.9,15.9z M112.1,10.1c2.7,0.5,4.3,2.5,4.9,5.9h-11.9l0,0C104.5,14.4,108,9.3,112.1,10.1z"/>`, true, "tribal", "0 0 120 26"},
	"leaves_tribal":  {`<path d="M3,1.5C5,4.9,6,8.8,6,13s-1.7,8.1-5,11.5C0.3,21.1,0,17.2,0,13S1,4.9,3,1.5z M16,1.5c2,3.4,3,7.3,3,11.5s-1,8.1-3,11.5c-2-4.1-3-8.3-3-12.5S14,4.3,16,1.5z M29,1.5c2,4.8,3,9.3,3,13.5s-1,7.4-3,9.5c-2-3.4-3-7.3-3-11.5S27,4.9,29,1.5z M41.1,1.5C43.7,4.9,45,8.8,45,13s-1,8.1-3,11.5c-2-3.4-3-7.3-3-11.5S39.7,4.9,41.1,1.5zM55,1.5c2,2.8,3,6.3,3,10.5s-1.3,8.4-4,12.5c-1.3-3.4-2-7.3-2-11.5S53,4.9,55,1.5z M68,1.5c2,3.4,3,7.3,3,11.5s-0.7,8.1-2,11.5c-2.7-4.8-4-9.3-4-13.5S66,3.6,68,1.5z M82,1.5c1.3,4.8,2,9.3,2,13.5s-1,7.4-3,9.5c-2-3.4-3-7.3-3-11.5S79.3,4.9,82,1.5z M94,1.5c2,3.4,3,7.3,3,11.5s-1.3,8.1-4,11.5c-1.3-1.4-2-4.3-2-8.5S92,6.9,94,1.5z M107,1.5c2,2.1,3,5.3,3,9.5s-0.7,8.7-2,13.5c-2.7-3.4-4-7.3-4-11.5S105,4.9,107,1.5z"/>`, true, "tribal", "0 0 117 26"},
	"stripes_tribal": {`<path d="M54,1.6V26h-9V2.5L54,1.6z M69,1.6v23.3L60,26V1.6H69z M24,1.6v23.5l-9-0.6V1.6H24z M30,0l9,0.7v24.5h-9V0z M9,2.5v22H0V3.7L9,2.5z M75,1.6l9,0.9v22h-9V1.6z M99,2.7v21.7h-9V3.8L99,2.7z M114,3.8v20.7l-9-0.5V3.8L114,3.8z"/>`, true, "tribal", "0 0 120 26"},
	"squares_tribal": {`<path d="M46.8,7.8v11.5L36,18.6V7.8H46.8z M82.4,7.8L84,18.6l-12,0.7L70.4,7.8H82.4z M0,7.8l12,0.9v9.9H1.3L0,7.8z M30,7.8v10.8H19L18,7.8H30z M63.7,7.8L66,18.6H54V9.5L63.7,7.8z M89.8,7L102,7.8v10.8H91.2L89.8,7zM108,7.8l12,0.9v8.9l-12,1V7.8z"/>`, true, "tribal", "0 0 126 26"},
	"trees_tribal":   {`<path d="M6.4,2l4.2,5.7H7.7v2.7l3.8,5.2l-3.8,0v7.8H4.8v-7.8H0l4.8-5.2V7.7H1.1L6.4,2z M25.6,2L31,7.7h-3.7v2.7l4.8,5.2h-4.8v7.8h-2.8v-7.8l-3.8,0l3.8-5.2V7.7h-2.9L25.6,2z M47.5,2l4.2,5.7h-3.3v2.7l3.8,5.2l-3.8,0l0.4,7.8h-2.8v-7.8H41l4.8-5.2V7.7h-3.7L47.5,2z M66.2,2l5.4,5.7h-3.7v2.7l4.8,5.2h-4.8v7.8H65v-7.8l-3.8,0l3.8-5.2V7.7h-2.9L66.2,2zM87.4,2l4.8,5.7h-2.9v3.1l3.8,4.8l-3.8,0v7.8h-2.8v-7.8h-4.8l4.8-4.8V7.7h-3.7L87.4,2z M107.3,2l5.4,5.7h-3.7v2.7l4.8,5.2h-4.8v7.8H106v-7.8l-3.8,0l3.8-5.2V7.7h-2.9L107.3,2z"/>`, true, "tribal", "0 0 123 26"},
	"planes_tribal":  {`<path d="M29.6,10.3l2.1,2.2l-3.6,3.3h7v2.9h-7l3.6,3.5l-2.1,1.7l-5.2-5.2h-5.8v-2.9h5.8L29.6,10.3z M70.9,9.6l2.1,1.7l-3.6,3.5h7v2.9h-7l3.6,3.3l-2.1,2.2l-5.2-5.5h-5.8v-2.9h5.8L70.9,9.6z M111.5,9.6l2.1,1.7l-3.6,3.5h7v2.9h-7l3.6,3.3l-2.1,2.2l-5.2-5.5h-5.8v-2.9h5.8L111.5,9.6z M50.2,2.7l2.1,1.7l-3.6,3.5h7v2.9h-7l3.6,3.3l-2.1,2.2L45,10.7h-5.8V7.9H45L50.2,2.7z M11,2l2.1,1.7L9.6,7.2h7V10h-7l3.6,3.3L11,15.5L5.8,10H0V7.2h5.8L11,2z M91.5,2l2.1,2.2l-3.6,3.3h7v2.9h-7l3.6,3.5l-2.1,1.7l-5.2-5.2h-5.8V7.5h5.8L91.5,2z"/>`, true, "tribal", "0 0 121 26"},
	"x_tribal":       {`<path d="M10.7,6l2.5,2.6l-4,4.3l4,5.4l-2.5,1.9l-4.5-5.2l-3.9,4.2L0.7,17L4,13.1L0,8.6l2.3-1.3l3.9,3.9L10.7,6z M23.9,6.6l4.2,4.5L32,7.2l2.3,1.3l-4,4.5l3.2,3.9L32,19.1l-3.9-3.3l-4.5,4.3l-2.5-1.9l4.4-5.1l-4.2-3.9L23.9,6.6zM73.5,6L76,8.6l-4,4.3l4,5.4l-2.5,1.9l-4.5-5.2l-3.9,4.2L63.5,17l4.1-4.7L63.5,8l2.3-1.3l4.1,3.6L73.5,6z M94,6l2.5,2.6l-4,4.3l4,5.4L94,20.1l-3.9-5l-3.9,4.2L84,17l3.2-3.9L84,8.6l2.3-1.3l3.2,3.9L94,6z M106.9,6l4.5,5.1l3.9-3.9l2.3,1.3l-4,4.5l3.2,3.9l-1.6,2.1l-3.9-4.2l-4.5,5.2l-2.5-1.9l4-5.4l-4-4.3L106.9,6z M53.1,6l2.5,2.6l-4,4.6l-2.5,1.9l-4.5-4.5l-3.5,4.5L43.1,17l3.2-3.9l-4-4.5l2.3-1.3l3.9,3.9L53.1,6z"/>`, true, "tribal", "0 0 126 26"},
	"zigzag_tribal":  {"<polygon points=\"0,14.4 0,21 11.5,12.4 21.3,20 30.4,11.1 40.3,20 51,12.4 60.6,20 69.6,11.1 79.3,20 90.1,12.4 99.6,20 109.7,11.1 120,21 120,14.4 109.7,5 99.6,13 90.1,5 79.3,14.5 71,5.7 60.6,12.4 51,5 40.3,14.5 31.1,5 21.3,13 11.5,5 \t\"/>", true, "tribal", "0 0 120 26"},
}

func dividerSVG(w *elemCtx) string {
	if w.s("separator_type") != "pattern" || !w.has("style") {
		return ""
	}
	st, ok := dividerStyles[w.s("style")]
	if !ok {
		return ""
	}
	a := w.attr("svg")
	par := "none"
	if st.preserve {
		par = "xMidYMid meet"
	}
	vb := st.viewBox
	if vb == "" {
		vb = "0 0 24 24"
	}
	a.add("preserveAspectRatio", par)
	a.add("overflow", "visible")
	a.add("height", "100%")
	a.add("viewBox", vb)
	if st.group != "line" {
		a.add("fill", "black")
		a.add("stroke", "none")
	} else {
		a.add("fill", "none")
		a.add("stroke", "black")
		a.add("stroke-width", phpString(sub(w.get("weight"), "size")))
		a.add("stroke-linecap", "square")
		a.add("stroke-miterlimit", "10")
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" ` + a.String() + ">" + st.shape + "</svg>"
}

var svgDataURIReplacer = strings.NewReplacer("<", "%3C", ">", "%3E", `"`, "'", "#", "%23")

func renderDivider(w *elemCtx) string {
	svg := dividerSVG(w)
	look := w.s("look")
	hasIcon := look == "line_icon" && w.has("icon")
	hasText := look == "line_text" && w.has("text")
	wr := w.attr("wrapper")
	wr.add("class", "elementor-divider")
	if svg != "" {
		wr.add("style", `--divider-pattern-url: url("data:image/svg+xml,`+svgDataURIReplacer.Replace(svg)+`");`)
	}
	var b strings.Builder
	b.WriteString("\t\t<div " + wr.String() + ">\n\t\t\t<span class=\"elementor-divider-separator\">\n")
	if hasIcon {
		b.WriteString("\t\t\t\t<div class=\"elementor-icon elementor-divider__element\">\n\t\t\t\t\t")
		b.WriteString(renderIcon(w.get("icon"), newAttrs().add("aria-hidden", "true"), "i"))
		b.WriteString("</div>\n")
	} else if hasText {
		w.addInlineEditing("text", "")
		w.attr("text").add("class", "elementor-divider__text", "elementor-divider__element")
		tag := validateHTMLTag(w.s("html_tag"))
		b.WriteString("\t\t\t\t<" + tag + " " + w.attrString("text") + ">\n\t\t\t\t" + w.s("text") + "\t\t\t\t</" + tag + ">\n")
	}
	b.WriteString("\t\t\t</span>\n\t\t</div>\n\t\t")
	return b.String()
}

// ---- html / shortcode / menu anchor / read more ----

func renderHTMLWidget(w *elemCtx) string { return w.s("html") }

func renderShortcodeWidget(w *elemCtx) string {
	sc := w.s("shortcode")
	if isEmpty(sc) {
		return ""
	}
	sc = w.r.doShortcode(shortcodeUnautop(sc))
	return "\t\t<div class=\"elementor-shortcode\">" + sc + "</div>\n\t\t"
}

var miscSanitizeClassStrip = regexp.MustCompile(`%[a-fA-F0-9][a-fA-F0-9]|[^A-Za-z0-9_-]`)

// miscSanitizeHTMLClass mirrors sanitize_html_class.
func miscSanitizeHTMLClass(s string) string { return miscSanitizeClassStrip.ReplaceAllString(s, "") }

func renderMenuAnchor(w *elemCtx) string {
	anchor := w.s("anchor")
	if isEmpty(anchor) {
		return ""
	}
	w.attr("inner").add("class", "elementor-menu-anchor").add("id", miscSanitizeHTMLClass(anchor))
	return "\t\t<div " + w.attrString("inner") + "></div>\n\t\t"
}

func renderReadMore(w *elemCtx) string {
	return "<!--more " + ksesPost(w.s("link_text")) + "-->"
}

var moreTagRe = regexp.MustCompile(`<!--more(.*?)?-->`)

// processMoreTag mirrors Frontend::process_more_tag for a singular view: the first <!--more-->
// (read-more widget) splits the content and becomes <div id="more-ID"></div>.
func processMoreTag(content string, postID int64) string {
	content = strings.ReplaceAll(content, "&lt;!--more--&gt;", "<!--more-->")
	loc := moreTagRe.FindStringIndex(content)
	if loc == nil {
		return content
	}
	main := strings.TrimSpace(content[:loc[0]])
	extended := strings.TrimSpace(content[loc[1]:])
	if extended == "" {
		return content
	}
	return main + `<div id="more-` + strconv.FormatInt(postID, 10) + `"></div>` + extended
}
