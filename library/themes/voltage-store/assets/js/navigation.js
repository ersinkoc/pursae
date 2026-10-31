/**
 * Voltage: the department menu on small screens, the search toggle (minimal header and phones) and
 * the mini-cart dropdown. Each toggles with its button, closes with Escape or a click outside, and keeps
 * aria-expanded in sync.
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
	var search = document.getElementById( 'vt-search' );
	toggler( document.querySelector( '.vt-search-toggle' ), search, {
		className: 'is-open',
		onOpen: function() {
			var field = search.querySelector( 'input[type="search"]' );
			if ( field ) {
				field.focus();
			}
		}
	} );
	toggler( document.querySelector( '.vt-cart-toggle' ), document.getElementById( 'vt-mini-cart' ) );
	// "/" focuses the product search, like most catalog sites.
	document.addEventListener( 'keydown', function( e ) {
		var t = e.target;
		if ( '/' !== e.key || e.metaKey || e.ctrlKey || ( t && ( /input|textarea|select/i.test( t.tagName ) || t.isContentEditable ) ) ) {
			return;
		}
		var field = search && search.querySelector( 'input[type="search"]' );
		if ( field && field.offsetParent !== null ) {
			e.preventDefault();
			field.focus();
		}
	} );
}() );
