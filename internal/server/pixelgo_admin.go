package server

import (
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

// Pursae Pixel's admin screens: /wp-admin/pixelgo?page=… (Dashboard, Pixels & APIs, Events, Custom
// Events, Product Feed, Delivery Log, Settings). Administrators only (manage_options).
//
// POST `do` values: save_pixels, test (platform), save_events, save_custom, custom_toggle (id),
// custom_delete (id), save_feed, regen_token, clear_log, save_settings, import, reset.
// GET ?action=export downloads the settings without tokens.

// pixelCanonEvents are the Google Ads conversion label slots.
var pixelCanonEvents = []string{"Purchase", "AddToCart", "InitiateCheckout", "Lead", "ViewContent", "Search", "CompleteRegistration"}

var pixelTriggers = []struct{ Key, Label string }{
	{"page_visit", "Page visit"}, {"click", "Click on element"}, {"scroll", "Scroll depth"},
	{"time", "Time on page"}, {"form_submit", "Form submit"},
}

type pixelLogRow struct {
	ID                                     int64
	Platform, Event, EventID, Source, Stat string
	Code                                   int
	Message, When                          string
}

type pixelRoleRow struct {
	Slug, Name string
	Excluded   bool
}

type pixelPlatformRow struct {
	Def       *pixelPlatformDef
	Cfg       pixelPlatformCfg
	IDsText   string
	HasToken  bool
	Labels    map[string]string
	Active    bool
	ConsentOn string
}

type pixelAdminData struct {
	Page, Title string
	S           pixelSettings
	Nonce       string
	Home        string

	Platforms []pixelPlatformRow
	Canon     []string
	General   []struct {
		Key, Label, Help string
		On               bool
	}
	Woo []struct {
		Key, Label, Help string
		On               bool
	}
	WooActive  bool
	CookieOn   bool
	Triggers   []struct{ Key, Label string }
	Edit       *pixelCustomEvent
	EditParams string
	TrigLabel  map[string]string
	FeedURL    string
	FeedCount  int
	Roles      []pixelRoleRow
	Checklist  []struct {
		Done  bool
		Label template.HTML
	}

	Delivered, Failed, ActiveCount, ServerCount int
	Logs                                        []pixelLogRow
	Total, PageNum, NumPages                    int
	FPlatform, FStatus, FSearch                 string
	QS                                          string
	Prev, Next                                  int
}

func (d *pixelAdminData) Selected(list []string, v string) bool { return pixelHas(list, v) }

// pixelAdmin serves every Pursae Pixel screen.
func (s *Server) pixelAdmin(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if c.Request.Method == http.MethodGet && c.Query("action") == "export" {
		if !s.checkNonce(c, pixelNonce) {
			s.denyNonce(c)
			return
		}
		st := s.pixelLoad()
		for k, p := range st.Platforms {
			p.Token = ""
			st.Platforms[k] = p
		}
		st.FeedToken = ""
		c.Header("Content-Disposition", `attachment; filename="pursae-pixel-settings.json"`)
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, st)
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, pixelNonce) {
			s.denyNonce(c)
			return
		}
		s.pixelAdminPost(c)
		return
	}
	s.pixelRender(c, c.Query("page"))
}

func (s *Server) pixelRender(c *gin.Context, page string) {
	st := s.pixelLoad()
	d := &pixelAdminData{Page: page, S: st, Nonce: s.nonce(c, pixelNonce), Home: s.links(c).HomeURL("/"), Canon: pixelCanonEvents,
		WooActive: s.gooOn(), CookieOn: s.gooikeOn(), Triggers: pixelTriggers, TrigLabel: map[string]string{}}
	for _, t := range pixelTriggers {
		d.TrigLabel[t.Key] = t.Label
	}
	for _, p := range pixelMenuPages {
		if p.Page == page {
			d.Title = strings.ReplaceAll(p.Title, "&amp;", "&")
		}
	}
	if d.Title == "" {
		page, d.Page, d.Title = "", "", "Dashboard"
	}
	screen := "pixelgo"
	if page != "" {
		screen += "?page=" + page
	}
	var notices []notice
	if m := c.Query("pixelgo-msg"); m != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(m)), Dismissible: true})
	}
	if e := c.Query("pixelgo-error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(e))})
	}
	for _, def := range pixelPlatforms {
		cfg := st.Platforms[def.Key]
		row := pixelPlatformRow{Def: def, Cfg: cfg, IDsText: strings.Join(cfg.IDs, "\n"), HasToken: cfg.Token != "", Labels: cfg.Labels,
			Active: cfg.Enabled && len(cfg.IDs) > 0, ConsentOn: def.Category}
		if row.Labels == nil {
			row.Labels = map[string]string{}
		}
		d.Platforms = append(d.Platforms, row)
	}
	d.ActiveCount, d.ServerCount = len(st.active()), len(st.serverReady())
	switch page {
	case "":
		_ = s.store.Pool.QueryRow(c, `SELECT count(*) FILTER (WHERE status = 'ok'), count(*) FILTER (WHERE status = 'error') FROM pixelgo_log WHERE created_at > now() - interval '7 days'`).Scan(&d.Delivered, &d.Failed)
		d.Logs, _, _ = s.pixelLogRows(c, "", "", "", 10, 1)
		if !d.CookieOn && st.RequireConsent {
			notices = append(notices, notice{Type: "warning", Message: template.HTML(`<strong>Pursae Pixel:</strong> Pursae Cookie is not active, so the tags load without consent management. Activate it on the <a href="/wp-admin/plugins">Plugins</a> screen, or switch off &ldquo;Require consent&rdquo; under Settings if you handle consent elsewhere.`)})
		}
		if d.WooActive && (!st.Woo["purchase"] || d.ActiveCount == 0) {
			notices = append(notices, notice{Type: "warning", Message: template.HTML(`<strong>Pursae Pixel:</strong> the store is active but Purchase events are not being sent. Enable a platform and the Purchase event under <a href="/wp-admin/pixelgo?page=events">Events</a>.`)})
		}
		chk := func(done bool, label string) {
			d.Checklist = append(d.Checklist, struct {
				Done  bool
				Label template.HTML
			}{done, template.HTML(label)})
		}
		chk(d.ActiveCount > 0, `Add a pixel or measurement ID under <a href="/wp-admin/pixelgo?page=pixels">Pixels &amp; APIs</a>`)
		chk(d.ServerCount > 0, `Add an API access token to deliver events server-side (better matching, ad-blocker proof)`)
		chk(d.CookieOn || !st.RequireConsent, `Consent: activate Pursae Cookie so tags wait for the visitor's choice`)
		chk(len(st.Custom) > 0, `Optional: create a <a href="/wp-admin/pixelgo?page=custom">custom event</a> for the buttons and forms that matter`)
		chk(!d.WooActive || st.FeedEnabled, `Optional: publish the <a href="/wp-admin/pixelgo?page=feed">product catalog feed</a>`)
	case "events":
		for _, e := range pixelGeneralEvents {
			d.General = append(d.General, struct {
				Key, Label, Help string
				On               bool
			}{e.Key, e.Label, e.Help, st.General[e.Key]})
		}
		for _, e := range pixelWooEvents {
			d.Woo = append(d.Woo, struct {
				Key, Label, Help string
				On               bool
			}{e.Key, e.Label, e.Help, st.Woo[e.Key]})
		}
	case "custom":
		if id := c.Query("edit"); id != "" {
			for i := range st.Custom {
				if st.Custom[i].ID == id {
					ce := st.Custom[i]
					d.Edit = &ce
				}
			}
		}
		if c.Query("new") == "1" {
			d.Edit = &pixelCustomEvent{Enabled: true, Trigger: "click"}
		}
		if d.Edit != nil {
			d.EditParams = d.Edit.Params
		}
	case "feed":
		if st.FeedEnabled && st.FeedToken != "" {
			d.FeedURL = d.Home + "pixelgo-feed/" + st.FeedToken
		}
		_ = s.store.Pool.QueryRow(c, `SELECT count(*) FROM goo_products gp JOIN posts p ON p.id = gp.post_id WHERE p.status = 'publish' AND gp.type <> 'variation'`).Scan(&d.FeedCount)
	case "logs":
		d.FPlatform, d.FStatus, d.FSearch = c.Query("platform"), c.Query("status"), strings.TrimSpace(c.Query("s"))
		d.PageNum, _ = strconv.Atoi(c.Query("paged"))
		d.PageNum = max(d.PageNum, 1)
		var err error
		d.Logs, d.Total, err = s.pixelLogRows(c, d.FPlatform, d.FStatus, d.FSearch, 20, d.PageNum)
		if err != nil {
			notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(err.Error()))})
		}
		d.NumPages = max((d.Total+19)/20, 1)
		d.Prev, d.Next = d.PageNum-1, d.PageNum+1
		q := url.Values{}
		for k, v := range map[string]string{"platform": d.FPlatform, "status": d.FStatus, "s": d.FSearch} {
			if v != "" {
				q.Set(k, v)
			}
		}
		d.QS = q.Encode()
	case "settings":
		if rows, err := s.store.Roles(c); err == nil {
			for _, r := range rows {
				d.Roles = append(d.Roles, pixelRoleRow{Slug: r.Slug, Name: r.Name, Excluded: pixelHas(st.ExcludeRoles, r.Slug)})
			}
		}
	}
	s.adminPage(c, screen, d.Title+" ‹ Pursae Pixel", "toplevel_page_pixelgo pixelgo-admin", "admin/pixelgo", d, notices...)
}

// pixelLogRows reads a page of the delivery log (perPage 0: no paging).
func (s *Server) pixelLogRows(c *gin.Context, platform, status, search string, perPage, page int) ([]pixelLogRow, int, error) {
	where, args := " WHERE true", []any{}
	if pixelPlatformByKey(platform) != nil {
		args = append(args, platform)
		where += fmt.Sprintf(" AND platform = $%d", len(args))
	}
	if status == "ok" || status == "error" || status == "skipped" {
		args = append(args, status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if search != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)+"%")
		where += fmt.Sprintf(" AND (event ILIKE $%d OR event_id ILIKE $%d)", len(args), len(args))
	}
	var total int
	if err := s.store.Pool.QueryRow(c, `SELECT count(*) FROM pixelgo_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, platform, event, event_id, source, status, http_code, message, created_at FROM pixelgo_log` + where + ` ORDER BY created_at DESC, id DESC`
	if perPage > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", perPage, max(page-1, 0)*perPage)
	}
	rows, err := s.store.Pool.Query(c, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []pixelLogRow
	for rows.Next() {
		var r pixelLogRow
		var at time.Time
		if err := rows.Scan(&r.ID, &r.Platform, &r.Event, &r.EventID, &r.Source, &r.Stat, &r.Code, &r.Message, &at); err != nil {
			return nil, 0, err
		}
		r.When = at.In(s.loc()).Format("2006-01-02 15:04:05")
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// pixelAdminPost handles the forms of every screen; do names the action.
func (s *Server) pixelAdminPost(c *gin.Context) {
	page := c.PostForm("page")
	back := "/wp-admin/pixelgo"
	if pixelMenuHas(page) && page != "" {
		back += "?page=" + url.QueryEscape(page)
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	done := func(msg string) { c.Redirect(http.StatusFound, back+sep+"pixelgo-msg="+url.QueryEscape(msg)) }
	fail := func(msg string) { c.Redirect(http.StatusFound, back+sep+"pixelgo-error="+url.QueryEscape(msg)) }
	st := s.pixelLoad()
	str := func(k string) string { return strings.TrimSpace(c.PostForm(k)) }
	on := func(k string) bool { v := c.PostForm(k); return v != "" && v != "0" }
	save := func(msg string) {
		if err := s.pixelSave(c, st); err != nil {
			fail("The settings could not be saved.")
			return
		}
		done(msg)
	}
	switch c.PostForm("do") {
	case "save_pixels":
		for _, def := range pixelPlatforms {
			k := def.Key
			cfg := st.Platforms[k]
			cfg.Enabled = on("enabled_" + k)
			cfg.IDs = strings.FieldsFunc(c.PostForm("ids_"+k), func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ' ' })
			if def.TokenLabel != "" {
				if t := str("token_" + k); t != "" {
					cfg.Token = t
				}
				if on("rm_token_" + k) {
					cfg.Token = ""
				}
				cfg.ServerSide = on("server_" + k)
				if def.TestLabel != "" {
					cfg.TestCode = str("test_" + k)
				}
			}
			if def.AccountLabel != "" {
				cfg.AccountID = str("account_" + k)
			}
			if k == "gads" {
				cfg.Labels = map[string]string{}
				for _, ev := range pixelCanonEvents {
					cfg.Labels[ev] = str("label_" + ev)
				}
			}
			st.Platforms[k] = cfg
		}
		save("Pixels and APIs saved.")
	case "test":
		pk := c.PostForm("platform")
		cfg, ok := st.Platforms[pk]
		if pixelPlatformByKey(pk) == nil || !ok || !cfg.ServerSide || cfg.Token == "" || len(cfg.IDs) == 0 {
			fail("Save the IDs and the API token with server-side delivery switched on first.")
			return
		}
		status, code, msg := s.pixelSendTest(c, pk)
		text := fmt.Sprintf("Test event to %s: %s", pixelPlatformByKey(pk).Name, status)
		if code > 0 {
			text += fmt.Sprintf(" (HTTP %d)", code)
		}
		if msg != "" {
			text += " — " + msg
		}
		if status == "ok" {
			done(text)
		} else {
			fail(text)
		}
	case "save_events":
		for _, e := range pixelGeneralEvents {
			st.General[e.Key] = on("general_" + e.Key)
		}
		for _, e := range pixelWooEvents {
			st.Woo[e.Key] = on("woo_" + e.Key)
		}
		st.ScrollDepths, st.TimeSeconds, st.DownloadExts = str("scroll_depths"), str("time_seconds"), str("download_exts")
		st.ContentID, st.IDPrefix, st.IDSuffix, st.ValueMode = str("content_id"), str("id_prefix"), str("id_suffix"), str("value_mode")
		st.PurchaseOnce, st.Advanced, st.Attribution = on("purchase_once"), on("advanced_matching"), on("attribution")
		st.CookieDays, _ = strconv.Atoi(str("cookie_days"))
		save("Events saved.")
	case "save_custom":
		ce := pixelCustomEvent{ID: str("id"), Name: str("name"), Enabled: on("enabled"), Trigger: str("trigger"), Selector: str("selector"),
			URL: str("url"), Value: str("value"), Currency: str("currency"), Params: c.PostForm("params"), Once: on("once"),
			Platforms: c.PostFormArray("platforms")}
		ce.Number, _ = strconv.Atoi(str("number"))
		if !pixelNameRe.MatchString(ce.Name) {
			fail("The event name must start with a letter and use letters, digits, spaces, dashes or underscores (40 characters at most).")
			return
		}
		if (ce.Trigger == "click" || ce.Trigger == "form_submit") && pixelSelector(ce.Selector) == "" {
			fail("This trigger needs a valid CSS selector.")
			return
		}
		found := false
		for i := range st.Custom {
			if ce.ID != "" && st.Custom[i].ID == ce.ID {
				st.Custom[i], found = ce, true
			}
		}
		if !found {
			ce.ID = ""
			st.Custom = append(st.Custom, ce)
		}
		back = "/wp-admin/pixelgo?page=custom"
		sep = "&"
		save("Custom event saved.")
	case "custom_toggle", "custom_delete":
		id := str("id")
		var out []pixelCustomEvent
		for _, ce := range st.Custom {
			if ce.ID == id {
				if c.PostForm("do") == "custom_delete" {
					continue
				}
				ce.Enabled = !ce.Enabled
			}
			out = append(out, ce)
		}
		st.Custom = out
		save("Custom event updated.")
	case "save_feed":
		st.FeedEnabled, st.FeedBrand, st.FeedCondition, st.FeedGoogleCat = on("feed_enabled"), str("feed_brand"), str("feed_condition"), str("feed_google_category")
		save("Product feed settings saved.")
	case "regen_token":
		st.FeedToken = pixelRandom(24)
		save("A new feed URL was generated. Update it in Meta Commerce Manager and Google Merchant Center.")
	case "clear_log":
		if _, err := s.store.Pool.Exec(c, `DELETE FROM pixelgo_log`); err != nil {
			fail("The log could not be cleared.")
			return
		}
		done("The delivery log was cleared.")
	case "save_settings":
		st.RequireConsent, st.LogServer, st.Debug = on("require_consent"), on("log_server"), on("debug")
		st.ExcludeRoles = c.PostFormArray("exclude_roles")
		st.LogRetention, _ = strconv.Atoi(str("log_retention_days"))
		save("Settings saved.")
	case "import":
		var in pixelSettings
		if err := json.Unmarshal([]byte(c.PostForm("json")), &in); err != nil {
			fail("The JSON could not be read.")
			return
		}
		for k, p := range in.Platforms { // tokens never travel in an export: keep the stored ones
			p.Token = st.Platforms[k].Token
			in.Platforms[k] = p
		}
		in.FeedToken = st.FeedToken
		st = pixelMerge(pixelDefaults(), in)
		save("Settings imported. Tokens were kept.")
	case "reset":
		if str("confirm") != "reset" {
			fail("Type reset to confirm.")
			return
		}
		def := pixelDefaults()
		if err := s.pixelSave(c, def); err != nil {
			fail("The settings could not be reset.")
			return
		}
		done("Settings were reset to the defaults.")
	default:
		fail("Unknown action.")
	}
}

func pixelMenuHas(page string) bool {
	for _, p := range pixelMenuPages {
		if p.Page == page {
			return true
		}
	}
	return false
}
