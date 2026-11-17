/**
 * Element editor local styles (builder/host/elements/local.ts): the generator against the fixtures
 * Go's LocalCSS is tested with (internal/webbuilder/testdata/local, byte for byte), the variant
 * edits, inheritance, the inline style helpers and local styles following their elements.
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import type { Section } from '@/builder/types'
import { attrSel, classSel, copyEntries, declOK, localCSS, localValue, overrides, parseStyleAttr, rehome, sectionsCSS, setDecls, withoutDecls, type LocalSection, type LocalStyles } from '../../host/elements/local'

const here = path.dirname(fileURLToPath(import.meta.url))
const FIX = path.join(here, '..', '..', '..', 'internal', 'webbuilder', 'testdata', 'local')

describe('localCSS = Go LocalCSS', () => {
  const files = fs.readdirSync(FIX).filter((f) => f.endsWith('.json'))
  it('has the shared fixtures', () => expect(files.length).toBeGreaterThanOrEqual(3))
  for (const f of files) {
    it(f, () => {
      const fx = JSON.parse(fs.readFileSync(path.join(FIX, f), 'utf8'))
      const want = fs.readFileSync(path.join(FIX, f.replace(/\.json$/, '.css')), 'utf8')
      expect(localCSS(fx.local, fx.bps, classSel(fx.post))).toBe(want)
    })
  }
})

describe('declarations', () => {
  it('accepts raw CSS and refuses what would break the stylesheet', () => {
    for (const [k, v] of [['padding-top', '96px'], ['--x', 'calc(1px + 2px)'], ['color', '#fff !important'], ['background', 'url("/a b.png")'], ['-webkit-line-clamp', '3']]) expect(declOK(k, v)).toBe(true)
    for (const [k, v] of [['Color', 'red'], ['color', ''], ['color', 'red;x:y'], ['color', 'a}'], ['top', '1px /*'], ['color', '</style>'], ['width', 'expression(1)'], ['background', 'url(javascript:x)'], ['color', '"open'], ['width', 'calc(1px']])
      expect(declOK(k, v), `${k}: ${v}`).toBe(false)
  })
  it('frame selectors escape the id', () => expect(attrSel('h:')('12')).toBe('[data-wbc="h:12"]'))
  it('forces a state for the element being edited', () => {
    const local: LocalStyles = { '3': { v: [{ bp: '', st: 'hover', p: { color: 'red' } }] } }
    expect(localCSS(local, null, attrSel(), { force: { id: '3', st: 'hover' } })).toBe('[data-wbc="3"]:not(#_):not(#_):not(._wbf){color:red}\n')
    expect(localCSS(local, null, attrSel(), { force: { id: '4', st: 'hover' } })).toContain(':hover{')
  })
})

describe('variants', () => {
  it('sets, merges, removes and prunes', () => {
    let l = setDecls(undefined, '7', 'tablet', '', { 'padding-top': '64px' })
    l = setDecls(l, '7', '', 'hover', { color: 'red' })
    l = setDecls(l, '7', '', '', { 'padding-top': '96px', margin: '0' })
    expect(l['7'].v.map((v) => `${v.bp}|${v.st}`)).toEqual(['|', '|hover', 'tablet|'])
    expect([...overrides(l, '7')].sort()).toEqual(['tablet|', '|', '|hover'].sort())
    l = setDecls(l, '7', '', '', { margin: null, 'padding-top': '' })
    l = setDecls(l, '7', '', 'hover', { color: null })
    expect(l['7'].v).toEqual([{ bp: 'tablet', st: '', p: { 'padding-top': '64px' } }])
    expect(setDecls(l, '7', 'tablet', '', { 'padding-top': null })).toEqual({})
  })
  it('mobile inherits tablet and desktop; a state inherits the normal state', () => {
    const l: LocalStyles = { '1': { v: [{ bp: '', st: '', p: { color: 'red', gap: '1px' } }, { bp: 'tablet', st: '', p: { color: 'blue' } }, { bp: '', st: 'hover', p: { gap: '2px' } }] } }
    expect(localValue(l, '1', 'mobile', '', 'color')).toEqual({ value: 'blue', bp: 'tablet', st: '' })
    expect(localValue(l, '1', '', '', 'color')).toEqual({ value: 'red', bp: '', st: '' })
    expect(localValue(l, '1', 'mobile', 'hover', 'gap')).toEqual({ value: '2px', bp: '', st: 'hover' })
    expect(localValue(l, '1', 'mobile', 'hover', 'color')?.value).toBe('blue')
    expect(localValue(l, '1', '', '', 'width')).toBeNull()
  })
})

describe('inline style attributes', () => {
  it('parses declarations with strings, parentheses and !important', () => {
    const d = parseStyleAttr(`color: red; background:url("a;b.png") ;width:calc(1px + 2px)!important;--x:1`)
    expect(d.map((x) => [x.name, x.value, x.important])).toEqual([
      ['color', 'red', false],
      ['background', 'url("a;b.png")', false],
      ['width', 'calc(1px + 2px)', true],
      ['--x', '1', false],
    ])
  })
  it('moves out only the edited declaration', () => {
    expect(withoutDecls('color: red; padding-top: 4px; margin:0', new Set(['padding-top']))).toBe('color: red; margin:0')
    expect(withoutDecls('padding-top: 4px;', new Set(['padding-top']))).toBeNull()
    expect(withoutDecls('color:red;padding-top:4px', new Set(['padding-top']))).toBe('color:red')
  })
})

describe('local styles follow their elements', () => {
  const sec = (id: string, html: string, local?: LocalStyles): LocalSection => ({ id, kind: 'code', name: id, hidden: false, content: { html }, items: [], style: { hideOn: [] }, local }) as unknown as LocalSection
  const red = { v: [{ bp: '' as const, st: '' as const, p: { color: 'red' } }] }
  it('moves an entry with its element and drops it with a deleted one', () => {
    const a = sec('a', '<div data-wbc="1"></div>', { '1': red, '2': red, '9': red })
    const b = sec('b', '<p data-wbc="3"></p><p data-wbc="2"></p>')
    expect(rehome([a, b])).toEqual({ a: { '1': red }, b: { '2': red } })
    expect(rehome([sec('a', '<div data-wbc="1"></div>', { '1': red })])).toEqual({})
    expect(rehome([sec('a', '<div data-wbc="1"></div>')])).toEqual({})
  })
  it('copies entries to new ids', () => {
    expect(copyEntries({ '1': red, '2': red }, ['1', '5'], ['x1', 'x2'])).toEqual({ x1: red })
  })
  it('prints visible sections with their kits', () => {
    const s = [sec('a', '', { '1': red }), { ...sec('b', '', { '2': red }), hidden: true } as LocalSection]
    ;(s[0].content as Record<string, string>).kit = 'k'
    expect(sectionsCSS(s as unknown as Section[] as LocalSection[], { k: { tablet: 991, mobile: 575 } })).toBe('[data-wbc="1"]:not(#_):not(#_){color:red}\n')
  })
})
