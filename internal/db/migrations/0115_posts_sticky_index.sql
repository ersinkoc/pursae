-- wpgo:no-transaction
-- The sticky posts of the blog's first page (prependSticky): a few rows, found without a scan.
CREATE INDEX CONCURRENTLY IF NOT EXISTS posts_sticky ON posts (type, status) WHERE sticky;
