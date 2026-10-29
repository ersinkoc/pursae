/* Golementor Studio — the Layers tab: an outline of the page. Select, rename (F2 / double-click),
   reorder by drag or Alt+Arrow, hide in the editor, duplicate and delete. A tree with arrow-key
   navigation (WAI-ARIA tree pattern). */
(function () {
	'use strict';
	const S = window.GWEStudio;
	const { h } = S;
	const Ly = S.layers = {};
	const collapsed = new Set();

	const iconFor = (c) => {
		const t = S.type(c);
		return { container: c.model.get('isInner') ? 'container' : 'section', section: 'section', heading: 'heading', 'text-editor': 'text', image: 'image', button: 'button', icon: 'star', 'icon-list': 'list', video: 'video', divider: 'divider', spacer: 'spacer', 'image-gallery': 'gallery', form: 'form', google_maps: 'map', posts: 'posts' }[t] || 'container';
	};

	Ly.pane = function () {
		const pane = h('div.gwe-pane-layers');
		const tree = h('ul.gwe-tree', { role: 'tree', 'aria-label': 'Page layers' });
		const hint = h('p.gwe-muted.gwe-pad', { text: 'Drag to reorder. Keyboard: arrows to move, Enter to select, F2 to rename, Alt+↑/↓ to move, Delete to remove.' });
		pane.append(tree, hint);
		let focusId = null;
		let drag = null;

		const row = (c, depth) => {
			const kids = c.children || [];
			const hasKids = S.isContainer(c) && kids.length > 0;
			const open = !collapsed.has(c.id);
			const sel = S.selected();
			const hidden = c.model.getVisibility ? !!c.model.getVisibility() : false; // getVisibility() is the editor-only "hidden" flag
			const li = h('li.gwe-node', { role: 'treeitem', 'aria-level': depth + 1, 'aria-selected': sel && sel.id === c.id ? 'true' : 'false', 'aria-expanded': hasKids ? String(open) : null, tabindex: '-1', dataset: { id: c.id } });
			const name = h('span.gwe-node-name', { text: S.title(c) });
			const line = h('div.gwe-node-row' + (sel && sel.id === c.id ? '.is-selected' : '') + (hidden ? '.is-hidden' : ''), { style: { paddingLeft: (8 + depth * 14) + 'px' }, draggable: 'true' },
				hasKids ? h('button.gwe-twisty', { type: 'button', tabindex: '-1', 'aria-hidden': 'true', onClick: (e) => { e.stopPropagation(); toggle(c); } }, S.icon(open ? 'down' : 'right', 14)) : h('span.gwe-twisty'),
				S.icon(iconFor(c), 16), name,
				h('span.gwe-node-actions', null,
					S.iconButton(hidden ? 'eyeOff' : 'eye', hidden ? 'Show in editor' : 'Hide in editor', (e) => { e.stopPropagation(); c.model.toggleVisibility(); render(); }, { tabindex: '-1' }),
					S.iconButton('copy', 'Duplicate', (e) => { e.stopPropagation(); S.run('document/elements/duplicate', { container: c }); }, { tabindex: '-1' }),
					S.iconButton('trash', 'Delete', (e) => { e.stopPropagation(); S.run('document/elements/delete', { container: c }); }, { tabindex: '-1' })));
			line.addEventListener('click', () => { focusId = c.id; S.select(c); S.scrollTo(c); });
			line.addEventListener('dblclick', () => rename(c, name));
			line.addEventListener('dragstart', (e) => { drag = c; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', S.title(c)); });
			line.addEventListener('dragend', () => { drag = null; clearMarks(); });
			line.addEventListener('dragover', (e) => {
				if (!drag || drag === c || isAncestor(drag, c)) { return; }
				e.preventDefault();
				clearMarks();
				line.classList.add('drop-' + zone(e, line, c));
			});
			line.addEventListener('dragleave', () => line.classList.remove('drop-before', 'drop-after', 'drop-inside'));
			line.addEventListener('drop', (e) => {
				if (!drag || drag === c || isAncestor(drag, c)) { return; }
				e.preventDefault();
				const z = zone(e, line, c);
				const src = drag;
				drag = null;
				clearMarks();
				if (z === 'inside') { move(src, c, c.children.length); return; }
				const at = S.indexOf(c) + (z === 'after' ? 1 : 0);
				move(src, c.parent, at);
			});
			li.appendChild(line);
			if (hasKids && open) {
				const ul = h('ul', { role: 'group' });
				kids.forEach((k) => ul.appendChild(row(k, depth + 1)));
				li.appendChild(ul);
			}
			return li;
		};
		const zone = (e, line, c) => {
			const r = line.getBoundingClientRect();
			const y = (e.clientY - r.top) / r.height;
			if (S.isContainer(c) && y > 0.3 && y < 0.7) { return 'inside'; }
			return y < 0.5 ? 'before' : 'after';
		};
		const clearMarks = () => tree.querySelectorAll('.drop-before,.drop-after,.drop-inside').forEach((n) => n.classList.remove('drop-before', 'drop-after', 'drop-inside'));
		const isAncestor = (a, b) => { for (let p = b.parent; p; p = p.parent) { if (p === a || p.id === a.id) { return true; } } return false; };
		/** Moves an element with document/elements/move (index adjusted for a move within the same parent). */
		const move = (src, target, at) => {
			if (!target) { return; }
			if (src.parent && src.parent.id === target.id && S.indexOf(src) < at) { at -= 1; }
			if (target.id === S.root().id && src.model.get('elType') === 'widget') {
				S.toast('Elements go inside a section.', 'error');
				return;
			}
			S.run('document/elements/move', { container: src, target, options: { at } });
			focusId = src.id;
		};
		const toggle = (c) => { if (collapsed.has(c.id)) { collapsed.delete(c.id); } else { collapsed.add(c.id); } render(); };
		const rename = (c, nameEl) => {
			const input = h('input.gwe-input.gwe-rename', { type: 'text', value: S.title(c), 'aria-label': 'Layer name' });
			nameEl.replaceWith(input);
			input.focus();
			input.select();
			let done = false;
			const finish = (save) => {
				if (done) { return; }
				done = true;
				const v = input.value.trim();
				if (save && v !== S.title(c)) { S.set(c, { _title: v }); }
				focusId = c.id;
				render();
			};
			input.addEventListener('keydown', (e) => { e.stopPropagation(); if (e.key === 'Enter') { finish(true); } if (e.key === 'Escape') { finish(false); } });
			input.addEventListener('blur', () => finish(true));
		};

		tree.addEventListener('keydown', (e) => {
			if (e.target.matches('input')) { return; }
			const items = Array.from(tree.querySelectorAll('[role=treeitem]'));
			const li = e.target.closest('[role=treeitem]');
			const i = items.indexOf(li);
			if (i < 0) { return; }
			const c = elementor.getContainer(li.dataset.id);
			const go = (n) => { if (n) { focusId = n.dataset.id; items.forEach((x) => x.setAttribute('tabindex', '-1')); n.setAttribute('tabindex', '0'); n.focus(); } };
			switch (e.key) {
				case 'ArrowDown':
					e.preventDefault();
					if (e.altKey && c) { const p = c.parent; const k = S.indexOf(c); if (k < p.children.length - 1) { move(c, p, k + 2); } } else { go(items[i + 1]); }
					break;
				case 'ArrowUp':
					e.preventDefault();
					if (e.altKey && c) { const k = S.indexOf(c); if (k > 0) { move(c, c.parent, k - 1); } } else { go(items[i - 1]); }
					break;
				case 'ArrowRight': e.preventDefault(); if (c && collapsed.has(c.id)) { toggle(c); } else { go(items[i + 1]); } break;
				case 'ArrowLeft': e.preventDefault(); if (c && S.isContainer(c) && c.children.length && !collapsed.has(c.id)) { toggle(c); } else { const p = li.parentElement.closest('[role=treeitem]'); go(p); } break;
				case 'Enter': case ' ': e.preventDefault(); if (c) { S.select(c); S.scrollTo(c); } break;
				case 'F2': e.preventDefault(); if (c) { rename(c, li.querySelector('.gwe-node-name')); } break;
				case 'Delete': case 'Backspace': e.preventDefault(); if (c) { go(items[i + 1] || items[i - 1]); S.run('document/elements/delete', { container: c }); } break;
				default:
			}
		});

		const render = () => {
			if (S.isKitOpen()) { return; }
			const hadFocus = tree.contains(document.activeElement);
			S.empty(tree);
			const root = S.root();
			if (!root.children.length) {
				tree.appendChild(h('li.gwe-muted.gwe-pad', { role: 'none', text: 'The page is empty. Add a section from the Add tab.' }));
				return;
			}
			root.children.forEach((c) => tree.appendChild(row(c, 0)));
			const sel = S.selected();
			const target = (focusId && tree.querySelector('[data-id="' + focusId + '"]')) || (sel && tree.querySelector('[data-id="' + sel.id + '"]')) || tree.querySelector('[role=treeitem]');
			if (target) {
				target.setAttribute('tabindex', '0');
				if (hadFocus) { target.focus(); }
			}
		};
		pane.render = render;
		S.on('changed', S.debounce(() => { if (pane.isConnected) { render(); } }, 80));
		S.on('selection', () => { if (pane.isConnected) { render(); } });
		render();
		return pane;
	};
})();
