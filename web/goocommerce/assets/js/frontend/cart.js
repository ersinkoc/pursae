/* Pursae Commerce cart page (cart.js): the shipping calculator toggle, shipping method choice and coupons
   by AJAX, and the Update cart button enabled once a quantity changes. Every action also works as a
   plain form post. */
(function () {
	'use strict';
	var params = window.goocommerce_params || {};
	var store = window.gooStore;
	if (!store) {
		return;
	}

	function ready(fn) {
		if (document.readyState === 'loading') {
			document.addEventListener('DOMContentLoaded', fn);
		} else {
			fn();
		}
	}

	// reloadCart fetches the cart page and swaps its store markup (WooCommerce's update_wc_div).
	function reloadCart(notices) {
		var root = document.querySelector('.woocommerce-cart-form') ? document.querySelector('.woocommerce-cart-form').closest('.woocommerce') : document.querySelector('.woocommerce');
		if (root) {
			root.classList.add('processing');
		}
		return fetch(params.cart_url || window.location.href, { credentials: 'same-origin' }).then(function (r) { return r.text(); }).then(function (html) {
			var doc = new DOMParser().parseFromString(html, 'text/html');
			var next = doc.querySelector('.woocommerce-cart-form') ? doc.querySelector('.woocommerce-cart-form').closest('.woocommerce') : doc.querySelector('.wc-empty-cart-message');
			if (root && next) {
				var wrap = next.classList.contains('woocommerce') ? next : next.closest('.woocommerce');
				root.replaceWith(wrap || next);
			}
			if (notices) {
				store.showNotices(notices, document.querySelector('.woocommerce'));
			}
			store.trigger('updated_wc_div');
			store.refreshFragments();
			init();
		});
	}

	function init() {
		var form = document.querySelector('.woocommerce-cart-form');
		var update = form ? form.querySelector('button[name="update_cart"]') : null;
		if (update) {
			update.disabled = true;
			form.addEventListener('input', function (e) {
				if (e.target.classList.contains('qty')) {
					update.disabled = false;
				}
			});
		}
	}

	document.addEventListener('click', function (e) {
		var btn = e.target.closest('.shipping-calculator-button');
		if (btn) {
			e.preventDefault();
			var sec = btn.parentNode.querySelector('.shipping-calculator-form');
			if (sec) {
				var open = sec.style.display === 'none';
				sec.style.display = open ? '' : 'none';
				btn.setAttribute('aria-expanded', open ? 'true' : 'false');
			}
			return;
		}
		var rm = e.target.closest('.cart_totals a.woocommerce-remove-coupon');
		if (rm) {
			e.preventDefault();
			store.post('remove_coupon', { security: params.remove_coupon_nonce, coupon: rm.getAttribute('data-coupon') }).then(function (html) {
				reloadCart(html);
			});
		}
	});

	document.addEventListener('change', function (e) {
		var radio = e.target.closest('.cart_totals input.shipping_method, .cart_totals select.shipping_method');
		if (!radio) {
			return;
		}
		var data = { security: params.update_shipping_method_nonce };
		document.querySelectorAll('.cart_totals input.shipping_method:checked, .cart_totals input[type=hidden].shipping_method, .cart_totals select.shipping_method').forEach(function (el) {
			data['shipping_method[' + (el.getAttribute('data-index') || '0') + ']'] = el.value;
		});
		var totals = document.querySelector('.cart_totals');
		if (totals) {
			totals.classList.add('processing');
		}
		store.post('update_shipping_method', data).then(function (html) {
			var next = store.html(html);
			if (totals && next) {
				totals.replaceWith(next);
			}
			store.trigger('updated_shipping_method');
		});
	});

	document.addEventListener('submit', function (e) {
		var form = e.target;
		if (!form.classList || !form.classList.contains('woocommerce-cart-form')) {
			return;
		}
		var submitter = e.submitter;
		if (submitter && submitter.name === 'apply_coupon') {
			e.preventDefault();
			var code = form.querySelector('#coupon_code');
			store.post('apply_coupon', { security: params.apply_coupon_nonce, coupon_code: code ? code.value : '' }).then(function (html) {
				reloadCart(html);
			});
		}
	});

	ready(init);
})();
