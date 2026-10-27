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

	// Outgoing mail. Without SMTPHost, messages are written as .eml files to MailDir (development).
	SMTPHost, SMTPUser, SMTPPass, MailFrom string
	SMTPPort                               string
	MailDir                                string
}

func Load() Config {
	c := Config{
		Addr:        env("WPGO_ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", "postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable"),
		SiteURL:     strings.TrimRight(env("WPGO_SITE_URL", "http://localhost:8080"), "/"),
		UploadsDir:  env("WPGO_UPLOADS_DIR", "./data/uploads"),
		Secret:      os.Getenv("WPGO_SECRET"),
		WebDir:      os.Getenv("WPGO_WEB_DIR"),
		HubURL:      strings.TrimRight(env("WPGO_HUB_URL", "https://hub.wordpressgo.dev"), "/"),
		SMTPHost:    os.Getenv("WPGO_SMTP_HOST"),
		SMTPPort:    env("WPGO_SMTP_PORT", "587"),
		SMTPUser:    os.Getenv("WPGO_SMTP_USER"),
		SMTPPass:    os.Getenv("WPGO_SMTP_PASS"),
		MailFrom:    os.Getenv("WPGO_MAIL_FROM"),
		MailDir:     os.Getenv("WPGO_MAIL_DIR"),
	}
	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
