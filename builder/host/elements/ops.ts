/**
 * Element operations over the markup of a page's code sections (pure functions, no DOM).
 *
 * The page is a list of sections, each with its source markup (content.html). An operation finds
 * the element by its data-wbc id, splices the source of the section(s) it touches and returns only
 * those sections' new markup; the canvas writes them back as one history entry. Bytes outside the
 * edited ranges never change.
 *
 * Rules every operation keeps:
 *  - the inside of an svg / math element is never edited, re-identified or a drop target;
 *  - script, style and other code elements are never moved into other elements;
 *  - void elements, raw text elements and elements with text of their own (a heading, a link
 *    label) take no element children;
 *  - a copy (duplicate, paste) gets new data-wbc ids and its HTML ids become name-2, name-3, …
 *    (free on the page), with aria-controls / aria-labelledby / for / href="#…" inside it following.
 */
import { attr, attrEdit, contains, elementKids, escText, isWs, outer, ownText, parse, splice, RAW, VOID, wbc, type Edit, type El, type Tree } from './html'

export interface Page {
  /** Section ids in page order. */
  order: string[]
  /** Section id → content.html. */
  html: Record<string, string>
}

/** Where an element goes: index among the element children of `parent` (null = the section's top level). */
export interface Place {
  sec: string
  parent: string | null
  index: number
}

export interface Result {
  /** New markup of the sections that changed. */
  html: Record<string, string>
  /** The element to select afterwards. */
  select?: string | null
  /** A copy's new data-wbc ids (to) of the original's (from), in order: its local styles follow. */
  copies?: { from: string[]; to: string[] }
}

export interface Located {
  sec: string
  tree: Tree
  el: El
}

/* ---------- Trees (cached per markup string) ---------- */

const cache = new Map<string, Tree>()
export function tree(src: string): Tree {
  let t = cache.get(src)
  if (!t) {
    t = parse(src)
    if (cache.size > 64) cache.delete(cache.keys().next().value!)
    cache.set(src, t)
  }
  return t
}

export function locate(page: Page, id: string): Located | null {
  for (const sec of page.order) {
    const src = page.html[sec]
    if (src === undefined || !src.includes(id)) continue
    const t = tree(src)
    const el = t.byId.get(id)
    if (el) return { sec, tree: t, el }
  }
  return null
}

/* ---------- New ids ---------- */

let seq = 0
/** A new element id, unique on the page (imported ids are numbers). Tests may replace it. */
export let freshId = () => `n${Date.now().toString(36)}${(seq++).toString(36)}`
export function setIdSource(fn: () => string) {
  freshId = fn
}

/** HTML ids used on the page (every section), for name-2 renaming. */
export function pageIds(page: Page): Set<string> {
  const out = new Set<string>()
  for (const sec of page.order) {
    for (const el of tree(page.html[sec] ?? '').all) {
      const v = attr(el, 'id')
      if (v) out.add(v)
    }
  }
  return out
}

const REF_ATTRS = ['aria-controls', 'aria-labelledby', 'aria-describedby', 'aria-owns', 'for', 'list', 'form', 'headers']

/**
 * The markup of `nodes` (top-level elements of tree t, in order, with the text between them) made
 * unique: new data-wbc ids, new HTML ids (base-2, base-3, … not in `taken`, which is updated) and
 * references inside following them. The inside of svg / math is copied as is.
 */
export function reIdRange(t: Tree, start: number, end: number, taken: Set<string>): { html: string; ids: string[]; from: string[] } {
  const els = t.all.filter((e) => e.start >= start && e.end <= end && !e.foreign)
  const renamed = new Map<string, string>()
  const edits: Edit[] = []
  const ids: string[] = []
  const from: string[] = []
  for (const e of els) {
    const id = attr(e, 'id')
    if (!id) continue
    const base = id.replace(/-\d+$/, '')
    let n = 2
    while (taken.has(`${base}-${n}`)) n++
    const next = `${base}-${n}`
    taken.add(next)
    renamed.set(id, next)
  }
  for (const e of els) {
    const w = wbc(e)
    if (w !== null) {
      const nid = freshId()
      ids.push(nid)
      from.push(w)
      edits.push(...attrEdit(t, e, 'data-wbc', nid))
    }
    const id = attr(e, 'id')
    if (id && renamed.has(id)) edits.push(...attrEdit(t, e, 'id', renamed.get(id)!))
    for (const a of REF_ATTRS) {
      const v = attr(e, a)
      if (!v) continue
      const nv = v.split(/\s+/).map((x) => renamed.get(x) ?? x).join(' ')
      if (nv !== v) edits.push(...attrEdit(t, e, a, nv))
    }
    const href = attr(e, 'href')
    if (href?.startsWith('#') && renamed.has(href.slice(1))) edits.push(...attrEdit(t, e, 'href', `#${renamed.get(href.slice(1))}`))
  }
  const local = edits.map((x) => ({ start: x.start - start, end: x.end - start, text: x.text }))
  return { html: splice(t.src.slice(start, end), local), ids, from }
}

/** A pasted snippet (an element's markup) made unique on the page. */
export function reIdHtml(html: string, page: Page): { html: string; ids: string[]; from: string[] } {
  const t = parse(html)
  return reIdRange(t, 0, html.length, pageIds(page))
}

/* ---------- Rules ---------- */

/** Code elements: kept where they are (never moved into other elements). */
export const CODE_TAGS = new Set(['script', 'style', 'link', 'meta', 'noscript', 'template', 'base', 'title'])
/** Elements that take no element children on the canvas. */
const LEAF_TAGS = new Set(['img', 'picture', 'video', 'audio', 'canvas', 'iframe', 'object', 'embed', 'select', 'option', 'input', 'textarea', 'svg', 'math', 'br', 'hr'])

/** The element may be moved, duplicated, wrapped or deleted. */
export function movable(el: El): boolean {
  return el.tag !== '#root' && !el.foreign && !CODE_TAGS.has(el.tag) && wbc(el) !== null
}

/** The element takes element children (a drop target). */
export function container(t: Tree, el: El): boolean {
  if (el.tag === '#root') return true
  if (el.foreign || VOID.has(el.tag) || RAW.has(el.tag) || LEAF_TAGS.has(el.tag) || CODE_TAGS.has(el.tag)) return false
  return !ownText(t, el)
}

export type DropError = 'missing' | 'locked' | 'self' | 'leaf' | 'code'

/** Why `id` cannot go to `place` (null: it can). */
export function dropError(page: Page, id: string, place: Place): DropError | null {
  const from = locate(page, id)
  if (!from) return 'missing'
  if (!movable(from.el)) return 'code'
  const src = page.html[place.sec]
  if (src === undefined) return 'locked'
  const t = tree(src)
  const parent = place.parent === null ? t.root : t.byId.get(place.parent)
  if (!parent) return 'missing'
  if (place.sec === from.sec && contains(from.el, parent)) return 'self'
  if (!container(t, parent)) return 'leaf'
  return null
}

/* ---------- Helpers ---------- */

/** The edit that removes element el (with the whitespace before it when whitespace follows too). */
function cutEdit(t: Tree, el: El): Edit {
  const sib = el.parent!.children
  const i = sib.indexOf(el)
  const prev = sib[i - 1]
  const next = sib[i + 1]
  const start = isWs(t, prev) && (!next || isWs(t, next)) ? prev.start : el.start
  return { start, end: el.end, text: '' }
}

/** The text before el when it is whitespace only (kept as the separator of a copy next to it). */
function wsBefore(t: Tree, el: El): string {
  const sib = el.parent!.children
  const prev = sib[sib.indexOf(el) - 1]
  return isWs(t, prev) ? t.src.slice(prev.start, prev.end) : ''
}

/** The edit that inserts `html` into parent at element index (skipping `skip`), with the siblings' separator. */
function insertEdit(t: Tree, parent: El, index: number, html: string, skip?: El): Edit {
  const kids = elementKids(parent).filter((k) => k !== skip)
  const i = Math.max(0, Math.min(kids.length, index))
  if (i < kids.length) {
    const ref = kids[i]
    const ws = wsBefore(t, ref)
    return { start: ref.start, end: ref.start, text: html + ws }
  }
  if (kids.length) {
    const last = kids[kids.length - 1]
    return { start: last.end, end: last.end, text: wsBefore(t, last) + html }
  }
  return { start: parent.closeStart, end: parent.closeStart, text: html }
}

const one = (sec: string, src: string, edits: Edit[]): Record<string, string> => ({ [sec]: splice(src, edits) })

/* ---------- Operations ---------- */

/** Replaces the content of element id (its inner markup). */
export function setInner(page: Page, id: string, html: string): Result | null {
  const at = locate(page, id)
  if (!at || at.el.foreign || at.el.tag === 'svg' || VOID.has(at.el.tag)) return null
  const { tree: t, el } = at
  if (t.src.slice(el.openEnd, el.closeStart) === html) return null
  return { html: one(at.sec, t.src, [{ start: el.openEnd, end: el.closeStart, text: html }]), select: id }
}

/** Sets the element's text (escaped). */
export const setText = (page: Page, id: string, text: string) => setInner(page, id, escText(text))

/** Sets (value) or removes (null) an attribute. data-wbc cannot be changed. */
export function setAttr(page: Page, id: string, name: string, value: string | null): Result | null {
  const at = locate(page, id)
  const n = name.toLowerCase()
  if (!at || n === 'data-wbc' || !/^[a-z_:][a-z0-9_.:-]*$/.test(n)) return null
  if (attr(at.el, n) === value) return null
  const edits = attrEdit(at.tree, at.el, n, value)
  if (!edits.length) return null
  return { html: one(at.sec, at.tree.src, edits), select: id }
}

const classes = (v: string | null) => (v ?? '').split(/\s+/).filter(Boolean)

export function addClass(page: Page, id: string, cls: string): Result | null {
  const at = locate(page, id)
  if (!at || !/^\S+$/.test(cls)) return null
  const list = classes(attr(at.el, 'class'))
  if (list.includes(cls)) return null
  const cur = attr(at.el, 'class')
  return setAttr(page, id, 'class', cur && cur.trim() ? `${cur.replace(/\s+$/, '')} ${cls}` : cls)
}

export function removeClass(page: Page, id: string, cls: string): Result | null {
  const at = locate(page, id)
  if (!at) return null
  const cur = attr(at.el, 'class')
  if (!classes(cur).includes(cls)) return null
  const next = classes(cur).filter((c) => c !== cls).join(' ')
  return setAttr(page, id, 'class', next || null)
}

/** Moves element id to place (index counted without the element itself). */
export function move(page: Page, id: string, place: Place): Result | null {
  if (dropError(page, id, place)) return null
  const from = locate(page, id)!
  const { tree: ft, el } = from
  const html = outer(ft, el)
  const tt = tree(page.html[place.sec])
  const parent = place.parent === null ? tt.root : tt.byId.get(place.parent)!
  if (place.sec === from.sec) {
    // Same position: nothing to do (the index is counted without the element itself).
    const kids = elementKids(parent)
    const n = kids.length - (el.parent === parent ? 1 : 0)
    if (el.parent === parent && Math.max(0, Math.min(n, place.index)) === kids.indexOf(el)) return null
    const ins = insertEdit(tt, parent, place.index, html, el)
    const cut = cutEdit(ft, el)
    // The insertion point is an element boundary outside the element, so it never lies inside the
    // cut (which is the element and the whitespace before it); it may touch it.
    if (ins.start > cut.start && ins.start < cut.end) return null
    return { html: { [place.sec]: splice(ft.src, [cut, ins]) }, select: id }
  }
  return {
    html: { [from.sec]: splice(ft.src, [cutEdit(ft, el)]), [place.sec]: splice(tt.src, [insertEdit(tt, parent, place.index, html)]) },
    select: id,
  }
}

/** Moves element id before (-1) or after (1) its element sibling; null at the end of the list. */
export function step(page: Page, id: string, dir: -1 | 1): Result | null {
  const at = locate(page, id)
  if (!at || !movable(at.el)) return null
  const kids = elementKids(at.el.parent!)
  const i = kids.indexOf(at.el)
  const j = i + dir
  if (j < 0 || j >= kids.length) return null
  const parent = at.el.parent!.tag === '#root' ? null : wbc(at.el.parent!)
  if (at.el.parent!.tag !== '#root' && parent === null) return null
  return move(page, id, { sec: at.sec, parent, index: j })
}

/** Inserts markup (made unique first) at place. */
export function insert(page: Page, place: Place, html: string): Result | null {
  const src = page.html[place.sec]
  if (src === undefined) return null
  const t = tree(src)
  const parent = place.parent === null ? t.root : t.byId.get(place.parent)
  if (!parent || !container(t, parent)) return null
  const copy = reIdHtml(html, page)
  if (!copy.html.trim()) return null
  return { html: one(place.sec, src, [insertEdit(t, parent, place.index, copy.html)]), select: copy.ids[0] ?? null, copies: { from: copy.from, to: copy.ids } }
}

/** Deletes element id; refuses the only top-level element of a section (delete the section instead). */
export function remove(page: Page, id: string): Result | null {
  const at = locate(page, id)
  if (!at || !movable(at.el)) return null
  if (at.el.parent!.tag === '#root' && elementKids(at.tree.root).length === 1) return null
  const parent = at.el.parent!
  return { html: one(at.sec, at.tree.src, [cutEdit(at.tree, at.el)]), select: parent.tag === '#root' ? null : wbc(parent) }
}

/** Puts a copy of element id right after it. */
export function duplicate(page: Page, id: string, taken?: Set<string>): Result | null {
  const at = locate(page, id)
  if (!at || !movable(at.el)) return null
  const { tree: t, el } = at
  const copy = reIdRange(t, el.start, el.end, taken ?? pageIds(page))
  return { html: one(at.sec, t.src, [{ start: el.end, end: el.end, text: wsBefore(t, el) + copy.html }]), select: copy.ids[0], copies: { from: copy.from, to: copy.ids } }
}

/** Wraps element id in a new element (a div). */
export function wrap(page: Page, id: string, tag = 'div'): Result | null {
  const at = locate(page, id)
  if (!at || !movable(at.el) || !/^[a-z][a-z0-9-]*$/.test(tag)) return null
  const nid = freshId()
  const { tree: t, el } = at
  return {
    html: one(at.sec, t.src, [
      { start: el.start, end: el.start, text: `<${tag} data-wbc="${nid}">` },
      { start: el.end, end: el.end, text: `</${tag}>` },
    ]),
    select: nid,
  }
}

/** Replaces element id by its content. */
export function unwrap(page: Page, id: string): Result | null {
  const at = locate(page, id)
  if (!at || !movable(at.el)) return null
  const { tree: t, el } = at
  if (!container(t, el) || !elementKids(el).length) return null
  const first = elementKids(el)[0]
  return {
    html: one(at.sec, t.src, [
      { start: el.start, end: el.openEnd, text: '' },
      { start: el.closeStart, end: el.end, text: '' },
    ]),
    select: wbc(first),
  }
}

/* ---------- Reading helpers for the canvas and the navigator ---------- */

/** Element id of the parent (null at a section's top level). */
export function parentId(page: Page, id: string): string | null {
  const at = locate(page, id)
  if (!at) return null
  for (let p = at.el.parent; p && p.tag !== '#root'; p = p.parent) {
    const w = wbc(p)
    if (w) return w
  }
  return null
}

/** Selectable element children of an element (or of a section's top level). */
export function childIds(t: Tree, el: El): string[] {
  if (el.tag === 'svg' || el.foreign) return []
  const out: string[] = []
  const walk = (e: El) => {
    for (const k of elementKids(e)) {
      const w = wbc(k)
      if (w) out.push(w)
      else if (!k.foreign && k.tag !== 'svg') walk(k) // an element without id (e.g. tbody): its children
    }
  }
  walk(el)
  return out
}

/**
 * The place before / after element `ref`, a child of `parent` (null = the section's top level), or
 * at the start / end of parent when ref is null; counted without the moved element `moving`.
 */
export function placeBy(page: Page, sec: string, parent: string | null, ref: string | null, after: boolean, moving?: string | null): Place | null {
  const src = page.html[sec]
  if (src === undefined) return null
  const t = tree(src)
  const p = parent === null ? t.root : t.byId.get(parent)
  if (!p) return null
  const kids = elementKids(p).filter((k) => !moving || wbc(k) !== moving)
  if (ref === null) return { sec, parent, index: after ? kids.length : 0 }
  const i = kids.findIndex((k) => wbc(k) === ref)
  if (i < 0) return null
  return { sec, parent, index: after ? i + 1 : i }
}

/** The section holding element id and its parent's id (null = top level); null when the parent has no id. */
export function whereIs(page: Page, id: string): { sec: string; parent: string | null } | null {
  const at = locate(page, id)
  if (!at) return null
  const p = at.el.parent!
  if (p.tag === '#root') return { sec: at.sec, parent: null }
  const w = wbc(p)
  return w === null ? null : { sec: at.sec, parent: w }
}
