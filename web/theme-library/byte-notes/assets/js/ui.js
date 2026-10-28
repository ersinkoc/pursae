/**
 * Byte Notes: the small-screen menu, the header search panel and the light/dark toggle.
 */
( function() {
	'use strict';
	var root = document.documentElement;

	// Menu toggle.
	var nav = document.getElementById( 'site-navigation' ),
		menuButton = nav && nav.querySelector( '.menu-toggle' );
	function closeMenu( focus ) {
		nav.classList.remove( 'toggled' );
		menuButton.setAttribute( 'aria-expanded', 'false' );
		if ( focus ) {
			menuButton.focus();
		}
	}
	if ( menuButton ) {
		menuButton.addEventListener( 'click', function() {
			var open = nav.classList.toggle( 'toggled' );
			menuButton.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
		} );
	}

	// Search panel.
	var searchButton = document.querySelector( '.header-search' ),
		panel = document.getElementById( 'header-search-panel' );
	function closeSearch( focus ) {
		panel.hidden = true;
		searchButton.setAttribute( 'aria-expanded', 'false' );
		if ( focus ) {
			searchButton.focus();
		}
	}
	if ( searchButton && panel ) {
		searchButton.addEventListener( 'click', function() {
			var open = panel.hidden;
			panel.hidden = ! open;
			searchButton.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
			if ( open ) {
				panel.querySelector( 'input' ).focus();
			}
		} );
	}

	document.addEventListener( 'keydown', function( e ) {
		if ( 'Escape' !== e.key ) {
			return;
		}
		if ( nav && nav.classList.contains( 'toggled' ) ) {
			closeMenu( true );
		}
		if ( panel && ! panel.hidden ) {
			closeSearch( true );
		}
	} );

	// Light/dark toggle: the choice is stored per reader.
	var toggle = document.querySelector( '.scheme-toggle' );
	function sync() {
		var dark = 'light' !== root.getAttribute( 'data-scheme' );
		if ( toggle ) {
			toggle.setAttribute( 'aria-pressed', dark ? 'true' : 'false' );
		}
	}
	if ( toggle ) {
		sync();
		toggle.addEventListener( 'click', function() {
			var next = 'light' === root.getAttribute( 'data-scheme' ) ? 'dark' : 'light';
			root.setAttribute( 'data-scheme', next );
			try {
				window.localStorage.setItem( 'byte-notes-scheme', next );
			} catch ( e ) {}
			sync();
		} );
	}
}() );
