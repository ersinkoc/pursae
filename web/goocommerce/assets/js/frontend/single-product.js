/* Goocommerce single product (single-product.js): tabs, the gallery (thumbnails swap the main image,
   a lightbox opens it full size) and the review rating stars. */
(function () {
	'use strict';
	var params = window.goocommerce_params || {};

	function ready(fn) {
		if (document.readyState === 'loading') {
			document.addEventListener('DOMContentLoaded', fn);
		} else {
			fn();
		}
	}

	function initTabs(wrapper) {
		var tabs = wrapper.querySelectorAll('.wc-tabs li');
		var panels = wrapper.querySelectorAll('.wc-tab, .woocommerce-tabs .panel:not(.panel .panel)');
		function show(id) {
			tabs.forEach(function (li) {
				var a = li.querySelector('a');
				var on = a && a.getAttribute('href') === '#' + id;
				li.classList.toggle('active', on);
				if (a) {
					a.setAttribute('aria-selected', on ? 'true' : 'false');
					a.setAttribute('tabindex', on ? '0' : '-1');
				}
			});
			panels.forEach(function (p) {
				p.style.display = p.id === id ? '' : 'none';
			});
		}
		wrapper.addEventListener('click', function (e) {
			var a = e.target.closest('.wc-tabs li a');
			if (!a) {
				return;
			}
			e.preventDefault();
			show(a.getAttribute('href').slice(1));
		});
		var hash = window.location.hash;
		var target = null;
		if (hash.indexOf('#comment-') === 0 || hash === '#reviews' || hash === '#tab-reviews') {
			target = 'tab-reviews';
		} else if (hash && wrapper.querySelector('.wc-tabs a[href="' + hash.replace(/"/g, '') + '"]')) {
			target = hash.slice(1);
		}
		var first = wrapper.querySelector('.wc-tabs li a');
		show(target && wrapper.querySelector('#' + target) ? target : first ? first.getAttribute('href').slice(1) : '');
	}

	function initGallery(gallery) {
		gallery.style.opacity = '1';
		var images = gallery.querySelectorAll('.woocommerce-product-gallery__image');
		if (images.length === 0) {
			return;
		}
		var main = images[0];
		var mainImg = main.querySelector('img');
		if (mainImg && !mainImg.hasAttribute('data-o_src')) {
			['src', 'srcset', 'sizes', 'width', 'height', 'title', 'alt', 'data-large_image'].forEach(function (k) {
				mainImg.setAttribute('data-o_' + k, mainImg.getAttribute(k) || '');
			});
		}
		images.forEach(function (div, i) {
			var a = div.querySelector('a');
			if (!a) {
				return;
			}
			a.addEventListener('click', function (e) {
				e.preventDefault();
				if (i === 0) {
					openLightbox(a.getAttribute('href'), (div.querySelector('img') || {}).alt || '');
					return;
				}
				// A thumbnail becomes the main image.
				var img = div.querySelector('img');
				if (!img || !mainImg) {
					return;
				}
				var large = img.getAttribute('data-large_image') || a.getAttribute('href');
				mainImg.src = large;
				mainImg.removeAttribute('srcset');
				mainImg.setAttribute('data-large_image', large);
				main.querySelector('a').setAttribute('href', large);
			});
		});
		main.classList.add('goo-zoom');
	}

	function openLightbox(src, alt) {
		var box = document.createElement('div');
		box.className = 'goo-lightbox';
		box.setAttribute('role', 'dialog');
		box.setAttribute('aria-modal', 'true');
		var img = document.createElement('img');
		img.src = src;
		img.alt = alt;
		var close = document.createElement('button');
		close.type = 'button';
		close.setAttribute('aria-label', 'Close');
		close.textContent = '×';
		box.appendChild(img);
		box.appendChild(close);
		function done() {
			box.remove();
			document.removeEventListener('keydown', esc);
		}
		function esc(e) {
			if (e.key === 'Escape') {
				done();
			}
		}
		box.addEventListener('click', done);
		document.addEventListener('keydown', esc);
		document.body.appendChild(box);
		close.focus();
	}

	function initRating(select) {
		if (select.dataset.gooStars) {
			return;
		}
		select.dataset.gooStars = '1';
		var p = document.createElement('p');
		p.className = 'stars';
		var span = document.createElement('span');
		p.appendChild(span);
		for (var i = 1; i <= 5; i++) {
			var a = document.createElement('a');
			a.className = 'star-' + i;
			a.href = '#';
			a.setAttribute('role', 'radio');
			a.setAttribute('aria-checked', 'false');
			a.textContent = i + ' of 5 stars';
			span.appendChild(a);
		}
		select.style.display = 'none';
		select.parentNode.insertBefore(p, select);
		p.addEventListener('click', function (e) {
			var a = e.target.closest('a');
			if (!a) {
				return;
			}
			e.preventDefault();
			var n = parseInt(a.className.replace('star-', ''), 10);
			select.value = String(n);
			p.classList.add('selected');
			p.querySelectorAll('a').forEach(function (x) {
				var on = x === a;
				x.classList.toggle('active', on);
				x.setAttribute('aria-checked', on ? 'true' : 'false');
			});
		});
		var form = select.closest('form');
		if (form) {
			form.addEventListener('submit', function (e) {
				if (params.review_rating_required && !select.value) {
					e.preventDefault();
					window.alert(params.i18n_required_rating_text || 'Please select a rating');
				}
			});
		}
	}

	ready(function () {
		document.querySelectorAll('.woocommerce-tabs').forEach(initTabs);
		document.querySelectorAll('.woocommerce-product-gallery').forEach(initGallery);
		document.querySelectorAll('#rating').forEach(initRating);
	});

	// Variation images (add-to-cart-variation.js asks through this event).
	document.addEventListener('goo:variation-image', function (e) {
		var form = e.target;
		var product = form.closest('.product');
		var gallery = product ? product.querySelector('.woocommerce-product-gallery') : null;
		var img = gallery ? gallery.querySelector('.woocommerce-product-gallery__image img') : null;
		if (!img) {
			return;
		}
		var link = img.closest('a');
		var v = e.detail && e.detail.variation;
		if (v && v.image && v.image.src) {
			img.setAttribute('src', v.image.src);
			img.removeAttribute('srcset');
			if (v.image.alt) {
				img.setAttribute('alt', v.image.alt);
			}
			if (link && v.image.full_src) {
				link.setAttribute('href', v.image.full_src);
			}
			return;
		}
		['src', 'srcset', 'sizes', 'alt'].forEach(function (k) {
			var o = img.getAttribute('data-o_' + k);
			if (o !== null) {
				if (o === '') {
					img.removeAttribute(k);
				} else {
					img.setAttribute(k, o);
				}
			}
		});
		if (link && img.getAttribute('data-o_data-large_image')) {
			link.setAttribute('href', img.getAttribute('data-o_data-large_image'));
		}
	});
})();
