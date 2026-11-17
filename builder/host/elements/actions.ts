/**
 * Element operations against the builder's document: each one writes the sections it changed
 * back as ONE labelled history entry (the builder's 'replace' action, which never merges with the
 * previous entry), then selects the element the operation names.
 */
import type { BuilderApi } from '@/builder/store'
import type { DocState, Section } from '@/builder/types'
import { t } from '../i18n'
import { clipboard, getElState, setElState } from './state'
import { attr, outer } from './html'
import { copyEntries, entryOf, idsIn, parseStyleAttr, rehome, setDecls, variantOf, withoutDecls, type Device, type LocalEntry, type LocalSection, type LocalStyles, type State } from './local'
import { childIds, container, duplicate, insert, locate, move, parentId, placeBy, remove, reIdHtml, replaceOuter, setAttr, setInner, setTag, step, tree, unwrap, whereIs, wrap, type Page, type Place, type Result } from './ops'

type B = Pick<BuilderApi, 'doc' | 'dispatch' | 'select' | 'notify'>

/** The page's code sections as the operations see them. */
export function pageOf(doc: DocState): Page {
  const order: string[] = []
  const html: Record<string, string> = {}
  for (const s of doc.sections) {
    if (s.kind !== 'code') continue
    order.push(s.id)
    html[s.id] = s.content.html ?? ''
  }
  return { order, html }
}

/** The document with the changed sections' markup (and other content keys) replaced. */
export function withContent(doc: DocState, html: Record<string, string>, extra?: Record<string, Record<string, string>>): DocState {
  return {
    ...doc,
    sections: doc.sections.map((s: Section) => {
      const h = html[s.id]
      const x = extra?.[s.id]
      if (h === undefined && !x) return s
      return { ...s, content: { ...s.content, ...(h !== undefined ? { html: h } : {}), ...x } }
    }),
  }
}

/** The document with the given sections' local styles replaced (undefined: the field goes). */
export function withLocal(doc: DocState, local: Record<string, LocalStyles | undefined>): DocState {
  if (!Object.keys(local).length) return doc
  return {
    ...doc,
    sections: doc.sections.map((s: Section) => {
      if (!(s.id in local)) return s
      const { local: _drop, ...rest } = s as LocalSection
      const l = local[s.id]
      return (l && Object.keys(l).length ? { ...rest, local: l } : rest) as Section
    }),
  }
}

/** Every code section's local styles merged (ids are unique on the page). */
export function pageLocal(doc: DocState): LocalStyles {
  const out: LocalStyles = {}
  for (const s of doc.sections as LocalSection[]) if (s.kind === 'code' && s.local) Object.assign(out, s.local)
  return out
}

/**
 * The latest document: the canvas keeps it current between renders, so two quick operations
 * (a key held down) each build on the one before.
 */
export const latest: { doc: DocState | null } = { doc: null }

const docOf = (b: B) => latest.doc ?? b.doc

/** Selects element id (null: nothing) and its section in the builder. */
export function selectEl(b: Pick<B, 'select' | 'doc'>, id: string | null, page?: Page) {
  setElState((s) => ({ sel: id, editing: null, classEdit: s.sel === id ? s.classEdit : null }))
  if (!id) return
  const at = locate(page ?? pageOf(latest.doc ?? b.doc), id)
  if (at) b.select({ sectionId: at.sec, el: 'html' })
}

/**
 * Writes an operation's result as one history entry; false when there was nothing to do. A copy's
 * new elements get the local styles of the elements they copy (from `src`, else the page's), and
 * local styles follow their elements to other sections and go with deleted ones (rehome).
 */
export function commit(b: B, label: string, res: Result | null, extra?: Record<string, Record<string, string>>, local?: Record<string, LocalStyles | undefined>, src?: LocalStyles | null): boolean {
  if (!res || (!Object.keys(res.html).length && !extra && !local)) return false
  let doc = withContent(docOf(b), res.html, extra)
  if (local) doc = withLocal(doc, local)
  if (res.copies?.to.length) {
    const copied = copyEntries(src ?? pageLocal(doc), res.copies.from, res.copies.to)
    if (Object.keys(copied).length) {
      const into = (doc.sections as LocalSection[]).find((s) => s.kind === 'code' && idsIn(s.content.html ?? '').has(res.copies!.to[0]))
      if (into) doc = withLocal(doc, { [into.id]: { ...(into.local ?? {}), ...copied } })
    }
  }
  doc = withLocal(doc, rehome(doc.sections as LocalSection[]))
  latest.doc = doc
  b.dispatch({ type: 'replace', doc, label })
  if (res.select !== undefined) selectEl(b, res.select, pageOf(doc))
  return true
}

const page = (b: B) => pageOf(docOf(b))

export const act = {
  moveTo(b: B, id: string, place: Place) {
    return commit(b, t('Element moved'), move(page(b), id, place))
  },
  step(b: B, id: string, dir: -1 | 1) {
    if (!commit(b, t('Element moved'), step(page(b), id, dir))) b.notify(dir < 0 ? t('It is already the first one.') : t('It is already the last one.'))
  },
  duplicate(b: B, id: string) {
    commit(b, t('Element duplicated'), duplicate(page(b), id))
  },
  remove(b: B, id: string) {
    const p = page(b)
    const r = remove(p, id)
    if (!r) {
      const at = locate(p, id)
      b.notify(at && at.el.parent?.tag === '#root' ? t('This is the whole section: remove it from the section list.') : t('This element cannot be deleted here.'))
      return
    }
    commit(b, t('Element deleted'), r)
  },
  wrap(b: B, id: string) {
    commit(b, t('Element wrapped in a Div'), wrap(page(b), id))
  },
  unwrap(b: B, id: string) {
    if (!commit(b, t('Element unwrapped'), unwrap(page(b), id))) b.notify(t('Only a container with elements inside can be unwrapped.'))
  },
  setInner(b: B, id: string, html: string, label = t('Text changed')) {
    return commit(b, label, setInner(page(b), id, html))
  },
  copy(b: B, id: string) {
    const at = locate(page(b), id)
    if (!at) return
    clipboard.html = outer(at.tree, at.el)
    const all = pageLocal(docOf(b))
    const mine: LocalStyles = {}
    for (const x of idsIn(clipboard.html)) if (all[x]) mine[x] = JSON.parse(JSON.stringify(all[x]))
    clipboard.local = mine
    b.notify(t('Element copied'))
  },
  /** Pastes the copied element after id (inside: as its last child). */
  paste(b: B, id: string, inside = false) {
    if (!clipboard.html) return false
    const p = page(b)
    let place: Place | null = null
    if (inside) {
      const w = locate(p, id)
      if (w) place = placeBy(p, w.sec, id, null, true)
    } else {
      const w = whereIs(p, id)
      if (w) place = placeBy(p, w.sec, w.parent, id, true)
    }
    if (!place || !commit(b, t('Element pasted'), insert(p, place, clipboard.html), undefined, undefined, clipboard.local)) {
      b.notify(t('The element cannot be pasted here.'))
      return false
    }
    return true
  },
  parent(b: B, id: string) {
    const p = parentId(page(b), id)
    if (p) selectEl(b, p)
    else b.notify(t('This is the section’s outermost element.'))
  },
  /** Inserts new markup (a palette element, a design sample) at place, with its local styles. */
  insertAt(b: B, place: Place, html: string, local: LocalStyles | null, label = t('Element added')) {
    const r = insert(page(b), place, html)
    if (!r || !commit(b, label, r, undefined, undefined, local ?? {})) {
      b.notify(t('The element cannot be added here.'))
      return false
    }
    return true
  },
  /** Inserts new markup after the selected element (none: at the end of the selected or last section's content). */
  insertNear(b: B, sel: string | null, sectionId: string | null, html: string, local: LocalStyles | null, label?: string) {
    const p = page(b)
    let place: Place | null = null
    if (sel) {
      const at = locate(p, sel)
      // An empty container takes it inside; anything else gets it after.
      if (at && container(at.tree, at.el) && !childIds(at.tree, at.el).length && at.el.tag !== 'a' && at.el.tag !== 'button') place = placeBy(p, at.sec, sel, null, true)
      else {
        const w = whereIs(p, sel)
        if (w) place = placeBy(p, w.sec, w.parent, sel, true)
      }
    }
    if (!place) {
      const sec = sectionId && p.html[sectionId] !== undefined ? sectionId : p.order[p.order.length - 1]
      if (!sec) return false
      const t0 = tree(p.html[sec])
      const tops = childIds(t0, t0.root)
      const last = tops[tops.length - 1]
      const lastEl = last ? t0.byId.get(last) : null
      place = lastEl && container(t0, lastEl) ? placeBy(p, sec, last, null, true) : placeBy(p, sec, null, null, true)
    }
    return place ? act.insertAt(b, place, html, local, label) : false
  },
  /** Sets (value) or removes (null) attributes of element id, as one history entry. */
  setAttrs(b: B, id: string, attrs: Record<string, string | null>, label = t('Attributes changed')) {
    let p = page(b)
    const html: Record<string, string> = {}
    for (const [n, v] of Object.entries(attrs)) {
      const r = setAttr(p, id, n, v)
      if (!r) continue
      Object.assign(html, r.html)
      p = { ...p, html: { ...p.html, ...r.html } }
    }
    return Object.keys(html).length ? commit(b, label, { html, select: id }) : false
  },
  /** Attribute changes of several elements (an image and its picture's sources), as one history entry. */
  setAttrsMany(b: B, edits: { id: string; attrs: Record<string, string | null> }[], label: string, select?: string) {
    let p = page(b)
    const html: Record<string, string> = {}
    for (const e of edits) {
      for (const [n, v] of Object.entries(e.attrs)) {
        const r = setAttr(p, e.id, n, v)
        if (!r) continue
        Object.assign(html, r.html)
        p = { ...p, html: { ...p.html, ...r.html } }
      }
    }
    return Object.keys(html).length ? commit(b, label, { html, select: select ?? edits[0]?.id }) : false
  },
  setTag(b: B, id: string, tag: string) {
    if (!commit(b, t('Tag changed'), setTag(page(b), id, tag))) {
      b.notify(t('This element’s tag cannot be changed to {tag}.', { tag }))
      return false
    }
    return true
  },
  /** Replaces element id's markup (an icon's svg), keeping its id and classes. */
  replace(b: B, id: string, html: string, label = t('Icon changed'), keep?: string[]) {
    return commit(b, label, replaceOuter(page(b), id, html, keep))
  },
  copyClasses(b: B, id: string) {
    const at = locate(page(b), id)
    clipboard.classes = at ? (attr(at.el, 'class') ?? '').split(/\s+/).filter(Boolean) : null
    b.notify(clipboard.classes?.length ? t('Classes copied') : t('This element has no classes to copy.'))
  },
  pasteClasses(b: B, id: string) {
    if (!clipboard.classes) return false
    return act.setAttrs(b, id, { class: clipboard.classes.join(' ') || null }, t('Classes pasted'))
  },
  rename(b: B, sec: string, id: string, name: string, names: Record<string, string>) {
    const next = { ...names }
    if (name.trim()) next[id] = name.trim().slice(0, 80)
    else delete next[id]
    const json = Object.keys(next).length ? JSON.stringify(next) : ''
    const s = docOf(b).sections.find((x) => x.id === sec)
    if (!s || (s.content.names ?? '') === json) return
    commit(b, t('Element renamed'), { html: {} }, { [sec]: { names: json } })
  },
}

/** The section holding element id, with its local styles. */
function sectionOf(b: B, id: string): { sec: LocalSection; page: Page } | null {
  const doc = docOf(b)
  const p = pageOf(doc)
  const at = locate(p, id)
  const sec = at && (doc.sections.find((s) => s.id === at.sec) as LocalSection | undefined)
  return sec ? { sec, page: p } : null
}

export const style = {
  /**
   * Sets (value) or resets (null) declarations of element id's local style at device bp and
   * state st, as one history entry. A declaration the element's inline style attribute sets for
   * a property being set (or one of `inline`, the inline declarations that cover it, e.g. padding
   * for padding-top) moves into the element's local style (all devices, normal state) first: an
   * inline style would beat the local one. The attribute's other declarations stay.
   */
  set(b: B, id: string, bp: Device, st: State, decls: Record<string, string | null>, inline?: string[], label = t('Style changed')): boolean {
    const w = sectionOf(b, id)
    if (!w) return false
    let local = w.sec.local
    let html: Record<string, string> = {}
    const at = locate(w.page, id)!
    const styleAttr = attr(at.el, 'style')
    const setting = Object.keys(decls).filter((k) => decls[k] !== null && decls[k] !== '')
    if (styleAttr && setting.length) {
      const names = new Set([...setting, ...(inline ?? [])])
      const found = parseStyleAttr(styleAttr).filter((d) => names.has(d.name))
      if (found.length) {
        const base = variantOf(local, id, '', '')
        const moved: Record<string, string> = {}
        for (const d of found) if (!(d.name in base) && d.value) moved[d.name] = d.important ? `${d.value} !important` : d.value
        local = setDecls(local, id, '', '', moved)
        const r = setAttr(w.page, id, 'style', withoutDecls(styleAttr, new Set(found.map((d) => d.name))))
        if (r) html = r.html
      }
    }
    local = setDecls(local, id, bp, st, decls)
    if (JSON.stringify(local) === JSON.stringify(w.sec.local ?? {}) && !Object.keys(html).length) return false
    return commit(b, label, { html, select: id }, undefined, { [w.sec.id]: local })
  },
  /** Copies element id's whole local style (every device and state). */
  copy(b: B, id: string) {
    const w = sectionOf(b, id)
    clipboard.style = w ? entryOf(w.sec.local, id) : null
    b.notify(clipboard.style ? t('Style copied') : t('This element has no style of its own to copy.'))
  },
  /** Replaces element id's local style by the copied one. */
  paste(b: B, id: string) {
    const w = sectionOf(b, id)
    if (!w || !clipboard.style) return false
    const entry: LocalEntry = JSON.parse(JSON.stringify(clipboard.style))
    return commit(b, t('Style pasted'), { html: {}, select: id }, undefined, { [w.sec.id]: { ...(w.sec.local ?? {}), [id]: entry } })
  },
  /** Removes element id's local style (every device and state). */
  clear(b: B, id: string) {
    const w = sectionOf(b, id)
    if (!w?.sec.local?.[id]) return false
    const next = { ...w.sec.local }
    delete next[id]
    return commit(b, t('Style cleared'), { html: {}, select: id }, undefined, { [w.sec.id]: next })
  },
}

export { reIdHtml }
export const selected = () => getElState().sel
