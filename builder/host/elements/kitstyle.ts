/**
 * Class and variable edits in the editor (plan §4.3 b, c; Faz 5): the kits' override stylesheets.
 *
 * The server keeps each kit's override (draft and live; internal/server/webbuilder_kitoverride.go).
 * The editor reads the draft of every kit the page uses when it opens. An edit writes the kit's
 * whole new override into the document's `wbKits` field (one history entry, so undo / redo work
 * like any other edit; the field is the editor's working copy and is not stored with the page — a
 * missing field means "as loaded"); whenever the working copy differs from what the server has,
 * it is sent as the kit's draft (debounced, versioned). The frame prints the working copies after
 * the design's stylesheets (<style data-wb-kit>). Publishing the page publishes the drafts of its
 * kits (cms-host → publishKitOverrides): the edit is then live on every page of the site.
 */
import { useSyncExternalStore } from 'react'
import type { DocState } from '@/builder/types'
import type { BuilderApi } from '@/builder/store'
import type { CmsHost } from '../cms-host'
import { isRestError } from '../rest'
import { t } from '../i18n'
import { asOverride, overrideCSS, sameOverride, type KitOverride } from './overrides'
import { mineSignatures, sourcesHash, type MineSource, type Sample } from './miner'

export type KitDoc = DocState & { wbKits?: Record<string, KitOverride> }

interface KitEntry {
  /** The draft the server had when the editor loaded it (or last saved). */
  saved: KitOverride
  version: number
  live: KitOverride
  /** A save is in flight / failed. */
  saving: boolean
  error: string
  /** The draft differs from the live override (publish makes it live). */
  pending: boolean
}

interface KitStore {
  kits: Record<string, KitEntry>
  /** The kit pages' markup ("From this design", class counts), per kit, once fetched. */
  pages: Record<string, MineSource[] | 'loading' | 'error'>
  catalog: Record<string, Sample[]>
}

let store: KitStore = { kits: {}, pages: {}, catalog: {} }
const subs = new Set<() => void>()
const emit = (patch: Partial<KitStore>) => {
  store = { ...store, ...patch }
  subs.forEach((f) => f())
}
export const getKitStore = () => store
export const useKitStore = () =>
  useSyncExternalStore(
    (f) => {
      subs.add(f)
      return () => {
        subs.delete(f)
      }
    },
    () => store,
    () => store,
  )

const host = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

const kitRoute = (slug: string, rest = '') => `web-builder/v2/kits/${encodeURIComponent(slug)}${rest}`

interface OverrideView {
  live: unknown
  version: number
  draft: unknown
  draftVersion: number
  pending: boolean
}

function entryOf(v: OverrideView): KitEntry {
  return { saved: asOverride(v.draft), version: v.draftVersion ?? 0, live: asOverride(v.live), saving: false, error: '', pending: !!v.pending }
}

/** The kits a page uses: its code sections' and its header's and footer's (content.kit). */
export function kitsOf(doc: DocState, parts?: { header: { content: { kit?: string } }[]; footer: { content: { kit?: string } }[] }): string[] {
  const out = new Set<string>()
  for (const s of [...(parts?.header ?? []), ...doc.sections, ...(parts?.footer ?? [])]) {
    const k = (s as { kind?: string; content: { kit?: string } }).content?.kit
    if (k && /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/.test(k)) out.add(k)
  }
  return [...out]
}

const loading = new Set<string>()

/** Reads the override of kits not read yet. */
export async function loadKits(slugs: string[]) {
  const h = host()
  if (!h) return
  for (const slug of slugs) {
    if (store.kits[slug] || loading.has(slug)) continue
    loading.add(slug)
    try {
      const r = await h.rest.request<OverrideView>('GET', kitRoute(slug, '/override'))
      emit({ kits: { ...store.kits, [slug]: entryOf(r.data) } })
    } catch {
      /* no such kit (deleted) or no access: the page edits nothing of it */
    } finally {
      loading.delete(slug)
    }
  }
}

/** The kit's override as the editor shows it: the document's working copy, else the loaded draft. */
export function overrideOf(doc: DocState, slug: string): KitOverride {
  return (doc as KitDoc).wbKits?.[slug] ?? store.kits[slug]?.saved ?? { rules: [] }
}

/** The frame's override stylesheet: every loaded kit's working copy. */
export function kitsCSS(doc: DocState): string {
  let out = ''
  for (const slug of Object.keys(store.kits).sort()) out += overrideCSS(overrideOf(doc, slug))
  return out
}

/** Writes a kit's new override into the document as one history entry. */
export function setOverride(b: Pick<BuilderApi, 'doc' | 'dispatch'>, doc: DocState, slug: string, next: KitOverride, label: string): boolean {
  if (!store.kits[slug]) return false
  if (sameOverride(next, overrideOf(doc, slug))) return false
  const d = doc as KitDoc
  b.dispatch({ type: 'replace', doc: { ...d, wbKits: { ...(d.wbKits ?? {}), [slug]: next } } as DocState, label })
  return true
}

/* ---------- Saving the drafts ---------- */

const timers: Record<string, ReturnType<typeof setTimeout>> = {}
const inflight: Record<string, Promise<void> | undefined> = {}
let lastDoc: DocState | null = null

function patchKit(slug: string, p: Partial<KitEntry>) {
  const cur = store.kits[slug]
  if (cur) emit({ kits: { ...store.kits, [slug]: { ...cur, ...p } } })
}

async function save(slug: string, notify?: (m: string) => void): Promise<void> {
  const h = host()
  const e = store.kits[slug]
  if (!h || !e || !lastDoc) return
  const want = overrideOf(lastDoc, slug)
  if (sameOverride(want, e.saved)) return
  patchKit(slug, { saving: true, error: '' })
  try {
    const r = await h.rest.request<OverrideView>('PUT', kitRoute(slug, '/override'), { json: { draft: want, base: e.version } })
    patchKit(slug, { saved: want, version: r.data.draftVersion, live: asOverride(r.data.live), pending: !!r.data.pending, saving: false })
  } catch (err) {
    const msg = isRestError(err, 'wb_kit_override_conflict')
      ? t('The design’s classes or variables were changed in another editor. Reload to see their changes.')
      : isRestError(err)
        ? err.message
        : t('The design’s classes and variables could not be saved.')
    patchKit(slug, { saving: false, error: msg })
    notify?.(msg)
  }
}

/** Sends the working copies that differ from the server's drafts (debounced). */
export function syncKits(doc: DocState, notify?: (m: string) => void) {
  lastDoc = doc
  for (const slug of Object.keys(store.kits)) {
    if (sameOverride(overrideOf(doc, slug), store.kits[slug].saved)) continue
    clearTimeout(timers[slug])
    timers[slug] = setTimeout(() => {
      inflight[slug] = save(slug, notify).finally(() => (inflight[slug] = undefined))
    }, 600)
  }
}

/** Saves now what is pending. */
export async function flushKits() {
  for (const slug of Object.keys(timers)) {
    clearTimeout(timers[slug])
    delete timers[slug]
    await save(slug)
  }
  await Promise.all(Object.values(inflight))
}

/**
 * After the page is published: the drafts of its kits go live (site-wide). Nothing happens for a
 * kit without changes; a failure is shown and the page stays published.
 */
export async function publishKitOverrides(): Promise<void> {
  const h = host()
  if (!h || !Object.keys(store.kits).length) return
  await flushKits()
  for (const [slug, e] of Object.entries(store.kits)) {
    if (!e.pending && sameOverride(e.saved, e.live)) continue
    try {
      const r = await h.rest.request<OverrideView>('POST', kitRoute(slug, '/override/publish'), { json: { base: e.version } })
      patchKit(slug, { version: r.data.draftVersion, live: asOverride(r.data.live), pending: false })
    } catch (err) {
      h.ui.notify({ tone: 'warning', message: t('The design’s class and variable changes could not be published: {m}', { m: isRestError(err) ? err.message : String(err) }) })
    }
  }
}

/* ---------- The kit's pages ("From this design", class counts) ---------- */

const CATALOG_KEY = 'wb-design-catalog:'

/** The markup of the kit's pages (fetched once per editor session). */
export async function loadPages(slug: string): Promise<MineSource[] | null> {
  const have = store.pages[slug]
  if (Array.isArray(have)) return have
  if (have === 'loading') return null
  const h = host()
  if (!h) return null
  emit({ pages: { ...store.pages, [slug]: 'loading' } })
  try {
    const r = await h.rest.request<{ pages: { id: number; title: string; html: string }[] }>('GET', kitRoute(slug, '/pages'))
    const list: MineSource[] = r.data.pages.map((p) => ({ html: p.html, page: String(p.id) }))
    emit({ pages: { ...store.pages, [slug]: list } })
    return list
  } catch {
    emit({ pages: { ...store.pages, [slug]: 'error' } })
    return null
  }
}

/**
 * The "From this design" catalog of a kit: mined in the editor from the kit's pages (and the
 * open page's own markup, which may not be published yet), cached in localStorage by the kit's
 * markup hash so a reopened editor does not mine again.
 */
export async function loadCatalog(slug: string, extra: MineSource[]): Promise<Sample[]> {
  const pages = (await loadPages(slug)) ?? []
  const sources = [...pages, ...extra]
  const key = CATALOG_KEY + slug + ':' + sourcesHash(sources)
  try {
    const hit = globalThis.localStorage?.getItem(key)
    if (hit) {
      const list = JSON.parse(hit) as Sample[]
      emit({ catalog: { ...store.catalog, [slug]: list } })
      return list
    }
  } catch {
    /* no storage */
  }
  const list = mineSignatures(sources, { max: 120 })
  emit({ catalog: { ...store.catalog, [slug]: list } })
  try {
    const ls = globalThis.localStorage
    if (ls) {
      for (let i = ls.length - 1; i >= 0; i--) {
        const k = ls.key(i)
        if (k?.startsWith(CATALOG_KEY + slug + ':') && k !== key) ls.removeItem(k)
      }
      ls.setItem(key, JSON.stringify(list))
    }
  } catch {
    /* full or no storage */
  }
  return list
}

/** Resets everything (tests). */
export function resetKitStore() {
  store = { kits: {}, pages: {}, catalog: {} }
  lastDoc = null
}

/** For tests: seed a kit's loaded state. */
export function seedKit(slug: string, saved: KitOverride, version = 0) {
  emit({ kits: { ...store.kits, [slug]: { saved, version, live: { rules: [] }, saving: false, error: '', pending: false } } })
}
