/*
 * The Pursae mascot on the log-in screens (login.html "login-mascot"): the mark's eyes follow the
 * pointer, glance around when it rests, blink now and then, look away while a hidden password is
 * typed and peek when it is shown. The page only loads this for the stock mark (brand_mascot.go).
 *
 * Every change is an SVG transform attribute written from one requestAnimationFrame loop, which stops
 * when the eyes are still and while the tab is hidden. prefers-reduced-motion: the mark stays still.
 */
(function () {
	'use strict';

	var EYE_CY = 24.5;        // the eyes' centre in the mark's viewBox (0 0 64 64)
	var EYE_CX = 35.75;
	var MAX_X = 3, MAX_Y = 2.4; // eye travel, in viewBox units
	var FOLLOW_PX = 220;        // pointer distance at which the eyes reach full travel
	var POINTER_IDLE = 2500;    // ms without pointer moves before the eyes idle
	var TAU = 90;               // ms: easing time constant of the eyes
	var TAU_LID = 70;           // ms: easing of closing / opening (not blinks)
	var BLINK_MS = 150;

	var reduce = window.matchMedia ? window.matchMedia('(prefers-reduced-motion: reduce)') : null;

	function init() {
		var marks = [];
		var svgs = document.querySelectorAll('svg.pursae-mascot');
		for (var i = 0; i < svgs.length; i++) {
			var g = svgs[i].querySelector('.pursae-mascot-eyes');
			var eyes = svgs[i].querySelectorAll('.pursae-mascot-eye');
			if (g && eyes.length === 2) {
				marks.push({ svg: svgs[i], g: g, eyes: eyes, cx: 0, cy: 0, scale: 0, visible: false });
			}
		}
		if (!marks.length) return;

		// Fields that started as passwords stay "password fields" when the show button makes them text.
		var pwFields = [];
		var inputs = document.querySelectorAll('input[type=password]');
		for (var k = 0; k < inputs.length; k++) pwFields.push(inputs[k]);

		var cur = { x: 0, y: 0, lid: [1, 1] };          // eased state
		var tgt = { x: 0, y: 0, lid: [1, 1] };          // where the state is heading
		var blink = { start: -1, double: false };
		var pointer = null, lastPointer = -1e9, lastType = -1e9;
		var glance = { x: 0, y: 0, until: 0 };
		var rectsDirty = true, raf = 0, lastT = 0;
		var blinkTimer = 0, glanceTimer = 0, running = false;
		var measureCtx = null, fieldFont = '', fieldPad = 0;

		function now() { return window.performance ? performance.now() : Date.now(); }
		function rand(a, b) { return a + Math.random() * (b - a); }

		function readRects() {
			for (var i = 0; i < marks.length; i++) {
				var m = marks[i], r = m.svg.getBoundingClientRect();
				m.visible = r.width > 0 && r.height > 0;
				m.scale = r.width / 64;
				m.cx = r.left + EYE_CX * m.scale;
				m.cy = r.top + EYE_CY * m.scale;
			}
			rectsDirty = false;
		}

		function isPw(el) { return pwFields.indexOf(el) >= 0; }

		// The password field the focus is on: the field itself or its show/hide button.
		function focusedPw() {
			var el = document.activeElement;
			if (!el) return null;
			if (isPw(el)) return el;
			if (el.classList && el.classList.contains('wp-hide-pw')) {
				var wrap = el.closest('.wp-pwd');
				var f = wrap && wrap.querySelector('input');
				if (f && isPw(f)) return f;
			}
			return null;
		}

		function focusedText() {
			var el = document.activeElement;
			if (!el || el.tagName !== 'INPUT' || isPw(el)) return null;
			return /^(text|email|search|tel|url|)$/.test(el.type) ? el : null;
		}

		// The caret of a text field, in client coordinates.
		function caretPoint(el) {
			var r = el.getBoundingClientRect();
			var x = r.left + r.width / 2;
			try {
				if (!measureCtx) measureCtx = document.createElement('canvas').getContext('2d');
				var pos = el.selectionStart == null ? el.value.length : el.selectionStart;
				measureCtx.font = fieldFont;
				x = r.left + fieldPad + measureCtx.measureText(el.value.slice(0, pos)).width - el.scrollLeft;
				x = Math.min(Math.max(x, r.left), r.right);
			} catch (e) { /* keep the field's centre */ }
			return { x: x, y: r.top + r.height / 2 };
		}

		// Offset of the eyes (viewBox units) looking at a client point, from the first visible mark.
		function lookAt(p) {
			for (var i = 0; i < marks.length; i++) {
				var m = marks[i];
				if (!m.visible) continue;
				var dx = p.x - m.cx, dy = p.y - m.cy, d = Math.sqrt(dx * dx + dy * dy);
				if (d < 0.5) return { x: 0, y: 0 };
				var f = Math.min(1, d / FOLLOW_PX);
				return { x: dx / d * MAX_X * f, y: dy / d * MAX_Y * f };
			}
			return { x: 0, y: 0 };
		}

		// Decide where the eyes go and how open each one is.
		function aim(t) {
			var pw = focusedPw(), field = focusedText();
			tgt.lid[0] = tgt.lid[1] = 1;
			if (pw && pw.type === 'password') { // privacy: look away, eyes shut
				tgt.x = -MAX_X * 0.8; tgt.y = -MAX_Y * 0.7;
				tgt.lid[0] = tgt.lid[1] = 0.1;
				return;
			}
			if (pw) { // the password is shown: one eye peeks at it
				var p = lookAt(caretPoint(pw));
				tgt.x = p.x; tgt.y = p.y;
				tgt.lid[0] = 0.15;
				return;
			}
			var pointerActive = pointer && t - lastPointer < POINTER_IDLE;
			if (field && (t - lastType < POINTER_IDLE || !pointerActive)) {
				var c = lookAt(caretPoint(field));
				tgt.x = c.x; tgt.y = c.y;
				return;
			}
			if (pointerActive) {
				var q = lookAt(pointer);
				tgt.x = q.x; tgt.y = q.y;
				return;
			}
			if (t < glance.until) { tgt.x = glance.x; tgt.y = glance.y; } else { tgt.x = 0; tgt.y = 0; }
		}

		function blinkFactor(t) {
			if (blink.start < 0) return 1;
			var e = t - blink.start, len = blink.double ? BLINK_MS * 2 + 60 : BLINK_MS;
			if (e >= len) { blink.start = -1; return 1; }
			if (blink.double && e > BLINK_MS) e = Math.max(0, e - BLINK_MS - 60);
			var u = Math.min(1, e / BLINK_MS);           // 0..1 over one blink
			return 0.08 + 0.92 * Math.abs(1 - 2 * u);    // shut at the middle
		}

		function write(bf) {
			var tr = 'translate(' + cur.x.toFixed(2) + ' ' + cur.y.toFixed(2) + ')';
			for (var i = 0; i < marks.length; i++) {
				var m = marks[i];
				m.g.setAttribute('transform', tr);
				for (var j = 0; j < 2; j++) {
					var s = Math.max(0.05, cur.lid[j] * bf);
					m.eyes[j].setAttribute('transform', 'matrix(1 0 0 ' + s.toFixed(3) + ' 0 ' + (EYE_CY * (1 - s)).toFixed(3) + ')');
				}
			}
		}

		function frame(t) {
			raf = 0;
			if (!running) return;
			if (rectsDirty) readRects();   // reads first, writes after: no forced layout
			aim(t);
			var dt = Math.min(64, lastT ? t - lastT : 16);
			lastT = t;
			var a = 1 - Math.exp(-dt / TAU), al = 1 - Math.exp(-dt / TAU_LID);
			cur.x += (tgt.x - cur.x) * a;
			cur.y += (tgt.y - cur.y) * a;
			cur.lid[0] += (tgt.lid[0] - cur.lid[0]) * al;
			cur.lid[1] += (tgt.lid[1] - cur.lid[1]) * al;
			var bf = blinkFactor(t);
			write(bf);
			var still = Math.abs(tgt.x - cur.x) < 0.01 && Math.abs(tgt.y - cur.y) < 0.01 &&
				Math.abs(tgt.lid[0] - cur.lid[0]) < 0.005 && Math.abs(tgt.lid[1] - cur.lid[1]) < 0.005 && blink.start < 0;
			// Keep going while moving, blinking, or while a pointer/glance deadline may change the aim.
			if (!still || (pointer && t - lastPointer < POINTER_IDLE + 50) || (glance.until && t < glance.until + 50)) kick();
			else lastT = 0;
		}

		function kick() { if (running && !raf) raf = requestAnimationFrame(frame); }

		function scheduleBlink() {
			clearTimeout(blinkTimer);
			blinkTimer = setTimeout(function () {
				if (focusedPw() && focusedPw().type === 'password') { scheduleBlink(); return; } // eyes already shut
				blink.start = now();
				blink.double = Math.random() < 0.2;
				kick();
				scheduleBlink();
			}, rand(3000, 6000));
		}

		var GLANCES = [[-1, 0], [1, 0], [0, -1], [0, 1], [-0.8, -0.7], [0.8, -0.7], [-0.8, 0.6], [0.8, 0.6]];
		function scheduleGlance() {
			clearTimeout(glanceTimer);
			glanceTimer = setTimeout(function () {
				var t = now();
				if (!(pointer && t - lastPointer < POINTER_IDLE) && !focusedPw()) {
					var d = GLANCES[Math.floor(Math.random() * GLANCES.length)];
					glance.x = d[0] * MAX_X * 0.9;
					glance.y = d[1] * MAX_Y * 0.9;
					glance.until = t + rand(600, 1300);
					kick();
				}
				scheduleGlance();
			}, rand(2000, 5000));
		}

		function start() {
			if (running) return;
			running = true;
			rectsDirty = true;
			lastT = 0;
			scheduleBlink();
			scheduleGlance();
			kick();
		}

		function stop() {
			running = false;
			clearTimeout(blinkTimer);
			clearTimeout(glanceTimer);
			if (raf) cancelAnimationFrame(raf);
			raf = 0;
		}

		function reset() { // reduced motion: the stock pose
			cur.x = cur.y = 0; cur.lid = [1, 1];
			for (var i = 0; i < marks.length; i++) {
				marks[i].g.removeAttribute('transform');
				marks[i].eyes[0].removeAttribute('transform');
				marks[i].eyes[1].removeAttribute('transform');
			}
		}

		function update() {
			if ((reduce && reduce.matches) || document.hidden) {
				stop();
				if (reduce && reduce.matches) reset();
			} else {
				start();
			}
		}

		document.addEventListener('pointermove', function (e) {
			if (e.pointerType === 'touch') return;
			pointer = { x: e.clientX, y: e.clientY };
			lastPointer = now();
			kick();
		}, { passive: true });
		document.documentElement.addEventListener('mouseleave', function () { lastPointer = -1e9; kick(); });
		window.addEventListener('resize', function () { rectsDirty = true; kick(); }, { passive: true });
		window.addEventListener('scroll', function () { rectsDirty = true; kick(); }, { passive: true });
		document.addEventListener('focusin', function (e) {
			var el = e.target;
			if (el && el.tagName === 'INPUT') { // the field's font, for caretPoint
				var cs = getComputedStyle(el);
				fieldFont = cs.fontStyle + ' ' + cs.fontWeight + ' ' + cs.fontSize + ' ' + cs.fontFamily;
				fieldPad = (parseFloat(cs.paddingLeft) || 0) + (parseFloat(cs.borderLeftWidth) || 0);
			}
			kick();
		});
		document.addEventListener('focusout', function () { setTimeout(kick, 0); });
		document.addEventListener('input', function () { lastType = now(); kick(); });
		document.addEventListener('keyup', function () { lastType = now(); kick(); }); // caret moves
		document.addEventListener('click', function () { setTimeout(kick, 0); });  // show/hide password
		document.addEventListener('visibilitychange', update);
		if (reduce) {
			if (reduce.addEventListener) reduce.addEventListener('change', update);
			else if (reduce.addListener) reduce.addListener(update);
		}
		update();
	}

	if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
	else init();
})();
