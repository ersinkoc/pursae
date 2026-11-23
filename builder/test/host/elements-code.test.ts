// @vitest-environment jsdom
/**
 * Element editor "Edit code" (elements/codeops.ts, CodeEditor.tsx, codehl.ts): the selected
 * element's own markup without the editor's data-wbc ids, spliced back byte for byte at its range
 * with the ids restored, refused when it is not balanced; and the code editor's keyboard handling.
 */
import { createElement, act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { checkMarkup, applyCode, codeView, withCodeIds } from '../../host/elements/codeops'
import { highlight } from '../../host/elements/codehl'
import { parse, wbc } from '../../host/elements/html'
import { locate, setIdSource, type Page } from '../../host/elements/ops'
import { BIG, CodeEditor } from '../../host/elements/CodeEditor'

let n = 0
beforeEach(() => {
  n = 0
  setIdSource(() => `x${++n}`)
})

const page = (html: Record<string, string>): Page => ({ order: Object.keys(html), html })

const CARDS = `<section data-wbc="1" class="a">
  <div class="grid" data-wbc="2">
    <a class="card" href="/a/" data-wbc="3"><span data-wbc="4">A</span></a>
    <a class="card" href='/b/?x=1&amp;y=2' title='say "hi" &amp; go' data-wbc="5"><span data-wbc="6">B &copy; C</span></a>
  </div>
  <p data-wbc="7">tail</p>
</section>`

/** Opens element `id` of a one-section page; returns its view and the apply function. */
function open(src: string, id: string, part: 'outer' | 'inner' = 'outer') {
  const p = page({ s1: src })
  const at = locate(p, id)!
  const view = codeView(at.tree, at.el, part)
  return { p, at, view, apply: (text: string) => applyCode(p, id, text, view.slots, part) }
}

describe('Edit code: the view', () => {
  it('hides the data-wbc ids of the element and everything in it', () => {
    const { view } = open(CARDS, '2')
    expect(view.text).not.toContain('data-wbc')
    expect(view.text.startsWith('<div class="grid">')).toBe(true)
    expect(view.text).toContain(`href='/b/?x=1&amp;y=2' title='say "hi" &amp; go'`)
    expect(view.slots.map((s) => s.id)).toEqual(['2', '3', '4', '5', '6'])
  })

  it('applying the shown text unchanged gives back the stored bytes', () => {
    for (const id of ['1', '2', '3', '5', '7']) {
      const { p, view, apply } = open(CARDS, id)
      const r = apply(view.text)
      expect(r.error).toBeUndefined()
      expect(r.result!.html).toEqual({})
      // ... and through the id step itself:
      const made = withCodeIds(view.text, view.slots, new Set())
      expect(made.html).toBe(p.html.s1.slice(locate(p, id)!.el.start, locate(p, id)!.el.end))
    }
  })
})

describe('Edit code: apply', () => {
  it('changes only the element: bytes outside its range stay, ids of matching elements stay', () => {
    const { p, at, view, apply } = open(CARDS, '3')
    const typed = view.text.replace('class="card"', 'class="card big"').replace('>A<', '>Alpha<')
    const r = apply(typed)
    expect(r.error).toBeUndefined()
    const next = r.result!.html.s1
    expect(next.slice(0, at.el.start)).toBe(p.html.s1.slice(0, at.el.start))
    expect(next.slice(next.length - (p.html.s1.length - at.el.end))).toBe(p.html.s1.slice(at.el.end))
    expect(next).toContain('<a class="card big" href="/a/" data-wbc="3"><span data-wbc="4">Alpha</span></a>')
    expect(r.result!.select).toBe('3')
  })

  it('keeps the other elements byte for byte, odd quoting and entities included', () => {
    const { p, view, apply } = open(CARDS, '2')
    const typed = view.text.replace('>A<', '>Z<')
    const next = apply(typed).result!.html.s1
    expect(next).toBe(p.html.s1.replace('>A<', '>Z<'))
    expect(next).toContain(`href='/b/?x=1&amp;y=2' title='say "hi" &amp; go' data-wbc="5"`)
    expect(next).toContain('B &copy; C')
  })

  it('gives new elements fresh ids and an id typed by the user is kept only when unused', () => {
    const { view, apply } = open(CARDS, '4')
    const r = apply('<b>one</b> <i data-wbc="2">two</i> <em data-wbc="mine">three</em>')
    expect(r.error).toBeUndefined()
    const t = parse(r.result!.html.s1)
    // <b> matched nothing of the old (a span), so it is new; "2" is used elsewhere on the page.
    expect(t.byId.has('mine')).toBe(true)
    expect([...t.byId.keys()].filter((k) => k === '2')).toHaveLength(1)
    expect(t.byId.get('2')!.tag).toBe('div')
    expect(new Set(t.all.map((e) => wbc(e)).filter(Boolean)).size).toBe(t.all.filter((e) => wbc(e)).length)
    expect(view.text).toBe('<span>A</span>')
  })

  it('an element typed in front shifts no ids', () => {
    const { view, apply } = open(CARDS, '2')
    const typed = view.text.replace('<a class="card" href="/a/">', '<h2>new</h2>\n    <a class="card" href="/a/">')
    const t = parse(apply(typed).result!.html.s1)
    expect(t.byId.get('3')!.tag).toBe('a')
    expect(t.byId.get('5')!.tag).toBe('a')
    expect(t.byId.get('2')!.tag).toBe('div')
    const h2 = t.all.find((e) => e.tag === 'h2')!
    expect(wbc(h2)).toBe('x1')
  })

  it('allows several top-level nodes and selects the first new root', () => {
    const { p, view, apply } = open(CARDS, '7')
    expect(view.text).toBe('<p>tail</p>')
    const r = apply('<p>one</p>\n<p>two</p>')
    expect(r.error).toBeUndefined()
    const t = parse(r.result!.html.s1)
    const ps = t.root.children[0] && (t.root.children[0] as { children: unknown[] }).children
    expect(ps).toBeTruthy()
    expect(r.result!.select).toBe('7')
    expect(r.result!.html.s1).toContain('<p data-wbc="7">one</p>\n<p data-wbc="x1">two</p>')
    expect(r.result!.html.s1.startsWith(p.html.s1.slice(0, locate(p, '7')!.el.start))).toBe(true)
  })

  it('selects the first new root when the old id cannot be reused', () => {
    const { view, apply } = open(CARDS, '7')
    const r = apply('<aside>x</aside><p>y</p>')
    expect(view.slots[0].tag).toBe('p')
    expect(r.result!.select).toBe('x1')
  })

  it('asks to delete when the code is empty', () => {
    const { apply } = open(CARDS, '7')
    expect(apply('  \n ').error).toBe('empty')
    expect(apply('').result).toBeUndefined()
  })

  it('refuses markup that is not balanced and applies nothing', () => {
    const { apply } = open(CARDS, '7')
    expect(apply('<div><span>x</div>').error).toBe('unclosed')
    expect(apply('<div>x').error).toBe('unclosed')
    expect(apply('<p>x</p></div>').error).toBe('stray')
    expect(apply('<p class="a>x</p>').error).toBeTruthy()
    expect(apply('<p>x</p><!-- open').error).toBe('unterminated')
    expect(apply('<script>var a = 1').error).toBe('unclosed')
    const r = apply('<p>x</p></section>')
    expect(r.error).toBe('stray')
    expect(r.problem!.line).toBe(1)
    expect(r.result).toBeUndefined()
  })

  it('does not let the code change what is around the element', () => {
    // A <li> typed into a <p> is balanced by itself and is fine; implied ends must not swallow siblings.
    const src = `<ul data-wbc="1"><li data-wbc="2">a</li><li data-wbc="3">b</li></ul>`
    const { apply } = open(src, '2')
    const r = apply('<li>a<li>c')
    expect(r.error).toBeUndefined()
    const t = parse(r.result!.html.s1)
    expect(t.byId.get('3')!.parent!.tag).toBe('ul')
  })

  it('handles void elements, an svg, script and style', () => {
    const src = `<div data-wbc="1"><img src="a.png" alt="" data-wbc="2"><br data-wbc="3"><svg viewBox="0 0 2 2" data-wbc="4"><path d="M0 0L2 2"/></svg><style>.a > b { color: red }</style><script>if (a < b && c > d) document.write('</div>')</script></div>`
    const { view, apply } = open(src, '1')
    expect(view.text).toBe(`<div><img src="a.png" alt=""><br><svg viewBox="0 0 2 2"><path d="M0 0L2 2"/></svg><style>.a > b { color: red }</style><script>if (a < b && c > d) document.write('</div>')</script></div>`)
    expect(apply(view.text).result!.html).toEqual({})
    const typed = view.text.replace('alt=""', 'alt="x"').replace('M0 0L2 2', 'M0 0L9 9').replace('color: red', 'color: blue')
    const r = apply(typed)
    expect(r.error).toBeUndefined()
    const out = r.result!.html.s1
    expect(out).toBe(src.replace('alt=""', 'alt="x"').replace('M0 0L2 2', 'M0 0L9 9').replace('color: red', 'color: blue'))
    // svg children and scripts / styles never get ids
    const t = parse(out)
    expect(t.all.filter((e) => e.tag === 'path' || e.tag === 'script' || e.tag === 'style').every((e) => wbc(e) === null)).toBe(true)
  })

  it('a typed svg replaces the element; its inside gets no ids', () => {
    const src = `<div data-wbc="1"><svg data-wbc="2"><g><path d="M0"/></g></svg></div>`
    const { view, apply } = open(src, '2')
    expect(view.text).toBe('<svg><g><path d="M0"/></g></svg>')
    const out = apply('<svg width="4"><g><circle r="1"/></g></svg>').result!.html.s1
    expect(out).toBe(`<div data-wbc="1"><svg data-wbc="2" width="4"><g><circle r="1"/></g></svg></div>`)
  })

  it('puts a data-wbc back into the slot it had (first, middle or last attribute)', () => {
    const src = `<div data-wbc="1"><a data-wbc="2" href="/x" class="k">x</a><a href="/y" data-wbc="3" class="k">y</a><a href="/z" class="k" data-wbc="4">z</a></div>`
    const { view, apply } = open(src, '1')
    expect(view.text).toBe('<div><a href="/x" class="k">x</a><a href="/y" class="k">y</a><a href="/z" class="k">z</a></div>')
    expect(apply(view.text.replace('>x<', '>X<')).result!.html.s1).toBe(src.replace('>x<', '>X<'))
  })

  it('works on the inside only (Inside tab) and keeps raw text elements as text', () => {
    const src = `<div data-wbc="1"><p data-wbc="2">a</p></div><script data-wbc="9">let a = "<b>"</script>`
    const o = open(src, '1', 'inner')
    expect(o.view.text).toBe('<p>a</p>')
    expect(o.apply('<p>a</p><p>b</p>').result!.html.s1).toBe(`<div data-wbc="1"><p data-wbc="2">a</p><p data-wbc="x1">b</p></div><script data-wbc="9">let a = "<b>"</script>`)
    expect(o.apply('').result!.html.s1).toBe(`<div data-wbc="1"></div><script data-wbc="9">let a = "<b>"</script>`)
    const s = open(src, '9', 'inner')
    expect(s.view.text).toBe('let a = "<b>"')
    expect(s.apply('let a = 1 < 2').result!.html.s1).toBe(src.replace('"<b>"', '1 < 2'))
    expect(s.apply('x</script><p>').error).toBe('around')
    expect(o.apply('<div>a').error).toBe('unclosed')
  })

  it('works across sections: other sections are not touched', () => {
    const p = page({ s1: `<div data-wbc="1">a</div>`, s2: `<div data-wbc="2">b</div>` })
    const at = locate(p, '1')!
    const view = codeView(at.tree, at.el)
    const r = applyCode(p, '1', '<div data-wbc="2">z</div>', view.slots)
    expect(Object.keys(r.result!.html)).toEqual(['s1'])
    // "2" lives in the other section: the typed id is replaced, not duplicated
    expect(r.result!.html.s1).not.toContain('data-wbc="2"')
  })
})

describe('checkMarkup', () => {
  it('accepts well formed markup, comments, entities and optional end tags', () => {
    for (const ok of ['<div>x</div>', '<p>a<p>b', '<ul><li>a<li>b</ul>', '<img src=x>', '<br/>', '<!-- c --><div></div>', 'text &amp; text', '<svg><path d="M0"/></svg>', '<table><tr><td>a<td>b</table>', '<script>if (a<b) {}</script>', '<input disabled>']) {
      expect(checkMarkup(ok), ok).toBeNull()
    }
  })
  it('reports the first problem with its line', () => {
    expect(checkMarkup('<div>\n<span>\n</div>')).toMatchObject({ kind: 'unclosed', tag: 'span', line: 2 })
    expect(checkMarkup('a\n\n</b>')).toMatchObject({ kind: 'stray', tag: 'b', line: 3 })
  })
})

describe('codehl', () => {
  it('colours tags, attributes, strings, comments, entities, style and script', () => {
    const src = `<!-- c --><a href="/x" hidden>&amp;</a><style>.a{color:#fff;margin:0 auto}</style><script>const a = "s" // c\n</script>`
    const t = highlight(src, 'html')
    const of = (c: string) => t.filter((x) => x.c === c).map((x) => src.slice(x.s, x.e))
    expect(of('com')).toContain('<!-- c -->')
    expect(of('tag')).toContain('<a')
    expect(of('attr')).toEqual(expect.arrayContaining(['href', 'hidden', 'color', 'margin']))
    expect(of('str')).toEqual(expect.arrayContaining(['"/x"', '"s"']))
    expect(of('ent')).toEqual(['&amp;'])
    expect(of('kw')).toContain('const')
    expect(of('num')).toEqual(expect.arrayContaining(['#fff']))
    for (let i = 1; i < t.length; i++) expect(t[i].s).toBeGreaterThanOrEqual(t[i - 1].e)
  })
  it('never throws on rubbish and stays ordered', () => {
    for (const s of ['<', '<<<', '<a', '<a b="', '<!--', '<style>a{', '<script>"', '&', '&#', '</', '<a /b=c>', '<a =>']) {
      const t = highlight(s, 'html')
      for (let i = 1; i < t.length; i++) expect(t[i].s).toBeGreaterThanOrEqual(t[i - 1].e)
    }
  })
})

/* ---------- The editor component ---------- */

let root: Root
let host: HTMLDivElement
const g = globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT?: boolean }
beforeEach(() => {
  g.IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.useRealTimers()
})

function Harness(p: { initial: string; readOnly?: boolean; onApply?: () => void; onEscape?: () => void; out: { v: string } }) {
  const [v, setV] = useState(p.initial)
  p.out.v = v
  return createElement(CodeEditor, { value: v, onChange: setV, label: 'Code', readOnly: p.readOnly, onApply: p.onApply, onEscape: p.onEscape })
}
const mount = (p: Omit<Parameters<typeof Harness>[0], 'out'>) => {
  const out = { v: p.initial }
  act(() => root.render(createElement(Harness, { ...p, out })))
  return { out, ta: host.querySelector('textarea') as HTMLTextAreaElement }
}
const press = (ta: HTMLTextAreaElement, key: string, mods: KeyboardEventInit = {}) => {
  const ev = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...mods })
  act(() => {
    ta.dispatchEvent(ev)
  })
  return ev
}
const sel = (ta: HTMLTextAreaElement, s: number, e = s) => ta.setSelectionRange(s, e)

describe('CodeEditor keyboard', () => {
  it('Tab inserts two spaces and keeps the caret after them', () => {
    const { out, ta } = mount({ initial: 'ab' })
    ta.focus()
    sel(ta, 1)
    const ev = press(ta, 'Tab')
    expect(ev.defaultPrevented).toBe(true)
    expect(out.v).toBe('a  b')
    expect(ta.selectionStart).toBe(3)
  })

  it('Tab over several lines indents them all; Shift+Tab outdents', () => {
    const { out, ta } = mount({ initial: 'a\nb\n\nc' })
    sel(ta, 0, 4)
    press(ta, 'Tab')
    expect(out.v).toBe('  a\n  b\n\nc')
    expect(ta.selectionStart).toBe(2)
    sel(ta, 0, out.v.length)
    press(ta, 'Tab', { shiftKey: true })
    expect(out.v).toBe('a\nb\n\nc')
  })

  it('Shift+Tab takes one space off a line indented by one', () => {
    const { out, ta } = mount({ initial: ' x' })
    sel(ta, 2)
    press(ta, 'Tab', { shiftKey: true })
    expect(out.v).toBe('x')
    expect(ta.selectionStart).toBe(1)
  })

  it('Escape leaves the field, so keyboard users are not trapped', () => {
    const onEscape = vi.fn()
    const { ta } = mount({ initial: 'x', onEscape })
    ta.focus()
    expect(document.activeElement).toBe(ta)
    const ev = press(ta, 'Escape')
    expect(ev.defaultPrevented).toBe(true)
    expect(document.activeElement).not.toBe(ta)
    expect(onEscape).toHaveBeenCalledTimes(1)
  })

  it('Tab is not trapped in a read-only field and nothing changes there', () => {
    const { out, ta } = mount({ initial: 'ab', readOnly: true })
    const ev = press(ta, 'Tab')
    expect(ev.defaultPrevented).toBe(false)
    expect(out.v).toBe('ab')
    expect(ta.readOnly).toBe(true)
  })

  it('Ctrl/⌘+Enter applies; Enter keeps the indentation', () => {
    const onApply = vi.fn()
    const { out, ta } = mount({ initial: '  <p>', onApply })
    press(ta, 'Enter', { ctrlKey: true })
    press(ta, 'Enter', { metaKey: true })
    expect(onApply).toHaveBeenCalledTimes(2)
    sel(ta, 5)
    press(ta, 'Enter')
    expect(out.v).toBe('  <p>\n  ')
    expect(ta.selectionStart).toBe(8)
  })

  it('keeps keys away from the builder (Delete, arrows, Ctrl+Z, Ctrl+F)', () => {
    const { ta } = mount({ initial: 'x' })
    const seen = vi.fn()
    document.addEventListener('keydown', seen)
    for (const k of ['Delete', 'Backspace', 'ArrowLeft', 'z', 'a', 'Tab', 'Escape']) press(ta, k, k === 'z' ? { ctrlKey: true } : {})
    press(ta, 'f', { ctrlKey: true })
    expect(seen).not.toHaveBeenCalled()
    expect(host.querySelector('[data-wb-code-find]')).toBeTruthy()
  })

  it('find selects the next match and counts them', () => {
    const { ta } = mount({ initial: 'foo bar foo\nfoo' })
    press(ta, 'f', { metaKey: true })
    const input = host.querySelector('[data-wb-code-find] input') as HTMLInputElement
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    act(() => {
      set.call(input, 'foo')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(host.querySelector('[data-wb-code-find]')!.textContent).toContain('3')
    press(input as unknown as HTMLTextAreaElement, 'Enter')
    expect([ta.selectionStart, ta.selectionEnd]).toEqual([0, 3])
    press(input as unknown as HTMLTextAreaElement, 'Enter')
    expect(ta.selectionStart).toBe(8)
  })

  it('shows the larger editor as a dialog and closes it with Escape', () => {
    const { ta } = mount({ initial: '<p>x</p>' })
    expect(ta).toBeTruthy()
    act(() => (host.querySelector('[data-wb-code-popout]') as HTMLButtonElement).click())
    const dlg = document.body.querySelector('[role="dialog"]')!
    expect(dlg.getAttribute('aria-modal')).toBe('true')
    expect((dlg as HTMLElement).style.width).toBe('80vw')
    const inner = dlg.querySelector('textarea')!
    expect(inner.value).toBe('<p>x</p>')
    press(inner, 'Escape')
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('what is typed is only ever text in the page (never parsed as markup)', () => {
    const evil = '<img src=x onerror="window.__pwned=1"><script>window.__pwned=1</script>'
    const { ta } = mount({ initial: evil })
    expect(ta.value).toBe(evil)
    expect(host.querySelector('img, script')).toBeNull()
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined()
    expect(host.querySelector('pre')!.textContent).toContain('onerror')
  })
})

describe('CodeEditor on big inputs', () => {
  const big = (() => {
    const row = '<div class="card" data-x="1"><span>Some text &amp; more</span></div>\n'
    let s = ''
    while (s.length < 200 * 1024) s += row
    return s
  })()

  it('shows plain text at once and colours only the lines near the viewport after a pause', () => {
    expect(big.length).toBeGreaterThan(BIG)
    vi.useFakeTimers()
    const t0 = performance.now()
    const { ta } = mount({ initial: big })
    expect(performance.now() - t0).toBeLessThan(3000)
    const pre = host.querySelector('pre[data-wb-code-paint]')!
    expect(pre.querySelectorAll('span').length).toBe(0)
    expect(ta.value.length).toBe(big.length)
    act(() => {
      vi.advanceTimersByTime(400)
    })
    const spans = pre.querySelectorAll('span').length
    expect(spans).toBeGreaterThan(0)
    expect(spans).toBeLessThan(2500)
    expect(pre.textContent).toBe(big + '\n')
  })

  it('does not colour on every keystroke', () => {
    vi.useFakeTimers()
    const { ta } = mount({ initial: big })
    act(() => {
      vi.advanceTimersByTime(400)
    })
    sel(ta, 0)
    press(ta, 'Tab')
    // right after an edit the text is plain again until typing pauses
    expect(host.querySelector('pre[data-wb-code-paint]')!.querySelectorAll('span').length).toBe(0)
    act(() => {
      vi.advanceTimersByTime(400)
    })
    expect(host.querySelector('pre[data-wb-code-paint]')!.querySelectorAll('span').length).toBeGreaterThan(0)
  })
})
