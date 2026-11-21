#!/usr/bin/env bash
# Seeds the test WordPress (runs in the WP-CLI container, see run.sh): users with known passwords,
# posts, pages, a media file, a menu, WooCommerce with a tax rate, 3 products (one variable), 2
# coupons, 2 customers and 3 orders (one with a refund). Then builds the package with the plugin's
# `wp pursae export` and writes what Pursae must end up with to /exports/expected.json.
set -euo pipefail
export WP_CLI_CACHE_DIR=/tmp/wp-cli-cache
cd /var/www/html

until [ -f wp-config.php ] && [ -f wp-includes/version.php ]; do sleep 1; done
until php -r 'exit(@mysqli_connect(getenv("WORDPRESS_DB_HOST"), getenv("WORDPRESS_DB_USER"), getenv("WORDPRESS_DB_PASSWORD"), getenv("WORDPRESS_DB_NAME")) ? 0 : 1);'; do sleep 2; done

wp core install --url="http://127.0.0.1:${WP2P_WP_PORT:-8089}" --title="WP2P Shop" --admin_user=wpadmin \
	--admin_password='Adm1n-pass-2026' --admin_email=wpadmin@example.test --skip-email
# No mail server here: WordPress and WooCommerce e-mails are dropped.
mkdir -p wp-content/mu-plugins
echo "<?php add_filter( 'pre_wp_mail', '__return_true' );" > wp-content/mu-plugins/wp2p-no-mail.php
wp option update blogdescription 'Moving to Pursae'
wp option update permalink_structure '/%postname%/'
wp option update timezone_string 'Europe/Berlin'

if [ -n "${WP2P_WOO_VERSION:-}" ]; then
	wp plugin install woocommerce --version="$WP2P_WOO_VERSION" --activate
else
	wp plugin install woocommerce --activate
fi
wp plugin activate wp-to-pursae
W="wp wc --user=wpadmin"

wp option update woocommerce_currency EUR
wp option update woocommerce_default_country 'DE:BE'
wp option update woocommerce_store_address 'Alexanderplatz 1'
wp option update woocommerce_store_city Berlin
wp option update woocommerce_store_postcode 10178
wp option update woocommerce_calc_taxes yes
wp option update woocommerce_prices_include_tax no
wp option update woocommerce_price_num_decimals 2

# Users with known passwords (run.sh signs in with them on Pursae).
EDITOR=$(wp user create editor1 editor1@example.test --role=editor --user_pass='Ed1tor-pass-2026' --first_name=Erin --last_name=Editor --porcelain)
wp user create author1 author1@example.test --role=author --user_pass='Auth0r-pass-2026' --display_name='Ann Author' --porcelain >/dev/null
C1=$($W customer create --email=customer1@example.test --username=customer1 --password='Cust0mer-1-pass' --first_name=Cora --last_name=Customer \
	--billing='{"first_name":"Cora","last_name":"Customer","address_1":"Unter den Linden 5","city":"Berlin","postcode":"10117","country":"DE","email":"customer1@example.test","phone":"030123"}' \
	--shipping='{"first_name":"Cora","last_name":"Customer","address_1":"Unter den Linden 5","city":"Berlin","postcode":"10117","country":"DE"}' --porcelain)
C2=$($W customer create --email=customer2@example.test --username=customer2 --password='Cust0mer-2-pass' --first_name=Carl --last_name=Client \
	--billing='{"first_name":"Carl","last_name":"Client","address_1":"Marienplatz 1","city":"Munich","postcode":"80331","country":"DE","email":"customer2@example.test"}' --porcelain)

# Content.
POST=$(wp post create --post_type=post --post_status=publish --post_author="$EDITOR" --post_title='Hello from WordPress' \
	--post_content='<!-- wp:paragraph --><p>This post moved from WordPress to Pursae.</p><!-- /wp:paragraph -->' --porcelain)
wp post create --post_type=post --post_status=draft --post_author="$EDITOR" --post_title='A draft' --post_content='Not yet.' --porcelain >/dev/null
ABOUT=$(wp post create --post_type=page --post_status=publish --post_title='About us' --post_name=about --post_content='<!-- wp:paragraph --><p>Who we are.</p><!-- /wp:paragraph -->' --porcelain)
wp post create --post_type=page --post_status=publish --post_title='Contact' --post_name=contact --post_content='Write to us.' --porcelain >/dev/null
wp comment create --comment_post_ID="$POST" --comment_content='Nice move!' --comment_author='Visitor' --comment_author_email=visitor@example.test --comment_approved=1 >/dev/null
wp media import /seed/pic.png --title='Pursae picture' --alt='A blue picture' --post_id="$POST" --featured_image --porcelain >/dev/null
wp menu create 'Main menu' >/dev/null
wp menu item add-post main-menu "$ABOUT" --title='About' >/dev/null
wp menu item add-custom main-menu 'Pursae' 'https://pursae.com/' >/dev/null

# Order storage: HPOS (the default of new stores) unless WP2P_ORDER_STORAGE=posts.
if [ "${WP2P_ORDER_STORAGE:-hpos}" = "hpos" ]; then
	wp wc hpos enable --ignore-plugin-compatibility >/dev/null 2>&1 || wp wc hpos enable >/dev/null
else
	wp wc hpos disable >/dev/null 2>&1 || true
fi

# Store.
$W tax create --country=DE --rate=19 --name=MwSt --priority=1 --shipping=true >/dev/null
TEE=$($W product create --name='Classic Tee' --type=simple --regular_price=20 --sku=TEE-1 --manage_stock=true --stock_quantity=50 --porcelain)
MUG=$($W product create --name='Pursae Mug' --type=simple --regular_price=12 --sku=MUG-1 --porcelain)
HOODIE=$($W product create --name='Hoodie' --type=variable --sku=HOODIE \
	--attributes='[{"name":"Color","options":["Blue","Red"],"variation":true,"visible":true}]' --porcelain)
BLUE=$($W product_variation create "$HOODIE" --regular_price=50 --sku=HOODIE-BLUE --attributes='[{"name":"Color","option":"Blue"}]' --porcelain)
$W product_variation create "$HOODIE" --regular_price=55 --sku=HOODIE-RED --attributes='[{"name":"Color","option":"Red"}]' --porcelain >/dev/null
$W shop_coupon create --code=summer10 --discount_type=percent --amount=10 --product_ids="[$TEE]" --usage_limit=100 --individual_use=true >/dev/null
$W shop_coupon create --code=welcome5 --discount_type=fixed_cart --amount=5 --minimum_amount=30 --date_expires='2030-12-31T23:59:59' --email_restrictions='["*@example.test"]' >/dev/null

BILL1='{"first_name":"Cora","last_name":"Customer","address_1":"Unter den Linden 5","city":"Berlin","postcode":"10117","country":"DE","email":"customer1@example.test"}'
O1=$($W shop_order create --customer_id="$C1" --status=completed --set_paid=true --payment_method=bacs --payment_method_title='Direct bank transfer' \
	--billing="$BILL1" --shipping="$BILL1" --line_items="[{\"product_id\":$TEE,\"quantity\":2}]" \
	--shipping_lines='[{"method_id":"flat_rate","method_title":"Flat rate","total":"5.00"}]' --porcelain)
# The coupon through the order API, so it really discounts (and counts a use).
wp eval "\$o = wc_get_order( $O1 ); \$r = \$o->apply_coupon( 'summer10' ); if ( is_wp_error( \$r ) ) { WP_CLI::error( \$r ); } \$o->save();"
O2=$($W shop_order create --customer_id="$C2" --status=processing --set_paid=true --payment_method=cod --payment_method_title='Cash on delivery' \
	--billing='{"first_name":"Carl","last_name":"Client","city":"Munich","postcode":"80331","country":"DE","email":"customer2@example.test"}' \
	--line_items="[{\"product_id\":$HOODIE,\"variation_id\":$BLUE,\"quantity\":1}]" --porcelain)
O3=$($W shop_order create --status=completed --set_paid=true --payment_method=bacs --payment_method_title='Direct bank transfer' \
	--billing='{"first_name":"Gus","last_name":"Guest","city":"Hamburg","postcode":"20095","country":"DE","email":"guest@example.test"}' \
	--line_items="[{\"product_id\":$MUG,\"quantity\":2}]" --porcelain)
wp wc order_note create "$O1" --note='Packed with care.' --customer_note=true --user=wpadmin >/dev/null
ITEM=$(wp eval "echo key( wc_get_order( $O3 )->get_items() );")
wp eval "\$r = wc_create_refund( array( 'order_id' => $O3, 'amount' => 12, 'reason' => 'One mug arrived broken', 'refund_payment' => false,
	'restock_items' => false, 'line_items' => array( $ITEM => array( 'qty' => 1, 'refund_total' => 12 ) ) ) ); if ( is_wp_error( \$r ) ) { WP_CLI::error( \$r ); }"
echo "Seeded: orders $O1 $O2 $O3"

# What Pursae must end up with.
wp eval-file /seed/expected.php > /exports/expected.json

# The package (the key goes to its own file, as the owner would copy it).
rm -f /exports/site.wp2p /exports/key.txt
wp pursae export --file=/exports/site.wp2p --porcelain > /exports/key.txt
echo "Package: $(stat -c %s /exports/site.wp2p) bytes"
