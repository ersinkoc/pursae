package server

import (
	"context"
	"encoding/json"
	"html"
	"html/template"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Pursae update e-mails. Pursae Manager keeps the e-mail addresses of a site's admin-side users
// (anyone who can edit_posts) who asked for Pursae product updates; the Manager shows them on the
// site's page. It is off by default and on only by the user's own choice, with the time recorded:
// the unticked box on the install screen (the owner) or the "Pursae updates" switch in the user's
// own profile (Users › Profile). Commercial e-mail needs prior consent (the Turkish e-commerce law
// 6563, KVKK, GDPR/ePrivacy), so nothing else turns it on: not the one-time admin notice, which
// only invites the user to opt in, and not an administrator editing someone else's profile.
// WordPress core has no such feature. Turning it off removes the address from the Manager on the
// next ping.
//
// Users who never open the admin (subscribers, shop customers) are never asked nor reported. The
// addresses travel in the daily ping as "contacts" (docs/developer/hub/index.md).

// updateEmailsMeta is the per-user switch in users.meta: true = send, false = do not send, absent
// = the user has not been asked yet (not sent).
const updateEmailsMeta = "pursae_update_emails"

// updateEmailsConsentMeta is when the user opted in (RFC 3339, UTC): the record of consent. A true
// switch without it is not consent (sites before opt-in stored true by default) and is not reported.
const updateEmailsConsentMeta = "pursae_update_emails_consent_at"

// hubContactsLimit caps the addresses one ping carries.
const hubContactsLimit = 50

type hubContact struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// updateEmailsEligible reports whether a user is asked about update e-mails (works in the admin).
func updateEmailsEligible(u *store.User) bool { return u != nil && u.Can("edit_posts", nil) }

// updateEmailsOn reports whether the user opted in: the switch is on and the time of consent is recorded.
func updateEmailsOn(u *store.User) bool {
	v, ok := u.Meta[updateEmailsMeta].(bool)
	at, _ := u.Meta[updateEmailsConsentMeta].(string)
	return ok && v && at != ""
}

// updateEmailsAsked reports whether the user has made a choice or seen the opt-in notice. A true
// switch without a consent time is the old opt-out default: that user is asked again.
func updateEmailsAsked(u *store.User) bool {
	v, ok := u.Meta[updateEmailsMeta].(bool)
	return ok && (!v || updateEmailsOn(u))
}

// setUpdateEmails stores a user's choice: on records the time of consent (kept when already on),
// off removes it.
func (s *Server) setUpdateEmails(ctx context.Context, u *store.User, on bool) error {
	if u.Meta == nil {
		u.Meta = map[string]any{}
	}
	if on {
		at, _ := u.Meta[updateEmailsConsentMeta].(string)
		if at == "" {
			at = time.Now().UTC().Format(time.RFC3339)
		}
		raw, _ := json.Marshal(map[string]any{updateEmailsMeta: true, updateEmailsConsentMeta: at})
		if _, err := s.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || $2::jsonb WHERE id = $1`, u.ID, raw); err != nil {
			return err
		}
		u.Meta[updateEmailsMeta], u.Meta[updateEmailsConsentMeta] = true, at
		return nil
	}
	raw, _ := json.Marshal(map[string]any{updateEmailsMeta: false})
	if _, err := s.store.Pool.Exec(ctx, `UPDATE users SET meta = (meta - $3::text) || $2::jsonb WHERE id = $1`,
		u.ID, raw, updateEmailsConsentMeta); err != nil {
		return err
	}
	u.Meta[updateEmailsMeta] = false
	delete(u.Meta, updateEmailsConsentMeta)
	return nil
}

// hubContacts lists the addresses the ping reports: eligible users who opted in.
func (s *Server) hubContacts(ctx context.Context) []hubContact {
	out := []hubContact{}
	users, err := s.store.Authors(ctx) // users with edit_posts through a role
	if err != nil {
		slog.Warn("hub: contacts", "err", err)
		return out
	}
	for _, u := range users {
		if len(out) == hubContactsLimit {
			break
		}
		if !updateEmailsOn(u) || u.Email == "" {
			continue
		}
		var role string
		var r []string
		if err := s.store.Pool.QueryRow(ctx, `SELECT coalesce(array_agg(r.slug ORDER BY ur.is_primary DESC, r.id), '{}')
			FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = $1`, u.ID).Scan(&r); err == nil && len(r) > 0 {
			role = r[0]
		}
		out = append(out, hubContact{Email: u.Email, Name: u.DisplayName, Role: role})
	}
	return out
}

// updateEmailsNotices invites an eligible user, once, to opt in to update e-mails in their profile.
// It records false (asked, not opted in) and never turns the e-mails on.
func (s *Server) updateEmailsNotices(c *gin.Context) []notice {
	u := currentUser(c)
	if s.sandbox != nil || s.hubBase() == "" || !updateEmailsEligible(u) || updateEmailsAsked(u) {
		return nil
	}
	if err := s.setUpdateEmails(c, u, false); err != nil {
		return nil // try again on the next screen
	}
	return []notice{{Type: "info", Dismissible: true, Message: template.HTML(updateEmailsNoticeText(u.Email) +
		` <a href="/wp-admin/profile#pursae-update-emails">Turn them on in your profile</a>`)}}
}

// updateEmailsNoticeText is the opt-in invitation the user is shown.
func updateEmailsNoticeText(email string) string {
	return "Want Pursae product update e-mails at <code>" + html.EscapeString(email) + "</code>? " +
		"They stay off until you turn them on."
}
