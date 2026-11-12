-- Pursae Forum (native plugin, bbPress-like). Forums, topics and replies are posts (types forum, topic,
-- reply); counters and state live in posts.meta. Rows exist on every site; nothing reads them while
-- native_pursae_forum is off. Roles are added on activation (forum.go), not here.
INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels) VALUES
('forum', 'Forums',  true,  false, '', false, '{"title":true,"editor":true,"page-attributes":true}', '{"name":"Forums","singular_name":"Forum"}'),
('topic', 'Topics',  false, false, '', false, '{"title":true,"editor":true}', '{"name":"Topics","singular_name":"Topic"}'),
('reply', 'Replies', false, false, '', false, '{"editor":true}', '{"name":"Replies","singular_name":"Reply"}')
ON CONFLICT (slug) DO NOTHING;

-- Topic list of a forum: sticky first, then last activity.
CREATE INDEX IF NOT EXISTS posts_forum_topics ON posts (parent_id, sticky DESC, (meta->>'_forum_last_active') DESC, id DESC)
  WHERE type = 'topic';
-- Recent topics across forums (index page, forum_recent_topics, the shortcode).
CREATE INDEX IF NOT EXISTS posts_forum_recent ON posts ((meta->>'_forum_last_active') DESC, id DESC)
  WHERE type = 'topic' AND status = 'publish';
-- Replies of a topic in order.
CREATE INDEX IF NOT EXISTS posts_forum_replies ON posts (parent_id, published_at, id) WHERE type = 'reply';
-- Profile pages, flood check.
CREATE INDEX IF NOT EXISTS posts_forum_author ON posts (author_id, type, created_at DESC) WHERE type IN ('topic','reply');
-- One Pursae account per Google subject.
CREATE UNIQUE INDEX IF NOT EXISTS users_forum_google_sub ON users ((meta->>'forum_google_sub')) WHERE meta ? 'forum_google_sub';
