/**
 * In-place text editing inside the element editor's frame: the element becomes contenteditable in
 * the frame document (its own selection and execCommand), and the result is written back through
 * the element operations when the user leaves it (Enter, a click elsewhere) — Escape cancels.
 */
import { freshId } from './ops'

export interface TextEdit {
  id: string
  el: HTMLElement
  /** Ends the session (commit: write the change). */
  end(commit: boolean): void
}

/** Elements whose Enter starts a new line instead of finishing the edit. */
const MULTILINE = /^(P|LI|DIV|BLOCKQUOTE|TD|DD|PRE)$/

/** The edited markup made clean: no editor attributes, element ids unique. */
export function cleanEdited(root: HTMLElement): string {
  const seen = new Set<string>()
  for (const el of Array.from(root.querySelectorAll('*'))) {
    el.removeAttribute('contenteditable')
    const id = el.getAttribute('data-wbc')
    if (id === null) continue
    // The browser copies attributes when it splits an element (Enter in a list item): keep ids unique.
    if (seen.has(id)) el.setAttribute('data-wbc', freshId())
    else seen.add(id)
  }
  return root.innerHTML
}

/**
 * Starts editing el. plain: plain text only (text a script drew differently from the source).
 * onDone gets the new inner markup (null: nothing changed or cancelled) and the plain text.
 */
export function startTextEdit(el: HTMLElement, plain: boolean, onDone: (html: string | null, text: string) => void): TextEdit {
  const doc = el.ownerDocument
  const before = el.innerHTML
  el.setAttribute('contenteditable', plain ? 'plaintext-only' : 'true')
  try {
    doc.execCommand('styleWithCSS', false, 'false')
  } catch {
    /* not supported */
  }
  el.focus({ preventScroll: true })
  let finished = false
  const end = (commit: boolean) => {
    if (finished) return
    finished = true
    el.removeEventListener('keydown', key, true)
    el.removeEventListener('blur', blur)
    if (!commit) el.innerHTML = before
    el.removeAttribute('contenteditable')
    const changed = commit && el.innerHTML !== before
    onDone(changed ? cleanEdited(el) : null, el.textContent ?? '')
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
  return { id: el.getAttribute('data-wbc') ?? '', el, end }
}

/** Formatting commands of the in-frame text toolbar. */
export function format(doc: Document, cmd: 'bold' | 'italic' | 'link' | 'unlink' | 'clear', value?: string) {
  if (cmd === 'clear') doc.execCommand('removeFormat')
  else if (cmd === 'link') {
    if (value) doc.execCommand('createLink', false, value)
  } else doc.execCommand(cmd)
}
