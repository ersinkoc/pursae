package server

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Visitor country: the header a CDN or proxy adds wins (Cloudflare CF-IPCountry and the like); without
// one the visitor address (clientIP / requestIP, which honour WPGO_TRUSTED_PROXIES) is looked up in a
// local IP-to-country database. The address is only looked up in memory, never written anywhere; only the
// two-letter country code leaves this file.
//
// The database is DB-IP "IP to Country Lite" (MMDB, CC BY 4.0, https://db-ip.com): no license key and it
// may be redistributed, so the Docker image ships a copy (geoipSeedPath) and the site downloads a fresh
// one every month into the data directory (geoipDBPath, WPGO_GEOIP_DB). A MaxMind GeoLite2 Country file
// placed at the same path works too: both use the same record layout. Without any file only the headers
// count, as before.

// geoipCountryHeaders are the country headers of CDNs and proxies, in order of preference.
var geoipCountryHeaders = []string{"CF-IPCountry", "CloudFront-Viewer-Country", "X-Vercel-IP-Country", "Fastly-Geo-Country", "X-Country-Code", "X-Geo-Country"}

// geoipSeedPath is the database baked into the Docker image, used until the data directory has one.
var geoipSeedPath = "/usr/share/wpgo/geoip/country.mmdb"

// geoipDownloadURL is DB-IP's monthly file for a month ("2006-01").
var geoipDownloadURL = func(month string) string {
	return "https://download.db-ip.com/free/dbip-country-lite-" + month + ".mmdb.gz"
}

const (
	geoipRecheck  = time.Minute // how often the files are stat'ed for a change
	geoipMaxGzip  = 64 << 20    // download limit
	geoipMaxMMDB  = 256 << 20   // uncompressed limit
	geoipAttrib   = "IP geolocation by DB-IP (db-ip.com)"
	geoipAttribTo = "https://db-ip.com"
)

// geoipDB is an opened database file, held in memory (not mmap'ed, so replacing the file is safe).
type geoipDB struct {
	r     *maxminddb.Reader
	path  string
	mod   time.Time
	size  int64
	built time.Time
	kind  string // metadata database type, e.g. "DBIP-Country-Lite"
}

// geoipState is the server's database: the opened file and when the files were last checked.
type geoipState struct {
	db       atomic.Pointer[geoipDB]
	mu       sync.Mutex // one reload at a time
	checked  atomic.Int64
	failed   string // the file that failed to open last (logged once)
	updating sync.Mutex
}

func (s *Server) geoip() *geoipState { return &s.geo }

// geoipDBPath is where the downloaded database lives: WPGO_GEOIP_DB, or geoip/country.mmdb next to the
// uploads (in Docker /data/geoip/country.mmdb, on the data volume).
func (s *Server) geoipDBPath() string {
	if p := strings.TrimSpace(s.cfg.GeoIPDB); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(filepath.Clean(s.cfg.UploadsDir)), "geoip", "country.mmdb")
}

// visitorCountry is the visitor's two-letter country code: a CDN or proxy header first, then the local
// database for ip (the visitor address; empty means the request's). "" when neither knows, and for
// private, loopback and other non-public addresses.
func (s *Server) visitorCountry(r *http.Request, ip string) string {
	if r != nil {
		for _, k := range geoipCountryHeaders {
			if v := strings.ToUpper(strings.TrimSpace(r.Header.Get(k))); geoipValidCode(v) {
				return v
			}
		}
		if ip == "" {
			ip = s.requestIP(r)
		}
	}
	return s.geoipLookup(ip)
}

// geoipValidCode accepts an ISO 3166 alpha-2 code; XX (unknown) and T1 (Tor) are not countries.
func geoipValidCode(v string) bool {
	return len(v) == 2 && v[0] >= 'A' && v[0] <= 'Z' && v[1] >= 'A' && v[1] <= 'Z' && v != "XX" && v != "T1"
}

// geoipPublic reports whether an address can be located: not private, loopback, link-local, multicast
// or unspecified.
func geoipPublic(a netip.Addr) bool {
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate()
}

// geoipLookup looks ip up in the local database.
func (s *Server) geoipLookup(ip string) (cc string) {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	a = a.Unmap().WithZone("")
	if !geoipPublic(a) {
		return ""
	}
	db := s.geoipCurrent()
	if db == nil {
		return ""
	}
	defer func() {
		if recover() != nil { // a damaged file must never take a request down
			cc = ""
		}
	}()
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := db.r.Lookup(a).Decode(&rec); err != nil {
		return ""
	}
	if v := strings.ToUpper(rec.Country.ISOCode); geoipValidCode(v) {
		return v
	}
	return ""
}

// geoipCurrent is the opened database, reopened when its file changed (checked once a minute). The data
// directory's file wins over the image's seed.
func (s *Server) geoipCurrent() *geoipDB {
	st := s.geoip()
	now := time.Now().UnixNano()
	if last := st.checked.Load(); last != 0 && time.Duration(now-last) < geoipRecheck {
		return st.db.Load()
	}
	if !st.mu.TryLock() { // another request is reloading: use what is there
		return st.db.Load()
	}
	defer st.mu.Unlock()
	st.checked.Store(now)
	s.geoipReload(st)
	return st.db.Load()
}

// geoipReload opens the first database file that exists, unless it is the one already open.
func (s *Server) geoipReload(st *geoipState) {
	cur := st.db.Load()
	for _, p := range []string{s.geoipDBPath(), geoipSeedPath} {
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if cur != nil && cur.path == p && cur.mod.Equal(fi.ModTime()) && cur.size == fi.Size() {
			return
		}
		db, err := geoipOpen(p, fi)
		if err != nil {
			if st.failed != p+fi.ModTime().String() {
				st.failed = p + fi.ModTime().String()
				slog.Warn("geoip: the country database cannot be read; trying the next one", "path", p, "err", err)
			}
			continue
		}
		st.db.Store(db)
		return
	}
	// No usable file: keep a working database already in memory (its file may have been removed by
	// mistake), otherwise headers only.
}

// geoipOpen reads a database file into memory and checks that it answers lookups.
func geoipOpen(path string, fi os.FileInfo) (*geoipDB, error) {
	if fi.Size() > geoipMaxMMDB {
		return nil, errors.New("file too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := geoipCheck(b)
	if err != nil {
		return nil, err
	}
	return &geoipDB{r: r, path: path, mod: fi.ModTime(), size: fi.Size(), built: r.Metadata.BuildTime(), kind: r.Metadata.DatabaseType}, nil
}

// geoipCheck opens an MMDB image and makes sure it is a country database that decodes.
func geoipCheck(b []byte) (r *maxminddb.Reader, err error) {
	defer func() {
		if p := recover(); p != nil {
			r, err = nil, fmt.Errorf("damaged database: %v", p)
		}
	}()
	r, err = maxminddb.OpenBytes(b)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(strings.ToLower(r.Metadata.DatabaseType), "country") {
		return nil, fmt.Errorf("not a country database (%q)", r.Metadata.DatabaseType)
	}
	var probe map[string]any
	if err := r.Lookup(netip.MustParseAddr("8.8.8.8")).Decode(&probe); err != nil {
		return nil, err
	}
	return r, nil
}

// geoipStatus describes the database for the admin screens.
type geoipStatus struct {
	Installed bool
	Seed      bool // the copy shipped with the image, not a downloaded one
	Built     string
	Kind      string
	Path      string
	Auto      bool // monthly download switched on
	Attrib    string
	AttribURL string
}

func (s *Server) geoipStatus() geoipStatus {
	st := geoipStatus{Path: s.geoipDBPath(), Auto: !s.cfg.GeoIPUpdateOff, Attrib: geoipAttrib, AttribURL: geoipAttribTo}
	if db := s.geoipCurrent(); db != nil {
		st.Installed, st.Seed, st.Kind = true, db.path == geoipSeedPath, db.kind
		if !db.built.IsZero() {
			st.Built = db.built.UTC().Format("2006-01-02")
		}
	}
	return st
}

// geoipUpdateJob is the daily scheduled event: it downloads this month's file once the month's release is
// out (DB-IP publishes at the start of each month). Failures are logged and never affect the site.
func (s *Server) geoipUpdateJob(ctx context.Context, _ map[string]any) error {
	if s.cfg.GeoIPUpdateOff || s.sandbox != nil {
		return nil
	}
	if _, err := s.geoipUpdate(ctx, false); err != nil {
		slog.Info("geoip: country database not updated", "err", err)
	}
	return nil
}

// errGeoipCurrent says the installed database is already this month's (or newer).
var errGeoipCurrent = errors.New("the country database is already up to date")

// geoipUpdate downloads the newest monthly database into geoipDBPath: tmp file, check, rename. force
// downloads even when the installed one is current. It returns the month installed.
func (s *Server) geoipUpdate(ctx context.Context, force bool) (string, error) {
	st := s.geoip()
	if !st.updating.TryLock() {
		return "", errors.New("an update is already running")
	}
	defer st.updating.Unlock()
	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var have time.Time
	if db := s.geoipCurrent(); db != nil && db.path == s.geoipDBPath() {
		have = db.built
	}
	var lastErr error
	for _, m := range []time.Time{thisMonth, thisMonth.AddDate(0, -1, 0)} {
		if !force && !have.IsZero() && !have.Before(m) {
			return "", errGeoipCurrent
		}
		month := m.Format("2006-01")
		err := s.geoipDownload(ctx, geoipDownloadURL(month))
		if err == nil {
			st.checked.Store(0) // reopen on the next lookup
			s.geoipCurrent()
			return month, nil
		}
		lastErr = err
		if !errors.Is(err, errGeoipNotPublished) {
			break
		}
	}
	return "", lastErr
}

var errGeoipNotPublished = errors.New("not published yet")

// geoipDownload fetches one .mmdb.gz and installs it atomically.
func (s *Server) geoipDownload(ctx context.Context, u string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Pursae/"+Version+" (geoip)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errGeoipNotPublished
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	zr, err := gzip.NewReader(io.LimitReader(resp.Body, geoipMaxGzip))
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(zr, geoipMaxMMDB+1))
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if len(b) > geoipMaxMMDB {
		return errors.New("download: file too large")
	}
	if _, err := geoipCheck(b); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	return geoipInstall(s.geoipDBPath(), b)
}

// geoipInstall writes b next to path and renames it over path, so a reader never sees half a file.
func geoipInstall(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".country-*.mmdb.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after the rename
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o644)
	return os.Rename(tmp, path)
}
