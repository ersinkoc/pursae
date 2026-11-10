/**
 * Bulk regenerate (Settings › Web Builder › Regenerate pages), docs/developer/web-builder.md. The
 * snapshot of a page is rendered by the editor, so after a renderer update (or a WXR import, which
 * writes documents without a snapshot) the browser draws every outdated page again and publishes it
 * through the ordinary document routes, one after the other, keeping each page's status. The server
 * only lists the work: GET web-builder/v2/outdated (keyset paging, no cap).
 */
import type { DocumentResponse, PublishRequest, PublishResponse, RenderOptions } from './contract'
import { routes } from './contract'
import { buildPublishRequest, loadLiveRenderer, type RendererLoader } from './publish'
import type { RestClient } from './rest'

export const outdatedRoute = () => 'web-builder/v2/outdated'

export interface OutdatedItem {
  id: number
  title: string
  type: string
  status: string
  renderer: number
}

export interface OutdatedResponse {
  renderer: number
  total: number
  items: OutdatedItem[]
}

/** Statuses a page is published again with; others (future, …) are left to the editor. */
const KEEP_STATUS = new Set(['publish', 'private', 'pending', 'draft'])

export interface RegenerateProgress {
  total: number
  done: number
  failed: { id: number; title: string; message: string }[]
  current: string
  finished: boolean
}

export interface RegenerateDeps {
  rest: RestClient
  boot: { rendererVersion: number; rest: { root: string }; links: { site: string } }
  loadRenderer?: RendererLoader
  onProgress: (p: RegenerateProgress) => void
}

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e))

/** Runs the queue to the end. A page that fails is listed and the queue goes on. */
export async function regenerateAll(d: RegenerateDeps): Promise<RegenerateProgress> {
  const { rest, boot } = d
  const live = await (d.loadRenderer ?? loadLiveRenderer)()
  const p: RegenerateProgress = { total: 0, done: 0, failed: [], current: '', finished: false }
  let after = 0
  for (;;) {
    const page = (await rest.request<OutdatedResponse>('GET', `${outdatedRoute()}?after=${after}&limit=50`)).data
    if (after === 0) p.total = page.total
    if (page.items.length === 0) break
    for (const it of page.items) {
      after = it.id
      p.current = it.title
      d.onProgress({ ...p })
      try {
        const res = (await rest.request<DocumentResponse>('GET', routes.document(it.id))).data
        if (!res.doc) throw new Error('The page has no design.')
        if (!KEEP_STATUS.has(res.status)) throw new Error(`Status "${res.status}" is published from the editor.`)
        const opts: RenderOptions = {
          postId: it.id,
          formAction: (sectionId: string) => boot.rest.root + routes.form(it.id, sectionId),
          siteUrl: boot.links.site.replace(/\/+$/, ''),
        }
        const render = await live.renderStaticAsync(res.doc, opts)
        if (render.renderer !== boot.rendererVersion) throw new Error('This editor is out of date. Reload it and try again.')
        const body: PublishRequest = buildPublishRequest({ doc: res.doc, page: res.page }, render, { baseRev: res.rev, status: res.status as PublishRequest['status'], reattach: false })
        await rest.request<PublishResponse>('POST', routes.publish(it.id), { json: body })
      } catch (e) {
        p.failed.push({ id: it.id, title: it.title, message: errMsg(e) })
      }
      p.done++
    }
  }
  p.current = ''
  p.finished = true
  d.onProgress({ ...p })
  return p
}
