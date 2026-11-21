/*
 * Pursae Flipbook viewer.
 *
 * Mounts on every [data-pfb] element: loads the PDF with pdf.js (Apache-2.0), draws the pages into
 * canvases around the current spread only, and turns them with StPageFlip (MIT). Double pages on
 * wide screens, single pages on narrow ones; keyboard, swipe, thumbnails, zoom, share, download and
 * the Fullscreen API. The full-screen page (/flipbook/N) keeps the page in the URL hash (#p=6).
 */
import * as pdfjs from './vendor/pdf.min.mjs';
import { PageFlip } from './vendor/page-flip.module.js';

const ICONS = {
	first: '<path d="M18 17 13 12l5-5M11 17l-5-5 5-5"/>',
	prev: '<path d="m15 18-6-6 6-6"/>',
	next: '<path d="m9 18 6-6-6-6"/>',
	last: '<path d="m6 17 5-5-5-5M13 17l5-5-5-5"/>',
	grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
	zoomIn: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5M11 8v6M8 11h6"/>',
	zoomOut: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5M8 11h6"/>',
	share: '<circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="m8.6 13.5 6.8 4M15.4 6.5l-6.8 4"/>',
	download: '<path d="M12 3v12M7 10l5 5 5-5M5 21h14"/>',
	expand: '<path d="M8 3H5a2 2 0 0 0-2 2v3M21 8V5a2 2 0 0 0-2-2h-3M3 16v3a2 2 0 0 0 2 2h3M16 21h3a2 2 0 0 0 2-2v-3"/>',
	shrink: '<path d="M8 3v3a2 2 0 0 1-2 2H3M21 8h-3a2 2 0 0 1-2-2V3M3 16h3a2 2 0 0 1 2 2v3M16 21v-3a2 2 0 0 1 2-2h3"/>',
	close: '<path d="M18 6 6 18M6 6l12 12"/>',
};
const svg = (name) => `<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">${ICONS[name]}</svg>`;
const ZOOMS = [1, 1.5, 2, 3];
const MAX_CANVAS = 2600; // px on the long side of a rendered page
const AHEAD = 3; // pages rendered on each side of the current spread

const lang = (document.documentElement.lang || 'en').slice(0, 2).toLowerCase();
const EN = { prev: 'Previous page', next: 'Next page', first: 'First page', last: 'Last page', thumbs: 'Pages', zoomIn: 'Zoom in', zoomOut: 'Zoom out', share: 'Copy link', download: 'Download PDF', full: 'Full screen', exitFull: 'Exit full screen', page: 'Page', copied: 'Link copied', error: 'The PDF could not be opened.', open: 'Open the PDF', close: 'Close' };
const T = {
	en: EN,
	tr:{ prev: 'Önceki sayfa', next: 'Sonraki sayfa', first: 'İlk sayfa', last: 'Son sayfa', thumbs: 'Sayfalar', zoomIn: 'Yakınlaştır', zoomOut: 'Uzaklaştır', share: 'Bağlantıyı kopyala', download: 'PDF indir', full: 'Tam ekran', exitFull: 'Tam ekrandan çık', page: 'Sayfa', copied: 'Bağlantı kopyalandı', error: 'PDF açılamadı.', open: 'PDF’i aç', close: 'Kapat' },
}[lang] || EN;
const t = (k) => T[k] || EN[k];

let workerSet = false;

class Viewer {
	constructor(root) {
		this.root = root;
		this.full = root.classList.contains('pfb--full');
		this.zoom = 0;
		this.rendered = new Map(); // page index -> rendered width in px
		this.rendering = new Map();
		this.build();
		this.load();
	}

	btn(name, label, cls = '') {
		return `<button type="button" class="pfb-btn ${cls}" data-act="${name}" aria-label="${label}" title="${label}">${svg(name === 'full' ? 'expand' : name)}</button>`;
	}

	build() {
		const d = this.root.dataset;
		this.root.tabIndex = 0;
		this.root.setAttribute('role', 'region');
		this.root.setAttribute('aria-label', d.title || 'Flipbook');
		this.root.insertAdjacentHTML('beforeend', `
			<div class="pfb-stage"><div class="pfb-book"></div>
				<button type="button" class="pfb-nav pfb-nav--prev" data-act="prev" aria-label="${t('prev')}">${svg('prev')}</button>
				<button type="button" class="pfb-nav pfb-nav--next" data-act="next" aria-label="${t('next')}">${svg('next')}</button>
			</div>
			<div class="pfb-bar" hidden>
				<div class="pfb-bar-group pfb-bar-title"><span>${escapeHTML(d.title || '')}</span></div>
				<div class="pfb-bar-group pfb-bar-pager">
					${this.btn('first', t('first'), 'pfb-hide-sm')}${this.btn('prev', t('prev'))}
					<label class="pfb-pager"><span class="pfb-sr">${t('page')}</span><input type="text" inputmode="numeric" class="pfb-page-input" value="1" aria-label="${t('page')}"><span class="pfb-total">/ 0</span></label>
					${this.btn('next', t('next'))}${this.btn('last', t('last'), 'pfb-hide-sm')}
				</div>
				<div class="pfb-bar-group pfb-bar-tools">
					${this.btn('grid', t('thumbs'))}
					${this.btn('zoomOut', t('zoomOut'), 'pfb-hide-sm')}${this.btn('zoomIn', t('zoomIn'), 'pfb-hide-sm')}
					${d.share ? this.btn('share', t('share')) : ''}
					${d.download ? `<a class="pfb-btn" data-act="download" href="${escapeAttr(d.pdf)}" download aria-label="${t('download')}" title="${t('download')}">${svg('download')}</a>` : ''}
					${this.btn('full', t('full'))}
				</div>
			</div>
			<div class="pfb-thumbs" hidden><div class="pfb-thumbs-head"><strong>${t('thumbs')}</strong>${this.btn('close', t('close'))}</div><div class="pfb-thumbs-grid"></div></div>
			<div class="pfb-toast" role="status" aria-live="polite"></div>`);
		this.stage = this.root.querySelector('.pfb-stage');
		this.bookEl = this.root.querySelector('.pfb-book');
		this.bar = this.root.querySelector('.pfb-bar');
		this.input = this.root.querySelector('.pfb-page-input');
		this.totalEl = this.root.querySelector('.pfb-total');
		this.thumbs = this.root.querySelector('.pfb-thumbs');
		this.loadingText = this.root.querySelector('.pfb-loading-text');

		this.root.addEventListener('click', (e) => {
			const el = e.target.closest('[data-act]');
			if (!el || !this.root.contains(el)) return;
			const act = el.dataset.act;
			if (act === 'download') return;
			e.preventDefault();
			this.act(act);
		});
		this.input.addEventListener('change', () => this.go(parseInt(this.input.value, 10) - 1));
		this.input.addEventListener('focus', () => this.input.select());
		const keyTarget = this.full ? document : this.root;
		keyTarget.addEventListener('keydown', (e) => {
			if (e.target === this.input || e.altKey || e.ctrlKey || e.metaKey) return;
			const map = { ArrowLeft: 'prev', ArrowRight: 'next', PageUp: 'prev', PageDown: 'next', Home: 'first', End: 'last' };
			if (map[e.key]) {
				e.preventDefault();
				this.act(map[e.key]);
			} else if (e.key === 'Escape' && !this.thumbs.hidden) {
				this.thumbs.hidden = true;
			}
		});
		document.addEventListener('fullscreenchange', () => this.onFullscreen());
		document.addEventListener('webkitfullscreenchange', () => this.onFullscreen());
	}

	async load() {
		const d = this.root.dataset;
		if (!workerSet) {
			pdfjs.GlobalWorkerOptions.workerSrc = d.worker;
			workerSet = true;
		}
		try {
			const task = pdfjs.getDocument({ url: d.pdf, isEvalSupported: false });
			this.pdf = await task.promise;
			const first = await this.pdf.getPage(1);
			const vp = first.getViewport({ scale: 1 });
			this.ratio = vp.width / vp.height;
			this.count = this.pdf.numPages;
			this.mount();
		} catch (err) {
			console.error('Pursae Flipbook:', err);
			this.root.classList.add('is-error');
			const l = this.root.querySelector('.pfb-loading');
			if (l) l.innerHTML = `<p class="pfb-loading-text">${t('error')}</p><a class="pfb-open" href="${escapeAttr(d.pdf)}" target="_blank" rel="noopener">${t('open')}</a>`;
		}
	}

	mount() {
		const pages = [];
		for (let i = 0; i < this.count; i++) {
			const p = document.createElement('div');
			p.className = 'pfb-page';
			if (i === 0 || (i === this.count - 1 && this.count > 2)) p.dataset.density = 'hard';
			p.innerHTML = '<canvas></canvas><span class="pfb-page-ph"></span>';
			pages.push(p);
		}
		const base = 600;
		this.flip = new PageFlip(this.bookEl, {
			width: base,
			height: Math.round(base / this.ratio),
			size: 'stretch',
			minWidth: 290, // StPageFlip shows single pages below 2 × minWidth: phones and narrow columns
			maxWidth: 4000,
			minHeight: 160,
			maxHeight: 6000,
			showCover: true,
			usePortrait: true,
			autoSize: true,
			drawShadow: true,
			maxShadowOpacity: 0.35,
			flippingTime: 650,
			mobileScrollSupport: !this.full,
			showPageCorners: true,
			disableFlipByClick: false,
		});
		this.root.classList.add('is-booting'); // the book takes its place without sliding
		this.flip.loadFromHTML(pages);
		this.pages = pages;
		this.root.classList.add('is-ready');
		this.bar.hidden = false;
		this.totalEl.textContent = `/ ${this.count}`;
		this.flip.on('flip', () => this.onFlip());
		this.flip.on('changeOrientation', () => this.onFlip());
		new ResizeObserver(() => this.refit()).observe(this.stage);

		const start = this.full ? parseInt((location.hash.match(/p=(\d+)/) || [])[1] || '1', 10) - 1 : 0;
		if (start > 0 && start < this.count) this.flip.turnToPage(start);
		this.onFlip();
		this.reveal();
	}

	// The loading screen stays until the opening spread is drawn, then fades out over 0.6 s.
	async reveal() {
		const loading = this.root.querySelector('.pfb-loading');
		const until = performance.now() + 4000; // a slow page must not keep the book hidden
		while (performance.now() < until && !this.visible().every((i) => this.pages[i].classList.contains('is-drawn'))) {
			this.visible().forEach((i) => this.renderPage(i));
			await new Promise((r) => setTimeout(r, 50));
		}
		await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
		this.root.classList.remove('is-booting');
		if (!loading) return;
		loading.classList.add('is-out');
		setTimeout(() => loading.remove(), 650);
	}

	refit() {
		if (!this.flip) return;
		clearTimeout(this.fitTimer);
		this.fitTimer = setTimeout(() => {
			this.flip.update();
			this.center();
			this.renderAround();
		}, 80);
	}

	current() {
		return this.flip ? this.flip.getCurrentPageIndex() : 0;
	}

	visible() {
		const i = this.current();
		if (this.flip.getOrientation() === 'portrait' || i === 0 || i >= this.count - 1) return [i];
		return i % 2 === 1 ? [i, i + 1].filter((n) => n < this.count) : [i - 1, i];
	}

	onFlip() {
		const v = this.visible();
		this.input.value = v.length > 1 ? `${v[0] + 1}–${v[1] + 1}` : `${v[0] + 1}`;
		this.root.classList.toggle('at-start', v[0] === 0);
		this.root.classList.toggle('at-end', v[v.length - 1] >= this.count - 1);
		if (this.full) history.replaceState(null, '', v[0] > 0 ? `#p=${v[0] + 1}` : location.pathname + location.search);
		this.center(v);
		this.renderAround();
		this.markThumb();
	}

	// A closed book (the front cover alone on the right half, or the back cover alone on the left)
	// slides to the middle of the stage, as a real book lying shut would.
	center(v = this.visible()) {
		let shift = 0;
		if (!this.zoom && this.flip.getOrientation() === 'landscape' && v.length === 1) {
			const w = Math.min(this.bookEl.clientWidth / 2, this.bookEl.clientHeight * this.ratio) / 2;
			shift = v[0] === 0 ? -w : v[0] === this.count - 1 ? w : 0;
		}
		this.bookEl.style.transform = shift ? `translateX(${shift}px)` : '';
	}

	renderAround() {
		const v = this.visible();
		const from = Math.max(0, v[0] - AHEAD), to = Math.min(this.count - 1, v[v.length - 1] + AHEAD);
		for (let i = from; i <= to; i++) this.renderPage(i);
		// drop far canvases on long books so memory stays flat
		if (this.count > 40) {
			for (const [i] of this.rendered) {
				if (i < from - 8 || i > to + 8) {
					const c = this.pages[i].querySelector('canvas');
					c.width = c.height = 0;
					this.pages[i].classList.remove('is-drawn');
					this.rendered.delete(i);
				}
			}
		}
	}

	async renderPage(i) {
		const el = this.pages[i];
		const cssW = el.clientWidth || this.bookEl.clientWidth / 2;
		if (!cssW) return;
		const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
		let want = Math.round(cssW * dpr);
		want = Math.min(want, Math.round(MAX_CANVAS * Math.min(1, this.ratio)));
		if ((this.rendered.get(i) || 0) >= want * 0.92 || this.rendering.has(i)) return;
		const job = (async () => {
			const page = await this.pdf.getPage(i + 1);
			const vp1 = page.getViewport({ scale: 1 });
			const vp = page.getViewport({ scale: want / vp1.width });
			const off = document.createElement('canvas');
			off.width = Math.round(vp.width);
			off.height = Math.round(vp.height);
			await page.render({ canvasContext: off.getContext('2d', { alpha: false }), viewport: vp, background: '#ffffff' }).promise;
			const c = el.querySelector('canvas');
			c.width = off.width;
			c.height = off.height;
			c.getContext('2d').drawImage(off, 0, 0);
			el.classList.add('is-drawn');
			this.rendered.set(i, want);
		})();
		this.rendering.set(i, job);
		try {
			await job;
		} catch (e) {
			console.warn('Pursae Flipbook: page', i + 1, e);
		} finally {
			this.rendering.delete(i);
		}
	}

	go(i) {
		if (!this.flip || Number.isNaN(i)) return this.onFlip();
		i = Math.max(0, Math.min(this.count - 1, i));
		Math.abs(i - this.current()) > 2 ? this.flip.turnToPage(i) : this.flip.flip(i);
		if (Math.abs(i - this.current()) > 2) this.onFlip();
	}

	act(a) {
		if (!this.flip && a !== 'full') return;
		switch (a) {
			case 'prev': return this.flip.flipPrev('top');
			case 'next': return this.flip.flipNext('top');
			case 'first': this.flip.turnToPage(0); return this.onFlip();
			case 'last': this.flip.turnToPage(this.count - 1); return this.onFlip();
			case 'grid': return this.toggleThumbs();
			case 'close': this.thumbs.hidden = true; return;
			case 'zoomIn': return this.setZoom(this.zoom + 1);
			case 'zoomOut': return this.setZoom(this.zoom - 1);
			case 'share': return this.share();
			case 'full': return this.toggleFullscreen();
		}
	}

	setZoom(z) {
		z = Math.max(0, Math.min(ZOOMS.length - 1, z));
		if (z === this.zoom) return;
		const st = this.stage;
		const cx = (st.scrollLeft + st.clientWidth / 2) / (st.scrollWidth || 1);
		const cy = (st.scrollTop + st.clientHeight / 2) / (st.scrollHeight || 1);
		this.zoom = z;
		const f = ZOOMS[z];
		this.root.classList.toggle('is-zoomed', z > 0);
		this.bookEl.style.width = `${f * 100}%`;
		this.bookEl.style.height = `${f * 100}%`;
		this.flip.update();
		st.scrollLeft = cx * st.scrollWidth - st.clientWidth / 2;
		st.scrollTop = cy * st.scrollHeight - st.clientHeight / 2;
		this.renderAround();
	}

	toggleThumbs() {
		this.thumbs.hidden = !this.thumbs.hidden;
		if (this.thumbs.hidden) return;
		const grid = this.thumbs.querySelector('.pfb-thumbs-grid');
		if (!grid.childElementCount) {
			const io = new IntersectionObserver((entries) => {
				for (const en of entries) {
					if (!en.isIntersecting) continue;
					io.unobserve(en.target);
					this.drawThumb(en.target);
				}
			}, { root: grid, rootMargin: '200px' });
			for (let i = 0; i < this.count; i++) {
				const b = document.createElement('button');
				b.type = 'button';
				b.className = 'pfb-thumb';
				b.dataset.page = i;
				b.style.aspectRatio = String(this.ratio);
				b.innerHTML = `<canvas></canvas><span>${i + 1}</span>`;
				b.addEventListener('click', () => {
					this.thumbs.hidden = true;
					this.go(i);
				});
				grid.appendChild(b);
				io.observe(b);
			}
		}
		this.markThumb(true);
	}

	async drawThumb(b) {
		try {
			const page = await this.pdf.getPage(+b.dataset.page + 1);
			const vp1 = page.getViewport({ scale: 1 });
			const vp = page.getViewport({ scale: (200 * Math.min(window.devicePixelRatio || 1, 2)) / vp1.width / 1.4 });
			const c = b.querySelector('canvas');
			c.width = Math.round(vp.width);
			c.height = Math.round(vp.height);
			await page.render({ canvasContext: c.getContext('2d', { alpha: false }), viewport: vp, background: '#ffffff' }).promise;
			b.classList.add('is-drawn');
		} catch (e) { /* a thumbnail that fails stays blank */ }
	}

	markThumb(scroll) {
		if (this.thumbs.hidden) return;
		const v = this.visible();
		let cur;
		this.thumbs.querySelectorAll('.pfb-thumb').forEach((b) => {
			const on = v.includes(+b.dataset.page);
			b.classList.toggle('is-current', on);
			if (on && !cur) cur = b;
		});
		if (scroll && cur) cur.scrollIntoView({ block: 'nearest' });
	}

	async share() {
		const url = new URL(this.root.dataset.share, location.href);
		const v = this.visible();
		if (v[0] > 0) url.hash = `p=${v[0] + 1}`;
		try {
			await navigator.clipboard.writeText(url.href);
		} catch (e) {
			const ta = document.createElement('textarea');
			ta.value = url.href;
			ta.style.position = 'fixed';
			ta.style.opacity = '0';
			this.root.appendChild(ta);
			ta.select();
			document.execCommand('copy');
			ta.remove();
		}
		this.toast(t('copied'));
	}

	toast(msg) {
		const el = this.root.querySelector('.pfb-toast');
		el.textContent = msg;
		el.classList.add('is-on');
		clearTimeout(this.toastTimer);
		this.toastTimer = setTimeout(() => el.classList.remove('is-on'), 1800);
	}

	isFullscreen() {
		return (document.fullscreenElement || document.webkitFullscreenElement) === this.root;
	}

	toggleFullscreen() {
		if (this.isFullscreen()) {
			(document.exitFullscreen || document.webkitExitFullscreen).call(document);
			return;
		}
		const req = this.root.requestFullscreen || this.root.webkitRequestFullscreen;
		if (req) {
			req.call(this.root).catch?.(() => this.openFullPage());
		} else {
			this.openFullPage(); // iPhone Safari: no element full screen
		}
	}

	openFullPage() {
		const href = this.root.dataset.full;
		if (href) location.href = href;
	}

	onFullscreen() {
		const on = this.isFullscreen();
		this.root.classList.toggle('is-fullscreen', on);
		const b = this.root.querySelector('[data-act="full"]');
		if (b) {
			b.innerHTML = svg(on ? 'shrink' : 'expand');
			b.title = b.ariaLabel = on ? t('exitFull') : t('full');
		}
		if (on) this.root.focus({ preventScroll: true });
		this.refit();
	}
}

function escapeHTML(s) {
	return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
}
const escapeAttr = escapeHTML;

function boot() {
	document.querySelectorAll('[data-pfb]:not([data-pfb-on])').forEach((el) => {
		el.dataset.pfbOn = '1';
		new Viewer(el);
	});
}
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
else boot();
