/* Pursae Commerce Wishlists: the heart buttons post to /?goo-ajax=wishlist_toggle without reloading. */
(function () {
	'use strict';
	var script = document.getElementById('goo-ext-wishlist-js');
	if (!script || !window.fetch || !window.FormData) { return; }
	var endpoint = script.getAttribute('data-endpoint');
	document.addEventListener('submit', function (e) {
		var form = e.target;
		if (!form.classList || !form.classList.contains('goo-wishlist-form')) { return; }
		e.preventDefault();
		var btn = form.querySelector('.goo-wishlist-button');
		if (btn) { btn.classList.add('loading'); btn.disabled = true; }
		fetch(endpoint, { method: 'POST', body: new FormData(form), credentials: 'same-origin', headers: { 'X-Requested-With': 'XMLHttpRequest' } })
			.then(function (r) { return r.json(); })
			.then(function (data) {
				if (btn) { btn.classList.remove('loading'); btn.disabled = false; }
				if (data.login_required && data.login_url) { window.location = data.login_url; return; }
				if (!data.success) { if (data.message) { window.alert(data.message.replace(/<[^>]+>/g, '')); } return; }
				if (data.redirect) { window.location = data.redirect; return; }
				var id = btn && btn.getAttribute('data-product_id');
				Array.prototype.forEach.call(document.querySelectorAll('.goo-wishlist-button[data-product_id="' + id + '"]'), function (b) {
					b.classList.toggle('added', !!data.added);
					b.setAttribute('aria-pressed', data.added ? 'true' : 'false');
					var t = b.querySelector('.goo-wishlist-text');
					if (t) { t.textContent = data.added ? b.getAttribute('data-added-text') : b.getAttribute('data-text'); }
					var h = b.querySelector('.goo-wishlist-heart');
					if (h) { h.innerHTML = data.added ? '&#9829;' : '&#9825;'; }
				});
			})
			.catch(function () { form.submit(); });
	});
})();
