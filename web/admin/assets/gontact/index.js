/* Pursae Contact Form front end: Ajax submission, validation and the DOM events of Contact Form 7
 * (wpcf7beforesubmit, wpcf7submit, wpcf7invalid, wpcf7unaccepted, wpcf7spam, wpcf7mailsent,
 * wpcf7mailfailed, wpcf7aborted, wpcf7statuschanged, wpcf7reset). Written for Pursae. */
( function () {
	'use strict';

	var statuses = {
		init: 'init', validation_failed: 'invalid', acceptance_missing: 'unaccepted', spam: 'spam',
		aborted: 'aborted', mail_sent: 'sent', mail_failed: 'failed', submitting: 'submitting',
		resetting: 'resetting', validating: 'validating', payment_required: 'payment-required'
	};
	var classValues = Object.keys( statuses ).map( function ( k ) { return statuses[ k ]; } );

	function absInt( v ) {
		var n = parseInt( v, 10 );
		return isNaN( n ) ? 0 : Math.abs( n );
	}

	function trigger( target, name, detail ) {
		target.dispatchEvent( new CustomEvent( 'wpcf7' + name, { bubbles: true, detail: detail } ) );
	}

	function baseDetail( form ) {
		var w = form.wpcf7;
		return {
			contactFormId: w.id, pluginVersion: w.pluginVersion, contactFormLocale: w.locale,
			unitTag: w.unitTag, containerPostId: w.containerPost, status: w.status
		};
	}

	function setStatus( form, status ) {
		if ( statuses[ status ] ) {
			status = statuses[ status ];
		}
		if ( classValues.indexOf( status ) < 0 ) {
			status = 'custom-' + status.replace( /[^0-9a-z]+/gi, ' ' ).trim().replace( /\s+/g, '-' );
		}
		var prev = form.getAttribute( 'data-status' );
		form.wpcf7.status = status;
		form.setAttribute( 'data-status', status );
		form.classList.add( status );
		if ( prev && prev !== status ) {
			form.classList.remove( prev );
			var detail = baseDetail( form );
			detail.prevStatus = prev;
			trigger( form, 'statuschanged', detail );
		}
		return status;
	}

	function apiFetch( path, options ) {
		var root = wpcf7.api.root, ns = ( wpcf7.api.namespace || 'contact-form-7/v1' ).replace( /^\/|\/$/g, '' );
		path = ns + '/' + path.replace( /^\//, '' );
		if ( root.indexOf( '?' ) >= 0 ) {
			path = path.replace( '?', '&' );
		}
		options = options || {};
		options.headers = Object.assign( { Accept: 'application/json, */*;q=0.1' }, options.headers || {} );
		options.credentials = 'same-origin';
		return window.fetch( root + path, options ).then( function ( res ) {
			if ( res.status >= 200 && res.status < 300 ) {
				return res.json().catch( function () {
					throw { code: 'invalid_json', message: 'The response is not a valid JSON response.' };
				} );
			}
			throw res;
		}, function () {
			throw { code: 'fetch_error', message: 'You are probably offline.' };
		} );
	}

	function errorId( form, field ) {
		return ( form.wpcf7.unitTag + '-ve-' + field ).replace( /[^0-9a-z_-]+/gi, '' );
	}

	function setValidationError( form, field, message ) {
		var id = errorId( form, field );
		var list = form.wpcf7.parent.querySelector( '.screen-reader-response ul' );
		if ( list && ! list.querySelector( 'li#' + id ) ) {
			var li = document.createElement( 'li' );
			li.setAttribute( 'id', id );
			var control = form.querySelector( '.wpcf7-form-control-wrap[data-name="' + field + '"] .wpcf7-form-control' );
			if ( control && control.id ) {
				var a = document.createElement( 'a' );
				a.setAttribute( 'href', '#' + control.id );
				a.textContent = message;
				li.appendChild( a );
			} else {
				li.textContent = message;
			}
			list.appendChild( li );
		}
		form.querySelectorAll( '.wpcf7-form-control-wrap[data-name="' + field + '"]' ).forEach( function ( wrap ) {
			if ( 'validating' === form.wpcf7.status && wrap.dataset.underValidation ) {
				return;
			}
			var tip = document.createElement( 'span' );
			tip.classList.add( 'wpcf7-not-valid-tip' );
			tip.setAttribute( 'aria-hidden', 'true' );
			tip.textContent = message;
			var old = wrap.querySelector( '.wpcf7-not-valid-tip' );
			if ( old ) {
				old.remove();
			}
			wrap.appendChild( tip );
			wrap.querySelectorAll( '[aria-invalid]' ).forEach( function ( el ) {
				el.setAttribute( 'aria-invalid', 'true' );
			} );
			wrap.querySelectorAll( '.wpcf7-form-control' ).forEach( function ( control ) {
				control.classList.add( 'wpcf7-not-valid' );
				control.setAttribute( 'aria-describedby', id );
				if ( typeof control.setCustomValidity === 'function' ) {
					control.setCustomValidity( message );
				}
				if ( control.closest( '.use-floating-validation-tip' ) ) {
					control.addEventListener( 'focus', function () { tip.setAttribute( 'style', 'display: none' ); } );
					tip.addEventListener( 'click', function () { tip.setAttribute( 'style', 'display: none' ); } );
				}
			} );
		} );
	}

	function removeValidationError( form, field ) {
		var li = form.wpcf7.parent.querySelector( '.screen-reader-response ul li#' + errorId( form, field ) );
		if ( li ) {
			li.remove();
		}
		form.querySelectorAll( '.wpcf7-form-control-wrap[data-name="' + field + '"]' ).forEach( function ( wrap ) {
			var tip = wrap.querySelector( '.wpcf7-not-valid-tip' );
			if ( tip ) {
				tip.remove();
			}
			wrap.querySelectorAll( '[aria-invalid]' ).forEach( function ( el ) {
				el.setAttribute( 'aria-invalid', 'false' );
			} );
			wrap.querySelectorAll( '.wpcf7-form-control' ).forEach( function ( control ) {
				control.removeAttribute( 'aria-describedby' );
				control.classList.remove( 'wpcf7-not-valid' );
				if ( typeof control.setCustomValidity === 'function' ) {
					control.setCustomValidity( '' );
				}
			} );
		} );
	}

	function clearResponse( form ) {
		form.querySelectorAll( '.wpcf7-form-control-wrap' ).forEach( function ( wrap ) {
			if ( wrap.dataset.name ) {
				removeValidationError( form, wrap.dataset.name );
			}
		} );
		var status = form.wpcf7.parent.querySelector( '.screen-reader-response [role="status"]' );
		if ( status ) {
			status.innerText = '';
		}
		form.querySelectorAll( '.wpcf7-response-output' ).forEach( function ( div ) {
			div.innerText = '';
		} );
	}

	function resetQuiz( form, quiz ) {
		Object.keys( quiz ).forEach( function ( name ) {
			var q = quiz[ name ];
			form.querySelectorAll( '.wpcf7-form-control-wrap[data-name="' + name + '"]' ).forEach( function ( wrap ) {
				var label = wrap.querySelector( '.wpcf7-quiz-label' );
				var answer = wrap.querySelector( 'input[name="_wpcf7_quiz_answer_' + name + '"]' );
				var input = wrap.querySelector( 'input.wpcf7-quiz' );
				if ( label ) {
					label.textContent = q[ 0 ];
				}
				if ( answer ) {
					answer.value = q[ 1 ];
				}
				if ( input ) {
					input.value = '';
				}
			} );
		} );
	}

	// resetCaptcha swaps in the new [captchac] images of a refill: the image URL ends in the new
	// challenge prefix, which goes into the hidden _wpcf7_captcha_challenge_ field.
	function resetCaptcha( form, refill ) {
		Object.keys( refill ).forEach( function ( name ) {
			var url = refill[ name ];
			form.querySelectorAll( 'input[name="' + name + '"]' ).forEach( function ( input ) {
				input.value = '';
			} );
			form.querySelectorAll( 'img.wpcf7-captcha-' + name.replace( /:/g, '' ) ).forEach( function ( img ) {
				img.setAttribute( 'src', url );
			} );
			var match = /([0-9]+)\.(png|gif|jpeg)$/.exec( url );
			if ( match ) {
				form.querySelectorAll( 'input[name="_wpcf7_captcha_challenge_' + name + '"]' ).forEach( function ( input ) {
					input.value = match[ 1 ];
				} );
			}
		} );
	}

	function refreshCaptcha( form ) {
		apiFetch( 'contact-forms/' + form.wpcf7.id + '/refill', { method: 'GET' } ).then( function ( response ) {
			if ( response && response.captcha ) {
				resetCaptcha( form, response.captcha );
			}
		} );
	}

	function submit( form, options ) {
		options = options || {};
		if ( 'submitting' === form.wpcf7.status ) {
			return;
		}
		if ( wpcf7.blocked ) {
			clearResponse( form );
			setStatus( form, 'submitting' );
			return;
		}
		var formData = new FormData( form );
		if ( options.submitter && options.submitter.name ) {
			formData.append( options.submitter.name, options.submitter.value );
		}
		var detail = baseDetail( form );
		detail.inputs = [];
		formData.forEach( function ( value, name ) {
			if ( ! /^_/.test( name ) ) {
				detail.inputs.push( { name: name, value: value } );
			}
		} );
		detail.formData = formData;
		clearResponse( form );
		trigger( form, 'beforesubmit', detail );
		setStatus( form, 'submitting' );

		apiFetch( 'contact-forms/' + form.wpcf7.id + '/feedback', { method: 'POST', body: formData } ).then( function ( response ) {
			var status = setStatus( form, response.status );
			detail.status = response.status;
			detail.apiResponse = response;
			if ( [ 'invalid', 'unaccepted', 'spam', 'aborted' ].indexOf( status ) >= 0 ) {
				trigger( form, status, detail );
			} else if ( [ 'sent', 'failed' ].indexOf( status ) >= 0 ) {
				trigger( form, 'mail' + status, detail );
			}
			trigger( form, 'submit', detail );
			if ( response.posted_data_hash ) {
				form.querySelector( 'input[name="_wpcf7_posted_data_hash"]' ).value = response.posted_data_hash;
			}
			if ( 'mail_sent' === response.status ) {
				form.reset();
				form.wpcf7.resetOnMailSent = true;
			}
			( response.invalid_fields || [] ).forEach( function ( error ) {
				setValidationError( form, error.field, error.message );
			} );
			form.wpcf7.parent.querySelector( '.screen-reader-response [role="status"]' ).insertAdjacentText( 'beforeend', response.message );
			form.querySelectorAll( '.wpcf7-response-output' ).forEach( function ( div ) {
				div.innerText = response.message;
			} );
		} ).catch( function ( error ) {
			setStatus( form, 'failed' );
			var message = error && error.message ? error.message : 'There was an error trying to send your message. Please try again later.';
			form.querySelectorAll( '.wpcf7-response-output' ).forEach( function ( div ) {
				div.innerText = message;
			} );
			if ( window.console ) {
				console.error( error );
			}
		} );
	}

	function reset( form ) {
		var detail = baseDetail( form );
		detail.inputs = [];
		setStatus( form, 'resetting' );
		clearResponse( form );
		apiFetch( 'contact-forms/' + form.wpcf7.id + '/refill', { method: 'GET' } ).then( function ( response ) {
			if ( form.wpcf7.resetOnMailSent ) {
				delete form.wpcf7.resetOnMailSent;
				setStatus( form, 'mail_sent' );
			} else {
				setStatus( form, 'init' );
			}
			detail.apiResponse = response;
			trigger( form, 'reset', detail );
		} ).catch( function () {
			setStatus( form, 'init' );
		} );
		updateCounters( form );
	}

	// Client-side validation with the form's SWV schema ------------------------------------------

	function inputValues( form, field ) {
		var out = [];
		new FormData( form ).forEach( function ( value, name ) {
			if ( name === field || name === field + '[]' ) {
				if ( value instanceof File ) {
					if ( value.name ) {
						out.push( value );
					}
				} else if ( String( value ).trim() !== '' ) {
					out.push( String( value ).trim() );
				}
			}
		} );
		return out;
	}

	var ruleChecks = {
		required: function ( v ) { return v.length > 0; },
		requiredfile: function ( v ) { return v.length > 0; },
		email: function ( v ) { return v.every( function ( x ) { return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test( x ); } ); },
		url: function ( v ) { return v.every( function ( x ) { return /^[a-z][a-z0-9.+-]*:[^\s]+$/i.test( x ); } ); },
		tel: function ( v ) { return v.every( function ( x ) { return /^[+]?[0-9() -]*[0-9][0-9() -]*$/.test( x ); } ); },
		number: function ( v ) { return v.every( function ( x ) { return /^[-]?(?:[0-9]+|[0-9]*[.][0-9]+)(?:[eE][+-]?[0-9]+)?$/.test( x ); } ); },
		date: function ( v ) { return v.every( function ( x ) { return /^[0-9]{4,}-[0-9]{2}-[0-9]{2}$/.test( x ); } ); },
		minlength: function ( v, r ) { return v.length === 0 || v.join( '' ).length >= r.threshold; },
		maxlength: function ( v, r ) { return v.join( '' ).length <= r.threshold; },
		minnumber: function ( v, r ) { return v.every( function ( x ) { return parseFloat( x ) >= r.threshold; } ); },
		maxnumber: function ( v, r ) { return v.every( function ( x ) { return parseFloat( x ) <= r.threshold; } ); },
		mindate: function ( v, r ) { return v.every( function ( x ) { return x >= r.threshold; } ); },
		maxdate: function ( v, r ) { return v.every( function ( x ) { return x <= r.threshold; } ); },
		maxfilesize: function ( v, r ) {
			return v.reduce( function ( t, f ) { return t + ( f.size || 0 ); }, 0 ) <= r.threshold;
		},
		file: function ( v, r ) {
			return v.every( function ( f ) {
				var name = ( f.name || '' ).toLowerCase(), type = f.type || '';
				return ( r.accept || [] ).some( function ( a ) {
					a = a.toLowerCase();
					if ( a.charAt( 0 ) === '.' ) {
						return name.slice( -a.length ) === a;
					}
					if ( /\/\*$/.test( a ) ) {
						return type.indexOf( a.slice( 0, -1 ) ) === 0;
					}
					return type === a;
				} );
			} );
		}
	};

	function validate( form, options ) {
		var target = options && options.target;
		if ( ! target || ! target.name ) {
			return;
		}
		var field = target.name.replace( /\[\]$/, '' );
		var schema = form.wpcf7.schema;
		var check = function ( s ) {
			var failed = null;
			( s.rules || [] ).some( function ( rule ) {
				if ( rule.field !== field || ! ruleChecks[ rule.rule ] ) {
					return false;
				}
				var values = inputValues( form, field );
				if ( 0 === values.length && 'required' !== rule.rule && 'requiredfile' !== rule.rule ) {
					return false;
				}
				if ( ! ruleChecks[ rule.rule ]( values, rule ) ) {
					failed = rule.error;
					return true;
				}
				return false;
			} );
			removeValidationError( form, field );
			if ( failed ) {
				setValidationError( form, field, failed );
			}
		};
		if ( schema ) {
			check( schema );
			return;
		}
		apiFetch( 'contact-forms/' + form.wpcf7.id + '/feedback/schema', { method: 'GET' } ).then( function ( s ) {
			wpcf7.schemas.set( form.wpcf7.id, s );
			check( s );
		} ).catch( function () {} );
	}

	// Helpers -----------------------------------------------------------------------------------

	function updateCounters( form ) {
		form.querySelectorAll( '.wpcf7-character-count' ).forEach( function ( count ) {
			var target = form.querySelector( '[name="' + count.dataset.targetName + '"]' );
			if ( ! target ) {
				return;
			}
			var len = target.value.length;
			var max = absInt( count.dataset.maximumValue );
			var value = count.classList.contains( 'down' ) ? max - len : len;
			count.setAttribute( 'data-current-value', value );
			count.innerText = value;
			count.classList.toggle( 'too-long', max > 0 && max < len );
			var min = absInt( count.dataset.minimumValue );
			count.classList.toggle( 'too-short', min > 0 && len < min );
		} );
	}

	function initHelpers( form ) {
		form.querySelectorAll( '.wpcf7-exclusive-checkbox' ).forEach( function ( el ) {
			el.addEventListener( 'change', function ( e ) {
				var name = e.target.getAttribute( 'name' );
				form.querySelectorAll( 'input[type="checkbox"][name="' + name + '"]' ).forEach( function ( sib ) {
					if ( sib !== e.target ) {
						sib.checked = false;
					}
				} );
			} );
		} );
		form.querySelectorAll( '.has-free-text' ).forEach( function ( el ) {
			var free = el.querySelector( 'input.wpcf7-free-text' );
			var box = el.querySelector( 'input[type="checkbox"], input[type="radio"]' );
			free.disabled = ! box.checked;
			form.addEventListener( 'change', function ( e ) {
				free.disabled = ! box.checked;
				if ( e.target === box && box.checked ) {
					free.focus();
				}
			} );
		} );
		form.querySelectorAll( '.wpcf7-validates-as-url' ).forEach( function ( el ) {
			el.addEventListener( 'change', function () {
				var val = el.value.trim();
				if ( val && ! /^[a-z][a-z0-9.+-]*:/i.test( val ) && val.indexOf( '.' ) >= 0 ) {
					val = 'http://' + val.replace( /^\/+/, '' );
				}
				el.value = val;
			} );
		} );
		// Acceptance checkboxes keep the submit button disabled until they are accepted.
		if ( form.querySelector( '.wpcf7-acceptance' ) && ! form.classList.contains( 'wpcf7-acceptance-as-validation' ) ) {
			var check = function () {
				var accepted = true;
				form.querySelectorAll( '.wpcf7-acceptance' ).forEach( function ( parent ) {
					if ( ! accepted || parent.classList.contains( 'optional' ) ) {
						return;
					}
					var box = parent.querySelector( 'input[type="checkbox"]' );
					if ( ( parent.classList.contains( 'invert' ) && box.checked ) || ( ! parent.classList.contains( 'invert' ) && ! box.checked ) ) {
						accepted = false;
					}
				} );
				form.querySelectorAll( '.wpcf7-submit' ).forEach( function ( button ) {
					button.disabled = ! accepted;
				} );
			};
			check();
			form.addEventListener( 'change', check );
			form.addEventListener( 'wpcf7reset', check );
		}
		if ( form.querySelector( '.wpcf7-character-count' ) ) {
			updateCounters( form );
			form.addEventListener( 'keyup', function () { updateCounters( form ); } );
			form.addEventListener( 'change', function () { updateCounters( form ); } );
		}
	}

	function initRecaptcha() {
		var conf = wpcf7.recaptcha;
		if ( ! conf || ! conf.sitekey || typeof window.grecaptcha === 'undefined' ) {
			return;
		}
		var execute = function ( action ) {
			window.grecaptcha.execute( conf.sitekey, { action: action } ).then( function ( token ) {
				document.querySelectorAll( 'form.wpcf7-form input[name="_wpcf7_recaptcha_response"]' ).forEach( function ( el ) {
					el.value = token;
				} );
			} );
		};
		window.grecaptcha.ready( function () {
			execute( conf.actions.homepage );
			setInterval( function () { execute( conf.actions.homepage ); }, 110000 );
		} );
		document.addEventListener( 'wpcf7submit', function () { execute( conf.actions.contactform ); } );
	}

	function init( form ) {
		var data = new FormData( form );
		form.wpcf7 = {
			id: absInt( data.get( '_wpcf7' ) ),
			status: form.getAttribute( 'data-status' ),
			pluginVersion: data.get( '_wpcf7_version' ),
			locale: data.get( '_wpcf7_locale' ),
			unitTag: data.get( '_wpcf7_unit_tag' ),
			containerPost: absInt( data.get( '_wpcf7_container_post' ) ),
			parent: form.closest( '.wpcf7' ),
			get schema() {
				return wpcf7.schemas.get( this.id );
			}
		};
		wpcf7.schemas.set( form.wpcf7.id, undefined );
		form.querySelectorAll( '.has-spinner' ).forEach( function ( el ) {
			el.insertAdjacentHTML( 'afterend', '<span class="wpcf7-spinner"></span>' );
		} );
		initHelpers( form );
		window.addEventListener( 'load', function () {
			if ( wpcf7.cached ) {
				form.reset();
			}
		} );
		form.addEventListener( 'reset', function () { wpcf7.reset( form ); } );
		form.addEventListener( 'submit', function ( e ) {
			wpcf7.submit( form, { submitter: e.submitter } );
			e.preventDefault();
		} );
		var refilled = function ( e ) {
			if ( e.detail.apiResponse && e.detail.apiResponse.quiz ) {
				resetQuiz( form, e.detail.apiResponse.quiz );
			}
			if ( e.detail.apiResponse && e.detail.apiResponse.captcha ) {
				resetCaptcha( form, e.detail.apiResponse.captcha );
			}
		};
		form.addEventListener( 'wpcf7submit', refilled );
		form.addEventListener( 'wpcf7reset', refilled );
		form.querySelectorAll( '.wpcf7-captcha-refresh' ).forEach( function ( button ) {
			var refresh = function ( e ) {
				e.preventDefault();
				refreshCaptcha( form );
				var answer = form.querySelector( 'input[name="' + button.getAttribute( 'data-captcha' ) + '"]' );
				if ( answer ) {
					answer.focus();
				}
			};
			button.hidden = false;
			button.addEventListener( 'click', refresh );
			button.addEventListener( 'keydown', function ( e ) {
				if ( 'Enter' === e.key || ' ' === e.key ) {
					refresh( e );
				}
			} );
		} );
		form.addEventListener( 'change', function ( e ) {
			if ( e.target.closest( '.wpcf7-form-control' ) ) {
				wpcf7.validate( form, { target: e.target } );
			}
		} );
		form.addEventListener( 'wpcf7statuschanged', function ( e ) {
			var status = e.detail.status;
			form.querySelectorAll( '.active-on-any' ).forEach( function ( el ) {
				el.removeAttribute( 'inert' );
				el.classList.remove( 'active-on-any' );
			} );
			form.querySelectorAll( '.inert-on-' + status ).forEach( function ( el ) {
				el.setAttribute( 'inert', 'inert' );
				el.classList.add( 'active-on-any' );
			} );
		} );
	}

	document.addEventListener( 'DOMContentLoaded', function () {
		if ( typeof wpcf7 === 'undefined' || typeof wpcf7.api === 'undefined' ) {
			if ( window.console ) {
				console.error( 'wpcf7 is not defined.' );
			}
			return;
		}
		if ( typeof window.fetch !== 'function' || typeof window.FormData !== 'function' ) {
			return;
		}
		window.wpcf7 = Object.assign( { init: init, submit: submit, reset: reset, validate: validate, schemas: new Map() }, window.wpcf7 );
		document.querySelectorAll( 'form .wpcf7[data-wpcf7-id]' ).forEach( function ( div ) {
			var p = document.createElement( 'p' );
			p.setAttribute( 'class', 'wpcf7-form-in-wrong-place' );
			p.innerHTML = '<strong>Error:</strong> This contact form is placed in the wrong place.';
			div.replaceWith( p );
		} );
		document.querySelectorAll( '.wpcf7 > form' ).forEach( function ( form ) {
			wpcf7.init( form );
			form.closest( '.wpcf7' ).classList.replace( 'no-js', 'js' );
		} );
		initRecaptcha();
	} );
}() );
