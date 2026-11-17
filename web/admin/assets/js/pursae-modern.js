/*
 * Pursae Modern: the command palette (⌘K / Ctrl+K, or the search box in the top bar).
 * It lists every screen of the admin menu as the server rendered it for this user (so only screens
 * the user can open, plugin screens included) plus a few quick actions, and jumps to the choice.
 * Labels come from the hidden .pm-cmd-labels spans, which the server translates like any text.
 */
( function () {
	'use strict';
	var dlg = document.getElementById( 'pm-cmd' );
	var input = document.getElementById( 'pm-cmd-input' );
	var list = document.getElementById( 'pm-cmd-list' );
	if ( ! dlg || ! input || ! list ) {
		return;
	}
	var labels = {};
	dlg.querySelectorAll( '.pm-cmd-labels [data-k]' ).forEach( function ( el ) {
		labels[ el.getAttribute( 'data-k' ) ] = el.textContent;
	} );
	var clean = function ( el ) {
		var c = el.cloneNode( true );
		c.querySelectorAll( '.awaiting-mod, .update-plugins, .screen-reader-text' ).forEach( function ( x ) { x.remove(); } );
		return c.textContent.replace( /\s+/g, ' ' ).trim();
	};

	var screens = [];
	document.querySelectorAll( '#adminmenu > li.menu-top' ).forEach( function ( li ) {
		var top = li.querySelector( 'a.menu-top' );
		if ( ! top ) {
			return;
		}
		var name = li.querySelector( '.wp-menu-name' );
		var parent = clean( name || top );
		var icon = li.querySelector( '.wp-menu-image' );
		var iconClass = icon ? ( icon.className.match( /dashicons-[a-z0-9-]+/ ) || [ '' ] )[ 0 ] : '';
		var subs = li.querySelectorAll( '.wp-submenu li:not(.wp-submenu-head) > a' );
		if ( ! subs.length ) {
			screens.push( { label: parent, hint: '', href: top.getAttribute( 'href' ), icon: iconClass } );
			return;
		}
		subs.forEach( function ( a ) {
			var sub = clean( a );
			screens.push( { label: sub === parent ? parent : parent + ' › ' + sub, hint: '', href: a.getAttribute( 'href' ), icon: iconClass } );
		} );
	} );

	var actions = [];
	var addAction = function ( key, href, icon ) {
		if ( href && labels[ key ] ) {
			actions.push( { label: labels[ key ], href: href, icon: icon } );
		}
	};
	var link = function ( sel ) {
		var a = document.querySelector( sel );
		return a ? a.getAttribute( 'href' ) : '';
	};
	addAction( 'new-post', link( '#wp-admin-bar-new-post a' ), 'dashicons-edit' );
	addAction( 'new-page', link( '#wp-admin-bar-new-page a' ), 'dashicons-admin-page' );
	if ( document.querySelector( '#adminmenu a[href*="post_type=product"]' ) ) {
		addAction( 'new-product', '/wp-admin/post-new?post_type=product', 'dashicons-products' );
	}
	addAction( 'visit', link( '#wp-admin-bar-view-site a' ) || link( '#wp-admin-bar-site-name > a' ), 'dashicons-external' );
	addAction( 'profile', '/wp-admin/profile', 'dashicons-admin-users' );

	var sel = 0, shown = [];
	var norm = function ( s ) {
		return s.toLocaleLowerCase( document.documentElement.lang || undefined ).normalize( 'NFD' ).replace( /[̀-ͯ]/g, '' );
	};
	var esc = function ( s ) {
		var d = document.createElement( 'span' );
		d.textContent = s;
		return d.innerHTML;
	};
	function render() {
		var q = norm( input.value.trim() );
		var match = function ( x ) { return ! q || norm( x.label ).indexOf( q ) !== -1; };
		var acts = actions.filter( match );
		var scr = screens.filter( match ).slice( 0, q ? 60 : 12 );
		shown = acts.concat( scr );
		if ( sel >= shown.length ) {
			sel = Math.max( 0, shown.length - 1 );
		}
		var i = 0, html = '';
		var row = function ( x ) {
			var k = i++;
			return '<a class="pm-cmd-it' + ( k === sel ? ' is-sel' : '' ) + '" role="option" aria-selected="' + ( k === sel ) + '" data-k="' + k + '" href="' + esc( x.href ) + '">' +
				'<span class="dashicons ' + esc( x.icon || 'dashicons-admin-generic' ) + '" aria-hidden="true"></span><span>' + esc( x.label ) + '</span></a>';
		};
		if ( acts.length ) {
			html += '<div class="pm-cmd-sec">' + esc( labels.actions || '' ) + '</div>' + acts.map( row ).join( '' );
		}
		if ( scr.length ) {
			html += '<div class="pm-cmd-sec">' + esc( labels.screens || '' ) + '</div>' + scr.map( row ).join( '' );
		}
		list.innerHTML = html || '<p class="pm-cmd-empty">' + esc( labels.none || '' ) + '</p>';
		var cur = list.querySelector( '.is-sel' );
		if ( cur ) {
			cur.scrollIntoView( { block: 'nearest' } );
		}
	}
	function open() {
		dlg.hidden = false;
		document.body.classList.add( 'pm-cmd-open' );
		input.value = '';
		sel = 0;
		render();
		input.focus();
	}
	function close() {
		dlg.hidden = true;
		document.body.classList.remove( 'pm-cmd-open' );
	}
	document.addEventListener( 'keydown', function ( e ) {
		if ( ( e.metaKey || e.ctrlKey ) && ( e.key === 'k' || e.key === 'K' ) ) {
			e.preventDefault();
			if ( dlg.hidden ) {
				open();
			} else {
				close();
			}
			return;
		}
		if ( dlg.hidden ) {
			return;
		}
		if ( e.key === 'Escape' ) {
			close();
		} else if ( e.key === 'ArrowDown' ) {
			e.preventDefault();
			sel = Math.min( shown.length - 1, sel + 1 );
			render();
		} else if ( e.key === 'ArrowUp' ) {
			e.preventDefault();
			sel = Math.max( 0, sel - 1 );
			render();
		} else if ( e.key === 'Enter' && shown[ sel ] ) {
			e.preventDefault();
			window.location.href = shown[ sel ].href;
		}
	} );
	input.addEventListener( 'input', function () {
		sel = 0;
		render();
	} );
	dlg.addEventListener( 'click', function ( e ) {
		if ( e.target === dlg ) {
			close();
		}
	} );
	document.querySelectorAll( '.pm-search-trigger' ).forEach( function ( b ) {
		b.addEventListener( 'click', open );
	} );
} )();

/* Row actions are icons in Pursae Modern: give each one its label as a tooltip and an accessible name. */
( function () {
	'use strict';
	var label = function ( root ) {
		( root || document ).querySelectorAll( '.wp-list-table:not(.plugins) .row-actions > span > a, .wp-list-table:not(.plugins) .row-actions > span > button' ).forEach( function ( el ) {
			if ( el.hasAttribute( 'data-pm-tip' ) ) {
				return;
			}
			var t = el.textContent.replace( /\s+/g, ' ' ).trim();
			if ( t ) {
				el.setAttribute( 'data-pm-tip', t );
				if ( ! el.hasAttribute( 'aria-label' ) ) {
					el.setAttribute( 'aria-label', t );
				}
			}
		} );
	};
	label();
	// Quick Edit and ajax row updates replace rows: label the new ones too.
	var tbody = document.querySelectorAll( '.wp-list-table tbody' );
	if ( window.MutationObserver ) {
		tbody.forEach( function ( tb ) {
			new MutationObserver( function () { label( tb ); } ).observe( tb, { childList: true, subtree: true } );
		} );
	}
} )();
