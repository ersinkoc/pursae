/**
 * The element editor's attribute rules (Content panel, Faz 4.3): which attributes the attributes
 * editor may set and which values it accepts, and the tags an element may become. Pure functions.
 *
 * Never: event handlers (on…), data-wbc (the editor's id), style and class (the Style panel and
 * the class chips own them), srcdoc; URL attributes refuse javascript:, vbscript: and data: (an
 * image source may be a data:image URI).
 */
import { RAW, VOID } from './html'

export type AttrError = 'name' | 'handler' | 'reserved' | 'url' | 'id' | 'value' | 'length'

const NAME_RE = /^[a-z_:][a-z0-9_.:-]{0,63}$/
const RESERVED = new Set(['data-wbc', 'style', 'class', 'srcdoc', 'is'])
export const URL_ATTRS = new Set(['href', 'src', 'action', 'formaction', 'poster', 'data', 'cite', 'background', 'xlink:href', 'longdesc', 'manifest', 'ping', 'codebase'])
const ID_RE = /^[A-Za-z][A-Za-z0-9_:.-]{0,127}$/

/** Why `name` cannot be set (null: it can). */
export function attrNameError(raw: string): AttrError | null {
  const name = raw.trim().toLowerCase()
  if (!NAME_RE.test(name)) return 'name'
  if (name.startsWith('on')) return 'handler'
  if (RESERVED.has(name)) return 'reserved'
  return null
}

/** Whether a URL value is safe for a URL attribute (img: data:image/… allowed on src). */
export function urlOK(value: string, allowDataImage = false): boolean {
  // eslint-disable-next-line no-control-regex
  const v = value.replace(/[\u0000- \u007f]+/g, '').toLowerCase()
  if (/^(javascript|vbscript):/.test(v)) return false
  if (v.startsWith('data:')) return allowDataImage && /^data:image\/(png|jpe?g|gif|webp|avif|svg\+xml)[;,]/.test(v)
  return true
}

/** Why `name="value"` cannot be set on a `tag` element (null: it can). */
export function attrError(name: string, value: string, tag = ''): AttrError | null {
  const ne = attrNameError(name)
  if (ne) return ne
  const n = name.trim().toLowerCase()
  if (value.length > 4096) return 'length'
  if (/[\u0000]/.test(value)) return 'value'
  if (URL_ATTRS.has(n) && !urlOK(value, n === 'src' && (tag === 'img' || tag === 'source' || tag === 'input'))) return 'url'
  if (n === 'srcset' && value.split(',').some((part) => !urlOK(part.trim().split(/\s+/)[0] ?? '', true))) return 'url'
  if (n === 'id' && value !== '' && !ID_RE.test(value)) return 'id'
  return null
}

/** Tags the "Change tag" control offers for an element of `tag` (same content model family). */
export function tagChoices(tag: string): string[] {
  const H = ['h1', 'h2', 'h3', 'h4', 'h5', 'h6']
  if (H.includes(tag)) return [...H, 'p', 'div']
  if (tag === 'p' || tag === 'span' || tag === 'blockquote' || tag === 'figcaption' || tag === 'label' || tag === 'small' || tag === 'strong' || tag === 'em') return ['p', 'span', 'div', 'blockquote', 'small', 'strong', 'em', ...H]
  if (tag === 'ul' || tag === 'ol') return ['ul', 'ol']
  if (tag === 'a' || tag === 'button') return ['a', 'button', 'span', 'div']
  if (['div', 'section', 'article', 'aside', 'header', 'footer', 'main', 'nav', 'figure', 'address'].includes(tag)) return ['div', 'section', 'article', 'aside', 'header', 'footer', 'main', 'nav', 'figure', 'address']
  return []
}

/** Whether an element may change from tag `from` to tag `to`. */
export function tagChangeOK(from: string, to: string): boolean {
  if (from === to || !/^[a-z][a-z0-9-]{0,30}$/.test(to)) return false
  const bad = (x: string) => VOID.has(x) || RAW.has(x) || x === 'svg' || x === 'math' || x === 'html' || x === 'body' || x === 'head' || x === 'script' || x === 'style'
  return !bad(from) && !bad(to)
}
