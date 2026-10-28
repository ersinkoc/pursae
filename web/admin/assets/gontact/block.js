/* Gontact Form block for Gotenberg: contact-form-7/contact-form-selector. The saved markup is CF7's
 * (a div with the [contact-form-7] shortcode), so posts move between WordPress and WordPress Go. */
( function ( wp ) {
	'use strict';
	if ( ! wp || ! wp.blocks || ! wp.element ) {
		return;
	}
	var el = wp.element.createElement;
	var forms = ( window.wpcf7 && window.wpcf7.contactForms ) || [];
	var C = wp.components || {};
	var be = wp.blockEditor || wp.editor || {};

	function shortcode( a ) {
		return '[contact-form-7 id="' + ( a.hash || a.id ) + '" title="' + String( a.title || '' ).replace( /"/g, '' ) + '"]';
	}

	wp.blocks.registerBlockType( 'contact-form-7/contact-form-selector', {
		apiVersion: 3,
		title: 'Contact Form 7',
		description: 'Insert a contact form you have created with Gontact Form.',
		category: 'widgets',
		icon: 'email',
		keywords: [ 'form', 'contact', 'gontact' ],
		attributes: {
			id: { type: 'integer' },
			hash: { type: 'string' },
			title: { type: 'string' },
			htmlId: { type: 'string' },
			htmlName: { type: 'string' },
			htmlTitle: { type: 'string' },
			htmlClass: { type: 'string' },
			output: { type: 'string', enum: [ 'form', 'raw_form' ], default: 'form' }
		},
		supports: { html: false, customClassName: false },
		edit: function ( props ) {
			var a = props.attributes;
			var blockProps = be.useBlockProps ? be.useBlockProps( { className: 'components-placeholder' } ) : { className: 'components-placeholder' };
			if ( ! a.id && forms.length ) {
				setTimeout( function () {
					props.setAttributes( { id: forms[ 0 ].id, hash: forms[ 0 ].hash, title: forms[ 0 ].title } );
				} );
			}
			var options = forms.map( function ( f ) { return { label: f.title, value: String( f.id ) }; } );
			var select = C.SelectControl ? el( C.SelectControl, {
				label: 'Select a contact form:',
				value: a.id ? String( a.id ) : '',
				options: options,
				__nextHasNoMarginBottom: true,
				onChange: function ( v ) {
					var f = forms.filter( function ( x ) { return String( x.id ) === v; } )[ 0 ];
					if ( f ) {
						props.setAttributes( { id: f.id, hash: f.hash, title: f.title } );
					}
				}
			} ) : null;
			var inspector = be.InspectorControls && C.PanelBody && C.TextControl ? el( be.InspectorControls, {},
				el( C.PanelBody, { title: 'Form attributes', initialOpen: false },
					el( C.TextControl, { label: 'ID', value: a.htmlId || '', onChange: function ( v ) { props.setAttributes( { htmlId: v } ); } } ),
					el( C.TextControl, { label: 'Name', value: a.htmlName || '', onChange: function ( v ) { props.setAttributes( { htmlName: v } ); } } ),
					el( C.TextControl, { label: 'Title', value: a.htmlTitle || '', onChange: function ( v ) { props.setAttributes( { htmlTitle: v } ); } } ),
					el( C.TextControl, { label: 'Class', value: a.htmlClass || '', onChange: function ( v ) { props.setAttributes( { htmlClass: v } ); } } )
				) ) : null;
			return el( 'div', blockProps, inspector,
				el( 'div', { className: 'components-placeholder__label' }, el( 'span', { className: 'dashicons dashicons-email', style: { marginRight: '8px' } } ), 'Contact Form 7' ),
				forms.length ? select : el( 'p', {}, 'No contact forms were found. ', el( 'a', { href: window.wpcf7.adminURL.replace( 'wpcf7', 'wpcf7-new' ), target: '_blank' }, 'Create one' ), '.' )
			);
		},
		save: function ( props ) {
			var a = props.attributes;
			var code = shortcode( a ).slice( 0, -1 );
			if ( a.htmlId ) { code += ' html_id="' + a.htmlId + '"'; }
			if ( a.htmlName ) { code += ' html_name="' + a.htmlName + '"'; }
			if ( a.htmlTitle ) { code += ' html_title="' + a.htmlTitle + '"'; }
			if ( a.htmlClass ) { code += ' html_class="' + a.htmlClass + '"'; }
			if ( a.output === 'raw_form' ) { code += ' output="raw_form"'; }
			code += ']';
			var blockProps = be.useBlockProps && be.useBlockProps.save ? be.useBlockProps.save() : {};
			return el( 'div', blockProps, code );
		}
	} );
}( window.wp ) );
