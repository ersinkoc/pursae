package server

import (
	"context"
	"log/slog"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// checkUserPassword verifies a user's own (login) password against the stored hash, whatever format
// it is in: Pursae's argon2id or a hash brought over from WordPress (phpass "$P$", 6.8+ "$wp$2y$",
// plain bcrypt, unsalted MD5; see auth.CheckUserPassword). After a match on a legacy hash the
// password is rehashed to argon2id and stored, like wp_authenticate_username_password() does when
// wp_password_needs_rehash() says so, and u.PasswordHash is updated in place so tokens derived from
// the hash in this request (Headless Go's pw tag) match the stored value.
//
// Every place that checks the account password goes through here: wp-login.php, the forum, store
// and Headless Go sign-ins and the "current password" confirmations. Application Passwords are not
// account passwords and keep their own argon2id-only check (app_password_auth.go).
func (s *Server) checkUserPassword(ctx context.Context, u *store.User, pass string) bool {
	if u == nil {
		return false
	}
	ok, rehash, err := auth.CheckUserPassword(ctx, pass, u.PasswordHash)
	if err != nil || !ok {
		return false
	}
	if rehash {
		s.upgradeUserPasswordHash(ctx, u, pass)
	}
	return true
}

// upgradeUserPasswordHash stores an argon2id hash of pass in place of u's legacy hash. A failure is
// logged and otherwise ignored: the sign-in already succeeded and the next one tries again.
func (s *Server) upgradeUserPasswordHash(ctx context.Context, u *store.User, pass string) {
	from := auth.HashKind(u.PasswordHash)
	hash, err := auth.HashPassword(pass)
	if err != nil {
		slog.Warn("password rehash", "user_id", u.ID, "err", err)
		return
	}
	// The sign-in has been decided: finish the write even if the client goes away now.
	updated, err := s.store.UpgradeUserPasswordHash(context.WithoutCancel(ctx), u.ID, u.PasswordHash, hash)
	if err != nil {
		slog.Warn("password rehash", "user_id", u.ID, "err", err)
		return
	}
	if !updated { // the password changed meanwhile: keep the newer hash
		return
	}
	u.PasswordHash = hash
	slog.Info("password rehashed to argon2id", "user_id", u.ID, "from", from)
}
