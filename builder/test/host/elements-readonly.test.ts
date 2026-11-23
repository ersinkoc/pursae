// @vitest-environment jsdom
/**
 * A user without unfiltered_html: the canvas's element operations on code sections are refused
 * (the server would drop them), not made locally and lost (trust.ts markupReadOnly, actions.commit).
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { commit } from '../../host/elements/actions'

const doc = () => ({ sections: [{ id: 's1', kind: 'code', content: { html: '<p data-wbc="1">a</p>' } }] }) as never

afterEach(() => {
  delete (window as { __WEB_BUILDER__?: unknown }).__WEB_BUILDER__
})

describe('commit and unfiltered_html', () => {
  it('refuses an edit without unfiltered_html and says why', () => {
    ;(window as { __WEB_BUILDER__?: unknown }).__WEB_BUILDER__ = { canRunScripts: false, caps: { unfilteredHtml: false } }
    const b = { doc: doc(), dispatch: vi.fn(), select: vi.fn(), notify: vi.fn() }
    expect(commit(b, 'x', { html: { s1: '<p data-wbc="1">b</p>' } })).toBe(false)
    expect(b.dispatch).not.toHaveBeenCalled()
    expect(b.notify).toHaveBeenCalledTimes(1)
  })
  it('makes it with unfiltered_html', () => {
    ;(window as { __WEB_BUILDER__?: unknown }).__WEB_BUILDER__ = { canRunScripts: true, caps: { unfilteredHtml: true } }
    const b = { doc: doc(), dispatch: vi.fn(), select: vi.fn(), notify: vi.fn() }
    expect(commit(b, 'x', { html: { s1: '<p data-wbc="1">b</p>' } })).toBe(true)
    expect(b.dispatch).toHaveBeenCalledTimes(1)
  })
})
