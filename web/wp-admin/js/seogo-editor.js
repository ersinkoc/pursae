/* SEO-Go editor box: the classic editor meta box and the Pursae Editor sidebar share this script.
   The server does the work (/wp-json/seogo/v1/analyze): variables, snippet values and both
   analyses; this file collects the editor's values, renders the results and saves in Pursae Editor. */
( function () {
	'use strict';

	var cfg = window.seogoEditor || {};
	var box = null;
	var timer = null;
	var lastPayload = '';
	var lastSuggest = 0;
	var latest = null;

	function $( sel, root ) { return ( root || box ).querySelector( sel ); }
	function $$( sel, root ) { return Array.prototype.slice.call( ( root || box ).querySelectorAll( sel ) ); }
	function esc( s ) {
		return String( s == null ? '' : s ).replace( /[&<>"']/g, function ( c ) {
			return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ c ];
		} );
	}
	function rating( n ) { return n >= 71 ? 'good' : n >= 41 ? 'ok' : 'bad'; }

	function api( path, opts ) {
		opts = opts || {};
		var headers = { 'X-WP-Nonce': cfg.nonce, 'Accept': 'application/json' };
		if ( opts.body ) { headers[ 'Content-Type' ] = 'application/json'; }
		return fetch( ( cfg.root || '/wp-json/' ) + path, {
			method: opts.method || 'GET', credentials: 'same-origin', headers: headers,
			body: opts.body ? JSON.stringify( opts.body ) : undefined
		} ).then( function ( r ) {
			if ( ! r.ok ) { throw new Error( 'HTTP ' + r.status ); }
			return r.json();
		} );
	}

	// --- editor values ----------------------------------------------------------------------
	function blockEditor() { return cfg.mode === 'block' && window.wp && wp.data && wp.data.select( 'core/editor' ); }

	function editorData() {
		var ed = blockEditor();
		if ( ed ) {
			return {
				title: ed.getEditedPostAttribute( 'title' ) || '',
				content: ed.getEditedPostContent() || '',
				slug: ed.getEditedPostAttribute( 'slug' ) || '',
				excerpt: ed.getEditedPostAttribute( 'excerpt' ) || ''
			};
		}
		var content = '';
		var mce = window.tinymce && tinymce.get && tinymce.get( 'content' );
		if ( mce && ! mce.isHidden() ) {
			content = mce.getContent();
		} else if ( document.getElementById( 'content' ) ) {
			content = document.getElementById( 'content' ).value;
		}
		var slugEl = document.getElementById( 'post_name' ) || document.getElementById( 'editable-post-name-full' );
		var titleEl = document.getElementById( 'title' );
		var exEl = document.getElementById( 'excerpt' );
		return {
			title: titleEl ? titleEl.value : '',
			content: content,
			slug: slugEl ? ( slugEl.value !== undefined ? slugEl.value : slugEl.textContent ) : '',
			excerpt: exEl ? exEl.value : ''
		};
	}

	function fields() {
		var out = {};
		$$( '[name^="seogo["]' ).forEach( function ( el ) {
			var m = el.name.match( /^seogo\[([a-z_]+)\](\[\])?$/ );
			if ( ! m ) { return; }
			var key = m[ 1 ];
			if ( m[ 2 ] ) {
				out[ key ] = out[ key ] || [];
				if ( el.checked ) { out[ key ].push( el.value ); }
				return;
			}
			if ( el.type === 'checkbox' ) {
				out[ key ] = el.checked ? '1' : '';
			} else {
				out[ key ] = el.value;
			}
		} );
		if ( Array.isArray( out.robots_adv ) ) { out.robots_adv = out.robots_adv.join( ',' ); }
		return out;
	}

	function product() {
		var out = {};
		$$( '[name^="seogo_product["]' ).forEach( function ( el ) {
			out[ el.name.slice( 14, -1 ) ] = el.value;
		} );
		return out;
	}

	// --- rendering --------------------------------------------------------------------------
	function titleWidth( t ) {
		var w = 0;
		for ( var i = 0; i < t.length; i++ ) {
			var c = t[ i ];
			if ( 'iljI.,;:!|\''.indexOf( c ) >= 0 ) { w += 4.5; } else if ( 'ftr()[]- '.indexOf( c ) >= 0 ) { w += 6; } else if ( 'mwMW'.indexOf( c ) >= 0 ) { w += 16; } else if ( c !== c.toLowerCase() || /\d/.test( c ) ) { w += 12.5; } else { w += 10.2; }
		}
		return w;
	}

	function highlight( text, keyphrase ) {
		var html = esc( text );
		if ( ! keyphrase ) { return html; }
		keyphrase.split( /\s+/ ).filter( function ( w ) { return w.length > 2; } ).forEach( function ( w ) {
			var re = new RegExp( '(^|[^\\w])(' + w.replace( /[.*+?^${}()|[\]\\]/g, '\\$&' ) + '\\w*)', 'gi' );
			html = html.replace( re, '$1<mark>$2</mark>' );
		} );
		return html;
	}

	function setProgress( which, value, max, okMin ) {
		var el = $( '.seogo-progress[data-for="' + which + '"]' );
		if ( ! el ) { return; }
		var pct = Math.min( 100, Math.round( value / max * 100 ) );
		el.firstElementChild.style.width = pct + '%';
		el.classList.remove( 'good', 'ok' );
		if ( value > 0 && value <= max ) { el.classList.add( value >= okMin ? 'good' : 'ok' ); }
	}

	function renderResults( kind, a ) {
		var wrap = $( '.seogo-analysis[data-kind="' + kind + '"] .seogo-results' );
		if ( ! wrap || ! a ) { return; }
		var html = '', group = '';
		( a.results || [] ).forEach( function ( r ) {
			var g = r.group || '';
			if ( g !== group ) {
				group = g;
				html += '<li class="seogo-group">' + ( g === 'extra' ? 'Additional checks' : g === 'insight' ? 'Insights' : '' ) + '</li>';
			}
			var text = esc( r.text ).replace( /^([^:]+):/, '<strong>$1</strong>:' );
			html += '<li class="' + esc( r.rating ) + ( g ? ' extra' : '' ) + '"><span class="seogo-bullet ' + esc( r.rating ) + '"></span><span>' + text + '</span></li>';
		} );
		wrap.innerHTML = html;
		var head = document.querySelectorAll( '.seogo-head-score[data-kind="' + kind + '"] .seogo-bullet' );
		head.forEach( function ( b ) { b.className = 'seogo-bullet ' + ( a.rating || 'na' ); } );
		var pts = $( '.seogo-analysis[data-kind="seo"] .seogo-points' );
		if ( kind === 'seo' && pts ) { pts.textContent = a.points ? a.points + ' / 100' : ''; }
	}

	function render( res, data ) {
		latest = res;
		var f = fields();
		var key = ( res.keywords && res.keywords[ 0 ] ) || '';
		var serp = $( '.seogo-serp' );
		var url = res.url || '';
		var crumbs = url.replace( /^https?:\/\//, '' ).replace( /\/$/, '' ).split( '/' ).join( ' › ' );
		$( '.seogo-serp-url', serp ).textContent = crumbs;
		$( '.seogo-serp-title', serp ).innerHTML = highlight( res.title || data.title || '', key );
		var desc = res.description || res.description_fallback || '';
		$( '.seogo-serp-desc', serp ).innerHTML = desc ? highlight( desc, key ) : '<em>Please provide a meta description by editing the snippet below.</em>';
		setProgress( 'title', titleWidth( res.title || '' ), 600, 400 );
		setProgress( 'description', ( res.description || '' ).length, 156, 121 );
		if ( res.seo ) { renderResults( 'seo', res.seo ); }
		if ( res.readability ) { renderResults( 'readability', res.readability ); }
		// social preview
		var fb = $( '.seogo-social-preview' );
		if ( fb ) {
			var img = f.og_image || res.og_image || '';
			$( '.seogo-fb-image', fb ).style.backgroundImage = img ? 'url("' + img.replace( /"/g, '%22' ) + '")' : '';
			$( '.seogo-fb-host', fb ).textContent = ( cfg.home || '' ).replace( /^https?:\/\//, '' ).replace( /\/.*$/, '' );
			$( '.seogo-fb-title', fb ).textContent = f.og_title || res.title || '';
			$( '.seogo-fb-desc', fb ).textContent = f.og_description || desc;
		}
		document.dispatchEvent( new CustomEvent( 'seogo:analysis', { detail: res } ) );
	}

	function analyze( force ) {
		var data = editorData();
		var payload = { post_id: cfg.postId, title: data.title, content: data.content, slug: data.slug, excerpt: data.excerpt, fields: fields() };
		var key = JSON.stringify( payload );
		if ( ! force && key === lastPayload ) { return; }
		lastPayload = key;
		api( 'seogo/v1/analyze', { method: 'POST', body: payload } ).then( function ( res ) { render( res, data ); } ).catch( function () {} );
		suggest( data );
	}

	function schedule() {
		clearTimeout( timer );
		timer = setTimeout( analyze, 600 );
	}

	function suggest( data ) {
		var list = $( '.seogo-suggestions' );
		if ( ! list || Date.now() - lastSuggest < 5000 ) { return; }
		lastSuggest = Date.now();
		var text = ( data.title + ' ' + data.content.replace( /<[^>]+>/g, ' ' ) ).replace( /\s+/g, ' ' ).slice( 0, 1500 );
		if ( text.trim().length < 20 ) { return; }
		api( 'seogo/v1/links/suggest?post_id=' + encodeURIComponent( cfg.postId ) + '&q=' + encodeURIComponent( text ) ).then( function ( items ) {
			if ( ! items.length ) {
				list.innerHTML = '<li class="description">No related content found yet.</li>';
				return;
			}
			list.innerHTML = items.map( function ( it ) {
				return '<li><a href="' + esc( it.url ) + '" target="_blank" rel="noopener">' + esc( it.title || '(no title)' ) + '</a>' +
					( it.cornerstone ? ' <span class="dashicons dashicons-star-filled seogo-star" title="Cornerstone content"></span>' : '' ) +
					( it.linked ? ' <span class="seogo-tag">Linked</span>' : ' <button type="button" class="button button-small seogo-copy" data-url="' + esc( it.url ) + '">Copy link</button>' ) + '</li>';
			} ).join( '' );
		} ).catch( function () {} );
	}

	// --- wiring -----------------------------------------------------------------------------
	// changed reanalyzes; in Pursae Editor it also marks the post as changed so Save is offered (the
	// SEO fields are stored right after the post itself, see initBlock).
	function changed( e ) {
		schedule();
		if ( blockEditor() && e && e.target && ( /^seogo(_product)?\[/.test( e.target.name || '' ) ) ) {
			wp.data.dispatch( 'core/editor' ).editPost( { seogo_changed: Date.now() } );
		}
	}

	function bindBox() {
		$$( '.seogo-tab' ).forEach( function ( tab ) {
			tab.addEventListener( 'click', function () {
				$$( '.seogo-tab' ).forEach( function ( t ) { t.classList.toggle( 'is-active', t === tab ); t.setAttribute( 'aria-selected', t === tab ? 'true' : 'false' ); } );
				$$( '.seogo-panel' ).forEach( function ( p ) {
					var on = p.dataset.panel === tab.dataset.tab;
					p.hidden = ! on;
					p.classList.toggle( 'is-active', on );
				} );
			} );
		} );
		$$( 'input[name="seogo_preview_mode"]' ).forEach( function ( r ) {
			r.addEventListener( 'change', function () { $( '.seogo-serp' ).classList.toggle( 'is-mobile', r.value === 'mobile' && r.checked ); } );
		} );
		var art = $( '.seogo-art-type' );
		if ( art ) {
			art.addEventListener( 'change', function () { $( '.seogo-product' ).hidden = art.value !== 'Product'; } );
		}
		box.addEventListener( 'input', changed );
		box.addEventListener( 'change', changed );
		box.addEventListener( 'click', function ( e ) {
			var b = e.target.closest( '.seogo-copy' );
			if ( b && navigator.clipboard ) {
				navigator.clipboard.writeText( b.dataset.url );
				b.textContent = 'Copied';
			}
		} );
		if ( window.seogoBindImagePickers ) { window.seogoBindImagePickers( box ); }
		if ( window.seogoBindVarMenus ) { window.seogoBindVarMenus( box ); }
	}

	function initClassic() {
		var target = document.getElementById( 'normal-sortables' );
		if ( target ) { target.insertBefore( box, target.firstChild ); }
		[ 'title', 'content', 'excerpt', 'post_name' ].forEach( function ( id ) {
			var el = document.getElementById( id );
			if ( el ) { el.addEventListener( 'input', schedule ); }
		} );
		var hook = function () {
			var mce = window.tinymce && tinymce.get && tinymce.get( 'content' );
			if ( mce ) {
				mce.on( 'keyup change SetContent', schedule );
				return true;
			}
			return false;
		};
		if ( ! hook() ) {
			var tries = 0, iv = setInterval( function () { if ( hook() || ++tries > 40 ) { clearInterval( iv ); } }, 250 );
		}
		setInterval( function () { analyze( false ); }, 5000 ); // slug edits, media inserts…
		analyze( true );
	}

	function initBlock() {
		var el = wp.element.createElement;
		var useState = wp.element.useState, useEffect = wp.element.useEffect;
		var PluginSidebar = ( wp.editor && wp.editor.PluginSidebar ) || ( wp.editPost && wp.editPost.PluginSidebar );
		var MoreItem = ( wp.editor && wp.editor.PluginSidebarMoreMenuItem ) || ( wp.editPost && wp.editPost.PluginSidebarMoreMenuItem );
		var DocPanel = ( wp.editor && wp.editor.PluginDocumentSettingPanel ) || ( wp.editPost && wp.editPost.PluginDocumentSettingPanel );
		box.classList.remove( 'closed' );

		function Mount() {
			return el( 'div', { className: 'seogo-sidebar', ref: function ( node ) { if ( node && box.parentNode !== node ) { node.appendChild( box ); } } } );
		}
		function Summary() {
			var st = useState( latest );
			useEffect( function () {
				var h = function ( e ) { st[ 1 ]( e.detail ); };
				document.addEventListener( 'seogo:analysis', h );
				return function () { document.removeEventListener( 'seogo:analysis', h ); };
			}, [] );
			var r = st[ 0 ] || {};
			var bullet = function ( a, label ) {
				return el( 'span', null, el( 'span', { className: 'seogo-bullet ' + ( ( a && a.rating ) || 'na' ) } ), label );
			};
			return el( 'div', { className: 'seogo-doc-panel' },
				bullet( r.seo, 'SEO analysis' ), bullet( r.readability, 'Readability analysis' ),
				el( 'button', { type: 'button', className: 'components-button is-secondary', onClick: function () {
					wp.data.dispatch( 'core/edit-post' ).openGeneralSidebar( 'seogo/seogo-sidebar' );
				} }, 'Open SEO-Go' ) );
		}
		wp.plugins.registerPlugin( 'seogo', {
			icon: 'chart-line',
			render: function () {
				return el( wp.element.Fragment, null,
					MoreItem ? el( MoreItem, { target: 'seogo-sidebar', icon: 'chart-line' }, 'SEO-Go' ) : null,
					el( PluginSidebar, { name: 'seogo-sidebar', title: 'SEO-Go', icon: 'chart-line' }, el( Mount ) ),
					DocPanel ? el( DocPanel, { name: 'seogo-summary', title: 'SEO-Go', className: 'seogo-summary' }, el( Summary ) ) : null
				);
			}
		} );

		var wasSaving = false;
		var lastContent = '';
		wp.data.subscribe( function () {
			var ed = wp.data.select( 'core/editor' );
			if ( ! ed ) { return; }
			var saving = ed.isSavingPost() && ! ed.isAutosavingPost();
			if ( wasSaving && ! saving && ed.didPostSaveRequestSucceed() ) {
				api( 'seogo/v1/meta/' + cfg.postId, { method: 'POST', body: { fields: fields(), product: product() } } ).catch( function () {} );
			}
			wasSaving = saving;
			var c = ed.getEditedPostContent() + '|' + ed.getEditedPostAttribute( 'title' ) + '|' + ed.getEditedPostAttribute( 'slug' );
			if ( c !== lastContent ) {
				lastContent = c;
				schedule();
			}
		} );
		analyze( true );
	}

	function init() {
		var src = document.getElementById( 'seogo-box-source' );
		box = src && src.querySelector( '#seogo-metabox' );
		if ( ! box ) { return; }
		bindBox();
		if ( cfg.mode === 'block' && window.wp && wp.plugins ) {
			initBlock();
		} else {
			initClassic();
		}
	}
	if ( document.readyState === 'loading' ) {
		document.addEventListener( 'DOMContentLoaded', init );
	} else {
		init();
	}
}() );
