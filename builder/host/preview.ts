/**
 * "Preview as" model (contract §11.6 preview objects): which kinds of preview a template type
 * offers, and object string <-> picker state. PreviewPicker.tsx draws it; the change goes through
 * TemplateController.setPreview (PUT templates/{id}/preview).
 */
import type { IdSearchKind, Option } from './conditions'
import type { PreviewObject, SiteBoot, TemplateType } from './contract'
import { t } from './i18n'

/** Picker kinds; 'page' and 'entry' are post:<id> objects searched in another collection. */
export type PreviewKind = 'latest' | 'front' | 'home' | 'post' | 'page' | 'entry' | 'category' | 'post_tag' | 'author' | 'type' | 'search' | '404'

export interface PreviewChoice {
  kind: PreviewKind
  /** id for post / page / entry / category / post_tag / author */
  id: string
  /** post type for 'type', query for 'search' */
  text: string
}

const ALL: PreviewKind[] = ['front', 'home', 'post', 'page', 'entry', 'category', 'post_tag', 'author', 'type', 'search', '404']

/** The kinds offered for a template type (header and footer show on every view). */
export function previewKinds(type: TemplateType, site: Pick<SiteBoot, 'postTypes'>): PreviewKind[] {
  const custom = site.postTypes.some((p) => p.slug !== 'post' && p.slug !== 'page')
  const archives = site.postTypes.some((p) => !!p.archive)
  let kinds: PreviewKind[]
  switch (type) {
    case 'single-post':
      kinds = ['latest', 'post']
      break
    case 'single-page':
      kinds = ['latest', 'front', 'page']
      break
    case 'single':
      kinds = ['latest', 'entry']
      break
    case 'front-page':
      kinds = ['front', 'home', 'page']
      break
    case 'archive':
      kinds = ['home', 'category', 'post_tag', 'author', 'type', 'search']
      break
    case 'search-results':
      kinds = ['search']
      break
    case 'error-404':
      kinds = ['404']
      break
    default:
      kinds = ALL
  }
  return kinds.filter((k) => (k !== 'entry' || custom) && (k !== 'type' || archives))
}

export function kindLabel(k: PreviewKind): string {
  switch (k) {
    case 'latest':
      return t('Latest entry')
    case 'front':
      return t('Front page')
    case 'home':
      return t('Posts page')
    case 'post':
      return t('A post')
    case 'page':
      return t('A page')
    case 'entry':
      return t('An entry (by ID)')
    case 'category':
      return t('A category')
    case 'post_tag':
      return t('A tag')
    case 'author':
      return t('An author')
    case 'type':
      return t('A post type archive')
    case 'search':
      return t('Search results')
    case '404':
      return t('404 page')
  }
}

export const kindOptions = (kinds: PreviewKind[]): Option[] => kinds.map((k) => ({ value: k, label: kindLabel(k) }))

/** Whether the kind needs an id, and where it is searched (null = typed in). */
export function kindIdSource(k: PreviewKind): IdSearchKind | null | undefined {
  switch (k) {
    case 'post':
      return 'posts'
    case 'page':
      return 'pages'
    case 'category':
      return 'categories'
    case 'post_tag':
      return 'tags'
    case 'author':
      return 'users'
    case 'entry':
      return null
    default:
      return undefined // no id
  }
}

const ID_RE = /^[1-9][0-9]{0,18}$/

/** Picker state of a stored object, within the kinds offered (else the first kind). */
export function parsePreview(object: PreviewObject, kinds: PreviewKind[]): PreviewChoice {
  const pick = (kind: PreviewKind, id = '', text = ''): PreviewChoice => (kinds.includes(kind) ? { kind, id, text } : { kind: kinds[0] ?? 'front', id: '', text: '' })
  if (object === 'latest' || object === 'front' || object === 'home' || object === '404') return pick(object)
  const [kind, ...rest] = object.split(':')
  const value = rest.join(':')
  if (kind === 'post') {
    const k = (['post', 'page', 'entry'] as PreviewKind[]).find((x) => kinds.includes(x)) ?? 'post'
    return pick(k, value)
  }
  if (kind === 'term' && (rest[0] === 'category' || rest[0] === 'post_tag')) return pick(rest[0], rest[1] ?? '')
  if (kind === 'author') return pick('author', value)
  if (kind === 'type') return pick('type', '', value)
  if (kind === 'search') return pick('search', '', value)
  return pick(kinds[0] ?? 'front')
}

/** The object for a picker state; null while it is incomplete. */
export function previewObject(c: PreviewChoice): PreviewObject | null {
  switch (c.kind) {
    case 'latest':
    case 'front':
    case 'home':
    case '404':
      return c.kind
    case 'post':
    case 'page':
    case 'entry':
      return ID_RE.test(c.id) ? `post:${c.id}` : null
    case 'category':
    case 'post_tag':
      return ID_RE.test(c.id) ? `term:${c.kind}:${c.id}` : null
    case 'author':
      return ID_RE.test(c.id) ? `author:${c.id}` : null
    case 'type':
      return /^[a-z0-9_-]{1,32}$/.test(c.text) ? `type:${c.text}` : null
    case 'search': {
      const q = c.text.trim()
      return q && [...q].length <= 100 ? `search:${q}` : null
    }
  }
}
