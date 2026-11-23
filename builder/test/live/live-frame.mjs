#!/usr/bin/env node
/**
 * Live frame ↔ published page (elements/live.ts, internal/server/webbuilder_liveframe.go).
 *
 * Against a running Pursae with a published code page (e.g. the editor-parity fixture that
 * builder/test/editor-parity/parity.mjs imports), in Playwright's Chromium:
 *   1. the published page at 1440 × 900: behaviour checks and a screenshot;
 *   2. the editor's page of that post: live.ts (bundled here) mounts a live frame the way the
 *      canvas does (openLive → loadLive, LIVE_SANDBOX, a 1440 × 900 viewport): the same checks
 *      and a screenshot at scale 1, then again scaled by a CSS transform (the canvas's zoom) with
 *      clicks through the transform (theme toggle, slider dot);
 *   3. applyToLive: a text edit and a class edit patched in place (no reload, the WebGL canvas
 *      keeps its pixels, script-set classes stay);
 *   4. reloadPreserving: scroll position and the theme kept; 24 reloads leave no "too many
 *      active WebGL contexts" warning.
 *
 *   cd builder && node test/live/live-frame.mjs --site http://127.0.0.1:8193 --user admin --pass '…' \
 *     --post 4 [--out .work/live-frame] [--generic]   (--generic: any design; pixels and structure only)
 * PARITY_CHROMIUM may name a Chromium / headless-shell binary. Exit 1 on a difference.
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { rolldown } from 'rolldown'
import { chromium } from 'playwright'

const here = path.dirname(fileURLToPath(import.meta.url))
const builder = path.resolve(here, '../..')
const work = path.join(builder, '.work', 'upstream', 'src')
const upstream = fs.existsSync(work) ? work : path.join(builder, 'upstream', 'src')
const args = Object.fromEntries(process.argv.slice(2).reduce((a, x, i, all) => (x.startsWith('--') ? [...a, [x.slice(2), all[i + 1] && !all[i + 1].startsWith('--') ? all[i + 1] : '1']] : a), []))
const SITE = (args.site || process.env.LIVE_SITE || '').replace(/\/+$/, '')
const POST = Number(args.post || process.env.LIVE_POST || 0)
const OUT = path.resolve(args.out || path.join(builder, '.work', 'live-frame'))
if (!SITE || !POST || !args.user || !args.pass) {
  console.error('usage: node test/live/live-frame.mjs --site URL --user U --pass P --post ID [--out dir]')
  process.exit(2)
}
fs.mkdirSync(OUT, { recursive: true })

const bundle = await rolldown({ input: path.join(here, 'harness.ts'), resolve: { alias: { '@': upstream } }, logLevel: 'silent' })
const { output } = await bundle.generate({ format: 'iife' })
const JS = output[0].code

const browser = await chromium.launch({ executablePath: process.env.PARITY_CHROMIUM || undefined, args: ['--force-color-profile=srgb', '--use-angle=swiftshader', '--enable-unsafe-swiftshader'] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })
const page = await ctx.newPage()
const problems = []
page.on('console', (m) => {
  if (/too many active webgl contexts/i.test(m.text())) problems.push('webgl: ' + m.text())
})

// Log in.
await page.goto(`${SITE}/wp-login`)
await page.fill('#user_login', args.user)
await page.fill('#user_pass', args.pass)
await Promise.all([page.waitForNavigation(), page.click('#wp-submit')])

/** What the fixture's scripts do, read in a document (the parity fixture's ids). */
function inspect(doc, win) {
  const html = doc.documentElement
  const $ = (s) => doc.querySelector(s)
  const canvas = $('#scene')
  let painted = 0
  if (canvas && canvas.width) {
    const c = doc.createElement('canvas')
    c.width = c.height = 32
    const g = c.getContext('2d')
    g.drawImage(canvas, 0, 0, 32, 32)
    const d = g.getImageData(0, 0, 32, 32).data
    for (let i = 0; i < d.length; i += 4) if (d[i + 3] && Math.abs(d[i] - d[0]) + Math.abs(d[i + 1] - d[1]) + Math.abs(d[i + 2] - d[2]) > 30) painted++
  }
  const hero = $('#hero')
  const header = $('.site-header')
  const reveals = Array.from(doc.querySelectorAll('.reveal'))
  return {
    js: html.getAttribute('data-js') === '1',
    module: html.getAttribute('data-module') === 'ok',
    asset: html.getAttribute('data-asset') || 'none',
    json: html.getAttribute('data-json') || 'none',
    painted: painted > 4,
    backingOk: canvas ? Math.abs(canvas.width - Math.floor(canvas.clientWidth * win.devicePixelRatio)) <= 1 : false,
    viewport: [win.innerWidth, win.innerHeight],
    hero100vh: hero ? Math.round(hero.getBoundingClientRect().height) === Math.max(420, win.innerHeight) : false,
    headerFixed: header ? win.getComputedStyle(header).position === 'fixed' && Math.round(header.getBoundingClientRect().top) === 0 : false,
    dark: html.classList.contains('dark'),
    counter: ($('#counter')?.textContent ?? '').trim(),
    activeIndex: Array.from(doc.querySelectorAll('.slide')).findIndex((x) => x.classList.contains('active')),
    dots: doc.querySelectorAll('#dots .dot').length,
    items: doc.querySelectorAll('#items li').length,
    revealed: reveals.filter((r) => r.classList.contains('visible')).length + '/' + reveals.length,
    path: win.location.pathname + win.location.search,
    lang: html.lang,
    // wp_footer's site-wide scripts (script#…-js: comment-reply) are not the design's.
    bodyKids: Array.from(doc.body.children).filter((e) => !(e.localName === 'script' && /-js$/.test(e.id))).map((e) => e.tagName.toLowerCase() + (e.id ? '#' + e.id : '')).join(' '),
    scripts: doc.querySelectorAll('script').length,
  }
}
const INSPECT = `(${inspect.toString()})`

async function settle(p, frame) {
  const target = frame ?? p.mainFrame()
  await target.waitForFunction(() => document.documentElement.getAttribute('data-json') && document.fonts.status === 'loaded', null, { timeout: 15000 }).catch(() => {})
  // Scroll through for the reveals, back to the top.
  await target.evaluate(async () => {
    for (let y = 0; y < document.documentElement.scrollHeight; y += 400) {
      window.scrollTo(0, y)
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
    }
    window.scrollTo(0, 0)
    await new Promise((r) => setTimeout(r, 150))
  })
}

// ---------- 1. the published page, as a visitor sees it (no admin bar) ----------
const visitor = await (await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })).newPage()
await visitor.goto(`${SITE}/?p=${POST}`)
const pageURL = visitor.url()
await settle(visitor)
const pub = await visitor.evaluate(`${INSPECT}(document, window)`)
await visitor.screenshot({ path: path.join(OUT, 'published.png') })
fs.writeFileSync(path.join(OUT, 'published.html'), await visitor.content())

// ---------- 2. the live frame in the editor's page ----------
await page.goto(`${SITE}/wp-admin/post?post=${POST}&action=web-builder`)
await page.waitForFunction(() => !!window.__WEB_BUILDER_HOST__, null, { timeout: 30000 })
await page.evaluate(JS) // CDP evaluation: not subject to the editor page's CSP, like the editor's own bundle
const mount = async (zoom) =>
  page.evaluate(
    async ({ zoom, url }) => {
      const L = window.__wbLive
      const boot = window.__WEB_BUILDER__
      const doc = boot.document.draft?.doc ?? boot.document.doc
      const parts = boot.codeParts
      const input = {
        postId: boot.document.postId,
        page: doc.sections.filter((s) => s.kind === 'code' && !s.hidden),
        header: parts?.header?.sections ?? [],
        footer: parts?.footer?.sections ?? [],
        shell: L.liveShellOf(!!boot.site.themeParts, boot.document.page?.layout, !!boot.template),
        url,
        kitCss: '',
        localCss: '',
        editCss: '',
      }
      window.__liveInput = input
      document.getElementById('lf-box')?.remove()
      const box = document.createElement('div')
      box.id = 'lf-box'
      box.style.cssText = 'position:fixed;inset:0;z-index:2147483647;background:#fff;overflow:hidden'
      const f = document.createElement('iframe')
      f.setAttribute('sandbox', L.LIVE_SANDBOX)
      f.setAttribute('data-live', '')
      f.style.cssText = `border:0;display:block;width:1440px;height:900px;transform:scale(${zoom});transform-origin:0 0`
      box.appendChild(f)
      document.body.appendChild(box)
      const src = await L.openLive(input)
      await L.loadLive(f, src)
      return { src: src.src ?? 'srcdoc' }
    },
    { zoom, url: pageURL },
  )
const frameOf = async () => (await page.$('iframe[data-live]'))?.contentFrame()

const m1 = await mount(1)
const lf = await frameOf()
if (!lf) throw new Error('no live frame: ' + JSON.stringify(m1))
await settle(page, lf)
const liv = await lf.evaluate(`${INSPECT}(document, window)`)
await page.screenshot({ path: path.join(OUT, 'live.png') })
fs.writeFileSync(path.join(OUT, 'live.html'), await lf.content())

// Pixel comparison (same 1440 × 900 viewport).
const diff = await page.evaluate(async ([a, b]) => {
  const load = (src) => new Promise((r) => { const i = new Image(); i.onload = () => r(i); i.src = src })
  const [x, y] = await Promise.all([load(a), load(b)])
  const c = document.createElement('canvas')
  c.width = x.width
  c.height = x.height
  const g = c.getContext('2d', { willReadFrequently: true })
  g.drawImage(x, 0, 0)
  const da = g.getImageData(0, 0, c.width, c.height).data
  g.clearRect(0, 0, c.width, c.height)
  g.drawImage(y, 0, 0)
  const db = g.getImageData(0, 0, c.width, c.height).data
  let n = 0
  for (let i = 0; i < da.length; i += 4) if (Math.abs(da[i] - db[i]) + Math.abs(da[i + 1] - db[i + 1]) + Math.abs(da[i + 2] - db[i + 2]) > 24) n++
  return Math.round((n / (da.length / 4)) * 10000) / 100
}, ['data:image/png;base64,' + fs.readFileSync(path.join(OUT, 'published.png')).toString('base64'), 'data:image/png;base64,' + fs.readFileSync(path.join(OUT, 'live.png')).toString('base64')])

// Scaled like the canvas (zoom 0.6): geometry and clicks through the transform.
await mount(0.6)
const sf = await frameOf()
await settle(page, sf)
const scaled = await sf.evaluate(`${INSPECT}(document, window)`)
if (!args.generic) {
  await sf.locator('#theme-toggle').click()
  await sf.locator('#dots .dot').nth(1).click()
}
const clicked = await sf.evaluate(`${INSPECT}(document, window)`)
const stored = await sf.evaluate(() => localStorage.getItem('parity-theme'))

// ---------- 3. applyToLive ----------
const patch = await page.evaluate(() => {
  const L = window.__wbLive
  const f = document.querySelector('iframe[data-live]')
  const d = f.contentDocument
  const sec = window.__liveInput.page.find((s) => /<h1/.test(s.content.html))
  if (!sec) return { r1: 'no h1', r2: 'no h1' }
  const prev = { [sec.id]: sec.content.html }
  const h1 = sec.content.html.match(/<h1[^>]*>/)[0]
  const next1 = sec.content.html.replace(/(<h1[^>]*>)[\s\S]*?(<\/h1>)/, '$1Edited <em>live</em>.$2')
  const canvas = d.getElementById('scene')
  const before = canvas
  const r1 = L.applyToLive(d, prev, { [sec.id]: next1 })
  const next2 = next1.replace('class="hero-copy"', 'class="hero-copy extra"')
  const r2 = L.applyToLive(d, { [sec.id]: next1 }, { [sec.id]: next2 })
  return {
    r1: r1.reload ? r1.reason : 'patched',
    r2: r2.reload ? r2.reason : 'patched',
    h1: d.querySelector('h1').innerHTML,
    h1tag: h1,
    canvasKept: d.getElementById('scene') === before && f.contentWindow.__wbLiveGL?.length >= 0,
    copy: d.querySelector('.hero-copy')?.className,
    slide: Array.from(d.querySelectorAll('.slide')).findIndex((x) => x.classList.contains('active')),
    dark: d.documentElement.classList.contains('dark'),
  }
})

// ---------- 4. reloadPreserving and WebGL contexts ----------
await sf.evaluate(() => window.scrollTo(0, 700))
const kept = await page.evaluate(async () => {
  const L = window.__wbLive
  const f = document.querySelector('iframe[data-live]')
  for (let i = 0; i < 24; i++) await L.reloadPreserving(f, await L.openLive(window.__liveInput))
  const d = f.contentDocument
  return { y: f.contentWindow.scrollY, dark: d.documentElement.classList.contains('dark'), gl: f.contentWindow.__wbLiveGL?.length ?? -1 }
})
await page.waitForTimeout(300)

// ---------- report ----------
const same = (k) => JSON.stringify(pub[k]) === JSON.stringify(liv[k])
const rows = {
  'scripts ran (inline, module, importmap)': pub.js && pub.module && same('js') && same('module'),
  'module-relative asset': same('asset') && pub.asset === 'ok',
  'page-relative fetch': same('json') && same('items') && pub.json === 'ok',
  'WebGL scene drawn, backing = css × dpr': liv.painted && liv.backingOk,
  'counter / slider / dots drawn': same('counter') && same('activeIndex') && same('dots'),
  'reveals (IntersectionObserver)': same('revealed'),
  'viewport 1440 × 900, 100vh hero, fixed header': same('viewport') && liv.hero100vh && liv.headerFixed,
  "location (path + query) is the page's": same('path'),
  'lang and body structure': same('lang') && same('bodyKids'),
  'scaled frame keeps the 1440 × 900 viewport': scaled.viewport.join() === '1440,900' && scaled.hero100vh,
  'click through the transform: theme toggle + localStorage': clicked.dark && stored === 'dark',
  'click through the transform: slider dot': clicked.activeIndex === 1,
  'applyToLive: text edit in place': patch.r1 === 'patched' && /Edited <em>live<\/em>\./.test(patch.h1),
  'applyToLive: class edit keeps script state': patch.r2 === 'patched' && /extra/.test(patch.copy) && patch.slide === 1 && patch.dark && patch.canvasKept,
  'reloadPreserving: scroll + theme': Math.abs(kept.y - 700) <= 2 && kept.dark,
  'no "too many WebGL contexts" after 24 reloads': !problems.length,
  [`pixels differ ≤ 0.5 % (${diff} %)`]: diff <= 0.5,
}
// --generic: any imported design (the fixture's own checks left out).
const GENERIC = !!args.generic
const generic = {
  'lang and body structure': same('lang') && same('bodyKids'),
  "location (path + query) is the page's": same('path'),
  'scaled frame keeps the 1440 × 900 viewport': scaled.viewport.join() === '1440,900',
  'reloadPreserving: scroll': Math.abs(kept.y - 700) <= 2 || kept.y < 700,
  'no "too many WebGL contexts" after 24 reloads': !problems.length,
  [`pixels differ ≤ 0.5 % (${diff} %)`]: diff <= 0.5,
}
if (GENERIC) for (const k of Object.keys(rows)) delete rows[k]
if (GENERIC) Object.assign(rows, generic)
const report = { site: SITE, post: POST, page: pageURL, source: m1.src, published: pub, live: liv, scaled, clicked, patch, kept, diff, problems, rows }
fs.writeFileSync(path.join(OUT, 'report.json'), JSON.stringify(report, null, 2))
let failed = false
for (const [k, ok] of Object.entries(rows)) {
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${k}`)
  failed ||= !ok
}
console.log(`→ ${path.join(OUT, 'report.json')}`)
await browser.close()
process.exit(failed ? 1 : 0)
