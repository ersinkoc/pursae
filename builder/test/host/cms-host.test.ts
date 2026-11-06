import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AUTOSAVE_MIN_MS, createCmsHost, pickInitial, readBoot, type CmsHost } from '../../host/cms-host'
import { MemoryStorage, PAGE, makeBoot, makeDoc, mockFetch, restErr } from './helpers'

const AUTOSAVE = 'web-builder/v2/documents/42/autosave'
const okPrefs = { 'GET web-builder/v2/prefs': { json: {} }, 'GET web-builder/v2/library': { json: [] } }

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-11-06T12:00:00Z'))
})
afterEach(() => {
  vi.useRealTimers()
})

describe('initial document: draft preference', () => {
  it('prefers the user draft when it is newer than the stored doc', () => {
    const draft = { doc: makeDoc('draft'), page: { ...PAGE, title: 'Draft title' }, baseRev: 2, savedAt: '2026-11-02T09:00:00Z' }
    const host = createCmsHost(makeBoot({ draft }), { fetch: mockFetch({}).fetch, local: null })
    expect(host.loadDoc()).toEqual(draft.doc)
    expect(host.loadPage()).toMatchObject({ title: 'Draft title', slug: 'home' })
    // The draft's base rev is kept, so a publish after someone else's publish is a conflict.
    expect(host.session.baseRev).toBe(2)
  })

  it('ignores a draft older than the stored doc', () => {
    const draft = { doc: makeDoc('old'), page: PAGE, baseRev: 1, savedAt: '2026-10-01T09:00:00Z' }
    const init = pickInitial(makeBoot({ draft }).document)
    expect(init.draft).toBeNull()
    expect(init.doc).toEqual(makeDoc('stored'))
    expect(init.baseRev).toBe(3)
  })

  it('starts fresh (null doc) on a new page and takes page settings from the post', () => {
    const host = createCmsHost(makeBoot({ doc: null, rev: 0, page: { ...PAGE, title: 'From post' } }), { fetch: mockFetch({}).fetch, local: null })
    expect(host.loadDoc()).toBeNull()
    expect(host.loadPage()).toMatchObject({ title: 'From post' })
    expect(host.session.baseRev).toBe(0)
  })

  it('shows a restore notice with a discard action that deletes the draft and reloads', async () => {
    const draft = { doc: makeDoc('draft'), page: PAGE, baseRev: 3, savedAt: '2026-11-02T09:00:00Z' }
    const m = mockFetch({ ...okPrefs, 'POST web-builder/v2/documents/42/lock': { json: { lock: null, expires: '' } }, [`DELETE ${AUTOSAVE}`]: { json: { deleted: true } } })
    const reload = vi.fn()
    const host = createCmsHost(makeBoot({ draft }), { fetch: m.fetch, local: null, reload })
    await host.start()
    const n = host.ui.getState().notices.find((x) => x.key === 'draft')!
    expect(n.actions.map((a) => a.id)).toEqual(['keep', 'discard'])
    host.ui.answer(n.id, 'discard')
    await vi.advanceTimersByTimeAsync(0)
    expect(m.find('DELETE', AUTOSAVE)).toHaveLength(1)
    expect(reload).toHaveBeenCalled()
    host.dispose()
  })
})

describe('host surface', () => {
  it('maps the boot config: urls, locale, fonts endpoint, AI off', () => {
    const host = createCmsHost(makeBoot({}, { locale: 'tr_TR' }), { fetch: mockFetch({}).fetch, local: null })
    expect(host.siteUrl).toBe('https://example.test')
    expect(host.exitUrl).toBe('https://example.test/wp-admin/edit?post_type=page')
    expect(host.locale).toBe('tr_TR')
    // Upstream appends "?family=…": the base must not carry a query.
    expect(host.fontsCss).toBe('https://example.test/wp-json/web-builder/v2/fonts/css2')
    expect(host.ai?.enabled).toBe(false)
    expect(host.autosaveDelay).toBeGreaterThanOrEqual(2000)
  })

  it('keeps AI disabled even if the boot config says otherwise', () => {
    const boot = makeBoot({}, { ai: { enabled: true, providers: [], llmBase: 'x', imagesUrl: 'y', allowCode: true } })
    expect(createCmsHost(boot, { fetch: mockFetch({}).fetch, local: null }).ai).toMatchObject({ enabled: false, allowCode: false })
  })

  it('readBoot rejects a missing config and an API mismatch', () => {
    expect(() => readBoot({})).toThrow(/missing/)
    expect(() => readBoot({ __WEB_BUILDER__: makeBoot({}, { apiVersion: 2 }) })).toThrow(/does not match/)
    expect(readBoot({ __WEB_BUILDER__: makeBoot() }).document.postId).toBe(42)
  })
})

describe('autosave', () => {
  function setup(reply: Parameters<typeof mockFetch>[0][string] = { json: { savedAt: '2026-11-06T12:00:00Z', rev: 3 } }, extra: Parameters<typeof mockFetch>[0] = {}) {
    const m = mockFetch({ [`PUT ${AUTOSAVE}`]: reply, ...extra })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null })
    return { m, host }
  }

  it('sends the latest change once, with nonce and API headers', async () => {
    const { m, host } = setup()
    const p1 = host.saveDraft(makeDoc('1'), PAGE)
    const p2 = host.saveDraft(makeDoc('2'), PAGE)
    const p3 = host.saveDraft(makeDoc('3'), PAGE)
    await vi.advanceTimersByTimeAsync(0)
    await Promise.all([p1, p2, p3])
    const puts = m.find('PUT', AUTOSAVE)
    expect(puts).toHaveLength(1)
    expect(puts[0].body).toEqual({ doc: makeDoc('3'), page: PAGE, baseRev: 3 })
    expect(puts[0].headers['x-wp-nonce']).toBe('nonce1')
    expect(puts[0].headers['x-wb-api']).toBe('1')
  })

  it('keeps at least AUTOSAVE_MIN_MS between two requests', async () => {
    const { m, host } = setup()
    await Promise.all([host.saveDraft(makeDoc('1'), PAGE), vi.advanceTimersByTimeAsync(0)])
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(1)
    const second = host.saveDraft(makeDoc('2'), PAGE)
    await vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_MS - 100)
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(1)
    host.saveDraft(makeDoc('3'), PAGE)
    await vi.advanceTimersByTimeAsync(100)
    await second
    const puts = m.find('PUT', AUTOSAVE)
    expect(puts).toHaveLength(2)
    expect((puts[1].body as { doc: unknown }).doc).toEqual(makeDoc('3'))
  })

  it('queues a change made while a request is in flight', async () => {
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    const { m, host } = setup(async () => {
      await gate
      return { json: { savedAt: 'x', rev: 3 } }
    })
    const first = host.saveDraft(makeDoc('1'), PAGE)
    await vi.advanceTimersByTimeAsync(0)
    const second = host.saveDraft(makeDoc('2'), PAGE)
    await vi.advanceTimersByTimeAsync(5000)
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(1)
    release()
    await first
    await vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_MS)
    await second
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(2)
  })

  it('skips a save when nothing changed since the loaded doc', async () => {
    const { m, host } = setup()
    await host.saveDraft(makeDoc('stored'), PAGE)
    await vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_MS)
    expect(m.calls).toHaveLength(0)
  })

  it('stops on 409 wb_api_mismatch and offers a reload', async () => {
    const { m, host } = setup(restErr(409, 'wb_api_mismatch', { reload: true, apiVersion: 2 }))
    await Promise.all([host.saveDraft(makeDoc('1'), PAGE), vi.advanceTimersByTimeAsync(0)])
    expect(host.session.stopped).toBe('api')
    const n = host.ui.getState().notices.find((x) => x.key === 'reload')
    expect(n?.actions[0].id).toBe('reload')
    await host.saveDraft(makeDoc('2'), PAGE)
    await vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_MS * 2)
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(1)
  })

  it('refreshes the nonce on 403 and retries once', async () => {
    const { m, host } = setup((c) => (c.headers['x-wp-nonce'] === 'nonce2' ? { json: { savedAt: 'x', rev: 3 } } : restErr(403, 'rest_cookie_invalid_nonce')), {
      'GET https://example.test/wp-admin/admin-ajax': { text: 'nonce2' },
    })
    await Promise.all([host.saveDraft(makeDoc('1'), PAGE), vi.advanceTimersByTimeAsync(0)])
    const puts = m.find('PUT', AUTOSAVE)
    expect(puts.map((c) => c.headers['x-wp-nonce'])).toEqual(['nonce1', 'nonce2'])
    expect(m.calls.find((c) => c.url.includes('admin-ajax'))?.query.get('action')).toBe('rest-nonce')
    expect(host.rest.nonce()).toBe('nonce2')
  })

  it('rejects with a readable message when the save fails, and retries on the next change', async () => {
    const { m, host } = setup([restErr(500, 'db_error', {}, 'Database down'), { json: { savedAt: 'x', rev: 3 } }])
    const p = host.saveDraft(makeDoc('1'), PAGE)
    const r = expect(p).rejects.toThrow('Changes could not be saved: Database down')
    await vi.advanceTimersByTimeAsync(0)
    await r
    await Promise.all([host.saveDraft(makeDoc('2'), PAGE), vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_MS)])
    expect(m.find('PUT', AUTOSAVE)).toHaveLength(2)
  })

  it('warns once when someone else published since the editor loaded', async () => {
    const { host } = setup({ json: { savedAt: 'x', rev: 5 } })
    await Promise.all([host.saveDraft(makeDoc('1'), PAGE), vi.advanceTimersByTimeAsync(0)])
    await Promise.all([host.saveDraft(makeDoc('2'), PAGE), vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_MS)])
    expect(host.ui.getState().notices.filter((n) => n.key === 'newer-rev')).toHaveLength(1)
    // baseRev stays: the publish must still detect the conflict.
    expect(host.session.baseRev).toBe(3)
  })
})

describe('storage', () => {
  async function started(boot = makeBoot(), routes: Parameters<typeof mockFetch>[0] = okPrefs, local = new MemoryStorage()) {
    const m = mockFetch({ 'POST web-builder/v2/documents/42/lock': { json: { lock: null, expires: '' } }, ...routes })
    const host = createCmsHost(boot, { fetch: m.fetch, local })
    await host.start()
    host.dispose()
    return { m, host, local }
  }

  it('namespaces per-post keys by user and post in localStorage', async () => {
    const local = new MemoryStorage()
    const a = await started(makeBoot(), okPrefs, local)
    const b = await started(makeBoot({ postId: 43 }), okPrefs, local)
    a.host.storage.set('flags', { publish: true })
    b.host.storage.set('flags', { publish: false })
    expect(local.getItem('pursae-builder:u7:p42:flags')).toBe('{"publish":true}')
    expect(local.getItem('pursae-builder:u7:p43:flags')).toBe('{"publish":false}')
    expect(a.host.storage.get('flags', null)).toEqual({ publish: true })
    expect(b.host.storage.get('flags', null)).toEqual({ publish: false })
    // Upstream's un-namespaced keys are never touched.
    expect(local.getItem('pursae-builder:flags')).toBeNull()
  })

  it('keeps user preferences in the prefs endpoint (debounced, merged)', async () => {
    const { m, host, local } = await started(makeBoot(), { ...okPrefs, 'GET web-builder/v2/prefs': { json: { 'pursae-builder:layout-width': 'full' } }, 'PUT web-builder/v2/prefs': { json: {} } })
    expect(host.storage.get('layout-width', 'boxed')).toBe('full')
    host.storage.set('recent-fonts', ['Inter'])
    host.storage.set('recent-fonts', ['Lora', 'Inter'])
    await vi.advanceTimersByTimeAsync(1000)
    const puts = m.find('PUT', 'web-builder/v2/prefs')
    expect(puts).toHaveLength(1)
    expect(puts[0].body).toEqual({ 'pursae-builder:recent-fonts': ['Lora', 'Inter'] })
    expect(puts[0].headers['x-wb-api']).toBe('1')
    expect(local.getItem('pursae-builder:u7:recent-fonts')).toBe('["Lora","Inter"]')
  })

  it('falls back to localStorage when the prefs route is unavailable', async () => {
    const local = new MemoryStorage()
    local.setItem('pursae-builder:u7:layout-width', '"full"')
    const { m, host } = await started(makeBoot(), { 'GET web-builder/v2/prefs': restErr(404, 'rest_no_route'), 'GET web-builder/v2/library': { json: [] } }, local)
    expect(host.storage.get('layout-width', 'boxed')).toBe('full')
    host.storage.set('layout-width', 'boxed')
    await vi.advanceTimersByTimeAsync(2000)
    expect(m.find('PUT', 'web-builder/v2/prefs')).toHaveLength(0)
    expect(local.getItem('pursae-builder:u7:layout-width')).toBe('"boxed"')
  })

  it('syncs saved components with the library', async () => {
    const section = makeDoc('lib').sections[0]
    const { m, host } = await started(makeBoot(), {
      'GET web-builder/v2/prefs': { json: {} },
      'GET web-builder/v2/library': { json: [{ id: '11', name: 'Hero A', section, author: { id: 7, name: 'Ada' }, created: 'x' }] },
      'POST web-builder/v2/library': { status: 201, json: { id: '12', name: 'New', section, author: { id: 7, name: 'Ada' }, created: 'x' } },
      'DELETE web-builder/v2/library/11': { json: { deleted: true } },
      'DELETE web-builder/v2/library/12': { json: { deleted: true } },
    })
    const saved = host.storage.get<{ id: string }[]>('saved', [])
    expect(saved.map((s) => s.id)).toEqual(['11'])
    host.storage.set('saved', saved) // the builder writes its initial state back: no request
    expect(m.calls.filter((c) => c.route.startsWith('web-builder/v2/library') && c.method !== 'GET')).toHaveLength(0)
    const local = { id: 'local-1', name: 'New', section }
    host.storage.set('saved', [...saved, local])
    await vi.advanceTimersByTimeAsync(0)
    expect(m.find('POST', 'web-builder/v2/library')[0].body).toEqual({ name: 'New', section })
    host.storage.set('saved', [])
    await vi.advanceTimersByTimeAsync(0)
    expect(m.calls.filter((c) => c.method === 'DELETE').map((c) => c.route).sort()).toEqual(['web-builder/v2/library/11', 'web-builder/v2/library/12'])
  })
})

describe('media', () => {
  const png = (size = 10, type = 'image/png', name = 'a.png') => new File([new Uint8Array(size)], name, { type })

  function host(over = {}, reply: Parameters<typeof mockFetch>[0][string] = { status: 201, json: { id: 9, source_url: 'https://example.test/wp-content/uploads/a.png', alt_text: 'Alt', mime_type: 'image/png', media_details: { width: 10, height: 20 } } }) {
    const m = mockFetch({ 'POST wp/v2/media': reply })
    return { m, host: createCmsHost(makeBoot({}, over), { fetch: m.fetch, local: null }) as CmsHost }
  }

  it('uploads as multipart field "file", attached to the post', async () => {
    const { m, host: h } = host()
    const asset = await h.media.upload(png(), 'image')
    expect(asset).toEqual({ url: 'https://example.test/wp-content/uploads/a.png', id: 9, alt: 'Alt', width: 10, height: 20 })
    const c = m.find('POST', 'wp/v2/media')[0]
    const fd = c.body as FormData
    expect(fd).toBeInstanceOf(FormData)
    expect((fd.get('file') as File).name).toBe('a.png')
    expect(fd.get('post')).toBe('42')
    expect(c.headers['x-wp-nonce']).toBe('nonce1')
    expect(c.headers['x-wb-api']).toBeUndefined()
  })

  it('checks caps, type and size before uploading', async () => {
    await expect(host({ caps: { publish: true, uploadFiles: false, unfilteredHtml: false, editOthers: false } }).host.media.upload(png(), 'image')).rejects.toThrow(/not allowed/)
    await expect(host().host.media.upload(png(10, 'application/pdf', 'a.pdf'), 'image')).rejects.toThrow(/not an image/)
    await expect(host().host.media.upload(png(10, 'image/gif', 'a.gif'), 'image')).rejects.toThrow(/type is not allowed/)
    await expect(host().host.media.upload(png(2 << 20), 'image')).rejects.toThrow(/larger than the upload limit/)
  })

  it('surfaces the server error message', async () => {
    const { host: h } = host({}, restErr(500, 'rest_upload_unknown_error', {}, 'Disk full'))
    await expect(h.media.upload(png(), 'image')).rejects.toThrow('a.png: Disk full')
  })

  it('pick opens the host media dialog and resolves with the choice', async () => {
    const { host: h } = host()
    const p = h.media.pick!('image')
    expect(h.ui.getState().picker?.kind).toBe('image')
    h.ui.closePicker({ url: 'u', id: 1 })
    await expect(p).resolves.toEqual({ url: 'u', id: 1 })
    expect(h.ui.getState().picker).toBeNull()
  })

  it('readSvg accepts small SVG files only', async () => {
    const { host: h } = host()
    await expect(h.media.readSvg(new File(['<svg viewBox="0 0 1 1"></svg>'], 'i.svg', { type: 'image/svg+xml' }))).resolves.toContain('<svg')
    await expect(h.media.readSvg(new File(['hello'], 'i.svg'))).rejects.toThrow(/not an SVG/)
    await expect(h.media.readSvg(new File([new Uint8Array(70 << 10)], 'big.svg'))).rejects.toThrow(/larger than/)
  })
})

describe('lock', () => {
  it('refreshes the lock on start and every 60 s, and offers take-over when someone else holds it', async () => {
    const m = mockFetch({
      ...okPrefs,
      'POST web-builder/v2/documents/42/lock': [{ json: { lock: null, expires: 'x' } }, { json: { lock: { user: { id: 8, name: 'Bob' }, since: 'x' }, expires: 'x' } }, { json: { lock: null, expires: 'x' } }],
    })
    const host = createCmsHost(makeBoot(), { fetch: m.fetch, local: null })
    await host.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(m.find('POST', 'web-builder/v2/documents/42/lock')).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(60_000)
    const n = host.ui.getState().notices.find((x) => x.key === 'locked')!
    expect(n.message).toContain('Bob')
    host.ui.answer(n.id, 'take')
    await vi.advanceTimersByTimeAsync(0)
    const locks = m.find('POST', 'web-builder/v2/documents/42/lock')
    expect(locks).toHaveLength(3)
    expect(locks[2].body).toEqual({ take: true })
    host.dispose()
  })
})
