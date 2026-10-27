-- Attachments share the post ID space, as in WordPress, so post.php?post=ID, featured images,
-- wp-image-ID classes and REST ids never collide between posts and media.
ALTER TABLE media ALTER COLUMN id SET DEFAULT nextval('posts_id_seq');
DROP SEQUENCE IF EXISTS media_id_seq;
ALTER TABLE media ADD COLUMN menu_order integer NOT NULL DEFAULT 0;
ALTER TABLE media ADD COLUMN comment_status text NOT NULL DEFAULT 'open';
CREATE INDEX media_created ON media(created_at DESC);
CREATE INDEX media_post ON media(post_id);
CREATE INDEX media_mime ON media(mime_type);
