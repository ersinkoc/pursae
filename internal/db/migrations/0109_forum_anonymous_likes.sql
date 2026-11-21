-- Pursae Forum: a like can outlive the link to the member who gave it. The personal data eraser
-- anonymizes the likes of the person (user_id NULL) instead of deleting them, so the post keeps its
-- count and its author keeps the points the like earned. One like per member and post still holds
-- for every like that has a member. Nothing is dropped or retyped: the previous release inserts with
-- ON CONFLICT DO NOTHING, which the partial unique index below still answers.
ALTER TABLE forum_likes DROP CONSTRAINT IF EXISTS forum_likes_pkey;
ALTER TABLE forum_likes ALTER COLUMN user_id DROP NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS forum_likes_once ON forum_likes (post_id, user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS forum_likes_post ON forum_likes (post_id);
