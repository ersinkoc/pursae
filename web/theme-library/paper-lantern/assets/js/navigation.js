/**
 * Paper Lantern: the small-screen menu toggle and keyboard-friendly submenus.
 */
( function() {
	'use strict';
	var nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' );
	if ( ! button ) {
		return;
	}
	function setOpen( open ) {
		nav.classList.toggle( 'toggled', open );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
	}
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
	// Submenus stay open while focus is inside them (desktop dropdowns).
	nav.querySelectorAll( '.menu-item-has-children' ).forEach( function( item ) {
		item.addEventListener( 'focusin', function() { item.classList.add( 'focus' ); } );
		item.addEventListener( 'focusout', function( e ) {
			if ( ! item.contains( e.relatedTarget ) ) {
				item.classList.remove( 'focus' );
			}
		} );
	} );
}() );
