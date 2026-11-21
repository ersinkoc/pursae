<?php
/**
 * Prints what the seeded WordPress holds, for run.sh to compare with Pursae (wp eval-file).
 *
 * @package WP_To_Pursae
 */

$out = array(
	'orders'  => array(),
	'coupons' => array(),
	'users'   => array(),
);
foreach ( wc_get_orders(
	array(
		'type'    => 'shop_order',
		'limit'   => -1,
		'orderby' => 'ID',
		'order'   => 'ASC',
	)
) as $o ) {
	$items = array();
	foreach ( $o->get_items() as $it ) {
		$items[] = array(
			'name' => $it->get_name(),
			'qty'  => $it->get_quantity(),
		);
	}
	$out['orders'][] = array(
		'id'             => $o->get_id(),
		'status'         => $o->get_status(),
		'total'          => (float) $o->get_total(),
		'total_tax'      => (float) $o->get_total_tax(),
		'discount_total' => (float) $o->get_discount_total(),
		'shipping_total' => (float) $o->get_shipping_total(),
		'refunded'       => (float) $o->get_total_refunded(),
		'email'          => $o->get_billing_email(),
		'customer'       => $o->get_customer_id() ? get_userdata( $o->get_customer_id() )->user_login : '',
		'lines'          => count( $o->get_items() ),
		'item_types'     => count( $o->get_items( array( 'line_item', 'fee', 'shipping', 'coupon', 'tax' ) ) ),
		'items'          => $items,
		'key'            => $o->get_order_key(),
	);
}
foreach ( get_posts(
	array(
		'post_type'   => 'shop_coupon',
		'numberposts' => -1,
	)
) as $p ) {
	$c                                 = new WC_Coupon( $p->ID );
	$out['coupons'][ $c->get_code() ] = array(
		'type'        => $c->get_discount_type(),
		'amount'      => (float) $c->get_amount(),
		'usage_count' => $c->get_usage_count(),
		'usage_limit' => $c->get_usage_limit(),
	);
}
foreach ( get_users() as $u ) {
	$out['users'][ $u->user_login ] = array(
		'roles' => array_values( $u->roles ),
		'email' => $u->user_email,
	);
}
$out['storage'] = \Automattic\WooCommerce\Utilities\OrderUtil::custom_orders_table_usage_is_enabled() ? 'hpos' : 'posts';
echo wp_json_encode( $out, JSON_PRETTY_PRINT ) . "\n";
