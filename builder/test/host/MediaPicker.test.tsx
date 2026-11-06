import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { MediaPicker } from '../../host/MediaPicker'
import { thumbUrl, toAsset } from '../../host/cms-host'
import { createRest } from '../../host/rest'
import { ROOT, mockFetch } from './helpers'

const rest = createRest({ root: ROOT, nonce: 'n', nonceUrl: '', fetch: mockFetch({}).fetch })

describe('MediaPicker', () => {
  it('renders an accessible dialog with search and upload', () => {
    const html = renderToStaticMarkup(<MediaPicker rest={rest} accept="image/png,image/jpeg" upload={async () => ({ url: 'u' })} onClose={() => {}} />)
    expect(html).toContain('role="dialog"')
    expect(html).toContain('aria-modal="true"')
    expect(html).toContain('Media Library')
    expect(html).toContain('type="search"')
    expect(html).toContain('accept="image/png,image/jpeg"')
    expect(html).toContain('Use image')
  })

  it('hides upload without upload rights', () => {
    const html = renderToStaticMarkup(<MediaPicker rest={rest} accept="image/*" upload={null} onClose={() => {}} />)
    expect(html).not.toContain('type="file"')
  })

  it('maps media items to assets and thumbnails', () => {
    const m = { id: 3, source_url: 'https://x/full.jpg', alt_text: 'A cat', mime_type: 'image/jpeg', media_details: { width: 800, height: 600, sizes: { medium: { source_url: 'https://x/m.jpg', width: 300, height: 225 } } } }
    expect(toAsset(m)).toEqual({ url: 'https://x/full.jpg', id: 3, alt: 'A cat', width: 800, height: 600 })
    expect(thumbUrl(m)).toBe('https://x/m.jpg')
    expect(thumbUrl({ ...m, media_details: {} })).toBe('https://x/full.jpg')
  })
})
