#!/usr/bin/env node
// Editor ↔ published parity (EDITOR-CANLI plan, K10): the fixture design in ./fixture is uploaded as a
// design kit and imported as a code page through the MCP route an agent uses (web_builder_kit_upload,
// then web_builder_import_site or web_builder_import_html method "code"), published, and then shot
// (a) on the published page and (b) in the Web Builder editor's canvas frame, at 1440 / 768 / 390 px,
// in Safe mode (static), in Live mode answered "This session only" (live) and "Always for this design"
// (always: reopening the editor must be Live with no question; the trust is revoked again after it).
// Clicks on the design go through Interact mode (--clicks interact, default; --clicks edit: plain
// canvas clicks, which select elements). Per viewport and mode: the share
// of differing pixels (top of the page, a scrolled position, dark theme) and functional checks (the
// canvas scene drew, the theme toggle works and is remembered, script-drawn text, the JSON fetch, the
// module + import map, the web font, 100vh, the fixed header). Output: report.json and report.html in
// --out (default builder/.work/editor-parity).
//
//   node builder/test/editor-parity/parity.mjs --site http://127.0.0.1:8191 --user admin --pass '…'
//     [--app-password 'xxxx xxxx …'] [--out dir] [--widths 1440,768,390] [--modes static,live,always] [--clicks interact|edit]
//     [--import site|html] [--kit editor-parity] [--csp keep|report|enforce|off] [--target 0.5] [--skip-import]
//     [--setup]   (fresh site: activate Pursae Web Builder and the Hello Pursae theme first)
//
// Environment instead of flags: PARITY_SITE, PARITY_USER, PARITY_PASS, PARITY_APP_PASSWORD;
// PW_CHROMIUM: path of a Chromium / headless shell binary (else Playwright's own; same variable for
// builder/test/live/live-frame.mjs and builder/test/security/editor-xss.mjs).
// Exit status: 0 when every Live shot (live, always) is at or under --target % and every check passes,
// 1 otherwise, 2 on a usage error. Safe mode (static) is reported but never fails the run.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from '@playwright/test'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '..', '..', '..')
const args = parseArgs(process.argv.slice(2))
const SITE = (args.site || process.env.PARITY_SITE || '').replace(/\/+$/, '')
const USER = args.user || process.env.PARITY_USER
const PASS = args.pass || process.env.PARITY_PASS
if (!SITE || !USER || !PASS) {
  console.error('usage: parity.mjs --site <url> --user <admin login> --pass <password> [--app-password …] [--out dir] [--widths 1440,768,390] [--modes static,live] [--import site|html] [--csp keep|report|enforce|off]')
  process.exit(2)
}
let madeAppUUID = ''
let APP = args['app-password'] || process.env.PARITY_APP_PASSWORD || ''
const OUT = path.resolve(args.out || path.join(repo, 'builder', '.work', 'editor-parity'))
const WIDTHS = (args.widths || '1440,768,390').split(',').map(Number)
const MODES = (args.modes || 'static,live,always').split(',')
const CLICKS = args.clicks || 'interact'
const IMPORT = args.import || 'site'
const KIT = args.kit || 'editor-parity'
const CSP = args.csp || 'keep'
const TARGET = Number(args.target ?? 0.5)
const HEIGHT = 900
/** The viewport height of each device (live.ts LIVE_VIEWPORT: the editor's frame is the device's viewport). */
const VIEW_H = { 1440: 900, 768: 1024, 390: 844 }
const heightOf = (width) => VIEW_H[width] ?? HEIGHT
const DEVICE = { 1440: 'desktop', 768: 'tablet', 390: 'mobile' }
const DEVICE_LABELS = { desktop: ['Desktop', 'Masaüstü'], tablet: ['Tablet'], mobile: ['Mobile', 'Mobil'] }
const FIXTURE = path.join(here, 'fixture')
// The kit's web font: an OFL face from the theme library, so the fixture itself stays a few KB.
const FONT = path.join(repo, 'library', 'themes', 'archform', 'assets', 'fonts', 'instrument-serif.woff2')
const log = (...a) => console.log(...a)
fs.mkdirSync(path.join(OUT, 'img'), { recursive: true })

function parseArgs(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (!a.startsWith('--')) continue
    const [k, v] = a.slice(2).split('=', 2)
    out[k] = v !== undefined ? v : argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[++i] : 'true'
  }
  return out
}

/* ---------- browser ---------- */

// Deterministic rasterization as fidelity.mjs; WebGL through SwiftShader (no GPU).
// PW_CHROMIUM: a Chromium binary to use when Playwright's own build is not installed.
const browser = await chromium.launch({
  executablePath: process.env.PW_CHROMIUM || undefined,
  args: ['--disable-lcd-text', '--force-color-profile=srgb', '--disable-gpu', '--font-render-hinting=none', '--disable-partial-raster', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--hide-scrollbars'],
})
const FREEZE = '*,*::before,*::after{transition:none!important;animation:none!important;caret-color:transparent!important;scroll-behavior:auto!important}'

/* ---------- log in, app password, CSP mode ---------- */

const admin = await browser.newContext({ viewport: { width: 1440, height: HEIGHT } })
const ap = await admin.newPage()
await ap.goto(`${SITE}/wp-login`)
await ap.locator('#user_login').fill(USER)
await ap.locator('#user_pass').fill(PASS)
await Promise.all([ap.waitForURL(/\/wp-admin\/?/), ap.locator('#wp-submit').click()])
const restNonce = async () => (await ap.evaluate(async (s) => (await fetch(`${s}/wp-admin/admin-ajax?action=rest-nonce`, { credentials: 'same-origin' })).text(), SITE)).trim()
if (!APP) {
  // A throwaway Application Password for the MCP calls (Basic auth, as an MCP client sends).
  const nonce = await restNonce()
  const r = await ap.evaluate(
    async ([s, n]) => {
      const res = await fetch(`${s}/wp-json/wp/v2/users/me/application-passwords`, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-WP-Nonce': n }, body: JSON.stringify({ name: 'editor-parity ' + Date.now(), full_access: true }) })
      return { status: res.status, body: await res.text() }
    },
    [SITE, nonce],
  )
  if (r.status >= 300) throw new Error(`could not create an Application Password: HTTP ${r.status} ${r.body.slice(0, 300)}`)
  const made = JSON.parse(r.body)
  APP = made.password
  madeAppUUID = made.uuid
}
if (CSP !== 'keep') {
  await ap.goto(`${SITE}/wp-admin/options-security`)
  const field = ap.locator(`[name=csp_mode]`)
  if ((await field.first().getAttribute('type')) === 'radio') await ap.locator(`[name=csp_mode][value=${CSP}]`).check()
  else await field.selectOption(CSP)
  await Promise.all([ap.waitForLoadState('load'), ap.locator('form [type=submit]').first().click()])
  log(`csp_mode set to ${CSP}`)
}
const cspNow = await ap.evaluate(async (s) => {
  const r = await fetch(`${s}/wp-admin/options-security`, { credentials: 'same-origin' })
  const d = new DOMParser().parseFromString(await r.text(), 'text/html')
  const el = d.querySelector('[name=csp_mode]:checked') || d.querySelector('select[name=csp_mode]')
  return el ? el.value : 'unknown'
}, SITE)

/* ---------- MCP: kit upload and import ---------- */

let mcpSession = ''
let mcpId = 0
async function mcp(method, params) {
  const headers = { 'Content-Type': 'application/json', Accept: 'application/json', Authorization: 'Basic ' + Buffer.from(`${USER}:${APP}`).toString('base64') }
  if (mcpSession) headers['Mcp-Session-Id'] = mcpSession
  const res = await fetch(`${SITE}/wp-json/pursae/v1/mcp`, { method: 'POST', headers, body: JSON.stringify({ jsonrpc: '2.0', id: ++mcpId, method, params }) })
  if (!mcpSession && res.headers.get('mcp-session-id')) mcpSession = res.headers.get('mcp-session-id')
  const j = await res.json()
  if (j.error) throw new Error(`${method}: ${j.error.message}`)
  return j.result
}
async function tool(name, a) {
  const r = await mcp('tools/call', { name, arguments: a })
  const text = (r.content || []).map((c) => c.text).join('\n')
  if (r.isError) throw new Error(`${name}: ${text}`)
  return r.structuredContent ?? JSON.parse(text)
}

function walk(dir, rel = '') {
  const out = []
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const r = rel ? `${rel}/${e.name}` : e.name
    if (e.isDirectory()) out.push(...walk(path.join(dir, e.name), r))
    else out.push(r)
  }
  return out.sort()
}

const PAGE_PATH = 'parity/index.html'
const pageHTML = fs.readFileSync(path.join(FIXTURE, PAGE_PATH), 'utf8')
let pageURL = ''
let postId = 0
let importAnswer = null
if (!args['skip-import']) {
  await mcp('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'editor-parity', version: '1' } })
  if (args.setup) {
    // A fresh site: Pursae Web Builder (plugin slug golementor) on, Hello Pursae active (site import needs it).
    // Through the REST API as the logged-in administrator (the MCP's plugin and theme tools are off by default).
    const nonce = await restNonce()
    for (const route of ['/pursae/v1/plugins/golementor/activate', '/pursae/v1/themes/hello-pursae/activate']) {
      const r = await ap.evaluate(
        async ([s, n, route]) => {
          const res = await fetch(`${s}/wp-json${route}`, { method: 'POST', credentials: 'same-origin', headers: { 'X-WP-Nonce': n } })
          return `${res.status} ${(await res.text()).slice(0, 160)}`
        },
        [SITE, nonce, route],
      )
      log(`${route}: ${r}`)
    }
  }
  const files = walk(FIXTURE)
    .filter((f) => !f.endsWith('.html'))
    .map((f) => ({ path: f, text: fs.readFileSync(path.join(FIXTURE, f), 'utf8') }))
  files.push({ path: 'parity/fonts/display.woff2', data: fs.readFileSync(FONT).toString('base64') })
  const up = await tool('web_builder_kit_upload', { kit: KIT, name: 'Editor parity fixture', files, fresh: true, commit: true })
  log(`kit ${KIT}: ${files.length} files, version ${up?.kit?.version ?? '?'}`)
  if (IMPORT === 'site') {
    importAnswer = await tool('web_builder_import_site', { kit: KIT, pages: [{ path: PAGE_PATH, html: pageHTML }], status: 'publish', front_page: false, menus: false })
  } else {
    importAnswer = await tool('web_builder_import_html', { html: pageHTML, method: 'code', kit: KIT, path: PAGE_PATH, title: 'Editor parity', status: 'publish' })
  }
  fs.writeFileSync(path.join(OUT, 'import.json'), JSON.stringify(importAnswer, null, 2))
}
{
  // The page: from the import's answer, else the newest page titled like the fixture.
  const a = importAnswer || {}
  const page = (a.pages || []).find((p) => (p.path || p.file) === PAGE_PATH) || a
  postId = Number(page.post_id || page.id || args['post-id'] || 0)
  pageURL = page.url || page.link || ''
  if (!postId || !pageURL) {
    const nonce = await restNonce()
    const r = await ap.evaluate(
      async ([s, n]) => (await fetch(`${s}/wp-json/wp/v2/pages?search=Editor%20parity&orderby=date&order=desc&status=publish`, { credentials: 'same-origin', headers: { 'X-WP-Nonce': n } })).json(),
      [SITE, nonce],
    )
    if (!Array.isArray(r) || !r.length) throw new Error('could not find the imported page; see import.json')
    postId ||= r[0].id
    pageURL ||= r[0].link
  }
  if (pageURL.startsWith('/')) pageURL = SITE + pageURL
}
const editURL = `${SITE}/wp-admin/post?post=${postId}&action=web-builder`
log(`page ${postId}: ${pageURL}\neditor: ${editURL}\ncsp_mode: ${cspNow}`)
const storage = await admin.storageState()

/* ---------- what is checked (in the page, or in the editor's frame from its parent) ---------- */

// Runs with the document and window of the design (published page or editor frame). Plain
// function: serialized into a string expression (the editor page's CSP does not apply to CDP).
function inspect(doc, win) {
  const html = doc.documentElement
  const $ = (s) => doc.querySelector(s)
  const txt = (s) => ($(s)?.textContent ?? '').trim()
  const canvas = $('#scene')
  let painted = 0
  let sample = 0
  if (canvas && canvas.width && canvas.height) {
    try {
      const c = doc.createElement('canvas')
      c.width = 64
      c.height = 64
      const g = c.getContext('2d')
      g.drawImage(canvas, 0, 0, 64, 64)
      const d = g.getImageData(0, 0, 64, 64).data
      const bg = [d[0], d[1], d[2]]
      for (let i = 0; i < d.length; i += 4) {
        sample++
        if (d[i + 3] && Math.abs(d[i] - bg[0]) + Math.abs(d[i + 1] - bg[1]) + Math.abs(d[i + 2] - bg[2]) > 30) painted++
      }
    } catch (e) {
      painted = -1
    }
  }
  const hero = $('#hero')
  const header = $('.site-header')
  const cs = (el) => (el ? win.getComputedStyle(el) : null)
  const reveals = Array.from(doc.querySelectorAll('.reveal'))
  return {
    js: html.getAttribute('data-js') === '1',
    module: html.getAttribute('data-module') === 'ok',
    gl: html.getAttribute('data-gl') || 'none',
    asset: html.getAttribute('data-asset') || 'none',
    json: html.getAttribute('data-json') || 'none',
    scene: html.getAttribute('data-scene') || '',
    canvas: canvas ? { css: [Math.round(canvas.clientWidth), Math.round(canvas.clientHeight)], backing: [canvas.width, canvas.height], painted: sample ? Math.round((painted / sample) * 1000) / 10 : painted } : null,
    dpr: win.devicePixelRatio,
    viewport: [win.innerWidth, win.innerHeight],
    heroHeight: hero ? Math.round(hero.getBoundingClientRect().height) : null,
    header: header ? { position: cs(header).position, top: Math.round(header.getBoundingClientRect().top) } : null,
    dark: html.classList.contains('dark'),
    stored: (() => {
      try {
        return win.localStorage.getItem('parity-theme')
      } catch (e) {
        return 'error: ' + e.message
      }
    })(),
    toggleLabel: txt('#theme-toggle'),
    counter: txt('#counter'),
    activeSlides: doc.querySelectorAll('.slide.active').length,
    activeIndex: Array.from(doc.querySelectorAll('.slide')).findIndex((x) => x.classList.contains('active')),
    visibleSlides: Array.from(doc.querySelectorAll('.slide')).filter((s) => cs(s).display !== 'none').length,
    dots: doc.querySelectorAll('#dots .dot').length,
    items: doc.querySelectorAll('#items li').length,
    itemsStatus: txt('#items-status'),
    revealed: reveals.filter((r) => r.classList.contains('visible')).length + '/' + reveals.length,
    revealOpacity: reveals.map((r) => cs(r).opacity).join(','),
    font: doc.fonts ? doc.fonts.check('20px ParityDisplay') && Array.from(doc.fonts).some((f) => f.family.replace(/"/g, '') === 'ParityDisplay' && f.status === 'loaded') : false,
    h1Font: cs($('h1'))?.fontFamily ?? '',
    baseURI: doc.baseURI,
    location: String(win.location.href),
    scriptsTotal: doc.querySelectorAll('script').length,
    scriptsInert: doc.querySelectorAll('script[type="text/x-wb-inert"]').length,
    nonceOnScripts: Array.from(doc.querySelectorAll('script')).filter((s) => s.nonce || s.getAttribute('nonce')).length,
    importmap: !!doc.querySelector('script[type=importmap]'),
    emptyOutlined: Array.from(doc.querySelectorAll('[data-wbc]:empty')).filter((e) => cs(e).outlineStyle === 'dashed').map((e) => e.id || e.className || e.tagName.toLowerCase()),
    scrollHeight: doc.documentElement.scrollHeight,
  }
}
const INSPECT = `(${inspect.toString()})`
const FRAME = 'iframe[data-wb-frame]'
const inFrame = (expr) => `(() => { const f = document.querySelector(${JSON.stringify(FRAME)}); if (!f || !f.contentDocument) return null; const doc = f.contentDocument, win = f.contentWindow; return ${expr} })()`

/** Expected values on the published page (and in an editor that is 1:1). */
function checksOf(r, width) {
  if (!r) return { frame: false }
  return {
    scriptsRan: r.js,
    moduleAndImportmap: r.module,
    sceneDrawn: r.canvas ? r.canvas.painted > 1 : false,
    sceneSizedToContainer: r.canvas ? Math.abs(r.canvas.backing[0] - Math.floor(r.canvas.css[0] * r.dpr)) <= 1 && Math.abs(r.canvas.backing[1] - Math.floor(r.canvas.css[1] * r.dpr)) <= 1 : false,
    moduleRelativeAsset: r.asset === 'ok',
    jsonFetch: r.json === 'ok' && r.items === 3,
    counterDrawn: r.counter === '42',
    sliderSlide1: r.activeSlides === 1 && r.visibleSlides === 1 && r.dots === 3,
    revealVisible: /^(\d+)\/\1$/.test(r.revealed) && r.revealed !== '0/0',
    webFont: !!r.font,
    hero100vh: r.heroHeight === Math.max(420, r.viewport[1]),
    fixedHeader: r.header?.position === 'fixed' && r.header.top === 0,
    viewportWidth: r.viewport[0] === width,
  }
}

/* ---------- shooting ---------- */

async function settleDoc(page, scope) {
  // scope: '' for the page itself, else an inFrame() wrapper.
  const ev = (expr) => page.evaluate(scope ? inFrame(expr) : `(() => { const doc = document, win = window; return ${expr} })()`)
  await ev(`(() => { const s = doc.createElement('style'); s.setAttribute('data-parity-freeze', ''); s.textContent = ${JSON.stringify(FREEZE)}; (doc.head || doc.documentElement).appendChild(s); return 1 })()`)
  await ev(`Promise.all(Array.from(doc.fonts, (f) => f.load().catch(() => 0))).then(() => doc.fonts.ready).then(() => 1)`)
  const h = await ev(`[doc.documentElement.scrollHeight, win.innerHeight]`)
  if (h) for (let y = 0; y < h[0]; y += Math.round(h[1] * 0.6)) {
    await ev(`(win.scrollTo(0, ${y}), 1)`)
    await page.waitForTimeout(40)
  }
  await ev(`(win.scrollTo(0, 0), 1)`)
  // Wait for the page's own async work (module, JSON) to land: bounded, then go on.
  for (let i = 0; i < 30; i++) {
    const done = await ev(`!doc.documentElement.hasAttribute('data-js') || ((doc.documentElement.getAttribute('data-json') || '') !== '' && (doc.documentElement.getAttribute('data-asset') || doc.documentElement.getAttribute('data-module') === null))`)
    if (done) break
    await page.waitForTimeout(100)
  }
  await page.waitForTimeout(200)
  return ev
}

const scrollToSlider = `(win.scrollTo(0, doc.getElementById('slider').getBoundingClientRect().top + win.scrollY - 64), 1)`

function watch(page) {
  const out = []
  page.on('pageerror', (e) => out.push('exception: ' + String(e.message || e).slice(0, 300)))
  page.on('console', (m) => (m.type() === 'error' || m.type() === 'warning') && out.push(`console.${m.type()}: ${m.text().slice(0, 300)}`))
  page.on('requestfailed', (r) => out.push(`request failed: ${r.url().slice(0, 200)} (${r.failure()?.errorText})`))
  page.on('response', (r) => r.status() >= 400 && out.push(`HTTP ${r.status()}: ${r.url().slice(0, 200)}`))
  return out
}

async function published(width) {
  const ctx = await browser.newContext({ viewport: { width, height: heightOf(width) }, deviceScaleFactor: 1, reducedMotion: 'reduce', serviceWorkers: 'block' })
  const page = await ctx.newPage()
  const problems = watch(page)
  await page.goto(pageURL, { waitUntil: 'load' })
  const ev = await settleDoc(page, '')
  const shots = {}
  const info = await ev(`${INSPECT}(doc, win)`)
  shots.top = await page.screenshot({ animations: 'disabled', caret: 'hide' })
  await ev(scrollToSlider)
  await page.waitForTimeout(100)
  shots.scrolled = await page.screenshot({ animations: 'disabled', caret: 'hide' })
  await ev(`(win.scrollTo(0, 0), 1)`)
  await page.locator('#theme-toggle').click()
  await page.waitForTimeout(150)
  const afterClick = await ev(`${INSPECT}(doc, win)`)
  shots.dark = await page.screenshot({ animations: 'disabled', caret: 'hide' })
  await page.locator('#dots .dot').nth(1).click().catch(() => undefined)
  afterClick.slideAfterDot = await ev(`Array.from(doc.querySelectorAll('.slide')).findIndex((x) => x.classList.contains('active'))`)
  await ev(`(win.scrollTo(0, 0), 1)`)
  await page.reload({ waitUntil: 'load' })
  const ev2 = await settleDoc(page, '')
  const afterReload = await ev2(`${INSPECT}(doc, win)`)
  await ctx.close()
  return { info, afterClick, afterReload, shots, problems }
}

/** Resizes the editor window until the canvas frame is the device's viewport (width × heightOf) at zoom 1. */
async function fitEditor(page, width) {
  for (let i = 0; i < 6; i++) {
    const g = await page.evaluate((sel) => {
      const f = document.querySelector(sel)
      const r = f.getBoundingClientRect()
      return { x: r.x, y: r.y, w: r.width, h: r.height, vw: innerWidth, vh: innerHeight, cw: f.offsetWidth, ch: f.offsetHeight }
    }, FRAME)
    // Whole pixels too: a frame centred at x.5 is drawn between pixels (and shot one pixel wider).
    const whole = g.x === Math.round(g.x) && g.y === Math.round(g.y)
    if (Math.abs(g.w - width) < 1 && Math.abs(g.h - heightOf(width)) < 1 && whole) return g
    // The panels around the canvas have fixed sizes: the window grows by what the frame lacks.
    let vw = Math.round(g.w < width - 0.5 ? g.vw + (width - g.w) + 2 : g.vw)
    if (Math.abs(g.w - width) < 1 && !whole) vw += 1
    const vh = Math.round(g.vh + (heightOf(width) - g.h))
    await page.setViewportSize({ width: Math.min(4000, vw), height: Math.max(400, vh) })
    await page.waitForTimeout(250)
  }
  return page.evaluate((sel) => {
    const r = document.querySelector(sel).getBoundingClientRect()
    return { x: r.x, y: r.y, w: r.width, h: r.height, vw: innerWidth, vh: innerHeight }
  }, FRAME)
}

async function frameReady(page, scripts) {
  await page.waitForFunction(
    ([sel, scripts]) => {
      const f = document.querySelector(sel)
      if (!f || (scripts !== null && f.getAttribute('data-wb-scripts') !== scripts)) return false
      const d = f.contentDocument
      return !!d && d.readyState === 'complete' && !!d.querySelector('[data-wbc]')
    },
    [FRAME, scripts],
    { timeout: 60_000 },
  )
  await page.waitForTimeout(300)
}

async function setDevice(page, width) {
  const dev = DEVICE[width]
  for (const l of DEVICE_LABELS[dev]) {
    const b = page.locator(`button[aria-label="${l}"][aria-pressed]`)
    if (await b.count()) {
      await b.first().click()
      await page.waitForFunction(([sel, w]) => document.querySelector(sel)?.offsetWidth === w, [FRAME, width], { timeout: 10_000 })
      return true
    }
  }
  throw new Error(`no device button for ${dev}`)
}

/** The trust prompt is on the canvas (an untrusted design with scripts): its answer, or null when it did not come. */
async function promptShown(page, timeout = 4000) {
  return page
    .locator('[data-wb-trust-prompt]')
    .waitFor({ state: 'visible', timeout })
    .then(() => true)
    .catch(() => false)
}

/**
 * Puts the canvas in the run's mode:
 *  - static: Safe mode (the prompt answered "Not now", or the Safe button);
 *  - live: "This session only" (nothing stored);
 *  - always: "Always for this design" (stored per user and kit: reopening the editor is Live at once).
 * The prompt appears by itself on an untrusted page; else the Live button asks.
 */
async function enterMode(page, mode) {
  const out = { prompted: await promptShown(page), answer: null }
  if (mode === 'static') {
    if (out.prompted) {
      await page.locator('[data-wb-trust="later"]').click()
      out.answer = 'later'
    }
    if ((await page.locator(FRAME).getAttribute('data-wb-scripts')) === '1') await page.locator('[data-wb-safe]').click()
    await frameReady(page, '0')
    return out
  }
  if ((await page.locator(FRAME).getAttribute('data-wb-scripts')) === '1' && !out.prompted) {
    // Trusted already ("Always" earlier): Live from the start, nothing asked.
    await frameReady(page, '1')
    return out
  }
  if (!out.prompted) {
    await page.locator('[data-wb-live]').click()
    out.prompted = await promptShown(page, 5000)
  }
  const answer = mode === 'always' ? 'always' : 'session'
  if (out.prompted) {
    await page.locator(`[data-wb-trust="${answer}"]`).click()
    out.answer = answer
  }
  await frameReady(page, '1')
  return out
}

/** Interact mode on or off (the canvas passes clicks to the design while it is on). */
async function setInteract(page, on) {
  const b = page.locator('[data-wb-interact]')
  if (!(await b.count())) return false
  if (((await b.getAttribute('aria-pressed')) === 'true') !== on) await b.click()
  return (await b.getAttribute('aria-pressed')) === 'true'
}

async function editor(width, mode, real) {
  // real: a laptop window (1440 × 900) as an author has it; else the window is grown until the
  // frame is the device's viewport at zoom 1, so the pixels can be compared 1:1.
  const ctx = await browser.newContext({ viewport: { width: 1440, height: HEIGHT }, deviceScaleFactor: real ? 2 : 1, reducedMotion: 'reduce', serviceWorkers: 'block', storageState: storage })
  const page = await ctx.newPage()
  const problems = watch(page)
  await page.goto(editURL, { waitUntil: 'load' })
  await frameReady(page, null)
  const defaultMode = await page.locator(FRAME).getAttribute('data-wb-frame')
  if (DEVICE[width] !== 'desktop') await setDevice(page, width)
  const consent = await enterMode(page, mode)
  const out = { defaultMode, consent, problems }
  if (mode === 'always') {
    // Trusted: a new editor session opens Live with no question.
    const p2 = await ctx.newPage()
    await p2.goto(editURL, { waitUntil: 'load' })
    await frameReady(p2, null)
    out.reopen = { frame: await p2.locator(FRAME).getAttribute('data-wb-frame'), scripts: await p2.locator(FRAME).getAttribute('data-wb-scripts'), prompted: await promptShown(p2, 2500) }
    await p2.close()
  }
  const geom = real ? null : await fitEditor(page, width)
  if (!real) await frameReady(page, mode === 'static' ? '0' : '1')
  await page.mouse.move(1, 1)
  const ev = await settleDoc(page, 'frame')
  out.geom = geom
  out.frameAttrs = await page.evaluate((sel) => {
    const f = document.querySelector(sel)
    return { mode: f.getAttribute('data-wb-frame'), scripts: f.getAttribute('data-wb-scripts'), sandbox: f.getAttribute('sandbox'), transform: f.style.transform, cssSize: [f.offsetWidth, f.offsetHeight], rect: [Math.round(f.getBoundingClientRect().width), Math.round(f.getBoundingClientRect().height)] }
  }, FRAME)
  out.info = await page.evaluate(inFrame(`${INSPECT}(doc, win)`))
  if (real) {
    if (mode === 'always') out.revoked = await revoke(page)
    await ctx.close()
    return out
  }
  const r = await page.evaluate((sel) => {
    const b = document.querySelector(sel).getBoundingClientRect()
    return { x: b.x, y: b.y, width: b.width, height: b.height }
  }, FRAME)
  const el = { screenshot: (o) => page.screenshot({ ...o, clip: { x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height) } }) }
  out.shots = { top: await el.screenshot({ animations: 'disabled', caret: 'hide' }) }
  await ev(scrollToSlider)
  await page.waitForTimeout(100)
  await page.mouse.move(1, 1)
  out.shots.scrolled = await el.screenshot({ animations: 'disabled', caret: 'hide' })
  await ev(`(win.scrollTo(0, 0), 1)`)
  // The theme toggle, clicked like a user would (through the canvas): in Interact mode (--clicks
  // interact, the default) the click is the design's; with --clicks edit it selects the element.
  const interact = CLICKS === 'interact' ? await setInteract(page, true) : false
  const fl = page.frameLocator(FRAME)
  let clickError = null
  try {
    await fl.locator('#theme-toggle').click({ timeout: 5000 })
  } catch (e) {
    clickError = String(e.message || e).split('\n')[0]
  }
  await page.waitForTimeout(200)
  out.afterClick = await page.evaluate(inFrame(`${INSPECT}(doc, win)`))
  out.afterClick.interact = interact
  out.afterClick.clickError = clickError
  out.afterClick.selected = await page.locator('[data-wb-selected]').getAttribute('data-wb-selected').catch(() => null)
  await page.mouse.move(1, 1)
  out.shots.dark = await el.screenshot({ animations: 'disabled', caret: 'hide' })
  // A slider dot (page content, not the locked header): does the design's own click handler run?
  try {
    await fl.locator('#dots .dot').nth(1).click({ timeout: 5000 })
  } catch (e) {
    out.afterClick.dotError = String(e.message || e).split('\n')[0]
  }
  await page.waitForTimeout(200)
  out.afterClick.slideAfterDot = await page.evaluate(inFrame(`Array.from(doc.querySelectorAll('.slide')).findIndex((x) => x.classList.contains('active'))`))
  out.afterClick.selectedAfterDot = await page.locator('[data-wb-selected]').getAttribute('data-wb-selected').catch(() => null)
  if (interact) {
    // Back to editing: what the design's scripts changed stays (K3).
    await setInteract(page, false)
    await page.waitForTimeout(150)
    out.afterClick.darkAfterInteract = await page.evaluate(inFrame(`doc.documentElement.classList.contains('dark')`))
    out.afterClick.slideAfterInteract = await page.evaluate(inFrame(`Array.from(doc.querySelectorAll('.slide')).findIndex((x) => x.classList.contains('active'))`))
  }
  await page.keyboard.press('Escape').catch(() => undefined)
  await page.evaluate(inFrame(`(win.scrollTo(0, 0), 1)`))
  // The remembered theme (same origin as the published page: one localStorage) on a fresh load.
  await page.evaluate(() => localStorage.setItem('parity-theme', 'dark'))
  await page.reload({ waitUntil: 'load' })
  await frameReady(page, null)
  if (DEVICE[width] !== 'desktop') await setDevice(page, width)
  out.consentAgain = await enterMode(page, mode)
  out.afterReload = await page.evaluate(inFrame(`${INSPECT}(doc, win)`))
  await page.evaluate(() => localStorage.removeItem('parity-theme'))
  await ctx.close()
  return out
}

/** "Stop always running this design's scripts" (the bottom bar): back to Safe mode, the trust gone. */
async function revoke(page) {
  const b = page.locator('[data-wb-untrust]')
  if (!(await b.count())) return { done: false }
  await b.click()
  await frameReady(page, '0')
  return { done: true, frame: await page.locator(FRAME).getAttribute('data-wb-frame'), untrustShown: (await page.locator('[data-wb-untrust]').count()) > 0 }
}

/* ---------- comparing (YIQ distance, as fidelity.mjs / builder/test/parity) ---------- */

let cmpPage
async function compare(a, b, threshold = 0.1) {
  if (!cmpPage) cmpPage = await (await browser.newContext()).newPage()
  const r = await cmpPage.evaluate(
    async ([a64, b64, threshold]) => {
      const load = async (s) => createImageBitmap(await (await fetch(`data:image/png;base64,${s}`)).blob())
      const [ia, ib] = await Promise.all([load(a64), load(b64)])
      const w = Math.max(ia.width, ib.width)
      const h = Math.max(ia.height, ib.height)
      const px = (img) => {
        const c = new OffscreenCanvas(w, h)
        const g = c.getContext('2d')
        g.drawImage(img, 0, 0)
        return g.getImageData(0, 0, w, h).data
      }
      const pa = px(ia)
      const pb = px(ib)
      const out = new OffscreenCanvas(w, h)
      const og = out.getContext('2d')
      const od = og.createImageData(w, h)
      const max = 35215 * threshold * threshold
      let diff = 0
      for (let y = 0; y < h; y++)
        for (let x = 0; x < w; x++) {
          const o = (y * w + x) * 4
          let d = x >= ia.width || y >= ia.height || x >= ib.width || y >= ib.height
          if (!d) {
            const r1 = pa[o], g1 = pa[o + 1], b1 = pa[o + 2], r2 = pb[o], g2 = pb[o + 1], b2 = pb[o + 2]
            const yy = (r1 - r2) * 0.29889531 + (g1 - g2) * 0.58662247 + (b1 - b2) * 0.11448223
            const ii = (r1 - r2) * 0.59597799 - (g1 - g2) * 0.2741761 - (b1 - b2) * 0.32180189
            const qq = (r1 - r2) * 0.21147017 - (g1 - g2) * 0.52261711 + (b1 - b2) * 0.31114694
            d = 0.5053 * yy * yy + 0.299 * ii * ii + 0.1957 * qq * qq > max
          }
          if (d) {
            diff++
            od.data[o] = 255; od.data[o + 1] = 0; od.data[o + 2] = 0; od.data[o + 3] = 255
          } else {
            const v = 255 - (255 - (pa[o] * 0.299 + pa[o + 1] * 0.587 + pa[o + 2] * 0.114)) * 0.15
            od.data[o] = v; od.data[o + 1] = v; od.data[o + 2] = v; od.data[o + 3] = 255
          }
        }
      og.putImageData(od, 0, 0)
      const bytes = new Uint8Array(await (await out.convertToBlob({ type: 'image/png' })).arrayBuffer())
      let s = ''
      for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
      return { w, h, sizeA: [ia.width, ia.height], sizeB: [ib.width, ib.height], diff, png: btoa(s) }
    },
    [a.toString('base64'), b.toString('base64'), threshold],
  )
  return { ...r, pct: Math.round((r.diff / (r.w * r.h)) * 1e6) / 1e4, png: Buffer.from(r.png, 'base64') }
}

/* ---------- the run ---------- */

const results = []
for (const width of WIDTHS) {
  log(`\n== ${width}px`)
  const pub = await published(width)
  const pubChecks = checksOf(pub.info, width)
  const row = { width, published: { info: pub.info, checks: pubChecks, afterClick: pub.afterClick, afterReload: pub.afterReload, problems: pub.problems }, editor: {} }
  log(`published: ${Object.entries(pubChecks).filter(([, v]) => !v).map(([k]) => k).join(', ') || 'all checks pass'}`)
  for (const mode of MODES) {
    let ed
    try {
      ed = await editor(width, mode, false)
    } catch (e) {
      row.editor[mode] = { error: String(e.message || e) }
      log(`editor ${mode}: ERROR ${e.message}`)
      continue
    }
    const checks = checksOf(ed.info, width)
    const diffs = {}
    for (const k of ['top', 'scrolled', 'dark']) {
      const d = await compare(pub.shots[k], ed.shots[k])
      const base = `${width}-${mode}-${k}`
      fs.writeFileSync(path.join(OUT, 'img', `${base}.published.png`), pub.shots[k])
      fs.writeFileSync(path.join(OUT, 'img', `${base}.editor.png`), ed.shots[k])
      fs.writeFileSync(path.join(OUT, 'img', `${base}.diff.png`), d.png)
      diffs[k] = { pct: d.pct, sizePublished: d.sizeA, sizeEditor: d.sizeB, images: base }
    }
    const functional = {
      ...Object.fromEntries(Object.entries(checks).map(([k, v]) => [k, { editor: v, published: pubChecks[k] }])),
      toggleClick: { editor: !!ed.afterClick?.dark, published: !!pub.afterClick?.dark },
      sliderDotClick: { editor: ed.afterClick?.slideAfterDot === 1, published: pub.afterClick?.slideAfterDot === 1 },
      toggleStored: { editor: ed.afterClick?.stored === 'dark', published: pub.afterClick?.stored === 'dark' },
      themeRememberedOnLoad: { editor: !!ed.afterReload?.dark, published: !!pub.afterReload?.dark },
    }
    if (ed.afterClick?.interact) {
      // Leaving Interact mode keeps what the design's scripts did (the theme, the slide).
      functional.darkKeptAfterInteract = { editor: !!ed.afterClick.darkAfterInteract, published: !!pub.afterClick?.dark }
      functional.slideKeptAfterInteract = { editor: ed.afterClick.slideAfterInteract === 1, published: pub.afterClick?.slideAfterDot === 1 }
    }
    if (mode === 'always') {
      // Trusted: a new editor session opens Live at once, with no question.
      functional.alwaysReopensLive = { editor: ed.reopen?.scripts === '1' && ed.reopen?.prompted === false, published: true }
    }
    const real = await editor(width, mode, true).catch((e) => ({ error: String(e.message || e) }))
    if (mode === 'always') functional.revokeGoesSafe = { editor: real?.revoked?.done === true && real?.revoked?.frame === 'static', published: true }
    row.editor[mode] = { defaultMode: ed.defaultMode, consent: ed.consent, consentAgain: ed.consentAgain, reopen: ed.reopen, clicks: CLICKS, fit: ed.geom, frame: ed.frameAttrs, info: ed.info, afterClick: ed.afterClick, afterReload: ed.afterReload, diffs, functional, real, problems: ed.problems }
    const bad = Object.entries(functional).filter(([, v]) => v.editor !== v.published).map(([k]) => k)
    log(`editor ${mode} (default frame mode: ${ed.defaultMode}): diff top ${diffs.top.pct} %, scrolled ${diffs.scrolled.pct} %, dark ${diffs.dark.pct} %; differs: ${bad.join(', ') || 'none'}`)
  }
  results.push(row)
}
if (madeAppUUID) {
  // The throwaway Application Password goes again.
  const nonce = await restNonce()
  await ap.evaluate(async ([s, n, id]) => fetch(`${s}/wp-json/wp/v2/users/me/application-passwords/${id}`, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-WP-Nonce': n } }).then((r) => r.status), [SITE, nonce, madeAppUUID])
}
await browser.close()

/* ---------- report ---------- */

const summary = { site: SITE, page: pageURL, editor: editURL, csp: cspNow, import: IMPORT, target: TARGET, rows: [] }
let failed = false
for (const r of results)
  for (const [mode, e] of Object.entries(r.editor)) {
    if (e.error) {
      failed = true
      summary.rows.push({ width: r.width, mode, error: e.error })
      continue
    }
    const differs = Object.entries(e.functional).filter(([, v]) => v.editor !== v.published).map(([k]) => k)
    const max = Math.max(...Object.values(e.diffs).map((d) => d.pct))
    // Safe mode (static) runs none of the design's scripts by design: reported, never a failure.
    if (mode !== 'static' && (differs.length || max > TARGET)) failed = true
    summary.rows.push({ width: r.width, mode, top: e.diffs.top.pct, scrolled: e.diffs.scrolled.pct, dark: e.diffs.dark.pct, differs })
  }
fs.writeFileSync(path.join(OUT, 'report.json'), JSON.stringify({ summary, results }, null, 2))
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c])
const yes = (v) => (v ? '<b class="y">yes</b>' : '<b class="n">no</b>')
let body = ''
for (const r of results) {
  for (const [mode, e] of Object.entries(r.editor)) {
    body += `<section><h2>${r.width}px · ${esc(mode)}</h2>`
    if (e.error) {
      body += `<p class="n">${esc(e.error)}</p></section>`
      continue
    }
    body += `<p>default frame mode <code>${esc(e.defaultMode)}</code> · frame <code>${esc(JSON.stringify(e.frame))}</code> · laptop window: zoom <code>${esc(e.real?.frameAttrs?.transform ?? '')}</code>, frame viewport <code>${esc(JSON.stringify(e.real?.info?.viewport ?? null))}</code>, hero <code>${esc(e.real?.info?.heroHeight ?? '')}</code>, canvas <code>${esc(JSON.stringify(e.real?.info?.canvas ?? null))}</code></p>`
    body += `<table><tr><th>check</th><th>published</th><th>editor</th></tr>${Object.entries(e.functional).map(([k, v]) => `<tr class="${v.editor === v.published ? '' : 'bad'}"><td>${k}</td><td>${yes(v.published)}</td><td>${yes(v.editor)}</td></tr>`).join('')}</table>`
    for (const [k, d] of Object.entries(e.diffs))
      body += `<h3>${k}: ${d.pct} % differing pixels</h3><div class="row">${['published', 'editor', 'diff'].map((s) => `<figure><figcaption>${s}</figcaption><a href="img/${d.images}.${s}.png"><img loading="lazy" src="img/${d.images}.${s}.png"></a></figure>`).join('')}</div>`
    body += `<details><summary>editor info, console and network</summary><pre>${esc(JSON.stringify({ info: e.info, afterClick: e.afterClick, afterReload: e.afterReload, problems: e.problems }, null, 2))}</pre></details></section>`
  }
  body += `<details><summary>published ${r.width}px info</summary><pre>${esc(JSON.stringify(r.published, null, 2))}</pre></details>`
}
fs.writeFileSync(
  path.join(OUT, 'report.html'),
  `<!doctype html><meta charset="utf-8"><title>Editor parity</title>
<style>body{font:14px system-ui,sans-serif;margin:24px;color:#222}table{border-collapse:collapse;margin:8px 0}td,th{border:1px solid #ddd;padding:3px 8px;text-align:left}tr.bad td{background:#fff0f0}.y{color:#070}.n{color:#b00}.row{display:flex;gap:8px;align-items:flex-start}figure{margin:0;flex:1}img{width:100%;border:1px solid #ccc}pre{white-space:pre-wrap;font-size:12px}section{border-top:2px solid #333;margin-top:24px}</style>
<h1>Editor ↔ published parity</h1><p>${esc(pageURL)} · editor ${esc(editURL)} · csp_mode ${esc(cspNow)} · import ${esc(IMPORT)} · target ≤ ${TARGET} %</p>
<table><tr><th>width</th><th>mode</th><th>top %</th><th>scrolled %</th><th>dark %</th><th>functional differences</th></tr>${summary.rows.map((s) => `<tr class="${s.error || s.differs?.length ? 'bad' : ''}"><td>${s.width}</td><td>${esc(s.mode)}</td><td>${s.top ?? ''}</td><td>${s.scrolled ?? ''}</td><td>${s.dark ?? ''}</td><td>${esc(s.error ?? s.differs.join(', '))}</td></tr>`).join('')}</table>${body}`,
)
log(`\n→ ${path.join(OUT, 'report.html')}`)
process.exit(failed ? 1 : 0)
