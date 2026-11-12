package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Goocommerce › Extensions: WooCommerce's premium extensions, built in as modules that start switched
// off. A module registers itself in init() with gooRegisterExtension; its hooks (goo_hooks.go) check
// s.gooExtOn(key) so a switched-off module changes nothing. Each module has a settings screen
// (/wp-admin/goocommerce?page=ext&ext=<key>) rendered from its Fields, stored in the option
// "ext_<key>_settings", and may add its own admin pages (gooExtAdminPages / gooExtMenuItems).

type gooExtension struct {
	Key         string
	Name        string
	Description string
	Icon        string // dashicon
	Category    string // Merchandising, Store management, Shipping & delivery, Payments & checkout, Customer engagement
	Fields      []gooField
	// OnActivate / OnDeactivate run when the module is switched (create pages, schedule jobs).
	OnActivate   func(s *Server, ctx context.Context) error
	OnDeactivate func(s *Server, ctx context.Context) error
	// Pages are the module's admin screens (page=<Page>) linked from its card.
	Pages []gooExtMenuItem
}

var gooExtensions = map[string]*gooExtension{}

// gooRegisterExtension adds a module and its admin pages.
func gooRegisterExtension(e *gooExtension) {
	gooExtensions[e.Key] = e
	for _, p := range e.Pages {
		p.Module = e.Key
		gooExtMenuItems = append(gooExtMenuItems, p)
	}
}

// gooExtensionList is the modules sorted by name.
func gooExtensionList() []*gooExtension {
	out := make([]*gooExtension, 0, len(gooExtensions))
	for _, e := range gooExtensions {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// gooExtSettings is a module's settings over its field defaults.
func (s *Server) gooExtSettings(key string) map[string]any {
	out := map[string]any{}
	if e := gooExtensions[key]; e != nil {
		for _, f := range e.Fields {
			if f.Default != nil {
				out[f.ID] = f.Default
			}
		}
	}
	for k, v := range s.gooOptMap("ext_" + key + "_settings") {
		out[k] = v
	}
	return out
}

// gooExtString / gooExtBool / gooExtFloat / gooExtInt read one module setting.
func (s *Server) gooExtString(key, field string) string {
	switch v := s.gooExtSettings(key)[field].(type) {
	case string:
		return v
	case bool:
		if v {
			return "yes"
		}
		return "no"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

func (s *Server) gooExtBool(key, field string) bool {
	switch v := s.gooExtSettings(key)[field].(type) {
	case bool:
		return v
	case string:
		return v == "yes" || v == "1" || v == "true"
	case float64:
		return v != 0
	}
	return false
}

func (s *Server) gooExtFloat(key, field string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s.gooExtString(key, field)), 64)
	return f
}

func (s *Server) gooExtInt(key, field string) int {
	return int(s.gooExtFloat(key, field))
}

// gooSetExtension switches a module on or off.
func (s *Server) gooSetExtension(ctx context.Context, key string, on bool) error {
	e := gooExtensions[key]
	if e == nil {
		return fmt.Errorf("unknown extension %q", key)
	}
	m := map[string]any{}
	for k, v := range s.gooOptMap("extensions") {
		m[k] = v
	}
	m[key] = on
	if err := s.gooSetOpts(ctx, map[string]any{"extensions": m}); err != nil {
		return err
	}
	if on && e.OnActivate != nil {
		if err := e.OnActivate(s, ctx); err != nil {
			return err
		}
	}
	if !on && e.OnDeactivate != nil {
		if err := e.OnDeactivate(s, ctx); err != nil {
			return err
		}
	}
	s.purgeAll(ctx)
	return nil
}

func init() {
	gooExtAdminPages["ext"] = func(s *Server, c *gin.Context) { s.gooAdminExtSettings(c) }
}

// gooAdminExtensions is Goocommerce › Extensions: the module cards with their switches.
func (s *Server) gooAdminExtensions(c *gin.Context) {
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-extensions") {
			s.denyNonce(c)
			return
		}
		key, on := c.PostForm("extension"), c.PostForm("state") == "on"
		if err := s.gooSetExtension(c.Request.Context(), key, on); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=extensions&goo-error="+url.QueryEscape(err.Error()))
			return
		}
		msg := "Extension activated."
		if !on {
			msg = "Extension deactivated."
		}
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=extensions&goo-msg="+url.QueryEscape(msg)+"#ext-"+key)
		return
	}
	type card struct {
		*gooExtension
		On          bool
		SettingsURL string
		PageLinks   []gooExtMenuItem
	}
	var cards []card
	cats := map[string][]card{}
	var order []string
	for _, e := range gooExtensionList() {
		cd := card{gooExtension: e, On: s.gooExtOn(e.Key)}
		if len(e.Fields) > 0 {
			cd.SettingsURL = "/wp-admin/goocommerce?page=ext&ext=" + e.Key
		}
		cd.PageLinks = e.Pages
		cards = append(cards, cd)
		cat := defaultStr(e.Category, "Extensions")
		if _, ok := cats[cat]; !ok {
			order = append(order, cat)
		}
		cats[cat] = append(cats[cat], cd)
	}
	sort.Strings(order)
	var groups []struct {
		Title string
		Cards []card
	}
	for _, t := range order {
		groups = append(groups, struct {
			Title string
			Cards []card
		}{t, cats[t]})
	}
	active := 0
	for _, cd := range cards {
		if cd.On {
			active++
		}
	}
	s.adminPage(c, "goocommerce?page=extensions", "Extensions", "goocommerce_page_goo-extensions", "admin/goo-extensions",
		map[string]any{"Groups": groups, "Nonce": s.nonce(c, "goo-extensions"), "Total": len(cards), "Active": active}, gooFlash(c)...)
}

// gooAdminExtSettings is a module's settings screen.
func (s *Server) gooAdminExtSettings(c *gin.Context) {
	key := c.Query("ext")
	e := gooExtensions[key]
	if e == nil {
		c.String(http.StatusNotFound, "Unknown extension.")
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-ext-"+key) {
			s.denyNonce(c)
			return
		}
		vals := gooReadFields(c, e.Fields, s.gooExtSettings(key))
		if err := s.gooSetOpts(c.Request.Context(), map[string]any{"ext_" + key + "_settings": vals}); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=ext&ext="+key+"&goo-error="+url.QueryEscape(err.Error()))
			return
		}
		s.purgeAll(c.Request.Context())
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=ext&ext="+key+"&goo-msg="+url.QueryEscape("Your settings have been saved."))
		return
	}
	for _, f := range e.Fields {
		if f.Type == "image" {
			s.enqueueMedia(c, 0)
			addLateFooterHTML(c, template.HTML(gooImageFieldJS))
			break
		}
	}
	s.adminPage(c, "goocommerce?page=extensions", e.Name+" settings", "goocommerce_page_goo-extensions", "admin/goo-ext-settings",
		map[string]any{"Ext": e, "On": s.gooExtOn(key), "Form": s.gooExtFieldsHTML(c.Request.Context(), e.Fields, s.gooExtSettings(key)),
			"Nonce": s.nonce(c, "goo-ext-"+key)}, gooFlash(c)...)
}

// gooFlash turns ?goo-msg= / ?goo-error= into notices.
func gooFlash(c *gin.Context) []notice {
	var out []notice
	if m := c.Query("goo-msg"); m != "" {
		out = append(out, notice{Type: "success", Message: template.HTML(html.EscapeString(m)), Dismissible: true})
	}
	if e := c.Query("goo-error"); e != "" {
		out = append(out, notice{Type: "error", Message: template.HTML(html.EscapeString(e))})
	}
	return out
}

// gooReadFields reads posted values of fields (name = field id), keeping current values of
// fields without input; checkboxes absent from the form are false.
func gooReadFields(c *gin.Context, fields []gooField, current map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range current {
		out[k] = v
	}
	for _, f := range fields {
		switch f.Type {
		case "title", "sectionend", "html":
			continue
		case "checkbox":
			out[f.ID] = c.PostForm(f.ID) != ""
		case "multiselect", "multi_select_countries":
			out[f.ID] = c.PostFormArray(f.ID + "[]")
		case "image":
			n, err := strconv.ParseInt(strings.TrimSpace(c.PostForm(f.ID)), 10, 64)
			if err != nil || n < 0 {
				n = 0
			}
			out[f.ID] = float64(n)
		case "number":
			if v := strings.TrimSpace(c.PostForm(f.ID)); v != "" {
				n, err := strconv.ParseFloat(v, 64)
				if err == nil {
					out[f.ID] = n
				}
			} else {
				out[f.ID] = ""
			}
		default:
			out[f.ID] = strings.TrimSpace(c.PostForm(f.ID))
		}
	}
	return out
}

// gooExtFieldsHTML renders fields as a WordPress form-table (the WC settings API look).
func (s *Server) gooExtFieldsHTML(ctx context.Context, fields []gooField, vals map[string]any) template.HTML {
	var b strings.Builder
	str := func(id string) string {
		switch v := vals[id].(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			if v {
				return "yes"
			}
			return "no"
		case int:
			return strconv.Itoa(v)
		}
		return ""
	}
	open := false
	openTable := func() {
		if !open {
			b.WriteString(`<table class="form-table"><tbody>`)
			open = true
		}
	}
	closeTable := func() {
		if open {
			b.WriteString(`</tbody></table>`)
			open = false
		}
	}
	for _, f := range fields {
		id := html.EscapeString(f.ID)
		switch f.Type {
		case "title":
			closeTable()
			if f.Title != "" {
				b.WriteString(`<h2>` + f.Title + `</h2>`)
			}
			if f.Desc != "" {
				b.WriteString(`<p>` + f.Desc + `</p>`)
			}
			continue
		case "sectionend":
			closeTable()
			continue
		}
		openTable()
		b.WriteString(`<tr valign="top"><th scope="row" class="titledesc"><label for="` + id + `">` + f.Title + `</label></th><td class="forminp forminp-` + f.Type + `">`)
		v := str(f.ID)
		switch f.Type {
		case "checkbox":
			checked := ""
			if v == "yes" || v == "1" || v == "true" {
				checked = " checked"
			}
			b.WriteString(`<fieldset><label for="` + id + `"><input type="checkbox" name="` + id + `" id="` + id + `" value="1"` + checked + `> ` + f.Desc + `</label></fieldset>`)
		case "textarea":
			b.WriteString(`<textarea name="` + id + `" id="` + id + `" rows="5" class="large-text">` + html.EscapeString(v) + `</textarea>`)
		case "select":
			b.WriteString(`<select name="` + id + `" id="` + id + `">`)
			for _, o := range f.Options {
				sel := ""
				if o.Value == v {
					sel = " selected"
				}
				b.WriteString(`<option value="` + html.EscapeString(o.Value) + `"` + sel + `>` + o.Label + `</option>`)
			}
			b.WriteString(`</select>`)
		case "multiselect":
			have := map[string]bool{}
			if list, ok := vals[f.ID].([]any); ok {
				for _, x := range list {
					if xs, ok := x.(string); ok {
						have[xs] = true
					}
				}
			}
			b.WriteString(`<select multiple name="` + id + `[]" id="` + id + `" size="6">`)
			for _, o := range f.Options {
				sel := ""
				if have[o.Value] {
					sel = " selected"
				}
				b.WriteString(`<option value="` + html.EscapeString(o.Value) + `"` + sel + `>` + o.Label + `</option>`)
			}
			b.WriteString(`</select>`)
		case "image":
			// An attachment ID picked from the Media Library (wp.media, enqueued by gooAdminExtSettings).
			img := ""
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				if a, err := s.store.GetAttachment(ctx, n); err == nil {
					img = `<img src="` + html.EscapeString(s.uploadURL(ctx, a.FilePath)) + `" alt="" style="max-width:150px;max-height:100px;display:block;margin-bottom:6px">`
				}
			}
			b.WriteString(`<span class="goo-image-field" data-title="` + html.EscapeString(gxaStripTags(f.Title)) + `"><span class="goo-image-preview">` + img + `</span>` +
				`<input type="number" min="0" step="1" name="` + id + `" id="` + id + `" value="` + html.EscapeString(v) + `" style="width:120px"> ` +
				`<button type="button" class="button goo-image-select">Select image</button> <button type="button" class="button-link goo-image-remove">Remove</button></span>`)
		case "number", "price":
			attrs := ""
			if f.Min != "" {
				attrs += ` min="` + f.Min + `"`
			}
			if f.Step != "" {
				attrs += ` step="` + f.Step + `"`
			} else if f.Type == "price" {
				attrs += ` step="any"`
			}
			typ := "number"
			if f.Type == "price" {
				typ = "text"
				attrs = ` class="wc_input_price"`
			}
			b.WriteString(`<input type="` + typ + `" name="` + id + `" id="` + id + `" value="` + html.EscapeString(v) + `"` + attrs + ` style="width:120px">`)
		case "color":
			b.WriteString(`<input type="color" name="` + id + `" id="` + id + `" value="` + html.EscapeString(v) + `">`)
		default:
			typ := "text"
			if f.Type == "email" || f.Type == "password" || f.Type == "date" || f.Type == "url" {
				typ = f.Type
			}
			ph := ""
			if f.Holder != "" {
				ph = ` placeholder="` + html.EscapeString(f.Holder) + `"`
			}
			b.WriteString(`<input type="` + typ + `" name="` + id + `" id="` + id + `" value="` + html.EscapeString(v) + `" class="regular-text"` + ph + `>`)
		}
		if f.Desc != "" && f.Type != "checkbox" {
			b.WriteString(`<p class="description">` + f.Desc + `</p>`)
		}
		b.WriteString(`</td></tr>`)
	}
	closeTable()
	return template.HTML(b.String())
}

// gooImageFieldJS drives the module settings' image fields: the Media Library modal fills the
// attachment ID and the preview.
const gooImageFieldJS = `<script>jQuery(function($){$('.goo-image-field').each(function(){var $w=$(this),$i=$w.find('input'),$p=$w.find('.goo-image-preview'),frame;` +
	`$w.find('.goo-image-select').on('click',function(e){e.preventDefault();if(!window.wp||!wp.media){return}if(!frame){frame=wp.media({title:$w.data('title'),library:{type:'image'},button:{text:'Use this image'},multiple:false});` +
	`frame.on('select',function(){var a=frame.state().get('selection').first().toJSON(),u=(a.sizes&&a.sizes.medium?a.sizes.medium.url:a.url);$i.val(a.id);$p.empty().append($('<img alt="" style="max-width:150px;max-height:100px;display:block;margin-bottom:6px">').attr('src',u))})}frame.open()});` +
	`$w.find('.goo-image-remove').on('click',function(e){e.preventDefault();$i.val('');$p.empty()})})});</script>`
