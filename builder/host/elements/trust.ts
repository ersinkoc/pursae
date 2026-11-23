/**
 * Whether the design's scripts run in the editor (plan K1, K2): the canvas opens in Live mode when
 * the user trusts everything the page runs, else in Safe mode (static, nothing runs) with a prompt
 * on the canvas. Only users who may publish scripts (`unfiltered_html`, Boot.canRunScripts) can
 * give trust or run scripts at all.
 *
 * What a page runs is read from its markup, never from labels (needsOf):
 *  - the kits it loads: every design-kit file URL in a section's html or head
 *    (/wp-content/uploads/web-builder/kits/<slug>/…, as the server's wbKitRefs finds them), and
 *    every same-origin kit script; `content.kit` is a label and grants nothing;
 *  - its own code: each inline script, inline handler (on…), javascript: / vbscript: URL, iframe
 *    srcdoc, <base>, SVG animation of a handler or link, and script loaded from anywhere but a kit,
 *    hashed (SHA-256) one by one. Markup the browser could read as more code than the editor found
 *    (a parser differential) counts as one item: the whole section.
 *
 * Trust:
 *  - "Always for this design" is stored per user and per kit on the server (PUT kits/{slug}/trust,
 *    Boot.scriptTrust): it names the kit's installation and the hash of its executable files, so a
 *    new upload with other scripts is asked about again; the page's inline code is stored with it
 *    (Boot.scriptCode: the hashes approved per kit);
 *  - "This session only" covers exactly the kits and code of the page when it was given (state.ts
 *    sessionKits / sessionCode), never stored.
 * The scripts run only while the grants cover everything the page needs (covers). The canvas reads
 * the page's needs on every change of its code sections (an insert, a paste, Edit code, undo) and
 * drops to Safe mode in the same render, before the new markup can reach a frame that runs scripts,
 * then asks again (reconcileTrust).
 */
import type { CmsHost } from '../cms-host'
import type { Boot } from '../contract'
import { setKitTrust as restSetKitTrust } from '../rest'
import { LIVE_SANDBOX } from './live'
import { sha256 } from './sha256'

/** The boot fields the trust decision reads. */
type TrustBoot = Pick<Boot, 'caps' | 'codeParts' | 'document' | 'scriptTrust' | 'scriptCode' | 'canRunScripts'>

const KIT = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/
const HASH = /^[0-9a-f]{64}$/

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
 * Basic fields of a code section, Edit code, Section code and the canvas's element operations
 * (actions.commit, the toolbar, text editing). Same rule as canRunScripts; without
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

/** The inline code hashes approved with each trusted kit (Boot.scriptCode, the trust route's scriptCode). */
export function codeTrust(v: unknown): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  if (!v || typeof v !== 'object' || Array.isArray(v)) return out
  for (const [k, l] of Object.entries(v as Record<string, unknown>)) {
    if (KIT.test(k) && Array.isArray(l)) out[k] = l.filter((h): h is string => typeof h === 'string' && HASH.test(h))
  }
  return out
}

/* ---------- what a page runs ---------- */

/** What markup needs to run: the kits it loads, the hashes of its own code; scripts: it runs anything at all. */
export interface Needs {
  kits: string[]
  code: string[]
  scripts: boolean
}

interface CodeSection {
  content?: { html?: unknown; head?: unknown; kit?: unknown }
}

/** A kit file URL (webbuilder_scan.go wbKitRefs: the prefix, the slug, a path with an extension). */
const KIT_REF = /\/wp-content\/uploads\/web-builder\/kits\/([a-z0-9-]{1,64})\/[A-Za-z0-9_./@+-]{0,511}?\.[A-Za-z0-9]{1,8}(?![A-Za-z0-9])(?:\?v=\d+)?(?=["'\s)>,])/g
/** A same-origin kit script address: root-relative (not protocol-relative) or this origin's. */
const KIT_SCRIPT = /^\/wp-content\/uploads\/web-builder\/kits\/([a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)\/[^?#]*\.(?:m?js)(?:\?v=\d+)?$/

/** Script types the browser runs (or that change what runs: an import map, speculation rules). */
const JS_TYPES = new Set(['', 'module', 'importmap', 'speculationrules', 'text/javascript', 'application/javascript', 'application/ecmascript', 'text/ecmascript', 'application/x-javascript', 'text/jscript', 'text/livescript', 'text/x-javascript', 'text/x-ecmascript', 'text/javascript1.0', 'text/javascript1.1', 'text/javascript1.2', 'text/javascript1.3', 'text/javascript1.4', 'text/javascript1.5'])

/**
 * What a raw scan finds of each kind (script and style contents left out, where a script's own
 * code says `const onClick =` or names javascript:); the parsed document must account for at least
 * as many. The scan ends a script where the browser ends it at the earliest (`</script` followed by
 * a space, / or >), so it never reads less markup than the browser does.
 */
const RAW_BODY = /<(script|style)\b[^>]*>[\s\S]*?<\/\1[\s/>]/gi
const RAW_SCRIPT = /<script\b/gi
const RAW_HANDLER = /[\s"'/]on[a-z][a-z0-9_-]*\s*=/gi
const RAW_JSURL = /(?:java|vb)script\s*:/gi
const RAW_OTHER = /<base\b|[\s"'/]srcdoc\s*=|attributename\s*=\s*["']?\s*(?:on|href|xlink:href)/gi

const countOf = (re: RegExp, s: string) => (s.match(re) ?? []).length
const urlCode = (v: string) => /^(?:java|vb)script:/i.test(v.replace(/[\u0000- \u007f-\u009f]+/g, ''))

/** Same-origin kit slug of a script address, or null. */
function kitScript(src: string, origin: string): string | null {
  let s = src.trim()
  if (origin && s.startsWith(origin + '/')) s = s.slice(origin.length)
  const m = KIT_SCRIPT.exec(s)
  return m && !s.includes('/../') && !s.includes('/./') ? m[1] : null
}

const cache = new Map<string, Needs>()

/**
 * What one section's markup needs (its html and head), parsed as the browser parses it (an inert
 * document: nothing loads or runs while it is read). Cached by markup.
 */
export function sectionNeeds(html: string, head: string, origin: string = typeof location !== 'undefined' ? location.origin : ''): Needs {
  const key = origin + '\u0000' + head + '\u0000' + html
  const hit = cache.get(key)
  if (hit) return hit
  const kits = new Set<string>()
  const code = new Set<string>()
  let scripts = false
  const raw = head + '\n' + html
  for (const m of raw.matchAll(KIT_REF)) kits.add(m[1])
  let nScript = 0
  let nHandler = 0
  let nUrl = 0
  let nOther = 0
  if (/[<:=]/.test(raw) && typeof DOMParser !== 'undefined') {
    const doc = new DOMParser().parseFromString(`<!DOCTYPE html><html><head>${head}</head><body>${html}</body></html>`, 'text/html')
    const walk = (root: ParentNode) => {
      for (const el of Array.from(root.querySelectorAll('*'))) {
        const tag = el.localName
        if (tag === 'template') walk((el as HTMLTemplateElement).content)
        if (tag === 'script') {
          nScript++
          const type = (el.getAttribute('type') ?? '').trim().toLowerCase().replace(/\s*;.*$/, '')
          const src = el.getAttribute('src')
          if (JS_TYPES.has(type) || el.namespaceURI !== 'http://www.w3.org/1999/xhtml') {
            scripts = true
            const slug = src !== null && el.namespaceURI === 'http://www.w3.org/1999/xhtml' ? kitScript(src, origin) : null
            if (slug) kits.add(slug)
            else if (src !== null) code.add('src\u0000' + type + '\u0000' + src.trim())
            if (src === null || el.textContent?.trim()) code.add('script\u0000' + type + '\u0000' + (el.textContent ?? ''))
          }
        }
        for (const a of Array.from(el.attributes)) {
          const n = a.name.toLowerCase()
          if (n.startsWith('on')) {
            nHandler++
            scripts = true
            code.add('on\u0000' + n + '\u0000' + a.value)
          } else if (urlCode(a.value)) {
            nUrl++
            scripts = true
            code.add('url\u0000' + n + '\u0000' + a.value)
          } else if (n === 'srcdoc') {
            nOther++
            scripts = true
            code.add('srcdoc\u0000' + a.value)
          }
        }
        if (tag === 'base' || /^\s*(on|href|xlink:href)/i.test(el.getAttribute('attributeName') ?? '')) {
          nOther++
          scripts = true
          code.add(tag + '\u0000' + el.outerHTML)
        }
      }
    }
    walk(doc)
  }
  // A parser differential (markup the frame's document could read as more code than this document
  // did, or code only a raw scan sees): the whole section is one item.
  const bare = raw.replace(RAW_BODY, (_m, tag: string) => `<${tag}>`)
  if (countOf(RAW_SCRIPT, bare) > nScript || countOf(RAW_HANDLER, bare) > nHandler || countOf(RAW_JSURL, bare) > nUrl || countOf(RAW_OTHER, bare) > nOther) {
    scripts = true
    code.add('section\u0000' + head + '\u0000' + html)
  }
  const out: Needs = { kits: [...kits].sort(), code: [...code].map(sha256).sort(), scripts }
  if (cache.size > 500) cache.clear()
  cache.set(key, out)
  return out
}

/** What the sections of the lists need together (the page's code sections and its header's and footer's). */
export function needsOf(...lists: (readonly CodeSection[] | null | undefined)[]): Needs {
  const kits = new Set<string>()
  const code = new Set<string>()
  let scripts = false
  for (const l of lists)
    for (const s of l ?? []) {
      const c = s?.content
      const n = sectionNeeds(typeof c?.html === 'string' ? c.html : '', typeof c?.head === 'string' ? c.head : '')
      n.kits.forEach((k) => kits.add(k))
      n.code.forEach((h) => code.add(h))
      scripts ||= n.scripts
    }
  return { kits: [...kits].sort(), code: [...code].sort(), scripts }
}

/** The page's needs as the boot object has it (the stored document, or the user's draft, and the code parts). */
export function bootNeeds(boot: Partial<TrustBoot> | undefined = bootOf()): Needs {
  const d = boot?.document
  const doc = d?.draft?.doc ?? d?.doc
  const secs = ((doc?.sections ?? []) as (CodeSection & { kind?: string; hidden?: boolean })[]).filter((s) => s?.kind === undefined || (s.kind === 'code' && !s.hidden))
  return needsOf(boot?.codeParts?.header?.sections, secs, boot?.codeParts?.footer?.sections)
}

/** The kits of the page's needs (what "Always for this design" trusts). */
export const bootKits = (boot: Partial<TrustBoot> | undefined = bootOf()): string[] => bootNeeds(boot).kits

/* ---------- grants ---------- */

/** What the user granted: stored trust (kits, their approved code) and this session's answer. */
export interface Grants {
  trust: ReadonlySet<string>
  trustCode: Readonly<Record<string, readonly string[]>>
  sessionKits: ReadonlySet<string>
  sessionCode: ReadonlySet<string>
}

const NONE: Grants = { trust: new Set(), trustCode: {}, sessionKits: new Set(), sessionCode: new Set() }

/** What needs still lacks: with session (default) or with the stored trust alone. */
export function missing(needs: Needs, g: Grants, session = true): { kits: string[]; code: string[] } {
  const kits = needs.kits.filter((k) => !g.trust.has(k) && !(session && g.sessionKits.has(k)))
  const approved = new Set<string>()
  for (const k of needs.kits) if (g.trust.has(k)) for (const h of g.trustCode[k] ?? []) approved.add(h)
  const code = needs.code.filter((h) => !approved.has(h) && !(session && g.sessionCode.has(h)))
  return { kits, code }
}

/** The grants cover everything needs runs. */
export function covers(needs: Needs, g: Grants, session = true): boolean {
  const m = missing(needs, g, session)
  return !m.kits.length && !m.code.length
}

/**
 * The page's standing with this user (stored trust only):
 *  - blocked: the user cannot run scripts (Safe mode only, no prompt);
 *  - trusted: the page loads kits and every kit and every piece of its code is trusted (Live from the start);
 *  - untrusted: it loads kits, but some kit or some code is not trusted (prompt: always / this session / not now);
 *  - nokit: it loads no kit (prompt: this session / not now: there is no kit to remember it with).
 */
export type PageTrust = 'blocked' | 'trusted' | 'untrusted' | 'nokit'

export function pageTrust(needs: Needs, g: Grants, can: boolean): PageTrust {
  if (!can) return 'blocked'
  if (!needs.kits.length) return 'nokit'
  return covers(needs, g, false) ? 'trusted' : 'untrusted'
}

/** The state's grants (state.ts). */
export const grantsOf = (s: Partial<Grants>): Grants => ({ ...NONE, ...s }) as Grants

/**
 * The design's scripts run now: the user may run scripts, chose to (state `scripts`), and the
 * grants cover what the page needs NOW. The canvas and the mixed pages' frames read it in render,
 * so a change that brings in code nobody accepted makes the frame static before it is drawn.
 */
export function runsNow(s: { canRun: boolean; scripts: boolean } & Partial<Grants>, needs: Needs): boolean {
  return !!s.canRun && !!s.scripts && covers(needs, grantsOf(s))
}

/** Markup that runs something (nothing to ask about without). */
export function usesScripts(...lists: (readonly CodeSection[] | null | undefined)[]): boolean {
  return needsOf(...lists).scripts
}

/** The editor state a page opens with (state.ts): the decision is taken before the first frame. */
export function initialScripts(boot: Partial<TrustBoot> | undefined = bootOf()): {
  scripts: boolean
  live: boolean
  prompt: boolean
  trust: Set<string>
  trustCode: Record<string, string[]>
  sessionKits: Set<string>
  sessionCode: Set<string>
  canRun: boolean
} {
  const trust = bootTrust(boot)
  const trustCode = codeTrust(boot?.scriptCode)
  const canRun = canRunScripts(boot)
  const needs = bootNeeds(boot)
  const g = grantsOf({ trust, trustCode })
  const pt = pageTrust(needs, g, canRun)
  const on = pt === 'trusted'
  const ask = (pt === 'untrusted' || pt === 'nokit') && needs.scripts
  return { scripts: on, live: on, prompt: ask, trust, trustCode, sessionKits: new Set(), sessionCode: new Set(), canRun }
}

/** The frame mode of the canvas: Preview, Live (the scripts run) or static (Safe mode). Never with scripts without canRun. */
export function frameModeOf(preview: boolean, live: boolean, scripts: boolean, canRun = true): 'preview' | 'live' | 'static' {
  const run = scripts && canRun
  if (preview) return run ? 'preview' : 'static'
  return live && run ? 'live' : 'static'
}

/** The frame's sandbox for a document that may (or may not) run the design's scripts; never scripts without canRun. */
export const sandboxOf = (scripts: boolean, canRun = true) => (scripts && canRun ? LIVE_SANDBOX : 'allow-same-origin')

/** The trust route's answer: the user's trusted kits and the code approved with them. */
export interface TrustList {
  trust: string[]
  code: Record<string, string[]>
}

/**
 * Trusts (or stops trusting) a kit for this user, with the inline code hashes approved with it:
 * `PUT web-builder/v2/kits/{slug}/trust` `{trust, code?}` → `{trust, scriptTrust, scriptCode}`
 * (rest.ts setKitTrust); resolves to the user's whole list. RestError 403 without unfiltered_html,
 * 404 wb_kit_not_found.
 */
export async function setKitTrust(slug: string, on: boolean, code: readonly string[] = []): Promise<TrustList> {
  const h = hostOf()
  if (!h) throw new Error('The editor is not ready.')
  if (!KIT.test(slug)) throw new Error('Unknown design kit.')
  return restSetKitTrust(h.rest, slug, on, code)
}
