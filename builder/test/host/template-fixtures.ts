import type { Boot, DynContext, SiteBoot, TemplateBoot } from '../../host/contract'
import { makeBoot } from './helpers'

export const SITE: SiteBoot = {
  title: 'Example',
  tagline: 'Just another site',
  home: 'https://example.test',
  logo: null,
  locations: [
    { slug: 'primary', name: 'Primary Menu' },
    { slug: 'footer', name: 'Footer Menu' },
  ],
  menus: {},
  themeParts: true,
  postTypes: [
    { slug: 'post', label: 'Posts', archive: 'https://example.test/blog/', hierarchical: false },
    { slug: 'page', label: 'Pages', archive: '', hierarchical: true },
    { slug: 'book', label: 'Books', archive: 'https://example.test/books/', hierarchical: false },
  ],
  taxonomies: [
    { slug: 'category', label: 'Categories', types: ['post'] },
    { slug: 'post_tag', label: 'Tags', types: ['post'] },
  ],
}

export function makeDyn(object: string, title = 'Hello world'): DynContext {
  const post = {
    id: 5,
    type: 'post',
    title,
    url: 'https://example.test/hello/',
    excerpt: 'Excerpt',
    contentHtml: '<p>Body</p>',
    date: '2026-11-01T10:00:00Z',
    dateText: 'November 1, 2026',
    modified: '2026-11-01T10:00:00Z',
    modifiedText: 'November 1, 2026',
    author: { name: 'Ada', url: 'https://example.test/author/ada/', avatar: '', bio: '' },
    image: null,
    terms: {},
    commentsCount: 0,
    commentsUrl: 'https://example.test/hello/#comments',
  }
  return {
    object,
    site: { title: 'Example', tagline: '', home: 'https://example.test', logo: null, year: '2026' },
    locations: SITE.locations,
    menus: { primary: [{ id: 1, label: 'Home', url: 'https://example.test/', current: true, children: [] }] },
    view: { kind: 'single', archiveTitle: '', archiveDesc: '', search: '', total: 1 },
    post,
    posts: [post],
    pagination: { current: 1, total: 1 },
  }
}

export function makeTemplate(over: Partial<TemplateBoot> = {}): TemplateBoot {
  return {
    type: 'single-post',
    conditions: ['include/singular/post'],
    priority: 0,
    starter: 'single',
    preview: { object: 'latest', label: 'Post: Hello world', url: 'https://example.test/hello/?wb_template_preview=42' },
    manageUrl: 'https://example.test/wp-admin/themes?page=web-builder-templates',
    ...over,
  }
}

/** A wb_template boot: never published (draft, rev 0) unless `doc` says otherwise. */
export function makeTemplateBoot(tpl: Partial<TemplateBoot> = {}, doc: Parameters<typeof makeBoot>[0] = {}, over: Partial<Boot> = {}): Boot {
  return makeBoot(
    { postType: 'wb_template', status: 'draft', rev: 0, doc: null, page: { title: 'Blog post', slug: '', description: '', indexable: false }, ...doc },
    { template: makeTemplate(tpl), site: SITE, dynamic: makeDyn('latest'), ...over },
  )
}
