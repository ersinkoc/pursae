<?php
$out = [];
foreach ( get_posts( [ 'post_type' => 'attachment', 'numberposts' => -1, 'post_status' => 'any' ] ) as $p ) {
	$meta = wp_get_attachment_metadata( $p->ID );
	$full = wp_get_attachment_url( $p->ID );
	$a = [ 'ID' => $p->ID, 'URL' => $full, 'Width' => $meta['width'] ?? 0, 'Height' => $meta['height'] ?? 0,
		'Alt' => get_post_meta( $p->ID, '_wp_attachment_image_alt', true ), 'Title' => $p->post_title,
		'Caption' => $p->post_excerpt, 'Description' => $p->post_content, 'MimeType' => $p->post_mime_type, 'Sizes' => [] ];
	$base = dirname( $full ) . '/';
	foreach ( ( $meta['sizes'] ?? [] ) as $name => $s ) {
		$a['Sizes'][] = [ 'Name' => $name, 'URL' => $base . $s['file'], 'Width' => $s['width'], 'Height' => $s['height'], 'MimeType' => $s['mime-type'] ];
	}
	$out[] = $a;
}
file_put_contents( __DIR__ . '/attachments.json', wp_json_encode( $out, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES ) );
echo "ok\n";
