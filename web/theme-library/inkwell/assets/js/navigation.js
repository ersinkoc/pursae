/**
 * Inkwell: the small-screen menu toggle and keyboard-friendly submenus.
 */
( function() {
	'use strict';
	var nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' );
	if ( ! button ) {
		return;
	}
	function close() {
		nav.classList.remove( 'toggled' );
		button.setAttribute( 'aria-expanded', 'false' );
	}
	button.addEventListener( 'click', function() {
		var open = nav.classList.toggle( 'toggled' );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
	} );
	document.addEventListener( 'keydown', function( e ) {
		if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
			close();
			button.focus();
		}
	} );
	// Parent items announce their submenu; focus inside keeps it open (CSS :focus-within).
	nav.querySelectorAll( '.menu-item-has-children > a' ).forEach( function( link ) {
		link.setAttribute( 'aria-haspopup', 'true' );
	} );
}() );
