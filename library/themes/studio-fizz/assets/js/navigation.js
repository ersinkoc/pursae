/**
 * Small-screen menu toggle: opens and closes the primary menu, closes it with Escape or a click
 * outside, and keeps aria-expanded in sync.
 */
( function() {
	'use strict';
	var nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' );
	if ( ! button ) {
		return;
	}
	function set( open ) {
		nav.classList.toggle( 'toggled', open );
		document.body.classList.toggle( 'menu-open', open );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
	}
	button.addEventListener( 'click', function() {
		set( ! nav.classList.contains( 'toggled' ) );
	} );
	document.addEventListener( 'keydown', function( e ) {
		if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
			set( false );
			button.focus();
		}
	} );
	document.addEventListener( 'click', function( e ) {
		if ( nav.classList.contains( 'toggled' ) && ! nav.contains( e.target ) ) {
			set( false );
		}
	} );
}() );
