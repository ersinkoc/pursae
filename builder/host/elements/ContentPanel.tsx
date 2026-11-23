/**
 * The element editor's Content tab (plan Faz 4.3, D2): what the selected element says and where it
 * points, by its type — rich text, a link (a page of the site or an address, new tab), an image
 * (media library, alt, object fit), an icon (paste or upload an SVG, or pick one of the editor's
 * icons), a video or embed source, a form field (name, type, placeholder, required, options), the
 * tag (h2 → h3, div → section), the HTML inside (embeds) and the attributes (id, aria-*, data-*,
 * title, rel …; never on… handlers). Every change is one history entry. Without a selected
 * element the tab shows the code section's own content (its HTML and head code).
 */
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { flushSync } from 'react-dom'
import { createRoot } from 'react-dom/client'
import { Bold, ExternalLink, Image as ImageIcon, Italic, Link2, Plus, RemoveFormatting, Search, Upload, X } from 'lucide-react'
import { Ctl, Panel, inputCls } from '@/builder/StudioPanel'
import { useB } from '@/builder/store'
import type { Section } from '@/builder/types'
import { cn } from '@/builder/ui'
import { ICON_SET } from '@/builder/icons'
import { cleanSvgCode } from '@/builder/svgTint'
import type { CmsHost } from '../cms-host'
import { t } from '../i18n'
import { act, commit, latest, pageOf, style as styleAct } from './actions'
import { attrError, tagChoices, urlOK, type AttrError } from './attrs'
import { attr, decode, elementKids, inner, linesApart, outer, ownText, VOID, wbc, type El, type Tree } from './html'
import { KEEP_ATTR, writeBack, type SrcMap } from './richtext'
import { kindLabel, kindOf } from './kinds'
import { locate, movable } from './ops'
import { useLiveEdit } from './livefield'
import { applyCode, checkMarkup, codeView, type CodeProblem, type Part } from './codeops'
import { CodeEditor } from './CodeEditor'
import { tc } from './code-tr'
import { frameEl } from './geometry'
import { frameInfo, getElState, useElState } from './state'
import { markupReadOnly } from './trust'
import { variantOf, type LocalSection } from './local'

type B = ReturnType<typeof useB>

const host = () => (typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined) : undefined)

function errText(e: AttrError): string {
  switch (e) {
    case 'name':
      return t('Not an attribute name.')
    case 'handler':
      return t('Event handlers (on…) cannot be added here.')
    case 'reserved':
      return t('This attribute is set elsewhere (classes and style in the Style tab).')
    case 'url':
      return t('This address is not allowed (javascript:, data: …).')
    case 'id':
      return t('An id starts with a letter and holds letters, digits, -, _, : or . only.')
    case 'length':
      return t('The value is too long.')
    default:
      return t('This value is not allowed.')
  }
}

/**
 * A text field: the canvas follows while typing (livefield.ts: debounced, one history entry per
 * typing session); Enter or blur ends the session, Escape takes it back. `live` false: written on
 * Enter / blur only (an address that would load the half-typed value, a list rewritten as markup).
 */
function Field({ value, onCommit, placeholder, mono, name, area, live = true }: { value: string; onCommit: (v: string, live: boolean) => boolean | void; placeholder?: string; mono?: boolean; name?: string; area?: boolean; live?: boolean }) {
  const [draft, setDraft] = useState<string | null>(null)
  const now = useRef(value)
  now.current = value
  const edit = useLiveEdit<string>(
    (v, isLive) => {
      if (v === now.current) return false
      if (isLive && !live) return false
      return onCommit(v, isLive)
    },
    () => now.current,
  )
  const props = {
    value: draft ?? value,
    placeholder,
    name,
    spellCheck: false,
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
      if (e.key === 'Enter' && !area) (e.target as HTMLInputElement).blur()
      if (e.key === 'Escape') {
        edit.revert()
        setDraft(null)
        ;(e.target as HTMLInputElement).blur()
      }
    },
  }
  return area ? (
    <textarea {...props} rows={6} className={cn(inputCls, 'h-auto w-full py-1.5 font-mono text-[11px]')} />
  ) : (
    <input {...props} className={cn(inputCls, 'w-full placeholder:text-ink-600', mono && 'font-mono text-[11px]')} />
  )
}

/** Sets one attribute, refusing what the rules refuse (with a message, unless the value is a draft being typed). */
function setOne(b: B, id: string, tag: string, name: string, value: string | null, label?: string, live = false): boolean {
  if (value !== null) {
    const e = attrError(name, value, tag)
    if (e) {
      if (!live) b.notify(errText(e))
      return false
    }
  }
  return act.setAttrs(b, id, { [name]: value }, label)
}

/* ---------- The tab ---------- */

export function ContentTab({ s, fallback }: { s: Section; fallback: ReactNode }) {
  const b = useB()
  const es = useElState()
  const doc = latest.doc ?? b.doc
  const at = es.sel ? locate(pageOf(doc), es.sel) : null
  if (!at || at.sec !== s.id) return <>{fallback}</>
  const { tree: tr, el } = at
  const id = es.sel!
  const fe = frameInfo.doc ? frameEl(frameInfo.doc, id) : null
  const display = fe ? (fe.ownerDocument.defaultView?.getComputedStyle(fe).display ?? undefined) : undefined
  const kind = kindOf(tr, el, display)
  const tag = el.tag
  const choices = tagChoices(tag)
  const isText = !el.foreign && (kind === 'heading' || kind === 'text' || ((kind === 'link' || kind === 'button') && !elementKids(el).some((k) => !['span', 'b', 'strong', 'em', 'i', 'br'].includes(k.tag))) || (ownText(tr, el) && !elementKids(el).some((k) => k.tag === 'div' || k.tag === 'p')))
  return (
    <div data-wb-content-panel={id} data-wb-content-kind={kind}>
      <div className="flex items-center justify-between gap-2 border-b border-white/[0.07] px-4 py-3">
        <span className="text-[12px] text-ink-300">
          {kindLabel(kind)} · <span className="font-mono text-ink-400">&lt;{tag}&gt;</span>
        </span>
        {choices.length > 0 && movable(el) && (
          <select aria-label={t('Tag')} name="wb-tag" value={tag} onChange={(e) => act.setTag(b, id, e.target.value)} className={cn(inputCls, 'w-[110px] font-mono text-[11px]')} data-wb-tag-select="">
            {[tag, ...choices.filter((c) => c !== tag)].map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        )}
      </div>
      {isText && <RichPanel key={id} b={b} id={id} html={inner(tr, el)} tr={tr} el={el} />}
      {tag === 'a' && <LinkPanel b={b} id={id} el={el} />}
      {tag === 'img' && <ImagePanel b={b} id={id} el={el} tr={tr} sec={s as LocalSection} />}
      {tag === 'svg' && <IconPanel b={b} id={id} tr={tr} el={el} />}
      {(tag === 'video' || tag === 'audio' || tag === 'iframe' || tag === 'embed' || tag === 'source') && <MediaPanel b={b} id={id} el={el} />}
      {(tag === 'input' || tag === 'textarea' || tag === 'select' || tag === 'button' || tag === 'label' || tag === 'form') && <FieldPanel b={b} id={id} el={el} tr={tr} />}
      {!el.foreign && <CodePanel key={id} b={b} id={id} tr={tr} el={el} inside={tag !== 'svg' && !VOID.has(tag) && !['img', 'input', 'hr', 'br', 'video', 'audio', 'iframe', 'source', 'textarea', 'select'].includes(tag)} open={!isText && !elementKids(el).length && tag !== 'svg'} sel={id} />}
      <AttrsPanel b={b} id={id} el={el} />
      <SectionCode key={s.id} b={b} s={s} sel={id} />
    </div>
  )
}

/* ---------- Text ---------- */

/** Elements the text box shows as themselves (no attributes; a link keeps a safe address). */
const RICH_TAGS = new Set(['b', 'strong', 'i', 'em', 'u', 's', 'a', 'br', 'span', 'code', 'sub', 'sup'])

/**
 * The text box's DOM, built from the source tree node by node — the design's markup is never
 * parsed in the editor's document: allowlisted elements without attributes (a link keeps its
 * address when it is safe), text as text, anything else (an image, an icon, code) a kept
 * placeholder written back as it is (richtext.writeBack).
 */
export function richNodes(tr: Tree, el: El, map: SrcMap): DocumentFragment {
  const f = document.createDocumentFragment()
  const add = (parent: Node, kids: El['children']) => {
    for (const k of kids) {
      if (k.type === 'text') {
        const n = document.createTextNode(decode(tr.src.slice(k.start, k.end)))
        map.set(n, k)
        parent.appendChild(n)
      } else if (k.type === 'el' && RICH_TAGS.has(k.tag) && !k.foreign) {
        const e = document.createElement(k.tag)
        const href = k.tag === 'a' ? attr(k, 'href') : null
        if (href && urlOK(href)) e.setAttribute('href', href)
        // An inline tag the design shows as a block (Tailwind block, a flex child): drawn apart here too.
        if (k.tag !== 'br' && linesApart(k)) e.style.display = 'block'
        map.set(e, k)
        parent.appendChild(e)
        if (k.tag !== 'br') add(e, k.children)
      } else if (k.type === 'el' || k.type === 'comment') {
        const ph = document.createElement('span')
        ph.setAttribute(KEEP_ATTR, '')
        ph.contentEditable = 'false'
        if (k.type !== 'el') ph.hidden = true
        else {
          ph.textContent = `<${k.tag}>`
          ph.className = 'mx-0.5 rounded bg-white/10 px-1 font-mono text-[10.5px] text-ink-400'
        }
        map.set(ph, k)
        parent.appendChild(ph)
      }
    }
  }
  add(f, el.children)
  return f
}

function RichPanel({ b, id, html, tr, el }: { b: B; id: string; html: string; tr: Tree; el: El }) {
  const box = useRef<HTMLDivElement>(null)
  const map = useRef<SrcMap>(new Map())
  // What the box was drawn from: its edits are written back against this (the tree of that moment),
  // whole, so the box need not be redrawn (and lose the caret) while the canvas follows the typing.
  const base = useRef({ tr, el, html })
  const focused = useRef(false)
  const paint = () => {
    if (!box.current) return
    map.current = new Map()
    box.current.replaceChildren(richNodes(base.current.tr, base.current.el, map.current))
  }
  const fill = () => {
    base.current = { tr, el, html }
    paint()
  }
  useEffect(() => {
    if (!focused.current) fill()
  }, [html]) // eslint-disable-line react-hooks/exhaustive-deps
  const read = () => (box.current ? writeBack(box.current, base.current.tr, map.current) : base.current.html)
  const edit = useLiveEdit<string>(
    (next) => (next === html ? false : act.setInner(b, id, next)),
    () => base.current.html,
  )
  const cmd = (c: string, v?: string) => {
    box.current?.focus()
    document.execCommand(c, false, v)
  }
  const btn = (label: string, icon: ReactNode, on: () => void) => (
    <button type="button" title={label} aria-label={label} onMouseDown={(e) => e.preventDefault()} onClick={on} className="grid size-7 place-items-center rounded text-ink-300 hover:bg-white/10 hover:text-white">
      {icon}
    </button>
  )
  return (
    <Panel title={t('Text')}>
      <div className="flex gap-0.5">
        {btn(t('Bold'), <Bold size={13} />, () => cmd('bold'))}
        {btn(t('Italic'), <Italic size={13} />, () => cmd('italic'))}
        {btn(t('Link'), <Link2 size={13} />, () => {
          const url = window.prompt(t('Link address'), 'https://')
          if (url && urlOK(url)) cmd('createLink', url)
        })}
        {btn(t('Clear formatting'), <RemoveFormatting size={13} />, () => cmd('removeFormat'))}
      </div>
      <div
        ref={box}
        contentEditable
        suppressContentEditableWarning
        data-wb-rich=""
        onFocus={() => (focused.current = true)}
        onInput={() => edit.touch(read)}
        onBlur={() => {
          focused.current = false
          edit.commit()
        }}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === 'Escape') {
            edit.revert()
            paint()
            box.current?.blur()
          }
        }}
        className="min-h-[60px] rounded-[5px] border border-white/[0.08] bg-ink-850 px-2 py-1.5 text-[12.5px] leading-relaxed text-ink-100 outline-none focus:border-accent/70 [&_a]:underline"
      />
    </Panel>
  )
}

/* ---------- Link ---------- */

interface SearchHit {
  id: number
  title: string
  url: string
  subtype?: string
}

/** A site address as the link stores it: a path on this site, else the full URL. */
function localUrl(url: string): string {
  try {
    const u = new URL(url, location.href)
    return u.origin === location.origin ? u.pathname + u.search + u.hash : url
  } catch {
    return url
  }
}

/**
 * Pages and posts of the site by name (core search), so a link can point at one: the Link panel
 * (Studio) and Basic mode's address fields share it. `big` is Basic mode's larger box.
 */
export function PagePicker({ onPick, big, autoFocus }: { onPick: (url: string) => void; big?: boolean; autoFocus?: boolean }) {
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
  return (
    <div className={big ? 'mt-1.5 space-y-1.5' : 'space-y-1'}>
      <label className="relative block">
        <Search size={big ? 13 : 12} className={big ? 'absolute left-2.5 top-2.5 text-ink-500' : 'absolute left-2 top-2 text-ink-500'} />
        <input autoFocus={autoFocus} value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.stopPropagation()} placeholder={t('Search pages and posts')} className={cn(inputCls, big ? 'h-9 w-full rounded-lg pl-8' : 'w-full pl-6')} data-wb-link-search="" />
      </label>
      {hits.length > 0 && (
        <div className={cn('overflow-y-auto border border-white/[0.08]', big ? 'max-h-44 rounded-lg' : 'max-h-48 rounded-md')}>
          {hits.map((h) => (
            <button
              key={h.id + h.url}
              type="button"
              onClick={() => {
                onPick(localUrl(h.url))
                setQ('')
              }}
              className="flex w-full items-center justify-between gap-2 px-2 py-1.5 text-left text-[11.5px] hover:bg-white/[0.06]"
            >
              <span className="truncate text-ink-100">{h.title || localUrl(h.url)}</span>
              <span className="shrink-0 font-mono text-[10px] text-ink-500">{localUrl(h.url)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function LinkPanel({ b, id, el }: { b: B; id: string; el: El }) {
  const href = attr(el, 'href') ?? ''
  const blank = attr(el, 'target') === '_blank'
  return (
    <Panel title={t('Link')}>
      <Ctl label={t('Address')} stack>
        <Field value={href} name="href" mono placeholder="https:// · /page/ · #id" onCommit={(v, live) => setOne(b, id, 'a', 'href', v.trim() || null, t('Link changed'), live)} />
      </Ctl>
      <Ctl label={t('Link to a page of the site')} stack>
        <PagePicker onPick={(u) => setOne(b, id, 'a', 'href', u, t('Link changed'))} />
      </Ctl>
      <label className="flex items-center gap-2 text-[11.5px] text-ink-300">
        <input
          type="checkbox"
          checked={blank}
          name="target-blank"
          onChange={(e) => {
            const rel = attr(el, 'rel')
            act.setAttrs(b, id, e.target.checked ? { target: '_blank', rel: rel ? (rel.includes('noopener') ? rel : `${rel} noopener`) : 'noopener' } : { target: null, rel: rel === 'noopener' || rel === 'noopener noreferrer' ? null : rel }, t('Link changed'))
          }}
        />
        <ExternalLink size={12} /> {t('Open in a new tab')}
      </label>
    </Panel>
  )
}

/* ---------- Image ---------- */

function ImagePanel({ b, id, el, tr, sec }: { b: B; id: string; el: El; tr: Tree; sec: LocalSection }) {
  const src = attr(el, 'src') ?? ''
  const fit = variantOf(sec.local, id, '', '')['object-fit'] ?? ''
  const replace = async () => {
    const h = host()
    if (!h?.media.pick) return
    const a = await h.media.pick('image')
    if (!a?.url) return
    const edits: { id: string; attrs: Record<string, string | null> }[] = [
      { id, attrs: { src: a.url, srcset: null, sizes: null, width: a.width ? String(a.width) : null, height: a.height ? String(a.height) : null, ...(a.alt && !attr(el, 'alt') ? { alt: a.alt } : {}) } },
    ]
    // A picture's other sources would still show the old image: they point at the new one.
    const pic = el.parent?.tag === 'picture' ? el.parent : null
    if (pic) for (const s of elementKids(pic)) if (s.tag === 'source' && wbc(s)) edits.push({ id: wbc(s)!, attrs: { srcset: a.url, type: null, sizes: null } })
    act.setAttrsMany(b, edits, t('Image replaced'), id)
  }
  void tr
  return (
    <Panel title={t('Image')}>
      <div className="grid h-28 place-items-center overflow-hidden rounded-md border border-white/[0.08] bg-[repeating-conic-gradient(#2a2f36_0_25%,#22262c_0_50%)] bg-[length:16px_16px]">
        {src ? <img src={src} alt="" className="max-h-full max-w-full object-contain" /> : <ImageIcon size={20} className="text-ink-500" />}
      </div>
      <button type="button" onClick={() => void replace()} data-wb-image-replace="" className="flex h-8 w-full items-center justify-center gap-1.5 rounded-[6px] bg-white/[0.08] text-[12px] text-ink-100 hover:bg-white/[0.12]">
        <ImageIcon size={13} /> {t('Replace from the media library')}
      </button>
      <Ctl label={t('Address')} stack>
        <Field value={src} name="src" mono live={false} onCommit={(v) => act.setAttrs(b, id, attrError('src', v, 'img') ? {} : { src: v || null, srcset: null, sizes: null }, t('Image replaced'))} />
      </Ctl>
      <Ctl label={t('Alternative text')} stack>
        <Field value={attr(el, 'alt') ?? ''} name="alt" placeholder={t('Describe the image (empty: decorative)')} onCommit={(v) => act.setAttrs(b, id, { alt: v }, t('Alternative text changed'))} />
      </Ctl>
      {attr(el, 'srcset') && <div className="text-[10.5px] text-ink-500">{t('This image has responsive sources (srcset); replacing it removes them.')}</div>}
      <Ctl label={t('Object fit')}>
        <select value={fit} name="object-fit" onChange={(e) => styleAct.set(b, id, '', '', { 'object-fit': e.target.value || null })} className={cn(inputCls, 'w-[130px]')}>
          {['', 'fill', 'contain', 'cover', 'none', 'scale-down'].map((o) => (
            <option key={o} value={o}>
              {o || '—'}
            </option>
          ))}
        </select>
      </Ctl>
    </Panel>
  )
}

/* ---------- Icon ---------- */

/** A lucide icon's SVG markup (rendered once, off screen). */
function iconMarkup(key: string): string {
  const I = ICON_SET[key]?.I
  if (!I) return ''
  const div = document.createElement('div')
  const root = createRoot(div)
  flushSync(() => root.render(<I size={24} strokeWidth={1.8} aria-hidden="true" />))
  const html = div.innerHTML
  root.unmount()
  return html.replace(/\sclass="[^"]*"/, '')
}

const svgPreview = (svg: string) => `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg.includes('xmlns=') ? svg : svg.replace('<svg', '<svg xmlns="http://www.w3.org/2000/svg"'))}`

function IconPanel({ b, id, tr, el }: { b: B; id: string; tr: Tree; el: El }) {
  const [code, setCode] = useState('')
  const [q, setQ] = useState('')
  const file = useRef<HTMLInputElement>(null)
  const keys = useMemo(() => Object.keys(ICON_SET).filter((k) => !q || k.includes(q.toLowerCase()) || ICON_SET[k].tags.toLowerCase().includes(q.toLowerCase())).slice(0, 72), [q])
  const KEEP = ['class', 'width', 'height', 'aria-hidden', 'aria-label', 'role', 'style', 'focusable']
  const apply = (raw: string) => {
    const r = cleanSvgCode(raw)
    if ('error' in r) return b.notify(r.error)
    if (!act.replace(b, id, r.svg, t('Icon changed'), KEEP)) b.notify(t('The icon is the same.'))
  }
  const now = outer(tr, el)
  return (
    <Panel title={t('Icon')}>
      <div className="grid h-20 place-items-center rounded-md border border-white/[0.08] bg-white">
        <img src={svgPreview(now)} alt="" className="max-h-14 max-w-[80%]" />
      </div>
      <Ctl label={t('Pick an icon')} stack>
        <input value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.stopPropagation()} placeholder={t('Search icons')} className={cn(inputCls, 'w-full')} />
        <div className="grid grid-cols-8 gap-0.5" data-wb-icon-grid="">
          {keys.map((k) => {
            const I = ICON_SET[k].I
            return (
              <button key={k} type="button" title={k} data-wb-icon={k} onClick={() => apply(iconMarkup(k))} className="grid size-7 place-items-center rounded text-ink-300 hover:bg-white/10 hover:text-white">
                <I size={15} />
              </button>
            )
          })}
        </div>
      </Ctl>
      <Ctl label={t('Paste SVG code')} stack>
        <textarea value={code} onChange={(e) => setCode(e.target.value)} onKeyDown={(e) => e.stopPropagation()} rows={4} spellCheck={false} placeholder="<svg …>…</svg>" className={cn(inputCls, 'h-auto w-full py-1.5 font-mono text-[10.5px]')} />
        <button type="button" disabled={!code.trim()} onClick={() => apply(code)} className="h-7 rounded-[5px] bg-white/[0.08] px-2 text-[11.5px] text-ink-100 hover:bg-white/[0.12] disabled:opacity-40">
          {t('Use this SVG')}
        </button>
      </Ctl>
      <input
        ref={file}
        type="file"
        accept=".svg,image/svg+xml"
        className="hidden"
        onChange={async (e) => {
          const f = e.target.files?.[0]
          e.target.value = ''
          const h = host()
          if (!f || !h) return
          try {
            apply(await h.media.readSvg(f))
          } catch (err) {
            b.notify(err instanceof Error ? err.message : String(err))
          }
        }}
      />
      <button type="button" onClick={() => file.current?.click()} className="flex h-8 w-full items-center justify-center gap-1.5 rounded-[6px] bg-white/[0.08] text-[12px] text-ink-100 hover:bg-white/[0.12]">
        <Upload size={13} /> {t('Upload an SVG file')}
      </button>
      <div className="text-[10.5px] text-ink-500">{t('The icon keeps its classes and size; set its color with the text color in the Style tab.')}</div>
    </Panel>
  )
}

/* ---------- Video / embed ---------- */

function MediaPanel({ b, id, el }: { b: B; id: string; el: El }) {
  const tag = el.tag
  const firstSource = tag === 'video' || tag === 'audio' ? elementKids(el).find((k) => k.tag === 'source' && wbc(k)) : undefined
  const srcId = firstSource ? wbc(firstSource)! : id
  const srcEl = firstSource ?? el
  const flag = (name: string, label: string) => (
    <label className="flex items-center gap-2 text-[11.5px] text-ink-300" key={name}>
      <input type="checkbox" name={name} checked={attr(el, name) !== null} onChange={(e) => act.setAttrs(b, id, { [name]: e.target.checked ? '' : null }, t('Video changed'))} /> {label}
    </label>
  )
  return (
    <Panel title={tag === 'iframe' || tag === 'embed' ? t('Embed') : t('Video')}>
      <Ctl label={t('Address')} stack>
        <Field value={attr(srcEl, 'src') ?? ''} name="src" mono placeholder="https://" live={false} onCommit={(v) => setOne(b, srcId, srcEl.tag, 'src', v.trim() || null, t('Source changed'))} />
      </Ctl>
      {tag === 'video' && (
        <>
          <Ctl label={t('Poster image')} stack>
            <Field value={attr(el, 'poster') ?? ''} name="poster" mono live={false} onCommit={(v) => setOne(b, id, tag, 'poster', v.trim() || null, t('Video changed'))} />
          </Ctl>
          <div className="grid grid-cols-2 gap-1">
            {flag('controls', t('Controls'))}
            {flag('autoplay', t('Autoplay'))}
            {flag('muted', t('Muted'))}
            {flag('loop', t('Loop'))}
            {flag('playsinline', t('Plays inline'))}
          </div>
        </>
      )}
      {tag === 'iframe' && (
        <Ctl label={t('Title')} stack>
          <Field value={attr(el, 'title') ?? ''} name="title" onCommit={(v) => act.setAttrs(b, id, { title: v || null }, t('Embed changed'))} />
        </Ctl>
      )}
    </Panel>
  )
}

/* ---------- Form fields ---------- */

const INPUT_TYPES = ['text', 'email', 'tel', 'number', 'url', 'password', 'date', 'time', 'search', 'checkbox', 'radio', 'file', 'hidden', 'range', 'color', 'submit']

function FieldPanel({ b, id, el, tr }: { b: B; id: string; el: El; tr: Tree }) {
  const tag = el.tag
  const label = t('Form field changed')
  const options = tag === 'select' ? elementKids(el).filter((k) => k.tag === 'option') : []
  const optText = options.map((o) => `${attr(o, 'value') ?? ''}|${inner(tr, o).trim()}`).join('\n')
  const esc = (v: string) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;')
  return (
    <Panel title={tag === 'form' ? t('Form') : tag === 'label' ? t('Label') : t('Form field')}>
      {(tag === 'input' || tag === 'textarea' || tag === 'select' || tag === 'button') && (
        <Ctl label={t('Name')}>
          <div className="w-[150px]">
            <Field value={attr(el, 'name') ?? ''} name="name" mono onCommit={(v, live) => setOne(b, id, tag, 'name', v.trim() || null, label, live)} />
          </div>
        </Ctl>
      )}
      {tag === 'input' && (
        <Ctl label={t('Type')}>
          <select name="type" value={attr(el, 'type') ?? 'text'} onChange={(e) => act.setAttrs(b, id, { type: e.target.value }, label)} className={cn(inputCls, 'w-[150px]')}>
            {INPUT_TYPES.map((x) => (
              <option key={x} value={x}>
                {x}
              </option>
            ))}
          </select>
        </Ctl>
      )}
      {tag === 'button' && (
        <Ctl label={t('Type')}>
          <select name="type" value={attr(el, 'type') ?? 'submit'} onChange={(e) => act.setAttrs(b, id, { type: e.target.value }, label)} className={cn(inputCls, 'w-[150px]')}>
            {['submit', 'button', 'reset'].map((x) => (
              <option key={x} value={x}>
                {x}
              </option>
            ))}
          </select>
        </Ctl>
      )}
      {(tag === 'input' || tag === 'textarea') && (
        <Ctl label={t('Placeholder')} stack>
          <Field value={attr(el, 'placeholder') ?? ''} name="placeholder" onCommit={(v) => act.setAttrs(b, id, { placeholder: v || null }, label)} />
        </Ctl>
      )}
      {tag === 'input' && (
        <Ctl label={t('Value')} stack>
          <Field value={attr(el, 'value') ?? ''} name="value" onCommit={(v) => act.setAttrs(b, id, { value: v || null }, label)} />
        </Ctl>
      )}
      {(tag === 'input' || tag === 'textarea' || tag === 'select') && (
        <label className="flex items-center gap-2 text-[11.5px] text-ink-300">
          <input type="checkbox" name="required" checked={attr(el, 'required') !== null} onChange={(e) => act.setAttrs(b, id, { required: e.target.checked ? '' : null }, label)} /> {t('Required')}
        </label>
      )}
      {tag === 'select' && (
        <Ctl label={t('Options (value|label, one per line)')} stack>
          <Field
            area
            live={false}
            value={optText}
            name="options"
            onCommit={(v) =>
              act.setInner(
                b,
                id,
                v
                  .split('\n')
                  .map((l) => l.trim())
                  .filter(Boolean)
                  .map((l) => {
                    const [val, ...rest] = l.split('|')
                    const lab = rest.length ? rest.join('|') : val
                    return `<option value="${esc(val.trim())}">${esc(lab.trim())}</option>`
                  })
                  .join(''),
                label,
              )
            }
          />
        </Ctl>
      )}
      {tag === 'label' && (
        <Ctl label={t('For (field id)')}>
          <div className="w-[150px]">
            <Field value={attr(el, 'for') ?? ''} name="for" mono onCommit={(v) => act.setAttrs(b, id, { for: v.trim() || null }, label)} />
          </div>
        </Ctl>
      )}
      {tag === 'form' && <div className="text-[10.5px] text-ink-500">{t('The form keeps the design’s own handling (its action and method in the attributes below).')}</div>}
    </Panel>
  )
}

/* ---------- Edit code (the element's own markup) and Section code ---------- */

/** Drafts that were not applied, kept while the page stays open (key → the source they started from and the text). */
const drafts = new Map<string, { base: string; text: string }>()
export const codeDrafts = drafts

/** Who may write code: users with unfiltered_html (the same users whose code sections are accepted; trust.ts markupReadOnly). */
const mayCode = () => !markupReadOnly()

function problemText(p: CodeProblem): string {
  const v = { line: p.line, tag: p.tag ?? '' }
  if (p.kind === 'unclosed') return tc('Line {line}: <{tag}> is never closed.', v)
  if (p.kind === 'stray') return tc('Line {line}: </{tag}> has no opening tag.', v)
  if (p.kind === 'unterminated') return tc('Line {line}: <{tag}> is not finished (a quote or > is missing).', v)
  return tc('Line {line}: this code would change <{tag}> outside the element.', v)
}

/** A text kept as a draft until it is applied; warns when the element it belongs to is left with unapplied changes. */
function useDraft(key: string, base: string, b: B, watch: string | null) {
  const kept = drafts.get(key)
  const [draft, setDraft] = useState(kept && kept.base === base ? kept.text : base)
  const [restored, setRestored] = useState(!!kept && kept.base === base && kept.text !== base)
  const [seen, setSeen] = useState(base)
  if (seen !== base) {
    // The source changed under us (applied, undo, another panel): start again from it.
    setSeen(base)
    setDraft(base)
    setRestored(false)
  }
  useEffect(() => {
    if (draft === base) drafts.delete(key)
    else drafts.set(key, { base, text: draft })
  }, [key, draft, base])
  useEffect(
    () => () => {
      if (drafts.has(key) && getElState().sel !== watch) b.notify(tc('You have code changes that were not applied. They are kept while you stay on this page; open the element again to continue.'))
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  )
  return { draft, setDraft, restored, setRestored }
}

function CodeBox({ b, id, tr, el, part, readOnly, sel }: { b: B; id: string; tr: Tree; el: El; part: Part; readOnly: boolean; sel: string | null }) {
  const view = useMemo(() => codeView(tr, el, part), [tr, el, part])
  const key = part + '\u0000' + id
  const { draft, setDraft, restored, setRestored } = useDraft(key, view.text, b, sel)
  const [msg, setMsg] = useState('')
  const [empty, setEmpty] = useState(false)
  const dirty = draft !== view.text
  const apply = () => {
    if (readOnly) return
    setEmpty(false)
    if (!dirty) return setMsg(tc('Nothing changed.'))
    const r = applyCode(pageOf(latest.doc ?? b.doc), id, draft, view.slots, part)
    if (r.error === 'empty') {
      setMsg(tc('The code is empty. Delete the element instead?'))
      return setEmpty(true)
    }
    if (r.error) return setMsg(r.problem ? problemText(r.problem) : tc('Nothing was applied: the element is gone or cannot be edited here.'))
    if (!r.result || !Object.keys(r.result.html).length) return setMsg(tc('Nothing changed.'))
    setMsg('')
    drafts.delete(key)
    commit(b, part === 'outer' ? tc('Edit code') : t('HTML changed'), r.result)
  }
  const outerPart = part === 'outer'
  const es = useElState()
  return (
    <div className="space-y-2" data-wb-code-box={part}>
      <div className="text-[10.5px] text-ink-500">{(outerPart ? tc('The element’s own markup, as stored. The editor’s ids are kept for you.') : tc('The markup inside the element.')) + ' ' + (es.live && es.scripts ? tc('Scripts run on the published page and in the Live canvas.') : t('Scripts run on the published page only.'))}</div>
      {readOnly && (
        <div className="text-[10.5px] text-amber-300" data-wb-code-readonly="">
          {tc('Your role may not edit code (it needs unfiltered HTML). It is shown here read-only.')}
        </div>
      )}
      {restored && !readOnly && (
        <div className="flex items-center justify-between gap-2 text-[10.5px] text-amber-300" data-wb-code-restored="">
          <span>{tc('A draft you had not applied was restored.')}</span>
          <button
            type="button"
            className="shrink-0 underline"
            onClick={() => {
              setDraft(view.text)
              setRestored(false)
              setMsg('')
            }}
          >
            {tc('Discard draft')}
          </button>
        </div>
      )}
      <CodeEditor
        value={draft}
        onChange={(v) => {
          setDraft(v)
          if (msg) setMsg('')
          setEmpty(false)
        }}
        label={outerPart ? tc('Element code') : tc('Code inside the element')}
        name={outerPart ? 'wb-outer-html' : 'wb-inner-html'}
        title={outerPart ? tc('Element code') : tc('Code inside the element')}
        readOnly={readOnly}
        invalid={!!msg}
        onApply={apply}
        extra={
          !readOnly && (
            <button type="button" disabled={!dirty} onClick={apply} className="h-7 shrink-0 rounded-[5px] bg-accent px-2.5 text-[11.5px] font-semibold text-white disabled:opacity-40">
              {tc('Apply')}
            </button>
          )
        }
      />
      {msg && (
        <div role="alert" className="text-[10.5px] text-red-300" data-wb-code-error="">
          {msg}
          {empty && (
            <button
              type="button"
              className="ml-2 underline"
              onClick={() => {
                drafts.delete(key)
                act.remove(b, id)
              }}
              data-wb-code-delete=""
            >
              {tc('Delete element')}
            </button>
          )}
        </div>
      )}
      {!readOnly && (
        <div className="flex items-center justify-end gap-2">
          <button
            type="button"
            disabled={!dirty}
            onClick={() => {
              setDraft(view.text)
              setMsg('')
              setEmpty(false)
              setRestored(false)
            }}
            className="h-7 shrink-0 rounded-[5px] px-2 text-[11.5px] text-ink-300 hover:text-white disabled:opacity-40"
          >
            {tc('Revert')}
          </button>
          <button type="button" disabled={!dirty} onClick={apply} className="h-7 shrink-0 rounded-[5px] bg-accent px-2.5 text-[11.5px] font-semibold text-white disabled:opacity-40" data-wb-code-apply={part} {...(outerPart ? {} : { 'data-wb-html-apply': '' })}>
            {tc('Apply')}
          </button>
        </div>
      )}
    </div>
  )
}

/** The Edit code group: the element's whole markup, and (where it has an inside) what is inside it. */
function CodePanel({ b, id, tr, el, inside, open, sel }: { b: B; id: string; tr: Tree; el: El; inside: boolean; open: boolean; sel: string }) {
  const [part, setPart] = useState<Part>('outer')
  const readOnly = !mayCode()
  return (
    <Panel title={tc('Edit code')} open={open}>
      {inside && (
        <div role="tablist" className="flex gap-1" data-wb-code-tabs="">
          {(['outer', 'inner'] as const).map((p) => (
            <button key={p} type="button" role="tab" aria-selected={part === p} onClick={() => setPart(p)} className={cn('h-6 rounded px-2 text-[11px]', part === p ? 'bg-white/10 text-white' : 'text-ink-400 hover:text-white')} data-wb-code-tab={p}>
              {p === 'outer' ? tc('Whole element') : t('HTML inside')}
            </button>
          ))}
        </div>
      )}
      <CodeBox key={part} b={b} id={id} tr={tr} el={el} part={inside ? part : 'outer'} readOnly={readOnly} sel={sel} />
    </Panel>
  )
}

/** The section's own HTML and head code, edited as one history entry. */
function SectionCode({ b, s, sel }: { b: B; s: Section; sel: string }) {
  const readOnly = !mayCode()
  const html0 = s.content.html ?? ''
  const head0 = s.content.head ?? ''
  const h = useDraft('section-html\u0000' + s.id, html0, b, sel)
  const d = useDraft('section-head\u0000' + s.id, head0, b, sel)
  const dirty = h.draft !== html0 || d.draft !== head0
  const warn = h.draft !== html0 ? checkMarkup(h.draft) : null
  const apply = () => {
    if (readOnly || !dirty) return
    drafts.delete('section-html\u0000' + s.id)
    drafts.delete('section-head\u0000' + s.id)
    commit(b, tc('Code changed'), { html: h.draft !== html0 ? { [s.id]: h.draft } : {} }, d.draft !== head0 ? { [s.id]: { head: d.draft } } : undefined)
  }
  return (
    <Panel title={t('Section code')} open={false}>
      {readOnly && <div className="text-[10.5px] text-amber-300">{tc('Your role may not edit code (it needs unfiltered HTML). It is shown here read-only.')}</div>}
      <Ctl label={tc('HTML code')} stack>
        <CodeEditor value={h.draft} onChange={h.setDraft} label={tc('HTML code')} name="wb-section-html" title={tc('HTML code')} readOnly={readOnly} maxHeight={320} onApply={apply} />
      </Ctl>
      {warn && (
        <div className="text-[10.5px] text-amber-300" data-wb-code-warn="">
          {problemText(warn)}
        </div>
      )}
      <Ctl label={tc('Head code')} stack>
        <CodeEditor value={d.draft} onChange={d.setDraft} label={tc('Head code')} name="wb-section-head" title={tc('Head code')} readOnly={readOnly} minRows={3} maxHeight={200} onApply={apply} />
      </Ctl>
      {!readOnly && (
        <div className="flex justify-end">
          <button type="button" disabled={!dirty} onClick={apply} className="h-7 shrink-0 rounded-[5px] bg-accent px-2.5 text-[11.5px] font-semibold text-white disabled:opacity-40" data-wb-section-code-apply="">
            {tc('Apply')}
          </button>
        </div>
      )}
    </Panel>
  )
}


/* ---------- Attributes ---------- */

const HIDDEN_ATTRS = new Set(['class', 'style', 'data-wbc'])

function AttrsPanel({ b, id, el }: { b: B; id: string; el: El }) {
  const list = el.attrs.filter((a) => !HIDDEN_ATTRS.has(a.name) && !a.name.startsWith('on'))
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [err, setErr] = useState('')
  const add = () => {
    const n = name.trim().toLowerCase()
    if (!n) return
    const e = attrError(n, value, el.tag)
    if (e) return setErr(errText(e))
    setErr('')
    act.setAttrs(b, id, { [n]: value }, t('Attribute added'))
    setName('')
    setValue('')
  }
  return (
    <Panel title={t('Attributes')} open={false}>
      <div className="space-y-1" data-wb-attrs="">
        {list.map((a) => (
          <div key={a.name} className="flex items-center gap-1" data-wb-attr={a.name}>
            <span className="w-[92px] shrink-0 truncate font-mono text-[10.5px] text-ink-300" title={a.name}>
              {a.name}
            </span>
            <div className="min-w-0 flex-1">
              <Field value={a.value ?? ''} mono name={`attr-${a.name}`} onCommit={(v, live) => setOne(b, id, el.tag, a.name, v, t('Attribute changed'), live)} />
            </div>
            <button type="button" aria-label={t('Remove')} onClick={() => act.setAttrs(b, id, { [a.name]: null }, t('Attribute removed'))} className="grid size-6 shrink-0 place-items-center rounded text-ink-500 hover:text-red-300">
              <X size={11} />
            </button>
          </div>
        ))}
        {!list.length && <div className="text-[10.5px] text-ink-500">{t('No attributes')}</div>}
      </div>
      <div className="flex items-center gap-1">
        <input value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.stopPropagation()} placeholder={t('name')} spellCheck={false} name="wb-attr-name" className={cn(inputCls, 'w-[92px] font-mono text-[11px]')} />
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            e.stopPropagation()
            if (e.key === 'Enter') add()
          }}
          placeholder={t('value')}
          spellCheck={false}
          name="wb-attr-value"
          className={cn(inputCls, 'min-w-0 flex-1 font-mono text-[11px]')}
        />
        <button type="button" aria-label={t('Add')} onClick={add} className="grid size-6 place-items-center rounded text-ink-400 hover:text-white" data-wb-attr-add="">
          <Plus size={13} />
        </button>
      </div>
      {err && <div className="text-[10.5px] text-red-300" data-wb-attr-error="">{err}</div>}
      <div className="text-[10.5px] text-ink-500">{t('id, aria-*, data-*, title, rel and the like. Event handlers (on…) are never added.')}</div>
    </Panel>
  )
}
