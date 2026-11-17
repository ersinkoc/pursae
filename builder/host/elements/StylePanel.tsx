/**
 * The element editor's Style and Advanced tabs (plan §4.3a, Faz 3): the selected element's local
 * styles per device × state, through the inspector seam of patch 0020 (ElementsSeam.Tab).
 *
 * The device is the canvas device (switching it here switches the canvas, so the frame's own
 * @media rules show what the device shows); the state (normal, hover, focus, focus-visible,
 * active) is the panel's, and the canvas shows the element in that state while it is edited. A
 * dot on a device or state marks variants that hold declarations.
 *
 * Every control shows the value in force as its placeholder (computed, or the winning rule's in a
 * state), where it comes from (local, a design rule's selector, the inline style, inherited, the
 * default; cascade.ts), a reset when the variant sets it and, for lengths, a unit. A typed value
 * is raw CSS (calc(), var(), clamp() and every unit work); a number alone gets the unit. When the
 * design's winning declaration is !important the local one is written !important too.
 */
import { useMemo, useState, type ReactNode } from 'react'
import { ClipboardPaste, Copy, Eraser, Monitor, Pencil, Plus, RotateCcw, Smartphone, Tablet, X } from 'lucide-react'
import { Ctl, Panel, inputCls } from '@/builder/StudioPanel'
import { useB } from '@/builder/store'
import type { Section } from '@/builder/types'
import { cn } from '@/builder/ui'
import { FontPicker } from '@/builder/FontPicker'
import { fontStack } from '@/builder/fonts'
import { t } from '../i18n'
import { latest, pageOf, style as styleAct } from './actions'
import { sourceOf, elLabel, logicalAliases, type Source } from './cascade'
import { ClassChips, ClassHeader, MatchedRules, classTarget, type ClassTarget } from './ClassPanel'
import { overrideOf, setOverride } from './kitstyle'
import { editTarget, ruleOf, setRuleDecls } from './overrides'
import { frameEl } from './geometry'
import { attr } from './html'
import { kindLabel, kindOf } from './kinds'
import { STATES, declOK, localValue, overrides, parseStyleAttr, variantOf, type Device, type LocalSection, type State } from './local'
import { locate } from './ops'
import { clipboard, deviceOf, frameInfo, getElState, setElState, useElState, useFrameVersion } from './state'

type B = ReturnType<typeof useB>

/* ---------- Control definitions ---------- */

type Kind = 'len' | 'num' | 'color' | 'text' | 'select' | 'font'

interface Prop {
  p: string
  label: string
  kind: Kind
  options?: string[]
  units?: string[]
}

const LEN_UNITS = ['px', '%', 'em', 'rem', 'vw', 'vh']
const len = (p: string, label: string, units = LEN_UNITS): Prop => ({ p, label, kind: 'len', units })
const sel = (p: string, label: string, options: string[]): Prop => ({ p, label, kind: 'select', options })
const txt = (p: string, label: string): Prop => ({ p, label, kind: 'text' })
const col = (p: string, label: string): Prop => ({ p, label, kind: 'color' })
const num = (p: string, label: string): Prop => ({ p, label, kind: 'num' })

const ALIGN = ['', 'flex-start', 'center', 'flex-end', 'stretch', 'baseline', 'start', 'end']
const JUSTIFY = ['', 'flex-start', 'center', 'flex-end', 'space-between', 'space-around', 'space-evenly', 'start', 'end', 'stretch']

const LAYOUT = (): Prop[] => [sel('display', t('Display'), ['', 'block', 'flex', 'grid', 'inline', 'inline-block', 'inline-flex', 'inline-grid', 'contents', 'none'])]
const FLEX = (): Prop[] => [
  sel('flex-direction', t('Direction'), ['', 'row', 'row-reverse', 'column', 'column-reverse']),
  sel('flex-wrap', t('Wrap'), ['', 'nowrap', 'wrap', 'wrap-reverse']),
  sel('justify-content', t('Justify content'), JUSTIFY),
  sel('align-items', t('Align items'), ALIGN),
  sel('align-content', t('Align content'), JUSTIFY),
  len('gap', t('Gap')),
]
const GRID = (): Prop[] => [
  txt('grid-template-columns', t('Columns')),
  txt('grid-template-rows', t('Rows')),
  len('column-gap', t('Column gap')),
  len('row-gap', t('Row gap')),
  sel('place-items', t('Place items'), ['', 'start', 'center', 'end', 'stretch']),
  sel('grid-auto-flow', t('Auto flow'), ['', 'row', 'column', 'dense', 'row dense', 'column dense']),
]
const CHILD = (): Prop[] => [
  num('flex-grow', t('Grow')),
  num('flex-shrink', t('Shrink')),
  len('flex-basis', t('Basis')),
  num('order', t('Order')),
  sel('align-self', t('Align self'), ['', 'auto', 'flex-start', 'center', 'flex-end', 'stretch', 'baseline', 'start', 'end']),
  txt('grid-column', t('Grid column')),
  txt('grid-row', t('Grid row')),
]
const SIZE = (): Prop[] => [
  len('width', t('Width')),
  len('height', t('Height')),
  len('min-width', t('Min width')),
  len('min-height', t('Min height')),
  len('max-width', t('Max width')),
  len('max-height', t('Max height')),
  txt('aspect-ratio', t('Aspect ratio')),
  sel('overflow', t('Overflow'), ['', 'visible', 'hidden', 'clip', 'auto', 'scroll']),
  sel('object-fit', t('Object fit'), ['', 'fill', 'contain', 'cover', 'none', 'scale-down']),
  txt('object-position', t('Object position')),
  sel('box-sizing', t('Box sizing'), ['', 'border-box', 'content-box']),
]
const POSITION = (): Prop[] => [
  sel('position', t('Position'), ['', 'static', 'relative', 'absolute', 'fixed', 'sticky']),
  len('top', t('Top')),
  len('right', t('Right')),
  len('bottom', t('Bottom')),
  len('left', t('Left')),
  num('z-index', t('Z-index')),
]
const TYPO = (): Prop[] => [
  { p: 'font-family', label: t('Font family'), kind: 'font' },
  len('font-size', t('Size'), ['px', 'rem', 'em', '%', 'vw']),
  sel('font-weight', t('Weight'), ['', '100', '200', '300', '400', '500', '600', '700', '800', '900']),
  len('line-height', t('Line height'), ['', 'px', 'em', 'rem', '%']),
  len('letter-spacing', t('Letter spacing'), ['px', 'em', 'rem']),
  col('color', t('Color')),
  sel('text-align', t('Align'), ['', 'left', 'center', 'right', 'justify', 'start', 'end']),
  sel('text-transform', t('Transform'), ['', 'none', 'uppercase', 'lowercase', 'capitalize']),
  sel('text-decoration-line', t('Decoration'), ['', 'none', 'underline', 'line-through', 'overline']),
  sel('font-style', t('Style'), ['', 'normal', 'italic']),
  sel('white-space', t('White space'), ['', 'normal', 'nowrap', 'pre', 'pre-wrap', 'pre-line', 'balance']),
]
const BACKGROUND = (): Prop[] => [
  col('background-color', t('Color')),
  txt('background-image', t('Image or gradient')),
  sel('background-size', t('Size'), ['', 'auto', 'cover', 'contain']),
  txt('background-position', t('Position')),
  sel('background-repeat', t('Repeat'), ['', 'repeat', 'no-repeat', 'repeat-x', 'repeat-y', 'space', 'round']),
  sel('background-attachment', t('Attachment'), ['', 'scroll', 'fixed', 'local']),
]
const SIDES = ['top', 'right', 'bottom', 'left'] as const
const CORNERS = ['top-left', 'top-right', 'bottom-right', 'bottom-left'] as const
const EFFECTS = (): Prop[] => [
  txt('box-shadow', t('Box shadow')),
  num('opacity', t('Opacity')),
  txt('transform', t('Transform')),
  txt('transition', t('Transition')),
  txt('filter', t('Filter')),
  txt('backdrop-filter', t('Backdrop filter')),
  sel('cursor', t('Cursor'), ['', 'auto', 'default', 'pointer', 'text', 'move', 'not-allowed', 'grab']),
  sel('mix-blend-mode', t('Blend mode'), ['', 'normal', 'multiply', 'screen', 'overlay', 'darken', 'lighten']),
]
function stateLabel(st: State): string {
  switch (st) {
    case 'hover':
      return t('Hover')
    case 'focus':
      return t('Focus')
    case 'focus-visible':
      return t('Focus visible')
    case 'active':
      return t('Active')
    default:
      return t('Normal')
  }
}

/** Every property a group control writes (custom properties list the others). */
const KNOWN = new Set<string>([
  ...[...LAYOUT(), ...FLEX(), ...GRID(), ...CHILD(), ...SIZE(), ...POSITION(), ...TYPO(), ...BACKGROUND(), ...EFFECTS()].map((x) => x.p),
  ...SIDES.flatMap((s) => [`margin-${s}`, `padding-${s}`, `border-${s}-width`, `border-${s}-style`, `border-${s}-color`]),
  ...CORNERS.map((c) => `border-${c}-radius`),
])

/* ---------- Context ---------- */

export interface Ctx {
  b: B
  id: string
  sec: LocalSection
  bp: Device
  st: State
  el: HTMLElement | null
  version: number
  /** Editing a design class (every element with it) instead of the element's own style. */
  cls: ClassTarget | null
}

/** The selected element's styling context. */
function useCtx(): Ctx | null {
  const b = useB()
  const es = useElState()
  const version = useFrameVersion()
  const doc = latest.doc ?? b.doc
  if (!es.sel) return null
  const at = locate(pageOf(doc), es.sel)
  if (!at) return null
  const sec = doc.sections.find((s) => s.id === at.sec) as LocalSection | undefined
  if (!sec) return null
  const fd = frameInfo.doc
  const el = fd ? frameEl(fd, es.sel) : null
  const cls = es.classEdit && el ? classTarget(el, es.classEdit, sec) : null
  return { b, id: es.sel, sec, bp: deviceOf(b.device), st: es.styleState, el, version, cls }
}

/** Where a class edit at the context's device and state goes. */
const classAt = (c: Ctx, bp = c.bp, st = c.st) => editTarget(c.cls!.ctx, c.cls!.sel, bp, st, c.cls!.bps)

/** The class edit's value of prop: its own, inherited from desktop / normal, the design rule's. */
function classValue(c: Ctx, prop: string) {
  const cl = c.cls!
  const o = overrideOf(latest.doc ?? c.b.doc, cl.kit)
  const at = classAt(c)
  const own = ruleOf(o, at.ctx, at.sel)?.p[prop]
  let inherited: { value: string; bp: Device; st: State } | null = null
  if (own === undefined) {
    const chain: [Device, State][] = c.st ? [[c.bp, ''], ['', ''], ...(c.bp === 'mobile' ? [['tablet', ''] as [Device, State]] : [])] : c.bp === 'mobile' ? [['tablet', ''], ['', '']] : c.bp ? [['', '']] : []
    for (const [bp, st] of chain) {
      const x = classAt(c, bp, st)
      const v = ruleOf(o, x.ctx, x.sel)?.p[prop]
      if (v !== undefined) {
        inherited = { value: v, bp, st }
        break
      }
    }
  }
  let design = ''
  let important = false
  for (const r of cl.rules) {
    for (const n of [prop, ...logicalAliases(prop)]) {
      const v = r.style.getPropertyValue(n)
      if (v && !design) {
        design = v.trim()
        important = r.style.getPropertyPriority(n) === 'important'
      }
    }
  }
  const src: Source = {
    kind: 'rule',
    decl: { value: own ?? inherited?.value ?? design, important, spec: [0, 1, 0], layer: 0, order: 0, selector: own !== undefined || inherited ? t('Class edit') : design ? cl.label : t('Not set by the class'), origin: 'design' },
    designImportant: important,
  }
  return { own, inherited, src, shown: (own ?? inherited?.value ?? design).replace(/\s*!important$/i, '') }
}

function sourceText(s: Source): string {
  switch (s.kind) {
    case 'local':
      return t('Local style')
    case 'inline':
      return t('Inline style')
    case 'rule':
      return s.decl!.selector.length > 48 ? s.decl!.selector.slice(0, 46) + '…' : s.decl!.selector
    case 'inherited':
      return t('Inherited from {el}', { el: s.from ? elLabel(s.from) : '' })
    default:
      return t('Default')
  }
}

/** The value in force, where it comes from and what the local variant sets. */
function useValue(c: Ctx, prop: string) {
  return useMemo(() => {
    if (c.cls) return classValue(c, prop)
    const own = variantOf(c.sec.local, c.id, c.bp, c.st)[prop]
    const lv = localValue(c.sec.local, c.id, c.bp, c.st, prop)
    let src: Source | null = null
    let shown = ''
    if (c.el) {
      try {
        src = sourceOf(c.el, prop, c.st, c.version)
      } catch {
        src = null
      }
      const win = c.el.ownerDocument.defaultView
      shown = !c.st && win ? win.getComputedStyle(c.el).getPropertyValue(prop).trim() : (src?.decl?.value ?? '')
      if (!shown && win) shown = win.getComputedStyle(c.el).getPropertyValue(prop).trim()
    }
    return { own, inherited: own === undefined && lv ? lv : null, src, shown }
  }, [c.sec.local, c.id, c.bp, c.st, c.el, c.version, prop, c.cls, c.cls && overrideOf(latest.doc ?? c.b.doc, c.cls.kit)]) // eslint-disable-line react-hooks/exhaustive-deps
}

/** Writes one declaration (null: reset) at the context's device and state. */
function write(c: Ctx, prop: string, value: string | null, important: boolean) {
  let v = value
  if (v !== null) {
    v = v.trim()
    if (!v) v = null
    else if (important && !/!\s*important$/i.test(v)) v = `${v} !important`
  }
  if (v !== null && !declOK(prop, v)) {
    c.b.notify(t('This is not a value CSS accepts here: {v}', { v }))
    return
  }
  if (c.cls) {
    const doc = latest.doc ?? c.b.doc
    const at = classAt(c)
    setOverride(c.b, doc, c.cls.kit, setRuleDecls(overrideOf(doc, c.cls.kit), at.ctx, at.sel, { [prop]: v }), v === null ? t('Class value reset') : t('Class changed'))
    return
  }
  // Inline declarations that cover the property (padding for padding-top) move with it.
  const inline: string[] = []
  const style = c.el?.getAttribute('style')
  if (v !== null && style && c.el) {
    const probe = c.el.ownerDocument.createElement('div')
    for (const d of parseStyleAttr(style)) {
      if (d.name === prop) continue
      probe.style.cssText = `${d.name}:${d.value}`
      if (probe.style.getPropertyValue(prop)) inline.push(d.name)
    }
  }
  styleAct.set(c.b, c.id, c.bp, c.st, { [prop]: v }, inline)
}

/* ---------- Controls ---------- */

function Row({ c, prop, label, children, wide }: { c: Ctx; prop: string; label: string; children: (v: ReturnType<typeof useValue>) => ReactNode; wide?: boolean }) {
  const v = useValue(c, prop)
  const set = v.own !== undefined
  return (
    <div className="space-y-0.5" data-wb-style-prop={prop}>
      <div className={cn(wide ? 'space-y-1' : 'flex items-center justify-between gap-2')}>
        <span className={cn('flex min-w-0 items-center gap-1 text-[11.5px]', set ? 'text-accent-soft' : v.inherited ? 'text-amber-200/80' : 'text-ink-400')} title={prop}>
          {set && <span className="size-1.5 shrink-0 rounded-full bg-accent-soft" />}
          <span className="truncate">{label}</span>
        </span>
        <div className="flex min-w-0 items-center gap-1">
          {children(v)}
          <button type="button" aria-label={t('Reset')} title={t('Reset')} disabled={!set} onClick={() => write(c, prop, null, false)} className="grid size-6 shrink-0 place-items-center rounded text-ink-500 hover:text-ink-100 disabled:opacity-0" data-wb-reset={prop}>
            <RotateCcw size={11} />
          </button>
        </div>
      </div>
      {v.src && (
        <div className="truncate text-right text-[10px] text-ink-500" data-wb-source={v.src.kind} title={v.src.decl?.selector ?? ''}>
          {v.inherited ? t('From {where}', { where: whereLabel(v.inherited.bp, v.inherited.st) }) + ' · ' : ''}
          {sourceText(v.src)}
          {v.src.decl?.important ? ' !important' : ''}
        </div>
      )}
    </div>
  )
}

const whereLabel = (bp: Device, st: State) => [bp === '' ? t('Desktop') : bp === 'tablet' ? t('Tablet') : t('Mobile'), st ? stateLabel(st) : ''].filter(Boolean).join(' · ')

/** A text field that commits on Enter or blur. */
function TextIn({ value, placeholder, onCommit, w = 132, mono, list, name }: { value: string; placeholder: string; onCommit: (v: string) => void; w?: number; mono?: boolean; list?: string; name?: string }) {
  const [draft, setDraft] = useState<string | null>(null)
  const shown = draft ?? value
  const done = () => {
    if (draft !== null && draft !== value) onCommit(draft)
    setDraft(null)
  }
  return (
    <input
      value={shown}
      placeholder={placeholder}
      list={list}
      name={name}
      spellCheck={false}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={done}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
        if (e.key === 'Escape') {
          setDraft(null)
          ;(e.target as HTMLInputElement).blur()
        }
      }}
      className={cn(inputCls, 'min-w-0 placeholder:text-ink-600', mono && 'font-mono text-[11px]')}
      style={{ width: w }}
    />
  )
}

const LEN_RE = /^(-?(?:\d+\.?\d*|\.\d+))([a-z%]*)$/i

function LenCtl({ c, prop, label, units }: { c: Ctx; prop: string; label: string; units: string[] }) {
  return (
    <Row c={c} prop={prop} label={label}>
      {(v) => {
        const own = (v.own ?? '').replace(/\s*!important$/i, '')
        const m = LEN_RE.exec(own)
        const unit = m ? m[2] : (LEN_RE.exec(v.shown)?.[2] ?? units[0])
        const imp = !!v.src?.designImportant
        return (
          <>
            <TextIn
              value={own}
              placeholder={v.shown}
              w={92}
              name={prop}
              onCommit={(x) => {
                const n = x.trim()
                write(c, prop, /^-?(?:\d+\.?\d*|\.\d+)$/.test(n) && n !== '0' ? n + (unit || '') : n, imp)
              }}
            />
            <select
              aria-label={t('Unit')}
              value={units.includes(unit) ? unit : ''}
              onChange={(e) => m && write(c, prop, m[1] + e.target.value, imp)}
              className={cn(inputCls, 'w-[46px] appearance-none px-1 text-center text-[10.5px]')}
            >
              {!units.includes(unit) && <option value="">{unit || '—'}</option>}
              {units.map((u) => (
                <option key={u} value={u}>
                  {u || '—'}
                </option>
              ))}
            </select>
          </>
        )
      }}
    </Row>
  )
}

function TextCtl({ c, prop, label, wide, list }: { c: Ctx; prop: string; label: string; wide?: boolean; list?: string }) {
  return (
    <Row c={c} prop={prop} label={label} wide={wide}>
      {(v) => <TextIn value={(v.own ?? '').replace(/\s*!important$/i, '')} placeholder={v.shown} w={wide ? 196 : 138} mono list={list} name={prop} onCommit={(x) => write(c, prop, x, !!v.src?.designImportant)} />}
    </Row>
  )
}

function SelectCtl({ c, prop, label, options }: { c: Ctx; prop: string; label: string; options: string[] }) {
  return (
    <Row c={c} prop={prop} label={label}>
      {(v) => {
        const own = (v.own ?? '').replace(/\s*!important$/i, '')
        return (
          <select
            name={prop}
            value={options.includes(own) ? own : own ? '__other' : ''}
            onChange={(e) => write(c, prop, e.target.value || null, !!v.src?.designImportant)}
            className={cn(inputCls, 'w-[138px] appearance-none')}
          >
            <option value="">{v.shown ? `${v.shown}` : '—'}</option>
            {own && !options.includes(own) && <option value="__other">{own}</option>}
            {options.filter(Boolean).map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
        )
      }}
    </Row>
  )
}

/** #rgb / #rrggbb / rgb() of a value, for the swatch; null when it is not a plain color. */
function toHex(v: string): string | null {
  const s = v.trim()
  if (/^#[0-9a-f]{6}$/i.test(s)) return s
  if (/^#[0-9a-f]{3}$/i.test(s)) return '#' + s.slice(1).replace(/./g, (x) => x + x)
  const m = /^rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)/i.exec(s)
  if (m) return '#' + [m[1], m[2], m[3]].map((x) => Math.min(255, Number(x)).toString(16).padStart(2, '0')).join('')
  return null
}

function ColorCtl({ c, prop, label }: { c: Ctx; prop: string; label: string }) {
  return (
    <Row c={c} prop={prop} label={label}>
      {(v) => {
        const own = (v.own ?? '').replace(/\s*!important$/i, '')
        const swatch = own || v.shown
        return (
          <>
            <label className="relative size-7 shrink-0 cursor-pointer overflow-hidden rounded-[5px] ring-1 ring-inset ring-white/15" style={{ background: swatch || 'transparent' }} title={t('Pick a color')}>
              <input type="color" value={toHex(swatch) ?? '#000000'} onChange={(e) => write(c, prop, e.target.value, !!v.src?.designImportant)} className="absolute inset-0 cursor-pointer opacity-0" />
            </label>
            <TextIn value={own} placeholder={v.shown} w={100} mono name={prop} onCommit={(x) => write(c, prop, x, !!v.src?.designImportant)} />
          </>
        )
      }}
    </Row>
  )
}

function NumCtl({ c, prop, label }: { c: Ctx; prop: string; label: string }) {
  return (
    <Row c={c} prop={prop} label={label}>
      {(v) => <TextIn value={(v.own ?? '').replace(/\s*!important$/i, '')} placeholder={v.shown} w={92} name={prop} onCommit={(x) => write(c, prop, x, !!v.src?.designImportant)} />}
    </Row>
  )
}

/** Font families the design uses (its @font-face rules and font-family declarations), for the list. */
function designFonts(doc: Document | null): string[] {
  const out = new Set<string>()
  if (!doc) return []
  const walk = (rules: CSSRuleList) => {
    for (const r of Array.from(rules)) {
      if (out.size > 60) return
      const st = (r as CSSStyleRule).style
      const fam = st?.getPropertyValue('font-family')
      if (fam && !fam.includes('var(')) out.add(fam.trim())
      if ('cssRules' in r && (r as CSSGroupingRule).cssRules) walk((r as CSSGroupingRule).cssRules)
    }
  }
  for (const s of Array.from(doc.styleSheets)) {
    try {
      walk(s.cssRules)
    } catch {
      /* another origin */
    }
  }
  return [...out]
}

function FontCtl({ c, prop, label }: { c: Ctx; prop: string; label: string }) {
  const fonts = useMemo(() => designFonts(frameInfo.doc), [c.version]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <>
      <datalist id="wb-style-fonts">
        {[...fonts, 'system-ui, sans-serif', 'Georgia, serif', 'ui-monospace, monospace'].map((f) => (
          <option key={f} value={f} />
        ))}
      </datalist>
      <TextCtl c={c} prop={prop} label={label} wide list="wb-style-fonts" />
      <div data-wb-font-library="">
        <FontPicker value={undefined} defaultLabel={t('Font library…')} onChange={(v) => v && write(c, prop, fontStack(v) ?? v, false)} />
      </div>
    </>
  )
}

function Ctl1({ c, d }: { c: Ctx; d: Prop }) {
  switch (d.kind) {
    case 'len':
      return <LenCtl c={c} prop={d.p} label={d.label} units={d.units ?? LEN_UNITS} />
    case 'select':
      return <SelectCtl c={c} prop={d.p} label={d.label} options={d.options ?? []} />
    case 'color':
      return <ColorCtl c={c} prop={d.p} label={d.label} />
    case 'num':
      return <NumCtl c={c} prop={d.p} label={d.label} />
    case 'font':
      return <FontCtl c={c} prop={d.p} label={d.label} />
    default:
      return <TextCtl c={c} prop={d.p} label={d.label} wide={d.p.startsWith('grid-template') || d.p === 'transition' || d.p === 'box-shadow' || d.p === 'background-image'} />
  }
}

const Group = ({ c, title, props, open = true }: { c: Ctx; title: string; props: Prop[]; open?: boolean }) => (
  <Panel title={title} open={open}>
    {props.map((d) => (
      <Ctl1 key={d.p} c={c} d={d} />
    ))}
  </Panel>
)

/** Margin and padding as a box model: four sides each (raw CSS values, a number gets px). */
function BoxModel({ c }: { c: Ctx }) {
  const side = (kind: 'margin' | 'padding', s: (typeof SIDES)[number], cls: string) => <BoxIn key={kind + s} c={c} prop={`${kind}-${s}`} className={cls} />
  return (
    <Panel title={t('Spacing')}>
      <div className="relative rounded-md border border-dashed border-white/15 p-1.5 text-[10px] text-ink-500" data-wb-boxmodel="">
        <span className="absolute left-1.5 top-1">{t('Margin')}</span>
        <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-1">
          <div />
          {side('margin', 'top', '')}
          <div />
          {side('margin', 'left', '')}
          <div className="relative rounded border border-white/15 bg-white/[0.03] p-1.5">
            <span className="absolute left-1.5 top-1">{t('Padding')}</span>
            <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-1 pt-3">
              <div />
              {side('padding', 'top', '')}
              <div />
              {side('padding', 'left', '')}
              <div className="h-5 w-8 rounded-sm bg-white/10" />
              {side('padding', 'right', '')}
              <div />
              {side('padding', 'bottom', '')}
              <div />
            </div>
          </div>
          {side('margin', 'right', '')}
          <div />
          {side('margin', 'bottom', '')}
          <div />
        </div>
      </div>
    </Panel>
  )
}

function BoxIn({ c, prop, className }: { c: Ctx; prop: string; className: string }) {
  const v = useValue(c, prop)
  const own = (v.own ?? '').replace(/\s*!important$/i, '')
  const tip = `${prop}: ${own || v.shown}${v.src ? ' · ' + sourceText(v.src) : ''}`
  return (
    <div className={cn('flex justify-center', className)} title={tip} data-wb-style-prop={prop} data-wb-source={v.src?.kind ?? ''}>
      <input
        key={own}
        defaultValue={own}
        placeholder={v.shown.replace(/px$/, '')}
        aria-label={prop}
        name={prop}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
        }}
        onBlur={(e) => {
          const x = e.target.value.trim()
          if (x === own) return
          write(c, prop, /^-?(?:\d+\.?\d*|\.\d+)$/.test(x) && x !== '0' ? x + 'px' : x || null, !!v.src?.designImportant)
        }}
        className={cn('h-6 w-[52px] rounded border bg-ink-850 px-1 text-center text-[11px] outline-none placeholder:text-ink-600 focus:border-accent/70', own ? 'border-accent/60 text-accent-soft' : 'border-white/[0.08] text-ink-100')}
      />
    </div>
  )
}

function BorderGroup({ c }: { c: Ctx }) {
  const [side, setSide] = useState<(typeof SIDES)[number]>('top')
  return (
    <Panel title={t('Border')} open={false}>
      <div className="flex gap-1">
        {SIDES.map((s) => (
          <button key={s} type="button" onClick={() => setSide(s)} className={cn('h-6 flex-1 rounded text-[11px]', side === s ? 'bg-ink-600 text-white' : 'text-ink-400 hover:text-ink-200')}>
            {s === 'top' ? t('Top') : s === 'right' ? t('Right') : s === 'bottom' ? t('Bottom') : t('Left')}
          </button>
        ))}
      </div>
      <LenCtl c={c} prop={`border-${side}-width`} label={t('Width')} units={['px', 'em', 'rem']} />
      <SelectCtl c={c} prop={`border-${side}-style`} label={t('Style')} options={['', 'none', 'solid', 'dashed', 'dotted', 'double']} />
      <ColorCtl c={c} prop={`border-${side}-color`} label={t('Color')} />
      <div className="pt-1 text-[11px] text-ink-500">{t('Corner radius')}</div>
      {CORNERS.map((k) => (
        <LenCtl key={k} c={c} prop={`border-${k}-radius`} label={k === 'top-left' ? t('Top left') : k === 'top-right' ? t('Top right') : k === 'bottom-right' ? t('Bottom right') : t('Bottom left')} units={['px', '%', 'em', 'rem']} />
      ))}
    </Panel>
  )
}

/* ---------- Header: device and state ---------- */

function Switches({ c }: { c: Ctx }) {
  const has = c.cls ? new Set<string>() : overrides(c.sec.local, c.id)
  const devHas = (bp: Device) => [...has].some((k) => k.startsWith(bp + '|'))
  const stHas = (st: State) => [...has].some((k) => k === `${c.bp}|${st}`)
  const dev: { d: 'desktop' | 'tablet' | 'mobile'; bp: Device; icon: typeof Monitor; label: string }[] = [
    { d: 'desktop', bp: '', icon: Monitor, label: t('Desktop') },
    { d: 'tablet', bp: 'tablet', icon: Tablet, label: t('Tablet') },
    { d: 'mobile', bp: 'mobile', icon: Smartphone, label: t('Mobile') },
  ]
  return (
    <div className="space-y-2 border-b border-white/[0.07] px-4 py-3" data-wb-style-switches="">
      <div className="flex rounded-[6px] border border-white/[0.08] bg-ink-850 p-0.5">
        {dev.map((x) => (
          <button
            key={x.d}
            type="button"
            title={x.label}
            aria-pressed={c.b.device === x.d}
            data-wb-style-device={x.d}
            onClick={() => c.b.setDevice(x.d)}
            className={cn('relative flex h-7 flex-1 items-center justify-center gap-1 rounded-[4px] text-[11px]', c.b.device === x.d ? 'bg-ink-600 text-white' : 'text-ink-400 hover:text-ink-200')}
          >
            <x.icon size={13} /> {x.label}
            {devHas(x.bp) && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-accent-soft" data-wb-dot="" />}
          </button>
        ))}
      </div>
      <div className="flex flex-wrap gap-1">
        {STATES.map((st) => (
          <button
            key={st || 'normal'}
            type="button"
            aria-pressed={c.st === st}
            data-wb-style-state={st || 'normal'}
            onClick={() => setElState({ styleState: st })}
            className={cn('relative h-6 rounded-[4px] px-2 text-[11px]', c.st === st ? 'bg-accent/25 text-accent-soft' : 'bg-white/[0.04] text-ink-400 hover:text-ink-200')}
          >
            {stateLabel(st)}
            {stHas(st) && <span className="absolute -right-0.5 -top-0.5 size-1.5 rounded-full bg-accent-soft" data-wb-dot="" />}
          </button>
        ))}
      </div>
      <div className="text-[10.5px] text-ink-500">
        {c.bp === '' && !c.st ? t('Applies to every device unless a device has its own value.') : t('Applies to: {where} (and smaller devices without their own value).', { where: whereLabel(c.bp, c.st) })}
      </div>
    </div>
  )
}

/* ---------- Advanced: custom properties, copy / paste ---------- */

function CustomProps({ c }: { c: Ctx }) {
  const own = variantOf(c.sec.local, c.id, c.bp, c.st)
  const rows = Object.keys(own).filter((k) => !KNOWN.has(k))
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const add = () => {
    const n = name.trim().toLowerCase()
    if (!n) return
    write(c, n, value, false)
    setName('')
    setValue('')
  }
  return (
    <Panel title={t('Custom properties')}>
      <div className="text-[10.5px] text-ink-500">{t('Any CSS property for this device and state, e.g. --brand: #0f172a or scroll-margin-top: 80px.')}</div>
      {rows.map((k) => (
        <div key={k} className="flex items-center gap-1" data-wb-custom-prop={k}>
          <span className="w-[96px] truncate font-mono text-[11px] text-accent-soft" title={k}>
            {k}
          </span>
          <TextIn value={own[k]} placeholder="" w={124} mono onCommit={(x) => write(c, k, x, false)} />
          <button type="button" aria-label={t('Remove')} onClick={() => write(c, k, null, false)} className="grid size-6 place-items-center rounded text-ink-500 hover:text-red-300">
            <X size={12} />
          </button>
        </div>
      ))}
      <div className="flex items-center gap-1">
        <input value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.stopPropagation()} placeholder={t('property')} spellCheck={false} name="wb-custom-name" className={cn(inputCls, 'w-[96px] font-mono text-[11px]')} />
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            e.stopPropagation()
            if (e.key === 'Enter') add()
          }}
          placeholder={t('value')}
          spellCheck={false}
          name="wb-custom-value"
          className={cn(inputCls, 'w-[124px] font-mono text-[11px]')}
        />
        <button type="button" aria-label={t('Add')} onClick={add} className="grid size-6 place-items-center rounded text-ink-400 hover:text-white">
          <Plus size={13} />
        </button>
      </div>
    </Panel>
  )
}

function StyleTools({ c }: { c: Ctx }) {
  const at = locate(pageOf(latest.doc ?? c.b.doc), c.id)
  const inline = at ? attr(at.el, 'style') : null
  const classes = at ? (attr(at.el, 'class') ?? '').split(/\s+/).filter(Boolean) : []
  return (
    <Panel title={t('Style of this element')}>
      <div className="flex flex-wrap gap-1">
        <button type="button" onClick={() => styleAct.copy(c.b, c.id)} className="flex h-7 items-center gap-1 rounded-[5px] bg-white/[0.06] px-2 text-[11.5px] text-ink-200 hover:bg-white/10">
          <Copy size={12} /> {t('Copy style')}
        </button>
        <button type="button" disabled={!clipboard.style} onClick={() => styleAct.paste(c.b, c.id)} className="flex h-7 items-center gap-1 rounded-[5px] bg-white/[0.06] px-2 text-[11.5px] text-ink-200 hover:bg-white/10 disabled:opacity-40">
          <ClipboardPaste size={12} /> {t('Paste style')}
        </button>
        <button type="button" disabled={!c.sec.local?.[c.id]} onClick={() => styleAct.clear(c.b, c.id)} className="flex h-7 items-center gap-1 rounded-[5px] bg-white/[0.06] px-2 text-[11.5px] text-ink-200 hover:bg-white/10 disabled:opacity-40">
          <Eraser size={12} /> {t('Clear style')}
        </button>
      </div>
      {classes.length > 0 && (
        <Ctl label={t('Classes')} stack>
          <div className="flex flex-wrap gap-1">
            {classes.map((k) => (
              <span key={k} className="rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[10.5px] text-ink-300">
                .{k}
              </span>
            ))}
          </div>
        </Ctl>
      )}
      {inline && (
        <Ctl label={t('Inline style')} stack>
          <div className="break-all rounded bg-white/[0.04] p-1.5 font-mono text-[10.5px] text-ink-300">{inline}</div>
          <div className="text-[10.5px] text-ink-500">{t('Editing a property set here moves that declaration into the element’s local style.')}</div>
        </Ctl>
      )}
    </Panel>
  )
}

/* ---------- The tabs ---------- */

export function StyleTab({ s, tab }: { s: Section; tab: 'style' | 'advanced' }) {
  const c = useCtx()
  if (!c || c.sec.id !== s.id) return <div className="px-4 py-6 text-[12px] text-ink-400">{t('Select an element on the canvas to style it.')}</div>
  const display = c.el ? (c.el.ownerDocument.defaultView?.getComputedStyle(c.el).display ?? '') : ''
  const parentDisplay = c.el?.parentElement ? (c.el.ownerDocument.defaultView?.getComputedStyle(c.el.parentElement).display ?? '') : ''
  const flex = /flex/.test(display)
  const grid = /grid/.test(display)
  const child = /flex|grid/.test(parentDisplay)
  return (
    <div data-wb-style-panel={tab} data-wb-style-el={c.id} data-wb-class-edit={c.cls?.cls ?? undefined}>
      <Switches c={c} />
      {c.cls ? <ClassHeader c={c} target={c.cls} /> : tab === 'style' && <ClassChips c={c} />}
      {tab === 'style' ? (
        <>
          <Panel title={t('Layout')}>
            {LAYOUT().map((d) => (
              <Ctl1 key={d.p} c={c} d={d} />
            ))}
            {flex && FLEX().map((d) => <Ctl1 key={d.p} c={c} d={d} />)}
            {grid && GRID().map((d) => <Ctl1 key={d.p} c={c} d={d} />)}
            {grid && <Ctl1 c={c} d={len('gap', t('Gap'))} />}
          </Panel>
          {child && <Group c={c} title={/grid/.test(parentDisplay) ? t('Grid item') : t('Flex item')} props={CHILD()} open={false} />}
          <BoxModel c={c} />
          <Group c={c} title={t('Size')} props={SIZE()} open={false} />
          <Group c={c} title={t('Position')} props={POSITION()} open={false} />
          <Group c={c} title={t('Typography')} props={TYPO()} />
          <Group c={c} title={t('Background')} props={BACKGROUND()} open={false} />
          <BorderGroup c={c} />
          <Group c={c} title={t('Effects')} props={EFFECTS()} open={false} />
        </>
      ) : (
        <>
          {!c.cls && <CustomProps c={c} />}
          {!c.cls && <StyleTools c={c} />}
          <MatchedRules c={c} />
        </>
      )}
    </div>
  )
}

/** The inspector's title for a code section of the element editor: the selected element's type. */
export function styleTitle(s: Section): string | null {
  const doc = latest.doc
  const id = getElState().sel
  if (!doc || !id) return null
  const at = locate(pageOf(doc), id)
  if (!at || at.sec !== s.id) return null
  const fd = frameInfo.doc
  const el = fd ? frameEl(fd, id) : null
  const display = el ? (el.ownerDocument.defaultView?.getComputedStyle(el).display ?? undefined) : undefined
  return t('Edit {v}', { v: kindLabel(kindOf(at.tree, at.el, display)) })
}
