/**
 * Dog-Ear Books: the small-screen sections menu, the search panel and the mini-basket. Each toggles with its
 * button, closes with Escape or a click outside, and keeps aria-expanded in sync.
 */
( function() {
	'use strict';
	function toggler( button, panel, options ) {
		if ( ! button || ! panel ) {
			return;
		}
		options = options || {};
		function set( open ) {
			if ( options.className ) {
				panel.classList.toggle( options.className, open );
			} else {
				panel.hidden = ! open;
			}
			button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
			if ( open && options.onOpen ) {
				options.onOpen();
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
	toggler( nav && nav.querySelector( '.menu-toggle' ), nav, { className: 'toggled' } );
	var search = document.getElementById( 'de-search' );
	toggler( document.querySelector( '.de-search-toggle' ), search, {
		onOpen: function() {
			var field = search.querySelector( 'input[type="search"]' );
			if ( field ) {
				field.focus();
			}
		}
	} );
	// The basket link opens the mini-cart on wide screens; on small screens it follows the link to the cart.
	var cartLink = document.querySelector( '.de-cart-toggle' ),
		mini = document.getElementById( 'de-mini-cart' );
	if ( cartLink && mini && window.matchMedia( '(min-width: 861px)' ).matches ) {
		toggler( cartLink, mini );
	}
}() );
