package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"strings"

	"github.com/gin-gonic/gin"
)

// Settings › Payments (tab=checkout): the gateways table (drag order saved to gateway_order, enabled
// toggles) and each gateway's settings (section=<id>) from its FormFields, saved as "<id>_settings".
// Settings › Emails (tab=email): the e-mails list and each e-mail's settings (section=<id>) saved as
// "email_<id>_settings", plus the sender / template options.

type gooGatewayRow struct {
	ID, Title, MethodTitle, Desc string
	Enabled                      bool
	URL                          string
}

func (s *Server) gooGatewaysView(c *gin.Context, d *gooSettingsData) {
	d.Kind, d.Title = "gateways", "Payment methods"
	d.Intro = `<p>Installed payment methods are listed below and can be sorted to control their display order on the frontend.</p>`
	for _, g := range s.gooGatewaysOrdered() {
		d.Gateways = append(d.Gateways, gooGatewayRow{ID: g.ID(), Title: g.Title(), MethodTitle: g.MethodTitle(), Desc: g.MethodDescription(),
			Enabled: g.Enabled(), URL: gooAdminURL("settings", "tab", "checkout", "section", g.ID())})
	}
}

// gooGatewaysPost saves the gateway order and the enabled toggles.
func (s *Server) gooGatewaysPost(c *gin.Context) {
	ctx := c.Request.Context()
	order := []string{}
	for _, id := range c.PostFormArray("gateway_order[]") {
		if gooGatewayRegistry[id] != nil && !containsStr(order, id) {
			order = append(order, id)
		}
	}
	vals := map[string]any{}
	if len(order) > 0 {
		vals["gateway_order"] = order
	}
	if c.PostForm("gateway_toggles") != "" {
		for _, g := range s.gooGatewaysOrdered() {
			st := s.gooOptMap(g.ID() + "_settings")
			en := "no"
			if c.PostForm("gateway_enabled["+g.ID()+"]") != "" {
				en = "yes"
			}
			if gooValStr(st["enabled"]) != en {
				cp := map[string]any{}
				for k, v := range st {
					cp[k] = v
				}
				cp["enabled"] = en
				vals[g.ID()+"_settings"] = cp
			}
		}
	}
	if len(vals) > 0 {
		_ = s.gooSetOpts(ctx, vals)
	}
}

// gooBACSAccountsHTML is the BACS "Account details" table (wc_bacs_accounts).
func (s *Server) gooBACSAccountsHTML(v any) template.HTML {
	list, _ := v.([]any)
	var b strings.Builder
	b.WriteString(`<table class="widefat wc_input_table sortable" cellspacing="0"><thead><tr><th class="sort">&nbsp;</th>
		<th>Account name</th><th>Account number</th><th>Bank name</th><th>Sort code</th><th>IBAN</th><th>BIC / Swift</th></tr></thead><tbody class="accounts">`)
	keys := []string{"account_name", "account_number", "bank_name", "sort_code", "iban", "bic"}
	row := func(m map[string]any) {
		b.WriteString(`<tr class="account"><td class="sort"></td>`)
		for _, k := range keys {
			v, _ := m[k].(string)
			fmt.Fprintf(&b, `<td><input type="text" value="%s" name="bacs_%s[]"></td>`, gooAttr(v), k)
		}
		b.WriteString(`</tr>`)
	}
	for _, a := range list {
		if m, ok := a.(map[string]any); ok {
			row(m)
		}
	}
	row(map[string]any{}) // a blank row to add an account
	b.WriteString(`</tbody><tfoot><tr><th colspan="7"><a href="#" class="add button">+ Add account</a> <a href="#" class="remove_rows button">Remove selected account(s)</a></th></tr></tfoot></table>`)
	return template.HTML(b.String())
}

// gooCODMethodOptions are the shipping methods COD can be limited to: method types and their instances.
func (s *Server) gooCODMethodOptions(ctx context.Context) []gooOpt {
	var out []gooOpt
	zones, _ := s.gooZones(ctx, true)
	for _, def := range s.gooShippingMethodOrder() {
		out = append(out, gooOpt{def.ID, def.Title})
		for _, z := range zones {
			for _, m := range z.Methods {
				if m.MethodID == def.ID {
					out = append(out, gooOpt{def.ID + ":" + itoa64(m.InstanceID), fmt.Sprintf("%s – %s (#%d)", z.Name, m.Title(), m.InstanceID)})
				}
			}
		}
	}
	return out
}

func (s *Server) gooGatewayFields(ctx context.Context, g gooGatewayDef) []gooField {
	fields := g.FormFields()
	for i := range fields {
		if g.ID() == "cod" && fields[i].ID == "enable_for_methods" {
			fields[i].Options = s.gooCODMethodOptions(ctx)
		}
	}
	return fields
}

func (s *Server) gooGatewayView(c *gin.Context, d *gooSettingsData, id string) {
	ctx := c.Request.Context()
	d.Kind = "fields"
	g := gooGatewayRegistry[id]
	if g == nil {
		d.Body, d.HideSave = `<p>Invalid payment method.</p>`, true
		return
	}
	d.Title = g.MethodTitle()
	d.BackURL, d.BackText = gooAdminURL("settings", "tab", "checkout"), "Payment methods"
	d.Intro = template.HTML(`<h2>` + gooEsc(g.MethodTitle()) + `</h2><p>` + gooEsc(g.MethodDescription()) + `</p>`)
	prefix := "woocommerce_" + id + "_"
	values := s.gooGatewaySettings(id)
	d.Body = s.gooFieldsHTML(ctx, s.gooGatewayFields(ctx, g), func(f string) string { return prefix + f }, gooMapValue(values),
		func(f gooField, name string) template.HTML {
			if f.Type == "bacs_accounts" {
				return s.gooBACSAccountsHTML(values["accounts"])
			}
			return ""
		})
}

func (s *Server) gooGatewayPost(c *gin.Context, id string) error {
	ctx := c.Request.Context()
	g := gooGatewayRegistry[id]
	if g == nil {
		return errors.New("Invalid payment method.")
	}
	st := map[string]any{}
	for k, v := range s.gooOptMap(id + "_settings") {
		st[k] = v
	}
	prefix := "woocommerce_" + id + "_"
	var plain []gooField
	for _, f := range s.gooGatewayFields(ctx, g) {
		if f.Type == "bacs_accounts" {
			names := c.PostFormArray("bacs_account_name[]")
			numbers := c.PostFormArray("bacs_account_number[]")
			banks := c.PostFormArray("bacs_bank_name[]")
			sorts := c.PostFormArray("bacs_sort_code[]")
			ibans := c.PostFormArray("bacs_iban[]")
			bics := c.PostFormArray("bacs_bic[]")
			at := func(l []string, i int) string {
				if i < len(l) {
					return strings.TrimSpace(l[i])
				}
				return ""
			}
			accounts := []any{}
			for i := range names {
				a := map[string]any{"account_name": at(names, i), "account_number": at(numbers, i), "bank_name": at(banks, i),
					"sort_code": at(sorts, i), "iban": at(ibans, i), "bic": at(bics, i)}
				empty := true
				for _, v := range a {
					if v != "" {
						empty = false
					}
				}
				if !empty {
					accounts = append(accounts, a)
				}
			}
			st["accounts"] = accounts
			continue
		}
		plain = append(plain, f)
	}
	s.gooReadMapFields(c, plain, func(f string) string { return prefix + f }, st)
	return s.gooSetOpts(ctx, map[string]any{id + "_settings": st})
}

// E-mails ------------------------------------------------------------------------------------------------------

type gooEmailRow struct {
	ID, Title, Desc, Type, Recipient, URL string
	Enabled, Manual                       bool
}

var gooEmailTypes = []gooOpt{{"plain", "Plain text"}, {"html", "HTML"}, {"multipart", "Multipart"}}

func (s *Server) gooVisibleEmails() []*gooEmailDef {
	var out []*gooEmailDef
	for _, d := range gooEmailDefs {
		if d.Module == "" || s.gooExtOn(d.Module) {
			out = append(out, d)
		}
	}
	return out
}

func (s *Server) gooEmailsView(c *gin.Context, d *gooSettingsData) {
	d.Kind, d.Title = "emails", "Emails"
	d.Intro = `<p>Email notifications sent from Goocommerce are listed below. Click on an email to configure it.</p>`
	for _, e := range s.gooVisibleEmails() {
		st := s.gooEmailSettings(e.ID)
		r := gooEmailRow{ID: e.ID, Title: e.Title, Desc: e.Description, Enabled: st["enabled"] == "yes", Manual: e.Manual,
			URL: gooAdminURL("settings", "tab", "email", "section", e.ID)}
		for _, t := range gooEmailTypes {
			if t.Value == st["email_type"] {
				r.Type = t.Label
			}
		}
		if e.Customer {
			r.Recipient = "Customer"
		} else {
			r.Recipient = st["recipient"]
		}
		d.Emails = append(d.Emails, r)
	}
}

func gooEmailFields(e *gooEmailDef) []gooField {
	ph := "Available placeholders: <code>{site_title}</code>, <code>{site_address}</code>, <code>{site_url}</code>, <code>{store_email}</code>, <code>{order_date}</code>, <code>{order_number}</code>"
	fields := []gooField{{ID: "email_options", Type: "title"}}
	if !e.Manual {
		fields = append(fields, gooField{ID: "enabled", Title: "Enable/Disable", Type: "checkbox", Default: "yes", Desc: "Enable this email notification"})
	}
	if !e.Customer {
		fields = append(fields, gooField{ID: "recipient", Title: "Recipient(s)", Type: "text", Holder: "", Desc: "Enter recipients (comma separated) for this email."})
	}
	fields = append(fields,
		gooField{ID: "subject", Title: "Subject", Type: "text", Holder: e.Subject, Desc: ph, Class: "large-text"},
		gooField{ID: "heading", Title: "Email heading", Type: "text", Holder: e.Heading, Desc: ph, Class: "large-text"},
		gooField{ID: "additional_content", Title: "Additional content", Type: "textarea", Holder: "N/A", Desc: "Text to appear below the main email content. " + ph},
		gooField{ID: "email_type", Title: "Email type", Type: "select", Default: "html", Options: gooEmailTypes, Desc: "Choose which format of email to send.", Class: "email_type"},
		gooField{ID: "email_options_end", Type: "sectionend"})
	return fields
}

func (s *Server) gooEmailView(c *gin.Context, d *gooSettingsData, id string) {
	d.Kind = "fields"
	e := gooEmailDefByID(id)
	if e == nil {
		d.Body, d.HideSave = `<p>Invalid email.</p>`, true
		return
	}
	d.Title = e.Title
	d.BackURL, d.BackText = gooAdminURL("settings", "tab", "email"), "Emails"
	d.Intro = template.HTML(`<h2>` + gooEsc(e.Title) + `</h2><p>` + gooEsc(e.Description) + `</p>`)
	st := s.gooEmailSettings(id)
	vals := map[string]any{}
	for k, v := range st {
		vals[k] = v
	}
	prefix := "woocommerce_" + id + "_"
	d.Body = s.gooFieldsHTML(c.Request.Context(), gooEmailFields(e), func(f string) string { return prefix + f }, gooMapValue(vals), nil)
}

func (s *Server) gooEmailPost(c *gin.Context, id string) error {
	e := gooEmailDefByID(id)
	if e == nil {
		return errors.New("Invalid email.")
	}
	st := map[string]any{}
	for k, v := range s.gooOptMap("email_" + id + "_settings") {
		st[k] = v
	}
	prefix := "woocommerce_" + id + "_"
	s.gooReadMapFields(c, gooEmailFields(e), func(f string) string { return prefix + f }, st)
	return s.gooSetOpts(c.Request.Context(), map[string]any{"email_" + id + "_settings": st})
}
