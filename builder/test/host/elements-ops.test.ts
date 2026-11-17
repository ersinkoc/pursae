/**
 * Element editor (builder/host/elements): the source-preserving tree and the element operations
 * over code sections' markup.
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { parse, outer, wbc } from '../../host/elements/html'
import { addClass, dropError, duplicate, insert, move, pageIds, remove, removeClass, setAttr, setIdSource, setInner, setText, step, unwrap, wrap, type Page } from '../../host/elements/ops'

let n = 0
beforeEach(() => {
  n = 0
  setIdSource(() => `x${++n}`)
})

const page = (html: Record<string, string>): Page => ({ order: Object.keys(html), html })
const apply = (p: Page, r: { html: Record<string, string> } | null): Page => {
  expect(r).not.toBeNull()
  return { ...p, html: { ...p.html, ...r!.html } }
}

const CARDS = `<section data-wbc="1">
  <div class="grid" data-wbc="2">
    <div class="col" data-wbc="3">
      <a class="card" href="/a/" data-wbc="4"><span data-wbc="5">A</span></a>
      <a class="card" href="/b/" data-wbc="6"><span data-wbc="7">B</span></a>
    </div>
    <div class="col" data-wbc="8">
      <a class="card" href="/c/" data-wbc="9"><span data-wbc="10">C</span></a>
    </div>
    <div class="col empty" data-wbc="11"></div>
  </div>
</section>`

describe('parse', () => {
  it('records ranges, ids and attributes', () => {
    const t = parse(CARDS)
    expect(t.byId.size).toBe(11)
    const a = t.byId.get('4')!
    expect(outer(t, a)).toBe('<a class="card" href="/a/" data-wbc="4"><span data-wbc="5">A</span></a>')
    expect(a.parent && wbc(a.parent)).toBe('3')
  })

  it('handles void, self-closing svg, raw text and implied ends', () => {
    const src = `<p data-wbc="1">one<p data-wbc="2">two</p><img src="a.png" data-wbc="3"/><svg data-wbc="4"><path d="M0"/><g><rect/></g></svg><script>if (a < b) document.write('</div>')</script><ul data-wbc="5"><li data-wbc="6">a<li data-wbc="7">b</ul>`
    const t = parse(src)
    const root = t.root.children.filter((c) => c.type === 'el').map((c) => (c as { tag: string }).tag)
    expect(root).toEqual(['p', 'p', 'img', 'svg', 'script', 'ul'])
    expect(t.byId.get('4')!.children.length).toBe(2)
    expect(t.byId.get('4')!.children.every((c) => c.type === 'el' && c.foreign)).toBe(true)
    expect(t.byId.get('5')!.children.length).toBe(2)
    expect(outer(t, t.all.find((e) => e.tag === 'script')!)).toContain("'</div>'")
  })

  it('reads entities in attribute values', () => {
    const t = parse(`<a title="A &amp; B &quot;c&quot;" data-wbc="1">x</a>`)
    expect(t.byId.get('1')!.attrs[0].value).toBe('A & B "c"')
  })
})

describe('operations', () => {
  it('set text and inner markup touch only the element', () => {
    const p = page({ s1: CARDS })
    const r = setText(p, '5', 'A & <b>')
    expect(r!.html.s1).toBe(CARDS.replace('<span data-wbc="5">A</span>', '<span data-wbc="5">A &amp; &lt;b&gt;</span>'))
    expect(setInner(p, '5', 'A')).toBeNull() // unchanged
  })

  it('sets, removes attributes and classes in place', () => {
    let p = page({ s1: CARDS })
    p = apply(p, setAttr(p, '4', 'href', '/x?a=1&b="2"'))
    expect(p.html.s1).toContain('<a class="card" href="/x?a=1&amp;b=&quot;2&quot;" data-wbc="4">')
    p = apply(p, setAttr(p, '4', 'target', '_blank'))
    expect(p.html.s1).toContain('data-wbc="4" target="_blank">')
    p = apply(p, setAttr(p, '4', 'target', null))
    expect(p.html.s1).toContain('data-wbc="4">')
    p = apply(p, addClass(p, '4', 'is-new'))
    expect(p.html.s1).toContain('class="card is-new"')
    expect(addClass(p, '4', 'is-new')).toBeNull()
    p = apply(p, removeClass(p, '4', 'card'))
    expect(p.html.s1).toContain('class="is-new"')
    expect(setAttr(p, '4', 'data-wbc', '9')).toBeNull()
  })

  it('moves an element into another column, keeping the whitespace around it', () => {
    let p = page({ s1: CARDS })
    p = apply(p, move(p, '6', { sec: 's1', parent: '8', index: 0 }))
    const t = parse(p.html.s1)
    expect(t.byId.get('6')!.parent && wbc(t.byId.get('6')!.parent!)).toBe('8')
    expect(p.html.s1).toContain(`<div class="col" data-wbc="3">
      <a class="card" href="/a/" data-wbc="4"><span data-wbc="5">A</span></a>
    </div>`)
    expect(p.html.s1).toContain(`<div class="col" data-wbc="8">
      <a class="card" href="/b/" data-wbc="6"><span data-wbc="7">B</span></a>
      <a class="card" href="/c/" data-wbc="9">`)
    // back where it was: the markup is the original again
    p = apply(p, move(p, '6', { sec: 's1', parent: '3', index: 1 }))
    expect(p.html.s1).toBe(CARDS)
  })

  it('moves into an empty container, to the top level and across sections', () => {
    let p = page({ s1: CARDS, s2: '<footer data-wbc="20"><p data-wbc="21">x</p></footer>' })
    p = apply(p, move(p, '9', { sec: 's1', parent: '11', index: 0 }))
    expect(p.html.s1).toContain('<div class="col empty" data-wbc="11"><a class="card" href="/c/" data-wbc="9"><span data-wbc="10">C</span></a></div>')
    p = apply(p, move(p, '4', { sec: 's2', parent: '20', index: 1 }))
    expect(p.html.s2).toBe('<footer data-wbc="20"><p data-wbc="21">x</p><a class="card" href="/a/" data-wbc="4"><span data-wbc="5">A</span></a></footer>')
    expect(p.html.s1).not.toContain('data-wbc="4"')
    p = apply(p, move(p, '21', { sec: 's1', parent: null, index: 0 }))
    expect(p.html.s1.startsWith('<p data-wbc="21">x</p><section')).toBe(true)
  })

  it('same position and invalid targets do nothing', () => {
    const p = page({ s1: CARDS })
    expect(move(p, '4', { sec: 's1', parent: '3', index: 0 })).toBeNull()
    expect(move(p, '6', { sec: 's1', parent: '3', index: 1 })).toBeNull()
    expect(dropError(p, '2', { sec: 's1', parent: '3', index: 0 })).toBe('self') // into its own descendant
    expect(dropError(p, '2', { sec: 's1', parent: '2', index: 0 })).toBe('self')
    expect(dropError(p, '6', { sec: 's1', parent: '5', index: 0 })).toBe('leaf') // a text element
  })

  it('refuses void elements, svg insides and script / style as drop targets or moved elements', () => {
    const src = '<div data-wbc="1"><img src="a" data-wbc="2"/><svg data-wbc="3"><g id="g"></g></svg><script data-wbc="4">x()</script><style data-wbc="5">a{}</style><p data-wbc="6">t</p></div><div data-wbc="7"></div>'
    const p = page({ s: src })
    expect(dropError(p, '6', { sec: 's', parent: '2', index: 0 })).toBe('leaf')
    expect(dropError(p, '6', { sec: 's', parent: '3', index: 0 })).toBe('leaf')
    expect(dropError(p, '4', { sec: 's', parent: '7', index: 0 })).toBe('code')
    expect(dropError(p, '5', { sec: 's', parent: '7', index: 0 })).toBe('code')
    expect(move(p, '4', { sec: 's', parent: '7', index: 0 })).toBeNull()
    expect(step(p, '5', -1)).toBeNull()
    expect(dropError(p, '3', { sec: 's', parent: '7', index: 0 })).toBeNull() // the svg moves as a whole
  })

  it('step moves among element siblings', () => {
    let p = page({ s1: CARDS })
    p = apply(p, step(p, '6', -1))
    const col = parse(p.html.s1).byId.get('3')!
    expect(col.children.filter((c) => c.type === 'el').map((c) => wbc(c as never))).toEqual(['6', '4'])
    expect(step(p, '6', -1)).toBeNull()
  })

  it('deletes, but not the only top-level element of a section', () => {
    const p = page({ s1: CARDS })
    const r = remove(p, '6')!
    expect(r.html.s1).toBe(CARDS.replace(`\n      <a class="card" href="/b/" data-wbc="6"><span data-wbc="7">B</span></a>`, ''))
    expect(r.select).toBe('3')
    expect(remove(p, '1')).toBeNull()
  })

  it('duplicates with new ids and unique HTML ids, references following', () => {
    const src = `<div data-wbc="1">
  <button aria-controls="panel" id="btn" data-wbc="2">Open</button>
  <div id="panel" aria-labelledby="btn" data-wbc="3"><label for="f" data-wbc="4">L</label><input id="f" data-wbc="5"/><a href="#panel" data-wbc="6">x</a><svg data-wbc="7"><linearGradient id="grad"></linearGradient><rect fill="url(#grad)"/></svg></div>
</div>`
    let p = page({ s: src, t: '<p id="panel-2" data-wbc="9">taken</p>' })
    const r = duplicate(p, '1')!
    p = apply(p, r)
    expect(r.select).toBe('x1')
    const t = parse(p.html.s)
    const ids = t.all.map((e) => e.attrs.find((a) => a.name === 'id')?.value).filter(Boolean)
    // ids unique on the page after the duplicate (svg insides are left as they are)
    const html = ids.filter((x) => x !== 'grad')
    expect(new Set(html).size).toBe(html.length)
    expect(pageIds(p).has('panel-3')).toBe(true)
    expect(p.html.s).toContain('<button aria-controls="panel-3" id="btn-2" data-wbc="x2">')
    expect(p.html.s).toContain('<div id="panel-3" aria-labelledby="btn-2" data-wbc="x3">')
    expect(p.html.s).toContain('<label for="f-2" data-wbc="x4">')
    expect(p.html.s).toContain('<a href="#panel-3" data-wbc="x6">')
    expect(p.html.s).toContain('<svg data-wbc="x7"><linearGradient id="grad"></linearGradient><rect fill="url(#grad)"/></svg>')
    const wbcs = t.all.map(wbc).filter(Boolean)
    expect(new Set(wbcs).size).toBe(wbcs.length)
    // the original is untouched and the copy follows it after the same whitespace
    expect(p.html.s.startsWith(src)).toBe(true)
  })

  it('keeps whitespace text nodes between inline elements', () => {
    const src = '<p data-wbc="1"><a data-wbc="2">a</a> <a data-wbc="3">b</a> <a data-wbc="4">c</a></p>'
    let p = page({ s: src })
    p = apply(p, duplicate(p, '3'))
    expect(p.html.s).toBe('<p data-wbc="1"><a data-wbc="2">a</a> <a data-wbc="3">b</a> <a data-wbc="x1">b</a> <a data-wbc="4">c</a></p>')
    p = apply(p, remove(p, 'x1'))
    expect(p.html.s).toBe(src)
  })

  it('wraps in a div and unwraps', () => {
    let p = page({ s1: CARDS })
    const w = wrap(p, '4')!
    p = apply(p, w)
    expect(w.select).toBe('x1')
    expect(p.html.s1).toContain('<div data-wbc="x1"><a class="card" href="/a/" data-wbc="4">')
    p = apply(p, unwrap(p, 'x1'))
    expect(p.html.s1).toBe(CARDS)
    expect(unwrap(p, '5')).toBeNull() // a text element
  })

  it('inserts pasted markup with fresh ids', () => {
    let p = page({ s1: CARDS })
    const r = insert(p, { sec: 's1', parent: '11', index: 0 }, '<a class="card" id="c" data-wbc="4"><span data-wbc="5">A</span></a>')!
    p = apply(p, r)
    expect(r.select).toBe('x1')
    expect(p.html.s1).toContain('<div class="col empty" data-wbc="11"><a class="card" id="c-2" data-wbc="x1"><span data-wbc="x2">A</span></a></div>')
    expect(insert(p, { sec: 's1', parent: '5', index: 0 }, '<b>x</b>')).toBeNull()
  })

  it('never changes bytes outside the edit', () => {
    const odd = `<div  class='a'   data-wbc=2 >\n\t<p data-wbc="3" >x&nbsp;y</p ><br>\n<img src=x.png data-wbc="4">\n</div>`
    let p = page({ s: odd })
    p = apply(p, setAttr(p, '4', 'alt', 'pic'))
    expect(p.html.s).toBe(`<div  class='a'   data-wbc=2 >\n\t<p data-wbc="3" >x&nbsp;y</p ><br>\n<img src=x.png data-wbc="4" alt="pic">\n</div>`)
    p = apply(p, setAttr(p, '2', 'class', 'b'))
    expect(p.html.s.startsWith(`<div  class="b"   data-wbc=2 >`)).toBe(true)
  })
})
