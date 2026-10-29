package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Gooike's admin screens: /wp-admin/gooike?page=… (Dashboard, Banner, Categories & Cookies,
// Scripts, Consent Logs, Settings) and the banner preview. Administrators only (manage_options),
// only while the plugin is active (gooikeOnly).

const gooikeNonce = "gooike-admin"

type gooikeTextField struct {
	Key, Label, Help, Value string
	Long                    bool
}

var gooikeTextFields = []gooikeTextField{
	{Key: "title", Label: "Banner title"},
	{Key: "message", Label: "Banner message", Long: true},
	{Key: "accept", Label: "Accept all button"},
	{Key: "reject", Label: "Reject all button"},
	{Key: "customize", Label: "Customize button"},
	{Key: "save", Label: "Save choices button"},
	{Key: "prefs_title", Label: "Preferences title"},
	{Key: "prefs_intro", Label: "Preferences introduction", Long: true},
	{Key: "always_on", Label: "\"Always active\" label"},
	{Key: "privacy_link", Label: "Privacy policy link"},
	{Key: "cookie_link", Label: "Cookie policy link"},
	{Key: "notice_link", Label: "KVKK information notice link"},
	{Key: "do_not_sell", Label: "Do Not Sell or Share link (CCPA)", Help: "Replaces the Reject all button under CCPA/CPRA; also [gooike_do_not_sell]."},
	{Key: "float_label", Label: "Floating button label"},
	{Key: "consent_id", Label: "Consent ID label"},
	{Key: "close", Label: "Close button label"},
	{Key: "cookies_label", Label: "Cookie list heading"},
	{Key: "embed_text", Label: "Blocked embed text", Help: "The first %s is the service (YouTube…), the second the category.", Long: true},
	{Key: "embed_button", Label: "Blocked embed button"},
	{Key: "table_name", Label: "Cookie table: Name"},
	{Key: "table_provider", Label: "Cookie table: Provider"},
	{Key: "table_purpose", Label: "Cookie table: Purpose"},
	{Key: "table_duration", Label: "Cookie table: Duration"},
	{Key: "table_empty", Label: "Cookie table: no cookies"},
	{Key: "preferences_shortcode", Label: "[gooike_preferences] link text"},
	{Key: "withdraw_notice", Label: "Saved message (screen readers)"},
}

type gooikeAdminData struct {
	Page, Tab, Title string
	S                gooikeSettings
	Nonce            string
	Home             string
	CacheOn          bool

	Themes      []*gooikeTheme
	Theme       *gooikeTheme
	Positions   []string
	Fonts       []struct{ Key, Label, Stack string }
	TextFields  []gooikeTextField
	PreviewBase string

	Signals    []string
	EditCat    *gooikeCategory
	EditCookie *gooikeCookieRow
	Scan       *gooikeScanResult

	Services   []*gooikeService
	Embeds     []string
	EditScript *gooikeScript
	ScriptCode string

	Stats    gooikeStats
	Logs     []gooikeLogEntry
	Total    int
	PageNum  int
	NumPages int
	Filter   gooikeLogFilter
	Actions  []struct{ Key, Label string }
	Entry    *gooikeLogEntry
	Related  []gooikeLogEntry
	QS       string // the filters, for pagination and export links

	PagesList []struct {
		ID    int64
		Title string
	}
	PrivacyURL string
}

func (d *gooikeAdminData) CatLabel(key string) string {
	if c := d.S.category(key); c != nil {
		return c.Label
	}
	return key
}

func (d *gooikeAdminData) ServiceName(key string) string {
	if sv := gooikeServiceByKey(key); sv != nil {
		return sv.Name
	}
	return "Custom"
}

func (d *gooikeAdminData) HasSignal(c gooikeCategory, sig string) bool {
	return gooikeContains(c.Signals, sig)
}

func (d *gooikeAdminData) CookieCount(cat string) int {
	n := 0
	for _, ck := range d.S.Cookies {
		if ck.Category == cat {
			n++
		}
	}
	return n
}

func (d *gooikeAdminData) EnabledScripts() int {
	n := 0
	for _, sc := range d.S.Scripts {
		if sc.Enabled {
			n++
		}
	}
	return n
}

// gooikeAdmin serves every Gooike screen.
func (s *Server) gooikeAdmin(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	page := c.Query("page")
	if page == "preview" {
		s.gooikePreview(c)
		return
	}
	if c.Query("action") == "export" {
		if !s.checkNonce(c, gooikeNonce) {
			s.denyNonce(c)
			return
		}
		s.gooikeExportCSV(c)
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, gooikeNonce) {
			s.denyNonce(c)
			return
		}
		s.gooikeAdminPost(c)
		return
	}
	s.gooikeRender(c, page, nil)
}

// gooikeRender renders a screen; scan carries the results of a scan just run.
func (s *Server) gooikeRender(c *gin.Context, page string, scan *gooikeScanResult) {
	st := s.gooikeLoad()
	d := &gooikeAdminData{Page: page, Tab: c.Query("tab"), S: st, Nonce: s.nonce(c, gooikeNonce), Home: s.links(c).HomeURL("/"),
		CacheOn: s.settings.Bool("wpgo_cache_enabled"), Scan: scan}
	if scan != nil {
		d.Tab = "scan"
	}
	for _, p := range gooikeMenuPages {
		if p.Page == page {
			d.Title = strings.ReplaceAll(p.Title, "&amp;", "&")
		}
	}
	if d.Title == "" {
		page, d.Page, d.Title = "", "", "Dashboard"
	}
	screen := "gooike"
	if page != "" {
		screen += "?page=" + page
	}
	var notices []notice
	if m := c.Query("gooike-msg"); m != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(m)), Dismissible: true})
	}
	if e := c.Query("gooike-error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(e))})
	}
	switch page {
	case "":
		d.Stats = s.gooikeStats(c, 30)
		d.PrivacyURL = s.gooikePrivacyURL(c, &st)
		if d.PrivacyURL == "" {
			notices = append(notices, notice{Type: "warning", Message: template.HTML(`<strong>Gooike:</strong> no privacy policy is linked from the banner. Choose one under <a href="/wp-admin/gooike?page=settings">Gooike &rsaquo; Settings</a> or <a href="/wp-admin/options-privacy">Settings &rsaquo; Privacy</a>.`)})
		}
	case "banner":
		if d.Tab != "design" && d.Tab != "texts" {
			d.Tab = "themes"
		}
		d.Themes, d.Theme, d.Fonts = gooikeThemes, gooikeThemeByKey(st.Design.Theme), gooikeFonts
		d.Positions = gooikePositions[d.Theme.Layout]
		d.PreviewBase = "/wp-admin/gooike?page=preview"
		raw, _ := json.Marshal(st.Texts)
		var tm map[string]string
		_ = json.Unmarshal(raw, &tm)
		for _, f := range gooikeTextFields {
			f.Value = tm[f.Key]
			d.TextFields = append(d.TextFields, f)
		}
	case "categories":
		if d.Tab != "cookies" && d.Tab != "scan" {
			d.Tab = "categories"
		}
		d.Signals = gooikeSignals
		if k := c.Query("edit"); k != "" {
			d.EditCat = st.category(k)
		}
		if c.Query("new") == "category" {
			d.EditCat = &gooikeCategory{Enabled: true}
		}
		if id := c.Query("cookie"); id != "" {
			for i := range st.Cookies {
				if st.Cookies[i].ID == id {
					ck := st.Cookies[i]
					d.EditCookie = &ck
				}
			}
		}
		if d.EditCookie == nil {
			d.EditCookie = &gooikeCookieRow{Category: "necessary"}
		}
	case "scripts":
		d.Services = gooikeServices
		if id := c.Query("edit"); id != "" {
			for i := range st.Scripts {
				if st.Scripts[i].ID == id {
					sc := st.Scripts[i]
					d.EditScript = &sc
					d.ScriptCode = sc.code()
				}
			}
		}
		if c.Query("new") == "1" {
			d.EditScript = &gooikeScript{Category: "statistics", Placement: "head", Enabled: true}
		}
	case "logs":
		f := gooikeLogFilter{Action: c.Query("log_action"), Search: c.Query("s"), From: c.Query("from"), To: c.Query("to"), PerPage: 20}
		f.Page, _ = strconv.Atoi(c.Query("paged"))
		f.Page = max(f.Page, 1)
		d.Filter = f
		for _, k := range []string{"accept_all", "reject_all", "custom", "withdraw", "gpc"} {
			d.Actions = append(d.Actions, struct{ Key, Label string }{k, gooikeActions[k]})
		}
		q := url.Values{}
		for k, v := range map[string]string{"log_action": f.Action, "s": f.Search, "from": f.From, "to": f.To} {
			if v != "" {
				q.Set(k, v)
			}
		}
		d.QS = q.Encode()
		if id, err := strconv.ParseInt(c.Query("id"), 10, 64); err == nil {
			var e gooikeLogEntry
			err := s.store.Pool.QueryRow(c, `SELECT id, consent_id, action, categories, policy_version, regulation, ip_anon, ua_hash, url, gpc, created_at FROM gooike_consent_log WHERE id = $1`, id).
				Scan(&e.ID, &e.ConsentID, &e.Action, &e.Categories, &e.PolicyVersion, &e.Regulation, &e.IP, &e.UAHash, &e.URL, &e.GPC, &e.Created)
			if err == nil {
				e.ActionLabel = gooikeActions[e.Action]
				e.When = e.Created.In(s.loc()).Format("2006-01-02 15:04:05")
				d.Entry = &e
				d.Related, _, _ = s.gooikeLogs(c, gooikeLogFilter{Search: e.ConsentID, PerPage: 50, Page: 1})
			}
		}
		if d.Entry == nil {
			var err error
			d.Logs, d.Total, err = s.gooikeLogs(c, f)
			if err != nil {
				notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(err.Error()))})
			}
			d.PageNum = f.Page
			d.NumPages = max((d.Total+f.PerPage-1)/f.PerPage, 1)
		}
	case "settings":
		rows, err := s.store.Pool.Query(c, `SELECT id, title FROM posts WHERE type = 'page' AND status = 'publish' ORDER BY title, id`)
		if err == nil {
			for rows.Next() {
				var p struct {
					ID    int64
					Title string
				}
				if rows.Scan(&p.ID, &p.Title) == nil {
					d.PagesList = append(d.PagesList, p)
				}
			}
			rows.Close()
		}
	}
	s.adminPage(c, screen, d.Title+" ‹ Gooike", "toplevel_page_gooike gooike-admin", "admin/gooike", d, notices...)
}

// gooikeAdminPost handles the forms of every screen; do names the action.
func (s *Server) gooikeAdminPost(c *gin.Context) {
	page := c.PostForm("page")
	tab := c.PostForm("tab")
	back := "/wp-admin/gooike"
	if page != "" {
		back += "?page=" + url.QueryEscape(page)
		if tab != "" {
			back += "&tab=" + url.QueryEscape(tab)
		}
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	done := func(msg string) { c.Redirect(http.StatusFound, back+sep+"gooike-msg="+url.QueryEscape(msg)) }
	fail := func(msg string) { c.Redirect(http.StatusFound, back+sep+"gooike-error="+url.QueryEscape(msg)) }
	st := s.gooikeLoad()
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
	save := func(msg string) {
		if err := s.gooikeSave(c, st); err != nil {
			fail(err.Error())
			return
		}
		done(msg)
	}
	switch c.PostForm("do") {
	// Banner
	case "theme":
		t := gooikeThemeByKey(str("theme"))
		if t == nil {
			fail("Unknown theme.")
			return
		}
		t.apply(&st.Design)
		save(t.Name + " is now the banner theme.")
	case "design":
		d := &st.Design
		d.Position = str("position")
		d.Background, d.Text = gooikeColor(str("background"), d.Background), gooikeColor(str("text"), d.Text)
		d.Primary, d.PrimaryText = gooikeColor(str("primary"), d.Primary), gooikeColor(str("primary_text"), d.PrimaryText)
		d.Font = str("font")
		d.Radius = num("radius", d.Radius, 0, 999)
		d.FontSize = num("font_size", 15, 11, 22)
		d.Floating = on("floating")
		d.FloatPosition = str("float_position")
		d.CustomCSS = text("custom_css")
		save("Design saved.")
	case "texts":
		raw, _ := json.Marshal(st.Texts)
		var tm map[string]string
		_ = json.Unmarshal(raw, &tm)
		for _, f := range gooikeTextFields {
			if v, ok := c.GetPostForm("text_" + f.Key); ok {
				tm[f.Key] = strings.TrimSpace(strings.ReplaceAll(v, "\r\n", "\n"))
			}
		}
		raw, _ = json.Marshal(tm)
		_ = json.Unmarshal(raw, &st.Texts)
		save("Texts saved.")
	case "texts_reset":
		st.Texts = gooikeDefaultTexts()
		save("Texts reset to the defaults.")

	// Categories & cookies
	case "category_save":
		key := gooikeKey(str("key"))
		label := str("label")
		if label == "" {
			fail("A category needs a name.")
			return
		}
		cat := st.category(key)
		if cat == nil {
			if key == "" {
				key = gooikeKey(label)
			}
			if key == "" || st.hasCategory(key) {
				fail("Choose another key: that one is empty or already used.")
				return
			}
			st.Categories = append(st.Categories, gooikeCategory{Key: key})
			cat = &st.Categories[len(st.Categories)-1]
		}
		cat.Label, cat.Description = label, text("description")
		cat.Enabled = cat.Key == "necessary" || on("enabled")
		cat.Signals = gooikeValidSignals(c.PostFormArray("signals[]"))
		save("Category saved.")
	case "category_delete":
		key := str("key")
		out := st.Categories[:0]
		for _, cc := range st.Categories {
			if cc.Key == key && !cc.Builtin {
				continue
			}
			out = append(out, cc)
		}
		st.Categories = out
		save("Category deleted. Its scripts moved to Marketing and its cookies to Necessary; review them.")
	case "cookie_save":
		row := gooikeCookieRow{ID: str("id"), Name: str("name"), Provider: str("provider"), Category: str("category"), Purpose: text("purpose"), Duration: str("duration")}
		if row.Name == "" {
			fail("A cookie needs a name.")
			return
		}
		found := false
		for i := range st.Cookies {
			if row.ID != "" && st.Cookies[i].ID == row.ID {
				st.Cookies[i], found = row, true
			}
		}
		if !found {
			row.ID = gooikeNewID("c-")
			st.Cookies = append(st.Cookies, row)
		}
		back = "/wp-admin/gooike?page=categories&tab=cookies"
		sep = "&"
		save("Cookie saved.")
	case "cookie_delete":
		ids := c.PostFormArray("ids[]")
		if id := str("id"); id != "" {
			ids = append(ids, id)
		}
		out := make([]gooikeCookieRow, 0, len(st.Cookies))
		for _, ck := range st.Cookies {
			if !gooikeContains(ids, ck.ID) {
				out = append(out, ck)
			}
		}
		st.Cookies = out
		save("Cookies deleted.")
	case "scan":
		res := s.gooikeScan(c, &st, lines(text("paths")))
		s.gooikeRender(c, "categories", &res)
		return
	case "scan_add":
		names, provs, cats, purps, durs := c.PostFormArray("row_name[]"), c.PostFormArray("row_provider[]"), c.PostFormArray("row_category[]"), c.PostFormArray("row_purpose[]"), c.PostFormArray("row_duration[]")
		added := 0
		for _, v := range c.PostFormArray("add[]") {
			i, err := strconv.Atoi(v)
			if err != nil || i < 0 || i >= len(names) || i >= len(provs) || i >= len(cats) || i >= len(purps) || i >= len(durs) || gooikeDeclared(&st, names[i]) {
				continue
			}
			st.Cookies = append(st.Cookies, gooikeCookieRow{ID: gooikeNewID("c-"), Name: names[i], Provider: provs[i], Category: cats[i], Purpose: purps[i], Duration: durs[i]})
			added++
		}
		back, sep = "/wp-admin/gooike?page=categories&tab=cookies", "&"
		save(fmt.Sprintf("%d cookie(s) added to the declaration.", added))

	// Scripts
	case "script_save":
		sc := gooikeScript{ID: str("id"), Name: str("name"), Category: str("category"), Placement: str("placement"), Code: text("code"),
			Service: str("service"), ServiceID: strings.TrimSpace(str("service_id")), Enabled: on("enabled")}
		if sv := gooikeServiceByKey(sc.Service); sv != nil {
			if !sv.IDPattern.MatchString(sc.ServiceID) {
				fail(sv.Name + ": that is not a valid " + sv.IDLabel + " (for example " + sv.IDExample + ").")
				return
			}
			sc.Code = ""
		} else {
			sc.Service, sc.ServiceID = "", ""
			if sc.Code == "" {
				fail("Paste the script's code.")
				return
			}
		}
		if sc.Name == "" {
			sc.Name = "Script"
		}
		found := false
		for i := range st.Scripts {
			if sc.ID != "" && st.Scripts[i].ID == sc.ID {
				st.Scripts[i], found = sc, true
			}
		}
		if !found {
			sc.ID = gooikeNewID("s-")
			st.Scripts = append(st.Scripts, sc)
		}
		back, sep = "/wp-admin/gooike?page=scripts", "&"
		save("Script saved.")
	case "service_add":
		sv := gooikeServiceByKey(str("service"))
		if sv == nil {
			fail("Unknown service.")
			return
		}
		id := strings.TrimSpace(str("service_id"))
		if sv.Key == "clarity" {
			id = strings.ToLower(id)
		} else {
			id = strings.ToUpper(id)
		}
		if !sv.IDPattern.MatchString(id) {
			fail(sv.Name + ": that is not a valid " + sv.IDLabel + " (for example " + sv.IDExample + ").")
			return
		}
		cat := str("category")
		if !st.hasCategory(cat) {
			cat = sv.Category
		}
		st.Scripts = append(st.Scripts, gooikeScript{ID: gooikeNewID("s-"), Name: sv.Name, Category: cat, Placement: sv.Placement, Service: sv.Key, ServiceID: id, Enabled: true})
		for _, ck := range sv.Cookies { // the service's cookies join the declaration
			if !gooikeDeclared(&st, ck.Name) {
				ck.ID, ck.Category = gooikeNewID("c-"), cat
				st.Cookies = append(st.Cookies, ck)
			}
		}
		save(sv.Name + " added. It runs only after consent for " + strings.ToLower(st.category(cat).Label) + ".")
	case "script_toggle", "script_delete":
		id := str("id")
		out := st.Scripts[:0]
		for _, sc := range st.Scripts {
			if sc.ID == id {
				if c.PostForm("do") == "script_delete" {
					continue
				}
				sc.Enabled = !sc.Enabled
			}
			out = append(out, sc)
		}
		st.Scripts = out
		if c.PostForm("do") == "script_delete" {
			save("Script deleted.")
		} else {
			save("Script updated.")
		}
	case "embeds":
		st.BlockYouTube, st.BlockVimeo, st.BlockMaps = on("block_youtube"), on("block_vimeo"), on("block_maps")
		st.EmbedCategory = str("embed_category")
		for _, e := range gooikeEmbeds {
			if map[string]bool{"youtube": st.BlockYouTube, "vimeo": st.BlockVimeo, "maps": st.BlockMaps}[e.Key] {
				for _, ck := range e.Cookies {
					if !gooikeDeclared(&st, ck.Name) {
						ck.ID, ck.Category = gooikeNewID("c-"), st.EmbedCategory
						st.Cookies = append(st.Cookies, ck)
					}
				}
			}
		}
		save("Embed blocking saved.")

	// Logs
	case "logs_purge":
		n, err := s.gooikePurge(c)
		if err != nil {
			fail(err.Error())
			return
		}
		done(fmt.Sprintf("%d log entries older than the retention period deleted.", n))

	// Settings
	case "settings":
		st.ExpiryMonths = num("expiry_months", 12, 1, 24)
		st.PrivacyPage = int64(num("privacy_page", 0, 0, 1<<30))
		st.PrivacyURL = gooikeURL(str("privacy_url"))
		st.CookiePolicyURL = gooikeURL(str("cookie_policy_url"))
		st.NoticeURL = gooikeURL(str("notice_url"))
		st.Regulation = str("regulation")
		st.Geo = on("geo")
		st.GeoOther = str("geo_other")
		st.RespectGPC, st.RespectDNT = on("respect_gpc"), on("respect_dnt")
		st.ConsentMode = on("consent_mode")
		st.WaitForUpdate = num("wait_for_update", 500, 0, 10000)
		st.AdsRedaction, st.URLPassthrough = on("ads_data_redaction"), on("url_passthrough")
		st.LogConsent = on("log_consent")
		st.IPMode = str("ip_mode")
		st.RetentionMonths = num("retention_months", 12, 0, 120)
		st.ReloadOnChange = on("reload_on_withdraw")
		st.ExtraHosts = text("extra_hosts")
		save("Settings saved.")
	case "bump_version":
		st.PolicyVersion++
		save(fmt.Sprintf("The policy version is now %d: every visitor is asked again.", st.PolicyVersion))
	default:
		c.Redirect(http.StatusFound, back)
	}
}

// gooikeURL keeps site-relative and http(s) URLs.
func gooikeURL(v string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	if u, err := url.Parse(v); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		return v
	}
	return ""
}

// gooikeExportCSV downloads the consent log (with the list's filters).
func (s *Server) gooikeExportCSV(c *gin.Context) {
	f := gooikeLogFilter{Action: c.Query("log_action"), Search: c.Query("s"), From: c.Query("from"), To: c.Query("to")}
	logs, _, err := s.gooikeLogs(c, f)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"id", "date", "consent_id", "action", "categories", "policy_version", "regulation", "ip_anonymized", "user_agent_hash", "url", "gpc"})
	for _, e := range logs {
		_ = w.Write([]string{strconv.FormatInt(e.ID, 10), e.Created.In(s.loc()).Format(time.RFC3339), maintCSVSafe(e.ConsentID), e.Action, strings.Join(e.Categories, " "),
			strconv.Itoa(e.PolicyVersion), e.Regulation, maintCSVSafe(e.IP), e.UAHash, maintCSVSafe(e.URL), strconv.FormatBool(e.GPC)})
	}
	w.Flush()
	c.Header("Content-Disposition", `attachment; filename="gooike-consent-log-`+time.Now().Format("2006-01-02")+`.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// gooikePreview is the banner's live preview: a sample page with the banner open, from the saved
// design or from the values of the unsaved Design form (query parameters).
func (s *Server) gooikePreview(c *gin.Context) {
	st := s.gooikeLoad()
	q := c.Query
	if t := gooikeThemeByKey(q("theme")); t != nil && t.Key != st.Design.Theme {
		t.apply(&st.Design)
	}
	d := &st.Design
	if v := q("position"); v != "" {
		d.Position = v
	}
	d.Background, d.Text = gooikeColor(q("background"), d.Background), gooikeColor(q("text"), d.Text)
	d.Primary, d.PrimaryText = gooikeColor(q("primary"), d.Primary), gooikeColor(q("primary_text"), d.PrimaryText)
	if v := q("font"); v != "" {
		d.Font = v
	}
	if n, err := strconv.Atoi(q("radius")); err == nil {
		d.Radius = n
	}
	if n, err := strconv.Atoi(q("font_size")); err == nil {
		d.FontSize = n
	}
	if _, ok := c.GetQuery("custom_css"); ok {
		d.CustomCSS = q("custom_css")
	}
	if q("form") == "1" { // the Design form sends unchecked boxes as absent
		d.Floating, d.FloatPosition = q("floating") != "", q("float_position")
	}
	st = gooikeSanitize(st)
	open := q("open")
	if open != "prefs" && open != "none" {
		open = "banner"
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Gooike banner preview</title>`)
	b.WriteString(`<meta name="robots" content="noindex">`)
	b.WriteString(`<link rel="stylesheet" href="` + gooikeAssets + `/gooike.css?ver=` + gooikeVersion + `">`)
	b.WriteString(`<style>` + gooikePreviewPageCSS + gooikeCSSVars(st.Design) + gooikeCustomCSS(st.Design) + `</style></head><body class="gk-preview-page">`)
	b.WriteString(`<header class="pv-head"><strong>` + template.HTMLEscapeString(s.settings.String("blogname")) + `</strong><nav><span></span><span></span><span></span></nav></header>`)
	b.WriteString(`<main class="pv-main"><div class="pv-hero"></div><h1>Welcome to our site</h1><p class="pv-line"></p><p class="pv-line short"></p><div class="pv-cards"><div></div><div></div><div></div></div><p class="pv-line"></p><p class="pv-line"></p><p class="pv-line short"></p></main>`)
	b.WriteString(s.gooikeMarkup(c, &st, true))
	cfg := strings.Replace(gooikeConfigScript(&st, true), `"preview":true`, `"preview":true,"open":"`+open+`"`, 1)
	b.WriteString(cfg)
	b.WriteString(`<script src="` + gooikeAssets + `/gooike.js?ver=` + gooikeVersion + `" defer></script></body></html>`)
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(b.String()))
}

const gooikePreviewPageCSS = `html,body{margin:0}body.gk-preview-page{font:16px/1.6 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:#f3f4f6;color:#1f2937;min-height:100vh}
.pv-head{display:flex;justify-content:space-between;align-items:center;padding:18px 32px;background:#fff;border-bottom:1px solid #e5e7eb}
.pv-head nav{display:flex;gap:14px}.pv-head nav span{display:block;width:56px;height:10px;border-radius:5px;background:#d1d5db}
.pv-main{max-width:900px;margin:0 auto;padding:32px}.pv-hero{height:180px;border-radius:12px;background:linear-gradient(135deg,#c7d2fe,#fbcfe8 60%,#fde68a)}
.pv-main h1{font-size:30px;margin:24px 0 12px}.pv-line{height:12px;border-radius:6px;background:#d1d5db;margin:12px 0}.pv-line.short{width:60%}
.pv-cards{display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin:24px 0}.pv-cards div{height:110px;border-radius:10px;background:#e5e7eb}`
