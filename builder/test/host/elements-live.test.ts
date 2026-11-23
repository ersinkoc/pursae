// @vitest-environment jsdom
/**
 * Element editor, live frame (elements/live.ts): the client-built live document (the srcdoc
 * fallback), the editor stylesheets, kept state and applyToLive (the live DOM patched from the
 * source delta, never read back).
 */
import { describe, expect, it } from 'vitest'
import type { Section } from '@/builder/types'
import { applyToLive, liveDoc, liveGeometry, setLiveStyles, withNonce, type LiveInput } from '../../host/elements/live'

const sec = (id: string, html: string, extra: Record<string, string> = {}): Section =>
  ({ id, kind: 'code', name: id, content: { html, head: '', wrap: '', ...extra }, items: [], style: { hideOn: [] } }) as unknown as Section

const input = (page: Section[], more: Partial<LiveInput> = {}): LiveInput => ({
  postId: 0,
  page,
  header: [],
  footer: [],
  shell: 'theme',
  url: 'https://site.test/a/',
  kitCss: '',
  localCss: '',
  editCss: '',
  ...more,
})

/** A live document made from markup (as the frame would show it). */
function live(html: string): Document {
  const d = document.implementation.createHTMLDocument('')
  d.body.innerHTML = html
  return d
}

describe('liveDoc (srcdoc fallback)', () => {
  const shell = '<div class="shell">'
  const doc = liveDoc(
    input([sec('p1', '<section data-wbc="1">A</section>', { wrap: shell, lang: 'tr', head: '<link rel="stylesheet" href="/k/page.css">\n<script>document.documentElement.classList.add("dark")</script>', htmlAttrs: 'class="no-js" data-theme="light"', bodyAttrs: 'class="antialiased" data-x="1"' }), sec('t1', '<script src="/k/app.js"></script>', { tail: '1' })], {
      header: [sec('h1', '<header data-wbc="1">H</header>', { wrap: shell, head: '<link rel="stylesheet" href="/k/page.css">\n<link rel="stylesheet" href="/k/header.css">' })],
      footer: [sec('f1', '<footer data-wbc="1">F</footer>', { wrap: shell })],
    }),
    'N0NCE',
  )
  it('is the published page: doctype, attributes, page head first, the editor styles last', () => {
    expect(doc.startsWith('<!DOCTYPE html>')).toBe(true)
    expect(doc).toContain('<html lang="tr" class="no-js" data-theme="light">')
    expect(doc).toMatch(/<body class="antialiased" data-x="1">/)
    expect(doc.indexOf('page.css')).toBeLessThan(doc.indexOf('header.css'))
    expect(doc.match(/page\.css/g)!.length).toBe(1)
    const head = doc.slice(0, doc.indexOf('</head>'))
    expect(head.endsWith('<style data-wb-edit></style>\n')).toBe(true)
    expect(doc).toContain('<base href="https://site.test/a/"')
  })
  it('puts main#content inside the shared shell and the tail after the footer', () => {
    const body = doc.slice(doc.indexOf('<body'))
    expect(body.match(/class="shell"/g)!.length).toBe(1)
    expect(body).toContain('<div class="shell"><header data-wbc="h:1">H</header><main id="content" class="site-main wb-main-part"><section data-wbc="1">A</section></main><footer data-wbc="f:1">F</footer></div><script src="/k/app.js" nonce="N0NCE"></script>')
  })
  it('gives every script the nonce, the GL hook first in the head', () => {
    expect(doc).toContain('<script nonce="N0NCE">document.documentElement.classList.add("dark")</script>')
    expect(doc.indexOf('<script nonce="N0NCE">(function(){var P=HTMLCanvasElement')).toBeLessThan(doc.indexOf('<meta charset'))
    expect(withNonce('<script nonce="old">x</script>', 'n')).toBe('<script nonce="n">x</script>')
  })
  it('a canvas page gets main#wb-main, a design main gets none', () => {
    expect(liveDoc(input([sec('p', '<p data-wbc="1">x</p>')], { shell: 'canvas' }))).toContain('<body class="web-builder-page">\n<main id="wb-main"><p data-wbc="1">x</p></main>')
    expect(liveDoc(input([sec('p', '<main data-wbc="1">x</main>')]))).not.toContain('id="content"')
  })
})

describe('setLiveStyles', () => {
  it('creates the three stylesheets and keeps them last', () => {
    const d = document.implementation.createHTMLDocument('')
    setLiveStyles(d, { kit: '.a{}', local: '.b{}', edit: '.c{}' })
    const s = d.createElement('style')
    d.head.appendChild(s) // a design script adds a stylesheet afterwards
    setLiveStyles(d, { local: '.b2{}' })
    const names = Array.from(d.head.children).map((e) => e.getAttributeNames().join())
    expect(names.slice(-3)).toEqual(['data-wb-kit', 'data-wb-local', 'data-wb-edit'])
    expect(d.head.querySelector('style[data-wb-local]')!.textContent).toBe('.b2{}')
  })
})

describe('liveGeometry', () => {
  it('is the device viewport scaled to fit', () => {
    expect(liveGeometry('desktop', { w: 1080, h: 1000 })).toEqual({ width: 1440, height: 900, zoom: 0.75 })
    expect(liveGeometry('mobile', { w: 2000, h: 2000 }).zoom).toBe(1)
  })
})

describe('applyToLive', () => {
  const A = '<section data-wbc="1" class="hero"><h2 data-wbc="2">Hello</h2><p data-wbc="3" style="color: red">Text</p><canvas data-wbc="4"></canvas></section>'
  it('changes attributes by delta: what the design script set stays', () => {
    const d = live(`<div id="shell">${A}</div>`)
    const sec1 = d.querySelector('[data-wbc="1"]')!
    sec1.classList.add('is-visible') // IntersectionObserver reveal
    const p = d.querySelector('[data-wbc="3"]') as HTMLElement
    p.style.transform = 'translateY(0px)' // a scroll effect
    const next = A.replace('class="hero"', 'class="hero big"').replace('color: red', 'color: blue')
    const r = applyToLive(d, { s: A }, { s: next })
    expect(r.reload).toBe(false)
    expect(sec1.className).toBe('hero is-visible big')
    expect(p.style.color).toBe('blue')
    expect(p.style.transform).toBe('translateY(0px)')
    expect(d.querySelector('[data-wbc="1"]')).toBe(sec1) // same node
  })
  it('edits text and moves elements, keeping the live nodes (a running canvas)', () => {
    const d = live(`<div id="shell">${A}</div>`)
    const canvas = d.querySelector('canvas')!
    const next = '<section data-wbc="1" class="hero"><canvas data-wbc="4"></canvas><h2 data-wbc="2">Hi <b>there</b></h2><p data-wbc="3" style="color: red">Text</p></section>'
    const r = applyToLive(d, { s: A }, { s: next })
    expect(r.reload).toBe(false)
    expect(d.querySelector('section')!.innerHTML).toBe('<canvas data-wbc="4"></canvas><h2 data-wbc="2">Hi <b>there</b></h2><p data-wbc="3" style="color: red">Text</p>')
    expect(d.querySelector('canvas')).toBe(canvas)
  })
  it('deletes, duplicates and adds top-level elements in place', () => {
    const d = live('<div id="w"><i>before</i><h1 data-wbc="a">A</h1>\n<p data-wbc="b">B</p><i>after</i></div>')
    const r = applyToLive(d, { s: '<h1 data-wbc="a">A</h1>\n<p data-wbc="b">B</p>' }, { s: '<p data-wbc="b">B</p><p data-wbc="c">B</p>' })
    expect(r.reload).toBe(false)
    expect(d.getElementById('w')!.innerHTML).toBe('<i>before</i><p data-wbc="b">B</p><p data-wbc="c">B</p><i>after</i>')
  })
  it('reloads for scripts, and when the design restructured what changes', () => {
    const d = live(`<div>${A}</div>`)
    expect(applyToLive(d, { s: A }, { s: A + '<script>x()</script>' })).toMatchObject({ reload: true })
    expect(applyToLive(d, { s: A }, { s: A.replace('Text', 'T<script>x()</script>') })).toMatchObject({ reload: true })
    // A slider wraps the children in its own track: a text change inside must reload.
    const d2 = live('<div data-wbc="s"><div class="track"><img data-wbc="i1"><img data-wbc="i2"></div></div>')
    const was = '<div data-wbc="s"><img data-wbc="i1"><img data-wbc="i2"></div>'
    expect(applyToLive(d2, { s: was }, { s: was.replace('<img data-wbc="i2">', '') })).toMatchObject({ reload: true })
    // …but an attribute change still applies (to the slider's clones too).
    const d3 = live('<div data-wbc="s"><div class="track"><img data-wbc="i1" alt="a"><img data-wbc="i1" alt="a" class="clone"></div></div>')
    const w3 = '<div data-wbc="s"><img data-wbc="i1" alt="a"></div>'
    expect(applyToLive(d3, { s: w3 }, { s: w3.replace('alt="a"', 'alt="b"') }).reload).toBe(false)
    expect(Array.from(d3.querySelectorAll('img')).map((i) => i.alt)).toEqual(['b', 'b'])
    // Nothing was written by the refused patches.
    expect(d.body.innerHTML).toBe(`<div>${A}</div>`)
  })
  it('counts sections added or removed as a reload, unchanged as nothing', () => {
    const d = live(A)
    expect(applyToLive(d, { s: A }, { s: A })).toEqual({ reload: false, touched: [] })
    expect(applyToLive(d, { s: A }, { s: A, t: '<p data-wbc="9">x</p>' }).reload).toBe(true)
  })
  it('rebuilds an svg from its new markup', () => {
    const was = '<div data-wbc="1"><svg data-wbc="2" viewBox="0 0 10 10"><path d="M0 0"></path></svg></div>'
    const d = live(was)
    const r = applyToLive(d, { s: was }, { s: was.replace('M0 0', 'M1 1') })
    expect(r.reload).toBe(false)
    expect(d.querySelector('path')!.getAttribute('d')).toBe('M1 1')
    expect(d.querySelector('svg')!.getAttribute('viewBox')).toBe('0 0 10 10')
  })
})
