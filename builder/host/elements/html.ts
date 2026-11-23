/**
 * A source-preserving HTML tree of a code section's markup (content.html).
 *
 * The element editor never re-serialises a section: every operation (ops.ts) splices the source
 * string at the ranges this parser records, so the bytes outside the edited element stay exactly
 * as stored (the design's whitespace, quoting and entities included) and a page opened and saved
 * without a change publishes byte for byte the same markup (acceptance S2).
 *
 * The parser is small on purpose and runs without a DOM (the unit tests run in node): tags,
 * attributes (quoted or not), comments, raw text elements (script, style, textarea, title),
 * void elements, self-closing tags inside svg / math, and the common implied end tags (p, li,
 * dt / dd, option, tr, td / th). Imported markup is the importer's own serialisation, so it is
 * well formed; hand-written markup that the browser would repair differently (a table without
 * tbody, misnested inline tags) still yields a usable tree because operations only rely on
 * element ranges and data-wbc ids.
 */

export const VOID = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'param', 'source', 'track', 'wbr'])
/** Elements whose content is text up to their own end tag. */
export const RAW = new Set(['script', 'style', 'textarea', 'title', 'xmp', 'noembed', 'noframes', 'iframe', 'noscript', 'template'])
const FOREIGN = new Set(['svg', 'math'])
/** An open p is closed by these start tags. */
const CLOSES_P = new Set(['address', 'article', 'aside', 'blockquote', 'details', 'div', 'dl', 'fieldset', 'figcaption', 'figure', 'footer', 'form', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'header', 'hgroup', 'hr', 'main', 'menu', 'nav', 'ol', 'p', 'pre', 'section', 'table', 'ul'])

const P_SCOPE = new Set(['button', 'table', 'td', 'th', 'caption', 'template', 'object', 'marquee', 'applet', 'svg', 'math'])

export interface Attr {
  name: string
  /** Decoded value; null for a bare attribute (`hidden`). */
  value: string | null
  /** Source range of the whole attribute (name="value"). */
  start: number
  end: number
}

export interface TextNode {
  type: 'text' | 'comment' | 'other'
  start: number
  end: number
  parent: El
}

export interface El {
  type: 'el'
  /** Lower-case tag name; '#root' for the section itself. */
  tag: string
  start: number
  /** End of the start tag (after '>'). */
  openEnd: number
  /** Start of the end tag; for void / self-closed elements equal to openEnd. */
  closeStart: number
  end: number
  attrs: Attr[]
  /** Offset where a new attribute goes (after the last attribute, before '/>' or '>'). */
  attrEnd: number
  children: Node[]
  parent: El | null
  /** Inside svg / math (svg and math themselves are not). */
  foreign: boolean
  /** The element had an end tag in the source. */
  closed: boolean
}

export type Node = El | TextNode

export interface Tree {
  src: string
  root: El
  /** Elements by data-wbc id (first one wins). */
  byId: Map<string, El>
  /** Every element in document order. */
  all: El[]
}

const NAME = /[A-Za-z][^\s/>\0]*/y
const ATTR_NAME = /[^\s"'>/=\0]+/y
const WS = /[\s]*/y

const ENTITIES: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' }

/** Decodes the entities an attribute value or text commonly holds. */
export function decode(v: string): string {
  if (!v.includes('&')) return v
  return v.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (m, e: string) => {
    if (e[0] === '#') {
      const n = e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10)
      return Number.isFinite(n) && n > 0 && n < 0x110000 ? String.fromCodePoint(n) : m
    }
    return ENTITIES[e.toLowerCase()] ?? m
  })
}

export const escAttr = (v: string) => v.replace(/&/g, '&amp;').replace(/"/g, '&quot;')
export const escText = (v: string) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

function mkEl(tag: string, start: number, parent: El | null, foreign: boolean): El {
  return { type: 'el', tag, start, openEnd: start, closeStart: start, end: start, attrs: [], attrEnd: start, children: [], parent, foreign, closed: false }
}

/** Parses a start tag at src[i] === '<'; null when it is not one. */
function startTag(src: string, i: number): { tag: string; attrs: Attr[]; attrEnd: number; end: number; selfClose: boolean } | null {
  NAME.lastIndex = i + 1
  const m = NAME.exec(src)
  if (!m) return null
  const tag = m[0].toLowerCase()
  let p = i + 1 + m[0].length
  const attrs: Attr[] = []
  let attrEnd = p
  for (;;) {
    WS.lastIndex = p
    WS.exec(src)
    p = WS.lastIndex
    if (p >= src.length) return { tag, attrs, attrEnd, end: src.length, selfClose: false }
    const c = src[p]
    if (c === '>') return { tag, attrs, attrEnd, end: p + 1, selfClose: false }
    if (c === '/') {
      if (src[p + 1] === '>') return { tag, attrs, attrEnd, end: p + 2, selfClose: true }
      p++
      continue
    }
    ATTR_NAME.lastIndex = p
    const a = ATTR_NAME.exec(src)
    if (!a) {
      p++
      continue
    }
    const aStart = p
    p += a[0].length
    WS.lastIndex = p
    WS.exec(src)
    let q = WS.lastIndex
    let value: string | null = null
    if (src[q] === '=') {
      q++
      WS.lastIndex = q
      WS.exec(src)
      q = WS.lastIndex
      const quote = src[q]
      if (quote === '"' || quote === "'") {
        const close = src.indexOf(quote, q + 1)
        const e = close < 0 ? src.length : close
        value = decode(src.slice(q + 1, e))
        p = Math.min(src.length, e + 1)
      } else {
        let e = q
        while (e < src.length && !/[\s>]/.test(src[e])) e++
        value = decode(src.slice(q, e))
        p = e
      }
    }
    attrs.push({ name: a[0].toLowerCase(), value, start: aStart, end: p })
    attrEnd = p
  }
}

/** Parses markup into a source-preserving tree. */
export function parse(src: string): Tree {
  const root = mkEl('#root', 0, null, false)
  root.closed = true
  const all: El[] = []
  const byId = new Map<string, El>()
  const stack: El[] = [root]
  const top = () => stack[stack.length - 1]
  const close = (el: El, at: number, end: number) => {
    el.closeStart = at
    el.end = end
  }
  /** Closes the open elements above `el` (implied end) and `el` itself at `at`. */
  const popTo = (el: El, at: number, end: number) => {
    while (stack.length > 1) {
      const x = stack.pop()!
      if (x === el) {
        close(x, at, end)
        x.closed = end > at
        return
      }
      close(x, at, at)
    }
  }
  const text = (type: TextNode['type'], s: number, e: number) => {
    if (e > s) top().children.push({ type, start: s, end: e, parent: top() })
  }
  let i = 0
  let textStart = 0
  const n = src.length
  while (i < n) {
    const lt = src.indexOf('<', i)
    if (lt < 0) break
    i = lt
    const c1 = src[i + 1]
    if (src.startsWith('<!--', i)) {
      text('text', textStart, i)
      const e = src.indexOf('-->', i + 4)
      const end = e < 0 ? n : e + 3
      text('comment', i, end)
      i = textStart = end
      continue
    }
    if (c1 === '!' || c1 === '?') {
      text('text', textStart, i)
      const e = src.indexOf('>', i)
      const end = e < 0 ? n : e + 1
      text('other', i, end)
      i = textStart = end
      continue
    }
    if (c1 === '/') {
      NAME.lastIndex = i + 2
      const m = NAME.exec(src)
      if (!m) {
        i++
        continue
      }
      text('text', textStart, i)
      const tag = m[0].toLowerCase()
      const e = src.indexOf('>', i)
      const end = e < 0 ? n : e + 1
      // The nearest open element with this name; a stray end tag is kept as text (ignored).
      let match: El | null = null
      for (let k = stack.length - 1; k > 0; k--) {
        if (stack[k].tag === tag) {
          match = stack[k]
          break
        }
        // An end tag does not close across svg / math boundaries from inside html.
      }
      if (match) popTo(match, i, end)
      else text('other', i, end)
      i = textStart = end
      continue
    }
    const st = startTag(src, i)
    if (!st) {
      i++
      continue
    }
    text('text', textStart, i)
    const parentForeign = top().foreign || FOREIGN.has(top().tag)
    // Implied end tags (html content only).
    if (!parentForeign) {
      const t = st.tag
      if (CLOSES_P.has(t)) {
        // "has a p element in button scope"
        for (let k = stack.length - 1; k > 0; k--) {
          const x = stack[k]
          if (x.tag === 'p') {
            popTo(x, i, i)
            break
          }
          if (P_SCOPE.has(x.tag)) break
        }
      }
      const implied = (names: string[], scope: string[]) => {
        for (let k = stack.length - 1; k > 0; k--) {
          if (names.includes(stack[k].tag)) {
            popTo(stack[k], i, i)
            return
          }
          if (scope.includes(stack[k].tag)) return
        }
      }
      if (t === 'li') implied(['li'], ['ul', 'ol', 'menu', 'div', 'section'])
      else if (t === 'dt' || t === 'dd') implied(['dt', 'dd'], ['dl', 'div'])
      else if (t === 'option') implied(['option'], ['select', 'datalist', 'optgroup'])
      else if (t === 'tr') implied(['tr'], ['table', 'thead', 'tbody', 'tfoot'])
      else if (t === 'td' || t === 'th') implied(['td', 'th'], ['tr', 'table'])
    }
    const parent = top()
    const el = mkEl(st.tag, i, parent, parentForeign)
    el.attrs = st.attrs
    el.attrEnd = st.attrEnd
    el.openEnd = st.end
    parent.children.push(el)
    all.push(el)
    const id = st.attrs.find((a) => a.name === 'data-wbc')?.value
    if (id && !byId.has(id)) byId.set(id, el)
    i = textStart = st.end
    if ((!parentForeign && VOID.has(st.tag)) || (st.selfClose && (parentForeign || FOREIGN.has(st.tag)))) {
      close(el, st.end, st.end)
      continue
    }
    if (!parentForeign && RAW.has(st.tag)) {
      const re = new RegExp(`</${st.tag}[\\s/>]`, 'ig')
      re.lastIndex = st.end
      const m = re.exec(src)
      const at = m ? m.index : n
      if (at > st.end) el.children.push({ type: 'text', start: st.end, end: at, parent: el })
      const e = m ? src.indexOf('>', at) : -1
      const end = e < 0 ? n : e + 1
      close(el, at, end)
      el.closed = !!m
      i = textStart = end
      continue
    }
    stack.push(el)
  }
  text('text', textStart, n)
  while (stack.length > 1) close(stack.pop()!, n, n)
  root.openEnd = 0
  root.closeStart = n
  root.end = n
  return { src, root, byId, all }
}

/* ---------- Reading ---------- */

export const attr = (el: El, name: string): string | null => {
  const a = el.attrs.find((x) => x.name === name)
  return a ? (a.value ?? '') : null
}

export const wbc = (el: El) => attr(el, 'data-wbc')

export const outer = (t: Tree, n: Node) => t.src.slice(n.start, n.end)
export const inner = (t: Tree, el: El) => t.src.slice(el.openEnd, el.closeStart)
export const openTag = (t: Tree, el: El) => t.src.slice(el.start, el.openEnd)

export const elementKids = (el: El) => el.children.filter((c): c is El => c.type === 'el')

/** true when `a` is `b` or contains it. */
export function contains(a: El, b: El): boolean {
  for (let x: El | null = b; x; x = x.parent) if (x === a) return true
  return false
}

/** Whether the node is whitespace-only text. */
export const isWs = (t: Tree, n: Node | undefined) => !!n && n.type === 'text' && !/\S/.test(t.src.slice(n.start, n.end))

/** The element has text of its own (not only inside children). */
export function ownText(t: Tree, el: El): boolean {
  return el.children.some((c) => c.type === 'text' && /\S/.test(t.src.slice(c.start, c.end)))
}

/** Plain text of an element (scripts and styles skipped), whitespace collapsed. */
/** Elements whose text runs on with the text around them (textOf). */
const INLINE_TEXT = new Set(['a', 'abbr', 'b', 'bdi', 'bdo', 'cite', 'code', 'data', 'del', 'dfn', 'em', 'font', 'i', 'ins', 'kbd', 'mark', 'q', 's', 'samp', 'small', 'span', 'strong', 'sub', 'sup', 'time', 'u', 'var'])

export function textOf(t: Tree, el: El, max = 200): string {
  let out = ''
  const walk = (e: El) => {
    if (e.tag === 'script' || e.tag === 'style' || e.tag === 'svg') return
    for (const c of e.children) {
      if (out.length > max) return
      if (c.type === 'text') out += decode(t.src.slice(c.start, c.end))
      else if (c.type === 'el') {
        // Inline elements join their text as the page shows it ("iş</span>, " is "iş,"); others are apart.
        const gap = INLINE_TEXT.has(c.tag) ? '' : ' '
        out += gap
        walk(c)
        out += gap
      }
    }
  }
  walk(el)
  return out.replace(/\s+/g, ' ').trim()
}

/* ---------- Writing ---------- */

export interface Edit {
  start: number
  end: number
  text: string
}

/** Applies non-overlapping edits to src. */
export function splice(src: string, edits: Edit[]): string {
  const sorted = [...edits].sort((a, b) => b.start - a.start || b.end - a.end)
  let out = src
  for (const e of sorted) out = out.slice(0, e.start) + e.text + out.slice(e.end)
  return out
}

/** Edits that set (value string) or remove (null) attribute `name` of el. */
export function attrEdit(t: Tree, el: El, name: string, value: string | null): Edit[] {
  const a = el.attrs.find((x) => x.name === name)
  if (value === null) {
    if (!a) return []
    // Take the whitespace before the attribute with it.
    let s = a.start
    while (s > el.start && /\s/.test(t.src[s - 1])) s--
    return [{ start: s, end: a.end, text: '' }]
  }
  const text = `${name}="${escAttr(value)}"`
  if (a) return [{ start: a.start, end: a.end, text }]
  return [{ start: el.attrEnd, end: el.attrEnd, text: ` ${text}` }]
}
