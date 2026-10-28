/**
 * Byte Notes: applies the reader's stored color scheme (or the Customizer default) before paint.
 */
( function() {
	'use strict';
	var root = document.documentElement, scheme = null;
	try {
		scheme = window.localStorage.getItem( 'byte-notes-scheme' );
	} catch ( e ) {}
	if ( 'light' !== scheme && 'dark' !== scheme ) {
		scheme = ( window.getComputedStyle( root ).getPropertyValue( '--bn-scheme' ) || 'dark' ).trim();
		if ( 'system' === scheme ) {
			scheme = window.matchMedia && window.matchMedia( '(prefers-color-scheme: light)' ).matches ? 'light' : 'dark';
		}
		if ( 'light' !== scheme ) {
			scheme = 'dark';
		}
	}
	root.setAttribute( 'data-scheme', scheme );
}() );
