/**
 * Quiet Hours: applies the reader's stored light/dark choice before the page paints.
 * Without a stored choice the body class from Customize (qh-scheme-auto|light|dark) decides.
 */
( function() {
	'use strict';
	try {
		var stored = window.localStorage.getItem( 'quiet-hours-scheme' );
		if ( 'light' === stored || 'dark' === stored ) {
			document.documentElement.setAttribute( 'data-scheme', stored );
		}
	} catch ( e ) {}
}() );
