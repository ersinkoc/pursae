/**
 * Basic mode's block panel (builder/host/elements/BasicCode.tsx): edits go through the element
 * actions as one labelled history entry each and change the stored markup only inside the edited
 * range; the section list row; the read-only panel of a user who may not change code sections.
 */
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { BuilderCtx } from '@/builder/store'
import type { DocState, Section } from '@/builder/types'
import { act, latest, pageOf } from '../../host/elements/actions'
import { BasicCode, basicSummary } from '../../host/elements/BasicCode'
import { extractFields, flatFields, plainInner } from '../../host/elements/fields'
import { inner, parse } from '../../host/elements/html'
import { locate, setIdSource } from '../../host/elements/ops'
import { resetElState } from '../../host/elements/state'

const HTML = `<section data-wbc="a" class="hero">
  <div data-wbc="b" class="wrap">
    <h1 data-wbc="c" class="text-5xl">
      Build <em data-wbc="c2" onclick="track()">faster</em>
    </h1>
    <p data-wbc="d" class="lead">Tom &amp; Jerry   love   <b data-wbc="d2">cheese</b>.</p>
    <h2 data-wbc="e">
      Plain heading
    </h2>
    <a data-wbc="f" href="/signup/" class="btn">Get started</a>
    <img data-wbc="g" src="/old.png" srcset="/old.png 1x, /old@2x.png 2x" sizes="100vw" alt="Old" width="10" height="10">
    <!-- keep me -->
  </div>
</section>`

const section = (html: string): Section =>
  ({ id: 's1', kind: 'code', name: 'Section 1', hidden: false, items: [], style: {}, content: { html, head: '' } }) as unknown as Section

const doc = (html: string): DocState => ({ sections: [section(html)] }) as unknown as DocState

/** A minimal builder API that records what is dispatched. */
function api(html: string) {
  const state = { doc: doc(html) }
  const dispatched: { label?: string; html: string }[] = []
  const b = {
    get doc() {
      return state.doc
    },
    dispatch: vi.fn((a: { type: string; doc: DocState; label?: string }) => {
      state.doc = a.doc
      dispatched.push({ label: a.label, html: a.doc.sections[0].content.html as string })
    }),
    select: vi.fn(),
    notify: vi.fn(),
    setMode: vi.fn(),
  }
  return { b, dispatched, html: () => state.doc.sections[0].content.html as string }
}

beforeEach(() => {
  setIdSource(() => 'n1')
  latest.doc = null
  resetElState()
})

describe('writing from the fields', () => {
  it('a plain text edit changes only the element content: one entry, "Text changed", surrounding whitespace kept', () => {
    const { b, dispatched, html } = api(HTML)
    const at = locate(pageOf(b.doc), 'e')!
    expect(act.setInner(b as never, 'e', plainInner(inner(at.tree, at.el), 'New heading'))).toBe(true)
    expect(dispatched).toHaveLength(1)
    expect(dispatched[0].label).toBe('Text changed')
    expect(html()).toBe(HTML.replace('Plain heading', 'New heading'))
  })

  it('escapes what was typed and writes nothing when the text did not change', () => {
    const { b, dispatched, html } = api(HTML)
    const at = locate(pageOf(b.doc), 'e')!
    expect(plainInner(inner(at.tree, at.el), 'Plain heading')).toBe(inner(at.tree, at.el))
    expect(act.setInner(b as never, 'e', plainInner(inner(at.tree, at.el), 'Plain heading'))).toBe(false)
    expect(dispatched).toHaveLength(0)
    act.setInner(b as never, 'e', plainInner(inner(at.tree, at.el), 'A < B & "C"'))
    expect(html()).toContain('A &lt; B &amp; "C"')
    expect(html().replace('A &lt; B &amp; "C"', 'Plain heading')).toBe(HTML)
  })

  it('reads the text of formatted elements as one line and keeps their stored tags (the rich text path)', () => {
    const f = flatFields(extractFields(HTML))
    const h1 = f.find((x) => x.id === 'c')!
    expect(h1).toMatchObject({ edit: 'rich', text: 'Build faster' })
    expect(f.find((x) => x.id === 'd')).toMatchObject({ edit: 'rich', text: 'Tom & Jerry love cheese.' })
    expect(f.find((x) => x.id === 'e')).toMatchObject({ edit: 'plain', text: 'Plain heading' })
    // Unedited elements are never rewritten: the section source is the parse's source.
    expect(parse(HTML).src).toBe(HTML)
  })

  it('a link address: "Link changed", only the attribute changes', () => {
    const { b, dispatched, html } = api(HTML)
    act.setAttrs(b as never, 'f', { href: '/pricing/' }, 'Link changed')
    expect(dispatched.map((d) => d.label)).toEqual(['Link changed'])
    expect(html()).toBe(HTML.replace('href="/signup/"', 'href="/pricing/"'))
  })

  it('alt text: "Alternative text changed"', () => {
    const { b, dispatched, html } = api(HTML)
    act.setAttrs(b as never, 'g', { alt: 'New' }, 'Alternative text changed')
    expect(dispatched[0].label).toBe('Alternative text changed')
    expect(html()).toBe(HTML.replace('alt="Old"', 'alt="New"'))
  })

  it('replacing an image drops srcset and sizes and follows the new width and height, in one entry', () => {
    const { b, dispatched, html } = api(HTML)
    act.setAttrsMany(b as never, [{ id: 'g', attrs: { src: '/new.png', srcset: null, sizes: null, width: '640', height: '480' } }], 'Image replaced', 'g')
    expect(dispatched).toHaveLength(1)
    expect(dispatched[0].label).toBe('Image replaced')
    const img = html().match(/<img[^>]*>/)![0]
    expect(img).toBe('<img data-wbc="g" src="/new.png" alt="Old" width="640" height="480">')
    // Everything outside the image tag is untouched, the comment included.
    expect(html().replace(img, '')).toBe(HTML.replace(/<img[^>]*>/, ''))
  })

  it('a form field placeholder changes through the attribute operation', () => {
    const { b, html } = api(`<form data-wbc="a"><input data-wbc="b" type="email" placeholder="Email"></form>`)
    act.setAttrs(b as never, 'b', { placeholder: 'Your email' })
    expect(html()).toBe(`<form data-wbc="a"><input data-wbc="b" type="email" placeholder="Your email"></form>`)
  })
})

describe('the section list row', () => {
  it('names the section by its first heading and counts its content', () => {
    expect(basicSummary(section(HTML))).toEqual({ title: 'Build faster', sub: '2 headings · 1 text · 1 button' })
  })

  it('keeps the section name when the markup says nothing, and counts singular and plural', () => {
    expect(basicSummary(section('<div data-wbc="a"><img data-wbc="b" src="x.png" alt=""></div>'))).toEqual({ title: 'Section 1', sub: '1 image' })
    expect(basicSummary(section(''))).toEqual({ title: 'Section 1', sub: '' })
  })

  it('shows at most three kinds, in a fixed order', () => {
    const html = `<div data-wbc="a"><h2 data-wbc="b">T</h2><p data-wbc="c">x</p><a data-wbc="d" href="/" class="btn">B</a><a data-wbc="e" href="/">L</a><img data-wbc="f" src="a.png" alt=""></div>`
    expect(basicSummary(section(html))?.sub).toBe('1 heading · 1 text · 1 button')
  })
})

describe('the panel', () => {
  const render = (html: string, readOnly?: boolean) => {
    const { b } = api(html)
    return renderToStaticMarkup(createElement(BuilderCtx.Provider, { value: b as never }, createElement(BasicCode, { s: section(html), readOnly })))
  }

  it('shows fields with inputs and no HTML or head code anywhere', () => {
    const out = render(HTML)
    expect(out).toContain('data-wb-basic-field="e"')
    expect(out).toContain('value="Plain heading"')
    expect(out).toContain('value="/signup/"')
    expect(out).toContain('Alternative text')
    expect(out).toContain('Switch to Studio')
    expect(out).not.toMatch(/HTML code|Head code|<textarea[^>]*>[^<]*&lt;/)
    expect(out).not.toContain('disabled=""')
    expect(out).not.toContain('data-wb-basic-readonly')
  })

  it('is read-only with a note for a user who may not change code sections', () => {
    const out = render(HTML, true)
    expect(out).toContain('data-wb-basic-readonly')
    expect(out).toContain('read-only')
    expect(out).toContain('disabled=""')
    expect(out).toContain('aria-disabled="true"')
    expect(out).not.toContain('data-wb-basic-replace')
    // Still looks the same, and still offers Studio.
    expect(out).toContain('value="Plain heading"')
  })

  it('shows groups of repeated cards collapsed, with the numbered title and the first heading', () => {
    const cards = `<div data-wbc="a" class="grid"><div data-wbc="b" class="card"><h3 data-wbc="c">One</h3></div><div data-wbc="d" class="card"><h3 data-wbc="e">Two</h3></div></div>`
    const out = render(cards)
    expect(out).toContain('Card 1')
    expect(out).toContain('Card 2')
    expect(out).toContain('aria-expanded="false"')
    expect(out).not.toContain('data-wb-basic-field')
  })

  it('says so when nothing is editable', () => {
    expect(render('<div data-wbc="a" class="x"></div>')).toContain('Nothing to edit here yet.')
  })

  it('offers "Show more" past 60 fields', () => {
    const many = Array.from({ length: 70 }, (_, i) => `<p data-wbc="p${i}">Line ${i}</p>`).join('')
    const out = render(many)
    expect(out).toContain('data-wb-basic-more')
    expect(out).toContain('Show 10 more')
    expect(out.match(/data-wb-basic-field=/g)).toHaveLength(60)
  })
})
