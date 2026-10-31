// Package config reads runtime configuration from the environment.
package config

import (
	"os"
	"strings"
)

type Config struct {
	Addr        string // listen address, e.g. ":8080"
	DatabaseURL string
	SiteURL     string // public base URL without trailing slash, used on first install
	UploadsDir  string
	Secret      string // optional; generated and stored in settings when empty
	WebDir      string // when set, web assets and templates are read from this directory and reloaded per request
	HubURL      string // the WordPress Go Hub (marketplace, release checks, site count); "off" disables it
	// ThemeLibraryDir (WPGO_THEME_LIBRARY_DIR) serves the theme library from a directory of theme package
	// directories (development, offline sites, tests) instead of Pursae Manager.
	ThemeLibraryDir string
	// TrustedProxies lists the proxies (IPs or CIDRs, comma separated) whose X-Forwarded-For / X-Real-IP is believed
	// when deriving the visitor address. Empty: loopback and private ranges; "none": no proxy is trusted.
	TrustedProxies string
	// ExposeUsers (WPGO_EXPOSE_USERS=1) lets visitors list accounts (REST users, /?author=N, the users sitemap). Off by
	// default: those routes disclose account names.
	ExposeUsers bool
	// LangDir holds the downloaded language packs (WP_LANG_DIR); default: "languages" next to the uploads.
	LangDir         string
	TranslationsAPI string // the WordPress.org translation API; default https://api.wordpress.org/translations

	// Outgoing mail. Without SMTPHost, messages are written as .eml files to MailDir (development).
	SMTPHost, SMTPUser, SMTPPass, MailFrom string
	SMTPPort                               string
	MailDir                                string
}

func Load() Config {
	c := Config{
		Addr:            env("WPGO_ADDR", ":8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable"),
		SiteURL:         strings.TrimRight(env("WPGO_SITE_URL", "http://localhost:8080"), "/"),
		UploadsDir:      env("WPGO_UPLOADS_DIR", "./data/uploads"),
		Secret:          os.Getenv("WPGO_SECRET"),
		WebDir:          os.Getenv("WPGO_WEB_DIR"),
		HubURL:          strings.TrimRight(env("WPGO_HUB_URL", "https://hub.wordpressgo.dev"), "/"),
		ThemeLibraryDir: os.Getenv("WPGO_THEME_LIBRARY_DIR"),
		TrustedProxies:  os.Getenv("WPGO_TRUSTED_PROXIES"),
		ExposeUsers:     os.Getenv("WPGO_EXPOSE_USERS") == "1",
		LangDir:         os.Getenv("WPGO_LANG_DIR"),
		TranslationsAPI: os.Getenv("WPGO_TRANSLATIONS_API"),
		SMTPHost:        os.Getenv("WPGO_SMTP_HOST"),
		SMTPPort:        env("WPGO_SMTP_PORT", "587"),
		SMTPUser:        os.Getenv("WPGO_SMTP_USER"),
		SMTPPass:        os.Getenv("WPGO_SMTP_PASS"),
		MailFrom:        os.Getenv("WPGO_MAIL_FROM"),
		MailDir:         os.Getenv("WPGO_MAIL_DIR"),
	}
	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
