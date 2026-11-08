/* Pursae Contact Form editor: tabs, the Mail (2) switch and the form-tag generator dialogs (CF7's
 * admin/includes/js). Written for Pursae. */
( function () {
	'use strict';

	var editor = document.getElementById( 'contact-form-editor' );
	if ( ! editor ) {
		return;
	}

	// Tabs --------------------------------------------------------------------------------------
	var tabs = editor.querySelectorAll( '.nav-tab-wrapper .nav-tab' );
	var panels = editor.querySelectorAll( '.contact-form-editor-panel' );
	var activeInput = document.getElementById( 'active-tab' );

	function activate( id ) {
		var found = false;
		panels.forEach( function ( p ) { found = found || p.id === id; } );
		if ( ! found ) {
			id = 'form-panel';
		}
		tabs.forEach( function ( t ) {
			var on = t.getAttribute( 'aria-controls' ) === id;
			t.classList.toggle( 'nav-tab-active', on );
			t.setAttribute( 'aria-selected', on ? 'true' : 'false' );
			t.setAttribute( 'tabindex', on ? '0' : '-1' );
		} );
		panels.forEach( function ( p ) {
			p.hidden = p.id !== id;
		} );
		editor.setAttribute( 'data-active-tab', id );
		if ( activeInput ) {
			activeInput.value = id;
		}
	}

	tabs.forEach( function ( t ) {
		t.addEventListener( 'click', function ( e ) {
			e.preventDefault();
			var id = t.getAttribute( 'aria-controls' );
			activate( id );
			if ( window.history && window.history.replaceState ) {
				window.history.replaceState( null, '', '#' + id );
			}
		} );
	} );
	activate( ( window.location.hash || '' ).replace( '#', '' ) || editor.getAttribute( 'data-active-tab' ) || 'form-panel' );

	// Mail (2) ----------------------------------------------------------------------------------
	document.querySelectorAll( '.toggle-form-table' ).forEach( function ( box ) {
		var target = document.querySelector( '[data-toggle="' + box.id + '"]' );
		var sync = function () {
			if ( target ) {
				target.hidden = ! box.checked;
			}
		};
		box.addEventListener( 'change', sync );
		sync();
	} );

	// Mail-tags in the legends are clickable: they select themselves for copying.
	document.querySelectorAll( '.mailtag' ).forEach( function ( tag ) {
		tag.addEventListener( 'click', function () {
			var range = document.createRange();
			range.selectNodeContents( tag );
			var sel = window.getSelection();
			sel.removeAllRanges();
			sel.addRange( range );
		} );
	} );

	// Unsaved changes ------------------------------------------------------------------------------
	var form = document.getElementById( 'wpcf7-admin-form-element' );
	var changed = false;
	if ( form ) {
		form.addEventListener( 'change', function () { changed = true; } );
		form.addEventListener( 'submit', function () { changed = false; } );
		window.addEventListener( 'beforeunload', function ( e ) {
			if ( changed ) {
				e.preventDefault();
				e.returnValue = '';
			}
		} );
	}

	// Form-tag generator ----------------------------------------------------------------------------
	var textarea = document.getElementById( 'wpcf7-form' );
	var defs = {
		text: { title: 'text', doc: 'text-fields', required: true, defaults: true, placeholder: true, length: true, autocomplete: true },
		email: { title: 'email', doc: 'text-fields', required: true, defaults: true, placeholder: true, length: true, autocomplete: true },
		url: { title: 'URL', doc: 'text-fields', required: true, defaults: true, placeholder: true, length: true },
		tel: { title: 'telephone number', doc: 'text-fields', required: true, defaults: true, placeholder: true, length: true },
		number: { title: 'number', doc: 'number-fields', required: true, defaults: true, placeholder: true, range: true, subtypes: [ 'number', 'range' ] },
		date: { title: 'date', doc: 'date-field', required: true, defaults: true, placeholder: true, range: true },
		textarea: { title: 'text area', doc: 'text-fields', required: true, defaults: true, placeholder: true, length: true },
		select: { title: 'drop-down menu', doc: 'checkboxes-radio-buttons-and-menus', required: true, choices: true, selectOptions: true },
		checkbox: { title: 'checkboxes', doc: 'checkboxes-radio-buttons-and-menus', required: true, choices: true, checkboxOptions: true },
		radio: { title: 'radio buttons', doc: 'checkboxes-radio-buttons-and-menus', choices: true, radioOptions: true },
		acceptance: { title: 'acceptance checkbox', doc: 'acceptance-checkbox', content: true, acceptanceOptions: true },
		quiz: { title: 'quiz', doc: 'quiz', quiz: true },
		captchac: { title: 'CAPTCHA (image)', doc: 'captcha', captcha: true, captchaImage: true },
		captchar: { title: 'CAPTCHA (answer)', doc: 'captcha', captcha: true, defaults: true, placeholder: true, length: true, size: true },
		file: { title: 'file uploading field', doc: 'file-uploading-and-attachment', required: true, file: true },
		submit: { title: 'submit button', doc: 'submit-button', noName: true, label: true }
	};

	function el( tag, attrs, html ) {
		var e = document.createElement( tag );
		Object.keys( attrs || {} ).forEach( function ( k ) { e.setAttribute( k, attrs[ k ] ); } );
		if ( html !== undefined ) {
			e.innerHTML = html;
		}
		return e;
	}

	function row( label, control ) {
		var tr = el( 'tr' );
		var th = el( 'th', { scope: 'row' } );
		th.appendChild( el( 'label', {}, label ) );
		var td = el( 'td' );
		td.appendChild( control );
		tr.appendChild( th );
		tr.appendChild( td );
		return tr;
	}

	function input( name, type, extra ) {
		return el( 'input', Object.assign( { type: type || 'text', 'data-tag-part': name, class: type === 'checkbox' ? '' : 'oneline' }, extra || {} ) );
	}

	function checkbox( name, label, extra ) {
		var l = el( 'label' );
		l.appendChild( input( name, 'checkbox', extra ) );
		l.appendChild( document.createTextNode( ' ' + label ) );
		return l;
	}

	function group() {
		var span = el( 'span', { class: 'tag-generator-group' } );
		Array.prototype.forEach.call( arguments, function ( c ) {
			span.appendChild( c );
			span.appendChild( el( 'br' ) );
		} );
		return span;
	}

	var counter = 0;

	function openDialog( type ) {
		var def = defs[ type ];
		if ( ! def ) {
			return;
		}
		counter++;
		var dialog = el( 'dialog', { class: 'tag-generator-dialog', id: 'tag-generator-panel-' + type } );
		var box = el( 'form', { method: 'dialog', class: 'tag-generator-panel', 'data-id': type } );
		box.appendChild( el( 'button', { class: 'close-button', type: 'submit', title: 'Close this dialog box', value: 'cancel' }, '<span class="screen-reader-text">Close</span><span class="dashicons dashicons-no-alt" aria-hidden="true"></span>' ) );
		box.appendChild( el( 'h3', {}, 'Form-tag Generator: ' + def.title ) );
		box.appendChild( el( 'p', { class: 'description' }, 'Generates a form-tag for a <a href="https://pursae.com/docs/built-in-plugins/gontact-form/" target="_blank" rel="noopener">' + def.title + '</a>.' ) );
		var table = el( 'table', { class: 'form-table' } );
		var tbody = el( 'tbody' );
		table.appendChild( tbody );

		if ( def.subtypes ) {
			var sel = el( 'select', { 'data-tag-part': 'basetype' } );
			def.subtypes.forEach( function ( t ) { sel.appendChild( el( 'option', { value: t }, t === 'range' ? 'Slider' : 'Spinbox' ) ); } );
			tbody.appendChild( row( 'Field type', sel ) );
		}
		if ( def.required ) {
			tbody.appendChild( row( 'Field type', checkbox( 'required', 'This is a required field.' ) ) );
		}
		if ( ! def.noName ) {
			// The image and the answer field of a CAPTCHA pair share one name.
			var defName = def.captcha ? 'your-captcha' : type + '-' + Math.floor( Math.random() * 900 + 100 );
			tbody.appendChild( row( 'Field name', input( 'name', 'text', { value: defName, pattern: '[A-Za-z][A-Za-z0-9_\\-]*' } ) ) );
		}
		if ( def.captcha ) {
			box.insertBefore( el( 'p', { class: 'description' }, 'A CAPTCHA is a pair of tags with the same name: <code>[captchac your-captcha]</code> shows the image, <code>[captchar your-captcha]</code> is the field for its code. The image is generated by Pursae itself; no external service is used.' ), null );
			box.appendChild( el( 'p', { class: 'description' }, '<strong>Accessibility:</strong> there is no audio version of the image, so visitors who cannot see it cannot pass it. Where that matters, use a <a href="https://pursae.com/docs/built-in-plugins/gontact-form/" target="_blank" rel="noopener">quiz</a> question instead (Form-tag Generator: quiz).' ) );
		}
		if ( def.captchaImage ) {
			var sz = el( 'select', { 'data-tag-part': 'imgsize' } );
			[ [ 's', 'Small (60×20)' ], [ 'm', 'Medium (72×24)' ], [ 'l', 'Large (84×28)' ] ].forEach( function ( o ) {
				var opt = el( 'option', { value: o[ 0 ] }, o[ 1 ] );
				if ( o[ 0 ] === 'm' ) {
					opt.setAttribute( 'selected', 'selected' );
				}
				sz.appendChild( opt );
			} );
			tbody.appendChild( row( 'Image size', sz ) );
			tbody.appendChild( row( 'Foreground color', input( 'fg', 'text', { placeholder: '#000000', pattern: '#[0-9a-fA-F]{6}' } ) ) );
			tbody.appendChild( row( 'Background color', input( 'bg', 'text', { placeholder: '#ffffff', pattern: '#[0-9a-fA-F]{6}' } ) ) );
		}
		if ( def.size ) {
			tbody.appendChild( row( 'Size', input( 'size', 'number', { min: '1', class: 'numeric' } ) ) );
		}
		if ( def.choices ) {
			var ta = el( 'textarea', { 'data-tag-part': 'values', rows: '4', class: 'values' } );
			ta.value = 'Option 1\nOption 2\nOption 3';
			var wrap = el( 'span' );
			wrap.appendChild( ta );
			wrap.appendChild( el( 'br' ) );
			wrap.appendChild( el( 'span', { class: 'description' }, 'One option per line. Use <code>label|value</code> for a different mail value.' ) );
			tbody.appendChild( row( 'Options', wrap ) );
		}
		if ( def.selectOptions ) {
			tbody.appendChild( row( '', group( checkbox( 'multiple', 'Allow multiple selections' ), checkbox( 'include_blank', 'Insert a blank item as the first option' ) ) ) );
		}
		if ( def.checkboxOptions ) {
			tbody.appendChild( row( '', group( checkbox( 'label_first', 'Put a label first, a checkbox last' ), checkbox( 'use_label_element', 'Wrap each item with label element', { checked: 'checked' } ), checkbox( 'exclusive', 'Make checkboxes exclusive' ) ) ) );
		}
		if ( def.radioOptions ) {
			tbody.appendChild( row( '', group( checkbox( 'label_first', 'Put a label first, a radio button last' ), checkbox( 'use_label_element', 'Wrap each item with label element', { checked: 'checked' } ) ) ) );
		}
		if ( def.defaults ) {
			tbody.appendChild( row( 'Default value', input( 'value' ) ) );
			if ( def.placeholder ) {
				tbody.appendChild( row( '', checkbox( 'placeholder', 'Use this text as the placeholder of the field' ) ) );
			}
		}
		if ( def.length ) {
			var lw = el( 'span' );
			lw.appendChild( document.createTextNode( 'Min ' ) );
			lw.appendChild( input( 'minlength', 'number', { min: '0', class: 'numeric' } ) );
			lw.appendChild( document.createTextNode( ' Max ' ) );
			lw.appendChild( input( 'maxlength', 'number', { min: '0', class: 'numeric' } ) );
			tbody.appendChild( row( 'Length', lw ) );
		}
		if ( def.range ) {
			var rw = el( 'span' );
			var t = type === 'date' ? 'date' : 'number';
			rw.appendChild( document.createTextNode( 'Min ' ) );
			rw.appendChild( input( 'min', t ) );
			rw.appendChild( document.createTextNode( ' Max ' ) );
			rw.appendChild( input( 'max', t ) );
			tbody.appendChild( row( 'Range', rw ) );
		}
		if ( def.autocomplete ) {
			tbody.appendChild( row( '', checkbox( 'autocomplete', 'Use autocomplete (' + ( type === 'email' ? 'email' : 'name' ) + ')' ) ) );
		}
		if ( def.content ) {
			tbody.appendChild( row( 'Condition', input( 'content', 'text', { value: 'I agree to the terms and conditions.' } ) ) );
		}
		if ( def.acceptanceOptions ) {
			tbody.appendChild( row( '', group( checkbox( 'optional', 'Make this checkbox optional' ) ) ) );
		}
		if ( def.quiz ) {
			var qa = el( 'textarea', { 'data-tag-part': 'values', rows: '4', class: 'values' } );
			qa.value = 'The capital of Japan?|Tokyo\nThe capital of France?|Paris';
			var qw = el( 'span' );
			qw.appendChild( qa );
			qw.appendChild( el( 'br' ) );
			qw.appendChild( el( 'span', { class: 'description' }, 'One pipe-separated question-answer pair (<code>question|answer</code>) per line.' ) );
			tbody.appendChild( row( 'Questions and answers', qw ) );
		}
		if ( def.file ) {
			tbody.appendChild( row( 'File size limit (bytes)', input( 'limit', 'text', { placeholder: '1mb' } ) ) );
			tbody.appendChild( row( 'Acceptable file types', input( 'filetypes', 'text', { placeholder: 'jpg|png|pdf' } ) ) );
		}
		if ( def.label ) {
			tbody.appendChild( row( 'Label', input( 'value', 'text', { value: 'Submit' } ) ) );
		}
		tbody.appendChild( row( 'Id attribute', input( 'id' ) ) );
		tbody.appendChild( row( 'Class attribute', input( 'class' ) ) );
		box.appendChild( table );

		var insertBox = el( 'div', { class: 'insert-box' } );
		var code = el( 'input', { type: 'text', class: 'code', readonly: 'readonly', onfocus: 'this.select()' } );
		insertBox.appendChild( code );
		var submitbox = el( 'div', { class: 'submitbox' } );
		var insert = el( 'button', { type: 'button', class: 'button button-primary insert-tag' }, 'Insert Tag' );
		submitbox.appendChild( insert );
		insertBox.appendChild( submitbox );
		var hint = el( 'p', { class: 'mail-tag-tip' } );
		insertBox.appendChild( hint );
		box.appendChild( insertBox );
		dialog.appendChild( box );
		document.body.appendChild( dialog );

		var part = function ( name ) { return box.querySelector( '[data-tag-part="' + name + '"]' ); };
		var val = function ( name ) { var p = part( name ); return p ? p.value.trim() : ''; };
		var on = function ( name ) { var p = part( name ); return !! ( p && p.checked ); };
		var quote = function ( v ) { return '"' + v.replace( /"/g, "'" ) + '"'; };

		var build = function () {
			var base = def.subtypes ? val( 'basetype' ) : type;
			var name = def.noName ? '' : val( 'name' ).replace( /[^A-Za-z0-9_:.\-]/g, '' );
			var tag = base + ( on( 'required' ) ? '*' : '' );
			var parts = [ tag ];
			if ( name ) {
				parts.push( name );
			}
			[ 'multiple', 'include_blank', 'label_first', 'use_label_element', 'exclusive', 'optional' ].forEach( function ( o ) {
				if ( on( o ) ) {
					parts.push( o );
				}
			} );
			if ( def.autocomplete && on( 'autocomplete' ) ) {
				parts.push( 'autocomplete:' + ( type === 'email' ? 'email' : 'name' ) );
			}
			if ( def.captchaImage ) {
				if ( val( 'imgsize' ) && val( 'imgsize' ) !== 'm' ) {
					parts.push( 'size:' + val( 'imgsize' ) );
				}
				[ 'fg', 'bg' ].forEach( function ( o ) {
					if ( /^#[0-9a-fA-F]{6}$/.test( val( o ) ) ) {
						parts.push( o + ':' + val( o ) );
					}
				} );
			}
			[ 'size', 'minlength', 'maxlength', 'min', 'max', 'limit', 'filetypes' ].forEach( function ( o ) {
				if ( val( o ) ) {
					parts.push( o + ':' + val( o ).replace( /\s+/g, '' ) );
				}
			} );
			if ( val( 'id' ) ) {
				parts.push( 'id:' + val( 'id' ).replace( /[^A-Za-z0-9_\-]/g, '' ) );
			}
			val( 'class' ).split( /\s+/ ).forEach( function ( c ) {
				if ( c ) {
					parts.push( 'class:' + c.replace( /[^A-Za-z0-9_\-]/g, '' ) );
				}
			} );
			if ( def.defaults && val( 'value' ) ) {
				if ( on( 'placeholder' ) ) {
					parts.push( 'placeholder' );
				}
				parts.push( quote( val( 'value' ) ) );
			}
			if ( def.label && val( 'value' ) ) {
				parts.push( quote( val( 'value' ) ) );
			}
			if ( def.choices || def.quiz ) {
				val( 'values' ).split( '\n' ).forEach( function ( v ) {
					if ( v.trim() ) {
						parts.push( quote( v.trim() ) );
					}
				} );
			}
			var out = '[' + parts.join( ' ' ) + ']';
			if ( def.content && val( 'content' ) ) {
				out += ' ' + val( 'content' ) + ' [/' + base + ']';
			}
			code.value = out;
			hint.innerHTML = name && type !== 'quiz' && ! def.captcha ? 'To use the user input in the email, insert the corresponding mail-tag <strong><span class="mail-tag">[' + name + ']</span></strong> into the email template.' : '';
			return out;
		};

		box.addEventListener( 'input', build );
		box.addEventListener( 'change', build );
		build();
		insert.addEventListener( 'click', function () {
			var tag = build();
			if ( textarea ) {
				var start = textarea.selectionStart || 0, end = textarea.selectionEnd || 0;
				textarea.value = textarea.value.slice( 0, start ) + tag + textarea.value.slice( end );
				textarea.selectionStart = textarea.selectionEnd = start + tag.length;
				textarea.focus();
				textarea.dispatchEvent( new Event( 'change', { bubbles: true } ) );
			}
			dialog.close();
		} );
		dialog.addEventListener( 'close', function () { dialog.remove(); } );
		if ( typeof dialog.showModal === 'function' ) {
			dialog.showModal();
		} else {
			dialog.setAttribute( 'open', 'open' );
		}
	}

	document.querySelectorAll( '[data-taggen="open-dialog"]' ).forEach( function ( button ) {
		button.addEventListener( 'click', function () { openDialog( button.getAttribute( 'data-type' ) ); } );
	} );
}() );
