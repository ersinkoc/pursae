// @vitest-environment jsdom
/**
 * Basic mode's panel for a code section (patch 0024): what it offers and what it does not.
 *
 *  - A code section is published as its own markup, so the section-level look settings (background
 *    color, decoration and photo, motion) print nothing for it: the static renderer replaces the
 *    section with a bare placeholder before any of that is drawn, and the server fills the
 *    placeholder with the markup alone (internal/webbuilder FillCode takes no style). Basic mode
 *    does not offer them for a code section.
 *  - No path of Basic mode shows raw HTML: a code section on a page the host does not draw (code
 *    next to builder sections) shows a note and a way to Studio instead of the HTML / Head code
 *    fields, and the Elements palette leaves out the HTML element.
 */
import { createElement, act as reactAct } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BasicPanel } from '@/builder/BasicPanel'
import { createSection, initialDoc } from '@/builder/data'
import { setElementsSeam } from '@/builder/elementsSeam'
import { renderStatic } from '@/builder/live/renderStatic'
import { BuilderCtx } from '@/builder/store'
import type { DocState, Section } from '@/builder/types'
import { handlesDoc } from '../../host/elements'
import { BASIC_HIDDEN, PALETTE, paletteItems } from '../../host/elements/palette'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const code = (id: string, html = '<section data-wbc="1"><h2 data-wbc="2">Hi</h2></section>'): Section => {
  const s = createSection('code', { html, head: '' })
  s.id = id
  s.style = { ...s.style, palette: 'dark', bg: '#112233' }
  return s
}

const docOf = (sections: Section[]): DocState => ({ ...initialDoc, sections }) as unknown as DocState

class NoObserver {
  observe() {}
  disconnect() {}
  unobserve() {}
}
;(globalThis as { IntersectionObserver?: unknown }).IntersectionObserver = NoObserver

afterEach(() => {
  setElementsSeam(null as never)
})

describe('section-level looks of a code section', () => {
  it('are not published: the static page holds a bare placeholder, whatever the section style says', () => {
    const s = code('c1')
    s.bg = { type: 'image', image: 'https://x.test/a.jpg', decor: 'dots', decorOpacity: 30 } as never
    s.style = { ...s.style, motion: { entrance: 'fade-up' } } as never
    const out = renderStatic(docOf([s]), { postId: 1, formAction: () => '', siteUrl: 'https://x.test' })
    expect(out.html).toContain('<div data-wb-code="c1"></div>')
    expect(out.html).not.toContain('#112233')
    expect(out.html).not.toContain('data-wb-motion')
    expect(out.html).not.toContain('dots')
    expect(out.html).not.toContain('a.jpg')
    expect(out.features).not.toContain('motion')
  })
})

function panel(sections: Section[], seam: boolean) {
  const doc = docOf(sections)
  if (seam) setElementsSeam({ handles: handlesDoc, Basic: () => createElement('div', { 'data-host-basic': '' }), Canvas: () => null, Nav: () => null, Tab: () => null as never, Content: () => null as never } as never)
  const b = {
    doc,
    basicView: 'editor',
    selection: { sectionId: sections[0].id },
    focusReq: null,
    milestones: { dismissed: true },
    setMode: vi.fn(),
    setBasicView: vi.fn(),
    setHoverSection: vi.fn(),
    select: vi.fn(),
    dispatch: vi.fn(),
    saved: [],
  }
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  reactAct(() => root.render(createElement(BuilderCtx.Provider, { value: b as never }, createElement(BasicPanel))))
  return { host, b, text: () => host.textContent ?? '', unmount: () => reactAct(() => root.unmount()) }
}

describe("Basic mode's editor of a code section", () => {
  it('on a page the host draws: its blocks, and none of the unpublished look settings (Section actions stay)', () => {
    const p = panel([code('c1'), code('c2')], true)
    expect(p.host.querySelector('[data-host-basic]')).toBeTruthy()
    for (const w of ['Background color', 'Background decoration', 'Background photo', 'Fade in on scroll', 'Arka plan rengi', 'Arka plan süsü', 'Motion', 'Hareket']) expect(p.text()).not.toContain(w)
    // The section actions (move, duplicate, hide, delete) are still there.
    expect(p.host.querySelectorAll('section button').length).toBeGreaterThanOrEqual(5)
    p.unmount()
  })

  it('on a mixed page: a note and a way to Studio, no HTML / Head code fields, no look settings', () => {
    const p = panel([code('c1'), createSection('hero')], true)
    expect(p.host.querySelector('[data-host-basic]')).toBeNull()
    expect(p.host.querySelector('textarea')).toBeNull()
    expect(p.host.querySelector('[data-field="html"], [data-field="head"]')).toBeNull()
    for (const w of ['Background color', 'Background decoration', 'Arka plan rengi']) expect(p.text()).not.toContain(w)
    const studio = Array.from(p.host.querySelectorAll('button')).find((x) => /Studio/.test(x.textContent ?? ''))
    expect(studio).toBeTruthy()
    reactAct(() => studio!.click())
    expect(p.b.setMode).toHaveBeenCalledWith('studio')
    p.unmount()
  })

  it('a builder section keeps its look settings', () => {
    const p = panel([createSection('hero'), code('c1')], false)
    expect(p.text()).toMatch(/Background color|Arka plan/)
    p.unmount()
  })
})

describe('the Elements palette in Basic mode', () => {
  it('leaves out the raw HTML element, Studio keeps it', () => {
    expect(BASIC_HIDDEN.has('html')).toBe(true)
    const basic = (['layout', 'basic', 'media', 'form'] as const).flatMap((g) => paletteItems(g, 'basic'))
    const studio = (['layout', 'basic', 'media', 'form'] as const).flatMap((g) => paletteItems(g, 'studio'))
    expect(basic.map((p) => p.id)).not.toContain('html')
    expect(studio.map((p) => p.id)).toContain('html')
    expect(basic.length).toBe(PALETTE.length - 1)
  })
})

