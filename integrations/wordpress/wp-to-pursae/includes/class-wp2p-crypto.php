<?php
/**
 * Package encryption.
 *
 * The package is sealed in chunks with libsodium's crypto_secretbox (XSalsa20-Poly1305). WordPress
 * ships sodium_compat, so this works even where PHP's sodium extension is missing (slower). Every
 * chunk has its own random 24-byte nonce, stored in front of it, so a chunk that is sealed again
 * after a request died (the writer cuts the file back to its last commit) never reuses a nonce. The
 * sealed plaintext starts with uint64(i), the chunk's index with the top bit set on the last chunk,
 * which lets Pursae detect reordered, dropped or cut-off chunks. The format is described in Pursae's
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
	const FORMAT    = 2; // The envelope: 1 used a counter nonce, which a resumed build could reuse.
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
	 * The file header: magic, format, a random package id.
	 *
	 * @param string $id 16 random bytes.
	 * @return string
	 */
	public static function header( $id ) {
		return self::MAGIC . chr( self::FORMAT ) . $id;
	}

	/**
	 * Seals one chunk: a big-endian length, then a fresh random nonce and the secretbox of the
	 * chunk's index (uint64, top bit on the last chunk) followed by the plaintext.
	 *
	 * @param string $plain Plaintext (at most 8 MiB).
	 * @param string $key   Raw key.
	 * @param int    $index Chunk index.
	 * @param bool   $last  Whether this is the last chunk.
	 * @return string
	 */
	public static function seal( $plain, $key, $index, $last ) {
		$high   = ( $index >> 32 ) & 0x7fffffff;
		$high  |= $last ? 0x80000000 : 0;
		$nonce  = random_bytes( SODIUM_CRYPTO_SECRETBOX_NONCEBYTES );
		$sealed = $nonce . sodium_crypto_secretbox( pack( 'NN', $high, $index & 0xffffffff ) . $plain, $nonce, $key );
		return pack( 'N', strlen( $sealed ) ) . $sealed;
	}
}
