package server

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/plugins"
	"wpgo/internal/wp"
)

// Declarative fields of Wasm plugins (settings pages, meta boxes): one renderer for the form-table
// row of every field type and one parser for the posted value, so a settings page and a meta box
// treat a field the same way.

var pluginColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// pluginFieldRow renders a field as a WordPress form-table row (<tr><th>…</th><td>…</td></tr>).
// name is the input name (and id); v the stored value (nil = the field's default).
func pluginFieldRow(name string, f plugins.SettingsField, v any) string {
	e := html.EscapeString
	text, checked := settingValue(v, f)
	n, label := e(name), e(f.Label)
	desc := ""
	if f.Description != "" {
		desc = "\n<p class=\"description\">" + e(f.Description) + "</p>"
	}
	ph := ""
	if f.Placeholder != "" {
		ph = ` placeholder="` + e(f.Placeholder) + `"`
	}
	th := `<label for="` + n + `">` + label + `</label>`
	var td string
	switch f.Type {
	case "checkbox":
		th = label
		chk := ""
		if checked {
			chk = ` checked="checked"`
		}
		inner := label
		if f.Description != "" {
			inner = e(f.Description)
		}
		td = `<fieldset><legend class="screen-reader-text"><span>` + label + `</span></legend>` + "\n" +
			`<label for="` + n + `"><input name="` + n + `" type="checkbox" id="` + n + `" value="1"` + chk + ` /> ` + inner + `</label></fieldset>`
	case "radio":
		th = label
		var b strings.Builder
		b.WriteString(`<fieldset><legend class="screen-reader-text"><span>` + label + `</span></legend>`)
		for i, o := range f.Options {
			chk := ""
			if o.Value == text {
				chk = ` checked="checked"`
			}
			if i > 0 {
				b.WriteString("<br />")
			}
			b.WriteString("\n" + `<label><input type="radio" name="` + n + `" value="` + e(o.Value) + `"` + chk + ` /> <span>` + e(o.Label) + `</span></label>`)
		}
		b.WriteString("</fieldset>")
		td = b.String() + desc
	case "textarea":
		rows := f.Rows
		if rows <= 0 {
			rows = 5
		}
		td = `<textarea name="` + n + `" id="` + n + `" rows="` + strconv.Itoa(rows) + `" cols="50" class="large-text code"` + ph + `>` + e(text) + `</textarea>` + desc
	case "number":
		mm := ""
		if f.Min != nil {
			mm += ` min="` + strconv.FormatFloat(*f.Min, 'f', -1, 64) + `"`
		}
		if f.Max != nil {
			mm += ` max="` + strconv.FormatFloat(*f.Max, 'f', -1, 64) + `"`
		}
		td = `<input name="` + n + `" type="number" id="` + n + `" value="` + e(text) + `" class="small-text"` + mm + ` step="any"` + ph + ` />` + desc
	case "select":
		var b strings.Builder
		b.WriteString(`<select name="` + n + `" id="` + n + `">`)
		for _, o := range f.Options {
			sel := ""
			if o.Value == text {
				sel = ` selected="selected"`
			}
			b.WriteString(`<option value="` + e(o.Value) + `"` + sel + `>` + e(o.Label) + `</option>`)
		}
		b.WriteString("</select>")
		td = b.String() + desc
	default: // text, email, url, password, color, date
		typ, class := f.Type, ` class="regular-text"`
		switch typ {
		case "password":
			text = "" // never printed back; an empty input keeps the stored value
			ph = ` autocomplete="new-password"` + ph
		case "color", "date":
			class = ""
		case "email", "url":
			class = ` class="regular-text code"`
		default:
			typ = "text"
		}
		td = `<input name="` + n + `" type="` + typ + `" id="` + n + `" value="` + e(text) + `"` + class + ph + ` />` + desc
	}
	return "<tr>\n<th scope=\"row\">" + th + "</th>\n<td>\n" + td + "\n</td></tr>\n"
}

// pluginFieldValue parses the posted value of a field. ok is false when nothing should be stored
// (absent, invalid, or an empty password, which keeps the stored one).
func pluginFieldValue(f plugins.SettingsField, raw string, present bool) (any, bool) {
	if f.Type == "checkbox" {
		return present && raw != "" && raw != "0", true
	}
	if !present {
		return nil, false
	}
	switch f.Type {
	case "number":
		n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, false
		}
		if f.Min != nil && n < *f.Min {
			n = *f.Min
		}
		if f.Max != nil && n > *f.Max {
			n = *f.Max
		}
		return n, true
	case "select", "radio":
		for _, o := range f.Options {
			if o.Value == raw {
				return raw, true
			}
		}
		return nil, false
	case "email":
		raw = strings.TrimSpace(raw)
		if raw != "" && (len(raw) > 254 || !validEmail(raw)) {
			return nil, false
		}
		return raw, true
	case "url":
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", true
		}
		clean := wp.CleanURL(raw, "http", "https")
		if clean == "" || len(clean) > 2048 {
			return nil, false
		}
		return clean, true
	case "color":
		raw = strings.TrimSpace(raw)
		if raw != "" && !pluginColorRe.MatchString(raw) {
			return nil, false
		}
		return raw, true
	case "date":
		raw = strings.TrimSpace(raw)
		if raw != "" {
			if _, err := time.Parse("2006-01-02", raw); err != nil {
				return nil, false
			}
		}
		return raw, true
	case "password":
		if raw == "" {
			return nil, false
		}
	case "text":
		raw = strings.NewReplacer("\r", "", "\n", " ").Replace(raw)
	}
	if len(raw) > 64<<10 {
		raw = raw[:64<<10]
	}
	return strings.ReplaceAll(raw, "\r\n", "\n"), true
}
