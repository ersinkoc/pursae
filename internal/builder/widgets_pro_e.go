package builder

// Golementor Pro widgets: Nav Menu (Pro's full widget, replacing the Site widget of the same name),
// Search, Form and Login. Ports of upstream Pro's modules/nav-menu/widgets/nav-menu.php,
// modules/search/widgets/search.php, modules/forms/widgets/{form,login}.php and the form fields
// (modules/forms/fields/*.php, honeypot and reCAPTCHA handlers). Forms only print markup here; the
// server answers elementor_pro_forms_send_form.

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ProELoginPath is the login form action path (the server's login route).
var ProELoginPath = "/wp-login"

// ProERecaptchaSiteKey returns the reCAPTCHA site key (v3 = reCAPTCHA v3); "" means reCAPTCHA is
// not set up. The server sets it from the Golementor integration settings.
var ProERecaptchaSiteKey func(v3 bool) string

// ProEUsersCanRegister reports whether visitors may register (the login widget's Register link).
var ProEUsersCanRegister func() bool

func init() {
	proSupersedes["nav-menu"] = true
	registerWidget("nav-menu", &widgetDef{render: proERenderNavMenu, styles: static("widget-nav-menu"), scripts: []string{"smartmenus"}})
	registerWidget("search", &widgetDef{render: proERenderSearch, styles: static("widget-search")})
	registerWidget("form", &widgetDef{render: proERenderForm, styles: proEFormStyles})
	registerWidget("login", &widgetDef{render: proERenderLogin, styles: static("widget-login", "widget-form")})
}

// ---- Nav Menu ------------------------------------------------------------------------------------

// proEMenuItems resolves the widget's menu: the Pro "menu" setting (a menu id), else the Site
// widget's menu_source/menu_location settings, else the primary location.
func proEMenuItems(w *elemCtx) []*MenuItem {
	t := w.theme()
	if t == nil || t.Menu == nil {
		return nil
	}
	menu := strings.TrimSpace(phpString(w.raw("menu")))
	if menu != "" {
		id, err := strconv.ParseInt(menu, 10, 64)
		if err != nil || id <= 0 {
			return nil
		}
		return t.Menu("", id)
	}
	loc := phpString(w.raw("menu_location"))
	if loc == "" {
		loc = "primary"
	}
	return t.Menu(loc, 0)
}

// proESubmenuIndicator mirrors build_submenu_indicator_markup (font icons render as SVG).
func proESubmenuIndicator(icon map[string]any) string {
	value := phpString(icon["value"])
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "fa ") {
		value = "fas " + value[3:]
		icon = map[string]any{"value": value, "library": icon["library"]}
	}
	a := newAttrs().add("aria-hidden", "true")
	if strings.Contains(value, "chevron-down") {
		a.add("class", "fa-svg-chevron-down")
	}
	if svg := fontIconSVG(icon, cloneAttrs(a)); svg != "" {
		return svg
	}
	a.add("class", value)
	return "<i " + a.String() + "></i>"
}

func proEIsHome(w *elemCtx, it *MenuItem) bool {
	if it.Kind != "custom" {
		return false
	}
	home := strings.TrimRight(w.homeURL(), "/")
	return strings.TrimRight(it.URL, "/") == home
}

// proEMenuHTML mirrors wp_nav_menu with Walker_Nav_Menu and the widget's link/submenu filters.
func proEMenuHTML(w *elemCtx, items []*MenuItem, menuID, menuClass, indicator string, dropdown bool) string {
	var b strings.Builder
	var walk func(list []*MenuItem, depth int)
	walk = func(list []*MenuItem, depth int) {
		indent := strings.Repeat("\t", depth)
		for _, it := range list {
			var cls []string
			if it.Classes != "" {
				cls = append(cls, strings.Fields(it.Classes)...)
			}
			cls = append(cls, "menu-item")
			if it.Kind != "" {
				cls = append(cls, "menu-item-type-"+it.Kind)
			}
			if it.Object != "" {
				cls = append(cls, "menu-item-object-"+it.Object)
			}
			if it.Current {
				cls = append(cls, "current-menu-item")
			}
			if it.CurrentAncestor {
				cls = append(cls, "current-menu-ancestor", "current-menu-parent")
			}
			if proEIsHome(w, it) {
				cls = append(cls, "menu-item-home")
			}
			if len(it.Children) > 0 {
				cls = append(cls, "menu-item-has-children")
			}
			cls = append(cls, "menu-item-"+strconv.FormatInt(it.ID, 10))
			b.WriteString(indent + "<li class=\"" + escAttr(strings.Join(cls, " ")) + "\">")
			a := newAttrs()
			if it.Target != "" {
				a.add("target", it.Target)
			}
			a.add("href", it.URL)
			if it.Current {
				a.add("aria-current", "page")
			}
			lc := "elementor-item"
			if depth > 0 {
				lc = "elementor-sub-item"
			}
			anchor := strings.Contains(it.URL, "#")
			if !anchor && it.Current {
				lc += " elementor-item-active"
			}
			if anchor {
				lc += " elementor-item-anchor"
			}
			a.add("class", lc)
			if dropdown {
				a.add("tabindex", "-1")
			}
			title := escHTML(it.Label)
			if len(it.Children) > 0 && indicator != "" {
				title += "<span class=\"sub-arrow\">" + indicator + "</span>"
			}
			b.WriteString("<a " + proEAttrsURL(a) + ">" + title + "</a>")
			if len(it.Children) > 0 {
				b.WriteString("\n" + indent + "<ul class=\"sub-menu elementor-nav-menu--dropdown\">\n")
				walk(it.Children, depth+1)
				b.WriteString(indent + "</ul>\n" + indent)
			}
			b.WriteString("</li>\n")
		}
	}
	walk(items, 0)
	return "<ul id=\"" + escAttr(menuID) + "\" class=\"" + escAttr(menuClass) + "\">" + b.String() + "</ul>"
}

// proEAttrsURL prints attributes with href escaped as a URL.
func proEAttrsURL(a *attrs) string {
	parts := make([]string, 0, len(a.keys))
	for _, k := range a.keys {
		v := strings.Join(a.vals[k], " ")
		if k == "href" {
			parts = append(parts, `href="`+escURL(v)+`"`)
			continue
		}
		parts = append(parts, k+`="`+escAttr(v)+`"`)
	}
	return strings.Join(parts, " ")
}

func proERenderNavMenu(w *elemCtx) string {
	items := proEMenuItems(w)
	if len(items) == 0 {
		if strings.TrimSpace(phpString(w.raw("menu"))) == "" {
			return w.proPlaceholder("No menu is assigned to this location yet. Assign one in Appearance › Menus, or choose a menu.")
		}
		return w.proPlaceholder("Choose a menu for this widget, or create one in Appearance › Menus.")
	}
	// get_frontend_settings sends the submenu indicator's markup instead of its icon value.
	icon := asMap(w.get("submenu_icon"))
	indicator := ""
	if icon != nil {
		indicator = proESubmenuIndicator(icon)
		fo := newOmap()
		fo.Set("value", indicator)
		fo.Set("library", icon["library"])
		w.disp.Set("submenu_icon", fo)
	}
	layout := w.s("layout")
	menuClass := "elementor-nav-menu"
	if layout == "vertical" {
		menuClass += " sm-vertical"
	}
	mainHTML := proEMenuHTML(w, items, "menu-1-"+w.el.ID, menuClass, indicator, layout == "dropdown")
	dropHTML := proEMenuHTML(w, items, "menu-2-"+w.el.ID, menuClass, indicator, true)
	var b strings.Builder
	if layout != "dropdown" {
		a := w.attr("main-menu")
		if name := w.s("menu_name"); name != "" {
			a.add("aria-label", name)
		}
		a.add("class", "elementor-nav-menu--main", "elementor-nav-menu__container", "elementor-nav-menu--layout-"+layout)
		if p := w.s("pointer"); p != "" {
			a.add("class", "e--pointer-"+p)
			for _, k := range w.disp.keys {
				if strings.HasPrefix(k, "animation") && truthy(w.disp.m[k]) {
					a.add("class", "e--animation-"+phpString(w.disp.m[k]))
					break
				}
			}
		}
		b.WriteString("\t\t\t<nav " + w.attrString("main-menu") + ">\n\t\t\t\t" + mainHTML + "\t\t\t</nav>\n")
	}
	b.WriteString(proENavToggle(w))
	b.WriteString("\t\t\t<nav class=\"elementor-nav-menu--dropdown elementor-nav-menu__container\" aria-hidden=\"true\">\n\t\t\t\t" + dropHTML + "\t\t\t</nav>\n")
	return b.String()
}

func proENavToggle(w *elemCtx) string {
	if w.s("toggle") != "burger" {
		return ""
	}
	a := w.attr("menu-toggle")
	a.add("class", "elementor-menu-toggle").add("role", "button").add("tabindex", "0").add("aria-label", "Menu Toggle").add("aria-expanded", "false")
	if w.editing() {
		a.add("class", "elementor-clickable")
	}
	anim := ""
	if h := w.s("toggle_icon_hover_animation"); h != "" {
		anim = " elementor-animation-" + h
	}
	icon := func(setting string, fallback string, cls string) string {
		im := asMap(w.get(setting))
		if im == nil || !truthy(im["value"]) {
			im = map[string]any{"library": "eicons", "value": fallback}
		}
		ia := newAttrs().add("aria-hidden", "true").add("role", "presentation").add("class", cls)
		out := renderIcon(im, ia, "i")
		if phpString(im["library"]) == "svg" {
			out = "<span class=\"" + escAttr(cls) + "\">" + out + "</span>"
		}
		return out
	}
	return "\t\t<div " + w.attrString("menu-toggle") + ">\n\t\t\t" +
		icon("toggle_icon_normal", "eicon-menu-bar", "elementor-menu-toggle__icon--open"+anim) +
		icon("toggle_icon_active", "eicon-close", "elementor-menu-toggle__icon--close"+anim) + "\t\t</div>\n"
}

// ---- Search --------------------------------------------------------------------------------------

func proERenderSearch(w *elemCtx) string {
	id := w.el.ID
	shown := w.s("submit_trigger") != "key_enter"
	screen := ""
	if !shown {
		screen = "elementor-screen-only"
	}
	results := w.attr("results")
	if w.s("pagination_type_options") == "previous_next" {
		results.add("class", "hide-pagination-numbers")
	}
	results.add("id", "results-"+id).add("class", "e-search-results-container hide-loader").add("aria-live", "polite").
		add("aria-atomic", "true").add("aria-label", "Results for search").add("tabindex", "0")
	autocomplete := "off"
	if w.s("autocomplete") == "yes" {
		autocomplete = "on"
	}
	input := w.attr("input")
	input.add("id", "search-"+id).add("placeholder", w.s("search_input_placeholder_text")).add("class", "e-search-input").
		add("type", "search").add("name", "s").add("value", "").add("autocomplete", autocomplete).add("role", "combobox").
		add("aria-autocomplete", "list").add("aria-expanded", "false").add("aria-controls", "results-"+id).add("aria-haspopup", "listbox")
	submit := w.attr("submit")
	anim := ""
	if h := w.s("submit_hover_animation"); h != "" {
		anim = "elementor-animation-" + h
	}
	submit.add("class", "e-search-submit", screen, anim).add("type", "submit")
	if w.s("submit_button_text") == "" {
		submit.add("aria-label", "Search")
	}
	icon := func(key string) string {
		return renderIcon(w.get(key), newAttrs().add("aria-hidden", "true"), "i")
	}
	loader := ""
	if w.s("enable_loader") == "yes" {
		loader = `<div class="e-search-loader"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 28 28"><path fill-rule="evenodd" d="M14 .188c.587 0 1.063.475 1.063 1.062V5.5a1.063 1.063 0 0 1-2.126 0V1.25c0-.587.476-1.063 1.063-1.063ZM4.182 4.181a1.063 1.063 0 0 1 1.503 0L8.73 7.228A1.062 1.062 0 1 1 7.228 8.73L4.182 5.685a1.063 1.063 0 0 1 0-1.503Zm19.636 0a1.063 1.063 0 0 1 0 1.503L20.772 8.73a1.062 1.062 0 1 1-1.502-1.502l3.045-3.046a1.063 1.063 0 0 1 1.503 0ZM.188 14c0-.587.475-1.063 1.062-1.063H5.5a1.063 1.063 0 0 1 0 2.126H1.25A1.063 1.063 0 0 1 .187 14Zm21.25 0c0-.587.475-1.063 1.062-1.063h4.25a1.063 1.063 0 0 1 0 2.126H22.5A1.063 1.063 0 0 1 21.437 14ZM8.73 19.27a1.062 1.062 0 0 1 0 1.502l-3.045 3.046a1.063 1.063 0 0 1-1.503-1.503l3.046-3.046a1.063 1.063 0 0 1 1.502 0Zm10.54 0a1.063 1.063 0 0 1 1.502 0l3.046 3.045a1.063 1.063 0 0 1-1.503 1.503l-3.046-3.046a1.063 1.063 0 0 1 0-1.502ZM14 21.438c.587 0 1.063.475 1.063 1.062v4.25a1.063 1.063 0 0 1-2.126 0V22.5c0-.587.476-1.063 1.063-1.063Z"/></svg></div>`
	}
	out := "<output " + results.String() + "><div class=\"e-search-results\"></div>" + loader + "</output>"
	widthDropdown := w.s("results_is_dropdown_width") == "widget_width"
	var b strings.Builder
	b.WriteString("\t\t<search class=\"e-search hidden\" role=\"search\">\n")
	b.WriteString("\t\t\t<form class=\"e-search-form\" action=\"" + escURL(strings.TrimRight(w.r.rc.HomeURL, "/")) + "\" method=\"get\">\n")
	b.WriteString("\t\t\t\t<label class=\"e-search-label\" for=\"search-" + escAttr(id) + "\">\n\t\t\t\t\t<span class=\"elementor-screen-only\">\n\t\t\t\t\t\tSearch\t\t\t\t\t</span>\n\t\t\t\t\t" +
		icon("icon_search") + "\t\t\t\t</label>\n")
	b.WriteString("\t\t\t\t<div class=\"e-search-input-wrapper\">\n\t\t\t\t\t<input " + input.String() + ">\n\t\t\t\t\t" + icon("icon_clear"))
	if !widthDropdown {
		b.WriteString(out)
	}
	b.WriteString("\t\t\t\t</div>\n")
	if widthDropdown {
		b.WriteString(out)
	}
	b.WriteString("\t\t\t\t<button " + submit.String() + ">\n\t\t\t\t\t" + icon("icon_submit"))
	if t := w.s("submit_button_text"); t != "" {
		b.WriteString("<span class=\"" + screen + "\">\n\t\t\t\t\t\t" + escHTML(t) + "\t\t\t\t\t</span>\n")
	}
	b.WriteString("\t\t\t\t</button>\n")
	b.WriteString("\t\t\t\t<input type=\"hidden\" name=\"e_search_props\" value=\"" + escAttr(id+"-"+strconv.FormatInt(proEDocID(w), 10)) + "\">\n")
	b.WriteString("\t\t\t</form>\n\t\t</search>\n")
	return b.String()
}

// proEDocID is Utils::get_current_post_id: the document being rendered.
func proEDocID(w *elemCtx) int64 {
	if w.r.doc != nil {
		return w.r.doc.ID
	}
	return 0
}

// ---- Form ----------------------------------------------------------------------------------------

func proEFormStyles(w *elemCtx) []string {
	out := []string{"widget-form"}
	for _, it := range asList(w.get("form_fields")) {
		switch phpString(asMap(it)["field_type"]) {
		case "date", "time":
			out = addUnique(out, "flatpickr")
		}
	}
	return out
}

// proEFormScripts adds the scripts the form's fields need (flatpickr for date and time fields).
func proEFormScripts(w *elemCtx) {
	for _, it := range asList(w.get("form_fields")) {
		switch phpString(asMap(it)["field_type"]) {
		case "date", "time":
			w.r.scripts = addUnique(w.r.scripts, "flatpickr")
		case "recaptcha":
			w.r.scripts = addUnique(w.r.scripts, "elementor-recaptcha-api")
		case "recaptcha_v3":
			w.r.scripts = addUnique(w.r.scripts, "elementor-recaptcha_v3-api")
		}
	}
}

func proEFieldName(item map[string]any) string {
	return "form_fields[" + phpString(item["custom_id"]) + "]"
}
func proEFieldID(item map[string]any) string {
	return "form-field-" + escAttr(phpString(item["custom_id"]))
}

var proELineRe = regexp.MustCompile(`\r\n|\r|\n`)

func proERenderForm(w *elemCtx) string {
	proEFormScripts(w)
	a := func(k string) *attrs { return w.attr(k) }
	a("wrapper").add("class", "elementor-form-fields-wrapper", "elementor-labels-"+w.s("label_position"))
	a("submit-group").add("class", "elementor-field-group", "elementor-column", "elementor-field-type-submit")
	a("button").add("class", "elementor-button").add("type", "submit")
	bw := w.s("button_width")
	if bw == "" {
		bw = "100"
	}
	a("submit-group").add("class", "elementor-col-"+bw+" e-form__buttons")
	if v := w.s("button_width_tablet"); v != "" {
		a("submit-group").add("class", "elementor-md-"+v)
	}
	if v := w.s("button_width_mobile"); v != "" {
		a("submit-group").add("class", "elementor-sm-"+v)
	}
	if v := w.s("button_size"); v != "" {
		a("button").add("class", "elementor-size-"+v)
	}
	if v := w.s("button_type"); v != "" {
		a("button").add("class", "elementor-button-"+v)
	}
	if v := w.s("button_hover_animation"); v != "" {
		a("button").add("class", "elementor-animation-"+v)
	}
	if v := w.s("form_id"); v != "" {
		a("form").add("id", v)
	}
	if v := w.s("form_name"); v != "" {
		a("form").add("name", v).add("aria-label", v)
	}
	if w.s("form_validation") == "custom" {
		a("form").add("novalidate", "")
	}
	if v := w.s("button_css_id"); v != "" {
		a("button").add("id", v)
	}
	refererTitle := ""
	post := w.currentPostAny()
	if post != nil {
		refererTitle = strings.TrimSpace(proEUnescHTML(post.Title))
	} else if t := w.theme(); t != nil {
		refererTitle = t.SiteName
	}
	var b strings.Builder
	formAttrs := ""
	if s := w.attrString("form"); s != "" {
		formAttrs = " " + s
	}
	b.WriteString("\t\t<form class=\"elementor-form\" method=\"post\"" + formAttrs + ">\n")
	b.WriteString("\t\t\t<input type=\"hidden\" name=\"post_id\" value=\"" + strconv.FormatInt(proEDocID(w), 10) + "\"/>\n")
	b.WriteString("\t\t\t<input type=\"hidden\" name=\"form_id\" value=\"" + escAttr(w.el.ID) + "\"/>\n")
	b.WriteString("\t\t\t<input type=\"hidden\" name=\"referer_title\" value=\"" + escAttr(refererTitle) + "\" />\n")
	if post != nil {
		b.WriteString("\t\t\t<input type=\"hidden\" name=\"queried_id\" value=\"" + strconv.FormatInt(post.ID, 10) + "\"/>\n")
	}
	b.WriteString("\t\t\t<div " + w.attrString("wrapper") + ">\n")
	inputSize := w.s("input_size")
	showLabels := w.has("show_labels")
	for i, raw := range asList(w.get("form_fields")) {
		item := asMap(raw)
		if item == nil {
			continue
		}
		item["input_size"] = inputSize
		idx := strconv.Itoa(i)
		typ := phpString(item["field_type"])
		if typ == "recaptcha" || typ == "recaptcha_v3" {
			item["field_label"] = false
		}
		proEFieldAttrs(w, idx, item, showLabels)
		b.WriteString("\t\t\t\t<div " + w.attrString("field-group"+idx) + ">\n")
		if typ != "hidden" && typ != "html" && typ != "step" && truthy(item["field_label"]) {
			b.WriteString("\t\t\t\t\t<label " + w.attrString("label"+idx) + ">\n\t\t\t\t\t\t" + phpString(item["field_label"]) + "\t\t\t\t\t</label>\n")
		}
		b.WriteString(proEFieldHTML(w, idx, item, typ))
		b.WriteString("\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t\t<div " + w.attrString("submit-group") + ">\n\t\t\t\t\t<button " + w.attrString("button") + ">\n")
	b.WriteString("\t\t\t\t\t\t<span class=\"elementor-button-content-wrapper\">\n")
	btnIcon := asMap(w.get("selected_button_icon"))
	if truthy(w.get("button_icon")) || (btnIcon != nil && truthy(btnIcon["value"])) {
		b.WriteString("\t\t\t\t\t\t\t<span class=\"elementor-button-icon\">\n")
		if v := w.s("button_icon"); v != "" && (btnIcon == nil || !truthy(btnIcon["value"])) {
			b.WriteString("<i class=\"" + escAttr(v) + "\" aria-hidden=\"true\"></i>")
		} else {
			b.WriteString(renderIcon(btnIcon, newAttrs().add("aria-hidden", "true"), "i"))
		}
		if w.s("button_text") == "" {
			b.WriteString("<span class=\"elementor-screen-only\">Submit</span>")
		}
		b.WriteString("\t\t\t\t\t\t\t</span>\n")
	}
	if t := w.s("button_text"); t != "" {
		b.WriteString("\t\t\t\t\t\t\t<span class=\"elementor-button-text\">" + ksesPost(t) + "</span>\n")
	}
	b.WriteString("\t\t\t\t\t\t</span>\n\t\t\t\t\t</button>\n\t\t\t\t</div>\n\t\t\t</div>\n\t\t</form>\n")
	return b.String()
}

// proEFieldAttrs mirrors Form_Base::form_fields_render_attributes.
func proEFieldAttrs(w *elemCtx, i string, item map[string]any, showLabels bool) {
	typ := phpString(item["field_type"])
	w.attr("field-group"+i).add("class", "elementor-field-type-"+typ, "elementor-field-group", "elementor-column", "elementor-field-group-"+phpString(item["custom_id"]))
	css := ""
	if truthy(item["css_classes"]) {
		css = escAttr(phpString(item["css_classes"]))
	}
	w.attr("input"+i).add("type", typ).add("name", proEFieldName(item)).add("id", proEFieldID(item)).
		add("class", "elementor-field", "elementor-size-"+phpString(item["input_size"]), css)
	w.attr("label"+i).add("for", proEFieldID(item)).add("class", "elementor-field-label")
	width := phpString(item["width"])
	if width == "" {
		width = "100"
	}
	w.attr("field-group"+i).add("class", "elementor-col-"+width)
	if v := phpString(item["width_tablet"]); v != "" {
		w.attr("field-group"+i).add("class", "elementor-md-"+v)
	}
	if truthy(item["allow_multiple"]) {
		w.attr("field-group"+i).add("class", "elementor-field-type-"+typ+"-multiple")
	}
	if v := phpString(item["width_mobile"]); v != "" {
		w.attr("field-group"+i).add("class", "elementor-sm-"+v)
	}
	if !miscIsEmpty(item["placeholder"]) {
		w.attr("input"+i).add("placeholder", phpString(item["placeholder"]))
	}
	if truthy(item["field_value"]) {
		w.attr("input"+i).add("value", phpString(item["field_value"]))
	}
	if !showLabels {
		w.attr("label"+i).add("class", "elementor-screen-only")
	}
	if truthy(item["required"]) {
		cls := "elementor-field-required"
		if w.has("mark_required") {
			cls += " elementor-mark-required"
		}
		w.attr("field-group"+i).add("class", cls)
		w.attr("input"+i).add("required", "required")
	}
}

func proEFieldHTML(w *elemCtx, i string, item map[string]any, typ string) string {
	in := w.attr("input" + i)
	input := func(prefix string) string { return "\t\t\t\t\t<input " + prefix + in.String() + ">\n" }
	switch typ {
	case "html":
		return "\t\t\t\t\t" + w.r.doShortcode(phpString(item["field_html"])) + "\n"
	case "textarea":
		ta := w.attr("textarea" + i)
		ta.add("class", "elementor-field-textual", "elementor-field", escAttr(phpString(item["css_classes"])), "elementor-size-"+phpString(item["input_size"])).
			add("name", proEFieldName(item)).add("id", proEFieldID(item)).add("rows", phpString(item["rows"]))
		if truthy(item["placeholder"]) {
			ta.add("placeholder", phpString(item["placeholder"]))
		}
		if truthy(item["required"]) {
			ta.add("required", "required")
		}
		value := ""
		if truthy(item["field_value"]) {
			value = phpString(item["field_value"])
		}
		return "<textarea " + ta.String() + ">" + value + "</textarea>"
	case "select":
		return proESelect(w, i, item)
	case "radio", "checkbox":
		return proERadioCheckbox(w, item, typ)
	case "text", "email", "url", "password", "hidden", "search":
		in.add("class", "elementor-field-textual")
		return input(`size="1" `)
	case "tel":
		in.add("class", "elementor-field-textual").add("pattern", "[0-9()#&+*-=.]+").add("title", "Only numbers and phone characters (#, -, *, etc) are accepted.")
		return input(`size="1" `)
	case "number":
		in.add("class", "elementor-field-textual")
		if item["field_min"] != nil {
			in.add("min", phpString(item["field_min"]))
		}
		if item["field_max"] != nil {
			in.add("max", phpString(item["field_max"]))
		}
		return "\t\t\t<input " + in.String() + " >\n"
	case "date":
		in.add("class", "elementor-field-textual elementor-date-field").add("pattern", "[0-9]{4}-[0-9]{2}-[0-9]{2}")
		if phpString(item["use_native_date"]) == "yes" {
			in.add("class", "elementor-use-native")
		}
		if truthy(item["min_date"]) {
			in.add("min", phpString(item["min_date"]))
		}
		if truthy(item["max_date"]) {
			in.add("max", phpString(item["max_date"]))
		}
		return input("")
	case "time":
		in.add("class", "elementor-field-textual elementor-time-field")
		if phpString(item["use_native_time"]) == "yes" {
			in.add("class", "elementor-use-native")
		}
		return input("")
	case "acceptance":
		in.add("class", "elementor-acceptance-field").set("type", "checkbox")
		label := ""
		if truthy(item["acceptance_text"]) {
			label = "<label for=\"" + proEFieldID(item) + "\">" + phpString(item["acceptance_text"]) + "</label>"
		}
		if truthy(item["checked_by_default"]) {
			in.add("checked", "checked")
		}
		return "\t\t<div class=\"elementor-field-subgroup\">\n\t\t\t<span class=\"elementor-field-option\">\n\t\t\t\t<input " + in.String() + ">\n\t\t\t\t" + label + "\t\t\t</span>\n\t\t</div>\n"
	case "upload":
		in.add("class", "elementor-upload-field").set("type", "file")
		if truthy(item["allow_multiple_upload"]) {
			in.add("multiple", "multiple").set("name", proEFieldName(item)+"[]")
		}
		if truthy(item["file_sizes"]) {
			in.add("data-maxsize", phpString(item["file_sizes"])).add("data-maxsize-message", "This file exceeds the maximum allowed size.")
		}
		return input("")
	case "step":
		return proEStep(w, i, item)
	case "honeypot":
		in.set("type", "text").add("style", "display:none !important;")
		return input(`size="1" `)
	case "recaptcha", "recaptcha_v3":
		v3 := typ == "recaptcha_v3"
		out := "<div class=\"elementor-field\" id=\"form-field-" + escHTML(phpString(item["custom_id"])) + "\">"
		key := ""
		if ProERecaptchaSiteKey != nil {
			key = ProERecaptchaSiteKey(v3)
		}
		if key != "" {
			ra := w.attr(typ + i)
			if v3 {
				ra.add("class", "elementor-g-recaptcha").add("data-sitekey", key).add("data-type", "v3").
					add("data-action", "Form").add("data-badge", phpString(item["recaptcha_badge"])).add("data-size", "invisible")
			} else {
				ra.add("class", "elementor-g-recaptcha").add("data-sitekey", key).add("data-type", "v2_checkbox").
					add("data-theme", phpString(item["recaptcha_style"])).add("data-size", phpString(item["recaptcha_size"]))
			}
			out += "<div " + ra.String() + "></div>"
		} else if w.editing() {
			out += "<div class=\"elementor-alert elementor-alert-info\">To use reCAPTCHA, add the keys under Golementor › Settings › Integrations.</div>"
		}
		return out + "</div>"
	}
	return ""
}

func proESelect(w *elemCtx, i string, item map[string]any) string {
	wr := w.attr("select-wrapper" + i)
	wr.add("class", "elementor-field", "elementor-select-wrapper", "remove-before", escAttr(phpString(item["css_classes"])))
	name := proEFieldName(item)
	multiple := truthy(item["allow_multiple"])
	if multiple {
		name += "[]"
	}
	sel := w.attr("select" + i)
	sel.add("name", name).add("id", proEFieldID(item)).add("class", "elementor-field-textual", "elementor-size-"+phpString(item["input_size"]))
	if truthy(item["required"]) {
		sel.add("required", "required")
	}
	if multiple {
		sel.add("multiple", "")
		if truthy(item["select_size"]) {
			sel.add("size", phpString(item["select_size"]))
		}
	}
	opts := proELineRe.Split(phpString(item["field_options"]), -1)
	var b strings.Builder
	b.WriteString("\t\t<div " + wr.String() + ">\n\t\t\t<div class=\"select-caret-down-wrapper\">\n")
	if !multiple {
		b.WriteString("\t\t\t\t" + renderIcon(map[string]any{"library": "eicons", "value": "eicon-caret-down", "position": "right"}, newAttrs().add("aria-hidden", "true"), "i"))
	}
	b.WriteString("\t\t\t</div>\n\t\t\t<select " + sel.String() + ">\n")
	selected := strings.Split(phpString(item["field_value"]), ",")
	for _, opt := range opts {
		value, label := escAttr(opt), escHTML(opt)
		if strings.Contains(opt, "|") {
			parts := strings.SplitN(opt, "|", 3)
			label, value = escHTML(parts[0]), escAttr(parts[1])
		}
		oa := newAttrs().add("value", value)
		if truthy(item["field_value"]) {
			for _, s := range selected {
				if s == value {
					oa.add("selected", "selected")
					break
				}
			}
		}
		// value is already escaped: print it as is
		b.WriteString("\t\t\t\t\t<option value=\"" + value + "\"")
		if oa.has("selected") {
			b.WriteString(" selected=\"selected\"")
		}
		b.WriteString(">" + label + "</option>\n")
	}
	b.WriteString("\t\t\t</select>\n\t\t</div>\n")
	return b.String()
}

func proERadioCheckbox(w *elemCtx, item map[string]any, typ string) string {
	opts := proELineRe.Split(phpString(item["field_options"]), -1)
	var b strings.Builder
	b.WriteString("<div class=\"elementor-field-subgroup " + escAttr(phpString(item["css_classes"])) + " " + escAttr(phpString(item["inline_list"])) + "\">")
	for k, opt := range opts {
		htmlID := proEFieldID(item) + "-" + strconv.Itoa(k)
		label, value := opt, opt
		if strings.Contains(opt, "|") {
			parts := strings.SplitN(opt, "|", 3)
			label, value = parts[0], parts[1]
		}
		name := proEFieldName(item)
		if typ == "checkbox" && len(opts) > 1 {
			name += "[]"
		}
		a := newAttrs().add("type", typ).add("value", value).add("id", htmlID).add("name", name)
		if truthy(item["field_value"]) && value == phpString(item["field_value"]) {
			a.add("checked", "checked")
		}
		if truthy(item["required"]) && typ == "radio" {
			a.add("required", "required")
		}
		b.WriteString("<span class=\"elementor-field-option\"><input " + a.String() + "> <label for=\"" + htmlID + "\">" + label + "</label></span>")
	}
	b.WriteString("</div>")
	return b.String()
}

func proEStep(w *elemCtx, i string, item map[string]any) string {
	icon := asMap(item["selected_icon"])
	lib := phpString(icon["library"])
	fontIcon, iconURL, iconLib := "", "", ""
	if icon != nil && truthy(icon["value"]) {
		if lib == "svg" {
			fontIcon = uploadedSVG(icon["value"])
			iconURL = phpString(sub(icon["value"], "url"))
		} else {
			fontIcon = fontIconSVG(icon, nil)
			iconLib = phpString(icon["value"])
		}
	}
	a := w.attr("step" + i)
	a.add("class", "e-field-step elementor-hidden").add("data-label", phpString(item["field_label"])).
		add("data-previousButton", phpString(item["previous_button"])).add("data-nextButton", phpString(item["next_button"])).
		add("data-iconUrl", iconURL).add("data-iconLibrary", iconLib).add("data-icon", fontIcon)
	return "\t\t<div " + a.String() + " ></div>\n"
}

// ---- Login ---------------------------------------------------------------------------------------

// proECurrentURI is the request URI (remove_query_arg( 'fake_arg' )): the request when known, else
// the path of the page being shown.
func proECurrentURI(w *elemCtx) string {
	raw := w.r.pro().CurrentURL
	if raw == "" {
		if p := w.currentPostAny(); p != nil {
			raw = p.URL
		}
	}
	if raw == "" {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.RequestURI()
}

func proELoginURL(w *elemCtx) string {
	return strings.TrimRight(w.r.rc.HomeURL, "/") + ProELoginPath
}

func proERenderLogin(w *elemCtx) string {
	current := proECurrentURI(w)
	redirect, logoutRedirect := current, current
	if ru := asMap(w.get("redirect_url")); w.s("redirect_after_login") == "yes" && ru != nil && truthy(ru["url"]) {
		redirect = phpString(ru["url"])
	}
	if lu := asMap(w.get("redirect_logout_url")); w.s("redirect_after_logout") == "yes" && lu != nil && truthy(lu["url"]) {
		logoutRedirect = phpString(lu["url"])
	}
	if u := w.r.pro().User; u != nil && !w.editing() {
		if w.s("show_logged_in_message") == "yes" {
			logout := proELoginURL(w) + "?action=logout&redirect_to=" + url.QueryEscape(logoutRedirect)
			return "<div class=\"elementor-login elementor-login__logged-in-message\">You are Logged in as " + ksesPost(u.DisplayName) +
				" (<a href=\"" + escURL(logout) + "\" target=\"_blank\">Logout</a>)</div>"
		}
		return ""
	}
	a := w.attr
	if v := w.s("button_size"); v != "" {
		a("button").add("class", "elementor-size-"+v)
	}
	if v := w.s("button_hover_animation"); v != "" {
		a("button").add("class", "elementor-animation-"+v)
	}
	id := w.el.ID
	size := "elementor-size-" + w.s("input_size")
	a("wrapper").add("class", "elementor-form-fields-wrapper")
	a("field-group").add("class", "elementor-field-type-text", "elementor-field-group", "elementor-column", "elementor-col-100")
	a("submit-group").add("class", "elementor-field-group", "elementor-column", "elementor-field-type-submit", "elementor-col-100")
	a("button").add("class", "elementor-button").add("name", "wp-submit")
	a("user_label").add("for", "user-"+id).add("class", "elementor-field-label")
	a("user_input").add("size", "1").add("type", "text").add("name", "log").add("id", "user-"+id).add("placeholder", w.s("user_placeholder")).
		add("class", "elementor-field", "elementor-field-textual", size)
	a("password_label").add("for", "password-"+id).add("class", "elementor-field-label")
	a("password_input").add("size", "1").add("type", "password").add("name", "pwd").add("id", "password-"+id).add("placeholder", w.s("password_placeholder")).
		add("class", "elementor-field", "elementor-field-textual", size)
	if !w.has("show_labels") {
		a("user_label").add("class", "elementor-screen-only")
		a("password_label").add("class", "elementor-screen-only")
	}
	a("field-group").add("class", "elementor-field-required")
	var b strings.Builder
	b.WriteString("\t\t<form class=\"elementor-login elementor-form\" method=\"post\" action=\"" + escURL(proELoginURL(w)) + "\" aria-label=\"Login form\">\n")
	b.WriteString("\t\t\t<input type=\"hidden\" name=\"redirect_to\" value=\"" + escAttr(redirect) + "\">\n")
	b.WriteString("\t\t\t<div " + w.attrString("wrapper") + ">\n")
	b.WriteString("\t\t\t\t<div " + w.attrString("field-group") + ">\n\t\t\t\t\t<label " + w.attrString("user_label") + ">" + ksesPost(w.s("user_label")) + "</label>\n\t\t\t\t\t<input " + w.attrString("user_input") + ">\n\t\t\t\t</div>\n")
	b.WriteString("\t\t\t\t<div " + w.attrString("field-group") + ">\n\t\t\t\t\t<label " + w.attrString("password_label") + ">" + ksesPost(w.s("password_label")) + "</label>\n\t\t\t\t\t<input " + w.attrString("password_input") + ">\n\t\t\t\t</div>\n")
	if w.s("show_remember_me") == "yes" {
		b.WriteString("\t\t\t\t<div class=\"elementor-field-type-checkbox elementor-field-group elementor-column elementor-col-100 elementor-remember-me\">\n" +
			"\t\t\t\t\t<label for=\"elementor-login-remember-me\">\n\t\t\t\t\t\t<input type=\"checkbox\" id=\"elementor-login-remember-me\" name=\"rememberme\" value=\"forever\">\n\t\t\t\t\t\tRemember Me\t\t\t\t\t</label>\n\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t\t<div " + w.attrString("submit-group") + ">\n\t\t\t\t\t<button type=\"submit\" " + w.attrString("button") + ">\n")
	if t := w.s("button_text"); t != "" {
		b.WriteString("\t\t\t\t\t\t\t<span class=\"elementor-button-text\">" + ksesPost(t) + "</span>\n")
	}
	b.WriteString("\t\t\t\t\t</button>\n\t\t\t\t</div>\n")
	showLost := w.s("show_lost_password") == "yes"
	showRegister := ProEUsersCanRegister != nil && ProEUsersCanRegister() && w.s("show_register") == "yes"
	if showLost || showRegister {
		b.WriteString("\t\t\t\t<div class=\"elementor-field-group elementor-column elementor-col-100\">\n")
		if showLost {
			lost := proELoginURL(w) + "?action=lostpassword&redirect_to=" + url.QueryEscape(redirect)
			b.WriteString("\t\t\t\t\t\t<a class=\"elementor-lost-password\" href=\"" + lost + "\">\n\t\t\t\t\t\t\tLost your password?\t\t\t\t\t\t</a>\n")
		}
		if showRegister {
			if showLost {
				b.WriteString("<span class=\"elementor-login-separator\"> | </span>")
			}
			b.WriteString("<a class=\"elementor-register\" href=\"" + escURL(proELoginURL(w)+"?action=register") + "\">\n\t\t\t\t\t\t\tRegister\t\t\t\t\t\t</a>\n")
		}
		b.WriteString("\t\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t</div>\n\t\t</form>\n")
	return b.String()
}

func proEUnescHTML(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#039;", "'", "&#8217;", "’").Replace(s)
}
