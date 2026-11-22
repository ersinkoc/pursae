<?php
/**
 * Cuts one export_wp() batch down to its part of the package's single WXR document.
 *
 * @package WP_To_Pursae
 */

defined( 'ABSPATH' ) || exit;

/**
 * An output buffer handler for export_wp(): it passes the output on in pieces, without the closing
 * </channel></rss> and, unless the head is kept, without everything before the first <item>.
 */
class WP2P_Content_Filter {

	const ITEM = "\t<item>\n\t\t<title>"; // How export_wp() starts every item.
	const HOLD = 4096; // Bytes held back so the closing tags can be cut off at the end.

	/**
	 * Receives each piece of output.
	 *
	 * @var callable
	 */
	private $emit;

	/**
	 * Whether the head is passed on (or skipped up to the first item).
	 *
	 * @var bool
	 */
	private $head;

	/**
	 * Whether the start was found.
	 *
	 * @var bool
	 */
	private $started;

	/**
	 * Output held back.
	 *
	 * @var string
	 */
	private $pending = '';

	/**
	 * Bytes passed on.
	 *
	 * @var int
	 */
	private $written = 0;

	/**
	 * Sets the filter up.
	 *
	 * @param callable $emit Receives each piece of output.
	 * @param bool     $head Whether to keep the document head.
	 */
	public function __construct( $emit, $head ) {
		$this->emit    = $emit;
		$this->head    = (bool) $head;
		$this->started = (bool) $head;
	}

	/**
	 * The output buffer callback.
	 *
	 * @param string $data Output.
	 * @return string Nothing: the output goes to the package.
	 */
	public function chunk( $data ) {
		$this->pending .= (string) $data;
		if ( ! $this->started ) {
			$p = strpos( $this->pending, self::ITEM );
			if ( false === $p ) {
				// Keep what could be the start of a marker cut in two.
				$this->pending = (string) substr( $this->pending, -( strlen( self::ITEM ) - 1 ) );
				return '';
			}
			$this->pending = (string) substr( $this->pending, $p );
			$this->started = true;
		}
		if ( strlen( $this->pending ) > self::HOLD ) {
			$this->send( (string) substr( $this->pending, 0, -self::HOLD ) );
			$this->pending = (string) substr( $this->pending, -self::HOLD );
		}
		return '';
	}

	/**
	 * Passes on the rest, without the closing tags.
	 *
	 * @return int Bytes passed on in all.
	 */
	public function finish() {
		if ( $this->started ) {
			$p = strrpos( $this->pending, '</channel>' );
			if ( false !== $p ) {
				$this->pending = (string) substr( $this->pending, 0, $p );
			}
			$this->send( $this->pending );
		}
		$this->pending = '';
		return $this->written;
	}

	/**
	 * Passes a piece on.
	 *
	 * @param string $data Output.
	 */
	private function send( $data ) {
		if ( '' === $data ) {
			return;
		}
		call_user_func( $this->emit, $data );
		$this->written += strlen( $data );
	}
}
