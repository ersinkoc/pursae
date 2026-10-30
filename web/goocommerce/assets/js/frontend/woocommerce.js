/* Goocommerce general storefront script (woocommerce.js): the store notice, the login / coupon
   toggles, password visibility and keyboard-friendly notices. */
(function () {
	'use strict';
	var store = window.gooStore || {};

	function ready(fn) {
		if (document.readyState === 'loading') {
			document.addEventListener('DOMContentLoaded', fn);
		} else {
			fn();
		}
	}

	ready(function () {
		// Store notice: shown until dismissed (a cookie per notice text).
		var notice = document.querySelector('.woocommerce-store-notice');
		if (notice) {
			var id = notice.getAttribute('data-notice-id') || '';
			var ck = 'store_notice' + id;
			if ((store.cookie ? store.cookie(ck) : '') !== 'hidden') {
				notice.style.display = '';
			}
			var dismiss = notice.querySelector('.woocommerce-store-notice__dismiss-link');
			if (dismiss) {
				dismiss.addEventListener('click', function (e) {
					e.preventDefault();
					document.cookie = ck + '=hidden; path=/; SameSite=Lax';
					notice.style.display = 'none';
				});
			}
		}

		// The checkout's "Click here to login" / "Click here to enter your code".
		document.addEventListener('click', function (e) {
			var a = e.target.closest('a.showlogin, a.showcoupon');
			if (!a) {
				return;
			}
			e.preventDefault();
			var form = document.querySelector(a.classList.contains('showlogin') ? 'form.woocommerce-form-login' : 'form.checkout_coupon');
			if (form) {
				form.style.display = form.style.display === 'none' ? '' : 'none';
				var first = form.querySelector('input:not([type=hidden])');
				if (first && form.style.display !== 'none') {
					first.focus();
				}
			}
		});

		// Show / hide password buttons.
		document.querySelectorAll('.woocommerce form input[type="password"]').forEach(function (input) {
			if (input.closest('.password-input')) {
				return;
			}
			var wrap = document.createElement('span');
			wrap.className = 'password-input';
			input.parentNode.insertBefore(wrap, input);
			wrap.appendChild(input);
			var btn = document.createElement('button');
			btn.type = 'button';
			btn.className = 'show-password-input';
			btn.setAttribute('aria-label', 'Show password');
			btn.textContent = '\u{1F441}';
			btn.style.cssText = 'position:absolute;right:.5em;background:none;border:0;cursor:pointer;opacity:.6';
			btn.addEventListener('click', function () {
				var show = input.type === 'password';
				input.type = show ? 'text' : 'password';
				btn.setAttribute('aria-label', show ? 'Hide password' : 'Show password');
			});
			wrap.appendChild(btn);
		});

		// Notices: move focus to the first error so screen readers announce it.
		var err = document.querySelector('.woocommerce-error[role="alert"]');
		if (err) {
			err.setAttribute('tabindex', '-1');
		}
	});
})();
