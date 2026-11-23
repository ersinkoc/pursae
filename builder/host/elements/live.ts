/**
 * The live frame: the element editor's canvas with the design's scripts running, equivalent to the
 * published page (EDITOR-CANLI plan, K1/K4/K5).
 *
 * API summary (stable; ElementsCanvas codes against it):
 *
 *   openLive(input)               → Promise<LiveSource>   the frame's source: { src } a same-origin URL at the
 *                                                         page's own address, rendered by the server like the
 *                                                         published page (POST documents/{id}/live-frame,
 *                                                         internal/server/webbuilder_liveframe.go: real location,
 *                                                         the front end's CSP and nonce, shell, tail, attributes);
 *                                                         { srcdoc } (liveDoc) only when that route is missing
 *   liveDoc(input, nonce?)        → string                the client-built fallback document (srcdoc)
 *   LIVE_SANDBOX                                          the sandbox a live frame gets (scripts, same origin,
 *                                                         forms, popups, modals; never top navigation)
 *   loadLive(frame, source)       → Promise<Document|null> releases the frame's current document (WebGL
 *                                                         contexts) and loads source; resolves on load
 *   setLiveStyles(doc, css)                               the editor stylesheets (kit, local, edit), kept last
 *   guardLive(doc, onLeave?)      → () => void            keeps the frame on the page: a link or form the design
 *                                                         does not handle itself that would load another
 *                                                         document is stopped (onLeave gets its URL)
 *   releaseLive(frame)                                    frees the frame's WebGL contexts and blanks it (unmount)
 *   captureLive(frame, keys?)     → LiveKeep              scroll position, <html>/<body> class, style and data-*
 *   restoreLive(frame, keep)                              puts them back
 *   reloadPreserving(frame, source, keys?) → Promise<Document|null>   capture + load + restore
 *   applyToLive(doc, prev, next)  → LiveApply             patches the live DOM for the sections whose markup
 *                                                         changed (elements matched by data-wbc), or answers
 *                                                         { reload: true, reason } when that is not safe
 *   liveShellOf(themeParts, layout, template) → LiveShell how the site prints the edited doc's main
 *   liveGeometry(device, avail)  → {width,height,zoom}   the frame's viewport (the device's) and its scale
 *
 * K5: nothing here reads the live DOM back into the source; applyToLive only writes source → DOM,
 * and only the delta between the old and the new source, so what the design's scripts did to the
 * DOM (classes, styles, drawn content) stays where the source did not change.
 */
import type { Section } from '@/builder/types'
import type { CmsHost } from '../cms-host'
import { isRestError } from '../rest'
import { RAW, attrEdit, elementKids, outer, parse, splice, wbc, type Edit, type El, type Node as SrcNode, type Tree } from './html'
import { prefixIds, wrapChain } from './frame'

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
  /** The page's address on the site (Boot.links.permalink): the frame is served at its path. */
  url: string
  /** The document title (optional). */
  title?: string
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
  html: Record<string, string> | null
  body: Record<string, string> | null
}

/** applyToLive's answer: the elements it changed, or why the frame must be reloaded. */
export type LiveApply = { reload: false; touched: Element[] } | { reload: true; reason: string }

/**
 * The sandbox of a live frame: the design runs as on the site (scripts, same origin for its fonts,
 * modules, storage and for the editor to reach the DOM, forms, popups, alert/confirm, downloads,
 * pointer lock, presentation), but it can never navigate the editor's page (no allow-top-navigation).
 */
export const LIVE_SANDBOX = 'allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-modals allow-downloads allow-pointer-lock allow-presentation'

/** The editor-owned global the GL hook keeps the frame's WebGL contexts in. */
const GL_KEY = '__wbLiveGL'

/**
 * The live document's first head script: records the WebGL contexts the design creates (so a
 * reload can release them, see releaseContexts) and removes itself at once, so the design's DOM
 * has no editor node in its head. getContext keeps its behaviour. It also takes the frame's
 * token (?wb-live-frame=…) out of the address before any design script runs, so location.search
 * and location.href are the published page's (a reload the design itself makes then shows the
 * published page; the canvas reloads with a new token).
 */
export const GL_HOOK =
  `(function(){var P=HTMLCanvasElement.prototype,g=P.getContext,L=window.${GL_KEY}=[];` +
  `P.getContext=function(t){var c=g.apply(this,arguments);if(c&&/webgl/i.test(String(t))&&L.indexOf(c)<0)L.push(c);return c};` +
  `if(window.OffscreenCanvas){var O=OffscreenCanvas.prototype,h=O.getContext;O.getContext=function(t){var c=h.apply(this,arguments);if(c&&/webgl/i.test(String(t))&&L.indexOf(c)<0)L.push(c);return c}}` +
  `try{var u=location.href,v=u.replace(/([?&])wb-live-frame=[^&#]*&?/,'$1').replace(/[?&](#|$)/,'$1');if(v!==u)history.replaceState(history.state,'',v)}catch(e){}` +
  `var s=document.currentScript;if(s)s.remove()})()`

const esc = (v: string) => v.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')
const styleText = (css: string) => css.replace(/<\//g, '<\\/').replace(/<!--/g, '<\\!--')
const tagOf = (open: string) => /^<([a-zA-Z][a-zA-Z0-9-]*)/.exec(open)![1].toLowerCase()

/** Gives every script start tag of markup the nonce (as the published page's cspTrustScripts does). */
export function withNonce(markup: string, nonce: string): string {
  if (!nonce || !/<script/i.test(markup)) return markup
  const t = parse(markup)
  const edits: Edit[] = []
  for (const el of t.all) if (el.tag === 'script') edits.push(...attrEdit(t, el, 'nonce', nonce))
  return edits.length ? splice(markup, edits) : markup
}

/** The editor page's CSP nonce ('' when it has none). */
export function editorNonce(doc: Document = document): string {
  for (const s of Array.from(doc.querySelectorAll('script'))) if (s.nonce) return s.nonce
  return ''
}

/** Distinct top-level head tags, the page's head first (webbuilder.MergeHeads, wbRenderTheme). */
function mergeLiveHeads(heads: string[]): string {
  const seen = new Set<string>()
  const out: string[] = []
  for (const h of heads) {
    if (!h.trim()) continue
    const t = parse(h)
    for (const el of elementKids(t.root)) {
      const tag = outer(t, el)
      if (seen.has(tag)) continue
      seen.add(tag)
      out.push(tag)
    }
  }
  return out.join('\n')
}

/**
 * The client-built live document, used only when the server route is missing (srcdoc). It follows
 * the published page as far as the client can: the parts in their shared wrappers, the heads page
 * first, the shell's main landmark, the tail after the footer, the nonce on every script and a
 * <base> at the page's address. Its location stays about:srcdoc (see openLive).
 */
export function liveDoc(input: LiveInput, nonce = ''): string {
  const theme = input.shell === 'theme'
  const header = theme ? input.header : []
  const footer = theme ? input.footer : []
  let tailFrom = input.page.length
  while (tailFrom > 0 && input.page[tailFrom - 1].content.tail === '1') tailFrom--
  const main = input.page.slice(0, tailFrom)
  const tail = input.page.slice(tailFrom)
  const stack: string[] = []
  let body = ''
  const closeTo = (k: number) => {
    while (stack.length > k) body += `</${tagOf(stack.pop()!)}>`
  }
  const put = (s: Section, prefix?: string) => {
    const chain = wrapChain(s.content.wrap)
    let k = 0
    while (k < stack.length && k < chain.length && stack[k] === chain[k]) k++
    closeTo(k)
    for (const w of chain.slice(k)) {
      body += w
      stack.push(w)
    }
    body += prefix ? prefixIds(s.content.html ?? '', prefix) : (s.content.html ?? '')
  }
  const ownMain = main.some((s) => /<main[\s>]/i.test(s.content.html ?? '') || /<main[\s>]/i.test(s.content.wrap ?? '') || /\srole\s*=\s*["']?main\b/i.test(s.content.html ?? ''))
  for (const s of header) put(s, 'h:')
  // The main landmark sits inside the wrappers the header and the page share.
  if (input.shell !== 'part' && !ownMain) {
    const first = main[0] ? wrapChain(main[0].content.wrap) : []
    let k = 0
    while (k < stack.length && k < first.length && stack[k] === first[k]) k++
    closeTo(k)
    const open = theme ? '<main id="content" class="site-main wb-main-part">' : '<main id="wb-main">'
    body += open
    const depth = stack.length
    for (const s of main) {
      const chain = wrapChain(s.content.wrap).slice(depth)
      let j = 0
      while (j < stack.length - depth && j < chain.length && stack[depth + j] === chain[j]) j++
      closeTo(depth + j)
      for (const w of chain.slice(j)) {
        body += w
        stack.push(w)
      }
      body += s.content.html ?? ''
    }
    closeTo(depth)
    body += '</main>'
  } else for (const s of main) put(s)
  for (const s of footer) put(s, 'f:')
  closeTo(0)
  for (const s of tail) body += s.content.html ?? ''
  const all = [...main, ...tail, ...header, ...footer]
  const first = all[0]?.content ?? {}
  const lang = first.lang || (theme || input.shell === 'part' ? 'en-US' : 'en')
  const htmlAttrs = (first.htmlAttrs ?? '').trim()
  const bodyAttrs = (first.bodyAttrs ?? '').trim()
  // The body's class: the design's (bodyAttrs, as the front end merges it, and content.body).
  const fromAttrs = /(?:^|\s)class="([^"]*)"/.exec(bodyAttrs)?.[1] ?? ''
  const bodyClass = [...new Set([input.shell === 'canvas' ? 'web-builder-page' : '', ...fromAttrs.split(/\s+/), ...(first.body ?? '').split(/\s+/)].filter(Boolean))].join(' ')
  const okAttrs = (a: string) => (a && !/[<>]/.test(a) ? ' ' + a : '')
  const bodyRest = okAttrs(bodyAttrs).replace(/\sclass="[^"]*"/, '')
  const head = mergeLiveHeads([...main, ...tail, ...header, ...footer].map((s) => s.content.head ?? ''))
  const doc =
    `<!DOCTYPE html>\n<html lang="${esc(lang)}"${okAttrs(htmlAttrs)}>\n<head>\n<script>${GL_HOOK}</script><meta charset="UTF-8">\n<meta name="viewport" content="width=device-width, initial-scale=1">\n` +
    `<base href="${esc(input.url)}" data-wb-editor="">${theme ? '\n<link rel="profile" href="https://gmpg.org/xfn/11">' : ''}\n<title>${esc(input.title ?? '')}</title>\n${head}\n` +
    `<style data-wb-kit>${styleText(input.kitCss)}</style><style data-wb-local>${styleText(input.localCss)}</style><style data-wb-edit>${styleText(input.editCss)}</style>\n</head>\n` +
    `<body class="${esc(bodyClass)}"${bodyRest}>\n${body}\n</body>\n</html>\n`
  return withNonce(doc, nonce)
}

const host = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

/** A section as the live-frame route reads it. */
const wire = (s: Section) => ({ id: s.id, kind: s.kind, hidden: false, content: s.content })

/**
 * The frame's source for input: the server renders it at the page's own address (true location,
 * CSP and nonce of the published page); the client-built srcdoc only when the route is missing
 * (an older server). Other errors (no permission: the user may not run scripts) are thrown.
 */
export async function openLive(input: LiveInput): Promise<LiveSource> {
  const h = host()
  if (!h || !input.postId) return { srcdoc: liveDoc(input, editorNonce()) }
  try {
    const r = await h.rest.request<{ url: string }>('POST', `web-builder/v2/documents/${input.postId}/live-frame`, {
      json: {
        url: input.url,
        title: input.title ?? '',
        shell: input.shell,
        page: input.page.map(wire),
        header: input.header.map((s) => wire({ ...s, content: { ...s.content, html: prefixIds(s.content.html ?? '', 'h:') } })),
        footer: input.footer.map((s) => wire({ ...s, content: { ...s.content, html: prefixIds(s.content.html ?? '', 'f:') } })),
        css: { kit: input.kitCss, local: input.localCss, edit: input.editCss },
        hook: GL_HOOK,
      },
    })
    return { src: r.data.url }
  } catch (e) {
    if (isRestError(e) && (e.status === 404 && /no_route/.test(e.code))) return { srcdoc: liveDoc(input, editorNonce()) }
    throw e
  }
}

/** Loses every WebGL context the frame's design created (the browser keeps only ~16 alive). */
function releaseContexts(win: Window | null | undefined): void {
  let list: unknown
  try {
    list = win && (win as unknown as Record<string, unknown>)[GL_KEY]
  } catch {
    return
  }
  if (!Array.isArray(list)) return
  for (const gl of list as WebGLRenderingContext[]) {
    try {
      if (!gl.isContextLost()) gl.getExtension('WEBGL_lose_context')?.loseContext()
    } catch {
      /* a context of a closed document */
    }
  }
  list.length = 0
}

/** Loads source into frame after releasing its current document; resolves with the new document. */
export function loadLive(frame: HTMLIFrameElement, source: LiveSource): Promise<Document | null> {
  releaseContexts(frame.contentWindow)
  return new Promise((resolve) => {
    const done = () => {
      frame.removeEventListener('load', done)
      resolve(frame.contentDocument)
    }
    frame.addEventListener('load', done)
    if (source.src !== undefined) {
      frame.removeAttribute('srcdoc')
      if (frame.getAttribute('src') === source.src) frame.contentWindow?.location.replace(source.src)
      else frame.src = source.src
    } else {
      frame.removeAttribute('src')
      frame.srcdoc = source.srcdoc
    }
  })
}

const STYLE_KEYS = ['kit', 'local', 'edit'] as const

/** Writes the editor stylesheets (created when missing) and keeps them at the end of the head. */
export function setLiveStyles(doc: Document, css: { kit?: string; local?: string; edit?: string }): void {
  const head = doc.head ?? doc.documentElement
  if (!head) return
  for (const k of STYLE_KEYS) {
    let el = head.querySelector(`style[data-wb-${k}]`)
    if (!el) {
      el = doc.createElement('style')
      el.setAttribute(`data-wb-${k}`, '')
      head.appendChild(el)
    }
    const v = css[k]
    if (v !== undefined && el.textContent !== v) el.textContent = v
  }
  // Last, in this order (a design script may have added stylesheets after them).
  const last = STYLE_KEYS.map((k) => head.querySelector(`style[data-wb-${k}]`)!)
  if (head.lastElementChild !== last[2] || last[1].nextElementSibling !== last[2] || last[0].nextElementSibling !== last[1]) for (const el of last) head.appendChild(el)
}

/**
 * Keeps the frame on the page: a click on a link or a form submission that the design does not
 * handle itself (not defaultPrevented once every listener ran) and that would load another
 * document in the frame is stopped; onLeave gets its URL (the canvas may offer to open it).
 * Same-document links (#hash) and links opening another window work as on the site.
 */
export function guardLive(doc: Document, onLeave?: (url: string) => void): () => void {
  const win = doc.defaultView
  if (!win) return () => {}
  const here = () => win.location.href.split('#')[0]
  const leaves = (url: string, target: string) => {
    if (target && target !== '_self' && target !== '_parent' && target !== '_top') return false
    try {
      const u = new URL(url, doc.baseURI)
      if (u.protocol === 'javascript:') return false
      return u.href.split('#')[0] !== here() || !u.hash
    } catch {
      return false
    }
  }
  const click = (e: Event) => {
    if (e.defaultPrevented) return
    const a = (e.target as Element | null)?.closest?.('a[href], area[href]') as HTMLAnchorElement | null
    if (!a || a.hasAttribute('download')) return
    if (leaves(a.href, a.target)) {
      e.preventDefault()
      onLeave?.(a.href)
    }
  }
  const submit = (e: Event) => {
    if (e.defaultPrevented) return
    const f = e.target as HTMLFormElement
    if (f.method === 'dialog') return
    e.preventDefault()
    onLeave?.(f.action)
  }
  // Bubble phase on the window: after the design's own listeners on the document and its elements.
  win.addEventListener('click', click)
  win.addEventListener('submit', submit)
  return () => {
    win.removeEventListener('click', click)
    win.removeEventListener('submit', submit)
  }
}

/** Frees the frame's WebGL contexts and blanks it (before the frame element goes away). */
export function releaseLive(frame: HTMLIFrameElement): void {
  releaseContexts(frame.contentWindow)
  try {
    frame.contentWindow?.location.replace('about:blank')
  } catch {
    /* gone already */
  }
}

/** The attributes of el that LiveKeep keeps: class, style and data-* (unless keys names them). */
function keepAttrs(el: Element | null, keys?: string[]): Record<string, string> | null {
  if (!el) return null
  const out: Record<string, string> = {}
  for (const a of Array.from(el.attributes)) {
    const want = keys ? keys.includes(a.name) : a.name === 'class' || a.name === 'style' || a.name.startsWith('data-')
    if (want) out[a.name] = a.value
  }
  return out
}

function putAttrs(el: Element | null, want: Record<string, string> | null, keys?: string[]) {
  if (!el || !want) return
  for (const a of Array.from(el.attributes)) {
    const kept = keys ? keys.includes(a.name) : a.name === 'class' || a.name === 'style' || a.name.startsWith('data-')
    if (kept && !(a.name in want)) el.removeAttribute(a.name)
  }
  for (const [n, v] of Object.entries(want)) if (el.getAttribute(n) !== v) el.setAttribute(n, v)
}

/**
 * The state of the frame worth keeping across a reload: the scroll position and the <html> and
 * <body> attributes the design's scripts set (a dark-mode toggle's class or data-theme; keys
 * narrows them to the names listed). localStorage and cookies outlive the reload by themselves.
 */
export function captureLive(frame: HTMLIFrameElement, keys?: string[]): LiveKeep {
  const w = frame.contentWindow
  const d = frame.contentDocument
  return { x: w?.scrollX ?? 0, y: w?.scrollY ?? 0, html: keepAttrs(d?.documentElement ?? null, keys), body: keepAttrs(d?.body ?? null, keys) }
}

/** Puts kept state back (after the new document loaded and its scripts ran). */
export function restoreLive(frame: HTMLIFrameElement, keep: LiveKeep, keys?: string[]): void {
  const d = frame.contentDocument
  putAttrs(d?.documentElement ?? null, keep.html, keys)
  putAttrs(d?.body ?? null, keep.body, keys)
  frame.contentWindow?.scrollTo({ left: keep.x, top: keep.y, behavior: 'instant' as ScrollBehavior })
}

/** Reloads the frame with source, keeping its scroll position and theme state. */
export async function reloadPreserving(frame: HTMLIFrameElement, source: LiveSource, keys?: string[]): Promise<Document | null> {
  const keep = captureLive(frame, keys)
  const d = await loadLive(frame, source)
  restoreLive(frame, keep, keys)
  return d
}

/** The viewport each canvas device stands for (a 1440 × 900 laptop, an iPad, an iPhone 14/15). */
export const LIVE_VIEWPORT = { desktop: { w: 1440, h: 900 }, tablet: { w: 768, h: 1024 }, mobile: { w: 390, h: 844 } } as const

/**
 * The live frame's geometry in a panel of avail (CSS px): the frame element is the device's
 * viewport (width × height: innerWidth, innerHeight and 100vh are the device's, not the panel's)
 * scaled by `zoom` with a CSS transform (never CSS zoom, which would change the design's
 * devicePixelRatio and layout), fitted to the panel's width and height (at most 1).
 */
export function liveGeometry(device: keyof typeof LIVE_VIEWPORT, avail: { w: number; h: number }): { width: number; height: number; zoom: number } {
  const v = LIVE_VIEWPORT[device]
  const zoom = Math.min(1, Math.max(0.1, Math.min(avail.w / v.w, avail.h / v.h)))
  return { width: v.w, height: v.h, zoom }
}

export function liveShellOf(themeParts: boolean, layout: string | undefined, template: boolean): LiveShell {
  if (template) return 'part'
  return themeParts && layout !== 'canvas' ? 'theme' : 'canvas'
}

/* ---------- Patching the live DOM (applyToLive) ---------- */

class Reload extends Error {}
const need = (reason: string): never => {
  throw new Reload(reason)
}

interface Src {
  t: Tree
  el: El
}

/** Parses markup in the live document's parser, inertly (a template's content). */
function fragment(doc: Document, markup: string, foreign: '' | 'svg' | 'math'): Node[] {
  const tpl = doc.createElement('template')
  tpl.innerHTML = foreign ? `<${foreign}>${markup}</${foreign}>` : markup
  const root = foreign ? tpl.content.firstElementChild! : tpl.content
  return Array.from(root.childNodes).map((n) => doc.importNode(n, true))
}

const foreignOf = (el: El): '' | 'svg' | 'math' => {
  if (!el.foreign) return ''
  for (let p = el.parent; p; p = p.parent) if (p.tag === 'svg' || p.tag === 'math') return p.tag as 'svg' | 'math'
  return 'svg'
}

/** The attributes of a source element as the live document's parser reads them. */
function attrsOf(doc: Document, t: Tree, el: El): Map<string, string> {
  const open = t.src.slice(el.start, el.openEnd).replace(/\/>$/, '>')
  const n = fragment(doc, open + (el.tag === 'template' ? '' : `</${el.tag}>`), foreignOf(el))[0]
  const out = new Map<string, string>()
  if (n && n.nodeType === 1) for (const a of Array.from((n as Element).attributes)) out.set(a.name, a.value)
  return out
}

const tokens = (v: string | undefined) => new Set((v ?? '').split(/[\t\n\f\r ]+/).filter(Boolean))

function styleDecls(doc: Document, v: string | undefined): Map<string, [string, string]> {
  const m = new Map<string, [string, string]>()
  if (!v) return m
  const d = doc.createElement('div')
  d.setAttribute('style', v)
  for (let i = 0; i < d.style.length; i++) {
    const p = d.style.item(i)
    m.set(p, [d.style.getPropertyValue(p), d.style.getPropertyPriority(p)])
  }
  return m
}

/** Applies the attribute delta old → new to live element n (class and style per token / property). */
function attrDelta(doc: Document, n: Element, was: Map<string, string>, now: Map<string, string>): boolean {
  let changed = false
  for (const name of new Set([...was.keys(), ...now.keys()])) {
    const a = was.get(name)
    const b = now.get(name)
    if (a === b) continue
    changed = true
    if (name === 'class') {
      const ta = tokens(a)
      const tb = tokens(b)
      for (const x of ta) if (!tb.has(x)) n.classList.remove(x)
      for (const x of tb) if (!ta.has(x)) n.classList.add(x)
    } else if (name === 'style' && (n as HTMLElement).style) {
      const da = styleDecls(doc, a)
      const db = styleDecls(doc, b)
      const st = (n as HTMLElement).style
      for (const p of da.keys()) if (!db.has(p)) st.removeProperty(p)
      for (const [p, [v, pr]] of db) {
        const o = da.get(p)
        if (!o || o[0] !== v || o[1] !== pr) st.setProperty(p, v, pr)
      }
    } else if (b === undefined) n.removeAttribute(name)
    else n.setAttribute(name, b)
  }
  return changed
}

const hasScript = (el: El) => {
  const walk = (ns: SrcNode[]): boolean => ns.some((k) => k.type === 'el' && (k.tag === 'script' || walk(k.children)))
  return el.tag === 'script' || walk(el.children)
}

const scriptsOf = (t: Tree) =>
  t.all
    .filter((e) => e.tag === 'script')
    .map((e) => outer(t, e))
    .sort()
    .join('\u0000')

interface Ctx {
  doc: Document
  old: Map<string, Src>
  live: Map<string, Element[]>
  touched: Set<Element>
}

/** The live node standing for source element k (unique), or null. */
function liveOf(ctx: Ctx, k: El): Element | null {
  const id = wbc(k)
  if (id === null) return null
  const l = ctx.live.get(id)
  return l && l.length === 1 ? l[0] : null
}

/**
 * Brings live element n from source element `was` to `now` (same tag): attributes by delta; the
 * children when the inner markup changed.
 */
function patchEl(ctx: Ctx, n: Element, wt: Tree, was: El, nt: Tree, now: El): Element {
  if (outer(wt, was) === outer(nt, now)) return n
  const oa = attrsOf(ctx.doc, wt, was)
  const na = attrsOf(ctx.doc, nt, now)
  if (attrDelta(ctx.doc, n, oa, na)) ctx.touched.add(n)
  // Copies the design made of the element (a slider's clones) get the same attribute change.
  const id = wbc(now)
  for (const c of id !== null ? (ctx.live.get(id) ?? []) : []) if (c !== n && attrDelta(ctx.doc, c, oa, na)) ctx.touched.add(c)
  const wi = wt.src.slice(was.openEnd, was.closeStart)
  const ni = nt.src.slice(now.openEnd, now.closeStart)
  if (wi === ni) return n
  if (now.tag === 'script') need('a script changed')
  if (sameShape(wt, was, nt, now)) {
    // Only what is inside the children changed: each child is patched where it is on the live
    // page (by data-wbc), whatever the design's scripts did around it (a slider's track).
    const ok = (k: SrcNode): k is El => k.type === 'el'
    const nk = now.children.filter(ok)
    was.children.filter(ok).forEach((k, i) => {
      const id = wbc(k)
      const copies = id !== null ? (ctx.live.get(id) ?? []) : []
      if (!copies.length) need('an element is not on the live page')
      patchEl(ctx, copies[0], wt, k, nt, nk[i])
    })
    return n
  }
  if (RAW.has(now.tag) || now.tag === 'svg' || now.tag === 'math' || now.foreign) {
    // Raw text, a template, an svg: rebuilt from the new markup as one element.
    const fresh = fragment(ctx.doc, outer(nt, now), foreignOf(now)).find((x) => x.nodeType === 1) as Element | undefined
    if (!fresh || fresh.querySelector('script')) need('new markup holds a script')
    n.replaceWith(fresh!)
    ctx.touched.add(fresh!)
    return fresh!
  }
  children(ctx, n, Array.from(n.childNodes), null, wt, was.children, nt, now.children, foreignOf(now))
  return n
}

/**
 * Whether two versions of an element have the same children (texts equal, elements with the same
 * tag and id in the same order, each with its own id): only their insides changed.
 */
function sameShape(wt: Tree, was: El, nt: Tree, now: El): boolean {
  if (was.children.length !== now.children.length || RAW.has(now.tag)) return false
  return was.children.every((a, i) => {
    const b = now.children[i]
    if (a.type !== b.type) return false
    if (a.type !== 'el' || b.type !== 'el') return wt.src.slice(a.start, a.end) === nt.src.slice(b.start, b.end)
    return a.tag === b.tag && wbc(a) !== null && wbc(a) === wbc(b) && a.foreign === b.foreign
  })
}

/**
 * Rebuilds the children `cur` of live node parent (before anchor) from source kids `oldKids` →
 * `newKids`. The live children must still be the old source's (the design's scripts did not
 * restructure them), else the frame is reloaded.
 */
function children(ctx: Ctx, parent: Node, cur: Node[], anchor: Node | null, wt: Tree, oldKids: SrcNode[], nt: Tree, newKids: SrcNode[], foreign: '' | 'svg' | 'math'): void {
  // 1. The live children are the old source's: same elements in the same order (comments aside).
  const liveEls = cur.filter((x) => x.nodeType === 1) as Element[]
  const oldEls = oldKids.filter((k): k is El => k.type === 'el')
  if (liveEls.length !== oldEls.length) need("the design's scripts changed this element's children")
  oldEls.forEach((k, i) => {
    const l = liveEls[i]
    const id = wbc(k)
    if (l.localName !== k.tag && l.localName.toLowerCase() !== k.tag) need("the design's scripts changed this element's children")
    if (id !== null && l.getAttribute('data-wbc') !== id) need("the design's scripts changed this element's children")
  })
  const byOld = new Map<El, Element>(oldEls.map((k, i) => [k, liveEls[i]]))
  // 2. The new children: kept live nodes (patched), or new nodes from the new markup.
  const out: Node[] = []
  let pending = ''
  const flush = () => {
    if (!pending) return
    const nodes = fragment(ctx.doc, pending, foreign)
    for (const x of nodes) if (x.nodeType === 1 && ((x as Element).localName === 'script' || (x as Element).querySelector('script'))) need('new markup holds a script')
    for (const x of nodes) if (x.nodeType === 1) ctx.touched.add(x as Element)
    out.push(...nodes)
    pending = ''
  }
  const oldTexts = oldKids.filter((k) => k.type !== 'el')
  const liveTexts = cur.filter((x) => x.nodeType !== 1)
  let ti = 0
  for (const k of newKids) {
    if (k.type !== 'el') {
      // A text node the old source had at this place with the same text keeps its live node.
      const src = nt.src.slice(k.start, k.end)
      const o = oldTexts[ti]
      const l = liveTexts[ti]
      ti++
      if (o && l && wt.src.slice(o.start, o.end) === src && l.parentNode === parent) {
        flush()
        out.push(l)
      } else pending += src
      continue
    }
    const id = wbc(k)
    const src = id !== null ? ctx.old.get(id) : undefined
    let n: Element | null = null
    if (src) {
      // An element the old markup had (here or moved from elsewhere).
      const local = [...byOld.entries()].find(([e]) => e === src.el)?.[1]
      n = local ?? liveOf(ctx, src.el)
      if (!n) need('an element is not on the live page')
      if (src.el.tag !== k.tag) n = null
    }
    if (n && src) {
      flush()
      out.push(patchEl(ctx, n, src.t, src.el, nt, k)) // a raw / svg element comes back rebuilt
      continue
    }
    if (hasScript(k)) need('new markup holds a script')
    pending += outer(nt, k)
  }
  flush()
  // 3. Place them: old nodes that are not kept go, the new list in order.
  const keep = new Set(out)
  for (const x of cur) if (!keep.has(x) && x.parentNode === parent) parent.removeChild(x)
  let ref = anchor && anchor.parentNode === parent ? anchor : null
  for (let i = out.length - 1; i >= 0; i--) {
    const x = out[i]
    if (x.parentNode !== parent || x.nextSibling !== ref) parent.insertBefore(x, ref)
    ref = x
  }
}

/**
 * Patches the live document for an edit: prev / next are the edited doc's section id →
 * content.html before and after. Elements are matched by data-wbc; attributes change by delta (a
 * class or inline style the design's script set stays), text and children are rebuilt where the
 * source changed, elements moved keep their live node (a running canvas keeps running). The live
 * DOM is never read back into the source (K5).
 *
 * A reload is answered (nothing is changed then) when: a section was added or removed, a script
 * was added, removed or changed, new markup holds a script, an element the edit touches is not on
 * the live page or is there more than once, or the design's scripts restructured the children the
 * edit changes. Head, wrappers and document attributes are not here: the canvas reloads for those.
 */
export function applyToLive(doc: Document, prev: Record<string, string>, next: Record<string, string>): LiveApply {
  const changed = Object.keys(next).filter((id) => prev[id] !== next[id])
  if (!changed.length) return { reload: false, touched: [] }
  if (Object.keys(prev).some((id) => !(id in next)) || changed.some((id) => prev[id] === undefined)) return { reload: true, reason: 'sections were added or removed' }
  const trees = changed.map((id) => ({ id, wt: parse(prev[id]), nt: parse(next[id]) }))
  for (const { wt, nt } of trees) if (scriptsOf(wt) !== scriptsOf(nt)) return { reload: true, reason: 'a script was added, removed or changed' }
  const ctx: Ctx = { doc, old: new Map(), live: new Map(), touched: new Set() }
  for (const { wt } of trees)
    for (const el of wt.all) {
      const w = wbc(el)
      if (w !== null && !ctx.old.has(w)) ctx.old.set(w, { t: wt, el })
    }
  for (const el of Array.from(doc.querySelectorAll('[data-wbc]'))) {
    const w = el.getAttribute('data-wbc')!
    const l = ctx.live.get(w)
    if (l) l.push(el)
    else ctx.live.set(w, [el])
  }
  try {
    // Plan on a check pass first: every section's top-level live nodes must be found.
    const spots = trees.map(({ id, wt, nt }) => {
      const tops = elementKids(wt.root)
      if (!tops.length) need(`section ${id} has no element on the live page`)
      const nodes = tops.map((k) => liveOf(ctx, k))
      if (nodes.some((n) => !n)) need('a section element is not on the live page (or is there more than once)')
      const parent = nodes[0]!.parentNode!
      if (nodes.some((n) => n!.parentNode !== parent)) need("the section's elements are not side by side on the live page")
      // The section's live nodes: from its first to its last element, with the text around them.
      const first = nodes[0]!
      const last = nodes[nodes.length - 1]!
      const cur: Node[] = []
      for (let x: Node | null = first; x; x = x.nextSibling) {
        cur.push(x)
        if (x === last) break
      }
      return { wt, nt, parent, cur, anchor: last.nextSibling }
    })
    // Text before the first / after the last element of a section is the shared wrapper's: only
    // the source between them is compared.
    const inner = (t: Tree) => {
      const ks = t.root.children
      const els = ks.map((k, i) => (k.type === 'el' ? i : -1)).filter((i) => i >= 0)
      return els.length ? ks.slice(els[0], els[els.length - 1] + 1) : []
    }
    // A dry run on clones is not possible (live nodes); validate what can be validated up front.
    for (const s of spots) {
      const oldEls = inner(s.wt).filter((k) => k.type === 'el')
      const liveEls = s.cur.filter((x) => x.nodeType === 1)
      if (oldEls.length !== liveEls.length) need("the design's scripts added elements between this section's elements")
    }
    // Deep validation before any write: each changed element's live counterpart exists and its
    // children still match the old source where they change.
    for (const { wt, nt } of trees) validate(ctx, wt, nt)
    for (const s of spots) children(ctx, s.parent, s.cur, s.anchor, s.wt, inner(s.wt), s.nt, inner(s.nt), '')
  } catch (e) {
    if (e instanceof Reload) return { reload: true, reason: e.message }
    throw e
  }
  return { reload: false, touched: [...ctx.touched] }
}

/** Checks before writing that every element whose markup changed is on the live page once and still has the old source's children. */
function validate(ctx: Ctx, wt: Tree, nt: Tree): void {
  for (const k of nt.all) {
    const id = wbc(k)
    if (id === null) continue
    const src = ctx.old.get(id)
    if (!src) continue
    const a = outer(src.t, src.el)
    const b = outer(nt, k)
    if (a === b) continue
    const live = ctx.live.get(id) ?? []
    if (!live.length) need('an element is not on the live page')
    const wi = src.t.src.slice(src.el.openEnd, src.el.closeStart)
    const ni = nt.src.slice(k.openEnd, k.closeStart)
    if (wi === ni) continue // attributes only: fine even for copies (applied to the first; see children)
    if (sameShape(src.t, src.el, nt, k)) continue // its children are checked themselves
    if (live.length > 1) need('the element is on the live page more than once (a copy the design made)')
    if (RAW.has(k.tag) || k.foreign || k.tag === 'svg' || k.tag === 'math') continue
    const l = live[0]
    const oldEls = src.el.children.filter((x): x is El => x.type === 'el')
    const liveEls = Array.from(l.children)
    if (oldEls.length !== liveEls.length || oldEls.some((x, i) => liveEls[i].localName.toLowerCase() !== x.tag || (wbc(x) !== null && liveEls[i].getAttribute('data-wbc') !== wbc(x))))
      need("the design's scripts changed this element's children")
  }
  void wt
}
