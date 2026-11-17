/**
 * The "From this design" palette (plan Faz 4.2): ready elements mined from the design's own
 * markup. An element whose tag and class list (order ignored) appear at least MIN_COUNT times in
 * the code sections of the design's pages is a building block of the design — its buttons,
 * headings, cards, badges, list items, sections — and inserting a copy of one sample (with fresh
 * ids, its text kept as the placeholder) gives a new element that looks like the design's. In a
 * utility-class design (Tailwind) this is the practical way to add elements.
 *
 * Pure functions over the source-preserving parser (html.ts); no DOM. The catalog is computed in
 * the editor (on first open of the palette, from the kit's pages, GET kits/{slug}/pages) and kept
 * per kit version and markup hash (palette cache, kitstyle.ts).
 */
import { attr, elementKids, outer, ownText, parse, textOf, type El, type Tree } from './html'
import { kindOf } from './kinds'
import { CODE_TAGS } from './ops'

export type SampleKind = 'button' | 'link' | 'heading' | 'text' | 'badge' | 'card' | 'list-item' | 'image' | 'section' | 'container'

export interface Sample {
  /** tag + sorted classes: "a btn btn-primary". */
  sig: string
  kind: SampleKind
  tag: string
  classes: string[]
  /** Occurrences in all sources. */
  count: number
  /** Sources (pages) it appears on. */
  pages: number
  /** The sample's markup (the first occurrence, as written). */
  html: string
  /** A short text of the sample (its label). */
  text: string
}

export interface MineSource {
  html: string
  /** Page id or label (counts pages). */
  page?: string
}

export const MIN_COUNT = 3
const MAX_SAMPLE_BYTES = 24 << 10
const SKIP_TAGS = new Set(['html', 'body', 'head', 'main', 'header', 'footer', 'nav', 'br', 'option', 'source', 'track', 'path', 'g', 'svg', 'use', 'symbol', 'defs', 'tbody', 'thead', 'tr', 'td', 'th', 'col', 'colgroup', 'picture', 'canvas', 'iframe', 'form'])

export const KIND_ORDER: SampleKind[] = ['button', 'heading', 'card', 'badge', 'text', 'link', 'list-item', 'image', 'section', 'container']

const clsOf = (el: El) => (attr(el, 'class') ?? '').split(/\s+/).filter(Boolean)

/** The element's class signature (null when it has no class or is not a candidate). */
export function signature(el: El): string | null {
  if (el.foreign || el.tag === '#root' || CODE_TAGS.has(el.tag) || SKIP_TAGS.has(el.tag)) return null
  const cls = clsOf(el)
  if (!cls.length) return null
  return el.tag + ' ' + [...new Set(cls)].sort().join(' ')
}

const hasClass = (cls: string[], re: RegExp) => cls.some((c) => re.test(c))

/** What a repeated element is, for the palette's groups. */
export function sampleKind(t: Tree, el: El): SampleKind {
  const k = kindOf(t, el)
  const cls = clsOf(el)
  const text = textOf(t, el, 80).trim()
  if (el.tag === 'li') return 'list-item'
  if (k === 'heading') return 'heading'
  if (k === 'image') return 'image'
  if (el.tag === 'button' || (el.tag === 'a' && (hasClass(cls, /(^|-)(btn|button)(-|$)/) || (hasClass(cls, /^(bg|border)-/) && hasClass(cls, /^(px|py|p)-/))))) return 'button'
  if (el.tag === 'a') return 'link'
  const kids = elementKids(el)
  if ((el.tag === 'span' || el.tag === 'small' || el.tag === 'div') && text.length > 0 && text.length <= 32 && kids.length <= 2 && hasClass(cls, /^(rounded|bg-|badge|tag|chip|pill|px-|uppercase|text-xs)/)) return 'badge'
  if (k === 'text') return 'text'
  if (el.tag === 'section' || (el.tag === 'article' && kids.length > 3)) return 'section'
  // A card: a box with a heading or an image and some text.
  const all = (e: El): El[] => elementKids(e).flatMap((x) => [x, ...all(x)])
  const inside = all(el)
  const heading = inside.some((x) => /^h[1-6]$/.test(x.tag) || x.tag === 'strong')
  const media = inside.some((x) => x.tag === 'img' || x.tag === 'svg' || x.tag === 'picture')
  const para = inside.some((x) => x.tag === 'p' || (x.tag === 'span' && ownText(t, x)))
  if ((el.tag === 'article' || el.tag === 'div' || el.tag === 'a' || el.tag === 'li') && inside.length >= 2 && inside.length <= 60 && (heading || media) && para) return 'card'
  return 'container'
}

/**
 * The repeated class signatures of the sources (≥ MIN_COUNT occurrences), most useful first:
 * by kind (buttons, headings, cards, …), then by how often they occur. A signature nested in a
 * sample of the same signature counts once per occurrence all the same.
 */
export function mineSignatures(sources: MineSource[], opts: { min?: number; max?: number } = {}): Sample[] {
  const min = opts.min ?? MIN_COUNT
  const found = new Map<string, Sample & { pageSet: Set<string> }>()
  sources.forEach((s, i) => {
    if (!s.html) return
    const t = parse(s.html)
    for (const el of t.all) {
      const sig = signature(el)
      if (!sig) continue
      const hit = found.get(sig)
      const page = s.page ?? String(i)
      if (hit) {
        hit.count++
        hit.pageSet.add(page)
        continue
      }
      const html = outer(t, el)
      if (html.length > MAX_SAMPLE_BYTES || /<(script|style|template|iframe|form)\b/i.test(html.slice(el.openEnd - el.start))) continue
      found.set(sig, {
        sig,
        kind: sampleKind(t, el),
        tag: el.tag,
        classes: clsOf(el),
        count: 1,
        pages: 0,
        html,
        text: el.tag === 'img' ? (attr(el, 'alt') ?? '') : textOf(t, el, 60).replace(/\s+/g, ' ').trim(),
        pageSet: new Set([page]),
      })
    }
  })
  const out: Sample[] = []
  for (const { pageSet, ...s } of found.values()) {
    if (s.count < min) continue
    out.push({ ...s, pages: pageSet.size })
  }
  out.sort((a, b) => KIND_ORDER.indexOf(a.kind) - KIND_ORDER.indexOf(b.kind) || b.count - a.count || (a.sig < b.sig ? -1 : 1))
  return opts.max ? out.slice(0, opts.max) : out
}

/** How many elements of the sources carry class `cls`. */
export function countClass(sources: MineSource[], cls: string): { elements: number; pages: number } {
  let elements = 0
  const pages = new Set<string>()
  const re = new RegExp(`(^|\\s)${cls.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(\\s|$)`)
  sources.forEach((s, i) => {
    let n = 0
    for (const m of s.html.matchAll(/\sclass\s*=\s*(?:"([^"]*)"|'([^']*)')/g)) if (re.test(m[1] ?? m[2] ?? '')) n++
    if (n) pages.add(s.page ?? String(i))
    elements += n
  })
  return { elements, pages: pages.size }
}

/** A cheap hash of the sources (the catalog cache key with the kit version). */
export function sourcesHash(sources: MineSource[]): string {
  let h = 2166136261
  for (const s of sources) {
    for (let i = 0; i < s.html.length; i++) {
      h ^= s.html.charCodeAt(i)
      h = Math.imul(h, 16777619)
    }
    h ^= 0x1f
  }
  return (h >>> 0).toString(36) + ':' + sources.length
}
