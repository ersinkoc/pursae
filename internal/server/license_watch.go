package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/mail"
)

// The daily wpgo_hub_check also re-asks the Manager about the license (licenseWatch), so an expiring,
// expired, revoked or wrong-site key is noticed without anyone opening Settings › License. The result
// is kept in the pursae_license_alert option: administrators see a notice on every admin screen and
// the site's admin address gets one e-mail per new alert. A Manager that cannot be reached changes
// nothing (the last answer stays as a grace period), so an outage never raises or clears an alert.

const (
	licenseAlertOption = "pursae_license_alert"      // "", expired, revoked, domain_mismatch, activation_limit, unknown or expiring:<RFC 3339>
	licenseSentOption  = "pursae_license_alert_sent" // the alert the e-mail went out for
	licenseWarnWindow  = 14 * 24 * time.Hour
)

// licenseAlertFor maps a license answer to an alert code ("" for none; ok false when the answer
// says nothing, i.e. the Manager was not reached).
func licenseAlertFor(l *licenseStatus, now time.Time) (alert string, ok bool) {
	switch {
	case l == nil || l.Error == "unreachable" || l.Error == "off" || l.Error == "missing":
		return "", false
	case !l.Valid:
		return l.Error, true
	}
	if l.Expires != "" {
		if t, err := time.Parse(time.RFC3339, l.Expires); err == nil && t.Sub(now) < licenseWarnWindow {
			if t.Before(now) {
				return "expired", true
			}
			return "expiring:" + l.Expires, true
		}
	}
	return "", true
}

// licenseAlertText is the sentence an alert code stands for.
func (s *Server) licenseAlertText(alert string) string {
	if rest, ok := strings.CutPrefix(alert, "expiring:"); ok {
		when := rest
		if t, err := time.Parse(time.RFC3339, rest); err == nil {
			when = t.In(s.loc()).Format("January 2, 2006")
			days := int(time.Until(t).Hours()/24) + 1
			return fmt.Sprintf("Your Pursae license expires on %s (%d days left). Renew it to keep Pro and private plugins and themes and their updates.", when, days)
		}
		return "Your Pursae license expires on " + when + "."
	}
	if t, ok := licenseErrorText[alert]; ok {
		return "Pursae license: " + t
	}
	return "Pursae license: the license key could not be validated."
}

// licenseWatch refreshes the license from the Manager and raises or clears the alert.
func (s *Server) licenseWatch(ctx context.Context) {
	if s.sandbox != nil || s.licenseKey() == "" || s.hubBase() == "" {
		if s.settings.String(licenseAlertOption) != "" && s.licenseKey() == "" {
			_ = s.settings.Set(ctx, map[string]any{licenseAlertOption: "", licenseSentOption: ""})
		}
		return
	}
	alert, ok := licenseAlertFor(s.licenseFetch(ctx), time.Now())
	if !ok {
		return
	}
	if s.settings.String(licenseAlertOption) != alert {
		if err := s.settings.Set(ctx, map[string]any{licenseAlertOption: alert}); err != nil {
			slog.Warn("license: remember alert", "err", err)
			return
		}
	}
	if alert == "" {
		if s.settings.String(licenseSentOption) != "" {
			_ = s.settings.Set(ctx, map[string]any{licenseSentOption: ""})
		}
		return
	}
	// One e-mail per alert; the expiring warning is sent once per expiry date.
	if s.settings.String(licenseSentOption) == alert {
		return
	}
	to := s.settings.String("admin_email")
	if to == "" {
		return
	}
	site := s.settings.String("blogname")
	msg := mail.Message{To: []string{to}, Subject: "[" + site + "] Your Pursae license needs attention",
		Body: s.licenseAlertText(alert) + "\n\nReview it under Settings > License: " + strings.TrimRight(s.settings.String("siteurl"), "/") + "/wp-admin/options-license\n"}
	if err := s.mailer().Send(ctx, msg); err != nil {
		slog.Warn("license: alert e-mail", "err", err)
		return
	}
	_ = s.settings.Set(ctx, map[string]any{licenseSentOption: alert})
}

// licenseNotices is the license alert for administrators on every admin screen.
func (s *Server) licenseNotices(c *gin.Context) []notice {
	alert := s.settings.String(licenseAlertOption)
	if alert == "" || s.licenseKey() == "" || !currentUser(c).Can("manage_options", nil) {
		return nil
	}
	typ := "error"
	if strings.HasPrefix(alert, "expiring:") {
		typ = "warning"
	}
	return []notice{{Type: typ, Message: template.HTML(html.EscapeString(s.licenseAlertText(alert)) + ` <a href="/wp-admin/options-license">Open License settings</a>`)}}
}
