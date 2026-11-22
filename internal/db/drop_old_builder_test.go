package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDropOldBuilderMigration seeds a migrated database with the old builder's data (stand-ins for
// its tables, posts, meta, options, user meta, saved themes) next to the new builder's web_builder_*
// rows, runs 0028_drop_old_builder twice and checks that only the old data went.
// Skipped without PURSAE_TEST_ADMIN_DB.
func TestDropOldBuilderMigration(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_dbtest_r3"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// A database migrated before 0028 by a release whose 0005-0007 still created the old builder's
	// schema: the objects 0028 removes are back.
	seedOldBuilderSchema(t, pool)

	q := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	id := func(sql string, args ...any) int64 {
		t.Helper()
		var v int64
		if err := pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}

	user := id(`INSERT INTO users (login, email, password_hash, nicename, display_name, meta)
		VALUES ('admin', 'a@example.test', 'x', 'admin', 'Admin',
		'{"gwe_introduction":{"a":true},"gwe_editor_preferences":{},"elementor_introduction":1,"persisted_preferences":{"k":1}}') RETURNING id`)

	// Old builder posts with a revision, a builder revision, a term, a comment and a child page.
	kit := id(`INSERT INTO posts (type, author_id, slug, title, status, meta) VALUES ('elementor_library', $1, 'kit', 'Kit', 'publish',
		'{"_elementor_template_type":"kit","_elementor_data":"[]"}') RETURNING id`, user)
	part := id(`INSERT INTO posts (type, slug, title, status, meta) VALUES ('gwe_template', 'header', 'Header', 'publish',
		'{"_gwe_theme":"x","_elementor_template_type":"header"}') RETURNING id`)
	comp := id(`INSERT INTO posts (type, slug, title, status, meta) VALUES ('elementor_component', 'c', 'C', 'publish',
		'{"_elementor_component_uid":"u1"}') RETURNING id`)
	rev := id(`INSERT INTO post_revisions (post_id, title) VALUES ($1, 'Kit') RETURNING id`, kit)
	q(`INSERT INTO builder_revisions (revision_id, data) VALUES ($1, '[]')`, rev)
	q(`INSERT INTO post_terms (post_id, term_id) SELECT $1, id FROM terms WHERE slug = 'uncategorized'`, part)
	q(`INSERT INTO comments (post_id, content) VALUES ($1, 'hi')`, comp)
	child := id(`INSERT INTO posts (type, parent_id, slug, title, status) VALUES ('page', $1, 'child', 'Child', 'draft') RETURNING id`, kit)

	// Old builder tables.
	q(`INSERT INTO gwe_global_classes (kit_id, id) VALUES ($1, 'g1')`, kit)
	q(`INSERT INTO gwe_variables (kit_id) VALUES ($1)`, kit)
	q(`INSERT INTO gwe_default_styles (kit_id, tag) VALUES ($1, 'h1')`, kit)
	q(`INSERT INTO gwe_form_submissions (post_id) VALUES ($1)`, kit)

	// Pages: one built with the old builder (Canvas template), one with the new builder, one plain.
	oldPage := id(`INSERT INTO posts (type, slug, title, status, content, meta) VALUES ('page', 'old', 'Old', 'publish', '<p>kept</p>',
		'{"_elementor_data":"[]","_elementor_edit_mode":"builder","_elementor_page_settings":{},"_gwe_conditions":[],
		  "_wp_page_template":"elementor_canvas","_thumbnail_id":7,"elementor_note":"not a meta prefix we own"}') RETURNING id`)
	fullWidth := id(`INSERT INTO posts (type, slug, title, status, meta) VALUES ('page', 'fw', 'FW', 'publish',
		'{"_wp_page_template":"elementor_header_footer"}') RETURNING id`)
	newPage := id(`INSERT INTO posts (type, slug, title, status, meta) VALUES ('page', 'new', 'New', 'publish',
		'{"_web_builder_doc":true,"_wp_page_template":"page-no-title"}') RETURNING id`)
	q(`INSERT INTO web_builder_docs (post_id, doc, html, rev) VALUES ($1, '{"v":1}', '<p>new</p>', 3)`, newPage)
	newRev := id(`INSERT INTO post_revisions (post_id, title) VALUES ($1, 'New') RETURNING id`, newPage)
	q(`INSERT INTO web_builder_revisions (revision_id, post_id, rev, doc) VALUES ($1, $2, 3, '{"v":1}')`, newRev, newPage)
	q(`INSERT INTO web_builder_drafts (post_id, user_id, doc) VALUES ($1, $2, '{"v":2}')`, newPage, user)
	q(`INSERT INTO web_builder_library (name, data) VALUES ('Hero', '{}')`)

	// Options.
	q(`INSERT INTO settings (key, value) VALUES
		('gwe_mailchimp_api_key', '"k"'), ('gwe_custom_icon_sets', '[]'), ('elementor_active_kit', to_jsonb($1::bigint)),
		('_elementor_design_system_sync_css_meta', '{}'), ('golementor_starter', '{}'), ('gowebeditor_x', '1'),
		('theme_mods_hello-web-builder', '{}'),
		('golementor_previous_theme', '"twentytwentyfive"'), ('native_golementor', 'true'),
		('web_builder_post_types', '["page"]'), ('blogname', '"Site"'),
		('stylesheet', '"my-saved"'), ('template', '"my-saved"')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, kit)
	q(`INSERT INTO transients (key, value) VALUES ('gwe_fonts', '1'), ('other', '1')`)

	// Themes: the active one saved with the old builder stays for the start-up fallback.
	q(`INSERT INTO themes (slug, version, status, custom) VALUES ('my-saved', '1.0.0', 'active', true),
		('old-saved', '1.0.0', 'inactive', true), ('hello-web-builder', '1.0.0', 'inactive', false),
		('twentytwentyfive', '1.0', 'inactive', false) ON CONFLICT (slug) DO NOTHING`)

	for i := 0; i < 2; i++ {
		execFile(t, pool, "migrations/0028_drop_old_builder.sql")
	}
	if applied, err := Migrate(ctx, pool); err != nil || len(applied) != 0 {
		t.Fatalf("Migrate after 0028: applied %v, err %v", applied, err)
	}

	count := func(sql string, args ...any) int64 { t.Helper(); return id(sql, args...) }
	exists := func(sql string, args ...any) bool {
		t.Helper()
		var b bool
		if err := pool.QueryRow(ctx, sql, args...).Scan(&b); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return b
	}

	for _, tbl := range []string{"builder_revisions", "gwe_global_classes", "gwe_variables", "gwe_default_styles", "gwe_form_submissions"} {
		if exists(`SELECT to_regclass($1) IS NOT NULL`, tbl) {
			t.Errorf("table %s still exists", tbl)
		}
	}
	for _, tbl := range []string{"web_builder_docs", "web_builder_drafts", "web_builder_revisions", "web_builder_library"} {
		if n := count(`SELECT count(*) FROM ` + tbl); n != 1 {
			t.Errorf("%s: %d rows, want 1", tbl, n)
		}
	}
	if n := count(`SELECT count(*) FROM posts WHERE type IN ('elementor_library', 'gwe_template', 'elementor_component')`); n != 0 {
		t.Errorf("%d old builder posts left", n)
	}
	if n := count(`SELECT count(*) FROM content_types WHERE slug IN ('elementor_library', 'gwe_template', 'elementor_component')`); n != 0 {
		t.Errorf("%d old builder content types left", n)
	}
	if n := count(`SELECT count(*) FROM post_revisions WHERE post_id = ANY($1)`, []int64{kit, part, comp}); n != 0 {
		t.Errorf("%d revisions of old builder posts left", n)
	}
	if n := count(`SELECT count(*) FROM post_terms WHERE post_id = $1`, part); n != 0 {
		t.Errorf("term relationships of a theme part left")
	}
	if n := count(`SELECT count(*) FROM comments WHERE post_id = $1`, comp); n != 0 {
		t.Errorf("comments of a component left")
	}
	if !exists(`SELECT parent_id IS NULL FROM posts WHERE id = $1`, child) {
		t.Error("the child of a deleted template keeps its parent")
	}
	if !exists(`SELECT EXISTS (SELECT 1 FROM post_revisions WHERE id = $1)`, newRev) {
		t.Error("a revision of a new builder page went")
	}

	var meta map[string]any
	var content string
	if err := pool.QueryRow(ctx, `SELECT meta, content FROM posts WHERE id = $1`, oldPage).Scan(&meta, &content); err != nil {
		t.Fatal(err)
	}
	for k := range meta {
		if len(k) > 4 && (k[:5] == "_elem" || k[:5] == "_gwe_") {
			t.Errorf("old page keeps meta %s", k)
		}
	}
	if meta["_wp_page_template"] != "default" || meta["_thumbnail_id"] == nil || meta["elementor_note"] == nil || content != "<p>kept</p>" {
		t.Errorf("old page after 0028: meta %v, content %q", meta, content)
	}
	var tpl string
	_ = pool.QueryRow(ctx, `SELECT meta->>'_wp_page_template' FROM posts WHERE id = $1`, fullWidth).Scan(&tpl)
	if tpl != "default" {
		t.Errorf("full width page template = %q, want default", tpl)
	}
	_ = pool.QueryRow(ctx, `SELECT meta->>'_wp_page_template' FROM posts WHERE id = $1`, newPage).Scan(&tpl)
	if tpl != "page-no-title" || !exists(`SELECT (meta->>'_web_builder_doc')::boolean FROM posts WHERE id = $1`, newPage) {
		t.Errorf("new builder page meta changed (template %q)", tpl)
	}

	var umeta map[string]any
	if err := pool.QueryRow(ctx, `SELECT meta FROM users WHERE id = $1`, user).Scan(&umeta); err != nil {
		t.Fatal(err)
	}
	if len(umeta) != 1 || umeta["persisted_preferences"] == nil {
		t.Errorf("user meta after 0028: %v", umeta)
	}

	for _, k := range []string{"gwe_mailchimp_api_key", "gwe_custom_icon_sets", "elementor_active_kit",
		"_elementor_design_system_sync_css_meta", "golementor_starter", "gowebeditor_x", "theme_mods_hello-web-builder"} {
		if exists(`SELECT EXISTS (SELECT 1 FROM settings WHERE key = $1)`, k) {
			t.Errorf("option %s left", k)
		}
	}
	for _, k := range []string{"golementor_previous_theme", "native_golementor", "web_builder_post_types", "blogname", "stylesheet"} {
		if !exists(`SELECT EXISTS (SELECT 1 FROM settings WHERE key = $1)`, k) {
			t.Errorf("option %s deleted", k)
		}
	}
	if exists(`SELECT EXISTS (SELECT 1 FROM transients WHERE key = 'gwe_fonts')`) || !exists(`SELECT EXISTS (SELECT 1 FROM transients WHERE key = 'other')`) {
		t.Error("transients: want gwe_fonts gone and other kept")
	}

	var themes []string
	rows, err := pool.Query(ctx, `SELECT slug FROM themes ORDER BY slug`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		themes = append(themes, s)
	}
	rows.Close()
	if got := strings.Join(themes, ","); got != "my-saved,twentytwentyfive" {
		t.Errorf("themes after 0028 = %s, want my-saved,twentytwentyfive", got)
	}
}

// oldBuilderSchema stands in for what the old builder's migrations left in a database: the tables,
// indexes, content types and the themes.custom column that 0028 removes or reads. Only the columns
// the tests use are created; 0005-0007 are empty steps now.
const oldBuilderSchema = `
INSERT INTO content_types (slug, name, public) VALUES
	('elementor_library', 'Templates', false), ('gwe_template', 'Theme Parts', false), ('elementor_component', 'Components', false)
ON CONFLICT (slug) DO NOTHING;
CREATE TABLE builder_revisions (revision_id bigint PRIMARY KEY REFERENCES post_revisions(id) ON DELETE CASCADE, data jsonb);
CREATE TABLE gwe_global_classes (kit_id bigint, id text, PRIMARY KEY (kit_id, id));
CREATE TABLE gwe_variables (kit_id bigint PRIMARY KEY);
CREATE TABLE gwe_default_styles (kit_id bigint, tag text, PRIMARY KEY (kit_id, tag));
CREATE TABLE gwe_form_submissions (id bigserial PRIMARY KEY, post_id bigint REFERENCES posts(id) ON DELETE SET NULL);
CREATE INDEX posts_gwe_parts ON posts ((meta->>'_gwe_theme')) WHERE type = 'gwe_template';
CREATE INDEX posts_gwe_components ON posts ((meta->>'_elementor_component_uid')) WHERE type = 'elementor_component';
ALTER TABLE themes ADD COLUMN custom boolean NOT NULL DEFAULT false;
`

func seedOldBuilderSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), oldBuilderSchema); err != nil {
		t.Fatalf("old builder schema: %v", err)
	}
}

// TestDropOldBuilderFreshInstall: on a database that never had the old builder (0005-0007 are
// empty steps) 0028 still runs, finds nothing of it and removes only the blank theme it always
// removed.
func TestDropOldBuilderFreshInstall(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_dbtest_r3f"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var objs []string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(n), '{}') FROM (
		SELECT tablename::text AS n FROM pg_tables WHERE schemaname = current_schema()
		   AND (tablename = 'builder_revisions' OR tablename LIKE 'gwe\_%')
		UNION ALL SELECT indexname::text FROM pg_indexes WHERE schemaname = current_schema() AND indexname LIKE 'posts\_gwe\_%'
		UNION ALL SELECT 'themes.' || column_name FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'themes' AND column_name = 'custom'
		UNION ALL SELECT 'content type ' || slug FROM content_types
		 WHERE slug IN ('elementor_library', 'gwe_template', 'elementor_component')) x`).Scan(&objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 0 {
		t.Errorf("a fresh install has old builder objects: %v", objs)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO themes (slug, version, status) VALUES ('hello-web-builder', '1.0.0', 'inactive'),
		('keep-me', '1.0.0', 'inactive') ON CONFLICT (slug) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		execFile(t, pool, "migrations/0028_drop_old_builder.sql")
	}
	var left []string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(slug ORDER BY slug), '{}') FROM themes
		WHERE slug IN ('hello-web-builder', 'keep-me')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if strings.Join(left, ",") != "keep-me" {
		t.Errorf("themes after 0028 on a fresh install = %v, want [keep-me]", left)
	}
}

func execFile(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	b, err := migrationFS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(b)); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
