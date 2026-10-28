-- Golylang (native multilingual plugin, a Polylang Pro clone). The tables exist on every site; while the
-- plugin is off nothing reads them. Polylang keeps languages and translation groups in taxonomies; here they
-- are plain tables. An object without a row is in the default language.

CREATE TABLE IF NOT EXISTS pll_languages (
	id         bigserial PRIMARY KEY,
	slug       text NOT NULL UNIQUE,              -- language code used in URLs: en, fr, pt-br
	name       text NOT NULL,                     -- full name: English, Français
	locale     text NOT NULL,                     -- WordPress locale: en_US, fr_FR
	rtl        boolean NOT NULL DEFAULT false,
	flag       text NOT NULL DEFAULT '',          -- flag code (ISO 3166-1 alpha-2, lower case), '' for none
	term_order integer NOT NULL DEFAULT 0,
	active     boolean NOT NULL DEFAULT true,     -- Pro: an inactive language is hidden from visitors
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

-- Posts and pages: language and translation group (Polylang's "language" and "post_translations" taxonomies).
CREATE TABLE IF NOT EXISTS pll_post_langs (
	post_id bigint PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
	lang    text NOT NULL REFERENCES pll_languages(slug) ON UPDATE CASCADE ON DELETE CASCADE,
	grp     bigint NOT NULL
);
CREATE INDEX IF NOT EXISTS pll_post_langs_lang ON pll_post_langs(lang);
CREATE INDEX IF NOT EXISTS pll_post_langs_grp ON pll_post_langs(grp);

-- Categories and tags ("term_language" and "term_translations").
CREATE TABLE IF NOT EXISTS pll_term_langs (
	term_id bigint PRIMARY KEY REFERENCES terms(id) ON DELETE CASCADE,
	lang    text NOT NULL REFERENCES pll_languages(slug) ON UPDATE CASCADE ON DELETE CASCADE,
	grp     bigint NOT NULL
);
CREATE INDEX IF NOT EXISTS pll_term_langs_lang ON pll_term_langs(lang);
CREATE INDEX IF NOT EXISTS pll_term_langs_grp ON pll_term_langs(grp);

-- Media (when "Translate media" is on, each language gets its own media row sharing the file).
CREATE TABLE IF NOT EXISTS pll_media_langs (
	media_id bigint PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
	lang     text NOT NULL REFERENCES pll_languages(slug) ON UPDATE CASCADE ON DELETE CASCADE,
	grp      bigint NOT NULL
);
CREATE INDEX IF NOT EXISTS pll_media_langs_grp ON pll_media_langs(grp);

CREATE SEQUENCE IF NOT EXISTS pll_translation_groups;

-- Strings translations (Polylang keeps them in polylang_mo_<id> posts).
CREATE TABLE IF NOT EXISTS pll_strings (
	lang        text NOT NULL REFERENCES pll_languages(slug) ON UPDATE CASCADE ON DELETE CASCADE,
	original    text NOT NULL,
	translation text NOT NULL,
	PRIMARY KEY (lang, original)
);
