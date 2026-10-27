<?php
// wp --user=labadmin eval-file wpgo-dump/make-kit.php <spec.json>: creates/updates an inactive kit
// with the given settings and prints WPGOID:<id> plus its post CSS to wpgo-dump/kit-<name>.css.
use Elementor\Plugin;
$spec = json_decode( file_get_contents( ABSPATH . $args[0] ), true );
$slug = 'wpgo-kit-' . $spec['name'];
$existing = get_page_by_path( $slug, OBJECT, 'elementor_library' );
if ( $existing ) {
	$id = $existing->ID;
} else {
	$id = wp_insert_post( [ 'post_type' => 'elementor_library', 'post_status' => 'publish', 'post_title' => 'WPGO kit ' . $spec['name'], 'post_name' => $slug ] );
	update_post_meta( $id, '_elementor_template_type', 'kit' );
	update_post_meta( $id, '_elementor_edit_mode', 'builder' );
	wp_set_object_terms( $id, 'kit', 'elementor_library_type' );
}
update_post_meta( $id, '_elementor_page_settings', $spec['settings'] );
update_post_meta( $id, '_elementor_data', '[]' );
Plugin::$instance->documents->get( $id, false );
$css = \Elementor\Core\Files\CSS\Post::create( $id );
file_put_contents( __DIR__ . '/kit-' . $spec['name'] . '.css', $css->get_content() );
echo "WPGOID:", $id, "\n";
