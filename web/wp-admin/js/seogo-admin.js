/* Pursae SEO admin screens: image pickers (Media Library), variable menus and score bullets. */
( function () {
	'use strict';

	function rating( n ) {
		n = parseInt( n, 10 );
		if ( isNaN( n ) ) { return 'na'; }
		return n >= 71 ? 'good' : n >= 41 ? 'ok' : 'bad';
	}

	window.seogoBindImagePickers = function ( root ) {
		( root || document ).querySelectorAll( '.seogo-pick-image' ).forEach( function ( btn ) {
			if ( btn.dataset.bound ) { return; }
			btn.dataset.bound = '1';
			btn.addEventListener( 'click', function () {
				var input = btn.parentNode.querySelector( 'input' );
				if ( ! window.wp || ! wp.media ) {
					input.focus();
					return;
				}
				var frame = wp.media( { title: 'Select image', button: { text: 'Use this image' }, library: { type: 'image' }, multiple: false } );
				frame.on( 'select', function () {
					var a = frame.state().get( 'selection' ).first().toJSON();
					input.value = a.url;
					input.dispatchEvent( new Event( 'input', { bubbles: true } ) );
				} );
				frame.open();
			} );
		} );
	};

	window.seogoBindVarMenus = function ( root ) {
		( root || document ).querySelectorAll( '.seogo-vars' ).forEach( function ( wrap ) {
			if ( wrap.dataset.bound ) { return; }
			wrap.dataset.bound = '1';
			var toggle = wrap.querySelector( '.seogo-vars-toggle' );
			var menu = wrap.querySelector( '.seogo-vars-menu' );
			var field = wrap.parentNode.querySelector( 'input, textarea' );
			toggle.addEventListener( 'click', function () {
				var open = menu.hidden;
				menu.hidden = ! open;
				toggle.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
			} );
			menu.addEventListener( 'click', function ( e ) {
				var b = e.target.closest( '.seogo-var' );
				if ( ! b ) { return; }
				var v = b.dataset.var;
				var start = field.selectionStart || field.value.length, end = field.selectionEnd || field.value.length;
				var before = field.value.slice( 0, start ), after = field.value.slice( end );
				var pad = before && ! /\s$/.test( before ) ? ' ' : '';
				field.value = before + pad + v + ( after && ! /^\s/.test( after ) ? ' ' : '' ) + after;
				field.dispatchEvent( new Event( 'input', { bubbles: true } ) );
				menu.hidden = true;
				toggle.setAttribute( 'aria-expanded', 'false' );
				field.focus();
			} );
			document.addEventListener( 'click', function ( e ) {
				if ( ! wrap.contains( e.target ) ) { menu.hidden = true; toggle.setAttribute( 'aria-expanded', 'false' ); }
			} );
		} );
	};

	function init() {
		window.seogoBindImagePickers();
		window.seogoBindVarMenus();
		document.querySelectorAll( '.seogo-score-num' ).forEach( function ( el ) {
			el.querySelector( '.seogo-bullet' ).classList.add( rating( el.dataset.score ) );
		} );
	}
	if ( document.readyState === 'loading' ) {
		document.addEventListener( 'DOMContentLoaded', init );
	} else {
		init();
	}
}() );
