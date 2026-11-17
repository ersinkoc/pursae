/**
 * Content bindings of code sections in the element editor (builder/host/elements/bind.ts): the
 * content.binds JSON, what an element can be bound to, a form's inputs.
 */
import { describe, expect, it } from 'vitest'
import { bindTarget, defaultFormField, kindsFor, newBind, parseBinds, stringifyBinds, withBind } from '../../host/elements/bind'

const html =
  '<nav data-wbc="1"><ul data-wbc="2"><li data-wbc="3"><a data-wbc="4" href="/">Home</a></li><li data-wbc="5"><a data-wbc="6" href="/b/">B</a>' +
  '<ul data-wbc="7"><li data-wbc="8"><a data-wbc="9" href="/c/">C</a></li></ul></li></ul></nav>' +
  '<section data-wbc="10"><ul data-wbc="11"><li data-wbc="12"><h3 data-wbc="13">One</h3></li><li data-wbc="14"><h3 data-wbc="15">Two</h3></li></ul></section>' +
  '<form data-wbc="20" id="f"><label data-wbc="21" for="n">Name <span data-wbc="22">*</span></label><input data-wbc="23" id="n" name="name">' +
  '<input data-wbc="24" name="company" placeholder="Company"><select data-wbc="25" name="subject"></select><input data-wbc="26" type="hidden" name="x">' +
  '<button data-wbc="27" type="submit">Send</button></form><div data-wbc="28" id="done" class="hidden">Thanks</div>'

describe('content bindings', () => {
  it('reads and writes content.binds as JSON (keys are element ids: numeric order)', () => {
    let b = parseBinds('{"11":{"kind":"loop"},"2":{"kind":"menu","location":"primary"},"x":{"kind":"menu"},"3":"bad"}')
    expect(Object.keys(b).sort()).toEqual(['11', '2'])
    b = withBind(b, '7', { kind: 'submenu' })
    expect(stringifyBinds(b)).toBe('{"2":{"kind":"menu","location":"primary"},"7":{"kind":"submenu"},"11":{"kind":"loop"}}')
    expect(stringifyBinds(withBind(withBind(withBind(b, '11', null), '2', null), '7', null))).toBe('')
    expect(parseBinds('not json')).toEqual({})
  })

  it('offers the kinds an element can take', () => {
    const binds = parseBinds('{"2":{"kind":"menu","location":"primary"},"11":{"kind":"loop"}}')
    expect(kindsFor(bindTarget(html, '2', {})!)).toEqual(['menu', 'loop', 'field'])
    const sub = bindTarget(html, '7', binds)!
    expect(sub.menu).toBe('2')
    expect(kindsFor(sub)).toContain('submenu')
    // An element of a loop's first card is a field of that card's post.
    const card = bindTarget(html, '13', binds)!
    expect(card.loop).toBe('11')
    expect(kindsFor(card)).toEqual(['field'])
    expect(bindTarget(html, '15', binds)!.loop).toBeNull()
    expect(kindsFor(bindTarget(html, '20', binds)!)).toEqual(['form'])
    expect(newBind('field', bindTarget(html, '4', binds)!)).toEqual({ kind: 'field', field: 'url' })
    expect(newBind('loop', bindTarget(html, '11', binds)!).query?.perPage).toBe('2')
  })

  it('lists a form inputs and the message candidates', () => {
    const t = bindTarget(html, '20', {})!
    expect(t.inputs).toEqual([
      { name: 'name', type: 'text', label: 'Name' },
      { name: 'company', type: 'text', label: 'Company' },
      { name: 'subject', type: 'select', label: 'subject' },
    ])
    expect(t.messages).toEqual([{ id: '28', label: '#done' }])
    expect(defaultFormField('Subject')).toBe('subject')
    expect(defaultFormField('consent')).toBe('field_consent')
    expect(defaultFormField('kvkk-onay')).toBe('field_kvkk_onay')
  })
})
