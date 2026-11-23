/**
 * Basic mode's fields (builder/host/elements/fields.ts): what is found in a code section's stored
 * markup, in document order, grouped, capped, and named for the section list.
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { extractFields, fieldFor, flatFields, limitItems, nameOf, pathTo, summarize, type Field, type Group } from '../../host/elements/fields'
import { setIdSource, tree, withIds } from '../../host/elements/ops'

let n = 0
beforeEach(() => {
  n = 0
  setIdSource(() => `w${++n}`)
})

/** Markup as the importer stores it: every element has a data-wbc id. */
const ided = (html: string) => withIds(html)
const kinds = (html: string) => flatFields(extractFields(ided(html))).map((f) => `${f.kind}:${f.text || f.hint}`)

const HERO = `
<section class="relative isolate overflow-hidden bg-slate-950 px-6 py-24 hero">
  <div class="mx-auto max-w-3xl text-center">
    <p class="text-sm font-semibold text-indigo-400">New in 2026</p>
    <h1 class="text-5xl font-bold tracking-tight text-white">Ship your <span class="text-indigo-400">landing page</span> faster</h1>
    <p class="mt-6 text-lg text-slate-300">Everything you need to launch, in one place.</p>
    <div class="mt-10 flex items-center justify-center gap-x-6">
      <a href="/signup/" class="rounded-md bg-indigo-500 px-4 py-2.5 btn btn-primary">Get started</a>
      <a href="#features" class="text-sm font-semibold text-white">Learn more <span aria-hidden="true">→</span></a>
    </div>
    <img src="/uploads/hero-shot.png" alt="Dashboard" class="mt-16 rounded-xl" width="1200" height="800">
    <svg class="icon" viewBox="0 0 10 10"><path d="M0 0"/></svg>
    <p aria-hidden="true">decorative</p>
    <p class="empty">   </p>
    <p hidden>never shown</p>
    <div style="display:none"><h2>Not visible</h2></div>
    <script>window.x = '<h2>nope</h2>'</script>
    <style>.hero{color:red}</style>
  </div>
</section>`

describe('extractFields', () => {
  it('finds headings, texts, buttons, links and images in document order', () => {
    const fields = flatFields(extractFields(ided(HERO)))
    expect(fields.map((f) => f.kind)).toEqual(['text', 'heading', 'text', 'button', 'link', 'image'])
    const [eyebrow, h1, lead, cta, more, img] = fields
    expect(eyebrow.text).toBe('New in 2026')
    expect(h1.text).toBe('Ship your landing page faster')
    // Inline formatting inside a heading: edited as rich text, stored tags kept.
    expect(h1.edit).toBe('rich')
    expect(eyebrow.edit).toBe('plain')
    expect(lead.text).toBe('Everything you need to launch, in one place.')
    expect(cta).toMatchObject({ href: '/signup/', text: 'Get started', edit: 'plain' })
    // The arrow in an aria-hidden span is not part of the label text; the link keeps its inline markup.
    expect(more).toMatchObject({ href: '#features', edit: 'rich' })
    expect(img).toMatchObject({ src: '/uploads/hero-shot.png', alt: 'Dashboard', hint: 'hero-shot.png' })
  })

  it('leaves out empty, hidden, aria-hidden, script, style and svg content', () => {
    const all = kinds(HERO).join('|')
    expect(all).not.toMatch(/decorative|never shown|Not visible|nope|color:red/)
    expect(flatFields(extractFields(ided(HERO)))).toHaveLength(6)
  })

  it('skips elements without a data-wbc id (nothing can address them)', () => {
    expect(extractFields('<h1>Plain</h1><p data-wbc="a">Has id</p>').length).toBe(1)
  })

  it('returns nothing for empty markup', () => {
    expect(extractFields('')).toEqual([])
    expect(extractFields('  \n')).toEqual([])
  })

  it('reads form fields, the source of a video and an embed, and a picture', () => {
    const html = ided(`<form class="space-y-4" action="/send">
      <label for="e" class="block">Email</label>
      <input id="e" type="email" name="email" placeholder="you@example.com">
      <input type="hidden" name="nonce" value="x" placeholder="ignored">
      <textarea name="m" placeholder="Your message"></textarea>
      <input type="submit" value="Send">
      <button type="submit" class="btn">Send message</button>
    </form>
    <video class="w-full" controls><source src="/media/clip.mp4" type="video/mp4"></video>
    <iframe src="https://www.youtube.com/embed/abc" title="Tour"></iframe>
    <picture><source srcset="/a.webp" type="image/webp"><img src="/a.jpg" alt=""></picture>`)
    const f = flatFields(extractFields(html))
    expect(f.map((x) => x.kind)).toEqual(['text', 'input', 'input', 'button', 'video', 'video', 'image'])
    expect(f[1].placeholder).toBe('you@example.com')
    expect(f[2].placeholder).toBe('Your message')
    const video = f[4]
    expect(video.src).toBe('/media/clip.mp4')
    // The address is held by the source child.
    expect(video.srcId).not.toBe(video.id)
    expect(f[5]).toMatchObject({ src: 'https://www.youtube.com/embed/abc', tag: 'iframe' })
    expect(f[6]).toMatchObject({ src: '/a.jpg', alt: '' })
  })
})

const CARDS = `
<section id="features" class="py-24">
  <h2 class="text-3xl">Why teams pick us</h2>
  <div class="grid gap-8 md:grid-cols-3">
    <div class="card rounded-2xl p-6"><svg class="i"><path d="M0"/></svg><h3>Fast setup</h3><p>Minutes, not days.</p><a href="/a/">Read more</a></div>
    <div class="rounded-2xl card p-6"><svg class="i"><path d="M0"/></svg><h3>Safe by default</h3><p>Sane defaults.</p><a href="/b/">Read more</a></div>
    <div class="card rounded-2xl p-6"><h3>Flexible</h3><p>Bring your own tools.</p><a href="/c/">Read more</a></div>
  </div>
</section>`

describe('grouping', () => {
  it('turns repeated siblings (same tag and class list) into Card groups with their own fields', () => {
    const items = extractFields(ided(CARDS))
    expect(items.map((i) => i.type)).toEqual(['field', 'group', 'group', 'group'])
    const groups = items.filter((i): i is Group => i.type === 'group')
    expect(groups.map((g) => [g.noun, g.n, g.snippet, g.count])).toEqual([
      ['Card', 1, 'Fast setup', 3],
      ['Card', 2, 'Safe by default', 3],
      ['Card', 3, 'Flexible', 3],
    ])
    // The class order differs on the second card; it is still the same shape.
    expect(groups[1].items.map((i) => (i as Field).kind)).toEqual(['heading', 'text', 'link'])
  })

  it('does not group a single card, and names list items, slides and rows', () => {
    expect(extractFields(ided('<div class="grid"><div class="card"><h3>One</h3></div></div>')).every((i) => i.type === 'field')).toBe(true)
    const li = extractFields(ided('<ul><li><a href="/x/">X</a></li><li><a href="/y/">Y</a></li></ul>'))
    expect((li as Group[]).map((g) => g.noun)).toEqual(['Item', 'Item'])
    const slides = extractFields(ided('<div class="swiper"><div class="swiper-slide"><h3>A</h3></div><div class="swiper-slide"><h3>B</h3></div></div>'))
    expect((slides as Group[]).map((g) => g.noun)).toEqual(['Slide', 'Slide'])
    const rows = extractFields(ided('<table><tbody><tr><td>A</td><td>1</td></tr><tr><td>B</td><td>2</td></tr></tbody></table>'))
    expect((rows as Group[]).map((g) => g.noun)).toEqual(['Row', 'Row'])
  })

  it('groups three or more links side by side (a menu) and keeps two CTA buttons flat', () => {
    const nav = extractFields(ided(`<nav class="flex gap-6"><a class="nav-link" href="/">Home</a><a class="nav-link" href="/about/">About</a><a class="nav-link" href="/blog/">Blog</a></nav>`))
    expect(nav).toHaveLength(1)
    expect(nav[0]).toMatchObject({ type: 'group', noun: 'Links', count: 3 })
    const ctas = extractFields(ided(`<div><a class="btn" href="/a">Start</a><a class="btn" href="/b">Docs</a></div>`))
    expect(ctas.every((i) => i.type === 'field')).toBe(true)
  })

  it('nests groups (cards with repeated list items)', () => {
    const items = extractFields(
      ided(`<div class="grid"><div class="plan"><h3>Basic</h3><ul><li class="f">One</li><li class="f">Two</li></ul></div><div class="plan"><h3>Pro</h3><ul><li class="f">Three</li><li class="f">Four</li></ul></div></div>`),
    )
    expect(items).toHaveLength(2)
    expect(flatFields(items)).toHaveLength(6)
  })

  it('limits to the first fields, keeping groups cut short, and finds the way to a field', () => {
    const items = extractFields(ided(CARDS))
    const cut = limitItems(items, 5)
    expect(cut.total).toBe(10)
    expect(flatFields(cut.items)).toHaveLength(5)
    expect((cut.items[2] as Group).items).toHaveLength(1)
    expect(limitItems(items, 60).items).toBe(items)
    const last = flatFields(items).pop()!
    expect(pathTo(items, last.id)).toEqual([(items[3] as Group).key])
    expect(pathTo(items, 'nope')).toBeNull()
  })

  it('maps a selected element to its field: itself, around it, or inside it', () => {
    const html = ided(CARDS)
    const tr = tree(html)
    const items = extractFields(html)
    const first = (items[1] as Group).items[0] as Field
    expect(fieldFor(items, tr, first.id)?.id).toBe(first.id)
    // A card (container): its first field.
    const card = (items[1] as Group).id!
    expect(fieldFor(items, tr, card)?.id).toBe(first.id)
    // The text inside a link is part of the link's field.
    const link = (items[1] as Group).items[2] as Field
    expect(fieldFor(items, tr, link.id)?.id).toBe(link.id)
    expect(fieldFor(items, tr, 'missing')).toBeNull()
  })

  it('caps one section at a sane number of fields', () => {
    const many = Array.from({ length: 900 }, (_, i) => `<p>Line ${i}</p>`).join('')
    expect(flatFields(extractFields(ided(many))).length).toBe(600)
  })
})

describe('summarize and names', () => {
  it('uses the first heading as the title and counts what is inside', () => {
    const s = summarize(ided(CARDS))
    expect(s.title).toBe('Why teams pick us')
    expect(s.named).toBe(false)
    expect(s.counts).toMatchObject({ headings: 4, texts: 3, links: 3 })
  })

  it('names header, footer and menu sections by their landmark even when they hold headings', () => {
    expect(summarize(ided('<footer><h4>Company</h4><a href="/x">X</a></footer>'))).toMatchObject({ title: 'Footer', named: true })
    expect(summarize(ided('<header><h1>Brand</h1></header>'))).toMatchObject({ title: 'Header', named: true })
    expect(summarize(ided('<nav><a href="/">Home</a></nav>'))).toMatchObject({ title: 'Menu', named: true })
  })

  it('falls back to aria-label, a recognised class or id, an id, then nothing (the section keeps its own name)', () => {
    expect(summarize(ided('<section aria-label="Our partners"><img src="a.png" alt=""></section>'))).toMatchObject({ title: 'Our partners', named: false })
    expect(summarize(ided('<section class="py-20 hero-wrap"><img src="a.png" alt=""></section>'))).toMatchObject({ title: 'Hero', named: true })
    expect(summarize(ided('<div id="price-table"><img src="a.png" alt=""></div>'))).toMatchObject({ title: 'Pricing', named: true })
    expect(summarize(ided('<div id="our-story"><img src="a.png" alt=""></div>')).title).toBe('Our story')
    expect(summarize(ided('<div class="x1"><img src="a.png" alt=""></div>')).title).toBeNull()
    expect(summarize('')).toMatchObject({ title: null, counts: { texts: 0 } })
  })

  it('reads the name from the first visible top-level element', () => {
    expect(nameOf(tree('<script>1</script><footer data-wbc="a">x</footer>'))).toMatchObject({ name: 'Footer', landmark: true })
  })
})
