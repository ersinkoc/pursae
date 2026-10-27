package server

import "strings"

// gweSplash is Golementor's loading screen. It covers upstream's #elementor-loading (which has no
// logo in the v4 shell and fades out the moment the editor is ready) with a dark screen, the
// bobbing gopher and a few rotating lines, kept on screen for a short minimum so the load reads as
// a moment rather than a flash. It leaves when upstream's loader is gone and the minimum has passed.
const gweSplash = `<div id="gwe-splash" role="status" aria-live="polite">
<style>
#elementor-loading{background:#191919 !important}
#elementor-loading .elementor-loader-wrapper{visibility:hidden}
#gwe-splash{position:fixed;inset:0;z-index:2147483000;display:flex;align-items:center;justify-content:center;background:#191919;
	font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Inter",Roboto,"Helvetica Neue",Arial,sans-serif;color:#c9c9c9;
	transition:opacity .35s ease,visibility .35s ease}
#gwe-splash.is-done{opacity:0;visibility:hidden;pointer-events:none}
#gwe-splash .gwe-splash-box{display:flex;flex-direction:column;align-items:center;gap:14px;padding:28px 36px;border:1px solid #303030;background:#1e1e1e;min-width:260px}
#gwe-splash .gwe-splash-logo{width:72px;height:72px;animation:gwe-splash-hop 1.1s cubic-bezier(.3,0,.3,1) infinite;transform-origin:50% 100%}
#gwe-splash .gwe-splash-shadow{width:44px;height:6px;margin-top:-10px;border-radius:50%;background:rgba(0,0,0,.45);animation:gwe-splash-shadow 1.1s cubic-bezier(.3,0,.3,1) infinite}
#gwe-splash .gwe-splash-name{font-size:15px;font-weight:600;color:#f2f2f2;letter-spacing:.01em}
#gwe-splash .gwe-splash-msg{font-size:12.5px;color:#9a9a9a;min-height:1.4em;transition:opacity .2s ease}
#gwe-splash .gwe-splash-bar{width:180px;height:3px;background:#303030;overflow:hidden}
#gwe-splash .gwe-splash-fill{display:block;height:100%;width:0;background:#2a73b5;transition:width .4s ease}
@keyframes gwe-splash-hop{0%,100%{transform:translateY(0) scale(1,1)}10%{transform:translateY(0) scale(1.08,.92)}45%{transform:translateY(-16px) scale(.96,1.04)}80%{transform:translateY(0) scale(1.04,.96)}}
@keyframes gwe-splash-shadow{0%,100%{transform:scale(1);opacity:.9}45%{transform:scale(.6);opacity:.4}}
@media (prefers-reduced-motion:reduce){#gwe-splash .gwe-splash-logo,#gwe-splash .gwe-splash-shadow{animation:none}}
</style>
<div class="gwe-splash-box">
<img class="gwe-splash-logo" src="@@ASSETS@@images/gopher.png" alt="" width="72" height="72">
<span class="gwe-splash-shadow" aria-hidden="true"></span>
<div class="gwe-splash-name">Golementor</div>
<div class="gwe-splash-msg">Waking up the gophers…</div>
<div class="gwe-splash-bar" aria-hidden="true"><span class="gwe-splash-fill"></span></div>
</div>
<script>
(function () {
	var splash = document.getElementById('gwe-splash');
	var msg = splash.querySelector('.gwe-splash-msg');
	var fill = splash.querySelector('.gwe-splash-fill');
	var lines = ['Waking up the gophers…', 'Stacking containers…', 'Lining up the widgets…', 'Polishing pixels…', 'Almost there…'];
	var start = Date.now(), minimum = 1400, i = 0, progress = 8;
	fill.style.width = progress + '%';
	var rotate = setInterval(function () {
		i = Math.min(i + 1, lines.length - 1);
		msg.style.opacity = 0;
		setTimeout(function () { msg.textContent = lines[i]; msg.style.opacity = 1; }, 180);
	}, 900);
	var creep = setInterval(function () { progress += (90 - progress) * 0.12; fill.style.width = progress + '%'; }, 200);
	function upstreamDone() {
		var el = document.getElementById('elementor-loading');
		return !el || el.style.display === 'none' || getComputedStyle(el).display === 'none';
	}
	function finish() {
		clearInterval(rotate); clearInterval(creep);
		fill.style.width = '100%';
		msg.textContent = 'Ready!';
		setTimeout(function () {
			splash.classList.add('is-done');
			setTimeout(function () { splash.remove(); }, 400);
		}, 250);
	}
	var watch = setInterval(function () {
		if (upstreamDone() && Date.now() - start >= minimum) { clearInterval(watch); finish(); }
	}, 100);
	// Never trap the user behind the splash if the editor fails to boot.
	setTimeout(function () { clearInterval(watch); finish(); }, 20000);
})();
</script>
</div>
`

// gweWithSplash puts the splash right after <body> so it shows before the editor's scripts load.
func gweWithSplash(page string) string {
	i := strings.Index(page, "<body")
	if i < 0 {
		return page
	}
	j := strings.Index(page[i:], ">")
	if j < 0 {
		return page
	}
	at := i + j + 1
	return page[:at] + gweSplash + page[at:]
}
