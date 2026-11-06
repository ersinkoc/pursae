package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// Gooike on the site. Everything printed here is the same for every visitor, so pages stay
// cacheable: the consent state is read in the browser.
//
//   - wp_head: the Consent Mode v2 defaults (and the stored choice, as an update, before any tag can
//     run), the stylesheet with the design's custom properties, and the head scripts, inert;
//   - wp_body_open: the "body open" scripts, inert;
//   - wp_footer: the banner, the preferences modal, the floating button, the footer scripts and
//     gooike.js with its configuration.

// gooikeFront reports whether Gooike prints anything on this request.
func (s *Server) gooikeFront(c *gin.Context) bool {
	return c != nil && s.gooikeOn()
}

// gooikeHead is Gooike's part of wp_head, printed first so the consent defaults precede every tag.
func (s *Server) gooikeHead(c *gin.Context) string {
	if !s.gooikeFront(c) {
		return ""
	}
	st := s.gooikeLoad()
	for dir, srcs := range gooikeCSPSources(&st) { // the hosts the enabled scripts load from
		cspAllow(c, dir, srcs...)
	}
	var b strings.Builder
	if st.ConsentMode {
		b.WriteString(gooikeConsentModeScript(&st))
	}
	b.WriteString(`<link rel="stylesheet" id="gooike-css" href="` + gooikeAssets + `/gooike.css?ver=` + gooikeVersion + `" media="all" />` + "\n")
	b.WriteString(`<style id="gooike-inline-css">` + gooikeCSSVars(st.Design) + gooikeCustomCSS(st.Design) + "</style>\n")
	b.WriteString(gooikeScriptsAt(&st, "head"))
	return b.String()
}

func gooikeCustomCSS(d gooikeDesign) string {
	if strings.TrimSpace(d.CustomCSS) == "" {
		return ""
	}
	return "\n/* Pursae Cookie custom CSS */\n" + d.CustomCSS
}

// gooikeBodyOpen is Gooike's part of wp_body_open.
func (s *Server) gooikeBodyOpen(c *gin.Context) string {
	if !s.gooikeFront(c) {
		return ""
	}
	st := s.gooikeLoad()
	return gooikeScriptsAt(&st, "body")
}

// gooikeFooter is Gooike's part of wp_footer.
func (s *Server) gooikeFooter(c *gin.Context) string {
	if !s.gooikeFront(c) {
		return ""
	}
	st := s.gooikeLoad()
	var b strings.Builder
	b.WriteString(s.gooikeMarkup(c, &st, false))
	b.WriteString(gooikeScriptsAt(&st, "footer"))
	b.WriteString(gooikeConfigScript(&st, false))
	b.WriteString(`<script src="` + gooikeAssets + `/gooike.js?ver=` + gooikeVersion + `" id="gooike-js" defer></script>` + "\n")
	return b.String()
}

// gooikeScriptsAt prints the enabled scripts of a placement: necessary ones as they are, the
// others inert until their category is accepted.
func gooikeScriptsAt(st *gooikeSettings, placement string) string {
	var b strings.Builder
	for _, sc := range st.Scripts {
		if !sc.Enabled || sc.Placement != placement {
			continue
		}
		code := gooikeInert(sc.code(), sc.Category)
		if code == "" {
			continue
		}
		fmt.Fprintf(&b, "<!-- Pursae Cookie: %s (%s) -->\n%s\n", html.EscapeString(strings.ReplaceAll(sc.Name, "--", "-")), html.EscapeString(sc.Category), code)
	}
	return b.String()
}

// gooikeSignalMap maps each category to its Consent Mode types.
func gooikeSignalMap(st *gooikeSettings) map[string][]string {
	m := map[string][]string{}
	for _, c := range st.visibleCategories() {
		m[c.Key] = append([]string{}, c.Signals...)
	}
	return m
}

// gooikeConsentModeScript sets gtag's consent defaults: denied (granted for necessary types) under
// an opt-in regulation, granted under CCPA unless the browser sends Global Privacy Control. A
// stored, current choice follows at once as an update, so tags never run on the defaults alone.
func gooikeConsentModeScript(st *gooikeSettings) string {
	cfg := map[string]any{
		"v": st.PolicyVersion, "cookie": gooikeCookie, "optout": !st.optIn(), "gpc": st.RespectGPC,
		"all": gooikeSignals, "base": gooikeSignalMap(st)["necessary"], "map": gooikeSignalMap(st),
		"wait": st.WaitForUpdate, "redact": st.AdsRedaction, "pass": st.URLPassthrough,
	}
	raw, _ := json.Marshal(cfg)
	return `<script id="gooike-consent-mode">window.dataLayer=window.dataLayer||[];window.gtag=window.gtag||function(){dataLayer.push(arguments);};` +
		`(function(c){function set(o,list,v){(list||[]).forEach(function(x){o[x]=v;});}` +
		`var d={},gpc=c.gpc&&navigator.globalPrivacyControl===true;set(d,c.all,c.optout?'granted':'denied');if(c.optout&&gpc){set(d,c.map.marketing,'denied');}set(d,c.base,'granted');` +
		`if(c.wait){d.wait_for_update=c.wait;}gtag('consent','default',d);if(c.redact){gtag('set','ads_data_redaction',true);}if(c.pass){gtag('set','url_passthrough',true);}` +
		`var m=document.cookie.match(new RegExp('(?:^|;\\s*)'+c.cookie+'=([^;]+)'));if(!m){return;}var s;try{s=JSON.parse(decodeURIComponent(m[1]));}catch(e){return;}` +
		`if(!s||s.v!==c.v||!s.c||!s.c.forEach){return;}var u={};set(u,c.all,'denied');set(u,c.base,'granted');s.c.forEach(function(k){set(u,c.map[k],'granted');});gtag('consent','update',u);` +
		`})(` + string(raw) + `);</script>` + "\n"
}

// gooikeConfigScript is gooike.js's configuration.
func gooikeConfigScript(st *gooikeSettings, preview bool) string {
	type cat struct {
		Key     string   `json:"key"`
		Signals []string `json:"signals"`
	}
	var cats []cat
	for _, c := range st.visibleCategories() {
		cats = append(cats, cat{c.Key, append([]string{}, c.Signals...)})
	}
	cookies := map[string][]string{}
	for _, ck := range st.Cookies {
		if ck.Category != "necessary" && ck.Name != "" {
			cookies[ck.Category] = append(cookies[ck.Category], ck.Name)
		}
	}
	t := gooikeThemeByKey(st.Design.Theme)
	cfg := map[string]any{
		"v": st.PolicyVersion, "days": st.ExpiryMonths * 365 / 12, "cookie": gooikeCookie, "mode": st.Regulation,
		"cats": cats, "signals": gooikeSignals, "cm": st.ConsentMode, "gpc": st.RespectGPC, "dnt": st.RespectDNT,
		"log": st.LogConsent, "endpoint": "/wp-json/" + gooikeNamespace + "/consent", "geo": st.Geo,
		"geoEndpoint": "/wp-json/" + gooikeNamespace + "/geo", "reload": st.ReloadOnChange, "float": st.Design.Floating,
		"modal": t != nil && (t.Layout == "modal" || t.Layout == "wall"), "cookies": cookies, "preview": preview,
		"saved": st.Texts.WithdrawNotice,
	}
	raw, _ := json.Marshal(cfg)
	return `<script type="application/json" id="gooike-config">` + strings.ReplaceAll(string(raw), "</", `<\/`) + "</script>\n"
}

// gooikePrivacyURL is the privacy policy link: the URL typed in Gooike's settings, its page, or
// the site's Privacy Policy page (Settings › Privacy).
func (s *Server) gooikePrivacyURL(ctx context.Context, st *gooikeSettings) string {
	if st.PrivacyURL != "" {
		return st.PrivacyURL
	}
	id := st.PrivacyPage
	if id == 0 {
		id = int64(s.settings.Int("wp_page_for_privacy_policy"))
	}
	if id == 0 {
		return ""
	}
	p, err := s.store.GetPost(ctx, id)
	if err != nil || p.Status != "publish" {
		return ""
	}
	return s.links(ctx).Permalink(p)
}

const gooikeCookieSVG = `<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path fill="currentColor" d="M21.6 11.2a3 3 0 0 1-3.3-2.9 3 3 0 0 1-3-3.4A3 3 0 0 1 13 2.1 10 10 0 1 0 21.9 12a3 3 0 0 1-.3-.8ZM8 7.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm-1 7a1.5 1.5 0 1 1 3 0 1.5 1.5 0 0 1-3 0Zm5.5 4a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3Zm.5-7a1 1 0 1 1 0-2 1 1 0 0 1 0 2Zm4 3.5a1.2 1.2 0 1 1 0-2.4 1.2 1.2 0 0 1 0 2.4Z"/></svg>`

const gooikeIllustration = `<svg class="gk-illu" viewBox="0 0 120 120" aria-hidden="true" focusable="false"><circle cx="60" cy="62" r="46" fill="#e6a157"/><path d="M60 16a46 46 0 1 0 46 46 12 12 0 0 1-14-12 12 12 0 0 1-14-14 12 12 0 0 1-18-20Z" fill="#d48a3a"/><circle cx="44" cy="48" r="6" fill="#6b3b12"/><circle cx="70" cy="72" r="7" fill="#6b3b12"/><circle cx="46" cy="82" r="5" fill="#6b3b12"/><circle cx="80" cy="44" r="4" fill="#6b3b12"/><circle cx="62" cy="50" r="3" fill="#6b3b12"/><path d="M96 18c3 4 9 5 12 2-1 5 2 9 6 10-5 2-6 7-4 11-4-3-10-2-12 2 0-5-4-8-8-8 4-3 5-8 3-12 1 0 2-3 3-5Z" fill="#f59e0b" opacity=".8"/></svg>`

// gooikeMarkup is the banner, the preferences modal and the floating button. preview is the admin's
// live preview (the banner shows without a stored choice and nothing is saved).
func (s *Server) gooikeMarkup(ctx context.Context, st *gooikeSettings, preview bool) string {
	esc := html.EscapeString
	tx := st.Texts
	t := gooikeThemeByKey(st.Design.Theme)
	modal := t.Layout == "modal" || t.Layout == "wall"
	var b strings.Builder

	// Links under the message.
	var links []string
	if u := s.gooikePrivacyURL(ctx, st); u != "" {
		links = append(links, `<a href="`+esc(u)+`">`+esc(tx.PrivacyLink)+`</a>`)
	}
	if st.CookiePolicyURL != "" {
		links = append(links, `<a href="`+esc(st.CookiePolicyURL)+`">`+esc(tx.CookieLink)+`</a>`)
	}
	if st.Regulation == "kvkk" && st.NoticeURL != "" {
		links = append(links, `<a href="`+esc(st.NoticeURL)+`">`+esc(tx.NoticeLink)+`</a>`)
	}

	role := `role="region"`
	if modal {
		role = `role="dialog" aria-modal="true"`
	}
	fmt.Fprintf(&b, `<div id="gooike-banner" class="gooike gk-banner gk-theme-%s gk-layout-%s gk-pos-%s gk-mode-%s" %s aria-labelledby="gk-title" aria-describedby="gk-msg" data-gooike-layout="%s" hidden>`,
		esc(t.Key), esc(t.Layout), esc(st.Design.Position), esc(st.Regulation), role, esc(t.Layout))
	b.WriteString(`<div class="gk-inner">`)
	if t.Layout == "card" {
		b.WriteString(gooikeIllustration)
	}
	b.WriteString(`<div class="gk-text"><h2 id="gk-title" class="gk-title">` + esc(tx.Title) + `</h2><p id="gk-msg" class="gk-msg">` + esc(tx.Message) + `</p>`)
	if len(links) > 0 {
		b.WriteString(`<p class="gk-links">` + strings.Join(links, "") + `</p>`)
	}
	b.WriteString(`</div><div class="gk-actions">`)
	reject := tx.Reject
	if st.Regulation == "ccpa" {
		reject = tx.DoNotSell
	}
	// Accept and Reject are the same kind of button: equally prominent, as the EDPB asks.
	b.WriteString(`<button type="button" class="gk-btn gk-btn-primary gk-btn-reject" data-gooike="reject">` + esc(reject) + `</button>`)
	b.WriteString(`<button type="button" class="gk-btn gk-btn-primary gk-btn-accept" data-gooike="accept">` + esc(tx.Accept) + `</button>`)
	b.WriteString(`<button type="button" class="gk-btn gk-btn-secondary gk-btn-customize" data-gooike="customize" aria-haspopup="dialog">` + esc(tx.Customize) + `</button>`)
	b.WriteString(`</div></div></div>` + "\n")

	// Preferences modal.
	cls := "gooike gk-prefs gk-theme-" + esc(t.Key)
	b.WriteString(`<div id="gooike-prefs" class="` + cls + `" hidden><div class="gk-overlay" data-gooike="close"></div>`)
	b.WriteString(`<div class="gk-dialog" role="dialog" aria-modal="true" aria-labelledby="gk-prefs-title" tabindex="-1">`)
	b.WriteString(`<div class="gk-dialog-head"><h2 id="gk-prefs-title" class="gk-title">` + esc(tx.PrefsTitle) + `</h2><button type="button" class="gk-close" data-gooike="close" aria-label="` + esc(tx.Close) + `"><span aria-hidden="true">&times;</span></button></div>`)
	b.WriteString(`<div class="gk-dialog-body"><p class="gk-msg">` + esc(tx.PrefsIntro) + `</p>`)
	if len(links) > 0 {
		b.WriteString(`<p class="gk-links">` + strings.Join(links, "") + `</p>`)
	}
	for _, c := range st.visibleCategories() {
		id := "gk-cat-" + esc(c.Key)
		b.WriteString(`<section class="gk-cat" data-gooike-section="` + esc(c.Key) + `"><div class="gk-cat-head"><h3 id="` + id + `-label">` + esc(c.Label) + `</h3>`)
		if c.Key == "necessary" {
			b.WriteString(`<span class="gk-always">` + esc(tx.AlwaysOn) + `</span>`)
		} else {
			b.WriteString(`<label class="gk-switch"><input type="checkbox" role="switch" data-gooike-cat="` + esc(c.Key) + `" aria-labelledby="` + id + `-label" aria-describedby="` + id + `-desc"><span class="gk-slider" aria-hidden="true"></span></label>`)
		}
		b.WriteString(`</div><p id="` + id + `-desc" class="gk-cat-desc">` + esc(c.Description) + `</p>`)
		var rows []gooikeCookieRow
		for _, ck := range st.Cookies {
			if ck.Category == c.Key {
				rows = append(rows, ck)
			}
		}
		if len(rows) > 0 {
			fmt.Fprintf(&b, `<details class="gk-cookies"><summary>%s (%d)</summary>%s</details>`, esc(tx.CookiesLabel), len(rows), gooikeCookieTable(tx, rows, "gk-table"))
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`<p class="gk-id">` + esc(tx.ConsentID) + `: <code data-gooike-id>&mdash;</code> <span data-gooike-date></span></p>`)
	b.WriteString(`</div><div class="gk-dialog-foot">`)
	b.WriteString(`<button type="button" class="gk-btn gk-btn-primary" data-gooike="reject">` + esc(reject) + `</button>`)
	b.WriteString(`<button type="button" class="gk-btn gk-btn-secondary" data-gooike="save">` + esc(tx.Save) + `</button>`)
	b.WriteString(`<button type="button" class="gk-btn gk-btn-primary" data-gooike="accept">` + esc(tx.Accept) + `</button>`)
	b.WriteString(`</div></div></div>` + "\n")

	// Floating button to reopen the preferences and withdraw consent.
	side := "left"
	if st.Design.FloatPosition == "right" {
		side = "right"
	}
	if st.Design.Floating || preview {
		b.WriteString(`<button type="button" id="gooike-float" class="gooike gk-float gk-float-` + side + `" data-gooike="open" aria-label="` + esc(tx.FloatLabel) + `" title="` + esc(tx.FloatLabel) + `" hidden>` + gooikeCookieSVG + `</button>` + "\n")
	}
	return b.String()
}

// gooikeCookieTable is a cookie declaration table.
func gooikeCookieTable(tx gooikeTexts, rows []gooikeCookieRow, class string) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<table class="` + class + `"><thead><tr><th scope="col">` + esc(tx.TableName) + `</th><th scope="col">` + esc(tx.TableProvider) + `</th><th scope="col">` + esc(tx.TablePurpose) + `</th><th scope="col">` + esc(tx.TableDuration) + `</th></tr></thead><tbody>`)
	for _, r := range rows {
		b.WriteString(`<tr><td><code>` + esc(r.Name) + `</code></td><td>` + esc(r.Provider) + `</td><td>` + esc(r.Purpose) + `</td><td>` + esc(r.Duration) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

var (
	gooikeShortcodeRe = regexp.MustCompile(`(?:<p>\s*)?\[gooike_(preferences|cookie_table|do_not_sell|consent_id)((?:\s+[a-z_]+="[^"\]]*")*)\s*/?\](?:\s*</p>)?`)
	gooikeSCAttrRe    = regexp.MustCompile(`([a-z_]+)="([^"]*)"`)
)

// gooikeShortcodes renders [gooike_preferences], [gooike_cookie_table category="…"],
// [gooike_do_not_sell] and [gooike_consent_id]. While the plugin is off they are left as typed,
// like any shortcode of an inactive plugin.
func (s *Server) gooikeShortcodes(ctx context.Context, content string) string {
	if !s.gooikeOn() || !strings.Contains(content, "[gooike_") {
		return content
	}
	st := s.gooikeLoad()
	esc := html.EscapeString
	return gooikeShortcodeRe.ReplaceAllStringFunc(content, func(m string) string {
		sm := gooikeShortcodeRe.FindStringSubmatch(m)
		attrs := map[string]string{}
		for _, a := range gooikeSCAttrRe.FindAllStringSubmatch(sm[2], -1) {
			attrs[a[1]] = html.UnescapeString(a[2])
		}
		inline := func(out string) string { // keep the paragraph around inline output
			if strings.HasPrefix(m, "<p>") {
				return "<p>" + out + "</p>"
			}
			return out
		}
		switch sm[1] {
		case "preferences":
			label := st.Texts.PreferencesSC
			if v := attrs["text"]; v != "" {
				label = v
			}
			return inline(`<a href="#gooike-preferences" class="gooike-open-preferences" data-gooike="open" role="button">` + esc(label) + `</a>`)
		case "do_not_sell":
			label := st.Texts.DoNotSell
			if v := attrs["text"]; v != "" {
				label = v
			}
			return inline(`<a href="#gooike-preferences" class="gooike-do-not-sell" data-gooike="open" role="button">` + esc(label) + `</a>`)
		case "consent_id":
			return inline(`<code class="gooike-consent-id" data-gooike-id>&mdash;</code>`)
		}
		// cookie_table
		var b strings.Builder
		b.WriteString(`<div class="gooike-cookie-declaration">`)
		only := attrs["category"]
		for _, c := range st.visibleCategories() {
			if only != "" && c.Key != only {
				continue
			}
			var rows []gooikeCookieRow
			for _, ck := range st.Cookies {
				if ck.Category == c.Key {
					rows = append(rows, ck)
				}
			}
			b.WriteString(`<h3 class="gooike-cookie-category">` + esc(c.Label) + `</h3><p>` + esc(c.Description) + `</p>`)
			if len(rows) == 0 {
				b.WriteString(`<p class="gooike-cookie-empty"><em>` + esc(st.Texts.TableEmpty) + `</em></p>`)
				continue
			}
			b.WriteString(`<div class="gooike-cookie-table-wrap">` + gooikeCookieTable(st.Texts, rows, "gooike-cookie-table") + `</div>`)
		}
		b.WriteString(`</div>`)
		return b.String()
	})
}

var (
	gooikeIframeRe    = regexp.MustCompile(`(?is)<iframe\b([^>]*)>(.*?)</iframe\s*>`)
	gooikeEmbedStyle  = `<link rel="stylesheet" id="gooike-css"`
	gooikeIframeSrcRe = regexp.MustCompile(`(?is)(\s)src(\s*=\s*)("[^"]*"|'[^']*')`)
)

// gooikeFilterPage holds back the iframes of YouTube, Vimeo and Google Maps on a rendered page:
// the src moves to data-gooike-src and a placeholder explains what loads after consent.
func (s *Server) gooikeFilterPage(c *gin.Context, page []byte) []byte {
	if !s.gooikeFront(c) || !strings.Contains(string(page), gooikeEmbedStyle) {
		return page
	}
	st := s.gooikeLoad()
	if !st.BlockYouTube && !st.BlockVimeo && !st.BlockMaps {
		return page
	}
	doc := string(page)
	if !strings.Contains(doc, "<iframe") {
		return page
	}
	cat := st.EmbedCategory
	catLabel := cat
	if c := st.category(cat); c != nil {
		catLabel = c.Label
	}
	esc := html.EscapeString
	out := gooikeIframeRe.ReplaceAllStringFunc(doc, func(tag string) string {
		m := gooikeIframeRe.FindStringSubmatch(tag)
		attrs := m[1]
		if strings.Contains(attrs, "data-gooike-") {
			return tag
		}
		sm := gooikeSrcValueRe.FindStringSubmatch(attrs)
		if sm == nil {
			return tag
		}
		src := strings.Trim(sm[1], `"'`)
		key, name := gooikeEmbedFor(&st, src)
		if key == "" {
			return tag
		}
		attrs = gooikeIframeSrcRe.ReplaceAllString(attrs, "${1}data-gooike-src${2}${3}")
		text := st.Texts.EmbedText
		if strings.Count(text, "%s") == 2 {
			text = fmt.Sprintf(text, name, strings.ToLower(catLabel))
		}
		return `<div class="gooike gooike-embed gooike-embed-` + key + `" data-gooike-embed="` + esc(cat) + `">` +
			`<iframe data-gooike-category="` + esc(cat) + `"` + attrs + `>` + m[2] + `</iframe>` +
			`<div class="gooike-embed-ph"><p>` + esc(text) + `</p><p><button type="button" class="gk-btn gk-btn-primary" data-gooike-accept-cat="` + esc(cat) + `">` + esc(st.Texts.EmbedButton) + `</button> ` +
			`<a href="` + esc(html.UnescapeString(src)) + `" target="_blank" rel="noopener noreferrer">` + esc(name) + ` &#8599;</a></p></div></div>`
	})
	return []byte(out)
}
