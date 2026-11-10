-- Lookups that had no index: foreign keys read per request or per row, and the scheduler's due posts.
-- Every attachment load reads its sizes by media_id (GetAttachment / GetAttachments, the request memo's
-- prefetch), and deleting a media row cascades to its sizes: without an index each was a scan of every size.
CREATE INDEX IF NOT EXISTS media_variants_media ON media_variants (media_id);
-- comment_previously_approved for a logged-in commenter (HasApprovedComment), the "Mine" comment view,
-- REST ?author= and the ON DELETE SET NULL when a user is deleted.
CREATE INDEX IF NOT EXISTS comments_user ON comments (user_id) WHERE user_id IS NOT NULL;
-- The Media Library's author / "Mine" filters and reassigning a deleted user's uploads.
CREATE INDEX IF NOT EXISTS media_uploader ON media (uploader_id);
-- The scheduler looks for due scheduled posts every 15 seconds (PublishDuePosts): only the few
-- scheduled posts are indexed, instead of scanning the whole posts table each time.
CREATE INDEX IF NOT EXISTS posts_future ON posts (published_at) WHERE status = 'future';
