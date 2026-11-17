/**
 * The element palette (plan Faz 4.1, D5): new elements as plain semantic HTML with a local-style
 * preset, so they look reasonable inside any design without its classes. Every element of an
 * item's markup carries a placeholder data-wbc id (p1, p2, …): inserting it gives them fresh ids
 * (ops.insert) and the preset's local styles follow them (copies.from → copies.to).
 *
 * Pure data and functions, no DOM.
 */
import type { LocalStyles } from './local'

export type PaletteId =
  | 'section'
  | 'flex'
  | 'grid'
  | 'div'
  | 'heading'
  | 'text'
  | 'button'
  | 'link'
  | 'image'
  | 'icon'
  | 'video'
  | 'divider'
  | 'list'
  | 'input'
  | 'textarea'
  | 'select'
  | 'checkbox'
  | 'submit'
  | 'html'

export interface PaletteItem {
  id: PaletteId
  group: 'layout' | 'basic' | 'media' | 'form'
  /** English label (the UI translates it). */
  label: string
  html: string
  local: LocalStyles
}

/** A neutral image placeholder (an inline SVG: no request, any design). */
export const PLACEHOLDER_IMAGE =
  'data:image/svg+xml;charset=utf-8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" width="800" height="500" viewBox="0 0 800 500"><rect width="800" height="500" fill="#e2e8f0"/><path d="M300 330l80-100 60 70 40-45 80 75z" fill="#94a3b8"/><circle cx="330" cy="200" r="28" fill="#94a3b8"/></svg>',
  )

/** The default icon: a star, drawn with the text color. */
export const DEFAULT_ICON = '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z"/></svg>'

const one = (p: Record<string, string>, hover?: Record<string, string>): LocalStyles => ({
  p1: { v: [{ bp: '', st: '', p }, ...(hover ? [{ bp: '' as const, st: 'hover' as const, p: hover }] : [])] },
})

const FIELD = { 'box-sizing': 'border-box', width: '100%', 'padding-top': '10px', 'padding-bottom': '10px', 'padding-left': '12px', 'padding-right': '12px', 'border-top-width': '1px', 'border-right-width': '1px', 'border-bottom-width': '1px', 'border-left-width': '1px', 'border-top-style': 'solid', 'border-right-style': 'solid', 'border-bottom-style': 'solid', 'border-left-style': 'solid', 'border-top-color': '#cbd5e1', 'border-right-color': '#cbd5e1', 'border-bottom-color': '#cbd5e1', 'border-left-color': '#cbd5e1', 'border-top-left-radius': '8px', 'border-top-right-radius': '8px', 'border-bottom-right-radius': '8px', 'border-bottom-left-radius': '8px', 'font-size': '1rem', 'background-color': '#fff', color: 'inherit' }

export const PALETTE: PaletteItem[] = [
  { id: 'section', group: 'layout', label: 'Section', html: '<section data-wbc="p1"></section>', local: one({ 'padding-top': '64px', 'padding-bottom': '64px', 'padding-left': '24px', 'padding-right': '24px' }) },
  { id: 'flex', group: 'layout', label: 'Flexbox', html: '<div data-wbc="p1"></div>', local: one({ display: 'flex', 'flex-wrap': 'wrap', gap: '16px', 'align-items': 'center' }) },
  { id: 'grid', group: 'layout', label: 'Grid', html: '<div data-wbc="p1"></div>', local: one({ display: 'grid', 'grid-template-columns': 'repeat(2, minmax(0, 1fr))', gap: '16px' }) },
  { id: 'div', group: 'layout', label: 'Div', html: '<div data-wbc="p1"></div>', local: {} },
  { id: 'heading', group: 'basic', label: 'Heading', html: '<h2 data-wbc="p1">Add your heading</h2>', local: one({ 'font-size': '2rem', 'font-weight': '700', 'line-height': '1.2', 'margin-top': '0', 'margin-bottom': '0.5em' }) },
  { id: 'text', group: 'basic', label: 'Text', html: '<p data-wbc="p1">Add your text here. Double-click to edit it.</p>', local: one({ 'font-size': '1rem', 'line-height': '1.6', 'margin-top': '0', 'margin-bottom': '1em' }) },
  {
    id: 'button',
    group: 'basic',
    label: 'Button',
    html: '<a data-wbc="p1" href="#">Click here</a>',
    local: one(
      { display: 'inline-block', 'padding-top': '12px', 'padding-bottom': '12px', 'padding-left': '24px', 'padding-right': '24px', 'border-top-left-radius': '8px', 'border-top-right-radius': '8px', 'border-bottom-right-radius': '8px', 'border-bottom-left-radius': '8px', 'background-color': '#2271b1', color: '#fff', 'font-weight': '600', 'line-height': '1.2', 'text-decoration-line': 'none' },
      { 'background-color': '#135e96' },
    ),
  },
  { id: 'link', group: 'basic', label: 'Link', html: '<a data-wbc="p1" href="#">Link text</a>', local: one({ 'text-decoration-line': 'underline' }) },
  { id: 'image', group: 'media', label: 'Image', html: `<img data-wbc="p1" src="${PLACEHOLDER_IMAGE}" alt="">`, local: one({ display: 'block', 'max-width': '100%', height: 'auto' }) },
  { id: 'icon', group: 'media', label: 'Icon', html: DEFAULT_ICON.replace('<svg ', '<svg data-wbc="p1" '), local: one({ display: 'inline-block', width: '32px', height: '32px' }) },
  { id: 'video', group: 'media', label: 'Video', html: '<video data-wbc="p1" controls playsinline preload="metadata"></video>', local: one({ display: 'block', width: '100%', 'aspect-ratio': '16 / 9', 'background-color': '#0f172a' }) },
  { id: 'divider', group: 'basic', label: 'Divider', html: '<hr data-wbc="p1">', local: one({ 'border-top-width': '1px', 'border-right-width': '0', 'border-bottom-width': '0', 'border-left-width': '0', 'border-top-style': 'solid', 'border-top-color': 'currentColor', opacity: '0.2', 'margin-top': '24px', 'margin-bottom': '24px' }) },
  {
    id: 'list',
    group: 'basic',
    label: 'List',
    html: '<ul data-wbc="p1"><li data-wbc="p2">First item</li><li data-wbc="p3">Second item</li><li data-wbc="p4">Third item</li></ul>',
    local: one({ 'list-style-type': 'disc', 'padding-left': '1.25em', 'margin-top': '0', 'margin-bottom': '1em', 'line-height': '1.6' }),
  },
  { id: 'input', group: 'form', label: 'Input', html: '<input data-wbc="p1" type="text" name="name" placeholder="Your name">', local: one(FIELD) },
  { id: 'textarea', group: 'form', label: 'Textarea', html: '<textarea data-wbc="p1" name="message" rows="4" placeholder="Your message"></textarea>', local: one(FIELD) },
  { id: 'select', group: 'form', label: 'Select', html: '<select data-wbc="p1" name="choice"><option value="">Choose…</option><option value="a">Option A</option><option value="b">Option B</option></select>', local: one(FIELD) },
  {
    id: 'checkbox',
    group: 'form',
    label: 'Checkbox',
    html: '<label data-wbc="p1"><input data-wbc="p2" type="checkbox" name="agree" value="1"> I agree</label>',
    local: one({ display: 'inline-flex', 'align-items': 'center', gap: '8px' }),
  },
  {
    id: 'submit',
    group: 'form',
    label: 'Submit button',
    html: '<button data-wbc="p1" type="submit">Send</button>',
    local: one(
      { 'padding-top': '12px', 'padding-bottom': '12px', 'padding-left': '24px', 'padding-right': '24px', 'border-top-left-radius': '8px', 'border-top-right-radius': '8px', 'border-bottom-right-radius': '8px', 'border-bottom-left-radius': '8px', 'background-color': '#2271b1', color: '#fff', 'font-weight': '600', cursor: 'pointer', 'border-top-width': '0', 'border-right-width': '0', 'border-bottom-width': '0', 'border-left-width': '0' },
      { 'background-color': '#135e96' },
    ),
  },
  { id: 'html', group: 'basic', label: 'HTML / Embed', html: '<div data-wbc="p1"><p data-wbc="p2">Custom HTML: select this block and edit its HTML in the Content tab.</p></div>', local: {} },
]

export const paletteItem = (id: string) => PALETTE.find((p) => p.id === id)
