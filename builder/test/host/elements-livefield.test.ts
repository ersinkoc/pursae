// @vitest-environment jsdom
/**
 * Typing into a field changes the canvas while typing (livefield.ts): debounced writes through the
 * element actions (the 'replace' action: the canvas patches the live DOM, no reload), one history
 * entry for a typing session (the store merges the 'field:' keys of patch 0024), Enter / blur
 * commit, Escape takes the session back; Basic mode's text boxes and Studio's fields use it.
 */
import { createElement, act as reactAct, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { BuilderCtx, historyReducer, initHistory, type DocAction } from '@/builder/store'
import type { DocState, Section } from '@/builder/types'
import { act, latest } from '../../host/elements/actions'
import { BasicCode } from '../../host/elements/BasicCode'
import { ContentTab } from '../../host/elements/ContentPanel'
import { LIVE_DELAY, createLiveEdit } from '../../host/elements/livefield'
import { resetElState, setElState } from '../../host/elements/state'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const HTML = `<section data-wbc="a"><h2 data-wbc="e">Plain heading</h2><a data-wbc="f" href="/signup/">Go</a><img data-wbc="g" src="/a.png" alt="Old"></section>`

const doc = (html: string): DocState => ({ sections: [{ id: 's1', kind: 'code', name: 'Section 1', hidden: false, items: [], style: {}, content: { html, head: '' } }] }) as unknown as DocState

/** A builder API over the store's real history reducer. */
function rig(html = HTML) {
  const s = { h: initHistory(doc(html)) }
  const actions: DocAction[] = []
  const sync = () => {
    latest.doc = s.h.entries[s.h.cursor].doc
  }
  const b = {
    get doc() {
      return s.h.entries[s.h.cursor].doc
    },
    get history() {
      return s.h
    },
    dispatch: vi.fn((a: DocAction) => {
      actions.push(a)
      s.h = historyReducer(s.h, a)
      sync()
    }),
    select: vi.fn(),
    notify: vi.fn(),
    setMode: vi.fn(),
  }
  sync()
  return { b, s, actions, html: () => b.doc.sections[0].content.html as string }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  latest.doc = null
  resetElState()
})
afterEach(() => {
  vi.useRealTimers()
})


describe('a typing session (createLiveEdit)', () => {
  const make = (r: ReturnType<typeof rig>, id = 'e') =>
    createLiveEdit<string>({
      builder: () => r.b,
      apply: (v) => act.setInner(r.b as never, id, v),
      original: () => 'Plain heading',
    })

  it('writes after the pause, not before; goes through "replace" (the live frame is patched, never reloaded)', () => {
    const r = rig()
    const e = make(r)
    e.touch(() => 'N')
    vi.advanceTimersByTime(LIVE_DELAY - 1)
    expect(r.actions).toHaveLength(0)
    vi.advanceTimersByTime(1)
    expect(r.actions).toHaveLength(1)
    expect(r.actions[0].type).toBe('replace')
    expect(r.html()).toContain('>N</h2>')
  })

  it('a whole typing burst, with pauses, is ONE history entry; blur adds none', () => {
    const r = rig()
    const e = make(r)
    const before = r.s.h.entries.length
    for (const v of ['N', 'Ne', 'New', 'New h']) {
      e.touch(() => v)
      vi.advanceTimersByTime(LIVE_DELAY)
    }
    // A pause longer than the store's own 1.5 s merge window, then more typing.
    vi.advanceTimersByTime(4000)
    e.touch(() => 'New heading')
    vi.advanceTimersByTime(LIVE_DELAY)
    e.commit()
    expect(r.actions.length).toBeGreaterThanOrEqual(5)
    expect(r.s.h.entries.length).toBe(before + 1)
    expect(r.html()).toContain('>New heading</h2>')
    // One undo goes back to the start.
    r.b.dispatch({ type: 'undo' })
    expect(r.html()).toContain('>Plain heading</h2>')
  })

  it('commit (blur / Enter) writes the draft that has not been written yet, once', () => {
    const r = rig()
    const e = make(r)
    e.touch(() => 'Quick')
    e.commit()
    expect(r.actions).toHaveLength(1)
    vi.advanceTimersByTime(1000)
    expect(r.actions).toHaveLength(1)
    expect(r.html()).toContain('>Quick</h2>')
    expect(e.open()).toBe(false)
  })

  it('the next session is a new entry', () => {
    const r = rig()
    const e = make(r)
    e.touch(() => 'One')
    e.commit()
    e.touch(() => 'Two')
    e.commit()
    expect(r.s.h.entries.length).toBe(3)
  })

  it('Escape takes the whole session back: the history goes to where it began, nothing is left to redo as an edit', () => {
    const r = rig()
    const e = make(r)
    e.touch(() => 'Draft')
    vi.advanceTimersByTime(LIVE_DELAY)
    e.touch(() => 'Draft two')
    vi.advanceTimersByTime(LIVE_DELAY)
    expect(r.html()).toContain('Draft two')
    e.revert()
    expect(r.html()).toBe(HTML)
    expect(r.s.h.cursor).toBe(0)
    // A blur after Escape writes nothing.
    e.commit()
    vi.advanceTimersByTime(1000)
    expect(r.html()).toBe(HTML)
    expect(r.s.h.cursor).toBe(0)
  })

  it('Escape before anything was written just drops the draft (no history change)', () => {
    const r = rig()
    const e = make(r)
    e.touch(() => 'Draft')
    e.revert()
    vi.advanceTimersByTime(1000)
    expect(r.actions).toHaveLength(0)
  })

  it('Escape after something else was done in between writes the original value instead of dropping that work', () => {
    const r = rig()
    const e = make(r)
    e.touch(() => 'Draft')
    vi.advanceTimersByTime(LIVE_DELAY)
    // Another edit lands (another element) before Escape.
    act.setInner(r.b as never, 'f', 'Changed link')
    e.revert()
    expect(r.html()).toContain('>Plain heading</h2>')
    expect(r.html()).toContain('Changed link')
  })

  it('a value the rules refuse is skipped while typing and reported once, by the final write', () => {
    const r = rig()
    const seen: boolean[] = []
    const e = createLiveEdit<string>({
      builder: () => r.b,
      apply: (v, live) => {
        seen.push(live)
        if (v.startsWith('javascript:')) {
          if (!live) r.b.notify('not allowed')
          return false
        }
        return act.setAttrs(r.b as never, 'f', { href: v })
      },
      original: () => '/signup/',
    })
    e.touch(() => 'javascript:x')
    vi.advanceTimersByTime(LIVE_DELAY)
    expect(r.b.notify).not.toHaveBeenCalled()
    expect(r.actions).toHaveLength(0)
    e.commit()
    expect(seen).toEqual([true, false])
    expect(r.b.notify).toHaveBeenCalledTimes(1)
    expect(r.html()).toContain('href="/signup/"')
  })
})

describe('the store merges a field session however slow it is', () => {
  it("'field:' keys merge past the 1.5 s window, other keys do not", () => {
    let h = initHistory(doc(HTML))
    const put = (key: string | undefined, text: string, at: number) => {
      vi.setSystemTime(at)
      h = historyReducer(h, { type: 'replace', doc: doc(HTML.replace('Plain heading', text)), label: 'Text changed', key })
    }
    put('field:1', 'a', 2_000_000)
    put('field:1', 'ab', 2_009_000)
    expect(h.entries).toHaveLength(2)
    put('other:1', 'x', 2_020_000)
    put('other:1', 'xy', 2_030_000)
    expect(h.entries).toHaveLength(4)
    put(undefined, 'z', 2_030_100)
    put(undefined, 'zz', 2_030_200)
    expect(h.entries).toHaveLength(6)
  })
})

/* ---------- Basic mode's boxes ---------- */

function mount(node: ReactNode) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  reactAct(() => root.render(node))
  return { host, unmount: () => reactAct(() => root.unmount()) }
}

/** Types into an input the way a user does: the native setter and an input event (React's onChange). */
function typeInto(el: HTMLInputElement | HTMLTextAreaElement, v: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  reactAct(() => {
    Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, v)
    el.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

describe('Basic mode text boxes', () => {
  function panel() {
    const r = rig()
    // The component re-renders from the document the API holds: the context value changes with it.
    const Wrapper = () => createElement(BuilderCtx.Provider, { value: r.b as never }, createElement(BasicCode, { s: r.b.doc.sections[0] as Section, readOnly: false }))
    const m = mount(createElement(Wrapper))
    const rerender = () => reactAct(() => undefined)
    return { r, m, rerender }
  }
  /** The nth text box of the block field `id` (the UI language does not matter). */
  const box = (host: HTMLElement, id: string, n = 0) => host.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(`[data-wb-basic-field="${id}"] input, [data-wb-basic-field="${id}"] textarea`)[n]

  it('the canvas document follows the typing; one history entry; Enter commits', () => {
    const { r, m } = panel()
    const before = r.s.h.entries.length
    const input = box(m.host, 'e')
    reactAct(() => input.focus())
    typeInto(input, 'Hel')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('>Hel</h2>')
    typeInto(input, 'Hello')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('>Hello</h2>')
    reactAct(() => input.blur())
    expect(r.s.h.entries.length).toBe(before + 1)
    m.unmount()
  })

  it('Escape puts the old text back, on the canvas document and in the box', () => {
    const { r, m } = panel()
    const input = box(m.host, 'e')
    reactAct(() => input.focus())
    typeInto(input, 'Hello')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('>Hello</h2>')
    reactAct(() => {
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(r.html()).toBe(HTML)
    expect(r.s.h.cursor).toBe(0)
    expect(box(m.host, 'e').value).toBe('Plain heading')
    m.unmount()
  })

  it('an address is live and one entry; a half-typed unsafe one is not written', () => {
    const { r, m } = panel()
    const addr = box(m.host, 'f', 1)
    expect(addr).toBeTruthy()
    reactAct(() => addr.focus())
    typeInto(addr, 'javascript:alert(1)')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('href="/signup/"')
    typeInto(addr, '/pricing/')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('href="/pricing/"')
    reactAct(() => addr.blur())
    m.unmount()
  })

  it('alternative text is live', () => {
    const { r, m } = panel()
    const alt = box(m.host, 'g')
    reactAct(() => alt.focus())
    typeInto(alt, 'A new picture')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('alt="A new picture"')
    m.unmount()
  })

  it('an image address is written on blur only (a half-typed one would load)', () => {
    const r = rig(`<video data-wbc="v" src="/a.mp4"></video>`)
    const m = mount(createElement(BuilderCtx.Provider, { value: r.b as never }, createElement(BasicCode, { s: r.b.doc.sections[0] as Section, readOnly: false })))
    const addr = box(m.host, 'v')
    reactAct(() => addr.focus())
    typeInto(addr, '/b.mp4')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY * 4))
    expect(r.actions).toHaveLength(0)
    reactAct(() => addr.blur())
    expect(r.html()).toContain('src="/b.mp4"')
    m.unmount()
  })
})

/* ---------- Studio's Content tab ---------- */

describe("Studio's Content tab", () => {
  function tab(id: string) {
    const r = rig()
    setElState({ sel: id })
    const m = mount(createElement(BuilderCtx.Provider, { value: r.b as never }, createElement(ContentTab, { s: r.b.doc.sections[0] as Section, fallback: createElement('i') })))
    return { r, m }
  }

  it('the link address follows the typing, is one entry, and Escape takes it back', () => {
    const { r, m } = tab('f')
    const before = r.s.h.entries.length
    const input = m.host.querySelector<HTMLInputElement>('input[name="href"]')!
    reactAct(() => input.focus())
    typeInto(input, '/pr')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    typeInto(input, '/pricing/')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('href="/pricing/"')
    expect(r.s.h.entries.length).toBe(before + 1)
    reactAct(() => {
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(r.html()).toBe(HTML)
    m.unmount()
  })

  it('an unsafe address is not written while typing and is refused with a message on blur', () => {
    const { r, m } = tab('f')
    const input = m.host.querySelector<HTMLInputElement>('input[name="href"]')!
    reactAct(() => input.focus())
    typeInto(input, 'javascript:alert(1)')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.b.notify).not.toHaveBeenCalled()
    reactAct(() => input.blur())
    expect(r.b.notify).toHaveBeenCalledTimes(1)
    expect(r.html()).toBe(HTML)
    m.unmount()
  })

  it('the alt text of an image follows the typing', () => {
    const { r, m } = tab('g')
    const input = m.host.querySelector<HTMLInputElement>('input[name="alt"]')!
    reactAct(() => input.focus())
    typeInto(input, 'Sunrise')
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('alt="Sunrise"')
    m.unmount()
  })

  it('the text box (rich text) follows the typing without being redrawn, one entry; Escape restores it', () => {
    const { r, m } = tab('e')
    const before = r.s.h.entries.length
    const box = m.host.querySelector<HTMLElement>('[data-wb-rich]')!
    reactAct(() => box.focus())
    reactAct(() => {
      box.textContent = 'Typed headi'
      box.dispatchEvent(new Event('input', { bubbles: true }))
    })
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('>Typed headi</h2>')
    const node = box.firstChild
    reactAct(() => {
      box.textContent = 'Typed heading'
      box.dispatchEvent(new Event('input', { bubbles: true }))
    })
    reactAct(() => vi.advanceTimersByTime(LIVE_DELAY))
    expect(r.html()).toContain('>Typed heading</h2>')
    expect(node).toBeTruthy()
    expect(r.s.h.entries.length).toBe(before + 1)
    reactAct(() => {
      box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(r.html()).toBe(HTML)
    expect(box.textContent).toBe('Plain heading')
    m.unmount()
  })
})
