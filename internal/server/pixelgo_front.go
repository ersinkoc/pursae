package server

import (
	"encoding/json"
	"html"
	"strings"

	"github.com/gin-gonic/gin"
)

// Pursae Pixel on the site. Everything printed here is the same for every visitor of a page (the
// order-received page aside), so pages stay cacheable.
//
//   - wp_head: one tag snippet per enabled platform. With Pursae Cookie active they are printed inert
//     (gooikeInert) in the platform's consent category and Pursae Cookie runs them when the visitor accepts
//     it. Each snippet ends by setting window.pixelgoReady[platform], which pixelgo.js waits for.
//   - wp_footer: the page configuration (a JSON data block, so no inline script) and pixelgo.js.

// pixelFront decides whether anything is printed on this request and returns the settings.
func (s *Server) pixelFront(c *gin.Context, q *siteQuery) (pixelSettings, bool) {
	if c == nil || !s.pixelOn() {
		return pixelSettings{}, false
	}
	st := s.pixelLoad()
	if len(st.active()) == 0 || (q != nil && (q.Preview || q.Kind == "feed")) {
		return st, false
	}
	if u := currentUser(c); u != nil {
		for _, r := range u.Roles {
			if pixelHas(st.ExcludeRoles, r) {
				return st, false
			}
		}
	}
	return st, true
}

// pixelConsentOn reports whether tags go through Pursae Cookie's consent gate.
func (s *Server) pixelConsentOn(st *pixelSettings) bool { return st.RequireConsent && s.gooikeOn() }

// pixelCategory is the consent category of a platform: its own, or marketing when the site removed it.
func pixelCategory(g *gooikeSettings, def *pixelPlatformDef) string {
	if g == nil || g.hasCategory(def.Category) { // without the gate the platform keeps its own category
		return def.Category
	}
	return "marketing"
}

// pixelHead prints the platform tags.
func (s *Server) pixelHead(c *gin.Context, q *siteQuery) string {
	st, ok := s.pixelFront(c, q)
	if !ok {
		return ""
	}
	var g *gooikeSettings
	gate := s.pixelConsentOn(&st)
	if gate {
		gs := s.gooikeLoad()
		g = &gs
	}
	var b strings.Builder
	b.WriteString("<!-- Pursae Pixel -->\n<script id=\"pixelgo-ready\">window.pixelgoReady=window.pixelgoReady||{};</script>\n")
	for _, def := range st.active() {
		cspAllow(c, "script-src", def.Script...)
		cspAllow(c, "connect-src", def.Connect...)
		cspAllow(c, "img-src", def.Img...)
		code := pixelSnippet(def, &st, st.Platforms[def.Key])
		if gate {
			code = gooikeInert(code, pixelCategory(g, def))
		}
		b.WriteString(code + "\n")
	}
	return b.String()
}

// pixelSnippet is the tag of a platform. IDs were validated by pixelSanitize and are escaped again here.
func pixelSnippet(def *pixelPlatformDef, st *pixelSettings, cfg pixelPlatformCfg) string {
	js := func(v string) string {
		b, _ := json.Marshal(v)
		return strings.NewReplacer("<", `<`, ">", `>`).Replace(string(b))
	}
	ready := "window.pixelgoReady=window.pixelgoReady||{};window.pixelgoReady." + def.Key + "=1;"
	switch def.Key {
	case "meta":
		var inits strings.Builder
		for _, id := range cfg.IDs {
			inits.WriteString("fbq('init'," + js(id) + ");")
		}
		return `<script>!function(f,b,e,v,n,t,s){if(f.fbq)return;n=f.fbq=function(){n.callMethod?n.callMethod.apply(n,arguments):n.queue.push(arguments)};if(!f._fbq)f._fbq=n;n.push=n;n.loaded=!0;n.version='2.0';n.queue=[];t=b.createElement(e);t.async=!0;t.src=v;s=b.getElementsByTagName(e)[0];s.parentNode.insertBefore(t,s)}(window,document,'script','https://connect.facebook.net/en_US/fbevents.js');` +
			inits.String() + ready + `</script>`
	case "ga4", "gads":
		var cf strings.Builder
		for _, id := range cfg.IDs {
			if def.Key == "ga4" {
				cf.WriteString("gtag('config'," + js(id) + ",{send_page_view:false});")
			} else {
				cf.WriteString("gtag('config'," + js(id) + ");")
			}
		}
		return `<script async src="https://www.googletagmanager.com/gtag/js?id=` + html.EscapeString(cfg.IDs[0]) + `"></script>` + "\n" +
			`<script>window.dataLayer=window.dataLayer||[];window.gtag=window.gtag||function(){dataLayer.push(arguments);};gtag('js',new Date());` + cf.String() + ready + `</script>`
	case "tiktok":
		var loads strings.Builder
		for _, id := range cfg.IDs {
			loads.WriteString("ttq.load(" + js(id) + ");")
		}
		return `<script>!function(w,d,t){w.TiktokAnalyticsObject=t;var ttq=w[t]=w[t]||[];ttq.methods=["page","track","identify","instances","debug","on","off","once","ready","alias","group","enableCookie","disableCookie"],ttq.setAndDefer=function(t,e){t[e]=function(){t.push([e].concat(Array.prototype.slice.call(arguments,0)))}};for(var i=0;i<ttq.methods.length;i++)ttq.setAndDefer(ttq,ttq.methods[i]);ttq.instance=function(t){for(var e=ttq._i[t]||[],n=0;n<ttq.methods.length;n++)ttq.setAndDefer(e,ttq.methods[n]);return e},ttq.load=function(e,n){var i="https://analytics.tiktok.com/i18n/pixel/events.js";ttq._i=ttq._i||{},ttq._i[e]=[],ttq._i[e]._u=i,ttq._t=ttq._t||{},ttq._t[e]=+new Date,ttq._o=ttq._o||{},ttq._o[e]=n||{};var o=document.createElement("script");o.type="text/javascript",o.async=!0,o.src=i+"?sdkid="+e+"&lib="+t;var a=document.getElementsByTagName("script")[0];a.parentNode.insertBefore(o,a)};` +
			loads.String() + `}(window,document,'ttq');` + ready + `</script>`
	case "pinterest":
		var loads strings.Builder
		for _, id := range cfg.IDs {
			loads.WriteString("pintrk('load'," + js(id) + ");")
		}
		return `<script>!function(e){if(!window.pintrk){window.pintrk=function(){window.pintrk.queue.push(Array.prototype.slice.call(arguments))};var n=window.pintrk;n.queue=[],n.version="3.0";var t=document.createElement("script");t.async=!0,t.src=e;var r=document.getElementsByTagName("script")[0];r.parentNode.insertBefore(t,r)}}("https://s.pinimg.com/ct/core.js");` +
			loads.String() + ready + `</script>`
	case "bing":
		page := ""
		if st.General["page_view"] {
			page = `,w[u].push("pageLoad")`
		}
		return `<script>(function(w,d,t,r,u){var f,n,i;w[u]=w[u]||[],f=function(){var o={ti:` + js(cfg.IDs[0]) + `,enableAutoSpaTracking:true};o.q=w[u],w[u]=new UET(o)` + page + `},n=d.createElement(t),n.src=r,n.async=1,n.onload=n.onreadystatechange=function(){var s=this.readyState;s&&s!=="loaded"&&s!=="complete"||(f(),n.onload=n.onreadystatechange=null)},i=d.getElementsByTagName(t)[0],i.parentNode.insertBefore(n,i)})(window,document,"script","https://bat.bing.com/bat.js","uetq");` + ready + `</script>`
	}
	return ""
}

// pixelFooter prints the page configuration and pixelgo.js.
func (s *Server) pixelFooter(c *gin.Context, q *siteQuery) string {
	st, ok := s.pixelFront(c, q)
	if !ok {
		return ""
	}
	cfg := s.pixelConfig(c, q, &st)
	raw, err := json.Marshal(cfg) // encoding/json escapes <, > and & as <…: safe inside a script element
	if err != nil {
		return ""
	}
	return `<script type="application/json" id="pixelgo-config">` + string(raw) + `</script>` + "\n" +
		`<script src="` + pixelAssets + `/pixelgo.js?ver=` + pixelVersion + `" id="pixelgo-js" defer></script>` + "\n"
}

// pixelConfig is the configuration of pixelgo.js for the page.
func (s *Server) pixelConfig(c *gin.Context, q *siteQuery, st *pixelSettings) map[string]any {
	plats := map[string]any{}
	cats := map[string]string{}
	var g *gooikeSettings
	gate := s.pixelConsentOn(st)
	if gate {
		gs := s.gooikeLoad()
		g = &gs
	}
	for _, def := range st.active() {
		pc := st.Platforms[def.Key]
		m := map[string]any{"ids": pc.IDs, "server": pc.ServerSide && pc.Token != ""}
		if def.Key == "gads" && len(pc.Labels) > 0 {
			m["labels"] = pc.Labels
		}
		plats[def.Key] = m
		cats[def.Key] = pixelCategory(g, def)
	}
	events, page := s.pixelPageEvents(c, q, st)
	cookie := gooikeCookie
	custom := []map[string]any{}
	for _, ce := range st.Custom {
		if !ce.Enabled {
			continue
		}
		params := map[string]any{}
		for _, ln := range pixelParamLines(ce.Params) {
			k, v, _ := strings.Cut(ln, "=")
			params[k] = v
		}
		custom = append(custom, map[string]any{"id": ce.ID, "name": ce.Name, "trigger": ce.Trigger, "selector": ce.Selector, "url": ce.URL,
			"number": ce.Number, "value": ce.Value, "currency": ce.Currency, "params": params, "platforms": ce.Platforms, "once": ce.Once})
	}
	general := map[string]bool{}
	for k, v := range st.General {
		general[k] = v
	}
	woo := map[string]any{"sku": st.ContentID == "sku", "prefix": st.IDPrefix, "suffix": st.IDSuffix}
	for k, v := range st.Woo {
		woo[k] = v
	}
	cfg := map[string]any{
		"v": 1, "rest": "/wp-json/" + pixelNamespace + "/event", "debug": st.Debug, "map": pixelEventMap, "metaStandard": pixelMetaStandard,
		"consent":   map[string]any{"require": gate, "cookie": cookie, "cats": cats},
		"platforms": plats, "general": general, "scroll": pixelInts(st.ScrollDepths), "time": pixelInts(st.TimeSeconds),
		"downloads": strings.Split(st.DownloadExts, ","), "woo": woo,
		"attr":   map[string]any{"enabled": st.Attribution, "days": st.CookieDays},
		"events": events, "page": page, "custom": custom,
	}
	if q != nil && q.Post != nil {
		cfg["post"] = map[string]any{"id": q.Post.ID, "type": q.Post.Type}
	}
	return cfg
}

func pixelInts(csv string) []int {
	out := []int{}
	for _, p := range strings.Split(csv, ",") {
		n := 0
		for _, r := range p {
			n = n*10 + int(r-'0')
		}
		if p != "" {
			out = append(out, n)
		}
	}
	return out
}
