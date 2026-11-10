package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Statistics for Pursae Manager (pursae-manager/API.md, "POST /api/v1/sites/ping (extended)" and
// "POST /api/v1/sites/events"). They are on by default; Settings › License has a "Send usage
// statistics" switch (statsOption). Off, the ping carries only what updates need (site address,
// version, license key) and no events are queued. A site without a Manager (a devhub build with
// WPGO_DEV_HUB_URL=off, or a test) sends nothing.
//
// The daily ping carries the site's URL, version, locale, OS and architecture, license key, active
// theme and its plugin and theme inventory. Events (a plugin installed, a theme previewed, a demo
// imported, the core updated, …) are queued in memory as they happen and sent in batches: after
// hubEventsFlushEvery, as soon as hubEventsBatch are waiting, and on the daily ping. While the Manager
// cannot be reached at most hubEventsBacklog events are kept (the oldest go first); with the Manager
// switched off they are dropped at once. Theme-preview sandboxes never send anything.

const (
	hubEventsFlushEvery = 30 * time.Second
	hubEventsBatch      = 50  // flush at once at this many queued events
	hubEventsPerRequest = 100 // the Manager's limit per request
	hubEventsBacklog    = 500 // kept while the Manager is unreachable
)

// Plugin sources (the plugins.source column and the inventory's "source").
const (
	pluginSourceNative = "native"
	pluginSourceHub    = "hub"
	pluginSourceUpload = "upload"
)

// statsOption is the Settings › License switch: "off" stops inventory and events; anything else is on.
const statsOption = "pursae_send_stats"

// statsEnabled reports whether the site sends usage statistics (the default).
func (s *Server) statsEnabled() bool { return s.settings.String(statsOption) != "off" }

// managerState is the Pursae Manager client's in-memory state of one Server.
type managerState struct {
	license atomic.Pointer[licenseStatus]
	events  hubEventQueue
}

type hubEvent struct {
	Type    string `json:"type"`
	Kind    string `json:"kind"`
	Slug    string `json:"slug"`
	Version string `json:"version"`
	At      string `json:"at"`
}

type hubEventQueue struct {
	mu      sync.Mutex
	list    []hubEvent
	timer   *time.Timer
	flushMu sync.Mutex  // one flush at a time
	batch   atomic.Bool // a full-batch flush is on its way
}

// hubEvent queues a statistics event: typ is one of the contract's types, kind plugin|theme|core.
func (s *Server) hubEvent(typ, kind, slug, version string) {
	if s.sandbox != nil || s.hubBase() == "" || !s.statsEnabled() {
		return
	}
	q := &s.mgr.events
	q.mu.Lock()
	q.list = append(q.list, hubEvent{Type: typ, Kind: kind, Slug: slug, Version: version, At: time.Now().UTC().Format(time.RFC3339)})
	if over := len(q.list) - hubEventsBacklog; over > 0 {
		q.list = append(q.list[:0:0], q.list[over:]...)
	}
	n := len(q.list)
	if q.timer == nil && n < hubEventsBatch {
		q.timer = time.AfterFunc(hubEventsFlushEvery, func() { s.hubFlushEvents(context.Background()) })
	}
	q.mu.Unlock()
	if n >= hubEventsBatch && q.batch.CompareAndSwap(false, true) {
		go func() {
			defer q.batch.Store(false)
			s.hubFlushEvents(context.Background())
		}()
	}
}

// hubQueuedEvents is a copy of the queue (tests, Site Health).
func (s *Server) hubQueuedEvents() []hubEvent {
	q := &s.mgr.events
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]hubEvent(nil), q.list...)
}

// hubFlushEvents sends the queued events to the Manager, at most hubEventsPerRequest per request.
// Events the Manager refused as malformed (4xx) are dropped; on a network error or 429/5xx they stay
// queued for the next flush.
func (s *Server) hubFlushEvents(ctx context.Context) {
	q := &s.mgr.events
	q.flushMu.Lock()
	defer q.flushMu.Unlock()
	q.mu.Lock()
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	q.mu.Unlock()
	base := s.hubBase()
	for {
		q.mu.Lock()
		if base == "" {
			q.list = nil
		}
		n := min(len(q.list), hubEventsPerRequest)
		batch := append([]hubEvent(nil), q.list[:n]...)
		q.mu.Unlock()
		if n == 0 {
			return
		}
		retry, err := s.hubPostEvents(ctx, base, batch)
		if err != nil && retry {
			q.mu.Lock()
			if q.timer == nil && len(q.list) > 0 {
				q.timer = time.AfterFunc(hubEventsFlushEvery, func() { s.hubFlushEvents(context.Background()) })
			}
			q.mu.Unlock()
			return
		}
		q.mu.Lock()
		// Events queued meanwhile were appended; drop the sent ones (unless the backlog cap already did).
		if len(q.list) >= n {
			q.list = append(q.list[:0:0], q.list[n:]...)
		}
		q.mu.Unlock()
	}
}

func (s *Server) hubPostEvents(ctx context.Context, base string, batch []hubEvent) (retry bool, err error) {
	body, _ := json.Marshal(map[string]any{"url": s.settings.String("home"), "version": Version, "events": batch})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := s.hubNewRequest(ctx, http.MethodPost, base+"/api/v1/sites/events", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	resp, err := s.hubClient().Do(req)
	if err != nil {
		return true, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck
	resp.Body.Close()
	switch {
	case resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return true, fmt.Errorf("hub: events: HTTP %d", resp.StatusCode)
	default:
		return false, fmt.Errorf("hub: events: HTTP %d", resp.StatusCode)
	}
}

// Ping -----------------------------------------------------------------------------------------

type hubPackageInfo struct {
	Slug    string `json:"slug"`
	Version string `json:"version"`
	Active  bool   `json:"active"`
	Source  string `json:"source"`
}

type hubPingPayload struct {
	URL     string           `json:"url"`
	Version string           `json:"version"`
	Locale  string           `json:"locale"`
	OS      string           `json:"os"`
	Arch    string           `json:"arch"`
	License string           `json:"license"`
	Theme   string           `json:"theme"`
	Plugins []hubPackageInfo `json:"plugins"`
	Themes  []hubPackageInfo `json:"themes"`
}

// hubPingBody is the daily ping: the site, its platform and license, and what it has installed.
func (s *Server) hubPingBody(ctx context.Context) hubPingPayload {
	if !s.statsEnabled() { // the minimum updates need: where, which version, which license
		return hubPingPayload{URL: s.settings.String("home"), Version: Version, License: s.licenseKey(), Plugins: []hubPackageInfo{}, Themes: []hubPackageInfo{}}
	}
	p := hubPingPayload{URL: s.settings.String("home"), Version: Version, Locale: s.siteLocale(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, License: s.licenseKey(), Plugins: []hubPackageInfo{}, Themes: []hubPackageInfo{}}
	if t := s.activeTheme(); t != nil {
		p.Theme = t.Slug
	}
	for _, r := range s.pluginInventory(ctx) {
		p.Plugins = append(p.Plugins, hubPackageInfo{Slug: r.Slug, Version: r.Version, Active: r.Active, Source: r.Source})
	}
	p.Themes = s.themeInventory()
	return p
}

// themeInventory lists the installed themes with where they came from: bundled themes ship with the
// core (native), library themes installed from Pursae Manager are hub, other packages were uploaded
// (a theme from a local library directory, WPGO_THEME_LIBRARY_DIR, counts as native).
func (s *Server) themeInventory() []hubPackageInfo {
	active := ""
	if t := s.activeTheme(); t != nil {
		active = t.Slug
	}
	var out []hubPackageInfo
	for _, t := range s.installedThemes() {
		if t.Custom { // a Web Builder design saved as a theme: site content, not a package
			continue
		}
		src := pluginSourceNative
		if t.Package != nil {
			switch themePackageSource(t) {
			case themeSourceHub:
				src = pluginSourceHub
			case themeSourceLibrary:
			default:
				src = pluginSourceUpload
			}
		}
		out = append(out, hubPackageInfo{Slug: t.Slug, Version: t.Version, Active: t.Slug == active, Source: src})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// themeVersion is the installed version of a theme, for events.
func (s *Server) themeVersion(slug string) string {
	if t := themeBySlug(slug); t != nil {
		return t.Version
	}
	return ""
}

// pluginVersion is the installed version of a plugin (native or package), for events.
func (s *Server) pluginVersion(ctx context.Context, slug string) string {
	var v string
	if err := s.store.Pool.QueryRow(ctx, `SELECT version FROM plugins WHERE slug = $1`, slug).Scan(&v); err == nil {
		return v
	}
	for _, r := range s.nativePluginRows() {
		if r.Slug == slug {
			return r.Version
		}
	}
	return ""
}
