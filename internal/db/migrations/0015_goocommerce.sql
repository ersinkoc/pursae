-- Goocommerce (native e-commerce plugin, a WooCommerce clone). The tables exist on every site; while
-- the plugin is off nothing reads them and nothing of it reaches the site. Products and variations are
-- posts (types "product" and "product_variation") like WooCommerce's, their data lives in goo_products
-- (WooCommerce's postmeta + wc_product_meta_lookup in one typed row). Orders use dedicated tables like
-- WooCommerce's High-Performance Order Storage (wc_orders, wc_order_addresses, woocommerce_order_items).

INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels) VALUES
('product', 'Products', false, true, 'product', false, '{"title":true,"editor":true,"excerpt":true,"thumbnail":true,"comments":true,"custom-fields":true}',
 '{"name":"Products","singular_name":"Product","all_items":"All Products","add_new":"Add New","add_new_item":"Add new product","edit_item":"Edit product","new_item":"New product","view_item":"View product","view_items":"View products","search_items":"Search products","not_found":"No products found","not_found_in_trash":"No products found in trash","menu_name":"Products"}'),
('product_variation', 'Variations', false, false, '', false, '{}', '{"name":"Variations","singular_name":"Variation"}')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO taxonomies (slug, name, hierarchical, object_types, rest_base, public, is_builtin, labels) VALUES
('product_cat', 'Product categories', true, '["product"]', 'product_cat', true, false,
 '{"name":"Product categories","singular_name":"Category","menu_name":"Categories","search_items":"Search categories","all_items":"All categories","parent_item":"Parent category","parent_item_colon":"Parent category:","edit_item":"Edit category","update_item":"Update category","add_new_item":"Add new category","new_item_name":"New category name","not_found":"No categories found"}'),
('product_tag', 'Product tags', false, '["product"]', 'product_tag', true, false,
 '{"name":"Product tags","singular_name":"Tag","menu_name":"Tags","search_items":"Search tags","all_items":"All tags","edit_item":"Edit tag","update_item":"Update tag","add_new_item":"Add new tag","new_item_name":"New tag name","popular_items":"Popular tags","separate_items_with_commas":"Separate tags with commas","add_or_remove_items":"Add or remove tags","choose_from_most_used":"Choose from the most used tags","not_found":"No tags found"}'),
('product_shipping_class', 'Shipping classes', false, '["product","product_variation"]', '', false, false,
 '{"name":"Shipping classes","singular_name":"Shipping class","menu_name":"Shipping classes","add_new_item":"Add new shipping class","not_found":"No shipping classes found."}')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO terms (taxonomy, slug, name) VALUES ('product_cat', 'uncategorized', 'Uncategorized') ON CONFLICT DO NOTHING;

-- WooCommerce's roles: the Shop manager and the Customer.
INSERT INTO roles (slug, name, is_system, capabilities)
SELECT 'shop_manager', 'Shop manager', false, capabilities || '{"manage_woocommerce":true,"view_woocommerce_reports":true,"edit_products":true,"edit_others_products":true,"publish_products":true,"read_private_products":true,"delete_products":true,"delete_others_products":true,"delete_published_products":true,"delete_private_products":true,"edit_published_products":true,"edit_private_products":true,"manage_product_terms":true,"edit_shop_orders":true,"edit_shop_coupons":true,"list_users":true,"edit_users":false}'::jsonb
FROM roles WHERE slug = 'editor'
ON CONFLICT (slug) DO NOTHING;
INSERT INTO roles (slug, name, is_system, capabilities) VALUES ('customer', 'Customer', false, '{"read":true,"level_0":true}')
ON CONFLICT (slug) DO NOTHING;
UPDATE roles SET capabilities = capabilities || '{"manage_woocommerce":true,"view_woocommerce_reports":true,"edit_products":true,"edit_others_products":true,"publish_products":true,"read_private_products":true,"delete_products":true,"delete_others_products":true,"delete_published_products":true,"delete_private_products":true,"edit_published_products":true,"edit_private_products":true,"manage_product_terms":true,"edit_shop_orders":true,"edit_shop_coupons":true}'::jsonb
WHERE slug = 'administrator';

-- Product data: one row per product and per variation.
CREATE TABLE IF NOT EXISTS goo_products (
	post_id            bigint PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
	type               text NOT NULL DEFAULT 'simple',     -- simple, variable, grouped, external, variation
	sku                text NOT NULL DEFAULT '',
	regular_price      numeric(19,4),
	sale_price         numeric(19,4),
	sale_from          timestamptz,
	sale_to            timestamptz,
	price              numeric(19,4),                      -- the active price (_price), kept by the store code
	min_price          numeric(19,4),                      -- variable products: lowest / highest variation price
	max_price          numeric(19,4),
	tax_status         text NOT NULL DEFAULT 'taxable',    -- taxable, shipping, none
	tax_class          text NOT NULL DEFAULT '',           -- '' (standard), reduced-rate, zero-rate, parent (variations)
	manage_stock       boolean NOT NULL DEFAULT false,
	stock_quantity     integer,
	stock_status       text NOT NULL DEFAULT 'instock',    -- instock, outofstock, onbackorder
	backorders         text NOT NULL DEFAULT 'no',         -- no, notify, yes
	low_stock_amount   integer,
	sold_individually  boolean NOT NULL DEFAULT false,
	weight             text NOT NULL DEFAULT '',
	length             text NOT NULL DEFAULT '',
	width              text NOT NULL DEFAULT '',
	height             text NOT NULL DEFAULT '',
	shipping_class_id  bigint REFERENCES terms(id) ON DELETE SET NULL,
	virtual            boolean NOT NULL DEFAULT false,
	downloadable       boolean NOT NULL DEFAULT false,
	downloads          jsonb NOT NULL DEFAULT '[]',        -- [{id, name, file}]
	download_limit     integer NOT NULL DEFAULT -1,
	download_expiry    integer NOT NULL DEFAULT -1,
	purchase_note      text NOT NULL DEFAULT '',
	upsell_ids         bigint[] NOT NULL DEFAULT '{}',
	cross_sell_ids     bigint[] NOT NULL DEFAULT '{}',
	children           bigint[] NOT NULL DEFAULT '{}',     -- grouped products
	product_url        text NOT NULL DEFAULT '',           -- external products
	button_text        text NOT NULL DEFAULT '',
	attributes         jsonb NOT NULL DEFAULT '[]',        -- [{name, taxonomy, options[], position, visible, variation}]
	default_attributes jsonb NOT NULL DEFAULT '{}',
	variation_attrs    jsonb NOT NULL DEFAULT '{}',        -- variations: {"pa_color":"red","size":""} ('' = any)
	gallery_ids        bigint[] NOT NULL DEFAULT '{}',
	image_id           bigint NOT NULL DEFAULT 0,          -- variations: their own image
	featured           boolean NOT NULL DEFAULT false,
	catalog_visibility text NOT NULL DEFAULT 'visible',    -- visible, catalog, search, hidden
	total_sales        integer NOT NULL DEFAULT 0,
	average_rating     numeric(3,2) NOT NULL DEFAULT 0,
	rating_count       integer NOT NULL DEFAULT 0,
	description        text NOT NULL DEFAULT '',           -- variations: their description
	meta               jsonb NOT NULL DEFAULT '{}',        -- extension data (bundles, add-ons, subscriptions, ...)
	updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_products_sku ON goo_products (sku) WHERE sku <> '';
CREATE INDEX IF NOT EXISTS goo_products_price ON goo_products (price);
CREATE INDEX IF NOT EXISTS goo_products_type ON goo_products (type);

-- Global attributes (Products › Attributes): each one is a taxonomy pa_<slug>.
CREATE TABLE IF NOT EXISTS goo_attributes (
	id           bigserial PRIMARY KEY,
	name         text NOT NULL,
	slug         text NOT NULL UNIQUE,                     -- without the pa_ prefix
	type         text NOT NULL DEFAULT 'select',
	order_by     text NOT NULL DEFAULT 'menu_order',       -- menu_order, name, name_num, id
	has_archives boolean NOT NULL DEFAULT false
);

-- Orders (and refunds, type shop_order_refund with parent_id = the order).
CREATE TABLE IF NOT EXISTS goo_orders (
	id                   bigserial PRIMARY KEY,
	type                 text NOT NULL DEFAULT 'shop_order',
	parent_id            bigint NOT NULL DEFAULT 0,
	status               text NOT NULL DEFAULT 'pending',  -- pending, processing, on-hold, completed, cancelled, refunded, failed, checkout-draft
	currency             text NOT NULL DEFAULT 'USD',
	prices_include_tax   boolean NOT NULL DEFAULT false,
	customer_id          bigint NOT NULL DEFAULT 0,
	billing              jsonb NOT NULL DEFAULT '{}',
	shipping             jsonb NOT NULL DEFAULT '{}',
	billing_email        text NOT NULL DEFAULT '',
	payment_method       text NOT NULL DEFAULT '',
	payment_method_title text NOT NULL DEFAULT '',
	transaction_id       text NOT NULL DEFAULT '',
	customer_ip          text NOT NULL DEFAULT '',
	user_agent           text NOT NULL DEFAULT '',
	customer_note        text NOT NULL DEFAULT '',
	created_via          text NOT NULL DEFAULT 'checkout',
	cart_hash            text NOT NULL DEFAULT '',
	order_key            text NOT NULL DEFAULT '',
	discount_total       numeric(19,4) NOT NULL DEFAULT 0,
	discount_tax         numeric(19,4) NOT NULL DEFAULT 0,
	shipping_total       numeric(19,4) NOT NULL DEFAULT 0,
	shipping_tax         numeric(19,4) NOT NULL DEFAULT 0,
	cart_tax             numeric(19,4) NOT NULL DEFAULT 0,
	total                numeric(19,4) NOT NULL DEFAULT 0,
	total_tax            numeric(19,4) NOT NULL DEFAULT 0,
	refund_reason        text NOT NULL DEFAULT '',
	refunded_by          bigint NOT NULL DEFAULT 0,
	stock_reduced        boolean NOT NULL DEFAULT false,
	downloads_granted    boolean NOT NULL DEFAULT false,
	new_order_email_sent boolean NOT NULL DEFAULT false,
	meta                 jsonb NOT NULL DEFAULT '{}',
	date_created         timestamptz NOT NULL DEFAULT now(),
	date_modified        timestamptz NOT NULL DEFAULT now(),
	date_paid            timestamptz,
	date_completed       timestamptz
);
CREATE INDEX IF NOT EXISTS goo_orders_status ON goo_orders (type, status, date_created DESC);
CREATE INDEX IF NOT EXISTS goo_orders_customer ON goo_orders (customer_id);
CREATE INDEX IF NOT EXISTS goo_orders_email ON goo_orders (billing_email);
CREATE INDEX IF NOT EXISTS goo_orders_parent ON goo_orders (parent_id) WHERE parent_id <> 0;

CREATE TABLE IF NOT EXISTS goo_order_items (
	id           bigserial PRIMARY KEY,
	order_id     bigint NOT NULL REFERENCES goo_orders(id) ON DELETE CASCADE,
	type         text NOT NULL DEFAULT 'line_item',        -- line_item, shipping, fee, coupon, tax
	name         text NOT NULL DEFAULT '',
	product_id   bigint NOT NULL DEFAULT 0,
	variation_id bigint NOT NULL DEFAULT 0,
	quantity     numeric(19,4) NOT NULL DEFAULT 1,
	tax_class    text NOT NULL DEFAULT '',
	subtotal     numeric(19,4) NOT NULL DEFAULT 0,
	subtotal_tax numeric(19,4) NOT NULL DEFAULT 0,
	total        numeric(19,4) NOT NULL DEFAULT 0,
	total_tax    numeric(19,4) NOT NULL DEFAULT 0,
	taxes        jsonb NOT NULL DEFAULT '{}',              -- {"total":{"rate_id":amount},"subtotal":{...}}
	meta         jsonb NOT NULL DEFAULT '{}',              -- variation attributes, method_id, rate_id, coupon code, ...
	position     integer NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS goo_order_items_order ON goo_order_items (order_id, position);
CREATE INDEX IF NOT EXISTS goo_order_items_product ON goo_order_items (product_id);

CREATE TABLE IF NOT EXISTS goo_order_notes (
	id               bigserial PRIMARY KEY,
	order_id         bigint NOT NULL REFERENCES goo_orders(id) ON DELETE CASCADE,
	content          text NOT NULL,
	is_customer_note boolean NOT NULL DEFAULT false,
	added_by         text NOT NULL DEFAULT '',             -- '' = system
	created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_order_notes_order ON goo_order_notes (order_id, created_at DESC);

-- Coupons (Marketing › Coupons).
CREATE TABLE IF NOT EXISTS goo_coupons (
	id                          bigserial PRIMARY KEY,
	code                        text NOT NULL,
	description                 text NOT NULL DEFAULT '',
	status                      text NOT NULL DEFAULT 'publish', -- publish, draft, trash
	discount_type               text NOT NULL DEFAULT 'fixed_cart', -- percent, fixed_cart, fixed_product
	amount                      numeric(19,4) NOT NULL DEFAULT 0,
	date_expires                timestamptz,
	free_shipping               boolean NOT NULL DEFAULT false,
	minimum_amount              numeric(19,4),
	maximum_amount              numeric(19,4),
	individual_use              boolean NOT NULL DEFAULT false,
	exclude_sale_items          boolean NOT NULL DEFAULT false,
	product_ids                 bigint[] NOT NULL DEFAULT '{}',
	excluded_product_ids        bigint[] NOT NULL DEFAULT '{}',
	product_categories          bigint[] NOT NULL DEFAULT '{}',
	excluded_product_categories bigint[] NOT NULL DEFAULT '{}',
	email_restrictions          text[] NOT NULL DEFAULT '{}',
	usage_limit                 integer,
	usage_limit_per_user        integer,
	limit_usage_to_x_items      integer,
	usage_count                 integer NOT NULL DEFAULT 0,
	used_by                     text[] NOT NULL DEFAULT '{}',  -- user IDs or billing e-mails, one entry per use
	meta                        jsonb NOT NULL DEFAULT '{}',
	date_created                timestamptz NOT NULL DEFAULT now(),
	date_modified               timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS goo_coupons_code ON goo_coupons (lower(code)) WHERE status <> 'trash';

-- Tax rates (Settings › Tax › Standard / Reduced rate / Zero rate rates).
CREATE TABLE IF NOT EXISTS goo_tax_rates (
	id        bigserial PRIMARY KEY,
	country   text NOT NULL DEFAULT '',                    -- ISO 3166-1 alpha-2, '' = any
	state     text NOT NULL DEFAULT '',
	postcodes text[] NOT NULL DEFAULT '{}',                -- may use * wildcards and ranges (90210...90299)
	cities    text[] NOT NULL DEFAULT '{}',
	rate      numeric(9,4) NOT NULL DEFAULT 0,
	name      text NOT NULL DEFAULT 'Tax',
	priority  integer NOT NULL DEFAULT 1,
	compound  boolean NOT NULL DEFAULT false,
	shipping  boolean NOT NULL DEFAULT true,
	position  integer NOT NULL DEFAULT 0,
	class     text NOT NULL DEFAULT ''                     -- '' (standard), reduced-rate, zero-rate, custom slugs
);
CREATE INDEX IF NOT EXISTS goo_tax_rates_class ON goo_tax_rates (class, position);

-- Shipping zones (Settings › Shipping › Shipping zones) and their methods.
CREATE TABLE IF NOT EXISTS goo_shipping_zones (
	id        bigserial PRIMARY KEY,
	name      text NOT NULL,
	position  integer NOT NULL DEFAULT 0,
	locations jsonb NOT NULL DEFAULT '[]'                  -- [{"code":"US:CA","type":"state"}], types: country, state, continent, postcode
);
CREATE TABLE IF NOT EXISTS goo_shipping_methods (
	instance_id bigserial PRIMARY KEY,
	zone_id     bigint NOT NULL DEFAULT 0,                 -- 0 = "Locations not covered by your other zones"
	method_id   text NOT NULL,                             -- flat_rate, free_shipping, local_pickup, table_rate
	position    integer NOT NULL DEFAULT 0,
	enabled     boolean NOT NULL DEFAULT true,
	settings    jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS goo_shipping_methods_zone ON goo_shipping_methods (zone_id, position);

-- Cart sessions of visitors (wp_woocommerce_sessions).
CREATE TABLE IF NOT EXISTS goo_sessions (
	session_key text PRIMARY KEY,
	data        jsonb NOT NULL DEFAULT '{}',
	expires_at  timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS goo_sessions_expires ON goo_sessions (expires_at);

-- Downloadable product permissions.
CREATE TABLE IF NOT EXISTS goo_download_permissions (
	id                  bigserial PRIMARY KEY,
	download_id         text NOT NULL,
	product_id          bigint NOT NULL,
	order_id            bigint NOT NULL REFERENCES goo_orders(id) ON DELETE CASCADE,
	order_key           text NOT NULL DEFAULT '',
	user_email          text NOT NULL DEFAULT '',
	user_id             bigint NOT NULL DEFAULT 0,
	downloads_remaining integer,                           -- NULL = unlimited
	access_granted      timestamptz NOT NULL DEFAULT now(),
	access_expires      timestamptz,
	download_count      integer NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS goo_download_permissions_user ON goo_download_permissions (user_id);
CREATE INDEX IF NOT EXISTS goo_download_permissions_order ON goo_download_permissions (order_id);

-- REST API keys (Settings › Advanced › REST API) for wc/v3 consumer key / secret authentication.
CREATE TABLE IF NOT EXISTS goo_api_keys (
	id              bigserial PRIMARY KEY,
	user_id         bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	description     text NOT NULL DEFAULT '',
	permissions     text NOT NULL DEFAULT 'read',          -- read, write, read_write
	consumer_key    text NOT NULL,                         -- sha256 of the key
	consumer_secret text NOT NULL,
	truncated_key   text NOT NULL,
	last_access     timestamptz,
	created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS goo_api_keys_key ON goo_api_keys (consumer_key);

-- Webhooks (Settings › Advanced › Webhooks).
CREATE TABLE IF NOT EXISTS goo_webhooks (
	id           bigserial PRIMARY KEY,
	name         text NOT NULL,
	status       text NOT NULL DEFAULT 'active',            -- active, paused, disabled
	topic        text NOT NULL,                             -- order.created, product.updated, ...
	delivery_url text NOT NULL,
	secret       text NOT NULL DEFAULT '',
	user_id      bigint NOT NULL DEFAULT 0,
	failures     integer NOT NULL DEFAULT 0,
	created_at   timestamptz NOT NULL DEFAULT now()
);
