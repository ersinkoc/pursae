// @vitest-environment jsdom
/**
 * Element editor canvas, Live by default (plan K1–K5): the mode chosen from the boot object before
 * the first frame, what a page runs (its kits and its own code, read from the markup), the
 * script-trust prompt and its answers, the choice following the page's changes, Interact mode's
 * pass-through, which source element a pointer means when a script made the element, overlay
 * re-measuring, live edits applied without a reload, and text edits a script interferes with.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { bootKits, bootTrust, canRunScripts, covers, frameModeOf, grantsOf, initialScripts, needsOf, pageTrust, runsNow, sandboxOf, sectionNeeds, usesScripts, type Needs, type TrustList } from '../../host/elements/trust'
import { getElState, resetElState, setElState } from '../../host/elements/state'
import { TrustPrompt, promptChoices, reconcileTrust, revokeTrust, trustAlways, trustLater, trustSession } from '../../host/elements/ScriptsGate'
import { drawnText, leavesFrame, liveChange, measureLoop, passesThrough, sourceTarget, watchFrame } from '../../host/elements/interact'
import { LIVE_SANDBOX, applyToLive, openLive } from '../../host/elements/live'
import { RestError } from '../../host/rest'
import { startTextEdit, watchEdit } from '../../host/elements/richtext'
import { sha256 } from '../../host/elements/sha256'
import { createHash } from 'node:crypto'

const KIT = (k: string, f = 'site.css') => `/wp-content/uploads/web-builder/kits/${k}/${f}`
const INLINE = '<div data-wbc="1"><button onclick="toggle()">Dark</button></div><script>init()</script>'
/** A section of kit k as the importer writes it: the kit's stylesheet and script, the design's inline code. */
const kitHtml = (k: string, inline = INLINE) => `${inline}<script src="${KIT(k, 'js/app.js')}?v=3"></script>`
const kitHead = (k: string) => `<link rel="stylesheet" href="${KIT(k)}">`

type Sec = { id: string; kind: string; content: { html: string; head?: string; kit?: string } }
const sec = (o: { kit?: string; label?: string; html?: string; head?: string }): Sec => ({
  id: 's' + Math.random(),
  kind: 'code',
  content: { html: o.html ?? (o.kit ? kitHtml(o.kit) : INLINE), head: o.head ?? (o.kit ? kitHead(o.kit) : ''), ...(o.label ? { kit: o.label } : {}) },
})

/** A boot object as the server prints it (only the fields the decision reads). */
function boot(o: { trust?: string[]; code?: Record<string, string[]>; can?: boolean; unfiltered?: boolean; secs?: Sec[]; header?: Sec; draft?: Sec[] }) {
  return {
    caps: { publish: true, uploadFiles: true, unfilteredHtml: o.unfiltered ?? false, editOthers: true },
    ...(o.can !== undefined ? { canRunScripts: o.can } : {}),
    ...(o.trust ? { scriptTrust: o.trust } : {}),
    ...(o.code ? { scriptCode: o.code } : {}),
    codeParts: o.header ? { header: { id: 1, title: 'h', editUrl: '', sections: [o.header] }, footer: null } : null,
    document: { doc: { sections: o.secs ?? [sec({ kit: 'acme' })] }, draft: o.draft ? { doc: { sections: o.draft } } : null },
  } as never
}

/** The code hashes a section list needs (what the server would hold after "Always"). */
const codeOf = (...secs: Sec[]) => needsOf(secs).code

const setBoot = (b: unknown) => {
  ;(window as unknown as { __WEB_BUILDER__?: unknown }).__WEB_BUILDER__ = b
  resetElState()
}

afterEach(() => setBoot(undefined))

describe('what a page runs (needsOf)', () => {
  it('sha256 is SHA-256 of the UTF-8 bytes', () => {
    for (const v of ['', 'abc', 'çğış ü — ✓', 'x'.repeat(55), 'y'.repeat(56), 'z'.repeat(1000)]) expect(sha256(v)).toBe(createHash('sha256').update(v, 'utf8').digest('hex'))
  })

  it('the kits come from the kit URLs in the markup, never from content.kit', () => {
    expect(needsOf([sec({ kit: 'acme' })]).kits).toEqual(['acme'])
    expect(needsOf([sec({ label: 'acme' })]).kits).toEqual([]) // a label grants nothing
    expect(needsOf([sec({ label: 'acme', html: `<p>x</p><script src="${KIT('other', 'a.js')}"></script>` })]).kits).toEqual(['other'])
    expect(needsOf([sec({ html: `<div style="background:url(${KIT('img-kit', 'a.png')})">x</div>` })]).kits).toEqual(['img-kit'])
  })

  it('inline scripts, handlers, javascript: URLs and non-kit scripts are code, hashed one by one', () => {
    const n = sectionNeeds('<p onclick="a()">x</p><a href=" javascript:b()">y</a><script>c()</script><script src="https://cdn.example/x.js"></script>', '')
    expect(n.scripts).toBe(true)
    expect(n.code).toHaveLength(4)
    expect(n.code.every((h) => /^[0-9a-f]{64}$/.test(h))).toBe(true)
    // stable, and any change of the code is another hash
    expect(sectionNeeds('<p onclick="a()">x</p>', '').code).toEqual(sectionNeeds('<p onclick="a()">x</p>', '').code)
    expect(sectionNeeds('<p onclick="a()">x</p>', '').code).not.toEqual(sectionNeeds('<p onclick="a(1)">x</p>', '').code)
    // text edits around the code keep its hashes
    expect(sectionNeeds('<h1>A</h1><script>c()</script>', '').code).toEqual(sectionNeeds('<h1>B</h1><script>c()</script>', '').code)
  })

  it('a kit script counts only from this origin; anything else is code', () => {
    const o = 'https://site.test'
    expect(sectionNeeds(`<script src="${KIT('acme', 'a.js')}"></script>`, '', o)).toMatchObject({ kits: ['acme'], code: [] })
    expect(sectionNeeds(`<script src="${o}${KIT('acme', 'a.js')}"></script>`, '', o)).toMatchObject({ kits: ['acme'], code: [] })
    expect(sectionNeeds(`<script src="//evil.test${KIT('acme', 'a.js')}"></script>`, '', o).code).toHaveLength(1)
    expect(sectionNeeds(`<script src="https://evil.test${KIT('acme', 'a.js')}"></script>`, '', o).code).toHaveLength(1)
    expect(sectionNeeds(`<script src="${KIT('acme', '../../../evil.js')}"></script>`, '', o).code).toHaveLength(1)
  })

  it('data blocks and markup without code need nothing; <base>, srcdoc and handler animations are code', () => {
    expect(sectionNeeds('<script type="application/ld+json">{"a":1}</script><p>hi</p>', '')).toMatchObject({ code: [], scripts: false })
    expect(sectionNeeds('<p>Hold onto = wait</p>', '').scripts).toBe(true) // a raw handler-like text: the whole section, to be safe
    expect(sectionNeeds('', '<base href="https://evil.test/">').code).toHaveLength(1)
    expect(sectionNeeds('<iframe srcdoc="&lt;script&gt;x()&lt;/script&gt;"></iframe>', '').code.length).toBeGreaterThan(0)
    expect(sectionNeeds('<svg><set attributeName="onmouseover" to="x()"/></svg>', '').code.length).toBeGreaterThan(0)
    expect(sectionNeeds('<script type="importmap">{"imports":{"x":"https://cdn/x.js"}}</script>', '').code).toHaveLength(1)
  })

  it('markup the browser could read as more code than the parse found is one item: the whole section', () => {
    // a script body names handlers and URLs: they are its own code, not more items
    expect(sectionNeeds('<script>const onClick = 1; location.href = "javascript:void 0"</script>', '').code).toHaveLength(1)
    const tricky = '<noscript><p title="</noscript><img src=x onerror=a()>"></p></noscript>'
    const n = sectionNeeds(tricky, '')
    expect(n.scripts).toBe(true)
    expect(n.code).toContain(sha256('section\u0000\u0000' + tricky))
  })

  it('the header and footer parts count; hidden and non-code sections do not', () => {
    const b = boot({ secs: [sec({ kit: 'acme' }), { ...sec({ kit: 'hid' }), hidden: true } as never], header: sec({ kit: 'shell' }) })
    expect(bootKits(b)).toEqual(['acme', 'shell'])
    expect(bootKits(boot({ secs: [sec({ kit: 'acme' })], draft: [sec({ kit: 'other' })] }))).toEqual(['other'])
  })
})

describe('mode before the first frame (K1, K2)', () => {
  it('reads canRunScripts, else caps.unfilteredHtml; trust only as valid kit slugs', () => {
    expect(canRunScripts(boot({ can: true }))).toBe(true)
    expect(canRunScripts(boot({ can: false, unfiltered: true }))).toBe(false)
    expect(canRunScripts(boot({ unfiltered: true }))).toBe(true)
    expect(canRunScripts(undefined)).toBe(false)
    expect([...bootTrust(boot({ trust: ['acme', '../x', 'B'] }))]).toEqual(['acme'])
  })

  it('page trust: blocked / trusted / untrusted / nokit', () => {
    const n = needsOf([sec({ kit: 'acme' })])
    const g = grantsOf({ trust: new Set(['acme']), trustCode: { acme: n.code } })
    expect(pageTrust(n, g, false)).toBe('blocked')
    expect(pageTrust(n, g, true)).toBe('trusted')
    expect(pageTrust(n, grantsOf({ trust: new Set(['acme']) }), true)).toBe('untrusted') // its inline code is not approved
    expect(pageTrust(needsOf([sec({ kit: 'acme' }), sec({ kit: 'shell' })]), g, true)).toBe('untrusted')
    expect(pageTrust(needsOf([sec({})]), g, true)).toBe('nokit')
  })

  it('a trusted page (its kit and its code) opens Live with the scripts allowed, no prompt', () => {
    const s0 = sec({ kit: 'acme' })
    setBoot(boot({ can: true, trust: ['acme'], code: { acme: codeOf(s0) }, secs: [s0] }))
    const s = getElState()
    expect([s.live, s.scripts, s.prompt, s.canRun]).toEqual([true, true, false, true])
    expect(frameModeOf(false, s.live, s.scripts)).toBe('live')
    expect(sandboxOf(s.scripts)).toBe(LIVE_SANDBOX)
  })

  it('an untrusted design never runs before the choice: Safe mode, inert sandbox, prompt', () => {
    setBoot(boot({ can: true, trust: ['other'], secs: [sec({ kit: 'acme' })] }))
    const s = getElState()
    expect([s.live, s.scripts, s.prompt]).toEqual([false, false, true])
    expect(frameModeOf(false, s.live, s.scripts)).toBe('static')
    expect(frameModeOf(true, s.live, s.scripts)).toBe('static') // Preview too
    expect(sandboxOf(s.scripts)).toBe('allow-same-origin')
  })

  it('(a) a trusted kit does not cover a section without a kit that has scripts', () => {
    const s0 = sec({ kit: 'acme' })
    setBoot(boot({ can: true, trust: ['acme'], code: { acme: codeOf(s0) }, secs: [s0, sec({ html: '<script>steal()</script>' })] }))
    expect([getElState().scripts, getElState().prompt]).toEqual([false, true])
  })

  it('(c) content.kit is a label: a section claiming a trusted kit with its own code is asked about', () => {
    setBoot(boot({ can: true, trust: ['acme'], code: { acme: [] }, secs: [sec({ label: 'acme', html: '<script>steal()</script>' })] }))
    expect([getElState().scripts, getElState().prompt]).toEqual([false, true])
    // …also when it loads the trusted kit's stylesheet: its inline code is not approved
    setBoot(boot({ can: true, trust: ['acme'], secs: [sec({ label: 'acme', head: kitHead('acme'), html: '<script>steal()</script>' })] }))
    expect([getElState().scripts, getElState().prompt]).toEqual([false, true])
    // …and when it loads another kit's script
    setBoot(boot({ can: true, trust: ['acme'], secs: [sec({ label: 'acme', html: `<script src="${KIT('evil', 'x.js')}"></script>` })] }))
    expect(getElState().scripts).toBe(false)
  })

  it('a header kit not trusted keeps the page in Safe mode', () => {
    const s0 = sec({ kit: 'acme' })
    setBoot(boot({ can: true, trust: ['acme'], code: { acme: codeOf(s0) }, secs: [s0], header: sec({ kit: 'shell' }) }))
    expect(getElState().scripts).toBe(false)
  })

  it('without canRunScripts: Safe mode only, no prompt, even with a (stale) trust list', () => {
    const s0 = sec({ kit: 'acme' })
    setBoot(boot({ can: false, trust: ['acme'], code: { acme: codeOf(s0) }, secs: [s0] }))
    const s = getElState()
    expect([s.live, s.scripts, s.prompt, s.canRun]).toEqual([false, false, false, false])
  })

  it('defence in depth: nothing turns the scripts on without canRun', () => {
    setBoot(boot({ can: false, secs: [sec({ kit: 'acme' })] }))
    setElState({ scripts: true, live: true })
    expect([getElState().scripts, getElState().live]).toEqual([false, false])
    trustSession(needsOf([sec({ kit: 'acme' })]))
    expect(getElState().scripts).toBe(false)
    expect(frameModeOf(false, true, true, false)).toBe('static')
    expect(frameModeOf(true, true, true, false)).toBe('static')
    expect(sandboxOf(true, false)).toBe('allow-same-origin')
    expect(runsNow({ canRun: false, scripts: true }, needsOf([]))).toBe(false)
  })

  it('no prompt for a design without scripts', () => {
    expect(usesScripts([{ content: { html: '<p>Hi</p>' } }])).toBe(false)
    expect(usesScripts([{ content: { html: '<p onclick="x()">Hi</p>' } }])).toBe(true)
    expect(usesScripts([{ content: { html: '<p></p>', head: '<script src="a.js"></script>' } }])).toBe(true)
    setBoot(boot({ can: true, secs: [sec({ html: '<p data-wbc="1">Hi</p>', head: kitHead('acme') })] }))
    expect(getElState().prompt).toBe(false)
  })

  it('a page that loads no kit: the prompt offers this session only', () => {
    setBoot(boot({ can: true, secs: [sec({})] }))
    expect(getElState().prompt).toBe(true)
    const none = grantsOf({})
    expect(promptChoices(needsOf([sec({})]), none, true)).toEqual(['session', 'later'])
    const n = needsOf([sec({ kit: 'acme' })])
    expect(promptChoices(n, none, true)).toEqual(['always', 'session', 'later'])
    expect(promptChoices(n, grantsOf({ trust: new Set(['acme']), trustCode: { acme: n.code } }), true)).toEqual([])
    expect(promptChoices(n, none, false)).toEqual([])
  })
})

describe('the choice follows the page (b)', () => {
  const s0 = sec({ kit: 'acme' })
  beforeEach(() => setBoot(boot({ can: true, trust: ['acme'], code: { acme: codeOf(s0) }, secs: [s0] })))

  it('inserting a section of an untrusted kit stops the scripts in the same render and asks again', () => {
    expect(runsNow(getElState(), needsOf([s0]))).toBe(true)
    const next = needsOf([s0, sec({ kit: 'other' })])
    // what the canvas computes in render: static before any frame draws the new markup
    expect(runsNow(getElState(), next)).toBe(false)
    expect(frameModeOf(false, getElState().live, runsNow(getElState(), next))).toBe('static')
    reconcileTrust(next)
    expect([getElState().scripts, getElState().live, getElState().prompt]).toEqual([false, false, true])
  })

  it('pasted or edited code is asked about too; text edits are not', () => {
    expect(runsNow(getElState(), needsOf([{ ...s0, content: { ...s0.content, html: s0.content.html.replace('Dark', 'Light') } }]))).toBe(true)
    expect(runsNow(getElState(), needsOf([{ ...s0, content: { ...s0.content, html: s0.content.html + '<img src=x onerror="steal()">' } }]))).toBe(false)
    expect(runsNow(getElState(), needsOf([{ ...s0, content: { ...s0.content, html: s0.content.html.replace('init()', 'steal()') } }]))).toBe(false)
  })

  it('"This session only" covers what the page ran when it was given, not what comes later', () => {
    const two = needsOf([s0, sec({ kit: 'other' })])
    reconcileTrust(two)
    trustSession(two)
    expect(runsNow(getElState(), two)).toBe(true)
    const three = needsOf([s0, sec({ kit: 'other' }), sec({ html: '<script>more()</script>' })])
    expect(runsNow(getElState(), three)).toBe(false)
    reconcileTrust(three)
    expect([getElState().scripts, getElState().prompt]).toEqual([false, true])
    // undo: the page is back to what was covered; Live runs it again without asking (covers)
    expect(covers(two, grantsOf(getElState()))).toBe(true)
  })
})

describe('the prompt’s answers', () => {
  const a = sec({ kit: 'acme' })
  const sh = sec({ kit: 'shell', html: '<p>x</p>' })
  let needs: Needs
  beforeEach(() => {
    setBoot(boot({ can: true, trust: ['other'], secs: [a, sh] }))
    needs = needsOf([a, sh])
  })
  const list = (trust: string[], code: Record<string, string[]> = {}): TrustList => ({ trust, code })

  it('Always for this design: every untrusted kit is trusted on the server with the page’s code, then Live', async () => {
    const put = vi.fn(async (slug: string, on: boolean, code?: readonly string[]) => list(slug === 'acme' ? ['acme', 'other'] : ['acme', 'other', 'shell'], { acme: [...(code ?? [])], shell: [...(code ?? [])] }))
    await trustAlways(needs, put)
    expect(put.mock.calls).toEqual([
      ['acme', true, needs.code],
      ['shell', true, needs.code],
    ])
    const s = getElState()
    expect([...s.trust].sort()).toEqual(['acme', 'other', 'shell'])
    expect([s.scripts, s.live, s.prompt]).toEqual([true, true, false])
  })

  it('only code missing: it is stored with the page’s first kit', async () => {
    setBoot(boot({ can: true, trust: ['acme', 'shell'], secs: [a, sh] }))
    const put = vi.fn(async (_s: string, _on: boolean, code?: readonly string[]) => list(['acme', 'shell'], { acme: [...(code ?? [])] }))
    await trustAlways(needs, put)
    expect(put.mock.calls).toEqual([['acme', true, needs.code]])
    expect(getElState().scripts).toBe(true)
  })

  it('a failed trust request runs nothing and keeps what the server confirmed before it', async () => {
    const put = vi.fn(async (slug: string) => {
      if (slug === 'shell') throw new Error('403')
      return list(['acme', 'other'], { acme: needs.code })
    })
    await expect(trustAlways(needs, put)).rejects.toThrow('403')
    const s = getElState()
    expect([s.scripts, s.live, s.prompt]).toEqual([false, false, true])
    expect([...s.trust].sort()).toEqual(['acme', 'other']) // the server's list, not the stale one
  })

  it('an answer that does not cover the page runs nothing', async () => {
    await expect(trustAlways(needs, async () => list(['other']))).rejects.toThrow()
    expect(getElState().scripts).toBe(false)
  })

  it('This session only: runs now, stores nothing', () => {
    trustSession(needs)
    const s = getElState()
    expect([s.scripts, s.live, s.prompt]).toEqual([true, true, false])
    expect([...s.trust]).toEqual(['other'])
    resetElState() // a new editor session asks again
    expect([getElState().scripts, getElState().prompt]).toEqual([false, true])
  })

  it('Not now: the prompt goes, Safe mode stays', () => {
    trustLater()
    const s = getElState()
    expect([s.scripts, s.live, s.prompt]).toEqual([false, false, false])
  })

  it('Stop always running: the page’s trusted kits are revoked and the canvas goes back to Safe mode', async () => {
    setBoot(boot({ can: true, trust: ['acme', 'other'], code: { acme: codeOf(a) }, secs: [a] }))
    setElState({ interact: true })
    const put = vi.fn(async () => list(['other']))
    await revokeTrust(['acme'], put)
    expect(put).toHaveBeenCalledWith('acme', false)
    const s = getElState()
    expect([...s.trust]).toEqual(['other'])
    expect(s.trustCode).toEqual({})
    expect([s.scripts, s.live, s.interact]).toEqual([false, false, false])
  })

  it('the trust persists: a new session with the server’s list opens Live', async () => {
    let stored = list([])
    await trustAlways(needs, async (slug, on, code) => (stored = list([...new Set([...stored.trust, slug])], { ...stored.code, [slug]: [...(code ?? [])] })))
    setBoot(boot({ can: true, trust: stored.trust, code: stored.code, secs: [a, sh] }))
    expect([getElState().scripts, getElState().live, getElState().prompt]).toEqual([true, true, false])
  })
})

describe('openLive (8)', () => {
  const input = { postId: 5, page: [], header: [], footer: [], shell: 'canvas' as const, url: '/', kitCss: '', localCss: '', editCss: '' }
  const host = (request: (...a: unknown[]) => Promise<unknown>) => ((window as unknown as { __WEB_BUILDER_HOST__?: unknown }).__WEB_BUILDER_HOST__ = { rest: { request } })
  afterEach(() => delete (window as unknown as { __WEB_BUILDER_HOST__?: unknown }).__WEB_BUILDER_HOST__)

  it('every failure is thrown (the canvas stays in Safe mode): never a srcdoc under the editor’s policy', async () => {
    await expect(openLive(input)).rejects.toThrow() // no host
    for (const e of [new RestError(404, 'rest_no_route', 'x'), new RestError(403, 'rest_forbidden', 'x'), new RestError(413, 'wb_too_large', 'x'), new TypeError('Failed to fetch')]) {
      host(async () => {
        throw e
      })
      await expect(openLive(input)).rejects.toBe(e)
    }
    for (const url of ['//evil.test/x', 'https://evil.test/x', 'javascript:x', 3]) {
      host(async () => ({ data: { url } }))
      await expect(openLive(input)).rejects.toThrow()
    }
    host(async () => ({ data: { url: '/p/?wb-live-frame=ab' } }))
    await expect(openLive(input)).resolves.toEqual({ src: '/p/?wb-live-frame=ab' })
  })
})

describe('TrustPrompt', () => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

  function render(props: Parameters<typeof TrustPrompt>[0]) {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const root = createRoot(el)
    act(() => root.render(createElement(TrustPrompt, props)))
    return { el, done: () => (act(() => root.unmount()), el.remove()) }
  }

  it('is non-blocking (not a modal) and offers the three answers with the warning', () => {
    const on = { onAlways: vi.fn(), onSession: vi.fn(), onLater: vi.fn() }
    const r = render({ needs: needsOf([sec({ kit: 'acme' })]), grants: grantsOf({}), canRun: true, ...on })
    const dlg = r.el.querySelector('[data-wb-trust-prompt]')!
    expect(dlg.getAttribute('aria-modal')).toBe('false')
    expect(dlg.textContent).toMatch(/admin access|yönetici yetkinizle/) // i18n-ignore
    for (const k of ['always', 'session', 'later'] as const) act(() => (r.el.querySelector(`[data-wb-trust="${k}"]`) as HTMLButtonElement).click())
    expect([on.onAlways.mock.calls.length, on.onSession.mock.calls.length, on.onLater.mock.calls.length]).toEqual([1, 1, 1])
    r.done()
  })

  it('no "Always" without a kit; nothing for a user who cannot run scripts', () => {
    const f = () => {}
    let r = render({ needs: needsOf([sec({})]), grants: grantsOf({}), canRun: true, onAlways: f, onSession: f, onLater: f })
    expect(r.el.querySelector('[data-wb-trust="always"]')).toBeNull()
    expect(r.el.querySelector('[data-wb-trust="session"]')).not.toBeNull()
    r.done()
    r = render({ needs: needsOf([sec({ kit: 'acme' })]), grants: grantsOf({}), canRun: false, onAlways: f, onSession: f, onLater: f })
    expect(r.el.innerHTML).toBe('')
    r.done()
  })
})

describe('text edits in Live (K5)', () => {
  it('a text is "drawn" by its text and elements, not by how the browser spells them', () => {
    expect(drawnText('Hello  <b>x</b>', 'Hello <b>x</b>')).toBe(false)
    expect(drawnText('Tom &amp; Jerry<br/>&nbsp;x', 'Tom &amp; Jerry<br>&nbsp;x')).toBe(false)
    expect(drawnText('<span class="a">x</span>', '<span class="a revealed">x</span>')).toBe(false)
    expect(drawnText('0', '1 250')).toBe(true)
    const el = document.createElement('p')
    el.innerHTML = 'Tom &amp; <b>Jerry</b>'
    expect(drawnText('Tom &#38; <b>Jerry</b>', el)).toBe(false)
  })

  const editable = () => {
    const el = document.createElement('p')
    el.innerHTML = 'Hello <b>world</b>'
    document.body.appendChild(el)
    return el
  }
  const flush = () => new Promise((r) => setTimeout(r, 0))

  it('the user’s typing (between beforeinput and input) is the user’s', async () => {
    const el = editable()
    const w = watchEdit(el)
    window.dispatchEvent(new Event('beforeinput'))
    el.firstChild!.textContent = 'Hi '
    window.dispatchEvent(new Event('input'))
    w.user(() => el.appendChild(document.createElement('i')))
    await flush()
    expect(w.foreign()).toBe(false)
    w.stop()
    el.remove()
  })

  it('a script changing the text during the edit makes it foreign; nothing is saved and the nodes come back', async () => {
    const el = editable()
    const done = vi.fn()
    const s = startTextEdit(el, false, null, () => 'WRITTEN', done, true)
    el.querySelector('b')!.textContent = '1 250' // the design's counter ticks
    await flush()
    s.end(true)
    expect(done).toHaveBeenCalledWith(null, 'Hello world', true)
    expect(el.innerHTML).toBe('Hello <b>world</b>')
    el.remove()
  })

  it('outside Live nothing is watched', async () => {
    const el = editable()
    const done = vi.fn()
    const s = startTextEdit(el, false, null, () => 'WRITTEN', done)
    el.querySelector('b')!.textContent = 'there'
    s.end(true)
    expect(done).toHaveBeenCalledWith('WRITTEN', 'Hello there', false)
    el.remove()
  })
})

describe('Interact mode (K3)', () => {
  it('pointer events pass to the design in Interact mode or with Alt held', () => {
    expect(passesThrough(false, { altKey: false })).toBe(false)
    expect(passesThrough(false, { altKey: true })).toBe(true)
    expect(passesThrough(true, {})).toBe(true)
    // the design's own script clicking its buttons (el.click()): the design's, not a selection
    expect(passesThrough(false, { altKey: false, isTrusted: false })).toBe(true)
    expect(passesThrough(false, { altKey: false, isTrusted: true })).toBe(false)
  })

  it('the toggle ends a text edit and Safe mode turns it off', () => {
    setElState({ editing: 'x' })
    setElState((s) => ({ interact: !s.interact, editing: null }))
    expect([getElState().interact, getElState().editing]).toEqual([true, null])
  })

  it('only links that leave the page are stopped', () => {
    const a = document.createElement('a')
    a.setAttribute('href', '#top')
    expect(leavesFrame(a)).toBe(false)
    a.setAttribute('href', '/about/')
    expect(leavesFrame(a)).toBe(true)
    expect(leavesFrame(null)).toBe(false)
  })
})

describe('what a pointer means in a live page', () => {
  const d = new DOMParser().parseFromString(
    '<body><div data-wbc="1"><ul data-wbc="2"><li data-wbc="3">A</li><li data-wbc="3" class="clone">A</li></ul><span class="dot"></span></div><header data-wbc="h:1"><button>t</button></header><p data-wbc="zz">x</p></body>',
    'text/html',
  )
  const known = (id: string) => ['1', '2', '3'].includes(id)
  it('a script-made element selects its nearest source ancestor', () => {
    expect(sourceTarget(d, d.querySelector('.dot'), known)?.getAttribute('data-wbc')).toBe('1')
  })
  it('a script’s clone of an element (same id) is not that element', () => {
    expect(sourceTarget(d, d.querySelector('.clone'), known)?.getAttribute('data-wbc')).toBe('2')
    expect(sourceTarget(d, d.querySelector('li'), known)?.getAttribute('data-wbc')).toBe('3')
  })
  it('ids the source does not have are skipped; locked parts are known', () => {
    expect(sourceTarget(d, d.querySelector('[data-wbc="zz"]'), known)).toBeNull()
    expect(sourceTarget(d, d.querySelector('header button'), known)?.getAttribute('data-wbc')).toBe('h:1')
  })
})

describe('overlays follow the page', () => {
  it('measure once per frame; busy keeps measuring', () => {
    const q: (() => void)[] = []
    let t = 0
    const m = vi.fn()
    const loop = measureLoop(m, (f) => q.push(f), () => {}, () => t)
    loop.kick()
    loop.kick()
    expect(q.length).toBe(1)
    q.shift()!()
    expect(m).toHaveBeenCalledTimes(1)
    expect(q.length).toBe(0)
    loop.kick(100)
    q.shift()!()
    expect(q.length).toBe(1) // still busy
    t = 200
    q.shift()!()
    expect(q.length).toBe(0)
    expect(m).toHaveBeenCalledTimes(3)
  })

  it('a script changing the DOM, a scroll or an animation kicks a measure', async () => {
    const d = document.implementation.createHTMLDocument('x')
    d.body.innerHTML = '<nav class="menu"></nav>'
    const kick = vi.fn()
    const stop = watchFrame(d, window, kick)
    d.querySelector('nav')!.classList.add('open')
    await new Promise((r) => setTimeout(r, 0))
    expect(kick).toHaveBeenCalled()
    kick.mockClear()
    d.dispatchEvent(new Event('scroll'))
    expect(kick).toHaveBeenCalledWith()
    d.body.dispatchEvent(new Event('animationstart', { bubbles: true }))
    expect(kick).toHaveBeenCalledWith(1000)
    stop()
    kick.mockClear()
    d.dispatchEvent(new Event('scroll'))
    expect(kick).not.toHaveBeenCalled()
  })
})

describe('editing in Live without reloading (K4)', () => {
  /** A stand-in for live.ts applyToLive: patches the element's class and text in place. */
  function patcher(doc: Document, next: Record<string, string>) {
    return () => {
      const m = /class="([^"]*)"[^>]*>([^<]*)</.exec(next.s1)!
      const el = doc.querySelector('[data-wbc="1"]')!
      el.setAttribute('class', m[1])
      el.textContent = m[2]
      return { reload: false }
    }
  }

  it('a patch keeps the running page (the element and the script’s state on it)', () => {
    const doc = document.implementation.createHTMLDocument('x')
    doc.body.innerHTML = '<h2 data-wbc="1" class="a">Old</h2>'
    doc.documentElement.classList.add('dark') // the theme switch the user clicked
    const el = doc.querySelector('[data-wbc="1"]') as HTMLElement & { _state?: number }
    el._state = 42
    const prev = { s1: '<h2 data-wbc="1" class="a">Old</h2>' }
    const next = { s1: '<h2 data-wbc="1" class="b">New</h2>' }
    expect(liveChange(prev, next, {}, patcher(doc, next))).toBe('patched')
    const now = doc.querySelector('[data-wbc="1"]') as HTMLElement & { _state?: number }
    expect(now).toBe(el)
    expect(now._state).toBe(42)
    expect(now.className).toBe('b')
    expect(now.textContent).toBe('New')
    expect(doc.documentElement.classList.contains('dark')).toBe(true)
  })

  it('a text already edited in the frame is not applied again', () => {
    const apply = vi.fn(() => ({ reload: false }))
    const next = { s1: '<p data-wbc="1">Typed</p>' }
    expect(liveChange({ s1: '<p data-wbc="1">x</p>' }, next, { s1: next.s1 }, apply)).toBe('shown')
    expect(apply).not.toHaveBeenCalled()
  })

  it('reloads only when live.ts cannot patch, or the frame is not ready', () => {
    expect(liveChange({ s1: 'a' }, { s1: 'b' }, {}, () => ({ reload: true }))).toBe('reload')
    expect(liveChange({ s1: 'a' }, { s1: 'b' }, {}, null)).toBe('reload')
  })

  it('live.ts’s applyToLive is what the canvas calls (its answer decides)', () => {
    const doc = document.implementation.createHTMLDocument('x')
    const r = applyToLive(doc, { s1: 'a' }, { s1: 'b' })
    expect(liveChange({ s1: 'a' }, { s1: 'b' }, {}, () => r)).toBe(r.reload ? 'reload' : 'patched')
  })
})
