/* Pursae Web Builder Studio — the Edit tab: a few plain-language fields per element type (content first,
   then style), and "Advanced settings" for everything else in the upstream panel. */
(function () {
	'use strict';
	const S = window.GWEStudio;
	const { h } = S;
	const I = S.inspector = {};

	const px = (n) => ({ unit: 'px', size: n, sizes: [] });
	const pct = (n) => ({ unit: '%', size: n, sizes: [] });
	const box = (v, side) => ({ unit: 'px', top: String(v), right: String(side == null ? v : side), bottom: String(v), left: String(side == null ? v : side), isLinked: side == null || side === v });
	const sizeMatch = (v, p) => v && p && Number(v.size) === Number(p.size) && (v.unit || 'px') === p.unit;
	const boxMatch = (v, p) => v && p && String(v.top) === p.top && String(v.left) === p.left;

	const ALIGN = [['start', 'Left', 'alignLeft'], ['center', 'Center', 'alignCenter'], ['end', 'Right', 'alignRight'], ['justify', 'Justify', 'alignJustify']];
	const ALIGN3 = ALIGN.slice(0, 3);
	const ALIGN_LR = [['left', 'Left', 'alignLeft'], ['center', 'Center', 'alignCenter'], ['right', 'Right', 'alignRight']];
	const ROUNDED = (key) => ({ key, label: 'Rounded corners', type: 'presets', responsive: true, match: boxMatch, options: [['0', 'None', box(0)], ['s', 'S', box(4)], ['m', 'M', box(12)], ['l', 'L', box(24)], ['full', 'Round', box(999)]] });

	/** Text size presets write the typography group (and unlink a global font preset). */
	const SIZE = (sizes) => ({
		key: 'typography_font_size', label: 'Size', type: 'presets', responsive: true, match: sizeMatch,
		options: [['s', 'S', px(sizes[0])], ['m', 'M', px(sizes[1])], ['l', 'L', px(sizes[2])], ['xl', 'XL', px(sizes[3])]],
		write(c, value, key) {
			S.batch('Size', c, () => {
				if (S.globalOf(c, 'typography_typography')) { S.setGlobal(c, 'typography_typography', ''); }
				const set = { [key]: value };
				if (S.get(c, 'typography_typography') !== 'custom') { set.typography_typography = 'custom'; }
				S.set(c, set);
			});
		},
	});

	const BUTTON_STYLES = {
		primary: { bg: 'primary', fg: '#FFFFFF', border: '' },
		secondary: { bg: 'secondary', fg: '#FFFFFF', border: '' },
		outline: { bg: '', fg: 'primary', border: 'primary' },
		text: { bg: '', fg: 'primary', border: '' },
	};
	function buttonStyle(c, id) {
		const st = BUTTON_STYLES[id];
		S.batch('Button style', c, () => {
			const g = (key, v) => { S.setGlobal(c, key, v && !v.startsWith('#') ? 'globals/colors?id=' + v : ''); };
			g('background_color', st.bg);
			g('button_text_color', st.fg);
			g('border_color', st.border);
			S.set(c, {
				background_background: 'classic',
				background_color: st.bg ? '' : 'transparent',
				button_text_color: st.fg.startsWith('#') ? st.fg : '',
				border_border: st.border ? 'solid' : '',
				border_width: st.border ? box(2) : { unit: 'px', top: '', right: '', bottom: '', left: '', isLinked: true },
			});
		});
	}
	/** Which style preset the button's settings match ('' when customised). */
	function buttonStyleOf(c) {
		const gid = (key) => (S.globalOf(c, key).match(/id=([\w-]+)/) || [])[1] || '';
		const bg = gid('background_color'), border = S.get(c, 'border_border');
		if (bg === 'primary' && !border) { return 'primary'; }
		if (bg === 'secondary' && !border) { return 'secondary'; }
		if (S.get(c, 'background_color') === 'transparent') { return border === 'solid' ? 'outline' : 'text'; }
		return '';
	}

	const VISIBILITY = [
		{ key: 'hide_desktop', label: 'Hide on desktop', type: 'switch', on: 'hidden-desktop' },
		{ key: 'hide_tablet', label: 'Hide on tablet', type: 'switch', on: 'hidden-tablet' },
		{ key: 'hide_mobile', label: 'Hide on mobile', type: 'switch', on: 'hidden-mobile' },
	];

	/** Columns preset: a row of N inner containers (missing ones are added, none are removed). */
	function columns(c, n) {
		S.batch('Columns', c, () => {
			const inner = c.children.filter((k) => k.model.get('elType') === 'container');
			S.set(c, { flex_direction: 'row', flex_wrap: 'wrap' });
			if (!inner.length && c.children.length) {
				// Widgets directly in the section: wrap nothing, just add the columns after them.
			}
			for (let i = inner.length; i < n; i++) {
				S.insert({ elType: 'container', isInner: true, settings: { flex_direction: 'column', content_width: 'full' }, elements: [] }, c, c.children.length, 'Column');
			}
			const now = c.children.filter((k) => k.model.get('elType') === 'container');
			const gap = Number((S.get(c, 'flex_gap') || {}).column) || 20;
			now.forEach((k, i) => {
				S.set(k, { width: i < n ? { unit: 'custom', size: 'calc(' + (100 / n).toFixed(3) + '% - ' + Math.round(gap * (n - 1) / n) + 'px)', sizes: [] } : pct(100), width_mobile: pct(100) });
			});
			S.set(c, { flex_direction_mobile: 'column' });
		});
	}

	const PAD = [['0', 'None', box(0)], ['s', 'S', box(16, 16)], ['m', 'M', box(40, 24)], ['l', 'L', box(80, 24)], ['xl', 'XL', box(120, 24)]];
	const GAP = (n) => ({ unit: 'px', size: n, column: String(n), row: String(n), isLinked: true });

	/** The schemas: content fields, style fields. A key may be a function of the element. */
	I.schemas = {
		heading: {
			content: [
				{ key: 'title', label: 'Text', type: 'textarea' },
				{ key: 'header_size', label: 'Level', type: 'choose', options: [['h1', 'H1'], ['h2', 'H2'], ['h3', 'H3'], ['h4', 'H4'], ['p', 'P']] },
				{ key: 'link', label: 'Link', type: 'link' },
			],
			style: [
				SIZE([20, 28, 40, 56]),
				{ key: 'align', label: 'Alignment', type: 'choose', responsive: true, options: ALIGN },
				{ key: 'title_color', label: 'Color', type: 'color' },
			],
		},
		'text-editor': {
			content: [{ key: 'editor', label: 'Text', type: 'richtext' }],
			style: [
				SIZE([14, 16, 18, 22]),
				{ key: 'align', label: 'Alignment', type: 'choose', responsive: true, options: ALIGN },
				{ key: 'text_color', label: 'Color', type: 'color' },
			],
		},
		image: {
			content: [
				{ key: 'image', label: 'Image', type: 'media' },
				{ key: 'image_size', label: 'Image size', type: 'select', options: [['thumbnail', 'Thumbnail'], ['medium', 'Medium'], ['large', 'Large'], ['full', 'Full size']] },
				{
					key: 'link', label: 'Link', type: 'link',
					write(c, v, key) { S.set(c, { [key]: v, link_to: v && v.url ? 'custom' : 'none' }); },
				},
			],
			style: [
				{ key: 'width', label: 'Width', type: 'presets', responsive: true, match: sizeMatch, options: [['25', '25%', pct(25)], ['50', '50%', pct(50)], ['75', '75%', pct(75)], ['100', 'Full', pct(100)]] },
				{ key: 'align', label: 'Alignment', type: 'choose', responsive: true, options: ALIGN3 },
				ROUNDED('image_border_radius'),
			],
		},
		button: {
			content: [
				{ key: 'text', label: 'Text', type: 'text' },
				{ key: 'link', label: 'Link', type: 'link' },
			],
			style: [
				{
					key: '_gwe_style', label: 'Style', type: 'choose', options: [['primary', 'Primary'], ['secondary', 'Secondary'], ['outline', 'Outline'], ['text', 'Text']],
					read: buttonStyleOf, write: (c, v) => { if (v) { buttonStyle(c, v); } }, reset: false,
				},
				{ key: 'size', label: 'Size', type: 'choose', options: [['xs', 'XS'], ['sm', 'S'], ['md', 'M'], ['lg', 'L'], ['xl', 'XL']] },
				{ key: 'align', label: 'Alignment', type: 'choose', responsive: true, options: ALIGN_LR.concat([['justify', 'Full width', 'alignJustify']]) },
				{ key: 'background_color', label: 'Background', type: 'color', write: (c, v, key) => S.set(c, { background_background: 'classic', [key]: v }) },
				{ key: 'button_text_color', label: 'Text color', type: 'color' },
				ROUNDED('border_radius'),
			],
		},
		icon: {
			content: [
				{
					key: 'selected_icon', label: 'Icon', type: 'select',
					options: [['fas fa-star', 'Star'], ['fas fa-check', 'Check'], ['fas fa-heart', 'Heart'], ['fas fa-bolt', 'Bolt'], ['fas fa-phone', 'Phone'], ['fas fa-envelope', 'Envelope'], ['fas fa-map-marker-alt', 'Location'], ['fas fa-clock', 'Clock'], ['fas fa-user', 'User'], ['fas fa-shopping-cart', 'Cart'], ['fas fa-leaf', 'Leaf'], ['fas fa-rocket', 'Rocket'], ['fas fa-shield-alt', 'Shield'], ['fas fa-arrow-right', 'Arrow']],
					read: (c) => (S.get(c, 'selected_icon') || {}).value || '',
					write: (c, v, key) => S.set(c, { [key]: { value: v, library: 'fa-solid' } }),
					help: 'More icons and uploads: Advanced settings.',
				},
				{ key: 'link', label: 'Link', type: 'link' },
			],
			style: [
				{ key: 'view', label: 'Look', type: 'choose', options: [['default', 'Plain'], ['stacked', 'Filled'], ['framed', 'Outlined']] },
				{ key: 'size', label: 'Size', type: 'slider', responsive: true, min: 8, max: 200 },
				{ key: 'align', label: 'Alignment', type: 'choose', responsive: true, options: ALIGN3 },
				{ key: 'primary_color', label: 'Color', type: 'color' },
			],
		},
		'icon-list': {
			content: [
				{ key: 'icon_list', label: 'Items', type: 'lines', newItem: { selected_icon: { value: 'fas fa-check', library: 'fa-solid' } }, help: 'One item per line. Icons and links per item: Advanced settings.' },
				{ key: 'view', label: 'Layout', type: 'choose', options: [['traditional', 'List'], ['inline', 'Inline']] },
			],
			style: [
				{ key: 'icon_color', label: 'Icon color', type: 'color' },
				{ key: 'text_color', label: 'Text color', type: 'color' },
				{ key: 'icon_size', label: 'Icon size', type: 'slider', responsive: true, min: 6, max: 60 },
			],
		},
		video: {
			content: [
				{ key: 'video_type', label: 'Source', type: 'choose', options: [['youtube', 'YouTube'], ['vimeo', 'Vimeo'], ['hosted', 'Upload']] },
				{ key: (c) => ({ youtube: 'youtube_url', vimeo: 'vimeo_url' }[S.get(c, 'video_type')] || 'youtube_url'), label: 'Video address', type: 'text', inputType: 'url', when: (c) => S.get(c, 'video_type') !== 'hosted' },
				{ key: 'hosted_url', label: 'Video file', type: 'media', mediaType: 'video', when: (c) => S.get(c, 'video_type') === 'hosted' },
				{ key: 'autoplay', label: 'Autoplay', type: 'switch' },
				{ key: 'mute', label: 'Muted', type: 'switch' },
				{ key: 'loop', label: 'Loop', type: 'switch' },
			],
			style: [
				{ key: 'aspect_ratio', label: 'Shape', type: 'choose', options: [['169', '16:9'], ['43', '4:3'], ['11', '1:1'], ['916', '9:16']] },
			],
		},
		divider: {
			content: [],
			style: [
				{ key: 'style', label: 'Line', type: 'choose', options: [['solid', 'Solid'], ['dashed', 'Dashed'], ['dotted', 'Dotted'], ['double', 'Double']] },
				{ key: 'weight', label: 'Thickness', type: 'slider', min: 1, max: 10 },
				{ key: 'width', label: 'Width', type: 'presets', responsive: true, match: sizeMatch, options: [['25', '25%', pct(25)], ['50', '50%', pct(50)], ['100', 'Full', pct(100)]] },
				{ key: 'align', label: 'Alignment', type: 'choose', responsive: true, options: ALIGN_LR },
				{ key: 'gap', label: 'Space around', type: 'slider', responsive: true, min: 2, max: 50 },
				{ key: 'color', label: 'Color', type: 'color' },
			],
		},
		spacer: {
			content: [],
			style: [
				{ key: 'space', label: 'Height', type: 'presets', responsive: true, match: sizeMatch, options: [['s', 'S', px(16)], ['m', 'M', px(40)], ['l', 'L', px(80)], ['xl', 'XL', px(120)]] },
				{ key: 'space', label: 'Custom height', type: 'slider', responsive: true, min: 0, max: 400 },
			],
		},
		'image-gallery': {
			content: [
				{ key: 'wp_gallery', label: 'Images', type: 'gallery' },
				{ key: 'gallery_link', label: 'Click opens', type: 'choose', options: [['file', 'Image'], ['attachment', 'Page'], ['none', 'Nothing']] },
			],
			style: [
				{ key: 'gallery_columns', label: 'Columns', type: 'choose', options: [['1', '1'], ['2', '2'], ['3', '3'], ['4', '4'], ['5', '5'], ['6', '6']] },
				{ key: 'thumbnail_size', label: 'Image size', type: 'select', options: [['thumbnail', 'Thumbnail'], ['medium', 'Medium'], ['large', 'Large'], ['full', 'Full size']] },
			],
		},
		form: {
			content: [
				{ key: 'form_name', label: 'Form name', type: 'text' },
				{ key: 'button_text', label: 'Button text', type: 'text' },
				{ key: 'email_to', label: 'Send entries to', type: 'text', inputType: 'email' },
				{ key: 'show_labels', label: 'Show labels', type: 'switch', on: 'true' },
				{ key: 'form_fields', label: 'Fields', type: 'info', text: (c) => ((S.get(c, 'form_fields') || {}).length || 0) + ' fields. Add, remove or reorder fields in Advanced settings.' },
			],
			style: [
				{ key: 'input_size', label: 'Field size', type: 'choose', options: [['xs', 'XS'], ['sm', 'S'], ['md', 'M'], ['lg', 'L'], ['xl', 'XL']] },
				{ key: 'button_size', label: 'Button size', type: 'choose', options: [['xs', 'XS'], ['sm', 'S'], ['md', 'M'], ['lg', 'L'], ['xl', 'XL']] },
			],
		},
		google_maps: {
			content: [{ key: 'address', label: 'Address', type: 'text' }],
			style: [
				{ key: 'zoom', label: 'Zoom', type: 'slider', min: 1, max: 20 },
				{ key: 'height', label: 'Height', type: 'slider', responsive: true, min: 100, max: 1000 },
			],
		},
		posts: {
			content: [
				{ key: '_skin', label: 'Layout', type: 'choose', options: [['classic', 'Classic'], ['cards', 'Cards']] },
				{ key: (c) => (S.get(c, '_skin') || 'classic') + '_posts_per_page', label: 'Number of posts', type: 'number', min: 1, max: 50 },
				{ key: (c) => (S.get(c, '_skin') || 'classic') + '_show_excerpt', label: 'Show excerpt', type: 'switch', off: 'no' },
				{ key: (c) => (S.get(c, '_skin') || 'classic') + '_show_read_more', label: 'Show “Read more”', type: 'switch', off: 'no' },
			],
			style: [
				{ key: (c) => (S.get(c, '_skin') || 'classic') + '_columns', label: 'Columns', type: 'choose', responsive: true, options: [['1', '1'], ['2', '2'], ['3', '3'], ['4', '4']] },
			],
		},
		container: {
			content: [
				{ key: 'flex_direction', label: 'Layout', type: 'choose', responsive: true, options: [['column', 'Stacked', 'col'], ['row', 'Side by side', 'row']] },
				{ key: '_columns', label: 'Columns', type: 'action', buttons: [['1', (c) => columns(c, 1)], ['2', (c) => columns(c, 2)], ['3', (c) => columns(c, 3)], ['4', (c) => columns(c, 4)]], help: 'Adds columns inside this section; existing content is kept.' },
				{ key: 'content_width', label: 'Content width', type: 'choose', options: [['boxed', 'Boxed'], ['full', 'Full width']], when: (c) => !c.model.get('isInner') },
				{ key: 'flex_align_items', label: 'Align items', type: 'choose', responsive: true, options: [['flex-start', 'Start', 'alignLeft'], ['center', 'Center', 'alignCenter'], ['flex-end', 'End', 'alignRight'], ['stretch', 'Stretch', 'alignJustify']] },
			],
			style: [
				{ key: 'padding', label: 'Padding', type: 'presets', responsive: true, match: boxMatch, options: PAD },
				{
					key: 'flex_gap', label: 'Gap', type: 'presets', responsive: true, match: (v, p) => v && String(v.column) === p.column,
					options: [['0', 'None', GAP(0)], ['s', 'S', GAP(10)], ['m', 'M', GAP(20)], ['l', 'L', GAP(40)], ['xl', 'XL', GAP(64)]],
				},
				{ key: 'min_height', label: 'Minimum height', type: 'presets', responsive: true, match: sizeMatch, options: [['none', 'Auto', { unit: 'px', size: '', sizes: [] }], ['50', 'Half', { unit: 'vh', size: 50, sizes: [] }], ['75', '¾', { unit: 'vh', size: 75, sizes: [] }], ['100', 'Screen', { unit: 'vh', size: 100, sizes: [] }]] },
				{ key: 'background_color', label: 'Background color', type: 'color', write: (c, v, key) => S.set(c, { background_background: 'classic', [key]: v }) },
				{
					key: 'background_image', label: 'Background image', type: 'media',
					write: (c, v, key) => S.set(c, v && v.url ? { background_background: 'classic', [key]: v, background_size: 'cover', background_position: 'center center' } : { [key]: v }),
				},
				ROUNDED('border_radius'),
			],
		},
	};
	I.schemas.section = I.schemas.container;

	const card = (title, kids) => h('section.gwe-card-sec', { 'aria-label': title }, h('h3.gwe-sec-title', { text: title }), kids);

	/** Renders the Edit tab for the selected element. */
	I.pane = function () {
		const pane = h('div.gwe-pane-edit');
		let fields = [];
		let current = null;
		const render = () => {
			S.empty(pane);
			fields = [];
			const c = S.selected();
			current = c;
			if (!c) {
				pane.append(h('div.gwe-empty', null, S.icon('edit', 32), h('p', { text: 'Select something on the page to edit it.' }), h('p.gwe-muted', { text: 'Click any heading, image, button or section.' })));
				return;
			}
			const type = S.type(c);
			const schema = I.schemas[type];
			// Header: breadcrumbs up to the section, the element name, quick actions.
			const crumbs = h('nav.gwe-crumbs', { 'aria-label': 'Element path' });
			const chain = [];
			for (let p = c.parent; p && p.parent; p = p.parent) { chain.unshift(p); }
			chain.forEach((p) => crumbs.append(h('button.gwe-crumb', { type: 'button', text: S.title(p), onClick: () => S.select(p) }), h('span.gwe-crumb-sep', { 'aria-hidden': 'true', text: '›' })));
			pane.append(h('div.gwe-edit-head', null,
				chain.length ? crumbs : null,
				h('div.gwe-edit-title', null, h('h2', { text: S.title(c) }),
					h('div.gwe-edit-actions', null,
						S.iconButton('copy', 'Duplicate', () => S.run('document/elements/duplicate', { container: c })),
						S.iconButton('trash', 'Delete', () => S.run('document/elements/delete', { container: c })))),
			));
			const add = (list) => list.filter((f) => !f.when || f.when(c)).map((f) => { const api = S.field(c, f); if (api) { fields.push(api); } return api && api.el; });
			if (schema) {
				if (schema.content.length) { pane.append(card('Content', add(schema.content))); }
				if (schema.style.length) { pane.append(card('Style', add(schema.style))); }
			} else {
				pane.append(h('p.gwe-muted.gwe-pad', { text: 'This element has no simple settings yet. Use Advanced settings for all its options.' }));
			}
			pane.append(card('Visibility', add(VISIBILITY)));
			pane.append(h('div.gwe-pad', null, h('button.gwe-btn.gwe-btn-wide', { type: 'button', onClick: () => S.editInUpstream(c) }, S.icon('gear', 16), h('span', { text: 'Advanced settings' }))));
		};
		pane.render = render;
		const signature = (c) => {
			const schema = c && I.schemas[S.type(c)];
			const all = schema ? schema.content.concat(schema.style) : [];
			return c ? S.title(c) + '|' + all.filter((f) => !f.when || f.when(c)).map((f) => S.baseKey(f, c)).join(',') : '';
		};
		let sig = '';
		const full = () => { render(); sig = signature(current); };
		S.on('selection', full);
		S.on('device', full);
		S.on('globals', full);
		S.on('changed', S.debounce(() => {
			const c = S.selected();
			if (c !== current || signature(c) !== sig) { full(); return; }
			fields.forEach((f) => f.refresh());
		}, 60));
		pane.render = full;
		full();
		return pane;
	};
})();
