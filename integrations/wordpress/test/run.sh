#!/usr/bin/env bash
# WP to Pursae end to end (make wp2p-test): a real WordPress + WooCommerce (docker-compose.yml) is
# seeded (seed/seed.sh), the plugin builds a package with `wp pursae export`, a Pursae binary built
# from this checkout imports it into its own database (wpgo_wp2p on the development Postgres of
# docker/compose.yaml, never the shared wpgo database), and the result is checked: users sign in with
# their WordPress passwords, content, media, menus, products, orders, refunds, coupons and customers
# match. Then the same site is sent again with the plugin's push mode (`wp pursae push` with a
# migration token) and nothing may be duplicated.
#
# Needs Docker, Go, curl and jq, and internet access (WordPress images, WooCommerce from wordpress.org).
# KEEP=1 leaves the containers and Pursae running for a look; WP2P_WOO_VERSION pins WooCommerce.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
TEST="$ROOT/integrations/wordpress/test"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/wp2p-e2e.XXXXXX")
PORT=${WP2P_PURSAE_PORT:-18089}
DB=wpgo_wp2p
PGPORT=${WPGO_DB_PORT:-5432}
COMPOSE=(docker compose -f "$TEST/docker-compose.yml")
PG=(docker compose -f "$ROOT/docker/compose.yaml")
PURSAE="$WORK/pursae"
PASS=0
FAIL=0
SERVER_PID=""

cleanup() {
	if [ -n "$SERVER_PID" ]; then kill "$SERVER_PID" 2>/dev/null || true; fi
	if [ "${KEEP:-}" != "1" ]; then
		"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
		rm -rf "$WORK"
	else
		echo "KEEP=1: WordPress on http://127.0.0.1:${WP2P_WP_PORT:-8089}, files in $WORK"
	fi
}
trap cleanup EXIT

ok() { PASS=$((PASS + 1)); echo "  ok   $*"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL $*"; }
check() { # check <description> <got> <want>
	if [ "$2" == "$3" ]; then ok "$1"; else bad "$1: got '$2', want '$3'"; fi
}
sql() { "${PG[@]}" exec -T db psql -U wpgo -d "$DB" -Atc "$1"; }

echo "== Pursae binary"
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$PURSAE" ./cmd/wpgo)

echo "== Pursae database $DB"
"${PG[@]}" up -d --wait db >/dev/null
"${PG[@]}" exec -T db psql -U wpgo -d wpgo -qc "DROP DATABASE IF EXISTS $DB WITH (FORCE)" -c "CREATE DATABASE $DB" >/dev/null
export DATABASE_URL="postgres://wpgo:wpgo@localhost:$PGPORT/$DB?sslmode=disable"
export WPGO_UPLOADS_DIR="$WORK/uploads" WPGO_SITE_URL="http://127.0.0.1:$PORT" WPGO_ADDR=":$PORT" WPGO_HUB=off WPGO_MAIL_DIR="$WORK/mail"
"$PURSAE" db migrate >/dev/null
"$PURSAE" core install --url="$WPGO_SITE_URL" --title="Pursae" --admin_user=admin --admin_email=admin@pursae.test --admin_password='Pursae-admin-2026' >/dev/null

echo "== WordPress + WooCommerce"
mkdir -p "$TEST/exports"
chmod 777 "$TEST/exports"
rm -f "$TEST/exports/"*
"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
"${COMPOSE[@]}" up -d --wait db wordpress >/dev/null
"${COMPOSE[@]}" up -d cli >/dev/null
"${COMPOSE[@]}" exec -T -e WP2P_WP_PORT="${WP2P_WP_PORT:-8089}" -e WP2P_WOO_VERSION="${WP2P_WOO_VERSION:-}" -e WP2P_ORDER_STORAGE="${WP2P_ORDER_STORAGE:-hpos}" cli bash /seed/seed.sh
EXP="$TEST/exports/expected.json"
KEY=$(tr -d '\r\n' <"$TEST/exports/key.txt")
echo "   order storage: $(jq -r .storage "$EXP")"

echo "== Import with the wrong key"
if "$PURSAE" import wordpress "$TEST/exports/site.wp2p" --key="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" >/dev/null 2>&1; then
	bad "a wrong key was accepted"
else
	ok "a wrong key is refused"
fi

echo "== pursae import wordpress"
"$PURSAE" import wordpress "$TEST/exports/site.wp2p" --key="$KEY" | tee "$WORK/import.log"

echo "== Checks"
# Users and passwords.
for login in $(jq -r '.users | keys[]' "$EXP"); do
	check "user $login e-mail" "$(sql "SELECT email FROM users WHERE login = '$login'")" "$(jq -r ".users[\"$login\"].email" "$EXP")"
	role=$(jq -r ".users[\"$login\"].roles[0]" "$EXP")
	check "user $login role" "$(sql "SELECT r.slug FROM users u JOIN user_roles ur ON ur.user_id = u.id AND ur.is_primary JOIN roles r ON r.id = ur.role_id WHERE u.login = '$login'")" "$role"
done
check "the Pursae administrator is untouched" "$(sql "SELECT email FROM users WHERE login = 'admin'")" "admin@pursae.test"

if curl -fs "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
	echo "Something already listens on port $PORT: stop it or set WP2P_PURSAE_PORT." >&2
	exit 1
fi
"$PURSAE" serve >"$WORK/serve.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 60); do curl -fs "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 1; done
login() { # login <user> <password> -> HTTP status of the sign-in
	curl -s -o /dev/null -w '%{http_code}' -c /dev/null --data-urlencode "log=$1" --data-urlencode "pwd=$2" "http://127.0.0.1:$PORT/wp-login"
}
for pair in "wpadmin:Adm1n-pass-2026" "editor1:Ed1tor-pass-2026" "author1:Auth0r-pass-2026" "customer1:Cust0mer-1-pass" "customer2:Cust0mer-2-pass"; do
	user=${pair%%:*}
	check "$user signs in with the WordPress password" "$(login "$user" "${pair#*:}")" "302"
done
check "a wrong password is refused" "$(login customer1 wrong-password)" "200"
check "the WordPress hash became argon2id at sign-in" "$(sql "SELECT left(password_hash, 10) FROM users WHERE login = 'customer1'")" '$argon2id$'

# Content, media, menus.
check "published post" "$(sql "SELECT status FROM posts WHERE type = 'post' AND title = 'Hello from WordPress'")" "publish"
check "draft post" "$(sql "SELECT status FROM posts WHERE type = 'post' AND title = 'A draft'")" "draft"
check "post author" "$(sql "SELECT u.login FROM posts p JOIN users u ON u.id = p.author_id WHERE p.title = 'Hello from WordPress'")" "editor1"
check "pages" "$(sql "SELECT string_agg(slug, ',' ORDER BY slug) FROM posts WHERE type = 'page' AND slug IN ('about', 'contact')")" "about,contact"
check "comment" "$(sql "SELECT count(*) FROM comments c JOIN posts p ON p.id = c.post_id WHERE p.title = 'Hello from WordPress' AND c.content = 'Nice move!'")" "1"
media=$(sql "SELECT file_path FROM media WHERE title = 'Pursae picture'")
if [ -n "$media" ] && [ -f "$WPGO_UPLOADS_DIR/$media" ]; then ok "media file $media"; else bad "media file missing ($media)"; fi
check "media alt text" "$(sql "SELECT alt_text FROM media WHERE title = 'Pursae picture'")" "A blue picture"
check "featured image" "$(sql "SELECT count(*) FROM posts p JOIN media m ON m.id = p.featured_media_id WHERE p.title = 'Hello from WordPress' AND m.title = 'Pursae picture'")" "1"
check "menu items" "$(sql "SELECT string_agg(i.label, ',' ORDER BY i.sort_order) FROM menu_items i JOIN menus m ON m.id = i.menu_id WHERE m.name = 'Main menu'")" "About,Pursae"
check "menu item points at the page" "$(sql "SELECT p.slug FROM menu_items i JOIN posts p ON p.id = i.target_id WHERE i.label = 'About'")" "about"
check "site title" "$(sql "SELECT value #>> '{}' FROM settings WHERE key = 'blogname'")" "WP2P Shop"
check "the cart page shows Pursae Commerce's cart" "$(sql "SELECT count(*) FROM posts WHERE id = (SELECT (value #>> '{}')::bigint FROM settings WHERE key = 'goocommerce_cart_page_id') AND content LIKE '%[goocommerce_cart]%'")" "1"
check "store currency" "$(sql "SELECT value #>> '{}' FROM settings WHERE key = 'goocommerce_currency'")" "EUR"

# Products.
check "products" "$(sql "SELECT count(*) FROM posts WHERE type = 'product'")" "3"
check "variations" "$(sql "SELECT count(*) FROM posts WHERE type = 'product_variation'")" "2"
check "tee price" "$(sql "SELECT regular_price::float8 FROM goo_products WHERE sku = 'TEE-1'")" "20"
check "blue hoodie price" "$(sql "SELECT regular_price::float8 FROM goo_products WHERE sku = 'HOODIE-BLUE'")" "50"

# Orders.
check "orders" "$(sql "SELECT count(*) FROM goo_orders WHERE type = 'shop_order'")" "$(jq '.orders | length' "$EXP")"
check "refunds" "$(sql "SELECT count(*) FROM goo_orders WHERE type = 'shop_order_refund'")" "1"
n=$(jq '.orders | length' "$EXP")
for i in $(seq 0 $((n - 1))); do
	o=$(jq -c ".orders[$i]" "$EXP")
	wid=$(jq -r .id <<<"$o")
	row=$(sql "SELECT o.status, o.total::float8, o.total_tax::float8, o.discount_total::float8, o.shipping_total::float8, o.billing_email, o.order_key,
		coalesce((SELECT u.login FROM users u WHERE u.id = o.customer_id), ''),
		(SELECT count(*) FROM goo_order_items i WHERE i.order_id = o.id AND i.type = 'line_item'),
		(SELECT count(*) FROM goo_order_items i WHERE i.order_id = o.id),
		coalesce((SELECT sum(-r.total)::float8 FROM goo_orders r WHERE r.parent_id = o.id AND r.type = 'shop_order_refund'), 0)
		FROM goo_orders o WHERE (o.meta->>'_wp2p_order_id')::bigint = $wid")
	want=$(jq -r '[.status, .total, .total_tax, .discount_total, .shipping_total, .email, .key, .customer, .lines, .item_types, .refunded] | map(tostring) | join("|")' <<<"$o")
	check "order #$wid (status|total|tax|discount|shipping|email|key|customer|lines|items|refunded)" "$row" "$want"
done
check "order item product link" "$(sql "SELECT count(*) FROM goo_order_items i JOIN goo_products p ON p.post_id = i.product_id WHERE i.type = 'line_item' AND p.sku = 'TEE-1'")" "1"
check "order item variation link" "$(sql "SELECT count(*) FROM goo_order_items i JOIN goo_products p ON p.post_id = i.variation_id WHERE p.sku = 'HOODIE-BLUE'")" "1"
check "customer note" "$(sql "SELECT count(*) FROM goo_order_notes WHERE content = 'Packed with care.' AND is_customer_note")" "1"
check "tax line rate" "$(sql "SELECT count(*) FROM goo_order_items i JOIN goo_tax_rates r ON r.id = (i.meta->>'rate_id')::bigint WHERE i.type = 'tax' AND r.name = 'MwSt'")" "$(sql "SELECT count(*) FROM goo_order_items WHERE type = 'tax'")"

# Coupons and customers.
for code in $(jq -r '.coupons | keys[]' "$EXP"); do
	c=$(jq -c ".coupons[\"$code\"]" "$EXP")
	check "coupon $code (type|amount|usage|limit)" "$(sql "SELECT discount_type, amount::float8, usage_count, coalesce(usage_limit::text, '') FROM goo_coupons WHERE code = '$code'")" \
		"$(jq -r '[.type, .amount, .usage_count, (if (.usage_limit == null or .usage_limit == 0) then "" else .usage_limit end)] | map(tostring) | join("|")' <<<"$c")"
done
check "coupon product restriction" "$(sql "SELECT count(*) FROM goo_coupons c JOIN goo_products p ON p.post_id = ANY(c.product_ids) WHERE c.code = 'summer10' AND p.sku = 'TEE-1'")" "1"
check "customer billing address" "$(sql "SELECT meta->>'billing_city' FROM users WHERE login = 'customer1'")" "Berlin"

echo "== Push mode (wp pursae push with a migration token), into the same site: nothing may be duplicated"
counts() { sql "SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM posts), (SELECT count(*) FROM media), (SELECT count(*) FROM menu_items), (SELECT count(*) FROM goo_orders), (SELECT count(*) FROM goo_coupons), (SELECT count(*) FROM goo_order_items)"; }
before=$(counts)
TOKEN=$("$PURSAE" import wordpress-token --porcelain)
"${COMPOSE[@]}" exec -T cli wp pursae push --to="http://host.docker.internal:$PORT" --token="$TOKEN"
state=""
for _ in $(seq 1 120); do
	state=$(sql "SELECT state FROM wp2p_migrations WHERE token_hash <> '' ORDER BY created_at DESC LIMIT 1")
	case "$state" in done | failed) break ;; esac
	sleep 1
done
check "pushed import finished" "$state" "done"
check "nothing duplicated (users|posts|media|menu items|orders|coupons|items)" "$(counts)" "$before"
check "the token cannot be used again" "$(curl -s -o /dev/null -w '%{http_code}' -H "X-Pursae-Migration-Token: $TOKEN" -H 'Content-Type: application/json' -d '{"size":10}' "http://127.0.0.1:$PORT/wp-json/pursae/v1/migrate/start")" "409"
check "the plugin deleted its package" "$("${COMPOSE[@]}" exec -T cli sh -c 'ls -d /var/www/html/wp-content/uploads/wp-to-pursae-* 2>/dev/null | wc -l' | tr -d ' ')" "0"

echo
echo "WP to Pursae e2e: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
