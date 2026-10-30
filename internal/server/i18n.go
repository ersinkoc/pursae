package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
)

// The admin in the user's language, like WordPress: Settings › General › Site Language picks the
// site's locale and downloads its language pack, Profile › Language overrides it per user. Pages
// are translated after they are rendered (i18n.Catalog.TranslateHTML), scripts get their JSON
// translations like load_script_textdomain().

// langPluginPacks are the WordPress.org plugins whose translations the native plugins share
// strings with: they are downloaded together with the core pack.
var langPluginPacks = []string{
	"classic-editor", "elementor", "contact-form-7", "wp-mail-smtp", "wordpress-seo", "seo-by-rank-math",
	"advanced-custom-fields", "polylang", "maintenance", "litespeed-cache", "cookie-law-info", "cookie-notice",
	"two-factor", "wps-hide-login", "limit-login-attempts-reloaded", "woocommerce",
}

func (s *Server) initLanguages() {
	dir := s.cfg.LangDir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(filepath.Clean(s.cfg.UploadsDir)), "languages")
	}
	var extra fs.FS
	if sub, err := fs.Sub(s.web, "languages"); err == nil {
		extra = sub
	}
	s.lang = i18n.NewManager(dir, extra)
	if path := os.Getenv("WPGO_I18N_MISSES"); path != "" && i18n.MissHook == nil {
		var mu sync.Mutex
		seen := map[string]bool{}
		i18n.MissHook = func(text string) {
			mu.Lock()
			defer mu.Unlock()
			if seen[text] {
				return
			}
			seen[text] = true
			if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				b, _ := json.Marshal(text)
				f.Write(append(b, '\n'))
				f.Close()
			}
		}
	}
}

func (s *Server) langInstaller() *i18n.Installer {
	return &i18n.Installer{API: s.cfg.TranslationsAPI}
}

// siteLocale is get_locale(): the WPLANG option, en_US when empty.
func (s *Server) siteLocale() string {
	if l := s.settings.String("WPLANG"); l != "" {
		return l
	}
	return "en_US"
}

// userLocale is get_user_locale(): the user's "locale" meta, else the site's.
func (s *Server) userLocale(c *gin.Context) string {
	if c != nil {
		if u := currentUser(c); u != nil {
			if v, _ := u.Meta["locale"].(string); v != "" {
				return v
			}
		} else if l := s.pickedLocale(c); l != "" {
			return l
		}
	}
	return s.siteLocale()
}

const langCookie = "wp_lang"

// pickedLocale is the language a visitor chose on the install and login screens (the wp_lang
// cookie, like WordPress): only there, and only an installed language.
func (s *Server) pickedLocale(c *gin.Context) string {
	if c.Request == nil || s.lang == nil {
		return ""
	}
	p := c.Request.URL.Path
	if !strings.HasPrefix(p, "/wp-login") && !strings.HasPrefix(p, "/wp-admin/install") && !s.isCustomLogin(p) {
		return ""
	}
	if v, ok := c.Get("wpgo.pickedLocale"); ok {
		l, _ := v.(string)
		return l
	}
	l := ""
	for _, cand := range []string{c.Query("wp_lang"), c.Query("language")} {
		if cand != "" {
			l = cand
			break
		}
	}
	if l == "" {
		if ck, err := c.Request.Cookie(langCookie); err == nil {
			l = ck.Value
		}
	}
	if l == "en_US" || (l != "" && validLanguage(l) && s.lang.IsInstalled(l)) {
		c.Set("wpgo.pickedLocale", l)
		return l
	}
	c.Set("wpgo.pickedLocale", "")
	return ""
}

func (s *Server) isCustomLogin(p string) bool {
	if !s.adminsecOn() {
		return false
	}
	lp := s.loginPath()
	return lp != "/wp-login" && strings.TrimSuffix(p, "/") == lp
}

// rememberLocale stores a language chosen on the login screen for later visits (a year).
func (s *Server) rememberLocale(c *gin.Context, locale string) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	http.SetCookie(c.Writer, &http.Cookie{Name: langCookie, Value: locale, Path: "/", MaxAge: 365 * 24 * 3600,
		Secure: secure, SameSite: http.SameSiteLaxMode})
}

// withLoginLanguageSwitcher adds the language dropdown below the login box.
func (s *Server) withLoginLanguageSwitcher(c *gin.Context, out []byte) []byte {
	if q := c.Query("wp_lang"); q != "" && s.pickedLocale(c) == q {
		s.rememberLocale(c, q)
	}
	sw := s.loginLanguageSwitcher(c)
	if sw == "" {
		return out
	}
	i := bytes.LastIndex(out, []byte("</body>"))
	if i < 0 {
		return out
	}
	res := make([]byte, 0, len(out)+len(sw))
	res = append(res, out[:i]...)
	res = append(res, sw...)
	res = append(res, out[i:]...)
	return res
}

// loginLanguageSwitcher is wp-login.php's language dropdown: shown when a language besides
// English is installed.
func (s *Server) loginLanguageSwitcher(c *gin.Context) string {
	if s.lang == nil || len(s.lang.Installed()) == 0 {
		return ""
	}
	cur := s.userLocale(c)
	sel := func(v bool) string {
		if v {
			return ` selected="selected"`
		}
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="language-switcher"><form id="language-switcher" method="get">` +
		`<label for="language-switcher-locales"><span class="dashicons dashicons-translation" aria-hidden="true"></span><span class="screen-reader-text">Language</span></label>` +
		`<select name="wp_lang" id="language-switcher-locales">`)
	b.WriteString(`<option value="en_US" lang="en" translate="no"` + sel(cur == "en_US") + `>English (United States)</option>`)
	have, _ := s.languageChoices(cur, true)
	for _, l := range have {
		b.WriteString(`<option value="` + html.EscapeString(l.Locale) + `" lang="` + html.EscapeString(strings.ReplaceAll(l.Locale, "_", "-")) +
			`" translate="no"` + sel(l.Locale == cur) + `>` + html.EscapeString(l.Name) + `</option>`)
	}
	b.WriteString(`</select>`)
	for _, k := range []string{"redirect_to", "action"} { // keep the screen the visitor is on
		if v := c.Query(k); v != "" {
			b.WriteString(`<input type="hidden" name="` + k + `" value="` + html.EscapeString(v) + `" />`)
		}
	}
	b.WriteString(`<input type="submit" class="button" value="Change" /></form></div>`)
	return b.String()
}

// catalog returns the translations of the current user's language (nil-safe, empty for English).
func (s *Server) catalog(c *gin.Context) *i18n.Catalog {
	if s.lang == nil {
		return &i18n.Catalog{}
	}
	return s.lang.Catalog(s.userLocale(c))
}

// __ translates a string of Go code in the current user's language (any context matches, like
// the rendered pages).
func (s *Server) __(c *gin.Context, msgid string) string {
	if tr, ok := s.catalog(c).Lookup(msgid); ok {
		return tr
	}
	return msgid
}

// adminRequest reports whether the request is for the admin or the login screen: only those are
// translated. Front-end pages (and Golementor's preview of them) carry the user's content.
func (s *Server) adminRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	p := c.Request.URL.Path
	if strings.HasPrefix(p, "/wp-admin") || strings.HasPrefix(p, "/wp-login") || strings.HasPrefix(p, "/wp-activate") || strings.HasPrefix(p, "/wp-signup") {
		return true
	}
	if s.adminsecOn() {
		if lp := s.loginPath(); lp != "/wp-login" && strings.TrimSuffix(p, "/") == lp {
			return true
		}
	}
	return false
}

// translateAdminPage translates a rendered admin screen and sets its language and direction.
func (s *Server) translateAdminPage(c *gin.Context, out []byte) []byte {
	loc := s.userLocale(c)
	if loc == "en_US" || s.lang == nil || !s.adminRequest(c) {
		return out
	}
	cat := s.lang.Catalog(loc)
	if cat.Empty() {
		return out
	}
	out = cat.TranslateHTML(out)
	if tag := s.adminL10nTag(cat); tag != "" {
		if i := bytes.Index(out, []byte("</head>")); i >= 0 {
			out = append(out[:i:i], append([]byte(tag), out[i:]...)...)
		}
	}
	return s.rtlStylesheets(setDocumentLanguage(out, loc), loc)
}

type staticRoot struct{ prefix, dir string }

var stylesheetHref = regexp.MustCompile(`(<link\b[^>]*\bhref=["'])([^"'?#]+?)((?:\.min)?\.css)([?#][^"']*)?(["'])`)

// rtlStylesheets loads the right-to-left version of the admin stylesheets for a right-to-left
// language, like WordPress' wp_style_add_data( $handle, 'rtl', 'replace' ): "common.min.css" becomes
// "common-rtl.min.css" when the embedded assets have one (generated with rtlcss).
func (s *Server) rtlStylesheets(out []byte, loc string) []byte {
	if !i18n.IsRTL(loc) || s.web == nil {
		return out
	}
	return stylesheetHref.ReplaceAllFunc(out, func(m []byte) []byte {
		sm := stylesheetHref.FindSubmatch(m)
		path, ext := string(sm[2]), string(sm[3])
		if strings.HasSuffix(path, "-rtl") {
			return m
		}
		for _, r := range s.staticRoots {
			if !strings.HasPrefix(path, r.prefix+"/") {
				continue
			}
			rtl := r.dir + strings.TrimPrefix(path, r.prefix) + "-rtl" + ext
			if _, err := fs.Stat(s.web, rtl); err == nil {
				return []byte(string(sm[1]) + path + "-rtl" + ext + string(sm[4]) + string(sm[5]))
			}
			break
		}
		return m
	})
}

// setDocumentLanguage writes the locale into <html lang> and, for right-to-left languages, the
// direction (WordPress' language_attributes() and the "rtl" body class).
func setDocumentLanguage(out []byte, loc string) []byte {
	head := out
	if len(head) > 4096 {
		head = head[:4096]
	}
	tag := `lang="en-US"`
	i := strings.Index(string(head), tag)
	if i < 0 {
		return out
	}
	repl := `lang="` + strings.ReplaceAll(loc, "_", "-") + `"`
	if i18n.IsRTL(loc) {
		repl += ` dir="rtl"`
	}
	res := make([]byte, 0, len(out)+16)
	res = append(res, out[:i]...)
	res = append(res, repl...)
	res = append(res, out[i+len(tag):]...)
	if i18n.IsRTL(loc) {
		if j := strings.Index(string(res), `<body class="`); j >= 0 {
			k := j + len(`<body class="`)
			res = append(res[:k], append([]byte("rtl "), res[k:]...)...)
		}
	}
	return res
}

// scriptTranslations is the inline script WordPress prints before a script with translations
// (wp_set_script_translations): its JSON locale data for the "default" domain.
func (s *Server) scriptTranslations(c *gin.Context, handle, src string) string {
	if s.lang == nil {
		return ""
	}
	loc := s.userLocale(c)
	if loc == "en_US" {
		return ""
	}
	data := s.lang.ScriptTranslations(loc, src, "")
	if len(data) == 0 {
		return ""
	}
	return "<script id=\"" + handle + "-js-translations\">\nwp.i18n.setLocaleData( " + string(data) + ", \"default\" );\n</script>\n"
}

// installLanguage downloads the core pack of locale now and the plugin packs in the background
// (they only add the native plugins' strings).
func (s *Server) installLanguage(ctx context.Context, locale string) error {
	if s.lang == nil || locale == "" || locale == "en_US" {
		return nil
	}
	in := s.langInstaller()
	if !s.lang.IsInstalled(locale) {
		if err := s.lang.Install(ctx, in, locale, nil); err != nil {
			return err
		}
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.lang.Install(bg, in, locale, langPluginPacks); err != nil {
			slog.Warn("language packs", "locale", locale, "err", err)
		}
	}()
	return nil
}

// langChoice is one entry of a language dropdown.
type langChoice struct {
	Locale, Name string
	Installed    bool
	Selected     bool
}

// languageChoices lists the languages for Settings › General (installed ones first, like
// wp_dropdown_languages()); onlyInstalled limits it to what is already on disk (Profile).
func (s *Server) languageChoices(selected string, onlyInstalled bool) (installed, available []langChoice) {
	have := map[string]bool{}
	if s.lang != nil {
		for _, l := range s.lang.Installed() {
			have[l] = true
		}
	}
	for _, l := range i18n.Languages() {
		ch := langChoice{Locale: l.Locale, Name: l.Native, Installed: have[l.Locale], Selected: l.Locale == selected}
		if ch.Installed {
			installed = append(installed, ch)
		} else if !onlyInstalled {
			available = append(available, ch)
		}
	}
	return
}

// translateLabels translates the "name"/"singular_name" and every "labels" entry of a REST
// object (post types, taxonomies), like the labels WordPress registers with __().
func (s *Server) translateLabels(c *gin.Context, o map[string]any) map[string]any {
	cat := s.catalog(c)
	if cat.Empty() {
		return o
	}
	tr := func(v string) string {
		if t, ok := cat.Lookup(v); ok {
			return t
		}
		return v
	}
	out := make(map[string]any, len(o))
	for k, v := range o {
		out[k] = v
	}
	for _, k := range []string{"name", "singular_name", "description"} {
		if v, ok := out[k].(string); ok && v != "" {
			out[k] = tr(v)
		}
	}
	if labels, ok := out["labels"].(map[string]string); ok {
		l := make(map[string]string, len(labels))
		for k, v := range labels {
			l[k] = tr(v)
		}
		out["labels"] = l
	}
	return out
}

// validLanguage reports whether locale may be chosen ("" is English).
func validLanguage(locale string) bool {
	if locale == "" || locale == "en_US" {
		return true
	}
	_, ok := i18n.LanguageByLocale(locale)
	return ok
}

// adminHTML writes an admin HTML response that is not a rendered template (an Ajax fragment, a
// wp_die() screen) in the user's language.
func (s *Server) adminHTML(c *gin.Context, status int, page string) {
	out := []byte(page)
	if loc := s.userLocale(c); loc != "en_US" && s.lang != nil && s.adminRequest(c) {
		if cat := s.lang.Catalog(loc); !cat.Empty() {
			out = s.rtlStylesheets(setDocumentLanguage(cat.TranslateHTML(out), loc), loc)
		}
	}
	c.Data(status, "text/html; charset=utf-8", out)
}
