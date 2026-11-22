<?php
/**
 * Unit tests of the WP to Pursae plugin that need no WordPress: the few WordPress functions the
 * tested code calls are stubbed below. Run: php integrations/wordpress/test/unit/run.php
 * (CI: the wordpress-plugin job). The end-to-end test with a real WordPress is ../run.sh.
 *
 * @package WP_To_Pursae
 */

// phpcs:ignoreFile

define( 'ABSPATH', __DIR__ . '/' );
define( 'DAY_IN_SECONDS', 86400 );
define( 'HOUR_IN_SECONDS', 3600 );
define( 'MB_IN_BYTES', 1048576 );

$GLOBALS['t'] = array(
	'multisite' => false,
	'user'      => 0,
	'caps'      => array(), // user id => true when the user has manage_options
	'super'     => array(), // user id => true for super admins
	'options'   => array(),
	'cron'      => array(), // hook => array( 'time' => int, 'recurrence' => string|false )
);

function is_multisite() { return $GLOBALS['t']['multisite']; }
function get_current_user_id() { return $GLOBALS['t']['user']; }
function is_super_admin( $id ) { return ! empty( $GLOBALS['t']['super'][ $id ] ); }
function user_can( $id, $cap ) { return 'manage_options' === $cap && ( ! empty( $GLOBALS['t']['caps'][ $id ] ) || is_super_admin( $id ) ); }
function get_option( $name, $default = false ) { return array_key_exists( $name, $GLOBALS['t']['options'] ) ? $GLOBALS['t']['options'][ $name ] : $default; }
function update_option( $name, $value ) { $GLOBALS['t']['options'][ $name ] = $value; return true; }
function delete_option( $name ) { unset( $GLOBALS['t']['options'][ $name ] ); return true; }
function wp_next_scheduled( $hook ) { return isset( $GLOBALS['t']['cron'][ $hook ] ) ? $GLOBALS['t']['cron'][ $hook ]['time'] : false; }
function wp_schedule_event( $time, $recurrence, $hook ) { $GLOBALS['t']['cron'][ $hook ] = array( 'time' => $time, 'recurrence' => $recurrence ); return true; }
function wp_schedule_single_event( $time, $hook ) { $GLOBALS['t']['cron'][ $hook ] = array( 'time' => $time, 'recurrence' => false ); return true; }
function wp_clear_scheduled_hook( $hook ) { unset( $GLOBALS['t']['cron'][ $hook ] ); return 1; }
function wp_delete_file( $f ) { @unlink( $f ); }

$plugin = dirname( __DIR__, 2 ) . '/wp-to-pursae/includes/';
require $plugin . 'class-wp2p-crypto.php';
require $plugin . 'class-wp2p-writer.php';
require $plugin . 'class-wp2p-content-filter.php';
require $plugin . 'class-wp2p-exporter.php';

$failed = 0;
$passed = 0;
function check( $what, $ok ) {
	global $failed, $passed;
	if ( $ok ) {
		++$passed;
	} else {
		++$failed;
		fwrite( STDERR, "FAIL $what\n" );
	}
}

/**
 * Opens a package: the header, then every chunk. Returns the plaintext and the chunks' nonces with
 * their ciphertexts, or throws.
 */
function open_package( $data, $key ) {
	if ( 'PURSAEWP' !== substr( $data, 0, 8 ) || 2 !== ord( $data[8] ) ) {
		throw new RuntimeException( 'header' );
	}
	$p      = 25;
	$i      = 0;
	$plain  = '';
	$nonces = array();
	$last   = false;
	while ( $p < strlen( $data ) ) {
		if ( $last ) {
			throw new RuntimeException( 'data after the last chunk' );
		}
		$len   = unpack( 'N', substr( $data, $p, 4 ) )[1];
		$nonce = substr( $data, $p + 4, 24 );
		$box   = substr( $data, $p + 28, $len - 24 );
		$out   = sodium_crypto_secretbox_open( $box, $nonce, $key );
		if ( false === $out ) {
			throw new RuntimeException( "chunk $i does not open" );
		}
		$idx = unpack( 'Nhigh/Nlow', substr( $out, 0, 8 ) );
		$last = (bool) ( $idx['high'] & 0x80000000 );
		if ( ( ( $idx['high'] & 0x7fffffff ) << 32 | $idx['low'] ) !== $i ) {
			throw new RuntimeException( "chunk $i has index {$idx['low']}" );
		}
		$nonces[] = array( $nonce, $box );
		$plain   .= substr( $out, 8 );
		$p       += 4 + $len;
		++$i;
	}
	if ( ! $last ) {
		throw new RuntimeException( 'no last chunk' );
	}
	return array( $plain, $nonces );
}

/** The entries of a ustar archive: name => data. */
function untar( $tar ) {
	$out = array();
	for ( $p = 0; $p + 512 <= strlen( $tar ); ) {
		$h = substr( $tar, $p, 512 );
		if ( trim( $h, "\0" ) === '' ) {
			break;
		}
		$name         = rtrim( substr( $h, 0, 100 ), "\0" );
		$size         = octdec( rtrim( substr( $h, 124, 11 ), "\0 " ) );
		$out[ $name ] = substr( $tar, $p + 512, $size );
		$p           += 512 + (int) ( ceil( $size / 512 ) * 512 );
	}
	return $out;
}

// --- Who may export: on multisite only a super admin (a sub-site admin has manage_options). ---
$GLOBALS['t']['caps']  = array( 2 => true, 3 => true );
$GLOBALS['t']['super'] = array( 3 => true );
check( 'nobody signed in', ! WP2P_Exporter::allowed( 0 ) );
check( 'single site: an administrator', WP2P_Exporter::allowed( 2 ) );
check( 'single site: a user without manage_options', ! WP2P_Exporter::allowed( 4 ) );
$GLOBALS['t']['user'] = 2;
check( 'single site: the current administrator', WP2P_Exporter::allowed() );
check( 'single site: capability', 'manage_options' === WP2P_Exporter::capability() );
$GLOBALS['t']['multisite'] = true;
check( 'multisite: a sub-site administrator is refused', ! WP2P_Exporter::allowed() );
check( 'multisite: a sub-site administrator by id', ! WP2P_Exporter::allowed( 2 ) );
check( 'multisite: a super admin', WP2P_Exporter::allowed( 3 ) );
check( 'multisite: capability', 'manage_network_options' === WP2P_Exporter::capability() );
$GLOBALS['t']['multisite'] = false;

// --- A package resumed after a request died mid-batch opens, and no nonce is used twice. ---
$dir = sys_get_temp_dir() . '/wp2p-unit-' . bin2hex( random_bytes( 6 ) );
mkdir( $dir );
$file  = $dir . '/p.wp2p';
$key   = WP2P_Crypto::random_key();
$state = WP2P_Writer::create( $file );
$w     = new WP2P_Writer( $state, $key );
$one   = random_bytes( (int) ( 2.5 * WP2P_Writer::CHUNK ) );
$w->add( 'users.000001.jsonl', $one );
$state = $w->commit();
$w->close();
// A request that dies: full chunks of its batch reach the file, the state is never saved.
$w = new WP2P_Writer( $state, $key );
$w->add( 'users.000002.jsonl', random_bytes( 3 * WP2P_Writer::CHUNK ) );
$w->close();
$dead = file_get_contents( $file );
check( 'the dead batch wrote chunks', strlen( $dead ) > $state['committed'] + 2 * WP2P_Writer::CHUNK );
// The resume cuts back to the commit and writes other bytes under the same chunk indexes.
$w   = new WP2P_Writer( $state, $key );
$two = random_bytes( 3 * WP2P_Writer::CHUNK + 777 );
$w->add( 'users.000002.jsonl', $two );
$w->commit();
$w->add( 'manifest.json', '{"format":1}' );
$state = $w->finish();
$final = file_get_contents( $file );
try {
	list( $tar, $chunks ) = open_package( $final, $key );
	$entries              = untar( $tar );
	check( 'resumed package: first batch', isset( $entries['users.000001.jsonl'] ) && $entries['users.000001.jsonl'] === $one );
	check( 'resumed package: the redone batch', isset( $entries['users.000002.jsonl'] ) && $entries['users.000002.jsonl'] === $two );
	check( 'resumed package: last entry', isset( $entries['manifest.json'] ) );
	check( 'resumed package: nothing else', 3 === count( $entries ) );
	// Every nonce seen in the dead file and in the final one seals one ciphertext only.
	$dead_chunks = array();
	$p = 25;
	while ( $p + 28 <= strlen( $dead ) ) {
		$len = unpack( 'N', substr( $dead, $p, 4 ) )[1];
		$dead_chunks[] = array( substr( $dead, $p + 4, 24 ), substr( $dead, $p + 28, $len - 24 ) );
		$p += 4 + $len;
	}
	$seen  = array();
	$reuse = 0;
	foreach ( array_merge( $dead_chunks, $chunks ) as $c ) {
		if ( isset( $seen[ $c[0] ] ) && $seen[ $c[0] ] !== $c[1] ) {
			++$reuse;
		}
		$seen[ $c[0] ] = $c[1];
	}
	check( 'no nonce seals two different chunks', 0 === $reuse );
	check( 'chunks were sealed again under the same index', count( $dead_chunks ) > 3 );
} catch ( RuntimeException $e ) {
	check( 'resumed package opens: ' . $e->getMessage(), false );
}
array_map( 'unlink', glob( $dir . '/*' ) );
rmdir( $dir );

// --- The content filter: one WXR document out of export_wp() batches. ---
$doc  = "<?xml version=\"1.0\"?>\n<rss>\n<channel>\n\t<wp:author>a</wp:author>\n";
$item = function ( $n ) {
	return "\t<item>\n\t\t<title>Post $n</title>\n\t\t<content:encoded><![CDATA[" . str_repeat( 'x', 3000 ) . "</channel>]]></content:encoded>\n\t</item>\n";
};
$foot = "\n</channel>\n</rss>\n\t";
$run  = function ( $out, $head, $piece ) {
	$got = '';
	$f   = new WP2P_Content_Filter(
		function ( $d ) use ( &$got ) {
			$got .= $d;
		},
		$head
	);
	foreach ( str_split( $out, $piece ) as $c ) {
		$f->chunk( $c );
	}
	$n = $f->finish();
	return array( $got, $n );
};
list( $got, $n ) = $run( $doc . $item( 1 ) . $item( 2 ) . $foot, true, 7 );
check( 'first batch keeps the head, drops the closing tags', $doc . $item( 1 ) . $item( 2 ) . "\n" === $got && strlen( $got ) === $n );
list( $got ) = $run( $doc . $item( 3 ) . $item( 4 ) . $foot, false, 5 );
check( 'later batches keep only their items', $item( 3 ) . $item( 4 ) . "\n" === $got );
list( $got ) = $run( $doc . $foot, false, 4096 );
check( 'a batch without items adds nothing', '' === $got );
list( $got ) = $run( $doc . $foot, true, 3 );
check( 'the head of an empty site', $doc . "\n" === $got );
$whole = $run( $doc . $item( 1 ) . $item( 2 ) . $foot, true, 100 )[0] . $run( $doc . $item( 3 ) . $foot, false, 100 )[0] . "</channel>\n</rss>\n";
check( 'the parts make one document', $doc . $item( 1 ) . $item( 2 ) . "\n" . $item( 3 ) . "\n</channel>\n</rss>\n" === $whole );

// --- Cleanup: an hourly event while there is a job, whenever it was started. ---
$GLOBALS['t']['cron']                   = array( 'wp2p_cleanup' => array( 'time' => time() + 10, 'recurrence' => false ) );
$GLOBALS['t']['options']['wp2p_job'] = array( 'created' => time() - 3600 );
unset( $GLOBALS['t']['cron']['wp2p_cleanup'] ); // WordPress unschedules a single event before running it.
WP2P_Exporter::cleanup_expired();
check( 'a young job is kept', null !== WP2P_Exporter::job() );
check( 'a young job keeps an hourly cleanup', isset( $GLOBALS['t']['cron']['wp2p_cleanup'] ) && 'hourly' === $GLOBALS['t']['cron']['wp2p_cleanup']['recurrence'] );
$GLOBALS['t']['options']['wp2p_job'] = array( 'created' => time() - DAY_IN_SECONDS - 1, 'token' => 'pmt_x', 'app_password' => 'x' );
WP2P_Exporter::cleanup_expired();
check( 'an expired job is removed with its secrets', null === WP2P_Exporter::job() );
check( 'the cleanup ends once nothing is left', ! isset( $GLOBALS['t']['cron']['wp2p_cleanup'] ) );

echo "$passed passed, $failed failed\n";
exit( $failed ? 1 : 0 );
