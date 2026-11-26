package server

import (
	"html/template"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
)

// The CAPTCHA of the account forms (Pursae Security > Login form): /wp-login, registration, lost
// password, and the login and register forms of Pursae Commerce's My Account page. Keys, widget
// and verifier are the shared ones of captcha.go.

// loginCaptchaCfg returns the active CAPTCHA, or ok=false when the option is off or no keys exist.
func (s *Server) loginCaptchaCfg() (captchaProvider, bool) {
	if !s.adminsecOn() || !s.asLoad().CaptchaForms {
		return captchaProvider{}, false
	}
	return s.captchaProvider()
}

// loginCaptchaWidget is the markup placed before the submit button ("" when no CAPTCHA is active).
func (s *Server) loginCaptchaWidget(c *gin.Context) template.HTML {
	cfg, ok := s.loginCaptchaCfg()
	if !ok {
		return ""
	}
	return captchaWidget(c, cfg, "login")
}

// loginCaptchaFailed verifies the CAPTCHA of a POSTed form. It returns the error to show, or "" when
// there is nothing to check or the answer is valid.
func (s *Server) loginCaptchaFailed(c *gin.Context) template.HTML {
	cfg, ok := s.loginCaptchaCfg()
	if !ok {
		return ""
	}
	if msg := s.captchaCheck(c, cfg); msg != "" {
		return template.HTML("<strong>Error:</strong> " + msg)
	}
	return ""
}

// loginPressureCaptcha asks for the CAPTCHA on a password sign-in at an account under a distributed
// attack (accountUnderAttack) when CAPTCHA keys are configured but the login form does not ask for one
// already. It returns the widget to show ("" when nothing is asked) and the error when the answer is
// missing or wrong ("" when it is valid). Guesses from many networks then each need a solved CAPTCHA,
// while the owner signs in as usual after solving one.
func (s *Server) loginPressureCaptcha(c *gin.Context, login string) (widget, msg template.HTML) {
	if _, always := s.loginCaptchaCfg(); always {
		return "", "" // asked and checked on every sign-in (loginCaptchaFailed)
	}
	p, ok := s.captchaProvider()
	if !ok || !s.accountUnderAttack(c, login) {
		return "", ""
	}
	widget = captchaWidget(c, p, "login")
	if m := s.captchaCheck(c, p); m != "" {
		return widget, template.HTML("<strong>Error:</strong> There were many failed sign-ins to this account. " + template.HTMLEscapeString(m))
	}
	return widget, ""
}

// accountUnderAttack reports whether the account login names has as many recent failed sign-ins from
// every network as make it "under attack" (auth.MaxFailedLoginsPerLogin, or Pursae Security's
// user_max_failures within its fail_window), seen from a network that has not signed in to it recently.
// A device Pursae Security remembers for the account, and an allow-listed address, are never asked.
func (s *Server) accountUnderAttack(c *gin.Context, login string) bool {
	u, _ := s.store.UserByLoginOrEmail(c, login)
	name := s.asNormLogin(c, login, u)
	if name == "" {
		return false
	}
	ip := clientIP(c)
	window, threshold := auth.FailedWindow, auth.MaxFailedLoginsPerLogin
	secOn := s.adminsecOn()
	if secOn {
		st := s.asLoad()
		if st.UserMaxFailures <= 0 || maintIPAllowed(st.AllowList, ip) {
			return false
		}
		window, threshold = time.Duration(max1(st.FailWindow))*time.Minute, st.UserMaxFailures
	}
	p, err := s.auth.LoginPressure(c, ip, name, window, 0)
	if err != nil || p.KnownNet || p.Failures < threshold {
		return false
	}
	return !secOn || u == nil || !s.asRememberedDevice(c, u)
}
