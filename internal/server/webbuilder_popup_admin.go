package server

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Appearance › Theme Builder › Popup Settings: the form for a popup template's trigger, frequency
// and look (the same PopupSettings the REST route takes). The Theme Builder screens are GET-only
// with a nonce, like the row actions beside it, so the form submits with GET.

const wbPopupAdminAction = "popup-settings"

type wbPopupAdminData struct {
	ID       int64
	Title    string
	Back     string
	Action   string // the form's action value
	Nonce    string
	Page     string
	S        webbuilder.PopupSettings
	CookieOn bool
	Error    string
}

// wbPopupAdmin handles action=popup-settings (show) and action=popup-settings-save (store).
func (s *Server) wbPopupAdmin(c *gin.Context, save bool) {
	id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
	t, err := s.store.WebBuilder().Template(c, id)
	if err != nil || t.Status == "trash" {
		c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
		return
	}
	if t.Type != webbuilder.TemplatePopup {
		c.String(http.StatusBadRequest, "Only a popup template has popup settings.")
		return
	}
	nonceAction := "wb-popup-settings_" + itoa64(id)
	d := wbPopupAdminData{ID: id, Title: t.Title, Back: wbThemeBuilderURL() + "&" + wbTBTypeParam + "=" + string(webbuilder.TemplatePopup),
		Action: wbPopupAdminAction + "-save", Nonce: s.nonce(c, nonceAction), Page: webbuilder.ThemeBuilderPage, CookieOn: s.gooikeOn()}
	d.S = s.wbPopupSettings(c, id)
	var notices []notice
	if save {
		if !s.checkNonce(c, nonceAction) {
			s.denyNonce(c)
			return
		}
		in, msg := wbPopupFromForm(c)
		if msg != "" {
			d.S = in
			notices = append(notices, notice{Type: "error", Message: template.HTML("<strong>" + html.EscapeString(msg) + "</strong>"), Dismissible: true})
		} else {
			b, _ := json.Marshal(in)
			if err := s.store.WebBuilder().SetTemplatePopup(c, id, string(b)); err != nil {
				c.String(http.StatusInternalServerError, "Could not save the popup settings.")
				return
			}
			s.wbTemplatesChanged(c)
			c.Redirect(http.StatusFound, wbThemeBuilderURL()+"&post="+itoa64(id)+"&action="+wbPopupAdminAction+"&saved=1")
			return
		}
	} else if c.Query("saved") != "" {
		notices = append(notices, notice{Type: "success", Message: "<strong>Popup settings saved.</strong>", Dismissible: true})
	}
	if d.Title == "" {
		d.Title = "(no title)"
	}
	s.adminPage(c, "themes?page="+webbuilder.ThemeBuilderPage, "Popup Settings", "appearance_page_"+webbuilder.ThemeBuilderPage,
		"admin/web-builder-popup", d, notices...)
}

// wbPopupFromForm reads the settings form; msg names the first invalid value.
func wbPopupFromForm(c *gin.Context) (webbuilder.PopupSettings, string) {
	num := func(key string, def int) int {
		if v, err := strconv.Atoi(c.Query(key)); err == nil {
			return v
		}
		return def
	}
	d := webbuilder.DefaultPopupSettings()
	in := webbuilder.PopupSettings{
		Trigger: c.Query("trigger"), Frequency: c.Query("frequency"), Position: c.Query("position"),
		Delay: num("delay", d.Delay), Scroll: num("scroll", d.Scroll), Days: num("days", d.Days),
		Version: num("version", d.Version), Width: num("width", d.Width), Selector: strings.TrimSpace(c.Query("selector")),
		Overlay: c.Query("overlay") != "", AfterConsent: c.Query("after_consent") != "",
	}
	return in, webbuilder.ValidatePopup(in)
}
