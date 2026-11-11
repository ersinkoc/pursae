package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAnalyticsUA(t *testing.T) {
	for _, c := range []struct {
		ua                string
		w                 int
		dev, browser, osn string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36", 1440, "Desktop", "Chrome", "Windows"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1", 390, "Mobile", "Safari", "iOS"},
		{"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0 Mobile Safari/537.36", 412, "Mobile", "Samsung Internet", "Android"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", 1512, "Desktop", "Safari", "macOS"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Edg/130.0", 1280, "Desktop", "Edge", "Windows"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", 1920, "Desktop", "Firefox", "Linux"},
		{"Mozilla/5.0 (Linux; Android 13; Tab) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36", 800, "Tablet", "Chrome", "Android"},
	} {
		d, b, o := analyticsUA(c.ua, c.w)
		if d != c.dev || b != c.browser || o != c.osn {
			t.Errorf("%s: %s %s %s", c.ua[:40], d, b, o)
		}
	}
}

func TestAnalyticsSourceAndHelpers(t *testing.T) {
	for in, want := range map[string]string{"www.google.com.tr": "Google", "news.google.com": "Google", "l.facebook.com": "Facebook",
		"t.co": "X (Twitter)", "chatgpt.com": "ChatGPT", "www.example.org": "example.org", "com.google.android.gm": "Google"} {
		if got := analyticsSourceName(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	if !analyticsIPExcluded("203.0.113.7\n198.51.100.0/24", "198.51.100.42") || analyticsIPExcluded("203.0.113.7", "203.0.113.8") {
		t.Error("IP exclusion")
	}
	if analyticsNum(1234567) != "1.2M" {
		t.Errorf("num: %s", analyticsNum(1234567))
	}
	if analyticsNum(12345) != "12,345" || analyticsNum(250_000) != "250k" {
		t.Errorf("num: %s %s", analyticsNum(12345), analyticsNum(250_000))
	}
	if analyticsFlag("TR") != "🇹🇷" {
		t.Errorf("flag: %q", analyticsFlag("TR"))
	}
	if analyticsRate(2400, 2500, 4000) != "good" || analyticsRate(3000, 2500, 4000) != "needs-improvement" || analyticsRate(5000, 2500, 4000) != "poor" {
		t.Error("rating")
	}
	loc := time.FixedZone("TRT", 3*3600)
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, loc)
	r := analyticsRangeFor("7d", now, loc)
	if !r.From.Equal(time.Date(2026, 11, 5, 0, 0, 0, 0, loc)) || !r.To.Equal(time.Date(2026, 11, 12, 0, 0, 0, 0, loc)) {
		t.Errorf("7d: %v – %v", r.From, r.To)
	}
	if r := analyticsRangeFor("bogus", now, loc); r.Key != "30d" {
		t.Errorf("default range: %s", r.Key)
	}
	if !analyticsLimit.allow("test-key", time.Now()) {
		t.Error("limiter refused the first request")
	}
}

// beacon posts one analytics hit like navigator.sendBeacon (text/plain, no cookies).
func beacon(t *testing.T, base string, body map[string]any, hdr map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, base+analyticsCollect, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("collect: %d", resp.StatusCode)
	}
}

// TestAnalyticsFlow: off by default (no script, collector 404), activated from the Plugins screen,
// the page carries the script for visitors but not for the signed-in admin, beacons become a
// view with an event and Web Vitals, bots and Do Not Track are dropped, and the overview, the
// settings and the REST summary show it.
func TestAnalyticsFlow(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()

	if code, _, _ := post(t, &http.Client{}, base+analyticsCollect, url.Values{}); code != http.StatusNotFound {
		t.Errorf("collector while off: %d", code)
	}
	_, plugins, _ := get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(plugins, "Pursae Analytics") {
		t.Fatal("no Pursae Analytics row on the Plugins screen")
	}
	if err := srv.analyticsActivate(ctx); err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	visitor := &http.Client{Jar: jar}
	if _, home, _ := get(t, visitor, base+"/"); !strings.Contains(home, `id="pursae-analytics-js"`) || !strings.Contains(home, `"u":"/wpgo-analytics/collect"`) {
		t.Fatal("no analytics script on the front page")
	}
	if _, home, _ := get(t, c, base+"/"); strings.Contains(home, "pursae-analytics-js") {
		t.Error("the script is printed for the signed-in admin")
	}

	id := float64(4242424242)
	beacon(t, base, map[string]any{"t": "v", "id": id, "p": "/hello-world/?utm_source=Newsletter&utm_medium=email&utm_campaign=launch", "r": "https://www.google.com/", "w": 390}, map[string]string{"CF-IPCountry": "TR"})
	beacon(t, base, map[string]any{"t": "e", "id": id, "p": "/hello-world/", "n": "outbound", "g": "https://example.org/x"}, nil)
	beacon(t, base, map[string]any{"t": "x", "id": id, "p": "/hello-world/", "d": 12000, "lcp": 1800, "inp": 120, "cls": 0.02, "ttfb": 300, "fcp": 900}, nil)
	beacon(t, base, map[string]any{"t": "v", "id": 77, "p": "/", "w": 1200}, map[string]string{"User-Agent": "Googlebot/2.1"})
	beacon(t, base, map[string]any{"t": "v", "id": 78, "p": "/", "w": 1200}, map[string]string{"DNT": "1"})
	beacon(t, base, map[string]any{"t": "v", "id": 79, "p": "/wp-admin/", "w": 1200}, nil)

	var n, engaged, lcp int
	var src, utm, country, device, browser string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("events: %d %v (bots, DNT and admin paths must be dropped)", n, err)
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT engaged_ms, lcp_ms, referrer, utm_source, country, device, browser FROM analytics_events WHERE name = 'pageview'`).
		Scan(&engaged, &lcp, &src, &utm, &country, &device, &browser)
	if engaged != 12000 || lcp != 1800 || src != "Google" || utm != "newsletter" || country != "TR" || device != "Mobile" || browser != "Safari" {
		t.Errorf("view: %d %d %s %s %s %s %s", engaged, lcp, src, utm, country, device, browser)
	}
	var ipStored int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'analytics_events' AND column_name ILIKE '%ip%'`).Scan(&ipStored)
	if ipStored != 0 {
		t.Error("an IP column exists")
	}

	_, page, _ := get(t, c, base+"/wp-admin/analytics?range=today")
	for _, want := range []string{"Unique visitors", "pa-chart-svg", "/hello-world/", "Google", "Türkiye", "Outbound link clicks", "launch", "Largest Contentful Paint", "1.8 s", "Good"} {
		if !strings.Contains(page, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if !strings.Contains(page, `toplevel_page_pursae-analytics`) {
		t.Error("no Analytics menu")
	}
	_, set, _ := get(t, c, base+"/wp-admin/analytics?tab=settings")
	if code, _, loc := post(t, c, base+"/wp-admin/analytics?tab=settings", url.Values{"_wpnonce": {formNonce(t, set)}, "retention_days": {"90"}, "vitals": {"1"}, "vitals_sample": {"50"}}); code != http.StatusFound || !strings.Contains(loc, "saved=1") {
		t.Fatalf("save settings: %d %s", code, loc)
	}
	if st := srv.analyticsLoad(); st.RetentionDays != 90 || st.VitalsSample != 50 || st.ExcludeLoggedIn || st.TrackOutbound {
		t.Errorf("settings: %+v", st)
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/wp-json/pursae/v1/analytics?range=today", nil)
	req.Header.Set("X-WP-Nonce", restNonce(t, srv, c, base))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || out["summary"].(map[string]any)["Page views"] != "1" {
		t.Errorf("REST summary: %d %v", resp.StatusCode, out["summary"])
	}

	// Deactivated: no script, collector 404; the data stays.
	if err := srv.analyticsDeactivate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, home, _ := get(t, visitor, base+"/"); strings.Contains(home, "pursae-analytics-js") {
		t.Error("script printed while off")
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&n)
	if n != 2 {
		t.Errorf("data lost on deactivation: %d", n)
	}
}
