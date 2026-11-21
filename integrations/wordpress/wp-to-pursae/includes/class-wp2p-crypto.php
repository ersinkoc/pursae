<?php
/**
 * Package encryption.
 *
 * The package is sealed in chunks with libsodium's crypto_secretbox (XSalsa20-Poly1305). WordPress
 * ships sodium_compat, so this works even where PHP's sodium extension is missing (slower). Chunk i
 * uses the nonce prefix || uint64(i) with the top bit set on the last chunk, which lets Pursae detect
 * reordered, dropped or cut-off chunks. The format is described in Pursae's
 * docs/developer/operations/migrate-from-wordpress.md.
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * Keys and chunk sealing.
 */
class WP2P_Crypto {

	const MAGIC     = 'PURSAEWP';
	const FORMAT    = 1;
	const KEY_LABEL = 'pursae-wp2p-package-key-v1';

	/**
	 * A new random package key (32 bytes).
	 *
	 * @return string
	 */
	public static function random_key() {
		return random_bytes( 32 );
	}

	/**
	 * The key of a package pushed with a migration token: HMAC-SHA256(token, label).
	 *
	 * @param string $token The migration token from Pursae.
	 * @return string
	 */
	public static function token_key( $token ) {
		return hash_hmac( 'sha256', self::KEY_LABEL, trim( (string) $token ), true );
	}

	/**
	 * The key as the owner copies it: unpadded base64url.
	 *
	 * @param string $key Raw key.
	 * @return string
	 */
	public static function encode_key( $key ) {
		return rtrim( strtr( base64_encode( $key ), '+/', '-_' ), '=' ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions.obfuscation_base64_encode -- the key's text form.
	}

	/**
	 * Reads a key in its text form.
	 *
	 * @param string $text Key text.
	 * @return string|false Raw key, or false.
	 */
	public static function decode_key( $text ) {
		$text = preg_replace( '/[\s=]+/', '', (string) $text );
		$raw  = base64_decode( strtr( $text, '-_', '+/' ), true ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions.obfuscation_base64_decode -- the key's text form.
		return ( false !== $raw && 32 === strlen( $raw ) ) ? $raw : false;
	}

	/**
	 * The file header: magic, format, nonce prefix.
	 *
	 * @param string $prefix 16-byte nonce prefix.
	 * @return string
	 */
	public static function header( $prefix ) {
		return self::MAGIC . chr( self::FORMAT ) . $prefix;
	}

	/**
	 * Seals one chunk: a big-endian length, then the secretbox.
	 *
	 * @param string $plain  Plaintext (at most 8 MiB).
	 * @param string $key    Raw key.
	 * @param string $prefix Nonce prefix.
	 * @param int    $index  Chunk index.
	 * @param bool   $last   Whether this is the last chunk.
	 * @return string
	 */
	public static function seal( $plain, $key, $prefix, $index, $last ) {
		$high   = ( $index >> 32 ) & 0x7fffffff;
		$high  |= $last ? 0x80000000 : 0;
		$nonce  = $prefix . pack( 'NN', $high, $index & 0xffffffff );
		$sealed = sodium_crypto_secretbox( $plain, $nonce, $key );
		return pack( 'N', strlen( $sealed ) ) . $sealed;
	}
}
