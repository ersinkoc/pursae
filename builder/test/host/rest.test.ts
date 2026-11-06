import { describe, expect, it } from 'vitest'
import { RestError, createRest, listMedia } from '../../host/rest'
import { ROOT, mockFetch, restErr } from './helpers'
import { API_VERSION } from '../../host/contract'

const NONCE_URL = 'https://example.test/wp-admin/admin-ajax?action=rest-nonce'

describe('rest client', () => {
  it('sends X-WB-Api only on web-builder/v2 routes', async () => {
    const m = mockFetch({ 'GET web-builder/v2/prefs': { json: {} }, 'GET wp/v2/media': { json: [] } })
    const rest = createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: m.fetch })
    await rest.request('GET', 'web-builder/v2/prefs')
    await rest.request('GET', 'wp/v2/media')
    expect(m.calls[0].headers['x-wb-api']).toBe(String(API_VERSION))
    expect(m.calls[1].headers['x-wb-api']).toBeUndefined()
    expect(m.calls.every((c) => c.headers['x-wp-nonce'] === 'n')).toBe(true)
  })

  it('parses WordPress errors into RestError', async () => {
    const m = mockFetch({ 'POST web-builder/v2/documents/1/publish': restErr(409, 'wb_rev_conflict', { rev: 5 }, 'Conflict') })
    const rest = createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: m.fetch })
    const e = await rest.request('POST', 'web-builder/v2/documents/1/publish', { json: {} }).catch((x: unknown) => x)
    expect(e).toBeInstanceOf(RestError)
    expect(e).toMatchObject({ status: 409, code: 'wb_rev_conflict', message: 'Conflict', data: { status: 409, rev: 5 } })
  })

  it('turns non-JSON errors and network failures into readable messages', async () => {
    const m = mockFetch({ 'GET a': { status: 502, text: '<html>bad gateway</html>' }, 'GET b': new TypeError('Failed to fetch') })
    const rest = createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: m.fetch })
    await expect(rest.request('GET', 'a')).rejects.toMatchObject({ status: 502, code: 'http_502' })
    await expect(rest.request('GET', 'b')).rejects.toMatchObject({ status: 0, code: 'wb_network', message: expect.stringMatching(/connection/) })
  })

  it('refreshes the nonce once per failing request and gives up when it does not change', async () => {
    const m = mockFetch({ 'GET x': restErr(403, 'rest_forbidden'), 'GET https://example.test/wp-admin/admin-ajax': { text: '0' } })
    const rest = createRest({ root: ROOT, nonce: 'n1', nonceUrl: NONCE_URL, fetch: m.fetch })
    await expect(rest.request('GET', 'x')).rejects.toMatchObject({ status: 403 })
    expect(m.find('GET', 'x')).toHaveLength(1)
    expect(rest.nonce()).toBe('n1')
  })

  it('retries a 403 that arrives after another request already refreshed the nonce', async () => {
    let releaseB!: () => void
    const gateB = new Promise<void>((r) => (releaseB = r))
    let n = 0
    const m = mockFetch({
      'GET a': (c) => (c.headers['x-wp-nonce'] === 'fresh1' ? { json: 'a' } : restErr(403, 'rest_cookie_invalid_nonce')),
      'GET b': async (c) => {
        if (c.headers['x-wp-nonce'] === 'fresh1') return { json: 'b' }
        await gateB
        return restErr(403, 'rest_cookie_invalid_nonce')
      },
      'GET https://example.test/wp-admin/admin-ajax': () => ({ text: `fresh${++n}` }),
    })
    const rest = createRest({ root: ROOT, nonce: 'old', nonceUrl: NONCE_URL, fetch: m.fetch })
    const b = rest.request('GET', 'b') // sent with "old", answers late
    await expect(rest.request('GET', 'a')).resolves.toMatchObject({ data: 'a' }) // refreshes to fresh1
    releaseB()
    await expect(b).resolves.toMatchObject({ data: 'b' })
    expect(m.find('GET', 'b').map((c) => c.headers['x-wp-nonce'])).toEqual(['old', 'fresh1'])
    expect(n).toBe(1)
  })

  it('sends keepalive only when asked', async () => {
    const inits: RequestInit[] = []
    const m = mockFetch({ 'PUT p': { json: {} } })
    const rest = createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: (u, i) => (inits.push(i ?? {}), m.fetch(u, i)) })
    await rest.request('PUT', 'p', { json: {} })
    await rest.request('PUT', 'p', { json: {}, keepalive: true })
    expect(inits.map((i) => i.keepalive)).toEqual([undefined, true])
  })

  it('shares one nonce refresh between concurrent requests', async () => {
    let n = 0
    const m = mockFetch({
      'GET x': (c) => (c.headers['x-wp-nonce'] === 'fresh1' ? { json: 1 } : restErr(403, 'rest_cookie_invalid_nonce')),
      'GET https://example.test/wp-admin/admin-ajax': () => ({ text: `fresh${++n}` }),
    })
    const rest = createRest({ root: ROOT, nonce: 'old', nonceUrl: NONCE_URL, fetch: m.fetch })
    const seen: string[] = []
    rest.onNonce((v) => seen.push(v))
    await Promise.all([rest.request('GET', 'x'), rest.request('GET', 'x')])
    expect(n).toBe(1)
    expect(seen).toEqual(['fresh1'])
  })
})

describe('media library list', () => {
  const item = (id: number, mime = 'image/jpeg') => ({ id, source_url: `https://example.test/u/${id}.jpg`, alt_text: '', mime_type: mime, media_details: {} })

  it('asks for images with search and pagination and reads the totals', async () => {
    const m = mockFetch({ 'GET wp/v2/media': { json: [item(1), item(2), item(3, 'application/pdf')], headers: { 'X-WP-Total': '30', 'X-WP-TotalPages': '2' } } })
    const rest = createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: m.fetch })
    const r = await listMedia(rest, { search: '  beach ', page: 2 })
    const q = m.calls[0].query
    expect(q.get('media_type')).toBe('image')
    expect(q.get('search')).toBe('beach')
    expect(q.get('page')).toBe('2')
    expect(q.get('per_page')).toBe('24')
    expect(r).toMatchObject({ page: 2, totalPages: 2, total: 30 })
    expect(r.items.map((x) => x.id)).toEqual([1, 2])
  })

  it('omits an empty search and defaults the totals', async () => {
    const m = mockFetch({ 'GET wp/v2/media': { json: [item(1)] } })
    const rest = createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: m.fetch })
    const r = await listMedia(rest, { search: '   ' })
    expect(m.calls[0].query.has('search')).toBe(false)
    expect(r).toMatchObject({ page: 1, totalPages: 1, total: 1 })
  })
})
