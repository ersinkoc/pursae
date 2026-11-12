/**
 * Display conditions model of the editor's conditions dialog (contract §11.3).
 *
 * A rule is "<include|exclude>/<scope>". The dialog edits it as a row of cascading selects:
 *
 *   group   general | singular | archive | 404 | user
 *   sub     singular: '' (all) | front_page | <ptype>
 *           user:     logged_in | logged_out | role/<role>   (audience rules: who is looking)
 *           archive:  '' (all) | home | date | search | author | type/<ptype> | <tax>
 *   target  singular/<ptype>: '' (all) | id | in/<tax> | child_of | by_author
 *           archive/<tax>, archive/author: '' (all) | id
 *   id      the entry, term or user a target needs
 *
 * Post types and taxonomies come from the boot config (SiteBoot.postTypes / taxonomies); a stored
 * rule naming one the site does not list (a valid rule that matches nothing in v1) is kept and
 * shown with its slug. No React here: ConditionsDialog.tsx draws the rows.
 */
import { CONDITION_LIMITS, CONDITION_PATTERN, RESERVED_ARCHIVE_WORDS, RESERVED_SINGULAR_WORDS, type PostTypeInfo, type SiteBoot, type TaxonomyInfo } from './contract'
import { t } from './i18n'

export type ConditionMode = 'include' | 'exclude'
export type ConditionGroup = 'general' | 'singular' | 'archive' | '404' | 'user'

export interface ConditionRow {
  mode: ConditionMode
  group: ConditionGroup
  /** See the module comment; '' = all of the group. */
  sub: string
  /** singular/<ptype>: '' | 'id' | 'in/<tax>' | 'child_of' | 'by_author'; archive/<tax>|author: '' | 'id'. */
  target: string
  /** Positive decimal id for targets that need one, else ''. */
  id: string
  /** Display label of `id` (search result or fetched title); not part of the rule. */
  idLabel?: string
}

export interface Option {
  value: string
  label: string
}

type Site = Pick<SiteBoot, 'postTypes' | 'taxonomies' | 'roles'>

const ID_RE = /^[1-9][0-9]{0,18}$/

export const emptyRow = (mode: ConditionMode = 'include'): ConditionRow => ({ mode, group: 'general', sub: '', target: '', id: '' })

/* ---------- Rule <-> row ---------- */

/** Parses a stored rule into a dialog row; null when it is not a valid rule (contract grammar). */
export function parseRule(rule: string): ConditionRow | null {
  const r = rule.trim()
  if (!isValidRule(r)) return null
  const [mode, group, ...rest] = r.split('/')
  const row: ConditionRow = { mode: mode as ConditionMode, group: group as ConditionGroup, sub: '', target: '', id: '' }
  if (group === 'singular' && rest.length) {
    row.sub = rest[0]
    const tail = rest.slice(1)
    if (tail.length === 1) {
      row.target = 'id'
      row.id = tail[0]
    } else if (tail[0] === 'in') {
      row.target = `in/${tail[1]}`
      row.id = tail[2]
    } else if (tail[0] === 'child_of' || tail[0] === 'by_author') {
      row.target = tail[0]
      row.id = tail[1]
    }
  } else if (group === 'user') {
    row.sub = rest.join('/')
  } else if (group === 'archive' && rest.length) {
    if (rest[0] === 'type') row.sub = `type/${rest[1]}`
    else {
      row.sub = rest[0]
      if (rest.length === 2) {
        row.target = 'id'
        row.id = rest[1]
      }
    }
  }
  return row
}

/** Whether the row's target needs an id. */
export const needsId = (row: Pick<ConditionRow, 'target'>) => row.target !== ''

/** The rule a row stands for; null while it is incomplete (a target without an id). */
export function rowRule(row: ConditionRow): string | null {
  const parts: string[] = [row.mode, row.group]
  if (row.group === 'singular' && row.sub) {
    parts.push(row.sub)
    if (row.sub !== 'front_page' && row.target) {
      if (!ID_RE.test(row.id)) return null
      if (row.target === 'id') parts.push(row.id)
      else parts.push(...row.target.split('/'), row.id)
    }
  } else if (row.group === 'user') {
    parts.push(...(row.sub || 'logged_in').split('/'))
  } else if (row.group === 'archive' && row.sub) {
    parts.push(...row.sub.split('/'))
    if (row.target === 'id' && (row.sub === 'author' || isTaxSub(row.sub))) {
      if (!ID_RE.test(row.id)) return null
      parts.push(row.id)
    }
  }
  const rule = parts.join('/')
  return isValidRule(rule) ? rule : null
}

const ARCHIVE_FIXED = new Set<string>(['home', 'date', 'search', 'author'])
const isTaxSub = (sub: string) => !!sub && !ARCHIVE_FIXED.has(sub) && !sub.startsWith('type/')

/** The contract grammar (CONDITION_PATTERN), the reserved words and the byte limit. */
export function isValidRule(rule: string): boolean {
  if (new TextEncoder().encode(rule).length > CONDITION_LIMITS.maxConditionBytes) return false
  if (!CONDITION_PATTERN.test(rule)) return false
  const p = rule.split('/')
  if (p[1] === 'singular' && p.length > 2 && p[2] !== 'front_page' && (RESERVED_SINGULAR_WORDS as readonly string[]).includes(p[2])) return false
  if (p[1] === 'archive' && p.length === 3 && (RESERVED_ARCHIVE_WORDS as readonly string[]).includes(p[2]) && !ARCHIVE_FIXED.has(p[2])) return false
  if (p[1] === 'archive' && p.length === 4 && p[2] !== 'author' && p[2] !== 'type' && (RESERVED_ARCHIVE_WORDS as readonly string[]).includes(p[2])) return false
  return true
}

/**
 * The rules to save: trimmed, duplicates dropped (order kept). Returns the per-row problems
 * (incomplete or invalid rows, too many rules) keyed by row index; save only when it is empty.
 */
export function rowsToRules(rows: ConditionRow[]): { rules: string[]; errors: Record<number, string> } {
  const rules: string[] = []
  const errors: Record<number, string> = {}
  rows.forEach((row, i) => {
    const r = rowRule(row)
    if (r === null) {
      errors[i] = needsId(row) && !ID_RE.test(row.id) ? t('Choose an item, or pick “All”.') : t('This condition is not valid.')
      return
    }
    if (!rules.includes(r)) rules.push(r)
  })
  if (rules.length > CONDITION_LIMITS.maxConditions) errors[CONDITION_LIMITS.maxConditions] = t('At most {n} conditions.', { n: CONDITION_LIMITS.maxConditions })
  return { rules, errors }
}

/** Audience rules (user/…) narrow who sees a template; they never place it. */
export const isAudienceRule = (rule: string) => /^(include|exclude)\/user\//.test(rule.trim())

/** True when a rule places the template somewhere (an include rule that is not an audience rule). */
export const hasInclude = (rules: readonly string[]) => rules.some((r) => r.trim().startsWith('include/') && !isAudienceRule(r))

export function clampPriority(n: number): number {
  if (!Number.isFinite(n)) return 0
  return Math.max(CONDITION_LIMITS.priorityMin, Math.min(CONDITION_LIMITS.priorityMax, Math.trunc(n)))
}

/* ---------- Cascade options ---------- */

/**
 * Display label of a post type or taxonomy from the boot config. The server sends the built-in
 * ones (post, page, category, post_tag) in English: those are shown in the editor's language.
 */
export function builtinLabel(slug: string, label?: string): string {
  switch (slug) {
    case 'post':
      if (!label || label === 'Posts') return t('Posts')
      break
    case 'page':
      if (!label || label === 'Pages') return t('Pages')
      break
    case 'category':
      if (!label || label === 'Categories') return t('Categories')
      break
    case 'post_tag':
      if (!label || label === 'Tags') return t('Tags')
      break
  }
  return label || slug
}

const typeOf = (site: Site, slug: string): PostTypeInfo | undefined => site.postTypes.find((p) => p.slug === slug)
const taxOf = (site: Site, slug: string): TaxonomyInfo | undefined => site.taxonomies.find((x) => x.slug === slug)
const typeLabel = (site: Site, slug: string) => builtinLabel(slug, typeOf(site, slug)?.label)
const taxLabel = (site: Site, slug: string) => builtinLabel(slug, taxOf(site, slug)?.label)

export function groupOptions(): Option[] {
  return [
    { value: 'general', label: t('Entire Site') },
    { value: 'singular', label: t('Singular') },
    { value: 'archive', label: t('Archives') },
    { value: '404', label: t('404 Page') },
    { value: 'user', label: t('Visitor') },
  ]
}

/** Second select; [] when the group has none. `keep` is the row's current value (kept even when unknown). */
export function subOptions(site: Site, group: ConditionGroup, keep = ''): Option[] {
  let out: Option[] = []
  if (group === 'singular') {
    out = [{ value: '', label: t('All Singular') }, { value: 'front_page', label: t('Front Page') }, ...site.postTypes.map((p) => ({ value: p.slug, label: builtinLabel(p.slug, p.label) }))]
  } else if (group === 'archive') {
    out = [
      { value: '', label: t('All Archives') },
      { value: 'home', label: t('Posts Page') },
      { value: 'date', label: t('Date Archives') },
      { value: 'search', label: t('Search Results') },
      { value: 'author', label: t('Author Archives') },
      ...site.postTypes.filter((p) => p.archive || p.slug === 'post').map((p) => ({ value: `type/${p.slug}`, label: t('{type} Archive', { type: builtinLabel(p.slug, p.label) }) })),
      ...site.taxonomies.map((x) => ({ value: x.slug, label: builtinLabel(x.slug, x.label) })),
    ]
  }
  if (group === 'user') {
    out = [
      { value: 'logged_in', label: t('Logged-in Users') },
      { value: 'logged_out', label: t('Logged-out Visitors') },
      ...(site.roles ?? []).map((r) => ({ value: `role/${r.slug}`, label: t('Role: {role}', { role: r.label || r.slug }) })),
    ]
    if (keep && !out.some((o) => o.value === keep)) out.push({ value: keep, label: t('Role: {role}', { role: keep.replace(/^role\//, '') }) })
    return out
  }
  if (keep && !out.some((o) => o.value === keep)) out.push({ value: keep, label: keep.startsWith('type/') ? t('{type} Archive', { type: keep.slice(5) }) : keep })
  return out
}

/** Third select; [] when the sub has none. */
export function targetOptions(site: Site, row: Pick<ConditionRow, 'group' | 'sub'>, keep = ''): Option[] {
  let out: Option[] = []
  if (row.group === 'singular' && row.sub && row.sub !== 'front_page') {
    const pt = typeOf(site, row.sub)
    const label = typeLabel(site, row.sub)
    out = [{ value: '', label: t('All {type}', { type: label }) }, { value: 'id', label: t('Specific item') }]
    for (const x of site.taxonomies.filter((x) => x.types.includes(row.sub))) out.push({ value: `in/${x.slug}`, label: t('In {taxonomy}', { taxonomy: builtinLabel(x.slug, x.label) }) })
    if (pt?.hierarchical) out.push({ value: 'child_of', label: t('Child of') })
    out.push({ value: 'by_author', label: t('By author') })
  } else if (row.group === 'archive' && (row.sub === 'author' || isTaxSub(row.sub))) {
    out = [{ value: '', label: row.sub === 'author' ? t('All authors') : t('All {taxonomy}', { taxonomy: taxLabel(site, row.sub) }) }, { value: 'id', label: row.sub === 'author' ? t('Specific author') : t('Specific term') }]
  }
  if (keep && out.length && !out.some((o) => o.value === keep)) out.push({ value: keep, label: keep.startsWith('in/') ? t('In {taxonomy}', { taxonomy: keep.slice(3) }) : keep })
  return out
}

/** Changes one level of a row; the levels below reset. */
export function updateRow(row: ConditionRow, change: Partial<Pick<ConditionRow, 'mode' | 'group' | 'sub' | 'target' | 'id' | 'idLabel'>>): ConditionRow {
  const next = { ...row, ...change }
  if ('group' in change && change.group !== row.group) Object.assign(next, { sub: change.group === 'user' ? 'logged_in' : '', target: '', id: '', idLabel: undefined })
  else if ('sub' in change && change.sub !== row.sub) Object.assign(next, { target: '', id: '', idLabel: undefined })
  else if ('target' in change && change.target !== row.target) Object.assign(next, { id: '', idLabel: undefined })
  return next
}

/* ---------- Id search (core wp/v2 routes) ---------- */

export type IdSearchKind = 'posts' | 'pages' | 'categories' | 'tags' | 'users'

/** The wp/v2 collection an id is searched in; null = no search route (the id is typed in). */
export function idSource(site: Site, row: ConditionRow): IdSearchKind | null {
  const ofType = (slug: string): IdSearchKind | null => (slug === 'post' ? 'posts' : slug === 'page' ? 'pages' : null)
  const ofTax = (slug: string): IdSearchKind | null => (slug === 'category' ? 'categories' : slug === 'post_tag' ? 'tags' : null)
  if (row.group === 'singular' && row.sub && row.target) {
    if (row.target === 'id' || row.target === 'child_of') return ofType(row.sub)
    if (row.target === 'by_author') return 'users'
    if (row.target.startsWith('in/')) return ofTax(row.target.slice(3))
  }
  if (row.group === 'archive' && row.target === 'id') return row.sub === 'author' ? 'users' : ofTax(row.sub)
  void site
  return null
}

/* ---------- Human labels (the Theme Builder screen uses the same wording) ---------- */

/** "Entire Site", "Posts: #12", "Excluded: Category: News", … */
export function ruleLabel(site: Site, rule: string, idLabel?: string): string {
  const row = parseRule(rule)
  if (!row) return rule
  const what = describe(site, row, idLabel)
  return row.mode === 'exclude' ? t('Excluded: {what}', { what }) : what
}

function describe(site: Site, row: ConditionRow, idLabel?: string): string {
  const item = idLabel ? `“${idLabel}”` : `#${row.id}`
  if (row.group === 'general') return t('Entire Site')
  if (row.group === '404') return t('404 Page')
  if (row.group === 'user') {
    if (row.sub === 'logged_in') return t('Logged-in Users')
    if (row.sub === 'logged_out') return t('Logged-out Visitors')
    const slug = row.sub.replace(/^role\//, '')
    return t('Role: {role}', { role: site.roles?.find((r) => r.slug === slug)?.label || slug })
  }
  if (row.group === 'singular') {
    if (!row.sub) return t('All Singular')
    if (row.sub === 'front_page') return t('Front Page')
    const type = typeLabel(site, row.sub)
    if (!row.target) return type
    if (row.target === 'id') return `${type}: ${item}`
    if (row.target === 'child_of') return t('{type}: child of {item}', { type, item })
    if (row.target === 'by_author') return t('{type} by {item}', { type, item })
    return t('{type} in {taxonomy}: {item}', { type, taxonomy: taxLabel(site, row.target.slice(3)), item })
  }
  if (!row.sub) return t('All Archives')
  if (row.sub === 'home') return t('Posts Page')
  if (row.sub === 'date') return t('Date Archives')
  if (row.sub === 'search') return t('Search Results')
  if (row.sub.startsWith('type/')) return t('{type} Archive', { type: typeLabel(site, row.sub.slice(5)) })
  const what = row.sub === 'author' ? t('Author Archives') : taxLabel(site, row.sub)
  return row.target === 'id' ? `${what}: ${item}` : what
}

/* ---------- Errors from the server (422 wb_bad_condition) ---------- */

/** Maps ValidationError paths (/conditions/<i>, /priority) to the rule index, or 'priority'. */
export function serverErrorIndex(path: string): number | 'priority' | null {
  if (path === '/priority') return 'priority'
  const m = /^\/conditions\/(\d+)$/.exec(path)
  return m ? Number(m[1]) : null
}
