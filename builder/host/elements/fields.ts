/**
 * Friendly fields of a code section (the Basic mode's blocks, BasicCode.tsx): headings, texts,
 * buttons and links (label and address), images (source and alt text), form field placeholders and
 * the source of a video or embed, found in document order in the section's stored markup — read
 * from the source tree (html.ts), never from the frame's DOM, so what the panel shows is what is
 * stored and an edit splices the source like every other element operation (ops.ts).
 *
 * Repeated siblings (a card grid, list items, slides, table rows: the same tag and class list at
 * least twice, each holding fields) become groups ("Card 1", "Card 2" …) with their own fields;
 * the same link or button class three or more times side by side (a nav) becomes one "Links" group.
 * Empty, hidden and aria-hidden elements, scripts, styles and svg internals are left out, and so is
 * any element without a data-wbc id (nothing can address it).
 *
 * Labels here are English keys; BasicCode.tsx translates them (basic-tr.ts).
 */
import { attr, decode, elementKids, escText, ownText, wbc, type El, type Tree } from './html'
import { tree } from './ops'

export type FieldKind = 'heading' | 'text' | 'button' | 'link' | 'image' | 'input' | 'video'

export interface Field {
  type: 'field'
  kind: FieldKind
  /** The element's data-wbc id (selection, edits). */
  id: string
  tag: string
  /** Heading level 1-6, else 0. */
  level: number
  /** What the user edits as text: plain (no tags inside: a text box) or rich (inline formatting, kept as stored); null: no text. */
  edit: 'plain' | 'rich' | null
  /** The text as shown (whitespace collapsed). */
  text: string
  /** A link's address (an `a` element), else null. */
  href: string | null
  /** An image's source / a video's or embed's source, else null. */
  src: string | null
  /** The element that holds the source (a video's source child), else id. */
  srcId: string
  /** An image's alt text, else null. */
  alt: string | null
  /** A form field's placeholder, else null. */
  placeholder: string | null
  /** One short line naming the field when it has no text of its own (an image's file name, an address). */
  hint: string
}

export interface Group {
  type: 'group'
  /** Stable key (the repeated element's id). */
  key: string
  /** The element's id (a click on the header selects it), null when it has none. */
  id: string | null
  /** "Card" | "Item" | "Slide" | "Row" | "Links". */
  noun: GroupNoun
  /** 1-based position among its siblings of the same shape (0 for "Links"). */
  n: number
  /** The group's first heading or text, for its header. */
  snippet: string
  items: Item[]
  /** Fields inside, nested groups included. */
  count: number
}

export type GroupNoun = 'Card' | 'Item' | 'Slide' | 'Row' | 'Links' | 'Buttons'
export type Item = Field | Group

/** Hard bound on one section's fields (a pathological table); the panel shows 60 at a time. */
const MAX_FIELDS = 600
const SKIP = new Set(['script', 'style', 'noscript', 'template', 'link', 'meta', 'base', 'title', 'head', 'svg', 'math', 'canvas', 'source', 'track', 'param', 'map', 'area', 'select', 'option', 'optgroup', 'datalist', 'br', 'hr', 'wbr', 'object'])
const INLINE = new Set(['a', 'abbr', 'b', 'bdi', 'bdo', 'br', 'cite', 'code', 'data', 'dfn', 'em', 'i', 'kbd', 'mark', 'q', 's', 'samp', 'small', 'span', 'strong', 'sub', 'sup', 'time', 'u', 'var', 'wbr', 'del', 'ins', 'svg', 'label'])
const TEXTY = new Set(['p', 'span', 'li', 'blockquote', 'label', 'figcaption', 'small', 'strong', 'em', 'b', 'i', 'td', 'th', 'dt', 'dd', 'summary', 'cite', 'q', 'time', 'mark', 'address', 'pre', 'code', 'legend', 'caption'])
const GROUP_TAGS = new Set(['li', 'article', 'tr', 'dd', 'figure', 'details'])
const BUTTONISH = /(^|[\s_-])(btn|button|cta)([\s_-]|$)/i

const collapse = (s: string) => s.replace(/\s+/g, ' ').trim()
const clip = (s: string, n = 40) => (s.length > n ? `${s.slice(0, n - 1)}…` : s)

/**
 * The text a reader sees in el, whitespace collapsed: inline elements join their neighbours without
 * a space, blocks and line breaks separate; scripts, styles, svg and aria-hidden parts are left out.
 */
function shown(tr: Tree, el: El): string {
  let out = ''
  const walk = (e: El) => {
    for (const c of e.children) {
      if (c.type === 'text') out += decode(tr.src.slice(c.start, c.end))
      else if (c.type === 'el') {
        if (c.tag === 'script' || c.tag === 'style' || c.tag === 'svg' || c.tag === 'template' || c.foreign || attr(c, 'aria-hidden') === 'true') continue
        const inline = INLINE.has(c.tag)
        if (!inline || c.tag === 'br') out += ' '
        walk(c)
        if (!inline) out += ' '
      }
    }
  }
  walk(el)
  return collapse(out)
}

/** The element is invisible to the reader: leave it (and what is in it) out. */
function hidden(el: El): boolean {
  if (attr(el, 'hidden') !== null) return true
  if (attr(el, 'aria-hidden') === 'true') return true
  const st = attr(el, 'style')
  return !!st && /(^|;)\s*display\s*:\s*none\b/i.test(st)
}

/** Phrasing content only (inline elements all the way down). */
const phrasing = (el: El): boolean => elementKids(el).every((k) => k.tag === 'svg' || (INLINE.has(k.tag) && phrasing(k)))

const hasLink = (el: El) => elementKids(el).some((k) => k.tag === 'a')

function editMode(tr: Tree, el: El): 'plain' | 'rich' | null {
  if (!shown(tr, el)) return null
  if (el.children.every((c) => c.type === 'text')) return 'plain'
  return phrasing(el) ? 'rich' : null
}

function base(tr: Tree, el: El, kind: FieldKind, id: string): Field {
  const level = /^h([1-6])$/.test(el.tag) ? Number(el.tag[1]) : 0
  return { type: 'field', kind, id, tag: el.tag, level, edit: null, text: '', href: null, src: null, srcId: id, alt: null, placeholder: null, hint: '' }
}

const fileName = (u: string) => {
  const p = u.split(/[?#]/)[0].split('/').filter(Boolean)
  return p.length ? decodeURIComponent(p[p.length - 1]).slice(0, 60) : ''
}

/** What the element is for the panel: a field, a container to look into, or nothing. */
function field(tr: Tree, el: El): Field | 'container' | null {
  const tag = el.tag
  if (SKIP.has(tag) || el.foreign || hidden(el)) return null
  const id = wbc(el)
  if (/^h[1-6]$/.test(tag)) {
    const text = shown(tr, el)
    if (!text || !id) return null
    return { ...base(tr, el, 'heading', id), edit: editMode(tr, el), text, hint: clip(text) }
  }
  if (tag === 'img') {
    if (!id) return null
    const src = attr(el, 'src') ?? ''
    return { ...base(tr, el, 'image', id), src, alt: decode(attr(el, 'alt') ?? ''), hint: fileName(src) }
  }
  if (tag === 'picture') return 'container'
  if (tag === 'input' || tag === 'textarea') {
    const ph = attr(el, 'placeholder')
    const type = (attr(el, 'type') ?? 'text').toLowerCase()
    if (!id || ph === null || ['hidden', 'submit', 'button', 'reset', 'image', 'checkbox', 'radio', 'file'].includes(type)) return null
    return { ...base(tr, el, 'input', id), placeholder: ph, hint: clip(collapse(ph)) }
  }
  if (tag === 'video' || tag === 'audio' || tag === 'iframe' || tag === 'embed') {
    if (!id) return null
    let srcId = id
    let src = attr(el, 'src')
    if (src === null) {
      const s = elementKids(el).find((k) => k.tag === 'source' && wbc(k) && attr(k, 'src'))
      if (s) {
        src = attr(s, 'src')
        srcId = wbc(s)!
      }
    }
    if (src === null) return null
    return { ...base(tr, el, 'video', id), src, srcId, hint: clip(src, 50) }
  }
  if (tag === 'button' || tag === 'a') {
    const text = shown(tr, el)
    const isBtn = tag === 'button' || BUTTONISH.test(attr(el, 'class') ?? '')
    const kind: FieldKind = isBtn ? 'button' : 'link'
    const inlineOnly = elementKids(el).every((k) => INLINE.has(k.tag) || k.tag === 'svg')
    if (tag === 'button' && !text) return inlineOnly ? null : 'container'
    if (!id) return inlineOnly ? null : 'container'
    const href = tag === 'a' ? (attr(el, 'href') ?? '') : null
    const label = text || collapse(decode(attr(el, 'aria-label') ?? attr(el, 'title') ?? ''))
    if (!inlineOnly) {
      // A card that is a link: its address here, what is inside as fields of their own.
      return { ...base(tr, el, kind, id), href, text: label, hint: clip(label || href || ''), edit: null }
    }
    if (!label && !href) return null
    return { ...base(tr, el, kind, id), href, text: text, edit: editMode(tr, el), hint: clip(label || href || '') }
  }
  // Text: phrasing content that says something (a link filling the element is a link, below).
  const text = shown(tr, el)
  if (!text) return 'container'
  const texty = (TEXTY.has(tag) || ownText(tr, el)) && phrasing(el) && !(hasLink(el) && !ownText(tr, el))
  if (texty && id) return { ...base(tr, el, 'text', id), edit: editMode(tr, el), text, hint: clip(text) }
  return 'container'
}

interface Entry {
  el: El
  items: Item[]
  /** A link or button on its own (not a card / container). */
  leaf: boolean
}

const sigOf = (el: El) => `${el.tag}|${(attr(el, 'class') ?? '').split(/\s+/).filter(Boolean).sort().join(' ')}`

function noun(el: El): GroupNoun {
  const cls = attr(el, 'class') ?? ''
  if (/slide|carousel|swiper/i.test(cls)) return 'Slide'
  if (el.tag === 'tr') return 'Row'
  if (el.tag === 'li' || el.tag === 'dd' || el.tag === 'details') return 'Item'
  return 'Card'
}

const count = (items: Item[]): number => items.reduce((n, i) => n + (i.type === 'field' ? 1 : i.count), 0)

function snippetOf(items: Item[]): string {
  for (const i of items) {
    if (i.type === 'field') {
      if ((i.kind === 'heading' || i.kind === 'text') && i.text) return clip(i.text, 32)
    } else if (i.snippet) return i.snippet
  }
  for (const i of items) if (i.type === 'field' && (i.text || i.hint)) return clip(i.text || i.hint, 32)
  return ''
}

function walk(tr: Tree, el: El, budget: { left: number }): Item[] {
  const entries: Entry[] = []
  for (const kid of elementKids(el)) {
    if (budget.left <= 0) break
    const f = field(tr, kid)
    if (f === null) continue
    if (f === 'container') {
      const items = walk(tr, kid, budget)
      if (items.length) entries.push({ el: kid, items, leaf: false })
      continue
    }
    budget.left--
    // A link that holds blocks (a card): its own field, then what is inside it.
    const inside = (f.kind === 'link' || f.kind === 'button') && !f.edit && elementKids(kid).some((k) => !INLINE.has(k.tag) && k.tag !== 'svg') ? walk(tr, kid, budget) : []
    entries.push({ el: kid, items: [f, ...inside], leaf: (f.kind === 'link' || f.kind === 'button') && !inside.length })
  }
  const containers = new Map<string, number>()
  const leaves = new Map<string, number>()
  for (const e of entries) {
    const sig = sigOf(e.el)
    if (e.leaf) leaves.set(sig, (leaves.get(sig) ?? 0) + 1)
    else if ((attr(e.el, 'class') ?? '').trim() || GROUP_TAGS.has(e.el.tag)) containers.set(sig, (containers.get(sig) ?? 0) + 1)
  }
  const out: Item[] = []
  const seen = new Map<string, number>()
  const done = new Set<string>()
  for (const e of entries) {
    const sig = sigOf(e.el)
    if (!e.leaf && (containers.get(sig) ?? 0) >= 2) {
      const n = (seen.get(sig) ?? 0) + 1
      seen.set(sig, n)
      out.push({ type: 'group', key: wbc(e.el) ?? `@${e.el.start}`, id: wbc(e.el), noun: noun(e.el), n, snippet: snippetOf(e.items), items: e.items, count: count(e.items) })
    } else if (e.leaf && (leaves.get(sig) ?? 0) >= 3) {
      if (done.has(sig)) continue
      done.add(sig)
      const same = entries.filter((x) => x.leaf && sigOf(x.el) === sig)
      const items = same.flatMap((x) => x.items)
      out.push({ type: 'group', key: wbc(e.el) ?? `@${e.el.start}`, id: null, noun: items.every((i) => i.type === 'field' && i.kind === 'button') ? 'Buttons' : 'Links', n: 0, snippet: snippetOf(items), items, count: items.length })
    } else out.push(...e.items)
  }
  return out
}

/**
 * The new inner markup of a plain-text element after the user typed `value`: the text escaped, the
 * whitespace around it (the design's indentation) kept.
 */
export function plainInner(innerHtml: string, value: string): string {
  const lead = /^\s*/.exec(innerHtml)![0]
  const trail = innerHtml.length > lead.length ? /\s*$/.exec(innerHtml)![0] : ''
  return lead + escText(value.replace(/\s+/g, ' ').trim()) + trail
}

/** The section's fields in document order (repeated siblings grouped). */
export function extractFields(html: string): Item[] {
  if (!html.trim()) return []
  const tr = tree(html)
  return walk(tr, tr.root, { left: MAX_FIELDS })
}

/** Every field, groups opened. */
export function flatFields(items: Item[]): Field[] {
  const out: Field[] = []
  for (const i of items) {
    if (i.type === 'field') out.push(i)
    else out.push(...flatFields(i.items))
  }
  return out
}

/** The first `n` fields (a group cut short keeps what fits), and how many fields there are in all. */
export function limitItems(items: Item[], n: number): { items: Item[]; total: number } {
  const total = count(items)
  if (total <= n) return { items, total }
  let left = n
  const cut = (list: Item[]): Item[] => {
    const out: Item[] = []
    for (const i of list) {
      if (left <= 0) break
      if (i.type === 'field') {
        out.push(i)
        left--
      } else {
        const inner = cut(i.items)
        out.push({ ...i, items: inner, count: count(inner) })
      }
    }
    return out
  }
  return { items: cut(items), total }
}

/** Ids of the groups on the path to field `id` (to open them), null when no field has it. */
export function pathTo(items: Item[], id: string): string[] | null {
  for (const i of items) {
    if (i.type === 'field') {
      if (i.id === id) return []
    } else {
      const p = pathTo(i.items, id)
      if (p) return [i.key, ...p]
    }
  }
  return null
}

/**
 * The field a selected element stands for: the element itself, else the nearest field around it,
 * else the first field inside it (a selected card or container).
 */
export function fieldFor(items: Item[], tr: Tree, sel: string): Field | null {
  const all = flatFields(items)
  const byId = new Map(all.map((f) => [f.id, f]))
  const el = tr.byId.get(sel)
  if (!el) return null
  for (let x: El | null = el; x; x = x.parent) {
    const id = x === el ? sel : wbc(x)
    const f = id ? byId.get(id) : undefined
    if (f) return f
  }
  for (const f of all) {
    const fe = tr.byId.get(f.id)
    for (let x: El | null = fe ?? null; x; x = x.parent) if (x === el) return f
  }
  return null
}

/* ---------- Summary (the section list row) ---------- */

export interface Counts {
  headings: number
  texts: number
  buttons: number
  links: number
  images: number
  fields: number
  videos: number
}

export function countKinds(items: Item[]): Counts {
  const c: Counts = { headings: 0, texts: 0, buttons: 0, links: 0, images: 0, fields: 0, videos: 0 }
  for (const f of flatFields(items)) {
    if (f.kind === 'heading') c.headings++
    else if (f.kind === 'text') c.texts++
    else if (f.kind === 'button') c.buttons++
    else if (f.kind === 'link') c.links++
    else if (f.kind === 'image') c.images++
    else if (f.kind === 'input') c.fields++
    else c.videos++
  }
  return c
}

/** Class and id words that name a part of a page. */
const WORDS: [RegExp, string][] = [
  [/hero|banner|jumbotron|masthead/i, 'Hero'],
  [/feature/i, 'Features'],
  [/pric(e|ing)|plans?\b/i, 'Pricing'],
  [/testimonial|review|quote/i, 'Testimonials'],
  [/faq|accordion/i, 'FAQ'],
  [/contact/i, 'Contact'],
  [/footer/i, 'Footer'],
  [/nav|menu/i, 'Menu'],
  [/header/i, 'Header'],
  [/cta|call-to-action/i, 'Call to action'],
  [/about/i, 'About'],
  [/team/i, 'Team'],
  [/gallery|portfolio/i, 'Gallery'],
  [/blog|news|posts?\b/i, 'Blog'],
  [/logo|client|partner|brand/i, 'Logos'],
  [/stat|counter|number/i, 'Numbers'],
  [/service/i, 'Services'],
  [/slider|carousel|swiper/i, 'Slider'],
  [/form|subscribe|newsletter/i, 'Form'],
  [/process|steps?\b|how/i, 'Steps'],
]

export interface Naming {
  /** A name an author gave it (aria-label, or a recognised id / class), else the tag's. */
  name: string | null
  /** The top-level element is header / footer / nav (its landmark wins over a heading's text). */
  landmark: boolean
  /** The name is the author's own (an aria-label or an id), not one of ours to translate. */
  custom?: boolean
}

/** The name of a section from its top-level element: landmark tag, aria-label, id, classes. */
export function nameOf(tr: Tree): Naming {
  const top = elementKids(tr.root).filter((e) => !SKIP.has(e.tag))
  const main = top.find((e) => !hidden(e)) ?? null
  if (!main) return { name: null, landmark: false }
  if (main.tag === 'header') return { name: 'Header', landmark: true }
  if (main.tag === 'footer') return { name: 'Footer', landmark: true }
  if (main.tag === 'nav') return { name: 'Menu', landmark: true }
  const label = collapse(decode(attr(main, 'aria-label') ?? ''))
  if (label) return { name: clip(label, 40), landmark: false, custom: true }
  const hay = `${attr(main, 'id') ?? ''} ${attr(main, 'class') ?? ''}`
  for (const [re, name] of WORDS) if (re.test(hay)) return { name, landmark: false }
  const id = attr(main, 'id')
  if (id && /^[A-Za-z][\w-]{1,30}$/.test(id) && !/\d{3,}/.test(id)) {
    const words = id.replace(/[-_]+/g, ' ').replace(/([a-z])([A-Z])/g, '$1 $2').trim()
    return { name: words.charAt(0).toUpperCase() + words.slice(1), landmark: false, custom: true }
  }
  return { name: null, landmark: false }
}

export interface Summary {
  /** Heading text, else a name for the part (named: an English key to translate); null: nothing to say (the section's own name stays). */
  title: string | null
  named: boolean
  counts: Counts
}

const cache = new Map<string, Summary>()

/** A section's title and counts (cached by markup). */
export function summarize(html: string): Summary {
  let s = cache.get(html)
  if (s) return s
  if (cache.size > 128) cache.delete(cache.keys().next().value!)
  if (!html.trim()) s = { title: null, named: false, counts: countKinds([]) }
  else {
    const tr = tree(html)
    const items = extractFields(html)
    const nm = nameOf(tr)
    const first = flatFields(items).find((f) => f.kind === 'heading')
    const byName = nm.landmark || !first
    s = { title: byName ? nm.name : clip(first.text, 60), named: byName && !!nm.name && nm.name !== null && !nm.custom, counts: countKinds(items) }
  }
  cache.set(html, s)
  return s
}
