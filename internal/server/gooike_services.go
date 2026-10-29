package server

import (
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Known services: presets of the script manager. A service script stores only its ID; the code is
// generated when the page is rendered, so the snippet stays correct and the hosts it loads from are
// known (they are added to the page's Content-Security-Policy). Each service also brings the
// cookies it sets, for the declaration, and the signatures the scanner looks for.

type gooikeService struct {
	Key, Name, Category, Placement string
	IDLabel, IDExample             string
	IDPattern                      *regexp.Regexp
	Note                           string
	code                           func(id string) string
	Script, Connect, Frame         []string // CSP sources
	Cookies                        []gooikeCookieRow
	Detect                         []string // lower-case substrings that reveal the service in a page
}

var gooikeServices = []*gooikeService{
	{Key: "ga4", Name: "Google Analytics 4", Category: "statistics", Placement: "head", IDLabel: "Measurement ID", IDExample: "G-XXXXXXXXXX",
		IDPattern: regexp.MustCompile(`^G-[A-Z0-9]{4,16}$`),
		code: func(id string) string {
			return `<script async src="https://www.googletagmanager.com/gtag/js?id=` + id + `"></script>` + "\n" +
				`<script>window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());gtag('config','` + id + `');</script>`
		},
		Script:  []string{"https://www.googletagmanager.com"},
		Connect: []string{"https://*.google-analytics.com", "https://*.analytics.google.com", "https://www.googletagmanager.com"},
		Cookies: []gooikeCookieRow{
			{Name: "_ga", Provider: "Google Analytics", Purpose: "Distinguishes visitors to count visits and page views.", Duration: "2 years"},
			{Name: "_ga_*", Provider: "Google Analytics", Purpose: "Keeps the session state of a Google Analytics 4 property.", Duration: "2 years"},
		},
		Detect: []string{"googletagmanager.com/gtag/js", "gtag('config'"}},
	{Key: "gtm", Name: "Google Tag Manager", Category: "statistics", Placement: "head", IDLabel: "Container ID", IDExample: "GTM-XXXXXXX",
		IDPattern: regexp.MustCompile(`^GTM-[A-Z0-9]{4,12}$`),
		Note:      "Put Tag Manager in Necessary to use Consent Mode's advanced mode: it then always loads and its tags follow the consent signals Gooike sends.",
		code: func(id string) string {
			return `<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src='https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);})(window,document,'script','dataLayer','` + id + `');</script>`
		},
		Script:  []string{"https://www.googletagmanager.com", "https://tagmanager.google.com"},
		Connect: []string{"https://www.googletagmanager.com", "https://*.google-analytics.com", "https://*.analytics.google.com"},
		Cookies: []gooikeCookieRow{
			{Name: "_ga", Provider: "Google Tag Manager", Purpose: "Set by the Google Analytics tags of the container.", Duration: "2 years"},
		},
		Detect: []string{"googletagmanager.com/gtm.js"}},
	{Key: "meta", Name: "Meta Pixel", Category: "marketing", Placement: "head", IDLabel: "Pixel ID", IDExample: "123456789012345",
		IDPattern: regexp.MustCompile(`^[0-9]{6,20}$`),
		code: func(id string) string {
			return `<script>!function(f,b,e,v,n,t,s){if(f.fbq)return;n=f.fbq=function(){n.callMethod?n.callMethod.apply(n,arguments):n.queue.push(arguments)};if(!f._fbq)f._fbq=n;n.push=n;n.loaded=!0;n.version='2.0';n.queue=[];t=b.createElement(e);t.async=!0;t.src=v;s=b.getElementsByTagName(e)[0];s.parentNode.insertBefore(t,s)}(window,document,'script','https://connect.facebook.net/en_US/fbevents.js');fbq('init','` + id + `');fbq('track','PageView');</script>`
		},
		Script:  []string{"https://connect.facebook.net"},
		Connect: []string{"https://www.facebook.com", "https://connect.facebook.net"},
		Cookies: []gooikeCookieRow{
			{Name: "_fbp", Provider: "Meta", Purpose: "Identifies browsers to deliver and measure Meta advertising.", Duration: "3 months"},
			{Name: "fr", Provider: "Meta", Purpose: "Delivers, measures and improves the relevance of ads.", Duration: "3 months"},
		},
		Detect: []string{"connect.facebook.net", "fbq('init'"}},
	{Key: "hotjar", Name: "Hotjar", Category: "statistics", Placement: "head", IDLabel: "Site ID", IDExample: "1234567",
		IDPattern: regexp.MustCompile(`^[0-9]{3,12}$`),
		code: func(id string) string {
			return `<script>(function(h,o,t,j,a,r){h.hj=h.hj||function(){(h.hj.q=h.hj.q||[]).push(arguments)};h._hjSettings={hjid:` + id + `,hjsv:6};a=o.getElementsByTagName('head')[0];r=o.createElement('script');r.async=1;r.src=t+h._hjSettings.hjid+j+h._hjSettings.hjsv;a.appendChild(r);})(window,document,'https://static.hotjar.com/c/hotjar-','.js?sv=');</script>`
		},
		Script:  []string{"https://*.hotjar.com"},
		Connect: []string{"https://*.hotjar.com", "https://*.hotjar.io", "wss://*.hotjar.com"},
		Cookies: []gooikeCookieRow{
			{Name: "_hjSessionUser_*", Provider: "Hotjar", Purpose: "Keeps a Hotjar user ID unique to the site.", Duration: "1 year"},
			{Name: "_hjSession_*", Provider: "Hotjar", Purpose: "Holds the current session data.", Duration: "30 minutes"},
		},
		Detect: []string{"static.hotjar.com"}},
	{Key: "clarity", Name: "Microsoft Clarity", Category: "statistics", Placement: "head", IDLabel: "Project ID", IDExample: "abcd1234ef",
		IDPattern: regexp.MustCompile(`^[a-z0-9]{6,16}$`),
		code: func(id string) string {
			return `<script>(function(c,l,a,r,i,t,y){c[a]=c[a]||function(){(c[a].q=c[a].q||[]).push(arguments)};t=l.createElement(r);t.async=1;t.src="https://www.clarity.ms/tag/"+i;y=l.getElementsByTagName(r)[0];y.parentNode.insertBefore(t,y);})(window,document,"clarity","script","` + id + `");</script>`
		},
		Script:  []string{"https://*.clarity.ms"},
		Connect: []string{"https://*.clarity.ms"},
		Cookies: []gooikeCookieRow{
			{Name: "_clck", Provider: "Microsoft Clarity", Purpose: "Keeps the Clarity user ID and preferences.", Duration: "1 year"},
			{Name: "_clsk", Provider: "Microsoft Clarity", Purpose: "Connects page views of a session into one recording.", Duration: "1 day"},
			{Name: "MUID", Provider: "Microsoft", Purpose: "Identifies unique browsers across Microsoft sites.", Duration: "1 year"},
		},
		Detect: []string{"clarity.ms/tag"}},
	{Key: "linkedin", Name: "LinkedIn Insight Tag", Category: "marketing", Placement: "footer", IDLabel: "Partner ID", IDExample: "1234567",
		IDPattern: regexp.MustCompile(`^[0-9]{3,12}$`),
		code: func(id string) string {
			return `<script>_linkedin_partner_id="` + id + `";window._linkedin_data_partner_ids=window._linkedin_data_partner_ids||[];window._linkedin_data_partner_ids.push(_linkedin_partner_id);(function(l){if(!l){window.lintrk=function(a,b){window.lintrk.q.push([a,b])};window.lintrk.q=[]}var s=document.getElementsByTagName("script")[0];var b=document.createElement("script");b.type="text/javascript";b.async=true;b.src="https://snap.licdn.com/li.lms-analytics/insight.min.js";s.parentNode.insertBefore(b,s);})(window.lintrk);</script>`
		},
		Script:  []string{"https://snap.licdn.com"},
		Connect: []string{"https://px.ads.linkedin.com"},
		Cookies: []gooikeCookieRow{
			{Name: "li_gc", Provider: "LinkedIn", Purpose: "Stores the visitor's consent to non-essential LinkedIn cookies.", Duration: "6 months"},
			{Name: "lidc", Provider: "LinkedIn", Purpose: "Routes requests to the right data center.", Duration: "1 day"},
			{Name: "bcookie", Provider: "LinkedIn", Purpose: "Identifies the browser for ad measurement.", Duration: "1 year"},
		},
		Detect: []string{"snap.licdn.com"}},
	{Key: "tiktok", Name: "TikTok Pixel", Category: "marketing", Placement: "head", IDLabel: "Pixel ID", IDExample: "C1A2B3C4D5E6F7G8H9I0",
		IDPattern: regexp.MustCompile(`^[A-Z0-9]{10,30}$`),
		code: func(id string) string {
			return `<script>!function(w,d,t){w.TiktokAnalyticsObject=t;var ttq=w[t]=w[t]||[];ttq.methods=["page","track","identify","instances","debug","on","off","once","ready","alias","group","enableCookie","disableCookie"];ttq.setAndDefer=function(t,e){t[e]=function(){t.push([e].concat(Array.prototype.slice.call(arguments,0)))}};for(var i=0;i<ttq.methods.length;i++)ttq.setAndDefer(ttq,ttq.methods[i]);ttq.load=function(e,n){var i="https://analytics.tiktok.com/i18n/pixel/events.js";ttq._i=ttq._i||{};ttq._i[e]=[];ttq._i[e]._u=i;ttq._t=ttq._t||{};ttq._t[e]=+new Date;ttq._o=ttq._o||{};ttq._o[e]=n||{};var o=d.createElement("script");o.type="text/javascript";o.async=!0;o.src=i+"?sdkid="+e+"&lib="+t;var a=d.getElementsByTagName("script")[0];a.parentNode.insertBefore(o,a)};ttq.load('` + id + `');ttq.page();}(window,document,'ttq');</script>`
		},
		Script:  []string{"https://analytics.tiktok.com"},
		Connect: []string{"https://analytics.tiktok.com"},
		Cookies: []gooikeCookieRow{
			{Name: "_ttp", Provider: "TikTok", Purpose: "Measures and improves the performance of TikTok ads.", Duration: "13 months"},
			{Name: "_tt_enable_cookie", Provider: "TikTok", Purpose: "Records whether the browser accepts TikTok's cookies.", Duration: "13 months"},
		},
		Detect: []string{"analytics.tiktok.com"}},
}

// gooikeEmbeds are the third-party iframes Gooike can hold back until consent.
var gooikeEmbeds = []struct {
	Key, Name string
	Hosts     []string // host suffixes
	Cookies   []gooikeCookieRow
}{
	{"youtube", "YouTube", []string{"youtube.com", "youtube-nocookie.com", "youtu.be"}, []gooikeCookieRow{
		{Name: "YSC", Provider: "YouTube", Purpose: "Counts views of embedded videos.", Duration: "Session"},
		{Name: "VISITOR_INFO1_LIVE", Provider: "YouTube", Purpose: "Estimates the bandwidth to pick the video quality and tracks preferences.", Duration: "6 months"},
	}},
	{"vimeo", "Vimeo", []string{"player.vimeo.com", "vimeo.com"}, []gooikeCookieRow{
		{Name: "vuid", Provider: "Vimeo", Purpose: "Collects analytics of the embedded Vimeo player.", Duration: "2 years"},
	}},
	{"maps", "Google Maps", []string{"google.com/maps", "maps.google.com", "www.google.com/maps"}, []gooikeCookieRow{
		{Name: "NID", Provider: "Google", Purpose: "Remembers preferences of embedded Google services such as Maps.", Duration: "6 months"},
	}},
}

func gooikeServiceByKey(k string) *gooikeService {
	for _, sv := range gooikeServices {
		if sv.Key == k {
			return sv
		}
	}
	return nil
}

// code is the snippet of a script entry: the generated code of a service, or the admin's own.
func (sc gooikeScript) code() string {
	if sv := gooikeServiceByKey(sc.Service); sv != nil {
		if sv.IDPattern.MatchString(sc.ServiceID) {
			return sv.code(sc.ServiceID)
		}
		return ""
	}
	return sc.Code
}

var (
	gooikeScriptTagRe   = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	gooikeNoscriptRe    = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript\s*>`)
	gooikeSrcTagRe      = regexp.MustCompile(`(?is)<(iframe|img|embed)\b([^>]*)>`)
	gooikeTypeAttrRe    = regexp.MustCompile(`(?is)\s+type\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	gooikeSrcAttrRe     = regexp.MustCompile(`(?is)(\s)(src|srcset)(\s*=)`)
	gooikeSrcValueRe    = regexp.MustCompile(`(?is)\ssrc\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	gooikeURLInScriptRe = regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
)

// gooikeWrap makes plain JavaScript (a snippet without any tag) a script element.
func gooikeWrap(code string) string {
	t := strings.TrimSpace(code)
	if t == "" {
		return ""
	}
	if !strings.HasPrefix(t, "<") {
		return "<script>\n" + t + "\n</script>"
	}
	return t
}

// gooikeInert turns a snippet into its blocked form: every script becomes type="text/plain" with
// data-gooike-category (its src kept in data-gooike-src), iframes, images and embeds lose their src
// the same way, and <noscript> fallbacks, which would track without JavaScript and without
// consent, are dropped. gooike.js brings the elements back once the category is accepted.
// Necessary snippets run as they are.
func gooikeInert(code, category string) string {
	code = gooikeWrap(code)
	if code == "" || category == "necessary" {
		return code
	}
	cat := html.EscapeString(category)
	code = gooikeNoscriptRe.ReplaceAllString(code, "")
	code = gooikeScriptTagRe.ReplaceAllStringFunc(code, func(tag string) string {
		m := gooikeScriptTagRe.FindStringSubmatch(tag)
		attrs, body := m[1], m[2]
		extra := ""
		if t := gooikeTypeAttrRe.FindStringSubmatch(attrs); t != nil {
			typ := strings.Trim(t[1], `"'`)
			switch strings.ToLower(typ) {
			case "", "text/javascript", "application/javascript":
			default:
				extra = ` data-gooike-type="` + html.EscapeString(typ) + `"`
			}
			attrs = gooikeTypeAttrRe.ReplaceAllString(attrs, "")
		}
		attrs = gooikeSrcAttrRe.ReplaceAllString(attrs, "${1}data-gooike-${2}${3}")
		return `<script type="text/plain" data-gooike-category="` + cat + `"` + extra + attrs + `>` + body + `</script>`
	})
	code = gooikeSrcTagRe.ReplaceAllStringFunc(code, func(tag string) string {
		m := gooikeSrcTagRe.FindStringSubmatch(tag)
		attrs := gooikeSrcAttrRe.ReplaceAllString(m[2], "${1}data-gooike-${2}${3}")
		return `<` + m[1] + ` data-gooike-category="` + cat + `"` + attrs + `>`
	})
	return code
}

// gooikeCSPSources are the sources the enabled scripts load from, per CSP directive: the known
// hosts of services, the https origins of custom snippets and the admin's extra hosts.
func gooikeCSPSources(st *gooikeSettings) map[string][]string {
	set := map[string]map[string]bool{"script-src": {}, "connect-src": {}}
	add := func(dir string, srcs ...string) {
		for _, s := range srcs {
			set[dir][s] = true
		}
	}
	for _, sc := range st.Scripts {
		if !sc.Enabled {
			continue
		}
		if sv := gooikeServiceByKey(sc.Service); sv != nil {
			add("script-src", sv.Script...)
			add("connect-src", sv.Connect...)
			continue
		}
		for _, u := range gooikeURLInScriptRe.FindAllString(sc.Code, -1) {
			if pu, err := url.Parse(u); err == nil && pu.Host != "" {
				add("script-src", "https://"+pu.Host)
				add("connect-src", "https://"+pu.Host)
			}
		}
	}
	for _, h := range strings.Fields(strings.NewReplacer(",", " ", "\n", " ").Replace(st.ExtraHosts)) {
		if gooikeHostSourceRe.MatchString(h) {
			add("script-src", h)
			add("connect-src", h)
		}
	}
	out := map[string][]string{}
	for dir, m := range set {
		for s := range m {
			out[dir] = append(out[dir], s)
		}
		sort.Strings(out[dir])
	}
	return out
}

var gooikeHostSourceRe = regexp.MustCompile(`^(https|wss)://(\*\.)?[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+$`)

// gooikeEmbedFor returns the embed provider of an iframe URL, when blocking it is switched on.
func gooikeEmbedFor(st *gooikeSettings, src string) (key, name string) {
	u, err := url.Parse(strings.TrimSpace(html.UnescapeString(src)))
	if err != nil || u.Host == "" {
		if strings.HasPrefix(src, "//") {
			u, err = url.Parse("https:" + src)
		}
		if err != nil || u == nil || u.Host == "" {
			return "", ""
		}
	}
	host := strings.ToLower(u.Hostname())
	hp := host + u.Path
	on := map[string]bool{"youtube": st.BlockYouTube, "vimeo": st.BlockVimeo, "maps": st.BlockMaps}
	for _, e := range gooikeEmbeds {
		if !on[e.Key] {
			continue
		}
		for _, h := range e.Hosts {
			if strings.Contains(h, "/") {
				if strings.HasPrefix(hp, h) || strings.HasPrefix(strings.TrimPrefix(hp, "www."), strings.TrimPrefix(h, "www.")) {
					return e.Key, e.Name
				}
			} else if host == h || strings.HasSuffix(host, "."+h) {
				return e.Key, e.Name
			}
		}
	}
	return "", ""
}
