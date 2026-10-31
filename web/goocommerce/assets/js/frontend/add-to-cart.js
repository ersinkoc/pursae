/* Pursae Commerce AJAX add to cart (add-to-cart.js): loop buttons with ajax_add_to_cart and the mini-cart's
   remove links. Without JavaScript the buttons are plain ?add-to-cart= links. */
(function () {
	'use strict';
	var params = window.goocommerce_params || {};
	var store = window.gooStore;
	if (!store) {
		return;
	}

	document.addEventListener('click', function (e) {
		var btn = e.target.closest('.ajax_add_to_cart');
		if (!btn || !btn.getAttribute('data-product_id')) {
			return;
		}
		e.preventDefault();
		btn.classList.remove('added');
		btn.classList.add('loading');
		var data = {};
		Array.prototype.forEach.call(btn.attributes, function (a) {
			if (a.name.indexOf('data-') === 0 && a.name !== 'data-success_message') {
				data[a.name.slice(5)] = a.value;
			}
		});
		store.trigger('adding_to_cart', { button: btn, data: data });
		store.post('add_to_cart', data).then(function (res) {
			btn.classList.remove('loading');
			if (!res) {
				return;
			}
			if (res.error && res.product_url) {
				window.location = res.product_url;
				return;
			}
			if (params.cart_redirect_after_add) {
				window.location = params.cart_url;
				return;
			}
			btn.classList.add('added');
			if (!btn.parentNode.querySelector('.added_to_cart')) {
				var view = document.createElement('a');
				view.href = params.cart_url;
				view.className = 'added_to_cart wc-forward';
				view.title = params.i18n_view_cart || 'View cart';
				view.textContent = params.i18n_view_cart || 'View cart';
				btn.insertAdjacentElement('afterend', view);
			}
			var live = document.getElementById(btn.getAttribute('aria-describedby') || '');
			if (live && btn.getAttribute('data-success_message')) {
				live.textContent = btn.getAttribute('data-success_message').replace(/&ldquo;|&rdquo;/g, '"');
			}
			store.applyFragments(res.fragments);
			store.saveFragments(res.fragments, res.cart_hash);
			store.trigger('added_to_cart', { fragments: res.fragments, cart_hash: res.cart_hash, button: btn });
		}).catch(function () {
			btn.classList.remove('loading');
			window.location = btn.href;
		});
	});

	// Mini-cart remove links.
	document.addEventListener('click', function (e) {
		var a = e.target.closest('.remove_from_cart_button');
		if (!a || !a.getAttribute('data-cart_item_key')) {
			return;
		}
		e.preventDefault();
		var item = a.closest('.woocommerce-mini-cart-item');
		if (item) {
			item.style.opacity = '0.5';
		}
		store.post('remove_from_cart', { cart_item_key: a.getAttribute('data-cart_item_key') }).then(function (res) {
			if (!res || !res.fragments) {
				window.location = a.href;
				return;
			}
			store.applyFragments(res.fragments);
			store.saveFragments(res.fragments, res.cart_hash);
			store.trigger('removed_from_cart', { fragments: res.fragments, cart_hash: res.cart_hash });
			if (params.is_cart) {
				window.location.reload();
			}
		}).catch(function () {
			window.location = a.href;
		});
	});
})();
