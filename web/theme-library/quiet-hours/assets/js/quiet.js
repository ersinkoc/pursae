/**
 * Quiet Hours: the light/dark switch and the small-screen menu.
 */
( function() {
	'use strict';
	var root = document.documentElement,
		body = document.body,
		key = 'quiet-hours-scheme';

	// The scheme in effect: a stored choice, else the Customizer default, else the system.
	function current() {
		var s = root.getAttribute( 'data-scheme' );
		if ( s ) {
			return s;
		}
		if ( body.classList.contains( 'qh-scheme-dark' ) ) {
			return 'dark';
		}
		if ( body.classList.contains( 'qh-scheme-light' ) ) {
			return 'light';
		}
		return window.matchMedia && window.matchMedia( '(prefers-color-scheme: dark)' ).matches ? 'dark' : 'light';
	}

	var toggle = document.querySelector( '.scheme-toggle' );
	function sync() {
		if ( ! toggle ) {
			return;
		}
		var dark = 'dark' === current();
		toggle.setAttribute( 'aria-pressed', dark ? 'true' : 'false' );
		toggle.querySelector( '.scheme-toggle__label' ).textContent = dark ? 'light' : 'dark';
	}
	if ( toggle ) {
		toggle.addEventListener( 'click', function() {
			var next = 'dark' === current() ? 'light' : 'dark';
			root.setAttribute( 'data-scheme', next );
			try {
				window.localStorage.setItem( key, next );
			} catch ( e ) {}
			sync();
		} );
		if ( window.matchMedia ) {
			var mq = window.matchMedia( '(prefers-color-scheme: dark)' );
			if ( mq.addEventListener ) {
				mq.addEventListener( 'change', sync );
			}
		}
		// The Customizer preview swaps body classes live.
		if ( window.MutationObserver ) {
			new MutationObserver( sync ).observe( body, { attributes: true, attributeFilter: [ 'class' ] } );
		}
		sync();
	}

	var nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' );
	if ( ! button ) {
		return;
	}
	function close() {
		nav.classList.remove( 'toggled' );
		button.setAttribute( 'aria-expanded', 'false' );
		button.textContent = 'menu';
	}
	button.addEventListener( 'click', function() {
		var open = nav.classList.toggle( 'toggled' );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
		button.textContent = open ? 'close' : 'menu';
	} );
	document.addEventListener( 'keydown', function( e ) {
		if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
			close();
			button.focus();
		}
	} );
}() );
