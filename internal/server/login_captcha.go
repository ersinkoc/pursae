package server

import (
	"html"
	"html/template"
	"strings"

	"github.com/gin-gonic/gin"
)

// loginCaptcha is the CAPTCHA of the login, registration and lost-password forms (Pursae Security >
// Login form). It reuses the keys saved in Pursae Contact Form (Turnstile first, then reCAPTCHA) and
// the Contact Form verifier, so there is one place to configure it.
type loginCaptcha struct{ kind, site, secret, endpoint, field string }

// loginCaptchaCfg returns the active CAPTCHA, or ok=false when the option is off or no keys exist.
func (s *Server) loginCaptchaCfg() (cfg loginCaptcha, ok bool) {
	if !s.adminsecOn() || !s.asLoad().CaptchaForms {
		return cfg, false
	}
	if site, secret := s.settings.String("gontact_turnstile_sitekey"), s.settings.String("gontact_turnstile_secret"); site != "" && secret != "" {
		return loginCaptcha{"turnstile", site, secret, "https://challenges.cloudflare.com/turnstile/v0/siteverify", "cf-turnstile-response"}, true
	}
	if site, secret := s.settings.String("gontact_recaptcha_sitekey"), s.settings.String("gontact_recaptcha_secret"); site != "" && secret != "" {
		return loginCaptcha{"recaptcha", site, secret, "https://www.google.com/recaptcha/api/siteverify", "g-recaptcha-response"}, true
	}
	return cfg, false
}

// loginCaptchaWidget is the markup placed before the submit button ("" when no CAPTCHA is active).
func (s *Server) loginCaptchaWidget() template.HTML {
	cfg, ok := s.loginCaptchaCfg()
	if !ok {
		return ""
	}
	site := html.EscapeString(cfg.site)
	if cfg.kind == "turnstile" {
		return template.HTML(`<div class="cf-turnstile" data-sitekey="` + site + `"></div>` +
			`<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>`)
	}
	return template.HTML(`<input type="hidden" name="g-recaptcha-response" value="" />` +
		`<script src="https://www.google.com/recaptcha/api.js?render=` + site + `"></script>` +
		`<script>(function(){var f=document.currentScript.closest('form');if(!f)return;var done=false;f.addEventListener('submit',function(e){if(done||!window.grecaptcha)return;e.preventDefault();grecaptcha.ready(function(){grecaptcha.execute('` + site + `',{action:'login'}).then(function(t){f.elements['g-recaptcha-response'].value=t;done=true;f.submit();});});});})();</script>`)
}

// loginCaptchaFailed verifies the CAPTCHA of a POSTed form. It returns the error to show, or "" when
// there is nothing to check or the answer is valid.
func (s *Server) loginCaptchaFailed(c *gin.Context) template.HTML {
	cfg, ok := s.loginCaptchaCfg()
	if !ok {
		return ""
	}
	token := strings.TrimSpace(c.PostForm(cfg.field))
	if token == "" {
		return "<strong>Error:</strong> Please complete the CAPTCHA."
	}
	score, good := s.gontactVerifyCaptcha(c.Request.Context(), cfg.endpoint, cfg.secret, token, clientIP(c))
	if !good || score < 0.5 {
		return "<strong>Error:</strong> The CAPTCHA could not be verified. Please try again."
	}
	return ""
}
