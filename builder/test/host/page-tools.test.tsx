import { describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { RestError, type RestClient } from '../../host/rest'
import { routes } from '../../host/contract'
import {
  PageToolsController,
  diffSeo,
  previewImageUrl,
  safeEditUrl,
  seoDraftOf,
  seoRating,
  seoScoreOf,
  tabsOf,
  type LanguagesInfo,
  type PageToolsDeps,
  type SeoMeta,
} from '../../host/page-tools'
import { PageToolsDialog, PageToolsMenuRow } from '../../host/PageToolsDialog'

const ID = 42

const languages = (over: Partial<LanguagesInfo> = {}): LanguagesInfo => ({
  enabled: true,
  canTranslate: true,
  current: { slug: 'en', name: 'English', flag: 'EN' },
  translations: [
    { slug: 'en', name: 'English', flag: 'EN', postId: ID, title: 'Home', status: 'publish', editUrl: '/wp-admin/post?post=42&action=web-builder', current: true },
    { slug: 'de', name: 'Deutsch', flag: 'DE', postId: 50, title: 'Startseite', status: 'draft', editUrl: '/wp-admin/post?post=50&action=web-builder' },
  ],
  missing: [{ slug: 'tr', name: 'Türkçe', flag: 'TR' }],
  ...over,
})

const meta = (fields: Record<string, string> = {}, over: Partial<SeoMeta> = {}): SeoMeta => ({ id: ID, fields: { title: 'T', description: 'D', focus: 'go', ...fields }, seo_score: '73', ...over })

type Handler = (body: unknown) => unknown | Promise<unknown>

/** A rest client answering "METHOD route" from a table; unknown routes are 404. */
function fakeRest(table: Record<string, Handler>) {
  const request = vi.fn(async (method: string, route: string, opts?: { json?: unknown }) => {
    const h = table[`${method} ${route}`]
    if (!h) throw new RestError(404, 'rest_no_route', 'No route was found matching the URL and request method.')
    return { data: await h(opts?.json), headers: new Headers(), status: 200 }
  })
  return { request } as unknown as RestClient & { request: typeof request }
}

const FULL_SEO = { languages: false, seo: true, seoEdit: true, seoAdvanced: true, seoSocial: true }
const PLUGINS = `GET ${routes.documentPlugins(ID)}`
const LANGS = `GET ${routes.documentLanguages(ID)}`
const TRANSLATE = `POST ${routes.documentTranslations(ID)}`
const SEO_GET = `GET ${routes.seoMeta(ID)}`
const SEO_POST = `POST ${routes.seoMeta(ID)}`

const make = (table: Record<string, Handler>, over: Partial<PageToolsDeps> = {}) => {
  const rest = fakeRest(table)
  const navigate = vi.fn()
  const c = new PageToolsController({ rest, postId: ID, navigate, ...over })
  return { c, rest, navigate }
}

describe('routes', () => {
  it('point at the plugin routes the server registers', () => {
    expect(routes.documentPlugins(7)).toBe('web-builder/v2/documents/7/plugins')
    expect(routes.documentLanguages(7)).toBe('web-builder/v2/documents/7/languages')
    expect(routes.documentTranslations(7)).toBe('web-builder/v2/documents/7/translations')
    expect(routes.seoMeta(7)).toBe('seogo/v1/meta/7')
  })
})

describe('helpers', () => {
  it('tabsOf shows only what the page reports', () => {
    expect(tabsOf(null)).toEqual([])
    expect(tabsOf({ languages: false, seo: false, seoEdit: false })).toEqual([])
    expect(tabsOf({ languages: true, seo: false, seoEdit: false })).toEqual(['languages'])
    expect(tabsOf({ languages: false, seo: true, seoEdit: true })).toEqual(['seo'])
    expect(tabsOf({ languages: true, seo: true, seoEdit: true })).toEqual(['languages', 'seo'])
    // SEO that may not be edited has no tab (the dialog only edits).
    expect(tabsOf({ languages: false, seo: true, seoEdit: false })).toEqual([])
  })

  it('maps SEO fields to a draft: only an explicit noindex counts', () => {
    expect(seoDraftOf(undefined)).toMatchObject({ title: '', og_image: '', noindex: false, nofollow: false })
    expect(seoDraftOf({ noindex: 'noindex', nofollow: '1', focus: 'go' })).toMatchObject({ noindex: true, nofollow: true, focus: 'go' })
    expect(seoDraftOf({ noindex: 'index' }).noindex).toBe(false)
  })

  it('diffSeo sends only changes, robots in the plugin\'s own values', () => {
    const f = { title: 'T', noindex: 'index' }
    expect(diffSeo(f, seoDraftOf(f))).toEqual({})
    expect(diffSeo(f, { ...seoDraftOf(f), title: 'New', og_image: 'https://x/y.jpg' })).toEqual({ title: 'New', og_image: 'https://x/y.jpg' })
    expect(diffSeo(f, { ...seoDraftOf(f), noindex: true, nofollow: true })).toEqual({ noindex: 'noindex', nofollow: '1' })
    const on = { noindex: 'noindex', nofollow: '1' }
    expect(diffSeo(on, { ...seoDraftOf(on), noindex: false, nofollow: false })).toEqual({ noindex: '', nofollow: '' })
  })

  it('reads the score the server stores as text', () => {
    expect(seoScoreOf({ seo_score: '73' })).toBe(73)
    expect(seoScoreOf({ seo_score: 88 })).toBe(88)
    expect(seoScoreOf({ seo_score: '' })).toBeNull()
    expect(seoScoreOf({})).toBeNull()
    expect(seoScoreOf({ seo_score: 'x' })).toBeNull()
    expect(seoScoreOf({ seo_score: '250' })).toBe(100)
    expect([seoRating(0), seoRating(40), seoRating(41), seoRating(70), seoRating(71), seoRating(100)]).toEqual(['bad', 'bad', 'ok', 'ok', 'good', 'good'])
  })

  it('previews only same-origin images and ones picked from the media library', () => {
    const origin = 'https://site.test'
    const picked = new Set(['https://cdn.test/og.jpg'])
    expect(previewImageUrl('', origin)).toBe('')
    expect(previewImageUrl('/wp-content/uploads/a.jpg', origin)).toBe('https://site.test/wp-content/uploads/a.jpg')
    expect(previewImageUrl('https://site.test/a.jpg', origin)).toBe('https://site.test/a.jpg')
    expect(previewImageUrl('https://cdn.test/og.jpg', origin, picked)).toBe('https://cdn.test/og.jpg')
    for (const bad of ['https://evil.test/p.gif', 'http://127.0.0.1:8080/x', '//evil.test/x', 'javascript:alert(1)', 'data:image/png;base64,AAAA', 'https://cdn.test/other.jpg']) {
      expect(previewImageUrl(bad, origin, picked)).toBe('')
    }
    expect(previewImageUrl('/a.jpg', '')).toBe('')
  })

  it('only same-site paths and http(s) addresses are followed', () => {
    expect(safeEditUrl('/wp-admin/post?post=5&action=web-builder')).toBe('/wp-admin/post?post=5&action=web-builder')
    expect(safeEditUrl('https://example.test/wp-admin/post?post=5')).toBe('https://example.test/wp-admin/post?post=5')
    for (const bad of ['javascript:alert(1)', '//evil.test/x', '/\\evil.test', 'data:text/html,x', '', undefined, 5]) expect(safeEditUrl(bad)).toBe('')
  })
})

describe('PageToolsController: probe and open', () => {
  it('has no tab (and no row) until the page reports one', async () => {
    const { c } = make({ [PLUGINS]: () => ({ languages: false, seo: false, seoEdit: false }) })
    expect(c.available).toBe(false)
    await c.probe()
    expect(c.available).toBe(false)
    c.open()
    expect(c.getState().open).toBe(false)
  })

  it('a failed probe hides the row', async () => {
    const { c } = make({})
    await c.probe()
    expect(c.getState().panels).toBeNull()
    expect(c.available).toBe(false)
  })

  it('opens on the first tab and loads it; select loads the other', async () => {
    const { c, rest } = make({ [PLUGINS]: () => ({ languages: true, seo: true, seoEdit: true }), [LANGS]: () => languages(), [SEO_GET]: () => meta() })
    await c.probe()
    expect(c.tabs).toEqual(['languages', 'seo'])
    c.open()
    expect(c.getState().tab).toBe('languages')
    await vi.waitFor(() => expect(c.getState().languages.load).toBe('ready'))
    c.select('seo')
    await vi.waitFor(() => expect(c.getState().seo.load).toBe('ready'))
    expect(c.getState().seo.draft).toMatchObject({ title: 'T', focus: 'go' })
    expect(rest.request).toHaveBeenCalledWith('GET', 'seogo/v1/meta/42')
    // Reopening keeps the last tab.
    c.close()
    c.open()
    expect(c.getState().tab).toBe('seo')
  })

  it('opens on the only tab, and ignores a tab the page does not have', async () => {
    const { c } = make({ [PLUGINS]: () => ({ languages: false, seo: true, seoEdit: true }), [SEO_GET]: () => meta() })
    await c.probe()
    c.open('languages')
    expect(c.getState().tab).toBe('seo')
  })
})

let stuckPending = false

describe('PageToolsController: languages', () => {
  const ready = async (table: Record<string, Handler>, over: Partial<PageToolsDeps> = {}) => {
    const m = make({ [PLUGINS]: () => ({ languages: true, seo: false, seoEdit: false }), [LANGS]: () => languages(), ...table }, over)
    await m.c.probe()
    m.c.open()
    await vi.waitFor(() => expect(m.c.getState().languages.load).toBe('ready'))
    return m
  }

  it('lists translations and missing languages', async () => {
    const { c } = await ready({})
    const d = c.getState().languages.data!
    expect(d.translations.map((x) => x.slug)).toEqual(['en', 'de'])
    expect(d.missing.map((x) => x.slug)).toEqual(['tr'])
  })

  it('shows a load error', async () => {
    const m = make({ [PLUGINS]: () => ({ languages: true, seo: false, seoEdit: false }), [LANGS]: () => { throw new RestError(500, 'x', 'Boom') } })
    await m.c.probe()
    m.c.open()
    await vi.waitFor(() => expect(m.c.getState().languages.load).toBe('error'))
    expect(m.c.getState().languages.error).toBe('Boom')
  })

  it('creates the translation and opens it', async () => {
    const { c, rest, navigate } = await ready({ [TRANSLATE]: () => ({ postId: 51, editUrl: '/wp-admin/post?post=51&action=web-builder' }) })
    await c.addTranslation('tr')
    expect(rest.request).toHaveBeenLastCalledWith('POST', 'web-builder/v2/documents/42/translations', { json: { lang: 'tr' } })
    expect(navigate).toHaveBeenCalledWith('/wp-admin/post?post=51&action=web-builder')
    expect(c.getState().languages.adding).toBe('tr') // the page is being left
  })

  it('flushes pending edits before creating the translation', async () => {
    const order: string[] = []
    let pending = true
    const { c } = await ready(
      { [TRANSLATE]: () => (order.push('post'), { postId: 51, editUrl: '/e' }) },
      {
        pending: () => pending,
        flush: async () => {
          order.push('flush')
          pending = false
        },
      },
    )
    await c.addTranslation('tr')
    expect(order).toEqual(['flush', 'post'])
  })

  it('does not flush when nothing is pending', async () => {
    const flush = vi.fn(async () => undefined)
    const { c } = await ready({ [TRANSLATE]: () => ({ postId: 51, editUrl: '/e' }) }, { pending: () => false, flush })
    await c.addTranslation('tr')
    expect(flush).not.toHaveBeenCalled()
  })

  it('creates nothing when the pending edits cannot be saved', async () => {
    const post = vi.fn(() => ({ postId: 51, editUrl: '/e' }))
    const { c, navigate } = await ready({ [TRANSLATE]: post }, { pending: () => true, flush: async () => undefined })
    await c.addTranslation('tr')
    expect(post).not.toHaveBeenCalled()
    expect(navigate).not.toHaveBeenCalled()
    expect(c.getState().languages.adding).toBe('')
    expect(c.getState().languages.error).toMatch(/could not be saved/)
  })

  it('shows the server error and stays', async () => {
    const { c, navigate } = await ready({ [TRANSLATE]: () => { throw new RestError(400, 'wb_bad_language', 'Unknown language.') } })
    await c.addTranslation('xx')
    expect(c.getState().languages.error).toBe('Unknown language.')
    expect(c.getState().languages.adding).toBe('')
    expect(navigate).not.toHaveBeenCalled()
  })

  it('reloads the list when the translation already exists', async () => {
    let n = 0
    const { c, rest } = await ready({
      [LANGS]: () => (n++ === 0 ? languages() : languages({ missing: [] })),
      [TRANSLATE]: () => { throw new RestError(409, 'wb_translation_exists', 'This page already has a translation in that language.') },
    })
    await c.addTranslation('tr')
    expect(c.getState().languages.data?.missing).toEqual([])
    expect(rest.request.mock.calls.filter((x) => x[1] === routes.documentLanguages(ID))).toHaveLength(2)
  })

  it('refuses an unsafe edit URL and lists the translation instead of navigating', async () => {
    const { c, navigate } = await ready({ [TRANSLATE]: () => ({ postId: 51, editUrl: 'javascript:alert(1)' }) })
    await c.addTranslation('tr')
    expect(navigate).not.toHaveBeenCalled()
    expect(c.getState().languages.adding).toBe('')
  })

  it('ignores a second add while one runs', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => (release = r))
    const post = vi.fn(async () => {
      await gate
      return { postId: 51, editUrl: '/e' }
    })
    const { c } = await ready({ [TRANSLATE]: post })
    const first = c.addTranslation('tr')
    await c.addTranslation('fr')
    release()
    await first
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('opens an existing translation after flushing', async () => {
    let pending = true
    const flush = vi.fn(async () => {
      pending = false
    })
    const { c, navigate } = await ready({}, { flush, pending: () => pending })
    await c.openTranslation('/wp-admin/post?post=50&action=web-builder')
    expect(flush).toHaveBeenCalled()
    expect(navigate).toHaveBeenCalledWith('/wp-admin/post?post=50&action=web-builder')
    navigate.mockClear()
    await c.openTranslation('javascript:alert(1)')
    expect(navigate).not.toHaveBeenCalled()
  })

  it('stays when the pending edits cannot be saved (rejected or ignored flush), instead of dropping them', async () => {
    for (const flush of [
      vi.fn(async () => {
        throw new Error('offline')
      }),
      vi.fn(async () => undefined),
    ]) {
      const { c, navigate } = await ready({}, { flush, pending: () => true })
      await c.openTranslation('/wp-admin/post?post=50&action=web-builder')
      expect(flush).toHaveBeenCalled()
      expect(navigate).not.toHaveBeenCalled()
      expect(c.getState().languages.error).toMatch(/could not be saved/)
    }
  })

  it('sends edits made while the translation was created before leaving, and stays when that fails', async () => {
    const order: string[] = []
    let pending = false
    const { c, navigate } = await ready(
      { [TRANSLATE]: () => (order.push('post'), (pending = true), { postId: 51, editUrl: '/e' }) },
      {
        pending: () => pending,
        flush: async () => {
          order.push('flush')
          pending = false
        },
      },
    )
    await c.addTranslation('tr')
    expect(order).toEqual(['post', 'flush'])
    expect(navigate).toHaveBeenCalledWith('/e')

    const stuck = await ready({ [TRANSLATE]: () => ((stuckPending = true), { postId: 52, editUrl: '/f' }) }, { pending: () => stuckPending, flush: async () => undefined })
    await stuck.c.addTranslation('tr')
    expect(stuck.navigate).not.toHaveBeenCalled()
    expect(stuck.c.getState().languages.adding).toBe('')
    expect(stuck.c.getState().languages.error).toMatch(/was created, but/)
  })

  it('does not close while a translation is being created', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => (release = r))
    const { c } = await ready({ [TRANSLATE]: async () => (await gate, { postId: 1, editUrl: '/e' }) })
    const p = c.addTranslation('tr')
    c.close()
    expect(c.getState().open).toBe(true)
    release()
    await p
  })
})

describe('PageToolsController: SEO', () => {
  const ready = async (table: Record<string, Handler> = {}, over: Partial<PageToolsDeps> = {}) => {
    const m = make({ [PLUGINS]: () => FULL_SEO, [SEO_GET]: () => meta(), ...table }, over)
    await m.c.probe()
    m.c.open()
    await vi.waitFor(() => expect(m.c.getState().seo.load).toBe('ready'))
    return m
  }

  it('loads, tracks edits and saves only the difference', async () => {
    let current = meta()
    const { c, rest } = await ready({
      [SEO_POST]: (body) => {
        const f = (body as { fields: Record<string, string> }).fields
        current = meta({ ...current.fields, ...f }, { seo_score: '80' })
        return current
      },
    })
    expect(c.seoDirty).toBe(false)
    c.editSeo({ title: 'New title', noindex: true, og_image: 'https://x/y.jpg' })
    expect(c.seoDirty).toBe(true)
    await c.saveSeo()
    expect(rest.request).toHaveBeenLastCalledWith('POST', 'seogo/v1/meta/42', { json: { fields: { title: 'New title', og_image: 'https://x/y.jpg', noindex: 'noindex' } } })
    const s = c.getState().seo
    expect(s.saved).toBe(true)
    expect(seoScoreOf(s.data)).toBe(80)
    expect(s.draft).toMatchObject({ title: 'New title', noindex: true })
    expect(c.seoDirty).toBe(false)
  })

  it('does not post without changes', async () => {
    const post = vi.fn(() => meta())
    const { c } = await ready({ [SEO_POST]: post })
    await c.saveSeo()
    expect(post).not.toHaveBeenCalled()
  })

  it('keeps the draft and shows the error when saving fails', async () => {
    const { c } = await ready({ [SEO_POST]: () => { throw new RestError(403, 'rest_forbidden', 'Sorry, you are not allowed to do that.') } })
    c.editSeo({ focus: 'new' })
    await c.saveSeo()
    const s = c.getState().seo
    expect(s.error).toBe('Sorry, you are not allowed to do that.')
    expect(s.draft?.focus).toBe('new')
    expect(s.saved).toBe(false)
    expect(s.saving).toBe(false)
  })

  it('editing clears the saved mark and the error', async () => {
    const { c } = await ready({ [SEO_POST]: () => meta({ focus: 'a' }) })
    c.editSeo({ focus: 'a' })
    await c.saveSeo()
    expect(c.getState().seo.saved).toBe(true)
    c.editSeo({ focus: 'b' })
    expect(c.getState().seo.saved).toBe(false)
  })

  it('picks the social image from the media library', async () => {
    const { c } = await ready({}, { pickImage: async () => ({ id: 9, url: 'https://x/og.jpg' }) })
    expect(c.canPick).toBe(true)
    await c.pickSocialImage()
    expect(c.getState().seo.draft?.og_image).toBe('https://x/og.jpg')
  })

  it('has no library button without a picker', async () => {
    const { c } = await ready()
    expect(c.canPick).toBe(false)
  })

  it('keeps unsaved edits when the tab is shown again or the dialog reopened', async () => {
    let loads = 0
    const { c } = await ready({ [SEO_GET]: () => (loads++, meta()) })
    c.editSeo({ title: 'Typed, not saved' })
    c.select('seo')
    c.close()
    c.open()
    await Promise.resolve()
    expect(loads).toBe(1)
    expect(c.getState().seo.draft?.title).toBe('Typed, not saved')
    expect(c.seoDirty).toBe(true)
  })

  it('keeps edits typed while a reload is in flight', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => (release = r))
    let n = 0
    const { c } = await ready({ [SEO_GET]: async () => (n++ === 0 ? meta() : (await gate, meta({ title: 'Server' }))) })
    const reload = c.loadSeo()
    c.editSeo({ title: 'Mine' })
    release()
    await reload
    expect(c.getState().seo.draft?.title).toBe('Mine')
    expect(c.getState().seo.load).toBe('ready')
  })

  it('reloads a clean form', async () => {
    let title = 'A'
    const { c } = await ready({ [SEO_GET]: () => meta({ title }) })
    title = 'B'
    await c.loadSeo()
    expect(c.getState().seo.draft?.title).toBe('B')
  })

  it('does not edit the fields the user may not save', async () => {
    const m = make({ [PLUGINS]: () => ({ ...FULL_SEO, seoAdvanced: false, seoSocial: false }), [SEO_GET]: () => meta() })
    await m.c.probe()
    m.c.open()
    await vi.waitFor(() => expect(m.c.getState().seo.load).toBe('ready'))
    expect([m.c.canEditSeo('title'), m.c.canEditSeo('focus'), m.c.canEditSeo('canonical'), m.c.canEditSeo('noindex'), m.c.canEditSeo('og_image')]).toEqual([true, true, false, false, false])
    m.c.editSeo({ canonical: 'https://x', noindex: true, og_title: 'x', og_image: 'https://x/y.jpg' })
    expect(m.c.seoDirty).toBe(false)
    m.c.editSeo({ title: 'ok', og_title: 'dropped' })
    expect(m.c.getState().seo.draft).toMatchObject({ title: 'ok', og_title: '' })
    const html = renderToStaticMarkup(<PageToolsDialog controller={m.c} />)
    expect(html).toContain('You do not have permission to change the social sharing settings.')
    expect(html).toMatch(/<input id="wb-seo-og_image"[^>]*disabled=""/)
    expect(html).toMatch(/<input id="wb-seo-noindex"[^>]*disabled=""/)
    expect(html).not.toMatch(/<input id="wb-seo-title"[^>]*disabled=""/)
  })

  it('previews only a same-origin image or one picked from the library', async () => {
    const { c } = await ready({}, { origin: 'https://site.test', pickImage: async () => ({ id: 9, url: 'https://cdn.test/og.jpg' }) })
    expect(c.previewUrl('https://tracker.test/p.gif')).toBe('')
    expect(c.previewUrl('/wp-content/uploads/a.jpg')).toBe('https://site.test/wp-content/uploads/a.jpg')
    expect(c.previewUrl('https://cdn.test/og.jpg')).toBe('')
    await c.pickSocialImage()
    expect(c.previewUrl('https://cdn.test/og.jpg')).toBe('https://cdn.test/og.jpg')
    c.editSeo({ og_image: 'https://tracker.test/p.gif' })
    expect(renderToStaticMarkup(<PageToolsDialog controller={c} />)).not.toContain('<img')
  })

  it('does not close while saving', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => (release = r))
    const { c } = await ready({ [SEO_POST]: async () => (await gate, meta({ focus: 'z' })) })
    c.editSeo({ focus: 'z' })
    const p = c.saveSeo()
    c.close()
    expect(c.getState().open).toBe(true)
    release()
    await p
    c.close()
    expect(c.getState().open).toBe(false)
  })

  it('drops a stale load', async () => {
    const gates: (() => void)[] = []
    let n = 0
    const { c } = await ready({
      [SEO_GET]: async () => {
        const i = n++
        if (i === 0) return meta()
        await new Promise<void>((r) => gates.push(r))
        return meta({ focus: `load${i}` })
      },
    })
    const a = c.loadSeo()
    const b = c.loadSeo()
    gates[1]()
    await b
    gates[0]()
    await a
    expect(c.getState().seo.draft?.focus).toBe('load2')
  })
})

describe('PageToolsMenuRow and dialog markup', () => {
  it('draws no row until a tab is known, then a Page tools menu item', async () => {
    const { c } = make({ [PLUGINS]: () => ({ languages: true, seo: false, seoEdit: false }) })
    expect(renderToStaticMarkup(<PageToolsMenuRow controller={c} close={() => undefined} />)).toBe('')
    await c.probe()
    const html = renderToStaticMarkup(<PageToolsMenuRow controller={c} close={() => undefined} />)
    expect(html).toContain('role="menuitem"')
    expect(html).toContain('Page tools')
  })

  it('draws no row when the page has no tab', async () => {
    const { c } = make({ [PLUGINS]: () => ({ languages: false, seo: false, seoEdit: false }) })
    await c.probe()
    expect(renderToStaticMarkup(<PageToolsMenuRow controller={c} close={() => undefined} />)).toBe('')
  })

  it('shows tabs only when there is more than one and lists languages', async () => {
    const both = make({ [PLUGINS]: () => ({ languages: true, seo: true, seoEdit: true }), [LANGS]: () => languages(), [SEO_GET]: () => meta() })
    await both.c.probe()
    both.c.open()
    await vi.waitFor(() => expect(both.c.getState().languages.load).toBe('ready'))
    const html = renderToStaticMarkup(<PageToolsDialog controller={both.c} />)
    expect(html).toContain('role="tablist"')
    expect(html).toContain('Add translation')
    expect(html).toContain('Deutsch')
    expect(html).toContain('href="/wp-admin/post?post=50&amp;action=web-builder"')

    const one = make({ [PLUGINS]: () => ({ languages: true, seo: false, seoEdit: false }), [LANGS]: () => languages({ canTranslate: false }) })
    await one.c.probe()
    one.c.open()
    await vi.waitFor(() => expect(one.c.getState().languages.load).toBe('ready'))
    const single = renderToStaticMarkup(<PageToolsDialog controller={one.c} />)
    expect(single).not.toContain('role="tablist"')
    expect(single).not.toContain('Add translation')
  })

  it('lists a translation the user cannot edit without a title, status or link', async () => {
    const m = make({
      [PLUGINS]: () => ({ languages: true, seo: false, seoEdit: false }),
      [LANGS]: () => languages({ translations: [{ slug: 'de', name: 'Deutsch', flag: 'DE', postId: 50, title: '', status: '', editUrl: '', readOnly: true }], missing: [] }),
    })
    await m.c.probe()
    m.c.open()
    await vi.waitFor(() => expect(m.c.getState().languages.load).toBe('ready'))
    const html = renderToStaticMarkup(<PageToolsDialog controller={m.c} />)
    expect(html).toContain('Deutsch')
    expect(html).toContain('You cannot edit this translation.')
    expect(html).not.toContain('>Open<')
  })

  it('draws the SEO form with its score', async () => {
    const m = make({ [PLUGINS]: () => FULL_SEO, [SEO_GET]: () => meta() })
    await m.c.probe()
    m.c.open()
    await vi.waitFor(() => expect(m.c.getState().seo.load).toBe('ready'))
    const html = renderToStaticMarkup(<PageToolsDialog controller={m.c} />)
    expect(html).toContain('73 / 100')
    expect(html).toContain('Focus keyword')
    expect(html).toContain('Meta description')
    expect(html).toContain('Social image')
  })
})
