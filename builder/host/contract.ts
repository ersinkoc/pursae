/**
 * Pursae Web Builder — integration contract, TypeScript half.
 *
 * The prose and field tables live in docs/developer/web-builder-contract.md; the Go half is
 * internal/webbuilder/contract.go. A change to one is a change to all three, in one commit.
 *
 * The document model (DocState, Section, PageSettings, SavedComponent, …) is imported from the
 * pinned upstream (`@` = builder/upstream/src), so the editor and the contract cannot drift; the
 * Go mirror was copied from upstream src/builder/types.ts at UPSTREAM_TYPES_SHA.
 *
 * Erasable syntax only (no enums / namespaces), so the file also runs under type stripping.
 */
import type { DocState, PageSettings, SavedComponent, Section } from '@/builder/types'

export type { DocState, PageSettings, SavedComponent, Section }

/* ---------- Versions and handshake ---------- */

/** REST/boot schema version; sent as X-WB-Api on every web-builder/v2 request. */
export const API_VERSION = 1
/** Static markup + data-wb-* runtime version; stamped into RenderResult.renderer. */
export const RENDERER_VERSION = 1
export const API_HEADER = 'X-WB-Api'
export const NONCE_HEADER = 'X-WP-Nonce'
export const BOOT_GLOBAL = '__WEB_BUILDER__'
export const UPSTREAM_TYPES_SHA = '22ff4976a1416accd3b7370e176d7206bf390aee'

/* ---------- REST ---------- */

export const REST_NAMESPACE = 'web-builder/v2'

/** Route builders, relative to Boot.rest.root (which ends with "/wp-json/"). */
export const routes = {
  document: (id: number) => `web-builder/v2/documents/${id}`,
  autosave: (id: number) => `web-builder/v2/documents/${id}/autosave`,
  publish: (id: number) => `web-builder/v2/documents/${id}/publish`,
  lock: (id: number) => `web-builder/v2/documents/${id}/lock`,
  revisions: (id: number) => `web-builder/v2/documents/${id}/revisions`,
  revision: (id: number, rid: number) => `web-builder/v2/documents/${id}/revisions/${rid}`,
  revisionRestore: (id: number, rid: number) => `web-builder/v2/documents/${id}/revisions/${rid}/restore`,
  library: () => 'web-builder/v2/library',
  libraryItem: (id: string) => `web-builder/v2/library/${encodeURIComponent(id)}`,
  prefs: () => 'web-builder/v2/prefs',
  fontsCss: () => 'web-builder/v2/fonts/css2',
  form: (postId: number, sectionId: string) => `web-builder/v2/forms/${postId}/${encodeURIComponent(sectionId)}`,
  aiStatus: () => 'web-builder/v2/ai/status',
  aiLlm: (provider: AiProviderId, path: string) => `web-builder/v2/ai/llm/${provider}/${path}`,
  aiImages: () => 'web-builder/v2/ai/images',
  media: () => 'wp/v2/media',
} as const

/* ---------- Asset prefixes ---------- */

export const EDITOR_ASSETS_PATH = '/wp-content/plugins/web-builder/dist/editor/'
export const EDITOR_NOAI_ASSETS_PATH = '/wp-content/plugins/web-builder/dist/editor-noai/'
export const SITE_ASSETS_PATH = '/wp-content/web-builder/site/'
export const FONTS_UPLOAD_PREFIX = '/wp-content/uploads/web-builder/fonts/'

/* ---------- Limits ---------- */

export const LIMITS = {
  maxDocBytes: 4 << 20,
  maxSnapshotHtml: 2 << 20,
  maxCssBytes: 1 << 20,
  inlineCssMax: 24 << 10,
  maxSections: 200,
  maxItems: 500,
  maxDepth: 4,
  maxStringBytes: 64 << 10,
  maxHtmlSectionLen: 256 << 10,
  maxDataUriBytes: 32 << 10,
  maxLibraryItems: 500,
  maxPrefsBytes: 16 << 10,
  maxRevisionsListed: 50,
  maxFormBodyBytes: 64 << 10,
  maxAiBodyBytes: 8 << 20,
  lockTtlSeconds: 150,
  autosaveMinMillis: 2000,
  lockRefreshSeconds: 60,
  aiMaxTokens: 8192,
  aiImageMaxCount: 12,
  aiImageDefaultCount: 6,
  formMinMillis: 2000,
  formNameMax: 200,
  formEmailMax: 254,
  formPhoneMax: 50,
  formMessageMax: 5000,
  formOtherMax: 200,
} as const

export const ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/
export const PREF_KEY_PATTERN = /^[a-z0-9:._-]{1,64}$/

/* ---------- Errors (WordPress REST error shape) ---------- */

export const ERR = {
  apiMismatch: 'wb_api_mismatch', // 409, data.reload = true
  revConflict: 'wb_rev_conflict', // 409
  detached: 'wb_detached', // 409
  locked: 'wb_locked', // 409
  tooLarge: 'wb_too_large', // 413
  invalidDoc: 'wb_invalid_doc', // 422
  notFound: 'wb_not_found', // 404
  notEnabled: 'wb_not_enabled', // 404
  formInvalid: 'wb_form_invalid', // 422
  rateLimited: 'wb_rate_limited', // 429
  aiBudget: 'wb_ai_budget', // 429
  aiPath: 'wb_ai_path', // 400
  aiModel: 'wb_ai_model', // 422
  aiNoKey: 'wb_ai_no_key', // 503
  aiUpstream: 'wb_ai_upstream', // 502
  aiTimeout: 'wb_ai_timeout', // 504
  forbidden: 'rest_forbidden', // 401 / 403
  invalidNonce: 'rest_cookie_invalid_nonce', // 403
} as const
export type ErrCode = (typeof ERR)[keyof typeof ERR]

export interface UserRef {
  id: number
  name: string
}

export interface LockInfo {
  user: UserRef
  /** RFC 3339 */
  since: string
}

export interface ValidationError {
  /** JSON pointer into the request body, e.g. /doc/sections/3/items/0/href */
  path: string
  code: 'required' | 'unknown_kind' | 'bad_id' | 'too_long' | 'too_many' | 'too_deep' | 'bad_url' | 'bad_css' | 'bad_value' | 'bad_renderer'
  message: string
}

export interface RestErrorData {
  status: number
  /** wb_api_mismatch: reload the editor. */
  reload?: boolean
  apiVersion?: number
  /** wb_rev_conflict */
  rev?: number
  modified?: string
  modifiedBy?: UserRef
  /** wb_locked */
  lock?: LockInfo
  /** wb_too_large */
  limit?: number
  /** wb_invalid_doc */
  errors?: ValidationError[]
  /** wb_form_invalid: field name → reason (required | invalid | too_long) */
  fields?: Record<string, string>
}

export interface RestError {
  code: ErrCode | string
  message: string
  data: RestErrorData
}

/* ---------- renderStatic ---------- */

export type Feature = 'menu' | 'acc' | 'tabs' | 'dismiss' | 'form' | 'motion' | 'embed'

export interface FontNeed {
  family: string
  weights: number[]
  italic: boolean
}

/** What upstream live/renderStatic(doc, opts) returns. */
export interface RenderResult {
  /** One root <div class="wb-page …" data-wb-root>…</div>. */
  html: string
  /** Section custom CSS (style.css), scoped to each section; privileged. */
  css: string
  /** Tailwind candidate classes used by html. */
  classes: string[]
  fonts: FontNeed[]
  /** Sorted, unique; empty = the page needs no runtime. */
  features: Feature[]
  renderer: number
}

/** Host-supplied options of renderStatic (the renderer never reads window globals). */
export interface RenderOptions {
  postId: number
  /** Absolute action URL of a form section (Boot.rest.root + routes.form(postId, sectionId)). */
  formAction: (sectionId: string) => string
  /** Home URL, no trailing slash; relative links in the doc stay relative. */
  siteUrl: string
}

/* ---------- Documents ---------- */

export type PublishStatus = 'publish' | 'draft' | 'pending' | 'private'

export interface DraftState {
  doc: DocState
  page: PageSettings
  baseRev: number
  savedAt: string
}

/** GET documents/{id}; also Boot.document. */
export interface DocumentResponse {
  postId: number
  postType: string
  status: string
  /** 0 = never published from the builder. */
  rev: number
  /** null = new page: the editor starts from its default doc. */
  doc: DocState | null
  page: PageSettings
  /** The current user's autosave, only when newer than doc. */
  draft: DraftState | null
  detached: boolean
  modified: string
  renderer: number
  /** Another user's live lock, else null. */
  lock: LockInfo | null
}

export interface AutosaveRequest {
  doc: DocState
  page: PageSettings
  baseRev: number
}

export interface AutosaveResponse {
  savedAt: string
  /** Current published rev; > baseRev means someone else published meanwhile. */
  rev: number
}

export interface PublishRequest {
  baseRev: number
  status: PublishStatus
  /** Required on a detached page: drops the outside edits. */
  reattach?: boolean
  doc: DocState
  page: PageSettings
  render: RenderResult
  /** Tailwind compile() output for render.classes. */
  utilityCss: string
}

export type Stripped = 'html-sections' | 'custom-css' | 'markup' | 'css'

export interface PublishResponse {
  rev: number
  status: string
  permalink: string
  revisionId: number
  modified: string
  stripped: Stripped[]
}

export interface LockRequest {
  take?: boolean
}

export interface LockResponse {
  lock: LockInfo | null
  expires: string
}

export interface RevisionSummary {
  id: number
  rev: number
  author: UserRef
  date: string
  title: string
  renderer: number
}

export interface RevisionResponse {
  id: number
  rev: number
  doc: DocState
  page: PageSettings
}

/** Restore writes the revision as the caller's draft and returns it as a DraftState. */
export interface RestoreRequest {
  baseRev: number
}

export interface LibraryItem extends SavedComponent {
  author: UserRef
  created: string
}

export interface LibraryCreateRequest {
  name: string
  section: Section
}

export type Prefs = Record<string, unknown>

export interface FormResponse {
  ok: boolean
  message: string
}

/* ---------- Boot: window.__WEB_BUILDER__ ---------- */

export type AiProviderId = 'anthropic' | 'openai' | 'google' | 'mistral' | 'ollama'

export interface AiModel {
  id: string
  name: string
}

export interface AiProviderInfo {
  id: AiProviderId
  models: AiModel[]
}

export interface BootAi {
  enabled: boolean
  /** Configured providers only; empty = the demo engine only, nothing leaves the browser. */
  providers: AiProviderInfo[]
  /** Absolute ".../wp-json/web-builder/v2/ai/llm/"; "" when off. */
  llmBase: string
  /** Absolute ai/images URL; "" when off. */
  imagesUrl: string
  /** Always false for now: AI tools may not write html sections or custom CSS. */
  allowCode: boolean
}

export interface Boot {
  apiVersion: number
  rendererVersion: number
  document: DocumentResponse
  user: UserRef
  rest: { root: string; namespace: string; nonce: string }
  links: { site: string; permalink: string; preview: string; exit: string; editPost: string; settings: string }
  caps: { publish: boolean; uploadFiles: boolean; unfilteredHtml: boolean; editOthers: boolean }
  /** WordPress locale, e.g. en_US, tr_TR. */
  locale: string
  fonts: { cssUrl: string; local: string[] }
  media: { maxUploadBytes: number; types: string[] }
  /** Absolute SITE_ASSETS_PATH. */
  siteAssets: string
  ai: BootAi
}

declare global {
  interface Window {
    __WEB_BUILDER__?: Boot
  }
}

/* ---------- Media (existing core route wp/v2/media) ---------- */

/** The subset of a wp/v2/media item the host reads. Upload: multipart field "file". */
export interface MediaItem {
  id: number
  source_url: string
  alt_text: string
  mime_type: string
  media_details: { width?: number; height?: number; sizes?: Record<string, { source_url: string; width: number; height: number }> }
}

/* ---------- AI proxy ---------- */

export const AI_PROVIDER_PATHS: Record<AiProviderId, readonly string[]> = {
  anthropic: ['v1/messages'],
  openai: ['v1/chat/completions'],
  google: ['v1beta/openai/chat/completions'],
  mistral: ['v1/chat/completions'],
  ollama: ['v1/chat/completions'],
}

export interface AiStatusResponse {
  enabled: boolean
  providers: AiProviderInfo[]
  budget: { daily: number; used: number }
}

/** Same shapes as upstream ai/images.ts ImageQuery / ImageResult, plus mediaId. */
export interface AiImageRequest {
  query: string
  count?: number
  orientation?: 'landscape' | 'portrait' | 'square'
}

export interface AiImage {
  url: string
  mediaId: number
  width?: number
  height?: number
  alt: string
  credit: string
  license: string
  source: string
}

export interface AiImageResponse {
  images: AiImage[]
  error?: string
}

/* ---------- data-wb-* runtime ---------- */

export const ATTR = {
  root: 'data-wb-root',
  menu: 'data-wb-menu',
  acc: 'data-wb-acc',
  accSingle: 'data-wb-acc-single',
  tabs: 'data-wb-tabs',
  tab: 'data-wb-tab',
  panel: 'data-wb-panel',
  autoplay: 'data-wb-autoplay',
  dismiss: 'data-wb-dismiss',
  dismissBtn: 'data-wb-dismiss-btn',
  form: 'data-wb-form',
  formStatus: 'data-wb-form-status',
  state: 'data-wb-state',
  motion: 'data-wb-motion',
  dur: 'data-wb-dur',
  delay: 'data-wb-delay',
  ease: 'data-wb-ease',
  repeat: 'data-wb-repeat',
  stagger: 'data-wb-stagger',
  staggerItem: 'data-wb-stagger-item',
  parallax: 'data-wb-parallax',
  parallaxOn: 'data-wb-parallax-on',
  scrollFade: 'data-wb-scroll-fade',
  scrollScale: 'data-wb-scroll-scale',
  hover: 'data-wb-hover',
  hoverOn: 'data-wb-hover-on',
  sticky: 'data-wb-sticky',
  embed: 'data-wb-embed',
} as const

export const CLASS_JS = 'wb-js'
export const CLASS_IN = 'wb-in'
export const FORM_HONEYPOT = 'wb_hp'
export const FORM_TIMING = 'wb_t'
export const FORM_SENT_QUERY = 'wb_form'
export const FORM_FIELDS = ['name', 'email', 'phone', 'message', 'date', 'choice'] as const
export type FormField = (typeof FORM_FIELDS)[number]

/** postMessage from a data-wb-embed iframe reporting its height. */
export interface EmbedHeightMessage {
  __wb: string
  h: number
}

/* ---------- Build output ---------- */

export interface ManifestEntry {
  js: string
  css: string[]
}

export interface Manifest {
  editor: ManifestEntry
  'editor-noai': ManifestEntry
  site: { runtime: string; css: string }
}

export interface BuildInfo {
  upstreamSha: string
  patches: Record<string, string>
  apiVersion: number
  rendererVersion: number
}
