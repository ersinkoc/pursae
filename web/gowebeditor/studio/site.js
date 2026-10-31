/* Pursae Web Builder Studio — the Site tab: the kit's global colors and fonts, site name and logo (edited
   in the kit document through the same commands Site Settings uses, then saved), the site's theme
   parts (header, footer, …), templates and pages. */
(function () {
	'use strict';
	const S = window.GWEStudio;
	const { h } = S;
	const Site = S.site = {};

	// --- the kit ------------------------------------------------------------------------------
	let queue = Promise.resolve();
	const wait = (ms) => new Promise((r) => setTimeout(r, ms));
	async function until(test, ms) {
		const end = Date.now() + (ms || 8000);
		while (!test()) {
			if (Date.now() > end) { throw new Error('timed out'); }
			await wait(50);
		}
	}
	/** Opens the kit (Site Settings, hidden), runs fn(kitDocument), saves it and returns to the page. */
	Site.editKit = function (label, fn) {
		const job = queue.then(async () => {
			const kitId = elementor.config.kit_id;
			const selected = S.selected();
			const selId = selected && selected.id;
			S.busy = true;
			try {
				await $e.run('panel/global/open');
				await until(() => { const d = elementor.documents.get(kitId); return d && d.container && elementor.documents.getCurrent() === d; });
				const kit = elementor.documents.get(kitId);
				fn(kit);
				await $e.run('document/save/update', { document: kit });
				await $e.run('panel/global/close');
				await until(() => { const d = elementor.documents.getCurrent(); return d && d.id !== kitId; });
				S.toast(label + ' saved');
			} catch (e) {
				console.error('[studio] kit', e);
				S.toast('Could not save ' + label.toLowerCase() + ': ' + e.message, 'error');
				try { if (elementor.documents.getCurrent() && elementor.documents.getCurrent().id === elementor.config.kit_id) { await $e.run('panel/global/close'); } } catch (_) { /* ignore */ }
			} finally {
				S.busy = false;
			}
			if (selId) { const c = elementor.getContainer(selId); if (c) { S.select(c); } }
			if (S.openUpstream && !S.upstreamWanted) { S.closeUpstream(true); }
			await S.loadGlobals(true);
			S.emit('changed');
		});
		queue = job.catch(() => {});
		return job;
	};
	const kitSettings = () => {
		const d = elementor.documents.get(elementor.config.kit_id);
		return d && d.container ? d.container.settings : null;
	};
	const repeaterItem = (kit, name, id) => kit.container.repeaters[name].children.find((ch) => ch.settings.get('_id') === id);

	// --- UI -----------------------------------------------------------------------------------
	const section = (title, ...kids) => h('section.gwe-card-sec', { 'aria-label': title }, h('h3.gwe-sec-title', { text: title }), kids);
	const linkRow = (text, href, icon, newTab) => h('a.gwe-linkrow', { href, target: newTab ? '_blank' : null, rel: newTab ? 'noopener' : null }, S.icon(icon || 'right', 16), h('span', { text }), newTab ? S.icon('external', 14) : null);

	function identity() {
		const box = h('div');
		const initial = kitSettings();
		const name = h('input.gwe-input', { id: 'gwe-site-name', type: 'text', value: (initial && initial.get('site_name')) || S.cfg.siteName || '' });
		const sendName = S.debounce(() => Site.editKit('Site name', (kit) => S.set(kit.container, { site_name: name.value })), 900);
		name.addEventListener('input', sendName);
		const logoBox = h('div.gwe-media');
		const renderLogo = (logo) => {
			S.empty(logoBox);
			logoBox.append(logo && logo.url ? h('img.gwe-media-thumb.is-logo', { src: logo.url, alt: 'Site logo' }) : h('div.gwe-media-empty', null, S.icon('image', 24), h('span', { text: 'No logo' })),
				h('div.gwe-media-actions', null,
					h('button.gwe-btn', { type: 'button', text: logo && logo.url ? 'Replace logo' : 'Choose logo', onClick: () => S.pickMedia({ title: 'Choose logo' }, (a) => { const v = { id: a.id, url: a.url }; renderLogo(v); Site.editKit('Logo', (kit) => S.set(kit.container, { site_logo: v })); }) }),
					logo && logo.url ? h('button.gwe-btn.is-link', { type: 'button', text: 'Remove', onClick: () => { renderLogo(null); Site.editKit('Logo', (kit) => S.set(kit.container, { site_logo: { id: '', url: '' } })); } }) : null));
		};
		renderLogo(initial && initial.get('site_logo'));
		box.append(h('div.gwe-f', null, h('div.gwe-f-label', null, h('label', { for: 'gwe-site-name', text: 'Site name' })), name),
			h('div.gwe-f', null, h('div.gwe-f-label', null, h('span', { text: 'Logo' })), logoBox));
		return box;
	}

	function colors() {
		const box = h('div.gwe-globals', { role: 'list' });
		const list = S.ordered(S.globals.colors);
		if (!list.length) { box.append(h('p.gwe-muted', { text: 'Loading colors…' })); }
		list.forEach((g) => {
			const input = h('input.gwe-color-input', { type: 'color', value: /^#[0-9a-f]{6}$/i.test(g.value) ? g.value : '#000000', 'aria-label': g.title + ' color' });
			const hex = h('span.gwe-hex-label', { text: g.value });
			const send = S.debounce(() => {
				Site.editKit('Colors', (kit) => {
					const item = repeaterItem(kit, 'system_colors', g.id) || repeaterItem(kit, 'custom_colors', g.id);
					if (item) { S.set(item, { color: input.value.toUpperCase() }); }
				});
			}, 700);
			input.addEventListener('input', () => { hex.textContent = input.value.toUpperCase(); send(); });
			box.append(h('div.gwe-global-row', { role: 'listitem' }, h('label.gwe-swatch.gwe-swatch-custom.is-big', { style: { background: g.value }, title: 'Change ' + g.title }, input), h('span.gwe-global-name', { text: g.title }), hex));
		});
		const add = h('button.gwe-btn.is-link', {
			type: 'button', text: '+ Add a color',
			onClick: () => Site.editKit('Colors', (kit) => S.run('document/repeater/insert', { container: kit.container, name: 'custom_colors', model: { _id: elementorCommon.helpers.getUniqueId(), title: 'New color', color: '#2271B1' } })),
		});
		return h('div', null, box, add);
	}

	let fontList = null;
	function fonts() {
		const box = h('div.gwe-globals', { role: 'list' });
		if (!fontList) {
			fontList = h('datalist#gwe-studio-fonts', null, Object.keys((elementor.config.controls.font || {}).options || {}).map((f) => h('option', { value: f })));
		}
		box.append(fontList);
		const list = S.ordered(S.globals.typography);
		list.forEach((g) => {
			const v = g.value || {};
			const id = 'gwe-font-' + g.id;
			const family = h('input.gwe-input', { id, type: 'text', list: 'gwe-studio-fonts', value: v.typography_font_family || '', placeholder: 'Default font' });
			const weight = h('select.gwe-input.gwe-weight', { 'aria-label': g.title + ' weight' }, [['', 'Default'], ['300', 'Light'], ['400', 'Regular'], ['500', 'Medium'], ['600', 'Semi-bold'], ['700', 'Bold'], ['800', 'Extra-bold']].map(([val, text]) => h('option', { value: val, text })));
			weight.value = String(v.typography_font_weight || '');
			const send = () => Site.editKit('Fonts', (kit) => {
				const item = repeaterItem(kit, 'system_typography', g.id) || repeaterItem(kit, 'custom_typography', g.id);
				if (item) { S.set(item, { typography_typography: 'custom', typography_font_family: family.value.trim(), typography_font_weight: weight.value }); }
			});
			family.addEventListener('change', send);
			weight.addEventListener('change', send);
			box.append(h('div.gwe-global-row.is-font', { role: 'listitem' },
				h('label.gwe-global-name', { for: id, text: g.title, style: { fontFamily: v.typography_font_family ? '"' + v.typography_font_family + '", sans-serif' : null } }), family, weight));
		});
		return box;
	}

	function parts() {
		const cfg = S.cfg;
		const box = h('div');
		const have = {};
		(cfg.parts || []).forEach((p) => {
			have[p.type] = true;
			box.append(h('a.gwe-linkrow', { href: p.edit }, S.icon(p.type === 'header' ? 'arrowUp' : p.type === 'footer' ? 'arrowDown' : 'pages', 16),
				h('span', null, p.title, h('small.gwe-muted', { text: ' ' + p.label + (p.status !== 'publish' ? ' · ' + p.status : '') }))));
		});
		if (!have.header) { box.append(linkRow('Create a header', cfg.links.newHeader, 'plus')); }
		if (!have.footer) { box.append(linkRow('Create a footer', cfg.links.newFooter, 'plus')); }
		box.append(linkRow('Theme Builder', cfg.links.themeBuilder, 'layers', true), linkRow('Saved templates', cfg.links.templates, 'save', true));
		return box;
	}

	function pages() {
		const box = h('div', null, h('p.gwe-muted', { text: 'Loading pages…' }));
		Site.loadPages().then((list) => {
			S.empty(box);
			list.slice(0, 12).forEach((p) => box.append(h('a.gwe-linkrow' + (p.id === S.cfg.postId ? '.is-current' : ''), { href: S.cfg.editURL + p.id, 'aria-current': p.id === S.cfg.postId ? 'page' : null }, S.icon('pages', 16),
				h('span', null, p.title || '(no title)', p.status !== 'publish' ? h('small.gwe-muted', { text: ' · ' + p.status }) : null))));
			box.append(linkRow('Add a page', S.cfg.links.newPage, 'plus'), linkRow('All pages', S.cfg.links.pages, 'menu', true));
		}).catch((e) => { S.empty(box).append(h('p.gwe-muted', { text: 'Could not load pages: ' + e.message })); });
		return box;
	}
	/** The site's pages (id, title, status), alphabetical. */
	Site.loadPages = async function (search) {
		const q = 'wp/v2/pages?per_page=100&context=edit&_fields=id,title,status,link&orderby=title&order=asc&status=publish,draft,pending,private,future' + (search ? '&search=' + encodeURIComponent(search) : '');
		const list = await S.rest(q);
		return (list || []).map((p) => ({ id: p.id, title: (p.title && (p.title.raw != null ? p.title.raw : p.title.rendered)) || '', status: p.status, link: p.link }));
	};

	Site.pane = function () {
		const pane = h('div.gwe-pane-site');
		const render = () => {
			S.empty(pane);
			if (S.cfg.canEditKit) {
				pane.append(section('Site identity', identity()), section('Colors', colors()), section('Fonts', fonts()));
			}
			pane.append(section('Header, footer and templates', parts()), section('Pages', pages()),
				section('This page', h('div', null,
					h('button.gwe-linkrow', { type: 'button', onClick: () => { S.openUpstream(); $e.route('panel/page-settings/settings'); } }, S.icon('gear', 16), h('span', { text: 'Page settings' })),
					h('button.gwe-linkrow', { type: 'button', onClick: () => { S.openUpstream(); $e.route('panel/history/revisions'); } }, S.icon('history', 16), h('span', { text: 'History and revisions' })),
					S.cfg.canEditKit ? h('button.gwe-linkrow', { type: 'button', onClick: () => { S.openUpstream(); $e.run('panel/global/open'); } }, S.icon('brush', 16), h('span', { text: 'All site settings' })) : null)));
		};
		pane.render = render;
		S.on('globals', () => { if (pane.isConnected && !pane.contains(document.activeElement)) { render(); } });
		render();
		return pane;
	};
})();
