/**
 * Template mode UI of the CMS host: TemplateBar, ConditionsDialog, PreviewPicker, the id search
 * helpers in rest.ts and the host string catalog (host/i18n.ts, host/i18n-tr.ts).
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { createCmsHost } from '../../host/cms-host'
import { ConditionsDialog } from '../../host/ConditionsDialog'
import { IdPicker } from '../../host/IdPicker'
import { PreviewPicker } from '../../host/PreviewPicker'
import { TemplateBar, TemplateDialogs, conditionsSummary, installTemplateUi } from '../../host/TemplateBar'
import { createRest, itemLabel, itemTitle, searchItems } from '../../host/rest'
import tr from '../../host/i18n-tr'
import { ROOT, makeBoot, mockFetch } from './helpers'
import { SITE, makeTemplateBoot } from './template-fixtures'

const here = path.dirname(fileURLToPath(import.meta.url))
const HOST = path.join(here, '..', '..', 'host')

const rest = (routes: Parameters<typeof mockFetch>[0] = {}) => {
  const m = mockFetch(routes)
  return { m, rest: createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: m.fetch }) }
}
const noSave = async () => ({ ok: true, errors: {}, message: '' })

afterEach(() => {
  vi.resetModules()
})

describe('id search (core wp/v2 collections)', () => {
  it('searches with ?search= and maps titles and names', async () => {
    const { m, rest: r } = rest({
      'GET wp/v2/pages': { json: [{ id: 3, title: { rendered: 'About &amp; <em>us</em>' } }, { id: 0, title: { rendered: 'bad' } }, { id: 4, title: { rendered: '' } }] },
      'GET wp/v2/users': { json: [{ id: 2, name: 'Ada &#8220;A&#8221;' }] },
    })
    expect(await searchItems(r, 'pages', ' abo ')).toEqual([
      { id: 3, label: 'About & us' },
      { id: 4, label: '#4' },
    ])
    const call = m.find('GET', 'wp/v2/pages')[0]
    expect(call.query.get('search')).toBe('abo')
    expect(call.query.get('per_page')).toBe('10')
    expect(call.query.get('_fields')).toBe('id,title')
    // Core routes do not get the builder's API header.
    expect(call.headers['x-wb-api']).toBeUndefined()
    expect(await searchItems(r, 'users', 'ada')).toEqual([{ id: 2, label: 'Ada “A”' }])
    expect(m.find('GET', 'wp/v2/users')[0].query.get('_fields')).toBe('id,name')
  })

  it('reads one title by id, "" on failure', async () => {
    const { rest: r } = rest({ 'GET wp/v2/categories/4': { json: { id: 4, name: 'News' } } })
    expect(await itemTitle(r, 'categories', 4)).toBe('News')
    expect(await itemTitle(r, 'categories', 5)).toBe('')
    expect(itemLabel(null)).toBe('')
    expect(itemLabel({ title: 'Plain &lt;b&gt;' })).toBe('Plain <b>')
  })
})

describe('TemplateBar', () => {
  it('shows the type badge, the conditions summary and the preview label', () => {
    const host = createCmsHost(makeTemplateBoot({ conditions: ['include/singular/post', 'exclude/singular/post/5'] }), { fetch: mockFetch({}).fetch, local: null })
    const html = renderToStaticMarkup(<TemplateBar host={host} controller={host.templates!} />)
    expect(html).toContain('data-wb-template-type="single-post"')
    expect(html).toContain('Single Post')
    expect(html).toContain('aria-label="Display Conditions"')
    expect(html).toContain('Posts +1')
    expect(html).toContain('Post: Hello world')
  })

  it('summarizes rules', () => {
    const host = { boot: makeTemplateBoot() }
    expect(conditionsSummary(host, [])).toBe('Nowhere')
    expect(conditionsSummary(host, ['exclude/general'])).toBe('Nowhere')
    expect(conditionsSummary(host, ['include/general'])).toBe('Entire Site')
  })

  it('installTemplateUi sets topBarExtra in template mode only', () => {
    const host = createCmsHost(makeTemplateBoot(), { fetch: mockFetch({}).fetch, local: null })
    expect(installTemplateUi(host)).toBe(true)
    expect(renderToStaticMarkup(<>{host.topBarExtra!()}</>)).toContain('Display Conditions')
    const page = createCmsHost(makeBoot(), { fetch: mockFetch({}).fetch, local: null })
    expect(installTemplateUi(page)).toBe(false)
    expect(page.topBarExtra).toBeUndefined()
  })

  it('TemplateDialogs draws the dialogs the controller opened', () => {
    const host = createCmsHost(makeTemplateBoot(), { fetch: mockFetch({ 'GET web-builder/v2/templates/42/conflicts': { json: [] } }).fetch, local: null })
    const c = host.templates!
    expect(renderToStaticMarkup(<TemplateDialogs host={host} controller={c} />)).toBe('')
    void c.openConditions('publish')
    let html = renderToStaticMarkup(<TemplateDialogs host={host} controller={c} />)
    expect(html).toContain('id="wb-cond-title"')
    expect(html).toContain('Save and publish')
    expect(html).toContain('“Blog post”')
    c.closeConditions(false)
    c.openPreview()
    html = renderToStaticMarkup(<TemplateDialogs host={host} controller={c} />)
    expect(html).toContain('id="wb-preview-title"')
  })
})

describe('ConditionsDialog', () => {
  const { rest: r } = rest()

  it('draws one row per rule with the cascade selected', () => {
    const html = renderToStaticMarkup(
      <ConditionsDialog rest={r} site={SITE} type="single-post" mode="edit" conditions={['include/singular/post/in/category/4', 'exclude/archive/author']} priority={5} conflicts={null} save={noSave} onClose={() => {}} />,
    )
    expect(html).toContain('role="dialog"')
    expect(html).toContain('aria-modal="true"')
    expect((html.match(/<li class="rounded-xl/g) ?? []).length).toBe(2)
    expect(html).toMatch(/<option value="include" selected="">Include<\/option>/)
    expect(html).toMatch(/<option value="in\/category" selected="">In Categories<\/option>/)
    expect(html).toMatch(/<option value="exclude" selected="">Exclude<\/option>/)
    expect(html).toMatch(/<option value="author" selected="">Author Archives<\/option>/)
    expect(html).toContain('role="combobox"')
    expect(html).toContain('value="#4"')
    expect(html).toContain('value="5"')
    expect(html).toContain('Save and close')
    expect(html).not.toContain('Without an Include rule')
  })

  it('warns about no include rule and lists conflicts', () => {
    const conflicts = [{ id: 9, title: 'Other post', type: 'single-post' as const, conditions: ['include/singular/post'], priority: 2, editUrl: 'https://example.test/e/9' }]
    const html = renderToStaticMarkup(<ConditionsDialog rest={r} site={SITE} type="single-post" mode="publish" conditions={['exclude/singular/post/5']} priority={0} conflicts={conflicts} save={noSave} onClose={() => {}} />)
    expect(html).toContain('Without an Include rule the template is not shown anywhere.')
    expect(html).toContain('Other published templates can show on the same views:')
    expect(html).toContain('href="https://example.test/e/9"')
    expect(html).toContain('Priority 2 · Posts')
    expect(html).toContain('Save and publish')
  })

  it('an empty list says the template shows nowhere', () => {
    const html = renderToStaticMarkup(<ConditionsDialog rest={r} site={SITE} type="header" mode="edit" conditions={[]} priority={0} conflicts={[]} save={noSave} onClose={() => {}} />)
    expect(html).toContain('No conditions yet')
  })

  it('custom types without a search route get a number field', () => {
    const html = renderToStaticMarkup(<IdPicker rest={r} collection={null} id="12" name="Item" onChange={() => {}} />)
    expect(html).toContain('inputMode="numeric"')
    expect(html).toContain('value="12"')
  })
})

describe('PreviewPicker', () => {
  const { rest: r } = rest()

  it('offers the kinds of the template type and preselects the stored object', () => {
    const html = renderToStaticMarkup(
      <PreviewPicker rest={r} site={SITE} type="archive" preview={{ object: 'term:category:4', label: 'Category: News', url: 'https://example.test/category/news/?wb_template_preview=42' }} apply={async () => {}} onClose={() => {}} />,
    )
    expect(html).toContain('Preview as')
    expect(html).toMatch(/<option value="category" selected="">A category<\/option>/)
    expect(html).toContain('<option value="search">Search results</option>')
    expect(html).not.toContain('value="404"')
    expect(html).toContain('Now: Category: News')
    expect(html).toContain('href="https://example.test/category/news/?wb_template_preview=42"')
    expect(html).toContain('role="combobox"')
  })

  it('a search preview shows the query field', () => {
    const html = renderToStaticMarkup(<PreviewPicker rest={r} site={SITE} type="search-results" preview={{ object: 'search:a', label: 'Search: a', url: '' }} apply={async () => {}} onClose={() => {}} />)
    expect(html).toMatch(/<input type="search" maxLength="100"[^>]*value="a"/)
    expect(html).not.toContain('Open on the site')
  })
})

describe('host strings', () => {
  const sources = fs.readdirSync(HOST).filter((f) => /\.(ts|tsx)$/.test(f) && f !== 'i18n-tr.ts')
  const used = new Map<string, string>()
  for (const f of sources) {
    const text = fs.readFileSync(path.join(HOST, f), 'utf8')
    for (const m of text.matchAll(/\bt\(\s*'((?:[^'\\]|\\.)*)'/g)) used.set(m[1].replace(/\\'/g, "'"), f)
  }
  const ph = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(',')

  it('every literal t() string of builder/host has a Turkish entry with the same placeholders', () => {
    expect(used.size).toBeGreaterThan(50)
    const missing = [...used.keys()].filter((k) => !(k in tr))
    expect(missing).toEqual([])
    const bad = Object.entries(tr).filter(([en, x]) => ph(en) !== ph(x))
    expect(bad).toEqual([])
  })

  it('has no stale entries', () => {
    expect(Object.keys(tr).filter((k) => !used.has(k))).toEqual([])
  })

  it('follows the editor language', async () => {
    vi.resetModules()
    const i18n = await import('@/builder/i18n')
    const { t } = await import('../../host/i18n')
    i18n.setLocale('tr_TR')
    expect(t('Display Conditions')).toBe(tr['Display Conditions'])
    expect(t('Priority {n}', { n: 3 })).toBe('Öncelik 3') // i18n-ignore
    expect(t('Not in the catalog')).toBe('Not in the catalog')
    i18n.setLocale('en_US')
    expect(t('Priority {n}', { n: 3 })).toBe('Priority 3')
  })

  it('never names the upstream product', () => {
    for (const f of fs.readdirSync(HOST).filter((x) => /\.(ts|tsx)$/.test(x))) {
      expect(fs.readFileSync(path.join(HOST, f), 'utf8')).not.toMatch(/elementor/i)
    }
  })
})
