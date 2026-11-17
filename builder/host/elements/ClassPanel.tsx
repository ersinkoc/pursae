/**
 * Classes in the Style panel (plan §4.3 b, Faz 5): the selected element's class chips (add with
 * autocomplete from the classes the design's stylesheets define, remove), editing a class for
 * every element that carries it (the panel's controls then write the kit's override stylesheet
 * with the design rule's own selector and @layer / @media context; how many elements use it on
 * this page and on the site is shown before an edit applies), and the rules that match the
 * selected element (selector, source, context, the winning declarations marked).
 */
import { useEffect, useMemo, useState } from 'react'
import { Check, Pencil, Plus, RotateCcw, X } from 'lucide-react'
import { Panel, inputCls } from '@/builder/StudioPanel'
import { cn } from '@/builder/ui'
import type { CmsHost } from '../cms-host'
import { t } from '../i18n'
import { act, latest, pageOf } from './actions'
import { beats, declsOf, matchedRules, winner, type Decl, type Matched } from './cascade'
import { classBase, classRules, cssEscape, designClasses, type RuleRef } from './cssom'
import { attr } from './html'
import { getKitStore, loadPages, overrideOf, setOverride, useKitStore } from './kitstyle'
import { sectionBps, type Breakpoints, type LocalSection } from './local'
import { countClass } from './miner'
import { locate } from './ops'
import { frameInfo, setElState } from './state'
import type { Ctx } from './StylePanel'

export interface ClassTarget {
  cls: string
  /** The kit whose override takes the edit ('' : none, the class cannot be edited). */
  kit: string
  /** The design's rules for the class alone. */
  rules: RuleRef[]
  /** The edit's context and selector (the design rule's). */
  ctx: string[]
  sel: string
  /** How the design rule reads ("@layer utilities .rounded-2xl"). */
  label: string
  bps: Breakpoints
}

const host = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

const targetCache = new Map<string, { v: number; doc: Document; target: ClassTarget }>()

/** The class edit of `cls` from the selected element's frame. */
export function classTarget(el: Element, cls: string, sec: LocalSection): ClassTarget {
  const doc = el.ownerDocument
  const key = `${cls}\u0000${sec.id}`
  const hit = targetCache.get(key)
  if (hit && hit.doc === doc && hit.v === frameInfo.version) return hit.target
  const rules = classRules(doc, cls)
  const base = classBase(rules)
  const kits = getKitStore().kits
  const kit = [base?.kit, ...rules.map((r) => r.kit), sec.content.kit, ...Object.keys(kits)].find((k): k is string => !!k && !!kits[k]) ?? ''
  const sel = '.' + cssEscape(cls)
  const target: ClassTarget = {
    cls,
    kit,
    rules,
    ctx: base?.ctx ?? [],
    sel,
    label: base ? [...base.ctx, base.sel].join(' ') : sel,
    bps: sectionBps(sec, host()?.boot.kitBreakpoints),
  }
  if (targetCache.size > 50) targetCache.clear()
  targetCache.set(key, { v: frameInfo.version, doc, target })
  return target
}

/** Elements with the class on this page (the frame: header and footer included) and on the site. */
function useCounts(cls: string, kit: string) {
  const ks = useKitStore()
  useEffect(() => {
    if (kit) void loadPages(kit)
  }, [kit])
  const page = frameInfo.doc ? frameInfo.doc.querySelectorAll('.' + cssEscape(cls)).length : 0
  const pages = ks.pages[kit]
  const site = Array.isArray(pages) ? countClass(pages, cls) : null
  return { page, site }
}

function CountLine({ cls, kit }: { cls: string; kit: string }) {
  const { page, site } = useCounts(cls, kit)
  return (
    <div className="text-[11px] leading-snug text-amber-200/90" data-wb-class-count={`${page}/${site ? site.elements : ''}`}>
      {site
        ? t('Used on {n} elements on this page and {m} elements on {p} pages of the site.', { n: page, m: site.elements, p: site.pages })
        : t('Used on {n} elements on this page.', { n: page })}
    </div>
  )
}

/* ---------- Chips ---------- */

export function ClassChips({ c }: { c: Ctx }) {
  const at = locate(pageOf(latest.doc ?? c.b.doc), c.id)
  const classes = at ? (attr(at.el, 'class') ?? '').split(/\s+/).filter(Boolean) : []
  const [draft, setDraft] = useState('')
  const [ask, setAsk] = useState<string | null>(null)
  const all = useMemo(() => (frameInfo.doc ? designClasses(frameInfo.doc, c.version) : []), [c.version])
  const options = useMemo(() => {
    const q = draft.trim()
    if (!q) return []
    return all.filter((x) => x.startsWith(q) && !classes.includes(x)).slice(0, 60)
  }, [all, draft, classes])
  const add = (v: string) => {
    const names = v.split(/\s+/).filter(Boolean).filter((x) => !classes.includes(x))
    if (!names.length) return
    if (names.some((x) => /["<>]/.test(x))) return c.b.notify(t('A class name cannot hold quotes or angle brackets.'))
    act.setAttrs(c.b, c.id, { class: [...classes, ...names].join(' ') }, t('Class added'))
    setDraft('')
  }
  const remove = (k: string) => act.setAttrs(c.b, c.id, { class: classes.filter((x) => x !== k).join(' ') || null }, t('Class removed'))
  const target = ask && c.el ? classTarget(c.el, ask, c.sec) : null
  return (
    <div className="space-y-2 border-b border-white/[0.07] px-4 py-3" data-wb-class-chips="">
      <div className="flex items-center justify-between text-[11px] text-ink-400">
        <span>{t('Classes')}</span>
        <span className="text-[10px] text-ink-500">{t('Click a class to edit it everywhere')}</span>
      </div>
      <div className="flex flex-wrap gap-1">
        {classes.map((k) => (
          <span key={k} className={cn('group flex max-w-full items-center rounded bg-white/[0.06] font-mono text-[10.5px] text-ink-200', ask === k && 'ring-1 ring-accent/60')} data-wb-chip={k}>
            <button type="button" title={t('Edit this class for every element that has it')} onClick={() => setAsk(ask === k ? null : k)} className="truncate px-1.5 py-0.5 hover:text-white">
              .{k}
            </button>
            <button type="button" aria-label={t('Remove class {c}', { c: k })} onClick={() => remove(k)} className="grid h-full place-items-center px-0.5 text-ink-500 hover:text-red-300">
              <X size={10} />
            </button>
          </span>
        ))}
        {!classes.length && <span className="text-[10.5px] text-ink-500">{t('No classes')}</span>}
      </div>
      <div className="flex items-center gap-1">
        <input
          value={draft}
          list="wb-class-list"
          name="wb-class-add"
          placeholder={t('Add a class')}
          spellCheck={false}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            e.stopPropagation()
            if (e.key === 'Enter') add(draft)
          }}
          className={cn(inputCls, 'min-w-0 flex-1 font-mono text-[11px]')}
        />
        <datalist id="wb-class-list">
          {options.map((o) => (
            <option key={o} value={o} />
          ))}
        </datalist>
        <button type="button" aria-label={t('Add')} onClick={() => add(draft)} className="grid size-7 place-items-center rounded text-ink-400 hover:text-white">
          <Plus size={13} />
        </button>
      </div>
      {ask && target && (
        <div className="space-y-2 rounded-md border border-amber-400/30 bg-amber-300/[0.06] p-2" data-wb-class-confirm={ask}>
          <div className="text-[11.5px] text-ink-100">{t('Edit .{c} for every element that has it?', { c: ask })}</div>
          <CountLine cls={ask} kit={target.kit} />
          {!target.kit ? (
            <div className="text-[10.5px] text-red-300">{t('This class does not come from a design kit: it cannot be edited here.')}</div>
          ) : (
            <div className="text-[10.5px] text-ink-400">{target.rules.length ? t('The change goes to the design’s rule {r}; it applies to every page when you publish.', { r: target.label }) : t('The design has no rule for this class alone: the edit adds one. It applies to every page when you publish.')}</div>
          )}
          <div className="flex gap-1">
            <button
              type="button"
              disabled={!target.kit}
              data-wb-class-edit-start={ask}
              onClick={() => {
                setElState({ classEdit: ask })
                setAsk(null)
              }}
              className="flex h-7 items-center gap-1 rounded-[5px] bg-amber-400 px-2 text-[11.5px] font-semibold text-black disabled:opacity-40"
            >
              <Pencil size={12} /> {t('Edit class')}
            </button>
            <button type="button" onClick={() => setAsk(null)} className="h-7 rounded-[5px] px-2 text-[11.5px] text-ink-300 hover:bg-white/10">
              {t('Cancel')}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

/* ---------- Editing a class ---------- */

export function ClassHeader({ c, target }: { c: Ctx; target: ClassTarget }) {
  const doc = latest.doc ?? c.b.doc
  const o = target.kit ? overrideOf(doc, target.kit) : null
  const edited = !!o?.rules.some((r) => r.sel === target.sel || r.sel.startsWith(target.sel + ':'))
  const reset = () => {
    if (!o) return
    setOverride(c.b, doc, target.kit, { rules: o.rules.filter((r) => !(r.sel === target.sel || r.sel.startsWith(target.sel + ':'))) }, t('Class reset'))
  }
  return (
    <div className="space-y-2 border-b border-amber-400/30 bg-amber-300/[0.06] px-4 py-3" data-wb-class-header={target.cls}>
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate text-[12px] font-semibold text-amber-100">{t('Editing class .{c}', { c: target.cls })}</span>
        <button type="button" data-wb-class-done="" onClick={() => setElState({ classEdit: null })} className="flex h-6 shrink-0 items-center gap-1 rounded-[5px] bg-white/10 px-2 text-[11px] text-white hover:bg-white/20">
          <Check size={11} /> {t('Done')}
        </button>
      </div>
      <CountLine cls={target.cls} kit={target.kit} />
      <div className="truncate font-mono text-[10px] text-ink-400" title={target.label}>
        {target.label}
      </div>
      {edited && (
        <button type="button" onClick={reset} data-wb-class-reset="" className="flex h-6 items-center gap-1 rounded-[5px] px-1.5 text-[11px] text-ink-300 hover:bg-white/10">
          <RotateCcw size={11} /> {t('Reset every change of this class')}
        </button>
      )}
    </div>
  )
}

/* ---------- Matched rules ---------- */

function declsText(style: CSSStyleDeclaration): { name: string; value: string; important: boolean }[] {
  const out: { name: string; value: string; important: boolean }[] = []
  for (let i = 0; i < style.length; i++) {
    const name = style.item(i)
    out.push({ name, value: style.getPropertyValue(name).trim(), important: style.getPropertyPriority(name) === 'important' })
  }
  return out
}

/** The rules of the frame that apply to the selected element (the winner first), the inline style last. */
export function MatchedRules({ c }: { c: Ctx }) {
  const el = c.el
  const rules = useMemo(() => {
    if (!el) return []
    let list: Matched[] = []
    try {
      list = [...matchedRules(el, c.st, c.version)]
    } catch {
      return []
    }
    const asDecl = (m: Matched): Decl => ({ value: '', important: false, spec: m.spec, layer: m.layer, order: m.order, selector: m.selector, origin: m.local ? 'local' : 'design' })
    list.sort((a, b) => (beats(asDecl(a), asDecl(b)) ? -1 : beats(asDecl(b), asDecl(a)) ? 1 : 0))
    return list
  }, [el, c.st, c.version])
  const wins = useMemo(() => {
    const cache = new Map<string, Decl | null>()
    return (prop: string) => {
      if (!el) return null
      if (!cache.has(prop)) cache.set(prop, winner(declsOf(el, prop, c.st, c.version)))
      return cache.get(prop)!
    }
  }, [el, c.st, c.version])
  if (!el) return null
  const inline = (el as HTMLElement).style
  return (
    <Panel title={t('Matched rules')} open={false}>
      <div className="space-y-2" data-wb-matched-rules={rules.length}>
        {!rules.length && !inline?.length && <div className="text-[11px] text-ink-500">{t('No rule of the design applies to this element.')}</div>}
        {inline?.length > 0 && <RuleBox title="style=" source={t('Inline style')} ctx={[]} decls={declsText(inline)} isWin={(d) => wins(d.name)?.origin === 'inline'} />}
        {rules.map((m) => (
          <RuleBox
            key={m.order}
            title={m.ruleText}
            source={m.local ? t('Local style') : m.override ? t('Class or variable edit') : m.source}
            ctx={m.ctx}
            decls={declsText(m.style)}
            isWin={(d) => {
              const w = wins(d.name)
              return !!w && w.order === m.order && w.origin !== 'inline'
            }}
            tone={m.local ? 'local' : m.override ? 'override' : 'design'}
          />
        ))}
      </div>
    </Panel>
  )
}

function RuleBox({ title, source, ctx, decls, isWin, tone = 'design' }: { title: string; source: string; ctx: string[]; decls: { name: string; value: string; important: boolean }[]; isWin: (d: { name: string }) => boolean; tone?: 'design' | 'local' | 'override' }) {
  return (
    <div className={cn('rounded-md border p-1.5 font-mono text-[10.5px]', tone === 'local' ? 'border-accent/40' : tone === 'override' ? 'border-amber-400/40' : 'border-white/[0.08]')} data-wb-rule={title}>
      <div className="flex items-start justify-between gap-2">
        <span className="min-w-0 break-all text-ink-100">{title}</span>
        <span className="shrink-0 text-[9.5px] text-ink-500">{source}</span>
      </div>
      {ctx.length > 0 && <div className="truncate text-[9.5px] text-ink-500">{ctx.join(' › ')}</div>}
      <div className="mt-1 space-y-0.5">
        {decls.map((d) => {
          const w = isWin(d)
          return (
            <div key={d.name} className={cn('flex gap-1', w ? 'text-ink-100' : 'text-ink-500 line-through decoration-ink-600')} data-wb-decl={d.name} data-wb-win={w ? '1' : ''}>
              <span className="text-accent-soft/80">{d.name}</span>: <span className="break-all">{d.value}{d.important ? ' !important' : ''}</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
