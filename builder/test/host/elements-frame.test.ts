/**
 * Element editor: the frame document (static copy, locked parts, shared wrappers), element types
 * and the drop rules / places the canvas and the navigator use.
 */
import { describe, expect, it } from 'vitest'
import type { Section } from '@/builder/types'
import { INERT, frameCopy, frameDoc, mergeHeads } from '../../host/elements/frame'
import { parse } from '../../host/elements/html'
import { kindOf, namesOf } from '../../host/elements/kinds'
import { container, dropError, movable, placeBy, whereIs, type Page } from '../../host/elements/ops'

const sec = (id: string, html: string, extra: Record<string, string> = {}): Section =>
  ({ id, kind: 'code', name: id, content: { html, head: '', wrap: '', ...extra }, items: [], style: { hideOn: [] } }) as unknown as Section

describe('frame copy', () => {
  it('static mode: scripts do not run, inline handlers go, the stored markup is untouched', () => {
    const src = '<div onclick="x()" data-wbc="1"><script>boom()</script><script type="module" src="a.js"></script><svg onload="y()"><g/></svg></div>'
    const out = frameCopy(src, 'static')
    expect(out).toBe(`<div data-wbc="1"><script type="${INERT}">boom()</script><script type="${INERT}" src="a.js"></script><svg><g/></svg></div>`)
    expect(frameCopy(src, 'live')).toBe(src)
  })

  it('locked parts get prefixed ids', () => {
    expect(frameCopy('<header data-wbc="2"><a data-wbc="3">x</a></header>', 'live', 'h:')).toBe('<header data-wbc="h:2"><a data-wbc="h:3">x</a></header>')
  })

  it('merges heads once, inert in static mode', () => {
    const h = '<link rel="stylesheet" href="/k/style.css"/>\n<script>document.documentElement.classList.add(\'js\')</script>'
    const m = mergeHeads([h, h], 'static')
    expect(m.match(/style\.css/g)!.length).toBe(1)
    expect(m).toContain(`<script type="${INERT}">`)
  })

  it('composes header, page and footer in shared wrappers with section markers', () => {
    const shell = '<div class="flex min-h-screen flex-col">'
    const header = [sec('h1', '<header data-wbc="1">H</header>', { wrap: shell })]
    const footer = [sec('f1', '<footer data-wbc="1">F</footer>', { wrap: shell })]
    const page = [sec('p1', '<section data-wbc="1">A</section>', { wrap: `${shell}\n<main id="icerik">`, lang: 'tr', body: 'antialiased' }), sec('p2', '<section data-wbc="2">B</section>', { wrap: `${shell}\n<main id="icerik">` })]
    const doc = frameDoc(page, { header, footer }, 'static', '', 'https://x.test/')
    expect(doc).toContain('<html lang="tr">')
    expect(doc).toContain('<body class="antialiased">')
    // one shell around all parts, one main around both page sections
    expect(doc.match(/flex min-h-screen/g)!.length).toBe(1)
    expect(doc.match(/<main/g)!.length).toBe(1)
    const body = doc.slice(doc.indexOf('<body'))
    expect(body.indexOf('data-wbc="h:1"')).toBeLessThan(body.indexOf('<!--wb:p1--><section data-wbc="1">A</section><!--/wb:p1-->'))
    expect(body).toContain('<!--wb:p2--><section data-wbc="2">B</section><!--/wb:p2--></main><!--wb:f:f1--><footer data-wbc="f:1">F</footer><!--/wb:f:f1--></div>')
    expect(doc).toContain('<style data-wb-edit>')
  })
})

describe('element types', () => {
  const t = parse(
    '<section data-wbc="1"><div class="flex gap-2" data-wbc="2"><h2 data-wbc="3">T</h2><p data-wbc="4">a <b>b</b></p><a class="flex" data-wbc="5"><span data-wbc="6">x</span></a><img data-wbc="7"/><svg data-wbc="8"></svg><ul data-wbc="9"><li data-wbc="10"><a class="group flex" data-wbc="11"><span>c</span></a></li></ul><div class="grid" data-wbc="12"><i data-wbc="13"></i></div><button data-wbc="14">b</button><script data-wbc="15"></script></div></section>',
  )
  const k = (id: string, display?: string) => kindOf(t, t.byId.get(id)!, display)
  it('derives types from tag and display', () => {
    expect(k('1')).toBe('section')
    expect(k('2')).toBe('flex')
    expect(k('2', 'block')).toBe('div')
    expect(k('3')).toBe('heading')
    expect(k('4')).toBe('text')
    expect(k('5')).toBe('link')
    expect(k('7')).toBe('image')
    expect(k('8')).toBe('icon')
    expect(k('9')).toBe('list')
    expect(k('10')).toBe('div') // a list item holding a card is not text
    expect(k('12')).toBe('grid')
    expect(k('14')).toBe('button')
    expect(k('15')).toBe('code')
  })
  it('reads stored names', () => {
    expect(namesOf('{"3":"Hero title","x":5}')).toEqual({ '3': 'Hero title' })
    expect(namesOf('nope')).toEqual({})
  })
})

describe('drop rules and places', () => {
  const page: Page = {
    order: ['a', 'b'],
    html: {
      a: '<div data-wbc="1"><p data-wbc="2">t</p><div data-wbc="3"></div><span data-wbc="4">s</span></div><div data-wbc="5"></div>',
      b: '<section data-wbc="9"><div data-wbc="10"></div></section>',
    },
  }
  const ta = parse(page.html.a)
  it('containers, movable elements', () => {
    expect(container(ta, ta.byId.get('1')!)).toBe(true)
    expect(container(ta, ta.byId.get('2')!)).toBe(false) // text of its own
    expect(container(ta, ta.byId.get('3')!)).toBe(true) // empty div
    expect(movable(ta.byId.get('2')!)).toBe(true)
  })
  it('places before / after a reference, counted without the moved element', () => {
    expect(placeBy(page, 'a', '1', '4', false, '2')).toEqual({ sec: 'a', parent: '1', index: 1 })
    expect(placeBy(page, 'a', '1', '4', true)).toEqual({ sec: 'a', parent: '1', index: 3 })
    expect(placeBy(page, 'a', null, '5', false)).toEqual({ sec: 'a', parent: null, index: 1 })
    expect(placeBy(page, 'b', '10', null, true)).toEqual({ sec: 'b', parent: '10', index: 0 })
    expect(whereIs(page, '4')).toEqual({ sec: 'a', parent: '1' })
    expect(whereIs(page, '9')).toEqual({ sec: 'b', parent: null })
  })
  it('rejects a node into itself, into text elements, and missing targets', () => {
    expect(dropError(page, '1', { sec: 'a', parent: '3', index: 0 })).toBe('self')
    expect(dropError(page, '4', { sec: 'a', parent: '2', index: 0 })).toBe('leaf')
    expect(dropError(page, '4', { sec: 'header', parent: null, index: 0 })).toBe('locked')
    expect(dropError(page, '4', { sec: 'b', parent: '10', index: 0 })).toBeNull() // across sections, empty container
    expect(dropError(page, 'zz', { sec: 'b', parent: '10', index: 0 })).toBe('missing')
  })
})
