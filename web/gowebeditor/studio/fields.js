/* Golementor Studio — inspector fields. Each field reads an element setting and writes it back
   with document/elements/settings (or document/globals/settings for kit colors). Responsive fields
   write the _tablet/_mobile key while the device switcher is on tablet/mobile. */
(function () {
	'use strict';
	const S = window.GWEStudio;
	const { h } = S;
	const F = S.fields = {};
	let uid = 0;
	const nextId = () => 'gwe-f-' + (++uid);

	const suffix = { desktop: '', tablet: '_tablet', mobile: '_mobile' };
	/** The setting key a field writes on the current device. */
	S.rkey = function (field, c) {
		const key = typeof field.key === 'function' ? field.key(c) : field.key;
		return field.responsive ? key + (suffix[S.device()] || '') : key;
	};
	S.baseKey = (field, c) => (typeof field.key === 'function' ? field.key(c) : field.key);
	/** The value in effect on this device: its own, else the next larger device's. */
	function inherited(c, field) {
		const key = S.baseKey(field, c);
		const d = S.device();
		const chain = d === 'mobile' ? ['_mobile', '_tablet', ''] : d === 'tablet' ? ['_tablet', ''] : [''];
		for (const sfx of chain.slice(1)) {
			const v = S.get(c, key + sfx);
			if (!isEmpty(v)) { return v; }
		}
		return undefined;
	}
	function isEmpty(v) {
		if (v == null || v === '') { return true; }
		if (typeof v === 'object' && !Array.isArray(v)) {
			if ('size' in v) { return v.size === '' || v.size == null; }
			if ('top' in v) { return ['top', 'right', 'bottom', 'left'].every((k) => v[k] === '' || v[k] == null); }
			if ('url' in v) { return !v.url; }
		}
		return false;
	}
	F.isEmpty = isEmpty;
	function read(c, field) {
		if (field.read) { return field.read(c); }
		return S.get(c, S.rkey(field, c));
	}
	function write(c, field, value) {
		if (!c.view || c.view.isDestroyed) { return; } // the element is gone (undo, delete)
		const now = field.read ? field.read(c) : S.get(c, S.rkey(field, c));
		const plain = (v) => JSON.stringify(v && v.toJSON ? v.toJSON() : v);
		if (plain(now) === plain(value)) { return; }
		if (field.write) { field.write(c, value, S.rkey(field, c)); return; }
		S.set(c, { [S.rkey(field, c)]: value });
	}
	F.read = read;
	F.write = write;

	/** The label row: name, a device badge for responsive fields, and an optional reset. */
	function label(field, id, c, onReset) {
		const row = h('div.gwe-f-label');
		row.appendChild(h('label', { for: id, text: field.label }));
		if (field.responsive) {
			const d = S.device();
			row.appendChild(h('span.gwe-f-device', { title: 'This value applies to ' + d + (d === 'desktop' ? ' and larger changes cascade down' : ''), 'aria-label': 'Applies to ' + d }, S.icon(d, 14)));
		}
		if (onReset) {
			row.appendChild(h('button.gwe-f-reset', { type: 'button', title: 'Reset ' + field.label, 'aria-label': 'Reset ' + field.label, onClick: onReset }, S.icon('undo', 14)));
		}
		return row;
	}

	/** Wraps a control in a field row; refresh() re-reads the value unless the user is typing in it. */
	function make(c, field, build) {
		const id = nextId();
		const wrap = h('div.gwe-f.gwe-f-' + field.type);
		const api = { el: wrap, refresh() {} };
		const render = () => {
			S.empty(wrap);
			const reset = field.reset !== false && !isEmpty(read(c, field)) && field.type !== 'action' ? () => { write(c, field, field.resetTo !== undefined ? field.resetTo : emptyFor(c, field)); } : null;
			if (field.label && !field.inline) { wrap.appendChild(label(field, id, c, reset)); }
			const body = build(id, api);
			wrap.appendChild(body);
			if (field.help) { wrap.appendChild(h('p.gwe-f-help', { text: field.help })); }
		};
		api.refresh = () => {
			const active = document.activeElement;
			const typing = wrap.contains(active) && active.matches('input:not([type=checkbox]):not([type=range]):not([type=color]),textarea,select,[contenteditable]');
			if (typing) { return; }
			// Keep keyboard focus on the same control across the re-render.
			const sel = 'button,input,select,textarea,[contenteditable]';
			const idx = wrap.contains(active) ? Array.from(wrap.querySelectorAll(sel)).indexOf(active) : -1;
			render();
			if (idx >= 0) { const n = wrap.querySelectorAll(sel)[idx]; if (n) { n.focus(); } }
		};
		render();
		return api;
	}
	function emptyFor(c, field) {
		const ctl = S.control(c, S.rkey(field, c));
		const def = ctl ? ctl.default : '';
		if (def && typeof def === 'object') {
			return Array.isArray(def) ? [] : Object.assign({}, def);
		}
		return field.responsive && S.device() !== 'desktop' ? '' : (def == null ? '' : def);
	}

	F.text = (c, field) => make(c, field, (id) => {
		const input = h(field.multiline ? 'textarea' : 'input', { id, class: 'gwe-input', rows: field.rows || 3, placeholder: field.placeholder || '', type: field.multiline ? null : (field.inputType || 'text') });
		input.value = read(c, field) || '';
		const send = S.debounce(() => write(c, field, input.value), 300);
		input.addEventListener('input', send);
		input.addEventListener('blur', () => send.flush());
		return input;
	});
	F.textarea = (c, field) => F.text(c, Object.assign({ multiline: true }, field));

	F.number = (c, field) => make(c, field, (id) => {
		const v = read(c, field);
		const input = h('input.gwe-input', { id, type: 'number', min: field.min, max: field.max, step: field.step || 1, placeholder: field.placeholder || '' });
		input.value = v == null ? '' : v;
		const send = S.debounce(() => write(c, field, input.value === '' ? '' : Number(input.value)), 300);
		input.addEventListener('input', send);
		return input;
	});

	F.select = (c, field) => make(c, field, (id) => {
		const v = read(c, field);
		const options = typeof field.options === 'function' ? field.options(c) : field.options;
		const sel = h('select.gwe-input', { id }, options.map(([val, text]) => h('option', { value: val, text })));
		sel.value = v == null ? '' : v;
		if (field.responsive && isEmpty(v)) {
			const inh = inherited(c, field);
			if (!isEmpty(inh)) { sel.value = ''; sel.title = 'Inherited: ' + inh; }
		}
		sel.addEventListener('change', () => write(c, field, sel.value));
		return sel;
	});

	/** A segmented radio group. options: [[value, label, icon?]]. Clicking the active one clears it. */
	function segmented(id, options, current, onPick, ariaLabel, inheritedValue) {
		const group = h('div.gwe-seg', { role: 'radiogroup', id, 'aria-label': ariaLabel });
		const buttons = options.map(([val, text, icon]) => {
			const on = String(current) === String(val);
			const b = h('button.gwe-seg-b', {
				type: 'button', role: 'radio', 'aria-checked': on ? 'true' : 'false', tabindex: on || (current === '' && val === options[0][0]) ? '0' : '-1',
				title: text, 'aria-label': text, class: (on ? 'is-on' : '') + (!on && inheritedValue !== undefined && String(inheritedValue) === String(val) ? ' is-inherited' : ''),
				onClick: () => onPick(on ? '' : val),
			}, icon ? S.icon(icon, 16) : h('span', { text }));
			b.className = 'gwe-seg-b' + (on ? ' is-on' : '') + (!on && inheritedValue !== undefined && String(inheritedValue) === String(val) ? ' is-inherited' : '');
			return b;
		});
		group.addEventListener('keydown', (e) => {
			const i = buttons.indexOf(document.activeElement);
			if (i < 0) { return; }
			const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0;
			if (!step) { return; }
			e.preventDefault();
			const n = buttons[(i + step + buttons.length) % buttons.length];
			n.focus();
		});
		S.append(group, buttons);
		return group;
	}
	F.segmented = segmented;

	F.choose = (c, field) => make(c, field, (id) => {
		const v = read(c, field);
		const inh = field.responsive && isEmpty(v) ? inherited(c, field) : undefined;
		const options = typeof field.options === 'function' ? field.options(c) : field.options;
		return segmented(id, options, v == null ? '' : v, (val) => write(c, field, val === '' && field.required ? options[0][0] : val), field.label, inh);
	});

	/** Presets: options [[id, label, value]]; field.match(c, value) finds the active one. */
	F.presets = (c, field) => make(c, field, (id) => {
		const v = read(c, field);
		const options = field.options;
		let current = '';
		for (const [pid, , val] of options) {
			if (field.match ? field.match(v, val) : JSON.stringify(v) === JSON.stringify(val)) { current = pid; break; }
		}
		if (!current && !isEmpty(v)) { current = '__custom'; }
		const opts = options.map(([pid, text, , icon]) => [pid, text, icon]);
		const group = segmented(id, opts, current, (pid) => {
			const hit = options.find((o) => o[0] === pid);
			write(c, field, hit ? hit[2] : emptyFor(c, field));
		}, field.label);
		if (current === '__custom') {
			return h('div', null, group, h('p.gwe-f-help', { text: 'Custom value set in Advanced settings.' }));
		}
		return group;
	});

	F.switch = (c, field) => make(c, Object.assign({ inline: true }, field), (id) => {
		const v = read(c, field);
		const on = field.on || 'yes';
		const box = h('input', { id, type: 'checkbox', class: 'gwe-switch' });
		box.checked = !!v && v !== 'no';
		box.addEventListener('change', () => write(c, field, box.checked ? on : (field.off != null ? field.off : '')));
		return h('label.gwe-switch-row', { for: id }, box, h('span', { text: field.label }), field.responsive ? h('span.gwe-f-device', null, S.icon(S.device(), 14)) : null);
	});

	/** A size slider ({unit, size}). */
	F.slider = (c, field) => make(c, field, (id) => {
		const v = read(c, field) || {};
		const inh = field.responsive && isEmpty(v) ? inherited(c, field) : null;
		const unit = v.unit || (inh && inh.unit) || field.unit || 'px';
		const range = h('input.gwe-range', { type: 'range', min: field.min || 0, max: field.max || 100, step: field.step || 1, 'aria-label': field.label });
		const num = h('input.gwe-input.gwe-num', { id, type: 'number', min: field.min || 0, max: field.max, step: field.step || 1, placeholder: inh && inh.size !== '' ? String(inh.size) : '' });
		const size = v.size === '' || v.size == null ? '' : v.size;
		range.value = size === '' ? (inh && inh.size !== '' ? inh.size : field.min || 0) : size;
		num.value = size;
		const send = S.debounce((n) => write(c, field, n === '' ? { unit, size: '', sizes: [] } : { unit, size: Number(n), sizes: [] }), 200);
		range.addEventListener('input', () => { num.value = range.value; send(range.value); });
		num.addEventListener('input', () => { range.value = num.value; send(num.value); });
		return h('div.gwe-slider', null, range, num, h('span.gwe-unit', { text: unit }));
	});

	/** Colors: the kit's global colors first, then a custom color. */
	F.color = (c, field) => make(c, field, (id) => {
		const key = S.baseKey(field, c);
		const gid = (S.globalOf(c, key).match(/id=([\w-]+)/) || [])[1] || '';
		const value = S.get(c, key) || '';
		const wrap = h('div.gwe-color', { id, role: 'group', 'aria-label': field.label });
		const swatches = h('div.gwe-swatches');
		S.ordered(S.globals.colors).forEach((g) => {
			const on = gid === g.id;
			swatches.appendChild(h('button.gwe-swatch' + (on ? '.is-on' : ''), {
				type: 'button', title: g.title + ' (global)', 'aria-label': g.title + ' global color', 'aria-pressed': on ? 'true' : 'false',
				style: { background: g.value },
				onClick: () => {
					S.batch(field.label, c, () => {
						S.setGlobal(c, key, on ? '' : 'globals/colors?id=' + g.id);
					});
				},
			}));
		});
		const custom = h('input.gwe-color-input', { type: 'color', 'aria-label': field.label + ' custom color', title: 'Custom color' });
		custom.value = /^#[0-9a-f]{6}$/i.test(value) ? value : '#000000';
		const hex = h('input.gwe-input.gwe-hex', { type: 'text', value: gid ? '' : value, placeholder: gid ? ((S.globals.colors[gid] || {}).title || 'Global') : 'Default', 'aria-label': field.label + ' hex value', maxlength: 9 });
		const apply = S.debounce((v) => {
			S.batch(field.label, c, () => {
				if (S.globalOf(c, key)) { S.setGlobal(c, key, ''); }
				S.set(c, { [key]: v });
			});
		}, 150);
		custom.addEventListener('input', () => { hex.value = custom.value; apply(custom.value); });
		hex.addEventListener('change', () => { if (hex.value === '' || /^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i.test(hex.value)) { apply(hex.value); } });
		swatches.appendChild(h('label.gwe-swatch.gwe-swatch-custom' + (!gid && value ? '.is-on' : ''), { title: 'Custom color', style: !gid && value ? { background: value } : null }, custom));
		wrap.appendChild(swatches);
		wrap.appendChild(hex);
		return wrap;
	});

	/** wp.media picker. */
	function pickMedia(opts, done) {
		if (!window.wp || !wp.media) { S.toast('The media library is not available.', 'error'); return; }
		const frame = wp.media({ title: opts.title || 'Choose image', multiple: !!opts.multiple, library: { type: opts.type || 'image' }, button: { text: opts.button || 'Use this' } });
		frame.on('select', () => {
			const sel = frame.state().get('selection').toJSON();
			done(opts.multiple ? sel : sel[0]);
		});
		frame.open();
	}
	S.pickMedia = pickMedia;

	F.media = (c, field) => make(c, field, (id) => {
		const v = read(c, field) || {};
		const box = h('div.gwe-media');
		const kind = field.mediaType || 'image';
		if (v.url) {
			box.appendChild(kind === 'image' ? h('img.gwe-media-thumb', { src: v.url, alt: '' }) : h('div.gwe-media-file', { text: v.url.split('/').pop() }));
		} else {
			box.appendChild(h('div.gwe-media-empty', null, S.icon(kind === 'image' ? 'image' : 'video', 24), h('span', { text: 'No ' + kind + ' chosen' })));
		}
		const choose = h('button.gwe-btn', {
			id, type: 'button', text: v.url ? 'Replace' : 'Choose ' + kind,
			onClick: () => pickMedia({ type: kind, title: 'Choose ' + kind }, (a) => write(c, field, { id: a.id, url: a.url, alt: a.alt || '', source: 'library', size: '' })),
		});
		const remove = v.url ? h('button.gwe-btn.is-link', { type: 'button', text: 'Remove', onClick: () => write(c, field, { id: '', url: '', size: '' }) }) : null;
		box.appendChild(h('div.gwe-media-actions', null, choose, remove));
		return box;
	});

	F.gallery = (c, field) => make(c, field, (id) => {
		const v = read(c, field) || [];
		const grid = h('div.gwe-gallery-thumbs', null, v.slice(0, 12).map((a) => h('img', { src: a.url, alt: '' })));
		const btn = h('button.gwe-btn', {
			id, type: 'button', text: v.length ? 'Choose images (' + v.length + ')' : 'Choose images',
			onClick: () => pickMedia({ multiple: true, title: 'Choose gallery images', button: 'Use these' }, (list) => write(c, field, list.map((a) => ({ id: a.id, url: a.url })))),
		});
		return h('div.gwe-gallery', null, v.length ? grid : null, btn);
	});

	F.link = (c, field) => make(c, field, (id) => {
		const v = read(c, field) || {};
		const url = h('input.gwe-input', { id, type: 'url', placeholder: 'https:// or /page', value: v.url || '' });
		const ext = h('input', { type: 'checkbox', id: id + '-ext' });
		ext.checked = v.is_external === 'on';
		const send = S.debounce(() => write(c, field, Object.assign({ is_external: '', nofollow: '', custom_attributes: '' }, v, { url: url.value.trim(), is_external: ext.checked ? 'on' : '' })), 350);
		url.addEventListener('input', send);
		url.addEventListener('blur', () => send.flush());
		ext.addEventListener('change', () => { send(); send.flush(); });
		return h('div.gwe-link', null, url, h('label.gwe-check', { for: id + '-ext' }, ext, h('span', { text: 'Open in a new tab' })));
	});

	/** Rich text: a contenteditable with a few commands; stores HTML. */
	F.richtext = (c, field) => make(c, field, (id) => {
		const ed = h('div.gwe-rich', { id, contenteditable: 'true', role: 'textbox', 'aria-multiline': 'true', 'aria-label': field.label, html: read(c, field) || '' });
		const send = S.debounce(() => write(c, field, ed.innerHTML), 350);
		ed.addEventListener('input', send);
		ed.addEventListener('blur', () => send.flush());
		const cmd = (name, arg) => () => { ed.focus(); document.execCommand(name, false, arg); send(); };
		const bar = h('div.gwe-rich-bar', { role: 'toolbar', 'aria-label': 'Text formatting' },
			S.iconButton('bold', 'Bold', cmd('bold')),
			S.iconButton('italic', 'Italic', cmd('italic')),
			S.iconButton('list', 'Bulleted list', cmd('insertUnorderedList')),
			S.iconButton('link', 'Link', () => { const u = prompt('Link address'); if (u) { cmd('createLink', u)(); } }),
		);
		return h('div.gwe-rich-wrap', null, bar, ed);
	});

	/** One line per repeater item (icon list items). */
	F.lines = (c, field) => make(c, field, (id) => {
		const items = (S.get(c, field.key) && S.get(c, field.key).toJSON ? S.get(c, field.key).toJSON() : S.get(c, field.key)) || [];
		const ta = h('textarea.gwe-input', { id, rows: Math.max(3, items.length + 1), placeholder: 'One item per line' });
		ta.value = items.map((it) => it[field.textKey || 'text'] || '').join('\n');
		const send = S.debounce(() => {
			const lines = ta.value.split('\n').map((s) => s.trim()).filter(Boolean);
			const next = lines.map((text, i) => Object.assign({}, items[i] || items[items.length - 1] || field.newItem || {}, { _id: (items[i] && items[i]._id) || elementorCommon.helpers.getUniqueId(), [field.textKey || 'text']: text }));
			write(c, field, next);
		}, 500);
		ta.addEventListener('input', send);
		ta.addEventListener('blur', () => send.flush());
		return ta;
	});

	F.action = (c, field) => make(c, field, (id) => h('div.gwe-actions', { id }, field.buttons.map(([text, fn, icon]) => h('button.gwe-btn', { type: 'button', onClick: () => fn(c), title: text }, icon ? S.icon(icon, 16) : null, h('span', { text })))));
	F.info = (c, field) => make(c, Object.assign({ reset: false }, field), () => h('p.gwe-f-help', { text: typeof field.text === 'function' ? field.text(c) : field.text }));

	/** Builds a field for a container from its spec. */
	S.field = function (c, field) {
		const fn = F[field.type];
		if (!fn) { return null; }
		return fn(c, field);
	};
})();
