/* Goocommerce cart fragments (cart-fragments.js): keeps the mini-cart and the header cart link right on
   cached pages. Loaded on every page first; it also defines window.gooStore, the helpers the other
   storefront scripts share. */
(function () {
	'use strict';
	var params = window.goocommerce_params || {};

	var store = window.gooStore = window.gooStore || {};

	// ajaxURL is the URL of an AJAX action (/?goo-ajax=<action>).
	store.ajaxURL = function (action) {
		return String(params.goo_ajax_url || '/?goo-ajax=%%endpoint%%').replace('%%endpoint%%', action);
	};

	// post sends form data to an AJAX action; resolves with the parsed JSON (or the text).
	store.post = function (action, data) {
		var body = data instanceof FormData ? new URLSearchParams(data) : new URLSearchParams(data || {});
		return fetch(store.ajaxURL(action), {
			method: 'POST',
			credentials: 'same-origin',
			headers: { 'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8', 'X-Requested-With': 'XMLHttpRequest' },
			body: body.toString()
		}).then(function (r) {
			var type = r.headers.get('Content-Type') || '';
			return type.indexOf('json') >= 0 ? r.json() : r.text();
		});
	};

	// cookie reads a cookie.
	store.cookie = function (name) {
		var m = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
		return m ? decodeURIComponent(m[1]) : '';
	};

	// html parses markup into its first element.
	store.html = function (markup) {
		var t = document.createElement('template');
		t.innerHTML = String(markup).trim();
		return t.content.firstElementChild;
	};

	// trigger dispatches a DOM event (jQuery-free counterparts of WooCommerce's triggers).
	store.trigger = function (name, detail, target) {
		(target || document.body).dispatchEvent(new CustomEvent(name, { bubbles: true, detail: detail || {} }));
	};

	// applyFragments replaces each selector's elements with the fragment markup.
	store.applyFragments = function (fragments) {
		if (!fragments) {
			return;
		}
		Object.keys(fragments).forEach(function (sel) {
			document.querySelectorAll(sel).forEach(function (el) {
				var next = store.html(fragments[sel]);
				if (next) {
					el.replaceWith(next);
				}
			});
		});
		// Theme headers mark their own count badge with data-goo-cart-count: keep its markup, set the number.
		var count = fragments['span.goo-cart-count'] && store.html(fragments['span.goo-cart-count']);
		if (count) {
			document.querySelectorAll('[data-goo-cart-count]').forEach(function (el) {
				el.textContent = count.textContent;
			});
		}
		store.trigger('wc_fragments_loaded', { fragments: fragments });
	};

	// noticesWrapper is the first notices wrapper of the store markup (created when missing).
	store.showNotices = function (markup, scope) {
		var root = scope || document.querySelector('.woocommerce') || document.body;
		var wrap = root.querySelector('.woocommerce-notices-wrapper');
		if (!wrap) {
			wrap = document.createElement('div');
			wrap.className = 'woocommerce-notices-wrapper';
			root.insertBefore(wrap, root.firstChild);
		}
		var el = store.html(markup);
		wrap.innerHTML = el && el.classList.contains('woocommerce-notices-wrapper') ? el.innerHTML : markup;
		wrap.scrollIntoView({ behavior: 'smooth', block: 'start' });
		var focus = wrap.querySelector('[role="alert"]');
		if (focus) {
			focus.setAttribute('tabindex', '-1');
			focus.focus({ preventScroll: true });
		}
	};

	var key = 'goo_fragments';
	var hashKey = 'goo_cart_hash';
	var storage = null;
	try {
		storage = window.sessionStorage;
		storage.setItem('goo_test', '1');
		storage.removeItem('goo_test');
	} catch (e) {
		storage = null;
	}

	store.saveFragments = function (fragments, hash) {
		if (!storage) {
			return;
		}
		try {
			storage.setItem(key, JSON.stringify(fragments || {}));
			storage.setItem(hashKey, hash || '');
		} catch (e) { /* quota */ }
	};

	store.refreshFragments = function () {
		return store.post('get_refreshed_fragments', { time: Date.now() }).then(function (res) {
			if (res && res.fragments) {
				store.applyFragments(res.fragments);
				store.saveFragments(res.fragments, res.cart_hash);
				store.trigger('wc_fragments_refreshed', res);
			}
			return res;
		});
	};

	function init() {
		var hasItems = store.cookie(params.items_cookie || 'goocommerce_items_in_cart') !== '';
		var hash = store.cookie(params.hash_cookie || 'goocommerce_cart_hash');
		var needs = document.querySelector('.widget_shopping_cart_content, a.cart-contents, .goo-cart-count');
		if (!needs) {
			return;
		}
		if (!hasItems) {
			// The cached page may show an old cart; an empty one needs no request.
			if (storage && storage.getItem(hashKey)) {
				storage.removeItem(key);
				storage.removeItem(hashKey);
				store.refreshFragments();
			}
			return;
		}
		if (storage && storage.getItem(hashKey) === hash && storage.getItem(key)) {
			try {
				store.applyFragments(JSON.parse(storage.getItem(key)));
				return;
			} catch (e) { /* stale */ }
		}
		store.refreshFragments();
	}

	document.addEventListener('goo:refresh-fragments', function () { store.refreshFragments(); });
	document.addEventListener('visibilitychange', function () {
		if (document.visibilityState === 'visible' && storage && storage.getItem(hashKey) !== store.cookie(params.hash_cookie || 'goocommerce_cart_hash')) {
			store.refreshFragments();
		}
	});
	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', init);
	} else {
		init();
	}
})();
