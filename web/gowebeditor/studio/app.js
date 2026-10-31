/* Pursae Web Builder Studio — the app: top bar (menu, page switcher, devices, undo/redo, Advanced, preview,
   save/publish), the left rail (Add, Edit, Layers, Site), the upstream panel overlay for Advanced
   settings, and the wiring that keeps everything in step with the editor. */
(function () {
	'use strict';
	const S = window.GWEStudio;
	const { h } = S;
	const TABS = [['add', 'Add', 'plus'], ['edit', 'Edit', 'edit'], ['layers', 'Layers', 'layers'], ['site', 'Site', 'site']];
	let els = {};
	let activeTab = 'add';
	const panes = {};

	// --- popovers (menu, page switcher) -------------------------------------------------------
	let openPop = null;
	function popover(anchor, content, opts) {
		closePop();
		const r = anchor.getBoundingClientRect();
		const pop = h('div.gwe-popover', { role: (opts && opts.role) || 'menu', style: { top: (r.bottom + 4) + 'px', left: Math.max(8, Math.min(r.left, window.innerWidth - 328)) + 'px' } }, content);
		document.body.appendChild(pop);
		anchor.setAttribute('aria-expanded', 'true');
		openPop = { pop, anchor };
		pop.addEventListener('keydown', (e) => {
			if (e.key === 'Escape') { e.stopPropagation(); closePop(true); }
			if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
				const items = Array.from(pop.querySelectorAll('[role=menuitem],a,button,input')).filter((n) => n.offsetParent);
				const i = items.indexOf(document.activeElement);
				const n = items[(i + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length];
				if (n) { e.preventDefault(); n.focus(); }
			}
		});
		setTimeout(() => { const f = pop.querySelector('input,[role=menuitem],a,button'); if (f) { f.focus(); } }, 0);
		return pop;
	}
	function closePop(refocus) {
		if (!openPop) { return; }
		openPop.pop.remove();
		openPop.anchor.setAttribute('aria-expanded', 'false');
		if (refocus) { openPop.anchor.focus(); }
		openPop = null;
	}
	document.addEventListener('mousedown', (e) => { if (openPop && !openPop.pop.contains(e.target) && !openPop.anchor.contains(e.target)) { closePop(); } }, true);
	const menuItem = (text, onClick, icon, href) => h(href ? 'a.gwe-menuitem' : 'button.gwe-menuitem', { role: 'menuitem', type: href ? null : 'button', href, onClick: href ? null : () => { closePop(); onClick(); } }, S.icon(icon || 'right', 16), h('span', { text }));

	function logoMenu(anchor) {
		const L = S.cfg.links || {};
		const urls = (S.doc() && S.doc().config.urls) || {};
		popover(anchor, [
			menuItem('Exit to dashboard', null, 'back', urls.exit_to_dashboard || L.dashboard),
			menuItem('Site setup', null, 'brush', L.siteSetup),
			menuItem('Theme Builder', null, 'layers', L.themeBuilder),
			menuItem('Pursae Web Builder settings', null, 'gear', L.settings),
			h('hr.gwe-sep'),
			menuItem('Editor preferences', () => { S.openUpstream(); $e.route('panel/editor-preferences'); }, 'edit'),
			menuItem('History and revisions', () => { S.openUpstream(); $e.route('panel/history/revisions'); }, 'history'),
			menuItem('Keyboard shortcuts', () => { try { $e.route('shortcuts'); } catch (e) { S.toast('Shortcuts are not available.', 'error'); } }, 'menu'),
		]);
	}

	function pageMenu(anchor) {
		const search = h('input.gwe-input.gwe-search', { type: 'search', placeholder: 'Search pages', 'aria-label': 'Search pages' });
		const list = h('div.gwe-page-list', { role: 'group', 'aria-label': 'Pages' }, h('p.gwe-muted', { text: 'Loading…' }));
		const fill = (pages) => {
			S.empty(list);
			if (!pages.length) { list.append(h('p.gwe-muted', { text: 'No pages found.' })); }
			pages.forEach((p) => list.append(h('a.gwe-menuitem' + (p.id === S.cfg.postId ? '.is-current' : ''), { role: 'menuitem', href: S.cfg.editURL + p.id, 'aria-current': p.id === S.cfg.postId ? 'page' : null },
				S.icon('pages', 16), h('span', { text: p.title || '(no title)' }), p.status !== 'publish' ? h('small.gwe-badge', { text: p.status }) : null)));
		};
		const load = (q) => S.site.loadPages(q).then(fill).catch((e) => { S.empty(list).append(h('p.gwe-muted', { text: e.message })); });
		search.addEventListener('input', S.debounce(() => load(search.value.trim()), 250));
		load('');
		popover(anchor, [h('div.gwe-pop-search', null, search), list, h('hr.gwe-sep'),
			menuItem('Add a new page', null, 'plus', S.cfg.links.newPage), menuItem('All pages', null, 'menu', S.cfg.links.pages)], { role: 'dialog' });
	}

	// --- top bar ------------------------------------------------------------------------------
	function topBar() {
		const logo = h('button.gwe-logo', { type: 'button', 'aria-label': 'Pursae Web Builder menu', 'aria-haspopup': 'menu', 'aria-expanded': 'false', title: 'Pursae Web Builder menu', onClick: (e) => logoMenu(e.currentTarget) },
			h('img', { src: S.cfg.assets + 'images/gopher.png', alt: '', width: 26, height: 26 }), S.icon('down', 14));
		els.pageTitle = h('span.gwe-page-title');
		els.pageStatus = h('span.gwe-badge');
		const page = h('button.gwe-page', { type: 'button', 'aria-haspopup': 'dialog', 'aria-expanded': 'false', title: 'Switch page', onClick: (e) => pageMenu(e.currentTarget) }, S.icon('pages', 16), els.pageTitle, els.pageStatus, S.icon('down', 14));
		els.devices = h('div.gwe-devices', { role: 'radiogroup', 'aria-label': 'Preview device' });
		[['desktop', 'Desktop'], ['tablet', 'Tablet'], ['mobile', 'Mobile']].forEach(([d, label]) => {
			els.devices.appendChild(h('button.gwe-ib.gwe-device', { type: 'button', role: 'radio', 'aria-label': label, title: label + ' view', dataset: { device: d }, onClick: () => S.setDevice(d) }, S.icon(d)));
		});
		els.devices.addEventListener('keydown', (e) => {
			const list = ['desktop', 'tablet', 'mobile'];
			const i = list.indexOf(S.device());
			if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
				e.preventDefault();
				const d = list[(i + (e.key === 'ArrowRight' ? 1 : -1) + 3) % 3];
				S.setDevice(d);
				setTimeout(() => els.devices.querySelector('[data-device=' + d + ']').focus(), 30);
			}
		});
		els.undo = S.iconButton('undo', 'Undo', () => S.run('document/history/undo'), { 'aria-keyshortcuts': 'Control+Z' });
		els.redo = S.iconButton('redo', 'Redo', () => S.run('document/history/redo'), { 'aria-keyshortcuts': 'Control+Shift+Z' });
		els.advanced = h('button.gwe-advanced', { type: 'button', role: 'switch', 'aria-checked': 'false', title: 'Show the full Pursae Web Builder panel', onClick: () => window.GWEStudioToggle.toAdvanced() }, h('span.gwe-switch-track', { 'aria-hidden': 'true' }), h('span', { text: 'Advanced' }));
		const preview = S.iconButton('eye', 'Preview in a new tab', () => {
			const d = S.doc();
			if (d) { S.run('editor/documents/preview', { id: d.id, force: true }); }
		});
		els.draft = h('button.gwe-btn', { type: 'button', text: 'Save draft', onClick: () => save('draft') });
		els.publish = h('button.gwe-btn.is-primary', { type: 'button', text: 'Publish', onClick: () => save('publish') });
		els.top = h('header#gwe-studio-top', { role: 'banner', 'aria-label': 'Pursae Web Builder Studio' },
			h('div.gwe-top-l', null, logo, h('span.gwe-top-sep'), page),
			h('div.gwe-top-c', null, els.devices),
			h('div.gwe-top-r', null, els.undo, els.redo, h('span.gwe-top-sep'), els.advanced, preview, h('span.gwe-top-sep'), els.draft, els.publish));
		return els.top;
	}

	async function save(kind) {
		const d = S.doc();
		if (!d) { return; }
		const btn = kind === 'draft' ? els.draft : els.publish;
		btn.disabled = true;
		btn.classList.add('is-busy');
		try {
			await S.run(kind === 'draft' ? 'document/save/draft' : 'document/save/publish');
			S.toast(kind === 'draft' ? 'Draft saved' : (d.config.status.value === 'publish' ? 'Page published' : 'Saved'));
		} catch (e) {
			S.toast('Saving failed: ' + e.message, 'error');
		}
		btn.classList.remove('is-busy');
		btn.disabled = false;
		refreshTop();
	}

	function historyState() {
		const d = S.doc();
		const items = d && d.history && d.history.getItems ? d.history.getItems() : null;
		if (!items) { return { undo: false, redo: false }; }
		const list = items.models || items;
		// Upstream marks an undone item "applied" (the undo was applied) and a live one "not_applied".
		const real = list.slice(0, -1); // the last item is "Editing Started"
		return { undo: real.some((i) => i.get('status') === 'not_applied'), redo: real.some((i) => i.get('status') === 'applied') };
	}

	function refreshTop() {
		const d = S.doc();
		if (!d || !els.top || S.busy || S.isKitOpen()) { return; }
		const title = (d.container && d.container.settings.get('post_title')) || (d.config.settings && d.config.settings.settings && d.config.settings.settings.post_title) || 'Untitled';
		els.pageTitle.textContent = title;
		const st = (d.config.status && d.config.status.value) || 'draft';
		els.pageStatus.textContent = st === 'publish' ? 'Published' : st.charAt(0).toUpperCase() + st.slice(1);
		els.pageStatus.className = 'gwe-badge is-' + st;
		const dev = S.device();
		els.devices.querySelectorAll('button').forEach((b) => { const on = b.dataset.device === dev; b.setAttribute('aria-checked', on ? 'true' : 'false'); b.tabIndex = on ? 0 : -1; b.classList.toggle('is-on', on); });
		const hs = historyState();
		els.undo.disabled = !hs.undo;
		els.redo.disabled = !hs.redo;
		const changed = !!(d.editor && d.editor.isChanged);
		els.publish.textContent = st === 'publish' ? 'Update' : 'Publish';
		els.publish.disabled = st === 'publish' && !changed;
		els.draft.hidden = st === 'publish';
		els.top.classList.toggle('is-changed', changed);
	}

	// --- left rail ----------------------------------------------------------------------------
	function side() {
		const rail = h('nav.gwe-rail', { role: 'tablist', 'aria-orientation': 'vertical', 'aria-label': 'Studio' });
		TABS.forEach(([id, label, icon]) => {
			rail.appendChild(h('button.gwe-tab', { type: 'button', role: 'tab', id: 'gwe-tab-' + id, 'aria-controls': 'gwe-studio-pane', 'aria-selected': 'false', tabindex: '-1', dataset: { tab: id }, title: label, onClick: () => S.showTab(id) },
				S.icon(icon, 22), h('span', { text: label })));
		});
		rail.addEventListener('keydown', (e) => {
			const list = TABS.map((t) => t[0]);
			const i = list.indexOf(activeTab);
			if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
				e.preventDefault();
				const id = list[(i + (e.key === 'ArrowDown' ? 1 : -1) + list.length) % list.length];
				S.showTab(id);
				rail.querySelector('[data-tab=' + id + ']').focus();
			}
		});
		els.rail = rail;
		els.paneTitle = h('h2.gwe-pane-title');
		els.paneBody = h('div.gwe-pane-body');
		els.pane = h('div#gwe-studio-pane', { role: 'tabpanel', tabindex: '-1' }, h('div.gwe-pane-head', null, els.paneTitle), els.paneBody);
		els.upbar = h('div#gwe-studio-upbar', null,
			h('button.gwe-btn.is-link', { type: 'button', onClick: () => S.closeUpstream() }, S.icon('back', 16), h('span', { text: 'Back to Studio' })),
			h('span.gwe-muted', { text: 'Advanced settings' }));
		els.side = h('div#gwe-studio-side', null, rail, els.pane, els.upbar);
		return els.side;
	}
	S.showTab = function (id) {
		if (document.body.classList.contains('gwe-studio-upstream')) { S.closeUpstream(); }
		activeTab = id;
		els.rail.querySelectorAll('[role=tab]').forEach((b) => { const on = b.dataset.tab === id; b.setAttribute('aria-selected', on ? 'true' : 'false'); b.tabIndex = on ? 0 : -1; b.classList.toggle('is-on', on); });
		els.pane.setAttribute('aria-labelledby', 'gwe-tab-' + id);
		els.paneTitle.textContent = { add: 'Add', edit: 'Edit', layers: 'Layers', site: 'Site' }[id];
		S.empty(els.paneBody);
		if (!panes[id]) {
			panes[id] = id === 'add' ? S.library.pane() : id === 'edit' ? S.inspector.pane() : id === 'layers' ? S.layers.pane() : S.site.pane();
			if (id === 'add') { S.addPane = panes[id]; }
		} else if (panes[id].render) {
			panes[id].render();
		}
		els.paneBody.appendChild(panes[id]);
	};

	// --- the upstream panel overlay -----------------------------------------------------------
	S.openUpstream = function () {
		S.upstreamWanted = true;
		document.body.classList.add('gwe-studio-upstream');
		requestAnimationFrame(() => { const p = document.getElementById('elementor-panel'); if (p && els.upbar) { els.upbar.style.width = p.offsetWidth + 'px'; } });
	};
	S.closeUpstream = function (quiet) {
		S.upstreamWanted = false;
		document.body.classList.remove('gwe-studio-upstream');
		const d = S.doc();
		if (d && d.config.type === 'kit' && !S.busy) { S.run('panel/global/close'); }
		if (!quiet) {
			try { $e.route('panel/elements/categories'); } catch (e) { /* the panel may be closed */ }
			const tab = els.rail && els.rail.querySelector('[aria-selected=true]');
			if (tab) { tab.focus(); }
		}
	};

	// --- show / hide (Advanced mode) ----------------------------------------------------------
	S.show = function () {
		document.body.classList.add('gwe-studio');
		if (!S.mounted) { S.mount(); return; }
		S.emit('mode');
		refreshTop();
		if (els.advanced) { els.advanced.setAttribute('aria-checked', 'false'); }
	};
	S.hide = function () {
		S.closeUpstream(true);
		document.body.classList.remove('gwe-studio');
		closePop();
		if (S.canvas) { S.canvas.closeGallery(); }
		S.emit('mode');
		try { $e.route('panel/elements/categories'); } catch (e) { /* ignore */ }
	};

	// --- wiring -------------------------------------------------------------------------------
	let lastSel = null;
	let lastDevice = null;
	const onCommand = S.debounce((command) => {
		if (S.busy || S.isKitOpen() || !S.doc()) { return; }
		const c = S.selected();
		const id = c ? c.id : null;
		if (id !== lastSel) {
			lastSel = id;
			S.emit('selection', c);
			// Picking something on the canvas opens its settings.
			if (c && /toggle-selection|panel\/editor\/open|elements\/select/.test(command || '') && (activeTab === 'add' || activeTab === 'site') && !S.quietSelect) { S.showTab('edit'); }
		}
		const dev = S.device();
		if (dev !== lastDevice) { lastDevice = dev; S.emit('device', dev); }
		S.emit('changed');
		refreshTop();
	}, 20);

	S.mount = function () {
		if (S.mounted) { return; }
		S.mounted = true;
		document.body.classList.add('gwe-studio');
		document.body.insertBefore(topBar(), document.getElementById('elementor-editor-wrapper'));
		const wrapper = document.getElementById('elementor-editor-wrapper');
		wrapper.insertBefore(side(), wrapper.firstChild);
		S.showTab('add');
		S.ready(() => {
			S.skipUnchangedSettings();
			S.loadGlobals();
			$e.commands.on('run:after', (component, command) => onCommand(command));
			if ($e.routes && $e.routes.on) { $e.routes.on('run:after', (component, route) => onCommand(route)); }
			elementor.channels.editor.on('status:change', () => refreshTop());
			elementor.channels.deviceMode.on('change', () => onCommand('device'));
			elementor.on('document:loaded', () => { lastSel = null; onCommand('document'); });
			S.canvas.init();
			lastDevice = S.device();
			refreshTop();
			document.documentElement.classList.add('gwe-studio-ready');
		});
		document.addEventListener('keydown', (e) => {
			if (!document.body.classList.contains('gwe-studio')) { return; }
			if (e.key === 'Escape' && document.body.classList.contains('gwe-studio-upstream') && !document.querySelector('.dialog-widget:not([style*="display: none"]), .elementor-templates-modal')) {
				S.closeUpstream();
			}
		});
	};

	if (S.cfg.mode !== 'advanced') {
		if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', () => S.mount()); } else { S.mount(); }
	}
})();
