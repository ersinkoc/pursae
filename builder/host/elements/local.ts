/**
 * Local styles of code sections (plan §4.3a; Go: internal/webbuilder/local.go): a section's
 * `local` field holds the element editor's style edits per element (data-wbc id) and device ×
 * state variant, as raw CSS values. On publish FillCode gives each styled element the class
 * e{post}-{id} and prints localCSS with that class; in the editor's frame the same stylesheet is
 * printed with [data-wbc="…"] selectors (same specificity), in one editor-owned <style
 * data-wb-local> that is rewritten on every change (no reload).
 *
 * localCSS must print byte for byte what Go's LocalCSS prints: the vitest suite runs the shared
 * fixtures of internal/webbuilder/testdata/local.
 *
 * Pure functions, no DOM.
 */
import type { Section } from '@/builder/types'

export type Device = '' | 'tablet' | 'mobile'
export type State = '' | 'hover' | 'focus' | 'focus-visible' | 'active'

export interface LocalVariant {
  bp: Device
  st: State
  p: Record<string, string>
}

export interface LocalEntry {
  label?: string
  v: LocalVariant[]
}

export type LocalStyles = Record<string, LocalEntry>

export interface Breakpoints {
  tablet: number
  mobile: number
}

/** A code section with its local styles (the field is not in upstream's Section type). */
export type LocalSection = Section & { local?: LocalStyles }

export const DEFAULT_BREAKPOINTS: Breakpoints = { tablet: 1023, mobile: 639 }
export const DEVICES: Device[] = ['', 'tablet', 'mobile']
export const STATES: State[] = ['', 'hover', 'focus', 'focus-visible', 'active']

const ID_RE = /^[A-Za-z0-9_-]{1,64}$/
const PROP_RE = /^(?:--[A-Za-z0-9_-]{1,80}|-?[a-z][a-z0-9-]{0,62})$/
/** Go's CSSForbiddenTokens (matched after lower-casing and removing whitespace). */
const FORBIDDEN = ['expression(', 'behavior', '-moz-binding', 'javascript:', 'vbscript:', '-o-link', '@import']
const MAX_VALUE = 4096

/** ASCII whitespace trim (Go's localTrim). */
export const trimCss = (s: string) => s.replace(/^[ \t\n\r\f]+|[ \t\n\r\f]+$/g, '')

/** Strings closed, () [] balanced (Go's cssBalanced, without unescaping). */
function balanced(s: string): boolean {
  const stack: string[] = []
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]
    if (ch === '\\') {
      if (i + 1 >= s.length) return false
      i++
    } else if (ch === '"' || ch === "'") {
      let j = i + 1
      while (j < s.length && s[j] !== ch) {
        if (s[j] === '\n') return false
        if (s[j] === '\\') j++
        j++
      }
      if (j >= s.length) return false
      i = j
    } else if (ch === '(' || ch === '[') stack.push(ch)
    else if (ch === ')' || ch === ']') {
      if (stack.pop() !== (ch === ')' ? '(' : '[')) return false
    }
  }
  return stack.length === 0
}

/**
 * Whether name: value is a declaration local styles may hold. The structural rules match Go's
 * LocalDeclOK exactly (name pattern, no ; { } <, no comment, no control characters, balanced);
 * the content rules (forbidden tokens, javascript: URLs) are a subset of Go's sanitizer, which
 * refuses the save when they disagree.
 */
export function declOK(name: string, value: string): boolean {
  if (!PROP_RE.test(name)) return false
  const v = trimCss(value)
  if (!v || v.length > MAX_VALUE || /[<;{}]/.test(v) || v.includes('/*') || v.includes('*/') || v.endsWith('\\')) return false
  for (let i = 0; i < v.length; i++) {
    const c = v.charCodeAt(i)
    if ((c < 0x20 && c !== 9) || c === 0x7f) return false
  }
  if (!balanced(`${name}:${v}`)) return false
  const n = `${name}:${v}`.toLowerCase().replace(/\s+/g, '')
  return !FORBIDDEN.some((f) => n.includes(f))
}

function bpsOf(b: Breakpoints | null | undefined): Breakpoints {
  const out = { ...DEFAULT_BREAKPOINTS, ...(b ?? {}) }
  if (!(out.tablet >= 768 && out.tablet <= 1439)) out.tablet = DEFAULT_BREAKPOINTS.tablet
  if (!(out.mobile >= 390 && out.mobile <= 767) || out.mobile >= out.tablet) out.mobile = DEFAULT_BREAKPOINTS.mobile
  return out
}

/** Byte order of Go's sort.Strings (UTF-16 code units equal bytes for the ASCII ids and names). */
const byBytes = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)

export interface CssOptions {
  /**
   * Editor only: show element `id`'s state `st` without the pointer (the panel edits that state):
   * its :st rules apply always (`:not(._wbf)` has the same specificity as the pseudo-class).
   */
  force?: { id: string; st: State } | null
}

/** The stylesheet of local styles (Go: LocalCSS). */
export function localCSS(local: LocalStyles | undefined, bps: Breakpoints | null | undefined, sel: (id: string) => string, opts: CssOptions = {}): string {
  if (!local) return ''
  const ids = Object.keys(local).filter((id) => ID_RE.test(id)).sort(byBytes)
  if (!ids.length) return ''
  const b = bpsOf(bps)
  let out = ''
  for (const bp of DEVICES) {
    const lines: string[] = []
    for (const id of ids) {
      for (const st of STATES) {
        const p: Record<string, string> = {}
        for (const v of local[id]?.v ?? []) if (v.bp === bp && v.st === st) Object.assign(p, v.p)
        const keys = Object.keys(p).filter((k) => declOK(k, p[k])).sort(byBytes)
        if (!keys.length) continue
        let s = sel(id) + ':not(#_):not(#_)'
        if (st) s += opts.force && opts.force.id === id && opts.force.st === st ? ':not(._wbf)' : ':' + st
        lines.push(s + '{' + keys.map((k) => k + ':' + trimCss(p[k])).join(';') + '}')
      }
    }
    if (!lines.length) continue
    if (!bp) out += lines.join('\n') + '\n'
    else out += `@media (max-width:${bp === 'mobile' ? b.mobile : b.tablet}px){\n${lines.join('\n')}\n}\n`
  }
  return out
}

/** Published selector (Go: LocalSelector). */
export const classSel = (post: string) => (id: string) => `.e${post}-${id}`
/** Frame selector: the element's data-wbc (an attribute selector weighs what a class does). */
export const attrSel = (prefix = '') => (id: string) => `[data-wbc="${(prefix + id).replace(/["\\]/g, '\\$&')}"]`

/* ---------- Reading and writing ---------- */

/** Declarations of one variant (merged when the list repeats it). */
export function variantOf(local: LocalStyles | undefined, id: string, bp: Device, st: State): Record<string, string> {
  const p: Record<string, string> = {}
  for (const v of local?.[id]?.v ?? []) if (v.bp === bp && v.st === st) Object.assign(p, v.p)
  return p
}

/** The devices a device inherits from, nearest first (desktop first: mobile ← tablet ← desktop). */
export const deviceChain = (bp: Device): Device[] => (bp === 'mobile' ? ['mobile', 'tablet', ''] : bp === 'tablet' ? ['tablet', ''] : [''])

/**
 * The local value that applies to prop at device bp and state st, and where it is set: the
 * state's own variants (nearest device first), then the normal state's.
 */
export function localValue(local: LocalStyles | undefined, id: string, bp: Device, st: State, prop: string): { value: string; bp: Device; st: State } | null {
  const states: State[] = st ? [st, ''] : ['']
  for (const s of states) {
    for (const d of deviceChain(bp)) {
      const v = variantOf(local, id, d, s)[prop]
      if (v !== undefined && trimCss(v) !== '') return { value: v, bp: d, st: s }
    }
  }
  return null
}

/** Sets (value) or removes (null / '') declarations of one variant; returns the new styles (pruned). */
export function setDecls(local: LocalStyles | undefined, id: string, bp: Device, st: State, decls: Record<string, string | null>): LocalStyles {
  const next: LocalStyles = { ...(local ?? {}) }
  const entry: LocalEntry = next[id] ? { ...next[id], v: [...next[id].v] } : { v: [] }
  const merged = variantOf(local, id, bp, st)
  for (const [k, v] of Object.entries(decls)) {
    if (v === null || trimCss(v) === '') delete merged[k]
    else merged[k] = v
  }
  const others = entry.v.filter((v) => !(v.bp === bp && v.st === st))
  entry.v = Object.keys(merged).length ? [...others, { bp, st, p: merged }] : others
  // Canonical order: device, then state (stable JSON, the same edits give the same doc).
  entry.v.sort((a, b) => DEVICES.indexOf(a.bp) - DEVICES.indexOf(b.bp) || STATES.indexOf(a.st) - STATES.indexOf(b.st))
  if (entry.v.length) next[id] = entry
  else delete next[id]
  return next
}

/** Which device × state variants of the element hold declarations ("device|state"). */
export function overrides(local: LocalStyles | undefined, id: string): Set<string> {
  const out = new Set<string>()
  for (const v of local?.[id]?.v ?? []) if (Object.keys(v.p).length) out.add(`${v.bp}|${v.st}`)
  return out
}

/** The element's whole local style (copy style), or null. */
export const entryOf = (local: LocalStyles | undefined, id: string): LocalEntry | null => (local?.[id]?.v.length ? JSON.parse(JSON.stringify(local[id])) : null)

/* ---------- Inline style attributes ---------- */

export interface InlineDecl {
  name: string
  value: string
  important: boolean
  /** Range in the attribute value (the declaration and its ";"). */
  start: number
  end: number
}

/** The declarations of a style attribute's value (CSS splitting: strings and () honoured). */
export function parseStyleAttr(style: string): InlineDecl[] {
  const out: InlineDecl[] = []
  let start = 0
  let paren = 0
  const flush = (end: number, next: number) => {
    const text = style.slice(start, end)
    const c = text.indexOf(':')
    if (c > 0) {
      const name = trimCss(text.slice(0, c)).toLowerCase()
      let value = trimCss(text.slice(c + 1))
      const imp = /!\s*important\s*$/i.exec(value)
      if (imp) value = trimCss(value.slice(0, imp.index))
      if (name) out.push({ name, value, important: !!imp, start, end: next })
    }
    start = next
  }
  for (let i = 0; i < style.length; i++) {
    const ch = style[i]
    if (ch === '\\') i++
    else if (ch === '"' || ch === "'") {
      const j = style.indexOf(ch, i + 1)
      i = j < 0 ? style.length : j
    } else if (ch === '(') paren++
    else if (ch === ')') paren = Math.max(0, paren - 1)
    else if (ch === ';' && !paren) flush(i, i + 1)
  }
  if (start < style.length) flush(style.length, style.length)
  return out
}

/** The style attribute without the named declarations (null: nothing left). */
export function withoutDecls(style: string, names: Set<string>): string | null {
  let out = ''
  let last = 0
  for (const d of parseStyleAttr(style)) {
    if (!names.has(d.name)) continue
    out += style.slice(last, d.start)
    last = d.end
  }
  out += style.slice(last)
  out = out.replace(/^[\s;]+/, '').replace(/\s+$/, '')
  if (!/;\s*$/.test(style)) out = out.replace(/;$/, '')
  return out.replace(/;\s*$/, '').trim() ? out : null
}

/* ---------- Keeping local styles with their elements ---------- */

const WBC_RE = /\sdata-wbc="([^"]*)"/g

/** The data-wbc ids in markup (a cheap scan; the tree is not needed). */
export function idsIn(html: string): Set<string> {
  const out = new Set<string>()
  for (const m of html.matchAll(WBC_RE)) out.add(m[1])
  return out
}

/**
 * Local styles follow their elements: after an operation an element's entry moves to the section
 * that now holds the element (a move across sections) and goes when no section holds it (a
 * delete; undo brings the whole document back). Returns the sections' new local fields, only for
 * sections that change (undefined: the field goes).
 */
export function rehome(sections: LocalSection[]): Record<string, LocalStyles | undefined> {
  const code = sections.filter((s) => s.kind === 'code')
  if (!code.some((s) => s.local && Object.keys(s.local).length)) return {}
  const where = new Map<string, string>()
  for (const s of code) for (const id of idsIn(s.content.html ?? '')) if (!where.has(id)) where.set(id, s.id)
  const next: Record<string, LocalStyles> = {}
  for (const s of code) next[s.id] = {}
  for (const s of code) {
    for (const [id, e] of Object.entries(s.local ?? {})) {
      const to = where.get(id)
      if (to && !next[to][id]) next[to][id] = e
    }
  }
  const out: Record<string, LocalStyles | undefined> = {}
  for (const s of code) {
    const was = s.local ?? {}
    const now = next[s.id]
    const same = Object.keys(was).length === Object.keys(now).length && Object.keys(now).every((k) => was[k] === now[k])
    if (!same) out[s.id] = Object.keys(now).length ? now : undefined
  }
  return out
}

/** Local styles copied to new ids (duplicate, paste): from[i] → to[i]. */
export function copyEntries(src: LocalStyles | undefined, from: string[], to: string[]): LocalStyles {
  const out: LocalStyles = {}
  from.forEach((id, i) => {
    const e = src?.[id]
    if (e && to[i]) out[to[i]] = JSON.parse(JSON.stringify(e))
  })
  return out
}

/** The first family of a font-family value, unquoted ('' for var() or nothing; Go: LocalFontFamily). */
export function fontFamilyOf(v: string | undefined): string {
  const s = trimCss((v ?? '').replace(/!\s*important\s*$/i, ''))
  if (!s || s.includes('var(')) return ''
  return trimCss(s.split(',')[0]).replace(/^['"]|['"]$/g, '')
}

/** Families named first in the local styles' font-family values of sections. */
export function localFonts(sections: LocalSection[]): string[] {
  const out = new Set<string>()
  for (const s of sections) for (const e of Object.values(s.local ?? {})) for (const v of e.v) if (fontFamilyOf(v.p['font-family'])) out.add(fontFamilyOf(v.p['font-family']))
  return [...out]
}

/* ---------- The page's stylesheet ---------- */

/** The breakpoints of a section (its kit's, Boot.kitBreakpoints). */
export const sectionBps = (s: Section, kits: Record<string, Breakpoints> | undefined): Breakpoints => bpsOf(kits?.[s.content.kit ?? ''])

/**
 * The local stylesheet of visible sections in order, each with its kit's breakpoints (what
 * FillCode prints, with the frame's selectors; prefix: 'h:' / 'f:' for locked parts).
 */
export function sectionsCSS(sections: LocalSection[], kits: Record<string, Breakpoints> | undefined, prefix = '', opts: CssOptions = {}): string {
  let out = ''
  for (const s of sections) if (s.kind === 'code' && !s.hidden && s.local) out += localCSS(s.local, sectionBps(s, kits), attrSel(prefix), opts)
  return out
}
