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
import { ChevronRight, Image as ImageIcon, Link2 } from 'lucide-react'
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
import { PagePicker, richNodes } from './ContentPanel'
import { tbasic } from './basic-tr'
import { useElState } from './state'
import { useLiveEdit } from './livefield'
import { KIND_ICON, blockOf, elementName, kindOf, namesOf, topBlocks } from './kinds'
import { tk } from './kinds-tr'
import { markupReadOnly } from './trust'
import { extractFields, fieldFor, limitItems, pathTo, plainInner, summarize, type Field, type FieldKind, type Group, type Item } from './fields'

type B = ReturnType<typeof useB>

const host = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

/** The user may change code sections (the server ignores the edits of one who may not): trust.ts markupReadOnly. */
export const canEditCode = (): boolean => !markupReadOnly()

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
  return { title: blockTitle(s) ?? (sum.title ? (sum.named ? tbasic(sum.title) : sum.title) : s.name), sub: parts.join(' · ') }
}

/**
 * A code section made of one block is called what the navigator calls that block (kinds.ts
 * elementName: the user's rename, its heading, landmark, label …); null when it has several blocks
 * or nothing but the generic "Section N" to say.
 */
function blockTitle(s: Section): string | null {
  const html = s.content.html ?? ''
  if (!html.trim()) return null
  const tr = parseTree(html)
  const blocks = topBlocks(tr)
  if (blocks.length !== 1) return null
  const el = tr.byId.get(blocks[0])
  if (!el) return null
  const name = elementName(tr, el, namesOf(s.content.names))
  return name === tk('Section {n}', { n: 1 }) ? null : name
}

/* ---------- Inputs ---------- */

/**
 * A text box: the canvas follows while typing (livefield.ts: debounced, one history entry for the
 * whole typing session), Enter or blur ends the session and Escape goes back to what it was.
 * `live` false: written on Enter / blur only (an address that would load half of what is typed).
 */
function TextBox({ value, onCommit, onFocus, disabled, multi, placeholder, label, mono, live = true }: { value: string; onCommit: (v: string, live: boolean) => boolean | void; onFocus?: () => void; disabled?: boolean; multi?: boolean; placeholder?: string; label: string; mono?: boolean; live?: boolean }) {
  const [draft, setDraft] = useState<string | null>(null)
  const area = useRef<HTMLTextAreaElement>(null)
  const now = useRef(value)
  now.current = value
  const edit = useLiveEdit<string>(
    (v, isLive) => {
      if (v === now.current || (isLive && !live)) return false
      return onCommit(v, isLive)
    },
    () => now.current,
  )
  useEffect(() => {
    const a = area.current
    if (!a) return
    a.style.height = 'auto'
    a.style.height = `${a.scrollHeight}px`
  }, [draft, value])
  const props = {
    value: draft ?? value,
    disabled,
    placeholder,
    spellCheck: !mono,
    'aria-label': label,
    onFocus,
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      const v = e.target.value
      setDraft(v)
      edit.touch(() => v)
    },
    onBlur: () => {
      edit.commit()
      setDraft(null)
    },
    onKeyDown: (e: React.KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      e.stopPropagation()
      if (e.key === 'Enter') {
        e.preventDefault()
        ;(e.target as HTMLElement).blur()
      } else if (e.key === 'Escape') {
        edit.revert()
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
  // What the box was drawn from: edits are written back against it (the tree of that moment), whole,
  // so the box is not redrawn (and the caret kept) while the canvas follows the typing.
  const base = useRef({ tr, el, html })
  const focused = useRef(false)
  const paint = () => {
    if (!box.current) return
    map.current = new Map()
    box.current.replaceChildren(richNodes(base.current.tr, base.current.el, map.current))
    // The kept placeholders name what they are (an icon, an image), not their tag.
    for (const ph of Array.from(box.current.querySelectorAll(`[${KEEP_ATTR}]`))) {
      const m = /^<(\w+)>$/.exec(ph.textContent ?? '')
      if (m) ph.textContent = CHIP[m[1]] ? tbasic(CHIP[m[1]]) : '…'
    }
  }
  useEffect(() => {
    if (focused.current) return
    base.current = { tr, el, html }
    paint()
  }, [html]) // eslint-disable-line react-hooks/exhaustive-deps
  const read = () => (box.current ? writeBack(box.current, base.current.tr, map.current) : base.current.html)
  const edit = useLiveEdit<string>(
    (next) => (next === html ? false : act.setInner(b, f.id, next)),
    () => base.current.html,
  )
  return (
    <div
      ref={box}
      contentEditable={!disabled}
      suppressContentEditableWarning
      role="textbox"
      aria-label={tbasic(LABEL[f.kind])}
      aria-disabled={disabled || undefined}
      data-wb-basic-rich=""
      onFocus={() => {
        focused.current = true
        onFocus()
      }}
      onInput={() => edit.touch(read)}
      onBlur={() => {
        focused.current = false
        edit.commit()
      }}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Enter') {
          e.preventDefault()
          box.current?.blur()
        } else if (e.key === 'Escape') {
          edit.revert()
          paint()
          box.current?.blur()
        }
      }}
      className={cn('min-h-9 rounded-lg border border-white/[0.08] bg-ink-850 px-3 py-2 text-[13px] leading-snug text-ink-100 outline-none transition focus:border-accent/70 [&_a]:underline', disabled && 'opacity-60')}
    />
  )
}

/** A link's address: typed, or one of the site's pages. */
function AddressBox({ b, f, disabled, onFocus, target = 'a' }: { b: B; f: Field; disabled: boolean; onFocus: () => void; target?: string }) {
  const [pick, setPick] = useState(false)
  const name = f.kind === 'video' ? 'src' : 'href'
  const value = (f.kind === 'video' ? f.src : f.href) ?? ''
  const id = f.kind === 'video' ? f.srcId : f.id
  const set = (v: string, live = false): boolean => {
    const e = v ? attrError(name, v, target) : null
    if (e) {
      if (!live) b.notify(t('This address is not allowed (javascript:, data: …).'))
      return false
    }
    return act.setAttrs(b, id, { [name]: v.trim() || null }, name === 'href' ? t('Link changed') : undefined)
  }
  return (
    <div>
      <span className="mb-1 flex items-center gap-1.5 text-[11.5px] text-ink-500">
        <Link2 size={11} /> {tbasic('Address')}
      </span>
      <TextBox value={value} label={tbasic('Address')} mono live={name === 'href'} disabled={disabled} onFocus={onFocus} placeholder="https:// · /page/ · #id" onCommit={set} />
      {name === 'href' && !disabled && host() && (
        <>
          <button type="button" onClick={() => setPick((p) => !p)} className="mt-1.5 text-[11.5px] text-accent hover:underline">
            {tbasic('Choose a page of the site')}
          </button>
          {pick && (
            <PagePicker
              big
              autoFocus
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
            <TextBox key={f.id} value={f.text} multi={f.kind === 'text'} label={label} disabled={disabled} onFocus={focus} onCommit={commitText} />
          ) : f.edit === 'rich' ? (
            <RichBox key={f.id} b={b} f={f} tr={tr} el={el} disabled={disabled} onFocus={focus} />
          ) : f.text ? (
            <div className="px-0.5 text-[13px] text-ink-300" onClick={focus}>
              {f.text}
            </div>
          ) : null)}
        {f.href !== null && <AddressBox key={f.id} b={b} f={f} disabled={disabled} onFocus={focus} />}
        {f.kind === 'image' && <ImageBox key={f.id + '\u0000' + f.src} b={b} f={f} tr={tr} el={el} disabled={disabled} onFocus={focus} />}
        {f.kind === 'input' && <TextBox key={f.id} value={f.placeholder ?? ''} label={tbasic('Placeholder')} disabled={disabled} onFocus={focus} onCommit={(v) => act.setAttrs(b, f.id, { placeholder: v })} />}
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
  const names = useMemo(() => namesOf(s.content.names), [s.content.names])
  // The fields under the name of the block (top-level element) they are in, as the navigator shows it.
  const parts = useMemo(() => byBlock(shown.items, tree), [shown.items, tree])
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
        ) : parts.length > 1 ? (
          <div className="space-y-5">
            {parts.map((p, n) => {
              const el = p.id ? tree.byId.get(p.id) : undefined
              const Icon = el ? KIND_ICON[kindOf(tree, el)] : null
              return (
                <div key={p.id ?? `@${n}`} data-wb-basic-block={p.id ?? ''}>
                  {el && (
                    <button type="button" onClick={() => selectEl(b, p.id!)} className="mb-2 flex w-full items-center gap-2 text-left text-[12.5px] font-semibold text-ink-200 hover:text-white">
                      {Icon && <Icon size={14} className="shrink-0 text-ink-500" />}
                      <span className="truncate">{elementName(tree, el, names)}</span>
                    </button>
                  )}
                  <Items b={b} items={p.items} tr={tree} sel={sel} open={open} toggle={toggle} disabled={disabled} onFocus={onFocus} />
                </div>
              )
            })}
          </div>
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

/** The first element id an item stands for (a field, a group or its first field). */
function firstId(i: Item): string | null {
  if (i.type === 'field') return i.id
  if (i.id) return i.id
  for (const x of i.items) {
    const r = firstId(x)
    if (r) return r
  }
  return null
}

/** Consecutive items split by the top-level block they are in (kinds.blockOf). */
function byBlock(items: Item[], tr: Tree): { id: string | null; items: Item[] }[] {
  const out: { id: string | null; items: Item[] }[] = []
  for (const i of items) {
    const fid = firstId(i)
    const el = fid ? tr.byId.get(fid) : undefined
    const blk = el ? blockOf(el) : null
    const id = blk ? wbc(blk) : null
    const last = out[out.length - 1]
    if (last && last.id === id) last.items.push(i)
    else out.push({ id, items: [i] })
  }
  return out
}
