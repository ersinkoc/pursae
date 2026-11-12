/**
 * Host tools of the builder menu (HostToolsDialogs.tsx draws them):
 *
 * - ScheduleController  scheduled publish: a date makes the next publish ask for status future
 *                       (PublishRequest.date); the core's publish_future_post cron puts it live.
 * - RevisionsController the published versions (GET documents/{id}/revisions); restoring one writes
 *                       it as the user's draft (POST …/restore) and reloads the editor on it.
 * - PopupController     a popup template's settings (GET/PUT templates/{id}/popup): trigger,
 *                       frequency, position, width.
 *
 * No React here: tests drive the controllers with a mocked RestClient.
 */
import { POPUP_LIMITS, POPUP_SELECTOR_PATTERN, routes, type Boot, type PopupSettings, type RevisionSummary } from './contract'
import { t } from './i18n'
import type { RestClient } from './rest'
import { errorText } from './theme-library'

type Listener = () => void

class Store<S> {
  protected state: S
  private listeners = new Set<Listener>()
  constructor(initial: S) {
    this.state = initial
  }
  subscribe = (f: Listener) => {
    this.listeners.add(f)
    return () => {
      this.listeners.delete(f)
    }
  }
  getState = () => this.state
  protected set(patch: Partial<S>) {
    this.state = { ...this.state, ...patch }
    this.listeners.forEach((f) => f())
  }
}

/* ---------- Scheduled publish ---------- */

/** "YYYY-MM-DDTHH:mm" in local time (a datetime-local input) of an ISO date; "" when invalid. */
export function toLocalInput(iso: string): string {
  const d = new Date(iso)
  if (!iso || Number.isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

/** The RFC 3339 (UTC) date of a datetime-local value; "" when invalid. */
export function fromLocalInput(v: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(v)) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

export interface ScheduleState {
  open: boolean
  /** RFC 3339 date the next publish schedules for; "" = publish now. */
  at: string
}

export class ScheduleController extends Store<ScheduleState> {
  /** Pages and posts only: a template is live or disabled, a private page stays private. */
  readonly available: boolean

  constructor(boot: Pick<Boot, 'document' | 'template' | 'caps'>, private now: () => number = Date.now) {
    const d = boot.document
    const at = d.status === 'future' && d.date ? d.date : ''
    super({ open: false, at })
    this.available = !boot.template && d.status !== 'private' && !!boot.caps.publish
  }

  open = () => this.set({ open: true })
  close = () => this.set({ open: false })

  /** Sets the date (RFC 3339); a past or invalid date clears the schedule. */
  schedule = (iso: string) => {
    const ms = Date.parse(iso)
    this.set({ at: iso && !Number.isNaN(ms) && ms > this.now() ? iso : '', open: false })
  }

  clear = () => this.set({ at: '', open: false })

  /** What the next publish sends: the date while it is still ahead, else "". */
  pending(): string {
    const at = this.state.at
    return at && Date.parse(at) > this.now() ? at : ''
  }

  /** After a publish: a scheduled page keeps its date, anything else drops it. */
  published(status: string, date?: string) {
    this.set({ at: status === 'future' && date ? date : '' })
  }
}

/* ---------- Revisions ---------- */

export interface RevisionsState {
  open: boolean
  loading: boolean
  error: string
  items: RevisionSummary[]
  /** Revision id being restored. */
  restoring: number
}

export interface RevisionsDeps {
  rest: RestClient
  postId: number
  /** The rev the editor's work is based on (RestoreRequest.baseRev). */
  baseRev: () => number
  /** Stops autosaves before the restore (the restored draft must not be overwritten). */
  stop: () => void
  reload: () => void
  ask: (message: string) => Promise<boolean>
}

export class RevisionsController extends Store<RevisionsState> {
  private seq = 0

  constructor(private d: RevisionsDeps) {
    super({ open: false, loading: false, error: '', items: [], restoring: 0 })
  }

  open = () => {
    this.set({ open: true, error: '' })
    void this.load()
  }

  close = () => {
    if (!this.state.restoring) this.set({ open: false })
  }

  async load() {
    const seq = ++this.seq
    this.set({ loading: true, error: '' })
    try {
      const r = await this.d.rest.request<RevisionSummary[]>('GET', routes.revisions(this.d.postId))
      if (seq !== this.seq) return
      this.set({ loading: false, items: Array.isArray(r.data) ? r.data : [] })
    } catch (e) {
      if (seq !== this.seq) return
      this.set({ loading: false, error: errorText(e, t('The revisions could not be loaded.')) })
    }
  }

  restore = async (rev: RevisionSummary) => {
    if (this.state.restoring) return
    if (!(await this.d.ask(t('Load revision {rev} into the editor? Your unpublished changes are replaced; the page stays as it is until you publish.', { rev: rev.rev })))) return
    this.set({ restoring: rev.id, error: '' })
    this.d.stop()
    try {
      await this.d.rest.request('POST', routes.revisionRestore(this.d.postId, rev.id), { json: { baseRev: this.d.baseRev() } })
      this.d.reload()
    } catch (e) {
      this.set({ restoring: 0, error: errorText(e, t('The revision could not be restored.')) })
    }
  }
}

/* ---------- Popup settings ---------- */

export interface PopupState {
  open: boolean
  loading: boolean
  saving: boolean
  saved: boolean
  error: string
  draft: PopupSettings | null
}

/** Client-side check matching webbuilder.ValidatePopup; "" = valid. */
export function popupProblem(p: PopupSettings): string {
  const int = (n: number, lo: number, hi: number) => Number.isInteger(n) && n >= lo && n <= hi
  if (!int(p.delay, 0, POPUP_LIMITS.maxDelay)) return t('The delay must be 0 to {max} seconds.', { max: POPUP_LIMITS.maxDelay })
  if (p.trigger === 'scroll' && !int(p.scroll, 1, 100)) return t('The scroll depth must be 1 to 100 percent.')
  if (p.trigger === 'click' && !POPUP_SELECTOR_PATTERN.test(p.selector.trim())) return t('Enter an element as #id or .class.')
  if (p.frequency === 'days' && !int(p.days, 1, POPUP_LIMITS.maxDays)) return t('The number of days must be 1 to {max}.', { max: POPUP_LIMITS.maxDays })
  if (!int(p.width, POPUP_LIMITS.minWidth, POPUP_LIMITS.maxWidth)) return t('The width must be {min} to {max} pixels.', { min: POPUP_LIMITS.minWidth, max: POPUP_LIMITS.maxWidth })
  return ''
}

export class PopupController extends Store<PopupState> {
  constructor(
    private rest: RestClient,
    private id: number,
  ) {
    super({ open: false, loading: false, saving: false, saved: false, error: '', draft: null })
  }

  open = () => {
    this.set({ open: true, error: '', saved: false })
    if (!this.state.draft) void this.load()
  }

  close = () => {
    if (!this.state.saving) this.set({ open: false })
  }

  async load() {
    this.set({ loading: true, error: '' })
    try {
      const r = await this.rest.request<PopupSettings>('GET', routes.templatePopup(this.id))
      this.set({ loading: false, draft: r.data })
    } catch (e) {
      this.set({ loading: false, error: errorText(e, t('The popup settings could not be loaded.')) })
    }
  }

  edit = (patch: Partial<PopupSettings>) => {
    if (this.state.draft) this.set({ draft: { ...this.state.draft, ...patch }, saved: false, error: '' })
  }

  save = async () => {
    const draft = this.state.draft
    if (!draft || this.state.saving) return
    const problem = popupProblem(draft)
    if (problem) {
      this.set({ error: problem })
      return
    }
    this.set({ saving: true, error: '' })
    try {
      const r = await this.rest.request<PopupSettings>('PUT', routes.templatePopup(this.id), { json: { ...draft, selector: draft.selector.trim() } })
      this.set({ saving: false, saved: true, draft: r.data })
    } catch (e) {
      this.set({ saving: false, error: errorText(e, t('The popup settings could not be saved.')) })
    }
  }
}
