/**
 * The design's stylesheets as the class and variable editors read them (plan §4.3 b, c): the
 * classes its selectors define (chips' autocomplete), the rules of one class with their at-rule
 * context (where a class edit goes), the custom properties defined on :root / html (the Variables
 * panel), read from the frame through the CSSOM (same-origin; other origins are skipped). The
 * editor's own stylesheets (local styles, the kit override, editor rules) are not the design's.
 *
 * Needs the frame's DOM, except the pure helpers at the end.
 */

export interface RuleRef {
  /** At-rule preludes around the rule, outermost first ("@layer utilities", "@media (…)"). */
  ctx: string[]
  /** The rule's whole selector text. */
  sel: string
  style: CSSStyleDeclaration
  /** Where the rule comes from: the stylesheet's file name, "<style>" for an inline one. */
  source: string
  /** The kit whose file holds the rule ('' for an inline stylesheet or another file). */
  kit: string
  /** Rule number in the source (1-based, depth-first), shown with the source. */
  index: number
  /** The rule is inside an anonymous layer (an override cannot join it). */
  anonymous: boolean
}

const KIT_RE = /\/wp-content\/uploads\/web-builder\/kits\/([a-z0-9-]{1,64})\//

/** The stylesheet's label and kit. */
export function sheetSource(sheet: CSSStyleSheet): { source: string; kit: string } {
  const href = sheet.href ?? ''
  if (!href) return { source: '<style>', kit: '' }
  const path = href.split(/[?#]/)[0]
  const m = KIT_RE.exec(path)
  const file = m ? path.slice(path.indexOf(m[0]) + m[0].length) : path.replace(/^.*\//, '')
  return { source: file || path, kit: m?.[1] ?? '' }
}

/** The editor's own stylesheets (not the design's). */
const editorSheet = (sheet: CSSStyleSheet) => {
  const o = sheet.ownerNode as Element | null
  return !!o?.hasAttribute?.('data-wb-local') || !!o?.hasAttribute?.('data-wb-edit') || !!o?.hasAttribute?.('data-wb-kit')
}

/** Walks every style rule of the design's stylesheets with its context. */
export function eachRule(doc: Document, fn: (r: CSSStyleRule, ref: Omit<RuleRef, 'sel' | 'style'>) => void) {
  const win = doc.defaultView
  if (!win) return
  for (const sheet of Array.from(doc.styleSheets)) {
    if (editorSheet(sheet)) continue
    const { source, kit } = sheetSource(sheet)
    let index = 0
    const walk = (rules: CSSRuleList, ctx: string[], anonymous: boolean) => {
      for (const r of Array.from(rules)) {
        index++
        const name = r.constructor?.name
        if ('selectorText' in r && (r as CSSStyleRule).style) {
          fn(r as CSSStyleRule, { ctx, source, kit, index, anonymous })
          // Nested rules (CSS nesting) are skipped: their selector is relative.
        } else if (r instanceof win.CSSMediaRule) walk(r.cssRules, [...ctx, `@media ${r.media.mediaText}`], anonymous)
        else if (r instanceof win.CSSSupportsRule) walk(r.cssRules, [...ctx, `@supports ${r.conditionText}`], anonymous)
        else if (name === 'CSSLayerBlockRule') {
          const lr = r as CSSLayerBlockRule
          walk(lr.cssRules, lr.name ? [...ctx, `@layer ${lr.name}`] : ctx, anonymous || !lr.name)
        } else if (name === 'CSSContainerRule') {
          const cr = r as CSSGroupingRule & { conditionText?: string }
          walk(cr.cssRules, [...ctx, `@container ${cr.conditionText ?? ''}`.trim()], anonymous)
        } else if (r instanceof win.CSSImportRule) {
          try {
            if (r.styleSheet) walk(r.styleSheet.cssRules, r.layerName ? [...ctx, `@layer ${r.layerName}`] : ctx, anonymous || r.layerName === '')
          } catch {
            /* another origin */
          }
        }
      }
    }
    try {
      walk(sheet.cssRules, sheet.media?.mediaText ? [`@media ${sheet.media.mediaText}`] : [], false)
    } catch {
      /* another origin */
    }
  }
}

/* ---------- Classes ---------- */

const classCache = new WeakMap<Document, { v: number; list: string[] }>()

/** The class names the design's selectors use (sorted), for the chips' autocomplete. */
export function designClasses(doc: Document, version: number): string[] {
  const hit = classCache.get(doc)
  if (hit && hit.v === version) return hit.list
  const set = new Set<string>()
  eachRule(doc, (r) => {
    for (const c of classesInSelector(r.selectorText)) set.add(c)
  })
  const list = [...set].sort()
  classCache.set(doc, { v: version, list })
  return list
}

/** The rules whose selector list has the class alone (".rounded-2xl", ".btn:hover" not). */
export function classRules(doc: Document, cls: string): RuleRef[] {
  const want = '.' + cssEscape(cls)
  const out: RuleRef[] = []
  eachRule(doc, (r, ref) => {
    if (splitSelectors(r.selectorText).some((p) => p === want)) out.push({ ...ref, sel: r.selectorText, style: r.style })
  })
  return out
}

/**
 * Where an edit of class `cls` goes: the context of the design's rule for the class alone that
 * applies everywhere (no @media), else the first one; null when the design has no rule for it
 * alone (the edit then makes a new unlayered rule).
 */
export function classBase(rules: RuleRef[]): RuleRef | null {
  return rules.find((r) => !r.anonymous && !r.ctx.some((c) => c.startsWith('@media') || c.startsWith('@supports') || c.startsWith('@container'))) ?? rules.find((r) => !r.anonymous) ?? null
}

/* ---------- Variables ---------- */

export type VarGroup = 'color' | 'font' | 'size' | 'other'

export interface VarDef {
  name: string
  value: string
  group: VarGroup
  /** The defining rule's context and selector (an edit goes there). */
  ctx: string[]
  sel: string
  kit: string
  source: string
  /** Other definitions (a dark mode, a breakpoint): edits apply to the first one only. */
  more: number
}

const ROOTISH = /^(?::root|html|:host)$/

/** Custom properties defined on :root / html (first definition outside @media first). */
export function rootVariables(doc: Document): VarDef[] {
  const found = new Map<string, VarDef & { media: boolean }>()
  eachRule(doc, (r, ref) => {
    if (ref.anonymous) return
    if (!splitSelectors(r.selectorText).some((p) => ROOTISH.test(p))) return
    const media = ref.ctx.some((c) => !c.startsWith('@layer'))
    for (let i = 0; i < r.style.length; i++) {
      const name = r.style.item(i)
      if (!name.startsWith('--')) continue
      const value = r.style.getPropertyValue(name).trim()
      const hit = found.get(name)
      if (hit) {
        hit.more++
        if (hit.media && !media) Object.assign(hit, { value, ctx: ref.ctx, sel: r.selectorText, kit: ref.kit, source: ref.source, media })
        continue
      }
      found.set(name, { name, value, group: varGroup(name, value), ctx: ref.ctx, sel: r.selectorText, kit: ref.kit, source: ref.source, more: 0, media })
    }
  })
  return [...found.values()].map(({ media: _m, ...v }) => v)
}

/* ---------- Pure helpers ---------- */

/** Top-level parts of a selector list. */
export function splitSelectors(sel: string): string[] {
  const out: string[] = []
  let depth = 0
  let start = 0
  for (let i = 0; i < sel.length; i++) {
    const ch = sel[i]
    if (ch === '\\') i++
    else if (ch === '(' || ch === '[') depth++
    else if (ch === ')' || ch === ']') depth--
    else if (ch === ',' && depth === 0) {
      out.push(sel.slice(start, i).trim())
      start = i + 1
    }
  }
  out.push(sel.slice(start).trim())
  return out.filter(Boolean)
}

/** CSS.escape for a class name (node has no CSS global). */
export function cssEscape(v: string): string {
  if (typeof CSS !== 'undefined' && CSS.escape) return CSS.escape(v)
  return v.replace(/^(-?\d)/, '\\3$1 ').replace(/[^A-Za-z0-9_ -￿-]/g, '\\$&')
}

/** The class names a selector text names (unescaped: ".sm\:p-4" → "sm:p-4"). */
export function classesInSelector(sel: string): string[] {
  const out: string[] = []
  const re = /\.((?:\\[0-9a-fA-F]{1,6}\s?|\\.|[A-Za-z0-9_ -￿-])+)/g
  for (const m of sel.matchAll(re)) {
    // A number's decimal point (".5" inside an attribute value) is not a class.
    if (/^\d/.test(m[1]) && m.index! > 0 && /[\d]/.test(sel[m.index! - 1])) continue
    out.push(m[1].replace(/\\([0-9a-fA-F]{1,6})\s?/g, (_, h: string) => String.fromCodePoint(parseInt(h, 16))).replace(/\\(.)/g, '$1'))
  }
  return out
}

const COLOR_VALUE = /^(#[0-9a-f]{3,8}|(rgb|rgba|hsl|hsla|hwb|lab|lch|oklab|oklch|color|color-mix)\(|(transparent|currentcolor|black|white|red|green|blue|orange|gray|grey|yellow|purple|pink)$)/i

/** The Variables panel's group of a custom property. */
export function varGroup(name: string, value: string): VarGroup {
  const n = name.toLowerCase()
  const v = value.trim()
  if (COLOR_VALUE.test(v) || /(^|-)(color|colour|bg|background|fg|brand|primary|secondary|accent|surface|border-color|ring|shadow-color)(-|$)/.test(n)) return 'color'
  if (/(^|-)(font|family|leading|tracking|weight|line-height|letter)(-|$)/.test(n) || /^(['"]?[A-Za-z][\w -]*['"]?\s*,\s*)+(sans-serif|serif|monospace|system-ui|ui-sans-serif|cursive)/.test(v)) return 'font'
  if (/^-?[\d.]+(px|rem|em|%|vw|vh|ch|ex|vmin|vmax|lh)?$/.test(v) || /^(calc|clamp|min|max)\(/.test(v) || /(^|-)(spacing|space|radius|size|width|height|gap|breakpoint|container|text|blur|inset)(-|$)/.test(n)) return 'size'
  return 'other'
}

/** A plain color value as #rrggbb for a color input (null: not a plain color). */
export function hexOf(v: string): string | null {
  const s = v.trim()
  if (/^#[0-9a-f]{6}$/i.test(s)) return s.toLowerCase()
  if (/^#[0-9a-f]{3}$/i.test(s)) return ('#' + s.slice(1).replace(/./g, (x) => x + x)).toLowerCase()
  const m = /^rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)/i.exec(s)
  if (m) return '#' + [m[1], m[2], m[3]].map((x) => Math.min(255, Number(x)).toString(16).padStart(2, '0')).join('')
  return null
}
