<?php
/**
 * Plugin Name:       WP to Pursae
 * Plugin URI:        https://pursae.com/docs/operations/migrate-from-wordpress/
 * Description:       Move this WordPress site to Pursae in one go: users (with their passwords), content, media, menus, settings and the WooCommerce store with its orders, customers and coupons.
 * Version:           1.0.0
 * Requires at least: 6.0
 * Requires PHP:      7.4
 * Author:            Pursae
 * Author URI:        https://pursae.com/
 * License:           GPL-2.0-or-later
 * License URI:       https://www.gnu.org/licenses/gpl-2.0.html
 * Text Domain:       wp-to-pursae
 * Domain Path:       /languages
 *
 * @package WP_To_Pursae
 */

/*
 * WP to Pursae is free software: you can redistribute it and/or modify it under the terms of the GNU
 * General Public License as published by the Free Software Foundation, either version 2 of the
 * License, or (at your option) any later version.
 *
 * It is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the
 * implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public
 * License for more details.
 */

defined( 'ABSPATH' ) || exit;

define( 'WP2P_VERSION', '1.0.0' );
define( 'WP2P_FILE', __FILE__ );
define( 'WP2P_DIR', plugin_dir_path( __FILE__ ) );

require_once WP2P_DIR . 'includes/class-wp2p-crypto.php';
require_once WP2P_DIR . 'includes/class-wp2p-writer.php';
require_once WP2P_DIR . 'includes/class-wp2p-woo.php';
require_once WP2P_DIR . 'includes/class-wp2p-exporter.php';
require_once WP2P_DIR . 'includes/class-wp2p-pusher.php';

if ( is_admin() ) {
	require_once WP2P_DIR . 'includes/class-wp2p-admin.php';
	WP2P_Admin::init();
}

if ( defined( 'WP_CLI' ) && WP_CLI ) {
	require_once WP2P_DIR . 'includes/class-wp2p-cli.php';
	WP_CLI::add_command( 'pursae', 'WP2P_CLI' );
}

add_action( 'init', 'wp2p_load_textdomain' );

/**
 * Loads the plugin's translations.
 */
function wp2p_load_textdomain() {
	load_plugin_textdomain( 'wp-to-pursae', false, dirname( plugin_basename( WP2P_FILE ) ) . '/languages' );
}

// A package that was built but not sent or deleted is removed after a day.
add_action( 'wp2p_cleanup', array( 'WP2P_Exporter', 'cleanup_expired' ) );

register_deactivation_hook( __FILE__, 'wp2p_deactivate' );

/**
 * Removes any package and its working files when the plugin is deactivated.
 */
function wp2p_deactivate() {
	WP2P_Exporter::discard();
	wp_clear_scheduled_hook( 'wp2p_cleanup' );
}
