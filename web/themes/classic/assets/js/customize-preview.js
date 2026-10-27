/**
 * Classic: live preview for the Customizer settings with transport postMessage.
 *
 * The Customizer screen posts { wpgoCustomize: 'setting', id, value } to the preview frame; the
 * values are applied here without reloading. Colors set the --cl-*-light tokens on <html>, the other
 * options swap the body classes style.css keys on.
 */
( function() {
	'use strict';

	var root = document.documentElement,
		body = document.body,
		classes = {
			'mod:font_pairing': 'classic-fonts-',
			'mod:font_size': 'classic-size-',
			'mod:heading_weight': 'classic-heading-',
			'mod:line_height': 'classic-leading-',
			'mod:content_width': 'classic-width-',
			'mod:header_layout': 'classic-header-',
			'mod:color_scheme': 'classic-scheme-'
		},
		toggles = {
			'mod:sticky_header': [ 'classic-sticky-header', true ],
			'mod:show_title': [ 'classic-hide-title', false ],
			'mod:show_tagline': [ 'classic-hide-tagline', false ]
		};

	function rgb( hex ) {
		hex = String( hex || '' ).trim().replace( '#', '' );
		if ( 3 === hex.length ) {
			hex = hex.replace( /./g, '$&$&' );
		}
		var n = parseInt( hex, 16 );
		return 6 === hex.length && ! isNaN( n ) ? [ n >> 16 & 255, n >> 8 & 255, n & 255 ] : null;
	}
	function luminance( hex ) {
		var c = rgb( hex );
		if ( ! c ) {
			return 1;
		}
		c = c.map( function( v ) {
			v /= 255;
			return v <= 0.04045 ? v / 12.92 : Math.pow( ( v + 0.055 ) / 1.055, 2.4 );
		} );
		return 0.2126 * c[ 0 ] + 0.7152 * c[ 1 ] + 0.0722 * c[ 2 ];
	}
	function contrast( a, b ) {
		var x = luminance( a ), y = luminance( b );
		return ( Math.max( x, y ) + 0.05 ) / ( Math.min( x, y ) + 0.05 );
	}
	function onColor( bg ) {
		return contrast( '#ffffff', bg ) >= contrast( '#111111', bg ) ? '#ffffff' : '#111111';
	}
	function token( name ) {
		return getComputedStyle( root ).getPropertyValue( name ).trim();
	}
	function setVar( name, value ) {
		// "initial" makes the token invalid, so var() falls back to its default.
		root.style.setProperty( name, value || 'initial' );
	}
	function swap( prefix, value ) {
		Array.prototype.slice.call( body.classList ).forEach( function( c ) {
			if ( 0 === c.indexOf( prefix ) ) {
				body.classList.remove( c );
			}
		} );
		body.classList.add( prefix + value );
	}
	function headerText() {
		var bg = token( '--cl-header-bg-light' ) || token( '--cl-bg-light' ) || '#ffffff',
			text = token( '--cl-text-light' ) || '#1d1d1f';
		setVar( '--cl-header-text-light', contrast( text, bg ) < 4.5 ? onColor( bg ) : '' );
	}

	function apply( id, value ) {
		if ( classes[ id ] ) {
			swap( classes[ id ], value );
			if ( 'mod:color_scheme' === id ) {
				root.setAttribute( 'data-color-scheme', value );
			}
			return;
		}
		if ( toggles[ id ] ) {
			body.classList.toggle( toggles[ id ][ 0 ], ( '1' === value ) === toggles[ id ][ 1 ] );
			return;
		}
		switch ( id ) {
			case 'blogname':
				document.querySelectorAll( '.site-title a' ).forEach( function( a ) {
					a.textContent = value;
				} );
				break;
			case 'blogdescription':
				document.querySelectorAll( '.site-description' ).forEach( function( p ) {
					p.textContent = value;
				} );
				break;
			case 'mod:accent_color':
				setVar( '--cl-accent-light', value );
				setVar( '--cl-accent-text-light', value );
				setVar( '--cl-on-accent-light', value ? onColor( value ) : '' );
				break;
			case 'mod:bg_color':
				setVar( '--cl-bg-light', value );
				headerText();
				break;
			case 'mod:text_color':
				setVar( '--cl-text-light', value );
				headerText();
				break;
			case 'mod:link_color':
				setVar( '--cl-link-light', value );
				break;
			case 'mod:header_bg_color':
				setVar( '--cl-header-bg-light', value );
				headerText();
				break;
		}
	}

	window.addEventListener( 'message', function( e ) {
		if ( e.origin !== window.location.origin || ! e.data || 'setting' !== e.data.wpgoCustomize ) {
			return;
		}
		apply( e.data.id, e.data.value );
	} );
}() );
