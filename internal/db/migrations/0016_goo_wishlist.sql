-- Goocommerce › Wishlists: lists of customers (or guests, by a wishlist key) and their items.
CREATE TABLE IF NOT EXISTS goo_wishlists (
	id          BIGSERIAL PRIMARY KEY,
	token       TEXT NOT NULL UNIQUE,
	user_id     BIGINT NOT NULL DEFAULT 0,
	session_key TEXT NOT NULL DEFAULT '',
	title       TEXT NOT NULL DEFAULT '',
	privacy     TEXT NOT NULL DEFAULT 'private',
	is_default  BOOLEAN NOT NULL DEFAULT false,
	created     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_wishlists_user ON goo_wishlists (user_id) WHERE user_id <> 0;
CREATE INDEX IF NOT EXISTS goo_wishlists_session ON goo_wishlists (session_key) WHERE session_key <> '';

CREATE TABLE IF NOT EXISTS goo_wishlist_items (
	id           BIGSERIAL PRIMARY KEY,
	wishlist_id  BIGINT NOT NULL REFERENCES goo_wishlists(id) ON DELETE CASCADE,
	product_id   BIGINT NOT NULL,
	variation_id BIGINT NOT NULL DEFAULT 0,
	quantity     INTEGER NOT NULL DEFAULT 1,
	added        TIMESTAMPTZ NOT NULL DEFAULT now(),
	price_at_add NUMERIC(19,4),
	UNIQUE (wishlist_id, product_id, variation_id)
);
CREATE INDEX IF NOT EXISTS goo_wishlist_items_product ON goo_wishlist_items (product_id);
