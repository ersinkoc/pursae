/**
 * Neutralising design markup for the element editor's static frame, with the browser's own HTML
 * parser (security model: docs/web-builder-code-sections.md, "Security model").
 *
 * The static edit frame must show the design without running any of it. The frame itself is
 * sandboxed without allow-scripts (ElementsCanvas), so nothing in it can run whatever it holds;
 * this module is the second, independent layer: the frame's copy of the markup is parsed by the
 * same parser the frame uses (a <template> / context element of an inert document: no browsing
 * context, so nothing it creates loads or runs), every node is walked in every namespace (HTML,
 * SVG, MathML, template contents) and made inert, and only then serialised (frame document) or
 * imported (reconciler, FrameSync). The stored markup is never touched: only the frame's copy.
 *
 * Inert means:
 *  - every script element, whatever its namespace, becomes an empty <script type="text/x-wb-inert">
 *    (keeping its data-wbc id);
 *  - every on* attribute, srcdoc, xml:base, the editor's own data-wb-editor marker and a template's
 *    shadowrootmode / shadowroot (declarative shadow roots) are removed;
 *  - URL attributes (href, src, action, formaction, data, poster, xlink:href, SVG animation values,
 *    srcset candidates, …) holding javascript:, vbscript: or a data: URL that is not an image,
 *    font, audio or video become about:blank#wb-blocked (the value as the URL parser reads it:
 *    entities decoded, tabs, newlines and controls ignored);
 *  - SVG animations that would set an href or a handler are removed;
 *  - <meta http-equiv=refresh> and <base> are removed, <object> / <embed> lose what they load,
 *    nested frames lose their src and get sandbox="" (they could otherwise load a same-origin page
 *    that runs); embedded content keeps its element (its box) and is marked data-wb-embed-ph, which
 *    the editor draws as a placeholder (embeds.ts: a video plays on the published page only),
 *    <noscript> is emptied (as a frame with scripts would show it) and <plaintext> becomes <pre>.
 *
 * Serialising and parsing again is not always the identity (mutation XSS): neutralMarkup repeats
 * parse → neutralise → serialise until the result is stable and falls back to the markup as text
 * when it is not. The frame's DOM is also swept again after it loads (sweep), and nothing in it
 * could have run in between (no allow-scripts).
 */
export const INERT = 'text/x-wb-inert'
export const BLOCKED_URL = 'about:blank#wb-blocked'
/** The editor's own nodes in the frame document (its <base>): never touched by sweep. */
export const EDITOR_ATTR = 'data-wb-editor'
/** Marks embedded content (iframe, object, embed …) of the static copy: drawn as a placeholder. */
export const EMBED_ATTR = 'data-wb-embed-ph'

const HTML_NS = 'http://www.w3.org/1999/xhtml'
const XLINK_NS = 'http://www.w3.org/1999/xlink'

/** Attributes that hold a URL (local names, any namespace). */
const URL_ATTRS = new Set([
  'href',
  'src',
  'action',
  'formaction',
  'data',
  'poster',
  'background',
  'cite',
  'codebase',
  'longdesc',
  'lowsrc',
  'dynsrc',
  'manifest',
  'ping',
  'usemap',
  'icon',
  'archive',
  'classid',
  'profile',
  'code',
  'itemtype',
  'xmlns:xlink',
  // SVG animation values (<animate attributeName="href" values="javascript:…">)
  'values',
  'to',
  'from',
  'by',
])
const SRCSET_ATTRS = new Set(['srcset', 'imagesrcset'])
/** Elements whose data: image URL may be an SVG (shown as an image: no script runs in it). */
const SVG_DATA_OK = new Set(['img', 'image', 'feimage', 'input', 'source', 'video', 'link'])
const ANIMATIONS = new Set(['animate', 'set', 'animatemotion', 'animatetransform', 'animatecolor', 'discard'])
const FRAMES = new Set(['iframe', 'frame', 'fencedframe', 'portal'])
const LOADERS = new Set(['object', 'embed', 'applet'])

/** Whether a URL attribute value must not reach the frame. */
export function unsafeUrl(value: string, tag = ''): boolean {
  // The URL parser ignores leading / trailing C0 controls and spaces and every tab and newline;
  // dropping every control and space anywhere is stricter still.
  const v = Array.from(value).filter((ch) => { const c = ch.codePointAt(0)!; return !(c <= 0x20 || (c >= 0x7f && c <= 0x9f) || c === 0xad || (c >= 0x200b && c <= 0x200f) || c === 0x2028 || c === 0x2029 || c === 0xfeff) }).join("").toLowerCase()
  const scheme = /^([a-z][a-z0-9+.-]*):/.exec(v)?.[1]
  if (!scheme) return false
  if (scheme === 'javascript' || scheme === 'vbscript' || scheme === 'livescript' || scheme === 'mocha') return true
  if (scheme !== 'data') return false
  const mime = /^data:([^;,]*)/.exec(v)?.[1] ?? ''
  if (mime === 'image/svg+xml') return !SVG_DATA_OK.has(tag)
  return !/^(image\/[a-z0-9.+-]+|font\/[a-z0-9.+-]+|application\/(font-[a-z0-9.+-]+|x-font-[a-z0-9.+-]+|vnd\.ms-fontobject|octet-stream)|audio\/[a-z0-9.+-]+|video\/[a-z0-9.+-]+|text\/(css|plain))$/.test(mime)
}

let inertDoc: Document | null = null
/** A document without a browsing context: what is parsed in it never loads and never runs. */
export function inertDocument(): Document {
  return (inertDoc ??= document.implementation.createHTMLDocument(''))
}

/**
 * Parses markup in the inert document. context: the local name of the HTML element the markup is
 * parsed inside (fragment parsing algorithm); a <template> by default (any content, as written).
 */
export function parseInert(markup: string, context = 'template'): DocumentFragment {
  const d = inertDocument()
  if (context === 'template' || !/^[a-z][a-z0-9-]*$/.test(context) || context === 'html' || context === 'head') {
    const tpl = d.createElement('template')
    // The inert document: nothing this creates loads or runs (and it is neutralised next).
    tpl.innerHTML = markup
    return tpl.content
  }
  const c = d.createElement(context)
  c.innerHTML = markup
  const f = d.createDocumentFragment()
  while (c.firstChild) f.appendChild(c.firstChild)
  return f
}

export interface NeutralOpts {
  /** Prefix for data-wbc ids (locked header / footer parts: "h:" / "f:"). */
  prefix?: string
  /** Sweep of the frame's own document: the editor's nodes (data-wb-editor) are kept as they are. */
  sweep?: boolean
}

const isInertScript = (el: Element) => el.getAttribute('type') === INERT && !el.firstChild && Array.from(el.attributes).every((a) => a.name === 'type' || a.name === 'data-wbc')

/** Makes one element inert in place; returns the node that now stands for it (null: removed). */
function neutraliseEl(el: Element, opts: NeutralOpts): Element | null {
  const tag = el.localName.toLowerCase()
  const doc = el.ownerDocument
  if (tag === 'script') {
    if (isInertScript(el)) return el
    const s = doc.createElementNS(HTML_NS, 'script')
    s.setAttribute('type', INERT)
    const id = el.getAttribute('data-wbc')
    if (id !== null) s.setAttribute('data-wbc', id)
    el.replaceWith(s)
    return s
  }
  if (tag === 'base' || (tag === 'meta' && (el.getAttribute('http-equiv') ?? '').trim().toLowerCase() === 'refresh')) {
    el.remove()
    return null
  }
  if (ANIMATIONS.has(tag)) {
    const target = (el.getAttribute('attributeName') ?? el.getAttribute('attributename') ?? '').trim().toLowerCase()
    if (/(^|:)href$/.test(target) || target.startsWith('on') || target === 'src' || target === 'action' || target === 'formaction') {
      el.remove()
      return null
    }
  }
  if (tag === 'plaintext' && el.namespaceURI === HTML_NS) {
    // Everything after a <plaintext> start tag is text: it would swallow the rest of the frame.
    const pre = doc.createElementNS(HTML_NS, 'pre')
    for (const a of Array.from(el.attributes)) pre.setAttributeNS(a.namespaceURI, a.name, a.value)
    while (el.firstChild) pre.appendChild(el.firstChild)
    el.replaceWith(pre)
    el = pre
  }
  for (const a of Array.from(el.attributes)) {
    const name = a.name.toLowerCase()
    const local = a.localName.toLowerCase()
    if (name.startsWith('on') || local.startsWith('on') || name === 'srcdoc' || name === EDITOR_ATTR || name === 'xml:base' || (a.namespaceURI === 'http://www.w3.org/XML/1998/namespace' && local === 'base') || name.startsWith('shadowroot')) {
      el.removeAttributeNode(a)
      continue
    }
    if (URL_ATTRS.has(local) || URL_ATTRS.has(name) || (a.namespaceURI === XLINK_NS && local === 'href')) {
      if (local === 'values' || local === 'to' || local === 'from' || local === 'by') {
        if (a.value.split(';').some((part) => unsafeUrl(part, tag))) a.value = BLOCKED_URL
      } else if (unsafeUrl(a.value, tag)) a.value = BLOCKED_URL
      continue
    }
    if (SRCSET_ATTRS.has(name) && a.value.split(',').some((part) => unsafeUrl(part.trim().split(/\s+/)[0] ?? '', tag))) a.value = BLOCKED_URL
  }
  if (LOADERS.has(tag)) {
    for (const n of ['data', 'src', 'codebase', 'classid', 'archive', 'code', 'type']) el.removeAttribute(n)
  }
  if (FRAMES.has(tag)) {
    // Nothing is loaded into a nested frame: a video player or a map would only show a broken
    // player without scripts. The element keeps its box (attributes, classes, styles).
    el.removeAttribute('src')
    if (tag !== 'frame') el.setAttribute('sandbox', '')
  }
  // Embedded content (iframe, object, embed …) is drawn as the editor's placeholder (embeds.ts).
  if (FRAMES.has(tag) || LOADERS.has(tag)) el.setAttribute(EMBED_ATTR, '')
  else if (el.hasAttribute(EMBED_ATTR)) el.removeAttribute(EMBED_ATTR)
  if (tag === 'noscript') el.replaceChildren()
  if (opts.prefix) {
    const id = el.getAttribute('data-wbc')
    if (id !== null) el.setAttribute('data-wbc', opts.prefix + id)
  }
  return el
}

/** Makes every node under root (and root itself when it is an element) inert, in place. */
export function neutralise(root: Node, opts: NeutralOpts = {}): void {
  const stack: Node[] = [root]
  while (stack.length) {
    let n: Node | null = stack.pop()!
    if (n.nodeType === 1) {
      const el = n as Element
      if (opts.sweep && el.hasAttribute(EDITOR_ATTR)) continue
      n = neutraliseEl(el, opts)
      if (!n) continue
      const tpl = n as HTMLTemplateElement
      if (tpl.localName === 'template' && tpl.content) stack.push(tpl.content)
      const sr = (n as Element).shadowRoot
      if (sr) stack.push(sr)
    } else if (n.nodeType === 8 && !opts.sweep) {
      // A design comment never looks like one of the frame's section markers.
      const c = n as Comment
      if (/^\/?wb:/.test(c.data)) c.data = ' ' + c.data
      continue
    }
    for (let k = n.lastChild; k; k = k.previousSibling) stack.push(k)
  }
}

/** The HTML serialisation of a fragment's children. */
function serialise(f: DocumentFragment): string {
  let out = ''
  for (let k = f.firstChild; k; k = k.nextSibling) {
    if (k.nodeType === 1) out += (k as Element).outerHTML
    else if (k.nodeType === 3) out += escText((k as Text).data)
    else if (k.nodeType === 8) out += `<!--${(k as Comment).data}-->`
  }
  return out
}

const escText = (v: string) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\u00a0/g, '&nbsp;')

/**
 * The frame's copy of markup: parsed by the browser, neutralised and serialised, as long as
 * parsing the result again yields the same markup (else the markup as text: nothing of it runs).
 */
export function neutralMarkup(markup: string, prefix?: string, context = 'template'): string {
  if (!markup) return ''
  const first = parseInert(markup, context)
  neutralise(first, { prefix })
  let out = serialise(first)
  for (let i = 0; i < 4; i++) {
    const again = parseInert(out, context)
    neutralise(again)
    const next = serialise(again)
    if (next === out) return out
    out = next
  }
  return escText(markup)
}

/** Neutralised nodes for markup parsed in the context of `parent`, imported into its document. */
export function neutralNodes(markup: string, parent: Node): Node[] {
  const ctx = parent.nodeType === 1 && (parent as Element).namespaceURI === HTML_NS ? (parent as Element).localName : 'template'
  const f = parseInert(markup, ctx)
  neutralise(f)
  const doc = parent.ownerDocument ?? (parent as Document)
  return Array.from(f.childNodes).map((n) => doc.importNode(n, true))
}

/** The attributes element markup (an open tag and its end tag) has once neutralised; null: not an element of `tag`. */
export function neutralAttrs(markup: string, tag: string, context = 'template'): Map<string, string> | null {
  const f = parseInert(markup, context)
  neutralise(f)
  const el = f.firstElementChild
  if (!el || el.localName !== tag) return null
  const out = new Map<string, string>()
  for (const a of Array.from(el.attributes)) out.set(a.name, a.value)
  return out
}

/** Sweeps the frame's document after it loaded: anything a second parse produced is made inert. */
export function sweep(doc: Document): void {
  neutralise(doc.documentElement, { sweep: true })
}
