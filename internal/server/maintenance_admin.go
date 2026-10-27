package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// The Maintenance screen (wp-admin/admin?page=maintenance): the original's top-level menu with its
// tabs, the status switch in the header, the admin bar menu, export/import/reset and, from the
// Pro version, the subscribers list. Only administrators (manage_options) reach any of it, and
// only while the plugin is active.

const maintNonce = "maintenance-options"

var maintTabs = []struct{ Key, Label string }{
	{"general", "General"}, {"themes", "Themes"}, {"design", "Design"}, {"modules", "Modules"},
	{"seo", "SEO"}, {"access", "Access"}, {"advanced", "Advanced"}, {"subscribers", "Subscribers"},
}

type maintAdminData struct {
	Tab          string
	Tabs         []struct{ Key, Label string }
	S            maintSettings
	Nonce        string
	HomeURL      string
	PreviewURL   string
	SecretURL    string
	Themes       []*maintTheme
	Fonts        []struct{ Key, Label, Stack string }
	Networks     []maintAdminNetwork
	Roles        []maintAdminRole
	Subscribers  []maintSubscriber
	Closed       bool   // visitors get the maintenance page right now
	EndPassed    bool   // the countdown's end is in the past
	TimeZone     string // the site's time zone, for the countdown field
	CanUpload    bool
	MyIP         string
	CacheEnabled bool
}

type maintAdminNetwork struct{ Key, Label, Icon, URL string }

type maintAdminRole struct {
	Slug, Name string
	Checked    bool
}

// maintMenu adds the top-level Maintenance menu, just above Settings, while the plugin is active.
func (s *Server) maintMenu(menu []*menuTop, u *store.User, screen string) []*menuTop {
	if !s.maintOn() || !u.Can("manage_options", nil) {
		return menu
	}
	slug := "admin?page=" + maintSlug
	title := "Maintenance"
	if s.maintLoad().Enabled {
		title += ` <span class="awaiting-mod count-1" style="background:#d63638"><span aria-hidden="true">On</span><span class="screen-reader-text">Maintenance mode is on</span></span>`
	}
	top := &menuTop{Slug: slug, Title: template.HTML(title), ID: "toplevel_page_maintenance", Icon: "dashicons-hammer", Current: screen == slug}
	for i, m := range menu {
		if m.ID == "menu-settings" {
			top.First, m.First = m.First, false
			return append(menu[:i], append([]*menuTop{top}, menu[i:]...)...)
		}
	}
	return append(menu, top)
}

// maintAdminBar is the toolbar node: the mode's state, a one-click toggle, Preview and Settings.
func (s *Server) maintAdminBar(c *gin.Context, u *store.User) template.HTML {
	if !s.maintOn() || !u.Can("manage_options", nil) {
		return ""
	}
	st := s.maintLoad()
	if !st.AdminBarMenu {
		return ""
	}
	_, closed := s.maintClosed(c)
	dot, state, toggle := "#00a32a", "OFF", "Turn Maintenance ON"
	if closed {
		dot, state, toggle = "#d63638", "ON", "Turn Maintenance OFF"
	}
	toggleURL := "/wp-admin/admin?page=maintenance&amp;action=toggle&amp;_wpnonce=" + s.nonce(c, "mtnc-toggle") +
		"&amp;_wp_http_referer=" + url.QueryEscape(c.Request.URL.RequestURI())
	return template.HTML(`<li role="group" id="wp-admin-bar-maintenance" class="menupop"><a class="ab-item" role="menuitem" aria-expanded="false" href="/wp-admin/admin?page=maintenance">` +
		`<span class="ab-icon dashicons dashicons-hammer" aria-hidden="true" style="top:2px"></span><span class="ab-label">Maintenance <span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:` + dot + `;margin-left:2px;vertical-align:1px" aria-hidden="true"></span><span class="screen-reader-text">is ` + state + `</span></span></a>` +
		`<div class="ab-sub-wrapper"><ul role="menu" id="wp-admin-bar-maintenance-default" class="ab-submenu">` +
		`<li role="group" id="wp-admin-bar-maintenance-toggle"><a class="ab-item" role="menuitem" href="` + toggleURL + `">` + toggle + `</a></li>` +
		`<li role="group" id="wp-admin-bar-maintenance-preview"><a class="ab-item" role="menuitem" href="/?` + maintPreviewParam + `" target="_blank" rel="noopener">Preview</a></li>` +
		`<li role="group" id="wp-admin-bar-maintenance-settings"><a class="ab-item" role="menuitem" href="/wp-admin/admin?page=maintenance">Settings</a></li></ul></div></li>`)
}

// maintPluginRow is Maintenance-Go's row on the Plugins screen.
func (s *Server) maintPluginRow() pluginRow {
	r := pluginRow{Slug: maintSlug, Name: "Maintenance-Go", Version: "4.32.0", Native: true, Active: s.maintOn(),
		Description: "Put the site in maintenance or coming soon mode: a designed page with 503 and Retry-After for visitors, while logged-in users, whitelisted IPs and a secret access link see the site. Themes, countdown with auto-disable, subscribe form, social icons, login form, SEO and Google Analytics. While it is off, the site is never affected.",
		Author:      "WordPress Go"}
	if r.Active {
		r.SettingsURL = "/wp-admin/admin?page=maintenance"
	}
	return r
}

// maintAdmin serves admin?page=maintenance.
func (s *Server) maintAdmin(c *gin.Context) {
	u := currentUser(c)
	if !s.maintOn() || !u.Can("manage_options", nil) { // an inactive plugin's screen is gone, as in WordPress
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	back := "/wp-admin/admin?page=maintenance"
	switch c.Query("action") {
	case "toggle":
		if !s.checkNonce(c, "mtnc-toggle") {
			s.denyNonce(c)
			return
		}
		_, closed := s.maintClosed(c)
		if err := s.maintSetMode(c, !closed); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		msg := "off"
		if !closed {
			msg = "on"
		}
		to := safeRedirect(c.Query("_wp_http_referer"), back)
		if strings.HasPrefix(to, back) { // the notice belongs to this screen; elsewhere the admin bar shows the state
			to = back + "&mtnc-msg=" + msg
		}
		c.Redirect(http.StatusFound, to)
		return
	case "export", "export_subscribers", "reset":
		if !s.checkNonce(c, maintNonce) {
			s.denyNonce(c)
			return
		}
		s.maintAdminAction(c, c.Query("action"))
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, maintNonce) {
			s.denyNonce(c)
			return
		}
		s.maintAdminPost(c)
		return
	}

	st := s.maintLoad()
	tab := c.DefaultQuery("tab", "general")
	valid := false
	for _, t := range maintTabs {
		valid = valid || t.Key == tab
	}
	if !valid {
		tab = "general"
	}
	d := maintAdminData{Tab: tab, Tabs: maintTabs, S: st, Nonce: s.nonce(c, maintNonce), HomeURL: s.links(c).HomeURL("/"),
		Themes: maintThemes, Fonts: maintFonts, TimeZone: s.loc().String(), CanUpload: u.Can("upload_files", nil),
		MyIP: clientIP(c), CacheEnabled: s.settings.Bool("wpgo_cache_enabled")}
	d.PreviewURL = d.HomeURL + "?" + maintPreviewParam
	if st.SecretKey != "" {
		d.SecretURL = d.HomeURL + "?" + url.QueryEscape(st.SecretKey)
	}
	_, d.Closed = s.maintClosed(c)
	if end, ok := s.maintEnd(st); ok && !end.After(time.Now()) {
		d.EndPassed = true
	}
	for _, n := range maintSocialNetworks {
		d.Networks = append(d.Networks, maintAdminNetwork{Key: n.Key, Label: n.Label, Icon: n.Icon, URL: st.Social[n.Key]})
	}
	roles, _ := s.store.Roles(c)
	for _, r := range roles {
		ar := maintAdminRole{Slug: r.Slug, Name: r.Name}
		for _, b := range st.BypassRoles {
			ar.Checked = ar.Checked || b == r.Slug
		}
		d.Roles = append(d.Roles, ar)
	}
	if tab == "subscribers" {
		d.Subscribers, _ = s.maintSubscribers(c)
	}
	if (tab == "general" || tab == "design" || tab == "seo") && d.CanUpload {
		s.enqueueMedia(c, 0)
		s.pageAssets(c).AddInline("media-editor", maintMediaJS)
	}
	var notices []notice
	switch c.Query("mtnc-msg") {
	case "on":
		notices = append(notices, notice{Type: "success", Message: "<strong>Maintenance mode is on.</strong> Visitors now see the maintenance page.", Dismissible: true})
	case "off":
		notices = append(notices, notice{Type: "success", Message: "<strong>Maintenance mode is off.</strong> The site is open to everyone.", Dismissible: true})
	case "imported":
		notices = append(notices, notice{Type: "success", Message: "Settings imported.", Dismissible: true})
	case "reset":
		notices = append(notices, notice{Type: "success", Message: "All settings were reset to their defaults.", Dismissible: true})
	case "theme":
		notices = append(notices, notice{Type: "success", Message: "Theme activated. Its colors and layout are now on the Design tab.", Dismissible: true})
	case "deleted":
		notices = append(notices, notice{Type: "success", Message: "Subscribers deleted.", Dismissible: true})
	}
	if e := c.Query("mtnc-error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(e))})
	}
	if d.EndPassed && st.Enabled && !st.AutoDisable {
		notices = append(notices, notice{Type: "warning", Message: "The countdown has ended, but maintenance mode stays on because <em>Disable maintenance mode when the countdown ends</em> is off."})
	}
	s.adminPage(c, "admin?page=maintenance", "Maintenance", "toplevel_page_maintenance", "admin/maintenance", d, notices...)
}

// maintAdminPost saves the fields of one tab (the other tabs' values stay), activates a theme,
// imports settings or deletes subscribers.
func (s *Server) maintAdminPost(c *gin.Context) {
	tab := c.PostForm("tab")
	back := "/wp-admin/admin?page=maintenance&tab=" + url.QueryEscape(tab)
	st := s.maintLoad()
	str := func(k string) string { return strings.TrimSpace(c.PostForm(k)) }
	text := func(k string) string { return strings.TrimSpace(strings.ReplaceAll(c.PostForm(k), "\r\n", "\n")) }
	on := func(k string) bool { v := c.PostForm(k); return v != "" && v != "0" }
	num := func(k string, def, lo, hi int) int {
		n, err := strconv.Atoi(str(k))
		if err != nil {
			return def
		}
		return min(max(n, lo), hi)
	}
	color := func(k, def string) string { return maintColor(str(k), def) }
	switch tab {
	case "general":
		st.Enabled = on("status")
		st.Mode = map[bool]string{true: "coming_soon", false: "maintenance"}[str("mode") == "coming_soon"]
		st.Heading = str("header_text")
		st.Content = text("content")
		st.FooterText = text("footer_text")
		st.LogoURL = maintMediaURL(str("logo"))
		st.LogoWidth = num("logo_width", 220, 16, 1200)
	case "themes":
		if t := maintThemeByKey(c.PostForm("theme")); t != nil {
			t.apply(&st)
			back = "/wp-admin/admin?page=maintenance&tab=themes&mtnc-msg=theme"
		}
	case "design":
		st.BgType = map[string]string{"image": "image", "gradient": "gradient"}[str("background_type")]
		if st.BgType == "" {
			st.BgType = "color"
		}
		st.BgImageURL = maintMediaURL(str("background_image"))
		st.BgSize, st.BgPosition = str("background_size_opt"), str("background_position")
		st.BgColor, st.BgColor2 = color("background_color", st.BgColor), color("background_color_2", st.BgColor2)
		st.BgBlur = num("background_blur", 0, 0, 40)
		st.Overlay = num("overlay_opacity", 0, 0, 90)
		if p := str("content_position"); p == "middle" || p == "left" || p == "right" || p == "top" || p == "bottom" {
			st.ContentPosition = p
		}
		st.ContentWidth = num("content_width", 760, 320, 1600)
		st.ContentBox = on("content_overlay")
		st.FontFamily = str("body_font")
		st.FontSize = num("body_font_size", 16, 12, 32)
		st.FontColor, st.HeadingColor = color("body_font_color", st.FontColor), color("heading_color", st.HeadingColor)
		st.LinkColor, st.AccentColor = color("body_link_color", st.LinkColor), color("controls_color", st.AccentColor)
		st.CustomCSS = text("custom_css")
	case "modules":
		st.Countdown = on("countdown")
		st.CountdownEnd = str("expiry_date")
		st.CountdownLabel = str("countdown_label")
		st.AutoDisable = on("auto_disable")
		st.Progress = on("progress")
		st.ProgressPercent = num("progress_percent", 75, 0, 100)
		st.ProgressLabel = str("progress_label")
		st.Subscribe = on("subscribe")
		st.SubscribeTitle, st.SubscribeButton, st.SubscribeThanks = str("subscribe_title"), str("subscribe_button"), str("subscribe_thanks")
		st.SubscribeNotify = on("subscribe_notify")
		st.GDPR, st.GDPRText, st.GDPRPolicyLink = on("gdpr"), str("gdpr_text"), on("gdpr_policy_link")
		st.Social = map[string]string{}
		for _, n := range maintSocialNetworks {
			if v := str("social_" + n.Key); v != "" && (n.Key == "email" || maintMediaURL(v) != "") {
				st.Social[n.Key] = v
			}
		}
		st.SocialNewTab = on("social_target")
		st.CustomHTML = text("custom_html")
		if st.SubscribeButton == "" {
			st.SubscribeButton = maintDefaults().SubscribeButton
		}
	case "seo":
		st.Title, st.MetaDescription = str("title"), text("description")
		st.FaviconURL, st.SocialImageURL = maintMediaURL(str("favicon")), maintMediaURL(str("social_preview"))
		st.NoIndex, st.ExcludeSE = on("noindex"), on("excludese")
		st.RetryAfter = num("retry_after", 1, 1, 168)
		if a := strings.ToUpper(str("analytics")); a == "" || maintGARe.MatchString(a) {
			st.Analytics = a
		} else {
			c.Redirect(http.StatusFound, back+"&mtnc-error="+url.QueryEscape("That is not a Google Analytics measurement ID (G-XXXXXXX)."))
			return
		}
	case "access":
		st.ShowLoggedIn = on("show_logged_in")
		st.BypassRoles = nil
		for _, r := range c.PostFormArray("roles[]") {
			st.BypassRoles = append(st.BypassRoles, r)
		}
		st.IPWhitelist = text("ip_whitelist")
		st.URLRules = map[string]string{"whitelist": "whitelist", "blacklist": "blacklist"}[str("per_url_settings")]
		st.URLList = text("per_url_enable_disable")
		st.SecretKey = maintSecretKey(str("direct_access_link"))
		st.SecretHours = num("direct_access_hours", 24, 1, 24*365)
		st.SitePassword = c.PostForm("site_password")
		if st.SitePassword != "" && len(st.SitePassword) < 4 {
			c.Redirect(http.StatusFound, back+"&mtnc-error="+url.QueryEscape("The site password must have at least 4 characters."))
			return
		}
		st.PasswordButton = on("password_button")
		st.PasswordMessage, st.PasswordButtonT, st.PasswordWrong = str("login_message"), str("login_button_text"), str("login_wrong_password_text")
		st.LoginButton, st.LoginTooltip = on("login_button"), str("wplogin_button_tooltip")
	case "advanced":
		if c.PostForm("import") != "" {
			s.maintImport(c)
			return
		}
		st.AdminBarMenu, st.BlockREST, st.BlockFeeds = on("toolbar_menu"), on("block_rest"), on("block_feeds")
	case "subscribers":
		var ids []int64
		for _, v := range c.PostFormArray("subscriber[]") {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				ids = append(ids, n)
			}
		}
		if c.PostForm("action") == "delete" && len(ids) > 0 {
			_, _ = s.store.Pool.Exec(c, `DELETE FROM maintenance_subscribers WHERE id = ANY($1)`, ids)
			c.Redirect(http.StatusFound, back+"&mtnc-msg=deleted")
			return
		}
		c.Redirect(http.StatusFound, back)
		return
	default:
		c.Redirect(http.StatusFound, "/wp-admin/admin?page=maintenance")
		return
	}
	if err := s.maintSave(c, st); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if !strings.Contains(back, "mtnc-msg") {
		back += "&settings-updated=true"
	}
	c.Redirect(http.StatusFound, back)
}

var maintGARe = regexp.MustCompile(`^(G|GT|AW|DC|UA)-[A-Z0-9-]{4,20}$`)

// maintMediaURL keeps site-relative and http(s) URLs (media library picks and pasted links).
func maintMediaURL(v string) string {
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	if u, err := url.Parse(v); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		return v
	}
	return ""
}

// maintSecretKey is the access link's key: letters, digits, dashes and underscores.
func maintSecretKey(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	k := b.String()
	if k == maintPreviewParam || len(k) > 64 {
		return ""
	}
	return k
}

// maintExport is the Export file: every setting except the site password, like the original's
// export of its options.
type maintExport struct {
	Plugin   string        `json:"plugin"`
	Version  string        `json:"version"`
	Exported string        `json:"exported"`
	Settings maintSettings `json:"settings"`
}

func (s *Server) maintAdminAction(c *gin.Context, action string) {
	switch action {
	case "export":
		st := s.maintLoad()
		st.SitePassword = ""
		raw, _ := json.MarshalIndent(maintExport{Plugin: "maintenance-go", Version: "1", Exported: time.Now().UTC().Format(time.RFC3339), Settings: st}, "", "  ")
		c.Header("Content-Disposition", `attachment; filename="maintenance-settings-`+time.Now().Format("2006-01-02")+`.json"`)
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	case "export_subscribers":
		subs, err := s.maintSubscribers(c)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"email", "consent", "subscribed"})
		for _, m := range subs {
			_ = w.Write([]string{maintCSVSafe(m.Email), strconv.FormatBool(m.Consent), m.Created.In(s.loc()).Format("2006-01-02 15:04:05")})
		}
		w.Flush()
		c.Header("Content-Disposition", `attachment; filename="maintenance-subscribers-`+time.Now().Format("2006-01-02")+`.csv"`)
		c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
	case "reset":
		st := maintDefaults()
		st.Enabled = s.maintLoad().Enabled // reset changes the page, not whether the site is closed
		if err := s.maintSave(c, st); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/admin?page=maintenance&tab=advanced&mtnc-msg=reset")
	}
}

// maintCSVSafe keeps a spreadsheet from reading a cell as a formula.
func maintCSVSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// maintImport reads an Export file. The current status and site password are kept: importing a
// design never opens or closes the site by itself.
func (s *Server) maintImport(c *gin.Context) {
	back := "/wp-admin/admin?page=maintenance&tab=advanced"
	fail := func(msg string) { c.Redirect(http.StatusFound, back+"&mtnc-error="+url.QueryEscape(msg)) }
	fh, err := c.FormFile("import_file")
	if err != nil {
		fail("Choose a settings file to import.")
		return
	}
	f, err := fh.Open()
	if err != nil {
		fail(err.Error())
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		fail(err.Error())
		return
	}
	var in struct {
		Plugin   string          `json:"plugin"`
		Settings json.RawMessage `json:"settings"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Plugin != "maintenance-go" || len(in.Settings) == 0 {
		fail("That is not a Maintenance-Go settings file.")
		return
	}
	cur := s.maintLoad()
	st := maintDefaults()
	if err := json.Unmarshal(in.Settings, &st); err != nil {
		fail("That is not a Maintenance-Go settings file.")
		return
	}
	st.Enabled, st.SitePassword = cur.Enabled, cur.SitePassword
	st = maintSanitize(st)
	if err := s.maintSave(c, st); err != nil {
		fail(err.Error())
		return
	}
	c.Redirect(http.StatusFound, back+"&mtnc-msg=imported")
}

// maintSanitize applies the save rules to values that did not come through the forms (imports).
func maintSanitize(st maintSettings) maintSettings {
	d := maintDefaults()
	for _, p := range []*string{&st.LogoURL, &st.BgImageURL, &st.FaviconURL, &st.SocialImageURL} {
		*p = maintMediaURL(*p)
	}
	for _, p := range []struct {
		v   *string
		def string
	}{{&st.BgColor, d.BgColor}, {&st.BgColor2, d.BgColor2}, {&st.FontColor, d.FontColor}, {&st.HeadingColor, d.HeadingColor}, {&st.LinkColor, d.LinkColor}, {&st.AccentColor, d.AccentColor}} {
		*p.v = maintColor(*p.v, p.def)
	}
	if st.Analytics != "" && !maintGARe.MatchString(st.Analytics) {
		st.Analytics = ""
	}
	st.SecretKey = maintSecretKey(st.SecretKey)
	for k, v := range st.Social {
		if k != "email" && maintMediaURL(v) == "" {
			delete(st.Social, k)
		}
	}
	return st
}

// maintMediaJS wires the "Select image" buttons to the media modal (the featured image frame).
const maintMediaJS = `jQuery( function( $ ) {
	$( '.mtnc-media' ).each( function() {
		var $wrap = $( this ), $input = $wrap.find( 'input[type=url], input[type=text]' ), $img = $wrap.find( 'img' ), frame;
		$wrap.find( '.mtnc-media-select' ).on( 'click', function( event ) {
			event.preventDefault();
			if ( ! frame ) {
				frame = wp.media( { title: $( this ).data( 'title' ), library: { type: 'image' }, button: { text: 'Use this image' }, multiple: false } );
				frame.on( 'select', function() {
					var a = frame.state().get( 'selection' ).first().toJSON();
					$input.val( a.url ).trigger( 'change' );
				} );
			}
			frame.open();
		} );
		$wrap.find( '.mtnc-media-remove' ).on( 'click', function( event ) {
			event.preventDefault();
			$input.val( '' ).trigger( 'change' );
		} );
		$input.on( 'change input', function() {
			var v = $input.val();
			$img.attr( 'src', v ).toggle( !! v );
			$wrap.find( '.mtnc-media-remove' ).toggle( !! v );
		} ).trigger( 'change' );
	} );
} );`
