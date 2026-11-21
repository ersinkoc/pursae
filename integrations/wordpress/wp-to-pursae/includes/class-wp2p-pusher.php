<?php
/**
 * Sends the package to Pursae ("Send to Pursae").
 *
 * The package goes up in chunks to Pursae's pursae/v1/migrate routes. Each chunk names its offset;
 * when a request fails the plugin asks Pursae how much it has and goes on from there, so a dropped
 * connection or a timed-out request only costs one chunk. Authentication is the one-time migration
 * token generated in Pursae (Tools › Import › From WordPress), or an administrator's Application
 * Password, in which case the package key is sent along with the first request (over HTTPS).
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * Upload client.
 */
class WP2P_Pusher {

	const MAX_CHUNK = 8388608;
	const RETRIES   = 6;

	/**
	 * Checks and normalizes the Pursae address. HTTPS is required, except for local test hosts or
	 * when WP2P_ALLOW_HTTP is defined as true.
	 *
	 * @param string $url Address as entered.
	 * @return string|WP_Error
	 */
	public static function check_url( $url ) {
		$url   = esc_url_raw( trim( (string) $url ) );
		$parts = wp_parse_url( $url );
		if ( ! $parts || empty( $parts['host'] ) || empty( $parts['scheme'] ) || ! in_array( $parts['scheme'], array( 'http', 'https' ), true ) ) {
			return new WP_Error( 'wp2p_url', __( 'Enter the address of the Pursae site, for example https://example.com.', 'wp-to-pursae' ) );
		}
		$host  = strtolower( $parts['host'] );
		$local = in_array( $host, array( 'localhost', '127.0.0.1', '::1', 'host.docker.internal' ), true ) || preg_match( '/\.(test|localhost|local)$/', $host );
		if ( 'https' !== $parts['scheme'] && ! $local && ! ( defined( 'WP2P_ALLOW_HTTP' ) && WP2P_ALLOW_HTTP ) ) {
			return new WP_Error( 'wp2p_https', __( 'The Pursae address must start with https://: the package holds your users\' password hashes and goes over the network.', 'wp-to-pursae' ) );
		}
		return untrailingslashit( $url );
	}

	/**
	 * Sends a request to the migrate API.
	 *
	 * @param array  $job    Job (url and credentials).
	 * @param string $method HTTP method.
	 * @param string $path   Path below pursae/v1/migrate.
	 * @param mixed  $body   Array or object (sent as JSON), string (raw bytes) or null.
	 * @return array|WP_Error array( code, data ).
	 */
	public static function request( array $job, $method, $path, $body = null ) {
		$headers = array( 'Accept' => 'application/json' );
		if ( ! empty( $job['token'] ) ) {
			$headers['X-Pursae-Migration-Token'] = $job['token'];
		} elseif ( ! empty( $job['app_user'] ) ) {
			$headers['Authorization'] = 'Basic ' . base64_encode( $job['app_user'] . ':' . $job['app_password'] ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions.obfuscation_base64_encode -- HTTP Basic auth.
		}
		if ( is_array( $body ) || is_object( $body ) ) {
			$headers['Content-Type'] = 'application/json';
			$body                    = wp_json_encode( $body );
		} elseif ( is_string( $body ) ) {
			$headers['Content-Type'] = 'application/octet-stream';
		}
		$resp = wp_remote_request(
			$job['url'] . '/wp-json/pursae/v1/migrate' . $path,
			array(
				'method'      => $method,
				'headers'     => $headers,
				'body'        => $body,
				'timeout'     => 120,
				'redirection' => 0,
				'user-agent'  => 'WP to Pursae/' . WP2P_VERSION,
			)
		);
		if ( is_wp_error( $resp ) ) {
			return $resp;
		}
		$data = json_decode( wp_remote_retrieve_body( $resp ), true );
		return array(
			'code' => (int) wp_remote_retrieve_response_code( $resp ),
			'data' => is_array( $data ) ? $data : array(),
		);
	}

	/**
	 * An error from an answer of Pursae.
	 *
	 * @param array|WP_Error $r Answer.
	 * @return string
	 */
	private static function error_text( $r ) {
		if ( is_wp_error( $r ) ) {
			return $r->get_error_message();
		}
		$msg = isset( $r['data']['message'] ) ? $r['data']['message'] : '';
		/* translators: 1: HTTP status code, 2: message from Pursae. */
		return sprintf( __( 'Pursae answered %1$d: %2$s', 'wp-to-pursae' ), $r['code'], $msg );
	}

	/**
	 * Uploads for at most $budget seconds.
	 *
	 * @param float $budget Seconds.
	 * @return array The job.
	 */
	public static function run( $budget ) {
		$job = WP2P_Exporter::job();
		if ( ! $job || ! in_array( $job['status'], array( 'built', 'uploading' ), true ) || 'push' !== $job['mode'] ) {
			return $job;
		}
		$until = microtime( true ) + $budget;
		$up    = isset( $job['upload'] ) ? $job['upload'] : array();
		if ( empty( $up['id'] ) ) {
			$start = array(
				'size'   => (int) $job['size'],
				'sha256' => $job['sha256'],
			);
			if ( empty( $job['token'] ) ) {
				$start['key'] = $job['key'];
			}
			$r = self::request( $job, 'POST', '/start', $start );
			if ( is_wp_error( $r ) || 200 !== $r['code'] || empty( $r['data']['id'] ) ) {
				return self::fail( $job, self::error_text( $r ) );
			}
			$up = array(
				'id'       => sanitize_key( $r['data']['id'] ),
				'received' => (int) $r['data']['received'],
				'chunk'    => min( self::MAX_CHUNK, max( 65536, (int) $r['data']['chunk_size'] ) ),
				'retries'  => 0,
			);
		}
		$job['status'] = 'uploading';
		$job['upload'] = $up;
		WP2P_Exporter::save( $job );
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fopen -- streamed binary input.
		$fh = fopen( $job['file'], 'rb' );
		if ( ! $fh ) {
			return self::fail( $job, __( 'The package file is gone; build it again.', 'wp-to-pursae' ) );
		}
		while ( $up['received'] < $job['size'] && microtime( true ) < $until ) {
			fseek( $fh, $up['received'] );
			// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fread -- streamed binary input.
			$data = fread( $fh, (int) min( $up['chunk'], $job['size'] - $up['received'] ) );
			$r    = self::request( $job, 'PUT', '/' . $up['id'] . '/chunk?offset=' . $up['received'], $data );
			if ( ! is_wp_error( $r ) && 200 === $r['code'] && isset( $r['data']['received'] ) ) {
				$up['received'] = (int) $r['data']['received'];
				$up['retries']  = 0;
			} else {
				++$up['retries'];
				if ( $up['retries'] > self::RETRIES ) {
					fclose( $fh ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
					return self::fail( $job, self::error_text( $r ) );
				}
				// Ask Pursae how much arrived and go on from there.
				$s = self::request( $job, 'GET', '/' . $up['id'] );
				if ( ! is_wp_error( $s ) && 200 === $s['code'] && isset( $s['data']['received'] ) ) {
					$up['received'] = (int) $s['data']['received'];
				}
				sleep( min( 10, $up['retries'] * 2 ) );
			}
			$job['upload'] = $up;
			WP2P_Exporter::save( $job );
		}
		fclose( $fh ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
		if ( $up['received'] < $job['size'] ) {
			return $job;
		}
		$r = self::request( $job, 'POST', '/' . $up['id'] . '/finish', new stdClass() );
		if ( is_wp_error( $r ) || ! in_array( $r['code'], array( 200, 202 ), true ) ) {
			if ( ! is_wp_error( $r ) && isset( $r['data']['data']['received'] ) ) {
				$job['upload']['received'] = (int) $r['data']['data']['received']; // A checksum mismatch: upload again.
				WP2P_Exporter::save( $job );
			}
			return self::fail( $job, self::error_text( $r ) );
		}
		// Sent: the package and its key are not needed here any more.
		$sent = array(
			'id'         => $job['id'],
			'mode'       => 'push',
			'status'     => 'sent',
			'url'        => $job['url'],
			'token'      => isset( $job['token'] ) ? $job['token'] : '',
			'upload'     => $job['upload'],
			'status_url' => isset( $r['data']['status_url'] ) ? esc_url_raw( $r['data']['status_url'] ) : '',
			'counts'     => $job['counts'],
			'skipped'    => $job['skipped'],
			'created'    => $job['created'],
			'step'       => count( WP2P_Exporter::$steps ),
			'message'    => '',
		);
		// An Application Password is not kept: the import's progress is then followed in Pursae.
		WP2P_Exporter::discard();
		WP2P_Exporter::save( $sent );
		return $sent;
	}

	/**
	 * Records an upload error; the upload can be resumed.
	 *
	 * @param array  $job     Job.
	 * @param string $message Error.
	 * @return array
	 */
	private static function fail( array $job, $message ) {
		$job['status']  = 'built';
		$job['message'] = $message;
		WP2P_Exporter::save( $job );
		return $job;
	}

	/**
	 * The import's state on the Pursae side (after the package was sent).
	 *
	 * @param array $job Job.
	 * @return array|WP_Error
	 */
	public static function remote_status( array $job ) {
		if ( empty( $job['upload']['id'] ) || ( empty( $job['token'] ) && empty( $job['app_user'] ) ) ) {
			return new WP_Error( 'wp2p_no_upload', __( 'Follow the import in Pursae (Tools › Import › From WordPress).', 'wp-to-pursae' ) );
		}
		$r = self::request( $job, 'GET', '/' . $job['upload']['id'] );
		if ( is_wp_error( $r ) ) {
			return $r;
		}
		if ( 200 !== $r['code'] ) {
			return new WP_Error( 'wp2p_status', self::error_text( $r ) );
		}
		return $r['data'];
	}
}
