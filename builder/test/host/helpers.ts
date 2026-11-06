import type { DocState, PageSettings } from '@/builder/types'
import { API_VERSION, type Boot, type DocumentResponse } from '../../host/contract'
import type { HostUi } from '../../host/cms-host'

export const ROOT = 'https://example.test/wp-json/'

export function makeDoc(tag = 'a'): DocState {
  return { site: { brand: '#5b7cfa', tag }, sections: [{ id: `s-${tag}`, kind: 'hero', content: { title: `Hello ${tag}` }, items: [], style: { hideOn: [] } }] } as unknown as DocState
}

export const PAGE: PageSettings = { title: 'Home', slug: 'home', description: '', indexable: true }

export function makeBoot(doc: Partial<DocumentResponse> = {}, over: Partial<Boot> = {}): Boot {
  return {
    apiVersion: API_VERSION,
    rendererVersion: 1,
    document: { postId: 42, postType: 'page', status: 'publish', rev: 3, doc: makeDoc('stored'), page: PAGE, draft: null, detached: false, modified: '2026-11-01T10:00:00Z', docModified: '2026-11-01T10:00:00Z', renderer: 1, lock: null, ...doc },
    user: { id: 7, name: 'Ada' },
    rest: { root: ROOT, namespace: 'web-builder/v2', nonce: 'nonce1' },
    links: { site: 'https://example.test', permalink: 'https://example.test/home/', preview: 'https://example.test/home/?preview=true', exit: 'https://example.test/wp-admin/edit?post_type=page', editPost: 'https://example.test/wp-admin/post?post=42&action=edit', settings: '' },
    caps: { publish: true, uploadFiles: true, unfilteredHtml: false, editOthers: false },
    locale: 'en_US',
    fonts: { cssUrl: 'https://example.test/wp-json/web-builder/v2/fonts/css2?_wpnonce=nonce1', local: [] },
    media: { maxUploadBytes: 1 << 20, types: ['image/jpeg', 'image/png', 'image/webp', 'image/svg+xml'] },
    siteAssets: 'https://example.test/wp-content/web-builder/site/',
    ai: { enabled: false, providers: [], llmBase: '', imagesUrl: '', allowCode: false },
    template: null,
    site: { title: 'Example', tagline: '', home: 'https://example.test', logo: null, locations: [], menus: {}, themeParts: false, postTypes: [], taxonomies: [] },
    dynamic: {
      object: 'post:42',
      site: { title: 'Example', tagline: '', home: 'https://example.test', logo: null, year: '2026' },
      locations: [],
      menus: {},
      view: { kind: 'page', archiveTitle: '', archiveDesc: '', search: '', total: 0 },
      post: null,
      posts: [],
      pagination: { current: 1, total: 1 },
    },
    ...over,
  }
}

export interface Call {
  method: string
  url: string
  /** path relative to the REST root, without the query */
  route: string
  query: URLSearchParams
  headers: Record<string, string>
  body: unknown
}

type Reply = { status?: number; json?: unknown; text?: string; headers?: Record<string, string> } | Error
type Handler = (c: Call) => Reply | Promise<Reply>

/** fetch mock: handlers are matched by "METHOD route" (route relative to ROOT) or a full URL prefix. */
export function mockFetch(routes: Record<string, Handler | Reply | Reply[]>) {
  const calls: Call[] = []
  const queues = new Map<string, Reply[]>()
  const fetch = async (input: string, init: RequestInit = {}): Promise<Response> => {
    const method = (init.method ?? 'GET').toUpperCase()
    const u = new URL(input)
    const route = input.startsWith(ROOT) ? u.pathname.slice(new URL(ROOT).pathname.length) : u.pathname
    const headers: Record<string, string> = {}
    for (const [k, v] of Object.entries((init.headers ?? {}) as Record<string, string>)) headers[k.toLowerCase()] = v
    let body: unknown = init.body
    if (typeof body === 'string' && headers['content-type'] === 'application/json') body = JSON.parse(body)
    const call: Call = { method, url: input, route, query: u.searchParams, headers, body }
    calls.push(call)
    const key = `${method} ${route}`
    let h = routes[key] ?? routes[`${method} ${u.origin}${u.pathname}`]
    if (h === undefined) return new Response(JSON.stringify({ code: 'rest_no_route', message: 'No route', data: { status: 404 } }), { status: 404 })
    let reply: Reply
    if (Array.isArray(h)) {
      if (!queues.has(key)) queues.set(key, [...h])
      const q = queues.get(key)!
      reply = q.length > 1 ? q.shift()! : q[0]
    } else reply = typeof h === 'function' ? await h(call) : h
    if (reply instanceof Error) throw reply
    const text = reply.text ?? (reply.json === undefined ? '' : JSON.stringify(reply.json))
    return new Response(text, { status: reply.status ?? 200, headers: reply.headers })
  }
  return { fetch, calls, find: (method: string, route: string) => calls.filter((c) => c.method === method && c.route === route) }
}

export const restErr = (status: number, code: string, data: Record<string, unknown> = {}, message = code) => ({ status, json: { code, message, data: { status, ...data } } })

/** Answers the next prompt that shows up in `ui` with `action`; resolves with the prompt. */
export function answerNext(ui: HostUi, action: string | null) {
  return new Promise<{ title?: string; message: string; actions: string[] }>((resolve) => {
    const seen = new Set(ui.getState().notices.map((n) => n.id))
    const check = () => {
      const n = ui.getState().notices.find((x) => x.prompt && !seen.has(x.id) && x.key !== 'reload')
      if (!n) return
      off()
      resolve({ title: n.title, message: n.message, actions: n.actions.map((a) => a.id) })
      queueMicrotask(() => ui.answer(n.id, action))
    }
    const off = ui.subscribe(check)
    check()
  })
}

export class MemoryStorage implements Storage {
  private m = new Map<string, string>()
  get length() {
    return this.m.size
  }
  clear() {
    this.m.clear()
  }
  getItem(k: string) {
    return this.m.has(k) ? this.m.get(k)! : null
  }
  key(i: number) {
    return [...this.m.keys()][i] ?? null
  }
  removeItem(k: string) {
    this.m.delete(k)
  }
  setItem(k: string, v: string) {
    this.m.set(k, String(v))
  }
  keys() {
    return [...this.m.keys()]
  }
}

export const flush = () => new Promise((r) => setTimeout(r, 0))
