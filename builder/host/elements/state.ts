/**
 * Editor-only state of the element editor, shared by the canvas and the navigator: the selected
 * and hovered element, elements hidden in the editor or shown temporarily, the live-edit switch,
 * the navigator's open rows and the element clipboard. Nothing here is stored in the document.
 */
import { useSyncExternalStore } from 'react'
import type { Device, LocalEntry, LocalStyles, State } from './local'

export interface ElState {
  /** Selected element (data-wbc id). */
  sel: string | null
  /** Element under the pointer (canvas or navigator). */
  hover: string | null
  /** Hidden in the editor only (eye in the navigator). */
  hidden: ReadonlySet<string>
  /** Shown although the design hides it ("Show temporarily"). */
  shown: ReadonlySet<string>
  /** Navigator rows the user opened (element ids, section ids). */
  open: ReadonlySet<string>
  /** Live edit: the design's scripts run in the editing frame (needs `scripts`). */
  live: boolean
  /**
   * The author confirmed, for this editor session, that the design's scripts may run with their
   * admin access (Live edit, Preview). Never stored: a new session asks again.
   */
  scripts: boolean
  /** Element whose text is being edited in place. */
  editing: string | null
  /** Style panel: the state being styled (the device is the canvas device). */
  styleState: State
  /** Style panel: the design class being edited (every element with it), null: the element's own style. */
  classEdit: string | null
  /** The palette drawer: closed, or its open tab. */
  palette: null | 'elements' | 'design' | 'variables'
}

/** The canvas device as local styles name it. */
export const deviceOf = (d: 'desktop' | 'tablet' | 'mobile'): Device => (d === 'desktop' ? '' : d)

let state: ElState = { sel: null, hover: null, hidden: new Set(), shown: new Set(), open: new Set(), live: false, scripts: false, editing: null, styleState: '', classEdit: null, palette: null }
const subs = new Set<() => void>()

export const getElState = () => state

export function setElState(patch: Partial<ElState> | ((s: ElState) => Partial<ElState>)) {
  const p = typeof patch === 'function' ? patch(state) : patch
  let changed = false
  for (const k of Object.keys(p) as (keyof ElState)[]) if (state[k] !== p[k]) changed = true
  if (!changed) return
  state = { ...state, ...p }
  subs.forEach((f) => f())
}

export function subscribeEl(f: () => void) {
  subs.add(f)
  return () => {
    subs.delete(f)
  }
}

export const useElState = () => useSyncExternalStore(subscribeEl, getElState, getElState)

export function toggleIn(set: ReadonlySet<string>, id: string, on?: boolean): Set<string> {
  const next = new Set(set)
  if (on ?? !next.has(id)) next.add(id)
  else next.delete(id)
  return next
}

/**
 * The element clipboard (an element's markup and the local styles of the elements in it) and the
 * style clipboard (one element's local style); session only.
 */
export const clipboard: { html: string | null; local: LocalStyles | null; style: LocalEntry | null; classes: string[] | null } = { html: null, local: null, style: null, classes: null }

/** Resets everything (tests, a new document). */
export function resetElState() {
  state = { sel: null, hover: null, hidden: new Set(), shown: new Set(), open: new Set(), live: false, scripts: false, editing: null, styleState: '', classEdit: null, palette: null }
  clipboard.html = null
  clipboard.local = null
  clipboard.style = null
  clipboard.classes = null
  subs.forEach((f) => f())
}

/**
 * The canvas frame as the Style panel reads it: its document and a version that changes whenever
 * the frame's markup or stylesheets change (the panel's cascade cache key).
 */
export const frameInfo: { doc: Document | null; version: number } = { doc: null, version: 0 }
const frameSubs = new Set<() => void>()
export function bumpFrame(doc?: Document | null) {
  if (doc !== undefined) frameInfo.doc = doc
  frameInfo.version++
  frameSubs.forEach((f) => f())
}
export const useFrameVersion = () =>
  useSyncExternalStore(
    (f) => {
      frameSubs.add(f)
      return () => {
        frameSubs.delete(f)
      }
    },
    () => frameInfo.version,
    () => frameInfo.version,
  )
