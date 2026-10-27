<?php
// wp --user=labadmin eval-file wpgo-dump/make-fixture.php <spec.json path relative to wp root>
use Elementor\Plugin;
$spec = json_decode( file_get_contents( ABSPATH . $args[0] ), true );
$slug = 'wpgo-fx-' . $spec['name'];
$existing = get_page_by_path( $slug, OBJECT, 'page' );
if ( $existing ) {
	$id = $existing->ID;
} else {
	$id = wp_insert_post( [ 'post_type' => 'page', 'post_status' => 'publish', 'post_title' => 'WPGO fixture ' . $spec['name'], 'post_name' => $slug ] );
}
update_post_meta( $id, '_elementor_edit_mode', 'builder' );
update_post_meta( $id, '_elementor_template_type', 'wp-page' );
$doc = Plugin::$instance->documents->get( $id, false );
$settings = $spec['settings'] ?? [];
$settings['post_status'] = 'publish';
$doc->save( [ 'elements' => $spec['elements'], 'settings' => $settings ] );
Plugin::$instance->files_manager->clear_cache();
echo "WPGOID:", $id, "\n";
