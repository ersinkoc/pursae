package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Field inputs are rendered server-side with ACF's markup and class names (acf-field, acf-label,
// acf-input, acf-repeater, …) so acfgo.css and acfgo-input.js can mirror ACF's look and behaviour.
// Inputs are named like ACF's: acf[field_key], acf[field_rep][row-0][field_sub]; repeater and
// flexible content templates sit in <template> elements, which never submit.

type acfgoRender struct {
	s       *Server
	ctx     context.Context
	sb      strings.Builder
	media   bool // an image/file/gallery field needs the media modal
	editor  bool // a wysiwyg field needs the editor scripts
	postID  int64
	inTable bool // a repeater cell: no label row
}

func (r *acfgoRender) w(format string, args ...any) {
	if len(args) == 0 {
		r.sb.WriteString(format)
		return
	}
	fmt.Fprintf(&r.sb, format, args...)
}

// fields renders a list of fields whose values live in vals (by field name; nil = new object).
func (r *acfgoRender) fields(fs []acfField, prefix string, vals map[string]any, labelPlacement, instr string) {
	for _, f := range fs {
		if acfgoStr(f["type"]) == "clone" && acfgoStr(f["display"]) != "group" {
			// Seamless: the cloned fields render in place, their values in the same row.
			sub := prefix + "[" + esc(acfgoStr(f["key"])) + "]"
			r.fields(r.s.acfgoCloneFields(r.ctx, f), sub, vals, labelPlacement, instr)
			continue
		}
		v, has := vals[acfgoStr(f["name"])]
		if !has {
			v = f["default_value"]
		}
		r.field(f, prefix+"["+esc(acfgoStr(f["key"]))+"]", v, labelPlacement, instr)
	}
}

// field renders one field with its wrapper (acf_render_field_wrap()).
func (r *acfgoRender) field(f acfField, name string, v any, labelPlacement, instr string) {
	typ := acfgoStr(f["type"])
	key := acfgoStr(f["key"])
	wrap, _ := f["wrapper"].(map[string]any)
	classes := "acf-field acf-field-" + strings.ReplaceAll(typ, "_", "-") + " acf-field-" + strings.ReplaceAll(key, "_", "-")
	if c := acfgoStr(wrap["class"]); c != "" {
		classes += " " + c
	}
	if acfgoBool(f["required"]) {
		classes += " is-required"
	}
	attrs := fmt.Sprintf(` class="%s" data-name="%s" data-type="%s" data-key="%s"`, esc(classes), esc(acfgoStr(f["name"])), esc(typ), esc(key))
	if id := acfgoStr(wrap["id"]); id != "" {
		attrs += ` id="` + esc(id) + `"`
	}
	if width := acfgoStr(wrap["width"]); width != "" && width != "0" && !r.inTable {
		attrs += ` data-width="` + esc(width) + `" style="width:` + esc(width) + `%"`
	}
	if acfgoBool(f["required"]) {
		attrs += ` data-required="1"`
	}
	if cl := f["conditional_logic"]; cl != nil && acfgoList(cl) != nil {
		b, _ := json.Marshal(cl)
		attrs += ` data-conditions="` + esc(string(b)) + `"`
	}
	switch typ {
	case "tab":
		r.w(`<div%s data-placement="%s" data-endpoint="%d" data-selected="%d"><div class="acf-tab-label">%s</div></div>`, attrs,
			esc(defaultStr(acfgoStr(f["placement"]), "top")), boolInt(acfgoBool(f["endpoint"])), boolInt(acfgoBool(f["selected"])), esc(acfgoStr(f["label"])))
		return
	case "accordion":
		r.w(`<div%s data-open="%d" data-multi-expand="%d" data-endpoint="%d"><div class="acf-accordion-title"><label>%s</label><span class="acf-accordion-icon dashicons dashicons-arrow-down"></span></div>`,
			attrs, boolInt(acfgoBool(f["open"])), boolInt(acfgoBool(f["multi_expand"])), boolInt(acfgoBool(f["endpoint"])), esc(acfgoStr(f["label"])))
		if ins := acfgoStr(f["instructions"]); ins != "" {
			r.w(`<p class="description">%s</p>`, esc(ins))
		}
		r.w(`</div>`)
		return
	}
	lp := "-" + defaultStr(labelPlacement, "top")
	r.w(`<div%s data-label-placement="%s">`, attrs, esc(lp))
	if !r.inTable {
		r.w(`<div class="acf-label"><label for="%s">%s`, esc(acfgoInputID(name)), esc(acfgoStr(f["label"])))
		if acfgoBool(f["required"]) {
			r.w(` <span class="acf-required">*</span>`)
		}
		r.w(`</label>`)
		if ins := acfgoStr(f["instructions"]); ins != "" && instr != "field" {
			if instr == "tooltip" {
				r.w(`<span class="acf-js-tooltip dashicons dashicons-editor-help" title="%s"></span>`, esc(ins))
			} else {
				r.w(`<p class="description">%s</p>`, esc(ins))
			}
		}
		r.w(`</div>`)
	}
	r.w(`<div class="acf-input">`)
	r.input(f, name, v)
	if ins := acfgoStr(f["instructions"]); ins != "" && (instr == "field" || r.inTable) {
		r.w(`<p class="description">%s</p>`, esc(ins))
	}
	r.w(`</div></div>`)
}

var acfgoIDClean = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func acfgoInputID(name string) string {
	return strings.Trim(acfgoIDClean.ReplaceAllString(name, "-"), "-")
}

func attrIf(cond bool, a string) string {
	if cond {
		return " " + a
	}
	return ""
}

// prepend/append wrappers of text-like inputs.
func (r *acfgoRender) wrapped(f acfField, input string) {
	pre, app := acfgoStr(f["prepend"]), acfgoStr(f["append"])
	if pre == "" && app == "" {
		r.sb.WriteString(input)
		return
	}
	r.w(`<div class="acf-input-wrap-outer">`)
	if pre != "" {
		r.w(`<div class="acf-input-prepend">%s</div>`, esc(pre))
	}
	r.w(`<div class="acf-input-wrap">%s</div>`, input)
	if app != "" {
		r.w(`<div class="acf-input-append">%s</div>`, esc(app))
	}
	r.w(`</div>`)
}

// input renders the control of one field type (render_field()).
func (r *acfgoRender) input(f acfField, name string, v any) {
	s, ctx := r.s, r.ctx
	typ := acfgoStr(f["type"])
	id := acfgoInputID(name)
	sv := acfgoStr(v)
	ph := ""
	if p := acfgoStr(f["placeholder"]); p != "" {
		ph = ` placeholder="` + esc(p) + `"`
	}
	req := attrIf(acfgoBool(f["required"]), `aria-required="true"`)
	switch typ {
	case "text", "email", "url", "password":
		it := map[string]string{"text": "text", "email": "email", "url": "url", "password": "password"}[typ]
		ml := ""
		if n := acfgoInt(f["maxlength"]); n > 0 {
			ml = fmt.Sprintf(` maxlength="%d"`, n)
		}
		r.wrapped(f, fmt.Sprintf(`<input type="%s" id="%s" name="%s" value="%s"%s%s%s />`, it, esc(id), name, esc(sv), ph, ml, req))
	case "textarea":
		rows := acfgoInt(f["rows"])
		if rows <= 0 {
			rows = 8
		}
		ml := ""
		if n := acfgoInt(f["maxlength"]); n > 0 {
			ml = fmt.Sprintf(` maxlength="%d"`, n)
		}
		r.w(`<textarea id="%s" name="%s" rows="%d"%s%s%s>%s</textarea>`, esc(id), name, rows, ph, ml, req, esc(sv))
	case "number":
		r.wrapped(f, fmt.Sprintf(`<input type="number" id="%s" name="%s" value="%s"%s%s%s%s%s />`, esc(id), name, esc(sv), ph,
			acfgoNumAttr("min", f["min"]), acfgoNumAttr("max", f["max"]), acfgoNumAttr("step", defaultAny(f["step"], "any")), req))
	case "range":
		min, max := defaultStr(acfgoStr(f["min"]), "0"), defaultStr(acfgoStr(f["max"]), "100")
		step := defaultStr(acfgoStr(f["step"]), "1")
		val := defaultStr(sv, min)
		r.w(`<div class="acf-range-wrap">`)
		if p := acfgoStr(f["prepend"]); p != "" {
			r.w(`<div class="acf-prepend">%s</div>`, esc(p))
		}
		r.w(`<input type="range" id="%s" name="%s" value="%s" min="%s" max="%s" step="%s" /><input type="number" class="acf-range-number" value="%s" min="%s" max="%s" step="%s" />`,
			esc(id), name, esc(val), esc(min), esc(max), esc(step), esc(val), esc(min), esc(max), esc(step))
		if a := acfgoStr(f["append"]); a != "" {
			r.w(`<div class="acf-append">%s</div>`, esc(a))
		}
		r.w(`</div>`)
	case "true_false":
		on := acfgoBool(v)
		r.w(`<input type="hidden" name="%s" value="0" />`, name)
		if acfgoBool(f["ui"]) {
			r.w(`<label class="acf-switch-label"><input type="checkbox" id="%s" name="%s" value="1" class="acf-switch-input"%s /><span class="acf-switch%s"><span class="acf-switch-on">%s</span><span class="acf-switch-off">%s</span><span class="acf-switch-slider"></span></span>`,
				esc(id), name, attrIf(on, "checked"), map[bool]string{true: " -on", false: ""}[on], esc(defaultStr(acfgoStr(f["ui_on_text"]), "Yes")), esc(defaultStr(acfgoStr(f["ui_off_text"]), "No")))
		} else {
			r.w(`<label><input type="checkbox" id="%s" name="%s" value="1"%s />`, esc(id), name, attrIf(on, "checked"))
		}
		if m := acfgoStr(f["message"]); m != "" {
			r.w(` <span class="message">%s</span>`, esc(m))
		}
		r.w(`</label>`)
	case "select":
		multi := acfgoBool(f["multiple"])
		sel := map[string]bool{}
		for _, x := range acfgoList(v) {
			sel[acfgoStr(x)] = true
		}
		nm := name
		if multi {
			nm += "[]"
			r.w(`<input type="hidden" name="%s" value="" />`, name)
		}
		r.w(`<select id="%s" name="%s"%s%s data-ui="%d">`, esc(id), nm, attrIf(multi, `multiple size="5"`), req, boolInt(acfgoBool(f["ui"])))
		if acfgoBool(f["allow_null"]) || (!multi && len(sel) == 0) {
			r.w(`<option value="">%s</option>`, esc(defaultStr(acfgoStr(f["placeholder"]), "- Select -")))
		}
		for _, c := range acfgoChoicesMap(f) {
			r.w(`<option value="%s"%s>%s</option>`, esc(c[0]), attrIf(sel[c[0]], "selected"), esc(c[1]))
		}
		r.w(`</select>`)
	case "checkbox":
		sel := map[string]bool{}
		for _, x := range acfgoList(v) {
			sel[acfgoStr(x)] = true
		}
		layout := defaultStr(acfgoStr(f["layout"]), "vertical")
		r.w(`<input type="hidden" name="%s" value="" /><ul class="acf-checkbox-list acf-bl -%s">`, name, esc(layout))
		if acfgoBool(f["toggle"]) {
			r.w(`<li><label><input type="checkbox" class="acf-checkbox-toggle" /> Toggle All</label></li>`)
		}
		known := map[string]bool{}
		for i, c := range acfgoChoicesMap(f) {
			known[c[0]] = true
			r.w(`<li><label%s><input type="checkbox" id="%s-%d" name="%s[]" value="%s"%s /> %s</label></li>`, attrIf(sel[c[0]], `class="selected"`), esc(id), i, name, esc(c[0]), attrIf(sel[c[0]], "checked"), esc(c[1]))
		}
		for _, x := range acfgoList(v) { // custom values
			if xs := acfgoStr(x); xs != "" && !known[xs] {
				r.w(`<li><input type="checkbox" checked value="%s" name="%s[]" /> <input type="text" class="acf-custom-value" value="%s" /></li>`, esc(xs), name, esc(xs))
			}
		}
		if acfgoBool(f["allow_custom"]) {
			r.w(`<li><a href="#" class="button acf-add-checkbox" data-name="%s[]">Add new choice</a></li>`, name)
		}
		r.w(`</ul>`)
	case "radio", "button_group":
		layout := defaultStr(acfgoStr(f["layout"]), map[string]string{"radio": "vertical", "button_group": "horizontal"}[typ])
		cls := "acf-radio-list acf-bl"
		if typ == "button_group" {
			cls = "acf-button-group"
		}
		r.w(`<ul class="%s -%s" data-allow-null="%d">`, cls, esc(layout), boolInt(acfgoBool(f["allow_null"])))
		known := false
		for i, c := range acfgoChoicesMap(f) {
			on := c[0] == sv
			known = known || on
			r.w(`<li><label%s><input type="radio" id="%s-%d" name="%s" value="%s"%s /> %s</label></li>`, attrIf(on, `class="selected"`), esc(id), i, name, esc(c[0]), attrIf(on, "checked"), esc(c[1]))
		}
		if typ == "radio" && acfgoBool(f["other_choice"]) {
			other := ""
			if !known {
				other = sv
			}
			r.w(`<li><label><input type="radio" class="acf-radio-other" name="%s" value="%s"%s /> Other</label> <input type="text" class="acf-radio-other-text" value="%s"%s /></li>`,
				name, esc(other), attrIf(other != "", "checked"), esc(other), attrIf(other == "", `style="display:none"`))
		}
		r.w(`</ul>`)
	case "image", "file", "gallery":
		r.media = true
		r.mediaInput(f, name, v)
	case "wysiwyg":
		r.editor = true
		r.w(`<div class="acf-editor-wrap" data-toolbar="%s" data-tabs="%s" data-media="%d"><textarea id="%s" class="wp-editor-area acfgo-wysiwyg" name="%s" rows="10">%s</textarea></div>`,
			esc(defaultStr(acfgoStr(f["toolbar"]), "full")), esc(defaultStr(acfgoStr(f["tabs"]), "all")), boolInt(f["media_upload"] == nil || acfgoBool(f["media_upload"])), esc(id), name, esc(sv))
	case "oembed":
		r.w(`<div class="acf-oembed"><input type="url" id="%s" name="%s" value="%s" placeholder="Enter URL" class="acf-oembed-url" /><div class="canvas-media">%s</div></div>`,
			esc(id), name, esc(sv), func() string {
				if sv == "" {
					return ""
				}
				return acfgoEmbedHTML(sv, int(acfgoInt(f["width"])), int(acfgoInt(f["height"])))
			}())
	case "link":
		m, _ := v.(map[string]any)
		r.w(`<div class="acf-link"><input type="url" name="%s[url]" id="%s" value="%s" placeholder="URL" class="acf-link-url" /> <input type="text" name="%s[title]" value="%s" placeholder="Link Text" class="acf-link-title" /> <label><input type="checkbox" name="%s[target]" value="_blank"%s /> Open in a new tab</label></div>`,
			name, esc(id), esc(acfgoStr(m["url"])), name, esc(acfgoStr(m["title"])), name, attrIf(acfgoStr(m["target"]) == "_blank", "checked"))
	case "post_object", "page_link", "user":
		multi := acfgoBool(f["multiple"])
		sel := map[string]bool{}
		for _, x := range acfgoList(v) {
			sel[acfgoStr(x)] = true
		}
		nm := name
		if multi {
			nm += "[]"
			r.w(`<input type="hidden" name="%s" value="" />`, name)
		}
		ajax := acfgoBool(f["ajax"])
		if ajax { // large sites: only the chosen values are rendered, the rest is searched while typing
			r.w(`<input type="search" class="acf-select-ajax-search" placeholder="Search..." autocomplete="off" data-key="%s" />`, esc(acfgoStr(f["key"])))
		}
		r.w(`<select id="%s" name="%s"%s class="acf-select-search"%s>`, esc(id), nm, attrIf(multi, `multiple size="6"`), attrIf(ajax, `data-ajax="1"`))
		if acfgoBool(f["allow_null"]) || !multi {
			r.w(`<option value="">- Select -</option>`)
		}
		if ajax {
			for _, x := range acfgoList(v) {
				id := acfgoInt(x)
				if id <= 0 {
					continue
				}
				if typ == "user" {
					if u := s.acfgoUserByID(ctx, id); u != nil {
						r.w(`<option value="%d" selected>%s</option>`, u.ID, esc(u.DisplayName+" ("+u.Login+")"))
					}
				} else if p, err := s.store.GetPost(ctx, id); err == nil {
					r.w(`<option value="%d" selected>%s</option>`, p.ID, esc(defaultStr(p.Title, "(no title)")))
				}
			}
		} else if typ == "user" {
			for _, u := range s.acfgoUserChoices(ctx, f, "") {
				r.w(`<option value="%d"%s>%s</option>`, u.ID, attrIf(sel[itoa64(u.ID)], "selected"), esc(u.DisplayName+" ("+u.Login+")"))
			}
		} else {
			cur := ""
			for _, p := range s.acfgoPostChoices(ctx, f, "", "") {
				if p.Type != cur && typ != "user" {
					if cur != "" {
						r.w(`</optgroup>`)
					}
					cur = p.Type
					r.w(`<optgroup label="%s">`, esc(acfgoTypeName(p.Type)))
				}
				r.w(`<option value="%d"%s>%s</option>`, p.ID, attrIf(sel[itoa64(p.ID)], "selected"), esc(defaultStr(p.Title, "(no title)")))
			}
			if cur != "" {
				r.w(`</optgroup>`)
			}
		}
		r.w(`</select>`)
	case "relationship":
		r.w(`<div class="acf-relationship" data-name="%s" data-key="%s" data-min="%d" data-max="%d"><input type="hidden" name="%s" value="" />`,
			name+"[]", esc(acfgoStr(f["key"])), acfgoInt(f["min"]), acfgoInt(f["max"]), name)
		filters := acfgoStrList(f["filters"])
		if f["filters"] == nil {
			filters = []string{"search", "post_type", "taxonomy"}
		}
		r.w(`<div class="filters">`)
		for _, fl := range filters {
			switch fl {
			case "search":
				r.w(`<input type="search" class="acf-rel-search" placeholder="Search..." />`)
			case "post_type":
				r.w(`<select class="acf-rel-type"><option value="">Select post type</option>`)
				for _, t := range acfgoAllowedTypes(f) {
					r.w(`<option value="%s">%s</option>`, esc(t), esc(acfgoTypeName(t)))
				}
				r.w(`</select>`)
			}
		}
		r.w(`</div><div class="selection"><div class="choices"><ul class="acf-bl list choices-list">`)
		chosen := map[int64]bool{}
		for _, x := range acfgoList(v) {
			chosen[acfgoInt(x)] = true
		}
		for _, p := range s.acfgoPostChoices(ctx, f, "", "") {
			r.w(`<li><span class="acf-rel-item%s" data-id="%d">%s</span></li>`, attrIf(chosen[p.ID], "disabled"), p.ID, s.acfgoRelItemLabel(ctx, f, p))
		}
		r.w(`</ul></div><div class="values"><ul class="acf-bl list values-list">`)
		for _, x := range acfgoList(v) {
			if p, err := s.store.GetPost(ctx, acfgoInt(x)); err == nil {
				r.w(`<li><input type="hidden" name="%s[]" value="%d" /><span class="acf-rel-item" data-id="%d">%s<a href="#" class="acf-icon -minus small dark" data-name="remove_item" title="Remove">&minus;</a></span></li>`,
					name, p.ID, p.ID, s.acfgoRelItemLabel(ctx, f, p))
			}
		}
		r.w(`</ul></div></div></div>`)
	case "taxonomy":
		tax := defaultStr(acfgoStr(f["taxonomy"]), "category")
		terms, _ := s.store.HierarchicalTerms(ctx, tax, false)
		sel := map[string]bool{}
		vals := acfgoList(v)
		if acfgoBool(f["load_terms"]) && r.postID > 0 {
			ts, _ := s.store.TermsForPosts(ctx, []int64{r.postID}, tax)
			vals = nil
			for _, t := range ts[r.postID] {
				vals = append(vals, itoa64(t.ID))
			}
		}
		for _, x := range vals {
			sel[acfgoStr(x)] = true
		}
		ft := defaultStr(acfgoStr(f["field_type"]), "checkbox")
		switch ft {
		case "checkbox", "radio":
			it := map[string]string{"checkbox": "checkbox", "radio": "radio"}[ft]
			nm := name
			if ft == "checkbox" {
				nm += "[]"
			}
			r.w(`<input type="hidden" name="%s" value="" /><div class="categorychecklist-holder"><ul class="acf-checkbox-list acf-bl">`, name)
			if ft == "radio" && acfgoBool(f["allow_null"]) {
				r.w(`<li><label><input type="radio" name="%s" value=""%s /> None</label></li>`, nm, attrIf(len(sel) == 0, "checked"))
			}
			for _, t := range terms {
				r.w(`<li style="margin-left:%dpx"><label><input type="%s" name="%s" value="%d"%s /> %s</label></li>`, t.Depth*18, it, nm, t.ID, attrIf(sel[itoa64(t.ID)], "checked"), esc(t.Name))
			}
			r.w(`</ul></div>`)
		default:
			multi := ft == "multi_select"
			nm := name
			if multi {
				nm += "[]"
				r.w(`<input type="hidden" name="%s" value="" />`, name)
			}
			r.w(`<select id="%s" name="%s"%s>`, esc(id), nm, attrIf(multi, `multiple size="6"`))
			if !multi {
				r.w(`<option value="">- Select -</option>`)
			}
			for _, t := range terms {
				r.w(`<option value="%d"%s>%s%s</option>`, t.ID, attrIf(sel[itoa64(t.ID)], "selected"), strings.Repeat("&nbsp;&nbsp;&nbsp;", t.Depth), esc(t.Name))
			}
			r.w(`</select>`)
		}
		if acfgoBool(f["add_term"]) {
			r.w(`<p class="acf-taxonomy-add"><input type="text" class="acf-new-term" placeholder="New term name" data-taxonomy="%s" /> <a href="#" class="button acf-add-term" data-name="%s">Add</a></p>`, esc(tax), esc(nameForMulti(ft, name)))
		}
	case "google_map":
		m, _ := v.(map[string]any)
		lat, lng := acfgoStr(m["lat"]), acfgoStr(m["lng"])
		if lat == "" {
			lat, lng = acfgoStr(f["center_lat"]), acfgoStr(f["center_lng"])
		}
		zoom := defaultStr(acfgoStr(m["zoom"]), defaultStr(acfgoStr(f["zoom"]), "14"))
		r.w(`<div class="acf-google-map" data-height="%s"><p><input type="text" name="%s[address]" id="%s" value="%s" placeholder="Search for address..." class="widefat search" /></p>`,
			esc(defaultStr(acfgoStr(f["height"]), "400")), name, esc(id), esc(acfgoStr(m["address"])))
		r.w(`<p class="acf-map-coords"><label>Lat <input type="text" name="%s[lat]" value="%s" class="small-text" /></label> <label>Lng <input type="text" name="%s[lng]" value="%s" class="small-text" /></label> <label>Zoom <input type="number" name="%s[zoom]" value="%s" class="small-text" min="1" max="21" /></label>`,
			name, esc(acfgoStr(m["lat"])), name, esc(acfgoStr(m["lng"])), name, esc(zoom))
		if lat != "" && lng != "" {
			r.w(` <a href="https://www.openstreetmap.org/?mlat=%s&amp;mlon=%s#map=%s/%s/%s" target="_blank" rel="noopener">View on map</a>`, esc(lat), esc(lng), esc(zoom), esc(lat), esc(lng))
		}
		r.w(`</p></div>`)
	case "date_picker":
		d := sv
		if len(d) == 8 && !strings.Contains(d, "-") {
			d = d[0:4] + "-" + d[4:6] + "-" + d[6:8]
		}
		r.w(`<div class="acf-date-picker"><input type="date" id="%s" name="%s" value="%s"%s /></div>`, esc(id), name, esc(d), req)
	case "date_time_picker":
		d := strings.Replace(sv, " ", "T", 1)
		if len(d) > 16 {
			d = d[:16]
		}
		r.w(`<div class="acf-date-time-picker"><input type="datetime-local" id="%s" name="%s" value="%s"%s /></div>`, esc(id), name, esc(d), req)
	case "time_picker":
		r.w(`<div class="acf-time-picker"><input type="time" step="1" id="%s" name="%s" value="%s"%s /></div>`, esc(id), name, esc(sv), req)
	case "color_picker":
		hex := sv
		if !strings.HasPrefix(hex, "#") || len(hex) != 7 {
			hex = "#000000"
		}
		r.w(`<div class="acf-color-picker"><input type="color" class="acf-color-swatch" value="%s" /> <input type="text" id="%s" name="%s" value="%s" class="acf-color-text" placeholder="#RRGGBB" data-alpha="%d" /></div>`,
			esc(hex), esc(id), name, esc(sv), boolInt(acfgoBool(f["enable_opacity"])))
	case "message":
		msg := acfgoStr(f["message"])
		if acfgoBool(f["esc_html"]) {
			msg = esc(msg)
		} else {
			msg = acfgoKses(msg)
		}
		switch acfgoStr(f["new_lines"]) {
		case "wpautop":
			msg = autopSafe(msg)
		case "br":
			msg = strings.ReplaceAll(msg, "\n", "<br />")
		}
		r.sb.WriteString(msg)
	case "icon_picker":
		m, _ := v.(map[string]any)
		cur := acfgoStr(m["value"])
		tabs := acfgoStrList(f["tabs"])
		if len(tabs) == 0 {
			tabs = []string{"dashicons", "media_library", "url"}
		}
		r.w(`<div class="acf-icon-picker" data-name="%s"><input type="hidden" class="acf-icon-type" name="%s[type]" value="%s" /><input type="hidden" class="acf-icon-value" name="%s[value]" value="%s" />`,
			name, name, esc(defaultStr(acfgoStr(m["type"]), "dashicons")), name, esc(cur))
		r.w(`<div class="acf-icon-preview">`)
		if acfgoStr(m["type"]) == "dashicons" && cur != "" {
			r.w(`<span class="dashicons %s"></span>`, esc(cur))
		} else if cur != "" {
			r.w(`<img src="%s" alt="" />`, esc(cur))
		}
		r.w(`</div><div class="acf-icon-tabs">`)
		for _, t := range tabs {
			r.w(`<a href="#" class="acf-icon-tab" data-tab="%s">%s</a>`, esc(t), esc(map[string]string{"dashicons": "Dashicons", "media_library": "Media Library", "url": "URL"}[t]))
		}
		r.w(`</div>`)
		for _, t := range tabs {
			switch t {
			case "dashicons":
				r.w(`<div class="acf-icon-panel" data-tab="dashicons"><input type="search" class="acf-icon-search" placeholder="Search icons" /><ul class="acf-dashicons-list">`)
				for _, ic := range s.acfgoDashicons() {
					r.w(`<li><a href="#" data-icon="%s" title="%s"%s><span class="dashicons %s"></span></a></li>`, ic, ic, attrIf(ic == cur, `class="active"`), ic)
				}
				r.w(`</ul></div>`)
			case "media_library":
				r.media = true
				r.w(`<div class="acf-icon-panel" data-tab="media_library" style="display:none"><a href="#" class="button acf-icon-media">Browse Media Library</a></div>`)
			case "url":
				r.w(`<div class="acf-icon-panel" data-tab="url" style="display:none"><input type="url" class="acf-icon-url widefat" placeholder="https://" value="%s" /></div>`, esc(map[bool]string{true: cur, false: ""}[acfgoStr(m["type"]) == "url"]))
			}
		}
		r.w(`</div>`)
	case "group":
		vals, _ := v.(map[string]any)
		layout := defaultStr(acfgoStr(f["layout"]), "block")
		r.w(`<div class="acf-fields -%s -border">`, map[string]string{"row": "left", "block": "top", "table": "top"}[layout])
		r.fields(acfgoSubFields(f), name, vals, map[string]string{"row": "left", "block": "top", "table": "top"}[layout], "label")
		r.w(`</div>`)
	case "clone": // display: group
		vals, _ := v.(map[string]any)
		layout := defaultStr(acfgoStr(f["layout"]), "block")
		r.w(`<div class="acf-fields -%s -border">`, map[string]string{"row": "left", "block": "top", "table": "top"}[layout])
		r.fields(s.acfgoCloneFields(ctx, f), name, vals, map[string]string{"row": "left", "block": "top", "table": "top"}[layout], "label")
		r.w(`</div>`)
	case "repeater":
		r.repeater(f, name, v)
	case "flexible_content":
		r.flexible(f, name, v)
	default:
		r.w(`<input type="text" id="%s" name="%s" value="%s" />`, esc(id), name, esc(sv))
	}
}

func nameForMulti(ft, name string) string {
	if ft == "checkbox" || ft == "multi_select" {
		return name + "[]"
	}
	return name
}

func defaultAny(v any, def string) any {
	if acfgoStr(v) == "" {
		return def
	}
	return v
}

func acfgoNumAttr(attr string, v any) string {
	if s := acfgoStr(v); s != "" {
		return " " + attr + `="` + esc(s) + `"`
	}
	return ""
}

// mediaInput renders image, file and gallery fields (the WordPress media modal picks).
func (r *acfgoRender) mediaInput(f acfField, name string, v any) {
	s, ctx := r.s, r.ctx
	typ := acfgoStr(f["type"])
	data := fmt.Sprintf(` data-library="%s" data-mime_types="%s" data-preview_size="%s" data-min="%d" data-max="%d" data-type="%s"`,
		esc(defaultStr(acfgoStr(f["library"]), "all")), esc(acfgoStr(f["mime_types"])), esc(defaultStr(acfgoStr(f["preview_size"]), "medium")),
		acfgoInt(f["min"]), acfgoInt(f["max"]), esc(typ))
	thumb := func(id int64) (string, string, string) {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return "", "", ""
		}
		url := s.uploadURL(ctx, a.FilePath)
		for _, vr := range a.Variants {
			if vr.Name == "thumbnail" || (vr.Name == "medium" && acfgoStr(f["preview_size"]) == "medium") {
				url = s.uploadURL(ctx, vr.FilePath)
			}
		}
		if !strings.HasPrefix(a.Mime, "image/") {
			url = mimeIcon(a.Mime)
		}
		return url, defaultStr(a.Title, a.Slug), s.uploadURL(ctx, a.FilePath)
	}
	switch typ {
	case "gallery":
		r.w(`<div class="acf-gallery"%s data-name="%s[]"><input type="hidden" name="%s" value="" /><div class="acf-gallery-attachments">`, data, name, name)
		for _, x := range acfgoList(v) {
			id := acfgoInt(x)
			if url, title, _ := thumb(id); url != "" {
				r.w(`<div class="acf-gallery-attachment" data-id="%d"><input type="hidden" name="%s[]" value="%d" /><img src="%s" alt="%s" title="%s" /><a href="#" class="acf-icon -cancel dark acf-gallery-remove" title="Remove">&times;</a></div>`,
					id, name, id, esc(url), esc(title), esc(title))
			}
		}
		r.w(`</div><div class="acf-gallery-toolbar"><a href="#" class="button button-primary acf-gallery-add">Add to gallery</a></div></div>`)
	default:
		id := acfgoInt(v)
		url, title, full := "", "", ""
		if id > 0 {
			url, title, full = thumb(id)
		}
		has := url != ""
		cls := "acf-image-uploader"
		if typ == "file" {
			cls = "acf-file-uploader"
		}
		r.w(`<div class="%s%s"%s><input type="hidden" name="%s" value="%s" class="acf-media-id" />`, cls, map[bool]string{true: " has-value", false: ""}[has], data, name, map[bool]string{true: itoa64(id), false: ""}[has])
		r.w(`<div class="show-if-value acf-media-preview">`)
		if typ == "image" {
			r.w(`<img src="%s" alt="" class="acf-media-thumb" />`, esc(url))
		} else {
			r.w(`<img src="%s" alt="" class="acf-media-thumb" /> <strong class="acf-media-title">%s</strong> <a href="%s" class="acf-media-link" target="_blank">%s</a>`, esc(url), esc(title), esc(full), esc(lastPathPart(full)))
		}
		r.w(` <a href="#" class="acf-icon -pencil dark acf-media-edit" title="Edit">Edit</a> <a href="#" class="acf-icon -cancel dark acf-media-remove" title="Remove">Remove</a></div>`)
		r.w(`<div class="hide-if-value"><p>%s <a href="#" class="button acf-media-add">%s</a></p></div></div>`,
			map[bool]string{true: "No image selected", false: "No file selected"}[typ == "image"], map[bool]string{true: "Add Image", false: "Add File"}[typ == "image"])
	}
}

func lastPathPart(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// repeater renders rows with the table, block or row layout and a <template> for new rows.
func (r *acfgoRender) repeater(f acfField, name string, v any) {
	subs := acfgoSubFields(f)
	layout := defaultStr(acfgoStr(f["layout"]), "table")
	rows := acfgoList(v)
	r.w(`<div class="acf-repeater -%s" data-min="%d" data-max="%d" data-collapsed="%s" data-name="%s" data-placeholder="acfcloneindex-%s"%s><input type="hidden" name="%s" value="" />`,
		esc(layout), acfgoInt(f["min"]), acfgoInt(f["max"]), esc(acfgoStr(f["collapsed"])), name, acfgoInputID(acfgoStr(f["key"])), acfgoRepeaterPageAttrs(f), name)
	r.w(`<table class="acf-table"><thead><tr><th class="acf-row-handle"></th>`)
	if layout == "table" {
		for _, sf := range subs {
			if acfgoStr(sf["type"]) == "tab" || acfgoStr(sf["type"]) == "accordion" {
				continue
			}
			w := ""
			if wr, _ := sf["wrapper"].(map[string]any); acfgoStr(wr["width"]) != "" {
				w = ` style="width:` + esc(acfgoStr(wr["width"])) + `%"`
			}
			r.w(`<th class="acf-th" data-key="%s"%s>%s%s`, esc(acfgoStr(sf["key"])), w, esc(acfgoStr(sf["label"])), map[bool]string{true: ` <span class="acf-required">*</span>`, false: ""}[acfgoBool(sf["required"])])
			if ins := acfgoStr(sf["instructions"]); ins != "" {
				r.w(`<p class="description">%s</p>`, esc(ins))
			}
			r.w(`</th>`)
		}
	} else {
		r.w(`<th class="acf-th"></th>`)
	}
	r.w(`<th class="acf-row-handle"></th></tr></thead><tbody>`)
	for i, row := range rows {
		vals, _ := row.(map[string]any)
		r.repeaterRow(f, subs, layout, name, "row-"+strconv.Itoa(i), i, vals)
	}
	r.w(`</tbody></table>`)
	r.w(`<template class="acf-clone-tpl">`)
	r.repeaterRow(f, subs, layout, name, "acfcloneindex-"+acfgoInputID(acfgoStr(f["key"])), 0, nil)
	r.w(`</template>`)
	r.w(`<div class="acf-actions"><a class="acf-button button button-primary acf-repeater-add" href="#">%s</a>`, esc(defaultStr(acfgoStr(f["button_label"]), "Add Row")))
	if acfgoBool(f["pagination"]) {
		r.w(`<span class="acf-tablenav tablenav-pages acf-repeater-pagination"><a href="#" class="acf-repeater-prev button" aria-label="Previous page">&lsaquo;</a> <span class="acf-repeater-page-info"></span> <a href="#" class="acf-repeater-next button" aria-label="Next page">&rsaquo;</a></span>`)
	}
	r.w(`</div></div>`)
}

// acfgoRepeaterPageAttrs are the data attributes that switch a repeater's row pagination on: the
// rows of the other pages stay in the form (hidden by acfgo-input.js) so every row is saved.
func acfgoRepeaterPageAttrs(f acfField) string {
	if !acfgoBool(f["pagination"]) {
		return ""
	}
	per := acfgoInt(f["rows_per_page"])
	if per < 1 {
		per = 20
	}
	return fmt.Sprintf(` data-pagination="1" data-per-page="%d"`, per)
}

func (r *acfgoRender) repeaterRow(f acfField, subs []acfField, layout, name, rowID string, i int, vals map[string]any) {
	prefix := name + "[" + rowID + "]"
	r.w(`<tr class="acf-row" data-id="%s"><td class="acf-row-handle order" title="Drag to reorder"><span class="acf-row-number">%d</span><input type="hidden" class="acf-row-order" name="%s[_order]" value="%d" /></td>`, rowID, i+1, prefix, i)
	if layout == "table" {
		old := r.inTable
		r.inTable = true
		for _, sf := range subs {
			if acfgoStr(sf["type"]) == "tab" || acfgoStr(sf["type"]) == "accordion" {
				continue
			}
			r.w(`<td class="acf-td">`)
			r.fields([]acfField{sf}, prefix, valsOrEmpty(vals, rowID), "top", "label")
			r.w(`</td>`)
		}
		r.inTable = old
	} else {
		r.w(`<td class="acf-fields -%s">`, map[string]string{"row": "left", "block": "top"}[layout])
		r.fields(subs, prefix, valsOrEmpty(vals, rowID), map[string]string{"row": "left", "block": "top"}[layout], "label")
		r.w(`</td>`)
	}
	r.w(`<td class="acf-row-handle remove"><a class="acf-icon -plus small acf-row-add" href="#" title="Add row">+</a><a class="acf-icon -duplicate small acf-row-duplicate" href="#" title="Duplicate row">&#10697;</a><a class="acf-icon -minus small acf-row-remove" href="#" title="Remove row">&minus;</a><a class="acf-icon -collapse small acf-row-collapse" href="#" title="Click to toggle">&#9662;</a></td></tr>`)
}

// valsOrEmpty gives new rows (the template) an empty value map so defaults apply.
func valsOrEmpty(vals map[string]any, rowID string) map[string]any {
	if vals == nil {
		return map[string]any{}
	}
	return vals
}

// flexible renders flexible content: the rows, a template per layout and the layout picker.
func (r *acfgoRender) flexible(f acfField, name string, v any) {
	layouts := acfgoList(f["layouts"])
	r.w(`<div class="acf-flexible-content" data-min="%d" data-max="%d" data-name="%s" data-placeholder="acfcloneindex-%s"><input type="hidden" name="%s" value="" />`, acfgoInt(f["min"]), acfgoInt(f["max"]), name, acfgoInputID(acfgoStr(f["key"])), name)
	rows := acfgoList(v)
	r.w(`<div class="no-value-message"%s>Click the "%s" button below to start creating your layout</div><div class="values">`,
		attrIf(len(rows) > 0, `style="display:none"`), esc(defaultStr(acfgoStr(f["button_label"]), "Add Row")))
	for i, row := range rows {
		vals, _ := row.(map[string]any)
		l := acfgoLayout(f, acfgoStr(vals["acf_fc_layout"]))
		if l == nil {
			continue
		}
		r.layout(l, name, "row-"+strconv.Itoa(i), i, vals)
	}
	r.w(`</div><div class="clones">`)
	for _, l := range layouts {
		if lm, ok := l.(map[string]any); ok {
			r.w(`<template class="acf-layout-tpl" data-layout="%s">`, esc(acfgoStr(lm["name"])))
			r.layout(lm, name, "acfcloneindex-"+acfgoInputID(acfgoStr(f["key"])), 0, map[string]any{})
			r.w(`</template>`)
		}
	}
	r.w(`</div><div class="acf-actions"><a class="acf-button button button-primary acf-fc-add" href="#">%s</a><ul class="acf-fc-popup" style="display:none">`, esc(defaultStr(acfgoStr(f["button_label"]), "Add Row")))
	for _, l := range layouts {
		if lm, ok := l.(map[string]any); ok {
			r.w(`<li><a href="#" data-layout="%s" data-min="%d" data-max="%d">%s</a></li>`, esc(acfgoStr(lm["name"])), acfgoInt(lm["min"]), acfgoInt(lm["max"]), esc(acfgoStr(lm["label"])))
		}
	}
	r.w(`</ul></div></div>`)
}

func (r *acfgoRender) layout(l map[string]any, name, rowID string, i int, vals map[string]any) {
	prefix := name + "[" + rowID + "]"
	display := defaultStr(acfgoStr(l["display"]), "block")
	r.w(`<div class="layout" data-id="%s" data-layout="%s"><input type="hidden" name="%s[acf_fc_layout]" value="%s" /><input type="hidden" class="acf-row-order" name="%s[_order]" value="%d" />`,
		rowID, esc(acfgoStr(l["name"])), prefix, esc(acfgoStr(l["name"])), prefix, i)
	r.w(`<div class="acf-fc-layout-handle" title="Drag to reorder"><span class="acf-fc-layout-order">%d</span> %s</div>`, i+1, esc(acfgoStr(l["label"])))
	r.w(`<div class="acf-fc-layout-controls"><a class="acf-icon -plus small light acf-fc-insert" href="#" title="Add layout">+</a><a class="acf-icon -duplicate small light acf-fc-duplicate" href="#" title="Duplicate layout">&#10697;</a><a class="acf-icon -minus small light acf-fc-remove" href="#" title="Remove layout">&minus;</a><a class="acf-icon -collapse small acf-fc-collapse" href="#" title="Click to toggle">&#9662;</a></div>`)
	subs := acfgoLayoutSubs(l)
	if display == "table" {
		old := r.inTable
		r.w(`<table class="acf-table"><thead><tr>`)
		for _, sf := range subs {
			r.w(`<th class="acf-th">%s</th>`, esc(acfgoStr(sf["label"])))
		}
		r.w(`</tr></thead><tbody><tr class="acf-row">`)
		r.inTable = true
		for _, sf := range subs {
			r.w(`<td class="acf-td">`)
			r.fields([]acfField{sf}, prefix, vals, "top", "label")
			r.w(`</td>`)
		}
		r.inTable = old
		r.w(`</tr></tbody></table>`)
	} else {
		lp := map[string]string{"row": "left", "block": "top"}[display]
		r.w(`<div class="acf-fields -%s">`, lp)
		r.fields(subs, prefix, vals, lp, "label")
		r.w(`</div>`)
	}
	r.w(`</div>`)
}

// Choices of relational fields ------------------------------------------------------------------

func acfgoAllowedTypes(f acfField) []string {
	types := acfgoStrList(f["post_type"])
	if len(types) == 0 {
		types = []string{"post", "page"}
		acfgoRegistry.RLock()
		for t := range acfgoRegistry.types {
			types = append(types, t)
		}
		acfgoRegistry.RUnlock()
		sort.Strings(types[2:])
	}
	return types
}

func acfgoTypeName(t string) string {
	switch t {
	case "post":
		return "Posts"
	case "page":
		return "Pages"
	case "attachment":
		return "Media"
	}
	if l, ok := acfgoTypeLabels(t); ok {
		return l.Name
	}
	return t
}

// acfgoPostChoices lists the posts a post object / relationship field may pick (its post type,
// status and taxonomy filters), optionally searched.
func (s *Server) acfgoPostChoices(ctx context.Context, f acfField, search string, typeFilter string) []*store.Post {
	types := acfgoAllowedTypes(f)
	if typeFilter != "" {
		types = []string{typeFilter}
	}
	statuses := acfgoStrList(f["post_status"])
	if len(statuses) == 0 {
		statuses = []string{"publish", "private", "draft", "pending", "future"}
	}
	var out []*store.Post
	for _, t := range types {
		ps, _, _ := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{t}, Statuses: statuses, Search: search, PerPage: 200, OrderBy: "title", Order: "asc"})
		out = append(out, ps...)
	}
	if taxes := acfgoStrList(f["taxonomy"]); len(taxes) > 0 && len(out) > 0 {
		ids := make([]int64, len(out))
		for i, p := range out {
			ids[i] = p.ID
		}
		keep := map[int64]bool{}
		for _, tv := range taxes { // "category:news"
			tax, slug, _ := strings.Cut(tv, ":")
			terms, _ := s.store.TermsForPosts(ctx, ids, tax)
			for pid, ts := range terms {
				for _, t := range ts {
					if t.Slug == slug {
						keep[pid] = true
					}
				}
			}
		}
		filtered := out[:0]
		for _, p := range out {
			if keep[p.ID] {
				filtered = append(filtered, p)
			}
		}
		out = filtered
	}
	return out
}

func (s *Server) acfgoRelItemLabel(ctx context.Context, f acfField, p *store.Post) string {
	label := esc(defaultStr(p.Title, "(no title)"))
	for _, el := range acfgoStrList(f["elements"]) {
		if el == "featured_image" && p.FeaturedMediaID > 0 {
			if a, err := s.store.GetAttachment(ctx, p.FeaturedMediaID); err == nil {
				label = `<span class="thumbnail"><img src="` + esc(s.uploadURL(ctx, a.FilePath)) + `" alt="" /></span>` + label
			}
		}
	}
	if p.Status != "publish" {
		label += ` <span class="acf-rel-status">(` + esc(p.Status) + `)</span>`
	}
	return label
}

func (s *Server) acfgoUserChoices(ctx context.Context, f acfField, search string) []*store.User {
	roles := acfgoStrList(f["role"])
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(search)) + "%"
	rows, err := s.store.Pool.Query(ctx, `SELECT u.id FROM users u WHERE $1 = '%%' OR u.display_name ILIKE $1 OR u.login ILIKE $1 ORDER BY u.display_name LIMIT 500`, like)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []*store.User
	for _, id := range ids {
		u := s.acfgoUserByID(ctx, id)
		if u == nil {
			continue
		}
		if len(roles) > 0 {
			ok := false
			for _, r := range roles {
				for _, ur := range u.Roles {
					ok = ok || r == ur
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, u)
	}
	return out
}

var (
	acfgoDashiconsOnce sync.Once
	acfgoDashiconsList []string
	acfgoDashiconRe    = regexp.MustCompile(`\.(dashicons-[a-z0-9-]+):before`)
)

// acfgoDashicons lists the dashicons of the admin's dashicons.css (the Icon Picker's library).
func (s *Server) acfgoDashicons() []string {
	acfgoDashiconsOnce.Do(func() {
		b, err := fs.ReadFile(s.web, "admin/assets/css/dashicons.css")
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, m := range acfgoDashiconRe.FindAllStringSubmatch(string(b), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				acfgoDashiconsList = append(acfgoDashiconsList, m[1])
			}
		}
	})
	return acfgoDashiconsList
}

// acfgoKses keeps the safe HTML of message fields (the same allow-list as post content).
func acfgoKses(v string) string { return wp.KsesPost(v) }

// Boxes ------------------------------------------------------------------------------------------

// acfgoBoxes renders the field groups of a screen. kind is post (postboxes for the classic and
// block editors), form (rows in a form-table: users, terms, comments) or options.
func (s *Server) acfgoBoxes(ctx context.Context, groups []*acfGroup, o *acfObject, kind string, postID int64) (string, bool, bool) {
	r := &acfgoRender{s: s, ctx: ctx, postID: postID}
	for _, g := range groups {
		vals := map[string]any{}
		if o != nil {
			for _, f := range g.Fields {
				s.acfgoLoadInto(ctx, o, vals, f, "", false)
				if acfgoStr(f["type"]) == "clone" && acfgoStr(f["display"]) != "group" {
					continue
				}
				if _, stored := s.acfgoGet(o, acfgoStr(f["name"])); !stored && acfgoStr(f["type"]) != "repeater" && acfgoStr(f["type"]) != "flexible_content" && acfgoStr(f["type"]) != "group" {
					delete(vals, acfgoStr(f["name"])) // the default applies
				}
			}
		}
		pos := g.Position
		if pos == "" {
			pos = "normal"
		}
		hide, _ := json.Marshal(g.HideOnScreen)
		switch kind {
		case "post", "options":
			cls := "postbox acf-postbox"
			if g.Style == "seamless" {
				cls += " seamless"
			}
			r.w(`<div id="acf-%s" class="%s" data-key="%s" data-position="%s" data-hide="%s"><div class="postbox-header"><h2 class="hndle">%s</h2>`,
				esc(g.Key), cls, esc(g.Key), esc(pos), esc(string(hide)), esc(g.Title))
			r.w(`<div class="handle-actions hide-if-no-js"><button type="button" class="handlediv" aria-expanded="true"><span class="screen-reader-text">Toggle panel: %s</span><span class="toggle-indicator" aria-hidden="true"></span></button></div></div>`, esc(g.Title))
			r.w(`<div class="inside acf-fields -%s">`, esc(defaultStr(g.LabelPlacement, "top")))
			r.fields(g.Fields, "acf", vals, g.LabelPlacement, g.InstructionPlacement)
			r.w(`</div></div>`)
		default:
			r.w(`<div class="acf-form-group" id="acf-%s" data-key="%s">`, esc(g.Key), esc(g.Key))
			if g.Style != "seamless" {
				r.w(`<h2>%s</h2>`, esc(g.Title))
			}
			r.w(`<div class="acf-fields -%s acf-form-fields">`, esc(defaultStr(g.LabelPlacement, "left")))
			r.fields(g.Fields, "acf", vals, defaultStr(g.LabelPlacement, "left"), g.InstructionPlacement)
			r.w(`</div></div>`)
		}
	}
	return r.sb.String(), r.media, r.editor
}

// autopSafe is wpautop for markup that is already safe.
func autopSafe(v string) string { return wp.Autop(v) }
