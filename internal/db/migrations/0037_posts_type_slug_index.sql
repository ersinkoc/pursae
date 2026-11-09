-- PostBySlug (single posts, products, custom post types, REST ?slug=) filters on type and slug
-- without the parent, which posts_type_parent_slug_lookup (type, parent, slug) cannot serve.
CREATE INDEX IF NOT EXISTS posts_type_slug ON posts (type, slug)
    WHERE slug <> '' AND status NOT IN ('auto-draft','trash');
