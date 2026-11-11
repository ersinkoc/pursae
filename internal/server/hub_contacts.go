package server

import (
	"context"
	"encoding/json"
	"html"
	"html/template"
	"log/slog"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Pursae update e-mails. Pursae Manager keeps the e-mail addresses of a site's admin-side users
// (anyone who can edit_posts) so Pursae can e-mail them product updates; the Manager shows them on
// the site's page. It is on by default and every user can turn it off under Users › Profile
// ("Pursae updates"), which removes the address from the Manager on the next ping.
//
// A user is only reported after being told: the installer sees the notice on the install screen,
// every other eligible user sees a one-time admin notice on their first admin screen (which also
// records the default, on). Users who never open the admin (subscribers, shop customers) are never
// reported. The addresses travel in the daily ping as "contacts" (pursae-manager/API.md).

// updateEmailsMeta is the per-user switch in users.meta: true = send, false = do not send, absent
// = the user has not been told yet (not sent).
const updateEmailsMeta = "pursae_update_emails"

// hubContactsLimit caps the addresses one ping carries.
const hubContactsLimit = 50

type hubContact struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// updateEmailsEligible reports whether a user is asked about update e-mails (works in the admin).
func updateEmailsEligible(u *store.User) bool { return u != nil && u.Can("edit_posts", nil) }

// updateEmailsOn reports a user's switch (false while the user has not been told).
func updateEmailsOn(u *store.User) bool {
	v, ok := u.Meta[updateEmailsMeta].(bool)
	return ok && v
}

// hubContacts lists the addresses the ping reports: eligible users whose switch is on.
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

// updateEmailsNotices tells an eligible user, once, that their address is kept for update e-mails
// and records the default (on). The next ping reports the address.
func (s *Server) updateEmailsNotices(c *gin.Context) []notice {
	u := currentUser(c)
	if s.sandbox != nil || s.hubBase() == "" || !updateEmailsEligible(u) {
		return nil
	}
	if _, told := u.Meta[updateEmailsMeta]; told {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{updateEmailsMeta: true})
	if _, err := s.store.Pool.Exec(c, `UPDATE users SET meta = meta || $2::jsonb WHERE id = $1`, u.ID, raw); err != nil {
		return nil // try again on the next screen
	}
	u.Meta[updateEmailsMeta] = true
	return []notice{{Type: "info", Dismissible: true, Message: template.HTML(updateEmailsNoticeText(u.Email) +
		` <a href="/wp-admin/profile#pursae-update-emails">Change this in your profile</a>`)}}
}

// updateEmailsNoticeText is the notice the user is shown (admin notice, install screen).
func updateEmailsNoticeText(email string) string {
	return "Pursae keeps your email address (<code>" + html.EscapeString(email) + "</code>) to send you Pursae product updates. " +
		"You can turn this off any time in your user profile."
}
