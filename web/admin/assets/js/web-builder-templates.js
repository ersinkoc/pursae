/**
 * Appearance › Theme Builder (web-builder-templates.html): the Add New dialog, the display
 * conditions dialog and the confirm links. The dialogs talk to the web-builder/v2 REST routes
 * (contract §11.8) with the wp_rest nonce and the X-WB-Api header; without JavaScript the Add New
 * form is a plain GET form and Edit Conditions is hidden.
 *
 * The conditions model is the editor's (builder/host/conditions.ts), so both dialogs build and
 * describe rules the same way. A rule is "<include|exclude>/<scope>"; a row is
 *   group   general | singular | archive | 404
 *   sub     singular: '' | front_page | <ptype>     archive: '' | home | date | search | author | type/<ptype> | <tax>
 *   target  singular/<ptype>: '' | id | in/<tax> | child_of | by_author     archive/<tax>|author: '' | id
 *   id      the entry, term or user a target needs
 */
( function() {
	'use strict';

	var cfgEl = document.getElementById( 'web-builder-templates-config' );
	if ( ! cfgEl ) {
		return;
	}
	var cfg = JSON.parse( cfgEl.textContent || '{}' );
	var PATTERN = new RegExp( cfg.pattern );
	var ID_RE = /^[1-9][0-9]{0,18}$/;
	var ARCHIVE_FIXED = [ 'home', 'date', 'search', 'author' ];

	function el( tag, attrs, children ) {
		var n = document.createElement( tag );
		Object.keys( attrs || {} ).forEach( function( k ) {
			if ( k === 'text' ) {
				n.textContent = attrs[ k ];
			} else if ( k === 'className' ) {
				n.className = attrs[ k ];
			} else if ( attrs[ k ] !== null && attrs[ k ] !== undefined && attrs[ k ] !== false ) {
				n.setAttribute( k, attrs[ k ] === true ? '' : attrs[ k ] );
			}
		} );
		( children || [] ).forEach( function( c ) {
			if ( c ) {
				n.appendChild( c );
			}
		} );
		return n;
	}

	function fmt( s, vars ) {
		return s.replace( /\{(\w+)\}/g, function( m, k ) {
			return vars[ k ] !== undefined ? vars[ k ] : m;
		} );
	}

	/* ---------- REST ---------- */

	function api( method, path, body ) {
		var headers = { 'X-WP-Nonce': cfg.nonce, Accept: 'application/json' };
		headers[ cfg.apiHeader ] = String( cfg.api );
		if ( body !== undefined ) {
			headers[ 'Content-Type' ] = 'application/json';
		}
		return fetch( cfg.rest + path, { method: method, credentials: 'same-origin', headers: headers, body: body === undefined ? undefined : JSON.stringify( body ) } )
			.then( function( r ) {
				return r.json().catch( function() {
					return null;
				} ).then( function( data ) {
					return { ok: r.ok, status: r.status, data: data };
				} );
			} );
	}

	function errorText( res ) {
		if ( res && res.data && res.data.message ) {
			return res.data.message;
		}
		if ( res && res.data && res.data.code === 'wb_api_mismatch' ) {
			return 'Pursae Web Builder was updated. Reload the page and try again.';
		}
		return 'Something went wrong. Please try again.';
	}

	/* ---------- dialogs ---------- */

	function openDialog( d ) {
		if ( typeof d.showModal === 'function' ) {
			d.showModal();
		} else {
			d.setAttribute( 'open', '' );
		}
	}

	function closeDialog( d ) {
		if ( typeof d.close === 'function' ) {
			d.close();
		} else {
			d.removeAttribute( 'open' );
		}
	}

	function showError( d, msg ) {
		var box = d.querySelector( '.web-builder-dialog-error' );
		box.querySelector( 'p' ).textContent = msg || '';
		box.hidden = ! msg;
	}

	document.querySelectorAll( '.web-builder-dialog' ).forEach( function( d ) {
		d.addEventListener( 'click', function( e ) {
			if ( e.target === d || e.target.closest( '[data-close]' ) ) {
				e.preventDefault();
				closeDialog( d );
			}
		} );
	} );

	/* ---------- Add New ---------- */

	var addDialog = document.getElementById( 'web-builder-add-template' );
	if ( addDialog ) {
		var addForm = document.getElementById( 'web-builder-add-template-form' );
		var typeSel = document.getElementById( 'web-builder-new-type' );
		var ptRow = addForm.querySelector( '.web-builder-new-post-type-row' );
		var syncType = function() {
			if ( ptRow ) {
				ptRow.hidden = typeSel.value !== 'single' || ! document.getElementById( 'web-builder-new-post-type' ).options.length;
			}
		};
		typeSel.addEventListener( 'change', syncType );
		syncType();
		var openAdd = function( e ) {
			if ( e ) {
				e.preventDefault();
			}
			showError( addDialog, '' );
			syncType();
			openDialog( addDialog );
			document.getElementById( 'web-builder-new-title' ).focus();
		};
		document.querySelectorAll( '#web-builder-add-template-button, .web-builder-add-template-link' ).forEach( function( a ) {
			a.addEventListener( 'click', openAdd );
		} );
		if ( location.hash === '#web-builder-add-template' ) {
			openAdd();
		}
		addForm.addEventListener( 'submit', function( e ) {
			e.preventDefault();
			var btn = document.getElementById( 'web-builder-create-template' );
			var starter = addForm.querySelector( 'input[name="starter"]:checked' );
			var body = { type: typeSel.value, title: document.getElementById( 'web-builder-new-title' ).value.trim() };
			if ( starter && starter.value ) {
				body.starter = starter.value;
			}
			if ( body.type === 'single' ) {
				body.postType = document.getElementById( 'web-builder-new-post-type' ).value;
			}
			btn.disabled = true;
			showError( addDialog, '' );
			api( 'POST', cfg.namespace + '/templates', body ).then( function( res ) {
				if ( res.ok && res.data && res.data.editUrl ) {
					location.href = res.data.editUrl;
					return;
				}
				btn.disabled = false;
				showError( addDialog, errorText( res ) );
			}, function() {
				btn.disabled = false;
				showError( addDialog, errorText( null ) );
			} );
		} );
	}

	/* ---------- conditions model (builder/host/conditions.ts) ---------- */

	function typeOf( slug ) {
		return ( cfg.postTypes || [] ).filter( function( p ) {
			return p.slug === slug;
		} )[ 0 ];
	}
	function taxOf( slug ) {
		return ( cfg.taxonomies || [] ).filter( function( x ) {
			return x.slug === slug;
		} )[ 0 ];
	}
	function typeLabel( slug ) {
		var p = typeOf( slug );
		return ( p && p.label ) || slug;
	}
	function taxLabel( slug ) {
		var x = taxOf( slug );
		return ( x && x.label ) || slug;
	}
	function isTaxSub( sub ) {
		return !! sub && ARCHIVE_FIXED.indexOf( sub ) < 0 && sub.indexOf( 'type/' ) !== 0;
	}
	function bytes( s ) {
		return new TextEncoder().encode( s ).length;
	}

	function isValidRule( rule ) {
		if ( bytes( rule ) > cfg.limits.maxConditionBytes || ! PATTERN.test( rule ) ) {
			return false;
		}
		var p = rule.split( '/' );
		if ( p[ 1 ] === 'singular' && p.length > 2 && p[ 2 ] !== 'front_page' && cfg.reservedSingular.indexOf( p[ 2 ] ) >= 0 ) {
			return false;
		}
		if ( p[ 1 ] === 'archive' && p.length === 3 && cfg.reservedArchive.indexOf( p[ 2 ] ) >= 0 && ARCHIVE_FIXED.indexOf( p[ 2 ] ) < 0 ) {
			return false;
		}
		if ( p[ 1 ] === 'archive' && p.length === 4 && p[ 2 ] !== 'author' && p[ 2 ] !== 'type' && cfg.reservedArchive.indexOf( p[ 2 ] ) >= 0 ) {
			return false;
		}
		return true;
	}

	function emptyRow( mode ) {
		return { mode: mode || 'include', group: 'general', sub: '', target: '', id: '', idLabel: '' };
	}

	function parseRule( rule ) {
		var r = String( rule ).trim();
		if ( ! isValidRule( r ) ) {
			return null;
		}
		var parts = r.split( '/' );
		var row = { mode: parts[ 0 ], group: parts[ 1 ], sub: '', target: '', id: '', idLabel: '' };
		var rest = parts.slice( 2 );
		if ( row.group === 'singular' && rest.length ) {
			row.sub = rest[ 0 ];
			var tail = rest.slice( 1 );
			if ( tail.length === 1 ) {
				row.target = 'id';
				row.id = tail[ 0 ];
			} else if ( tail[ 0 ] === 'in' ) {
				row.target = 'in/' + tail[ 1 ];
				row.id = tail[ 2 ];
			} else if ( tail[ 0 ] === 'child_of' || tail[ 0 ] === 'by_author' ) {
				row.target = tail[ 0 ];
				row.id = tail[ 1 ];
			}
		} else if ( row.group === 'user' ) {
			row.sub = rest.join( '/' );
		} else if ( row.group === 'archive' && rest.length ) {
			if ( rest[ 0 ] === 'type' ) {
				row.sub = 'type/' + rest[ 1 ];
			} else {
				row.sub = rest[ 0 ];
				if ( rest.length === 2 ) {
					row.target = 'id';
					row.id = rest[ 1 ];
				}
			}
		}
		return row;
	}

	function rowRule( row ) {
		var parts = [ row.mode, row.group ];
		if ( row.group === 'singular' && row.sub ) {
			parts.push( row.sub );
			if ( row.sub !== 'front_page' && row.target ) {
				if ( ! ID_RE.test( row.id ) ) {
					return null;
				}
				if ( row.target === 'id' ) {
					parts.push( row.id );
				} else {
					parts.push.apply( parts, row.target.split( '/' ).concat( [ row.id ] ) );
				}
			}
		} else if ( row.group === 'user' ) {
			parts.push.apply( parts, ( row.sub || 'logged_in' ).split( '/' ) );
		} else if ( row.group === 'archive' && row.sub ) {
			parts.push.apply( parts, row.sub.split( '/' ) );
			if ( row.target === 'id' && ( row.sub === 'author' || isTaxSub( row.sub ) ) ) {
				if ( ! ID_RE.test( row.id ) ) {
					return null;
				}
				parts.push( row.id );
			}
		}
		var rule = parts.join( '/' );
		return isValidRule( rule ) ? rule : null;
	}

	function groupOptions() {
		return [
			{ value: 'general', label: 'Entire Site' },
			{ value: 'singular', label: 'Singular' },
			{ value: 'archive', label: 'Archives' },
			{ value: '404', label: '404 Page' },
			{ value: 'user', label: 'Visitor' },
		];
	}

	function keepOption( out, keep, label ) {
		if ( keep && ! out.some( function( o ) {
			return o.value === keep;
		} ) ) {
			out.push( { value: keep, label: label } );
		}
		return out;
	}

	function subOptions( group, keep ) {
		var out = [];
		if ( group === 'user' ) {
			out = [ { value: 'logged_in', label: 'Logged-in Users' }, { value: 'logged_out', label: 'Logged-out Visitors' } ].concat( ( cfg.roles || [] ).map( function( r ) {
				return { value: 'role/' + r.slug, label: 'Role: ' + ( r.label || r.slug ) };
			} ) );
			return keepOption( out, keep, 'Role: ' + String( keep ).replace( /^role\//, '' ) );
		}
		if ( group === 'singular' ) {
			out = [ { value: '', label: 'All Singular' }, { value: 'front_page', label: 'Front Page' } ].concat( ( cfg.postTypes || [] ).map( function( p ) {
				return { value: p.slug, label: p.label || p.slug };
			} ) );
		} else if ( group === 'archive' ) {
			out = [
				{ value: '', label: 'All Archives' },
				{ value: 'home', label: 'Posts Page' },
				{ value: 'date', label: 'Date Archives' },
				{ value: 'search', label: 'Search Results' },
				{ value: 'author', label: 'Author Archives' },
			].concat( ( cfg.postTypes || [] ).filter( function( p ) {
				return p.archive || p.slug === 'post';
			} ).map( function( p ) {
				return { value: 'type/' + p.slug, label: fmt( '{type} Archive', { type: p.label || p.slug } ) };
			} ), ( cfg.taxonomies || [] ).map( function( x ) {
				return { value: x.slug, label: x.label || x.slug };
			} ) );
		}
		return keepOption( out, keep, keep && keep.indexOf( 'type/' ) === 0 ? fmt( '{type} Archive', { type: keep.slice( 5 ) } ) : keep );
	}

	function targetOptions( row, keep ) {
		var out = [];
		if ( row.group === 'singular' && row.sub && row.sub !== 'front_page' ) {
			var pt = typeOf( row.sub );
			out = [ { value: '', label: fmt( 'All {type}', { type: typeLabel( row.sub ) } ) }, { value: 'id', label: 'Specific item' } ];
			( cfg.taxonomies || [] ).filter( function( x ) {
				return ( x.types || [] ).indexOf( row.sub ) >= 0;
			} ).forEach( function( x ) {
				out.push( { value: 'in/' + x.slug, label: fmt( 'In {taxonomy}', { taxonomy: x.label || x.slug } ) } );
			} );
			if ( pt && pt.hierarchical ) {
				out.push( { value: 'child_of', label: 'Child of' } );
			}
			out.push( { value: 'by_author', label: 'By author' } );
		} else if ( row.group === 'archive' && ( row.sub === 'author' || isTaxSub( row.sub ) ) ) {
			out = [
				{ value: '', label: row.sub === 'author' ? 'All authors' : fmt( 'All {taxonomy}', { taxonomy: taxLabel( row.sub ) } ) },
				{ value: 'id', label: row.sub === 'author' ? 'Specific author' : 'Specific term' },
			];
		}
		if ( ! out.length ) {
			return out;
		}
		return keepOption( out, keep, keep && keep.indexOf( 'in/' ) === 0 ? fmt( 'In {taxonomy}', { taxonomy: keep.slice( 3 ) } ) : keep );
	}

	/** The wp/v2 collection an id is searched in, with what it is: null = typed in. */
	function idSource( row ) {
		var ofType = function( slug ) {
			return cfg.restBases && cfg.restBases[ slug ] ? { base: cfg.restBases[ slug ], kind: 'post' } : null;
		};
		var ofTax = function( slug ) {
			return slug === 'category' ? { base: 'categories', kind: 'term' } : slug === 'post_tag' ? { base: 'tags', kind: 'term' } : null;
		};
		if ( row.group === 'singular' && row.sub && row.target ) {
			if ( row.target === 'id' || row.target === 'child_of' ) {
				return ofType( row.sub );
			}
			if ( row.target === 'by_author' ) {
				return { base: 'users', kind: 'user' };
			}
			if ( row.target.indexOf( 'in/' ) === 0 ) {
				return ofTax( row.target.slice( 3 ) );
			}
		}
		if ( row.group === 'archive' && row.target === 'id' ) {
			return row.sub === 'author' ? { base: 'users', kind: 'user' } : ofTax( row.sub );
		}
		return null;
	}

	function idPlaceholder( row ) {
		if ( row.target === 'by_author' || row.sub === 'author' ) {
			return 'Search users or type an ID';
		}
		if ( row.target.indexOf( 'in/' ) === 0 || ( row.group === 'archive' && row.target === 'id' ) ) {
			return 'Search terms or type an ID';
		}
		return 'Search by title or type an ID';
	}

	/* ---------- conditions dialog ---------- */

	var condDialog = document.getElementById( 'web-builder-conditions' );
	if ( ! condDialog ) {
		return;
	}
	var list = condDialog.querySelector( '.web-builder-condition-rows' );
	var prioInput = document.getElementById( 'web-builder-priority' );
	var saveBtn = condDialog.querySelector( '.web-builder-save-conditions' );
	var state = { id: 0, rows: [] };

	function select( cls, label, opts, value, onChange ) {
		var s = el( 'select', { className: cls, 'aria-label': label } );
		opts.forEach( function( o ) {
			var opt = el( 'option', { value: o.value, text: o.label } );
			if ( o.value === value ) {
				opt.selected = true;
			}
			s.appendChild( opt );
		} );
		s.addEventListener( 'change', function() {
			onChange( s.value );
		} );
		return s;
	}

	function idField( row ) {
		var src = idSource( row );
		var wrap = el( 'span', { className: 'web-builder-cond-id' } );
		var input = el( 'input', { type: 'text', autocomplete: 'off', role: 'combobox', 'aria-autocomplete': 'list', 'aria-expanded': 'false',
			'aria-label': src ? idPlaceholder( row ) : 'ID', placeholder: src ? idPlaceholder( row ) : 'ID' } );
		input.value = row.idLabel ? row.idLabel + ' (#' + row.id + ')' : row.id;
		var results = el( 'ul', { className: 'web-builder-cond-results', role: 'listbox', hidden: true } );
		var timer = 0;
		var seq = 0;
		var hide = function() {
			results.hidden = true;
			input.setAttribute( 'aria-expanded', 'false' );
		};
		var pick = function( id, label ) {
			row.id = String( id );
			row.idLabel = label || '';
			input.value = label ? label + ' (#' + id + ')' : String( id );
			hide();
			clearError( row );
		};
		input.addEventListener( 'input', function() {
			var q = input.value.trim();
			row.idLabel = '';
			if ( ID_RE.test( q ) ) {
				row.id = q;
			} else {
				row.id = '';
			}
			clearTimeout( timer );
			if ( ! src || q.length < 2 || ID_RE.test( q ) ) {
				hide();
				return;
			}
			timer = setTimeout( function() {
				var mine = ++seq;
				var fields = src.kind === 'post' ? 'id,title' : 'id,name';
				fetch( cfg.rest + 'wp/v2/' + src.base + '?per_page=10&_fields=' + fields + '&search=' + encodeURIComponent( q ), {
					credentials: 'same-origin', headers: { 'X-WP-Nonce': cfg.nonce, Accept: 'application/json' },
				} ).then( function( r ) {
					return r.ok ? r.json() : [];
				} ).then( function( items ) {
					if ( mine !== seq ) {
						return;
					}
					results.textContent = '';
					( Array.isArray( items ) ? items : [] ).forEach( function( it ) {
						var label = src.kind === 'post' ? ( it.title && ( it.title.rendered || it.title.raw ) ) || '(no title)' : it.name || '';
						var tmp = document.createElement( 'textarea' );
						tmp.innerHTML = label; // decode entities of title.rendered
						label = tmp.value;
						var li = el( 'li', { role: 'option', 'aria-selected': 'false', text: label + ' (#' + it.id + ')' } );
						li.addEventListener( 'mousedown', function( e ) {
							e.preventDefault();
							pick( it.id, label );
						} );
						results.appendChild( li );
					} );
					if ( ! results.children.length ) {
						results.appendChild( el( 'li', { className: 'is-empty', text: 'No results found.' } ) );
					}
					results.hidden = false;
					input.setAttribute( 'aria-expanded', 'true' );
				} ).catch( hide );
			}, 250 );
		} );
		input.addEventListener( 'keydown', function( e ) {
			var items = Array.prototype.slice.call( results.querySelectorAll( 'li[role="option"]' ) );
			if ( results.hidden || ! items.length ) {
				return;
			}
			var cur = items.findIndex( function( li ) {
				return li.getAttribute( 'aria-selected' ) === 'true';
			} );
			if ( e.key === 'ArrowDown' || e.key === 'ArrowUp' ) {
				e.preventDefault();
				var next = e.key === 'ArrowDown' ? Math.min( cur + 1, items.length - 1 ) : Math.max( cur - 1, 0 );
				items.forEach( function( li, i ) {
					li.setAttribute( 'aria-selected', i === next ? 'true' : 'false' );
				} );
			} else if ( e.key === 'Enter' && cur >= 0 ) {
				e.preventDefault();
				items[ cur ].dispatchEvent( new MouseEvent( 'mousedown' ) );
			} else if ( e.key === 'Escape' ) {
				e.preventDefault();
				e.stopPropagation();
				hide();
			}
		} );
		input.addEventListener( 'blur', function() {
			setTimeout( function() {
				if ( document.activeElement !== input ) { // not when the whole window lost focus
					hide();
				}
			}, 150 );
		} );
		wrap.appendChild( input );
		wrap.appendChild( results );
		return wrap;
	}

	function update( row, change ) {
		if ( 'group' in change && change.group !== row.group ) {
			Object.assign( row, change, { sub: change.group === 'user' ? 'logged_in' : '', target: '', id: '', idLabel: '' } );
		} else if ( 'sub' in change && change.sub !== row.sub ) {
			Object.assign( row, change, { target: '', id: '', idLabel: '' } );
		} else if ( 'target' in change && change.target !== row.target ) {
			Object.assign( row, change, { id: '', idLabel: '' } );
		} else {
			Object.assign( row, change );
		}
		delete row.error;
		render();
	}

	function clearError( row ) {
		if ( row.error ) {
			delete row.error;
			render();
		}
	}

	function render() {
		var focused = document.activeElement && list.contains( document.activeElement ) ? document.activeElement.getAttribute( 'data-focus' ) : null;
		list.textContent = '';
		state.rows.forEach( function( row, i ) {
			var li = el( 'li', { className: 'web-builder-condition-row is-' + row.mode + ( row.error ? ' has-error' : '' ) } );
			var mode = select( 'web-builder-cond-mode', 'Include or exclude', [ { value: 'include', label: 'Include' }, { value: 'exclude', label: 'Exclude' } ], row.mode, function( v ) {
				update( row, { mode: v } );
			} );
			mode.setAttribute( 'data-focus', i + ':mode' );
			li.appendChild( mode );
			var group = select( 'web-builder-cond-group', 'Where', groupOptions(), row.group, function( v ) {
				update( row, { group: v } );
			} );
			group.setAttribute( 'data-focus', i + ':group' );
			li.appendChild( group );
			var subs = subOptions( row.group, row.sub );
			if ( subs.length ) {
				var sub = select( 'web-builder-cond-sub', 'Which', subs, row.sub, function( v ) {
					update( row, { sub: v } );
				} );
				sub.setAttribute( 'data-focus', i + ':sub' );
				li.appendChild( sub );
			}
			var targets = targetOptions( row, row.target );
			if ( targets.length ) {
				var target = select( 'web-builder-cond-target', 'Narrow down', targets, row.target, function( v ) {
					update( row, { target: v } );
				} );
				target.setAttribute( 'data-focus', i + ':target' );
				li.appendChild( target );
			}
			var needsId = row.target !== '' && ! ( row.group === 'singular' && row.sub === 'front_page' );
			if ( needsId ) {
				li.appendChild( idField( row ) );
			}
			var rm = el( 'button', { type: 'button', className: 'button-link button-link-delete web-builder-cond-remove', 'aria-label': 'Remove this condition' }, [ el( 'span', { className: 'dashicons dashicons-trash', 'aria-hidden': 'true' } ) ] );
			rm.addEventListener( 'click', function() {
				state.rows.splice( i, 1 );
				render();
			} );
			li.appendChild( rm );
			if ( row.error ) {
				li.appendChild( el( 'p', { className: 'web-builder-cond-error', role: 'alert', text: row.error } ) );
			}
			list.appendChild( li );
		} );
		if ( ! state.rows.length ) {
			list.appendChild( el( 'li', { className: 'description', text: 'No conditions: the template is not shown anywhere. Add a condition to choose where it appears.' } ) );
		}
		if ( focused ) {
			var f = list.querySelector( '[data-focus="' + focused + '"]' );
			if ( f ) {
				f.focus();
			}
		}
	}

	function openConditions( tr ) {
		var rules = [];
		var labels = {};
		try {
			rules = JSON.parse( tr.getAttribute( 'data-conditions' ) || '[]' ) || [];
			labels = JSON.parse( tr.getAttribute( 'data-labels' ) || '{}' ) || {};
		} catch ( e ) {}
		state.id = Number( tr.getAttribute( 'data-id' ) );
		state.rows = [];
		rules.forEach( function( r ) {
			var row = parseRule( r );
			if ( row ) {
				row.idLabel = labels[ r ] || '';
				state.rows.push( row );
			}
		} );
		condDialog.querySelector( '.web-builder-conditions-name' ).textContent = tr.getAttribute( 'data-title' ) || '';
		prioInput.value = tr.getAttribute( 'data-priority' ) || '0';
		saveBtn.disabled = false;
		showError( condDialog, '' );
		render();
		openDialog( condDialog );
		var first = list.querySelector( 'select, input' );
		( first || condDialog.querySelector( '.web-builder-add-condition' ) ).focus();
	}

	document.querySelectorAll( '.web-builder-edit-conditions' ).forEach( function( b ) {
		b.addEventListener( 'click', function() {
			openConditions( b.closest( 'tr' ) );
		} );
	} );

	condDialog.querySelector( '.web-builder-add-condition' ).addEventListener( 'click', function() {
		state.rows.push( emptyRow( state.rows.length ? 'exclude' : 'include' ) );
		render();
		var rows = list.querySelectorAll( '.web-builder-condition-row' );
		if ( rows.length ) {
			rows[ rows.length - 1 ].querySelector( 'select' ).focus();
		}
	} );

	condDialog.querySelector( 'form' ).addEventListener( 'submit', function( e ) {
		e.preventDefault();
		var rules = [];
		var bad = false;
		state.rows.forEach( function( row ) {
			delete row.error;
			var r = rowRule( row );
			if ( r === null ) {
				row.error = row.target && ! ID_RE.test( row.id ) ? 'Choose an item, or pick “All”.' : 'This condition is not valid.';
				bad = true;
				return;
			}
			if ( rules.indexOf( r ) < 0 ) {
				rules.push( r );
			}
		} );
		if ( rules.length > cfg.limits.maxConditions ) {
			showError( condDialog, fmt( 'At most {n} conditions.', { n: cfg.limits.maxConditions } ) );
			bad = true;
		}
		var prio = Number( prioInput.value );
		if ( ! Number.isFinite( prio ) || Math.trunc( prio ) !== prio || prio < cfg.limits.priorityMin || prio > cfg.limits.priorityMax ) {
			showError( condDialog, fmt( 'The priority is a whole number from {min} to {max}.', { min: cfg.limits.priorityMin, max: cfg.limits.priorityMax } ) );
			bad = true;
		}
		if ( bad ) {
			render();
			return;
		}
		saveBtn.disabled = true;
		showError( condDialog, '' );
		api( 'PUT', cfg.namespace + '/templates/' + state.id + '/conditions', { conditions: rules, priority: prio } ).then( function( res ) {
			if ( res.ok ) {
				location.reload();
				return;
			}
			saveBtn.disabled = false;
			var errs = res.data && res.data.data && res.data.data.errors;
			if ( Array.isArray( errs ) ) {
				errs.forEach( function( er ) {
					var m = /^\/conditions\/(\d+)$/.exec( er.path || '' );
					if ( m ) {
						// The server's index is into the rules sent; rows map 1:1 unless duplicates were dropped.
						var rule = rules[ Number( m[ 1 ] ) ];
						state.rows.forEach( function( row ) {
							if ( rowRule( row ) === rule ) {
								row.error = er.message || 'This condition is not valid.';
							}
						} );
					}
				} );
				render();
			}
			showError( condDialog, errorText( res ) );
		}, function() {
			saveBtn.disabled = false;
			showError( condDialog, errorText( null ) );
		} );
	} );

	/* ---------- import a template file ---------- */

	var importBtn = document.getElementById( 'web-builder-import-template-button' );
	var importFile = document.getElementById( 'web-builder-import-template-file' );
	if ( importBtn && importFile ) {
		importBtn.addEventListener( 'click', function() {
			importFile.value = '';
			importFile.click();
		} );
		importFile.addEventListener( 'change', function() {
			var file = importFile.files && importFile.files[ 0 ];
			if ( ! file ) {
				return;
			}
			var failed = function( msg ) {
				importBtn.disabled = false;
				window.alert( msg );
			};
			importBtn.disabled = true;
			file.text().then( function( text ) {
				var data;
				try {
					data = JSON.parse( text );
				} catch ( err ) {
					failed( 'This is not a Pursae Web Builder template file.' );
					return;
				}
				api( 'POST', cfg.namespace + '/templates/import', data ).then( function( res ) {
					if ( res.ok && res.data && res.data.id ) {
						var u = new URL( location.href );
						u.search = '?page=' + encodeURIComponent( cfg.page || 'web-builder-templates' ) + '&imported=' + encodeURIComponent( res.data.id );
						location.href = u.toString();
						return;
					}
					failed( errorText( res ) );
				}, function() {
					failed( errorText( null ) );
				} );
			}, function() {
				failed( 'The file could not be read.' );
			} );
		} );
	}

	/* ---------- confirm links ---------- */

	document.addEventListener( 'click', function( e ) {
		var a = e.target.closest && e.target.closest( '.web-builder-confirm' );
		if ( a && ! window.confirm( a.getAttribute( 'data-confirm' ) || 'Are you sure?' ) ) {
			e.preventDefault();
		}
	} );
	var bulkForm = document.getElementById( 'posts-filter' );
	if ( bulkForm ) {
		bulkForm.addEventListener( 'submit', function( e ) {
			var sub = e.submitter;
			if ( ! sub || sub.name !== 'bulk_action' ) {
				return;
			}
			var sel = document.getElementById( sub.id === 'doaction2' ? 'bulk-action-selector-bottom' : 'bulk-action-selector-top' );
			if ( sel && sel.value === 'delete' && ! window.confirm( 'You are about to permanently delete the selected templates. This cannot be undone.' ) ) {
				e.preventDefault();
			}
		} );
	}
}() );
