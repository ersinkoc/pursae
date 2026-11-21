<?php
/**
 * WP-CLI: wp pursae export / push.
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * Moves this site to Pursae from the command line.
 */
class WP2P_CLI {

	/**
	 * Builds a migration package for `pursae import wordpress`.
	 *
	 * The package is encrypted. The key is printed once; pass it to Pursae with --key (or the
	 * PURSAE_WP2P_KEY environment variable).
	 *
	 * ## OPTIONS
	 *
	 * [--file=<path>]
	 * : Where to write the package. Default: <host>-<date>.wp2p in the current directory.
	 *
	 * [--key=<key>]
	 * : Use this key (43 characters of base64url) instead of a random one.
	 *
	 * [--porcelain]
	 * : Print only the key.
	 *
	 * ## EXAMPLES
	 *
	 *     wp pursae export --file=/tmp/site.wp2p
	 *
	 * @param array $args       Positional arguments.
	 * @param array $assoc_args Options.
	 */
	public function export( $args, $assoc_args ) {
		$key = WP2P_Crypto::random_key();
		if ( ! empty( $assoc_args['key'] ) ) {
			$key = WP2P_Crypto::decode_key( $assoc_args['key'] );
			if ( false === $key ) {
				WP_CLI::error( 'The key must be 32 bytes in base64url (43 characters).' );
			}
		}
		$file = ! empty( $assoc_args['file'] ) ? $assoc_args['file'] : getcwd() . '/' . WP2P_Exporter::download_name();
		$job  = $this->build( 'download', $key, array(), ! empty( $assoc_args['porcelain'] ) );
		if ( ! @rename( $job['file'], $file ) && ! copy( $job['file'], $file ) ) { // phpcs:ignore WordPress.PHP.NoSilencedErrors.Discouraged, WordPress.WP.AlternativeFunctions.rename_rename -- moving our own file.
			WP_CLI::error( "Cannot write $file." );
		}
		WP2P_Exporter::discard();
		if ( ! empty( $assoc_args['porcelain'] ) ) {
			WP_CLI::line( WP2P_Crypto::encode_key( $key ) );
			return;
		}
		$this->report( $job );
		WP_CLI::success( sprintf( 'Package written to %s (%s).', $file, size_format( filesize( $file ), 1 ) ) );
		WP_CLI::line( 'Package key (keep it secret, Pursae needs it): ' . WP2P_Crypto::encode_key( $key ) );
		WP_CLI::line( 'Import it in Pursae: pursae import WordPress ' . basename( $file ) . ' --key=<key>' );
	}

	/**
	 * Builds the package and sends it to a Pursae site.
	 *
	 * ## OPTIONS
	 *
	 * --to=<url>
	 * : The Pursae site's address.
	 *
	 * [--token=<token>]
	 * : The migration token from Pursae (Tools › Import › From WordPress).
	 *
	 * [--user=<login>]
	 * : Instead of a token: a Pursae administrator...
	 *
	 * [--app-password=<password>]
	 * : ...and one of their Application Passwords.
	 *
	 * ## EXAMPLES
	 *
	 *     wp pursae push --to=https://new.example.com --token=pmt_...
	 *
	 * @param array $args       Positional arguments.
	 * @param array $assoc_args Options.
	 */
	public function push( $args, $assoc_args ) {
		$url = WP2P_Pusher::check_url( $assoc_args['to'] );
		if ( is_wp_error( $url ) ) {
			WP_CLI::error( $url->get_error_message() );
		}
		if ( ! empty( $assoc_args['token'] ) ) {
			$job = $this->build(
				'push',
				WP2P_Crypto::token_key( $assoc_args['token'] ),
				array(
					'url'   => $url,
					'token' => trim( $assoc_args['token'] ),
				)
			);
		} elseif ( ! empty( $assoc_args['app-password'] ) && ! empty( $assoc_args['user'] ) ) {
			$job = $this->build(
				'push',
				WP2P_Crypto::random_key(),
				array(
					'url'          => $url,
					'app_user'     => $assoc_args['user'],
					'app_password' => preg_replace( '/\s+/', '', $assoc_args['app-password'] ),
				)
			);
		} else {
			WP_CLI::error( 'Pass --token, or --user and --app-password.' );
		}
		$this->report( $job );
		$this->push_loop();
	}

	/**
	 * Continues a package build or upload that stopped.
	 *
	 * @param array $args       Positional arguments.
	 * @param array $assoc_args Options.
	 */
	public function resume( $args, $assoc_args ) {
		$job = WP2P_Exporter::job();
		if ( ! $job ) {
			WP_CLI::error( 'Nothing to resume.' );
		}
		$job['message'] = '';
		if ( 'error' === $job['status'] ) {
			$job['status'] = 'building';
		}
		WP2P_Exporter::save( $job );
		while ( 'building' === $job['status'] ) {
			$job = WP2P_Exporter::run( 60 );
		}
		if ( 'push' !== $job['mode'] ) {
			WP_CLI::success( 'Package built: ' . $job['file'] );
			return;
		}
		$this->push_loop();
	}

	/**
	 * Deletes the package and its working files.
	 */
	public function clean() {
		WP2P_Exporter::discard();
		WP_CLI::success( 'Removed.' );
	}

	/**
	 * Uploads until done.
	 */
	private function push_loop() {
		do {
			$job = WP2P_Pusher::run( 60 );
			if ( '' !== $job['message'] ) {
				WP_CLI::error( $job['message'] . ' (run `wp pursae resume` to continue)' );
			}
			if ( 'sent' !== $job['status'] ) {
				WP_CLI::log( sprintf( 'Sent %d of %d bytes …', $job['upload']['received'], $job['size'] ) );
			}
		} while ( 'sent' !== $job['status'] );
		WP_CLI::success( 'Sent. Pursae is importing the site: ' . $job['status_url'] );
	}

	/**
	 * Builds a package.
	 *
	 * @param string $mode  Mode.
	 * @param string $key   Raw key.
	 * @param array  $extra Push settings.
	 * @param bool   $quiet No progress lines.
	 * @return array The built job.
	 */
	private function build( $mode, $key, array $extra, $quiet = false ) {
		try {
			WP2P_Exporter::start( $mode, $key, $extra );
		} catch ( Throwable $e ) {
			WP_CLI::error( $e->getMessage() );
		}
		$last = '';
		do {
			$job   = WP2P_Exporter::run( 300 );
			$label = WP2P_Exporter::step_label( $job );
			if ( ! $quiet && $label !== $last && 'building' === $job['status'] ) {
				WP_CLI::log( $label . ' …' );
				$last = $label;
			}
		} while ( 'building' === $job['status'] );
		if ( 'error' === $job['status'] ) {
			WP_CLI::error( $job['message'] . ' (run `wp pursae resume` to continue)' );
		}
		return $job;
	}

	/**
	 * Prints the counts and what was left out.
	 *
	 * @param array $job Job.
	 */
	private function report( array $job ) {
		foreach ( $job['counts'] as $k => $n ) {
			if ( false === strpos( $k, '_bytes' ) ) {
				WP_CLI::log( sprintf( '%s: %d', str_replace( '_', ' ', $k ), $n ) );
			}
		}
		foreach ( $job['skipped'] as $s ) {
			WP_CLI::warning( sprintf( 'Left out %s %s: %s', $s['kind'], $s['item'], $s['reason'] ) );
		}
	}
}
