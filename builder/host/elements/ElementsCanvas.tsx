/**
 * The element editor's canvas (plan §4.4, K5–K8): one frame with the whole page — the header
 * template, the page's code sections and the footer template — in the device's viewport (live.ts
 * liveGeometry: 1440 × 900, 768 × 1024 or 390 × 844, scaled to fit the panel), and every editing control drawn outside the
 * frame, over it: hover and selection outlines, the element's type, the toolbar, the breadcrumb,
 * the drop indicator, the context menu and the text toolbar.
 *
 * Two modes (plan K1–K5):
 *
 *  - **Live** (the default when the user trusts every design of the page, trust.ts): the design's
 *    scripts run, so the canvas is the published page (three.js scenes, a theme switch, sliders).
 *    The frame's document comes from live.ts (openLive / loadLive); edits patch the live DOM in place
 *    (applyToLive) and keep the scripts' state; only what cannot be patched (a script, the head,
 *    the wrappers) reloads it, keeping the scroll position and the theme (reloadPreserving).
 *    Interact mode (bottom bar, or holding Alt / ⌥) lets clicks and keys reach the design; text a
 *    script drew is edited as source text in a popover, never read back from the DOM (K5).
 *  - **Safe mode** (static): nothing of the design runs: the frame is sandboxed WITHOUT
 *    allow-scripts (the editor works on its DOM from outside) and its copy of the markup is
 *    neutralised by the browser's parser (neutral.ts); operations are reconciled into it
 *    (FrameSync). The only mode of a user who cannot run scripts.
 *
 * Live and Preview run the design with the user's admin access (same origin as the editor), so
 * they need the user's choice first: the non-blocking TrustPrompt (Always for this design / This
 * session only / Not now). The mode is decided from the boot object before the first frame is
 * made, so a trusted page loads once; switching modes makes a new frame element (the sandbox and
 * the document always change together). The choice covers what the page ran when it was given
 * (trust.ts): on every change of the code sections the canvas reads what the page runs now
 * (needsOf) and, when something is not covered (a section of another kit inserted, code pasted or
 * edited), draws the change static in the same render and asks again (runsNow, reconcileTrust).
 * Live never falls back to a frame in the editor's own document: when the server cannot give the
 * live frame, the canvas stays in Safe mode with a notice.
 */
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, Bold, ChevronRight, Clipboard, ClipboardPaste, Copy, CornerLeftUp, Eraser, Eye, EyeOff, GripVertical, Italic, Layers, Link2, MousePointerClick, Paintbrush, Palette as PaletteIcon, Pencil, Plus, RemoveFormatting, Shield, ShieldOff, SquareDashed, Tags, Trash2, Ungroup, Zap } from 'lucide-react'
import { useB, type BuilderApi } from '@/builder/store'
import { openInspectorTab } from '@/builder/StudioPanel'
import type { Section } from '@/builder/types'
import type { CmsHost } from '../cms-host'
import type { CodeParts } from '../contract'
import { t } from '../i18n'
import { act, commit, latest, pageOf, selectEl } from './actions'
import { FrameSync, frameDoc, type FrameMode } from './frame'
import { boxIn, dropAt, frameEl, isLocked, union, type Box, type Drop } from './geometry'
import { attr, decode, escText, ownText } from './html'
import { KIND_ICON, elementName, kindLabel, kindOf, namesOf } from './kinds'
import { childIds, locate, movable, parentId, tree, type Page } from './ops'
import { format, mapSource, startTextEdit, writeBack, type TextEdit } from './richtext'
import { sweep } from './neutral'
import { embedCss, embedTarget } from './embeds'
import { bumpFrame, clipboard, getElState, setElState, toggleIn, useElState } from './state'
import { localFonts, sectionsCSS, type LocalSection } from './local'
import { fontUrlFor } from '@/builder/fonts'
import { style as styleAct } from './actions'
import { kitsCSS, kitsOf, loadKits, syncKits, useKitStore } from './kitstyle'
import { Palette, type NewElement } from './PaletteDrawer'
import { TrustPrompt, reconcileTrust, revokeTrust, trustAlways, trustLater, trustSession } from './ScriptsGate'
import { applyToLive, guardLive, liveGeometry, liveShellOf, loadLive, openLive, releaseLive, reloadPreserving, setLiveStyles, type LiveInput } from './live'
import { drawnText, holdLabel, leavesFrame, liveChange, measureLoop, passesThrough, sourceTarget, watchFrame } from './interact'
import { covers, frameModeOf, grantsOf, markupReadOnly, needsOf, runsNow, sandboxOf } from './trust'
import { tc } from './canvas-tr'

const BAR = 34

const cmsHost = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

function partsOf(p: CodeParts | null | undefined) {
  return { header: p?.header?.sections ?? [], footer: p?.footer?.sections ?? [] }
}

/**
 * The editor-only stylesheet of the frame (a static frame: embeds drawn as placeholders, also in
 * Preview). Interact mode keeps the design's own cursors.
 */
function editCss(mode: FrameMode, fmode: FrameMode, hidden: ReadonlySet<string>, shown: ReadonlySet<string>, dragging: string | null, interact = false): string {
  const embeds = fmode === 'static' ? embedCss() : ''
  if (mode === 'preview') return embeds
  const q = (id: string) => `[data-wbc="${CSS.escape(id)}"]`
  let css = interact ? '' : '[data-wbc],a[href]{cursor:default}[data-wbc][contenteditable]{cursor:text;outline:none}'
  // An empty container (a new Section, Flexbox, Grid or Div) has a size to see and drop into. Safe
  // mode only: in Live the canvas is the published page's layout (a container the design's script
  // fills — slider dots, a list it renders — must not get a size of its own).
  if (!interact && fmode === 'static') css += '[data-wbc]:empty:not(img,input,hr,br,iframe,video,audio,svg,textarea,select,canvas,embed,object,source,track,wbr,script,style,template){min-height:48px;outline:1px dashed rgba(34,113,177,.55);outline-offset:-1px}'
  for (const id of shown) css += `${q(id)}{display:revert!important;visibility:visible!important;opacity:1!important}`
  for (const id of hidden) css += `${q(id)}{display:none!important}`
  if (dragging) css += `${q(dragging)}{opacity:.35!important;pointer-events:none!important}`
  return embeds + css
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
  // Without unfiltered_html the design's markup is read-only (trust.ts; actions.commit refuses too).
  const readOnly = markupReadOnly(host?.boot)
  const parts = useMemo(() => partsOf(host?.boot.codeParts), [host])
  const kits = host?.boot.kitBreakpoints
  const secs = useMemo(() => b.doc.sections.filter((s) => s.kind === 'code' && !s.hidden), [b.doc.sections])
  /** What the page runs now (its kits, its own code), read from the markup on every change. */
  const needs = useMemo(() => needsOf(parts.header, secs, parts.footer), [parts, secs])
  /** The design's scripts run: the user chose it and the choice covers what the page runs NOW. */
  const run = runsNow(st, needs)
  const mode: FrameMode = b.preview ? 'preview' : st.live && run ? 'live' : 'static'
  /** The frame's document: the design's scripts run only once the user chose it (else static, inert). */
  const fmode: FrameMode = frameModeOf(b.preview, st.live, run, st.canRun)
  const page = useMemo(() => pageOf(b.doc), [b.doc])
  const htmlNow = useMemo(() => Object.fromEntries(secs.map((s) => [s.id, s.content.html ?? ''])), [secs])
  const structKey = mode + '\u0003' + fmode + '\u0002' + secs.map((s, i) => [s.id, s.content.head, s.content.wrap, i === 0 ? `${s.content.lang}\u0000${s.content.body}\u0000${s.content.htmlAttrs}\u0000${s.content.bodyAttrs}` : ''].join('\u0000')).join('\u0001')

  const area = useRef<HTMLDivElement>(null)
  const frame = useRef<HTMLIFrameElement>(null)
  const [size, setSize] = useState({ w: 1200, h: 800 })
  // The frame is the device's viewport (1440 × 900, 768 × 1024, 390 × 844: innerWidth, innerHeight
  // and 100vh are the device's, as on the published page), scaled with a transform to fit the panel.
  // Live and Safe mode share it, so switching modes does not move the page.
  const geo = liveGeometry(b.device, { w: Math.max(100, size.w - (b.device === 'desktop' ? 0 : 48)), h: Math.max(100, size.h - BAR - (b.device === 'desktop' ? 0 : 32)) })
  const width = geo.width
  const zoom = geo.zoom
  /** The scaled frame's height on screen. */
  const stageH = geo.height * zoom

  // ---------- frame document ----------
  const base = typeof location !== 'undefined' ? location.origin + '/' : '/'
  const force = mode !== 'preview' && st.sel && st.styleState ? { id: st.sel, st: st.styleState } : null
  const localCss = useMemo(() => localCssOf(secs, parts, kits, force), [secs, parts, kits, force?.id, force?.st]) // eslint-disable-line react-hooks/exhaustive-deps
  const localRef = useRef(localCss)
  localRef.current = localCss
  // Class and variable edits (the kits' override stylesheets, kitstyle.ts).
  const kitStore = useKitStore()
  const kitList = useMemo(() => kitsOf(b.doc, parts as never).join(','), [b.doc, parts])
  // The page brought in something the user's choice does not cover: the frame above is already
  // static (run is false in this render); the choice is withdrawn and the prompt asks again.
  useLayoutEffect(() => reconcileTrust(needs), [needs, st.scripts])
  useEffect(() => {
    void loadKits(kitList ? kitList.split(',') : [])
  }, [kitList])
  const kitCss = useMemo(() => kitsCSS(b.doc), [b.doc, kitStore.kits]) // eslint-disable-line react-hooks/exhaustive-deps
  const kitRef = useRef(kitCss)
  kitRef.current = kitCss
  useEffect(() => syncKits(b.doc, (m) => b.notify(m)), [b.doc, kitStore.kits]) // eslint-disable-line react-hooks/exhaustive-deps
  // The document and whether the design's scripts may run in it change together: the sandbox the
  // frame gets is always the one its document was made for (a new frame element when it changes).
  // A static frame gets its document as srcdoc; a frame with the scripts gets it from live.ts once
  // it exists (gen: load again; keep: in the same element, keeping the scroll position and theme).
  // The mode is known before the first frame (state.ts reads the trust from the boot object).
  const [frameSrc, setFrameSrc] = useState(() => ({
    html: fmode === 'static' ? frameDoc(secs, parts, fmode, editCss(mode, fmode, st.hidden, st.shown, null, st.interact), base, localCss, kitCss) : '',
    scripts: fmode !== 'static',
    gen: 0,
    keep: false,
  }))
  const shown = useRef<Record<string, string>>(htmlNow)
  const shownKey = useRef(structKey)
  const sync = useRef<FrameSync | null>(null)
  /** The live frame finished loading (applyToLive may patch it). */
  const liveReady = useRef(false)
  const keepY = useRef(0)
  const [loaded, setLoaded] = useState(0)
  /** Markup an in-frame text edit (live mode) already shows: no reload for it. */
  const expect = useRef<Record<string, string>>({})

  const fdoc = () => frame.current?.contentDocument ?? null
  const fwin = () => frame.current?.contentWindow ?? null

  /** What live.ts builds the live document from (read when the frame loads). */
  const liveInput = useRef<LiveInput | null>(null)
  liveInput.current = {
    postId: host?.boot.document.postId ?? 0,
    page: secs,
    header: parts.header,
    footer: parts.footer,
    shell: liveShellOf(!!host?.boot.site?.themeParts, (host?.boot.document.page as { layout?: string } | undefined)?.layout, !!host?.boot.template),
    url: host?.boot.links.permalink || base,
    kitCss,
    localCss,
    editCss: editCss(mode, fmode, st.hidden, st.shown, null, st.interact),
  }

  const rebuild = useCallback(() => {
    keepY.current = fwin()?.scrollY ?? 0
    sync.current = null
    liveReady.current = false
    shown.current = htmlNow
    shownKey.current = structKey
    const s = getElState()
    if (fmode === 'static') {
      if (frame.current && frameSrc.scripts) releaseLive(frame.current)
      setFrameSrc((p) => ({ html: frameDoc(secs, parts, fmode, editCss(mode, fmode, s.hidden, s.shown, null, s.interact), base, localRef.current, kitRef.current), scripts: false, gen: p.gen + 1, keep: false }))
    } else setFrameSrc((p) => ({ html: '', scripts: true, gen: p.gen + 1, keep: p.scripts }))
  }, [htmlNow, structKey, secs, parts, mode, fmode, base, frameSrc.scripts])

  useLayoutEffect(() => {
    if (structKey !== shownKey.current) return rebuild()
    const changed = Object.keys(htmlNow).some((k) => htmlNow[k] !== shown.current[k])
    if (!changed) return
    if (fmode === 'static' && sync.current) {
      if (sync.current.update(shown.current, htmlNow)) {
        shown.current = htmlNow
        measure.current.kick()
        bumpFrame()
        return
      }
      return rebuild()
    }
    // Live: the change is patched into the running page (the scripts keep their state); a reload
    // only when it cannot be (live.ts says why). A text edited in the frame is already shown.
    const d = fdoc()
    const how = liveChange(
      shown.current,
      htmlNow,
      expect.current,
      fmode !== 'static' && d && liveReady.current
        ? () => {
            const r = applyToLive(d, shown.current, htmlNow)
            // Why an edit reloaded the live page (K4: only when it cannot be patched in place).
            if (r.reload) console.info(`[web-builder] live canvas reloaded: ${r.reason}`)
            return r
          }
        : null,
    )
    if (how === 'shown' || how === 'patched') {
      shown.current = htmlNow
      if (how === 'patched') {
        measure.current.kick()
        bumpFrame()
      }
      return
    }
    rebuild()
  }, [htmlNow, structKey]) // eslint-disable-line react-hooks/exhaustive-deps

  // A frame with the design's scripts: its document from live.ts (a new element, or reloaded in place).
  useEffect(() => {
    const f = frame.current
    const input = liveInput.current
    if (!frameSrc.scripts || !f || !input) return
    let cancelled = false
    void (async () => {
      let source
      try {
        source = await openLive(input)
      } catch (e) {
        // No live frame (refused, too large, offline, an older server): Safe mode, never a blank
        // canvas; the user's choice stays (Live asks the server again).
        if (cancelled) return
        console.warn('[web-builder] live canvas unavailable', e)
        setElState({ live: false, interact: false, editing: null })
        if (cur.current.b.preview) setElState({ scripts: false })
        cur.current.b.notify(tc('The live canvas could not be loaded ({message}). The canvas is in Safe mode.', { message: e instanceof Error ? e.message : String(e) }))
        return
      }
      if (cancelled) return
      const d = frameSrc.keep ? await reloadPreserving(f, source) : await loadLive(f, source)
      if (cancelled || !d || frame.current !== f) return
      if (!frameSrc.keep && keepY.current) f.contentWindow?.scrollTo(0, keepY.current)
      liveReady.current = true
      attach(d)
    })()
    return () => {
      cancelled = true
    }
  }, [frameSrc]) // eslint-disable-line react-hooks/exhaustive-deps

  // Unmounting: the live frame's WebGL contexts are freed.
  useLayoutEffect(
    () => () => {
      if (frame.current) releaseLive(frame.current)
    },
    [],
  )

  /** Writes one of the editor stylesheets into the frame; true when it changed. */
  const writeCss = (which: 'kit' | 'local' | 'edit', css: string): boolean => {
    const d = fdoc()
    if (!d) return false
    const el = d.querySelector(`style[data-wb-${which}]`)
    if (el) {
      if (el.textContent === css) return false
      el.textContent = css
      return true
    }
    if (!frameSrc.scripts || !liveReady.current) return false
    setLiveStyles(d, { [which]: css })
    return true
  }

  // The frame's width changed: the Style panel reads the cascade again.
  useEffect(() => {
    const id = requestAnimationFrame(() => bumpFrame())
    return () => cancelAnimationFrame(id)
  }, [b.device, zoom])

  // Local styles: the editor-owned stylesheet is rewritten on every change (no reload).
  useEffect(() => {
    if (writeCss('local', localCss)) bumpFrame()
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
    if (writeCss('kit', kitCss)) bumpFrame()
  }, [kitCss, loaded]) // eslint-disable-line react-hooks/exhaustive-deps

  // Editor stylesheet: hidden, temporarily shown, dragged element, Interact mode.
  const [dragId, setDragId] = useState<string | null>(null)
  useEffect(() => {
    writeCss('edit', editCss(mode, fmode, st.hidden, st.shown, dragId, st.interact))
  }, [st.hidden, st.shown, dragId, mode, fmode, loaded, st.interact]) // eslint-disable-line react-hooks/exhaustive-deps

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
  /** A trust request in flight / its error (the prompt, the revoke button). */
  const [trustBusy, setTrustBusy] = useState(false)
  const [trustError, setTrustError] = useState('')
  /** Text a script draws differently from its source, edited as source text (K5). */
  const [srcText, setSrcText] = useState<{ id: string; value: string; x: number; y: number } | null>(null)

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
      if (!bx || cur.current.mode === 'preview' || st.interact) tool.style.display = 'none'
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

  /** The navigator's hovered element, measured again with the selection. */
  const navHover = () => {
    const d = fdoc()
    const id = cur.current.st.hover
    const el = d && id ? frameEl(d, id) : null
    place(hoverRef.current, el ? box(el.getBoundingClientRect()) : null)
  }

  // Overlays follow the page: measured again (once per animation frame at most) whenever the frame
  // scrolls, resizes, changes (a script opening a menu or moving an element), loads an image or a
  // font, or animates (interact.ts watchFrame) — not in a loop that competes with the design's own.
  const measure = useRef(measureLoop(() => {
    refresh()
    if (cur.current.st.hover) navHover()
    if (partHoverRef.current) showPart(partHoverRef.current)
  }))
  useEffect(() => {
    const d = fdoc()
    const w = fwin()
    if (!d || !w) return
    const stop = watchFrame(d, w, (busy) => measure.current.kick(busy))
    return () => {
      stop()
      measure.current.stop()
    }
  }, [loaded])
  useEffect(() => {
    if (!st.sel && !(b.selection && !b.selection.el)) {
      place(selRef.current, null)
      if (toolRef.current) toolRef.current.style.display = 'none'
      setHiddenNow(false)
      return
    }
    measure.current.kick()
  }, [st.sel, b.selection, refresh, loaded, zoom, b.device, st.interact, size, editing])

  // Hover from the navigator.
  useEffect(navHover, [st.hover]) // eslint-disable-line react-hooks/exhaustive-deps

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
    if (!d || mode === 'preview' || markupReadOnly() || !textEditable(page, id)) return false
    const el = frameEl(d, id)
    if (!el) return false
    const at = locate(page, id)!
    // Text a script drew differently from the source (Live): its SOURCE text is edited in a
    // popover, the DOM the script drew is never read back (K5).
    const live = mode === 'live'
    const drawn = live && drawnText(at.tree.src.slice(at.el.openEnd, at.el.closeStart), el)
    setMenu(null)
    selectEl(cur.current.b, id, page)
    /** The source text in a popover (the design's DOM is never read back). */
    const popover = () => {
      const a = locate(cur.current.page, id)
      if (!a) return
      if (a.el.children.some((c) => c.type === 'el')) {
        cur.current.b.notify(t('This text is drawn by the design’s script: edit it in the section’s HTML code.'))
        return
      }
      const e2 = fdoc() && frameEl(fdoc()!, id)
      const bx = e2 ? box(e2.getBoundingClientRect()) : null
      setSrcText({ id, value: decode(a.tree.src.slice(a.el.openEnd, a.el.closeStart)), x: bx ? Math.max(4, bx.x) : 16, y: bx ? Math.max(4, bx.y + bx.h + 6) : 16 })
    }
    if (drawn) {
      popover()
      return true
    }
    // The new text is written from the source (the frame's copy lacks handlers and more). In Live
    // mode the element is watched while it is edited: if the design changes it meanwhile (not the
    // user's typing), nothing is saved and the source text is edited in the popover instead (K5).
    const map = mapSource(el, at.tree, at.el)
    const session = startTextEdit(el, false, point ?? null, (e) => writeBack(e, at.tree, map), (html, _text, foreign) => {
      setEditing(null)
      setElState({ editing: null })
      const { b, page } = cur.current
      if (foreign) {
        b.notify(tc('The design’s script changed this text while you edited it, so it was not saved. Edit the stored text here instead.'))
        popover()
        return
      }
      if (html === null) return
      const a = locate(page, id)
      if (a && cur.current.mode === 'live') expect.current[a.sec] = a.tree.src.slice(0, a.el.openEnd) + html + a.tree.src.slice(a.el.closeStart)
      act.setInner(b, id, html)
    }, live)
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

  /** The static frame loaded (srcdoc): swept, its sections collected for the reconciler, then attached. */
  const onLoad = () => {
    const d = fdoc()
    const w = fwin()
    if (!d || !w) return
    if (frame.current?.sandbox.contains('allow-scripts')) return // a live frame is attached after live.ts loaded it
    // The static frame's DOM is swept once more (whatever a second parse made of the copy is
    // inert too); nothing in it could run in between: no allow-scripts.
    sweep(d)
    const s = new FrameSync(d)
    s.collect()
    sync.current = s
    if (keepY.current) w.scrollTo(0, keepY.current)
    attach(d)
  }

  /**
   * The editor's listeners on a loaded frame document (static or live). In Interact mode, or with
   * Alt / ⌥ held, pointer events go to the design: the listeners step aside (only a link leaving
   * the page and a form submission are stopped: the editor's document must stay).
   */
  const attach = (d: Document) => {
    const w = d.defaultView
    if (!w) return
    setLoaded((n) => n + 1)
    bumpFrame(d)
    const hover = hoverRef.current
    let raf = 0
    let lastTarget: Element | null = null
    let lastAlt = false
    /** The source element a pointer means (a script's element: its nearest source ancestor). */
    const pick = (el: Element | null) => sourceTarget(d, el, (id) => locate(cur.current.page, id) !== null)
    const passes = (e: { altKey?: boolean }) => passesThrough(cur.current.st.interact, e)
    const hoverAt = () => {
      raf = 0
      const { mode, st } = cur.current
      if (mode === 'preview' || drag.current?.on || st.editing || passesThrough(st.interact, { altKey: lastAlt })) return place(hover, null)
      const el = lastTarget
      const part = partOf(el)
      if (part !== partHoverRef.current) showPart(part)
      if (part) return place(hover, null)
      const target = pick(el)
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
      lastTarget = embedTarget(d, e.target as Element, e.clientX, e.clientY)
      lastAlt = e.altKey
      if (!raf) raf = requestAnimationFrame(hoverAt)
    })
    d.addEventListener('pointerleave', () => {
      lastTarget = null
      place(hover, null)
    })
    w.addEventListener('scroll', () => place(hover, null), { passive: true })
    d.addEventListener(
      'pointerdown',
      (e) => {
        const { mode, st } = cur.current
        if (mode === 'preview' || e.button !== 0 || passes(e)) return
        const tg = embedTarget(d, e.target as Element, e.clientX, e.clientY)!
        if (st.editing && tg.closest('[contenteditable]')) return
        if (partOf(tg)) return
        const el = pick(tg)
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
    d.addEventListener('dragstart', (e) => cur.current.mode !== 'preview' && !passes(e) && e.preventDefault(), true)
    d.addEventListener(
      'click',
      (e) => {
        // An embed placeholder takes no pointer events: the one under the pointer is meant.
        const tg = embedTarget(d, e.target as Element, e.clientX, e.clientY)!
        const a = tg.closest('a[href]')
        const { mode, st, b } = cur.current
        if (mode === 'preview' || passes(e)) {
          // The design gets the click; only following a link away from the page is stopped: with
          // the design's scripts by guardLive (after the design's own handlers, so a script that
          // handles the link itself still works), without them here.
          if (!d.defaultView || !guarded.has(d.defaultView)) {
            if (leavesFrame(a)) e.preventDefault()
          }
          return
        }
        if (st.editing && tg.closest('[contenteditable]')) return
        e.preventDefault()
        e.stopPropagation()
        setMenu(null)
        if (Date.now() - dragged.current < 300) return // the click that ends a drag
        if (partOf(tg)) return
        const el = pick(tg)
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
        if (cur.current.mode === 'preview' || passes(e)) return
        const el = pick(embedTarget(d, e.target as Element, e.clientX, e.clientY))
        if (!el || isLocked(el) || cur.current.st.editing) return
        e.preventDefault()
        beginText(el.getAttribute('data-wbc')!, { x: e.clientX, y: e.clientY })
      },
      true,
    )
    d.addEventListener(
      'contextmenu',
      (e) => {
        if (cur.current.mode === 'preview' || passes(e)) return
        const el = pick(embedTarget(d, e.target as Element, e.clientX, e.clientY))
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
    // A live page stays on its address (Interact mode, Preview): a link or form the design does
    // not handle itself is stopped after the design's own listeners ran (live.ts guardLive).
    if (frameSrc.scripts) {
      guarded.add(w)
      guardLive(d, (url) => cur.current.b.notify(tc('Links open on the published page, not in the editor: {url}', { url: url.length > 80 ? url.slice(0, 79) + '…' : url })))
    }
    // A form never submits the frame away (also in Interact mode: the design's own handlers still run).
    d.addEventListener('submit', (e) => e.preventDefault(), true)
    // Keys inside the frame reach the editor's shortcuts (undo, delete, arrows…), not while typing
    // and not in Interact mode (they are the design's then: a slider's arrows, a menu's Escape).
    d.addEventListener(
      'keydown',
      (e) => {
        const tg = e.target as HTMLElement
        // A key the design's script dispatched is not the user's: it never reaches the editor's shortcuts.
        if (!e.isTrusted || cur.current.st.interact || tg.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(tg.tagName)) return
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
      // Interact mode: the editor's keys rest; Escape (outside the frame) goes back to selecting.
      if (s.interact) {
        if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          setElState({ interact: false })
        }
        return
      }
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
    if (editing && (st.sel !== editing.id || st.interact)) editing.end(true)
  }, [st.sel, editing, st.interact])

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
      if (id) out.unshift({ id, label: elementName(sel.tree, p, names) })
    }
    const s = b.doc.sections.find((x) => x.id === sel.sec)
    out.unshift({ id: null, label: s?.name || t('Section') })
    // A name the crumb before already says (a block named by its own heading) shows the type instead.
    for (let i = 1; i < out.length; i++) {
      const el = out[i].id ? sel.tree.byId.get(out[i].id!) : undefined
      if (el && out[i].label === out[i - 1].label && !names[out[i].id!]) out[i] = { ...out[i], label: kindLabel(kindOf(sel.tree, el)) }
    }
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
    if (!st.sel || !frame.current || readOnly) return
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

  /** A trust request (Always, Stop always): busy while it runs; its failure shown in the prompt, else as a notice. */
  const runTrust = async (f: () => Promise<void>) => {
    setTrustBusy(true)
    setTrustError('')
    try {
      await f()
    } catch (e) {
      const msg = tc('Your choice could not be saved: {message}', { message: e instanceof Error ? e.message : String(e) })
      if (getElState().prompt) setTrustError(msg)
      else b.notify(msg)
    } finally {
      setTrustBusy(false)
    }
  }

  const framed = b.device !== 'desktop'
  const SelIcon = selKind ? KIND_ICON[selKind] : null
  const selName = sel && selKind ? elementName(sel.tree, sel.el, names, selKind) : ''
  const canEditText = !readOnly && !!st.sel && textEditable(page, st.sel)

  return (
    <div ref={area} className="relative min-w-0 flex-1 overflow-hidden bg-ink-950" data-wb-elements="" onClick={() => !b.preview && (selectEl(b, null), b.select(null))}>
      <div className={`absolute inset-x-0 top-0 flex justify-center ${framed ? 'dots-bg pt-4' : ''}`} style={{ bottom: BAR }}>
        <div className={framed ? 'overflow-hidden rounded-[14px] shadow-[0_0_0_1px_rgba(255,255,255,.08),0_30px_80px_-20px_rgba(0,0,0,.7)]' : ''} style={{ width: width * zoom, height: stageH }} data-wb-stage="">
          {/* Static: no allow-scripts (the editor drives the DOM from outside; nothing of the design
              can run). With the user's choice only (trust or this session): the design's scripts,
              same origin, the document from live.ts. */}
          <iframe
            key={frameSrc.scripts ? 'scripts' : 'inert'}
            ref={frame}
            title={t('Page canvas')}
            srcDoc={frameSrc.scripts ? undefined : frameSrc.html}
            onLoad={frameSrc.scripts ? undefined : onLoad}
            data-wb-frame={mode}
            data-wb-scripts={frameSrc.scripts ? '1' : '0'}
            className="block border-0 bg-white"
            style={{ width, height: geo.height, transform: `scale(${zoom})`, transformOrigin: '0 0' }}
            sandbox={sandboxOf(frameSrc.scripts)}
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
              <SelIcon size={12} /> {selName === kindLabel(selKind!) ? selName : `${kindLabel(selKind!)} · ${selName}`}
            </span>
          )}
        </div>
        <div ref={dropRef} data-wb-drop="" className="absolute left-0 top-0 rounded-sm bg-[#2271b1] data-[inside='1']:border-2 data-[inside='1']:border-dashed data-[inside='1']:border-[#2271b1] data-[inside='1']:bg-[#2271b1]/10" style={{ display: 'none' }} />
        {sel && !b.preview && !editing && (
          <div ref={toolRef} data-wb-toolbar="" className="pointer-events-auto absolute left-0 top-0 flex items-center gap-0.5 rounded-lg bg-[#1d2327] p-0.5 text-white shadow-xl" style={{ display: 'none' }} onClick={(e) => e.stopPropagation()}>
            {!readOnly && (
              <span title={t('Drag to move')} onPointerDown={startHandleDrag} className="grid size-7 cursor-grab place-items-center rounded-md hover:bg-white/15" data-wb-handle="">
                <GripVertical size={14} />
              </span>
            )}
            {bx(t('Select parent'), <CornerLeftUp size={14} />, () => act.parent(b, st.sel!))}
            {!readOnly && bx(t('Move up'), <ArrowUp size={14} />, () => act.step(b, st.sel!, -1))}
            {!readOnly && bx(t('Move down'), <ArrowDown size={14} />, () => act.step(b, st.sel!, 1))}
            {canEditText && bx(t('Edit text'), <Pencil size={14} />, () => beginText(st.sel!))}
            {bx(t('Style'), <Paintbrush size={14} />, () => {
              b.setMode('studio')
              openInspectorTab('style')
            })}
            {!readOnly && bx(t('Duplicate'), <Copy size={14} />, () => act.duplicate(b, st.sel!))}
            {!readOnly && bx(t('Wrap in a Div'), <SquareDashed size={14} />, () => act.wrap(b, st.sel!))}
            {(hiddenNow || st.shown.has(st.sel!)) && bx(t('Show temporarily'), st.shown.has(st.sel!) ? <EyeOff size={14} /> : <Eye size={14} />, () => toggleShown(st.sel!), { active: st.shown.has(st.sel!) })}
            {!readOnly && bx(t('Delete'), <Trash2 size={14} />, () => act.remove(b, st.sel!), { danger: true })}
          </div>
        )}
        {editing && (
          <div ref={textBarRef} data-wb-textbar="" className="pointer-events-auto absolute left-0 top-0 flex items-center gap-0.5 rounded-lg bg-[#1d2327] p-0.5 text-white shadow-xl" onPointerDown={(e) => e.preventDefault()} onClick={(e) => e.stopPropagation()}>
            {bx(t('Bold'), <Bold size={14} />, () => editing.user(() => format(editing.el.ownerDocument, 'bold')))}
            {bx(t('Italic'), <Italic size={14} />, () => editing.user(() => format(editing.el.ownerDocument, 'italic')))}
            {bx(t('Link'), <Link2 size={14} />, () => {
              const url = window.prompt(t('Link address'), 'https://')
              if (url) editing.user(() => format(editing.el.ownerDocument, 'link', url))
            })}
            {bx(t('Clear formatting'), <RemoveFormatting size={14} />, () => editing.user(() => format(editing.el.ownerDocument, 'clear')))}
          </div>
        )}
        {menu && <ContextMenu b={b} menu={menu} page={page} close={() => setMenu(null)} hidden={st.hidden} shown={st.shown} toggleHidden={toggleHidden} toggleShown={toggleShown} beginText={beginText} />}
      </div>

      {st.palette && !b.preview && <Palette startDrag={startAddDrag} secs={secs} parts={parts} />}

      {b.preview && !run && (
        <div data-wb-scripts-off="" className="absolute left-1/2 top-3 z-30 flex max-w-[92%] -translate-x-1/2 items-center gap-3 rounded-lg bg-[#1d2327] px-3 py-2 text-[12px] text-ink-200 shadow-xl" onClick={(e) => e.stopPropagation()}>
          <span>{t('The design’s scripts are not running in this preview.')}</span>
          {st.canRun ? (
            <button type="button" data-wb-scripts-ask="" onClick={() => (covers(needs, grantsOf(st)) ? setElState({ scripts: true }) : setElState({ prompt: true }))} className="shrink-0 rounded-md bg-amber-400/20 px-2 py-1 text-amber-200 hover:bg-amber-400/30">
              {t('Run them…')}
            </button>
          ) : (
            <span className="text-ink-400">{tc('The design’s scripts run on the published page.')}</span>
          )}
        </div>
      )}
      {st.prompt && !run && (
        <TrustPrompt
          needs={needs}
          grants={grantsOf(st)}
          canRun={st.canRun}
          busy={trustBusy}
          error={trustError}
          onAlways={() => void runTrust(() => trustAlways(needs))}
          onSession={() => (setTrustError(''), trustSession(needs))}
          onLater={() => (setTrustError(''), trustLater())}
        />
      )}
      {srcText && (
        <SourceText
          value={srcText.value}
          x={srcText.x}
          y={srcText.y}
          onCancel={() => setSrcText(null)}
          onSave={(v) => {
            const id = srcText.id
            setSrcText(null)
            if (v !== srcText.value) act.setInner(b, id, escText(v))
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
          {st.interact && !b.preview ? (
            <span className="truncate text-accent-soft" data-wb-interacting="">
              {tc('Interacting with the page · Esc or Interact to go back to editing')}
            </span>
          ) : (
            !crumbs.length && <span className="text-ink-500">{b.preview ? t('Preview') : t('Click an element to select it · double-click a text to edit it')}</span>
          )}
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
        {!b.preview &&
          (st.canRun ? (
            <div role="group" aria-label={tc('Live')} className="flex shrink-0 items-center rounded-md bg-white/[0.06] p-0.5" data-wb-mode={st.live && run ? 'live' : 'safe'}>
              <button
                type="button"
                data-wb-live=""
                aria-pressed={st.live && run}
                title={tc('Live: the design’s scripts run and the canvas matches your site.')}
                onClick={() => (covers(needs, grantsOf(st)) ? setElState({ scripts: true, live: true, editing: null, interact: false }) : setElState({ prompt: true }))}
                className={`flex items-center gap-1 rounded px-2 py-0.5 ${st.live && run ? 'bg-amber-400/20 text-amber-200' : 'hover:bg-white/10 hover:text-white'}`}
              >
                <Zap size={12} /> {tc('Live')}
              </button>
              <button
                type="button"
                data-wb-safe=""
                aria-pressed={!(st.live && run)}
                title={tc('Safe mode: the design’s scripts do not run in the editor (they run on the published page).')}
                onClick={() => setElState({ live: false, editing: null, interact: false })}
                className={`flex items-center gap-1 rounded px-2 py-0.5 ${!(st.live && run) ? 'bg-white/15 text-white' : 'hover:bg-white/10 hover:text-white'}`}
              >
                <Shield size={12} /> {tc('Safe mode')}
              </button>
            </div>
          ) : (
            <span data-wb-safe-note="" title={tc('Your role cannot run a design’s scripts in the editor. They run on the published page.')} className="flex shrink-0 items-center gap-1 text-ink-400">
              <Shield size={12} /> {tc('Safe mode · scripts run on the published page')}
            </span>
          ))}
        {!b.preview && st.canRun && needs.kits.some((k) => st.trust.has(k)) && (
          <button
            type="button"
            data-wb-untrust=""
            disabled={trustBusy}
            title={tc('Stop always running this design’s scripts')}
            aria-label={tc('Stop always running this design’s scripts')}
            onClick={() =>
              void runTrust(async () => {
                await revokeTrust(needs.kits)
                b.notify(tc('This design’s scripts no longer run automatically in the editor.'))
              })
            }
            className="grid size-6 shrink-0 place-items-center rounded-md hover:bg-white/10 hover:text-white disabled:opacity-50"
          >
            <ShieldOff size={12} />
          </button>
        )}
        {!b.preview && (
          <button
            type="button"
            data-wb-interact=""
            aria-pressed={st.interact}
            title={tc('Interact with the page: clicks and keys go to the design (theme switch, menus, tabs, sliders). Hold {key} to interact for a moment.', { key: holdLabel() })}
            onClick={() => {
              setMenu(null)
              setElState((s) => ({ interact: !s.interact, editing: null }))
            }}
            className={`flex shrink-0 items-center gap-1 rounded-md px-2 py-1 ${st.interact ? 'bg-accent/25 text-accent-soft' : 'hover:bg-white/10 hover:text-white'}`}
          >
            <MousePointerClick size={12} /> {tc('Interact')}
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

/**
 * The source text of an element whose text a script shows differently (Live, K5): edited as the
 * page stores it, never taken from what the script drew. Enter saves, Escape cancels.
 */
function SourceText({ value, x, y, onSave, onCancel }: { value: string; x: number; y: number; onSave: (v: string) => void; onCancel: () => void }) {
  const [v, setV] = useState(value)
  const ref = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    ref.current?.focus()
    ref.current?.select()
  }, [])
  return (
    <div role="dialog" aria-labelledby="wb-src-text" data-wb-source-text="" className="absolute z-40 w-[min(360px,calc(100%-8px))] rounded-lg border border-white/10 bg-[#1d2327] p-3 text-[12px] text-ink-200 shadow-2xl" style={{ left: x, top: y }} onClick={(e) => e.stopPropagation()} onPointerDown={(e) => e.stopPropagation()}>
      <div id="wb-src-text" className="mb-1 font-semibold text-white">
        {tc('Text shown by the design’s script')}
      </div>
      <p className="mb-2 text-ink-400">{tc('On the page the design’s script shows this text differently. This edits the text stored in the page; the script may still change it.')}</p>
      <textarea
        ref={ref}
        value={v}
        rows={3}
        onChange={(e) => setV(e.target.value)}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === 'Escape') onCancel()
          else if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault()
            onSave(v)
          }
        }}
        className="w-full resize-y rounded-md border border-white/10 bg-black/30 p-2 text-white outline-none focus:border-accent"
      />
      <div className="mt-2 flex justify-end gap-2">
        <button type="button" onClick={onCancel} className="rounded-md px-2.5 py-1 hover:bg-white/10">
          {tc('Cancel')}
        </button>
        <button type="button" data-wb-source-save="" onClick={() => onSave(v)} className="rounded-md bg-accent px-2.5 py-1 font-semibold text-white hover:opacity-90">
          {tc('Save')}
        </button>
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
  const can = movable(at.el) && !markupReadOnly()
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

/** Frame windows guardLive watches (their links are stopped after the design's handlers). */
const guarded = new WeakSet<Window>()

/** Exposed for the navigator: write the result of an element operation. */
export { commit }
export type { Section }
