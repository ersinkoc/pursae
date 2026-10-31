/**
 * Datafield: mobile navigation toggle, submenu toggles and a scrolled-state class for the header.
 */
( function() {
	'use strict';
	var nav = document.getElementById( 'site-navigation' ),
		button = nav && nav.querySelector( '.menu-toggle' ),
		body = document.body,
		mq = window.matchMedia( '(max-width: 860px)' );

	function setOpen( open ) {
		nav.classList.toggle( 'toggled', open );
		body.classList.toggle( 'menu-open', open );
		button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
	}

	if ( button ) {
		button.addEventListener( 'click', function() {
			setOpen( ! nav.classList.contains( 'toggled' ) );
		} );
		document.addEventListener( 'keydown', function( e ) {
			if ( 'Escape' === e.key && nav.classList.contains( 'toggled' ) ) {
				setOpen( false );
				button.focus();
			}
		} );
		nav.addEventListener( 'click', function( e ) {
			var link = e.target.closest && e.target.closest( 'a' );
			if ( link && link.hash && link.pathname === location.pathname ) {
				setOpen( false );
			}
		} );
		var onChange = function() {
			if ( ! mq.matches && nav.classList.contains( 'toggled' ) ) {
				setOpen( false );
			}
		};
		if ( mq.addEventListener ) {
			mq.addEventListener( 'change', onChange );
		}

		// Submenus: a toggle button after each parent link (always open on desktop hover/focus).
		Array.prototype.forEach.call( nav.querySelectorAll( '.menu-item-has-children > a' ), function( link, i ) {
			var sub = link.parentNode.querySelector( '.sub-menu' ),
				t = document.createElement( 'button' );
			if ( ! sub ) {
				return;
			}
			sub.id = sub.id || 'sub-menu-' + i;
			t.type = 'button';
			t.className = 'submenu-toggle';
			t.setAttribute( 'aria-expanded', 'false' );
			t.setAttribute( 'aria-controls', sub.id );
			t.innerHTML = '<span class="screen-reader-text">Show submenu for ' + link.textContent + '</span>';
			link.parentNode.insertBefore( t, sub );
			t.addEventListener( 'click', function() {
				var open = t.getAttribute( 'aria-expanded' ) !== 'true';
				t.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
				link.parentNode.classList.toggle( 'submenu-open', open );
			} );
		} );
	}

	function onScroll() {
		body.classList.toggle( 'is-scrolled', window.scrollY > 8 );
	}
	window.addEventListener( 'scroll', onScroll, { passive: true } );
	onScroll();
}() );
