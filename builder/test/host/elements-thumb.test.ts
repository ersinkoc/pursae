// @vitest-environment jsdom
/**
 * Basic mode's section list thumbnails of code sections (thumb.ts, SectionThumb.tsx): a small,
 * scaled, static snapshot of the section — never running a script —, built lazily (only for rows
 * near the viewport) and cached by the section's markup hash; the kind icon until then.
 */
import { createElement, act as reactAct } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { BuilderCtx } from '@/builder/store'
import type { Section } from '@/builder/types'
import { SectionThumb } from '../../host/elements/SectionThumb'
import { SECTION_THUMB_W, THUMB_SANDBOX, sectionThumbDoc, thumbCache, thumbDoc, thumbKey, thumbStats } from '../../host/elements/thumb'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const EVIL = `<section data-wbc="1" class="hero" onclick="steal()"><h1 data-wbc="2">Hi</h1><script data-wbc="3">window.pwned=1</script><a data-wbc="4" href="javascript:alert(1)" onmouseover="x()">go</a><img data-wbc="5" src=x onerror="steal()"><iframe data-wbc="6" src="https://evil.test/" srcdoc="<script>1</script>"></iframe></section>`

const sec = (html: string, extra: Partial<Section['content']> = {}, id = 's1'): Section => ({ id, kind: 'code', name: 'S', hidden: false, items: [], style: {}, content: { html, head: '<link rel="stylesheet" href="/d.css"><script src="/d.js"></script>', body: 'dark', ...extra } }) as unknown as Section

beforeEach(() => {
  thumbCache.clear()
  thumbStats.built = 0
})

describe('the thumbnail document', () => {
  const doc = sectionThumbDoc(sec(EVIL), '', 'https://x.test/')!

  it('keeps the design look: its stylesheet and body class, scaled from a desktop width', () => {
    expect(doc).toContain('<link rel="stylesheet" href="/d.css">')
    expect(doc).toContain('<body class="dark">')
    expect(doc).toContain(`width:${SECTION_THUMB_W}px`)
  })

  it('never runs anything: no live script, no handler, no javascript: address, no nested page', () => {
    // The head script and the section script are inert, never type=text/javascript or src-bearing.
    const d = new DOMParser().parseFromString(doc, 'text/html')
    for (const s of Array.from(d.querySelectorAll('script'))) {
      expect(s.getAttribute('type')).toBe('text/x-wb-inert')
      expect(s.hasAttribute('src')).toBe(false)
      expect(s.textContent).toBe('')
    }
    expect(doc).not.toMatch(/\son[a-z]+\s*=/i)
    expect(doc).not.toContain('javascript:')
    expect(doc).not.toContain('steal')
    expect(doc).not.toContain('evil.test')
    expect(doc).not.toContain('srcdoc')
    expect(doc).toContain("script-src 'none'")
  })

  it('the frame is sandboxed without scripts', () => {
    expect(THUMB_SANDBOX).toBe('allow-same-origin')
    expect(THUMB_SANDBOX).not.toMatch(/allow-scripts|allow-top-navigation|allow-popups|allow-forms/)
  })

  it('has nothing to show for a section without markup', () => {
    expect(sectionThumbDoc(sec('   '), '', '/')).toBeNull()
  })
})

describe('the cache', () => {
  it('builds a document once per markup hash; changing the markup, head, body or styles builds another', () => {
    const a = sec('<p data-wbc="1">One</p>')
    const d1 = sectionThumbDoc(a, '', '/')
    expect(sectionThumbDoc(sec('<p data-wbc="1">One</p>', {}, 'other-id'), '', '/')).toBe(d1)
    expect(thumbStats.built).toBe(1)
    sectionThumbDoc(sec('<p data-wbc="1">Two</p>'), '', '/')
    sectionThumbDoc(sec('<p data-wbc="1">One</p>', { head: '' }), '', '/')
    sectionThumbDoc(sec('<p data-wbc="1">One</p>', { body: 'light' }), '', '/')
    sectionThumbDoc(a, ':root{--x:1}', '/')
    expect(thumbStats.built).toBe(5)
    expect(thumbKey(a, '')).not.toBe(thumbKey(sec('<p data-wbc="1">Two</p>'), ''))
  })

  it('is bounded', () => {
    for (let i = 0; i < 260; i++) sectionThumbDoc(sec(`<p data-wbc="1">n${i}</p>`), '', '/')
    expect(thumbCache.size).toBeLessThanOrEqual(200)
  })
})

describe('the palette sample document (shared builder) keeps its layout and gets the same guards', () => {
  it('pads and shrinks a sample to its content, neutralised', () => {
    const d = thumbDoc({ head: '', body: '', markup: '<a href="javascript:x()" onclick="y()">b</a>', base: '/', width: 640, fit: 'content' })
    expect(d).toContain('padding:12px;width:max-content;max-width:616px')
    expect(d).not.toContain('onclick')
    expect(d).not.toContain('javascript:')
  })
})

/* ---------- The row component: lazy, fallback ---------- */

describe('SectionThumb', () => {
  let observers: { cb: (l: { isIntersecting: boolean }[]) => void; el?: Element; disconnected: boolean }[] = []
  class IO {
    rec: (typeof observers)[number]
    constructor(cb: (l: { isIntersecting: boolean }[]) => void) {
      this.rec = { cb, disconnected: false }
      observers.push(this.rec)
    }
    observe(el: Element) {
      this.rec.el = el
    }
    disconnect() {
      this.rec.disconnected = true
    }
  }
  beforeEach(() => {
    observers = []
    ;(globalThis as { IntersectionObserver?: unknown }).IntersectionObserver = IO
  })
  afterEach(() => {
    delete (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver
  })

  function mount(s: Section) {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    const b = { doc: { sections: [s] } }
    reactAct(() => root.render(createElement(BuilderCtx.Provider, { value: b as never }, createElement(SectionThumb, { s, size: 38, fallback: createElement('i', { 'data-fallback': '' }) }))))
    return { host, unmount: () => reactAct(() => root.unmount()) }
  }

  it('shows the kind icon only and builds nothing until its row is near the viewport', () => {
    const m = mount(sec('<p data-wbc="1">One</p>'))
    expect(m.host.querySelector('[data-fallback]')).toBeTruthy()
    expect(m.host.querySelector('iframe')).toBeNull()
    expect(thumbStats.built).toBe(0)
    reactAct(() => observers[0].cb([{ isIntersecting: false }]))
    expect(m.host.querySelector('iframe')).toBeNull()
    reactAct(() => observers[0].cb([{ isIntersecting: true }]))
    const f = m.host.querySelector('iframe')!
    expect(f).toBeTruthy()
    expect(f.getAttribute('sandbox')).toBe(THUMB_SANDBOX)
    expect(f.getAttribute('sandbox')).not.toContain('allow-scripts')
    expect(f.getAttribute('srcdoc')).toContain('One')
    expect(f.getAttribute('aria-hidden')).toBe('true')
    // The kind icon stays under it until the snapshot has loaded.
    expect(m.host.querySelector('[data-fallback]')).toBeTruthy()
    expect(thumbStats.built).toBe(1)
    expect(observers[0].disconnected).toBe(true)
    m.unmount()
  })

  it('a second row with the same markup is served from the cache', () => {
    const a = mount(sec('<p data-wbc="1">Same</p>', {}, 'a'))
    reactAct(() => observers[0].cb([{ isIntersecting: true }]))
    const b = mount(sec('<p data-wbc="1">Same</p>', {}, 'b'))
    reactAct(() => observers[1].cb([{ isIntersecting: true }]))
    expect(thumbStats.built).toBe(1)
    expect(b.host.querySelector('iframe')).toBeTruthy()
    a.unmount()
    b.unmount()
  })

  it('a section without markup keeps the icon, even when visible', () => {
    const m = mount(sec(''))
    reactAct(() => observers[0].cb([{ isIntersecting: true }]))
    expect(m.host.querySelector('iframe')).toBeNull()
    expect(m.host.querySelector('[data-fallback]')).toBeTruthy()
    m.unmount()
  })

  it('without IntersectionObserver it stays on the icon (nothing is built blindly)', () => {
    delete (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver
    const m = mount(sec('<p data-wbc="1">One</p>'))
    expect(m.host.querySelector('iframe')).toBeNull()
    expect(thumbStats.built).toBe(0)
    m.unmount()
  })
})

