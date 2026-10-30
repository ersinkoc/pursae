package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
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
	"two-factor", "wps-hide-login", "limit-login-attempts-reloaded",
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
		}
	}
	return s.siteLocale()
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
	return setDocumentLanguage(out, loc)
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
