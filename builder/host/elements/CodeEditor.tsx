/**
 * A light code editor without dependencies: a transparent textarea over a coloured copy of its text
 * (a `<pre>` of text nodes and spans built from the tokens of codehl.ts — what is typed is never
 * parsed as markup by the editor's page), a line-number gutter, soft tabs (Tab = 2 spaces,
 * Shift+Tab outdents, Esc leaves the field so keyboard users are not trapped), auto-indent, find
 * (Ctrl/⌘F), Ctrl/⌘Enter to apply, a height that grows to a maximum and then scrolls, and a
 * "larger editor" dialog at about 80% of the window.
 *
 * Big inputs stay responsive: above BIG characters the colouring is computed after a short pause
 * (until then the text is shown plain, aligned with the textarea) and, from WINDOW_AT lines, only
 * the lines near the viewport are coloured.
 */
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, ChevronUp, Maximize2, X } from 'lucide-react'
import { cn } from '@/builder/ui'
import { tc } from './code-tr'
import { highlight, type Cls, type Lang, type Tok } from './codehl'

export const LINE = 16
/** Above this many characters the colouring waits for a pause in typing. */
export const BIG = 40_000
/** From this many lines only the viewport's lines are coloured. */
export const WINDOW_AT = 300

const FONT = 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace'
const METRICS = { fontFamily: FONT, fontSize: 11, lineHeight: `${LINE}px`, tabSize: 2, whiteSpace: 'pre', letterSpacing: 0, padding: '6px 8px', margin: 0, wordBreak: 'normal', overflowWrap: 'normal' } as const

const COLOR: Record<Cls, string> = {
  tag: 'text-sky-300',
  attr: 'text-amber-200',
  str: 'text-emerald-300',
  com: 'text-ink-500',
  kw: 'text-violet-300',
  num: 'text-orange-300',
  ent: 'text-pink-300',
  meta: 'text-ink-400',
}

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || '')

/** Offset of the start of line `n` (0-based) in v (v.length past the last line). */
function lineStart(v: string, n: number): number {
  let at = 0
  for (let k = 0; k < n; k++) {
    const i = v.indexOf('\n', at)
    if (i < 0) return v.length
    at = i + 1
  }
  return at
}

const countLines = (v: string) => {
  let n = 1
  for (let i = v.indexOf('\n'); i >= 0; i = v.indexOf('\n', i + 1)) n++
  return n
}

/** The coloured text: strings and spans, only inside [a, b) when a window is given. */
function paint(v: string, toks: Tok[] | null, win: [number, number] | null): ReactNode[] {
  if (!toks || !toks.length) return [v + '\n']
  const from = win ? lineStart(v, win[0]) : 0
  const to = win ? lineStart(v, win[1] + 1) : v.length
  // First token that ends after `from`.
  let lo = 0
  let hi = toks.length
  while (lo < hi) {
    const m = (lo + hi) >> 1
    if (toks[m].e <= from) lo = m + 1
    else hi = m
  }
  const out: ReactNode[] = []
  let at = 0
  if (from > 0) {
    out.push(v.slice(0, from))
    at = from
  }
  for (let k = lo; k < toks.length && toks[k].s < to; k++) {
    const t = toks[k]
    const s = Math.max(t.s, at)
    const e = Math.min(t.e, to)
    if (e <= s) continue
    if (s > at) out.push(v.slice(at, s))
    out.push(
      <span key={k} className={COLOR[t.c]}>
        {v.slice(s, e)}
      </span>,
    )
    at = e
  }
  out.push(v.slice(at) + '\n')
  return out
}

/** The colouring of `value`: at once for small inputs, after a pause for big ones (null meanwhile). */
function useTokens(value: string, lang: Lang): Tok[] | null {
  const small = value.length <= BIG
  const now = useMemo(() => (small ? highlight(value, lang) : null), [value, lang, small])
  const [late, setLate] = useState<{ v: string; l: Lang; t: Tok[] } | null>(null)
  useEffect(() => {
    if (small) return
    const h = setTimeout(() => setLate({ v: value, l: lang, t: highlight(value, lang) }), 250)
    return () => clearTimeout(h)
  }, [value, lang, small])
  if (small) return now
  return late && late.v === value && late.l === lang ? late.t : null
}

export interface CodeEditorProps {
  value: string
  onChange: (v: string) => void
  language?: Lang
  /** Accessible name of the field. */
  label: string
  name?: string
  readOnly?: boolean
  /** Height in pixels after which the field scrolls. */
  maxHeight?: number
  minRows?: number
  /** Ctrl/⌘+Enter. */
  onApply?: () => void
  /** Called after Esc has taken the focus out of the field. */
  onEscape?: () => void
  /** Offer the larger-editor dialog (default true). */
  popout?: boolean
  /** Name of the dialog. */
  title?: string
  invalid?: boolean
  /** Shown in the dialog's header, beside Close (e.g. an Apply button). */
  extra?: ReactNode
  /** Hide the key hint line. */
  noHint?: boolean
}

/** Replaces v[from, to) by `text` through the textarea (the undo history keeps working where the browser allows). */
function edit(ta: HTMLTextAreaElement, from: number, to: number, text: string, selS: number, selE: number, onChange: (v: string) => void) {
  const before = ta.value
  ta.setSelectionRange(from, to)
  let done = false
  try {
    done = typeof document.execCommand === 'function' && document.execCommand('insertText', false, text)
  } catch {
    done = false
  }
  if (!done || ta.value === before) {
    ta.setRangeText(text, from, to, 'end')
    onChange(ta.value)
  }
  ta.setSelectionRange(selS, selE)
}

export function CodeEditor(p: CodeEditorProps) {
  const { value, onChange, language = 'html', readOnly, label, onApply, onEscape } = p
  const maxHeight = p.maxHeight ?? 280
  const taRef = useRef<HTMLTextAreaElement>(null)
  const scRef = useRef<HTMLDivElement>(null)
  const openRef = useRef<HTMLButtonElement>(null)
  const [big, setBig] = useState(false)
  const [find, setFind] = useState<string | null>(null)
  const [win, setWin] = useState<[number, number]>([0, 120])
  const findRef = useRef<HTMLInputElement>(null)
  const toks = useTokens(value, language)
  const lines = useMemo(() => countLines(value), [value])
  const numbers = useMemo(() => {
    let s = '1'
    for (let i = 2; i <= lines; i++) s += '\n' + i
    return s
  }, [lines])
  const windowed = lines > WINDOW_AT
  const children = useMemo(() => paint(value, toks, windowed ? win : null), [value, toks, windowed, win])

  const syncWin = useCallback(() => {
    const sc = scRef.current
    if (!sc) return
    const q = (n: number) => Math.floor(n / 20) * 20
    const a = Math.max(0, q(sc.scrollTop / LINE) - 40)
    const b = q((sc.scrollTop + (sc.clientHeight || 400)) / LINE) + 60
    setWin((w) => (w[0] === a && w[1] === b ? w : [a, b]))
  }, [])
  useLayoutEffect(() => {
    if (windowed) syncWin()
  }, [windowed, syncWin, lines])

  const matches = useMemo(() => {
    if (!find) return 0
    const q = find.toLowerCase()
    const v = value.toLowerCase()
    let n = 0
    for (let i = v.indexOf(q); i >= 0 && n < 9999; i = v.indexOf(q, i + q.length)) n++
    return n
  }, [find, value])

  const go = (dir: 1 | -1) => {
    const ta = taRef.current
    if (!ta || !find) return
    const v = value.toLowerCase()
    const q = find.toLowerCase()
    const from = dir > 0 ? ta.selectionEnd : ta.selectionStart
    let i = dir > 0 ? v.indexOf(q, from) : v.lastIndexOf(q, Math.max(0, from - 1))
    if (i < 0) i = dir > 0 ? v.indexOf(q) : v.lastIndexOf(q)
    if (i < 0) return
    ta.focus()
    ta.setSelectionRange(i, i + q.length)
    const sc = scRef.current
    if (sc) {
      const y = (value.slice(0, i).split('\n').length - 1) * LINE
      if (y < sc.scrollTop || y > sc.scrollTop + sc.clientHeight - LINE * 2) sc.scrollTop = Math.max(0, y - sc.clientHeight / 2)
    }
  }

  const indent = (shift: boolean) => {
    const ta = taRef.current!
    const v = ta.value
    const s = ta.selectionStart
    const e = ta.selectionEnd
    const multi = s !== e && v.slice(s, e).includes('\n')
    if (!shift && !multi) return edit(ta, s, e, '  ', s + 2, s + 2, onChange)
    const ls = v.lastIndexOf('\n', s - 1) + 1
    const last = e > s && v[e - 1] === '\n' ? e - 1 : e
    let le = v.indexOf('\n', last)
    if (le < 0) le = v.length
    const old = v.slice(ls, le).split('\n')
    const next = old.map((l) => (shift ? (l.startsWith('  ') ? l.slice(2) : l.startsWith(' ') || l.startsWith('\t') ? l.slice(1) : l) : l.length ? '  ' + l : l))
    const d0 = next[0].length - old[0].length
    const total = next.reduce((n, l, i) => n + l.length - old[i].length, 0)
    const ns = Math.max(ls, s + (shift ? Math.max(d0, -(s - ls)) : d0))
    edit(ta, ls, le, next.join('\n'), ns, Math.max(ns, e + total), onChange)
  }

  const newline = () => {
    const ta = taRef.current!
    const v = ta.value
    const s = ta.selectionStart
    const ls = v.lastIndexOf('\n', s - 1) + 1
    const ind = /^[ \t]*/.exec(v.slice(ls, s))![0]
    const text = '\n' + ind
    edit(ta, s, ta.selectionEnd, text, s + text.length, s + text.length, onChange)
  }

  const key = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // The builder's own shortcuts (Delete, arrows, Ctrl+Z …) must not see what is typed here.
    e.stopPropagation()
    const mod = e.ctrlKey || e.metaKey
    if (e.key === 'Escape') {
      e.preventDefault()
      e.currentTarget.blur()
      onEscape?.()
      return
    }
    if (mod && e.key === 'Enter') {
      e.preventDefault()
      onApply?.()
      return
    }
    if (mod && (e.key === 'f' || e.key === 'F')) {
      e.preventDefault()
      setFind((f) => f ?? '')
      setTimeout(() => findRef.current?.focus(), 0)
      return
    }
    if (readOnly) return
    if (e.key === 'Tab' && !mod && !e.altKey) {
      e.preventDefault()
      indent(e.shiftKey)
    } else if (e.key === 'Enter' && !mod && !e.shiftKey && !e.altKey) {
      e.preventDefault()
      newline()
    }
  }

  const keyHint = isMac() ? '⌘' : 'Ctrl'
  return (
    <div className="space-y-1" data-wb-code-editor="">
      <div className="relative">
        {find !== null && (
          <div className="mb-1 flex items-center gap-1" data-wb-code-find="">
            <input
              ref={findRef}
              value={find}
              onChange={(e) => setFind(e.target.value)}
              onKeyDown={(e) => {
                e.stopPropagation()
                if (e.key === 'Enter') {
                  e.preventDefault()
                  go(e.shiftKey ? -1 : 1)
                } else if (e.key === 'Escape') {
                  e.preventDefault()
                  setFind(null)
                  taRef.current?.focus()
                }
              }}
              aria-label={tc('Find')}
              placeholder={tc('Find')}
              spellCheck={false}
              className="h-6 min-w-0 flex-1 rounded border border-white/10 bg-black/30 px-1.5 font-mono text-[11px] text-ink-200"
            />
            <span className="shrink-0 text-[10.5px] text-ink-500" aria-live="polite">
              {find ? (matches ? tc('{n} matches', { n: matches }) : tc('No match')) : ''}
            </span>
            <button type="button" aria-label={tc('Previous match')} onClick={() => go(-1)} className="grid size-6 place-items-center rounded text-ink-400 hover:text-white">
              <ChevronUp size={13} />
            </button>
            <button type="button" aria-label={tc('Next match')} onClick={() => go(1)} className="grid size-6 place-items-center rounded text-ink-400 hover:text-white">
              <ChevronDown size={13} />
            </button>
            <button type="button" aria-label={tc('Close find')} onClick={() => setFind(null)} className="grid size-6 place-items-center rounded text-ink-400 hover:text-white">
              <X size={12} />
            </button>
          </div>
        )}
        {p.popout !== false && (
          <button
            ref={openRef}
            type="button"
            aria-label={tc('Larger editor')}
            title={tc('Larger editor')}
            onClick={() => setBig(true)}
            className="absolute right-2 top-1.5 z-20 grid size-6 place-items-center rounded bg-black/40 text-ink-400 opacity-70 hover:text-white hover:opacity-100"
            data-wb-code-popout=""
          >
            <Maximize2 size={12} />
          </button>
        )}
        <div ref={scRef} onScroll={windowed ? syncWin : undefined} style={{ maxHeight }} className={cn('flex overflow-auto rounded-[5px] border bg-black/30', p.invalid ? 'border-red-400/60' : 'border-white/10 focus-within:border-white/30')} data-wb-code-scroll="">
          <div aria-hidden="true" className="sticky left-0 z-10 shrink-0 select-none bg-[#14161a] text-right text-ink-600" style={{ ...METRICS, paddingLeft: 6, paddingRight: 6 }} data-wb-code-gutter="">
            {numbers}
          </div>
          <div className="relative min-w-0 flex-1" style={{ minHeight: (p.minRows ?? 6) * LINE + 12 }}>
            <pre aria-hidden="true" className="pointer-events-none min-w-max text-ink-200" style={METRICS} data-wb-code-paint="">
              {children}
            </pre>
            <textarea
              ref={taRef}
              value={value}
              name={p.name}
              aria-label={label}
              aria-invalid={p.invalid || undefined}
              readOnly={readOnly}
              spellCheck={false}
              autoCapitalize="off"
              autoComplete="off"
              autoCorrect="off"
              wrap="off"
              onChange={(e) => onChange(e.target.value)}
              onKeyDown={key}
              style={METRICS}
              className="absolute inset-0 size-full resize-none overflow-hidden border-0 bg-transparent text-transparent caret-white outline-none selection:bg-sky-400/30"
            />
          </div>
        </div>
      </div>
      {!p.noHint && (
        <div className="text-[10.5px] text-ink-500">
          {tc('Tab indents · Shift+Tab outdents · Esc leaves the field')}
          {onApply && !readOnly ? ' · ' + tc('{key}+Enter applies', { key: keyHint }) : ''}
        </div>
      )}
      {big && (
        <Dialog
          title={p.title ?? tc('Code editor')}
          extra={p.extra}
          onClose={() => {
            setBig(false)
            setTimeout(() => openRef.current?.focus(), 0)
          }}
        >
          {(close) => <CodeEditor {...p} popout={false} extra={undefined} maxHeight={Math.max(200, Math.floor((typeof window !== 'undefined' ? window.innerHeight : 800) * 0.8) - 130)} minRows={20} onEscape={close} noHint={false} />}
        </Dialog>
      )}
    </div>
  )
}

/** The larger-editor dialog: about 80% of the window, over the editor. */
function Dialog({ title, extra, onClose, children }: { title: string; extra?: ReactNode; onClose: () => void; children: (close: () => void) => ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    ref.current?.querySelector('textarea')?.focus()
  }, [])
  if (typeof document === 'undefined') return null
  return createPortal(
    <div
      className="fixed inset-0 z-[1000] grid place-items-center bg-black/60"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Escape') onClose()
      }}
      data-wb-code-dialog=""
    >
      <div ref={ref} role="dialog" aria-modal="true" aria-label={title} className="flex flex-col gap-2 overflow-hidden rounded-lg border border-white/10 bg-[#101216] p-3 text-ink-200" style={{ width: '80vw', height: '80vh' }}>
        <div className="flex items-center justify-between gap-2">
          <span className="text-[12px] font-semibold">{title}</span>
          <div className="flex items-center gap-2">
            {extra}
            <button type="button" aria-label={tc('Close')} onClick={onClose} className="grid size-6 place-items-center rounded text-ink-400 hover:text-white">
              <X size={14} />
            </button>
          </div>
        </div>
        <div className="min-h-0 flex-1">{children(onClose)}</div>
      </div>
    </div>,
    document.body,
  )
}
