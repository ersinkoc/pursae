/**
 * The CMS host: upstream's BuilderHost (src/builder/host.ts, patch 0001) on top of the boot config
 * the editor screen writes into `window.__WEB_BUILDER__` (docs/developer/web-builder-contract.md §2).
 *
 *   loadDoc/loadPage  the stored doc, or the current user's newer draft
 *   saveDraft         PUT documents/{id}/autosave, at most one request per AUTOSAVE_MIN_MS, coalesced
 *   publish           live renderStatic snapshot → POST documents/{id}/publish (publish.ts)
 *   storage           per-user prefs (web-builder/v2/prefs), saved components (library), per-post keys
 *                     in localStorage; every key namespaced by user (and post)
 *   media             wp/v2/media upload + a React media library dialog (MediaPicker.tsx)
 *   fontsCss          the server's local Google Fonts css2 endpoint
 *   ai                always disabled (AI is out of scope)
 *   dynamic           sample data of dynamic widgets: Boot.dynamic, changed by "Preview as" (dynamic.ts)
 *   template          Theme Builder template mode: Boot.template, display conditions, preview and the
 *                     conditions-first publish step (template.ts); themeParts from Boot.site
 *
 * The host's own messages (reload prompts, conflicts, restored drafts) go through HostUi, which
 * editor-main.tsx renders next to the builder.
 */
import type { BuilderHost, BuilderHostAi, NavItem, BuilderHostStorage, FormResult, MediaAsset, MediaKind, PublishInput, PublishResult } from '@/builder/host'
import type { DocState, PageSettings, SavedComponent } from '@/builder/types'
import { API_VERSION, ERR, LIMITS, PREF_KEY_PATTERN, routes, type AutosaveResponse, type Boot, type DraftState, type NavigatorResponse, type LibraryItem, type LockResponse, type MediaItem, type Prefs } from './contract'
import { publishDocument, loadLiveRenderer, type RendererLoader } from './publish'
import { ScheduleController } from './tools'
import { createRest, isRestError, uploadMedia, type FetchLike, type RestClient } from './rest'
import { DynStore } from './dynamic'
import { TemplateController, readTemplate, templateLive } from './template'

/** Autosave debounce and minimum gap between two autosave requests (contract AutosaveMinMillis). */
export const AUTOSAVE_MIN_MS = LIMITS.autosaveMinMillis
/** Lock heartbeat (contract: the editor refreshes every 60 s). */
export const LOCK_REFRESH_MS = LIMITS.lockRefreshSeconds * 1000
/** Largest .svg file read into the document (a content string, contract §8.1). */
export const MAX_SVG_BYTES = LIMITS.maxStringBytes

/** AI is out of scope: no AI mode, no AI requests. */
/** The page switcher's flat list: templates first (Theme Builder order), then each post type's posts. */
export function navItems(r: NavigatorResponse): NavItem[] {
  const out: NavItem[] = []
  for (const t of r.templates ?? []) {
    out.push({ id: t.id, title: t.title, group: 'Templates', kind: 'template', detail: t.typeLabel, status: t.status, editUrl: t.editUrl, disabled: t.disabled || undefined })
  }
  for (const p of r.posts ?? []) {
    out.push({ id: p.id, title: p.title, group: p.typeLabel, kind: 'post', status: p.status, editUrl: p.editUrl })
  }
  return out
}

export const HOST_AI_OFF: BuilderHostAi = Object.freeze({ enabled: false, serverKeys: true, allowCode: false, cloneEndpoint: null })

/* ======================================================================================
 * Host UI store (notices, prompts, the media dialog) — rendered by editor-main.tsx
 * ==================================================================================== */

export type NoticeTone = 'info' | 'success' | 'warning' | 'error'

export interface NoticeAction {
  id: string
  label: string
  primary?: boolean
  /** A link instead of a button (still resolves the prompt with `id` when clicked). */
  href?: string
  external?: boolean
}

export interface NoticeInput {
  tone: NoticeTone
  title?: string
  message: string
  actions?: NoticeAction[]
  /** Auto-dismiss after this many ms (passive notices only). */
  timeout?: number
  /** Replaces an open notice with the same key instead of stacking. */
  key?: string
}

export interface Notice extends NoticeInput {
  id: number
  actions: NoticeAction[]
  /** A prompt waits for an answer; a passive notice can just be dismissed. */
  prompt: boolean
}

export interface PickerRequest {
  id: number
  kind: MediaKind
}

export interface HostUiState {
  notices: Notice[]
  picker: PickerRequest | null
}

type Timers = { set(f: () => void, ms: number): unknown; clear(t: unknown): void }
const realTimers: Timers = { set: (f, ms) => setTimeout(f, ms), clear: (t) => clearTimeout(t as ReturnType<typeof setTimeout>) }

export class HostUi {
  private state: HostUiState = { notices: [], picker: null }
  private listeners = new Set<() => void>()
  private answers = new Map<number, (id: string | null) => void>()
  private pickerResolve: ((a: MediaAsset | null) => void) | null = null
  private seq = 0
  private timers: Timers
  /** Reload action of showReload(); set by the host. */
  reload: () => void = () => location.reload()

  constructor(timers: Timers = realTimers) {
    this.timers = timers
  }

  subscribe = (f: () => void) => {
    this.listeners.add(f)
    return () => {
      this.listeners.delete(f)
    }
  }

  getState = (): HostUiState => this.state

  private set(patch: Partial<HostUiState>) {
    this.state = { ...this.state, ...patch }
    for (const f of this.listeners) f()
  }

  private push(n: NoticeInput, prompt: boolean): Notice {
    const notice: Notice = { ...n, actions: n.actions ?? [], prompt, id: ++this.seq }
    const rest = n.key ? this.state.notices.filter((x) => x.key !== n.key || (x.prompt && this.answers.has(x.id))) : this.state.notices
    this.set({ notices: [...rest, notice] })
    return notice
  }

  /** A passive notice; returns its id. */
  notify(n: NoticeInput): number {
    const notice = this.push(n, false)
    if (n.timeout) this.timers.set(() => this.dismiss(notice.id), n.timeout)
    return notice.id
  }

  /** A prompt: resolves with the chosen action id, or null when dismissed. */
  ask(n: NoticeInput): Promise<string | null> {
    const notice = this.push(n, true)
    return new Promise((resolve) => this.answers.set(notice.id, resolve))
  }

  /** Answers a prompt (or closes a notice). */
  answer(id: number, actionId: string | null) {
    const resolve = this.answers.get(id)
    this.answers.delete(id)
    this.set({ notices: this.state.notices.filter((n) => n.id !== id) })
    resolve?.(actionId)
  }

  dismiss(id: number) {
    this.answer(id, null)
  }

  /** The blocking "reload the editor" notice (API mismatch, stale bundle). */
  showReload(message = 'The editor was updated on the server. Reload to keep working; your last autosaved draft stays on the server.') {
    void this.ask({ key: 'reload', tone: 'error', title: 'Reload required', message, actions: [{ id: 'reload', label: 'Reload', primary: true }] }).then((a) => {
      if (a === 'reload') this.reload()
    })
  }

  /** Opens the media dialog; resolves with the chosen asset or null. */
  pickMedia(kind: MediaKind): Promise<MediaAsset | null> {
    this.pickerResolve?.(null)
    return new Promise((resolve) => {
      this.pickerResolve = resolve
      this.set({ picker: { id: ++this.seq, kind } })
    })
  }

  closePicker(asset: MediaAsset | null) {
    const resolve = this.pickerResolve
    this.pickerResolve = null
    this.set({ picker: null })
    resolve?.(asset)
  }
}

/* ======================================================================================
 * Session: what the host knows about the document on the server
 * ==================================================================================== */

export type StopReason = 'api' | 'renderer' | 'reload'

export class HostSession {
  /** The rev the next autosave / publish is based on. */
  baseRev: number
  detached: boolean
  /** The user agreed to replace outside edits (publish sends reattach: true). */
  reattach = false
  stopped: StopReason | null = null
  /** JSON of the last {doc, page} the server has (autosaved or published). */
  lastSavedJson = ''
  /** Someone published since the editor loaded (from an autosave answer); notified once. */
  newerRevNotified = false

  constructor(baseRev: number, detached: boolean) {
    this.baseRev = baseRev
    this.detached = detached
  }

  stop(reason: StopReason) {
    this.stopped ??= reason
  }

  published(rev: number, input: { doc: DocState; page: PageSettings }) {
    this.baseRev = rev
    this.detached = false
    this.reattach = false
    this.newerRevNotified = false
    this.lastSavedJson = draftJson(input.doc, input.page)
  }
}

const draftJson = (doc: DocState, page: PageSettings) => JSON.stringify({ doc, page })

/* ======================================================================================
 * Initial document: prefer the user's newer draft
 * ==================================================================================== */

export interface InitialDoc {
  doc: DocState | null
  page: PageSettings
  baseRev: number
  /** The draft that was picked, if any. */
  draft: DraftState | null
}

const time = (s: string | undefined) => {
  const t = s ? Date.parse(s) : NaN
  return Number.isFinite(t) ? t : NaN
}

/**
 * The server sends `draft` only when it is newer than the stored doc; the client re-checks the
 * timestamps (a clock or cache hiccup must never bring back an older draft) and keeps the draft's
 * baseRev, so a publish after someone else's publish is caught as a rev conflict.
 */
export function pickInitial(d: Boot['document']): InitialDoc {
  const draft = d.draft && d.draft.doc && typeof d.draft.doc === 'object' ? d.draft : null
  // Against the doc's own time (docModified), not the post's: a rename outside the builder bumps
  // posts.modified_at, and the server already overlays the post's fields on such a draft.
  const newer = draft && !(time(draft.savedAt) < time(d.docModified))
  if (draft && newer) return { doc: draft.doc, page: { ...d.page, ...draft.page }, baseRev: typeof draft.baseRev === 'number' ? draft.baseRev : d.rev, draft }
  return { doc: d.doc, page: d.page, baseRev: d.rev, draft: null }
}

/* ======================================================================================
 * Autosave
 * ==================================================================================== */

interface Waiter {
  resolve(): void
  reject(e: unknown): void
}

export interface AutosaverDeps {
  rest: RestClient
  postId: number
  session: HostSession
  ui: HostUi
  minInterval?: number
  now?: () => number
  timers?: Timers
}

/** Retry delays after a failed autosave (the last one repeats). */
export const AUTOSAVE_RETRY_MS = [5_000, 15_000, 30_000, 60_000]

/**
 * Coalescing autosave: at most one request in flight and at least `minInterval` between two
 * request starts; only the latest {doc, page} is sent. Each save() resolves when a request that
 * includes its payload succeeded (or nothing needed saving).
 *
 * save() is cheap (no serialization): the builder calls it on every change (autosaveDelay 0), so
 * `pending` is true from the first unsaved edit and the throttling lives here. A failed request keeps
 * its change pending and retries with backoff. hold() keeps requests back while a publish is in
 * flight (the publish deletes the draft; an autosave racing it would be wiped or marked saved).
 */
export class Autosaver {
  private latest: { doc: DocState; page: PageSettings } | null = null
  private waiters: Waiter[] = []
  private inflight: Promise<void> | null = null
  private timer: unknown = null
  private lastStart = -Infinity
  private failures = 0
  private generation = 0
  private held = false
  private readonly min: number
  private readonly now: () => number
  private readonly timers: Timers

  constructor(private d: AutosaverDeps) {
    this.min = d.minInterval ?? AUTOSAVE_MIN_MS
    this.now = d.now ?? (() => Date.now())
    this.timers = d.timers ?? realTimers
  }

  /** True while a change has not reached the server yet. */
  get pending(): boolean {
    return this.latest !== null || this.inflight !== null
  }

  save(doc: DocState, page: PageSettings): Promise<void> {
    if (this.d.session.stopped) return Promise.resolve()
    this.latest = { doc, page }
    const p = new Promise<void>((resolve, reject) => this.waiters.push({ resolve, reject }))
    if (this.failures > 0) this.clearTimer() // a new change does not wait out the retry backoff
    this.schedule(0)
    return p
  }

  /** Sends a pending change now (ignoring the interval and any hold) and waits for it. */
  async flush(opts: { keepalive?: boolean } = {}): Promise<void> {
    this.clearTimer()
    if (this.inflight) await this.inflight.catch(() => undefined)
    if (this.latest) await this.run(opts.keepalive).catch(() => undefined)
  }

  /** While held (a publish in flight) nothing is sent; on release a newer change is saved. */
  hold(on: boolean) {
    this.held = on
    if (on) this.clearTimer()
    else if (this.latest) this.schedule(0)
  }

  /** Drops the pending change and waits for a request in flight (before deleting the draft). */
  async cancel(): Promise<void> {
    this.generation++
    this.clearTimer()
    this.latest = null
    for (const w of this.waiters.splice(0)) w.resolve()
    if (this.inflight) await this.inflight.catch(() => undefined)
  }

  private clearTimer() {
    if (this.timer !== null) {
      this.timers.clear(this.timer)
      this.timer = null
    }
  }

  private schedule(delay: number) {
    if (this.held || this.inflight || this.timer !== null) return
    const wait = Math.max(delay, this.lastStart + this.min - this.now())
    this.timer = this.timers.set(() => {
      this.timer = null
      void this.run().catch(() => undefined)
    }, wait)
  }

  private run(keepalive = false): Promise<void> {
    const item = this.latest
    const { rest, postId, session, ui } = this.d
    const json = item && !session.stopped ? draftJson(item.doc, item.page) : ''
    if (!item || session.stopped || json === session.lastSavedJson) {
      // Nothing to send, or the server already has exactly this (an undo back to the saved state).
      const ws = this.waiters.splice(0)
      for (const w of ws) w.resolve()
      this.latest = null
      this.failures = 0
      return Promise.resolve()
    }
    this.latest = null
    const batch = this.waiters.splice(0)
    const generation = this.generation
    this.lastStart = this.now()
    const task = (async () => {
      try {
        const r = await rest.request<AutosaveResponse>('PUT', routes.autosave(postId), { json: { doc: item.doc, page: item.page, baseRev: session.baseRev }, keepalive })
        if (generation !== this.generation) {
          for (const w of batch) w.resolve()
          return
        }
        session.lastSavedJson = json
        this.failures = 0
        if (typeof r.data?.rev === 'number' && r.data.rev > session.baseRev && !session.newerRevNotified) {
          session.newerRevNotified = true
          ui.notify({
            key: 'newer-rev',
            tone: 'warning',
            title: 'Published elsewhere',
            message: 'Someone else published this page after you opened it. Your changes are saved as your draft; publishing will ask before replacing their version.',
          })
        }
        for (const w of batch) w.resolve()
      } catch (e) {
        if (generation !== this.generation) {
          for (const w of batch) w.resolve()
          return
        }
        if (isRestError(e, ERR.apiMismatch)) {
          session.stop('api')
          ui.showReload()
          for (const w of batch) w.resolve()
          return
        }
        // The change stays pending (unless a newer one replaced it) and is retried with backoff.
        this.latest ??= item
        const retry = AUTOSAVE_RETRY_MS[Math.min(this.failures, AUTOSAVE_RETRY_MS.length - 1)]
        this.failures++
        const err = new Error(isRestError(e) && e.status !== 0 ? `Changes could not be saved: ${e.message}` : 'Changes could not be saved. Check your connection; saving is retried.')
        for (const w of batch) w.reject(err)
        this.inflight = null
        this.schedule(retry)
        throw err
      } finally {
        this.inflight = null
        if (this.latest) this.schedule(this.min)
      }
    })()
    this.inflight = task
    return task
  }
}

/* ======================================================================================
 * Storage: user prefs, saved components (library), per-post keys
 * ==================================================================================== */

/** Upstream storage keys that are per user (prefs on the server); every other key is per post. */
export const USER_KEYS = new Set(['recent-fonts', 'layout-width'])
/** Saved components live in the site library (web-builder/v2/library). */
export const SAVED_KEY = 'saved'
/** Upstream's localStorage prefix; prefs keys on the server keep it (contract §3.2). */
export const PREF_PREFIX = 'pursae-builder:'

export interface StorageDeps {
  rest: RestClient
  userId: number
  postId: number
  ui: HostUi
  /** localStorage, or null when unavailable. */
  local: Storage | null
  timers?: Timers
  /** Debounce of prefs writes. */
  prefsDelay?: number
}

const toSaved = (x: LibraryItem): SavedComponent => ({ id: String(x.id), name: x.name, section: x.section })

export class HostStorage implements BuilderHostStorage {
  private prefs: Prefs = {}
  private prefsOk = false
  private dirty = new Map<string, unknown>()
  private prefsFlight: Promise<void> | null = null
  private prefsTimer: unknown = null
  private libraryOk = false
  private saved: SavedComponent[] = []
  /** local id (from the builder) → library id (from the server) */
  private alias = new Map<string, Promise<string | null>>()
  /** get/set of saved components happened (before init finished, or after). */
  private used = false
  private readonly timers: Timers

  /** Library ids known on the server (only these are ever deleted there). */
  private serverIds = new Set<string>()

  constructor(private d: StorageDeps) {
    this.timers = d.timers ?? realTimers
    // Until init() answers, the builder sees this browser's copy (never an empty list it would
    // then write back over that copy).
    this.saved = this.readLocal<SavedComponent[]>(SAVED_KEY, [])
  }

  /** Local key: namespaced by user, and by post for per-post keys. */
  localKey(key: string): string {
    return USER_KEYS.has(key) || key === SAVED_KEY ? `${PREF_PREFIX}u${this.d.userId}:${key}` : `${PREF_PREFIX}u${this.d.userId}:p${this.d.postId}:${key}`
  }

  /** Loads prefs and the library; each falls back to localStorage when its route fails. */
  async init(): Promise<void> {
    const { rest } = this.d
    const [prefs, lib] = await Promise.allSettled([rest.request<Prefs>('GET', routes.prefs()), rest.request<LibraryItem[]>('GET', routes.library())])
    if (prefs.status === 'fulfilled' && prefs.value.data && typeof prefs.value.data === 'object' && !Array.isArray(prefs.value.data)) {
      this.prefs = { ...prefs.value.data }
      for (const [key, value] of this.dirty) {
        if (value === null) delete this.prefs[key]
        else this.prefs[key] = value
      }
      this.prefsOk = true
      if (this.dirty.size) this.schedulePrefs()
    }
    const items = lib.status === 'fulfilled' && Array.isArray(lib.value.data) ? lib.value.data.filter((x) => x && x.section) : null
    if (!items) return // this.saved already holds the localStorage copy
    for (const x of items) this.serverIds.add(String(x.id))
    this.libraryOk = true
    // The builder already read the list (start-up timed out): it keeps what it holds, and from now
    // on new saved components go to the library. Only server ids are ever deleted there, so the
    // builder's local items are never diffed into DELETEs.
    if (!this.used) this.saved = items.map(toSaved)
  }

  private readLocal<T>(key: string, fallback: T): T {
    try {
      const raw = this.d.local?.getItem(this.localKey(key))
      return raw ? (JSON.parse(raw) as T) : fallback
    } catch {
      return fallback
    }
  }

  private writeLocal(key: string, value: unknown) {
    try {
      if (value === undefined || value === null) this.d.local?.removeItem(this.localKey(key))
      else this.d.local?.setItem(this.localKey(key), JSON.stringify(value))
    } catch {
      /* storage full or blocked: keep working in memory */
    }
  }

  get<T>(key: string, fallback: T): T {
    if (key === SAVED_KEY) {
      this.used = true
      return this.saved as unknown as T
    }
    if (USER_KEYS.has(key)) {
      const k = PREF_PREFIX + key
      if (this.prefsOk && k in this.prefs) return this.prefs[k] as T
      return this.readLocal(key, fallback)
    }
    return this.readLocal(key, fallback)
  }

  set(key: string, value: unknown): void {
    if (key === SAVED_KEY) {
      this.used = true
      return this.setSaved(Array.isArray(value) ? (value as SavedComponent[]) : [])
    }
    this.writeLocal(key, value)
    if (!USER_KEYS.has(key)) return
    const k = PREF_PREFIX + key
    if (!PREF_KEY_PATTERN.test(k)) return
    if (JSON.stringify(this.prefs[k]) === JSON.stringify(value)) return
    this.prefs[k] = value
    this.dirty.set(k, value ?? null)
    if (this.prefsOk) this.schedulePrefs()
  }

  private schedulePrefs() {
    if (this.prefsTimer !== null) return
    this.prefsTimer = this.timers.set(() => {
      this.prefsTimer = null
      void this.flushPrefs()
    }, this.d.prefsDelay ?? 1000)
  }

  /** Writes changed user prefs (merged on the server; null deletes). */
  async flushPrefs(opts: { keepalive?: boolean } = {}): Promise<void> {
    if (this.prefsFlight) {
      await this.prefsFlight
      return this.flushPrefs(opts)
    }
    if (!this.dirty.size) return
    if (this.prefsTimer !== null) {
      this.timers.clear(this.prefsTimer)
      this.prefsTimer = null
    }
    const patch = Object.fromEntries(this.dirty)
    if (new TextEncoder().encode(JSON.stringify({ ...this.prefs })).length > LIMITS.maxPrefsBytes) {
      this.dirty.clear()
      return // kept locally only
    }
    const task = this.sendPrefs(patch, opts)
    this.prefsFlight = task
    try {
      await task
    } finally {
      this.prefsFlight = null
    }
  }

  private async sendPrefs(patch: Record<string, unknown>, opts: { keepalive?: boolean }) {
    try {
      // keepalive: the pagehide flush must survive the page going away.
      await this.d.rest.request<Prefs>('PUT', routes.prefs(), { json: patch, keepalive: opts.keepalive })
      // Clear only what was sent and not changed again meanwhile.
      for (const [k, v] of Object.entries(patch)) if (this.dirty.get(k) === v) this.dirty.delete(k)
    } catch {
      /* the localStorage copy keeps it on this browser; the next change or flush sends it again */
    }
  }

  private setSaved(next: SavedComponent[]) {
    const prev = this.saved
    this.saved = next
    if (!this.libraryOk) return this.writeLocal(SAVED_KEY, next)
    const prevIds = new Set(prev.map((s) => s.id))
    const nextIds = new Set(next.map((s) => s.id))
    for (const s of next) if (!prevIds.has(s.id)) this.addToLibrary(s)
    for (const s of prev) if (!nextIds.has(s.id)) this.removeFromLibrary(s.id)
  }

  private addToLibrary(s: SavedComponent) {
    const task = this.d.rest
      .request<LibraryItem>('POST', routes.library(), { json: { name: s.name, section: s.section } })
      .then((r) => {
        const id = String(r.data.id)
        this.serverIds.add(id)
        return id
      })
      .catch((e: unknown) => {
        this.d.ui.notify({ tone: 'error', message: `“${s.name}” could not be saved to the library${isRestError(e) ? `: ${e.message}` : '.'}`, timeout: 8000 })
        return null
      })
    this.alias.set(s.id, task)
  }

  private removeFromLibrary(id: string) {
    const known = this.alias.get(id)
    this.alias.delete(id)
    void (known ?? Promise.resolve(this.serverIds.has(id) ? id : null)).then((serverId) => {
      if (!serverId) return
      this.serverIds.delete(serverId)
      return this.d.rest.request('DELETE', routes.libraryItem(serverId)).catch((e: unknown) => {
        this.d.ui.notify({ tone: 'error', message: `The saved component could not be removed from the library${isRestError(e) ? `: ${e.message}` : '.'}`, timeout: 8000 })
      })
    })
  }
}

/* ======================================================================================
 * The host
 * ==================================================================================== */

export interface CmsHostDeps {
  fetch?: FetchLike
  local?: Storage | null
  timers?: Timers
  now?: () => number
  loadRenderer?: RendererLoader
  navigate?: (url: string) => void
  reload?: () => void
}

export interface CmsHost extends BuilderHost {
  readonly boot: Boot
  readonly rest: RestClient
  readonly ui: HostUi
  readonly session: HostSession
  readonly autosaver: Autosaver
  readonly storage: HostStorage
  /** What loadDoc/loadPage return. */
  readonly initial: InitialDoc
  /** Sample data of dynamic widgets (BuilderHost.dynamic / onDynamic). */
  readonly dyn: DynStore
  /** Template mode (Boot.template), else null. */
  readonly templates: TemplateController | null
  /** Scheduled publish of pages and posts (tools.ts). */
  readonly schedule: ScheduleController
  /** Async start-up (prefs, library) and the notices about the loaded document; call before mounting. */
  start(): Promise<void>
  /** Takes or refreshes the edit lock; returns the other holder's lock, if any. */
  refreshLock(take?: boolean): Promise<LockResponse | null>
  /** Stops timers (lock heartbeat). */
  dispose(): void
}

export function localStorageOrNull(): Storage | null {
  try {
    const s = globalThis.localStorage
    const probe = '__wb_probe__'
    s.setItem(probe, '1')
    s.removeItem(probe)
    return s
  } catch {
    return null
  }
}

/** Reads and checks the boot config the editor screen wrote. */
export function readBoot(w: { __WEB_BUILDER__?: Boot } = window): Boot {
  const boot = w.__WEB_BUILDER__
  if (!boot || typeof boot !== 'object' || !boot.document || !boot.rest?.root) throw new Error('The Web Builder boot configuration is missing.')
  if (boot.apiVersion !== API_VERSION) throw new Error(`The editor (API ${API_VERSION}) does not match the server (API ${boot.apiVersion}). Reload the page; clear the browser cache if this persists.`)
  return boot
}

/** URL of the wp_rest nonce refresh (admin-ajax next to the site home). */
export const nonceUrl = (boot: Boot) => `${boot.links.site.replace(/\/+$/, '')}/wp-admin/admin-ajax?action=rest-nonce`

/** The css2 endpoint without its query (upstream appends "?family=…"); the nonce is added per link (editor-main). */
export const fontsBase = (boot: Boot) => boot.fonts.cssUrl.split('?')[0]

export function createCmsHost(boot: Boot, deps: CmsHostDeps = {}): CmsHost {
  const timers = deps.timers ?? realTimers
  const rest = createRest({ root: boot.rest.root, nonce: boot.rest.nonce, nonceUrl: nonceUrl(boot), fetch: deps.fetch })
  const ui = new HostUi(timers)
  const reload = deps.reload ?? (() => location.reload())
  const navigate = deps.navigate ?? ((url: string) => location.assign(url))
  ui.reload = reload
  const initial = pickInitial(boot.document)
  const session = new HostSession(initial.baseRev, !!boot.document.detached)
  // The doc the editor opens with is what the server already has.
  if (initial.doc) session.lastSavedJson = draftJson(initial.doc, initial.page)
  const postId = boot.document.postId
  const autosaver = new Autosaver({ rest, postId, session, ui, now: deps.now, timers })
  const storage = new HostStorage({ rest, userId: boot.user.id, postId, ui, local: deps.local === undefined ? localStorageOrNull() : deps.local, timers })
  const loadRenderer = deps.loadRenderer ?? loadLiveRenderer
  let disposed = false
  let lockTimer: unknown = null
  const dyn = new DynStore(rest, boot)
  const template = readTemplate(boot)
  const templates = template ? new TemplateController(rest, dyn, boot, template) : null
  const schedule = new ScheduleController(boot)
  // What the editor sees of the template: live tells it whether Publish is needed although a
  // stored doc or a restored draft is loaded (a duplicate, a disabled or never published template).
  const templateBoot = template ? { ...template, live: templateLive(boot) } : null

  const media = {
    async upload(file: File, kind: MediaKind): Promise<MediaAsset> {
      checkUpload(boot, file, kind)
      try {
        return toAsset(await uploadMedia(rest, file, postId))
      } catch (e) {
        throw new Error(isRestError(e) ? `${file.name}: ${e.message}` : `${file.name} could not be uploaded.`)
      }
    },
    pick: (kind: MediaKind) => ui.pickMedia(kind),
    async readSvg(file: File): Promise<string> {
      if (file.size > MAX_SVG_BYTES) throw new Error(`the file is larger than ${MAX_SVG_BYTES >> 10} KB`)
      const text = await file.text()
      if (!/<svg[\s>]/i.test(text)) throw new Error('not an SVG file')
      return text
    },
  }

  const refreshLock = async (take = false): Promise<LockResponse | null> => {
    if (disposed || session.stopped) return null
    try {
      const r = await rest.request<LockResponse>('POST', routes.lock(postId), { json: take ? { take: true } : {} })
      if (disposed) return null
      if (r.data?.lock) showLocked(r.data.lock.user?.name)
      return r.data
    } catch (e) {
      if (disposed) return null
      if (isRestError(e, ERR.apiMismatch)) {
        session.stop('api')
        ui.showReload()
      }
      return null
    }
  }

  let lockedOpen = false
  const showLocked = (name?: string) => {
    if (disposed || lockedOpen) return // the heartbeat must not stack another prompt while one is open
    lockedOpen = true
    void ui
      .ask({
        key: 'locked',
        tone: 'warning',
        title: 'Someone else is editing',
        message: `${name || 'Another user'} is editing this page. Your changes are saved as your own draft; take over to publish them.`,
        actions: [
          { id: 'take', label: 'Take over', primary: true },
          { id: 'exit', label: 'Exit', href: boot.links.exit },
        ],
      })
      .then((a) => {
        lockedOpen = false
        if (disposed) return
        if (a === 'take') void refreshLock(true)
        else if (a === 'exit') navigate(boot.links.exit)
      })
  }

  const host: CmsHost = {
    boot,
    rest,
    ui,
    session,
    autosaver,
    storage,
    initial,
    dyn,
    templates,
    schedule,
    loadDoc: () => initial.doc,
    loadPage: () => initial.page,
    saveDraft: (doc, page) => autosaver.save(doc, page),
    // The builder hands every change over at once; the Autosaver debounces (AUTOSAVE_MIN_MS), so an
    // edit counts as pending (unload warning, flush) from the moment it is made.
    autosaveDelay: 0,
    async publish(input: PublishInput): Promise<PublishResult & { permalink: string }> {
      await autosaver.flush()
      autosaver.hold(true)
      try {
        const out = await publishDocument({ boot, rest, ui, session, loadRenderer, navigate, reload, previewUrl: templates ? () => templates.getState().preview?.url ?? '' : undefined, scheduleAt: () => (schedule.available ? schedule.pending() : ''), onPublished: (st, date) => schedule.published(st, date) }, input)
        templates?.markPublished()
        return { message: out.message, permalink: out.permalink }
      } finally {
        // Edits made while publishing were held back; one that differs from the published input
        // is saved now as the new draft.
        autosaver.hold(false)
      }
    },
    media,
    fontsCss: fontsBase(boot),
    siteUrl: boot.links.site.replace(/\/+$/, ''),
    exitUrl: boot.links.exit || undefined,
    submitForm: async (): Promise<FormResult> => ({ ok: false, message: 'Forms are sent from the published page.' }),
    ai: HOST_AI_OFF,
    locale: boot.locale || 'en_US',
    dynamic: dyn.get,
    onDynamic: (cb) => dyn.subscribe(cb),
    template: () => templateBoot,
    themeParts: !template && !!boot.site?.themeParts,
    beforePublish: templates ? () => templates.beforePublish() : undefined,
    currentId: postId,
    navigator: async () => navItems((await rest.request<NavigatorResponse>('GET', routes.navigator())).data),
    // Pending edits are sent before the page switcher leaves; a failed flush never blocks leaving
    // (the unload warning covers what stayed unsaved).
    beforeLeave: () => (session.stopped ? Promise.resolve() : autosaver.flush()),
    async start() {
      if (disposed) return
      // An older boot config without sample data: fetch it instead of drawing empty widgets.
      if (!dyn.fromBoot) void dyn.load(template?.preview?.object || `post:${postId}`).catch(() => undefined)
      await storage.init()
      if (disposed) return
      if (initial.draft) {
        void ui
          .ask({
            key: 'draft',
            tone: 'info',
            message: `Your unpublished changes${savedAt(initial.draft.savedAt)} were restored.`,
            actions: [
              { id: 'keep', label: 'Keep', primary: true },
              { id: 'discard', label: 'Discard changes' },
            ],
          })
          .then(async (a) => {
            if (a !== 'discard') return
            session.stop('reload')
            await autosaver.cancel() // a save in flight must land before the DELETE, not after it
            await rest.request('DELETE', routes.autosave(postId)).catch(() => undefined)
            reload()
          })
      }
      if (session.detached) {
        ui.notify({
          key: 'detached',
          tone: 'warning',
          title: 'Edited outside the builder',
          message: 'This page was changed in another editor. The live page shows that version until you publish from the builder, which replaces it.',
          actions: [{ id: 'leave', label: 'Leave the builder', href: boot.links.editPost }],
        })
      }
      if (boot.document.lock) showLocked(boot.document.lock.user?.name)
      else void refreshLock(false)
      lockTimer = timers.set(function beat() {
        if (disposed) return
        void refreshLock(false)
        lockTimer = timers.set(beat, LOCK_REFRESH_MS)
      }, LOCK_REFRESH_MS)
    },
    refreshLock,
    dispose() {
      disposed = true
      if (lockTimer !== null) timers.clear(lockTimer)
      lockTimer = null
    },
  }
  return host
}

const savedAt = (iso: string) => {
  const t = time(iso)
  return Number.isFinite(t) ? ` from ${new Date(t).toLocaleString()}` : ''
}

/** Client-side checks before an upload (the server checks again). */
export function checkUpload(boot: Boot, file: File, kind: MediaKind) {
  if (!boot.caps.uploadFiles) throw new Error('You are not allowed to upload files.')
  if (kind === 'image' && !/^image\//.test(file.type)) throw new Error(`${file.name} is not an image.`)
  const types = boot.media?.types ?? []
  if (types.length && file.type && !types.includes(file.type)) throw new Error(`${file.name}: this file type is not allowed.`)
  const max = boot.media?.maxUploadBytes ?? 0
  if (max > 0 && file.size > max) throw new Error(`${file.name} is larger than the upload limit (${formatBytes(max)}).`)
}

export const formatBytes = (n: number) => (n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(n % (1 << 20) ? 1 : 0)} MB` : `${Math.round(n / 1024)} KB`)

/** A wp/v2/media item as the builder's MediaAsset (the full-size file). */
export function toAsset(m: MediaItem): MediaAsset {
  return { url: m.source_url, id: m.id, alt: m.alt_text || undefined, width: m.media_details?.width, height: m.media_details?.height }
}

/** A small preview of a media item for the dialog grid. */
export function thumbUrl(m: MediaItem): string {
  const sizes = m.media_details?.sizes ?? {}
  for (const k of ['medium', 'medium_large', 'thumbnail']) if (sizes[k]?.source_url) return sizes[k].source_url
  return m.source_url
}
