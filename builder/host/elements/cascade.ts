/**
 * Where an element's value comes from (Style panel): the design's rules, the local styles, the
 * inline style attribute or inheritance, read from the frame's stylesheets through the CSSOM (the
 * frame is same-origin; stylesheets from other origins cannot be read and are skipped).
 *
 * The cascade is modelled for what designs use: !important, cascade layers (Tailwind v4), media,
 * supports and container rules, CSS nesting, specificity (with :is / :not / :has / :where) and
 * source order. Shadow DOM, @scope and user-agent styles are not. A state (hover, focus, …) is
 * read by dropping that pseudo-class from the selectors, so the rules the state would add apply.
 *
 * The selector arithmetic is pure (tested in node); the walk needs the frame's DOM.
 */
import type { State } from './local'

export type Spec = [number, number, number]

/* ---------- Selectors ---------- */

/** Top-level parts of a selector list (commas outside (), [] and strings). */
export function splitList(sel: string): string[] {
  const out: string[] = []
  let depth = 0
  let start = 0
  for (let i = 0; i < sel.length; i++) {
    const ch = sel[i]
    if (ch === '\\') i++
    else if (ch === '"' || ch === "'") {
      const j = sel.indexOf(ch, i + 1)
      i = j < 0 ? sel.length : j
    } else if (ch === '(' || ch === '[') depth++
    else if (ch === ')' || ch === ']') depth--
    else if (ch === ',' && depth === 0) {
      out.push(sel.slice(start, i).trim())
      start = i + 1
    }
  }
  out.push(sel.slice(start).trim())
  return out.filter(Boolean)
}

const LEGACY_PSEUDO_EL = new Set(['before', 'after', 'first-line', 'first-letter'])
const IDENT = /[A-Za-z0-9_\- -￿]/

const add = (a: Spec, b: Spec): Spec => [a[0] + b[0], a[1] + b[1], a[2] + b[2]]
const max = (list: Spec[]): Spec => list.reduce<Spec>((m, s) => (cmpSpec(s, m) > 0 ? s : m), [0, 0, 0])

export function cmpSpec(a: Spec, b: Spec): number {
  return a[0] - b[0] || a[1] - b[1] || a[2] - b[2]
}

/** Specificity of one complex selector (no commas at the top level). */
export function specificity(sel: string): Spec {
  let s: Spec = [0, 0, 0]
  let i = 0
  const ident = () => {
    const st = i
    while (i < sel.length) {
      if (sel[i] === '\\') i += 2
      else if (IDENT.test(sel[i])) i++
      else break
    }
    return sel.slice(st, i)
  }
  const args = () => {
    // at "(": the argument text up to the matching ")"
    let depth = 0
    const st = i + 1
    for (; i < sel.length; i++) {
      const ch = sel[i]
      if (ch === '\\') i++
      else if (ch === '(') depth++
      else if (ch === ')' && --depth === 0) break
    }
    const a = sel.slice(st, i)
    i++
    return a
  }
  while (i < sel.length) {
    const ch = sel[i]
    if (ch === '#') {
      i++
      ident()
      s = add(s, [1, 0, 0])
    } else if (ch === '.') {
      i++
      ident()
      s = add(s, [0, 1, 0])
    } else if (ch === '[') {
      let depth = 0
      for (; i < sel.length; i++) {
        if (sel[i] === '\\') i++
        else if (sel[i] === '"' || sel[i] === "'") {
          const j = sel.indexOf(sel[i], i + 1)
          i = j < 0 ? sel.length : j
        } else if (sel[i] === '[') depth++
        else if (sel[i] === ']' && --depth === 0) break
      }
      i++
      s = add(s, [0, 1, 0])
    } else if (ch === ':') {
      const el = sel[i + 1] === ':'
      i += el ? 2 : 1
      const name = ident().toLowerCase()
      const fn = sel[i] === '('
      const a = fn ? args() : ''
      if (el || LEGACY_PSEUDO_EL.has(name)) s = add(s, [0, 0, 1])
      else if (name === 'where') continue
      else if (name === 'is' || name === 'not' || name === 'has' || name === 'matches' || name === '-webkit-any') s = add(s, max(splitList(a).map(specificity)))
      else if ((name === 'nth-child' || name === 'nth-last-child') && / of /i.test(a)) s = add(add(s, [0, 1, 0]), max(splitList(a.replace(/^.*? of /i, '')).map(specificity)))
      else s = add(s, [0, 1, 0])
    } else if (IDENT.test(ch) || ch === '\\') {
      ident()
      s = add(s, [0, 0, 1])
    } else i++ // combinators, *, &, whitespace
  }
  return s
}

/** Pseudo-classes a state implies (an element being pressed is hovered, a focus-visible one focused). */
const STATE_PSEUDOS: Record<Exclude<State, ''>, string[]> = {
  hover: ['hover'],
  focus: ['focus', 'focus-within'],
  'focus-visible': ['focus-visible', 'focus', 'focus-within'],
  active: ['active', 'hover'],
}

/** The selector with the state's pseudo-classes removed (they then match as if the state held). */
export function stripState(sel: string, st: State): string {
  if (!st) return sel
  let out = sel
  for (const p of STATE_PSEUDOS[st]) out = out.replace(new RegExp(`(?<!\\\\):${p}(?![A-Za-z0-9_-])`, 'g'), '')
  if (out === sel) return sel
  // A compound left empty (".a :hover" → ".a ") matches any element: "*".
  return out
    .replace(/\s+/g, ' ')
    .replace(/ $/, ' *')
    .replace(/^ /, '')
    .replace(/(^|[>+~(,] ?)(?=[>+~),]|$)/g, '$1*')
}

/* ---------- Declarations ---------- */

export interface Decl {
  value: string
  important: boolean
  spec: Spec
  /** Cascade layer order (Infinity: unlayered). */
  layer: number
  order: number
  /** The rule's selector part that matched ('style' for the inline style attribute). */
  selector: string
  origin: 'design' | 'local' | 'inline'
}

/** true when a beats b in the cascade. */
export function beats(a: Decl, b: Decl): boolean {
  if (a.important !== b.important) return a.important
  if (a.origin === 'inline' || b.origin === 'inline') return (a.origin === 'inline') !== (b.origin === 'inline') ? a.origin === 'inline' : a.order > b.order
  if (a.layer !== b.layer) return a.important ? a.layer < b.layer : a.layer > b.layer
  const c = cmpSpec(a.spec, b.spec)
  if (c) return c > 0
  return a.order > b.order
}

export function winner(list: Decl[]): Decl | null {
  let w: Decl | null = null
  for (const d of list) if (!w || beats(d, w)) w = d
  return w
}

/* ---------- The frame's rules (DOM) ---------- */

interface Matched {
  style: CSSStyleDeclaration
  spec: Spec
  layer: number
  order: number
  selector: string
  local: boolean
}

/** Properties a child inherits. */
export const INHERITED = new Set([
  'color', 'font', 'font-family', 'font-size', 'font-style', 'font-variant', 'font-weight', 'line-height', 'letter-spacing', 'word-spacing', 'text-align',
  'text-indent', 'text-transform', 'text-shadow', 'white-space', 'visibility', 'cursor', 'direction', 'list-style', 'list-style-type', 'list-style-position',
  'quotes', 'tab-size', 'text-decoration-color', 'hyphens', 'word-break', 'overflow-wrap', 'caret-color', 'accent-color',
])

const cache = new WeakMap<Element, { key: string; rules: Matched[] }>()

/** Rules of the frame that apply to el in state st at the frame's current width. */
export function matchedRules(el: Element, st: State, version: number): Matched[] {
  const doc = el.ownerDocument
  const win = doc.defaultView
  if (!win) return []
  const key = `${st}|${win.innerWidth}|${version}`
  const hit = cache.get(el)
  if (hit && hit.key === key) return hit.rules
  const layers = new Map<string, number>()
  const rank = (name: string) => {
    if (!layers.has(name)) layers.set(name, layers.size)
    return layers.get(name)!
  }
  const out: Matched[] = []
  let order = 0
  let anon = 0
  const test = (sel: string): { spec: Spec; part: string } | null => {
    let best: { spec: Spec; part: string } | null = null
    for (const part of splitList(sel)) {
      const probe = stripState(part, st)
      let ok = false
      try {
        ok = el.matches(probe)
      } catch {
        ok = false
      }
      if (!ok) continue
      const spec = specificity(part)
      if (!best || cmpSpec(spec, best.spec) > 0) best = { spec, part }
    }
    return best
  }
  const walk = (rules: CSSRuleList, layer: string | null, parent: string | null, local: boolean) => {
    for (const r of Array.from(rules)) {
      order++
      const name = r.constructor?.name
      if ('selectorText' in r && (r as CSSStyleRule).style) {
        const sr = r as CSSStyleRule
        let sel = sr.selectorText
        if (parent) sel = splitList(sel).map((p) => (p.includes('&') ? p.replace(/&/g, `:is(${parent})`) : `:is(${parent}) ${p}`)).join(', ')
        const m = test(sel)
        if (m) out.push({ style: sr.style, spec: m.spec, layer: layer === null ? Infinity : rank(layer), order, selector: m.part, local })
        if (sr.cssRules?.length) walk(sr.cssRules, layer, sel, local)
      } else if (name === 'CSSNestedDeclarations' && parent) {
        const m = test(parent)
        if (m) out.push({ style: (r as unknown as { style: CSSStyleDeclaration }).style, spec: m.spec, layer: layer === null ? Infinity : rank(layer), order, selector: m.part, local })
      } else if (r instanceof win.CSSMediaRule) {
        if (win.matchMedia(r.media.mediaText).matches) walk(r.cssRules, layer, parent, local)
      } else if (r instanceof win.CSSSupportsRule) {
        let ok = true
        try {
          ok = win.CSS.supports(r.conditionText)
        } catch {
          ok = true
        }
        if (ok) walk(r.cssRules, layer, parent, local)
      } else if (name === 'CSSLayerBlockRule') {
        const lr = r as CSSLayerBlockRule
        const n = (layer ? layer + '.' : '') + (lr.name || `#anon${anon++}`)
        rank(n)
        walk(lr.cssRules, n, parent, local)
      } else if (name === 'CSSLayerStatementRule') {
        for (const n of (r as CSSLayerStatementRule).nameList) rank((layer ? layer + '.' : '') + n)
      } else if (r instanceof win.CSSImportRule) {
        try {
          if (r.styleSheet) walk(r.styleSheet.cssRules, r.layerName !== null && r.layerName !== undefined ? (layer ? layer + '.' : '') + (r.layerName || `#anon${anon++}`) : layer, parent, local)
        } catch {
          /* another origin */
        }
      } else if ('cssRules' in r && (r as CSSGroupingRule).cssRules && name !== 'CSSKeyframesRule' && name !== 'CSSFontFaceRule') {
        walk((r as CSSGroupingRule).cssRules, layer, parent, local) // @container, @scope (approximate)
      }
    }
  }
  for (const sheet of Array.from(doc.styleSheets)) {
    const owner = sheet.ownerNode as Element | null
    if (owner?.hasAttribute?.('data-wb-edit')) continue
    if (sheet.media?.mediaText && !win.matchMedia(sheet.media.mediaText).matches) continue
    let rules: CSSRuleList
    try {
      rules = sheet.cssRules
    } catch {
      continue // another origin
    }
    walk(rules, null, null, !!owner?.hasAttribute?.('data-wb-local'))
  }
  cache.set(el, { key, rules: out })
  return out
}

/**
 * The flow-relative properties that set the same value as a physical one in a horizontal,
 * left-to-right page (Tailwind v4 writes py-4 as padding-block): they cascade together.
 */
export function logicalAliases(prop: string): string[] {
  const side: Record<string, string> = { top: 'block-start', bottom: 'block-end', left: 'inline-start', right: 'inline-end' }
  let m = /^(margin|padding|scroll-margin|scroll-padding)-(top|right|bottom|left)$/.exec(prop)
  if (m) return [`${m[1]}-${side[m[2]]}`]
  m = /^border-(top|right|bottom|left)-(width|style|color)$/.exec(prop)
  if (m) return [`border-${side[m[1]]}-${m[2]}`]
  m = /^(top|right|bottom|left)$/.exec(prop)
  if (m) return [`inset-${side[m[1]]}`]
  const corner: Record<string, string> = { 'top-left': 'start-start', 'top-right': 'start-end', 'bottom-left': 'end-start', 'bottom-right': 'end-end' }
  m = /^border-(top-left|top-right|bottom-left|bottom-right)-radius$/.exec(prop)
  if (m) return [`border-${corner[m[1]]}-radius`]
  const size: Record<string, string> = { width: 'inline-size', height: 'block-size', 'min-width': 'min-inline-size', 'min-height': 'min-block-size', 'max-width': 'max-inline-size', 'max-height': 'max-block-size' }
  return size[prop] ? [size[prop]] : []
}

/** The declarations of prop on el in state st (rules, then the inline style). */
export function declsOf(el: Element, prop: string, st: State, version: number): Decl[] {
  const out: Decl[] = []
  const names = [prop, ...logicalAliases(prop)]
  for (const m of matchedRules(el, st, version)) {
    for (const n of names) {
      const value = m.style.getPropertyValue(n)
      if (!value) continue
      out.push({ value: value.trim(), important: m.style.getPropertyPriority(n) === 'important', spec: m.spec, layer: m.layer, order: m.order, selector: m.selector, origin: m.local ? 'local' : 'design' })
    }
  }
  const inline = (el as HTMLElement).style
  for (const n of names) {
    const v = inline?.getPropertyValue(n)
    if (v) out.push({ value: v.trim(), important: inline.getPropertyPriority(n) === 'important', spec: [1, 0, 0], layer: Infinity, order: Number.MAX_SAFE_INTEGER, selector: 'style', origin: 'inline' })
  }
  return out
}

export interface Source {
  kind: 'local' | 'rule' | 'inline' | 'inherited' | 'default'
  /** The winning declaration (for inherited: the ancestor's). */
  decl: Decl | null
  /** The ancestor an inherited value comes from. */
  from?: Element
  /** A design declaration the local value must beat is !important. */
  designImportant: boolean
}

/** Where el's value of prop comes from in state st. */
export function sourceOf(el: Element, prop: string, st: State, version: number): Source {
  const list = declsOf(el, prop, st, version)
  const w = winner(list)
  const design = winner(list.filter((d) => d.origin !== 'local'))
  const designImportant = !!design?.important
  if (w) return { kind: w.origin === 'local' ? 'local' : w.origin === 'inline' ? 'inline' : 'rule', decl: w, designImportant }
  if (INHERITED.has(prop) || prop.startsWith('--')) {
    for (let p = el.parentElement; p; p = p.parentElement) {
      const d = winner(declsOf(p, prop, '', version))
      if (d) return { kind: 'inherited', decl: d, from: p, designImportant }
    }
  }
  return { kind: 'default', decl: null, designImportant }
}

/** A short label for an element (inherited from …): tag, id or first class. */
export function elLabel(el: Element): string {
  const tag = el.tagName.toLowerCase()
  if (el.id) return `${tag}#${el.id}`
  const c = (el.getAttribute('class') ?? '').trim().split(/\s+/)[0]
  return c ? `${tag}.${c}` : tag
}
