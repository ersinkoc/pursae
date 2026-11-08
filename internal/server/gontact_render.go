package server

import (
	"context"
	"encoding/json"
	"html"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Front end of Gontact Form: the [contact-form-7] shortcode and WPCF7_ContactForm::form_html() with
// the markup of every form-tag handler, so CF7 themes and CSS apply unchanged.

// gontactShortcodes expands [contact-form-7] and [contact-form] in rendered content (CF7 registers
// them with add_shortcode, so they run in do_shortcode at the_content priority 11).
func (s *Server) gontactShortcodes(ctx context.Context, content string) string {
	if !s.gontactOn() || !strings.Contains(content, "[contact-form") {
		return content
	}
	// wptexturize() leaves registered shortcodes alone; WordPress Go's Texturize runs before this
	// pass, so the curled quotes of the shortcode's attributes are straightened again.
	content = gfShortcodeQuotesRe.ReplaceAllStringFunc(content, func(m string) string {
		return gfQuoteFix.Replace(m)
	})
	return wp.DoShortcode(content, []string{"contact-form-7", "contact-form"}, func(tag string, attrs map[string]string, _ string) string {
		return s.gontactShortcode(ctx, attrs)
	})
}

var (
	gfShortcodeQuotesRe = regexp.MustCompile(`\[contact-form(?:-7)?\s[^\]]*\]`)
	gfQuoteFix          = strings.NewReplacer("&#8220;", `"`, "&#8221;", `"`, "&#8243;", `"`, "&#8216;", "'", "&#8217;", "'", "“", `"`, "”", `"`, "″", `"`, "‘", "'", "’", "'")
)

// gontactShortcode is wpcf7_contact_form_tag_func().
func (s *Server) gontactShortcode(ctx context.Context, attrs map[string]string) string {
	id := attrs["id"]
	if id == "" {
		id = attrs["0"]
	}
	f := s.gontactFormByRef(ctx, id, attrs["title"])
	if f == nil {
		return `[contact-form-7 404 "Not Found"]`
	}
	rc := renderFrom(ctx)
	r := &gfRender{s: s, f: f, ctx: ctx, atts: attrs}
	if rc != nil {
		r.c, r.user, r.postID = rc.c, rc.user, rc.postID
	}
	if r.c != nil && r.user == nil {
		r.user = currentUser(r.c)
	}
	return r.formHTML()
}

// gfRender is the state of one form being rendered.
type gfRender struct {
	s         *Server
	f         *gfForm
	ctx       context.Context
	c         *gin.Context // nil outside a request
	user      *store.User
	postID    int64
	atts      map[string]string
	unitTag   string
	sub       *gfSubmission // this form was posted without JavaScript
	responses int
}

// unit returns the next wpcf7-f{id}-p{post}-o{n} unit tag of this request (generate_unit_tag()).
func (r *gfRender) nextUnitTag() string {
	n := 1
	if r.c != nil {
		if v, ok := r.c.Get("gontact.units"); ok {
			n = v.(int) + 1
		}
		r.c.Set("gontact.units", n)
	}
	tag := "wpcf7-f" + itoa64(r.f.ID)
	if r.postID > 0 {
		tag += "-p" + itoa64(r.postID)
	}
	return tag + "-o" + strconv.Itoa(n)
}

func (r *gfRender) formHTML() string {
	f := r.f
	if r.atts["output"] == "raw_form" {
		return `<pre class="wpcf7-raw-form"><code>` + html.EscapeString(f.Form) + `</code></pre>`
	}
	if f.isTrue("subscribers_only") && r.user == nil {
		return `<p class="wpcf7-subscribers-only">This contact form is available only for logged in users.</p>`
	}
	r.unitTag = r.nextUnitTag()
	if r.c != nil {
		if sub, ok := r.c.Get("gontact.posted"); ok {
			if p := sub.(*gfSubmission); p.form.ID == f.ID && p.unitTag == r.unitTag {
				r.sub = p
			}
		}
	}
	action := "/"
	if r.c != nil {
		action = r.c.Request.URL.RequestURI()
	}
	action = strings.SplitN(action, "#", 2)[0] + "#" + r.unitTag

	elements, tags := r.formElements()
	class := "wpcf7-form"
	status := "init"
	if r.sub != nil {
		status = gfStatusClass(r.sub.status)
	}
	class += " " + status
	if cl := r.atts["html_class"]; cl != "" {
		for _, c := range strings.Fields(cl) {
			class += " " + gfSanitizeClass(c)
		}
	}
	if f.demoMode() {
		class += " demo"
	}
	if f.isTrue("acceptance_as_validation") {
		class += " wpcf7-acceptance-as-validation"
	}
	title := r.atts["html_title"]
	if title == "" {
		title = "Contact form"
	}
	a := gfAtts{}
	a.add("action", action).add("method", "post").add("class", class)
	a.add("id", gfAttrClean(r.atts["html_id"])).add("name", gfAttrClean(r.atts["html_name"])).add("aria-label", title)
	for _, t := range tags {
		if t.BaseType == "file" {
			a.add("enctype", "multipart/form-data")
			break
		}
	}
	a.flag("novalidate", true).add("data-status", status)

	lang := strings.ReplaceAll(f.Locale, "_", "-")
	var b strings.Builder
	b.WriteString(r.s.gontactAssetTags(r.c))
	b.WriteString("\n<div class=\"wpcf7 no-js\" id=\"" + r.unitTag + "\" lang=\"" + html.EscapeString(lang) + "\" dir=\"ltr\" data-wpcf7-id=\"" + itoa64(f.ID) + "\">\n")
	b.WriteString(r.screenReaderResponse() + "\n")
	b.WriteString("<form" + a.String() + ">\n")
	b.WriteString(r.hiddenFields())
	b.WriteString(elements)
	if r.responses == 0 {
		b.WriteString(r.responseOutput())
	}
	b.WriteString("\n</form>\n</div>\n")
	return b.String()
}

// gfStatusClass is form_status_class_name().
func gfStatusClass(status string) string {
	switch status {
	case "init":
		return "init"
	case "validation_failed":
		return "invalid"
	case "acceptance_missing":
		return "unaccepted"
	case "spam":
		return "spam"
	case "aborted":
		return "aborted"
	case "mail_sent":
		return "sent"
	case "mail_failed":
		return "failed"
	}
	return "custom-" + gfNonAlnumLowerRe.ReplaceAllString(strings.ToLower(status), "-")
}

func (r *gfRender) hiddenFields() string {
	type kv struct{ k, v string }
	fields := []kv{{"_wpcf7", itoa64(r.f.ID)}, {"_wpcf7_version", gontactVersion}, {"_wpcf7_locale", r.f.Locale},
		{"_wpcf7_unit_tag", r.unitTag}, {"_wpcf7_container_post", itoa64(r.postID)}, {"_wpcf7_posted_data_hash", ""}}
	if r.user != nil && r.c != nil {
		fields = append(fields, kv{"_wpnonce", r.s.nonce(r.c, "wp_rest")})
	}
	if r.s.gontactRecaptchaKeys() != "" {
		fields = append(fields, kv{"_wpcf7_recaptcha_response", ""})
	}
	var b strings.Builder
	b.WriteString(`<fieldset class="hidden-fields-container">`)
	for _, f := range fields {
		b.WriteString(`<input type="hidden" name="` + f.k + `" value="` + html.EscapeString(f.v) + `" />`)
	}
	b.WriteString("\n</fieldset>\n")
	return b.String()
}

func (r *gfRender) screenReaderResponse() string {
	primary := ""
	var items []string
	if r.sub != nil {
		primary = r.sub.message
		for _, inv := range r.sub.invalid {
			item := html.EscapeString(inv.reason)
			if inv.idref != "" {
				item = `<a href="#` + html.EscapeString(inv.idref) + `">` + item + `</a>`
			}
			items = append(items, `<li id="`+r.errorID(inv.name)+`">`+item+`</li>`)
		}
	}
	return `<div class="screen-reader-response"><p role="status" aria-live="polite" aria-atomic="true">` + html.EscapeString(primary) +
		`</p> <ul>` + strings.Join(items, "\n") + `</ul></div>`
}

func (r *gfRender) responseOutput() string {
	r.responses++
	content := ""
	if r.sub != nil {
		content = r.sub.message
	}
	return `<div class="wpcf7-response-output" aria-hidden="true">` + html.EscapeString(content) + `</div>`
}

// errorID is wpcf7_get_validation_error_reference().
func (r *gfRender) errorID(name string) string {
	return gfNonIDRe.ReplaceAllString(r.unitTag+"-ve-"+name, "")
}

// validationError is the inline tip of a field that failed validation in a non-JS submission.
func (r *gfRender) validationError(name string) string {
	if r.sub == nil {
		return ""
	}
	for _, inv := range r.sub.invalid {
		if inv.name == name {
			return `<span class="wpcf7-not-valid-tip" aria-hidden="true">` + html.EscapeString(inv.reason) + `</span>`
		}
	}
	return ""
}

// hangover is wpcf7_get_hangover(): the value a non-JS submission posted, unless the mail went out.
func (r *gfRender) hangover(name string) ([]string, bool) {
	if r.sub == nil || r.sub.status == "mail_sent" {
		return nil, false
	}
	v, ok := r.sub.raw[name]
	return v, ok
}

// formElements is replace_all_form_tags() with wpcf7_autop(): tags become placeholders while the
// template gets its paragraphs and line breaks.
func (r *gfRender) formElements() (string, []*gfTag) {
	var parts []string
	var hidden []bool
	form, tags := gfScan(r.f.Form, func(t *gfTag) string {
		parts = append(parts, r.tagHTML(t))
		hidden = append(hidden, t.BaseType == "hidden")
		return "GFPH" + strconv.Itoa(len(parts)-1) + "X"
	})
	out := wp.Autop(form)
	for i := len(parts) - 1; i >= 0; i-- {
		ph := "GFPH" + strconv.Itoa(i) + "X"
		if hidden[i] {
			out = strings.ReplaceAll(out, "<p>"+ph+"</p>", ph)
		}
		out = strings.ReplaceAll(out, ph, parts[i])
	}
	return out, tags
}

// gfAtts is wpcf7_format_atts(): attributes in insertion order, empty ones dropped.
type gfAtts []([2]string)

func (a *gfAtts) add(k, v string) *gfAtts {
	if v != "" || k == "value" {
		*a = append(*a, [2]string{k, v})
	}
	return a
}

func (a *gfAtts) flag(k string, on bool) *gfAtts {
	if on {
		*a = append(*a, [2]string{k, k})
	}
	return a
}

func (a gfAtts) String() string {
	var b strings.Builder
	for _, kv := range a {
		b.WriteString(" " + kv[0] + `="` + html.EscapeString(kv[1]) + `"`)
	}
	return b.String()
}

// gfControlsClass is wpcf7_form_controls_class().
func gfControlsClass(typ string, extra ...string) string {
	classes := []string{"wpcf7-form-control", "wpcf7-" + strings.TrimSuffix(typ, "*")}
	if strings.HasSuffix(typ, "*") {
		classes = append(classes, "wpcf7-validates-as-required")
	}
	for _, e := range extra {
		if e != "" {
			classes = append(classes, e)
		}
	}
	return strings.Join(classes, " ")
}

func gfSanitizeClass(c string) string {
	return gfNonClassRe.ReplaceAllString(c, "")
}

func gfAttrClean(v string) string {
	return gfNonAttrRe.ReplaceAllString(v, "")
}

func (r *gfRender) wrap(t *gfTag, inner string) string {
	return `<span class="wpcf7-form-control-wrap" data-name="` + html.EscapeString(t.Name) + `">` + inner + r.validationError(t.Name) + `</span>`
}

// ariaState adds aria-required / aria-invalid / aria-describedby like the CF7 handlers.
func (r *gfRender) ariaState(a *gfAtts, t *gfTag, required bool) {
	if required {
		a.add("aria-required", "true")
	}
	if r.validationError(t.Name) != "" {
		a.add("aria-invalid", "true").add("aria-describedby", r.errorID(t.Name))
	} else {
		a.add("aria-invalid", "false")
	}
}

// defaultValues is get_default_option() for the request at hand.
func (r *gfRender) defaultValues(t *gfTag, multiple, shifted bool) []string {
	var out []string
	for _, opt := range t.option("default", "") {
		opt = strings.ToLower(opt)
		switch {
		case strings.HasPrefix(opt, "user_") && r.user != nil:
			if v := gfUserProp(r.user, opt); v != "" {
				out = append(out, v)
			}
		case opt == "post_meta" && r.postID > 0:
			if p, err := r.s.store.GetPost(r.ctx, r.postID); err == nil {
				if v, ok := p.Meta[t.Name].(string); ok && v != "" {
					out = append(out, v)
				}
			}
		case opt == "get" && r.c != nil:
			out = append(out, r.queryValues(r.c.QueryArray(t.Name), r.c.QueryArray(t.Name+"[]"))...)
		case opt == "post" && r.c != nil && r.c.Request.Method == "POST":
			out = append(out, r.queryValues(r.c.PostFormArray(t.Name), r.c.PostFormArray(t.Name+"[]"))...)
		case opt == "shortcode_attr":
			if v := r.atts[t.Name]; v != "" {
				out = append(out, v)
			}
		case gfDigitsRe.MatchString(opt):
			for _, n := range strings.Split(opt, "_") {
				i, err := strconv.Atoi(n)
				if err != nil {
					continue
				}
				if !shifted {
					i--
				}
				if i >= 0 && i < len(t.Values) {
					out = append(out, t.Values[i])
				}
			}
		}
		if !multiple && len(out) > 0 {
			return out[:1]
		}
	}
	return out
}

func (r *gfRender) queryValues(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, v := range l {
			if v = strings.TrimSpace(wp.StripTags(v)); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// gfUserProp reads user_login, user_email, user_url, user_display_name, user_nickname … of a user.
func gfUserProp(u *store.User, opt string) string {
	switch opt {
	case "user_login":
		return u.Login
	case "user_email":
		return u.Email
	case "user_url":
		return u.URL
	case "user_display_name":
		return u.DisplayName
	case "user_nicename":
		return u.Nicename
	}
	key := strings.TrimPrefix(opt, "user_")
	if u.Profile != nil {
		return u.Profile[key]
	}
	return ""
}

// singleValue is the value a text-like control starts with: hangover, default option or the first value.
func (r *gfRender) singleValue(t *gfTag, a *gfAtts, base string) string {
	value := base
	if t.has("placeholder") || t.has("watermark") {
		a.add("placeholder", value)
		value = ""
	}
	if d := r.defaultValues(t, false, false); len(d) > 0 {
		value = d[0]
	}
	if h, ok := r.hangover(t.Name); ok {
		value = strings.Join(h, "")
	}
	return value
}

func first(v []string) string {
	if len(v) > 0 {
		return v[0]
	}
	return ""
}

func (r *gfRender) tagHTML(t *gfTag) string {
	switch t.BaseType {
	case "text", "email", "url", "tel":
		return r.textHTML(t)
	case "textarea":
		return r.textareaHTML(t)
	case "number", "range":
		return r.numberHTML(t)
	case "date":
		return r.dateHTML(t)
	case "select":
		return r.selectHTML(t)
	case "checkbox", "radio":
		return r.checkboxHTML(t)
	case "acceptance":
		return r.acceptanceHTML(t)
	case "quiz":
		return r.quizHTML(t)
	case "captchac":
		return r.captchacHTML(t)
	case "captchar":
		return r.captcharHTML(t)
	case "file":
		return r.fileHTML(t)
	case "submit":
		return r.submitHTML(t)
	case "hidden":
		return r.hiddenHTML(t)
	case "count":
		return r.countHTML(t)
	case "response":
		return r.responseOutput()
	}
	return ""
}

func (r *gfRender) notValid(t *gfTag) string {
	if r.validationError(t.Name) != "" {
		return "wpcf7-not-valid"
	}
	return ""
}

func (r *gfRender) textHTML(t *gfTag) string {
	extra := "wpcf7-text"
	if t.BaseType != "text" {
		extra += " wpcf7-validates-as-" + t.BaseType
	}
	class := gfControlsClass(t.Type, extra, r.notValid(t))
	a := &gfAtts{}
	size, maxl, minl := t.sizeOption("40"), t.maxlengthOption("400"), t.minlengthOption()
	if gfAtoi(maxl) > 0 && gfAtoi(minl) > 0 && gfAtoi(maxl) < gfAtoi(minl) {
		maxl, minl = "", ""
	}
	a.add("size", size).add("maxlength", maxl).add("minlength", minl)
	a.add("class", t.classOption(class)).add("id", t.idOption()).add("list", t.option1("list", "id"))
	a.add("tabindex", t.option1("tabindex", "signed_int")).flag("readonly", t.has("readonly")).add("autocomplete", t.autocompleteOption())
	r.ariaState(a, t, t.required())
	value := r.singleValue(t, a, first(t.Values))
	a.add("value", value).add("type", t.BaseType).add("name", t.Name)
	return r.wrap(t, "<input"+a.String()+" />")
}

func (r *gfRender) textareaHTML(t *gfTag) string {
	class := gfControlsClass(t.Type, r.notValid(t))
	a := &gfAtts{}
	maxl, minl := t.maxlengthOption("2000"), t.minlengthOption()
	if gfAtoi(maxl) > 0 && gfAtoi(minl) > 0 && gfAtoi(maxl) < gfAtoi(minl) {
		maxl, minl = "", ""
	}
	a.add("cols", t.colsOption("40")).add("rows", t.rowsOption("10")).add("maxlength", maxl).add("minlength", minl)
	a.add("class", t.classOption(class)).add("id", t.idOption()).add("tabindex", t.option1("tabindex", "signed_int"))
	a.flag("readonly", t.has("readonly")).add("autocomplete", t.autocompleteOption())
	r.ariaState(a, t, t.required())
	base := first(t.Values)
	if t.Content != "" {
		base = t.Content
	}
	value := r.singleValue(t, a, base)
	a.add("name", t.Name)
	return r.wrap(t, "<textarea"+a.String()+">"+html.EscapeString(value)+"</textarea>")
}

func (r *gfRender) numberHTML(t *gfTag) string {
	class := gfControlsClass(t.Type, "wpcf7-validates-as-number", r.notValid(t))
	a := &gfAtts{}
	a.add("class", t.classOption(class)).add("id", t.idOption()).add("tabindex", t.option1("tabindex", "signed_int"))
	min, max := t.option1("min", "signed_num"), t.option1("max", "signed_num")
	if t.BaseType == "range" {
		if min == "" {
			min = "0"
		}
		if max == "" {
			max = "100"
		}
	}
	a.add("min", min).add("max", max).add("step", t.option1("step", "num"))
	a.flag("readonly", t.has("readonly")).add("autocomplete", t.autocompleteOption())
	r.ariaState(a, t, t.required())
	value := r.singleValue(t, a, first(t.Values))
	if t.BaseType == "range" && value == "" {
		lo, _ := strconv.ParseFloat(min, 64)
		hi, _ := strconv.ParseFloat(max, 64)
		if lo < hi {
			value = strconv.FormatFloat((lo+hi)/2, 'f', -1, 64)
		} else {
			value = min
		}
	}
	a.add("value", value).add("type", t.BaseType).add("name", t.Name)
	return r.wrap(t, "<input"+a.String()+" />")
}

func (r *gfRender) today() (int, int, int) {
	n := time.Now().In(r.s.loc())
	return n.Year(), int(n.Month()), n.Day()
}

// gfDate formats a (possibly overflowing) year/month/day as YYYY-MM-DD.
func gfDate(y, m, d int) string {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func (r *gfRender) dateHTML(t *gfTag) string {
	class := gfControlsClass(t.Type, "wpcf7-validates-as-date", r.notValid(t))
	a := &gfAtts{}
	a.add("class", t.classOption(class)).add("id", t.idOption()).add("tabindex", t.option1("tabindex", "signed_int"))
	a.add("min", t.dateOption("min", r.today)).add("max", t.dateOption("max", r.today)).add("step", t.option1("step", "int"))
	a.flag("readonly", t.has("readonly")).add("autocomplete", t.autocompleteOption())
	r.ariaState(a, t, t.required())
	value := r.singleValue(t, a, first(t.Values))
	a.add("value", value).add("type", "date").add("name", t.Name)
	return r.wrap(t, "<input"+a.String()+" />")
}

func (r *gfRender) selectHTML(t *gfTag) string {
	class := gfControlsClass(t.Type, r.notValid(t))
	a := &gfAtts{}
	a.add("class", t.classOption(class)).add("id", t.idOption()).add("tabindex", t.option1("tabindex", "signed_int")).add("autocomplete", t.autocompleteOption())
	r.ariaState(a, t, t.required())
	multiple := t.has("multiple")
	values, labels := append([]string{}, t.Values...), append([]string{}, t.Labels...)
	var blankLabel string
	hasBlank := t.has("include_blank") || len(values) == 0
	if t.has("first_as_label") && len(values) > 0 {
		blankLabel, hasBlank = labels[0], true
		values, labels = values[1:], labels[1:]
	} else if hasBlank {
		blankLabel = "—Please choose an option—"
	}
	selected := r.defaultValues(t, multiple, false)
	if h, ok := r.hangover(t.Name); ok {
		selected = h
	}
	var opts strings.Builder
	if hasBlank && !multiple {
		opts.WriteString(`<option value="">` + html.EscapeString(blankLabel) + `</option>`)
	}
	for i, v := range values {
		o := &gfAtts{}
		o.add("value", v).flag("selected", gfContains(selected, v))
		opts.WriteString("<option" + o.String() + ">" + html.EscapeString(labels[i]) + "</option>")
	}
	name := t.Name
	if multiple {
		name += "[]"
	}
	a.flag("multiple", multiple).add("name", name)
	return r.wrap(t, "<select"+a.String()+">"+opts.String()+"</select>")
}

func gfContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (r *gfRender) checkboxHTML(t *gfTag) string {
	exclusive := t.has("exclusive") && t.BaseType == "checkbox"
	multiple := t.BaseType == "checkbox" && !exclusive
	extra := ""
	if exclusive {
		extra = "wpcf7-exclusive-checkbox"
	}
	class := gfControlsClass(t.Type, r.notValid(t), extra)
	a := &gfAtts{}
	a.add("class", t.classOption(class)).add("id", t.idOption())
	if r.validationError(t.Name) != "" {
		a.add("aria-describedby", r.errorID(t.Name))
	}
	tabindex := gfAtoi(t.option1("tabindex", "signed_int"))
	checked := r.defaultValues(t, multiple, false)
	if h, ok := r.hangover(t.Name); ok {
		checked = h
	}
	var b strings.Builder
	for i, v := range t.Values {
		label := v
		if i < len(t.Labels) {
			label = t.Labels[i]
		}
		ia := &gfAtts{}
		name := t.Name
		if multiple {
			name += "[]"
		}
		ia.add("type", t.BaseType).add("name", name).add("value", v).flag("checked", gfContains(checked, v))
		if tabindex != 0 {
			ia.add("tabindex", strconv.Itoa(tabindex))
		}
		var item string
		if t.has("label_first") {
			item = `<span class="wpcf7-list-item-label">` + html.EscapeString(label) + `</span><input` + ia.String() + ` />`
		} else {
			item = `<input` + ia.String() + ` /><span class="wpcf7-list-item-label">` + html.EscapeString(label) + `</span>`
		}
		if t.has("use_label_element") {
			item = "<label>" + item + "</label>"
		}
		if tabindex > 0 {
			tabindex++
		}
		cls := "wpcf7-list-item"
		if i == 0 {
			cls += " first"
		}
		if i == len(t.Values)-1 {
			cls += " last"
			if t.has("free_text") {
				fname := "_wpcf7_free_text_" + t.Name
				fa := &gfAtts{}
				fa.add("type", "text").add("name", fname).add("class", "wpcf7-free-text")
				if tabindex != 0 {
					fa.add("tabindex", strconv.Itoa(tabindex))
				}
				if r.sub != nil {
					fa.add("value", first(r.sub.raw[fname]))
				}
				item += " <input" + fa.String() + " />"
				cls += " has-free-text"
			}
		}
		b.WriteString(`<span class="` + cls + `">` + item + `</span>`)
	}
	return r.wrap(t, "<span"+a.String()+">"+b.String()+"</span>")
}

func (r *gfRender) acceptanceHTML(t *gfTag) string {
	extra := []string{r.notValid(t)}
	if t.has("invert") {
		extra = append(extra, "invert")
	}
	if t.has("optional") {
		extra = append(extra, "optional")
	}
	class := gfControlsClass(t.Type, extra...)
	a := &gfAtts{}
	a.add("class", t.classOption(class)).add("id", t.idOption())
	ia := &gfAtts{}
	checked := t.has("default:on")
	if h, ok := r.hangover(t.Name); ok {
		checked = first(h) != ""
	}
	ia.add("type", "checkbox").add("name", t.Name).add("value", "1").add("tabindex", t.option1("tabindex", "signed_int")).flag("checked", checked)
	r.ariaState(ia, t, false)
	content := t.Content
	if content == "" {
		content = first(t.Values)
	}
	var inner string
	if content = strings.TrimSpace(content); content != "" {
		label := `<span class="wpcf7-list-item-label">` + wp.KsesPost(content) + `</span>`
		if t.has("label_first") {
			inner = `<span class="wpcf7-list-item"><label>` + label + `<input` + ia.String() + ` /></label></span>`
		} else {
			inner = `<span class="wpcf7-list-item"><label><input` + ia.String() + ` />` + label + `</label></span>`
		}
	} else {
		inner = `<span class="wpcf7-list-item"><input` + ia.String() + ` /></span>`
	}
	return r.wrap(t, "<span"+a.String()+">"+inner+"</span>")
}

func (r *gfRender) quizHTML(t *gfTag) string {
	class := gfControlsClass(t.Type, "wpcf7-quiz", r.notValid(t))
	a := &gfAtts{}
	maxl, minl := t.maxlengthOption(""), t.minlengthOption()
	a.add("size", t.sizeOption("40")).add("maxlength", maxl).add("minlength", minl)
	a.add("class", t.classOption(class)).add("id", t.idOption()).add("tabindex", t.option1("tabindex", "signed_int")).add("autocomplete", "off")
	r.ariaState(a, t, true)
	q, ans := r.s.gontactQuizPick(t)
	a.add("type", "text").add("name", t.Name)
	return `<span class="wpcf7-form-control-wrap" data-name="` + html.EscapeString(t.Name) + `"><label><span class="wpcf7-quiz-label">` +
		html.EscapeString(q) + `</span> <input` + a.String() + ` /></label><input type="hidden" name="_wpcf7_quiz_answer_` +
		html.EscapeString(t.Name) + `" value="` + r.s.gontactQuizHash(ans) + `" />` + r.validationError(t.Name) + `</span>`
}

// gontactQuizPick chooses a random question of a quiz tag (1+1=? without questions).
func (s *Server) gontactQuizPick(t *gfTag) (question, answer string) {
	if len(t.Pipes) == 0 {
		return "1+1=?", "2"
	}
	p := t.Pipes[rand.IntN(len(t.Pipes))]
	return p.Before, p.After
}

// gfAcceptTypes is wpcf7_acceptable_filetypes() for the accept attribute.
func gfAcceptTypes(t *gfTag) string {
	types := gfFileTypes(t)
	var out []string
	for _, ft := range types {
		if strings.Contains(ft, "/") {
			out = append(out, ft)
		} else {
			out = append(out, "."+ft)
		}
	}
	return strings.Join(out, ",")
}

// gfFileTypes lists the filetypes: option (extensions or MIME types); audio, video and images by default.
func gfFileTypes(t *gfTag) []string {
	var out []string
	for _, v := range t.option("filetypes", "") {
		for _, ft := range strings.Split(v, "|") {
			if ft = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ft), ".")); ft != "" {
				out = append(out, ft)
			}
		}
	}
	if len(out) == 0 {
		out = []string{"audio/*", "video/*", "image/*"}
	}
	return out
}

func (r *gfRender) fileHTML(t *gfTag) string {
	class := gfControlsClass(t.Type, "wpcf7-file", r.notValid(t))
	a := &gfAtts{}
	a.add("size", t.sizeOption("40")).add("class", t.classOption(class)).add("id", t.idOption())
	a.add("capture", t.option1("capture", "")).add("tabindex", t.option1("tabindex", "signed_int")).add("accept", gfAcceptTypes(t))
	r.ariaState(a, t, t.required())
	a.add("type", "file").add("name", t.Name)
	return r.wrap(t, "<input"+a.String()+" />")
}

func (r *gfRender) submitHTML(t *gfTag) string {
	class := gfControlsClass(t.Type, "has-spinner")
	a := &gfAtts{}
	value := first(t.Values)
	if value == "" {
		value = "Send"
	}
	a.add("class", t.classOption(class)).add("id", t.idOption()).add("tabindex", t.option1("tabindex", "signed_int"))
	a.add("type", "submit").add("value", value)
	out := "<input" + a.String() + " />"
	if site := r.s.settings.String("gontact_turnstile_sitekey"); site != "" && r.s.settings.String("gontact_turnstile_secret") != "" {
		out = `<div class="wpcf7-turnstile cf-turnstile" data-sitekey="` + html.EscapeString(site) +
			`" data-response-field-name="_wpcf7_turnstile_response"></div>` + out
	}
	return out
}

func (r *gfRender) hiddenHTML(t *gfTag) string {
	a := &gfAtts{}
	value := first(t.Values)
	if d := r.defaultValues(t, false, false); len(d) > 0 {
		value = d[0]
	}
	a.add("class", t.classOption("")).add("id", t.idOption()).add("value", value).add("type", "hidden").add("name", t.Name)
	return "<input" + a.String() + " />"
}

// countHTML is the [count] character counter of a textarea or text field.
func (r *gfRender) countHTML(t *gfTag) string {
	_, targets := gfScan(r.f.Form, nil)
	var target *gfTag
	for _, tt := range targets {
		if tt.Name == t.Name && tt.BaseType != "count" {
			target = tt
			break
		}
	}
	maxl, minl := "", ""
	if target != nil {
		def := "400"
		if target.BaseType == "textarea" {
			def = "2000"
		}
		maxl, minl = target.maxlengthOption(def), target.minlengthOption()
	}
	down := t.has("down")
	class := "wpcf7-character-count"
	start := "0"
	if down {
		class += " down"
		start = maxl
	} else {
		class += " up"
	}
	a := &gfAtts{}
	a.add("id", t.idOption()).add("class", t.classOption(class)).add("data-target-name", t.Name).add("data-starting-value", start)
	a.add("data-current-value", start).add("data-maximum-value", maxl).add("data-minimum-value", minl)
	return "<span" + a.String() + ">" + html.EscapeString(start) + "</span>"
}

func gfAtoi(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}

// gontactAssetTags prints the stylesheet, the wpcf7 settings and the script once per request.
func (s *Server) gontactAssetTags(c *gin.Context) string {
	if c != nil {
		if _, done := c.Get("gontact.assets"); done {
			return ""
		}
		c.Set("gontact.assets", true)
	}
	root := "/wp-json/"
	if c != nil {
		root = s.links(c).HomeURL("/wp-json/")
	}
	cfg := map[string]any{"api": map[string]any{"root": root, "namespace": gontactNamespace}}
	if s.settings.Bool("wpgo_cache_enabled") {
		cfg["cached"] = 1
	}
	out := `<link rel="stylesheet" id="contact-form-7-css" href="` + gontactAssets + `/styles.css?ver=` + gontactVersion + `" media="all" />` + "\n"
	if site := s.settings.String("gontact_recaptcha_sitekey"); s.gontactRecaptchaKeys() != "" {
		cfg["recaptcha"] = map[string]any{"sitekey": site, "actions": map[string]string{"homepage": "homepage", "contactform": "contactform"}}
		out += `<script src="https://www.google.com/recaptcha/api.js?render=` + html.EscapeString(site) + `&amp;ver=3.0" id="google-recaptcha-js" defer></script>` + "\n"
		cspAllow(c, "script-src", "https://www.google.com", "https://www.gstatic.com")
		cspAllow(c, "frame-src", "https://www.google.com")
		cspAllow(c, "connect-src", "https://www.google.com")
	}
	if s.settings.String("gontact_turnstile_sitekey") != "" && s.settings.String("gontact_turnstile_secret") != "" {
		out += `<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" id="cloudflare-turnstile-js" defer></script>` + "\n"
		cspAllow(c, "script-src", "https://challenges.cloudflare.com")
		cspAllow(c, "frame-src", "https://challenges.cloudflare.com")
	}
	js, _ := json.Marshal(cfg)
	out += `<script id="contact-form-7-js-before">var wpcf7 = ` + string(js) + `;</script>` + "\n" +
		`<script src="` + gontactAssets + `/index.js?ver=` + gontactVersion + `" id="contact-form-7-js" defer></script>`
	return out
}

// gontactRecaptchaKeys returns the site key when reCAPTCHA is fully configured.
func (s *Server) gontactRecaptchaKeys() string {
	if s.settings.String("gontact_recaptcha_secret") == "" {
		return ""
	}
	return s.settings.String("gontact_recaptcha_sitekey")
}

// Compiled once (they used to be compiled on every call).
var (
	gfNonAlnumLowerRe = regexp.MustCompile(`[^0-9a-z]+`)
	gfNonIDRe         = regexp.MustCompile(`[^0-9a-zA-Z_-]+`)
	gfNonClassRe      = regexp.MustCompile(`[^A-Za-z0-9_-]`)
	gfNonAttrRe       = regexp.MustCompile(`[^A-Za-z0-9:._-]`)
	gfDigitsRe        = regexp.MustCompile(`^[0-9_]+$`)
)
