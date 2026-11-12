import { describe, expect, it, vi } from 'vitest'
import { createCmsHost } from '../../host/cms-host'
import type { PopupSettings, RenderResult } from '../../host/contract'
import { createRest } from '../../host/rest'
import { PopupController, RevisionsController, ScheduleController, fromLocalInput, popupProblem, toLocalInput } from '../../host/tools'
import { PAGE, ROOT, makeBoot, makeDoc, mockFetch } from './helpers'

const PUBLISH = 'web-builder/v2/documents/42/publish'
const RENDER: RenderResult = { html: '<div class="wb-page"></div>', css: '', classes: ['wb-page'], fonts: [], features: [], renderer: 1 }
const NOW = Date.parse('2026-11-12T10:00:00Z')
const LATER = '2026-12-01T09:30:00Z'

describe('ScheduleController', () => {
  it('starts from a scheduled document and drops past dates', () => {
    const s = new ScheduleController(makeBoot({ status: 'future', date: LATER }), () => NOW)
    expect(s.available).toBe(true)
    expect(s.pending()).toBe(LATER)
    s.schedule('2026-01-01T00:00:00Z')
    expect(s.pending()).toBe('')
    s.schedule(LATER)
    s.published('publish')
    expect(s.getState().at).toBe('')
  })

  it('is not offered in templates, on private pages or without publish rights', () => {
    expect(new ScheduleController(makeBoot({ status: 'private' })).available).toBe(false)
    const b = makeBoot()
    expect(new ScheduleController({ ...b, caps: { ...b.caps, publish: false } }).available).toBe(false)
  })

  it('round-trips a datetime-local value', () => {
    const iso = fromLocalInput('2026-12-01T09:30')
    expect(iso).toMatch(/^2026-12-0\dT\d\d:30:00Z$/)
    expect(toLocalInput(iso)).toBe('2026-12-01T09:30')
    expect(fromLocalInput('nope')).toBe('')
  })
})

describe('scheduled publish', () => {
  it('sends status future with the date and keeps the answer', async () => {
    const m = mockFetch({ [`POST ${PUBLISH}`]: { json: { rev: 4, status: 'future', date: '2099-12-01T09:30:00Z', permalink: 'https://example.test/home/', revisionId: 9, modified: 'x', stripped: [] } } })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null, loadRenderer: async () => ({ renderStaticAsync: vi.fn(async () => RENDER) }) })
    host.schedule.schedule('2099-12-01T09:30:00Z')
    const r = await host.publish({ doc: makeDoc(), page: PAGE })
    expect((r as { message: string }).message).toMatch(/^Scheduled at /)
    expect(m.find('POST', PUBLISH)[0].body).toMatchObject({ status: 'future', date: '2099-12-01T09:30:00Z' })
    expect(host.schedule.getState().at).toBe('2099-12-01T09:30:00Z')
  })
})

const rest = (routes: Parameters<typeof mockFetch>[0]) => {
  const m = mockFetch(routes)
  return { m, rest: createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: m.fetch }) }
}

describe('RevisionsController', () => {
  it('lists revisions and restores one as the draft, then reloads', async () => {
    const { m, rest: r } = rest({
      'GET web-builder/v2/documents/42/revisions': { json: [{ id: 7, rev: 2, author: { id: 1, name: 'A' }, date: '2026-11-01T00:00:00Z', title: 'Home', renderer: 1 }] },
      'POST web-builder/v2/documents/42/revisions/7/restore': { json: { doc: {}, page: {}, baseRev: 3, savedAt: 'x' } },
    })
    const stop = vi.fn()
    const reload = vi.fn()
    const c = new RevisionsController({ rest: r, postId: 42, baseRev: () => 3, stop, reload, ask: async () => true })
    c.open()
    await c.load()
    expect(c.getState().items).toHaveLength(1)
    await c.restore(c.getState().items[0])
    expect(stop).toHaveBeenCalled()
    expect(reload).toHaveBeenCalled()
    expect(m.find('POST', 'web-builder/v2/documents/42/revisions/7/restore')[0].body).toEqual({ baseRev: 3 })
  })

  it('does nothing when the user cancels', async () => {
    const { m, rest: r } = rest({})
    const c = new RevisionsController({ rest: r, postId: 42, baseRev: () => 3, stop: vi.fn(), reload: vi.fn(), ask: async () => false })
    await c.restore({ id: 7, rev: 2, author: { id: 1, name: 'A' }, date: '', title: '', renderer: 1 })
    expect(m.calls).toHaveLength(0)
  })
})

describe('PopupController', () => {
  const base: PopupSettings = { trigger: 'load', delay: 2, scroll: 50, selector: '', frequency: 'session', days: 7, version: 1, position: 'center', width: 640, overlay: true, afterConsent: true }

  it('validates like the server', () => {
    expect(popupProblem(base)).toBe('')
    expect(popupProblem({ ...base, trigger: 'click', selector: 'div' })).not.toBe('')
    expect(popupProblem({ ...base, width: 10 })).not.toBe('')
    expect(popupProblem({ ...base, frequency: 'days', days: 0 })).not.toBe('')
  })

  it('loads and saves the settings', async () => {
    const { m, rest: r } = rest({
      'GET web-builder/v2/templates/5/popup': { json: base },
      'PUT web-builder/v2/templates/5/popup': (call) => ({ json: call.body }),
    })
    const c = new PopupController(r, 5)
    await c.load()
    c.edit({ trigger: 'click', selector: ' #go ' })
    await c.save()
    expect(c.getState().saved).toBe(true)
    expect(m.find('PUT', 'web-builder/v2/templates/5/popup')[0].body).toMatchObject({ trigger: 'click', selector: '#go' })
  })
})
