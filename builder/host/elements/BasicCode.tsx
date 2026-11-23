/**
 * Basic mode's editor of a code section (elementsSeam.Basic, patch 0024-basic-seam): instead of the
 * section's HTML and head code, the blocks the design is made of as friendly fields (fields.ts) —
 * headings, texts, buttons and links with their addresses, images with their alt text, form field
 * placeholders, video sources — repeated cards and items in groups. No markup is shown anywhere.
 *
 * Every edit goes through the element operations (actions.ts: act.setInner / act.setAttrs /
 * act.setAttrsMany), so it is one labelled history entry, undo and redo work, and the stored markup
 * changes only inside the edited range (ops.ts splices the source). Text with inline formatting is
 * written back by the same rules as Studio's text box (richtext.writeBack: the stored tags and
 * handlers stay byte for byte). A field takes the canvas selection on focus; selecting an element
 * on the canvas scrolls the panel to its field. A user who may not change code sections (no
 * unfiltered_html: the server ignores the edit) sees the fields read-only with a note.
 */
import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronRight, Image as ImageIcon, Link2, Search } from 'lucide-react'
import { useB } from '@/builder/store'
import type { Section } from '@/builder/types'
import { cn } from '@/builder/ui'
import type { CmsHost } from '../cms-host'
import { t } from '../i18n'
import { act, selectEl } from './actions'
import { attrError } from './attrs'
import { attr, elementKids, inner, wbc, type El, type Tree } from './html'
import { tree as parseTree } from './ops'
import { KEEP_ATTR, writeBack, type SrcMap } from './richtext'
import { richNodes } from './ContentPanel'
import { tbasic } from './basic-tr'
import { useElState } from './state'
import { extractFields, fieldFor, limitItems, pathTo, plainInner, summarize, type Field, type FieldKind, type Group, type Item } from './fields'

type B = ReturnType<typeof useB>

const host = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

/** The user may change code sections (the server ignores the edits of one who may not). */
export function canEditCode(): boolean {
  const h = host()
  return h ? !!h.boot?.caps?.unfilteredHtml : true
}

const PAGE = 60
const LABEL: Record<FieldKind, string> = { heading: 'Heading', text: 'Text', button: 'Button', link: 'Link', image: 'Image', input: 'Form field', video: 'Video' }
const inputCls = 'w-full rounded-lg border border-white/[0.08] bg-ink-850 px-3 text-[13px] text-ink-100 outline-none transition placeholder:text-ink-600 focus:border-accent/70 disabled:opacity-60'

/* ---------- The section list row ---------- */

const PLURAL: [keyof ReturnType<typeof summarize>['counts'], string, string][] = [
  ['headings', '{count} heading', '{count} headings'],
  ['texts', '{count} text', '{count} texts'],
  ['buttons', '{count} button', '{count} buttons'],
  ['links', '{count} link', '{count} links'],
  ['images', '{count} image', '{count} images'],
  ['fields', '{count} form field', '{count} form fields'],
  ['videos', '{count} video', '{count} videos'],
]

/** The row of a code section in Basic mode's section list: a name, and what is in it. */
export function basicSummary(s: Section): { title: string; sub?: string } | null {
  const sum = summarize(s.content.html ?? '')
  const parts = PLURAL.filter(([k]) => sum.counts[k] > 0)
    .slice(0, 3)
    .map(([k, one, many]) => tbasic(sum.counts[k] === 1 ? one : many, { count: sum.counts[k] }))
  const title = sum.title ? (sum.named ? tbasic(sum.title) : sum.title) : s.name
  return { title, sub: parts.join(' · ') }
}

/* ---------- Inputs ---------- */

/** A text box that commits on Enter or blur and goes back on Escape. */
function TextBox({ value, onCommit, onFocus, disabled, multi, placeholder, label, mono }: { value: string; onCommit: (v: string) => void; onFocus?: () => void; disabled?: boolean; multi?: boolean; placeholder?: string; label: string; mono?: boolean }) {
  const [draft, setDraft] = useState<string | null>(null)
  const area = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    const a = area.current
    if (!a) return
    a.style.height = 'auto'
    a.style.height = `${a.scrollHeight}px`
  }, [draft, value])
  const done = () => {
    if (draft !== null && draft !== value) onCommit(draft)
    setDraft(null)
  }
  const props = {
    value: draft ?? value,
    disabled,
    placeholder,
    spellCheck: !mono,
    'aria-label': label,
    onFocus,
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setDraft(e.target.value),
    onBlur: done,
    onKeyDown: (e: React.KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      e.stopPropagation()
      if (e.key === 'Enter') {
        e.preventDefault()
        ;(e.target as HTMLElement).blur()
      } else if (e.key === 'Escape') {
        setDraft(null)
        ;(e.target as HTMLElement).blur()
      }
    },
  }
  return multi ? <textarea {...props} ref={area} rows={2} className={cn(inputCls, 'block resize-none overflow-hidden py-2 leading-snug')} /> : <input {...props} className={cn(inputCls, 'h-9', mono && 'font-mono text-[12px]')} />
}

/** What a kept element shows inside a text box: a word, never its tag. */
const CHIP: Record<string, string> = { svg: 'Icon', img: 'Image', br: '↵' }

/** Text with inline formatting: the stored tags stay as they are (richtext.writeBack). */
function RichBox({ b, f, tr, el, disabled, onFocus }: { b: B; f: Field; tr: Tree; el: El; disabled: boolean; onFocus: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  const map = useRef<SrcMap>(new Map())
  const html = inner(tr, el)
  const fill = () => {
    if (!box.current) return
    map.current = new Map()
    box.current.replaceChildren(richNodes(tr, el, map.current))
    // The kept placeholders name what they are (an icon, an image), not their tag.
    for (const ph of Array.from(box.current.querySelectorAll(`[${KEEP_ATTR}]`))) {
      const m = /^<(\w+)>$/.exec(ph.textContent ?? '')
      if (m) ph.textContent = CHIP[m[1]] ? tbasic(CHIP[m[1]]) : '…'
    }
  }
  useEffect(fill, [html]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = () => {
    if (!box.current) return
    const next = writeBack(box.current, tr, map.current)
    if (next !== html) act.setInner(b, f.id, next)
  }
  return (
    <div
      ref={box}
      contentEditable={!disabled}
      suppressContentEditableWarning
      role="textbox"
      aria-label={tbasic(LABEL[f.kind])}
      aria-disabled={disabled || undefined}
      data-wb-basic-rich=""
      onFocus={onFocus}
      onBlur={save}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Enter') {
          e.preventDefault()
          box.current?.blur()
        } else if (e.key === 'Escape') {
          fill()
          box.current?.blur()
        }
      }}
      className={cn('min-h-9 rounded-lg border border-white/[0.08] bg-ink-850 px-3 py-2 text-[13px] leading-snug text-ink-100 outline-none transition focus:border-accent/70 [&_a]:underline', disabled && 'opacity-60')}
    />
  )
}

interface SearchHit {
  id: number
  title: string
  url: string
}

/** Pages and posts of the site by name (core search), so a link can point at one. */
function PagePicker({ onPick }: { onPick: (url: string) => void }) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<SearchHit[]>([])
  useEffect(() => {
    const h = host()
    if (!h || q.trim().length < 2) return setHits([])
    let off = false
    const tm = setTimeout(() => {
      h.rest
        .request<SearchHit[]>('GET', 'wp/v2/search', { query: { search: q.trim(), per_page: 8, type: 'post' } })
        .then((r) => !off && setHits(Array.isArray(r.data) ? r.data : []))
        .catch(() => !off && setHits([]))
    }, 250)
    return () => {
      off = true
      clearTimeout(tm)
    }
  }, [q])
  const local = (url: string) => {
    try {
      const u = new URL(url, location.href)
      return u.origin === location.origin ? u.pathname + u.search + u.hash : url
    } catch {
      return url
    }
  }
  return (
    <div className="mt-1.5 space-y-1.5">
      <label className="relative block">
        <Search size={13} className="absolute left-2.5 top-2.5 text-ink-500" />
        <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.stopPropagation()} placeholder={tbasic('Search pages and posts')} className={cn(inputCls, 'h-9 pl-8')} />
      </label>
      {hits.length > 0 && (
        <div className="max-h-44 overflow-y-auto rounded-lg border border-white/[0.08]">
          {hits.map((h) => (
            <button key={h.id + h.url} type="button" onClick={() => onPick(local(h.url))} className="flex w-full items-center justify-between gap-2 px-2.5 py-2 text-left text-[12px] hover:bg-white/[0.06]">
              <span className="truncate text-ink-100">{h.title || local(h.url)}</span>
              <span className="shrink-0 font-mono text-[10px] text-ink-500">{local(h.url)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** A link's address: typed, or one of the site's pages. */
function AddressBox({ b, f, disabled, onFocus, target = 'a' }: { b: B; f: Field; disabled: boolean; onFocus: () => void; target?: string }) {
  const [pick, setPick] = useState(false)
  const name = f.kind === 'video' ? 'src' : 'href'
  const value = (f.kind === 'video' ? f.src : f.href) ?? ''
  const id = f.kind === 'video' ? f.srcId : f.id
  const set = (v: string) => {
    const e = v ? attrError(name, v, target) : null
    if (e) return b.notify(t('This address is not allowed (javascript:, data: …).'))
    act.setAttrs(b, id, { [name]: v.trim() || null }, name === 'href' ? t('Link changed') : undefined)
  }
  return (
    <div>
      <span className="mb-1 flex items-center gap-1.5 text-[11.5px] text-ink-500">
        <Link2 size={11} /> {tbasic('Address')}
      </span>
      <TextBox value={value} label={tbasic('Address')} mono disabled={disabled} onFocus={onFocus} placeholder="https:// · /page/ · #id" onCommit={set} />
      {name === 'href' && !disabled && host() && (
        <>
          <button type="button" onClick={() => setPick((p) => !p)} className="mt-1.5 text-[11.5px] text-accent hover:underline">
            {tbasic('Choose a page of the site')}
          </button>
          {pick && (
            <PagePicker
              onPick={(u) => {
                set(u)
                setPick(false)
              }}
            />
          )}
        </>
      )}
    </div>
  )
}

/** An image: a preview, Replace (the media library), alt text. */
function ImageBox({ b, f, tr, el, disabled, onFocus }: { b: B; f: Field; tr: Tree; el: El; disabled: boolean; onFocus: () => void }) {
  const src = f.src ?? ''
  const replace = async () => {
    const h = host()
    if (!h?.media.pick) return
    onFocus()
    const a = await h.media.pick('image')
    if (!a?.url) return
    const edits: { id: string; attrs: Record<string, string | null> }[] = [
      { id: f.id, attrs: { src: a.url, srcset: null, sizes: null, width: a.width ? String(a.width) : null, height: a.height ? String(a.height) : null, ...(a.alt && !attr(el, 'alt') ? { alt: a.alt } : {}) } },
    ]
    // A picture's other sources would still show the old image: they point at the new one.
    const pic = el.parent?.tag === 'picture' ? el.parent : null
    if (pic) for (const s of elementKids(pic)) if (s.tag === 'source' && wbc(s)) edits.push({ id: wbc(s)!, attrs: { srcset: a.url, type: null, sizes: null } })
    act.setAttrsMany(b, edits, t('Image replaced'), f.id)
  }
  void tr
  const canPick = !disabled && !!host()?.media.pick
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-3">
        <span className="grid h-16 w-24 shrink-0 place-items-center overflow-hidden rounded-lg border border-white/[0.08] bg-[repeating-conic-gradient(#2a2f36_0_25%,#22262c_0_50%)] bg-[length:14px_14px]">
          {src ? <img src={src} alt="" className="max-h-full max-w-full object-contain" /> : <ImageIcon size={18} className="text-ink-500" />}
        </span>
        {canPick && (
          <button type="button" data-wb-basic-replace="" onClick={() => void replace()} className="flex h-9 items-center gap-1.5 rounded-lg bg-white/[0.07] px-3 text-[12.5px] text-ink-100 transition hover:bg-white/[0.12]">
            <ImageIcon size={14} /> {tbasic('Replace image')}
          </button>
        )}
      </div>
      <div>
        <span className="mb-1 block text-[11.5px] text-ink-500">{tbasic('Alternative text')}</span>
        <TextBox value={f.alt ?? ''} label={tbasic('Alternative text')} disabled={disabled} onFocus={onFocus} placeholder={tbasic('Describe the image (empty: decorative)')} onCommit={(v) => act.setAttrs(b, f.id, { alt: v }, t('Alternative text changed'))} />
      </div>
    </div>
  )
}

/* ---------- Fields and groups ---------- */

function FieldView({ b, f, tr, active, disabled, onFocus }: { b: B; f: Field; tr: Tree; active: boolean; disabled: boolean; onFocus: (id: string) => void }) {
  const el = tr.byId.get(f.id)
  if (!el) return null
  const focus = () => onFocus(f.id)
  const label = f.kind === 'video' && f.tag !== 'video' && f.tag !== 'audio' ? tbasic('Embed') : tbasic(LABEL[f.kind])
  const commitText = (v: string) => act.setInner(b, f.id, plainInner(inner(tr, el), v))
  return (
    <div data-wb-basic-field={f.id} data-wb-basic-kind={f.kind} className={cn('rounded-xl p-2.5 ring-1 transition-colors', active ? 'bg-accent/10 ring-accent/50' : 'bg-white/[0.025] ring-white/[0.05]')}>
      <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-[.08em] text-ink-500">{label}</div>
      <div className="space-y-2.5">
        {(f.kind === 'heading' || f.kind === 'text' || f.kind === 'button' || f.kind === 'link') &&
          (f.edit === 'plain' ? (
            <TextBox key={f.id + '\u0000' + f.text} value={f.text} multi={f.kind === 'text'} label={label} disabled={disabled} onFocus={focus} onCommit={commitText} />
          ) : f.edit === 'rich' ? (
            <RichBox key={f.id + '\u0000' + inner(tr, el)} b={b} f={f} tr={tr} el={el} disabled={disabled} onFocus={focus} />
          ) : f.text ? (
            <div className="px-0.5 text-[13px] text-ink-300" onClick={focus}>
              {f.text}
            </div>
          ) : null)}
        {f.href !== null && <AddressBox key={f.id + '\u0000' + f.href} b={b} f={f} disabled={disabled} onFocus={focus} />}
        {f.kind === 'image' && <ImageBox key={f.id + '\u0000' + f.src} b={b} f={f} tr={tr} el={el} disabled={disabled} onFocus={focus} />}
        {f.kind === 'input' && <TextBox key={f.id + '\u0000' + f.placeholder} value={f.placeholder ?? ''} label={tbasic('Placeholder')} disabled={disabled} onFocus={focus} onCommit={(v) => act.setAttrs(b, f.id, { placeholder: v })} />}
        {f.kind === 'video' && <AddressBox key={f.srcId + '\u0000' + f.src} b={b} f={f} disabled={disabled} onFocus={focus} target={f.tag} />}
      </div>
    </div>
  )
}

const groupTitle = (g: Group) => (g.noun === 'Links' ? tbasic('Links') : g.noun === 'Buttons' ? tbasic('Buttons') : tbasic(`${g.noun} {n}`, { n: g.n }))

function Items({ b, items, tr, sel, open, toggle, disabled, onFocus }: { b: B; items: Item[]; tr: Tree; sel: string | null; open: ReadonlySet<string>; toggle: (g: Group) => void; disabled: boolean; onFocus: (id: string) => void }) {
  return (
    <div className="space-y-2.5">
      {items.map((i) =>
        i.type === 'field' ? (
          <FieldView key={i.id} b={b} f={i} tr={tr} active={sel === i.id} disabled={disabled} onFocus={onFocus} />
        ) : (
          <div key={i.key} data-wb-basic-group={i.key} className="rounded-xl ring-1 ring-white/[0.06]">
            <button type="button" aria-expanded={open.has(i.key)} onClick={() => toggle(i)} className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left transition hover:bg-white/[0.04]">
              <ChevronRight size={14} className={cn('shrink-0 text-ink-500 transition-transform', open.has(i.key) && 'rotate-90')} />
              <span className="shrink-0 text-[13px] font-medium text-ink-100">{groupTitle(i)}</span>
              <span className="min-w-0 flex-1 truncate text-[12px] text-ink-500">{i.snippet}</span>
              <span className="shrink-0 text-[11px] text-ink-500">{i.count}</span>
            </button>
            {open.has(i.key) && (
              <div className="px-2.5 pb-2.5">
                <Items b={b} items={i.items} tr={tr} sel={sel} open={open} toggle={toggle} disabled={disabled} onFocus={onFocus} />
              </div>
            )}
          </div>
        ),
      )}
    </div>
  )
}

/* ---------- The panel ---------- */

export function BasicCode({ s, readOnly }: { s: Section; readOnly?: boolean }) {
  const b = useB()
  const es = useElState()
  const root = useRef<HTMLDivElement>(null)
  const html = s.content.html ?? ''
  const items = useMemo(() => extractFields(html), [html])
  const tree = useMemo(() => parseTree(html), [html])
  const [limit, setLimit] = useState(PAGE)
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const disabled = readOnly ?? !canEditCode()
  const own = useRef<string | null>(null)
  const shown = useMemo(() => limitItems(items, limit), [items, limit])
  const toggle = (g: Group) => {
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(g.key)) n.delete(g.key)
      else n.add(g.key)
      return n
    })
    if (g.id && !disabled) {
      own.current = g.id
      selectEl(b, g.id)
    }
  }
  const onFocus = (id: string) => {
    own.current = id
    selectEl(b, id)
  }
  // An element selected on the canvas: open its groups, show its field and scroll to it.
  const sel = es.sel
  useEffect(() => {
    if (!sel || own.current === sel) {
      own.current = null
      return
    }
    const f = fieldFor(items, tree, sel)
    if (!f) return
    const path = pathTo(items, f.id) ?? []
    if (path.length) setOpen((o) => (path.every((k) => o.has(k)) ? o : new Set([...o, ...path])))
    const at = flatIndex(items, f.id)
    if (at >= limit) setLimit(at + 1)
    const tm = window.setTimeout(() => {
      const node = root.current?.querySelector<HTMLElement>(`[data-wb-basic-field="${CSS.escape(f.id)}"]`)
      if (!node) return
      node.scrollIntoView({ block: 'center', behavior: 'smooth' })
      node.classList.remove('flash')
      void node.offsetWidth
      node.classList.add('flash')
    }, 60)
    return () => window.clearTimeout(tm)
  }, [sel]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div ref={root} data-wb-basic-code="">
      <section className="border-t border-white/[0.06] px-5 py-5">
        <h3 className="mb-3.5 text-[11px] font-semibold uppercase tracking-[.1em] text-ink-500">{tbasic('On this section')}</h3>
        {disabled && (
          <p data-wb-basic-readonly="" className="mb-3 rounded-lg bg-amber-400/10 px-3 py-2 text-[12px] leading-relaxed text-amber-200 ring-1 ring-amber-300/20">
            {tbasic('Your role may not change this part of the design, so the fields are read-only here.')}
          </p>
        )}
        {items.length === 0 ? (
          <p className="text-[12.5px] leading-relaxed text-ink-500">
            {tbasic('Nothing to edit here yet.')} {tbasic("This section is made of the design's own layout. To change more of it, switch to Studio.")}
          </p>
        ) : (
          <Items b={b} items={shown.items} tr={tree} sel={sel} open={open} toggle={toggle} disabled={disabled} onFocus={onFocus} />
        )}
        {shown.total > limit && (
          <button type="button" data-wb-basic-more="" onClick={() => setLimit((l) => l + PAGE)} className="mt-3 h-9 w-full rounded-lg bg-white/[0.05] text-[12.5px] text-ink-200 transition hover:bg-white/[0.09]">
            {tbasic('Show {count} more', { count: Math.min(PAGE, shown.total - limit) })}
          </button>
        )}
        <p className="mt-4 text-[12px] text-ink-500">
          {tbasic('Need more control?')}{' '}
          <button type="button" data-wb-basic-studio="" onClick={() => b.setMode('studio')} className="text-accent hover:underline">
            {tbasic('Switch to Studio')}
          </button>
        </p>
      </section>
    </div>
  )
}

/** Position of field `id` among the section's fields in document order. */
function flatIndex(items: Item[], id: string): number {
  let n = 0
  const walk = (list: Item[]): number => {
    for (const i of list) {
      if (i.type === 'field') {
        if (i.id === id) return n
        n++
      } else {
        const r = walk(i.items)
        if (r >= 0) return r
      }
    }
    return -1
  }
  return walk(items)
}
