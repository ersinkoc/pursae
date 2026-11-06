/**
 * Parity harness, side (a): the editor's live React preview of a fixture doc.
 *
 * Mounts upstream's Canvas (the editor canvas, from the patched copy in .work/upstream) in preview
 * mode inside the same MotionConfig as Builder.tsx, with a read-only BuilderApi. The device follows the
 * viewport width like the editor's device switch (≥ 1024 desktop, ≥ 640 tablet, else mobile).
 * URL: live.html?f=<fixture>. Sets window.__parityReady once the doc is mounted.
 */
import '@/index.css'
import { createRoot } from 'react-dom/client'
import { MotionConfig } from 'motion/react'
import { Canvas } from '@/builder/Canvas'
import { BuilderCtx, initHistory, type BuilderApi } from '@/builder/store'
import type { DocState, PageSettings } from '@/builder/types'

declare global {
  interface Window {
    __parityReady?: boolean
    __parityError?: string
  }
}

const noop = () => {}

function previewApi(doc: DocState, device: BuilderApi['device']): BuilderApi {
  return {
    doc,
    history: initHistory(doc),
    dispatch: noop,
    canUndo: false,
    canRedo: false,
    mode: 'basic',
    setMode: noop,
    device,
    setDevice: noop,
    preview: true,
    setPreview: noop,
    selection: null,
    select: noop,
    hoverSection: null,
    setHoverSection: noop,
    activeField: null,
    setActiveField: noop,
    focusReq: null,
    scrollReq: null,
    basicView: 'list',
    setBasicView: noop,
    studioTab: 'widgets',
    setStudioTab: noop,
    picker: null,
    boxHint: null,
    setBoxHint: noop,
    setPicker: noop,
    insertAt: null,
    setInsertAt: noop,
    dragging: null,
    setDragging: noop,
    drop: null,
    setDrop: noop,
    add: noop,
    remove: noop,
    notify: noop,
    structureOpen: false,
    setStructureOpen: noop,
    publishState: 'idle',
    dirty: false,
    publish: noop,
    clipboard: null,
    copySection: noop,
    pasteSection: noop,
    styleClipboard: null,
    copyStyle: noop,
    pasteStyle: noop,
    saved: [],
    saveComponent: noop,
    removeSaved: noop,
    menu: null,
    setMenu: noop,
    paletteOpen: false,
    setPaletteOpen: noop,
    dialog: null,
    setDialog: noop,
    page: {} as PageSettings,
    setPage: noop,
    saveState: 'saved',
    scrollTo: noop,
    milestones: { title: true, color: true, section: true, publish: true, dismissed: true },
    setMilestone: noop,
    resetPage: noop,
  }
}

export function deviceFor(width: number): BuilderApi['device'] {
  return width >= 1024 ? 'desktop' : width >= 640 ? 'tablet' : 'mobile'
}

async function main() {
  const name = new URLSearchParams(location.search).get('f') ?? ''
  if (!/^[\w-]+$/.test(name)) throw new Error(`bad fixture name "${name}"`)
  const res = await fetch(`/fixtures/${name}.doc.json`)
  if (!res.ok) throw new Error(`fixture ${name}: HTTP ${res.status}`)
  const doc = (await res.json()) as DocState
  const api = previewApi(doc, deviceFor(window.innerWidth))
  createRoot(document.getElementById('root')!).render(
    <MotionConfig reducedMotion="user">
      <BuilderCtx.Provider value={api}>
        <Canvas />
      </BuilderCtx.Provider>
    </MotionConfig>,
  )
  requestAnimationFrame(() => requestAnimationFrame(() => (window.__parityReady = true)))
}

main().catch((e) => {
  window.__parityError = String(e?.stack ?? e)
  console.error(e)
})
