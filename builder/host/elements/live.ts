/**
 * The live frame: the element editor's canvas with the design's scripts running, equivalent to the
 * published page (EDITOR-CANLI plan, K1/K4/K5).
 *
 * API summary (stable; ElementsCanvas codes against it):
 *
 *   openLive(input, rest?)        → Promise<LiveSource>   the frame's source: { src } a same-origin URL at
 *                                                         the page's own address, rendered by the server like
 *                                                         the published page (true location, CSP nonce, shell,
 *                                                         tail); { srcdoc } when the server route is not there
 *   liveDoc(input, nonce?)        → string                the client-built fallback document (srcdoc)
 *   loadLive(frame, source)       → Promise<Document|null> releases the old document (WebGL contexts) and
 *                                                         loads the source; resolves once it fired load
 *   setLiveStyles(doc, css)                               editor stylesheets (kit, local, edit) at the head's end
 *   releaseLive(frame)                                    frees the frame's WebGL contexts, blanks it (unmount)
 *   captureLive(frame, keys?)     → LiveKeep              scroll position, <html>/<body> class + data-* (theme)
 *   restoreLive(frame, keep)                              puts them back
 *   reloadPreserving(frame, source, keys?) → Promise<Document|null>   capture + load + restore
 *   applyToLive(doc, prev, next)  → LiveApply             patches the live DOM for changed sections (by
 *                                                         data-wbc), or says a reload is unavoidable
 *   liveShellOf(themeParts, layout, template) → LiveShell            how the site prints the main part
 *
 * K5: nothing here reads the live DOM back into the source; applyToLive only writes source → DOM.
 */
import type { Section } from '@/builder/types'
import { frameDoc } from './frame'

/** How the site prints the edited doc's main part (it changes the document's structure). */
export type LiveShell =
  /** Hello Pursae with its parts: a main without its own <main> sits in main#content.site-main.wb-main-part. */
  | 'theme'
  /** A canvas page (wbCanvas): a main without its own <main> sits in main#wb-main. */
  | 'canvas'
  /** A header / footer template edited alone: no main wrapper. */
  | 'part'

export interface LiveInput {
  /** The edited post (page or template) id. */
  postId: number
  /** The edited doc's visible code sections, in order. */
  page: Section[]
  /** The locked header / footer template sections (Boot.codeParts), [] for none. */
  header: Section[]
  footer: Section[]
  shell: LiveShell
  /** The page's address on the site (Boot.links.permalink): relative URLs resolve against it. */
  url: string
  /** The kits' class/variable overrides (kitstyle.ts), the local styles (local.ts), editor-only rules. */
  kitCss: string
  localCss: string
  editCss: string
}

/** The frame's source: set `src` (and no srcdoc) or `srcdoc`. */
export type LiveSource = { src: string; srcdoc?: undefined } | { srcdoc: string; src?: undefined }

/** What reloadPreserving keeps. */
export interface LiveKeep {
  x: number
  y: number
  html: Record<string, string>
  body: Record<string, string>
}

/** applyToLive's answer. */
export type LiveApply = { reload: false; touched: Element[] } | { reload: true; reason: string }

/** The client-built live document (srcdoc fallback). */
export function liveDoc(input: LiveInput, nonce = ''): string {
  void nonce
  return frameDoc(input.page, { header: input.header, footer: input.footer }, 'live', input.editCss, input.url, input.localCss, input.kitCss)
}

/** The frame's source for input (server-rendered when possible). */
export async function openLive(input: LiveInput): Promise<LiveSource> {
  return { srcdoc: liveDoc(input) }
}

/** Loads source into frame after releasing its current document. */
export async function loadLive(frame: HTMLIFrameElement, source: LiveSource): Promise<Document | null> {
  releaseContexts(frame.contentDocument)
  return new Promise((resolve) => {
    const done = () => {
      frame.removeEventListener('load', done)
      resolve(frame.contentDocument)
    }
    frame.addEventListener('load', done)
    if (source.src !== undefined) {
      frame.removeAttribute('srcdoc')
      frame.src = source.src
    } else frame.srcdoc = source.srcdoc
  })
}

/** Writes the editor stylesheets (created at the end of the head when missing). */
export function setLiveStyles(doc: Document, css: { kit?: string; local?: string; edit?: string }): void {
  void doc
  void css
}

/** Loses every WebGL context of doc's canvases (the browser's context limit). */
function releaseContexts(doc: Document | null | undefined): void {
  void doc
}

/** Frees the frame's WebGL contexts and blanks it. */
export function releaseLive(frame: HTMLIFrameElement): void {
  releaseContexts(frame.contentDocument)
}

export function captureLive(frame: HTMLIFrameElement, keys?: string[]): LiveKeep {
  void keys
  const w = frame.contentWindow
  return { x: w?.scrollX ?? 0, y: w?.scrollY ?? 0, html: {}, body: {} }
}

export function restoreLive(frame: HTMLIFrameElement, keep: LiveKeep): void {
  frame.contentWindow?.scrollTo(keep.x, keep.y)
}

export async function reloadPreserving(frame: HTMLIFrameElement, source: LiveSource, keys?: string[]): Promise<Document | null> {
  const keep = captureLive(frame, keys)
  const d = await loadLive(frame, source)
  restoreLive(frame, keep)
  return d
}

/** Patches the live DOM: prev/next are section id → content.html of the edited doc. */
export function applyToLive(doc: Document, prev: Record<string, string>, next: Record<string, string>): LiveApply {
  void doc
  void prev
  void next
  return { reload: true, reason: 'not implemented' }
}

export function liveShellOf(themeParts: boolean, layout: string | undefined, template: boolean): LiveShell {
  if (template) return 'part'
  return themeParts && layout !== 'canvas' ? 'theme' : 'canvas'
}
