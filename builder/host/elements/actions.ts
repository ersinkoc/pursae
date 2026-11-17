/**
 * Element operations against the builder's document: each one writes the sections it changed
 * back as ONE labelled history entry (the builder's 'replace' action, which never merges with the
 * previous entry), then selects the element the operation names.
 */
import type { BuilderApi } from '@/builder/store'
import type { DocState, Section } from '@/builder/types'
import { t } from '../i18n'
import { clipboard, getElState, setElState } from './state'
import { outer } from './html'
import { duplicate, insert, locate, move, parentId, placeBy, remove, reIdHtml, setInner, step, unwrap, whereIs, wrap, type Page, type Place, type Result } from './ops'

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

/**
 * The latest document: the canvas keeps it current between renders, so two quick operations
 * (a key held down) each build on the one before.
 */
export const latest: { doc: DocState | null } = { doc: null }

const docOf = (b: B) => latest.doc ?? b.doc

/** Selects element id (null: nothing) and its section in the builder. */
export function selectEl(b: Pick<B, 'select' | 'doc'>, id: string | null, page?: Page) {
  setElState({ sel: id, editing: null })
  if (!id) return
  const at = locate(page ?? pageOf(latest.doc ?? b.doc), id)
  if (at) b.select({ sectionId: at.sec, el: 'html' })
}

/** Writes an operation's result as one history entry; false when there was nothing to do. */
export function commit(b: B, label: string, res: Result | null, extra?: Record<string, Record<string, string>>): boolean {
  if (!res || (!Object.keys(res.html).length && !extra)) return false
  const doc = withContent(docOf(b), res.html, extra)
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
  setInner(b: B, id: string, html: string) {
    return commit(b, t('Text changed'), setInner(page(b), id, html))
  },
  copy(b: B, id: string) {
    const at = locate(page(b), id)
    if (!at) return
    clipboard.html = outer(at.tree, at.el)
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
    if (!place || !commit(b, t('Element pasted'), insert(p, place, clipboard.html))) {
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

export { reIdHtml }
export const selected = () => getElState().sel
