package builder

// GoWebEditor Pro element features shared by every element: custom attributes (_attributes),
// custom CSS (custom_css, with "selector" standing for the element), display conditions
// (e_display_conditions) and the helpers Pro widgets share. Ports of upstream Pro's
// modules/custom-attributes, custom-css and display-conditions.

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// dropSettingKey removes a key from the element's saved key order.
func (e *Element) dropSettingKey(key string) {
	for i, k := range e.settingsKeys {
		if k == key {
			e.settingsKeys = append(e.settingsKeys[:i], e.settingsKeys[i+1:]...)
			return
		}
	}
}

// proSupersedes lists Site widgets (widgets_theme.go) that a GoWebEditor Pro port replaces; a Pro
// widget file adds its name from its init (which runs before widgets_theme.go's).
var proSupersedes = map[string]bool{}

// --- custom attributes -----------------------------------------------------------------------------

var proAttrBlackList = map[string]bool{"id": true, "class": true, "data-id": true, "data-settings": true, "data-element_type": true,
	"data-widget_type": true, "data-model-cid": true}

// proRenderAttributes mirrors CustomAttributes\Module::render_attributes (Utils::parse_custom_attributes with "\n").
func proRenderAttributes(e *elemCtx, w *attrs) {
	raw := e.s("_attributes")
	if strings.TrimSpace(raw) == "" {
		return
	}
	for _, line := range strings.Split(raw, "\n") {
		kv := strings.SplitN(line, "|", 2)
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		m := customAttrKeyRe.FindString(key)
		if m == "" || proAttrBlackList[m] || m == "href" || strings.HasPrefix(m, "on") {
			continue
		}
		val := ""
		if len(kv) > 1 {
			val = strings.TrimSpace(kv[1])
		}
		w.add(m, val)
	}
}

// --- custom CSS ------------------------------------------------------------------------------------

// proElementCustomCSS mirrors CustomCss\Module::add_post_css for one element.
func proElementCustomCSS(g *cssGen, el *Element, name string, settings map[string]any, wrapper string) {
	css := strings.TrimSpace(phpString(settings["custom_css"]))
	if css == "" {
		return
	}
	css = strings.ReplaceAll(css, "selector", wrapper)
	g.sheet.raw = append(g.sheet.raw, "/* Start custom CSS for "+name+", class: .elementor-element-"+el.ID+" */"+css+"/* End custom CSS */")
}

// proDocumentCustomCSS mirrors CustomCss\Module::add_page_settings_css.
func proDocumentCustomCSS(g *cssGen, settings map[string]any, wrapper string) {
	css := strings.TrimSpace(phpString(settings["custom_css"]))
	if css == "" {
		return
	}
	css = strings.ReplaceAll(css, "selector", wrapper)
	g.sheet.raw = append(g.sheet.raw, "/* Start custom CSS */"+css+"/* End custom CSS */")
}

// --- display conditions -----------------------------------------------------------------------------

// proShouldRender mirrors DisplayConditions\Module::before_element_render: an element with
// conditions renders only when one AND-group passes (the editor always shows it).
func proShouldRender(e *elemCtx) bool {
	if e.editing() {
		return true
	}
	raw := e.get("e_display_conditions")
	var text string
	switch v := raw.(type) {
	case []any:
		if len(v) == 0 {
			return true
		}
		text = phpString(v[0])
	case string:
		text = v
	default:
		return true
	}
	text = strings.TrimSpace(text)
	if text == "" || text == "[]" {
		return true
	}
	var any_ any
	if json.Unmarshal([]byte(text), &any_) != nil {
		return true
	}
	groups := asList(any_)
	if len(groups) == 0 {
		return true
	}
	// a flat list of conditions is one group
	if m := asMap(groups[0]); m != nil && m["condition"] != nil {
		groups = []any{groups}
	}
	for _, g := range groups {
		pass := true
		for _, c := range asList(g) {
			cm := asMap(c)
			if cm == nil {
				continue
			}
			if !proCheckCondition(e, cm) {
				pass = false
				break
			}
		}
		if pass {
			return true
		}
	}
	return false
}

func proCheckCondition(e *elemCtx, c map[string]any) bool {
	comp := phpString(c["comparator"])
	pd := e.r.pro()
	post := e.currentPostAny()
	ids := func(key string) []int64 {
		var out []int64
		for _, it := range asList(c[key]) {
			if m := asMap(it); m != nil {
				out = append(out, toInt64(m["id"]))
			} else {
				out = append(out, toInt64(it))
			}
		}
		return out
	}
	contains := func(expected []int64, actual []int64) bool {
		found := false
		for _, a := range actual {
			for _, x := range expected {
				if a == x {
					found = true
				}
			}
		}
		switch comp {
		case "is", "is_one_of":
			return found
		case "is_not", "is_none_of":
			return !found
		}
		return false
	}
	equal := func(a, b bool) bool {
		switch comp {
		case "is":
			return a == b
		case "is_not":
			return a != b
		}
		return false
	}
	now := pd.Now
	if now.IsZero() {
		now = time.Now()
	}
	dateCmp := func(set, actual time.Time) bool {
		switch comp {
		case "is":
			return set.Equal(actual)
		case "is_not":
			return !set.Equal(actual)
		case "is_after":
			return set.Before(actual)
		case "is_before":
			return set.After(actual)
		case "is_after_inclusive":
			return !set.After(actual)
		case "is_before_inclusive":
			return !set.Before(actual)
		}
		return false
	}
	parseDate := func(s string) (time.Time, bool) {
		for _, l := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02", "15:04"} {
			if t, err := time.ParseInLocation(l, strings.TrimSpace(s), now.Location()); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	switch phpString(c["condition"]) {
	case "page_title", "post_title":
		if post == nil {
			return false
		}
		var titles []string
		for _, id := range ids("titles") {
			if pd.PostByID != nil {
				if p := pd.PostByID(id); p != nil {
					titles = append(titles, p.Title)
				}
			}
		}
		found := false
		for _, t := range titles {
			if t == post.Title {
				found = true
			}
		}
		return equal(found, true)
	case "page_parent":
		if post == nil || pd.PostMeta == nil {
			return comp == "is_none_of"
		}
		return contains(ids("pages"), []int64{toInt64(pd.PostMeta(post.ID, "_parent_id"))})
	case "page_author", "post_author":
		if post == nil || pd.AuthorOf == nil {
			return false
		}
		u := pd.AuthorOf(post.ID)
		if u == nil {
			return false
		}
		return contains(ids("authors"), []int64{u.ID})
	case "in_categories", "in_tags":
		if post == nil {
			return false
		}
		want := ids("categories")
		tax := "category"
		if phpString(c["condition"]) == "in_tags" {
			want = ids("tags")
			tax = "post_tag"
		}
		if len(want) == 0 {
			return true
		}
		var have []int64
		if pd.PostTermIDs != nil {
			have = pd.PostTermIDs(post.ID, tax)
		}
		if len(have) == 0 {
			return comp != "is"
		}
		return contains(want, have)
	case "date_of_publish", "date_of_modification":
		if post == nil {
			return false
		}
		set, ok := parseDate(phpString(c["date"]))
		if !ok {
			return false
		}
		iso := post.DateISO
		if phpString(c["condition"]) == "date_of_modification" && pd.PostMeta != nil {
			iso = phpString(pd.PostMeta(post.ID, "_modified_iso"))
		}
		actual, ok := parseISO(iso)
		if !ok {
			return false
		}
		return dateCmp(set, actual.In(now.Location()))
	case "number_of_comments":
		if post == nil {
			return false
		}
		n := toInt(c["number_of_comments"])
		have := post.CommentCount
		switch comp {
		case "is":
			return have == n
		case "is_not":
			return have != n
		case "is_less_than_inclusive":
			return have <= n
		case "is_greater_than_inclusive":
			return have >= n
		}
		return false
	case "featured_image":
		has := post != nil && post.ImageID > 0
		return equal(phpString(c["status"]) == "set", has)
	case "login_status":
		return equal(phpString(c["status"]) == "logged_in", pd.User != nil)
	case "user_role":
		var roles []string
		if pd.User != nil {
			roles = pd.User.Roles
		}
		found := false
		for _, r := range asList(c["roles"]) {
			for _, have := range roles {
				if phpString(r) == have {
					found = true
				}
			}
		}
		if comp == "is_none_of" || comp == "is_not" {
			return !found
		}
		return found
	case "user_registration_date":
		if pd.User == nil {
			return false
		}
		set, ok := parseDate(phpString(c["date"]))
		if !ok {
			return false
		}
		reg, ok2 := parseISO(phpString(pd.User.Meta["registered_iso"]))
		if !ok2 {
			return false
		}
		return dateCmp(set, reg.In(now.Location()))
	case "day_of_the_week":
		day := strings.ToLower(now.Weekday().String())
		found := false
		for _, d := range asList(c["days"]) {
			if phpString(d) == day {
				found = true
			}
		}
		if comp == "is_none_of" {
			return !found
		}
		return found
	case "time_of_the_day":
		set, ok := parseDate(phpString(c["time"]))
		if !ok {
			return false
		}
		set = time.Date(now.Year(), now.Month(), now.Day(), set.Hour(), set.Minute(), 0, 0, now.Location())
		actual := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, now.Location())
		return dateCmp(set, actual)
	case "current_date":
		set, ok := parseDate(phpString(c["date"]))
		if !ok {
			return false
		}
		return dateCmp(set, now.Truncate(time.Minute))
	case "archive_of_categories", "archive_of_tags", "archive_of_authors":
		a := e.archiveAny()
		if a == nil {
			return comp == "is_not"
		}
		var have []int64
		if pd.ArchiveTermID > 0 {
			have = append(have, pd.ArchiveTermID)
		}
		if phpString(c["condition"]) == "archive_of_authors" {
			have = nil
			if pd.ArchiveAuthorID > 0 {
				have = []int64{pd.ArchiveAuthorID}
			}
		}
		key := "categories"
		if phpString(c["condition"]) == "archive_of_tags" {
			key = "tags"
		} else if phpString(c["condition"]) == "archive_of_authors" {
			key = "authors"
		}
		want := ids(key)
		if len(want) == 0 {
			if comp == "is" {
				return len(have) > 0
			}
			return len(have) == 0
		}
		return contains(want, have)
	case "from_url":
		ref := ""
		if pd.Request != nil {
			ref = pd.Request("server", "HTTP_REFERER")
		}
		want := strings.ToLower(phpString(c["from_url"]))
		have := strings.ToLower(ref)
		switch comp {
		case "is":
			return want == have
		case "is_not":
			return want != have
		case "contains":
			return strings.Contains(have, want)
		case "not_contain":
			return !strings.Contains(have, want)
		}
		return false
	case "dynamic_tags":
		val := ""
		switch phpString(c["dynamic_tag"]) {
		case "archive_title", "archive":
			if a := e.archiveAny(); a != nil {
				val = stripTags(a.PlainTitle)
			}
		case "featured_image":
			if post != nil && post.ImageID > 0 {
				val = post.ImageURL
			}
		case "featured_image_data_title", "featured_image_data_alt", "featured_image_data_caption":
			if post != nil && post.ImageID > 0 {
				if a := e.r.attachment(post.ImageID); a != nil {
					switch phpString(c["dynamic_tag"]) {
					case "featured_image_data_alt":
						val = a.Alt
					case "featured_image_data_caption":
						val = a.Caption
					default:
						val = a.Title
					}
				}
			}
		case "author", "author_info_bio", "author_info_email", "author_info_website":
			if post != nil && pd.AuthorOf != nil {
				if u := pd.AuthorOf(post.ID); u != nil {
					switch phpString(c["dynamic_tag"]) {
					case "author_info_bio":
						val = u.Description
					case "author_info_email":
						val = u.Email
					case "author_info_website":
						val = u.URL
					default:
						val = u.DisplayName
					}
				}
			}
		}
		want := strings.ToLower(phpString(c["dynamic_tag_value"]))
		have := strings.ToLower(val)
		switch comp {
		case "is":
			return want == have
		case "is_not":
			return want != have
		case "contains":
			return strings.Contains(have, want)
		case "not_contain":
			return !strings.Contains(have, want)
		case "is_empty":
			return have == ""
		case "is_not_empty":
			return have != ""
		}
		return false
	}
	return true
}

// currentPostAny is the post being shown, including the document itself when it is a page/post.
func (e *elemCtx) currentPostAny() *PostData {
	th := e.r.rc.Theme
	if th == nil || th.Post == nil {
		return nil
	}
	return th.Post()
}

func (e *elemCtx) archiveAny() *ArchiveData {
	th := e.r.rc.Theme
	if th == nil || th.Archive == nil {
		return nil
	}
	return th.Archive()
}

// --- shared Pro helpers ------------------------------------------------------------------------------

// proEditorPlaceholder prints an editor-only notice (Pro widgets with nothing to show).
func (w *elemCtx) proPlaceholder(text string) string {
	if !w.editing() {
		return ""
	}
	return "\t\t\t<div class=\"elementor-theme-placeholder\">" + escHTML(text) + "</div>\n"
}

// settingsJSON prints a settings subset as a data attribute value.
func settingsJSON(o *omap) string { return phpJSON(o) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// proSliderSize returns size as text ("" when unset).
func proSliderSize(v any) string {
	m := asMap(v)
	if m == nil {
		return ""
	}
	return phpString(m["size"])
}

// --- global widgets ------------------------------------------------------------------------------------

// resolveGlobals mirrors Global_Widget: a "global" element takes the settings of the widget saved in
// its template (RenderContext.Pro.Template) and renders and styles as that widget, with the wrapper
// class elementor-widget-global elementor-global-<id> elementor-widget-<type>.
func (r *renderer) resolveGlobals(list []*Element) {
	for _, el := range list {
		if el == nil {
			continue
		}
		if el.ElType == "widget" && el.WidgetType == "global" && el.globalOf == "" {
			id := el.TemplateID
			if id == 0 {
				id = toInt64(el.Settings["templateID"])
			}
			if id > 0 && r.rc.Pro != nil && r.rc.Pro.Template != nil {
				if tdoc := r.rc.Pro.Template(id); tdoc != nil && len(tdoc.Elements) > 0 && tdoc.Elements[0] != nil && tdoc.Elements[0].ElType == "widget" {
					src := tdoc.Elements[0]
					el.TemplateID = id
					el.globalOf = src.WidgetType
					el.Settings = deepCopy(src.Settings).(map[string]any)
					el.settingsKeys = append([]string(nil), src.settingsKeys...)
					el.nestedKeys = src.nestedKeys
				}
			}
		}
		r.resolveGlobals(el.Elements)
	}
}

var proGlobalDefs = map[string]*widgetDef{}

// proGlobalDef is the original widget's definition with the global wrapper class.
func proGlobalDef(el *Element) *widgetDef {
	key := el.globalOf + "|" + itoa64(el.TemplateID)
	if d, ok := proGlobalDefs[key]; ok {
		return d
	}
	orig := widgetDefs[el.globalOf]
	if orig == nil {
		return nil
	}
	cp := *orig
	wc := orig.wrapperClass
	if wc == "" {
		wc = "elementor-widget-" + el.globalOf
	}
	cp.wrapperClass = "elementor-widget-global elementor-global-" + itoa64(el.TemplateID) + " " + wc
	proGlobalDefs[key] = &cp
	return &cp
}

// --- server helpers ----------------------------------------------------------------------------------

// FindElement returns the element with id in a tree (nil when absent).
func FindElement(list []*Element, id string) *Element {
	for _, el := range list {
		if el == nil {
			continue
		}
		if el.ID == id {
			return el
		}
		if f := FindElement(el.Elements, id); f != nil {
			return f
		}
	}
	return nil
}

// WidgetSettings mirrors get_settings_for_display for an element: saved settings with control
// defaults, without settings whose conditions fail (the forms handler reads a form this way).
func WidgetSettings(el *Element, rc *RenderContext) map[string]any {
	if el == nil {
		return nil
	}
	r := newRenderer(&Document{}, rc)
	r.resolveGlobals([]*Element{el})
	e := r.newElemCtx(el)
	if e == nil {
		return nil
	}
	return e.disp.toMap()
}

// RenderWidgetFront renders a widget's content for the front end (Widget_Base::render_content
// outside the editor), as the loop filter's refresh-loop route returns it.
func RenderWidgetFront(el *Element, doc *Document, rc *RenderContext) string {
	if el == nil || el.ElType != "widget" {
		return ""
	}
	r := newRenderer(doc, rc)
	r.resolveGlobals([]*Element{el})
	r.resolveDynamic(&Document{ID: doc.ID, Elements: []*Element{el}})
	e := r.newElemCtx(el)
	if e == nil || e.def == nil {
		return ""
	}
	return e.widgetContent()
}
