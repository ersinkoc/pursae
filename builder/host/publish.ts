/**
 * Publish flow of the CMS host (docs/developer/web-builder-contract.md §3.1, §4):
 * live renderStatic snapshot → PublishRequest → POST documents/{id}/publish, with the 409 flows
 * (rev conflict, detached page, lock, API mismatch) asked through the host UI.
 *
 * The renderer (upstream src/builder/live/renderStatic, patch 0002) is loaded lazily: it pulls in
 * react-dom/server, which the editor only needs when the user publishes.
 */
import type { DocState, PageSettings } from '@/builder/types'
import { ERR, LIMITS, routes, type Boot, type LockResponse, type PublishRequest, type PublishResponse, type PublishStatus, type RenderOptions, type RenderResult, type Stripped } from './contract'
import { isRestError, type RestClient } from './rest'
import type { HostSession, HostUi } from './cms-host'
import { t } from './i18n'

export interface LiveRenderer {
  renderStaticAsync(doc: DocState, opts: RenderOptions): Promise<RenderResult>
}

export type RendererLoader = () => Promise<LiveRenderer>

/** The real renderer: a separate chunk, fetched on the first publish. */
export const loadLiveRenderer: RendererLoader = () => import('@/builder/live/renderStatic')

export interface PublishContext {
  boot: Boot
  rest: RestClient
  ui: HostUi
  session: HostSession
  loadRenderer: RendererLoader
  /** Leaves the editor (navigation); injectable for tests. */
  navigate(url: string): void
  reload(): void
  /**
   * Template mode: the "Preview as" front URL, shown instead of the publish answer's permalink (a
   * template has no page of its own).
   */
  previewUrl?: () => string
}

export interface PublishOutcome {
  message: string
  permalink: string
  rev: number
  status: string
  stripped: Stripped[]
}

/** Thrown when the user cancels a publish prompt; the message is shown as a toast. */
export class PublishCancelled extends Error {
  constructor(message = 'Publishing cancelled') {
    super(message)
    this.name = 'PublishCancelled'
  }
}

export function renderOptions(boot: Boot): RenderOptions {
  const postId = boot.document.postId
  return {
    postId,
    formAction: (sectionId: string) => boot.rest.root + routes.form(postId, sectionId),
    siteUrl: boot.links.site.replace(/\/+$/, ''),
  }
}

/** Status a publish asks for: private pages stay private; the server turns it into pending without caps.publish. */
export function publishStatus(boot: Boot): PublishStatus {
  return boot.document.status === 'private' ? 'private' : 'publish'
}

/**
 * The PublishRequest body. `utilityCss` stays empty: the per-release site stylesheet
 * (web/web-builder/site/wb.<hash>.css, built from a scan of every file that draws sections)
 * already holds every utility class the renderer can emit, so the editor does not ship the
 * Tailwind compiler (K0 report §3). The server sanitizes it like `render.css` either way.
 */
export function buildPublishRequest(input: { doc: DocState; page: PageSettings }, render: RenderResult, o: { baseRev: number; status: PublishStatus; reattach: boolean }): PublishRequest {
  const body: PublishRequest = { baseRev: o.baseRev, status: o.status, doc: input.doc, page: input.page, render, utilityCss: '' }
  if (o.reattach) body.reattach = true
  return body
}

const byteLength = (s: string) => new TextEncoder().encode(s).length

const STRIPPED_TEXT: Record<Stripped, string> = {
  'html-sections': 'HTML sections were removed (your role may not publish unfiltered HTML).',
  'custom-css': 'Custom CSS was removed (your role may not publish unfiltered HTML).',
  markup: 'Some markup was cleaned up by the server.',
  css: 'Some CSS rules were removed by the server.',
}

export function successMessage(status: string, template = false): string {
  // A template is live (publish) or disabled (draft); it has no review or private path.
  if (template) return status === 'publish' ? t('Template published') : t('Template saved as draft')
  switch (status) {
    case 'pending':
      return 'Submitted for review'
    case 'draft':
      return 'Saved as draft'
    case 'private':
      return 'Published privately'
    case 'future':
      return 'Scheduled'
    default:
      return 'Page published'
  }
}

const when = (iso?: string) => {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : ` at ${d.toLocaleString()}`
}

/** Asks what to do about a page edited outside the builder; resolves when the user chose to replace those edits. */
async function confirmReattach(ctx: PublishContext): Promise<void> {
  const choice = await ctx.ui.ask({
    tone: 'warning',
    title: 'Edited outside the builder',
    message: 'This page was changed in another editor. Publishing from the builder replaces those changes.',
    actions: [
      { id: 'replace', label: 'Publish and replace', primary: true },
      { id: 'leave', label: 'Leave the builder', href: ctx.boot.links.editPost },
      { id: 'cancel', label: 'Cancel' },
    ],
  })
  if (choice === 'leave') {
    ctx.navigate(ctx.boot.links.editPost)
    throw new PublishCancelled('Leaving the builder')
  }
  if (choice !== 'replace') throw new PublishCancelled()
  ctx.session.reattach = true
}

export async function publishDocument(ctx: PublishContext, input: { doc: DocState; page: PageSettings }): Promise<PublishOutcome> {
  const { boot, rest, ui, session } = ctx
  if (session.stopped) {
    ui.showReload()
    throw new Error('Reload the editor before publishing.')
  }
  if (session.detached && !session.reattach) await confirmReattach(ctx)

  let render: RenderResult
  try {
    const live = await ctx.loadRenderer()
    render = await live.renderStaticAsync(input.doc, renderOptions(boot))
  } catch (e) {
    throw new Error(`The page could not be rendered for publishing${e instanceof Error && e.message ? `: ${e.message}` : '.'}`)
  }
  if (render.renderer !== boot.rendererVersion) {
    session.stop('renderer')
    ui.showReload('This editor is out of date. Reload it to publish; your changes are kept as a draft.')
    throw new Error('The editor is out of date. Reload it to publish.')
  }

  const status = publishStatus(boot)
  for (let attempt = 0; attempt < 4; attempt++) {
    const body = buildPublishRequest(input, render, { baseRev: session.baseRev, status, reattach: session.reattach })
    const raw = JSON.stringify(body)
    if (byteLength(raw) > LIMITS.maxDocBytes) throw new Error(`The page is too large to publish (limit ${LIMITS.maxDocBytes >> 20} MB). Remove some sections or large images.`)
    try {
      const r = await rest.request<PublishResponse>('POST', routes.publish(boot.document.postId), { json: body })
      const out = r.data
      session.published(out.rev, input)
      const isTemplate = !!boot.template
      const message = successMessage(out.status, isTemplate)
      const permalink = (isTemplate && ctx.previewUrl?.()) || out.permalink
      const stripped = Array.isArray(out.stripped) ? out.stripped : []
      if (stripped.length) {
        ui.notify({ tone: 'warning', title: message, message: stripped.map((k) => STRIPPED_TEXT[k] ?? k).join(' ') })
      }
      if (permalink) {
        ui.notify({ tone: 'success', message: `${message}.`, actions: [{ id: 'view', label: isTemplate ? t('Preview on the site') : 'View page', href: permalink, external: true }], timeout: 8000 })
      }
      return { message, permalink, rev: out.rev, status: out.status, stripped }
    } catch (e) {
      if (!isRestError(e)) throw e
      if (e.code === ERR.apiMismatch) {
        session.stop('api')
        ui.showReload()
        throw new Error('The editor was updated on the server. Reload to publish.')
      }
      if (e.code === ERR.revConflict) {
        const by = e.data.modifiedBy?.name
        const choice = await ui.ask({
          tone: 'warning',
          title: 'This page was published by someone else',
          message: `${by ? by : 'Another user'} published this page${when(e.data.modified)} after you started editing. Publishing now replaces their version.`,
          actions: [
            { id: 'overwrite', label: 'Publish mine anyway', primary: true },
            { id: 'reload', label: 'Load their version' },
            { id: 'cancel', label: 'Cancel' },
          ],
        })
        if (choice === 'overwrite') {
          if (typeof e.data.rev === 'number') session.baseRev = e.data.rev
          continue
        }
        if (choice === 'reload') {
          // Their version wins: drop this user's draft so the reload does not bring it back. Stop
          // first, so no autosave can write a new draft after the DELETE (the host holds autosaves
          // while publishing, so none is in flight).
          session.stop('reload')
          await rest.request('DELETE', routes.autosave(boot.document.postId)).catch(() => undefined)
          ctx.reload()
          throw new PublishCancelled('Loading the latest version…')
        }
        throw new PublishCancelled()
      }
      if (e.code === ERR.detached) {
        session.detached = true
        session.reattach = false
        await confirmReattach(ctx)
        continue
      }
      if (e.code === ERR.locked) {
        const holder = e.data.lock?.user?.name ?? 'Another user'
        const choice = await ui.ask({
          tone: 'warning',
          title: 'Someone else is editing',
          message: `${holder} is editing this page right now. Take over to publish your version.`,
          actions: [
            { id: 'take', label: 'Take over and publish', primary: true },
            { id: 'cancel', label: 'Cancel' },
          ],
        })
        if (choice !== 'take') throw new PublishCancelled()
        await rest.request<LockResponse>('POST', routes.lock(boot.document.postId), { json: { take: true } })
        continue
      }
      if (e.code === ERR.invalidDoc) {
        const first = e.data.errors?.[0]
        if (first?.code === 'bad_renderer') {
          session.stop('renderer')
          ui.showReload('This editor is out of date. Reload it to publish; your changes are kept as a draft.')
        }
        const extra = (e.data.errors?.length ?? 0) > 1 ? ` (and ${e.data.errors!.length - 1} more)` : ''
        throw new Error(first ? `The page could not be published: ${first.message}${extra}` : e.message)
      }
      if (e.code === ERR.tooLarge) throw new Error('The page is too large to publish. Remove some sections or large images.')
      throw new Error(e.message)
    }
  }
  throw new Error('The page could not be published. Try again.')
}
