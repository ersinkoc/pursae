import { expect, it } from 'vitest'
import { SiteSettingsController, type SiteSettings } from '../../host/site-settings'
import type { RestClient } from '../../host/rest'
const data = (blogname: string): SiteSettings => ({ blogname, blogdescription: '', site_icon: null, custom_logo: null, supports_logo: true, show_on_front: 'posts', page_on_front: 0, page_for_posts: 0, custom_css: '', pages: [], can_upload: true })

it.each(['old-success', 'old-error', 'new-error'])('only current settings load publishes: %s', async (mode) => {
  const deferred = () => {
    let resolve!: (data: SiteSettings) => void
    let reject!: (error: Error) => void
    const promise = new Promise<SiteSettings>((ok, fail) => { resolve = ok; reject = fail })
    return { promise, resolve, reject }
  }
  const old = deferred(), newer = deferred()
  let calls = 0
  const c = new SiteSettingsController({ rest: { request: async () => ({ data: await (++calls === 1 ? old.promise : newer.promise) }) } as unknown as RestClient })
  const a = c.load(), b = c.load()
  if (mode === 'new-error') newer.reject(new Error('new request failed'))
  else newer.resolve(data('new'))
  await b
  const current = c.getState()
  if (mode === 'old-error') old.reject(new Error('old request failed'))
  else old.resolve(data('old'))
  await a
  expect(c.getState()).toBe(current)
  expect(c.getState().loading).toBe(false)
  if (mode === 'new-error') expect(c.getState().draft).toBeNull()
  else expect(c.getState().draft?.blogname).toBe('new')
})
