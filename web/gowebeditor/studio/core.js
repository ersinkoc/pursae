/* Pursae Web Builder Studio — core: namespace, DOM helpers, icons and the thin layer over the editor's
   public API ($e.run commands, elementor.documents, the selection). Every edit goes through a
   command so history, undo/redo and saving stay upstream's. */
(function () {
	'use strict';
	const S = window.GWEStudio = window.GWEStudio || {};
	S.cfg = window.GWEStudioConfig || {};

	// --- DOM ----------------------------------------------------------------------------------
	/** h('button.cls', {attrs}, ...children): a tiny element factory. */
	S.h = function (sel, attrs, ...kids) {
		const m = /^([a-z0-9]+)?((?:[.#][\w-]+)*)$/i.exec(sel) || [];
		const el = document.createElement(m[1] || 'div');
		(m[2] || '').replace(/([.#])([\w-]+)/g, (_, t, v) => { if (t === '#') { el.id = v; } else { el.classList.add(v); } });
		if (attrs) {
			for (const k in attrs) {
				const v = attrs[k];
				if (v == null || v === false) { continue; }
				if (k.startsWith('on') && typeof v === 'function') { el.addEventListener(k.slice(2).toLowerCase(), v); }
				else if (k === 'html') { el.innerHTML = v; }
				else if (k === 'text') { el.textContent = v; }
				else if (k === 'style' && typeof v === 'object') { Object.assign(el.style, v); }
				else if (k === 'dataset') { Object.assign(el.dataset, v); }
				else if (k in el && typeof v !== 'string' && k !== 'list') { el[k] = v; }
				else { el.setAttribute(k, v === true ? '' : v); }
			}
		}
		S.append(el, kids);
		return el;
	};
	S.append = function (el, kids) {
		for (const k of kids.flat(Infinity)) {
			if (k == null || k === false) { continue; }
			el.appendChild(typeof k === 'string' || typeof k === 'number' ? document.createTextNode(String(k)) : k);
		}
		return el;
	};
	S.empty = (el) => { while (el.firstChild) { el.removeChild(el.firstChild); } return el; };
	S.debounce = function (fn, ms) {
		let t = null;
		const d = function (...a) { clearTimeout(t); t = setTimeout(() => { t = null; fn.apply(this, a); }, ms); };
		/** Runs a pending call now (nothing when none is pending). */
		d.flush = function (...a) { if (t === null) { return; } clearTimeout(t); t = null; fn.apply(this, a); };
		return d;
	};
	S.esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

	// --- icons (20px, stroke) -----------------------------------------------------------------
	const P = {
		plus: 'M10 4v12M4 10h12',
		edit: 'M4 6h6M14 6h2M4 14h2M10 14h6M12 4v4M8 12v4',
		layers: 'M10 3l7 4-7 4-7-4 7-4zM3 11l7 4 7-4',
		site: 'M10 3a7 7 0 100 14 7 7 0 000-14zM3 10h14M10 3c2 2 3 4.5 3 7s-1 5-3 7c-2-2-3-4.5-3-7s1-5 3-7z',
		undo: 'M7 8H4V5M4.5 8A6 6 0 1110 16',
		redo: 'M13 8h3V5M15.5 8A6 6 0 1010 16',
		desktop: 'M3 4h14v9H3zM7 16h6M10 13v3',
		tablet: 'M5 3h10v14H5zM9 14h2',
		mobile: 'M7 3h6v14H7zM9.5 14h1',
		eye: 'M2 10s3-5 8-5 8 5 8 5-3 5-8 5-8-5-8-5zM10 8a2 2 0 100 4 2 2 0 000-4z',
		eyeOff: 'M3 3l14 14M8 5.3A8 8 0 0110 5c5 0 8 5 8 5a13 13 0 01-2.2 2.7M12 14.7a8 8 0 01-2 .3c-5 0-8-5-8-5a13 13 0 013-3.3',
		down: 'M5 8l5 5 5-5',
		up: 'M5 12l5-5 5 5',
		right: 'M8 5l5 5-5 5',
		left: 'M12 5l-5 5 5 5',
		arrowUp: 'M10 16V4M5 9l5-5 5 5',
		arrowDown: 'M10 4v12M5 11l5 5 5-5',
		copy: 'M7 7h9v9H7zM4 13V4h9',
		trash: 'M4 6h12M8 6V4h4v2M6 6l1 10h6l1-10',
		save: 'M4 4h10l2 2v10H4zM7 4v4h6V4M7 16v-5h6v5',
		close: 'M5 5l10 10M15 5L5 15',
		search: 'M9 4a5 5 0 100 10A5 5 0 009 4zM13 13l4 4',
		grip: 'M7 5h.01M13 5h.01M7 10h.01M13 10h.01M7 15h.01M13 15h.01',
		back: 'M16 10H4M9 5l-5 5 5 5',
		external: 'M11 4h5v5M16 4l-7 7M14 12v4H4V6h4',
		gear: 'M10 7a3 3 0 100 6 3 3 0 000-6zM10 2v2M10 16v2M2 10h2M16 10h2M4.3 4.3l1.4 1.4M14.3 14.3l1.4 1.4M4.3 15.7l1.4-1.4M14.3 5.7l1.4-1.4',
		heading: 'M5 4v12M15 4v12M5 10h10',
		text: 'M4 5h12M4 9h12M4 13h12M4 17h7',
		image: 'M3 4h14v12H3zM3 13l4-4 4 4 2-2 4 4M13 7.5h.01',
		button: 'M3 7h14v6H3zM7 10h6',
		star: 'M10 3l2.2 4.5 4.8.7-3.5 3.4.8 4.9-4.3-2.3-4.3 2.3.8-4.9L3 8.2l4.8-.7z',
		list: 'M8 5h9M8 10h9M8 15h9M4 5h.01M4 10h.01M4 15h.01',
		video: 'M3 5h14v10H3zM8.5 7.5v5l4-2.5z',
		divider: 'M3 10h14',
		spacer: 'M10 3v14M7 6l3-3 3 3M7 14l3 3 3-3',
		columns: 'M3 4h14v12H3zM10 4v12',
		gallery: 'M3 3h6v6H3zM11 3h6v6h-6zM3 11h6v6H3zM11 11h6v6h-6z',
		form: 'M3 4h14v4H3zM3 11h14v4H3z',
		map: 'M10 17s5-4.5 5-9a5 5 0 00-10 0c0 4.5 5 9 5 9zM10 6a2 2 0 100 4 2 2 0 000-4z',
		posts: 'M3 4h6v5H3zM11 4h6M11 7h4M3 11h6v5H3zM11 11h6M11 14h4',
		container: 'M3 4h14v12H3z',
		section: 'M2 6h16M2 14h16M4 6v8M16 6v8',
		link: 'M8.5 11.5l3-3M7 9l-2 2a2.8 2.8 0 004 4l2-2M13 11l2-2a2.8 2.8 0 00-4-4L9 7',
		check: 'M4 10l4 4 8-8',
		alignLeft: 'M3 5h14M3 9h9M3 13h14M3 17h9',
		alignCenter: 'M3 5h14M5.5 9h9M3 13h14M5.5 17h9',
		alignRight: 'M3 5h14M8 9h9M3 13h14M8 17h9',
		alignJustify: 'M3 5h14M3 9h14M3 13h14M3 17h14',
		row: 'M3 6h6v8H3zM11 6h6v8h-6z',
		col: 'M5 3h10v6H5zM5 11h10v6H5z',
		pages: 'M5 3h7l3 3v11H5zM12 3v3h3',
		menu: 'M4 6h12M4 10h12M4 14h12',
		history: 'M3 10a7 7 0 107-7 7 7 0 00-5 2L3 7M3 3v4h4M10 6v4l3 2',
		brush: 'M13 3l4 4-7 7-4-4zM6 10l-3 7 7-3',
		upload: 'M10 13V4M6 8l4-4 4 4M4 16h12',
		bold: 'M6 4h5a3 3 0 010 6H6zM6 10h6a3 3 0 010 6H6z',
		italic: 'M9 4h6M5 16h6M12 4l-4 12',
		dots: 'M5 10h.01M10 10h.01M15 10h.01',
	};
	/** S.icon('plus') → an inline SVG element (decorative). */
	S.icon = function (name, size) {
		const ns = 'http://www.w3.org/2000/svg';
		const svg = document.createElementNS(ns, 'svg');
		const n = size || 20;
		svg.setAttribute('viewBox', '0 0 20 20');
		svg.setAttribute('width', n);
		svg.setAttribute('height', n);
		svg.setAttribute('aria-hidden', 'true');
		svg.setAttribute('focusable', 'false');
		svg.classList.add('gwe-i');
		const path = document.createElementNS(ns, 'path');
		path.setAttribute('d', P[name] || P.container);
		svg.appendChild(path);
		return svg;
	};
	/** An icon button with an accessible name and a tooltip. */
	S.iconButton = function (icon, label, onClick, extra) {
		return S.h('button.gwe-ib', Object.assign({ type: 'button', 'aria-label': label, title: label, onClick }, extra || {}), S.icon(icon));
	};

	// --- events -------------------------------------------------------------------------------
	const handlers = {};
	S.on = (name, fn) => { (handlers[name] = handlers[name] || []).push(fn); };
	S.emit = (name, ...args) => {
		(handlers[name] || []).forEach((fn) => {
			try { fn(...args); } catch (e) { console.error('[studio]', name, e); }
		});
	};

	// --- notices ------------------------------------------------------------------------------
	S.toast = function (msg, type) {
		let box = document.getElementById('gwe-studio-toasts');
		if (!box) {
			box = S.h('div#gwe-studio-toasts', { role: 'status', 'aria-live': 'polite' });
			document.body.appendChild(box);
		}
		const t = S.h('div.gwe-toast' + (type ? '.is-' + type : ''), { text: msg });
		box.appendChild(t);
		setTimeout(() => { t.classList.add('is-out'); setTimeout(() => t.remove(), 300); }, type === 'error' ? 6000 : 2600);
	};

	// --- the editor ---------------------------------------------------------------------------
	S.doc = () => elementor.documents.getCurrent();
	S.root = () => elementor.getPreviewContainer();
	S.isKitOpen = () => { const d = S.doc(); return !!(d && d.config && d.config.type === 'kit'); };
	/** Runs a public editor command; errors become a notice instead of breaking the UI. */
	S.run = function (cmd, args) {
		try {
			return $e.run(cmd, args || {});
		} catch (e) {
			console.error('[studio]', cmd, e);
			S.toast((e && e.message) || String(e), 'error');
			return null;
		}
	};
	S.selected = function () {
		const list = elementor.selection && elementor.selection.getElements ? elementor.selection.getElements() : [];
		const c = list && list.length ? list[0] : null;
		return c && c.model && c.view && !c.view.isDestroyed ? c : null;
	};
	S.select = (c) => { if (c) { S.run('document/elements/select', { container: c }); } };
	S.deselect = () => S.run('document/elements/deselect-all');
	S.device = () => (elementor.channels.deviceMode.request('currentMode') || 'desktop');
	S.setDevice = (device) => S.run('panel/change-device-mode', { device });
	S.elType = (c) => c.model.get('elType');
	S.type = (c) => (c.model.get('elType') === 'widget' ? c.model.get('widgetType') : c.model.get('elType'));
	S.isContainer = (c) => c && (c.model.get('elType') === 'container' || c.model.get('elType') === 'section' || c.model.get('elType') === 'column');
	/** The element's display name (its _title when renamed). */
	S.title = function (c) {
		const t = c.settings && c.settings.get('_title');
		if (t) { return t; }
		const data = elementor.getElementData(c.model);
		return (data && data.title) || S.type(c);
	};
	S.topLevel = function (c) {
		const root = S.root();
		while (c && c.parent && c.parent !== root && c.parent.id !== root.id) { c = c.parent; }
		return c && c.parent ? c : null;
	};
	S.indexOf = (c) => (c && c.parent ? c.parent.children.indexOf(c) : -1);

	/** Fresh element ids for a tree of element JSON (ids from a template are not reused). */
	S.withIds = function (model) {
		const m = Object.assign({}, model);
		m.id = elementorCommon.helpers.getUniqueId();
		m.settings = m.settings || {};
		m.elements = (m.elements || []).map(S.withIds);
		if (m.elType === 'widget') { m.elements = []; }
		return m;
	};
	/** Inserts element JSON into a container at an index as one undo step; returns the new containers. */
	S.insert = function (models, parent, at, title) {
		parent = parent || S.root();
		const list = Array.isArray(models) ? models : [models];
		const id = $e.internal('document/history/start-log', { type: 'add', title: title || 'Section', container: parent });
		const out = [];
		try {
			list.forEach((model, i) => {
				const options = { edit: false };
				if (at != null && at >= 0) { options.at = at + i; }
				const c = $e.run('document/elements/create', { container: parent, model: S.withIds(model), options });
				if (c) { out.push(c); }
			});
		} catch (e) {
			console.error('[studio] insert', e);
			S.toast('Could not insert: ' + e.message, 'error');
		}
		$e.internal('document/history/end-log', { id });
		return out;
	};
	/** Writes settings through document/elements/settings (history, undo and the panel follow). */
	S.set = function (c, settings) {
		return S.run('document/elements/settings', { container: c, settings, options: { external: true } });
	};
	S.get = (c, key) => (c && c.settings ? c.settings.get(key) : undefined);
	S.globalOf = (c, key) => ((c.settings.get('__globals__') || {})[key] || '');
	/** Links a setting to a kit global ('globals/colors?id=primary') or unlinks it (''). */
	S.setGlobal = (c, key, value) => S.run('document/globals/settings', { container: c, settings: { [key]: value }, options: { external: true } });
	/** Runs several commands as one undo step. */
	S.batch = function (title, c, fn) {
		const id = $e.internal('document/history/start-log', { type: 'change', title: title || 'Settings', container: c });
		try { fn(); } finally { $e.internal('document/history/end-log', { id }); }
	};
	S.control = function (c, key) {
		const controls = c && c.settings && c.settings.controls;
		return controls ? controls[key] : null;
	};
	S.scrollTo = function (c) {
		const el = c && c.view && c.view.el;
		if (el && el.scrollIntoView) { el.scrollIntoView({ behavior: 'smooth', block: 'center' }); }
	};
	/** Opens the upstream panel for an element (Advanced settings). */
	S.editInUpstream = function (c) {
		S.openUpstream();
		S.run('panel/editor/open', { model: c.model, view: c.view, container: c });
	};

	// --- kit globals --------------------------------------------------------------------------
	S.globals = { colors: {}, typography: {} };
	/** Kit globals in the kit's order: primary, secondary, text, accent, then custom ones. */
	S.ordered = function (map) {
		const rank = { primary: 0, secondary: 1, text: 2, accent: 3 };
		return Object.values(map || {}).sort((a, b) => (a.id in rank ? rank[a.id] : 9) - (b.id in rank ? rank[b.id] : 9));
	};
	S.loadGlobals = async function (refresh) {
		try {
			const opts = refresh ? { refresh: true } : {};
			const [col, typo] = await Promise.all([$e.data.get('globals/colors', {}, opts), $e.data.get('globals/typography', {}, opts)]);
			S.globals.colors = (col && col.data) || {};
			S.globals.typography = (typo && typo.data) || {};
		} catch (e) {
			console.warn('[studio] globals', e);
		}
		S.emit('globals');
		return S.globals;
	};

	// --- REST ---------------------------------------------------------------------------------
	S.rest = async function (path, opts) {
		opts = opts || {};
		const res = await fetch(S.cfg.rest + path, {
			method: opts.method || 'GET', credentials: 'same-origin',
			headers: { 'X-WP-Nonce': S.cfg.nonce, 'Content-Type': 'application/json', Accept: 'application/json' },
			body: opts.body ? JSON.stringify(opts.body) : undefined,
		});
		const text = await res.text();
		let data = null;
		try { data = text ? JSON.parse(text) : null; } catch (e) { data = text; }
		if (!res.ok) {
			const err = new Error((data && data.message) || ('HTTP ' + res.status));
			err.status = res.status;
			throw err;
		}
		return data;
	};

	// --- no-op settings ------------------------------------------------------------------------
	/** Skips document/elements/settings runs that change nothing (an inline edit ending without a
	   change, a field re-sending its value), so every undo step undoes something visible. */
	S.skipUnchangedSettings = function () {
		if (S.skipHook || !$e.modules || !$e.modules.hookData || !$e.modules.hookData.Dependency) { return; }
		const plain = (v) => JSON.stringify(v && v.toJSON ? v.toJSON() : (v === undefined ? '' : v));
		class SkipUnchanged extends $e.modules.hookData.Dependency {
			getCommand() { return 'document/elements/settings'; }
			getId() { return 'gwe-studio-skip-unchanged-settings'; }
			getConditions(args) { return !!(args && args.settings && (args.container || args.containers)); }
			apply(args) {
				const list = args.containers || [args.container];
				return !list.every((c) => c && c.settings && Object.keys(args.settings).every((k) => plain(c.settings.get(k)) === plain(args.settings[k])));
			}
		}
		S.skipHook = new SkipUnchanged();
		S.skipHook.register();
	};

	// --- readiness ----------------------------------------------------------------------------
	/** Calls fn once the editor, its document and the preview are ready. */
	S.ready = function (fn) {
		const ok = () => window.elementor && window.$e && elementor.documents && elementor.documents.getCurrent && elementor.documents.getCurrent()
			&& elementor.getPreviewContainer && elementor.$previewContents && elementor.$previewContents[0] && elementor.$previewContents[0].body
			&& elementor.selection;
		if (ok()) { fn(); return; }
		const t = setInterval(() => { if (ok()) { clearInterval(t); fn(); } }, 100);
	};
})();
