/**
 * Numera: the mobile navigation toggle (Escape closes it, focus returns to the button) and an
 * is-scrolled class on the body for the sticky header.
 */
( function() {
	'use strict';
	var nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' ),
		body = document.body;

	function setOpen( open ) {
		nav.classList.toggle( 'toggled', open );
		body.classList.toggle( 'menu-open', open );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
	}

	if ( button ) {
		button.addEventListener( 'click', function() {
			setOpen( ! nav.classList.contains( 'toggled' ) );
		} );
		document.addEventListener( 'keydown', function( e ) {
			if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
				setOpen( false );
				button.focus();
			}
		} );
		document.addEventListener( 'click', function( e ) {
			if ( nav.classList.contains( 'toggled' ) && ! nav.contains( e.target ) ) {
				setOpen( false );
			}
		} );
		nav.addEventListener( 'click', function( e ) {
			var link = e.target.closest && e.target.closest( 'a' );
			if ( link && link.hash && link.pathname === location.pathname ) {
				setOpen( false );
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
