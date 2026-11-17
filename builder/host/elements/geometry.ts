/**
 * Canvas geometry of the element editor: boxes of frame elements in the canvas's coordinates
 * (the frame is scaled by the fit-to-panel zoom) and the drop target under the pointer while an
 * element is dragged.
 */
import { contains, elementKids, wbc } from './html'
import { childIds, container, dropError, insertError, locate, placeBy, tree, whereIs, type Page, type Place } from './ops'

export interface Box {
  x: number
  y: number
  w: number
  h: number
}

/** A frame element's box in the coordinates of `area` (both in the editor document). */
export function boxIn(frame: HTMLIFrameElement, area: HTMLElement, zoom: number, r: { left: number; top: number; width: number; height: number }): Box {
  const f = frame.getBoundingClientRect()
  const a = area.getBoundingClientRect()
  return { x: f.left - a.left + r.left * zoom, y: f.top - a.top + r.top * zoom, w: r.width * zoom, h: r.height * zoom }
}

/** Union of rectangles (empty ones skipped). */
export function union(rects: DOMRect[]): DOMRect | null {
  let l = Infinity
  let t = Infinity
  let r = -Infinity
  let b = -Infinity
  for (const x of rects) {
    if (!x.width && !x.height) continue
    l = Math.min(l, x.left)
    t = Math.min(t, x.top)
    r = Math.max(r, x.right)
    b = Math.max(b, x.bottom)
  }
  return l === Infinity ? null : new DOMRect(l, t, r - l, b - t)
}

export const isLocked = (el: Element) => (el.getAttribute('data-wbc') ?? '').includes(':')

/** The page's element with id in the frame (not a header / footer copy). */
export const frameEl = (doc: Document, id: string) => doc.querySelector<HTMLElement>(`[data-wbc="${CSS.escape(id)}"]`)

export type Axis = 'x' | 'y'

/** The direction a container lays its children out in. */
export function axisOf(el: Element | null, kids: Element[]): Axis {
  if (el) {
    const cs = el.ownerDocument.defaultView?.getComputedStyle(el)
    if (cs && (cs.display === 'flex' || cs.display === 'inline-flex')) return cs.flexDirection.startsWith('column') ? 'y' : 'x'
  }
  const vis = kids.map((k) => k.getBoundingClientRect()).filter((r) => r.width || r.height)
  if (vis.length >= 2 && Math.abs(vis[0].top - vis[1].top) < 2 && vis[1].left > vis[0].left) return 'x'
  return 'y'
}

export interface Drop {
  place: Place
  /** Indicator in frame viewport coordinates. */
  line: { left: number; top: number; width: number; height: number }
  /** The indicator is a container's box (dropping into an empty container). */
  inside: boolean
}

const lineAt = (r: DOMRect, axis: Axis, after: boolean) =>
  axis === 'y' ? { left: r.left, top: (after ? r.bottom : r.top) - 1.5, width: r.width, height: 3 } : { left: (after ? r.right : r.left) - 1.5, top: r.top, width: 3, height: r.height }

/**
 * Where element `moving` would go when dropped at frame point (x, y); null when nowhere (over a
 * locked header / footer, or no valid container around the point).
 */
export function dropAt(doc: Document, page: Page, x: number, y: number, moving: string | null, zoom: number): Drop | null {
  const edge = 8 / Math.max(zoom, 0.1)
  // moving null: a new element from the palette (any container takes it).
  const mv = moving === null ? null : locate(page, moving)
  if (moving !== null && !mv) return null
  const refused = (place: Place) => (moving === null ? insertError(page, place) : dropError(page, moving, place))
  let target = doc.elementFromPoint(x, y)?.closest<HTMLElement>('[data-wbc]') ?? null
  if (target && isLocked(target)) return null
  let forceEdge = false
  if (!target) {
    // Between sections (a wrapper's padding): next to the nearest top-level element.
    let best: { el: HTMLElement; d: number } | null = null
    for (const sec of page.order) {
      for (const k of elementKids(tree(page.html[sec]).root)) {
        const id = wbc(k)
        const fe = id ? frameEl(doc, id) : null
        if (!fe) continue
        const r = fe.getBoundingClientRect()
        if (!r.width && !r.height) continue
        const d = y < r.top ? r.top - y : y > r.bottom ? y - r.bottom : 0
        if (!best || d < best.d) best = { el: fe, d }
      }
    }
    if (!best) return null
    target = best.el
    forceEdge = true
  }
  for (let guard = 0; target && guard < 40; guard++) {
    const id = target.getAttribute('data-wbc')!
    const next: HTMLElement | null = target.parentElement?.closest<HTMLElement>('[data-wbc]') ?? null
    const at = locate(page, id)
    if (!at || (mv && at.sec === mv.sec && contains(mv.el, at.el))) {
      target = next
      continue
    }
    const r = target.getBoundingClientRect()
    const w = whereIs(page, id)
    const parentEl = w?.parent ? frameEl(doc, w.parent) : target.parentElement
    const siblings = w ? (w.parent ? childIds(tree(page.html[w.sec]), tree(page.html[w.sec]).byId.get(w.parent)!) : childIds(tree(page.html[w.sec]), tree(page.html[w.sec]).root)) : []
    const pAxis = axisOf(parentEl, siblings.slice(0, 2).map((s) => frameEl(doc, s)).filter((e): e is HTMLElement => !!e))
    const nearEdge = forceEdge || (pAxis === 'y' ? y - r.top < edge || r.bottom - y < edge : x - r.left < edge || r.right - x < edge)
    if (!nearEdge && container(at.tree, at.el)) {
      const kids = childIds(at.tree, at.el)
        .filter((k) => k !== moving)
        .map((k) => ({ id: k, el: frameEl(doc, k) }))
        .filter((k): k is { id: string; el: HTMLElement } => !!k.el)
        .map((k) => ({ ...k, r: k.el.getBoundingClientRect() }))
        .filter((k) => k.r.width || k.r.height)
      let drop: Drop | null = null
      if (!kids.length) {
        const place = placeBy(page, at.sec, id, null, true, moving)
        if (place) drop = { place, line: { left: r.left, top: r.top, width: r.width, height: r.height }, inside: true }
      } else {
        const axis = axisOf(target, kids.map((k) => k.el))
        let near = kids[0]
        let nd = Infinity
        for (const k of kids) {
          const dx = x < k.r.left ? k.r.left - x : x > k.r.right ? x - k.r.right : 0
          const dy = y < k.r.top ? k.r.top - y : y > k.r.bottom ? y - k.r.bottom : 0
          const d = dx * dx + dy * dy
          if (d < nd) {
            nd = d
            near = k
          }
        }
        const after = axis === 'x' ? x > near.r.left + near.r.width / 2 : y > near.r.top + near.r.height / 2
        const place = placeBy(page, at.sec, id, near.id, after, moving)
        if (place) drop = { place, line: lineAt(near.r, axis, after), inside: false }
      }
      if (drop && !refused(drop.place)) return drop
    }
    if (w) {
      const after = pAxis === 'x' ? x > r.left + r.width / 2 : y > r.top + r.height / 2
      const place = id === moving ? null : placeBy(page, w.sec, w.parent, id, after, moving)
      if (place && !refused(place)) return { place, line: lineAt(r, pAxis, after), inside: false }
    }
    forceEdge = false
    target = next
  }
  return null
}
