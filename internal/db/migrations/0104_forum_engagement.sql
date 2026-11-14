-- Pursae Forum engagement (v0.2.5): likes, topic views, the points ledger, topic subscriptions and
-- post reports. Rows exist on every site; nothing reads or writes them while native_pursae_forum is
-- off. Every row follows its post or member (ON DELETE CASCADE), so purging a topic, a reply or a
-- member leaves nothing behind.

-- One like per member and post (topic opening post or reply).
CREATE TABLE IF NOT EXISTS forum_likes (
  post_id    BIGINT      NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (post_id, user_id)
);
CREATE INDEX IF NOT EXISTS forum_likes_user ON forum_likes (user_id, created_at DESC);

-- Topic view counter (the cache-friendly beacon POST forum/v1/topics/<id>/view writes it).
CREATE TABLE IF NOT EXISTS forum_topic_views (
  topic_id BIGINT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
  views    BIGINT NOT NULL DEFAULT 0
);

-- The points ledger: one row per point-earning event (topic, reply, accepted solution, like received,
-- manual adjustment). Totals stay computed from the posts; the ledger dates the points, so "members
-- of the week" sums the last seven days. Rows of a post that is no longer live do not count.
CREATE TABLE IF NOT EXISTS forum_points (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       TEXT        NOT NULL, -- topic|reply|solution|like|manual
  points     INTEGER     NOT NULL,
  post_id    BIGINT      REFERENCES posts(id) ON DELETE CASCADE,
  actor_id   BIGINT      REFERENCES users(id) ON DELETE SET NULL,
  note       TEXT        NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS forum_points_week ON forum_points (created_at DESC, user_id);
CREATE INDEX IF NOT EXISTS forum_points_user ON forum_points (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS forum_points_post ON forum_points (post_id) WHERE post_id IS NOT NULL;
-- An event is recorded once (a like by one member, a topic, a reply, a solution).
CREATE UNIQUE INDEX IF NOT EXISTS forum_points_event ON forum_points (kind, post_id, coalesce(actor_id, 0)) WHERE kind <> 'manual';

-- Topic subscriptions: "email me when someone replies".
CREATE TABLE IF NOT EXISTS forum_subscriptions (
  topic_id   BIGINT      NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (topic_id, user_id)
);
CREATE INDEX IF NOT EXISTS forum_subscriptions_user ON forum_subscriptions (user_id);

-- Reported posts: the moderation queue's "Reported" view.
CREATE TABLE IF NOT EXISTS forum_reports (
  id          BIGSERIAL PRIMARY KEY,
  post_id     BIGINT      NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  user_id     BIGINT      REFERENCES users(id) ON DELETE SET NULL,
  reason      TEXT        NOT NULL,
  note        TEXT        NOT NULL DEFAULT '',
  status      TEXT        NOT NULL DEFAULT 'open', -- open|resolved
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_by BIGINT      REFERENCES users(id) ON DELETE SET NULL,
  resolved_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS forum_reports_once ON forum_reports (post_id, user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS forum_reports_open ON forum_reports (post_id) WHERE status = 'open';

-- Topic tags (posts.meta _forum_tags: an array of tag slugs) and the solved filter.
CREATE INDEX IF NOT EXISTS posts_forum_tags ON posts USING gin ((meta->'_forum_tags')) WHERE type = 'topic';
CREATE INDEX IF NOT EXISTS posts_forum_solved ON posts ((meta->>'_forum_solution')) WHERE type = 'topic' AND meta ? '_forum_solution';
