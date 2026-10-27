package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/plugins"
)

// The WordPress Go Hub is the api.wordpress.org of WordPress Go: it lists the plugin and theme
// marketplace, announces core releases and counts the sites that run WordPress Go. The contract is
// wpgo-admin/API.md. A site sends it its URL host and core version, nothing else, once a day
// (wpgo_hub_ping, like WordPress' update checks), reads the catalog for Plugins › Add Plugin and
// Appearance › Add Theme, and trusts the Hub's signing keys so published packages install.

const (
	hubCacheTTL   = 12 * time.Hour
	hubMaxPackage = 64 << 20
	hubUserAgent  = "WordPress Go/" + Version
)

type hubIndex struct {
	Hub    string         `json:"hub"`
	API    int            `json:"api"`
	Core   *hubCore       `json:"core"`
	Keys   []hubKey       `json:"keys"`
	Counts map[string]int `json:"counts"`
}

type hubCore struct {
	Latest      string `json:"latest"`
	Released    string `json:"released"`
	DownloadURL string `json:"download_url"`
	NotesURL    string `json:"notes_url"`
	Notes       string `json:"notes"`
}

type hubKey struct {
	ID     string `json:"id"`
	Public string `json:"public"`
}

// hubItem is one plugin or theme listing.
type hubItem struct {
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Description   string   `json:"description"`
	Author        string   `json:"author"`
	AuthorURL     string   `json:"author_url"`
	Homepage      string   `json:"homepage"`
	IconURL       string   `json:"icon_url"`
	ScreenshotURL string   `json:"screenshot_url"`
	RequiresCore  string   `json:"requires_core"`
	TestedCore    string   `json:"tested_core"`
	Permissions   []string `json:"permissions"`
	Tags          []string `json:"tags"`
	Downloads     int      `json:"downloads"`
	Updated       string   `json:"updated"`
	Size          int64    `json:"size"`
	SHA256        string   `json:"sha256"`
	SignedBy      string   `json:"signed_by"`
	DownloadURL   string   `json:"download_url"`
}

type hubList struct {
	Total int       `json:"total"`
	Items []hubItem `json:"items"`
}

// hubBase is the Hub's URL: the wpgo_hub_url setting, else WPGO_HUB_URL. "off" (or empty) disables it.
func (s *Server) hubBase() string {
	u := strings.TrimSpace(s.settings.String("wpgo_hub_url"))
	if u == "" {
		u = s.cfg.HubURL
	}
	switch strings.ToLower(u) {
	case "", "off", "none", "0", "false":
		return ""
	}
	return strings.TrimRight(u, "/")
}

var errHubOff = errors.New("the WordPress Go Hub is switched off (WPGO_HUB_URL)")

func (s *Server) hubClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

// hubGet fetches a JSON document from the Hub.
func (s *Server) hubGet(ctx context.Context, path string, out any) error {
	base := s.hubBase()
	if base == "" {
		return errHubOff
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", hubUserAgent+"; "+s.settings.String("home"))
	req.Header.Set("Accept", "application/json")
	resp, err := s.hubClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hub: %s: HTTP %d", path, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

// hubCacheGet reads a cached Hub document from the transients table.
func (s *Server) hubCacheGet(ctx context.Context, key string, out any) bool {
	var raw []byte
	err := s.store.Pool.QueryRow(ctx, `SELECT value FROM transients WHERE key = $1 AND (expires_at IS NULL OR expires_at > now())`, key).Scan(&raw)
	return err == nil && json.Unmarshal(raw, out) == nil
}

func (s *Server) hubCacheSet(ctx context.Context, key string, v any, ttl time.Duration) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = s.store.Pool.Exec(ctx, `INSERT INTO transients(key, value, expires_at) VALUES ($1, $2, now() + $3::interval)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at, updated_at = now()`,
		key, raw, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
}

// hubIndexCached is the Hub index (latest core release, signing keys), cached for 12 hours.
func (s *Server) hubIndexCached(ctx context.Context) (*hubIndex, error) {
	var idx hubIndex
	if s.hubCacheGet(ctx, "wpgo_hub_index", &idx) && idx.API > 0 {
		s.hubTrustKeys(idx.Keys)
		return &idx, nil
	}
	return s.hubFetchIndex(ctx)
}

// hubTrustCached trusts the keys of an already cached index without going to the network (start-up).
func (s *Server) hubTrustCached(ctx context.Context) {
	var idx hubIndex
	if s.hubCacheGet(ctx, "wpgo_hub_index", &idx) {
		s.hubTrustKeys(idx.Keys)
	}
}

func (s *Server) hubFetchIndex(ctx context.Context) (*hubIndex, error) {
	var idx hubIndex
	if err := s.hubGet(ctx, "/api/v1/index", &idx); err != nil {
		return nil, err
	}
	s.hubCacheSet(ctx, "wpgo_hub_index", idx, hubCacheTTL)
	s.hubTrustKeys(idx.Keys)
	return &idx, nil
}

// hubTrustKeys adds the Hub's signing keys to the trusted plugin keys (wpgo_hub_trust_keys).
func (s *Server) hubTrustKeys(keys []hubKey) {
	if s.plugins == nil || !s.settings.Bool("wpgo_hub_trust_keys") {
		return
	}
	for _, k := range keys {
		pub, err := plugins.ParsePublicKey(k.Public)
		if err != nil {
			continue
		}
		known := false
		for _, have := range s.plugins.keys {
			if bytes.Equal(have, pub) {
				known = true
				break
			}
		}
		if !known {
			s.plugins.keys = append(s.plugins.keys, pub)
		}
	}
}

// hubCatalog lists the Hub's plugins or themes (kind "plugins" | "themes"), cached for 12 hours.
func (s *Server) hubCatalog(ctx context.Context, kind string) ([]hubItem, error) {
	var list hubList
	if s.hubCacheGet(ctx, "wpgo_hub_"+kind, &list) {
		return list.Items, nil
	}
	if err := s.hubGet(ctx, "/api/v1/"+kind+"?per_page=100&sort=downloads", &list); err != nil {
		return nil, err
	}
	s.hubCacheSet(ctx, "wpgo_hub_"+kind, list, hubCacheTTL)
	return list.Items, nil
}

// hubItemFor is one listing with its download details.
func (s *Server) hubItemFor(ctx context.Context, kind, slug string) (*hubItem, error) {
	if !validSlug(slug) {
		return nil, errors.New("invalid slug")
	}
	var it hubItem
	if err := s.hubGet(ctx, "/api/v1/"+kind+"/"+slug, &it); err != nil {
		return nil, err
	}
	return &it, nil
}

func validSlug(slug string) bool {
	if len(slug) < 2 || len(slug) > 64 {
		return false
	}
	for i, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(slug)-1:
		default:
			return false
		}
	}
	return true
}

// hubDownload fetches a package from the Hub and checks its SHA-256 against the listing.
func (s *Server) hubDownload(ctx context.Context, it *hubItem) ([]byte, error) {
	base := s.hubBase()
	if base == "" {
		return nil, errHubOff
	}
	u := it.DownloadURL
	if u == "" || !strings.HasPrefix(u, base+"/") {
		// Only the Hub itself may serve packages: no third-party download URLs.
		return nil, fmt.Errorf("hub: %s has no download on the Hub", it.Slug)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hubUserAgent+"; "+s.settings.String("home"))
	resp, err := s.hubClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub: download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, hubMaxPackage+1))
	if err != nil {
		return nil, err
	}
	if len(data) > hubMaxPackage {
		return nil, errors.New("hub: the package is larger than 64 MiB")
	}
	sum := sha256.Sum256(data)
	if it.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(sum[:]), it.SHA256) {
		return nil, errors.New("hub: the downloaded package does not match the published checksum")
	}
	return data, nil
}

// hubInstallPlugin installs a plugin from the marketplace: download, checksum, signature, install.
func (s *Server) hubInstallPlugin(ctx context.Context, slug string) (*plugins.Manifest, error) {
	if _, err := s.hubIndexCached(ctx); err != nil { // the Hub's signing keys
		return nil, err
	}
	it, err := s.hubItemFor(ctx, "plugins", slug)
	if err != nil {
		return nil, err
	}
	data, err := s.hubDownload(ctx, it)
	if err != nil {
		return nil, err
	}
	return s.installPackage(ctx, data)
}

// hubPing tells the Hub this site exists (URL host and version only) and refreshes the cached index.
// It is the daily wpgo_hub_check event; WordPress does the same against api.wordpress.org.
func (s *Server) hubPing(ctx context.Context) error {
	base := s.hubBase()
	if base == "" {
		return nil
	}
	if s.settings.Bool("wpgo_hub_ping") {
		body, _ := json.Marshal(map[string]string{"url": s.settings.String("home"), "version": Version})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/sites/ping", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", hubUserAgent)
		resp, err := s.hubClient().Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
			return fmt.Errorf("hub: ping: HTTP %d", resp.StatusCode)
		}
	}
	_, err := s.hubFetchIndex(ctx)
	return err
}

// hubRefresh drops the cached catalog and index (Updates › Check again).
func (s *Server) hubRefresh(ctx context.Context) {
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM transients WHERE key IN ('wpgo_hub_index', 'wpgo_hub_plugins', 'wpgo_hub_themes')`)
	if _, err := s.hubFetchIndex(ctx); err != nil && !errors.Is(err, errHubOff) {
		slog.Warn("hub: refresh", "err", err)
	}
}

// hubUpdate reports a newer core release than the running one, if the Hub announced one.
func (s *Server) hubUpdate(ctx context.Context) *hubCore {
	idx, err := s.hubIndexCached(ctx)
	if err != nil || idx.Core == nil || idx.Core.Latest == "" {
		return nil
	}
	if versionLess(Version, idx.Core.Latest) {
		return idx.Core
	}
	return nil
}

// versionLess reports whether a < b comparing major.minor.patch (pre-release suffixes ignored).
func versionLess(a, b string) bool {
	parts := func(v string) [3]int {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			v = v[:i]
		}
		var out [3]int
		for i, p := range strings.SplitN(v, ".", 3) {
			out[i], _ = strconv.Atoi(p)
		}
		return out
	}
	pa, pb := parts(a), parts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}
