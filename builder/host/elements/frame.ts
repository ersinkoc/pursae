/**
 * The element editor's frame: one document with the header template, the page's code sections and
 * the footer template, in their shared wrappers (like the site prints them).
 *
 * Static edit mode (K7): the frame gets a copy of the markup whose scripts do not run (a script's
 * type becomes text/x-wb-inert, inline on* handlers are dropped); the stored markup is never
 * changed. The DOM on screen is then the source DOM, so an operation is applied to the frame by
 * reconciling the changed sections' new markup against the elements already there (FrameSync):
 * unchanged elements are kept (no reload, no image flash), changed ones updated or rebuilt.
 *
 * Header and footer parts are locked: their data-wbc ids get an "h:" / "f:" prefix in the copy, so
 * they never clash with the page's ids and the canvas knows them apart. The only editor node in the
 * design's DOM is one <style data-wb-edit>; section boundaries are marked by comments while the
 * frame loads and the comments are removed at once (FrameSync.collect).
 */
import type { Section } from '@/builder/types'
import { RAW, attrEdit, elementKids, outer, parse, splice, wbc, type Edit, type El, type Node as SrcNode, type Tree } from './html'
import { tree } from './ops'

export type FrameMode = 'static' | 'live' | 'preview'

export const INERT = 'text/x-wb-inert'

const esc = (v: string) => v.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')
const TAG_RE = /^<[a-zA-Z][a-zA-Z0-9-]{0,30}(?:\s[^<>]*)?>$/

/** A section's wrapper chain (content.wrap). */
export function wrapChain(wrap: string | undefined): string[] {
  return (wrap ?? '')
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => TAG_RE.test(l))
}

const closeOf = (open: string) => `</${/^<([a-zA-Z][a-zA-Z0-9-]*)/.exec(open)![1]}>`

/** Edits that make markup inert: scripts get a type that does not run, on* handlers go. */
function inertEdits(t: Tree, prefix?: string): Edit[] {
  const edits: Edit[] = []
  for (const el of t.all) {
    for (const a of el.attrs) {
      if (/^on/.test(a.name)) edits.push(...attrEdit(t, el, a.name, null))
    }
    if (el.tag === 'script' && !el.foreign) edits.push(...attrEdit(t, el, 'type', INERT))
    if (prefix) {
      const w = wbc(el)
      if (w !== null) edits.push(...attrEdit(t, el, 'data-wbc', prefix + w))
    }
  }
  return edits
}

/** The frame's copy of markup: inert in static mode; ids prefixed for a locked part. */
export function frameCopy(html: string, mode: FrameMode, prefix?: string): string {
  if (mode !== 'static' && !prefix) return html
  const t = parse(html)
  const edits = mode === 'static' ? inertEdits(t, prefix) : []
  if (mode !== 'static' && prefix) {
    for (const el of t.all) {
      const w = wbc(el)
      if (w !== null) edits.push(...attrEdit(t, el, 'data-wbc', prefix + w))
    }
  }
  return edits.length ? splice(html, edits) : html
}

/** Attributes written in an opening tag ("class=a data-x"): inert in static mode. */
function attrsCopy(attrs: string | undefined, mode: FrameMode): string {
  const a = (attrs ?? '').trim()
  if (!a || /[<>]/.test(a)) return ''
  if (mode !== 'static') return ' ' + a
  const t = parse(`<x ${a}>`)
  const el = t.all[0]
  if (!el) return ''
  return ' ' + splice(t.src, inertEdits(t)).slice(3, -1).trim()
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

export const markStart = (id: string) => `<!--wb:${id}-->`
export const markEnd = (id: string) => `<!--/wb:${id}-->`

/**
 * The frame document. Sections are in page order between the header's and the footer's; wrappers
 * shared by consecutive sections are opened once (like the site's shared shell).
 */
export function frameDoc(page: Section[], parts: FrameParts, mode: FrameMode, editCss: string, base: string): string {
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
      body += frameCopy(w + closeOf(w), mode).slice(0, -closeOf(w).length)
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
    `<meta name="viewport" content="width=device-width,initial-scale=1"><base href="${esc(base)}">` +
    `${mergeHeads(all.map(({ s }) => s.content.head ?? ''), mode)}<style data-wb-edit>${editCss}</style></head><body${bodyClass}${bodyAttrs}>${body}</body></html>`
  )
}

/* ---------- Keeping the frame in step with the markup (static mode) ---------- */

interface SecNodes {
  parent: Node
  nodes: Node[]
}

/**
 * The page sections' nodes in the frame, and the reconciler that applies new markup to them.
 * Works in the browser (needs the frame's DOM).
 */
export class FrameSync {
  readonly secs = new Map<string, SecNodes>()
  constructor(
    readonly doc: Document,
    private mode: FrameMode,
  ) {}

  /** Reads and removes the section markers; false when a section's markup is not well formed. */
  collect(): boolean {
    const w = this.doc.createTreeWalker(this.doc.body ?? this.doc.documentElement, NodeFilter.SHOW_COMMENT)
    const starts = new Map<string, Comment>()
    const marks: Comment[] = []
    let ok = true
    for (let n = w.nextNode() as Comment | null; n; n = w.nextNode() as Comment | null) {
      const m = /^(\/?)wb:(.+)$/.exec(n.data)
      if (!m) continue
      marks.push(n)
      if (!m[1]) starts.set(m[2], n)
      else {
        const s = starts.get(m[2])
        if (!s || s.parentNode !== n.parentNode) {
          ok = false
          continue
        }
        const nodes: Node[] = []
        for (let x = s.nextSibling; x && x !== n; x = x.nextSibling) nodes.push(x)
        if (!m[2].startsWith('h:') && !m[2].startsWith('f:')) this.secs.set(m[2], { parent: s.parentNode!, nodes })
      }
    }
    for (const c of marks) {
      // An empty section keeps an empty text node as its place.
      const id = /^\/?wb:(.+)$/.exec(c.data)![1]
      const e = this.secs.get(id)
      if (e && !e.nodes.length && !c.data.startsWith('/')) {
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
      out.push(...this.fragment(parent, pending))
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
      if (fe && was !== undefined && ctx.oldTag.get(id!) === k.tag && !RAW.has(k.tag) && k.tag !== 'svg' && !k.foreign && fe.parentNode) {
        flush()
        this.attrs(fe, t, k)
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

  /** Nodes parsed from source markup in the context of parent. */
  private fragment(parent: Node, src: string): Node[] {
    const r = this.doc.createRange()
    r.selectNodeContents(parent)
    const f = r.createContextualFragment(frameCopy(src, this.mode))
    return Array.from(f.childNodes)
  }

  /** Gives the frame element the attributes of source element k (in the frame's copy). */
  private attrs(fe: Element, t: Tree, k: El) {
    const copy = parse(frameCopy(t.src.slice(k.start, k.openEnd) + '</' + k.tag + '>', this.mode)).all[0]
    const want = new Map<string, string>()
    for (const a of copy?.attrs ?? []) want.set(a.name, a.value ?? '')
    for (const a of Array.from(fe.attributes)) if (!want.has(a.name)) fe.removeAttribute(a.name)
    for (const [n, v] of want) if (fe.getAttribute(n) !== v) fe.setAttribute(n, v)
  }
}

interface Ctx {
  oldOuter: Map<string, string>
  oldTag: Map<string, string>
  frame: Map<string, Element>
}
