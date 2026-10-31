/* Pursae Web Builder Studio — the Studio / Advanced switch. Loaded on every editor page: in Advanced mode
   it adds a "Studio" button to the upstream top bar and loads the Studio on demand; the choice is
   saved per user (web-builder/v1/studio/preferences). */
(function () {
	'use strict';
	const cfg = window.GWEStudioConfig || {};
	const T = window.GWEStudioToggle = {};

	T.save = function (mode) {
		cfg.mode = mode;
		return fetch(cfg.rest + 'web-builder/v1/studio/preferences', {
			method: 'POST', credentials: 'same-origin',
			headers: { 'X-WP-Nonce': cfg.nonce, 'Content-Type': 'application/json' },
			body: JSON.stringify({ mode }),
		}).catch((e) => console.warn('[studio] preference not saved', e));
	};

	function load(src, tag) {
		return new Promise((resolve, reject) => {
			const el = document.createElement(tag || 'script');
			if (tag === 'link') { el.rel = 'stylesheet'; el.href = src; el.id = 'gwe-studio-css'; } else { el.src = src; el.async = false; }
			el.onload = resolve;
			el.onerror = () => reject(new Error('Could not load ' + src));
			(tag === 'link' ? document.head : document.body).appendChild(el);
		});
	}

	T.toAdvanced = function () {
		T.save('advanced');
		if (window.GWEStudio && window.GWEStudio.hide) { window.GWEStudio.hide(); }
		T.button(true);
	};
	T.toStudio = async function () {
		T.save('studio');
		T.button(false);
		const S = window.GWEStudio;
		if (S && S.mount && S.show) { S.show(); return; }
		try {
			if (!document.getElementById('gwe-studio-css')) { await load(cfg.css, 'link'); }
			for (const src of cfg.scripts || []) { await load(src); }
			window.GWEStudio.show();
		} catch (e) {
			console.error('[studio]', e);
			alert(e.message);
		}
	};

	/** The "Studio" button shown in Advanced mode, placed left of the upstream top bar's right group. */
	let btn = null;
	T.button = function (on) {
		if (!on) { if (btn) { btn.remove(); btn = null; } return; }
		if (btn) { return; }
		btn = document.createElement('button');
		btn.type = 'button';
		btn.id = 'gwe-studio-switch';
		btn.textContent = 'Studio';
		btn.title = 'Switch to the simple Studio panel';
		btn.setAttribute('aria-label', 'Switch to Pursae Web Builder Studio');
		btn.style.cssText = 'position:fixed;top:12px;z-index:100000;height:30px;padding:0 12px;border:1px solid #2271b1;border-radius:2px;background:#fff;color:#2271b1;font:600 13px/28px -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;cursor:pointer';
		btn.addEventListener('click', () => T.toStudio());
		document.body.appendChild(btn);
		const place = () => {
			if (!btn) { return; }
			const groups = document.querySelectorAll('#elementor-editor-wrapper-v2 header .MuiGrid-container');
			const right = groups.length ? groups[groups.length - 1].firstElementChild || groups[groups.length - 1] : null;
			const r = right && right.getBoundingClientRect();
			btn.style.left = r && r.width ? Math.max(8, r.left - btn.offsetWidth - 12) + 'px' : 'auto';
			btn.style.right = r && r.width ? 'auto' : '220px';
		};
		place();
		window.addEventListener('resize', place);
		const t = setInterval(() => { if (!btn) { clearInterval(t); return; } place(); }, 1000);
	};

	if (cfg.mode === 'advanced') {
		const start = () => T.button(true);
		if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', start); } else { start(); }
	}
})();
