// Scenario tests of gooike.js on a minimal fake DOM (no jsdom needed): node --test web/gooike/gooike.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const src = fs.readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), 'gooike.js'), 'utf8');

class El {
	constructor(tag, attrs = {}) {
		this.tagName = tag.toUpperCase(); this.attrs = { ...attrs }; this.children = []; this.parentNode = null;
		this.hidden = !!attrs.hidden; this.text = attrs.text || ''; this.textContent = this.text; this.checked = !!attrs.checked;
		const set = new Set((attrs.class || '').split(/\s+/).filter(Boolean));
		this.classList = { add: (c) => set.add(c), remove: (c) => set.delete(c), contains: (c) => set.has(c) };
	}
	get attributes() { return Object.entries(this.attrs).map(([name, value]) => ({ name, value })); }
	getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
	setAttribute(n, v) { this.attrs[n] = String(v); }
	removeAttribute(n) { delete this.attrs[n]; }
	hasAttribute(n) { return n in this.attrs; }
	set type(v) { this.attrs.type = v; }
	set src(v) { this.attrs.src = v; }
	appendChild(c) { c.parentNode = this; this.children.push(c); return c; }
	insertBefore(c, ref) { c.parentNode = this; const i = ref ? this.children.indexOf(ref) : -1; if (i < 0) this.children.push(c); else this.children.splice(i, 0, c); return c; }
	get nextSibling() { const i = this.parentNode.children.indexOf(this); return this.parentNode.children[i + 1] || null; }
	querySelector() { return null; }
	querySelectorAll() { return []; }
	getClientRects() { return [1]; }
	focus() {}
	closest() { return this; }
	contains() { return true; }
}

function setup({ cfg = {}, gpc = false, cats = ['statistics', 'marketing'], cookie = '' } = {}) {
	const C = { cookie: 'gooike_consent', days: 365, v: 1, cats: [{ key: 'necessary', signals: [] }, { key: 'statistics', signals: ['analytics_storage'] }, { key: 'marketing', signals: ['ad_storage'] }],
		cookies: { statistics: ['_ga', '_gid', '_ga_*'] }, cm: true, log: true, endpoint: '/beacon', mode: 'gdpr', gpc: true, signals: ['analytics_storage', 'ad_storage'], ...cfg };
	const jar = new Map();
	if (cookie) for (const part of cookie.split(';')) { const [k, ...v] = part.split('='); jar.set(k.trim(), v.join('=')); }
	const body = new El('body'); const head = new El('head');
	const els = {
		'gooike-config': Object.assign(new El('script'), { textContent: JSON.stringify(C) }),
		'gooike-banner': new El('div', { hidden: true }), 'gooike-prefs': new El('div', { hidden: true }), 'gooike-float': new El('button', { hidden: true }),
	};
	const blocked = cats.map((c, i) => { const s = new El('script', { type: 'text/plain', 'data-gooike-category': c, 'data-gooike-src': 'https://t.example/' + c + '.js' }); body.appendChild(s); return s; });
	const listeners = {}; const beacons = []; const gtag = []; const events = [];
	const doc = {
		readyState: 'complete', documentElement: new El('html'), body, activeElement: null,
		getElementById: (id) => els[id] || null,
		createElement: (t) => new El(t),
		contains: () => true,
		addEventListener: (n, f) => { (listeners[n] ||= []).push(f); },
		dispatchEvent: (e) => { events.push(e); return true; },
		querySelectorAll(sel) {
			if (sel.startsWith('[data-gooike-category]')) return body.children.filter((e) => e.hasAttribute('data-gooike-category') && !e.hasAttribute('data-gooike-done'));
			return [];
		},
		get cookie() { return [...jar].map(([k, v]) => k + '=' + v).join('; '); },
		set cookie(s) {
			const [pair, ...attrs] = s.split(';'); const i = pair.indexOf('='); const k = pair.slice(0, i).trim();
			if (attrs.some((a) => a.trim() === 'Max-Age=0')) jar.delete(k); else jar.set(k, pair.slice(i + 1));
		},
	};
	const win = { Gooike: undefined, dataLayer: undefined, gtag: (...a) => gtag.push(a), crypto: { randomUUID: () => '0123456789abcdef-0123-4567-89ab-cdef01234567' }, doNotTrack: '0' };
	const ctx = vm.createContext({
		document: doc, window: win, crypto: win.crypto, navigator: { globalPrivacyControl: gpc, doNotTrack: '0', sendBeacon: (u, b) => { beacons.push({ u, b }); return true; } },
		location: { pathname: '/p', protocol: 'https:', hostname: 'example.com', reload() {} }, Blob: class { constructor(p) { this.p = p.join(''); } },
		CustomEvent: class { constructor(t, o) { this.type = t; this.detail = o.detail; } }, setTimeout: () => 0, JSON, Date, Math, Array, Uint8Array, encodeURIComponent, decodeURIComponent, RegExp,
	});
	vm.runInContext(src, ctx);
	const click = (attrs) => { const t = new El('button', attrs); for (const f of listeners.click || []) f({ target: t, preventDefault() {} }); };
	// Blocking external scripts are waited for: finish each load so the next one runs.
	const live = () => { for (let i = 0; i < 10; i++) { const w = body.children.find((e) => e.onload && !e.loaded); if (!w) break; w.loaded = true; w.onload(); } return live0(); };
	const live0 = () => body.children.filter((e) => e.tagName === 'SCRIPT' && !e.hasAttribute('data-gooike-category') && e.attrs.src);
	return { doc, win, els, click, jar, beacons, gtag, events, live, blocked };
}

test('accept all activates blocked scripts, updates Consent Mode and logs', () => {
	const t = setup();
	assert.equal(t.els['gooike-banner'].hidden, false, 'banner shows without a choice');
	assert.equal(t.live().length, 0, 'nothing runs before consent');
	t.click({ 'data-gooike': 'accept' });
	assert.deepEqual(t.live().map((s) => s.attrs.src).sort(), ['https://t.example/marketing.js', 'https://t.example/statistics.js']);
	const upd = t.gtag.find((a) => a[0] === 'consent' && a[1] === 'update');
	assert.equal(upd[2].analytics_storage, 'granted'); assert.equal(upd[2].ad_storage, 'granted');
	assert.equal(t.beacons.length, 1); assert.equal(JSON.parse(t.beacons[0].b.p).action, 'accept_all');
	assert.ok(t.jar.has('gooike_consent'));
	assert.equal(t.els['gooike-banner'].hidden, true);
});

test('reject keeps scripts inert and denies signals', () => {
	const t = setup();
	t.click({ 'data-gooike': 'reject' });
	assert.equal(t.live().length, 0);
	assert.equal(t.blocked.every((s) => !s.hasAttribute('data-gooike-done')), true);
	const upd = t.gtag.find((a) => a[1] === 'update');
	assert.equal(upd[2].analytics_storage, 'denied'); assert.equal(upd[2].ad_storage, 'denied');
	assert.equal(JSON.parse(t.beacons[0].b.p).action, 'reject_all');
});

test('withdrawing deletes the cookies of the revoked categories', () => {
	const t = setup();
	t.click({ 'data-gooike': 'accept' });
	t.jar.set('_ga', '1'); t.jar.set('_ga_ABC', '2'); t.jar.set('keep', '3');
	t.win.Gooike.withdraw();
	assert.equal(t.jar.has('_ga'), false); assert.equal(t.jar.has('_ga_ABC'), false); assert.equal(t.jar.has('keep'), true);
	assert.equal(t.win.Gooike.granted('statistics'), false);
	assert.equal(JSON.parse(t.beacons.at(-1).b.p).action, 'withdraw');
});

test('Global Privacy Control under an opt-in law decides silently: no banner, nothing optional runs', () => {
	const t = setup({ gpc: true });
	assert.equal(t.els['gooike-banner'].hidden, true);
	assert.equal(t.live().length, 0);
	assert.equal(JSON.parse(t.beacons[0].b.p).action, 'gpc');
	assert.equal(t.win.Gooike.granted('marketing'), false);
});

test('a stored choice is honoured on load without a banner', () => {
	const val = encodeURIComponent(JSON.stringify({ id: 'abc', v: 1, t: Math.floor(Date.now() / 1000), c: ['necessary', 'statistics'], a: 'custom' }));
	const t = setup({ cookie: 'gooike_consent=' + val });
	assert.equal(t.els['gooike-banner'].hidden, true);
	assert.deepEqual(t.live().map((s) => s.attrs.src), ['https://t.example/statistics.js']);
	assert.equal(t.beacons.length, 0, 'restoring a choice is not logged again');
});
