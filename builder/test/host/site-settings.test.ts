import { describe, expect, it, vi } from 'vitest'
import { RestError, type RestClient } from '../../host/rest'
import { SiteSettingsController, diffDraft, type SiteDraft, type SiteSettings } from '../../host/site-settings'

const base = (over: Partial<SiteSettings> = {}): SiteSettings => ({
  blogname: 'Test',
  blogdescription: 'Just another site',
  site_icon: null,
  custom_logo: null,
  supports_logo: true,
  show_on_front: 'posts',
  page_on_front: 0,
  page_for_posts: 0,
  custom_css: '',
  pages: [{ id: 7, title: 'Start' }],
  can_upload: true,
  ...over,
})

/** The editable part of settings, as the dialog's draft holds it. */
const draft = (s: SiteSettings, over: Partial<SiteDraft> = {}): SiteDraft => ({
  blogname: s.blogname,
  blogdescription: s.blogdescription,
  site_icon: s.site_icon,
  custom_logo: s.custom_logo,
  show_on_front: s.show_on_front,
  page_on_front: s.page_on_front,
  page_for_posts: s.page_for_posts,
  custom_css: s.custom_css,
  tokens: { colors: { ...(s.tokens?.colors ?? {}) }, fonts: { ...(s.tokens?.fonts ?? {}) } },
  ...over,
})

function fakeRest(handler: (method: string, route: string, body: unknown) => unknown | Promise<unknown>) {
  const request = vi.fn(async (method: string, route: string, opts?: { json?: unknown }) => ({ data: await handler(method, route, opts?.json), headers: new Headers(), status: 200 }))
  return { request } as unknown as RestClient & { request: typeof request }
}

describe('diffDraft', () => {
  it('sends only what changed, images by id', () => {
    const data = base({ site_icon: { id: 5, url: 'a' } })
    expect(diffDraft(data, draft(data, { site_icon: { id: 9, url: 'b' }, blogname: 'New' }))).toEqual({ site_icon: 9, blogname: 'New' })
  })
  it('removing an image sends 0', () => {
    const data = base({ custom_logo: { id: 3, url: 'x' } })
    expect(diffDraft(data, draft(data, { custom_logo: null }))).toEqual({ custom_logo: 0 })
  })
  it('is empty without edits', () => {
    expect(diffDraft(base(), draft(base()))).toEqual({})
  })
})

describe('SiteSettingsController', () => {
  it('loads on open and saves the difference', async () => {
    let current = base()
    const rest = fakeRest((m, _r, body) => {
      if (m === 'PUT') current = { ...current, ...(body as object) } as SiteSettings
      return current
    })
    const c = new SiteSettingsController({ rest })
    c.open()
    await vi.waitFor(() => expect(c.getState().data).not.toBeNull())
    expect(c.dirty).toBe(false)
    c.edit({ blogname: 'Pursae', show_on_front: 'page', page_on_front: 7 })
    expect(c.dirty).toBe(true)
    await c.save()
    expect(rest.request).toHaveBeenLastCalledWith('PUT', 'web-builder/v2/site-settings', { json: { blogname: 'Pursae', show_on_front: 'page', page_on_front: 7 } })
    expect(c.getState().saved).toBe(true)
    expect(c.dirty).toBe(false)
  })

  it('shows the server error and keeps the draft', async () => {
    const rest = fakeRest((m) => {
      if (m === 'PUT') throw new RestError(400, 'rest_invalid_param', 'Homepage and posts page must be different.')
      return base()
    })
    const c = new SiteSettingsController({ rest })
    c.open()
    await vi.waitFor(() => expect(c.getState().data).not.toBeNull())
    c.edit({ page_on_front: 7 })
    await c.save()
    expect(c.getState().error).toBe('Homepage and posts page must be different.')
    expect(c.getState().draft?.page_on_front).toBe(7)
    expect(c.getState().saved).toBe(false)
  })

  it('picks an image from the library', async () => {
    const c = new SiteSettingsController({ rest: fakeRest(() => base()), pickImage: async () => ({ id: 11, url: 'u' }) })
    c.open()
    await vi.waitFor(() => expect(c.getState().data).not.toBeNull())
    await c.pick('site_icon')
    expect(c.getState().draft?.site_icon).toEqual({ id: 11, url: 'u' })
  })

  it('does not close while saving', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => (release = r))
    const rest = fakeRest(async (m) => {
      if (m === 'PUT') await gate
      return base()
    })
    const c = new SiteSettingsController({ rest })
    c.open()
    await vi.waitFor(() => expect(c.getState().data).not.toBeNull())
    c.edit({ blogname: 'x' })
    const p = c.save()
    c.close()
    expect(c.getState().open).toBe(true)
    release()
    await p
    c.close()
    expect(c.getState().open).toBe(false)
  })
})

it('preserves edits typed while a save is in flight', async () => {
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  let current = base()
  const rest = fakeRest(async (method, _route, body) => {
    if (method === 'PUT') {
      await gate
      current = { ...current, ...(body as object), blogdescription: 'Normalized tagline' }
    }
    return current
  })
  const c = new SiteSettingsController({ rest })
  await c.load()
  c.edit({ blogname: 'Sent title' })
  const saving = c.save()
  c.edit({ blogname: 'Newer title', custom_css: 'p { color: red; }' })
  release()
  await saving
  expect(c.getState().data?.blogname).toBe('Sent title')
  expect(c.getState().draft?.blogname).toBe('Newer title')
  expect(c.getState().draft?.custom_css).toBe('p { color: red; }')
  expect(c.getState().draft?.blogdescription).toBe('Normalized tagline')
  expect(c.dirty).toBe(true)
  await c.save()
  expect(c.getState().data?.blogname).toBe('Newer title')
  expect(c.dirty).toBe(false)
})
