/*! Pursae Pixel 0.1 — event tracking for Pursae.
 *
 * Reads the page configuration (#pixelgo-config), turns every event into each platform's own call
 * (Meta Pixel, GA4, Google Ads, TikTok, Pinterest, Microsoft Advertising) and forwards a copy to the
 * server-side API with the same event id so the platforms can deduplicate.
 *
 * Consent: with Pursae Cookie as the gate the tags are inert until the visitor accepts their category.
 * Events wait in a queue until their platform is loaded (window.pixelgoReady[platform], set by the tag)
 * and are sent only to platforms whose category is accepted; a refused platform never receives them.
 *
 * Public API: window.pixelgo.track(name, params, opts) for custom code; the "pixelgo:event" DOM event
 * fires for every event (detail: {name, id, p}).
 */
(function () {
	'use strict';
	var doc = document, win = window;
	var el = doc.getElementById('pixelgo-config');
	if (!el) { return; }
	var C;
	try { C = JSON.parse(el.textContent); } catch (e) { return; }
	var PL = C.platforms || {};
	var log = [];            // events and what they were already sent to
	var timer = 0, started = Date.now();
	var seenOnce = {};

	/* ---------- helpers ---------- */
	function uuid() {
		if (win.crypto && crypto.randomUUID) { return crypto.randomUUID(); }
		var a = new Uint8Array(16), h = '';
		(win.crypto || win.msCrypto).getRandomValues(a);
		for (var i = 0; i < 16; i++) { h += (a[i] < 16 ? '0' : '') + a[i].toString(16); }
		return h;
	}
	function dbg() { if (C.debug && win.console) { console.log.apply(console, ['[Pursae Pixel]'].concat([].slice.call(arguments))); } }
	function getCookie(n) {
		var m = doc.cookie.match(new RegExp('(?:^|;\\s*)' + n.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
		if (!m) { return ''; }
		try { return decodeURIComponent(m[1]); } catch (e) { return m[1]; }
	}
	function setCookie(n, v, days) {
		doc.cookie = n + '=' + encodeURIComponent(v) + '; Max-Age=' + (days * 86400) + '; Path=/; SameSite=Lax' + (location.protocol === 'https:' ? '; Secure' : '');
	}
	function store(kind, k, v) {
		try { var s = win[kind + 'Storage']; if (v === undefined) { return s.getItem(k); } s.setItem(k, v); } catch (e) { return null; }
		return null;
	}
	function extend(a, b) { for (var k in b) { if (Object.prototype.hasOwnProperty.call(b, k)) { a[k] = b[k]; } } return a; }
	function num(v) { var f = parseFloat(v); return isNaN(f) ? 0 : f; }

	/* ---------- consent ---------- */
	function consent() {
		if (!C.consent.require) { return { decided: true, cats: null }; }
		var raw = getCookie(C.consent.cookie), s = null;
		if (raw) { try { s = JSON.parse(raw); } catch (e) { s = null; } }
		if (!s || !s.c || !s.c.indexOf) { return { decided: false, cats: [] }; }
		return { decided: true, cats: s.c };
	}
	function allowed(p) {
		var c = consent();
		return !C.consent.require || (c.cats && c.cats.indexOf(C.consent.cats[p]) !== -1);
	}
	function anyAllowed() { for (var p in PL) { if (allowed(p)) { return true; } } return false; }

	/* ---------- attribution: UTM and click ids, kept in a first-party cookie ---------- */
	var ATTR_KEYS = ['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content', 'fbclid', 'gclid', 'ttclid', 'epik', 'msclkid'];
	function attribution() {
		if (!C.attr.enabled) { return {}; }
		var cur = {}, old = {}, q = location.search, any = false;
		try { old = JSON.parse(getCookie('pixelgo_attr') || '{}') || {}; } catch (e) { old = {}; }
		ATTR_KEYS.forEach(function (k) {
			var m = q.match(new RegExp('[?&]' + k + '=([^&#]*)'));
			if (m && m[1]) { try { cur[k] = decodeURIComponent(m[1].replace(/\+/g, ' ')).slice(0, 200); any = true; } catch (e) { /* ignore */ } }
		});
		var merged = any ? extend(extend({}, old), cur) : old;
		if (any && !merged.lp) { merged.lp = location.pathname.slice(0, 200); }
		if (any && anyAllowed()) { setCookie('pixelgo_attr', JSON.stringify(merged), C.attr.days); }
		return merged;
	}
	var ATTR = attribution();
	function attrParams() {
		var o = {};
		['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content'].forEach(function (k) { if (ATTR[k]) { o[k] = ATTR[k]; } });
		return o;
	}

	/* ---------- canonical → platform parameters ---------- */
	function ids(items) { return (items || []).map(function (i) { return i.id; }); }
	function build(plat, p, name) {
		var o = {}, items = p.items || [], x = p.extra || {}, k;
		switch (plat) {
			case 'meta':
				if (p.value != null) { o.value = p.value; }
				if (p.currency) { o.currency = p.currency; }
				if (items.length) {
					o.content_ids = ids(items); o.content_type = p.content_type || 'product';
					o.contents = items.map(function (i) { return { id: i.id, quantity: i.quantity, item_price: i.price }; });
				} else if (p.content_type) { o.content_type = p.content_type; }
				if (p.num_items != null) { o.num_items = p.num_items; }
				['content_name', 'content_category', 'search_string', 'order_id'].forEach(function (f) { if (p[f]) { o[f] = p[f]; } });
				break;
			case 'ga4':
				if (p.currency) { o.currency = p.currency; }
				if (p.value != null) { o.value = p.value; }
				if (p.order_id) { o.transaction_id = p.order_id; }
				if (p.tax != null) { o.tax = p.tax; }
				if (p.shipping != null) { o.shipping = p.shipping; }
				if (p.payment_type) { o.payment_type = p.payment_type; }
				if (p.search_string) { o.search_term = p.search_string; }
				if (p.content_name && !items.length) { o.item_list_name = p.content_name; }
				if (items.length) { o.items = items.map(function (i) { return { item_id: i.id, item_name: i.name, price: i.price, quantity: i.quantity, item_category: i.category }; }); }
				break;
			case 'gads':
				if (p.value != null) { o.value = p.value; }
				if (p.currency) { o.currency = p.currency; }
				if (p.order_id) { o.transaction_id = p.order_id; }
				if (items.length) { o.items = items.map(function (i) { return { id: i.id, google_business_vertical: 'retail' }; }); }
				break;
			case 'tiktok':
				if (p.value != null) { o.value = p.value; }
				if (p.currency) { o.currency = p.currency; }
				if (p.search_string) { o.query = p.search_string; }
				if (p.content_name && !items.length) { o.description = p.content_name; }
				if (items.length) {
					o.content_type = 'product';
					o.contents = items.map(function (i) { return { content_id: i.id, content_type: 'product', content_name: i.name, quantity: i.quantity, price: i.price }; });
				}
				break;
			case 'pinterest':
				if (p.value != null) { o.value = p.value; }
				if (p.currency) { o.currency = p.currency; }
				if (p.num_items != null) { o.order_quantity = p.num_items; }
				if (p.order_id) { o.order_id = p.order_id; }
				if (p.search_string) { o.search_query = p.search_string; }
				if (items.length) { o.line_items = items.map(function (i) { return { product_id: i.id, product_name: i.name, product_price: i.price, product_quantity: i.quantity, product_category: i.category }; }); }
				break;
			case 'bing':
				o.event_category = items.length ? 'ecommerce' : 'engagement';
				o.event_label = p.content_name || p.search_string || name;
				if (p.value != null) { o.event_value = p.value; o.revenue_value = p.value; }
				if (p.currency) { o.currency = p.currency; }
				break;
		}
		for (k in x) { if (Object.prototype.hasOwnProperty.call(x, k)) { o[k] = x[k]; } }
		return o;
	}

	/* ---------- platform senders ---------- */
	function platformName(plat, name) {
		var m = C.map[name];
		if (m) { return m[plat] || ''; }
		if (plat === 'ga4') { return name.toLowerCase().replace(/[ -]+/g, '_'); }
		if (plat === 'pinterest') { return 'custom'; }
		return name;
	}
	var senders = {
		meta: function (ev, n) {
			if (!win.fbq) { return false; }
			var std = C.map[ev.name] ? C.metaStandard[ev.name] === true : false;
			if (ev.user && ev.name === 'Purchase') { (PL.meta.ids || []).forEach(function (id) { fbq('init', id, ev.user); }); }
			fbq(std ? 'track' : 'trackCustom', n, build('meta', ev.p, ev.name), { eventID: ev.id });
			return true;
		},
		ga4: function (ev, n) {
			if (!win.gtag) { return false; }
			var o = build('ga4', ev.p, ev.name);
			if (ev.name === 'Scroll' && ev.p.extra && ev.p.extra.percent) { o.percent_scrolled = ev.p.extra.percent; }
			if (ev.name === 'PageView') { o.page_title = doc.title; o.page_location = location.href; }
			(PL.ga4.ids || []).forEach(function (id) { var d = extend({ send_to: id }, o); gtag('event', n, d); });
			return true;
		},
		gads: function (ev) {
			if (!win.gtag) { return false; }
			var labels = PL.gads.labels || {}, o = build('gads', ev.p, ev.name), n = platformName('gads', ev.name), sent = false;
			(PL.gads.ids || []).forEach(function (id) {
				if (labels[ev.name]) {
					var c = { send_to: id + '/' + labels[ev.name] };
					if (ev.p.value != null) { c.value = ev.p.value; c.currency = ev.p.currency; }
					if (ev.p.order_id) { c.transaction_id = ev.p.order_id; }
					gtag('event', 'conversion', c); sent = true;
				}
				if (n) { gtag('event', n, extend({ send_to: id }, o)); sent = true; }
			});
			return true;
		},
		tiktok: function (ev, n) {
			if (!win.ttq) { return false; }
			if (n === '@page') { ttq.page(); return true; }
			var o = build('tiktok', ev.p, ev.name);
			(PL.tiktok.ids || []).forEach(function (id) {
				var inst = ttq.instance ? ttq.instance(id) : ttq;
				if (ev.user && ev.user.em && inst.identify) { inst.identify({ email: ev.user.em, phone_number: ev.user.ph }); }
				(inst.track || ttq.track).call(inst, n, o, { event_id: ev.id });
			});
			return true;
		},
		pinterest: function (ev, n) {
			if (!win.pintrk) { return false; }
			if (n === '@page') { pintrk('page'); return true; }
			var o = build('pinterest', ev.p, ev.name);
			o.event_id = ev.id;
			if (n === 'custom') { o.event_name = ev.name; }
			pintrk('track', n, o);
			return true;
		},
		bing: function (ev, n) {
			win.uetq = win.uetq || [];
			win.uetq.push('event', n, build('bing', ev.p, ev.name));
			return true;
		}
	};

	/* ---------- queue and dispatch ---------- */
	function ready(p) { return !!(win.pixelgoReady && win.pixelgoReady[p]); }
	function targets(ev) {
		var list = [];
		for (var p in PL) {
			if (ev.platforms && ev.platforms.length && ev.platforms.indexOf(p) === -1) { continue; }
			list.push(p);
		}
		return list;
	}
	function flush() {
		var pending = 0, forward = {};
		log.forEach(function (e) {
			targets(e.ev).forEach(function (p) {
				if (e.done[p]) { return; }
				if (!allowed(p)) { pending++; return; }
				var n = platformName(p, e.ev.name);
				if (!n) { e.done[p] = 1; return; }
				// The server-side copy does not wait for the browser tag: it is the one that survives ad blockers.
				if (PL[p].server && !e.ev.skipServer && !e.fwd[p]) { e.fwd[p] = 1; (forward[e.ev.id] = forward[e.ev.id] || { e: e.ev, p: [] }).p.push(p); }
				if (!ready(p)) { pending++; return; }
				try {
					if (senders[p](e.ev, n)) { e.done[p] = 1; markOnce(e.ev); dbg(p, n, e.ev.p); }
				} catch (err) { e.done[p] = 1; dbg('error', p, err); }
			});
		});
		Object.keys(forward).forEach(function (id) { toServer(forward[id].e, forward[id].p); });
		var c = consent();
		if (Date.now() - started > 30000 && c.decided) { log = log.filter(function (e) { return targets(e.ev).some(function (p) { return !e.done[p] && allowed(p) && ready(p); }); }); }
		if (log.length > 60) { log = log.slice(-60); }
		if (pending && !timer) { timer = setInterval(function () { flush(); if (!pendingLeft()) { clearInterval(timer); timer = 0; } }, 500); }
	}
	function pendingLeft() {
		return log.some(function (e) { return targets(e.ev).some(function (p) { return !e.done[p] && platformName(p, e.ev.name) && (allowed(p) ? Date.now() - started < 30000 : !consent().decided); }); });
	}
	function markOnce(ev) { if (ev.onceKey) { store(ev.onceKind, ev.onceKey, '1'); } }
	function toServer(ev, plats) {
		if (!C.rest) { return; }
		markOnce(ev);
		var body = JSON.stringify({ name: ev.name, event_id: ev.id, p: ev.p, platforms: plats, url: location.pathname + location.search });
		try {
			if (win.fetch) { fetch(C.rest, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: body, keepalive: true, credentials: 'same-origin' }).catch(function () {}); }
		} catch (e) { /* ignore */ }
	}

	function fire(name, p, opts) {
		opts = opts || {};
		p = p || {};
		var ev = { name: name, id: opts.id || uuid(), p: p, platforms: opts.platforms, user: opts.user, skipServer: !!opts.skipServer };
		var a = attrParams();
		if (opts.once) {
			// "Once" is recorded when the event is delivered, not when it is queued: a visitor who has not
			// consented yet must still be tracked after they do.
			ev.onceKey = 'pxg_once_' + opts.once; ev.onceKind = opts.session ? 'session' : 'local';
			if (store(ev.onceKind, ev.onceKey)) { return; }
		}
		if (Object.keys(a).length) { p.extra = extend(extend({}, a), p.extra || {}); }
		log.push({ ev: ev, done: {}, fwd: {} });
		try { doc.dispatchEvent(new CustomEvent('pixelgo:event', { detail: { name: name, id: ev.id, p: p } })); } catch (e) { /* old browsers */ }
		flush();
		return ev.id;
	}
	win.pixelgo = { track: function (name, params, opts) { return fire(String(name), params || {}, opts); } };
	doc.addEventListener('gooike:consent', function () { ATTR = attribution(); flush(); });

	/* ---------- page events from the server ---------- */
	(C.events || []).forEach(function (e) {
		var once = e.once && e.p && e.p.order_id ? 'purchase_' + e.p.order_id : '';
		fire(e.name, e.p || {}, { id: e.id, user: e.user, skipServer: e.skip_server, once: once });
	});

	/* ---------- general triggers ---------- */
	var G = C.general || {};
	function skipForm(f) {
		return !!(f.closest && f.closest('.woocommerce-cart-form, form.cart, form.variations_form, form.checkout, form.woocommerce-checkout, form[role="search"], .search-form, .woocommerce-product-search, #commentform, .comment-form, form.login, form.woocommerce-form-login, form.woocommerce-form-register, #loginform, #registerform, #lostpasswordform'));
	}
	function vars(v) {
		return String(v).replace(/\{url\}/g, location.href).replace(/\{title\}/g, doc.title).replace(/\{referrer\}/g, doc.referrer);
	}
	function customParams(ce) {
		var o = {}, k;
		for (k in (ce.params || {})) { if (Object.prototype.hasOwnProperty.call(ce.params, k)) { o[k] = vars(ce.params[k]); } }
		return o;
	}
	function customEvent(ce) {
		var p = { extra: customParams(ce) };
		if (ce.value) { p.value = num(ce.value); p.currency = ce.currency || ((C.page.product && C.page.product.currency) || 'USD'); }
		fire(ce.name, p, { platforms: ce.platforms && ce.platforms.length ? ce.platforms : undefined, once: ce.once ? ce.id : '', session: true });
	}
	function urlMatches(ce) { return !ce.url || (location.pathname + location.search).indexOf(ce.url) !== -1; }

	var customs = C.custom || [];
	customs.filter(function (c) { return c.trigger === 'page_visit' && urlMatches(c); }).forEach(customEvent);

	// clicks
	doc.addEventListener('click', function (e) {
		var t = e.target;
		if (!t || !t.closest) { return; }
		customs.forEach(function (ce) {
			if (ce.trigger !== 'click' || !urlMatches(ce)) { return; }
			try { if (t.closest(ce.selector)) { customEvent(ce); } } catch (err) { /* bad selector */ }
		});
		var a = t.closest('a[href]');
		if (a) {
			var href = a.getAttribute('href') || '';
			if (/^tel:/i.test(href) && G.tel) { fire('Contact', { extra: { contact_type: 'phone', link_url: href.slice(4, 60) } }); }
			else if (/^mailto:/i.test(href) && G.mail) { fire('Contact', { extra: { contact_type: 'email' } }); }
			else if (/^https?:/i.test(a.href)) {
				var ext = (a.pathname.split('.').pop() || '').toLowerCase();
				if (G.download && C.downloads.indexOf(ext) !== -1 && a.host === location.host) {
					fire('Download', { extra: { file_extension: ext, file_name: a.pathname.split('/').pop().slice(0, 100), link_url: a.href.slice(0, 200) } });
				} else if (G.outbound && a.host !== location.host) {
					fire('OutboundClick', { extra: { link_url: a.href.slice(0, 200), link_domain: a.host } });
				}
			}
		}
		woocommerceClick(t, e);
	}, true);

	// forms
	var lastSubmit = 0;
	doc.addEventListener('submit', function (e) {
		var f = e.target;
		if (!f || f.tagName !== 'FORM') { return; }
		customs.forEach(function (ce) {
			if (ce.trigger !== 'form_submit' || !urlMatches(ce)) { return; }
			try { if (f.matches(ce.selector)) { customEvent(ce); } } catch (err) { /* bad selector */ }
		});
		var now = Date.now();
		if (now - lastSubmit < 1500) { return; }
		if (f.matches && f.matches('#commentform, .comment-form')) { if (G.comment) { lastSubmit = now; fire('Comment', {}); } return; }
		if (f.matches && f.matches('form.woocommerce-form-register, #registerform, form.register')) { if (G.registration) { lastSubmit = now; fire('CompleteRegistration', {}); } return; }
		if (f.matches && f.matches('form.checkout, form.woocommerce-checkout')) { addPaymentInfo(); return; }
		if (G.form && !skipForm(f)) { lastSubmit = now; fire('Lead', { content_name: (f.getAttribute('id') || f.getAttribute('name') || 'form').slice(0, 80), extra: { form_id: (f.id || '').slice(0, 80) } }); }
	}, true);

	// scroll depth
	var depths = (G.scroll ? C.scroll : []).concat(customs.filter(function (c) { return c.trigger === 'scroll' && urlMatches(c); }).map(function (c) { return c.number; }));
	if (depths.length) {
		var hit = {};
		var onScroll = function () {
			var h = doc.documentElement, max = (h.scrollHeight || 1) - h.clientHeight;
			var pct = max > 0 ? Math.round(((win.pageYOffset || h.scrollTop) / max) * 100) : 100;
			depths.forEach(function (d) {
				if (pct >= d && !hit[d]) {
					hit[d] = 1;
					if (G.scroll && C.scroll.indexOf(d) !== -1) { fire('Scroll', { extra: { percent: d } }, { once: '' }); }
					customs.forEach(function (ce) { if (ce.trigger === 'scroll' && ce.number === d && urlMatches(ce)) { customEvent(ce); } });
				}
			});
		};
		win.addEventListener('scroll', onScroll, { passive: true });
		onScroll();
	}

	// time on page (visible seconds)
	var marks = (G.time ? C.time : []).concat(customs.filter(function (c) { return c.trigger === 'time' && urlMatches(c); }).map(function (c) { return c.number; }));
	if (marks.length) {
		var secs = 0, done = {};
		setInterval(function () {
			if (doc.hidden) { return; }
			secs++;
			marks.forEach(function (m) {
				if (secs >= m && !done[m]) {
					done[m] = 1;
					if (G.time && C.time.indexOf(m) !== -1) { fire('TimeOnPage', { extra: { seconds: m } }); }
					customs.forEach(function (ce) { if (ce.trigger === 'time' && ce.number === m && urlMatches(ce)) { customEvent(ce); } });
				}
			});
		}, 1000);
	}

	// HTML5 video
	if (G.video) {
		['play', 'ended'].forEach(function (t) {
			doc.addEventListener(t, function (e) {
				var v = e.target;
				if (!v || v.tagName !== 'VIDEO') { return; }
				fire(t === 'play' ? 'VideoPlay' : 'VideoComplete', { content_name: (v.currentSrc || '').split('/').pop().slice(0, 100), extra: { video_url: (v.currentSrc || '').slice(0, 200) } }, { once: t === 'play' ? '' : '' });
			}, true);
		});
	}

	/* ---------- the store ---------- */
	var W = C.woo || {};
	function itemId(id, sku) {
		var base = (W.sku && sku) ? sku : String(id);
		return (W.prefix || '') + base + (W.suffix || '');
	}
	function addToCart(items, extra) {
		if (!W.add_to_cart) { return; }
		var p = { items: items, currency: (C.page.product || {}).currency || undefined, content_type: 'product' };
		var v = 0, n = 0;
		items.forEach(function (i) { v += (i.price || 0) * i.quantity; n += i.quantity; });
		if (v) { p.value = Math.round(v * 100) / 100; }
		p.num_items = n;
		fire('AddToCart', extend(p, extra || {}));
	}
	function woocommerceClick(t, e) {
		var b = t.closest('.single_add_to_cart_button, .add_to_cart_button, a.remove, .remove_from_cart_button');
		if (!b || b.classList.contains('disabled') || b.classList.contains('wc-variation-selection-needed') || b.classList.contains('wc-variation-is-unavailable')) { return; }
		if (b.matches('.single_add_to_cart_button') && C.page.product) {
			var base = C.page.product, form = b.closest('form'), q = 1;
			var qi = form && form.querySelector('input.qty, input[name="quantity"]');
			if (qi) { q = Math.max(parseInt(qi.value, 10) || 1, 1); }
			var items = (base.items || []).map(function (i) { return extend({}, i); });
			items.forEach(function (i) { i.quantity = q; });
			addToCart(items, { content_name: base.content_name, content_category: base.content_category, currency: base.currency });
		} else if (b.matches('.add_to_cart_button') && b.getAttribute('data-product_id')) {
			var q2 = Math.max(parseInt(b.getAttribute('data-quantity'), 10) || 1, 1);
			addToCart([{ id: itemId(b.getAttribute('data-product_id'), b.getAttribute('data-product_sku')), quantity: q2, price: 0 }]);
		} else if (b.matches('a.remove, .remove_from_cart_button') && W.remove_from_cart && b.getAttribute('data-product_id')) {
			fire('RemoveFromCart', { items: [{ id: itemId(b.getAttribute('data-product_id'), b.getAttribute('data-product_sku')), quantity: 1, price: 0 }], content_type: 'product' });
		}
	}
	var paymentSent = false;
	function addPaymentInfo() {
		if (paymentSent || !W.add_payment_info || !C.page.checkout) { return; }
		paymentSent = true;
		var p = extend({}, C.page.checkout);
		var sel = doc.querySelector('input[name="payment_method"]:checked');
		if (sel) { p.payment_type = sel.value; }
		fire('AddPaymentInfo', p);
	}
	doc.addEventListener('change', function (e) {
		if (e.target && e.target.name === 'payment_method') { addPaymentInfo(); }
	}, true);

	// the store's AJAX add to cart from outside the click path (quick view, wishlists, bundles)
	if (win.jQuery) {
		win.jQuery(doc.body).on('added_to_cart.pixelgo', function () { /* the click handler already sent it */ });
	}
	flush();
})();
