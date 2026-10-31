/**
 * Pursae Custom Fields blocks in Pursae Editor: registers every ACF block (block.json with an "acf" key) and gives it
 * ACF's Edit / Preview modes. Edit mode shows the block's field form (rendered by the server);
 * every change is stored in the block's "data" attribute and the preview re-rendered.
 */
( function ( wp, $ ) {
	'use strict';
	if ( ! wp || ! wp.blocks || ! window.acfgoBlocks ) {
		return;
	}
	var el = wp.element.createElement, useState = wp.element.useState, useEffect = wp.element.useEffect, useRef = wp.element.useRef;
	var be = wp.blockEditor, cmp = wp.components;
	var ajaxurl = window.ajaxurl || '/wp-admin/admin-ajax';
	var nonce = window.acfgoBlocks.nonce;

	function post( action, data ) {
		return $.post( ajaxurl, $.extend( { action: action, nonce: nonce }, data ) );
	}

	function Form( props ) {
		var ref = useRef();
		useEffect( function () {
			var node = ref.current, timer;
			post( 'acfgo/block_form', { name: props.name, data: JSON.stringify( props.data || {} ) } ).done( function ( r ) {
				if ( ! node || ! r || ! r.data ) {
					return;
				}
				node.innerHTML = r.data.form;
				if ( window.acfgoInit ) {
					$( node ).children().each( function () {
						window.acfgoInit( this );
					} );
				}
			} );
			function changed() {
				clearTimeout( timer );
				timer = setTimeout( function () {
					if ( window.acfgoSyncOrders ) {
						window.acfgoSyncOrders( node );
					}
					// the form's acf[...] inputs, names kept exact, become the block's data
					var fields = $( node ).find( '[name^="acf["]' ).not( ':disabled' ).serializeArray();
					var body = $.param( fields.concat( [ { name: 'action', value: 'acfgo/block_preview' }, { name: 'nonce', value: nonce },
						{ name: 'name', value: props.name }, { name: 'data', value: '{}' }, { name: 'attributes', value: JSON.stringify( props.attributes ) } ] ) );
					$.post( ajaxurl, body ).done( function ( r ) {
						if ( r && r.data ) {
							props.onChange( r.data.data, r.data.html );
						}
					} );
				}, 400 );
			}
			$( node ).on( 'change input', changed );
			$( node ).on( 'click', '.acf-row-remove, .acf-fc-remove, .acf-media-remove, .acf-gallery-remove', function () {
				setTimeout( changed, 50 );
			} );
			return function () {
				$( node ).off();
				clearTimeout( timer );
			};
		}, [] );
		return el( 'div', { ref: ref, className: 'acf-block-fields acf-fields' }, el( cmp.Spinner ) );
	}

	function Preview( props ) {
		var st = useState( null ), html = st[ 0 ], setHTML = st[ 1 ];
		useEffect( function () {
			post( 'acfgo/block_preview', { name: props.name, data: JSON.stringify( props.data || {} ), attributes: JSON.stringify( props.attributes ) } ).done( function ( r ) {
				setHTML( r && r.data ? r.data.html : '' );
			} );
		}, [ JSON.stringify( props.data ) ] );
		if ( html === null ) {
			return el( cmp.Spinner );
		}
		return el( wp.element.RawHTML, { className: 'acf-block-preview' }, html || '<p class="acf-block-empty">' + props.title + '</p>' );
	}

	window.acfgoBlocks.blocks.forEach( function ( b ) {
		if ( wp.blocks.getBlockType( b.name ) ) {
			return;
		}
		wp.blocks.registerBlockType( b.name, {
			apiVersion: 3,
			title: b.title,
			description: b.description,
			category: b.category || 'common',
			icon: b.icon || 'block-default',
			keywords: b.keywords || [],
			supports: $.extend( { html: false, customClassName: true, align: true }, b.supports || {} ),
			attributes: {
				name: { type: 'string', default: b.name },
				data: { type: 'object', default: {} },
				mode: { type: 'string', default: b.mode || 'preview' },
				align: { type: 'string', default: '' },
				className: { type: 'string', default: '' },
			},
			edit: function ( props ) {
				var a = props.attributes, mode = a.mode === 'edit' ? 'edit' : 'preview';
				var blockProps = be.useBlockProps ? be.useBlockProps( { className: 'acf-block-component acf-block-' + mode } ) : { className: 'acf-block-component' };
				var toolbar = el( be.BlockControls, null, el( cmp.ToolbarGroup, null, el( cmp.ToolbarButton, {
					icon: mode === 'edit' ? 'visibility' : 'edit',
					label: mode === 'edit' ? 'Switch to Preview' : 'Switch to Edit',
					onClick: function () {
						props.setAttributes( { mode: mode === 'edit' ? 'preview' : 'edit' } );
					},
				} ) ) );
				var body = mode === 'edit' ?
					el( Form, { name: b.name, data: a.data, attributes: a, onChange: function ( data ) {
						props.setAttributes( { data: data } );
					} } ) :
					el( Preview, { name: b.name, title: b.title, data: a.data, attributes: a } );
				return el( 'div', blockProps, toolbar, body );
			},
			save: function () {
				return null;
			},
		} );
	} );
}( window.wp, jQuery ) );
