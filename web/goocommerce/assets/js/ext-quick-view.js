/* Goocommerce Quick View: loads a product summary into an accessible modal. */
(function () {
	'use strict';
	var modal = document.getElementById('goo-quick-view');
	if (!modal || !window.fetch) { return; }
	var content = modal.querySelector('.goo-quick-view-content');
	var lastFocus = null;
	var FOCUSABLE = 'a[href], area[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

	function focusables() {
		return Array.prototype.filter.call(modal.querySelectorAll(FOCUSABLE), function (el) {
			return el.offsetWidth > 0 || el.offsetHeight > 0 || el === document.activeElement;
		});
	}

	function open(html) {
		content.innerHTML = html;
		modal.hidden = false;
		document.documentElement.classList.add('goo-quick-view-open');
		initGallery();
		initVariations();
		var title = modal.querySelector('#goo-quick-view-title');
		var first = focusables()[0];
		if (title) { title.setAttribute('tabindex', '-1'); title.focus(); } else if (first) { first.focus(); }
	}

	function close() {
		if (modal.hidden) { return; }
		closeLightbox();
		modal.hidden = true;
		content.innerHTML = '';
		document.documentElement.classList.remove('goo-quick-view-open');
		if (lastFocus && lastFocus.focus) { lastFocus.focus(); }
	}

	function load(id, trigger) {
		lastFocus = trigger || document.activeElement;
		var url = modal.getAttribute('data-endpoint') + '&product_id=' + encodeURIComponent(id);
		modal.classList.add('is-loading');
		fetch(url, { credentials: 'same-origin', headers: { 'X-Requested-With': 'XMLHttpRequest' } })
			.then(function (r) { return r.text(); })
			.then(function (html) { modal.classList.remove('is-loading'); open(html); })
			.catch(function () { modal.classList.remove('is-loading'); if (trigger && trigger.href) { window.location = trigger.href; } });
	}

	document.addEventListener('click', function (e) {
		var btn = e.target.closest && e.target.closest('.goo-quick-view-button');
		if (btn) {
			if (modal.getAttribute('data-mobile') === '0' && window.matchMedia('(max-width: 768px)').matches) { return; }
			e.preventDefault();
			load(btn.getAttribute('data-product_id'), btn);
			return;
		}
		if (e.target.closest && e.target.closest('[data-goo-qv-close]') && modal.contains(e.target)) {
			e.preventDefault();
			close();
		}
	});

	document.addEventListener('keydown', function (e) {
		if (modal.hidden) { return; }
		if (e.key === 'Escape' || e.key === 'Esc') {
			e.preventDefault();
			if (lightbox) { closeLightbox(); } else { close(); }
			return;
		}
		if (e.key === 'Tab') {
			var els = focusables();
			if (!els.length) { e.preventDefault(); return; }
			var first = els[0], last = els[els.length - 1];
			if (e.shiftKey && (document.activeElement === first || !modal.contains(document.activeElement))) {
				e.preventDefault(); last.focus();
			} else if (!e.shiftKey && (document.activeElement === last || !modal.contains(document.activeElement))) {
				e.preventDefault(); first.focus();
			}
		}
	});

	/* Gallery thumbnails and the lightbox. */
	var lightbox = null;
	function closeLightbox() {
		if (lightbox) { lightbox.parentNode.removeChild(lightbox); lightbox = null; }
	}
	function initGallery() {
		var main = content.querySelector('.goo-quick-view-main');
		var zoom = content.querySelector('[data-goo-qv-lightbox]');
		Array.prototype.forEach.call(content.querySelectorAll('.goo-quick-view-thumb'), function (t) {
			t.addEventListener('click', function () {
				if (main) { main.src = t.getAttribute('data-large'); }
				if (zoom) { zoom.href = t.getAttribute('data-full'); }
				Array.prototype.forEach.call(content.querySelectorAll('.goo-quick-view-thumb'), function (o) { o.removeAttribute('aria-current'); });
				t.setAttribute('aria-current', 'true');
			});
		});
		if (zoom) {
			zoom.addEventListener('click', function (e) {
				e.preventDefault();
				if (modal.getAttribute('data-lightbox') !== '1') { return; }
				closeLightbox();
				lightbox = document.createElement('div');
				lightbox.className = 'goo-quick-view-lightbox';
				lightbox.setAttribute('role', 'dialog');
				lightbox.setAttribute('aria-label', 'Image');
				lightbox.innerHTML = '<button type="button" class="goo-quick-view-close" aria-label="Close image">&times;</button><img alt="">';
				lightbox.querySelector('img').src = zoom.href;
				lightbox.addEventListener('click', closeLightbox);
				modal.appendChild(lightbox);
				lightbox.querySelector('button').focus();
			});
		}
	}

	/* Variable products: pick the matching variation. */
	function initVariations() {
		var form = content.querySelector('.variations_form');
		if (!form) { return; }
		var variations = [];
		try { variations = JSON.parse(form.getAttribute('data-product_variations') || '[]'); } catch (err) { variations = []; }
		var selects = form.querySelectorAll('select[data-attribute_name]');
		var button = form.querySelector('.single_add_to_cart_button');
		var idField = form.querySelector('.variation_id');
		var out = form.querySelector('.single_variation');
		function update() {
			var chosen = {};
			var complete = true;
			Array.prototype.forEach.call(selects, function (s) {
				chosen[s.getAttribute('data-attribute_name')] = s.value;
				if (!s.value) { complete = false; }
			});
			var match = null;
			if (complete) {
				variations.some(function (v) {
					var ok = Object.keys(v.attributes).every(function (k) { return !v.attributes[k] || v.attributes[k] === chosen[k]; });
					if (ok) { match = v; }
					return ok;
				});
			}
			var usable = match && match.is_in_stock && match.is_purchasable;
			idField.value = match ? match.variation_id : 0;
			button.disabled = !usable;
			button.classList.toggle('disabled', !usable);
			out.innerHTML = match ? '<span class="price">' + match.price_html + '</span>' + (match.is_in_stock ? '' : '<p class="stock out-of-stock">Out of stock</p>') :
				(complete ? '<p>Sorry, this product is unavailable. Please choose a different combination.</p>' : '');
		}
		Array.prototype.forEach.call(selects, function (s) { s.addEventListener('change', update); });
		update();
	}
})();
