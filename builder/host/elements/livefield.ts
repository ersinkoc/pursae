/**
 * Typing into a text, link, alt or attribute field changes the canvas while typing (debounced), as
 * one history entry per typing session — the way the builder's own inputs behave.
 *
 * A session starts with the first keystroke in a focused field and ends on blur / Enter (commit) or
 * Escape (revert). While it lasts:
 *  - every pause of LIVE_DELAY ms writes the draft through the field's element action (actions.ts →
 *    commit → 'replace', so the canvas patches the live DOM in place with applyToLive: no frame
 *    reload), all under one history key ("field:N"), which the store merges into the entry the
 *    first write made (patch 0024: a 'field:' key merges however long the pauses were);
 *  - a value the rules refuse (an unsafe address) is skipped silently; the final write on blur
 *    runs the same rules and says why when it refuses;
 *  - Escape takes the entry back: the history goes to where it was when the session began (when
 *    something else was done in between, the original value is written instead).
 */
import { useEffect, useRef } from 'react'
import { useB } from '@/builder/store'
import { markupReadOnly } from './trust'
import { withKey } from './actions'

/** The pause after the last keystroke before the canvas follows. */
export const LIVE_DELAY = 250

/** What the controller needs of the builder (BuilderApi's history and dispatch). */
export interface LiveBuilder {
  history: { cursor: number }
  dispatch: (a: { type: 'jump'; cursor: number }) => void
}

export interface LiveEdit<V> {
  /** The field changed: `get` reads its current value when the write is due (a rich box reads its DOM then). */
  touch(get: () => V): void
  /** Blur / Enter: the last draft is written (if it differs), the session ends. */
  commit(): void
  /** Escape: the session's changes are taken back, the session ends. */
  revert(): void
  /** A session is open (a keystroke has not been committed or reverted yet). */
  open(): boolean
}

export interface LiveOptions<V> {
  builder: () => LiveBuilder
  /** Writes a value; false: nothing was written (unchanged, or refused). `live`: a write while typing. */
  apply: (value: V, live: boolean) => boolean | void
  /** The value the field had when the session began (for a revert that cannot step the history back). */
  original: () => V
  delay?: number
}

let seq = 0

export function createLiveEdit<V>(o: LiveOptions<V>): LiveEdit<V> {
  const delay = o.delay ?? LIVE_DELAY
  let key: string | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let get: (() => V) | null = null
  let start = 0
  let wrote = false
  let lastWritten: { v: V } | null = null

  const stop = () => {
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
  const reset = () => {
    stop()
    key = null
    get = null
    wrote = false
    lastWritten = null
  }
  const write = (live: boolean) => {
    if (!get || key === null) return
    const v = get()
    if (lastWritten && Object.is(lastWritten.v, v)) return
    if (withKey(key, () => o.apply(v, live)) !== false) {
      wrote = true
      lastWritten = { v }
    }
  }
  return {
    touch(g) {
      if (key === null) {
        key = `field:${++seq}`
        start = o.builder().history.cursor
      }
      get = g
      stop()
      timer = setTimeout(() => {
        timer = null
        write(true)
      }, delay)
    },
    commit() {
      if (key === null) return
      stop()
      write(false)
      reset()
    },
    revert() {
      if (key === null) return
      stop()
      const did = wrote
      reset()
      if (!did) return
      const b = o.builder()
      // The session's one entry is the one after where it began: step back over it.
      if (b.history.cursor === start + 1) b.dispatch({ type: 'jump', cursor: start })
      else o.apply(o.original(), false)
    },
    open: () => key !== null,
  }
}

/** The controller of one field for a component: always the latest builder and callbacks; a pending draft is written when the field goes away. */
export function useLiveEdit<V>(apply: (value: V, live: boolean) => boolean | void, original: () => V): LiveEdit<V> {
  const b = useB()
  const ref = useRef({ b, apply, original })
  ref.current = { b, apply, original }
  const live = useRef<LiveEdit<V> | null>(null)
  if (!live.current)
    live.current = createLiveEdit<V>({
      builder: () => ref.current.b as unknown as LiveBuilder,
      apply: (v, isLive) => (isLive && markupReadOnly() ? false : ref.current.apply(v, isLive)),
      original: () => ref.current.original(),
    })
  useEffect(() => () => live.current?.commit(), [])
  return live.current
}
