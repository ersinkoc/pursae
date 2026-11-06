/**
 * Serves both sides of the parity check from disk through Playwright request routing (no server
 * process): every request of a page goes to `route()`, nothing reaches the network.
 *
 *   /live.html?f=<name>      harness/live.tsx (side a, the editor's live preview)
 *   /static.html?f=<name>    the static snapshot (side b): render.html + site/wb.css + render.css +
 *                            the site runtime when the snapshot lists features, laid out like the
 *                            server's canvas page (internal/server/webbuilder_front.go wbCanvas)
 *   /live/**                 the built harness (builder/.work/parity/live)
 *   /site/**                 web/web-builder/site
 *   /fixtures/<name>.doc.json  internal/webbuilder/testdata/render
 *   images.unsplash.com      one fixed local image, so both sides draw the same pixels
 *
 * Both sides use the same @font-face rules (the editor stylesheet's local fonts); the static page
 * gets them as an inline style instead of the server's font links.
 */
import fs from 'node:fs'
import path from 'node:path'
import zlib from 'node:zlib'
import type { BrowserContext, Page, Route } from '@playwright/test'
import { OUT as LIVE_OUT, REPO } from './build-harness.mjs'

export const ORIGIN = 'http://parity.localhost'
export const FIXTURES = path.join(REPO, 'internal', 'webbuilder', 'testdata', 'render')
const SITE = path.join(REPO, 'web', 'web-builder', 'site')

export interface RenderResult {
  html: string
  css: string
  classes: string[]
  fonts: { family: string; weights: number[]; italic: boolean }[]
  features: string[]
  renderer: number
}

/** Fixture names (every <name>.render.json with its doc). */
export function fixtureNames(): string[] {
  return fs
    .readdirSync(FIXTURES)
    .filter((f) => f.endsWith('.render.json'))
    .map((f) => f.slice(0, -'.render.json'.length))
    .filter((n) => fs.existsSync(path.join(FIXTURES, `${n}.doc.json`)))
    .sort()
}

export function readRender(name: string): RenderResult {
  return JSON.parse(fs.readFileSync(path.join(FIXTURES, `${name}.render.json`), 'utf8'))
}

let cache: { entry: { js: string; css: string[] }; fontFaces: string; site: { runtime: string; css: string } } | null = null
function assets() {
  if (cache) return cache
  const entry = JSON.parse(fs.readFileSync(path.join(LIVE_OUT, 'entry.json'), 'utf8'))
  const css = entry.css.map((f: string) => fs.readFileSync(path.join(LIVE_OUT, f), 'utf8')).join('\n')
  const fontFaces = (css.match(/@font-face\s*\{[^}]*\}/g) ?? [])
    .map((r: string) => r.replace(/url\(\s*['"]?(?:\.\/)?([^'")]+)['"]?\s*\)/g, (_m: string, f: string) => `url('/live/assets/${path.basename(f)}')`))
    .join('\n')
  if (!fontFaces) throw new Error('parity: the editor stylesheet has no @font-face rules')
  const manifest = JSON.parse(fs.readFileSync(path.join(REPO, 'web', 'web-builder', 'manifest.json'), 'utf8'))
  cache = { entry, fontFaces, site: manifest.site }
  return cache
}

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;')

function livePage(): string {
  const a = assets()
  // Canvas fills a full-height editor pane with its own scroller; let the page scroll the document
  // instead, and drop the device frame (label, padding, rounded shadowed box) so only the page remains.
  return `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>parity live</title>
${a.entry.css.map((f) => `<link rel="stylesheet" href="/live/${f}">`).join('\n')}
<style>
html,body,#root{height:auto!important}
html,body{margin:0;background:#fff!important;overflow:visible!important}
#root>div{overflow:visible!important;background:#fff!important}
#root>div>div{position:static!important;overflow:visible!important;padding:0!important;background:none!important}
#root>div>div>.absolute{display:none!important}
#root [data-preview]{border-radius:0!important;box-shadow:none!important;overflow:visible!important}
</style>
</head><body><div id="root"></div><script type="module" src="/live/${a.entry.js}"></script></body></html>`
}

/**
 * Tabs markup of contract §5 (no shipped design draws tabs yet, but the runtime supports them): appended
 * to a snapshot page by /static.html?f=<name>&x=tabs.
 */
export const TABS_MARKUP = `<section class="sf" id="parity-tabs"><div data-wb-tabs="">
<div role="tablist" aria-label="Plans">
<button type="button" role="tab" id="pt-a" data-wb-tab="a" aria-controls="pp-a" aria-selected="false">Starter</button>
<button type="button" role="tab" id="pt-b" data-wb-tab="b" aria-controls="pp-b" aria-selected="false">Team</button>
<button type="button" role="tab" id="pt-c" data-wb-tab="c" aria-controls="pp-c" aria-selected="false">Business</button>
</div>
<div role="tabpanel" id="pp-a" data-wb-panel="a" aria-labelledby="pt-a"><p>Starter panel</p></div>
<div role="tabpanel" id="pp-b" data-wb-panel="b" aria-labelledby="pt-b" data-wb-inactive=""><p>Team panel</p></div>
<div role="tabpanel" id="pp-c" data-wb-panel="c" aria-labelledby="pt-c" data-wb-inactive=""><p>Business panel</p></div>
</div></section>`

/** The static snapshot page; `extra` (synthetic markup for runtime checks) goes at the end of the page root. */
export function staticPage(name: string, extra = ''): string {
  const a = assets()
  const r = readRender(name)
  const runtime = r.features.length > 0 || extra !== ''
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
${runtime ? "<script>document.documentElement.classList.add('wb-js')</script>\n" : ''}<title>${esc(name)}</title>
<link rel='stylesheet' id='web-builder-css' href='/site/${a.site.css}' media='all' />
<style id="wb-fonts">
${a.fontFaces}
</style>
<style id="wb-css">
${r.css}
</style>
</head>
<body class="web-builder-page">
${extra ? r.html.replace(/<\/div>$/, `${extra}</div>`) : r.html}
${runtime ? `<script defer src="/site/${a.site.runtime}" id="web-builder-runtime-js"></script>\n` : ''}</body>
</html>
`
}

/* ---------- A fixed image for remote image URLs (CRC/zlib PNG, no dependencies) ---------- */
function crc32(buf: Buffer): number {
  let c = ~0
  for (const b of buf) {
    c ^= b
    for (let k = 0; k < 8; k++) c = c & 1 ? (c >>> 1) ^ 0xedb88320 : c >>> 1
  }
  return ~c >>> 0
}
function chunk(type: string, data: Buffer): Buffer {
  const len = Buffer.alloc(4)
  len.writeUInt32BE(data.length)
  const td = Buffer.concat([Buffer.from(type, 'ascii'), data])
  const crc = Buffer.alloc(4)
  crc.writeUInt32BE(crc32(td))
  return Buffer.concat([len, td, crc])
}
/** 320×200 diagonal gradient PNG (detail on every row, so a misplaced or mis-cropped image shows). */
function placeholderPng(): Buffer {
  const w = 320
  const h = 200
  const raw = Buffer.alloc((w * 3 + 1) * h)
  for (let y = 0; y < h; y++) {
    const row = y * (w * 3 + 1)
    raw[row] = 0
    for (let x = 0; x < w; x++) {
      const o = row + 1 + x * 3
      raw[o] = (x * 255) / w
      raw[o + 1] = (y * 255) / h
      raw[o + 2] = ((x + y) * 4) & 255
    }
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0)
  ihdr.writeUInt32BE(h, 4)
  ihdr[8] = 8
  ihdr[9] = 2
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk('IHDR', ihdr), chunk('IDAT', zlib.deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}
const PLACEHOLDER = placeholderPng()

const TYPES: Record<string, string> = {
  '.js': 'text/javascript',
  '.css': 'text/css',
  '.json': 'application/json',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
}

function file(route: Route, root: string, rel: string) {
  const p = path.resolve(root, rel)
  if (!p.startsWith(root + path.sep) || !fs.existsSync(p) || !fs.statSync(p).isFile()) return route.fulfill({ status: 404, body: 'not found' })
  return route.fulfill({ status: 200, body: fs.readFileSync(p), contentType: TYPES[path.extname(p)] ?? 'application/octet-stream' })
}

/** Remote requests the pages made (aborted), for the report. */
export const blocked: string[] = []

async function handle(route: Route) {
  const u = new URL(route.request().url())
  if (u.hostname === 'images.unsplash.com') return route.fulfill({ status: 200, body: PLACEHOLDER, contentType: 'image/png' })
  if (u.origin !== ORIGIN) {
    blocked.push(u.origin + u.pathname)
    return route.abort('blockedbyclient')
  }
  const f = u.searchParams.get('f') ?? ''
  const html = (body: string) => route.fulfill({ status: 200, body, contentType: 'text/html; charset=utf-8' })
  if (u.pathname === '/live.html') return html(livePage())
  if (u.pathname === '/static.html') {
    if (!fixtureNames().includes(f)) return route.fulfill({ status: 404, body: 'no fixture' })
    return html(staticPage(f, u.searchParams.get('x') === 'tabs' ? TABS_MARKUP : ''))
  }
  if (u.pathname.startsWith('/live/')) return file(route, LIVE_OUT, decodeURIComponent(u.pathname.slice(6)))
  if (u.pathname.startsWith('/site/')) return file(route, SITE, decodeURIComponent(u.pathname.slice(6)))
  if (u.pathname.startsWith('/fixtures/')) return file(route, FIXTURES, decodeURIComponent(u.pathname.slice(10)))
  return route.fulfill({ status: 404, body: 'not found' })
}

export async function serve(ctx: BrowserContext) {
  await ctx.route('**/*', handle)
}

/* ---------- Settling a page before a screenshot ---------- */

/**
 * Makes a page deterministic: fonts loaded, every lazy image and on-view entrance triggered (scrolls
 * through the page), images decoded, finite animations finished and endless ones cancelled, back at
 * the top.
 */
export async function settle(page: Page) {
  await page.evaluate(() => document.fonts.ready)
  const { h, vh } = await page.evaluate(() => ({ h: document.documentElement.scrollHeight, vh: innerHeight }))
  for (let y = 0; y < h; y += Math.round(vh * 0.7)) {
    await page.evaluate((y) => scrollTo(0, y), y)
    await page.waitForTimeout(40)
  }
  await page.evaluate(() => scrollTo(0, 0))
  await page.waitForFunction(() => Array.from(document.images).every((i) => i.complete), null, { timeout: 15_000 })
  await page.waitForTimeout(700)
  await page.evaluate(async () => {
    await Promise.all(Array.from(document.images).map((i) => i.decode().catch(() => undefined)))
    await document.fonts.ready
    for (const a of document.getAnimations()) {
      const end = a.effect?.getComputedTiming().endTime
      if (typeof end === 'number' && isFinite(end)) a.finish()
      else a.cancel()
    }
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
  })
}

/** Full-height screenshot of `selector` (document coordinates, page scrolled to the top). */
export async function shoot(page: Page, selector: string): Promise<Buffer> {
  const box = await page.evaluate((sel) => {
    const el = document.querySelector(sel)
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { x: r.left + scrollX, y: r.top + scrollY, width: r.width, height: r.height }
  }, selector)
  if (!box || box.width < 1 || box.height < 1) throw new Error(`nothing to shoot: ${selector}`)
  return page.screenshot({ fullPage: true, clip: box, animations: 'disabled', caret: 'hide', scale: 'css' })
}

/* ---------- Pixel comparison (in the browser: canvas + YIQ distance, like pixelmatch without AA detection) ---------- */

export interface Diff {
  width: number
  height: number
  sizeA: [number, number]
  sizeB: [number, number]
  diff: number
  ratio: number
  /** Vertical bands that differ: [top, bottom, differing pixels]. */
  bands: [number, number, number][]
  /** Red-on-gray diff image (PNG), only when something differs. */
  png?: Buffer
}

export async function compare(page: Page, a: Buffer, b: Buffer, threshold = 0.1): Promise<Diff> {
  const r = await page.evaluate(
    async ([a64, b64, threshold]) => {
      const load = async (s: string) => createImageBitmap(await (await fetch(`data:image/png;base64,${s}`)).blob())
      const [ia, ib] = await Promise.all([load(a64 as string), load(b64 as string)])
      const w = Math.max(ia.width, ib.width)
      const h = Math.max(ia.height, ib.height)
      const px = (img: ImageBitmap) => {
        const c = new OffscreenCanvas(w, h)
        const g = c.getContext('2d')!
        g.drawImage(img, 0, 0)
        return g.getImageData(0, 0, w, h).data
      }
      const pa = px(ia)
      const pb = px(ib)
      const out = new OffscreenCanvas(w, h)
      const og = out.getContext('2d')!
      const od = og.createImageData(w, h)
      const max = 35215 * (threshold as number) * (threshold as number)
      const yiq = (r1: number, g1: number, b1: number, r2: number, g2: number, b2: number) => {
        const y = (r1 - r2) * 0.29889531 + (g1 - g2) * 0.58662247 + (b1 - b2) * 0.11448223
        const i = (r1 - r2) * 0.59597799 - (g1 - g2) * 0.2741761 - (b1 - b2) * 0.32180189
        const q = (r1 - r2) * 0.21147017 - (g1 - g2) * 0.52261711 + (b1 - b2) * 0.31114694
        return 0.5053 * y * y + 0.299 * i * i + 0.1957 * q * q
      }
      let diff = 0
      const rows = new Uint32Array(h)
      for (let y = 0; y < h; y++)
        for (let x = 0; x < w; x++) {
          const o = (y * w + x) * 4
          const outside = x >= ia.width || y >= ia.height || x >= ib.width || y >= ib.height
          const d = outside || yiq(pa[o], pa[o + 1], pa[o + 2], pb[o], pb[o + 1], pb[o + 2]) > max
          if (d) {
            diff++
            rows[y]++
            od.data.set([255, 0, 0, 255], o)
          } else {
            const v = 255 - (255 - (pa[o] * 0.299 + pa[o + 1] * 0.587 + pa[o + 2] * 0.114)) * 0.15
            od.data.set([v, v, v, 255], o)
          }
        }
      let png = ''
      if (diff) {
        og.putImageData(od, 0, 0)
        const blob = await out.convertToBlob({ type: 'image/png' })
        const bytes = new Uint8Array(await blob.arrayBuffer())
        let s = ''
        for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
        png = btoa(s)
      }
      // Vertical bands with differences (rows closer than 8 px merged): [top, bottom, pixels].
      const bands: [number, number, number][] = []
      for (let y = 0; y < h; y++) {
        if (!rows[y]) continue
        const last = bands[bands.length - 1]
        if (last && y - last[1] <= 8) {
          last[1] = y
          last[2] += rows[y]
        } else bands.push([y, y, rows[y]])
      }
      return { width: w, height: h, sizeA: [ia.width, ia.height], sizeB: [ib.width, ib.height], diff, png, bands }
    },
    [a.toString('base64'), b.toString('base64'), threshold] as const,
  )
  return {
    width: r.width,
    height: r.height,
    sizeA: r.sizeA as [number, number],
    sizeB: r.sizeB as [number, number],
    diff: r.diff,
    ratio: r.diff / (r.width * r.height),
    bands: r.bands as [number, number, number][],
    png: r.png ? Buffer.from(r.png, 'base64') : undefined,
  }
}
