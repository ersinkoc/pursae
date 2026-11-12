package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Golylang's wp-admin: the Languages menu (Languages, Translations, Settings), the admin bar language
// filter, the language columns of the posts and terms lists, the Languages meta box, new translations
// and the per-language menu locations.

// golylangPluginRow is the Plugins screen row.
func golylangPluginRow(active bool) pluginRow {
	r := pluginRow{Slug: golylangSlug, Name: "Pursae Multi Language", Version: builtinPluginVersion, Like: "Polylang", Native: true, Active: active,
		Description: "Making Pursae multilingual: languages, translated posts, pages, categories, tags and menus, a language switcher, " +
			"translated strings, per-language URLs (directory, subdomain or domain), hreflang tags and synchronization. A clone of Polylang Pro. While it is off, none of it is part of the site.",
		Author: "Pursae"}
	if active {
		r.SettingsURL = "/wp-admin/admin?page=mlang_settings"
	}
	return r
}

// golylangSwitch activates or deactivates the plugin. Languages, translations and strings stay in the
// database and come back with the plugin.
func (s *Server) golylangSwitch(ctx context.Context, on bool) error {
	if err := s.settings.Set(ctx, map[string]any{golylangSetting: on}); err != nil {
		return err
	}
	s.pllFlush()
	s.purgeAll(ctx)
	return nil
}

var pllPages = map[string]string{"mlang": "Languages", "mlang_strings": "Strings translations", "mlang_settings": "Settings", "mlang_visual": "Translate visually",
	"mlang_translate": "Translate content"}

// golylangAdmin serves admin?page=mlang, mlang_strings and mlang_settings; false for other pages.
func (s *Server) golylangAdmin(c *gin.Context, page string) bool {
	if _, ok := pllPages[page]; !ok {
		return false
	}
	u := currentUser(c)
	allowed := u.Can("manage_options", nil)
	if page == "mlang_strings" || page == "mlang_visual" {
		allowed = pllCanTranslateAny(u) // translators: golylang_caps.go
	}
	if page == "mlang_translate" {
		allowed = u.Can("edit_posts", nil) || u.Can("edit_pages", nil) // each action checks the post itself
	}
	if !s.golylangOn() || !allowed {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return true
	}
	switch page {
	case "mlang":
		s.pllLanguagesScreen(c)
	case "mlang_strings":
		s.pllStringsScreen(c)
	case "mlang_settings":
		s.pllSettingsScreen(c)
	case "mlang_visual":
		s.pllVisualScreen(c) // golylang_visual.go
	case "mlang_translate":
		s.pllTranslateScreen(c) // golylang_translate.go
	}
	return true
}

// golylangAdminView adds the Languages menu and the admin bar language filter to every admin screen.
func (s *Server) golylangAdminView(c *gin.Context, v *adminView) {
	if !s.golylangOn() || v.User == nil {
		return
	}
	u := v.User
	if pllCanTranslateAny(u) {
		admin := u.Can("manage_options", nil)
		first := "admin?page=mlang"
		pages := []string{"mlang", "mlang_strings", "mlang_settings"}
		if !admin { // a translator only sees the Translations screen
			first, pages = "admin?page=mlang_strings", []string{"mlang_strings"}
		}
		top := &menuTop{Slug: first, Title: "Languages", ID: "toplevel_page_mlang", Icon: "dashicons-translation"}
		for i, p := range pages {
			title := pllPages[p]
			if p == "mlang_strings" {
				title = "Translations"
			}
			ms := &menuSub{Slug: "admin?page=" + p, Title: template.HTML(title), First: i == 0, Current: v.Screen == "admin?page="+p}
			top.Current = top.Current || ms.Current
			top.Sub = append(top.Sub, ms)
		}
		if n := len(v.Menu); n > 0 {
			v.Menu[n-1].Last = false
		}
		top.Last = true
		v.Menu = append(v.Menu, top)
	}
	langs := s.pllLanguages(c)
	if len(langs) > 0 && u.Can("edit_posts", nil) {
		cur := s.pllLang(c, s.pllUserFilter(u))
		var b strings.Builder
		label := "Show all languages"
		flag := `<span class="ab-icon dashicons dashicons-translation" aria-hidden="true"></span>`
		if cur != nil {
			label, flag = cur.Name, string(cur.FlagHTML())+" "
		}
		fmt.Fprintf(&b, `<li role="group" id="wp-admin-bar-languages" class="menupop"><a class="ab-item" role="menuitem" aria-expanded="false" href="%s" title="Filters content by language">%s<span class="ab-label">%s</span></a><div class="ab-sub-wrapper"><ul role="menu" id="wp-admin-bar-languages-default" class="ab-submenu">`,
			html.EscapeString(pllWithLang(c, "all")), flag, html.EscapeString(label))
		fmt.Fprintf(&b, `<li role="group" id="wp-admin-bar-all"><a class="ab-item" role="menuitem" href="%s"><span class="ab-icon dashicons dashicons-translation" aria-hidden="true"></span>Show all languages</a></li>`, html.EscapeString(pllWithLang(c, "all")))
		for _, l := range langs {
			fmt.Fprintf(&b, `<li role="group" id="wp-admin-bar-%s"><a class="ab-item" role="menuitem" lang="%s" href="%s">%s %s</a></li>`,
				html.EscapeString(l.Slug), html.EscapeString(l.W3C()), html.EscapeString(pllWithLang(c, l.Slug)), l.FlagHTML(), html.EscapeString(l.Name))
		}
		b.WriteString(`</ul></div></li>`)
		v.CacheBar += template.HTML(b.String())
	}
	v.ExtraStyles += template.HTML(`<style id="golylang-admin-css">` + pllAdminCSS + `</style>`)
}

const pllAdminCSS = `.pll-flag{font-size:16px;line-height:1;vertical-align:middle;font-family:"Apple Color Emoji","Segoe UI Emoji","Noto Color Emoji",sans-serif}
#wpadminbar #wp-admin-bar-languages .pll-flag{font-size:14px}#wpadminbar #wp-admin-bar-languages>.ab-item .ab-icon:before{content:"\f326";top:2px}
.wp-list-table th[class*=column-language_],.wp-list-table td[class*=column-language_]{width:2.2em;text-align:center;padding-left:2px;padding-right:2px}
.wp-list-table td[class*=column-language_] .dashicons{color:#2271b1}.wp-list-table td[class*=column-language_] .pll_icon_tick{color:#00a32a}
#pll-translations table,#ml_box table{width:100%}#ml_box td,#pll-translations td,#pll-translations th{padding:3px 2px;vertical-align:middle;text-align:left}#pll-translations .pll-column-icon{width:20px}
#ml_box select,.pll-box-inner select{width:100%;max-width:100%}.pll-translation-flag{width:24px}.pll-language-list .default-lang .dashicons{color:#dba617}
.languages_page_mlang_settings .pll-module td{vertical-align:top}.pll-configure{background:#f6f7f7}.pll-configure td{padding:12px 20px}
.pll-strings-table textarea,.pll-strings-table input[type=text]{width:100%}.pll-strings-table .translation label{display:block;font-size:12px;color:#50575e}`

// pllWithLang is the current admin URL with the language filter set (lang=all clears it).
func pllWithLang(c *gin.Context, lang string) string {
	u := *c.Request.URL
	q := u.Query()
	q.Set("lang", lang)
	q.Del("_wpnonce")
	u.RawQuery = q.Encode()
	return u.Path + "?" + u.RawQuery
}

// Languages > Languages --------------------------------------------------------------------

type pllLangRow struct {
	*pllLanguage
	Count                int
	Missing, Outdated    int // published default-language items without a translation / whose translation is older
	EditURL, DeleteURL   string
	DefaultURL           string
	ToggleURL, ToggleLbl string
}

type pllLanguagesData struct {
	Tabs         []pllTab
	Rows         []pllLangRow
	Edit         *pllLanguage
	Form         pllLanguage
	Predefined   []pllPredefinedLang
	Nonce        string
	Untranslated bool
	AssignURL    string
}

type pllTab struct {
	Slug, Label string
	Current     bool
}

func pllTabs(cur string) []pllTab {
	return []pllTab{{"mlang", "Languages", cur == "mlang"}, {"mlang_strings", "Translations", cur == "mlang_strings"}, {"mlang_settings", "Settings", cur == "mlang_settings"}}
}

var (
	pllLocaleRe = regexp.MustCompile(`^[a-z]{2,3}(?:_[A-Z]{2})?(?:_[a-z0-9]+)?$`)
	pllSlugRe   = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// pllLangNotices are the ?pll_msg= notices of the Languages screen.
var pllLangNotices = map[string]string{"added": "Language added.", "updated": "Language updated.", "deleted": "Language deleted.",
	"default": "Default language changed.", "content": "Content without language now uses the default language.",
	"activated": "Language activated.", "deactivated": "Language deactivated."}

// pllLangErrorNotice is the ?pll_msg=save_error notice: a database write of the action failed.
const pllLangErrorNotice = "The change could not be saved. Please try again."

func (s *Server) pllLanguagesScreen(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost || c.Query("pll_action") != "" && c.Query("pll_action") != "edit" {
		s.pllLanguagesAction(c)
		return
	}
	var notices []notice
	if m := pllLangNotices[c.Query("pll_msg")]; m != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML(m), Dismissible: true})
	} else if c.Query("pll_msg") == "save_error" {
		notices = append(notices, notice{Type: "error", Message: pllLangErrorNotice, Dismissible: true})
	}
	s.pllRenderLanguages(c, nil, notices...)
	_ = ctx
}

// pllTranslationStatus counts, for a non-default language, the published posts of the default language that
// have no translation into it yet, and those whose translation was last edited before the source changed.
func (s *Server) pllTranslationStatus(ctx context.Context, l *pllLanguage) (missing, outdated int) {
	def := defaultSlug(s.pllDefault(ctx))
	if def == "" || def == l.Slug {
		return
	}
	const src = `SELECT p.id, p.modified_at, coalesce(sl.grp, 0) AS grp FROM posts p LEFT JOIN pll_post_langs sl ON sl.post_id = p.id
		WHERE p.type = ANY($1) AND p.status = 'publish' AND coalesce(sl.lang, $2) = $2`
	_ = s.store.Pool.QueryRow(ctx, `WITH src AS (`+src+`) SELECT count(*) FROM src s WHERE NOT EXISTS
		(SELECT 1 FROM pll_post_langs t WHERE t.grp = s.grp AND s.grp <> 0 AND t.lang = $3)`, s.pllPostTypes(), def, l.Slug).Scan(&missing)
	_ = s.store.Pool.QueryRow(ctx, `WITH src AS (`+src+`) SELECT count(*) FROM src s JOIN pll_post_langs t ON t.grp = s.grp AND t.lang = $3
		JOIN posts tp ON tp.id = t.post_id WHERE s.grp <> 0 AND tp.modified_at < s.modified_at`, s.pllPostTypes(), def, l.Slug).Scan(&outdated)
	return
}

func (s *Server) pllRenderLanguages(c *gin.Context, form *pllLanguage, notices ...notice) {
	ctx := c.Request.Context()
	d := pllLanguagesData{Tabs: pllTabs("mlang"), Predefined: pllPredefined, Nonce: s.nonce(c, "add-lang")}
	counts := map[string]int{}
	if rows, err := s.store.Pool.Query(ctx, `SELECT coalesce(l.lang, $1), count(*) FROM posts p LEFT JOIN pll_post_langs l ON l.post_id = p.id
		WHERE p.type = ANY($2) AND p.status = 'publish' GROUP BY 1`, defaultSlug(s.pllDefault(ctx)), s.pllPostTypes()); err == nil {
		for rows.Next() {
			var lang string
			var n int
			if rows.Scan(&lang, &n) == nil {
				counts[lang] = n
			}
		}
		rows.Close()
	}
	for _, l := range s.pllLanguages(ctx) {
		id := itoa64(l.ID)
		r := pllLangRow{pllLanguage: l, Count: counts[l.Slug],
			EditURL:   "/wp-admin/admin?page=mlang&pll_action=edit&lang=" + id,
			DeleteURL: "/wp-admin/admin?page=mlang&pll_action=delete&lang=" + id + "&_wpnonce=" + s.nonce(c, "delete-lang"),
		}
		if !l.IsDefault {
			r.Missing, r.Outdated = s.pllTranslationStatus(ctx, l)
			r.DefaultURL = "/wp-admin/admin?page=mlang&pll_action=default-lang&lang=" + id + "&_wpnonce=" + s.nonce(c, "default-lang")
			r.ToggleURL = "/wp-admin/admin?page=mlang&pll_action=toggle&lang=" + id + "&_wpnonce=" + s.nonce(c, "toggle-lang")
			r.ToggleLbl = map[bool]string{true: "Deactivate", false: "Activate"}[l.Active]
		}
		d.Rows = append(d.Rows, r)
		if c.Query("pll_action") == "edit" && c.Query("lang") == id {
			d.Edit = l
		}
	}
	switch {
	case form != nil:
		d.Form = *form
	case d.Edit != nil:
		d.Form = *d.Edit
	default:
		d.Form = pllLanguage{Order: len(d.Rows)}
	}
	if len(d.Rows) > 0 && s.pllUntranslatedContent(ctx) {
		d.Untranslated = true
		d.AssignURL = "/wp-admin/admin?page=mlang&pll_action=content-default-lang&_wpnonce=" + s.nonce(c, "content-default-lang")
	}
	s.adminPage(c, "admin?page=mlang", "Languages", "languages_page_mlang toplevel_page_mlang", "admin/golylang-languages", d, notices...)
}

func defaultSlug(l *pllLanguage) string {
	if l == nil {
		return ""
	}
	return l.Slug
}

func (s *Server) pllLanguagesAction(c *gin.Context) {
	ctx := c.Request.Context()
	action := c.PostForm("pll_action")
	if action == "" {
		action = c.Query("pll_action")
	}
	back := func(msg string) { c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang&pll_msg="+msg) }
	lang := func() *pllLanguage {
		id, _ := strconv.ParseInt(c.Query("lang"), 10, 64)
		for _, l := range s.pllLanguages(ctx) {
			if l.ID == id {
				return l
			}
		}
		return nil
	}
	switch action {
	case "add", "update":
		if !s.checkNonce(c, "add-lang") {
			s.denyNonce(c)
			return
		}
		f := &pllLanguage{Name: strings.TrimSpace(c.PostForm("name")), Locale: strings.TrimSpace(c.PostForm("locale")),
			Slug: strings.TrimSpace(c.PostForm("slug")), RTL: c.PostForm("rtl") == "1", Flag: c.PostForm("flag"), Active: true}
		f.Order, _ = strconv.Atoi(c.PostForm("term_group"))
		f.ID, _ = strconv.ParseInt(c.PostForm("lang_id"), 10, 64)
		if err := s.pllSaveLanguage(ctx, f, action == "add"); err != nil {
			s.pllRenderLanguages(c, f, notice{Type: "error", Message: template.HTML(html.EscapeString(err.Error()))})
			return
		}
		back(map[string]string{"add": "added", "update": "updated"}[action])
	case "delete":
		if !s.checkNonce(c, "delete-lang") {
			s.denyNonce(c)
			return
		}
		l := lang()
		if l == nil {
			back("deleted")
			return
		}
		posts, terms := s.pllLangContentCount(ctx, l)
		mode := c.Query("content")
		if (posts+terms) > 0 && s.pllHasOtherLanguage(ctx, l) && mode != "trash" && mode != "draft" && mode != "default" {
			base := "/wp-admin/admin?page=mlang&pll_action=delete&lang=" + itoa64(l.ID) + "&_wpnonce=" + s.nonce(c, "delete-lang") + "&content="
			msg := fmt.Sprintf("%s has %d post(s) and %d term(s). Choose what happens to them: <a href=\"%s\">move the posts to the Trash</a> | <a href=\"%s\">turn the posts into drafts</a> | <a href=\"%s\">move the content to the default language</a>. Nothing was deleted.",
				html.EscapeString(l.Name), posts, terms, html.EscapeString(base+"trash"), html.EscapeString(base+"draft"), html.EscapeString(base+"default"))
			s.pllRenderLanguages(c, nil, notice{Type: "error", Message: template.HTML(msg)})
			return
		}
		if err := s.pllDeleteLanguageWith(ctx, l, mode); err != nil {
			s.pllRenderLanguages(c, nil, notice{Type: "error", Message: template.HTML(html.EscapeString("The language could not be deleted: " + err.Error()))})
			return
		}
		back("deleted")
	case "default-lang":
		if !s.checkNonce(c, "default-lang") {
			s.denyNonce(c)
			return
		}
		if l := lang(); l != nil {
			o := s.pllOpts()
			o.DefaultLang = l.Slug
			err := pllLog("saving the default language", s.pllSaveOpts(ctx, o))
			if _, e := s.store.Pool.Exec(ctx, `UPDATE pll_languages SET active = true WHERE id = $1`, l.ID); err == nil {
				err = pllLog("activating the default language", e)
			}
			s.pllFlush()
			if err != nil {
				back("save_error")
				return
			}
		}
		back("default")
	case "toggle":
		if !s.checkNonce(c, "toggle-lang") {
			s.denyNonce(c)
			return
		}
		msg := "updated"
		if l := lang(); l != nil && !l.IsDefault {
			_, err := s.store.Pool.Exec(ctx, `UPDATE pll_languages SET active = NOT active, updated_at = now() WHERE id = $1`, l.ID)
			if pllLog("toggling a language", err) != nil {
				back("save_error")
				return
			}
			s.pllFlush()
			s.purgeAll(ctx)
			msg = map[bool]string{true: "deactivated", false: "activated"}[l.Active]
		}
		back(msg)
	case "content-default-lang":
		if !s.checkNonce(c, "content-default-lang") {
			s.denyNonce(c)
			return
		}
		_ = s.pllAssignDefault(ctx)
		s.purgeAll(ctx)
		back("content")
	default:
		c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang")
	}
}

// pllSaveLanguage validates and stores a language (the Add new language / Edit language form, REST).
// The first language becomes the default one and takes the existing content; later ones get their
// translation of the default category.
func (s *Server) pllSaveLanguage(ctx context.Context, f *pllLanguage, isNew bool) error {
	if f.Name == "" {
		return fmt.Errorf("The language must have a name")
	}
	if !pllLocaleRe.MatchString(f.Locale) {
		return fmt.Errorf("Enter a valid WordPress locale")
	}
	if !pllSlugRe.MatchString(f.Slug) {
		return fmt.Errorf("The language code contains invalid characters")
	}
	for _, l := range s.pllLanguages(ctx) {
		if l.Slug == f.Slug && (isNew || l.ID != f.ID) {
			return fmt.Errorf("The language code must be unique")
		}
	}
	f.Flag = strings.ToLower(strings.TrimSpace(f.Flag))
	if isNew {
		first := len(s.pllLanguages(ctx)) == 0
		if err := s.store.Pool.QueryRow(ctx, `INSERT INTO pll_languages (slug, name, locale, rtl, flag, term_order, active) VALUES ($1,$2,$3,$4,$5,$6,true) RETURNING id`,
			f.Slug, f.Name, f.Locale, f.RTL, f.Flag, f.Order).Scan(&f.ID); err != nil {
			return err
		}
		s.pllFlush()
		if first {
			o := s.pllOpts()
			o.DefaultLang = f.Slug
			if err := s.pllSaveOpts(ctx, o); err != nil {
				return err
			}
			_ = s.pllAssignDefault(ctx)
		} else {
			s.pllDefaultCategory(ctx, f)
		}
		s.purgeAll(ctx)
		return nil
	}
	var old string
	_ = s.store.Pool.QueryRow(ctx, `SELECT slug FROM pll_languages WHERE id = $1`, f.ID).Scan(&old)
	if old == "" {
		return fmt.Errorf("Invalid language")
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE pll_languages SET slug=$2, name=$3, locale=$4, rtl=$5, flag=$6, term_order=$7, updated_at=now() WHERE id=$1`,
		f.ID, f.Slug, f.Name, f.Locale, f.RTL, f.Flag, f.Order); err != nil {
		return err
	}
	if old != f.Slug { // the code is a key of the settings too
		o := s.pllOpts()
		if o.DefaultLang == old {
			o.DefaultLang = f.Slug
		}
		if d, ok := o.Domains[old]; ok {
			delete(o.Domains, old)
			o.Domains[f.Slug] = d
		}
		for _, locs := range o.NavMenus {
			for _, m := range locs {
				if id, ok := m[old]; ok {
					delete(m, old)
					m[f.Slug] = id
				}
			}
		}
		_ = s.pllSaveOpts(ctx, o)
		// The string translations are cached by language code (the rows followed the code by ON
		// UPDATE CASCADE): the old code's cache would leave the language untranslated.
		s.pllFlushStrings()
		s.pllMediaLangsForget()
	}
	s.pllFlush()
	s.purgeAll(ctx)
	return nil
}

// pllDefaultCategory creates the default category of a new language, linked to the default category.
func (s *Server) pllDefaultCategory(ctx context.Context, l *pllLanguage) {
	defID := int64(s.settings.Int("default_category"))
	def, err := s.store.GetTerm(ctx, defID)
	if err != nil || def.Taxonomy != "category" {
		return
	}
	tr := s.pllTranslations(ctx, pllTerm, defID)
	if tr[l.Slug] > 0 {
		return
	}
	t := &store.Term{Taxonomy: "category", Name: def.Name, Slug: def.Slug + "-" + l.Slug}
	if err := s.store.InsertTerm(context.WithValue(ctx, pllCtxKey{}, &pllReq{srv: s, Lang: l, Filter: true}), t); err != nil {
		return
	}
	tr[l.Slug] = t.ID
	_ = s.pllSaveTranslations(ctx, pllTerm, tr)
}

// pllDeleteLanguage removes a language: its content stays (in the default language), translation links to
// it, its string translations and its menu locations go. Deleting the default language picks another.
func (s *Server) pllDeleteLanguage(ctx context.Context, l *pllLanguage) error {
	return s.pllDeleteLanguageWith(ctx, l, "")
}

// pllLangContentCount counts the posts (any status but auto-draft) and terms that belong to a language.
func (s *Server) pllLangContentCount(ctx context.Context, l *pllLanguage) (posts, terms int) {
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_post_langs pl JOIN posts p ON p.id = pl.post_id
		WHERE pl.lang = $1 AND p.status <> 'auto-draft'`, l.Slug).Scan(&posts)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_term_langs WHERE lang = $1`, l.Slug).Scan(&terms)
	return
}

func (s *Server) pllHasOtherLanguage(ctx context.Context, l *pllLanguage) bool {
	for _, o := range s.pllLanguages(ctx) {
		if o.ID != l.ID {
			return true
		}
	}
	return false
}

// pllDeleteLanguageWith deletes a language after dealing with its content in one transaction: mode "trash"
// moves its posts to the Trash, "draft" turns them into drafts, "default" (and, for terms, every mode) moves
// them to the default language as untranslated items. An empty mode touches nothing (the language is empty).
func (s *Server) pllDeleteLanguageWith(ctx context.Context, l *pllLanguage, mode string) error {
	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	target := ""
	for _, o := range s.pllLanguages(ctx) {
		if o.ID != l.ID && (target == "" || o.IsDefault) {
			target = o.Slug
		}
	}
	if mode != "" && target != "" {
		switch mode {
		case "trash":
			_, err = tx.Exec(ctx, `UPDATE posts SET trashed_status = status, status = 'trash', deleted_at = now() WHERE status NOT IN ('trash','auto-draft')
				AND id IN (SELECT post_id FROM pll_post_langs WHERE lang = $1)`, l.Slug)
		case "draft":
			_, err = tx.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE status NOT IN ('trash','auto-draft')
				AND id IN (SELECT post_id FROM pll_post_langs WHERE lang = $1)`, l.Slug)
		case "default":
			_, err = tx.Exec(ctx, `UPDATE pll_post_langs SET lang = $2, grp = nextval('pll_translation_groups') WHERE lang = $1`, l.Slug, target)
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE pll_term_langs SET lang = $2, grp = nextval('pll_translation_groups') WHERE lang = $1`, l.Slug, target); err != nil {
			return err
		}
		if mode != "default" {
			// the posts stay in the database; detach them from the deleted language as default-language items
			if _, err = tx.Exec(ctx, `UPDATE pll_post_langs SET lang = $2, grp = nextval('pll_translation_groups') WHERE lang = $1`, l.Slug, target); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM pll_languages WHERE id = $1`, l.ID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.pllFlush()
	s.pllFlushStrings() // its string translations went with it (ON DELETE CASCADE)
	s.pllMediaLangsForget()
	o := s.pllOpts()
	delete(o.Domains, l.Slug)
	for _, locs := range o.NavMenus {
		for _, m := range locs {
			delete(m, l.Slug)
		}
	}
	if o.DefaultLang == l.Slug {
		o.DefaultLang = ""
		if rest := s.pllLanguages(ctx); len(rest) > 0 {
			o.DefaultLang = rest[0].Slug
		}
	}
	return s.pllSaveOpts(ctx, o)
}

// Languages > Settings -----------------------------------------------------------------------

type pllModule struct {
	Key, Title, Description string
	Active                  bool // shown for toggleable modules
	Toggle                  bool
	Pro                     bool
	Unavailable             string // why a module cannot be used in WordPress Go
	Open                    bool
}

type pllSettingsData struct {
	Tabs        []pllTab
	Modules     []pllModule
	O           pllOptions
	Langs       []*pllLanguage
	Pretty      bool
	StaticFront bool
	PostTypes   []pllChoice
	Taxonomies  []pllChoice
	SyncItems   []pllChoice
	Nonce       string
	Module      string
	MT          pllMTView // Machine translation module (golylang_mt.go)
}

type pllChoice struct {
	Value, Label string
	Checked      bool
}

var pllSyncLabels = [][2]string{{"taxonomies", "Taxonomies"}, {"post_meta", "Custom fields"}, {"comment_status", "Comment status"},
	{"ping_status", "Ping status"}, {"sticky_posts", "Sticky posts"}, {"post_date", "Published date"}, {"post_format", "Post format"},
	{"post_parent", "Page parent"}, {"_wp_page_template", "Page template"}, {"menu_order", "Page order"}, {"_thumbnail_id", "Featured image"}}

func (s *Server) pllSettingsScreen(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost {
		s.pllSettingsSave(c)
		return
	}
	if a := c.Query("pll_action"); a == "activate" || a == "deactivate" {
		if !s.checkNonce(c, "pll-module") {
			s.denyNonce(c)
			return
		}
		o := s.pllOpts()
		on := a == "activate"
		switch c.Query("module") {
		case "browser":
			o.Browser = on
		case "media":
			o.MediaSupport = on
			if on {
				defer func() { _ = s.pllAssignDefault(ctx) }()
			}
		case "translate-slugs":
			o.TranslateSlugs = on
		case "share-slugs":
			o.ShareSlugs = on
		case "machine-translation":
			o.MT = on
		case "string-capture":
			o.StringCapture = on
		}
		_ = s.pllSaveOpts(ctx, o)
		c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_settings&settings-updated=true")
		return
	}
	o := s.pllOpts()
	d := pllSettingsData{Tabs: pllTabs("mlang_settings"), O: o, Langs: s.pllLanguages(ctx), Pretty: s.settings.String("permalink_structure") != "",
		StaticFront: s.settings.String("show_on_front") == "page" && s.settings.Int("page_on_front") > 0, Nonce: s.nonce(c, "pll-module"), Module: c.Query("module"),
		MT: s.pllMTViewFor(c)}
	d.Modules = []pllModule{
		{Key: "url", Title: "URL modifications", Description: "Decide how your URLs will look like."},
		{Key: "browser", Title: "Detect browser language", Toggle: true, Active: o.Browser,
			Description: "When the front page is visited, redirects to itself in the browser preferred language. As this doesn't work if it is cached, Pursae Multi Language keeps the front page out of the cache while it is on."},
		{Key: "media", Title: "Media", Toggle: true, Active: o.MediaSupport,
			Description: "Activate languages and translations for media only if you need to translate the text attached to the media: the title, the alternative text, the caption, the description... Note that the file is not duplicated."},
		{Key: "cpt", Title: "Custom post types and Taxonomies", Description: "Activate languages and translations management for the custom post types and the taxonomies."},
		{Key: "sync", Title: "Synchronization", Description: "The synchronization options allow to maintain exact same values (or translations in the case of taxonomies and page parent) of meta content between the translations of a post or page."},
		{Key: "translate-slugs", Title: "Translate slugs", Pro: true, Toggle: true, Active: o.TranslateSlugs,
			Description: "Allows to translate the category, tag, author and search bases in URLs. The bases are translated in Languages > Translations, group URL slugs."},
		{Key: "share-slugs", Title: "Share slugs", Pro: true, Toggle: true, Active: o.ShareSlugs,
			Description: "Allows to share the same URL slug across languages for posts and terms. The language must be set from the URL (directory, subdomain or domain) in URL modifications."},
		{Key: "string-capture", Title: "Record strings on pages", Toggle: true, Active: o.StringCapture,
			Description: "Lists the text of themes, plugins and builder templates found on the pages you visit (as an administrator, or in visual translation) in Languages > Translations, group Page text, so it can be translated. Visitors' page views never record anything."},
		{Key: "machine-translation", Title: "Machine translation", Pro: true, Toggle: true, Active: o.MT,
			Description: "Translate strings, posts and pages with DeepL, Google Cloud Translation or an OpenAI-compatible API. Text is sent to the chosen service only when you ask for a translation, never for visitors."},
	}
	for i := range d.Modules {
		d.Modules[i].Open = d.Modules[i].Key == d.Module
	}
	ptRows, _ := s.store.Pool.Query(ctx, `SELECT slug, name FROM content_types WHERE NOT is_builtin AND public ORDER BY name`)
	for ptRows != nil && ptRows.Next() {
		var slug, name string
		if ptRows.Scan(&slug, &name) == nil {
			d.PostTypes = append(d.PostTypes, pllChoice{slug, name, containsStr(o.PostTypes, slug)})
		}
	}
	if ptRows != nil {
		ptRows.Close()
	}
	txRows, _ := s.store.Pool.Query(ctx, `SELECT slug, name FROM taxonomies WHERE NOT is_builtin AND public ORDER BY name`)
	for txRows != nil && txRows.Next() {
		var slug, name string
		if txRows.Scan(&slug, &name) == nil {
			d.Taxonomies = append(d.Taxonomies, pllChoice{slug, name, containsStr(o.Taxonomies, slug)})
		}
	}
	if txRows != nil {
		txRows.Close()
	}
	for _, sl := range pllSyncLabels {
		d.SyncItems = append(d.SyncItems, pllChoice{sl[0], sl[1], o.syncs(sl[0])})
	}
	var notices []notice
	if e := c.Query("pll_error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(html.EscapeString(e)), Dismissible: true})
	}
	s.adminPage(c, "admin?page=mlang_settings", "Settings", "languages_page_mlang_settings", "admin/golylang-settings", d, notices...)
}

func (s *Server) pllSettingsSave(c *gin.Context) {
	ctx := c.Request.Context()
	if !s.checkNonce(c, "pll-module") {
		s.denyNonce(c)
		return
	}
	o := s.pllOpts()
	switch c.PostForm("module") {
	case "url":
		o.ForceLang, _ = strconv.Atoi(c.PostForm("force_lang"))
		if o.ForceLang < 0 || o.ForceLang > 3 {
			o.ForceLang = 1
		}
		o.HideDefault = c.PostForm("hide_default") == "1"
		o.Rewrite = c.PostForm("rewrite") != "0"
		o.Domains = map[string]string{}
		var missing []string
		for _, l := range s.pllLanguages(ctx) {
			d := strings.TrimRight(strings.TrimSpace(c.PostForm("domains["+l.Slug+"]")), "/")
			if d != "" {
				if u, err := url.Parse(d); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
					missing = append(missing, l.Name)
					continue
				}
				o.Domains[l.Slug] = d
			} else if o.ForceLang == 3 {
				missing = append(missing, l.Name)
			}
		}
		if o.ForceLang == 3 && len(missing) > 0 {
			msg := "Please enter a valid URL for " + strings.Join(missing, ", ") + "."
			if len(missing) > 1 {
				msg = "Please enter valid URLs for " + strings.Join(missing, ", ") + "."
			}
			c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_settings&module=url&pll_error="+url.QueryEscape(msg))
			return
		}
		if (o.ForceLang == 2 || o.ForceLang == 3) && s.settings.String("permalink_structure") == "" {
			o.ForceLang = 1
		}
	case "media":
		// nothing to save: media is never duplicated (one item per upload, texts translated per language)
	case "machine-translation":
		if msg := s.pllMTSettingsSave(c, &o); msg != "" {
			c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_settings&module=machine-translation&pll_error="+url.QueryEscape(msg))
			return
		}
	case "cpt":
		o.PostTypes, o.Taxonomies = []string{}, []string{}
		for _, v := range c.PostFormArray("post_types[]") {
			o.PostTypes = append(o.PostTypes, v)
		}
		for _, v := range c.PostFormArray("taxonomies[]") {
			o.Taxonomies = append(o.Taxonomies, v)
		}
	case "sync":
		o.Sync = []string{}
		for _, sl := range pllSyncLabels {
			if c.PostForm("sync["+sl[0]+"]") == "1" {
				o.Sync = append(o.Sync, sl[0])
			}
		}
	}
	if err := s.pllSaveOpts(ctx, o); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	s.pllFlush()
	c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_settings&settings-updated=true")
}

// Posts and terms lists --------------------------------------------------------------------

// pllGroups loads the translations of many objects at once: id => language => id.
func (s *Server) pllGroups(ctx context.Context, k pllKind, ids []int64) map[int64]map[string]int64 {
	out := map[int64]map[string]int64{}
	if len(ids) == 0 {
		return out
	}
	tbl, col := k.table()
	rows, err := s.store.Pool.Query(ctx, `SELECT a.`+col+`, b.`+col+`, b.lang FROM `+tbl+` a JOIN `+tbl+` b ON b.grp = a.grp WHERE a.`+col+` = ANY($1)`, ids)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, oid int64
		var lang string
		if rows.Scan(&id, &oid, &lang) == nil {
			if out[id] == nil {
				out[id] = map[string]int64{}
			}
			out[id][lang] = oid
		}
	}
	return out
}

// pllOwnLang finds the language of an object inside its group map (the default language when unset).
func pllOwnLang(m map[string]int64, id int64, def string) string {
	for lang, oid := range m {
		if oid == id {
			return lang
		}
	}
	return def
}

// pllColumnLangs are the languages that get a column: all but the one the list is filtered by.
func (s *Server) pllColumnLangs(c *gin.Context) []*pllLanguage {
	var out []*pllLanguage
	cur := s.pllCurrent(c)
	for _, l := range s.pllLanguages(c) {
		if cur == nil || cur.Slug != l.Slug {
			out = append(out, l)
		}
	}
	return out
}

func pllColumnHead(l *pllLanguage) template.HTML {
	label := string(l.FlagHTML())
	if label == "" {
		label = html.EscapeString(l.Slug)
	}
	return template.HTML(fmt.Sprintf(`<th scope="col" class="manage-column column-language_%s">%s<span class="screen-reader-text">%s</span></th>`,
		html.EscapeString(l.Slug), label, html.EscapeString(l.Name)))
}

// golylangPostColumns adds a column per language to edit.php: a tick for the post's own language, a pencil
// for an existing translation and a plus to add one.
func (s *Server) golylangPostColumns(c *gin.Context, d *postsListData, posts []*store.Post) {
	if !s.golylangOn() || len(posts) == 0 || !s.pllTranslatedType(posts[0].Type) {
		return
	}
	langs := s.pllColumnLangs(c)
	if len(langs) == 0 {
		return
	}
	for _, l := range langs {
		d.PluginHeads = append(d.PluginHeads, pllColumnHead(l))
	}
	d.ColCount += len(langs)
	ids := make([]int64, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}
	groups := s.pllGroups(c, pllPost, ids)
	def := defaultSlug(s.pllDefault(c))
	nonce := s.nonce(c, "new-post-translation")
	var trIDs []int64
	for _, m := range groups {
		for _, id := range m {
			trIDs = append(trIDs, id)
		}
	}
	outdated := s.pllOutdatedSet(c.Request.Context(), trIDs) // golylang_listfilter.go
	for i := range d.Rows {
		p := posts[i]
		m := groups[p.ID]
		own := pllOwnLang(m, p.ID, def)
		if p.Status == "publish" {
			d.Rows[i].TranslateURL = s.pllVisualRowAction(c, d.Rows[i].ViewURL, own) // golylang_visual.go
		}
		for _, l := range langs {
			var cell string
			switch {
			case own == l.Slug:
				cell = fmt.Sprintf(`<a href="/wp-admin/post?post=%d&amp;action=edit" class="pll_icon_tick" title="Edit this item"><span class="dashicons dashicons-yes" aria-hidden="true"></span><span class="screen-reader-text">Edit this item</span></a>`, p.ID)
			case m[l.Slug] > 0 && outdated[m[l.Slug]]:
				cell = fmt.Sprintf(`<a href="/wp-admin/post?post=%d&amp;action=edit" class="pll_icon_edit pll_icon_outdated" title="The translation in %s is older than its source: edit it"><span class="dashicons dashicons-update" aria-hidden="true"></span><span class="screen-reader-text">The translation in %s is outdated: edit it</span></a>`,
					m[l.Slug], html.EscapeString(l.Name), html.EscapeString(l.Name))
			case m[l.Slug] > 0:
				cell = fmt.Sprintf(`<a href="/wp-admin/post?post=%d&amp;action=edit" class="pll_icon_edit" title="Edit the translation in %s"><span class="dashicons dashicons-edit" aria-hidden="true"></span><span class="screen-reader-text">Edit the translation in %s</span></a>`,
					m[l.Slug], html.EscapeString(l.Name), html.EscapeString(l.Name))
			default:
				cell = fmt.Sprintf(`<a href="/wp-admin/post-new?post_type=%s&amp;from_post=%d&amp;new_lang=%s&amp;_wpnonce=%s" class="pll_icon_add" title="Add a translation in %s"><span class="dashicons dashicons-plus" aria-hidden="true"></span><span class="screen-reader-text">Add a translation in %s</span></a>`,
					url.QueryEscape(p.Type), p.ID, url.QueryEscape(l.Slug), nonce, html.EscapeString(l.Name), html.EscapeString(l.Name))
			}
			d.Rows[i].PluginCols = append(d.Rows[i].PluginCols, template.HTML(fmt.Sprintf(`<td class="language_%s column-language_%s" data-colname="%s">%s</td>`,
				html.EscapeString(l.Slug), html.EscapeString(l.Slug), html.EscapeString(l.Name), cell)))
		}
	}
}

// golylangTermsList adds the language column headers and the Language field of the add form.
func (s *Server) golylangTermsList(c *gin.Context, tax string, d *termsData) {
	if !s.golylangOn() || !s.pllTranslatedTaxonomy(tax) || len(s.pllLanguages(c)) == 0 {
		return
	}
	langs := s.pllColumnLangs(c)
	for _, l := range langs {
		d.PluginHeads = append(d.PluginHeads, pllColumnHead(l))
	}
	d.ColCount += len(langs)
	sel := s.pllCurrent(c)
	from, _ := strconv.ParseInt(c.Query("from_tag"), 10, 64)
	if nl := s.pllLang(c, c.Query("new_lang")); nl != nil {
		sel = nl
	}
	if sel == nil {
		sel = s.pllDefault(c)
	}
	var b strings.Builder
	b.WriteString(`<div class="form-field term-language-wrap"><label for="term_lang_choice">Language</label>`)
	b.WriteString(s.pllLangSelect(c, "term_lang_choice", "term_lang_choice", sel))
	b.WriteString(`<p>Sets the language</p></div>`)
	if from > 0 {
		fmt.Fprintf(&b, `<input type="hidden" name="from_tag" value="%d" />`, from)
	}
	d.PluginFields = template.HTML(b.String())
}

func (s *Server) pllLangSelect(c *gin.Context, name, id string, sel *pllLanguage) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<select name="%s" id="%s" class="tags-input">`, name, id)
	for _, l := range s.pllLanguages(c) {
		fmt.Fprintf(&b, `<option value="%s" lang="%s"%s>%s</option>`, html.EscapeString(l.Slug), html.EscapeString(l.W3C()),
			map[bool]string{true: ` selected="selected"`}[sel != nil && sel.Slug == l.Slug], html.EscapeString(strings.TrimSpace(l.FlagEmoji()+" "+l.Name)))
	}
	b.WriteString(`</select>`)
	return b.String()
}

// golylangTermCols are the language cells of one terms list row.
func (s *Server) golylangTermCols(c *gin.Context, t *store.Term) []template.HTML {
	if !s.golylangOn() || !s.pllTranslatedTaxonomy(t.Taxonomy) || len(s.pllLanguages(c)) == 0 {
		return nil
	}
	m := s.pllTranslations(c, pllTerm, t.ID)
	own := s.pllObjLang(c, pllTerm, t.ID)
	var out []template.HTML
	for _, l := range s.pllColumnLangs(c) {
		var cell string
		switch {
		case own == l.Slug:
			cell = fmt.Sprintf(`<a href="/wp-admin/term?taxonomy=%s&amp;tag_ID=%d" class="pll_icon_tick" title="Edit this item"><span class="dashicons dashicons-yes" aria-hidden="true"></span></a>`, url.QueryEscape(t.Taxonomy), t.ID)
		case m[l.Slug] > 0:
			cell = fmt.Sprintf(`<a href="/wp-admin/term?taxonomy=%s&amp;tag_ID=%d" class="pll_icon_edit" title="Edit the translation in %s"><span class="dashicons dashicons-edit" aria-hidden="true"></span></a>`, url.QueryEscape(t.Taxonomy), m[l.Slug], html.EscapeString(l.Name))
		default:
			cell = fmt.Sprintf(`<a href="/wp-admin/edit-tags?taxonomy=%s&amp;from_tag=%d&amp;new_lang=%s" class="pll_icon_add" title="Add a translation in %s"><span class="dashicons dashicons-plus" aria-hidden="true"></span></a>`, url.QueryEscape(t.Taxonomy), t.ID, url.QueryEscape(l.Slug), html.EscapeString(l.Name))
		}
		out = append(out, template.HTML(fmt.Sprintf(`<td class="language_%s column-language_%s" data-colname="%s">%s</td>`, html.EscapeString(l.Slug), html.EscapeString(l.Slug), html.EscapeString(l.Name), cell)))
	}
	return out
}

// golylangTermFields are the Language and Translations rows of the term edit form.
func (s *Server) golylangTermFields(c *gin.Context, t *store.Term) template.HTML {
	if !s.golylangOn() || !s.pllTranslatedTaxonomy(t.Taxonomy) || len(s.pllLanguages(c)) == 0 {
		return ""
	}
	own := s.pllLang(c, s.pllObjLang(c, pllTerm, t.ID))
	m := s.pllTranslations(c, pllTerm, t.ID)
	var b strings.Builder
	b.WriteString(`<tr class="form-field term-language-wrap"><th scope="row"><label for="term_lang_choice">Language</label></th><td>`)
	b.WriteString(s.pllLangSelect(c, "term_lang_choice", "term_lang_choice", own))
	b.WriteString(`<p class="description">Sets the language</p></td></tr>`)
	b.WriteString(`<tr class="form-field term-translations-wrap"><th scope="row">Translations</th><td><table class="widefat striped" id="term-translations"><tbody>`)
	for _, l := range s.pllLanguages(c) {
		if own != nil && l.Slug == own.Slug {
			continue
		}
		// Terms of that language that are not translated yet can be linked; the current link is preselected.
		opts := `<option value="0">&mdash;</option>`
		rows, err := s.store.Pool.Query(c, `SELECT t.id, t.name FROM terms t LEFT JOIN pll_term_langs tl ON tl.term_id = t.id
			WHERE t.taxonomy = $1 AND coalesce(tl.lang, $3) = $2 AND (t.id = $4 OR NOT EXISTS (SELECT 1 FROM pll_term_langs o
			WHERE o.grp = tl.grp AND o.term_id <> t.id)) ORDER BY lower(t.name) LIMIT 200`, t.Taxonomy, l.Slug, defaultSlug(s.pllDefault(c)), m[l.Slug])
		if err == nil {
			for rows.Next() {
				var id int64
				var name string
				if rows.Scan(&id, &name) == nil {
					opts += fmt.Sprintf(`<option value="%d"%s>%s</option>`, id, map[bool]string{true: ` selected="selected"`}[id == m[l.Slug]], html.EscapeString(name))
				}
			}
			rows.Close()
		}
		action := fmt.Sprintf(`<a href="/wp-admin/edit-tags?taxonomy=%s&amp;from_tag=%d&amp;new_lang=%s" class="pll_icon_add" title="Add a translation in %s"><span class="dashicons dashicons-plus" aria-hidden="true"></span></a>`,
			url.QueryEscape(t.Taxonomy), t.ID, url.QueryEscape(l.Slug), html.EscapeString(l.Name))
		if m[l.Slug] > 0 {
			action = fmt.Sprintf(`<a href="/wp-admin/term?taxonomy=%s&amp;tag_ID=%d" class="pll_icon_edit" title="Edit the translation in %s"><span class="dashicons dashicons-edit" aria-hidden="true"></span></a>`,
				url.QueryEscape(t.Taxonomy), m[l.Slug], html.EscapeString(l.Name))
		}
		fmt.Fprintf(&b, `<tr><td class="pll-translation-flag">%s</td><td style="width:20%%">%s</td><td class="pll-column-icon">%s</td><td><label class="screen-reader-text" for="term_tr_lang_%s">Translation</label><select name="term_tr_lang[%s]" id="term_tr_lang_%s">%s</select></td></tr>`,
			l.FlagHTML(), html.EscapeString(l.Name), action, html.EscapeString(l.Slug), html.EscapeString(l.Slug), html.EscapeString(l.Slug), opts)
	}
	b.WriteString(`</tbody></table></td></tr>`)
	return template.HTML(b.String())
}

// golylangTermSaved stores the language and the translations a term form posted (add and edit).
func (s *Server) golylangTermSaved(c *gin.Context, t *store.Term) {
	if !s.golylangOn() || !s.pllTranslatedTaxonomy(t.Taxonomy) {
		return
	}
	l := s.pllLang(c, c.PostForm("term_lang_choice"))
	if l == nil {
		if cur := s.pllCurrent(c); cur != nil {
			l = cur
		} else if l = s.pllDefault(c); l == nil {
			return
		}
	}
	_ = s.pllSetLang(c, pllTerm, t.ID, l.Slug)
	tr := map[string]int64{}
	if from, _ := strconv.ParseInt(c.PostForm("from_tag"), 10, 64); from > 0 {
		if ft, err := s.store.GetTerm(c, from); err == nil && ft.Taxonomy == t.Taxonomy {
			tr = s.pllTranslations(c, pllTerm, from)
		}
	} else if form := c.PostFormMap("term_tr_lang"); len(form) > 0 {
		for lang, v := range form {
			id, _ := strconv.ParseInt(v, 10, 64)
			if id <= 0 || id == t.ID || lang == l.Slug || s.pllLang(c, lang) == nil {
				continue
			}
			// Polylang's validate_translations: only a term of the same taxonomy that is in that
			// language joins the group; a posted id is never relabelled into another language.
			if ot, err := s.store.GetTerm(c, id); err != nil || ot.Taxonomy != t.Taxonomy || s.pllObjLang(c, pllTerm, id) != lang {
				continue
			}
			tr[lang] = id
		}
	} else {
		return
	}
	tr[l.Slug] = t.ID
	_ = s.pllSaveTranslations(c, pllTerm, tr)
	s.purgeAll(c)
}

// Languages meta box ------------------------------------------------------------------------

// golylangPostBox is the Languages box of the classic editor.
func (s *Server) golylangPostBox(c *gin.Context, p *store.Post) template.HTML {
	if !s.golylangOn() || !s.pllTranslatedType(p.Type) || len(s.pllLanguages(c)) == 0 {
		return ""
	}
	s.pllEnsureLang(c, p)
	return template.HTML(`<div id="ml_box" class="postbox"><div class="postbox-header"><h2 class="hndle ui-sortable-handle">Languages</h2>` +
		`<div class="handle-actions hide-if-no-js"><button type="button" class="handlediv" aria-expanded="true"><span class="screen-reader-text">Toggle panel: Languages</span><span class="toggle-indicator" aria-hidden="true"></span></button></div></div>` +
		`<div class="inside">` + string(s.pllBoxInner(c, p)) + `</div></div>` + pllBoxScript(s.nonce(c, "pll_language")))
}

// pllEnsureLang gives a post without a language the admin filter language, or the default one.
func (s *Server) pllEnsureLang(ctx context.Context, p *store.Post) {
	tbl, col := pllPost.table()
	var has bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+tbl+` WHERE `+col+` = $1)`, p.ID).Scan(&has)
	if has {
		return
	}
	l := s.pllCurrent(ctx)
	if l == nil {
		l = s.pllDefault(ctx)
	}
	if l != nil {
		_ = s.pllSetLang(ctx, pllPost, p.ID, l.Slug)
	}
}

// pllBoxInner is the content of the Languages box (classic editor) and panel (block editor).
func (s *Server) pllBoxInner(c *gin.Context, p *store.Post) template.HTML {
	own := s.pllLang(c, s.pllObjLang(c, pllPost, p.ID))
	m := s.pllTranslations(c, pllPost, p.ID)
	var b strings.Builder
	fmt.Fprintf(&b, `<div id="pll-language" data-post="%d"><p><strong>Language</strong></p><label class="screen-reader-text" for="post_lang_choice">Language</label>`, p.ID)
	b.WriteString(s.pllLangSelect(c, "post_lang_choice", "post_lang_choice", own))
	b.WriteString(`</div>`)
	dup, _ := currentUser(c).Meta["pll_duplicate_content"].(map[string]any)
	dupOn, _ := dup[p.Type].(bool)
	fmt.Fprintf(&b, `<p><label><input type="checkbox" id="pll_duplicate_content"%s /> Duplicate content</label><br /><span class="description">Copies the title, the content and the excerpt into new translations.</span></p>`,
		map[bool]string{true: ` checked="checked"`}[dupOn])
	if s.pllMTEnabled() {
		fmt.Fprintf(&b, `<p id="pll-mt-copy-row"%s><label><input type="checkbox" id="pll_mt_copy"%s /> Machine translate the copy</label><br /><span class="description">New translations get the duplicated title, excerpt and content translated by the machine translation service.</span></p>`,
			map[bool]string{false: ` style="display:none"`}[dupOn], map[bool]string{true: ` checked="checked"`}[pllMTCopyWanted(currentUser(c), p.Type)])
	}
	b.WriteString(`<div id="pll-translations"><p><strong>Translations</strong></p><table><tbody>`)
	nonce := s.nonce(c, "new-post-translation")
	def := defaultSlug(s.pllDefault(c))
	for _, l := range s.pllLanguages(c) {
		if own != nil && l.Slug == own.Slug {
			continue
		}
		icon := fmt.Sprintf(`<a href="/wp-admin/post-new?post_type=%s&amp;from_post=%d&amp;new_lang=%s&amp;_wpnonce=%s" class="pll_icon_add" title="Add a translation in %s"><span class="dashicons dashicons-plus" aria-hidden="true"></span><span class="screen-reader-text">Add a translation in %s</span></a>`,
			url.QueryEscape(p.Type), p.ID, url.QueryEscape(l.Slug), nonce, html.EscapeString(l.Name), html.EscapeString(l.Name))
		if m[l.Slug] > 0 {
			icon = fmt.Sprintf(`<a href="/wp-admin/post?post=%d&amp;action=edit" class="pll_icon_edit" title="Edit the translation in %s"><span class="dashicons dashicons-edit" aria-hidden="true"></span><span class="screen-reader-text">Edit the translation in %s</span></a>`,
				m[l.Slug], html.EscapeString(l.Name), html.EscapeString(l.Name))
		}
		opts := `<option value="0">&mdash;</option>`
		rows, err := s.store.Pool.Query(c, `SELECT p.id, p.title FROM posts p LEFT JOIN pll_post_langs pl ON pl.post_id = p.id
			WHERE p.type = $1 AND p.status NOT IN ('auto-draft','trash') AND coalesce(pl.lang, $3) = $2
			AND (p.id = $4 OR pl.grp IS NULL OR NOT EXISTS (SELECT 1 FROM pll_post_langs o WHERE o.grp = pl.grp AND o.post_id <> p.id))
			ORDER BY p.modified_at DESC LIMIT 200`, p.Type, l.Slug, def, m[l.Slug])
		if err == nil {
			for rows.Next() {
				var id int64
				var title string
				if rows.Scan(&id, &title) == nil {
					opts += fmt.Sprintf(`<option value="%d"%s>%s</option>`, id, map[bool]string{true: ` selected="selected"`}[id == m[l.Slug]], html.EscapeString(defaultStr(title, "(no title)")))
				}
			}
			rows.Close()
		}
		fmt.Fprintf(&b, `<tr><th class="pll-translation-flag" scope="row">%s<span class="screen-reader-text">%s</span></th><td class="pll-column-icon">%s</td><td><label class="screen-reader-text" for="tr_lang_%s">Translation</label><select class="tr_lang" data-lang="%s" id="tr_lang_%s">%s</select></td></tr>`,
			defaultStr(string(l.FlagHTML()), html.EscapeString(l.Slug)), html.EscapeString(l.Name), icon, html.EscapeString(l.Slug), html.EscapeString(l.Slug), html.EscapeString(l.Slug), opts)
	}
	b.WriteString(`</tbody></table></div>`)
	if again := s.pllMTAgainURL(c, p); again != "" {
		fmt.Fprintf(&b, `<p id="pll-mt-again"><a class="button" href="%s" onclick="return window.confirm('Replace the title, the excerpt and the content of this translation with a new machine translation of its source? Unsaved changes are lost.');">Machine translate again</a><br /><span class="description">Translates this item again from its source. The current text is kept as a revision.</span></p>`,
			html.EscapeString(again))
	}
	return template.HTML(b.String())
}

// pllBoxScript wires the Languages box: language and translation changes are saved at once (admin-ajax),
// and the box is redrawn with the answer, as Polylang does.
func pllBoxScript(nonce string) string {
	return `<script>
(function(){
var nonce = ` + strconvQuote(nonce) + `;
function post(data, done){
	var fd = new FormData(); fd.append('_pll_nonce', nonce);
	for (var k in data) { fd.append(k, data[k]); }
	fetch('/wp-admin/admin-ajax', {method: 'POST', body: fd, credentials: 'same-origin'}).then(function(r){ return r.text(); }).then(done);
}
document.addEventListener('change', function(e){
	var t = e.target, box = t.closest && t.closest('.pll-box-inner, #ml_box .inside');
	if (!box) { return; }
	var id = box.querySelector('#pll-language').getAttribute('data-post');
	if (t.id === 'post_lang_choice') {
		var lang = t.value;
		post({action: 'pll_post_lang', post_id: id, lang: lang}, function(h){
			box.innerHTML = h;
			if (window.pllPostLang !== undefined) {
				// Block editor: the term panels and the parent selector follow the new language.
				window.pllPostLang = lang;
				var core = window.wp && wp.data && wp.data.dispatch('core');
				if (core && core.invalidateResolutionForStoreSelector) { core.invalidateResolutionForStoreSelector('getEntityRecords'); }
			}
		});
	} else if (t.classList.contains('tr_lang')) {
		post({action: 'pll_link_translation', post_id: id, lang: t.getAttribute('data-lang'), target: t.value}, function(h){ box.innerHTML = h; });
	} else if (t.id === 'pll_duplicate_content') {
		post({action: 'pll_toggle_duplicate', post_id: id, on: t.checked ? '1' : '0'}, function(){});
		var mt = box.querySelector('#pll-mt-copy-row');
		if (mt) { mt.style.display = t.checked ? '' : 'none'; }
	} else if (t.id === 'pll_mt_copy') {
		post({action: 'pll_toggle_mt', post_id: id, on: t.checked ? '1' : '0'}, function(){});
	}
});
})();
</script>`
}

// golylangBlockEditor adds the Languages panel to the block editor's document sidebar.
func (s *Server) golylangBlockEditor(c *gin.Context, p *store.Post) {
	if !s.golylangOn() || len(s.pllLanguages(c)) == 0 {
		return
	}
	a := s.pageAssets(c)
	a.AddInline("wp-edit-post", pllSwitcherBlockScript)
	if !s.pllTranslatedType(p.Type) {
		return
	}
	s.pllEnsureLang(c, p)
	a.Script("wp-plugins")
	inner := strconvQuote(string(s.pllBoxInner(c, p)))
	a.AddInline("wp-edit-post", `( function() {
	var el = wp.element.createElement, Panel = ( wp.editor && wp.editor.PluginDocumentSettingPanel ) || wp.editPost.PluginDocumentSettingPanel;
	wp.plugins.registerPlugin( 'golylang-languages', { render: function() {
		return el( Panel, { name: 'golylang-languages', title: 'Languages', className: 'pll-panel' },
			el( 'div', { className: 'pll-box-inner', id: 'ml_box_inner', dangerouslySetInnerHTML: { __html: `+inner+` } } ) );
	} } );
} )();`)
	a.AddInline("wp-edit-post", s.pllEditorFetchScript(c, p))
	addBodyHTML(c, template.HTML(pllBoxScript(s.nonce(c, "pll_language"))+`<style>`+pllAdminCSS+`</style>`))
}

// pllEditorFetchScript is Polylang's block editor apiFetch middleware: the term panels and the parent
// page selector ask for the edited post's language (lang=<post language>), so they list that
// language's categories, tags and pages, and a term created from a panel gets that language. Single
// object reads and the post's own saves are left alone. window.pllPostLang follows the Languages panel
// (pllBoxScript).
func (s *Server) pllEditorFetchScript(c *gin.Context, p *store.Post) string {
	bases := []string{}
	for _, tax := range s.pllTaxonomies() {
		switch tax {
		case "category":
			bases = append(bases, "categories")
		case "post_tag":
			bases = append(bases, "tags")
		default:
			bases = append(bases, tax)
		}
	}
	rawBases, _ := json.Marshal(bases)
	return `( function() {
	window.pllPostLang = ` + strconvQuote(s.pllObjLang(c, pllPost, p.ID)) + `;
	var terms = ` + string(rawBases) + `;
	if ( ! wp.apiFetch || ! wp.url ) { return; }
	wp.apiFetch.use( function( options, next ) {
		var lang = window.pllPostLang, key = options.path ? 'path' : ( options.url ? 'url' : '' );
		if ( ! lang || ! key ) { return next( options ); }
		var target = options[ key ], method = ( options.method || 'GET' ).toUpperCase();
		var route = key === 'path' ? target : ( wp.url.getQueryArg( target, 'rest_route' ) || target );
		var m = /\/wp\/v2\/([\w-]+)\/?(?:\?|$)/.exec( route );
		if ( ! m || wp.url.getQueryArg( target, 'lang' ) !== undefined ) { return next( options ); }
		var add = ( terms.indexOf( m[1] ) >= 0 && ( method === 'GET' || method === 'POST' ) ) || ( m[1] === 'pages' && method === 'GET' );
		if ( add ) {
			options = Object.assign( {}, options );
			options[ key ] = wp.url.addQueryArgs( target, { lang: lang } );
		}
		return next( options );
	} );
} )();`
}

// golylangAjax answers the Languages box actions.
func (s *Server) golylangAjax(c *gin.Context, action string) bool {
	switch action {
	case "pll_post_lang", "pll_link_translation", "pll_toggle_duplicate", "pll_toggle_mt":
	default:
		return false
	}
	if !s.golylangOn() {
		c.String(http.StatusBadRequest, "0")
		return true
	}
	if !s.checkNonceValue(c, c.PostForm("_pll_nonce"), "pll_language") {
		c.String(http.StatusForbidden, "-1")
		return true
	}
	id, _ := strconv.ParseInt(c.PostForm("post_id"), 10, 64)
	p, err := s.store.GetPost(c, id)
	if err != nil || !currentUser(c).Can("edit_post", p) || !s.pllTranslatedType(p.Type) {
		c.String(http.StatusForbidden, "-1")
		return true
	}
	switch action {
	case "pll_post_lang":
		if l := s.pllLang(c, c.PostForm("lang")); l != nil {
			if pllLog("setting the post language", s.pllSetLang(c, pllPost, p.ID, l.Slug)) != nil {
				c.String(http.StatusInternalServerError, "-1")
				return true
			}
			s.pllReslug(c, p) // Share slugs: the slug may be taken in the new language
		}
	case "pll_link_translation":
		l := s.pllLang(c, c.PostForm("lang"))
		target, _ := strconv.ParseInt(c.PostForm("target"), 10, 64)
		if l != nil {
			tr := s.pllTranslations(c, pllPost, p.ID)
			if old := tr[l.Slug]; old > 0 {
				_ = s.pllUnlink(c, pllPost, old)
				delete(tr, l.Slug)
			}
			if target > 0 {
				if tp, err := s.store.GetPost(c, target); err == nil && tp.Type == p.Type && currentUser(c).Can("edit_post", tp) {
					_ = s.pllSetLang(c, pllPost, tp.ID, l.Slug)
					s.pllReslug(c, tp)
					tr[l.Slug] = tp.ID
				}
			}
			tr[s.pllObjLang(c, pllPost, p.ID)] = p.ID
			if pllLog("linking the translation", s.pllSaveTranslations(c, pllPost, tr)) != nil {
				c.String(http.StatusInternalServerError, "-1")
				return true
			}
		}
	case "pll_toggle_duplicate":
		u := currentUser(c)
		dup, _ := u.Meta["pll_duplicate_content"].(map[string]any)
		if dup == nil {
			dup = map[string]any{}
		}
		dup[p.Type] = c.PostForm("on") == "1"
		_ = s.setUserMeta(c, u, "pll_duplicate_content", dup)
		c.String(http.StatusOK, "1")
		return true
	case "pll_toggle_mt":
		u := currentUser(c)
		mt, _ := u.Meta["pll_mt_copy"].(map[string]any)
		if mt == nil {
			mt = map[string]any{}
		}
		mt[p.Type] = c.PostForm("on") == "1"
		_ = s.setUserMeta(c, u, "pll_mt_copy", mt)
		c.String(http.StatusOK, "1")
		return true
	}
	if wbIsTemplate(p) {
		// A template's language and translation group decide which template wins per language:
		// rebuild the template index, not only the page cache.
		s.wbTemplatesChanged(c)
	} else {
		s.purgeAll(c)
	}
	s.adminHTML(c, http.StatusOK, string(s.pllBoxInner(c, p)))
	return true
}

// golylangNewPost prepares post-new: the language (new_lang, the admin filter or the default one) and,
// for a new translation (from_post), the link to the source and its copied data.
func (s *Server) golylangNewPost(c *gin.Context, p *store.Post) {
	if !s.golylangOn() || !s.pllTranslatedType(p.Type) || len(s.pllLanguages(c)) == 0 {
		return
	}
	l := s.pllLang(c, c.Query("new_lang"))
	if l == nil {
		l = s.pllCurrent(c)
	}
	if l == nil {
		l = s.pllDefault(c)
	}
	_ = s.pllSetLang(c, pllPost, p.ID, l.Slug)
	from, _ := strconv.ParseInt(c.Query("from_post"), 10, 64)
	if from <= 0 || !s.checkNonce(c, "new-post-translation") {
		return
	}
	src, err := s.store.GetPost(c, from)
	if err != nil || src.Type != p.Type || !currentUser(c).Can("edit_post", src) {
		return
	}
	tr := s.pllTranslations(c, pllPost, src.ID)
	if tr[l.Slug] > 0 && tr[l.Slug] != p.ID {
		return // already translated
	}
	tr[l.Slug] = p.ID
	_ = s.pllSaveTranslations(c, pllPost, tr)
	dup, _ := currentUser(c).Meta["pll_duplicate_content"].(map[string]any)
	duplicate, _ := dup[p.Type].(bool)
	s.pllCopyForTranslation(c, src, p, l, duplicate)
	s.pllMTAfterCopy(c, src, p, l, duplicate) // Machine translate the copy (golylang_mt.go)
}

// Menus ---------------------------------------------------------------------------------------

// golylangNavMenus turns each theme location into one location per language on Manage Locations
// ("Primary Menu English", "Primary Menu Français") and adds the Language switcher box.
func (s *Server) golylangNavMenus(c *gin.Context, d *navMenusData, menus []*store.Menu) {
	if !s.golylangOn() {
		return
	}
	langs := s.pllLanguages(c)
	if len(langs) == 0 {
		return
	}
	d.PluginBoxes = template.HTML(`<li class="control-section accordion-section add-pll_lang_switch_box" id="add-pll_lang_switch_box">
	<h3 class="accordion-section-title hndle"><button type="button" class="accordion-trigger" aria-expanded="false" aria-controls="pll_lang_switch_box-content"><span class="accordion-title">Language switcher <span class="dashicons dashicons-arrow-down" aria-hidden="true"></span></span></button></h3>
	<div class="accordion-section-content" id="pll_lang_switch_box-content"><div class="inside"><div id="posttype-lang-switch" class="posttypediv"><div id="tabs-panel-lang-switch" class="tabs-panel tabs-panel-active">
	<ul id="lang-switch-checklist" class="categorychecklist form-no-clear">` + string(checklistHTML(checklistItem{Type: "custom", Object: "custom", Title: "Languages", URL: "#pll_switcher"}, -1, d.Disabled)) + `</ul></div>
	<p class="button-controls" data-items-type="posttype-lang-switch"><span class="add-to-menu"><input type="submit"` + map[bool]string{true: ` disabled='disabled'`}[d.Disabled] + ` class="button submit-add-to-menu right" value="Add to Menu" name="add-post-type-menu-item" id="submit-posttype-lang-switch" /><span class="spinner"></span></span></p>
	</div></div></div></li>`)
	if d.Tab != "locations" {
		return
	}
	def := s.pllDefault(c)
	o := s.pllOpts()
	theme := s.activeTheme().Slug
	var rows []locationRow
	for _, row := range d.Locations {
		base := row
		base.Name = row.Name + " " + def.Name
		rows = append(rows, base)
		for _, l := range langs {
			if l.IsDefault {
				continue
			}
			lr := locationRow{Slug: row.Slug + "___" + l.Slug, Name: row.Name + " " + l.Name}
			lr.MenuID = o.NavMenus[theme][row.Slug][l.Slug]
			lr.Options = append(lr.Options, menuChoice{Value: "0", Label: "&mdash; Select a Menu &mdash;"})
			for _, m := range menus {
				lr.Options = append(lr.Options, menuChoice{Value: itoa64(m.ID), Label: template.HTMLEscapeString(m.Name), Selected: m.ID == lr.MenuID})
			}
			rows = append(rows, lr)
		}
	}
	d.Locations = rows
}

// golylangSaveMenuLocations stores the locations of the other languages (option nav_menus).
func (s *Server) golylangSaveMenuLocations(c *gin.Context) {
	if !s.golylangOn() {
		return
	}
	o := s.pllOpts()
	theme := s.activeTheme().Slug
	locs := map[string]map[string]int64{}
	for _, loc := range s.activeTheme().MenuLocations {
		locs[loc.Slug] = map[string]int64{}
		for _, l := range s.pllLanguages(c) {
			if l.IsDefault {
				continue
			}
			if id, _ := strconv.ParseInt(c.PostForm("menu-locations["+loc.Slug+"___"+l.Slug+"]"), 10, 64); id > 0 {
				locs[loc.Slug][l.Slug] = id
			}
		}
	}
	o.NavMenus[theme] = locs
	_ = s.pllSaveOpts(c, o)
}

// helpers -----------------------------------------------------------------------------------
