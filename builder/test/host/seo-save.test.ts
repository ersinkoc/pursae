import { expect, it } from 'vitest'
import { PageToolsController, type SeoDraft, type SeoMeta } from '../../host/page-tools'
import type { RestClient } from '../../host/rest'

it.each([{ title: 'latest' }, { description: '' }, { noindex: true }] as Partial<SeoDraft>[])('preserves SEO edits made during save: %o', async (patch) => {
  let release!: (value: SeoMeta) => void
  const gate = new Promise<SeoMeta>((resolve) => { release = resolve })
  const initial = { id: 42, fields: { title: 'initial', description: 'initial description' } }
  const rest = { request: async (method: string, route: string) => ({ data: route.endsWith('/plugins') ? { seo: true, seoEdit: true, seoAdvanced: true } : method === 'GET' ? initial : await gate }) } as unknown as RestClient
  const c = new PageToolsController({ rest, postId: 42 })
  await c.probe()
  await c.loadSeo()
  c.editSeo({ title: 'sent' })
  const saving = c.saveSeo()
  c.editSeo(patch)
  release({ id: 42, fields: { title: 'sent', description: 'normalized description' } })
  await saving
  expect(c.getState().seo.draft).toMatchObject(patch)
  expect(c.getState().seo.data?.fields.title).toBe('sent')
  expect(c.seoDirty).toBe(true)
  if (!('description' in patch)) expect(c.getState().seo.draft?.description).toBe('normalized description')
})
