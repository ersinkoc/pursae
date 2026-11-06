package webbuilder

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Form schema (contract §3.5). The fields of a form come from the stored doc, never from the
// request: a `form` section's variant decides which inputs it renders (upstream
// variants/form.tsx), and the live renderer names them after FormFields.

// FormField is one input of a form section.
type FormField struct {
	Name     string   `json:"name"` // one of FormFields
	Type     string   `json:"type"` // text | email | tel | date | textarea | select
	Label    string   `json:"label"`
	Required bool     `json:"required"`
	Max      int      `json:"max"` // characters
	Options  []string `json:"options,omitempty"`
}

// FormSchema is the form of one section.
type FormSchema struct {
	SectionID string      `json:"sectionId"`
	Variant   string      `json:"variant"`
	Title     string      `json:"title"` // section title as plain text (mail subject)
	Fields    []FormField `json:"fields"`
}

// Field reasons in FormSchema.Validate (data.fields of wb_form_invalid).
const (
	FormRequired = "required"
	FormInvalid  = "invalid"
	FormTooLong  = "too_long"
)

// formVariants: the inputs each upstream form design renders, in order. "default" is also used
// for unknown or missing variants.
var formVariants = map[string][]string{
	"default":      {"name", "email", "message"},
	"card-center":  {"name", "email", "message"},
	"contact-list": {"name", "email", "choice", "message"},
	"dark":         {"name", "email", "message"},
	"image":        {"name", "email", "message"},
	"underline":    {"name", "email", "message"},
	"newsletter":   {"email"},
	"booking":      {"name", "email", "date", "choice", "message"},
	"map":          {"name", "email", "message"},
	"steps":        {"name", "email", "choice"},
	"chat":         {"name", "email", "message"},
	"faq-notes":    {"name", "email", "message"},
}

var fieldDefaults = map[string]FormField{
	"name":    {Name: "name", Type: "text", Label: "Name", Max: FormNameMax},
	"email":   {Name: "email", Type: "email", Label: "Email", Required: true, Max: FormEmailMax},
	"phone":   {Name: "phone", Type: "tel", Label: "Phone", Max: FormPhoneMax},
	"message": {Name: "message", Type: "textarea", Label: "Message", Max: FormMessageMax},
	"date":    {Name: "date", Type: "select", Label: "Date", Max: FormOtherMax},
	"choice":  {Name: "choice", Type: "select", Label: "Choice", Max: FormOtherMax},
}

// choiceLabels: the default label of the "choice" select per design.
var choiceLabels = map[string]string{"contact-list": "Topic", "steps": "Sites", "booking": "Time"}

// labelKeys: the content key holding a field's visible label.
var labelKeys = map[string]string{"name": "nameLabel", "email": "emailLabel", "message": "messageLabel", "date": "dateLabel", "choice": "timeLabel"}

// FormSchemas returns the schema of every visible form section, nested ones included, in
// document order.
func FormSchemas(d *DocState) []FormSchema {
	var out []FormSchema
	var walk func(list []Section, depth int)
	walk = func(list []Section, depth int) {
		if depth > MaxDepth+1 {
			return
		}
		for i := range list {
			s := &list[i]
			if s.Hidden {
				continue
			}
			if s.Kind == KindForm {
				out = append(out, schemaOf(s))
			}
			for j := range s.Items {
				if s.Items[j].Sub != nil {
					walk([]Section{*s.Items[j].Sub}, depth+1)
				}
			}
		}
	}
	walk(d.Sections, 0)
	return out
}

// FindForm returns the schema of the visible form section with that id.
func FindForm(d *DocState, sectionID string) (FormSchema, bool) {
	for _, f := range FormSchemas(d) {
		if f.SectionID == sectionID {
			return f, true
		}
	}
	return FormSchema{}, false
}

func schemaOf(s *Section) FormSchema {
	variant := s.Content["variant"]
	names, ok := formVariants[variant]
	if !ok {
		variant = "default"
		names = formVariants[variant]
	}
	fs := FormSchema{SectionID: s.ID, Variant: variant, Title: ToPlain(s.Content["title"])}
	for _, n := range names {
		f := fieldDefaults[n]
		if n == "choice" && choiceLabels[variant] != "" {
			f.Label = choiceLabels[variant]
		}
		if k := labelKeys[n]; k != "" {
			if l := strings.TrimSpace(ToPlain(s.Content[k])); l != "" {
				f.Label = l
			}
		}
		// A contact form without a message is not useful; the newsletter only asks for email.
		if n == "message" && variant != "booking" {
			f.Required = true
		}
		fs.Fields = append(fs.Fields, f)
	}
	return fs
}

// Validate checks submitted values against the schema. clean holds the trimmed values of the
// schema's fields (others are ignored); errs maps field → FormRequired | FormInvalid | FormTooLong.
func (fs FormSchema) Validate(values map[string]string) (clean map[string]string, errs map[string]string) {
	clean = map[string]string{}
	errs = map[string]string{}
	for _, f := range fs.Fields {
		v := strings.TrimSpace(strings.ToValidUTF8(values[f.Name], ""))
		v = stripControl(v, f.Type == "textarea")
		switch {
		case v == "":
			if f.Required {
				errs[f.Name] = FormRequired
			}
			continue
		case utf8.RuneCountInString(v) > f.Max:
			errs[f.Name] = FormTooLong
			continue
		}
		if f.Type == "email" {
			a, err := mail.ParseAddress(v)
			if err != nil || a.Name != "" || a.Address != v || !strings.Contains(v[strings.LastIndexByte(v, '@')+1:], ".") {
				errs[f.Name] = FormInvalid
				continue
			}
		}
		clean[f.Name] = v
	}
	return clean, errs
}

// stripControl removes control characters; single-line fields also lose CR/LF (header safety).
func stripControl(s string, multiline bool) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' && multiline {
			return r
		}
		if r == '\r' {
			if multiline {
				return -1
			}
			return ' '
		}
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return -1
		}
		return r
	}, s)
}
