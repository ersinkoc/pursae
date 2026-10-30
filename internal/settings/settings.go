// Package settings is the typed counterpart of WordPress options: every value is
// stored as JSONB, autoloaded rows are cached in memory and the cache is
// refreshed on write.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Defaults mirrors populate_options() of a fresh WordPress install. Keys keep
// the WordPress option names so REST and import code can map 1:1.
var Defaults = map[string]any{
	"blogname":                      "My WordPress Go Site",
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
	"native_golementor":             false, // the page builder is a plugin that starts switched off
	"golementor_previous_theme":     "",    // the theme Golementor left for Hello Golementor; restored on deactivation
	"native_golylang":               false, // the multilingual plugin (Polylang Pro clone) starts switched off
	"native_gooike":                 false, // Gooike, the cookie consent plugin, starts switched off
	"native_goocommerce":            false, // Goocommerce, the e-commerce plugin, starts switched off
	"plugin_trusted_keys":           "",
	"plugin_allow_unsigned":         false,
	"wpgo_hub_url":                  "",   // overrides WPGO_HUB_URL when set
	"wpgo_hub_ping":                 true, // tell the Hub this site's URL host and version once a day
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for k, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO settings(key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, k, raw); err != nil {
			return err
		}
	}
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
