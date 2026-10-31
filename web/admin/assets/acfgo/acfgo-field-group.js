/**
 * Pursae Custom Fields field group editor: the field list (add, edit, duplicate, delete, drag to reorder, sub
 * fields and flexible content layouts), each field's settings in ACF's tabs (General, Validation,
 * Presentation, Conditional Logic), and the group's Location Rules, Presentation and Group
 * Settings. The whole group is serialized to ACF's JSON format on save.
 */
( function ( $ ) {
	'use strict';

	var schema, types = {}, group;

	// __ translates a message (wpgo-l10n.js), then the {placeholders} are filled in.
	function __( msg, vars ) {
		var out = window.wpgo__ ? window.wpgo__( msg ) : msg;
		Object.keys( vars || {} ).forEach( function ( k ) { out = out.split( '{' + k + '}' ).join( vars[ k ] ); } );
		return out;
	}

	function esc( s ) {
		return $( '<div>' ).text( s == null ? '' : String( s ) ).html();
	}
	function uniqid( prefix ) {
		var hex = Math.floor( Date.now() / 1000 ).toString( 16 );
		for ( var i = 0; i < 5; i++ ) {
			hex += Math.floor( Math.random() * 16 ).toString( 16 );
		}
		return prefix + hex;
	}
	function sanitizeName( label ) {
		return String( label ).toLowerCase().normalize( 'NFD' ).replace( /[̀-ͯ]/g, '' ).replace( /[^a-z0-9_\- ]/g, '' ).trim().replace( /[\s-]+/g, '_' );
	}
	function truthy( v ) {
		return v === true || v === 1 || v === '1' || v === 'true';
	}

	/* Choices <-> "value : label" text ----------------------------------------------------------- */

	function choicesToText( c ) {
		if ( ! c ) {
			return '';
		}
		if ( typeof c === 'string' ) {
			return c;
		}
		var lines = [];
		if ( Array.isArray( c ) ) {
			c.forEach( function ( x ) {
				var v = x && typeof x === 'object' ? x.value : x, l = x && typeof x === 'object' ? x.label : x;
				lines.push( String( v ) === String( l ) ? v : v + ' : ' + l );
			} );
		} else {
			Object.keys( c ).forEach( function ( k ) {
				lines.push( k === String( c[ k ] ) ? k : k + ' : ' + c[ k ] );
			} );
		}
		return lines.join( '\n' );
	}
	function textToChoices( t ) {
		var out = [];
		String( t || '' ).split( /\r?\n/ ).forEach( function ( line ) {
			line = line.trim();
			if ( ! line ) {
				return;
			}
			var i = line.indexOf( ' : ' );
			out.push( i === -1 ? { value: line, label: line } : { value: line.slice( 0, i ).trim(), label: line.slice( i + 3 ).trim() } );
		} );
		return out;
	}
	function choiceList( f ) {
		var c = f.choices;
		if ( typeof c === 'string' ) {
			return textToChoices( c );
		}
		if ( Array.isArray( c ) ) {
			return c.map( function ( x ) {
				return typeof x === 'object' ? x : { value: x, label: x };
			} );
		}
		return Object.keys( c || {} ).map( function ( k ) {
			return { value: k, label: c[ k ] };
		} );
	}

	/* Setting controls ------------------------------------------------------------------------------ */

	function sourceChoices( type ) {
		var src = schema.sources[ type ];
		return Array.isArray( src ) ? src : [];
	}

	// settingRow renders one setting as a table row of the field settings form.
	function settingRow( f, s ) {
		var v = f[ s.name ];
		if ( v === undefined ) {
			v = s.default;
		}
		var input = '', id = 'acfs-' + f.key + '-' + s.name;
		var attrs = ' id="' + id + '" data-setting="' + s.name + '" data-stype="' + s.type + '"';
		switch ( s.type ) {
			case 'textarea':
				input = '<textarea rows="4"' + attrs + '>' + esc( Array.isArray( v ) ? v.join( '\n' ) : v ) + '</textarea>';
				break;
			case 'choices':
				input = '<textarea rows="6" class="acf-choices"' + attrs + '>' + esc( choicesToText( v ) ) + '</textarea>';
				break;
			case 'number':
				input = '<input type="number" step="any"' + attrs + ' value="' + esc( v ) + '" />';
				break;
			case 'true_false':
				input = '<label class="acf-switch-label"><input type="checkbox"' + attrs + ( truthy( v ) ? ' checked' : '' ) + ' /><span class="acf-switch' + ( truthy( v ) ? ' -on' : '' ) + '"><span class="acf-switch-slider"></span></span></label>';
				break;
			case 'select':
			case 'taxonomies':
				var opts = s.type === 'select' ? s.choices : sourceChoices( 'taxonomies' );
				input = '<select' + attrs + '>' + opts.map( function ( c ) {
					return '<option value="' + esc( c.value ) + '"' + ( String( c.value ) === String( v == null ? '' : v ) ? ' selected' : '' ) + '>' + esc( c.label ) + '</option>';
				} ).join( '' ) + '</select>';
				break;
			case 'radio':
				input = '<ul class="acf-radio-list acf-hl"' + attrs + '>' + s.choices.map( function ( c ) {
					return '<li><label><input type="radio" name="' + id + '" value="' + esc( c.value ) + '"' + ( String( c.value ) === String( v ) ? ' checked' : '' ) + ' /> ' + esc( c.label ) + '</label></li>';
				} ).join( '' ) + '</ul>';
				break;
			case 'checkbox':
				var cur = Array.isArray( v ) ? v.map( String ) : ( v ? [ String( v ) ] : [] );
				input = '<ul class="acf-checkbox-list acf-hl"' + attrs + '>' + s.choices.map( function ( c ) {
					return '<li><label><input type="checkbox" value="' + esc( c.value ) + '"' + ( cur.indexOf( String( c.value ) ) !== -1 ? ' checked' : '' ) + ' /> ' + esc( c.label ) + '</label></li>';
				} ).join( '' ) + '</ul>';
				break;
			case 'post_types':
			case 'post_statuses':
			case 'terms':
			case 'roles':
			case 'groups':
				var sel = Array.isArray( v ) ? v.map( String ) : ( v ? [ String( v ) ] : [] );
				var lastGroup = null, html = '';
				sourceChoices( s.type ).forEach( function ( c ) {
					if ( c.group && c.group !== lastGroup ) {
						html += ( lastGroup ? '</optgroup>' : '' ) + '<optgroup label="' + esc( c.group ) + '">';
						lastGroup = c.group;
					}
					if ( s.type === 'groups' && c.value === group.key ) {
						return;
					}
					html += '<option value="' + esc( c.value ) + '"' + ( sel.indexOf( String( c.value ) ) !== -1 ? ' selected' : '' ) + '>' + esc( c.label ) + '</option>';
				} );
				input = '<select multiple size="5"' + attrs + '>' + html + ( lastGroup ? '</optgroup>' : '' ) + '</select><p class="description">Leave empty for all. Ctrl/⌘-click to select several.</p>';
				break;
			case 'sub_field_choice':
				input = '<select' + attrs + '><option value="">- Select -</option>' + ( f._subLabels || [] ).map( function ( c ) {
					return '<option value="' + esc( c.value ) + '"' + ( c.value === v ? ' selected' : '' ) + '>' + esc( c.label ) + '</option>';
				} ).join( '' ) + '</select>';
				break;
			case 'sub_fields':
				return '<tr class="acf-field-setting-sub_fields"><td colspan="2"><div class="acf-label"><label>Sub Fields</label></div><div class="acf-sub-fields" data-for="' + esc( f.key ) + '"></div></td></tr>';
			case 'layouts':
				return '<tr class="acf-field-setting-layouts"><td colspan="2"><div class="acf-label"><label>Layouts</label></div><div class="acf-fc-layouts" data-for="' + esc( f.key ) + '"></div><p><a href="#" class="button acf-add-layout">Add Layout</a></p></td></tr>';
			default:
				input = '<input type="text"' + attrs + ' value="' + esc( v ) + '" />';
		}
		var show = s.show_if ? ' data-show-if="' + esc( s.show_if.join( ',' ) ) + '"' : '';
		return '<tr class="acf-field-setting acf-field-setting-' + s.name + '"' + show + '><td class="acf-label"><label for="' + id + '">' + esc( s.label ) + '</label>' +
			( s.instructions ? '<p class="description">' + esc( s.instructions ).replace( /\n/g, '<br />' ) + '</p>' : '' ) + '</td><td class="acf-input">' + input + '</td></tr>';
	}

	function readSetting( $el ) {
		var t = $el.data( 'stype' );
		switch ( t ) {
			case 'true_false':
				return $el.prop( 'checked' ) ? 1 : 0;
			case 'choices':
				return textToChoices( $el.val() );
			case 'radio':
				return $el.find( 'input:checked' ).val() || '';
			case 'checkbox':
				return $el.find( 'input:checked' ).map( function () {
					return this.value;
				} ).get();
			case 'post_types':
			case 'post_statuses':
			case 'terms':
			case 'roles':
			case 'groups':
				return $el.val() || [];
		}
		return $el.val();
	}

	/* Field objects ---------------------------------------------------------------------------------- */

	var commonGeneral = [
		{ name: 'label', label: 'Field Label', type: 'text', instructions: 'This is the name which will appear on the EDIT page' },
		{ name: 'name', label: 'Field Name', type: 'text', instructions: 'Single word, no spaces. Underscores and dashes allowed' },
	];
	var commonPresentation = [
		{ name: 'instructions', label: 'Instructions', type: 'textarea', instructions: 'Instructions for content editors. Shown when submitting data.' },
	];

	function typeSelect( f ) {
		var cats = {}, order = [];
		schema.types.forEach( function ( t ) {
			if ( ! cats[ t.category ] ) {
				cats[ t.category ] = [];
				order.push( t.category );
			}
			cats[ t.category ].push( t );
		} );
		return '<select class="acf-field-type-select" data-setting="type">' + order.map( function ( c ) {
			return '<optgroup label="' + esc( c ) + '">' + cats[ c ].map( function ( t ) {
				return '<option value="' + t.name + '"' + ( t.name === f.type ? ' selected' : '' ) + '>' + esc( t.label ) + '</option>';
			} ).join( '' ) + '</optgroup>';
		} ).join( '' ) + '</select>';
	}

	function fieldObject( f ) {
		f = $.extend( { label: '', name: '', type: 'text', instructions: '', required: 0, conditional_logic: 0, wrapper: { width: '', class: '', id: '' } }, f );
		if ( ! f.key ) {
			f.key = uniqid( 'field_' );
		}
		var $li = $( '<li class="acf-field-object"></li>' ).attr( 'data-key', f.key ).attr( 'data-type', f.type );
		$li.data( 'field', f );
		$li.append(
			'<div class="handle"><ul class="acf-hl acf-tbody">' +
			'<li class="li-field-order"><span class="acf-icon acf-sortable-handle" title="Drag to reorder"></span></li>' +
			'<li class="li-field-label"><strong><a class="edit-field" href="#"></a></strong><div class="row-options"><a class="edit-field" href="#">Edit</a> <a class="duplicate-field" href="#">Duplicate</a> <a class="delete-field" href="#">Delete</a></div></li>' +
			'<li class="li-field-name"></li><li class="li-field-key"><code></code></li><li class="li-field-type"></li></ul></div>' +
			'<div class="settings" style="display:none"></div>'
		);
		refreshHandle( $li );
		return $li;
	}

	function refreshHandle( $li ) {
		var f = $li.data( 'field' ), t = types[ f.type ] || { label: f.type };
		$li.find( '> .handle .li-field-label > strong > a' ).text( f.label || '(no label)' );
		$li.find( '> .handle .li-field-name' ).text( f.name );
		$li.find( '> .handle .li-field-key code' ).text( f.key );
		$li.find( '> .handle .li-field-type' ).text( t.label );
		$li.attr( 'data-type', f.type ).toggleClass( 'acf-field-required', truthy( f.required ) );
	}

	function renderSettings( $li ) {
		var f = $li.data( 'field' ), t = types[ f.type ] || { settings: [] };
		var tabs = { general: [], validation: [], presentation: [] };
		tabs.general.push( '<tr class="acf-field-setting acf-field-setting-type"><td class="acf-label"><label>Field Type</label></td><td class="acf-input">' + typeSelect( f ) + '</td></tr>' );
		commonGeneral.forEach( function ( s ) {
			tabs.general.push( settingRow( f, s ) );
		} );
		tabs.validation.push( settingRow( f, { name: 'required', label: 'Required', type: 'true_false' } ) );
		f._subLabels = ( f.sub_fields || [] ).map( function ( sf ) {
			return { value: sf.key, label: sf.label || sf.name };
		} );
		t.settings.forEach( function ( s ) {
			( tabs[ s.tab ] || tabs.general ).push( settingRow( f, s ) );
		} );
		commonPresentation.forEach( function ( s ) {
			tabs.presentation.push( settingRow( f, s ) );
		} );
		var w = f.wrapper || {};
		tabs.presentation.push( '<tr class="acf-field-setting acf-field-setting-wrapper"><td class="acf-label"><label>Wrapper Attributes</label></td><td class="acf-input acf-wrapper-inputs">' +
			'<label>width <input type="number" min="0" max="100" data-wrapper="width" value="' + esc( w.width ) + '" />%</label> ' +
			'<label>class <input type="text" data-wrapper="class" value="' + esc( w.class ) + '" /></label> ' +
			'<label>id <input type="text" data-wrapper="id" value="' + esc( w.id ) + '" /></label></td></tr>' );
		var html = '<ul class="acf-hl acf-tab-group acf-settings-tabs">' +
			'<li class="active"><a href="#" data-tab="general">General</a></li><li><a href="#" data-tab="validation">Validation</a></li>' +
			'<li><a href="#" data-tab="presentation">Presentation</a></li><li><a href="#" data-tab="conditional">Conditional Logic</a></li></ul>';
		Object.keys( tabs ).forEach( function ( k ) {
			html += '<table class="form-table acf-field-settings-tab" data-tab="' + k + '"' + ( k === 'general' ? '' : ' style="display:none"' ) + '><tbody>' + tabs[ k ].join( '' ) + '</tbody></table>';
		} );
		html += '<div class="acf-field-settings-tab acf-conditional-logic" data-tab="conditional" style="display:none"></div>';
		html += '<div class="acf-field-settings-footer"><a href="#" class="button close-field">Close Field</a></div>';
		var $s = $li.children( '.settings' ).html( html );
		renderConditional( $li );
		// sub fields and layouts
		$s.find( '> table .acf-sub-fields' ).each( function () {
			$( this ).append( fieldList( f.sub_fields || [] ) );
		} );
		$s.find( '> table .acf-fc-layouts' ).each( function () {
			var $wrap = $( this );
			( f.layouts && ! Array.isArray( f.layouts ) ? Object.values( f.layouts ) : ( f.layouts || [] ) ).forEach( function ( l ) {
				$wrap.append( layoutObject( l ) );
			} );
			if ( ! $wrap.children().length ) {
				$wrap.append( layoutObject( {} ) );
			}
			$wrap.sortable && $wrap.sortable( { items: '> .acf-fc-layout', handle: '.acf-fc-layout-head', axis: 'y' } );
		} );
		applyShowIf( $s );
	}

	// own finds the elements of a field's settings, not those of its sub fields.
	function own( $s, sel ) {
		var li = $s.closest( '.acf-field-object' )[ 0 ];
		return $s.find( sel ).filter( function () {
			return $( this ).closest( '.acf-field-object' )[ 0 ] === li;
		} );
	}

	function applyShowIf( $s ) {
		$s.find( '> table > tbody > tr[data-show-if]' ).each( function () {
			var $tr = $( this ), ok = true;
			$tr.data( 'show-if' ).split( ',' ).forEach( function ( cond ) {
				var p = cond.split( '=' ), $in = own( $s, '> table [data-setting="' + p[ 0 ] + '"]' ).first();
				var v = $in.length ? readSetting( $in ) : '';
				ok = ok && String( v ) === p[ 1 ];
			} );
			$tr.toggle( ok );
		} );
	}

	function fieldList( fields ) {
		var $wrap = $( '<div class="acf-field-list-wrap"></div>' );
		$wrap.append( '<ul class="acf-hl acf-thead"><li class="li-field-order"></li><li class="li-field-label">Label</li><li class="li-field-name">Name</li><li class="li-field-key">Key</li><li class="li-field-type">Type</li></ul>' );
		var $ul = $( '<ul class="acf-field-list"></ul>' );
		( fields || [] ).forEach( function ( f ) {
			$ul.append( fieldObject( f ) );
		} );
		$wrap.append( $ul );
		$wrap.append( '<div class="no-fields-message"' + ( fields && fields.length ? ' style="display:none"' : '' ) + '>No fields. Click the <strong>+ Add Field</strong> button to create your first field.</div>' );
		$wrap.append( '<ul class="acf-hl acf-tfoot"><li class="acf-fr"><a href="#" class="button button-primary add-field">+ Add Field</a></li></ul>' );
		if ( $ul.sortable ) {
			$ul.sortable( { items: '> li.acf-field-object', handle: '.acf-sortable-handle, .li-field-order', axis: 'y', connectWith: '.acf-field-list', forcePlaceholderSize: true,
				stop: function () {
					updateEmpty();
				} } );
		}
		return $wrap;
	}

	function updateEmpty() {
		$( '.acf-field-list-wrap' ).each( function () {
			$( this ).children( '.no-fields-message' ).toggle( ! $( this ).children( '.acf-field-list' ).children().length );
		} );
	}

	function layoutObject( l ) {
		l = $.extend( { key: uniqid( 'layout_' ), name: '', label: '', display: 'block', sub_fields: [], min: '', max: '' }, l );
		var $l = $( '<div class="acf-fc-layout"></div>' ).attr( 'data-key', l.key );
		$l.append( '<div class="acf-fc-layout-head"><span class="acf-sortable-handle"></span> <strong class="acf-fc-layout-title"></strong> ' +
			'<span class="acf-fc-layout-actions"><a href="#" class="acf-duplicate-layout">Duplicate</a> <a href="#" class="acf-delete-layout">Delete</a></span></div>' +
			'<table class="form-table"><tbody>' +
			'<tr><td class="acf-label"><label>Label</label></td><td><input type="text" data-layout="label" /></td>' +
			'<td class="acf-label"><label>Name</label></td><td><input type="text" data-layout="name" /></td></tr>' +
			'<tr><td class="acf-label"><label>Layout</label></td><td><select data-layout="display"><option value="table">Table</option><option value="block">Block</option><option value="row">Row</option></select></td>' +
			'<td class="acf-label"><label>Min / Max</label></td><td><input type="number" min="0" data-layout="min" class="small-text" /> <input type="number" min="0" data-layout="max" class="small-text" /></td></tr>' +
			'</tbody></table>' );
		[ 'label', 'name', 'display', 'min', 'max' ].forEach( function ( k ) {
			$l.find( '[data-layout="' + k + '"]' ).val( l[ k ] );
		} );
		$l.find( '.acf-fc-layout-title' ).text( l.label || '(no label)' );
		$l.append( fieldList( l.sub_fields ) );
		return $l;
	}

	/* Conditional logic ------------------------------------------------------------------------------- */

	function allFields() {
		var out = [];
		$( '#acfgo-fields-editor .acf-field-object' ).each( function () {
			var f = $( this ).data( 'field' );
			if ( f && [ 'tab', 'accordion', 'message', 'repeater', 'flexible_content', 'group', 'clone' ].indexOf( f.type ) === -1 ) {
				out.push( f );
			}
		} );
		return out;
	}

	function operatorsFor( f ) {
		var any = [ [ '!=empty', 'Has any value' ], [ '==empty', 'Has no value' ] ];
		switch ( f && f.type ) {
			case 'true_false':
				return [ [ '!=empty', 'Checked' ], [ '==empty', 'Unchecked' ] ];
			case 'select':
			case 'checkbox':
			case 'radio':
			case 'button_group':
				return [ [ '==', 'Value is equal to' ], [ '!=', 'Value is not equal to' ] ].concat( any );
			case 'number':
			case 'range':
				return [ [ '==', 'Value is equal to' ], [ '!=', 'Value is not equal to' ], [ '>', 'Value is greater than' ], [ '<', 'Value is less than' ] ].concat( any );
		}
		return [ [ '==', 'Value is equal to' ], [ '!=', 'Value is not equal to' ], [ '==pattern', 'Value matches pattern' ], [ '==contains', 'Value contains' ] ].concat( any );
	}

	function ruleRow( self, rule ) {
		var fields = allFields().filter( function ( x ) {
			return x.key !== self.key;
		} );
		rule = rule || { field: fields[ 0 ] ? fields[ 0 ].key : '', operator: '==', value: '' };
		var target = fields.filter( function ( x ) {
			return x.key === rule.field;
		} )[ 0 ];
		var html = '<tr class="acf-cl-rule"><td><select class="cl-field">' + fields.map( function ( x ) {
			return '<option value="' + esc( x.key ) + '"' + ( x.key === rule.field ? ' selected' : '' ) + '>' + esc( x.label || x.name ) + '</option>';
		} ).join( '' ) + '</select></td><td><select class="cl-operator">' + operatorsFor( target ).map( function ( o ) {
			return '<option value="' + o[ 0 ] + '"' + ( o[ 0 ] === rule.operator ? ' selected' : '' ) + '>' + o[ 1 ] + '</option>';
		} ).join( '' ) + '</select></td><td class="cl-value">';
		var hideVal = rule.operator === '!=empty' || rule.operator === '==empty';
		if ( target && [ 'select', 'checkbox', 'radio', 'button_group' ].indexOf( target.type ) !== -1 ) {
			html += '<select class="cl-value-input"' + ( hideVal ? ' style="display:none"' : '' ) + '>' + choiceList( target ).map( function ( c ) {
				return '<option value="' + esc( c.value ) + '"' + ( String( c.value ) === String( rule.value ) ? ' selected' : '' ) + '>' + esc( c.label ) + '</option>';
			} ).join( '' ) + '</select>';
		} else {
			html += '<input type="text" class="cl-value-input" value="' + esc( rule.value ) + '"' + ( hideVal ? ' style="display:none"' : '' ) + ' />';
		}
		html += '</td><td class="add"><a href="#" class="button cl-add-rule">and</a></td><td class="remove"><a href="#" class="acf-icon -minus cl-remove-rule" title="Remove">&minus;</a></td></tr>';
		return html;
	}

	function renderConditional( $li ) {
		var f = $li.data( 'field' ), $c = $li.find( '> .settings > .acf-conditional-logic' );
		var groups = Array.isArray( f.conditional_logic ) ? f.conditional_logic : [];
		var html = '<table class="form-table"><tr><td class="acf-label"><label>Conditional Logic</label></td><td><label class="acf-switch-label"><input type="checkbox" class="cl-enabled"' + ( groups.length ? ' checked' : '' ) + ' /><span class="acf-switch' + ( groups.length ? ' -on' : '' ) + '"><span class="acf-switch-slider"></span></span></label></td></tr></table>';
		html += '<div class="cl-groups"' + ( groups.length ? '' : ' style="display:none"' ) + '><p>Show this field if</p>';
		( groups.length ? groups : [ [ null ] ] ).forEach( function ( rules, i ) {
			html += '<div class="cl-group">' + ( i ? '<h4>or</h4>' : '' ) + '<table class="acf-table"><tbody>' + rules.map( function ( r ) {
				return ruleRow( f, r );
			} ).join( '' ) + '</tbody></table></div>';
		} );
		html += '<p><a href="#" class="button cl-add-group">Add rule group</a></p></div>';
		$c.html( html );
	}

	function readConditional( $li ) {
		var $c = $li.find( '> .settings > .acf-conditional-logic' );
		if ( ! $c.length ) {
			return $li.data( 'field' ).conditional_logic || 0;
		}
		if ( ! $c.find( '.cl-enabled' ).prop( 'checked' ) ) {
			return 0;
		}
		var groups = [];
		$c.find( '.cl-group' ).each( function () {
			var rules = [];
			$( this ).find( '.acf-cl-rule' ).each( function () {
				var $r = $( this ), field = $r.find( '.cl-field' ).val();
				if ( field ) {
					rules.push( { field: field, operator: $r.find( '.cl-operator' ).val(), value: $r.find( '.cl-value-input' ).val() || '' } );
				}
			} );
			if ( rules.length ) {
				groups.push( rules );
			}
		} );
		return groups.length ? groups : 0;
	}

	/* Location rules and group settings ----------------------------------------------------------------- */

	function paramSelect( v ) {
		var html = '<select class="loc-param">', last = '';
		schema.locationParams.forEach( function ( p ) {
			if ( p.group !== last ) {
				html += ( last ? '</optgroup>' : '' ) + '<optgroup label="' + esc( p.group ) + '">';
				last = p.group;
			}
			html += '<option value="' + p.value + '"' + ( p.value === v ? ' selected' : '' ) + '>' + esc( p.label ) + '</option>';
		} );
		return html + '</optgroup></select>';
	}

	function valueSelect( param, v ) {
		var list = schema.locationValues[ param ] || [], last = '', html = '<select class="loc-value">';
		list.forEach( function ( c ) {
			if ( c.group && c.group !== last ) {
				html += ( last ? '</optgroup>' : '' ) + '<optgroup label="' + esc( c.group ) + '">';
				last = c.group;
			}
			html += '<option value="' + esc( c.value ) + '"' + ( String( c.value ) === String( v ) ? ' selected' : '' ) + '>' + esc( c.label ) + '</option>';
		} );
		return html + ( last ? '</optgroup>' : '' ) + '</select>';
	}

	function locRow( r ) {
		r = r || { param: 'post_type', operator: '==', value: 'post' };
		return '<tr class="loc-rule"><td class="param">' + paramSelect( r.param ) + '</td><td class="operator"><select class="loc-operator"><option value="=="' + ( r.operator === '==' ? ' selected' : '' ) + '>is equal to</option><option value="!="' + ( r.operator === '!=' ? ' selected' : '' ) + '>is not equal to</option></select></td>' +
			'<td class="value">' + valueSelect( r.param, r.value ) + '</td><td class="add"><a href="#" class="button loc-add-rule">and</a></td><td class="remove"><a href="#" class="acf-icon -minus loc-remove-rule" title="Remove">&minus;</a></td></tr>';
	}

	function locGroup( rules, i ) {
		return '<div class="rule-group">' + ( i ? '<h4>or</h4>' : '' ) + '<table class="acf-table"><tbody>' + rules.map( locRow ).join( '' ) + '</tbody></table></div>';
	}

	function radio( name, v, choices ) {
		return '<ul class="acf-radio-list acf-hl">' + choices.map( function ( c ) {
			return '<li><label><input type="radio" name="' + name + '" value="' + c[ 0 ] + '"' + ( c[ 0 ] === v ? ' checked' : '' ) + ' /> ' + c[ 1 ] + '</label></li>';
		} ).join( '' ) + '</ul>';
	}

	function renderGroupSettings() {
		var g = group, loc = g.location && g.location.length ? g.location : [ [ { param: 'post_type', operator: '==', value: 'post' } ] ];
		var hide = g.hide_on_screen || [];
		var html = '<ul class="acf-hl acf-tab-group acf-group-tabs"><li class="active"><a href="#" data-tab="location">Location Rules</a></li><li><a href="#" data-tab="presentation">Presentation</a></li><li><a href="#" data-tab="settings">Group Settings</a></li></ul>';
		html += '<div class="acf-group-tab" data-tab="location"><p class="acf-location-intro"><strong>Rules</strong><br />Create a set of rules to determine which edit screens will use these advanced custom fields</p>' +
			'<p>Show this field group if</p><div class="rule-groups">' + loc.map( locGroup ).join( '' ) + '</div><p><a href="#" class="button loc-add-group">Add rule group</a></p></div>';
		html += '<div class="acf-group-tab" data-tab="presentation" style="display:none"><table class="form-table"><tbody>' +
			'<tr><th>Style</th><td>' + radio( 'acf-style', g.style || 'default', [ [ 'default', 'Standard (WP metabox)' ], [ 'seamless', 'Seamless (no metabox)' ] ] ) + '</td></tr>' +
			'<tr><th>Position</th><td>' + radio( 'acf-position', g.position || 'normal', [ [ 'acf_after_title', 'High (after title)' ], [ 'normal', 'Normal (after content)' ], [ 'side', 'Side' ] ] ) + '</td></tr>' +
			'<tr><th>Label Placement</th><td>' + radio( 'acf-label-placement', g.label_placement || 'top', [ [ 'top', 'Top aligned' ], [ 'left', 'Left aligned' ] ] ) + '</td></tr>' +
			'<tr><th>Instruction Placement</th><td>' + radio( 'acf-instruction-placement', g.instruction_placement || 'label', [ [ 'label', 'Below labels' ], [ 'field', 'Below fields' ], [ 'tooltip', 'Tooltip' ] ] ) + '</td></tr>' +
			'<tr><th><label for="acf-menu-order">Order No.</label></th><td><input type="number" id="acf-menu-order" value="' + esc( g.menu_order || 0 ) + '" class="small-text" /><p class="description">Field groups with a lower order will appear first</p></td></tr>' +
			'<tr><th>Hide on screen</th><td><p class="description">Select items to hide them from the edit screen.</p><ul class="acf-checkbox-list acf-bl acf-hide-on-screen">' + schema.hideOnScreen.map( function ( c ) {
				return '<li><label><input type="checkbox" value="' + c.value + '"' + ( hide.indexOf( c.value ) !== -1 ? ' checked' : '' ) + ' /> ' + esc( c.label ) + '</label></li>';
			} ).join( '' ) + '</ul></td></tr></tbody></table></div>';
		html += '<div class="acf-group-tab" data-tab="settings" style="display:none"><table class="form-table"><tbody>' +
			'<tr><th>Show In REST API</th><td><label><input type="checkbox" id="acf-show-in-rest"' + ( truthy( g.show_in_rest ) ? ' checked' : '' ) + ' /> Expose this field group\'s fields in the REST API (the "acf" field)</label></td></tr>' +
			'<tr><th><label for="acf-description">Description</label></th><td><input type="text" id="acf-description" class="large-text" value="' + esc( g.description ) + '" /><p class="description">Shown in field group list</p></td></tr>' +
			'</tbody></table></div>';
		$( '#acfgo-settings-editor' ).html( html );
	}

	/* Serialization ------------------------------------------------------------------------------------- */

	function readField( $li ) {
		var f = $.extend( {}, $li.data( 'field' ) );
		var $s = $li.children( '.settings' );
		if ( $s.children().length ) {
			own( $s, '> table [data-setting]' ).each( function () {
				var $el = $( this ), name = $el.data( 'setting' );
				if ( name === 'type' ) {
					return;
				}
				var v = readSetting( $el );
				if ( name === 'default_value' && [ 'checkbox' ].indexOf( f.type ) !== -1 || ( name === 'default_value' && f.type === 'select' && truthy( f.multiple ) ) ) {
					v = String( v || '' ).split( /\r?\n/ ).filter( Boolean );
				}
				f[ name ] = v;
			} );
			f.wrapper = { width: own( $s, '[data-wrapper=width]' ).val() || '', class: own( $s, '[data-wrapper=class]' ).val() || '', id: own( $s, '[data-wrapper=id]' ).val() || '' };
			f.conditional_logic = readConditional( $li );
		}
		if ( ! f.name && f.label ) {
			f.name = sanitizeName( f.label );
		}
		delete f._subLabels;
		var $subs = $s.find( '> table .acf-sub-fields' ).first();
		if ( $subs.length ) {
			f.sub_fields = readList( $subs.children( '.acf-field-list-wrap' ).children( '.acf-field-list' ) );
		}
		var $layouts = $s.find( '> table .acf-fc-layouts' ).first();
		if ( $layouts.length ) {
			f.layouts = $layouts.children( '.acf-fc-layout' ).map( function () {
				var $l = $( this ), l = { key: $l.data( 'key' ) };
				[ 'label', 'name', 'display', 'min', 'max' ].forEach( function ( k ) {
					l[ k ] = $l.find( '> table [data-layout="' + k + '"]' ).val();
				} );
				if ( ! l.name ) {
					l.name = sanitizeName( l.label );
				}
				l.sub_fields = readList( $l.children( '.acf-field-list-wrap' ).children( '.acf-field-list' ) );
				return l;
			} ).get();
		}
		return f;
	}

	function readList( $ul ) {
		return $ul.children( '.acf-field-object' ).map( function ( i ) {
			var f = readField( $( this ) );
			f.menu_order = i;
			return f;
		} ).get();
	}

	function serialize() {
		var g = $.extend( {}, group );
		g.title = $( '#title' ).val();
		g.fields = readList( $( '#acfgo-fields-editor > .acf-field-list-wrap > .acf-field-list' ) );
		var loc = [];
		$( '#acfgo-settings-editor .rule-group' ).each( function () {
			var rules = [];
			$( this ).find( '.loc-rule' ).each( function () {
				rules.push( { param: $( this ).find( '.loc-param' ).val(), operator: $( this ).find( '.loc-operator' ).val(), value: $( this ).find( '.loc-value' ).val() || '' } );
			} );
			if ( rules.length ) {
				loc.push( rules );
			}
		} );
		g.location = loc;
		var r = function ( n ) {
			return $( '#acfgo-settings-editor input[name="' + n + '"]:checked' ).val();
		};
		g.style = r( 'acf-style' );
		g.position = r( 'acf-position' );
		g.label_placement = r( 'acf-label-placement' );
		g.instruction_placement = r( 'acf-instruction-placement' );
		g.menu_order = parseInt( $( '#acf-menu-order' ).val(), 10 ) || 0;
		g.hide_on_screen = $( '.acf-hide-on-screen input:checked' ).map( function () {
			return this.value;
		} ).get();
		g.show_in_rest = $( '#acf-show-in-rest' ).prop( 'checked' ) ? 1 : 0;
		g.description = $( '#acf-description' ).val();
		g.active = $( '#acf-active' ).prop( 'checked' );
		return g;
	}

	// validateFields checks labels and names like ACF does before saving.
	function validateFields( g ) {
		var errors = [];
		( function walk( fields, path ) {
			var names = {};
			fields.forEach( function ( f ) {
				if ( [ 'tab', 'accordion', 'message' ].indexOf( f.type ) === -1 ) {
					if ( ! f.label && ! f.name ) {
						errors.push( __( 'Every field needs a label ({path}).', { path: path } ) );
					}
					if ( f.name && names[ f.name ] ) {
						errors.push( __( 'The field name "{name}" is used twice ({path}).', { name: f.name, path: path } ) );
					}
					names[ f.name ] = true;
				}
				walk( f.sub_fields || [], ( f.label || f.name ) + ' › sub fields' );
				( f.layouts || [] ).forEach( function ( l ) {
					walk( l.sub_fields || [], ( f.label || f.name ) + ' › ' + ( l.label || l.name ) );
				} );
			} );
		}( g.fields, 'top level' ) );
		return errors;
	}

	/* Events ---------------------------------------------------------------------------------------------- */

	function bind() {
		var $doc = $( document );
		$doc.on( 'click', '#acfgo-fields-editor .add-field', function ( e ) {
			e.preventDefault();
			var $ul = $( this ).closest( '.acf-field-list-wrap' ).children( '.acf-field-list' );
			var $li = fieldObject( { label: '', type: 'text' } ).appendTo( $ul );
			updateEmpty();
			openField( $li );
			$li.find( '[data-setting=label]' ).first().trigger( 'focus' );
		} );
		$doc.on( 'click', '#acfgo-fields-editor .edit-field', function ( e ) {
			e.preventDefault();
			var $li = $( this ).closest( '.acf-field-object' );
			if ( $li.hasClass( 'open' ) ) {
				closeField( $li );
			} else {
				openField( $li );
			}
		} );
		$doc.on( 'click', '#acfgo-fields-editor .close-field', function ( e ) {
			e.preventDefault();
			closeField( $( this ).closest( '.acf-field-object' ) );
		} );
		$doc.on( 'click', '#acfgo-fields-editor .delete-field', function ( e ) {
			e.preventDefault();
			var $li = $( this ).closest( '.acf-field-object' );
			if ( window.confirm( 'Delete this field?' ) ) {
				$li.remove();
				updateEmpty();
			}
		} );
		$doc.on( 'click', '#acfgo-fields-editor .duplicate-field', function ( e ) {
			e.preventDefault();
			var $li = $( this ).closest( '.acf-field-object' ), f = readField( $li );
			var copy = JSON.parse( JSON.stringify( f ) );
			( function rekey( x ) {
				x.key = uniqid( 'field_' );
				( x.sub_fields || [] ).forEach( rekey );
				( x.layouts || [] ).forEach( function ( l ) {
					l.key = uniqid( 'layout_' );
					( l.sub_fields || [] ).forEach( rekey );
				} );
			}( copy ) );
			copy.label = ( copy.label || '' ) + ' (copy)';
			copy.name = ( copy.name || '' ) + '_copy';
			fieldObject( copy ).insertAfter( $li );
		} );
		// settings edits
		$doc.on( 'input change', '#acfgo-fields-editor .settings [data-setting]', function () {
			var $el = $( this ), $li = $el.closest( '.acf-field-object' ), f = $li.data( 'field' ), name = $el.data( 'setting' );
			if ( $el.closest( '.acf-field-object' )[ 0 ] !== $li[ 0 ] ) {
				return;
			}
			if ( name === 'type' ) {
				if ( $el.val() !== f.type ) {
					var keep = readField( $li );
					keep.type = $el.val();
					$li.data( 'field', keep );
					renderSettings( $li );
					refreshHandle( $li );
				}
				return;
			}
			f[ name ] = readSetting( $el );
			if ( name === 'label' ) {
				var $name = $li.find( '> .settings [data-setting=name]' ).first();
				if ( ! $name.data( 'touched' ) && ( ! f.name || $name.data( 'auto' ) ) ) {
					f.name = sanitizeName( f.label );
					$name.val( f.name ).data( 'auto', true );
				}
			}
			if ( name === 'name' && this.type === 'text' ) {
				$el.data( 'touched', true );
			}
			if ( $el.data( 'stype' ) === 'true_false' ) {
				$el.siblings( '.acf-switch' ).toggleClass( '-on', $el.prop( 'checked' ) );
			}
			refreshHandle( $li );
			applyShowIf( $li.children( '.settings' ) );
		} );
		$doc.on( 'click', '.acf-settings-tabs a, .acf-group-tabs a', function ( e ) {
			e.preventDefault();
			var $a = $( this ), tab = $a.data( 'tab' ), $ul = $a.closest( 'ul' );
			$ul.children().removeClass( 'active' );
			$a.parent().addClass( 'active' );
			if ( $ul.hasClass( 'acf-group-tabs' ) ) {
				$( '#acfgo-settings-editor .acf-group-tab' ).hide().filter( '[data-tab="' + tab + '"]' ).show();
				return;
			}
			var $s = $ul.parent();
			$s.children( '.acf-field-settings-tab' ).hide().filter( '[data-tab="' + tab + '"]' ).show();
			if ( tab === 'conditional' ) {
				renderConditional( $s.closest( '.acf-field-object' ).data( 'field', readField( $s.closest( '.acf-field-object' ) ) ) );
				$s.children( '.acf-conditional-logic' ).show();
			}
		} );
		// layouts
		$doc.on( 'click', '.acf-add-layout', function ( e ) {
			e.preventDefault();
			$( this ).closest( 'td' ).children( '.acf-fc-layouts' ).append( layoutObject( {} ) );
		} );
		$doc.on( 'click', '.acf-delete-layout', function ( e ) {
			e.preventDefault();
			if ( window.confirm( 'Delete this layout?' ) ) {
				$( this ).closest( '.acf-fc-layout' ).remove();
			}
		} );
		$doc.on( 'click', '.acf-duplicate-layout', function ( e ) {
			e.preventDefault();
			var $l = $( this ).closest( '.acf-fc-layout' ), $owner = $l.closest( '.acf-field-object' );
			var f = readField( $owner ), key = $l.data( 'key' ), src = ( f.layouts || [] ).filter( function ( x ) {
				return x.key === key;
			} )[ 0 ];
			if ( ! src ) {
				return;
			}
			var copy = JSON.parse( JSON.stringify( src ) );
			copy.key = uniqid( 'layout_' );
			copy.name += '_copy';
			copy.label += ' (copy)';
			( copy.sub_fields || [] ).forEach( function rekey( x ) {
				x.key = uniqid( 'field_' );
				( x.sub_fields || [] ).forEach( rekey );
			} );
			layoutObject( copy ).insertAfter( $l );
		} );
		$doc.on( 'input', '.acf-fc-layout [data-layout=label]', function () {
			var $l = $( this ).closest( '.acf-fc-layout' ), $name = $l.find( '> table [data-layout=name]' );
			$l.find( '> .acf-fc-layout-head .acf-fc-layout-title' ).text( this.value || '(no label)' );
			if ( ! $name.data( 'touched' ) ) {
				$name.val( sanitizeName( this.value ) );
			}
		} );
		$doc.on( 'input', '.acf-fc-layout [data-layout=name]', function () {
			$( this ).data( 'touched', true );
		} );
		// conditional logic
		$doc.on( 'change', '.cl-enabled', function () {
			$( this ).closest( '.acf-conditional-logic' ).find( '.cl-groups' ).toggle( this.checked );
			$( this ).siblings( '.acf-switch' ).toggleClass( '-on', this.checked );
		} );
		$doc.on( 'change', '.cl-field, .cl-operator', function () {
			var $tr = $( this ).closest( '.acf-cl-rule' ), self = $tr.closest( '.acf-field-object' ).data( 'field' );
			var rule = { field: $tr.find( '.cl-field' ).val(), operator: $( this ).hasClass( 'cl-operator' ) ? $tr.find( '.cl-operator' ).val() : '', value: $tr.find( '.cl-value-input' ).val() };
			if ( ! rule.operator ) {
				var target = allFields().filter( function ( x ) {
					return x.key === rule.field;
				} )[ 0 ];
				rule.operator = operatorsFor( target )[ 0 ][ 0 ];
			}
			$tr.replaceWith( ruleRow( self, rule ) );
		} );
		$doc.on( 'click', '.cl-add-rule', function ( e ) {
			e.preventDefault();
			var $tr = $( this ).closest( 'tr' );
			$( ruleRow( $tr.closest( '.acf-field-object' ).data( 'field' ) ) ).insertAfter( $tr );
		} );
		$doc.on( 'click', '.cl-remove-rule', function ( e ) {
			e.preventDefault();
			var $tr = $( this ).closest( 'tr' ), $g = $tr.closest( '.cl-group' );
			$tr.remove();
			if ( ! $g.find( '.acf-cl-rule' ).length ) {
				$g.remove();
			}
		} );
		$doc.on( 'click', '.cl-add-group', function ( e ) {
			e.preventDefault();
			var $c = $( this ).closest( '.cl-groups' ), self = $( this ).closest( '.acf-field-object' ).data( 'field' );
			$( '<div class="cl-group"><h4>or</h4><table class="acf-table"><tbody>' + ruleRow( self ) + '</tbody></table></div>' ).insertBefore( $( this ).parent() );
			return $c;
		} );
		// location rules
		$doc.on( 'change', '.loc-param', function () {
			$( this ).closest( 'tr' ).find( 'td.value' ).html( valueSelect( this.value, '' ) );
		} );
		$doc.on( 'click', '.loc-add-rule', function ( e ) {
			e.preventDefault();
			$( locRow() ).insertAfter( $( this ).closest( 'tr' ) );
		} );
		$doc.on( 'click', '.loc-remove-rule', function ( e ) {
			e.preventDefault();
			var $g = $( this ).closest( '.rule-group' );
			$( this ).closest( 'tr' ).remove();
			if ( ! $g.find( '.loc-rule' ).length ) {
				$g.remove();
			}
		} );
		$doc.on( 'click', '.loc-add-group', function ( e ) {
			e.preventDefault();
			var $groups = $( '#acfgo-settings-editor .rule-groups' );
			$groups.append( locGroup( [ { param: 'post_type', operator: '==', value: 'post' } ], $groups.children().length ) );
		} );
		$doc.on( 'change', '#acf-active', function () {
			group.active = this.checked;
			$( this ).siblings( '.acf-switch' ).toggleClass( '-on', this.checked );
		} );
		// save
		$( '#post' ).on( 'submit', function ( e ) {
			var g = serialize(), errors = validateFields( g );
			if ( ! $.trim( g.title ) ) {
				errors.unshift( 'Field group title is required.' );
			}
			if ( errors.length ) {
				e.preventDefault();
				$( '.acf-editor-errors' ).remove();
				$( '<div class="notice notice-error acf-editor-errors"><p></p></div>' ).find( 'p' ).text( errors.join( ' ' ) ).end().insertBefore( '#post' );
				return;
			}
			$( '#acfgo-group-json' ).val( JSON.stringify( g ) );
		} );
	}

	function openField( $li ) {
		if ( ! $li.children( '.settings' ).children().length ) {
			renderSettings( $li );
		}
		$li.addClass( 'open' ).children( '.settings' ).slideDown( 150 );
	}
	function closeField( $li ) {
		$li.data( 'field', readField( $li ) );
		refreshHandle( $li );
		$li.removeClass( 'open' ).children( '.settings' ).slideUp( 150 );
	}

	$( function () {
		var raw = document.getElementById( 'acfgo-schema' ), data = document.getElementById( 'acfgo-group-data' );
		if ( ! raw || ! data ) {
			return;
		}
		schema = JSON.parse( raw.textContent );
		group = JSON.parse( data.textContent );
		schema.types.forEach( function ( t ) {
			types[ t.name ] = t;
		} );
		$( '#acfgo-fields-editor' ).append( fieldList( group.fields || [] ) );
		renderGroupSettings();
		bind();
	} );
}( jQuery ) );
