package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wpgo/internal/config"
)

// testdata/geoip-country-test.mmdb maps 81.214.0.0/16 → TR, 8.8.8.0/24 → US, 2a02:2e0::/32 → DE and,
// to prove private addresses are never looked up, 10.0.0.0/8 → ZZ.

func geoipTestServer(t *testing.T, withDB bool) *Server {
	t.Helper()
	dir := t.TempDir()
	oldSeed := geoipSeedPath
	geoipSeedPath = filepath.Join(dir, "no-seed.mmdb")
	t.Cleanup(func() { geoipSeedPath = oldSeed })
	s := &Server{cfg: config.Config{UploadsDir: filepath.Join(dir, "uploads")}}
	if withDB {
		b, err := os.ReadFile("testdata/geoip-country-test.mmdb")
		if err != nil {
			t.Fatal(err)
		}
		if err := geoipInstall(s.geoipDBPath(), b); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func geoipReq(remote string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/wpgo-analytics/collect", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestVisitorCountryHeaderWins(t *testing.T) {
	s := geoipTestServer(t, true)
	if got := s.visitorCountry(geoipReq("81.214.1.1:4000", map[string]string{"CF-IPCountry": "fr"}), "81.214.1.1"); got != "FR" {
		t.Errorf("CF-IPCountry must win over the database: %q", got)
	}
	if got := s.visitorCountry(geoipReq("81.214.1.1:4000", map[string]string{"Fastly-Geo-Country": "NL"}), ""); got != "NL" {
		t.Errorf("Fastly header: %q", got)
	}
	// XX (unknown) and T1 (Tor) are no country: fall through to the database.
	for _, v := range []string{"XX", "T1", "turkey", ""} {
		if got := s.visitorCountry(geoipReq("81.214.1.1:4000", map[string]string{"CF-IPCountry": v}), ""); got != "TR" {
			t.Errorf("header %q: got %q, want the database's TR", v, got)
		}
	}
}

func TestVisitorCountryFromDatabase(t *testing.T) {
	s := geoipTestServer(t, true)
	cases := map[string]string{
		"81.214.10.20":        "TR",
		"8.8.8.8":             "US",
		"2a02:2e0:1::5":       "DE",
		"::ffff:81.214.10.20": "TR", // IPv4-mapped
		"1.1.1.1":             "",   // not in the test database
		"not an ip":           "",
		"":                    "",
	}
	for ip, want := range cases {
		if got := s.visitorCountry(geoipReq("127.0.0.1:1", nil), ip); got != want {
			t.Errorf("%q: got %q, want %q", ip, got, want)
		}
	}
	// Without an explicit address the request's own peer is used.
	if got := s.visitorCountry(geoipReq("8.8.8.8:55555", nil), ""); got != "US" {
		t.Errorf("peer address: %q", got)
	}
}

func TestVisitorCountryPrivateAddresses(t *testing.T) {
	s := geoipTestServer(t, true)
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.16.5.4", "172.31.255.1", "192.168.1.10", "::1", "fc00::1", "fd12:3456::1", "fe80::1", "169.254.1.1", "0.0.0.0", "::"} {
		if got := s.visitorCountry(geoipReq("127.0.0.1:1", nil), ip); got != "" {
			t.Errorf("%s: got %q, private and local addresses have no country", ip, got)
		}
	}
}

func TestVisitorCountryNoDatabase(t *testing.T) {
	s := geoipTestServer(t, false)
	if got := s.visitorCountry(geoipReq("81.214.1.1:4000", nil), "81.214.1.1"); got != "" {
		t.Errorf("no database: %q", got)
	}
	if got := s.visitorCountry(geoipReq("81.214.1.1:4000", map[string]string{"CloudFront-Viewer-Country": "TR"}), ""); got != "TR" {
		t.Errorf("headers still work without a database: %q", got)
	}
	if s.geoipStatus().Installed {
		t.Error("status says installed")
	}
}

func TestVisitorCountryBrokenFiles(t *testing.T) {
	good, err := os.ReadFile("testdata/geoip-country-test.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		"garbage":   []byte("this is not a MaxMind database"),
		"truncated": good[:len(good)/2],
		"empty":     {},
		"no-data":   append(append([]byte{}, good[:200]...), good[len(good)-300:]...),
	} {
		t.Run(name, func(t *testing.T) {
			s := geoipTestServer(t, false)
			if err := os.MkdirAll(filepath.Dir(s.geoipDBPath()), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.geoipDBPath(), b, 0o644); err != nil {
				t.Fatal(err)
			}
			if got := s.visitorCountry(geoipReq("81.214.1.1:4000", nil), ""); got != "" {
				t.Errorf("damaged file answered %q", got)
			}
		})
	}
	// A directory where the file should be is ignored too.
	s := geoipTestServer(t, false)
	_ = os.MkdirAll(s.geoipDBPath(), 0o755)
	if got := s.visitorCountry(geoipReq("81.214.1.1:4000", nil), ""); got != "" {
		t.Errorf("directory answered %q", got)
	}
}

func TestGeoipSeedAndReload(t *testing.T) {
	s := geoipTestServer(t, false)
	b, _ := os.ReadFile("testdata/geoip-country-test.mmdb")
	geoipSeedPath = filepath.Join(t.TempDir(), "seed.mmdb")
	if err := os.WriteFile(geoipSeedPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.geoipLookup("8.8.8.8"); got != "US" {
		t.Fatalf("seed database: %q", got)
	}
	if st := s.geoipStatus(); !st.Installed || !st.Seed {
		t.Errorf("status: %+v", st)
	}
	// A downloaded file in the data directory takes over once the files are checked again.
	if err := geoipInstall(s.geoipDBPath(), b); err != nil {
		t.Fatal(err)
	}
	s.geo.checked.Store(0)
	if st := s.geoipStatus(); !st.Installed || st.Seed {
		t.Errorf("data file not preferred: %+v", st)
	}
	// Replacing it with garbage keeps the database already in memory.
	_ = os.WriteFile(s.geoipDBPath(), []byte("broken"), 0o644)
	_ = os.Remove(geoipSeedPath)
	s.geo.checked.Store(0)
	if got := s.geoipLookup("8.8.8.8"); got != "US" {
		t.Errorf("a damaged replacement dropped the working database: %q", got)
	}
}

func TestGeoipUpdate(t *testing.T) {
	good, _ := os.ReadFile("testdata/geoip-country-test.mmdb")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(good)
	_ = zw.Close()
	now := time.Now().UTC()
	thisMonth := now.Format("2006-01")
	prevMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")

	var served []string
	mode := "prev-only"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = append(served, r.URL.Path)
		switch {
		case mode == "broken":
			_, _ = w.Write([]byte("not gzip"))
		case mode == "down":
			w.WriteHeader(http.StatusBadGateway)
		case strings.Contains(r.URL.Path, prevMonth) || mode == "all":
			_, _ = w.Write(gz.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	oldURL := geoipDownloadURL
	geoipDownloadURL = func(m string) string { return ts.URL + "/dbip-country-lite-" + m + ".mmdb.gz" }
	defer func() { geoipDownloadURL = oldURL }()

	s := geoipTestServer(t, false)
	ctx := context.Background()
	// This month's file is not out yet: last month's is installed.
	month, err := s.geoipUpdate(ctx, false)
	if err != nil || month != prevMonth {
		t.Fatalf("update: %q %v (served %v)", month, err, served)
	}
	if got := s.geoipLookup("81.214.1.1"); got != "TR" {
		t.Errorf("after update: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(s.geoipDBPath()))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	// A broken or failing download leaves the installed database alone and never panics.
	for _, m := range []string{"broken", "down"} {
		mode = m
		if _, err := s.geoipUpdate(ctx, true); err == nil {
			t.Errorf("%s download reported success", m)
		}
		s.geo.checked.Store(0)
		if got := s.geoipLookup("81.214.1.1"); got != "TR" {
			t.Errorf("after a %s download: %q", m, got)
		}
	}
	// The scheduled job swallows errors and is off with WPGO_GEOIP_UPDATE=off.
	if err := s.geoipUpdateJob(ctx, nil); err != nil {
		t.Errorf("job: %v", err)
	}
	mode, served = "all", nil
	s.cfg.GeoIPUpdateOff = true
	_ = s.geoipUpdateJob(ctx, nil)
	if len(served) != 0 {
		t.Errorf("job downloaded with updates off: %v", served)
	}
	s.cfg.GeoIPUpdateOff = false
	if month, err := s.geoipUpdate(ctx, true); err != nil || month != thisMonth {
		t.Errorf("this month: %q %v", month, err)
	}
	// The test file's build date is today, so a non-forced update now finds it current.
	if _, err := s.geoipUpdate(ctx, false); err != errGeoipCurrent {
		t.Errorf("current database: %v", err)
	}
}

func TestGeoipDBPath(t *testing.T) {
	s := &Server{cfg: config.Config{UploadsDir: "/data/uploads"}}
	if p := s.geoipDBPath(); p != "/data/geoip/country.mmdb" {
		t.Errorf("default path: %s", p)
	}
	s.cfg.GeoIPDB = "/srv/geo/c.mmdb"
	if p := s.geoipDBPath(); p != "/srv/geo/c.mmdb" {
		t.Errorf("WPGO_GEOIP_DB: %s", p)
	}
}

// TestAnalyticsCountryWithoutProxyHeader: with no country header (Cloudflare in DNS-only mode) the
// collector still stores the country, from the IP database and the forwarded visitor address; a
// CF-IPCountry header still wins; the settings screen shows the database and the DB-IP credit.
func TestAnalyticsCountryWithoutProxyHeader(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	oldSeed := geoipSeedPath
	geoipSeedPath = ""
	defer func() { geoipSeedPath = oldSeed }()
	b, err := os.ReadFile("testdata/geoip-country-test.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	if err := geoipInstall(srv.geoipDBPath(), b); err != nil {
		t.Fatal(err)
	}
	srv.geo.checked.Store(0)
	if err := srv.analyticsActivate(ctx); err != nil {
		t.Fatal(err)
	}
	// The test server's peer is 127.0.0.1, a trusted proxy by default, so X-Forwarded-For is the visitor.
	beacon(t, ts.URL, map[string]any{"t": "v", "id": 501, "p": "/", "w": 1200}, map[string]string{"X-Forwarded-For": "81.214.7.7"})
	beacon(t, ts.URL, map[string]any{"t": "v", "id": 502, "p": "/", "w": 1200}, map[string]string{"X-Forwarded-For": "81.214.7.8", "CF-IPCountry": "DE"})
	beacon(t, ts.URL, map[string]any{"t": "v", "id": 503, "p": "/", "w": 1200}, map[string]string{"X-Forwarded-For": "192.168.1.9"})
	got := map[int64]string{}
	rows, err := srv.store.Pool.Query(ctx, `SELECT page_id, country FROM analytics_events WHERE name = 'pageview'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var cc string
		_ = rows.Scan(&id, &cc)
		got[id] = cc
	}
	rows.Close()
	if got[501] != "TR" || got[502] != "DE" || got[503] != "" {
		t.Errorf("countries: %v (want 501 TR from the database, 502 DE from CF-IPCountry, 503 none)", got)
	}
	_, body, _ := get(t, c, ts.URL+"/wp-admin/analytics?tab=settings")
	for _, want := range []string{"IP geolocation by DB-IP (db-ip.com)", "Update country database", "Installed"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings screen lacks %q", want)
		}
	}
}
