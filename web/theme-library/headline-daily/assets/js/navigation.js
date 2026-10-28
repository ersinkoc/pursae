/**
 * Headline Daily: the small-screen sections menu.
 */
( function() {
	'use strict';
	var nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' );
	if ( ! button ) {
		return;
	}
	function close( focus ) {
		nav.classList.remove( 'toggled' );
		button.setAttribute( 'aria-expanded', 'false' );
		if ( focus ) {
			button.focus();
		}
	}
	button.addEventListener( 'click', function() {
		var open = nav.classList.toggle( 'toggled' );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
	} );
	document.addEventListener( 'keydown', function( e ) {
		if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
			close( true );
		}
	} );
	window.matchMedia( '(min-width: 960px)' ).addEventListener( 'change', function( mq ) {
		if ( mq.matches ) {
			close( false );
		}
	} );
	// Ticker: pause while the reader hovers or tabs through it.
	var ticker = document.querySelector( '.hd-ticker__viewport' );
	if ( ticker ) {
		ticker.addEventListener( 'focusin', function() { ticker.classList.add( 'is-paused' ); } );
		ticker.addEventListener( 'focusout', function() { ticker.classList.remove( 'is-paused' ); } );
	}
}() );
