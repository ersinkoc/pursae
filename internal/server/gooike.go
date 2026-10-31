package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Gooike is a native cookie consent plugin, on par with Complianz, CookieYes and Cookie Notice:
// a first-layer banner (Accept all / Reject all / Customize, equally prominent), a preferences
// modal with categories and their cookies, a script manager that keeps third-party scripts and
// embeds inert until their category is accepted, Google Consent Mode v2 signals, a cookie
// declaration with a simple scanner, and a consent log with retention.
//
// Like every native plugin it is off on a fresh install (native_gooike) and, while it is off,
// nothing of it is printed on the site, its REST routes, admin screens, assets and menu are gone.
// Its settings and the consent log stay when it is deactivated.
//
// It is built for the full-page cache: the banner, the inert scripts and the configuration are the
// same HTML for every visitor; the consent state lives in a first-party cookie read by the browser
// (gooike.js) and decisions are logged through POST /wp-json/gooike/v1/consent, which the cache
// never stores.

const (
	gooikeSlug      = "gooike"
	gooikeSetting   = "native_gooike"   // the plugin is active
	gooikeOptions   = "gooike_settings" // gooikeSettings as one JSON object
	gooikeVersion   = "1.0.0"
	gooikeCookie    = "gooike_consent"
	gooikeNamespace = "gooike/v1"
	gooikeAssets    = "/wp-content/plugins/gooike/assets"
	gooikePurgeJob  = "gooike_purge_logs"
)

// gooikeCategory is a consent category. The four built-in ones cannot be deleted; necessary is
// always granted. Signals are the Google Consent Mode v2 types the category grants.
type gooikeCategory struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	Enabled     bool     `json:"enabled"`
	Signals     []string `json:"signals"`
}

// gooikeScript is one entry of the script manager: a snippet of the admin's or a known service.
type gooikeScript struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Placement string `json:"placement"` // head, body (right after <body>), footer
	Code      string `json:"code"`
	Service   string `json:"service"`    // "" for a custom snippet, else a gooikeServices key
	ServiceID string `json:"service_id"` // the measurement / container / pixel ID of a service
	Enabled   bool   `json:"enabled"`
}

// gooikeCookieRow is a line of the cookie declaration.
type gooikeCookieRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Category string `json:"category"`
	Purpose  string `json:"purpose"`
	Duration string `json:"duration"`
}

// gooikeTexts are every visitor-facing string, editable on the Banner screen (and translatable
// through them: a multilingual site edits them per language or through its string translation).
type gooikeTexts struct {
	Title          string `json:"title"`
	Message        string `json:"message"`
	Accept         string `json:"accept"`
	Reject         string `json:"reject"`
	Customize      string `json:"customize"`
	Save           string `json:"save"`
	PrefsTitle     string `json:"prefs_title"`
	PrefsIntro     string `json:"prefs_intro"`
	AlwaysOn       string `json:"always_on"`
	PrivacyLink    string `json:"privacy_link"`
	CookieLink     string `json:"cookie_link"`
	NoticeLink     string `json:"notice_link"` // KVKK Aydınlatma Metni
	DoNotSell      string `json:"do_not_sell"`
	FloatLabel     string `json:"float_label"`
	ConsentID      string `json:"consent_id"`
	Close          string `json:"close"`
	CookiesLabel   string `json:"cookies_label"`
	EmbedText      string `json:"embed_text"`
	EmbedButton    string `json:"embed_button"`
	TableName      string `json:"table_name"`
	TableProvider  string `json:"table_provider"`
	TablePurpose   string `json:"table_purpose"`
	TableDuration  string `json:"table_duration"`
	TableEmpty     string `json:"table_empty"`
	PreferencesSC  string `json:"preferences_shortcode"` // the [gooike_preferences] link text
	WithdrawNotice string `json:"withdraw_notice"`
}

// gooikeDesign is the look of the banner: a theme and its customizations.
type gooikeDesign struct {
	Theme         string `json:"theme"`
	Position      string `json:"position"` // bottom, top, bottom-left, bottom-right, top-left, top-right, center
	Background    string `json:"background"`
	Text          string `json:"text"`
	Primary       string `json:"primary"`
	PrimaryText   string `json:"primary_text"`
	Font          string `json:"font"`
	Radius        int    `json:"radius"`
	FontSize      int    `json:"font_size"`
	Floating      bool   `json:"floating"`
	FloatPosition string `json:"float_position"` // left, right
	CustomCSS     string `json:"custom_css"`
}

// gooikeSettings are the plugin's options.
type gooikeSettings struct {
	PolicyVersion   int    `json:"policy_version"`
	ExpiryMonths    int    `json:"expiry_months"`
	PrivacyPage     int64  `json:"privacy_page"`
	PrivacyURL      string `json:"privacy_url"`
	CookiePolicyURL string `json:"cookie_policy_url"`
	NoticeURL       string `json:"notice_url"`
	Regulation      string `json:"regulation"` // gdpr (opt-in), ccpa (opt-out), kvkk (opt-in, Turkey)
	Geo             bool   `json:"geo"`
	GeoOther        string `json:"geo_other"` // regulation for visitors outside the EU/EEA/UK, Turkey and the US: none, gdpr, ccpa, kvkk
	RespectGPC      bool   `json:"respect_gpc"`
	RespectDNT      bool   `json:"respect_dnt"`
	ConsentMode     bool   `json:"consent_mode"`
	WaitForUpdate   int    `json:"wait_for_update"`
	AdsRedaction    bool   `json:"ads_data_redaction"`
	URLPassthrough  bool   `json:"url_passthrough"`
	LogConsent      bool   `json:"log_consent"`
	IPMode          string `json:"ip_mode"` // truncate, hash, none
	RetentionMonths int    `json:"retention_months"`
	ReloadOnChange  bool   `json:"reload_on_withdraw"`
	BlockYouTube    bool   `json:"block_youtube"`
	BlockVimeo      bool   `json:"block_vimeo"`
	BlockMaps       bool   `json:"block_maps"`
	EmbedCategory   string `json:"embed_category"`
	ExtraHosts      string `json:"extra_hosts"` // more sources for the CSP (connect-src / script-src)

	Texts      gooikeTexts       `json:"texts"`
	Design     gooikeDesign      `json:"design"`
	Categories []gooikeCategory  `json:"categories"`
	Scripts    []gooikeScript    `json:"scripts"`
	Cookies    []gooikeCookieRow `json:"cookies"`
}

func gooikeDefaultTexts() gooikeTexts {
	return gooikeTexts{
		Title:          "We value your privacy",
		Message:        "We use cookies to keep this site working, to understand how it is used and to show you relevant content. You can accept all cookies, reject the optional ones or choose which categories to allow. You can change your choice at any time.",
		Accept:         "Accept all",
		Reject:         "Reject all",
		Customize:      "Customize",
		Save:           "Save my choices",
		PrefsTitle:     "Cookie preferences",
		PrefsIntro:     "Choose which cookies this site may use. Necessary cookies are always on because the site cannot work without them. Your choice is stored in a cookie on this device.",
		AlwaysOn:       "Always active",
		PrivacyLink:    "Privacy policy",
		CookieLink:     "Cookie policy",
		NoticeLink:     "Information notice (KVKK)",
		DoNotSell:      "Do Not Sell or Share My Personal Information",
		FloatLabel:     "Cookie settings",
		ConsentID:      "Your consent ID",
		Close:          "Close",
		CookiesLabel:   "Cookies used",
		EmbedText:      "This content is hosted by %s, which may set cookies. Accept %s cookies to see it.",
		EmbedButton:    "Load content",
		TableName:      "Name",
		TableProvider:  "Provider",
		TablePurpose:   "Purpose",
		TableDuration:  "Duration",
		TableEmpty:     "No cookies of this category are declared.",
		PreferencesSC:  "Cookie settings",
		WithdrawNotice: "Your choice has been saved.",
	}
}

// gooikeDefaultCategories are the categories of a fresh install.
func gooikeDefaultCategories() []gooikeCategory {
	return []gooikeCategory{
		{Key: "necessary", Label: "Necessary", Builtin: true, Enabled: true, Signals: []string{"security_storage", "functionality_storage"},
			Description: "These cookies are needed for the site to work: page navigation, security, logging in, remembering your cookie choice. They cannot be switched off."},
		{Key: "preferences", Label: "Preferences", Builtin: true, Enabled: true, Signals: []string{"personalization_storage"},
			Description: "Preference (functional) cookies remember choices you make, such as your language or region, to give you a more personal experience."},
		{Key: "statistics", Label: "Statistics", Builtin: true, Enabled: true, Signals: []string{"analytics_storage"},
			Description: "Statistics (analytics) cookies help us understand how visitors use the site by collecting information anonymously, so we can improve it."},
		{Key: "marketing", Label: "Marketing", Builtin: true, Enabled: true, Signals: []string{"ad_storage", "ad_user_data", "ad_personalization"},
			Description: "Marketing cookies track visitors across websites to show relevant ads, measure campaigns and power embedded content from other sites such as videos and maps."},
	}
}

// gooikeSignals are the Google Consent Mode v2 consent types.
var gooikeSignals = []string{"ad_storage", "ad_user_data", "ad_personalization", "analytics_storage", "functionality_storage", "personalization_storage", "security_storage"}

// gooikeDefaultCookies are the cookies WordPress Go itself sets, pre-declared.
func gooikeDefaultCookies() []gooikeCookieRow {
	return []gooikeCookieRow{
		{ID: "c-consent", Name: gooikeCookie, Provider: "This site", Category: "necessary", Purpose: "Stores your cookie consent choice, its date, the policy version and your consent ID.", Duration: "12 months"},
		{ID: "c-login", Name: "wpgo_logged_in", Provider: "This site", Category: "necessary", Purpose: "Keeps a logged-in user signed in.", Duration: "Session / 14 days"},
		{ID: "c-comment", Name: "comment_author_*", Provider: "This site", Category: "preferences", Purpose: "Remembers the name, e-mail and website you entered when you left a comment.", Duration: "1 year"},
	}
}

func gooikeDefaults() gooikeSettings {
	st := gooikeSettings{
		PolicyVersion:   1,
		ExpiryMonths:    12,
		Regulation:      "gdpr",
		GeoOther:        "none",
		RespectGPC:      true,
		RespectDNT:      false,
		ConsentMode:     true,
		WaitForUpdate:   500,
		AdsRedaction:    true,
		LogConsent:      true,
		IPMode:          "truncate",
		RetentionMonths: 12,
		ReloadOnChange:  true,
		BlockYouTube:    true,
		BlockVimeo:      true,
		BlockMaps:       true,
		EmbedCategory:   "marketing",
		Texts:           gooikeDefaultTexts(),
		Categories:      gooikeDefaultCategories(),
		Cookies:         gooikeDefaultCookies(),
	}
	gooikeThemeByKey("classic").apply(&st.Design)
	return st
}

// gooikeOn reports whether the Gooike plugin is active: one cached option read.
func (s *Server) gooikeOn() bool {
	return s.settings.Bool(gooikeSetting)
}

// gooikeOnly is the middleware of Gooike's own routes: 404 while the plugin is off.
func (s *Server) gooikeOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.gooikeOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// gooikeLoad returns the saved settings over the defaults, repaired where needed.
func (s *Server) gooikeLoad() gooikeSettings {
	st := gooikeDefaults()
	if v := s.settings.Get(gooikeOptions); v != nil {
		if raw, err := json.Marshal(v); err == nil {
			st.Categories, st.Cookies = nil, nil
			_ = json.Unmarshal(raw, &st)
		}
	}
	return gooikeSanitize(st)
}

// gooikeSanitize keeps the settings consistent: the built-in categories exist, values are in range.
func gooikeSanitize(st gooikeSettings) gooikeSettings {
	d := gooikeDefaults()
	have := map[string]bool{}
	var cats []gooikeCategory
	for _, c := range st.Categories {
		c.Key = gooikeKey(c.Key)
		if c.Key == "" || have[c.Key] {
			continue
		}
		have[c.Key] = true
		if c.Key == "necessary" {
			c.Enabled = true
		}
		c.Signals = gooikeValidSignals(c.Signals)
		cats = append(cats, c)
	}
	for i, bc := range d.Categories { // a built-in category that went missing comes back, in its place
		if !have[bc.Key] {
			at := min(i, len(cats))
			cats = append(cats[:at], append([]gooikeCategory{bc}, cats[at:]...)...)
		}
	}
	for i := range cats {
		for _, bc := range d.Categories {
			if cats[i].Key == bc.Key {
				cats[i].Builtin = true
			}
		}
	}
	st.Categories = cats
	if st.Cookies == nil {
		st.Cookies = d.Cookies
	}
	if st.PolicyVersion < 1 {
		st.PolicyVersion = 1
	}
	if st.ExpiryMonths < 1 || st.ExpiryMonths > 24 {
		st.ExpiryMonths = 12
	}
	if st.RetentionMonths < 0 || st.RetentionMonths > 120 {
		st.RetentionMonths = 12
	}
	if st.WaitForUpdate < 0 || st.WaitForUpdate > 10000 {
		st.WaitForUpdate = 500
	}
	switch st.Regulation {
	case "gdpr", "ccpa", "kvkk":
	default:
		st.Regulation = "gdpr"
	}
	switch st.GeoOther {
	case "none", "gdpr", "ccpa", "kvkk":
	default:
		st.GeoOther = "none"
	}
	switch st.IPMode {
	case "truncate", "hash", "none":
	default:
		st.IPMode = "truncate"
	}
	if !st.hasCategory(st.EmbedCategory) || st.EmbedCategory == "necessary" {
		st.EmbedCategory = "marketing"
	}
	if gooikeThemeByKey(st.Design.Theme) == nil {
		gooikeThemeByKey("classic").apply(&st.Design)
	}
	st.Design = gooikeSanitizeDesign(st.Design)
	// Empty texts fall back to the defaults, so an upgrade adding a string never shows a blank.
	def, _ := json.Marshal(d.Texts)
	var dm, cur map[string]string
	_ = json.Unmarshal(def, &dm)
	raw, _ := json.Marshal(st.Texts)
	_ = json.Unmarshal(raw, &cur)
	for k, v := range dm {
		if strings.TrimSpace(cur[k]) == "" {
			cur[k] = v
		}
	}
	raw, _ = json.Marshal(cur)
	_ = json.Unmarshal(raw, &st.Texts)
	for i := range st.Scripts {
		sc := &st.Scripts[i]
		if !st.hasCategory(sc.Category) {
			sc.Category = "marketing"
		}
		if sc.Placement != "head" && sc.Placement != "body" && sc.Placement != "footer" {
			sc.Placement = "footer"
		}
		if sc.Service != "" && gooikeServiceByKey(sc.Service) == nil {
			sc.Service = ""
		}
	}
	for i := range st.Cookies {
		if !st.hasCategory(st.Cookies[i].Category) {
			st.Cookies[i].Category = "necessary"
		}
	}
	return st
}

func (st *gooikeSettings) hasCategory(key string) bool {
	for _, c := range st.Categories {
		if c.Key == key {
			return true
		}
	}
	return false
}

func (st *gooikeSettings) category(key string) *gooikeCategory {
	for i := range st.Categories {
		if st.Categories[i].Key == key {
			return &st.Categories[i]
		}
	}
	return nil
}

// visibleCategories are the categories shown to visitors: enabled ones, necessary first.
func (st *gooikeSettings) visibleCategories() []gooikeCategory {
	var out []gooikeCategory
	for _, c := range st.Categories {
		if c.Enabled || c.Key == "necessary" {
			out = append(out, c)
		}
	}
	return out
}

// optIn reports whether the regulation asks for consent before anything optional runs.
func (st *gooikeSettings) optIn() bool { return st.Regulation != "ccpa" }

var gooikeKeyRe = regexp.MustCompile(`[^a-z0-9_-]+`)

// gooikeKey is a category key: lowercase letters, digits, dashes and underscores.
func gooikeKey(v string) string {
	k := gooikeKeyRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(v)), "-")
	k = strings.Trim(k, "-")
	if len(k) > 40 {
		k = k[:40]
	}
	return k
}

func gooikeValidSignals(in []string) []string {
	var out []string
	for _, s := range in {
		for _, v := range gooikeSignals {
			if s == v {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// gooikeNewID is a short random identifier for scripts and cookie rows.
func gooikeNewID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// gooikeSave stores the settings and drops every cached page: the banner and scripts are in them.
func (s *Server) gooikeSave(ctx context.Context, st gooikeSettings) error {
	if err := s.settings.Set(ctx, map[string]any{gooikeOptions: gooikeSanitize(st)}); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// gooikeActivate switches the plugin on and schedules the daily consent log purge.
func (s *Server) gooikeActivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{gooikeSetting: true}); err != nil {
		return err
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence)
		SELECT $1, now() + interval '5 minutes', 'daily' WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE name = $1 AND recurrence <> '')`, gooikePurgeJob); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// gooikeDeactivate switches the plugin off. Settings and the consent log are kept.
func (s *Server) gooikeDeactivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{gooikeSetting: false}); err != nil {
		return err
	}
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM jobs WHERE name = $1`, gooikePurgeJob)
	s.purgeAll(ctx)
	return nil
}

// gooikePluginRow is Gooike's row on the Plugins screen.
func (s *Server) gooikePluginRow() pluginRow {
	r := pluginRow{Slug: gooikeSlug, Name: "Pursae Cookie", Version: gooikeVersion, Native: true, Active: s.gooikeOn(),
		Description: "Cookie consent the friendly way: a GDPR, KVKK and CCPA/CPRA ready banner with 14 themes, a preferences center, a script manager that blocks trackers and embeds until consent, Google Consent Mode v2, a cookie declaration with a scanner and a consent log. Works with the page cache. While it is off, nothing of it reaches the site.",
		Author:      "Pursae"}
	if r.Active {
		r.SettingsURL = "/wp-admin/gooike"
	}
	return r
}

// gooikePluginAction is the Activate / Deactivate link of the Plugins screen.
func (s *Server) gooikePluginAction(c *gin.Context, action string) {
	var err error
	if action == "activate" {
		err = s.gooikeActivate(c)
	} else {
		err = s.gooikeDeactivate(c)
	}
	if err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+template.URLQueryEscaper(err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
}

// gooikeMenuPages are the screens of the Gooike menu.
var gooikeMenuPages = []struct{ Page, Title string }{
	{"", "Dashboard"}, {"banner", "Banner"}, {"categories", "Categories &amp; Cookies"}, {"scripts", "Scripts"},
	{"logs", "Consent Logs"}, {"settings", "Settings"},
}

// gooikeMenu adds the Gooike top-level menu after Settings, only while the plugin is active.
func (s *Server) gooikeMenu(menu []*menuTop, u *store.User, screen string) []*menuTop {
	if !s.gooikeOn() || u == nil || !u.Can("manage_options", nil) {
		return menu
	}
	top := &menuTop{Slug: "gooike", Title: "Pursae Cookie", ID: "toplevel_page_gooike", Icon: "dashicons-shield"}
	for i, p := range gooikeMenuPages {
		slug := "gooike"
		if p.Page != "" {
			slug += "?page=" + p.Page
		}
		ms := &menuSub{Slug: slug, Title: template.HTML(p.Title), First: i == 0, Current: slug == screen}
		top.Current = top.Current || ms.Current
		top.Sub = append(top.Sub, ms)
	}
	at := len(menu)
	for i, m := range menu {
		if m.ID == "menu-settings" {
			at = i + 1
			m.Last = false
		}
	}
	top.Last = at == len(menu) || menu[at].Separator
	out := append([]*menuTop{}, menu[:at]...)
	out = append(out, top)
	return append(out, menu[at:]...)
}

// registerGooike wires the plugin's routes; every one answers 404 while it is off.
func (s *Server) registerGooike(r *gin.Engine, admin *gin.RouterGroup) {
	s.static(r, gooikeAssets, "gooike", s.gooikeOnly())
	admin.GET("/gooike", s.gooikeOnly(), s.gooikeAdmin)
	admin.POST("/gooike", s.gooikeOnly(), s.gooikeAdmin)
}
