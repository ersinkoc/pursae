-- Pursae Multi Language: per-language media texts, and the sources of translatable strings.

-- Per-language overrides of a media item's texts. The media row itself holds the default
-- language; a NULL column here falls back to it.
CREATE TABLE IF NOT EXISTS pll_media_i18n (
	media_id    bigint NOT NULL REFERENCES media(id) ON DELETE CASCADE,
	lang        text   NOT NULL REFERENCES pll_languages(slug) ON UPDATE CASCADE ON DELETE CASCADE,
	alt_text    text,
	title       text,
	caption     text,
	description text,
	updated_at  timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (media_id, lang)
);
CREATE INDEX IF NOT EXISTS pll_media_i18n_lang ON pll_media_i18n (lang);

-- Strings found on the site (theme templates, plugins, captured page text) for the
-- Translations screen. source: "theme:<slug>", "plugin:<slug>", "core" or "page".
CREATE TABLE IF NOT EXISTS pll_string_sources (
	original   text NOT NULL,
	context    text NOT NULL DEFAULT '',
	domain     text NOT NULL DEFAULT '',
	source     text NOT NULL DEFAULT '',
	first_seen timestamptz NOT NULL DEFAULT now(),
	last_seen  timestamptz NOT NULL DEFAULT now(),
	url        text NOT NULL DEFAULT '',
	PRIMARY KEY (original, context, domain)
);
CREATE INDEX IF NOT EXISTS pll_string_sources_source ON pll_string_sources (source);
CREATE INDEX IF NOT EXISTS pll_string_sources_last_seen ON pll_string_sources (last_seen);

-- Content images without a media id (Web Builder images, pasted uploads URLs) are matched to their
-- media item by file path on translated pages (golylang_media.go pllMediaByPath).
CREATE INDEX IF NOT EXISTS media_file_path ON media (file_path);
CREATE INDEX IF NOT EXISTS media_variants_file_path ON media_variants (file_path);
