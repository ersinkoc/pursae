/* Golementor Studio — the Add tab: the section library (golementor/v1/library/sections) and the
   basic elements. Click inserts after the selected section (or at the chosen "+ Add section"
   divider); cards can also be dragged onto the canvas (canvas.js handles the drop). */
(function () {
	'use strict';
	const S = window.GWEStudio;
	const { h } = S;
	const L = S.library = {};

	// --- sections -----------------------------------------------------------------------------
	let listing = null;
	L.load = function () {
		if (!listing) {
			listing = S.rest('golementor/v1/library/sections').then((d) => ({ categories: (d && d.categories) || [], items: (d && d.items) || [] }))
				.catch((e) => { listing = null; throw e; });
		}
		return listing;
	};
	/** Fetches a section's element JSON and inserts it into the page at an index (end when null). */
	L.insertSection = async function (id, at) {
		try {
			const d = await S.rest('golementor/v1/library/sections/' + encodeURIComponent(id));
			const content = (d && d.content) || [];
			if (!content.length) { throw new Error('The section is empty.'); }
			const made = S.insert(content, S.root(), at == null ? S.root().children.length : at, 'Section');
			if (made[0]) {
				S.quietSelect = true; // stay in the gallery: several sections are often added in a row
				S.select(made[0]);
				setTimeout(() => { S.quietSelect = false; }, 100);
				setTimeout(() => S.scrollTo(made[0]), 60);
			}
			S.toast('Section added');
			return made;
		} catch (e) {
			S.toast('Could not add the section: ' + e.message, 'error');
			return [];
		}
	};
	/** Where a click in the gallery inserts: after the selected section, else at the end. */
	L.defaultIndex = function () {
		const sel = S.selected();
		const top = sel && S.topLevel(sel);
		return top ? S.indexOf(top) + 1 : S.root().children.length;
	};

	/** The gallery UI (used in the Add tab and in the canvas popover). opts.at: insert index; opts.onDone. */
	L.gallery = function (opts) {
		opts = opts || {};
		const state = { cat: '', q: '' };
		const root = h('div.gwe-gallery-ui');
		const search = h('input.gwe-input.gwe-search', { type: 'search', placeholder: 'Search sections', 'aria-label': 'Search sections' });
		const cats = h('div.gwe-chips', { role: 'tablist', 'aria-label': 'Section categories' });
		const grid = h('div.gwe-cards', { role: 'list', 'aria-label': 'Sections' });
		const status = h('p.gwe-muted', { role: 'status', text: 'Loading sections…' });
		root.append(h('div.gwe-search-wrap', null, S.icon('search', 16), search), cats, status, grid);
		let data = { categories: [], items: [] };
		const render = () => {
			S.empty(cats);
			const chip = (slug, title, count) => h('button.gwe-chip' + (state.cat === slug ? '.is-on' : ''), {
				type: 'button', role: 'tab', 'aria-selected': state.cat === slug ? 'true' : 'false',
				onClick: () => { state.cat = slug; render(); },
			}, title, count != null ? h('span.gwe-count', { text: String(count) }) : null);
			cats.appendChild(chip('', 'All', data.items.length));
			data.categories.forEach((cat) => cats.appendChild(chip(cat.slug, cat.title, cat.count)));
			const q = state.q.toLowerCase();
			const items = data.items.filter((it) => (!state.cat || it.category === state.cat) &&
				(!q || (it.title + ' ' + (it.tags || []).join(' ') + ' ' + it.category).toLowerCase().includes(q)));
			S.empty(grid);
			status.textContent = items.length ? '' : 'No sections match.';
			items.forEach((it) => grid.appendChild(card(it)));
		};
		const card = (it) => {
			const cat = data.categories.find((c) => c.slug === it.category);
			const thumb = it.thumbnail ? h('img', { src: it.thumbnail, alt: '', loading: 'lazy' }) : h('div.gwe-card-ph', null, S.icon('section', 28), h('span', { text: cat ? cat.title : it.category }));
			const b = h('button.gwe-card', {
				type: 'button', role: 'listitem', draggable: 'true', title: 'Insert “' + it.title + '”',
				onClick: async () => {
					b.disabled = true;
					const at = opts.at != null ? (typeof opts.at === 'function' ? opts.at() : opts.at) : L.defaultIndex();
					await L.insertSection(it.id, at);
					b.disabled = false;
					if (opts.onDone) { opts.onDone(); }
				},
				onDragstart: (e) => {
					e.dataTransfer.effectAllowed = 'copy';
					e.dataTransfer.setData('application/x-gwe-studio', JSON.stringify({ kind: 'section', id: it.id }));
					e.dataTransfer.setData('text/plain', it.title);
					S.dragging = { kind: 'section', id: it.id };
				},
				onDragend: () => { S.dragging = null; S.emit('dragend'); },
			}, h('span.gwe-card-thumb', null, thumb), h('span.gwe-card-title', { text: it.title }));
			return b;
		};
		search.addEventListener('input', S.debounce(() => { state.q = search.value.trim(); render(); }, 120));
		L.load().then((d) => { data = d; render(); }).catch((e) => { status.textContent = 'The section library is not available (' + e.message + ').'; });
		root.focusSearch = () => search.focus();
		return root;
	};

	// --- elements -----------------------------------------------------------------------------
	const w = (widgetType, settings) => ({ elType: 'widget', widgetType, settings: settings || {}, elements: [] });
	const col = () => ({ elType: 'container', isInner: true, settings: { flex_direction: 'column', content_width: 'full', width: { unit: '%', size: 50, sizes: [] } }, elements: [] });
	/** The basic elements: [widget type (availability check), title, icon, model]. */
	L.elements = [
		['heading', 'Heading', 'heading', () => w('heading', { title: 'Your heading here', header_size: 'h2' })],
		['text-editor', 'Text', 'text', () => w('text-editor', { editor: '<p>Write something people will want to read. Click here to edit this text.</p>' })],
		['image', 'Image', 'image', () => w('image', {})],
		['button', 'Button', 'button', () => w('button', { text: 'Learn more' })],
		['icon', 'Icon', 'star', () => w('icon', { selected_icon: { value: 'fas fa-star', library: 'fa-solid' } })],
		['icon-list', 'Icon list', 'list', () => w('icon-list', {})],
		['video', 'Video', 'video', () => w('video', {})],
		['divider', 'Divider', 'divider', () => w('divider', {})],
		['spacer', 'Spacer', 'spacer', () => w('spacer', { space: { unit: 'px', size: 40, sizes: [] } })],
		['container', 'Columns', 'columns', () => ({ elType: 'container', settings: { flex_direction: 'row', flex_gap: { unit: 'px', size: 24, column: '24', row: '24', isLinked: true } }, elements: [col(), col()] })],
		['image-gallery', 'Gallery', 'gallery', () => w('image-gallery', { gallery_columns: '3' })],
		['form', 'Form', 'form', () => w('form', {})],
		['google_maps', 'Map', 'map', () => w('google_maps', {})],
		['posts', 'Posts', 'posts', () => w('posts', {})],
	];
	L.available = () => L.elements.filter(([type]) => type === 'container' || (elementor.widgetsCache && elementor.widgetsCache[type]));

	/** Inserts an element: into the selected container, after the selected widget, or in a new section. */
	L.insertElement = function (type) {
		const def = L.elements.find((e) => e[0] === type);
		if (!def) { return null; }
		const model = def[3]();
		const sel = S.selected();
		let parent, at;
		if (sel && S.isContainer(sel)) {
			parent = sel; at = sel.children.length;
		} else if (sel && sel.parent && S.isContainer(sel.parent)) {
			parent = sel.parent; at = S.indexOf(sel) + 1;
		}
		let made;
		if (parent) {
			made = S.insert(model, parent, at, def[1]);
		} else {
			// No target: a new full section holding the element at the end of the page.
			const wrap = type === 'container' ? model : { elType: 'container', settings: { flex_direction: 'column' }, elements: [model] };
			const sec = S.insert(wrap, S.root(), S.root().children.length, def[1])[0];
			made = sec ? (type === 'container' ? [sec] : sec.children.slice(0, 1)) : [];
		}
		if (made && made[0]) {
			S.select(made[0]);
			setTimeout(() => S.scrollTo(made[0]), 60);
			S.emit('inserted', made[0]);
		}
		return made && made[0];
	};
	L.insertElementAt = function (type, parent, at) {
		const def = L.elements.find((e) => e[0] === type);
		if (!def) { return; }
		const made = S.insert(def[3](), parent, at, def[1]);
		if (made[0]) { S.select(made[0]); S.emit('inserted', made[0]); }
	};

	L.elementsGrid = function () {
		const grid = h('div.gwe-elements', { role: 'list', 'aria-label': 'Elements' });
		L.available().forEach(([type, title, icon]) => {
			grid.appendChild(h('button.gwe-element', {
				type: 'button', role: 'listitem', draggable: 'true', title: 'Add ' + title,
				onClick: () => L.insertElement(type),
				onDragstart: (e) => {
					e.dataTransfer.effectAllowed = 'copy';
					e.dataTransfer.setData('application/x-gwe-studio', JSON.stringify({ kind: 'element', type }));
					e.dataTransfer.setData('text/plain', title);
					S.dragging = { kind: 'element', type };
				},
				onDragend: () => { S.dragging = null; S.emit('dragend'); },
			}, S.icon(icon, 22), h('span', { text: title })));
		});
		return grid;
	};

	/** The Add tab. */
	L.pane = function () {
		const pane = h('div.gwe-pane-add');
		const sub = { tab: 'sections' };
		const tabs = h('div.gwe-subtabs', { role: 'tablist', 'aria-label': 'Add' });
		const body = h('div.gwe-subbody');
		const show = (t) => {
			sub.tab = t;
			Array.from(tabs.children).forEach((b) => { const on = b.dataset.tab === t; b.classList.toggle('is-on', on); b.setAttribute('aria-selected', on ? 'true' : 'false'); });
			S.empty(body);
			body.appendChild(t === 'sections' ? L.gallery() : h('div', null, h('p.gwe-muted', { text: 'Click to add to the selected section, or drag onto the page.' }), L.elementsGrid()));
		};
		[['sections', 'Sections'], ['elements', 'Elements']].forEach(([t, text]) => tabs.appendChild(h('button.gwe-subtab', { type: 'button', role: 'tab', dataset: { tab: t }, text, onClick: () => show(t) })));
		pane.append(tabs, body);
		pane.show = show;
		show('sections');
		return pane;
	};
})();
