#!/usr/bin/env node
// Import fidelity check (docs/developer/web-builder-code-sections.md, "Fidelity check"): every page
// of a static HTML site, served from its folder, against the page the site import made of it on a
// running Pursae, full page at 1440, 1024, 768 and 390 px, as a share of differing pixels. Both
// sides load with reduced motion, transitions and animations off and a paused clock (timers run for
// --clock ms after load, so a slider does not move on), are scrolled through so scroll reveals
// finish, and wait for fonts and images. An optional scenario file adds states (a hover, an open
// menu). The answer: report.json and report.html (source, target and diff images of every page and
// width that differs) in --out.
//
//   node builder/test/fidelity/fidelity.mjs --src <folder> --site <url> [--map <map.json>] --out <dir>
//     [--widths 1440,1024,768,390] [--only <substring>] [--scenarios <file.json>] [--target 0.1]
//     [--jobs 4] [--clock 1000] [--images fail|all] [--mask <selector>] [--block <url,…>] [--self]
//
// --map: {"index.html": "/", "hizmet/3.html": "/hizmet/3/"}; without it the site import's own plan
// is used (index.html → /, a/b.html → /a/b/, a/index.html → /a/, 404.html → a missing URL).
// Scenario file: [{"name": "kurumsal-open", "pages": ["index.html"], "widths": [1440],
//   "steps": [{"click": "[data-menu-trigger=corporate]"}, {"hover": "#x"}, {"wait": 300}],
//   "fullPage": false}]
// Exit status: 0 when every shot is at or under --target %, 1 otherwise, 2 on a usage error.

import fs from 'node:fs'
import http from 'node:http'
import path from 'node:path'
import { chromium } from '@playwright/test'

const args = parseArgs(process.argv.slice(2))
if (!args.src || !args.site || !args.out) {
  console.error('usage: fidelity.mjs --src <folder> --site <url> --out <dir> [--map map.json] [--widths …] [--only …] [--scenarios file] [--target 0.1] [--jobs 4] [--clock 1000] [--images fail|all]')
  process.exit(2)
}
const SRC = path.resolve(args.src)
const SITE = args.site.replace(/\/+$/, '')
const OUT = path.resolve(args.out)
const WIDTHS = (args.widths || '1440,1024,768,390').split(',').map(Number)
const TARGET = Number(args.target ?? 0.1) // percent
const JOBS = Number(args.jobs || 4)
const CLOCK = Number(args.clock ?? 1000)
const IMAGES = args.images || 'fail'
const HEIGHT = 900
// --mask: elements hidden on both sides (live third-party content such as a map iframe), e.g. "iframe[src*=google]".
const MASK = args.mask && args.mask !== 'true' ? args.mask : ''
// --block: requests aborted on both sides, comma-separated URL substrings (a live third-party API such
// as a GitHub star count: its answer and its timing differ between loads, and it rate-limits).
const BLOCK = args.block && args.block !== 'true' ? args.block.split(',').filter(Boolean) : []
const dbg = (...a) => process.env.FIDELITY_DEBUG && console.error(new Date().toISOString().slice(11, 23), ...a)
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

/* ---------- the pages and their URLs ---------- */

function walk(dir, rel = '') {
  const out = []
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name.startsWith('.') || e.name === 'node_modules') continue
    const r = rel ? `${rel}/${e.name}` : e.name
    if (e.isDirectory()) out.push(...walk(path.join(dir, e.name), r))
    else if (/\.html?$/i.test(e.name)) out.push(r)
  }
  return out.sort()
}

// The site import's plan (webbuilder.PlanSite): slugs are lowercased names, other characters dashes.
const slug = (s) => s.toLowerCase().replace(/[^a-z0-9\u0080-￿]+/g, '-').replace(/^-+|-+$/g, '')
function autoURL(file) {
  const parts = file.split('/')
  const name = parts.pop().replace(/\.html?$/i, '')
  if (parts.length === 0 && name.toLowerCase() === 'index') return '/'
  if (parts.length === 0 && name === '404') return '/__fidelity-missing-page__/'
  const segs = parts.map(slug)
  if (name.toLowerCase() !== 'index') segs.push(slug(name))
  return `/${segs.join('/')}/`
}

const files = walk(SRC).filter((f) => !args.only || f.includes(args.only))
const map = args.map ? JSON.parse(fs.readFileSync(args.map, 'utf8')) : {}
const pages = files.map((f) => ({ file: f, url: map[f] ?? autoURL(f) }))
const scenarios = args.scenarios ? JSON.parse(fs.readFileSync(args.scenarios, 'utf8')) : []

/* ---------- the source folder, served ---------- */

const TYPES = { '.html': 'text/html; charset=utf-8', '.htm': 'text/html; charset=utf-8', '.css': 'text/css', '.js': 'text/javascript', '.mjs': 'text/javascript',
  '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.gif': 'image/gif',
  '.webp': 'image/webp', '.avif': 'image/avif', '.ico': 'image/x-icon', '.woff2': 'font/woff2', '.woff': 'font/woff', '.ttf': 'font/ttf',
  '.otf': 'font/otf', '.mp4': 'video/mp4', '.webm': 'video/webm', '.pdf': 'application/pdf' }
const server = http.createServer((req, res) => {
  let p = decodeURIComponent(new URL(req.url, 'http://x').pathname)
  if (p.endsWith('/')) p += 'index.html'
  const full = path.join(SRC, path.normalize(p).replace(/^(\.\.[/\\])+/, ''))
  if (!full.startsWith(SRC) || !fs.existsSync(full) || fs.statSync(full).isDirectory()) {
    res.writeHead(404).end('not found')
    return
  }
  res.writeHead(200, { 'Content-Type': TYPES[path.extname(full).toLowerCase()] || 'application/octet-stream', 'Cache-Control': 'no-store' })
  fs.createReadStream(full).pipe(res)
})
await new Promise((r) => server.listen(0, '127.0.0.1', r))
const ORIGIN = `http://127.0.0.1:${server.address().port}`

/* ---------- shooting ---------- */

// Both sides: no transitions or animations, no caret.
const FREEZE = '*,*::before,*::after{transition:none!important;animation:none!important;caret-color:transparent!important;scroll-behavior:auto!important}'

// Deterministic rasterization: no LCD text (its use depends on layer compositing, which load and
// timing change), one color profile, no GPU.
const browser = await chromium.launch({ args: ['--disable-lcd-text', '--force-color-profile=srgb', '--disable-gpu', '--font-render-hinting=none', '--disable-partial-raster'] })

async function open(url, width) {
  const ctx = await browser.newContext({ viewport: { width, height: HEIGHT }, deviceScaleFactor: 1, reducedMotion: 'reduce', serviceWorkers: 'block', bypassCSP: !!process.env.FIDELITY_BYPASS_CSP })
  const page = await ctx.newPage()
  if (BLOCK.length) await page.route((u) => BLOCK.some((b) => u.href.includes(b)), (r) => r.abort('blockedbyclient'))
  const errors = []
  page.on('pageerror', (e) => errors.push(String(e.message || e)))
  // Console errors (a CSP violation, a failed module import) and failed requests: a page can draw
  // the same and still lose a script on one side.
  const console_ = []
  // (Not the page's own status: the 404 page is answered 404 on purpose.)
  page.on('console', (m) => m.type() === 'error' && m.location().url !== url && console_.push(m.text().slice(0, 300)))
  page.on('requestfailed', (r) => !BLOCK.some((b) => r.url().includes(b)) && console_.push(`request failed: ${r.url().slice(0, 200)} (${r.failure()?.errorText})`))
  page.on('response', (r) => r.status() >= 400 && r.url() !== url && console_.push(`HTTP ${r.status()}: ${r.url().slice(0, 200)}`))
  page.on('crash', () => dbg('CRASH', url))
  page.on('close', () => dbg('CLOSE', url))
  if (CLOCK > 0) {
    // Installed a minute early and paused at once: the same clock on both sides, timers frozen.
    await page.clock.install({ time: new Date('2026-01-01T09:59:00Z') })
    await page.clock.pauseAt(new Date('2026-01-01T10:00:00Z'))
  }
  dbg("goto", url)
  const resp = await page.goto(url, { waitUntil: 'load', timeout: 60_000 })
  dbg("loaded", url)
  await page.addStyleTag({ content: FREEZE + (MASK ? `${MASK}{visibility:hidden!important}` : "") })
  if (CLOCK > 0) await page.clock.runFor(CLOCK)
  dbg("settle", url)
  await settle(page)
  dbg("settled", url)
  return { ctx, page, status: resp ? resp.status() : 0, errors, console: console_ }
}

async function tick(page, ms) {
  if (CLOCK > 0) await page.clock.runFor(ms)
  else await page.waitForTimeout(ms)
}

async function settle(page) {
  await page.evaluate(() => document.fonts.ready)
  dbg("fonts ok")
  const { h, vh } = await page.evaluate(() => ({ h: document.documentElement.scrollHeight, vh: innerHeight }))
  dbg("height", h, vh)
  for (let y = 0; y < h; y += Math.round(vh * 0.6)) {
    dbg("s", y)
    await page.evaluate((y) => scrollTo(0, y), y)
    dbg("e", y)
    await page.waitForTimeout(30)
  }
  dbg("loop done")
  await page.evaluate(() => scrollTo(0, 0))
  dbg("top")
  // Polled from here: the page's own timers and animation frames are the paused clock's.
  for (let t = 0; t < 100; t++) {
    if (await page.evaluate(() => Array.from(document.images).every((i) => i.complete || (i.loading === "lazy" && !i.getClientRects().length)))) break
    await page.waitForTimeout(100)
  }
  await page.evaluate(async () => {
    // Only images that loaded (a lazy image in a closed menu never does).
    const decode = Promise.all(Array.from(document.images).filter((i) => i.complete && i.naturalWidth).map((i) => i.decode().catch(() => undefined)))
    await decode
    await document.fonts.ready
  })
  await page.waitForTimeout(150)
}

async function runSteps(page, steps) {
  for (const s of steps || []) {
    if (s.click) await page.click(s.click)
    if (s.hover) await page.hover(s.hover)
    if (s.focus) await page.focus(s.focus)
    if (s.press) await page.keyboard.press(s.press)
    if (s.scroll !== undefined) await page.evaluate((y) => scrollTo(0, y), s.scroll)
    if (s.wait) await tick(page, s.wait)
  }
  await page.waitForTimeout(100)
}

async function shot(page, fullPage) {
  return page.screenshot({ fullPage, animations: 'disabled', caret: 'hide', scale: 'css' })
}

/* ---------- comparing (in a browser page: canvas + YIQ distance, as builder/test/parity) ---------- */

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
      const rows = new Uint32Array(h)
      for (let y = 0; y < h; y++)
        for (let x = 0; x < w; x++) {
          const o = (y * w + x) * 4
          const outside = x >= ia.width || y >= ia.height || x >= ib.width || y >= ib.height
          let d = outside
          if (!d) {
            const r1 = pa[o], g1 = pa[o + 1], b1 = pa[o + 2], r2 = pb[o], g2 = pb[o + 1], b2 = pb[o + 2]
            const yy = (r1 - r2) * 0.29889531 + (g1 - g2) * 0.58662247 + (b1 - b2) * 0.11448223
            const ii = (r1 - r2) * 0.59597799 - (g1 - g2) * 0.2741761 - (b1 - b2) * 0.32180189
            const qq = (r1 - r2) * 0.21147017 - (g1 - g2) * 0.52261711 + (b1 - b2) * 0.31114694
            d = 0.5053 * yy * yy + 0.299 * ii * ii + 0.1957 * qq * qq > max
          }
          if (d) {
            diff++
            rows[y]++
            od.data[o] = 255; od.data[o + 1] = 0; od.data[o + 2] = 0; od.data[o + 3] = 255
          } else {
            const v = 255 - (255 - (pa[o] * 0.299 + pa[o + 1] * 0.587 + pa[o + 2] * 0.114)) * 0.15
            od.data[o] = v; od.data[o + 1] = v; od.data[o + 2] = v; od.data[o + 3] = 255
          }
        }
      let png = ''
      if (diff) {
        og.putImageData(od, 0, 0)
        const bytes = new Uint8Array(await (await out.convertToBlob({ type: 'image/png' })).arrayBuffer())
        let s = ''
        for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
        png = btoa(s)
      }
      const bands = []
      for (let y = 0; y < h; y++) {
        if (!rows[y]) continue
        const last = bands[bands.length - 1]
        if (last && y - last[1] <= 8) {
          last[1] = y
          last[2] += rows[y]
        } else bands.push([y, y, rows[y]])
      }
      return { w, h, sizeA: [ia.width, ia.height], sizeB: [ib.width, ib.height], diff, png, bands }
    },
    [a.toString('base64'), b.toString('base64'), threshold],
  )
  return { ...r, ratio: r.diff / (r.w * r.h), png: r.png ? Buffer.from(r.png, 'base64') : null }
}

/* ---------- the run ---------- */

const jobs = []
for (const p of pages) for (const w of WIDTHS) jobs.push({ ...p, width: w, name: 'page', steps: null, fullPage: true })
for (const sc of scenarios) {
  const list = sc.pages && sc.pages !== '*' ? pages.filter((p) => sc.pages.includes(p.file)) : pages
  for (const p of list)
    for (const w of sc.widths || WIDTHS) jobs.push({ ...p, width: w, name: sc.name, steps: sc.steps, fullPage: sc.fullPage ?? false })
}

const results = []
const slugOf = (j) => `${j.file.replace(/[^A-Za-z0-9]+/g, '_')}-${j.name}-${j.width}`
// One side at a time: a page loads, settles, runs the scenario's steps and is shot alone (a
// second page open in the browser can stall the first one's rendering).
async function capture(url, j) {
  const side = await open(url, j.width)
  try {
    if (j.steps) await runSteps(side.page, j.steps)
    return { png: await shot(side.page, j.fullPage), status: side.status, errors: side.errors, console: side.console }
  } finally {
    await side.ctx.close()
  }
}

async function one(j) {
  const src = await capture(`${ORIGIN}/${j.file}`, j)
  // --self compares the source with itself: the noise floor of the measurement.
  const dst = await capture(args.self ? `${ORIGIN}/${j.file}` : `${SITE}${j.url}`, j)
  {
    const [a, b] = [src.png, dst.png]
    const d = await compare(a, b)
    const pct = d.ratio * 100
    const row = { file: j.file, url: j.url, scenario: j.name, width: j.width, diffPct: Number(pct.toFixed(4)), diffPx: d.diff,
      sizeSource: d.sizeA, sizeTarget: d.sizeB, bands: d.bands.slice(0, 20), status: dst.status, ok: pct <= TARGET,
      errorsSource: src.errors, errorsTarget: dst.errors, consoleSource: src.console, consoleTarget: dst.console }
    if (IMAGES === 'all' || d.diff > 0) {
      const base = slugOf(j)
      fs.writeFileSync(path.join(OUT, 'img', `${base}.source.png`), a)
      fs.writeFileSync(path.join(OUT, 'img', `${base}.target.png`), b)
      if (d.png) fs.writeFileSync(path.join(OUT, 'img', `${base}.diff.png`), d.png)
      row.images = base
    }
    results.push(row)
    console.log(`${row.ok ? 'ok  ' : 'FAIL'} ${pct.toFixed(4).padStart(8)} %  ${String(j.width).padStart(4)}  ${j.name === 'page' ? '' : `[${j.name}] `}${j.file} → ${j.url}`)
    // What the target reports and the source does not (origins and kit paths differ, so by message shape).
    const shape = (m) => m.replace(/https?:\/\/[^\s'")]+/g, 'URL')
    const seen = new Set([...src.console, ...src.errors].map(shape))
    for (const m of [...dst.errors, ...dst.console]) if (!seen.has(shape(m))) console.log(`       target only: ${m}`)
  }
}

let next = 0
await Promise.all(
  Array.from({ length: Math.max(1, JOBS) }, async () => {
    while (next < jobs.length) {
      const j = jobs[next++]
      try {
        await one(j)
      } catch (e) {
        results.push({ file: j.file, url: j.url, scenario: j.name, width: j.width, ok: false, error: String(e.message || e) })
        console.log(`ERR  ${j.file} ${j.width}: ${e.message || e}`)
      }
    }
  }),
)
await browser.close()
server.close()

/* ---------- the report ---------- */

results.sort((a, b) => a.file.localeCompare(b.file) || a.scenario.localeCompare(b.scenario) || b.width - a.width)
const vals = results.filter((r) => typeof r.diffPct === 'number').map((r) => r.diffPct).sort((a, b) => a - b)
const summary = {
  site: SITE, source: SRC, target: TARGET, shots: results.length, failed: results.filter((r) => !r.ok).length,
  max: vals.length ? vals[vals.length - 1] : null, median: vals.length ? vals[Math.floor(vals.length / 2)] : null,
  byWidth: Object.fromEntries(WIDTHS.map((w) => {
    const v = results.filter((r) => r.width === w && typeof r.diffPct === 'number').map((r) => r.diffPct).sort((a, b) => a - b)
    return [w, { max: v[v.length - 1] ?? null, median: v[Math.floor(v.length / 2)] ?? null }]
  })),
}
fs.writeFileSync(path.join(OUT, 'report.json'), JSON.stringify({ summary, results }, null, 2))
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c])
const rows = results.map((r) => `<tr class="${r.ok ? 'ok' : 'bad'}"><td>${esc(r.file)}</td><td>${esc(r.url)}</td><td>${esc(r.scenario)}</td><td>${r.width}</td>
<td>${r.error ? esc(r.error) : `${r.diffPct.toFixed(4)} %`}</td><td>${r.sizeSource ? `${r.sizeSource.join('×')} / ${r.sizeTarget.join('×')}` : ''}</td>
<td>${r.images ? ['source', 'target', 'diff'].map((k) => `<a href="img/${r.images}.${k}.png">${k}</a>`).join(' ') : ''}</td></tr>`).join('\n')
const shots = results.filter((r) => r.images && !r.ok).slice(0, 60).map((r) => `<section><h2>${esc(r.file)} · ${r.width}px · ${esc(r.scenario)} · ${r.diffPct.toFixed(4)} %</h2>
<div class="row">${['source', 'target', 'diff'].map((k) => `<figure><figcaption>${k}</figcaption><a href="img/${r.images}.${k}.png"><img loading="lazy" src="img/${r.images}.${k}.png"></a></figure>`).join('')}</div></section>`).join('\n')
fs.writeFileSync(path.join(OUT, 'report.html'), `<!doctype html><meta charset="utf-8"><title>Import fidelity</title>
<style>body{font:14px system-ui,sans-serif;margin:24px;color:#222}table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:4px 8px;text-align:left}
tr.bad td{background:#fff0f0}tr.ok td:nth-child(5){color:#070}.row{display:flex;gap:8px;align-items:flex-start}figure{margin:0;flex:1}img{width:100%;border:1px solid #ccc}</style>
<h1>Import fidelity</h1><p>${esc(SRC)} → ${esc(SITE)} · target ≤ ${TARGET} % · ${summary.shots} shots, ${summary.failed} over · max ${summary.max} %, median ${summary.median} %</p>
<table><tr><th>file</th><th>url</th><th>scenario</th><th>width</th><th>diff</th><th>size source / target</th><th>images</th></tr>${rows}</table>${shots}`)
console.log(`\n${summary.shots} shots, ${summary.failed} over ${TARGET} %, max ${summary.max} %, median ${summary.median} % → ${path.join(OUT, 'report.html')}`)
process.exit(summary.failed ? 1 : 0)
