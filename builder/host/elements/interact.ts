/**
 * The canvas with the design's scripts running (plan K3–K5): which events reach the design
 * (Interact mode, or the held modifier), which element of the source a pointer means when a script
 * created or moved the element under it, and when the overlays must be measured again (the design's
 * scripts move things: scrolling, animations, menus opening, a resize).
 */

/** The key held to interact with the page for a moment (Alt / ⌥: free in the editor's pointer handling). */
export const HOLD_KEY = 'Alt'

/** The held key as the platform names it. */
export const holdLabel = () => (typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.platform) ? '⌥' : 'Alt')

/**
 * A pointer event goes to the design, not the editor: Interact mode is on, the modifier is held, or
 * the design's own script sent it (`el.click()`, a dispatched event: not a user's, isTrusted false;
 * a carousel or tabs that click their own buttons keep working in Edit mode).
 */
export const passesThrough = (interact: boolean, e: { altKey?: boolean; isTrusted?: boolean }) => interact || !!e.altKey || e.isTrusted === false

/**
 * A link the design's page would leave the frame with: the editor's document must stay (a `#` link
 * or a script's handler may run; following the address may not).
 */
export function leavesFrame(a: Element | null): boolean {
  if (!a) return false
  const href = a.getAttribute('href') ?? ''
  return !href.startsWith('#')
}

/**
 * The element of the source the pointer means: the nearest element (from `from` up) with a
 * data-wbc id the source has (`known`) that is also the frame's element for that id (the first one
 * in the document — a script's clone of an element, a slider's copied slide, carries the same id
 * but is not it). Elements a script created have no id: their nearest source ancestor is meant.
 * Ids of the locked header / footer (`h:` / `f:`) count as known.
 */
export function sourceTarget(doc: Document, from: Element | null, known: (id: string) => boolean): HTMLElement | null {
  for (let el = from?.closest<HTMLElement>('[data-wbc]') ?? null; el; el = el.parentElement?.closest<HTMLElement>('[data-wbc]') ?? null) {
    const id = el.getAttribute('data-wbc') ?? ''
    if (!(known(id) || id.includes(':'))) continue
    if (doc.querySelector(`[data-wbc="${cssEscape(id)}"]`) === el) return el
  }
  return null
}

const cssEscape = (v: string) => (typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(v) : v.replace(/["\\]/g, '\\$&'))

/**
 * Text whose live DOM differs from its source (a script drew or changed it): edited as source text
 * (K5), never read back from the DOM. Both sides are compared as the browser reads them — the
 * source parsed in an inert document (entities decoded, `<br/>` and `<br>` alike) — by their
 * element names and text, whitespace collapsed; attributes a script changes (a class it toggles)
 * do not count, they are never written back (richtext.writeBack keeps the source tags).
 */
export function drawnText(srcInner: string, live: string | Element): boolean {
  return shapeOf(srcInner) !== (typeof live === 'string' ? shapeOf(live) : shapeOfNode(live))
}

const shapeOf = (markup: string): string => {
  if (typeof DOMParser === 'undefined') return markup.replace(/\s+/g, ' ').trim()
  const d = new DOMParser().parseFromString(`<!DOCTYPE html><body>${markup}`, 'text/html')
  return shapeOfNode(d.body)
}

function shapeOfNode(root: Node): string {
  let out = ''
  const walk = (n: Node) => {
    for (let k = n.firstChild; k; k = k.nextSibling) {
      if (k.nodeType === 3) out += (k as Text).data
      else if (k.nodeType === 1) {
        const tag = (k as Element).localName
        out += `<${tag}>`
        walk(k)
        out += `</${tag}>`
      }
    }
  }
  walk(root)
  return out.replace(/\s+/g, ' ').trim()
}

/**
 * How a change of the sections' markup reaches a frame running the design's scripts (K4):
 *  - shown: the frame already shows it (a text edited in place in the frame);
 *  - patched: `apply` (live.ts applyToLive) patched the running page; its scripts keep their state;
 *  - reload: it cannot be patched (or the frame is not ready): load again, keeping scroll and theme.
 */
export function liveChange(prev: Record<string, string>, next: Record<string, string>, expect: Record<string, string>, apply: (() => { reload: boolean }) | null): 'shown' | 'patched' | 'reload' {
  if (Object.keys(next).every((k) => next[k] === prev[k] || next[k] === expect[k])) return 'shown'
  if (apply && !apply().reload) return 'patched'
  return 'reload'
}

/**
 * Calls `kick` whenever the frame's layout may have changed: scrolling (the page or any scroller
 * in it), a resize, DOM and attribute changes (a script opening a menu, moving an element), images
 * and fonts loading, CSS transitions and animations (kept up while they run). The caller throttles
 * `kick` to one measure per animation frame. Returns the cleanup.
 */
export function watchFrame(doc: Document, win: Window, kick: (busyMs?: number) => void): () => void {
  const off: (() => void)[] = []
  const on = (t: EventTarget, type: string, f: () => void, opts: AddEventListenerOptions = { capture: true, passive: true }) => {
    t.addEventListener(type, f, opts)
    off.push(() => t.removeEventListener(type, f, opts))
  }
  const once = () => kick()
  const busy = () => kick(1000)
  for (const type of ['scroll', 'load', 'pointerup', 'keyup', 'input', 'change', 'toggle']) on(doc, type, once)
  for (const type of ['transitionrun', 'transitionend', 'transitioncancel', 'animationstart', 'animationiteration', 'animationend', 'animationcancel']) on(doc, type, busy)
  on(win, 'resize', once, { passive: true })
  const MO = (win as unknown as { MutationObserver?: typeof MutationObserver }).MutationObserver ?? (typeof MutationObserver !== 'undefined' ? MutationObserver : undefined)
  if (MO && doc.documentElement) {
    const mo = new MO(once)
    mo.observe(doc.documentElement, { attributes: true, childList: true, subtree: true, characterData: true })
    off.push(() => mo.disconnect())
  }
  const RO = (win as unknown as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver ?? (typeof ResizeObserver !== 'undefined' ? ResizeObserver : undefined)
  if (RO && doc.documentElement) {
    const ro = new RO(once)
    ro.observe(doc.documentElement)
    if (doc.body) ro.observe(doc.body)
    off.push(() => ro.disconnect())
  }
  const fonts = (doc as Document & { fonts?: EventTarget }).fonts
  if (fonts && typeof fonts.addEventListener === 'function') on(fonts, 'loadingdone', once, { passive: true })
  return () => off.forEach((f) => f())
}

/**
 * A measure scheduler: `kick()` measures once on the next animation frame; transitions and
 * animations keep it measuring every frame for a moment (`busy`), so an outline follows an element
 * a CSS animation or a script moves.
 */
export function measureLoop(measure: () => void, raf: (f: () => void) => number = (f) => requestAnimationFrame(f), caf: (id: number) => void = (id) => cancelAnimationFrame(id), now: () => number = () => performance.now()) {
  let id = 0
  let until = 0
  const tick = () => {
    id = 0
    measure()
    if (now() < until) id = raf(tick)
  }
  return {
    kick(busyMs = 0) {
      if (busyMs) until = Math.max(until, now() + busyMs)
      if (!id) id = raf(tick)
    },
    stop() {
      if (id) caf(id)
      id = 0
      until = 0
    },
  }
}
