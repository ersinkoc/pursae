/**
 * "Edit code" operations (pure, no DOM): the selected element's own markup shown without the
 * editor's data-wbc ids, checked with the source-preserving parser, and spliced back at the
 * element's range with the ids restored (an element the typed code still has at the same place
 * keeps its id; a new one gets a fresh id).
 *
 * Like "HTML inside" and the Section code fields this is raw code by a user who may write code
 * sections (unfiltered_html): the attribute rules of the Attributes list (attrs.ts) do not apply,
 * and nothing is filtered here (the server's publish filter is the boundary, as for Section code).
 * The canvas shows the result through the usual reconcile path (neutralised in static mode).
 */
import { attrEdit, parse, RAW, splice, VOID, wbc, type Attr, type Edit, type El, type Tree } from './html'
import { CODE_TAGS, freshId, locate, tree, type Page, type Result } from './ops'

/** The whole element ('outer') or what is inside it ('inner'). */
export type Part = 'outer' | 'inner'

/** The source range of the part. */
export const rangeOf = (el: El, part: Part): [number, number] => (part === 'outer' ? [el.start, el.end] : [el.openEnd, el.closeStart])

/** The inside of these is text, not markup (script, style, textarea …): no ids, no markup check. */
export const rawInside = (el: El) => RAW.has(el.tag)

/** A data-wbc attribute as it stood in the source, so it can be put back where it was. */
export interface IdSlot {
  id: string
  /** Element tag (lower case). */
  tag: string
  /** The attribute's source text (data-wbc="3"). */
  raw: string
  /** The whitespace before it. */
  ws: string
  /** How many attributes came before it. */
  at: number
}

export interface CodeView {
  /** The element's markup without its data-wbc attributes. */
  text: string
  slots: IdSlot[]
}

/** The elements of the range [start, end) of t, in document order. */
const within = (t: Tree, start: number, end: number) => t.all.filter((e) => e.start >= start && e.end <= end)

/** The element's outer markup with the editor's ids hidden (and where they were, to restore them). */
export function codeView(t: Tree, el: El, part: Part = 'outer'): CodeView {
  const [from, to] = rangeOf(el, part)
  if (part === 'inner' && rawInside(el)) return { text: t.src.slice(from, to), slots: [] }
  const els = within(t, from, to)
  const edits: Edit[] = []
  const slots: IdSlot[] = []
  els.forEach((e) => {
    const k = e.attrs.findIndex((a) => a.name === 'data-wbc')
    if (k < 0) return
    const a = e.attrs[k]
    let s = a.start
    while (s > e.start && /\s/.test(t.src[s - 1])) s--
    slots.push({ id: a.value ?? '', tag: e.tag, raw: t.src.slice(a.start, a.end), ws: t.src.slice(s, a.start), at: k })
    edits.push({ start: s, end: a.end, text: '' })
  })
  const local = edits.map((x) => ({ start: x.start - from, end: x.end - from, text: x.text }))
  return { text: splice(t.src.slice(from, to), local), slots }
}

/* ---------- Checking ---------- */

export type CodeProblem = { kind: 'unclosed' | 'stray' | 'unterminated' | 'around'; tag?: string; line: number }

/** Elements whose end tag may be left out. */
const OPTIONAL_END = new Set(['p', 'li', 'dt', 'dd', 'option', 'optgroup', 'tr', 'td', 'th', 'thead', 'tbody', 'tfoot', 'colgroup', 'caption', 'rp', 'rt'])

const lineAt = (src: string, pos: number) => {
  let n = 1
  for (let i = src.indexOf('\n'); i >= 0 && i < pos; i = src.indexOf('\n', i + 1)) n++
  return n
}

/** A start tag that ends with its > (a quote opened after = is closed again). */
function openTagClosed(tag: string): boolean {
  let q = ''
  let prev = ''
  for (let i = 1; i < tag.length; i++) {
    const c = tag[i]
    if (q) {
      if (c === q) q = ''
    } else if ((c === '"' || c === "'") && prev === '=') q = c
    if (!q && !/\s/.test(c)) prev = c
    else if (q) prev = c
  }
  return !q && tag.endsWith('>')
}

/** The first thing wrong with a piece of markup, or null when it is balanced and complete. */
export function checkMarkup(src: string): CodeProblem | null {
  const t = parse(src)
  let first: CodeProblem | null = null
  let firstAt = Infinity
  const note = (p: CodeProblem, at: number) => {
    if (at < firstAt) {
      first = p
      firstAt = at
    }
  }
  for (const e of t.all) {
    if (!openTagClosed(src.slice(e.start, e.openEnd))) note({ kind: 'unterminated', tag: e.tag, line: lineAt(src, e.start) }, e.start)
    else if (!e.closed && e.closeStart === e.end && !VOID.has(e.tag) && !OPTIONAL_END.has(e.tag) && !/\/>$/.test(src.slice(e.start, e.openEnd))) {
      note({ kind: 'unclosed', tag: e.tag, line: lineAt(src, e.start) }, e.start)
    }
  }
  const strays = (el: El) => {
    for (const c of el.children) {
      if (c.type === 'other' && src.startsWith('</', c.start)) note({ kind: 'stray', tag: /^<\/([^\s>]+)/.exec(src.slice(c.start, c.end))?.[1]?.toLowerCase(), line: lineAt(src, c.start) }, c.start)
      else if (c.type === 'comment' && !src.slice(c.start, c.end).endsWith('-->')) note({ kind: 'unterminated', tag: '!--', line: lineAt(src, c.start) }, c.start)
      else if (c.type === 'el') strays(c)
    }
  }
  strays(t.root)
  return first
}

/* ---------- Applying ---------- */

export type CodeError = 'missing' | 'locked' | 'empty' | CodeProblem['kind']

export interface CodeResult {
  result?: Result
  error?: CodeError
  problem?: CodeProblem
}

/** Elements of the typed code that get an editor id (the same ones "HTML inside" gives one). */
const identifiable = (e: El) => e.tag !== '#root' && !e.foreign && !CODE_TAGS.has(e.tag) && e.tag !== 'option'

/**
 * Which new elements keep which old id: the longest run of equal tags in order (tags only, so an
 * element typed in front shifts nothing), over the old elements that had an id.
 */
function matchIds(oldTags: string[], newTags: string[]): number[] {
  const out = new Array<number>(newTags.length).fill(-1)
  let a = 0
  while (a < oldTags.length && a < newTags.length && oldTags[a] === newTags[a]) {
    out[a] = a
    a++
  }
  let ao = oldTags.length
  let bo = newTags.length
  while (ao > a && bo > a && oldTags[ao - 1] === newTags[bo - 1]) {
    ao--
    bo--
    out[bo] = ao
  }
  const n = ao - a
  const m = bo - a
  if (!n || !m) return out
  if (n * m > 4_000_000) {
    // Too many for the table: pair what is left by position while the tags agree.
    for (let i = 0; i < Math.min(n, m); i++) if (oldTags[a + i] === newTags[a + i]) out[a + i] = a + i
    return out
  }
  const w = m + 1
  const lcs = new Uint32Array((n + 1) * w)
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--) lcs[i * w + j] = oldTags[a + i] === newTags[a + j] ? lcs[(i + 1) * w + j + 1] + 1 : Math.max(lcs[(i + 1) * w + j], lcs[i * w + j + 1])
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (oldTags[a + i] === newTags[a + j]) {
      out[a + j] = a + i
      i++
      j++
    } else if (lcs[(i + 1) * w + j] >= lcs[i * w + j + 1]) i++
    else j++
  }
  return out
}

/**
 * The typed code with data-wbc ids put in: back on the elements that still match an old one
 * (in the attribute slot they had), fresh ones on new elements. An id the user typed stays when it
 * is unique in the code and not used elsewhere on the page; otherwise it is replaced.
 */
export function withCodeIds(code: string, slots: IdSlot[], taken: Set<string>): { html: string; ids: (string | null)[] } {
  const t = parse(code)
  const edits: Edit[] = []
  const els = t.all.filter(identifiable)
  const typed = new Map<string, number>()
  for (const e of t.all) {
    const w = wbc(e)
    if (w) typed.set(w, (typed.get(w) ?? 0) + 1)
  }
  const keep = (e: El) => {
    const w = wbc(e)
    return !!w && typed.get(w) === 1 && !taken.has(w)
  }
  // A typed id that is not usable goes (and the element gets one below).
  const dropped = new Set<Attr>()
  for (const e of t.all) {
    if (wbc(e) === null || keep(e)) continue
    for (const a of e.attrs) if (a.name === 'data-wbc') dropped.add(a)
    edits.push(...attrEdit(t, e, 'data-wbc', null))
  }
  const need = els.filter((e) => !keep(e))
  const usedTyped = new Set(els.filter(keep).map((e) => wbc(e)!))
  const pool = slots.filter((s) => !usedTyped.has(s.id))
  const m = matchIds(
    pool.map((s) => s.tag),
    need.map((e) => e.tag),
  )
  need.forEach((e, k) => {
    const slot = m[k] >= 0 ? pool[m[k]] : null
    const rest = e.attrs.filter((x) => !dropped.has(x))
    if (slot && slot.at > 0 && rest.length >= slot.at) {
      // Back in its old slot: after the attribute that was before it.
      const at = rest[slot.at - 1].end
      edits.push({ start: at, end: at, text: slot.ws + slot.raw })
    } else if (slot && slot.at === 0) {
      const at = e.start + 1 + e.tag.length
      edits.push({ start: at, end: at, text: slot.ws + slot.raw })
    } else edits.push({ start: e.attrEnd, end: e.attrEnd, text: ` data-wbc="${slot ? slot.id : freshId()}"` })
  })
  const html = splice(code, edits)
  const all = parse(html).root
  const rootIds: (string | null)[] = []
  for (const c of all.children) if (c.type === 'el') rootIds.push(wbc(c))
  return { html, ids: rootIds }
}

/** Replaces element `id` by the typed code (zero, one or several top-level nodes). */
export function applyCode(page: Page, id: string, code: string, slots: IdSlot[], part: Part = 'outer'): CodeResult {
  const at = locate(page, id)
  if (!at) return { error: 'missing' }
  const { tree: t, el } = at
  if (part === 'inner' && (el.foreign || el.tag === 'svg' || VOID.has(el.tag))) return { error: 'missing' }
  if (part === 'outer' && !code.trim()) return { error: 'empty' }
  const [from, to] = rangeOf(el, part)
  if (part === 'inner' && rawInside(el)) {
    // Text: it must not end the element early.
    if (new RegExp(`</${el.tag}[\\s/>]`, 'i').test(code)) return { error: 'around', problem: { kind: 'around', tag: el.tag, line: lineAt(code, code.search(new RegExp(`</${el.tag}[\\s/>]`, 'i'))) } }
    const next = t.src.slice(0, from) + code + t.src.slice(to)
    return next === t.src ? { result: { html: {}, select: id } } : { result: { html: { [at.sec]: next }, select: id } }
  }
  const problem = checkMarkup(code)
  if (problem) return { error: problem.kind, problem }
  // Ids used by the page outside this element's range stay out of reach of the typed code.
  const taken = new Set<string>()
  for (const sec of page.order) {
    const tt = tree(page.html[sec] ?? '')
    for (const e of tt.all) {
      if (sec === at.sec && e.start >= from && e.end <= to) continue
      const w = wbc(e)
      if (w) taken.add(w)
    }
  }
  const made = withCodeIds(code, slots, taken)
  const next = t.src.slice(0, from) + made.html + t.src.slice(to)
  // The code must not change what is around it: every element outside keeps its place.
  const nt = parse(next)
  for (const e of t.all) {
    if (e.start >= from && e.end <= to) continue
    const w = wbc(e)
    if (!w) continue
    const e2 = nt.byId.get(w)
    const pw = e.parent && wbc(e.parent)
    const pw2 = e2?.parent && wbc(e2.parent)
    if (!e2 || e2.tag !== e.tag || (pw || null) !== (pw2 || null)) return { error: 'around', problem: { kind: 'around', tag: e.tag, line: lineAt(t.src, e.start) } }
  }
  if (next === t.src) return { result: { html: {}, select: id } }
  return { result: { html: { [at.sec]: next }, select: part === 'inner' ? id : (made.ids.find((x) => x) ?? null) } }
}
