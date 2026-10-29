/*! Gooike cookie consent for WordPress Go.
 *
 * The page is the same for every visitor (it may come from the page cache); this script reads the
 * visitor's choice from the gooike_consent cookie, shows the banner when there is none (or the
 * policy version changed), brings blocked scripts and embeds to life for the accepted categories,
 * sends Google Consent Mode updates and logs each decision to /wp-json/gooike/v1/consent.
 *
 * Public API: window.Gooike.open(), .acceptAll(), .rejectAll(), .withdraw(), .consent(), .granted(category)
 * Events: document "gooike:consent" (detail: the consent), dataLayer event "gooike_consent_update".
 */
(function () {
	'use strict';
	var doc = document, root = doc.documentElement;
	var cfgEl = doc.getElementById('gooike-config');
	if (!cfgEl || window.Gooike) { return; }
	var C;
	try { C = JSON.parse(cfgEl.textContent); } catch (e) { return; }
	var banner = doc.getElementById('gooike-banner');
	var prefs = doc.getElementById('gooike-prefs');
	var floatBtn = doc.getElementById('gooike-float');
	var mode = C.mode || 'gdpr';
	var state = null; // { id, v, t, c: [categories], a: action }
	var implied = false; // CCPA / regions without a banner: optional categories run until the visitor opts out
	var lastFocus = null;
	var keys = (C.cats || []).map(function (c) { return c.key; });

	function optOut() { return mode === 'ccpa'; }
	function privacySignal() {
		return (C.gpc && navigator.globalPrivacyControl === true) ||
			(C.dnt && (navigator.doNotTrack === '1' || window.doNotTrack === '1'));
	}

	/* ---------- Storage ---------- */
	function readCookie() {
		var m = doc.cookie.match(new RegExp('(?:^|;\\s*)' + C.cookie + '=([^;]+)'));
		if (!m) { return null; }
		var s;
		try { s = JSON.parse(decodeURIComponent(m[1])); } catch (e) { return null; }
		if (!s || typeof s !== 'object' || s.v !== C.v || !s.id || !Array.isArray(s.c)) { return null; }
		if (s.t && Date.now() / 1000 - s.t > C.days * 86400) { return null; }
		return s;
	}
	function writeCookie(s) {
		if (C.preview) { return; }
		doc.cookie = C.cookie + '=' + encodeURIComponent(JSON.stringify(s)) + '; Max-Age=' + (C.days * 86400) +
			'; Path=/; SameSite=Lax' + (location.protocol === 'https:' ? '; Secure' : '');
	}
	function uuid() {
		if (window.crypto && crypto.randomUUID) { return crypto.randomUUID(); }
		var b = new Uint8Array(16), h = '';
		(window.crypto || window.msCrypto).getRandomValues(b);
		b[6] = (b[6] & 15) | 64; b[8] = (b[8] & 63) | 128;
		for (var i = 0; i < 16; i++) { h += (b[i] + 256).toString(16).slice(1); if (i === 3 || i === 5 || i === 7 || i === 9) { h += '-'; } }
		return h;
	}

	/* ---------- Consent ---------- */
	function granted(key) {
		if (key === 'necessary') { return true; }
		if (state) { return state.c.indexOf(key) >= 0; }
		return implied;
	}
	function signals() {
		var u = {};
		(C.signals || []).forEach(function (s) { u[s] = 'denied'; });
		(C.cats || []).forEach(function (c) {
			if (granted(c.key)) { (c.signals || []).forEach(function (s) { u[s] = 'granted'; }); }
		});
		return u;
	}
	function consentMode() {
		if (C.cm && typeof window.gtag === 'function') { window.gtag('consent', 'update', signals()); }
	}

	/* ---------- Activation of blocked scripts and embeds ---------- */
	var queue = [], running = false;
	function activate() {
		var els = doc.querySelectorAll('[data-gooike-category]:not([data-gooike-done])');
		for (var i = 0; i < els.length; i++) {
			var el = els[i];
			if (!granted(el.getAttribute('data-gooike-category'))) { continue; }
			el.setAttribute('data-gooike-done', '1');
			if (el.tagName === 'SCRIPT') { queue.push(el); } else { enableSrc(el); }
		}
		pump();
	}
	function enableSrc(el) {
		['srcset', 'src'].forEach(function (a) {
			var v = el.getAttribute('data-gooike-' + a);
			if (v !== null) { el.setAttribute(a, v); el.removeAttribute('data-gooike-' + a); }
		});
		var w = el.parentNode;
		if (w && w.classList && w.classList.contains('gooike-embed')) { w.classList.add('is-loaded'); }
	}
	// Scripts run one after the other in document order; a blocking external script is waited for,
	// so inline code that depends on it finds it loaded.
	function pump() {
		if (running) { return; }
		var old = queue.shift();
		if (!old) { return; }
		running = true;
		var s = doc.createElement('script');
		for (var i = 0; i < old.attributes.length; i++) {
			var a = old.attributes[i];
			if (a.name === 'type' || a.name.indexOf('data-gooike-') === 0) { continue; }
			s.setAttribute(a.name, a.value);
		}
		var type = old.getAttribute('data-gooike-type');
		if (type) { s.type = type; }
		var src = old.getAttribute('data-gooike-src');
		var next = function () { running = false; pump(); };
		if (src) {
			s.src = src;
			if (!old.hasAttribute('async') && !old.hasAttribute('defer')) {
				s.async = false;
				s.onload = s.onerror = next;
				old.parentNode.insertBefore(s, old.nextSibling);
				return;
			}
		} else {
			s.text = old.text;
		}
		old.parentNode.insertBefore(s, old.nextSibling);
		next();
	}

	/* ---------- Cookies of withdrawn categories ---------- */
	function deleteCookies(cats) {
		var pats = [];
		cats.forEach(function (k) { (C.cookies[k] || []).forEach(function (n) { pats.push(n); }); });
		if (!pats.length) { return; }
		var host = location.hostname.split('.'), domains = [''];
		for (var i = 0; i < host.length - 1; i++) { domains.push('; domain=.' + host.slice(i).join('.')); }
		doc.cookie.split(';').forEach(function (part) {
			var name = part.split('=')[0].trim();
			var hit = pats.some(function (p) {
				return p.slice(-1) === '*' ? name.indexOf(p.slice(0, -1)) === 0 : name === p;
			});
			if (!hit) { return; }
			domains.forEach(function (d) { doc.cookie = name + '=; Max-Age=0; Path=/' + d; });
		});
	}

	/* ---------- Logging ---------- */
	function log(action) {
		if (!C.log || C.preview || !state) { return; }
		var body = JSON.stringify({
			consent_id: state.id, action: action, categories: state.c, policy_version: C.v,
			url: location.pathname, gpc: navigator.globalPrivacyControl === true, regulation: mode
		});
		try {
			if (navigator.sendBeacon && navigator.sendBeacon(C.endpoint, new Blob([body], { type: 'application/json' }))) { return; }
		} catch (e) { /* fall back to fetch */ }
		if (window.fetch) {
			fetch(C.endpoint, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: body, credentials: 'same-origin', keepalive: true })['catch'](function () {});
		}
	}

	/* ---------- Decisions ---------- */
	function decide(action, cats, silent) {
		var prev = state ? state.c.slice() : (implied ? keys.slice() : null);
		cats = cats.filter(function (k) { return k !== 'necessary' && keys.indexOf(k) >= 0; });
		var revoked = prev ? prev.filter(function (k) { return k !== 'necessary' && cats.indexOf(k) < 0; }) : [];
		var logAction = action;
		if (state && revoked.length && action !== 'gpc') { logAction = 'withdraw'; }
		state = { id: (state && state.id) || uuid(), v: C.v, t: Math.floor(Date.now() / 1000), c: ['necessary'].concat(cats), a: logAction };
		implied = false;
		writeCookie(state);
		hideBanner();
		closePrefs();
		consentMode();
		activate();
		showID();
		log(logAction);
		try {
			doc.dispatchEvent(new CustomEvent('gooike:consent', { detail: { id: state.id, categories: state.c.slice(), action: logAction } }));
		} catch (e) { /* old browsers */ }
		(window.dataLayer = window.dataLayer || []).push({ event: 'gooike_consent_update', gooike_categories: state.c.slice(), gooike_action: logAction });
		if (!silent) { announce(C.saved); }
		showFloat();
		if (revoked.length && !C.preview) {
			deleteCookies(revoked);
			// Scripts that already ran cannot be unloaded: a reload starts the page without them.
			if (C.reload && prev) { setTimeout(function () { location.reload(); }, 350); }
		}
		if (C.preview) { setTimeout(function () { state = null; hideFloat(); showBanner(); }, 1600); }
	}
	function acceptAll() { decide('accept_all', keys.slice()); }
	function rejectAll() { decide('reject_all', []); }
	function saveChoices() {
		var cats = [];
		if (prefs) {
			var boxes = prefs.querySelectorAll('input[data-gooike-cat]');
			for (var i = 0; i < boxes.length; i++) { if (boxes[i].checked) { cats.push(boxes[i].getAttribute('data-gooike-cat')); } }
		}
		var all = keys.filter(function (k) { return k !== 'necessary'; });
		decide(cats.length === all.length ? 'accept_all' : (cats.length === 0 ? 'reject_all' : 'custom'), cats);
	}

	/* ---------- UI ---------- */
	var live;
	function announce(msg) {
		if (!msg) { return; }
		if (!live) {
			live = doc.createElement('div');
			live.className = 'gooike gk-sr';
			live.setAttribute('role', 'status');
			live.setAttribute('aria-live', 'polite');
			doc.body.appendChild(live);
		}
		live.textContent = '';
		setTimeout(function () { live.textContent = msg; }, 50);
	}
	function focusables(el) {
		return Array.prototype.filter.call(
			el.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]), select, textarea, summary, [tabindex]:not([tabindex="-1"])'),
			function (x) { return x.offsetWidth || x.offsetHeight || x.getClientRects().length; }
		);
	}
	function trap(el, e) {
		var f = focusables(el);
		if (!f.length) { return; }
		var first = f[0], last = f[f.length - 1];
		if (e.shiftKey && (doc.activeElement === first || !el.contains(doc.activeElement))) { e.preventDefault(); last.focus(); }
		else if (!e.shiftKey && doc.activeElement === last) { e.preventDefault(); first.focus(); }
	}
	function bannerIsModal() { return !!(banner && C.modal); }
	function showBanner() {
		if (!banner) { return; }
		banner.hidden = false;
		hideFloat();
		if (bannerIsModal()) {
			root.classList.add('gooike-locked');
			lastFocus = doc.activeElement;
			var f = focusables(banner);
			if (f.length) { f[0].focus({ preventScroll: true }); }
		}
	}
	function hideBanner() {
		if (!banner || banner.hidden) { return; }
		banner.hidden = true;
		if (prefs && prefs.hidden) { root.classList.remove('gooike-locked'); }
	}
	function syncSwitches() {
		if (!prefs) { return; }
		var boxes = prefs.querySelectorAll('input[data-gooike-cat]');
		for (var i = 0; i < boxes.length; i++) {
			var k = boxes[i].getAttribute('data-gooike-cat');
			boxes[i].checked = state ? state.c.indexOf(k) >= 0 : (optOut() && !(k === 'marketing' && privacySignal()));
		}
	}
	function openPrefs() {
		if (!prefs) { return; }
		if (prefs.hidden) { lastFocus = doc.activeElement; }
		syncSwitches();
		showID();
		prefs.hidden = false;
		root.classList.add('gooike-locked');
		var dlg = prefs.querySelector('.gk-dialog');
		if (dlg) { dlg.focus({ preventScroll: true }); }
	}
	function closePrefs() {
		if (!prefs || prefs.hidden) { return; }
		prefs.hidden = true;
		if (!banner || banner.hidden || !bannerIsModal()) { root.classList.remove('gooike-locked'); }
		if (banner && !banner.hidden) {
			var f = focusables(banner);
			if (f.length) { f[f.length - 1].focus({ preventScroll: true }); return; }
		}
		if (lastFocus && lastFocus.focus && doc.contains(lastFocus)) { lastFocus.focus({ preventScroll: true }); }
		else if (floatBtn && !floatBtn.hidden) { floatBtn.focus({ preventScroll: true }); }
	}
	function showFloat() { if (floatBtn && (C.float || C.preview) && (state || C.preview)) { floatBtn.hidden = false; } }
	function hideFloat() { if (floatBtn) { floatBtn.hidden = true; } }
	function showID() {
		var ids = doc.querySelectorAll('[data-gooike-id]'), dates = doc.querySelectorAll('[data-gooike-date]');
		var i;
		for (i = 0; i < ids.length; i++) { ids[i].textContent = state ? state.id : '—'; }
		for (i = 0; i < dates.length; i++) { dates[i].textContent = state && state.t ? '(' + new Date(state.t * 1000).toLocaleString() + ')' : ''; }
	}

	doc.addEventListener('click', function (e) {
		var t = e.target && e.target.closest ? e.target.closest('[data-gooike], [data-gooike-accept-cat], a[href="#gooike-preferences"]') : null;
		if (!t) { return; }
		var cat = t.getAttribute('data-gooike-accept-cat');
		if (cat) {
			e.preventDefault();
			var cats = state ? state.c.slice() : [];
			if (cats.indexOf(cat) < 0) { cats.push(cat); }
			decide(cats.length >= keys.length ? 'accept_all' : 'custom', cats);
			return;
		}
		var a = t.getAttribute('data-gooike') || 'open';
		e.preventDefault();
		switch (a) {
			case 'accept': acceptAll(); break;
			case 'reject': rejectAll(); break;
			case 'save': saveChoices(); break;
			case 'customize': case 'open': openPrefs(); break;
			case 'close': closePrefs(); break;
		}
	});
	doc.addEventListener('keydown', function (e) {
		if (prefs && !prefs.hidden) {
			if (e.key === 'Escape') { e.preventDefault(); closePrefs(); return; }
			if (e.key === 'Tab') { trap(prefs, e); }
			return;
		}
		if (bannerIsModal() && !banner.hidden && e.key === 'Tab') { trap(banner, e); }
	});

	/* ---------- Start ---------- */
	function start() {
		state = C.preview ? null : readCookie();
		if (state) {
			consentMode();
			activate();
			showID();
			showFloat();
			return;
		}
		if (C.preview) {
			if (C.open === 'prefs') { openPrefs(); } else if (C.open !== 'none') { showBanner(); }
			return;
		}
		if (mode === 'none') { // geo targeting: no consent law for this visitor
			implied = true;
			consentMode();
			activate();
			return;
		}
		if (privacySignal()) {
			// Global Privacy Control / Do Not Track: an opt-out the visitor already expressed. Under an
			// opt-in law nothing optional runs; under CCPA the sale/sharing (marketing) stops.
			decide('gpc', optOut() ? keys.filter(function (k) { return k !== 'marketing'; }) : [], true);
			return;
		}
		if (optOut()) { implied = true; consentMode(); activate(); }
		showBanner();
	}

	window.Gooike = {
		open: openPrefs,
		acceptAll: acceptAll,
		rejectAll: rejectAll,
		withdraw: function () { rejectAll(); },
		consent: function () { return state ? JSON.parse(JSON.stringify(state)) : null; },
		granted: granted
	};

	function geoThenStart() {
		if (!C.geo || C.preview) { start(); return; }
		var cached = null;
		try { cached = sessionStorage.getItem('gooike_geo'); } catch (e) { /* storage blocked */ }
		if (cached) { mode = cached; start(); return; }
		fetch(C.geoEndpoint, { credentials: 'omit' }).then(function (r) { return r.json(); }).then(function (g) {
			if (g && g.regulation) { mode = g.regulation; }
			try { sessionStorage.setItem('gooike_geo', mode); } catch (e) { /* storage blocked */ }
		})['catch'](function () {}).then(start);
	}

	if (doc.readyState === 'loading') { doc.addEventListener('DOMContentLoaded', geoThenStart); } else { geoThenStart(); }
})();
