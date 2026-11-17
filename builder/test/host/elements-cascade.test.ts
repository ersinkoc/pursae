/**
 * Element editor Style panel: the selector arithmetic of the cascade-source detection
 * (builder/host/elements/cascade.ts). The DOM walk runs in the browser (end-to-end check).
 */
import { describe, expect, it } from 'vitest'
import { beats, specificity, splitList, stripState, winner, type Decl } from '../../host/elements/cascade'

describe('specificity', () => {
  it.each([
    ['*', [0, 0, 0]],
    ['h1', [0, 0, 1]],
    ['.a', [0, 1, 0]],
    ['#x .a > p', [1, 1, 1]],
    ['a[href^="http"]:hover', [0, 2, 1]],
    ['.hover\\:bg-red-500:hover', [0, 2, 0]],
    ['.md\\:px-4', [0, 1, 0]],
    ['p::before', [0, 0, 2]],
    ['p:before', [0, 0, 2]],
    [':where(#a .b) p', [0, 0, 1]],
    [':is(#a, .b) p', [1, 0, 1]],
    [':not(.a, .b .c)', [0, 2, 0]],
    ['li:nth-child(2n+1 of .x)', [0, 2, 1]],
    ['.e5-1:not(#_):not(#_)', [2, 1, 0]],
    ['[data-wbc="12"]:not(#_):not(#_):hover', [2, 2, 0]],
    ['.space-y-4 > :not(:last-child)', [0, 2, 0]],
  ])('%s', (sel, want) => expect(specificity(sel)).toEqual(want))
  it('splits lists outside parentheses and strings', () => {
    expect(splitList('.a, :is(.b, .c) d, [x=","] ')).toEqual(['.a', ':is(.b, .c) d', '[x=","]'])
  })
})

describe('states', () => {
  it('drops the pseudo-classes a state implies', () => {
    expect(stripState('.btn:hover', 'hover')).toBe('.btn')
    expect(stripState('.btn:hover .icon', 'hover')).toBe('.btn .icon')
    expect(stripState('.hover\\:x:hover', 'hover')).toBe('.hover\\:x')
    expect(stripState('a:focus-visible', 'focus')).toBe('a:focus-visible')
    expect(stripState('a:focus-visible', 'focus-visible')).toBe('a')
    expect(stripState('a:focus', 'focus-visible')).toBe('a')
    expect(stripState('.a:active', 'active')).toBe('.a')
    expect(stripState('.a :hover', 'hover')).toBe('.a *')
    expect(stripState('.a:hover', '')).toBe('.a:hover')
  })
})

describe('cascade order', () => {
  const d = (p: Partial<Decl>): Decl => ({ value: 'x', important: false, spec: [0, 1, 0], layer: Infinity, order: 1, selector: '.a', origin: 'design', ...p })
  it('importance, layers, specificity, order, inline', () => {
    expect(beats(d({ important: true, spec: [0, 0, 1] }), d({ spec: [9, 0, 0] }))).toBe(true)
    expect(beats(d({ layer: Infinity }), d({ layer: 3, spec: [5, 0, 0] }))).toBe(true) // unlayered beats layered
    expect(beats(d({ important: true, layer: 0 }), d({ important: true, layer: Infinity }))).toBe(true) // reversed for !important
    expect(beats(d({ spec: [0, 2, 0], order: 1 }), d({ spec: [0, 1, 0], order: 9 }))).toBe(true)
    expect(beats(d({ order: 9 }), d({ order: 1 }))).toBe(true)
    expect(beats(d({ origin: 'inline', spec: [1, 0, 0] }), d({ spec: [3, 0, 0], origin: 'local' }))).toBe(true)
    expect(beats(d({ origin: 'local', spec: [2, 1, 0], important: true }), d({ origin: 'inline' }))).toBe(true)
    expect(winner([d({ value: 'a', order: 1 }), d({ value: 'b', order: 2 }), d({ value: 'c', spec: [0, 0, 1], order: 3 })])?.value).toBe('b')
  })
})
