/**
 * In-place text editing inside the element editor's frame: the element becomes contenteditable in
 * the frame document (its own selection and execCommand), and the result is written back through
 * the element operations when the user leaves it (Enter, a click elsewhere) — Escape cancels.
 *
 * The result is written from the SOURCE, not from the edited DOM's markup (finding 17): before the
 * edit the edited DOM's nodes are paired with the source nodes they show (mapSource), and the new
 * inner markup keeps each paired element's own source tags byte for byte (its on* handlers, ids,
 * attributes the static frame's neutralised copy does not have), paired text unchanged keeps its
 * source spelling, code inside (svg, script, a kept placeholder) is copied from the source as it
 * is; only what the edit made (bold, a link, a line break) is written new, from a short allowlist
 * and without attributes but a safe link address. The Content panel's text box uses the same
 * write-back (ContentPanel.RichPanel).
 */
import { RAW, VOID, attr, attrEdit, decode, escAttr, escText, outer, type El, type Node as SrcNode, type Tree } from './html'
import { urlOK } from './attrs'
import { BLOCKED_URL } from './neutral'
import { freshId } from './ops'

export interface TextEdit {
  id: string
  el: HTMLElement
  /** Ends the session (commit: write the change). */
  end(commit: boolean): void
  /** Runs an edit of the user's (a toolbar command): its DOM changes are the user's, not the design's. */
  user<T>(f: () => T): T
}

/**
 * Watches an element being edited in a frame where the design's scripts run (Live, K5): every
 * change of its content (child nodes, text) is the user's when it happens between a beforeinput
 * and its input event, seen first on the frame's window (capture), or inside user(); any other
 * change — a script typing a counter, re-rendering the element, adding a node — makes the edit
 * foreign, and the caller does not save it (the DOM then holds what the design wrote).
 */
export function watchEdit(el: HTMLElement): { user<T>(f: () => T): T; foreign(): boolean; stop(): void } {
  const win = el.ownerDocument.defaultView
  const MO = (win as unknown as { MutationObserver?: typeof MutationObserver } | null)?.MutationObserver ?? (typeof MutationObserver !== 'undefined' ? MutationObserver : null)
  if (!MO || !win) return { user: (f) => f(), foreign: () => false, stop: () => {} }
  let tainted = false
  const mo = new MO(() => {
    // Delivered after a task: whatever is still pending was not taken at a user boundary.
    tainted = true
  })
  mo.observe(el, { childList: true, characterData: true, subtree: true })
  const check = () => {
    if (mo.takeRecords().length) tainted = true
  }
  const take = () => void mo.takeRecords()
  win.addEventListener('beforeinput', check, true)
  win.addEventListener('input', take, true)
  return {
    user<T>(f: () => T): T {
      check()
      try {
        return f()
      } finally {
        take()
      }
    },
    foreign() {
      check()
      return tainted
    },
    stop() {
      check()
      mo.disconnect()
      win.removeEventListener('beforeinput', check, true)
      win.removeEventListener('input', take, true)
    },
  }
}

/** DOM nodes → the source nodes they show. */
export type SrcMap = Map<Node, SrcNode>

/** Elements an edit may add (written without attributes, a link with its address only). */
export const EDIT_TAGS = new Set(['b', 'strong', 'i', 'em', 'u', 's', 'a', 'br', 'span', 'code', 'sub', 'sup', 'p', 'div', 'li', 'ul', 'ol'])

/** Elements whose Enter starts a new line instead of finishing the edit. */
const MULTILINE = /^(P|LI|DIV|BLOCKQUOTE|TD|DD|PRE)$/

/** Marks a DOM node standing for a source node that is kept as written (Content panel). */
export const KEEP_ATTR = 'data-wb-keep'

/**
 * Pairs the DOM children of `dom` with the source children of `el`, in order: an element with the
 * next source element of the same tag, a text with the source text right there when they read the
 * same, a comment with a comment.
 */
export function mapSource(dom: Node, t: Tree, el: El, map: SrcMap = new Map()): SrcMap {
  const src = el.children.filter((c) => c.type !== 'other')
  let i = 0
  for (let n = dom.firstChild; n; n = n.nextSibling) {
    if (n.nodeType === 1) {
      const tag = (n as Element).localName.toLowerCase()
      let j = i
      while (j < src.length && !(src[j].type === 'el' && (src[j] as El).tag === tag)) j++
      if (j < src.length) {
        map.set(n, src[j])
        i = j + 1
        const se = src[j] as El
        if (!se.foreign && !RAW.has(se.tag) && se.tag !== 'svg' && se.tag !== 'math') mapSource(n, t, se, map)
      }
    } else if (n.nodeType === 3) {
      while (i < src.length && src[i].type === 'comment') i++
      if (i < src.length && src[i].type === 'text') {
        if (decode(t.src.slice(src[i].start, src[i].end)) === (n as Text).data) map.set(n, src[i])
        i++
      }
    } else if (n.nodeType === 8) {
      if (i < src.length && src[i].type === 'comment') map.set(n, src[i++])
    }
  }
  return map
}

/** The new inner markup of an edited DOM element, written from the source (see the file comment). */
export function writeBack(root: Node, t: Tree, map: SrcMap): string {
  const kids = (n: Node) => {
    let out = ''
    for (let k = n.firstChild; k; k = k.nextSibling) out += one(k)
    return out
  }
  const one = (k: Node): string => {
    const s = map.get(k)
    if (k.nodeType === 3) {
      const data = (k as Text).data
      if (s && s.type === 'text' && decode(t.src.slice(s.start, s.end)) === data) return t.src.slice(s.start, s.end)
      return escText(data)
    }
    if (k.nodeType === 8) return s ? t.src.slice(s.start, s.end) : ''
    if (k.nodeType !== 1) return ''
    const e = k as Element
    const tag = e.localName.toLowerCase()
    if (e.hasAttribute(KEEP_ATTR)) return s ? outer(t, s) : ''
    if (s && s.type === 'el') {
      if (s.foreign || RAW.has(s.tag) || s.tag === 'svg' || s.tag === 'math' || VOID.has(s.tag)) return outer(t, s)
      if (s.tag === tag) {
        let open = t.src.slice(s.start, s.openEnd)
        // A link the edit pointed elsewhere (the Link button on a link): its new safe address.
        const href = tag === 'a' ? e.getAttribute('href') : null
        if (href !== null && href !== BLOCKED_URL && href !== attr(s, 'href') && urlOK(href)) {
          const [ed] = attrEdit(t, s, 'href', href)
          open = t.src.slice(s.start, ed.start) + ed.text + t.src.slice(ed.end, s.openEnd)
        }
        return open + kids(e) + (s.closed ? t.src.slice(s.closeStart, s.end) : `</${s.tag}>`)
      }
    }
    // A copy of a source element (Enter splits a list item or a paragraph): its source tag, a new id.
    const id = e.getAttribute('data-wbc')
    const orig = id !== null ? t.byId.get(id) : undefined
    if (orig && orig.tag === tag && !orig.foreign && !RAW.has(tag) && !VOID.has(tag)) {
      const [ed] = attrEdit(t, orig, 'data-wbc', freshId())
      return t.src.slice(orig.start, ed.start) + ed.text + t.src.slice(ed.end, orig.openEnd) + kids(e) + `</${tag}>`
    }
    if (!EDIT_TAGS.has(tag)) return kids(e)
    if (tag === 'br') return '<br>'
    const href = tag === 'a' ? e.getAttribute('href') : null
    const attrs = href && urlOK(href) ? ` href="${escAttr(href)}"` : ''
    return `<${tag}${attrs}>${kids(e)}</${tag}>`
  }
  return kids(root)
}

/**
 * Starts editing el. plain: plain text only (text a script drew differently from the source).
 * write: the new inner markup of el (from the source, writeBack). onDone gets it (null: nothing
 * changed or cancelled) and the plain text; foreign (live: the frame runs the design's scripts,
 * watchEdit): the design changed the element during the edit — nothing is saved, the element gets
 * back its nodes of before, and onDone's foreign is true.
 */
export function startTextEdit(el: HTMLElement, plain: boolean, at: { x: number; y: number } | null, write: (el: HTMLElement) => string, onDone: (html: string | null, text: string, foreign?: boolean) => void, live = false): TextEdit {
  const doc = el.ownerDocument
  const before = el.innerHTML
  const watch = live ? watchEdit(el) : null
  // Escape puts the nodes themselves back (never markup parsed again).
  const saved = Array.from(el.childNodes).map((n) => n.cloneNode(true))
  el.setAttribute('contenteditable', plain ? 'plaintext-only' : 'true')
  try {
    doc.execCommand('styleWithCSS', false, 'false')
  } catch {
    /* not supported */
  }
  el.focus({ preventScroll: true })
  // The caret goes where the user double-clicked, else at the end of the text.
  const sel = doc.getSelection()
  const r = at && 'caretRangeFromPoint' in doc ? doc.caretRangeFromPoint(at.x, at.y) : null
  if (sel) {
    const range = r && el.contains(r.startContainer) ? r : doc.createRange()
    if (range !== r) {
      range.selectNodeContents(el)
      range.collapse(false)
    }
    sel.removeAllRanges()
    sel.addRange(range)
  }
  let finished = false
  const end = (commit: boolean) => {
    if (finished) return
    finished = true
    el.removeEventListener('keydown', key, true)
    el.removeEventListener('blur', blur)
    const foreign = !!watch?.foreign()
    watch?.stop()
    if (!commit || foreign) el.replaceChildren(...saved)
    el.removeAttribute('contenteditable')
    const changed = commit && !foreign && el.innerHTML !== before
    onDone(changed ? write(el) : null, el.textContent ?? '', foreign)
  }
  const key = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      end(false)
    } else if (e.key === 'Enter' && !e.shiftKey && !MULTILINE.test(el.tagName)) {
      e.preventDefault()
      end(true)
    }
  }
  const blur = () => end(true)
  el.addEventListener('keydown', key, true)
  el.addEventListener('blur', blur)
  return { id: el.getAttribute('data-wbc') ?? '', el, end, user: (f) => (watch ? watch.user(f) : f()) }
}

/** Formatting commands of the in-frame text toolbar. */
export function format(doc: Document, cmd: 'bold' | 'italic' | 'link' | 'unlink' | 'clear', value?: string) {
  if (cmd === 'clear') doc.execCommand('removeFormat')
  else if (cmd === 'link') {
    if (value && urlOK(value)) doc.execCommand('createLink', false, value)
  } else doc.execCommand(cmd)
}
