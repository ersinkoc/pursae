/*
 * Pursae Flipbook admin: the Media Library modal (PDFs only) for Add New and Replace PDF, cover
 * thumbnails drawn with pdf.js, copy buttons and the full-screen viewer over the admin.
 */
import * as pdfjs from './vendor/pdf.min.mjs';

const root = document.querySelector('[data-pfb-admin]');
if (root) init();

function init() {
	pdfjs.GlobalWorkerOptions.workerSrc = root.dataset.worker;

	// --- Media Library modal ---------------------------------------------------------------------
	let frame;
	let onPick = null;
	const pick = (title, button, cb) => {
		const wp = window.wp;
		if (!wp || !wp.media) {
			window.alert('The Media Library could not be loaded. Reload the page and try again.');
			return;
		}
		onPick = cb;
		if (!frame) {
			frame = wp.media({ title, library: { type: 'application/pdf' }, button: { text: button }, multiple: false });
			frame.on('select', () => {
				const a = frame.state().get('selection').first().toJSON();
				if (a.mime !== 'application/pdf' && !/\.pdf($|\?)/i.test(a.url || '')) {
					window.alert('Please pick a PDF file.');
					return;
				}
				onPick && onPick(a);
			});
		}
		frame.open();
	};
	const fill = (form, a) => {
		form.elements.attachment_id.value = a.id || '';
		form.elements.pdf_url.value = a.url || '';
		form.elements.file_name.value = a.filename || '';
		form.elements.mime.value = a.mime || '';
	};
	const create = () => pick('Choose a PDF for the flipbook', 'Create flipbook', (a) => {
		const f = document.getElementById('pfb-create');
		fill(f, a);
		f.elements.title.value = a.title || '';
		f.submit();
	});
	root.querySelectorAll('[data-pfb-add]').forEach((b) => b.addEventListener('click', create));
	document.querySelectorAll('#adminmenu a[href$="page=pursae-flipbook&action=new"]').forEach((a) =>
		a.addEventListener('click', (e) => { e.preventDefault(); create(); }));
	if (root.dataset.autoopen) {
		const open = () => (window.wp && window.wp.media ? create() : setTimeout(open, 150));
		open();
		history.replaceState(null, '', location.pathname + '?page=pursae-flipbook');
	}

	const edit = document.getElementById('pfb-edit-form');
	root.querySelector('[data-pfb-replace]')?.addEventListener('click', () =>
		pick('Replace the PDF', 'Use this PDF', (a) => {
			fill(edit, a);
			const name = root.querySelector('[data-pfb-file]');
			name.textContent = (a.filename || a.url) + ' — save to apply';
			name.classList.add('is-changed');
		}));
	const bg = root.querySelector('#pfb-bg');
	bg?.addEventListener('input', () => (root.querySelector('[data-pfb-bg-code]').textContent = bg.value));

	// --- Copy, confirm ---------------------------------------------------------------------------
	root.addEventListener('click', async (e) => {
		const copy = e.target.closest('[data-pfb-copy]');
		if (copy) {
			const text = copy.dataset.pfbCopy;
			try {
				await navigator.clipboard.writeText(text);
			} catch (err) {
				const input = copy.parentElement.querySelector('input');
				input.select();
				document.execCommand('copy');
			}
			const label = copy.querySelector('.pfb-copy-label');
			copy.classList.add('is-copied');
			label.textContent = 'Copied!';
			setTimeout(() => { copy.classList.remove('is-copied'); label.textContent = 'Copy'; }, 1600);
			return;
		}
		const del = e.target.closest('[data-pfb-confirm]');
		if (del && !window.confirm(del.dataset.pfbConfirm)) e.preventDefault();
	});
	root.querySelectorAll('.pfb-code input').forEach((i) => i.addEventListener('focus', () => i.select()));

	// --- Full-screen viewer over the admin -------------------------------------------------------
	const overlay = document.getElementById('pfb-overlay');
	const iframe = overlay.querySelector('iframe');
	let lastFocus;
	const close = () => {
		overlay.hidden = true;
		iframe.src = 'about:blank';
		document.documentElement.classList.remove('pfb-noscroll');
		lastFocus && lastFocus.focus();
	};
	root.addEventListener('click', (e) => {
		const v = e.target.closest('[data-pfb-view]');
		if (!v) return;
		e.preventDefault();
		lastFocus = v;
		iframe.src = v.dataset.pfbView;
		overlay.hidden = false;
		document.documentElement.classList.add('pfb-noscroll');
		overlay.querySelector('[data-pfb-close]').focus();
		iframe.addEventListener('load', () => {
			try { iframe.contentWindow.focus(); } catch (err) { /* same origin, but stay safe */ }
			try {
				iframe.contentDocument.addEventListener('keydown', (ev) => ev.key === 'Escape' && !iframe.contentDocument.fullscreenElement && close());
			} catch (err) { /* ignore */ }
		}, { once: true });
	});
	overlay.querySelector('[data-pfb-close]').addEventListener('click', close);
	document.addEventListener('keydown', (e) => e.key === 'Escape' && !overlay.hidden && close());

	// --- Covers ----------------------------------------------------------------------------------
	const docs = new Map();
	const cover = async (btn) => {
		const url = btn.dataset.pfbCover;
		try {
			if (!docs.has(url)) docs.set(url, pdfjs.getDocument({ url, isEvalSupported: false }).promise);
			const pdf = await docs.get(url);
			const page = await pdf.getPage(1);
			const vp1 = page.getViewport({ scale: 1 });
			const box = btn.getBoundingClientRect();
			const dpr = Math.min(window.devicePixelRatio || 1, 2);
			const scale = Math.min((box.width * 0.8 * dpr) / vp1.width, (box.height * 0.84 * dpr) / vp1.height);
			const vp = page.getViewport({ scale });
			const c = document.createElement('canvas');
			c.width = Math.round(vp.width);
			c.height = Math.round(vp.height);
			c.className = 'pfb-cover-img';
			await page.render({ canvasContext: c.getContext('2d', { alpha: false }), viewport: vp, background: '#fff' }).promise;
			btn.prepend(c);
			btn.classList.add('is-drawn');
			const pages = btn.querySelector('.pfb-cover-pages');
			pages.textContent = pdf.numPages + (pdf.numPages === 1 ? ' page' : ' pages');
			pages.hidden = false;
		} catch (err) {
			btn.classList.add('is-broken');
			btn.querySelector('.pfb-cover-ph').title = 'The PDF could not be read';
		}
	};
	const io = new IntersectionObserver((entries) => {
		for (const en of entries) {
			if (en.isIntersecting) {
				io.unobserve(en.target);
				cover(en.target);
			}
		}
	}, { rootMargin: '300px' });
	root.querySelectorAll('[data-pfb-cover]').forEach((b) => io.observe(b));
}
