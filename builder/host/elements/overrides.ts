/**
 * Kit overrides (plan §4.3 b, c; Go: internal/webbuilder/kitoverride.go): edits of a design's
 * classes and variables, kept per design kit as a list of rules with the design rule's own
 * selector inside the design rule's own at-rule context, printed after the design's stylesheets:
 *
 *   {rules: [{ctx: ['@layer utilities'], sel: '.rounded-2xl', p: {'border-radius': '2rem'}}]}
 *   → @layer utilities{.rounded-2xl{border-radius:2rem}}
 *
 * overrideCSS prints byte for byte what Go's KitOverrideCSS prints (shared fixtures in
 * internal/webbuilder/testdata/override). Pure functions, no DOM.
 */
import { declOK, trimCss } from './local'

export interface OverrideRule {
  /** At-rule preludes, outermost first ("@layer utilities", "@media (max-width:1023px)"). */
  ctx?: string[]
  sel: string
  p: Record<string, string>
}

export interface KitOverride {
  rules: OverrideRule[]
}

export const EMPTY_OVERRIDE: KitOverride = { rules: [] }

const byBytes = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)

/** The override's stylesheet (Go: KitOverrideCSS). */
export function overrideCSS(o: KitOverride | null | undefined): string {
  if (!o) return ''
  let out = ''
  for (const r of o.rules) {
    const keys = Object.keys(r.p ?? {}).filter((k) => declOK(k, r.p[k])).sort(byBytes)
    if (!keys.length) continue
    const ctx = r.ctx ?? []
    for (const c of ctx) out += c.trim() + '{'
    out += r.sel.trim() + '{' + keys.map((k) => k + ':' + trimCss(r.p[k])).join(';') + '}'
    out += '}'.repeat(ctx.length) + '\n'
  }
  return out
}

const CTX_RE = /^@(?:media|layer|supports|container)(?:[ \t][^{};<>\\]{0,300})?$/

/** A context prelude the override takes (Go: overrideCtxOK, the structural part). */
export const ctxOK = (c: string) => CTX_RE.test(c) && !c.includes('/*')

/** A selector the override takes (Go: overrideSelOK, the structural part). */
export function selOK(sel: string): boolean {
  if (!sel || sel.length > 1024 || /[{};<>]/.test(sel) || sel.includes('/*') || sel.startsWith('@')) return false
  if (/url\(|javascript:|expression\(/i.test(sel)) return false
  let depth = 0
  for (const ch of sel) {
    if (ch === '(' || ch === '[') depth++
    else if (ch === ')' || ch === ']') depth--
    if (depth < 0) return false
  }
  return depth === 0
}

const sameCtx = (a: string[] | undefined, b: string[] | undefined) => (a ?? []).join('\u0001') === (b ?? []).join('\u0001')

/** The rule for ctx × sel (undefined: none). */
export function ruleOf(o: KitOverride | null | undefined, ctx: string[], sel: string): OverrideRule | undefined {
  return o?.rules.find((r) => r.sel === sel && sameCtx(r.ctx, ctx))
}

/**
 * Sets (value) or removes (null / '') declarations of the rule ctx × sel; a rule left empty goes.
 * A new rule is added at the end (it comes after every rule already there). Returns a new override.
 */
export function setRuleDecls(o: KitOverride | null | undefined, ctx: string[], sel: string, decls: Record<string, string | null>): KitOverride {
  const rules = (o?.rules ?? []).map((r) => ({ ...r, p: { ...r.p } }))
  let r = rules.find((x) => x.sel === sel && sameCtx(x.ctx, ctx))
  if (!r) {
    r = { ...(ctx.length ? { ctx: [...ctx] } : {}), sel, p: {} }
    rules.push(r)
  }
  for (const [k, v] of Object.entries(decls)) {
    if (v === null || trimCss(v) === '') delete r.p[k]
    else r.p[k] = v
  }
  return { rules: rules.filter((x) => Object.keys(x.p).length) }
}

/** The override without any declaration of property `prop` in rules for selector `sel` (every context). */
export function dropProp(o: KitOverride | null | undefined, sel: string, prop: string): KitOverride {
  const rules = (o?.rules ?? []).map((r) => {
    if (r.sel !== sel || !(prop in r.p)) return r
    const p = { ...r.p }
    delete p[prop]
    return { ...r, p }
  })
  return { rules: rules.filter((x) => Object.keys(x.p).length) }
}

/** A value from the server or the doc as an override (anything else: empty). */
export function asOverride(v: unknown): KitOverride {
  const rules: OverrideRule[] = []
  const list = (v as { rules?: unknown } | null)?.rules
  if (!Array.isArray(list)) return { rules }
  for (const r of list) {
    if (!r || typeof r !== 'object') continue
    const x = r as { ctx?: unknown; sel?: unknown; p?: unknown }
    if (typeof x.sel !== 'string' || !x.p || typeof x.p !== 'object') continue
    const ctx = Array.isArray(x.ctx) ? x.ctx.filter((c): c is string => typeof c === 'string') : []
    const p: Record<string, string> = {}
    for (const [k, val] of Object.entries(x.p as Record<string, unknown>)) if (typeof val === 'string') p[k] = val
    rules.push({ ...(ctx.length ? { ctx } : {}), sel: x.sel, p })
  }
  return { rules }
}

/** Whether two overrides print the same stylesheet. */
export const sameOverride = (a: KitOverride | null | undefined, b: KitOverride | null | undefined) => overrideCSS(a) === overrideCSS(b)

/**
 * The context a device × state edit of a design rule goes to: the design rule's context, plus the
 * device's max-width media query (desktop first, like local styles) and the state's pseudo-class
 * on the selector.
 */
export function editTarget(ruleCtx: string[], sel: string, bp: '' | 'tablet' | 'mobile', st: string, bps: { tablet: number; mobile: number }): { ctx: string[]; sel: string } {
  const ctx = [...ruleCtx]
  if (bp) ctx.push(`@media (max-width:${bp === 'mobile' ? bps.mobile : bps.tablet}px)`)
  return { ctx, sel: st ? `${sel}:${st}` : sel }
}
