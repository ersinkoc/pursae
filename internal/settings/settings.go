// Package settings is the typed counterpart of WordPress options: every value is
// stored as JSONB, autoloaded rows are cached in memory and the cache is
// refreshed on write.
//
// Several processes may share one database. Reads never leave memory; to keep
// the caches in step, a statement trigger on the settings table (migration
// 0047) sends NOTIFY wpgo_settings when its transaction commits, and Watch
// (started by `wpgo serve`) listens on a dedicated connection and reloads the
// cache on each notification, after every (re)connect and every
// WatchFullReload as a safety net. A change made by another instance therefore
// shows up here within moments, at worst after WatchFullReload. Watch only
// refreshes values: side effects a process runs when it saves a setting itself
// (starting a plugin, reconfiguring the page cache) still happen only there.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotifyChannel is the channel the settings trigger notifies (migration 0047).
const NotifyChannel = "wpgo_settings"

// WatchFullReload is how often Watch reloads the cache even without a
// notification, in case one was lost.
var WatchFullReload = time.Minute

// Defaults mirrors populate_options() of a fresh WordPress install. Keys keep
// the WordPress option names so REST and import code can map 1:1.
var Defaults = map[string]any{
	"blogname":                      "My Pursae Site",
	"blogdescription":               "",
	"siteurl":                       "",
	"home":                          "",
	"admin_email":                   "",
	"users_can_register":            false,
	"default_role":                  "subscriber",
	"WPLANG":                        "",
	"timezone_string":               "",
	"gmt_offset":                    0.0,
	"date_format":                   "F j, Y",
	"time_format":                   "g:i a",
	"start_of_week":                 1,
	"use_smilies":                   true,
	"default_category":              1,
	"default_post_format":           "0",
	"classic-editor-replace":        "block",
	"classic-editor-allow-users":    "disallow",
	"native_classic_editor":         true,
	"native_golementor":             false,       // Pursae Web Builder is a plugin that starts switched off
	"web_builder_post_types":        "page,post", // Settings › Web Builder: the post types it edits
	"native_golylang":               false,       // the multilingual plugin (Polylang Pro clone) starts switched off
	"native_gooike":                 false,       // Gooike, the cookie consent plugin, starts switched off
	"native_goocommerce":            false,       // Goocommerce, the e-commerce plugin, starts switched off
	"native_headlessgo":             false,       // HeadlessGo, the headless CMS plugin, starts switched off
	"plugin_trusted_keys":           "",
	"plugin_allow_unsigned":         false,
	"pursae_license_key":            "",   // Settings › License: the key sent to Pursae Manager
	"wpgo_hub_trust_keys":           true, // packages signed by the Hub install without extra setup
	"wpgo_cache_enabled":            true,
	"wpgo_cache_ttl_public":         604800,
	"wpgo_cache_ttl_front":          604800,
	"wpgo_cache_ttl_feed":           604800,
	"wpgo_cache_ttl_404":            3600,
	"wpgo_cache_mobile":             false,
	"wpgo_cache_logged_in":          false,
	"wpgo_cache_exclude_uri":        "",
	"wpgo_cache_drop_qs":            "",
	"wpgo_cache_server":             "auto",
	"wpgo_cache_disk":               true, // warm pages survive restarts and updates
	"wpgo_opt_lazy":                 true,
	"wpgo_opt_lazy_skip":            1,
	"wpgo_opt_defer":                true, // an empty list defers only scripts nothing depends on
	"wpgo_opt_defer_list":           "",
	"wpgo_opt_minify_html":          true,
	"wpgo_opt_preload_fonts":        true,
	"posts_per_page":                10,
	"posts_per_rss":                 10,
	"rss_use_excerpt":               false,
	"show_on_front":                 "posts",
	"page_on_front":                 0,
	"page_for_posts":                0,
	"blog_public":                   true,
	"default_pingback_flag":         true,
	"default_ping_status":           "open",
	"default_comment_status":        "open",
	"require_name_email":            true,
	"comment_registration":          false,
	"close_comments_for_old_posts":  false,
	"close_comments_days_old":       14,
	"show_comments_cookies_opt_in":  true,
	"thread_comments":               true,
	"thread_comments_depth":         5,
	"page_comments":                 false,
	"comments_per_page":             50,
	"default_comments_page":         "newest",
	"comment_order":                 "asc",
	"comments_notify":               true,
	"moderation_notify":             true,
	"comment_moderation":            false,
	"comment_previously_approved":   true,
	"comment_max_links":             2,
	"moderation_keys":               "",
	"disallowed_keys":               "",
	"show_avatars":                  true,
	"avatar_rating":                 "G",
	"avatar_default":                "mystery",
	"thumbnail_size_w":              150,
	"thumbnail_size_h":              150,
	"thumbnail_crop":                true,
	"medium_size_w":                 300,
	"medium_size_h":                 300,
	"medium_large_size_w":           768,
	"medium_large_size_h":           0,
	"large_size_w":                  1024,
	"large_size_h":                  1024,
	"uploads_use_yearmonth_folders": true,
	"image_output_format":           "", // Settings › Media: "", "webp" or "webp-all" (Server.imageEditorOptions)
	"permalink_structure":           "/%year%/%monthnum%/%day%/%postname%/",
	"category_base":                 "",
	"tag_base":                      "",
	"template":                      "twentytwentyfive",
	"stylesheet":                    "twentytwentyfive",
	"sticky_posts":                  []any{},
	"wp_page_for_privacy_policy":    0,
	"site_icon":                     0,
}

type Store struct {
	pool  *pgxpool.Pool
	mu    sync.RWMutex
	cache map[string]any
	// writeMu orders Set's commit and cache update, so the cache ends with the
	// value committed last when two Sets of one key race.
	writeMu sync.Mutex
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Load fills the cache with every autoloaded row.
func (s *Store) Load(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings WHERE autoload`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[string]any{}
	for rows.Next() {
		var k string
		var raw []byte
		if err := rows.Scan(&k, &raw); err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("setting %s: %w", k, err)
		}
		m[k] = v
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.cache = m
	s.mu.Unlock()
	return nil
}

// Installed reports whether the install wizard has run.
func (s *Store) Installed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.cache["siteurl"]
	return ok
}

func (s *Store) Get(key string) any {
	s.mu.RLock()
	v, ok := s.cache[key]
	s.mu.RUnlock()
	if ok {
		return v
	}
	return Defaults[key]
}

func (s *Store) String(key string) string {
	switch v := s.Get(key).(type) {
	case string:
		return v
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func (s *Store) Int(key string) int {
	switch v := s.Get(key).(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func (s *Store) Float(key string) float64 {
	switch v := s.Get(key).(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	}
	return 0
}

func (s *Store) Bool(key string) bool {
	switch v := s.Get(key).(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	case string:
		return v != "" && v != "0"
	}
	return false
}

// Set writes the given values in one transaction and refreshes the cache.
func (s *Store) Set(ctx context.Context, values map[string]any) error {
	// Upsert in key order: two concurrent saves of the same screen lock the rows
	// in the same order and cannot deadlock.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, k := range keys {
		v := values[k]
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO settings(key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, k, raw); err != nil {
			return err
		}
	}
	// Commit and refresh the cache under writeMu: a Set blocked on our row locks
	// commits after us and so also updates the cache after us.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Round-trip through JSON so cached values have the same types as loaded ones.
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]any{}
	}
	for k, v := range values {
		raw, _ := json.Marshal(v)
		var norm any
		_ = json.Unmarshal(raw, &norm)
		s.cache[k] = norm
	}
	s.mu.Unlock()
	return nil
}

// Watch keeps the cache in step with changes other processes commit (see the
// package comment). It blocks until ctx ends. The LISTEN connection is opened
// outside the pool, so closing the pool never waits for it.
func (s *Store) Watch(ctx context.Context) {
	stop := make(chan struct{})
	defer close(stop)
	go func() { // safety net for lost notifications and a broken listener
		t := time.NewTicker(WatchFullReload)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := s.Load(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("settings: periodic reload", "err", err)
				}
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		err := s.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("settings: change listener stopped, reconnecting", "err", err, "in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// listen runs one LISTEN session until it fails or ctx ends.
func (s *Store) listen(ctx context.Context) error {
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
		return err
	}
	// Changes made while nobody listened are only visible through a reload.
	if err := s.Load(ctx); err != nil {
		return err
	}
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		if err := s.Load(ctx); err != nil {
			return err
		}
	}
}
