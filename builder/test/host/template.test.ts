/**
 * Template mode of the CMS host: DynContext provider (host/dynamic.ts), TemplateController
 * (host/template.ts: conditions, conflicts, "Preview as", conditions-first publish), the preview
 * model (host/preview.ts) and the wiring into BuilderHost (cms-host.ts, publish.ts).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createCmsHost } from '../../host/cms-host'
import { DynStore, emptyDynamic, isDynContext } from '../../host/dynamic'
import { kindIdSource, parsePreview, previewKinds, previewObject } from '../../host/preview'
import { createRest } from '../../host/rest'
import { TemplateController, readTemplate, typeLabel } from '../../host/template'
import type { LiveRenderer } from '../../host/publish'
import { PAGE, ROOT, makeBoot, makeDoc, mockFetch, restErr } from './helpers'
import { SITE, makeDyn, makeTemplate, makeTemplateBoot } from './template-fixtures'

const COND = 'web-builder/v2/templates/42/conditions'
const CONFLICTS = 'web-builder/v2/templates/42/conflicts'
const PREVIEW = 'web-builder/v2/templates/42/preview'
const PCTX = 'web-builder/v2/preview-context'

const rest = (fetch: ReturnType<typeof mockFetch>['fetch']) => createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch })

const renderer: LiveRenderer = {
  renderStaticAsync: async () => ({ html: '<div class="wb-page" data-wb-root></div>', css: '', classes: [], fonts: [], features: [], renderer: 1 }),
}

describe('DynStore', () => {
  it('starts from the boot context and keeps the same object until it changes', () => {
    const boot = makeTemplateBoot()
    const s = new DynStore(rest(mockFetch({}).fetch), boot)
    expect(s.fromBoot).toBe(true)
    expect(s.get()).toBe(boot.dynamic)
    expect(s.get()).toBe(s.get())
  })

  it('set() notifies subscribers and ignores malformed data', () => {
    const s = new DynStore(rest(mockFetch({}).fetch), makeTemplateBoot())
    const seen: string[] = []
    const off = s.subscribe((c) => seen.push(c.object))
    expect(s.set({ nope: 1 })).toBe(false)
    expect(s.set(makeDyn('post:9'))).toBe(true)
    off()
    s.set(makeDyn('home'))
    expect(seen).toEqual(['post:9'])
    expect(s.get().object).toBe('home')
  })

  it('load() fetches preview-context for the object', async () => {
    const m = mockFetch({ [`GET ${PCTX}`]: (c) => ({ json: makeDyn(c.query.get('object')!) }) })
    const s = new DynStore(rest(m.fetch), makeTemplateBoot())
    const ctx = await s.load('term:category:4')
    expect(ctx.object).toBe('term:category:4')
    expect(s.get().object).toBe('term:category:4')
    expect(m.calls[0].headers['x-wb-api']).toBe('2')
  })

  it('a load answered after a newer set() does not overwrite it', async () => {
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    const m = mockFetch({ [`GET ${PCTX}`]: async () => (await gate, { json: makeDyn('home') }) })
    const s = new DynStore(rest(m.fetch), makeTemplateBoot())
    const p = s.load('home')
    s.set(makeDyn('post:7'))
    release()
    await p
    expect(s.get().object).toBe('post:7')
  })

  it('without a usable boot context it starts empty, from the site identity (never sample content)', () => {
    const boot = makeTemplateBoot({}, {}, { dynamic: undefined as never })
    const s = new DynStore(rest(mockFetch({}).fetch), boot)
    expect(s.fromBoot).toBe(false)
    expect(isDynContext(s.get())).toBe(true)
    expect(s.get().post).toBeNull()
    expect(s.get().posts).toEqual([])
    expect(s.get().site.title).toBe('Example')
    expect(s.get().locations).toHaveLength(2)
    expect(emptyDynamic({ site: undefined as never, links: boot.links }).site.home).toBe('https://example.test')
  })
})

describe('preview model', () => {
  it('offers the kinds that fit the template type', () => {
    expect(previewKinds('single-post', SITE)).toEqual(['latest', 'post'])
    expect(previewKinds('single-page', SITE)).toEqual(['latest', 'front', 'page'])
    expect(previewKinds('single', SITE)).toEqual(['latest', 'entry'])
    expect(previewKinds('archive', SITE)).toEqual(['home', 'category', 'post_tag', 'author', 'type', 'search'])
    expect(previewKinds('search-results', SITE)).toEqual(['search'])
    expect(previewKinds('error-404', SITE)).toEqual(['404'])
    expect(previewKinds('header', SITE)).toContain('post')
    // No custom type, no post type archive: those kinds go.
    const plain = { postTypes: SITE.postTypes.filter((p) => p.slug !== 'book').map((p) => ({ ...p, archive: '' })) }
    expect(previewKinds('header', plain)).not.toContain('entry')
    expect(previewKinds('archive', plain)).not.toContain('type')
  })

  it('offers store previews for the Pursae Commerce types', () => {
    expect(previewKinds('single-product', SITE)).toEqual(['latest', 'entry'])
    expect(previewKinds('product-archive', SITE)).toEqual(['type', 'product_cat', 'product_tag'])
    const kinds = previewKinds('product-archive', SITE)
    expect(parsePreview('term:product_cat:7', kinds)).toEqual({ kind: 'product_cat', id: '7', text: '' })
    expect(previewObject({ kind: 'product_tag', id: '9', text: '' })).toBe('term:product_tag:9')
    expect(previewObject({ kind: 'product_cat', id: '0', text: '' })).toBeNull()
  })

  it('object <-> choice', () => {
    const kinds = previewKinds('archive', SITE)
    expect(parsePreview('term:category:4', kinds)).toEqual({ kind: 'category', id: '4', text: '' })
    expect(parsePreview('search:hello world', previewKinds('search-results', SITE))).toEqual({ kind: 'search', id: '', text: 'hello world' })
    expect(parsePreview('type:book', kinds)).toEqual({ kind: 'type', id: '', text: 'book' })
    expect(parsePreview('post:5', previewKinds('single-page', SITE))).toEqual({ kind: 'page', id: '5', text: '' })
    expect(parsePreview('post:5', previewKinds('single', SITE))).toEqual({ kind: 'entry', id: '5', text: '' })
    // An object the type does not offer falls back to its first kind.
    expect(parsePreview('404', kinds)).toEqual({ kind: 'home', id: '', text: '' })

    expect(previewObject({ kind: 'page', id: '5', text: '' })).toBe('post:5')
    expect(previewObject({ kind: 'post_tag', id: '8', text: '' })).toBe('term:post_tag:8')
    expect(previewObject({ kind: 'author', id: '2', text: '' })).toBe('author:2')
    expect(previewObject({ kind: 'search', id: '', text: '  shoes ' })).toBe('search:shoes')
    expect(previewObject({ kind: 'search', id: '', text: ' ' })).toBeNull()
    expect(previewObject({ kind: 'search', id: '', text: 'x'.repeat(101) })).toBeNull()
    expect(previewObject({ kind: 'post', id: '', text: '' })).toBeNull()
    expect(previewObject({ kind: 'type', id: '', text: 'Bad Type' })).toBeNull()
    expect(previewObject({ kind: 'latest', id: '', text: '' })).toBe('latest')
    expect(kindIdSource('entry')).toBeNull()
    expect(kindIdSource('home')).toBeUndefined()
  })
})

describe('TemplateController', () => {
  const make = (routes: Parameters<typeof mockFetch>[0], tpl = {}, doc = {}) => {
    const m = mockFetch({ [`GET ${CONFLICTS}`]: { json: [] }, ...routes })
    const boot = makeTemplateBoot(tpl, doc)
    const r = rest(m.fetch)
    const dyn = new DynStore(r, boot)
    return { m, dyn, c: new TemplateController(r, dyn, boot, readTemplate(boot)!) }
  }

  it('readTemplate accepts known types only', () => {
    expect(readTemplate({ template: null })).toBeNull()
    expect(readTemplate({ template: { ...makeTemplate(), type: 'sidebar' as never } })).toBeNull()
    expect(readTemplate({ template: makeTemplate() })?.type).toBe('single-post')
    expect(typeLabel('error-404')).toBe('404 Page')
  })

  it('saves conditions with PUT and takes the normalized answer, then loads conflicts', async () => {
    const conflict = { id: 9, title: 'Other', type: 'single-post', conditions: ['include/singular/post'], priority: 2, editUrl: 'https://example.test/e/9' }
    const { m, c } = make({
      [`PUT ${COND}`]: { json: { conditions: ['include/singular/post', 'exclude/singular/post/5'], priority: 3 } },
      [`GET ${CONFLICTS}`]: { json: [conflict] },
    })
    const r = await c.saveConditions([' include/singular/post', 'exclude/singular/post/5'], 3)
    expect(r.ok).toBe(true)
    const put = m.find('PUT', COND)[0]
    expect(put.body).toEqual({ conditions: [' include/singular/post', 'exclude/singular/post/5'], priority: 3 })
    expect(put.headers['x-wb-api']).toBe('2')
    expect(c.getState().conditions).toEqual(['include/singular/post', 'exclude/singular/post/5'])
    expect(c.getState().priority).toBe(3)
    await vi.waitFor(() => expect(c.getState().conflicts).toEqual([conflict]))
  })

  it('maps 422 wb_bad_condition errors to rules and the priority', async () => {
    const { c } = make({
      [`PUT ${COND}`]: restErr(422, 'wb_bad_condition', {
        errors: [
          { path: '/conditions/1', code: 'bad_condition', message: 'Unknown scope' },
          { path: '/priority', code: 'bad_condition', message: 'Out of range' },
        ],
      }),
    })
    const r = await c.saveConditions(['include/general', 'include/x'], 500)
    expect(r.ok).toBe(false)
    expect(r.errors).toEqual({ 1: 'Unknown scope', priority: 'Out of range' })
    expect(c.getState().conditions).toEqual(['include/singular/post'])
  })

  it('other save failures give a message', async () => {
    const { c } = make({ [`PUT ${COND}`]: restErr(403, 'rest_forbidden', {}, 'Sorry, you are not allowed to do that.') })
    expect(await c.saveConditions([], 0)).toEqual({ ok: false, errors: {}, message: 'Sorry, you are not allowed to do that.' })
  })

  it('conflicts that cannot be loaded leave an empty list', async () => {
    const { c } = make({ [`GET ${CONFLICTS}`]: restErr(500, 'db_error') })
    expect(await c.loadConflicts()).toEqual([])
    expect(c.getState().conflicts).toEqual([])
  })

  it('setPreview sends PUT preview and swaps the DynContext and the preview label', async () => {
    const answer = { preview: { object: 'post:7', label: 'Post: Seven', url: 'https://example.test/seven/?wb_template_preview=42' }, dynamic: makeDyn('post:7', 'Seven') }
    const { m, c, dyn } = make({ [`PUT ${PREVIEW}`]: { json: answer } })
    const seen: string[] = []
    dyn.subscribe((x) => seen.push(x.post?.title ?? ''))
    c.openPreview()
    expect(c.getState().picker).toBe(true)
    await c.setPreview('post:7')
    expect(m.find('PUT', PREVIEW)[0].body).toEqual({ object: 'post:7' })
    expect(c.getState().preview).toEqual(answer.preview)
    expect(dyn.get().object).toBe('post:7')
    expect(seen).toEqual(['Seven'])
  })

  it('setPreview errors are readable and keep the old preview', async () => {
    const { c, dyn } = make({ [`PUT ${PREVIEW}`]: [restErr(404, 'wb_not_found'), restErr(422, 'wb_bad_preview'), { json: { preview: { object: 'x', label: '', url: '' }, dynamic: { bad: true } } }] })
    await expect(c.setPreview('post:99')).rejects.toThrow('Nothing was found to preview with this choice.')
    await expect(c.setPreview('post:x')).rejects.toThrow('This preview is not valid.')
    await expect(c.setPreview('home')).rejects.toThrow('The preview data could not be read.')
    expect(c.getState().preview.object).toBe('latest')
    expect(dyn.get().object).toBe('latest')
  })

  it('first publish opens the conditions dialog and publishes only when it is saved', async () => {
    const { c } = make({})
    expect(c.needsConditionsFirst()).toBe(true)
    let p = c.beforePublish()
    expect(c.getState().dialog).toMatchObject({ mode: 'publish' })
    c.closeConditions(false)
    expect(await p).toBe(false)
    expect(c.getState().dialog).toBeNull()

    p = c.beforePublish()
    c.closeConditions(true)
    expect(await p).toBe(true)
  })

  it('a published template with an include rule publishes directly; without one it asks again', async () => {
    const { c } = make({}, {}, { status: 'publish', rev: 4 })
    expect(c.getState().published).toBe(true)
    expect(await c.beforePublish()).toBe(true)
    expect(c.getState().dialog).toBeNull()

    const bare = make({}, { conditions: ['exclude/singular/post/5'] }, { status: 'publish', rev: 4 }).c
    const p = bare.beforePublish()
    expect(bare.getState().dialog).toMatchObject({ mode: 'publish' })
    bare.closeConditions(true)
    expect(await p).toBe(true)
  })

  it('conditions saved through the Conditions button make the first publish go straight on', async () => {
    const { m, c } = make({ [`PUT ${COND}`]: { json: { conditions: ['include/singular/post'], priority: 0 } } })
    expect(c.needsConditionsFirst()).toBe(true)
    const edit = c.openConditions('edit')
    expect((await c.saveConditions(['include/singular/post'], 0)).ok).toBe(true)
    c.closeConditions(true)
    expect(await edit).toBe(true)
    expect(c.getState().confirmed).toBe(true)
    expect(c.needsConditionsFirst()).toBe(false)
    expect(await c.beforePublish()).toBe(true)
    expect(c.getState().dialog).toBeNull()
    expect(m.find('PUT', COND)).toHaveLength(1)
  })

  it('an edit-mode save without an include rule still asks on publish', async () => {
    const { c } = make({ [`PUT ${COND}`]: { json: { conditions: ['exclude/singular/post/5'], priority: 0 } } })
    c.openConditions('edit')
    expect((await c.saveConditions(['exclude/singular/post/5'], 0)).ok).toBe(true)
    c.closeConditions(true)
    expect(c.needsConditionsFirst()).toBe(true)
    const p = c.beforePublish()
    expect(c.getState().dialog).toMatchObject({ mode: 'publish' })
    c.closeConditions(false)
    expect(await p).toBe(false)
  })

  it('opening the Conditions dialog without saving, or a failed save, does not count as confirmation', async () => {
    const { c } = make({ [`PUT ${COND}`]: restErr(403, 'rest_forbidden') })
    const edit = c.openConditions('edit')
    c.closeConditions(false)
    expect(await edit).toBe(false)
    expect(c.needsConditionsFirst()).toBe(true)
    c.openConditions('edit')
    expect((await c.saveConditions(['include/singular/post'], 0)).ok).toBe(false)
    c.closeConditions(false)
    expect(c.getState().confirmed).toBe(false)
    expect(c.needsConditionsFirst()).toBe(true)
  })

  it('a save from the publish dialog confirms that publish only, not a later first publish', async () => {
    const { c } = make({ [`PUT ${COND}`]: { json: { conditions: ['include/singular/post'], priority: 0 } } })
    const p = c.beforePublish()
    expect((await c.saveConditions(['include/singular/post'], 0)).ok).toBe(true)
    c.closeConditions(true)
    expect(await p).toBe(true)
    expect(c.getState().confirmed).toBe(false)
    // The publish itself failed (no markPublished): the next try asks again.
    expect(c.needsConditionsFirst()).toBe(true)
  })

  it('a disabled (draft) template that was published before only asks without an include rule', async () => {
    const { c } = make({}, {}, { status: 'draft', rev: 2 })
    // rev > 0 but not live: still a "first publish" in the sense of going live again.
    expect(c.needsConditionsFirst()).toBe(true)
    c.markPublished()
    expect(c.needsConditionsFirst()).toBe(false)
  })

  it('opening the dialog again cancels the pending one', async () => {
    const { c } = make({})
    const a = c.openConditions('publish')
    const b = c.openConditions('edit')
    expect(await a).toBe(false)
    expect(c.getState().dialog?.mode).toBe('edit')
    c.closeConditions(true)
    expect(await b).toBe(true)
  })
})

describe('cms-host in template mode', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  const prefs = { 'GET web-builder/v2/prefs': { json: {} }, 'GET web-builder/v2/library': { json: [] }, 'POST web-builder/v2/documents/42/lock': { json: { lock: null, expires: '' } } }

  it('exposes template, dynamic data and no themeParts for the template itself', () => {
    const boot = makeTemplateBoot()
    const host = createCmsHost(boot, { fetch: mockFetch({}).fetch, local: null })
    expect(host.template?.()).toEqual({ ...boot.template, live: false })
    expect(host.template?.()).toBe(host.template?.())
    expect(host.themeParts).toBe(false)
    expect(host.dynamic?.()).toBe(boot.dynamic)
    expect(host.templates).not.toBeNull()
    expect(typeof host.beforePublish).toBe('function')
    const seen: string[] = []
    const off = host.onDynamic!((c) => seen.push(c.object))
    host.dyn.set(makeDyn('home'))
    off()
    expect(seen).toEqual(['home'])
  })

  it('reports whether the template is live, so a stored but unpublished doc can be published', () => {
    // A duplicate or a disabled template has a doc (rev > 0) but is a draft: not live.
    const dup = createCmsHost(makeTemplateBoot({}, { status: 'draft', rev: 3, doc: makeDoc() }), { fetch: mockFetch({}).fetch, local: null })
    expect(dup.template?.()?.live).toBe(false)
    const live = createCmsHost(makeTemplateBoot({}, { status: 'publish', rev: 3, doc: makeDoc() }), { fetch: mockFetch({}).fetch, local: null })
    expect(live.template?.()?.live).toBe(true)
  })

  it('ordinary pages: no template, themeParts from the boot site, boot dynamic', () => {
    const boot = makeBoot({}, { site: SITE })
    const host = createCmsHost(boot, { fetch: mockFetch({}).fetch, local: null })
    expect(host.template?.()).toBeNull()
    expect(host.templates).toBeNull()
    expect(host.beforePublish).toBeUndefined()
    expect(host.themeParts).toBe(true)
    expect(host.dynamic?.()).toBe(boot.dynamic)
    expect(createCmsHost(makeBoot(), { fetch: mockFetch({}).fetch, local: null }).themeParts).toBe(false)
  })

  it('start() fetches preview-context when the boot config has no sample data', async () => {
    const m = mockFetch({ ...prefs, [`GET ${PCTX}`]: (c) => ({ json: makeDyn(c.query.get('object')!) }) })
    const host = createCmsHost(makeTemplateBoot({ preview: { object: 'post:5', label: '', url: '' } }, {}, { dynamic: null as never }), { fetch: m.fetch, local: null })
    await host.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(m.find('GET', PCTX)[0].query.get('object')).toBe('post:5')
    expect(host.dynamic?.().object).toBe('post:5')
    host.dispose()

    const page = mockFetch({ ...prefs, [`GET ${PCTX}`]: (c) => ({ json: makeDyn(c.query.get('object')!) }) })
    const h2 = createCmsHost(makeBoot({}, { dynamic: undefined as never }), { fetch: page.fetch, local: null })
    await h2.start()
    expect(page.find('GET', PCTX)[0].query.get('object')).toBe('post:42')
    h2.dispose()
  })

  it('publish: conditions first, then the template publish with a preview link', async () => {
    const m = mockFetch({
      ...prefs,
      [`GET ${CONFLICTS}`]: { json: [] },
      'POST web-builder/v2/documents/42/publish': { json: { rev: 1, status: 'publish', permalink: 'https://example.test/?p=42', revisionId: 1, modified: '', stripped: [] } },
    })
    const host = createCmsHost(makeTemplateBoot(), { fetch: m.fetch, local: null, loadRenderer: async () => renderer })
    const input = { doc: makeDoc('t'), page: PAGE }
    // What upstream Builder does: beforePublish, and publish only when it resolves true.
    const go = host.beforePublish!(input)
    expect(host.templates!.getState().dialog?.mode).toBe('publish')
    host.templates!.closeConditions(true)
    expect(await go).toBe(true)
    const out = await host.publish(input)
    expect(out).toMatchObject({ message: 'Template published', permalink: 'https://example.test/hello/?wb_template_preview=42' })
    const toast = host.ui.getState().notices.find((n) => n.tone === 'success')!
    expect(toast.actions[0]).toMatchObject({ label: 'Preview on the site', href: 'https://example.test/hello/?wb_template_preview=42' })
    expect(m.find('POST', 'web-builder/v2/documents/42/publish')[0].body).toMatchObject({ status: 'publish' })
    // Published now: the next publish does not ask again.
    expect(await host.beforePublish!(input)).toBe(true)
    host.dispose()
  })
})
