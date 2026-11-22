<?php
/**
 * WooCommerce data: tax rates, coupons, orders and customers.
 *
 * Orders are read through WooCommerce's own CRUD API (wc_get_orders(), WC_Order), which reads
 * whichever storage the store uses: the High-Performance Order Storage tables (wc_orders, ...) or
 * the older posts and postmeta. Order items and their meta live in the same tables in both cases
 * (woocommerce_order_items, woocommerce_order_itemmeta) and are exported raw, with WooCommerce's own
 * meta keys, which Pursae Commerce uses too.
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * WooCommerce export.
 */
class WP2P_Woo {

	/**
	 * Whether WooCommerce is active.
	 *
	 * @return bool
	 */
	public static function active() {
		return class_exists( 'WooCommerce' ) && function_exists( 'wc_get_orders' );
	}

	/**
	 * Where the orders are stored: "hpos" or "posts".
	 *
	 * @return string
	 */
	public static function order_storage() {
		if ( class_exists( '\Automattic\WooCommerce\Utilities\OrderUtil' ) && \Automattic\WooCommerce\Utilities\OrderUtil::custom_orders_table_usage_is_enabled() ) {
			return 'hpos';
		}
		return 'posts';
	}

	/**
	 * The order statuses that are exported (everything but drafts and the trash).
	 *
	 * @return string[]
	 */
	public static function order_statuses() {
		return array_keys( wc_get_order_statuses() );
	}

	/**
	 * Counts for the manifest.
	 *
	 * @return array
	 */
	public static function counts() {
		$orders = wc_get_orders(
			array(
				'type'     => 'shop_order',
				'status'   => self::order_statuses(),
				'limit'    => 1,
				'paginate' => true,
				'return'   => 'ids',
			)
		);
		return array(
			'orders'  => (int) $orders->total,
			'coupons' => (int) self::coupon_query( 0, 1, true ),
		);
	}

	/**
	 * Tax rates with their locations.
	 *
	 * @return array[]
	 */
	public static function tax_rates() {
		global $wpdb;
		// phpcs:ignore WordPress.DB.DirectDatabaseQuery -- WooCommerce's own tables, read once.
		$rates = $wpdb->get_results( "SELECT * FROM {$wpdb->prefix}woocommerce_tax_rates ORDER BY tax_rate_order, tax_rate_id", ARRAY_A );
		// phpcs:ignore WordPress.DB.DirectDatabaseQuery
		$locs = $wpdb->get_results( "SELECT tax_rate_id, location_code, location_type FROM {$wpdb->prefix}woocommerce_tax_rate_locations", ARRAY_A );
		$by   = array();
		foreach ( (array) $locs as $l ) {
			$by[ $l['tax_rate_id'] ][ 'postcode' === $l['location_type'] ? 'postcodes' : 'cities' ][] = $l['location_code'];
		}
		$out = array();
		foreach ( (array) $rates as $r ) {
			$out[] = array(
				'id'        => (int) $r['tax_rate_id'],
				'country'   => $r['tax_rate_country'],
				'state'     => $r['tax_rate_state'],
				'rate'      => $r['tax_rate'],
				'name'      => $r['tax_rate_name'],
				'priority'  => (int) $r['tax_rate_priority'],
				'compound'  => (bool) $r['tax_rate_compound'],
				'shipping'  => (bool) $r['tax_rate_shipping'],
				'order'     => (int) $r['tax_rate_order'],
				'class'     => $r['tax_rate_class'],
				'postcodes' => isset( $by[ $r['tax_rate_id'] ]['postcodes'] ) ? $by[ $r['tax_rate_id'] ]['postcodes'] : array(),
				'cities'    => isset( $by[ $r['tax_rate_id'] ]['cities'] ) ? $by[ $r['tax_rate_id'] ]['cities'] : array(),
			);
		}
		return $out;
	}

	/**
	 * Coupon ids after $after (or their count).
	 *
	 * @param int  $after Last id done.
	 * @param int  $limit Batch size.
	 * @param bool $count Count instead.
	 * @return int[]|int
	 */
	private static function coupon_query( $after, $limit, $count = false ) {
		global $wpdb;
		if ( $count ) {
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery
			return (int) $wpdb->get_var( "SELECT COUNT(*) FROM {$wpdb->posts} WHERE post_type = 'shop_coupon' AND post_status NOT IN ('trash', 'auto-draft')" );
		}
		// phpcs:ignore WordPress.DB.DirectDatabaseQuery
		return array_map( 'intval', $wpdb->get_col( $wpdb->prepare( "SELECT ID FROM {$wpdb->posts} WHERE post_type = 'shop_coupon' AND post_status NOT IN ('trash', 'auto-draft') AND ID > %d ORDER BY ID LIMIT %d", $after, $limit ) ) );
	}

	/**
	 * A batch of coupons.
	 *
	 * @param int $after Last id done.
	 * @param int $limit Batch size.
	 * @return array{rows: array[], last: int}
	 */
	public static function coupons( $after, $limit ) {
		$rows = array();
		$last = $after;
		foreach ( self::coupon_query( $after, $limit ) as $id ) {
			$last = $id;
			$c    = new WC_Coupon( $id );
			if ( ! $c->get_id() ) {
				continue;
			}
			$rows[] = array(
				'id'                          => $c->get_id(),
				'code'                        => $c->get_code(),
				'description'                 => $c->get_description(),
				'status'                      => get_post_status( $id ),
				'discount_type'               => $c->get_discount_type(),
				'amount'                      => (string) $c->get_amount(),
				'date_expires'                => self::ts( $c->get_date_expires() ),
				'free_shipping'               => $c->get_free_shipping(),
				'minimum_amount'              => (string) $c->get_minimum_amount(),
				'maximum_amount'              => (string) $c->get_maximum_amount(),
				'individual_use'              => $c->get_individual_use(),
				'exclude_sale_items'          => $c->get_exclude_sale_items(),
				'product_ids'                 => array_map( 'intval', $c->get_product_ids() ),
				'excluded_product_ids'        => array_map( 'intval', $c->get_excluded_product_ids() ),
				'product_categories'          => array_map( 'intval', $c->get_product_categories() ),
				'excluded_product_categories' => array_map( 'intval', $c->get_excluded_product_categories() ),
				'email_restrictions'          => array_values( $c->get_email_restrictions() ),
				'usage_limit'                 => (string) $c->get_usage_limit(),
				'usage_limit_per_user'        => (string) $c->get_usage_limit_per_user(),
				'limit_usage_to_x_items'      => (string) $c->get_limit_usage_to_x_items(),
				'usage_count'                 => (int) $c->get_usage_count(),
				'used_by'                     => array_values( array_map( 'strval', $c->get_used_by() ) ),
				'date_created'                => self::ts( $c->get_date_created() ),
				'date_modified'               => self::ts( $c->get_date_modified() ),
				'meta'                        => self::meta( $c->get_meta_data() ),
			);
		}
		return array(
			'rows' => $rows,
			'last' => $last,
		);
	}

	/**
	 * The ids of the next orders after $after, in id order, from whichever table holds them.
	 *
	 * @param int $after Last order id done.
	 * @param int $limit Batch size.
	 * @return int[]
	 */
	private static function order_ids( $after, $limit ) {
		global $wpdb;
		$statuses = self::order_statuses();
		$in       = implode( ',', array_fill( 0, count( $statuses ), '%s' ) );
		if ( 'hpos' === self::order_storage() ) {
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery, WordPress.DB.PreparedSQLPlaceholders.UnfinishedPrepare, WordPress.DB.PreparedSQL.InterpolatedNotPrepared -- placeholders built above.
			$ids = $wpdb->get_col( $wpdb->prepare( "SELECT id FROM {$wpdb->prefix}wc_orders WHERE type = 'shop_order' AND status IN ($in) AND id > %d ORDER BY id LIMIT %d", array_merge( $statuses, array( $after, $limit ) ) ) );
		} else {
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery, WordPress.DB.PreparedSQLPlaceholders.UnfinishedPrepare, WordPress.DB.PreparedSQL.InterpolatedNotPrepared -- placeholders built above.
			$ids = $wpdb->get_col( $wpdb->prepare( "SELECT ID FROM {$wpdb->posts} WHERE post_type = 'shop_order' AND post_status IN ($in) AND ID > %d ORDER BY ID LIMIT %d", array_merge( $statuses, array( $after, $limit ) ) ) );
		}
		return array_map( 'intval', (array) $ids );
	}

	/**
	 * A batch of orders after an order id (a cursor, not a page number: on a live store orders
	 * that are deleted or change status between requests cannot shift later pages).
	 *
	 * @param int $after Last order id done.
	 * @param int $limit Batch size.
	 * @return array{rows: array[], last: int}
	 */
	public static function orders( $after, $limit ) {
		$rows = array();
		$last = $after;
		foreach ( self::order_ids( $after, $limit ) as $id ) {
			$last = $id;
			$o    = wc_get_order( $id );
			if ( $o instanceof WC_Order && ! ( $o instanceof WC_Order_Refund ) ) {
				$rows[] = self::order( $o );
			}
		}
		return array(
			'rows' => $rows,
			'last' => $last,
		);
	}

	/**
	 * One order with its items, notes, refunds and download permissions.
	 *
	 * @param WC_Order $o Order.
	 * @return array
	 */
	public static function order( $o ) {
		$id  = $o->get_id();
		$row = array(
			'id'                    => $id,
			'status'                => $o->get_status(),
			'currency'              => $o->get_currency(),
			'prices_include_tax'    => $o->get_prices_include_tax(),
			'customer_id'           => $o->get_customer_id(),
			'billing'               => array_map( 'strval', (array) $o->get_address( 'billing' ) ),
			'shipping'              => array_map( 'strval', (array) $o->get_address( 'shipping' ) ),
			'payment_method'        => $o->get_payment_method(),
			'payment_method_title'  => $o->get_payment_method_title(),
			'transaction_id'        => $o->get_transaction_id(),
			'customer_ip'           => $o->get_customer_ip_address(),
			'user_agent'            => $o->get_customer_user_agent(),
			'customer_note'         => $o->get_customer_note(),
			'created_via'           => $o->get_created_via(),
			'cart_hash'             => $o->get_cart_hash(),
			'order_key'             => $o->get_order_key(),
			'discount_total'        => (string) $o->get_discount_total(),
			'discount_tax'          => (string) $o->get_discount_tax(),
			'shipping_total'        => (string) $o->get_shipping_total(),
			'shipping_tax'          => (string) $o->get_shipping_tax(),
			'cart_tax'              => (string) $o->get_cart_tax(),
			'total'                 => (string) $o->get_total(),
			'total_tax'             => (string) $o->get_total_tax(),
			'date_created'          => self::ts( $o->get_date_created() ),
			'date_modified'         => self::ts( $o->get_date_modified() ),
			'date_paid'             => self::ts( $o->get_date_paid() ),
			'date_completed'        => self::ts( $o->get_date_completed() ),
			'stock_reduced'         => self::flag( $o, 'get_order_stock_reduced', '_order_stock_reduced' ),
			'downloads_granted'     => self::flag( $o, 'get_download_permissions_granted', '_download_permissions_granted' ),
			'new_order_email_sent'  => self::flag( $o, 'get_new_order_email_sent', '_new_order_email_sent' ),
			'recorded_sales'        => self::flag( $o, 'get_recorded_sales', '_recorded_sales' ),
			'recorded_coupon_usage' => self::flag( $o, 'get_recorded_coupon_usage_counts', '_recorded_coupon_usage_counts' ),
			'meta'                  => self::meta( $o->get_meta_data() ),
			'items'                 => self::items( $o ),
			'notes'                 => array(),
			'refunds'               => array(),
			'downloads'             => array(),
		);
		foreach ( wc_get_order_notes(
			array(
				'order_id' => $id,
				'orderby'  => 'date_created',
				'order'    => 'ASC',
			)
		) as $n ) {
			$row['notes'][] = array(
				'content'       => $n->content,
				'customer_note' => (bool) $n->customer_note,
				'added_by'      => (string) $n->added_by,
				'date'          => self::ts( $n->date_created ),
			);
		}
		foreach ( $o->get_refunds() as $r ) {
			$row['refunds'][] = array(
				'id'               => $r->get_id(),
				'amount'           => (string) $r->get_amount(),
				'reason'           => $r->get_reason(),
				'refunded_by'      => (int) $r->get_refunded_by(),
				'refunded_payment' => (bool) $r->get_refunded_payment(),
				'date_created'     => self::ts( $r->get_date_created() ),
				'shipping_total'   => (string) $r->get_shipping_total(),
				'shipping_tax'     => (string) $r->get_shipping_tax(),
				'cart_tax'         => (string) $r->get_cart_tax(),
				'discount_total'   => (string) $r->get_discount_total(),
				'discount_tax'     => (string) $r->get_discount_tax(),
				'total_tax'        => (string) $r->get_total_tax(),
				'meta'             => self::meta( $r->get_meta_data() ),
				'items'            => self::items( $r ),
			);
		}
		global $wpdb;
		// phpcs:ignore WordPress.DB.DirectDatabaseQuery -- WooCommerce's permissions table.
		$perms = $wpdb->get_results( $wpdb->prepare( "SELECT * FROM {$wpdb->prefix}woocommerce_downloadable_product_permissions WHERE order_id = %d", $id ), ARRAY_A );
		foreach ( (array) $perms as $p ) {
			$row['downloads'][] = array(
				'download_id'         => $p['download_id'],
				'product_id'          => (int) $p['product_id'],
				'user_email'          => $p['user_email'],
				'user_id'             => (int) $p['user_id'],
				'downloads_remaining' => (string) $p['downloads_remaining'],
				'access_granted'      => $p['access_granted'] ? strtotime( $p['access_granted'] . ' UTC' ) : 0,
				'access_expires'      => $p['access_expires'] ? strtotime( $p['access_expires'] . ' UTC' ) : 0,
				'download_count'      => (int) $p['download_count'],
			);
		}
		return $row;
	}

	/**
	 * The items of an order or refund with their raw meta.
	 *
	 * @param WC_Abstract_Order $o Order or refund.
	 * @return array[]
	 */
	private static function items( $o ) {
		global $wpdb;
		$items = $o->get_items( array( 'line_item', 'fee', 'shipping', 'coupon', 'tax' ) );
		if ( ! $items ) {
			return array();
		}
		$ids  = array_map( 'intval', array_keys( $items ) );
		$in   = implode( ',', $ids );
		$meta = array();
		// phpcs:ignore WordPress.DB.DirectDatabaseQuery, WordPress.DB.PreparedSQL.InterpolatedNotPrepared -- integer ids.
		foreach ( (array) $wpdb->get_results( "SELECT order_item_id, meta_key, meta_value FROM {$wpdb->prefix}woocommerce_order_itemmeta WHERE order_item_id IN ($in) ORDER BY meta_id", ARRAY_A ) as $m ) {
			$meta[ $m['order_item_id'] ][ $m['meta_key'] ] = self::plain( maybe_unserialize( $m['meta_value'] ) );
		}
		$out = array();
		foreach ( $items as $item_id => $item ) {
			$out[] = array(
				'id'   => (int) $item_id,
				'type' => $item->get_type(),
				'name' => $item->get_name(),
				'meta' => isset( $meta[ $item_id ] ) ? $meta[ $item_id ] : new stdClass(),
			);
		}
		return $out;
	}

	/**
	 * A batch of customers: users with the customer role, then the guest customers WooCommerce
	 * Analytics knows (wc_customer_lookup), which Pursae lists from their orders.
	 *
	 * @param int $after Last user id done; once the users are done, -1 - the last guest's
	 *                   customer_id in wc_customer_lookup (so -1 starts the guests).
	 * @param int $limit Batch size.
	 * @return array{rows: array[], last: int, done: bool}
	 */
	public static function customers( $after, $limit ) {
		$rows = array();
		if ( $after >= 0 ) {
			global $wpdb;
			// Users of this site with the customer role (wp_capabilities holds the serialized role list).
			$ids = $wpdb->get_col( $wpdb->prepare( "SELECT u.ID FROM {$wpdb->users} u INNER JOIN {$wpdb->usermeta} m ON m.user_id = u.ID AND m.meta_key = %s WHERE m.meta_value LIKE %s AND u.ID > %d ORDER BY u.ID LIMIT %d", $wpdb->get_blog_prefix() . 'capabilities', '%"customer"%', $after, $limit ) ); // phpcs:ignore WordPress.DB.DirectDatabaseQuery
			if ( ! $ids ) {
				return array(
					'rows' => array(),
					'last' => -1,
					'done' => ! self::has_lookup(),
				);
			}
			foreach ( $ids as $id ) {
				$c      = new WC_Customer( (int) $id );
				$rows[] = array(
					'user_id'            => (int) $id,
					'email'              => $c->get_email(),
					'billing'            => array_map( 'strval', (array) $c->get_billing() ),
					'shipping'           => array_map( 'strval', (array) $c->get_shipping() ),
					'is_paying_customer' => (bool) $c->get_is_paying_customer(),
					'last_active'        => (int) get_user_meta( (int) $id, 'wc_last_active', true ),
				);
				$after  = (int) $id;
			}
			return array(
				'rows' => $rows,
				'last' => $after,
				'done' => false,
			);
		}
		// Guests, in batches by customer_id like the users.
		global $wpdb;
		$from = -1 - $after;
		// phpcs:ignore WordPress.DB.DirectDatabaseQuery
		$guests = $wpdb->get_results( $wpdb->prepare( "SELECT customer_id, email FROM {$wpdb->prefix}wc_customer_lookup WHERE user_id IS NULL AND customer_id > %d ORDER BY customer_id LIMIT %d", $from, $limit ), ARRAY_A );
		foreach ( (array) $guests as $g ) {
			$rows[] = array(
				'user_id' => 0,
				'email'   => $g['email'],
			);
			$from   = (int) $g['customer_id'];
		}
		return array(
			'rows' => $rows,
			'last' => -1 - $from,
			'done' => count( (array) $guests ) < $limit,
		);
	}

	/**
	 * Whether WooCommerce Analytics' customer table exists.
	 *
	 * @return bool
	 */
	private static function has_lookup() {
		global $wpdb;
		$table = $wpdb->prefix . 'wc_customer_lookup';
		return $table === $wpdb->get_var( $wpdb->prepare( 'SHOW TABLES LIKE %s', $table ) ); // phpcs:ignore WordPress.DB.DirectDatabaseQuery
	}

	/**
	 * Custom meta (WC_Meta_Data) as key => value; WooCommerce's internal keys are not in it.
	 *
	 * @param WC_Meta_Data[] $data Meta.
	 * @return array|stdClass
	 */
	private static function meta( $data ) {
		$out = array();
		foreach ( (array) $data as $m ) {
			$d = $m->get_data();
			if ( '' === (string) $d['key'] ) {
				continue;
			}
			$out[ $d['key'] ] = self::plain( $d['value'] );
		}
		return $out ? $out : new stdClass();
	}

	/**
	 * A value JSON can carry (objects become arrays).
	 *
	 * @param mixed $v Value.
	 * @return mixed
	 */
	private static function plain( $v ) {
		if ( is_object( $v ) ) {
			$v = get_object_vars( $v );
		}
		if ( is_array( $v ) ) {
			return array_map( array( __CLASS__, 'plain' ), $v );
		}
		return $v;
	}

	/**
	 * A WC_DateTime as a Unix timestamp (0 for none).
	 *
	 * @param WC_DateTime|null $d Date.
	 * @return int
	 */
	private static function ts( $d ) {
		return $d ? (int) $d->getTimestamp() : 0;
	}

	/**
	 * A bookkeeping flag of an order, by its getter or its meta key on older WooCommerce versions.
	 *
	 * @param WC_Order $o      Order.
	 * @param string   $getter Getter name.
	 * @param string   $key    Meta key.
	 * @return bool
	 */
	private static function flag( $o, $getter, $key ) {
		if ( is_callable( array( $o, $getter ) ) ) {
			return wc_string_to_bool( $o->$getter() );
		}
		return wc_string_to_bool( $o->get_meta( $key, true ) );
	}
}
