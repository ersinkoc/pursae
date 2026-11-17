// @vitest-environment jsdom
/**
 * Embedded content in the static edit frame (embeds.ts, neutral.ts) and the per-run code frames
 * of mixed pages (patch 0021-mixed-frames: codeFrameDoc + the host's frame guard, guard.ts).
 *
 * Embeds (a YouTube / Vimeo player, a map, any iframe, object, embed) keep their element and its
 * attributes — the box and the layout around it are the published page's — load nothing, and are
 * drawn by the editor stylesheet as a placeholder that a click selects. <video> / <audio> files
 * stay native media. Mixed pages draw the same neutralised copy in a frame without allow-scripts.
 */
import { afterEach, describe, expect, it } from 'vitest'
import type { Section } from '@/builder/types'
import { setLocale } from '@/builder/i18n'
import { codeFrameDoc } from '@/builder/codeFrameDoc'
import { FrameSync, frameDoc } from '../../host/elements/frame'
import { EMBED_ATTR, embedCss, embedImage, embedTarget, embedText } from '../../host/elements/embeds'
import { BLOCKED_URL, INERT, neutralMarkup, sweep } from '../../host/elements/neutral'
import { frameGuard } from '../../host/elements/guard'
import tr from '../../host/i18n-tr'
import { HEAD_PAYLOADS, PAYLOADS, hitJs } from '../security/payloads'

const sec = (id: string, html: string, extra: Record<string, string> = {}): Section =>
  ({ id, kind: 'code', name: id, content: { html, head: '', wrap: '', ...extra }, items: [], style: { hideOn: [] } }) as unknown as Section

const EN = 'Embedded content plays on the published page. Publish and open the page to watch it.'
const TR = 'Gömülü içerik yayınlanan sayfada oynar. İzlemek için yayınlayıp sayfayı açın.'

const EMBEDS: [string, string][] = [
  ['youtube', '<iframe data-wbc="1" class="video" width="560" height="315" src="https://www.youtube.com/embed/dQw4w9WgXcQ?rel=0" title="YouTube video player" frameborder="0" allow="accelerometer; autoplay; encrypted-media; picture-in-picture" referrerpolicy="strict-origin-when-cross-origin" allowfullscreen></iframe>'],
  ['youtube-nocookie', '<iframe data-wbc="2" style="aspect-ratio:16/9;width:100%" src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" loading="lazy" allowfullscreen></iframe>'],
  ['vimeo', '<iframe data-wbc="3" src="https://player.vimeo.com/video/76979871?h=8272103f6e" width="640" height="360" allow="autoplay; fullscreen; picture-in-picture"></iframe>'],
  ['maps', '<iframe data-wbc="4" class="map" src="https://www.google.com/maps/embed?pb=!1m18!1m12!1m3!1d3010.27" width="600" height="450" style="border:0;" loading="lazy" referrerpolicy="no-referrer-when-downgrade"></iframe>'],
]

const parsedBody = (html: string) => new DOMParser().parseFromString(`<!doctype html><html><head></head><body>${html}</body></html>`, 'text/html')
const attrsOf = (el: Element) => new Map(Array.from(el.attributes).map((a) => [a.name, a.value]))

describe('embeds in the static copy', () => {
  for (const [name, src] of EMBEDS) {
    it(`${name}: same element and attributes (same box), nothing loaded, marked as a placeholder`, () => {
      const out = neutralMarkup(src)
      const before = parsedBody(src).body.firstElementChild!
      const after = parsedBody(out).body.firstElementChild!
      expect(after.localName).toBe('iframe')
      expect(after.hasAttribute('src')).toBe(false)
      expect(after.getAttribute('sandbox')).toBe('')
      expect(after.getAttribute(EMBED_ATTR)).toBe('')
      const want = attrsOf(before)
      want.delete('src')
      want.set('sandbox', '')
      want.set(EMBED_ATTR, '')
      expect(attrsOf(after)).toEqual(want)
      expect(neutralMarkup(out)).toBe(out) // stable
    })
  }

  it('object / embed / applet are marked and load nothing; <video> / <audio> files stay native', () => {
    const out = neutralMarkup('<object data="/a.pdf" type="application/pdf" width="400" height="300"><p>fallback</p></object><embed src="/a.swf" width="300" height="100"><video src="/wp-content/uploads/web-builder/kits/k/v.mp4" controls poster="/p.jpg"></video><audio src="/a.mp3" controls></audio>')
    const d = parsedBody(out)
    for (const tag of ['object', 'embed']) {
      const el = d.querySelector(tag)!
      expect(el.getAttribute(EMBED_ATTR), tag).toBe('')
      expect(el.hasAttribute('src') || el.hasAttribute('data'), tag).toBe(false)
      expect(el.getAttribute('width'), tag).not.toBeNull()
    }
    expect(d.querySelector('video')!.getAttribute('src')).toBe('/wp-content/uploads/web-builder/kits/k/v.mp4')
    expect(d.querySelector('video')!.hasAttribute(EMBED_ATTR)).toBe(false)
    expect(d.querySelector('audio')!.getAttribute('src')).toBe('/a.mp3')
  })

  it("a design's own placeholder marker on another element is dropped", () => {
    expect(neutralMarkup(`<div ${EMBED_ATTR}="" class="x">y</div>`)).toBe('<div class="x">y</div>')
  })

  it('the reconciler keeps the placeholder: a new src in the source changes nothing in the frame', () => {
    const prev = `<section data-wbc="0">${EMBEDS[0][1]}</section>`
    const next = prev.replace('dQw4w9WgXcQ?rel=0', 'abc')
    const d = new DOMParser().parseFromString(frameDoc([sec('s', prev)], { header: [], footer: [] }, 'static', embedCss(), 'https://x.test/'), 'text/html')
    sweep(d)
    const s = new FrameSync(d)
    s.collect()
    expect(s.update({ s: prev }, { s: next })).toBe(true)
    // an iframe's content is raw text for the source parser: it is rebuilt from neutralised nodes
    const f = d.querySelector('iframe')!
    expect(f.getAttribute('data-wbc')).toBe('1')
    expect(f.hasAttribute('src')).toBe(false)
    expect(f.getAttribute(EMBED_ATTR)).toBe('')
    expect(f.getAttribute('sandbox')).toBe('')
  })
})

describe('the placeholder', () => {
  afterEach(() => setLocale('en'))

  it('text in English, Turkish in the host catalog', () => {
    expect(tr[EN]).toBe(TR)
    setLocale('en')
    expect(embedText()).toBe(EN)
    setLocale('tr')
    expect(embedText()).toBe(TR)
  })

  it('stylesheet: a background image with the icon and the text, no pointer events, object / embed keep a box', () => {
    const css = embedCss('A & <b>')
    expect(css).toContain(`[${EMBED_ATTR}]:not(#wb#wb)`)
    expect(css).toContain('pointer-events:none!important')
    expect(css).toContain(':where(object,embed,applet)')
    const img = embedImage('A & <b>')
    expect(img.startsWith('data:image/svg+xml,')).toBe(true)
    const svg = decodeURIComponent(img.slice('data:image/svg+xml,'.length))
    expect(svg).toContain('A &amp; &lt;b&gt;')
    expect(svg).not.toMatch(/<script|on\w+=/i)
    // well-formed XML (an SVG image must parse)
    expect(new DOMParser().parseFromString(svg, 'image/svg+xml').querySelector('parsererror')).toBeNull()
  })

  it('a click on the placeholder selects the embed (it takes no pointer events; the hit is the element behind it)', () => {
    const d = document.implementation.createHTMLDocument('')
    d.body.innerHTML = `<div data-wbc="1" id="box"><p data-wbc="2" id="p">x</p><iframe data-wbc="3" ${EMBED_ATTR}="" sandbox=""></iframe></div><div id="other"></div>`
    const rect = (l: number, t: number, w: number, h: number) => () => ({ left: l, top: t, right: l + w, bottom: t + h, width: w, height: h, x: l, y: t, toJSON() {} }) as DOMRect
    d.querySelector('iframe')!.getBoundingClientRect = rect(10, 100, 560, 315)
    const box = d.getElementById('box')!
    expect(embedTarget(d, box, 50, 200)).toBe(d.querySelector('iframe'))
    expect(embedTarget(d, box, 50, 20)).toBe(box)
    expect(embedTarget(d, d.getElementById('other'), 50, 200)).toBe(d.getElementById('other'))
    expect(embedTarget(d, d.getElementById('p'), 50, 200)).toBe(d.getElementById('p'))
    expect(frameGuard.target(d, box, 50, 200)).toBe(d.querySelector('iframe'))
  })
})

/** What in a tree could run (the frame's parse), stricter than neutral.ts's own rules. */
function runs(root: Node): string[] {
  const out: string[] = []
  const all = (root as Document).querySelectorAll ? Array.from((root as Document).querySelectorAll('*')) : []
  for (const el of all) {
    const tag = el.localName.toLowerCase()
    if (tag === 'script' && (el.getAttribute('type') !== INERT || el.textContent || el.hasAttribute('src'))) out.push('script')
    if (['iframe', 'frame', 'object', 'embed'].includes(tag) && (el.hasAttribute('src') || el.hasAttribute('data') || el.hasAttribute('srcdoc'))) out.push(`${tag} loads`)
    if (tag === 'iframe' && el.getAttribute('sandbox') !== '') out.push('iframe sandbox')
    for (const a of Array.from(el.attributes)) {
      if (/^on/i.test(a.localName)) out.push(`handler ${a.name}`)
      if (/^\s*(javascript|vbscript):/i.test(a.value.replace(/[\s\u0000-\u001f]/g, ''))) out.push(`url ${a.name}`)
    }
    if (el.shadowRoot) out.push('shadow root')
  }
  return out
}

describe('mixed pages: the per-run code frame (patch 0021)', () => {
  const payloadRun = () => [
    sec('a', PAYLOADS.map(([n, p], i) => `<div data-wbc="${i}">${p(hitJs(n))}</div>`).join(''), { head: HEAD_PAYLOADS.map(([n, p]) => p(hitJs(n))).join('\n'), body: 'site' }),
    sec('b', `<section data-wbc="e">${EMBEDS.map(([, h]) => h).join('')}</section>`, { wrap: '<main class="m">' }),
  ]

  it('edit mode: the static copy of every payload and head payload, nothing runs (before and after the sweep), embeds are placeholders', () => {
    const doc = codeFrameDoc(payloadRun(), true, frameGuard, 'https://x.test/')
    const d = new DOMParser().parseFromString(doc, 'text/html')
    expect(Array.from(d.querySelectorAll('base')).map((b) => b.getAttribute('href'))).toEqual(['https://x.test/'])
    expect(d.querySelector('base')!.hasAttribute('data-wb-editor')).toBe(true)
    expect(runs(d)).toEqual([])
    sweep(d)
    expect(runs(d)).toEqual([])
    expect(d.querySelector('base')).not.toBeNull() // the editor's base survives the sweep
    expect(d.querySelectorAll(`iframe[${EMBED_ATTR}]`).length).toBeGreaterThanOrEqual(EMBEDS.length)
    expect(d.querySelector('style[data-wb-edit]')!.textContent).toContain(EMBED_ATTR)
    expect(d.body.className).toBe('site')
    // one body for the run: a payload that swallows what follows it (plaintext, raw text) may hide later ones
    expect(d.querySelector('[data-wbc="0"]')).not.toBeNull()
  })

  it('preview without the confirmation is static too (no outlines, placeholders kept)', () => {
    const doc = codeFrameDoc(payloadRun(), false, frameGuard, 'https://x.test/')
    const d = new DOMParser().parseFromString(doc, 'text/html')
    expect(runs(d)).toEqual([])
    expect(d.querySelector('style[data-wb-edit]')!.textContent).not.toContain('data-wbc-sel')
    expect(d.querySelector('style[data-wb-edit]')!.textContent).toContain(EMBED_ATTR)
  })

  it('with the scripts allowed (no guard): the markup as stored', () => {
    const src = '<div data-wbc="1" onclick="x()"><script>boom()</script><iframe src="https://player.vimeo.com/video/1"></iframe></div>'
    const doc = codeFrameDoc([sec('a', src)], false, null, 'https://x.test/')
    expect(doc).toContain(src)
  })

  it('an attribute the toolbar writes into the static frame is made inert there', () => {
    const d = document.implementation.createHTMLDocument('')
    d.body.innerHTML = '<a data-wbc="1" href="/x"><img data-wbc="2" src="/a.png"></a>'
    const a = d.querySelector('a')!
    a.setAttribute('href', 'javascript:top.hit(1)')
    frameGuard.neutralEl(a)
    expect(a.getAttribute('href')).toBe(BLOCKED_URL)
    const img = d.querySelector('img')!
    img.setAttribute('onerror', 'x()')
    frameGuard.neutralEl(img)
    expect(img.hasAttribute('onerror')).toBe(false)
  })

  it('in-place text is written back from the source: handlers and scripts kept as stored', () => {
    const srcOuter = `<p data-wbc="1">Hi <b onclick="track()" data-wbc="2">there</b><script>count()</script></p>`
    const d = new DOMParser().parseFromString(`<body>${neutralMarkup(srcOuter)}</body>`, 'text/html')
    const el = d.querySelector<HTMLElement>('[data-wbc="1"]')!
    expect(el.innerHTML).not.toContain('onclick')
    const finish = frameGuard.beginText(srcOuter, el)
    ;(el.firstChild as Text).data = 'Hello '
    expect(finish()).toBe('Hello <b onclick="track()" data-wbc="2">there</b><script>count()</script>')
    // another element than the source: nothing to write back
    expect(frameGuard.beginText('<h2 data-wbc="1">x</h2>', el)()).toBeNull()
  })
})
