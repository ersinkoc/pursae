/**
 * Element types (plan §4.2, K4): derived from the tag and the computed display, never stored.
 */
import { CodeXml, Columns3, Film, Heading, Image as ImageIcon, LayoutGrid, LayoutTemplate, Link2, List, Pilcrow, RectangleHorizontal, Shapes, Square, TextCursorInput, type LucideIcon } from 'lucide-react'
import { attr, decode, elementKids, ownText, textOf, type El, type Tree } from './html'
import { CODE_TAGS } from './ops'
import { t } from '../i18n'

export type Kind = 'section' | 'flex' | 'grid' | 'div' | 'heading' | 'text' | 'link' | 'button' | 'image' | 'icon' | 'video' | 'list' | 'form' | 'code'

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

/**
 * The element's type. display: its computed display when known (the canvas measures it), else
 * guessed from display classes.
 */
export function kindOf(tr: Tree, el: El, display?: string): Kind {
  const tag = el.tag
  if (CODE_TAGS.has(tag) || tag === 'canvas') return 'code'
  if (/^h[1-6]$/.test(tag)) return 'heading'
  if (tag === 'img' || tag === 'picture') return 'image'
  if (tag === 'svg') return 'icon'
  if (tag === 'video' || tag === 'iframe' || tag === 'audio' || tag === 'embed' || tag === 'object') return 'video'
  if (tag === 'form' || tag === 'input' || tag === 'select' || tag === 'textarea' || tag === 'label' || tag === 'fieldset') return 'form'
  if (el.parent?.tag === '#root') return 'section'
  if (tag === 'button') return 'button'
  if (tag === 'a') return 'link'
  if (tag === 'ul' || tag === 'ol' || tag === 'dl') return 'list'
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
      return t('Button')
    case 'image':
      return t('Image')
    case 'icon':
      return t('Icon')
    case 'video':
      return t('Video / embed')
    case 'list':
      return t('List')
    case 'form':
      return t('Form element')
    case 'code':
      return t('Code')
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
