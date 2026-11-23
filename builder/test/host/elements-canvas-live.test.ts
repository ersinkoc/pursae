// @vitest-environment jsdom
/**
 * Element editor canvas, Live by default (plan K1–K5): the mode chosen from the boot object before
 * the first frame, the script-trust prompt and its answers, Interact mode's pass-through, which
 * source element a pointer means when a script made the element, overlay re-measuring, and live
 * edits applied without a reload.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { bootKits, bootTrust, canRunScripts, frameModeOf, initialScripts, pageTrust, sandboxOf, usesScripts } from '../../host/elements/trust'
import { getElState, resetElState, setElState } from '../../host/elements/state'
import { TrustPrompt, promptChoices, revokeTrust, trustAlways, trustLater, trustSession } from '../../host/elements/ScriptsGate'
import { drawnText, leavesFrame, liveChange, measureLoop, passesThrough, sourceTarget, watchFrame } from '../../host/elements/interact'
import { LIVE_SANDBOX, applyToLive } from '../../host/elements/live'

const SCRIPTED = '<div data-wbc="1"><button onclick="toggle()">Dark</button></div><script>init()</script>'

/** A boot object as the server prints it (only the fields the decision reads). */
function boot(o: { trust?: string[]; can?: boolean; unfiltered?: boolean; kits?: (string | undefined)[]; html?: string; header?: string; draftKits?: string[] }) {
  const sec = (kit: string | undefined, html = o.html ?? SCRIPTED) => ({ id: 's' + Math.random(), kind: 'code', content: { html, ...(kit ? { kit } : {}) } })
  return {
    caps: { publish: true, uploadFiles: true, unfilteredHtml: o.unfiltered ?? false, editOthers: true },
    ...(o.can !== undefined ? { canRunScripts: o.can } : {}),
    ...(o.trust ? { scriptTrust: o.trust } : {}),
    codeParts: o.header ? { header: { id: 1, title: 'h', editUrl: '', sections: [sec(o.header, '<header data-wbc="1">H</header>')] }, footer: null } : null,
    document: { doc: { sections: (o.kits ?? ['acme']).map((k) => sec(k)) }, draft: o.draftKits ? { doc: { sections: o.draftKits.map((k) => sec(k)) } } : null },
  } as never
}

const setBoot = (b: unknown) => {
  ;(window as unknown as { __WEB_BUILDER__?: unknown }).__WEB_BUILDER__ = b
  resetElState()
}

afterEach(() => setBoot(undefined))

describe('mode before the first frame (K1, K2)', () => {
  it('reads canRunScripts, else caps.unfilteredHtml; trust only as valid kit slugs', () => {
    expect(canRunScripts(boot({ can: true }))).toBe(true)
    expect(canRunScripts(boot({ can: false, unfiltered: true }))).toBe(false)
    expect(canRunScripts(boot({ unfiltered: true }))).toBe(true)
    expect(canRunScripts(undefined)).toBe(false)
    expect([...bootTrust(boot({ trust: ['acme', '../x', 'B'] }))]).toEqual(['acme'])
  })

  it("the page's kits: draft first, the header and footer parts included", () => {
    expect(bootKits(boot({ kits: ['acme', undefined, 'acme'], header: 'shell' }))).toEqual(['shell', 'acme'])
    expect(bootKits(boot({ kits: ['acme'], draftKits: ['other'] }))).toEqual(['other'])
  })

  it('page trust: blocked / trusted / untrusted / nokit', () => {
    const tr = new Set(['acme'])
    expect(pageTrust(['acme'], tr, false)).toBe('blocked')
    expect(pageTrust(['acme'], tr, true)).toBe('trusted')
    expect(pageTrust(['acme', 'shell'], tr, true)).toBe('untrusted')
    expect(pageTrust([], tr, true)).toBe('nokit')
  })

  it('a trusted page opens Live with the scripts allowed, no prompt', () => {
    setBoot(boot({ can: true, trust: ['acme'], kits: ['acme'] }))
    const s = getElState()
    expect([s.live, s.scripts, s.prompt, s.canRun]).toEqual([true, true, false, true])
    expect(frameModeOf(false, s.live, s.scripts)).toBe('live')
    expect(sandboxOf(s.scripts)).toBe(LIVE_SANDBOX)
  })

  it('an untrusted design never runs before the choice: Safe mode, inert sandbox, prompt', () => {
    setBoot(boot({ can: true, trust: ['other'], kits: ['acme'] }))
    const s = getElState()
    expect([s.live, s.scripts, s.prompt]).toEqual([false, false, true])
    expect(frameModeOf(false, s.live, s.scripts)).toBe('static')
    expect(frameModeOf(true, s.live, s.scripts)).toBe('static') // Preview too
    expect(sandboxOf(s.scripts)).toBe('allow-same-origin')
  })

  it('a header kit not trusted keeps the page in Safe mode', () => {
    setBoot(boot({ can: true, trust: ['acme'], kits: ['acme'], header: 'shell' }))
    expect(getElState().scripts).toBe(false)
  })

  it('without canRunScripts: Safe mode only, no prompt, even with a (stale) trust list', () => {
    setBoot(boot({ can: false, trust: ['acme'], kits: ['acme'] }))
    const s = getElState()
    expect([s.live, s.scripts, s.prompt, s.canRun]).toEqual([false, false, false, false])
  })

  it('no prompt for a design without scripts', () => {
    expect(usesScripts([{ content: { html: '<p>Hi</p>' } }])).toBe(false)
    expect(usesScripts([{ content: { html: '<p onclick="x()">Hi</p>' } }])).toBe(true)
    expect(usesScripts([{ content: { html: '<p></p>', head: '<script src="a.js"></script>' } }])).toBe(true)
    setBoot(boot({ can: true, kits: ['acme'], html: '<p data-wbc="1">Hi</p>' }))
    expect(getElState().prompt).toBe(false)
  })

  it('a page whose sections name no kit: the prompt offers this session only', () => {
    setBoot(boot({ can: true, kits: [undefined] }))
    expect(getElState().prompt).toBe(true)
    expect(promptChoices([], new Set(), true)).toEqual(['session', 'later'])
    expect(promptChoices(['acme'], new Set(), true)).toEqual(['always', 'session', 'later'])
    expect(promptChoices(['acme'], new Set(['acme']), true)).toEqual([])
    expect(promptChoices(['acme'], new Set(), false)).toEqual([])
  })

  it('initialScripts is what the state starts from', () => {
    const i = initialScripts(boot({ can: true, trust: ['acme'] }))
    expect([i.scripts, i.live, i.prompt]).toEqual([true, true, false])
  })
})

describe('the prompt’s answers', () => {
  beforeEach(() => setBoot(boot({ can: true, trust: ['other'], kits: ['acme', 'shell'] })))

  it('Always for this design: every untrusted kit is trusted on the server, then Live', async () => {
    const put = vi.fn(async (slug: string, on: boolean) => (on ? ['acme', 'other', 'shell'].filter((k) => k !== (slug === 'acme' ? 'shell' : '')) : []))
    await trustAlways(['acme', 'shell', 'other'], put)
    expect(put.mock.calls).toEqual([
      ['acme', true],
      ['shell', true],
    ])
    const s = getElState()
    expect([...s.trust].sort()).toEqual(['acme', 'other', 'shell'])
    expect([s.scripts, s.live, s.prompt]).toEqual([true, true, false])
  })

  it('a failed trust request changes nothing (the scripts still do not run)', async () => {
    const put = vi.fn(async () => {
      throw new Error('403')
    })
    await expect(trustAlways(['acme'], put)).rejects.toThrow('403')
    const s = getElState()
    expect([s.scripts, s.live, s.prompt]).toEqual([false, false, true])
  })

  it('This session only: runs now, stores nothing', () => {
    trustSession()
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
    setBoot(boot({ can: true, trust: ['acme', 'other'], kits: ['acme'] }))
    setElState({ interact: true })
    const put = vi.fn(async () => ['other'])
    await revokeTrust(['acme'], put)
    expect(put).toHaveBeenCalledWith('acme', false)
    const s = getElState()
    expect([...s.trust]).toEqual(['other'])
    expect([s.scripts, s.live, s.interact]).toEqual([false, false, false])
  })

  it('the trust persists: a new session with the server’s list opens Live', async () => {
    await trustAlways(['acme', 'shell'], async (slug, on) => (on ? ['acme', 'other', 'shell'] : []))
    setBoot(boot({ can: true, trust: [...getElState().trust], kits: ['acme', 'shell'] }))
    expect([getElState().scripts, getElState().live, getElState().prompt]).toEqual([true, true, false])
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
    const r = render({ kits: ['acme'], trust: new Set(), canRun: true, ...on })
    const dlg = r.el.querySelector('[data-wb-trust-prompt]')!
    expect(dlg.getAttribute('aria-modal')).toBe('false')
    expect(dlg.textContent).toMatch(/admin access|yönetici yetkinizle/) // i18n-ignore
    for (const k of ['always', 'session', 'later'] as const) act(() => (r.el.querySelector(`[data-wb-trust="${k}"]`) as HTMLButtonElement).click())
    expect([on.onAlways.mock.calls.length, on.onSession.mock.calls.length, on.onLater.mock.calls.length]).toEqual([1, 1, 1])
    r.done()
  })

  it('no "Always" without a kit; nothing for a user who cannot run scripts', () => {
    const f = () => {}
    let r = render({ kits: [], trust: new Set(), canRun: true, onAlways: f, onSession: f, onLater: f })
    expect(r.el.querySelector('[data-wb-trust="always"]')).toBeNull()
    expect(r.el.querySelector('[data-wb-trust="session"]')).not.toBeNull()
    r.done()
    r = render({ kits: ['acme'], trust: new Set(), canRun: false, onAlways: f, onSession: f, onLater: f })
    expect(r.el.innerHTML).toBe('')
    r.done()
  })
})

describe('Interact mode (K3)', () => {
  it('pointer events pass to the design in Interact mode or with Alt held', () => {
    expect(passesThrough(false, { altKey: false })).toBe(false)
    expect(passesThrough(false, { altKey: true })).toBe(true)
    expect(passesThrough(true, {})).toBe(true)
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
  it('text a script drew differently is "drawn" (K5)', () => {
    expect(drawnText('Hello  <b>x</b>', 'Hello <b>x</b>')).toBe(false)
    expect(drawnText('0', '1 250')).toBe(true)
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
