/**
 * Green Pulse: small-screen menu toggle, header search and the sticky header state.
 */
( function() {
	'use strict';
	var body = document.body,
		nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' );

	function close() {
		nav.classList.remove( 'toggled' );
		body.classList.remove( 'menu-open' );
		button.setAttribute( 'aria-expanded', 'false' );
	}

	if ( button ) {
		button.addEventListener( 'click', function() {
			var open = nav.classList.toggle( 'toggled' );
			body.classList.toggle( 'menu-open', open );
			button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
		} );
		document.addEventListener( 'keydown', function( e ) {
			if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
				close();
				button.focus();
			}
		} );
		document.addEventListener( 'click', function( e ) {
			if ( nav.classList.contains( 'toggled' ) && ! nav.contains( e.target ) ) {
				close();
			}
		} );
		window.addEventListener( 'resize', function() {
			if ( window.innerWidth > 960 && nav.classList.contains( 'toggled' ) ) {
				close();
			}
		} );
	}

	// Header search: close on Escape or a click elsewhere, focus the field when opened.
	var search = document.querySelector( '.gp-search' );
	if ( search ) {
		search.addEventListener( 'toggle', function() {
			if ( search.open ) {
				var field = search.querySelector( 'input[type="search"]' );
				if ( field ) {
					field.focus();
				}
			}
		} );
		document.addEventListener( 'keydown', function( e ) {
			if ( 'Escape' === e.key && search.open ) {
				search.open = false;
				search.querySelector( 'summary' ).focus();
			}
		} );
		document.addEventListener( 'click', function( e ) {
			if ( search.open && ! search.contains( e.target ) ) {
				search.open = false;
			}
		} );
	}

	var onScroll = function() {
		body.classList.toggle( 'is-scrolled', window.scrollY > 24 );
	};
	window.addEventListener( 'scroll', onScroll, { passive: true } );
	onScroll();
}() );
