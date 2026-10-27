package server

// Golementor v4: the atomic form (e-form and its field widgets). Submissions arrive on the
// admin-ajax action elementor_pro_atomic_forms_send_form (Pro's Atomic_Form_Controller) and run
// through the Pro forms back end (GWEFormSubmit in gowebeditor_pro_forms.go: validation, Collect
// Submissions, e-mail, webhook, action log): the atomic form's settings are mapped onto the
// classic form widget's setting names and its posted fields onto form record fields.

import (
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder/atomic"
)

// gweV4FormNonce is the nonce action of atomic form submissions (Atomic_Form_Controller::NONCE_ACTION).
const gweV4FormNonce = "elementor_pro_atomic_forms_send_form"

// gweV4CapturedRecipient is the admin e-mail of the lab the editor data was captured from: the atomic
// form's default "To" address (Atomic_Form::get_default_recipient_email), replaced by this site's.
const gweV4CapturedRecipient = "lab@example.test"

// gweV4Fill swaps the captured site values in editor data for this site's (called from gweFill).
func (s *Server) gweV4Fill(_ *gin.Context, text string) string {
	if strings.Contains(text, gweV4CapturedRecipient) {
		text = strings.ReplaceAll(text, gweV4CapturedRecipient, s.settings.String("admin_email"))
	}
	return text
}

// gweV4FormFieldTypes are the atomic form's field widgets (Module::get_form_field_widget_types).
var gweV4FormFieldTypes = map[string]bool{"e-form-input": true, "e-form-textarea": true, "e-form-checkbox": true,
	"e-form-radio-button": true, "e-form-date-picker": true, "e-form-time-picker": true, "e-form-select": true, "e-form-file-upload": true}

// gweV4PostedField is one form_fields[i] entry of an atomic form submission.
type gweV4PostedField struct {
	id, typ, label, name string
	values               []string
	files                []*multipart.FileHeader
}

var gweV4FieldKey = regexp.MustCompile(`^form_fields\[(\d+)\]\[(id|type|label|name|options|value)\](?:\[(\d+)\])?$`)

// gweV4PostedFields parses form_fields[i][key] / form_fields[i][value][j] (files included), in index order.
func gweV4PostedFields(c *gin.Context) []*gweV4PostedField {
	byIndex := map[int]*gweV4PostedField{}
	maxIndex := -1
	get := func(i int) *gweV4PostedField {
		f := byIndex[i]
		if f == nil {
			f = &gweV4PostedField{}
			byIndex[i] = f
			if i > maxIndex {
				maxIndex = i
			}
		}
		return f
	}
	for key, vals := range c.Request.PostForm {
		m := gweV4FieldKey.FindStringSubmatch(key)
		if m == nil || len(vals) == 0 {
			continue
		}
		i, err := strconv.Atoi(m[1])
		if err != nil || i > 500 {
			continue
		}
		f := get(i)
		switch m[2] {
		case "id":
			f.id = vals[0]
		case "type":
			f.typ = vals[0]
		case "label":
			f.label = vals[0]
		case "name":
			f.name = vals[0]
		case "value":
			f.values = append(f.values, vals...)
		}
	}
	if mf := c.Request.MultipartForm; mf != nil {
		for key, fhs := range mf.File {
			m := gweV4FieldKey.FindStringSubmatch(key)
			if m == nil || m[2] != "value" {
				continue
			}
			if i, err := strconv.Atoi(m[1]); err == nil && i <= 500 {
				get(i).files = append(get(i).files, fhs...)
			}
		}
	}
	var out []*gweV4PostedField
	for i := 0; i <= maxIndex; i++ {
		if f := byIndex[i]; f != nil && f.id != "" {
			out = append(out, f)
		}
	}
	return out
}

func gweV4FormFail(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, gin.H{"success": false, "data": gin.H{"message": msg}})
}

// gweV4SendForm is the elementor_pro_atomic_forms_send_form admin-ajax action.
func (s *Server) gweV4SendForm(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		_ = c.Request.ParseForm()
	}
	invalid := gweFormMessages["invalid_form"]
	if !s.checkNonceValue(c, c.PostForm("_nonce"), gweV4FormNonce) {
		gweV4FormFail(c, invalid)
		return
	}
	postID := gweInt(c.PostForm("post_id"))
	formID := strings.TrimSpace(c.PostForm("form_id"))
	posted := gweV4PostedFields(c)
	if postID <= 0 || formID == "" || len(posted) == 0 {
		gweV4FormFail(c, invalid)
		return
	}
	p, err := s.store.GetPost(c, postID)
	if err != nil || p == nil || (p.Status != "publish" && !currentUser(c).Can("edit_post", p)) {
		gweV4FormFail(c, "Document not found")
		return
	}
	nodes, err := atomic.ParseNodes(gweElementsRaw(p))
	form := atomic.FindNode(nodes, formID)
	if err != nil || form == nil || form.Type() != "e-form" {
		gweV4FormFail(c, "Form element not found")
		return
	}
	env := s.gweAtomicEnv(c, false)
	fs := env.ResolvedSettings(form)
	settings, actions := s.gweV4FormSettings(form, fs, formID, p.ID)
	if len(actions) == 0 {
		gweV4FormFail(c, "No actions configured for this form")
		return
	}

	// The form's field elements: their resolved settings, and css id → element id (shortcodes).
	fieldSettings := map[string]map[string]any{}
	cssids := map[string]string{}
	atomic.Walk(form.Children, func(n *atomic.Node) {
		if !gweV4FormFieldTypes[n.Type()] {
			return
		}
		st := env.ResolvedSettings(n)
		if st == nil {
			st = map[string]any{}
		}
		fieldSettings[n.ID] = st
		if id := gweStr(st["_cssid"]); id != "" {
			if _, dup := cssids[id]; !dup {
				cssids[id] = n.ID
			}
		}
	})
	s.gweV4FormShortcodes(settings, cssids)
	// Reply-To: the classic e-mail action takes a field id, e-mail 2 a value with shortcodes.
	if m := gweV4ReplyField.FindStringSubmatch(gweStr(settings["gwe_v4_reply_to"])); m != nil {
		settings["email_reply_to"] = m[1]
	}
	settings["email_reply_to_2"] = gweStr(settings["gwe_v4_reply_to_2"])

	res := newGWEFormResult(settings)
	var fields []*GWEFormField
	seen := map[string]bool{}
	var uploadItems []any
	for _, pf := range posted {
		st, ok := fieldSettings[pf.id]
		if !ok || seen[pf.id] {
			continue // not a field of this form
		}
		seen[pf.id] = true
		typ := gweV4ClassicType(pf.typ)
		f := &GWEFormField{ID: pf.id, Type: typ, Title: strings.TrimSpace(pf.label), Required: truthyAny(st["required"])}
		if f.Title == "" {
			f.Title = pf.name
		}
		switch {
		case typ == "upload":
			key := "form_fields[" + pf.id + "][]"
			if len(pf.files) > 0 && c.Request.MultipartForm != nil {
				c.Request.MultipartForm.File[key] = pf.files
			}
			mx := gweInt(st["max-files"])
			uploadItems = append(uploadItems, map[string]any{"custom_id": pf.id, "file_sizes": gweInt(st["max-file-size"]),
				"file_types": gweStr(st["file-types"]), "allow_multiple_upload": truthyAny(st["multiple"]) || mx > 1, "max_files": mx})
			if msg := s.gweProFormUpload(c, f, uploadItems); msg != "" {
				res.addError(f.ID, msg)
			}
		case len(pf.values) > 1:
			f.RawValue = pf.values
			f.Value = gweProSanitizeField(typ, strings.Join(pf.values, ", "))
		case len(pf.values) == 1:
			f.RawValue = pf.values[0]
			f.Value = gweProSanitizeField(typ, pf.values[0])
		}
		fields = append(fields, f)
	}
	// Required fields the browser did not send at all (an unchecked checkbox group, a field removed
	// from the page) still fail validation.
	for id, st := range fieldSettings {
		if !seen[id] && truthyAny(st["required"]) {
			res.addError(id, res.message("required_field"))
		}
	}
	if len(fields) == 0 {
		gweV4FormFail(c, invalid)
		return
	}
	if !res.Success() {
		res.addErrorMessage(res.message("error"))
		s.GWEFormRespond(c, res)
		return
	}
	s.GWEFormRespond(c, s.GWEFormSubmit(c, p, settings, fields))
}

// gweV4ClassicType maps an atomic field's posted type onto the classic form's field types.
func gweV4ClassicType(t string) string {
	switch t {
	case "file":
		return "upload"
	case "text", "email", "tel", "url", "number", "password", "textarea", "checkbox", "radio", "select", "date", "time":
		return t
	}
	return "text"
}

// gweV4FormActions maps the atomic form's actions after submit onto the classic form's.
var gweV4FormActions = map[string]string{"email": "email", "email_2": "email2", "collect-submissions": "save-to-database", "webhook": "webhook"}

// gweV4FormSettings maps an e-form's resolved settings onto the classic form widget's setting names
// (Atomic_Form_Controller::resolve_widget_settings, Email_Settings).
func (s *Server) gweV4FormSettings(form *atomic.Node, fs map[string]any, formID string, postID int64) (map[string]any, []any) {
	name := strings.TrimSpace(gweStr(fs["form-name"]))
	if name == "" {
		name = formID
	}
	settings := map[string]any{"id": formID, "form_name": name, "form_post_id": postID}
	acts := asAnyList(fs["actions-after-submit"])
	if acts == nil && fs["email"] != nil {
		acts = []any{"email"}
	}
	var actions []any
	metaKeys := []any{}
	for _, a := range acts {
		classic, ok := gweV4FormActions[gweStr(a)]
		if !ok {
			continue
		}
		actions = append(actions, classic)
		switch classic {
		case "email", "email2":
			suffix := ""
			src := fs["email"]
			if classic == "email2" {
				suffix, src = "_2", fs["email_2"]
			}
			em, _ := src.(map[string]any)
			list := func(v any) string {
				var out []string
				switch x := v.(type) {
				case []any:
					for _, it := range x {
						if t := strings.TrimSpace(gweStr(it)); t != "" {
							out = append(out, t)
						}
					}
				case string:
					if t := strings.TrimSpace(x); t != "" {
						out = append(out, t)
					}
				}
				return strings.Join(out, ", ")
			}
			to := strings.ReplaceAll(list(em["to"]), gweV4CapturedRecipient, s.settings.String("admin_email"))
			settings["email_to"+suffix] = to
			settings["email_subject"+suffix] = gweStr(em["subject"])
			settings["email_content"+suffix] = gweStr(em["message"])
			settings["email_from"+suffix] = gweStr(em["from"])
			settings["email_from_name"+suffix] = gweStr(em["from-name"])
			settings["email_to_cc"+suffix] = list(em["cc"])
			settings["email_to_bcc"+suffix] = list(em["bcc"])
			settings["gwe_v4_reply_to"+suffix] = gweStr(em["reply-to"])
			ct := gweStr(em["send-as"])
			if ct == "" {
				ct = "html"
			}
			settings["email_content_type"+suffix] = ct
			for _, k := range asAnyList(em["meta-data"]) {
				switch gweStr(k) {
				case "date", "time", "credit":
					metaKeys = append(metaKeys, gweStr(k))
				case "page-url":
					metaKeys = append(metaKeys, "page_url")
				case "user-agent":
					metaKeys = append(metaKeys, "user_agent")
				}
			}
		case "webhook":
			settings["webhooks"] = strings.TrimSpace(gweStr(fs["webhook_url"]))
		}
	}
	settings["form_metadata"] = metaKeys
	settings["submit_actions"] = actions
	return settings, actions
}

var gweV4ReplyField = regexp.MustCompile(`^\s*\[field id="(\w+)"\]\s*$`)

var gweV4QuotedField = regexp.MustCompile(`\[field([^\]]*)id='([^']+)'([^\]]*)\]`)

// gweV4FormShortcodes rewrites the atomic shortcodes of the e-mail settings ([<css id>] and
// [field id='...']) into the classic [field id="<element id>"] the forms back end resolves.
func (s *Server) gweV4FormShortcodes(settings map[string]any, cssids map[string]string) {
	for k, v := range settings {
		text, ok := v.(string)
		if !ok || !strings.HasPrefix(k, "email_") && !strings.HasPrefix(k, "gwe_v4_reply_to") {
			continue
		}
		text = gweV4QuotedField.ReplaceAllString(text, `[field${1}id="${2}"${3}]`)
		for cssid, el := range cssids {
			if "["+cssid+"]" != "[all-fields]" {
				text = strings.ReplaceAll(text, "["+cssid+"]", `[field id="`+el+`"]`)
			}
		}
		settings[k] = text
	}
}

// gweV4FormCacheHints keeps pages with atomic display conditions out of the page cache (their
// output depends on the visitor) and caps the cache lifetime of pages with an atomic form to 12
// hours so the submission nonce they carry stays valid (nonces live 12–24 hours).
func gweV4FormCacheHints(c *gin.Context, nodes []*atomic.Node) {
	hasForm, hasConds := false, false
	atomic.Walk(nodes, func(n *atomic.Node) {
		if n.Type() == "e-form" {
			hasForm = true
		}
		if atomic.DisplayConditions(n) != "" {
			hasConds = true
		}
	})
	h := c.Writer.Header()
	if hasConds {
		h.Set("X-WPGo-Cache-Control", "no-cache")
	} else if hasForm {
		if ttl, err := strconv.Atoi(h.Get("X-WPGo-Cache-TTL")); err != nil || ttl <= 0 || ttl > 43200 {
			h.Set("X-WPGo-Cache-TTL", "43200")
		}
	}
}
