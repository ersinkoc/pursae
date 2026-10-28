/**
 * Mobile navigation: toggle, Escape to close, closes on resize to desktop.
 * Also: a scrolled-state class for the sticky header and print buttons
 * (any link to "#print" prints the page).
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
		window.addEventListener( 'resize', function() {
			if ( window.innerWidth > 900 && nav.classList.contains( 'toggled' ) ) {
				setOpen( false );
			}
		} );
	}

	function onScroll() {
		body.classList.toggle( 'is-scrolled', window.scrollY > 12 );
	}
	window.addEventListener( 'scroll', onScroll, { passive: true } );
	onScroll();

	document.addEventListener( 'click', function( e ) {
		var link = e.target.closest && e.target.closest( 'a[href="#print"]' );
		if ( link ) {
			e.preventDefault();
			window.print();
		}
	} );
}() );
