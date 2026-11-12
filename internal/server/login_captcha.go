package server

import (
	"html/template"

	"github.com/gin-gonic/gin"
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
