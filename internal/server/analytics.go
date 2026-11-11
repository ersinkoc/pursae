package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Pursae Analytics is a native, privacy-friendly statistics plugin in the class of Plausible,
// Jetpack Stats and MonsterInsights' dashboard, plus real-user Core Web Vitals:
//
//   - no cookies, no local storage, no IP address kept: a visitor is a hash of the IP address and the
//     user agent with a salt that changes every day and is then deleted (analytics_salts), so
//     visitors are counted per day and can never be followed across days or identified;
//   - a script of about 2.3 KB, 1.2 KB gzipped (analyticsScript), printed in wp_footer, the same for every visitor (it
//     works behind the page cache), sends a page view, the time the page was visible, outbound
//     link clicks, file downloads, form submissions, 404s and the page's LCP, INP, CLS, FCP and TTFB
//     with navigator.sendBeacon to POST /wpgo-analytics/collect;
//   - one admin page (Analytics) shows everything: visitors, visits, page views, bounce rate, visit
//     duration, a chart, top and entry pages, sources, UTM campaigns, countries, devices, browsers,
//     operating systems, goals, 404s and the Core Web Vitals (p75, site-wide and per page).
//
// Like every native plugin it is off on a fresh install (native_pursae_analytics); while it is off
// nothing is printed, the collector answers 404 and the menu is gone. Stored data stays.

const (
	analyticsSlug     = "pursae-analytics"
	analyticsSetting  = "native_pursae_analytics"
	analyticsOptions  = "pursae_analytics_settings"
	analyticsVersion  = "0.1.0"
	analyticsCollect  = "/wpgo-analytics/collect"
	analyticsPurgeJob = "pursae_analytics_purge"
)

// analyticsSettings are the plugin's settings (Analytics › Settings).
type analyticsSettings struct {
	ExcludeLoggedIn bool   `json:"exclude_logged_in"` // signed-in users are not counted
	ExcludeIPs      string `json:"exclude_ips"`       // one address or CIDR range per line
	RespectDNT      bool   `json:"respect_dnt"`       // Do Not Track / Global Privacy Control
	RetentionDays   int    `json:"retention_days"`    // raw events are deleted after this many days
	TrackOutbound   bool   `json:"track_outbound"`
	TrackDownloads  bool   `json:"track_downloads"`
	TrackForms      bool   `json:"track_forms"`
	Track404        bool   `json:"track_404"`
	Vitals          bool   `json:"vitals"`        // Core Web Vitals
	VitalsSample    int    `json:"vitals_sample"` // percent of page views that measure them
}

func analyticsDefaults() analyticsSettings {
	return analyticsSettings{ExcludeLoggedIn: true, RespectDNT: true, RetentionDays: 395, TrackOutbound: true,
		TrackDownloads: true, TrackForms: true, Track404: true, Vitals: true, VitalsSample: 100}
}

func (s *Server) analyticsOn() bool { return s.settings.Bool(analyticsSetting) }

// analyticsLoad reads the settings over the defaults.
func (s *Server) analyticsLoad() analyticsSettings {
	st := analyticsDefaults()
	if v := s.settings.Get(analyticsOptions); v != nil {
		if raw, err := json.Marshal(v); err == nil {
			_ = json.Unmarshal(raw, &st) // keys the saved settings lack keep their defaults
		}
	}
	return analyticsSanitize(st)
}

func analyticsSanitize(st analyticsSettings) analyticsSettings {
	if st.RetentionDays < 30 {
		st.RetentionDays = 30
	}
	if st.RetentionDays > 3650 {
		st.RetentionDays = 3650
	}
	if st.VitalsSample < 1 || st.VitalsSample > 100 {
		st.VitalsSample = 100
	}
	var ips []string
	for _, l := range strings.Split(st.ExcludeIPs, "\n") {
		if l = strings.TrimSpace(l); l != "" && len(ips) < 200 {
			ips = append(ips, l)
		}
	}
	st.ExcludeIPs = strings.Join(ips, "\n")
	return st
}

func (s *Server) analyticsSave(ctx context.Context, st analyticsSettings) error {
	if err := s.settings.Set(ctx, map[string]any{analyticsOptions: analyticsSanitize(st)}); err != nil {
		return err
	}
	s.purgeAll(ctx) // the script's options are part of every page
	return nil
}

// analyticsActivate switches the plugin on and schedules the daily retention job.
func (s *Server) analyticsActivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{analyticsSetting: true}); err != nil {
		return err
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence)
		SELECT $1, now() + interval '10 minutes', 'daily' WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE name = $1 AND recurrence <> '')`, analyticsPurgeJob); err != nil {
		return err
	}
	s.purgeAll(ctx) // pages get the script
	return nil
}

// analyticsDeactivate switches it off; the statistics stay.
func (s *Server) analyticsDeactivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{analyticsSetting: false}); err != nil {
		return err
	}
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM jobs WHERE name = $1`, analyticsPurgeJob)
	s.purgeAll(ctx)
	return nil
}

// analyticsPurge deletes events past the retention period and the salts of past days.
func (s *Server) analyticsPurge(ctx context.Context) (int64, error) {
	st := s.analyticsLoad()
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM analytics_events WHERE ts < now() - make_interval(days => $1)`, st.RetentionDays)
	if err != nil {
		return 0, err
	}
	_, err = s.store.Pool.Exec(ctx, `DELETE FROM analytics_salts WHERE day < (now() AT TIME ZONE 'UTC')::date`)
	return tag.RowsAffected(), err
}

// analyticsPluginRow is the plugin's row on the Plugins screen.
func (s *Server) analyticsPluginRow() pluginRow {
	r := pluginRow{Slug: analyticsSlug, Name: "Pursae Analytics", Version: analyticsVersion, Native: true, Active: s.analyticsOn(),
		Like:        "Plausible / Jetpack Stats",
		Description: "Privacy-friendly site statistics without cookies: visitors, page views, bounce rate, visit duration, sources, UTM campaigns, countries, devices, outbound links, downloads, forms and 404s, plus real-user Core Web Vitals (LCP, INP, CLS) for every page. No IP address is stored and no consent banner is needed. One page shows everything.",
		Author:      "Pursae"}
	if r.Active {
		r.SettingsURL = "/wp-admin/analytics?tab=settings"
	}
	return r
}

// analyticsPluginAction is the Activate / Deactivate link of the Plugins screen.
func (s *Server) analyticsPluginAction(c *gin.Context, action string) {
	var err error
	if action == "activate" {
		err = s.analyticsActivate(c)
	} else {
		err = s.analyticsDeactivate(c)
	}
	if err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+template.URLQueryEscaper(err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
}

// analyticsMenu adds the Analytics menu right under Dashboard while the plugin is active.
func (s *Server) analyticsMenu(menu []*menuTop, u *store.User, screen string) []*menuTop {
	if !s.analyticsOn() || u == nil || !u.Can("manage_options", nil) {
		return menu
	}
	top := &menuTop{Slug: "analytics", Title: "Analytics", ID: "toplevel_page_pursae-analytics", Icon: "dashicons-chart-bar"}
	for i, p := range []struct{ Slug, Title string }{{"analytics", "Overview"}, {"analytics?tab=settings", "Settings"}} {
		ms := &menuSub{Slug: p.Slug, Title: template.HTML(p.Title), First: i == 0, Current: p.Slug == screen}
		top.Current = top.Current || ms.Current
		top.Sub = append(top.Sub, ms)
	}
	at := len(menu)
	for i, m := range menu {
		if m.ID == "menu-dashboard" {
			at = i + 1
			break
		}
	}
	out := append([]*menuTop{}, menu[:at]...)
	out = append(out, top)
	return append(out, menu[at:]...)
}

// analyticsOnly answers 404 while the plugin is off.
func (s *Server) analyticsOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.analyticsOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// registerAnalytics wires the collector and the admin page.
func (s *Server) registerAnalytics(r *gin.Engine, admin *gin.RouterGroup) {
	r.POST(analyticsCollect, s.analyticsOnly(), s.analyticsCollect)
	admin.GET("/analytics", s.analyticsOnly(), s.analyticsAdmin)
	admin.POST("/analytics", s.analyticsOnly(), s.analyticsAdmin)
}

// analyticsFooter is the tracking script for wp_footer: nothing while the plugin is off, on previews
// and feeds, and for signed-in users when they are excluded.
func (s *Server) analyticsFooter(c *gin.Context, q *siteQuery) string {
	if c == nil || !s.analyticsOn() || (q != nil && (q.Preview || q.Kind == "feed")) {
		return ""
	}
	st := s.analyticsLoad()
	if st.ExcludeLoggedIn && currentUser(c) != nil {
		return ""
	}
	cfg := map[string]any{"u": analyticsCollect, "dnt": st.RespectDNT, "o": st.TrackOutbound, "dl": st.TrackDownloads,
		"f": st.TrackForms, "nf": st.Track404 && q != nil && q.Kind == "404", "v": st.Vitals, "vs": st.VitalsSample}
	raw, err := json.Marshal(cfg) // encoding/json escapes <, > and &: safe inside a script element
	if err != nil {
		return ""
	}
	return `<script id="pursae-analytics-js">` + strings.Replace(analyticsScript, "__CONFIG__", string(raw), 1) + "</script>\n"
}

// analyticsScript is the browser side, kept small and dependency-free. It stores nothing in the
// browser; one random number ties a view's later measurements (visible time, Web Vitals) to it.
const analyticsScript = `(function(w,d,n){var C=__CONFIG__,L=location;` +
	`if(C.dnt&&(n.doNotTrack=="1"||n.globalPrivacyControl))return;` +
	`if(n.webdriver||/bot|crawl|spider|headless|lighthouse/i.test(n.userAgent))return;` +
	`var id=Math.floor(Math.random()*9e15)+1,vis=0,t0=Date.now(),on=d.visibilityState!="hidden",m={};` +
	`function send(o){o.id=id;o.p=L.pathname+L.search;var b=JSON.stringify(o);try{if(n.sendBeacon&&n.sendBeacon(C.u,b))return}catch(e){}` +
	`try{fetch(C.u,{method:"POST",body:b,keepalive:true,credentials:"same-origin"})}catch(e){}}` +
	`send({t:"v",r:d.referrer,w:w.innerWidth});if(C.nf)send({t:"e",n:"404",g:L.pathname});` +
	`function tick(){var x=Date.now();if(on)vis+=x-t0;t0=x}` +
	`function leave(){tick();var o={t:"x",d:vis};for(var k in m)o[k]=m[k];send(o)}` +
	`d.addEventListener("visibilitychange",function(){tick();on=d.visibilityState!="hidden";if(!on)leave()});w.addEventListener("pagehide",leave);` +
	`if(C.v&&w.PerformanceObserver&&Math.random()*100<C.vs){var ob=function(t,f,x){try{var o={type:t,buffered:true};for(var k in x||{})o[k]=x[k];new PerformanceObserver(function(l){l.getEntries().forEach(f)}).observe(o)}catch(e){}};` +
	`ob("largest-contentful-paint",function(e){m.lcp=Math.round(e.startTime)});` +
	`ob("paint",function(e){if(e.name=="first-contentful-paint")m.fcp=Math.round(e.startTime)});` +
	`var cs=0,ss=0,fi=0,la=0;ob("layout-shift",function(e){if(e.hadRecentInput)return;if(ss&&e.startTime-la<1000&&e.startTime-fi<5000)ss+=e.value;else{ss=e.value;fi=e.startTime}la=e.startTime;if(ss>cs){cs=ss;m.cls=Math.round(cs*1e4)/1e4}});` +
	`ob("event",function(e){if(e.interactionId){var x=Math.round(e.duration);if(!(m.inp>=x))m.inp=x}},{durationThreshold:16});` +
	`try{var nv=performance.getEntriesByType("navigation")[0];if(nv)m.ttfb=Math.round(nv.responseStart)}catch(e){}}` +
	`d.addEventListener("click",function(e){var a=e.target.closest&&e.target.closest("a[href]");if(!a)return;var u;try{u=new URL(a.href)}catch(x){return}` +
	`if(!/^https?:$/.test(u.protocol))return;if(C.dl&&/\.(pdf|zip|rar|7z|gz|docx?|xlsx?|pptx?|odt|ods|csv|txt|rtf|epub|mp3|wav|mp4|mov|avi|dmg|exe|msi|apk)$/i.test(u.pathname))send({t:"e",n:"download",g:u.href});` +
	`else if(C.o&&u.host!=L.host)send({t:"e",n:"outbound",g:u.href})},true);` +
	`if(C.f)d.addEventListener("submit",function(e){var f=e.target;send({t:"e",n:"form",g:f.getAttribute("id")||f.getAttribute("name")||f.getAttribute("action")||""})},true)` +
	`})(window,document,navigator);`

// analyticsMenuScreen is the menu slug of the current Analytics screen.
func analyticsMenuScreen(tab string) string {
	if tab == "settings" {
		return "analytics?tab=settings"
	}
	return "analytics"
}

func analyticsAtoi(v string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return n
	}
	return def
}
