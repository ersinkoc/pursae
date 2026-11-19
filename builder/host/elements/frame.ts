/**
 * The element editor's frame: one document with the header template, the page's code sections and
 * the footer template, in their shared wrappers (like the site prints them).
 *
 * Static edit mode (K7): the design's scripts do not run. Two independent layers (security model:
 * docs/web-builder-code-sections.md): the frame is sandboxed without allow-scripts (ElementsCanvas),
 * and its copy of the markup is neutralised with the browser's own parser (neutral.ts: scripts of
 * every namespace inert, on* handlers, srcdoc, javascript: URLs, declarative shadow roots, meta
 * refresh, base … gone). The stored markup is never changed. The DOM on screen is then the source
 * DOM, so an operation is applied to the frame by reconciling the changed sections' new markup
 * against the elements already there (FrameSync): unchanged elements are kept (no reload, no image
 * flash), changed ones updated, or rebuilt from neutralised nodes.
 *
 * Header and footer parts are locked: their data-wbc ids get an "h:" / "f:" prefix in the copy, so
 * they never clash with the page's ids and the canvas knows them apart. The only editor nodes in
 * the design's DOM are its <base data-wb-editor> and three stylesheets at the end of the head:
 * <style data-wb-kit> (the kits' class and variable overrides, kitstyle.ts), <style data-wb-local>
 * (the local styles, like the published page's, see local.ts) and <style data-wb-edit>
 * (editor-only rules); section boundaries are marked by comments while the frame loads and the
 * comments are removed at once (FrameSync.collect).
 */
import type { Section } from '@/builder/types'
import { RAW, attrEdit, elementKids, outer, parse, splice, wbc, type Edit, type El, type Node as SrcNode, type Tree } from './html'
import { EDITOR_ATTR, neutralAttrs, neutralMarkup, neutralNodes } from './neutral'
import { tree } from './ops'

export { INERT } from './neutral'

export type FrameMode = 'static' | 'live' | 'preview'

const esc = (v: string) => v.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')
const TAG_RE = /^<[a-zA-Z][a-zA-Z0-9-]{0,30}(?:\s[^<>]*)?>$/

/** A section's wrapper chain (content.wrap). */
export function wrapChain(wrap: string | undefined): string[] {
  return (wrap ?? '')
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => TAG_RE.test(l))
}

const tagOf = (open: string) => /^<([a-zA-Z][a-zA-Z0-9-]*)/.exec(open)![1].toLowerCase()
const closeOf = (open: string) => `</${tagOf(open)}>`

/** Prefixes the data-wbc ids of markup (a locked part), byte for byte the same otherwise. */
function prefixIds(html: string, prefix: string): string {
  const t = parse(html)
  const edits: Edit[] = []
  for (const el of t.all) {
    const w = wbc(el)
    if (w !== null) edits.push(...attrEdit(t, el, 'data-wbc', prefix + w))
  }
  return edits.length ? splice(html, edits) : html
}

/**
 * The frame's copy of markup: in static mode parsed by the browser, neutralised and serialised
 * (neutral.ts); otherwise — the author chose to run the design's scripts — the markup itself.
 * Ids are prefixed for a locked part.
 */
export function frameCopy(html: string, mode: FrameMode, prefix?: string): string {
  if (mode === 'static') return neutralMarkup(html, prefix)
  return prefix ? prefixIds(html, prefix) : html
}

/** A wrapper's opening tag in the frame's copy. */
function openCopy(open: string, mode: FrameMode): string {
  if (mode !== 'static') return open
  const close = closeOf(open)
  const out = neutralMarkup(open + close)
  return out.startsWith(`<${tagOf(open)}`) && out.endsWith(close) ? out.slice(0, -close.length) : `<${tagOf(open)}>`
}

/** Attributes written in an opening tag ("class=a data-x"): neutralised in static mode. */
function attrsCopy(attrs: string | undefined, mode: FrameMode): string {
  const a = (attrs ?? '').trim()
  if (!a || /[<>]/.test(a)) return ''
  if (mode !== 'static') return ' ' + a
  const want = neutralAttrs(`<div ${a}></div>`, 'div')
  let out = ''
  // eslint-disable-next-line no-control-regex
  for (const [n, v] of want ?? []) if (/^[^\s"'>/=\u0000-\u001f]+$/.test(n)) out += ` ${n}="${esc(v)}"`
  return out
}

/** The head markup of all parts: each distinct top-level tag once, in order (like MergeHeads). */
export function mergeHeads(heads: string[], mode: FrameMode): string {
  const seen = new Set<string>()
  const out: string[] = []
  for (const h of heads) {
    if (!h.trim()) continue
    const t = parse(h)
    for (const el of elementKids(t.root)) {
      const tag = outer(t, el)
      if (seen.has(tag)) continue
      seen.add(tag)
      out.push(frameCopy(tag, mode))
    }
  }
  return out.join('\n')
}

export interface FrameParts {
  header: Section[]
  footer: Section[]
}

// Section ids are encoded: a marker never holds "-->" or "<".
const markId = (id: string) => encodeURIComponent(id).replace(/%3A/g, ":")
export const markStart = (id: string) => `<!--wb:${markId(id)}-->`
export const markEnd = (id: string) => `<!--/wb:${markId(id)}-->`

/**
 * The frame document. Sections are in page order between the header's and the footer's; wrappers
 * shared by consecutive sections are opened once (like the site's shared shell).
 */
export function frameDoc(page: Section[], parts: FrameParts, mode: FrameMode, editCss: string, base: string, localCss = '', kitCss = ''): string {
  const all: { s: Section; prefix?: string }[] = [...parts.header.map((s) => ({ s, prefix: 'h:' })), ...page.map((s) => ({ s })), ...parts.footer.map((s) => ({ s, prefix: 'f:' }))]
  const first = page[0] ?? all[0]?.s
  const stack: string[] = []
  let body = ''
  for (const { s, prefix } of all) {
    const chain = wrapChain(s.content.wrap)
    let k = 0
    while (k < stack.length && k < chain.length && stack[k] === chain[k]) k++
    while (stack.length > k) body += closeOf(stack.pop()!)
    for (const w of chain.slice(k)) {
      body += openCopy(w, mode)
      stack.push(w)
    }
    const id = (prefix ?? '') + s.id
    body += markStart(id) + frameCopy(s.content.html ?? '', mode, prefix) + markEnd(id)
  }
  while (stack.length) body += closeOf(stack.pop()!)
  const c = first?.content ?? {}
  const bodyClass = c.body ? ` class="${esc(c.body)}"` : ''
  const bodyAttrs = attrsCopy(c.bodyAttrs, mode).replace(/\sclass="[^"]*"/, bodyClass ? '' : '$&')
  return (
    `<!doctype html><html lang="${esc(c.lang || 'en')}"${attrsCopy(c.htmlAttrs, mode)}><head><meta charset="utf-8">` +
    `<meta name="viewport" content="width=device-width,initial-scale=1"><base href="${esc(base)}" ${EDITOR_ATTR}="">` +
    `${mergeHeads(all.map(({ s }) => s.content.head ?? ''), mode)}<style data-wb-kit>${kitCss.replace(/<\//g, '<\\/')}</style><style data-wb-local>${localCss.replace(/<\//g, '<\\/')}</style><style data-wb-edit>${editCss}</style></head><body${bodyClass}${bodyAttrs}>${body}</body></html>`
  )
}

/* ---------- Keeping the frame in step with the markup (static mode) ---------- */

interface SecNodes {
  parent: Node
  nodes: Node[]
}

/**
 * The page sections' nodes in the static frame, and the reconciler that applies new markup to
 * them: changed elements get the neutralised copy's attributes, new markup becomes neutralised
 * nodes (neutral.ts) — nothing the design wrote is ever parsed into the frame's own document.
 * Works in the browser (needs the frame's DOM).
 */
export class FrameSync {
  readonly secs = new Map<string, SecNodes>()
  constructor(readonly doc: Document) {}

  /** Reads and removes the section markers; false when a section's markup is not well formed. */
  collect(): boolean {
    const w = this.doc.createTreeWalker(this.doc.body ?? this.doc.documentElement, NodeFilter.SHOW_COMMENT)
    const starts = new Map<string, Comment>()
    const marks: Comment[] = []
    let ok = true
    const idOf = (data: string) => {
      const m = /^(\/?)wb:(.+)$/.exec(data)
      if (!m) return null
      try {
        return { end: !!m[1], id: decodeURIComponent(m[2]) }
      } catch {
        return null
      }
    }
    for (let n = w.nextNode() as Comment | null; n; n = w.nextNode() as Comment | null) {
      const m = idOf(n.data)
      if (!m) continue
      marks.push(n)
      if (!m.end) starts.set(m.id, n)
      else {
        const s = starts.get(m.id)
        if (!s || s.parentNode !== n.parentNode) {
          ok = false
          continue
        }
        const nodes: Node[] = []
        for (let x = s.nextSibling; x && x !== n; x = x.nextSibling) nodes.push(x)
        if (!m.id.startsWith('h:') && !m.id.startsWith('f:')) this.secs.set(m.id, { parent: s.parentNode!, nodes })
      }
    }
    for (const c of marks) {
      // An empty section keeps an empty text node as its place.
      const m = idOf(c.data)!
      const e = this.secs.get(m.id)
      if (e && !e.nodes.length && !m.end) {
        const t = this.doc.createTextNode('')
        c.parentNode!.insertBefore(t, c)
        e.nodes.push(t)
      }
      c.remove()
    }
    return ok
  }

  /**
   * Applies the new markup of the sections that changed. false: the frame must be reloaded (the
   * sections' order or membership changed, or a section was not found).
   */
  update(prev: Record<string, string>, next: Record<string, string>): boolean {
    const changed = Object.keys(next).filter((id) => prev[id] !== next[id])
    if (!changed.length) return true
    if (changed.some((id) => !this.secs.has(id) || prev[id] === undefined)) return false
    const ctx: Ctx = { oldOuter: new Map(), oldTag: new Map(), frame: new Map() }
    for (const id of changed) {
      for (const el of tree(prev[id]).all) {
        const w = wbc(el)
        if (w !== null && !ctx.oldOuter.has(w)) {
          ctx.oldOuter.set(w, outer(tree(prev[id]), el))
          ctx.oldTag.set(w, el.tag)
        }
      }
    }
    for (const el of Array.from(this.doc.querySelectorAll<Element>('[data-wbc]'))) {
      const w = el.getAttribute('data-wbc')!
      if (!ctx.frame.has(w)) ctx.frame.set(w, el)
    }
    for (const id of changed) {
      const e = this.secs.get(id)!
      const t = tree(next[id])
      const last = e.nodes[e.nodes.length - 1]
      const anchor = last && last.parentNode === e.parent ? last.nextSibling : null
      let nodes = this.list(e.parent, e.nodes, anchor, t, t.root.children, ctx)
      if (!nodes.length) {
        const ph = this.doc.createTextNode('')
        e.parent.insertBefore(ph, anchor)
        nodes = [ph]
      }
      e.nodes = nodes
    }
    return true
  }

  /** Nodes for source nodes `kids` placed in parent (replacing `old`, before `anchor`). */
  private list(parent: Node, old: Node[], anchor: Node | null, t: Tree, kids: SrcNode[], ctx: Ctx): Node[] {
    const out: Node[] = []
    let pending = ''
    const flush = () => {
      if (!pending) return
      out.push(...neutralNodes(pending, parent))
      pending = ''
    }
    for (const k of kids) {
      if (k.type !== 'el') {
        pending += t.src.slice(k.start, k.end)
        continue
      }
      const id = wbc(k)
      const fe = id !== null ? ctx.frame.get(id) : undefined
      const was = id !== null ? ctx.oldOuter.get(id) : undefined
      const src = outer(t, k)
      if (fe && was === src) {
        flush()
        out.push(fe)
        continue
      }
      const want = fe && was !== undefined && ctx.oldTag.get(id!) === k.tag && !RAW.has(k.tag) && k.tag !== 'svg' && k.tag !== 'math' && !k.foreign && fe.parentNode ? this.want(fe, t, k) : null
      if (fe && want) {
        flush()
        for (const a of Array.from(fe.attributes)) if (!want.has(a.name)) fe.removeAttribute(a.name)
        for (const [n, v] of want) if (fe.getAttribute(n) !== v) fe.setAttribute(n, v)
        this.list(fe, Array.from(fe.childNodes), null, t, k.children, ctx)
        out.push(fe)
        continue
      }
      pending += src
    }
    flush()
    const keep = new Set(out)
    for (const n of old) if (!keep.has(n) && n.parentNode === parent) parent.removeChild(n)
    let ref = anchor && anchor.parentNode === parent ? anchor : null
    for (let i = out.length - 1; i >= 0; i--) {
      const n = out[i]
      if (n.parentNode !== parent || n.nextSibling !== ref) parent.insertBefore(n, ref)
      ref = n
    }
    return out
  }

  /** The attributes frame element fe gets for source element k (its neutralised copy); null: rebuild it. */
  private want(fe: Element, t: Tree, k: El): Map<string, string> | null {
    const ctx = fe.parentElement && fe.parentElement.namespaceURI === 'http://www.w3.org/1999/xhtml' ? fe.parentElement.localName : 'template'
    return neutralAttrs(t.src.slice(k.start, k.openEnd) + '</' + k.tag + '>', k.tag, ctx)
  }
}

interface Ctx {
  oldOuter: Map<string, string>
  oldTag: Map<string, string>
  frame: Map<string, Element>
}
