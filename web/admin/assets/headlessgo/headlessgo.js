/* HeadlessGo admin: the entry form of the Content Manager and the Content-Type Builder. Both are
 * rendered from a JSON model printed by the server (#hg-editor-model, #hg-builder-model) and post
 * their state as JSON in a hidden field. */
( function () {
	'use strict';

	function el( tag, attrs, kids ) {
		var n = document.createElement( tag );
		if ( attrs ) {
			Object.keys( attrs ).forEach( function ( k ) {
				var v = attrs[ k ];
				if ( v === null || v === undefined || v === false ) {
					return;
				}
				if ( k === 'text' ) {
					n.textContent = v;
				} else if ( k === 'class' ) {
					n.className = v;
				} else if ( k.indexOf( 'on' ) === 0 && typeof v === 'function' ) {
					n.addEventListener( k.slice( 2 ), v );
				} else if ( v === true ) {
					n.setAttribute( k, '' );
				} else {
					n.setAttribute( k, v );
				}
			} );
		}
		( kids || [] ).forEach( function ( c ) {
			if ( c === null || c === undefined || c === false ) {
				return;
			}
			n.appendChild( typeof c === 'string' ? document.createTextNode( c ) : c );
		} );
		return n;
	}

	function btn( label, cls, fn, title ) {
		return el( 'button', { type: 'button', class: 'button ' + ( cls || '' ), title: title || null, 'aria-label': title || null, onclick: function ( e ) {
			e.preventDefault();
			fn( e );
		} }, [ label ] );
	}

	function clone( v ) {
		return v === undefined ? undefined : JSON.parse( JSON.stringify( v ) );
	}

	function slugify( v ) {
		var map = { 'ç': 'c', 'ğ': 'g', 'ı': 'i', 'ö': 'o', 'ş': 's', 'ü': 'u', 'ä': 'a', 'é': 'e', 'è': 'e', 'á': 'a', 'à': 'a', 'ñ': 'n', 'ß': 'ss' };
		return String( v || '' ).toLowerCase().replace( /[çğıöşüäéèáàñß]/g, function ( c ) {
			return map[ c ];
		} ).replace( /[^a-z0-9]+/g, '-' ).replace( /^-+|-+$/g, '' );
	}

	function move( list, i, d ) {
		var j = i + d;
		if ( j < 0 || j >= list.length ) {
			return;
		}
		var t = list[ i ];
		list[ i ] = list[ j ];
		list[ j ] = t;
	}

	/* ===================================== Entry form ===================================== */

	function initEditor( model ) {
		var root = document.getElementById( 'hg-editor' );
		var form = document.getElementById( 'hg-entry-form' );
		if ( ! root || ! form ) {
			return;
		}
		var state = clone( model.data ) || {};
		var errors = {};
		( model.errors || [] ).forEach( function ( e ) {
			errors[ ( e.path || [] ).join( '.' ) ] = e.message;
		} );
		var jsonBad = {};

		function applyDefaults( schema, obj ) {
			( schema.attributes || [] ).forEach( function ( f ) {
				if ( obj[ f.name ] === undefined && f.default !== undefined && f.default !== null && f.default !== '' ) {
					obj[ f.name ] = f.type === 'number' && f.format !== 'biginteger' ? Number( f.default ) : f.default;
				}
			} );
			return obj;
		}
		if ( model.isNew ) {
			applyDefaults( model.schema, state );
		}

		function hint( f ) {
			var h = [];
			if ( f.min !== undefined && f.min !== null ) {
				h.push( 'min ' + f.min );
			}
			if ( f.max !== undefined && f.max !== null ) {
				h.push( 'max ' + f.max );
			}
			if ( f.unique ) {
				h.push( 'unique' );
			}
			if ( f.private ) {
				h.push( 'private: not in the API' );
			}
			if ( f.regex ) {
				h.push( 'pattern ' + f.regex );
			}
			return h.join( ' · ' );
		}

		function renderObject( schema, obj, box, path ) {
			( schema.attributes || [] ).forEach( function ( f ) {
				var p = path.concat( [ f.name ] );
				var key = p.join( '.' );
				var wrap = el( 'div', { class: 'hg-field hg-field-' + f.type + ( errors[ key ] ? ' hg-has-error' : '' ), 'data-path': key } );
				var id = 'hg-f-' + key.replace( /[^a-zA-Z0-9]/g, '-' );
				var label = el( 'label', { class: 'hg-label', for: id }, [ el( 'span', { translate: 'no', text: f.name } ), f.required ? el( 'span', { class: 'hg-req', text: ' *' } ) : null,
					el( 'span', { class: 'hg-type', text: typeLabel( f ) } ) ] );
				wrap.appendChild( label );
				wrap.appendChild( control( schema, f, obj, id, p ) );
				var h = hint( f );
				if ( h ) {
					wrap.appendChild( el( 'p', { class: 'description', text: h } ) );
				}
				if ( errors[ key ] ) {
					wrap.appendChild( el( 'p', { class: 'hg-error', text: errors[ key ] } ) );
				}
				box.appendChild( wrap );
			} );
		}

		function typeLabel( f ) {
			switch ( f.type ) {
				case 'relation':
					return 'Relation · ' + f.relation + ' · ' + targetName( f.target ) + ( f.mappedBy ? ' · inverse of ' + targetName( f.target ) + '.' + f.mappedBy : '' );
				case 'component':
					return 'Component' + ( f.repeatable ? ' (repeatable)' : '' );
				case 'dynamiczone':
					return 'Dynamic zone';
				case 'media':
					return f.multiple ? 'Media (multiple)' : 'Media';
				case 'richtext':
					return 'Rich text (' + f.format + ')';
				case 'number':
					return 'Number (' + f.format + ')';
				case 'datetime':
					return 'Date and time (UTC)';
			}
			return f.type.charAt( 0 ).toUpperCase() + f.type.slice( 1 );
		}

		function targetName( t ) {
			if ( t === 'wp:post' ) {
				return 'Posts';
			}
			if ( t === 'wp:page' ) {
				return 'Pages';
			}
			if ( t === 'wp:user' ) {
				return 'Users';
			}
			return ( model.types[ t ] && model.types[ t ].displayName ) || t;
		}

		function setVal( obj, name, v ) {
			if ( v === '' || v === undefined ) {
				obj[ name ] = null;
			} else {
				obj[ name ] = v;
			}
		}

		function control( schema, f, obj, id, path ) {
			var v = obj[ f.name ];
			switch ( f.type ) {
				case 'text':
				case 'email':
					if ( f.long ) {
						return el( 'textarea', { id: id, class: 'large-text', rows: 4, translate: 'no', oninput: function ( e ) {
							setVal( obj, f.name, e.target.value );
						} }, [ v || '' ] );
					}
					return el( 'input', { id: id, type: f.type === 'email' ? 'email' : 'text', class: 'large-text', value: v || '', translate: 'no', oninput: function ( e ) {
						setVal( obj, f.name, e.target.value );
					} } );
				case 'uid':
					var inp = el( 'input', { id: id, type: 'text', class: 'regular-text code', value: v || '', translate: 'no', oninput: function ( e ) {
						setVal( obj, f.name, e.target.value );
					} } );
					return el( 'div', { class: 'hg-row' }, [ inp, f.targetField ? btn( 'Regenerate', 'button-small', function () {
						inp.value = slugify( obj[ f.targetField ] );
						setVal( obj, f.name, inp.value );
					}, 'Regenerate from ' + f.targetField ) : null ] );
				case 'password':
					var isSet = !! v;
					obj[ f.name ] = '';
					return el( 'input', { id: id, type: 'password', class: 'regular-text', autocomplete: 'new-password', placeholder: isSet ? 'Set · leave empty to keep it' : '', oninput: function ( e ) {
						obj[ f.name ] = e.target.value;
					} } );
				case 'richtext':
					if ( f.format === 'markdown' ) {
						return el( 'textarea', { id: id, class: 'large-text code hg-markdown', rows: 10, translate: 'no', oninput: function ( e ) {
							setVal( obj, f.name, e.target.value );
						} }, [ v || '' ] );
					}
					return blocksEditor( f, obj, id );
				case 'number':
					return el( 'input', { id: id, type: f.format === 'biginteger' ? 'text' : 'number', step: f.format === 'integer' ? '1' : 'any', class: 'regular-text', value: v === null || v === undefined ? '' : v, oninput: function ( e ) {
						var s = e.target.value.trim();
						if ( s === '' ) {
							obj[ f.name ] = null;
						} else {
							obj[ f.name ] = f.format === 'biginteger' ? s : Number( s );
						}
					} } );
				case 'boolean':
					var cb = el( 'input', { id: id, type: 'checkbox', checked: v === true, onchange: function ( e ) {
						obj[ f.name ] = e.target.checked;
					} } );
					return el( 'label', { class: 'hg-switch' }, [ cb, v === null || v === undefined ? ' (not set)' : '' ] );
				case 'date':
					return el( 'input', { id: id, type: 'date', value: v || '', onchange: function ( e ) {
						setVal( obj, f.name, e.target.value );
					} } );
				case 'datetime':
					return el( 'input', { id: id, type: 'datetime-local', step: 1, value: v ? String( v ).replace( /(\.\d+)?Z$/, '' ) : '', onchange: function ( e ) {
						setVal( obj, f.name, e.target.value ? e.target.value + ( e.target.value.length === 16 ? ':00' : '' ) + 'Z' : '' );
					} } );
				case 'time':
					return el( 'input', { id: id, type: 'time', step: 1, value: v ? String( v ).replace( /\.\d+$/, '' ) : '', onchange: function ( e ) {
						setVal( obj, f.name, e.target.value );
					} } );
				case 'enumeration':
					var sel = el( 'select', { id: id, onchange: function ( e ) {
						setVal( obj, f.name, e.target.value );
					} }, [ el( 'option', { value: '', text: '— Choose —' } ) ] );
					( f.enum || [] ).forEach( function ( o ) {
						sel.appendChild( el( 'option', { value: o, selected: v === o, translate: 'no', text: o } ) );
					} );
					return sel;
				case 'json':
					var ta = el( 'textarea', { id: id, class: 'large-text code', rows: 6, translate: 'no', oninput: function ( e ) {
						var s = e.target.value.trim();
						var key = path.join( '.' );
						if ( s === '' ) {
							obj[ f.name ] = null;
							delete jsonBad[ key ];
							ta.classList.remove( 'hg-invalid' );
							return;
						}
						try {
							obj[ f.name ] = JSON.parse( s );
							delete jsonBad[ key ];
							ta.classList.remove( 'hg-invalid' );
						} catch ( err ) {
							jsonBad[ key ] = true;
							ta.classList.add( 'hg-invalid' );
						}
					} }, [ v === null || v === undefined ? '' : JSON.stringify( v, null, 2 ) ] );
					return ta;
				case 'media':
					return mediaControl( f, obj );
				case 'relation':
					return relationControl( f, obj, id );
				case 'component':
					return componentControl( f, obj, path );
				case 'dynamiczone':
					return zoneControl( f, obj, path );
			}
			return el( 'span', { text: 'Unsupported field type ' + f.type } );
		}

		/* Rich text blocks: one item per block; untouched blocks keep their inline formatting. */
		function blockText( b ) {
			var out = '';
			( function walk( nodes ) {
				( nodes || [] ).forEach( function ( n ) {
					if ( n.type === 'text' ) {
						out += n.text || '';
					} else if ( n.type === 'list-item' ) {
						walk( n.children );
						out += '\n';
					} else {
						walk( n.children );
					}
				} );
			}( b.children ) );
			return out.replace( /\n$/, '' );
		}

		function buildBlock( it ) {
			if ( ! it.dirty && it.orig ) {
				return it.orig;
			}
			var text = function ( t ) {
				return [ { type: 'text', text: t } ];
			};
			switch ( it.kind ) {
				case 'heading':
					return { type: 'heading', level: it.level || 2, children: text( it.text ) };
				case 'quote':
					return { type: 'quote', children: text( it.text ) };
				case 'code':
					return { type: 'code', children: text( it.text ) };
				case 'list-ordered':
				case 'list-unordered':
					return { type: 'list', format: it.kind === 'list-ordered' ? 'ordered' : 'unordered', children: it.text.split( '\n' ).filter( function ( l ) {
						return l.trim() !== '';
					} ).map( function ( l ) {
						return { type: 'list-item', children: text( l ) };
					} ) };
				case 'image':
					return { type: 'image', image: it.image || {}, children: text( '' ) };
			}
			return { type: 'paragraph', children: text( it.text ) };
		}

		function blocksEditor( f, obj ) {
			var items = ( Array.isArray( obj[ f.name ] ) ? obj[ f.name ] : [] ).map( function ( b ) {
				var kind = b.type;
				if ( b.type === 'list' ) {
					kind = b.format === 'ordered' ? 'list-ordered' : 'list-unordered';
				}
				return { orig: b, kind: kind, level: b.level, text: blockText( b ), image: b.image };
			} );
			var box = el( 'div', { class: 'hg-blocks' } );
			function sync() {
				var keep = items.filter( function ( it ) {
					return it.kind === 'image' ? !! ( it.image && it.image.url ) : ( it.text || '' ).trim() !== '' || ( it.orig && ! it.dirty );
				} );
				obj[ f.name ] = keep.length ? keep.map( buildBlock ) : null;
			}
			function render() {
				box.innerHTML = '';
				items.forEach( function ( it, i ) {
					var kindSel = el( 'select', { 'aria-label': 'Block type', onchange: function ( e ) {
						var v = e.target.value;
						it.dirty = true;
						if ( v.indexOf( 'h' ) === 0 && v.length === 2 ) {
							it.kind = 'heading';
							it.level = Number( v.charAt( 1 ) );
						} else {
							it.kind = v;
						}
						sync();
						render();
					} } );
					[ [ 'paragraph', 'Paragraph' ], [ 'h1', 'Heading 1' ], [ 'h2', 'Heading 2' ], [ 'h3', 'Heading 3' ], [ 'h4', 'Heading 4' ], [ 'h5', 'Heading 5' ], [ 'h6', 'Heading 6' ],
						[ 'quote', 'Quote' ], [ 'code', 'Code' ], [ 'list-unordered', 'Bulleted list' ], [ 'list-ordered', 'Numbered list' ], [ 'image', 'Image' ] ].forEach( function ( o ) {
						var cur = it.kind === 'heading' ? 'h' + ( it.level || 2 ) : it.kind;
						kindSel.appendChild( el( 'option', { value: o[ 0 ], selected: cur === o[ 0 ], text: o[ 1 ] } ) );
					} );
					var body;
					if ( it.kind === 'image' ) {
						var img = it.image && it.image.url ? el( 'img', { src: it.image.url, alt: '', class: 'hg-block-img' } ) : null;
						body = el( 'div', { class: 'hg-row' }, [ img, btn( it.image && it.image.url ? 'Replace image' : 'Choose image', 'button-small', function () {
							pickMedia( false, 'image', function ( list ) {
								var a = list[ 0 ];
								it.dirty = true;
								it.image = { url: a.url, alternativeText: a.alt || null, width: a.width || null, height: a.height || null, name: a.filename, mime: a.mime };
								sync();
								render();
							} );
						} ) ] );
					} else {
						body = el( 'textarea', { class: 'large-text' + ( it.kind === 'code' ? ' code' : '' ), rows: it.kind.indexOf( 'list' ) === 0 ? 4 : 2, translate: 'no',
							placeholder: it.kind.indexOf( 'list' ) === 0 ? 'One item per line' : '', oninput: function ( e ) {
								it.text = e.target.value;
								it.dirty = true;
								sync();
							} }, [ it.text || '' ] );
					}
					box.appendChild( el( 'div', { class: 'hg-block' }, [ el( 'div', { class: 'hg-block-bar' }, [ kindSel,
						btn( '↑', 'button-small', function () {
							move( items, i, -1 );
							sync();
							render();
						}, 'Move up' ),
						btn( '↓', 'button-small', function () {
							move( items, i, 1 );
							sync();
							render();
						}, 'Move down' ),
						btn( '×', 'button-small hg-del', function () {
							items.splice( i, 1 );
							sync();
							render();
						}, 'Remove block' ) ] ), body ] ) );
				} );
				box.appendChild( btn( '+ Add block', 'button-small', function () {
					items.push( { kind: 'paragraph', text: '', dirty: true } );
					sync();
					render();
				} ) );
			}
			render();
			return box;
		}

		/* Media */
		function pickMedia( multiple, type, cb ) {
			if ( ! window.wp || ! wp.media ) {
				window.alert( 'The media library is not available.' );
				return;
			}
			var lib = {};
			if ( type ) {
				lib.type = type;
			}
			var frame = wp.media( { title: multiple ? 'Select files' : 'Select a file', multiple: multiple ? 'add' : false, library: lib, button: { text: 'Select' } } );
			frame.on( 'select', function () {
				cb( frame.state().get( 'selection' ).map( function ( a ) {
					return a.toJSON();
				} ) );
			} );
			frame.open();
		}

		function mediaControl( f, obj ) {
			var box = el( 'div', { class: 'hg-media' } );
			function ids() {
				var v = obj[ f.name ];
				return Array.isArray( v ) ? v.slice() : ( v ? [ v ] : [] );
			}
			function render() {
				box.innerHTML = '';
				var list = ids();
				var grid = el( 'div', { class: 'hg-media-grid' } );
				list.forEach( function ( id, i ) {
					var m = model.media[ String( id ) ] || { title: '#' + id };
					grid.appendChild( el( 'div', { class: 'hg-media-item' }, [
						m.thumb ? el( 'img', { src: m.thumb, alt: '' } ) : el( 'span', { class: 'dashicons dashicons-media-default' } ),
						el( 'span', { class: 'hg-media-title', translate: 'no', text: m.title } ),
						el( 'span', { class: 'hg-media-tools' }, [
							f.multiple ? btn( '←', 'button-small', function () {
								move( list, i, -1 );
								obj[ f.name ] = list;
								render();
							}, 'Move left' ) : null,
							f.multiple ? btn( '→', 'button-small', function () {
								move( list, i, 1 );
								obj[ f.name ] = list;
								render();
							}, 'Move right' ) : null,
							btn( '×', 'button-small hg-del', function () {
								list.splice( i, 1 );
								obj[ f.name ] = list.length ? list : null;
								render();
							}, 'Remove' ) ] ) ] ) );
				} );
				box.appendChild( grid );
				var type = ( f.allowedTypes || [] ).length === 1 ? { images: 'image', videos: 'video', audios: 'audio' }[ f.allowedTypes[ 0 ] ] : null;
				box.appendChild( btn( f.multiple ? 'Add files' : ( list.length ? 'Replace' : 'Choose a file' ), 'button-small', function () {
					pickMedia( f.multiple, type, function ( sel ) {
						sel.forEach( function ( a ) {
							model.media[ String( a.id ) ] = { id: a.id, url: a.url, title: a.title || a.filename, thumb: a.type === 'image' ? ( ( a.sizes && a.sizes.thumbnail && a.sizes.thumbnail.url ) || a.url ) : '' };
						} );
						var add = sel.map( function ( a ) {
							return a.id;
						} );
						obj[ f.name ] = f.multiple ? list.concat( add.filter( function ( x ) {
							return list.indexOf( x ) < 0;
						} ) ) : add.slice( 0, 1 );
						render();
					} );
				} ) );
			}
			render();
			return box;
		}

		/* Relations */
		function relationControl( f, obj, id ) {
			var multi = f.relation === 'oneToMany' || f.relation === 'manyToMany';
			var box = el( 'div', { class: 'hg-rel' } );
			var chips = el( 'ul', { class: 'hg-chips' } );
			var results = el( 'ul', { class: 'hg-results', hidden: true } );
			var timer;
			function list() {
				var v = obj[ f.name ];
				return Array.isArray( v ) ? v.slice() : ( v !== null && v !== undefined ? [ v ] : [] );
			}
			function label( x ) {
				return model.relations[ f.target + '|' + x ] || String( x );
			}
			function renderChips() {
				chips.innerHTML = '';
				var l = list();
				l.forEach( function ( x, i ) {
					chips.appendChild( el( 'li', {}, [ el( 'span', { translate: 'no', text: label( x ) } ),
						multi ? btn( '↑', 'button-small', function () {
							move( l, i, -1 );
							obj[ f.name ] = l;
							renderChips();
						}, 'Move up' ) : null,
						btn( '×', 'button-small hg-del', function () {
							l.splice( i, 1 );
							obj[ f.name ] = l.length ? l : null;
							renderChips();
						}, 'Remove' ) ] ) );
				} );
			}
			var search = el( 'input', { id: id, type: 'search', class: 'regular-text', placeholder: 'Search ' + targetName( f.target ) + '…', autocomplete: 'off', oninput: function () {
				clearTimeout( timer );
				timer = setTimeout( run, 250 );
			}, onfocus: function () {
				run();
			} } );
			function run() {
				var u = model.searchURL + '?target=' + encodeURIComponent( f.target ) + '&q=' + encodeURIComponent( search.value ) + '&locale=' + encodeURIComponent( model.locale || '' );
				fetch( u, { credentials: 'same-origin' } ).then( function ( r ) {
					return r.json();
				} ).then( function ( rows ) {
					results.innerHTML = '';
					rows.forEach( function ( row ) {
						results.appendChild( el( 'li', {}, [ el( 'button', { type: 'button', class: 'button-link', onclick: function ( e ) {
							e.preventDefault();
							model.relations[ f.target + '|' + row.id ] = row.label;
							var l = multi ? list() : [];
							if ( l.indexOf( row.id ) < 0 ) {
								l.push( row.id );
							}
							obj[ f.name ] = l;
							results.hidden = true;
							search.value = '';
							renderChips();
						} }, [ el( 'span', { translate: 'no', text: row.label } ), row.status ? el( 'span', { class: 'hg-type', text: row.status } ) : null ] ) ] ) );
					} );
					if ( ! rows.length ) {
						results.appendChild( el( 'li', { class: 'description', text: 'Nothing found.' } ) );
					}
					results.hidden = false;
				} );
			}
			document.addEventListener( 'click', function ( e ) {
				if ( ! box.contains( e.target ) ) {
					results.hidden = true;
				}
			} );
			renderChips();
			box.appendChild( chips );
			box.appendChild( search );
			box.appendChild( results );
			return box;
		}

		/* Components and dynamic zones */
		function titleOf( schema, item ) {
			var tf = null;
			( schema.attributes || [] ).some( function ( f ) {
				if ( f.type === 'text' && ! f.long ) {
					tf = f.name;
					return true;
				}
				return false;
			} );
			return tf && item[ tf ] ? String( item[ tf ] ) : '';
		}

		function componentControl( f, obj, path ) {
			var comp = model.components[ f.component ];
			var box = el( 'div', { class: 'hg-comp' } );
			if ( ! comp ) {
				return el( 'p', { text: 'Unknown component ' + f.component } );
			}
			function render() {
				box.innerHTML = '';
				if ( ! f.repeatable ) {
					var v = obj[ f.name ];
					if ( ! v || typeof v !== 'object' ) {
						box.appendChild( btn( '+ Add ' + comp.displayName, 'button-small', function () {
							obj[ f.name ] = applyDefaults( comp, {} );
							render();
						} ) );
						return;
					}
					var inner = el( 'div', { class: 'hg-comp-body' } );
					renderObject( comp, v, inner, path );
					box.appendChild( el( 'div', { class: 'hg-comp-item' }, [ el( 'div', { class: 'hg-comp-head' }, [ el( 'strong', { translate: 'no', text: comp.displayName } ),
						btn( 'Remove', 'button-small hg-del', function () {
							obj[ f.name ] = null;
							render();
						} ) ] ), inner ] ) );
					return;
				}
				var list = Array.isArray( obj[ f.name ] ) ? obj[ f.name ] : [];
				obj[ f.name ] = list;
				list.forEach( function ( item, i ) {
					box.appendChild( itemBox( comp, item, list, i, path.concat( [ String( i ) ] ), render ) );
				} );
				box.appendChild( btn( '+ Add an entry', 'button-small', function () {
					list.push( applyDefaults( comp, {} ) );
					render();
				} ) );
			}
			render();
			return box;
		}

		function itemBox( comp, item, list, i, path, render ) {
			var inner = el( 'div', { class: 'hg-comp-body' } );
			renderObject( comp, item, inner, path );
			var collapsed = false;
			var head = el( 'div', { class: 'hg-comp-head' }, [
				el( 'button', { type: 'button', class: 'button-link hg-toggle', 'aria-expanded': 'true', onclick: function ( e ) {
					e.preventDefault();
					collapsed = ! collapsed;
					inner.hidden = collapsed;
					e.currentTarget.setAttribute( 'aria-expanded', collapsed ? 'false' : 'true' );
				} }, [ el( 'strong', { translate: 'no', text: ( i + 1 ) + '. ' + comp.displayName } ), el( 'span', { class: 'hg-type', translate: 'no', text: titleOf( comp, item ) } ) ] ),
				el( 'span', { class: 'hg-comp-tools' }, [
					btn( '↑', 'button-small', function () {
						move( list, i, -1 );
						render();
					}, 'Move up' ),
					btn( '↓', 'button-small', function () {
						move( list, i, 1 );
						render();
					}, 'Move down' ),
					btn( '×', 'button-small hg-del', function () {
						list.splice( i, 1 );
						render();
					}, 'Remove' ) ] ) ] );
			return el( 'div', { class: 'hg-comp-item' }, [ head, inner ] );
		}

		function zoneControl( f, obj, path ) {
			var box = el( 'div', { class: 'hg-zone' } );
			function render() {
				box.innerHTML = '';
				var list = Array.isArray( obj[ f.name ] ) ? obj[ f.name ] : [];
				obj[ f.name ] = list;
				list.forEach( function ( item, i ) {
					var comp = model.components[ item.__component ];
					if ( comp ) {
						box.appendChild( itemBox( comp, item, list, i, path.concat( [ String( i ) ] ), render ) );
					}
				} );
				var sel = el( 'select', { 'aria-label': 'Component to add' } );
				( f.components || [] ).forEach( function ( cu ) {
					var c = model.components[ cu ];
					sel.appendChild( el( 'option', { value: cu, translate: 'no', text: c ? c.displayName + ' (' + cu + ')' : cu } ) );
				} );
				box.appendChild( el( 'div', { class: 'hg-row' }, [ sel, btn( '+ Add a component', 'button-small', function () {
					var c = model.components[ sel.value ];
					if ( c ) {
						var item = applyDefaults( c, {} );
						item.__component = sel.value;
						list.push( item );
						render();
					}
				} ) ] ) );
			}
			render();
			return box;
		}

		root.innerHTML = '';
		renderObject( model.schema, state, root, [] );
		var publish = document.getElementById( 'hg-publish' );
		form.querySelectorAll( '[data-hg-publish]' ).forEach( function ( b ) {
			b.addEventListener( 'click', function () {
				publish.value = b.getAttribute( 'data-hg-publish' ) === '1' ? '1' : '';
			} );
		} );
		form.addEventListener( 'submit', function ( e ) {
			if ( Object.keys( jsonBad ).length ) {
				e.preventDefault();
				window.alert( 'A JSON field is not valid: ' + Object.keys( jsonBad ).join( ', ' ) );
				return;
			}
			document.getElementById( 'hg-payload' ).value = JSON.stringify( state );
		} );
		var firstErr = root.querySelector( '.hg-has-error' );
		if ( firstErr ) {
			firstErr.scrollIntoView( { block: 'center' } );
		}
	}

	/* ===================================== Builder ===================================== */

	var stringTypes = [ 'text', 'email', 'uid', 'enumeration', 'date', 'datetime', 'time' ];
	var complexTypes = [ 'media', 'relation', 'component', 'dynamiczone' ];

	function convKind( f ) {
		if ( f.type === 'richtext' ) {
			return f.format === 'markdown' ? 'string' : 'blocks';
		}
		if ( stringTypes.indexOf( f.type ) >= 0 ) {
			return 'string';
		}
		if ( complexTypes.indexOf( f.type ) >= 0 ) {
			return 'complex';
		}
		return f.type; // number, boolean, json, password
	}

	function isMulti( f ) {
		return f.relation === 'oneToMany' || f.relation === 'manyToMany';
	}

	/* What a change of a field does to the values stored in the entries ('' when nothing is lost);
	 * the server converts them (headlessgo_convert.go). */
	function lossy( from, to ) {
		if ( ! from || from.mappedBy || to.mappedBy ) {
			return '';
		}
		if ( from.type === to.type ) {
			switch ( to.type ) {
				case 'number':
					if ( ( to.format === 'integer' || to.format === 'biginteger' ) && ( from.format === 'decimal' || from.format === 'float' ) ) {
						return 'Decimals are rounded to whole numbers.';
					}
					return '';
				case 'richtext':
					return from.format === 'blocks' && to.format === 'markdown' ? 'The formatting of the blocks is lost; their text is kept.' : '';
				case 'media':
					return from.multiple && ! to.multiple ? 'Only the first file of each entry is kept.' : '';
				case 'relation':
					if ( from.target !== to.target ) {
						return 'Every link is removed: the relation points to another type.';
					}
					return isMulti( from ) && ! isMulti( to ) ? 'Only the first related entry of each entry is kept.' : '';
				case 'enumeration':
					var renamed = to.enumRenames || {};
					var gone = ( from.enum || [] ).filter( function ( v ) {
						return ( to.enum || [] ).indexOf( v ) < 0 && ! renamed[ v ];
					} );
					return gone.length ? 'Entries with the removed values (' + gone.join( ', ' ) + ') lose them.' : '';
				case 'component':
					if ( from.component !== to.component ) {
						return 'Every value is removed: it is another component.';
					}
					return from.repeatable && ! to.repeatable ? 'Only the first item of each entry is kept.' : '';
				case 'dynamiczone':
					var dropped = ( from.components || [] ).filter( function ( c ) {
						return ( to.components || [] ).indexOf( c ) < 0;
					} );
					return dropped.length ? 'Items of the removed components (' + dropped.join( ', ' ) + ') are removed.' : '';
			}
			return '';
		}
		var fk = convKind( from );
		var tk = convKind( to );
		if ( fk === 'complex' || tk === 'complex' || fk === 'password' || tk === 'password' ) {
			return 'Every stored value is removed: it cannot be converted.';
		}
		if ( tk === 'json' ) {
			return '';
		}
		if ( fk === 'blocks' ) {
			return 'The formatting of the blocks is lost; their text is kept' + ( tk === 'string' && to.type !== 'text' && to.type !== 'richtext' ? ', and values that do not fit are removed.' : '.' );
		}
		switch ( to.type ) {
			case 'number':
				return to.format === 'integer' || to.format === 'biginteger' ? 'Values that are not numbers are removed; decimals are rounded.' : 'Values that are not numbers are removed.';
			case 'boolean':
				return fk === 'number' ? 'Numbers become true, 0 becomes false.' : 'Values other than true/false, yes/no, on/off and 1/0 are removed.';
			case 'enumeration':
				return 'Values that are not one of the enumeration\'s values are removed.';
			case 'date':
				return from.type === 'datetime' ? 'The time of each value is dropped.' : 'Values that are not dates are removed.';
			case 'datetime':
				return from.type === 'date' ? '' : 'Values that are not dates are removed.';
			case 'time':
				return 'Values that are not times of day are removed.';
			case 'email':
				return 'Values that are not e-mail addresses are removed.';
			case 'uid':
				return 'Values are turned into slugs (lowercase, dashes).';
			case 'richtext':
				return '';
			case 'text':
				return fk === 'json' ? 'JSON values are stored as their text.' : '';
		}
		return '';
	}

	function initBuilder( model ) {
		var root = document.getElementById( 'hg-builder' );
		var form = document.getElementById( 'hg-builder-form' );
		if ( ! root || ! form ) {
			return;
		}
		model.all = model.all || [];
		var sc = clone( model.schema );
		sc.attributes = sc.attributes || [];
		sc.attributes.forEach( function ( f ) {
			f._orig = f.name;
		} );
		var isNew = model.isNew;
		var editing = null; // {index, field} while the field panel is open
		var originals = {}; // saved fields by name, to tell what a change does to the stored values
		( ( model.schema && model.schema.attributes ) || [] ).forEach( function ( f ) {
			originals[ f.name ] = f;
		} );
		var kindLabel = { collectionType: 'Collection type', singleType: 'Single type', component: 'Component' }[ sc.kind ];
		var components = model.all.filter( function ( t ) {
			return t.kind === 'component' && t.uid !== sc.uid;
		} );
		var targets = model.all.filter( function ( t ) {
			return t.kind !== 'component';
		} );

		function input( label, value, onchange, attrs ) {
			var id = 'hg-b-' + Math.random().toString( 36 ).slice( 2 );
			var a = Object.assign( { id: id, type: 'text', class: 'regular-text', value: value === undefined || value === null ? '' : value, oninput: function ( e ) {
				onchange( e.target.value );
			} }, attrs || {} );
			return el( 'tr', {}, [ el( 'th', { scope: 'row' }, [ el( 'label', { for: id, text: label } ) ] ), el( 'td', {}, [ el( 'input', a ), attrs && attrs.help ? el( 'p', { class: 'description', text: attrs.help } ) : null ] ) ] );
		}

		function check( label, value, onchange, help ) {
			return el( 'label', { class: 'hg-check' }, [ el( 'input', { type: 'checkbox', checked: !! value, onchange: function ( e ) {
				onchange( e.target.checked );
			} } ), ' ' + label, help ? el( 'span', { class: 'description', text: ' — ' + help } ) : null ] );
		}

		function settingsBox() {
			var t = el( 'table', { class: 'form-table', role: 'presentation' } );
			var plural;
			var singular;
			t.appendChild( input( 'Display name', sc.displayName, function ( v ) {
				sc.displayName = v;
				if ( isNew && sc.kind !== 'component' ) {
					sc.singularName = slugify( v );
					singular.value = sc.singularName;
					if ( sc.kind === 'collectionType' ) {
						sc.pluralName = sc.singularName ? sc.singularName + ( /s$/.test( sc.singularName ) ? 'es' : 's' ) : '';
						plural.value = sc.pluralName;
					}
				}
			}, { required: true, translate: 'no' } ) );
			if ( sc.kind !== 'component' ) {
				var r1 = input( 'API ID (singular)', sc.singularName, function ( v ) {
					sc.singularName = v;
				}, { class: 'regular-text code', readonly: ! isNew, help: isNew ? 'Kebab-case, used in the API. It cannot be changed later.' : 'The singular API ID cannot be changed.' } );
				singular = r1.querySelector( 'input' );
				t.appendChild( r1 );
				if ( sc.kind === 'collectionType' ) {
					var r2 = input( 'API ID (plural)', sc.pluralName, function ( v ) {
						sc.pluralName = v;
					}, { class: 'regular-text code', help: 'The collection route: /wp-json/headlessgo/v1/<plural>' } );
					plural = r2.querySelector( 'input' );
					t.appendChild( r2 );
				}
				t.appendChild( input( 'Description', sc.description, function ( v ) {
					sc.description = v;
				}, { class: 'large-text', translate: 'no' } ) );
				t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row', text: 'Options' } ), el( 'td', {}, [
					check( 'Draft & publish', sc.draftAndPublish, function ( v ) {
						sc.draftAndPublish = v;
					}, 'entries are drafts until published; turning it off publishes every entry' ),
					check( 'Review before publishing', sc.reviewWorkflow, function ( v ) {
						sc.reviewWorkflow = v;
					}, 'a draft must be submitted for review and approved by an editor before it can be published (needs draft & publish)' ),
					check( 'Internationalization', sc.localized, function ( v ) {
						sc.localized = v;
					}, model.golylang ? 'one version per Golylang language' : 'locales come from Golylang when it is active' ) ] ) ] ) );
				t.appendChild( input( 'Preview URL', sc.previewUrl, function ( v ) {
					sc.previewUrl = v;
				}, { type: 'url', class: 'large-text code', placeholder: 'https://app.example.com/preview?slug={slug}&secret={secret}&locale={locale}',
					help: 'Placeholders: {documentId}, {locale}, {status}, {secret}, {previewToken} and any field, such as {slug}.' } ) );
			} else {
				t.appendChild( input( 'Category', sc.category, function ( v ) {
					sc.category = v;
				}, { class: 'regular-text code', readonly: ! isNew, help: isNew ? 'Groups components, e.g. shared, seo, blocks. The component\'s ID is category.name.' : 'ID: ' + sc.uid } ) );
			}
			return el( 'div', { class: 'postbox hg-b-settings' }, [ el( 'h2', { class: 'hndle', text: ( isNew ? 'New ' : '' ) + kindLabel } ), el( 'div', { class: 'inside' }, [ t ] ) ] );
		}

		function summary( f ) {
			var s = [];
			switch ( f.type ) {
				case 'text':
					s.push( f.long ? 'long' : 'short' );
					break;
				case 'richtext':
					s.push( f.format );
					break;
				case 'number':
					s.push( f.format );
					break;
				case 'enumeration':
					s.push( ( f.enum || [] ).join( ', ' ) );
					break;
				case 'media':
					s.push( f.multiple ? 'multiple' : 'single' );
					if ( ( f.allowedTypes || [] ).length ) {
						s.push( f.allowedTypes.join( '/' ) );
					}
					break;
				case 'relation':
					var tname = ( ( targets.filter( function ( t ) {
						return t.uid === f.target;
					} )[ 0 ] || {} ).displayName || ( model.wpTargets.filter( function ( t ) {
						return t.Key === f.target;
					} )[ 0 ] || {} ).Label || ( f.target === sc.uid ? sc.displayName : f.target ) );
					s.push( f.relation + ' → ' + tname );
					if ( f.inversedBy ) {
						s.push( 'inverse: ' + tname + '.' + f.inversedBy );
					}
					if ( f.mappedBy ) {
						s.push( 'inverse of ' + tname + '.' + f.mappedBy );
					}
					break;
				case 'uid':
					if ( f.targetField ) {
						s.push( 'from ' + f.targetField );
					}
					break;
				case 'component':
					s.push( f.component + ( f.repeatable ? ' (repeatable)' : '' ) );
					break;
				case 'dynamiczone':
					s.push( ( f.components || [] ).join( ', ' ) );
					break;
			}
			[ 'required', 'unique', 'private' ].forEach( function ( k ) {
				if ( f[ k ] ) {
					s.push( k );
				}
			} );
			return s.join( ' · ' );
		}

		function typeLabel( t ) {
			var ft = model.fieldTypes.filter( function ( x ) {
				return x.Key === t;
			} )[ 0 ];
			return ft ? ft.Label : t;
		}

		function fieldsBox() {
			var tbody = el( 'tbody' );
			sc.attributes.forEach( function ( f, i ) {
				var ft = model.fieldTypes.filter( function ( x ) {
					return x.Key === f.type;
				} )[ 0 ] || {};
				tbody.appendChild( el( 'tr', {}, [
					el( 'td', {}, [ el( 'span', { class: 'dashicons ' + ( ft.Icon || 'dashicons-admin-generic' ), 'aria-hidden': 'true' } ), ' ', el( 'strong', { translate: 'no', text: f.name } ),
						f._orig && f._orig !== f.name ? el( 'span', { class: 'description', text: ' (renamed from ' + f._orig + ')' } ) : null ] ),
					el( 'td', { text: typeLabel( f.type ) } ),
					el( 'td', { class: 'hg-sum', translate: 'no', text: summary( f ) } ),
					el( 'td', { class: 'hg-tools' }, [
						btn( 'Edit', 'button-small', function () {
							editing = { index: i, field: clone( f ), enumBefore: ( f.enum || [] ).slice() };
							render();
						} ),
						btn( '↑', 'button-small', function () {
							move( sc.attributes, i, -1 );
							render();
						}, 'Move up' ),
						btn( '↓', 'button-small', function () {
							move( sc.attributes, i, 1 );
							render();
						}, 'Move down' ),
						btn( 'Delete', 'button-small hg-del', function () {
							if ( window.confirm( 'Delete the field ' + f.name + '? Its values are removed from every entry when you save.' ) ) {
								sc.attributes.splice( i, 1 );
								render();
							}
						} ) ] ) ] ) );
			} );
			if ( ! sc.attributes.length ) {
				tbody.appendChild( el( 'tr', { class: 'no-items' }, [ el( 'td', { colspan: 4, text: 'No fields yet. Add the first one.' } ) ] ) );
			}
			return el( 'div', { class: 'hg-b-fields' }, [ el( 'h2', { text: 'Fields' } ),
				el( 'table', { class: 'widefat striped hg-table' }, [ el( 'thead', {}, [ el( 'tr', {}, [ el( 'th', { text: 'Name' } ), el( 'th', { text: 'Type' } ), el( 'th', { text: 'Details' } ), el( 'th', { text: '' } ) ] ) ] ), tbody ] ),
				el( 'p', {}, [ btn( '+ Add another field', 'button-secondary', function () {
					editing = { index: -1, field: null };
					render();
				} ) ] ) ] );
		}

		function numInput( label, f, key ) {
			return input( label, f[ key ], function ( v ) {
				f[ key ] = v === '' ? null : Number( v );
			}, { type: 'number', class: 'small-text', step: 'any' } );
		}

		function fieldPanel() {
			var panel = el( 'div', { class: 'postbox hg-b-panel' } );
			var inside = el( 'div', { class: 'inside' } );
			panel.appendChild( el( 'h2', { class: 'hndle', text: editing.index >= 0 ? 'Edit field' : 'Add a field' } ) );
			panel.appendChild( inside );
			if ( ! editing.field ) {
				var grid = el( 'div', { class: 'hg-types' } );
				model.fieldTypes.forEach( function ( t ) {
					if ( sc.kind === 'component' && t.Key === 'dynamiczone' ) {
						return;
					}
					grid.appendChild( el( 'button', { type: 'button', class: 'hg-type-card', onclick: function ( e ) {
						e.preventDefault();
						editing.field = { name: '', type: t.Key };
						if ( t.Key === 'richtext' ) {
							editing.field.format = 'blocks';
						}
						if ( t.Key === 'number' ) {
							editing.field.format = 'integer';
						}
						if ( t.Key === 'relation' ) {
							editing.field.relation = 'manyToOne';
							editing.field.target = targets.length ? targets[ 0 ].uid : 'wp:post';
						}
						if ( t.Key === 'component' && components.length ) {
							editing.field.component = components[ 0 ].uid;
						}
						if ( t.Key === 'dynamiczone' ) {
							editing.field.components = [];
						}
						render();
					} }, [ el( 'span', { class: 'dashicons ' + t.Icon, 'aria-hidden': 'true' } ), el( 'strong', { text: t.Label } ), el( 'span', { text: t.Help } ) ] ) );
				} );
				inside.appendChild( grid );
				inside.appendChild( el( 'p', {}, [ btn( 'Cancel', '', function () {
					editing = null;
					render();
				} ) ] ) );
				return panel;
			}
			var f = editing.field;
			var t = el( 'table', { class: 'form-table', role: 'presentation' } );
			var orig = f._orig ? originals[ f._orig ] : null;
			if ( orig && ! f.mappedBy ) {
				var typeSel = el( 'select', { id: 'hg-b-type', onchange: function ( e ) {
					var nt = e.target.value;
					var keep = { name: f.name, _orig: f._orig, type: nt, required: f.required, private: f.private };
					Object.keys( f ).forEach( function ( k ) {
						delete f[ k ];
					} );
					Object.assign( f, keep );
					if ( nt === orig.type ) {
						Object.assign( f, clone( orig ), { name: keep.name, _orig: keep._orig } );
					} else if ( nt === 'richtext' ) {
						f.format = 'blocks';
					} else if ( nt === 'number' ) {
						f.format = 'integer';
					} else if ( nt === 'relation' ) {
						f.relation = 'manyToOne';
						f.target = targets.length ? targets[ 0 ].uid : 'wp:post';
					} else if ( nt === 'enumeration' ) {
						f.enum = [];
					} else if ( nt === 'dynamiczone' ) {
						f.components = [];
					} else if ( nt === 'component' && components.length ) {
						f.component = components[ 0 ].uid;
					}
					render();
				} } );
				model.fieldTypes.forEach( function ( x ) {
					if ( sc.kind === 'component' && x.Key === 'dynamiczone' ) {
						return;
					}
					typeSel.appendChild( el( 'option', { value: x.Key, selected: x.Key === f.type, text: x.Label } ) );
				} );
				t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row' }, [ el( 'label', { for: 'hg-b-type', text: 'Type' } ) ] ), el( 'td', {}, [ typeSel,
					el( 'p', { class: 'description', text: 'Changing the type converts the values stored in the entries where it can.' } ) ] ) ] ) );
			} else {
				t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row', text: 'Type' } ), el( 'td', { text: typeLabel( f.type ) } ) ] ) );
			}
			t.appendChild( input( 'Name', f.name, function ( v ) {
				f.name = v;
			}, { class: 'regular-text code', required: true, help: 'Letters, digits and underscores, e.g. title or coverImage. No spaces.' } ) );
			function selectRow( label, options, value, onchange ) {
				var id = 'hg-b-' + Math.random().toString( 36 ).slice( 2 );
				var s = el( 'select', { id: id, onchange: function ( e ) {
					onchange( e.target.value );
				} } );
				options.forEach( function ( o ) {
					s.appendChild( el( 'option', { value: o[ 0 ], selected: String( value ) === String( o[ 0 ] ), text: o[ 1 ] } ) );
				} );
				return el( 'tr', {}, [ el( 'th', { scope: 'row' }, [ el( 'label', { for: id, text: label } ) ] ), el( 'td', {}, [ s ] ) ] );
			}
			switch ( f.type ) {
				case 'text':
					t.appendChild( selectRow( 'Kind', [ [ '', 'Short text (titles, names, links)' ], [ '1', 'Long text (descriptions, biographies)' ] ], f.long ? '1' : '', function ( v ) {
						f.long = v === '1';
					} ) );
					break;
				case 'richtext':
					t.appendChild( selectRow( 'Format', [ [ 'blocks', 'Blocks (structured JSON)' ], [ 'markdown', 'Markdown' ] ], f.format, function ( v ) {
						f.format = v;
					} ) );
					break;
				case 'number':
					t.appendChild( selectRow( 'Number format', [ [ 'integer', 'integer (ex: 10)' ], [ 'biginteger', 'big integer (ex: 123456789012345678)' ], [ 'decimal', 'decimal (ex: 2.22)' ], [ 'float', 'float (ex: 3.3333333)' ] ], f.format, function ( v ) {
						f.format = v;
					} ) );
					break;
				case 'enumeration':
					var ta = el( 'textarea', { class: 'large-text code', rows: 4, translate: 'no', oninput: function ( e ) {
						f.enum = e.target.value.split( '\n' ).map( function ( x ) {
							return x.trim();
						} ).filter( Boolean );
					} }, [ ( f.enum || [] ).join( '\n' ) ] );
					t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row', text: 'Values' } ), el( 'td', {}, [ ta, el( 'p', { class: 'description', text: 'One value per line.' } ),
						orig && orig.type === 'enumeration' ? el( 'p', { class: 'description', text: 'Changing a value on its line renames it in every entry; entries lose a removed value.' } ) : null ] ) ] ) );
					break;
				case 'media':
					t.appendChild( selectRow( 'Files', [ [ '', 'Single media' ], [ '1', 'Multiple media' ] ], f.multiple ? '1' : '', function ( v ) {
						f.multiple = v === '1';
					} ) );
					var allowed = el( 'td' );
					[ 'images', 'videos', 'audios', 'files' ].forEach( function ( k ) {
						allowed.appendChild( check( k, ( f.allowedTypes || [] ).indexOf( k ) >= 0, function ( on ) {
							var l = ( f.allowedTypes || [] ).filter( function ( x ) {
								return x !== k;
							} );
							if ( on ) {
								l.push( k );
							}
							f.allowedTypes = l;
						} ) );
					} );
					allowed.appendChild( el( 'p', { class: 'description', text: 'None ticked: any file.' } ) );
					t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row', text: 'Allowed types' } ), allowed ] ) );
					break;
				case 'relation':
					var opts = targets.map( function ( x ) {
						return [ x.uid, x.displayName ];
					} );
					if ( ! isNew && sc.kind !== 'component' && ! opts.some( function ( o ) {
						return o[ 0 ] === sc.uid;
					} ) ) {
						opts.push( [ sc.uid, sc.displayName ] );
					}
					model.wpTargets.forEach( function ( x ) {
						opts.push( [ x.Key, x.Label ] );
					} );
					if ( f.mappedBy ) {
						t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row', text: 'Relation' } ), el( 'td', {}, [ el( 'span', { text: f.relation + ' → ' } ), el( 'span', { translate: 'no', text: ( opts.filter( function ( o ) {
							return o[ 0 ] === f.target;
						} )[ 0 ] || [ '', f.target ] )[ 1 ] + '.' + f.mappedBy } ),
						el( 'p', { class: 'description', text: 'The inverse side of a relation: the links are stored on the other type and edited from either side. Deleting it removes only this side.' } ) ] ) ] ) );
						break;
					}
					t.appendChild( selectRow( 'Relation', model.relations.map( function ( r ) {
						return [ r.Key, r.Label ];
					} ), f.relation, function ( v ) {
						f.relation = v;
					} ) );
					t.appendChild( selectRow( 'Target', opts, f.target, function ( v ) {
						f.target = v;
						if ( v.indexOf( 'wp:' ) === 0 ) {
							delete f.inversedBy;
						}
						render();
					} ) );
					if ( sc.kind !== 'component' && f.target && f.target.indexOf( 'wp:' ) !== 0 ) {
						t.appendChild( input( 'Inverse field (optional)', f.inversedBy, function ( v ) {
							f.inversedBy = v.trim();
						}, { class: 'regular-text code', translate: 'no', placeholder: 'e.g. ' + ( isMulti( f ) ? 'owner' : ( sc.pluralName || 'items' ).replace( /-([a-z])/g, function ( m, c ) {
							return c.toUpperCase();
						} ) ),
							help: 'Adds this field name to the target type: it lists the entries that point to it, and links can be edited from both sides.' } ) );
					}
					break;
				case 'uid':
					var tf = [ [ '', '— None —' ] ];
					sc.attributes.forEach( function ( x ) {
						if ( x.type === 'text' ) {
							tf.push( [ x.name, x.name ] );
						}
					} );
					t.appendChild( selectRow( 'Attached field', tf, f.targetField || '', function ( v ) {
						f.targetField = v;
					} ) );
					break;
				case 'component':
					if ( ! components.length ) {
						t.appendChild( el( 'tr', {}, [ el( 'td', { colspan: 2 }, [ el( 'p', { class: 'description', text: 'Create a component first (Content-Type Builder › Create new component).' } ) ] ) ] ) );
					}
					t.appendChild( selectRow( 'Component', components.map( function ( x ) {
						return [ x.uid, x.displayName + ' (' + x.uid + ')' ];
					} ), f.component, function ( v ) {
						f.component = v;
					} ) );
					t.appendChild( selectRow( 'Kind', [ [ '', 'Single component' ], [ '1', 'Repeatable component' ] ], f.repeatable ? '1' : '', function ( v ) {
						f.repeatable = v === '1';
					} ) );
					break;
				case 'dynamiczone':
					var cell = el( 'td' );
					components.forEach( function ( x ) {
						cell.appendChild( check( x.displayName + ' (' + x.uid + ')', ( f.components || [] ).indexOf( x.uid ) >= 0, function ( on ) {
							var l = ( f.components || [] ).filter( function ( y ) {
								return y !== x.uid;
							} );
							if ( on ) {
								l.push( x.uid );
							}
							f.components = l;
						} ) );
					} );
					if ( ! components.length ) {
						cell.appendChild( el( 'p', { class: 'description', text: 'Create components first.' } ) );
					}
					t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row', text: 'Components' } ), cell ] ) );
					break;
			}
			// Advanced settings
			var adv = el( 'td' );
			adv.appendChild( check( 'Required field', f.required, function ( v ) {
				f.required = v;
			}, 'entries cannot be published without it' ) );
			if ( [ 'text', 'number', 'date', 'datetime', 'time', 'email', 'enumeration', 'uid' ].indexOf( f.type ) >= 0 ) {
				adv.appendChild( check( 'Unique field', f.unique || f.type === 'uid', function ( v ) {
					f.unique = v;
				}, 'no two entries share a value' ) );
			}
			adv.appendChild( check( 'Private field', f.private || f.type === 'password', function ( v ) {
				f.private = v;
			}, 'never returned by the API' ) );
			t.appendChild( el( 'tr', {}, [ el( 'th', { scope: 'row', text: 'Settings' } ), adv ] ) );
			var minLabel = { number: [ 'Minimum value', 'Maximum value' ], component: [ 'Minimum items', 'Maximum items' ], dynamiczone: [ 'Minimum items', 'Maximum items' ],
				media: [ 'Minimum files', 'Maximum files' ], relation: [ 'Minimum entries', 'Maximum entries' ] }[ f.type ] || [ 'Minimum length', 'Maximum length' ];
			if ( [ 'boolean', 'date', 'datetime', 'time', 'enumeration', 'json' ].indexOf( f.type ) < 0 && ! ( f.type === 'component' && ! f.repeatable ) && ! ( f.type === 'richtext' && f.format === 'blocks' ) ) {
				t.appendChild( numInput( minLabel[ 0 ], f, 'min' ) );
				t.appendChild( numInput( minLabel[ 1 ], f, 'max' ) );
			}
			if ( f.type === 'text' || f.type === 'uid' || f.type === 'email' ) {
				t.appendChild( input( 'RegExp pattern', f.regex, function ( v ) {
					f.regex = v;
				}, { class: 'regular-text code', help: 'The value must match this regular expression (RE2 syntax).' } ) );
			}
			if ( f.type === 'boolean' ) {
				t.appendChild( selectRow( 'Default value', [ [ '', 'none' ], [ 'true', 'true' ], [ 'false', 'false' ] ], f.default === true ? 'true' : ( f.default === false ? 'false' : '' ), function ( v ) {
					f.default = v === '' ? null : v === 'true';
				} ) );
			} else if ( f.type === 'enumeration' ) {
				t.appendChild( selectRow( 'Default value', [ [ '', 'none' ] ].concat( ( f.enum || [] ).map( function ( x ) {
					return [ x, x ];
				} ) ), f.default || '', function ( v ) {
					f.default = v || null;
				} ) );
			} else if ( [ 'text', 'number', 'date', 'datetime', 'time', 'email', 'uid' ].indexOf( f.type ) >= 0 ) {
				t.appendChild( input( 'Default value', f.default, function ( v ) {
					f.default = v === '' ? null : v;
				}, { type: { number: 'text', date: 'date', datetime: 'datetime-local', time: 'time' }[ f.type ] || 'text', class: 'regular-text', translate: 'no' } ) );
			}
			inside.appendChild( t );
			var warn = lossy( orig, f );
			if ( warn ) {
				inside.appendChild( el( 'div', { class: 'notice notice-warning inline' }, [ el( 'p', {}, [ el( 'strong', { text: 'Stored values change: ' } ), warn ] ) ] ) );
			}
			inside.appendChild( el( 'p', { class: 'hg-b-actions' }, [
				btn( 'Finish', 'button-primary', function () {
					if ( ! /^[A-Za-z][A-Za-z0-9_]*$/.test( f.name || '' ) ) {
						window.alert( 'Give the field a name: letters, digits and underscores, starting with a letter.' );
						return;
					}
					var dup = sc.attributes.some( function ( x, i ) {
						return i !== editing.index && x.name.toLowerCase() === f.name.toLowerCase();
					} );
					if ( dup ) {
						window.alert( 'Another field is already called ' + f.name + '.' );
						return;
					}
					if ( f.type === 'enumeration' && editing.enumBefore ) {
						var before = editing.enumBefore;
						var after = f.enum || [];
						var ren = {};
						for ( var k = 0; k < Math.min( before.length, after.length ); k++ ) {
							if ( before[ k ] !== after[ k ] && after.indexOf( before[ k ] ) < 0 && before.indexOf( after[ k ] ) < 0 ) {
								ren[ before[ k ] ] = after[ k ];
							}
						}
						f.enumRenames = Object.assign( {}, f.enumRenames || {}, ren );
						if ( ! Object.keys( f.enumRenames ).length ) {
							delete f.enumRenames;
						}
					}
					if ( editing.index >= 0 ) {
						sc.attributes[ editing.index ] = f;
					} else {
						sc.attributes.push( f );
					}
					editing = null;
					render();
				} ),
				' ',
				btn( 'Cancel', '', function () {
					editing = null;
					render();
				} ) ] ) );
			return panel;
		}

		function render() {
			root.innerHTML = '';
			root.appendChild( settingsBox() );
			root.appendChild( fieldsBox() );
			if ( editing ) {
				var p = fieldPanel();
				root.appendChild( p );
				p.scrollIntoView( { block: 'nearest' } );
			}
			root.appendChild( el( 'p', { class: 'submit' }, [ el( 'button', { type: 'submit', class: 'button button-primary button-large' }, [ 'Save' ] ),
				editing ? el( 'span', { class: 'description', text: '  Finish or cancel the field first.' } ) : null ] ) );
		}

		form.addEventListener( 'submit', function ( e ) {
			if ( editing ) {
				e.preventDefault();
				window.alert( 'Finish or cancel the field you are editing first.' );
				return;
			}
			var losses = [];
			sc.attributes.forEach( function ( f ) {
				var w = f._orig ? lossy( originals[ f._orig ], f ) : '';
				if ( w ) {
					losses.push( f.name + ': ' + w );
				}
			} );
			if ( losses.length && ! window.confirm( 'Saving changes the values stored in the entries:\n\n' + losses.join( '\n' ) + '\n\nSave anyway?' ) ) {
				e.preventDefault();
				return;
			}
			var out = clone( sc );
			out.attributes.forEach( function ( f ) {
				if ( f._orig && f._orig !== f.name ) {
					f.renamedFrom = f._orig;
				}
				delete f._orig;
				[ 'min', 'max' ].forEach( function ( k ) {
					if ( f[ k ] === null || f[ k ] === undefined || isNaN( f[ k ] ) ) {
						delete f[ k ];
					}
				} );
			} );
			document.getElementById( 'hg-schema' ).value = JSON.stringify( out );
		} );
		render();
	}

	/* ===================================== GraphQL playground ===================================== */

	function initPlayground( form ) {
		var q = document.getElementById( 'hg-gql-query' );
		var vars = document.getElementById( 'hg-gql-vars' );
		var tok = document.getElementById( 'hg-gql-token' );
		var out = document.getElementById( 'hg-gql-result' );
		try {
			var saved = window.localStorage.getItem( 'hg-gql-query' );
			if ( saved ) {
				q.value = saved;
			}
		} catch ( err ) {}
		function run() {
			var body = { query: q.value };
			if ( vars.value.trim() ) {
				try {
					body.variables = JSON.parse( vars.value );
				} catch ( err ) {
					out.textContent = 'The variables are not valid JSON.';
					return;
				}
			}
			try {
				window.localStorage.setItem( 'hg-gql-query', q.value );
			} catch ( err ) {}
			var headers = { 'Content-Type': 'application/json' };
			var url = '/wp-admin/headlessgo/graphql';
			if ( tok.value.trim() ) {
				url = form.getAttribute( 'data-endpoint' );
				headers.Authorization = 'Bearer ' + tok.value.trim();
			} else {
				headers[ 'X-HG-Nonce' ] = form.getAttribute( 'data-nonce' );
			}
			out.textContent = '…';
			fetch( url, { method: 'POST', credentials: tok.value.trim() ? 'omit' : 'same-origin', headers: headers, body: JSON.stringify( body ) } ).then( function ( r ) {
				return r.text();
			} ).then( function ( t ) {
				try {
					out.textContent = JSON.stringify( JSON.parse( t ), null, 2 );
				} catch ( err ) {
					out.textContent = t;
				}
			} ).catch( function ( err ) {
				out.textContent = String( err );
			} );
		}
		form.addEventListener( 'submit', function ( e ) {
			e.preventDefault();
			run();
		} );
		q.addEventListener( 'keydown', function ( e ) {
			if ( e.key === 'Enter' && ( e.ctrlKey || e.metaKey ) ) {
				e.preventDefault();
				run();
			}
		} );
	}

	// Edit lock: renew it while the page is open, tell the user when somebody takes it over, give it up
	// when the page closes. A locked entry (someone else edits it) is read-only.
	function initLock( model ) {
		var form = document.getElementById( 'hg-entry-form' );
		var holder = document.getElementById( 'hg-lock-data' );
		if ( ! form || model.isNew ) {
			return;
		}
		function field( n ) {
			var i = form.querySelector( '[name="' + n + '"]' );
			return i ? i.value : '';
		}
		function freeze( by ) {
			form.querySelectorAll( '#hg-editor input, #hg-editor select, #hg-editor textarea, #hg-editor button, .hg-actions button' ).forEach( function ( x ) {
				x.disabled = true;
			} );
			var hasBanner = document.getElementById( 'hg-lock-banner' );
			if ( ! hasBanner ) {
				var n = el( 'div', { id: 'hg-lock-banner', class: 'notice notice-warning' }, [ el( 'p', { text: by + ' is now editing this entry. Reload the page to take over editing.' } ) ] );
				form.parentNode.insertBefore( n, form );
			}
		}
		if ( model.locked ) {
			freeze( holder ? holder.getAttribute( 'data-by' ) : '' );
			return;
		}
		function send( op, beacon ) {
			var fd = new FormData();
			fd.append( '_wpnonce', field( '_wpnonce' ) );
			[ 'type', 'doc', 'locale' ].forEach( function ( k ) {
				fd.append( k, field( k ) );
			} );
			fd.append( 'op', op );
			if ( beacon && navigator.sendBeacon ) {
				navigator.sendBeacon( '/wp-admin/headlessgo/lock', fd );
				return Promise.resolve( null );
			}
			return fetch( '/wp-admin/headlessgo/lock', { method: 'POST', credentials: 'same-origin', body: fd } ).then( function ( r ) {
				return r.json();
			} );
		}
		var beat = ( holder ? parseInt( holder.getAttribute( 'data-beat' ), 10 ) : 0 ) || 15;
		var timer = setInterval( function () {
			send( 'heartbeat' ).then( function ( r ) {
				if ( r && r.locked ) {
					clearInterval( timer );
					freeze( r.by );
				}
			} ).catch( function () {} );
		}, beat * 1000 );
		window.addEventListener( 'pagehide', function () {
			send( 'release', true );
		} );
		form.addEventListener( 'submit', function () {
			clearInterval( timer );
		} );
	}

	function boot() {
		var g = document.getElementById( 'hg-gql-form' );
		if ( g ) {
			initPlayground( g );
		}
		var m = document.getElementById( 'hg-editor-model' );
		if ( m ) {
			var model = JSON.parse( m.textContent );
			initEditor( model );
			initLock( model );
		}
		var b = document.getElementById( 'hg-builder-model' );
		if ( b ) {
			initBuilder( JSON.parse( b.textContent ) );
		}
	}
	if ( document.readyState === 'loading' ) {
		document.addEventListener( 'DOMContentLoaded', boot );
	} else {
		boot();
	}
}() );
