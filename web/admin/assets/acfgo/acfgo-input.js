/**
 * Pursae Custom Fields field inputs: places the field boxes into the edit screens (classic editor, Pursae Editor's
 * meta box area, user / term / comment forms), and gives every field type its behaviour —
 * repeaters, flexible content, media pickers, relationship search, conditional logic, tabs,
 * accordions and validation before submit.
 */
( function ( $ ) {
	'use strict';

	var ajaxurl = window.ajaxurl || '/wp-admin/admin-ajax';
	var uid = 0;
	// __ translates a message (wpgo-l10n.js), then the {placeholders} are filled in.
	function __( msg, vars ) {
		var out = window.wpgo__ ? window.wpgo__( msg ) : msg;
		Object.keys( vars || {} ).forEach( function ( k ) { out = out.split( '{' + k + '}' ).join( vars[ k ] ); } );
		return out;
	}
	function newRowID() {
		uid++;
		return 'r' + Date.now().toString( 36 ) + uid;
	}

	/* Placement ------------------------------------------------------------------------------ */

	function place() {
		var $boxes = $( '#acfgo-boxes' );
		if ( ! $boxes.length ) {
			return;
		}
		var kind = $boxes.data( 'kind' ), editor = $boxes.data( 'editor' );
		if ( kind === 'post' && editor === 'block' ) {
			placeBlockEditor( $boxes );
		} else if ( kind === 'post' ) {
			$boxes.children( '.acf-postbox' ).each( function () {
				var $box = $( this ), pos = $box.data( 'position' );
				if ( pos === 'acf_after_title' && $( '#titlediv' ).length ) {
					var $after = $( '#acf_after_title-sortables' );
					if ( ! $after.length ) {
						$after = $( '<div id="acf_after_title-sortables" class="meta-box-sortables"></div>' ).insertAfter( '#titlediv' );
					}
					$after.append( $box );
				} else if ( pos === 'side' && $( '#side-sortables' ).length ) {
					$( '#side-sortables' ).append( $box );
				} else if ( $( '#normal-sortables' ).length ) {
					$( '#normal-sortables' ).prepend( $box );
				} else {
					$( '#post-body-content' ).append( $box );
				}
			} );
			$( '#normal-sortables .acf-postbox' ).each( function () {
				// keep the order of the field groups (menu order)
				$( '#normal-sortables' ).append( this );
			} );
		} else {
			var $form = $( $boxes.data( 'form' ) ), $before = $( $boxes.data( 'before' ) ).first();
			var $content = $boxes.children();
			if ( $before.length ) {
				$content.insertBefore( $before );
			} else if ( $form.length ) {
				$form.append( $content );
			}
		}
		$boxes.remove();
		window.acfgoNonce = $boxes.data( 'nonce' );
		window.acfgoValidateNonce = $boxes.data( 'validate' );
	}

	// Pursae Editor: the boxes go into #metaboxes as the location forms the editor moves into its
	// meta box area and posts to window._wpMetaBoxUrl when the post is saved.
	function placeBlockEditor( $boxes ) {
		var $meta = $( '#metaboxes' );
		if ( ! $meta.length ) {
			$meta = $( '<div id="metaboxes" class="hidden"></div>' ).appendTo( 'body' );
		}
		var $base = $( '<form class="metabox-base-form"><input type="hidden" name="post_ID" value="' + ( $boxes.data( 'post' ) || '' ) + '" />' +
			'<input type="hidden" name="_acfnonce" value="' + $boxes.data( 'nonce' ) + '" /><input type="hidden" name="post_type" value="' + ( $boxes.data( 'post-type' ) || '' ) + '" /></form>' );
		$meta.append( $base );
		var locations = { normal: [], side: [] };
		$boxes.children( '.acf-postbox' ).each( function () {
			var $box = $( this ), loc = $box.data( 'position' ) === 'side' ? 'side' : 'normal';
			var $form = $meta.children( '.metabox-location-' + loc );
			if ( ! $form.length ) {
				$form = $( '<form class="metabox-location-' + loc + '" onsubmit="return false;"><div id="poststuff" class="sidebar-open"><div id="postbox-container-2" class="postbox-container"><div id="' + loc + '-sortables" class="meta-box-sortables ui-sortable"></div></div></div></form>' ).appendTo( $meta );
			}
			$form.find( '.meta-box-sortables' ).append( $box );
			locations[ loc ].push( { id: $box.attr( 'id' ), title: $box.find( '.hndle' ).first().text() } );
		} );
		// The editor's init promise is defined by an inline script printed after this file.
		$( function () {
			if ( ! window._wpLoadBlockEditor || ! window.wp || ! wp.data ) {
				return;
			}
			window._wpLoadBlockEditor.then( function () {
				window._wpMetaBoxUrl = ajaxurl + '?action=acfgo/meta_box_save';
				// The editor reads post_ID and post_type from the base form when it saves the boxes.
				var sync = function () {
					var editor = wp.data.select( 'core/editor' ), id = editor.getCurrentPostId(), type = editor.getCurrentPostType();
					if ( id && String( $base.find( '[name=post_ID]' ).val() ) !== String( id ) ) {
						$base.find( '[name=post_ID]' ).val( id );
					}
					if ( type && $base.find( '[name=post_type]' ).val() !== type ) {
						$base.find( '[name=post_type]' ).val( type );
					}
				};
				sync();
				wp.data.subscribe( sync );
				wp.data.dispatch( 'core/edit-post' ).setAvailableMetaBoxesPerLocation( locations );
				$meta.find( '.acf-postbox' ).each( function () {
					init( this );
				} );
			} );
		} );
	}

	/* Field values --------------------------------------------------------------------------- */

	// fieldValue reads a field's current value for conditional logic.
	function fieldValue( $field ) {
		var type = $field.data( 'type' ), $input = $field.children( '.acf-input' );
		if ( type === 'true_false' ) {
			return $input.find( 'input[type=checkbox]' ).first().prop( 'checked' ) ? '1' : '';
		}
		if ( type === 'checkbox' ) {
			return $input.find( 'input[type=checkbox]:checked' ).not( '.acf-checkbox-toggle' ).map( function () {
				return this.value;
			} ).get();
		}
		if ( type === 'radio' || type === 'button_group' ) {
			return $input.find( 'input[type=radio]:checked' ).val() || '';
		}
		var $sel = $input.find( 'select' ).first();
		if ( $sel.length ) {
			return $sel.val() || '';
		}
		var $f = $input.find( 'input:not([type=hidden]), textarea' ).first();
		if ( ! $f.length ) {
			$f = $input.find( 'input' ).first();
		}
		return $f.val() || '';
	}

	function isEmpty( v ) {
		return v === '' || v === null || v === undefined || ( Array.isArray( v ) && v.length === 0 );
	}

	function matchRule( rule, v ) {
		var target = rule.value, list = Array.isArray( v ) ? v : [ v ];
		switch ( rule.operator ) {
			case '!=empty':
				return ! isEmpty( v );
			case '==empty':
				return isEmpty( v );
			case '==':
				return list.indexOf( String( target ) ) !== -1;
			case '!=':
				return list.indexOf( String( target ) ) === -1;
			case '==pattern':
				try {
					return new RegExp( target ).test( String( v ) );
				} catch ( e ) {
					return false;
				}
			case '==contains':
				return String( v ).indexOf( target ) !== -1;
			case '>':
				return parseFloat( v ) > parseFloat( target );
			case '<':
				return parseFloat( v ) < parseFloat( target );
			case 'selection_greater_than':
				return list.filter( Boolean ).length > parseInt( target, 10 );
			case 'selection_less_than':
				return list.filter( Boolean ).length < parseInt( target, 10 );
		}
		return false;
	}

	// findField looks for the rule's field next to the conditional field first, then further out.
	function findField( $from, key ) {
		var $scope = $from.parent().closest( '.acf-fields, .acf-row, .layout, .inside, .acf-form-group, form, body' );
		while ( $scope.length ) {
			var $f = $scope.find( '.acf-field[data-key="' + key + '"]' ).first();
			if ( $f.length ) {
				return $f;
			}
			$scope = $scope.parent().closest( '.acf-fields, .acf-row, .layout, .inside, .acf-form-group, form, body' );
		}
		return $();
	}

	function evaluate( root ) {
		$( root ).find( '.acf-field[data-conditions]' ).each( function () {
			var $field = $( this ), groups = $field.data( 'conditions' ), show = false;
			if ( ! Array.isArray( groups ) || ! groups.length ) {
				return;
			}
			groups.forEach( function ( rules ) {
				var ok = rules.length > 0;
				rules.forEach( function ( r ) {
					var $t = findField( $field, r.field );
					if ( ! $t.length || $t.hasClass( 'acf-hidden' ) || ! matchRule( r, fieldValue( $t ) ) ) {
						ok = false;
					}
				} );
				show = show || ok;
			} );
			toggleField( $field, show );
		} );
	}

	function toggleField( $field, show ) {
		if ( show === ! $field.hasClass( 'acf-hidden' ) ) {
			return;
		}
		$field.toggleClass( 'acf-hidden', ! show );
		// Hidden fields are not submitted, so their stored values stay (ACF's behaviour).
		$field.find( 'input, select, textarea' ).each( function () {
			if ( ! show ) {
				if ( ! this.disabled ) {
					this.disabled = true;
					$( this ).attr( 'data-acf-disabled', '1' );
				}
			} else if ( $( this ).attr( 'data-acf-disabled' ) ) {
				this.disabled = false;
				$( this ).removeAttr( 'data-acf-disabled' );
			}
		} );
	}

	/* Tabs and accordions -------------------------------------------------------------------- */

	function initTabs( root ) {
		$( root ).find( '.acf-fields, .inside' ).addBack( '.acf-fields, .inside' ).each( function () {
			var $wrap = $( this ), $tabs = $wrap.children( '.acf-field-tab' );
			if ( ! $tabs.length || $wrap.data( 'acf-tabs' ) ) {
				return;
			}
			$wrap.data( 'acf-tabs', true );
			var groups = [], cur = null;
			$tabs.each( function () {
				var $t = $( this );
				if ( ! cur || $t.data( 'endpoint' ) ) {
					cur = { $nav: $( '<ul class="acf-hl acf-tab-group"></ul>' ), tabs: [], placement: $t.data( 'placement' ) };
					groups.push( cur );
					cur.$nav.insertBefore( $t );
				}
				var $li = $( '<li><a href="#" class="acf-tab-button">' + $( '<span>' ).text( $t.find( '.acf-tab-label' ).text() ).html() + '</a></li>' );
				$li.data( 'tab', $t );
				cur.$nav.append( $li );
				cur.tabs.push( $t );
			} );
			groups.forEach( function ( g ) {
				if ( g.placement === 'left' ) {
					g.$nav.wrap( '<div class="acf-tab-wrap -left"></div>' );
					$wrap.addClass( '-sidebar' );
				}
				var $sel = g.$nav.children().filter( function () {
					return $( this ).data( 'tab' ).data( 'selected' );
				} ).first();
				( $sel.length ? $sel : g.$nav.children().first() ).find( 'a' ).trigger( 'click' );
			} );
		} );
	}

	$( document ).on( 'click', '.acf-tab-button', function ( e ) {
		e.preventDefault();
		var $li = $( this ).parent(), $nav = $li.parent();
		$nav.children().removeClass( 'active' );
		$li.addClass( 'active' );
		$nav.children().each( function () {
			var $t = $( this ).data( 'tab' ), on = this === $li[ 0 ];
			$t.nextUntil( '.acf-field-tab, .acf-tab-group, .acf-tab-wrap' ).toggleClass( 'acf-tab-hidden', ! on );
		} );
	} );

	function initAccordions( root ) {
		$( root ).find( '.acf-field-accordion' ).each( function () {
			var $a = $( this );
			if ( $a.data( 'acf-acc' ) ) {
				return;
			}
			$a.data( 'acf-acc', true );
			if ( $a.data( 'endpoint' ) ) {
				$a.addClass( 'acf-hidden-accordion' );
				return;
			}
			setAccordion( $a, !! $a.data( 'open' ) );
		} );
	}

	function setAccordion( $a, open ) {
		$a.toggleClass( '-open', open );
		$a.nextUntil( '.acf-field-accordion, .acf-field-tab' ).toggleClass( 'acf-accordion-hidden', ! open );
		$a.find( '.acf-accordion-icon' ).toggleClass( 'dashicons-arrow-down', ! open ).toggleClass( 'dashicons-arrow-up', open );
	}

	$( document ).on( 'click', '.acf-accordion-title', function () {
		var $a = $( this ).closest( '.acf-field-accordion' ), open = ! $a.hasClass( '-open' );
		if ( open && ! $a.data( 'multi-expand' ) ) {
			$a.siblings( '.acf-field-accordion.-open' ).each( function () {
				setAccordion( $( this ), false );
			} );
		}
		setAccordion( $a, open );
	} );

	/* Repeater ------------------------------------------------------------------------------------ */

	function renumber( $list, itemSel, numberSel ) {
		$list.children( itemSel ).each( function ( i ) {
			$( this ).find( numberSel ).first().text( i + 1 );
			$( this ).find( '.acf-row-order' ).first().val( i );
		} );
	}

	function repeaterRows( $rep ) {
		return $rep.children( 'table' ).children( 'tbody' ).children( '.acf-row' );
	}

	// repeaterPage shows one page of rows when the field has pagination on. Rows of the other
	// pages are only hidden, so they are still submitted and saved.
	function repeaterPage( $rep, page ) {
		if ( ! $rep.attr( 'data-pagination' ) ) {
			return;
		}
		var per = parseInt( $rep.attr( 'data-per-page' ), 10 ) || 20, $rows = repeaterRows( $rep );
		var pages = Math.max( 1, Math.ceil( $rows.length / per ) );
		page = Math.min( Math.max( page || $rep.data( 'page' ) || 1, 1 ), pages );
		$rep.data( 'page', page );
		$rows.each( function ( i ) {
			$( this ).toggleClass( 'acf-row-paged-out', Math.floor( i / per ) + 1 !== page );
		} );
		$rep.find( '.acf-repeater-pagination' ).first().toggle( pages > 1 ).find( '.acf-repeater-page-info' ).text( page + ' / ' + pages );
	}

	function repeaterState( $rep ) {
		var n = repeaterRows( $rep ).length, max = parseInt( $rep.data( 'max' ), 10 ) || 0;
		$rep.toggleClass( '-empty', n === 0 ).toggleClass( 'is-full', max > 0 && n >= max );
		renumber( $rep.children( 'table' ).children( 'tbody' ), '.acf-row', '.acf-row-number' );
		repeaterPage( $rep );
	}

	$( document ).on( 'click', '.acf-repeater-prev, .acf-repeater-next', function ( e ) {
		e.preventDefault();
		var $rep = $( this ).closest( '.acf-repeater' );
		repeaterPage( $rep, ( $rep.data( 'page' ) || 1 ) + ( $( this ).hasClass( 'acf-repeater-next' ) ? 1 : -1 ) );
	} );

	function addFromTemplate( tpl, placeholder ) {
		var html = tpl.innerHTML.split( placeholder ).join( newRowID() );
		var $el = $( $.parseHTML( html, document, true ) ).filter( '*' );
		return $el;
	}

	function addRepeaterRow( $rep, $after ) {
		var max = parseInt( $rep.data( 'max' ), 10 ) || 0;
		if ( max > 0 && repeaterRows( $rep ).length >= max ) {
			window.alert( __( 'Maximum rows reached ({max} rows)', { max: max } ) );
			return;
		}
		var tpl = $rep.children( 'template.acf-clone-tpl' )[ 0 ];
		var $row = addFromTemplate( tpl, $rep.data( 'placeholder' ) );
		if ( $after && $after.length ) {
			$row.insertBefore( $after );
		} else {
			$rep.children( 'table' ).children( 'tbody' ).append( $row );
		}
		init( $row[ 0 ] );
		repeaterState( $rep );
		if ( $rep.attr( 'data-pagination' ) ) { // jump to the page of the new row
			var per = parseInt( $rep.attr( 'data-per-page' ), 10 ) || 20;
			repeaterPage( $rep, Math.floor( repeaterRows( $rep ).index( $row.first() ) / per ) + 1 );
		}
		return $row;
	}

	// renameRow gives a duplicated row a new row id in every input name.
	function renameRow( $row, base, oldID, newID ) {
		var from = base + '[' + oldID + ']', to = base + '[' + newID + ']';
		$row.attr( 'data-id', newID );
		$row.find( '[name]' ).each( function () {
			if ( this.name.indexOf( from ) === 0 ) {
				this.name = to + this.name.slice( from.length );
			}
		} );
	}

	$( document ).on( 'click', '.acf-repeater-add', function ( e ) {
		e.preventDefault();
		addRepeaterRow( $( this ).closest( '.acf-repeater' ) );
	} );
	$( document ).on( 'click', '.acf-row-add', function ( e ) {
		e.preventDefault();
		addRepeaterRow( $( this ).closest( '.acf-repeater' ), $( this ).closest( '.acf-row' ) );
	} );
	$( document ).on( 'click', '.acf-row-remove', function ( e ) {
		e.preventDefault();
		var $rep = $( this ).closest( '.acf-repeater' ), min = parseInt( $rep.data( 'min' ), 10 ) || 0;
		if ( min > 0 && repeaterRows( $rep ).length <= min ) {
			window.alert( __( 'Minimum rows not reached ({min} rows)', { min: min } ) );
			return;
		}
		$( this ).closest( '.acf-row' ).remove();
		repeaterState( $rep );
		evaluate( document );
	} );
	$( document ).on( 'click', '.acf-row-duplicate', function ( e ) {
		e.preventDefault();
		var $rep = $( this ).closest( '.acf-repeater' ), $row = $( this ).closest( '.acf-row' );
		var $copy = $row.clone();
		renameRow( $copy, $rep.data( 'name' ), $row.attr( 'data-id' ), newRowID() );
		$copy.insertAfter( $row );
		init( $copy[ 0 ] );
		repeaterState( $rep );
	} );
	$( document ).on( 'click', '.acf-row-collapse', function ( e ) {
		e.preventDefault();
		var $row = $( this ).closest( '.acf-row' ), $rep = $row.closest( '.acf-repeater' );
		$row.toggleClass( '-collapsed' );
		var key = $rep.data( 'collapsed' );
		var $sum = $row.find( '.acf-row-summary' ).first();
		if ( $row.hasClass( '-collapsed' ) && key ) {
			var $f = $row.find( '.acf-field[data-key="' + key + '"]' ).first();
			if ( ! $sum.length ) {
				$sum = $( '<td class="acf-row-summary"></td>' ).insertAfter( $row.children( '.acf-row-handle.order' ) );
			}
			$sum.text( [].concat( fieldValue( $f ) ).join( ', ' ) );
		} else {
			$sum.remove();
		}
	} );

	/* Flexible content -------------------------------------------------------------------------- */

	function fcState( $fc ) {
		var $values = $fc.children( '.values' );
		var n = $values.children( '.layout' ).length, max = parseInt( $fc.data( 'max' ), 10 ) || 0;
		$fc.children( '.no-value-message' ).toggle( n === 0 );
		$fc.toggleClass( 'is-full', max > 0 && n >= max );
		renumber( $values, '.layout', '.acf-fc-layout-order' );
	}

	$( document ).on( 'click', '.acf-fc-add, .acf-fc-insert', function ( e ) {
		e.preventDefault();
		var $fc = $( this ).closest( '.acf-flexible-content' );
		var $popup = $fc.children( '.acf-actions' ).children( '.acf-fc-popup' );
		$( '.acf-fc-popup' ).not( $popup ).hide();
		$popup.data( 'insert-before', $( this ).hasClass( 'acf-fc-insert' ) ? $( this ).closest( '.layout' ) : null );
		$popup.toggle();
	} );
	$( document ).on( 'click', '.acf-fc-popup a', function ( e ) {
		e.preventDefault();
		var $popup = $( this ).closest( '.acf-fc-popup' ), $fc = $popup.closest( '.acf-flexible-content' );
		var name = $( this ).data( 'layout' ), max = parseInt( $fc.data( 'max' ), 10 ) || 0;
		var $values = $fc.children( '.values' );
		$popup.hide();
		if ( max > 0 && $values.children( '.layout' ).length >= max ) {
			window.alert( __( 'This field has a limit of {max} {label} {identifier}', { max: max, label: __( 'layouts' ), identifier: '' } ).trim() );
			return;
		}
		var lmax = parseInt( $( this ).data( 'max' ), 10 ) || 0;
		if ( lmax > 0 && $values.children( '.layout[data-layout="' + name + '"]' ).length >= lmax ) {
			window.alert( 'This layout has a limit of ' + lmax );
			return;
		}
		var tpl = $fc.children( '.clones' ).children( 'template[data-layout="' + name + '"]' )[ 0 ];
		var $layout = addFromTemplate( tpl, $fc.data( 'placeholder' ) );
		var $before = $popup.data( 'insert-before' );
		if ( $before && $before.length ) {
			$layout.insertBefore( $before );
		} else {
			$values.append( $layout );
		}
		init( $layout[ 0 ] );
		fcState( $fc );
	} );
	$( document ).on( 'click', '.acf-fc-remove', function ( e ) {
		e.preventDefault();
		var $fc = $( this ).closest( '.acf-flexible-content' );
		$( this ).closest( '.layout' ).remove();
		fcState( $fc );
	} );
	$( document ).on( 'click', '.acf-fc-duplicate', function ( e ) {
		e.preventDefault();
		var $fc = $( this ).closest( '.acf-flexible-content' ), $l = $( this ).closest( '.layout' );
		var $copy = $l.clone();
		renameRow( $copy, $fc.data( 'name' ), $l.attr( 'data-id' ), newRowID() );
		$copy.insertAfter( $l );
		init( $copy[ 0 ] );
		fcState( $fc );
	} );
	$( document ).on( 'click', '.acf-fc-collapse, .acf-fc-layout-handle', function ( e ) {
		e.preventDefault();
		$( this ).closest( '.layout' ).toggleClass( '-collapsed' );
	} );
	$( document ).on( 'click', function ( e ) {
		if ( ! $( e.target ).closest( '.acf-fc-popup, .acf-fc-add, .acf-fc-insert' ).length ) {
			$( '.acf-fc-popup' ).hide();
		}
	} );

	/* Media: image, file, gallery ----------------------------------------------------------------- */

	function mediaFrame( $el, multiple, cb ) {
		if ( ! window.wp || ! wp.media ) {
			window.alert( 'The media library is not available on this screen.' );
			return;
		}
		var type = $el.data( 'type' ), lib = {};
		if ( type === 'image' || type === 'gallery' ) {
			lib.type = 'image';
		}
		if ( $el.data( 'library' ) === 'uploadedTo' && wp.media.view.settings.post && wp.media.view.settings.post.id ) {
			lib.uploadedTo = wp.media.view.settings.post.id;
		}
		var frame = wp.media( {
			title: type === 'file' ? 'Select File' : ( type === 'gallery' ? 'Add Image to Gallery' : 'Select Image' ),
			multiple: multiple ? 'add' : false,
			library: lib,
			button: { text: 'Select' },
		} );
		frame.on( 'select', function () {
			cb( frame.state().get( 'selection' ).map( function ( a ) {
				return a.toJSON();
			} ) );
		} );
		frame.open();
	}

	function thumbURL( a, size ) {
		if ( a.sizes ) {
			return ( a.sizes[ size ] || a.sizes.thumbnail || a.sizes.medium || a.sizes.full || {} ).url || a.url;
		}
		return a.icon || a.url;
	}

	$( document ).on( 'click', '.acf-media-add, .acf-media-edit', function ( e ) {
		e.preventDefault();
		var $up = $( this ).closest( '.acf-image-uploader, .acf-file-uploader' );
		mediaFrame( $up, false, function ( list ) {
			var a = list[ 0 ];
			if ( ! a ) {
				return;
			}
			$up.find( '.acf-media-id' ).val( a.id ).trigger( 'change' );
			$up.find( '.acf-media-thumb' ).attr( 'src', a.type === 'image' ? thumbURL( a, $up.data( 'preview_size' ) ) : a.icon );
			$up.find( '.acf-media-title' ).text( a.title );
			$up.find( '.acf-media-link' ).attr( 'href', a.url ).text( a.filename );
			$up.addClass( 'has-value' );
		} );
	} );
	$( document ).on( 'click', '.acf-media-remove', function ( e ) {
		e.preventDefault();
		var $up = $( this ).closest( '.acf-image-uploader, .acf-file-uploader' );
		$up.find( '.acf-media-id' ).val( '' ).trigger( 'change' );
		$up.removeClass( 'has-value' );
	} );
	$( document ).on( 'click', '.acf-gallery-add', function ( e ) {
		e.preventDefault();
		var $g = $( this ).closest( '.acf-gallery' ), name = $g.data( 'name' ), max = parseInt( $g.data( 'max' ), 10 ) || 0;
		mediaFrame( $g, true, function ( list ) {
			var $wrap = $g.find( '.acf-gallery-attachments' );
			list.forEach( function ( a ) {
				if ( $wrap.find( '[data-id="' + a.id + '"]' ).length || ( max > 0 && $wrap.children().length >= max ) ) {
					return;
				}
				var $item = $( '<div class="acf-gallery-attachment"><input type="hidden" /><img alt="" /><a href="#" class="acf-icon -cancel dark acf-gallery-remove" title="Remove">&times;</a></div>' );
				$item.attr( 'data-id', a.id );
				$item.find( 'input' ).attr( 'name', name ).val( a.id );
				$item.find( 'img' ).attr( 'src', thumbURL( a, 'thumbnail' ) ).attr( 'title', a.title );
				$wrap.append( $item );
			} );
		} );
	} );
	$( document ).on( 'click', '.acf-gallery-remove', function ( e ) {
		e.preventDefault();
		$( this ).closest( '.acf-gallery-attachment' ).remove();
	} );

	/* Relationship -------------------------------------------------------------------------------- */

	function relSearch( $rel ) {
		var params = { action: 'acfgo/query_posts', field_key: $rel.data( 'key' ), s: $rel.find( '.acf-rel-search' ).val() || '', post_type: $rel.find( '.acf-rel-type' ).val() || '' };
		$.getJSON( ajaxurl, params, function ( r ) {
			var $list = $rel.find( '.choices-list' ).empty(), chosen = {};
			$rel.find( '.values-list input' ).each( function () {
				chosen[ this.value ] = true;
			} );
			( r.data || [] ).forEach( function ( it ) {
				var $li = $( '<li><span class="acf-rel-item"></span></li>' );
				$li.find( 'span' ).attr( 'data-id', it.id ).html( it.html ).toggleClass( 'disabled', !! chosen[ it.id ] );
				$list.append( $li );
			} );
		} );
	}

	/* Post Object / Page Link / User with "AJAX lazy load": search while typing ----------------------- */

	var selectTimer;
	$( document ).on( 'input', '.acf-select-ajax-search', function () {
		var $in = $( this ), $sel = $in.nextAll( 'select[data-ajax]' ).first();
		clearTimeout( selectTimer );
		selectTimer = setTimeout( function () {
			$.getJSON( ajaxurl, { action: 'acfgo/query_posts', field_key: $in.data( 'key' ), s: $in.val() || '' }, function ( r ) {
				$sel.children( 'option:not(:selected):not([value=""])' ).remove();
				( r.data || [] ).forEach( function ( it ) {
					if ( ! $sel.children( 'option' ).filter( function () {
						return this.value === String( it.id );
					} ).length ) {
						$sel.append( $( '<option></option>' ).val( it.id ).text( it.text ) );
					}
				} );
			} );
		}, 300 );
	} );

	var relTimer;
	$( document ).on( 'input change', '.acf-rel-search, .acf-rel-type', function () {
		var $rel = $( this ).closest( '.acf-relationship' );
		clearTimeout( relTimer );
		relTimer = setTimeout( function () {
			relSearch( $rel );
		}, 300 );
	} );
	$( document ).on( 'click', '.choices-list .acf-rel-item', function ( e ) {
		e.preventDefault();
		var $item = $( this ), $rel = $item.closest( '.acf-relationship' ), max = parseInt( $rel.data( 'max' ), 10 ) || 0;
		var $values = $rel.find( '.values-list' );
		if ( $item.hasClass( 'disabled' ) ) {
			return;
		}
		if ( max > 0 && $values.children().length >= max ) {
			window.alert( __( 'Maximum values reached ( {max} values )', { max: max } ) );
			return;
		}
		var $li = $( '<li><input type="hidden" /><span class="acf-rel-item"></span></li>' );
		$li.find( 'input' ).attr( 'name', $rel.data( 'name' ) ).val( $item.data( 'id' ) );
		$li.find( 'span' ).attr( 'data-id', $item.data( 'id' ) ).html( $item.html() ).append( '<a href="#" class="acf-icon -minus small dark" data-name="remove_item" title="Remove">&minus;</a>' );
		$values.append( $li );
		$item.addClass( 'disabled' );
	} );
	$( document ).on( 'click', '.values-list [data-name=remove_item]', function ( e ) {
		e.preventDefault();
		var $li = $( this ).closest( 'li' ), id = $li.find( 'input' ).val();
		$li.closest( '.acf-relationship' ).find( '.choices-list [data-id="' + id + '"]' ).removeClass( 'disabled' );
		$li.remove();
	} );

	/* Choices, toggles, small widgets ------------------------------------------------------------- */

	$( document ).on( 'change', '.acf-checkbox-toggle', function () {
		$( this ).closest( 'ul' ).find( 'input[type=checkbox]' ).not( this ).prop( 'checked', this.checked ).trigger( 'change' );
	} );
	$( document ).on( 'click', '.acf-add-checkbox', function ( e ) {
		e.preventDefault();
		var $li = $( '<li><input type="checkbox" checked /> <input type="text" class="acf-custom-value" /></li>' );
		$li.find( 'input[type=checkbox]' ).attr( 'name', $( this ).data( 'name' ) );
		$li.insertBefore( $( this ).closest( 'li' ) );
		$li.find( 'input[type=text]' ).trigger( 'focus' );
	} );
	$( document ).on( 'input', '.acf-custom-value', function () {
		$( this ).siblings( 'input[type=checkbox]' ).val( this.value );
	} );
	$( document ).on( 'change', '.acf-radio-list input[type=radio], .acf-button-group input[type=radio]', function () {
		var $ul = $( this ).closest( 'ul' );
		$ul.find( 'label' ).removeClass( 'selected' );
		$( this ).closest( 'label' ).addClass( 'selected' );
		$ul.find( '.acf-radio-other-text' ).toggle( $( this ).hasClass( 'acf-radio-other' ) );
	} );
	// allow_null button groups: clicking the selected button clears it
	$( document ).on( 'click', '.acf-button-group[data-allow-null="1"] label.selected input', function () {
		var input = this;
		setTimeout( function () {
			input.checked = false;
			$( input ).closest( 'label' ).removeClass( 'selected' );
			$( input ).trigger( 'change' );
		}, 0 );
	} );
	$( document ).on( 'input', '.acf-radio-other-text', function () {
		$( this ).closest( 'li' ).find( '.acf-radio-other' ).val( this.value );
	} );
	$( document ).on( 'change', '.acf-checkbox-list input[type=checkbox]', function () {
		$( this ).closest( 'label' ).toggleClass( 'selected', this.checked );
	} );
	$( document ).on( 'change', '.acf-switch-input', function () {
		$( this ).siblings( '.acf-switch' ).toggleClass( '-on', this.checked );
	} );
	$( document ).on( 'input change', '.acf-range-wrap input', function () {
		$( this ).siblings( 'input' ).val( this.value );
	} );
	$( document ).on( 'input', '.acf-color-swatch', function () {
		$( this ).siblings( '.acf-color-text' ).val( this.value ).trigger( 'change' );
	} );
	$( document ).on( 'input', '.acf-color-text', function () {
		if ( /^#[0-9a-f]{6}$/i.test( this.value ) ) {
			$( this ).siblings( '.acf-color-swatch' ).val( this.value );
		}
	} );
	$( document ).on( 'click', '.acf-add-term', function ( e ) {
		e.preventDefault();
		var $p = $( this ).closest( '.acf-taxonomy-add' ), $in = $p.find( '.acf-new-term' ), name = $.trim( $in.val() );
		if ( ! name ) {
			return;
		}
		var $input = $p.closest( '.acf-input' );
		var $list = $input.find( '.acf-checkbox-list' );
		var field = $( this ).data( 'name' );
		if ( $list.length ) {
			var multi = /\[\]$/.test( field );
			var $li = $( '<li><label><input checked /> </label></li>' );
			$li.find( 'input' ).attr( { type: multi ? 'checkbox' : 'radio', name: field, value: 'new:' + name } );
			$li.find( 'label' ).append( document.createTextNode( name ) );
			$list.append( $li );
		} else {
			var $sel = $input.find( 'select' ).first();
			$sel.append( $( '<option selected>' ).val( 'new:' + name ).text( name ) );
		}
		$in.val( '' );
	} );
	var oembedTimer;
	$( document ).on( 'input', '.acf-oembed-url', function () {
		var $f = $( this ), $canvas = $f.siblings( '.canvas-media' ), $wrap = $f.closest( '.acf-field' );
		clearTimeout( oembedTimer );
		oembedTimer = setTimeout( function () {
			$.getJSON( ajaxurl, { action: 'acfgo/oembed', url: $f.val() }, function ( r ) {
				$canvas.html( r && r.data ? r.data.html : '' );
			} );
		}, 500 );
		return $wrap;
	} );

	/* Icon picker ----------------------------------------------------------------------------------- */

	function setIcon( $p, type, value ) {
		$p.find( '.acf-icon-type' ).val( value ? type : '' );
		$p.find( '.acf-icon-value' ).val( value );
		var $prev = $p.find( '.acf-icon-preview' ).empty();
		if ( value && type === 'dashicons' ) {
			$prev.append( $( '<span class="dashicons"></span>' ).addClass( value ) );
		} else if ( value ) {
			$prev.append( $( '<img alt="" />' ).attr( 'src', value ) );
		}
	}
	$( document ).on( 'click', '.acf-icon-tab', function ( e ) {
		e.preventDefault();
		var $p = $( this ).closest( '.acf-icon-picker' );
		$p.find( '.acf-icon-tab' ).removeClass( 'active' );
		$( this ).addClass( 'active' );
		$p.find( '.acf-icon-panel' ).hide().filter( '[data-tab="' + $( this ).data( 'tab' ) + '"]' ).show();
	} );
	$( document ).on( 'click', '.acf-dashicons-list a', function ( e ) {
		e.preventDefault();
		var $p = $( this ).closest( '.acf-icon-picker' );
		$p.find( '.acf-dashicons-list a' ).removeClass( 'active' );
		$( this ).addClass( 'active' );
		setIcon( $p, 'dashicons', $( this ).data( 'icon' ) );
	} );
	$( document ).on( 'input', '.acf-icon-search', function () {
		var q = this.value.toLowerCase();
		$( this ).siblings( '.acf-dashicons-list' ).children().each( function () {
			$( this ).toggle( $( this ).find( 'a' ).data( 'icon' ).indexOf( q ) !== -1 );
		} );
	} );
	$( document ).on( 'input', '.acf-icon-url', function () {
		setIcon( $( this ).closest( '.acf-icon-picker' ), 'url', this.value );
	} );
	$( document ).on( 'click', '.acf-icon-media', function ( e ) {
		e.preventDefault();
		var $p = $( this ).closest( '.acf-icon-picker' );
		mediaFrame( $( '<div data-type="image"></div>' ), false, function ( list ) {
			if ( list[ 0 ] ) {
				setIcon( $p, 'media_library', list[ 0 ].url );
			}
		} );
	} );

	/* WYSIWYG ------------------------------------------------------------------------------------------ */

	function initEditors( root ) {
		$( root ).find( 'textarea.acfgo-wysiwyg' ).each( function () {
			var ta = this;
			if ( $( ta ).data( 'acf-editor' ) || $( ta ).closest( 'template' ).length ) {
				return;
			}
			$( ta ).data( 'acf-editor', true );
			var $wrap = $( ta ).closest( '.acf-editor-wrap' );
			var tabs = $wrap.data( 'tabs' );
			if ( window.tinymce && window.tinyMCEPreInit && tinyMCEPreInit.mceInit && tinyMCEPreInit.mceInit.content && tabs !== 'text' ) {
				var settings = $.extend( {}, tinyMCEPreInit.mceInit.content, { selector: '#' + ta.id, body_class: 'acf-editor', height: 250, wpautop: true } );
				delete settings.wp_autoresize_on;
				if ( $wrap.data( 'toolbar' ) === 'basic' ) {
					settings.toolbar1 = 'bold,italic,underline,blockquote,strikethrough,bullist,numlist,alignleft,aligncenter,alignright,undo,redo,link,fullscreen';
					settings.toolbar2 = '';
				}
				tinymce.init( settings );
				return;
			}
			// Formatting buttons for a plain textarea (quicktags style).
			var $bar = $( '<div class="acf-quicktags"></div>' ).insertBefore( ta );
			[ [ 'b', '<strong>', '</strong>' ], [ 'i', '<em>', '</em>' ], [ 'link', '<a href="">', '</a>' ], [ 'ul', '<ul>\n<li>', '</li>\n</ul>' ], [ 'ol', '<ol>\n<li>', '</li>\n</ol>' ], [ 'p', '<p>', '</p>' ] ].forEach( function ( b ) {
				$( '<button type="button" class="button button-small"></button>' ).text( b[ 0 ] ).appendTo( $bar ).on( 'click', function () {
					var s = ta.selectionStart, en = ta.selectionEnd, v = ta.value;
					ta.value = v.slice( 0, s ) + b[ 1 ] + v.slice( s, en ) + b[ 2 ] + v.slice( en );
					ta.focus();
				} );
			} );
		} );
	}

	/* Sortables ------------------------------------------------------------------------------------- */

	function initSortables( root ) {
		if ( ! $.fn.sortable ) {
			return;
		}
		$( root ).find( '.acf-repeater > table > tbody' ).addBack( '.acf-repeater > table > tbody' ).each( function () {
			if ( $( this ).data( 'ui-sortable' ) || $( this ).closest( 'template' ).length ) {
				return;
			}
			var $rep = $( this ).closest( '.acf-repeater' );
			$( this ).sortable( { items: '> .acf-row', handle: '> .acf-row-handle.order', forceHelperSize: true, forcePlaceholderSize: true, axis: 'y',
				stop: function () {
					repeaterState( $rep );
				} } );
		} );
		$( root ).find( '.acf-flexible-content > .values' ).each( function () {
			if ( $( this ).data( 'ui-sortable' ) ) {
				return;
			}
			var $fc = $( this ).closest( '.acf-flexible-content' );
			$( this ).sortable( { items: '> .layout', handle: '> .acf-fc-layout-handle', forcePlaceholderSize: true, axis: 'y',
				stop: function () {
					fcState( $fc );
				} } );
		} );
		$( root ).find( '.values-list' ).each( function () {
			if ( ! $( this ).data( 'ui-sortable' ) ) {
				$( this ).sortable( { items: 'li', axis: 'y' } );
			}
		} );
		$( root ).find( '.acf-gallery-attachments' ).each( function () {
			if ( ! $( this ).data( 'ui-sortable' ) ) {
				$( this ).sortable( { items: '.acf-gallery-attachment' } );
			}
		} );
	}

	/* Validation --------------------------------------------------------------------------------------- */

	function syncOrders( form ) {
		$( form ).find( '.acf-repeater' ).each( function () {
			renumber( $( this ).children( 'table' ).children( 'tbody' ), '.acf-row', '.acf-row-number' );
		} );
		$( form ).find( '.acf-flexible-content' ).each( function () {
			renumber( $( this ).children( '.values' ), '.layout', '.acf-fc-layout-order' );
		} );
		if ( window.tinymce ) {
			tinymce.triggerSave();
		}
	}

	function showErrors( form, errors ) {
		var $form = $( form );
		$form.find( '.acf-error-message' ).remove();
		$form.find( '.acf-field.acf-error' ).removeClass( 'acf-error' );
		errors.forEach( function ( err ) {
			var $input = $form.find( '[name="' + err.input + '"], [name="' + err.input + '[]"]' ).first();
			var $field = $input.closest( '.acf-field' );
			if ( ! $field.length ) {
				// a container (repeater, flexible content): its hidden placeholder input
				$field = $form.find( '[name="' + err.input + '"]' ).closest( '.acf-field' );
			}
			$field.addClass( 'acf-error' );
			$field.children( '.acf-input' ).prepend( $( '<div class="acf-notice -error acf-error-message"><p></p></div>' ).find( 'p' ).text( err.message ).end() );
		} );
		var $notice = $( '<div class="notice notice-error acf-error-message is-dismissible"><p><strong>Validation failed.</strong> ' + errors.length + ' field' + ( errors.length === 1 ? ' requires' : 's require' ) + ' attention.</p></div>' );
		$form.prepend( $notice );
		var $first = $form.find( '.acf-error' ).first();
		if ( $first.length ) {
			$( 'html, body' ).animate( { scrollTop: $first.offset().top - 80 }, 200 );
		}
	}

	function bindValidation() {
		var nonce = window.acfgoValidateNonce || $( '#acfgo-options-config' ).data( 'validate' );
		if ( ! nonce ) {
			return;
		}
		$( 'form' ).has( '.acf-field' ).each( function () {
			var form = this, validated = false, clicked = null;
			$( form ).on( 'click', 'input[type=submit], button[type=submit], button:not([type])', function () {
				clicked = this;
			} );
			$( form ).on( 'submit', function ( e ) {
				syncOrders( form );
				if ( validated || ( clicked && ( clicked.id === 'post-preview' || clicked.name === 'save' && $( clicked ).hasClass( 'acf-skip-validation' ) ) ) ) {
					return true;
				}
				e.preventDefault();
				var data = $( form ).find( '[name^="acf["]' ).not( ':disabled' ).serializeArray();
				data.push( { name: 'action', value: 'acfgo/validate_save_post' }, { name: '_acfnonce', value: nonce } );
				$.post( ajaxurl, $.param( data ), function ( r ) {
					if ( r && r.data && ! r.data.valid ) {
						showErrors( form, r.data.errors );
						$( form ).find( '.spinner' ).removeClass( 'is-active' );
						$( form ).find( '#publish, #save-post' ).removeClass( 'disabled' );
						return;
					}
					validated = true;
					if ( clicked && clicked.name ) {
						$( '<input type="hidden" />' ).attr( 'name', clicked.name ).val( clicked.value ).appendTo( form );
					}
					HTMLFormElement.prototype.submit.call( form );
				}, 'json' ).fail( function () {
					validated = true;
					HTMLFormElement.prototype.submit.call( form );
				} );
			} );
		} );
	}

	/* Init ---------------------------------------------------------------------------------------------- */

	function init( root ) {
		initTabs( root );
		initAccordions( root );
		initSortables( root );
		initEditors( root );
		$( root ).find( '.acf-repeater' ).addBack( '.acf-repeater' ).each( function () {
			repeaterState( $( this ) );
		} );
		$( root ).find( '.acf-flexible-content' ).each( function () {
			fcState( $( this ) );
		} );
		evaluate( document );
	}

	$( document ).on( 'change input', '.acf-field input, .acf-field select, .acf-field textarea', function () {
		evaluate( document );
	} );

	window.acfgoInit = init;
	window.acfgoSyncOrders = syncOrders;

	place();
	$( function () {
		$( '.acf-postbox, .acf-form-group' ).each( function () {
			init( this );
		} );
		bindValidation();
	} );
}( jQuery ) );
