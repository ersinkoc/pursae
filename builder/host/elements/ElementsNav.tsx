/**
 * The navigator's branch of a code section (patch 0020 hands code sections to it): the section's
 * elements as a tree with their type icons (plan §4.2), drag to reorder or move into another
 * container (also into another section), rename (stored in the section's content.names, keyed by
 * data-wbc, so the design's markup stays clean), hide / show in the editor only, collapse.
 */
import { memo, useMemo, useState } from 'react'
import { ChevronDown, Eye, EyeOff, LayoutTemplate } from 'lucide-react'
import { useB, type BuilderApi } from '@/builder/store'
import type { Section } from '@/builder/types'
import { t } from '../i18n'
import { act, latest, pageOf, selectEl } from './actions'
import { type El, type Tree } from './html'
import { KIND_ICON, kindLabel, kindOf, namesOf, snippet, tagName } from './kinds'
import { childIds, container, dropError, locate, movable, placeBy, tree, whereIs, type Place } from './ops'
import { setElState, toggleIn, useElState } from './state'

const ROW = 'mx-1 flex h-7 cursor-pointer select-none items-center gap-1.5 rounded-[5px] pr-1.5 text-[11.5px] transition-colors'

/* ---------- Dragging rows ---------- */

interface NavDrag {
  id: string
  x0: number
  y0: number
  on: boolean
  place: Place | null
}

let navDrag: NavDrag | null = null
let indicator: HTMLDivElement | null = null

function showIndicator(r: { left: number; top: number; width: number; height: number } | null, inside: boolean) {
  if (!r) {
    if (indicator) indicator.style.display = 'none'
    return
  }
  if (!indicator) {
    indicator = document.createElement('div')
    indicator.setAttribute('data-wb-nav-drop', '')
    indicator.style.cssText = 'position:fixed;z-index:200;pointer-events:none;border-radius:3px'
    document.body.appendChild(indicator)
  }
  Object.assign(indicator.style, {
    display: '',
    left: `${r.left}px`,
    top: `${r.top}px`,
    width: `${r.width}px`,
    height: `${r.height}px`,
    background: inside ? 'rgba(61,99,245,.18)' : '#3d63f5',
    outline: inside ? '1px solid #3d63f5' : 'none',
  })
}

/** The drop place for the row under (x, y). */
function navTarget(x: number, y: number, moving: string): { place: Place; line: { left: number; top: number; width: number; height: number }; inside: boolean } | null {
  const row = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-wb-nav-row]')
  if (!row || !latest.doc) return null
  const page = pageOf(latest.doc)
  const id = row.dataset.wbNavRow!
  const sec = row.dataset.wbNavSec!
  const r = row.getBoundingClientRect()
  const indent = Number(row.dataset.wbNavIndent ?? 0)
  const line = (after: boolean, depth = indent) => ({ left: r.left + depth, top: (after ? r.bottom : r.top) - 1, width: Math.max(20, r.width - depth), height: 2 })
  if (!id) {
    // A section row: at the end of the section's top level.
    const place = placeBy(page, sec, null, null, true, moving)
    return place && !dropError(page, moving, place) ? { place, line: { left: r.left, top: r.top, width: r.width, height: r.height }, inside: true } : null
  }
  const at = locate(page, id)
  if (!at) return null
  const f = (y - r.top) / r.height
  const tries: { place: Place | null; line: ReturnType<typeof line>; inside: boolean }[] = []
  const w = whereIs(page, id)
  const edgeOk = w && id !== moving
  if (f < 0.3 && edgeOk) tries.push({ place: placeBy(page, w!.sec, w!.parent, id, false, moving), line: line(false), inside: false })
  else if (f > 0.7 && edgeOk) tries.push({ place: placeBy(page, w!.sec, w!.parent, id, true, moving), line: line(true), inside: false })
  if (container(at.tree, at.el) && id !== moving) tries.push({ place: placeBy(page, at.sec, id, null, true, moving), line: { left: r.left + indent, top: r.top, width: r.width - indent, height: r.height }, inside: true })
  if (edgeOk) tries.push({ place: placeBy(page, w!.sec, w!.parent, id, f > 0.5, moving), line: line(f > 0.5), inside: false })
  for (const x of tries) if (x.place && !dropError(page, moving, x.place)) return x as { place: Place; line: ReturnType<typeof line>; inside: boolean }
  return null
}

function startNavDrag(e: React.PointerEvent, id: string, b: BuilderApi) {
  if (e.button !== 0) return
  navDrag = { id, x0: e.clientX, y0: e.clientY, on: false, place: null }
  const move = (ev: PointerEvent) => {
    const g = navDrag
    if (!g) return
    if (!g.on) {
      if (Math.hypot(ev.clientX - g.x0, ev.clientY - g.y0) < 5) return
      g.on = true
      document.body.style.cursor = 'grabbing'
    }
    const tg = navTarget(ev.clientX, ev.clientY, g.id)
    g.place = tg?.place ?? null
    showIndicator(tg?.line ?? null, !!tg?.inside)
    // Auto-scroll the list near its edges.
    const list = (document.elementFromPoint(ev.clientX, ev.clientY) as HTMLElement | null)?.closest('.overflow-y-auto')
    if (list) {
      const lr = list.getBoundingClientRect()
      if (ev.clientY < lr.top + 24) list.scrollBy(0, -12)
      else if (ev.clientY > lr.bottom - 24) list.scrollBy(0, 12)
    }
  }
  const up = () => {
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', up)
    document.body.style.cursor = ''
    showIndicator(null, false)
    const g = navDrag
    navDrag = null
    if (g?.on && g.place) {
      suppressClick = Date.now()
      act.moveTo(b, g.id, g.place)
    }
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', up)
}
let suppressClick = 0

/* ---------- Rows ---------- */

export function ElementsNav({ s }: { s: Section }) {
  const b = useB()
  const st = useElState()
  const src = s.content.html ?? ''
  const tr = useMemo(() => tree(src), [src])
  const names = useMemo(() => namesOf(s.content.names), [s.content.names])
  // The path to the selection is open.
  const path = useMemo(() => {
    const out = new Set<string>()
    const el = st.sel ? tr.byId.get(st.sel) : undefined
    if (!el) return out
    out.add(s.id)
    for (let p = el.parent; p && p.tag !== '#root'; p = p.parent) {
      const w = p.attrs.find((a) => a.name === 'data-wbc')?.value
      if (w) out.add(w)
    }
    return out
  }, [st.sel, tr, s.id])
  // A row on the path to the selection is open unless the user closed it.
  const open = (k: string) => st.open.has(k) !== path.has(k)
  const toggle = (k: string) => setElState((x) => ({ open: toggleIn(x.open, k) }))
  const secSelected = b.selection?.sectionId === s.id && !b.selection.el && !st.sel
  const isOpen = open(s.id)
  return (
    <div data-wb-nav-section={s.id}>
      <div
        data-wb-nav-row=""
        data-wb-nav-sec={s.id}
        onClick={() => {
          setElState({ sel: null })
          b.select({ sectionId: s.id }, { scroll: true })
        }}
        style={{ paddingLeft: 4 }}
        className={`group/row ${ROW} h-8 text-[12px] ${secSelected ? 'bg-accent/20 text-white' : 'text-ink-300 hover:bg-white/[0.05]'} ${s.hidden ? 'opacity-45' : ''}`}
      >
        <Chevron open={isOpen} onClick={() => toggle(s.id)} />
        <LayoutTemplate size={13} className="shrink-0 text-ink-500" />
        <span className="truncate">{t('Section')}</span>
        <span className="min-w-0 flex-1 truncate text-ink-500">· {s.name}</span>
        <EyeBtn hidden={s.hidden} label={s.hidden ? t('Show') : t('Hide')} onClick={() => b.dispatch({ type: 'toggleHidden', id: s.id })} />
      </div>
      {isOpen && (
        <div>
          {childIds(tr, tr.root).map((id) => (
            <Row key={id} b={b} sec={s.id} tr={tr} id={id} depth={1} names={names} sel={st.sel} hidden={st.hidden} open={open} toggle={toggle} />
          ))}
        </div>
      )}
    </div>
  )
}

function Chevron({ open, onClick }: { open: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={open ? t('Collapse') : t('Expand')}
      aria-expanded={open}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation()
        onClick()
      }}
      className="grid size-6 shrink-0 place-items-center text-ink-500 hover:text-ink-200"
    >
      <ChevronDown size={12} className={open ? '' : '-rotate-90'} />
    </button>
  )
}

function EyeBtn({ hidden, label, onClick }: { hidden: boolean; label: string; onClick: () => void }) {
  const I = hidden ? EyeOff : Eye
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation()
        onClick()
      }}
      className={`ml-auto grid size-6 shrink-0 place-items-center rounded text-ink-500 hover:bg-white/10 hover:text-white ${hidden ? '' : 'opacity-0 group-hover/row:opacity-100'}`}
    >
      <I size={12.5} />
    </button>
  )
}

interface RowProps {
  b: BuilderApi
  sec: string
  tr: Tree
  id: string
  depth: number
  names: Record<string, string>
  sel: string | null
  hidden: ReadonlySet<string>
  open: (k: string) => boolean
  toggle: (k: string) => void
}

const Row = memo(function Row({ b, sec, tr, id, depth, names, sel, hidden, open, toggle }: RowProps) {
  const el = tr.byId.get(id) as El
  const [renaming, setRenaming] = useState(false)
  if (!el) return null
  const kind = kindOf(tr, el)
  const Icon = KIND_ICON[kind]
  const kids = childIds(tr, el)
  const isOpen = kids.length > 0 && open(id)
  const name = names[id]
  const text = snippet(tr, el, 28)
  const indent = 4 + depth * 14
  const selected = sel === id
  return (
    <div>
      <div
        data-wb-nav-row={id}
        data-wb-nav-sec={sec}
        data-wb-nav-indent={indent}
        onPointerDown={(e) => !renaming && movable(el) && startNavDrag(e, id, b)}
        onClick={() => {
          if (Date.now() - suppressClick < 300) return
          selectEl(b, id)
        }}
        onDoubleClick={() => setRenaming(true)}
        onMouseEnter={() => setElState({ hover: id })}
        onMouseLeave={() => setElState({ hover: null })}
        style={{ paddingLeft: kids.length ? indent : indent + 24 }}
        className={`group/row ${ROW} ${selected ? 'bg-accent/20 text-white' : 'text-ink-400 hover:bg-white/[0.05]'} ${hidden.has(id) ? 'opacity-50' : ''}`}
        title={tagName(el)}
      >
        {kids.length > 0 && <Chevron open={isOpen} onClick={() => toggle(id)} />}
        <Icon size={12.5} className="shrink-0 text-ink-500" />
        {renaming ? (
          <input
            autoFocus
            aria-label={t('Element name')}
            defaultValue={name ?? ''}
            placeholder={kindLabel(kind)}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => {
              e.stopPropagation()
              if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
              if (e.key === 'Escape') setRenaming(false)
            }}
            onBlur={(e) => {
              setRenaming(false)
              act.rename(b, sec, id, e.target.value, names)
            }}
            className="min-w-0 flex-1 rounded bg-white/10 px-1 text-[11.5px] text-white outline-none"
          />
        ) : (
          <>
            <span className={`shrink-0 ${name ? 'text-ink-200' : 'text-ink-300'}`}>{name || kindLabel(kind)}</span>
            {!name && text && <span className="min-w-0 flex-1 truncate text-ink-500">{text}</span>}
            {(name || !text) && <span className="min-w-0 flex-1" />}
          </>
        )}
        <EyeBtn hidden={hidden.has(id)} label={hidden.has(id) ? t('Show in the editor') : t('Hide in the editor')} onClick={() => setElState((x) => ({ hidden: toggleIn(x.hidden, id) }))} />
      </div>
      {isOpen && (
        <div>
          {kids.map((k) => (
            <Row key={k} b={b} sec={sec} tr={tr} id={k} depth={depth + 1} names={names} sel={sel} hidden={hidden} open={open} toggle={toggle} />
          ))}
        </div>
      )}
    </div>
  )
})
