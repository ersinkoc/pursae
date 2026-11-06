/**
 * REST client of the CMS host: web-builder/v2 and the core wp/v2/media routes
 * (docs/developer/web-builder-contract.md §1, §3).
 *
 * - Every web-builder/v2 request carries X-WP-Nonce and X-WB-Api.
 * - A 401/403 refreshes the wp_rest nonce once (admin-ajax?action=rest-nonce) and retries.
 * - Errors are RestError (WordPress error shape kept in `code` / `data`).
 *
 * No React, no globals: everything comes in through RestOptions, so tests pass a mocked fetch.
 */
import { API_HEADER, API_VERSION, NONCE_HEADER, REST_NAMESPACE, routes, type MediaItem, type RestErrorData } from './contract'

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export interface RestOptions {
  /** Boot.rest.root: absolute, ends with "/wp-json/". */
  root: string
  /** Boot.rest.nonce */
  nonce: string
  /** Absolute nonce refresh URL (…/wp-admin/admin-ajax?action=rest-nonce); "" = never refresh. */
  nonceUrl: string
  fetch?: FetchLike
}

export class RestError extends Error {
  readonly status: number
  readonly code: string
  readonly data: RestErrorData

  constructor(status: number, code: string, message: string, data?: Partial<RestErrorData>) {
    super(message)
    this.name = 'RestError'
    this.status = status
    this.code = code
    this.data = { ...data, status }
  }
}

export const isRestError = (e: unknown, code?: string): e is RestError => e instanceof RestError && (code === undefined || e.code === code)

export interface RequestOptions {
  /** JSON body (sent as application/json). */
  json?: unknown
  /** Raw body (FormData for uploads). */
  body?: BodyInit
  query?: Record<string, string | number | undefined>
  signal?: AbortSignal
  /** fetch keepalive: the request outlives the page (pagehide flushes; small bodies only). */
  keepalive?: boolean
}

export interface RestResponse<T> {
  data: T
  headers: Headers
  status: number
}

export interface RestClient {
  request<T>(method: string, route: string, opts?: RequestOptions): Promise<RestResponse<T>>
  /** Absolute URL of a route (query appended). */
  url(route: string, query?: RequestOptions['query']): string
  /** The current wp_rest nonce (changes after a refresh). */
  nonce(): string
  /** Fetches a new nonce; resolves to the new value, or the old one when the refresh failed. */
  refreshNonce(): Promise<string>
  /** Called with the new nonce after each successful refresh. */
  onNonce(f: (nonce: string) => void): void
}

const DEFAULT_MESSAGE: Record<number, string> = {
  0: 'The server could not be reached. Check your connection.',
  401: 'Your session has expired. Log in again in another tab, then retry.',
  403: 'You are not allowed to do that, or your session has expired.',
  404: 'Not found.',
  409: 'The page changed on the server.',
  413: 'The page is too large to save.',
  422: 'The page contains invalid content.',
  429: 'Too many requests. Wait a moment and retry.',
}

const messageFor = (status: number) => DEFAULT_MESSAGE[status] ?? `The server answered with an error (${status}).`

export function createRest(opts: RestOptions): RestClient {
  const doFetch: FetchLike = opts.fetch ?? ((input, init) => globalThis.fetch(input, init))
  let nonce = opts.nonce
  let refreshing: Promise<string> | null = null
  const listeners: ((n: string) => void)[] = []

  const url = (route: string, query?: RequestOptions['query']) => {
    let u = opts.root + route.replace(/^\/+/, '')
    const q = Object.entries(query ?? {}).filter(([, v]) => v !== undefined && v !== '')
    if (q.length) u += (u.includes('?') ? '&' : '?') + q.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`).join('&')
    return u
  }

  const refreshNonce = (): Promise<string> => {
    if (!opts.nonceUrl) return Promise.resolve(nonce)
    refreshing ??= doFetch(opts.nonceUrl, { credentials: 'same-origin', cache: 'no-store' })
      .then(async (r) => {
        const text = r.ok ? (await r.text()).trim() : ''
        // admin-ajax answers "0" (or a login page) when the session is gone.
        if (/^[A-Za-z0-9]{6,64}$/.test(text) && text !== '0') {
          nonce = text
          for (const f of listeners) f(text)
        }
        return nonce
      })
      .catch(() => nonce)
      .finally(() => {
        refreshing = null
      })
    return refreshing
  }

  async function send<T>(method: string, route: string, o: RequestOptions, retried: boolean): Promise<RestResponse<T>> {
    const sent = nonce
    const headers: Record<string, string> = { Accept: 'application/json', [NONCE_HEADER]: sent }
    if (route.startsWith(REST_NAMESPACE + '/')) headers[API_HEADER] = String(API_VERSION)
    let body: BodyInit | undefined = o.body
    if (o.json !== undefined) {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(o.json)
    }
    let res: Response
    try {
      const init: RequestInit = { method, headers, body, credentials: 'same-origin', cache: 'no-store', signal: o.signal }
      if (o.keepalive) init.keepalive = true
      res = await doFetch(url(route, o.query), init)
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') throw e
      throw new RestError(0, 'wb_network', messageFor(0))
    }
    if ((res.status === 401 || res.status === 403) && !retried && opts.nonceUrl) {
      // Compare with the nonce this request carried: another request may have refreshed it already.
      if (nonce !== sent) return send<T>(method, route, o, true)
      const after = await refreshNonce()
      if (after !== sent) return send<T>(method, route, o, true)
    }
    const text = await res.text()
    let parsed: unknown = null
    if (text) {
      try {
        parsed = JSON.parse(text)
      } catch {
        parsed = null
      }
    }
    if (!res.ok) {
      const err = parsed && typeof parsed === 'object' ? (parsed as { code?: unknown; message?: unknown; data?: unknown }) : {}
      const code = typeof err.code === 'string' ? err.code : `http_${res.status}`
      const message = typeof err.message === 'string' && err.message ? err.message : messageFor(res.status)
      const data = err.data && typeof err.data === 'object' ? (err.data as Partial<RestErrorData>) : undefined
      throw new RestError(res.status, code, message, data)
    }
    return { data: parsed as T, headers: res.headers, status: res.status }
  }

  return {
    request: (method, route, o = {}) => send(method, route, o, false),
    url,
    nonce: () => nonce,
    refreshNonce,
    onNonce: (f) => {
      listeners.push(f)
    },
  }
}

/* ---------- Media (core wp/v2/media, contract §3.4) ---------- */

export interface MediaPage {
  items: MediaItem[]
  page: number
  totalPages: number
  total: number
}

export const MEDIA_PER_PAGE = 24

/** One page of the image library, newest first. */
export async function listMedia(rest: RestClient, q: { search?: string; page?: number; perPage?: number; signal?: AbortSignal } = {}): Promise<MediaPage> {
  const page = Math.max(1, q.page ?? 1)
  const r = await rest.request<MediaItem[]>('GET', routes.media(), {
    query: { media_type: 'image', per_page: q.perPage ?? MEDIA_PER_PAGE, page, search: q.search?.trim() || undefined, orderby: 'date', order: 'desc' },
    signal: q.signal,
  })
  const items = Array.isArray(r.data) ? r.data.filter((m) => m && typeof m.source_url === 'string' && /^image\//.test(m.mime_type ?? '')) : []
  const num = (h: string | null, fb: number) => {
    const n = Number(h)
    return Number.isFinite(n) && h !== null && h !== '' ? n : fb
  }
  return { items, page, totalPages: Math.max(1, num(r.headers.get('X-WP-TotalPages'), 1)), total: num(r.headers.get('X-WP-Total'), items.length) }
}

/** Uploads one file (multipart field "file"), attached to the post being edited. */
export async function uploadMedia(rest: RestClient, file: File, postId: number): Promise<MediaItem> {
  const fd = new FormData()
  fd.append('file', file, file.name)
  if (postId > 0) fd.append('post', String(postId))
  const r = await rest.request<MediaItem>('POST', routes.media(), { body: fd })
  if (!r.data || typeof r.data.source_url !== 'string') throw new RestError(500, 'wb_upload', 'The upload did not return a file URL.')
  return r.data
}

/* ---------- Theme Builder: id search in core collections (contract §11.3 dialog) ---------- */

/** Core wp/v2 collections the conditions dialog and the preview picker search ids in. */
export const SEARCH_COLLECTIONS = ['posts', 'pages', 'categories', 'tags', 'users'] as const
export type SearchCollection = (typeof SEARCH_COLLECTIONS)[number]

export interface FoundItem {
  id: number
  label: string
}

const decodeEntities = (s: string) =>
  s
    .replace(/<[^>]*>/g, '')
    .replace(/&(#x[0-9a-f]+|#[0-9]+|amp|lt|gt|quot|apos|nbsp|hellip|ndash|mdash|lsquo|rsquo|ldquo|rdquo);/gi, (m, e: string) => {
      const named: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ', hellip: '…', ndash: '–', mdash: '—', lsquo: '‘', rsquo: '’', ldquo: '“', rdquo: '”' }
      if (e[0] !== '#') return named[e.toLowerCase()] ?? m
      const n = e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10)
      return Number.isFinite(n) && n > 0 && n <= 0x10ffff ? String.fromCodePoint(n) : m
    })
    .trim()

/** Display label of one wp/v2 item: a post's rendered title, a term's or user's name. */
export function itemLabel(raw: unknown): string {
  if (!raw || typeof raw !== 'object') return ''
  const o = raw as { title?: unknown; name?: unknown }
  if (o.title && typeof o.title === 'object' && typeof (o.title as { rendered?: unknown }).rendered === 'string') return decodeEntities((o.title as { rendered: string }).rendered)
  if (typeof o.title === 'string') return decodeEntities(o.title)
  if (typeof o.name === 'string') return decodeEntities(o.name)
  return ''
}

const toFound = (raw: unknown): FoundItem | null => {
  const id = raw && typeof raw === 'object' ? (raw as { id?: unknown }).id : undefined
  if (typeof id !== 'number' || !Number.isInteger(id) || id <= 0) return null
  return { id, label: itemLabel(raw) || `#${id}` }
}

/** GET wp/v2/{collection}?search= (10 results, only the fields the dialog shows). */
export async function searchItems(rest: RestClient, collection: SearchCollection, search: string, signal?: AbortSignal): Promise<FoundItem[]> {
  const fields = collection === 'posts' || collection === 'pages' ? 'id,title' : 'id,name'
  const query: RequestOptions['query'] = { search: search.trim() || undefined, per_page: 10, _fields: fields }
  const r = await rest.request<unknown[]>('GET', `wp/v2/${collection}`, { query, signal })
  return Array.isArray(r.data) ? r.data.map(toFound).filter((x): x is FoundItem => x !== null) : []
}

/** The label of one item by id (GET wp/v2/{collection}/{id}); '' when it cannot be read. */
export async function itemTitle(rest: RestClient, collection: SearchCollection, id: number, signal?: AbortSignal): Promise<string> {
  try {
    const r = await rest.request<unknown>('GET', `wp/v2/${collection}/${id}`, { query: { _fields: collection === 'posts' || collection === 'pages' ? 'id,title' : 'id,name' }, signal })
    return itemLabel(r.data)
  } catch {
    return ''
  }
}
