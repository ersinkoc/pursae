<?php
/**
 * Builds the static stylesheets of the Twenty Twenty-Five port (web/themes/twentytwentyfive/assets):
 *
 *   style.min.css            global styles of theme.json, the self-hosted font faces (font-display:
 *                            swap), core's common block styles, the styles of the blocks every page
 *                            of the theme prints (header, footer, query loop, post meta), the layout
 *                            container rules of the theme's templates, Twenty Twenty-Five's own
 *                            style.min.css and wpgo.css (WordPress Go additions).
 *   css/editor-global.min.css  the global styles for Gotenberg (font URLs absolute).
 *   css/styles/<slug>.min.css  per color variation (styles/colors/*.json): only the rules and custom
 *                            properties that differ from the default palette, and the section styles
 *                            (is-style-section-N) the variation changes, such as their button colors;
 *                            printed inline after style.min.css when the Customizer's Color scheme
 *                            picks it.
 *
 * WordPress itself computes the CSS: the script boots a WordPress tree (read only) with a database
 * stub and the theme active, so the output is exactly what WordPress prints for the theme.
 *
 *   php tools/twentytwentyfive/build.php <wordpress dir> <twentytwentyfive dir> <web/themes/twentytwentyfive/assets>
 *   php tools/twentytwentyfive/build.php ... --variation <slug>   (internal: one variation per process)
 */

error_reporting( E_ALL & ~E_DEPRECATED & ~E_NOTICE & ~E_WARNING );
if ( $argc < 4 ) {
	fwrite( STDERR, "usage: php build.php <wordpress dir> <twentytwentyfive dir> <assets dir>\n" );
	exit( 1 );
}
list( , $wp_dir, $theme_dir, $out_dir ) = $argv;
$variation = ( $argc >= 6 && '--variation' === $argv[4] ) ? $argv[5] : '';
$wp_dir    = rtrim( realpath( $wp_dir ), '/' ) . '/';
$theme_dir = rtrim( realpath( $theme_dir ), '/' );
$out_dir   = rtrim( realpath( $out_dir ), '/' );

// A wp-content directory in the temp dir: the theme (symlinked), a database stub and the options.
$content = sys_get_temp_dir() . '/wpgo-tt5-build';
@mkdir( $content . '/themes', 0777, true );
@mkdir( $content . '/mu-plugins', 0777, true );
@unlink( $content . '/themes/twentytwentyfive' );
symlink( $theme_dir, $content . '/themes/twentytwentyfive' );
copy( __DIR__ . '/stub-db.php', $content . '/db.php' );
file_put_contents( $content . '/mu-plugins/options.php', '<?php
foreach ( array( "template" => "twentytwentyfive", "stylesheet" => "twentytwentyfive", "blogname" => "Site", "permalink_structure" => "/%postname%/" ) as $k => $v ) {
	add_filter( "pre_option_" . $k, function () use ( $v ) { return $v; } );
}
add_filter( "wp_theme_json_data_user", function ( $data ) {
	if ( empty( $GLOBALS["wpgo_variation"] ) ) {
		return $data;
	}
	return $data->update_with( array_merge( array( "version" => 3 ), $GLOBALS["wpgo_variation"] ) );
} );
' );

$_SERVER['HTTP_HOST']      = 'example.test';
$_SERVER['REQUEST_URI']    = '/';
$_SERVER['SERVER_NAME']    = 'example.test';
$_SERVER['REQUEST_METHOD'] = 'GET';
define( 'ABSPATH', $wp_dir );
define( 'WP_CONTENT_DIR', $content );
define( 'WP_CONTENT_URL', 'http://example.test/wp-content' );
define( 'WP_SITEURL', 'http://example.test' );
define( 'WP_HOME', 'http://example.test' );
define( 'DB_NAME', 'x' );
define( 'DB_USER', 'x' );
define( 'DB_PASSWORD', 'x' );
define( 'DB_HOST', 'x' );
define( 'DB_CHARSET', 'utf8mb4' );
define( 'DB_COLLATE', '' );
define( 'WP_DEBUG', false );
define( 'SCRIPT_DEBUG', false );
define( 'WP_USE_THEMES', false );
define( 'DISABLE_WP_CRON', true );
$table_prefix = 'wp_';

if ( $variation ) {
	// The user origin carries the variation, like picking it in the Site Editor's Styles.
	$data = json_decode( file_get_contents( $theme_dir . '/styles/colors/' . $variation . '.json' ), true );
	unset( $data['title'], $data['slug'] );
	$GLOBALS['wpgo_variation'] = $data;
}
require ABSPATH . 'wp-settings.php';

/** minify collapses whitespace without touching the meaning of selectors (descendant spaces stay). */
function minify( $css ) {
	$css = preg_replace( '#/\*.*?\*/#s', '', $css );
	$css = preg_replace( '/\s+/', ' ', $css );
	$css = preg_replace( '/\s*([{};])\s*/', '$1', $css );
	$css = preg_replace( '/([{;])\s*([a-z-]+)\s*:\s*/i', '$1$2:', $css );
	$css = str_replace( ';}', '}', $css );
	return trim( $css );
}

/** rules splits a stylesheet into top-level rules: [selector or at-rule prelude, body]. */
function rules( $css ) {
	$out   = array();
	$depth = 0;
	$start = 0;
	$open  = 0;
	$len   = strlen( $css );
	for ( $i = 0; $i < $len; $i++ ) {
		if ( '{' === $css[ $i ] ) {
			if ( 0 === $depth ) {
				$open = $i;
			}
			$depth++;
		} elseif ( '}' === $css[ $i ] ) {
			$depth--;
			if ( 0 === $depth ) {
				$out[] = array( trim( substr( $css, $start, $open - $start ) ), substr( $css, $open + 1, $i - $open - 1 ) );
				$start = $i + 1;
			}
		}
	}
	return $out;
}

/** decls splits a declaration block. */
function decls( $body ) {
	$out = array();
	foreach ( preg_split( '/;(?![^(]*\))/', $body ) as $d ) {
		$d = trim( $d );
		if ( '' === $d || false === strpos( $d, ':' ) ) {
			continue;
		}
		list( $k, $v ) = array_map( 'trim', explode( ':', $d, 2 ) );
		$out[ $k ]     = $v;
	}
	return $out;
}

/**
 * section_styles is the CSS of the block style variations (styles/blocks/*.json, styles/sections/*.json)
 * as the merged theme.json resolves them, with a color variation's own section overrides when one is
 * active. WordPress prints them per block instance (is-style-section-1--3); the port prints them once,
 * for the plain class name.
 */
function section_styles( $theme_dir ) {
	foreach ( array_merge( glob( $theme_dir . '/styles/blocks/*.json' ), glob( $theme_dir . '/styles/sections/*.json' ) ) as $file ) {
		$json = json_decode( file_get_contents( $file ), true );
		foreach ( (array) $json['blockTypes'] as $type ) {
			wp_render_block_style_variation_support_styles( array( 'blockName' => $type, 'attrs' => array( 'className' => 'is-style-' . $json['slug'] ), 'innerBlocks' => array(), 'innerHTML' => '', 'innerContent' => array() ) );
		}
	}
	$css  = '';
	$seen = array();
	foreach ( (array) wp_styles()->get_data( 'block-style-variation-styles', 'after' ) as $chunk ) {
		foreach ( rules( minify( preg_replace( '/(is-style-[a-z0-9-]+?)--\d+/', '$1', $chunk ) ) ) as $r ) {
			$rule = $r[0] . '{' . $r[1] . '}';
			if ( ! isset( $seen[ $rule ] ) ) {
				$seen[ $rule ] = true;
				$css          .= $rule;
			}
		}
	}
	return $css;
}

// The marker between a variation's global styles and its section styles in the --variation output.
const SECTIONS_MARKER = "\n/*@sections*/\n";

if ( $variation ) {
	echo wp_get_global_stylesheet(), SECTIONS_MARKER, section_styles( $theme_dir );
	exit;
}

$block = function ( $name ) use ( $wp_dir ) {
	return file_get_contents( $wp_dir . 'wp-includes/blocks/' . $name . '/style.min.css' );
};

// Font faces: the theme.json fontFamilies, self-hosted, with font-display: swap.
ob_start();
wp_print_font_faces();
$faces = ob_get_clean();
$faces = preg_replace( '#</?style[^>]*>#', '', $faces );
$faces = str_replace( 'font-display:fallback', 'font-display:swap', $faces );
$faces_rel = str_replace( "url('http://example.test/wp-content/themes/twentytwentyfive/assets/fonts/", "url('fonts/", $faces );
$faces_abs = str_replace( "url('http://example.test/wp-content/themes/twentytwentyfive/assets/fonts/", "url('/wp-content/themes/twentytwentyfive/fonts/", $faces );

$global = wp_get_global_stylesheet();

// The blocks of the theme's header, footer and query loops: on (nearly) every page. Keep this list in
// sync with tt5BundledBlocks; the other blocks' styles are inlined by the pages that use them.
$always = array( 'site-title', 'site-tagline', 'navigation', 'navigation-link', 'page-list', 'group', 'columns', 'spacer', 'paragraph', 'heading',
	'post-title', 'post-date', 'post-content', 'post-template', 'post-featured-image', 'query-pagination' );
$blocks = '';
foreach ( $always as $name ) {
	$blocks .= $block( $name );
}

// The layout container rules of the templates (wp-container-core-*-is-layout-*), as WordPress prints
// them for the theme's header, footer, single, 404 and query loops.
$containers = file_get_contents( __DIR__ . '/containers.css' );

// Block style variations (styles/blocks/*.json, styles/sections/*.json).
$variation_css = section_styles( $theme_dir );

$tt5   = file_get_contents( $theme_dir . '/style.min.css' );
$tt5   = preg_replace( '#/\*.*?\*/#s', '', $tt5 );
$check = 'ul.is-style-checkmark-list{list-style-type:"\2713"}ul.is-style-checkmark-list li{padding-inline-start:1ch}';

$header = "/*! Twenty Twenty-Five 1.5 for Pursae. (C) WordPress.org and contributors, GPLv2 or later. Generated by tools/twentytwentyfive/build.php from theme.json and WordPress " . $GLOBALS['wp_version'] . "; do not edit. */\n";
$bundle = $header . minify( $faces_rel ) . minify( $global ) . "\n"
	. file_get_contents( $wp_dir . 'wp-includes/css/dist/block-library/common.min.css' ) . "\n"
	. file_get_contents( $wp_dir . 'wp-includes/css/wp-block-template-skip-link.min.css' ) . "\n"
	. 'img:is([sizes=auto i],[sizes^="auto," i]){contain-intrinsic-size:3000px 1500px}' . "\n"
	. $blocks . "\n" . minify( $containers ) . "\n" . $variation_css . "\n" . minify( $tt5 ) . $check . "\n"
	. minify( file_get_contents( __DIR__ . '/wpgo.css' ) ) . "\n";
$bundle = preg_replace( '#/\*\! This file is auto-generated \*/#', '', $bundle );
file_put_contents( $out_dir . '/style.min.css', $bundle );
@mkdir( $out_dir . '/css/styles', 0777, true );
file_put_contents( $out_dir . '/css/editor-global.min.css', $header . minify( $faces_abs ) . minify( $global ) . "\n" );
echo 'style.min.css ', strlen( $bundle ), " bytes\n";

// Color variations: the difference from the default global styles.
// A selector can have several rules (:root has a few): compare with all of its declarations.
$base   = array();
$bodies = array();
foreach ( rules( minify( $global ) ) as $r ) {
	$bodies[ $r[0] . '{' . $r[1] . '}' ] = true;
	if ( false === strpos( $r[1], '{' ) ) {
		$base[ $r[0] ] = array_merge( isset( $base[ $r[0] ] ) ? $base[ $r[0] ] : array(), decls( $r[1] ) );
	}
}
$base_sections = array();
foreach ( rules( $variation_css ) as $r ) {
	$base_sections[ $r[0] . '{' . $r[1] . '}' ] = true;
}
$index = array();
foreach ( glob( $theme_dir . '/styles/colors/*.json' ) as $file ) {
	$slug = basename( $file, '.json' );
	$json = json_decode( file_get_contents( $file ), true );
	$out  = shell_exec( escapeshellarg( PHP_BINARY ) . ' ' . escapeshellarg( __FILE__ ) . ' ' . escapeshellarg( $wp_dir ) . ' ' . escapeshellarg( $theme_dir ) . ' ' . escapeshellarg( $out_dir ) . ' --variation ' . escapeshellarg( $slug ) );
	list( $css, $sections ) = array_pad( explode( SECTIONS_MARKER, $out, 2 ), 2, '' );
	$diff = '';
	foreach ( rules( minify( $css ) ) as $r ) {
		list( $sel, $body ) = $r;
		if ( isset( $bodies[ $sel . '{' . $body . '}' ] ) ) {
			continue;
		}
		if ( isset( $base[ $sel ] ) && false === strpos( $body, '{' ) ) {
			$old  = $base[ $sel ];
			$keep = array();
			foreach ( decls( $body ) as $k => $v ) {
				if ( ! isset( $old[ $k ] ) || $old[ $k ] !== $v ) {
					$keep[] = $k . ':' . $v;
				}
			}
			if ( $keep ) {
				$diff .= $sel . '{' . implode( ';', $keep ) . '}';
			}
			continue;
		}
		$diff .= $sel . '{' . $body . '}';
	}
	// The section styles the variation changes (styles.variations.section-N in its JSON, e.g. the
	// buttons of a section): whole rules, printed after the default ones so they win. WordPress prints
	// every section style after the global styles; here the variation's global rules come after
	// style.min.css, so a default section rule with the same selector inside the section (its
	// buttons, say) is printed again after them, or the variation's global rule would win.
	$global_sel = array();
	foreach ( rules( $diff ) as $r ) {
		$global_sel[ $r[0] ] = true;
	}
	foreach ( rules( $sections ) as $r ) {
		$rule = $r[0] . '{' . $r[1] . '}';
		$bare = preg_replace( '/\.is-style-[a-z0-9-]+ ?/', '', $r[0] );
		if ( ! isset( $base_sections[ $rule ] ) || isset( $global_sel[ $bare ] ) ) {
			$diff .= $rule;
		}
	}
	$name = preg_replace( '/^\d+-/', '', $slug );
	file_put_contents( $out_dir . '/css/styles/' . $name . '.min.css', $diff . "\n" );
	$index[] = $name . ' ' . $json['title'] . ' ' . strlen( $diff );
}
echo implode( "\n", $index ), "\n";
