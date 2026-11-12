package store

import (
	"context"
	"strings"
)

// searchStemConfigs maps a WordPress locale's language to the PostgreSQL text search configuration
// (a stemmer) the site's search uses next to the exact-word 'simple' vector (migration 0101).
var searchStemConfigs = map[string]string{
	"ar": "arabic", "hy": "armenian", "eu": "basque", "ca": "catalan", "da": "danish", "nl": "dutch",
	"en": "english", "fi": "finnish", "fr": "french", "de": "german", "el": "greek", "hi": "hindi",
	"hu": "hungarian", "id": "indonesian", "ga": "irish", "it": "italian", "lt": "lithuanian",
	"ne": "nepali", "nb": "norwegian", "nn": "norwegian", "no": "norwegian", "pt": "portuguese",
	"ro": "romanian", "ru": "russian", "sr": "serbian", "es": "spanish", "sv": "swedish", "ta": "tamil",
	"tr": "turkish", "yi": "yiddish",
}

// SearchStemConfig is the text search configuration for a site locale (WPLANG, "" = en_US); "simple"
// when PostgreSQL has no stemmer for the language.
func SearchStemConfig(locale string) string {
	if locale == "" {
		locale = "en_US"
	}
	lang := strings.ToLower(locale)
	if i := strings.IndexAny(lang, "_-"); i > 0 {
		lang = lang[:i]
	}
	if cfg, ok := searchStemConfigs[lang]; ok {
		return cfg
	}
	return "simple"
}

// searchStemLock is the advisory lock id of the rebuild, so two instances do not rebuild at once.
const searchStemLock = 0x77706f5354454d // "wpoSTEM"

// SyncSearchStem switches the stemmed search to the configuration of locale and rebuilds
// posts.search_stem when it changed. It reports whether it rebuilt. Posts are rebuilt in batches so a
// large site never holds one long lock; new and edited posts use the new configuration right away
// (the trigger reads search_stem_state).
func (s *Store) SyncSearchStem(ctx context.Context, locale string) (bool, error) {
	want := SearchStemConfig(locale)
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(searchStemLock)).Scan(&locked); err != nil || !locked {
		return false, err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(searchStemLock))
	var cur string
	if err := conn.QueryRow(ctx, `SELECT wpgo_search_stem_config()::text`).Scan(&cur); err != nil {
		return false, err
	}
	// A partly rebuilt column (an interrupted run) is finished on the next call: rows with a NULL
	// vector under a real stemmer are rebuilt even when the configuration is unchanged.
	if cur == want {
		if want == "simple" {
			return false, nil
		}
		var stale bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE search_stem IS NULL)`).Scan(&stale); err != nil || !stale {
			return false, err
		}
	} else if _, err := conn.Exec(ctx, `UPDATE search_stem_state SET cfg = $1::regconfig`, want); err != nil {
		return false, err
	}
	var last int64
	for {
		var n, maxID int64
		err := conn.QueryRow(ctx, `WITH b AS (SELECT id FROM posts WHERE id > $1 ORDER BY id LIMIT 500),
			u AS (UPDATE posts p SET search_stem = wpgo_search_stem_vector(wpgo_search_stem_config(), p.title, p.excerpt, p.content)
				FROM b WHERE p.id = b.id RETURNING p.id)
			SELECT count(*), coalesce(max(id), 0) FROM b`, last).Scan(&n, &maxID)
		if err != nil {
			return false, err
		}
		if n == 0 {
			return true, nil
		}
		last = maxID
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
	}
}
