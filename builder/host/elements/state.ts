/**
 * Editor-only state of the element editor, shared by the canvas and the navigator: the selected
 * and hovered element, elements hidden in the editor or shown temporarily, Live / Safe mode and
 * whether the design's scripts may run, Interact mode, the navigator's open rows and the element clipboard. Nothing here is stored in the document.
 */
import { useSyncExternalStore } from 'react'
import type { Device, LocalEntry, LocalStyles, State } from './local'
import { initialScripts } from './trust'

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
  /**
   * Live mode: the design's scripts run in the editing frame (needs `scripts`); off: Safe mode
   * (static, nothing runs). On from the start when the user trusts every design of the page.
   */
  live: boolean
  /**
   * The design's scripts may run with the user's admin access (Live, Preview, mixed-page Preview
   * through guard.ts): the user trusts every design of the page (Boot.scriptTrust, trust.ts) or
   * chose "This session only". The session answer is never stored.
   */
  scripts: boolean
  /** The canvas shows the "run this design's scripts?" prompt (not trusted yet, not dismissed). */
  prompt: boolean
  /** Kits this user always runs (Boot.scriptTrust, then the trust endpoint's answers). */
  trust: ReadonlySet<string>
  /** The user may run scripts at all (unfiltered_html; Boot.canRunScripts). */
  canRun: boolean
  /** Interact mode: pointer and keys reach the design (theme toggle, menus, sliders), not the editor. */
  interact: boolean
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

/** A fresh state; whether the scripts run is decided from the boot object (trust.ts), before any frame. */
const fresh = (): ElState => ({ sel: null, hover: null, hidden: new Set(), shown: new Set(), open: new Set(), ...initialScripts(), interact: false, editing: null, styleState: '', classEdit: null, palette: null })

let state: ElState | null = null
const subs = new Set<() => void>()

export const getElState = (): ElState => (state ??= fresh())

export function setElState(patch: Partial<ElState> | ((s: ElState) => Partial<ElState>)) {
  const cur = getElState()
  const p = typeof patch === 'function' ? patch(cur) : patch
  let changed = false
  for (const k of Object.keys(p) as (keyof ElState)[]) if (cur[k] !== p[k]) changed = true
  if (!changed) return
  state = { ...cur, ...p }
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

/** Resets everything (tests, a new document); the scripts decision is read from the boot object again. */
export function resetElState() {
  state = fresh()
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
