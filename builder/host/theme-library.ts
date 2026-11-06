/**
 * Theme library of the CMS host (docs/developer/web-builder-theme-library.md): the ready-made
 * full-site themes the editor can import. The server creates the templates and pages as drafts
 * (POST themes/{slug}/import); the browser draws and publishes each of them, because the snapshot is
 * rendered by the editor (contract §4); then POST themes/{slug}/finish disables the replaced templates
 * and creates the demo menus. Nothing of the theme is live until its own document is published.
 *
 * State lives in ThemeLibraryController (ThemeLibraryDialog.tsx draws it); the "Browse theme library"
 * card in the Items panel only calls open() (upstream seam, patch 0012).
 */
import type { DocState, PageSettings } from '@/builder/types'
import type { DocumentResponse, PublishRequest, PublishResponse, RenderOptions } from './contract'
import { routes } from './contract'
import { t } from './i18n'
import { buildPublishRequest, loadLiveRenderer, type RendererLoader } from './publish'
import type { RestClient } from './rest'
import { isRestError } from './rest'

/* ---------- REST shapes (web-builder/v2/themes, contract §12) ---------- */

export interface ThemePart {
  key: string
  type?: string
  label: string
}

export interface ThemeCard {
  slug: string
  name: string
  description: string
  version: string
  category?: string
  style?: string
  screenshot: string
  templates: ThemePart[]
  pages: ThemePart[]
  menus: number
  images: number
  store: boolean
  imported: boolean
  error?: string
}

export interface ThemesResponse {
  themes: ThemeCard[]
  /** Published templates the site has now, per template type. */
  existing: Record<string, number>
  /** Why the library is not available ("" when it is). */
  source: string
}

export type ImportMode = 'replace' | 'keep'

export interface ImportedPart {
  id: number
  key: string
  type?: string
  title: string
  rev: number
}

export interface ImportResponse {
  mode: ImportMode
  templates: ImportedPart[]
  pages: ImportedPart[]
  media: number
}

export interface FinishResponse {
  disabled: string[]
  menus: number
  pages: string[]
}

export const themeRoutes = {
  list: () => 'web-builder/v2/themes',
  import: (slug: string) => `web-builder/v2/themes/${encodeURIComponent(slug)}/import`,
  finish: (slug: string) => `web-builder/v2/themes/${encodeURIComponent(slug)}/finish`,
}

/* ---------- Controller ---------- */

export type ThemePhase = 'list' | 'confirm' | 'working' | 'done' | 'error'

export interface ThemeLibraryState {
  open: boolean
  loading: boolean
  /** Library problem (list could not be loaded, or no library is configured). */
  error: string
  data: ThemesResponse | null
  selected: ThemeCard | null
  phase: ThemePhase
  mode: ImportMode
  pages: boolean
  /** Working: what is being done, and how far. */
  step: string
  done: number
  total: number
  /** Done: what happened. */
  result: { mode: ImportMode; published: number; kept: number; pages: number; disabled: string[]; menus: number; media: number; links: string[] } | null
  /** Error: what went wrong. */
  message: string
}

export interface ThemeLibraryDeps {
  rest: RestClient
  boot: { links: { site: string }; rendererVersion: number; rest: { root: string } }
  loadRenderer?: RendererLoader
}

const INITIAL: ThemeLibraryState = {
  open: false,
  loading: false,
  error: '',
  data: null,
  selected: null,
  phase: 'list',
  mode: 'replace',
  pages: true,
  step: '',
  done: 0,
  total: 0,
  result: null,
  message: '',
}

/** The message of a failed request, with the server's own text when it sent one. */
export function errorText(e: unknown, fallback: string): string {
  if (isRestError(e) && e.message) return e.message
  return e instanceof Error && e.message ? e.message : fallback
}

export class ThemeLibraryController {
  private state: ThemeLibraryState = INITIAL
  private listeners = new Set<() => void>()
  private d: ThemeLibraryDeps

  constructor(deps: ThemeLibraryDeps) {
    this.d = deps
  }

  subscribe = (f: () => void) => {
    this.listeners.add(f)
    return () => {
      this.listeners.delete(f)
    }
  }
  getState = () => this.state

  /** Home URL of the site (the result's "View the site" link). */
  siteUrl = () => this.d.boot.links.site.replace(/\/+$/, '') + '/'

  private set(patch: Partial<ThemeLibraryState>) {
    this.state = { ...this.state, ...patch }
    this.listeners.forEach((f) => f())
  }

  /** True while an import runs: the dialog cannot be closed (a half-finished import is resumable, but not interruptible). */
  get busy() {
    return this.state.phase === 'working'
  }

  open = () => {
    this.set({ open: true, phase: 'list', selected: null, result: null, message: '' })
    void this.load()
  }

  close = () => {
    if (this.busy) return
    this.set({ open: false })
  }

  /** Back from the confirmation / result to the list. */
  back = () => {
    if (this.busy) return
    this.set({ phase: 'list', selected: null, result: null, message: '' })
    void this.load()
  }

  async load() {
    this.set({ loading: true, error: '' })
    try {
      const r = await this.d.rest.request<ThemesResponse>('GET', themeRoutes.list())
      this.set({ data: r.data, loading: false, error: r.data.source || '' })
    } catch (e) {
      this.set({ loading: false, data: null, error: errorText(e, t('The theme library could not be loaded.')) })
    }
  }

  select = (card: ThemeCard) => this.set({ selected: card, phase: 'confirm', mode: 'replace', message: '' })
  setMode = (mode: ImportMode) => this.set({ mode })
  setPages = (pages: boolean) => this.set({ pages })

  /** Types the theme provides that the site already has published templates for. */
  conflicts(card: ThemeCard | null = this.state.selected): { type: string; label: string; count: number }[] {
    if (!card || !this.state.data) return []
    const seen = new Set<string>()
    const out: { type: string; label: string; count: number }[] = []
    for (const p of card.templates) {
      const n = this.state.data.existing[p.type ?? ''] ?? 0
      if (p.type && n > 0 && !seen.has(p.type)) {
        seen.add(p.type)
        out.push({ type: p.type, label: p.label, count: n })
      }
    }
    return out
  }

  /** Imports the selected theme. Resolves when it is over (done or error shown in the state). */
  run = async () => {
    const card = this.state.selected
    if (!card || this.busy) return
    const { mode, pages } = this.state
    const rest = this.d.rest
    const publishNow = mode === 'replace'
    this.set({ phase: 'working', step: t('Creating the templates…'), done: 0, total: 1, message: '', result: null })
    let imported: ImportResponse
    try {
      imported = (await rest.request<ImportResponse>('POST', themeRoutes.import(card.slug), { json: { mode, pages } })).data
    } catch (e) {
      this.set({ phase: 'error', message: errorText(e, t('The theme could not be imported.')) })
      return
    }
    const todo = [...(publishNow ? imported.templates : []), ...imported.pages]
    this.set({ total: todo.length + 1, done: 1 })
    try {
      const live = await (this.d.loadRenderer ?? loadLiveRenderer)()
      let n = 1
      for (const part of todo) {
        this.set({ step: t('Publishing “{name}”…', { name: part.title }), done: n })
        await this.publishPart(live, part)
        n++
      }
      this.set({ step: t('Finishing…'), done: n })
      const fin = (await rest.request<FinishResponse>('POST', themeRoutes.finish(card.slug), { json: { mode, templates: publishNow ? imported.templates.map((p) => p.id) : [], pages: imported.pages.map((p) => p.id) } })).data
      this.set({
        phase: 'done',
        done: todo.length + 1,
        result: {
          mode,
          published: publishNow ? imported.templates.length : 0,
          kept: publishNow ? 0 : imported.templates.length,
          pages: imported.pages.length,
          disabled: fin.disabled,
          menus: fin.menus,
          media: imported.media,
          links: fin.pages,
        },
      })
    } catch (e) {
      this.set({ phase: 'error', message: errorText(e, t('The theme could not be imported.')) })
    }
  }

  private async publishPart(live: { renderStaticAsync(doc: DocState, opts: RenderOptions): Promise<import('./contract').RenderResult> }, part: ImportedPart) {
    const { rest, boot } = this.d
    const doc = (await rest.request<DocumentResponse>('GET', routes.document(part.id))).data
    if (!doc.doc) throw new Error(t('“{name}” has no design.', { name: part.title }))
    const opts: RenderOptions = {
      postId: part.id,
      formAction: (sectionId: string) => boot.rest.root + routes.form(part.id, sectionId),
      siteUrl: boot.links.site.replace(/\/+$/, ''),
    }
    const render = await live.renderStaticAsync(doc.doc, opts)
    if (render.renderer !== boot.rendererVersion) throw new Error(t('This editor is out of date. Reload it and try again.'))
    const page: PageSettings = { ...doc.page, title: part.title }
    const body: PublishRequest = buildPublishRequest({ doc: doc.doc, page }, render, { baseRev: part.rev, status: 'publish', reattach: false })
    await rest.request<PublishResponse>('POST', routes.publish(part.id), { json: body })
  }
}
