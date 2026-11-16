/*
 * Pursae guided tour (Dashboard). The steps are <section>s in #ptour-steps (partials/brand.html),
 * already translated by the admin's translation pass; this file only shows them one at a time in a
 * card next to the admin menu item each step names, with a spotlight around that item.
 *
 *   [data-ptour-start]        a button that starts the tour; its value is the step to open (0 by default)
 *   <section data-target="">  the menu item(s) to point at: the first selector that matches a visible
 *                             element; empty, or nothing visible (folded/mobile menu), centres the card
 */
(function () {
	'use strict';
	var src = document.getElementById('ptour-steps');
	if (!src) return;
	var steps = Array.prototype.slice.call(src.querySelectorAll('section'));
	if (!steps.length) return;
	function label(k) { var el = src.querySelector('[data-label="' + k + '"]'); return el ? el.textContent : k; }

	var root, card, body, spot, count, dots, back, next, at = 0, lastFocus = null;

	function build() {
		root = document.createElement('div');
		root.className = 'ptour';
		root.hidden = true;
		root.innerHTML =
			'<div class="ptour__scrim"></div><div class="ptour__spot"></div>' +
			'<div class="ptour__card" role="dialog" aria-modal="true" aria-labelledby="ptour-title" tabindex="-1">' +
			'<div class="ptour__step"><span class="ptour__count"></span>' +
			'<button type="button" class="ptour__x"><span class="dashicons dashicons-no-alt" aria-hidden="true"></span></button></div>' +
			'<div class="ptour__body"></div>' +
			'<div class="ptour__nav"><span class="ptour__dots" aria-hidden="true"></span>' +
			'<button type="button" class="px-btn px-btn--ghost" data-go="-1"></button>' +
			'<button type="button" class="px-btn" data-go="1"></button></div></div>';
		document.body.appendChild(root);
		card = root.querySelector('.ptour__card');
		body = root.querySelector('.ptour__body');
		spot = root.querySelector('.ptour__spot');
		count = root.querySelector('.ptour__count');
		dots = root.querySelector('.ptour__dots');
		back = root.querySelector('[data-go="-1"]');
		next = root.querySelector('[data-go="1"]');
		back.textContent = label('back');
		var x = root.querySelector('.ptour__x');
		x.setAttribute('aria-label', label('close'));
		x.addEventListener('click', close);
		root.querySelector('.ptour__scrim').addEventListener('click', close);
		back.addEventListener('click', function () { go(at - 1); });
		next.addEventListener('click', function () { at === steps.length - 1 ? close() : go(at + 1); });
		for (var i = 0; i < steps.length; i++) dots.appendChild(document.createElement('i'));
	}

	function target(step) {
		var sel = (step.getAttribute('data-target') || '').split(',');
		for (var i = 0; i < sel.length; i++) {
			var s = sel[i].trim();
			if (!s) continue;
			var el = document.querySelector(s);
			if (!el) continue;
			var a = el.querySelector(':scope > a') || el, r = a.getBoundingClientRect();
			if (r.width > 0 && r.height > 0 && r.right > 0 && r.left < innerWidth) return a;
		}
		return null;
	}

	function place() {
		if (!root || root.hidden) return;
		var el = target(steps[at]);
		var cw = card.offsetWidth, ch = card.offsetHeight, pad = 16;
		if (!el) {
			root.classList.remove('has-spot');
			root.classList.add('is-center');
			card.style.left = Math.max(pad, (innerWidth - cw) / 2) + 'px';
			card.style.top = Math.max(pad, (innerHeight - ch) / 2) + 'px';
			return;
		}
		root.classList.add('has-spot');
		root.classList.remove('is-center');
		var r = el.getBoundingClientRect(), g = 4;
		spot.style.left = (r.left + g) + 'px';
		spot.style.top = (r.top + g / 2) + 'px';
		spot.style.width = (r.width - g * 2) + 'px';
		spot.style.height = (r.height - g) + 'px';
		var rtl = document.documentElement.dir === 'rtl';
		var left = rtl ? r.left - cw - 18 : r.right + 18;
		var top = Math.min(Math.max(pad, r.top + r.height / 2 - 34), innerHeight - ch - pad);
		card.style.left = Math.min(Math.max(pad, left), innerWidth - cw - pad) + 'px';
		card.style.top = top + 'px';
	}

	function go(i) {
		if (i < 0 || i >= steps.length) return;
		at = i;
		body.innerHTML = steps[i].innerHTML;
		var h = body.querySelector('h2');
		if (h) h.id = 'ptour-title';
		count.textContent = (i + 1) + ' / ' + steps.length;
		back.style.visibility = i === 0 ? 'hidden' : '';
		next.textContent = i === steps.length - 1 ? label('done') : label('next');
		var d = dots.children;
		for (var k = 0; k < d.length; k++) d[k].className = k === i ? 'is-on' : '';
		var el = target(steps[i]);
		if (el && el.scrollIntoView) el.scrollIntoView({ block: 'nearest' });
		place();
		next.focus({ preventScroll: true });
	}

	function open(i) {
		if (!root) build();
		lastFocus = document.activeElement;
		root.hidden = false;
		document.addEventListener('keydown', key, true);
		addEventListener('resize', place);
		addEventListener('scroll', place, true);
		requestAnimationFrame(function () { root.classList.add('is-on'); });
		go(i || 0);
	}

	function close() {
		if (!root || root.hidden) return;
		root.hidden = true;
		root.classList.remove('is-on');
		document.removeEventListener('keydown', key, true);
		removeEventListener('resize', place);
		removeEventListener('scroll', place, true);
		if (lastFocus && lastFocus.focus) lastFocus.focus();
	}

	function key(e) {
		if (e.key === 'Escape') { e.preventDefault(); close(); }
		else if (e.key === 'ArrowRight') { e.preventDefault(); at === steps.length - 1 ? close() : go(at + 1); }
		else if (e.key === 'ArrowLeft') { e.preventDefault(); go(at - 1); }
		else if (e.key === 'Tab') { // keep focus inside the card
			var f = card.querySelectorAll('a[href],button:not([style*="hidden"])');
			if (!f.length) return;
			var first = f[0], last = f[f.length - 1];
			if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
			else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
		}
	}

	document.addEventListener('click', function (e) {
		var b = e.target.closest && e.target.closest('[data-ptour-start]');
		if (!b) return;
		e.preventDefault();
		open(parseInt(b.getAttribute('data-ptour-start'), 10) || 0);
	});
})();
