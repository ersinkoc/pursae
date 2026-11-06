package server

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Site settings of the Pursae Web Builder: while the builder is active the editor manages what the
// Customizer used to (site identity, homepage, additional CSS). GET/PUT web-builder/v2/site-settings
// reuse the Customizer's validation (czSanitize) and storage, so both screens always agree.

type wbSiteImage struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
}

type wbSitePage struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type wbSiteSettings struct {
	Blogname        string       `json:"blogname"`
	Blogdescription string       `json:"blogdescription"`
	SiteIcon        *wbSiteImage `json:"site_icon"`
	CustomLogo      *wbSiteImage `json:"custom_logo"`
	SupportsLogo    bool         `json:"supports_logo"`
	ShowOnFront     string       `json:"show_on_front"`
	PageOnFront     int64        `json:"page_on_front"`
	PageForPosts    int64        `json:"page_for_posts"`
	CustomCSS       string       `json:"custom_css"`
	Pages           []wbSitePage `json:"pages"`
	CanUpload       bool         `json:"can_upload"`
}

func (s *Server) wbSiteImageOf(c *gin.Context, id int64) *wbSiteImage {
	v := s.czImage(c, id)
	if v.ID == 0 {
		return nil
	}
	return &wbSiteImage{ID: v.ID, URL: v.URL}
}

// wbSiteSettingsView is the current state of the settings the editor shows.
func (s *Server) wbSiteSettingsView(c *gin.Context, u *store.User) wbSiteSettings {
	t := s.activeTheme()
	mods := s.themeMods(t.Slug)
	out := wbSiteSettings{
		Blogname:        s.settings.String("blogname"),
		Blogdescription: s.settings.String("blogdescription"),
		SiteIcon:        s.wbSiteImageOf(c, int64(s.settings.Int("site_icon"))),
		SupportsLogo:    themeSupportsLogo(t),
		ShowOnFront:     s.settings.String("show_on_front"),
		PageOnFront:     int64(s.settings.Int("page_on_front")),
		PageForPosts:    int64(s.settings.Int("page_for_posts")),
		CustomCSS:       modString(mods, "custom_css", ""),
		Pages:           []wbSitePage{},
		CanUpload:       u.Can("upload_files", nil),
	}
	if out.ShowOnFront != "page" {
		out.ShowOnFront = "posts"
	}
	if out.SupportsLogo {
		out.CustomLogo = s.wbSiteImageOf(c, int64(modInt(mods, "custom_logo", 0)))
	}
	pages, _, _ := s.store.QueryPosts(c, store.PostQuery{Types: []string{"page"}, Statuses: []string{"publish"}, OrderBy: "title", Order: "asc", PerPage: -1})
	for _, p := range pages {
		out.Pages = append(out.Pages, wbSitePage{ID: p.ID, Title: p.Title})
	}
	return out
}

// wbRESTSiteSettings is GET site-settings.
func (s *Server) wbRESTSiteSettings(c *gin.Context, _ *restRequest) {
	u := wbUser(c, "edit_theme_options")
	if u == nil {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, s.wbSiteSettingsView(c, u))
}

// wbRESTSiteSettingsSave is PUT site-settings: a partial object of blogname, blogdescription,
// site_icon (attachment id, 0 removes), custom_logo, show_on_front, page_on_front, page_for_posts and
// custom_css. It answers with the new state.
func (s *Server) wbRESTSiteSettingsSave(c *gin.Context, r *restRequest) {
	u := wbUser(c, "edit_theme_options")
	if u == nil {
		return
	}
	raw, ok := wbBody(c, r, 512<<10)
	if !ok {
		return
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil || in == nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_json", "The settings must be a JSON object.")
		return
	}
	values := map[string]string{}
	for key, v := range in {
		id := key
		switch key {
		case "blogname", "blogdescription", "site_icon", "show_on_front", "page_on_front", "page_for_posts", "custom_css":
		case "custom_logo":
			id = "mod:custom_logo"
		default:
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Unknown setting: "+wbClip(key, 64), map[string]any{"key": wbClip(key, 64)})
			return
		}
		var str string
		var num json.Number
		switch {
		case json.Unmarshal(v, &str) == nil:
			values[id] = str
		case json.Unmarshal(v, &num) == nil:
			values[id] = num.String()
		default:
			values[id] = "" // null clears
		}
		if key == "custom_logo" && !themeSupportsLogo(s.activeTheme()) {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "The active theme has no custom logo.", map[string]any{"key": key})
			return
		}
	}
	t := s.activeTheme()
	opts, mods, err := s.czSanitize(c, t, values)
	if err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", err.Error())
		return
	}
	save := map[string]any{"theme_mods_" + t.Slug: mods}
	for k, v := range opts {
		save[k] = v
	}
	if err := s.settings.Set(c, save); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the settings.")
		return
	}
	if id, ok := opts["site_icon"].(int64); ok && id > 0 {
		s.siteIconSizes(c, id)
	}
	s.purgeAll(c)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, s.wbSiteSettingsView(c, u))
}

// customizeURL is where "Customize" leads: the Customizer, or the Pursae Web Builder's Theme Builder
// while the builder is active (its menu has the site settings).
func (s *Server) customizeURL() string {
	if s.wbOn() {
		return "/wp-admin/themes?page=" + webbuilder.ThemeBuilderPage
	}
	return "/wp-admin/customize"
}

// wbHideCustomizer drops Appearance › Customize from the admin menu while the builder is active.
func (s *Server) wbHideCustomizer(menu []*menuTop) []*menuTop {
	if !s.wbOn() {
		return menu
	}
	for _, top := range menu {
		subs := top.Sub[:0]
		for _, sd := range top.Sub {
			if sd.Slug != "customize" {
				subs = append(subs, sd)
			}
		}
		top.Sub = subs
		if len(top.Sub) > 0 {
			top.Sub[0].First = true
		}
	}
	return menu
}
