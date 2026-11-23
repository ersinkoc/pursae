/**
 * Static thumbnails of design markup: the document a small sandboxed frame shows (the palette's
 * "From this design" samples and Basic mode's section list rows).
 *
 * Nothing in it runs: the markup is the frame's neutralised copy (neutral.ts: scripts become inert,
 * handlers and unsafe addresses go, embeds become placeholders), the head's scripts are inert too
 * (frame.mergeHeads in static mode), and a Content-Security-Policy meta forbids scripts and plugins
 * again; the frames that show it are sandboxed without allow-scripts (SectionThumb, PaletteDrawer).
 * The stored markup is never touched.
 */
import type { Section } from '@/builder/types'
import { embedCss } from './embeds'
import { frameCopy, mergeHeads } from './frame'
import { sectionsCSS } from './local'
import type { LocalSection } from './local'
import { sourcesHash } from './miner'

/** The sandbox of a thumbnail frame: same origin (for the design's stylesheets and measuring), never scripts. */
export const THUMB_SANDBOX = 'allow-same-origin'

export interface ThumbInput {
  /** The design's head markup (stylesheets; its scripts are made inert). */
  head: string
  /** The body's class attribute value. */
  body: string
  /** The markup to show (neutralised here). */
  markup: string
  /** The design kit's override stylesheet and the markup's local styles. */
  css?: string
  base: string
  /** The frame's width in px: a sample is laid out at most this wide, a section exactly this wide. */
  width: number
  /** A sample is padded and shrunk to its content; a section fills the width. */
  fit: 'content' | 'page'
}

const NO_SCRIPTS = `<meta http-equiv="Content-Security-Policy" content="script-src 'none'; object-src 'none'">`

export function thumbDoc(i: ThumbInput): string {
  const body = i.body.replace(/"/g, '&quot;')
  const layout =
    i.fit === 'content'
      ? `body>div[data-wb-thumb]{display:flow-root;padding:12px;width:max-content;max-width:${i.width - 24}px}`
      : `body>div[data-wb-thumb]{display:flow-root;width:${i.width}px;max-height:${i.width}px;overflow:hidden}`
  return (
    `<!doctype html><html><head><meta charset="utf-8">${NO_SCRIPTS}<base href="${i.base.replace(/"/g, '&quot;')}">${i.head}<style data-wb-kit>${(i.css ?? '').replace(/<\//g, '<\\/')}</style><style>${embedCss()}</style>` +
    `<style>html,body{margin:0!important;background:transparent!important;overflow:hidden!important}${layout}</style></head>` +
    `<body class="${body}"><div data-wb-thumb>${frameCopy(i.markup, 'static')}</div></body></html>`
  )
}

/* ---------- A code section's thumbnail ---------- */

/** The width a section thumbnail is laid out at (a desktop viewport); the frame is scaled down from it. */
export const SECTION_THUMB_W = 1200

/** Cached documents by markup hash: a row that scrolls out and in, or is redrawn, builds nothing again. */
const CACHE_MAX = 200
const cache = new Map<string, string>()
export const thumbCache = {
  get size() {
    return cache.size
  },
  clear: () => cache.clear(),
}
/** How many documents were built (not served from the cache): for tests. */
export const thumbStats = { built: 0 }

/** What the thumbnail of a section depends on, hashed: its markup, head, body class, local styles and the kit styles. */
export function thumbKey(s: Section, css: string): string {
  const c = s.content
  return sourcesHash([{ html: c.html ?? '' }, { html: c.head ?? '' }, { html: c.body ?? '' }, { html: css }])
}

/** The local styles and kit styles a section's thumbnail draws with. */
export function thumbCss(s: Section, kitCss: string): string {
  return kitCss + sectionsCSS([s as LocalSection], undefined)
}

/** The thumbnail document of a code section (cached), or null when it has no markup to show. */
export function sectionThumbDoc(s: Section, kitCss: string, base: string): string | null {
  const markup = s.content.html ?? ''
  if (!markup.trim()) return null
  const css = thumbCss(s, kitCss)
  const key = thumbKey(s, css)
  const hit = cache.get(key)
  if (hit !== undefined) {
    // most recently used last
    cache.delete(key)
    cache.set(key, hit)
    return hit
  }
  thumbStats.built++
  const doc = thumbDoc({ head: mergeHeads([s.content.head ?? ''], 'static'), body: s.content.body ?? '', markup, css, base, width: SECTION_THUMB_W, fit: 'page' })
  cache.set(key, doc)
  if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value as string)
  return doc
}
