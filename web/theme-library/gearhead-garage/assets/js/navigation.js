/**
 * Mobile navigation toggle, Escape to close, and a scrolled-state class for sticky headers.
 */
( function() {
	'use strict';
	var nav = document.getElementById( 'site-navigation' ),
		button = document.querySelector( '.menu-toggle' ),
		body = document.body;

	function setOpen( open ) {
		nav.classList.toggle( 'toggled', open );
		body.classList.toggle( 'menu-open', open );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
	}

	if ( nav && button ) {
		button.addEventListener( 'click', function() {
			setOpen( ! nav.classList.contains( 'toggled' ) );
		} );
		document.addEventListener( 'keydown', function( e ) {
			if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
				setOpen( false );
				button.focus();
			}
		} );
		window.addEventListener( 'resize', function() {
			if ( window.innerWidth > 960 && nav.classList.contains( 'toggled' ) ) {
				setOpen( false );
			}
		} );
	}

	function onScroll() {
		body.classList.toggle( 'is-scrolled', window.scrollY > 8 );
	}
	window.addEventListener( 'scroll', onScroll, { passive: true } );
	onScroll();
}() );
