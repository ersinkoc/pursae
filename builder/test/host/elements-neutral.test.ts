// @vitest-environment jsdom
/**
 * Element editor security (review 2026-11-17, findings 1, 2, 3, 17): the static frame's copy of
 * the design is neutralised by the HTML parser (neutral.ts) — every payload of the review and a
 * broad mutation-XSS list ends up without anything that could run, through the frame document
 * (frameDoc), the reconciler's nodes (neutralNodes / neutralAttrs) and a second parse; text edits
 * write back from the source (richtext.writeBack) and the Content panel never parses markup.
 * jsdom's parser is parse5 (the WHATWG algorithm); the same payloads run in Chromium in
 * test/security/editor-xss.mjs.
 */
import { describe, expect, it } from 'vitest'
import type { Section } from '@/builder/types'
import { FrameSync, frameDoc, mergeHeads } from '../../host/elements/frame'
import { parse } from '../../host/elements/html'
import { BLOCKED_URL, INERT, neutralAttrs, neutralMarkup, neutralNodes, sweep, unsafeUrl } from '../../host/elements/neutral'
import { mapSource, writeBack } from '../../host/elements/richtext'
import { richNodes } from '../../host/elements/ContentPanel'
import { HEAD_PAYLOADS, PAYLOADS, hitJs } from '../security/payloads'

const sec = (id: string, html: string, extra: Record<string, string> = {}): Section =>
  ({ id, kind: 'code', name: id, content: { html, head: '', wrap: '', ...extra }, items: [], style: { hideOn: [] } }) as unknown as Section

/** Independent check: what in a node tree could run (a different, stricter rule set than neutral.ts). */
function dangers(root: Node): string[] {
  const out: string[] = []
  const strip = (v: string) => Array.from(v).filter((c) => c.codePointAt(0)! > 0x20 && !(c.codePointAt(0)! >= 0x7f && c.codePointAt(0)! <= 0x9f)).join('').toLowerCase()
  const walk = (n: Node) => {
    if (n.nodeType === 1) {
      const el = n as Element
      const tag = el.localName.toLowerCase()
      if (tag === 'script' && (el.getAttribute('type') !== INERT || el.hasAttribute('src') || el.hasAttribute('href') || el.textContent)) out.push(`script <${el.outerHTML.slice(0, 60)}>`)
      if (tag === 'base') out.push('base')
      if (tag === 'meta' && /refresh/i.test(el.getAttribute('http-equiv') ?? '')) out.push('meta refresh')
      if ((tag === 'object' || tag === 'embed') && (el.hasAttribute('data') || el.hasAttribute('src'))) out.push(`${tag} loads`)
      if (tag === 'iframe' && el.getAttribute('sandbox') !== '') out.push('iframe without sandbox=""')
      if (['iframe', 'frame', 'fencedframe', 'portal'].includes(tag) && el.hasAttribute('src')) out.push(`${tag} loads`)
      if (tag === 'noscript' && el.firstChild) out.push('noscript content')
      if (['animate', 'set'].includes(tag) && /href|^on/i.test(el.getAttribute('attributeName') ?? '')) out.push('animation of href / handler')
      for (const a of Array.from(el.attributes)) {
        const name = a.name.toLowerCase()
        const v = strip(a.value)
        if (name.startsWith('on') || a.localName.toLowerCase().startsWith('on')) out.push(`handler ${a.name}`)
        if (name === 'srcdoc' || name.startsWith('shadowroot') || name === 'data-wb-editor') out.push(`attr ${a.name}`)
        if (/(^|;)(javascript|vbscript):/.test(v) || /^data:(text\/html|application\/xhtml|text\/javascript|application\/javascript)/.test(v)) out.push(`url ${a.name}=${a.value.slice(0, 40)}`)
        if (/^data:image\/svg\+xml/.test(v) && !['img', 'image', 'feimage', 'input', 'source', 'video', 'link'].includes(tag)) out.push(`svg data url on ${tag}`)
      }
      const tpl = el as HTMLTemplateElement
      if (tag === 'template' && tpl.content) walk(tpl.content)
      if (el.shadowRoot) walk(el.shadowRoot)
    } else if (n.nodeType === 8 && /^\/?wb:/.test((n as Comment).data)) out.push('marker-like comment')
    for (let k = n.firstChild; k; k = k.nextSibling) walk(k)
  }
  walk(root)
  return out
}

/** The frame's view of markup: a full document parse (as the frame parses its srcdoc). */
const parsed = (html: string) => new DOMParser().parseFromString(`<!doctype html><html><head></head><body>${html}</body></html>`, 'text/html')

describe('neutraliser: every payload', () => {
  for (const [name, p] of PAYLOADS) {
    const src = p(hitJs(name))
    it(name, () => {
      const out = neutralMarkup(src)
      // nothing that runs, in the copy and in what the frame's parser makes of it
      expect(dangers(parsed(out))).toEqual([])
      // stable: parsing and neutralising the copy again gives the copy (no mutation left)
      expect(neutralMarkup(out)).toBe(out)
      // the reconciler's nodes, in a body and in table / list / select contexts
      const d = document.implementation.createHTMLDocument('')
      for (const ctx of ['div', 'tbody', 'ul', 'select', 'p']) {
        const parent = d.createElement(ctx)
        d.body.appendChild(parent)
        for (const n of neutralNodes(src, parent)) parent.appendChild(n)
        expect(dangers(parent)).toEqual([])
      }
    })
  }
  for (const [name, p] of HEAD_PAYLOADS) {
    it(name, () => {
      const out = mergeHeads([p(hitJs(name))], 'static')
      expect(dangers(parsed(out))).toEqual([])
    })
  }
})

describe('neutraliser: the frame document', () => {
  it('a page made of all the payloads: nothing runs, before and after the frame sweep, ids kept', () => {
    const page = PAYLOADS.map(([n, p], i) => sec('s' + i, `<div data-wbc="${i}">${p(hitJs(n))}</div>`))
    const header = [sec('h', HEAD_PAYLOADS.map(([n, p]) => p(hitJs(n))).join(''), { head: HEAD_PAYLOADS.map(([n, p]) => p(hitJs(n))).join('') })]
    const doc = frameDoc(page, { header, footer: [] }, 'static', '', 'https://x.test/', '', '')
    const d = new DOMParser().parseFromString(doc, 'text/html')
    // the editor's own <base> is the only one, and it is marked
    expect(Array.from(d.querySelectorAll('base')).map((b) => b.getAttribute('href'))).toEqual(['https://x.test/'])
    sweep(d)
    const issues = dangers(d.documentElement).filter((x) => x !== 'base' && x !== 'attr data-wb-editor' && x !== 'marker-like comment')
    expect(issues).toEqual([])
    for (let i = 0; i < PAYLOADS.length; i++) expect(d.querySelector(`[data-wbc="${i}"]`), PAYLOADS[i][0]).not.toBeNull()
  })

  it('body and html attributes, wrappers', () => {
    const doc = frameDoc([sec('a', '<p>x</p>', { wrap: `<main onclick="${hitJs('w')}" class="m">`, bodyAttrs: `onload=${hitJs('b')} data-x="1"`, htmlAttrs: `onclick=${hitJs('h')} data-theme="dark"` })], { header: [], footer: [] }, 'static', '', 'https://x.test/')
    expect(doc).not.toMatch(/onclick|onload/)
    expect(doc).toContain('<main class="m">')
    expect(doc).toContain('data-x="1"')
    expect(doc).toContain('data-theme="dark"')
  })

  it('section markers are encoded: a section id cannot close the comment', () => {
    const doc = frameDoc([sec('x--><img src=x onerror=alert(1)>', '<p>x</p>')], { header: [], footer: [] }, 'static', '', 'https://x.test/')
    expect(doc).not.toContain('<img')
    const d = new DOMParser().parseFromString(doc, 'text/html')
    const s = new FrameSync(d)
    expect(s.collect()).toBe(true)
    expect(Array.from(s.secs.keys())).toEqual(['x--><img src=x onerror=alert(1)>'])
  })

  it('live / preview with the scripts allowed: the markup as stored', () => {
    const src = `<div onclick="x()" data-wbc="1"><script>boom()</script></div>`
    const doc = frameDoc([sec('a', src)], { header: [], footer: [] }, 'live', '', 'https://x.test/')
    expect(doc).toContain(src)
  })

  it('URL rules', () => {
    expect(unsafeUrl('java\tscript:x')).toBe(true)
    expect(unsafeUrl('\u0001 JavaScript:x')).toBe(true)
    expect(unsafeUrl('data:text/html,x')).toBe(true)
    expect(unsafeUrl('data:image/svg+xml,<svg/>', 'img')).toBe(false)
    expect(unsafeUrl('data:image/svg+xml,<svg/>', 'iframe')).toBe(true)
    expect(unsafeUrl('data:image/png;base64,AAA', 'img')).toBe(false)
    expect(unsafeUrl('data:font/woff2;base64,AAA', 'link')).toBe(false)
    expect(unsafeUrl('/a/b.png')).toBe(false)
    expect(unsafeUrl('https://x.test/javascript:1')).toBe(false)
    expect(unsafeUrl('#javascript:')).toBe(false)
    expect(neutralMarkup('<a href="javascript:x">y</a>')).toBe(`<a href="${BLOCKED_URL}">y</a>`)
  })

  it('ordinary design markup is kept as it renders', () => {
    const src = '<section class="hero" data-wbc="1"><h1 data-wbc="2">Hi &amp; <em>there</em></h1><img src="/a.png" srcset="/a.png 1x, /a@2x.png 2x" alt=""><a href="/x" target="_blank">x</a><svg viewBox="0 0 10 10"><use href="#i"></use><linearGradient id="g"></linearGradient></svg><iframe src="https://www.youtube.com/embed/x"></iframe></section>'
    const out = neutralMarkup(src)
    expect(out).toBe(
      '<section class="hero" data-wbc="1"><h1 data-wbc="2">Hi &amp; <em>there</em></h1><img src="/a.png" srcset="/a.png 1x, /a@2x.png 2x" alt=""><a href="/x" target="_blank">x</a><svg viewBox="0 0 10 10"><use href="#i"></use><linearGradient id="g"></linearGradient></svg><iframe sandbox="" data-wb-embed-ph=""></iframe></section>',
    )
    expect(neutralMarkup('<header data-wbc="2"><a data-wbc="3">x</a></header>', 'h:')).toBe('<header data-wbc="h:2"><a data-wbc="h:3">x</a></header>')
  })

  it('reconciler attributes come from the neutralised copy', () => {
    expect(neutralAttrs(`<a href="javascript:x()" onclick="y()" class="c" data-wbc="4"></a>`, 'a')).toEqual(
      new Map([
        ['href', BLOCKED_URL],
        ['class', 'c'],
        ['data-wbc', '4'],
      ]),
    )
    expect(neutralAttrs('<tr data-wbc="1" onclick="x()"></tr>', 'tr', 'tbody')).toEqual(new Map([['data-wbc', '1']]))
  })

  it('FrameSync inserts neutralised nodes only (no contextual fragments of design markup)', () => {
    const prev = '<div data-wbc="1" class="a"><p data-wbc="2">Hi</p></div>'
    const next = `<div data-wbc="1" class="b" onmouseover="${hitJs('r')}"><p data-wbc="2">Hi</p><!--><script>${hitJs('r2')}</script>--><img src=x onerror="${hitJs('r3')}"><svg><script>${hitJs('r4')}</script></svg></div>`
    const d = new DOMParser().parseFromString(frameDoc([sec('s1', prev)], { header: [], footer: [] }, 'static', '', 'https://x.test/'), 'text/html')
    const s = new FrameSync(d)
    s.collect()
    const p = d.querySelector('[data-wbc="2"]')
    expect(s.update({ s1: prev }, { s1: next })).toBe(true)
    expect(d.querySelector('[data-wbc="2"]')).toBe(p) // kept, not rebuilt
    expect(d.querySelector('[data-wbc="1"]')!.getAttribute('class')).toBe('b')
    expect(dangers(d.body)).toEqual([])
    expect(d.body.querySelectorAll('img').length).toBe(1)
  })
})

describe('text edits write back from the source (finding 17)', () => {
  const src = `<p data-wbc="1" class="lead">Hello <b onclick="track()" data-wbc="2">bold</b> &amp; <a href="/x" onmouseover="hover()" data-wbc="3">link</a><svg data-wbc="4" onload="i()"><path d="M0"/></svg></p>`
  const t = parse(src)
  const p = t.byId.get('1')!
  /** The static frame's element (the neutralised copy), as the user edits it. */
  const frameEl = () => {
    const d = new DOMParser().parseFromString(`<body>${neutralMarkup(src)}</body>`, 'text/html')
    return d.querySelector('[data-wbc="1"]')!
  }

  it('the frame copy has no handlers; the written markup keeps the source ones', () => {
    const el = frameEl()
    expect(el.innerHTML).not.toMatch(/onclick|onmouseover|onload/)
    const map = mapSource(el, t, p)
    ;(el.firstChild as Text).data = 'Hi there '
    const out = writeBack(el, t, map)
    expect(out).toBe(`Hi there <b onclick="track()" data-wbc="2">bold</b> &amp; <a href="/x" onmouseover="hover()" data-wbc="3">link</a><svg data-wbc="4" onload="i()"><path d="M0"/></svg>`)
  })

  it('unchanged: the source bytes', () => {
    const el = frameEl()
    expect(writeBack(el, t, mapSource(el, t, p))).toBe(src.slice(p.openEnd, p.closeStart))
  })

  it('new formatting is written from an allowlist without attributes; a split element gets a new id', () => {
    const el = frameEl()
    const map = mapSource(el, t, p)
    const d = el.ownerDocument
    const strong = d.createElement('strong')
    strong.setAttribute('style', 'color:red')
    strong.setAttribute('onclick', 'evil()')
    strong.textContent = 'new'
    el.appendChild(strong)
    const a = d.createElement('a')
    a.setAttribute('href', 'javascript:evil()')
    a.textContent = 'bad'
    el.appendChild(a)
    const clone = el.querySelector('[data-wbc="2"]')!.cloneNode(true) as Element
    el.appendChild(clone)
    const weird = d.createElement('marquee')
    weird.textContent = 'm'
    el.appendChild(weird)
    const out = writeBack(el, t, map)
    expect(out).toContain('<strong>new</strong>')
    expect(out).toContain('<a>bad</a>')
    expect(out).not.toContain('evil')
    expect(out).toMatch(/<b onclick="track\(\)" data-wbc="(?!2")[^"]+">bold<\/b>m$/)
  })

  it('a link pointed elsewhere keeps its handlers and gets the new safe address', () => {
    const el = frameEl()
    const map = mapSource(el, t, p)
    el.querySelector('a')!.setAttribute('href', '/y')
    expect(writeBack(el, t, map)).toContain('<a href="/y" onmouseover="hover()" data-wbc="3">link</a>')
    el.querySelector('a')!.setAttribute('href', 'javascript:z()')
    expect(writeBack(el, t, map)).toContain('<a href="/x" onmouseover="hover()" data-wbc="3">link</a>')
  })
})

describe('Content panel text box (finding 3)', () => {
  it('is built from the source tree: no attributes, no markup parsed, kept parts written back as they are', () => {
    const src = `<h2 data-wbc="1">Hi <img src=x onerror="${hitJs('panel')}"><b class="x" onclick="y()">b</b><a href="javascript:z()">j</a><a href="/ok">ok</a><span>&lt;img src=x onerror=alert(1)&gt;</span><!--c--></h2>`
    const t = parse(src)
    const el = t.byId.get('1')!
    const map = new Map()
    const box = document.createElement('div')
    box.appendChild(richNodes(t, el, map))
    expect(box.querySelector('img')).toBeNull()
    expect(dangers(box)).toEqual([])
    expect(box.querySelector('b')!.attributes.length).toBe(0)
    expect(box.querySelectorAll('a')[0].hasAttribute('href')).toBe(false)
    expect(box.querySelectorAll('a')[1].getAttribute('href')).toBe('/ok')
    expect(box.querySelector('span:not([data-wb-keep])')!.textContent).toBe('<img src=x onerror=alert(1)>')
    // unchanged → the source bytes (the image, the handlers, the comment)
    expect(writeBack(box, t, map)).toBe(src.slice(el.openEnd, el.closeStart))
  })
})
