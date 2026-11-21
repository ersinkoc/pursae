<?php
/**
 * The package writer: a ustar archive written straight into the encrypted stream.
 *
 * Nothing is written unencrypted: each batch (users, orders, part of the WXR) becomes its own
 * archive entry, sealed as it is added. The writer can stop after any batch and go on in the next
 * request: commit() seals what is buffered and records how many bytes of the file are complete, and
 * a later open() cuts the file back to that size, so a request that died mid-batch is simply redone.
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * Encrypted tar writer.
 */
class WP2P_Writer {

	const CHUNK = 1048576; // Plaintext bytes per sealed chunk.

	/**
	 * Open file handle.
	 *
	 * @var resource
	 */
	private $fh;

	/**
	 * Raw key.
	 *
	 * @var string
	 */
	private $key;

	/**
	 * Persistent state: file, prefix, index, committed.
	 *
	 * @var array
	 */
	private $state;

	/**
	 * Plaintext not sealed yet.
	 *
	 * @var string
	 */
	private $buf = '';

	/**
	 * Starts a new package file.
	 *
	 * @param string $file Path of the package.
	 * @return array The writer state to keep between requests.
	 * @throws RuntimeException When the file cannot be written.
	 */
	public static function create( $file ) {
		$prefix = random_bytes( 16 );
		$header = WP2P_Crypto::header( $prefix );
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_file_put_contents -- streamed binary output, not a WP_Filesystem job.
		if ( false === file_put_contents( $file, $header ) ) {
			throw new RuntimeException( 'The package file cannot be written.' );
		}
		return array(
			'file'      => $file,
			'prefix'    => base64_encode( $prefix ), // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions.obfuscation_base64_encode -- binary nonce prefix in an option.
			'index'     => 0,
			'committed' => strlen( $header ),
			'closed'    => false,
		);
	}

	/**
	 * Opens the package to go on writing.
	 *
	 * @param array  $state State from create() or commit().
	 * @param string $key   Raw key.
	 * @throws RuntimeException When the file cannot be opened.
	 */
	public function __construct( array $state, $key ) {
		$this->state = $state;
		$this->key   = $key;
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fopen -- streamed binary output.
		$this->fh = fopen( $state['file'], 'c+b' );
		if ( ! $this->fh ) {
			throw new RuntimeException( 'The package file cannot be opened.' );
		}
		ftruncate( $this->fh, (int) $state['committed'] );
		fseek( $this->fh, 0, SEEK_END );
	}

	/**
	 * Adds an entry from a string.
	 *
	 * @param string $name Entry name (ASCII, at most 100 bytes).
	 * @param string $data Contents.
	 */
	public function add( $name, $data ) {
		$this->write( self::tar_header( $name, strlen( $data ) ) );
		$this->write( $data );
		$this->pad( strlen( $data ) );
	}

	/**
	 * Adds an entry from a file on disk, streamed.
	 *
	 * @param string $name Entry name.
	 * @param string $path File to copy.
	 * @return bool False when the file could not be read.
	 */
	public function add_file( $name, $path ) {
		$size = filesize( $path );
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fopen -- streamed binary input.
		$in = ( false === $size ) ? false : fopen( $path, 'rb' );
		if ( ! $in ) {
			return false;
		}
		$this->write( self::tar_header( $name, $size ) );
		$left = $size;
		while ( $left > 0 && ! feof( $in ) ) {
			// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fread -- streamed binary input.
			$data = fread( $in, (int) min( self::CHUNK, $left ) );
			if ( false === $data || '' === $data ) {
				break;
			}
			$left -= strlen( $data );
			$this->write( $data );
		}
		fclose( $in ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
		if ( $left > 0 ) {
			// The file shrank while it was read: fill up, the entry's size is in its header already.
			$this->write( str_repeat( "\0", (int) $left ) );
		}
		$this->pad( $size );
		return true;
	}

	/**
	 * Seals what is buffered and records the complete size.
	 *
	 * @return array The state to keep.
	 */
	public function commit() {
		if ( '' !== $this->buf ) {
			$this->seal( $this->buf, false );
			$this->buf = '';
		}
		fflush( $this->fh );
		$this->state['committed'] = ftell( $this->fh );
		return $this->state;
	}

	/**
	 * Ends the archive and seals the last chunk.
	 *
	 * @return array The final state.
	 */
	public function finish() {
		$this->write( str_repeat( "\0", 1024 ) ); // The tar end: two empty blocks.
		$this->seal( $this->buf, true );
		$this->buf = '';
		fflush( $this->fh );
		$this->state['committed'] = ftell( $this->fh );
		$this->state['closed']    = true;
		fclose( $this->fh ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
		return $this->state;
	}

	/**
	 * Closes the file without committing (the next open() cuts back to the last commit).
	 */
	public function close() {
		if ( is_resource( $this->fh ) ) {
			fclose( $this->fh ); // phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fclose
		}
	}

	/**
	 * Buffers plaintext, sealing full chunks.
	 *
	 * @param string $data Plaintext.
	 */
	private function write( $data ) {
		$this->buf .= $data;
		$len        = strlen( $this->buf );
		while ( $len >= self::CHUNK ) {
			$this->seal( substr( $this->buf, 0, self::CHUNK ), false );
			$this->buf = (string) substr( $this->buf, self::CHUNK );
			$len      -= self::CHUNK;
		}
	}

	/**
	 * Pads an entry to the 512-byte block size.
	 *
	 * @param int $size Entry size.
	 */
	private function pad( $size ) {
		$rest = $size % 512;
		if ( $rest ) {
			$this->write( str_repeat( "\0", 512 - $rest ) );
		}
	}

	/**
	 * Seals and writes one chunk.
	 *
	 * @param string $plain Plaintext.
	 * @param bool   $last  Whether it is the last chunk.
	 * @throws RuntimeException When the disk is full.
	 */
	private function seal( $plain, $last ) {
		$prefix = base64_decode( $this->state['prefix'] ); // phpcs:ignore WordPress.PHP.DiscouragedPHPFunctions.obfuscation_base64_decode -- binary nonce prefix.
		$sealed = WP2P_Crypto::seal( $plain, $this->key, $prefix, (int) $this->state['index'], $last );
		// phpcs:ignore WordPress.WP.AlternativeFunctions.file_system_operations_fwrite -- streamed binary output.
		if ( false === fwrite( $this->fh, $sealed ) ) {
			throw new RuntimeException( 'Writing the package failed (is the disk full?).' );
		}
		++$this->state['index'];
	}

	/**
	 * A ustar header block.
	 *
	 * @param string $name Entry name.
	 * @param int    $size Entry size in bytes.
	 * @return string
	 */
	public static function tar_header( $name, $size ) {
		$h   = str_pad( substr( $name, 0, 100 ), 100, "\0" );
		$h  .= sprintf( "%07o\0%07o\0%07o\0", 0600, 0, 0 );
		$h  .= str_pad( decoct( $size ), 11, '0', STR_PAD_LEFT ) . "\0";
		$h  .= str_pad( decoct( time() ), 11, '0', STR_PAD_LEFT ) . "\0";
		$h  .= '        '; // Checksum, counted as spaces.
		$h  .= '0';
		$h  .= str_repeat( "\0", 100 );
		$h  .= "ustar\0" . '00';
		$h   = str_pad( $h, 512, "\0" );
		$sum = 0;
		for ( $i = 0; $i < 512; $i++ ) {
			$sum += ord( $h[ $i ] );
		}
		return substr_replace( $h, str_pad( decoct( $sum ), 6, '0', STR_PAD_LEFT ) . "\0 ", 148, 8 );
	}
}
