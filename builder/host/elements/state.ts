/**
 * Editor-only state of the element editor, shared by the canvas and the navigator: the selected
 * and hovered element, elements hidden in the editor or shown temporarily, the live-edit switch,
 * the navigator's open rows and the element clipboard. Nothing here is stored in the document.
 */
import { useSyncExternalStore } from 'react'

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
  /** Live edit: the design's scripts run in the editing frame. */
  live: boolean
  /** Element whose text is being edited in place. */
  editing: string | null
}

let state: ElState = { sel: null, hover: null, hidden: new Set(), shown: new Set(), open: new Set(), live: false, editing: null }
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

/** The element clipboard (an element's markup); session only. */
export const clipboard: { html: string | null } = { html: null }

/** Resets everything (tests, a new document). */
export function resetElState() {
  state = { sel: null, hover: null, hidden: new Set(), shown: new Set(), open: new Set(), live: false, editing: null }
  clipboard.html = null
  subs.forEach((f) => f())
}
