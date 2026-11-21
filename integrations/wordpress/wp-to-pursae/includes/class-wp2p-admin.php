<?php
/**
 * Tools › Move to Pursae.
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * The admin screen and its AJAX steps.
 */
class WP2P_Admin {

	const NONCE = 'wp2p';
	const SLUG  = 'wp-to-pursae';

	/**
	 * Hooks.
	 */
	public static function init() {
		add_action( 'admin_menu', array( __CLASS__, 'menu' ) );
		add_action( 'admin_enqueue_scripts', array( __CLASS__, 'assets' ) );
		foreach ( array( 'start', 'step', 'cancel', 'remote_status' ) as $a ) {
			add_action( 'wp_ajax_wp2p_' . $a, array( __CLASS__, 'ajax_' . $a ) );
		}
		add_action( 'admin_post_wp2p_download', array( __CLASS__, 'download' ) );
		add_filter( 'plugin_action_links_' . plugin_basename( WP2P_FILE ), array( __CLASS__, 'action_links' ) );
	}

	/**
	 * Adds the Tools entry.
	 */
	public static function menu() {
		add_management_page( __( 'Move to Pursae', 'wp-to-pursae' ), __( 'Move to Pursae', 'wp-to-pursae' ), 'manage_options', self::SLUG, array( __CLASS__, 'page' ) );
	}

	/**
	 * A "Move to Pursae" link on the Plugins screen.
	 *
	 * @param string[] $links Links.
	 * @return string[]
	 */
	public static function action_links( $links ) {
		array_unshift( $links, '<a href="' . esc_url( admin_url( 'tools.php?page=' . self::SLUG ) ) . '">' . esc_html__( 'Move to Pursae', 'wp-to-pursae' ) . '</a>' );
		return $links;
	}

	/**
	 * Script and style of the screen.
	 *
	 * @param string $hook Screen hook.
	 */
	public static function assets( $hook ) {
		if ( 'tools_page_' . self::SLUG !== $hook ) {
			return;
		}
		wp_enqueue_style( 'wp2p-admin', plugins_url( 'assets/admin.css', WP2P_FILE ), array(), WP2P_VERSION );
		wp_enqueue_script( 'wp2p-admin', plugins_url( 'assets/admin.js', WP2P_FILE ), array(), WP2P_VERSION, true );
		wp_localize_script(
			'wp2p-admin',
			'wp2p',
			array(
				'ajax'  => admin_url( 'admin-ajax.php' ),
				'nonce' => wp_create_nonce( self::NONCE ),
				'job'   => self::summary( WP2P_Exporter::job() ),
				'i18n'  => array(
					'building'   => __( 'Building the package…', 'wp-to-pursae' ),
					'readyTitle' => __( 'The package is ready', 'wp-to-pursae' ),
					'sentTitle'  => __( 'Sent to Pursae', 'wp-to-pursae' ),
					'again'      => __( 'Start a new move', 'wp-to-pursae' ),
					'uploading'  => __( 'Sending to Pursae…', 'wp-to-pursae' ),
					/* translators: %s: percentage. */
					'sent'       => __( 'Sent: %s', 'wp-to-pursae' ),
					'failed'     => __( 'Something went wrong:', 'wp-to-pursae' ),
					'confirm'    => __( 'Delete the package and start over?', 'wp-to-pursae' ),
					'network'    => __( 'The request failed; retrying…', 'wp-to-pursae' ),
					'imported'   => __( 'Pursae is importing the site:', 'wp-to-pursae' ),
					'done'       => __( 'Pursae has imported the site.', 'wp-to-pursae' ),
					'importErr'  => __( 'The import stopped in Pursae:', 'wp-to-pursae' ),
				),
			)
		);
	}

	/**
	 * What the screen may know about the job: never the token or the Application Password; the key
	 * only of a built download package (it is shown to the owner, who needs it in Pursae).
	 *
	 * @param array|null $job Job.
	 * @return array|null
	 */
	public static function summary( $job ) {
		if ( ! $job ) {
			return null;
		}
		$total = count( WP2P_Exporter::$steps );
		$out   = array(
			'mode'       => $job['mode'],
			'status'     => $job['status'],
			'message'    => $job['message'],
			'step'       => WP2P_Exporter::step_label( $job ),
			'progress'   => min( 100, (int) floor( 100 * min( $job['step'], $total ) / $total ) ),
			'counts'     => $job['counts'],
			'skipped'    => array_slice( $job['skipped'], 0, 100 ),
			'size'       => isset( $job['size'] ) ? size_format( $job['size'], 1 ) : '',
			'status_url' => isset( $job['status_url'] ) ? $job['status_url'] : '',
		);
		if ( isset( $job['upload']['received'], $job['size'] ) && $job['size'] > 0 ) {
			$out['sent'] = (int) floor( 100 * $job['upload']['received'] / $job['size'] );
		}
		if ( 'download' === $job['mode'] && 'built' === $job['status'] ) {
			$out['key']      = $job['key'];
			$out['download'] = add_query_arg( '_wpnonce', wp_create_nonce( 'wp2p_download' ), admin_url( 'admin-post.php?action=wp2p_download' ) ); // Raw URL: the script sets it as href.
			$out['filename'] = WP2P_Exporter::download_name();
		}
		return $out;
	}

	/**
	 * Checks the capability and the nonce of an AJAX request.
	 */
	private static function guard() {
		if ( ! current_user_can( 'manage_options' ) ) {
			wp_send_json_error( array( 'message' => __( 'Sorry, you are not allowed to do that.', 'wp-to-pursae' ) ), 403 );
		}
	}

	/**
	 * Starts a job.
	 */
	public static function ajax_start() {
		self::guard();
		check_ajax_referer( self::NONCE );
		$mode = isset( $_POST['mode'] ) && 'push' === $_POST['mode'] ? 'push' : 'download';
		try {
			if ( 'push' === $mode ) {
				$url = WP2P_Pusher::check_url( isset( $_POST['url'] ) ? sanitize_text_field( wp_unslash( $_POST['url'] ) ) : '' );
				if ( is_wp_error( $url ) ) {
					wp_send_json_error( array( 'message' => $url->get_error_message() ) );
				}
				$token    = isset( $_POST['token'] ) ? sanitize_text_field( wp_unslash( $_POST['token'] ) ) : '';
				$app_user = isset( $_POST['app_user'] ) ? sanitize_user( wp_unslash( $_POST['app_user'] ) ) : '';
				$app_pass = isset( $_POST['app_password'] ) ? preg_replace( '/\s+/', '', sanitize_text_field( wp_unslash( $_POST['app_password'] ) ) ) : '';
				if ( '' !== $token ) {
					$job = WP2P_Exporter::start(
						'push',
						WP2P_Crypto::token_key( $token ),
						array(
							'url'   => $url,
							'token' => $token,
						)
					);
				} elseif ( '' !== $app_user && '' !== $app_pass ) {
					$job = WP2P_Exporter::start(
						'push',
						WP2P_Crypto::random_key(),
						array(
							'url'          => $url,
							'app_user'     => $app_user,
							'app_password' => $app_pass,
						)
					);
				} else {
					wp_send_json_error( array( 'message' => __( 'Enter the migration token from Pursae (or an administrator\'s Application Password).', 'wp-to-pursae' ) ) );
				}
			} else {
				$job = WP2P_Exporter::start( 'download', WP2P_Crypto::random_key() );
			}
		} catch ( Throwable $e ) {
			wp_send_json_error( array( 'message' => $e->getMessage() ) );
		}
		wp_send_json_success( self::summary( $job ) );
	}

	/**
	 * Works on the job for a few seconds.
	 */
	public static function ajax_step() {
		self::guard();
		check_ajax_referer( self::NONCE );
		$job = WP2P_Exporter::job();
		if ( ! $job ) {
			wp_send_json_error( array( 'message' => __( 'There is no package in progress.', 'wp-to-pursae' ) ) );
		}
		if ( function_exists( 'set_time_limit' ) ) {
			set_time_limit( 120 ); // phpcs:ignore Squiz.PHP.DiscouragedFunctions.Discouraged -- each step stays well below this.
		}
		$resume = ! empty( $_POST['resume'] );
		if ( 'building' === $job['status'] ) {
			$job = WP2P_Exporter::run( 15 );
		} elseif ( 'error' === $job['status'] && $resume ) {
			$job['status']  = 'building';
			$job['message'] = '';
			WP2P_Exporter::save( $job );
			$job = WP2P_Exporter::run( 15 );
		} elseif ( 'push' === $job['mode'] && in_array( $job['status'], array( 'built', 'uploading' ), true ) && ( '' === $job['message'] || $resume ) ) {
			$job['message'] = '';
			WP2P_Exporter::save( $job );
			$job = WP2P_Pusher::run( 15 );
		}
		wp_send_json_success( self::summary( $job ) );
	}

	/**
	 * Deletes the package and the job.
	 */
	public static function ajax_cancel() {
		self::guard();
		check_ajax_referer( self::NONCE );
		WP2P_Exporter::discard();
		wp_send_json_success( null );
	}

	/**
	 * The import's progress in Pursae, after the package was sent.
	 */
	public static function ajax_remote_status() {
		self::guard();
		check_ajax_referer( self::NONCE );
		$job = WP2P_Exporter::job();
		$s   = $job ? WP2P_Pusher::remote_status( $job ) : new WP_Error( 'wp2p', '' );
		if ( is_wp_error( $s ) ) {
			wp_send_json_error( array( 'message' => $s->get_error_message() ) );
		}
		wp_send_json_success(
			array(
				'state' => isset( $s['state'] ) ? sanitize_key( $s['state'] ) : '',
				'phase' => isset( $s['phase_label'] ) ? sanitize_text_field( $s['phase_label'] ) : '',
				'error' => isset( $s['error'] ) ? sanitize_text_field( $s['error'] ) : '',
			)
		);
	}

	/**
	 * Streams the built package.
	 */
	public static function download() {
		if ( ! current_user_can( 'manage_options' ) ) {
			wp_die( esc_html__( 'Sorry, you are not allowed to do that.', 'wp-to-pursae' ), 403 );
		}
		check_admin_referer( 'wp2p_download' );
		$job = WP2P_Exporter::job();
		if ( ! $job || 'built' !== $job['status'] || ! is_readable( $job['file'] ) ) {
			wp_die( esc_html__( 'There is no package to download.', 'wp-to-pursae' ), 404 );
		}
		nocache_headers();
		header( 'Content-Type: application/octet-stream' );
		header( 'Content-Disposition: attachment; filename="' . WP2P_Exporter::download_name() . '"' );
		header( 'Content-Length: ' . filesize( $job['file'] ) );
		header( 'X-Content-Type-Options: nosniff' );
		while ( ob_get_level() ) {
			ob_end_clean();
		}
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_readfile -- streams the package.
		readfile( $job['file'] );
		exit;
	}

	/**
	 * The screen.
	 */
	public static function page() {
		if ( ! current_user_can( 'manage_options' ) ) {
			return;
		}
		$woo = WP2P_Woo::active();
		?>
		<div class="wrap wp2p">
			<h1><?php esc_html_e( 'Move to Pursae', 'wp-to-pursae' ); ?></h1>
			<p><?php esc_html_e( 'Move this site to Pursae in one go. Your users keep their passwords; posts, pages, comments, categories and tags, media files, menus and the main settings come along.', 'wp-to-pursae' ); ?>
			<?php if ( $woo ) : ?>
				<?php esc_html_e( 'So does the WooCommerce store: products and variations, tax rates, coupons, orders with their refunds and notes, and customers.', 'wp-to-pursae' ); ?>
			<?php endif; ?></p>
			<p class="description"><?php esc_html_e( 'Themes and plugins do not move: they are PHP code, and Pursae is not PHP. Pick a Pursae theme after the move; most popular plugins have a built-in Pursae equivalent. Payment gateway settings are not exported: set up payments again in Pursae.', 'wp-to-pursae' ); ?></p>

			<div id="wp2p-form" class="card">
				<h2 class="title"><?php esc_html_e( 'How do you want to move?', 'wp-to-pursae' ); ?></h2>
				<fieldset>
					<p><label><input type="radio" name="wp2p-mode" value="push" checked="checked" /> <strong><?php esc_html_e( 'Send to Pursae', 'wp-to-pursae' ); ?></strong> &mdash; <?php esc_html_e( 'recommended: the package goes straight to your Pursae site, and the import starts on its own.', 'wp-to-pursae' ); ?></label></p>
					<p><label><input type="radio" name="wp2p-mode" value="download" /> <strong><?php esc_html_e( 'Download a package', 'wp-to-pursae' ); ?></strong> &mdash; 
					<?php
					/* translators: %s: the command, pursae import WordPress. */
					printf( esc_html__( 'upload it in Pursae yourself (Tools › Import › From WordPress) or import it with %s.', 'wp-to-pursae' ), '<code>pursae import wordpress</code>' ); // phpcs:ignore WordPress.WP.CapitalPDangit.MisspelledInText, WordPress.Security.EscapeOutput.OutputNotEscaped -- a command, literal markup.
					?>
					</label></p>
				</fieldset>
				<div id="wp2p-push">
					<p><label for="wp2p-url"><?php esc_html_e( 'Pursae site address', 'wp-to-pursae' ); ?></label><br />
					<input type="url" id="wp2p-url" class="regular-text" placeholder="https://example.com" autocomplete="off" /></p>
					<p><label for="wp2p-token"><?php esc_html_e( 'Migration token', 'wp-to-pursae' ); ?></label><br />
					<input type="password" id="wp2p-token" class="regular-text code" autocomplete="off" /><br />
					<span class="description"><?php esc_html_e( 'In Pursae: Tools › Import › From WordPress › Generate a migration token. It works once, for 24 hours.', 'wp-to-pursae' ); ?></span></p>
					<details><summary><?php esc_html_e( 'Use an Application Password instead', 'wp-to-pursae' ); ?></summary>
						<p><label for="wp2p-app-user"><?php esc_html_e( 'Pursae administrator username', 'wp-to-pursae' ); ?></label><br />
						<input type="text" id="wp2p-app-user" class="regular-text" autocomplete="off" /></p>
						<p><label for="wp2p-app-password"><?php esc_html_e( 'Application Password', 'wp-to-pursae' ); ?></label><br />
						<input type="password" id="wp2p-app-password" class="regular-text code" autocomplete="off" /></p>
					</details>
				</div>
				<p><button type="button" class="button button-primary" id="wp2p-start"><?php esc_html_e( 'Move to Pursae', 'wp-to-pursae' ); ?></button></p>
			</div>

			<div id="wp2p-progress" class="card" hidden>
				<h2 class="title" id="wp2p-title"></h2>
				<div class="wp2p-bar"><span id="wp2p-bar"></span></div>
				<p id="wp2p-step"></p>
				<ul id="wp2p-counts"></ul>
				<div id="wp2p-error" class="notice notice-error inline" hidden><p></p></div>
				<div id="wp2p-built" hidden>
					<div class="notice notice-success inline"><p><?php esc_html_e( 'The package is ready. Copy the key now and keep it safe: Pursae needs it to open the package, and the package holds your users\' password hashes.', 'wp-to-pursae' ); ?></p></div>
					<p><label for="wp2p-key"><?php esc_html_e( 'Package key', 'wp-to-pursae' ); ?></label><br />
					<input type="text" id="wp2p-key" class="large-text code" readonly="readonly" onfocus="this.select()" /></p>
					<p><a class="button button-primary" id="wp2p-download" href="#"><?php esc_html_e( 'Download the package', 'wp-to-pursae' ); ?></a> <span id="wp2p-size"></span></p>
					<p class="description"><?php esc_html_e( 'The package is deleted from this server automatically after 24 hours, or now with the button below.', 'wp-to-pursae' ); ?></p>
				</div>
				<div id="wp2p-sent" hidden>
					<div class="notice notice-success inline"><p id="wp2p-sent-text"><?php esc_html_e( 'The package was sent to Pursae and deleted from this server.', 'wp-to-pursae' ); ?></p></div>
					<p id="wp2p-remote"></p>
					<p><a class="button" id="wp2p-status-link" href="#" target="_blank" rel="noopener"><?php esc_html_e( 'Open the import in Pursae', 'wp-to-pursae' ); ?></a></p>
				</div>
				<ul id="wp2p-skipped" class="wp2p-skipped"></ul>
				<p>
					<button type="button" class="button" id="wp2p-resume" hidden><?php esc_html_e( 'Continue', 'wp-to-pursae' ); ?></button>
					<button type="button" class="button-link button-link-delete" id="wp2p-cancel"><?php esc_html_e( 'Delete the package and start over', 'wp-to-pursae' ); ?></button>
				</p>
			</div>
		</div>
		<?php
	}
}
