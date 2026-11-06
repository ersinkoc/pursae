/**
 * DynContext provider of the CMS host (contract §11.6): the sample data dynamic widgets are drawn
 * with in the editor (BuilderHost.dynamic / onDynamic).
 *
 * It starts from the boot config's `dynamic` and changes only when the preview object changes
 * ("Preview as": PUT templates/{id}/preview answers the new context; set()) or when the host asks
 * for another object (load(): GET preview-context?object=). The object returned by get() stays
 * the same until then, as useSyncExternalStore needs.
 *
 * A boot config without a usable `dynamic` (an older server) starts from an empty context built
 * from Boot.site, never from invented sample content.
 */
import { routes, type Boot, type DynContext, type PreviewObject } from './contract'
import type { RestClient } from './rest'
import { t } from './i18n'

const isObj = (x: unknown): x is Record<string, unknown> => !!x && typeof x === 'object' && !Array.isArray(x)

/** Shape check of a DynContext from the server (enough for the widgets not to throw). */
export function isDynContext(x: unknown): x is DynContext {
  if (!isObj(x) || !isObj(x.site) || !isObj(x.view) || !isObj(x.menus) || !isObj(x.pagination)) return false
  return Array.isArray(x.locations) && Array.isArray(x.posts) && (x.post === null || isObj(x.post)) && typeof x.site.home === 'string'
}

/** An empty context with the site's own identity (title, tagline, logo, menus). */
export function emptyDynamic(boot: Pick<Boot, 'site' | 'links'>, object: PreviewObject = ''): DynContext {
  const s = boot.site
  return {
    object,
    site: { title: s?.title ?? '', tagline: s?.tagline ?? '', home: s?.home || boot.links.site.replace(/\/+$/, ''), logo: s?.logo ?? null, year: String(new Date().getFullYear()) },
    locations: Array.isArray(s?.locations) ? s.locations : [],
    menus: isObj(s?.menus) ? s.menus : {},
    view: { kind: 'page', archiveTitle: '', archiveDesc: '', search: '', total: 0 },
    post: null,
    posts: [],
    pagination: { current: 1, total: 1 },
  }
}

export class DynStore {
  private ctx: DynContext
  private listeners = new Set<(ctx: DynContext) => void>()
  private seq = 0
  /** The boot config had no usable `dynamic`. */
  readonly fromBoot: boolean

  constructor(
    private rest: RestClient,
    boot: Pick<Boot, 'site' | 'links' | 'dynamic'>,
  ) {
    this.fromBoot = isDynContext(boot.dynamic)
    this.ctx = this.fromBoot ? boot.dynamic : emptyDynamic(boot)
  }

  get = (): DynContext => this.ctx

  subscribe = (cb: (ctx: DynContext) => void): (() => void) => {
    this.listeners.add(cb)
    return () => {
      this.listeners.delete(cb)
    }
  }

  /** Replaces the context (a preview change answered it); ignored when it is malformed. */
  set(ctx: unknown): boolean {
    if (!isDynContext(ctx)) return false
    this.seq++ // a load() still in flight must not overwrite a newer context
    this.apply(ctx)
    return true
  }

  private apply(ctx: DynContext) {
    if (ctx === this.ctx) return
    this.ctx = ctx
    for (const f of this.listeners) f(ctx)
  }

  /** GET preview-context?object= and use it; resolves with the new context (throws a RestError). */
  async load(object: PreviewObject, signal?: AbortSignal): Promise<DynContext> {
    const id = ++this.seq
    const r = await this.rest.request<DynContext>('GET', routes.previewContext(), { query: { object }, signal })
    if (!isDynContext(r.data)) throw new Error(t('The preview data could not be read.'))
    if (id === this.seq) this.apply(r.data)
    return r.data
  }
}
