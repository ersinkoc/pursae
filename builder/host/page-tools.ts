/**
 * Page tools of the CMS host (docs/developer/web-builder-contract.md §3.7): what the built-in plugins
 * add to a page while the builder is on, moved into the editor: the page's languages and their
 * translations (Pursae Multi Language) and its search engine data (Pursae SEO). Both talk to the
 * plugins' own REST routes, which enforce their own capabilities.
 *
 * GET documents/{id}/plugins tells which tabs exist; no tab, no "Page tools" row in the builder menu
 * (and none at all in template mode). State lives in PageToolsController (PageToolsDialog.tsx draws
 * it); the row of the builder menu (BuilderHost.menuExtra, patch 0014) only calls open().
 */
import type { MediaAsset } from '@/builder/host'
import { routes } from './contract'
import { t } from './i18n'
import { isRestError, type RestClient } from './rest'
import { errorText } from './theme-library'

/* ---------- Server shapes ---------- */

/** GET documents/{id}/plugins. */
export interface PagePanels {
  languages: boolean
  seo: boolean
  seoEdit: boolean
  /** The user may save canonical, noindex and nofollow (seogo_onpage_advanced). Absent = no. */
  seoAdvanced?: boolean
  /** The user may save the social title, description and image (seogo_onpage_social). Absent = no. */
  seoSocial?: boolean
}

export interface LangRef {
  slug: string
  name: string
  /** Emoji flag. */
  flag: string
  rtl?: boolean
}

export interface LangTranslation extends LangRef {
  postId: number
  title: string
  status: string
  editUrl: string
  current?: boolean
  /** The user may not edit this translation: title, status and editUrl come empty. */
  readOnly?: boolean
}

/** GET documents/{id}/languages. */
export interface LanguagesInfo {
  enabled: boolean
  canTranslate: boolean
  current?: LangRef
  translations: LangTranslation[]
  missing: LangRef[]
}

/** The Pursae SEO fields the dialog edits (seogo/v1/meta fields). */
export const SEO_TEXT_FIELDS = ['title', 'description', 'focus', 'canonical', 'og_title', 'og_description', 'og_image'] as const
export type SeoTextField = (typeof SEO_TEXT_FIELDS)[number]

/** The fields Pursae SEO saves only with its "advanced" / "social" on-page capabilities (others need seogo_onpage_general). */
export const SEO_ADVANCED_FIELDS = ['canonical', 'noindex', 'nofollow'] as const
export const SEO_SOCIAL_FIELDS = ['og_title', 'og_description', 'og_image'] as const
export type SeoField = SeoTextField | 'noindex' | 'nofollow'

/** GET / POST seogo/v1/meta/{id}: every field is a string; the scores are numbers as text ("" = never analyzed). */
export interface SeoMeta {
  id: number
  fields: Record<string, string>
  seo_score?: string | number
}

export interface SeoDraft extends Record<SeoTextField, string> {
  noindex: boolean
  nofollow: boolean
}

/* ---------- Pure helpers ---------- */

export type PageToolsTab = 'languages' | 'seo'

/** The tabs a page has, in dialog order. */
export function tabsOf(p: PagePanels | null): PageToolsTab[] {
  const out: PageToolsTab[] = []
  if (p?.languages) out.push('languages')
  if (p?.seo && p.seoEdit) out.push('seo')
  return out
}

/** The draft the SEO form starts from. noindex: only an explicit "noindex" counts (the plugin also stores "index"). */
export function seoDraftOf(fields: Record<string, string> | undefined): SeoDraft {
  const f = fields ?? {}
  const d = { noindex: f.noindex === 'noindex', nofollow: f.nofollow === '1' } as SeoDraft
  for (const k of SEO_TEXT_FIELDS) d[k] = f[k] ?? ''
  return d
}

/**
 * The POST body's `fields`: only what differs from the loaded meta. noindex is the string "noindex"
 * (empty = the plugin's default for the post type; an explicit "index" is kept unless the toggle is
 * turned on), nofollow "1" or empty.
 */
export function diffSeo(fields: Record<string, string> | undefined, draft: SeoDraft): Record<string, string> {
  const base = seoDraftOf(fields)
  const out: Record<string, string> = {}
  for (const k of SEO_TEXT_FIELDS) if (draft[k] !== base[k]) out[k] = draft[k]
  if (draft.noindex !== base.noindex) out.noindex = draft.noindex ? 'noindex' : ''
  if (draft.nofollow !== base.nofollow) out.nofollow = draft.nofollow ? '1' : ''
  return out
}

export type SeoRating = 'good' | 'ok' | 'bad'

/** The SEO score (0–100) of a meta response, or null while the page has never been analyzed. */
export function seoScoreOf(m: Pick<SeoMeta, 'seo_score'> | null | undefined): number | null {
  const raw = m?.seo_score
  if (raw === undefined || raw === null || raw === '') return null
  const n = typeof raw === 'number' ? raw : Number(raw)
  return Number.isFinite(n) ? Math.max(0, Math.min(100, Math.round(n))) : null
}

/** Pursae SEO's bands: 71 and up good, 41 and up needs improvement, below that poor. */
export const seoRating = (score: number): SeoRating => (score >= 71 ? 'good' : score >= 41 ? 'ok' : 'bad')

/** An edit URL the dialog may link to or navigate to: same-site absolute path or http(s). */
export function safeEditUrl(u: unknown): string {
  if (typeof u !== 'string') return ''
  const s = u.trim()
  if (/^\/(?![/\\])/.test(s)) return s
  return /^https?:\/\//i.test(s) ? s : ''
}

/**
 * The address the social image preview may load: a path or an http(s) URL on the site's own origin,
 * or a URL the user chose from the media library in this session (`picked`, which may live on a
 * CDN). Anything else, such as text typed into the field or stored by another editor, is not
 * requested by the editor's browser. "" = draw no preview.
 */
export function previewImageUrl(value: string, origin: string, picked: ReadonlySet<string> = new Set()): string {
  const v = value.trim()
  if (!v) return ''
  if (picked.has(v)) return /^https?:\/\//i.test(v) || /^\/(?![/\\])/.test(v) ? v : ''
  try {
    const u = new URL(v, origin)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.origin === new URL(origin).origin ? u.href : ''
  } catch {
    return ''
  }
}

/** The label of a post status. */
export function statusLabel(status: string): string {
  switch (status) {
    case 'publish':
      return t('Published')
    case 'draft':
      return t('Draft')
    case 'pending':
      return t('Pending review')
    case 'private':
      return t('Private')
    case 'future':
      return t('Scheduled')
    default:
      return status
  }
}

/* ---------- Controller ---------- */

export type Load = 'idle' | 'loading' | 'ready' | 'error'

export interface PageToolsState {
  open: boolean
  /** The plugin panels once probed; null = unknown or none (the menu row stays hidden). */
  panels: PagePanels | null
  tab: PageToolsTab
  languages: { load: Load; data: LanguagesInfo | null; error: string; /** slug of the translation being created */ adding: string }
  seo: { load: Load; data: SeoMeta | null; draft: SeoDraft | null; error: string; saving: boolean; saved: boolean }
}

const INITIAL: PageToolsState = {
  open: false,
  panels: null,
  tab: 'languages',
  languages: { load: 'idle', data: null, error: '', adding: '' },
  seo: { load: 'idle', data: null, draft: null, error: '', saving: false, saved: false },
}

export interface PageToolsDeps {
  rest: RestClient
  postId: number
  /** Sends pending autosave changes (host.autosaver.flush); absent = nothing to flush. */
  flush?: () => Promise<void>
  /** True while a change has not reached the server (host.autosaver.pending). */
  pending?: () => boolean
  /** Leaves for a URL (location.assign). */
  navigate?: (url: string) => void
  /** Opens the media library (host.media.pick); absent = no library button. */
  pickImage?: () => Promise<MediaAsset | null>
  /** The site's origin, for the social image preview (default location.origin). */
  origin?: string
}

export class PageToolsController {
  private state: PageToolsState = INITIAL
  private listeners = new Set<() => void>()
  /** Guards stale answers: a tab's load result is dropped after a newer load started. */
  private seq = { languages: 0, seo: 0 }
  /** Image URLs chosen from the media library in this session: their previews may load. */
  private picked = new Set<string>()

  constructor(private d: PageToolsDeps) {}

  subscribe = (f: () => void) => {
    this.listeners.add(f)
    return () => {
      this.listeners.delete(f)
    }
  }
  getState = () => this.state

  private set(patch: Partial<PageToolsState>) {
    this.state = { ...this.state, ...patch }
    this.listeners.forEach((f) => f())
  }
  private setLanguages(patch: Partial<PageToolsState['languages']>) {
    this.set({ languages: { ...this.state.languages, ...patch } })
  }
  private setSeo(patch: Partial<PageToolsState['seo']>) {
    this.set({ seo: { ...this.state.seo, ...patch } })
  }

  get tabs(): PageToolsTab[] {
    return tabsOf(this.state.panels)
  }
  /** False = hide the builder menu row. */
  get available() {
    return this.tabs.length > 0
  }
  get canPick() {
    return !!this.d.pickImage
  }
  /** True when the SEO form differs from what the server has. */
  get seoDirty() {
    const { data, draft } = this.state.seo
    return !!data && !!draft && Object.keys(diffSeo(data.fields, draft)).length > 0
  }

  /** True when the user may save this SEO field (the plugin drops the others silently). */
  canEditSeo = (field: SeoField): boolean => {
    const p = this.state.panels
    if ((SEO_ADVANCED_FIELDS as readonly string[]).includes(field)) return !!p?.seoAdvanced
    if ((SEO_SOCIAL_FIELDS as readonly string[]).includes(field)) return !!p?.seoSocial
    return true
  }

  /** The social image preview address for a field value ("" = none; see previewImageUrl). */
  previewUrl = (value: string): string => previewImageUrl(value, this.d.origin ?? (typeof location !== 'undefined' ? location.origin : ''), this.picked)

  /** Asks which tabs the page has. A failed request means none: the menu row stays hidden. */
  async probe(): Promise<void> {
    try {
      const r = await this.d.rest.request<Partial<PagePanels>>('GET', routes.documentPlugins(this.d.postId))
      const p = r.data
      this.set({ panels: { languages: !!p?.languages, seo: !!p?.seo, seoEdit: !!p?.seoEdit, seoAdvanced: !!p?.seoAdvanced, seoSocial: !!p?.seoSocial } })
    } catch {
      this.set({ panels: null })
    }
  }

  open = (tab?: PageToolsTab) => {
    const tabs = this.tabs
    if (!tabs.length) return
    this.set({ open: true })
    this.select(tab && tabs.includes(tab) ? tab : tabs.includes(this.state.tab) ? this.state.tab : tabs[0])
  }

  close = () => {
    const { languages, seo } = this.state
    if (seo.saving || languages.adding) return
    this.set({ open: false })
  }

  /** Shows a tab and (re)loads its data. */
  select = (tab: PageToolsTab) => {
    if (!this.tabs.includes(tab)) return
    this.set({ tab })
    void (tab === 'languages' ? this.loadLanguages() : this.loadSeo())
  }

  async loadLanguages(): Promise<void> {
    const n = ++this.seq.languages
    this.setLanguages({ load: 'loading', error: '' })
    try {
      const r = await this.d.rest.request<LanguagesInfo>('GET', routes.documentLanguages(this.d.postId))
      if (n !== this.seq.languages) return
      const data: LanguagesInfo = { ...r.data, translations: r.data.translations ?? [], missing: r.data.missing ?? [] }
      this.setLanguages({ load: 'ready', data })
    } catch (e) {
      if (n !== this.seq.languages) return
      this.setLanguages({ load: 'error', data: null, error: errorText(e, t('The languages could not be loaded.')) })
    }
  }

  /**
   * Creates the translation in `lang` (a draft copy of this page) and opens it in the builder. Pending
   * edits are sent first, so the copy starts from what is on screen; when they cannot be saved
   * nothing is created.
   */
  addTranslation = async (lang: string): Promise<void> => {
    if (this.state.languages.adding) return
    this.setLanguages({ adding: lang, error: '' })
    try {
      if (this.d.pending?.()) {
        await this.d.flush?.()
        if (this.d.pending?.()) {
          this.setLanguages({ adding: '', error: t('Your latest changes could not be saved yet. Try again in a moment.') })
          return
        }
      }
      const r = await this.d.rest.request<{ postId?: number; editUrl?: string }>('POST', routes.documentTranslations(this.d.postId), { json: { lang } })
      const url = safeEditUrl(r.data?.editUrl)
      // Edits made while the request ran are not in the copy: send them to this page before leaving.
      if (!(await this.settle())) {
        // The copy exists: refresh the list (it now shows the translation), then keep the message.
        this.setLanguages({ adding: '' })
        await this.loadLanguages()
        this.setLanguages({ error: t('The translation was created, but your latest changes could not be saved yet. Try again in a moment.') })
        return
      }
      if (!url) {
        // Created, but no address to open: show it in the list.
        this.setLanguages({ adding: '' })
        await this.loadLanguages()
        return
      }
      // adding stays set: the page is being left.
      ;(this.d.navigate ?? ((u: string) => location.assign(u)))(url)
    } catch (e) {
      this.setLanguages({ adding: '', error: errorText(e, t('The translation could not be created.')) })
      // Someone else created it meanwhile: show the current list.
      if (isRestError(e, 'wb_translation_exists')) await this.loadLanguages()
    }
  }

  /** Sends pending autosave changes; true when nothing is left unsaved (a rejected flush counts as not saved). */
  private async settle(): Promise<boolean> {
    if (!this.d.pending?.()) return true
    try {
      await this.d.flush?.()
    } catch {
      /* judged by pending() below */
    }
    return !this.d.pending?.()
  }

  /**
   * Opens another language's page in the builder, after pending edits were sent. Edits that cannot be
   * saved keep the dialog open with a message instead of being dropped (Ctrl/Cmd-click on the link
   * still leaves without asking, in a new tab).
   */
  openTranslation = async (editUrl: string): Promise<void> => {
    const url = safeEditUrl(editUrl)
    if (!url) return
    this.setLanguages({ error: '' })
    if (!(await this.settle())) {
      this.setLanguages({ error: t('Your latest changes could not be saved yet. Try again in a moment.') })
      return
    }
    ;(this.d.navigate ?? ((u: string) => location.assign(u)))(url)
  }

  /**
   * Loads the SEO data. Unsaved edits are never replaced: while the form is dirty (before the request,
   * or when edits began while it ran) the draft stays as it is, so switching tabs or reopening the
   * dialog keeps what was typed.
   */
  async loadSeo(): Promise<void> {
    if (this.seoDirty) return
    const n = ++this.seq.seo
    this.setSeo({ load: 'loading', error: '', saved: false })
    try {
      const r = await this.d.rest.request<SeoMeta>('GET', routes.seoMeta(this.d.postId))
      if (n !== this.seq.seo) return
      if (this.seoDirty) {
        this.setSeo({ load: 'ready' })
        return
      }
      this.setSeo({ load: 'ready', data: r.data, draft: seoDraftOf(r.data.fields) })
    } catch (e) {
      if (n !== this.seq.seo) return
      this.setSeo({ load: this.seoDirty ? 'ready' : 'error', ...(this.seoDirty ? {} : { data: null, draft: null }), error: errorText(e, t('The SEO settings could not be loaded.')) })
    }
  }

  editSeo = (patch: Partial<SeoDraft>) => {
    const { draft } = this.state.seo
    if (!draft) return
    // A field the user may not save is not edited (the plugin would drop it silently).
    const allowed = Object.fromEntries(Object.entries(patch).filter(([k]) => this.canEditSeo(k as SeoField))) as Partial<SeoDraft>
    if (!Object.keys(allowed).length) return
    this.setSeo({ draft: { ...draft, ...allowed }, saved: false, error: '' })
  }

  /** Chooses the social image from the media library. */
  pickSocialImage = async () => {
    if (!this.d.pickImage) return
    const a = await this.d.pickImage()
    if (a?.url) {
      this.picked.add(a.url.trim())
      this.editSeo({ og_image: a.url })
    }
  }

  saveSeo = async (): Promise<void> => {
    const { data, draft, saving } = this.state.seo
    if (!data || !draft || saving) return
    const fields = diffSeo(data.fields, draft)
    if (!Object.keys(fields).length) return
    this.setSeo({ saving: true, error: '', saved: false })
    try {
      const r = await this.d.rest.request<SeoMeta>('POST', routes.seoMeta(this.d.postId), { json: { fields } })
      const next = seoDraftOf(r.data.fields)
      const latest = this.state.seo.draft
      if (latest) {
        for (const key of Object.keys(draft) as (keyof SeoDraft)[]) {
          if (latest[key] !== draft[key]) Object.assign(next, { [key]: latest[key] })
        }
      }
      this.setSeo({ saving: false, saved: true, data: r.data, draft: next })
    } catch (e) {
      this.setSeo({ saving: false, error: errorText(e, t('The SEO settings could not be saved.')) })
    }
  }
}
