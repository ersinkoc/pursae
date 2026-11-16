// Package config reads runtime configuration from the environment.
package config

import (
	"os"
	"strings"
)

// DefaultHubURL is the Pursae Manager every Pursae install talks to.
const DefaultHubURL = "https://hub.pursae.com"

type Config struct {
	Addr        string // listen address, e.g. ":8080"
	DatabaseURL string
	SiteURL     string // public base URL without trailing slash, used on first install
	UploadsDir  string
	Secret      string // optional; generated and stored in settings when empty
	// SetupToken (WPGO_SETUP_TOKEN) guards /wp-admin/install until the site is installed. Empty: the
	// server generates one on an uninstalled site and prints the install URL (`wpgo setup-token` issues a new one).
	SetupToken string
	WebDir     string // when set, web assets and templates are read from this directory and reloaded per request
	// HubURL is Pursae Manager (marketplace, licenses, release checks, statistics): always DefaultHubURL.
	// Only a development build (-tags devhub) can point it elsewhere, with WPGO_DEV_HUB_URL
	// (config_devhub.go); "off" there disables it.
	HubURL string
	// HubOff (WPGO_HUB=off) switches Pursae Manager off in any build: the site sends nothing and gets
	// no updates, marketplace or licenses; every admin page then says how to turn it back on.
	HubOff bool
	// ThemeLibraryDir (WPGO_THEME_LIBRARY_DIR) serves the theme library from a directory of theme package
	// directories (development, offline sites, tests) instead of Pursae Manager.
	ThemeLibraryDir string
	// TrustedProxies lists the proxies (IPs or CIDRs, comma separated) whose X-Forwarded-For / X-Real-IP is believed
	// when deriving the visitor address. Empty: loopback and private ranges; "none": no proxy is trusted.
	TrustedProxies string
	// ExposeUsers (WPGO_EXPOSE_USERS=1) lets visitors list accounts (REST users, /?author=N, the users sitemap). Off by
	// default: those routes disclose account names.
	ExposeUsers bool
	// AllowUnsignedPlugins (WPGO_ALLOW_UNSIGNED_PLUGINS=1) is the server-side half of the plugin_allow_unsigned
	// setting: unsigned plugin packages are accepted only when both are on. A database or CLI write alone (an
	// administrator account, a SQL injection) cannot turn plugin signature checks off.
	AllowUnsignedPlugins bool
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
		Addr:                 env("WPGO_ADDR", ":8080"),
		DatabaseURL:          env("DATABASE_URL", "postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable"),
		SiteURL:              strings.TrimRight(env("WPGO_SITE_URL", "http://localhost:8080"), "/"),
		UploadsDir:           env("WPGO_UPLOADS_DIR", "./data/uploads"),
		Secret:               os.Getenv("WPGO_SECRET"),
		SetupToken:           strings.TrimSpace(os.Getenv("WPGO_SETUP_TOKEN")),
		WebDir:               os.Getenv("WPGO_WEB_DIR"),
		HubURL:               hubURL(),
		HubOff:               hubOff(os.Getenv("WPGO_HUB")),
		ThemeLibraryDir:      os.Getenv("WPGO_THEME_LIBRARY_DIR"),
		TrustedProxies:       os.Getenv("WPGO_TRUSTED_PROXIES"),
		ExposeUsers:          os.Getenv("WPGO_EXPOSE_USERS") == "1",
		AllowUnsignedPlugins: os.Getenv("WPGO_ALLOW_UNSIGNED_PLUGINS") == "1",
		LangDir:              os.Getenv("WPGO_LANG_DIR"),
		TranslationsAPI:      os.Getenv("WPGO_TRANSLATIONS_API"),
		SMTPHost:             os.Getenv("WPGO_SMTP_HOST"),
		SMTPPort:             env("WPGO_SMTP_PORT", "587"),
		SMTPUser:             os.Getenv("WPGO_SMTP_USER"),
		SMTPPass:             os.Getenv("WPGO_SMTP_PASS"),
		MailFrom:             os.Getenv("WPGO_MAIL_FROM"),
		MailDir:              os.Getenv("WPGO_MAIL_DIR"),
	}
	if c.HubOff {
		c.HubURL = "off" // hubBase() treats it as no Manager
	}
	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// hubOff reports whether WPGO_HUB asks to switch Pursae Manager off.
func hubOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "none", "0", "false", "no":
		return true
	}
	return false
}
