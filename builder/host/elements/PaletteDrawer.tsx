/**
 * The palette drawer of the element editor (bottom bar › Add / Variables), over the canvas's left
 * edge:
 *
 *  - Elements (Faz 4.1): new elements as plain semantic HTML with a local-style preset (palette.ts);
 *  - From this design (Faz 4.2): the design's repeated class signatures as ready elements, each
 *    with a live thumbnail drawn with the design's own stylesheets (miner.ts, kitstyle.ts);
 *  - Variables (Faz 5.7): the design's custom properties on :root / html, grouped, edited into the
 *    kit's override stylesheet (live in the frame, site-wide on publish), reset per variable.
 *
 * An item is dragged onto the canvas (the drop indicator shows where it goes) or clicked: then it
 * goes after the selected element (into it when it is an empty container), else at the end of the
 * selected section's content. Each insert is one history entry.
 */
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  CheckSquare,
  Code2,
  Columns3,
  Film,
  Heading,
  Image as ImageIcon,
  LayoutGrid,
  LayoutTemplate,
  Link2,
  List,
  ListChecks,
  Minus,
  Pilcrow,
  RectangleHorizontal,
  RotateCcw,
  Search,
  Send,
  Shapes,
  Square,
  TextCursorInput,
  Type,
  X,
  type LucideIcon,
} from 'lucide-react'
import { useB } from '@/builder/store'
import type { Section } from '@/builder/types'
import { cn } from '@/builder/ui'
import { inputCls } from '@/builder/StudioPanel'
import { t } from '../i18n'
import { act, latest } from './actions'
import { mergeHeads } from './frame'
import { THUMB_SANDBOX, thumbDoc } from './thumb'
import { hexOf, rootVariables, type VarDef, type VarGroup } from './cssom'
import { kitsOf, loadCatalog, overrideOf, setOverride, useKitStore } from './kitstyle'
import type { LocalStyles } from './local'
import { KIND_ORDER, type Sample, type SampleKind } from './miner'
import { overrideCSS, ruleOf, setRuleDecls } from './overrides'
import { paletteItems, type PaletteId, type PaletteItem } from './palette'
import { frameInfo, getElState, setElState, useElState, useFrameVersion } from './state'

export interface NewElement {
  html: string
  local: LocalStyles | null
  label: string
}

const ICONS: Record<PaletteId, LucideIcon> = {
  section: LayoutTemplate,
  flex: Columns3,
  grid: LayoutGrid,
  div: Square,
  heading: Heading,
  text: Pilcrow,
  button: RectangleHorizontal,
  link: Link2,
  image: ImageIcon,
  icon: Shapes,
  video: Film,
  divider: Minus,
  list: List,
  input: TextCursorInput,
  textarea: Type,
  select: ListChecks,
  checkbox: CheckSquare,
  submit: Send,
  html: Code2,
}

function itemLabel(id: PaletteId): string {
  switch (id) {
    case 'section':
      return t('Section')
    case 'flex':
      return t('Flexbox')
    case 'grid':
      return t('Grid')
    case 'div':
      return t('Div')
    case 'heading':
      return t('Heading')
    case 'text':
      return t('Text')
    case 'button':
      return t('Button')
    case 'link':
      return t('Link')
    case 'image':
      return t('Image')
    case 'icon':
      return t('Icon')
    case 'video':
      return t('Video')
    case 'divider':
      return t('Divider')
    case 'list':
      return t('List')
    case 'input':
      return t('Input')
    case 'textarea':
      return t('Textarea')
    case 'select':
      return t('Select')
    case 'checkbox':
      return t('Checkbox')
    case 'submit':
      return t('Submit button')
    case 'html':
      return t('HTML / Embed')
  }
}

function groupLabel(g: PaletteItem['group']): string {
  switch (g) {
    case 'layout':
      return t('Layout')
    case 'basic':
      return t('Basic')
    case 'media':
      return t('Media')
    case 'form':
      return t('Form fields')
  }
}

function sampleLabel(k: SampleKind): string {
  switch (k) {
    case 'button':
      return t('Buttons')
    case 'link':
      return t('Links')
    case 'heading':
      return t('Headings')
    case 'text':
      return t('Texts')
    case 'badge':
      return t('Badges')
    case 'card':
      return t('Cards')
    case 'list-item':
      return t('List items')
    case 'image':
      return t('Images')
    case 'section':
      return t('Sections')
    case 'container':
      return t('Containers')
  }
}

function varGroupLabel(g: VarGroup): string {
  switch (g) {
    case 'color':
      return t('Colors')
    case 'font':
      return t('Fonts')
    case 'size':
      return t('Spacing and sizes')
    default:
      return t('Other')
  }
}

type StartDrag = (e: React.PointerEvent, add: NewElement) => void

export function Palette({ startDrag, secs, parts }: { startDrag: StartDrag; secs: Section[]; parts: { header: Section[]; footer: Section[] } }) {
  const st = useElState()
  const tab = st.palette ?? 'elements'
  const tabBtn = (id: 'elements' | 'design' | 'variables', label: string) => (
    <button type="button" aria-pressed={tab === id} data-wb-palette-tab={id} onClick={() => setElState({ palette: id })} className={cn('h-7 flex-1 rounded-[5px] text-[11.5px]', tab === id ? 'bg-ink-600 text-white' : 'text-ink-400 hover:text-ink-200')}>
      {label}
    </button>
  )
  return (
    <div data-wb-palette={tab} className="absolute bottom-[34px] left-0 top-0 z-30 flex w-[292px] flex-col border-r border-white/[0.07] bg-ink-900/[0.98] text-ink-200 shadow-2xl" onClick={(e) => e.stopPropagation()} onPointerDown={(e) => e.stopPropagation()}>
      <div className="flex items-center gap-1 border-b border-white/[0.07] p-2">
        <div className="flex flex-1 gap-0.5 rounded-[6px] border border-white/[0.08] bg-ink-850 p-0.5">
          {tabBtn('elements', t('Elements'))}
          {tabBtn('design', t('From this design'))}
          {tabBtn('variables', t('Variables'))}
        </div>
        <button type="button" aria-label={t('Close')} onClick={() => setElState({ palette: null })} className="grid size-7 place-items-center rounded-md text-ink-400 hover:bg-white/10 hover:text-white">
          <X size={14} />
        </button>
      </div>
      <div className="scroll-thin min-h-0 flex-1 overflow-y-auto p-2">
        {tab === 'elements' && <ElementsTab startDrag={startDrag} />}
        {tab === 'design' && <DesignTab startDrag={startDrag} secs={secs} parts={parts} />}
        {tab === 'variables' && <VariablesTab secs={secs} parts={parts} />}
      </div>
    </div>
  )
}

/** Click: insert next to the selection. */
function useInsert() {
  const b = useB()
  return (add: NewElement) => act.insertNear(b, getElState().sel, b.selection?.sectionId ?? null, add.html, add.local, add.label)
}

function ElementsTab({ startDrag }: { startDrag: StartDrag }) {
  const insert = useInsert()
  const mode = useB().mode
  const groups: PaletteItem['group'][] = ['layout', 'basic', 'media', 'form']
  return (
    <div className="space-y-3">
      <p className="text-[10.5px] leading-snug text-ink-500">{t('Drag an element onto the page, or click it to add it after the selected element.')}</p>
      {groups.map((g) => (
        <div key={g}>
          <div className="mb-1 text-[10.5px] font-semibold uppercase tracking-wide text-ink-500">{groupLabel(g)}</div>
          <div className="grid grid-cols-3 gap-1">
            {paletteItems(g, mode).map((p) => {
              const I = ICONS[p.id]
              const add: NewElement = { html: p.html, local: p.local, label: t('{el} added', { el: itemLabel(p.id) }) }
              return (
                <button
                  key={p.id}
                  type="button"
                  data-wb-palette-item={p.id}
                  onPointerDown={(e) => {
                    if (e.button === 0) startDrag(e, add)
                  }}
                  onClick={() => insert(add)}
                  className="flex h-[62px] cursor-grab flex-col items-center justify-center gap-1 rounded-md border border-white/[0.06] bg-white/[0.03] text-[10.5px] text-ink-300 hover:border-accent/40 hover:bg-white/[0.07] hover:text-white"
                >
                  <I size={17} />
                  <span className="max-w-full truncate px-1">{itemLabel(p.id)}</span>
                </button>
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}

/* ---------- From this design ---------- */

/** The design's head markup and body attributes, for thumbnails. */
function useDesignShell(secs: Section[], parts: { header: Section[]; footer: Section[] }) {
  return useMemo(() => {
    const all = [...parts.header, ...secs, ...parts.footer]
    const head = mergeHeads(
      all.map((s) => s.content.head ?? ''),
      'static',
    )
    const first = secs[0] ?? all[0]
    const body = (first?.content.body ?? '').replace(/"/g, '&quot;')
    return { head, body }
  }, [secs, parts])
}

function Thumb({ sample, shell, kitCss }: { sample: Sample; shell: { head: string; body: string }; kitCss: string }) {
  const box = useRef<HTMLDivElement>(null)
  const [show, setShow] = useState(false)
  const [scale, setScale] = useState(0.4)
  const [h, setH] = useState(96)
  useEffect(() => {
    const el = box.current
    if (!el || show) return
    const io = new IntersectionObserver((list) => list.some((x) => x.isIntersecting) && setShow(true), { rootMargin: '200px' })
    io.observe(el)
    return () => io.disconnect()
  }, [show])
  const W = 640
  const base = typeof location !== 'undefined' ? location.origin + '/' : '/'
  const doc = useMemo(() => thumbDoc({ head: shell.head, body: shell.body, markup: sample.html, css: kitCss, base, width: W, fit: 'content' }), [shell, sample.html, kitCss, base])
  return (
    <div ref={box} className="relative h-[110px] overflow-hidden rounded-md border border-white/[0.06] bg-white" data-wb-thumb-box="">
      {show && (
        <iframe
          title={sample.sig}
          srcDoc={doc}
          sandbox={THUMB_SANDBOX}
          tabIndex={-1}
          aria-hidden="true"
          className="pointer-events-none absolute left-0 top-0 origin-top-left border-0"
          style={{ width: W, height: h, transform: `scale(${scale})` }}
          onLoad={(e) => {
            const d = (e.target as HTMLIFrameElement).contentDocument
            const el = d?.querySelector('[data-wb-thumb]') as HTMLElement | null
            if (!el) return
            const r = el.getBoundingClientRect()
            const s = Math.min(1, (box.current?.clientWidth ?? 260) / Math.max(1, r.width), 110 / Math.max(1, r.height))
            setScale(s)
            setH(Math.min(1600, Math.ceil(r.height) + 2))
          }}
        />
      )}
    </div>
  )
}

function DesignTab({ startDrag, secs, parts }: { startDrag: StartDrag; secs: Section[]; parts: { header: Section[]; footer: Section[] } }) {
  const b = useB()
  const insert = useInsert()
  const ks = useKitStore()
  const kits = useMemo(() => kitsOf(b.doc, parts as never), [b.doc, parts])
  const [kit, setKit] = useState(kits[0] ?? '')
  const [q, setQ] = useState('')
  const [busy, setBusy] = useState(false)
  const shell = useDesignShell(secs, parts)
  const kitCss = useMemo(() => (kit ? (Object.keys(ks.kits).includes(kit) ? overrideCssOf(b.doc, kit) : '') : ''), [b.doc, kit, ks.kits])
  useEffect(() => {
    if (!kit) return
    setBusy(true)
    const extra = [...parts.header, ...secs, ...parts.footer].filter((s) => s.content.kit === kit).map((s) => ({ html: s.content.html ?? '', page: 'this' }))
    void loadCatalog(kit, extra).finally(() => setBusy(false))
  }, [kit]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!kits.length) return <p className="text-[11.5px] text-ink-400">{t('This page uses no design kit: there is no design to take elements from.')}</p>
  const list = (ks.catalog[kit] ?? []).filter((s) => !q || s.sig.includes(q.toLowerCase()) || s.text.toLowerCase().includes(q.toLowerCase()))
  const by = new Map<SampleKind, Sample[]>()
  for (const s of list) by.set(s.kind, [...(by.get(s.kind) ?? []), s])
  return (
    <div className="space-y-3" data-wb-design-palette={kit}>
      {kits.length > 1 && (
        <select value={kit} onChange={(e) => setKit(e.target.value)} className={cn(inputCls, 'w-full')}>
          {kits.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
      )}
      <label className="relative block">
        <Search size={12} className="absolute left-2 top-2 text-ink-500" />
        <input value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.stopPropagation()} placeholder={t('Search classes or text')} className={cn(inputCls, 'w-full pl-6')} />
      </label>
      <p className="text-[10.5px] leading-snug text-ink-500">{t('Elements the design repeats (same tag and classes at least three times). Drag one onto the page or click it: a copy with the design’s classes is added.')}</p>
      {busy && !list.length && <p className="text-[11.5px] text-ink-400">{t('Reading the design…')}</p>}
      {!busy && !list.length && <p className="text-[11.5px] text-ink-400">{t('No repeated elements found.')}</p>}
      {KIND_ORDER.filter((k) => by.has(k)).map((k) => (
        <Group key={k} title={`${sampleLabel(k)} · ${by.get(k)!.length}`}>
          {by.get(k)!.map((s) => {
            const add: NewElement = { html: s.html, local: {}, label: t('Design element added') }
            return (
              <button
                key={s.sig}
                type="button"
                data-wb-design-item={s.sig}
                data-wb-design-kind={s.kind}
                title={`<${s.tag} class="${s.classes.join(' ')}">`}
                onPointerDown={(e) => {
                  if (e.button === 0) startDrag(e, add)
                }}
                onClick={() => insert(add)}
                className="block w-full cursor-grab space-y-1 rounded-md p-1 text-left hover:bg-white/[0.06]"
              >
                <Thumb sample={s} shell={shell} kitCss={kitCss} />
                <span className="flex items-center justify-between gap-2 text-[10.5px]">
                  <span className="truncate font-mono text-ink-300">
                    {s.tag}.{s.classes.slice(0, 3).join('.')}
                    {s.classes.length > 3 ? '…' : ''}
                  </span>
                  <span className="shrink-0 text-ink-500">×{s.count}</span>
                </span>
              </button>
            )
          })}
        </Group>
      ))}
    </div>
  )
}

const overrideCssOf = (doc: Parameters<typeof overrideOf>[0], kit: string) => overrideCSS(overrideOf(doc, kit))

function Group({ title, children }: { title: string; children: ReactNode }) {
  const [open, setOpen] = useState(true)
  return (
    <div>
      <button type="button" onClick={() => setOpen(!open)} className="mb-1 flex w-full items-center justify-between text-[10.5px] font-semibold uppercase tracking-wide text-ink-500 hover:text-ink-300">
        {title}
        <span>{open ? '−' : '+'}</span>
      </button>
      {open && <div className="space-y-1">{children}</div>}
    </div>
  )
}

/* ---------- Variables ---------- */

function VariablesTab({ secs, parts }: { secs: Section[]; parts: { header: Section[]; footer: Section[] } }) {
  const b = useB()
  const ks = useKitStore()
  const version = useFrameVersion()
  const [q, setQ] = useState('')
  const vars = useMemo(() => (frameInfo.doc ? rootVariables(frameInfo.doc) : []), [version]) // eslint-disable-line react-hooks/exhaustive-deps
  const pageKits = useMemo(() => kitsOf(b.doc, parts as never), [b.doc, parts])
  void secs
  const doc = latest.doc ?? b.doc
  const kitOf = (v: VarDef) => (v.kit && ks.kits[v.kit] ? v.kit : pageKits.find((k) => ks.kits[k]) ?? '')
  const own = (v: VarDef) => {
    const k = kitOf(v)
    return k ? ruleOf(overrideOf(doc, k), v.ctx, v.sel)?.p[v.name] : undefined
  }
  const write = (v: VarDef, value: string | null) => {
    const k = kitOf(v)
    if (!k) return b.notify(t('This page uses no design kit: its variables cannot be changed here.'))
    const val = value?.trim() ?? null
    if (val !== null && (/[;{}<]/.test(val) || !val)) return b.notify(t('This is not a value CSS accepts here: {v}', { v: val }))
    setOverride(b, latest.doc ?? b.doc, k, setRuleDecls(overrideOf(latest.doc ?? b.doc, k), v.ctx, v.sel, { [v.name]: val }), val === null ? t('Variable reset') : t('Variable changed'))
  }
  if (!frameInfo.doc) return <p className="text-[11.5px] text-ink-400">{t('Loading…')}</p>
  if (!vars.length) return <p className="text-[11.5px] text-ink-400">{t('The design defines no variables on :root.')}</p>
  const list = vars.filter((v) => !q || v.name.includes(q) || v.value.includes(q))
  const groups: VarGroup[] = ['color', 'font', 'size', 'other']
  return (
    <div className="space-y-3" data-wb-variables-panel="">
      <label className="relative block">
        <Search size={12} className="absolute left-2 top-2 text-ink-500" />
        <input value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.stopPropagation()} placeholder={t('Search variables')} className={cn(inputCls, 'w-full pl-6')} />
      </label>
      <p className="text-[10.5px] leading-snug text-ink-500">{t('A change applies to every page of the site that uses this design when you publish.')}</p>
      {groups.map((g) => {
        const rows = list.filter((v) => v.group === g)
        if (!rows.length) return null
        return (
          <Group key={g} title={`${varGroupLabel(g)} · ${rows.length}`}>
            {rows.map((v) => (
              <VarRow key={v.name} v={v} own={own(v)} onWrite={(x) => write(v, x)} />
            ))}
          </Group>
        )
      })}
    </div>
  )
}

function VarRow({ v, own, onWrite }: { v: VarDef; own: string | undefined; onWrite: (v: string | null) => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  const value = own ?? v.value
  const hex = v.group === 'color' ? hexOf(value) : null
  const done = () => {
    if (draft !== null && draft.trim() !== value) onWrite(draft.trim() === v.value && own !== undefined ? null : draft)
    setDraft(null)
  }
  return (
    <div className="space-y-0.5 rounded-md px-1 py-1 hover:bg-white/[0.03]" data-wb-var={v.name}>
      <div className="flex items-center gap-1">
        {own !== undefined && <span className="size-1.5 shrink-0 rounded-full bg-accent-soft" data-wb-dot="" />}
        <span className="min-w-0 flex-1 truncate font-mono text-[10.5px] text-ink-300" title={`${v.name} · ${v.source}${v.ctx.length ? ' · ' + v.ctx.join(' ') : ''}${v.more ? ' · +' + v.more : ''}`}>
          {v.name}
        </span>
        <button type="button" aria-label={t('Reset')} title={t('Reset to the design’s value: {v}', { v: v.value })} disabled={own === undefined} onClick={() => onWrite(null)} className="grid size-5 shrink-0 place-items-center rounded text-ink-500 hover:text-ink-100 disabled:opacity-0" data-wb-var-reset={v.name}>
          <RotateCcw size={10} />
        </button>
      </div>
      <div className="flex items-center gap-1">
        {v.group === 'color' && (
          <label className="relative size-6 shrink-0 cursor-pointer overflow-hidden rounded-[4px] ring-1 ring-inset ring-white/15" style={{ background: value }} title={t('Pick a color')}>
            {hex && <input type="color" value={hex} onChange={(e) => onWrite(e.target.value)} className="absolute inset-0 cursor-pointer opacity-0" />}
          </label>
        )}
        <input
          value={draft ?? value}
          name={v.name}
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
          className={cn(inputCls, 'min-w-0 flex-1 font-mono text-[10.5px]', own !== undefined && 'border-accent/60 text-accent-soft')}
        />
      </div>
    </div>
  )
}
