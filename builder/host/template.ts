/**
 * Template mode of the CMS host (contract §11.7, §11.8): the state behind the TemplateBar, the
 * ConditionsDialog and the PreviewPicker, and the conditions-first publish step.
 *
 *   conditions / priority   PUT templates/{id}/conditions, then GET templates/{id}/conflicts
 *   preview                 PUT templates/{id}/preview → {preview, dynamic} → DynStore.set
 *   beforePublish           the first publish of a template, and any publish of a template
 *                           without an include rule, opens the conditions dialog first; the
 *                           publish goes on only when the user saves it. Rules the user already
 *                           saved through the Conditions button in this session count as that
 *                           confirmation: with an include rule among them, the first publish
 *                           goes straight on
 *
 * Plain store (subscribe / getState) for useSyncExternalStore; no React here.
 */
import { ERR, TEMPLATE_TYPE_LABELS, routes, type Boot, type TemplateBoot, type TemplateConditionsResponse, type TemplateConflict, type TemplatePreview, type TemplatePreviewResponse, type TemplateType } from './contract'
import { clampPriority, hasInclude, serverErrorIndex } from './conditions'
import type { DynStore } from './dynamic'
import { t } from './i18n'
import { isRestError, type RestClient } from './rest'

export type DialogMode = 'edit' | 'publish'

export interface TemplateUiState {
  conditions: string[]
  priority: number
  preview: TemplatePreview
  /** The template is live (published from the builder in this session or before it). */
  published: boolean
  /** Open conditions dialog, else null. */
  dialog: { id: number; mode: DialogMode } | null
  /** The preview picker is open. */
  picker: boolean
  /** Other published templates of the same location that can match the same views; null = not loaded. */
  conflicts: TemplateConflict[] | null
  /** The user saved the conditions from the dialog in 'edit' mode (Conditions button) in this session. */
  confirmed: boolean
}

export interface SaveResult {
  ok: boolean
  /** Field errors: rule index or 'priority' → message. */
  errors: Partial<Record<number | 'priority', string>>
  /** A general error (network, permissions). */
  message: string
}

/** The type's label (TEMPLATE_TYPE_LABELS) in the editor's language. */
export function typeLabel(type: TemplateType): string {
  const labels: Record<TemplateType, string> = {
    header: t('Header'),
    footer: t('Footer'),
    'single-post': t('Single Post'),
    'single-page': t('Single Page'),
    single: t('Single'),
    'front-page': t('Front Page'),
    archive: t('Archive'),
    'search-results': t('Search Results'),
    'error-404': t('404 Page'),
    'single-product': t('Single Product'),
    'product-archive': t('Product Archive'),
  }
  return labels[type] ?? TEMPLATE_TYPE_LABELS[type] ?? type
}

/** Reads Boot.template; null for an ordinary post or a malformed value. */
export function readTemplate(boot: Pick<Boot, 'template'>): TemplateBoot | null {
  const tpl = boot.template
  if (!tpl || typeof tpl !== 'object' || typeof tpl.type !== 'string' || !(tpl.type in TEMPLATE_TYPE_LABELS)) return null
  return tpl
}

/** The template renders on the site: published (enabled) with a stored doc. */
export function templateLive(boot: Pick<Boot, 'document'>): boolean {
  return boot.document.status === 'publish' && boot.document.rev > 0
}

export class TemplateController {
  readonly boot: TemplateBoot
  readonly postId: number
  private state: TemplateUiState
  private listeners = new Set<() => void>()
  private seq = 0
  private dialogResolve: ((saved: boolean) => void) | null = null

  constructor(
    private rest: RestClient,
    private dyn: DynStore,
    boot: Boot,
    tpl: TemplateBoot,
  ) {
    this.boot = tpl
    this.postId = boot.document.postId
    this.state = {
      conditions: Array.isArray(tpl.conditions) ? tpl.conditions.filter((x) => typeof x === 'string') : [],
      priority: clampPriority(Number(tpl.priority) || 0),
      preview: tpl.preview,
      published: templateLive(boot),
      dialog: null,
      picker: false,
      conflicts: null,
      confirmed: false,
    }
  }

  subscribe = (f: () => void) => {
    this.listeners.add(f)
    return () => {
      this.listeners.delete(f)
    }
  }

  getState = (): TemplateUiState => this.state

  private set(patch: Partial<TemplateUiState>) {
    this.state = { ...this.state, ...patch }
    for (const f of this.listeners) f()
  }

  get type(): TemplateType {
    return this.boot.type
  }

  /* ---------- Conditions ---------- */

  /** Opens the conditions dialog; resolves true when the user saved it, false when closed. */
  openConditions(mode: DialogMode = 'edit'): Promise<boolean> {
    this.dialogResolve?.(false)
    this.set({ dialog: { id: ++this.seq, mode }, picker: false })
    void this.loadConflicts()
    return new Promise((resolve) => (this.dialogResolve = resolve))
  }

  /** Closes the dialog (saved = the user saved before closing). */
  closeConditions(saved = false) {
    const resolve = this.dialogResolve
    this.dialogResolve = null
    this.set({ dialog: null })
    resolve?.(saved)
  }

  /** PUT templates/{id}/conditions; on success the state holds the server's normalized rules. */
  async saveConditions(conditions: string[], priority: number): Promise<SaveResult> {
    try {
      const r = await this.rest.request<TemplateConditionsResponse>('PUT', routes.templateConditions(this.postId), { json: { conditions, priority: clampPriority(priority) } })
      const out = r.data
      // A save from the Conditions button is the user's explicit choice; a save from the publish
      // dialog confirms that publish only (the dialog's promise).
      const confirmed = this.state.confirmed || this.state.dialog?.mode === 'edit'
      this.set({ conditions: Array.isArray(out?.conditions) ? out.conditions : conditions, priority: typeof out?.priority === 'number' ? out.priority : clampPriority(priority), confirmed })
      void this.loadConflicts()
      return { ok: true, errors: {}, message: '' }
    } catch (e) {
      if (isRestError(e, ERR.badCondition)) {
        const errors: SaveResult['errors'] = {}
        for (const v of e.data.errors ?? []) {
          const i = serverErrorIndex(v.path)
          if (i !== null && errors[i] === undefined) errors[i] = v.message || t('This condition is not valid.')
        }
        return { ok: false, errors, message: Object.keys(errors).length ? '' : e.message }
      }
      return { ok: false, errors: {}, message: isRestError(e) ? e.message : t('The display conditions could not be saved.') }
    }
  }

  /** GET templates/{id}/conflicts (a warning only; failures leave the list empty). */
  async loadConflicts(): Promise<TemplateConflict[]> {
    try {
      const r = await this.rest.request<TemplateConflict[]>('GET', routes.templateConflicts(this.postId))
      const list = Array.isArray(r.data) ? r.data.filter((c) => c && typeof c.id === 'number') : []
      this.set({ conflicts: list })
      return list
    } catch {
      this.set({ conflicts: [] })
      return []
    }
  }

  /* ---------- Preview ---------- */

  openPreview() {
    this.set({ picker: true })
  }

  closePreview() {
    this.set({ picker: false })
  }

  /** PUT templates/{id}/preview; the answer's DynContext redraws the dynamic widgets. Throws an Error to show. */
  async setPreview(object: string): Promise<TemplatePreview> {
    let r
    try {
      r = await this.rest.request<TemplatePreviewResponse>('PUT', routes.templatePreview(this.postId), { json: { object } })
    } catch (e) {
      if (isRestError(e, ERR.badPreview)) throw new Error(t('This preview is not valid.'))
      if (isRestError(e, ERR.notFound)) throw new Error(t('Nothing was found to preview with this choice.'))
      throw new Error(isRestError(e) ? e.message : t('The preview could not be changed.'))
    }
    const out = r.data
    if (!out || !out.preview || !this.dyn.set(out.dynamic)) throw new Error(t('The preview data could not be read.'))
    this.set({ preview: out.preview })
    return out.preview
  }

  /* ---------- Publish ---------- */

  /**
   * Whether publishing should open the conditions dialog first: always without an include rule;
   * with one, only on the first publish the user has not already confirmed by saving the
   * conditions through the Conditions button in this session.
   */
  needsConditionsFirst(): boolean {
    if (!hasInclude(this.state.conditions)) return true
    return !this.state.published && !this.state.confirmed
  }

  /** BuilderHost.beforePublish: true = publish now. */
  async beforePublish(): Promise<boolean> {
    if (!this.needsConditionsFirst()) return true
    return this.openConditions('publish')
  }

  markPublished() {
    if (!this.state.published) this.set({ published: true })
  }
}
