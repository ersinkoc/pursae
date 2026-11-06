import { describe, expect, it, vi } from 'vitest'
import type { DocState } from '@/builder/types'
import { createCmsHost } from '../../host/cms-host'
import type { Boot, RenderOptions, RenderResult } from '../../host/contract'
import { buildPublishRequest } from '../../host/publish'
import { PAGE, ROOT, answerNext, makeBoot, makeDoc, mockFetch, restErr } from './helpers'

const PUBLISH = 'web-builder/v2/documents/42/publish'
const AUTOSAVE = 'web-builder/v2/documents/42/autosave'

const RENDER: RenderResult = {
  html: '<div class="wb-page cv" data-wb-root=""><section id="s-a">Hi</section></div>',
  css: '#s-a{color:red}',
  classes: ['cv', 'wb-page'],
  fonts: [{ family: 'Inter', weights: [400, 700], italic: false }],
  features: ['acc', 'menu'],
  renderer: 1,
}

const ok = (over: Record<string, unknown> = {}) => ({ json: { rev: 4, status: 'publish', permalink: 'https://example.test/home/', revisionId: 99, modified: 'x', stripped: [], ...over } })

function setup(publish: Parameters<typeof mockFetch>[0][string], boot: Boot = makeBoot(), render: Partial<RenderResult> = {}) {
  const m = mockFetch({ [`POST ${PUBLISH}`]: publish, [`DELETE ${AUTOSAVE}`]: { json: { deleted: true } }, 'POST web-builder/v2/documents/42/lock': { json: { lock: null, expires: 'x' } } })
  const seen: { doc: DocState; opts: RenderOptions }[] = []
  const renderStaticAsync = vi.fn(async (doc: DocState, opts: RenderOptions) => {
    seen.push({ doc, opts })
    return { ...RENDER, ...render }
  })
  const reload = vi.fn()
  const navigate = vi.fn()
  const host = createCmsHost(boot, { fetch: m.fetch, local: null, loadRenderer: async () => ({ renderStaticAsync }), reload, navigate })
  return { m, host, seen, reload, navigate, renderStaticAsync }
}

describe('publish payload', () => {
  it('renders the live snapshot and posts the contract payload', async () => {
    const { m, host, seen } = setup(ok())
    const doc = makeDoc('pub')
    const r = await host.publish({ doc, page: PAGE })
    expect(r).toEqual({ message: 'Page published', permalink: 'https://example.test/home/' })
    const c = m.find('POST', PUBLISH)[0]
    expect(c.headers['x-wb-api']).toBe('1')
    expect(c.headers['x-wp-nonce']).toBe('nonce1')
    expect(c.body).toEqual({ baseRev: 3, status: 'publish', doc, page: PAGE, render: RENDER, utilityCss: '' })
    expect(c.body).not.toHaveProperty('reattach')
    // Render options: the renderer reads no globals.
    expect(seen[0].doc).toEqual(doc)
    expect(seen[0].opts.postId).toBe(42)
    expect(seen[0].opts.siteUrl).toBe('https://example.test')
    expect(seen[0].opts.formAction('sec 1')).toBe(`${ROOT}web-builder/v2/forms/42/sec%201`)
    // The next publish is based on the new rev; an unchanged autosave is skipped.
    expect(host.session.baseRev).toBe(4)
    await host.saveDraft(doc, PAGE)
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(0)
    expect(host.ui.getState().notices.find((n) => n.tone === 'success')?.actions[0]).toMatchObject({ href: 'https://example.test/home/' })
  })

  it('keeps private pages private and reports pending for contributors', async () => {
    const priv = setup(ok({ status: 'private' }), makeBoot({ status: 'private' }))
    await expect(priv.host.publish({ doc: makeDoc(), page: PAGE })).resolves.toMatchObject({ message: 'Published privately' })
    expect((priv.m.find('POST', PUBLISH)[0].body as { status: string }).status).toBe('private')
    const contrib = setup(ok({ status: 'pending' }), makeBoot({}, { caps: { publish: false, uploadFiles: true, unfilteredHtml: false, editOthers: false } }))
    await expect(contrib.host.publish({ doc: makeDoc(), page: PAGE })).resolves.toMatchObject({ message: 'Submitted for review' })
  })

  it('buildPublishRequest adds reattach only when set', () => {
    expect(buildPublishRequest({ doc: makeDoc(), page: PAGE }, RENDER, { baseRev: 1, status: 'publish', reattach: true }).reattach).toBe(true)
  })

  it('shows what the server stripped', async () => {
    const { host } = setup(ok({ stripped: ['custom-css', 'markup'] }))
    await host.publish({ doc: makeDoc(), page: PAGE })
    const n = host.ui.getState().notices.find((x) => x.tone === 'warning')!
    expect(n.message).toMatch(/Custom CSS was removed/)
    expect(n.message).toMatch(/markup was cleaned up/)
  })

  it('refuses a snapshot from another renderer version without posting', async () => {
    const { m, host } = setup(ok(), makeBoot(), { renderer: 2 })
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow(/out of date/)
    expect(m.find('POST', PUBLISH)).toHaveLength(0)
    expect(host.session.stopped).toBe('renderer')
  })

  it('reports render failures', async () => {
    const m = mockFetch({})
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null, loadRenderer: async () => ({ renderStaticAsync: async () => { throw new Error('boom') } }) })
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow('The page could not be rendered for publishing: boom')
  })

  it('flushes a pending autosave before publishing', async () => {
    const order: string[] = []
    const m = mockFetch({
      [`PUT ${AUTOSAVE}`]: () => (order.push('autosave'), { json: { savedAt: 'x', rev: 3 } }),
      [`POST ${PUBLISH}`]: () => (order.push('publish'), ok()),
    })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null, loadRenderer: async () => ({ renderStaticAsync: async () => RENDER }) })
    void host.saveDraft(makeDoc('x'), PAGE)
    await host.publish({ doc: makeDoc('x'), page: PAGE })
    expect(order).toEqual(['autosave', 'publish'])
  })
})

describe('publish 409 flows', () => {
  const conflict = restErr(409, 'wb_rev_conflict', { rev: 6, modified: '2026-11-05T08:00:00Z', modifiedBy: { id: 8, name: 'Bob' } })

  it('an edit made while publishing is not marked saved: it is saved as the new draft afterwards', async () => {
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    const order: string[] = []
    const m = mockFetch({
      [`PUT ${AUTOSAVE}`]: (c) => (order.push(`autosave:${JSON.stringify((c.body as { doc: unknown }).doc)}`), { json: { savedAt: 'x', rev: 4 } }),
      [`POST ${PUBLISH}`]: async () => {
        await gate
        order.push('publish')
        return ok()
      },
    })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null, loadRenderer: async () => ({ renderStaticAsync: async () => RENDER }) })
    const r = host.publish({ doc: makeDoc('d1'), page: PAGE })
    await new Promise((res) => setTimeout(res, 0))
    void host.saveDraft(makeDoc('d2'), PAGE) // edited during the publish
    await new Promise((res) => setTimeout(res, 2500))
    expect(order).toEqual([]) // held: the publish would delete it
    release()
    await r
    await new Promise((res) => setTimeout(res, 0))
    expect(order).toEqual(['publish', `autosave:${JSON.stringify(makeDoc('d2'))}`])
    expect(host.autosaver.pending).toBe(false)
  })

  it('rev conflict: "publish mine anyway" retries on the server rev', async () => {
    const { m, host } = setup([conflict, ok({ rev: 7 })])
    const asked = answerNext(host.ui, 'overwrite')
    const r = host.publish({ doc: makeDoc(), page: PAGE })
    expect((await asked).message).toContain('Bob')
    await expect(r).resolves.toMatchObject({ permalink: 'https://example.test/home/' })
    expect(m.find('POST', PUBLISH).map((c) => (c.body as { baseRev: number }).baseRev)).toEqual([3, 6])
    expect(host.session.baseRev).toBe(7)
  })

  it('rev conflict: "load their version" drops the draft and reloads', async () => {
    const { m, host, reload } = setup(conflict)
    let stoppedAtDelete: unknown = 'not called'
    const origRequest = host.rest.request.bind(host.rest)
    host.rest.request = (method, route, o) => {
      if (method === 'DELETE') stoppedAtDelete = host.session.stopped
      return origRequest(method, route, o)
    }
    void answerNext(host.ui, 'reload')
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow(/latest version/)
    // Stopped before the DELETE, so no autosave can write a draft after it.
    expect(stoppedAtDelete).toBe('reload')
    expect(m.find('DELETE', AUTOSAVE)).toHaveLength(1)
    expect(reload).toHaveBeenCalled()
    expect(m.find('POST', PUBLISH)).toHaveLength(1)
  })

  it('rev conflict: cancel rejects without retrying', async () => {
    const { m, host } = setup(conflict)
    void answerNext(host.ui, 'cancel')
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow('Publishing cancelled')
    expect(m.find('POST', PUBLISH)).toHaveLength(1)
  })

  it('detached page: asks first, then publishes with reattach', async () => {
    const { m, host } = setup(ok(), makeBoot({ detached: true }))
    const asked = answerNext(host.ui, 'replace')
    await host.publish({ doc: makeDoc(), page: PAGE })
    expect((await asked).actions).toEqual(['replace', 'leave', 'cancel'])
    expect((m.find('POST', PUBLISH)[0].body as { reattach?: boolean }).reattach).toBe(true)
    expect(host.session.detached).toBe(false)
  })

  it('detached page: "leave the builder" goes to the core editor', async () => {
    const { m, host, navigate } = setup(ok(), makeBoot({ detached: true }))
    void answerNext(host.ui, 'leave')
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow(/Leaving/)
    expect(navigate).toHaveBeenCalledWith('https://example.test/wp-admin/post?post=42&action=edit')
    expect(m.find('POST', PUBLISH)).toHaveLength(0)
  })

  it('409 wb_detached from the server: asks, then retries with reattach', async () => {
    const { m, host } = setup([restErr(409, 'wb_detached'), ok()])
    void answerNext(host.ui, 'replace')
    await host.publish({ doc: makeDoc(), page: PAGE })
    expect(m.find('POST', PUBLISH).map((c) => (c.body as { reattach?: boolean }).reattach)).toEqual([undefined, true])
  })

  it('409 wb_locked: take over, then retry', async () => {
    const { m, host } = setup([restErr(409, 'wb_locked', { lock: { user: { id: 8, name: 'Bob' }, since: 'x' } }), ok()])
    const asked = answerNext(host.ui, 'take')
    await host.publish({ doc: makeDoc(), page: PAGE })
    expect((await asked).message).toContain('Bob')
    expect(m.find('POST', 'web-builder/v2/documents/42/lock')[0].body).toEqual({ take: true })
    expect(m.find('POST', PUBLISH)).toHaveLength(2)
  })

  it('409 wb_api_mismatch: stops and shows the reload prompt', async () => {
    const { host } = setup(restErr(409, 'wb_api_mismatch', { reload: true, apiVersion: 2 }))
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow(/Reload/)
    expect(host.session.stopped).toBe('api')
    expect(host.ui.getState().notices.some((n) => n.key === 'reload')).toBe(true)
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow(/Reload the editor/)
  })

  it('422: shows the first validation error', async () => {
    const { host } = setup(restErr(422, 'wb_invalid_doc', { errors: [{ path: '/doc/sections/0/href', code: 'bad_url', message: 'Link must be http(s)' }, { path: '/x', code: 'too_long', message: 'y' }] }))
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow('The page could not be published: Link must be http(s) (and 1 more)')
  })

  it('413: too large', async () => {
    const { host } = setup(restErr(413, 'wb_too_large', { limit: 4 << 20 }))
    await expect(host.publish({ doc: makeDoc(), page: PAGE })).rejects.toThrow(/too large/)
  })
})
