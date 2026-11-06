import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createCmsHost, navItems } from '../../host/cms-host'
import type { NavigatorResponse } from '../../host/contract'
import { PAGE, makeBoot, makeDoc, mockFetch } from './helpers'

const NAV = 'web-builder/v2/navigator'
const AUTOSAVE = 'web-builder/v2/documents/42/autosave'

const reply: NavigatorResponse = {
  templates: [
    { id: 5, title: 'Main header', type: 'header', typeLabel: 'Header', status: 'draft', editUrl: 'https://example.test/wp-admin/post?post=5&action=web-builder', disabled: true },
    { id: 6, title: 'Footer', type: 'footer', typeLabel: 'Footer', status: 'publish', editUrl: 'https://example.test/wp-admin/post?post=6&action=web-builder', disabled: false },
  ],
  posts: [
    { id: 42, title: 'Home', postType: 'page', typeLabel: 'Pages', status: 'publish', editUrl: 'https://example.test/wp-admin/post?post=42&action=web-builder' },
    { id: 9, title: 'Hello', postType: 'post', typeLabel: 'Posts', status: 'draft', editUrl: 'https://example.test/wp-admin/post?post=9&action=web-builder' },
  ],
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-11-06T12:00:00Z'))
})
afterEach(() => vi.useRealTimers())

describe('page switcher host seam', () => {
  it('maps the response to a flat list: templates first, then posts under their type label', () => {
    const items = navItems(reply)
    expect(items.map((i) => [i.kind, i.id, i.group])).toEqual([
      ['template', 5, 'Templates'],
      ['template', 6, 'Templates'],
      ['post', 42, 'Pages'],
      ['post', 9, 'Posts'],
    ])
    expect(items[0]).toMatchObject({ detail: 'Header', disabled: true, status: 'draft' })
    expect(items[1].disabled).toBeUndefined()
    expect(navItems({ templates: [], posts: [] })).toEqual([])
  })

  it('fetches the list from the navigator route and knows the current document', async () => {
    const m = mockFetch({ [`GET ${NAV}`]: { json: reply } })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null })
    expect(host.currentId).toBe(42)
    const items = await host.navigator!()
    expect(items).toHaveLength(4)
    expect(m.find('GET', NAV)).toHaveLength(1)
    expect(m.find('GET', NAV)[0].headers['x-wb-api']).toBeTruthy()
  })

  it('rejects when the list cannot be loaded', async () => {
    const m = mockFetch({ [`GET ${NAV}`]: { status: 500, json: { code: 'db_error', message: 'x' } } })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null })
    await expect(host.navigator!()).rejects.toBeTruthy()
  })

  it('beforeLeave sends a pending autosave first and resolves when nothing is pending', async () => {
    const m = mockFetch({ [`PUT ${AUTOSAVE}`]: { json: { savedAt: '2026-11-06T12:00:00Z', rev: 3 } } })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null })
    await Promise.all([host.saveDraft(makeDoc('1'), PAGE), vi.advanceTimersByTimeAsync(0)])
    void host.saveDraft(makeDoc('2'), PAGE) // throttled, pending
    expect(host.autosaver.pending).toBe(true)
    await host.beforeLeave!()
    expect(host.autosaver.pending).toBe(false)
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(2)
    await host.beforeLeave!()
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(2)
  })

  it('beforeLeave does not reject when the save fails', async () => {
    const m = mockFetch({ [`PUT ${AUTOSAVE}`]: { status: 500, json: { code: 'db_error', message: 'x' } } })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null })
    void Promise.resolve(host.saveDraft(makeDoc('1'), PAGE)).catch(() => undefined)
    await expect(host.beforeLeave!()).resolves.toBeUndefined()
  })
})
