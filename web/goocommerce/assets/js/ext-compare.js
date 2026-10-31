/* Pursae Commerce Product Compare: toggles products through /?goo-ajax=compare_toggle and refreshes the bar. */
(function () {
	'use strict';
	var script = document.getElementById('goo-ext-compare-js');
	if (!script || !window.fetch || !window.FormData) { return; }
	var endpoint = script.getAttribute('data-endpoint');

	function refresh(data) {
		var bar = document.getElementById('goo-compare-bar');
		if (bar && data.bar) {
			var tmp = document.createElement('div');
			tmp.innerHTML = data.bar;
			bar.parentNode.replaceChild(tmp.firstChild, bar);
		}
		Array.prototype.forEach.call(document.querySelectorAll('.goo-compare-button'), function (b) {
			var on = (data.ids || []).indexOf(parseInt(b.getAttribute('data-product_id'), 10)) !== -1;
			b.classList.toggle('added', on);
			b.setAttribute('aria-pressed', on ? 'true' : 'false');
			var f = b.form && b.form.querySelector('[name="goo_compare_action"]');
			if (f) { f.value = on ? 'remove' : 'add'; }
		});
	}

	document.addEventListener('submit', function (e) {
		var form = e.target;
		if (!form.classList || !form.classList.contains('goo-compare-form') || form.classList.contains('goo-compare-clear-form')) { return; }
		if (document.querySelector('.goo-compare-table')) { return; } /* the compare page reloads to redraw the table */
		e.preventDefault();
		fetch(endpoint, { method: 'POST', body: new FormData(form), credentials: 'same-origin', headers: { 'X-Requested-With': 'XMLHttpRequest' } })
			.then(function (r) { return r.json(); })
			.then(function (data) {
				if (data.message) { window.alert(data.message); }
				refresh(data);
			})
			.catch(function () { form.submit(); });
	});
})();
