<?php
/**
 * Removes the plugin's data: an unfinished package and its job.
 *
 * @package WP_To_Pursae
 */

defined( 'WP_UNINSTALL_PLUGIN' ) || exit;

require_once __DIR__ . '/includes/class-wp2p-crypto.php';
require_once __DIR__ . '/includes/class-wp2p-writer.php';
require_once __DIR__ . '/includes/class-wp2p-exporter.php';

WP2P_Exporter::discard();
wp_clear_scheduled_hook( 'wp2p_cleanup' );
