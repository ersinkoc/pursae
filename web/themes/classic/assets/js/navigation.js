/**
 * Classic: the mobile menu toggle and the optional dark mode toggle.
 *
 * Loaded with defer; no dependencies.
 */
( function() {
	'use strict';

	var root = document.documentElement,
		nav = document.getElementById( 'site-navigation' ),
		toggle = nav && nav.querySelector( '.menu-toggle' ),
		wide = window.matchMedia( '(min-width: 48rem)' );

	if ( toggle ) {
		var setOpen = function( open, focusToggle ) {
			toggle.setAttribute( 'aria-expanded', String( open ) );
			nav.classList.toggle( 'is-open', open );
			if ( ! open && focusToggle ) {
				toggle.focus();
			}
		};
		toggle.addEventListener( 'click', function() {
			setOpen( 'true' !== toggle.getAttribute( 'aria-expanded' ), false );
		} );
		document.addEventListener( 'keydown', function( e ) {
			if ( 'Escape' === e.key && nav.classList.contains( 'is-open' ) ) {
				setOpen( false, true );
			}
		} );
		document.addEventListener( 'click', function( e ) {
			if ( nav.classList.contains( 'is-open' ) && ! nav.contains( e.target ) ) {
				setOpen( false, false );
			}
		} );
		// Close when focus leaves the open menu (keyboard users tabbing past it).
		nav.addEventListener( 'focusout', function( e ) {
			if ( ! wide.matches && nav.classList.contains( 'is-open' ) && e.relatedTarget && ! nav.contains( e.relatedTarget ) ) {
				setOpen( false, false );
			}
		} );
		wide.addEventListener( 'change', function( m ) {
			if ( m.matches ) {
				setOpen( false, false );
			}
		} );
	}

	// Dark mode toggle: overrides the site's scheme for this visitor (stored in localStorage).
	var systemDark = window.matchMedia( '(prefers-color-scheme: dark)' ),
		buttons = document.querySelectorAll( '.scheme-toggle' );
	function isDark() {
		var scheme = root.getAttribute( 'data-color-scheme' );
		return 'dark' === scheme || ( 'auto' === scheme && systemDark.matches );
	}
	function sync() {
		buttons.forEach( function( b ) {
			b.setAttribute( 'aria-pressed', String( isDark() ) );
		} );
	}
	buttons.forEach( function( b ) {
		b.addEventListener( 'click', function() {
			var next = isDark() ? 'light' : 'dark';
			root.setAttribute( 'data-color-scheme', next );
			try {
				window.localStorage.setItem( 'classic-color-scheme', next );
			} catch ( e ) {}
			sync();
		} );
	} );
	if ( buttons.length ) {
		systemDark.addEventListener( 'change', sync );
		sync();
	}
}() );
