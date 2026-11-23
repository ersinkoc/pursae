/**
 * Element types (plan §4.2, K4): derived from the tag and the computed display, never stored.
 */
import { AppWindow, Box, ChevronsUpDown, CodeXml, Columns3, Film, GalleryHorizontal, GalleryVerticalEnd, Heading, Hexagon, Image as ImageIcon, LayoutGrid, LayoutList, LayoutTemplate, Link2, List, ListTree, MapPin, Menu, Minus, Newspaper, PanelBottom, PanelRight, PanelsTopLeft, PanelTop, PenTool, Pilcrow, Quote, RectangleHorizontal, Shapes, Sparkles, Square, Table, Tag, TextCursorInput, Volume2, type LucideIcon } from 'lucide-react'
import { attr, decode, elementKids, ownText, textOf, type El, type Tree } from './html'
import { CODE_TAGS } from './ops'
import { t } from '../i18n'
import { tk } from './kinds-tr'

export type Kind =
  | 'section' | 'flex' | 'grid' | 'div' | 'heading' | 'text' | 'link' | 'button' | 'image' | 'icon' | 'video' | 'list' | 'form' | 'code'
  | 'canvas' | 'menu' | 'menuitems' | 'header' | 'footer' | 'main' | 'aside' | 'article' | 'figure' | 'table' | 'accordion' | 'popup' | 'quote' | 'divider'
  | 'audio' | 'map' | 'field' | 'inputbutton' | 'slider' | 'tabs' | 'cards' | 'hero' | 'badge' | 'logo' | 'graphic'

/** Phrasing elements: a text element holds only these. */
const INLINE = new Set(['a', 'abbr', 'b', 'bdi', 'bdo', 'br', 'cite', 'code', 'data', 'dfn', 'em', 'i', 'kbd', 'mark', 'q', 's', 'samp', 'small', 'span', 'strong', 'sub', 'sup', 'time', 'u', 'var', 'wbr', 'del', 'ins'])
const TEXTY = new Set(['p', 'span', 'li', 'blockquote', 'label', 'figcaption', 'small', 'strong', 'em', 'b', 'i', 'td', 'th', 'dt', 'dd', 'summary', 'cite', 'q', 'time', 'mark', 'address', 'pre', 'code', 'legend', 'caption'])

/** Tailwind-style display classes, for rows the canvas has not measured. */
function classDisplay(el: El): string {
  const cls = (attr(el, 'class') ?? '').split(/\s+/)
  if (cls.includes('grid') || cls.includes('inline-grid')) return 'grid'
  if (cls.includes('flex') || cls.includes('inline-flex')) return 'flex'
  return ''
}

const classes = (el: El): string[] => (attr(el, 'class') ?? '').split(/\s+/).filter(Boolean)
const roleOf = (el: El): string => (attr(el, 'role') ?? '').trim().toLowerCase()
const BRAND = /(^|[\s_-])(logo|brand|site-title)([\s_-]|$)/i

function inside(el: El, pred: (p: El) => boolean): boolean {
  for (let p = el.parent; p && p.tag !== '#root'; p = p.parent) if (pred(p)) return true
  return false
}

/** Counts descendants matching pred, stopping at limit. */
function count(el: El, pred: (e: El) => boolean, limit = 400): number {
  let n = 0
  const walk = (e: El) => {
    for (const k of elementKids(e)) {
      if (n >= limit) return
      if (pred(k)) n++
      walk(k)
    }
  }
  walk(el)
  return n
}

/** An svg is an icon (small, or in a button / link) or a graphic (big: an illustration or a diagram). */
function svgKind(el: El): Kind {
  if (BRAND.test(`${attr(el, 'class') ?? ''} ${attr(el, 'id') ?? ''} ${attr(el, 'aria-label') ?? ''}`)) return 'logo'
  if (inside(el, (p) => p.tag === 'button' || p.tag === 'a' || p.tag === 'summary' || p.tag === 'label')) return 'icon'
  const size = Math.max(parseFloat(attr(el, 'width') ?? '') || 0, parseFloat(attr(el, 'height') ?? '') || 0)
  if (size > 64) return 'graphic'
  const sized = attr(el, 'width') != null || attr(el, 'height') != null
  if (!sized && (attr(el, 'viewbox') ?? attr(el, 'viewBox')) && count(el, (e) => e.tag === 'path' || e.tag === 'polygon' || e.tag === 'circle' || e.tag === 'rect') > 8) return 'graphic'
  return 'icon'
}

const NOT_CARD = new Set(['a', 'button', 'span', 'input', 'option', 'tr', 'td', 'th', 'dt', 'dd', 'summary', 'label', 'p', 'img', 'svg', 'hr', 'br', 'li'])
const repeated = new WeakMap<El, boolean>()

/** Three or more alike children that each have media, or a heading and text: a set of cards. */
function isCards(tr: Tree, el: El): boolean {
  const hit = repeated.get(el)
  if (hit !== undefined) return hit
  let ok = false
  const kids = elementKids(el).filter((k) => !CODE_TAGS.has(k.tag))
  if (kids.length >= 3) {
    const sig = (k: El) => `${k.tag}|${classes(k).sort().join(' ')}`
    const tally = new Map<string, number>()
    for (const k of kids) tally.set(sig(k), (tally.get(sig(k)) ?? 0) + 1)
    const [best, n] = [...tally.entries()].sort((a, b) => b[1] - a[1])[0]
    const tag = best.split('|')[0]
    // A list's items are cards too (li), a plain run of links, spans or table cells is not.
    if (n >= 3 && n >= kids.length * 0.75 && (!NOT_CARD.has(tag) || (tag === 'li' && (el.tag === 'ul' || el.tag === 'ol')))) {
      const rich = (k: El) => {
        const media = count(k, (e) => e.tag === 'img' || e.tag === 'picture' || e.tag === 'video', 1) > 0
        const head = count(k, (e) => /^h[1-6]$/.test(e.tag), 1) > 0
        const para = count(k, (e) => e.tag === 'p' || ((e.tag === 'span' || e.tag === 'div') && ownText(tr, e)), 1) > 0
        return media || (head && (para || ownText(tr, k)))
      }
      ok = kids.filter((k) => sig(k) === best).every(rich)
    }
  }
  repeated.set(el, ok)
  return ok
}

const SLIDER = /^(swiper|swiper-container|carousel|slider|slick-slider|splide|glide|owl-carousel|keen-slider|embla)$|^(carousel|slider)[-_](container|wrapper|root)$|[-_](carousel|slider)$/
const isSlider = (el: El): boolean => !['input', 'a', 'button', 'li'].includes(el.tag) && classes(el).some((c) => SLIDER.test(c))
const isTabs = (el: El): boolean => roleOf(el) === 'tablist' || classes(el).some((c) => /^(tabs|tablist|tab-list|tab-nav)$/.test(c))
const isHero = (el: El): boolean => !['a', 'button'].includes(el.tag) && !/^h[1-6]$/.test(el.tag) && classes(el).some((c) => /^(hero|hero[-_].*|.*[-_]hero)$/.test(c)) && !/^hero[-_](title|text|subtitle|heading|content|image|img|button|btn|cta|desc|description|inner|copy|media|actions)$/.test(classes(el).find((c) => /^hero/.test(c)) ?? '')
function isBadge(tr: Tree, el: El): boolean {
  if (!['span', 'div', 'p', 'small', 'b', 'strong', 'i', 'em', 'li'].includes(el.tag)) return false
  return classes(el).some((c) => /^(badge|chip|pill)$|[-_](badge|chip|pill)$|^(badge|chip|pill)[-_]/.test(c)) && textOf(tr, el, 40).length <= 30 && !elementKids(el).some((k) => k.tag === 'div')
}

function iframeKind(el: El): Kind {
  const src = (attr(el, 'src') ?? attr(el, 'data-src') ?? '').toLowerCase()
  if (/(google\.[a-z.]+\/maps|maps\.google|openstreetmap|mapbox|maps\.app\.goo\.gl|\/maps\/embed)/.test(src)) return 'map'
  return 'video' // YouTube, Vimeo and other embeds
}

/**
 * The element's type. display: its computed display when known (the canvas measures it), else
 * guessed from display classes. Finer types (menu, slider, cards ...) come from the tag, the role
 * and common class names of the source; every value has an icon in KIND_ICON.
 */
export function kindOf(tr: Tree, el: El, display?: string): Kind {
  const tag = el.tag
  if (CODE_TAGS.has(tag)) return 'code'
  if (tag === 'canvas') return 'canvas'
  if (/^h[1-6]$/.test(tag)) return 'heading'
  if (tag === 'img' || tag === 'picture') {
    const brand = BRAND.test(`${attr(el, 'class') ?? ''} ${attr(el, 'id') ?? ''} ${attr(el, 'alt') ?? ''}`)
    const linked = tag === 'img' && el.parent?.tag === 'a' && elementKids(el.parent).length === 1 && inside(el, (p) => p.tag === 'header' || p.tag === 'nav')
    return brand || linked ? 'logo' : 'image'
  }
  if (tag === 'svg') return svgKind(el)
  if (tag === 'audio') return 'audio'
  if (tag === 'iframe') return iframeKind(el)
  if (tag === 'video' || tag === 'embed' || tag === 'object') return 'video'
  if (tag === 'form') return 'form'
  if (tag === 'input') return /^(submit|button|reset|image)$/i.test(attr(el, 'type') ?? '') ? 'inputbutton' : 'field'
  if (tag === 'select' || tag === 'textarea' || tag === 'label' || tag === 'fieldset') return 'field'
  const role = roleOf(el)
  if (tag === 'nav' || role === 'navigation') return 'menu'
  if (tag === 'header' || role === 'banner') return 'header'
  if (tag === 'footer' || role === 'contentinfo') return 'footer'
  if (tag === 'main' || role === 'main') return 'main'
  if (tag === 'aside' || role === 'complementary') return 'aside'
  if (tag === 'article') return 'article'
  if (tag === 'figure') return 'figure'
  if (tag === 'table') return 'table'
  if (tag === 'details') return 'accordion'
  if (tag === 'dialog' || role === 'dialog' || role === 'alertdialog' || attr(el, 'aria-modal') === 'true') return 'popup'
  if (tag === 'blockquote') return 'quote'
  if (tag === 'hr' || role === 'separator') return 'divider'
  if (isTabs(el)) return 'tabs'
  if (isSlider(el)) return 'slider'
  if (isHero(el)) return 'hero'
  if (el.parent?.tag === '#root') return 'section'
  if (tag === 'button') return 'button'
  if (tag === 'a') return 'link'
  if (tag === 'ul' || tag === 'ol') {
    if (role === 'menu' || role === 'menubar' || inside(el, (p) => p.tag === 'nav' || roleOf(p) === 'navigation')) return 'menuitems'
    return isCards(tr, el) ? 'cards' : 'list'
  }
  if (tag === 'dl') return 'list'
  if (isBadge(tr, el)) return 'badge'
  if (!['li', 'dd', 'dt', 'p'].includes(tag) && isCards(tr, el)) return 'cards'
  if (BRAND.test(`${attr(el, 'class') ?? ''} ${attr(el, 'id') ?? ''}`) && !textOf(tr, el, 5)) return 'logo'
  const kids = elementKids(el)
  const d = display ?? classDisplay(el)
  if (kids.length && (d === 'flex' || d === 'inline-flex')) return 'flex'
  if (kids.length && (d === 'grid' || d === 'inline-grid')) return 'grid'
  // Text: phrasing content only (a link laid out as a flex card is not phrasing).
  const phrasing = (e: El): boolean => elementKids(e).every((k) => INLINE.has(k.tag) && !classDisplay(k) && !/(^|\s)(block|inline-block)(\s|$)/.test(attr(k, 'class') ?? '') && phrasing(k))
  if (phrasing(el) && (TEXTY.has(tag) || ownText(tr, el))) return 'text'
  return 'div'
}

export const KIND_ICON: Record<Kind, LucideIcon> = {
  section: LayoutTemplate,
  flex: Columns3,
  grid: LayoutGrid,
  div: Square,
  heading: Heading,
  text: Pilcrow,
  link: Link2,
  button: RectangleHorizontal,
  image: ImageIcon,
  icon: Shapes,
  video: Film,
  list: List,
  form: TextCursorInput,
  code: CodeXml,
  canvas: Box,
  menu: Menu,
  menuitems: ListTree,
  header: PanelTop,
  footer: PanelBottom,
  main: PanelsTopLeft,
  aside: PanelRight,
  article: Newspaper,
  figure: ImageIcon,
  table: Table,
  accordion: ChevronsUpDown,
  popup: AppWindow,
  quote: Quote,
  divider: Minus,
  audio: Volume2,
  map: MapPin,
  field: TextCursorInput,
  inputbutton: RectangleHorizontal,
  slider: GalleryHorizontal,
  tabs: LayoutList,
  cards: GalleryVerticalEnd,
  hero: Sparkles,
  badge: Tag,
  logo: Hexagon,
  graphic: PenTool,
}

export function kindLabel(k: Kind): string {
  switch (k) {
    case 'section':
      return t('Section')
    case 'flex':
      return t('Flexbox')
    case 'grid':
      return t('Grid')
    case 'div':
      return t('Div')
    case 'heading':
      return t('Heading')
    case 'text':
      return t('Text')
    case 'link':
      return t('Link')
    case 'button':
    case 'inputbutton':
      return t('Button')
    case 'image':
      return t('Image')
    case 'icon':
      return t('Icon')
    case 'video':
      return t('Video / embed')
    case 'list':
      return t('List')
    case 'code':
      return t('Code')
    case 'form':
      return tk('Form')
    case 'canvas':
      return tk('3D / Canvas')
    case 'menu':
      return tk('Menu')
    case 'menuitems':
      return tk('Menu items')
    case 'header':
      return tk('Header')
    case 'footer':
      return tk('Footer')
    case 'main':
      return tk('Main content')
    case 'aside':
      return tk('Sidebar')
    case 'article':
      return tk('Article')
    case 'figure':
      return tk('Figure')
    case 'table':
      return tk('Table')
    case 'accordion':
      return tk('Accordion')
    case 'popup':
      return tk('Popup')
    case 'quote':
      return tk('Quote')
    case 'divider':
      return tk('Divider')
    case 'audio':
      return tk('Audio')
    case 'map':
      return tk('Map')
    case 'field':
      return tk('Field')
    case 'slider':
      return tk('Slider')
    case 'tabs':
      return tk('Tabs')
    case 'cards':
      return tk('Cards')
    case 'hero':
      return tk('Hero')
    case 'badge':
      return tk('Badge')
    case 'logo':
      return tk('Logo')
    case 'graphic':
      return tk('Graphic')
  }
}

/** tag, then #id or the first class: "div.mx-auto". */
export function tagName(el: El): string {
  const id = attr(el, 'id')
  const cls = (attr(el, 'class') ?? '').trim().split(/\s+/)[0]
  return el.tag + (id ? `#${id}` : cls ? `.${cls}` : '')
}

/** A short description of the element: its text, alt text or tag name. */
export function snippet(tr: Tree, el: El, max = 32): string {
  if (el.tag === 'svg') return ''
  const v = el.tag === 'img' ? decode(attr(el, 'alt') ?? '') : textOf(tr, el, max + 8)
  const s = v.replace(/\s+/g, ' ').trim()
  return s.length > max ? `${s.slice(0, max - 1)}…` : s
}

/** The names the user gave elements (content.names: JSON map data-wbc id → name). */
export function namesOf(json: string | undefined): Record<string, string> {
  if (!json) return {}
  try {
    const v = JSON.parse(json) as unknown
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {}
    const out: Record<string, string> = {}
    for (const [k, x] of Object.entries(v)) if (typeof x === 'string' && x) out[k] = x
    return out
  } catch {
    return {}
  }
}

/* ---------- Names ---------- */

/** Utility classes (Tailwind, Bootstrap, WordPress state classes), never a meaningful name. */
const UTILITY =
  /^(?:-?(?:m|p|mx|my|mt|mb|ml|mr|px|py|pt|pb|pl|pr|w|h|min-w|max-w|min-h|max-h|size|gap|space|text|font|leading|tracking|bg|border|rounded|shadow|ring|opacity|z|top|left|right|bottom|inset|flex|grid|col|row|order|items|justify|content|self|place|overflow|object|aspect|transition|duration|ease|delay|animate|cursor|select|pointer|fill|stroke|divide|outline|translate|rotate|scale|skew|origin|backdrop|blur|brightness|grayscale|basis|grow|shrink|columns|break|whitespace|list|decoration|underline|align|float|clear|appearance|resize|scroll|snap|touch|will|mix|filter|sepia|invert|saturate|contrast|hue|drop|accent|caret|sr|is|has|js|no-js|wp-element|alignwide|alignfull|alignleft|alignright|aligncenter|d|g|offset|ms|me|ps|pe|fw|fs|lh|aos)(?:-.*)?|block|inline|inline-block|inline-flex|inline-grid|hidden|visible|invisible|static|fixed|absolute|relative|sticky|container|container-fluid|group|peer|dark|italic|uppercase|lowercase|capitalize|truncate|antialiased|isolate|contents|table|prose|clearfix|active|open|show|collapse|fade|in|on|off|selected|current|disabled|ready|loaded|animated|reveal|wow)$/i

/** A class name as words: "pricing-card" becomes "Pricing card". null for utility and generated classes. */
export function humanClass(cls: string): string | null {
  if (!cls || cls.includes(':') || cls.includes('[') || cls.includes('/') || UTILITY.test(cls)) return null
  if (/\d{3,}|^(css|sc|jsx|svelte|astro|framer)[-_]|^_/.test(cls) || /[A-Za-z0-9]{14,}/.test(cls.replace(/[-_]/g, ''))) return null
  let c = cls.replace(/^wp-block-/, '').replace(/^elementor-/, '').replace(/^et_pb_/, '')
  c = c
    .replace(/--[a-z0-9-]*$/i, '')
    .replace(/__/g, ' ')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .replace(/[-_]+/g, ' ')
    .trim()
  if (c.length < 2 || c.length > 40 || !/[a-z]/i.test(c) || /^\d/.test(c) || /\s\d+$/.test(c)) return null
  return c.charAt(0).toUpperCase() + c.slice(1).toLowerCase()
}

/** The first meaningful class of an element, as words. */
export function className(el: El): string | null {
  for (const c of (attr(el, 'class') ?? '').split(/\s+/)) {
    const h = humanClass(c)
    if (h) return h
  }
  return null
}

/** An id as words ("pricing-section" becomes "Pricing section"); generated or generic ids are skipped. */
function idName(el: El): string | null {
  const id = attr(el, 'id')
  if (!id || /^(root|app|main|content|wrapper|page|__next|__nuxt)$/i.test(id)) return null
  return humanClass(id)
}

function clip(s: string, max: number): string {
  const v = s.replace(/\s+/g, ' ').trim()
  return v.length > max ? `${v.slice(0, max - 1)}…` : v
}

/** The text of the first heading inside the element (or of the element itself). */
export function firstHeading(tr: Tree, el: El, max = 40): string {
  let out = ''
  const walk = (e: El): boolean => {
    if (/^h[1-6]$/.test(e.tag)) {
      const v = textOf(tr, e, max + 8)
      if (v) {
        out = v
        return true
      }
    }
    if (e.tag === 'svg' || e.tag === 'script' || e.tag === 'style') return false
    return elementKids(e).some(walk)
  }
  walk(el)
  return clip(out, max)
}

const TEXT_KINDS = new Set<Kind>(['heading', 'text', 'link', 'button', 'badge', 'quote'])

/**
 * What a row is called besides its type: aria-label, alt, the heading or text, an id, a class as
 * words. Never a raw "div.mx-auto". Empty when nothing meaningful is found. Derived from the
 * source, never stored (the user's own rename lives in content.names and wins).
 */
export function autoName(tr: Tree, el: El, kind: Kind = kindOf(tr, el), max = 28): string {
  const aria = decode(attr(el, 'aria-label') ?? '').trim()
  if (aria) return clip(aria, max)
  if (el.tag === 'img') return clip(decode(attr(el, 'alt') ?? ''), max)
  if (kind === 'code') {
    const src = attr(el, 'src') ?? attr(el, 'href')
    return src ? clip(src.split(/[?#]/)[0].split('/').pop() ?? '', max) : ''
  }
  if (el.tag === 'iframe') return clip(decode(attr(el, 'title') ?? ''), max)
  if (kind === 'field' || kind === 'inputbutton') return clip(decode(attr(el, 'placeholder') ?? attr(el, 'value') ?? attr(el, 'name') ?? ''), max)
  if (TEXT_KINDS.has(kind) || (kind === 'logo' && el.tag !== 'img' && el.tag !== 'svg')) {
    const s = snippet(tr, el, max)
    if (s) return s
  } else if (kind !== 'divider' && kind !== 'canvas' && kind !== 'graphic' && kind !== 'icon' && kind !== 'logo') {
    const h = firstHeading(tr, el, max)
    if (h) return h
    if (ownText(tr, el)) {
      const s = snippet(tr, el, max)
      if (s) return s
    }
  }
  return clip(idName(el) ?? className(el) ?? '', max)
}

/** Kinds whose row shows how many items they hold ("Cards · 6"). */
const COUNTED = new Set<Kind>(['cards', 'menuitems', 'list', 'slider', 'tabs'])

/** "Cards · 6" for a repeated set (two or more children), else the plain type. */
export function labelWithCount(kind: Kind, n: number): string {
  return COUNTED.has(kind) && n >= 2 ? `${kindLabel(kind)} · ${n}` : kindLabel(kind)
}

const LANDMARK = new Set<Kind>(['header', 'footer', 'main', 'menu', 'aside', 'hero', 'article', 'form', 'slider', 'table', 'canvas'])

/**
 * The name of a top-level child of a code section ("Section 3" at worst): its first heading, then
 * its landmark (Header, Menu, Footer ...), aria-label, id or class as words. The Basic panel uses
 * the same order.
 */
export function sectionName(tr: Tree, el: El, index: number): string {
  const h = firstHeading(tr, el)
  if (h) return h
  const k = kindOf(tr, el)
  if (LANDMARK.has(k)) return kindLabel(k)
  const aria = decode(attr(el, 'aria-label') ?? '').trim()
  if (aria) return clip(aria, 40)
  const n = idName(el) ?? className(el)
  if (n) return n
  return tk('Section {n}', { n: index + 1 })
}

/**
 * Splits child ids into the visible blocks (document order) and the quiet code elements (script,
 * style, template, noscript, link, meta), which go last and collapsed so they stop dominating.
 */
export function groupKids(tr: Tree, ids: string[]): { blocks: string[]; quiet: string[] } {
  const blocks: string[] = []
  const quiet: string[] = []
  for (const id of ids) {
    const el = tr.byId.get(id)
    if (el && CODE_TAGS.has(el.tag)) quiet.push(id)
    else blocks.push(id)
  }
  return { blocks, quiet }
}

/** Rows rendered at once per parent; the rest sit behind "Show N more". */
export const ROW_CAP = 60
