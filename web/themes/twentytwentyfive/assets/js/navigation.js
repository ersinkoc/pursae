/**
 * Twenty Twenty-Five for Pursae: the navigation block's overlay menu and submenu toggles
 * (core drives them with the Interactivity API; this is the same behaviour in plain JavaScript).
 */
( function () {
	var open = null;

	function setOpen( nav, on ) {
		var box = nav.querySelector( '.wp-block-navigation__responsive-container' );
		var dialog = nav.querySelector( '.wp-block-navigation__responsive-dialog' );
		if ( ! box ) {
			return;
		}
		box.classList.toggle( 'is-menu-open', on );
		box.classList.toggle( 'has-modal-open', on );
		document.documentElement.classList.toggle( 'has-modal-open', on );
		if ( dialog ) {
			if ( on ) {
				dialog.setAttribute( 'aria-modal', 'true' );
				dialog.setAttribute( 'role', 'dialog' );
			} else {
				dialog.removeAttribute( 'aria-modal' );
				dialog.removeAttribute( 'role' );
			}
		}
		open = on ? nav : null;
		var target = on ? box.querySelector( '.wp-block-navigation__responsive-container-close' ) : nav.querySelector( '.wp-block-navigation__responsive-container-open' );
		if ( target ) {
			target.focus();
		}
	}

	document.addEventListener( 'click', function ( e ) {
		var t = e.target.closest ? e.target : e.target.parentNode;
		var btn = t.closest( '.wp-block-navigation__responsive-container-open, .wp-block-navigation__responsive-container-close, .wp-block-navigation-submenu__toggle' );
		if ( ! btn ) {
			if ( open && ! t.closest( '.wp-block-navigation__responsive-container-content' ) && t.closest( '.wp-block-navigation__responsive-container' ) ) {
				setOpen( open, false );
			}
			return;
		}
		var nav = btn.closest( '.wp-block-navigation' );
		if ( btn.classList.contains( 'wp-block-navigation-submenu__toggle' ) ) {
			btn.setAttribute( 'aria-expanded', btn.getAttribute( 'aria-expanded' ) === 'true' ? 'false' : 'true' );
			return;
		}
		setOpen( nav, btn.classList.contains( 'wp-block-navigation__responsive-container-open' ) );
	} );

	document.addEventListener( 'keydown', function ( e ) {
		if ( e.key === 'Escape' ) {
			if ( open ) {
				setOpen( open, false );
			}
			document.querySelectorAll( '.wp-block-navigation-submenu__toggle[aria-expanded="true"]' ).forEach( function ( b ) {
				b.setAttribute( 'aria-expanded', 'false' );
			} );
		}
	} );
}() );
