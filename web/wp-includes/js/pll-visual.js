/**
 * Pursae Multi Language: visual translation mode, inside the page.
 *
 * Loaded only in a visual session (the page was opened by Languages > Translate visually with a
 * token). It reports the page and its translatable strings to the editor panel (the parent
 * window, same origin), outlines what can be translated, turns a click into a selection, applies
 * live previews sent by the panel and keeps navigation inside the editor.
 */
( function() {
	'use strict';
	// The real data island is the last one: the server adds it right before </body>, after any
	// post content (which could hold a look-alike element with the same id).
	var islands = document.querySelectorAll( 'script#pll-visual-data[type="application/json"]' ),
		island = islands.length ? islands[ islands.length - 1 ] : null,
		data;
	if ( ! island ) {
		return;
	}
	try {
		data = JSON.parse( island.textContent );
	} catch ( e ) {
		return;
	}
	var param = data.param || 'pll_visual',
		token = new URL( location.href ).searchParams.get( param ) || '',
		parentWin = window.parent !== window ? window.parent : null,
		markerRe = /^s[0-9a-f]{12}$/,
		attrRe = /^(alt|title|placeholder|aria-label|aria-description|value)$/,
		hovered = null,
		selected = null;

	function send( msg ) {
		if ( parentWin ) {
			parentWin.postMessage( msg, location.origin );
		}
	}
	function clean( u ) {
		u.searchParams.delete( param );
		return u.pathname + u.search + u.hash;
	}
	function inside( u ) {
		return u.origin === location.origin && ! /^\/(wp-admin|wp-login|wp-json|xmlrpc)/.test( u.pathname );
	}
	function attrMarkers( el ) {
		var out = [], i, a;
		if ( ! el || ! el.attributes ) {
			return out;
		}
		for ( i = 0; i < el.attributes.length; i++ ) {
			a = el.attributes[ i ];
			if ( a.name.indexOf( 'data-pll-a-' ) === 0 && markerRe.test( a.value ) ) {
				out.push( { attr: a.name.slice( 11 ), m: a.value } );
			}
		}
		return out;
	}

	// What a node of the page translates as: a string, an image, an element with translatable
	// attributes, or a post content region.
	function targetOf( node ) {
		var el = node && node.nodeType === 1 ? node : node && node.parentElement, t, n;
		if ( ! el || ! el.closest ) {
			return null;
		}
		t = el.closest( 'pll-t[data-pll-s]' );
		if ( t ) {
			return { el: t, kind: 'string', m: t.getAttribute( 'data-pll-s' ) };
		}
		if ( el.matches( 'img[data-pll-img]' ) ) {
			return { el: el, kind: 'image', id: parseInt( el.getAttribute( 'data-pll-img' ), 10 ), alt: el.getAttribute( 'alt' ) || '' };
		}
		for ( n = el; n && n !== document.body; n = n.parentElement ) {
			if ( n.tagName === 'PLL-UC' ) {
				break;
			}
			t = attrMarkers( n );
			if ( t.length ) {
				n.classList.add( 'pll-v-attr' );
				return { el: n, kind: 'attr', attrs: t, m: t[ 0 ].m };
			}
		}
		t = el.closest( 'pll-uc[data-pll-post]' );
		if ( t ) {
			return { el: t, kind: 'post', id: parseInt( t.getAttribute( 'data-pll-post' ), 10 ) };
		}
		return null;
	}

	if ( parentWin ) {
		document.documentElement.classList.add( 'pll-visual' );
		send( { pllVisual: 'ready', url: clean( new URL( location.href ) ), title: document.title, data: data } );

		document.addEventListener( 'mouseover', function( e ) {
			var t = targetOf( e.target );
			if ( hovered && ( ! t || hovered !== t.el ) ) {
				hovered.classList.remove( 'pll-v-hover' );
				hovered = null;
			}
			if ( t ) {
				hovered = t.el;
				hovered.classList.add( 'pll-v-hover' );
			}
		} );
		document.addEventListener( 'mouseleave', function() {
			if ( hovered ) {
				hovered.classList.remove( 'pll-v-hover' );
				hovered = null;
			}
		} );
	}

	function select( t ) {
		if ( selected ) {
			selected.classList.remove( 'pll-v-selected' );
		}
		selected = t.el;
		selected.classList.add( 'pll-v-selected' );
		var msg = { pllVisual: 'select', kind: t.kind };
		if ( t.kind === 'string' || t.kind === 'attr' ) {
			msg.m = t.m;
			msg.attrs = t.attrs || [];
		} else if ( t.kind === 'image' ) {
			msg.id = t.id;
			msg.alt = t.alt;
		} else {
			msg.id = t.id;
		}
		send( msg );
	}

	// A click selects (Shift+click follows a link); navigation stays inside the editor.
	document.addEventListener( 'click', function( e ) {
		var t, a, u;
		if ( parentWin && ! e.shiftKey ) {
			t = targetOf( e.target );
			if ( t ) {
				e.preventDefault();
				e.stopPropagation();
				select( t );
				return;
			}
		}
		a = e.target.closest && e.target.closest( 'a[href]' );
		if ( ! a || a.getAttribute( 'href' ).charAt( 0 ) === '#' ) {
			return;
		}
		u = new URL( a.href, location.href );
		if ( ! inside( u ) ) {
			e.preventDefault();
			return;
		}
		u.searchParams.set( param, token );
		if ( e.shiftKey || e.ctrlKey || e.metaKey || a.target ) {
			// Shift+click (and new-window clicks) follow the link in this frame, never a new window.
			e.preventDefault();
			location.href = u.toString();
			return;
		}
		a.href = u.toString();
	}, true );
	document.addEventListener( 'submit', function( e ) {
		var f = e.target, u = new URL( f.action || location.href, location.href ), i;
		if ( ! inside( u ) || ( f.method || 'get' ).toLowerCase() !== 'get' ) {
			e.preventDefault();
			return;
		}
		i = document.createElement( 'input' );
		i.type = 'hidden';
		i.name = param;
		i.value = token;
		f.appendChild( i );
	}, true );

	function each( sel, fn ) {
		Array.prototype.forEach.call( document.querySelectorAll( sel ), fn );
	}

	// Messages of the editor panel.
	window.addEventListener( 'message', function( e ) {
		var d = e.data, first = null;
		if ( e.origin !== location.origin || e.source !== window.parent || ! d || typeof d !== 'object' ) {
			return;
		}
		if ( d.pllVisual === 'update' && markerRe.test( d.m ) && typeof d.text === 'string' ) {
			each( 'pll-t[data-pll-s="' + d.m + '"]', function( el ) {
				el.textContent = d.text;
			} );
			( d.attrs || [] ).forEach( function( attr ) {
				if ( attr && attrRe.test( attr ) ) {
					each( '[data-pll-a-' + attr + '="' + d.m + '"]', function( el ) {
						el.setAttribute( attr, d.text );
					} );
				}
			} );
		} else if ( d.pllVisual === 'alt' && typeof d.alt === 'string' ) {
			each( 'img[data-pll-img="' + parseInt( d.id, 10 ) + '"]', function( el ) {
				el.setAttribute( 'alt', d.alt );
			} );
		} else if ( d.pllVisual === 'focus' && markerRe.test( d.m ) ) {
			first = document.querySelector( 'pll-t[data-pll-s="' + d.m + '"]' );
			if ( ! first ) {
				each( '*', function( el ) {
					if ( ! first && attrMarkers( el ).some( function( x ) { return x.m === d.m; } ) ) {
						first = el;
					}
				} );
			}
			if ( first ) {
				if ( selected ) {
					selected.classList.remove( 'pll-v-selected' );
				}
				selected = first;
				first.classList.add( 'pll-v-selected', 'pll-v-attr' );
				first.scrollIntoView( { block: 'center', behavior: 'smooth' } );
				first.classList.remove( 'pll-v-flash' );
				void first.offsetWidth;
				first.classList.add( 'pll-v-flash' );
			}
		}
	} );
} )();
