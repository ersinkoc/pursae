<?php
/**
 * Builds the migration package step by step.
 *
 * A job (the option wp2p_job) goes through the steps below; each call of run() works until its time
 * budget is used up and records where it stopped, so the admin screen can build a package of any size
 * in short AJAX requests, and WP-CLI in one go. Every batch is written into the encrypted package as
 * it is read; the working directory has a random name, refuses web access, and is removed once the
 * package is sent or deleted (or after a day).
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * Package builder.
 */
class WP2P_Exporter {

	const OPTION         = 'wp2p_job';
	const TTL            = DAY_IN_SECONDS;
	const CLEANUP        = 'wp2p_cleanup';
	const CONTENT_FORMAT = 1; // manifest.json's "format": the archive's entries (the envelope is WP2P_Crypto::FORMAT).

	/**
	 * Whether export_wp() already ran in this PHP process: it declares its helper functions (wxr_cdata,
	 * ...) when it runs, so a second call in the same process would end in a fatal error.
	 *
	 * @var bool
	 */
	private static $exported = false;

	/**
	 * Set by a step that cannot go on in this request (the content step after its export_wp() batch).
	 *
	 * @var bool
	 */
	private static $yield = false;

	/**
	 * The steps, in order.
	 *
	 * @var string[]
	 */
	public static $steps = array( 'users', 'options', 'content', 'media', 'tax_rates', 'coupons', 'orders', 'customers', 'close' );

	/**
	 * Core options that move (Pursae maps them to its settings).
	 *
	 * @var string[]
	 */
	public static $core_options = array(
		'blogname',
		'blogdescription',
		'permalink_structure',
		'category_base',
		'tag_base',
		'timezone_string',
		'gmt_offset',
		'date_format',
		'time_format',
		'start_of_week',
		'posts_per_page',
		'posts_per_rss',
		'rss_use_excerpt',
		'show_on_front',
		'page_on_front',
		'page_for_posts',
		'wp_page_for_privacy_policy',
		'default_category',
		'default_comment_status',
		'default_ping_status',
		'comment_registration',
		'close_comments_for_old_posts',
		'close_comments_days_old',
		'thread_comments',
		'thread_comments_depth',
		'page_comments',
		'comments_per_page',
		'default_comments_page',
		'comment_order',
		'comment_moderation',
		'comment_previously_approved',
		'require_name_email',
		'show_avatars',
		'avatar_rating',
		'avatar_default',
		'blog_public',
		'users_can_register',
		'default_role',
		'uploads_use_yearmonth_folders',
		'thumbnail_size_w',
		'thumbnail_size_h',
		'thumbnail_crop',
		'medium_size_w',
		'medium_size_h',
		'large_size_w',
		'large_size_h',
		'site_icon',
		'WPLANG',
	);

	/**
	 * User meta that move besides the WooCommerce addresses.
	 *
	 * @var string[]
	 */
	public static $user_meta = array( 'first_name', 'last_name', 'nickname', 'description', 'locale', 'paying_customer' );

	/**
	 * Whether a user may build and send packages. A package holds the password hashes of every user
	 * of the site, so on multisite, where a site's administrators have manage_options but network
	 * users (super admins among them) are members of their site, only a super admin may.
	 *
	 * @param int|null $user_id User; the current user when null.
	 * @return bool
	 */
	public static function allowed( $user_id = null ) {
		$user_id = null === $user_id ? get_current_user_id() : (int) $user_id;
		if ( $user_id <= 0 ) {
			return false;
		}
		if ( is_multisite() ) {
			return is_super_admin( $user_id );
		}
		return user_can( $user_id, 'manage_options' );
	}

	/**
	 * The capability of the Tools entry (allowed() is checked on every request as well).
	 *
	 * @return string
	 */
	public static function capability() {
		return is_multisite() ? 'manage_network_options' : 'manage_options';
	}

	/**
	 * The current job, or null.
	 *
	 * @return array|null
	 */
	public static function job() {
		$job = get_option( self::OPTION );
		return is_array( $job ) ? $job : null;
	}

	/**
	 * Saves the job.
	 *
	 * @param array $job Job.
	 */
	public static function save( array $job ) {
		update_option( self::OPTION, $job, false );
	}

	/**
	 * The largest file a tar header can describe: 11 octal digits, 8 GiB - 1.
	 */
	const MAX_ENTRY = 8589934591;

	/**
	 * Media files over this size are packed in a request of their own.
	 */
	const BIG_FILE = 67108864;

	/**
	 * The largest media file that is packed: 4 GB by default (what Pursae takes from a package). Files are
	 * streamed, never read into memory. The filter's result is kept between 0 and 8 GiB - 1 (what the
	 * archive format can hold) and within PHP's integer range (32-bit builds).
	 *
	 * @return int
	 */
	public static function max_media_bytes() {
		$default = (float) min( 4096 * MB_IN_BYTES, PHP_INT_MAX );
		/**
		 * Filters the size above which a media file is left out of the package (it is listed in the
		 * report so it can be copied by hand).
		 *
		 * @param int $bytes Size in bytes.
		 */
		$limit = apply_filters( 'wp2p_max_media_bytes', (int) $default );
		$limit = is_numeric( $limit ) ? (float) $limit : $default;
		$cap   = (float) min( PHP_INT_MAX, self::MAX_ENTRY );
		return (int) max( 0.0, min( $limit, $cap ) );
	}

	/**
	 * Starts a new job (an unfinished one is discarded).
	 *
	 * @param string $mode  "download" or "push".
	 * @param string $key   Raw package key.
	 * @param array  $extra Push settings (url, token or user and application password).
	 * @return array The job.
	 * @throws RuntimeException When the working directory cannot be created.
	 */
	public static function start( $mode, $key, array $extra = array() ) {
		self::discard();
		$dir           = self::make_dir();
		$id            = strtolower( wp_generate_password( 24, false ) );
		$job           = array_merge(
			array(
				'id'      => $id,
				'mode'    => $mode,
				'dir'     => $dir,
				'file'    => $dir . '/' . $id . '.wp2p',
				'key'     => WP2P_Crypto::encode_key( $key ),
				'step'    => 0,
				'cursor'  => 0,
				'part'    => 0,
				'counts'  => array(),
				'skipped' => array(),
				'status'  => 'building',
				'created' => time(),
				'message' => '',
			),
			$extra
		);
		$job['writer'] = WP2P_Writer::create( $job['file'] );
		self::save( $job );
		self::schedule_cleanup();
		return $job;
	}

	/**
	 * Makes sure the hourly cleanup event exists while there is a job: it removes the job once it is
	 * older than TTL, whenever that job was started.
	 */
	public static function schedule_cleanup() {
		if ( ! wp_next_scheduled( self::CLEANUP ) ) {
			wp_schedule_event( time() + HOUR_IN_SECONDS, 'hourly', self::CLEANUP );
		}
	}

	/**
	 * Creates the working directory: WP2P_WORK_DIR when it is defined (best outside the web root),
	 * else a directory with a random name in uploads that refuses web access.
	 *
	 * @return string
	 * @throws RuntimeException When it cannot be created.
	 */
	private static function make_dir() {
		$base = defined( 'WP2P_WORK_DIR' ) ? untrailingslashit( WP2P_WORK_DIR ) : untrailingslashit( wp_upload_dir()['basedir'] );
		$dir  = $base . '/wp-to-pursae-' . strtolower( wp_generate_password( 32, false ) );
		if ( ! wp_mkdir_p( $dir ) ) {
			throw new RuntimeException( esc_html__( 'The working directory cannot be created. Check that the uploads directory is writable.', 'wp-to-pursae' ) );
		}
		// phpcs:disable WordPress.WP.AlternativeFunctions.file_system_operations_file_put_contents -- tiny guard files.
		file_put_contents( $dir . '/.htaccess', "Require all denied\nDeny from all\n" );
		file_put_contents( $dir . '/web.config', '<?xml version="1.0"?><configuration><system.webServer><authorization><deny users="*" /></authorization></system.webServer></configuration>' );
		file_put_contents( $dir . '/index.php', "<?php\n// Silence is golden.\n" );
		// phpcs:enable
		return $dir;
	}

	/**
	 * Removes the job's files and the job.
	 */
	public static function discard() {
		$job = self::job();
		if ( $job && ! empty( $job['dir'] ) && false !== strpos( basename( $job['dir'] ), 'wp-to-pursae-' ) ) {
			self::rmdir( $job['dir'] );
		}
		delete_option( self::OPTION );
	}

	/**
	 * Cron (hourly while there is a job): removes a job older than TTL with its package, key, token
	 * and Application Password. The event ends itself once nothing is left.
	 */
	public static function cleanup_expired() {
		$job = self::job();
		if ( $job && time() - (int) ( isset( $job['created'] ) ? $job['created'] : 0 ) > self::TTL ) {
			self::discard();
			$job = null;
		}
		if ( $job ) {
			self::schedule_cleanup(); // An older version's single event may have been this one.
		} else {
			wp_clear_scheduled_hook( self::CLEANUP );
		}
	}

	/**
	 * Removes a directory and its files (one level deep, as the job creates it).
	 *
	 * @param string $dir Directory.
	 */
	private static function rmdir( $dir ) {
		foreach ( (array) scandir( $dir ) as $f ) {
			if ( is_file( $dir . '/' . $f ) ) {
				wp_delete_file( $dir . '/' . $f );
			}
		}
		@rmdir( $dir ); // phpcs:ignore WordPress.PHP.NoSilencedErrors.Discouraged, WordPress.WP.AlternativeFunctions.file_system_operations_rmdir
	}

	/**
	 * Works on the job for at most $budget seconds. Only one request works on a job at a time: the
	 * package file is cut back to the last saved point when a run opens it, so a second run (Resume
	 * clicked while a timed-out request still writes) would destroy what the first one writes. The lock
	 * is an flock on a file in the job's directory, which the system releases when the request ends,
	 * however it ends. While another request holds it, run() returns the job as it is.
	 *
	 * @param float $budget Seconds.
	 * @return array The job.
	 */
	public static function run( $budget ) {
		$job = self::job();
		if ( ! $job || 'building' !== $job['status'] ) {
			return $job;
		}
		$lock = self::lock( $job );
		if ( false === $lock ) {
			return $job;
		}
		try {
			return self::run_locked( $budget );
		} finally {
			if ( is_resource( $lock ) ) {
				flock( $lock, LOCK_UN );
				fclose( $lock ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
			}
		}
	}

	/**
	 * Takes the job's lock.
	 *
	 * @param array $job Job.
	 * @return resource|true|false The lock; true when the file system cannot lock (the run goes on
	 *                              unlocked); false when another request holds it.
	 */
	private static function lock( array $job ) {
		if ( empty( $job['dir'] ) || ! is_dir( $job['dir'] ) ) {
			return true;
		}
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fopen, WordPress.PHP.NoSilencedErrors.Discouraged
		$fh = @fopen( $job['dir'] . '/.lock', 'c' );
		if ( ! $fh ) {
			return true;
		}
		if ( ! flock( $fh, LOCK_EX | LOCK_NB ) ) {
			fclose( $fh ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
			return false;
		}
		return $fh;
	}

	/**
	 * run() with the lock held.
	 *
	 * @param float $budget Seconds.
	 * @return array The job.
	 */
	private static function run_locked( $budget ) {
		// The request that held the lock before may have saved progress since run() read the job.
		wp_cache_delete( self::OPTION, 'options' );
		$job = self::job();
		if ( ! $job || 'building' !== $job['status'] ) {
			return $job;
		}
		$until       = microtime( true ) + $budget;
		self::$yield = false;
		$key         = WP2P_Crypto::decode_key( $job['key'] );
		$writer      = new WP2P_Writer( $job['writer'], $key );
		$steps       = count( self::$steps );
		try {
			while ( $job['step'] < $steps ) {
				$step = self::$steps[ $job['step'] ];
				if ( 'close' === $step ) {
					if ( empty( $job['writer']['closed'] ) ) {
						self::step_close( $job, $writer );
						$job['writer'] = $writer->finish();
						$job['size']   = filesize( $job['file'] );
						self::save( $job );
					}
					$writer->close();
					// The checksum of a package of many gigabytes is made over several requests.
					if ( self::hash_step( $job, $until ) ) {
						$job['status'] = 'built';
						++$job['step'];
					}
					self::save( $job );
					return $job;
				}
				$done          = self::step( $step, $job, $writer, $until );
				$job['writer'] = $writer->commit();
				if ( $done ) {
					++$job['step'];
					$job['cursor'] = 0;
					$job['part']   = 0;
				}
				self::save( $job );
				if ( self::$yield || microtime( true ) >= $until ) {
					break;
				}
			}
		} catch ( Throwable $e ) {
			$writer->close();
			$job['status']  = 'error';
			$job['message'] = $e->getMessage();
			self::save( $job );
			return $job;
		}
		$writer->close();
		return $job;
	}

	/**
	 * Computes the package's SHA-256 in pieces. From PHP 8.1 the hash state is serializable, so each
	 * request hashes for as long as its budget lasts (at least one piece) and saves the state in the job;
	 * a request that is killed repeats only its own piece. Before 8.1 the state cannot be kept, and the
	 * whole file is hashed in one go (hash_file streams it; about 0.5 GB/s, so a package would have to
	 * be tens of gigabytes to meet a request time limit).
	 *
	 * @param array $job   Job.
	 * @param float $until Deadline.
	 * @return bool Whether the checksum is done (in $job['sha256']).
	 */
	private static function hash_step( array &$job, $until ) {
		if ( PHP_VERSION_ID < 80100 ) {
			$job['sha256'] = hash_file( 'sha256', $job['file'] );
			return true;
		}
		$size = (int) $job['size'];
		$pos  = isset( $job['hash_pos'] ) ? (int) $job['hash_pos'] : 0;
		$ctx  = false;
		if ( $pos > 0 && ! empty( $job['hash_state'] ) ) {
			$ctx = @unserialize( base64_decode( $job['hash_state'] ), array( 'allowed_classes' => array( 'HashContext' ) ) ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions.serialize_unserialize, WordPress.PHP.NoSilencedErrors.Discouraged
		}
		if ( ! $ctx instanceof HashContext ) {
			$ctx = hash_init( 'sha256' );
			$pos = 0;
		}
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fopen -- streamed binary input.
		$in = fopen( $job['file'], 'rb' );
		if ( ! $in ) {
			throw new RuntimeException( 'The package file cannot be read.' );
		}
		fseek( $in, $pos );
		do {
			// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fread -- streamed binary input.
			$data = fread( $in, (int) min( 4 * MB_IN_BYTES, max( 1, $size - $pos ) ) );
			if ( false === $data || '' === $data ) {
				break;
			}
			hash_update( $ctx, $data );
			$pos += strlen( $data );
		} while ( $pos < $size && microtime( true ) < $until );
		fclose( $in ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
		if ( $pos >= $size ) {
			$job['sha256'] = hash_final( $ctx );
			unset( $job['hash_state'], $job['hash_pos'] );
			return true;
		}
		$job['hash_pos']   = $pos;
		$job['hash_state'] = base64_encode( serialize( $ctx ) ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions.serialize_serialize
		return false;
	}

	/**
	 * Runs one step.
	 *
	 * @param string      $step   Step.
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param float       $until  Deadline.
	 * @return bool Whether the step is done.
	 */
	private static function step( $step, array &$job, WP2P_Writer $writer, $until ) {
		switch ( $step ) {
			case 'users':
				return self::step_users( $job, $writer, $until );
			case 'options':
				return self::step_options( $job, $writer );
			case 'content':
				return self::step_content( $job, $writer, $until );
			case 'media':
				return self::step_media( $job, $writer, $until );
			case 'tax_rates':
				return self::step_tax_rates( $job, $writer );
			case 'coupons':
				return self::step_coupons( $job, $writer, $until );
			case 'orders':
				return self::step_orders( $job, $writer, $until );
			case 'customers':
				return self::step_customers( $job, $writer, $until );
			default:
				return self::step_close( $job, $writer );
		}
	}

	/**
	 * The next part name of a logical file (users.000001.jsonl, ...).
	 *
	 * @param array  $job  Job.
	 * @param string $base Base name without extension.
	 * @param string $ext  Extension with the dot.
	 * @return string
	 */
	private static function part( array &$job, $base, $ext ) {
		++$job['part'];
		return sprintf( '%s.%06d%s', $base, $job['part'], $ext );
	}

	/**
	 * JSON lines of rows.
	 *
	 * @param array[] $rows Rows.
	 * @return string
	 */
	private static function jsonl( array $rows ) {
		$out = '';
		foreach ( $rows as $r ) {
			$out .= wp_json_encode( $r, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PARTIAL_OUTPUT_ON_ERROR ) . "\n";
		}
		return $out;
	}

	/**
	 * Adds a counter.
	 *
	 * @param array  $job  Job.
	 * @param string $what Counter.
	 * @param int    $n    Amount.
	 */
	private static function count( array &$job, $what, $n ) {
		$job['counts'][ $what ] = ( isset( $job['counts'][ $what ] ) ? $job['counts'][ $what ] : 0 ) + $n;
	}

	/**
	 * Users with their password hash as WordPress stores it, roles and selected meta.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param float       $until  Deadline.
	 * @return bool Whether the step is done.
	 */
	private static function step_users( array &$job, WP2P_Writer $writer, $until ) {
		global $wpdb;
		while ( microtime( true ) < $until ) {
			if ( is_multisite() ) {
				// phpcs:ignore WordPress.DB.DirectDatabaseQuery
				$ids = $wpdb->get_col( $wpdb->prepare( "SELECT u.ID FROM {$wpdb->users} u INNER JOIN {$wpdb->usermeta} m ON m.user_id = u.ID AND m.meta_key = %s WHERE u.ID > %d ORDER BY u.ID LIMIT 200", $wpdb->get_blog_prefix() . 'capabilities', $job['cursor'] ) );
			} else {
				// phpcs:ignore WordPress.DB.DirectDatabaseQuery
				$ids = $wpdb->get_col( $wpdb->prepare( "SELECT ID FROM {$wpdb->users} WHERE ID > %d ORDER BY ID LIMIT 200", $job['cursor'] ) );
			}
			if ( ! $ids ) {
				return true;
			}
			$rows = array();
			foreach ( $ids as $id ) {
				$u = get_userdata( (int) $id );
				if ( ! $u ) {
					continue;
				}
				$meta = array();
				foreach ( get_user_meta( $u->ID ) as $k => $v ) {
					if ( in_array( $k, self::$user_meta, true ) || 0 === strpos( $k, 'billing_' ) || 0 === strpos( $k, 'shipping_' ) ) {
						$meta[ $k ] = (string) maybe_unserialize( $v[0] );
					}
				}
				$rows[] = array(
					'id'           => (int) $u->ID,
					'login'        => $u->user_login,
					'email'        => $u->user_email,
					'pass'         => $u->user_pass,
					'nicename'     => $u->user_nicename,
					'display_name' => $u->display_name,
					'url'          => $u->user_url,
					'registered'   => $u->user_registered,
					'roles'        => array_values( (array) $u->roles ),
					'meta'         => $meta ? $meta : new stdClass(),
				);
			}
			$writer->add( self::part( $job, 'users', '.jsonl' ), self::jsonl( $rows ) );
			self::count( $job, 'users', count( $rows ) );
			$job['cursor'] = (int) end( $ids );
		}
		return false;
	}

	/**
	 * Site settings, WooCommerce store settings, menu locations.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @return bool
	 */
	private static function step_options( array &$job, WP2P_Writer $writer ) {
		global $wpdb;
		$opts = array();
		foreach ( self::$core_options as $name ) {
			$v = get_option( $name, null );
			if ( null !== $v ) {
				$opts[ $name ] = $v;
			}
		}
		if ( WP2P_Woo::active() ) {
			// WooCommerce's store settings; never payment gateways or anything holding a secret.
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery
			$names = $wpdb->get_col( "SELECT option_name FROM {$wpdb->options} WHERE option_name LIKE 'woocommerce\\_%'" );
			foreach ( $names as $name ) {
				if ( preg_match( '/(_settings$|secret|password|token|_key|api|webhook|_hash|transient|_version$|queue|_notice|_admin_|onboarding|tracker)/i', $name ) ) {
					continue;
				}
				$opts[ $name ] = get_option( $name );
			}
			if ( class_exists( 'WC_Tax' ) ) {
				$opts['woocommerce_tax_classes'] = implode( "\n", WC_Tax::get_tax_classes() );
			}
		}
		$locations = array();
		foreach ( (array) get_nav_menu_locations() as $loc => $term_id ) {
			$t = $term_id ? get_term( (int) $term_id, 'nav_menu' ) : null;
			if ( $t && ! is_wp_error( $t ) ) {
				$locations[ $loc ] = $t->slug;
			}
		}
		$data = array(
			'options'        => $opts,
			'menu_locations' => $locations ? $locations : new stdClass(),
			'theme'          => get_stylesheet(),
			'active_plugins' => array_values( (array) get_option( 'active_plugins', array() ) ),
		);
		$writer->add( 'options.json', wp_json_encode( $data, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PARTIAL_OUTPUT_ON_ERROR ) );
		return true;
	}

	/**
	 * How many posts (of any type) one content batch covers. WP-CLI has no request time limit and
	 * exports the rest in one go (0).
	 *
	 * @return int
	 */
	private static function content_batch() {
		if ( defined( 'WP_CLI' ) && WP_CLI ) {
			return 0;
		}
		/**
		 * Filters how many posts one content batch of the package covers in a web request.
		 *
		 * @param int $n Posts.
		 */
		return max( 1, (int) apply_filters( 'wp2p_content_batch', 200 ) );
	}

	/**
	 * All content as WordPress' own WXR export (Tools › Export › All content), in batches of post ids
	 * so no request has to export the whole site: each request runs export_wp() for the posts after
	 * the cursor (it can run only once per PHP process), and the batch is committed with the cursor.
	 * The parts form one WXR document: the first batch keeps export_wp()'s head (site, every author,
	 * categories, tags, terms, menus), later batches only their <item>s, and the last step closes the
	 * channel. WooCommerce orders, refunds and coupons are left out: they come in their own files.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param float       $until  Deadline.
	 * @return bool
	 * @throws RuntimeException When the content cannot be exported.
	 */
	private static function step_content( array &$job, WP2P_Writer $writer, $until ) {
		global $wpdb;
		$after = (int) $job['cursor'];
		$first = 0 === (int) $job['part'];
		$limit = self::content_batch();
		if ( $limit > 0 ) {
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery
			$upto = $wpdb->get_var( $wpdb->prepare( "SELECT MAX(ID) FROM (SELECT ID FROM {$wpdb->posts} WHERE ID > %d ORDER BY ID LIMIT %d) b", $after, $limit ) );
		} else {
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery
			$upto = $wpdb->get_var( $wpdb->prepare( "SELECT MAX(ID) FROM {$wpdb->posts} WHERE ID > %d", $after ) );
		}
		$upto = max( $after, (int) $upto );
		if ( $upto === $after && ! $first ) {
			$writer->add( self::part( $job, 'content', '.xml' ), "</channel>\n</rss>\n" );
			return true;
		}
		if ( self::$exported ) {
			if ( 0 === $limit ) {
				throw new RuntimeException( 'The content was exported once in this process already.' );
			}
			// The next batch needs a request of its own.
			self::$yield = true;
			return false;
		}
		if ( $limit > 0 && microtime( true ) >= $until ) {
			self::$yield = true;
			return false;
		}
		// A whole-site export in one go (WP-CLI from the start) is right even unlimited.
		self::export_range( $job, $writer, $after, $upto, $first, 0 === $limit && 0 === $after );
		$job['cursor'] = $upto;
		if ( 0 === $limit ) {
			// Everything there was: posts added meanwhile wait for the next package.
			$writer->add( self::part( $job, 'content', '.xml' ), "</channel>\n</rss>\n" );
			return true;
		}
		self::$yield = true;
		return false;
	}

	/**
	 * Runs export_wp() for the posts with ids in ($after, $upto] and writes its output as content
	 * parts: with the document head for the first batch, else only the <item>s; never the closing tags.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param int         $after  Last post id of the previous batch.
	 * @param int         $upto   Last post id of this batch.
	 * @param bool        $head   Whether to keep the head.
	 * @param bool        $whole  Whether the batch is the whole site, so the range may be missed.
	 * @throws RuntimeException When export_wp() could not be limited to the batch.
	 */
	private static function export_range( array &$job, WP2P_Writer $writer, $after, $upto, $head, $whole ) {
		global $wp_post_types, $wpdb;
		require_once ABSPATH . 'wp-admin/includes/export.php';
		$hidden = array();
		foreach ( array( 'shop_order', 'shop_order_refund', 'shop_order_placehold', 'shop_coupon', 'shop_subscription', 'scheduled-action' ) as $type ) {
			if ( isset( $wp_post_types[ $type ] ) && $wp_post_types[ $type ]->can_export ) {
				$wp_post_types[ $type ]->can_export = false;
				$hidden[]                           = $type;
			}
		}
		// export_wp() has no id range: its post id query gets one here. The head's author list is
		// kept whole, as a single export would have it (later batches' items need those authors).
		$limited = false;
		$posts   = $wpdb->posts;
		$range   = $wpdb->prepare( " AND {$wpdb->posts}.ID > %d AND {$wpdb->posts}.ID <= %d ORDER BY {$wpdb->posts}.ID", $after, $upto );
		$filter  = static function ( $query ) use ( &$limited, $posts, $range ) {
			if ( ! $limited && 0 === strpos( $query, "SELECT ID FROM {$posts} " ) && false !== strpos( $query, "WHERE {$posts}.post_type" ) && false === stripos( $query, ' LIMIT ' ) ) {
				$limited = true;
				return $query . $range;
			}
			if ( 0 === strpos( $query, "SELECT DISTINCT post_author FROM {$posts} WHERE post_status != 'auto-draft' AND ID IN (" ) ) {
				return "SELECT DISTINCT post_author FROM {$posts} WHERE post_status != 'auto-draft'";
			}
			return $query;
		};
		$out = new WP2P_Content_Filter(
			function ( $data ) use ( &$job, $writer ) {
				$writer->add( self::part( $job, 'content', '.xml' ), $data );
			},
			$head
		);
		add_filter( 'query', $filter, PHP_INT_MAX );
		ob_start( array( $out, 'chunk' ), 4 * MB_IN_BYTES );
		self::$exported = true;
		try {
			export_wp( array( 'content' => 'all' ) );
		} finally {
			ob_end_flush();
			remove_filter( 'query', $filter, PHP_INT_MAX );
			foreach ( $hidden as $type ) {
				$wp_post_types[ $type ]->can_export = true;
			}
			if ( ! headers_sent() ) {
				// export_wp() sends download headers; this request is not a download.
				header_remove( 'Content-Disposition' );
				header_remove( 'Content-Description' );
				header_remove( 'Content-Type' );
			}
		}
		if ( ! $limited && ! $whole ) {
			throw new RuntimeException( esc_html__( 'This WordPress version\'s export cannot be split into batches. Delete the package and build it with WP-CLI instead (wp pursae export).', 'wp-to-pursae' ) );
		}
		self::count( $job, 'content_bytes', $out->finish() );
	}

	/**
	 * The original file of every attachment.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param float       $until  Deadline.
	 * @return bool
	 */
	private static function step_media( array &$job, WP2P_Writer $writer, $until ) {
		global $wpdb;
		$max = self::max_media_bytes();
		while ( microtime( true ) < $until ) {
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery
			$ids = $wpdb->get_col( $wpdb->prepare( "SELECT ID FROM {$wpdb->posts} WHERE post_type = 'attachment' AND post_status <> 'trash' AND ID > %d ORDER BY ID LIMIT 25", $job['cursor'] ) );
			if ( ! $ids ) {
				return true;
			}
			$rows = array();
			$last = (int) $job['cursor'];
			foreach ( $ids as $n => $id ) {
				// The time budget is checked after every file, not only between batches of 25.
				if ( $n > 0 && microtime( true ) >= $until ) {
					break;
				}
				$id   = (int) $id;
				$prev = $last;
				$last = $id;
				$file = function_exists( 'wp_get_original_image_path' ) ? wp_get_original_image_path( $id ) : false;
				if ( ! $file || ! is_readable( $file ) ) {
					$file = get_attached_file( $id );
				}
				$name = $file ? wp_basename( $file ) : (string) $id;
				if ( ! $file || ! is_readable( $file ) ) {
					$job['skipped'][] = array(
						'kind'   => 'media',
						'item'   => $name,
						'reason' => __( 'the file is missing on the WordPress server', 'wp-to-pursae' ),
					);
					continue;
				}
				$size = filesize( $file );
				if ( $size > $max ) {
					$job['skipped'][] = array(
						'kind'   => 'media',
						'item'   => $name,
						/* translators: %s: size limit, e.g. "64 MB". */
						'reason' => sprintf( __( 'larger than %s: copy it by hand', 'wp-to-pursae' ), size_format( $max ) ),
					);
					continue;
				}
				if ( $size > self::BIG_FILE && $rows ) {
					// A big file takes a request of its own: what is packed so far is saved first.
					$last = $prev;
					break;
				}
				if ( ! $writer->add_file( 'media/' . $id, $file ) ) {
					continue;
				}
				$rows[] = array(
					'id'   => $id,
					'name' => $name,
					'url'  => wp_get_attachment_url( $id ),
					'size' => (int) $size,
					'mime' => get_post_mime_type( $id ),
				);
				self::count( $job, 'media_bytes', (int) $size );
				if ( $size > self::BIG_FILE ) {
					break; // ... and is the last of its request.
				}
			}
			if ( $rows ) {
				$writer->add( self::part( $job, 'media', '.jsonl' ), self::jsonl( $rows ) );
				self::count( $job, 'media', count( $rows ) );
			}
			$job['cursor'] = $last;
			// Media batches are heavy: hand back after each so progress is saved.
			return false;
		}
		return false;
	}

	/**
	 * WooCommerce tax rates.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @return bool
	 */
	private static function step_tax_rates( array &$job, WP2P_Writer $writer ) {
		if ( WP2P_Woo::active() ) {
			$rows = WP2P_Woo::tax_rates();
			if ( $rows ) {
				$writer->add( 'woo/tax_rates.jsonl', self::jsonl( $rows ) );
			}
			self::count( $job, 'tax_rates', count( $rows ) );
		}
		return true;
	}

	/**
	 * WooCommerce coupons.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param float       $until  Deadline.
	 * @return bool
	 */
	private static function step_coupons( array &$job, WP2P_Writer $writer, $until ) {
		if ( ! WP2P_Woo::active() ) {
			return true;
		}
		while ( microtime( true ) < $until ) {
			$batch = WP2P_Woo::coupons( (int) $job['cursor'], 100 );
			if ( ! $batch['rows'] && $batch['last'] === (int) $job['cursor'] ) {
				return true;
			}
			if ( $batch['rows'] ) {
				$writer->add( self::part( $job, 'woo/coupons', '.jsonl' ), self::jsonl( $batch['rows'] ) );
				self::count( $job, 'coupons', count( $batch['rows'] ) );
			}
			$job['cursor'] = $batch['last'];
		}
		return false;
	}

	/**
	 * WooCommerce orders with their items, refunds, notes and download permissions.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param float       $until  Deadline.
	 * @return bool
	 */
	private static function step_orders( array &$job, WP2P_Writer $writer, $until ) {
		if ( ! WP2P_Woo::active() ) {
			return true;
		}
		while ( microtime( true ) < $until ) {
			// By order id, not page number: orders deleted or added meanwhile cannot shift a page.
			$batch = WP2P_Woo::orders( (int) $job['cursor'], 50 );
			if ( ! $batch['rows'] && $batch['last'] === (int) $job['cursor'] ) {
				return true;
			}
			if ( $batch['rows'] ) {
				$writer->add( self::part( $job, 'woo/orders', '.jsonl' ), self::jsonl( $batch['rows'] ) );
				self::count( $job, 'orders', count( $batch['rows'] ) );
				foreach ( $batch['rows'] as $r ) {
					self::count( $job, 'refunds', count( $r['refunds'] ) );
				}
			}
			$job['cursor'] = $batch['last'];
		}
		return false;
	}

	/**
	 * WooCommerce customers.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @param float       $until  Deadline.
	 * @return bool
	 */
	private static function step_customers( array &$job, WP2P_Writer $writer, $until ) {
		if ( ! WP2P_Woo::active() ) {
			return true;
		}
		while ( microtime( true ) < $until ) {
			$batch = WP2P_Woo::customers( (int) $job['cursor'], 200 );
			if ( $batch['rows'] ) {
				$writer->add( self::part( $job, 'woo/customers', '.jsonl' ), self::jsonl( $batch['rows'] ) );
				self::count( $job, 'customers', count( $batch['rows'] ) );
			}
			$job['cursor'] = $batch['last'];
			if ( $batch['done'] ) {
				return true;
			}
		}
		return false;
	}

	/**
	 * The manifest, last: it carries the final counts and what was left out.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @return bool
	 */
	private static function step_close( array &$job, WP2P_Writer $writer ) {
		global $wp_version;
		$manifest = array(
			'format'        => self::CONTENT_FORMAT,
			'generator'     => 'wp-to-pursae/' . WP2P_VERSION,
			'site_url'      => site_url(),
			'home_url'      => home_url(),
			'uploads_url'   => wp_upload_dir()['baseurl'],
			'wp_version'    => $wp_version,
			'wc_version'    => WP2P_Woo::active() ? WC()->version : '',
			'php_version'   => PHP_VERSION,
			'multisite'     => is_multisite(),
			'order_storage' => WP2P_Woo::active() ? WP2P_Woo::order_storage() : '',
			'created_gmt'   => gmdate( 'Y-m-d H:i:s' ),
			'counts'        => $job['counts'] ? $job['counts'] : new stdClass(),
			'skipped'       => $job['skipped'],
		);
		$writer->add( 'manifest.json', wp_json_encode( $manifest, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE ) );
		return true;
	}

	/**
	 * The download file name: <host>-<date>.wp2p.
	 *
	 * @return string
	 */
	public static function download_name() {
		$host = wp_parse_url( home_url(), PHP_URL_HOST );
		return sanitize_file_name( ( $host ? $host : 'site' ) . '-' . gmdate( 'Y-m-d' ) . '.wp2p' );
	}

	/**
	 * A human label of the current step.
	 *
	 * @param array $job Job.
	 * @return string
	 */
	public static function step_label( array $job ) {
		$labels = array(
			'users'     => __( 'Users', 'wp-to-pursae' ),
			'options'   => __( 'Settings', 'wp-to-pursae' ),
			'content'   => __( 'Content (posts, pages, menus, products)', 'wp-to-pursae' ),
			'media'     => __( 'Media files', 'wp-to-pursae' ),
			'tax_rates' => __( 'Tax rates', 'wp-to-pursae' ),
			'coupons'   => __( 'Coupons', 'wp-to-pursae' ),
			'orders'    => __( 'Orders', 'wp-to-pursae' ),
			'customers' => __( 'Customers', 'wp-to-pursae' ),
			'close'     => __( 'Finishing the package', 'wp-to-pursae' ),
		);
		$step   = isset( self::$steps[ $job['step'] ] ) ? self::$steps[ $job['step'] ] : 'close';
		return $labels[ $step ];
	}
}
