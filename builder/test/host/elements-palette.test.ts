/**
 * Element editor, Faz 4 and 5 (builder/host/elements): the palette's elements and their insert,
 * the "From this design" signature miner, tag change, attribute rules, icon replacement, and the
 * kit override generator (byte for byte with Go's KitOverrideCSS on the shared fixtures).
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { beforeEach, describe, expect, it } from 'vitest'
import { attr, parse, wbc } from '../../host/elements/html'
import { attrError, attrNameError, tagChangeOK, tagChoices, urlOK } from '../../host/elements/attrs'
import { classesInSelector, hexOf, splitSelectors, varGroup } from '../../host/elements/cssom'
import { copyEntries } from '../../host/elements/local'
import { countClass, mineSignatures, signature, sourcesHash } from '../../host/elements/miner'
import { insert, insertError, locate, replaceOuter, setIdSource, setTag, withIds, type Page } from '../../host/elements/ops'
import { asOverride, dropProp, editTarget, overrideCSS, ruleOf, sameOverride, selOK, ctxOK, setRuleDecls } from '../../host/elements/overrides'
import { PALETTE, paletteItem } from '../../host/elements/palette'

let n = 0
beforeEach(() => {
  n = 0
  setIdSource(() => `x${++n}`)
})

const page = (html: Record<string, string>): Page => ({ order: Object.keys(html), html })

const SECTION = `<section data-wbc="1">
  <div class="grid" data-wbc="2">
    <div class="col" data-wbc="3"><h2 data-wbc="4">Title</h2></div>
    <div class="col" data-wbc="5"></div>
  </div>
</section>`

describe('palette', () => {
  it('every item is one element with placeholder ids and a valid preset', () => {
    for (const p of PALETTE) {
      const t = parse(p.html)
      const tops = t.root.children.filter((c) => c.type === 'el')
      expect(tops.length, p.id).toBe(1)
      for (const el of t.all) if (el.tag !== '#root' && el.tag !== 'option' && !el.foreign) expect(wbc(el), `${p.id} <${el.tag}>`).toMatch(/^p\d+$/)
      for (const id of Object.keys(p.local)) expect(t.byId.has(id), `${p.id} local ${id}`).toBe(true)
      expect(p.html).not.toMatch(/\son\w+=/i)
    }
  })
  it('inserts with fresh ids, its local preset following them, as one splice', () => {
    const p = page({ s: SECTION })
    const btn = paletteItem('button')!
    const r = insert(p, { sec: 's', parent: '5', index: 0 }, btn.html)!
    expect(r.html.s).toContain('<div class="col" data-wbc="5"><a data-wbc="x1" href="#">Click here</a></div>')
    expect(r.select).toBe('x1')
    expect(r.copies).toEqual({ from: ['p1'], to: ['x1'] })
    const local = copyEntries(btn.local, r.copies!.from, r.copies!.to)
    expect(local.x1.v[0].p['background-color']).toBe('#2271b1')
    expect(local.x1.v[1]).toEqual({ bp: '', st: 'hover', p: { 'background-color': '#135e96' } })
    // Everything outside the insert is unchanged.
    expect(r.html.s.replace('<a data-wbc="x1" href="#">Click here</a>', '')).toBe(SECTION)
  })
  it('a list keeps its items, each with a new id; an svg icon gets an id, its inside does not', () => {
    const p = page({ s: SECTION })
    const r = insert(p, { sec: 's', parent: '2', index: 2 }, paletteItem('list')!.html)!
    expect(r.copies!.to).toEqual(['x1', 'x2', 'x3', 'x4'])
    const ic = insert(p, { sec: 's', parent: '5', index: 0 }, paletteItem('icon')!.html)!
    expect(ic.html.s).toMatch(/<svg data-wbc="x5" xmlns=/)
    expect(ic.html.s.match(/data-wbc=/g)!.length).toBe(6)
  })
  it('refuses a place that takes no element', () => {
    const p = page({ s: SECTION })
    expect(insertError(p, { sec: 's', parent: '4', index: 0 })).toBe('leaf') // a heading with text
    expect(insertError(p, { sec: 's', parent: '5', index: 0 })).toBeNull()
    expect(insertError(p, { sec: 'nope', parent: null, index: 0 })).toBe('locked')
  })
})

describe('From this design: the signature miner', () => {
  const card = (i: number) =>
    `<article class="card rounded-2xl p-6" data-wbc="c${i}"><img class="h-10 w-10" src="/i${i}.svg" alt="" data-wbc="i${i}"><h3 class="text-lg font-semibold" data-wbc="h${i}">Service ${i}</h3><p class="text-slate-600" data-wbc="p${i}">Text ${i}</p></article>`
  const btn = (i: number, extra = '') => `<a class="btn btn-primary${extra}" href="/x${i}" data-wbc="b${i}">Go ${i}</a>`
  const sources = [
    { html: `<section class="py-24" data-wbc="s1">${card(1)}${card(2)}${btn(1)}<span class="badge rounded-full bg-teal-50 px-3 text-xs" data-wbc="g1">New</span></section>`, page: 'a' },
    { html: `<section class="py-24" data-wbc="s2">${card(3)}${btn(2, ' ')}<span class="rounded-full bg-teal-50 badge px-3 text-xs" data-wbc="g2">Hot</span><div data-wbc="z"></div></section>`, page: 'b' },
    { html: `<section class="py-24" data-wbc="s3">${btn(3)}<span class="badge rounded-full bg-teal-50 px-3 text-xs" data-wbc="g3">Top</span><script class="x">1</script><script class="x">1</script><script class="x">1</script></section>`, page: 'c' },
  ]
  it('finds tags with the same class list three or more times, class order ignored', () => {
    const list = mineSignatures(sources)
    const by = Object.fromEntries(list.map((s) => [s.sig, s]))
    expect(by['a btn btn-primary']).toMatchObject({ kind: 'button', count: 3, pages: 3, text: 'Go 1' })
    expect(by['a btn btn-primary'].html).toBe(btn(1))
    expect(by['article card p-6 rounded-2xl']).toMatchObject({ kind: 'card', count: 3, pages: 2 })
    expect(by['h3 font-semibold text-lg']).toMatchObject({ kind: 'heading', count: 3 })
    expect(by['span badge bg-teal-50 px-3 rounded-full text-xs']).toMatchObject({ kind: 'badge', count: 3 })
    expect(by['section py-24']).toMatchObject({ kind: 'section', count: 3 })
    expect(by['img h-10 w-10']).toMatchObject({ kind: 'image' })
    // Code elements and elements without classes are never samples.
    expect(list.some((s) => s.tag === 'script' || s.sig === 'div')).toBe(false)
    // Buttons first, then headings, cards…
    expect(list[0].kind).toBe('button')
    expect(list.findIndex((s) => s.kind === 'heading')).toBeLessThan(list.findIndex((s) => s.kind === 'card'))
  })
  it('drops what occurs less often than the minimum', () => {
    expect(mineSignatures(sources, { min: 4 })).toEqual([])
    expect(mineSignatures([{ html: btn(1) + btn(2) }]).length).toBe(0)
  })
  it('a sample inserts as a copy with fresh ids and the sample text kept', () => {
    const s = mineSignatures(sources).find((x) => x.kind === 'card')!
    const r = insert(page({ s: SECTION }), { sec: 's', parent: '5', index: 0 }, s.html)!
    expect(r.copies!.from).toEqual(['c1', 'i1', 'h1', 'p1'])
    expect(r.html.s).toContain('<article class="card rounded-2xl p-6" data-wbc="x1"><img class="h-10 w-10" src="/i1.svg" alt="" data-wbc="x2"><h3 class="text-lg font-semibold" data-wbc="x3">Service 1</h3>')
  })
  it('signatures, class counts and the cache key', () => {
    const t = parse('<p class=" b  a b">x</p><p>y</p><svg class="i"><path class="i"/></svg>')
    expect(signature(t.all[0])).toBe('p a b')
    expect(signature(t.all[1])).toBeNull()
    expect(signature(t.all[2])).toBeNull()
    expect(signature(t.all[3])).toBeNull()
    expect(countClass(sources, 'rounded-2xl')).toEqual({ elements: 3, pages: 2 })
    expect(countClass(sources, 'rounded')).toEqual({ elements: 0, pages: 0 })
    expect(sourcesHash(sources)).toBe(sourcesHash(sources.map((s) => ({ ...s }))))
    expect(sourcesHash(sources)).not.toBe(sourcesHash(sources.slice(1)))
  })
})

describe('tag change', () => {
  it('changes the open and close tag, keeping attributes and children', () => {
    const p = page({ s: '<div data-wbc="1"><h2 class="t" id="x" data-wbc="2">Hi <em>you</em></h2></div>' })
    const r = setTag(p, '2', 'h3')!
    expect(r.html.s).toBe('<div data-wbc="1"><h3 class="t" id="x" data-wbc="2">Hi <em>you</em></h3></div>')
    expect(setTag(p, '1', 'section')!.html.s).toBe('<section data-wbc="1"><h2 class="t" id="x" data-wbc="2">Hi <em>you</em></h2></section>')
  })
  it('refuses void, raw and foreign elements, and the same tag', () => {
    const p = page({ s: '<div data-wbc="1"><img data-wbc="2" src="a"><h2 data-wbc="3">x</h2><svg data-wbc="4"><g data-wbc="5"></g></svg><li data-wbc="6">no end</div>' })
    expect(setTag(p, '2', 'div')).toBeNull()
    expect(setTag(p, '3', 'img')).toBeNull()
    expect(setTag(p, '3', 'script')).toBeNull()
    expect(setTag(p, '3', 'h2')).toBeNull()
    expect(setTag(p, '4', 'div')).toBeNull()
    expect(setTag(p, '5', 'div')).toBeNull()
    expect(tagChangeOK('div', 'section')).toBe(true)
    expect(tagChoices('h2')).toContain('h3')
    expect(tagChoices('img')).toEqual([])
  })
})

describe('attributes', () => {
  it('never takes handlers, the editor id, style or class', () => {
    expect(attrNameError('onclick')).toBe('handler')
    expect(attrNameError('ONLOAD')).toBe('handler')
    expect(attrNameError('data-wbc')).toBe('reserved')
    expect(attrNameError('style')).toBe('reserved')
    expect(attrNameError('class')).toBe('reserved')
    expect(attrNameError('a b')).toBe('name')
    expect(attrNameError('aria-label')).toBeNull()
    expect(attrNameError('data-track')).toBeNull()
  })
  it('checks URLs and ids', () => {
    expect(attrError('href', 'javascript:alert(1)')).toBe('url')
    expect(attrError('href', ' JavaScript:alert(1)')).toBe('url')
    expect(attrError('href', 'java\nscript:alert(1)')).toBe('url')
    expect(attrError('href', 'data:text/html,x')).toBe('url')
    expect(attrError('src', 'data:image/png;base64,AAA', 'img')).toBeNull()
    expect(attrError('src', 'data:image/png;base64,AAA', 'iframe')).toBe('url')
    expect(attrError('srcset', 'a.png 1x, javascript:x 2x', 'img')).toBe('url')
    expect(attrError('href', '/about/')).toBeNull()
    expect(attrError('id', '9lives')).toBe('id')
    expect(attrError('id', 'hero-title')).toBeNull()
    expect(attrError('title', 'x'.repeat(5000))).toBe('length')
    expect(urlOK('https://example.com')).toBe(true)
  })
})

describe('icons and hand-written HTML', () => {
  it('replaces an svg, keeping its id and classes', () => {
    const p = page({ s: '<a data-wbc="1"><svg class="h-6 w-6 text-teal-600" viewBox="0 0 24 24" data-wbc="2"><path d="M0 0"/></svg> Go</a>' })
    const r = replaceOuter(p, '2', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" class="lucide" data-wbc="zz"><circle r="4"/></svg>')!
    expect(r.html.s).toBe('<a data-wbc="1"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" data-wbc="2" class="h-6 w-6 text-teal-600"><circle r="4"/></svg> Go</a>')
    expect(replaceOuter(p, '2', '<svg></svg><svg></svg>')).toBeNull()
  })
  it('gives new elements ids', () => {
    expect(withIds('<div><p data-wbc="7">a</p><svg><path/></svg><script>x</script></div>')).toBe('<div data-wbc="x1"><p data-wbc="7">a</p><svg data-wbc="x2"><path/></svg><script>x</script></div>')
  })
  it('locates elements after an insert', () => {
    const p = page({ s: SECTION })
    const r = insert(p, { sec: 's', parent: null, index: 1 }, paletteItem('heading')!.html)!
    const next = { ...p, html: { ...p.html, ...r.html } }
    expect(attr(locate(next, 'x1')!.el, 'data-wbc')).toBe('x1')
  })
})

/* ---------- Kit overrides ---------- */

const here = path.dirname(fileURLToPath(import.meta.url))
const FIX = path.join(here, '..', '..', '..', 'internal', 'webbuilder', 'testdata', 'override')

describe('overrideCSS = Go KitOverrideCSS', () => {
  const files = fs.readdirSync(FIX).filter((f) => f.endsWith('.json'))
  it('has the shared fixtures', () => expect(files.length).toBeGreaterThanOrEqual(2))
  for (const f of files) {
    it(f, () => {
      const fx = JSON.parse(fs.readFileSync(path.join(FIX, f), 'utf8'))
      expect(overrideCSS(asOverride(fx.override))).toBe(fs.readFileSync(path.join(FIX, f.replace(/\.json$/, '.css')), 'utf8'))
    })
  }
})

describe('override edits', () => {
  it('sets, merges and prunes declarations per selector and context', () => {
    let o = setRuleDecls(undefined, ['@layer utilities'], '.rounded-2xl', { 'border-radius': '2rem' })
    o = setRuleDecls(o, ['@layer theme'], ':root, :host', { '--color-brand-600': '#c2410c' })
    o = setRuleDecls(o, ['@layer utilities'], '.rounded-2xl', { 'border-top-left-radius': '0' })
    expect(overrideCSS(o)).toBe('@layer utilities{.rounded-2xl{border-radius:2rem;border-top-left-radius:0}}\n@layer theme{:root, :host{--color-brand-600:#c2410c}}\n')
    expect(ruleOf(o, ['@layer theme'], ':root, :host')?.p['--color-brand-600']).toBe('#c2410c')
    o = setRuleDecls(o, ['@layer theme'], ':root, :host', { '--color-brand-600': null })
    expect(o.rules.length).toBe(1)
    expect(sameOverride(dropProp(o, '.rounded-2xl', 'border-radius'), setRuleDecls(undefined, ['@layer utilities'], '.rounded-2xl', { 'border-top-left-radius': '0' }))).toBe(true)
    expect(overrideCSS(setRuleDecls(o, ['@layer utilities'], '.rounded-2xl', { 'border-radius': null, 'border-top-left-radius': '' }))).toBe('')
  })
  it('device and state edits add the breakpoint and the pseudo-class', () => {
    const bps = { tablet: 1023, mobile: 639 }
    expect(editTarget(['@layer utilities'], '.btn', '', '', bps)).toEqual({ ctx: ['@layer utilities'], sel: '.btn' })
    expect(editTarget(['@layer utilities'], '.btn', 'mobile', 'hover', bps)).toEqual({ ctx: ['@layer utilities', '@media (max-width:639px)'], sel: '.btn:hover' })
  })
  it('prints only valid declarations and validates contexts and selectors like Go', () => {
    expect(overrideCSS({ rules: [{ sel: '.a', p: { color: 'red;x:y', 'background-color': 'url(javascript:1)', width: '1px' } }] })).toBe('.a{width:1px}\n')
    expect(ctxOK('@layer utilities')).toBe(true)
    expect(ctxOK('@media (max-width:1023px)')).toBe(true)
    expect(ctxOK('@import url(x)')).toBe(false)
    expect(ctxOK('@media x{')).toBe(false)
    expect(selOK('.sm\\:p-4')).toBe(true)
    expect(selOK('.a{')).toBe(false)
    expect(selOK('.a</style>')).toBe(false)
    expect(selOK(':not(.a')).toBe(false)
    expect(asOverride({ rules: [{ sel: 3 }, { sel: '.a', p: { color: 'red', x: 1 } }] })).toEqual({ rules: [{ sel: '.a', p: { color: 'red' } }] })
  })
})

describe('the design stylesheet helpers', () => {
  it('reads class names from selectors (escapes included)', () => {
    expect(classesInSelector('.sm\\:py-24 > .a.b:hover, .\\31 0x, a[href$=".pdf"]')).toEqual(['sm:py-24', 'a', 'b', '10x', 'pdf'])
    expect(splitSelectors(':root, :host')).toEqual([':root', ':host'])
    expect(splitSelectors(':is(.a, .b), .c')).toEqual([':is(.a, .b)', '.c'])
  })
  it('groups variables', () => {
    expect(varGroup('--color-brand-600', 'oklch(60% .1 200)')).toBe('color')
    expect(varGroup('--x', '#fff')).toBe('color')
    expect(varGroup('--font-sans', 'Inter, sans-serif')).toBe('font')
    expect(varGroup('--spacing', '0.25rem')).toBe('size')
    expect(varGroup('--radius-2xl', '1rem')).toBe('size')
    expect(varGroup('--ease', 'cubic-bezier(.4,0,.2,1)')).toBe('other')
    expect(hexOf('#abc')).toBe('#aabbcc')
    expect(hexOf('rgb(255, 0, 16)')).toBe('#ff0010')
    expect(hexOf('oklch(1 0 0)')).toBeNull()
  })
})
