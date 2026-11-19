/**
 * Content bindings of code sections (docs/developer/web-builder-code-sections.md, "Binding to
 * content"; Go: internal/webbuilder/codebind.go): an element of the design is bound to a Pursae
 * menu, a post loop, a post field or the form endpoint. The bindings live in the section's
 * content.binds (JSON keyed by data-wbc), never in the markup; the server turns them into the
 * dynamic engine's placeholders on publish. This module is the pure part: reading and writing the
 * JSON, and what an element can be bound to.
 */
import { attr, elementKids, parse, textOf, wbc, type El, type Tree } from './html'

export type BindKind = 'menu' | 'submenu' | 'loop' | 'field' | 'form'

export const BIND_FIELDS = ['title', 'excerpt', 'content', 'image', 'url', 'link', 'date', 'modified', 'author', 'terms', 'custom', 'pagination'] as const
export type BindField = (typeof BIND_FIELDS)[number]

export const CUSTOM_ATTRS = ['', 'html', 'url', 'attrs'] as const

export interface CodeBind {
  kind: BindKind
  location?: string
  menu?: number
  items?: Record<string, string>
  query?: Record<string, string>
  field?: BindField
  name?: string
  attr?: string
  text?: boolean
  format?: string
  tax?: string
  sep?: string
  links?: boolean
  words?: number
  size?: string
  srcset?: boolean
  fields?: Record<string, string>
  required?: string[]
  done?: string
  error?: string
  title?: string
  to?: string
  cc?: string
  success?: string
  failure?: string
  redirect?: string
}

export type CodeBinds = Record<string, CodeBind>

const isObj = (x: unknown): x is Record<string, unknown> => !!x && typeof x === 'object' && !Array.isArray(x)

/** content.binds read leniently (bad JSON: none). */
export function parseBinds(json: string | undefined): CodeBinds {
  if (!json) return {}
  try {
    const v = JSON.parse(json) as unknown
    if (!isObj(v)) return {}
    const out: CodeBinds = {}
    for (const [k, b] of Object.entries(v)) if (/^[0-9]{1,9}$/.test(k) && isObj(b) && typeof b.kind === 'string') out[k] = b as unknown as CodeBind
    return out
  } catch {
    return {}
  }
}

/** content.binds as stored ("" when empty; the ids are numeric keys, so JSON lists them in numeric order). */
export function stringifyBinds(b: CodeBinds): string {
  return Object.keys(b).length ? JSON.stringify(b) : ''
}

/** The bindings with element id's set (null: removed). */
export function withBind(b: CodeBinds, id: string, bind: CodeBind | null): CodeBinds {
  const next = { ...b }
  if (bind) next[id] = bind
  else delete next[id]
  return next
}

/** What the editor knows of an element for binding it. */
export interface BindTarget {
  tag: string
  /** Element children (a list's items, a loop's cards). */
  kids: number
  /** The loop or menu binding of an ancestor (its id), when the element is inside one's first child. */
  loop: string | null
  menu: string | null
  /** A <form>'s named inputs: name, type, label. */
  inputs: { name: string; type: string; label: string }[]
  /** Elements of the section with an id attribute (success / error message candidates): data-wbc → label. */
  messages: { id: string; label: string }[]
}

/** Reads element id of a section's markup. */
export function bindTarget(html: string, id: string, binds: CodeBinds): BindTarget | null {
  const tr = parse(html)
  const el = tr.byId.get(id)
  if (!el) return null
  const out: BindTarget = { tag: el.tag, kids: elementKids(el).length, loop: null, menu: null, inputs: [], messages: [] }
  for (let p = el.parent, child: El = el; p; child = p, p = p.parent) {
    const pid = wbc(p)
    const b = pid ? binds[pid] : undefined
    if (!b) continue
    if (b.kind === 'loop' && !out.loop && elementKids(p)[0] === child) out.loop = pid
    if ((b.kind === 'menu' || b.kind === 'submenu') && !out.menu) out.menu = pid
  }
  if (el.tag === 'form') out.inputs = formInputs(tr, el)
  for (const e of tr.all) {
    const i = attr(e, 'id')
    const w = wbc(e)
    if (i && w && e !== el && e.tag !== 'input' && e.tag !== 'select' && e.tag !== 'textarea' && e.tag !== 'label') out.messages.push({ id: w, label: `#${i}` })
  }
  return out
}

/** A form's named inputs with their labels (not buttons, files, hidden or the honeypot). */
export function formInputs(tr: Tree, form: El): BindTarget['inputs'] {
  const labels = new Map<string, string>()
  const all: El[] = []
  const walk = (e: El) => {
    for (const c of elementKids(e)) {
      if (c.tag === 'label') {
        const f = attr(c, 'for')
        if (f) labels.set(f, textOf(tr, c).replace(/[*:\s]+$/, ''))
      }
      if (c.tag === 'input' || c.tag === 'select' || c.tag === 'textarea') all.push(c)
      walk(c)
    }
  }
  walk(form)
  const seen = new Set<string>()
  const out: BindTarget['inputs'] = []
  for (const c of all) {
    const name = attr(c, 'name') ?? ''
    const type = (attr(c, 'type') ?? (c.tag === 'input' ? 'text' : c.tag)).toLowerCase()
    if (!name || seen.has(name) || name.startsWith('wb_') || ['submit', 'button', 'reset', 'image', 'file', 'hidden', 'password'].includes(type)) continue
    seen.add(name)
    const id = attr(c, 'id')
    out.push({ name, type, label: (id && labels.get(id)) || attr(c, 'placeholder') || name })
  }
  return out
}

export const FORM_FIELD_NAMES = ['name', 'email', 'phone', 'message', 'date', 'choice', 'company', 'subject'] as const

/** The Pursae field an input maps to by default: itself when it is one, else field_<name>. */
export function defaultFormField(name: string): string {
  const low = name.toLowerCase()
  if ((FORM_FIELD_NAMES as readonly string[]).includes(low)) return low
  const s = low
    .replace(/[-.[\]]/g, '_')
    .replace(/[^a-z0-9_]/g, '')
    .replace(/^_+|_+$/g, '')
    .slice(0, 40)
  return s ? `field_${s}` : ''
}

/** A new binding of a kind with its defaults. */
export function newBind(kind: BindKind, t: BindTarget): CodeBind {
  switch (kind) {
    case 'menu':
      return { kind, location: 'primary' }
    case 'loop':
      return { kind, query: { postType: 'post', perPage: String(Math.max(1, Math.min(24, t.kids || 3))), orderBy: 'date', order: 'desc' } }
    case 'field':
      return { kind, field: t.tag === 'img' ? 'image' : t.tag === 'a' ? 'url' : 'title' }
    default:
      return { kind }
  }
}

/** The kinds an element can take. */
export function kindsFor(t: BindTarget): BindKind[] {
  const out: BindKind[] = []
  if (t.tag === 'form') return ['form']
  if (t.kids > 0 && !t.loop) out.push('menu', 'loop')
  if (t.menu && t.kids > 0) out.push('submenu')
  out.push('field')
  return out
}
