/**
 * The element editor's canvas (plan §4.4, K5–K8): one frame with the whole page — the header
 * template, the page's code sections and the footer template — at the device's width (desktop is
 * a fixed 1440 px wide page scaled to fit the panel), and every editing control drawn outside the
 * frame, over it: hover and selection outlines, the element's type, the toolbar, the breadcrumb,
 * the drop indicator, the context menu and the text toolbar.
 *
 * Edit mode is static (the design's scripts do not run, so what is on screen is the stored markup
 * and operations apply at once without reloading): the frame is sandboxed WITHOUT allow-scripts
 * (nothing in it can run, whatever the markup holds; the editor works on its DOM from outside) and
 * its copy of the markup is neutralised by the browser's parser (neutral.ts). "Live edit" runs the
 * scripts (a structural change reloads the frame); Preview runs them without editing controls.
 * Both run the design with the author's admin access (same origin as the editor), so they need
 * the author's confirmation once per editor session (ScriptsConsent); until then Preview shows
 * the page without its scripts.
 */
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, Bold, ChevronRight, Clipboard, ClipboardPaste, Copy, CornerLeftUp, Eraser, Eye, EyeOff, GripVertical, Italic, Layers, Link2, Paintbrush, Palette as PaletteIcon, Pencil, Plus, RemoveFormatting, SquareDashed, Tags, Trash2, Ungroup, Zap } from 'lucide-react'
import { useB, type BuilderApi } from '@/builder/store'
import { openInspectorTab } from '@/builder/StudioPanel'
import type { Section } from '@/builder/types'
import type { CmsHost } from '../cms-host'
import type { CodeParts } from '../contract'
import { t } from '../i18n'
import { act, commit, latest, pageOf, selectEl } from './actions'
import { FrameSync, frameDoc, type FrameMode } from './frame'
import { boxIn, dropAt, frameEl, isLocked, union, type Box, type Drop } from './geometry'
import { attr, ownText } from './html'
import { KIND_ICON, kindLabel, kindOf, namesOf, tagName } from './kinds'
import { childIds, locate, movable, parentId, tree, type Page } from './ops'
import { format, mapSource, startTextEdit, writeBack, type TextEdit } from './richtext'
import { sweep } from './neutral'
import { bumpFrame, clipboard, getElState, setElState, toggleIn, useElState } from './state'
import { localFonts, sectionsCSS, type LocalSection } from './local'
import { fontUrlFor } from '@/builder/fonts'
import { style as styleAct } from './actions'
import { kitsCSS, kitsOf, loadKits, syncKits, useKitStore } from './kitstyle'
import { Palette, type NewElement } from './PaletteDrawer'

const WIDTH = { desktop: 1440, tablet: 768, mobile: 390 } as const
const BAR = 34

const cmsHost = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

function partsOf(p: CodeParts | null | undefined) {
  return { header: p?.header?.sections ?? [], footer: p?.footer?.sections ?? [] }
}

/** The editor-only stylesheet of the frame. */
function editCss(mode: FrameMode, hidden: ReadonlySet<string>, shown: ReadonlySet<string>, dragging: string | null): string {
  if (mode === 'preview') return ''
  const q = (id: string) => `[data-wbc="${CSS.escape(id)}"]`
  let css = '[data-wbc],a[href]{cursor:default}[data-wbc][contenteditable]{cursor:text;outline:none}'
  // An empty container (a new Section, Flexbox, Grid or Div) has a size to see and drop into.
  css += '[data-wbc]:empty:not(img,input,hr,br,iframe,video,audio,svg,textarea,select,canvas,embed,object,source,track,wbr,script,style,template){min-height:48px;outline:1px dashed rgba(34,113,177,.55);outline-offset:-1px}'
  for (const id of shown) css += `${q(id)}{display:revert!important;visibility:visible!important;opacity:1!important}`
  for (const id of hidden) css += `${q(id)}{display:none!important}`
  if (dragging) css += `${q(dragging)}{opacity:.35!important;pointer-events:none!important}`
  return css
}

/** Elements edited as text in place: text of their own and no code inside. */
function textEditable(page: Page, id: string): boolean {
  const at = locate(page, id)
  if (!at || !movable(at.el)) return false
  const k = kindOf(at.tree, at.el)
  if (!(ownText(at.tree, at.el) || k === 'heading' || k === 'text' || ((k === 'link' || k === 'button') && !at.el.children.some((c) => c.type === 'el' && !['span', 'b', 'strong', 'em', 'i', 'br', 'svg'].includes(c.tag)))))
    return false
  return !at.tree.src.slice(at.el.openEnd, at.el.closeStart).match(/<(script|style|template|textarea)\b/i)
}

interface Menu {
  x: number
  y: number
  id: string
}

/** The frame's local stylesheet: header, page and footer local styles, the edited state forced. */
function localCssOf(secs: Section[], parts: { header: Section[]; footer: Section[] }, kits: Record<string, { tablet: number; mobile: number }> | undefined, force: { id: string; st: '' | 'hover' | 'focus' | 'focus-visible' | 'active' } | null): string {
  const f = force && force.st ? { force } : {}
  return sectionsCSS(parts.header as LocalSection[], kits, 'h:') + sectionsCSS(secs as LocalSection[], kits, '', f) + sectionsCSS(parts.footer as LocalSection[], kits, 'f:')
}

export function ElementsCanvas() {
  const b = useB()
  latest.doc = b.doc
  const st = useElState()
  const host = cmsHost()
  const parts = useMemo(() => partsOf(host?.boot.codeParts), [host])
  const kits = host?.boot.kitBreakpoints
  const mode: FrameMode = b.preview ? 'preview' : st.live && st.scripts ? 'live' : 'static'
  /** The frame's document: the design's scripts run only once the author confirmed it (else static, inert). */
  const fmode: FrameMode = mode !== 'static' && st.scripts ? mode : 'static'
  const secs = useMemo(() => b.doc.sections.filter((s) => s.kind === 'code' && !s.hidden), [b.doc.sections])
  const page = useMemo(() => pageOf(b.doc), [b.doc])
  const htmlNow = useMemo(() => Object.fromEntries(secs.map((s) => [s.id, s.content.html ?? ''])), [secs])
  const structKey = mode + '\u0003' + fmode + '\u0002' + secs.map((s, i) => [s.id, s.content.head, s.content.wrap, i === 0 ? `${s.content.lang}\u0000${s.content.body}\u0000${s.content.htmlAttrs}\u0000${s.content.bodyAttrs}` : ''].join('\u0000')).join('\u0001')

  const area = useRef<HTMLDivElement>(null)
  const frame = useRef<HTMLIFrameElement>(null)
  const [size, setSize] = useState({ w: 1200, h: 800 })
  const width = WIDTH[b.device]
  const zoom = Math.min(1, Math.max(0.2, (size.w - (b.device === 'desktop' ? 0 : 48)) / width))
  const stageH = Math.max(200, size.h - BAR - (b.device === 'desktop' ? 0 : 32))

  // ---------- frame document ----------
  const base = typeof location !== 'undefined' ? location.origin + '/' : '/'
  const force = mode !== 'preview' && st.sel && st.styleState ? { id: st.sel, st: st.styleState } : null
  const localCss = useMemo(() => localCssOf(secs, parts, kits, force), [secs, parts, kits, force?.id, force?.st]) // eslint-disable-line react-hooks/exhaustive-deps
  const localRef = useRef(localCss)
  localRef.current = localCss
  // Class and variable edits (the kits' override stylesheets, kitstyle.ts).
  const kitStore = useKitStore()
  const kitList = useMemo(() => kitsOf(b.doc, parts as never).join(','), [b.doc, parts])
  useEffect(() => {
    void loadKits(kitList ? kitList.split(',') : [])
  }, [kitList])
  const kitCss = useMemo(() => kitsCSS(b.doc), [b.doc, kitStore.kits]) // eslint-disable-line react-hooks/exhaustive-deps
  const kitRef = useRef(kitCss)
  kitRef.current = kitCss
  useEffect(() => syncKits(b.doc, (m) => b.notify(m)), [b.doc, kitStore.kits]) // eslint-disable-line react-hooks/exhaustive-deps
  // The document and whether the design's scripts may run in it change together: the sandbox the
  // frame gets is always the one its document was made for (a new frame element when it changes).
  const [frameSrc, setFrameSrc] = useState(() => ({ html: frameDoc(secs, parts, fmode, editCss(mode, st.hidden, st.shown, null), base, localCss, kitCss), scripts: fmode !== 'static' }))
  const shown = useRef<Record<string, string>>(htmlNow)
  const shownKey = useRef(structKey)
  const sync = useRef<FrameSync | null>(null)
  const keepY = useRef(0)
  const [loaded, setLoaded] = useState(0)
  /** Markup an in-frame text edit (live mode) already shows: no reload for it. */
  const expect = useRef<Record<string, string>>({})

  const fdoc = () => frame.current?.contentDocument ?? null
  const fwin = () => frame.current?.contentWindow ?? null

  const rebuild = useCallback(() => {
    keepY.current = fwin()?.scrollY ?? 0
    sync.current = null
    shown.current = htmlNow
    shownKey.current = structKey
    const s = getElState()
    setFrameSrc({ html: frameDoc(secs, parts, fmode, editCss(mode, s.hidden, s.shown, null), base, localRef.current, kitRef.current), scripts: fmode !== 'static' })
  }, [htmlNow, structKey, secs, parts, mode, fmode, base])

  useLayoutEffect(() => {
    if (structKey !== shownKey.current) return rebuild()
    const changed = Object.keys(htmlNow).some((k) => htmlNow[k] !== shown.current[k])
    if (!changed) return
    if (fmode === 'static' && sync.current) {
      if (sync.current.update(shown.current, htmlNow)) {
        shown.current = htmlNow
        refresh()
        bumpFrame()
        return
      }
      return rebuild()
    }
    if (Object.keys(htmlNow).every((k) => htmlNow[k] === shown.current[k] || htmlNow[k] === expect.current[k])) {
      shown.current = htmlNow
      return
    }
    rebuild()
  }, [htmlNow, structKey]) // eslint-disable-line react-hooks/exhaustive-deps

  // The frame's width changed: the Style panel reads the cascade again.
  useEffect(() => {
    const id = requestAnimationFrame(() => bumpFrame())
    return () => cancelAnimationFrame(id)
  }, [b.device, zoom])

  // Local styles: the editor-owned stylesheet is rewritten on every change (no reload).
  useEffect(() => {
    const el = fdoc()?.querySelector('style[data-wb-local]')
    if (el && el.textContent !== localCss) {
      el.textContent = localCss
      bumpFrame()
    }
    // Library fonts the local styles name (the published page links the same ones).
    const d = fdoc()
    if (d?.head) {
      for (const fam of localFonts([...parts.header, ...secs, ...parts.footer] as LocalSection[])) {
        const href = fontUrlFor(fam, [400, 500, 600, 700])
        if (!href || d.head.querySelector(`link[data-wb-font="${CSS.escape(fam)}"]`)) continue
        const l = d.createElement('link')
        l.rel = 'stylesheet'
        l.href = href
        l.setAttribute('data-wb-font', fam)
        d.head.appendChild(l)
      }
    }
  }, [localCss, loaded])

  // The kits' overrides: rewritten on every change (no reload).
  useEffect(() => {
    const el = fdoc()?.querySelector('style[data-wb-kit]')
    if (el && el.textContent !== kitCss) {
      el.textContent = kitCss
      bumpFrame()
    }
  }, [kitCss, loaded])

  // Editor stylesheet: hidden, temporarily shown, dragged element.
  const [dragId, setDragId] = useState<string | null>(null)
  useEffect(() => {
    const el = fdoc()?.querySelector('style[data-wb-edit]')
    if (el) el.textContent = editCss(mode, st.hidden, st.shown, dragId)
  }, [st.hidden, st.shown, dragId, mode, loaded])

  // ---------- size ----------
  useLayoutEffect(() => {
    const el = area.current
    if (!el) return
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    setSize({ w: el.clientWidth, h: el.clientHeight })
    return () => ro.disconnect()
  }, [])

  // ---------- overlay ----------
  const hoverRef = useRef<HTMLDivElement>(null)
  const selRef = useRef<HTMLDivElement>(null)
  const toolRef = useRef<HTMLDivElement>(null)
  const partRef = useRef<HTMLDivElement>(null)
  const dropRef = useRef<HTMLDivElement>(null)
  const textBarRef = useRef<HTMLDivElement>(null)
  const [partHover, setPartHover] = useState<'header' | 'footer' | null>(null)
  const [menu, setMenu] = useState<Menu | null>(null)
  const [editing, setEditing] = useState<TextEdit | null>(null)
  const [hiddenNow, setHiddenNow] = useState(false)
  /** The confirmation asked before the design's scripts run (for Live edit or for Preview). */
  const [ask, setAsk] = useState<null | 'live' | 'preview'>(null)

  const cur = useRef({ b, page, mode, zoom, st, editing })
  cur.current = { b, page, mode, zoom, st, editing }

  const box = (r: { left: number; top: number; width: number; height: number }): Box | null => (frame.current && area.current ? boxIn(frame.current, area.current, cur.current.zoom, r) : null)
  const place = (el: HTMLDivElement | null, bx: Box | null) => {
    if (!el) return
    if (!bx) {
      el.style.display = 'none'
      return
    }
    el.style.display = ''
    el.style.transform = `translate(${bx.x}px, ${bx.y}px)`
    el.style.width = `${Math.max(0, bx.w)}px`
    el.style.height = `${Math.max(0, bx.h)}px`
  }

  /** The selected element's (or section's) box, the toolbar next to it. */
  const refresh = useCallback(() => {
    const d = fdoc()
    const { st, b, page } = cur.current
    let r: DOMRect | null = null
    if (d && st.sel) r = frameEl(d, st.sel)?.getBoundingClientRect() ?? null
    else if (d && b.selection && !b.selection.el && page.html[b.selection.sectionId] !== undefined) {
      const t = tree(page.html[b.selection.sectionId])
      r = union(childIds(t, t.root).map((id) => frameEl(d, id)?.getBoundingClientRect()).filter((x): x is DOMRect => !!x))
    }
    const bx = r ? box(r) : null
    place(selRef.current, bx)
    const tool = toolRef.current
    if (tool) {
      if (!bx || cur.current.mode === 'preview') tool.style.display = 'none'
      else {
        tool.style.display = ''
        const w = tool.offsetWidth
        const h = tool.offsetHeight
        const aw = area.current?.clientWidth ?? 0
        const x = Math.max(4, Math.min(bx.x + bx.w - w, aw - w - 4))
        let y = bx.y - h - 6
        if (y < 4) y = Math.min(bx.y + bx.h + 6, stageHRef.current - h - 4)
        if (y < 4) y = 4
        tool.style.transform = `translate(${x}px, ${y}px)`
      }
    }
    const tb = textBarRef.current
    const ed = cur.current.editing
    if (tb && ed) {
      const er = box(ed.el.getBoundingClientRect())
      if (er) tb.style.transform = `translate(${Math.max(4, er.x)}px, ${Math.max(4, er.y - tb.offsetHeight - 6)}px)`
    }
    setHiddenNow(!!(st.sel && r && !r.width && !r.height))
  }, [])
  const stageHRef = useRef(stageH)
  stageHRef.current = stageH

  // Follow layout changes (images loading, fonts, scrolling) while something is selected.
  useEffect(() => {
    if (!st.sel && !(b.selection && !b.selection.el)) {
      place(selRef.current, null)
      if (toolRef.current) toolRef.current.style.display = 'none'
      return
    }
    let raf = 0
    const loop = () => {
      refresh()
      raf = requestAnimationFrame(loop)
    }
    raf = requestAnimationFrame(loop)
    return () => cancelAnimationFrame(raf)
  }, [st.sel, b.selection, refresh, loaded, zoom, b.device])

  // Hover from the navigator.
  useEffect(() => {
    const d = fdoc()
    const id = st.hover
    const el = d && id ? frameEl(d, id) : null
    place(hoverRef.current, el ? box(el.getBoundingClientRect()) : null)
  }, [st.hover]) // eslint-disable-line react-hooks/exhaustive-deps

  // Keep the element selection inside the builder's selected section.
  useEffect(() => {
    const sel = getElState().sel
    if (!sel) return
    const at = locate(page, sel)
    if (!at || !b.selection || b.selection.sectionId !== at.sec) setElState({ sel: null })
  }, [b.selection, page])

  // The section list asks to scroll to a section.
  useEffect(() => {
    const d = fdoc()
    const w = fwin()
    if (!b.scrollReq || !d || !w || page.html[b.scrollReq.id] === undefined) return
    const t = tree(page.html[b.scrollReq.id])
    const first = childIds(t, t.root).map((id) => frameEl(d, id)).find(Boolean)
    if (first) w.scrollTo({ top: Math.max(0, first.getBoundingClientRect().top + w.scrollY - 80), behavior: 'smooth' })
  }, [b.scrollReq]) // eslint-disable-line react-hooks/exhaustive-deps

  // ---------- text editing ----------
  const beginText = useCallback((id: string, point?: { x: number; y: number }) => {
    const d = fdoc()
    const { page, mode } = cur.current
    if (!d || mode === 'preview' || !textEditable(page, id)) return false
    const el = frameEl(d, id)
    if (!el) return false
    const at = locate(page, id)!
    // Text a script drew differently from the source (live edit): plain text only.
    const norm = (v: string) => v.replace(/\s+/g, ' ').trim()
    const drawn = mode === 'live' && norm(at.tree.src.slice(at.el.openEnd, at.el.closeStart)) !== norm(el.innerHTML)
    setMenu(null)
    selectEl(cur.current.b, id, page)
    // The new text is written from the source (the frame's copy lacks handlers and more).
    const map = mapSource(el, at.tree, at.el)
    const session = startTextEdit(el, drawn, point ?? null, (e) => writeBack(e, at.tree, map), (html, text) => {
      setEditing(null)
      setElState({ editing: null })
      const { b, page } = cur.current
      if (html === null) return
      if (drawn) {
        const a = locate(page, id)
        if (a && !a.el.children.some((c) => c.type === 'el')) {
          const esc = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
          const next = { ...page.html, [a.sec]: a.tree.src.slice(0, a.el.openEnd) + esc + a.tree.src.slice(a.el.closeStart) }
          expect.current[a.sec] = next[a.sec]
          act.setInner(b, id, esc)
        } else b.notify(t('This text is drawn by the design’s script: edit it in the section’s HTML code.'))
        return
      }
      const a = locate(page, id)
      if (a && cur.current.mode === 'live') expect.current[a.sec] = a.tree.src.slice(0, a.el.openEnd) + html + a.tree.src.slice(a.el.closeStart)
      act.setInner(b, id, html)
    })
    setEditing(session)
    setElState({ editing: id, sel: id })
    return true
  }, [])

  // ---------- frame events ----------
  /** A drag: an element (id) or a new one from the palette (add). */
  const drag = useRef<{ id: string | null; add?: NewElement; x0: number; y0: number; on: boolean; drop: Drop | null; lastX: number; lastY: number } | null>(null)

  const dragged = useRef(0)
  const finishDrag = useCallback((commitIt: boolean) => {
    const g = drag.current
    drag.current = null
    setDragId(null)
    place(dropRef.current, null)
    if (!g?.on) return
    dragged.current = Date.now()
    if (commitIt && g.drop && g.add) act.insertAt(cur.current.b, g.drop.place, g.add.html, g.add.local, g.add.label)
    else if (commitIt && g.drop && g.id) act.moveTo(cur.current.b, g.id, g.drop.place)
  }, [])

  /** Drag step at frame point (x, y). */
  const dragTo = useCallback((x: number, y: number) => {
    const g = drag.current
    const d = fdoc()
    if (!g || !d) return
    g.lastX = x
    g.lastY = y
    if (!g.on) {
      if (Math.hypot(x - g.x0, y - g.y0) * cur.current.zoom < 6) return
      g.on = true
      setDragId(g.id ?? '')
      setMenu(null)
    }
    const w = d.defaultView
    const inside = !!w && x >= 0 && y >= 0 && x <= w.innerWidth && y <= w.innerHeight
    g.drop = inside ? dropAt(d, cur.current.page, x, y, g.id, cur.current.zoom) : null
    const dr = dropRef.current
    if (dr) {
      place(dr, g.drop ? box(g.drop.line) : null)
      dr.dataset.inside = g.drop?.inside ? '1' : ''
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-scroll while dragging near the frame's top or bottom edge.
  useEffect(() => {
    if (!dragId) return
    let raf = 0
    const loop = () => {
      const g = drag.current
      const w = fwin()
      if (g?.on && w) {
        const edge = 48 / cur.current.zoom
        const v = g.lastY < edge ? -(edge - g.lastY) : g.lastY > w.innerHeight - edge ? g.lastY - (w.innerHeight - edge) : 0
        if (v) {
          w.scrollBy(0, v * 0.35)
          dragTo(g.lastX, g.lastY)
        }
      }
      raf = requestAnimationFrame(loop)
    }
    raf = requestAnimationFrame(loop)
    return () => cancelAnimationFrame(raf)
  }, [dragId, dragTo])

  const partOf = (el: Element | null): 'header' | 'footer' | null => {
    const v = el?.closest('[data-wbc]')?.getAttribute('data-wbc') ?? ''
    return v.startsWith('h:') ? 'header' : v.startsWith('f:') ? 'footer' : null
  }

  const showPart = (which: 'header' | 'footer' | null) => {
    const d = fdoc()
    setPartHover(which)
    if (!d || !which) return place(partRef.current, null)
    const p = which === 'header' ? 'h:' : 'f:'
    const tops = Array.from(d.querySelectorAll(`[data-wbc^="${p}"]`)).filter((e) => !e.parentElement?.closest(`[data-wbc^="${p}"]`))
    const r = union(tops.map((e) => e.getBoundingClientRect()))
    place(partRef.current, r ? box(r) : null)
  }

  const onLoad = () => {
    const d = fdoc()
    const w = fwin()
    if (!d || !w) return
    const inert = !frame.current?.sandbox.contains('allow-scripts')
    // The static frame's DOM is swept once more (whatever a second parse made of the copy is
    // inert too); nothing in it could run in between: no allow-scripts.
    if (inert) sweep(d)
    const s = new FrameSync(d)
    s.collect()
    sync.current = inert ? s : null
    if (keepY.current) w.scrollTo(0, keepY.current)
    setLoaded((n) => n + 1)
    bumpFrame(d)
    const hover = hoverRef.current
    let raf = 0
    let lastTarget: Element | null = null
    const hoverAt = () => {
      raf = 0
      const { mode, st } = cur.current
      if (mode === 'preview' || drag.current?.on || st.editing) return place(hover, null)
      const el = lastTarget
      const part = partOf(el)
      if (part !== partHoverRef.current) showPart(part)
      if (part) return place(hover, null)
      const target = el?.closest<HTMLElement>('[data-wbc]')
      if (!target || target.getAttribute('data-wbc') === st.sel) return place(hover, null)
      place(hover, box(target.getBoundingClientRect()))
      if (hover) hover.dataset.label = labelFor(target.getAttribute('data-wbc')!)
    }
    d.addEventListener('pointermove', (e) => {
      if (drag.current) {
        if (e.buttons === 0) return finishDrag(true)
        dragTo(e.clientX, e.clientY)
        return
      }
      lastTarget = e.target as Element
      if (!raf) raf = requestAnimationFrame(hoverAt)
    })
    d.addEventListener('pointerleave', () => {
      lastTarget = null
      place(hover, null)
    })
    w.addEventListener('scroll', () => {
      place(hover, null)
      if (partHoverRef.current) showPart(partHoverRef.current)
    }, { passive: true })
    d.addEventListener(
      'pointerdown',
      (e) => {
        const { mode, st } = cur.current
        if (mode === 'preview' || e.button !== 0) return
        const tg = e.target as Element
        if (st.editing && tg.closest('[contenteditable]')) return
        if (partOf(tg)) return
        const el = tg.closest<HTMLElement>('[data-wbc]')
        if (!el) return
        e.preventDefault() // no text selection or native drag while picking / dragging elements
        // Pressing inside the selected element drags the selection; elsewhere the element under the pointer.
        const selEl = st.sel ? frameEl(d, st.sel) : null
        const id = selEl && selEl.contains(el) ? st.sel! : el.getAttribute('data-wbc')!
        const at = locate(cur.current.page, id)
        if (at && movable(at.el)) drag.current = { id, x0: e.clientX, y0: e.clientY, on: false, drop: null, lastX: e.clientX, lastY: e.clientY }
      },
      true,
    )
    d.addEventListener('pointerup', () => {
      if (drag.current?.on) finishDrag(true)
      else drag.current = null
    }, true)
    d.addEventListener('dragstart', (e) => cur.current.mode !== 'preview' && e.preventDefault(), true)
    d.addEventListener(
      'click',
      (e) => {
        const tg = e.target as Element
        const a = tg.closest('a[href]')
        const { mode, st, b } = cur.current
        if (mode === 'preview') {
          if (a && !(a.getAttribute('href') ?? '').startsWith('#')) e.preventDefault()
          return
        }
        if (st.editing && tg.closest('[contenteditable]')) return
        e.preventDefault()
        e.stopPropagation()
        setMenu(null)
        if (Date.now() - dragged.current < 300) return // the click that ends a drag
        if (partOf(tg)) return
        const el = tg.closest<HTMLElement>('[data-wbc]')
        if (!el) {
          selectEl(b, null)
          b.select(null)
          return
        }
        const id = el.getAttribute('data-wbc')!
        if (st.sel === id && textEditable(cur.current.page, id)) {
          beginText(id)
          return
        }
        selectEl(b, id, cur.current.page)
      },
      true,
    )
    d.addEventListener(
      'dblclick',
      (e) => {
        if (cur.current.mode === 'preview') return
        const el = (e.target as Element).closest<HTMLElement>('[data-wbc]')
        if (!el || isLocked(el) || cur.current.st.editing) return
        e.preventDefault()
        beginText(el.getAttribute('data-wbc')!, { x: e.clientX, y: e.clientY })
      },
      true,
    )
    d.addEventListener(
      'contextmenu',
      (e) => {
        if (cur.current.mode === 'preview') return
        const el = (e.target as Element).closest<HTMLElement>('[data-wbc]')
        if (!el || isLocked(el) || cur.current.st.editing) return
        e.preventDefault()
        // On the selected element (or inside it) the menu is the selection's.
        const sel = cur.current.st.sel
        const selEl = sel ? frameEl(d, sel) : null
        const id = selEl && selEl.contains(el) ? sel! : el.getAttribute('data-wbc')!
        selectEl(cur.current.b, id, cur.current.page)
        const bx = box({ left: e.clientX, top: e.clientY, width: 0, height: 0 })
        if (bx) setMenu({ x: bx.x, y: bx.y, id })
      },
      true,
    )
    d.addEventListener('submit', (e) => e.preventDefault(), true)
    // Keys inside the frame reach the editor's shortcuts (undo, delete, arrows…), not while typing.
    d.addEventListener(
      'keydown',
      (e) => {
        const tg = e.target as HTMLElement
        if (tg.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(tg.tagName)) return
        const fwd = new KeyboardEvent('keydown', { key: e.key, code: e.code, metaKey: e.metaKey, ctrlKey: e.ctrlKey, shiftKey: e.shiftKey, altKey: e.altKey, repeat: e.repeat, bubbles: true, cancelable: true })
        document.body.dispatchEvent(fwd)
        if (fwd.defaultPrevented) e.preventDefault()
      },
      true,
    )
    refresh()
  }
  const partHoverRef = useRef(partHover)
  partHoverRef.current = partHover

  // Pointer moves over the editor while dragging (the pointer left the frame).
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const g = drag.current
      const f = frame.current
      if (!g || !f) return
      if (e.buttons === 0) return finishDrag(true)
      // Over the palette (it covers the frame): no drop.
      if ((e.target as Element | null)?.closest?.('[data-wb-palette]')) {
        g.drop = null
        place(dropRef.current, null)
        return
      }
      const r = f.getBoundingClientRect()
      dragTo((e.clientX - r.left) / cur.current.zoom, (e.clientY - r.top) / cur.current.zoom)
    }
    const up = () => drag.current?.on && finishDrag(true)
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && drag.current?.on) {
        e.stopPropagation()
        finishDrag(false)
      }
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('keydown', key, true)
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('keydown', key, true)
    }
  }, [finishDrag, dragTo])

  // ---------- keyboard (the editor's own keys and the frame's, forwarded) ----------
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tg = e.target as HTMLElement
      if (tg && (tg.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(tg.tagName))) return
      const { b, mode, page } = cur.current
      const s = getElState()
      if (mode === 'preview' || s.editing) return
      const id = s.sel
      const mod = e.metaKey || e.ctrlKey
      const k = e.key.toLowerCase()
      const done = () => {
        e.preventDefault()
        e.stopPropagation()
      }
      if (!id) {
        if (mod && k === 'v' && clipboard.html && b.selection && page.html[b.selection.sectionId] !== undefined) {
          // Paste at the end of the selected section.
          const t0 = tree(page.html[b.selection.sectionId])
          const last = childIds(t0, t0.root).pop()
          if (last && act.paste(b, last)) done()
        }
        return
      }
      if (!locate(page, id)) return
      const textSelected = !!window.getSelection()?.toString() || !!fdoc()?.getSelection()?.toString()
      if ((e.key === 'Delete' || e.key === 'Backspace') && !mod) {
        done()
        if (!e.repeat) act.remove(b, id)
      } else if (mod && k === 'd') {
        done()
        act.duplicate(b, id)
      } else if (mod && k === 'c' && !textSelected) {
        done()
        act.copy(b, id)
      } else if (mod && k === 'v' && clipboard.html) {
        done()
        act.paste(b, id, e.shiftKey)
      } else if (e.key === 'Escape') {
        done()
        if (menu) setMenu(null)
        else if (parentId(page, id)) act.parent(b, id)
        else {
          setElState({ sel: null })
          const at = locate(page, id)
          if (at) b.select({ sectionId: at.sec })
        }
      } else if (e.key === 'Enter' && !mod) {
        if (beginText(id)) done()
      } else if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
        done()
        act.step(b, id, e.key === 'ArrowUp' ? -1 : 1)
      } else if (!mod && !e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown' || e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
        done()
        const at = locate(page, id)!
        if (e.key === 'ArrowLeft') return act.parent(b, id)
        if (e.key === 'ArrowRight') {
          const kid = childIds(at.tree, at.el)[0]
          if (kid) selectEl(b, kid, page)
          return
        }
        const sib = childIds(at.tree, at.el.parent!)
        const i = sib.indexOf(id) + (e.key === 'ArrowUp' ? -1 : 1)
        if (sib[i]) selectEl(b, sib[i], page)
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [beginText, menu])

  // Close the context menu on a click elsewhere.
  useEffect(() => {
    if (!menu) return
    const close = () => setMenu(null)
    window.addEventListener('pointerdown', close)
    return () => window.removeEventListener('pointerdown', close)
  }, [menu])

  // Leaving static mode or selecting something else ends a text edit.
  useEffect(() => {
    if (editing && st.sel !== editing.id) editing.end(true)
  }, [st.sel, editing])

  // ---------- labels ----------
  const labelFor = (id: string): string => {
    const at = locate(cur.current.page, id)
    if (!at) return ''
    const fe = fdoc() && frameEl(fdoc()!, id)
    const display = fe ? fe.ownerDocument.defaultView?.getComputedStyle(fe).display : undefined
    return kindLabel(kindOf(at.tree, at.el, display))
  }

  const sel = st.sel ? locate(page, st.sel) : null
  const selKind = useMemo(() => {
    if (!sel) return null
    const fe = fdoc() && frameEl(fdoc()!, st.sel!)
    const display = fe ? fe.ownerDocument.defaultView?.getComputedStyle(fe).display : undefined
    return kindOf(sel.tree, sel.el, display)
  }, [sel, st.sel, loaded]) // eslint-disable-line react-hooks/exhaustive-deps
  const names = useMemo(() => (sel ? namesOf(b.doc.sections.find((s) => s.id === sel.sec)?.content.names) : {}), [sel, b.doc.sections])

  // Breadcrumb: section › … › element.
  const crumbs = useMemo(() => {
    if (!sel) return []
    const out: { id: string | null; label: string }[] = []
    for (let p = sel.el; p && p.tag !== '#root'; p = p.parent!) {
      const id = attr(p, 'data-wbc')
      if (id) out.unshift({ id, label: names[id] || tagName(p) })
    }
    const s = b.doc.sections.find((x) => x.id === sel.sec)
    out.unshift({ id: null, label: s?.name || t('Section') })
    return out
  }, [sel, names, b.doc.sections])

  const bx = (label: string, icon: ReactNode, on: () => void, opts: { danger?: boolean; active?: boolean; disabled?: boolean } = {}) => (
    <button
      type="button"
      title={label}
      aria-label={label}
      aria-pressed={opts.active || undefined}
      disabled={opts.disabled}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation()
        on()
      }}
      className={`grid size-7 place-items-center rounded-md transition-colors disabled:opacity-35 ${opts.danger ? 'hover:bg-red-500/25 hover:text-red-200' : 'hover:bg-white/15'} ${opts.active ? 'bg-white/20' : ''}`}
    >
      {icon}
    </button>
  )

  /** A palette item pressed: dragging it onto the canvas inserts it where it is dropped. */
  const startAddDrag = (e: React.PointerEvent, add: NewElement) => {
    if (!frame.current || b.preview) return
    const r = frame.current.getBoundingClientRect()
    const x = (e.clientX - r.left) / zoom
    const y = (e.clientY - r.top) / zoom
    drag.current = { id: null, add, x0: x, y0: y, on: false, drop: null, lastX: x, lastY: y }
  }

  const startHandleDrag = (e: React.PointerEvent) => {
    if (!st.sel || !frame.current) return
    e.preventDefault()
    e.stopPropagation()
    const r = frame.current.getBoundingClientRect()
    const x = (e.clientX - r.left) / zoom
    const y = (e.clientY - r.top) / zoom
    drag.current = { id: st.sel, x0: x - 100, y0: y - 100, on: false, drop: null, lastX: x, lastY: y }
    dragTo(x, y)
  }

  const toggleShown = (id: string) => setElState((s) => ({ shown: toggleIn(s.shown, id) }))
  const toggleHidden = (id: string) => setElState((s) => ({ hidden: toggleIn(s.hidden, id) }))

  const editUrl = (which: 'header' | 'footer') => host?.boot.codeParts?.[which]?.editUrl ?? ''
  const openPart = async (which: 'header' | 'footer') => {
    const url = editUrl(which)
    if (!url) return
    await host?.beforeLeave?.()
    window.location.href = url
  }

  const framed = b.device !== 'desktop'
  const SelIcon = selKind ? KIND_ICON[selKind] : null
  const canEditText = !!st.sel && textEditable(page, st.sel)

  return (
    <div ref={area} className="relative min-w-0 flex-1 overflow-hidden bg-ink-950" data-wb-elements="" onClick={() => !b.preview && (selectEl(b, null), b.select(null))}>
      <div className={`absolute inset-x-0 top-0 flex justify-center ${framed ? 'dots-bg pt-4' : ''}`} style={{ bottom: BAR }}>
        <div className={framed ? 'overflow-hidden rounded-[14px] shadow-[0_0_0_1px_rgba(255,255,255,.08),0_30px_80px_-20px_rgba(0,0,0,.7)]' : ''} style={{ width: width * zoom, height: stageH }}>
          {/* Static: no allow-scripts (the editor drives the DOM from outside; nothing of the design
              can run). With the author's confirmation only: the design's scripts, same origin. */}
          <iframe
            key={frameSrc.scripts ? 'scripts' : 'inert'}
            ref={frame}
            title={t('Page canvas')}
            srcDoc={frameSrc.html}
            onLoad={onLoad}
            data-wb-frame={mode}
            data-wb-scripts={frameSrc.scripts ? '1' : '0'}
            className="block border-0 bg-white"
            style={{ width, height: stageH / zoom, transform: `scale(${zoom})`, transformOrigin: '0 0' }}
            sandbox={frameSrc.scripts ? 'allow-scripts allow-same-origin allow-forms' : 'allow-same-origin'}
          />
        </div>
      </div>

      {/* overlay: drawn over the frame, never inside it */}
      <div className="pointer-events-none absolute inset-0 z-20 overflow-hidden" style={{ bottom: BAR }} onClick={(e) => e.stopPropagation()}>
        <div ref={hoverRef} data-wb-hover="" className="absolute left-0 top-0 border border-dashed border-[#5b8def] after:absolute after:-top-[18px] after:left-[-1px] after:rounded-t after:bg-[#5b8def] after:px-1.5 after:text-[10.5px] after:leading-[18px] after:text-white after:content-[attr(data-label)]" style={{ display: 'none' }} />
        <div ref={partRef} className="absolute left-0 top-0 border border-dashed border-amber-400/80 bg-amber-300/[0.06]" style={{ display: 'none' }}>
          {partHover && editUrl(partHover) && (
            <button
              type="button"
              data-wb-edit-part={partHover}
              onClick={() => void openPart(partHover)}
              className="pointer-events-auto absolute left-1/2 top-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center gap-1.5 rounded-full bg-amber-400 px-3 py-1.5 text-[12px] font-semibold text-black shadow-lg"
            >
              <Pencil size={13} /> {partHover === 'header' ? t('Edit header') : t('Edit footer')}
            </button>
          )}
        </div>
        <div ref={selRef} data-wb-selected={st.sel ?? ''} className="absolute left-0 top-0 border-2 border-[#2271b1]" style={{ display: 'none' }}>
          {sel && SelIcon && (
            <span className="absolute -top-[22px] left-[-2px] flex h-[22px] max-w-[320px] items-center gap-1 truncate rounded-t-md bg-[#2271b1] px-2 text-[11px] font-medium text-white">
              <SelIcon size={12} /> {kindLabel(selKind!)} · {names[st.sel!] || tagName(sel.el)}
            </span>
          )}
        </div>
        <div ref={dropRef} data-wb-drop="" className="absolute left-0 top-0 rounded-sm bg-[#2271b1] data-[inside='1']:border-2 data-[inside='1']:border-dashed data-[inside='1']:border-[#2271b1] data-[inside='1']:bg-[#2271b1]/10" style={{ display: 'none' }} />
        {sel && !b.preview && !editing && (
          <div ref={toolRef} data-wb-toolbar="" className="pointer-events-auto absolute left-0 top-0 flex items-center gap-0.5 rounded-lg bg-[#1d2327] p-0.5 text-white shadow-xl" style={{ display: 'none' }} onClick={(e) => e.stopPropagation()}>
            <span title={t('Drag to move')} onPointerDown={startHandleDrag} className="grid size-7 cursor-grab place-items-center rounded-md hover:bg-white/15" data-wb-handle="">
              <GripVertical size={14} />
            </span>
            {bx(t('Select parent'), <CornerLeftUp size={14} />, () => act.parent(b, st.sel!))}
            {bx(t('Move up'), <ArrowUp size={14} />, () => act.step(b, st.sel!, -1))}
            {bx(t('Move down'), <ArrowDown size={14} />, () => act.step(b, st.sel!, 1))}
            {canEditText && bx(t('Edit text'), <Pencil size={14} />, () => beginText(st.sel!))}
            {bx(t('Style'), <Paintbrush size={14} />, () => {
              b.setMode('studio')
              openInspectorTab('style')
            })}
            {bx(t('Duplicate'), <Copy size={14} />, () => act.duplicate(b, st.sel!))}
            {bx(t('Wrap in a Div'), <SquareDashed size={14} />, () => act.wrap(b, st.sel!))}
            {(hiddenNow || st.shown.has(st.sel!)) && bx(t('Show temporarily'), st.shown.has(st.sel!) ? <EyeOff size={14} /> : <Eye size={14} />, () => toggleShown(st.sel!), { active: st.shown.has(st.sel!) })}
            {bx(t('Delete'), <Trash2 size={14} />, () => act.remove(b, st.sel!), { danger: true })}
          </div>
        )}
        {editing && (
          <div ref={textBarRef} data-wb-textbar="" className="pointer-events-auto absolute left-0 top-0 flex items-center gap-0.5 rounded-lg bg-[#1d2327] p-0.5 text-white shadow-xl" onPointerDown={(e) => e.preventDefault()} onClick={(e) => e.stopPropagation()}>
            {bx(t('Bold'), <Bold size={14} />, () => format(editing.el.ownerDocument, 'bold'))}
            {bx(t('Italic'), <Italic size={14} />, () => format(editing.el.ownerDocument, 'italic'))}
            {bx(t('Link'), <Link2 size={14} />, () => {
              const url = window.prompt(t('Link address'), 'https://')
              if (url) format(editing.el.ownerDocument, 'link', url)
            })}
            {bx(t('Clear formatting'), <RemoveFormatting size={14} />, () => format(editing.el.ownerDocument, 'clear'))}
          </div>
        )}
        {menu && <ContextMenu b={b} menu={menu} page={page} close={() => setMenu(null)} hidden={st.hidden} shown={st.shown} toggleHidden={toggleHidden} toggleShown={toggleShown} beginText={beginText} />}
      </div>

      {st.palette && !b.preview && <Palette startDrag={startAddDrag} secs={secs} parts={parts} />}

      {b.preview && !st.scripts && (
        <div data-wb-scripts-off="" className="absolute left-1/2 top-3 z-30 flex max-w-[92%] -translate-x-1/2 items-center gap-3 rounded-lg bg-[#1d2327] px-3 py-2 text-[12px] text-ink-200 shadow-xl" onClick={(e) => e.stopPropagation()}>
          <span>{t('The design’s scripts are not running in this preview.')}</span>
          <button type="button" data-wb-scripts-ask="" onClick={() => setAsk('preview')} className="shrink-0 rounded-md bg-amber-400/20 px-2 py-1 text-amber-200 hover:bg-amber-400/30">
            {t('Run them…')}
          </button>
        </div>
      )}
      {ask && (
        <ScriptsConsent
          onCancel={() => setAsk(null)}
          onRun={() => {
            setElState(ask === 'live' ? { scripts: true, live: true, editing: null } : { scripts: true })
            setAsk(null)
          }}
        />
      )}

      {/* bottom bar: breadcrumb, live edit, layers */}
      <div className="absolute inset-x-0 bottom-0 z-30 flex items-center gap-2 border-t border-white/[0.07] bg-ink-900 px-3 text-[11.5px] text-ink-300" style={{ height: BAR }} onClick={(e) => e.stopPropagation()}>
        <nav aria-label={t('Breadcrumb')} className="flex min-w-0 flex-1 items-center gap-0.5 overflow-hidden" data-wb-breadcrumb="">
          {crumbs.map((c, i) => (
            <span key={c.id ?? 'sec'} className="flex min-w-0 items-center gap-0.5">
              {i > 0 && <ChevronRight size={11} className="shrink-0 text-ink-500" />}
              <button
                type="button"
                className={`truncate rounded px-1 py-0.5 hover:bg-white/10 hover:text-white ${i === crumbs.length - 1 ? 'text-white' : ''}`}
                onClick={() => (c.id ? selectEl(b, c.id, page) : (setElState({ sel: null }), b.select({ sectionId: sel!.sec })))}
              >
                {c.label}
              </button>
            </span>
          ))}
          {!crumbs.length && <span className="text-ink-500">{b.preview ? t('Preview') : t('Click an element to select it · double-click a text to edit it')}</span>}
        </nav>
        <span className="shrink-0 text-ink-500">
          {width} px{zoom < 1 ? ` · ${Math.round(zoom * 100)}%` : ''}
        </span>
        {!b.preview && (
          <button
            type="button"
            data-wb-add=""
            aria-pressed={st.palette === 'elements' || st.palette === 'design'}
            title={t('Add elements')}
            onClick={() => setElState((s) => ({ palette: s.palette === 'elements' || s.palette === 'design' ? null : 'elements' }))}
            className={`flex shrink-0 items-center gap-1 rounded-md px-2 py-1 ${st.palette === 'elements' || st.palette === 'design' ? 'bg-accent/25 text-accent-soft' : 'hover:bg-white/10 hover:text-white'}`}
          >
            <Plus size={12} /> {t('Add')}
          </button>
        )}
        {!b.preview && (
          <button
            type="button"
            data-wb-variables=""
            aria-pressed={st.palette === 'variables'}
            title={t('The design’s variables (colors, fonts, sizes)')}
            onClick={() => setElState((s) => ({ palette: s.palette === 'variables' ? null : 'variables' }))}
            className={`flex shrink-0 items-center gap-1 rounded-md px-2 py-1 ${st.palette === 'variables' ? 'bg-accent/25 text-accent-soft' : 'hover:bg-white/10 hover:text-white'}`}
          >
            <PaletteIcon size={12} /> {t('Variables')}
          </button>
        )}
        {!b.preview && (
          <button
            type="button"
            data-wb-live=""
            aria-pressed={st.live}
            title={t('Run the design’s scripts while editing (menus, sliders, animations). Structural changes reload the page.')}
            onClick={() => (st.live || st.scripts ? setElState((s) => ({ live: !s.live, editing: null })) : setAsk('live'))}
            className={`flex shrink-0 items-center gap-1 rounded-md px-2 py-1 ${st.live ? 'bg-amber-400/20 text-amber-200' : 'hover:bg-white/10 hover:text-white'}`}
          >
            <Zap size={12} /> {t('Live edit')}
          </button>
        )}
        {!b.preview && (
          <button
            type="button"
            title={t('Layers')}
            onClick={() => {
              b.setMode('studio')
              b.setStructureOpen(true)
            }}
            className="flex shrink-0 items-center gap-1 rounded-md px-2 py-1 hover:bg-white/10 hover:text-white"
          >
            <Layers size={12} /> {t('Layers')}
          </button>
        )}
      </div>
    </div>
  )
}

function ContextMenu({
  b,
  menu,
  page,
  close,
  hidden,
  shown,
  toggleHidden,
  toggleShown,
  beginText,
}: {
  b: BuilderApi
  menu: Menu
  page: Page
  close: () => void
  hidden: ReadonlySet<string>
  shown: ReadonlySet<string>
  toggleHidden: (id: string) => void
  toggleShown: (id: string) => void
  beginText: (id: string, at?: { x: number; y: number }) => boolean
}) {
  const id = menu.id
  const at = locate(page, id)
  if (!at) return null
  const can = movable(at.el)
  const item = (label: string, icon: ReactNode, on: () => void, opts: { disabled?: boolean; danger?: boolean; kbd?: string } = {}) => (
    <button
      type="button"
      role="menuitem"
      disabled={opts.disabled}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation()
        close()
        on()
      }}
      className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left disabled:opacity-35 ${opts.danger ? 'text-red-300 hover:bg-red-500/20' : 'hover:bg-white/10'}`}
    >
      {icon}
      <span className="flex-1">{label}</span>
      {opts.kbd && <kbd className="text-[10.5px] text-ink-400">{opts.kbd}</kbd>}
    </button>
  )
  const mod = typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.platform) ? '⌘' : 'Ctrl+'
  return (
    <div role="menu" data-wb-menu="" className="pointer-events-auto absolute z-40 w-56 rounded-lg border border-white/10 bg-[#1d2327] p-1 text-[12px] text-white shadow-2xl" style={{ left: menu.x, top: menu.y }} onPointerDown={(e) => e.stopPropagation()}>
      {textEditable(page, id) && item(t('Edit text'), <Pencil size={13} />, () => beginText(id), { kbd: '↵' })}
      {item(t('Copy'), <Clipboard size={13} />, () => act.copy(b, id), { disabled: !can, kbd: `${mod}C` })}
      {item(t('Paste after'), <ClipboardPaste size={13} />, () => act.paste(b, id), { disabled: !clipboard.html, kbd: `${mod}V` })}
      {item(t('Paste inside'), <ClipboardPaste size={13} />, () => act.paste(b, id, true), { disabled: !clipboard.html, kbd: `⇧${mod}V` })}
      {item(t('Duplicate'), <Copy size={13} />, () => act.duplicate(b, id), { disabled: !can, kbd: `${mod}D` })}
      <div className="my-1 h-px bg-white/10" />
      {item(t('Copy style'), <Paintbrush size={13} />, () => styleAct.copy(b, id))}
      {item(t('Paste style'), <Paintbrush size={13} />, () => styleAct.paste(b, id), { disabled: !clipboard.style })}
      {item(t('Clear style'), <Eraser size={13} />, () => styleAct.clear(b, id), { disabled: !(b.doc.sections.find((s) => s.id === at.sec) as LocalSection | undefined)?.local?.[id] })}
      {item(t('Copy classes'), <Tags size={13} />, () => act.copyClasses(b, id), { disabled: !attr(at.el, 'class') })}
      {item(t('Paste classes'), <Tags size={13} />, () => act.pasteClasses(b, id), { disabled: !clipboard.classes })}
      <div className="my-1 h-px bg-white/10" />
      {item(t('Wrap in a Div'), <SquareDashed size={13} />, () => act.wrap(b, id), { disabled: !can })}
      {item(t('Unwrap'), <Ungroup size={13} />, () => act.unwrap(b, id), { disabled: !can })}
      {item(t('Select parent'), <CornerLeftUp size={13} />, () => act.parent(b, id), { kbd: 'Esc' })}
      {item(hidden.has(id) ? t('Show in the editor') : t('Hide in the editor'), hidden.has(id) ? <Eye size={13} /> : <EyeOff size={13} />, () => toggleHidden(id))}
      {item(shown.has(id) ? t('Stop showing') : t('Show temporarily'), <Eye size={13} />, () => toggleShown(id))}
      <div className="my-1 h-px bg-white/10" />
      {item(t('Delete'), <Trash2 size={13} />, () => act.remove(b, id), { danger: true, disabled: !can, kbd: '⌫' })}
    </div>
  )
}

/**
 * The confirmation before the design's scripts run in the editor (Live edit, Preview): they run in
 * the editor's origin, so they can do whatever the author can on this site. Asked once per editor
 * session; never remembered.
 */
function ScriptsConsent({ onRun, onCancel }: { onRun: () => void; onCancel: () => void }) {
  const run = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    run.current?.focus()
    const key = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      onCancel()
    }
    window.addEventListener('keydown', key, true)
    return () => window.removeEventListener('keydown', key, true)
  }, [onCancel])
  return (
    <div className="absolute inset-0 z-50 grid place-items-center bg-black/50 p-4" onClick={(e) => (e.stopPropagation(), onCancel())}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="wb-scripts-title" data-wb-scripts-consent="" className="w-full max-w-[440px] rounded-xl border border-white/10 bg-[#1d2327] p-5 text-[12.5px] leading-relaxed text-ink-200 shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <h2 id="wb-scripts-title" className="mb-2 text-[14px] font-semibold text-white">
          {t('Run this design’s scripts?')}
        </h2>
        <p>{t('The design’s scripts will run inside the editor with your admin access: they can do anything you can do on this site, such as change content, settings and users.')}</p>
        <p className="mt-2 text-ink-400">{t('Only continue if you trust where this design came from. Editing works without them; this is asked again in every editor session.')}</p>
        <div className="mt-4 flex flex-wrap justify-end gap-2">
          <button type="button" onClick={onCancel} className="rounded-md px-3 py-1.5 hover:bg-white/10">
            {t('Cancel')}
          </button>
          <button ref={run} type="button" data-wb-scripts-run="" onClick={onRun} className="rounded-md bg-amber-400 px-3 py-1.5 font-semibold text-black hover:bg-amber-300">
            {t('Run this design’s scripts with your admin access')}
          </button>
        </div>
      </div>
    </div>
  )
}

/** Exposed for the navigator: write the result of an element operation. */
export { commit }
export type { Section }
