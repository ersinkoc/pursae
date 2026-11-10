package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
	"wpgo/internal/mail"
	"wpgo/internal/store"
)

// lostPassword is wp-login.php?action=lostpassword (retrieve_password()).
func (s *Server) lostPassword(c *gin.Context, v loginView) {
	v.Title, v.Action = "Lost Password", "lostpassword"
	if c.Request.Method == http.MethodPost {
		login := strings.TrimSpace(c.PostForm("user_login"))
		if login == "" {
			v.Error = "<strong>Error:</strong> Please enter a username or email address."
		} else {
			if msg := s.loginCaptchaFailed(c); msg != "" {
				v.Error = msg
				s.renderAdminTemplate(c, http.StatusOK, "admin/login-lost", v)
				return
			}
			if u, err := s.store.UserByLoginOrEmail(c, login); err == nil {
				if s.resetRequestAllowed(c, u) {
					s.asAudit(c, asEvent{Event: "password_reset_requested", Severity: "notice", User: u, Username: u.Login, Message: "A password reset link was requested."})
					s.sendResetLink(c, u)
				}
			}
			// The same answer whether or not the account exists, so the form can't be used to find accounts.
			c.Redirect(http.StatusFound, s.loginPath()+"?checkemail=confirm")
			return
		}
	}
	s.renderAdminTemplate(c, http.StatusOK, "admin/login-lost", v)
}

// resetLink is get_password_reset_key() + the wp-login.php?action=rp URL: a fresh key (hashed in user
// meta, valid for a day) and the link that carries it.
func (s *Server) resetLink(c *gin.Context, u *store.User) string {
	key := auth.RandomToken(20)
	sum := sha256.Sum256([]byte(key))
	_ = s.setUserMeta(c, u, "password_reset", map[string]any{"hash": hex.EncodeToString(sum[:]), "expires": time.Now().Add(24 * time.Hour).Unix()})
	return s.links(c).HomeURL(s.loginPath() + "?action=rp&key=" + key + "&login=" + url.QueryEscape(u.Login))
}

// sendResetLink is retrieve_password(): the "[Site] Password Reset" message.
func (s *Server) sendResetLink(c *gin.Context, u *store.User) {
	link := s.resetLink(c, u)
	// The store's own "Reset password" e-mail (Settings > Emails) replaces the core message when it is enabled.
	if s.gooOn() {
		if sent, err := s.gooSendEmail(c, "customer_reset_password", nil, map[string]any{"user_id": u.ID, "login": u.Login, "to": u.Email, "reset_url": link}); err == nil && sent {
			return
		}
	}
	site := s.settings.String("blogname")
	body := fmt.Sprintf("Someone has requested a password reset for the following account:\n\nSite Name: %s\n\nUsername: %s\n\nIf this was a mistake, ignore this email and nothing will happen.\n\nTo reset your password, visit the following address:\n\n%s\n\n",
		site, u.Login, link)
	if currentUser(c) == nil {
		body += fmt.Sprintf("This password reset request originated from the IP address %s.\n", clientIP(c))
	}
	s.sendMail(c, mail.Message{Kind: "password_reset", To: []string{u.Email}, Subject: "[" + site + "] Password Reset", Body: body})
}

// register is wp-login.php?action=register (register_new_user()).
func (s *Server) register(c *gin.Context, v loginView) {
	if !v.CanRegister {
		c.Redirect(http.StatusFound, s.loginPath()+"?registration=disabled")
		return
	}
	v.Title, v.Action = "Registration Form", "register"
	v.Message = `<span class="message register">Register For This Site</span>`
	if c.Request.Method == http.MethodPost {
		login := strings.TrimSpace(c.PostForm("user_login"))
		email := strings.TrimSpace(c.PostForm("user_email"))
		v.Login, v.Email, v.RedirectTo = login, email, c.PostForm("redirect_to")
		var errs []string
		if msg := s.loginCaptchaFailed(c); msg != "" {
			errs = append(errs, string(msg))
		}
		lt, et, _ := s.store.LoginOrEmailTaken(c, login, email)
		switch {
		case login == "":
			errs = append(errs, "<strong>Error:</strong> Please enter a username.")
		case !validUsername.MatchString(login):
			errs = append(errs, "<strong>Error:</strong> This username is invalid because it uses illegal characters. Please enter a valid username.")
		case lt:
			errs = append(errs, "<strong>Error:</strong> This username is already registered. Please choose another one.")
		}
		switch {
		case email == "":
			errs = append(errs, "<strong>Error:</strong> Please type your email address.")
		case !validEmail(email):
			errs = append(errs, "<strong>Error:</strong> The email address isn&#8217;t correct.")
		case et:
			errs = append(errs, "<strong>Error:</strong> This email address is already registered. <a href=\""+s.loginPath()+"\">Log in</a> with this address or choose another one.")
		}
		if len(errs) == 0 {
			// The password is random; the user sets their own from the link in the notification.
			hash, err := auth.HashPassword(auth.RandomToken(24))
			var id int64
			if err == nil {
				id, err = s.store.CreateUser(c, store.NewUser{Login: login, Email: email, PasswordHash: hash, Role: s.settings.String("default_role")})
			}
			if err != nil {
				errs = append(errs, "<strong>Error:</strong> Could not register you&hellip; please contact the site administrator!")
			} else {
				if nu, e := s.store.UserByID(c, id); e == nil {
					s.notifyNewUser(c, nu, true)
				}
				target := s.loginPath() + "?checkemail=registered"
				if rt := safeRedirect(v.RedirectTo, ""); rt != "" {
					target += "&redirect_to=" + url.QueryEscape(rt)
				}
				c.Redirect(http.StatusFound, target)
				return
			}
		}
		v.Error = template.HTML(strings.Join(errs, "<br />\n"))
	}
	s.renderAdminTemplate(c, http.StatusOK, "admin/login-register", v)
}

// validEmail is is_email(): one @, a dot in the domain, no spaces.
func validEmail(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 || strings.ContainsAny(email, " \t\r\n") || strings.Count(email, "@") != 1 {
		return false
	}
	return strings.Contains(email[at+1:], ".")
}

// checkResetKey is check_password_reset_key().
func (s *Server) checkResetKey(ctx context.Context, login, key string) (*store.User, bool) {
	u, err := s.store.UserByLoginOrEmail(ctx, login)
	if err != nil || key == "" {
		return nil, false
	}
	rec, _ := u.Meta["password_reset"].(map[string]any)
	hash, _ := rec["hash"].(string)
	exp, _ := rec["expires"].(float64)
	sum := sha256.Sum256([]byte(key))
	if hash == "" || time.Now().Unix() > int64(exp) || subtle.ConstantTimeCompare([]byte(hash), []byte(hex.EncodeToString(sum[:]))) != 1 {
		return nil, false
	}
	return u, true
}

// resetPassword is wp-login.php?action=rp / resetpass.
func (s *Server) resetPassword(c *gin.Context, v loginView) {
	v.Title, v.Action = "Reset Password", "resetpass"
	login, key := defaultStr(c.PostForm("rp_login"), c.Query("login")), defaultStr(c.PostForm("rp_key"), c.Query("key"))
	u, ok := s.checkResetKey(c, login, key)
	if !ok {
		c.Redirect(http.StatusFound, s.loginPath()+"?action=lostpassword&error=invalidkey")
		return
	}
	v.Login, v.ResetKey = u.Login, key
	if c.Request.Method == http.MethodPost {
		p1, p2 := c.PostForm("pass1"), c.PostForm("pass2")
		switch {
		case p1 == "":
			v.Error = "<strong>Error:</strong> Please enter a new password."
		case p1 != p2:
			v.Error = "<strong>Error:</strong> The passwords do not match."
		case s.adminsecUserPasswordProblem(u, p1) != "": // AdminSecGo password policy
			v.Error = template.HTML("<strong>Error:</strong> " + template.HTMLEscapeString(s.adminsecUserPasswordProblem(u, p1)))
		default:
			hash, err := auth.HashPassword(p1)
			if err != nil {
				v.Error = template.HTML("<strong>Error:</strong> " + template.HTMLEscapeString(err.Error()))
				break
			}
			// Spend the key first and atomically: two requests with the same key can't both reset.
			sum := sha256.Sum256([]byte(key))
			if ok, err := s.store.ConsumePasswordReset(c, u.ID, hex.EncodeToString(sum[:])); err != nil || !ok {
				c.Redirect(http.StatusFound, s.loginPath()+"?action=lostpassword&error=invalidkey")
				return
			}
			if err := s.store.SetUserPassword(c, u.ID, hash); err != nil {
				v.Error = template.HTML("<strong>Error:</strong> " + template.HTMLEscapeString(err.Error()))
				break
			}
			delete(u.Meta, "password_reset")
			s.adminsecPasswordChanged(c, u)
			_, _ = s.store.Pool.Exec(c, `DELETE FROM sessions WHERE user_id = $1`, u.ID)
			site := s.settings.String("blogname")
			s.sendMail(c, mail.Message{Kind: "password_changed", To: []string{s.settings.String("admin_email")}, Subject: "[" + site + "] Password Changed",
				Body: fmt.Sprintf("Password changed for user: %s\n", u.Login)})
			c.Redirect(http.StatusFound, s.loginPath()+"?password=changed")
			return
		}
	}
	s.renderAdminTemplate(c, http.StatusOK, "admin/login-reset", v)
}

// notifyComment is wp_new_comment_notify_moderator() / wp_new_comment_notify_postauthor().
func (s *Server) notifyComment(c *gin.Context, cm *store.Comment) {
	p, err := s.store.GetPost(c, cm.PostID)
	if err != nil {
		return
	}
	site := s.settings.String("blogname")
	link := s.links(c).Permalink(p)
	text := fmt.Sprintf("Author: %s (IP address: %s)\nEmail: %s\nURL: %s\nComment:\n%s\n", cm.AuthorName, cm.AuthorIP, cm.AuthorEmail, cm.AuthorURL, cm.Content)
	admin := s.links(c).HomeURL("/wp-admin/edit-comments?comment_status=")
	switch cm.Status {
	case "pending":
		if !s.settings.Bool("moderation_notify") {
			return
		}
		body := fmt.Sprintf("A new comment on the post \"%s\" is waiting for your approval\n%s\n\n%s\nApprove it: %s\nTrash it: %s\nSpam it: %s\n\nCurrently 1 comment is waiting for approval. Please visit the moderation panel:\n%s\n",
			p.Title, link, text, admin+"pending", admin+"pending", admin+"pending", admin+"moderated")
		s.sendMail(c, mail.Message{Kind: "comment_moderation", To: []string{s.settings.String("admin_email")}, Subject: fmt.Sprintf("[%s] Please moderate: \"%s\"", site, p.Title), Body: body})
	case "approved":
		if !s.settings.Bool("comments_notify") || cm.UserID == p.AuthorID {
			return
		}
		author, err := s.store.UserByID(c, p.AuthorID)
		if err != nil || author.Email == "" {
			return
		}
		body := fmt.Sprintf("New comment on your post \"%s\"\n%s\nYou can see all comments on this post here:\n%s#comments\n\nPermalink: %s#comment-%d\n", p.Title, text, link, link, cm.ID)
		s.sendMail(c, mail.Message{Kind: "comment_published", To: []string{author.Email}, Subject: fmt.Sprintf("[%s] Comment: \"%s\"", site, p.Title), Body: body, ReplyTo: cm.AuthorEmail})
	}
}

// notifyNewUser is wp_new_user_notification(): the admin is told, and the user gets "[Site] Login Details"
// with a link to set a password.
func (s *Server) notifyNewUser(c *gin.Context, u *store.User, sendUser bool) {
	site := s.settings.String("blogname")
	s.sendMail(c, mail.Message{Kind: "new_user_admin", To: []string{s.settings.String("admin_email")}, Subject: "[" + site + "] New User Registration",
		Body: fmt.Sprintf("New user registration on your site %s:\n\nUsername: %s\n\nEmail: %s\n", site, u.Login, u.Email)})
	if sendUser {
		link := s.resetLink(c, u)
		body := fmt.Sprintf("Username: %s\n\nTo set your password, visit the following address:\n\n%s\n\n%s\n", u.Login, link, s.links(c).HomeURL(s.loginPath()))
		s.sendMail(c, mail.Message{Kind: "new_user_user", To: []string{u.Email}, Subject: "[" + site + "] Login Details", Body: body})
	}
}

// Password reset requests are limited per IP address and per account so the form cannot be used to
// flood a mailbox (every request mails the owner and replaces the previous key). The answer shown to
// the visitor stays the same whether or not a request was throttled or the account exists.
const (
	resetRequestMax    = 3
	resetRequestWindow = 15 * time.Minute
)

var (
	resetReqMu   sync.Mutex
	resetReqSeen = map[string][]time.Time{}
)

// resetRequestAllowed reports whether a reset mail may be sent for u from this request, and records it.
func (s *Server) resetRequestAllowed(c *gin.Context, u *store.User) bool {
	ip := clientIP(c)
	if st := s.asLoad(); s.adminsecOn() && st.BlockList != "" && maintIPAllowed(st.BlockList, ip) && !maintIPAllowed(st.AllowList, ip) {
		return false
	}
	now := time.Now()
	resetReqMu.Lock()
	defer resetReqMu.Unlock()
	for k, v := range resetReqSeen { // drop expired entries
		for len(v) > 0 && now.Sub(v[0]) > resetRequestWindow {
			v = v[1:]
		}
		if len(v) == 0 {
			delete(resetReqSeen, k)
		} else {
			resetReqSeen[k] = v
		}
	}
	keys := []string{"ip:" + ip, "user:" + strconv.FormatInt(u.ID, 10)}
	for _, k := range keys {
		if len(resetReqSeen[k]) >= resetRequestMax {
			return false
		}
	}
	for _, k := range keys {
		resetReqSeen[k] = append(resetReqSeen[k], now)
	}
	return true
}
