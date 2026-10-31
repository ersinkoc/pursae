/**
 * Threadline: the small-screen menu, the search panel and the mini-cart dropdown. Each toggles with
 * its button, closes with Escape or a click outside, and keeps aria-expanded in sync.
 */
( function() {
	'use strict';
	function toggler( button, panel, onOpen ) {
		if ( ! button || ! panel ) {
			return;
		}
		function set( open ) {
			if ( panel.hasAttribute( 'hidden' ) || panel.hidden !== undefined ) {
				if ( panel.id === 'site-navigation' ) {
					panel.classList.toggle( 'toggled', open );
				} else {
					panel.hidden = ! open;
				}
			}
			button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
			if ( open && onOpen ) {
				onOpen();
			}
		}
		function isOpen() {
			return button.getAttribute( 'aria-expanded' ) === 'true';
		}
		button.addEventListener( 'click', function( e ) {
			e.preventDefault();
			set( ! isOpen() );
		} );
		document.addEventListener( 'keydown', function( e ) {
			if ( 'Escape' === e.key && isOpen() ) {
				set( false );
				button.focus();
			}
		} );
		document.addEventListener( 'click', function( e ) {
			if ( isOpen() && ! panel.contains( e.target ) && ! button.contains( e.target ) ) {
				set( false );
			}
		} );
	}
	var nav = document.getElementById( 'site-navigation' );
	toggler( nav && nav.querySelector( '.menu-toggle' ), nav );
	var search = document.getElementById( 'tl-search' );
	toggler( document.querySelector( '.tl-search-toggle' ), search, function() {
		var field = search.querySelector( 'input[type="search"]' );
		if ( field ) {
			field.focus();
		}
	} );
	// The cart icon opens the mini-cart on wide screens; on small screens it follows the link.
	var cartLink = document.querySelector( '.tl-cart-toggle' ),
		mini = document.getElementById( 'tl-mini-cart' );
	if ( cartLink && mini && window.matchMedia( '(min-width: 861px)' ).matches ) {
		toggler( cartLink, mini );
	}
}() );
