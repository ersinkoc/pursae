/* Golementor Studio — on the canvas: a small square toolbar on the hovered section (move up/down,
   duplicate, delete, save as template), "+ Add section" dividers above and below it that open the
   section gallery in place, an "Add section" area at the end of the page, and drops from the Add tab. */
(function () {
	'use strict';
	const S = window.GWEStudio;
	const { h } = S;
	const C = S.canvas = {};
	let doc = null;
	let ui = null;
	let hovered = null; // the hovered top-level element (DOM)
	let topIds = new Set();

	const CSS = `
body.gwe-studio .elementor-element.e-parent > .elementor-element-overlay > .elementor-editor-element-settings,
body.gwe-studio .elementor-top-section > .elementor-element-overlay > .elementor-editor-element-settings { display: none !important; }
body.gwe-studio #elementor-add-new-section { display: none !important; }
#gwe-studio-secbar, .gwe-studio-adder, #gwe-studio-dropline { display: none; }
body.gwe-studio #gwe-studio-secbar.is-on { display: flex; }
body.gwe-studio .gwe-studio-adder.is-on, body.gwe-studio #gwe-studio-dropline.is-on { display: flex; }
#gwe-studio-secbar { position: absolute; z-index: 9999; gap: 0; background: #fff; border: 1px solid #dcdcde; border-radius: 2px; box-shadow: 0 1px 3px rgba(0,0,0,.12); font: 13px/1 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
#gwe-studio-secbar .gwe-sb-label { display: flex; align-items: center; padding: 0 8px; color: #50575e; border-right: 1px solid #dcdcde; max-width: 160px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
#gwe-studio-secbar button { all: unset; box-sizing: border-box; width: 30px; height: 30px; display: flex; align-items: center; justify-content: center; color: #1e1e1e; cursor: pointer; }
#gwe-studio-secbar button + button { border-left: 1px solid #f0f0f1; }
#gwe-studio-secbar button:hover { background: #f0f6fc; color: #2271b1; }
#gwe-studio-secbar button:focus-visible, .gwe-studio-adder button:focus-visible, #gwe-studio-addend button:focus-visible { outline: 2px solid #2271b1; outline-offset: 1px; }
#gwe-studio-secbar button[disabled] { opacity: .35; cursor: default; background: none; }
#gwe-studio-secbar svg, .gwe-studio-adder svg, #gwe-studio-addend svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; }
.gwe-studio-adder { position: absolute; z-index: 9998; left: 0; right: 0; height: 0; align-items: center; justify-content: center; pointer-events: none; }
.gwe-studio-adder::before { content: ""; position: absolute; left: 0; right: 0; top: -1px; border-top: 2px solid #2271b1; opacity: .55; }
.gwe-studio-adder button { all: unset; pointer-events: auto; position: relative; display: inline-flex; align-items: center; gap: 4px; padding: 5px 10px; background: #2271b1; color: #fff; border-radius: 2px; font: 600 12px/1 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; cursor: pointer; box-shadow: 0 1px 3px rgba(0,0,0,.2); }
.gwe-studio-adder button:hover { background: #135e96; }
#gwe-studio-dropline { position: absolute; z-index: 9999; height: 0; border-top: 3px solid #2271b1; pointer-events: none; }
#gwe-studio-addend { display: none; }
body.gwe-studio #gwe-studio-addend { display: flex; flex-direction: column; align-items: center; gap: 12px; margin: 24px auto 48px; padding: 32px 16px; max-width: 1140px; border: 1px dashed #a7aaad; border-radius: 2px; background: rgba(255,255,255,.6); font: 13px/1.4 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #50575e; }
#gwe-studio-addend .gwe-ae-row { display: flex; gap: 8px; flex-wrap: wrap; justify-content: center; }
#gwe-studio-addend button { all: unset; display: inline-flex; align-items: center; gap: 6px; padding: 8px 14px; border: 1px solid #2271b1; color: #2271b1; background: #fff; border-radius: 2px; cursor: pointer; font-weight: 600; }
#gwe-studio-addend button.is-primary { background: #2271b1; color: #fff; }
#gwe-studio-addend button:hover { background: #135e96; border-color: #135e96; color: #fff; }
body.gwe-studio.gwe-studio-dragging .elementor-element { pointer-events: auto; }
`;
	const svg = (name) => { const s = S.icon(name, 16); return doc.importNode(s, true); };
	const button = (icon, label, fn) => {
		const b = doc.createElement('button');
		b.type = 'button';
		b.setAttribute('aria-label', label);
		b.title = label;
		b.appendChild(svg(icon));
		b.addEventListener('click', (e) => { e.preventDefault(); e.stopPropagation(); fn(); });
		b.addEventListener('mousedown', (e) => e.stopPropagation());
		return b;
	};

	function build() {
		doc = elementor.$previewContents[0];
		if (!doc || doc.getElementById('gwe-studio-canvas-style')) { return; }
		const style = doc.createElement('style');
		style.id = 'gwe-studio-canvas-style';
		style.textContent = CSS;
		doc.head.appendChild(style);
		ui = {};
		ui.bar = doc.createElement('div');
		ui.bar.id = 'gwe-studio-secbar';
		ui.bar.setAttribute('role', 'toolbar');
		ui.bar.setAttribute('aria-label', 'Section');
		ui.label = doc.createElement('span');
		ui.label.className = 'gwe-sb-label';
		ui.up = button('arrowUp', 'Move section up', () => moveBy(-1));
		ui.down = button('arrowDown', 'Move section down', () => moveBy(1));
		ui.bar.append(ui.label, button('edit', 'Edit section', () => { const c = current(); if (c) { S.select(c); S.showTab('edit'); } }), ui.up, ui.down,
			button('copy', 'Duplicate section', () => { const c = current(); if (c) { S.run('document/elements/duplicate', { container: c }); } }),
			button('save', 'Save section as template', () => { const c = current(); if (c) { $e.route('library/save-template', { model: c.model }); } }),
			button('trash', 'Delete section', () => { const c = current(); if (c) { S.run('document/elements/delete', { container: c }); hide(); } }));
		const adder = (where) => {
			const a = doc.createElement('div');
			a.className = 'gwe-studio-adder';
			const b = doc.createElement('button');
			b.type = 'button';
			b.append(svg('plus'), doc.createTextNode('Add section'));
			b.setAttribute('aria-label', 'Add a section ' + where + ' this one');
			b.addEventListener('click', (e) => {
				e.preventDefault(); e.stopPropagation();
				const c = current();
				const at = c ? S.indexOf(c) + (where === 'below' ? 1 : 0) : S.root().children.length;
				C.openGallery(at, a.getBoundingClientRect().top);
			});
			a.appendChild(b);
			return a;
		};
		ui.above = adder('above');
		ui.below = adder('below');
		ui.line = doc.createElement('div');
		ui.line.id = 'gwe-studio-dropline';
		doc.body.append(ui.bar, ui.above, ui.below, ui.line);

		doc.addEventListener('mousemove', onMove, { passive: true });
		doc.documentElement.addEventListener('mouseleave', () => { if (!S.dragging) { hide(); } });
		const win = doc.defaultView;
		win.addEventListener('scroll', () => place(), { passive: true });
		['dragenter', 'dragover'].forEach((t) => win.addEventListener(t, onDragOver, true));
		win.addEventListener('drop', onDrop, true);
		win.addEventListener('dragleave', (e) => { if (S.dragging && !e.relatedTarget) { ui.line.classList.remove('is-on'); } }, true);
		S.on('dragend', () => { ui.line.classList.remove('is-on'); doc.body.classList.remove('gwe-studio-dragging'); });
		C.sync();
	}

	const current = () => (hovered ? elementor.getContainer(hovered.dataset.id) : null);
	function hide() {
		hovered = null;
		if (!ui) { return; }
		ui.bar.classList.remove('is-on');
		ui.above.classList.remove('is-on');
		ui.below.classList.remove('is-on');
	}
	function topOf(target) {
		let el = target && target.closest ? target.closest('.elementor-element') : null;
		let found = null;
		while (el) {
			if (topIds.has(el.dataset.id)) { found = el; }
			el = el.parentElement ? el.parentElement.closest('.elementor-element') : null;
		}
		return found;
	}
	function onMove(e) {
		if (!ui || S.dragging || e.target.closest('#gwe-studio-secbar,.gwe-studio-adder,#gwe-studio-addend')) { return; }
		const el = topOf(e.target);
		if (el !== hovered) {
			hovered = el;
			place();
		}
	}
	/** Positions the toolbar and dividers on the hovered section. */
	function place() {
		if (!ui) { return; }
		if (!hovered || !hovered.isConnected) { hide(); return; }
		const c = current();
		if (!c) { hide(); return; }
		const win = doc.defaultView;
		const r = hovered.getBoundingClientRect();
		const top = r.top + win.scrollY, bottom = r.bottom + win.scrollY;
		const i = S.indexOf(c);
		ui.label.textContent = S.title(c);
		ui.up.disabled = i <= 0;
		ui.down.disabled = i >= S.root().children.length - 1;
		ui.bar.style.top = Math.max(top + 8, win.scrollY + 8) + 'px';
		ui.bar.style.left = Math.max(8, r.right + win.scrollX - ui.bar.offsetWidth - 8) + 'px';
		ui.bar.classList.add('is-on');
		ui.bar.style.left = Math.max(8, r.right + win.scrollX - ui.bar.offsetWidth - 8) + 'px';
		ui.above.style.top = top + 'px';
		ui.below.style.top = bottom + 'px';
		ui.above.classList.add('is-on');
		ui.below.classList.add('is-on');
	}
	function moveBy(step) {
		const c = current();
		if (!c) { return; }
		const i = S.indexOf(c);
		const at = i + step;
		if (at < 0 || at >= S.root().children.length) { return; }
		S.run('document/elements/move', { container: c, target: S.root(), options: { at } });
		setTimeout(() => { const n = elementor.getContainer(c.id); hovered = n && n.view ? n.view.el : null; place(); if (n) { S.scrollTo(n); } }, 50);
	}

	// --- drops from the Add tab ---------------------------------------------------------------
	let drop = null;
	function dropTarget(e) {
		const d = S.dragging;
		const root = S.root();
		const win = doc.defaultView;
		if (d.kind === 'section' || !e.target.closest) {
			const el = topOf(e.target);
			if (!el) { return { parent: root, at: root.children.length, rect: endRect() }; }
			const c = elementor.getContainer(el.dataset.id);
			const r = el.getBoundingClientRect();
			const after = e.clientY > r.top + r.height / 2;
			return { parent: root, at: S.indexOf(c) + (after ? 1 : 0), rect: { left: r.left, width: r.width, top: (after ? r.bottom : r.top) + win.scrollY } };
		}
		// Elements: into the innermost container under the pointer, at the nearest gap.
		const conEl = e.target.closest('.e-con, .elementor-column, .elementor-section');
		const con = conEl && elementor.getContainer(conEl.dataset.id);
		if (!con || !S.isContainer(con)) { return { parent: null, at: null, rect: endRect() }; }
		const row = /row/.test(getComputedStyle(conEl).flexDirection || '');
		let at = con.children.length;
		let rect = null;
		for (let i = 0; i < con.children.length; i++) {
			const ch = con.children[i].view && con.children[i].view.el;
			if (!ch) { continue; }
			const r = ch.getBoundingClientRect();
			if (row ? e.clientX < r.left + r.width / 2 : e.clientY < r.top + r.height / 2) {
				at = i;
				rect = row ? { left: r.left - 2, width: 4, top: r.top + win.scrollY, height: r.height } : { left: r.left, width: r.width, top: r.top + win.scrollY };
				break;
			}
		}
		if (!rect) {
			const last = con.children.length && con.children[con.children.length - 1].view.el.getBoundingClientRect();
			const cr = conEl.getBoundingClientRect();
			rect = last ? (row ? { left: last.right, width: 4, top: last.top + win.scrollY, height: last.height } : { left: last.left, width: last.width, top: last.bottom + win.scrollY }) : { left: cr.left + 8, width: cr.width - 16, top: cr.top + cr.height / 2 + win.scrollY };
		}
		return { parent: con, at, rect };
	}
	function endRect() {
		const root = S.root();
		const last = root.children.length && root.children[root.children.length - 1].view.el;
		const r = last ? last.getBoundingClientRect() : doc.body.getBoundingClientRect();
		return { left: r.left, width: r.width, top: (last ? r.bottom : r.top + 40) + doc.defaultView.scrollY };
	}
	function onDragOver(e) {
		if (!S.dragging) { return; }
		e.preventDefault();
		e.stopPropagation();
		e.dataTransfer.dropEffect = 'copy';
		doc.body.classList.add('gwe-studio-dragging');
		hide();
		drop = dropTarget(e);
		const r = drop.rect;
		ui.line.style.left = r.left + 'px';
		ui.line.style.width = r.width + 'px';
		ui.line.style.top = r.top + 'px';
		ui.line.style.height = r.height ? r.height + 'px' : '0';
		ui.line.style.borderLeft = r.height ? '3px solid #2271b1' : '';
		ui.line.style.borderTop = r.height ? '0' : '';
		ui.line.classList.add('is-on');
	}
	function onDrop(e) {
		const d = S.dragging;
		if (!d) { return; }
		e.preventDefault();
		e.stopPropagation();
		const t = drop || dropTarget(e);
		S.dragging = null;
		drop = null;
		ui.line.classList.remove('is-on');
		doc.body.classList.remove('gwe-studio-dragging');
		if (d.kind === 'section') {
			S.library.insertSection(d.id, t.at);
		} else if (t.parent) {
			S.library.insertElementAt(d.type, t.parent, t.at);
		} else {
			S.deselect();
			S.library.insertElement(d.type);
		}
	}

	// --- the inline gallery -------------------------------------------------------------------
	let pop = null;
	let returnFocus = null;
	C.openGallery = function (at, y) {
		C.closeGallery();
		returnFocus = document.activeElement;
		const frame = document.getElementById('elementor-preview-iframe');
		const fr = frame ? frame.getBoundingClientRect() : { top: 60, left: 360, width: 900, height: 700 };
		const width = Math.min(760, fr.width - 32);
		const height = Math.min(520, window.innerHeight - 96);
		let top = fr.top + (y || 0) + 12;
		if (top + height > window.innerHeight - 16) { top = Math.max(fr.top + 8, window.innerHeight - height - 16); }
		pop = h('div#gwe-studio-pop', { role: 'dialog', 'aria-modal': 'true', 'aria-label': 'Add a section', style: { top: top + 'px', left: (fr.left + (fr.width - width) / 2) + 'px', width: width + 'px', height: height + 'px' } },
			h('div.gwe-pop-head', null, h('h2', { text: 'Add a section' }),
				h('button.gwe-btn.is-link', { type: 'button', text: 'Blank section', onClick: () => { const made = S.insert({ elType: 'container', settings: { flex_direction: 'column' }, elements: [] }, S.root(), at, 'Section'); C.closeGallery(); if (made[0]) { S.select(made[0]); S.showTab('add'); } } }),
				S.iconButton('close', 'Close', () => C.closeGallery())),
			S.library.gallery({ at, onDone: () => C.closeGallery() }));
		pop.addEventListener('keydown', (e) => {
			if (e.key === 'Escape') { e.stopPropagation(); C.closeGallery(); }
			if (e.key === 'Tab') { // keep focus in the dialog
				const f = Array.from(pop.querySelectorAll('button,input,[tabindex="0"]')).filter((n) => !n.disabled && n.offsetParent);
				if (!f.length) { return; }
				if (e.shiftKey && document.activeElement === f[0]) { e.preventDefault(); f[f.length - 1].focus(); }
				else if (!e.shiftKey && document.activeElement === f[f.length - 1]) { e.preventDefault(); f[0].focus(); }
			}
		});
		document.body.appendChild(pop);
		const scrim = h('div#gwe-studio-scrim', { onClick: () => C.closeGallery() });
		document.body.insertBefore(scrim, pop);
		setTimeout(() => { const s = pop && pop.querySelector('input'); if (s) { s.focus(); } }, 30);
	};
	C.closeGallery = function () {
		if (!pop) { return; }
		pop.remove();
		const scrim = document.getElementById('gwe-studio-scrim');
		if (scrim) { scrim.remove(); }
		pop = null;
		if (returnFocus && returnFocus.focus) { try { returnFocus.focus(); } catch (e) { /* gone */ } }
	};

	/** Re-reads the page structure: top-level ids, the end-of-page add area, the studio class. */
	C.sync = function () {
		if (!doc || !doc.body || S.busy || S.isKitOpen() || !S.doc()) { return; }
		doc.body.classList.toggle('gwe-studio', document.body.classList.contains('gwe-studio'));
		const root = S.root();
		if (!root || !root.children) { setTimeout(C.sync, 200); return; }
		topIds = new Set(root.children.map((c) => c.id));
		if (hovered && !hovered.isConnected) {
			const id = hovered.dataset.id;
			const c = elementor.getContainer(id);
			hovered = c && c.view ? c.view.el : null;
		}
		place();
		const anchor = doc.getElementById('elementor-add-new-section');
		let end = doc.getElementById('gwe-studio-addend');
		if (anchor && (!end || end.previousElementSibling !== anchor)) {
			if (!end) {
				end = doc.createElement('div');
				end.id = 'gwe-studio-addend';
				const p = doc.createElement('div');
				p.textContent = 'Add a section to this page: pick a ready-made one, or start blank.';
				const row = doc.createElement('div');
				row.className = 'gwe-ae-row';
				const browse = doc.createElement('button');
				browse.type = 'button';
				browse.className = 'is-primary';
				browse.append(svg('plus'), doc.createTextNode('Browse sections'));
				browse.addEventListener('click', () => C.openGallery(S.root().children.length, end.getBoundingClientRect().top));
				const blank = doc.createElement('button');
				blank.type = 'button';
				blank.append(svg('container'), doc.createTextNode('Blank section'));
				blank.addEventListener('click', () => {
					const made = S.insert({ elType: 'container', settings: { flex_direction: 'column' }, elements: [] }, S.root(), S.root().children.length, 'Section');
					if (made[0]) { S.select(made[0]); S.showTab('add'); S.addPane && S.addPane.show('elements'); }
				});
				row.append(browse, blank);
				end.append(p, row);
			}
			anchor.after(end);
		}
	};

	C.init = function () {
		build();
		elementor.on('preview:loaded', () => { doc = null; ui = null; hovered = null; build(); });
		S.on('changed', S.debounce(() => C.sync(), 30));
		S.on('mode', () => C.sync());
	};
})();
