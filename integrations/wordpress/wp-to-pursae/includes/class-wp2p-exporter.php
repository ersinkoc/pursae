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

	const OPTION = 'wp2p_job';
	const TTL    = DAY_IN_SECONDS;

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
	 * The largest media file that is packed (Pursae's upload limit is 64 MB).
	 *
	 * @return int
	 */
	public static function max_media_bytes() {
		/**
		 * Filters the size above which a media file is left out of the package (it is listed in the
		 * report so it can be copied by hand).
		 *
		 * @param int $bytes Size in bytes.
		 */
		return (int) apply_filters( 'wp2p_max_media_bytes', 64 * MB_IN_BYTES );
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
		if ( ! wp_next_scheduled( 'wp2p_cleanup' ) ) {
			wp_schedule_single_event( time() + self::TTL + HOUR_IN_SECONDS, 'wp2p_cleanup' );
		}
		return $job;
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
	 * Cron: removes a package older than a day.
	 */
	public static function cleanup_expired() {
		$job = self::job();
		if ( $job && time() - (int) $job['created'] > self::TTL ) {
			self::discard();
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
	 * Works on the job for at most $budget seconds.
	 *
	 * @param float $budget Seconds.
	 * @return array The job.
	 */
	public static function run( $budget ) {
		$job = self::job();
		if ( ! $job || 'building' !== $job['status'] ) {
			return $job;
		}
		$until  = microtime( true ) + $budget;
		$key    = WP2P_Crypto::decode_key( $job['key'] );
		$writer = new WP2P_Writer( $job['writer'], $key );
		$steps  = count( self::$steps );
		try {
			while ( $job['step'] < $steps ) {
				$step = self::$steps[ $job['step'] ];
				$done = self::step( $step, $job, $writer, $until );
				if ( 'close' === $step ) {
					$job['writer'] = $writer->finish();
					$job['size']   = filesize( $job['file'] );
					$job['sha256'] = hash_file( 'sha256', $job['file'] );
					$job['status'] = 'built';
					++$job['step'];
					self::save( $job );
					return $job;
				}
				$job['writer'] = $writer->commit();
				if ( $done ) {
					++$job['step'];
					$job['cursor'] = 0;
					$job['part']   = 0;
				}
				self::save( $job );
				if ( microtime( true ) >= $until ) {
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
				return self::step_content( $job, $writer );
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
	 * All content as WordPress' own WXR export (Tools › Export › All content), written in parts as
	 * export_wp() prints it. WooCommerce orders, refunds and coupons are left out: they come in their
	 * own files.
	 *
	 * @param array       $job    Job.
	 * @param WP2P_Writer $writer Writer.
	 * @return bool
	 */
	private static function step_content( array &$job, WP2P_Writer $writer ) {
		global $wp_post_types;
		require_once ABSPATH . 'wp-admin/includes/export.php';
		$hidden = array();
		foreach ( array( 'shop_order', 'shop_order_refund', 'shop_order_placehold', 'shop_coupon', 'shop_subscription', 'scheduled-action' ) as $type ) {
			if ( isset( $wp_post_types[ $type ] ) && $wp_post_types[ $type ]->can_export ) {
				$wp_post_types[ $type ]->can_export = false;
				$hidden[]                           = $type;
			}
		}
		$written = 0;
		ob_start(
			function ( $chunk ) use ( &$job, $writer, &$written ) {
				if ( '' !== $chunk ) {
					$writer->add( self::part( $job, 'content', '.xml' ), $chunk );
					$written += strlen( $chunk );
				}
				return '';
			},
			4 * MB_IN_BYTES
		);
		try {
			export_wp( array( 'content' => 'all' ) );
		} finally {
			ob_end_flush();
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
		$job['counts']['content_bytes'] = $written;
		return true;
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
			foreach ( $ids as $id ) {
				$id   = (int) $id;
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
			}
			if ( $rows ) {
				$writer->add( self::part( $job, 'media', '.jsonl' ), self::jsonl( $rows ) );
				self::count( $job, 'media', count( $rows ) );
			}
			$job['cursor'] = (int) end( $ids );
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
			$page = (int) $job['cursor'] + 1;
			$rows = WP2P_Woo::orders( $page, 50 );
			if ( ! $rows ) {
				return true;
			}
			$writer->add( self::part( $job, 'woo/orders', '.jsonl' ), self::jsonl( $rows ) );
			self::count( $job, 'orders', count( $rows ) );
			foreach ( $rows as $r ) {
				self::count( $job, 'refunds', count( $r['refunds'] ) );
			}
			$job['cursor'] = $page;
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
			'format'        => WP2P_Crypto::FORMAT,
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
