package server

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

// v0.2.5 membership additions: the community rules and weekly digest choices of the sign-up form,
// and "change the address" while an email address is not confirmed yet.

// forumSaveSignupPrefs stores what the sign-up form asked: when the rules were accepted and whether
// the member wants the weekly digest.
func (s *Server) forumSaveSignupPrefs(ctx context.Context, userID int64, rules, digest bool) {
	set := map[string]any{forumUMetaDigest: digest}
	if rules {
		set[forumUMetaRules] = time.Now().UTC().Format(time.RFC3339)
	}
	_ = s.forumSetUserMeta(ctx, userID, set)
}

// forumChangeEmailPost changes the address of an account that is not confirmed yet and sends a new
// confirmation link: the logged-in member (verify view), or the visitor who just registered (the
// signed change_token of the register view's check-your-inbox step). Confirmed accounts change
// their address elsewhere (the profile screen).
func (s *Server) forumChangeEmailPost(c *gin.Context) {
	ctx := c.Request.Context()
	newEmail := strings.TrimSpace(c.PostForm("user_email"))
	oldEmail := strings.TrimSpace(c.PostForm("old_email"))
	page := func() *forumPage {
		p := s.forumAuthPage(c, forumViewVerify, "")
		p.Form["user_email"] = newEmail
		if cu := currentUser(c); cu != nil && forumMetaBool(cu.Meta, forumUMetaUnverified) {
			p.Extra["change_email"] = true
		}
		return p
	}
	if !s.checkNonceValue(c, c.PostForm("_wpnonce"), forumAuthNonce) {
		s.forumAccountFail(c, page(), "verify", http.StatusForbidden, "Your session has expired. Please reload the page and try again.")
		return
	}
	if !forumAllow("change-email:"+gooikeTruncateIP(clientIP(c)), 5, time.Hour) {
		s.forumAccountFail(c, page(), "verify", http.StatusTooManyRequests, "Too many requests. Please try again later.")
		return
	}
	var uid int64
	if cu := currentUser(c); cu != nil {
		uid = cu.ID
	} else {
		uid = s.forumCheckChangeEmailToken(c.PostForm("change_token"), oldEmail, time.Now())
	}
	u, err := s.store.UserByID(ctx, uid)
	if uid <= 0 || err != nil || !forumMetaBool(u.Meta, forumUMetaUnverified) {
		s.forumAccountFail(c, page(), "verify", http.StatusForbidden, "Your address can no longer be changed here.")
		return
	}
	if a, err := mail.ParseAddress(newEmail); err != nil || a.Address != newEmail || len(newEmail) > 100 {
		s.forumAccountFail(c, page(), "verify", http.StatusUnprocessableEntity, "Please enter a valid email address.")
		return
	}
	if !strings.EqualFold(newEmail, u.Email) {
		if _, taken, err := s.store.LoginOrEmailTaken(ctx, newEmail, newEmail); err != nil || taken {
			s.forumAccountFail(c, page(), "verify", http.StatusUnprocessableEntity, "An account with this email already exists. Log in instead.")
			return
		}
		if _, err := s.store.Pool.Exec(ctx, `UPDATE users SET email = $2 WHERE id = $1`, u.ID, newEmail); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				s.forumAccountFail(c, page(), "verify", http.StatusUnprocessableEntity, "An account with this email already exists. Log in instead.")
				return
			}
			s.forumAccountFail(c, page(), "verify", http.StatusInternalServerError, "Something went wrong. Please try again.")
			return
		}
		u.Email = newEmail
	}
	if err := s.forumSendVerify(ctx, u); err != nil {
		s.forumAccountFail(c, page(), "verify", http.StatusInternalServerError, "Something went wrong. Please try again.")
		return
	}
	c.Redirect(http.StatusFound, s.forumURL(ctx, "verify")+"?forum_msg=email_changed")
}
