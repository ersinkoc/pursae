package server

import (
	"html/template"
	"log"
	"strings"
	"sync"
)

// Theme templates read settings through setting, settingStr, settingBool, settingInt and settingID.
// A theme is not trusted with the site's secrets: a package from the Library or an upload, an
// overridden template in the Theme File Editor and an ACF block of a theme all render with these
// functions, and whatever they print reaches every visitor. So outside the core admin templates
// (adminSettingFuncs, templates.adminFuncs) the functions answer only the public settings below and
// the zero value ("", false, 0, nil) for any other key: never auth_secret, license keys, payment or
// captcha secrets, SMTP/CDN credentials, the admin e-mail or the built-in plugins' configuration.
//
// The list is what a visitor can learn from the site anyway: WordPress' public options (the same
// set Wasm plugins may read, pluginPublicOptions), the site logo and the theme mods of the active
// theme. None of the bundled themes (web/themes) or the Library themes (library/themes) calls a
// setting* function today; site data a theme needs is in the view (.SiteName, .Description,
// .HomeURL, .CustomLogo, .SiteIcon, theme_mod, nav_menu, …).

// themeExtraPublicSettings are public settings beyond pluginPublicOptions.
var themeExtraPublicSettings = map[string]bool{
	"site_logo": true, // the core/site-logo block's attachment, public in the REST index
}

// themeSettingPublic reports whether a theme template may read the setting key.
func (s *Server) themeSettingPublic(key string) bool {
	if pluginPublicOptions[key] || themeExtraPublicSettings[key] {
		return true
	}
	if mods, ok := strings.CutPrefix(key, "theme_mods_"); ok && mods != "" { // get_theme_mods() of the active theme
		return mods == s.settings.String("stylesheet") || mods == s.settings.String("template")
	}
	return false
}

// themeSettingRefused remembers the keys logged by themeSetting: once per key, so a theme author sees
// why a value is empty, and at most themeSettingLogMax keys (a key built from request data cannot
// grow the set or flood the log).
var themeSettingRefused struct {
	sync.Mutex
	keys map[string]bool
}

const themeSettingLogMax = 64

// themeSetting reports whether a theme template may read key, logging a refused key.
func (s *Server) themeSetting(key string) bool {
	if s.themeSettingPublic(key) {
		return true
	}
	r := &themeSettingRefused
	r.Lock()
	logIt := !r.keys[key] && len(r.keys) < themeSettingLogMax
	if logIt {
		if r.keys == nil {
			r.keys = map[string]bool{}
		}
		r.keys[key] = true
	}
	r.Unlock()
	if logIt {
		log.Printf("template: setting %q is not a public setting; theme templates read it as empty", key) //nolint:gosec // G706: quoted by %q (no raw newlines), at most themeSettingLogMax lines
	}
	return false
}

// themeSettingFuncs are setting* of the theme and plugin templates: public settings only.
func (s *Server) themeSettingFuncs() template.FuncMap {
	return template.FuncMap{
		"setting": func(key string) any {
			if !s.themeSetting(key) {
				return nil
			}
			return s.settings.Get(key)
		},
		"settingStr": func(key string) string {
			if !s.themeSetting(key) {
				return ""
			}
			return s.settings.String(key)
		},
		"settingBool": func(key string) bool { return s.themeSetting(key) && s.settings.Bool(key) },
		"settingInt": func(key string) int {
			if !s.themeSetting(key) {
				return 0
			}
			return s.settings.Int(key)
		},
		"settingID": func(key string) int64 {
			if !s.themeSetting(key) {
				return 0
			}
			return int64(s.settings.Int(key))
		},
	}
}

// adminSettingFuncs are setting* of the core admin templates (embedded, never theme or plugin code):
// any setting, as the option screens need.
func (s *Server) adminSettingFuncs() template.FuncMap {
	return template.FuncMap{
		"setting":     func(key string) any { return s.settings.Get(key) },
		"settingStr":  func(key string) string { return s.settings.String(key) },
		"settingBool": func(key string) bool { return s.settings.Bool(key) },
		"settingInt":  func(key string) int { return s.settings.Int(key) },
		"settingID":   func(key string) int64 { return int64(s.settings.Int(key)) },
	}
}
