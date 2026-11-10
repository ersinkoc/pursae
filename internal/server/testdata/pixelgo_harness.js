// Runs web/pixelgo/pixelgo.js against a minimal DOM stub and prints the observations as JSON.
// usage: node pixelgo_harness.js <path to pixelgo.js>
const fs = require('fs');
const src = fs.readFileSync(process.argv[2], 'utf8');
const out = { gtag: [], uet: [], posts: [], iframeSrc: '' };
const docL = {}, winL = {};
const mkFrame = (src) => {
  const f = { tagName: 'IFRAME', attrs: { src }, getAttribute(k) { return this.attrs[k] || ''; }, setAttribute(k, v) { this.attrs[k] = v; if (k === 'src') out.iframeSrc = v; },
    addEventListener() {}, contentWindow: { postMessage(m) { out.posts.push(m); } } };
  return f;
};
const frames = [mkFrame('https://www.youtube.com/embed/abcDEF12345'), mkFrame('https://player.vimeo.com/video/76979871')];
const cfg = { v: 1, rest: '', debug: false, map: {}, metaStandard: {}, consent: { require: false, cookie: 'x', cats: {} },
  platforms: { gads: { ids: ['AW-1'], labels: { Purchase: 'lbl' } }, bing: { ids: ['1', '2'] } },
  general: { video: true }, scroll: [], time: [], downloads: [], woo: {}, attr: { enabled: false, days: 1 }, events: [], page: {}, custom: [] };
const document = {
  cookie: '', title: 't', referrer: '', readyState: 'complete', documentElement: {},
  getElementById: (id) => id === 'pixelgo-config' ? { textContent: JSON.stringify(cfg) } : null,
  getElementsByTagName: (t) => t === 'iframe' ? frames : [],
  querySelectorAll: () => [], querySelector: () => null,
  addEventListener: (t, f) => { (docL[t] = docL[t] || []).push(f); }, dispatchEvent: () => true,
};
const window = { addEventListener: (t, f) => { (winL[t] = winL[t] || []).push(f); }, crypto: require('crypto').webcrypto, console,
  gtag: (...a) => out.gtag.push(a), pixelgoReady: { gads: 1, bing: 1 },
  pixelgoUET: [{ push: (...a) => out.uet.push(['a', ...a]) }, { push: (...a) => out.uet.push(['b', ...a]) }],
  localStorage: { getItem: () => null, setItem() {} }, sessionStorage: { getItem: () => null, setItem() {} } };
const location = { href: 'https://x.test/', pathname: '/', search: '', host: 'x.test', protocol: 'https:' };
class CustomEvent { constructor(n, o) { this.type = n; this.detail = o && o.detail; } }
new Function('window', 'document', 'location', 'CustomEvent', 'gtag', 'navigator', 'setTimeout', 'setInterval', 'clearInterval', src)(
  window, document, location, CustomEvent, window.gtag, { sendBeacon() {} }, () => 0, () => 0, () => {});
window.pixelgo.track('Purchase', { value: 5, currency: 'USD', order_id: '9' }, { user: { em: 'e1', ph: 'p1', fn: 'f1', ln: 'l1' } });
window.pixelgo.track('Lead', {});
const msg = (f, data) => (winL.message || []).forEach((h) => h({ source: f.contentWindow, data }));
msg(frames[0], JSON.stringify({ event: 'onStateChange', info: 1 }));
msg(frames[1], JSON.stringify({ event: 'play' }));
out.videoEvents = [];
document.addEventListener = document.addEventListener;
console.log(JSON.stringify(out));
