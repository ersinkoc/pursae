/**
 * Whether the design's scripts run in the editor (plan K1, K2): the canvas opens in Live mode when
 * the user trusts every design (kit) of the page, else in Safe mode (static, nothing runs) with a
 * prompt on the canvas. Trust is per user and per kit, kept on the server (`PUT kits/{slug}/trust`)
 * and handed to the editor in Boot.scriptTrust; only users who may publish scripts
 * (`unfiltered_html`, Boot.canRunScripts) can give it or run scripts at all. "This session only"
 * is the editor-state answer of before (state.ts `scripts`), never stored.
 *
 * The decision is made from the boot object before the canvas creates its first frame, so opening
 * a trusted page loads the frame once, in Live mode.
 */
import type { CmsHost } from '../cms-host'
import type { Boot } from '../contract'
import { setKitTrust as restSetKitTrust } from '../rest'

/** The boot fields the trust decision reads (Boot.scriptTrust, Boot.canRunScripts: the trust store). */
type TrustBoot = Pick<Boot, 'caps' | 'codeParts' | 'document' | 'scriptTrust' | 'canRunScripts'>

const KIT = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/

const bootOf = (): Partial<TrustBoot> | undefined => (typeof window !== 'undefined' ? (window.__WEB_BUILDER__ as Partial<TrustBoot> | undefined) : undefined)
const hostOf = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

/**
 * The user may run (and trust) a design's scripts in the editor, and edit markup (code sections,
 * element code, Basic fields of a code section): both are `unfiltered_html`. The one rule every
 * panel and the canvas use (Boot.canRunScripts; caps.unfilteredHtml is the same capability, read
 * when an older server leaves canRunScripts out).
 */
export function canRunScripts(boot: Partial<TrustBoot> | undefined = bootOf()): boolean {
  if (!boot) return false
  if (typeof boot.canRunScripts === 'boolean') return boot.canRunScripts
  return !!boot.caps?.unfilteredHtml
}

/**
 * Markup is read-only for this user (no unfiltered_html: the server would drop the edit): the
 * Basic fields of a code section, Edit code and Section code. Same rule as canRunScripts; without
 * any boot object (unit tests of a panel alone) nothing is read-only.
 */
export function markupReadOnly(boot: Partial<TrustBoot> | undefined = hostOf()?.boot ?? bootOf()): boolean {
  return !!boot && !canRunScripts(boot)
}

/** The kits this user always runs (Boot.scriptTrust). */
export function bootTrust(boot: Partial<TrustBoot> | undefined = bootOf()): Set<string> {
  const v = boot?.scriptTrust
  return new Set(Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string' && KIT.test(s)) : [])
}

interface KitSection {
  content?: { kit?: unknown }
}

/** The kits of sections (the page's code sections and its header's and footer's), in order, once each. */
export function kitsIn(...lists: (readonly KitSection[] | null | undefined)[]): string[] {
  const out = new Set<string>()
  for (const l of lists) for (const s of l ?? []) {
    const k = s?.content?.kit
    if (typeof k === 'string' && KIT.test(k)) out.add(k)
  }
  return [...out]
}

/** The page's kits as the boot object has them (the stored document, or the user's draft, and the code parts). */
export function bootKits(boot: Partial<TrustBoot> | undefined = bootOf()): string[] {
  const d = boot?.document
  const doc = d?.draft?.doc ?? d?.doc
  return kitsIn(boot?.codeParts?.header?.sections, (doc?.sections ?? []) as KitSection[], boot?.codeParts?.footer?.sections)
}

/**
 * The page's standing with this user:
 *  - blocked: the user cannot run scripts (Safe mode only, no prompt);
 *  - trusted: every kit of the page is trusted (Live mode from the start);
 *  - untrusted: some kit is not (prompt: always / this session / not now);
 *  - nokit: no section names a kit (prompt: this session / not now).
 */
export type PageTrust = 'blocked' | 'trusted' | 'untrusted' | 'nokit'

export function pageTrust(kits: readonly string[], trust: ReadonlySet<string>, can: boolean): PageTrust {
  if (!can) return 'blocked'
  if (!kits.length) return 'nokit'
  return kits.every((k) => trust.has(k)) ? 'trusted' : 'untrusted'
}

interface CodeSection {
  content?: { html?: unknown; head?: unknown }
}

const SCRIPTED = /<script\b|\son[a-z]+\s*=|javascript:/i

/** Markup that runs something: a script, an inline handler or a javascript: URL (nothing to ask about without). */
export function usesScripts(...lists: (readonly CodeSection[] | null | undefined)[]): boolean {
  for (const l of lists) for (const s of l ?? []) {
    const c = s?.content
    if ((typeof c?.html === 'string' && SCRIPTED.test(c.html)) || (typeof c?.head === 'string' && SCRIPTED.test(c.head))) return true
  }
  return false
}

/** The editor state a page opens with (state.ts): the decision is taken before the first frame. */
export function initialScripts(boot: Partial<TrustBoot> | undefined = bootOf()): { scripts: boolean; live: boolean; prompt: boolean; trust: Set<string>; canRun: boolean } {
  const trust = bootTrust(boot)
  const canRun = canRunScripts(boot)
  const pt = pageTrust(bootKits(boot), trust, canRun)
  const on = pt === 'trusted'
  const d = boot?.document
  const secs = ((d?.draft?.doc ?? d?.doc)?.sections ?? []) as CodeSection[]
  const ask = (pt === 'untrusted' || pt === 'nokit') && usesScripts(boot?.codeParts?.header?.sections, secs, boot?.codeParts?.footer?.sections)
  return { scripts: on, live: on, prompt: ask, trust, canRun }
}

/** The frame mode of the canvas: Preview, Live (the scripts run) or static (Safe mode). */
export function frameModeOf(preview: boolean, live: boolean, scripts: boolean): 'preview' | 'live' | 'static' {
  if (preview) return scripts ? 'preview' : 'static'
  return live && scripts ? 'live' : 'static'
}

/** The frame's sandbox for a document that may (or may not) run the design's scripts. */
export const sandboxOf = (scripts: boolean) => (scripts ? 'allow-scripts allow-same-origin allow-forms' : 'allow-same-origin')

/**
 * Trusts (or stops trusting) a kit for this user: `PUT web-builder/v2/kits/{slug}/trust`
 * `{trust}` → `{trust, scriptTrust}` (rest.ts setKitTrust); resolves to the user's whole trusted
 * list. RestError 403 without unfiltered_html, 404 wb_kit_not_found.
 */
export async function setKitTrust(slug: string, on: boolean): Promise<string[]> {
  const h = hostOf()
  if (!h) throw new Error('The editor is not ready.')
  if (!KIT.test(slug)) throw new Error('Unknown design kit.')
  return restSetKitTrust(h.rest, slug, on)
}
