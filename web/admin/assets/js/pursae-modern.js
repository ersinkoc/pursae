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

/*
 * Manual order of categories and tags (Pursae Modern): a grip to drag each row and arrows to move it
 * one place. A row moves with its child terms and only among its siblings; every move is saved at
 * once (admin-ajax action pursae-term-order), and the site lists the terms in that order.
 */
( function () {
	'use strict';
	var tbody = document.querySelector( 'tbody[data-pm-sort]' );
	if ( ! tbody ) {
		return;
	}
	var tax = tbody.getAttribute( 'data-pm-sort' );
	var nonce = tbody.getAttribute( 'data-pm-nonce' );
	var labels = {};
	var tpl = document.getElementById( 'pm-sort-labels' );
	if ( tpl ) {
		tpl.querySelectorAll( '[data-k]' ).forEach( function ( el ) {
			labels[ el.getAttribute( 'data-k' ) ] = el.textContent;
		} );
	}
	// the row markup is WordPress's own: id="tag-N", class "level-D", the parent in Quick Edit's hidden data
	tbody.querySelectorAll( 'tr[id^="tag-"]' ).forEach( function ( tr ) {
		var id = tr.id.slice( 4 );
		var lvl = ( tr.className.match( /\blevel-(\d+)/ ) || [ 0, '0' ] )[ 1 ];
		var par = tr.querySelector( '#inline_' + id + ' .parent' );
		tr.setAttribute( 'data-id', id );
		tr.setAttribute( 'data-depth', lvl );
		tr.setAttribute( 'data-parent', par ? par.textContent.trim() || '0' : '0' );
	} );
	var rows = function () { return Array.prototype.slice.call( tbody.querySelectorAll( 'tr[data-id]' ) ); };
	var depth = function ( tr ) { return parseInt( tr.getAttribute( 'data-depth' ) || '0', 10 ); };
	var parent = function ( tr ) { return tr.getAttribute( 'data-parent' ) || '0'; };
	// a row and the rows of its descendants that follow it
	var block = function ( tr ) {
		var out = [ tr ], d = depth( tr ), n = tr.nextElementSibling;
		while ( n && n.hasAttribute( 'data-id' ) && depth( n ) > d ) {
			out.push( n );
			n = n.nextElementSibling;
		}
		return out;
	};
	var prevSibling = function ( tr ) {
		var p = tr.previousElementSibling, d = depth( tr );
		while ( p && p.hasAttribute( 'data-id' ) ) {
			if ( depth( p ) < d ) {
				return null;
			}
			if ( depth( p ) === d && parent( p ) === parent( tr ) ) {
				return p;
			}
			p = p.previousElementSibling;
		}
		return null;
	};
	var nextSibling = function ( tr ) {
		var b = block( tr ), n = b[ b.length - 1 ].nextElementSibling;
		return n && n.hasAttribute( 'data-id' ) && depth( n ) === depth( tr ) && parent( n ) === parent( tr ) ? n : null;
	};
	var insertBlockBefore = function ( b, ref ) {
		b.forEach( function ( r ) { tbody.insertBefore( r, ref ); } );
	};
	var insertBlockAfter = function ( b, target ) {
		var tb = block( target ), ref = tb[ tb.length - 1 ].nextElementSibling;
		b.forEach( function ( r ) { tbody.insertBefore( r, ref ); } );
	};

	var toast = document.createElement( 'div' );
	toast.className = 'pm-toast';
	toast.setAttribute( 'role', 'status' );
	document.body.appendChild( toast );
	var toastTimer;
	var say = function ( msg, bad ) {
		toast.textContent = msg;
		toast.classList.toggle( 'is-bad', !! bad );
		toast.classList.add( 'is-on' );
		clearTimeout( toastTimer );
		toastTimer = setTimeout( function () { toast.classList.remove( 'is-on' ); }, 2200 );
	};
	var saving = null;
	var save = function () {
		var body = new URLSearchParams();
		body.append( 'action', 'pursae-term-order' );
		body.append( 'taxonomy', tax );
		body.append( '_wpnonce', nonce );
		rows().forEach( function ( r ) { body.append( 'ids[]', r.getAttribute( 'data-id' ) ); } );
		var req = fetch( window.ajaxurl || '/wp-admin/admin-ajax', { method: 'POST', credentials: 'same-origin', body: body } )
			.then( function ( r ) { return r.text().then( function ( t ) { return r.ok && t.trim() === '1'; } ); } )
			.catch( function () { return false; } );
		saving = req;
		req.then( function ( ok ) {
			if ( saving === req ) {
				say( ok ? labels.saved : labels.failed, ! ok );
			}
		} );
	};
	var refreshArrows = function () {
		rows().forEach( function ( r ) {
			var up = r.querySelector( '.pm-sort-up' ), down = r.querySelector( '.pm-sort-down' );
			if ( up ) { up.disabled = ! prevSibling( r ); }
			if ( down ) { down.disabled = ! nextSibling( r ); }
		} );
	};
	var flash = function ( b ) {
		b.forEach( function ( r ) {
			r.classList.remove( 'pm-moved' );
			void r.offsetWidth;
			r.classList.add( 'pm-moved' );
		} );
	};

	var dragBlock = null;
	rows().forEach( function ( tr ) {
		var cell = tr.querySelector( 'td.column-primary' );
		if ( ! cell ) {
			return;
		}
		var ctl = document.createElement( 'span' );
		ctl.className = 'pm-sort';
		ctl.innerHTML = '<button type="button" class="pm-sort-grip"><span class="dashicons dashicons-menu" aria-hidden="true"></span></button>' +
			'<button type="button" class="pm-sort-up"><span class="dashicons dashicons-arrow-up-alt2" aria-hidden="true"></span></button>' +
			'<button type="button" class="pm-sort-down"><span class="dashicons dashicons-arrow-down-alt2" aria-hidden="true"></span></button>';
		var name = ( tr.querySelector( '.row-title' ) || {} ).textContent || '';
		ctl.children[ 0 ].setAttribute( 'aria-label', ( labels.handle || '' ) + ': ' + name );
		ctl.children[ 0 ].setAttribute( 'data-pm-tip', labels.handle || '' );
		ctl.children[ 1 ].setAttribute( 'aria-label', ( labels.up || '' ) + ': ' + name );
		ctl.children[ 1 ].setAttribute( 'data-pm-tip', labels.up || '' );
		ctl.children[ 2 ].setAttribute( 'aria-label', ( labels.down || '' ) + ': ' + name );
		ctl.children[ 2 ].setAttribute( 'data-pm-tip', labels.down || '' );
		cell.insertBefore( ctl, cell.firstChild );

		ctl.children[ 1 ].addEventListener( 'click', function () {
			var p = prevSibling( tr );
			if ( ! p ) { return; }
			var b = block( tr );
			insertBlockBefore( b, p );
			flash( b ); refreshArrows(); save();
			this.focus();
		} );
		ctl.children[ 2 ].addEventListener( 'click', function () {
			var n = nextSibling( tr );
			if ( ! n ) { return; }
			var b = block( tr );
			insertBlockAfter( b, n );
			flash( b ); refreshArrows(); save();
			this.focus();
		} );
		ctl.children[ 0 ].addEventListener( 'pointerdown', function ( e ) { startDrag( e, tr ); } );
		// keyboard: Alt+↑ / Alt+↓ on any control of the row
		ctl.addEventListener( 'keydown', function ( e ) {
			if ( e.altKey && ( e.key === 'ArrowUp' || e.key === 'ArrowDown' ) ) {
				e.preventDefault();
				( e.key === 'ArrowUp' ? ctl.children[ 1 ] : ctl.children[ 2 ] ).click();
			}
		} );
	} );

	// pointer drag (mouse, touch and pen alike): the row follows under the pointer as a drop line
	var targetAt = function ( x, y ) {
		var el = document.elementFromPoint( x, y );
		var tr = el && el.closest && el.closest( 'tr[data-id]' );
		if ( ! tr || tr.parentNode !== tbody || ! dragBlock || dragBlock.indexOf( tr ) !== -1 ) {
			return null;
		}
		var head = tr;
		while ( head && head.hasAttribute( 'data-id' ) && depth( head ) > depth( dragBlock[ 0 ] ) ) {
			head = head.previousElementSibling;
		}
		if ( ! head || depth( head ) !== depth( dragBlock[ 0 ] ) || parent( head ) !== parent( dragBlock[ 0 ] ) ) {
			return null;
		}
		var rect = tr.getBoundingClientRect();
		return { row: head, after: head !== tr || y > rect.top + rect.height / 2 };
	};
	var clearMarks = function () {
		rows().forEach( function ( r ) { r.classList.remove( 'pm-drop-before', 'pm-drop-after' ); } );
	};
	var startDrag = function ( e, tr ) {
		if ( e.button !== undefined && e.button !== 0 ) {
			return;
		}
		e.preventDefault();
		dragBlock = block( tr );
		var target = null, moved = false, sx = e.clientX, sy = e.clientY;
		var move = function ( ev ) {
			if ( ! moved && Math.abs( ev.clientY - sy ) + Math.abs( ev.clientX - sx ) < 4 ) {
				return;
			}
			if ( ! moved ) {
				moved = true;
				document.body.classList.add( 'pm-sorting' );
				dragBlock.forEach( function ( r ) { r.classList.add( 'pm-dragging' ); } );
			}
			clearMarks();
			target = targetAt( ev.clientX, ev.clientY );
			if ( target ) {
				if ( target.after ) {
					var tb = block( target.row );
					tb[ tb.length - 1 ].classList.add( 'pm-drop-after' );
				} else {
					target.row.classList.add( 'pm-drop-before' );
				}
			}
		};
		var up = function () {
			document.removeEventListener( 'pointermove', move );
			document.removeEventListener( 'pointerup', up );
			document.removeEventListener( 'pointercancel', up );
			document.body.classList.remove( 'pm-sorting' );
			clearMarks();
			var b = dragBlock;
			b.forEach( function ( r ) { r.classList.remove( 'pm-dragging' ); } );
			dragBlock = null;
			if ( moved && target ) {
				if ( target.after ) {
					insertBlockAfter( b, target.row );
				} else {
					insertBlockBefore( b, target.row );
				}
				flash( b ); refreshArrows(); save();
			}
		};
		document.addEventListener( 'pointermove', move );
		document.addEventListener( 'pointerup', up );
		document.addEventListener( 'pointercancel', up );
	};

	refreshArrows();
	var table = tbody.closest( 'table' );
	if ( table && labels.hint ) {
		var hint = document.createElement( 'p' );
		hint.className = 'pm-sort-hint';
		hint.textContent = labels.hint;
		table.parentNode.insertBefore( hint, table );
	}
} )();

/* Toolbar light / dark toggle: switches the colour mode at once and saves it (admin_theme_mode). */
( function () {
	'use strict';
	var item = document.querySelector( '#wp-admin-bar-pm-mode > .ab-item' );
	if ( ! item ) {
		return;
	}
	var body = document.body;
	var sysDark = window.matchMedia ? window.matchMedia( '(prefers-color-scheme: dark)' ) : null;
	var isDark = function () {
		return body.classList.contains( 'pm-dark' ) || ( body.classList.contains( 'pm-auto' ) && sysDark && sysDark.matches );
	};
	var sync = function () { item.setAttribute( 'aria-pressed', isDark() ? 'true' : 'false' ); };
	sync();
	if ( sysDark && sysDark.addEventListener ) {
		sysDark.addEventListener( 'change', sync );
	}
	item.addEventListener( 'click', function ( e ) {
		e.preventDefault();
		var mode = isDark() ? 'light' : 'dark';
		body.classList.remove( 'pm-dark', 'pm-auto' );
		if ( mode === 'dark' ) {
			body.classList.add( 'pm-dark' );
		}
		sync();
		var data = new URLSearchParams();
		data.append( 'action', 'pursae-theme-mode' );
		data.append( 'mode', mode );
		data.append( '_wpnonce', item.getAttribute( 'data-pm-mode-nonce' ) || '' );
		fetch( window.ajaxurl || '/wp-admin/admin-ajax', { method: 'POST', credentials: 'same-origin', body: data } );
	} );
} )();

/* Classic editor (TinyMCE): the writing area follows the dark content mode. Its iframe does not see
   the admin styles, so a small style sheet goes in and a class on its body follows the admin's mode. */
( function () {
	'use strict';
	var body = document.body;
	if ( ! body.classList.contains( 'pursae-modern' ) ) {
		return;
	}
	var sysDark = window.matchMedia ? window.matchMedia( '(prefers-color-scheme: dark)' ) : null;
	var isDark = function () {
		return body.classList.contains( 'pm-dark' ) || ( body.classList.contains( 'pm-auto' ) && sysDark && sysDark.matches );
	};
	var css = 'html.pm-dark-content,html.pm-dark-content body{background:#121317!important;color:#D3D5DA!important;color-scheme:dark}' +
		'html.pm-dark-content body :is(h1,h2,h3,h4,h5,h6,strong,b,dt){color:#ECEDEF!important}' +
		'html.pm-dark-content body a{color:#8796EC!important}' +
		'html.pm-dark-content body :is(pre,code,kbd){background:#17191E!important;color:#ECEDEF!important;border-color:#2A2D35!important}' +
		'html.pm-dark-content body :is(blockquote,hr,table,td,th){border-color:#3A3E47!important}' +
		'html.pm-dark-content body ::selection{background:rgba(135,150,236,.35)}' +
		'html.pm-dark-content body .mce-content-body [data-mce-selected],html.pm-dark-content body img::selection{outline-color:#8796EC!important}' +
		'html.pm-dark-content body .mce-item-table,html.pm-dark-content body .mce-item-table :is(td,th,caption){border-color:#3A3E47!important}' +
		'html.pm-dark-content body .wpview-wrap,html.pm-dark-content body .mce-wp-more,html.pm-dark-content body .mce-wp-nextpage{filter:invert(.88) hue-rotate(180deg)}';
	var apply = function ( ed ) {
		var doc = ed && ed.getDoc && ed.getDoc();
		if ( ! doc || ! doc.documentElement ) {
			return;
		}
		if ( ! doc.getElementById( 'pm-dark-content' ) ) {
			var st = doc.createElement( 'style' );
			st.id = 'pm-dark-content';
			st.textContent = css;
			( doc.head || doc.documentElement ).appendChild( st );
		}
		doc.documentElement.classList.toggle( 'pm-dark-content', isDark() );
	};
	var applyAll = function () {
		if ( window.tinymce && window.tinymce.editors ) {
			Array.prototype.forEach.call( window.tinymce.editors, function ( ed ) {
				if ( ed.initialized ) {
					apply( ed );
				} else if ( ! ed.pmDarkHooked ) {
					ed.pmDarkHooked = true;
					ed.on( 'init', function () { apply( ed ); } );
				}
			} );
		}
	};
	var hook = function () {
		if ( ! window.tinymce ) {
			return;
		}
		window.tinymce.on( 'AddEditor', function ( e ) {
			e.editor.pmDarkHooked = true;
			e.editor.on( 'init', function () { apply( e.editor ); } );
		} );
		applyAll();
	};
	hook();
	window.addEventListener( 'load', applyAll );
	new MutationObserver( applyAll ).observe( body, { attributes: true, attributeFilter: [ 'class' ] } );
	if ( sysDark && sysDark.addEventListener ) {
		sysDark.addEventListener( 'change', applyAll );
	}
} )();

/* Dark mode: plugin screens hard-code light grey borders (#c3c4c7, #dcdcde, #fff…). Rather than chase
   each selector, borders that are light and colourless get the dark border token; coloured accents
   (notices, focus, primary, status) keep their colour. Runs again for content added later. */
( function () {
	'use strict';
	var body = document.body;
	if ( ! body.classList.contains( 'pursae-modern' ) ) {
		return;
	}
	var root = document.getElementById( 'wpbody-content' );
	if ( ! root ) {
		return;
	}
	var sysDark = window.matchMedia ? window.matchMedia( '(prefers-color-scheme: dark)' ) : null;
	var isDark = function () {
		return body.classList.contains( 'pm-dark' ) || ( body.classList.contains( 'pm-auto' ) && sysDark && sysDark.matches );
	};
	var sides = [ 'Top', 'Right', 'Bottom', 'Left' ];
	var lightGrey = function ( c ) {
		var m = c && c.match( /[\d.]+/g );
		if ( ! m || m.length < 3 || ( m.length > 3 && +m[ 3 ] < 0.35 ) ) {
			return false;
		}
		var r = +m[ 0 ], g = +m[ 1 ], b = +m[ 2 ];
		var max = Math.max( r, g, b ), min = Math.min( r, g, b );
		return ( 0.299 * r + 0.587 * g + 0.114 * b ) > 150 && ( max - min ) < 28;
	};
	var scan = function ( scope ) {
		if ( ! isDark() ) {
			return;
		}
		var els = ( scope || root ).querySelectorAll( '*' );
		for ( var i = 0; i < els.length; i++ ) {
			var el = els[ i ];
			if ( el.classList.contains( 'pm-dk-border' ) || el.closest( '.seogo-serp, .pm-keep-light' ) || el.matches( '.nav-tab-active, .current, .active, .selected, .is-active, .is-selected, [aria-selected="true"], [aria-current], :focus' ) ) {
				continue;
			}
			var cs = getComputedStyle( el );
			for ( var s = 0; s < 4; s++ ) {
				if ( cs[ 'border' + sides[ s ] + 'Style' ] !== 'none' && parseFloat( cs[ 'border' + sides[ s ] + 'Width' ] ) > 0 && lightGrey( cs[ 'border' + sides[ s ] + 'Color' ] ) ) {
					el.classList.add( 'pm-dk-border' );
					break;
				}
			}
		}
	};
	var clear = function () {
		root.querySelectorAll( '.pm-dk-border' ).forEach( function ( el ) { el.classList.remove( 'pm-dk-border' ); } );
	};
	var refresh = function () {
		if ( isDark() ) {
			scan();
		} else {
			clear();
		}
	};
	refresh();
	window.addEventListener( 'load', refresh );
	new MutationObserver( refresh ).observe( body, { attributes: true, attributeFilter: [ 'class' ] } );
	if ( sysDark && sysDark.addEventListener ) {
		sysDark.addEventListener( 'change', refresh );
	}
	var timer;
	new MutationObserver( function () {
		clearTimeout( timer );
		timer = setTimeout( function () { scan(); }, 150 );
	} ).observe( root, { childList: true, subtree: true } );
} )();
