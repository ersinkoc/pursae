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
	NoID                           bool // the service has no account ID (the snippet is the same for every site)
	LowerID                        bool // IDs are lower-case (the default normalises to upper-case)
	code                           func(id string) string
	sources                        func(id string) []string // CSP sources that depend on the ID (self-hosted services)
	Script, Connect, Frame         []string                 // CSP sources
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
		Note:      "Put Tag Manager in Necessary to use Consent Mode's advanced mode: it then always loads and its tags follow the consent signals Pursae Cookie sends.",
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
	{Key: "gads", Name: "Google Ads", Category: "marketing", Placement: "head", IDLabel: "Conversion ID", IDExample: "AW-123456789",
		IDPattern: regexp.MustCompile(`^AW-[0-9]{6,12}$`),
		code: func(id string) string {
			return `<script async src="https://www.googletagmanager.com/gtag/js?id=` + id + `"></script>` + "\n" +
				`<script>window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());gtag('config','` + id + `');</script>`
		},
		Script:  []string{"https://www.googletagmanager.com", "https://googleads.g.doubleclick.net"},
		Connect: []string{"https://www.googletagmanager.com", "https://*.google.com", "https://googleads.g.doubleclick.net"},
		Cookies: []gooikeCookieRow{
			{Name: "_gcl_aw", Provider: "Google Ads", Purpose: "Stores ad click information to attribute conversions.", Duration: "3 months"},
			{Name: "IDE", Provider: "Google DoubleClick", Purpose: "Measures the effectiveness of ads and shows relevant ads.", Duration: "13 months"},
		},
		Detect: []string{"googletagmanager.com/gtag/js?id=aw-"}},
	{Key: "pinterest", Name: "Pinterest Tag", Category: "marketing", Placement: "head", IDLabel: "Tag ID", IDExample: "2612345678901",
		IDPattern: regexp.MustCompile(`^[0-9]{10,16}$`),
		code: func(id string) string {
			return `<script>!function(e){if(!window.pintrk){window.pintrk=function(){window.pintrk.queue.push(Array.prototype.slice.call(arguments))};var n=window.pintrk;n.queue=[],n.version="3.0";var t=document.createElement("script");t.async=!0,t.src=e;var r=document.getElementsByTagName("script")[0];r.parentNode.insertBefore(t,r)}}("https://s.pinimg.com/ct/core.js");pintrk('load','` + id + `');pintrk('page');</script>`
		},
		Script:  []string{"https://s.pinimg.com"},
		Connect: []string{"https://ct.pinterest.com", "https://*.pinterest.com"},
		Cookies: []gooikeCookieRow{
			{Name: "_pin_unauth", Provider: "Pinterest", Purpose: "Groups actions of one visitor for ad measurement.", Duration: "1 year"},
			{Name: "_epik", Provider: "Pinterest", Purpose: "Stores the ad click ID to attribute conversions.", Duration: "1 year"},
		},
		Detect: []string{"s.pinimg.com/ct/core.js"}},
	{Key: "uet", Name: "Microsoft Advertising (UET)", Category: "marketing", Placement: "head", IDLabel: "UET tag ID", IDExample: "12345678",
		IDPattern: regexp.MustCompile(`^[0-9]{6,12}$`),
		code: func(id string) string {
			return `<script>(function(w,d,t,r,u){var f,n,i;w[u]=w[u]||[],f=function(){var o={ti:"` + id + `",enableAutoSpaTracking:true};o.q=w[u],w[u]=new UET(o),w[u].push("pageLoad")},n=d.createElement(t),n.src=r,n.async=1,n.onload=n.onreadystatechange=function(){var s=this.readyState;s&&s!=="loaded"&&s!=="complete"||(f(),n.onload=n.onreadystatechange=null)},i=d.getElementsByTagName(t)[0],i.parentNode.insertBefore(n,i)})(window,document,"script","//bat.bing.com/bat.js","uetq");</script>`
		},
		Script:  []string{"https://bat.bing.com"},
		Connect: []string{"https://bat.bing.com", "https://bat.bing.net"},
		Cookies: []gooikeCookieRow{
			{Name: "_uetsid", Provider: "Microsoft Advertising", Purpose: "Stores the visit session ID for ad measurement.", Duration: "1 day"},
			{Name: "_uetvid", Provider: "Microsoft Advertising", Purpose: "Stores a visitor ID for ad measurement.", Duration: "13 months"},
		},
		Detect: []string{"bat.bing.com/bat.js"}},
	{Key: "matomo", Name: "Matomo", Category: "statistics", Placement: "head", IDLabel: "Matomo address and site ID", IDExample: "analytics.example.com/1",
		IDPattern: regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}(/[a-z0-9_-]+)*/[0-9]{1,8}$`), LowerID: true,
		Note: "The host (and folder) of your Matomo, a slash, then the site ID. Matomo Cloud: yoursite.matomo.cloud/1.",
		code: func(id string) string {
			i := strings.LastIndex(id, "/")
			return `<script>var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);_paq.push(['enableLinkTracking']);(function(){var u="https://` + id[:i] + `/";_paq.push(['setTrackerUrl',u+'matomo.php']);_paq.push(['setSiteId','` + id[i+1:] + `']);var d=document,g=d.createElement('script'),s=d.getElementsByTagName('script')[0];g.async=true;g.src=u+'matomo.js';s.parentNode.insertBefore(g,s);})();</script>`
		},
		sources: func(id string) []string {
			host, _, _ := strings.Cut(id, "/")
			return []string{"https://" + host}
		},
		Cookies: []gooikeCookieRow{
			{Name: "_pk_id.*", Provider: "Matomo", Purpose: "Stores a visitor ID to count returning visitors.", Duration: "13 months"},
			{Name: "_pk_ses.*", Provider: "Matomo", Purpose: "Holds the data of the current visit.", Duration: "30 minutes"},
			{Name: "_pk_ref.*", Provider: "Matomo", Purpose: "Remembers the site the visitor came from.", Duration: "6 months"},
		},
		Detect: []string{"/matomo.js", "/piwik.js", "matomo.php"}},
	{Key: "intercom", Name: "Intercom Messenger", Category: "preferences", Placement: "footer", IDLabel: "App ID", IDExample: "ab12cd34",
		IDPattern: regexp.MustCompile(`^[a-z0-9]{6,12}$`), LowerID: true,
		code: func(id string) string {
			return `<script>window.intercomSettings={api_base:"https://api-iam.intercom.io",app_id:"` + id + `"};(function(){var w=window;var ic=w.Intercom;if(typeof ic==="function"){ic('reattach_activator');ic('update',w.intercomSettings);}else{var d=document;var i=function(){i.c(arguments);};i.q=[];i.c=function(args){i.q.push(args);};w.Intercom=i;var l=function(){var s=d.createElement('script');s.type='text/javascript';s.async=true;s.src='https://widget.intercom.io/widget/` + id + `';var x=d.getElementsByTagName('script')[0];x.parentNode.insertBefore(s,x);};if(document.readyState==='complete'){l();}else{w.addEventListener('load',l,false);}}})();</script>`
		},
		Script:  []string{"https://widget.intercom.io", "https://js.intercomcdn.com"},
		Connect: []string{"https://*.intercom.io", "wss://*.intercom.io", "https://*.intercomcdn.com"},
		Cookies: []gooikeCookieRow{
			{Name: "intercom-id-*", Provider: "Intercom", Purpose: "Identifies an anonymous visitor of the messenger.", Duration: "9 months"},
			{Name: "intercom-session-*", Provider: "Intercom", Purpose: "Keeps the messenger conversation across pages.", Duration: "7 days"},
			{Name: "intercom-device-id-*", Provider: "Intercom", Purpose: "Identifies the device the messenger runs on.", Duration: "9 months"},
		},
		Detect: []string{"widget.intercom.io", "intercomsettings"}},
	{Key: "hubspot", Name: "HubSpot Tracking", Category: "marketing", Placement: "footer", IDLabel: "Hub ID", IDExample: "12345678",
		IDPattern: regexp.MustCompile(`^[0-9]{5,12}$`),
		code: func(id string) string {
			return `<script type="text/javascript" id="hs-script-loader" async defer src="https://js.hs-scripts.com/` + id + `.js"></script>`
		},
		Script:  []string{"https://js.hs-scripts.com", "https://js.hs-analytics.net", "https://js.hs-banner.com", "https://js.hscollectedforms.net", "https://js.usemessages.com"},
		Connect: []string{"https://*.hubspot.com", "https://*.hubapi.com", "https://*.hsforms.com", "https://*.hscollectedforms.net"},
		Cookies: []gooikeCookieRow{
			{Name: "hubspotutk", Provider: "HubSpot", Purpose: "Keeps a visitor identity that is passed to HubSpot on form submission.", Duration: "6 months"},
			{Name: "__hstc", Provider: "HubSpot", Purpose: "Main tracking cookie: first, last and current visit and the session count.", Duration: "6 months"},
			{Name: "__hssc", Provider: "HubSpot", Purpose: "Keeps track of sessions.", Duration: "30 minutes"},
			{Name: "__hssrc", Provider: "HubSpot", Purpose: "Detects whether the browser was restarted.", Duration: "Session"},
		},
		Detect: []string{"js.hs-scripts.com", "js.hs-analytics.net"}},
	{Key: "stripe", Name: "Stripe.js", Category: "necessary", Placement: "head", NoID: true,
		Note:    "Stripe asks for Stripe.js on every page for fraud prevention; its cookies are strictly necessary for payments. Adding it declares them and allows Stripe's hosts.",
		code:    func(string) string { return `<script async src="https://js.stripe.com/v3/"></script>` },
		Script:  []string{"https://js.stripe.com", "https://*.js.stripe.com"},
		Connect: []string{"https://api.stripe.com", "https://m.stripe.com", "https://m.stripe.network", "https://q.stripe.com"},
		Cookies: []gooikeCookieRow{
			{Name: "__stripe_mid", Provider: "Stripe", Purpose: "Fraud prevention: identifies the device during payments.", Duration: "1 year"},
			{Name: "__stripe_sid", Provider: "Stripe", Purpose: "Fraud prevention: identifies the payment session.", Duration: "30 minutes"},
		},
		Detect: []string{"js.stripe.com/v3"}},
}

// gooikeRecaptchaCookies are what the reCAPTCHA v3 script of Pursae Contact Form sets once it runs.
var gooikeRecaptchaCookies = []gooikeCookieRow{
	{Name: "_GRECAPTCHA", Provider: "Google", Purpose: "Tells humans from bots with Google reCAPTCHA.", Duration: "6 months"},
	{Name: "rc::*", Provider: "Google", Purpose: "Stores reCAPTCHA risk analysis state in the browser.", Duration: "Persistent"},
}

// gooikeEmbeds are the third-party embeds Gooike can hold back until consent: their iframes get a
// placeholder, and the loader scripts some of them use (Scripts: lower-case signatures) are
// blocked like a script of the embed category.
var gooikeEmbeds = []struct {
	Key, Name string
	Hosts     []string // host suffixes, or host/path prefixes
	Scripts   []string
	Cookies   []gooikeCookieRow
}{
	{"youtube", "YouTube", []string{"youtube.com", "youtube-nocookie.com", "youtu.be"}, nil, []gooikeCookieRow{
		{Name: "YSC", Provider: "YouTube", Purpose: "Counts views of embedded videos.", Duration: "Session"},
		{Name: "VISITOR_INFO1_LIVE", Provider: "YouTube", Purpose: "Estimates the bandwidth to pick the video quality and tracks preferences.", Duration: "6 months"},
	}},
	{"vimeo", "Vimeo", []string{"player.vimeo.com", "vimeo.com"}, nil, []gooikeCookieRow{
		{Name: "vuid", Provider: "Vimeo", Purpose: "Collects analytics of the embedded Vimeo player.", Duration: "2 years"},
	}},
	{"maps", "Google Maps", []string{"google.com/maps", "maps.google.com", "www.google.com/maps"}, nil, []gooikeCookieRow{
		{Name: "NID", Provider: "Google", Purpose: "Remembers preferences of embedded Google services such as Maps.", Duration: "6 months"},
	}},
	{"spotify", "Spotify", []string{"open.spotify.com", "embed.spotify.com"}, nil, []gooikeCookieRow{
		{Name: "sp_t", Provider: "Spotify", Purpose: "Identifies the browser for the embedded Spotify player.", Duration: "1 year"},
		{Name: "sp_landing", Provider: "Spotify", Purpose: "Remembers how the visitor reached the embedded player.", Duration: "1 day"},
	}},
	{"soundcloud", "SoundCloud", []string{"w.soundcloud.com", "api.soundcloud.com"}, nil, []gooikeCookieRow{
		{Name: "sc_anonymous_id", Provider: "SoundCloud", Purpose: "Identifies an anonymous listener of the embedded player.", Duration: "10 years"},
	}},
	{"instagram", "Instagram", []string{"instagram.com"}, []string{"instagram.com/embed.js"}, []gooikeCookieRow{
		{Name: "csrftoken", Provider: "Instagram", Purpose: "Protects the embedded Instagram post against forged requests.", Duration: "1 year"},
		{Name: "mid", Provider: "Instagram", Purpose: "Identifies the browser for embedded Instagram content.", Duration: "13 months"},
		{Name: "ig_did", Provider: "Instagram", Purpose: "Identifies the device for embedded Instagram content.", Duration: "1 year"},
	}},
	{"x", "X (Twitter)", []string{"platform.twitter.com", "syndication.twitter.com", "platform.x.com"}, []string{"platform.twitter.com/widgets.js", "platform.x.com/widgets.js"}, []gooikeCookieRow{
		{Name: "guest_id", Provider: "X (Twitter)", Purpose: "Identifies a visitor who is not logged in to X for embedded posts.", Duration: "13 months"},
		{Name: "personalization_id", Provider: "X (Twitter)", Purpose: "Tracks activity on and off X to personalise content and ads.", Duration: "13 months"},
	}},
	{"facebook", "Facebook", []string{"www.facebook.com/plugins", "web.facebook.com/plugins", "www.facebook.com/v"}, []string{"connect.facebook.net/*/sdk.js", "connect.facebook.net/*/all.js"}, []gooikeCookieRow{
		{Name: "datr", Provider: "Facebook", Purpose: "Identifies the browser for security and site integrity in embedded Facebook content.", Duration: "13 months"},
	}},
	{"tiktok", "TikTok", []string{"www.tiktok.com/embed", "www.tiktok.com/player"}, []string{"tiktok.com/embed.js"}, []gooikeCookieRow{
		{Name: "ttwid", Provider: "TikTok", Purpose: "Identifies the visitor of an embedded TikTok video.", Duration: "1 year"},
		{Name: "tt_webid_v2", Provider: "TikTok", Purpose: "Identifies the browser for embedded TikTok videos.", Duration: "1 year"},
	}},
	{"dailymotion", "Dailymotion", []string{"dailymotion.com", "geo.dailymotion.com"}, nil, []gooikeCookieRow{
		{Name: "ts", Provider: "Dailymotion", Purpose: "Counts views of the embedded player.", Duration: "13 months"},
		{Name: "v1st", Provider: "Dailymotion", Purpose: "Identifies the browser of the embedded player.", Duration: "13 months"},
	}},
	{"twitch", "Twitch", []string{"player.twitch.tv", "clips.twitch.tv"}, nil, []gooikeCookieRow{
		{Name: "unique_id", Provider: "Twitch", Purpose: "Identifies the viewer of the embedded Twitch player.", Duration: "1 year"},
	}},
}

// embedBlocked reports whether an embed provider is held back until consent. YouTube, Vimeo and
// Google Maps have their own switches; every other provider is blocked unless listed in EmbedsOff.
func (st *gooikeSettings) embedBlocked(key string) bool {
	switch key {
	case "youtube":
		return st.BlockYouTube
	case "vimeo":
		return st.BlockVimeo
	case "maps":
		return st.BlockMaps
	}
	return !gooikeContains(st.EmbedsOff, key)
}

// gooikeSigIn reports whether a lower-case script signature occurs in low; a "*" in the signature
// stands for any run of characters without a quote or space (the locale in connect.facebook.net/xx_XX/sdk.js).
func gooikeSigIn(low, sig string) bool {
	pre, post, wild := strings.Cut(sig, "*")
	if !wild {
		return strings.Contains(low, sig)
	}
	for rest := low; ; {
		i := strings.Index(rest, pre)
		if i < 0 {
			return false
		}
		rest = rest[i+len(pre):]
		if j := strings.IndexAny(rest, "\"' <>/"); j >= 0 && strings.HasPrefix(rest[j:], post) {
			return true
		}
	}
}

// anyEmbedBlocked reports whether at least one embed provider is held back.
func (st *gooikeSettings) anyEmbedBlocked() bool {
	for _, e := range gooikeEmbeds {
		if st.embedBlocked(e.Key) {
			return true
		}
	}
	return false
}

func gooikeServiceByKey(k string) *gooikeService {
	for _, sv := range gooikeServices {
		if sv.Key == k {
			return sv
		}
	}
	return nil
}

// validID reports whether id is a usable account ID of the service.
func (sv *gooikeService) validID(id string) bool {
	if sv.NoID {
		return true
	}
	return sv.IDPattern != nil && sv.IDPattern.MatchString(id)
}

// code is the snippet of a script entry: the generated code of a service, or the admin's own.
func (sc gooikeScript) code() string {
	if sv := gooikeServiceByKey(sc.Service); sv != nil {
		if sv.validID(sc.ServiceID) {
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
			if sv.sources != nil && sv.validID(sc.ServiceID) {
				add("script-src", sv.sources(sc.ServiceID)...)
				add("connect-src", sv.sources(sc.ServiceID)...)
			}
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
	for _, e := range gooikeEmbeds {
		if !st.embedBlocked(e.Key) {
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

// gooikeBlockScripts holds back the third-party scripts of known services that a page carries
// outside the script manager (post content, theme templates, Web Builder custom HTML, other
// plugins): a script whose src or inline code carries the signature of a known service becomes
// type="text/plain" with that service's category, like a script manager entry. A script an author
// tags with data-gooike-category="<key>" is held back in that category. Scripts already inert,
// the necessary ones of the script manager and Pursae Cookie's own are left alone.
func gooikeBlockScripts(st *gooikeSettings, doc string) string {
	if !strings.Contains(doc, "<script") {
		return doc
	}
	necessary := ""
	for _, sc := range st.Scripts {
		if sc.Enabled && sc.Category == "necessary" {
			necessary += strings.ToLower(sc.code()) + "\n"
		}
	}
	return gooikeScriptTagRe.ReplaceAllStringFunc(doc, func(tag string) string {
		m := gooikeScriptTagRe.FindStringSubmatch(tag)
		attrs := m[1]
		if t := gooikeTypeAttrRe.FindStringSubmatch(attrs); t != nil && strings.Contains(strings.ToLower(t[1]), "text/plain") {
			return tag
		}
		if cm := gooikeCategoryAttrRe.FindStringSubmatch(attrs); cm != nil { // an author's manual tag
			cat := strings.Trim(cm[1], `"'`)
			if st.category(cat) == nil || cat == "necessary" {
				return tag
			}
			attrs = gooikeCategoryAttrRe.ReplaceAllString(attrs, "")
			return gooikeInert("<script"+attrs+">"+m[2]+"</script>", cat)
		}
		if strings.Contains(attrs, "data-gooike-") || strings.Contains(attrs, "gooike.js") {
			return tag
		}
		low := strings.ToLower(tag)
		best, bestLen := "", 0
		for _, sv := range gooikeServices {
			for _, d := range sv.Detect {
				if d = strings.ToLower(d); strings.Contains(low, d) && len(d) > bestLen && !strings.Contains(necessary, d) {
					best, bestLen = sv.Category, len(d)
				}
			}
		}
		if best == "" { // the loader script of a blocked embed provider (Instagram, X, TikTok, Facebook)
			for _, e := range gooikeEmbeds {
				if !st.embedBlocked(e.Key) {
					continue
				}
				for _, sig := range e.Scripts {
					if gooikeSigIn(low, sig) {
						best = st.EmbedCategory
					}
				}
			}
		}
		if best == "" && strings.Contains(low, "google.com/recaptcha/") && !strings.Contains(necessary, "google.com/recaptcha/") {
			best = st.EmbedCategory
		}
		if best == "" || best == "necessary" || st.category(best) == nil {
			return tag
		}
		return gooikeInert(tag, best)
	})
}

var gooikeCategoryAttrRe = regexp.MustCompile(`(?is)\s+data-gooike-category\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
