/**
 * Oak & Form: the menu drawer on small screens, sub-menu toggles, the search panel and the mini-cart.
 * Every panel toggles with its button, closes with Escape or a click outside and keeps aria-expanded in sync.
 */
( function() {
	'use strict';
	var openPanels = [];
	function toggler( button, panel, opts ) {
		if ( ! button || ! panel ) {
			return null;
		}
		opts = opts || {};
		function isOpen() {
			return button.getAttribute( 'aria-expanded' ) === 'true';
		}
		function set( open ) {
			if ( opts.className ) {
				panel.classList.toggle( opts.className, open );
			} else {
				panel.hidden = ! open;
			}
			button.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
			if ( opts.bodyClass ) {
				document.body.classList.toggle( opts.bodyClass, open );
			}
			if ( open && opts.focus ) {
				var f = panel.querySelector( opts.focus );
				if ( f ) {
					f.focus( { preventScroll: true } );
				}
			}
			if ( opts.onChange ) {
				opts.onChange( open );
			}
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
		var api = { set: set, isOpen: isOpen };
		openPanels.push( api );
		return api;
	}

	// Primary menu: a drawer / dropdown below the breakpoint.
	var nav = document.getElementById( 'site-navigation' ),
		scrim = document.querySelector( '.of-scrim' );
	var menu = toggler( document.querySelector( '.of-menu-toggle' ), nav, {
		className: 'is-open',
		bodyClass: 'of-nav-open',
		focus: 'a',
		onChange: function( open ) {
			if ( scrim ) {
				scrim.hidden = ! open;
			}
		}
	} );
	if ( nav && menu ) {
		var close = nav.querySelector( '.of-drawer__close' );
		if ( close ) {
			close.addEventListener( 'click', function() {
				menu.set( false );
				document.querySelector( '.of-menu-toggle' ).focus();
			} );
		}
	}

	// Sub-menus: a toggle button after each parent link (hover and focus open them on wide screens).
	if ( nav ) {
		nav.querySelectorAll( '.menu-item-has-children > a' ).forEach( function( link, i ) {
			var sub = link.parentNode.querySelector( '.sub-menu' );
			if ( ! sub ) {
				return;
			}
			sub.id = sub.id || 'of-sub-' + i;
			var b = document.createElement( 'button' );
			b.type = 'button';
			b.className = 'of-sub-toggle';
			b.setAttribute( 'aria-expanded', 'false' );
			b.setAttribute( 'aria-controls', sub.id );
			b.innerHTML = '<span class="screen-reader-text">Show the ' + link.textContent.trim() + ' menu</span><svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2.5 4.5 6 8l3.5-3.5"/></svg>';
			link.insertAdjacentElement( 'afterend', b );
			b.addEventListener( 'click', function( e ) {
				e.stopPropagation();
				var open = b.getAttribute( 'aria-expanded' ) !== 'true';
				b.setAttribute( 'aria-expanded', open ? 'true' : 'false' );
				link.parentNode.classList.toggle( 'is-open', open );
			} );
			link.parentNode.addEventListener( 'keydown', function( e ) {
				if ( 'Escape' === e.key && link.parentNode.classList.contains( 'is-open' ) ) {
					e.stopPropagation();
					b.setAttribute( 'aria-expanded', 'false' );
					link.parentNode.classList.remove( 'is-open' );
					b.focus();
				}
			} );
		} );
	}

	// Search panel.
	var search = document.getElementById( 'of-search' );
	toggler( document.querySelector( '.of-search-toggle' ), search, { focus: 'input[type="search"]' } );

	// The cart opens the mini-cart on wide screens; on small screens it follows the link to the cart.
	var cartLink = document.querySelector( '.of-cart-toggle' ),
		mini = document.getElementById( 'of-mini-cart' );
	if ( cartLink && mini && window.matchMedia( '(min-width: 960px)' ).matches ) {
		toggler( cartLink, mini );
	}
}() );
