package server

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Captcha widgets of builder forms. wbFormCaptcha (webbuilder_forms.go) demands the Turnstile or
// reCAPTCHA token whenever Pursae Contact Form has keys, so every form[data-wb-form] of a filled
// snapshot gets the matching widget here, at the end of the form. Turnstile renders a
// .cf-turnstile div and adds the cf-turnstile-response input itself; reCAPTCHA (v3, scored like
// Pursae Contact Form) gets a hidden g-recaptcha-response input that a small script fills before
// the runtime's own submit handler reads the form.

var wbFormBlockRE = regexp.MustCompile(`(?s)(<form\b[^>]*\bdata-wb-form\b[^>]*>)(.*?)(</form>)`)

// wbFormCaptchas adds the configured captcha widget (and its scripts, once) to the builder forms of h.
func (s *Server) wbFormCaptchas(c *gin.Context, h string) string {
	if !strings.Contains(h, "data-wb-form") || !s.gontactOn() {
		return h
	}
	tsSite, tsOn := "", s.settings.String("gontact_turnstile_secret") != ""
	if tsOn {
		tsSite = s.settings.String("gontact_turnstile_sitekey")
		tsOn = tsSite != ""
	}
	rcSite := ""
	if s.settings.String("gontact_recaptcha_secret") != "" {
		rcSite = s.settings.String("gontact_recaptcha_sitekey")
	}
	if !tsOn && rcSite == "" {
		return h
	}
	widgets := ""
	if tsOn {
		widgets += `<div class="cf-turnstile" data-sitekey="` + html.EscapeString(tsSite) + `"></div>`
	}
	if rcSite != "" {
		widgets += `<input type="hidden" name="g-recaptcha-response" value="" />`
	}
	found := false
	h = wbFormBlockRE.ReplaceAllStringFunc(h, func(m string) string {
		found = true
		g := wbFormBlockRE.FindStringSubmatch(m)
		return g[1] + g[2] + widgets + g[3]
	})
	if !found {
		return h
	}
	if _, done := c.Get("wb.formcaptcha"); done {
		return h
	}
	c.Set("wb.formcaptcha", true)
	var b strings.Builder
	if tsOn {
		b.WriteString(`<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" id="wb-turnstile-js" async defer></script>`)
		cspAllow(c, "script-src", "https://challenges.cloudflare.com")
		cspAllow(c, "frame-src", "https://challenges.cloudflare.com")
	}
	if rcSite != "" {
		b.WriteString(`<script src="https://www.google.com/recaptcha/api.js?render=` + html.EscapeString(rcSite) + `" id="wb-recaptcha-js" async defer></script>`)
		key, _ := json.Marshal(rcSite)
		b.WriteString(`<script id="wb-recaptcha-run">(function(k){document.addEventListener("submit",function(e){` +
			`var f=e.target;if(!f||!f.matches||!f.matches("form[data-wb-form]")||f.dataset.wbRc)return;` +
			`var i=f.querySelector("input[name=g-recaptcha-response]");if(!i||!window.grecaptcha)return;` +
			`e.preventDefault();e.stopImmediatePropagation();` +
			`var go=function(t){if(t)i.value=t;f.dataset.wbRc="1";try{f.requestSubmit()}finally{delete f.dataset.wbRc}};` +
			`grecaptcha.ready(function(){grecaptcha.execute(k,{action:"contactform"}).then(go,function(){go("")})})},true)})(` + string(key) + `);</script>`)
		cspAllow(c, "script-src", "https://www.google.com", "https://www.gstatic.com")
		cspAllow(c, "frame-src", "https://www.google.com")
		cspAllow(c, "connect-src", "https://www.google.com")
	}
	if tsOn {
		// A Turnstile token is single use: renew it after every attempt (the runtime has read the form by then).
		b.WriteString(`<script id="wb-turnstile-reset">document.addEventListener("submit",function(e){var f=e.target;` +
			`if(!f||!f.matches||!f.matches("form[data-wb-form]"))return;setTimeout(function(){var w=f.querySelector(".cf-turnstile");` +
			`if(w&&window.turnstile)try{turnstile.reset(w)}catch(x){}},1500)})</script>`)
	}
	return h + b.String()
}

// --- redirect after sending ----------------------------------------------------------------------

var wbFormActionRE = regexp.MustCompile(`/forms/(\d+)/([A-Za-z0-9_-]{1,64})`)

// wbFormEnhance adds what the stored form settings need to the filled snapshot: data-wb-redirect on
// forms whose redirect is set (the runtime sets data-wb-state="sent" and a small script follows it)
// and the captcha widgets. d is the doc the snapshot belongs to.
func (s *Server) wbFormEnhance(c *gin.Context, d *store.WebBuilderDoc, h string) string {
	if !strings.Contains(h, "data-wb-form") {
		return h
	}
	h = s.wbFormRedirects(c, d, h)
	return s.wbFormCaptchas(c, h)
}

func (s *Server) wbFormRedirects(c *gin.Context, d *store.WebBuilderDoc, h string) string {
	global := strings.TrimSpace(s.settings.String(wbFormRedirectOption)) != ""
	if d == nil || (!global && !strings.Contains(string(d.Doc), webbuilder.FormKeyRedirect)) {
		return h
	}
	var schemas map[string]webbuilder.FormSchema
	used := false
	h = wbFormTagRE.ReplaceAllStringFunc(h, func(tag string) string {
		if !strings.Contains(tag, "data-wb-form") {
			return tag
		}
		m := wbFormActionRE.FindStringSubmatch(tag)
		if m == nil {
			return tag
		}
		if schemas == nil {
			schemas = map[string]webbuilder.FormSchema{}
			var doc webbuilder.DocState
			if json.Unmarshal(d.Doc, &doc) == nil {
				for _, f := range webbuilder.FormSchemas(&doc) {
					schemas[f.SectionID] = f
				}
			}
		}
		to := s.wbFormRedirectURL(c.Request.Context(), defaultStr(schemas[m[2]].Redirect, s.settings.String(wbFormRedirectOption)))
		if to == "" {
			return tag
		}
		used = true
		return strings.TrimSuffix(tag, ">") + ` data-wb-redirect="` + html.EscapeString(to) + `">`
	})
	if used {
		h += `<script id="wb-form-redirect">new MutationObserver(function(l){l.forEach(function(r){var f=r.target;` +
			`if(f.dataset&&f.dataset.wbState==="sent"&&f.dataset.wbRedirect)setTimeout(function(){location.href=f.dataset.wbRedirect},800)})})` +
			`.observe(document.documentElement,{subtree:true,attributes:true,attributeFilter:["data-wb-state"]})</script>`
	}
	return h
}
