/* WP to Pursae: drives the package build and upload in short AJAX steps. */
( function () {
	'use strict';
	var cfg = window.wp2p;
	if ( ! cfg ) {
		return;
	}
	var $        = function ( id ) {
		return document.getElementById( id );
	};
	var failures = 0;
	var polling  = false;

	function post( action, data ) {
		var body = new FormData();
		body.append( 'action', action );
		body.append( '_ajax_nonce', cfg.nonce );
		Object.keys( data || {} ).forEach(
			function ( k ) {
				body.append( k, data[ k ] );
			}
		);
		return fetch( cfg.ajax, { method: 'POST', body: body, credentials: 'same-origin' } ).then(
			function ( r ) {
				return r.json();
			}
		);
	}

	function text( el, value ) {
		el.textContent = value;
	}

	function render( job ) {
		var form = $( 'wp2p-form' );
		var prog = $( 'wp2p-progress' );
		if ( ! job ) {
			form.hidden = false;
			prog.hidden = true;
			return;
		}
		form.hidden   = true;
		prog.hidden   = false;
		var uploading = job.mode === 'push' && ( job.status === 'built' || job.status === 'uploading' );
		var title     = uploading ? cfg.i18n.uploading : cfg.i18n.building;
		if ( job.status === 'sent' ) {
			title = cfg.i18n.sentTitle;
		} else if ( job.status === 'built' && job.mode === 'download' ) {
			title = cfg.i18n.readyTitle;
		}
		text( $( 'wp2p-title' ), title );
		var pct = uploading ? job.sent || 0 : job.progress;
		if ( job.status === 'sent' || ( job.status === 'built' && job.mode === 'download' ) ) {
			pct = 100;
		}
		$( 'wp2p-bar' ).style.width = pct + '%';
		text( $( 'wp2p-step' ), uploading ? cfg.i18n.sent.replace( '%s', pct + '%' ) : ( job.status === 'building' ? job.step : '' ) );
		var counts       = $( 'wp2p-counts' );
		counts.innerHTML = '';
		Object.keys( job.counts || {} ).forEach(
			function ( k ) {
				if ( k.indexOf( '_bytes' ) > 0 ) {
						return;
				}
				var li         = document.createElement( 'li' );
				li.textContent = k.replace( /_/g, ' ' ) + ': ' + job.counts[ k ];
				counts.appendChild( li );
			}
		);
		var skipped       = $( 'wp2p-skipped' );
		skipped.innerHTML = '';
		( job.skipped || [] ).forEach(
			function ( s ) {
				var li         = document.createElement( 'li' );
				li.textContent = s.kind + ' ' + s.item + ': ' + s.reason;
				skipped.appendChild( li );
			}
		);
		var err    = $( 'wp2p-error' );
		err.hidden = ! job.message;
		text( err.querySelector( 'p' ), job.message ? cfg.i18n.failed + ' ' + job.message : '' );
		$( 'wp2p-resume' ).hidden = ! job.message;
		$( 'wp2p-built' ).hidden  = ! ( job.mode === 'download' && job.status === 'built' );
		if ( job.key ) {
			$( 'wp2p-key' ).value     = job.key;
			$( 'wp2p-download' ).href = job.download;
			$( 'wp2p-download' ).setAttribute( 'download', job.filename );
			text( $( 'wp2p-size' ), job.size );
		}
		$( 'wp2p-sent' ).hidden = job.status !== 'sent';
		if ( job.status === 'sent' ) {
			text( $( 'wp2p-cancel' ), cfg.i18n.again );
			if ( job.status_url ) {
				$( 'wp2p-status-link' ).href = job.status_url;
			} else {
				$( 'wp2p-status-link' ).hidden = true;
			}
			if ( ! polling ) {
				polling = true;
				pollRemote();
			}
		}
	}

	function busy( job ) {
		if ( ! job || job.message ) {
			return false;
		}
		return job.status === 'building' || ( job.mode === 'push' && ( job.status === 'built' || job.status === 'uploading' ) );
	}

	function step( resume ) {
		post( 'wp2p_step', resume ? { resume : '1' } : {} )
			.then(
				function ( r ) {
					failures = 0;
					if ( ! r.success ) {
							render( { mode: '', status: 'error', message: r.data && r.data.message, counts: {}, progress: 0 } );
							return;
					}
					render( r.data );
					if ( busy( r.data ) ) {
						step( false );
					}
				}
			)
			.catch(
				function () {
					// A timed-out request: the job keeps its place, try again.
					failures++;
					text( $( 'wp2p-step' ), cfg.i18n.network );
					if ( failures < 20 ) {
							setTimeout(
								function () {
									step( false );
								},
								3000 * failures
							);
					}
				}
			);
	}

	function pollRemote() {
		post( 'wp2p_remote_status' ).then(
			function ( r ) {
				var out = $( 'wp2p-remote' );
				if ( ! r.success ) {
						text( out, r.data && r.data.message ? r.data.message : '' );
						return;
				}
				if ( r.data.state === 'done' ) {
					text( out, cfg.i18n.done );
					return;
				}
				if ( r.data.state === 'failed' ) {
					text( out, cfg.i18n.importErr + ' ' + r.data.error );
					return;
				}
				text( out, cfg.i18n.imported + ' ' + ( r.data.phase || r.data.state ) );
				setTimeout( pollRemote, 5000 );
			}
		);
	}

	document.querySelectorAll( 'input[name="wp2p-mode"]' ).forEach(
		function ( r ) {
			r.addEventListener(
				'change',
				function () {
					$( 'wp2p-push' ).hidden = r.value !== 'push' || ! r.checked;
				}
			);
		}
	);

	$( 'wp2p-start' ).addEventListener(
		'click',
		function () {
			var mode     = document.querySelector( 'input[name="wp2p-mode"]:checked' ).value;
			var btn      = this;
			btn.disabled = true;
			post(
				'wp2p_start',
				{
					mode: mode,
					url: $( 'wp2p-url' ).value,
					token: $( 'wp2p-token' ).value,
					app_user: $( 'wp2p-app-user' ).value,
					app_password: $( 'wp2p-app-password' ).value,
				}
			).then(
				function ( r ) {
					btn.disabled = false;
					if ( ! r.success ) {
							window.alert( r.data && r.data.message ); // eslint-disable-line no-alert
							return;
					}
					$( 'wp2p-token' ).value        = '';
					$( 'wp2p-app-password' ).value = '';
					render( r.data );
					step( false );
				}
			);
		}
	);

	$( 'wp2p-resume' ).addEventListener(
		'click',
		function () {
			step( true );
		}
	);

	$( 'wp2p-cancel' ).addEventListener(
		'click',
		function () {
			if ( ! window.confirm( cfg.i18n.confirm ) ) { // eslint-disable-line no-alert
				return;
			}
			post( 'wp2p_cancel' ).then(
				function () {
					render( null );
				}
			);
		}
	);

	render( cfg.job );
	if ( busy( cfg.job ) ) {
		step( false );
	}
}() );
